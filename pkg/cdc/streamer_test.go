package cdc

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestCDC_RecordToMap(t *testing.T) {
	rec := storage.Record{
		"id":    storage.IntValue(100),
		"name":  storage.StringValue("Alice"),
		"score": storage.FloatValue(95.5),
		"valid": storage.BoolValue(true),
		"null":  storage.NullValue(storage.TypeVarChar),
	}

	m := RecordToMap(rec)
	if m["id"] != int64(100) {
		t.Fatalf("expected id=100, got %v", m["id"])
	}
	if m["name"] != "Alice" {
		t.Fatalf("expected name=Alice, got %v", m["name"])
	}
	if m["score"] != 95.5 {
		t.Fatalf("expected score=95.5, got %v", m["score"])
	}
	if m["valid"] != true {
		t.Fatalf("expected valid=true, got %v", m["valid"])
	}
	if m["null"] != nil {
		t.Fatalf("expected null=nil, got %v", m["null"])
	}
}

func TestCDC_Streamer_SubscribeAndBroadcast(t *testing.T) {
	streamer := NewStreamer(100)
	defer streamer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, unsub, err := streamer.Subscribe(ctx, 0, "users", 10)
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	defer unsub()

	// Ingest WALRecord with 1 users table op and 1 orders table op
	walRec := memengine.WALRecord{
		LSN:       1,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{
				Type:   memengine.OpInsert,
				Table:  "users",
				Key:    "1",
				Record: storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("Bob")},
			},
			{
				Type:   memengine.OpInsert,
				Table:  "orders",
				Key:    "101",
				Record: storage.Record{"id": storage.IntValue(101)},
			},
		},
	}

	streamer.OnWALRecord(walRec)

	select {
	case ev := <-ch:
		if ev.Table != "users" {
			t.Fatalf("expected table users, got %s", ev.Table)
		}
		if ev.Op != OpInsert {
			t.Fatalf("expected op INSERT, got %s", ev.Op)
		}
		if ev.Data["name"] != "Bob" {
			t.Fatalf("expected name Bob, got %v", ev.Data["name"])
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for CDC event")
	}

	// Orders event should NOT be received because filter was "users"
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event received: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// Expected
	}
}

