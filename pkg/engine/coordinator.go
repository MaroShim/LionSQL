package engine

import (
	"context"
	"time"

	"lionsql/pkg/auth"
)

// QueryResult represents the outcome of a SQL statement execution.
type QueryResult struct {
	Success      bool             `json:"success"`
	Command      string           `json:"command"`
	Columns      []string         `json:"columns,omitempty"`
	Rows         []map[string]any `json:"rows,omitempty"`
	RowsAffected int64            `json:"rows_affected"`
	DurationUS   int64            `json:"duration_us"`
	Messages     []string         `json:"messages,omitempty"`
	Error        string           `json:"error,omitempty"`
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

	// CloseSession gracefully closes and unregisters a client session.
	CloseSession(id string)

	// CancelSessionQuery triggers cooperative cancellation for the currently active statement on sessionID.
	CancelSessionQuery(sessionID string) bool

	// CancelQueryBySPID cancels the active query executing on the connection identified by spid.
	CancelQueryBySPID(spid int) bool

	// GetActiveQueries returns a snapshot list of all currently executing queries across all sessions.
	GetActiveQueries() []ActiveQueryInfo

	// GetSessionTxStatus returns PostgreSQL transaction status byte ('I', 'T', 'E').
	GetSessionTxStatus(id string) byte

	// SetSessionUser updates the active user principal on an interactive session for RBAC.
	SetSessionUser(id, user string)

	// AuthService returns the underlying authentication service.
	AuthService() *auth.AuthService

	// IsAuthEnabled reports whether RBAC authentication and permission checks are enforced.
	IsAuthEnabled() bool
}
