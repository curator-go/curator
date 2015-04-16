package curator

import (
	"github.com/samuel/go-zookeeper/zk"
)

type Pathable /*[T]*/ interface {
	// Commit the currently building operation using the given path
	ForPath(path string) (interface{} /* T */, error)
}

type PathAndBytesable /*[T]*/ interface {
	Pathable /*[T]*/

	// Commit the currently building operation using the given path and data
	ForPathWithData(path string, payload []byte) (interface{} /* T */, error)
}

type Compressible /*[T]*/ interface {
	// Cause the data to be compressed using the configured compression provider
	Compressed() interface{} // T
}

type Decompressible /*[T]*/ interface {
	// Cause the data to be de-compressed using the configured compression provider
	Decompressed() interface{} // T
}

type CreateMode int32

const (
	PERSISTENT            CreateMode = 0
	PERSISTENT_SEQUENTIAL            = zk.FlagSequence
	EPHEMERAL                        = zk.FlagEphemeral
	EPHEMERAL_SEQUENTIAL             = zk.FlagEphemeral + zk.FlagSequence
)

func (m CreateMode) IsSequential() bool { return (m & zk.FlagSequence) == zk.FlagSequence }
func (m CreateMode) IsEphemeral() bool  { return (m & zk.FlagEphemeral) == zk.FlagEphemeral }

type CreateModable /*[T]*/ interface {
	// Set a create mode - the default is CreateMode.PERSISTENT
	WithMode(mode CreateMode) interface{} // T
}

type ACLable /*[T]*/ interface {
	// Set an ACL list
	WithACL(acl ...zk.ACL) interface{} // T
}

type Versionable /*[T]*/ interface {
	// Use the given version (the default is -1)
	WithVersion(version int) interface{} // T
}

type Statable /*[T]*/ interface {
	// Have the operation fill the provided stat object
	StoringStatIn(*zk.Stat) interface{}
}

type Watchable /*[T]*/ interface {
	// Have the operation set a watch
	Watched() interface{} // T

	// Set a watcher for the operation
	UsingWatcher(watcher Watcher) interface{} // T
}

// Called when the async background operation completes
type BackgroundCallback func(client CuratorFramework, event CuratorEvent) error

type Backgroundable /*[T]*/ interface {
	// Perform the action in the background
	InBackground() interface{} // T

	// Perform the action in the background
	InBackgroundWithContext(context interface{}) interface{} // T

	// Perform the action in the background
	InBackgroundWithCallback(callback BackgroundCallback) interface{} // T

	// Perform the action in the background
	InBackgroundWithCallbackAndContext(callback BackgroundCallback, context interface{}) interface{} // T

	// Perform the action in the background
	InBackgroundWithCallbackAndExecutor(callback BackgroundCallback, executor Executor) interface{} // T
}
