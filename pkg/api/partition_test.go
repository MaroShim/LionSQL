package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"lionsql/pkg/api"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func setupPartitionTestServer(t *testing.T) (*api.Server, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	srv := api.NewServer(cat, eng)
	cleanup := func() {
		_ = eng.Close()
	}
	return srv, cleanup
}

func TestPartition_CreateTable_Syntax(t *testing.T) {
	srv, cleanup := setupPartitionTestServer(t)
	defer cleanup()

	// 1. toYYYYMM function
	_, err := srv.ExecuteSQL(`CREATE TABLE logs_monthly (
		id INT PRIMARY KEY,
		created_at DATETIME,
		msg VARCHAR(100)
	) PARTITION BY toYYYYMM(created_at);`)
	if err != nil {
		t.Fatalf("CREATE TABLE with toYYYYMM failed: %v", err)
	}

	// 2. MONTH function
	_, err = srv.ExecuteSQL(`CREATE TABLE events_monthly (
		id INT PRIMARY KEY,
		event_date DATE,
		val INT
	) PARTITION BY MONTH(event_date);`)
	if err != nil {
		t.Fatalf("CREATE TABLE with MONTH failed: %v", err)
	}

	// 3. DAY / toYYYYMMDD function
	_, err = srv.ExecuteSQL(`CREATE TABLE daily_stats (
		id INT PRIMARY KEY,
		logged_at DATETIME,
		score FLOAT
	) PARTITION BY DAY(logged_at);`)
	if err != nil {
		t.Fatalf("CREATE TABLE with DAY failed: %v", err)
	}

	// 4. Direct Column
	_, err = srv.ExecuteSQL(`CREATE TABLE items_by_cat (
		id INT PRIMARY KEY,
		category VARCHAR(50),
		price INT
	) PARTITION BY category;`)
	if err != nil {
		t.Fatalf("CREATE TABLE with column partition failed: %v", err)
	}

	// 5. Invalid partition column should return error
	_, err = srv.ExecuteSQL(`CREATE TABLE bad_tbl (
		id INT PRIMARY KEY,
		val VARCHAR(50)
	) PARTITION BY toYYYYMM(non_existent_col);`)
	if err == nil {
		t.Fatalf("expected error for non-existent partition column, got nil")
	}
	if !strings.Contains(err.Error(), "partition column \"non_existent_col\" not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPartition_AutoRolling_InsertAndShowPartitions(t *testing.T) {
	srv, cleanup := setupPartitionTestServer(t)
	defer cleanup()

	_, err := srv.ExecuteSQL(`CREATE TABLE audit_logs (
		id INT PRIMARY KEY,
		created_at DATETIME,
		msg VARCHAR(100)
	) PARTITION BY toYYYYMM(created_at);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Insert rows spanning different months (Auto Rolling partitions)
	inserts := []string{
		`INSERT INTO audit_logs (id, created_at, msg) VALUES (1, '2026-08-15 10:00:00', 'August log');`,
		`INSERT INTO audit_logs (id, created_at, msg) VALUES (2, '2026-09-01 12:00:00', 'September log 1');`,
		`INSERT INTO audit_logs (id, created_at, msg) VALUES (3, '2026-09-28 23:59:59', 'September log 2');`,
		`INSERT INTO audit_logs (id, created_at, msg) VALUES (4, '2026-10-01 00:00:00', 'October log 1');`,
		`INSERT INTO audit_logs (id, created_at, msg) VALUES (5, '2026-10-15 14:30:00', 'October log 2');`,
		`INSERT INTO audit_logs (id, created_at, msg) VALUES (6, '2026-10-31 18:00:00', 'October log 3');`,
	}
	for _, sql := range inserts {
		if _, err := srv.ExecuteSQL(sql); err != nil {
			t.Fatalf("insert failed for %q: %v", sql, err)
		}
	}

	// Verify SHOW PARTITIONS
	res, err := srv.ExecuteSQL(`SHOW PARTITIONS audit_logs;`)
	if err != nil {
		t.Fatalf("SHOW PARTITIONS failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 partitions, got %d: %+v", len(res.Rows), res.Rows)
	}

	expectedCounts := map[string]int64{
		"202608": 1,
		"202609": 2,
		"202610": 3,
	}
	for _, r := range res.Rows {
		pName := fmt.Sprintf("%v", r["partition"])
		cnt := r["row_count"].(int64)
		if expectedCounts[pName] != cnt {
			t.Errorf("partition %q: expected count %d, got %d", pName, expectedCounts[pName], cnt)
		}
	}

	// Verify system.partitions virtual view
	sysRes, err := srv.ExecuteSQL(`SELECT * FROM system.partitions WHERE table_name = 'audit_logs';`)
	if err != nil {
		t.Fatalf("SELECT from system.partitions failed: %v", err)
	}
	if len(sysRes.Rows) != 3 {
		t.Fatalf("expected 3 partitions in system.partitions, got %d", len(sysRes.Rows))
	}
}

func TestPartition_PartitionPruning_ExplainAndQuery(t *testing.T) {
	srv, cleanup := setupPartitionTestServer(t)
	defer cleanup()

	_, err := srv.ExecuteSQL(`CREATE TABLE web_events (
		id INT PRIMARY KEY,
		created_at DATETIME,
		ip VARCHAR(50)
	) PARTITION BY toYYYYMM(created_at);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	for i := 1; i <= 3; i++ {
		_, _ = srv.ExecuteSQL(fmt.Sprintf("INSERT INTO web_events (id, created_at, ip) VALUES (%d, '2026-08-10 10:00:00', '1.1.1.1');", i))
		_, _ = srv.ExecuteSQL(fmt.Sprintf("INSERT INTO web_events (id, created_at, ip) VALUES (%d, '2026-09-10 10:00:00', '2.2.2.2');", i+10))
		_, _ = srv.ExecuteSQL(fmt.Sprintf("INSERT INTO web_events (id, created_at, ip) VALUES (%d, '2026-10-10 10:00:00', '3.3.3.3');", i+20))
	}

	// 1. Range predicate partition pruning (EXPLAIN Text)
	explainRes, err := srv.ExecuteSQL(`EXPLAIN SELECT * FROM web_events WHERE created_at >= '2026-10-01' AND created_at < '2026-11-01';`)
	if err != nil {
		t.Fatalf("EXPLAIN failed: %v", err)
	}
	foundPart := false
	for _, r := range explainRes.Rows {
		line := fmt.Sprintf("%v", r["QUERY PLAN"])
		if strings.Contains(line, "Partitions: [202610]") {
			foundPart = true
			break
		}
	}
	if !foundPart {
		t.Errorf("expected EXPLAIN to indicate Partitions: [202610]; got: %+v", explainRes.Rows)
	}

	// 2. Range predicate partition pruning (EXPLAIN FORMAT JSON)
	jsonRes, err := srv.ExecuteSQL(`EXPLAIN (FORMAT JSON) SELECT * FROM web_events WHERE created_at >= '2026-10-01' AND created_at < '2026-11-01';`)
	if err != nil {
		t.Fatalf("EXPLAIN (FORMAT JSON) failed: %v", err)
	}
	jsonStr := jsonRes.Rows[0]["QUERY PLAN"].(string)
	var parsedPlan []map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsedPlan); err != nil {
		t.Fatalf("failed to unmarshal explain json: %v", err)
	}
	var findSeqScan func(m map[string]any) map[string]any
	findSeqScan = func(m map[string]any) map[string]any {
		if m["Node Type"] == "Seq Scan" {
			return m
		}
		if children, ok := m["Plans"].([]any); ok {
			for _, ch := range children {
				if chMap, ok := ch.(map[string]any); ok {
					if res := findSeqScan(chMap); res != nil {
						return res
					}
				}
			}
		}
		return nil
	}
	seqScanObj := findSeqScan(parsedPlan[0]["Plan"].(map[string]any))
	if seqScanObj == nil {
		t.Fatalf("Seq Scan node not found in JSON plan: %s", jsonStr)
	}
	partsRaw, ok := seqScanObj["Partitions"].([]any)
	if !ok || len(partsRaw) != 1 || partsRaw[0] != "202610" {
		t.Errorf("expected Seq Scan JSON Plan to contain Partitions: [202610], got: %+v", seqScanObj["Partitions"])
	}

	// 3. Execution correctness: returns only October rows
	queryRes, err := srv.ExecuteSQL(`SELECT id, ip FROM web_events WHERE created_at >= '2026-10-01' AND created_at < '2026-11-01';`)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(queryRes.Rows) != 3 {
		t.Errorf("expected 3 rows returned for October, got %d: %+v", len(queryRes.Rows), queryRes.Rows)
	}
}

func TestPartition_AlterTableDropPartition_O1(t *testing.T) {
	srv, cleanup := setupPartitionTestServer(t)
	defer cleanup()

	_, err := srv.ExecuteSQL(`CREATE TABLE app_metrics (
		id INT PRIMARY KEY,
		created_at DATETIME,
		val INT
	) PARTITION BY toYYYYMM(created_at);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, _ = srv.ExecuteSQL(`INSERT INTO app_metrics VALUES (1, '2026-08-01 00:00:00', 10);`)
	_, _ = srv.ExecuteSQL(`INSERT INTO app_metrics VALUES (2, '2026-08-15 00:00:00', 20);`)
	_, _ = srv.ExecuteSQL(`INSERT INTO app_metrics VALUES (3, '2026-09-01 00:00:00', 30);`)
	_, _ = srv.ExecuteSQL(`INSERT INTO app_metrics VALUES (4, '2026-10-01 00:00:00', 40);`)

	// Drop partition '202608'
	alterRes, err := srv.ExecuteSQL(`ALTER TABLE app_metrics DROP PARTITION '202608';`)
	if err != nil {
		t.Fatalf("ALTER TABLE DROP PARTITION failed: %v", err)
	}
	if !strings.Contains(alterRes.Messages[0], "Partition \"202608\" dropped") {
		t.Errorf("unexpected message: %v", alterRes.Messages)
	}

	// Verify 202608 rows are gone
	rowsRes, err := srv.ExecuteSQL(`SELECT * FROM app_metrics;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(rowsRes.Rows) != 2 {
		t.Errorf("expected 2 rows remaining (for Sept and Oct), got %d: %+v", len(rowsRes.Rows), rowsRes.Rows)
	}

	// Drop partition without quotes: 202609
	_, err = srv.ExecuteSQL(`ALTER TABLE app_metrics DROP PARTITION 202609;`)
	if err != nil {
		t.Fatalf("ALTER TABLE DROP PARTITION without quotes failed: %v", err)
	}

	// Verify only 202610 remains
	showRes, err := srv.ExecuteSQL(`SHOW PARTITIONS app_metrics;`)
	if err != nil {
		t.Fatalf("SHOW PARTITIONS failed: %v", err)
	}
	if len(showRes.Rows) != 1 || showRes.Rows[0]["partition"] != "202610" {
		t.Errorf("expected only partition 202610 to remain; got: %+v", showRes.Rows)
	}

	// Dropping non-existent partition should return error
	_, err = srv.ExecuteSQL(`ALTER TABLE app_metrics DROP PARTITION '202608';`)
	if err == nil {
		t.Fatalf("expected error dropping non-existent partition, got nil")
	}
}

func TestPartition_SecondaryIndex_Consistency(t *testing.T) {
	srv, cleanup := setupPartitionTestServer(t)
	defer cleanup()

	_, err := srv.ExecuteSQL(`CREATE TABLE telemetry (
		id INT PRIMARY KEY,
		created_at DATETIME,
		device_id VARCHAR(50)
	) PARTITION BY toYYYYMM(created_at);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = srv.ExecuteSQL(`CREATE INDEX idx_device ON telemetry (device_id);`)
	if err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	_, _ = srv.ExecuteSQL(`INSERT INTO telemetry VALUES (1, '2026-09-01 00:00:00', 'dev-A');`)
	_, _ = srv.ExecuteSQL(`INSERT INTO telemetry VALUES (2, '2026-10-01 00:00:00', 'dev-A');`)
	_, _ = srv.ExecuteSQL(`INSERT INTO telemetry VALUES (3, '2026-10-02 00:00:00', 'dev-B');`)

	// Drop partition 202609
	_, err = srv.ExecuteSQL(`ALTER TABLE telemetry DROP PARTITION '202609';`)
	if err != nil {
		t.Fatalf("DROP PARTITION failed: %v", err)
	}

	// Query device 'dev-A': should only find the October row
	res, err := srv.ExecuteSQL(`SELECT id, device_id FROM telemetry WHERE device_id = 'dev-A';`)
	if err != nil {
		t.Fatalf("index query failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["id"] != int64(2) {
		t.Errorf("expected only row 2 for dev-A; got: %+v", res.Rows)
	}
}

func TestPartition_PersistenceAndReload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_part_persist_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Step 1: Initialize server and insert partitioned records
	eng1 := memengine.New()
	if err := eng1.Open(tmpDir); err != nil {
		t.Fatalf("eng1.Open failed: %v", err)
	}
	cat1 := catalog.New(eng1)
	srv1 := api.NewServer(cat1, eng1)

	_, err = srv1.ExecuteSQL(`CREATE TABLE server_logs (
		id INT PRIMARY KEY,
		created_at DATETIME,
		level VARCHAR(10)
	) PARTITION BY toYYYYMM(created_at);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, _ = srv1.ExecuteSQL(`INSERT INTO server_logs VALUES (101, '2026-09-15 12:00:00', 'INFO');`)
	_, _ = srv1.ExecuteSQL(`INSERT INTO server_logs VALUES (102, '2026-10-15 12:00:00', 'ERROR');`)

	// Close engine (flushes snapshot to disk)
	_ = eng1.Close()

	// Step 2: Reopen engine from disk snapshot
	eng2 := memengine.New()
	if err := eng2.Open(tmpDir); err != nil {
		t.Fatalf("eng2.Open failed: %v", err)
	}
	defer eng2.Close()
	cat2 := catalog.New(eng2)
	srv2 := api.NewServer(cat2, eng2)

	// Verify SHOW PARTITIONS on reloaded server
	showRes, err := srv2.ExecuteSQL(`SHOW PARTITIONS server_logs;`)
	if err != nil {
		t.Fatalf("SHOW PARTITIONS on reloaded server failed: %v", err)
	}
	if len(showRes.Rows) != 2 {
		t.Fatalf("expected 2 partitions after reload, got %d: %+v", len(showRes.Rows), showRes.Rows)
	}

	// Drop partition 202609 on reloaded server
	_, err = srv2.ExecuteSQL(`ALTER TABLE server_logs DROP PARTITION '202609';`)
	if err != nil {
		t.Fatalf("ALTER TABLE DROP PARTITION after reload failed: %v", err)
	}

	// Verify only row 102 remains
	rowsRes, err := srv2.ExecuteSQL(`SELECT id FROM server_logs;`)
	if err != nil {
		t.Fatalf("SELECT after reload failed: %v", err)
	}
	if len(rowsRes.Rows) != 1 || rowsRes.Rows[0]["id"] != int64(102) {
		t.Errorf("expected only row 102 after drop partition, got: %+v", rowsRes.Rows)
	}
}
