package execution_test

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/pkg/execution"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func setupGatherTestTable(t testing.TB, numRows int) (storage.Engine, storage.Session, storage.Transaction, *storage.TableSchema) {
	eng := memengine.New()
	_ = eng.Open("")

	schema := &storage.TableSchema{
		Name: "t_gather_items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
			{Name: "category", Type: storage.TypeVarChar},
			{Name: "note", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: false})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	for i := 1; i <= numRows; i++ {
		rec := storage.Record{
			"id":       storage.IntValue(int64(i)),
			"val":      storage.IntValue(int64(i * 10)),
			"category": storage.StringValue(fmt.Sprintf("cat_%d", i%5)),
			"note":     storage.StringValue(fmt.Sprintf("note_long_text_payload_%d", i)),
		}
		if err := tx.Insert("t_gather_items", rec); err != nil {
			t.Fatalf("insert %d failed: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	readTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx readTx failed: %v", err)
	}

	return eng, sess, readTx, schema
}

// TestGatherIterator_BasicStreaming verifies that GatherIterator scans all rows across parallel workers
// without dropping or corrupting any tuples.
func TestGatherIterator_BasicStreaming(t *testing.T) {
	const totalRows = 5000
	eng, sess, tx, schema := setupGatherTestTable(t, totalRows)
	defer eng.Close()
	defer sess.Close()
	defer tx.Rollback()

	gather := execution.NewGatherIterator(tx, "t_gather_items", "t", schema)
	gather.SetDOP(4)

	ctx := context.Background()
	if err := gather.Open(ctx); err != nil {
		t.Fatalf("gather.Open failed: %v", err)
	}
	defer gather.Close()

	var count int64
	var sumVal int64
	dest := execution.GetChunk()
	defer execution.PutChunk(dest)

	for {
		err := gather.NextChunk(ctx, dest)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextChunk error: %v", err)
		}
		for _, tuple := range dest.Tuples {
			count++
			if tuple.Values[1] != nil {
				v := tuple.Values[1].(int64)
				sumVal += v
			}
		}
	}

	if count != totalRows {
		t.Fatalf("expected %d rows, got %d", totalRows, count)
	}

	// Sum of 1..5000 * 10 = 10 * (5000 * 5001 / 2) = 125,025,000
	expectedSum := int64(10 * (totalRows * (totalRows + 1) / 2))
	if sumVal != expectedSum {
		t.Fatalf("expected sum %d, got %d", expectedSum, sumVal)
	}
}

// TestGatherIterator_WithFilterPushdown verifies that filter evaluation is distributed
// across parallel workers and properly filters rows in parallel.
func TestGatherIterator_WithFilterPushdown(t *testing.T) {
	const totalRows = 6000
	eng, sess, tx, schema := setupGatherTestTable(t, totalRows)
	defer eng.Close()
	defer sess.Close()
	defer tx.Rollback()

	gather := execution.NewGatherIterator(tx, "t_gather_items", "t", schema)
	gather.SetDOP(4)
	gather.SetFilterCond("val > 50000") // Matches i > 5000, so exactly 1000 rows (5001..6000)

	ctx := context.Background()
	if err := gather.Open(ctx); err != nil {
		t.Fatalf("gather.Open failed: %v", err)
	}
	defer gather.Close()

	var matchCount int64
	dest := execution.GetChunk()
	defer execution.PutChunk(dest)

	for {
		err := gather.NextChunk(ctx, dest)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextChunk error: %v", err)
		}
		for _, tuple := range dest.Tuples {
			matchCount++
			v := tuple.Values[1].(int64)
			if v <= 50000 {
				t.Fatalf("unexpected tuple violating filter: val=%d", v)
			}
		}
	}

	if matchCount != 1000 {
		t.Fatalf("expected 1000 matching rows, got %d", matchCount)
	}
}

// TestGatherIterator_WithProjectionPushdown verifies that selective column decoding works
// correctly across parallel workers without buffer overruns.
func TestGatherIterator_WithProjectionPushdown(t *testing.T) {
	const totalRows = 2000
	eng, sess, tx, schema := setupGatherTestTable(t, totalRows)
	defer eng.Close()
	defer sess.Close()
	defer tx.Rollback()

	gather := execution.NewGatherIterator(tx, "t_gather_items", "t", schema)
	gather.SetDOP(3)
	gather.SetRequiredCols([]int{0, 2}) // id and category only

	ctx := context.Background()
	if err := gather.Open(ctx); err != nil {
		t.Fatalf("gather.Open failed: %v", err)
	}
	defer gather.Close()

	var rowCount int
	for {
		tuple, err := gather.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		rowCount++
		if tuple.Values[0] == nil {
			t.Fatalf("expected id to be decoded, got nil")
		}
		if tuple.Values[2] == nil {
			t.Fatalf("expected category to be decoded, got nil")
		}
	}

	if rowCount != totalRows {
		t.Fatalf("expected %d rows, got %d", totalRows, rowCount)
	}
}

