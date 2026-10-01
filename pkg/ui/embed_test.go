package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardMenuOrderAndContent(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}

	html := string(data)

	// Ensure "PocketBase Style Single Binary" was removed
	if strings.Contains(html, "PocketBase Style Single Binary") {
		t.Errorf("HTML should not contain 'PocketBase Style Single Binary'")
	}

	// Verify menu items exist in the specified order
	expectedMenuItems := []string{
		`data-view="tables"`,
		`Table & Record`,
		`data-view="procedures"`,
		`Stored Procedures`,
		`data-view="webhooks"`,
		`Web Hooks`,
		`data-view="crons"`,
		`Cron Jobs`,
		`data-view="query"`,
		`SQL Query Studio`,
		`data-view="apikeys"`,
		`Auth & API Keys`,
		`data-view="sdk"`,
		`Client SDK`,
		`data-view="mail"`,
		`Settings / Mail`,
	}

	lastIdx := 0
	for _, item := range expectedMenuItems {
		idx := strings.Index(html[lastIdx:], item)
		if idx == -1 {
			t.Fatalf("menu item %q not found after index %d in dashboard HTML", item, lastIdx)
		}
		lastIdx += idx + len(item)
	}
}

func TestHandler(t *testing.T) {
	handler := Handler()
	req := httptest.NewRequest(http.MethodGet, "/dashboard.html", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	if !bytes.Contains(rr.Body.Bytes(), []byte("SQLion")) {
		t.Errorf("expected response to contain SQLion brand")
	}
}
