package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sqlserver/pkg/api"
	"sqlserver/pkg/catalog"
	"sqlserver/pkg/storage"
	"sqlserver/pkg/storage/memengine"
	"sqlserver/pkg/storage/wiredtiger"
)

func main() {
	httpAddr := flag.String("http", "127.0.0.1:8090", "HTTP service and admin dashboard address")
	dataDir := flag.String("dir", "data", "Data directory for database files")
	engineType := flag.String("engine", "memory", "Storage engine backend (memory, wiredtiger)")
	autoSeed := flag.Bool("seed", true, "Automatically seed sample data on startup")
	flag.Parse()

	log.Printf("[SQLServer] Initializing database server...")
	log.Printf("[SQLServer] Storage backend selected: %s", *engineType)

	var eng storage.Engine
	switch *engineType {
	case "wiredtiger":
		eng = wiredtiger.New()
	case "memory":
		eng = memengine.New()
	default:
		log.Fatalf("Unknown storage engine: %s. Options: memory, wiredtiger", *engineType)
	}

	if err := eng.Open(*dataDir); err != nil {
		log.Fatalf("Failed to open storage engine: %v", err)
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

	// Graceful shutdown channel
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Println("------------------------------------------------------------")
		fmt.Println(" SQLServer - High-Concurrency OLTP & Stored Procedure Server")
		fmt.Println("------------------------------------------------------------")
		fmt.Printf(" [Web Dashboard]  http://%s/_/\n", *httpAddr)
		fmt.Printf(" [HTTP API]       http://%s/api/health\n", *httpAddr)
		fmt.Printf(" [Data Directory] %s\n", *dataDir)
		fmt.Printf(" [Storage Engine] %s\n", *engineType)
		fmt.Println("------------------------------------------------------------")
		fmt.Println("Press Ctrl+C to stop server")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failure: %v", err)
		}
	}()

	<-stop
	log.Println("\n[SQLServer] Initiating graceful shutdown...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("[SQLServer] HTTP server forced shutdown: %v", err)
	}

	log.Println("[SQLServer] Server stopped cleanly.")
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

	_ = cat.RegisterProcedure(&catalog.Procedure{
		Name: "TransferFunds",
		Params: []catalog.ParamDef{
			{Name: "@from", Type: storage.TypeInt},
			{Name: "@to", Type: storage.TypeInt},
			{Name: "@amount", Type: storage.TypeInt},
		},
		Body: script,
	})
}
