package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"lionsql/pkg/config"
)

func printShortUsage() {
	fmt.Printf(`LionSQL - Single binary High-Concurrency OLTP Database Server with T-SQL & Stored Procedure (v%s)
%s

Usage:
  lionsql <command> [arguments...]
  lionsql [options...]

Quickstart:
  lionsql start             # Run as background daemon
  lionsql                   # Run foreground server (:5433, :1433, :8090)
  lionsql -i                # Start interactive SQL shell

Commands:
  start | stop              Start or stop background daemon
  restart | status          Restart daemon or check running status
  serve                     Run database server in foreground (default)
  cli | sql "<statement>"   Interactive SQL shell or one-off query
  doctor                    Run system and storage diagnostic checks
  backup | restore <file>   Point-in-time database backup and restore
  migrate <subcommand>      Manage schema migrations (status, up, down)
  init-config [path]        Generate default configuration file
  version                   Display version and engine information

Common Options:
  -dir <path>               Database data directory (default: ./data)
  -engine <type>            Storage engine: wiredtiger or compact (default: wiredtiger)
  -http <addr>              HTTP service & Web Studio address (default: 127.0.0.1:8090)
  -pg <addr>                PostgreSQL v3 wire port (default: 127.0.0.1:5433)
  -tds <addr>               MS SQL Server TDS wire port (default: 127.0.0.1:1433)
  -dev                      Developer mode (real-time query & parameter tracing)
  -seed                     Seed demo sample database on startup
  -config <path>            Path to JSON configuration file (auto-detects ./lionsql.json)

Examples:
  lionsql start             # Start background daemon
  lionsql status            # Check background daemon status
  lionsql stop              # Stop background daemon
  lionsql -i                # Start interactive SQL REPL shell
  lionsql sql "SELECT 1;"   # Execute one-off SQL statement

For full options and comprehensive manual (Replication, Outbox, Memory, TLS), run:
  lionsql --help
`, config.Version, config.CompactVersion())
}

