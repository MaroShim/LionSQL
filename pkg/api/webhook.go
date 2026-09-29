package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	MaxWebhookDeliveries  = 200
	DefaultWebhookTimeout = 5 * time.Second
	MaxWebhookRetries     = 3
	WebhookQueueCapacity  = 1024
	WebhookWorkerCount    = 4
)

// Webhook defines a target endpoint and filtering criteria for database mutation notifications.
type Webhook struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Table     string            `json:"table"`            // Specific table name or "*" for all tables
	Events    []string          `json:"events"`           // "create", "update", "delete", "import", "create_table", "drop_table", or "*"
	URL       string            `json:"url"`              // Target HTTP/HTTPS URL
	Secret    string            `json:"secret,omitempty"` // Secret key for HMAC-SHA256 signature
	Headers   map[string]string `json:"headers,omitempty"`// Custom HTTP request headers
	Enabled   bool              `json:"enabled"`          // Active toggle
	CreatedAt int64             `json:"created_at"`
	UpdatedAt int64             `json:"updated_at"`
}

// WebhookDelivery records the status and metadata of an individual HTTP notification attempt.
type WebhookDelivery struct {
	ID           string         `json:"id"`
	WebhookID    string         `json:"webhook_id"`
	WebhookName  string         `json:"webhook_name"`
	Event        string         `json:"event"`
	Table        string         `json:"table"`
	URL          string         `json:"url"`
	Payload      map[string]any `json:"payload"`
	StatusCode   int            `json:"status_code"`
	Success      bool           `json:"success"`
	DurationMs   int64          `json:"duration_ms"`
	Error        string         `json:"error,omitempty"`
	AttemptCount int            `json:"attempt_count"`
	Timestamp    int64          `json:"timestamp"`
}

// WebhookDispatcher manages background workers, queueing, retries, and delivery logging.
type WebhookDispatcher struct {
	mu           sync.RWMutex
	webhooks     map[string]*Webhook
	deliveries   []*WebhookDelivery
	queue        chan RealtimeEvent
	client       *http.Client
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	closed       bool
	brokerSub    chan RealtimeEvent
	broker       *RealtimeBroker
	retryBackoff time.Duration
}

// NewWebhookDispatcher creates a new thread-safe Webhook dispatch engine.
func NewWebhookDispatcher() *WebhookDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &WebhookDispatcher{
		webhooks:     make(map[string]*Webhook),
		deliveries:   make([]*WebhookDelivery, 0, MaxWebhookDeliveries),
		queue:        make(chan RealtimeEvent, WebhookQueueCapacity),
		client:       &http.Client{Timeout: DefaultWebhookTimeout},
		ctx:          ctx,
		cancel:       cancel,
		retryBackoff: 100 * time.Millisecond,
	}

	for i := 0; i < WebhookWorkerCount; i++ {
		d.wg.Add(1)
		go d.workerLoop()
	}

	return d
}

// SetTransport configures a custom HTTP transport (e.g. for hermetic testing without TCP).
func (d *WebhookDispatcher) SetTransport(tr http.RoundTripper) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.client.Transport = tr
}

// SetClient overrides the HTTP client instance.
func (d *WebhookDispatcher) SetClient(client *http.Client) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if client != nil {
		d.client = client
	}
}

// StartBrokerListener subscribes the dispatcher to the RealtimeBroker for zero-config mutation streaming.
func (d *WebhookDispatcher) StartBrokerListener(broker *RealtimeBroker) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if broker == nil || d.closed {
		return
	}
	d.broker = broker
	d.brokerSub = broker.Subscribe()

	d.wg.Add(1)
	go func(ch chan RealtimeEvent) {
		defer d.wg.Done()
		for {
			select {
			case <-d.ctx.Done():
				return
			case event, ok := <-ch:
				if !ok {
					return
				}
				d.Dispatch(event)
			}
		}
	}(d.brokerSub)
}

// Close gracefully terminates all background workers and network connections.
func (d *WebhookDispatcher) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.cancel()
	d.mu.Unlock()

	if d.broker != nil && d.brokerSub != nil {
		d.broker.Unsubscribe(d.brokerSub)
	}

	d.wg.Wait()
	return nil
}

