package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

type sweepCountingSession struct {
	storage.Session
	resets *atomic.Int64
	closes *atomic.Int64
}

func (s *sweepCountingSession) Reset() error {
	s.resets.Add(1)
	return nil
}

func (s *sweepCountingSession) Close() error {
	s.closes.Add(1)
	return s.Session.Close()
}

type sweepCountingEngine struct {
	storage.Engine
	resets atomic.Int64
	closes atomic.Int64
}

func (e *sweepCountingEngine) NewSession() (storage.Session, error) {
	sess, err := e.Engine.NewSession()
	if err != nil {
		return nil, err
	}
	return &sweepCountingSession{
		Session: sess,
		resets:  &e.resets,
		closes:  &e.closes,
	}, nil
}

func waitUntilCondition(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, desc)
		}
		<-ticker.C
	}
}

func TestSessionPool_BackgroundSweep_ResetsAndExpiresAllIdleSessions(t *testing.T) {
	eng := &sweepCountingEngine{Engine: memengine.New()}
	pool := NewSessionPool(eng, PoolConfig{
		MaxWorkers:      8,
		MaxIdleSessions: 8,
		AcquireTimeout:  time.Second,
		IdleSessionTTL:  80 * time.Millisecond,
		SweepInterval:   20 * time.Millisecond,
	})
	defer func() {
		if err := pool.Close(); err != nil {
			t.Fatalf("first pool.Close failed: %v", err)
		}
		// [RULE:DoubleCloseSafety] Double close must be idempotent without panic.
		if err := pool.Close(); err != nil {
			t.Fatalf("second pool.Close failed: %v", err)
		}
	}()

	// Seed 3 concurrent sessions into idleSessions: index 0 and 1 sit deeper in the LIFO stack.
	s1, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("AcquireSession 1 failed: %v", err)
	}
	s2, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("AcquireSession 2 failed: %v", err)
	}
	s3, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("AcquireSession 3 failed: %v", err)
	}
	pool.ReleaseSession(s1)
	pool.ReleaseSession(s2)
	pool.ReleaseSession(s3) // 3 initial resets on ReleaseSession

	// Repeatedly exercise only the LIFO top session (simulating periodic health ping)
	// and verify the buried sessions still get reset & eventually closed by background Sweep().
	top, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("AcquireSession top failed: %v", err)
	}
	pool.ReleaseSession(top)

	waitUntilCondition(t, time.Second, "periodic resets on idle sessions", func() bool {
		return eng.resets.Load() >= 6
	})

	waitUntilCondition(t, 2*time.Second, "all expired idle sessions reaped", func() bool {
		return eng.closes.Load() == 3 && pool.Stats().IdleSessions == 0
	})
}

