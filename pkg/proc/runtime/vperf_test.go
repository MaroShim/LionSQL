package runtime_test

import (
	"errors"
	"sync"
	"testing"

	"lionsql/pkg/proc"
	_ "lionsql/pkg/proc/parser/antlr"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestVPerf_PlanCacheConcurrentReaders(t *testing.T) {
	cache := runtime.NewPlanCacheWithCapacity(10)
	query := "SELECT 42 AS val;"

	// Pre-warm cache
	p1, err := cache.GetOrCompile(query)
	if err != nil {
		t.Fatalf("pre-warm failed: %v", err)
	}

	var wg sync.WaitGroup
	workers := 20
	iterations := 200

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				p, err := cache.GetOrCompile(query)
				if err != nil {
					t.Errorf("GetOrCompile failed: %v", err)
					return
				}
				if p != p1 {
					t.Errorf("expected pointer %p, got %p", p1, p)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestVPerf_TriggerBypassAndWhereOptimizations(t *testing.T) {
	cache := runtime.NewPlanCache()
	exec := runtime.NewExecutor(cache)

	// Verify HasTriggers returns false when no triggers exist
	if exec.HasTriggers("test_tab", "INSERT") {
		t.Fatalf("expected HasTriggers to return false for table without triggers")
	}

	eng := memengine.New()
	err := eng.CreateTable(&storage.TableSchema{
		Name: "t_vperf",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	procSQL := `
CREATE PROCEDURE dbo.sp_VperfTest
AS
BEGIN
    INSERT INTO t_vperf (id, val) VALUES (1, 10);
    INSERT INTO t_vperf (id, val) VALUES (2, 20);
    INSERT INTO t_vperf (id, val) VALUES (3, 30);
    UPDATE t_vperf SET val = val + 1 WHERE id > 1;
    DELETE FROM t_vperf WHERE id = 1;
    RETURN 0;
END
`
	procStmt, err := proc.Parse(procSQL)
	if err != nil {
		t.Fatalf("proc.Parse failed: %v", err)
	}
	procProg, err := proc.CompileProcedure(procStmt)
	if err != nil {
		t.Fatalf("CompileProcedure failed: %v", err)
	}

	_, err = exec.Run(procProg, "dbo.sp_VperfTest", nil, session)
	if err != nil {
		t.Fatalf("exec.Run failed: %v", err)
	}

	// Verify results
	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	// id=1 should be deleted
	if _, err := tx.Get("t_vperf", storage.IntValue(1)); !errors.Is(err, storage.ErrKeyNotFound) {
		t.Fatalf("expected id=1 to be deleted, got err: %v", err)
	}

	// id=2 should have val=21
	rec2, err := tx.Get("t_vperf", storage.IntValue(2))
	if err != nil {
		t.Fatalf("expected id=2 to exist: %v", err)
	}
	if rec2["val"].IVal != 21 {
		t.Fatalf("expected val=21, got %d", rec2["val"].IVal)
	}
}
