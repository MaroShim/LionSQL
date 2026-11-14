package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestAnalyzeTable_BoundedSampling(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	// Create table
	_, err := coord.ExecuteSQL("CREATE TABLE t_large_analyze (id INT PRIMARY KEY, val INT, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Insert 100 rows
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := int64(1); i <= 100; i++ {
		_ = tx.Insert("t_large_analyze", storage.Record{
			"id":   storage.IntValue(i),
			"val":  storage.IntValue(i % 10),
			"name": storage.StringValue(fmt.Sprintf("user_%d", i)),
		})
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ts, err := coord.AnalyzeTable("t_large_analyze")
	if err != nil {
		t.Fatalf("AnalyzeTable failed: %v", err)
	}

	if ts.RowCount != 100 {
		t.Fatalf("expected 100 total rows, got %d", ts.RowCount)
	}
	if len(ts.Columns) != 3 {
		t.Fatalf("expected 3 column stats, got %d", len(ts.Columns))
	}

	valStats := ts.Columns["val"]
	if valStats == nil {
		t.Fatalf("missing stats for column val")
	}
	if valStats.DistinctCount != 10 {
		t.Fatalf("expected 10 distinct values for val, got %d", valStats.DistinctCount)
	}
}
