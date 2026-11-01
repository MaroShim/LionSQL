package slt

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"

	_ "github.com/lib/pq"

	"lionsql/pkg/engine"
)

// Executor represents any database execution engine capable of running SQL and returning tabular results.
type Executor interface {
	Execute(sqlStr string) (columns []string, rows []map[string]any, err error)
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
func (e *CoordinatorExecutor) Execute(sqlStr string) ([]string, []map[string]any, error) {
	res, err := e.coord.ExecuteSQL(sqlStr)
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

// PGWireExecutor executes SQL statements over a real PostgreSQL Wire Protocol v3 TCP connection.
type PGWireExecutor struct {
	db   *sql.DB
	conn *sql.Conn
}

// NewPGWireExecutor opens a persistent single TCP connection to the given PGWire address (e.g. "127.0.0.1:5433").
func NewPGWireExecutor(addr string) (*PGWireExecutor, error) {
	dsn := fmt.Sprintf("postgres://admin:admin@%s/lionsql?sslmode=disable", addr)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &PGWireExecutor{db: db, conn: conn}, nil
}

// Close releases the underlying TCP connection and database handle.
func (e *PGWireExecutor) Close() error {
	if e.conn != nil {
		_ = e.conn.Close()
	}
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

// Execute runs SQL over the PGWire TCP socket and decodes tabular rows.
func (e *PGWireExecutor) Execute(sqlStr string) ([]string, []map[string]any, error) {
	rows, err := e.conn.QueryContext(context.Background(), sqlStr)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	if len(cols) == 0 {
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}

	// Disambiguate duplicate column names so row map does not overwrite earlier columns
	uniqueCols := make([]string, len(cols))
	seen := make(map[string]int, len(cols))
	for i, c := range cols {
		if count, exists := seen[c]; exists {
			uniqueCols[i] = fmt.Sprintf("%s#%d", c, count)
		} else {
			uniqueCols[i] = c
		}
		seen[c]++
	}

	var result []map[string]any
	for rows.Next() {
		rawVals := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range rawVals {
			dest[i] = &rawVals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		rowMap := make(map[string]any, len(cols))
		for i, colName := range uniqueCols {
			val := rawVals[i]
			if b, ok := val.([]byte); ok {
				s := string(b)
				if f, ferr := strconv.ParseFloat(s, 64); ferr == nil && !strings.HasPrefix(s, "0x") {
					if iVal, ierr := strconv.ParseInt(s, 10, 64); ierr == nil {
						val = iVal
					} else {
						val = f
					}
				} else {
					val = s
				}
			}
			rowMap[colName] = val
		}
		result = append(result, rowMap)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return uniqueCols, result, nil
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
		if colType == 'R' || colType == 'r' {
			return fmt.Sprintf("%.3f", v)
		}
		if colType == 'I' || colType == 'i' {
			return fmt.Sprintf("%.0f", v)
		}
		if v == math.Trunc(v) {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%.3f", v)
	case float32:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "NULL"
		}
		if colType == 'R' || colType == 'r' {
			return fmt.Sprintf("%.3f", f)
		}
		if colType == 'I' || colType == 'i' {
			return fmt.Sprintf("%.0f", f)
		}
		if f == math.Trunc(f) {
			return fmt.Sprintf("%.0f", f)
		}
		return fmt.Sprintf("%.3f", f)
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
		if rec.ExpectedError != "" {
			errStr := strings.ToLower(err.Error())
			expected := strings.ToLower(rec.ExpectedError)
			if !strings.Contains(errStr, expected) {
				return fmt.Errorf("[%s:%d] statement error message mismatch: expected %q, got %q\nSQL: %s",
					rec.FilePath, rec.LineNumber, rec.ExpectedError, err.Error(), rec.SQL)
			}
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
