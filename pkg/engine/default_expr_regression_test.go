package engine_test

import (
	"testing"
	"time"

	"lionsql/pkg/engine"
)

func TestDefaultExpr_DynamicEvaluation(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	// 1. Create table with DEFAULT NEWID() and DEFAULT GETDATE()
	createSQL := `CREATE TABLE test_defaults (
		id UNIQUEIDENTIFIER DEFAULT NEWID(),
		created_at DATETIME DEFAULT GETDATE(),
		name VARCHAR(50)
	);`
	if _, err := coord.ExecuteSQL(createSQL); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. Insert row 1
	if _, err := coord.ExecuteSQL("INSERT INTO test_defaults (name) VALUES ('row1');"); err != nil {
		t.Fatalf("INSERT row 1 failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// 3. Insert row 2
	if _, err := coord.ExecuteSQL("INSERT INTO test_defaults (name) VALUES ('row2');"); err != nil {
		t.Fatalf("INSERT row 2 failed: %v", err)
	}

	// 4. Insert row 3 with explicit DEFAULT keyword
	resInsert, err := coord.ExecuteSQL("INSERT INTO test_defaults (id, created_at, name) VALUES (DEFAULT, DEFAULT, 'row3');")
	if err != nil {
		t.Fatalf("INSERT row 3 with DEFAULT failed: %v", err)
	}
	t.Logf("resInsert row3: %+v", resInsert)

	// 5. Multi-row batch insert omitting defaults
	if _, err := coord.ExecuteSQL("INSERT INTO test_defaults (name) VALUES ('batch1'), ('batch2');"); err != nil {
		t.Fatalf("INSERT batch failed: %v", err)
	}

	// 6. Query all rows
	res, err := coord.ExecuteSQL("SELECT id, created_at, name FROM test_defaults ORDER BY name ASC;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	tbl, err := coord.GetTable("test_defaults")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	t.Logf("Testing parseLiteralValue: %v", engine.ParseLiteralValue("NEWID()", tbl.Columns[0].Type))
	for _, col := range tbl.Columns {
		t.Logf("Column %s: Type=%v, Default=%q", col.Name, col.Type, col.Default)
	}

	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(res.Rows))
	}

	seenIDs := make(map[string]bool)
	for i, r := range res.Rows {
		t.Logf("Row %d (%s): id=%v, created_at=%v", i, r["name"], r["id"], r["created_at"])
		idStr, ok := r["id"].(string)
		if !ok || idStr == "" || idStr == "NEWID()" || idStr == "NEWID" {
			t.Errorf("row %d (%s): id is not a valid generated UUID: %v", i, r["name"], r["id"])
		} else {
			if seenIDs[idStr] {
				t.Errorf("CRITICAL DEFECT: duplicate UUID %s found in row %d (%s)!", idStr, i, r["name"])
			}
			seenIDs[idStr] = true
		}

		if r["created_at"] == nil || r["created_at"] == "" || r["created_at"] == "GETDATE()" {
			t.Errorf("row %d (%s): created_at is not a valid timestamp: %v", i, r["name"], r["created_at"])
		}
	}

	// 7. ALTER TABLE ADD COLUMN with DEFAULT NEWID()
	if _, err := coord.ExecuteSQL("ALTER TABLE test_defaults ADD extra_uuid UNIQUEIDENTIFIER DEFAULT NEWID();"); err != nil {
		t.Fatalf("ALTER TABLE ADD COLUMN failed: %v", err)
	}
	resAlter, err := coord.ExecuteSQL("SELECT id, extra_uuid, name FROM test_defaults ORDER BY name ASC;")
	if err != nil {
		t.Fatalf("SELECT after alter failed: %v", err)
	}
	seenExtra := make(map[string]bool)
	for i, r := range resAlter.Rows {
		t.Logf("After ALTER Row %d (%s): extra_uuid=%v", i, r["name"], r["extra_uuid"])
		uStr, ok := r["extra_uuid"].(string)
		if !ok || uStr == "" || uStr == "NEWID()" {
			t.Errorf("row %d (%s): extra_uuid is not a valid UUID: %v", i, r["name"], r["extra_uuid"])
		} else {
			if seenExtra[uStr] {
				t.Errorf("CRITICAL DEFECT: duplicate extra_uuid %s found across existing rows in ALTER TABLE!", uStr)
			}
			seenExtra[uStr] = true
		}
	}
}
