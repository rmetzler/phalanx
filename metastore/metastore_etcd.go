package metastore

import (
	"context"
	"net/url"
	"path/filepath"
	"time"

	"github.com/mosuka/phalanx/clients"
	"github.com/mosuka/phalanx/errors"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

type EtcdMetastore struct {
	client         *clientv3.Client
	kv             clientv3.KV
	root           string
	logger         *zap.Logger
	ctx            context.Context
	stopWatching   chan bool
	events         chan MetastoreEvent
	requestTimeout time.Duration
}

func NewEtcdMetastoreWithUri(uri string, logger *zap.Logger) (*EtcdMetastore, error) {
	metastorelogger := logger.Named("etcd")

	client, err := clients.NewEtcdClientWithUri(uri)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	if u.Scheme != SchemeType_name[SchemeTypeEtcd] {
		return nil, errors.ErrInvalidUri
	}

	root := filepath.ToSlash(filepath.Join(string(filepath.Separator), u.Host, u.Path))

	return &EtcdMetastore{
		client:         client,
		kv:             clientv3.NewKV(client),
		root:           root,
		logger:         metastorelogger,
		ctx:            context.Background(),
		stopWatching:   make(chan bool),
		events:         make(chan MetastoreEvent, 10),
		requestTimeout: 3 * time.Second,
	}, nil
}

func (m *EtcdMetastore) convertMetastoreEvent(event *clientv3.Event) (*MetastoreEvent, error) {
	switch {
	case event.Type == mvccpb.PUT:
		return &MetastoreEvent{
			Type:  EventTypePut,
			Path:  string(event.Kv.Key),
			Value: event.Kv.Value,
		}, nil
	case event.Type == mvccpb.DELETE:
		return &MetastoreEvent{
			Type:  EventTypeDelete,
			Path:  string(event.Kv.Key),
			Value: event.Kv.Value,
		}, nil
	default:
		return nil, errors.ErrUnsupportedMetastoreEvent
	}
}

// Replace the path separator with '/'.
func (m *EtcdMetastore) makePath(path string) string {
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(m.root), path))
}

func (m *EtcdMetastore) Get(path string) ([]byte, error) {
	fullPath := m.makePath(path)

	ctx, cancel := context.WithTimeout(m.ctx, m.requestTimeout)
	defer cancel()

	resp, err := m.kv.Get(ctx, fullPath)
	if err != nil {
		m.logger.Error("failed to get key", zap.Error(err), zap.String("key", fullPath))
		return nil, err
	}

	if resp.Count > 0 {
		return resp.Kvs[0].Value, nil
	} else {
		return nil, errors.ErrIndexMetadataDoesNotExist
	}
}

func (m *EtcdMetastore) List(prefix string) ([]string, error) {
	prefixPath := m.makePath(prefix)

	ctx, cancel := context.WithTimeout(m.ctx, m.requestTimeout)
	defer cancel()

	opts := []clientv3.OpOption{
		clientv3.WithPrefix(),
		clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
	}

	resp, err := m.kv.Get(ctx, prefixPath, opts...)
	if err != nil {
		m.logger.Error("failed to get keys", zap.Error(err), zap.String("key", prefixPath))
		return nil, err
	}

	paths := make([]string, 0)
	for _, kv := range resp.Kvs {
		// Remove prefixPath.
		// E.g. /tmp/phalanx179449480/hello.txt to /hello.txt
		path := string(kv.Key)
		paths = append(paths, path[len(prefixPath):])
	}

	return paths, nil
}

func (m *EtcdMetastore) Put(path string, content []byte) error {
	fullPath := m.makePath(path)

	ctx, cancel := context.WithTimeout(m.ctx, m.requestTimeout)
	defer cancel()

	if _, err := m.kv.Put(ctx, fullPath, string(content)); err != nil {
		m.logger.Error("failed to put key-value", zap.Error(err), zap.String("key", fullPath))
		return err
	}

	return nil
}

func (m *EtcdMetastore) Delete(path string) error {
	fullPath := m.makePath(path)

	ctx, cancel := context.WithTimeout(m.ctx, m.requestTimeout)
	defer cancel()

	if _, err := m.kv.Delete(ctx, fullPath); err != nil {
		m.logger.Error("failed to delete key", zap.Error(err), zap.String("key", fullPath))
		return err
	}

	return nil
}

func (m *EtcdMetastore) Exists(path string) (bool, error) {
	fullPath := m.makePath(path)

	ctx, cancel := context.WithTimeout(m.ctx, m.requestTimeout)
	defer cancel()

	resp, err := m.kv.Get(ctx, fullPath)
	if err != nil {
		m.logger.Error("failed to get key", zap.Error(err), zap.String("key", fullPath))
		return false, err
	}

	if resp.Count > 0 {
		return true, nil
	} else {
		return false, nil
	}
}

func (m *EtcdMetastore) Start() error {
	go func() {
		watchPath := m.root + "/"
		opts := []clientv3.OpOption{
			clientv3.WithFromKey(),
		}
		watchChan := m.client.Watch(m.ctx, watchPath, opts...)
		for {
			select {
			case cancel := <-m.stopWatching:
				// check
				if cancel {
					return
				}
			case result := <-watchChan:
				for _, event := range result.Events {
					metastoreEvent, err := m.convertMetastoreEvent(event)
					if err != nil {
						m.logger.Error("failed to convert event", zap.Error(err), zap.Any("event", event))
						continue
					}

					m.logger.Debug("received etcd event", zap.Any("path", metastoreEvent.Path), zap.String("type", EventType_name[metastoreEvent.Type]))

					m.events <- *metastoreEvent
				}
			}
		}
	}()

	return nil
}

func (m *EtcdMetastore) Stop() error {
	m.stopWatching <- true

	if err := m.client.Close(); err != nil {
		m.logger.Error("failed to close etcd client", zap.Error(err))
		return err
	}

	return nil
}

func (m *EtcdMetastore) Events() chan MetastoreEvent {
	return m.events
}
