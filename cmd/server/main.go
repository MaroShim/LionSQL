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

	"sqlion/pkg/api"
	"sqlion/pkg/auth"
	"sqlion/pkg/backup"
	"sqlion/pkg/catalog"
	"sqlion/pkg/config"
	"sqlion/pkg/execution"
	"sqlion/pkg/proc"
	_ "sqlion/pkg/proc/parser/antlr"
	"sqlion/pkg/replication"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/storage/wiredtiger"
	"sqlion/pkg/wire"
	"sqlion/pkg/wire/tds"
)

func main() {
	httpAddr := flag.String("http", "127.0.0.1:8090", "HTTP service address (set to empty to disable)")
	pgAddr := flag.String("pg", "127.0.0.1:5433", "PostgreSQL wire protocol listen address (set to empty to disable)")
	tdsAddr := flag.String("tds", "127.0.0.1:1433", "TDS (MSSQL) wire protocol listen address (set to empty to disable)")
	defaultDataDir := resolveDefaultDataDir()
	dataDir := flag.String("dir", defaultDataDir, "Data directory for database files (default: adjacent to bin/ or 'data')")
	engineType := flag.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
	ephemeralFlag := flag.Bool("ephemeral", false, "Run in purely in-memory ephemeral mode with zero disk persistence (disables WAL, AOF, and RDB snapshots)")
	autoSeed := flag.Bool("seed", true, "Automatically seed sample data on startup")
	adminEmail := flag.String("admin-email", "admin@sqlion.local", "Default superuser email")
	adminPass := flag.String("admin-pass", "1234567890", "Default superuser password")
	enableAuth := flag.Bool("auth", false, "Enforce JWT authentication on protected REST endpoints")
	maxWorkers := flag.Int("max-workers", 0, "Max concurrent query execution workers (default: NumCPU * 4)")
	maxIdleSessions := flag.Int("max-idle-sessions", 64, "Max idle storage sessions to retain in pool")
	workMemStr := flag.String("work-mem", "64MB", "Default memory budget per query operator (e.g. 64MB, 128MB)")
	enableTLS := flag.Bool("tls", false, "Enable SSL/TLS encrypted connections for PostgreSQL and TDS wire protocols")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file")
	requireSSL := flag.Bool("require-ssl", false, "Require SSL/TLS for PostgreSQL and TDS wire connections")
	backupTarget := flag.String("backup", "", "Perform live point-in-time snapshot backup to target archive and exit")
	restoreSource := flag.String("restore", "", "Restore database from backup archive and exit")
	walArchiveDir := flag.String("wal-archive", "", "Path to WAL archive directory for continuous archiving and PITR")
	restoreTimeStr := flag.String("restore-time", "", "Target timestamp for Point-In-Time Recovery (RFC3339 or 'YYYY-MM-DD HH:MM:SS')")
	restoreLSN := flag.Int64("restore-lsn", 0, "Target Log Sequence Number (LSN) for Point-In-Time Recovery")
	archiveWalFile := flag.String("archive-wal", "", "Archive a specific WAL file into the WAL archive directory and exit")
	slowLog := flag.String("slow-log", "", "Path to slow query log file (e.g. data/slow_query.log, pass 'true' for default)")
	slowThreshold := flag.Float64("slow-threshold-ms", 50.0, "Threshold in milliseconds to record slow queries (default: 50ms)")
	generalLog := flag.String("general-log", "", "Enable general query logging (e.g. data/general.log, pass 'true' for default)")
	replicaOf := flag.String("replica-of", "", "Primary server HTTP URL to replicate from (e.g. http://127.0.0.1:8090)")
	nodeID := flag.String("node-id", "", "Unique replication node identifier")
	configPath := flag.String("config", "", "Path to JSON configuration file (default: auto-detect ./sqlion.json or <dir>/sqlion.json)")
	initConfig := flag.String("init-config", "", "Generate a default sqlion.json configuration file at specified path (or ./sqlion.json) and exit")
	doctorFlag := flag.Bool("doctor", false, "Run self-diagnostic health checks on host environment, resources, ports, and storage engines and exit")
	devFlag := flag.Bool("dev", false, "Enable developer mode (verbose wire query and parameter logging on stdout/stderr)")

	// Daemon control flags
	var daemon bool
	flag.BoolVar(&daemon, "d", false, "Run SQLion server as background daemon")
	flag.BoolVar(&daemon, "daemon", false, "Run SQLion server as background daemon")
	stopFlag := flag.Bool("stop", false, "Stop running SQLion daemon")
	statusFlag := flag.Bool("status", false, "Check status of running SQLion daemon")
	restartFlag := flag.Bool("restart", false, "Restart SQLion daemon")
	pidFile := flag.String("pidfile", "", "Path to PID file (default: <dir>/sqlion.pid)")
	logFile := flag.String("logfile", "", "Path to log file (default: <dir>/sqlion.log)")
	isChildWorker := flag.Bool("is-child-worker", false, "Internal flag for daemon worker child")

	flag.Parse()

	// 1. Standalone Config Generator (--init-config)
	if *initConfig != "" {
		targetPath := *initConfig
		if targetPath == "true" || targetPath == "1" {
			targetPath = "sqlion.json"
		}
		if err := config.WriteToFileAtomic(config.DefaultConfig(), targetPath); err != nil {
			log.Fatalf("[SQLion] Failed to initialize config file: %v", err)
		}
		fmt.Printf("\n[SQLion] Default configuration generated successfully: %s\n\n", targetPath)
		return
	}

	// 2. Load Configuration (Auto-Discovery: explicit > ./sqlion.json > <dir>/sqlion.json > Built-in Defaults)
	cfg, loadedConfigPath, err := config.AutoDiscoverConfig(*configPath, *dataDir)
	if err != nil {
		log.Fatalf("[SQLion] Configuration error: %v", err)
	}

	// 3. Apply CLI Argument Overrides (Deterministic Hierarchy: CLI Flags > JSON Config > Built-in Defaults)
	visited := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
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
	if visited["dir"] {
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
	if visited["pidfile"] {
		cfg.Server.PIDFile = *pidFile
	}
	if visited["logfile"] {
		cfg.Server.LogFile = *logFile
	}
	if visited["slow-threshold-ms"] {
		cfg.Logging.SlowQuery.ThresholdMS = *slowThreshold
	}
	if visited["slow-log"] {
		cfg.Logging.SlowQuery.File = resolveLogFilePath(*slowLog, "slow_query.log", cfg.Server.DataDir)
	}
	if visited["general-log"] {
		cfg.Logging.GeneralQuery.File = resolveLogFilePath(*generalLog, "general.log", cfg.Server.DataDir)
	}
	if visited["dev"] {
		cfg.Logging.DevMode = *devFlag
	}
	if visited["replica-of"] {
		cfg.Replication.PrimaryURL = *replicaOf
		if *replicaOf != "" {
			cfg.Replication.Role = "REPLICA"
			cfg.Replication.Enabled = true
		}
	}
	if visited["node-id"] {
		cfg.Replication.NodeID = *nodeID
	}

	if strings.TrimSpace(cfg.Server.DataDir) == "" {
		cfg.Server.DataDir = defaultDataDir
	}

	resolvedPidFile := cfg.Server.PIDFile
	if resolvedPidFile == "" {
		resolvedPidFile = filepath.Join(cfg.Server.DataDir, "sqlion.pid")
	}
	resolvedLogFile := cfg.Server.LogFile
	if resolvedLogFile == "" {
		resolvedLogFile = filepath.Join(cfg.Server.DataDir, "sqlion.log")
	}

	// 4. Standalone Self-Diagnostic Health Verification (--doctor)
	if *doctorFlag {
		if err := RunDoctor(cfg); err != nil {
			os.Exit(1)
		}
		return
	}

	// Handle daemon commands if not running as internal worker
	if !*isChildWorker {
		if handleDaemonCommands(
			daemon,
			*stopFlag,
			*statusFlag,
			*restartFlag,
			cfg.Server.DataDir,
			resolvedPidFile,
			resolvedLogFile,
			cfg.Network.HTTPAddr,
			cfg.Network.PGAddr,
			cfg.Network.TDSAddr,
			cfg.Server.Engine,
		) {
			return
		}

		// Prevent multiple server instances from running concurrently on the same data directory
		if existingPid, err := readPidFile(resolvedPidFile); err == nil && isProcessAlive(existingPid) && existingPid != os.Getpid() {
			fmt.Printf("\n[SQLion] Error: Another SQLion server instance (PID %d) is already running for data directory '%s'.\n", existingPid, cfg.Server.DataDir)
			fmt.Println("         To view status:  ./bin/server -status")
			fmt.Println("         To stop daemon:  ./bin/server -stop")
			fmt.Println("         To restart:      ./bin/server -restart")
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
				if err := checkPortAvailable(cfg.Network.HTTPAddr); err != nil {
					fmt.Printf("\n[SQLion] Error: HTTP port conflict (%s): %v\n", cfg.Network.HTTPAddr, err)
					fmt.Println("         Another SQLion server or service is already listening on this port.")
					fmt.Println("         To view status:  ./bin/server -status")
					fmt.Println("         To stop daemon:  ./bin/server -stop")
					fmt.Println("         To restart:      ./bin/server -restart")
					fmt.Println()
					os.Exit(1)
				}
			}
			if cfg.Network.IsPGEnabled() {
				if err := checkPortAvailable(cfg.Network.PGAddr); err != nil {
					fmt.Printf("\n[SQLion] Error: PostgreSQL wire port conflict (%s): %v\n", cfg.Network.PGAddr, err)
					fmt.Println("         Another SQLion server or service is already listening on this port.")
					fmt.Println("         To view status:  ./bin/server -status")
					fmt.Println("         To stop daemon:  ./bin/server -stop")
					fmt.Println("         To restart:      ./bin/server -restart")
					fmt.Println()
					os.Exit(1)
				}
			}
			if cfg.Network.IsTDSEnabled() {
				if err := checkPortAvailable(cfg.Network.TDSAddr); err != nil {
					fmt.Printf("\n[SQLion] Error: TDS/MSSQL wire port conflict (%s): %v\n", cfg.Network.TDSAddr, err)
					fmt.Println("         Another SQLion server or service is already listening on this port.")
					fmt.Println("         To view status:  ./bin/server -status")
					fmt.Println("         To stop daemon:  ./bin/server -stop")
					fmt.Println("         To restart:      ./bin/server -restart")
					fmt.Println()
					os.Exit(1)
				}
			}
		}
	}

	log.Printf("[SQLion] Initializing database server...")
	log.Printf("[SQLion] Storage backend selected: %s", cfg.Server.Engine)

	var eng storage.Engine
	switch cfg.Server.Engine {
	case "wiredtiger":
		eng = wiredtiger.New()
		if err := eng.Open(cfg.Server.DataDir); err != nil {
			if strings.Contains(err.Error(), "Resource busy") || strings.Contains(err.Error(), "already being managed") {
				fmt.Printf("\n[SQLion] Error: Database directory '%s' is locked by another running process.\n", cfg.Server.DataDir)
				fmt.Println("         Please stop the existing SQLion instance before starting a new one.")
				fmt.Println()
				os.Exit(1)
			}
			log.Fatalf("[SQLion] Failed to open WiredTiger storage engine: %v. If you wish to use the in-memory engine, specify --engine=memory explicitly.", err)
		}
	case "memory":
		memEng := memengine.New()
		opts := memengine.DefaultOptions(cfg.Server.DataDir)
		if cfg.Server.Ephemeral {
			opts.EnableAOF = false
			opts.EnableRDB = false
			opts.DataDir = ""
			log.Printf("[SQLion] In-Memory engine running in EPHEMERAL mode (pure in-memory, zero disk persistence)")
		} else {
			log.Printf("[SQLion] In-Memory engine running in PERSISTENT mode (AOF: %v, RDB: %v, dir: %s)", opts.EnableAOF, opts.EnableRDB, cfg.Server.DataDir)
		}
		if err := memEng.OpenWithOptions(opts); err != nil {
			log.Fatalf("Failed to open storage engine: %v", err)
		}
		eng = memEng
	default:
		log.Fatalf("Unknown storage engine: %s. Options: wiredtiger, memory", cfg.Server.Engine)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	if !*ephemeralFlag && cfg.Server.DataDir != "" {
		if err := cat.SetPersistenceDir(cfg.Server.DataDir); err != nil {
			log.Printf("[SQLion] Warning: Failed to load CBO statistics: %v", err)
		} else if stats := cat.ListTableStats(); len(stats) > 0 {
			log.Printf("[SQLion] Restored CBO statistics for %d tables from %s/cbo_stats.json", len(stats), cfg.Server.DataDir)
		}
	}

	// Standalone CLI Backup Operation
	if *backupTarget != "" {
		log.Printf("[SQLion] Initiating online snapshot backup to %s...", *backupTarget)
		manifest, err := backup.CreateBackupFile(*backupTarget, eng, cat)
		if err != nil {
			log.Fatalf("[SQLion] Backup failed: %v", err)
		}
		log.Printf("[SQLion] Backup successfully completed: %d tables, %d total records, %d procedures saved to %s",
			manifest.TablesCount, manifest.TotalRecords, manifest.ProceduresCount, *backupTarget)
		return
	}

	// Standalone CLI WAL Segment Archiving Operation
	if *archiveWalFile != "" {
		archiveDir := *walArchiveDir
		if archiveDir == "" {
			archiveDir = filepath.Join(cfg.Server.DataDir, "wal_archive")
		}
		archiver, err := backup.NewWALArchiver(archiveDir)
		if err != nil {
			log.Fatalf("[SQLion] Failed to initialize WAL archiver: %v", err)
		}
		seg, err := archiver.ArchiveSegment(*archiveWalFile)
		if err != nil {
			log.Fatalf("[SQLion] Failed to archive WAL segment: %v", err)
		}
		log.Printf("[SQLion] WAL segment archived successfully: %s (LSN %d-%d, %d records)",
			seg.Filename, seg.MinLSN, seg.MaxLSN, seg.RecordCount)
		return
	}

	// Standalone CLI Restore / PITR Operation
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
				log.Fatalf("[SQLion] Invalid --restore-time format %q: %v", *restoreTimeStr, parseErr)
			}
		}

		if !targetTime.IsZero() || *restoreLSN > 0 {
			log.Printf("[SQLion] Initiating Point-In-Time Recovery (PITR)... (target_time: %v, target_lsn: %d)", targetTime, *restoreLSN)
			pitrOpts := backup.PITROptions{
				RestoreOptions: backup.DefaultRestoreOptions(),
				TargetTime:     targetTime,
				TargetLSN:      *restoreLSN,
			}
			res, err := backup.RestorePITR(*restoreSource, archiveDir, eng, cat, pitrOpts)
			if err != nil {
				log.Fatalf("[SQLion] PITR restoration failed: %v", err)
			}
			log.Printf("[SQLion] PITR restoration successfully completed: %d WAL segments replayed, %d records applied, final LSN: %d, final time: %v (duration: %v)",
				res.ArchivedSegmentsParsed, res.WALRecordsReplayed, res.FinalLSN, res.FinalTimestamp, res.Duration)
			return
		}

		log.Printf("[SQLion] Initiating database restore from %s...", *restoreSource)
		res, err := backup.RestoreBackupFile(*restoreSource, eng, cat, backup.DefaultRestoreOptions())
		if err != nil {
			log.Fatalf("[SQLion] Restore failed: %v", err)
		}
		log.Printf("[SQLion] Restore successfully completed: %d tables, %d records, %d procedures restored in %v",
			res.TablesRestored, res.RecordsRestored, res.ProceduresRestored, res.Duration)
		return
	}

	// Auto-seed if requested
	if cfg.Server.AutoSeed {
		seedSampleAccounts(eng)
		seedSampleProcedure(cat)
	}

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

	if cfg.Resources.WorkMem != "" {
		if bytes, err := execution.ParseMemSize(cfg.Resources.WorkMem); err == nil && bytes > 0 {
			server.SetDefaultWorkMem(bytes)
		}
	}

	if cfg.Logging.SlowQuery.ThresholdMS > 0 {
		server.SetSlowQueryThreshold(time.Duration(cfg.Logging.SlowQuery.ThresholdMS * float64(time.Millisecond)))
	}
	if cfg.Logging.SlowQuery.IsEnabled() {
		if err := server.SetSlowQueryLogFile(cfg.Logging.SlowQuery.File); err != nil {
			log.Printf("[SQLion] Warning: Failed to enable slow query log file: %v", err)
		} else {
			log.Printf("[SQLion] Slow query log file enabled: %s (threshold: %.1fms)", cfg.Logging.SlowQuery.File, cfg.Logging.SlowQuery.ThresholdMS)
		}
	}

	if cfg.Logging.GeneralQuery.IsEnabled() {
		server.SetGeneralLogEnabled(true)
		if err := server.SetGeneralLogFile(cfg.Logging.GeneralQuery.File); err != nil {
			log.Printf("[SQLion] Warning: Failed to enable general query log file: %v", err)
		} else {
			log.Printf("[SQLion] General query log file enabled: %s", cfg.Logging.GeneralQuery.File)
		}
	}

	var repMgr *replication.Manager
	if cfg.Replication.Enabled {
		role := replication.Role(strings.ToUpper(cfg.Replication.Role))
		if role == "" {
			role = replication.RolePrimary
		}
		var err error
		repMgr, err = replication.NewManager(replication.Config{
			NodeID:     cfg.Replication.NodeID,
			Role:       role,
			PrimaryURL: cfg.Replication.PrimaryURL,
			HistoryCap: cfg.Replication.HistoryCap,
			Engine:     eng,
		})
		if err != nil {
			log.Fatalf("[SQLion] Failed to initialize replication manager: %v", err)
		}
		server.SetReplicationManager(repMgr)
		if err := repMgr.Start(context.Background()); err != nil {
			log.Fatalf("[SQLion] Failed to start replication: %v", err)
		}
	}

	var httpServer *http.Server
	if cfg.Network.IsHTTPEnabled() {
		httpServer = &http.Server{
			Addr:         cfg.Network.HTTPAddr,
			Handler:      server.Handler(),
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		}
	}

	sharedCache := server.BytecodePlanCache()
	var wireServer *wire.Server
	var tdsServer *tds.Server

	pgEnabled := cfg.Network.IsPGEnabled()
	tdsEnabled := cfg.Network.IsTDSEnabled()

	if pgEnabled {
		wireServer = wire.NewServerWithCache(cfg.Network.PGAddr, cat, eng, sharedCache)
		wireServer.SetDevMode(cfg.Logging.DevMode)
	}
	if tdsEnabled {
		tdsServer = tds.NewServerWithCache(cfg.Network.TDSAddr, cat, eng, sharedCache)
		tdsServer.SetDevMode(cfg.Logging.DevMode)
	}

	if (pgEnabled || tdsEnabled) && cfg.Network.TLS.IsEnabled() {
		var tlsConfig *tls.Config
		if cfg.Network.TLS.CertFile != "" && cfg.Network.TLS.KeyFile != "" {
			tlsCfg, err := wire.LoadTLSConfig(cfg.Network.TLS.CertFile, cfg.Network.TLS.KeyFile)
			if err != nil {
				log.Fatalf("[SQLion] Failed to load TLS certificate and key: %v", err)
			}
			tlsConfig = tlsCfg
		} else {
			log.Printf("[SQLion] Generating self-signed TLS certificate for localhost...")
			tlsCfg, err := wire.NewSelfSignedTLSConfig()
			if err != nil {
				log.Fatalf("[SQLion] Failed to generate self-signed TLS certificate: %v", err)
			}
			tlsConfig = tlsCfg
		}
		if wireServer != nil {
			wireServer.SetTLSConfig(tlsConfig)
			wireServer.SetRequireSSL(cfg.Network.TLS.RequireSSL)
			log.Printf("[SQLion] PostgreSQL Wire SSL/TLS enabled (Require SSL: %v)", cfg.Network.TLS.RequireSSL)
		}
		if tdsServer != nil {
			tdsServer.SetTLSConfig(tlsConfig)
			tdsServer.SetRequireSSL(cfg.Network.TLS.RequireSSL)
			log.Printf("[SQLion] TDS (MSSQL) Wire SSL/TLS enabled (Require SSL: %v)", cfg.Network.TLS.RequireSSL)
		}
	}

	if wireServer != nil {
		if err := wireServer.Start(); err != nil {
			if strings.Contains(err.Error(), "address already in use") {
				fmt.Printf("\n[SQLion] Error: PostgreSQL wire port conflict (%s): %v\n", cfg.Network.PGAddr, err)
				fmt.Println("         Another SQLion server or service is already listening on this port.")
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
				fmt.Printf("\n[SQLion] Error: TDS/MSSQL wire port conflict (%s): %v\n", cfg.Network.TDSAddr, err)
				fmt.Println("         Another SQLion server or service is already listening on this port.")
				fmt.Println()
				os.Exit(1)
			}
			log.Fatalf("Failed to start TDS wire protocol listener: %v", err)
		}
		defer tdsServer.Close()
	}

	// Graceful shutdown channel
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Println("------------------------------------------------------------")
		fmt.Println(" SQLion - High-Concurrency OLTP & Stored Procedure Server")
		fmt.Println("------------------------------------------------------------")
		if loadedConfigPath != "" {
			fmt.Printf(" [Configuration]  %s (JSON Config File)\n", loadedConfigPath)
		} else {
			fmt.Println(" [Configuration]  Zero-Config (Built-in Defaults)")
		}
		if httpServer != nil && strings.Contains(cfg.Network.HTTPAddr, ":") {
			fmt.Printf(" [Web Dashboard]  http://%s/_/\n", cfg.Network.HTTPAddr)
			fmt.Printf(" [HTTP API]       http://%s/api/health\n", cfg.Network.HTTPAddr)
		} else {
			fmt.Println(" [Web Dashboard]  Disabled (opted out)")
			fmt.Println(" [HTTP API]       Disabled (opted out)")
		}
		fmt.Printf(" [Superuser Auth] %s (JWT Auth: %v)\n", cfg.Auth.AdminEmail, cfg.Auth.Enabled)
		if wireServer != nil && strings.Contains(cfg.Network.PGAddr, ":") {
			fmt.Printf(" [Postgres Wire]  psql -h 127.0.0.1 -p %s (Beekeeper / DBeaver / psql)\n", strings.Split(cfg.Network.PGAddr, ":")[1])
		} else {
			fmt.Println(" [Postgres Wire]  Disabled (opted out)")
		}
		if tdsServer != nil && strings.Contains(cfg.Network.TDSAddr, ":") {
			fmt.Printf(" [TDS / MSSQL]    sqlcmd -S 127.0.0.1,%s (Beekeeper / DBeaver / Azure Data Studio / pymssql)\n", strings.Split(cfg.Network.TDSAddr, ":")[1])
		} else {
			fmt.Println(" [TDS / MSSQL]    Disabled (opted out)")
		}
		fmt.Printf(" [Data Directory] %s\n", cfg.Server.DataDir)
		fmt.Printf(" [Storage Engine] %s\n", cfg.Server.Engine)
		stats := server.Pool().Stats()
		fmt.Printf(" [Session Pooler] Max Workers: %d, Max Idle Sessions: %d (No PgBouncer required)\n", stats.MaxWorkers, stats.MaxIdleSessions)
		fmt.Printf(" [Memory Governor] work_mem: %s (External Merge Sort spill-to-disk enabled)\n", execution.FormatMemSize(server.DefaultWorkMem()))
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
		if cfg.Logging.DevMode {
			fmt.Println(" [Developer Mode] Enabled (-dev: Real-time Wire Query & Error Tracing)")
		}
		fmt.Println("------------------------------------------------------------")
		fmt.Println("Press Ctrl+C to stop server")

		if httpServer != nil {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				if strings.Contains(err.Error(), "address already in use") {
					fmt.Printf("\n[SQLion] Error: HTTP service port conflict (%s): %v\n\n", cfg.Network.HTTPAddr, err)
					os.Exit(1)
				}
				log.Fatalf("HTTP server failure: %v", err)
			}
		}
	}()

	<-stop
	log.Println("\n[SQLion] Initiating graceful shutdown...")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

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

	if curPid, err := readPidFile(resolvedPidFile); err == nil && curPid == os.Getpid() {
		_ = os.Remove(resolvedPidFile)
	}

	log.Println("[SQLion] Server stopped cleanly.")
	os.Exit(0)
}

