package tx

import "fmt"

// State represents the lifecycle state of a transaction.
type State int32

const (
	// StateIdle indicates no transaction is currently active (autocommit mode).
	StateIdle State = iota

	// StateActive indicates an explicit transaction is active and accepting statements.
	StateActive

	// StateInError indicates a statement inside an explicit transaction failed.
	// In PostgreSQL wire semantics (state 'E'), subsequent commands are rejected
	// until an explicit ROLLBACK (or ROLLBACK TO SAVEPOINT) is executed.
	StateInError

	// StateCommitted indicates the transaction successfully committed and is terminating.
	StateCommitted

	// StateAborted indicates the transaction was rolled back and is terminating.
	StateAborted
)

// String returns the human-readable name of the transaction state.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "IDLE"
	case StateActive:
		return "ACTIVE"
	case StateInError:
		return "IN_ERROR"
	case StateCommitted:
		return "COMMITTED"
	case StateAborted:
		return "ABORTED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int32(s))
	}
}

// IsTerminal returns true if the state represents a completed transaction lifecycle.
func (s State) IsTerminal() bool {
	return s == StateCommitted || s == StateAborted
}
