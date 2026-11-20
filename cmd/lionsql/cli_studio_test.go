package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/api"
	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// TestWebStudioHybrid_NoStudioFlag verifies that --no-studio disables / and /_/
// while preserving normal /api/ health and functional endpoints.
func TestWebStudioHybrid_NoStudioFlag(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	authSvc := auth.NewAuthService(auth.Config{})
	server := api.NewServerWithAuth(cat, eng, authSvc, false)

	// Case 1: Default mode -> Web Studio enabled
	if server.IsNoStudio() {
		t.Fatalf("expected default server.IsNoStudio() == false")
	}

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/_/", nil)
	server.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /_/, got %d", rec1.Code)
	}
	if !strings.Contains(rec1.Body.String(), "LionSQL Web Studio") {
		t.Fatalf("expected dashboard html on /_/")
	}

	// Case 2: Enable NoStudio
	server.SetNoStudio(true)
	if !server.IsNoStudio() {
		t.Fatalf("expected server.IsNoStudio() == true")
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/_/", nil)
	server.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found on /_/ when no-studio is active, got %d", rec2.Code)
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found on / when no-studio is active, got %d", rec3.Code)
	}

	// API endpoints must remain fully operational
	recHealth := httptest.NewRecorder()
	reqHealth := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	server.Handler().ServeHTTP(recHealth, reqHealth)
	if recHealth.Code != http.StatusOK {
		t.Fatalf("expected /api/health to return 200 OK, got %d", recHealth.Code)
	}
}

// TestWebStudioHybrid_StandaloneStudioProxy verifies that lionsql studio proxy
// serves dashboard.html and forwards /api/ calls to the remote target.
func TestWebStudioHybrid_StandaloneStudioProxy(t *testing.T) {
	// 1. Mock remote backend LionSQL instance (running with NoStudio=true)
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	authSvc := auth.NewAuthService(auth.Config{})
	backendServer := api.NewServerWithAuth(cat, eng, authSvc, false)
	backendServer.SetNoStudio(true)

	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind backend listener: %v", err)
	}
	defer backendListener.Close()

	backendHTTPServer := &http.Server{Handler: backendServer.Handler()}
	go func() {
		_ = backendHTTPServer.Serve(backendListener)
	}()
	defer backendHTTPServer.Close()

	backendURL := fmt.Sprintf("http://%s", backendListener.Addr().String())

	// 2. Pick a dynamic port for standalone studio proxy
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind proxy listener: %v", err)
	}
	proxyAddr := proxyListener.Addr().String()
	proxyListener.Close() // Free port for runStudioProxy to listen

	// 3. Launch runStudioProxy in background goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runStudioProxyWithContext(ctx, []string{
			"-listen", proxyAddr,
			"-target", backendURL,
		})
	}()

	// Wait for proxy to start listening
	var client = &http.Client{Timeout: 2 * time.Second}
	proxyURL := fmt.Sprintf("http://%s", proxyAddr)

	var studioOK bool
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		resp, err := client.Get(proxyURL + "/_/")
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(body), "LionSQL Web Studio") {
				studioOK = true
				break
			}
		}
	}
	if !studioOK {
		t.Fatalf("failed to connect to standalone Web Studio proxy at %s/_/", proxyURL)
	}

	// 4. Test proxy forwarding of /api/health through studio proxy to backend
	respHealth, err := client.Get(proxyURL + "/api/health")
	if err != nil {
		t.Fatalf("failed to proxy /api/health: %v", err)
	}
	defer respHealth.Body.Close()
	if respHealth.StatusCode != http.StatusOK {
		t.Fatalf("expected /api/health through proxy to return 200, got %d", respHealth.StatusCode)
	}

	// 5. Test root redirect / -> /_/ on proxy
	reqRoot, _ := http.NewRequest(http.MethodGet, proxyURL+"/", nil)
	clientNoRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 2 * time.Second,
	}
	respRoot, err := clientNoRedirect.Do(reqRoot)
	if err != nil {
		t.Fatalf("failed to request / on proxy: %v", err)
	}
	defer respRoot.Body.Close()
	if respRoot.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 Found on proxy root, got %d", respRoot.StatusCode)
	}
	if loc := respRoot.Header.Get("Location"); loc != "/_/" {
		t.Fatalf("expected Location /_/, got %s", loc)
	}
}
