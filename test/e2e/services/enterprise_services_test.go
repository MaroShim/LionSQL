package services_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"lionsql/test/e2e/harness"
)

// TestE2E_EnterpriseServices runs comprehensive enterprise migration & CRUD scenarios
// across both In-Memory OCC (MemEngine) and WiredTiger (MVCC B+Tree) storage engines.
func TestE2E_EnterpriseServices(t *testing.T) {
	storageEngines := []harness.StorageType{
		harness.StorageMemEngine,
	}
	if harness.IsWiredTigerAvailable() {
		storageEngines = append(storageEngines, harness.StorageWiredTiger)
	}

	for _, sType := range storageEngines {
		t.Run(string(sType), func(t *testing.T) {
			server := harness.StartDualServer(t, sType)
			defer server.Cleanup()

			pgDB, tdsDB := server.OpenClients(t)
			defer pgDB.Close()
			defer tdsDB.Close()

			t.Run("1_Gitea_XORM_PGWire_And_TDS", func(t *testing.T) {
				testGiteaLifecycle(t, pgDB, tdsDB)
			})

			t.Run("2_Metabase_Toucan_PGWire_And_TDS", func(t *testing.T) {
				testMetabaseLifecycle(t, pgDB, tdsDB)
			})

			t.Run("3_WikiJS_Knex_PGWire", func(t *testing.T) {
				testWikiJSLifecycle(t, pgDB)
			})

			t.Run("4_Vaultwarden_SeaORM_PGWire", func(t *testing.T) {
				testVaultwardenLifecycle(t, pgDB)
			})

			t.Run("5_Casdoor_XORM_PGWire_And_TDS", func(t *testing.T) {
				testCasdoorLifecycle(t, pgDB, tdsDB)
			})

			t.Run("6_GoCD_Liquibase_PGWire_And_TDS", func(t *testing.T) {
				testGoCDLifecycle(t, pgDB, tdsDB)
			})

			t.Run("7_Umami_Prisma_PGWire", func(t *testing.T) {
				testUmamiLifecycle(t, pgDB)
			})

			t.Run("8_Woodpecker_XORM_PGWire", func(t *testing.T) {
				testWoodpeckerLifecycle(t, pgDB)
			})

			t.Run("9_Vikunja_XORM_PGWire", func(t *testing.T) {
				testVikunjaLifecycle(t, pgDB)
			})
		})
	}
}

// 1. Gitea XORM Lifecycle
func testGiteaLifecycle(t *testing.T, pgDB, tdsDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddls := []string{
		`CREATE TABLE gitea_user (
			id INT PRIMARY KEY,
			name VARCHAR(50) NOT NULL,
			lower_name VARCHAR(50) NOT NULL,
			email VARCHAR(100) NOT NULL,
			is_admin BOOLEAN,
			num_repos INT,
			created_unix INT
		);`,
		`CREATE TABLE gitea_repository (
			id INT PRIMARY KEY,
			owner_id INT NOT NULL,
			name VARCHAR(50) NOT NULL,
			lower_name VARCHAR(50) NOT NULL,
			description TEXT,
			is_private BOOLEAN,
			num_stars INT,
			created_unix INT
		);`,
		`CREATE TABLE gitea_issue (
			id INT PRIMARY KEY,
			repo_id INT NOT NULL,
			index_in_repo INT NOT NULL,
			poster_id INT NOT NULL,
			title VARCHAR(255) NOT NULL,
			content TEXT,
			is_closed BOOLEAN
		);`,
	}

	for _, ddl := range ddls {
		if _, err := pgDB.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("Gitea DDL failed: %v", err)
		}
	}

	// Dynamic ALTER TABLE ADD COLUMN verification
	if _, err := pgDB.ExecContext(ctx, `ALTER TABLE gitea_user ADD COLUMN avatar_url VARCHAR(255);`); err != nil {
		t.Fatalf("Gitea ALTER TABLE ADD COLUMN failed: %v", err)
	}

	// Insert via PGWire
	_, err := pgDB.ExecContext(ctx, `
		INSERT INTO gitea_user VALUES (1, 'maro', 'maro', 'maro@test.local', true, 5, 1700000000, 'http://avatar');
		INSERT INTO gitea_repository VALUES (10, 1, 'LionSQL', 'lionsql', 'Fast Autonomous DB', false, 128, 1700000000);
		INSERT INTO gitea_issue VALUES (100, 10, 1, 1, 'Issue #1 Title', 'Bug report details', false);
	`)
	if err != nil {
		t.Fatalf("Gitea INSERT via PGWire failed: %v", err)
	}

	// Query via TDS
	var userName, repoName, issueTitle string
	err = tdsDB.QueryRowContext(ctx, `
		SELECT u.name, r.name, i.title
		FROM gitea_user u
		JOIN gitea_repository r ON u.id = r.owner_id
		JOIN gitea_issue i ON r.id = i.repo_id
		WHERE u.id = 1;
	`).Scan(&userName, &repoName, &issueTitle)
	if err != nil {
		t.Fatalf("Gitea cross-protocol query via TDS failed: %v", err)
	}
	if userName != "maro" || repoName != "LionSQL" || issueTitle != "Issue #1 Title" {
		t.Fatalf("Gitea data mismatch: %s / %s / %s", userName, repoName, issueTitle)
	}
}

