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

	// Verify menu items and category sections exist in the specified order
	expectedMenuItems := []string{
		`Database &amp; Query`,
		`data-view="overview"`,
		`Overview`,
		`data-view="tables"`,
		`Table &amp; Record`,
		`data-view="query"`,
		`SQL Query`,
		`data-view="procedures"`,
		`Stored Procedures`,

		`Automation &amp; Stream`,
		`data-view="realtime"`,
		`Realtime Live Stream`,
		`data-view="webhooks"`,
		`Web Hooks`,
		`data-view="crons"`,
		`Cron Jobs`,

		`Developer &amp; Access`,
		`data-view="apikeys"`,
		`Auth &amp; API Keys`,
		`data-view="sdk"`,
		`Client SDK`,

		`Monitoring &amp; System`,
		`data-view="logs"`,
		`Request Logs`,
		`data-view="replication"`,
		`Replication (HA)`,
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
		`--pk-val-color`,
		`.cell-pk-val`,
		`--pk-tag-bg`,
		`--col-tag-bg`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q", snippet)
		}
	}
}

func TestLightModeHighContrastStyles(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`--alert-danger-text`,
		`--alert-danger-strong`,
		`--btn-danger-bg`,
		`--btn-danger-color`,
		`.btn-danger-subtle`,
		`.alert-danger-box`,
		`.badge-success`,
		`.badge-danger`,
		`.badge-warning`,
		`.badge-info`,
		`.badge-purple`,
		`.badge-muted`,
		`class="alert-danger-box"`,
		`var(--alert-danger-strong)`,
		`var(--pk-val-color)`,
		`--tab-inactive-bg`,
		`--tab-inactive-color`,
		`.tab-btn`,
		`.nav-section-title`,
		`.nav-section-title::after`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain contrast snippet %q", snippet)
		}
	}
}

func TestAddColumnModal_UI_DDL_1(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`id="btn-open-add-col"`,
		`openAddColumnModal()`,
		`id="add-column-modal"`,
		`id="add-col-name"`,
		`id="add-col-type"`,
		`id="add-col-nullable"`,
		`id="add-col-default"`,
		`id="btn-submit-add-col"`,
		`handleAddColumnSubmit(event)`,
		`ALTER TABLE`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q for UI-DDL-1", snippet)
		}
	}
}

func TestRealtimeInspector_UI_REALTIME_1(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`data-view="realtime"`,
		`id="view-realtime"`,
		`id="realtime-status-pill"`,
		`id="btn-realtime-pause"`,
		`id="realtime-filter-table"`,
		`id="realtime-filter-action"`,
		`id="realtime-search-input"`,
		`id="realtime-feed-container"`,
		`handleIncomingRealtimeEvent`,
		`renderRealtimeFeed()`,
		`toggleRealtimeStreamPause()`,
		`copyEventPayload`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q for UI-REALTIME-1", snippet)
		}
	}
}

func TestBulkExportAndFKJump_UI_DATA_1(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`id="btn-batch-delete"`,
		`id="btn-batch-export-csv"`,
		`id="btn-batch-export-json"`,
		`exportSelectedRecords('csv')`,
		`exportSelectedRecords('json')`,
		`id="fk-relation-modal"`,
		`id="btn-fk-jump-table"`,
		`openFKRelationModal`,
		`closeFKRelationModal`,
		`jumpToFKParentTable`,
		`currentLoadedRecords`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q for UI-DATA-1", snippet)
		}
	}
}

func TestRequestLogsViewer_UI_LOGS_1(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`data-view="logs"`,
		`id="view-logs"`,
		`id="logs-count-badge"`,
		`id="logs-live-indicator"`,
		`id="btn-logs-pause"`,
		`id="btn-logs-clear"`,
		`id="logs-filter-query"`,
		`id="logs-filter-method"`,
		`id="logs-filter-status"`,
		`id="logs-filter-limit"`,
		`id="logs-table-body"`,
		`loadRequestLogs`,
		`clearRequestLogs`,
		`toggleLogsPause`,
		`/api/logs/requests`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q for UI-LOGS-1", snippet)
		}
	}
}
func TestDashboardViewSeparationAndStructure(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	// Ensure view-sdk is closed before view-replication begins
	sdkIdx := strings.Index(html, `id="view-sdk"`)
	repIdx := strings.Index(html, `id="view-replication"`)
	realtimeIdx := strings.Index(html, `id="view-realtime"`)
	logsIdx := strings.Index(html, `id="view-logs"`)

	if sdkIdx == -1 || repIdx == -1 || realtimeIdx == -1 || logsIdx == -1 {
		t.Fatalf("missing one or more view containers in dashboard HTML")
	}

	if !(sdkIdx < repIdx && repIdx < realtimeIdx && realtimeIdx < logsIdx) {
		t.Fatalf("views order is unexpected: sdk=%d rep=%d realtime=%d logs=%d", sdkIdx, repIdx, realtimeIdx, logsIdx)
	}

	// Between view-sdk and view-replication there must be a closing </div> for view-sdk
	snippetBetween := html[sdkIdx:repIdx]
	openDivs := strings.Count(snippetBetween, "<div")
	closeDivs := strings.Count(snippetBetween, "</div>")
	if openDivs != closeDivs {
		t.Errorf("unbalanced <div> tags in view-sdk section: %d open vs %d close (view-replication would be nested)", openDivs, closeDivs)
	}
}

func TestOverviewMonitoringElements(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Fatalf("failed to read index HTML: %v", err)
	}
	html := string(data)

	requiredSnippets := []string{
		`data-view="overview"`,
		`id="view-overview"`,
		`id="overview-live-dot"`,
		`id="overview-refresh-interval"`,
		`id="overview-engine"`,
		`id="overview-sessions"`,
		`id="overview-tables"`,
		`id="overview-uptime"`,
		`id="overview-tps"`,
		`id="overview-total-tx"`,
		`loadOverviewMetrics()`,
		`changeOverviewInterval(`,
		`startOverviewPolling()`,
		`/api/overview`,
		`copyToClipboard(`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(html, snippet) {
			t.Errorf("expected dashboard HTML to contain snippet %q", snippet)
		}
	}
}

