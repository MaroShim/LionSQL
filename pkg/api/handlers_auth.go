package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"sqlion/pkg/auth"
)

type createAPIKeyReq struct {
	Name          string          `json:"name"`
	Role          auth.APIKeyRole `json:"role"`
	AllowedTables []string        `json:"allowed_tables"`
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys := s.auth.ListAPIKeys()
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api key name is required"))
		return
	}
	keyObj, err := s.auth.CreateAPIKey(req.Name, req.Role, req.AllowedTables)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, keyObj)
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.auth.RevokeAPIKey(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "api key revoked"})
}

type authLoginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req authLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	u, token, err := s.auth.Authenticate(req.Email, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":  token,
		"record": superuserRecord(u),
	})
}

func superuserRecord(u *auth.Superuser) map[string]any {
	return map[string]any{
		"id":         u.ID,
		"email":      u.Email,
		"name":       u.Name,
		"avatar_url": u.AvatarURL,
		"created_at": u.CreatedAt,
		"updated_at": u.UpdatedAt,
	}
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid authorization header"))
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	u, err := s.auth.ValidateToken(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	writeJSON(w, http.StatusOK, superuserRecord(u))
}

func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid authorization header"))
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	u, freshToken, err := s.auth.RefreshToken(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":  freshToken,
		"record": superuserRecord(u),
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid authorization header"))
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	u, err := s.auth.ValidateToken(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	if err := s.auth.RevokeAllTokens(u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "all sessions revoked successfully",
	})
}
