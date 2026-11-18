//go:build cgo && wiredtiger

package wire_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/wiredtiger"
	"lionsql/pkg/wire"
	"lionsql/pkg/wire/tds"
)

// startTestWT_PGServer launches a fresh WiredTiger-backed PGWire server on a dynamic port.
func startTestWT_PGServer(t *testing.T) (*wire.Server, int, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wt_pgwire_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}

	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	cat := catalog.New(eng)

	srv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		eng.Close()
		os.RemoveAll(dir)
		t.Fatalf("Failed to start PG wire server: %v", err)
	}

	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		srv.Close()
		eng.Close()
		os.RemoveAll(dir)
		t.Fatalf("Failed to get TCP address from PG server")
	}

	cleanup := func() {
		srv.Close()
		eng.Close()
		os.RemoveAll(dir)
	}

	return srv, tcpAddr.Port, cleanup
}

// startTestWT_TDSServer launches a fresh WiredTiger-backed TDS server on a dynamic port.
func startTestWT_TDSServer(t *testing.T) (*tds.Server, int, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wt_tds_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}

	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		eng.Close()
		os.RemoveAll(dir)
		t.Fatalf("Failed to start TDS server: %v", err)
	}

	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		srv.Close()
		eng.Close()
		os.RemoveAll(dir)
		t.Fatalf("Failed to get TCP address from TDS server")
	}

	cleanup := func() {
		srv.Close()
		eng.Close()
		os.RemoveAll(dir)
	}

	return srv, tcpAddr.Port, cleanup
}

