//go:build cgo && wiredtiger

package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/wiredtiger"
)

func TestTransactionalTruncate_WiredTiger_Rollback(t *testing.T) {
	wtDir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(wtDir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE wt_tx_trunc (id INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO wt_tx_trunc VALUES (1, 'One'), (2, 'Two');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	sessID := "wt-tx-trunc-sess"
	if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN TRANSACTION failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "TRUNCATE TABLE wt_tx_trunc;"); err != nil {
		t.Fatalf("TRUNCATE TABLE failed: %v", err)
	}

	resInside, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM wt_tx_trunc;")
	if err != nil {
		t.Fatalf("SELECT inside tx failed: %v", err)
	}
	if fmt.Sprintf("%v", resInside.Rows[0]["c"]) != "0" {
		t.Fatalf("expected 0 inside tx, got %v", resInside.Rows[0]["c"])
	}

	if _, err := coord.ExecuteSessionSQL(sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	resAfter, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM wt_tx_trunc;")
	if err != nil {
		t.Fatalf("SELECT after rollback failed: %v", err)
	}
	if fmt.Sprintf("%v", resAfter.Rows[0]["c"]) != "2" {
		t.Fatalf("expected 2 restored rows after ROLLBACK, got %v", resAfter.Rows[0]["c"])
	}
}
