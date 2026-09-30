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
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/storage/wiredtiger"
	"sqlion/pkg/wire"
	"sqlion/pkg/wire/tds"
)

func main() {
	httpAddr := flag.String("http", "127.0.0.1:8090", "HTTP service and admin dashboard address")
	pgAddr := flag.String("pg", "127.0.0.1:5433", "PostgreSQL wire protocol listen address")
	tdsAddr := flag.String("tds", "127.0.0.1:1433", "TDS (MSSQL) wire protocol listen address")
	dataDir := flag.String("dir", "data", "Data directory for database files (default: data)")
	engineType := flag.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
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
	slowLog := flag.String("slow-log", "", "Path to slow query log file (e.g. data/slow_query.log, pass 'true' for default)")
	slowThreshold := flag.Float64("slow-threshold-ms", 50.0, "Threshold in milliseconds to record slow queries (default: 50ms)")
	generalLog := flag.String("general-log", "", "Enable general query logging (e.g. data/general.log, pass 'true' for default)")
	configPath := flag.String("config", "", "Path to JSON configuration file (default: auto-detect ./sqlion.json or <dir>/sqlion.json)")
	initConfig := flag.String("init-config", "", "Generate a default sqlion.json configuration file at specified path (or ./sqlion.json) and exit")

	// Daemon control flags
	daemonFlag := flag.Bool("d", false, "Run SQLion server as background daemon")
	daemonLongFlag := flag.Bool("daemon", false, "Run SQLion server as background daemon")
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
		if *slowLog == "true" || *slowLog == "1" {
			cfg.Logging.SlowQuery.Enabled = true
			if cfg.Logging.SlowQuery.File == "" {
				cfg.Logging.SlowQuery.File = filepath.Join(cfg.Server.DataDir, "slow_query.log")
			}
		} else if *slowLog == "false" || *slowLog == "0" {
			cfg.Logging.SlowQuery.Enabled = false
		} else {
			cfg.Logging.SlowQuery.Enabled = true
			cfg.Logging.SlowQuery.File = *slowLog
		}
	}
	if visited["general-log"] {
		if *generalLog == "true" || *generalLog == "1" {
			cfg.Logging.GeneralQuery.Enabled = true
			if cfg.Logging.GeneralQuery.File == "" {
				cfg.Logging.GeneralQuery.File = filepath.Join(cfg.Server.DataDir, "general.log")
			}
		} else if *generalLog == "false" || *generalLog == "0" {
			cfg.Logging.GeneralQuery.Enabled = false
		} else {
			cfg.Logging.GeneralQuery.Enabled = true
			cfg.Logging.GeneralQuery.File = *generalLog
		}
	}

	if strings.TrimSpace(cfg.Server.DataDir) == "" {
		cfg.Server.DataDir = "data"
	}

	resolvedPidFile := cfg.Server.PIDFile
	if resolvedPidFile == "" {
		resolvedPidFile = filepath.Join(cfg.Server.DataDir, "sqlion.pid")
	}
	resolvedLogFile := cfg.Server.LogFile
	if resolvedLogFile == "" {
		resolvedLogFile = filepath.Join(cfg.Server.DataDir, "sqlion.log")
	}

	// Handle daemon commands if not running as internal worker
	if !*isChildWorker {
		if handleDaemonCommands(
			*daemonFlag || *daemonLongFlag,
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
			if err := checkPortAvailable(cfg.Network.HTTPAddr); err != nil {
				fmt.Printf("\n[SQLion] Error: HTTP port conflict (%s): %v\n", cfg.Network.HTTPAddr, err)
				fmt.Println("         Another SQLion server or service is already listening on this port.")
				fmt.Println("         To view status:  ./bin/server -status")
				fmt.Println("         To stop daemon:  ./bin/server -stop")
				fmt.Println("         To restart:      ./bin/server -restart")
				fmt.Println()
				os.Exit(1)
			}
			if err := checkPortAvailable(cfg.Network.PGAddr); err != nil {
				fmt.Printf("\n[SQLion] Error: PostgreSQL wire port conflict (%s): %v\n", cfg.Network.PGAddr, err)
				fmt.Println("         Another SQLion server or service is already listening on this port.")
				fmt.Println("         To view status:  ./bin/server -status")
				fmt.Println("         To stop daemon:  ./bin/server -stop")
				fmt.Println("         To restart:      ./bin/server -restart")
				fmt.Println()
				os.Exit(1)
			}
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
		eng = memengine.New()
		if err := eng.Open(cfg.Server.DataDir); err != nil {
			log.Fatalf("Failed to open storage engine: %v", err)
		}
	default:
		log.Fatalf("Unknown storage engine: %s. Options: wiredtiger, memory", cfg.Server.Engine)
	}
	defer eng.Close()

	cat := catalog.New(eng)

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

	// Standalone CLI Restore Operation
	if *restoreSource != "" {
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
	if cfg.Logging.SlowQuery.Enabled && cfg.Logging.SlowQuery.File != "" {
		if err := server.SetSlowQueryLogFile(cfg.Logging.SlowQuery.File); err != nil {
			log.Printf("[SQLion] Warning: Failed to enable slow query log file: %v", err)
		} else {
			log.Printf("[SQLion] Slow query log file enabled: %s (threshold: %.1fms)", cfg.Logging.SlowQuery.File, cfg.Logging.SlowQuery.ThresholdMS)
		}
	}

	if cfg.Logging.GeneralQuery.Enabled {
		server.SetGeneralLogEnabled(true)
		if cfg.Logging.GeneralQuery.File != "" {
			if err := server.SetGeneralLogFile(cfg.Logging.GeneralQuery.File); err != nil {
				log.Printf("[SQLion] Warning: Failed to enable general query log file: %v", err)
			} else {
				log.Printf("[SQLion] General query log file enabled: %s", cfg.Logging.GeneralQuery.File)
			}
		}
	}

	httpServer := &http.Server{
		Addr:         cfg.Network.HTTPAddr,
		Handler:      server.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	sharedCache := server.BytecodePlanCache()
	wireServer := wire.NewServerWithCache(cfg.Network.PGAddr, cat, eng, sharedCache)
	tdsServer := tds.NewServerWithCache(cfg.Network.TDSAddr, cat, eng, sharedCache)
	if cfg.Network.TLS.Enabled || cfg.Network.TLS.CertFile != "" || cfg.Network.TLS.RequireSSL {
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
		wireServer.SetTLSConfig(tlsConfig)
		wireServer.SetRequireSSL(cfg.Network.TLS.RequireSSL)
		tdsServer.SetTLSConfig(tlsConfig)
		tdsServer.SetRequireSSL(cfg.Network.TLS.RequireSSL)
		log.Printf("[SQLion] PostgreSQL Wire SSL/TLS enabled (Require SSL: %v)", cfg.Network.TLS.RequireSSL)
		log.Printf("[SQLion] TDS (MSSQL) Wire SSL/TLS enabled (Require SSL: %v)", cfg.Network.TLS.RequireSSL)
	}
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
		fmt.Printf(" [Web Dashboard]  http://%s/_/\n", cfg.Network.HTTPAddr)
		fmt.Printf(" [HTTP API]       http://%s/api/health\n", cfg.Network.HTTPAddr)
		fmt.Printf(" [Superuser Auth] %s (JWT Auth: %v)\n", cfg.Auth.AdminEmail, cfg.Auth.Enabled)
		fmt.Printf(" [Postgres Wire]  psql -h 127.0.0.1 -p %s (DBeaver / psql)\n", strings.Split(cfg.Network.PGAddr, ":")[1])
		fmt.Printf(" [TDS / MSSQL]    sqlcmd -S 127.0.0.1,%s (DBeaver / Azure Data Studio / pymssql)\n", strings.Split(cfg.Network.TDSAddr, ":")[1])
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
		fmt.Println("------------------------------------------------------------")
		fmt.Println("Press Ctrl+C to stop server")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			if strings.Contains(err.Error(), "address already in use") {
				fmt.Printf("\n[SQLion] Error: HTTP service port conflict (%s): %v\n\n", cfg.Network.HTTPAddr, err)
				os.Exit(1)
			}
			log.Fatalf("HTTP server failure: %v", err)
		}
	}()

	<-stop
	log.Println("\n[SQLion] Initiating graceful shutdown...")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_ = tdsServer.Close()
	_ = wireServer.Close()
	_ = httpServer.Shutdown(ctx)
	_ = httpServer.Close()
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

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		return
	}
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(500),
	})

	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(1),
		"name":    storage.StringValue("Alice"),
		"balance": storage.FloatValue(1000.5),
		"status":  storage.StringValue("active"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(2),
		"name":    storage.StringValue("Bob"),
		"balance": storage.FloatValue(450.0),
		"status":  storage.StringValue("pending"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(3),
		"name":    storage.StringValue("Charlie"),
		"balance": storage.FloatValue(800.0),
		"status":  storage.StringValue("active"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(4),
		"name":    storage.StringValue("Diana"),
		"balance": storage.FloatValue(200.0),
		"status":  storage.StringValue("inactive"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(5),
		"name":    storage.StringValue("Evan"),
		"balance": storage.FloatValue(600.0),
		"status":  storage.StringValue("active"),
	})

	_ = tx.Commit()
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
