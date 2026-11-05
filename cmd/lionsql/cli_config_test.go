package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"lionsql/pkg/api"
	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestCLIGetAndSet(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	authSvc := auth.NewAuthService(auth.Config{})
	_, _ = authSvc.EnsureDefaultSuperuser("admin@lionsql.local", "1234567890")

	server := api.NewServerWithAuth(cat, eng, authSvc, true)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// 1. runGet without args
	if err := runGet([]string{"--addr=" + ts.URL}); err != nil {
		t.Fatalf("runGet failed: %v", err)
	}

	// 2. runGet with specific key
	if err := runGet([]string{"--addr=" + ts.URL, "tolerant_ddl"}); err != nil {
		t.Fatalf("runGet key failed: %v", err)
	}

	// 3. runSet with invalid password -> error
	err := runSet([]string{"--addr=" + ts.URL, "--pass=wrongpass", "tolerant_ddl", "on"})
	if err == nil {
		t.Fatalf("expected error with wrong password, got nil")
	}

	// 4. runSet with correct password via flag
	if err := runSet([]string{"--addr=" + ts.URL, "--pass=1234567890", "tolerant_ddl", "on"}); err != nil {
		t.Fatalf("runSet failed: %v", err)
	}
	if !server.TolerantDDL() {
		t.Fatalf("expected server.TolerantDDL() to be true")
	}

	// 5. runSet via env var
	_ = os.Setenv("LIONSQL_ADMIN_PASS", "1234567890")
	defer os.Unsetenv("LIONSQL_ADMIN_PASS")

	if err := runSet([]string{"--addr=" + ts.URL, "tolerant_ddl", "off"}); err != nil {
		t.Fatalf("runSet with env pass failed: %v", err)
	}
	if server.TolerantDDL() {
		t.Fatalf("expected server.TolerantDDL() to be false")
	}
	_ = http.StatusOK
	_ = json.Marshal
}
