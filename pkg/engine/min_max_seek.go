package engine

import (
	"strings"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
)

// tryMinMaxIndexSeek executes global MIN/MAX aggregate queries in O(1) time
// by performing single-leaf seeks on B-Tree index cursors.
func (s *Coordinator) tryMinMaxIndexSeek(qInfo *planner.QueryInfo, tblSch *storage.TableSchema, start time.Time, cs *ClientSession) (*QueryResult, error, bool) {
	if qInfo == nil || tblSch == nil {
		return nil, nil, false
	}
	if tblSch.IsPartitioned() || tblSch.IsTemporary() {
		return nil, nil, false
	}
	if len(qInfo.Joins) > 0 || qInfo.Join != nil {
		return nil, nil, false
	}
	if len(qInfo.GroupByCols) > 0 || qInfo.HavingClause != "" {
		return nil, nil, false
	}
	if len(qInfo.WindowDefs) > 0 {
		return nil, nil, false
	}
	where := strings.TrimSpace(qInfo.WhereClause)
	if where != "" && where != "1=1" && where != "1 = 1" && !strings.EqualFold(where, "true") {
		return nil, nil, false
	}
	if len(qInfo.Projections) == 0 {
		return nil, nil, false
	}

	// Verify that every projection is MIN(col) or MAX(col) with an available index seek path
	for _, proj := range qInfo.Projections {
		item := parseAggItem(proj)
		if item.FuncType != "MIN" && item.FuncType != "MAX" {
			return nil, nil, false
		}
		if item.ArgCol == "" || item.ArgCol == "*" {
			return nil, nil, false
		}
		col, _, ok := tblSch.FindColumn(item.ArgCol)
		if !ok || col == nil {
			return nil, nil, false
		}
		_, hasIndex := tblSch.FindIndexByColumn(item.ArgCol)
		if !hasIndex {
			if item.FuncType == "MIN" && col.PrimaryKey && !tblSch.IsCompositePK() {
				// MIN on primary key can seek first row via tx.Scan
				continue
			}
			return nil, nil, false
		}
	}

	// RBAC Permission Check
	if err := s.checkTableAccess(cs, tblSch.Name, auth.PrivSelect); err != nil {
		return nil, err, true
	}

	// Acquire transaction handle
	var tx storage.Transaction
	if cs != nil && cs.Tx() != nil {
		tx = cs.Tx()
	} else {
		session, closeFn, err := s.acquireSessionForQuery(cs)
		if err != nil {
			return nil, err, true
		}
		defer closeFn()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err, true
		}
		tx = t
		defer tx.Rollback()
	}

	outCols := make([]string, 0, len(qInfo.Projections))
	outRow := make(map[string]any, len(qInfo.Projections)*2)
	outVals := make([]any, len(qInfo.Projections))

	for pIdx, proj := range qInfo.Projections {
		item := parseAggItem(proj)
		header := item.Alias
		if header == "" {
			header = item.RawStr
		}
		outCols = append(outCols, header)

		col, colIdx, _ := tblSch.FindColumn(item.ArgCol)
		idx, hasIndex := tblSch.FindIndexByColumn(item.ArgCol)

		var cur storage.Cursor
		var curErr error

		if hasIndex {
			if item.FuncType == "MIN" {
				cur, curErr = tx.IndexScan(tblSch.Name, idx.Name, nil, nil)
			} else {
				cur, curErr = tx.IndexScanDesc(tblSch.Name, idx.Name, nil, nil)
			}
		} else if item.FuncType == "MIN" && col.PrimaryKey && !tblSch.IsCompositePK() {
			cur, curErr = tx.Scan(tblSch.Name)
		}

		if curErr != nil || cur == nil {
			return nil, curErr, true
		}

		var val any = nil
		for cur.Next() {
			row, err := cur.Row()
			if err != nil {
				break
			}
			if colIdx < len(row.Values) {
				stVal := row.Values[colIdx]
				if !stVal.IsNull {
					val = formatStorageValue(stVal)
					break // Found first non-null entry in index order!
				}
			}
		}
		_ = cur.Close()

		outRow[header] = val
		if item.RawStr != header {
			outRow[item.RawStr] = val
		}
		outVals[pIdx] = val
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      outCols,
		Rows:         []map[string]any{outRow},
		Values:       [][]any{outVals},
		RowsAffected: 1,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil, true
}
