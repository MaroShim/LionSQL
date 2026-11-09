package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
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

	// 3. Verify GET /sdk and /sdk/ redirect to /sdk/lionsql.js
	for _, path := range []string{"/sdk", "/sdk/"} {
		req = httptest.NewRequest("GET", path, nil)
		w = httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusFound {
			t.Fatalf("GET %s expected 302 Found, got %d", path, w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "/sdk/lionsql.js" {
			t.Fatalf("GET %s expected redirect to /sdk/lionsql.js, got %q", path, loc)
		}
	}
}
