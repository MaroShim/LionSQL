package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddleware_CORSAndSecurityHeaders(t *testing.T) {
	srv := setupTestServer()
	handler := srv.Handler()

	// 1. Regular GET request should include CORS and Security headers
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	h := rec.Header()
	if h.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got %q", h.Get("Access-Control-Allow-Origin"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got %q", h.Get("X-Content-Type-Options"))
	}
	if h.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("expected X-Frame-Options: SAMEORIGIN, got %q", h.Get("X-Frame-Options"))
	}

	// 2. OPTIONS Preflight request should return 204 No Content immediately
	optReq := httptest.NewRequest(http.MethodOptions, "/api/records/customers", nil)
	optReq.Header.Set("Origin", "http://localhost:3000")
	optReq.Header.Set("Access-Control-Request-Method", "POST")
	optRec := httptest.NewRecorder()
	handler.ServeHTTP(optRec, optReq)

	if optRec.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content for OPTIONS preflight, got %d", optRec.Code)
	}
	if optRec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header on OPTIONS, got %q", optRec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestMiddleware_GzipCompression(t *testing.T) {
	srv := setupTestServer()
	handler := srv.Handler()

	// 1. Client requesting gzip should receive Content-Encoding: gzip
	req := httptest.NewRequest(http.MethodGet, "/_/", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip, got %q", rec.Header().Get("Content-Encoding"))
	}

	// Decompress and verify content
	gzReader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer func() { _ = gzReader.Close() }()

	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("failed to read decompressed body: %v", err)
	}
	if !strings.Contains(string(decompressed), "LionSQL") {
		t.Errorf("decompressed HTML does not contain LionSQL")
	}

	// 2. Client without gzip header should receive plain uncompressed content
	plainReq := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	plainRec := httptest.NewRecorder()
	handler.ServeHTTP(plainRec, plainReq)

	if plainRec.Header().Get("Content-Encoding") != "" {
		t.Errorf("expected empty Content-Encoding without Accept-Encoding: gzip, got %q", plainRec.Header().Get("Content-Encoding"))
	}
}

func TestMiddleware_RequestBodyLimit(t *testing.T) {
	srv := setupTestServer()
	handler := srv.Handler()

	// 1. Normal payload fits within limit
	normalBody := []byte(`{"name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tables", bytes.NewReader(normalBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Will fail on schema validation, but not on payload size limit
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Errorf("unexpected 413 for normal payload")
	}

	// 2. Regression P0-35: Chunked transfer (ContentLength == -1) exceeding DefaultMaxRequestBodySize
	// Must be wrapped by MaxBytesReader and rejected on read.
	oversizedStream := io.LimitReader(zeroByteReader{}, DefaultMaxRequestBodySize+1024)
	chunkedReq := httptest.NewRequest(http.MethodPost, "/api/test/oversized", io.NopCloser(oversizedStream))
	chunkedReq.ContentLength = -1 // Chunked transfer has ContentLength == -1

	testHandler := corsAndSecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err == nil {
			t.Fatalf("expected MaxBytesError for chunked request exceeding size limit, but got nil")
		}
		var maxBytesErr *http.MaxBytesError
		if !strings.Contains(err.Error(), "request body too large") && !strings.Contains(err.Error(), "http: request body too large") {
			t.Errorf("expected request body too large error, got: %v", err)
		}
		_ = maxBytesErr
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))

	chunkedRec := httptest.NewRecorder()
	testHandler.ServeHTTP(chunkedRec, chunkedReq)
}

type zeroByteReader struct{}

func (z zeroByteReader) Read(p []byte) (n int, err error) {
	for i := range p {
		p[i] = 'A'
	}
	return len(p), nil
}