// Register adds a new webhook destination.
func (d *WebhookDispatcher) Register(wh *Webhook) error {
	if wh == nil {
		return errors.New("webhook definition cannot be nil")
	}
	if strings.TrimSpace(wh.URL) == "" {
		return errors.New("webhook url is required")
	}
	if !strings.HasPrefix(wh.URL, "http://") && !strings.HasPrefix(wh.URL, "https://") {
		return errors.New("webhook url must start with http:// or https://")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if wh.ID == "" {
		wh.ID = generateWebhookID()
	}
	if wh.Name == "" {
		wh.Name = wh.ID
	}
	if wh.Table == "" {
		wh.Table = "*"
	}
	if len(wh.Events) == 0 {
		wh.Events = []string{"*"}
	}
	now := time.Now().UnixMilli()
	wh.CreatedAt = now
	wh.UpdatedAt = now

	d.webhooks[wh.ID] = wh
	return nil
}

// Update updates an existing webhook definition.
func (d *WebhookDispatcher) Update(id string, updated *Webhook) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	existing, exists := d.webhooks[id]
	if !exists {
		return fmt.Errorf("webhook with id %q not found", id)
	}

	if updated.Name != "" {
		existing.Name = updated.Name
	}
	if updated.Table != "" {
		existing.Table = updated.Table
	}
	if len(updated.Events) > 0 {
		existing.Events = updated.Events
	}
	if updated.URL != "" {
		if !strings.HasPrefix(updated.URL, "http://") && !strings.HasPrefix(updated.URL, "https://") {
			return errors.New("webhook url must start with http:// or https://")
		}
		existing.URL = updated.URL
	}
	if updated.Secret != "" {
		existing.Secret = updated.Secret
	}
	if updated.Headers != nil {
		existing.Headers = updated.Headers
	}
	existing.Enabled = updated.Enabled
	existing.UpdatedAt = time.Now().UnixMilli()

	return nil
}

// Delete removes a webhook destination.
func (d *WebhookDispatcher) Delete(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.webhooks[id]; exists {
		delete(d.webhooks, id)
		return true
	}
	return false
}

// Get retrieves a webhook by ID.
func (d *WebhookDispatcher) Get(id string) (*Webhook, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	wh, ok := d.webhooks[id]
	if !ok {
		return nil, false
	}
	cpy := *wh
	return &cpy, true
}

// List returns all registered webhooks.
func (d *WebhookDispatcher) List() []*Webhook {
	d.mu.RLock()
	defer d.mu.RUnlock()

	res := make([]*Webhook, 0, len(d.webhooks))
	for _, wh := range d.webhooks {
		cpy := *wh
		res = append(res, &cpy)
	}
	return res
}

// Dispatch queues an event for asynchronous delivery across matching webhooks.
func (d *WebhookDispatcher) Dispatch(event RealtimeEvent) {
	d.mu.RLock()
	isClosed := d.closed
	d.mu.RUnlock()

	if isClosed {
		return
	}

	select {
	case d.queue <- event:
	default:
		// Queue full; drop under extreme backpressure to maintain primary engine stability
	}
}

// workerLoop drains mutation events and dispatches HTTP requests.
func (d *WebhookDispatcher) workerLoop() {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		case event, ok := <-d.queue:
			if !ok {
				return
			}
			d.processEvent(event)
		}
	}
}

// processEvent matches the event against enabled webhooks and sends HTTP notifications.
func (d *WebhookDispatcher) processEvent(event RealtimeEvent) {
	targets := d.matchWebhooks(event.Table, string(event.Action))
	if len(targets) == 0 {
		return
	}

	payload := map[string]any{
		"event":     string(event.Action),
		"table":     event.Table,
		"timestamp": event.Time,
	}
	if event.ID != nil {
		payload["id"] = event.ID
	}
	if event.Record != nil {
		payload["record"] = event.Record
	}

	for _, wh := range targets {
		d.deliverWithRetry(wh, payload, string(event.Action), event.Table)
	}
}

// matchWebhooks returns webhooks matching the target table and event action.
func (d *WebhookDispatcher) matchWebhooks(table, action string) []*Webhook {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var matched []*Webhook
	for _, wh := range d.webhooks {
		if !wh.Enabled {
			continue
		}
		if wh.Table != "*" && !strings.EqualFold(wh.Table, table) {
			continue
		}
		eventMatched := false
		for _, e := range wh.Events {
			if e == "*" || strings.EqualFold(e, action) {
				eventMatched = true
				break
			}
		}
		if eventMatched {
			cpy := *wh
			matched = append(matched, &cpy)
		}
	}
	return matched
}

