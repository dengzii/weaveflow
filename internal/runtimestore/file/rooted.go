package file

import (
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
)

func runnerRootedPath(path string) (*os.Root, string, error) {
	cleanPath := filepath.Clean(path)
	root, err := os.OpenRoot(filepath.Dir(cleanPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", &os.PathError{Op: "open", Path: cleanPath, Err: fs.ErrNotExist}
		}
		return nil, "", err
	}
	return root, filepath.Base(cleanPath), nil
}

func runnerRootedReadFile(path string) ([]byte, error) {
	root, name, err := runnerRootedPath(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(name)
}

func runnerRootedReadDir(path string) ([]os.DirEntry, error) {
	root, name, err := runnerRootedPath(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	return directory.ReadDir(-1)
}

func runnerRootedCreateTemp(directory, prefix string) (*os.File, string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = root.Close() }()
	for attempt := 0; attempt < 10; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := prefix + hex.EncodeToString(random[:]) + ".tmp"
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, filepath.Join(directory, name), nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", os.ErrExist
}

func runnerRootedStat(path string) (os.FileInfo, error) {
	root, name, err := runnerRootedPath(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Stat(name)
}

func runnerRootedRemove(path string) error {
	root, name, err := runnerRootedPath(path)
	if err != nil {
		if os.IsNotExist(err) {
			return os.ErrNotExist
		}
		return err
	}
	defer func() { _ = root.Close() }()
	return root.Remove(name)
}

func runnerRootedRemoveAll(path string) error {
	root, name, err := runnerRootedPath(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = root.Close() }()
	err = root.RemoveAll(name)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func runnerRootedMkdirAll(path string, mode os.FileMode) error {
	cleanPath := filepath.Clean(path)
	root, name, err := runnerRootedPath(cleanPath)
	if err != nil {
		parent := filepath.Dir(cleanPath)
		if parent == cleanPath {
			return err
		}
		if err := runnerRootedMkdirAll(parent, mode); err != nil {
			return err
		}
		root, name, err = runnerRootedPath(cleanPath)
		if err != nil {
			return err
		}
	}
	defer func() { _ = root.Close() }()
	return root.MkdirAll(name, mode)
}
