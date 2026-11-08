package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
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

// EphemeralTable represents an in-memory session-isolated table (such as a materialized CTE result).
type EphemeralTable struct {
	Schema *storage.TableSchema
	Rows   []storage.Row
	Recs   []storage.Record
}

// Dialect represents client wire protocol and SQL semantics family.
type Dialect int

const (
	// DialectDefault is the default Microsoft T-SQL dialect (TDS, Web Studio).
	DialectDefault Dialect = iota
	// DialectTSQL is Microsoft SQL Server T-SQL dialect.
	DialectTSQL
	// DialectPostgres is PostgreSQL wire protocol v3 dialect.
	DialectPostgres
	// DialectMySQL is MySQL client compatibility dialect.
	DialectMySQL
)

// ClientSession maintains interactive connection state: open transaction, session variables, and query cancellation context.
type ClientSession struct {
	ID              string
	SPID            int
	Dialect         Dialect
	Session         storage.Session
	Tx              storage.Transaction
	TxFailed        bool // whether current transaction encountered an error ('E' in PGWire)
	TranCount       int  // T-SQL @@TRANCOUNT transaction nesting depth (0 = autocommit)
	Vars            map[string]any
	TableVars       map[string]string // @table_var -> physical table name
	TempTables      []string          // session-scoped #temp_tables
	TempTableMap    map[string]string // user #temp_table name -> physical __temp_<sessionID>_tableName
	EphemeralTables map[string]*EphemeralTable // session-local in-memory ephemeral tables (e.g. materialized CTEs)
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
	sessInUse       bool        // whether Session is currently leased to an active autocommit execution
	activeQueries   int         // number of active top-level queries on this session
	closed          bool        // whether CloseSession has been called on this session
	releaseOnDrain  func(storage.Session) // callback to release storage session when in-flight queries drain
	cancelMu        sync.Mutex  // dedicated lock for cooperative cancellation to avoid deadlocks with cs.Mu
	Mu              sync.Mutex
}

// checkAndDrainClosedLocked checks whether the session was marked closed while queries were in-flight,
// and if all queries have completed, detaches the session and returns the drain release callback.
// MUST be called while holding cs.Mu.
func (cs *ClientSession) checkAndDrainClosedLocked() (storage.Session, func(storage.Session)) {
	if cs.closed && cs.activeQueries <= 0 && !cs.sessInUse && cs.Session != nil {
		drainSess := cs.Session
		drainFn := cs.releaseOnDrain
		cs.Session = nil
		cs.releaseOnDrain = nil
		return drainSess, drainFn
	}
	return nil, nil
}

