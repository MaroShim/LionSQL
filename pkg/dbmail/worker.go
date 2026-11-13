package dbmail

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lionsql/pkg/engine"
)

// SendMailFunc represents the signature for sending an email message over SMTP.
type SendMailFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// DBClient defines the database execution contract required by the DatabaseMail worker.
type DBClient interface {
	Query(ctx context.Context, query string) ([]map[string]any, error)
	Exec(ctx context.Context, query string) (int64, error)
	Close() error
}

// sqlDBClient adapts a standard database/sql connection pool.
type sqlDBClient struct {
	db *sql.DB
}

func NewSQLDBClient(db *sql.DB) DBClient {
	return &sqlDBClient{db: db}
}

func (c *sqlDBClient) Query(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]any
	for rows.Next() {
		colVals := make([]any, len(cols))
		colPointers := make([]any, len(cols))
		for i := range colVals {
			colPointers[i] = &colVals[i]
		}
		if err := rows.Scan(colPointers...); err != nil {
			return nil, err
		}
		rowMap := make(map[string]any, len(cols))
		for i, col := range cols {
			val := colVals[i]
			if b, ok := val.([]byte); ok {
				rowMap[strings.ToLower(col)] = string(b)
			} else {
				rowMap[strings.ToLower(col)] = val
			}
		}
		results = append(results, rowMap)
	}
	return results, rows.Err()
}

func (c *sqlDBClient) Exec(ctx context.Context, query string) (int64, error) {
	res, err := c.db.ExecContext(ctx, query)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (c *sqlDBClient) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

// coordinatorClient adapts an in-process Coordinator for embedded execution and hermetic testing.
type coordinatorClient struct {
	coord *engine.Coordinator
}

func NewCoordinatorClient(coord *engine.Coordinator) DBClient {
	return &coordinatorClient{coord: coord}
}

func (c *coordinatorClient) Query(ctx context.Context, query string) ([]map[string]any, error) {
	res, err := c.coord.ExecuteSQL(query)
	if err != nil {
		return nil, err
	}
	var normalized []map[string]any
	for _, row := range res.Rows {
		normRow := make(map[string]any, len(row))
		for k, v := range row {
			normRow[strings.ToLower(k)] = v
		}
		normalized = append(normalized, normRow)
	}
	return normalized, nil
}

func (c *coordinatorClient) Exec(ctx context.Context, query string) (int64, error) {
	res, err := c.coord.ExecuteSQL(query)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected, nil
}

func (c *coordinatorClient) Close() error {
	return nil
}

// Worker periodically inspects msdb.dbo.sysmail_mailitems for unsent messages and dispatches them via SMTP.
type Worker struct {
	cfg          Config
	client       DBClient
	sendMailFn   SendMailFunc
	running      atomic.Bool
	logIDCounter atomic.Int64
	stopCh       chan struct{}
	wg           sync.WaitGroup
	mu           sync.Mutex
}

// NewWorker constructs a new DatabaseMail Worker instance.
func NewWorker(cfg Config, client DBClient) *Worker {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 20
	}
	if cfg.SMTPPort <= 0 {
		cfg.SMTPPort = 587
	}

	w := &Worker{
		cfg:    cfg,
		client: client,
		stopCh: make(chan struct{}),
	}
	w.sendMailFn = w.defaultSendMail
	return w
}

// SetSendMailFunc allows overriding the SMTP transmission function (useful for hermetic deterministic testing).
func (w *Worker) SetSendMailFunc(fn SendMailFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sendMailFn = fn
}

// Start spawns the background worker poll loop.
func (w *Worker) Start(ctx context.Context) error {
	if !w.running.CompareAndSwap(false, true) {
		return fmt.Errorf("databasemail worker is already running")
	}

	w.wg.Add(1)
	go w.runLoop(ctx)
	return nil
}

// Stop cleanly terminates the worker background loop.
func (w *Worker) Stop() {
	if w.running.CompareAndSwap(true, false) {
		close(w.stopCh)
		w.wg.Wait()
		if w.client != nil {
			_ = w.client.Close()
		}
	}
}

