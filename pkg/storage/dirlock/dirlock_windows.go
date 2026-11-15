//go:build windows

package dirlock

import (
	"errors"
	"os"
	"syscall"
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

func lockOSFile(f *os.File) error {
	ol := new(syscall.Overlapped)
	err := syscall.LockFileEx(
		syscall.Handle(f.Fd()),
		lockfileExclusiveLock|lockfileFailImmediately,
		0,
		1, 0,
		ol,
	)
	if err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) {
			// ERROR_SHARING_VIOLATION = 32, ERROR_LOCK_VIOLATION = 33
			if errno == 32 || errno == 33 {
				return ErrDirectoryLocked
			}
		}
		return err
	}
	return nil
}

func unlockOSFile(f *os.File) error {
	ol := new(syscall.Overlapped)
	return syscall.UnlockFileEx(
		syscall.Handle(f.Fd()),
		0,
		1, 0,
		ol,
	)
}
