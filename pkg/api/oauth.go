package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/config"
)

const (
	// Google OAuth2 endpoints and default scopes
	GoogleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleTokenURL    = "https://oauth2.googleapis.com/token"
	GoogleUserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"
	GoogleScopes      = "openid email profile"

	// GitHub OAuth2 endpoints and default scopes
	GitHubAuthURL     = "https://github.com/login/oauth/authorize"
	GitHubTokenURL    = "https://github.com/login/oauth/access_token"
	GitHubUserInfoURL = "https://api.github.com/user"
	GitHubEmailsURL   = "https://api.github.com/user/emails"
	GitHubScopes      = "read:user user:email"
)

var (
	ErrOAuthProviderDisabled   = errors.New("oauth provider is not enabled")
	ErrOAuthProviderNotConfig  = errors.New("oauth provider client_id or client_secret is missing")
	ErrOAuthUnknownProvider    = errors.New("unknown oauth provider")
	ErrOAuthCodeExchangeFailed = errors.New("failed to exchange authorization code")
	ErrOAuthUserFetchFailed    = errors.New("failed to fetch user profile from provider")
)

// OAuthUserInfo encapsulates normalized identity information returned by an OAuth2 provider.
type OAuthUserInfo struct {
	Provider  string `json:"provider"`
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// OAuthProviderPublicInfo exposes safe provider metadata to frontend clients.
type OAuthProviderPublicInfo struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	ClientID string `json:"client_id,omitempty"`
	AuthURL  string `json:"auth_url,omitempty"`
}

// OAuthManager manages third-party OAuth2 configurations, token exchange, and user profile resolution.
type OAuthManager struct {
	mu         sync.RWMutex
	cfg        config.OAuth2Config
	httpClient *http.Client
}

