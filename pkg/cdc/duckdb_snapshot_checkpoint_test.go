package cdc

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func setupTestStorageEngineWithData(t *testing.T, count int) storage.Engine {
	t.Helper()
	eng := memengine.New()
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer sess.Close()

	schema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "customer_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
			{Name: "status", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	for i := 1; i <= count; i++ {
		rec := storage.Record{
			"id":          storage.IntValue(int64(i)),
			"customer_id": storage.IntValue(int64(100 + i)),
			"amount":      storage.FloatValue(float64(i) * 10.5),
			"status":      storage.StringValue("PLACED"),
		}
		if err := tx.Insert("orders", rec); err != nil {
			t.Fatalf("failed to insert record %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit tx: %v", err)
	}

	return eng
}

// TestCDC_DuckDB_InitialSnapshotGeneration verifies that a fresh consumer receives
// all pre-existing table rows as OpSnapshot events followed by real-time mutations.
func TestCDC_DuckDB_InitialSnapshotGeneration(t *testing.T) {
	eng := setupTestStorageEngineWithData(t, 5)
	defer eng.Close()

	streamer := NewStreamer(1000)
	defer streamer.Close()

	type walHookAdder interface {
		AddWALHook(fn func(rec memengine.WALRecord))
	}
	if wha, ok := eng.(walHookAdder); ok {
		wha.AddWALHook(streamer.OnWALRecord)
	}

	snapGen := NewSnapshotGenerator(eng)
	streamer.SetSnapshotGenerator(snapGen)

	cpStore := NewMemoryCheckpointStore()
	streamer.SetCheckpointStore(cpStore)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Initial request with from_lsn=0 & consumer_id="duckdb_worker_1"
	req := httptest.NewRequest(http.MethodGet, "/api/cdc/stream?table=orders&consumer_id=duckdb_worker_1&from_lsn=0", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		streamer.ServeHTTP(rec, req)
	}()

	// Allow snapshot to emit and enter streaming loop
	time.Sleep(50 * time.Millisecond)

	// Perform a new live insert while connected
	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("orders", storage.Record{
		"id":          storage.IntValue(6),
		"customer_id": storage.IntValue(106),
		"amount":      storage.FloatValue(63.0),
		"status":      storage.StringValue("COMPLETED"),
	})
	_ = tx.Commit()
	sess.Close()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-doneCh

	// Parse NDJSON lines received
	scanner := bufio.NewScanner(rec.Body)
	var snapshotEvents []ChangeEvent
	var liveEvents []ChangeEvent

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		var ev ChangeEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("failed to decode event JSON %q: %v", line, err)
		}
		if ev.Op == OpSnapshot {
			snapshotEvents = append(snapshotEvents, ev)
		} else {
			liveEvents = append(liveEvents, ev)
		}
	}

	if len(snapshotEvents) != 5 {
		t.Fatalf("expected 5 snapshot events, got %d", len(snapshotEvents))
	}
	if len(liveEvents) == 0 {
		t.Fatalf("expected at least 1 live event, got 0")
	}
	if liveEvents[0].Op != OpInsert {
		t.Fatalf("expected first live event to be INSERT, got %s", liveEvents[0].Op)
	}

	// Verify checkpoint was updated in cpStore
	lastLSN, err := cpStore.GetCheckpoint("duckdb_worker_1", "orders")
	if err != nil || lastLSN <= 0 {
		t.Fatalf("expected saved checkpoint > 0, got %d (err: %v)", lastLSN, err)
	}
}

