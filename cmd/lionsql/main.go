package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"lionsql/pkg/config"
)

func printUsage() {
	fmt.Printf(`LionSQL - Single binary High-Concurrency OLTP Database Server with T-SQL & Stored Procedure (v%s)
%s

Usage:
  lionsql <command> [arguments...]
  lionsql [options...]

Commands (기본 명령체계):
  start                  Start LionSQL server as background daemon
  stop                   Stop running LionSQL background daemon
  restart                Restart running LionSQL background daemon
  serve                  Run LionSQL database server in foreground (default)
  status                 Check status of running LionSQL daemon (PID, uptime, ports)
  doctor                 Run self-diagnostic health checks on host, ports, and storage
  init-config [path]     Generate a default lionsql.json configuration file
  migrate <subcommand>   Manage declarative database schema migrations (status, up, down)
  sql "<statement>"      Execute one-off SQL statements against the local database
  backup <file>          Create a point-in-time database snapshot backup archive
  restore <file>         Restore database state from a backup archive
  version                Display version and storage engine information
  help                   Show this help message

Options by Category (옵션 상세):

  [Network & Wire Protocols]
    -http <addr>           HTTP service & Web Studio address (default: 127.0.0.1:8090, empty to disable)
    -pg <addr>             PostgreSQL v3 wire listen address (default: 127.0.0.1:5433, empty to disable)
    -tds <addr>            MS SQL Server TDS listen address (default: 127.0.0.1:1433, empty to disable)
    -tls                   Enable SSL/TLS encrypted wire connections
    -tls-cert <path>       Path to TLS certificate file (auto-generates self-signed if omitted)
    -tls-key <path>        Path to TLS private key file
    -require-ssl           Enforce mandatory SSL/TLS for all wire clients

  [Storage & Persistence]
    -dir <path>            Database data directory (default: ./data) (aliases: -data, -data-dir)
    -engine <type>         Storage engine: wiredtiger (default, lock-free B+Tree) or memory (pure OCC)
    -ephemeral             Run in purely in-memory ephemeral mode with zero disk writes
    -seed                  Automatically seed demo e-commerce sample database on startup
    -config <path>         Path to JSON configuration file (auto-detects ./lionsql.json)

  [Memory & Performance]
    -max-memory <size>     Global heap memory budget in MB/GB (default: 65%% of physical RAM, e.g. 4096MB)
    -work-mem <size>       Memory budget per query operator (default: 64MB, e.g. 64MB, 128MB)
    -max-workers <N>       Max concurrent query execution workers (default: NumCPU * 4)
    -max-idle-sessions <N> Max idle storage sessions retained in pool (default: 64)

  [Security & Authentication]
    -auth                  Enforce JWT authentication on protected REST endpoints (default: true)
    -admin-email <email>   Default superuser email (default: admin@lionsql.local)
    -admin-pass <pass>     Default superuser password (default: 1234567890)

  [Logging & Diagnostics]
    -dev                   Enable developer mode (real-time query & parameter tracing on console)
    -slow-log <path>       Path to slow query log file (e.g. data/slow_query.log, pass 'true' for default)
    -slow-threshold-ms <N> Threshold in ms to record slow queries (default: 100.0)
    -general-log <path>    Enable general query logging (e.g. data/general.log, pass 'true' for default)

  [Daemon Lifecycle (Background Mode)]
    -d, -daemon            Run server as background daemon
    -stop                  Stop running background daemon
    -restart               Restart running background daemon
    -status                Query status of running daemon
    -pidfile <path>        Path to PID file (default: <dir>/lionsql.pid)
    -logfile <path>        Path to daemon log file (default: <dir>/lionsql.log)

  [High Availability & Replication]
    -replica-of <url>      Primary server HTTP URL to replicate from (e.g. http://127.0.0.1:8090)
    -node-id <id>          Unique replication node identifier
    -auto-failover         Enable Always On automatic failover upon primary heartbeat timeout
    -peers <urls>          Comma-separated list of peer HTTP URLs for quorum election
    -self-url <url>        HTTP URL of this node used for peer routing and failover election
    -heartbeat-timeout-ms <N> Heartbeat failure detection timeout (default: 3000ms)
    -election-timeout-ms <N>  Leader election vote timeout (default: 1500ms)

  [Backup, Restore & PITR]
    -backup <file>         Perform live snapshot backup to target archive and exit
    -restore <file>        Restore database from backup archive and exit
    -wal-archive <dir>     Path to WAL archive directory for continuous archiving and PITR
    -restore-time <ts>     Target timestamp for Point-In-Time Recovery
    -restore-lsn <lsn>     Target Log Sequence Number for Point-In-Time Recovery

  [Transactional Outbox Broker]
    -outbox                Enable Transactional Outbox broker dispatcher
    -outbox-table <table>  Filter outbox dispatcher to specific table (default: all)
    -outbox-webhook <url>  Webhook URL for outbox message forwarding
    -outbox-kafka <url>    Kafka REST Proxy endpoint (e.g. http://localhost:8082)
    -outbox-rabbitmq <url> RabbitMQ Management API endpoint (e.g. http://localhost:15672)

Examples:
  lionsql start                   # Start LionSQL as background daemon
  lionsql status                  # Check running background daemon status
  lionsql stop                    # Stop running background daemon
  lionsql restart                 # Restart running background daemon
  lionsql                         # Run LionSQL server in foreground (default)
  lionsql -dir=./mydb -http=:8090 # Start server on custom directory and port
  lionsql doctor                  # Run system health and diagnostic checks
  lionsql init-config             # Create default lionsql.json configuration
  lionsql sql "SELECT 1;"         # Execute one-off SQL statement
`, config.Version, config.CompactVersion())
}

func printVersion() {
	fmt.Println(config.DetailedVersion())
}

func main() {
	// Intercept help flags immediately
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "-help" || os.Args[1] == "--help" || os.Args[1] == "-?" || os.Args[1] == "help") {
		printUsage()
		return
	}

	// Intercept version flags immediately
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "-version" || os.Args[1] == "--version" || os.Args[1] == "version") {
		printVersion()
		return
	}

	// Rule 1: No args or starts with flag -> Default to running server in foreground
	if len(os.Args) == 1 || strings.HasPrefix(os.Args[1], "-") {
		if err := runServer(os.Args[1:]); err != nil {
			if err == flag.ErrHelp {
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cmd := strings.ToLower(os.Args[1])
	subargs := os.Args[2:]

	switch cmd {
	case "serve":
		if err := runServer(subargs); err != nil {
			if err == flag.ErrHelp {
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
