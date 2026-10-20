package engine

import (
	"fmt"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestAutoAnalyzer_ThresholdTrigger(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	// 1. Create table 'customers'
	schema := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "city", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 2. Insert initial rows
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := int64(1); i <= 10; i++ {
		_ = tx.Insert("customers", storage.Record{
			"id":   storage.IntValue(i),
			"name": storage.StringValue(fmt.Sprintf("User%d", i)),
			"city": storage.StringValue("Seoul"),
		})
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 3. Initialize Coordinator with AutoAnalyzer (threshold = 20 rows)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	cfg := AutoAnalyzeConfig{
		Enabled:      true,
		MinThreshold: 20,
		ScaleFactor:  0.10,
	}
	analyzer := NewAutoAnalyzer(coord, cfg)
	defer analyzer.Close()

	// Initially stats should have no column metrics in catalog
	if s := cat.ListTableStats()["customers"]; s != nil && len(s.Columns) > 0 {
		t.Fatalf("expected no detailed column stats initially, got: %+v", s)
	}

	// 4. Record mutations below threshold (10 < 20)
	analyzer.RecordMutation("customers", 10)
	if analyzer.DirtyCount("customers") != 10 {
		t.Fatalf("expected dirty count 10, got %d", analyzer.DirtyCount("customers"))
	}
	if s := cat.ListTableStats()["customers"]; s != nil && len(s.Columns) > 0 {
		t.Fatalf("expected detailed stats to still not exist below threshold")
	}

	// 5. Record 15 more mutations (total 25 >= 20 threshold -> trigger!)
	analyzer.RecordMutation("customers", 15)

	// Wait up to 1 second for background analyze to complete
	deadline := time.Now().Add(1 * time.Second)
	var statsFound bool
	for time.Now().Before(deadline) {
		if s := cat.ListTableStats()["customers"]; s != nil && len(s.Columns) == 3 && s.RowCount == 10 {
			statsFound = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !statsFound {
		t.Fatalf("expected AutoAnalyzer to populate detailed table stats for customers")
	}

	// Dirty counter should be reset to 0 after analyze
	if cnt := analyzer.DirtyCount("customers"); cnt != 0 {
		t.Fatalf("expected dirty counter to reset to 0, got %d", cnt)
	}
}

func TestAutoAnalyzer_OnWALRecord(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	analyzer := NewAutoAnalyzer(coord, AutoAnalyzeConfig{
		Enabled:      true,
		MinThreshold: 5,
		ScaleFactor:  0.10,
	})
	defer analyzer.Close()

	// Simulate WAL record with 6 INSERT ops
	ops := make([]memengine.WALOp, 6)
	for i := 0; i < 6; i++ {
		ops[i] = memengine.WALOp{
			Type:  memengine.OpInsert,
			Table: "items",
			Key:   fmt.Sprintf("%d", i),
		}
	}

	analyzer.OnWALRecord(memengine.WALRecord{
		LSN: 1,
		Ops: ops,
	})

	// Wait for background analyze to complete before closing engine
	deadline := time.Now().Add(1 * time.Second)
	var analyzed bool
	for time.Now().Before(deadline) {
		if s := cat.ListTableStats()["items"]; s != nil && len(s.Columns) == 2 {
			analyzed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !analyzed {
		t.Fatalf("expected items table to be analyzed from WAL hook")
	}
}
