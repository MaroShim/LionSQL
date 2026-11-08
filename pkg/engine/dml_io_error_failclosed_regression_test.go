package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// faultyCursor wraps a real storage.Cursor and injects an I/O read failure on the Nth Record() call.
type faultyCursor struct {
	realCur     storage.Cursor
	failOnIndex int
	curIndex    int
}

func (fc *faultyCursor) Next() bool {
	return fc.realCur.Next()
}

func (fc *faultyCursor) Record() (storage.Record, error) {
	fc.curIndex++
	if fc.curIndex == fc.failOnIndex {
		return nil, errors.New("simulated disk sector read error during scan")
	}
	return fc.realCur.Record()
}

func (fc *faultyCursor) Row() (storage.Row, error) {
	fc.curIndex++
	if fc.curIndex == fc.failOnIndex {
		return storage.Row{}, errors.New("simulated disk sector read error during scan")
	}
	return fc.realCur.Row()
}

func (fc *faultyCursor) Close() error {
	return fc.realCur.Close()
}

// faultyTx wraps a real storage.Transaction and intercepts Scan() to inject faultyCursor.
type faultyTx struct {
	storage.Transaction
	targetTable string
	failOnIndex int
	scanActive  bool
}

func (ft *faultyTx) Scan(table string) (storage.Cursor, error) {
	cur, err := ft.Transaction.Scan(table)
	if err != nil {
		return nil, err
	}
	if ft.scanActive && strings.EqualFold(table, ft.targetTable) {
		return &faultyCursor{realCur: cur, failOnIndex: ft.failOnIndex}, nil
	}
	return cur, nil
}

// faultySession wraps a real storage.Session.
type faultySession struct {
	storage.Session
	ft *faultyTx
}

func (fs *faultySession) BeginTx(opts storage.TxOptions) (storage.Transaction, error) {
	tx, err := fs.Session.BeginTx(opts)
	if err != nil {
		return nil, err
	}
	fs.ft.Transaction = tx
	return fs.ft, nil
}

// faultyEngine intercepts NewSession to return faultySession.
type faultyEngine struct {
	storage.Engine
	fs *faultySession
}

func (fe *faultyEngine) NewSession() (storage.Session, error) {
	sess, err := fe.Engine.NewSession()
	if err != nil {
		return nil, err
	}
	fe.fs.Session = sess
	return fe.fs, nil
}

// TestDML_Regression_P0_19_IOErrorFailClosed verifies that when a storage cursor encounters
// an I/O error mid-scan during UPDATE or DELETE:
// 1. The operation fails closed immediately with an explicit diagnostic error ([RULE:ExplicitError]).
// 2. The transaction is safely rolled back ([RULE:AtomicSaves]).
// 3. Zero partial mutations are committed to the database.
func TestDML_Regression_P0_19_IOErrorFailClosed(t *testing.T) {
	baseEng := memengine.New()
	ftx := &faultyTx{targetTable: "dml_fail_tbl", failOnIndex: 3, scanActive: false}
	fs := &faultySession{ft: ftx}
	fe := &faultyEngine{Engine: baseEng, fs: fs}

	cat := catalog.New(fe)
	coord := NewCoordinator(cat, fe, Config{})
	defer coord.Close()

	// Create table and insert 5 rows
	ddl := `
	CREATE TABLE dml_fail_tbl (
		id INT PRIMARY KEY,
		val VARCHAR(50) NOT NULL
	);
	`
	if _, err := coord.ExecuteSQL(ddl); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	insertSQL := `
	INSERT INTO dml_fail_tbl (id, val) VALUES
	(1, 'orig_1'),
	(2, 'orig_2'),
	(3, 'orig_3'),
	(4, 'orig_4'),
	(5, 'orig_5');
	`
	if _, err := coord.ExecuteSQL(insertSQL); err != nil {
		t.Fatalf("insert rows failed: %v", err)
	}

	// -------------------------------------------------------------------------
	// Scenario 1: UPDATE mid-scan failure -> Fail-Closed & Zero Partial Commit
	// -------------------------------------------------------------------------
	t.Run("Update_FailClosed_NoPartialCommit", func(t *testing.T) {
		ftx.scanActive = true
		ftx.failOnIndex = 3 // Fail on 3rd row read

		updateSQL := "UPDATE dml_fail_tbl SET val = 'HACKED_VAL'"
		_, dmlErr := coord.ExecuteSQL(updateSQL)
		if dmlErr == nil {
			t.Fatalf("[P0-19 DEFECT] UPDATE succeeded despite mid-scan storage I/O error!")
		}
		if !strings.Contains(dmlErr.Error(), "simulated disk sector read error") {
			t.Fatalf("[P0-19 DEFECT] Expected simulated read error, got: %v", dmlErr)
		}

		ftx.scanActive = false // Disable fault injection to inspect state

		// Verify that none of the rows (especially rows 1 and 2 read before the failure) were updated
		sel, err := coord.ExecuteSQL("SELECT id, val FROM dml_fail_tbl ORDER BY id")
		if err != nil {
			t.Fatalf("SELECT failed: %v", err)
		}
		if len(sel.Rows) != 5 {
			t.Fatalf("expected 5 rows, got %d", len(sel.Rows))
		}
		for i, r := range sel.Rows {
			expectedVal := fmt.Sprintf("orig_%d", i+1)
			if r["val"] != expectedVal {
				t.Fatalf("[P0-19 DEFECT] Partial commit detected! Row %d has val=%q, expected=%q", i+1, r["val"], expectedVal)
			}
		}

		t.Logf("[P0-19 PASS] UPDATE failed closed cleanly with zero partial commit: %v", dmlErr)
	})

	// -------------------------------------------------------------------------
	// Scenario 2: DELETE mid-scan failure -> Fail-Closed & Zero Partial Deletion
	// -------------------------------------------------------------------------
	t.Run("Delete_FailClosed_NoPartialDeletion", func(t *testing.T) {
		ftx.scanActive = true
		ftx.failOnIndex = 3 // Fail on 3rd row read

		deleteSQL := "DELETE FROM dml_fail_tbl"
		_, dmlErr := coord.ExecuteSQL(deleteSQL)
		if dmlErr == nil {
			t.Fatalf("[P0-19 DEFECT] DELETE succeeded despite mid-scan storage I/O error!")
		}
		if !strings.Contains(dmlErr.Error(), "simulated disk sector read error") {
			t.Fatalf("[P0-19 DEFECT] Expected simulated read error, got: %v", dmlErr)
		}

		ftx.scanActive = false // Disable fault injection to inspect state

		// Verify that none of the 5 rows were deleted
		sel, err := coord.ExecuteSQL("SELECT id, val FROM dml_fail_tbl ORDER BY id")
		if err != nil {
			t.Fatalf("SELECT failed: %v", err)
		}
		if len(sel.Rows) != 5 {
			t.Fatalf("[P0-19 DEFECT] Partial deletion detected! Expected 5 rows remaining, but found %d", len(sel.Rows))
		}
		for i, r := range sel.Rows {
			expectedVal := fmt.Sprintf("orig_%d", i+1)
			if r["val"] != expectedVal {
				t.Fatalf("[P0-19 DEFECT] Data corruption in row %d: val=%q", i+1, r["val"])
			}
		}

		t.Logf("[P0-19 PASS] DELETE failed closed cleanly with zero partial deletion: %v", dmlErr)
	})
}

// Unused context import suppression
var _ = context.Background
