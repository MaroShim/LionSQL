package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// PreparedStatement stores compiled bytecode and parameter definitions for repeated execution.
type PreparedStatement struct {
	Name       string
	Query      string
	Program    *proc.Program
	ParamNames []string
}

// SessionCursor maintains stateful row iteration for server-side SQL cursors.
type SessionCursor struct {
	Name       string
	Query      string
	Columns    []string
	Rows       []map[string]any
	Pos        int // next row index to fetch (0-indexed)
	WithHold   bool
	Scroll     bool
	Binary     bool
	DeclaredAt time.Time
}

// ClientSession maintains interactive connection state: open transaction, session variables, and query cancellation context.
type ClientSession struct {
	ID              string
	SPID            int
	Session         storage.Session
	Tx              storage.Transaction
	TxFailed        bool // whether current transaction encountered an error ('E' in PGWire)
	Vars            map[string]any
	TableVars       map[string]string // @table_var -> physical table name
	TempTables      []string          // session-scoped #temp_tables
	TempTableMap    map[string]string // user #temp_table name -> physical __temp_<sessionID>_tableName
	PreparedStmts   map[string]*PreparedStatement
	Cursors         map[string]*SessionCursor // server-side cursor state
	User            string                    // active database user principal for RBAC
	OriginalUser    string                    // original user prior to SETUSER / EXECUTE AS
	Database        string                    // active virtual database context (default: "master")
	QueryCtx        context.Context
	CancelFunc      context.CancelFunc
	RunningQuery    string
	QueryStartTime  time.Time
	CancelRequested bool
	IsReadOnlyFn    func() bool // reports whether the engine/host is running in read-only replica mode
	Mu              sync.Mutex
}

// IsReadOnly returns true if the session or host node was configured as read-only.
func (cs *ClientSession) IsReadOnly() bool {
	if cs == nil {
		return false
	}
	if cs.IsReadOnlyFn != nil && cs.IsReadOnlyFn() {
		return true
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if ro, ok := cs.Vars["transaction_read_only"].(bool); ok && ro {
		return true
	}
	return false
}

// TxOptions returns the configured transaction options for this session.
func (cs *ClientSession) TxOptions() storage.TxOptions {
	return storage.TxOptions{
		ReadOnly: cs.IsReadOnly(),
	}
}

// GetQueryTimeout returns the configured query or statement timeout for this session.
func (cs *ClientSession) GetQueryTimeout() time.Duration {
	if cs == nil {
		return 0
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	if v, ok := cs.Vars["query_timeout"]; ok && v != nil {
		return parseTimeoutDurationVal(v)
	} else if v, ok := cs.Vars["statement_timeout"]; ok && v != nil {
		return parseTimeoutDurationVal(v)
	}
	return 0
}

func parseTimeoutDurationVal(v any) time.Duration {
	switch val := v.(type) {
	case int:
		if val > 0 {
			return time.Duration(val) * time.Millisecond
		}
	case int64:
		if val > 0 {
			return time.Duration(val) * time.Millisecond
		}
	case float64:
		if val > 0 {
			return time.Duration(val) * time.Millisecond
		}
	case string:
		if d, err := ParseTimeoutDuration(val); err == nil && d > 0 {
			return d
		}
	case time.Duration:
		if val > 0 {
			return val
		}
	}
	return 0
}

// ResolveTempTableName returns the physical table name for a session-scoped temporary table.
// If it is not a local temporary table (e.g. ##global_temp, or ordinary table), it returns the input unchanged.
func (cs *ClientSession) ResolveTempTableName(tbl string) string {
	if cs == nil || !strings.HasPrefix(tbl, "#") || strings.HasPrefix(tbl, "##") || strings.HasPrefix(tbl, "#__") {
		return tbl
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.TempTableMap == nil {
		cs.TempTableMap = make(map[string]string)
	}
	lower := strings.ToLower(tbl)
	if phys, exists := cs.TempTableMap[lower]; exists {
		return phys
	}
	// Generate mangled physical name
	clean := strings.TrimPrefix(lower, "#")
	sessID := cs.ID
	if sessID == "" {
		sessID = "default"
	}
	phys := fmt.Sprintf("__temp_%s_%s", sessID, clean)
	cs.TempTableMap[lower] = phys
	cs.TempTables = append(cs.TempTables, phys)
	return phys
}

// GetTempTablePhysical returns the registered physical table name for a local #temp table if registered.
func (cs *ClientSession) GetTempTablePhysical(tbl string) (string, bool) {
	if cs == nil || !strings.HasPrefix(tbl, "#") || strings.HasPrefix(tbl, "##") || strings.HasPrefix(tbl, "#__") {
		return tbl, false
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.TempTableMap == nil {
		return "", false
	}
	phys, exists := cs.TempTableMap[strings.ToLower(tbl)]
	return phys, exists
}

// UnregisterTempTable unregisters a dropped local temporary table.
func (cs *ClientSession) UnregisterTempTable(tbl string) {
	if cs == nil || !strings.HasPrefix(tbl, "#") || strings.HasPrefix(tbl, "##") || strings.HasPrefix(tbl, "#__") {
		return
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.TempTableMap == nil {
		return
	}
	lower := strings.ToLower(tbl)
	phys, exists := cs.TempTableMap[lower]
	if exists {
		delete(cs.TempTableMap, lower)
		for i, t := range cs.TempTables {
			if t == phys || t == lower {
				cs.TempTables = append(cs.TempTables[:i], cs.TempTables[i+1:]...)
				break
			}
		}
	}
}

