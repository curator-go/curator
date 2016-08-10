package cache

import (
	"errors"
	"path"
	"sort"
	"sync/atomic"

	"github.com/curator-go/curator"
	"github.com/samuel/go-zookeeper/zk"
)

// NodeState represents state of TreeNode.
// TODO: make this a private type?
type NodeState int32

// Available node states.
const (
	NodeStatePENDING NodeState = iota
	NodeStateLIVE
	NodeStateDEAD
)

// TreeNode represents a node in a tree of znodes.
type TreeNode struct {
	tree      *TreeCache
	state     NodeState
	parent    *TreeNode
	path      string
	childData *ChildData
	children  map[string]*TreeNode
	depth     int
}

// NewTreeNode creates a TreeNode with given path and parent.
// NOTE: parent should be nil if the node is root.
func NewTreeNode(tree *TreeCache, path string, parent *TreeNode) *TreeNode {
	depth := 0
	if parent != nil {
		depth = parent.depth + 1
	}
	return &TreeNode{
		tree:     tree,
		state:    NodeStatePENDING,
		parent:   parent,
		path:     path,
		children: make(map[string]*TreeNode),
		depth:    depth,
	}
}

func (tn *TreeNode) refresh() {
	if (tn.depth < tn.tree.maxDepth) && tn.tree.selector.TraverseChildren(tn.path) {
		atomic.AddUint64(&tn.tree.outstandingOps, 2)
		tn.doRefreshData()
		tn.doRefreshChildren()
	} else {
		tn.refreshData()
	}
}

func (tn *TreeNode) refreshChildren() {
	if (tn.depth < tn.tree.maxDepth) && tn.tree.selector.TraverseChildren(tn.path) {
		atomic.AddUint64(&tn.tree.outstandingOps, 1)
		tn.doRefreshChildren()
	}
}

func (tn *TreeNode) refreshData() {
	atomic.AddUint64(&tn.tree.outstandingOps, 1)
	tn.doRefreshData()
}

func (tn *TreeNode) doRefreshChildren() {
	tn.tree.client.GetChildren().UsingWatcher(
		curator.NewWatcher(tn.processWatchEvent),
	).InBackgroundWithCallback(tn.processResult).ForPath(tn.path)
}

func (tn *TreeNode) doRefreshData() {
	tn.tree.client.GetData().UsingWatcher(
		curator.NewWatcher(tn.processWatchEvent),
	).InBackgroundWithCallback(tn.processResult).ForPath(tn.path)
}

func (tn *TreeNode) wasReconnected() error {
	tn.refresh()
	for _, child := range tn.children {
		if err := child.wasReconnected(); err != nil {
			return err
		}
	}
	return nil
}

func (tn *TreeNode) wasCreated() {
	tn.refresh()
}

func (tn *TreeNode) wasDeleted() {
	oldChildData := tn.childData
	tn.childData = nil
	for _, child := range tn.children {
		child.wasDeleted()
	}

	if tn.tree.state.Value() == curator.STOPPED {
		return
	}

	oldState := NodeState(atomic.SwapInt32((*int32)(&tn.state), int32(NodeStateDEAD)))
	if oldState == NodeStateLIVE {
		tn.tree.publishEvent(TreeCacheEventNodeRemoved, oldChildData)
	}

	if tn.parent == nil {
		// Root node; use an exist query to watch for existence.
		tn.tree.client.CheckExists().UsingWatcher(
			curator.NewWatcher(tn.processWatchEvent),
		).InBackgroundWithCallback(tn.processResult).ForPath(tn.path)
	} else {
		// Remove from parent if we're currently a child
		for child := range tn.parent.children {
			delete(tn.parent.children, child)
		}
	}
}