func printFullUsage() {
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
  cli                    Start interactive SQL shell or execute batch SQL
  sql "<statement>"      Execute one-off SQL statements against the local database
  get [key]              Query runtime configuration parameter(s) from running instance
  set <key> <val>        Update runtime configuration parameter (sudo-like password prompt)
  backup <file>          Create a point-in-time database snapshot backup archive
  restore <file>         Restore database state from a backup archive
  studio [flags]         Run standalone Web Studio proxy server connecting to remote LionSQL
  dbmail-worker          Run DatabaseMail background queue dispatcher worker
  version                Display version and storage engine information
  help                   Show this help message

Options by Category (옵션 상세):

  [Interactive & Batch SQL Execution]
    -i, -interactive           Start interactive SQL REPL shell (aliases: cli, -shell)
    -e, -execute <query>       Execute inline SQL statement and print results
    -f, -file <path>           Execute SQL statements from a file

  [Network & Wire Protocols]
    -http <addr>               HTTP service & Web Studio address (default: 127.0.0.1:8090, empty to disable)
    -no-studio                 Disable embedded Web Studio UI (/ and /_/)
    -app <path>                Host single-page HTML file or flat static assets directory (SPA Mode)
    -pg <addr>                 PostgreSQL v3 wire listen address (default: 127.0.0.1:5433, empty to disable)
    -tds <addr>                MS SQL Server TDS listen address (default: 127.0.0.1:1433, empty to disable)
    -tls                       Enable SSL/TLS encrypted wire connections
    -tls-cert <path>           Path to TLS certificate file (auto-generates self-signed if omitted)
    -tls-key <path>            Path to TLS private key file
    -require-ssl               Enforce mandatory SSL/TLS for all wire clients

  [Storage & Persistence]
    -dir <path>                Database data directory (default: ./data) (aliases: -data, -data-dir)
    -engine <type>             Storage engine backend: wiredtiger or compact (default: wiredtiger)
                                 - wiredtiger: High-concurrency Disk B+Tree MVCC engine for enterprise databases
                                 - compact   : Pure-Go In-Memory OCC engine with zero-GC flat buffers and AOF/WAL durability (alias: memory)
    -seed                      Automatically seed demo e-commerce sample database on startup
    -config <path>             Path to JSON configuration file (auto-detects ./lionsql.json)

  [Memory & Performance]
    -max-memory <size>         Global heap memory budget in MB/GB (default: 65%% of physical RAM, e.g. 4096MB)
    -work-mem <size>           Memory budget per query operator (default: 64MB, e.g. 64MB, 128MB)
    -max-workers <N>           Max concurrent query execution workers (default: NumCPU * 4)
    -max-idle-sessions <N>     Max idle storage sessions retained in pool (default: 64)
    -tx-idle-timeout <dur>     Idle transaction/session timeout before automatic rollback (default: 5m, Sweep: 10s)

  [Security & Authentication]
    -auth                      Enforce JWT authentication on protected REST endpoints (default: true)
    -admin-email <email>       Default superuser email (default: admin@lionsql.local)
    -admin-pass <pass>         Default superuser password (default: 1234567890)

  [Logging & Diagnostics]
    -dev                       Enable developer mode (real-time query & parameter tracing on console)
    -slow-log <path>           Path to slow query log file (e.g. data/slow_query.log, pass 'true' for default)
    -slow-threshold-ms <N>     Threshold in ms to record slow queries (default: 100.0)
    -general-log <path>        Enable general query logging (e.g. data/general.log, pass 'true' for default)

  [Daemon Configuration]
    -d, -daemon                Run server as background daemon
    -pidfile <path>            Path to PID file (default: <dir>/lionsql.pid)
    -logfile <path>            Path to daemon log file (default: <dir>/lionsql.log)

  [High Availability & Replication]
    -replica-of <url>          Primary server HTTP URL to replicate from (e.g. http://127.0.0.1:8090)
    -node-id <id>              Unique replication node identifier
    -auto-failover             Enable Always On automatic failover upon primary heartbeat timeout
    -peers <urls>              Comma-separated list of peer HTTP URLs for quorum election
    -self-url <url>            HTTP URL of this node used for peer routing and failover election
    -heartbeat-timeout-ms <N>  Heartbeat failure detection timeout (default: 3000ms)
    -election-timeout-ms <N>   Leader election vote timeout (default: 1500ms)
    -cluster-token <token>     Pre-shared cluster secret token for HA replication authentication (or LIONSQL_CLUSTER_TOKEN)

  [Backup & PITR Options]
    -wal-archive <dir>         Path to WAL archive directory for continuous archiving and PITR
    -restore-time <ts>         Target timestamp for Point-In-Time Recovery
    -restore-lsn <lsn>         Target Log Sequence Number for Point-In-Time Recovery

  [Transactional Outbox Broker]
    -outbox                    Enable Transactional Outbox broker dispatcher
    -outbox-table <table>      Filter outbox dispatcher to specific table (default: all)
    -outbox-webhook <url>      Webhook URL for outbox message forwarding
    -outbox-kafka <url>        Kafka REST Proxy endpoint (e.g. http://localhost:8082)
    -outbox-rabbitmq <url>     RabbitMQ Management API endpoint (e.g. http://localhost:15672)

Examples:
  lionsql start                   # Start LionSQL as background daemon
  lionsql status                  # Check running background daemon status
  lionsql stop                    # Stop running background daemon
  lionsql restart                 # Restart running background daemon
  lionsql                         # Run LionSQL server in foreground (default)
  lionsql -app=./dist             # Serve SPA web application along with database
  lionsql -dir=./mydb -http=:8090 # Start server on custom directory and port
  lionsql doctor                  # Run system health and diagnostic checks
  lionsql init-config             # Create default lionsql.json configuration
  lionsql -i                      # Start interactive SQL REPL shell
  lionsql -e "SELECT 1;"          # Execute inline SQL statement
  lionsql -f ./schema.sql         # Execute SQL statements from file
  lionsql sql "SELECT 1;"         # Execute one-off SQL statement
`, config.Version, config.CompactVersion())
}

func printUsage() {
	printFullUsage()
}

func printVersion() {
	fmt.Println(config.DetailedVersion())
}

func isCLICall(args []string) bool {
	for _, arg := range args {
		name := arg
		if strings.HasPrefix(name, "--") {
			name = name[2:]
		} else if strings.HasPrefix(name, "-") {
			name = name[1:]
		} else {
			continue
		}
		if eq := strings.IndexByte(name, '='); eq != -1 {
			name = name[:eq]
		}
		switch strings.ToLower(name) {
		case "i", "interactive", "shell", "e", "execute", "f", "file":
			return true
		}
	}
	return false
}

func main() {
	defer cleanupPlatformTimer()

	// Intercept help flags immediately
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "-?":
			printShortUsage()
			return
		case "--help", "-help", "help":
			printFullUsage()
			return
		}
	}

	// Intercept version flags immediately
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "-version" || os.Args[1] == "--version" || os.Args[1] == "version") {
		printVersion()
		return
	}

	// Intercept CLI flags (-i, --interactive, -e, --execute, -f, --file, -shell, --shell)
	if len(os.Args) > 1 && isCLICall(os.Args[1:]) {
		if err := runCLI(os.Args[1:]); err != nil {
			if err == flag.ErrHelp {
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
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

	case "cli":
		if err := runCLI(subargs); err != nil {
			if err == flag.ErrHelp {
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "migrate":
		if err := runMigrate(subargs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "sql":
		if len(subargs) == 0 || isCLICall(subargs) {
			if err := runCLI(subargs); err != nil {
				if err == flag.ErrHelp {
					return
				}
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if err := runSQL(subargs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "get":
		if err := runGet(subargs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "set":
		if err := runSet(subargs); err != nil {
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
	case "studio":
		if err := runStudioProxy(subargs); err != nil {
			if err == flag.ErrHelp {
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "dbmail-worker":
		if err := runDbMailWorker(subargs); err != nil {
			if err == flag.ErrHelp {
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		printVersion()
	case "-h", "-?":
		printShortUsage()
	case "help", "--help", "-help":
		printFullUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %q\n\n", os.Args[1])
		printShortUsage()
		os.Exit(1)
	}
}