// NewOAuthManager initializes an OAuthManager with the provided configuration.
func NewOAuthManager(cfg config.OAuth2Config) *OAuthManager {
	return &OAuthManager{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SetHTTPClient overrides the internal HTTP client (used for hermetic tests with mock servers).
func (m *OAuthManager) SetHTTPClient(client *http.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpClient = client
}

// GetConfig returns a copy of current OAuth2 configuration.
func (m *OAuthManager) GetConfig() config.OAuth2Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// UpdateConfig updates the OAuth2 configuration, preserving existing secrets if incoming is masked or empty.
func (m *OAuthManager) UpdateConfig(newCfg config.OAuth2Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Google Secret preservation
	if newCfg.Google.ClientSecret == "" || strings.Contains(newCfg.Google.ClientSecret, "••••") {
		newCfg.Google.ClientSecret = m.cfg.Google.ClientSecret
	}
	// GitHub Secret preservation
	if newCfg.GitHub.ClientSecret == "" || strings.Contains(newCfg.GitHub.ClientSecret, "••••") {
		newCfg.GitHub.ClientSecret = m.cfg.GitHub.ClientSecret
	}

	m.cfg = newCfg
}

// GetPublicProviders returns client-safe metadata for all registered OAuth2 providers.
func (m *OAuthManager) GetPublicProviders() map[string]OAuthProviderPublicInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make(map[string]OAuthProviderPublicInfo)

	// Google
	gAuth := m.cfg.Google.AuthURL
	if gAuth == "" {
		gAuth = GoogleAuthURL
	}
	res["google"] = OAuthProviderPublicInfo{
		Name:     "Google",
		Enabled:  m.cfg.Google.Enabled && m.cfg.Google.ClientID != "",
		ClientID: m.cfg.Google.ClientID,
		AuthURL:  gAuth,
	}

	// GitHub
	ghAuth := m.cfg.GitHub.AuthURL
	if ghAuth == "" {
		ghAuth = GitHubAuthURL
	}
	res["github"] = OAuthProviderPublicInfo{
		Name:     "GitHub",
		Enabled:  m.cfg.GitHub.Enabled && m.cfg.GitHub.ClientID != "",
		ClientID: m.cfg.GitHub.ClientID,
		AuthURL:  ghAuth,
	}

	return res
}

// BuildAuthURL constructs the authorization redirect URL with CSRF state for the requested provider.
func (m *OAuthManager) BuildAuthURL(provider, redirectURI, state string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	normProvider := strings.ToLower(strings.TrimSpace(provider))
	switch normProvider {
	case "google":
		if !m.cfg.Google.Enabled {
			return "", ErrOAuthProviderDisabled
		}
		if m.cfg.Google.ClientID == "" {
			return "", ErrOAuthProviderNotConfig
		}
		authBase := m.cfg.Google.AuthURL
		if authBase == "" {
			authBase = GoogleAuthURL
		}
		red := redirectURI
		if red == "" {
			red = m.cfg.Google.RedirectURL
		}

		v := url.Values{}
		v.Set("client_id", m.cfg.Google.ClientID)
		v.Set("redirect_uri", red)
		v.Set("response_type", "code")
		v.Set("scope", GoogleScopes)
		v.Set("access_type", "offline")
		v.Set("prompt", "select_account")
		if state != "" {
			v.Set("state", state)
		}
		return authBase + "?" + v.Encode(), nil

	case "github":
		if !m.cfg.GitHub.Enabled {
			return "", ErrOAuthProviderDisabled
		}
		if m.cfg.GitHub.ClientID == "" {
			return "", ErrOAuthProviderNotConfig
		}
		authBase := m.cfg.GitHub.AuthURL
		if authBase == "" {
			authBase = GitHubAuthURL
		}
		red := redirectURI
		if red == "" {
			red = m.cfg.GitHub.RedirectURL
		}

		v := url.Values{}
		v.Set("client_id", m.cfg.GitHub.ClientID)
		v.Set("redirect_uri", red)
		v.Set("scope", GitHubScopes)
		if state != "" {
			v.Set("state", state)
		}
		return authBase + "?" + v.Encode(), nil

	default:
		return "", fmt.Errorf("%w: %q", ErrOAuthUnknownProvider, provider)
	}
}

// ExchangeAndFetchUser exchanges the authorization code for access tokens and fetches the user's profile.
func (m *OAuthManager) ExchangeAndFetchUser(ctx context.Context, provider, code, redirectURI string) (*OAuthUserInfo, error) {
	normProvider := strings.ToLower(strings.TrimSpace(provider))
	switch normProvider {
	case "google":
		return m.exchangeGoogle(ctx, code, redirectURI)
	case "github":
		return m.exchangeGitHub(ctx, code, redirectURI)
	default:
		return nil, fmt.Errorf("%w: %q", ErrOAuthUnknownProvider, provider)
	}
}

func (m *OAuthManager) exchangeGoogle(ctx context.Context, code, redirectURI string) (*OAuthUserInfo, error) {
	m.mu.RLock()
	cfg := m.cfg.Google
	client := m.httpClient
	m.mu.RUnlock()

	if !cfg.Enabled {
		return nil, ErrOAuthProviderDisabled
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, ErrOAuthProviderNotConfig
	}

	tokenEndpoint := cfg.TokenURL
	if tokenEndpoint == "" {
		tokenEndpoint = GoogleTokenURL
	}
	userEndpoint := cfg.UserInfoURL
	if userEndpoint == "" {
		userEndpoint = GoogleUserInfoURL
	}
	red := redirectURI
	if red == "" {
		red = cfg.RedirectURL
	}

	// 1. Exchange code for access token
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", red)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOAuthCodeExchangeFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read token response: %v", ErrOAuthCodeExchangeFailed, err)
	}

	var tokenRes struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenRes); err != nil {
		return nil, fmt.Errorf("%w: invalid token json: %v", ErrOAuthCodeExchangeFailed, err)
	}
	if tokenRes.Error != "" || tokenRes.AccessToken == "" {
		return nil, fmt.Errorf("%w: %s (%s)", ErrOAuthCodeExchangeFailed, tokenRes.Error, tokenRes.ErrorDesc)
	}

	// 2. Fetch user profile
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, userEndpoint, nil)
	if err != nil {
		return nil, err
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	userReq.Header.Set("Accept", "application/json")

	userResp, err := client.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOAuthUserFetchFailed, err)
	}
	defer userResp.Body.Close()

	userBody, err := io.ReadAll(userResp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read userinfo: %v", ErrOAuthUserFetchFailed, err)
	}

	var gUser struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(userBody, &gUser); err != nil {
		return nil, fmt.Errorf("%w: invalid userinfo json: %v", ErrOAuthUserFetchFailed, err)
	}
	if gUser.ID == "" {
		return nil, fmt.Errorf("%w: empty user id in google profile", ErrOAuthUserFetchFailed)
	}

	return &OAuthUserInfo{
		Provider:  "google",
		ID:        gUser.ID,
		Email:     strings.ToLower(strings.TrimSpace(gUser.Email)),
		Name:      gUser.Name,
		AvatarURL: gUser.Picture,
	}, nil
}