func TestCDC_Streamer_HistoricalReplay(t *testing.T) {
	streamer := NewStreamer(100)
	defer streamer.Close()

	// Ingest 3 WAL records before subscriber connects
	for i := int64(1); i <= 3; i++ {
		streamer.OnWALRecord(memengine.WALRecord{
			LSN:       i,
			Timestamp: time.Now().UnixNano(),
			Ops: []memengine.WALOp{
				{
					Type:   memengine.OpInsert,
					Table:  "items",
					Key:    "k",
					Record: storage.Record{"val": storage.IntValue(i)},
				},
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Subscribe from LSN 1 (should receive LSN 2 and 3)
	ch, unsub, err := streamer.Subscribe(ctx, 1, "", 10)
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	defer unsub()

	ev1 := <-ch
	if ev1.LSN != 2 {
		t.Fatalf("expected LSN 2, got %d", ev1.LSN)
	}

	ev2 := <-ch
	if ev2.LSN != 3 {
		t.Fatalf("expected LSN 3, got %d", ev2.LSN)
	}
}

// TestCDC_Streamer_Regression_P0_10_HistoricalReplayBufferOverflowNoDrop verifies that
// when historical events exceed the subscriber's channel buffer capacity, events are NEVER silently
// dropped, guaranteeing gapless historical replay (P0-10).
func TestCDC_Streamer_Regression_P0_10_HistoricalReplayBufferOverflowNoDrop(t *testing.T) {
	const historyEvents = 50
	const subscriberBufSize = 10 // Intentionally much smaller than history events to trigger overflow

	streamer := NewStreamer(100)
	defer streamer.Close()

	// 1. Commit 50 sequential events into the streamer's ring buffer
	for i := int64(1); i <= historyEvents; i++ {
		streamer.OnWALRecord(memengine.WALRecord{
			LSN:       i,
			Timestamp: time.Now().UnixNano(),
			Ops: []memengine.WALOp{
				{
					Type:   memengine.OpInsert,
					Table:  "orders",
					Key:    "ord",
					Record: storage.Record{"id": storage.IntValue(i)},
				},
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 2. Subscribe starting from LSN 1 with a small buffer size (10)
	// (Should replay all events with LSN > 1, i.e. 49 events)
	ch, unsub, err := streamer.Subscribe(ctx, 1, "", subscriberBufSize)
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	defer unsub()

	// 3. Consume all 49 events concurrently as they stream out
	const expectedReplayCount = historyEvents - 1
	received := make([]int64, 0, expectedReplayCount)
	for i := 0; i < expectedReplayCount; i++ {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed prematurely after %d events", len(received))
			}
			received = append(received, ev.LSN)
		case <-ctx.Done():
			t.Fatalf("[CRITICAL P0-10 REGRESSION] Timed out waiting for historical events. Received only %d/%d (events %d~%d were silently dropped!)",
				len(received), expectedReplayCount, len(received)+2, historyEvents)
		}
	}

	// 4. Verify all 49 events were delivered sequentially with zero gap
	for i := 0; i < expectedReplayCount; i++ {
		expectedLSN := int64(i + 2)
		if received[i] != expectedLSN {
			t.Errorf("expected received[%d] == LSN %d, got %d", i, expectedLSN, received[i])
		}
	}

	// 5. Test fromLSN = 0 replays all 50 events without gaps
	chAll, unsubAll, err := streamer.Subscribe(ctx, 0, "", subscriberBufSize)
	if err != nil {
		t.Fatalf("failed to subscribe from LSN 0: %v", err)
	}
	defer unsubAll()

	receivedAll := make([]int64, 0, historyEvents)
	for i := 0; i < historyEvents; i++ {
		select {
		case ev, ok := <-chAll:
			if !ok {
				t.Fatalf("channel closed prematurely for LSN 0 replay after %d events", len(receivedAll))
			}
			receivedAll = append(receivedAll, ev.LSN)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for LSN 0 full replay, got %d/%d", len(receivedAll), historyEvents)
		}
	}

	for i := 0; i < historyEvents; i++ {
		expectedLSN := int64(i + 1)
		if receivedAll[i] != expectedLSN {
			t.Errorf("expected receivedAll[%d] == LSN %d, got %d", i, expectedLSN, receivedAll[i])
		}
	}
}

func TestCDC_Streamer_ServeHTTP_NDJSON(t *testing.T) {
	streamer := NewStreamer(100)
	defer streamer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/cdc/stream?table=products&format=ndjson", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		streamer.ServeHTTP(w, req)
	}()

	// Wait briefly for subscriber registration
	time.Sleep(50 * time.Millisecond)

	streamer.OnWALRecord(memengine.WALRecord{
		LSN:       10,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{
				Type:   memengine.OpInsert,
				Table:  "products",
				Key:    "p1",
				Record: storage.Record{"sku": storage.StringValue("SKU-001")},
			},
		},
	})

	time.Sleep(50 * time.Millisecond)
	cancel() // Close HTTP request
	<-done

	body := w.Body.String()
	reader := bufio.NewReader(strings.NewReader(body))
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read ndjson line: %v", err)
	}

	var ev ChangeEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v, raw: %s", err, line)
	}

	if ev.LSN != 10 {
		t.Fatalf("expected LSN 10, got %d", ev.LSN)
	}
	if ev.Table != "products" {
		t.Fatalf("expected table products, got %s", ev.Table)
	}
	if ev.Data["sku"] != "SKU-001" {
		t.Fatalf("expected sku SKU-001, got %v", ev.Data["sku"])
	}
}