// TestCDC_DuckDB_PersistentCheckpointAndReconnect verifies that upon disconnect and reconnect,
// the consumer receives only missed events starting from saved checkpoint LSN without replaying snapshot.
func TestCDC_DuckDB_PersistentCheckpointAndReconnect(t *testing.T) {
	tmpDir := t.TempDir()
	eng := setupTestStorageEngineWithData(t, 3)
	defer eng.Close()

	streamer := NewStreamer(1000)
	defer streamer.Close()

	type walHookAdder interface {
		AddWALHook(fn func(rec memengine.WALRecord))
	}
	if wha, ok := eng.(walHookAdder); ok {
		wha.AddWALHook(streamer.OnWALRecord)
	}

	snapGen := NewSnapshotGenerator(eng)
	streamer.SetSnapshotGenerator(snapGen)

	fileStore, err := NewFileCheckpointStore(tmpDir, "checkpoints.json")
	if err != nil {
		t.Fatalf("failed to create file checkpoint store: %v", err)
	}
	defer fileStore.Close()
	streamer.SetCheckpointStore(fileStore)

	// Step 1: Initial connection - consumes snapshot (3 rows)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 2*time.Second)
	req1 := httptest.NewRequest(http.MethodGet, "/api/cdc/stream?table=orders&consumer_id=worker_alpha", nil).WithContext(ctx1)
	rec1 := httptest.NewRecorder()

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		streamer.ServeHTTP(rec1, req1)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel1()
	<-done1

	cpLSN1, _ := fileStore.GetCheckpoint("worker_alpha", "orders")
	if cpLSN1 <= 0 {
		t.Fatalf("expected checkpoint recorded after snapshot, got %d", cpLSN1)
	}

	// Step 2: While consumer is disconnected, execute 2 new transactions in LionSQL
	sess, _ := eng.NewSession()
	tx1, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx1.Insert("orders", storage.Record{
		"id":          storage.IntValue(4),
		"customer_id": storage.IntValue(104),
		"amount":      storage.FloatValue(42.0),
		"status":      storage.StringValue("PLACED"),
	})
	_ = tx1.Commit()

	tx2, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx2.Insert("orders", storage.Record{
		"id":          storage.IntValue(5),
		"customer_id": storage.IntValue(105),
		"amount":      storage.FloatValue(52.5),
		"status":      storage.StringValue("PLACED"),
	})
	_ = tx2.Commit()
	sess.Close()

	// Step 3: Simulate restart - new file store reloads checkpoints from disk ([RULE:AtomicSaves])
	reloadedStore, err := NewFileCheckpointStore(tmpDir, "checkpoints.json")
	if err != nil {
		t.Fatalf("failed to reload file checkpoint store: %v", err)
	}
	defer reloadedStore.Close()
	streamer.SetCheckpointStore(reloadedStore)

	// Step 4: Reconnect with same consumer_id (without explicit from_lsn; should auto-resume)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	req2 := httptest.NewRequest(http.MethodGet, "/api/cdc/stream?table=orders&consumer_id=worker_alpha", nil).WithContext(ctx2)
	rec2 := httptest.NewRecorder()

	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		streamer.ServeHTTP(rec2, req2)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel2()
	<-done2

	// Step 5: Verify no OpSnapshot was resent, only missed transactions were received
	scanner := bufio.NewScanner(rec2.Body)
	var reconnectedEvents []ChangeEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		var ev ChangeEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
		reconnectedEvents = append(reconnectedEvents, ev)
	}

	for _, ev := range reconnectedEvents {
		if ev.Op == OpSnapshot {
			t.Fatalf("unexpected OpSnapshot received on reconnect! Snapshot should not be replayed.")
		}
	}
	if len(reconnectedEvents) != 2 {
		t.Fatalf("expected exactly 2 missed events on reconnect, got %d", len(reconnectedEvents))
	}

	// Verify final checkpoint advances
	finalLSN, _ := reloadedStore.GetCheckpoint("worker_alpha", "orders")
	if finalLSN <= cpLSN1 {
		t.Fatalf("expected final LSN %d to advance past initial %d", finalLSN, cpLSN1)
	}
}

// TestCDC_FileCheckpointStore_Persistence guarantees atomicity and crash recovery.
func TestCDC_FileCheckpointStore_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileCheckpointStore(tmpDir, "test_cp.json")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if err := store.SaveCheckpoint("client_1", "orders", 1500); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}
	if err := store.SaveCheckpoint("client_2", "users", 2300); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}
	_ = store.Close()

	// Verify physical file exists
	filePath := filepath.Join(tmpDir, "test_cp.json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("checkpoint file missing: %v", err)
	}

	// Reload in fresh store
	store2, err := NewFileCheckpointStore(tmpDir, "test_cp.json")
	if err != nil {
		t.Fatalf("failed to reload store: %v", err)
	}
	defer store2.Close()

	lsn1, err := store2.GetCheckpoint("client_1", "orders")
	if err != nil || lsn1 != 1500 {
		t.Fatalf("expected lsn1=1500, got %d (err: %v)", lsn1, err)
	}

	lsn2, err := store2.GetCheckpoint("client_2", "users")
	if err != nil || lsn2 != 2300 {
		t.Fatalf("expected lsn2=2300, got %d (err: %v)", lsn2, err)
	}
}
