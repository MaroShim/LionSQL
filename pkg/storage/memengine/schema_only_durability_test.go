package memengine

import (
	"testing"

	"lionsql/pkg/storage"
)

func countRows(cur storage.Cursor) int {
	if cur == nil {
		return 0
	}
	defer cur.Close()
	count := 0
	for cur.Next() {
		count++
	}
	return count
}

func TestMemengine_SchemaOnlyDurability_ZeroRowsOnRestart(t *testing.T) {
	tempDir := t.TempDir()

	opts := Options{
		DataDir:      tempDir,
		EnableAOF:    true,
		EnableRDB:    true,
		Mode:         MemoryCompact,
		AOFThreshold: 0, // disable auto-rewrite during test
		FsyncPolicy:  FsyncAlways,
	}

	// 1. Initial engine start
	eng := NewWithOptions(opts)
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Create persistent table (SCHEMA_AND_DATA - default)
	persistentSchema := &storage.TableSchema{
		Name:       "users",
		Durability: storage.DurabilitySchemaAndData,
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(persistentSchema); err != nil {
		t.Fatalf("CreateTable(users) failed: %v", err)
	}

	// Create ephemeral table (SCHEMA_ONLY)
	schemaOnlyTable := &storage.TableSchema{
		Name:            "sessions",
		Durability:      storage.DurabilitySchemaOnly,
		MemoryOptimized: true,
		Columns: []storage.Column{
			{Name: "token", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_sessions_user", TableName: "sessions", Columns: []string{"user_id"}},
		},
	}
	if err := eng.CreateTable(schemaOnlyTable); err != nil {
		t.Fatalf("CreateTable(sessions) failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// Insert into persistent table
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if err := tx1.Insert("users", storage.Record{"id": storage.IntValue(int64(i)), "name": storage.StringValue("user")}); err != nil {
			t.Fatalf("Insert users failed: %v", err)
		}
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit users failed: %v", err)
	}

	// Insert into schema-only table
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := 1; i <= 5; i++ {
		tok := "tok_" + string(rune('0'+i))
		if err := tx2.Insert("sessions", storage.Record{"token": storage.StringValue(tok), "user_id": storage.IntValue(int64(i))}); err != nil {
			t.Fatalf("Insert sessions failed: %v", err)
		}
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit sessions failed: %v", err)
	}

	// Verify both tables have rows before restart
	txRead, _ := sess.BeginTx(storage.TxOptions{})
	curUsers, err := txRead.Scan("users")
	if err != nil {
		t.Fatalf("Scan users before restart failed: %v", err)
	}
	if n := countRows(curUsers); n != 5 {
		t.Fatalf("expected 5 users before restart, got %d", n)
	}

	curSessions, err := txRead.Scan("sessions")
	if err != nil {
		t.Fatalf("Scan sessions before restart failed: %v", err)
	}
	if n := countRows(curSessions); n != 5 {
		t.Fatalf("expected 5 sessions before restart, got %d", n)
	}
	_ = txRead.Rollback()

	// Graceful engine close
	if err := eng.Close(); err != nil {
		t.Fatalf("eng.Close failed: %v", err)
	}

	// 2. Restart engine from same directory
	eng2 := NewWithOptions(opts)
	if err := eng2.Open(tempDir); err != nil {
		t.Fatalf("Open (restart) failed: %v", err)
	}
	defer eng2.Close()

	// Verify schemas exist for both tables
	tables, err := eng2.ListTables()
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}
	var hasUsers, hasSessions bool
	for _, s := range tables {
		if s.Name == "users" {
			hasUsers = true
		}
		if s.Name == "sessions" {
			hasSessions = true
		}
	}
	if !hasUsers {
		t.Fatalf("expected table 'users' to persist across restart")
	}
	if !hasSessions {
		t.Fatalf("expected table 'sessions' (SCHEMA_ONLY) schema to persist across restart")
	}

	sess2, err := eng2.NewSession()
	if err != nil {
		t.Fatalf("NewSession (recovered) failed: %v", err)
	}
	defer sess2.Close()

	// Verify persistent table restored all 5 rows
	txRecovered, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx on recovered engine failed: %v", err)
	}
	defer txRecovered.Rollback()

	recCurUsers, err := txRecovered.Scan("users")
	if err != nil {
		t.Fatalf("Scan users failed: %v", err)
	}
	if n := countRows(recCurUsers); n != 5 {
		t.Fatalf("expected 5 users restored, got %d", n)
	}

	// Verify schema-only table restored 0 rows (Empty)!
	recCurSessions, err := txRecovered.Scan("sessions")
	if err != nil {
		t.Fatalf("Scan sessions failed: %v", err)
	}
	if n := countRows(recCurSessions); n != 0 {
		t.Fatalf("expected 0 sessions in SCHEMA_ONLY table after restart, got %d", n)
	}

	// 3. Verify SCHEMA_ONLY table can immediately receive new rows and index works after restart
	tx3, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx for new insert failed: %v", err)
	}
	if err := tx3.Insert("sessions", storage.Record{"token": storage.StringValue("new_token"), "user_id": storage.IntValue(99)}); err != nil {
		t.Fatalf("Insert into recovered SCHEMA_ONLY table failed: %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("Commit new insert failed: %v", err)
	}

	tx4, _ := sess2.BeginTx(storage.TxOptions{})
	curAfter, err := tx4.Scan("sessions")
	if err != nil {
		t.Fatalf("Scan sessions after new insert failed: %v", err)
	}
	if n := countRows(curAfter); n != 1 {
		t.Fatalf("expected 1 session after new insert, got %d", n)
	}
	_ = tx4.Rollback()
}
