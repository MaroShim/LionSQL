package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("target 'to' email address required"))
		return
	}

	if s.coordinator != nil {
		testQuery := fmt.Sprintf(`EXEC msdb.dbo.sp_send_dbmail 
			@profile_name = 'TestPing',
			@recipients = '%s',
			@subject = 'LionSQL DatabaseMail Test Ping',
			@body = 'This is a test notification queued via DatabaseMail sp_send_dbmail.',
			@body_format = 'TEXT';`, strings.ReplaceAll(req.To, "'", "''"))

		res, err := s.coordinator.ExecuteSQL(testQuery)
		if err == nil {
			msg := "Test email queued into DatabaseMail for dispatch to " + req.To
			if len(res.Messages) > 0 {
				msg = res.Messages[0]
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"success": true,
				"message": msg,
			})
			return
		}
	}

	if s.mailer == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("mailer is not initialized"))
		return
	}

	err := s.mailer.SendTemplate(req.To, "welcome", map[string]interface{}{
		"UserName": req.To,
		"AppName":  "LionSQL Test Email",
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
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	if s.coordinator != nil {
		query := fmt.Sprintf("SELECT log_id, mailitem_id, event_type, log_date, description FROM sysmail_log ORDER BY log_id DESC LIMIT %d;", limit)
		if res, err := s.coordinator.ExecuteSQL(query); err == nil && len(res.Rows) > 0 {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"items": res.Rows,
				"total": len(res.Rows),
			})
			return
		}
	}

	if s.mailer == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": []interface{}{}})
		return
	}

	logs := s.mailer.GetDeliveryLogs(limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": logs,
		"total": len(logs),
	})
}

func (s *Server) handleDeleteMailLogs(w http.ResponseWriter, r *http.Request) {
	if s.coordinator != nil {
		_, _ = s.coordinator.ExecuteSQL("DELETE FROM sysmail_log;")
	}
	if s.mailer != nil {
		s.mailer.ClearLogs()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *Server) handleGetDatabaseMailItems(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	if s.coordinator == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	query := fmt.Sprintf("SELECT mailitem_id, profile_name, recipients, copy_recipients, subject, body_format, sent_status, send_request_date, sent_date FROM sysmail_mailitems ORDER BY mailitem_id DESC LIMIT %d;", limit)
	res, err := s.coordinator.ExecuteSQL(query)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": res.Rows,
		"total": len(res.Rows),
	})
}

func (s *Server) handleGetDatabaseMailLogs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	if s.coordinator == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	query := fmt.Sprintf("SELECT log_id, mailitem_id, event_type, log_date, description FROM sysmail_log ORDER BY log_id DESC LIMIT %d;", limit)
	res, err := s.coordinator.ExecuteSQL(query)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": res.Rows,
		"total": len(res.Rows),
	})
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

	if s.coordinator != nil {
		queueSQL := fmt.Sprintf(`EXEC msdb.dbo.sp_send_dbmail 
			@recipients = '%s',
			@subject = 'LionSQL Password Reset Request',
			@body = 'Your password reset token is %s. Reset link: %s',
			@body_format = 'TEXT';`,
			strings.ReplaceAll(req.Email, "'", "''"),
			resetToken,
			actionURL,
		)
		_, _ = s.coordinator.ExecuteSQL(queueSQL)
	} else if s.mailer != nil && s.mailer.GetConfig().Enabled {
		_ = s.mailer.SendTemplate(req.Email, "password_reset", map[string]interface{}{
			"Token":     resetToken,
			"ActionURL": actionURL,
			"AppName":   "LionSQL",
		})
	}

	// Always return 200 OK to prevent email enumeration
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "If the email is registered, a password reset link has been dispatched.",
	})
}
