package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestSDK_StaticAssetServing(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := NewServer(cat, eng)
	defer server.Crons().Stop()

	// 1. Verify GET /sdk/sqlion.js
	req := httptest.NewRequest("GET", "/sdk/sqlion.js", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /sdk/sqlion.js expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/javascript") {
		t.Fatalf("expected application/javascript content type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "class SQLion") {
		t.Fatalf("expected JS body to contain class SQLion")
	}

	// 2. Verify GET /sdk/sqlion.d.ts
	req = httptest.NewRequest("GET", "/sdk/sqlion.d.ts", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /sdk/sqlion.d.ts expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "export default class SQLion") {
		t.Fatalf("expected DTS body to contain export default class SQLion")
	}
}
