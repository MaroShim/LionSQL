package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"sqlion/pkg/config"
)

func (s *Server) handleListOAuthProviders(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"providers": map[string]any{}})
		return
	}
	providers := s.oauth.GetPublicProviders()
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *Server) handleGetOAuthURL(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("oauth2 is not configured"))
		return
	}
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing provider query parameter"))
		return
	}
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")

	authURL, err := s.oauth.BuildAuthURL(provider, redirectURI, state)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"provider": provider,
		"url":      authURL,
	})
}

type oauthCallbackReq struct {
	Provider    string `json:"provider"`
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("oauth2 is not configured"))
		return
	}

	var provider, code, redirectURI string

	if r.Method == http.MethodPost {
		var req oauthCallbackReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json payload: %w", err))
			return
		}
		provider = req.Provider
		code = req.Code
		redirectURI = req.RedirectURI
	} else {
		// GET method: query parameters
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			errDesc := r.URL.Query().Get("error_description")
			writeError(w, http.StatusBadRequest, fmt.Errorf("oauth provider error: %s (%s)", errParam, errDesc))
			return
		}
		provider = r.URL.Query().Get("provider")
		code = r.URL.Query().Get("code")
		redirectURI = r.URL.Query().Get("redirect_uri")
	}

	if provider == "" || code == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provider and code are required"))
		return
	}

	userInfo, err := s.oauth.ExchangeAndFetchUser(r.Context(), provider, code, redirectURI)
	if err != nil {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("oauth verification failed: %w", err))
		return
	}

	// Auto-provision or link existing superuser account
	user, err := s.auth.ProvisionOAuthUser(userInfo.Provider, userInfo.ID, userInfo.Email, userInfo.Name, userInfo.AvatarURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to provision user: %w", err))
		return
	}

	token, err := s.auth.GenerateToken(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to issue jwt: %w", err))
		return
	}

	userRecord := superuserRecord(user)

	// For browser redirect GET requests without explicit application/json header, render HTML helper
	acceptHeader := r.Header.Get("Accept")
	if r.Method == http.MethodGet && (strings.Contains(acceptHeader, "text/html") || !strings.Contains(acceptHeader, "application/json")) {
		recordJSON, _ := json.Marshal(userRecord)
		html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><title>SQLion Auth</title></head>
<body>
<script>
  const token = %q;
  const record = %s;
  try {
    localStorage.setItem("sqlion_auth_token", token);
    localStorage.setItem("sqlion_auth_record", JSON.stringify(record));
  } catch(e){}
  if (window.opener) {
    window.opener.postMessage({ type: "SQLION_OAUTH_SUCCESS", token: token, record: record }, "*");
    window.close();
  } else {
    window.location.href = "/_/";
  }
</script>
<p>Authentication successful. Redirecting...</p>
</body>
</html>`, token, string(recordJSON))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":  token,
		"record": userRecord,
	})
}

func (s *Server) handleGetOAuthSettings(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeJSON(w, http.StatusOK, config.OAuth2Config{})
		return
	}
	cfg := s.oauth.GetConfig()
	// Mask secrets for display
	cfg.Google.ClientSecret = cfg.Google.MaskedSecret()
	cfg.GitHub.ClientSecret = cfg.GitHub.MaskedSecret()
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleUpdateOAuthSettings(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("oauth2 is not configured"))
		return
	}
	var newCfg config.OAuth2Config
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json payload: %w", err))
		return
	}

	s.oauth.UpdateConfig(newCfg)

	resCfg := s.oauth.GetConfig()
	resCfg.Google.ClientSecret = resCfg.Google.MaskedSecret()
	resCfg.GitHub.ClientSecret = resCfg.GitHub.MaskedSecret()
	writeJSON(w, http.StatusOK, resCfg)
}
