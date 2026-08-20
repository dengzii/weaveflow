//go:build windows

package file

import "golang.org/x/sys/windows"

func replaceFile(source, target string) error {
	if err := validateRunnerPath(source); err != nil {
		return err
	}
	if err := validateRunnerPath(target); err != nil {
		return err
	}
	sourcePath, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPath, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourcePath,
		targetPath,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func syncDirectory(string) error {
	return nil
}
