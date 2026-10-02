package backup

import (
	"errors"
	"fmt"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

// ErrPITRTargetReached is a sentinel error signaling that the target recovery point was reached.
var ErrPITRTargetReached = errors.New("PITR target reached")

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
					memengine.OpCreateIndex, memengine.OpDropIndex, memengine.OpDropPartition:
					ddlHook(op)
				}
			}
		}
		return err
	}

	for _, op := range rec.Ops {
		switch op.Type {
		case memengine.OpCreateTable:
			if op.Schema != nil {
				_ = eng.CreateTable(op.Schema)
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpDropTable:
			_ = eng.DropTable(op.Table)
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpTruncateTable:
			_ = eng.TruncateTable(op.Table)
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpDropPartition:
			_ = eng.DropPartition(op.Table, op.Key)
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpCreateIndex:
			if op.IndexSchema != nil {
				_ = eng.CreateIndex(op.IndexSchema)
			}
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpDropIndex:
			_ = eng.DropIndex(op.Table, op.IndexName)
			if ddlHook != nil {
				ddlHook(op)
			}
		case memengine.OpInsert:
			sess, err := eng.NewSession()
			if err != nil {
				return err
			}
			tx, err := sess.BeginTx(storage.TxOptions{})
			if err != nil {
				_ = sess.Close()
				return err
			}
			rec := op.Record
			if rec == nil && len(op.Data) > 0 {
				tblSchema, err := eng.GetTable(op.Table)
				if err == nil {
					rec, _ = storage.DecodeTuple(tblSchema, op.Data)
				}
			}
			if err := tx.Insert(op.Table, rec); err != nil {
				_ = tx.Rollback()
				_ = sess.Close()
				return err
			}
			if err := tx.Commit(); err != nil {
				_ = sess.Close()
				return err
			}
			_ = sess.Close()
		case memengine.OpUpdate:
			sess, err := eng.NewSession()
			if err != nil {
				return err
			}
			tx, err := sess.BeginTx(storage.TxOptions{})
			if err != nil {
				_ = sess.Close()
				return err
			}
			tblSchema, err := eng.GetTable(op.Table)
			if err != nil {
				_ = tx.Rollback()
				_ = sess.Close()
				return err
			}
			rec := op.Record
			if rec == nil && len(op.Data) > 0 {
				rec, _ = storage.DecodeTuple(tblSchema, op.Data)
			}
			pkCol, err := tblSchema.PrimaryKeyColumn()
			if err != nil {
				_ = tx.Rollback()
				_ = sess.Close()
				return err
			}
			var pkVal storage.Value
			if rec != nil {
				pkVal = rec[pkCol.Name]
			}
			if pkVal.IsNull || pkVal.Type == 0 {
				pkVal = memengine.ParsePKValue(op.Key, pkCol.Type)
			}
			if err := tx.Update(op.Table, pkVal, rec); err != nil {
				_ = tx.Rollback()
				_ = sess.Close()
				return err
			}
			if err := tx.Commit(); err != nil {
				_ = sess.Close()
				return err
			}
			_ = sess.Close()
		case memengine.OpDelete:
			sess, err := eng.NewSession()
			if err != nil {
				return err
			}
			tx, err := sess.BeginTx(storage.TxOptions{})
			if err != nil {
				_ = sess.Close()
				return err
			}
			tblSchema, err := eng.GetTable(op.Table)
			if err != nil {
				_ = tx.Rollback()
				_ = sess.Close()
				return err
			}
			pkCol, err := tblSchema.PrimaryKeyColumn()
			if err != nil {
				_ = tx.Rollback()
				_ = sess.Close()
				return err
			}
			pkVal := memengine.ParsePKValue(op.Key, pkCol.Type)
			if err := tx.Delete(op.Table, pkVal); err != nil {
				_ = tx.Rollback()
				_ = sess.Close()
				return err
			}
			if err := tx.Commit(); err != nil {
				_ = sess.Close()
				return err
			}
			_ = sess.Close()
		}
	}
	return nil
}

// RestorePITR restores the base snapshot (if supplied) and continuously applies archived WAL segments
// up to the specified target point-in-time or LSN.
func RestorePITR(baseBackupPath string, archiveDir string, eng storage.Engine, cat *catalog.Catalog, opts PITROptions) (*PITRResult, error) {
	startTime := time.Now()
	res := &PITRResult{}

	// 1. Restore baseline snapshot if provided
	if baseBackupPath != "" {
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
	stopped := false
	for _, seg := range segments {
		if stopped {
			break
		}

		res.ArchivedSegmentsParsed++
		_, rErr := memengine.ReplayWAL(seg.Path, func(rec memengine.WALRecord) error {
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

			// Apply WAL operations
			if err := ApplyWALRecordToEngine(eng, rec); err != nil {
				return err
			}

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
