//go:build windows

package dirlock

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

func lockOSFile(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procLockFileEx.Call(
		f.Fd(),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		var errno syscall.Errno
		if errors.As(err, &errno) {
			// ERROR_SHARING_VIOLATION = 32, ERROR_LOCK_VIOLATION = 33
			if errno == 32 || errno == 33 {
				return ErrDirectoryLocked
			}
		}
		if err != nil {
			return err
		}
		return syscall.EINVAL
	}
	return nil
}

func unlockOSFile(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procUnlockFileEx.Call(
		f.Fd(),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		if err != nil {
			return err
		}
		return syscall.EINVAL
	}
	return nil
}
