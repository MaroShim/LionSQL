package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestEngine_MultiRowInsert_BatchPipeline(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Create table with indexes
	ddl := `CREATE TABLE bulk_items (
		id INT IDENTITY(1, 1) PRIMARY KEY,
		sku VARCHAR(50) NOT NULL,
		price INT NOT NULL,
		category VARCHAR(50) NOT NULL
	);
	CREATE INDEX idx_bulk_sku ON bulk_items (sku);
	CREATE INDEX idx_bulk_cat ON bulk_items (category);`

	if _, err := coord.ExecuteSQL(ddl); err != nil {
		t.Fatalf("DDL failed: %v", err)
	}

	// 2. Multi-row insert with 200 rows
	var valParts []string
	for i := 1; i <= 200; i++ {
		valParts = append(valParts, fmt.Sprintf("('SKU-%04d', %d, 'CAT-%d')", i, i*10, i%5))
	}
	insertSQL := fmt.Sprintf("INSERT INTO bulk_items (sku, price, category) VALUES %s;", strings.Join(valParts, ", "))

	res, err := coord.ExecuteSQL(insertSQL)
	if err != nil {
		t.Fatalf("Multi-row insert failed: %v", err)
	}
	if res.RowsAffected != 200 {
		t.Fatalf("expected 200 rows affected, got %d", res.RowsAffected)
	}

	// 3. Verify total count
	countRes, err := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM bulk_items;")
	if err != nil {
		t.Fatalf("Count query failed: %v", err)
	}
	if len(countRes.Rows) != 1 || fmt.Sprintf("%v", countRes.Rows[0]["cnt"]) != "200" {
		t.Fatalf("expected count 200, got %v", countRes.Rows[0]["cnt"])
	}

	// 4. Verify index lookup
	idxRes, err := coord.ExecuteSQL("SELECT id, sku, price FROM bulk_items WHERE sku = 'SKU-0042';")
	if err != nil {
		t.Fatalf("Index query failed: %v", err)
	}
	if len(idxRes.Rows) != 1 || fmt.Sprintf("%v", idxRes.Rows[0]["price"]) != "420" {
		t.Fatalf("expected price 420 for SKU-0042, got %v", idxRes.Rows)
	}

	// 5. Test INSERT INTO ... SELECT bulk batching
	insSelectDDL := `CREATE TABLE bulk_items_copy (
		id INT PRIMARY KEY,
		sku VARCHAR(50) NOT NULL,
		price INT NOT NULL,
		category VARCHAR(50) NOT NULL
	);`
	if _, err := coord.ExecuteSQL(insSelectDDL); err != nil {
		t.Fatalf("insSelectDDL failed: %v", err)
	}

	insSelectSQL := "INSERT INTO bulk_items_copy (id, sku, price, category) SELECT id, sku, price, category FROM bulk_items WHERE price <= 500;"
	selRes, err := coord.ExecuteSQL(insSelectSQL)
	if err != nil {
		t.Fatalf("INSERT SELECT failed: %v", err)
	}
	if selRes.RowsAffected != 50 {
		t.Fatalf("expected 50 rows affected in INSERT SELECT, got %d", selRes.RowsAffected)
	}
}