func (w *Worker) runLoop(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			_, _ = w.ProcessBatch(ctx)
		}
	}
}

// MailItem represents a row fetched from sysmail_mailitems.
type MailItem struct {
	ID                  int64
	ProfileName         string
	Recipients          string
	CopyRecipients      string
	BlindCopyRecipients string
	FromAddress         string
	ReplyTo             string
	Subject             string
	Body                string
	BodyFormat          string
	Importance          string
	Sensitivity         string
	FileAttachments     string
}

// ProcessBatch fetches unsent items from sysmail_mailitems, transmits them, and updates their status.
// Returns the count of processed items.
func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	query := fmt.Sprintf(`SELECT mailitem_id, profile_name, recipients, copy_recipients, blind_copy_recipients,
		from_address, reply_to, subject, body, body_format, importance, sensitivity, file_attachments
		FROM sysmail_mailitems WHERE sent_status = 'unsent'
		ORDER BY mailitem_id ASC LIMIT %d;`, w.cfg.BatchSize)

	rows, err := w.client.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch unsent mail items: %w", err)
	}

	if len(rows) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	sendFn := w.sendMailFn
	w.mu.Unlock()

	processedCount := 0
	for _, row := range rows {
		item := parseMailItem(row)
		if item.ID <= 0 {
			continue
		}

		sendErr := w.dispatchItem(sendFn, item)
		logID := w.nextLogID(ctx)
		if sendErr == nil {
			// Mark as sent
			updateSQL := fmt.Sprintf(`UPDATE sysmail_mailitems 
				SET sent_status = 'sent', sent_date = CURRENT_TIMESTAMP, last_mod_date = CURRENT_TIMESTAMP 
				WHERE mailitem_id = %d;`, item.ID)
			_, _ = w.client.Exec(ctx, updateSQL)

			logSQL := fmt.Sprintf(`INSERT INTO sysmail_log (log_id, mailitem_id, event_type, log_date, description)
				VALUES (%d, %d, 'information', CURRENT_TIMESTAMP, 'Mail item %d sent successfully.');`, logID, item.ID, item.ID)
			_, _ = w.client.Exec(ctx, logSQL)
		} else {
			// Mark as failed
			updateSQL := fmt.Sprintf(`UPDATE sysmail_mailitems 
				SET sent_status = 'failed', last_mod_date = CURRENT_TIMESTAMP 
				WHERE mailitem_id = %d;`, item.ID)
			_, _ = w.client.Exec(ctx, updateSQL)

			desc := strings.ReplaceAll(sendErr.Error(), "'", "''")
			logSQL := fmt.Sprintf(`INSERT INTO sysmail_log (log_id, mailitem_id, event_type, log_date, description)
				VALUES (%d, %d, 'error', CURRENT_TIMESTAMP, 'Transmission failed: %s');`, logID, item.ID, desc)
			_, _ = w.client.Exec(ctx, logSQL)
		}
		processedCount++
	}

	return processedCount, nil
}

