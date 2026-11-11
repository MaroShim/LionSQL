package proc

import (
	"fmt"
	"sync"
)

// FeatureNotSupportedError indicates that the SQL is grammatically valid Microsoft T-SQL,
// but the specific statement, clause, or feature is not yet implemented in the current engine release.
type FeatureNotSupportedError struct {
	FeatureName string
	Line        int
	Column      int
	ContextHint string
}

func (e *FeatureNotSupportedError) Error() string {
	if e.ContextHint != "" {
		return fmt.Sprintf("[42000] Valid T-SQL syntax recognized ('%s'), but this feature is not yet supported in LionSQL 1.0 release (%s)", e.FeatureName, e.ContextHint)
	}
	return fmt.Sprintf("[42000] Valid T-SQL syntax recognized ('%s'), but this feature is not yet supported in LionSQL 1.0 release (planned for post-1.0)", e.FeatureName)
}

// SyntaxDiagnosticOracle defines the function signature for an external syntax oracle
// (such as official ANTLR4 Microsoft T-SQL grammar) that diagnoses parsing failures.
type SyntaxDiagnosticOracle func(sql string, prattErr error) error

var (
	diagnosticOracleMu sync.RWMutex
	diagnosticOracle   SyntaxDiagnosticOracle
)

// RegisterDiagnosticOracle registers a pluggable syntax diagnostic oracle.
func RegisterDiagnosticOracle(oracle SyntaxDiagnosticOracle) {
	diagnosticOracleMu.Lock()
	defer diagnosticOracleMu.Unlock()
	diagnosticOracle = oracle
}

// DiagnoseParseFailure invokes the registered syntax diagnostic oracle if available,
// or returns the original parsing error unchanged.
func DiagnoseParseFailure(sql string, prattErr error) error {
	if prattErr == nil {
		return nil
	}
	diagnosticOracleMu.RLock()
	oracle := diagnosticOracle
	diagnosticOracleMu.RUnlock()

	if oracle == nil {
		return prattErr
	}
	return oracle(sql, prattErr)
}
