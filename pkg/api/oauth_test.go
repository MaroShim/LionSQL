package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sqlion/pkg/auth"
	"sqlion/pkg/catalog"
	"sqlion/pkg/config"
	"sqlion/pkg/storage/memengine"
)

func setupTestServerWithAuth(t *testing.T) (*Server, *auth.AuthService) {
	eng := memengine.New()
	cat := catalog.New(eng)
	authSvc := auth.NewAuthService(auth.Config{})
	_, err := authSvc.EnsureDefaultSuperuser("admin@sqlion.local", "secret12345")
	if err != nil {
		t.Fatalf("Failed to create admin superuser: %v", err)
	}

	server := NewServerWithAuth(cat, eng, authSvc, true)
	return server, authSvc
}

func TestOAuth2_ProvidersAndURL(t *testing.T) {
	server, _ := setupTestServerWithAuth(t)

	// 1. Initial providers list (none enabled)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth2/providers", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var provRes struct {
		Providers map[string]OAuthProviderPublicInfo `json:"providers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&provRes); err != nil {
		t.Fatalf("Failed to decode providers: %v", err)
	}
	if provRes.Providers["google"].Enabled || provRes.Providers["github"].Enabled {
		t.Fatalf("Expected providers to be disabled initially")
	}

	// 2. Configure providers
	server.SetOAuthConfig(config.OAuth2Config{
		Google: config.OAuthProviderConfig{
			Enabled:     true,
			ClientID:    "google-client-123",
			RedirectURL: "http://localhost:8090/api/auth/oauth2/callback",
		},
		GitHub: config.OAuthProviderConfig{
			Enabled:     true,
			ClientID:    "github-client-456",
			RedirectURL: "http://localhost:8090/api/auth/oauth2/callback",
		},
	})

	// Check providers list again
	req = httptest.NewRequest(http.MethodGet, "/api/auth/oauth2/providers", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if err := json.NewDecoder(w.Body).Decode(&provRes); err != nil {
		t.Fatalf("Failed to decode providers: %v", err)
	}
	if !provRes.Providers["google"].Enabled || provRes.Providers["google"].ClientID != "google-client-123" {
		t.Fatalf("Expected google provider enabled with client ID")
	}
	if !provRes.Providers["github"].Enabled || provRes.Providers["github"].ClientID != "github-client-456" {
		t.Fatalf("Expected github provider enabled with client ID")
	}

	// 3. Generate Auth URLs
	req = httptest.NewRequest(http.MethodGet, "/api/auth/oauth2/url?provider=google&state=csrf_123", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for google auth url, got %d: %s", w.Code, w.Body.String())
	}
	var urlRes struct {
		Provider string `json:"provider"`
		URL      string `json:"url"`
	}
	if err := json.NewDecoder(w.Body).Decode(&urlRes); err != nil {
		t.Fatalf("Failed to decode url response: %v", err)
	}
	if !strings.Contains(urlRes.URL, "client_id=google-client-123") || !strings.Contains(urlRes.URL, "state=csrf_123") {
		t.Fatalf("Google auth URL missing expected parameters: %s", urlRes.URL)
	}

	// 4. Unknown provider error
	req = httptest.NewRequest(http.MethodGet, "/api/auth/oauth2/url?provider=facebook", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for unknown provider, got %d", w.Code)
	}
}

func TestOAuth2_GoogleFlowHermetic(t *testing.T) {
	server, authSvc := setupTestServerWithAuth(t)

	// Mock Google OAuth2 Server
	mockGoogle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			_ = r.ParseForm()
			if r.FormValue("code") != "valid_google_code" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_grant","error_description":"Code expired"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"mock_google_token_abc","token_type":"Bearer","expires_in":3600}`))

		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer mock_google_token_abc" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "google_uid_998877",
				"email": "tester@google.com",
				"name": "Google Tester",
				"picture": "https://lh3.googleusercontent.com/avatar.png"
			}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGoogle.Close()

	// Configure Google OAuth to point to mock server
	server.OAuth().SetHTTPClient(mockGoogle.Client())
	server.SetOAuthConfig(config.OAuth2Config{
		Google: config.OAuthProviderConfig{
			Enabled:      true,
			ClientID:     "g_client",
			ClientSecret: "g_secret",
			TokenURL:     mockGoogle.URL + "/token",
			UserInfoURL:  mockGoogle.URL + "/userinfo",
			RedirectURL:  "http://localhost:8090/api/auth/oauth2/callback",
		},
	})

	// 1. Test POST /api/auth/oauth2/callback
	callbackPayload := map[string]string{
		"provider":     "google",
		"code":         "valid_google_code",
		"redirect_uri": "http://localhost:8090/api/auth/oauth2/callback",
	}
	bodyBytes, _ := json.Marshal(callbackPayload)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth2/callback", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from oauth callback, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		Token  string `json:"token"`
		Record struct {
			ID            string `json:"id"`
			Email         string `json:"email"`
			Name          string `json:"name"`
			AvatarURL     string `json:"avatar_url"`
			OAuthProvider string `json:"oauth_provider"`
		} `json:"record"`
	}
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to decode callback response: %v", err)
	}

	if res.Token == "" {
		t.Fatalf("Expected non-empty JWT token")
	}
	if res.Record.Email != "tester@google.com" || res.Record.Name != "Google Tester" {
		t.Fatalf("Unexpected user record in response: %+v", res.Record)
	}
	if res.Record.OAuthProvider != "google" {
		t.Fatalf("Expected oauth_provider=google, got %s", res.Record.OAuthProvider)
	}

	// 2. Verify issued token can authenticate against /api/auth/me
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+res.Token)
	meW := httptest.NewRecorder()
	server.ServeHTTP(meW, meReq)

	if meW.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/auth/me, got %d: %s", meW.Code, meW.Body.String())
	}

	var meRes struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(meW.Body).Decode(&meRes); err != nil {
		t.Fatalf("Failed to decode /api/auth/me response: %v", err)
	}
	if meRes.Email != "tester@google.com" || meRes.Name != "Google Tester" {
		t.Fatalf("Mismatch in /api/auth/me: %+v", meRes)
	}

	// Verify user is provisioned in AuthService
	u := authSvc.GetUserByOAuth("google", "google_uid_998877")
	if u == nil {
		t.Fatalf("Expected user to be findable in AuthService by OAuth key")
	}

	// 3. Test failure case with invalid code
	errPayload := map[string]string{
		"provider": "google",
		"code":     "bad_code",
	}
	errBytes, _ := json.Marshal(errPayload)
	errReq := httptest.NewRequest(http.MethodPost, "/api/auth/oauth2/callback", bytes.NewReader(errBytes))
	errReq.Header.Set("Content-Type", "application/json")
	errW := httptest.NewRecorder()
	server.ServeHTTP(errW, errReq)
	if errW.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized for invalid code, got %d", errW.Code)
	}
}

