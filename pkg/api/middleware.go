package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	// DefaultMaxRequestBodySize limits HTTP request payloads to 32MB to prevent memory exhaustion DoS.
	DefaultMaxRequestBodySize = 32 << 20
)

var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer     *gzip.Writer
	headerSent bool
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.headerSent {
		return
	}
	w.headerSent = true
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.headerSent {
		w.WriteHeader(http.StatusOK)
	}
	return w.writer.Write(b)
}

func (w *gzipResponseWriter) Flush() {
	if w.writer != nil {
		_ = w.writer.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// corsAndSecurityMiddleware applies standard CORS headers, security hardening headers,
// and handles HTTP OPTIONS preflight requests cleanly.
func corsAndSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// 1. CORS Headers
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key, X-Requested-With, X-Query-Timeout, Accept, Origin")
		h.Set("Access-Control-Expose-Headers", "Content-Length, Content-Type, Content-Encoding, X-Query-Duration-Ms")
		h.Set("Access-Control-Max-Age", "86400")

		// 2. Security Hardening Headers
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// 3. Preflight OPTIONS Fast-Path
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// 4. Request Body Size Limit Guard (DoS defense)
		if r.Body != nil && r.ContentLength > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, DefaultMaxRequestBodySize)
		}

		next.ServeHTTP(w, r)
	})
}

// gzipMiddleware transparently compresses eligible HTTP response bodies using compress/gzip.
// Streaming endpoints (SSE / NDJSON) and WebSockets bypass compression to avoid buffering.
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bypass gzip for streaming endpoints or clients without gzip support
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			r.Header.Get("Upgrade") != "" ||
			strings.HasPrefix(r.URL.Path, "/api/cdc/stream") ||
			strings.HasPrefix(r.URL.Path, "/api/realtime") {
			next.ServeHTTP(w, r)
			return
		}

		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w)
		defer func() {
			_ = gz.Close()
			gzipWriterPool.Put(gz)
		}()

		gzw := &gzipResponseWriter{
			ResponseWriter: w,
			writer:         gz,
		}

		next.ServeHTTP(gzw, r)
	})
}
