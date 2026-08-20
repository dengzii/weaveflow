//go:build !windows

package file

import (
	"os"
	"path/filepath"
)

func replaceFile(source, target string) error {
	if err := validateRunnerPath(source); err != nil {
		return err
	}
	if err := validateRunnerPath(target); err != nil {
		return err
	}
	if filepath.Dir(source) != filepath.Dir(target) {
		return os.ErrInvalid
	}
	root, err := os.OpenRoot(filepath.Dir(source))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.Rename(filepath.Base(source), filepath.Base(target))
}

func syncDirectory(path string) error {
	if err := validateRunnerPath(path); err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