// processWatchEvent processes watch events.
func (tn *TreeNode) processWatchEvent(evt *zk.Event) {
	tn.tree.logger.Debugf("ProcessWatchEvent: %v", evt)
	switch evt.Type {
	case zk.EventNodeCreated:
		if tn.parent != nil {
			tn.tree.handleException(errors.New("unexpected NodeCreated on non-root node"))
			return
		}
		tn.wasCreated()
	case zk.EventNodeChildrenChanged:
		tn.refreshChildren()
	case zk.EventNodeDataChanged:
		tn.refreshData()
	case zk.EventNodeDeleted:
		tn.wasDeleted()
	default:
		// Leave other type of events unhandled
		// tn.tree.logger.Printf("Event received: %v", evt)
	}
}

// processResult is a callback for every zk operation.
func (tn *TreeNode) processResult(client curator.CuratorFramework, evt curator.CuratorEvent) error {
	tn.tree.logger.Debugf("ProcessResult: %v", evt)
	newStat := evt.Stat()
	switch evt.Type() {
	case curator.EXISTS:
		if tn.parent != nil {
			tn.tree.handleException(errors.New("unexpected EXISTS on non-root node"))
		}
		if evt.Err() == nil {
			atomic.CompareAndSwapInt32(
				(*int32)(&tn.state),
				int32(NodeStateDEAD),
				int32(NodeStatePENDING))
			tn.wasCreated()
		}
	case curator.CHILDREN:
		switch evt.Err() {
		case zk.ErrNoNode:
			tn.wasDeleted()
		case nil:
			oldChildData := tn.childData
			if oldChildData != nil && oldChildData.Stat().Mzxid == newStat.Mzxid {
				// Only update stat if mzxid is same, otherwise we might obscure
				// GET_DATA event updates.
				tn.childData.SetStat(newStat)
			}

			if len(evt.Children()) == 0 {
				break
			}

			// Present new children in sorted order for test determinism.
			children := sort.StringSlice(evt.Children())
			sort.Sort(children)
			for _, child := range children {
				if accepted := tn.tree.selector.AcceptChild(path.Join(tn.path, child)); !accepted {
					continue
				}
				if _, exists := tn.children[child]; !exists {
					fullPath := path.Join(tn.path, child)
					node := NewTreeNode(tn.tree, fullPath, tn)
					tn.children[child] = node
					node.wasCreated()
				}
			}
		}
	case curator.GET_DATA:
		switch evt.Err() {
		case zk.ErrNoNode:
			tn.wasDeleted()
		case nil:
			newChildData := NewChildData(evt.Path(), newStat, evt.Data())
			oldChildData := tn.childData
			if tn.tree.cacheData {
				tn.childData = newChildData
			} else {
				tn.childData = NewChildData(evt.Path(), newStat, nil)
			}

			var added bool
			if tn.parent == nil {
				// We're the singleton root.
				added = NodeState(atomic.SwapInt32((*int32)(&tn.state),
					int32(NodeStateLIVE))) != NodeStateLIVE
			} else {
				added = atomic.CompareAndSwapInt32((*int32)(&tn.state),
					int32(NodeStatePENDING),
					int32(NodeStateLIVE))
				if !added {
					// Ordinary nodes are not allowed to transition from dead -> live;
					// make sure this isn't a delayed response that came in after death.
					if tn.state != NodeStateLIVE {
						return nil
					}
				}
			}

			if added {
				tn.tree.publishEvent(TreeCacheEventNodeAdded, newChildData)
			} else {
				if oldChildData == nil || oldChildData.Stat().Mzxid != newStat.Mzxid {
					tn.tree.publishEvent(TreeCacheEventNodeUpdated, newChildData)
				}
			}
		default:
			tn.tree.logger.Printf("Unknown GET_DATA event: %v", evt)
		}
	default:
		// An unknown event, probably an error of some sort like connection loss.
		tn.tree.logger.Printf("Unknown event: %v", evt)
		// Don't produce an initialized event on error; reconnect can fix this.
		atomic.AddUint64(&tn.tree.outstandingOps, ^uint64(0))

		return nil
	}

	// Decrease by 1
	if atomic.AddUint64(&tn.tree.outstandingOps, ^uint64(0)) == 0 {
		if !tn.tree.isInitialized.IsSet() {
			tn.tree.isInitialized.Set()
			tn.tree.publishEvent(TreeCacheEventInitialized, nil)
		}
	}
	return nil
}