// GetQueryContext safely returns the active query context for this session, or context.Background() if none is set.
func (cs *ClientSession) GetQueryContext() context.Context {
	if cs == nil {
		return context.Background()
	}
	cs.Mu.Lock()
	ctx := cs.QueryCtx
	cs.Mu.Unlock()
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

// HasCancelFunc reports whether an active statement cancellation function is set under cancelMu.
func (cs *ClientSession) HasCancelFunc() bool {
	if cs == nil {
		return false
	}
	cs.cancelMu.Lock()
	has := cs.CancelFunc != nil
	cs.cancelMu.Unlock()
	return has
}

// SetCancel sets the active statement cancellation function under cancelMu.
func (cs *ClientSession) SetCancel(cancel context.CancelFunc) {
	if cs == nil {
		return
	}
	cs.cancelMu.Lock()
	cs.CancelFunc = cancel
	cs.CancelRequested = false
	cs.cancelMu.Unlock()
}

// ClearCancel resets the active cancellation function under cancelMu.
func (cs *ClientSession) ClearCancel() {
	if cs == nil {
		return
	}
	cs.cancelMu.Lock()
	cs.CancelFunc = nil
	cs.cancelMu.Unlock()
}

// CancelQuery invokes the active statement cancelFunc without holding cs.Mu.
func (cs *ClientSession) CancelQuery() bool {
	if cs == nil {
		return false
	}
	cs.cancelMu.Lock()
	defer cs.cancelMu.Unlock()
	if cs.CancelFunc != nil {
		cs.CancelRequested = true
		cs.CancelFunc()
		return true
	}
	return false
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

// IsTxFailed reports whether the session's active transaction has aborted ('E' status in PGWire).
func (cs *ClientSession) IsTxFailed() bool {
	if cs == nil {
		return false
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	return cs.TxFailed
}

// MarkTxFailed marks the session's active transaction as aborted after an execution failure.
func (cs *ClientSession) MarkTxFailed() {
	if cs == nil {
		return
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	cs.TxFailed = true
}

// InTransaction reports whether the session is currently in an active transaction block.
func (cs *ClientSession) InTransaction() bool {
	if cs == nil {
		return false
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	return cs.Tx != nil
}

// GetActiveTx returns the active transaction if one is open.
func (cs *ClientSession) GetActiveTx() (storage.Transaction, bool) {
	if cs == nil {
		return nil, false
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	return cs.Tx, cs.Tx != nil
}

// TxStatus returns the transaction status indicator for PGWire ('I' = idle, 'T' = in transaction, 'E' = aborted transaction).
func (cs *ClientSession) TxStatus() byte {
	if cs == nil {
		return 'I'
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.TxFailed {
		return 'E'
	}
	if cs.Tx != nil {
		return 'T'
	}
	return 'I'
}

// IsPostgres reports whether the session operates under PostgreSQL dialect semantics.
func (cs *ClientSession) IsPostgres() bool {
	if cs == nil {
		return false
	}
	return cs.Dialect == DialectPostgres || strings.HasPrefix(cs.ID, "pg-")
}

// IsTSQL reports whether the session operates under Microsoft T-SQL dialect semantics.
func (cs *ClientSession) IsTSQL() bool {
	if cs == nil {
		return true
	}
	return cs.Dialect == DialectTSQL || cs.Dialect == DialectDefault
}

// ExecuteProgram executes a compiled program in the session's active transaction if present,
// or in an autocommit session. Mutex synchronization, transaction failure flags, and session
// life cycles are completely encapsulated within this method.
func (cs *ClientSession) ExecuteProgram(
	ctx context.Context,
	prog *proc.Program,
	exec *runtime.Executor,
	paramMap map[string]any,
	eng storage.Engine,
	stmtLabel string,
) (res *runtime.ExecutionResult, txAborted bool, err error) {
	if cs != nil && cs.IsPostgres() && paramMap != nil {
		paramMap["identity_insert_all"] = true
	}
	if cs == nil {
		if eng == nil || exec == nil {
			return nil, false, fmt.Errorf("storage engine or executor not configured")
		}
		sess, err := eng.NewSession()
		if err != nil {
			return nil, false, err
		}
		defer sess.Close()
		res, err = exec.RunContext(ctx, prog, stmtLabel, paramMap, sess)
		return res, false, err
	}

	cs.Mu.Lock()
	if cs.TxFailed {
		cs.Mu.Unlock()
		return nil, true, nil
	}

	if cs.Tx != nil {
		tx := cs.Tx
		sess := cs.Session
		res, err = exec.ExecuteInTxWithSessionContext(ctx, prog, paramMap, tx, sess, nil)
		if err != nil {
			cs.TxFailed = true
		}
		cs.Mu.Unlock()
		return res, false, err
	}

	// Autocommit under existing or temporary session
	if cs.Session != nil && !cs.sessInUse {
		cs.sessInUse = true
		sess := cs.Session
		cs.Mu.Unlock()
		defer func() {
			cs.Mu.Lock()
			cs.sessInUse = false
			drainSess, drainFn := cs.checkAndDrainClosedLocked()
			cs.Mu.Unlock()
			if drainFn != nil {
				drainFn(drainSess)
			}
		}()
		res, err = exec.RunContext(ctx, prog, stmtLabel, paramMap, sess)
		return res, false, err
	}
	cs.Mu.Unlock()

	if eng != nil {
		sess, err := eng.NewSession()
		if err != nil {
			return nil, false, err
		}
		defer sess.Close()
		res, err = exec.RunContext(ctx, prog, stmtLabel, paramMap, sess)
		return res, false, err
	}

	return nil, false, fmt.Errorf("no active storage session")
}

// RegisterEphemeralTable registers or replaces an in-memory session-isolated table.
func (cs *ClientSession) RegisterEphemeralTable(name string, schema *storage.TableSchema, rows []storage.Row, recs []storage.Record) {
	if cs == nil {
		return
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.EphemeralTables == nil {
		cs.EphemeralTables = make(map[string]*EphemeralTable)
	}
	cs.EphemeralTables[strings.ToLower(name)] = &EphemeralTable{
		Schema: schema,
		Rows:   rows,
		Recs:   recs,
	}
}

// AppendEphemeralRows appends additional rows/records to an existing ephemeral table (used by recursive CTEs).
func (cs *ClientSession) AppendEphemeralRows(name string, rows []storage.Row, recs []storage.Record) {
	if cs == nil {
		return
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.EphemeralTables == nil {
		return
	}
	tbl, ok := cs.EphemeralTables[strings.ToLower(name)]
	if !ok {
		return
	}
	if len(rows) > 0 {
		tbl.Rows = append(tbl.Rows, rows...)
	}
	if len(recs) > 0 {
		tbl.Recs = append(tbl.Recs, recs...)
	}
}

// GetEphemeralTable retrieves an in-memory session-isolated table by name if present.
func (cs *ClientSession) GetEphemeralTable(name string) *EphemeralTable {
	if cs == nil {
		return nil
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.EphemeralTables == nil {
		return nil
	}
	return cs.EphemeralTables[strings.ToLower(name)]
}

// UnregisterEphemeralTable removes an in-memory ephemeral table by name.
func (cs *ClientSession) UnregisterEphemeralTable(name string) {
	if cs == nil {
		return
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.EphemeralTables == nil {
		return
	}
	delete(cs.EphemeralTables, strings.ToLower(name))
}

// sessionTxWrapper intercepts table scans and lookups for in-memory session ephemeral tables.
type sessionTxWrapper struct {
	storage.Transaction
	cs *ClientSession
}

func (w *sessionTxWrapper) GetTable(name string) (*storage.TableSchema, error) {
	if w.cs != nil {
		if eph := w.cs.GetEphemeralTable(name); eph != nil {
			return eph.Schema, nil
		}
	}
	return nil, storage.ErrTableNotFound
}

func (w *sessionTxWrapper) Scan(tableName string) (storage.Cursor, error) {
	if w.cs != nil {
		if eph := w.cs.GetEphemeralTable(tableName); eph != nil {
			return storage.NewSliceCursor(eph.Schema, eph.Rows, eph.Recs), nil
		}
	}
	return w.Transaction.Scan(tableName)
}

func (w *sessionTxWrapper) ScanPartitions(tableName string, partitions []string) (storage.Cursor, error) {
	if w.cs != nil {
		if eph := w.cs.GetEphemeralTable(tableName); eph != nil {
			return storage.NewSliceCursor(eph.Schema, eph.Rows, eph.Recs), nil
		}
	}
	return w.Transaction.ScanPartitions(tableName, partitions)
}

func (w *sessionTxWrapper) Get(tableName string, pk storage.Value) (storage.Record, error) {
	if w.cs != nil {
		if eph := w.cs.GetEphemeralTable(tableName); eph != nil {
			pkStr := pk.String()
			for _, r := range eph.Recs {
				if val, ok := r.FirstValue(); ok && val.String() == pkStr {
					return r, nil
				}
			}
			return nil, storage.ErrKeyNotFound
		}
	}
	return w.Transaction.Get(tableName, pk)
}

func (w *sessionTxWrapper) GetRow(tableName string, pk storage.Value) (storage.Row, error) {
	if w.cs != nil {
		if eph := w.cs.GetEphemeralTable(tableName); eph != nil {
			pkStr := pk.String()
			for _, r := range eph.Rows {
				if len(r.Values) > 0 && r.Values[0].String() == pkStr {
					return r, nil
				}
			}
			return storage.Row{}, storage.ErrKeyNotFound
		}
	}
	return w.Transaction.GetRow(tableName, pk)
}

// wrapSessionTx wraps a storage.Transaction with session ephemeral table interception.
func wrapSessionTx(cs *ClientSession, tx storage.Transaction) storage.Transaction {
	if tx == nil || cs == nil {
		return tx
	}
	if _, already := tx.(*sessionTxWrapper); already {
		return tx
	}
	return &sessionTxWrapper{Transaction: tx, cs: cs}
}



