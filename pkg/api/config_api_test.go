package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestAPIConfigGetAndSet(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	authSvc := auth.NewAuthService(auth.Config{})
	_, _ = authSvc.EnsureDefaultSuperuser("admin@lionsql.local", "1234567890")

	server := NewServerWithAuth(cat, eng, authSvc, true)

	// 1. GET /api/config without key -> list all items
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /api/config, got %d", rec.Code)
	}

	var listResp struct {
		Configs []ConfigItem `json:"configs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to decode list configs response: %v", err)
	}
	if len(listResp.Configs) == 0 {
		t.Fatalf("expected at least one config item")
	}

	// 2. GET /api/config?key=tolerant_ddl -> single item (default: false)
	req = httptest.NewRequest(http.MethodGet, "/api/config?key=tolerant_ddl", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	var single ConfigItem
	if err := json.Unmarshal(rec.Body.Bytes(), &single); err != nil {
		t.Fatalf("failed to decode single config: %v", err)
	}
	if single.Key != "tolerant_ddl" || single.Value != false {
		t.Fatalf("expected tolerant_ddl to be false, got %v", single.Value)
	}

	// 3. POST /api/config without auth token -> 401 Unauthorized
	setPayload, _ := json.Marshal(map[string]any{
		"key":   "tolerant_ddl",
		"value": "on",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(setPayload))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 4. Authenticate and obtain token
	loginPayload, _ := json.Marshal(map[string]string{
		"identity": "admin@lionsql.local",
		"password": "1234567890",
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginPayload))
	loginRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from login, got %d: %s", loginRec.Code, loginRec.Body.String())
	}
	var authRes struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(loginRec.Body.Bytes(), &authRes)
	if authRes.Token == "" {
		t.Fatalf("expected valid auth token")
	}

	// 5. POST /api/config with valid superuser token -> toggle to true
	req = httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(setPayload))
	req.Header.Set("Authorization", "Bearer "+authRes.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from authorized set, got %d: %s", rec.Code, rec.Body.String())
	}
	if !server.TolerantDDL() {
		t.Fatalf("expected server.TolerantDDL() to be true after POST")
	}

	// 6. Verify GET reflects new value
	req = httptest.NewRequest(http.MethodGet, "/api/config?key=tolerant_ddl", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	_ = json.Unmarshal(rec.Body.Bytes(), &single)
	if single.Value != true {
		t.Fatalf("expected tolerant_ddl value to be true, got %v", single.Value)
	}

	// 7. Toggle back to false
	setPayloadOff, _ := json.Marshal(map[string]any{
		"key":   "tolerant_ddl",
		"value": "off",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(setPayloadOff))
	req.Header.Set("Authorization", "Bearer "+authRes.Token)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if server.TolerantDDL() {
		t.Fatalf("expected server.TolerantDDL() to be false after toggle off")
	}
}
