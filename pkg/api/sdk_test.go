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

	// 1. Verify GET /sdk/lionsql.js
	req := httptest.NewRequest("GET", "/sdk/lionsql.js", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /sdk/lionsql.js expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/javascript") {
		t.Fatalf("expected application/javascript content type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "class LionSQL") {
		t.Fatalf("expected JS body to contain class LionSQL")
	}

	// 2. Verify GET /sdk/lionsql.d.ts
	req = httptest.NewRequest("GET", "/sdk/lionsql.d.ts", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /sdk/lionsql.d.ts expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "export default class LionSQL") {
		t.Fatalf("expected DTS body to contain export default class LionSQL")
	}
}
