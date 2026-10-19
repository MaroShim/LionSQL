package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"sqlion/pkg/config"
)

func printUsage() {
	fmt.Printf(`LionSQL v%s - High-Concurrency Embedded OLTP Database Engine & CLI

Usage:
  lionsql [flags]
  lionsql <command> [arguments...]

Server Commands:
  serve        Run LionSQL database server in foreground (default)
  start        Run LionSQL server as background daemon
  stop         Stop running LionSQL daemon
  restart      Restart running LionSQL daemon
  status       Check status of running LionSQL daemon
  doctor       Run self-diagnostic health checks on host, ports, and storage
  init-config  Generate a default lionsql.json configuration file

Database Management:
  migrate      Declarative database schema migration management
  sql          Execute one-off SQL statements against the local database
  backup       Create a point-in-time database snapshot backup
  restore      Restore database from backup archive

Informational:
  version      Display version and engine information
  help         Show this help message

Server Flags:
  -http <addr>           HTTP service address (default: 127.0.0.1:8090, empty to disable)
  -pg <addr>             PostgreSQL wire listen address (default: 127.0.0.1:5433)
  -tds <addr>            TDS (MSSQL) wire listen address (default: 127.0.0.1:1433)
  -dir <path>            Database data directory (default: ./data)
  -engine <type>         Storage engine: wiredtiger (default) or memory
  -auth                  Enforce JWT authentication on protected REST endpoints (default: true)
  -admin-email <email>   Default superuser email (default: admin@lionsql.local)
  -admin-pass <pass>     Default superuser password (default: 1234567890)
  -config <path>         Path to JSON configuration file (auto-detects ./lionsql.json)
  -dev                   Enable developer mode (real-time query & error tracing)
  -tls                   Enable SSL/TLS encrypted wire connections
  -slow-threshold-ms <N> Threshold in ms to record slow queries (default: 100.0)

Examples:
  lionsql                         # Start server with default configuration
  lionsql -dir=./mydb -http=:8090 # Start server on custom directory and port
  lionsql start                   # Start server as background daemon
  lionsql status                  # View running daemon status
  lionsql stop                    # Stop running background daemon
  lionsql doctor                  # Run system health and diagnostic checks
  lionsql init-config             # Create default lionsql.json configuration
  lionsql migrate status          # Inspect migration deployment status
  lionsql sql "SELECT 1;"         # Execute one-off SQL query
`, config.Version)
}

func printVersion() {
	fmt.Printf("LionSQL version v%s\n", config.Version)
	fmt.Println("Database Engine: LionSQL High-Concurrency Embedded OLTP")
	fmt.Println("Storage Backends: WiredTiger (Lock-free B+Tree MVCC), In-Memory OCC")
	fmt.Println("Wire Protocols:   PostgreSQL v3 (:5433), TDS 7.x/8.0 (:1433), HTTP REST (:8090)")
}

func main() {
	// Rule 1: No args or starts with flag -> Default to running server in foreground
	if len(os.Args) == 1 || strings.HasPrefix(os.Args[1], "-") {
		if err := runServer(os.Args[1:]); err != nil {
			if err != flag.ErrHelp {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			os.Exit(1)
		}
		return
	}

	cmd := strings.ToLower(os.Args[1])
	subargs := os.Args[2:]

	switch cmd {
	case "serve":
		if err := runServer(subargs); err != nil {
			if err != flag.ErrHelp {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			os.Exit(1)
		}
	case "start":
		if err := runServer(append(subargs, "-daemon")); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "stop":
		if err := runServer(append(subargs, "-stop")); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "restart":
		if err := runServer(append(subargs, "-restart")); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "status":
		if err := runServer(append(subargs, "-status")); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "doctor":
		if err := runServer(append(subargs, "-doctor")); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "init-config":
		targetPath := "lionsql.json"
		if len(subargs) > 0 && !strings.HasPrefix(subargs[0], "-") {
			targetPath = subargs[0]
		}
		if err := config.WriteToFileAtomic(config.DefaultConfig(), targetPath); err != nil {
			log.Fatalf("[LionSQL] Failed to initialize config file: %v", err)
		}
		fmt.Printf("\n[LionSQL] Default configuration generated successfully: %s\n\n", targetPath)

	case "migrate":
		if err := runMigrate(subargs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "sql":
		if err := runSQL(subargs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "backup":
		if len(subargs) == 0 {
			fmt.Println("Usage: lionsql backup <target.tar.gz> [flags]")
			os.Exit(1)
		}
		targetArchive := subargs[0]
		passArgs := append([]string{"-backup=" + targetArchive}, subargs[1:]...)
		if err := runServer(passArgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "restore":
		if len(subargs) == 0 {
			fmt.Println("Usage: lionsql restore <source.tar.gz> [flags]")
			os.Exit(1)
		}
		sourceArchive := subargs[0]
		passArgs := append([]string{"-restore=" + sourceArchive}, subargs[1:]...)
		if err := runServer(passArgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		printVersion()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}