// 2. Metabase Toucan / JDBC Lifecycle
func testMetabaseLifecycle(t *testing.T, pgDB, tdsDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddls := []string{
		`CREATE TABLE metabase_database (
			id INT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			engine VARCHAR(50) NOT NULL,
			details TEXT,
			created_at DATETIME
		);`,
		`CREATE TABLE metabase_table (
			id INT PRIMARY KEY,
			db_id INT NOT NULL,
			schema_name VARCHAR(255),
			name VARCHAR(255) NOT NULL,
			active BOOLEAN
		);`,
		`CREATE TABLE metabase_field (
			id INT PRIMARY KEY,
			table_id INT NOT NULL,
			name VARCHAR(255) NOT NULL,
			base_type VARCHAR(255),
			position INT
		);`,
	}
	for _, ddl := range ddls {
		if _, err := tdsDB.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("Metabase DDL via TDS failed: %v", err)
		}
	}

	_, err := tdsDB.ExecContext(ctx, `
		INSERT INTO metabase_database VALUES (1, 'AnalyticsDB', 'postgres', '{"host":"localhost"}', '2026-01-01 12:00:00');
		INSERT INTO metabase_table VALUES (10, 1, 'public', 'sales', true);
		INSERT INTO metabase_field VALUES (100, 10, 'amount', 'type/Decimal', 1);
		INSERT INTO metabase_field VALUES (101, 10, 'created_at', 'type/DateTime', 2);
	`)
	if err != nil {
		t.Fatalf("Metabase INSERT via TDS failed: %v", err)
	}

	// Metabase metadata exploration query via PGWire
	rows, err := pgDB.QueryContext(ctx, `
		SELECT f.name, f.base_type, f.position
		FROM metabase_field f
		JOIN metabase_table t ON f.table_id = t.id
		JOIN metabase_database d ON t.db_id = d.id
		WHERE d.id = 1 AND t.name = 'sales'
		ORDER BY f.position ASC;
	`)
	if err != nil {
		t.Fatalf("Metabase query via PGWire failed: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var fName, bType string
		var pos int
		if err := rows.Scan(&fName, &bType, &pos); err != nil {
			t.Fatalf("Metabase Scan failed: %v", err)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("expected 2 fields, got %d", count)
	}
}

// 3. Wiki.js Knex Lifecycle
func testWikiJSLifecycle(t *testing.T, pgDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddls := []string{
		`CREATE TABLE wikijs_pages (
			id INT PRIMARY KEY,
			path VARCHAR(255) NOT NULL,
			title VARCHAR(255) NOT NULL,
			content TEXT,
			is_private BOOLEAN,
			created_at DATETIME
		);`,
		`CREATE TABLE wikijs_users (
			id INT PRIMARY KEY,
			email VARCHAR(100) NOT NULL,
			name VARCHAR(100) NOT NULL,
			provider VARCHAR(50)
		);`,
	}
	for _, ddl := range ddls {
		if _, err := pgDB.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("WikiJS DDL failed: %v", err)
		}
	}

	_, err := pgDB.ExecContext(ctx, `
		INSERT INTO wikijs_pages VALUES (1, 'home', 'Welcome', '# Hello Wiki.js', false, '2026-01-01 00:00:00');
		INSERT INTO wikijs_pages VALUES (2, 'architecture/lionsql', 'LionSQL Engine', 'Autonomous OLTP', false, '2026-01-02 00:00:00');
	`)
	if err != nil {
		t.Fatalf("WikiJS INSERT failed: %v", err)
	}

	var title string
	err = pgDB.QueryRowContext(ctx, `SELECT title FROM wikijs_pages WHERE path = 'architecture/lionsql';`).Scan(&title)
	if err != nil || title != "LionSQL Engine" {
		t.Fatalf("WikiJS query failed: %v, got %q", err, title)
	}
}

