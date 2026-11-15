//go:build !windows

package dirlock

import (
	"errors"
	"os"
	"syscall"
)

func lockOSFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EBUSY) {
			return ErrDirectoryLocked
		}
		return err
	}
	return nil
}

func unlockOSFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
