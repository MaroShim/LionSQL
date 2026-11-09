package api

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

// TestP1_BatchM5_WebhookCooperativeAbort tests Scope-127 [RULE:CooperativeAbort]:
// When WebhookDispatcher's context is cancelled during retry backoff, the retry loop
// must terminate immediately rather than continuing through remaining attempts.
func TestP1_BatchM5_WebhookCooperativeAbort(t *testing.T) {
	d := NewWebhookDispatcher()
	defer d.Close()

	var attempts atomic.Int32

	d.SetTransport(mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		att := attempts.Add(1)
		if att == 1 {
			// On first failure, close dispatcher (which cancels d.ctx) immediately
			_ = d.Close()
		}
		return nil, fmt.Errorf("simulated network failure")
	}))

	wh := &Webhook{
		Name:    "Cancellation Test",
		Table:   "users",
		Events:  []string{"insert"},
		URL:     "http://mock-service.local/webhook",
		Enabled: true,
	}
	_ = d.Register(wh)

	// Dispatch synchronously via deliverWithRetry
	del := d.deliverWithRetry(wh, map[string]any{"id": 1}, "insert", "users")

	if del.Success {
		t.Fatalf("expected delivery to fail, got success")
	}

	// Because context was cancelled immediately on attempt 1,
	// the loop should have cooperatively aborted after attempt 1, making exactly 1 request.
	if total := attempts.Load(); total != 1 {
		t.Fatalf("Scope-127 violated: expected exactly 1 attempt before cooperative abort, but got %d attempts", total)
	}
}
