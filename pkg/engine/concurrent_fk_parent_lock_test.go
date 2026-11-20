package engine_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestConcurrent_FK_Parent_Delete_Race verifies that when Tx1 inserts a child referencing parent row P,
// and Tx2 concurrently deletes parent row P, the engine enforces referential integrity without leaving
// ghost orphan child rows. One transaction must succeed and the conflicting one must fail with
// serialization failure or foreign key violation.
func TestConcurrent_FK_Parent_Delete_Race(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sessSetup := "sess_fk_setup"

	// 1. Setup parent and child tables with FOREIGN KEY constraint
	setupSQL := `
CREATE TABLE t_parent (
    id INT PRIMARY KEY,
    name VARCHAR(50)
);

CREATE TABLE t_child (
    cid INT PRIMARY KEY,
    parent_id INT,
    val VARCHAR(50),
    FOREIGN KEY (parent_id) REFERENCES t_parent(id)
);

INSERT INTO t_parent VALUES (1, 'Parent One');
`
	for _, stmt := range engine.SplitStatements(setupSQL) {
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessSetup, stmt); err != nil {
			t.Fatalf("setup failed: %v", err)
		}
	}

	// 2. Concurrently execute:
	// Session 1: BEGIN; INSERT INTO t_child VALUES (10, 1, 'Child 10'); COMMIT;
	// Session 2: BEGIN; DELETE FROM t_parent WHERE id = 1; COMMIT;
	sess1 := "sess_fk_child_insert"
	sess2 := "sess_fk_parent_delete"

	var wg sync.WaitGroup
	var s1Err atomic.Pointer[error]
	var s2Err atomic.Pointer[error]

	startBarrier := make(chan struct{})

	// Worker 1: Insert child referencing parent 1
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startBarrier

		if _, err := coord.ExecuteSessionSQLContext(ctx, sess1, "BEGIN;"); err != nil {
			s1Err.Store(&err)
			return
		}
		if _, err := coord.ExecuteSessionSQLContext(ctx, sess1, "INSERT INTO t_child VALUES (10, 1, 'Child 10');"); err != nil {
			_, _ = coord.ExecuteSessionSQLContext(ctx, sess1, "ROLLBACK;")
			s1Err.Store(&err)
			return
		}
		time.Sleep(50 * time.Millisecond) // Hold transaction to overlap with Session 2
		if _, err := coord.ExecuteSessionSQLContext(ctx, sess1, "COMMIT;"); err != nil {
			s1Err.Store(&err)
			return
		}
	}()

	// Worker 2: Delete parent 1
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startBarrier
		time.Sleep(10 * time.Millisecond) // Let Session 1 perform child insert first

		if _, err := coord.ExecuteSessionSQLContext(ctx, sess2, "BEGIN;"); err != nil {
			s2Err.Store(&err)
			return
		}
		if _, err := coord.ExecuteSessionSQLContext(ctx, sess2, "DELETE FROM t_parent WHERE id = 1;"); err != nil {
			_, _ = coord.ExecuteSessionSQLContext(ctx, sess2, "ROLLBACK;")
			s2Err.Store(&err)
			return
		}
		if _, err := coord.ExecuteSessionSQLContext(ctx, sess2, "COMMIT;"); err != nil {
			s2Err.Store(&err)
			return
		}
	}()

	close(startBarrier)
	wg.Wait()

	var err1 error
	if e := s1Err.Load(); e != nil {
		err1 = *e
	}
	var err2 error
	if e := s2Err.Load(); e != nil {
		err2 = *e
	}

	t.Logf("Session 1 (Child Insert) err: %v", err1)
	t.Logf("Session 2 (Parent Delete) err: %v", err2)

	// Invariant: Both cannot succeed if child was committed while parent was deleted.
	// If both succeeded, check if child exists while parent is missing (orphan row corruption).
	resChild, _ := coord.ExecuteSessionSQLContext(ctx, sessSetup, "SELECT COUNT(*) AS cnt FROM t_child WHERE parent_id = 1;")
	resParent, _ := coord.ExecuteSessionSQLContext(ctx, sessSetup, "SELECT COUNT(*) AS cnt FROM t_parent WHERE id = 1;")

	childCount := resChild.Rows[0]["cnt"]
	parentCount := resParent.Rows[0]["cnt"]

	t.Logf("Final State: parent count = %v, child count = %v", parentCount, childCount)

	if childCount == int64(1) && parentCount == int64(0) {
		t.Fatalf("VIOLATION: Ghost orphan child row exists! Child has parent_id=1 but parent row was deleted!")
	}

	// At least one transaction must have encountered a conflict / serialization failure / FK violation
	if err1 == nil && err2 == nil {
		t.Fatalf("Expected at least one session to detect concurrent FK parent deletion conflict, but both succeeded!")
	}
}
