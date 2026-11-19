package e2e_test

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

// setupDualServer launches both PGWire (:5433 equivalent) and TDS (:1433 equivalent)
// sharing the exact same in-memory database storage and catalog instances.
func setupDualServer(t *testing.T) (*wire.Server, int, *tds.Server, int, func()) {
	t.Helper()

	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}
	cat := catalog.New(eng)

	pgSrv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := pgSrv.Start(); err != nil {
		eng.Close()
		t.Fatalf("failed to start PGWire server: %v", err)
	}
	pgPort := pgSrv.Addr().(*net.TCPAddr).Port

	tdsSrv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := tdsSrv.Start(); err != nil {
		pgSrv.Close()
		eng.Close()
		t.Fatalf("failed to start TDS server: %v", err)
	}
	tdsPort := tdsSrv.Addr().(*net.TCPAddr).Port

	cleanup := func() {
		tdsSrv.Close()
		pgSrv.Close()
		eng.Close()
	}

	return pgSrv, pgPort, tdsSrv, tdsPort, cleanup
}

func openConnections(t *testing.T, pgPort, tdsPort int) (*sql.DB, *sql.DB) {
	t.Helper()

	pgConn := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", pgPort)
	pgDB, err := sql.Open("postgres", pgConn)
	if err != nil {
		t.Fatalf("sql.Open postgres failed: %v", err)
	}

	tdsConn := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", tdsPort)
	tdsDB, err := sql.Open("sqlserver", tdsConn)
	if err != nil {
		pgDB.Close()
		t.Fatalf("sql.Open sqlserver failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pgDB.PingContext(ctx); err != nil {
		pgDB.Close()
		tdsDB.Close()
		t.Fatalf("PGWire ping failed: %v", err)
	}
	if err := tdsDB.PingContext(ctx); err != nil {
		pgDB.Close()
		tdsDB.Close()
		t.Fatalf("TDS ping failed: %v", err)
	}

	return pgDB, tdsDB
}

func TestE2E_UnifiedSQLSuperset_DualProtocolParity(t *testing.T) {
	_, pgPort, _, tdsPort, cleanup := setupDualServer(t)
	defer cleanup()

	pgDB, tdsDB := openConnections(t, pgPort, tdsPort)
	defer pgDB.Close()
	defer tdsDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Initial Schema and Seed Data via PGWire
	setupDDL := `
	CREATE TABLE user_accounts (
		user_id INT PRIMARY KEY,
		name VARCHAR(50),
		email VARCHAR(100),
		phone VARCHAR(20),
		profile TEXT,
		points INT,
		created_at DATETIME
	);
	`
	if _, err := pgDB.ExecContext(ctx, setupDDL); err != nil {
		t.Fatalf("CREATE TABLE user_accounts via PGWire failed: %v", err)
	}

	seedDML := `
	INSERT INTO user_accounts VALUES
	(1, 'Alice', 'Alice@Company.com', NULL, '{"nickname":"Alicia"}', 100, '2026-01-01 10:00:00'),
	(2, 'Bob', 'bob@gmail.com', '123-456', '{"nickname":"Bobby"}', NULL, '2026-01-02 11:00:00'),
	(3, 'Charlie', 'CHARLIE@company.com', '789-012', '{"nickname":"Chuck"}', 250, '2026-01-03 12:00:00');
	`
	if _, err := tdsDB.ExecContext(ctx, seedDML); err != nil {
		t.Fatalf("INSERT seed data via TDS failed: %v", err)
	}

	dbs := []struct {
		name string
		db   *sql.DB
	}{
		{"PGWire(:5433)", pgDB},
		{"TDS(:1433)", tdsDB},
	}

	for _, tc := range dbs {
		t.Run(tc.name+"_Section5_1_Paging", func(t *testing.T) {
			// SQL Server style TOP
			row := tc.db.QueryRowContext(ctx, `SELECT TOP 1 user_id FROM user_accounts ORDER BY user_id ASC;`)
			var uid int
			if err := row.Scan(&uid); err != nil || uid != 1 {
				t.Fatalf("[%s] SELECT TOP 1 failed: %v (got %d)", tc.name, err, uid)
			}

			// PostgreSQL style LIMIT / OFFSET
			row = tc.db.QueryRowContext(ctx, `SELECT user_id FROM user_accounts ORDER BY user_id ASC LIMIT 1 OFFSET 1;`)
			if err := row.Scan(&uid); err != nil || uid != 2 {
				t.Fatalf("[%s] SELECT LIMIT/OFFSET failed: %v (got %d)", tc.name, err, uid)
			}

			// SQL Server FETCH NEXT
			row = tc.db.QueryRowContext(ctx, `SELECT user_id FROM user_accounts ORDER BY user_id ASC OFFSET 2 ROWS FETCH NEXT 1 ROWS ONLY;`)
			if err := row.Scan(&uid); err != nil || uid != 3 {
				t.Fatalf("[%s] SELECT OFFSET FETCH NEXT failed: %v (got %d)", tc.name, err, uid)
			}
		})

		t.Run(tc.name+"_Section5_2_Casting", func(t *testing.T) {
			// PG ::int casting
			row := tc.db.QueryRowContext(ctx, `SELECT '123'::int + 10 AS total;`)
			var total int
			if err := row.Scan(&total); err != nil || total != 133 {
				t.Fatalf("[%s] ::int casting failed: %v (got %d)", tc.name, err, total)
			}

			// Standard CAST
			row = tc.db.QueryRowContext(ctx, `SELECT CAST('123' AS INT) + 10 AS total;`)
			if err := row.Scan(&total); err != nil || total != 133 {
				t.Fatalf("[%s] CAST AS INT failed: %v (got %d)", tc.name, err, total)
			}
		})

		t.Run(tc.name+"_Section5_3_PatternSearch", func(t *testing.T) {
			// ILIKE (case-insensitive) matches Alice@Company.com and CHARLIE@company.com
			rows, err := tc.db.QueryContext(ctx, `SELECT user_id FROM user_accounts WHERE email ILIKE '%@company.com' ORDER BY user_id ASC;`)
			if err != nil {
				t.Fatalf("[%s] ILIKE query failed: %v", tc.name, err)
			}
			defer rows.Close()
			var ids []int
			for rows.Next() {
				var id int
				if err := rows.Scan(&id); err == nil {
					ids = append(ids, id)
				}
			}
			if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
				t.Fatalf("[%s] unexpected ILIKE ids: %+v (expected [1, 3])", tc.name, ids)
			}

			// Standard LIKE (CI default)
			rows, err = tc.db.QueryContext(ctx, `SELECT user_id FROM user_accounts WHERE email LIKE '%@company.com' ORDER BY user_id ASC;`)
			if err != nil {
				t.Fatalf("[%s] LIKE query failed: %v", tc.name, err)
			}
			defer rows.Close()
			ids = nil
			for rows.Next() {
				var id int
				if err := rows.Scan(&id); err == nil {
					ids = append(ids, id)
				}
			}
			if len(ids) != 2 {
				t.Fatalf("[%s] unexpected LIKE ids count: %d (expected 2)", tc.name, len(ids))
			}

			// COLLATE CS LIKE: matches only 'Alice'
			row := tc.db.QueryRowContext(ctx, `SELECT user_id FROM user_accounts WHERE name COLLATE Latin1_General_CS_AS LIKE 'Ali%';`)
			var csID int
			if err := row.Scan(&csID); err != nil || csID != 1 {
				t.Fatalf("[%s] COLLATE CS LIKE failed: %v (got %d)", tc.name, err, csID)
			}
		})

		t.Run(tc.name+"_Section5_4_JSONExtraction", func(t *testing.T) {
			// Arrow operator ->>
			row := tc.db.QueryRowContext(ctx, `SELECT profile->>'nickname' AS nick FROM user_accounts WHERE user_id = 1;`)
			var nick string
			if err := row.Scan(&nick); err != nil || nick != "Alicia" {
				t.Fatalf("[%s] ->> JSON extraction failed: %v (got %q)", tc.name, err, nick)
			}

			// JSON_VALUE function
			row = tc.db.QueryRowContext(ctx, `SELECT JSON_VALUE(profile, '$.nickname') AS nick FROM user_accounts WHERE user_id = 1;`)
			if err := row.Scan(&nick); err != nil || nick != "Alicia" {
				t.Fatalf("[%s] JSON_VALUE failed: %v (got %q)", tc.name, err, nick)
			}
		})

		t.Run(tc.name+"_Section5_5_NullHandlingAndDates", func(t *testing.T) {
			// ISNULL, COALESCE, IFNULL, NVL
			row := tc.db.QueryRowContext(ctx, `
				SELECT 
					ISNULL(phone, 'N/A') AS p1,
					COALESCE(phone, 'N/A') AS p2,
					IFNULL(phone, 'N/A') AS p3,
					NVL(phone, 'N/A') AS p4
				FROM user_accounts WHERE user_id = 1;
			`)
			var p1, p2, p3, p4 string
			if err := row.Scan(&p1, &p2, &p3, &p4); err != nil || p1 != "N/A" || p2 != "N/A" || p3 != "N/A" || p4 != "N/A" {
				t.Fatalf("[%s] NULL handling functions failed: %v (%s, %s, %s, %s)", tc.name, err, p1, p2, p3, p4)
			}

			// GETDATE() and NOW()
			row = tc.db.QueryRowContext(ctx, `SELECT GETDATE(), NOW(), CURRENT_TIMESTAMP;`)
			var d1, d2, d3 any
			if err := row.Scan(&d1, &d2, &d3); err != nil {
				t.Fatalf("[%s] Date functions failed: %v", tc.name, err)
			}
		})

		t.Run(tc.name+"_Section5_6_Identifiers", func(t *testing.T) {
			// Brackets []
			row := tc.db.QueryRowContext(ctx, `SELECT [user_id] FROM [user_accounts] WHERE [user_id] = 1;`)
			var id int
			if err := row.Scan(&id); err != nil || id != 1 {
				t.Fatalf("[%s] Bracket identifiers failed: %v", tc.name, err)
			}

			// Double quotes ""
			row = tc.db.QueryRowContext(ctx, `SELECT "user_id" FROM "user_accounts" WHERE "user_id" = 1;`)
			if err := row.Scan(&id); err != nil || id != 1 {
				t.Fatalf("[%s] Double quote identifiers failed: %v", tc.name, err)
			}

			// Backticks ``
			row = tc.db.QueryRowContext(ctx, "SELECT `user_id` FROM `user_accounts` WHERE `user_id` = 1;")
			if err := row.Scan(&id); err != nil || id != 1 {
				t.Fatalf("[%s] Backtick identifiers failed: %v", tc.name, err)
			}
		})

		t.Run(tc.name+"_Section6_MixedSupersetQuery", func(t *testing.T) {
			mixedSQL := `
			SELECT 
				[user_id],
				profile->>'nickname' AS nickname,
				ISNULL(points, 0)::int AS points
			FROM "user_accounts"
			WHERE email ILIKE '%@company.com'
			ORDER BY created_at DESC
			LIMIT 10 OFFSET 0;
			`
			rows, err := tc.db.QueryContext(ctx, mixedSQL)
			if err != nil {
				t.Fatalf("[%s] Mixed Superset Query execution failed: %v", tc.name, err)
			}
			defer rows.Close()

			type userResult struct {
				id     int
				nick   string
				points int
			}
			var results []userResult
			for rows.Next() {
				var r userResult
				if err := rows.Scan(&r.id, &r.nick, &r.points); err != nil {
					t.Fatalf("[%s] Scan failed on mixed query: %v", tc.name, err)
				}
				results = append(results, r)
			}

			if len(results) != 2 {
				t.Fatalf("[%s] expected 2 rows, got %d: %+v", tc.name, len(results), results)
			}

			// Ordered by created_at DESC: User 3 (2026-01-03) first, then User 1 (2026-01-01)
			if results[0].id != 3 || results[0].nick != "Chuck" || results[0].points != 250 {
				t.Fatalf("[%s] Row 0 mismatch: %+v (expected id=3, nick=Chuck, points=250)", tc.name, results[0])
			}
			if results[1].id != 1 || results[1].nick != "Alicia" || results[1].points != 100 {
				t.Fatalf("[%s] Row 1 mismatch: %+v (expected id=1, nick=Alicia, points=100)", tc.name, results[1])
			}
		})
	}
}
