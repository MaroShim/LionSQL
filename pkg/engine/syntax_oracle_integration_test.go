package engine_test

import (
	"errors"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/proc"
	_ "lionsql/pkg/proc/parser/antlr"
	"lionsql/pkg/storage/memengine"
)

func TestSyntaxOracle_Integration(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	// Pre-create table for query validation
	_, err := coord.ExecuteSQL("CREATE TABLE Customers (id INT, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("setup Customers failed: %v", err)
	}

	t.Run("FOR XML Unsupported Clause Diagnosis", func(t *testing.T) {
		sql := `SELECT id, name FROM Customers FOR XML RAW;`
		_, err := coord.ExecuteSQL(sql)
		if err == nil {
			t.Fatalf("expected error for unsupported FOR XML clause, got nil")
		}

		var notSupported *proc.FeatureNotSupportedError
		if !errors.As(err, &notSupported) {
			t.Fatalf("expected *proc.FeatureNotSupportedError, got %T: %v", err, err)
		}
		if notSupported.FeatureName != "FOR XML clause" {
			t.Errorf("expected FeatureName 'FOR XML clause', got %q", notSupported.FeatureName)
		}
		if !strings.Contains(err.Error(), "[42000]") {
			t.Errorf("expected [42000] SQL state, got %q", err.Error())
		}
	})

	t.Run("BACKUP Statement Unsupported in T-SQL Diagnosis", func(t *testing.T) {
		sql := `BACKUP DATABASE SalesDB TO DISK = 'sales.bak';`
		_, err := coord.ExecuteSQL(sql)
		if err == nil {
			t.Fatalf("expected error for unsupported BACKUP statement, got nil")
		}

		var notSupported *proc.FeatureNotSupportedError
		if !errors.As(err, &notSupported) {
			t.Fatalf("expected *proc.FeatureNotSupportedError, got %T: %v", err, err)
		}
		if notSupported.FeatureName != "BACKUP statement" {
			t.Errorf("expected FeatureName 'BACKUP statement', got %q", notSupported.FeatureName)
		}
		if !strings.Contains(err.Error(), "[42000]") {
			t.Errorf("expected [42000] SQL state, got %q", err.Error())
		}
	})

	t.Run("True Syntax Error Line Col", func(t *testing.T) {
		sql := `SELEC * FROM Customers;`
		_, err := coord.ExecuteSQL(sql)
		if err == nil {
			t.Fatalf("expected error for typo, got nil")
		}

		var notSupported *proc.FeatureNotSupportedError
		if errors.As(err, &notSupported) {
			t.Fatalf("did not expect FeatureNotSupportedError for typo, got: %v", err)
		}

		if !strings.Contains(err.Error(), "[42000]") {
			t.Errorf("expected [42000] error prefix, got %q", err.Error())
		}
		if !strings.Contains(err.Error(), "line 1:") {
			t.Errorf("expected line 1 info, got %q", err.Error())
		}
	})

	t.Run("Normal Valid SQL Unaffected", func(t *testing.T) {
		res, err := coord.ExecuteSQL("SELECT 42 AS answer;")
		if err != nil {
			t.Fatalf("unexpected error for normal query: %v", err)
		}
		if len(res.Rows) != 1 || res.Rows[0]["answer"] != int64(42) {
			t.Errorf("unexpected result: %+v", res.Rows)
		}
	})
}
