package tx

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"lionsql/pkg/storage"
)

var (
	// ErrTxActive indicates an attempt to start a transaction when one is already active.
	ErrTxActive = errors.New("transaction is already active")
	// ErrTxIdle indicates an attempt to commit/rollback when no transaction is active.
	ErrTxIdle = errors.New("no active transaction to commit or rollback")
	// ErrTxInError indicates statements are rejected because the transaction is in an error state.
	ErrTxInError = errors.New("current transaction is aborted, commands ignored until end of transaction block")
)

// IsolationLevel defines transaction isolation level.
type IsolationLevel int

const (
	ReadCommitted IsolationLevel = iota
	RepeatableRead
	Snapshot
	Serializable
)

// Options specifies transaction configuration options.
type Options struct {
	Isolation IsolationLevel
	ReadOnly  bool
}

// Context encapsulates the runtime state of a single physical transaction.
type Context struct {
	mu sync.RWMutex

	ID        uint64
	State     State
	Opts      Options
	Ephemeral bool

	// Storage handles
	Session   storage.Session
	StorageTx storage.Transaction

	// Savepoints & Checkpoints
	Savepoints *SavepointStack

	// Deferred actions executed upon successful physical commit (e.g., physical table deletion)
	DeferredActions []func() error

	// ShadowCatalog overlay for transactional DDL
	ShadowCatalog *ShadowCatalog

	// Error that placed the transaction into StateInError
	Err error

	CreatedAt time.Time
}

// NewContext creates a new transaction context in StateActive.
func NewContext(id uint64, sess storage.Session, stx storage.Transaction, opts Options, ephemeral bool) *Context {
	return &Context{
		ID:         id,
		State:      StateActive,
		Opts:       opts,
		Ephemeral:  ephemeral,
		Session:    sess,
		StorageTx:     stx,
		Savepoints:    NewSavepointStack(),
		ShadowCatalog: NewShadowCatalog(),
		CreatedAt:     time.Now(),
	}
}

// GetState returns the current state of the transaction.
func (c *Context) GetState() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.State
}

// SetState transitions the transaction state.
func (c *Context) SetState(s State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.State = s
}

// SetError transitions the transaction into StateInError.
func (c *Context) SetError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.State = StateInError
	c.Err = err
}

// GetError returns the error that caused StateInError.
func (c *Context) GetError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Err
}

// AddDeferredAction enqueues an action to be executed upon successful commit.
func (c *Context) AddDeferredAction(fn func() error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DeferredActions = append(c.DeferredActions, fn)
}

// ExecuteDeferredActions runs all enqueued deferred actions in order.
func (c *Context) ExecuteDeferredActions() error {
	c.mu.Lock()
	actions := c.DeferredActions
	c.DeferredActions = nil
	c.mu.Unlock()

	for _, fn := range actions {
		if err := fn(); err != nil {
			return fmt.Errorf("deferred action failed: %w", err)
		}
	}
	return nil
}

// ClearDeferredActions discards all deferred actions without executing them.
func (c *Context) ClearDeferredActions() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DeferredActions = nil
}
