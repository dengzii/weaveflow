package trigger

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

func rootedPath(path string) (*os.Root, string, error) {
	cleanPath := filepath.Clean(path)
	root, err := os.OpenRoot(filepath.Dir(cleanPath))
	if err != nil {
		return nil, "", err
	}
	return root, filepath.Base(cleanPath), nil
}

func rootedReadFile(path string) ([]byte, error) {
	root, name, err := rootedPath(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(name)
}

func rootedReadDir(path string) ([]os.DirEntry, error) {
	root, name, err := rootedPath(path)
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

func rootedCreateTemp(directory, prefix string) (*os.File, string, error) {
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

func rootedWriteFile(path string, data []byte, mode os.FileMode) error {
	root, name, err := rootedPath(path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.WriteFile(name, data, mode)
}

func rootedStat(path string) (os.FileInfo, error) {
	root, name, err := rootedPath(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Stat(name)
}

func rootedRemove(path string) error {
	root, name, err := rootedPath(path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.Remove(name)
}

func rootedRemoveAll(path string) error {
	root, name, err := rootedPath(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = root.Close() }()
	return root.RemoveAll(name)
}

func rootedMkdirAll(path string, mode os.FileMode) error {
	root, name, err := rootedPath(path)
	if err != nil {
		parent := filepath.Dir(filepath.Clean(path))
		if parent == filepath.Clean(path) {
			return err
		}
		if err := rootedMkdirAll(parent, mode); err != nil {
			return err
		}
		root, name, err = rootedPath(path)
		if err != nil {
			return err
		}
	}
	defer func() { _ = root.Close() }()
	return root.MkdirAll(name, mode)
}

func rootedChmod(path string, mode os.FileMode) error {
	root, name, err := rootedPath(path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.Chmod(name, mode)
}

func rootedRename(source, target string) error {
	root, sourceName, err := rootedPath(source)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if filepath.Dir(filepath.Clean(source)) != filepath.Dir(filepath.Clean(target)) {
		return os.ErrInvalid
	}
	return root.Rename(sourceName, filepath.Base(filepath.Clean(target)))
}