func TestOAuth2_GitHubFlowWithPrivateEmail(t *testing.T) {
	server, _ := setupTestServerWithAuth(t)

	// Mock GitHub OAuth2 Server (returning empty email on /user and private verified email on /user/emails)
	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			_ = r.ParseForm()
			if r.FormValue("code") != "valid_github_code" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"mock_gh_token_xyz","token_type":"bearer"}`))

		case "/user":
			if r.Header.Get("Authorization") != "Bearer mock_gh_token_xyz" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			// Primary email is private (null/empty), numeric ID
			w.Write([]byte(`{
				"id": 1234567,
				"login": "octotests",
				"name": "Octo Cat",
				"email": null,
				"avatar_url": "https://avatars.githubusercontent.com/u/1234567"
			}`))

		case "/user/emails":
			if r.Header.Get("Authorization") != "Bearer mock_gh_token_xyz" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
				{"email": "unverified@example.com", "primary": false, "verified": false},
				{"email": "octo_private@users.noreply.github.com", "primary": true, "verified": true}
			]`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGitHub.Close()

	server.OAuth().SetHTTPClient(mockGitHub.Client())
	server.SetOAuthConfig(config.OAuth2Config{
		GitHub: config.OAuthProviderConfig{
			Enabled:      true,
			ClientID:     "gh_client",
			ClientSecret: "gh_secret",
			TokenURL:     mockGitHub.URL + "/login/oauth/access_token",
			UserInfoURL:  mockGitHub.URL + "/user",
			RedirectURL:  "http://localhost:8090/api/auth/oauth2/callback",
		},
	})

	// Test GET /api/auth/oauth2/callback (browser redirect simulation)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth2/callback?provider=github&code=valid_github_code", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GET browser callback, got %d: %s", w.Code, w.Body.String())
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "sqlion_auth_token") || !strings.Contains(bodyStr, "octo_private@users.noreply.github.com") {
		t.Fatalf("Browser HTML response missing token or resolved private email: %s", bodyStr)
	}
}

