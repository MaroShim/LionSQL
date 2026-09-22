package api_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"sqlion/pkg/api"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func TestSessionPool_AcquireReleaseAndStats(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cfg := api.PoolConfig{
		MaxWorkers:      4,
		MaxIdleSessions: 2,
		AcquireTimeout:  500 * time.Millisecond,
	}
	pool := api.NewSessionPool(eng, cfg)
	defer pool.Close()

	// 1. Initial stats check
	stats := pool.Stats()
	if stats.MaxWorkers != 4 || stats.MaxIdleSessions != 2 {
		t.Fatalf("unexpected pool config in stats: %+v", stats)
	}
	if stats.ActiveSessions != 0 || stats.IdleSessions != 0 {
		t.Fatalf("expected 0 initial sessions, got active=%d, idle=%d", stats.ActiveSessions, stats.IdleSessions)
	}

	// 2. Acquire and release workers
	ctx := context.Background()
	if err := pool.AcquireWorker(ctx); err != nil {
		t.Fatalf("failed to acquire worker: %v", err)
	}
	if pool.Stats().ActiveWorkers != 1 {
		t.Errorf("expected 1 active worker, got %d", pool.Stats().ActiveWorkers)
	}
	pool.ReleaseWorker()
	if pool.Stats().ActiveWorkers != 0 {
		t.Errorf("expected 0 active workers after release, got %d", pool.Stats().ActiveWorkers)
	}

	// 3. Acquire and release storage sessions (pool caching)
	s1, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("failed to acquire session 1: %v", err)
	}
	s2, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("failed to acquire session 2: %v", err)
	}
	if pool.Stats().ActiveSessions != 2 {
		t.Errorf("expected 2 active sessions, got %d", pool.Stats().ActiveSessions)
	}

	// Release s1 and s2 (both should enter idle list since MaxIdleSessions=2)
	pool.ReleaseSession(s1)
	pool.ReleaseSession(s2)
	if pool.Stats().IdleSessions != 2 || pool.Stats().ActiveSessions != 0 {
		t.Errorf("expected 2 idle and 0 active sessions, got %+v", pool.Stats())
	}

	// Re-acquire s3 should reuse an idle session without allocating new ones
	s3, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("failed to reacquire session: %v", err)
	}
	if pool.Stats().IdleSessions != 1 || pool.Stats().ActiveSessions != 1 {
		t.Errorf("expected 1 idle and 1 active session after reuse, got %+v", pool.Stats())
	}
	pool.ReleaseSession(s3)
}

func TestSessionPool_WorkerTimeoutAndBackpressure(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cfg := api.PoolConfig{
		MaxWorkers:      2,
		MaxIdleSessions: 2,
		AcquireTimeout:  50 * time.Millisecond,
	}
	pool := api.NewSessionPool(eng, cfg)
	defer pool.Close()

	ctx := context.Background()

	// Fill both worker slots
	if err := pool.AcquireWorker(ctx); err != nil {
		t.Fatalf("acquire worker 1 failed: %v", err)
	}
	if err := pool.AcquireWorker(ctx); err != nil {
		t.Fatalf("acquire worker 2 failed: %v", err)
	}

	// 3rd attempt should time out
	err := pool.AcquireWorker(ctx)
	if err != api.ErrPoolTimeout {
		t.Fatalf("expected ErrPoolTimeout, got: %v", err)
	}

	// Release one worker, next acquire should succeed immediately
	pool.ReleaseWorker()
	if err := pool.AcquireWorker(ctx); err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	pool.ReleaseWorker()
	pool.ReleaseWorker()
}

func TestSessionPool_MassiveConcurrentMultiplexing(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)

	cfg := api.PoolConfig{
		MaxWorkers:      8,
		MaxIdleSessions: 8,
		AcquireTimeout:  2 * time.Second,
	}
	pool := api.NewSessionPool(eng, cfg)
	defer pool.Close()

	const concurrentClients = 100
	var wg sync.WaitGroup
	wg.Add(concurrentClients)

	ctx := context.Background()

	for i := 0; i < concurrentClients; i++ {
		go func(id int) {
			defer wg.Done()

			// 1. Acquire execution worker
			if err := pool.AcquireWorker(ctx); err != nil {
				t.Errorf("client %d worker acquire failed: %v", id, err)
				return
			}
			defer pool.ReleaseWorker()

			// 2. Acquire engine session
			sess, err := pool.AcquireSession()
			if err != nil {
				t.Errorf("client %d session acquire failed: %v", id, err)
				return
			}
			defer pool.ReleaseSession(sess)

			// 3. Perform quick transaction
			tx, err := sess.BeginTx(storage.TxOptions{})
			if err != nil {
				t.Errorf("client %d begin tx failed: %v", id, err)
				return
			}
			_ = tx.Insert("items", storage.Record{
				"id":  storage.IntValue(int64(id + 1)),
				"val": storage.IntValue(int64(id * 10)),
			})
			_ = tx.Commit()
		}(i)
	}

	wg.Wait()

	stats := pool.Stats()
	if stats.ActiveSessions != 0 || stats.ActiveWorkers != 0 {
		t.Errorf("expected 0 active sessions and 0 active workers after completion, got %+v", stats)
	}
	if stats.TotalAcquired != concurrentClients {
		t.Errorf("expected %d total acquisitions, got %d", concurrentClients, stats.TotalAcquired)
	}
}
