package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func TestWebhook_CRUDOperations(t *testing.T) {
	d := NewWebhookDispatcher()
	defer d.Close()

	// 1. Register
	wh := &Webhook{
		Name:    "Order Created Alert",
		Table:   "orders",
		Events:  []string{"create", "update"},
		URL:     "http://mock-service.local/webhook",
		Secret:  "super-secret-key",
		Headers: map[string]string{"X-Test-Header": "hello"},
		Enabled: true,
	}
	if err := d.Register(wh); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if wh.ID == "" {
		t.Fatalf("expected generated webhook ID")
	}

	// 2. Get
	fetched, ok := d.Get(wh.ID)
	if !ok || fetched == nil {
		t.Fatalf("Get webhook failed")
	}
	if fetched.Name != "Order Created Alert" || fetched.Table != "orders" {
		t.Fatalf("unexpected fetched webhook: %+v", fetched)
	}

	// 3. List
	list := d.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(list))
	}

	// 4. Update
	err := d.Update(wh.ID, &Webhook{
		Name:    "Renamed Alert",
		Table:   "*",
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	updated, _ := d.Get(wh.ID)
	if updated.Name != "Renamed Alert" || updated.Table != "*" || updated.Enabled != false {
		t.Fatalf("unexpected updated webhook: %+v", updated)
	}

	// 5. Delete
	if !d.Delete(wh.ID) {
		t.Fatalf("Delete failed")
	}
	if _, ok := d.Get(wh.ID); ok {
		t.Fatalf("expected deleted webhook to not exist")
	}
}

func TestWebhook_DispatchDeliveryAndHMAC(t *testing.T) {
	var receivedBody []byte
	var receivedHeader http.Header
	var mu sync.Mutex
	delivered := make(chan struct{}, 1)

	secretKey := "test-hmac-secret-123"

	d := NewWebhookDispatcher()
	defer d.Close()

	// In-memory hermetic transport without external TCP dialing
	d.SetTransport(mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()

		body, _ := io.ReadAll(req.Body)
		receivedBody = body
		receivedHeader = req.Header.Clone()

		select {
		case delivered <- struct{}{}:
		default:
		}

		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			Header:     make(http.Header),
			Request:    req,
		}
		return resp, nil
	}))

	wh := &Webhook{
		Name:    "Customer Event Webhook",
		Table:   "customers",
		Events:  []string{"create"},
		URL:     "http://mock-service.local/webhook",
		Secret:  secretKey,
		Headers: map[string]string{"X-Custom-Env": "testing"},
		Enabled: true,
	}
	if err := d.Register(wh); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Dispatch an event
	event := RealtimeEvent{
		Action: ActionCreate,
		Table:  "customers",
		ID:     101,
		Record: map[string]any{"id": 101, "name": "Alice Corp", "balance": 1500},
		Time:   time.Now().UnixMilli(),
	}
	d.Dispatch(event)

	// Wait for delivery
	select {
	case <-delivered:
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for webhook delivery")
	}

	mu.Lock()
	defer mu.Unlock()

	// 1. Verify Headers
	if receivedHeader.Get("X-Custom-Env") != "testing" {
		t.Errorf("expected X-Custom-Env header 'testing', got %q", receivedHeader.Get("X-Custom-Env"))
	}
	if receivedHeader.Get("X-SQLion-Event") != "create" {
		t.Errorf("expected X-SQLion-Event 'create', got %q", receivedHeader.Get("X-SQLion-Event"))
	}

	// 2. Verify HMAC Signature
	sigHeader := receivedHeader.Get("X-SQLion-Signature")
	if sigHeader == "" {
		t.Fatalf("expected X-SQLion-Signature header")
	}
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write(receivedBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sigHeader != expectedSig {
		t.Errorf("HMAC mismatch: got %q, expected %q", sigHeader, expectedSig)
	}

	// 3. Verify JSON Payload content
	var payload map[string]any
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("invalid payload json: %v", err)
	}
	if payload["event"] != "create" || payload["table"] != "customers" {
		t.Errorf("unexpected payload content: %+v", payload)
	}

	// 4. Verify Delivery Log
	var dels []*WebhookDelivery
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		dels = d.Deliveries(10)
		if len(dels) == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(dels) != 1 {
		t.Fatalf("expected 1 delivery record, got %d", len(dels))
	}
	if !dels[0].Success || dels[0].StatusCode != 200 {
		t.Errorf("delivery record indicates failure: %+v", dels[0])
	}
}