func TestOAuth2_SettingsEndpoint(t *testing.T) {
	server, authSvc := setupTestServerWithAuth(t)

	// Admin token
	admin, _ := authSvc.EnsureDefaultSuperuser("admin@sqlion.local", "secret12345")
	adminToken, _ := authSvc.GenerateToken(admin)

	// 1. GET /api/settings/oauth2
	req := httptest.NewRequest(http.MethodGet, "/api/settings/oauth2", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GET settings, got %d", w.Code)
	}

	// 2. PUT /api/settings/oauth2 to configure Google
	updateCfg := config.OAuth2Config{
		Google: config.OAuthProviderConfig{
			Enabled:      true,
			ClientID:     "live_google_id",
			ClientSecret: "top_secret_key",
		},
	}
	cfgBytes, _ := json.Marshal(updateCfg)
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings/oauth2", bytes.NewReader(cfgBytes))
	putReq.Header.Set("Authorization", "Bearer "+adminToken)
	putReq.Header.Set("Content-Type", "application/json")
	putW := httptest.NewRecorder()
	server.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from PUT settings, got %d: %s", putW.Code, putW.Body.String())
	}

	respBytes := putW.Body.Bytes()
	var savedCfg config.OAuth2Config
	if err := json.Unmarshal(respBytes, &savedCfg); err != nil {
		t.Fatalf("Failed to decode saved settings: %v", err)
	}
	// Verify secret is masked in response
	if savedCfg.Google.ClientSecret != "••••••••" {
		t.Fatalf("Expected masked secret in settings response, got %s", savedCfg.Google.ClientSecret)
	}
	// Verify actual secret is stored internally
	internalCfg := server.OAuth().GetConfig()
	if internalCfg.Google.ClientSecret != "top_secret_key" {
		t.Fatalf("Expected actual secret saved internally, got %s", internalCfg.Google.ClientSecret)
	}

	// 3. Re-save with masked secret: actual secret must not be overwritten
	maskedSaveReq := httptest.NewRequest(http.MethodPut, "/api/settings/oauth2", bytes.NewReader(respBytes))
	maskedSaveReq.Header.Set("Authorization", "Bearer "+adminToken)
	maskedSaveReq.Header.Set("Content-Type", "application/json")
	maskedW := httptest.NewRecorder()
	server.ServeHTTP(maskedW, maskedSaveReq)
	if maskedW.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK: %s", maskedW.Body.String())
	}
	internalCfgAfter := server.OAuth().GetConfig()
	if internalCfgAfter.Google.ClientSecret != "top_secret_key" {
		t.Fatalf("Expected secret preserved when masked string submitted, got %s", internalCfgAfter.Google.ClientSecret)
	}
}
