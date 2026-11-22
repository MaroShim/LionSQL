package slt

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/microsoft/go-mssqldb"
)

// TDSExecutor opens a persistent connection to the given TDS address (e.g. "127.0.0.1:1433").
type TDSExecutor struct {
	db   *sql.DB
	conn *sql.Conn
}

// NewTDSExecutor opens a persistent single TCP connection to the TDS server.
func NewTDSExecutor(addr string) (*TDSExecutor, error) {
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
	return &TDSExecutor{db: db, conn: conn}, nil
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
