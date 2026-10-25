//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	stillActive             = 259
	processQueryLimitedInfo = 0x1000
	processQueryInfo        = 0x0400
	createNewProcessGroup   = 0x00000200
	detachedProcess         = 0x00000008
)

var (
	modwinmm            = syscall.NewLazyDLL("winmm.dll")
	procTimeBeginPeriod = modwinmm.NewProc("timeBeginPeriod")
	procTimeEndPeriod   = modwinmm.NewProc("timeEndPeriod")
)

func init() {
	// Request 1ms timer resolution on Windows to prevent 15.6ms scheduler sleep quantums.
	// In Windows 10 (2004+) and Windows 11, timeBeginPeriod is per-process isolated.
	_ = timeBeginPeriod(1)
}

// timeBeginPeriod requests a minimum timer resolution for the current process.
func timeBeginPeriod(period uint32) error {
	if err := procTimeBeginPeriod.Find(); err != nil {
		return err
	}
	r, _, _ := procTimeBeginPeriod.Call(uintptr(period))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// timeEndPeriod clears a previously set minimum timer resolution.
func timeEndPeriod(period uint32) error {
	if err := procTimeEndPeriod.Find(); err != nil {
		return err
	}
	r, _, _ := procTimeEndPeriod.Call(uintptr(period))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// isProcessAlive returns true if the process with the given PID is currently active on Windows.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Try PROCESS_QUERY_LIMITED_INFORMATION first, fallback to PROCESS_QUERY_INFORMATION
	h, err := syscall.OpenProcess(processQueryLimitedInfo, false, uint32(pid))
	if err != nil {
		h, err = syscall.OpenProcess(processQueryInfo, false, uint32(pid))
		if err != nil {
			// If permission is denied, the process exists and is active under a different security token
			if err == syscall.ERROR_ACCESS_DENIED {
				return true
			}
			return false
		}
	}
	defer syscall.CloseHandle(h)

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}

// killProcessGraceful requests process termination on Windows.
func killProcessGraceful(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// killProcessForce forcefully terminates the process on Windows.
func killProcessForce(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// setDaemonSysProcAttr configures detached background execution on Windows.
func setDaemonSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
	}
}
