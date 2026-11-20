package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lionsql/pkg/config"
	"lionsql/pkg/ui"
)

// runStudioProxy starts a standalone Web Studio server that serves dashboard.html
// and proxies all /api/ and /auth/ calls to a target remote/local LionSQL server.
func runStudioProxy(args []string) error {
	return runStudioProxyWithContext(context.Background(), args)
}

func runStudioProxyWithContext(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("studio", flag.ContinueOnError)
	listenAddr := fs.String("listen", "127.0.0.1:8090", "Studio listen HTTP address (e.g. 127.0.0.1:8090 or :8090)")
	fs.StringVar(listenAddr, "http", "127.0.0.1:8090", "Alias for -listen")
	targetURLStr := fs.String("target", "http://127.0.0.1:8090", "Target LionSQL HTTP API server URL")

	if err := fs.Parse(args); err != nil {
		return err
	}

	target := strings.TrimSpace(*targetURLStr)
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "http://" + target
	}

	parsedTarget, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("invalid target URL %q: %w", target, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(parsedTarget)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, fmt.Sprintf("LionSQL Studio Proxy Error: target %s unreachable (%v)", parsedTarget.Host, err), http.StatusBadGateway)
	}

	mux := http.NewServeMux()

	// 1. /_/ Web Studio HTML
	mux.HandleFunc("GET /_/", func(w http.ResponseWriter, r *http.Request) {
		html, err := ui.IndexHTML()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Write(html)
	})

	// 2. Proxy all /api/ endpoints to target LionSQL server
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})

	// 3. Root handler: redirect / -> /_/ or proxy other requests
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/_/", http.StatusFound)
			return
		}
		proxy.ServeHTTP(w, r)
	})

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("failed to bind studio listener on %s: %w", *listenAddr, err)
	}

	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	sigCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-sigCtx.Done()
		shutdownCtx, sCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer sCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Printf(`--------------------------------------------------------------------------------
 LionSQL Standalone Web Studio Proxy (v%s)
--------------------------------------------------------------------------------
 [Studio UI]       http://%s/_/
 [Proxy Target]    %s
--------------------------------------------------------------------------------
`, config.Version, listener.Addr().String(), parsedTarget.String())

	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	log.Printf("[LionSQL Studio] Proxy stopped cleanly.")
	return nil
}
