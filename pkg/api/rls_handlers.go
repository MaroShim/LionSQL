package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"sqlion/pkg/auth"
)

func (s *Server) extractAuthContext(r *http.Request) *AuthContext {
	authCtx := &AuthContext{
		Custom: make(map[string]string),
	}

	// 1. Check Bearer Token (Superuser JWT)
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := s.auth.ValidateToken(token)
		if err == nil && claims != nil {
			authCtx.ID = claims.ID
			authCtx.Email = claims.Email
			authCtx.Role = "admin"
			authCtx.IsSuperuser = true
			return authCtx
		}
	}

	// 2. Check API Key
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" && strings.HasPrefix(authHeader, "ApiKey ") {
		apiKey = strings.TrimPrefix(authHeader, "ApiKey ")
	}
	if apiKey != "" {
		keyObj, err := s.auth.ValidateAPIKey(apiKey)
		if err == nil && keyObj != nil {
			authCtx.ID = keyObj.ID
			authCtx.Role = string(keyObj.Role)
			if keyObj.Role == auth.RoleAdmin {
				authCtx.IsSuperuser = true
			}
		}
	}

	// 3. Forwarded identity headers from frontend / API Gateway
	if uid := r.Header.Get("X-User-ID"); uid != "" {
		authCtx.ID = uid
	} else if aid := r.Header.Get("X-Auth-ID"); aid != "" {
		authCtx.ID = aid
	}

	if email := r.Header.Get("X-User-Email"); email != "" {
		authCtx.Email = email
	}
	if role := r.Header.Get("X-User-Role"); role != "" {
		authCtx.Role = role
	}
	if tid := r.Header.Get("X-Tenant-ID"); tid != "" {
		authCtx.TenantID = tid
	}

	return authCtx
}

func (s *Server) handleGetTableRules(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	if tableName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("table name is required"))
		return
	}

	rules, exists := s.rls.GetRules(tableName)
	if !exists {
		writeJSON(w, http.StatusOK, TableRules{
			TableName: tableName,
		})
		return
	}

	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) handleSetTableRules(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	if tableName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("table name is required"))
		return
	}

	var tr TableRules
	if err := json.NewDecoder(r.Body).Decode(&tr); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json payload: %w", err))
		return
	}
	tr.TableName = tableName

	if err := s.rls.SetRules(&tr); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, tr)
}

func (s *Server) handleDeleteTableRules(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	if tableName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("table name is required"))
		return
	}

	s.rls.DeleteRules(tableName)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": "table rls rules deleted",
		"table":   tableName,
	})
}
