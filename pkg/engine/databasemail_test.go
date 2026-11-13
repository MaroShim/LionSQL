package engine

import (
	"strings"
	"testing"
)

func TestDatabaseMail_EnsureTables(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	if err := coord.EnsureDatabaseMailTables(); err != nil {
		t.Fatalf("first EnsureDatabaseMailTables failed: %v", err)
	}

	// Idempotency: calling again should succeed without error
	if err := coord.EnsureDatabaseMailTables(); err != nil {
		t.Fatalf("second EnsureDatabaseMailTables failed: %v", err)
	}

	// Verify tables are queryable
	res, err := coord.ExecuteSQL("SELECT * FROM msdb.dbo.sysmail_mailitems;")
	if err != nil {
		t.Fatalf("query sysmail_mailitems failed: %v", err)
	}
	if len(res.Columns) == 0 {
		t.Fatalf("expected columns in sysmail_mailitems, got 0")
	}

	resLog, err := coord.ExecuteSQL("SELECT * FROM msdb.dbo.sysmail_log;")
	if err != nil {
		t.Fatalf("query sysmail_log failed: %v", err)
	}
	if len(resLog.Columns) == 0 {
		t.Fatalf("expected columns in sysmail_log, got 0")
	}
}

func TestDatabaseMail_SpSendDbMail_Basic(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	res, err := coord.ExecuteSQL(`EXEC msdb.dbo.sp_send_dbmail 
		@profile_name = 'TestProfile',
		@recipients = 'alice@example.com',
		@subject = 'Hello LionSQL',
		@body = 'This is a test body',
		@body_format = 'TEXT';`)
	if err != nil {
		t.Fatalf("sp_send_dbmail failed: %v", err)
	}

	if !res.Success {
		t.Fatalf("expected Success=true, got false")
	}
	if !res.HasReturnStatus || res.ReturnStatus != 0 {
		t.Fatalf("expected ReturnStatus=0, got %d (has=%v)", res.ReturnStatus, res.HasReturnStatus)
	}
	if len(res.Messages) == 0 || !strings.Contains(res.Messages[0], "Mail (Id: 1) queued.") {
		t.Fatalf("expected message 'Mail (Id: 1) queued.', got %v", res.Messages)
	}

	// Verify row in table
	checkRes, err := coord.ExecuteSQL("SELECT mailitem_id, profile_name, recipients, subject, body, body_format, sent_status FROM sysmail_mailitems WHERE mailitem_id = 1;")
	if err != nil {
		t.Fatalf("query queued mail failed: %v", err)
	}
	if len(checkRes.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(checkRes.Rows))
	}
	row := checkRes.Rows[0]
	if row["recipients"] != "alice@example.com" {
		t.Errorf("expected recipients 'alice@example.com', got %v", row["recipients"])
	}
	if row["subject"] != "Hello LionSQL" {
		t.Errorf("expected subject 'Hello LionSQL', got %v", row["subject"])
	}
	if row["body"] != "This is a test body" {
		t.Errorf("expected body 'This is a test body', got %v", row["body"])
	}
	if row["sent_status"] != "unsent" {
		t.Errorf("expected sent_status 'unsent', got %v", row["sent_status"])
	}
	if row["body_format"] != "TEXT" {
		t.Errorf("expected body_format 'TEXT', got %v", row["body_format"])
	}
}

func TestDatabaseMail_SpSendDbMail_MissingRecipientsError(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`EXEC msdb.dbo.sp_send_dbmail @subject = 'Missing Recipient';`)
	if err == nil {
		t.Fatalf("expected error for missing recipients, got nil")
	}
	if !strings.Contains(err.Error(), "Msg 14618") {
		t.Fatalf("expected error Msg 14618, got: %v", err)
	}
}

func TestDatabaseMail_SpSendDbMail_InvalidBodyFormat(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`EXEC msdb.dbo.sp_send_dbmail @recipients = 'bob@example.com', @body_format = 'XML';`)
	if err == nil {
		t.Fatalf("expected error for invalid body_format, got nil")
	}
	if !strings.Contains(err.Error(), "Msg 14626") {
		t.Fatalf("expected error Msg 14626, got: %v", err)
	}
}

func TestDatabaseMail_SpSendDbMail_OutputMailItemId(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sessID := "sess-dbmail-test"
	cs, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// Execute with output parameter @mailitem_id = @id OUTPUT
	res, err := coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_send_dbmail 
		@recipients = 'charlie@example.com', 
		@subject = 'Output Test',
		@mailitem_id = @id OUTPUT;`)
	if err != nil {
		t.Fatalf("sp_send_dbmail with output param failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got failure")
	}

	cs.Mu.Lock()
	val, ok := cs.Vars["@id"]
	if !ok {
		val, ok = cs.Vars["id"]
	}
	cs.Mu.Unlock()

	if !ok || val == nil {
		t.Fatalf("expected @id to be set in session vars, got: %v", cs.Vars)
	}
	var idInt int64
	switch n := val.(type) {
	case int:
		idInt = int64(n)
	case int64:
		idInt = n
	default:
		t.Fatalf("unexpected type for @id: %T (%v)", val, val)
	}
	if idInt != 1 {
		t.Fatalf("expected @id = 1, got %d", idInt)
	}
}

func TestDatabaseMail_SpSendDbMail_TransactionRollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sessID := "sess-dbmail-tx"

	_, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	if err != nil {
		t.Fatalf("BEGIN TRAN failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL(sessID, `EXEC msdb.dbo.sp_send_dbmail 
		@recipients = 'dave@example.com', 
		@subject = 'Tx Rollback Test';`)
	if err != nil {
		t.Fatalf("sp_send_dbmail inside tx failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL(sessID, "ROLLBACK TRANSACTION;")
	if err != nil {
		t.Fatalf("ROLLBACK TRAN failed: %v", err)
	}

	// Verify the item was rolled back
	checkRes, err := coord.ExecuteSessionSQL(sessID, "SELECT * FROM sysmail_mailitems WHERE recipients = 'dave@example.com';")
	if err != nil {
		t.Fatalf("query sysmail_mailitems failed: %v", err)
	}
	if len(checkRes.Rows) != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", len(checkRes.Rows))
	}
}

func TestDatabaseMail_SpSendDbMail_Syntaxes(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	syntaxes := []string{
		`EXEC msdb.dbo.sp_send_dbmail @recipients = 's1@example.com', @subject = 'S1';`,
		`EXEC dbo.sp_send_dbmail @recipients = 's2@example.com', @subject = 'S2';`,
		`EXEC sp_send_dbmail @recipients = 's3@example.com', @subject = 'S3';`,
		`EXEC [msdb].[dbo].[sp_send_dbmail] @recipients = 's4@example.com', @subject = 'S4';`,
	}

	for i, q := range syntaxes {
		res, err := coord.ExecuteSQL(q)
		if err != nil {
			t.Fatalf("syntax %d failed (%s): %v", i+1, q, err)
		}
		if !res.Success {
			t.Fatalf("syntax %d expected Success=true", i+1)
		}
	}

	checkRes, err := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM sysmail_mailitems;")
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if len(checkRes.Rows) == 0 {
		t.Fatalf("expected row in count result")
	}
}
