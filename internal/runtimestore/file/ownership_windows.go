//go:build windows

package file

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var errWriterLockUnavailable = errors.New("writer lock unavailable")

func lockWriterFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errWriterLockUnavailable
	}
	return err
}

func unlockWriterFile(file *os.File) error {
	if file == nil {
		return nil
	}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, new(windows.Overlapped))
}
