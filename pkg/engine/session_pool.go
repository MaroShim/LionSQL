package engine

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/storage"
)

var (
	ErrPoolClosed    = errors.New("session pool is closed")
	ErrPoolTimeout   = errors.New("timeout waiting for available session from pool")
	ErrPoolExhausted = errors.New("maximum active sessions reached and queue full")
)

// PoolConfig specifies parameters for the built-in Goroutine Connection Pooler.
type PoolConfig struct {
	MaxWorkers      int           // Maximum concurrent query execution workers
	MaxIdleSessions int           // Maximum cached idle storage sessions
	AcquireTimeout  time.Duration // Timeout for waiting for a session from the pool
}

// DefaultPoolConfig returns sensible production defaults for connection pooling.
func DefaultPoolConfig() PoolConfig {
	workers := runtime.NumCPU() * 4
	if workers < 16 {
		workers = 16
	}
	return PoolConfig{
		MaxWorkers:      workers,
		MaxIdleSessions: 64,
		AcquireTimeout:  10 * time.Second,
	}
}

// PoolStats holds snapshot metrics for monitoring the connection pool.
type PoolStats struct {
	MaxWorkers       int   `json:"max_workers"`
	MaxIdleSessions  int   `json:"max_idle_sessions"`
	ActiveWorkers    int64 `json:"active_workers"`
	ActiveSessions   int64 `json:"active_sessions"`
	IdleSessions     int   `json:"idle_sessions"`
	QueuedRequests   int64 `json:"queued_requests"`
	TotalAcquired    int64 `json:"total_acquired"`
	TotalReleased    int64 `json:"total_released"`
	TotalTimeouts    int64 `json:"total_timeouts"`
	WaitDurationNano int64 `json:"wait_duration_ns"`
}

// SessionPool manages multiplexed access to storage engine sessions across thousands of client connections.
type SessionPool struct {
	engine storage.Engine
	config PoolConfig

	mu           sync.Mutex
	idleSessions []storage.Session
	semWorkers   chan struct{} // limits concurrent active execution workers
	closed       bool

	// Realtime metrics
	activeWorkers    int64
	activeSessions   int64
	queuedRequests   int64
	totalAcquired    int64
	totalReleased    int64
	totalTimeouts    int64
	waitDurationNano int64
}

// NewSessionPool creates a new Goroutine Connection Pooler for the given storage engine.
func NewSessionPool(eng storage.Engine, cfg PoolConfig) *SessionPool {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = runtime.NumCPU() * 4
		if cfg.MaxWorkers < 16 {
			cfg.MaxWorkers = 16
		}
	}
	if cfg.MaxIdleSessions <= 0 {
		cfg.MaxIdleSessions = 64
	}
	if cfg.AcquireTimeout <= 0 {
		cfg.AcquireTimeout = 10 * time.Second
	}

	return &SessionPool{
		engine:       eng,
		config:       cfg,
		idleSessions: make([]storage.Session, 0, cfg.MaxIdleSessions),
		semWorkers:   make(chan struct{}, cfg.MaxWorkers),
	}
}

// AcquireWorker acquires a slot in the concurrent execution worker pool with backpressure.
func (p *SessionPool) AcquireWorker(ctx context.Context) error {
	atomic.AddInt64(&p.queuedRequests, 1)
	defer atomic.AddInt64(&p.queuedRequests, -1)

	start := time.Now()

	timeout := p.config.AcquireTimeout
	var timer *time.Timer
	var timeoutCh <-chan time.Time

	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timeoutCh:
		atomic.AddInt64(&p.totalTimeouts, 1)
		return ErrPoolTimeout
	case p.semWorkers <- struct{}{}:
		waitNs := time.Since(start).Nanoseconds()
		atomic.AddInt64(&p.waitDurationNano, waitNs)
		atomic.AddInt64(&p.activeWorkers, 1)
		return nil
	}
}

// ReleaseWorker frees a slot in the concurrent execution worker pool.
func (p *SessionPool) ReleaseWorker() {
	select {
	case <-p.semWorkers:
		atomic.AddInt64(&p.activeWorkers, -1)
	default:
	}
}

// AcquireSession retrieves an idle storage session from the pool or allocates a new one.
func (p *SessionPool) AcquireSession() (storage.Session, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}

	n := len(p.idleSessions)
	if n > 0 {
		sess := p.idleSessions[n-1]
		p.idleSessions = p.idleSessions[:n-1]
		p.mu.Unlock()
		atomic.AddInt64(&p.activeSessions, 1)
		atomic.AddInt64(&p.totalAcquired, 1)
		return sess, nil
	}
	p.mu.Unlock()

	// Create new session from engine
	sess, err := p.engine.NewSession()
	if err != nil {
		return nil, err
	}

	atomic.AddInt64(&p.activeSessions, 1)
	atomic.AddInt64(&p.totalAcquired, 1)
	return sess, nil
}

// ReleaseSession returns an idle storage session back to the pool or closes it if the pool is full.
func (p *SessionPool) ReleaseSession(sess storage.Session) {
	if sess == nil {
		return
	}

	atomic.AddInt64(&p.activeSessions, -1)
	atomic.AddInt64(&p.totalReleased, 1)

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || len(p.idleSessions) >= p.config.MaxIdleSessions {
		_ = sess.Close()
		return
	}

	p.idleSessions = append(p.idleSessions, sess)
}

// Stats returns a snapshot of current connection pool and worker metrics.
func (p *SessionPool) Stats() PoolStats {
	p.mu.Lock()
	idleCount := len(p.idleSessions)
	p.mu.Unlock()

	return PoolStats{
		MaxWorkers:       p.config.MaxWorkers,
		MaxIdleSessions:  p.config.MaxIdleSessions,
		ActiveWorkers:    atomic.LoadInt64(&p.activeWorkers),
		ActiveSessions:   atomic.LoadInt64(&p.activeSessions),
		IdleSessions:     idleCount,
		QueuedRequests:   atomic.LoadInt64(&p.queuedRequests),
		TotalAcquired:    atomic.LoadInt64(&p.totalAcquired),
		TotalReleased:    atomic.LoadInt64(&p.totalReleased),
		TotalTimeouts:    atomic.LoadInt64(&p.totalTimeouts),
		WaitDurationNano: atomic.LoadInt64(&p.waitDurationNano),
	}
}

// Close closes all idle sessions in the pool.
func (p *SessionPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	for _, sess := range p.idleSessions {
		_ = sess.Close()
	}
	p.idleSessions = nil
	return nil
}
