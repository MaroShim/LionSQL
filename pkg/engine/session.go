package engine

import (
	"context"
	"sync"
	"time"

	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

// PreparedStatement stores compiled bytecode and parameter definitions for repeated execution.
type PreparedStatement struct {
	Name       string
	Query      string
	Program    *proc.Program
	ParamNames []string
}

// ClientSession maintains interactive connection state: open transaction, session variables, and query cancellation context.
type ClientSession struct {
	ID              string
	SPID            int
	Session         storage.Session
	Tx              storage.Transaction
	TxFailed        bool              // whether current transaction encountered an error ('E' in PGWire)
	Vars            map[string]any
	TableVars       map[string]string // @table_var -> physical table name
	TempTables      []string          // session-scoped #temp_tables
	PreparedStmts   map[string]*PreparedStatement
	User            string            // active database user principal for RBAC
	OriginalUser    string            // original user prior to SETUSER / EXECUTE AS
	QueryCtx        context.Context
	CancelFunc      context.CancelFunc
	RunningQuery    string
	QueryStartTime  time.Time
	CancelRequested bool
	Mu              sync.Mutex
}

// IsReadOnly returns true if the session was configured as read-only.
func (cs *ClientSession) IsReadOnly() bool {
	if cs == nil {
		return false
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
		return time.Duration(val) * time.Millisecond
	case int64:
		return time.Duration(val) * time.Millisecond
	case float64:
		return time.Duration(val) * time.Millisecond
	case time.Duration:
		return val
	}
	return 0
}
