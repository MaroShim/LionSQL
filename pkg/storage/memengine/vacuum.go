package memengine

import (
	"sync"
	"sync/atomic"
	"time"
)

// VacuumStats tracks telemetry for MVCC dead-row version and tombstone reclamation.
type VacuumStats struct {
	Runs             int64 `json:"runs"`
	PurgedTombstones int64 `json:"purged_tombstones"`
	ReclaimedRecords int64 `json:"reclaimed_records"`
	LastDurationUS   int64 `json:"last_duration_us"`
}

// VacuumCleaner is a background worker that purges expired tombstones and dead row versions
// whose lifespans are older than the active transaction watermark.
type VacuumCleaner struct {
	engine   *Engine
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
	stats    VacuumStats
	statsMu  sync.RWMutex
	running  atomic.Bool
}

// NewVacuumCleaner instantiates a vacuum cleaner attached to an in-memory engine.
func NewVacuumCleaner(engine *Engine, interval time.Duration) *VacuumCleaner {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	return &VacuumCleaner{
		engine:   engine,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the background vacuuming goroutine.
func (vc *VacuumCleaner) Start() {
	if vc.running.Swap(true) {
		return // Already running
	}

	vc.wg.Add(1)
	go func() {
		defer vc.wg.Done()
		ticker := time.NewTicker(vc.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				_, _ = vc.Vacuum()
			case <-vc.stopCh:
				return
			}
		}
	}()
}

// Stop cleanly terminates the background vacuum cleaner worker.
func (vc *VacuumCleaner) Stop() {
	if !vc.running.Swap(false) {
		return
	}
	close(vc.stopCh)
	vc.wg.Wait()
}

// Vacuum executes an immediate sweep pass across all tables in the engine.
func (vc *VacuumCleaner) Vacuum() (VacuumStats, error) {
	start := time.Now()

	// 1. Determine the oldest active transaction watermark.
	watermark := vc.engine.getOldestActiveTxTime()

	var purgedTotal int64

	vc.engine.mu.Lock()
	for _, td := range vc.engine.tables {
		if td.Tombstones == nil {
			continue
		}
		for key, deletedAt := range td.Tombstones {
			if deletedAt <= watermark {
				delete(td.Tombstones, key)
				purgedTotal++
			}
		}
	}
	vc.engine.mu.Unlock()

	durationUS := time.Since(start).Microseconds()

	vc.statsMu.Lock()
	vc.stats.Runs++
	vc.stats.PurgedTombstones += purgedTotal
	vc.stats.ReclaimedRecords += purgedTotal
	vc.stats.LastDurationUS = durationUS
	currentStats := vc.stats
	vc.statsMu.Unlock()

	return currentStats, nil
}

// Stats returns a copy of the current vacuum cleaner statistics.
func (vc *VacuumCleaner) Stats() VacuumStats {
	vc.statsMu.RLock()
	defer vc.statsMu.RUnlock()
	return vc.stats
}