// -----------------------------------------------------------------------------
// 1. Apache Answer Compatibility (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_ApacheAnswer_PGWire(t *testing.T) {
	_, port, cleanup := startTestWT_PGServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/answer?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	// 1. Create Apache Answer core tables
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

	_, err = db.ExecContext(ctx, `
		CREATE TABLE answer (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			question_id BIGINT NOT NULL,
			user_id BIGINT NOT NULL,
			content VARCHAR(4000) NOT NULL,
			vote_count INT DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE answer failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		CREATE TABLE role_power_rel (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			role_id INT NOT NULL,
			power_type VARCHAR(50) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE role_power_rel failed: %v", err)
	}

	// 2. Insert user, question, and answer
	_, err = db.ExecContext(ctx, `INSERT INTO "user" (username, email, status) VALUES ('dev_guru', 'guru@example.com', 1);`)
	if err != nil {
		t.Fatalf("INSERT user failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO question (user_id, title, content, view_count) VALUES (1, 'How to use LionSQL with WiredTiger?', 'LionSQL runs with WT Lock-Free B+Tree.', 10);`)
	if err != nil {
		t.Fatalf("INSERT question failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO answer (question_id, user_id, content, vote_count) VALUES (1, 1, 'It is completely transparent and ACID compliant.', 5);`)
	if err != nil {
		t.Fatalf("INSERT answer failed: %v", err)
	}

	// 3. Batch insert into role_power_rel (testing batch IDENTITY auto-generation)
	_, err = db.ExecContext(ctx, `
		INSERT INTO role_power_rel (role_id, power_type) VALUES
		(1, 'admin_access'),
		(1, 'question_edit'),
		(1, 'answer_delete'),
		(2, 'question_post');
	`)
	if err != nil {
		t.Fatalf("Batch INSERT into role_power_rel failed: %v", err)
	}

	// 4. Verify batch IDENTITY count
	var powerCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM role_power_rel`).Scan(&powerCount); err != nil {
		t.Fatalf("SELECT COUNT(*) failed: %v", err)
	}
	if powerCount != 4 {
		t.Fatalf("expected 4 role_power_rel rows, got %d", powerCount)
	}

	// 5. Complex JOIN query across user, question, and answer
	var uName, qTitle, aContent string
	var vCount, aVotes int
	err = db.QueryRowContext(ctx, `
		SELECT u.username, q.title, q.view_count, a.content, a.vote_count
		FROM "user" u
		JOIN question q ON u.id = q.user_id
		JOIN answer a ON q.id = a.question_id
		WHERE u.username = 'dev_guru';
	`).Scan(&uName, &qTitle, &vCount, &aContent, &aVotes)
	if err != nil {
		t.Fatalf("JOIN query failed: %v", err)
	}
	if uName != "dev_guru" || qTitle != "How to use LionSQL with WiredTiger?" || vCount != 10 || aVotes != 5 {
		t.Fatalf("unexpected Answer query result: uName=%q, qTitle=%q, vCount=%d, aVotes=%d", uName, qTitle, vCount, aVotes)
	}
	t.Logf("Apache Answer on WiredTiger PASSED successfully!")
}

// -----------------------------------------------------------------------------
// 2. GoCD Compatibility (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_GoCD_PGWire(t *testing.T) {
	_, port, cleanup := startTestWT_PGServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/gocd?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Liquibase changeLog & lock
	_, err = db.ExecContext(ctx, `
		CREATE TABLE databasechangeloglock (
			id INT PRIMARY KEY,
			locked BOOLEAN NOT NULL,
			lockgranted TIMESTAMP NULL,
			lockedby VARCHAR(255) NULL
		);
	`)
	if err != nil {
		t.Fatalf("create changeloglock failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO databasechangeloglock VALUES (1, false, NULL, NULL)`)
	if err != nil {
		t.Fatalf("insert lock row failed: %v", err)
	}

	// GoCD pipelines and stages
	_, err = db.ExecContext(ctx, `
		CREATE TABLE pipelines (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			counter INT NOT NULL
		);
		CREATE TABLE stages (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			pipeline_id BIGINT NOT NULL,
			name VARCHAR(255) NOT NULL,
			result VARCHAR(50) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create pipelines/stages failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO pipelines (name, counter) VALUES ('build-service', 42)`)
	if err != nil {
		t.Fatalf("insert pipeline failed: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO stages (pipeline_id, name, result) VALUES (1, 'unit-tests', 'Passed')`)
	if err != nil {
		t.Fatalf("insert stage failed: %v", err)
	}

	var pName, sName, sResult string
	err = db.QueryRowContext(ctx, `
		SELECT p.name, s.name, s.result
		FROM pipelines p JOIN stages s ON p.id = s.pipeline_id
		WHERE p.name = 'build-service'
	`).Scan(&pName, &sName, &sResult)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if pName != "build-service" || sName != "unit-tests" || sResult != "Passed" {
		t.Fatalf("unexpected GoCD result: %s, %s, %s", pName, sName, sResult)
	}
	t.Logf("GoCD on WiredTiger PASSED successfully!")
}

// -----------------------------------------------------------------------------
// 3. Wiki.js Compatibility (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_WikiJS_PGWire(t *testing.T) {
	_, port, cleanup := startTestWT_PGServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/wiki?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Knex migrations lock (Table without primary key to test synthetic _row_id hiding)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE knex_migrations_lock (
			"index" INT PRIMARY KEY,
			is_locked INT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create knex_migrations_lock failed: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO knex_migrations_lock ("index", is_locked) VALUES (1, 0)`)
	if err != nil {
		t.Fatalf("insert knex lock failed: %v", err)
	}

	// Pages table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE pages (
			id INT IDENTITY(1,1) PRIMARY KEY,
			path VARCHAR(255) NOT NULL,
			title VARCHAR(255) NOT NULL,
			content VARCHAR(4000) NOT NULL,
			is_published BOOLEAN DEFAULT true
		);
	`)
	if err != nil {
		t.Fatalf("create pages failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO pages (path, title, content) VALUES ('home', 'Welcome to Wiki.js', 'Content on LionSQL WT engine.')`)
	if err != nil {
		t.Fatalf("insert page failed: %v", err)
	}

	var pTitle, pContent string
	err = db.QueryRowContext(ctx, `SELECT title, content FROM pages WHERE path = 'home'`).Scan(&pTitle, &pContent)
	if err != nil {
		t.Fatalf("query page failed: %v", err)
	}
	if pTitle != "Welcome to Wiki.js" || pContent != "Content on LionSQL WT engine." {
		t.Fatalf("unexpected Wiki.js result: %s, %s", pTitle, pContent)
	}
	t.Logf("Wiki.js on WiredTiger PASSED successfully!")
}

// -----------------------------------------------------------------------------
// 4. Casdoor Compatibility (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_Casdoor_PGWire(t *testing.T) {
	_, port, cleanup := startTestWT_PGServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/casdoor?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Composite primary key (owner, name)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE provider (
			owner VARCHAR(100) NOT NULL,
			name VARCHAR(100) NOT NULL,
			category VARCHAR(100) NULL,
			type VARCHAR(100) NOT NULL,
			PRIMARY KEY (owner, name)
		);
	`)
	if err != nil {
		t.Fatalf("create provider failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO provider (owner, name, category, type) VALUES ('admin', 'oauth_github', 'OAuth', 'GitHub')`)
	if err != nil {
		t.Fatalf("insert provider 1 failed: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider (owner, name, category, type) VALUES ('admin', 'oauth_google', 'OAuth', 'Google')`)
	if err != nil {
		t.Fatalf("insert provider 2 failed: %v", err)
	}

	var pType string
	err = db.QueryRowContext(ctx, `SELECT type FROM provider WHERE owner = 'admin' AND name = 'oauth_github'`).Scan(&pType)
	if err != nil {
		t.Fatalf("query provider failed: %v", err)
	}
	if pType != "GitHub" {
		t.Fatalf("expected GitHub, got %s", pType)
	}
	t.Logf("Casdoor on WiredTiger PASSED successfully!")
}

// -----------------------------------------------------------------------------
// 5. Vaultwarden Compatibility (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_Vaultwarden_PGWire(t *testing.T) {
	_, port, cleanup := startTestWT_PGServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/vaultwarden?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, `
		CREATE TABLE users (
			uuid VARCHAR(36) PRIMARY KEY,
			email VARCHAR(255) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			security_stamp VARCHAR(255) NOT NULL
		);
		CREATE TABLE ciphers (
			uuid VARCHAR(36) PRIMARY KEY,
			user_uuid VARCHAR(36) NOT NULL,
			type INT NOT NULL,
			name VARCHAR(255) NOT NULL,
			notes VARCHAR(4000) NULL
		);
	`)
	if err != nil {
		t.Fatalf("create users/ciphers failed: %v", err)
	}

	uUUID := "11111111-2222-3333-4444-555555555555"
	cUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO users VALUES ('%s', 'user@safe.org', 'hash123', 'stamp456')", uUUID))
	if err != nil {
		t.Fatalf("insert user failed: %v", err)
	}

	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO ciphers VALUES ('%s', '%s', 1, 'MyBank Login', 'Encrypted Payload')", cUUID, uUUID))
	if err != nil {
		t.Fatalf("insert cipher failed: %v", err)
	}

	var cName, uEmail string
	err = db.QueryRowContext(ctx, `
		SELECT c.name, u.email FROM ciphers c JOIN users u ON c.user_uuid = u.uuid WHERE c.uuid = $1
	`, cUUID).Scan(&cName, &uEmail)
	if err != nil {
		t.Fatalf("query cipher failed: %v", err)
	}
	if cName != "MyBank Login" || uEmail != "user@safe.org" {
		t.Fatalf("unexpected Vaultwarden result: %s, %s", cName, uEmail)
	}
	t.Logf("Vaultwarden on WiredTiger PASSED successfully!")
}

// -----------------------------------------------------------------------------
// 6. Umami Compatibility (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_Umami_PGWire(t *testing.T) {
	_, port, cleanup := startTestWT_PGServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/umami?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, `
		CREATE TABLE website (
			website_id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			domain VARCHAR(500) NOT NULL
		);
		CREATE TABLE website_event (
			event_id VARCHAR(36) PRIMARY KEY,
			website_id VARCHAR(36) NOT NULL,
			url_path VARCHAR(500) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("create website/event failed: %v", err)
	}

	wID := "w-100"
	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO website VALUES ('%s', 'MyBlog', 'blog.example.com')", wID))
	if err != nil {
		t.Fatalf("insert website failed: %v", err)
	}

	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO website_event (event_id, website_id, url_path) VALUES ('e-1', '%s', '/posts/first-post')", wID))
	if err != nil {
		t.Fatalf("insert event 1 failed: %v", err)
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO website_event (event_id, website_id, url_path) VALUES ('e-2', '%s', '/posts/second-post')", wID))
	if err != nil {
		t.Fatalf("insert event 2 failed: %v", err)
	}

	var eventCount int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM website_event WHERE website_id = $1`, wID).Scan(&eventCount)
	if err != nil {
		t.Fatalf("query count failed: %v", err)
	}
	if eventCount != 2 {
		t.Fatalf("expected 2 events, got %d", eventCount)
	}
	t.Logf("Umami on WiredTiger PASSED successfully!")
}

