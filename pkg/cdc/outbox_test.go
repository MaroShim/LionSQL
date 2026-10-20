package cdc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/pkg/cdc"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestOutboxDispatcher_GeneralCDCToMemorySink(t *testing.T) {
	streamer := cdc.NewStreamer(100)
	defer streamer.Close()

	cfg := cdc.DefaultDispatcherConfig()
	cfg.TopicPrefix = "events."

	dispatcher := cdc.NewOutboxDispatcher(streamer, cfg)
	sink := cdc.NewMemorySink("test_mem_sink")
	dispatcher.RegisterSink(sink)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start outbox dispatcher: %v", err)
	}
	defer dispatcher.Stop()

	// Commit WAL records via streamer
	rec1 := memengine.WALRecord{
		LSN:       101,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{
				Type:  memengine.OpInsert,
				Table: "orders",
				Key:   "order_101",
				Record: storage.Record{
					"id":     storage.IntValue(101),
					"amount": storage.FloatValue(250.75),
					"status": storage.StringValue("CREATED"),
				},
			},
		},
	}
	streamer.OnWALRecord(rec1)

	// Wait for message in sink deterministically
	if !sink.WaitForMessages(1, 2*time.Second) {
		t.Fatalf("Expected 1 message in sink, got %d", sink.Count())
	}

	msgs := sink.Messages()
	if len(msgs) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(msgs))
	}

	msg := msgs[0]
	if msg.Topic != "events.orders" {
		t.Errorf("Expected topic 'events.orders', got '%s'", msg.Topic)
	}
	if msg.Key != "order_101" {
		t.Errorf("Expected key 'order_101', got '%s'", msg.Key)
	}
	if msg.LSN != 101 {
		t.Errorf("Expected LSN 101, got %d", msg.LSN)
	}
	if msg.Table != "orders" {
		t.Errorf("Expected table 'orders', got '%s'", msg.Table)
	}

	// Verify stats
	stats := dispatcher.Stats()
	if stats.DispatchedCount != 1 {
		t.Errorf("Expected DispatchedCount 1, got %d", stats.DispatchedCount)
	}
	if stats.LastAckLSN != 101 {
		t.Errorf("Expected LastAckLSN 101, got %d", stats.LastAckLSN)
	}
}

func TestOutboxDispatcher_DedicatedOutboxTable(t *testing.T) {
	streamer := cdc.NewStreamer(100)
	defer streamer.Close()

	cfg := cdc.DefaultDispatcherConfig()
	dispatcher := cdc.NewOutboxDispatcher(streamer, cfg)
	sink := cdc.NewMemorySink("outbox_mem_sink")
	dispatcher.RegisterSink(sink)

	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start outbox dispatcher: %v", err)
	}
	defer dispatcher.Stop()

	// Insert into dedicated "outbox" table
	customPayload := `{"event_type":"UserSignedUp","email":"alice@example.com"}`
	customHeaders := `{"trace_id":"xyz-987","source":"auth_service"}`

	rec := memengine.WALRecord{
		LSN:       205,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{
				Type:  memengine.OpInsert,
				Table: "outbox",
				Key:   "evt_001",
				Record: storage.Record{
					"id":      storage.StringValue("evt_001"),
					"topic":   storage.StringValue("notifications.user.signup"),
					"key":     storage.StringValue("user_42"),
					"payload": storage.StringValue(customPayload),
					"headers": storage.StringValue(customHeaders),
				},
			},
		},
	}
	streamer.OnWALRecord(rec)

	if !sink.WaitForMessages(1, 2*time.Second) {
		t.Fatalf("Expected 1 message from outbox table, got %d", sink.Count())
	}

	msg := sink.Messages()[0]
	if msg.Topic != "notifications.user.signup" {
		t.Errorf("Expected topic 'notifications.user.signup', got '%s'", msg.Topic)
	}
	if msg.Key != "user_42" {
		t.Errorf("Expected key 'user_42', got '%s'", msg.Key)
	}
	if string(msg.Payload) != customPayload {
		t.Errorf("Expected payload '%s', got '%s'", customPayload, string(msg.Payload))
	}
	if msg.Headers["trace_id"] != "xyz-987" {
		t.Errorf("Expected header trace_id 'xyz-987', got '%s'", msg.Headers["trace_id"])
	}
	if msg.Headers["source"] != "auth_service" {
		t.Errorf("Expected header source 'auth_service', got '%s'", msg.Headers["source"])
	}
}

