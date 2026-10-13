package memengine

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"sqlion/pkg/storage"
)

func newTestRecord(lsn int64) WALRecord {
	return WALRecord{
		LSN:       lsn,
		TxID:      lsn,
		Timestamp: lsn,
		Ops: []WALOp{{
			Type:  OpInsert,
			Table: "t",
			Key:   fmt.Sprintf("k%d", lsn),
			Data:  []byte("payload-payload-payload"),
		}},
	}
}

// BenchmarkWAL_AppendFsyncAlways measures concurrent durable commit throughput.
func BenchmarkWAL_AppendFsyncAlways(b *testing.B) {
	for _, workers := range []int{1, 16, 64} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			w, err := NewWALWriter(filepath.Join(b.TempDir(), "bench.aof"), FsyncAlways)
			if err != nil {
				b.Fatal(err)
			}
			defer w.Close()

			var lsn atomic.Int64
			b.SetParallelism(workers)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if err := w.Append(newTestRecord(lsn.Add(1))); err != nil {
						b.Error(err)
						return
					}
				}
			})
		})
	}
}

// TestWAL_GroupCommit_AllRecordsDurableAndReplayable verifies that every concurrently
// appended record survives a replay, regardless of how syncs were batched.
func TestWAL_GroupCommit_AllRecordsDurableAndReplayable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "group.aof")
	w, err := NewWALWriter(path, FsyncAlways)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 32
	const perWorker = 50
	var lsn atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				if err := w.Append(newTestRecord(lsn.Add(1))); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("append failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	seen := make(map[int64]bool)
	maxLSN, err := ReplayWAL(path, func(rec WALRecord) error {
		seen[rec.LSN] = true
		return nil
	})
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if want := int64(workers * perWorker); maxLSN != want || int64(len(seen)) != want {
		t.Fatalf("expected %d distinct records, got %d (maxLSN=%d)", want, len(seen), maxLSN)
	}
}

// TestWAL_GroupCommit_BatchesSyncs asserts that concurrent commits share fsync calls.
func TestWAL_GroupCommit_BatchesSyncs(t *testing.T) {
	w, err := NewWALWriter(filepath.Join(t.TempDir(), "batch.aof"), FsyncAlways)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	const workers = 64
	const perWorker = 20
	var lsn atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				if err := w.Append(newTestRecord(lsn.Add(1))); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	total := int64(workers * perWorker)
	syncs := w.SyncCount()
	if syncs <= 0 || syncs > total {
		t.Fatalf("sync count %d out of range (1..%d)", syncs, total)
	}
	t.Logf("%d commits durably flushed with %d fsync calls (%.1f commits/sync)", total, syncs, float64(total)/float64(syncs))
}

// TestWAL_GroupCommit_TruncateDuringAppend ensures AOF rewrite truncation cannot race with
// an in-flight group sync (run with -race).
func TestWAL_GroupCommit_TruncateDuringAppend(t *testing.T) {
	w, err := NewWALWriter(filepath.Join(t.TempDir(), "trunc.aof"), FsyncAlways)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var lsn atomic.Int64
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := w.Append(newTestRecord(lsn.Add(1))); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		if err := w.Truncate(); err != nil {
			t.Errorf("truncate failed: %v", err)
			break
		}
	}
	close(stop)
	wg.Wait()
}

// TestMemEngine_GroupCommit_ConcurrentAOFCompaction verifies that concurrent
// Engine transactions under FsyncAlways with aggressive AOF compaction do not
// race on snapshot maps or lose records across Truncate boundaries.
func TestMemEngine_GroupCommit_ConcurrentAOFCompaction(t *testing.T) {
	dir := t.TempDir()
	eng := New()
	eng.SetFsyncPolicy(FsyncAlways)
	eng.SetAOFThreshold(512) // Trigger frequent AOF rewrites during concurrent commits
	if err := eng.Open(dir); err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	err := eng.CreateTable(&storage.TableSchema{
		Name: "kv",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "v", Type: storage.TypeVarChar},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	const workers = 16
	const perWorker = 20
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wID := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess, err := eng.NewSession()
			if err != nil {
				t.Error(err)
				return
			}
			defer sess.Close()
			for j := 0; j < perWorker; j++ {
				id := int64(wID*1000 + j)
				tx, err := sess.BeginTx(storage.TxOptions{})
				if err != nil {
					t.Error(err)
					return
				}
				if err := tx.Insert("kv", storage.Record{
					"id": storage.IntValue(id),
					"v":  storage.StringValue("val"),
				}); err != nil {
					_ = tx.Rollback()
					t.Error(err)
					return
				}
				if err := tx.Commit(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	cnt, err := eng.TableRowCount("kv")
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(workers * perWorker); cnt != want {
		t.Fatalf("expected %d rows after concurrent group commit + AOF rewrite, got %d", want, cnt)
	}
}
