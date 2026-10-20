package engine

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"lionsql/pkg/storage/memengine"
)

// AutoAnalyzeConfig holds threshold settings for background statistics updates.
type AutoAnalyzeConfig struct {
	Enabled      bool    `json:"enabled"`
	MinThreshold int64   `json:"min_threshold"` // Minimum dirty row mutations before triggering (default: 50)
	ScaleFactor  float64 `json:"scale_factor"`  // Mutation fraction of table rows to trigger analyze (default: 0.10)
}

// DefaultAutoAnalyzeConfig provides safe defaults aligned with PostgreSQL autovacuum_analyze standards.
func DefaultAutoAnalyzeConfig() AutoAnalyzeConfig {
	return AutoAnalyzeConfig{
		Enabled:      true,
		MinThreshold: 50,
		ScaleFactor:  0.10,
	}
}

// AutoAnalyzer tracks row mutation volumes and asynchronously refreshes CBO statistics.
type AutoAnalyzer struct {
	coordinator *Coordinator
	cfg         AutoAnalyzeConfig
	dirtyRows   sync.Map // lowercase table name -> *atomic.Int64
	inProgress  sync.Map // lowercase table name -> *atomic.Bool
	closed      atomic.Bool
}

// NewAutoAnalyzer creates an auto-analyzer manager attached to the given coordinator.
func NewAutoAnalyzer(coord *Coordinator, cfg AutoAnalyzeConfig) *AutoAnalyzer {
	if cfg.MinThreshold <= 0 {
		cfg.MinThreshold = 50
	}
	if cfg.ScaleFactor <= 0 {
		cfg.ScaleFactor = 0.10
	}
	return &AutoAnalyzer{
		coordinator: coord,
		cfg:         cfg,
	}
}

// OnWALRecord is the storage commit callback that tallies dirty mutations per table.
func (a *AutoAnalyzer) OnWALRecord(rec memengine.WALRecord) {
	if a.closed.Load() || !a.cfg.Enabled || len(rec.Ops) == 0 {
		return
	}

	counts := make(map[string]int64)
	for _, op := range rec.Ops {
		switch op.Type {
		case memengine.OpInsert, memengine.OpUpdate, memengine.OpDelete:
			if op.Table != "" {
				tbl := strings.ToLower(op.Table)
				counts[tbl]++
			}
		}
	}

	for tbl, cnt := range counts {
		a.RecordMutation(tbl, cnt)
	}
}

// RecordMutation increments the dirty count for a table and triggers background analysis if min threshold is reached.
func (a *AutoAnalyzer) RecordMutation(table string, count int64) {
	if a.closed.Load() || !a.cfg.Enabled || count <= 0 {
		return
	}
	tblKey := strings.ToLower(table)

	val, _ := a.dirtyRows.LoadOrStore(tblKey, new(atomic.Int64))
	counter := val.(*atomic.Int64)
	newDirty := counter.Add(count)

	if newDirty >= a.cfg.MinThreshold {
		a.triggerAnalyze(tblKey, counter)
	}
}

func (a *AutoAnalyzer) triggerAnalyze(table string, counter *atomic.Int64) {
	val, _ := a.inProgress.LoadOrStore(table, new(atomic.Bool))
	running := val.(*atomic.Bool)
	if !running.CompareAndSwap(false, true) {
		// Analysis is already in progress for this table
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[AutoAnalyze] Panic recovered while analyzing %q: %v", table, r)
			}
			running.Store(false)
		}()

		// Outside transaction commit locks: safely evaluate dynamic scale factor if stats exist
		dirty := counter.Swap(0)
		if a.coordinator != nil && a.coordinator.catalog != nil {
			if stats, ok := a.coordinator.catalog.GetTableStats(table); ok && stats != nil && stats.RowCount > 0 {
				threshold := a.cfg.MinThreshold + int64(float64(stats.RowCount)*a.cfg.ScaleFactor)
				if dirty < threshold {
					// Restore dirty count and skip this pass
					counter.Add(dirty)
					return
				}
			}
		}

		if a.coordinator != nil {
			_, err := a.coordinator.AnalyzeTable(table)
			if err != nil {
				log.Printf("[AutoAnalyze] Warning: Auto-analyze on %q returned: %v", table, err)
			}
		}
	}()
}

// DirtyCount returns the current dirty row count for the table.
func (a *AutoAnalyzer) DirtyCount(table string) int64 {
	tblKey := strings.ToLower(table)
	if val, ok := a.dirtyRows.Load(tblKey); ok {
		return val.(*atomic.Int64).Load()
	}
	return 0
}

// Reset clears the dirty counter for the specified table.
func (a *AutoAnalyzer) Reset(table string) {
	tblKey := strings.ToLower(table)
	if val, ok := a.dirtyRows.Load(tblKey); ok {
		val.(*atomic.Int64).Store(0)
	}
}

// Close gracefully closes the analyzer.
func (a *AutoAnalyzer) Close() {
	a.closed.Store(true)
}
