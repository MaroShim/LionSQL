package engine

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"lionsql/pkg/storage"
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
	IdleSessionTTL  time.Duration // Maximum idle time before an idle session is reaped (default 30m)
	SweepInterval   time.Duration // Interval for background TTL reaping and periodic session reset (default 5m)
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
		IdleSessionTTL:  30 * time.Minute,
		SweepInterval:   5 * time.Minute,
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

type idleSessionItem struct {
	sess      storage.Session
	idleTime  time.Time
	lastReset time.Time
}

// SessionPool manages multiplexed access to storage engine sessions across thousands of client connections.
type SessionPool struct {
	engine storage.Engine
	config PoolConfig

	mu           sync.Mutex
	idleSessions []idleSessionItem
	semWorkers   chan struct{} // limits concurrent active execution workers
	closed       bool

	stopCh    chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once

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
	if cfg.IdleSessionTTL <= 0 {
		cfg.IdleSessionTTL = 30 * time.Minute
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = 5 * time.Minute
		if cfg.IdleSessionTTL < cfg.SweepInterval {
			cfg.SweepInterval = cfg.IdleSessionTTL
		}
		if cfg.SweepInterval < 10*time.Millisecond {
			cfg.SweepInterval = 10 * time.Millisecond
		}
	}

	p := &SessionPool{
		engine:       eng,
		config:       cfg,
		idleSessions: make([]idleSessionItem, 0, cfg.MaxIdleSessions),
		semWorkers:   make(chan struct{}, cfg.MaxWorkers),
		stopCh:       make(chan struct{}),
	}
	p.wg.Add(1)
	go p.sweepLoop()
	return p
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

	var expired []storage.Session
	var acquired storage.Session
	for len(p.idleSessions) > 0 {
		n := len(p.idleSessions)
		item := p.idleSessions[n-1]
		p.idleSessions[n-1] = idleSessionItem{}
		p.idleSessions = p.idleSessions[:n-1]
		if p.config.IdleSessionTTL > 0 && time.Since(item.idleTime) > p.config.IdleSessionTTL {
			expired = append(expired, item.sess)
			continue
		}
		acquired = item.sess
		break
	}
	p.mu.Unlock()

	for _, s := range expired {
		_ = s.Close()
	}

	if acquired != nil {
		atomic.AddInt64(&p.activeSessions, 1)
		atomic.AddInt64(&p.totalAcquired, 1)
		return acquired, nil
	}

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

	if r, ok := sess.(interface{ Reset() error }); ok {
		_ = r.Reset()
	}

	p.mu.Lock()
	if p.closed || len(p.idleSessions) >= p.config.MaxIdleSessions {
		p.mu.Unlock()
		_ = sess.Close()
		return
	}

	now := time.Now()
	p.idleSessions = append(p.idleSessions, idleSessionItem{
		sess:      sess,
		idleTime:  now,
		lastReset: now,
	})
	p.mu.Unlock()
}

func (p *SessionPool) sweepLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.config.SweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.Sweep()
		}
	}
}

// Sweep expires idle sessions exceeding IdleSessionTTL across the entire pool
// and resets remaining idle sessions that have been inactive for SweepInterval
// so WiredTiger's sweep-server does not emit 60-minute idle session warnings.
func (p *SessionPool) Sweep() (closedCount, resetCount int) {
	now := time.Now()
	p.mu.Lock()
	if p.closed || len(p.idleSessions) == 0 {
		p.mu.Unlock()
		return 0, 0
	}

	var toClose []storage.Session
	var toReset []idleSessionItem
	kept := make([]idleSessionItem, 0, len(p.idleSessions))

	for _, item := range p.idleSessions {
		if p.config.IdleSessionTTL > 0 && now.Sub(item.idleTime) > p.config.IdleSessionTTL {
			toClose = append(toClose, item.sess)
		} else if p.config.SweepInterval > 0 && now.Sub(item.lastReset) >= p.config.SweepInterval {
			toReset = append(toReset, item)
		} else {
			kept = append(kept, item)
		}
	}
	p.idleSessions = kept
	p.mu.Unlock()

	for _, s := range toClose {
		_ = s.Close()
		closedCount++
	}

	if len(toReset) == 0 {
		return closedCount, 0
	}

	var resetOK []idleSessionItem
	for _, item := range toReset {
		if r, ok := item.sess.(interface{ Reset() error }); ok {
			if err := r.Reset(); err != nil {
				_ = item.sess.Close()
				closedCount++
				continue
			}
		}
		resetCount++
		item.lastReset = time.Now()
		resetOK = append(resetOK, item)
	}

	if len(resetOK) > 0 {
		var overflow []storage.Session
		p.mu.Lock()
		for _, item := range resetOK {
			if p.closed || len(p.idleSessions) >= p.config.MaxIdleSessions {
				overflow = append(overflow, item.sess)
			} else {
				p.idleSessions = append(p.idleSessions, item)
			}
		}
		p.mu.Unlock()
		for _, s := range overflow {
			_ = s.Close()
			closedCount++
		}
	}
	return closedCount, resetCount
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

// Close stops the background sweep goroutine and closes all idle sessions in the pool.
func (p *SessionPool) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()

		close(p.stopCh)
		p.wg.Wait()

		p.mu.Lock()
		toClose := p.idleSessions
		p.idleSessions = nil
		p.mu.Unlock()

		for _, item := range toClose {
			_ = item.sess.Close()
		}
	})
	return nil
}
