//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// isProcessAlive returns true if the process with the given PID is currently active.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil || err == syscall.EPERM {
		return true
	}
	return false
}

// killProcessGraceful sends SIGTERM to the process.
func killProcessGraceful(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

// killProcessForce sends SIGKILL to the process.
func killProcessForce(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// setDaemonSysProcAttr sets POSIX session detachment attributes.
func setDaemonSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach process from terminal session
	}
}
