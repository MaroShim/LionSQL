//go:build cgo && wiredtiger

package engine_test

import (
	"os"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/wiredtiger"
)

func startTestWTCoordinator(t *testing.T) (*engine.Coordinator, *wiredtiger.Engine, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wt_engine_compat_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}

	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	cleanup := func() {
		coord.Close()
		eng.Close()
		os.RemoveAll(dir)
	}

	return coord, eng, cleanup
}

func TestWT_Engine_WikiJS_Knex(t *testing.T) {
	coord, _, cleanup := startTestWTCoordinator(t)
	defer cleanup()

	// 1. Drizzle / Knex Migration tracking table
	_, err := coord.ExecuteSQL(`
		CREATE TABLE "knex_migrations" (
			"id" INT IDENTITY(1,1) PRIMARY KEY,
			"name" VARCHAR(255) NOT NULL,
			"batch" INT NOT NULL,
			"migration_time" DATETIME
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE knex_migrations failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`
		CREATE TABLE "knex_migrations_lock" (
			"index" INT PRIMARY KEY,
			"is_locked" INT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE knex_migrations_lock failed: %v", err)
	}

	// 2. Core Wiki.js Tables
	_, err = coord.ExecuteSQL(`
		CREATE TABLE "wiki_users" (
			"id" INT IDENTITY(1,1) PRIMARY KEY,
			"email" VARCHAR(255) NOT NULL,
			"name" VARCHAR(255) NOT NULL,
			"providerKey" VARCHAR(50) DEFAULT 'local',
			"isActive" INT DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE wiki_users failed: %v", err)
	}

	// 3. Insert and verify
	_, err = coord.ExecuteSQL(`INSERT INTO "knex_migrations_lock" ("index", "is_locked") VALUES (1, 0);`)
	if err != nil {
		t.Fatalf("INSERT knex_migrations_lock failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO "wiki_users" ("email", "name") VALUES ('admin@wiki.js', 'Administrator');`)
	if err != nil {
		t.Fatalf("INSERT wiki_users failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT name, email, providerKey FROM "wiki_users" WHERE "email" = 'admin@wiki.js';`)
	if err != nil {
		t.Fatalf("SELECT wiki_users failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Administrator" {
		t.Fatalf("unexpected Wiki.js query result: %+v", res.Rows)
	}
	t.Logf("WikiJS on WiredTiger Coordinator PASSED successfully!")
}

func TestWT_Engine_Casdoor_CompositePK(t *testing.T) {
	coord, _, cleanup := startTestWTCoordinator(t)
	defer cleanup()

	ddl := `CREATE TABLE [provider] (
		[owner] VARCHAR(100) NOT NULL,
		[name] VARCHAR(100) NOT NULL,
		[category] VARCHAR(100) NULL,
		[addresses] VARBINARY(50) NULL,
		PRIMARY KEY (owner, name)
	);`
	if _, err := coord.ExecuteSQL(ddl); err != nil {
		t.Fatalf("CREATE TABLE with composite PK failed: %v", err)
	}

	ins1 := `INSERT INTO [provider] ([owner], [name], [category], [addresses]) VALUES ('admin', 'provider_captcha_default', 'Captcha', 0x6e756c6c);`
	if _, err := coord.ExecuteSQL(ins1); err != nil {
		t.Fatalf("ins1 failed: %v", err)
	}

	ins2 := `INSERT INTO [provider] ([owner], [name], [category], [addresses]) VALUES ('admin', 'provider_oauth_github', 'OAuth', 0x61626364);`
	if _, err := coord.ExecuteSQL(ins2); err != nil {
		t.Fatalf("ins2 failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT [owner], [name], [category] FROM [provider] WHERE [owner] = 'admin' AND [name] = 'provider_oauth_github';`)
	if err != nil {
		t.Fatalf("SELECT provider failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["category"] != "OAuth" {
		t.Fatalf("unexpected Casdoor query result: %+v", res.Rows)
	}
	t.Logf("Casdoor on WiredTiger Coordinator PASSED successfully!")
}

func TestWT_Engine_GoCD_Liquibase(t *testing.T) {
	coord, _, cleanup := startTestWTCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`
		CREATE TABLE DATABASECHANGELOGLOCK (
			ID INT NOT NULL PRIMARY KEY,
			LOCKED BOOLEAN NOT NULL,
			LOCKGRANTED TIMESTAMP NULL,
			LOCKEDBY VARCHAR(255) NULL
		);
		CREATE TABLE PIPELINES (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			NAME VARCHAR(255) NOT NULL,
			COUNTER INT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE GoCD tables failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO DATABASECHANGELOGLOCK (ID, LOCKED) VALUES (1, false);`)
	if err != nil {
		t.Fatalf("INSERT lock failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO PIPELINES (NAME, COUNTER) VALUES ('my-pipeline', 1);`)
	if err != nil {
		t.Fatalf("INSERT pipeline failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT NAME, COUNTER FROM PIPELINES WHERE NAME = 'my-pipeline';`)
	if err != nil {
		t.Fatalf("SELECT pipeline failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["COUNTER"] != int64(1) {
		t.Fatalf("unexpected GoCD query result: %+v", res.Rows)
	}
	t.Logf("GoCD on WiredTiger Coordinator PASSED successfully!")
}

func TestWT_Engine_Vaultwarden_Diesel(t *testing.T) {
	coord, _, cleanup := startTestWTCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`
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
			name VARCHAR(255) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE Vaultwarden tables failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES ('u-1', 'admin@vault.org', 'pwd', 'stamp');`)
	if err != nil {
		t.Fatalf("INSERT user failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO ciphers VALUES ('c-1', 'u-1', 1, 'Server Login');`)
	if err != nil {
		t.Fatalf("INSERT cipher failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT c.name, u.email FROM ciphers c JOIN users u ON c.user_uuid = u.uuid WHERE c.uuid = 'c-1';`)
	if err != nil {
		t.Fatalf("SELECT cipher failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Server Login" || res.Rows[0]["email"] != "admin@vault.org" {
		t.Fatalf("unexpected Vaultwarden query result: %+v", res.Rows)
	}
	t.Logf("Vaultwarden on WiredTiger Coordinator PASSED successfully!")
}

func TestWT_Engine_Umami_Prisma(t *testing.T) {
	coord, _, cleanup := startTestWTCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`
		CREATE TABLE website (
			website_id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			domain VARCHAR(500) NOT NULL
		);
		CREATE TABLE website_event (
			event_id VARCHAR(36) PRIMARY KEY,
			website_id VARCHAR(36) NOT NULL,
			url_path VARCHAR(500) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE Umami tables failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO website VALUES ('w-1', 'Analytics', 'example.org');`)
	if err != nil {
		t.Fatalf("INSERT website failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO website_event VALUES ('e-1', 'w-1', '/docs'), ('e-2', 'w-1', '/about');`)
	if err != nil {
		t.Fatalf("INSERT events failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT COUNT(*) AS total FROM website_event WHERE website_id = 'w-1';`)
	if err != nil {
		t.Fatalf("SELECT count failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["total"] != int64(2) {
		t.Fatalf("unexpected Umami count result: %+v", res.Rows)
	}
	t.Logf("Umami on WiredTiger Coordinator PASSED successfully!")
}

func TestWT_Engine_WoodpeckerAndVikunja_XORM(t *testing.T) {
	coord, _, cleanup := startTestWTCoordinator(t)
	defer cleanup()

	// Woodpecker repos & Vikunja tasks
	_, err := coord.ExecuteSQL(`
		CREATE TABLE wp_repos (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			owner VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			is_active BOOLEAN DEFAULT true
		);
		CREATE TABLE tasks (
			id BIGINT IDENTITY(1,1) PRIMARY KEY,
			title VARCHAR(255) NOT NULL,
			done BOOLEAN DEFAULT false
		);
	`)
	if err != nil {
		t.Fatalf("CREATE WP/Vikunja tables failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO wp_repos (owner, name) VALUES ('lionsql', 'engine');`)
	if err != nil {
		t.Fatalf("INSERT repo failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO tasks (title, done) VALUES ('Verify WiredTiger Compatibility', true);`)
	if err != nil {
		t.Fatalf("INSERT task failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT title, done FROM tasks WHERE id = 1;`)
	if err != nil {
		t.Fatalf("SELECT task failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Verify WiredTiger Compatibility" {
		t.Fatalf("unexpected Vikunja task result: %+v", res.Rows)
	}
	t.Logf("Woodpecker & Vikunja on WiredTiger Coordinator PASSED successfully!")
}
