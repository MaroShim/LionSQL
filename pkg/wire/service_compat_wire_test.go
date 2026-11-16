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

// -----------------------------------------------------------------------------
// 3. Grafana Compatibility Tests across PGWire (:5433) and TDS (:1433)
// -----------------------------------------------------------------------------

func TestGrafana_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/grafana?sslmode=disable&connect_timeout=3", port)
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

	// 1. Advisory lock test (used by Grafana migrator)
	var locked bool
	if err := db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(12345);").Scan(&locked); err != nil {
		t.Fatalf("pg_try_advisory_lock query failed: %v", err)
	}

	// 2. Grafana metadata tables
	_, err = db.ExecContext(ctx, `
		CREATE TABLE org (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			name VARCHAR(190) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE org failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE "user" (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			login VARCHAR(190) NOT NULL,
			email VARCHAR(190) NOT NULL,
			name VARCHAR(255)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE \"user\" failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO org (name) VALUES ('Main Org.');`)
	if err != nil {
		t.Fatalf("INSERT org failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO "user" (login, email, name) VALUES ('admin', 'admin@grafana.local', 'Admin User');`)
	if err != nil {
		t.Fatalf("INSERT \"user\" failed: %v", err)
	}

	var orgName, userLogin string
	err = db.QueryRowContext(ctx, `SELECT o.name, u.login FROM org o, "user" u WHERE u.login = 'admin';`).
		Scan(&orgName, &userLogin)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if orgName != "Main Org." || userLogin != "admin" {
		t.Errorf("unexpected Grafana metadata result: %s, %s", orgName, userLogin)
	}
}

func TestGrafana_TDS_Connection(t *testing.T) {
	srv, port := startTestTDSServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=grafana;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
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

	// Grafana MSSQL Data Source time-series metrics table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE [server_metrics] (
			[id] INT IDENTITY(1,1) PRIMARY KEY,
			[metric_name] NVARCHAR(100) NOT NULL,
			[value] FLOAT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE [server_metrics] failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO [server_metrics] ([metric_name], [value]) VALUES ('cpu_load', 68.5);`)
	if err != nil {
		t.Fatalf("INSERT server_metrics failed: %v", err)
	}

	var metric string
	var val float64
	err = db.QueryRowContext(ctx, `SELECT [metric_name], [value] FROM [server_metrics] WHERE [metric_name] = 'cpu_load';`).
		Scan(&metric, &val)
	if err != nil {
		t.Fatalf("QueryRowContext (TDS) failed: %v", err)
	}
	if metric != "cpu_load" || val != 68.5 {
		t.Errorf("unexpected Grafana TDS metric result: %s, %f", metric, val)
	}
}

// -----------------------------------------------------------------------------
// 4. Gitea Compatibility Tests across PGWire (:5433) and TDS (:1433)
// -----------------------------------------------------------------------------

func TestGitea_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/gitea?sslmode=disable&connect_timeout=3", port)
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

	// Gitea PostgreSQL XORM schema
	_, err = db.ExecContext(ctx, `
		CREATE TABLE "user" (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			lower_name VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE \"user\" failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE repository (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			owner_id BIGINT NOT NULL,
			name VARCHAR(255) NOT NULL,
			is_private INT DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE repository failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO "user" (lower_name, name, email) VALUES ('octocat', 'Octocat', 'octocat@gitea.io');`)
	if err != nil {
		t.Fatalf("INSERT \"user\" failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO repository (owner_id, name, is_private) VALUES (1, 'hello-world', 0);`)
	if err != nil {
		t.Fatalf("INSERT repository failed: %v", err)
	}

	var uName, rName string
	var isPriv int
	err = db.QueryRowContext(ctx, `SELECT u.name, r.name, r.is_private FROM "user" u JOIN repository r ON u.id = r.owner_id WHERE u.lower_name = 'octocat';`).
		Scan(&uName, &rName, &isPriv)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if uName != "Octocat" || rName != "hello-world" || isPriv != 0 {
		t.Errorf("unexpected Gitea PGWire result: %s, %s, %d", uName, rName, isPriv)
	}
}

func TestGitea_TDS_Connection(t *testing.T) {
	srv, port := startTestTDSServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=gitea;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
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

	// Gitea MSSQL XORM bracket schema
	_, err = db.ExecContext(ctx, `
		CREATE TABLE [user] (
			[id] BIGINT IDENTITY(1,1) PRIMARY KEY,
			[lower_name] NVARCHAR(255) NOT NULL,
			[name] NVARCHAR(255) NOT NULL,
			[email] NVARCHAR(255) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE [user] (TDS) failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE [repository] (
			[id] BIGINT IDENTITY(1,1) PRIMARY KEY,
			[owner_id] BIGINT NOT NULL,
			[name] NVARCHAR(255) NOT NULL,
			[is_private] INT DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE [repository] (TDS) failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO [user] ([lower_name], [name], [email]) VALUES ('gitea-admin', 'Administrator', 'admin@gitea.local');`)
	if err != nil {
		t.Fatalf("INSERT [user] (TDS) failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO [repository] ([owner_id], [name], [is_private]) VALUES (1, 'core-repo', 1);`)
	if err != nil {
		t.Fatalf("INSERT [repository] (TDS) failed: %v", err)
	}

	var uName, rName string
	err = db.QueryRowContext(ctx, `SELECT u.[name], r.[name] FROM [user] u INNER JOIN [repository] r ON u.[id] = r.[owner_id] WHERE u.[lower_name] = 'gitea-admin';`).
		Scan(&uName, &rName)
	if err != nil {
		t.Fatalf("QueryRowContext (TDS) failed: %v", err)
	}
	if uName != "Administrator" || rName != "core-repo" {
		t.Errorf("unexpected Gitea TDS result: %s, %s", uName, rName)
	}
}

// -----------------------------------------------------------------------------
// 5. Metabase Compatibility Tests across PGWire (:5433) and TDS (:1433)
// -----------------------------------------------------------------------------

func TestMetabase_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/metabase?sslmode=disable&connect_timeout=3", port)
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

	// Metabase application metadata schema
	_, err = db.ExecContext(ctx, `
		CREATE TABLE metabase_database (
			id INT IDENTITY(1,1) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			engine VARCHAR(255) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE metabase_database failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE metabase_table (
			id INT IDENTITY(1,1) PRIMARY KEY,
			db_id INT NOT NULL,
			name VARCHAR(255) NOT NULL,
			active INT DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE metabase_table failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO metabase_database (name, engine) VALUES ('Analytics DB', 'postgres');`)
	if err != nil {
		t.Fatalf("INSERT metabase_database failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO metabase_table (db_id, name, active) VALUES (1, 'orders', 1);`)
	if err != nil {
		t.Fatalf("INSERT metabase_table failed: %v", err)
	}

	var dbName, tblName string
	err = db.QueryRowContext(ctx, `SELECT d.name, t.name FROM metabase_database d JOIN metabase_table t ON d.id = t.db_id;`).
		Scan(&dbName, &tblName)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if dbName != "Analytics DB" || tblName != "orders" {
		t.Errorf("unexpected Metabase PGWire result: %s, %s", dbName, tblName)
	}
}

func TestMetabase_TDS_Connection(t *testing.T) {
	srv, port := startTestTDSServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=metabase;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
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

	// Metabase MSSQL analytical queries
	_, err = db.ExecContext(ctx, `
		CREATE TABLE [sales_data] (
			[order_id] INT PRIMARY KEY,
			[product] NVARCHAR(100) NOT NULL,
			[amount] FLOAT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE [sales_data] failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO [sales_data] ([order_id], [product], [amount]) VALUES (1001, 'MacBook Pro', 2499.0);`)
	if err != nil {
		t.Fatalf("INSERT [sales_data] failed: %v", err)
	}

	var product string
	var total float64
	err = db.QueryRowContext(ctx, `SELECT [product], [amount] FROM [sales_data] WHERE [order_id] = 1001;`).
		Scan(&product, &total)
	if err != nil {
		t.Fatalf("QueryRowContext (TDS) failed: %v", err)
	}
	if product != "MacBook Pro" || total != 2499.0 {
		t.Errorf("unexpected Metabase TDS result: %s, %f", product, total)
	}
}

// -----------------------------------------------------------------------------
// 6. Casdoor Compatibility Tests across PGWire (:5433) and TDS (:1433)
// -----------------------------------------------------------------------------

func TestCasdoor_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/casdoor?sslmode=disable&connect_timeout=3", port)
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

	// Casdoor PostgreSQL organization and user schema
	_, err = db.ExecContext(ctx, `
		CREATE TABLE organization (
			owner VARCHAR(100) NOT NULL,
			name VARCHAR(100) NOT NULL,
			display_name VARCHAR(100),
			PRIMARY KEY (owner, name)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE organization failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE "user" (
			owner VARCHAR(100) NOT NULL,
			name VARCHAR(100) NOT NULL,
			email VARCHAR(255),
			PRIMARY KEY (owner, name)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE \"user\" failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO organization (owner, name, display_name) VALUES ('admin', 'built-in', 'Built-in Org');`)
	if err != nil {
		t.Fatalf("INSERT organization failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO "user" (owner, name, email) VALUES ('built-in', 'admin', 'admin@casdoor.com');`)
	if err != nil {
		t.Fatalf("INSERT \"user\" failed: %v", err)
	}

	var disp, email string
	err = db.QueryRowContext(ctx, `SELECT o.display_name, u.email FROM organization o JOIN "user" u ON o.name = u.owner WHERE o.owner = 'admin';`).
		Scan(&disp, &email)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if disp != "Built-in Org" || email != "admin@casdoor.com" {
		t.Errorf("unexpected Casdoor PGWire result: %s, %s", disp, email)
	}
}

func TestCasdoor_TDS_Connection(t *testing.T) {
	srv, port := startTestTDSServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=casdoor;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
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

	// Casdoor MSSQL organization and user schema with bracket syntax
	_, err = db.ExecContext(ctx, `
		CREATE TABLE [organization] (
			[owner] NVARCHAR(100) NOT NULL,
			[name] NVARCHAR(100) NOT NULL,
			[display_name] NVARCHAR(100),
			PRIMARY KEY ([owner], [name])
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE [organization] failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE [user] (
			[owner] NVARCHAR(100) NOT NULL,
			[name] NVARCHAR(100) NOT NULL,
			[password] NVARCHAR(255),
			PRIMARY KEY ([owner], [name])
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE [user] failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO [organization] ([owner], [name], [display_name]) VALUES ('admin', 'company-org', 'Company Org');`)
	if err != nil {
		t.Fatalf("INSERT [organization] failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO [user] ([owner], [name], [password]) VALUES ('company-org', 'alice', 'hashpass');`)
	if err != nil {
		t.Fatalf("INSERT [user] failed: %v", err)
	}

	var disp, uName string
	err = db.QueryRowContext(ctx, `SELECT o.[display_name], u.[name] FROM [organization] o INNER JOIN [user] u ON o.[name] = u.[owner];`).
		Scan(&disp, &uName)
	if err != nil {
		t.Fatalf("QueryRowContext (TDS) failed: %v", err)
	}
	if disp != "Company Org" || uName != "alice" {
		t.Errorf("unexpected Casdoor TDS result: %s, %s", disp, uName)
	}
}

// -----------------------------------------------------------------------------
// 7. Vaultwarden, Umami, and Apache Answer Compatibility Tests (PGWire :5433)
// -----------------------------------------------------------------------------

func TestVaultwarden_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/vaultwarden?sslmode=disable&connect_timeout=3", port)
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

	// Vaultwarden Diesel ORM tables
	_, err = db.ExecContext(ctx, `
		CREATE TABLE users (
			uuid VARCHAR(40) PRIMARY KEY,
			email VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			password_hash VARCHAR(255) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE users failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE ciphers (
			uuid VARCHAR(40) PRIMARY KEY,
			user_uuid VARCHAR(40) NOT NULL,
			type INT NOT NULL,
			name VARCHAR(255) NOT NULL,
			notes VARCHAR(2000)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE ciphers failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO users (uuid, email, name, password_hash) VALUES ('u-1', 'user@vaultwarden.local', 'User One', 'hash123');`)
	if err != nil {
		t.Fatalf("INSERT users failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO ciphers (uuid, user_uuid, type, name, notes) VALUES ('c-1', 'u-1', 1, 'GitHub Login', 'secure note');`)
	if err != nil {
		t.Fatalf("INSERT ciphers failed: %v", err)
	}

	var email, cipherName string
	var cipherType int
	err = db.QueryRowContext(ctx, `SELECT u.email, c.name, c.type FROM users u JOIN ciphers c ON u.uuid = c.user_uuid WHERE u.email = 'user@vaultwarden.local';`).
		Scan(&email, &cipherName, &cipherType)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if email != "user@vaultwarden.local" || cipherName != "GitHub Login" || cipherType != 1 {
		t.Errorf("unexpected Vaultwarden PGWire result: %s, %s, %d", email, cipherName, cipherType)
	}
}

func TestUmami_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/umami?sslmode=disable&connect_timeout=3", port)
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

	// Umami Prisma ORM tables
	_, err = db.ExecContext(ctx, `
		CREATE TABLE website (
			website_id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			domain VARCHAR(500) NOT NULL,
			share_id VARCHAR(50)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE website failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE session (
			session_id VARCHAR(36) PRIMARY KEY,
			website_id VARCHAR(36) NOT NULL,
			hostname VARCHAR(100),
			browser VARCHAR(50)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE session failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO website (website_id, name, domain) VALUES ('w-1', 'My Blog', 'blog.example.com');`)
	if err != nil {
		t.Fatalf("INSERT website failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO session (session_id, website_id, hostname, browser) VALUES ('s-1', 'w-1', 'blog.example.com', 'Chrome');`)
	if err != nil {
		t.Fatalf("INSERT session failed: %v", err)
	}

	var siteName, browser string
	err = db.QueryRowContext(ctx, `SELECT w.name, s.browser FROM website w JOIN session s ON w.website_id = s.website_id WHERE w.domain = 'blog.example.com';`).
		Scan(&siteName, &browser)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if siteName != "My Blog" || browser != "Chrome" {
		t.Errorf("unexpected Umami PGWire result: %s, %s", siteName, browser)
	}
}

func TestApacheAnswer_PGWire_Connection(t *testing.T) {
	srv, port := startTestPGServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/answer?sslmode=disable&connect_timeout=3", port)
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

	// Apache Answer XORM tables
	_, err = db.ExecContext(ctx, `
		CREATE TABLE "user" (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			username VARCHAR(50) NOT NULL,
			email VARCHAR(255) NOT NULL,
			status INT DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE \"user\" failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE question (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			user_id BIGINT NOT NULL,
			title VARCHAR(150) NOT NULL,
			content VARCHAR(4000) NOT NULL,
			view_count INT DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE question failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO "user" (username, email, status) VALUES ('dev_guru', 'guru@example.com', 1);`)
	if err != nil {
		t.Fatalf("INSERT \"user\" failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO question (user_id, title, content, view_count) VALUES (1, 'How to use LionSQL?', 'LionSQL is an embedded OLTP engine.', 10);`)
	if err != nil {
		t.Fatalf("INSERT question failed: %v", err)
	}

	var uName, title string
	var viewCount int
	err = db.QueryRowContext(ctx, `SELECT u.username, q.title, q.view_count FROM "user" u JOIN question q ON u.id = q.user_id WHERE u.username = 'dev_guru';`).
		Scan(&uName, &title, &viewCount)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if uName != "dev_guru" || title != "How to use LionSQL?" || viewCount != 10 {
		t.Errorf("unexpected Apache Answer PGWire result: %s, %s, %d", uName, title, viewCount)
	}
}

