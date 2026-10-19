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
		`data-view="replication"`,
		`Replication (HA)`,
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

	if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("expected Cache-Control header to contain 'no-cache', got %q", cc)
	}

	if !bytes.Contains(rr.Body.Bytes(), []byte("LionSQL")) {
		t.Errorf("expected response to contain LionSQL brand")
	}
}

func TestReplicationMonitoringElements(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`id="view-replication"`,
		`id="ha-role-badge"`,
		`id="ha-current-lsn"`,
		`id="ha-access-mode"`,
		`id="ha-primary-replicas-card"`,
		`id="ha-replicas-tbody"`,
		`id="ha-replica-action-card"`,
		`id="ha-standalone-card"`,
		`id="promote-replica-modal"`,
		`openPromoteModal()`,
		`closePromoteModal()`,
		`executePromote()`,
		`loadReplicationStatus(`,
		`/api/replication/promote`,
		`/api/replication/status`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q", snippet)
		}
	}
}

func TestDropTableConfirmationModal(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`id="drop-table-modal"`,
		`id="drop-table-target-name"`,
		`id="drop-table-prompt-name"`,
		`id="drop-table-confirm-input"`,
		`id="btn-confirm-drop-table"`,
		`closeDropTableModal()`,
		`onDropTableInputChange(`,
		`executeDropTable()`,
		`pendingDropTableName`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q", snippet)
		}
	}
}

func TestSeedSampleDataUI(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`id="btn-seed-sample"`,
		`seedSampleData()`,
		`tablesList.length > 0`,
		`Sample data can only be seeded on a clean, empty database`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q", snippet)
		}
	}
}

func TestAPIPreviewEnhancements(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`id="api-preview-modal"`,
		`id="api-lang-sdk"`,
		`LionSQL SDK`,
		`getDummyRecordForTable`,
		`getPrimaryKeySample`,
		`getFilterSample`,
		`db.collection`,
		`openAPIPreviewModal()`,
		`closeAPIPreviewModal()`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q", snippet)
		}
	}
}

func TestThemeToggleUI(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`data-theme="light"`,
		`id="theme-toggle-btn"`,
		`id="theme-toggle-icon"`,
		`id="theme-toggle-label"`,
		`toggleTheme()`,
		`initTheme()`,
		`applyTheme(`,
		`lionsql_theme`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q", snippet)
		}
	}
}




