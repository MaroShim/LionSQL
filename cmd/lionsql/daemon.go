package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// handleDaemonCommands processes daemon control flags (-stop, -status, -restart, -daemon).
// Returns true if the main process should exit immediately.
func handleDaemonCommands(
	daemonFlag bool,
	stopFlag bool,
	statusFlag bool,
	restartFlag bool,
	dataDir string,
	pidFile string,
	logFile string,
	httpAddr string,
	pgAddr string,
	tdsAddr string,
	engineType string,
) bool {
	if pidFile == "" {
		pidFile = filepath.Join(dataDir, "lionsql.pid")
	}
	if logFile == "" {
		logFile = filepath.Join(dataDir, "lionsql.log")
	}

	// 1. Status Command
	if statusFlag {
		checkDaemonStatus(pidFile, logFile, httpAddr, pgAddr, tdsAddr)
		return true
	}

	// 2. Stop Command
	if stopFlag {
		stopDaemon(pidFile)
		return true
	}

	// 3. Restart Command
	if restartFlag {
		fmt.Println("[LionSQL] Restarting daemon...")
		stopDaemon(pidFile)
		time.Sleep(500 * time.Millisecond)
		startDaemon(pidFile, logFile, dataDir, httpAddr, pgAddr, tdsAddr, engineType)
		return true
	}

	// 4. Start Daemon Command
	if daemonFlag {
		startDaemon(pidFile, logFile, dataDir, httpAddr, pgAddr, tdsAddr, engineType)
		return true
	}

	return false
}

func readPidFile(pidFile string) (int, error) {
	data, err := os.ReadFile(filepath.Clean(pidFile))
	if err != nil {
		return 0, err
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("invalid pid format in %s: %w", pidFile, err)
	}
	return pid, nil
}

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

func checkDaemonStatus(pidFile, logFile, httpAddr, pgAddr, tdsAddr string) {
	pid, err := readPidFile(pidFile)
	if err != nil || !isProcessAlive(pid) {
		if err == nil {
			_ = os.Remove(pidFile) // Clean up stale pidfile
		}
		fmt.Println("[LionSQL] Daemon status: STOPPED (No active process found)")
		return
	}

	fmt.Println("============================================================")
	fmt.Println("  LionSQL Server Daemon Status")
	fmt.Println("============================================================")
	fmt.Println(" State:       RUNNING (Active)")
	fmt.Printf(" Process ID:  %d\n", pid)
	fmt.Printf(" PID File:    %s\n", pidFile)
	fmt.Printf(" Log File:    %s\n", logFile)
	if strings.Contains(httpAddr, ":") {
		fmt.Printf(" Web UI:      http://%s/_/\n", httpAddr)
	}
	if strings.Contains(pgAddr, ":") {
		fmt.Printf(" Postgres:    psql -h 127.0.0.1 -p %s\n", strings.Split(pgAddr, ":")[1])
	}
	if strings.Contains(tdsAddr, ":") {
		fmt.Printf(" TDS / MSSQL: sqlcmd -S 127.0.0.1,%s\n", strings.Split(tdsAddr, ":")[1])
	}
	fmt.Println("============================================================")
}

