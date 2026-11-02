package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestStoredProcedureInsertSelect(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create source and destination tables
	if _, err := coord.ExecuteSQL("CREATE TABLE source_items (id INT PRIMARY KEY, name VARCHAR(50), qty INT);"); err != nil {
		t.Fatalf("CREATE TABLE source_items failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE dest_items (id INT PRIMARY KEY, name VARCHAR(50), qty INT);"); err != nil {
		t.Fatalf("CREATE TABLE dest_items failed: %v", err)
	}

	// 2. Populate source items
	if _, err := coord.ExecuteSQL("INSERT INTO source_items (id, name, qty) VALUES (1, 'Apple', 10), (2, 'Banana', 20), (3, 'Cherry', 30);"); err != nil {
		t.Fatalf("INSERT INTO source_items failed: %v", err)
	}

	// 3. Create procedure with INSERT INTO ... SELECT referencing input variable
	procSQL := `CREATE PROCEDURE sp_CopyItems
    @p_min_qty INT
AS
BEGIN
    INSERT INTO dest_items (id, name, qty)
    SELECT id, name, qty
    FROM source_items
    WHERE qty >= @p_min_qty;
END;`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_CopyItems failed: %v", err)
	}

	// 4. Execute procedure with @p_min_qty = 15 (should copy Banana and Cherry)
	res, err := coord.ExecuteSQL("EXEC sp_CopyItems @p_min_qty = 15;")
	if err != nil {
		t.Fatalf("EXEC sp_CopyItems failed: %v", err)
	}
	_ = res

	// 5. Verify destination items
	checkRes, err := coord.ExecuteSQL("SELECT id, name, qty FROM dest_items ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT from dest_items failed: %v", err)
	}
	if len(checkRes.Rows) != 2 {
		t.Fatalf("expected 2 copied rows, got %d", len(checkRes.Rows))
	}

	if fmt.Sprintf("%v", checkRes.Rows[0]["name"]) != "Banana" || fmt.Sprintf("%v", checkRes.Rows[1]["name"]) != "Cherry" {
		t.Fatalf("unexpected destination rows: %+v", checkRes.Rows)
	}

	// 6. Test procedure with multiple steps: INSERT INTO ... SELECT followed by UPDATE
	procMulti := `CREATE PROCEDURE sp_ArchiveAndClear
    @p_id INT
AS
BEGIN
    INSERT INTO dest_items (id, name, qty)
    SELECT id, name, qty FROM source_items WHERE id = @p_id;

    DELETE FROM source_items WHERE id = @p_id;
END;`
	if _, err := coord.ExecuteSQL(procMulti); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_ArchiveAndClear failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("EXEC sp_ArchiveAndClear @p_id = 1;"); err != nil {
		t.Fatalf("EXEC sp_ArchiveAndClear failed: %v", err)
	}

	// Verify id 1 was deleted from source_items and now in dest_items
	srcRes, _ := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM source_items WHERE id = 1;")
	if fmt.Sprintf("%v", srcRes.Rows[0]["cnt"]) != "0" {
		t.Fatalf("expected 0 in source_items, got %v", srcRes.Rows[0]["cnt"])
	}

	dstRes, _ := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM dest_items WHERE id = 1;")
	if fmt.Sprintf("%v", dstRes.Rows[0]["cnt"]) != "1" {
		t.Fatalf("expected 1 in dest_items, got %v", dstRes.Rows[0]["cnt"])
	}
}
