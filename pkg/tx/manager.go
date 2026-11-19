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
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) State() State {
	if m.current == nil {
		return StateIdle
	}
	return m.current.GetState()
}

// InTransaction reports whether an explicit, non-ephemeral transaction is active.
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) InTransaction() bool {
	return m.current != nil && !m.current.Ephemeral
}

// IsInError reports whether the current transaction has failed (PGWire 'E' state).
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) IsInError() bool {
	if m.current == nil {
		return false
	}
	return m.current.GetState() == StateInError
}

// ActiveStorageTx returns the active storage transaction if one exists.
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) ActiveStorageTx() storage.Transaction {
	if m.current == nil {
		return nil
	}
	return m.current.StorageTx
}

// SetActiveStorageTx updates the active storage transaction (e.g. after procedure execution or in VM subqueries).
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) SetActiveStorageTx(stx storage.Transaction) {
	if m.current != nil {
		m.current.StorageTx = stx
	} else if stx != nil {
		m.current = &Context{
			ID:        m.nextXID.Add(1),
			StorageTx: stx,
			State:     StateActive,
			Ephemeral: false,
		}
	}
}

// NestLevel returns the current transaction nesting level (T-SQL @@TRANCOUNT).
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) NestLevel() int {
	if m.current == nil || m.current.Ephemeral {
		return 0
	}
	return m.current.GetNestLevel()
}

// IncNestLevel increments the transaction nesting depth.
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) IncNestLevel() int {
	if m.current == nil {
		return 0
	}
	return m.current.IncNestLevel()
}

// DecNestLevel decrements the transaction nesting depth.
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) DecNestLevel() int {
	if m.current == nil {
		return 0
	}
	return m.current.DecNestLevel()
}

// SetNestLevel sets the transaction nesting depth.
// Caller must hold Manager.Lock() or invoke within serialized session execution.
func (m *Manager) SetNestLevel(n int) {
	if m.current != nil {
		m.current.SetNestLevel(n)
	}
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
			_ = txCtx.ExecuteRollbackActions()
			txCtx.SetState(StateAborted)
			m.current = nil
			return fmt.Errorf("storage commit failed: %w", err)
		}
	}

	// 2. Clear rollback actions upon successful commit
	txCtx.ClearRollbackActions()

	// 3. Execute deferred physical actions (e.g. drop tombstones, finalize DDL)
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

	// 3. Execute rollback actions in LIFO order (e.g. drop created tables, restore tombstones)
	if err := txCtx.ExecuteRollbackActions(); err != nil && rollErr == nil {
		rollErr = err
	}

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

// ClearInError recovers the current transaction from StateInError back to StateActive.
func (m *Manager) ClearInError() {
	if m.current != nil && m.current.GetState() == StateInError {
		m.current.SetState(StateActive)
		m.current.Err = nil
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
	m.current.mu.RLock()
	defOffset := len(m.current.DeferredActions)
	rbOffset := len(m.current.RollbackActions)
	m.current.mu.RUnlock()
	m.current.Savepoints.PushWithOffsets(name, 0, defOffset, rbOffset)
	return nil
}

// RollbackTo rolls back the active transaction to the specified savepoint.
func (m *Manager) RollbackTo(name string) error {
	if m.current == nil {
		return ErrTxIdle
	}
	sp, err := m.current.Savepoints.RollbackTo(name)
	if err != nil {
		return err
	}

	// [TX-001] Truncate deferred and rollback actions recorded after this savepoint
	m.current.mu.Lock()
	if sp.DeferredOffset <= len(m.current.DeferredActions) {
		m.current.DeferredActions = m.current.DeferredActions[:sp.DeferredOffset]
	}
	if sp.RollbackOffset <= len(m.current.RollbackActions) {
		m.current.RollbackActions = m.current.RollbackActions[:sp.RollbackOffset]
	}
	m.current.mu.Unlock()

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
