package slt

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"lionsql/pkg/engine"
)

// Executor represents any database execution engine capable of running SQL and returning tabular results.
type Executor interface {
	Execute(sql string) (columns []string, rows []map[string]any, err error)
}

// CoordinatorExecutor wraps an in-process LionSQL Coordinator as an SLT Executor.
type CoordinatorExecutor struct {
	coord *engine.Coordinator
}

// NewCoordinatorExecutor creates an in-process Executor wrapping coord.
func NewCoordinatorExecutor(coord *engine.Coordinator) *CoordinatorExecutor {
	return &CoordinatorExecutor{coord: coord}
}

// Execute runs SQL via the in-process coordinator.
func (e *CoordinatorExecutor) Execute(sql string) ([]string, []map[string]any, error) {
	res, err := e.coord.ExecuteSQL(sql)
	if err != nil {
		return nil, nil, err
	}
	if !res.Success {
		errMsg := "query failed"
		if len(res.Messages) > 0 {
			errMsg = strings.Join(res.Messages, "; ")
		}
		return nil, nil, fmt.Errorf("%s", errMsg)
	}
	return res.Columns, res.Rows, nil
}

// FormatCell formats a raw database value into a standardized SLT text string.
func FormatCell(val any, colType byte) string {
	if val == nil {
		return "NULL"
	}

	switch v := val.(type) {
	case bool:
		if colType == 'I' || colType == 'i' {
			if v {
				return "1"
			}
			return "0"
		}
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "NULL"
		}
		// SLT rounds floats or displays standard representation
		if v == math.Trunc(v) {
			return fmt.Sprintf("%.0f", v)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "NULL"
		}
		if f == math.Trunc(f) {
			return fmt.Sprintf("%.0f", f)
		}
		return strconv.FormatFloat(f, 'f', -1, 32)
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// RunRecord executes a single Record against executor and verifies the outcome.
// Returns an error if verification fails.
func RunRecord(exec Executor, rec Record) error {
	switch rec.Type {
	case CmdHalt:
		return nil

	case CmdStatementOk:
		_, _, err := exec.Execute(rec.SQL)
		if err != nil {
			return fmt.Errorf("[%s:%d] statement ok failed: %w\nSQL: %s", rec.FilePath, rec.LineNumber, err, rec.SQL)
		}
		return nil

	case CmdStatementError:
		_, _, err := exec.Execute(rec.SQL)
		if err == nil {
			return fmt.Errorf("[%s:%d] statement error expected failure, but succeeded\nSQL: %s", rec.FilePath, rec.LineNumber, rec.SQL)
		}
		return nil

	case CmdQuery:
		cols, rows, err := exec.Execute(rec.SQL)
		if err != nil {
			return fmt.Errorf("[%s:%d] query execution failed: %w\nSQL: %s", rec.FilePath, rec.LineNumber, err, rec.SQL)
		}

		numCols := len(cols)
		if numCols == 0 && len(rec.TypeSignature) > 0 {
			numCols = len(rec.TypeSignature)
		}

		// Flatten cells in row-major order
		var actualValues []string
		for _, row := range rows {
			for cIdx, colName := range cols {
				val := row[colName]
				var tSig byte = 'T'
				if cIdx < len(rec.TypeSignature) {
					tSig = rec.TypeSignature[cIdx]
				}
				actualValues = append(actualValues, FormatCell(val, tSig))
			}
		}

		// Sort results per directive
		sortedActual := SortResults(actualValues, numCols, rec.Sort)

		// Verification Branch 1: MD5 Hash Match
		if rec.ExpectedHash != "" {
			actualHash := ComputeHash(sortedActual)
			if actualHash != rec.ExpectedHash || len(sortedActual) != rec.ExpectedCount {
				return fmt.Errorf("[%s:%d] hash mismatch: got %d values hashing to %s, expected %d values hashing to %s\nSQL: %s",
					rec.FilePath, rec.LineNumber, len(sortedActual), actualHash, rec.ExpectedCount, rec.ExpectedHash, rec.SQL)
			}
			return nil
		}

		// Verification Branch 2: Value-by-Value Match
		sortedExpected := SortResults(rec.ExpectedValues, numCols, rec.Sort)
		if len(sortedActual) != len(sortedExpected) {
			return fmt.Errorf("[%s:%d] row count mismatch: got %d cells (%d rows), expected %d cells (%d rows)\nSQL: %s\nGot:      %v\nExpected: %v",
				rec.FilePath, rec.LineNumber, len(sortedActual), len(rows), len(sortedExpected), len(sortedExpected)/max(1, numCols), rec.SQL, sortedActual, sortedExpected)
		}

		for i := range sortedActual {
			if sortedActual[i] != sortedExpected[i] {
				return fmt.Errorf("[%s:%d] cell %d mismatch at SQL: %s\nGot:      %q\nExpected: %q\nAll Got:      %v\nAll Expected: %v",
					rec.FilePath, rec.LineNumber, i, rec.SQL, sortedActual[i], sortedExpected[i], sortedActual, sortedExpected)
			}
		}

		return nil

	default:
		return fmt.Errorf("[%s:%d] unknown command type: %v", rec.FilePath, rec.LineNumber, rec.Type)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