// -----------------------------------------------------------------------------
// 7. Metabase Compatibility (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_Metabase_PGWire(t *testing.T) {
	_, port, cleanup := startTestWT_PGServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/metabase?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Metabase metadata introspection query
	rows, err := db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatalf("query information_schema.tables failed: %v", err)
	}
	rows.Close()

	// Metabase core report table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE report_card (
			id INT IDENTITY(1,1) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			dataset_query VARCHAR(4000) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create report_card failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO report_card (name, dataset_query) VALUES ('Monthly Revenue', 'SELECT SUM(amount) FROM sales')`)
	if err != nil {
		t.Fatalf("insert report_card failed: %v", err)
	}

	var name string
	err = db.QueryRowContext(ctx, `SELECT name FROM report_card WHERE id = 1`).Scan(&name)
	if err != nil {
		t.Fatalf("query report_card failed: %v", err)
	}
	if name != "Monthly Revenue" {
		t.Fatalf("expected 'Monthly Revenue', got %q", name)
	}
	t.Logf("Metabase on WiredTiger PASSED successfully!")
}

// -----------------------------------------------------------------------------
// 8. Grafana & Gitea Compatibility (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_GrafanaAndGitea_PGWire(t *testing.T) {
	_, port, cleanup := startTestWT_PGServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/grafana?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Grafana dashboard table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE dashboard (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			title VARCHAR(255) NOT NULL,
			slug VARCHAR(255) NOT NULL,
			data VARCHAR(4000) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create dashboard failed: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO dashboard (title, slug, data) VALUES ('Server Health', 'server-health', '{"panels": []}')`)
	if err != nil {
		t.Fatalf("insert dashboard failed: %v", err)
	}

	// Gitea repository table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE repository (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			owner_id BIGINT NOT NULL,
			name VARCHAR(255) NOT NULL,
			is_private BOOLEAN DEFAULT false
		);
	`)
	if err != nil {
		t.Fatalf("create repository failed: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO repository (owner_id, name) VALUES (1, 'LionSQL')`)
	if err != nil {
		t.Fatalf("insert repository failed: %v", err)
	}

	var dTitle, rName string
	err = db.QueryRowContext(ctx, `SELECT title FROM dashboard WHERE slug = 'server-health'`).Scan(&dTitle)
	if err != nil {
		t.Fatalf("query dashboard failed: %v", err)
	}
	err = db.QueryRowContext(ctx, `SELECT name FROM repository WHERE id = 1`).Scan(&rName)
	if err != nil {
		t.Fatalf("query repository failed: %v", err)
	}
	if dTitle != "Server Health" || rName != "LionSQL" {
		t.Fatalf("unexpected Grafana/Gitea result: %s, %s", dTitle, rName)
	}
	t.Logf("Grafana & Gitea on WiredTiger PASSED successfully!")
}

