package wire_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire"
	"lionsql/pkg/wire/tds"
)

// startTestPGServer launches an in-memory PGWire server on a dynamic port.
func startTestPGServer(t *testing.T) (*wire.Server, int) {
	t.Helper()
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)

	srv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start PG wire server: %v", err)
	}

	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		srv.Close()
		t.Fatalf("Failed to get TCP address from PG server")
	}
	return srv, tcpAddr.Port
}

// startTestTDSServer launches an in-memory TDS server on a dynamic port.
func startTestTDSServer(t *testing.T) (*tds.Server, int) {
	t.Helper()
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}

	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		srv.Close()
		t.Fatalf("Failed to get TCP address from TDS server")
	}
	return srv, tcpAddr.Port
}

// -----------------------------------------------------------------------------
// 1. GoCD Compatibility Tests across PGWire (:5433) and TDS (:1433)
// -----------------------------------------------------------------------------

func TestGoCD_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/gocd?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open postgres failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db.PingContext (PGWire) failed: %v", err)
	}

	// 1. Liquibase lock table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE DATABASECHANGELOGLOCK (
			ID INT PRIMARY KEY,
			LOCKED INT NOT NULL,
			LOCKEDBY VARCHAR(255)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE DATABASECHANGELOGLOCK failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO DATABASECHANGELOGLOCK (ID, LOCKED, LOCKEDBY) VALUES (1, 0, NULL);`)
	if err != nil {
		t.Fatalf("INSERT DATABASECHANGELOGLOCK failed: %v", err)
	}

	// 2. GoCD Core Tables
	_, err = db.ExecContext(ctx, `
		CREATE TABLE AGENTS (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			UUID VARCHAR(255) NOT NULL,
			HOSTNAME VARCHAR(255) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE AGENTS failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE PIPELINES (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			NAME VARCHAR(255) NOT NULL,
			COUNTER INT DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE PIPELINES failed: %v", err)
	}

	// 3. Insert and Query
	_, err = db.ExecContext(ctx, `INSERT INTO AGENTS (UUID, HOSTNAME) VALUES ('gocd-agent-01', 'worker-linux-01');`)
	if err != nil {
		t.Fatalf("INSERT AGENTS failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO PIPELINES (NAME, COUNTER) VALUES ('deploy-pipeline', 10);`)
	if err != nil {
		t.Fatalf("INSERT PIPELINES failed: %v", err)
	}

	var hostname, pipelineName string
	var counter int
	err = db.QueryRowContext(ctx, `SELECT a.HOSTNAME, p.NAME, p.COUNTER FROM AGENTS a, PIPELINES p WHERE a.UUID = 'gocd-agent-01';`).
		Scan(&hostname, &pipelineName, &counter)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}

	if hostname != "worker-linux-01" || pipelineName != "deploy-pipeline" || counter != 10 {
		t.Errorf("unexpected query result: %s, %s, %d", hostname, pipelineName, counter)
	}
}

func TestGoCD_TDS_Connection(t *testing.T) {
	srv, port := startTestTDSServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=gocd;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open sqlserver failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db.PingContext (TDS) failed: %v", err)
	}

	// 1. Liquibase lock table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE DATABASECHANGELOGLOCK (
			ID INT PRIMARY KEY,
			LOCKED INT NOT NULL,
			LOCKEDBY VARCHAR(255)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE DATABASECHANGELOGLOCK (TDS) failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO DATABASECHANGELOGLOCK (ID, LOCKED, LOCKEDBY) VALUES (1, 0, NULL);`)
	if err != nil {
		t.Fatalf("INSERT DATABASECHANGELOGLOCK (TDS) failed: %v", err)
	}

	// 2. GoCD Tables
	_, err = db.ExecContext(ctx, `
		CREATE TABLE AGENTS (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			UUID VARCHAR(255) NOT NULL,
			HOSTNAME VARCHAR(255) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE AGENTS (TDS) failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE PIPELINES (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			NAME VARCHAR(255) NOT NULL,
			COUNTER INT DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE PIPELINES (TDS) failed: %v", err)
	}

	// 3. Insert and Query
	_, err = db.ExecContext(ctx, `INSERT INTO AGENTS (UUID, HOSTNAME) VALUES ('gocd-agent-tds', 'worker-win-01');`)
	if err != nil {
		t.Fatalf("INSERT AGENTS (TDS) failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO PIPELINES (NAME, COUNTER) VALUES ('tds-pipeline', 42);`)
	if err != nil {
		t.Fatalf("INSERT PIPELINES (TDS) failed: %v", err)
	}

	var hostname, pipelineName string
	var counter int
	err = db.QueryRowContext(ctx, `SELECT a.HOSTNAME, p.NAME, p.COUNTER FROM AGENTS a, PIPELINES p WHERE a.UUID = 'gocd-agent-tds';`).
		Scan(&hostname, &pipelineName, &counter)
	if err != nil {
		t.Fatalf("QueryRowContext (TDS) failed: %v", err)
	}

	if hostname != "worker-win-01" || pipelineName != "tds-pipeline" || counter != 42 {
		t.Errorf("unexpected query result: %s, %s, %d", hostname, pipelineName, counter)
	}
}

// -----------------------------------------------------------------------------
// 2. Wiki.js Compatibility Tests across PGWire (:5433) and TDS (:1433)
// -----------------------------------------------------------------------------

func TestWikiJS_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/wikijs?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open postgres failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db.PingContext (PGWire) failed: %v", err)
	}

	// 1. Create wiki tables
	_, err = db.ExecContext(ctx, `
		CREATE TABLE wiki_pages (
			id INT IDENTITY(1,1) PRIMARY KEY,
			path VARCHAR(500) NOT NULL,
			title VARCHAR(255) NOT NULL,
			content VARCHAR(2000) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE wiki_pages failed: %v", err)
	}

	// 2. Insert page and query
	_, err = db.ExecContext(ctx, `INSERT INTO wiki_pages (path, title, content) VALUES ('docs/pgwire', 'PGWire Support', '# PGWire Works');`)
	if err != nil {
		t.Fatalf("INSERT wiki_pages failed: %v", err)
	}

	var title, content string
	err = db.QueryRowContext(ctx, `SELECT title, content FROM wiki_pages WHERE path = 'docs/pgwire';`).
		Scan(&title, &content)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}

	if title != "PGWire Support" || content != "# PGWire Works" {
		t.Errorf("unexpected wiki page: %s, %s", title, content)
	}
}

func TestWikiJS_TDS_Connection(t *testing.T) {
	srv, port := startTestTDSServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=wikijs;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open sqlserver failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db.PingContext (TDS) failed: %v", err)
	}

	// 1. Create wiki tables with Knex mssql bracket syntax
	_, err = db.ExecContext(ctx, `
		CREATE TABLE [wiki_pages] (
			[id] INT IDENTITY(1,1) PRIMARY KEY,
			[path] NVARCHAR(500) NOT NULL,
			[title] NVARCHAR(255) NOT NULL,
			[content] NVARCHAR(2000) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE [wiki_pages] (TDS) failed: %v", err)
	}

	// 2. Insert with OUTPUT INSERTED.id (Knex MSSQL standard pattern)
	var newId int
	err = db.QueryRowContext(ctx, `
		INSERT INTO [wiki_pages] ([path], [title], [content])
		OUTPUT INSERTED.[id]
		VALUES ('docs/tds', 'TDS Protocol Support', '# TDS Wire Works');
	`).Scan(&newId)
	if err != nil {
		t.Fatalf("INSERT with OUTPUT INSERTED (TDS) failed: %v", err)
	}
	if newId != 1 {
		t.Errorf("expected newId=1, got %d", newId)
	}

	// 3. Query
	var title, content string
	err = db.QueryRowContext(ctx, `SELECT [title], [content] FROM [wiki_pages] WHERE [path] = 'docs/tds';`).
		Scan(&title, &content)
	if err != nil {
		t.Fatalf("QueryRowContext (TDS) failed: %v", err)
	}

	if title != "TDS Protocol Support" || content != "# TDS Wire Works" {
		t.Errorf("unexpected wiki page: %s, %s", title, content)
	}
}