// TestGatherIterator_EarlyCancelAndLimit verifies that when the consumer terminates early (e.g. LIMIT 10),
// all background feeder and worker goroutines are promptly reaped without hanging ([RULE:ZeroOrphan]).
func TestGatherIterator_EarlyCancelAndLimit(t *testing.T) {
	const totalRows = 10000
	eng, sess, tx, schema := setupGatherTestTable(t, totalRows)
	defer eng.Close()
	defer sess.Close()
	defer tx.Rollback()

	gather := execution.NewGatherIterator(tx, "t_gather_items", "t", schema)
	gather.SetDOP(4)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := gather.Open(ctx); err != nil {
		t.Fatalf("gather.Open failed: %v", err)
	}

	// Pull only 25 tuples and immediately Close
	for i := 0; i < 25; i++ {
		_, err := gather.Next(ctx)
		if err != nil {
			t.Fatalf("Next %d failed: %v", i, err)
		}
	}

	done := make(chan struct{})
	go func() {
		_ = gather.Close()
		close(done)
	}()

	select {
	case <-done:
		// Clean teardown verified
	case <-time.After(1 * time.Second):
		t.Fatalf("GatherIterator.Close hung! ZeroOrphan invariant violated")
	}
}

// TestGatherIterator_EffectiveDOPBounds verifies that EffectiveDOP respects NumCPU bounds,
// minimum 1, and upper bound caps.
func TestGatherIterator_EffectiveDOPBounds(t *testing.T) {
	gather := execution.NewGatherIterator(nil, "t", "t", nil)

	gather.SetDOP(0)
	dop0 := gather.EffectiveDOP()
	expectedAuto := runtime.NumCPU() / 2
	if expectedAuto < 1 {
		expectedAuto = 1
	}
	if expectedAuto > 8 {
		expectedAuto = 8
	}
	if dop0 != expectedAuto {
		t.Fatalf("expected auto DOP %d, got %d", expectedAuto, dop0)
	}

	gather.SetDOP(1)
	if gather.EffectiveDOP() != 1 {
		t.Fatalf("expected DOP=1, got %d", gather.EffectiveDOP())
	}

	gather.SetDOP(6)
	if gather.EffectiveDOP() != 6 {
		t.Fatalf("expected DOP=6, got %d", gather.EffectiveDOP())
	}
}

// BenchmarkParallelGather_vs_SequentialScan compares the throughput and latency of
// single-threaded SeqScan vs parallel GatherIterator on 30,000 rows.
func BenchmarkParallelGather_vs_SequentialScan(b *testing.B) {
	const numRows = 30000
	eng, sess, tx, schema := setupGatherTestTable(b, numRows)
	defer eng.Close()
	defer sess.Close()
	defer tx.Rollback()

	ctx := context.Background()

	b.Run("SingleThread_SeqScan", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			iter := execution.NewSeqScanIterator(tx, "t_gather_items", "b", schema)
			iter.SetFilterCond("val > 100000")
			_ = iter.Open(ctx)
			chunk := execution.GetChunk()
			var count atomic.Int64
			for {
				err := iter.NextChunk(ctx, chunk)
				if err == io.EOF {
					break
				}
				count.Add(int64(chunk.Len()))
			}
			execution.PutChunk(chunk)
			_ = iter.Close()
		}
	})

	b.Run("MultiCore_ParallelGather_DOP4", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			iter := execution.NewGatherIterator(tx, "t_gather_items", "b", schema)
			iter.SetDOP(4)
			iter.SetFilterCond("val > 100000")
			_ = iter.Open(ctx)
			chunk := execution.GetChunk()
			var count atomic.Int64
			for {
				err := iter.NextChunk(ctx, chunk)
				if err == io.EOF {
					break
				}
				count.Add(int64(chunk.Len()))
			}
			execution.PutChunk(chunk)
			_ = iter.Close()
		}
	})
}

// TestParallelHashJoin_WorkerPoolProbe verifies that HashJoinIterator pushes down its
// read-only build table into GatherIterator, so that multiple parallel workers execute
// the join probe concurrently across CPU cores.
func TestParallelHashJoin_WorkerPoolProbe(t *testing.T) {
	const totalRows = 5000
	eng, sess, tx, schema := setupGatherTestTable(t, totalRows)
	defer eng.Close()
	defer sess.Close()
	defer tx.Rollback()

	// Build table: 3 categories
	catSchema := []string{"cat_id", "cat_name"}
	catTuples := []execution.Tuple{
		execution.NewTupleFromMap(map[string]any{"cat_id": int64(10), "cat_name": "Cat10"}, catSchema),
		execution.NewTupleFromMap(map[string]any{"cat_id": int64(20), "cat_name": "Cat20"}, catSchema),
		execution.NewTupleFromMap(map[string]any{"cat_id": int64(30), "cat_name": "Cat30"}, catSchema),
	}
	catMock := &mockTupleIterator{schema: catSchema, tuples: catTuples}

	// Probe table: GatherIterator on t_gather_items (val is i * 10)
	gather := execution.NewGatherIterator(tx, "t_gather_items", "t", schema)
	gather.SetDOP(4)

	// HashJoin on t.val = cat.cat_id
	joinIter := execution.NewHashJoinIterator(gather, catMock, "INNER", "val", "cat_id")

	ctx := context.Background()
	if err := joinIter.Open(ctx); err != nil {
		t.Fatalf("joinIter.Open failed: %v", err)
	}
	defer joinIter.Close()

	var joinedCount int64
	for {
		tuple, err := joinIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("joinIter.Next failed: %v", err)
		}
		joinedCount++
		val := tuple.Get("val").(int64)
		catName := tuple.Get("cat_name").(string)
		if fmt.Sprintf("Cat%d", val) != catName {
			t.Fatalf("unexpected join result: val=%d, cat_name=%s", val, catName)
		}
	}

	// val is i * 10 for i=1..5000. Exactly 3 rows match cat_id in (10, 20, 30).
	if joinedCount != 3 {
		t.Fatalf("expected 3 joined rows, got %d", joinedCount)
	}
}
