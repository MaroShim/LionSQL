package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lionsql/pkg/api"
	"lionsql/pkg/catalog"
	"lionsql/pkg/cdc"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestOutboxAPI_StatusEndpoint(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)

	server := api.NewServer(cat, eng)

	// 1. Without outbox configured -> 503 Service Unavailable
	req := httptest.NewRequest(http.MethodGet, "/api/cdc/outbox/status", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Expected 503 when outbox is unconfigured, got %d", rec.Code)
	}

	// 2. Attach Streamer and Outbox Dispatcher
	streamer := cdc.NewStreamer(100)
	defer streamer.Close()

	cfg := cdc.DefaultDispatcherConfig()
	dispatcher := cdc.NewOutboxDispatcher(streamer, cfg)
	memSink := cdc.NewMemorySink("api_test_sink")
	dispatcher.RegisterSink(memSink)

	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop()

	server.SetCDCStreamer(streamer)
	server.SetOutboxDispatcher(dispatcher)

	// Ingest a test WAL record
	streamer.OnWALRecord(memengine.WALRecord{
		LSN:       777,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{
				Type:  memengine.OpInsert,
				Table: "customers",
				Key:   "cust_1",
				Record: storage.Record{
					"id":   storage.IntValue(1),
					"name": storage.StringValue("Carol"),
				},
			},
		},
	})

	if !memSink.WaitForMessages(1, 2*time.Second) {
		t.Fatalf("Expected 1 message dispatched to memory sink")
	}

	// 3. Query status endpoint -> 200 OK with metrics
	req2 := httptest.NewRequest(http.MethodGet, "/api/cdc/outbox/status", nil)
	rec2 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from outbox status, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var stats cdc.DispatcherStats
	if err := json.Unmarshal(rec2.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to parse status JSON: %v", err)
	}

	if !stats.Active {
		t.Errorf("Expected Active=true, got %v", stats.Active)
	}
	if stats.SinkCount != 1 {
		t.Errorf("Expected SinkCount=1, got %d", stats.SinkCount)
	}
	if stats.DispatchedCount != 1 {
		t.Errorf("Expected DispatchedCount=1, got %d", stats.DispatchedCount)
	}
	if stats.LastAckLSN != 777 {
		t.Errorf("Expected LastAckLSN=777, got %d", stats.LastAckLSN)
	}
}