func (w *Worker) dispatchItem(sendFn SendMailFunc, item MailItem) error {
	from := item.FromAddress
	if from == "" {
		from = w.cfg.FromEmail
	}

	toRecipients := parseEmailList(item.Recipients)
	ccRecipients := parseEmailList(item.CopyRecipients)
	bccRecipients := parseEmailList(item.BlindCopyRecipients)

	var allDestinations []string
	allDestinations = append(allDestinations, toRecipients...)
	allDestinations = append(allDestinations, ccRecipients...)
	allDestinations = append(allDestinations, bccRecipients...)

	if len(allDestinations) == 0 {
		return fmt.Errorf("no valid recipients found for mail item %d", item.ID)
	}

	bodyFormat := strings.ToUpper(item.BodyFormat)
	contentType := "text/plain; charset=UTF-8"
	if bodyFormat == "HTML" {
		contentType = "text/html; charset=UTF-8"
	}

	var msgBuf bytes.Buffer
	if w.cfg.FromName != "" {
		msgBuf.WriteString(fmt.Sprintf("From: %s <%s>\r\n", w.cfg.FromName, from))
	} else {
		msgBuf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	}
	if len(toRecipients) > 0 {
		msgBuf.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(toRecipients, ", ")))
	}
	if len(ccRecipients) > 0 {
		msgBuf.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(ccRecipients, ", ")))
	}
	if item.ReplyTo != "" {
		msgBuf.WriteString(fmt.Sprintf("Reply-To: %s\r\n", item.ReplyTo))
	}
	msgBuf.WriteString(fmt.Sprintf("Subject: %s\r\n", item.Subject))
	msgBuf.WriteString("MIME-Version: 1.0\r\n")
	msgBuf.WriteString(fmt.Sprintf("Content-Type: %s\r\n", contentType))
	msgBuf.WriteString("\r\n")
	msgBuf.WriteString(item.Body)

	var auth smtp.Auth
	if w.cfg.SMTPUser != "" && w.cfg.SMTPPass != "" {
		auth = smtp.PlainAuth("", w.cfg.SMTPUser, w.cfg.SMTPPass, w.cfg.SMTPHost)
	}

	addr := fmt.Sprintf("%s:%d", w.cfg.SMTPHost, w.cfg.SMTPPort)
	return sendFn(addr, auth, from, allDestinations, msgBuf.Bytes())
}

func (w *Worker) defaultSendMail(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	if !w.cfg.UseTLS {
		return smtp.SendMail(addr, a, from, to, msg)
	}

	host := w.cfg.SMTPHost
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial failed: %w", err)
	}
	defer client.Close()

	tlsConfig := &tls.Config{
		ServerName: host,
	}

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("starttls failed: %w", err)
		}
	}

	if a != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(a); err != nil {
				return fmt.Errorf("smtp auth failed: %w", err)
			}
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mail from failed: %w", err)
	}
	for _, addr := range to {
		if err := client.Rcpt(addr); err != nil {
			return fmt.Errorf("rcpt to failed for %s: %w", addr, err)
		}
	}

	wData, err := client.Data()
	if err != nil {
		return fmt.Errorf("data command failed: %w", err)
	}
	if _, err := wData.Write(msg); err != nil {
		return fmt.Errorf("writing body failed: %w", err)
	}
	if err := wData.Close(); err != nil {
		return fmt.Errorf("closing data writer failed: %w", err)
	}

	return client.Quit()
}

func (w *Worker) nextLogID(ctx context.Context) int64 {
	if w.logIDCounter.Load() == 0 {
		if rows, err := w.client.Query(ctx, "SELECT MAX(log_id) AS max_id FROM sysmail_log;"); err == nil && len(rows) > 0 {
			w.logIDCounter.Store(toInt64(rows[0]["max_id"]))
		}
	}
	return w.logIDCounter.Add(1)
}

func parseMailItem(row map[string]any) MailItem {
	var item MailItem
	if idVal, ok := row["mailitem_id"]; ok {
		item.ID = toInt64(idVal)
	}
	item.ProfileName = toString(row["profile_name"])
	item.Recipients = toString(row["recipients"])
	item.CopyRecipients = toString(row["copy_recipients"])
	item.BlindCopyRecipients = toString(row["blind_copy_recipients"])
	item.FromAddress = toString(row["from_address"])
	item.ReplyTo = toString(row["reply_to"])
	item.Subject = toString(row["subject"])
	item.Body = toString(row["body"])
	item.BodyFormat = toString(row["body_format"])
	item.Importance = toString(row["importance"])
	item.Sensitivity = toString(row["sensitivity"])
	item.FileAttachments = toString(row["file_attachments"])
	return item
}

func parseEmailList(s string) []string {
	var list []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ';' || r == ','
	}) {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			list = append(list, trimmed)
		}
	}
	return list
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case string:
		if val, err := strconv.ParseInt(n, 10, 64); err == nil {
			return val
		}
	}
	return 0
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
