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
)

func main() {
	httpAddr := flag.String("http", "127.0.0.1:8090", "HTTP service and admin dashboard address")
	pgAddr := flag.String("pg", "127.0.0.1:5433", "PostgreSQL wire protocol listen address")
	dataDir := flag.String("dir", "data", "Data directory for database files")
	engineType := flag.String("engine", "wiredtiger", "Storage engine backend (wiredtiger, memory)")
	autoSeed := flag.Bool("seed", true, "Automatically seed sample data on startup")
	flag.Parse()

	log.Printf("[SQLion] Initializing database server...")
	log.Printf("[SQLion] Storage backend selected: %s", *engineType)

	var eng storage.Engine
	switch *engineType {
	case "wiredtiger":
		eng = wiredtiger.New()
		if err := eng.Open(*dataDir); err != nil {
			log.Printf("[SQLion] Notice: WiredTiger Cgo engine not enabled in this binary (%v)", err)
			log.Printf("[SQLion] Falling back to embedded ACID storage engine (memengine)")
			eng = memengine.New()
			if err := eng.Open(*dataDir); err != nil {
				log.Fatalf("Failed to open fallback storage engine: %v", err)
			}
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = wireServer.Close()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("[SQLion] HTTP server forced shutdown: %v", err)
	}

	log.Println("[SQLion] Server stopped cleanly.")
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
		jsCode, _ = proc.Transpile(plan)
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
