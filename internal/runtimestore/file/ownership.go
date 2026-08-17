package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var ErrWriterLocked = errors.New("file runtime store already has a writer")
var ErrWriterClosed = errors.New("file runtime store writer is closed")

type writerState struct {
	closed bool
}

func requireWritable(state *writerState) error {
	if state == nil {
		return errors.New("file runtime store is read-only")
	}
	if state.closed {
		return ErrWriterClosed
	}
	return nil
}

type writerLock struct {
	file *os.File
	once sync.Once
	err  error
}

func acquireWriterLock(baseDir string) (*writerLock, error) {
	if err := ensureRunnerDirectory(baseDir); err != nil {
		return nil, err
	}
	if err := os.Chmod(baseDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(baseDir, ".writer.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := lockWriterFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errWriterLockUnavailable) {
			return nil, fmt.Errorf("%w: %s", ErrWriterLocked, baseDir)
		}
		return nil, err
	}
	return &writerLock{file: file}, nil
}

func (lock *writerLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.once.Do(func() {
		lock.err = errors.Join(unlockWriterFile(lock.file), lock.file.Close())
	})
	return lock.err
}
