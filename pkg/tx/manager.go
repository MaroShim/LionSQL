package tx

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"lionsql/pkg/storage"
)

// SessionProvider is a function or interface that supplies or recreates storage sessions.
type SessionProvider interface {
	NewSession() (storage.Session, error)
}

// Manager orchestrates the transaction lifecycle for a single client session.
// It guarantees that each session maintains at most one active transaction context,
// enforces command serialization via its Mutex, and manages atomic commit/rollback.
type Manager struct {
	mu sync.Mutex

	sessID   string
	session  storage.Session
	provider SessionProvider
	nextXID  *atomic.Uint64

	current *Context
}

// NewManager creates a transaction manager dedicated to a client session.
func NewManager(sessID string, session storage.Session, provider SessionProvider, nextXID *atomic.Uint64) *Manager {
	if nextXID == nil {
		nextXID = new(atomic.Uint64)
		nextXID.Store(1)
	}
	return &Manager{
		sessID:   sessID,
		session:  session,
		provider: provider,
		nextXID:  nextXID,
	}
}

// Lock acquires the session's command execution lock.
// This guarantees strictly serial query execution on the session.
func (m *Manager) Lock() {
	m.mu.Lock()
}

// Unlock releases the session's command execution lock.
func (m *Manager) Unlock() {
	m.mu.Unlock()
}

// Current returns the current active transaction context (or nil if idle).
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) Current() *Context {
	return m.current
}

// State returns the current transaction state.
func (m *Manager) State() State {
	if m.current == nil {
		return StateIdle
	}
	return m.current.GetState()
}

// Session returns the underlying storage session.
func (m *Manager) Session() storage.Session {
	return m.session
}

// SetSession updates the underlying storage session (e.g. after session recreation).
func (m *Manager) SetSession(sess storage.Session) {
	m.session = sess
}

// Begin starts an explicit transaction on this session.
// Returns ErrTxActive if an explicit transaction is already in progress.
func (m *Manager) Begin(ctx context.Context, opts Options) (*Context, error) {
	if m.current != nil && !m.current.Ephemeral {
		// In T-SQL / Postgres, starting a transaction when one is active is rejected or warned.
		return nil, ErrTxActive
	}

	stx, sess, err := m.acquireStorageTx(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to start storage transaction: %w", err)
	}

	xid := m.nextXID.Add(1)
	m.current = NewContext(xid, sess, stx, opts, false)
	return m.current, nil
}

// BeginEphemeral starts an autocommit single-statement transaction.
func (m *Manager) BeginEphemeral(ctx context.Context, opts Options) (*Context, error) {
	if m.current != nil {
		if m.current.GetState() == StateInError {
			return nil, ErrTxInError
		}
		// If an explicit transaction is already active, return the existing context.
		return m.current, nil
	}

	stx, sess, err := m.acquireStorageTx(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to start ephemeral storage transaction: %w", err)
	}

	xid := m.nextXID.Add(1)
	m.current = NewContext(xid, sess, stx, opts, true)
	return m.current, nil
}

// Commit commits the current transaction.
// If the transaction is in StateInError, commit fails with ErrTxInError and aborts.
func (m *Manager) Commit(ctx context.Context) error {
	if m.current == nil {
		return ErrTxIdle
	}

	txCtx := m.current
	if txCtx.GetState() == StateInError {
		_ = m.Rollback(ctx)
		return ErrTxInError
	}

	// 1. Commit physical storage transaction
	if txCtx.StorageTx != nil {
		if err := txCtx.StorageTx.Commit(); err != nil {
			_ = txCtx.StorageTx.Rollback()
			txCtx.ClearDeferredActions()
			txCtx.SetState(StateAborted)
			m.current = nil
			return fmt.Errorf("storage commit failed: %w", err)
		}
	}

	// 2. Execute deferred physical actions (e.g. physical file drop)
	if err := txCtx.ExecuteDeferredActions(); err != nil {
		// Log or return error (commit has already occurred in WAL/storage)
		txCtx.SetState(StateCommitted)
		m.current = nil
		return err
	}

	txCtx.SetState(StateCommitted)
	m.current = nil
	return nil
}

// Rollback rolls back the current transaction.
func (m *Manager) Rollback(ctx context.Context) error {
	if m.current == nil {
		return ErrTxIdle
	}

	txCtx := m.current
	var rollErr error

	// 1. Roll back physical storage transaction
	if txCtx.StorageTx != nil {
		rollErr = txCtx.StorageTx.Rollback()
	}

	// 2. Clear deferred actions without executing them
	txCtx.ClearDeferredActions()

	txCtx.SetState(StateAborted)
	m.current = nil
	return rollErr
}

// SetInError marks the current transaction as aborted/in-error (Postgres 'E' state).
func (m *Manager) SetInError(err error) {
	if m.current != nil {
		m.current.SetError(err)
	}
}

// Savepoint creates a savepoint marker within the active transaction.
func (m *Manager) Savepoint(name string) error {
	if m.current == nil {
		return ErrTxIdle
	}
	if m.current.GetState() == StateInError {
		return ErrTxInError
	}
	m.current.Savepoints.Push(name, 0)
	return nil
}

// RollbackTo rolls back the active transaction to the specified savepoint.
func (m *Manager) RollbackTo(name string) error {
	if m.current == nil {
		return ErrTxIdle
	}
	_, err := m.current.Savepoints.RollbackTo(name)
	if err != nil {
		return err
	}

	// If transaction was in StateInError, rolling back to a valid savepoint recovers it to StateActive
	if m.current.GetState() == StateInError {
		m.current.SetState(StateActive)
		m.current.Err = nil
	}
	return nil
}

// ReleaseSavepoint releases/destroys the specified savepoint.
func (m *Manager) ReleaseSavepoint(name string) error {
	if m.current == nil {
		return ErrTxIdle
	}
	if m.current.GetState() == StateInError {
		return ErrTxInError
	}
	return m.current.Savepoints.Release(name)
}

// Close closes the manager and rolls back any uncommitted transaction cleanly.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var err error
	if m.current != nil {
		err = m.Rollback(context.Background())
	}
	if m.session != nil {
		_ = m.session.Close()
		m.session = nil
	}
	return err
}

func (m *Manager) acquireStorageTx(opts Options) (storage.Transaction, storage.Session, error) {
	if m.session == nil && m.provider != nil {
		sess, err := m.provider.NewSession()
		if err != nil {
			return nil, nil, err
		}
		m.session = sess
	}
	if m.session == nil {
		return nil, nil, fmt.Errorf("no storage session available")
	}

	stxOpts := storage.TxOptions{
		ReadOnly: opts.ReadOnly,
	}
	stx, err := m.session.BeginTx(stxOpts)
	if err != nil {
		return nil, nil, err
	}
	return stx, m.session, nil
}
