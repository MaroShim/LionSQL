package parser

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"lionsql/pkg/proc"
)

func init() {
	proc.RegisterDiagnosticOracle(DiagnoseSyntax)
}

// DiagnoseSyntax inspects a SQL query that failed in LionSQL's native Pratt parser.
func DiagnoseSyntax(sql string, prattErr error) error {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return prattErr
	}

	// 1. Run official full Microsoft T-SQL ANTLR4 parser
	res, err := ParseTSql(trimmed)
	if err != nil {
		// ANTLR also reported syntax errors: return the first formatted syntax error
		if res != nil && len(res.Errors) > 0 {
			first := res.Errors[0]
			return fmt.Errorf("[42000] Syntax error at line %d:%d: %s", first.Line, first.Column, first.Message)
		}
		return fmt.Errorf("[42000] Syntax error: %w", prattErr)
	}

	// 2. ANTLR parsed the statement with ZERO errors: it is grammatically valid Microsoft T-SQL,
	// but the specific statement or clause is not yet implemented in the current engine release.
	listener := &featureDetectorListener{}
	walker := antlr.NewParseTreeWalker()
	walker.Walk(listener, res.Tree)

	featureName := listener.detectedFeature
	if featureName == "" {
		fields := strings.Fields(trimmed)
		if len(fields) > 0 {
			featureName = strings.ToUpper(fields[0]) + " statement"
		} else {
			featureName = "unsupported T-SQL statement or clause"
		}
	}

	return &proc.FeatureNotSupportedError{
		FeatureName: featureName,
		ContextHint: listener.contextHint,
	}
}

type featureDetectorListener struct {
	BaseTSqlParserListener
	detectedFeature string
	contextHint     string
}

func (l *featureDetectorListener) EnterPivot(ctx *PivotContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "PIVOT clause"
		l.contextHint = "PIVOT is not supported in LionSQL 1.0; use conditional aggregation (SUM(CASE ...)) instead"
	}
}

func (l *featureDetectorListener) EnterPivot_clause(ctx *Pivot_clauseContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "PIVOT clause"
		l.contextHint = "PIVOT is not supported in LionSQL 1.0; use conditional aggregation (SUM(CASE ...)) instead"
	}
}

func (l *featureDetectorListener) EnterUnpivot(ctx *UnpivotContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "UNPIVOT clause"
		l.contextHint = "UNPIVOT is not supported in LionSQL 1.0; use CROSS APPLY (VALUES ...) instead"
	}
}

func (l *featureDetectorListener) EnterUnpivot_clause(ctx *Unpivot_clauseContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "UNPIVOT clause"
		l.contextHint = "UNPIVOT is not supported in LionSQL 1.0; use CROSS APPLY (VALUES ...) instead"
	}
}

func (l *featureDetectorListener) EnterFor_clause(ctx *For_clauseContext) {
	if l.detectedFeature == "" {
		if ctx.XML() != nil {
			l.detectedFeature = "FOR XML clause"
			l.contextHint = "Legacy XML is Spec-Out; use JSON functions (FOR JSON PATH, JSON_VALUE) instead"
		} else if ctx.JSON() != nil {
			l.detectedFeature = "FOR JSON clause"
			l.contextHint = "FOR JSON clause is not supported in LionSQL 1.0"
		} else {
			l.detectedFeature = "FOR clause"
			l.contextHint = "FOR clause is not supported in LionSQL 1.0"
		}
	}
}

func (l *featureDetectorListener) EnterXml_common_directives(ctx *Xml_common_directivesContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "FOR XML clause"
		l.contextHint = "Legacy XML is Spec-Out; use JSON functions (FOR JSON PATH, JSON_VALUE) instead"
	}
}

func (l *featureDetectorListener) EnterOption_clause(ctx *Option_clauseContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "OPTION query hints"
		l.contextHint = "Query hints (e.g. RECOMPILE, MAXDOP, OPTIMIZE FOR) are not supported in 1.0"
	}
}

func (l *featureDetectorListener) EnterTable_hint(ctx *Table_hintContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "Table Hint (WITH ...)"
		l.contextHint = "Locking hints (NOLOCK, HOLDLOCK, READPAST) are unnecessary under OCC/MVCC engines"
	}
}

func (l *featureDetectorListener) EnterOutput_clause(ctx *Output_clauseContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "OUTPUT clause"
		l.contextHint = "OUTPUT clause is not supported; use RETURNING or separate SELECT instead"
	}
}

func (l *featureDetectorListener) EnterDeclare_cursor(ctx *Declare_cursorContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "CURSOR definition"
		l.contextHint = "Advanced/Scrollable cursors are not supported; use set-based operations or FORWARD_ONLY cursors"
	}
}

func (l *featureDetectorListener) EnterTry_catch_statement(ctx *Try_catch_statementContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "TRY...CATCH statement"
		l.contextHint = "TRY...CATCH is supported inside stored procedures, but not in top-level ad-hoc batch queries"
	}
}

func (l *featureDetectorListener) EnterBackup_statement(ctx *Backup_statementContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "BACKUP statement"
		l.contextHint = "use the standalone CLI 'lionsql backup' instead"
	}
}

func (l *featureDetectorListener) EnterThrow_statement(ctx *Throw_statementContext) {
	if l.detectedFeature == "" {
		l.detectedFeature = "THROW statement"
		l.contextHint = "THROW is supported inside stored procedures, but not in top-level ad-hoc batch queries"
	}
}
