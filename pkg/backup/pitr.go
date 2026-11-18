package backup

import (
	"errors"
	"fmt"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// ErrPITRTargetReached is a sentinel error signaling that the target recovery point was reached.
var ErrPITRTargetReached = errors.New("PITR target reached")

// ErrPITRTargetBeforeBase is returned when the target recovery point is earlier than the base backup.
var ErrPITRTargetBeforeBase = errors.New("PITR target is before base backup")

// ErrPITRLSNGapDetected is returned when an LSN gap is detected during PITR recovery.
var ErrPITRLSNGapDetected = errors.New("PITR LSN gap detected: missing WAL records")

// PITROptions specifies target parameters for Point-In-Time Recovery.
type PITROptions struct {
	RestoreOptions RestoreOptions
	TargetTime     time.Time // Recover up to this timestamp (inclusive). If zero, ignored.
	TargetLSN      int64     // Recover up to this LSN (inclusive). If 0, ignored.
}

// PITRResult returns detailed operational metrics from a completed PITR restoration.
type PITRResult struct {
	BaseResult             *RestoreResult `json:"base_result,omitempty"`
	ArchivedSegmentsParsed int            `json:"archived_segments_parsed"`
	WALRecordsReplayed     int64          `json:"wal_records_replayed"`
	TargetReached          bool           `json:"target_reached"`
	FinalLSN               int64          `json:"final_lsn"`
	FinalTimestamp         time.Time      `json:"final_timestamp"`
	Duration               time.Duration  `json:"duration_ms"`
}

// ApplyWALRecordToEngine applies a single committed WAL batch to any storage engine.
// ApplyWALRecordToEngine applies a single committed WAL batch to any storage engine.
// If the underlying engine is *memengine.Engine, it uses direct memory mutation for maximum throughput.
// Otherwise, it replays operations through the transactional engine contract.
func ApplyWALRecordToEngine(eng storage.Engine, rec memengine.WALRecord) error {
	return ApplyWALRecordWithOptions(eng, rec, nil)
}

// ApplyWALRecordWithOptions applies a WAL record and invokes an optional ddlHook callback for catalog synchronization.
func ApplyWALRecordWithOptions(eng storage.Engine, rec memengine.WALRecord, ddlHook func(op memengine.WALOp)) error {
	if me, ok := eng.(*memengine.Engine); ok {
		err := me.ApplyWALRecord(rec)
		if err == nil && ddlHook != nil {
			for _, op := range rec.Ops {
				switch op.Type {
				case memengine.OpCreateTable, memengine.OpDropTable, memengine.OpTruncateTable,
					memengine.OpCreateIndex, memengine.OpDropIndex, memengine.OpDropPartition,
					memengine.OpRenameTable, memengine.OpAlterTable:
					ddlHook(op)
				}
			}
		}
		return err
	}

	var (
		sess storage.Session
		tx   storage.Transaction
	)

	ensureTx := func() error {
		if tx != nil {
			return nil
		}
		var err error
		sess, err = eng.NewSession()
		if err != nil {
			return err
		}
		tx, err = sess.BeginTx(storage.TxOptions{})
		if err != nil {
			_ = sess.Close()
			sess = nil
			return err
		}
		return nil
	}

	commitTx := func() error {
		if tx == nil {
			return nil
		}
		err := tx.Commit()
		_ = sess.Close()
		tx = nil
		sess = nil
		return err
	}

	rollbackTx := func() {
		if tx != nil {
			_ = tx.Rollback()
			_ = sess.Close()
			tx = nil
			sess = nil
		}
	}

	defer func() {
		rollbackTx()
	}()

	for _, op := range rec.Ops {
		switch op.Type {
		case memengine.OpCreateTable:
			if err := commitTx(); err != nil {
				return err
			}
			if op.Schema != nil {
				if err := eng.CreateTable(op.Schema); err != nil {
					return fmt.Errorf("pitr apply create table %s failed: %w", op.Table, err)
				}
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpDropTable:
			if err := commitTx(); err != nil {
				return err
			}
			if err := eng.DropTable(op.Table); err != nil && err != storage.ErrTableNotFound {
				return fmt.Errorf("pitr apply drop table %s failed: %w", op.Table, err)
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpTruncateTable:
			if err := commitTx(); err != nil {
				return err
			}
			if err := eng.TruncateTable(op.Table); err != nil && err != storage.ErrTableNotFound {
				return fmt.Errorf("pitr apply truncate table %s failed: %w", op.Table, err)
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpDropPartition:
			if err := commitTx(); err != nil {
				return err
			}
			if err := eng.DropPartition(op.Table, op.Key); err != nil {
				return fmt.Errorf("pitr apply drop partition %s.%s failed: %w", op.Table, op.Key, err)
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpCreateIndex:
			if err := commitTx(); err != nil {
				return err
			}
			if op.IndexSchema != nil {
				if err := eng.CreateIndex(op.IndexSchema); err != nil {
					return fmt.Errorf("pitr apply create index on %s failed: %w", op.Table, err)
				}
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpDropIndex:
			if err := commitTx(); err != nil {
				return err
			}
			if err := eng.DropIndex(op.Table, op.IndexName); err != nil && err != storage.ErrTableNotFound {
				return fmt.Errorf("pitr apply drop index %s on %s failed: %w", op.IndexName, op.Table, err)
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpRenameTable:
			if err := commitTx(); err != nil {
				return err
			}
			if err := eng.RenameTable(op.Key, op.Table); err != nil && err != storage.ErrTableNotFound {
				return fmt.Errorf("pitr apply rename table %s to %s failed: %w", op.Key, op.Table, err)
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpInsert:
			if err := ensureTx(); err != nil {
				return err
			}
			rec := op.Record
			if rec == nil && len(op.Data) > 0 {
				tblSchema, err := eng.GetTable(op.Table)
				if err != nil {
					return fmt.Errorf("pitr apply insert get table %s failed: %w", op.Table, err)
				}
				rec, err = storage.DecodeTuple(tblSchema, op.Data)
				if err != nil {
					return fmt.Errorf("pitr apply insert decode tuple failed on table %s (pk=%s): %w", op.Table, op.Key, err)
				}
			}
			if rec == nil {
				return fmt.Errorf("pitr apply insert missing record and data on table %s (pk=%s)", op.Table, op.Key)
			}
			if err := tx.Insert(op.Table, rec); err != nil {
				return err
			}
		case memengine.OpUpdate:
			if err := ensureTx(); err != nil {
				return err
			}
			tblSchema, err := eng.GetTable(op.Table)
			if err != nil {
				return err
			}
			rec := op.Record
			if rec == nil && len(op.Data) > 0 {
				var err error
				rec, err = storage.DecodeTuple(tblSchema, op.Data)
				if err != nil {
					return fmt.Errorf("pitr apply update decode tuple failed on table %s (pk=%s): %w", op.Table, op.Key, err)
				}
			}
			if rec == nil {
				return fmt.Errorf("pitr apply update missing record and data on table %s (pk=%s)", op.Table, op.Key)
			}
			var pkVal storage.Value
			if tblSchema.IsCompositePK() {
				if resolved, ok := tblSchema.ResolveRecordPK(rec); ok && !resolved.IsNull {
					pkVal = resolved
				} else {
					pkVal = storage.StringValue(op.Key)
				}
			} else {
				pkCol, err := tblSchema.PrimaryKeyColumn()
				if err != nil {
					return err
				}
				if rec != nil {
					pkVal = rec[pkCol.Name]
				}
				if pkVal.IsNull || pkVal.Type == 0 {
					pkVal = memengine.ParsePKValue(op.Key, pkCol.Type)
				}
			}
			if err := tx.Update(op.Table, pkVal, rec); err != nil {
				return err
			}
		case memengine.OpDelete:
			if err := ensureTx(); err != nil {
				return err
			}
			tblSchema, err := eng.GetTable(op.Table)
			if err != nil {
				return err
			}
			var pkVal storage.Value
			if tblSchema.IsCompositePK() {
				pkVal = storage.StringValue(op.Key)
			} else {
				pkCol, err := tblSchema.PrimaryKeyColumn()
				if err != nil {
					return err
				}
				pkVal = memengine.ParsePKValue(op.Key, pkCol.Type)
			}
			if err := tx.Delete(op.Table, pkVal); err != nil {
				return err
			}
		}
	}

	return commitTx()
}

// RestorePITR restores the base snapshot (if supplied) and continuously applies archived WAL segments
// up to the specified target point-in-time or LSN.
func RestorePITR(baseBackupPath string, archiveDir string, eng storage.Engine, cat *catalog.Catalog, opts PITROptions) (*PITRResult, error) {
	startTime := time.Now()
	res := &PITRResult{}

	var baseManifest *Manifest
	// 1. Inspect and restore baseline snapshot if provided
	if baseBackupPath != "" {
		var err error
		baseManifest, err = ReadBackupManifestFile(baseBackupPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read baseline manifest from %s: %w", baseBackupPath, err)
		}

		// [RULE:ExplicitError] Fail fast if recovery targets are strictly before base backup creation
		if !opts.TargetTime.IsZero() && opts.TargetTime.Before(baseManifest.CreatedAt) {
			return nil, fmt.Errorf("%w: target time %s is before base backup creation time %s",
				ErrPITRTargetBeforeBase, opts.TargetTime.Format(time.RFC3339Nano), baseManifest.CreatedAt.Format(time.RFC3339Nano))
		}
		if opts.TargetLSN > 0 && baseManifest.SnapshotLSN > 0 && opts.TargetLSN < baseManifest.SnapshotLSN {
			return nil, fmt.Errorf("%w: target LSN %d is before base backup snapshot LSN %d",
				ErrPITRTargetBeforeBase, opts.TargetLSN, baseManifest.SnapshotLSN)
		}

		baseRes, err := RestoreBackupFile(baseBackupPath, eng, cat, opts.RestoreOptions)
		if err != nil {
			return nil, fmt.Errorf("failed to restore baseline snapshot %s: %w", baseBackupPath, err)
		}
		res.BaseResult = baseRes
	}

	// 2. Discover archived WAL segments
	if archiveDir == "" {
		res.Duration = time.Since(startTime)
		return res, nil
	}

	archiver, err := NewWALArchiver(archiveDir)
	if err != nil {
		return nil, fmt.Errorf("failed to access WAL archive: %w", err)
	}

	segments, err := archiver.ListSegments()
	if err != nil {
		return nil, fmt.Errorf("failed to list WAL archive segments: %w", err)
	}

	// 3. Incrementally replay WAL records in strict LSN order
	var baseLSN int64
	var lastAppliedLSN int64
	if baseManifest != nil && baseManifest.SnapshotLSN > 0 {
		baseLSN = baseManifest.SnapshotLSN
		lastAppliedLSN = baseLSN
	}

	stopped := false
	for _, seg := range segments {
		if stopped {
			break
		}

		res.ArchivedSegmentsParsed++
		_, rErr := memengine.ReplayWAL(seg.Path, func(rec memengine.WALRecord) error {
			// Skip records already committed in the base backup snapshot
			if baseLSN > 0 && rec.LSN <= baseLSN {
				return nil
			}

			recTime := time.Unix(0, rec.Timestamp).UTC()

			// Check Target Time condition
			if !opts.TargetTime.IsZero() && recTime.After(opts.TargetTime) {
				res.TargetReached = true
				stopped = true
				return ErrPITRTargetReached
			}

			// Check Target LSN condition
			if opts.TargetLSN > 0 && rec.LSN > opts.TargetLSN {
				res.TargetReached = true
				stopped = true
				return ErrPITRTargetReached
			}

			// Strict LSN Continuity / Gap Detection
			if lastAppliedLSN > 0 && rec.LSN != lastAppliedLSN+1 {
				stopped = true
				return fmt.Errorf("%w: expected contiguous LSN %d, got %d (missing LSNs %d..%d)",
					ErrPITRLSNGapDetected, lastAppliedLSN+1, rec.LSN, lastAppliedLSN+1, rec.LSN-1)
			}

			// Synchronize catalog stats on DDL mutations ([발굴 결함 9-5])
			var ddlHook func(op memengine.WALOp)
			if cat != nil {
				ddlHook = func(op memengine.WALOp) {
					switch op.Type {
					case memengine.OpCreateTable, memengine.OpDropTable, memengine.OpTruncateTable,
						memengine.OpCreateIndex, memengine.OpDropIndex, memengine.OpDropPartition,
						memengine.OpRenameTable, memengine.OpAlterTable:
						cat.InvalidateTableStats(op.Table)
						if op.Key != "" {
							cat.InvalidateTableStats(op.Key)
						}
					}
				}
			}

			// Apply WAL operations
			if err := ApplyWALRecordWithOptions(eng, rec, ddlHook); err != nil {
				return err
			}

			lastAppliedLSN = rec.LSN
			res.WALRecordsReplayed++
			res.FinalLSN = rec.LSN
			res.FinalTimestamp = recTime
			return nil
		})

		if rErr != nil && !errors.Is(rErr, ErrPITRTargetReached) {
			return nil, fmt.Errorf("error replaying WAL segment %s: %w", seg.Filename, rErr)
		}
	}

	res.Duration = time.Since(startTime)
	return res, nil
}

// RestorePITRFile restores from baseline backup file and archived WAL directory up to target timestamp or LSN.
func RestorePITRFile(baseBackupPath string, archiveDir string, targetTime time.Time, targetLSN int64, eng storage.Engine, cat *catalog.Catalog) (*PITRResult, error) {
	opts := PITROptions{
		RestoreOptions: DefaultRestoreOptions(),
		TargetTime:     targetTime,
		TargetLSN:      targetLSN,
	}
	return RestorePITR(baseBackupPath, archiveDir, eng, cat, opts)
}