func TestCoordinator_ReapIdleClientSessions_ReclaimsAndReacquiresSafely(t *testing.T) {
	eng := &sweepCountingEngine{Engine: memengine.New()}
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{
		ClientSessionIdleTTL: 30 * time.Millisecond,
		SessionSweepInterval: 10 * time.Millisecond,
		PoolConfig: PoolConfig{
			MaxWorkers:      8,
			MaxIdleSessions: 8,
			AcquireTimeout:  time.Second,
			IdleSessionTTL:  5 * time.Second,
			SweepInterval:   2 * time.Second,
		},
	})
	defer coord.Close()

	ctx := context.Background()
	if _, err := coord.ExecuteSessionSQLContext(ctx, "idle-client", "CREATE TABLE t_sweep (id INT PRIMARY KEY, v INT)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Open an active transaction on another session; it MUST NOT be reaped while tx is active.
	if err := coord.BeginSessionTx("active-tx-client", storage.TxOptions{}); err != nil {
		t.Fatalf("BeginSessionTx active-tx-client failed: %v", err)
	}

	// Simulate idle-client becoming idle beyond ClientSessionIdleTTL and wait for background reaper.
	// Meanwhile, active-tx-client is actively utilized so it is not treated as an abandoned transaction.
	waitUntilCondition(t, time.Second, "idle client session storage detachment", func() bool {
		if actCs := coord.GetSession("active-tx-client"); actCs != nil {
			actCs.Mu.Lock()
			actCs.lastUsedAt = time.Now()
			actCs.Mu.Unlock()
		}
		cs := coord.GetSession("idle-client")
		if cs == nil {
			return false
		}
		cs.Mu.Lock()
		detached := cs.Session == nil
		cs.Mu.Unlock()
		return detached
	})

	// Active transaction client MUST retain its storage session.
	activeCs := coord.GetSession("active-tx-client")
	activeCs.Mu.Lock()
	activeHasSess := activeCs.Session != nil && activeCs.Tx() != nil
	activeCs.Mu.Unlock()
	if !activeHasSess {
		t.Fatalf("active transaction session must not have cs.Session detached")
	}
	if err := coord.RollbackSessionTx("active-tx-client"); err != nil {
		t.Fatalf("RollbackSessionTx failed: %v", err)
	}

	// Re-awakening the idle client with explicit BEGIN -> INSERT -> COMMIT must lazily re-acquire cs.Session cleanly.
	if err := coord.BeginSessionTx("idle-client", storage.TxOptions{}); err != nil {
		t.Fatalf("BeginSessionTx after idle detach failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, "idle-client", "INSERT INTO t_sweep (id, v) VALUES (1, 42)"); err != nil {
		t.Fatalf("INSERT after idle detach failed: %v", err)
	}
	if err := coord.CommitSessionTx("idle-client"); err != nil {
		t.Fatalf("CommitSessionTx after idle detach failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQLContext(ctx, "idle-client", "SELECT v FROM t_sweep WHERE id = 1")
	if err != nil {
		t.Fatalf("SELECT after idle detach failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["v"] != int64(42) {
		t.Fatalf("unexpected row after idle reacquire: %+v", res.Rows)
	}
}

func TestCoordinator_ReapIdleClientSessions_AbandonedTransactionRollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{
		ClientSessionIdleTTL: 20 * time.Millisecond,
		SessionSweepInterval: 10 * time.Millisecond,
	})
	defer coord.Close()

	ctx := context.Background()
	_, err := coord.ExecuteSessionSQLContext(ctx, "sess-abandoned", "CREATE TABLE t_abandoned (id INT PRIMARY KEY, val INT)")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Begin a transaction on sess-abandoned and insert a row without committing
	if err := coord.BeginSessionTx("sess-abandoned", storage.TxOptions{}); err != nil {
		t.Fatalf("BeginSessionTx failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQLContext(ctx, "sess-abandoned", "INSERT INTO t_abandoned VALUES (1, 100)")
	if err != nil {
		t.Fatalf("INSERT in transaction failed: %v", err)
	}

	// Verify the session has active transaction and pinned session
	cs := coord.GetSession("sess-abandoned")
	if cs == nil {
		t.Fatalf("expected session sess-abandoned to exist")
	}
	cs.Mu.Lock()
	hasTx := cs.Tx() != nil && cs.Session != nil
	cs.Mu.Unlock()
	if !hasTx {
		t.Fatalf("expected sess-abandoned to have active transaction and session")
	}

	// Wait for idleTTL (20ms) to elapse, triggering ReapIdleClientSessions
	waitUntilCondition(t, 500*time.Millisecond, "abandoned transaction rollback and session reap", func() bool {
		cs.Mu.Lock()
		defer cs.Mu.Unlock()
		return cs.Tx() == nil && cs.Session == nil
	})

	// After rollback and reap, verify the uncommitted row was NOT persisted
	res, err := coord.ExecuteSQL("SELECT * FROM t_abandoned WHERE id = 1")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected uncommitted transaction to be rolled back (0 rows), got %d rows", len(res.Rows))
	}
}

func TestCoordinator_ShowIdleInTransactionSessionTimeoutAndDefaults(t *testing.T) {
	defCfg := DefaultConfig()
	if defCfg.ClientSessionIdleTTL != 5*time.Minute {
		t.Fatalf("expected default ClientSessionIdleTTL = 5m, got %v", defCfg.ClientSessionIdleTTL)
	}
	if defCfg.SessionSweepInterval != 10*time.Second {
		t.Fatalf("expected default SessionSweepInterval = 10s, got %v", defCfg.SessionSweepInterval)
	}

	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, defCfg)
	defer coord.Close()

	res, err := coord.ExecuteSQL("SHOW idle_in_transaction_session_timeout;")
	if err != nil {
		t.Fatalf("SHOW idle_in_transaction_session_timeout failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["idle_in_transaction_session_timeout"] != "5min" {
		t.Fatalf("expected '5min', got %+v", res.Rows)
	}

	coord.SetClientSessionIdleTTL(30 * time.Second)
	res2, err := coord.ExecuteSQL("SHOW idle_in_transaction_session_timeout;")
	if err != nil {
		t.Fatalf("SHOW after SetClientSessionIdleTTL failed: %v", err)
	}
	if len(res2.Rows) != 1 || res2.Rows[0]["idle_in_transaction_session_timeout"] != "30s" {
		t.Fatalf("expected '30s', got %+v", res2.Rows)
	}
}