func TestOutboxDispatcher_RetryAndExponentialBackoff(t *testing.T) {
	streamer := cdc.NewStreamer(100)
	defer streamer.Close()

	cfg := cdc.DefaultDispatcherConfig()
	cfg.MaxRetries = 3
	cfg.InitialBackoff = 10 * time.Millisecond
	cfg.MaxBackoff = 50 * time.Millisecond

	dispatcher := cdc.NewOutboxDispatcher(streamer, cfg)
	sink := cdc.NewMemorySink("retry_sink")

	// Inject 2 transient failures, succeeding on attempt 3
	sink.SetFailUntil(2, fmt.Errorf("temporary connection refused"))
	dispatcher.RegisterSink(sink)

	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start outbox dispatcher: %v", err)
	}
	defer dispatcher.Stop()

	rec := memengine.WALRecord{
		LSN:       301,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{
				Type:  memengine.OpInsert,
				Table: "payments",
				Key:   "pay_1",
				Record: storage.Record{
					"id":     storage.IntValue(1),
					"status": storage.StringValue("PAID"),
				},
			},
		},
	}
	streamer.OnWALRecord(rec)

	if !sink.WaitForMessages(1, 2*time.Second) {
		t.Fatalf("Expected message to eventually succeed after retries, got %d", sink.Count())
	}

	stats := dispatcher.Stats()
	if stats.RetriedCount < 2 {
		t.Errorf("Expected at least 2 retries, got %d", stats.RetriedCount)
	}
	if stats.DispatchedCount != 1 {
		t.Errorf("Expected DispatchedCount 1, got %d", stats.DispatchedCount)
	}
}

func TestOutboxDispatcher_CheckpointPersistenceAndResume(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_outbox_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	streamer := cdc.NewStreamer(100)
	defer streamer.Close()

	cfg := cdc.DefaultDispatcherConfig()
	cfg.DataDir = tmpDir

	dispatcher1 := cdc.NewOutboxDispatcher(streamer, cfg)
	sink1 := cdc.NewMemorySink("sink1")
	dispatcher1.RegisterSink(sink1)

	if err := dispatcher1.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start dispatcher1: %v", err)
	}

	// Commit events 1 and 2
	streamer.OnWALRecord(memengine.WALRecord{
		LSN:       10,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{Type: memengine.OpInsert, Table: "items", Key: "item_1", Record: storage.Record{"id": storage.IntValue(1)}},
		},
	})
	streamer.OnWALRecord(memengine.WALRecord{
		LSN:       20,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{Type: memengine.OpInsert, Table: "items", Key: "item_2", Record: storage.Record{"id": storage.IntValue(2)}},
		},
	})

	if !sink1.WaitForMessages(2, 2*time.Second) {
		t.Fatalf("Expected 2 messages in sink1, got %d", sink1.Count())
	}
	dispatcher1.Stop()

	// Verify checkpoint file exists
	checkpointFile := filepath.Join(tmpDir, "outbox_checkpoint.json")
	if _, err := os.Stat(checkpointFile); err != nil {
		t.Fatalf("Expected checkpoint file to exist at %s: %v", checkpointFile, err)
	}

	// Now start a second dispatcher instance reading the checkpoint
	dispatcher2 := cdc.NewOutboxDispatcher(streamer, cfg)
	if dispatcher2.LastAckLSN() != 20 {
		t.Fatalf("Expected dispatcher2 to load LastAckLSN 20, got %d", dispatcher2.LastAckLSN())
	}

	sink2 := cdc.NewMemorySink("sink2")
	dispatcher2.RegisterSink(sink2)

	if err := dispatcher2.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start dispatcher2: %v", err)
	}
	defer dispatcher2.Stop()

	// Streamer publishes event 3 (LSN 30)
	streamer.OnWALRecord(memengine.WALRecord{
		LSN:       30,
		Timestamp: time.Now().UnixNano(),
		Ops: []memengine.WALOp{
			{Type: memengine.OpInsert, Table: "items", Key: "item_3", Record: storage.Record{"id": storage.IntValue(3)}},
		},
	})

	if !sink2.WaitForMessages(1, 2*time.Second) {
		t.Fatalf("Expected 1 new message in sink2, got %d", sink2.Count())
	}

	// sink2 should only receive event 3 (LSN 30), because LSN 20 was already acknowledged
	msg := sink2.Messages()[0]
	if msg.LSN != 30 {
		t.Errorf("Expected msg LSN 30, got %d", msg.LSN)
	}
}