func seedSampleAccounts(eng storage.Engine) {
	accountsSchema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = eng.CreateTable(accountsSchema)

	customersSchema := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeFloat, Nullable: false},
			{Name: "status", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	_ = eng.CreateTable(customersSchema)

	session, err := eng.NewSession()
	if err != nil {
		return
	}
	defer session.Close()

	// Seed accounts if empty
	tx, err := session.BeginTx(storage.TxOptions{})
	if err == nil {
		cur, err := tx.Scan("accounts")
		hasRows := false
		if err == nil {
			hasRows = cur.Next()
			cur.Close()
		}
		if !hasRows {
			err1 := tx.Insert("accounts", storage.Record{
				"id":      storage.IntValue(1),
				"holder":  storage.StringValue("Alice"),
				"balance": storage.IntValue(1000),
			})
			err2 := tx.Insert("accounts", storage.Record{
				"id":      storage.IntValue(2),
				"holder":  storage.StringValue("Bob"),
				"balance": storage.IntValue(500),
			})
			if err1 != nil || err2 != nil {
				log.Printf("[SQLion] Seed accounts insert error: %v, %v", err1, err2)
			}
			if err := tx.Commit(); err != nil {
				log.Printf("[SQLion] Seed accounts commit error: %v", err)
			}
		} else {
			_ = tx.Rollback()
		}
	}

	// Seed customers if empty
	txCust, err := session.BeginTx(storage.TxOptions{})
	if err == nil {
		cur, err := txCust.Scan("customers")
		hasRows := false
		if err == nil {
			hasRows = cur.Next()
			cur.Close()
		}
		if !hasRows {
			_ = txCust.Insert("customers", storage.Record{
				"id":      storage.IntValue(1),
				"name":    storage.StringValue("Alice"),
				"balance": storage.FloatValue(1000.5),
				"status":  storage.StringValue("active"),
			})
			_ = txCust.Insert("customers", storage.Record{
				"id":      storage.IntValue(2),
				"name":    storage.StringValue("Bob"),
				"balance": storage.FloatValue(450.0),
				"status":  storage.StringValue("pending"),
			})
			_ = txCust.Insert("customers", storage.Record{
				"id":      storage.IntValue(3),
				"name":    storage.StringValue("Charlie"),
				"balance": storage.FloatValue(800.0),
				"status":  storage.StringValue("active"),
			})
			_ = txCust.Insert("customers", storage.Record{
				"id":      storage.IntValue(4),
				"name":    storage.StringValue("Diana"),
				"balance": storage.FloatValue(200.0),
				"status":  storage.StringValue("inactive"),
			})
			_ = txCust.Insert("customers", storage.Record{
				"id":      storage.IntValue(5),
				"name":    storage.StringValue("Evan"),
				"balance": storage.FloatValue(600.0),
				"status":  storage.StringValue("active"),
			})
			_ = txCust.Commit()
		} else {
			_ = txCust.Rollback()
		}
	}
}