func stopDaemon(pidFile string) {
	pid, err := readPidFile(pidFile)
	if err != nil {
		fmt.Printf("[LionSQL] No running daemon found (could not read %s)\n", pidFile)
		return
	}

	if !isProcessAlive(pid) {
		_ = os.Remove(pidFile)
		fmt.Printf("[LionSQL] Daemon process %d was not running. Removed stale PID file.\n", pid)
		return
	}

	fmt.Printf("[LionSQL] Terminating daemon process %d...\n", pid)

	// 1. Send graceful SIGTERM directly via syscall
	_ = syscall.Kill(pid, syscall.SIGTERM)

	// 2. Poll for termination up to 3.0 seconds
	deadline := time.Now().Add(3000 * time.Millisecond)
	stopped := false
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			stopped = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 3. Escalate to SIGKILL if still alive after timeout (Golden Rule 4)
	if !stopped && isProcessAlive(pid) {
		fmt.Println("[LionSQL] Process did not terminate within timeout. Sending SIGKILL...")
		_ = syscall.Kill(pid, syscall.SIGKILL)
		for i := 0; i < 20; i++ {
			if !isProcessAlive(pid) {
				stopped = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	_ = os.Remove(pidFile)
	// Small settle time for kernel to release TCP ports and file locks
	time.Sleep(200 * time.Millisecond)
	fmt.Printf("[LionSQL] Daemon (PID %d) stopped successfully.\n", pid)
}

func startDaemon(pidFile, logFile, dataDir, httpAddr, pgAddr, tdsAddr, engineType string) {
	// Check if already running
	if pid, err := readPidFile(pidFile); err == nil && isProcessAlive(pid) {
		fmt.Printf("[LionSQL] Daemon is already running with PID %d.\n", pid)
		fmt.Println("Use 'lionsql restart' (or -restart) to restart or 'lionsql stop' (or -stop) to stop.")
		os.Exit(1)
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		fmt.Printf("[LionSQL] Error creating data directory %s: %v\n", dataDir, err)
		os.Exit(1)
	}

	// Build child args without daemon flags, adding child worker flag
	var childArgs []string
	for _, arg := range os.Args[1:] {
		if arg == "-d" || arg == "-daemon" || arg == "--daemon" ||
			arg == "-stop" || arg == "--stop" ||
			arg == "-status" || arg == "--status" ||
			arg == "-restart" || arg == "--restart" ||
			arg == "start" || arg == "stop" || arg == "status" || arg == "restart" {
			continue
		}
		childArgs = append(childArgs, arg)
	}
	childArgs = append(childArgs, "-is-child-worker=true")

	// Open log file (append mode)
	logFd, err := os.OpenFile(filepath.Clean(logFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Printf("[LionSQL] Failed to open log file %s: %v\n", logFile, err)
		os.Exit(1)
	}

	// Golden Rule 3: Execve Safety without raw shell
	cmd := exec.Command(os.Args[0], childArgs...)
	cmd.Stdout = logFd
	cmd.Stderr = logFd
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach process from terminal session
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("[LionSQL] Failed to launch daemon process: %v\n", err)
		os.Exit(1)
	}

	childPID := cmd.Process.Pid

	// Write PID file
	if err := os.WriteFile(filepath.Clean(pidFile), []byte(strconv.Itoa(childPID)), 0644); err != nil {
		fmt.Printf("[LionSQL] Warning: Failed to write PID file %s: %v\n", pidFile, err)
	}

	// Give child a brief moment to initialize
	time.Sleep(300 * time.Millisecond)

	if !isProcessAlive(childPID) {
		fmt.Println("[LionSQL] Error: Daemon process exited immediately. Check log file for details:")
		fmt.Printf("Log file: %s\n", logFile)
		os.Exit(1)
	}

	fmt.Println("============================================================")
	fmt.Println("  LionSQL Server Started in Daemon Mode (Background)")
	fmt.Println("============================================================")
	fmt.Printf(" Process ID (PID): %d\n", childPID)
	fmt.Printf(" Storage Engine:   %s\n", engineType)
	fmt.Printf(" Data Directory:   %s\n", dataDir)
	fmt.Printf(" Log File:         %s\n", logFile)
	fmt.Printf(" PID File:         %s\n", pidFile)
	if strings.Contains(httpAddr, ":") {
		fmt.Printf(" Web Admin UI:     http://%s/_/\n", httpAddr)
	}
	if strings.Contains(pgAddr, ":") {
		fmt.Printf(" PostgreSQL Wire:  psql -h 127.0.0.1 -p %s\n", strings.Split(pgAddr, ":")[1])
	}
	if strings.Contains(tdsAddr, ":") {
		fmt.Printf(" TDS / MSSQL:      sqlcmd -S 127.0.0.1,%s\n", strings.Split(tdsAddr, ":")[1])
	}
	fmt.Println("------------------------------------------------------------")
	fmt.Println(" Commands:")
	fmt.Println("   Check status:   lionsql status (or -status)")
	fmt.Println("   Stop daemon:    lionsql stop (or -stop)")
	fmt.Println("   Restart daemon: lionsql restart (or -restart)")
	fmt.Println("============================================================")
}
