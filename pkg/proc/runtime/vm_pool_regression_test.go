package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// Regression test for P0-29: VDBE VM framePool pointer retention & GC leak prevention
func TestVMPool_Regression_P0_29_FullCapZeroing(t *testing.T) {
	exec := NewExecutor(nil)

	prog := &proc.Program{
		NumRegisters: 32,
		SlotCount:    16,
	}

	// 1. Acquire frame and populate with data & pointers
	f := exec.newFrame(prog, nil)
	if f == nil {
		t.Fatalf("failed to acquire frame")
	}

	for i := 0; i < len(f.regs); i++ {
		f.regs[i] = storage.StringValue(fmt.Sprintf("secret_user_data_%d", i))
	}
	for i := 0; i < len(f.slots); i++ {
		f.slots[i] = storage.StringValue(fmt.Sprintf("slot_data_%d", i))
	}
	f.vars["secret_token"] = storage.StringValue("token_xyz_12345")
	f.evalCtx["client_session"] = "admin_super_user"
	f.rows = []map[string]any{{"col1": "val1"}, {"col2": "val2"}}
	f.values = [][]any{{"row1_val1", "row1_val2"}}
	f.transientTables["#temp"] = []map[string]any{{"temp_col": 123}}
	f.ctx = context.Background()

	// Capture capacity before release
	regsCap := cap(f.regs)
	slotsCap := cap(f.slots)

	// 2. Release frame back to pool
	exec.releaseFrame(f)

	// 3. Verify ALL backing arrays are strictly zeroed out up to cap ([RULE:ZeroAllocation])
	regsFull := f.regs[:regsCap]
	for i, v := range regsFull {
		if v != (storage.Value{}) {
			t.Fatalf("f.regs[%d] not zeroed out at cap boundary: %v", i, v)
		}
	}

	slotsFull := f.slots[:slotsCap]
	for i, v := range slotsFull {
		if v != (storage.Value{}) {
			t.Fatalf("f.slots[%d] not zeroed out at cap boundary: %v", i, v)
		}
	}

	if len(f.vars) != 0 {
		t.Fatalf("expected f.vars to be empty, got %d entries", len(f.vars))
	}
	if len(f.evalCtx) != 0 {
		t.Fatalf("expected f.evalCtx to be empty, got %d entries", len(f.evalCtx))
	}
	if f.rows != nil {
		t.Fatalf("expected f.rows to be nil, got %v", f.rows)
	}
	if f.values != nil {
		t.Fatalf("expected f.values to be nil, got %v", f.values)
	}
	if len(f.transientTables) != 0 {
		t.Fatalf("expected f.transientTables to be empty, got %d entries", len(f.transientTables))
	}
	if f.ctx != nil {
		t.Fatalf("expected f.ctx to be nil, got %v", f.ctx)
	}
}

func TestVMPool_Regression_P0_29_ConcurrentPoolRecycle(t *testing.T) {
	exec := NewExecutor(nil)
	prog := &proc.Program{NumRegisters: 16, SlotCount: 8}

	const numWorkers = 16
	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				f := exec.newFrame(prog, nil)
				f.vars["w_id"] = storage.IntValue(int64(workerID))
				f.regs[0] = storage.StringValue(fmt.Sprintf("worker_%d_iter_%d", workerID, i))
				exec.releaseFrame(f)
			}
		}(w)
	}

	wg.Wait()
}
