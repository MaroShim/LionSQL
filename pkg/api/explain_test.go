package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"lionsql/pkg/api"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestExplain_FormatJSON_VisualDBeaverCompatibility(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Precision: 50},
			{Name: "score", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_score", TableName: "customers", Columns: []string{"score"}, Unique: false},
		},
	})

	// Insert test data
	_, _ = server.ExecuteSQL("INSERT INTO customers VALUES (1, 'Alice', 100);")
	_, _ = server.ExecuteSQL("INSERT INTO customers VALUES (2, 'Bob', 200);")

	// 1. EXPLAIN (FORMAT JSON) SELECT
	res, err := server.ExecuteSQL("EXPLAIN (FORMAT JSON) SELECT * FROM customers WHERE score = 100;")
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN (FORMAT JSON) failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row containing JSON plan, got %d", len(res.Rows))
	}
	jsonStr, ok := res.Rows[0]["QUERY PLAN"].(string)
	if !ok || jsonStr == "" {
		t.Fatalf("expected QUERY PLAN string, got %v", res.Rows[0]["QUERY PLAN"])
	}

	// Verify valid PostgreSQL visual plan JSON array: [{"Plan": {...}}]
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON plan: %v\nJSON:\n%s", err, jsonStr)
	}
	if len(parsed) != 1 || parsed[0]["Plan"] == nil {
		t.Fatalf("expected [{\"Plan\": {...}}], got: %+v", parsed)
	}
	planObj, ok := parsed[0]["Plan"].(map[string]any)
	if !ok || planObj["Node Type"] == "" {
		t.Fatalf("expected Plan object with Node Type, got: %+v", parsed[0]["Plan"])
	}

	// 2. EXPLAIN (ANALYZE, FORMAT JSON) SELECT
	res, err = server.ExecuteSQL("EXPLAIN (ANALYZE, FORMAT JSON) SELECT * FROM customers ORDER BY score DESC LIMIT 1;")
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN (ANALYZE, FORMAT JSON) failed: %v", err)
	}
	jsonStr = res.Rows[0]["QUERY PLAN"].(string)
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("failed to unmarshal ANALYZE JSON plan: %v", err)
	}
	planObj = parsed[0]["Plan"].(map[string]any)
	if planObj["Actual Rows"] == nil {
		t.Errorf("expected Actual Rows in ANALYZE JSON plan: %+v", planObj)
	}
}

func TestExplain_DMLStatements(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "inventory",
		Columns: []storage.Column{
			{Name: "item_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "quantity", Type: storage.TypeInt},
		},
	})

	// 1. EXPLAIN INSERT
	res, err := server.ExecuteSQL("EXPLAIN INSERT INTO inventory VALUES (10, 50);")
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN INSERT failed: %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected rows from EXPLAIN INSERT")
	}
	planLine := res.Rows[0]["QUERY PLAN"].(string)
	if !strings.Contains(planLine, "ModifyTable") && !strings.Contains(planLine, "inventory") {
		t.Errorf("expected ModifyTable on inventory, got: %s", planLine)
	}

	// 2. EXPLAIN UPDATE with PK index lookup
	res, err = server.ExecuteSQL("EXPLAIN UPDATE inventory SET quantity = 100 WHERE item_id = 10;")
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN UPDATE failed: %v", err)
	}
	foundIndexScan := false
	for _, row := range res.Rows {
		if strings.Contains(row["QUERY PLAN"].(string), "Index Scan") {
			foundIndexScan = true
			break
		}
	}
	if !foundIndexScan {
		t.Errorf("expected Index Scan for PK WHERE condition in UPDATE EXPLAIN, got: %+v", res.Rows)
	}

	// 3. EXPLAIN DELETE with JSON format
	res, err = server.ExecuteSQL("EXPLAIN (FORMAT JSON) DELETE FROM inventory WHERE item_id = 10;")
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN (FORMAT JSON) DELETE failed: %v", err)
	}
	jsonStr := res.Rows[0]["QUERY PLAN"].(string)
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("failed to unmarshal DELETE JSON plan: %v", err)
	}
	planObj := parsed[0]["Plan"].(map[string]any)
	if planObj["Node Type"] != "ModifyTable" {
		t.Errorf("expected ModifyTable, got %v", planObj["Node Type"])
	}
}