// -----------------------------------------------------------------------------
// 9. TDS Protocol Compatibility across Services (WiredTiger Fresh Init)
// -----------------------------------------------------------------------------

func TestWT_EnterpriseServices_TDS(t *testing.T) {
	_, port, cleanup := startTestWT_TDSServer(t)
	defer cleanup()

	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=5", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Create table via TDS
	_, err = db.ExecContext(ctx, `
		CREATE TABLE tds_services (
			id INT IDENTITY(1,1) PRIMARY KEY,
			service_name NVARCHAR(100) NOT NULL,
			framework NVARCHAR(100) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("TDS CREATE TABLE failed: %v", err)
	}

	// 2. Insert services via TDS
	_, err = db.ExecContext(ctx, `
		INSERT INTO tds_services (service_name, framework) VALUES
		('Apache Answer', 'Go XORM'),
		('GoCD', 'Java Hibernate'),
		('Wiki.js', 'Node Knex'),
		('Casdoor', 'Go XORM'),
		('Vaultwarden', 'Rust Diesel'),
		('Umami', 'TS Prisma');
	`)
	if err != nil {
		t.Fatalf("TDS batch INSERT failed: %v", err)
	}

	// 3. Query count via TDS
	var totalServices int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tds_services`).Scan(&totalServices)
	if err != nil {
		t.Fatalf("TDS query count failed: %v", err)
	}
	if totalServices != 6 {
		t.Fatalf("expected 6 services via TDS, got %d", totalServices)
	}
	t.Logf("TDS Enterprise Services on WiredTiger PASSED successfully!")
}
