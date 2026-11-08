package backup_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lionsql/pkg/backup"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestWALArchiver_Regression_P0_15_ConcurrentWriteTruncateLoss verifies that
// concurrent transaction writes during RotateAndArchive are NEVER truncated or lost.
func TestWALArchiver_Regression_P0_15_ConcurrentWriteTruncateLoss(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_p0_15_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	archiveDir := filepath.Join(tmpDir, "wal_archive")
	walPath := filepath.Join(tmpDir, "active.wal")

	archiver, err := backup.NewWALArchiver(archiveDir)
	if err != nil {
		t.Fatalf("NewWALArchiver failed: %v", err)
	}

	writer, err := memengine.NewWALWriter(walPath, memengine.FsyncAlways)
	if err != nil {
		t.Fatalf("NewWALWriter failed: %v", err)
	}
	defer writer.Close()

	// Initial batch: write 50 records
	t0 := time.Now().UnixNano()
	for i := int64(1); i <= 50; i++ {
		err := writer.Append(memengine.WALRecord{
			LSN:       i,
			Timestamp: t0 + i*1000,
			Ops: []memengine.WALOp{
				{
					Type:  memengine.OpInsert,
					Table: "accounts",
					Key:   fmt.Sprintf("%d", i),
					Record: storage.Record{
						"id":   storage.IntValue(i),
						"name": storage.StringValue(fmt.Sprintf("user_%d", i)),
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("Initial Append failed: %v", err)
		}
	}

	// Now launch concurrent writer while RotateAndArchive is running
	var stopWrite atomic.Bool
	var writtenCount atomic.Int64
	writtenCount.Store(50) // initial 50 records

	var writerWg sync.WaitGroup
	writerWg.Add(1)

	go func() {
		defer writerWg.Done()
		currLSN := int64(51)
		for !stopWrite.Load() {
			err := writer.Append(memengine.WALRecord{
				LSN:       currLSN,
				Timestamp: time.Now().UnixNano(),
				Ops: []memengine.WALOp{
					{
						Type:  memengine.OpInsert,
						Table: "accounts",
						Key:   fmt.Sprintf("%d", currLSN),
						Record: storage.Record{
							"id":   storage.IntValue(currLSN),
							"name": storage.StringValue(fmt.Sprintf("user_%d", currLSN)),
						},
					},
				},
			})
			if err == nil {
				writtenCount.Store(currLSN)
				currLSN++
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()

	// Perform RotateAndArchive
	time.Sleep(2 * time.Millisecond) // let concurrent writer start
	seg, err := archiver.RotateAndArchive(writer, walPath)
	if err != nil {
		t.Fatalf("RotateAndArchive failed: %v", err)
	}

	// Stop concurrent writer
	stopWrite.Store(true)
	writerWg.Wait()

	totalExpected := writtenCount.Load()

	// Now count records:
	// 1. Records in archived segment
	archivedCount := seg.RecordCount

	// 2. Records remaining in active WAL (or secondary archive)
	var activeCount int64
	_, _ = memengine.ReplayWAL(walPath, func(rec memengine.WALRecord) error {
		activeCount++
		return nil
	})

	totalFound := archivedCount + activeCount
	t.Logf("[P0-15 Audit] Expected total: %d, Archived: %d, Active WAL: %d (Sum: %d)",
		totalExpected, archivedCount, activeCount, totalFound)

	if totalFound != totalExpected {
		t.Fatalf("[P0-15 CRITICAL DATA LOSS DETECTED] Total expected transactions=%d, but only found=%d (Archived=%d, Active=%d). Lost %d transactions due to Truncate race!",
			totalExpected, totalFound, archivedCount, activeCount, totalExpected-totalFound)
	}
}
