package memengine

import (
	"fmt"
	"sync"
	"testing"

	"lionsql/pkg/storage"
)

// Regression test for P0-29: memengine txPool pointer retention & rollback map leak prevention
func TestTxPool_Regression_P0_29_ResetForPool(t *testing.T) {
	eng := New()
	defer eng.Close()

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to open session: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	memTx := tx.(*Transaction)

	// Populate uncommitted rollback modifications
	row := storage.Row{Values: []storage.Value{storage.StringValue("uncommitted_sensitive_data")}}
	memTx.local["accounts"] = map[string]storage.Row{"acc1": row}
	memTx.changes["accounts:acc1"] = rowChange{key: "acc1", row: row}
	memTx.deleted["accounts"] = map[string]bool{"acc2": true}
	memTx.baseVers["accounts"] = map[string]int64{"acc1": 1}

	// Rollback transaction to return it to txPool
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	// Verify all transaction maps and references were completely wiped clean
	if len(memTx.local) != 0 {
		t.Fatalf("expected memTx.local to be empty, got %d entries", len(memTx.local))
	}
	if len(memTx.changes) != 0 {
		t.Fatalf("expected memTx.changes to be empty, got %d entries", len(memTx.changes))
	}
	if len(memTx.deleted) != 0 {
		t.Fatalf("expected memTx.deleted to be empty, got %d entries", len(memTx.deleted))
	}
	if len(memTx.baseVers) != 0 {
		t.Fatalf("expected memTx.baseVers to be empty, got %d entries", len(memTx.baseVers))
	}
	if memTx.engine != nil {
		t.Fatalf("expected memTx.engine to be nil")
	}
}

func TestTxPool_Regression_P0_29_ConcurrentTxRecycle(t *testing.T) {
	eng := New()
	defer eng.Close()

	const numWorkers = 8
	const iterations = 300
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			defer wg.Done()
			sess, err := eng.NewSession()
			if err != nil {
				return
			}
			defer sess.Close()

			for i := 0; i < iterations; i++ {
				tx, err := sess.BeginTx(storage.TxOptions{})
				if err != nil {
					return
				}
				memTx := tx.(*Transaction)
				memTx.local["t"] = map[string]storage.Row{
					"k": {Values: []storage.Value{storage.StringValue(fmt.Sprintf("w_%d_i_%d", workerID, i))}},
				}
				_ = tx.Rollback()
			}
		}(w)
	}

	wg.Wait()
}
