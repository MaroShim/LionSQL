package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestMailer_SendAndTemplate(t *testing.T) {
	cfg := SMTPConfig{
		Host:      "smtp.example.com",
		Port:      587,
		Username:  "mailer@example.com",
		Password:  "secret-pwd",
		FromEmail: "no-reply@example.com",
		FromName:  "LionSQL Notifications",
		Enabled:   true,
	}

	mailer := NewMailer(cfg)

	var interceptedFrom string
	var interceptedTo []string
	var interceptedPayload []byte

	mailer.SetSendFunc(func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		interceptedFrom = from
		interceptedTo = to
		interceptedPayload = msg
		return nil
	})

	// 1. Direct Send
	err := mailer.Send(EmailMessage{
		To:      "alice@example.com",
		Subject: "Test Direct Message",
		Body:    "Hello World!",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if interceptedFrom != "no-reply@example.com" || len(interceptedTo) != 1 || interceptedTo[0] != "alice@example.com" {
		t.Fatalf("unexpected send intercepted: from=%q to=%v", interceptedFrom, interceptedTo)
	}
	if !strings.Contains(string(interceptedPayload), "Hello World!") {
		t.Fatalf("missing body in payload: %s", string(interceptedPayload))
	}

	// 2. Template Send: password_reset
	err = mailer.SendTemplate("bob@example.com", "password_reset", map[string]interface{}{
		"ActionURL": "https://app.example.com/reset?token=xyz123",
		"AppName":   "LionSQL Test",
	})
	if err != nil {
		t.Fatalf("SendTemplate failed: %v", err)
	}
	if !strings.Contains(string(interceptedPayload), "https://app.example.com/reset?token=xyz123") {
		t.Fatalf("expected template body to contain action URL: %s", string(interceptedPayload))
	}

	// 3. Delivery Logs
	logs := mailer.GetDeliveryLogs(10)
	if len(logs) != 2 {
		t.Fatalf("expected 2 delivery logs, got %d", len(logs))
	}
	if !logs[0].Success || logs[0].To != "bob@example.com" {
		t.Fatalf("unexpected latest log entry: %+v", logs[0])
	}

	// 4. Clear logs
	mailer.ClearLogs()
	if len(mailer.GetDeliveryLogs(10)) != 0 {
		t.Fatalf("expected logs to be empty after clear")
	}
}

func TestMailerAPI_Endpoints(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := NewServer(cat, eng)
	defer server.Crons().Stop()

	// Mock send function to prevent external dials
	server.Mailer().SetSendFunc(func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		return nil
	})

	// 1. Get Mail Settings (Initial)
	req := httptest.NewRequest("GET", "/api/settings/mail", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/settings/mail expected 200, got %d", w.Code)
	}

	// 2. Update Mail Settings
	updateBody := map[string]interface{}{
		"host":       "smtp.mailgun.org",
		"port":       587,
		"username":   "postmaster@domain.com",
		"password":   "super-secret-key",
		"from_email": "alerts@domain.com",
		"from_name":  "Domain Alerts",
		"enabled":    true,
	}
	payload, _ := json.Marshal(updateBody)
	req = httptest.NewRequest("PUT", "/api/settings/mail", bytes.NewReader(payload))
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings/mail expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resCfg SMTPConfig
	_ = json.Unmarshal(w.Body.Bytes(), &resCfg)
	if resCfg.Password != "••••••••" {
		t.Fatalf("expected masked password, got %q", resCfg.Password)
	}

	// 3. Test Mail Dispatch
	testBody, _ := json.Marshal(map[string]string{"to": "tester@example.com"})
	req = httptest.NewRequest("POST", "/api/settings/mail/test", bytes.NewReader(testBody))
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/settings/mail/test expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Get Mail Logs
	req = httptest.NewRequest("GET", "/api/mail/logs", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/mail/logs expected 200, got %d", w.Code)
	}

	// 5. Password Reset Request
	resetReqBody, _ := json.Marshal(map[string]string{"email": "user@example.com"})
	req = httptest.NewRequest("POST", "/api/auth/request-password-reset", bytes.NewReader(resetReqBody))
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/auth/request-password-reset expected 200, got %d", w.Code)
	}
}