func seedSampleProcedure(cat *catalog.Catalog) {
	script := `CREATE PROCEDURE TransferFunds
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    DECLARE @sender_bal INT;
    SELECT balance INTO @sender_bal FROM accounts WHERE id = @from;

    IF @sender_bal < @amount
    BEGIN
        RAISERROR('Insufficient funds in sender balance');
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;
    PRINT 'Money successfully transferred!';
    RETURN 0;
END;`

	var jsCode string
	lexer := proc.NewLexer(script)
	parser := proc.NewParser(lexer)
	if plan, err := parser.ParseProcedure(); err == nil {
		if prog, err := proc.CompileProcedure(plan); err == nil {
			jsCode = prog.Disassemble()
		}
	}

	_ = cat.RegisterProcedure(&catalog.Procedure{
		Name: "TransferFunds",
		Params: []catalog.ParamDef{
			{Name: "@from", Type: storage.TypeInt},
			{Name: "@to", Type: storage.TypeInt},
			{Name: "@amount", Type: storage.TypeInt},
		},
		Body:   script,
		JSCode: jsCode,
	})
}

func checkPortAvailable(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	_ = ln.Close()
	return nil
}

func resolveLogFilePath(flagVal, defaultName, dataDir string) string {
	switch flagVal {
	case "false", "0", "":
		return ""
	case "true", "1":
		return filepath.Join(dataDir, defaultName)
	default:
		return flagVal
	}
}

// resolveDefaultDataDir locates the default data directory.
// If the server binary is inside a 'bin' directory (e.g. <root>/bin/server),
// it resolves to the sibling 'data' directory at the same level (<root>/data).
// Otherwise, it falls back to "data" in the current working directory.
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

