package lock

import (
	"context"
	"net/url"
	"path/filepath"
	"time"

	"github.com/mosuka/phalanx/clients"
	"github.com/mosuka/phalanx/errors"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"go.uber.org/zap"
)

type EtcdLockManager struct {
	client *clientv3.Client
	path   string
	logger *zap.Logger
	ctx    context.Context
	mutex  *concurrency.Mutex
}

func NewEtcdLockManagerWithUri(uri string, logger *zap.Logger) (*EtcdLockManager, error) {
	lockManagerLogger := logger.Named("etcd")

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

	return &EtcdLockManager{
		client: client,
		path:   filepath.Join("/", u.Host, u.Path),
		logger: lockManagerLogger,
		ctx:    context.Background(),
		mutex:  nil,
	}, nil
}

func (m *EtcdLockManager) Lock() (int64, error) {
	if m.mutex == nil {
		var err error
		session, err := concurrency.NewSession(m.client) // without TTL
		if err != nil {
			m.logger.Error("failed to create session", zap.Error(err), zap.String("path", m.path))
			return 0, err
		}
		m.mutex = concurrency.NewMutex(session, m.path)
	}

	requestTimeout := 3 * time.Second
	ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
	defer cancel()

	if err := m.mutex.Lock(ctx); err != nil {
		m.logger.Error("failed to lock", zap.Error(err), zap.String("path", m.path))
		return 0, err
	}

	m.logger.Info("locked", zap.String("path", m.path))

	return m.mutex.Header().Revision, nil
}

func (m *EtcdLockManager) Unlock() error {
	if m.mutex == nil {
		err := errors.ErrLockDoesNotExists
		m.logger.Error("lock not held", zap.Error(err), zap.String("path", m.path))
		return err
	}

	requestTimeout := 3 * time.Second
	ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
	defer cancel()

	if err := m.mutex.Unlock(ctx); err != nil {
		m.logger.Error("failed to unlock", zap.Error(err), zap.String("path", m.path))
		return err
	}

	m.logger.Info("unlocked", zap.String("path", m.path))

	return nil
}
