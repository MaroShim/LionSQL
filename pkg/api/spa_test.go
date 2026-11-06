package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestIsSafeFlatAsset(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		// Allowed: root and flat file names
		{"", true},
		{"/", true},
		{"/index.html", true},
		{"index.html", true},
		{"bundle.js", true},
		{"/bundle.js", true},
		{"style.css", true},
		{"favicon.ico", true},
		{"logo.svg", true},

		// Allowed: clean SPA routes without extension
		{"/dashboard", true},
		{"/dashboard/analytics", true},
		{"/users/123", true},

		// Rejected: Subdirectory traversal for asset files
		{"/sub/app.js", false},
		{"sub/app.js", false},
		{"/a/b/c.png", false},
		{"assets\\style.css", false},
		{"../secret.txt", false},
		{"/../secret.txt", false},
		{"..\\secret.txt", false},

		// Rejected: Dot-prefixed hidden files
		{".env", false},
		{"/.env", false},
		{".git", false},
		{"/.git", false},
		{".DS_Store", false},
		{"..", false},
		{"/..", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := isSafeFlatAsset(tc.path)
			if got != tc.expected {
				t.Errorf("isSafeFlatAsset(%q) = %v, expected %v", tc.path, got, tc.expected)
			}
		})
	}
}

func TestSingleFileHandler(t *testing.T) {
	tmpDir := t.TempDir()
	htmlFile := filepath.Join(tmpDir, "index.html")
	content := `<!DOCTYPE html><html><body><h1>Hello Single App</h1></body></html>`
	if err := os.WriteFile(htmlFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp html file: %v", err)
	}

	handler, err := NewSingleFileHandler(htmlFile)
	if err != nil {
		t.Fatalf("NewSingleFileHandler failed: %v", err)
	}

	// Test GET /
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Hello Single App") {
		t.Errorf("expected body to contain 'Hello Single App', got %q", rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
		t.Errorf("expected Content-Type text/html, got %q", rr.Header().Get("Content-Type"))
	}

	// Test GET /dashboard (SPA Route)
	req2 := httptest.NewRequest("GET", "/dashboard/settings", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("expected 200 OK for SPA route, got %d", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "Hello Single App") {
		t.Errorf("expected body to contain 'Hello Single App', got %q", rr2.Body.String())
	}
}

func TestFlatDirectoryHandler(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create flat files
	indexContent := `<!DOCTYPE html><html><body><h1>Lion SPA</h1></body></html>`
	jsContent := `console.log("lion bundle");`
	cssContent := `body { background: #000; }`

	_ = os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte(indexContent), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "bundle.js"), []byte(jsContent), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "style.css"), []byte(cssContent), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("SECRET=123"), 0644)

	// Create a subdirectory to test traversal rejection
	subDir := filepath.Join(tmpDir, "sub")
	_ = os.Mkdir(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "hidden.js"), []byte("sub"), 0644)

	handler, err := NewFlatDirectoryHandler(tmpDir)
	if err != nil {
		t.Fatalf("NewFlatDirectoryHandler failed: %v", err)
	}

	// 1. Root /
	{
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET / expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "Lion SPA") {
			t.Errorf("GET / expected 'Lion SPA', got %q", rr.Body.String())
		}
	}

	// 2. Static Flat Assets
	{
		req := httptest.NewRequest("GET", "/bundle.js", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /bundle.js expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "lion bundle") {
			t.Errorf("GET /bundle.js unexpected content: %q", rr.Body.String())
		}
		if !strings.Contains(rr.Header().Get("Content-Type"), "javascript") {
			t.Errorf("GET /bundle.js Content-Type expected javascript, got %q", rr.Header().Get("Content-Type"))
		}
	}

	{
		req := httptest.NewRequest("GET", "/style.css", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /style.css expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "background: #000") {
			t.Errorf("GET /style.css unexpected content: %q", rr.Body.String())
		}
		if !strings.Contains(rr.Header().Get("Content-Type"), "text/css") {
			t.Errorf("GET /style.css Content-Type expected text/css, got %q", rr.Header().Get("Content-Type"))
		}
	}

	// 3. SPA History API Fallback (routes without file extension)
	{
		req := httptest.NewRequest("GET", "/dashboard/analytics", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /dashboard/analytics expected 200 (fallback), got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "Lion SPA") {
			t.Errorf("GET /dashboard/analytics expected index.html fallback, got %q", rr.Body.String())
		}
	}

	// 4. Missing Static Asset with Extension -> MUST NOT fallback, MUST return 404
	{
		req := httptest.NewRequest("GET", "/missing-script.js", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET /missing-script.js expected 404, got %d", rr.Code)
		}
	}

	// 5. Flat Security Invariant: Subdirectory requests rejected
	{
		req := httptest.NewRequest("GET", "/sub/hidden.js", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET /sub/hidden.js expected 404 (subdirectory forbidden), got %d", rr.Code)
		}
	}

	// 6. Flat Security Invariant: Dot-prefixed hidden files rejected
	{
		req := httptest.NewRequest("GET", "/.env", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET /.env expected 404, got %d", rr.Code)
		}
	}
}

func TestServerAppHostingE2E(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := NewServer(cat, eng)

	tmpDir := t.TempDir()
	indexContent := `<!DOCTYPE html><html><body><h1>E2E SPA App</h1></body></html>`
	bundleContent := `var app = 1;`
	_ = os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte(indexContent), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "app.js"), []byte(bundleContent), 0644)

	// Bind App path
	if err := server.SetAppPath(tmpDir); err != nil {
		t.Fatalf("SetAppPath failed: %v", err)
	}

	handler := server.Handler()

	// 1. API Health should work seamlessly (Highest priority routing)
	{
		req := httptest.NewRequest("GET", "/api/health", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /api/health expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "ok") {
			t.Errorf("GET /api/health expected 'ok', got %q", rr.Body.String())
		}
	}

	// 2. Web Studio should work seamlessly
	{
		req := httptest.NewRequest("GET", "/_/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /_/ expected 200, got %d", rr.Code)
		}
	}

	// 3. SPA Root (GET /)
	{
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET / expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "E2E SPA App") {
			t.Errorf("GET / expected 'E2E SPA App', got %q", rr.Body.String())
		}
	}

	// 4. Flat static asset (GET /app.js)
	{
		req := httptest.NewRequest("GET", "/app.js", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /app.js expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "var app = 1;") {
			t.Errorf("GET /app.js expected 'var app = 1;', got %q", rr.Body.String())
		}
	}

	// 5. SPA Route (GET /orders/123) -> Fallback to index.html
	{
		req := httptest.NewRequest("GET", "/orders/123", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /orders/123 expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "E2E SPA App") {
			t.Errorf("GET /orders/123 expected 'E2E SPA App', got %q", rr.Body.String())
		}
	}
}
