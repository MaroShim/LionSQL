package slt_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/slt"
	"lionsql/pkg/storage/memengine"
)

func TestDEFECT_HotPlanCache_ErrorMasking(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer func() {
		coord.Close()
		eng.Close()
	}()

	sessID := "slt_hot_defect_test"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_hot_cache (id INT);")
	coord.ExecuteSessionSQL(sessID, "INSERT INTO t_hot_cache VALUES (1);")

	hotExec := slt.NewHotCoordinatorExecutor(coord, sessID)

	cols, rows, err := hotExec.Execute("SELECT id FROM t_hot_cache;")
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if len(rows) != 1 || fmt.Sprintf("%v", rows[0]["id"]) != "1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if len(cols) != 1 || cols[0] != "id" {
		t.Fatalf("unexpected cols: %+v", cols)
	}
}
