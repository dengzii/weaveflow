//go:build !windows

package file

import "os"

func replaceFile(source, target string) error {
	if err := validateRunnerPath(source); err != nil {
		return err
	}
	if err := validateRunnerPath(target); err != nil {
		return err
	}
	return os.Rename(source, target)
}

func syncDirectory(path string) error {
	if err := validateRunnerPath(path); err != nil {
		return err
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
