package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestRegression_BUG_UPSERT_1_CaseInsensitiveConflict(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// Create table with uppercase column names
	createSQL := `CREATE TABLE users (
		UserID INT PRIMARY KEY,
		Email VARCHAR(100) UNIQUE,
		NickName VARCHAR(50)
	);`
	if _, err := coord.ExecuteSQL(createSQL); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Initial insert
	initSQL := `INSERT INTO users (UserID, Email, NickName) VALUES (1, 'user@test.com', 'Original');`
	if _, err := coord.ExecuteSQL(initSQL); err != nil {
		t.Fatalf("Initial INSERT failed: %v", err)
	}

	// 1. Upsert with lowercase conflict column: ON CONFLICT (email) matching Email
	upsertSQL1 := `INSERT INTO users (UserID, Email, NickName) VALUES (2, 'user@test.com', 'Updated1')
		ON CONFLICT (email) DO UPDATE SET NickName = 'Updated1';`
	if _, err := coord.ExecuteSQL(upsertSQL1); err != nil {
		t.Fatalf("UPSERT with lowercase conflict col failed: %v", err)
	}

	selRes1, err := coord.ExecuteSQL("SELECT NickName FROM users WHERE UserID = 1;")
	if err != nil || len(selRes1.Rows) != 1 {
		t.Fatalf("SELECT 1 failed: %v", err)
	}
	if selRes1.Rows[0]["NickName"] != "Updated1" {
		t.Fatalf("expected NickName 'Updated1', got %v", selRes1.Rows[0]["NickName"])
	}

	// 2. Upsert with lowercase PK conflict column: ON CONFLICT (userid) matching UserID
	upsertSQL2 := `INSERT INTO users (UserID, Email, NickName) VALUES (1, 'newuser@test.com', 'Updated2')
		ON CONFLICT (userid) DO UPDATE SET NickName = 'Updated2';`
	if _, err := coord.ExecuteSQL(upsertSQL2); err != nil {
		t.Fatalf("UPSERT with lowercase PK conflict col failed: %v", err)
	}

	selRes2, err := coord.ExecuteSQL("SELECT NickName FROM users WHERE UserID = 1;")
	if err != nil || len(selRes2.Rows) != 1 {
		t.Fatalf("SELECT 2 failed: %v", err)
	}
	if selRes2.Rows[0]["NickName"] != "Updated2" {
		t.Fatalf("expected NickName 'Updated2', got %v", selRes2.Rows[0]["NickName"])
	}
}
