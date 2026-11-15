package dbmail

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

type sentEmail struct {
	addr string
	from string
	to   []string
	msg  []byte
}

func setupTestDBMailCoordinator(t *testing.T) (*engine.Coordinator, func()) {
	t.Helper()
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("open memengine failed: %v", err)
	}
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	if err := coord.EnsureDatabaseMailTables(); err != nil {
		t.Fatalf("EnsureDatabaseMailTables failed: %v", err)
	}
	cleanup := func() {
		_ = eng.Close()
	}
	return coord, cleanup
}

func TestWorker_ProcessBatch_Success(t *testing.T) {
	coord, cleanup := setupTestDBMailCoordinator(t)
	defer cleanup()

	// 1. Queue two mail items via sp_send_dbmail
	_, err := coord.ExecuteSQL(`EXEC msdb.dbo.sp_send_dbmail 
		@recipients = 'alice@example.com',
		@subject = 'Subject 1',
		@body = 'Text body 1',
		@body_format = 'TEXT';`)
	if err != nil {
		t.Fatalf("queue email 1 failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`EXEC msdb.dbo.sp_send_dbmail 
		@recipients = 'bob@example.com; charlie@example.com',
		@copy_recipients = 'dave@example.com',
		@subject = 'Subject 2',
		@body = '<h1>HTML Body 2</h1>',
		@body_format = 'HTML';`)
	if err != nil {
		t.Fatalf("queue email 2 failed: %v", err)
	}

	cfg := DefaultConfig()
	cfg.FromEmail = "notifications@lionsql.local"
	cfg.FromName = "LionSQL Bot"
	client := NewCoordinatorClient(coord)
	worker := NewWorker(cfg, client)

	var mu sync.Mutex
	var sentMails []sentEmail
	worker.SetSendMailFunc(func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		mu.Lock()
		defer mu.Unlock()
		sentMails = append(sentMails, sentEmail{addr: addr, from: from, to: to, msg: msg})
		return nil
	})

	ctx := context.Background()
	count, err := worker.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 items processed, got %d", count)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sentMails) != 2 {
		t.Fatalf("expected 2 emails sent via mock SMTP, got %d", len(sentMails))
	}

	// Verify Email 1
	m1 := sentMails[0]
	if m1.from != "notifications@lionsql.local" {
		t.Errorf("m1 from expected 'notifications@lionsql.local', got %s", m1.from)
	}
	if len(m1.to) != 1 || m1.to[0] != "alice@example.com" {
		t.Errorf("m1 to expected ['alice@example.com'], got %v", m1.to)
	}
	msgStr1 := string(m1.msg)
	if !strings.Contains(msgStr1, "Content-Type: text/plain; charset=UTF-8") {
		t.Errorf("m1 expected text/plain content type, got: %s", msgStr1)
	}
	if !strings.Contains(msgStr1, "Text body 1") {
		t.Errorf("m1 expected body, got: %s", msgStr1)
	}

	// Verify Email 2 (HTML format and multiple recipients)
	m2 := sentMails[1]
	expectedRecipients := []string{"bob@example.com", "charlie@example.com", "dave@example.com"}
	if len(m2.to) != len(expectedRecipients) {
		t.Errorf("m2 expected %d recipients, got %v", len(expectedRecipients), m2.to)
	}
	msgStr2 := string(m2.msg)
	if !strings.Contains(msgStr2, "Content-Type: text/html; charset=UTF-8") {
		t.Errorf("m2 expected text/html content type, got: %s", msgStr2)
	}
	if !strings.Contains(msgStr2, "<h1>HTML Body 2</h1>") {
		t.Errorf("m2 expected HTML body, got: %s", msgStr2)
	}

	// Verify database rows updated to 'sent'
	checkRes, err := coord.ExecuteSQL("SELECT mailitem_id, sent_status, sent_date FROM sysmail_mailitems ORDER BY mailitem_id;")
	if err != nil {
		t.Fatalf("check sysmail_mailitems failed: %v", err)
	}
	for i, r := range checkRes.Rows {
		if r["sent_status"] != "sent" {
			t.Errorf("item %d expected sent_status='sent', got %v", i+1, r["sent_status"])
		}
		if r["sent_date"] == nil || r["sent_date"] == "" {
			t.Errorf("item %d expected non-null sent_date", i+1)
		}
	}

	// Verify sysmail_log has information entries
	logRes, err := coord.ExecuteSQL("SELECT mailitem_id, event_type, description FROM sysmail_log ORDER BY log_id;")
	if err != nil {
		t.Fatalf("check sysmail_log failed: %v", err)
	}
	if len(logRes.Rows) != 2 {
		t.Fatalf("expected 2 log rows, got %d", len(logRes.Rows))
	}
	for i, r := range logRes.Rows {
		if r["event_type"] != "information" {
			t.Errorf("log %d expected event_type='information', got %v", i+1, r["event_type"])
		}
	}

	// Subsequent ProcessBatch should find 0 items
	count2, err := worker.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("second ProcessBatch failed: %v", err)
	}
	if count2 != 0 {
		t.Fatalf("expected 0 items in second batch, got %d", count2)
	}
}

func TestWorker_ProcessBatch_Failure(t *testing.T) {
	coord, cleanup := setupTestDBMailCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`EXEC msdb.dbo.sp_send_dbmail 
		@recipients = 'fail@example.com',
		@subject = 'Will Fail',
		@body = 'Testing error handling';`)
	if err != nil {
		t.Fatalf("queue email failed: %v", err)
	}

	cfg := DefaultConfig()
	cfg.FromEmail = "notifications@lionsql.local"
	client := NewCoordinatorClient(coord)
	worker := NewWorker(cfg, client)

	// Mock SMTP returns failure
	worker.SetSendMailFunc(func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		return errors.New("simulated SMTP connection refused: 550 Relay Access Denied")
	})

	ctx := context.Background()
	count, err := worker.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 item processed, got %d", count)
	}

	// Verify item marked as 'failed'
	checkRes, err := coord.ExecuteSQL("SELECT mailitem_id, sent_status FROM sysmail_mailitems WHERE mailitem_id = 1;")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if checkRes.Rows[0]["sent_status"] != "failed" {
		t.Errorf("expected sent_status='failed', got %v", checkRes.Rows[0]["sent_status"])
	}

	// Verify error log recorded
	logRes, err := coord.ExecuteSQL("SELECT mailitem_id, event_type, description FROM sysmail_log WHERE mailitem_id = 1;")
	if err != nil {
		t.Fatalf("check log failed: %v", err)
	}
	if len(logRes.Rows) != 1 {
		t.Fatalf("expected 1 log row, got %d", len(logRes.Rows))
	}
	if logRes.Rows[0]["event_type"] != "error" {
		t.Errorf("expected event_type='error', got %v", logRes.Rows[0]["event_type"])
	}
	desc := logRes.Rows[0]["description"].(string)
	if !strings.Contains(desc, "550 Relay Access Denied") {
		t.Errorf("expected error description to contain failure message, got: %s", desc)
	}
}

func TestWorker_BackgroundLifecycle(t *testing.T) {
	coord, cleanup := setupTestDBMailCoordinator(t)
	defer cleanup()

	cfg := DefaultConfig()
	cfg.PollInterval = 20 * time.Millisecond
	cfg.FromEmail = "bot@lionsql.local"
	client := NewCoordinatorClient(coord)
	worker := NewWorker(cfg, client)

	receivedCh := make(chan struct{}, 1)
	worker.SetSendMailFunc(func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		select {
		case receivedCh <- struct{}{}:
		default:
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := worker.Start(ctx); err != nil {
		t.Fatalf("worker.Start failed: %v", err)
	}

	// Queue a mail item while worker is running in background
	_, err := coord.ExecuteSQL(`EXEC msdb.dbo.sp_send_dbmail 
		@recipients = 'bg@example.com',
		@subject = 'Async Test',
		@body = 'Background loop test';`)
	if err != nil {
		t.Fatalf("queue mail failed: %v", err)
	}

	// Wait deterministically for receipt
	select {
	case <-receivedCh:
		// Success!
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for background worker to process mail item")
	}

	worker.Stop()
}

func TestRegression_AUDIT_093_021_SanitizeSMTPHeaderCRLFInjection(t *testing.T) {
	coord, cleanup := setupTestDBMailCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL("EXEC msdb.dbo.sp_send_dbmail @recipients = 'victim@example.com\r\nBcc: attacker@evil.com', @subject = 'Safe\r\nX-Injected: pwned', @body = 'Hello';")
	if err != nil {
		t.Fatalf("queue email failed: %v", err)
	}

	cfg := DefaultConfig()
	cfg.FromEmail = "bot@lionsql.local\r\nX-Evil: 1"
	cfg.FromName = "LionSQL\r\nAdmin"
	client := NewCoordinatorClient(coord)
	worker := NewWorker(cfg, client)

	var capturedMsg string
	worker.SetSendMailFunc(func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		capturedMsg = string(msg)
		return nil
	})

	count, err := worker.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 item processed, got %d", count)
	}
	if strings.Contains(capturedMsg, "\r\nX-Injected:") || strings.Contains(capturedMsg, "\r\nBcc:") || strings.Contains(capturedMsg, "\r\nX-Evil:") {
		t.Fatalf("CRLF injection was not sanitized in headers: %q", capturedMsg)
	}
}