// 4. Vaultwarden SeaORM Lifecycle
func testVaultwardenLifecycle(t *testing.T, pgDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddls := []string{
		`CREATE TABLE vw_users (
			uuid VARCHAR(36) PRIMARY KEY,
			email VARCHAR(100) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			security_stamp VARCHAR(36) NOT NULL
		);`,
		`CREATE TABLE vw_ciphers (
			uuid VARCHAR(36) PRIMARY KEY,
			user_uuid VARCHAR(36) NOT NULL,
			type INT NOT NULL,
			name TEXT NOT NULL,
			notes TEXT,
			data TEXT NOT NULL
		);`,
	}
	for _, ddl := range ddls {
		if _, err := pgDB.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("Vaultwarden DDL failed: %v", err)
		}
	}

	_, err := pgDB.ExecContext(ctx, `
		INSERT INTO vw_users VALUES ('u-1', 'admin@bitwarden.local', 'argon2_hash...', 'stamp-1');
		INSERT INTO vw_ciphers VALUES ('c-1', 'u-1', 1, '2FA Secret', 'Emergency note', 'encrypted_payload_data');
	`)
	if err != nil {
		t.Fatalf("Vaultwarden INSERT failed: %v", err)
	}

	var cipherName, payload string
	err = pgDB.QueryRowContext(ctx, `
		SELECT c.name, c.data 
		FROM vw_ciphers c
		JOIN vw_users u ON c.user_uuid = u.uuid
		WHERE u.email = 'admin@bitwarden.local';
	`).Scan(&cipherName, &payload)
	if err != nil || cipherName != "2FA Secret" || payload != "encrypted_payload_data" {
		t.Fatalf("Vaultwarden cipher retrieval failed: %v (got %s, %s)", err, cipherName, payload)
	}
}

// 5. Casdoor XORM Lifecycle
func testCasdoorLifecycle(t *testing.T, pgDB, tdsDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddl := `
	CREATE TABLE casdoor_organization (
		id INT PRIMARY KEY,
		owner VARCHAR(100),
		name VARCHAR(100),
		display_name VARCHAR(100)
	);
	CREATE TABLE casdoor_user (
		id INT PRIMARY KEY,
		owner VARCHAR(100),
		name VARCHAR(100),
		password VARCHAR(100),
		org_id INT
	);
	`
	if _, err := pgDB.ExecContext(ctx, ddl); err != nil {
		t.Fatalf("Casdoor DDL failed: %v", err)
	}

	_, err := pgDB.ExecContext(ctx, `
		INSERT INTO casdoor_organization VALUES (1, 'built-in', 'app-org', 'Application Organization');
		INSERT INTO casdoor_user VALUES (10, 'app-org', 'alice', 'hashpass', 1);
	`)
	if err != nil {
		t.Fatalf("Casdoor INSERT failed: %v", err)
	}

	var dName string
	err = tdsDB.QueryRowContext(ctx, `
		SELECT o.display_name
		FROM casdoor_user u
		JOIN casdoor_organization o ON u.org_id = o.id
		WHERE u.name = 'alice';
	`).Scan(&dName)
	if err != nil || dName != "Application Organization" {
		t.Fatalf("Casdoor cross-protocol verify failed: %v (got %q)", err, dName)
	}
}

// 6. GoCD Liquibase Lifecycle
func testGoCDLifecycle(t *testing.T, pgDB, tdsDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddls := []string{
		`CREATE TABLE DATABASECHANGELOGLOCK (
			ID INT PRIMARY KEY,
			LOCKED INT NOT NULL,
			LOCKEDBY VARCHAR(255)
		);`,
		`CREATE TABLE gocd_pipelines (
			id INT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			counter INT NOT NULL
		);`,
	}
	for _, ddl := range ddls {
		if _, err := pgDB.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("GoCD DDL failed: %v", err)
		}
	}

	// Liquibase acquire lock
	_, err := pgDB.ExecContext(ctx, `INSERT INTO DATABASECHANGELOGLOCK VALUES (1, 1, 'node-1');`)
	if err != nil {
		t.Fatalf("GoCD Lock acquire failed: %v", err)
	}

	// Insert pipeline data
	_, err = tdsDB.ExecContext(ctx, `INSERT INTO gocd_pipelines VALUES (10, 'deploy-prod', 42);`)
	if err != nil {
		t.Fatalf("GoCD Pipeline insert failed: %v", err)
	}

	// Release lock
	_, err = pgDB.ExecContext(ctx, `UPDATE DATABASECHANGELOGLOCK SET LOCKED = 0, LOCKEDBY = NULL WHERE ID = 1;`)
	if err != nil {
		t.Fatalf("GoCD Lock release failed: %v", err)
	}

	var counter int
	err = pgDB.QueryRowContext(ctx, `SELECT counter FROM gocd_pipelines WHERE name = 'deploy-prod';`).Scan(&counter)
	if err != nil || counter != 42 {
		t.Fatalf("GoCD pipeline query failed: %v (got %d)", err, counter)
	}
}

