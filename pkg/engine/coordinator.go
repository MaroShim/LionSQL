package engine

import (
	"context"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/storage"
)

// QueryResult represents the outcome of a SQL statement execution.
type QueryResult struct {
	Success      bool             `json:"success"`
	Command      string           `json:"command"`
	Columns      []string         `json:"columns,omitempty"`
	Rows         []map[string]any `json:"rows,omitempty"`
	Values       [][]any          `json:"values,omitempty"`
	RowsAffected int64            `json:"rows_affected"`
	DurationUS   int64            `json:"duration_us"`
	Messages     []string         `json:"messages,omitempty"`
	Error        string           `json:"error,omitempty"`

	// ReturnStatus carries the integer a stored procedure passed to RETURN.
	// It is meaningful only when HasReturnStatus is true (the statement was a procedure call).
	ReturnStatus    int32 `json:"return_status,omitempty"`
	HasReturnStatus bool  `json:"has_return_status,omitempty"`

	// Next links subsequent result sets produced by multi-statement batches
	// or stored procedures emitting multiple SELECT queries.
	Next *QueryResult `json:"next,omitempty"`
}

// ReturnStatusFromValue converts a procedure RETURN value into the int32 sent as the TDS RETURNSTATUS token.
// A missing value (procedure ended without RETURN) and non-integer values map to 0, as in SQL Server.
func ReturnStatusFromValue(v any) int32 {
	switch n := v.(type) {
	case int:
		return int32(n)
	case int8:
		return int32(n)
	case int16:
		return int32(n)
	case int32:
		return n
	case int64:
		return int32(n)
	case uint8:
		return int32(n)
	case uint16:
		return int32(n)
	case uint32:
		return int32(n)
	case float64:
		return int32(n)
	}
	return 0
}

// ActiveQueryInfo represents snapshot metadata of an in-flight query.
type ActiveQueryInfo struct {
	SessionID  string    `json:"session_id"`
	SPID       int       `json:"spid"`
	Query      string    `json:"query"`
	StartTime  time.Time `json:"start_time"`
	DurationUS int64     `json:"duration_us"`
}

// SQLCoordinator defines the core contract for executing SQL statements and managing interactive sessions.
// Implemented by *api.Server (and future *engine.Coordinator), consumed cleanly by wire protocol listeners.
type SQLCoordinator interface {
	// ExecuteSQL parses and runs one or more SQL statements on the default session.
	ExecuteSQL(query string) (*QueryResult, error)

	// ExecuteSessionSQL runs one or more SQL statements within an existing or new session.
	ExecuteSessionSQL(sessionID string, query string) (*QueryResult, error)

	// ExecuteSessionSQLContext runs SQL statements within a specific client session and cancellation context.
	ExecuteSessionSQLContext(ctx context.Context, sessionID string, query string) (*QueryResult, error)

	// ExecutePointLookup executes a direct primary key point lookup bypassing Volcano CBO,
	// planner AST allocation, and iterator state machines.
	ExecutePointLookup(sessID string, tableName string, pkVal storage.Value, projections []string) (*QueryResult, error)

	// BeginSessionTx starts an interactive transaction on the specified session directly without query parsing overhead.
	BeginSessionTx(sessionID string, opts storage.TxOptions) error

	// CommitSessionTx commits the active transaction on the specified session directly.
	CommitSessionTx(sessionID string) error

	// RollbackSessionTx rolls back the active transaction on the specified session directly.
	RollbackSessionTx(sessionID string) error

	// CloseSession gracefully closes and unregisters a client session.
	CloseSession(id string)

	// ResetSession restores a persistent client session to a clean, isolated initial state for connection pooling reuse.
	ResetSession(id string, resetTx bool) error

	// ResetSessionWithOptions restores a persistent client session to a clean state according to the given options.
	ResetSessionWithOptions(id string, opts SessionResetOptions) error

	// DiscardSessionTempTables drops all session-local temporary and ephemeral tables without altering other session state.
	DiscardSessionTempTables(id string) error

	// CancelSessionQuery triggers cooperative cancellation for the currently active statement on sessionID.
	CancelSessionQuery(sessionID string) bool

	// CancelQueryBySPID cancels the active query executing on the connection identified by spid.
	CancelQueryBySPID(spid int) bool

	// GetActiveQueries returns a snapshot list of all currently executing queries across all sessions.
	GetActiveQueries() []ActiveQueryInfo

	// GetSessionTxStatus returns PostgreSQL transaction status byte ('I', 'T', 'E').
	GetSessionTxStatus(id string) byte

	// MaxConnections returns the configured maximum concurrent client connections limit.
	MaxConnections() int

	// SetMaxConnections dynamically updates the maximum concurrent connections limit.
	SetMaxConnections(n int)

	// GetSession returns the active ClientSession by ID, or nil if not found.
	GetSession(id string) *ClientSession

	// SetSessionUser updates the active user principal on an interactive session for RBAC.
	SetSessionUser(id, user string)

	// SetSessionDialect explicitly sets the SQL dialect family for an interactive session.
	SetSessionDialect(id string, dialect Dialect)

	// AuthService returns the underlying authentication service.
	AuthService() *auth.AuthService

	// IsAuthEnabled reports whether RBAC authentication and permission checks are enforced.
	IsAuthEnabled() bool

	// AllocTableIdentity atomically allocates a contiguous range of count IDs for tableName.
	AllocTableIdentity(tableName string, count int64, initFn func() int64) int64

	// AllocTableIdentityWithStep atomically allocates a contiguous range of count IDs for tableName with custom seed and step.
	AllocTableIdentityWithStep(tableName string, count int64, seed int64, step int64, initFn func() (int64, bool)) int64

	// SetTableLastIdentity records or ratchets the identity value generated for a table.
	SetTableLastIdentity(tableName string, id int64)

	// GetTableLastIdentity retrieves the most recent identity value generated for a table.
	GetTableLastIdentity(tableName string) (int64, bool)
}

// SessionResetOptions specifies granular options for resetting client session state.
type SessionResetOptions struct {
	ResetTx        bool // Rollback active uncommitted transaction
	DropTempTables bool // Drop session-local temporary tables (#temp) and table variables (@table)
	ClearVars      bool // Clear session user variables and reset system variables (timezone, isolation, database)
	ClearPlans     bool // Clear session prepared statements and cached execution plans
	CloseCursors   bool // Close and deallocate all session server-side cursors
	ResetAuth      bool // Revert user impersonation (SETUSER / EXECUTE AS) stack
}

// ResetOptionsAll returns options for a complete session reset (DISCARD ALL, sp_reset_connection, StatusResetConnection).
func ResetOptionsAll() SessionResetOptions {
	return SessionResetOptions{
		ResetTx:        true,
		DropTempTables: true,
		ClearVars:      true,
		ClearPlans:     true,
		CloseCursors:   true,
		ResetAuth:      true,
	}
}

// ResetOptionsSkipTran returns options for resetting session state while preserving the active transaction
// (TDS StatusResetConnectionSkipTran 0x10).
func ResetOptionsSkipTran() SessionResetOptions {
	opts := ResetOptionsAll()
	opts.ResetTx = false
	return opts
}

// ResetOptionsPlansOnly returns options for clearing prepared statements only (DISCARD PLANS).
func ResetOptionsPlansOnly() SessionResetOptions {
	return SessionResetOptions{
		ClearPlans: true,
	}
}

// ResetOptionsTempOnly returns options for dropping session temporary tables only (DISCARD TEMP).
func ResetOptionsTempOnly() SessionResetOptions {
	return SessionResetOptions{
		DropTempTables: true,
	}
}
