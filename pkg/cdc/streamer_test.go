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
