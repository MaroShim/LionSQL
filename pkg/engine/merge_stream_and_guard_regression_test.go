package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// TestMerge_Regression_P0_18_StreamLookupAndMemoryGuardrail tests both paths of P0-18:
// Path 1 (Fast Direct PK Streaming): Target table with 60,000 rows (> 50k limit) succeeds
// without full-scan heap allocation when ON target.id = source.id.
// Path 2 (Memory Guardrail): Non-indexed ON condition or BY SOURCE scan exceeding 50,000 rows
// fails closed with ErrMergeTargetTableTooLarge to prevent OOM crash ([RULE:BoundedBuffers]).
func TestMerge_Regression_P0_18_StreamLookupAndMemoryGuardrail(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create target table
	ddlTarget := `
	CREATE TABLE large_target (
		id INT PRIMARY KEY,
		val VARCHAR(50) NOT NULL,
		code VARCHAR(50) NOT NULL
	);
	`
	if _, err := coord.ExecuteSQL(ddlTarget); err != nil {
		t.Fatalf("create target table failed: %v", err)
	}

	// Populate target table with 60,000 rows in batches of 1,000
	t.Log("Populating large_target with 60,000 rows...")
	for b := 0; b < 60; b++ {
		var sb strings.Builder
		sb.WriteString("INSERT INTO large_target (id, val, code) VALUES ")
		for i := 1; i <= 1000; i++ {
			id := int64(b*1000 + i)
			if i > 1 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("(%d, 'orig_%d', 'code_%d')", id, id, id))
		}
		if _, err := coord.ExecuteSQL(sb.String()); err != nil {
			t.Fatalf("batch insert failed at batch %d: %v", b, err)
		}
	}
	t.Log("large_target populated with 60,000 rows successfully.")

	// Create source table
	ddlSource := `
	CREATE TABLE source_items (
		src_id INT PRIMARY KEY,
		src_val VARCHAR(50) NOT NULL,
		src_code VARCHAR(50) NOT NULL
	);
	`
	if _, err := coord.ExecuteSQL(ddlSource); err != nil {
		t.Fatalf("create source table failed: %v", err)
	}

	// Insert 5 rows: 3 updates (id 1, 2, 3) and 2 inserts (id 90001, 90002)
	insertSource := `
	INSERT INTO source_items (src_id, src_val, src_code) VALUES
	(1, 'updated_1', 'C1'),
	(2, 'updated_2', 'C2'),
	(3, 'updated_3', 'C3'),
	(90001, 'inserted_90001', 'C90001'),
	(90002, 'inserted_90002', 'C90002');
	`
	if _, err := coord.ExecuteSQL(insertSource); err != nil {
		t.Fatalf("insert source failed: %v", err)
	}

	// -------------------------------------------------------------------------
	// Scenario 1: Fast Direct PK Streaming Path (Target > 50,000 rows)
	// -------------------------------------------------------------------------
	t.Run("FastDirectPKStreaming_SucceedsWithoutFullScanHeapOOM", func(t *testing.T) {
		mergeSQL := `
		MERGE large_target AS t
		USING source_items AS s
		ON t.id = s.src_id
		WHEN MATCHED THEN
			UPDATE SET val = s.src_val, code = s.src_code
		WHEN NOT MATCHED THEN
			INSERT (id, val, code) VALUES (s.src_id, s.src_val, s.src_code);
		`
		res, err := coord.ExecuteSQL(mergeSQL)
		if err != nil {
			t.Fatalf("[P0-18 DEFECT] Fast path MERGE failed on 60,000 row target table: %v", err)
		}
		if res.RowsAffected != 5 {
			t.Fatalf("[P0-18 DEFECT] Expected 5 rows affected (3 updates + 2 inserts), got %d", res.RowsAffected)
		}

		// Verify update for ID 1
		selUpdate, err := coord.ExecuteSQL("SELECT val FROM large_target WHERE id = 1")
		if err != nil || len(selUpdate.Rows) == 0 || selUpdate.Rows[0]["val"] != "updated_1" {
			t.Fatalf("[P0-18 DEFECT] ID 1 was not updated properly: %+v", selUpdate.Rows)
		}

		// Verify insert for ID 90001
		selInsert, err := coord.ExecuteSQL("SELECT val FROM large_target WHERE id = 90001")
		if err != nil || len(selInsert.Rows) == 0 || selInsert.Rows[0]["val"] != "inserted_90001" {
			t.Fatalf("[P0-18 DEFECT] ID 90001 was not inserted properly: %+v", selInsert.Rows)
		}

		t.Logf("[P0-18 PASS] Fast direct PK streaming completed with %d rows affected without OOM!", res.RowsAffected)
	})

	// -------------------------------------------------------------------------
	// Scenario 2: Memory Guardrail Triggered on Non-Indexed ON Condition
	// -------------------------------------------------------------------------
	t.Run("MemoryGuardrail_TriggeredOnNonIndexedFullScan", func(t *testing.T) {
		// Non-indexed condition (t.val = s.src_val) forces target table full-scan.
		// Since target has 60,000+ rows, it must fail closed with ErrMergeTargetTableTooLarge.
		mergeSQL := `
		MERGE large_target AS t
		USING source_items AS s
		ON t.val = s.src_val
		WHEN MATCHED THEN
			UPDATE SET code = s.src_code;
		`
		_, err := coord.ExecuteSQL(mergeSQL)
		if err == nil {
			t.Fatalf("[P0-18 DEFECT] Expected ErrMergeTargetTableTooLarge on 60,000 row non-indexed MERGE, but got nil!")
		}
		if !errors.Is(err, ErrMergeTargetTableTooLarge) {
			t.Fatalf("[P0-18 DEFECT] Expected ErrMergeTargetTableTooLarge, got: %v", err)
		}

		t.Logf("[P0-18 PASS] Non-indexed full-scan MERGE safely protected by memory guardrail: %v", err)
	})

	// -------------------------------------------------------------------------
	// Scenario 3: Memory Guardrail Triggered on WHEN NOT MATCHED BY SOURCE
	// -------------------------------------------------------------------------
	t.Run("MemoryGuardrail_TriggeredOnBySourceScan", func(t *testing.T) {
		// Even with t.id = s.src_id, BY SOURCE requires scanning the entire target table.
		// Target has 60,000+ rows (> 50,000 limit) -> must fail closed safely.
		mergeSQL := `
		MERGE large_target AS t
		USING source_items AS s
		ON t.id = s.src_id
		WHEN NOT MATCHED BY SOURCE THEN
			DELETE;
		`
		_, err := coord.ExecuteSQL(mergeSQL)
		if err == nil {
			t.Fatalf("[P0-18 DEFECT] Expected ErrMergeTargetTableTooLarge on BY SOURCE MERGE with 60k rows, but got nil!")
		}
		if !errors.Is(err, ErrMergeTargetTableTooLarge) {
			t.Fatalf("[P0-18 DEFECT] Expected ErrMergeTargetTableTooLarge, got: %v", err)
		}

		t.Logf("[P0-18 PASS] BY SOURCE full-scan MERGE safely protected by memory guardrail: %v", err)
	})
}
