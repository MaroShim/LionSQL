package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func main() {
	fmt.Fprintln(os.Stderr, "[LionSQL Notice] 'bin/server' is deprecated and will be removed in LionSQL 1.1; please use 'bin/lionsql' instead.")

	// 1. Locate lionsql executable
	targetExe := findLionsqlExecutable()
	if targetExe == "" {
		fmt.Fprintln(os.Stderr, "[LionSQL Error] Unable to locate 'lionsql' binary in PATH or adjacent to 'server'.")
		os.Exit(1)
	}

	// 2. Delegate execution with all arguments (Golden Rule 3: Direct argument vector execution)
	args := append([]string{targetExe}, os.Args[1:]...)
	env := os.Environ()
	if err := syscall.Exec(targetExe, args, env); err != nil {
		// Fallback for non-execve or environments where syscall.Exec fails
		cmd := exec.Command(targetExe, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if runErr := cmd.Run(); runErr != nil {
			if exitErr, ok := runErr.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
	}
}

func findLionsqlExecutable() string {
	// 1. Check sibling in same directory as current executable (e.g. bin/server -> bin/lionsql)
	if selfExe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(selfExe), "lionsql")
		if fi, err := os.Stat(sibling); err == nil && !fi.IsDir() {
			return sibling
		}
	}

	// 2. Check ./bin/lionsql relative to current working directory
	if fi, err := os.Stat(filepath.Join("bin", "lionsql")); err == nil && !fi.IsDir() {
		return filepath.Join("bin", "lionsql")
	}

	// 3. Check ./lionsql relative to cwd
	if fi, err := os.Stat("lionsql"); err == nil && !fi.IsDir() {
		return "lionsql"
	}

	// 4. Look up in system PATH
	if path, err := exec.LookPath("lionsql"); err == nil {
		return path
	}

	return ""
}
