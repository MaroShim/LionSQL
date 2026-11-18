package tx_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/tx"
)

type mockProvider struct {
	eng storage.Engine
}

func (m *mockProvider) NewSession() (storage.Session, error) {
	return m.eng.NewSession()
}

func setupTestTxManager(t *testing.T) (*tx.Manager, storage.Engine, func()) {
	eng := memengine.New()
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create storage session: %v", err)
	}

	provider := &mockProvider{eng: eng}
	mgr := tx.NewManager("test-session", sess, provider, nil)

	cleanup := func() {
		_ = mgr.Close()
		_ = eng.Close()
	}
	return mgr, eng, cleanup
}

func TestTxManager_ExplicitCommit(t *testing.T) {
	mgr, _, cleanup := setupTestTxManager(t)
	defer cleanup()

	ctx := context.Background()
	if mgr.State() != tx.StateIdle {
		t.Fatalf("expected StateIdle, got %v", mgr.State())
	}

	txCtx, err := mgr.Begin(ctx, tx.Options{Isolation: tx.ReadCommitted})
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if txCtx == nil || mgr.State() != tx.StateActive {
		t.Fatalf("expected StateActive, got %v", mgr.State())
	}

	// Deferred action check
	actionRan := false
	txCtx.AddDeferredAction(func() error {
		actionRan = true
		return nil
	})

	if err := mgr.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	if mgr.State() != tx.StateIdle {
		t.Fatalf("expected StateIdle after commit, got %v", mgr.State())
	}
	if !actionRan {
		t.Fatalf("expected deferred action to run on commit")
	}

	// Commit when idle must return ErrTxIdle
	if err := mgr.Commit(ctx); !errors.Is(err, tx.ErrTxIdle) {
		t.Fatalf("expected ErrTxIdle, got %v", err)
	}
}

func TestTxManager_ExplicitRollback(t *testing.T) {
	mgr, _, cleanup := setupTestTxManager(t)
	defer cleanup()

	ctx := context.Background()
	txCtx, err := mgr.Begin(ctx, tx.Options{Isolation: tx.ReadCommitted})
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	actionRan := false
	txCtx.AddDeferredAction(func() error {
		actionRan = true
		return nil
	})

	if err := mgr.Rollback(ctx); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
	if mgr.State() != tx.StateIdle {
		t.Fatalf("expected StateIdle after rollback, got %v", mgr.State())
	}
	if actionRan {
		t.Fatalf("deferred action should NOT run on rollback")
	}

	// Rollback when idle must return ErrTxIdle
	if err := mgr.Rollback(ctx); !errors.Is(err, tx.ErrTxIdle) {
		t.Fatalf("expected ErrTxIdle, got %v", err)
	}
}

func TestTxManager_DoubleBeginDefense(t *testing.T) {
	mgr, _, cleanup := setupTestTxManager(t)
	defer cleanup()

	ctx := context.Background()
	_, err := mgr.Begin(ctx, tx.Options{})
	if err != nil {
		t.Fatalf("first Begin failed: %v", err)
	}

	// Second explicit Begin must fail with ErrTxActive
	_, err2 := mgr.Begin(ctx, tx.Options{})
	if !errors.Is(err2, tx.ErrTxActive) {
		t.Fatalf("expected ErrTxActive, got %v", err2)
	}
}

func TestTxManager_InErrorStateAndSavepointRecovery(t *testing.T) {
	mgr, _, cleanup := setupTestTxManager(t)
	defer cleanup()

	ctx := context.Background()
	_, err := mgr.Begin(ctx, tx.Options{})
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	// 1. Create savepoint sp1
	if err := mgr.Savepoint("sp1"); err != nil {
		t.Fatalf("Savepoint sp1 failed: %v", err)
	}

	// 2. Mark error occurred
	mockErr := errors.New("syntax error in statement")
	mgr.SetInError(mockErr)

	if mgr.State() != tx.StateInError {
		t.Fatalf("expected StateInError, got %v", mgr.State())
	}

	// 3. In error state, Commit must fail and automatically rollback
	errCommit := mgr.Commit(ctx)
	if !errors.Is(errCommit, tx.ErrTxInError) {
		t.Fatalf("expected ErrTxInError on commit, got %v", errCommit)
	}
	if mgr.State() != tx.StateIdle {
		t.Fatalf("expected StateIdle after failed commit, got %v", mgr.State())
	}

	// 4. Test savepoint recovery: Begin -> savepoint -> InError -> RollbackTo -> Active
	_, err = mgr.Begin(ctx, tx.Options{})
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if err := mgr.Savepoint("sp_retry"); err != nil {
		t.Fatalf("Savepoint sp_retry failed: %v", err)
	}
	mgr.SetInError(mockErr)
	if mgr.State() != tx.StateInError {
		t.Fatalf("expected StateInError, got %v", mgr.State())
	}

	// Rollback to savepoint recovers to StateActive
	if err := mgr.RollbackTo("sp_retry"); err != nil {
		t.Fatalf("RollbackTo failed: %v", err)
	}
	if mgr.State() != tx.StateActive {
		t.Fatalf("expected StateActive after RollbackTo, got %v", mgr.State())
	}

	if err := mgr.Commit(ctx); err != nil {
		t.Fatalf("Commit after savepoint recovery failed: %v", err)
	}
}

func TestTxManager_EphemeralAutocommit(t *testing.T) {
	mgr, _, cleanup := setupTestTxManager(t)
	defer cleanup()

	ctx := context.Background()
	txCtx, err := mgr.BeginEphemeral(ctx, tx.Options{})
	if err != nil {
		t.Fatalf("BeginEphemeral failed: %v", err)
	}
	if !txCtx.Ephemeral {
		t.Fatalf("expected Ephemeral = true")
	}

	if err := mgr.Commit(ctx); err != nil {
		t.Fatalf("Commit ephemeral failed: %v", err)
	}
	if mgr.State() != tx.StateIdle {
		t.Fatalf("expected StateIdle, got %v", mgr.State())
	}
}

func TestTxManager_SessionSerialization(t *testing.T) {
	mgr, _, cleanup := setupTestTxManager(t)
	defer cleanup()

	const workers = 20
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(workers)

	counter := 0
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				mgr.Lock()
				// Protected critical section
				counter++
				mgr.Unlock()
			}
		}()
	}

	wg.Wait()
	if counter != workers*iterations {
		t.Fatalf("expected counter %d, got %d", workers*iterations, counter)
	}
}
