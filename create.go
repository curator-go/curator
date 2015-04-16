package curator

import (
	"fmt"

	"github.com/samuel/go-zookeeper/zk"
)

type createBuilder struct {
	client                *curatorFramework
	createMode            CreateMode
	backgrounding         backgrounding
	createParentsIfNeeded bool
	compress              bool
	acling                acling
}

func (b *createBuilder) ForPath(path string) (string, error) {
	return b.ForPathWithData(path, b.client.defaultData)
}

func (b *createBuilder) ForPathWithData(givenPath string, payload []byte) (string, error) {
	if b.compress {
		if data, err := b.client.compressionProvider.Compress(givenPath, payload); err != nil {
			return "", err
		} else {
			payload = data
		}
	}

	adjustedPath := b.client.fixForNamespace(givenPath, b.createMode.IsSequential())

	if b.backgrounding.inBackground {
		go b.pathInBackground(adjustedPath, payload, givenPath)

		return b.client.unfixForNamespace(adjustedPath), nil
	} else {
		path, err := b.pathInForeground(adjustedPath, payload)

		return b.client.unfixForNamespace(path), err
	}
}

func (b *createBuilder) pathInBackground(path string, payload []byte, givenPath string) {
	tracer := b.client.ZookeeperClient().startTracer("createBuilder.pathInBackground")

	defer tracer.Commit()

	createdPath, err := b.pathInForeground(path, payload)

	if b.backgrounding.callback != nil {
		event := &curatorEvent{
			eventType: CREATE,
			err:       err,
			path:      createdPath,
			data:      payload,
			context:   b.backgrounding.context,
		}

		if err != nil {
			event.path = givenPath
		}

		event.name = GetNodeFromPath(createdPath)

		b.backgrounding.callback(b.client, event)
	}
}

func (b *createBuilder) pathInForeground(path string, payload []byte) (string, error) {
	zkClient := b.client.ZookeeperClient()

	result, err := zkClient.newRetryLoop().CallWithRetry(func() (interface{}, error) {
		if conn, err := zkClient.Conn(); err != nil {
			return nil, err
		} else {
			result, err := conn.Create(path, payload, int32(b.createMode), b.acling.aclList)

			if err == zk.ErrNoNode && b.createParentsIfNeeded {
				MakeDirs(conn, path, false, b.acling.aclProvider)

				return conn.Create(path, payload, int32(b.createMode), b.acling.aclList)
			} else {
				return result, err
			}
		}
	})

	if err != nil {
		return "", err
	} else if createdPath, ok := result.(string); !ok {
		return "", fmt.Errorf("fail to convert result to string, %s", result)
	} else {
		return createdPath, nil
	}
}

func (b *createBuilder) CreatingParentsIfNeeded() CreateBuilder {
	b.createParentsIfNeeded = true

	return b
}

func (b *createBuilder) WithMode(mode CreateMode) CreateBuilder {
	b.createMode = mode

	return b
}

func (b *createBuilder) WithACL(acls ...zk.ACL) CreateBuilder {
	b.acling = acling{aclList: acls, aclProvider: b.client.aclProvider}

	return b
}

func (b *createBuilder) Compressed() CreateBuilder {
	b.compress = true

	return b
}

func (b *createBuilder) InBackground() CreateBuilder {
	b.backgrounding = backgrounding{inBackground: true}

	return b
}

func (b *createBuilder) InBackgroundWithContext(context interface{}) CreateBuilder {
	b.backgrounding = backgrounding{inBackground: true, context: context}

	return b
}

func (b *createBuilder) InBackgroundWithCallback(callback BackgroundCallback) CreateBuilder {
	b.backgrounding = backgrounding{inBackground: true, callback: callback}

	return b
}

func (b *createBuilder) InBackgroundWithCallbackAndContext(callback BackgroundCallback, context interface{}) CreateBuilder {
	b.backgrounding = backgrounding{inBackground: true, context: context, callback: callback}

	return b
}