func TestWebhook_RetryOnFailure(t *testing.T) {
	var attempts int32
	delivered := make(chan struct{}, 1)

	d := NewWebhookDispatcher()
	d.retryBackoff = 5 * time.Millisecond // fast backoff for hermetic test
	defer d.Close()

	d.SetTransport(mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		att := atomic.AddInt32(&attempts, 1)
		if att < 3 {
			// Fail first 2 attempts with 503
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(bytes.NewBufferString(`{"error":"temporarily unavailable"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}

		// Succeed on 3rd attempt
		select {
		case delivered <- struct{}{}:
		default:
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"status":"delivered"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))

	wh := &Webhook{
		Name:    "Retry Target",
		Table:   "*",
		Events:  []string{"*"},
		URL:     "http://mock-service.local/retry",
		Enabled: true,
	}
	_ = d.Register(wh)

	d.Dispatch(RealtimeEvent{
		Action: ActionUpdate,
		Table:  "orders",
		ID:     5,
		Time:   time.Now().UnixMilli(),
	})

	select {
	case <-delivered:
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for retried delivery")
	}

	finalAttempts := atomic.LoadInt32(&attempts)
	if finalAttempts != 3 {
		t.Errorf("expected 3 attempts before success, got %d", finalAttempts)
	}

	var dels []*WebhookDelivery
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		dels = d.Deliveries(1)
		if len(dels) > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if len(dels) == 0 || !dels[0].Success || dels[0].AttemptCount != 3 {
		t.Errorf("delivery record expected attempt_count=3 and success=true, got %+v", dels)
	}
}

func TestWebhook_HTTPManagementEndpoints(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)

	server := NewServer(cat, eng)
	defer eng.Close()
	defer server.Webhooks().Close()

	// Mock in-memory transport for test ping
	server.Webhooks().SetTransport(mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"status":"pong"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))

	// 1. POST /api/webhooks
	newWH := map[string]any{
		"name":    "Order Pipeline Hook",
		"table":   "orders",
		"events":  []string{"create", "delete"},
		"url":     "http://mock-service.local/pipeline",
		"secret":  "whsec_test",
		"enabled": true,
	}
	bodyBytes, _ := json.Marshal(newWH)
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	var created Webhook
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	if created.ID == "" || created.Name != "Order Pipeline Hook" {
		t.Fatalf("unexpected created webhook: %+v", created)
	}

	// 2. GET /api/webhooks
	req = httptest.NewRequest(http.MethodGet, "/api/webhooks", nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/webhooks failed: %d", rec.Code)
	}
	var listResult struct {
		Items []*Webhook `json:"items"`
		Total int        `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listResult)

	if listResult.Total != 1 || len(listResult.Items) != 1 {
		t.Fatalf("expected 1 webhook in list, got %d", listResult.Total)
	}

	// 3. POST /api/webhooks/{id}/test (Test Ping)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/webhooks/%s/test", created.ID), nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for test ping, got %d: %s", rec.Code, rec.Body.String())
	}
	var delResult WebhookDelivery
	_ = json.Unmarshal(rec.Body.Bytes(), &delResult)

	if delResult.WebhookID != created.ID || delResult.Event != "test" || !delResult.Success {
		t.Errorf("unexpected test delivery result: %+v", delResult)
	}

	// 4. GET /api/webhooks/deliveries
	req = httptest.NewRequest(http.MethodGet, "/api/webhooks/deliveries", nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/webhooks/deliveries failed: %d", rec.Code)
	}
	var delList struct {
		Items []*WebhookDelivery `json:"items"`
		Total int                `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &delList)

	if delList.Total == 0 {
		t.Fatalf("expected at least 1 delivery logged")
	}

	// 5. DELETE /api/webhooks/{id}
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/webhooks/%s", created.ID), nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on delete, got %d", rec.Code)
	}

	// 6. DELETE /api/webhooks/deliveries (Clear)
	req = httptest.NewRequest(http.MethodDelete, "/api/webhooks/deliveries", nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on clear deliveries, got %d", rec.Code)
	}
}

func TestWebhook_EndToEndRecordMutationTrigger(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)

	server := NewServer(cat, eng)
	defer eng.Close()
	defer server.Webhooks().Close()

	var receivedAction string
	var receivedTable string
	var receivedRecord map[string]any
	var mu sync.Mutex
	delivered := make(chan struct{}, 1)

	server.Webhooks().SetTransport(mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()

		var payload map[string]any
		_ = json.NewDecoder(req.Body).Decode(&payload)

		receivedAction, _ = payload["event"].(string)
		receivedTable, _ = payload["table"].(string)
		receivedRecord, _ = payload["record"].(map[string]any)

		select {
		case delivered <- struct{}{}:
		default:
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"received":true}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))

	// Register webhook for table "orders" on "create"
	wh := &Webhook{
		Name:    "Orders Discord Notifier",
		Table:   "orders",
		Events:  []string{"create"},
		URL:     "http://mock-service.local/discord-webhook",
		Enabled: true,
	}
	_ = server.Webhooks().Register(wh)

	// Create table via SQL
	_, err := server.ExecuteSQL("CREATE TABLE orders (id INT PRIMARY KEY, amount FLOAT, customer VARCHAR(50));")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// Trigger via POST /api/records/orders
	bodyBytes := []byte(`{"id": 999, "amount": 199.99, "customer": "Eve"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/records/orders", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from records API, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify webhook receives the event
	select {
	case <-delivered:
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for webhook dispatch from record creation")
	}

	mu.Lock()
	defer mu.Unlock()

	if receivedAction != "create" || receivedTable != "orders" {
		t.Errorf("unexpected action or table: %s / %s", receivedAction, receivedTable)
	}
	if receivedRecord == nil || receivedRecord["customer"] != "Eve" {
		t.Errorf("expected customer 'Eve' in record payload, got %+v", receivedRecord)
	}
}
