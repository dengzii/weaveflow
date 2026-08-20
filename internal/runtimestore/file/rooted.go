package file

import (
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
