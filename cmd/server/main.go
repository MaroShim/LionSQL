package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/proc"
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

	if strings.TrimSpace(*dataDir) == "" {
		*dataDir = "data"
	}

	// Handle daemon commands if not running as internal worker
	if !*isChildWorker {
		if handleDaemonCommands(
			*daemonFlag || *daemonLongFlag,
			*stopFlag,
			*statusFlag,
			*restartFlag,
			*dataDir,
			*pidFile,
			*logFile,
			*httpAddr,
			*pgAddr,
			*tdsAddr,
			*engineType,
		) {
			return
		}
	}

	log.Printf("[SQLion] Initializing database server...")
	log.Printf("[SQLion] Storage backend selected: %s", *engineType)

	var eng storage.Engine
	switch *engineType {
	case "wiredtiger":
		eng = wiredtiger.New()
		if err := eng.Open(*dataDir); err != nil {
			log.Fatalf("[SQLion] Failed to open WiredTiger storage engine: %v. If you wish to use the in-memory engine, specify --engine=memory explicitly.", err)
		}
	case "memory":
		eng = memengine.New()
		if err := eng.Open(*dataDir); err != nil {
			log.Fatalf("Failed to open storage engine: %v", err)
		}
	default:
		log.Fatalf("Unknown storage engine: %s. Options: wiredtiger, memory", *engineType)
	}
	defer eng.Close()

	cat := catalog.New(eng)

	// Auto-seed if requested
	if *autoSeed {
		seedSampleAccounts(eng)
		seedSampleProcedure(cat)
	}

	server := api.NewServer(cat, eng)
	httpServer := &http.Server{
		Addr:         *httpAddr,
		Handler:      server.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	wireServer := wire.NewServer(*pgAddr, cat, eng)
	if err := wireServer.Start(); err != nil {
		log.Fatalf("Failed to start PostgreSQL wire protocol listener: %v", err)
	}
	defer wireServer.Close()

	tdsServer := tds.NewServer(*tdsAddr, cat, eng)
	if err := tdsServer.Start(); err != nil {
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
		fmt.Printf(" [Web Dashboard]  http://%s/_/\n", *httpAddr)
		fmt.Printf(" [HTTP API]       http://%s/api/health\n", *httpAddr)
		fmt.Printf(" [Postgres Wire]  psql -h 127.0.0.1 -p %s (DBeaver / psql)\n", strings.Split(*pgAddr, ":")[1])
		fmt.Printf(" [TDS / MSSQL]    sqlcmd -S 127.0.0.1,%s (DBeaver / Azure Data Studio / pymssql)\n", strings.Split(*tdsAddr, ":")[1])
		fmt.Printf(" [Data Directory] %s\n", *dataDir)
		fmt.Printf(" [Storage Engine] %s\n", *engineType)
		fmt.Println("------------------------------------------------------------")
		fmt.Println("Press Ctrl+C to stop server")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
	_ = eng.Close()

	log.Println("[SQLion] Server stopped cleanly.")
	os.Exit(0)
}

func seedSampleAccounts(eng storage.Engine) {
	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)

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
