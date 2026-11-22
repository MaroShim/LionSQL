package slt

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"
)

// TDSExecutor opens a persistent connection to the given TDS address (e.g. "127.0.0.1:1433").
type TDSExecutor struct {
	db     *sql.DB
	conn   *sql.Conn
	useRPC bool
}

// NewTDSExecutor opens a persistent single TCP connection to the TDS server.
func NewTDSExecutor(addr string) (*TDSExecutor, error) {
	return NewTDSExecutorWithRPC(addr, false)
}

// NewTDSExecutorWithRPC opens a persistent TCP connection with either raw SQL Batch or RPC sp_executesql mode.
func NewTDSExecutorWithRPC(addr string, useRPC bool) (*TDSExecutor, error) {
	parts := strings.Split(addr, ":")
	host := parts[0]
	port := "1433"
	if len(parts) > 1 {
		port = parts[1]
	}
	dsn := fmt.Sprintf("server=%s;port=%s;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", host, port)
	db, err := sql.Open("sqlserver", dsn)
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
	return &TDSExecutor{db: db, conn: conn, useRPC: useRPC}, nil
}

// Close releases the underlying connection and database handle.
func (e *TDSExecutor) Close() error {
	if e.conn != nil {
		_ = e.conn.Close()
	}
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

// Execute runs SQL over TDS and decodes tabular rows.
func (e *TDSExecutor) Execute(sqlStr string) ([]string, []map[string]any, error) {
	query := sqlStr
	if e.useRPC {
		trimmed := strings.TrimSpace(sqlStr)
		upper := strings.ToUpper(trimmed)
		if !strings.HasPrefix(upper, "EXEC SP_EXECUTESQL") && !strings.Contains(upper, "\nGO") {
			escaped := strings.ReplaceAll(trimmed, "'", "''")
			query = fmt.Sprintf("EXEC sp_executesql N'%s'", escaped)
		}
	}
	rows, err := e.conn.QueryContext(context.Background(), query)
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
			if t, ok := val.(time.Time); ok {
				if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
					val = t.Format("2006-01-02")
				} else {
					val = t.Format("2006-01-02 15:04:05")
				}
			} else if b, ok := val.([]byte); ok {
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
