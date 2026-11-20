package cdc

import (
	"context"
	"fmt"
	"time"

	"lionsql/pkg/storage"
)

// SnapshotGenerator extracts consistent point-in-time table rows for initial CDC bootstrapping.
type SnapshotGenerator struct {
	engine storage.Engine
}

// NewSnapshotGenerator creates a new snapshot generator over a storage engine.
func NewSnapshotGenerator(eng storage.Engine) *SnapshotGenerator {
	return &SnapshotGenerator{
		engine: eng,
	}
}

// GenerateTableSnapshot scans the given table within a ReadOnly snapshot transaction,
// producing OpSnapshot ChangeEvents through the provided emitter channel or callback.
// It returns the snapshot boundary LSN and count of exported rows.
func (g *SnapshotGenerator) GenerateTableSnapshot(
	ctx context.Context,
	tableName string,
	emit func(ev ChangeEvent) error,
) (snapshotLSN int64, rowCount int, err error) {
	if g.engine == nil {
		return 0, 0, fmt.Errorf("storage engine not configured")
	}

	sess, err := g.engine.NewSession()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create storage session: %w", err)
	}
	defer sess.Close()

	// Begin ReadOnly transaction to capture point-in-time snapshot
	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin snapshot transaction: %w", err)
	}
	defer tx.Rollback()

	snapshotLSN = tx.SnapshotLSN()
	if snapshotLSN <= 0 {
		snapshotLSN = g.engine.CurrentLSN()
	}
	snapTS := tx.SnapshotTimestamp()
	if snapTS <= 0 {
		snapTS = time.Now().UnixNano()
	}

	tblSchema, err := g.engine.GetTable(tableName)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get table %q schema: %w", tableName, err)
	}

	cursor, err := tx.Scan(tableName)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to scan table %q: %w", tableName, err)
	}
	defer cursor.Close()

	count := 0
	for cursor.Next() {
		select {
		case <-ctx.Done():
			return snapshotLSN, count, ctx.Err()
		default:
		}

		rec, err := cursor.Record()
		if err != nil {
			return snapshotLSN, count, fmt.Errorf("cursor record error on %q: %w", tableName, err)
		}

		// Extract primary key
		var pkStr string
		if pkCol, err := tblSchema.PrimaryKeyColumn(); err == nil && pkCol != nil {
			if v, ok := rec[pkCol.Name]; ok {
				pkStr = v.String()
			}
		}

		ev := ChangeEvent{
			LSN:       snapshotLSN,
			Timestamp: snapTS,
			Table:     tableName,
			Op:        OpSnapshot,
			Key:       pkStr,
			Data:      RecordToMap(rec),
		}

		if err := emit(ev); err != nil {
			return snapshotLSN, count, fmt.Errorf("snapshot emit error: %w", err)
		}
		count++
	}

	if err := cursor.Err(); err != nil {
		return snapshotLSN, count, fmt.Errorf("cursor scan terminated with error: %w", err)
	}

	return snapshotLSN, count, nil
}