func TestWebhookSink(t *testing.T) {
	var received atomic.Int64
	var lastReqHeader http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastReqHeader = r.Header.Clone()
		var msg cdc.OutboxMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := cdc.NewWebhookSink(cdc.WebhookConfig{
		TargetURL: server.URL,
		AuthToken: "secret_token_123",
		Headers: map[string]string{
			"X-Custom-Env": "production",
		},
	})
	if err != nil {
		t.Fatalf("Failed to create webhook sink: %v", err)
	}
	defer sink.Close()

	msg := &cdc.OutboxMessage{
		ID:        "msg_1",
		LSN:       50,
		Topic:     "users.created",
		Payload:   []byte(`{"user":"bob"}`),
		Timestamp: time.Now().UnixNano(),
	}

	if err := sink.Publish(context.Background(), msg); err != nil {
		t.Fatalf("Webhook publish failed: %v", err)
	}

	if received.Load() != 1 {
		t.Errorf("Expected 1 webhook call, got %d", received.Load())
	}
	if auth := lastReqHeader.Get("Authorization"); auth != "Bearer secret_token_123" {
		t.Errorf("Expected auth header 'Bearer secret_token_123', got '%s'", auth)
	}
	if env := lastReqHeader.Get("X-Custom-Env"); env != "production" {
		t.Errorf("Expected custom header 'production', got '%s'", env)
	}
}

func TestKafkaRESTSink(t *testing.T) {
	var requestedTopic string
	var reqContentType string
	var reqBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedTopic = r.URL.Path
		reqContentType = r.Header.Get("Content-Type")
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		reqBody = buf[:n]
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"offsets":[{"partition":0,"offset":1,"error_code":null}]}`))
	}))
	defer server.Close()

	sink, err := cdc.NewKafkaRESTSink(cdc.KafkaRESTConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("Failed to create Kafka REST sink: %v", err)
	}
	defer sink.Close()

	msg := &cdc.OutboxMessage{
		ID:      "k_1",
		LSN:     99,
		Topic:   "telemetry_events",
		Key:     "sensor_5",
		Payload: []byte(`{"temperature":23.5}`),
	}

	if err := sink.Publish(context.Background(), msg); err != nil {
		t.Fatalf("Kafka REST publish failed: %v", err)
	}

	if requestedTopic != "/topics/telemetry_events" {
		t.Errorf("Expected topic path '/topics/telemetry_events', got '%s'", requestedTopic)
	}
	if reqContentType != "application/vnd.kafka.json.v2+json" {
		t.Errorf("Expected Kafka Content-Type, got '%s'", reqContentType)
	}
	if len(reqBody) == 0 {
		t.Errorf("Expected non-empty Kafka request body")
	}
}

func TestRabbitMQHTTPSink(t *testing.T) {
	var requestedPath string
	var authHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.EscapedPath()
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"routed":true}`))
	}))
	defer server.Close()

	sink, err := cdc.NewRabbitMQHTTPSink(cdc.RabbitMQHTTPConfig{
		Endpoint: server.URL,
		VHost:    "/",
		Exchange: "amq.topic",
		Username: "admin",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("Failed to create RabbitMQ HTTP sink: %v", err)
	}
	defer sink.Close()

	msg := &cdc.OutboxMessage{
		ID:      "r_1",
		LSN:     150,
		Topic:   "orders.placed",
		Key:     "region_kr",
		Payload: []byte(`{"order_id":123}`),
	}

	if err := sink.Publish(context.Background(), msg); err != nil {
		t.Fatalf("RabbitMQ HTTP publish failed: %v", err)
	}

	expectedPath := "/api/exchanges/%2f/amq.topic/publish"
	if requestedPath != expectedPath {
		t.Errorf("Expected path '%s', got '%s'", expectedPath, requestedPath)
	}
	if authHeader == "" {
		t.Errorf("Expected non-empty Basic Auth header")
	}
}
