//go:build cgo && wiredtiger

package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage/wiredtiger"
)

func TestJSON_WiredTigerQueries(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_json_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err = coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, doc VARCHAR(500));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES 
		(1, '{"city": "Seoul", "age": 28, "tags": ["go", "db"]}'),
		(2, '{"city": "Busan", "age": 35, "tags": ["rust"]}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT id, doc->>'city' AS city, JSON_VALUE(doc, '$.age') AS age FROM users WHERE doc->>'city' = 'Seoul';")
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["city"] != "Seoul" || fmt.Sprintf("%v", res.Rows[0]["age"]) != "28" {
		t.Fatalf("expected 1 row with city='Seoul' age=28, got %+v", res.Rows)
	}
}

func TestJSON_ExpressionIndex_WiredTiger(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_json_expr_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err = coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, doc VARCHAR(500));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES 
		(1, '{"city": "Seoul", "country": "KR"}'),
		(2, '{"city": "Busan", "country": "KR"}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	res, err := coord.ExecuteSQL("CREATE INDEX idx_wt_city ON users ((doc->>'city'));")
	if err != nil || !res.Success {
		t.Fatalf("CREATE INDEX ((doc->>'city')) failed: %v, res: %+v", err, res)
	}

	res, err = coord.ExecuteSQL("CREATE INDEX idx_wt_country ON users ((JSON_VALUE(doc, '$.country')));")
	if err != nil || !res.Success {
		t.Fatalf("CREATE INDEX ((JSON_VALUE(...))) failed: %v, res: %+v", err, res)
	}

	tbl, err := eng.GetTable("users")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}

	idxCity, found := tbl.FindExpressionIndex("doc", "$.city")
	if !found || !idxCity.IsExpression() || idxCity.ExprPath != "$.city" {
		t.Fatalf("expected idx_wt_city expression index, got: %+v", idxCity)
	}

	idxCountry, found := tbl.FindExpressionIndex("doc", "$.country")
	if !found || !idxCountry.IsExpression() || idxCountry.ExprPath != "$.country" {
		t.Fatalf("expected idx_wt_country expression index, got: %+v", idxCountry)
	}

	// Test EXPLAIN selecting idx_wt_city on WiredTiger
	resExp, err := coord.ExecuteSQL("EXPLAIN SELECT id, doc FROM users WHERE doc->>'city' = 'Seoul';")
	if err != nil || !resExp.Success {
		t.Fatalf("EXPLAIN failed on WT: %v", err)
	}
	expText := fmt.Sprintf("%v", resExp.Rows)
	if !strings.Contains(expText, "Index Scan") || !strings.Contains(expText, "idx_wt_city") {
		t.Fatalf("expected Index Scan on WT, got: %s", expText)
	}

	// Test SELECT with WHERE condition on WiredTiger
	resQuery, err := coord.ExecuteSQL("SELECT id, doc->>'city' AS city FROM users WHERE doc->>'city' = 'Seoul';")
	if err != nil || !resQuery.Success {
		t.Fatalf("SELECT failed on WT: %v", err)
	}
	if len(resQuery.Rows) != 1 || resQuery.Rows[0]["city"] != "Seoul" {
		t.Fatalf("expected 1 row with city='Seoul' on WT, got: %+v", resQuery.Rows)
	}
}

