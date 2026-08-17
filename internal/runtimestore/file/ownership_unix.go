//go:build !windows

package file

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errWriterLockUnavailable = errors.New("writer lock unavailable")

func lockWriterFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return errWriterLockUnavailable
	}
	return err
}

func unlockWriterFile(file *os.File) error {
	if file == nil {
		return nil
	}
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
