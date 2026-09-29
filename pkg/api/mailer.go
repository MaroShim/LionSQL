package api

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// SMTPConfig holds configuration for the SMTP client.
type SMTPConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	FromEmail string `json:"from_email"`
	FromName  string `json:"from_name"`
	TLS       bool   `json:"tls"`
	Enabled   bool   `json:"enabled"`
}

// MaskedPassword returns the config with sensitive password obscured.
func (c SMTPConfig) MaskedPassword() SMTPConfig {
	cpy := c
	if cpy.Password != "" {
		cpy.Password = "••••••••"
	}
	return cpy
}

// EmailMessage defines an outgoing email.
type EmailMessage struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	HTML    bool   `json:"html"`
}

// EmailDeliveryLog records the result of an email dispatch.
type EmailDeliveryLog struct {
	ID         string    `json:"id"`
	To         string    `json:"to"`
	Subject    string    `json:"subject"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	DurationMs int64     `json:"duration_ms"`
}

// Mailer handles SMTP email delivery, template rendering, and delivery logging.
type Mailer struct {
	mu       sync.RWMutex
	config   SMTPConfig
	logs     []*EmailDeliveryLog
	maxLogs  int
	sendFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

// NewMailer returns an initialized Mailer instance.
func NewMailer(cfg SMTPConfig) *Mailer {
	return &Mailer{
		config:  cfg,
		logs:    make([]*EmailDeliveryLog, 0, 100),
		maxLogs: 100,
	}
}

// SetSendFunc allows tests to mock actual network SMTP transport.
func (m *Mailer) SetSendFunc(fn func(addr string, a smtp.Auth, from string, to []string, msg []byte) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendFunc = fn
}

// GetConfig returns current SMTP configuration.
func (m *Mailer) GetConfig() SMTPConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// UpdateConfig updates SMTP settings.
func (m *Mailer) UpdateConfig(cfg SMTPConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Retain existing password if omitted or masked
	if cfg.Password == "" || cfg.Password == "••••••••" {
		cfg.Password = m.config.Password
	}
	m.config = cfg
}

// Send sends an email message according to active SMTP config.
func (m *Mailer) Send(msg EmailMessage) error {
	m.mu.RLock()
	cfg := m.config
	sendFn := m.sendFunc
	m.mu.RUnlock()

	start := time.Now()
	var sendErr error

	if !cfg.Enabled && sendFn == nil {
		sendErr = fmt.Errorf("mailer is disabled; configure SMTP settings first")
		m.recordLog(msg, false, sendErr.Error(), time.Since(start).Milliseconds())
		return sendErr
	}

	fromHeader := cfg.FromEmail
	if cfg.FromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", cfg.FromName, cfg.FromEmail)
	}

	contentType := "text/plain; charset=UTF-8"
	if msg.HTML {
		contentType = "text/html; charset=UTF-8"
	}

	headers := []string{
		fmt.Sprintf("From: %s", fromHeader),
		fmt.Sprintf("To: %s", msg.To),
		fmt.Sprintf("Subject: %s", msg.Subject),
		"MIME-Version: 1.0",
		fmt.Sprintf("Content-Type: %s", contentType),
		"",
		msg.Body,
	}
	payload := []byte(strings.Join(headers, "\r\n"))

	if sendFn != nil {
		sendErr = sendFn(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), nil, cfg.FromEmail, []string{msg.To}, payload)
	} else {
		sendErr = m.sendRealSMTP(cfg, msg.To, payload)
	}

	duration := time.Since(start).Milliseconds()
	if sendErr != nil {
		m.recordLog(msg, false, sendErr.Error(), duration)
		return sendErr
	}

	m.recordLog(msg, true, "", duration)
	return nil
}

func (m *Mailer) sendRealSMTP(cfg SMTPConfig, to string, payload []byte) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}

	if cfg.TLS {
		tlsConfig := &tls.Config{
			ServerName: cfg.Host,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("TLS dial failed: %w", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("SMTP client initialization failed: %w", err)
		}
		defer client.Quit()

		if auth != nil {
			if ok, _ := client.Extension("AUTH"); ok {
				if err = client.Auth(auth); err != nil {
					return fmt.Errorf("SMTP authentication failed: %w", err)
				}
			}
		}

		if err = client.Mail(cfg.FromEmail); err != nil {
			return fmt.Errorf("MAIL command failed: %w", err)
		}
		if err = client.Rcpt(to); err != nil {
			return fmt.Errorf("RCPT command failed: %w", err)
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("DATA command failed: %w", err)
		}
		if _, err = w.Write(payload); err != nil {
			return err
		}
		return w.Close()
	}

	return smtp.SendMail(addr, auth, cfg.FromEmail, []string{to}, payload)
}

func (m *Mailer) recordLog(msg EmailMessage, success bool, errMsg string, durationMs int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	log := &EmailDeliveryLog{
		ID:         "mail-" + hex.EncodeToString(idBytes),
		To:         msg.To,
		Subject:    msg.Subject,
		Success:    success,
		Error:      errMsg,
		Timestamp:  time.Now(),
		DurationMs: durationMs,
	}

	m.logs = append(m.logs, log)
	if len(m.logs) > m.maxLogs {
		m.logs = m.logs[len(m.logs)-m.maxLogs:]
	}
}

// GetDeliveryLogs returns recent email dispatch logs.
func (m *Mailer) GetDeliveryLogs(limit int) []*EmailDeliveryLog {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 || limit > len(m.logs) {
		limit = len(m.logs)
	}

	out := make([]*EmailDeliveryLog, 0, limit)
	for i := len(m.logs) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.logs[i])
	}
	return out
}

// ClearLogs clears recorded delivery logs.
func (m *Mailer) ClearLogs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = m.logs[:0]
}

// SendTemplate renders a built-in template and dispatches the email.
func (m *Mailer) SendTemplate(to, templateName string, data map[string]interface{}) error {
	tmplDef, exists := emailTemplates[templateName]
	if !exists {
		return fmt.Errorf("unknown email template %q", templateName)
	}

	if data == nil {
		data = make(map[string]interface{})
	}
	if _, ok := data["AppName"]; !ok {
		data["AppName"] = "SQLion Database"
	}

	subjTmpl, err := template.New("subj").Parse(tmplDef.Subject)
	if err != nil {
		return err
	}
	var subjBuf bytes.Buffer
	if err = subjTmpl.Execute(&subjBuf, data); err != nil {
		return err
	}

	bodyTmpl, err := template.New("body").Parse(tmplDef.HTMLBody)
	if err != nil {
		return err
	}
	var bodyBuf bytes.Buffer
	if err = bodyTmpl.Execute(&bodyBuf, data); err != nil {
		return err
	}

	return m.Send(EmailMessage{
		To:      to,
		Subject: subjBuf.String(),
		Body:    bodyBuf.String(),
		HTML:    true,
	})
}

type templateDefinition struct {
	Subject  string
	HTMLBody string
}

var emailTemplates = map[string]templateDefinition{
	"password_reset": {
		Subject: "Reset your {{.AppName}} password",
		HTMLBody: `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif; background:#f4f4f5; padding:20px;">
  <div style="max-width:540px; margin:auto; background:#ffffff; border-radius:8px; padding:30px; border:1px solid #e4e4e7;">
    <h2 style="color:#18181b; margin-top:0;">Password Reset Request</h2>
    <p style="color:#52525b; line-height:1.5;">You requested a password reset for your <strong>{{.AppName}}</strong> account.</p>
    <div style="margin:24px 0; text-align:center;">
      <a href="{{.ActionURL}}" style="background:#2563eb; color:#ffffff; padding:12px 24px; border-radius:6px; text-decoration:none; font-weight:600; display:inline-block;">Reset Password</a>
    </div>
    <p style="color:#71717a; font-size:0.85rem;">Or copy this link to your browser: <br><code style="word-break:break-all; color:#2563eb;">{{.ActionURL}}</code></p>
    <p style="color:#a1a1aa; font-size:0.75rem; margin-top:24px;">If you did not request this, please ignore this email.</p>
  </div>
</body>
</html>`,
	},
	"verification": {
		Subject: "Verify your email for {{.AppName}}",
		HTMLBody: `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif; background:#f4f4f5; padding:20px;">
  <div style="max-width:540px; margin:auto; background:#ffffff; border-radius:8px; padding:30px; border:1px solid #e4e4e7;">
    <h2 style="color:#18181b; margin-top:0;">Verify Your Email</h2>
    <p style="color:#52525b; line-height:1.5;">Welcome to <strong>{{.AppName}}</strong>! Please verify your email address to complete your registration.</p>
    <div style="margin:24px 0; text-align:center;">
      <a href="{{.ActionURL}}" style="background:#059669; color:#ffffff; padding:12px 24px; border-radius:6px; text-decoration:none; font-weight:600; display:inline-block;">Verify Email</a>
    </div>
    <p style="color:#71717a; font-size:0.85rem;">Token code: <code>{{.Token}}</code></p>
  </div>
</body>
</html>`,
	},
	"welcome": {
		Subject: "Welcome to {{.AppName}}",
		HTMLBody: `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif; background:#f4f4f5; padding:20px;">
  <div style="max-width:540px; margin:auto; background:#ffffff; border-radius:8px; padding:30px; border:1px solid #e4e4e7;">
    <h2 style="color:#18181b; margin-top:0;">Welcome, {{.UserName}}!</h2>
    <p style="color:#52525b; line-height:1.5;">Your account on <strong>{{.AppName}}</strong> is ready to use.</p>
    <p style="color:#52525b; line-height:1.5;">You can now log in and start creating tables, queries, and automated workflows.</p>
  </div>
</body>
</html>`,
	},
}
