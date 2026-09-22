package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRealtimeBroker_PubSub(t *testing.T) {
	broker := NewRealtimeBroker()
	defer broker.Close()

	if broker.SubscriberCount() != 0 {
		t.Fatalf("Expected 0 subscribers initially, got %d", broker.SubscriberCount())
	}

	ch1 := broker.Subscribe()
	ch2 := broker.Subscribe()

	if broker.SubscriberCount() != 2 {
		t.Fatalf("Expected 2 subscribers, got %d", broker.SubscriberCount())
	}

	event := RealtimeEvent{
		Action: ActionCreate,
		Table:  "accounts",
		ID:     "123",
		Record: map[string]any{"id": 123, "holder": "Eve"},
	}

	broker.Broadcast(event)

	select {
	case e := <-ch1:
		if e.Action != ActionCreate || e.Table != "accounts" || fmt.Sprint(e.ID) != "123" {
			t.Fatalf("ch1 received unexpected event: %+v", e)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("ch1 timed out waiting for event")
	}

	select {
	case e := <-ch2:
		if e.Action != ActionCreate || e.Table != "accounts" {
			t.Fatalf("ch2 received unexpected event: %+v", e)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("ch2 timed out waiting for event")
	}

	broker.Unsubscribe(ch1)
	if broker.SubscriberCount() != 1 {
		t.Fatalf("Expected 1 subscriber after unsubscribe, got %d", broker.SubscriberCount())
	}

	// Verify channel buffer overflow does not block producer
	for i := 0; i < 100; i++ {
		broker.Broadcast(RealtimeEvent{
			Action: ActionUpdate,
			Table:  "test",
			ID:     i,
		})
	}

	broker.Unsubscribe(ch2)
	if broker.SubscriberCount() != 0 {
		t.Fatalf("Expected 0 subscribers, got %d", broker.SubscriberCount())
	}
}

func TestRealtime_HTTPStream(t *testing.T) {
	srv := setupTestServer()

	// Seed tables
	seedReq := httptest.NewRequest(http.MethodPost, "/api/seed", nil)
	seedRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(seedRec, seedReq)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/realtime", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Handler().ServeHTTP(rec, req)
	}()

	// Wait briefly for SSE stream to establish and greeting to be sent
	time.Sleep(50 * time.Millisecond)

	// Trigger a mutation that broadcasts an event
	createReq := httptest.NewRequest(http.MethodPost, "/api/tables/accounts/records", strings.NewReader(`{"id":99,"holder":"TestSSE","balance":500}`))
	createRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("Create record failed: %s", createRec.Body.String())
	}

	// Wait for event to flush
	time.Sleep(50 * time.Millisecond)

	// Cancel client context to terminate SSE connection
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: connected") {
		t.Fatalf("Expected 'event: connected' in SSE stream, got:\n%s", body)
	}
	if !strings.Contains(body, "event: record_change") {
		t.Fatalf("Expected 'event: record_change' in SSE stream, got:\n%s", body)
	}
	if !strings.Contains(body, `"table":"accounts"`) || !strings.Contains(body, `"TestSSE"`) {
		t.Fatalf("Expected accounts record in SSE payload, got:\n%s", body)
	}
}