func (m *OAuthManager) exchangeGitHub(ctx context.Context, code, redirectURI string) (*OAuthUserInfo, error) {
	m.mu.RLock()
	cfg := m.cfg.GitHub
	client := m.httpClient
	m.mu.RUnlock()

	if !cfg.Enabled {
		return nil, ErrOAuthProviderDisabled
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, ErrOAuthProviderNotConfig
	}

	tokenEndpoint := cfg.TokenURL
	if tokenEndpoint == "" {
		tokenEndpoint = GitHubTokenURL
	}
	userEndpoint := cfg.UserInfoURL
	if userEndpoint == "" {
		userEndpoint = GitHubUserInfoURL
	}
	red := redirectURI
	if red == "" {
		red = cfg.RedirectURL
	}

	// 1. Exchange code for access token
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", red)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOAuthCodeExchangeFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read token response: %v", ErrOAuthCodeExchangeFailed, err)
	}

	var tokenRes struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenRes); err != nil {
		return nil, fmt.Errorf("%w: invalid token json: %v", ErrOAuthCodeExchangeFailed, err)
	}
	if tokenRes.Error != "" || tokenRes.AccessToken == "" {
		return nil, fmt.Errorf("%w: %s (%s)", ErrOAuthCodeExchangeFailed, tokenRes.Error, tokenRes.ErrorDesc)
	}

	// 2. Fetch user profile
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, userEndpoint, nil)
	if err != nil {
		return nil, err
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	userReq.Header.Set("Accept", "application/json")

	userResp, err := client.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOAuthUserFetchFailed, err)
	}
	defer userResp.Body.Close()

	userBody, err := io.ReadAll(userResp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read userinfo: %v", ErrOAuthUserFetchFailed, err)
	}

	var ghUser struct {
		ID        any    `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(userBody, &ghUser); err != nil {
		return nil, fmt.Errorf("%w: invalid github user json: %v", ErrOAuthUserFetchFailed, err)
	}

	var idStr string
	switch v := ghUser.ID.(type) {
	case float64:
		idStr = strconv.FormatInt(int64(v), 10)
	case string:
		idStr = v
	default:
		idStr = fmt.Sprintf("%v", ghUser.ID)
	}
	if idStr == "" || idStr == "<nil>" {
		return nil, fmt.Errorf("%w: empty user id in github profile", ErrOAuthUserFetchFailed)
	}

	resolvedEmail := strings.ToLower(strings.TrimSpace(ghUser.Email))
	userName := ghUser.Name
	if userName == "" {
		userName = ghUser.Login
	}

	// 3. Fallback to /user/emails if primary email is private or empty
	if resolvedEmail == "" {
		emailsEndpoint := GitHubEmailsURL
		if strings.Contains(userEndpoint, "/user") {
			emailsEndpoint = strings.TrimSuffix(userEndpoint, "/user") + "/user/emails"
		}
		if emailsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, emailsEndpoint, nil); err == nil {
			emailsReq.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
			emailsReq.Header.Set("Accept", "application/json")
			if emailsResp, err := client.Do(emailsReq); err == nil {
				defer emailsResp.Body.Close()
				if emailsBody, err := io.ReadAll(emailsResp.Body); err == nil {
					var emails []struct {
						Email    string `json:"email"`
						Primary  bool   `json:"primary"`
						Verified bool   `json:"verified"`
					}
					if err := json.Unmarshal(emailsBody, &emails); err == nil {
						for _, e := range emails {
							if e.Primary && e.Verified {
								resolvedEmail = strings.ToLower(strings.TrimSpace(e.Email))
								break
							}
						}
						if resolvedEmail == "" {
							for _, e := range emails {
								if e.Verified {
									resolvedEmail = strings.ToLower(strings.TrimSpace(e.Email))
									break
								}
							}
						}
					}
				}
			}
		}
	}

	return &OAuthUserInfo{
		Provider:  "github",
		ID:        idStr,
		Email:     resolvedEmail,
		Name:      userName,
		AvatarURL: ghUser.AvatarURL,
	}, nil
}
