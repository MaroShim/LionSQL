package parser

import (
	"errors"
	"strings"
	"testing"

	"lionsql/pkg/proc"
)

func TestDiagnoseSyntax_PivotDetection(t *testing.T) {
	sql := `SELECT * FROM Sales.Orders PIVOT (SUM(Amount) FOR Month IN ([Jan], [Feb])) AS p;`
	prattErr := errors.New("expected projection expression, got PIVOT")

	err := proc.DiagnoseParseFailure(sql, prattErr)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	var notSupported *proc.FeatureNotSupportedError
	if !errors.As(err, &notSupported) {
		t.Fatalf("expected *proc.FeatureNotSupportedError, got %T: %v", err, err)
	}

	if notSupported.FeatureName != "PIVOT clause" {
		t.Errorf("expected FeatureName 'PIVOT clause', got %q", notSupported.FeatureName)
	}
	if !strings.Contains(notSupported.Error(), "[42000]") {
		t.Errorf("expected [42000] SQL state, got %q", notSupported.Error())
	}
}

func TestDiagnoseSyntax_XmlDetection(t *testing.T) {
	sql := `SELECT id, name FROM Customers FOR XML RAW;`
	prattErr := errors.New("unexpected token FOR")

	err := proc.DiagnoseParseFailure(sql, prattErr)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	var notSupported *proc.FeatureNotSupportedError
	if !errors.As(err, &notSupported) {
		t.Fatalf("expected *proc.FeatureNotSupportedError, got %T: %v", err, err)
	}

	if notSupported.FeatureName != "FOR XML clause" {
		t.Errorf("expected FeatureName 'FOR XML clause', got %q", notSupported.FeatureName)
	}
	if !strings.Contains(notSupported.ContextHint, "JSON") {
		t.Errorf("expected JSON hint, got %q", notSupported.ContextHint)
	}
}

func TestDiagnoseSyntax_OptionHintsDetection(t *testing.T) {
	sql := `SELECT id FROM Employees OPTION (RECOMPILE, MAXDOP 2);`
	prattErr := errors.New("unexpected token OPTION")

	err := proc.DiagnoseParseFailure(sql, prattErr)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	var notSupported *proc.FeatureNotSupportedError
	if !errors.As(err, &notSupported) {
		t.Fatalf("expected *proc.FeatureNotSupportedError, got %T: %v", err, err)
	}

	if notSupported.FeatureName != "OPTION query hints" {
		t.Errorf("expected FeatureName 'OPTION query hints', got %q", notSupported.FeatureName)
	}
}

func TestDiagnoseSyntax_SyntaxErrorWithLineCol(t *testing.T) {
	sql := `SELEC * FROM Customers;`
	prattErr := errors.New("syntax error at or near SELEC")

	err := proc.DiagnoseParseFailure(sql, prattErr)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	// Should NOT be FeatureNotSupportedError
	var notSupported *proc.FeatureNotSupportedError
	if errors.As(err, &notSupported) {
		t.Fatalf("did not expect FeatureNotSupportedError for typo, got: %v", err)
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "[42000]") {
		t.Errorf("expected [42000] prefix, got %q", errMsg)
	}
	if !strings.Contains(errMsg, "line 1:") {
		t.Errorf("expected line 1 info, got %q", errMsg)
	}
}