// deliverWithRetry sends the HTTP request with up to MaxWebhookRetries attempts.
func (d *WebhookDispatcher) deliverWithRetry(wh *Webhook, payload map[string]any, eventName, tableName string) *WebhookDelivery {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil
	}

	delivery := &WebhookDelivery{
		ID:          generateDeliveryID(),
		WebhookID:   wh.ID,
		WebhookName: wh.Name,
		Event:       eventName,
		Table:       tableName,
		URL:         wh.URL,
		Payload:     payload,
		Timestamp:   time.Now().UnixMilli(),
	}

	var lastErr error
	var statusCode int
	var success bool
	start := time.Now()

	for attempt := 1; attempt <= MaxWebhookRetries; attempt++ {
		delivery.AttemptCount = attempt

		reqCtx, reqCancel := context.WithTimeout(d.ctx, DefaultWebhookTimeout)
		req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, wh.URL, bytes.NewReader(bodyBytes))
		if reqErr != nil {
			reqCancel()
			lastErr = reqErr
			break
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "SQLion-Webhook/1.0")
		req.Header.Set("X-SQLion-Event", eventName)
		req.Header.Set("X-SQLion-Delivery", delivery.ID)

		if wh.Secret != "" {
			mac := hmac.New(sha256.New, []byte(wh.Secret))
			mac.Write(bodyBytes)
			signature := hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-SQLion-Signature", "sha256="+signature)
		}

		for k, v := range wh.Headers {
			req.Header.Set(k, v)
		}

		resp, respErr := d.client.Do(req)
		reqCancel()

		if respErr == nil {
			statusCode = resp.StatusCode
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			if statusCode >= 200 && statusCode < 300 {
				success = true
				lastErr = nil
				break
			}
			lastErr = fmt.Errorf("server responded with status %d", statusCode)
		} else {
			lastErr = respErr
		}

		if attempt < MaxWebhookRetries {
			select {
			case <-d.ctx.Done():
				break
			case <-time.After(d.retryBackoff * time.Duration(attempt)):
			}
		}
	}

	delivery.DurationMs = time.Since(start).Milliseconds()
	delivery.StatusCode = statusCode
	delivery.Success = success
	if lastErr != nil {
		delivery.Error = lastErr.Error()
	}

	d.recordDelivery(delivery)
	return delivery
}

// recordDelivery appends to the circular delivery log buffer.
func (d *WebhookDispatcher) recordDelivery(del *WebhookDelivery) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.deliveries) >= MaxWebhookDeliveries {
		// Drop oldest entry
		d.deliveries = d.deliveries[1:]
	}
	d.deliveries = append(d.deliveries, del)
}

// Deliveries returns recent delivery attempts, newest first.
func (d *WebhookDispatcher) Deliveries(limit int) []*WebhookDelivery {
	d.mu.RLock()
	defer d.mu.RUnlock()

	count := len(d.deliveries)
	if count == 0 {
		return []*WebhookDelivery{}
	}

	if limit <= 0 || limit > count {
		limit = count
	}

	res := make([]*WebhookDelivery, limit)
	for i := 0; i < limit; i++ {
		res[i] = d.deliveries[count-1-i]
	}
	return res
}

// ClearDeliveries purges all delivery history.
func (d *WebhookDispatcher) ClearDeliveries() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deliveries = d.deliveries[:0]
}

// TestPing sends an immediate synthetic test notification for a registered webhook.
func (d *WebhookDispatcher) TestPing(id string) (*WebhookDelivery, error) {
	d.mu.RLock()
	wh, exists := d.webhooks[id]
	if !exists {
		d.mu.RUnlock()
		return nil, fmt.Errorf("webhook with id %q not found", id)
	}
	cpy := *wh
	d.mu.RUnlock()

	testPayload := map[string]any{
		"event":     "test",
		"table":     cpy.Table,
		"message":   "SQLion test ping notification",
		"timestamp": time.Now().UnixMilli(),
	}

	del := d.deliverWithRetry(&cpy, testPayload, "test", cpy.Table)
	return del, nil
}

func generateWebhookID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("whk_%s", hex.EncodeToString(b))
}

func generateDeliveryID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("del_%s", hex.EncodeToString(b))
}
