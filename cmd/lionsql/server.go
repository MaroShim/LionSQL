package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lionsql/pkg/api"
	"lionsql/pkg/auth"
	"lionsql/pkg/backup"
	"lionsql/pkg/catalog"
	"lionsql/pkg/cdc"
	"lionsql/pkg/config"
	"lionsql/pkg/execution"
	_ "lionsql/pkg/proc/parser/antlr"
	"lionsql/pkg/replication"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
	"lionsql/pkg/wire"
	"lionsql/pkg/wire/tds"
)

// resolveDefaultDataDir locates the default data directory.
func resolveDefaultDataDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return "data"
	}
	return resolveDefaultDataDirFromExe(exePath)
}

func resolveDefaultDataDirFromExe(exePath string) string {
	resolvedPath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		resolvedPath = exePath
	}
	exeDir := filepath.Dir(resolvedPath) // e.g. /path/to/bin
	parentDir := filepath.Dir(exeDir)    // e.g. /path/to

	if filepath.Base(exeDir) == "bin" {
		return filepath.Join(parentDir, "data")
	}
	return "data"
}

// runServer parses flags from args and executes the LionSQL server.
func runServer(args []string) error {
	fs := flag.NewFlagSet("lionsql", flag.ContinueOnError)
	fs.Usage = printUsage

	httpAddr := fs.String("http", "127.0.0.1:8090", "HTTP service address (set to empty to disable)")
	pgAddr := fs.String("pg", "127.0.0.1:5433", "PostgreSQL wire protocol listen address (set to empty to disable)")
	tdsAddr := fs.String("tds", "127.0.0.1:1433", "TDS (MSSQL) wire protocol listen address (set to empty to disable)")
	defaultDataDir := resolveDefaultDataDir()
	dataDir := fs.String("dir", defaultDataDir, "Data directory for database files (default: adjacent to bin/ or 'data')")
	fs.StringVar(dataDir, "data", defaultDataDir, "Alias for -dir")
	fs.StringVar(dataDir, "data-dir", defaultDataDir, "Alias for -dir")

	engineType := fs.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
	ephemeralFlag := fs.Bool("ephemeral", false, "Run in purely in-memory ephemeral mode with zero disk persistence")
	autoSeed := fs.Bool("seed", false, "Automatically seed demo sample database (scripts/demo_seed.sql) on startup")
	adminEmail := fs.String("admin-email", "admin@lionsql.local", "Default superuser email")
	adminPass := fs.String("admin-pass", "1234567890", "Default superuser password")
	enableAuth := fs.Bool("auth", true, "Enforce JWT authentication on protected REST endpoints (default: true)")
	maxWorkers := fs.Int("max-workers", 0, "Max concurrent query execution workers (default: NumCPU * 4)")
	maxIdleSessions := fs.Int("max-idle-sessions", 64, "Max idle storage sessions to retain in pool")
	workMemStr := fs.String("work-mem", "64MB", "Default memory budget per query operator (e.g. 64MB, 128MB)")
	maxMemoryStr := fs.String("max-memory", "", "Maximum global heap memory budget (default: 70% of physical RAM, e.g. 2GB, 4GB)")
	enableTLS := fs.Bool("tls", false, "Enable SSL/TLS encrypted connections for PostgreSQL and TDS wire protocols")
	tlsCert := fs.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := fs.String("tls-key", "", "Path to TLS private key file")
	requireSSL := fs.Bool("require-ssl", false, "Require SSL/TLS for PostgreSQL and TDS wire connections")
	backupTarget := fs.String("backup", "", "Perform live point-in-time snapshot backup to target archive and exit")
	restoreSource := fs.String("restore", "", "Restore database from backup archive and exit")
	walArchiveDir := fs.String("wal-archive", "", "Path to WAL archive directory for continuous archiving and PITR")
	restoreTimeStr := fs.String("restore-time", "", "Target timestamp for Point-In-Time Recovery (RFC3339 or 'YYYY-MM-DD HH:MM:SS')")
	restoreLSN := fs.Int64("restore-lsn", 0, "Target Log Sequence Number (LSN) for Point-In-Time Recovery")
	archiveWalFile := fs.String("archive-wal", "", "Archive a specific WAL file into the WAL archive directory and exit")
	slowLog := fs.String("slow-log", "", "Path to slow query log file (e.g. data/slow_query.log, pass 'true' for default)")
	slowThreshold := fs.Float64("slow-threshold-ms", 100.0, "Threshold in milliseconds to record slow queries (default: 100ms)")
	generalLog := fs.String("general-log", "", "Enable general query logging (e.g. data/general.log, pass 'true' for default)")
	replicaOf := fs.String("replica-of", "", "Primary server HTTP URL to replicate from (e.g. http://127.0.0.1:8090)")
	nodeID := fs.String("node-id", "", "Unique replication node identifier")
	autoFailover := fs.Bool("auto-failover", false, "Enable Always On automatic failover upon primary heartbeat timeout")
	peersFlag := fs.String("peers", "", "Comma-separated list of peer HTTP URLs for availability group quorum")
	selfURLFlag := fs.String("self-url", "", "HTTP URL of this node used for peer routing and failover election")
	heartbeatTimeout := fs.Float64("heartbeat-timeout-ms", 0, "Heartbeat failure detection timeout in milliseconds (default: 3000ms)")
	electionTimeout := fs.Float64("election-timeout-ms", 0, "Leader election vote timeout in milliseconds (default: 1500ms)")
	configPath := fs.String("config", "", "Path to JSON configuration file (default: auto-detect ./lionsql.json or <dir>/lionsql.json)")
	initConfig := fs.String("init-config", "", "Generate a default lionsql.json configuration file at specified path (or ./lionsql.json) and exit")
	doctorFlag := fs.Bool("doctor", false, "Run self-diagnostic health checks on host environment, resources, ports, and storage engines and exit")
	devFlag := fs.Bool("dev", false, "Enable developer mode (verbose wire query and parameter logging on stdout/stderr)")
	outboxFlag := fs.Bool("outbox", false, "Enable Transactional Outbox broker dispatcher")
	outboxTable := fs.String("outbox-table", "", "Filter outbox dispatcher to specific table (default: all)")
	outboxTopicPrefix := fs.String("outbox-topic-prefix", "", "Prefix for outbox broker topics (default: 'lionsql.')")
	outboxWebhook := fs.String("outbox-webhook", "", "Webhook URL for outbox message forwarding")
	outboxKafka := fs.String("outbox-kafka", "", "Kafka REST Proxy endpoint (e.g. http://localhost:8082)")
	outboxRabbitMQ := fs.String("outbox-rabbitmq", "", "RabbitMQ Management API endpoint (e.g. http://localhost:15672)")

	// Daemon control flags
	var daemon bool
	fs.BoolVar(&daemon, "d", false, "Run LionSQL server as background daemon")
	fs.BoolVar(&daemon, "daemon", false, "Run LionSQL server as background daemon")
	stopFlag := fs.Bool("stop", false, "Stop running LionSQL daemon")
	statusFlag := fs.Bool("status", false, "Check status of running LionSQL daemon")
	restartFlag := fs.Bool("restart", false, "Restart LionSQL daemon")
	pidFile := fs.String("pidfile", "", "Path to PID file (default: <dir>/lionsql.pid)")
	logFile := fs.String("logfile", "", "Path to log file (default: <dir>/lionsql.log)")
	isChildWorker := fs.Bool("is-child-worker", false, "Internal flag for daemon worker child")
	var versionFlag bool
	fs.BoolVar(&versionFlag, "v", false, "Display version and build information")
	fs.BoolVar(&versionFlag, "version", false, "Display version and build information")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if versionFlag {
		printVersion()
		return nil
	}

	// 1. Standalone Config Generator (--init-config)
	if *initConfig != "" {
		targetPath := *initConfig
		if targetPath == "true" || targetPath == "1" {
			targetPath = "lionsql.json"
		}
		if err := config.WriteToFileAtomic(config.DefaultConfig(), targetPath); err != nil {
			log.Fatalf("[LionSQL] Failed to initialize config file: %v", err)
		}
		fmt.Printf("\n[LionSQL] Default configuration generated successfully: %s\n\n", targetPath)
		return nil
	}

	// 2. Load Configuration (Auto-Discovery: explicit > ./lionsql.json > <dir>/lionsql.json > Built-in Defaults)
	cfg, loadedConfigPath, err := config.AutoDiscoverConfig(*configPath, *dataDir)
	if err != nil {
		log.Fatalf("[LionSQL] Configuration error: %v", err)
	}

	// 3. Apply CLI Argument Overrides (Deterministic Hierarchy: CLI Flags > JSON Config > Built-in Defaults)
	visited := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})

	if visited["http"] {
		cfg.Network.HTTPAddr = *httpAddr
	}
	if visited["pg"] {
		cfg.Network.PGAddr = *pgAddr
	}
	if visited["tds"] {
		cfg.Network.TDSAddr = *tdsAddr
	}
	if visited["dir"] || visited["data"] || visited["data-dir"] {
		cfg.Server.DataDir = *dataDir
	}
	if visited["engine"] {
		cfg.Server.Engine = *engineType
	}
	if visited["ephemeral"] {
		cfg.Server.Ephemeral = *ephemeralFlag
	}
	if visited["seed"] {
		cfg.Server.AutoSeed = *autoSeed
	}
	if visited["admin-email"] {
		cfg.Auth.AdminEmail = *adminEmail
	}
	if visited["admin-pass"] {
		cfg.Auth.AdminPass = *adminPass
	}
	if visited["auth"] {
		cfg.Auth.Enabled = *enableAuth
	}
	if visited["max-workers"] {
		cfg.Resources.MaxWorkers = *maxWorkers
	}
	if visited["max-idle-sessions"] {
		cfg.Resources.MaxIdleSessions = *maxIdleSessions
	}
	if visited["work-mem"] {
		cfg.Resources.WorkMem = *workMemStr
	}
	if visited["max-memory"] {
		cfg.Resources.MaxMemory = *maxMemoryStr
	}
	if visited["tls"] {
		cfg.Network.TLS.Enabled = *enableTLS
	}
	if visited["tls-cert"] {
		cfg.Network.TLS.CertFile = *tlsCert
	}
	if visited["tls-key"] {
		cfg.Network.TLS.KeyFile = *tlsKey
	}
	if visited["require-ssl"] {
		cfg.Network.TLS.RequireSSL = *requireSSL
	}
	if visited["slow-log"] {
		cfg.Logging.SlowQuery.File = *slowLog
	}
	if visited["slow-threshold-ms"] {
		cfg.Logging.SlowQuery.ThresholdMS = *slowThreshold
	}
	if visited["general-log"] {
		cfg.Logging.GeneralQuery.File = *generalLog
	}
	if visited["replica-of"] {
		cfg.Replication.PrimaryURL = *replicaOf
		cfg.Replication.Enabled = true
		cfg.Replication.Role = "REPLICA"
	}
	if visited["node-id"] {
		cfg.Replication.NodeID = *nodeID
	}
	if visited["auto-failover"] {
		cfg.Replication.AutoFailover = *autoFailover
	}
	if visited["peers"] && *peersFlag != "" {
		cfg.Replication.Peers = strings.Split(*peersFlag, ",")
		for i := range cfg.Replication.Peers {
			cfg.Replication.Peers[i] = strings.TrimSpace(cfg.Replication.Peers[i])
		}
	}
	if visited["self-url"] {
		cfg.Replication.SelfURL = *selfURLFlag
	}
	if visited["heartbeat-timeout-ms"] {
		cfg.Replication.HeartbeatTimeoutMS = *heartbeatTimeout
	}
	if visited["election-timeout-ms"] {
		cfg.Replication.ElectionTimeoutMS = *electionTimeout
	}
	if visited["pidfile"] {
		cfg.Server.PIDFile = *pidFile
	}
	if visited["logfile"] {
		cfg.Server.LogFile = *logFile
	}
	if visited["dev"] {
		cfg.Logging.DevMode = *devFlag
	}
	if visited["outbox"] {
		cfg.Outbox.Enabled = *outboxFlag
	}
	if visited["outbox-table"] {
		cfg.Outbox.TableFilter = *outboxTable
	}
	if visited["outbox-topic-prefix"] {
		cfg.Outbox.TopicPrefix = *outboxTopicPrefix
	}
	if visited["outbox-webhook"] && *outboxWebhook != "" {
		cfg.Outbox.Enabled = true
		cfg.Outbox.WebhookURLs = append(cfg.Outbox.WebhookURLs, *outboxWebhook)
	}
	if visited["outbox-kafka"] && *outboxKafka != "" {
		cfg.Outbox.Enabled = true
		cfg.Outbox.KafkaEndpoint = *outboxKafka
	}
	if visited["outbox-rabbitmq"] && *outboxRabbitMQ != "" {
		cfg.Outbox.Enabled = true
		cfg.Outbox.RabbitMQURL = *outboxRabbitMQ
	}

	if strings.TrimSpace(cfg.Server.DataDir) == "" {
		cfg.Server.DataDir = defaultDataDir
	}

	resolvedPidFile := cfg.Server.PIDFile
	if resolvedPidFile == "" {
		resolvedPidFile = filepath.Join(cfg.Server.DataDir, "lionsql.pid")
	}
	resolvedLogFile := cfg.Server.LogFile
	if resolvedLogFile == "" {
		resolvedLogFile = filepath.Join(cfg.Server.DataDir, "lionsql.log")
	}

	// 4. Standalone Self-Diagnostic Health Verification (--doctor)
	if *doctorFlag {
		if err := RunDoctor(cfg); err != nil {
			os.Exit(1)
		}
		return nil
	}

	// Inquiries and shutdown commands (-status, -stop)
	if !*isChildWorker {
		if *statusFlag || *stopFlag {
			if handleDaemonCommands(
				false,
				*stopFlag,
				*statusFlag,
				false,
				cfg.Server.DataDir,
				resolvedPidFile,
				resolvedLogFile,
				cfg.Network.HTTPAddr,
				cfg.Network.PGAddr,
				cfg.Network.TDSAddr,
				cfg.Server.Engine,
			) {
				return nil
			}
		}
	}

	// 5. Production Security Guard (PROD-SEC-1):
	// Default superuser password ("1234567890") is strictly permitted ONLY on loopback (127.0.0.1, localhost).
	// If bound to 0.0.0.0 or any external interface with the default password, abort startup immediately.
	if *backupTarget == "" && *restoreSource == "" && *archiveWalFile == "" {
		if err := cfg.ValidateSecurityGuard(); err != nil {
			fmt.Println()
			fmt.Println("================================================================================")
			fmt.Println(" [LionSQL Production Security Guard] Server Startup Aborted")
			fmt.Println("================================================================================")
			fmt.Printf(" Error: %v\n\n", err)
			fmt.Println(" LionSQL permits zero-config default credentials only on local loopback (127.0.0.1).")
			fmt.Println(" When exposing services to external/public networks, you must provide a secure password.")
			fmt.Println()
			fmt.Println(" How to fix:")
			fmt.Println("   1. Specify a custom password via CLI flag:")
			fmt.Println("      lionsql serve -admin-pass=\"<secure-password>\" ...")
			fmt.Println("   2. Or set via environment variable:")
			fmt.Println("      export LIONSQL_ADMIN_PASS=\"<secure-password>\"")
			fmt.Println("   3. Or set in lionsql.json:")
			fmt.Println("      \"auth\": { \"admin_pass\": \"<secure-password>\" }")
			fmt.Println("   4. Or bind strictly to 127.0.0.1 if external access is not required:")
			fmt.Println("      lionsql serve -http=127.0.0.1:8090 -pg=127.0.0.1:5433 -tds=127.0.0.1:1433")
			fmt.Println("================================================================================")
			fmt.Println()
			os.Exit(1)
		}
	}

	// Handle daemon commands if not running as internal worker
	if !*isChildWorker {
		if handleDaemonCommands(
			daemon,
			false,
			false,
			*restartFlag,
			cfg.Server.DataDir,
			resolvedPidFile,
			resolvedLogFile,
			cfg.Network.HTTPAddr,
			cfg.Network.PGAddr,
			cfg.Network.TDSAddr,
			cfg.Server.Engine,
		) {
			return nil
		}

		// Prevent multiple server instances from running concurrently on the same data directory
		if existingPid, err := readPidFile(resolvedPidFile); err == nil && isProcessAlive(existingPid) && existingPid != os.Getpid() {
			fmt.Printf("\n[LionSQL] Error: Another LionSQL server instance (PID %d) is already running for data directory '%s'.\n", existingPid, cfg.Server.DataDir)
			fmt.Println("         To view status:  lionsql status")
			fmt.Println("         To stop daemon:  lionsql stop")
			fmt.Println("         To restart:      lionsql restart")
			fmt.Println()
			os.Exit(1)
		}

		// Record PID for this foreground process
		_ = os.WriteFile(filepath.Clean(resolvedPidFile), []byte(strconv.Itoa(os.Getpid())), 0644)
		defer func() {
			if curPid, err := readPidFile(resolvedPidFile); err == nil && curPid == os.Getpid() {
				_ = os.Remove(resolvedPidFile)
			}
		}()

		// Pre-flight port collision verification for server mode
		if *backupTarget == "" && *restoreSource == "" {
			if cfg.Network.IsHTTPEnabled() {
				if l, err := net.Listen("tcp", cfg.Network.HTTPAddr); err != nil {
					fmt.Printf("\n[LionSQL] Error: HTTP/REST port conflict (%s): %v\n", cfg.Network.HTTPAddr, err)
					fmt.Println("         Another LionSQL server or service is already listening on this port.")
					fmt.Println("         Please specify a different port (e.g. -http=127.0.0.1:8091).")
					fmt.Println()
					os.Exit(1)
				} else {
					_ = l.Close()
				}
			}
			if cfg.Network.IsPGEnabled() {
				if l, err := net.Listen("tcp", cfg.Network.PGAddr); err != nil {
					fmt.Printf("\n[LionSQL] Error: PostgreSQL wire port conflict (%s): %v\n", cfg.Network.PGAddr, err)
					fmt.Println("         Another LionSQL server, PostgreSQL instance, or service is already listening on this port.")
					fmt.Println("         Please specify a different port (e.g. -pg=127.0.0.1:5434).")
					fmt.Println()
					os.Exit(1)
				} else {
					_ = l.Close()
				}
			}
			if cfg.Network.IsTDSEnabled() {
				if l, err := net.Listen("tcp", cfg.Network.TDSAddr); err != nil {
					fmt.Printf("\n[LionSQL] Error: TDS/MSSQL wire port conflict (%s): %v\n", cfg.Network.TDSAddr, err)
					fmt.Println("         Another LionSQL server, MS SQL instance, or service is already listening on this port.")
					fmt.Println("         Please specify a different port (e.g. -tds=127.0.0.1:1434).")
					fmt.Println()
					os.Exit(1)
				} else {
					_ = l.Close()
				}
			}
		}
	}

	// 5. Initialize Storage Engine Backend
	var eng storage.Engine
	switch cfg.Server.Engine {
	case "wiredtiger":
		if cfg.Server.Ephemeral {
			log.Fatalf("[LionSQL] Ephemeral mode (-ephemeral) is only supported with -engine=memory. WiredTiger is a disk-persistent engine.")
		}
		if err := os.MkdirAll(cfg.Server.DataDir, 0755); err != nil {
			log.Fatalf("[LionSQL] Failed to create data directory: %v", err)
		}
		wtEng := wiredtiger.New()
		if err := wtEng.Open(cfg.Server.DataDir); err != nil {
			log.Fatalf("[LionSQL] Failed to open WiredTiger storage: %v", err)
		}
		eng = wtEng
	case "memory":
		memEng := memengine.New()
		opts := memengine.DefaultOptions(cfg.Server.DataDir)
		if cfg.Server.Ephemeral {
			opts.EnableAOF = false
			opts.EnableRDB = false
			opts.DataDir = ""
			log.Printf("[LionSQL] In-Memory engine running in EPHEMERAL mode (pure in-memory, zero disk persistence)")
		} else {
			log.Printf("[LionSQL] In-Memory engine running in PERSISTENT mode (AOF: %v, RDB: %v, dir: %s)", opts.EnableAOF, opts.EnableRDB, cfg.Server.DataDir)
		}
		if err := memEng.OpenWithOptions(opts); err != nil {
			log.Fatalf("Failed to open storage engine: %v", err)
		}
		eng = memEng
	default:
		log.Fatalf("[LionSQL] Unknown storage engine: %s (supported: wiredtiger, memory)", cfg.Server.Engine)
	}
	defer eng.Close()

	// 6. Initialize System Catalog
	cat := catalog.New(eng)
	if !cfg.Server.Ephemeral && cfg.Server.DataDir != "" {
		if err := cat.SetPersistenceDir(cfg.Server.DataDir); err != nil {
			log.Printf("[LionSQL] Warning: Failed to set catalog persistence dir: %v", err)
		}
	}

	// 7. Handle Standalone WAL Archiving / Backup / Restore operations
	if *backupTarget != "" {
		log.Printf("[LionSQL] Initiating online snapshot backup to %s...", *backupTarget)
		manifest, err := backup.CreateBackupFile(*backupTarget, eng, cat)
		if err != nil {
			log.Fatalf("[LionSQL] Backup failed: %v", err)
		}
		log.Printf("[LionSQL] Backup successfully completed: %d tables, %d total records, %d procedures saved to %s",
			manifest.TablesCount, manifest.TotalRecords, manifest.ProceduresCount, *backupTarget)
		return nil
	}

	if *archiveWalFile != "" {
		archiveDir := *walArchiveDir
		if archiveDir == "" {
			archiveDir = filepath.Join(cfg.Server.DataDir, "wal_archive")
		}
		archiver, err := backup.NewWALArchiver(archiveDir)
		if err != nil {
			log.Fatalf("[LionSQL] Failed to initialize WAL archiver: %v", err)
		}
		seg, err := archiver.ArchiveSegment(*archiveWalFile)
		if err != nil {
			log.Fatalf("[LionSQL] Failed to archive WAL segment: %v", err)
		}
		log.Printf("[LionSQL] WAL segment archived successfully: %s (LSN %d-%d, %d records)",
			seg.Filename, seg.MinLSN, seg.MaxLSN, seg.RecordCount)
		return nil
	}

	if *restoreSource != "" || *restoreTimeStr != "" || *restoreLSN > 0 {
		archiveDir := *walArchiveDir
		if archiveDir == "" {
			archiveDir = filepath.Join(cfg.Server.DataDir, "wal_archive")
		}

		var targetTime time.Time
		if *restoreTimeStr != "" {
			var parseErr error
			for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
				if t, err := time.Parse(layout, *restoreTimeStr); err == nil {
					targetTime = t.UTC()
					parseErr = nil
					break
				} else {
					parseErr = err
				}
			}
			if parseErr != nil {
				log.Fatalf("[LionSQL] Invalid --restore-time format %q: %v", *restoreTimeStr, parseErr)
			}
		}

		if !targetTime.IsZero() || *restoreLSN > 0 {
			log.Printf("[LionSQL] Initiating Point-In-Time Recovery (PITR)... (target_time: %v, target_lsn: %d)", targetTime, *restoreLSN)
			pitrOpts := backup.PITROptions{
				RestoreOptions: backup.DefaultRestoreOptions(),
				TargetTime:     targetTime,
				TargetLSN:      *restoreLSN,
			}
			res, err := backup.RestorePITR(*restoreSource, archiveDir, eng, cat, pitrOpts)
			if err != nil {
				log.Fatalf("[LionSQL] PITR restoration failed: %v", err)
			}
			log.Printf("[LionSQL] PITR restoration successfully completed: %d WAL segments replayed, %d records applied, final LSN: %d, final time: %v (duration: %v)",
				res.ArchivedSegmentsParsed, res.WALRecordsReplayed, res.FinalLSN, res.FinalTimestamp, res.Duration)
			return nil
		}

		log.Printf("[LionSQL] Initiating database restore from %s...", *restoreSource)
		res, err := backup.RestoreBackupFile(*restoreSource, eng, cat, backup.DefaultRestoreOptions())
		if err != nil {
			log.Fatalf("[LionSQL] Restore failed: %v", err)
		}
		log.Printf("[LionSQL] Restore successfully completed: %d tables, %d records, %d procedures restored in %v",
			res.TablesRestored, res.RecordsRestored, res.ProceduresRestored, res.Duration)
		return nil
	}

	// 8. Authentication & RBAC Service
	authSvc := auth.NewAuthService(auth.Config{})
	_, _ = authSvc.EnsureDefaultSuperuser(cfg.Auth.AdminEmail, cfg.Auth.AdminPass)

	server := api.NewServerWithAuth(cat, eng, authSvc, cfg.Auth.Enabled)
	if cfg.Resources.MaxWorkers > 0 || cfg.Resources.MaxIdleSessions > 0 {
		poolCfg := api.DefaultPoolConfig()
		if cfg.Resources.MaxWorkers > 0 {
			poolCfg.MaxWorkers = cfg.Resources.MaxWorkers
		}
		if cfg.Resources.MaxIdleSessions > 0 {
			poolCfg.MaxIdleSessions = cfg.Resources.MaxIdleSessions
		}
		server.SetPoolConfig(poolCfg)
	}

	// Memory Governor: Global memory budget (GOMEMLIMIT) & operator work_mem
	var maxMemBytes int64
	if cfg.Resources.MaxMemory != "" {
		if bytes, err := execution.ParseMemSize(cfg.Resources.MaxMemory); err == nil && bytes > 0 {
			maxMemBytes = bytes
		}
	}
	if maxMemBytes <= 0 {
		maxMemBytes = execution.GetDefaultMaxMemory()
	}
	execution.SetGlobalMemoryLimit(maxMemBytes)

	if cfg.Resources.WorkMem != "" {
		if bytes, err := execution.ParseMemSize(cfg.Resources.WorkMem); err == nil && bytes > 0 {
			server.SetDefaultWorkMem(bytes)
		}
	}

	// Auto-seed demo sample database if requested
	if cfg.Server.AutoSeed {
		seedPath := "scripts/demo_seed.sql"
		sqlBytes, err := os.ReadFile(seedPath)
		if err != nil {
			log.Printf("[LionSQL] Error reading seed script %q: %v", seedPath, err)
		} else {
			log.Printf("[LionSQL] Seeding demo sample database from %s...", seedPath)
			if _, err := server.ExecuteSQL(string(sqlBytes)); err != nil {
				log.Printf("[LionSQL] Warning: demo seed script returned error: %v", err)
			} else {
				log.Printf("[LionSQL] Demo sample database seeded successfully.")
			}
		}
	}

	if cfg.Logging.SlowQuery.ThresholdMS > 0 {
		server.SetSlowQueryThreshold(time.Duration(cfg.Logging.SlowQuery.ThresholdMS * float64(time.Millisecond)))
	}
	if cfg.Logging.SlowQuery.IsEnabled() {
		if err := server.SetSlowQueryLogFile(cfg.Logging.SlowQuery.File); err != nil {
			log.Printf("[LionSQL] Warning: Failed to enable slow query log file: %v", err)
		} else {
			log.Printf("[LionSQL] Slow query log file enabled: %s (threshold: %.1fms)", cfg.Logging.SlowQuery.File, cfg.Logging.SlowQuery.ThresholdMS)
		}
	}

	if cfg.Logging.GeneralQuery.IsEnabled() {
		server.SetGeneralLogEnabled(true)
		if err := server.SetGeneralLogFile(cfg.Logging.GeneralQuery.File); err != nil {
			log.Printf("[LionSQL] Warning: Failed to enable general query log file: %v", err)
		} else {
			log.Printf("[LionSQL] General query log file enabled: %s", cfg.Logging.GeneralQuery.File)
		}
	}

	var repMgr *replication.Manager
	if cfg.Replication.Enabled {
		role := replication.Role(strings.ToUpper(cfg.Replication.Role))
		if role == "" {
			role = replication.RolePrimary
		}

		var hbTimeout time.Duration
		if cfg.Replication.HeartbeatTimeoutMS > 0 {
			hbTimeout = time.Duration(cfg.Replication.HeartbeatTimeoutMS * float64(time.Millisecond))
		}
		var elecTimeout time.Duration
		if cfg.Replication.ElectionTimeoutMS > 0 {
			elecTimeout = time.Duration(cfg.Replication.ElectionTimeoutMS * float64(time.Millisecond))
		}

		var err error
		repMgr, err = replication.NewManager(replication.Config{
			NodeID:           cfg.Replication.NodeID,
			Role:             role,
			PrimaryURL:       cfg.Replication.PrimaryURL,
			SelfURL:          cfg.Replication.SelfURL,
			Peers:            cfg.Replication.Peers,
			AutoFailover:     cfg.Replication.AutoFailover,
			HeartbeatTimeout: hbTimeout,
			ElectionTimeout:  elecTimeout,
			Quorum:           cfg.Replication.Quorum,
			HistoryCap:       cfg.Replication.HistoryCap,
			Engine:           eng,
		})
		if err != nil {
			log.Fatalf("[LionSQL] Failed to initialize replication manager: %v", err)
		}
		server.SetReplicationManager(repMgr)
		if err := repMgr.Start(context.Background()); err != nil {
			log.Fatalf("[LionSQL] Failed to start replication: %v", err)
		}
		if cfg.Replication.AutoFailover {
			log.Printf("[LionSQL] Availability Group: Always On Auto-Failover enabled (peers: %d, heartbeat timeout: %v)", len(cfg.Replication.Peers), hbTimeout)
		}
	}

	// Initialize CDC Streamer
	cdcStreamer := cdc.NewStreamer(10000)
	type walHookAdder interface {
		AddWALHook(fn func(rec memengine.WALRecord))
	}
	if wha, ok := eng.(walHookAdder); ok {
		wha.AddWALHook(cdcStreamer.OnWALRecord)
	}
	server.SetCDCStreamer(cdcStreamer)

	// Initialize Transactional Outbox Broker Dispatcher
	var outboxDispatcher *cdc.OutboxDispatcher
	if cfg.Outbox.Enabled {
		outboxCfg := cdc.DefaultDispatcherConfig()
		outboxCfg.DataDir = cfg.Server.DataDir
		if cfg.Outbox.TableFilter != "" {
			outboxCfg.TableFilter = cfg.Outbox.TableFilter
		}
		if cfg.Outbox.TopicPrefix != "" {
			outboxCfg.TopicPrefix = cfg.Outbox.TopicPrefix
		}
		outboxCfg.DedicatedTable = cfg.Outbox.DedicatedTable

		outboxDispatcher = cdc.NewOutboxDispatcher(cdcStreamer, outboxCfg)

		for _, whURL := range cfg.Outbox.WebhookURLs {
			if whSink, err := cdc.NewWebhookSink(cdc.WebhookConfig{TargetURL: whURL}); err == nil {
				outboxDispatcher.RegisterSink(whSink)
			}
		}
		if cfg.Outbox.KafkaEndpoint != "" {
			if kSink, err := cdc.NewKafkaRESTSink(cdc.KafkaRESTConfig{Endpoint: cfg.Outbox.KafkaEndpoint}); err == nil {
				outboxDispatcher.RegisterSink(kSink)
			}
		}
		if cfg.Outbox.RabbitMQURL != "" {
			if rSink, err := cdc.NewRabbitMQHTTPSink(cdc.RabbitMQHTTPConfig{Endpoint: cfg.Outbox.RabbitMQURL}); err == nil {
				outboxDispatcher.RegisterSink(rSink)
			}
		}

		if err := outboxDispatcher.Start(context.Background()); err != nil {
			log.Printf("[LionSQL] Warning: Failed to start Outbox Dispatcher: %v", err)
		} else {
			server.SetOutboxDispatcher(outboxDispatcher)
		}
	}

	var httpServer *http.Server
	if cfg.Network.IsHTTPEnabled() {
		httpServer = &http.Server{
			Addr:              cfg.Network.HTTPAddr,
			Handler:           server.Handler(),
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	}

	sharedCache := server.BytecodePlanCache()
	var wireServer *wire.Server
	var tdsServer *tds.Server

	pgEnabled := cfg.Network.IsPGEnabled()
	tdsEnabled := cfg.Network.IsTDSEnabled()

	if pgEnabled {
		wireServer = wire.NewServerWithCache(cfg.Network.PGAddr, cat, eng, sharedCache)
		wireServer.SetCoordinator(server)
		wireServer.SetDevMode(cfg.Logging.DevMode)
	}
	if tdsEnabled {
		tdsServer = tds.NewServerWithCache(cfg.Network.TDSAddr, cat, eng, sharedCache)
		tdsServer.SetCoordinator(server)
		tdsServer.SetDevMode(cfg.Logging.DevMode)
	}

	if (pgEnabled || tdsEnabled) && cfg.Network.TLS.IsEnabled() {
		var tlsConfig *tls.Config
		if cfg.Network.TLS.CertFile != "" && cfg.Network.TLS.KeyFile != "" {
			tlsCfg, err := wire.LoadTLSConfig(cfg.Network.TLS.CertFile, cfg.Network.TLS.KeyFile)
			if err != nil {
				log.Fatalf("[LionSQL] Failed to load TLS certificate and key: %v", err)
			}
			tlsConfig = tlsCfg
		} else {
			log.Printf("[LionSQL] Generating self-signed TLS certificate for localhost...")
			tlsCfg, err := wire.NewSelfSignedTLSConfig()
			if err != nil {
				log.Fatalf("[LionSQL] Failed to generate self-signed TLS certificate: %v", err)
			}
			tlsConfig = tlsCfg
		}
		if wireServer != nil {
			wireServer.SetTLSConfig(tlsConfig)
			wireServer.SetRequireSSL(cfg.Network.TLS.RequireSSL)
			log.Printf("[LionSQL] PostgreSQL Wire SSL/TLS enabled (Require SSL: %v)", cfg.Network.TLS.RequireSSL)
		}
		if tdsServer != nil {
			tdsServer.SetTLSConfig(tlsConfig)
			tdsServer.SetRequireSSL(cfg.Network.TLS.RequireSSL)
			log.Printf("[LionSQL] TDS (MSSQL) Wire SSL/TLS enabled (Require SSL: %v)", cfg.Network.TLS.RequireSSL)
		}
	}

	if wireServer != nil {
		if err := wireServer.Start(); err != nil {
			if strings.Contains(err.Error(), "address already in use") {
				fmt.Printf("\n[LionSQL] Error: PostgreSQL wire port conflict (%s): %v\n", cfg.Network.PGAddr, err)
				fmt.Println("         Another LionSQL server or service is already listening on this port.")
				fmt.Println()
				os.Exit(1)
			}
			log.Fatalf("Failed to start PostgreSQL wire protocol listener: %v", err)
		}
		defer wireServer.Close()
	}

	if tdsServer != nil {
		if err := tdsServer.Start(); err != nil {
			if strings.Contains(err.Error(), "address already in use") {
				fmt.Printf("\n[LionSQL] Error: TDS/MSSQL wire port conflict (%s): %v\n", cfg.Network.TDSAddr, err)
				fmt.Println("         Another LionSQL server or service is already listening on this port.")
				fmt.Println()
				os.Exit(1)
			}
			log.Fatalf("Failed to start TDS wire protocol listener: %v", err)
		}
		defer tdsServer.Close()
	}

	// 10. Graceful Shutdown Notification Channel
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Printf(" LionSQL - Single binary High-Concurrency OLTP Database Server with T-SQL & Stored Procedure (v%s)\n", config.Version)
		fmt.Printf(" %s\n", config.CompactVersion())
		fmt.Println("--------------------------------------------------------------------------------")
		if loadedConfigPath != "" {
			fmt.Printf(" [Configuration]  %s (JSON Config File)\n", loadedConfigPath)
		} else {
			fmt.Println(" [Configuration]  Zero-Config (Built-in Defaults)")
		}
		if httpServer != nil && strings.Contains(cfg.Network.HTTPAddr, ":") {
			fmt.Printf(" [Web Dashboard]  http://%s/_/\n", cfg.Network.HTTPAddr)
			fmt.Printf(" [HTTP API]       http://%s/api/health\n", cfg.Network.HTTPAddr)
			fmt.Printf(" [CDC Stream]     http://%s/api/cdc/stream (NDJSON / SSE)\n", cfg.Network.HTTPAddr)
		} else {
			fmt.Println(" [Web Dashboard]  Disabled (opted out)")
			fmt.Println(" [HTTP API]       Disabled (opted out)")
		}
		fmt.Printf(" [Superuser Auth] %s (JWT Auth: %v)\n", cfg.Auth.AdminEmail, cfg.Auth.Enabled)
		fmt.Println(" [Supported GUI]  DBeaver / Beekeeper Studio / DbGate / Azure Data Studio")
		if wireServer != nil && strings.Contains(cfg.Network.PGAddr, ":") {
			fmt.Printf(" [Postgres Wire]  psql -h 127.0.0.1 -p %s (PostgreSQL v3 protocol, psql / pgx)\n", strings.Split(cfg.Network.PGAddr, ":")[1])
		} else {
			fmt.Println(" [Postgres Wire]  Disabled (opted out)")
		}
		if tdsServer != nil && strings.Contains(cfg.Network.TDSAddr, ":") {
			fmt.Printf(" [TDS / MSSQL]    sqlcmd -S 127.0.0.1,%s (TDS 7.x/8.0 protocol, sqlcmd / SSMS)\n", strings.Split(cfg.Network.TDSAddr, ":")[1])
		} else {
			fmt.Println(" [TDS / MSSQL]    Disabled (opted out)")
		}
		fmt.Printf(" [Data Directory] %s\n", cfg.Server.DataDir)
		fmt.Printf(" [Storage Engine] %s\n", cfg.Server.Engine)
		stats := server.Pool().Stats()
		fmt.Printf(" [Session Pooler] Max Workers: %d, Max Idle Sessions: %d (No PgBouncer required)\n", stats.MaxWorkers, stats.MaxIdleSessions)
		fmt.Printf(" [Memory Governor] max_memory: %s (Go runtime GC soft limit), work_mem: %s (External Merge Sort & HashJoin guard)\n", execution.FormatMemMB(execution.GetGlobalMemoryLimit()), execution.FormatMemSize(server.DefaultWorkMem()))
		if server.SlowQueries().LogFilePath() != "" {
			fmt.Printf(" [Slow Query Log] %s (Threshold: %v)\n", server.SlowQueries().LogFilePath(), server.SlowQueries().Threshold())
		}
		if server.GeneralLog().IsEnabled() {
			genPath := server.GeneralLog().LogFilePath()
			if genPath == "" {
				genPath = "in-memory only"
			}
			fmt.Printf(" [General Log]    %s (Enabled: true)\n", genPath)
		}
		if repMgr != nil {
			fmt.Printf(" [Replication]    Role: %s, Node: %s (HistoryCap: %d)\n", repMgr.Role(), cfg.Replication.NodeID, cfg.Replication.HistoryCap)
		}
		if outboxDispatcher != nil {
			fmt.Printf(" [Outbox Broker]  Active (Sinks: %d, Table Filter: '%s')\n", len(outboxDispatcher.Sinks()), cfg.Outbox.TableFilter)
		}
		if cfg.Logging.DevMode {
			fmt.Println(" [Developer Mode] Enabled (-dev: Real-time Wire Query & Error Tracing)")
		}
		if cfg.Auth.AdminPass == "1234567890" {
			fmt.Println("--------------------------------------------------------------------------------")
			fmt.Println(" [SECURITY NOTICE] Default administrator credentials active on localhost (127.0.0.1).")
			fmt.Println("                   (admin@lionsql.local / 1234567890)")
			fmt.Println("                   Set -admin-pass or LIONSQL_ADMIN_PASS before exposing to external networks.")
		}
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Println("Press Ctrl+C to stop server")

		if httpServer != nil {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				if strings.Contains(err.Error(), "address already in use") {
					fmt.Printf("\n[LionSQL] Error: HTTP service port conflict (%s): %v\n\n", cfg.Network.HTTPAddr, err)
					os.Exit(1)
				}
				log.Fatalf("HTTP server failure: %v", err)
			}
		}
	}()

	<-stop
	log.Println("\n[LionSQL] Initiating graceful shutdown...")

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	shutdownDone := make(chan struct{})
	go func() {
		if cdcStreamer != nil {
			cdcStreamer.Close()
		}
		if repMgr != nil {
			_ = repMgr.Close()
		}
		if tdsServer != nil {
			_ = tdsServer.Close()
		}
		if wireServer != nil {
			_ = wireServer.Close()
		}
		if httpServer != nil {
			_ = httpServer.Shutdown(ctx)
			_ = httpServer.Close()
		}
		_ = server.Close()
		_ = eng.Close()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		log.Println("[LionSQL] Server components stopped gracefully.")
	case <-ctx.Done():
		log.Println("[LionSQL] Shutdown deadline reached; terminating remaining tasks.")
	}

	return nil
}