// 7. Umami Prisma Lifecycle
func testUmamiLifecycle(t *testing.T, pgDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddls := []string{
		`CREATE TABLE umami_website (
			website_id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			domain VARCHAR(255) NOT NULL,
			created_at DATETIME
		);`,
		`CREATE TABLE umami_website_event (
			event_id VARCHAR(36) PRIMARY KEY,
			website_id VARCHAR(36) NOT NULL,
			event_name VARCHAR(50),
			page_url VARCHAR(255),
			created_at DATETIME
		);`,
	}
	for _, ddl := range ddls {
		if _, err := pgDB.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("Umami DDL failed: %v", err)
		}
	}

	_, err := pgDB.ExecContext(ctx, `
		INSERT INTO umami_website VALUES ('site-1', 'LionSQL Docs', 'lionsql.org', '2026-01-01 00:00:00');
		INSERT INTO umami_website_event VALUES ('ev-1', 'site-1', 'pageview', '/docs/overview', '2026-01-01 00:01:00');
		INSERT INTO umami_website_event VALUES ('ev-2', 'site-1', 'pageview', '/docs/install', '2026-01-01 00:02:00');
	`)
	if err != nil {
		t.Fatalf("Umami INSERT failed: %v", err)
	}

	var eventCount int
	err = pgDB.QueryRowContext(ctx, `
		SELECT COUNT(*) 
		FROM umami_website_event e
		JOIN umami_website w ON e.website_id = w.website_id
		WHERE w.domain = 'lionsql.org';
	`).Scan(&eventCount)
	if err != nil || eventCount != 2 {
		t.Fatalf("Umami aggregate query failed: %v (got %d)", err, eventCount)
	}
}

// 8. Woodpecker CI Lifecycle
func testWoodpeckerLifecycle(t *testing.T, pgDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddl := `
	CREATE TABLE wp_repos (
		id INT PRIMARY KEY,
		owner VARCHAR(50),
		name VARCHAR(50)
	);
	CREATE TABLE wp_builds (
		id INT PRIMARY KEY,
		repo_id INT,
		number INT,
		status VARCHAR(20)
	);
	`
	if _, err := pgDB.ExecContext(ctx, ddl); err != nil {
		t.Fatalf("Woodpecker DDL failed: %v", err)
	}

	_, err := pgDB.ExecContext(ctx, `
		INSERT INTO wp_repos VALUES (1, 'org', 'repo');
		INSERT INTO wp_builds VALUES (101, 1, 1, 'success');
		INSERT INTO wp_builds VALUES (102, 1, 2, 'running');
	`)
	if err != nil {
		t.Fatalf("Woodpecker INSERT failed: %v", err)
	}

	var latestStatus string
	err = pgDB.QueryRowContext(ctx, `
		SELECT status 
		FROM wp_builds 
		WHERE repo_id = 1 
		ORDER BY number DESC 
		LIMIT 1;
	`).Scan(&latestStatus)
	if err != nil || latestStatus != "running" {
		t.Fatalf("Woodpecker query failed: %v (got %q)", err, latestStatus)
	}
}

// 9. Vikunja XORM Lifecycle
func testVikunjaLifecycle(t *testing.T, pgDB *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddl := `
	CREATE TABLE vikunja_tasks (
		id INT PRIMARY KEY,
		title VARCHAR(255) NOT NULL,
		description TEXT,
		done BOOLEAN,
		priority INT
	);
	`
	if _, err := pgDB.ExecContext(ctx, ddl); err != nil {
		t.Fatalf("Vikunja DDL failed: %v", err)
	}

	_, err := pgDB.ExecContext(ctx, `
		INSERT INTO vikunja_tasks VALUES (1, 'Release v1.0', 'Final safety audits', false, 1);
		INSERT INTO vikunja_tasks VALUES (2, 'Celebrate', 'Team party', false, 2);
	`)
	if err != nil {
		t.Fatalf("Vikunja INSERT failed: %v", err)
	}

	if _, err := pgDB.ExecContext(ctx, `UPDATE vikunja_tasks SET done = true WHERE id = 1;`); err != nil {
		t.Fatalf("Vikunja UPDATE failed: %v", err)
	}

	var isDone bool
	err = pgDB.QueryRowContext(ctx, `SELECT done FROM vikunja_tasks WHERE id = 1;`).Scan(&isDone)
	if err != nil || !isDone {
		t.Fatalf("Vikunja verify failed: %v (got %v)", err, isDone)
	}
}
