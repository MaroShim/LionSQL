package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func (s *Server) handleGetMailSettings(w http.ResponseWriter, r *http.Request) {
	if s.mailer == nil {
		writeJSON(w, http.StatusOK, SMTPConfig{Enabled: false})
		return
	}
	cfg := s.mailer.GetConfig()
	writeJSON(w, http.StatusOK, cfg.MaskedPassword())
}

func (s *Server) handleUpdateMailSettings(w http.ResponseWriter, r *http.Request) {
	if s.mailer == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("mailer is not initialized"))
		return
	}

	var req SMTPConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	s.mailer.UpdateConfig(req)
	cfg := s.mailer.GetConfig()
	writeJSON(w, http.StatusOK, cfg.MaskedPassword())
}

func (s *Server) handleTestMail(w http.ResponseWriter, r *http.Request) {
	if s.mailer == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("mailer is not initialized"))
		return
	}

	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("target 'to' email address required"))
		return
	}

	err := s.mailer.SendTemplate(req.To, "welcome", map[string]interface{}{
		"UserName": req.To,
		"AppName":  "SQLion Test Email",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Test email sent successfully to " + req.To,
	})
}

func (s *Server) handleGetMailLogs(w http.ResponseWriter, r *http.Request) {
	if s.mailer == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": []interface{}{}})
		return
	}

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	logs := s.mailer.GetDeliveryLogs(limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": logs,
		"total": len(logs),
	})
}

func (s *Server) handleDeleteMailLogs(w http.ResponseWriter, r *http.Request) {
	if s.mailer == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}
	s.mailer.ClearLogs()
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *Server) handleRequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("email address required"))
		return
	}

	// Generate secure one-time reset token
	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	resetToken := hex.EncodeToString(tokenBytes)

	actionURL := fmt.Sprintf("http://%s/reset-password?token=%s", r.Host, resetToken)

	if s.mailer != nil && s.mailer.GetConfig().Enabled {
		_ = s.mailer.SendTemplate(req.Email, "password_reset", map[string]interface{}{
			"Token":     resetToken,
			"ActionURL": actionURL,
			"AppName":   "SQLion",
		})
	}

	// Always return 200 OK to prevent email enumeration
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "If the email is registered, a password reset link has been dispatched.",
	})
}
