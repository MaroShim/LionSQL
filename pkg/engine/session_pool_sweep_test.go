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
	waitUntilCondition(t, time.Second, "idle client session storage detachment", func() bool {
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
	activeHasSess := activeCs.Session != nil && activeCs.Tx != nil
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
