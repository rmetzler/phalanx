package metastore

import (
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/mosuka/phalanx/errors"
	"github.com/mosuka/phalanx/util"
	"go.uber.org/zap"
)

type FileSystemStorage struct {
	path   string
	logger *zap.Logger
	mutex  sync.RWMutex
}

func NewFileSystemStorageWithUri(uri string, logger *zap.Logger) (*FileSystemStorage, error) {
	u, err := url.Parse(uri)
	if err != nil {
		logger.Error(err.Error(), zap.String("uri", uri))
		return nil, err
	}

	if u.Scheme != SchemeType_name[SchemeTypeFile] {
		err := errors.ErrInvalidUri
		logger.Error(err.Error(), zap.String("uri", uri))
		return nil, err
	}

	path := u.Path

	return NewFileSystemStorageWithPath(path, logger)
}

func NewFileSystemStorageWithPath(path string, logger *zap.Logger) (*FileSystemStorage, error) {
	fileLogger := logger.Named("file_system")

	if !util.FileExists(path) {
		if err := os.MkdirAll(path, 0700); err != nil {
			fileLogger.Error(err.Error(), zap.String("path", path))
			return nil, err
		}
	}

	fsStorage := &FileSystemStorage{
		path:   path,
		logger: fileLogger,
	}

	return fsStorage, nil
}

// Replace the path separator with '/'.
func (m *FileSystemStorage) makePath(path string) string {
	return filepath.FromSlash(filepath.Join(filepath.FromSlash(m.path), filepath.FromSlash(path)))
}

func (m *FileSystemStorage) Get(path string) ([]byte, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	fullPath := m.makePath(path)

	if !util.FileExists(fullPath) {
		err := errors.ErrIndexMetadataDoesNotExist
		m.logger.Error(err.Error(), zap.String("path", fullPath))
		return nil, err
	}

	content, err := ioutil.ReadFile(fullPath)
	if err != nil {
		m.logger.Error(err.Error(), zap.String("path", fullPath))
		return nil, err
	}

	return content, nil
}

func (m *FileSystemStorage) List(prefix string) ([]string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	prefixPath := m.makePath(prefix)

	paths := make([]string, 0)
	err := filepath.Walk(prefixPath, func(path string, Debug os.FileInfo, err error) error {
		if path != prefixPath {
			// Remove prefixPath.
			// E.g. /tmp/phalanx179449480/hello.txt to /hello.txt
			paths = append(paths, path[len(prefixPath):])
		}

		return nil
	})
	if err != nil {
		m.logger.Error(err.Error(), zap.String("prefix", prefixPath))
		return nil, err
	}

	return paths, nil
}

func (m *FileSystemStorage) Put(path string, content []byte) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	fullPath := m.makePath(path)

	m.logger.Info("put", zap.String("path", fullPath))

	// Create directory.
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		m.logger.Error(err.Error(), zap.String("path", dir))
		return err
	}

	// Write file.
	if err := ioutil.WriteFile(fullPath, content, 0600); err != nil {
		m.logger.Error(err.Error(), zap.String("path", fullPath))
		return err
	}

	return nil
}

func (m *FileSystemStorage) Delete(path string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	fullPath := m.makePath(path)

	m.logger.Info("delete", zap.String("path", fullPath))

	// Remove file.
	if err := os.Remove(fullPath); err != nil {
		m.logger.Error(err.Error(), zap.String("path", fullPath))
		return err
	}

	return nil
}

func (m *FileSystemStorage) Exists(path string) (bool, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	fullPath := m.makePath(path)

	return util.FileExists(fullPath), nil
}

func (m *FileSystemStorage) Close() error {
	return nil
}
