package engine

import (
	"fmt"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
)

// isDeclareCursorQuery reports whether query is a SQL server-side cursor declaration.
func isDeclareCursorQuery(query string) bool {
	trimmed := strings.TrimSpace(query)
	if !sqlutil.HasPrefixKeyword(trimmed, "DECLARE") {
		return false
	}
	return sqlutil.ContainsKeyword(trimmed, "CURSOR", false)
}

// execDeclareCursor handles DECLARE <name> [BINARY] [INSENSITIVE] [NO SCROLL|SCROLL] CURSOR [WITH|WITHOUT HOLD] FOR <query>.
func (s *Coordinator) execDeclareCursor(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if cs == nil {
		return nil, fmt.Errorf("active session required for cursor declaration")
	}

	stmt, err := proc.ParseServerCursorDeclare(query)
	if err != nil {
		return nil, err
	}

	cursorName := stmt.Name
	lookupKey := strings.ToLower(cursorName)

	cs.Mu.Lock()
	if cs.Cursors == nil {
		cs.Cursors = make(map[string]*SessionCursor)
	}
	if _, exists := cs.Cursors[lookupKey]; exists {
		cs.Mu.Unlock()
		return nil, fmt.Errorf("cursor %q already exists", cursorName)
	}
	cs.Mu.Unlock()

	// Execute underlying query to materialize cursor snapshot
	res, err := s.execSelect(stmt.Query, start, cs)
	if err != nil {
		return nil, fmt.Errorf("cursor query execution failed: %w", err)
	}

	cs.Mu.Lock()
	cs.Cursors[lookupKey] = &SessionCursor{
		Name:       cursorName,
		Query:      stmt.Query,
		Columns:    res.Columns,
		Rows:       res.Rows,
		Pos:        0,
		IsOpen:     false, // T-SQL lifecycle: cursor is declared closed until explicit OPEN
		EverOpened: false,
		IsClosed:   false,
		WithHold:   stmt.WithHold,
		Scroll:     stmt.Scroll,
		Binary:     stmt.Binary,
		DeclaredAt: time.Now(),
	}
	cs.Mu.Unlock()

	return &QueryResult{
		Success:    true,
		Command:    "DECLARE CURSOR",
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

// advanceCursor evaluates direction and count on cursor rows and returns the slice of fetched rows.
func advanceCursor(cur *SessionCursor, dir string, count int, isAll bool) []map[string]any {
	total := len(cur.Rows)
	if total == 0 {
		return []map[string]any{}
	}

	var fetched []map[string]any

	switch dir {
	case "NEXT", "FORWARD":
		if isAll {
			count = max(total-cur.Pos, 0)
		}
		if count <= 0 || cur.Pos >= total {
			return []map[string]any{}
		}
		end := min(cur.Pos+count, total)
		fetched = cur.Rows[cur.Pos:end]
		cur.Pos = end

	case "PRIOR":
		if cur.Pos <= 0 {
			return []map[string]any{}
		}
		target := cur.Pos - 1
		if target >= 0 && target < total {
			fetched = []map[string]any{cur.Rows[target]}
			cur.Pos = target
		}

	case "FIRST":
		if total > 0 {
			fetched = []map[string]any{cur.Rows[0]}
			cur.Pos = 1
		}

	case "LAST":
		if total > 0 {
			fetched = []map[string]any{cur.Rows[total-1]}
			cur.Pos = total
		}

	case "BACKWARD":
		if isAll {
			count = cur.Pos
		}
		if count <= 0 || cur.Pos <= 0 {
			return []map[string]any{}
		}
		start := max(cur.Pos-count, 0)
		slice := cur.Rows[start:cur.Pos]
		fetched = make([]map[string]any, len(slice))
		for i, r := range slice {
			fetched[len(slice)-1-i] = r
		}
		cur.Pos = start

	case "ABSOLUTE":
		targetIdx := 0
		if count > 0 {
			targetIdx = count - 1
		} else if count < 0 {
			targetIdx = total + count
		} else {
			cur.Pos = 0
			return []map[string]any{}
		}

		if targetIdx >= 0 && targetIdx < total {
			fetched = []map[string]any{cur.Rows[targetIdx]}
			cur.Pos = targetIdx + 1
		} else if targetIdx >= total {
			cur.Pos = total + 1
		} else {
			cur.Pos = 0
		}

	case "RELATIVE":
		if count == 0 {
			if cur.Pos > 0 && cur.Pos <= total {
				fetched = []map[string]any{cur.Rows[cur.Pos-1]}
			}
			return fetched
		}
		target := cur.Pos + count
		if target >= 1 && target <= total {
			fetched = []map[string]any{cur.Rows[target-1]}
			cur.Pos = target
		} else if target > total {
			cur.Pos = total + 1
		} else {
			cur.Pos = 0
		}
	}

	return fetched
}

// execFetchCursor handles FETCH [direction] [count] [FROM|IN] <name>.
func (s *Coordinator) execFetchCursor(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if cs == nil {
		return nil, fmt.Errorf("active session required for cursor fetch")
	}

	stmt, err := proc.ParseServerCursorFetchOrMove(query)
	if err != nil {
		return nil, err
	}

	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	cur, exists := cs.Cursors[strings.ToLower(stmt.CursorName)]
	if !exists {
		return nil, fmt.Errorf("cursor %q does not exist", stmt.CursorName)
	}
	if !cur.IsOpen {
		if !cur.EverOpened && !cur.IsClosed {
			// PostgreSQL compatibility: FETCH immediately after DECLARE auto-opens cursor
			cur.IsOpen = true
			cur.EverOpened = true
		} else {
			return nil, fmt.Errorf("cursor %q is not open", stmt.CursorName)
		}
	}

	// Validate SCROLL vs FORWARD_ONLY
	dirUpper := strings.ToUpper(stmt.Direction)
	if !cur.Scroll && (dirUpper == "PRIOR" || dirUpper == "FIRST" || dirUpper == "LAST" || dirUpper == "ABSOLUTE" || dirUpper == "BACKWARD" || (dirUpper == "RELATIVE" && stmt.Count < 0)) {
		return nil, fmt.Errorf("Msg 16911, Level 16, State 1: A fetch type '%s' cannot be used with forward only cursor '%s'.", stmt.Direction, stmt.CursorName)
	}

	fetched := advanceCursor(cur, stmt.Direction, stmt.Count, stmt.IsAll)

	if cs.Vars == nil {
		cs.Vars = make(map[string]any)
	}
	if len(fetched) > 0 {
		cs.Vars["fetch_status"] = int64(0)
	} else {
		cs.Vars["fetch_status"] = int64(-1)
	}

	return &QueryResult{
		Success:      true,
		Command:      fmt.Sprintf("FETCH %d", len(fetched)),
		Columns:      cur.Columns,
		Rows:         fetched,
		RowsAffected: int64(len(fetched)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execMoveCursor handles MOVE [direction] [count] [FROM|IN] <name>.
func (s *Coordinator) execMoveCursor(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if cs == nil {
		return nil, fmt.Errorf("active session required for cursor move")
	}

	stmt, err := proc.ParseServerCursorFetchOrMove(query)
	if err != nil {
		return nil, err
	}

	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	cur, exists := cs.Cursors[strings.ToLower(stmt.CursorName)]
	if !exists {
		return nil, fmt.Errorf("cursor %q does not exist", stmt.CursorName)
	}
	if !cur.IsOpen {
		return nil, fmt.Errorf("cursor %q is not open", stmt.CursorName)
	}

	movedRows := advanceCursor(cur, stmt.Direction, stmt.Count, stmt.IsAll)
	countMoved := len(movedRows)

	return &QueryResult{
		Success:      true,
		Command:      fmt.Sprintf("MOVE %d", countMoved),
		RowsAffected: int64(countMoved),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execOpenCursor handles OPEN <name> to re-open a previously closed server-side cursor.
func (s *Coordinator) execOpenCursor(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if cs == nil {
		return nil, fmt.Errorf("active session required for cursor open")
	}

	target, err := proc.ParseServerCursorOpenOrClose(query, "OPEN")
	if err != nil {
		return nil, err
	}
	lookupKey := strings.ToLower(target)

	cs.Mu.Lock()
	cur, exists := cs.Cursors[lookupKey]
	if !exists {
		cs.Mu.Unlock()
		return nil, fmt.Errorf("cursor %q does not exist", target)
	}
	if cur.IsOpen {
		cs.Mu.Unlock()
		return nil, fmt.Errorf("cursor %q is already open", target)
	}
	cursorQuery := cur.Query
	cs.Mu.Unlock()

	res, err := s.execSelect(cursorQuery, start, cs)
	if err != nil {
		return nil, fmt.Errorf("cursor query execution failed: %w", err)
	}

	cs.Mu.Lock()
	if cur, exists := cs.Cursors[lookupKey]; exists {
		cur.Columns = res.Columns
		cur.Rows = res.Rows
		cur.Pos = 0
		cur.IsOpen = true
		cur.EverOpened = true
		cur.IsClosed = false
	}
	cs.Mu.Unlock()

	return &QueryResult{
		Success:    true,
		Command:    "OPEN CURSOR",
		Messages:   []string{fmt.Sprintf("Cursor %q opened.", target)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

// execCloseCursor handles CLOSE <name> or CLOSE ALL.
func (s *Coordinator) execCloseCursor(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if cs == nil {
		return nil, fmt.Errorf("active session required for cursor close")
	}

	target, err := proc.ParseServerCursorOpenOrClose(query, "CLOSE")
	if err != nil {
		return nil, err
	}
	upperTarget := strings.ToUpper(target)

	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	if upperTarget == "ALL" {
		for _, cur := range cs.Cursors {
			cur.IsOpen = false
			cur.IsClosed = true
			cur.Rows = nil
			cur.Pos = 0
		}
		return &QueryResult{
			Success:    true,
			Command:    "CLOSE CURSOR",
			Messages:   []string{"CLOSE ALL"},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	cur, exists := cs.Cursors[strings.ToLower(target)]
	if !exists {
		return nil, fmt.Errorf("cursor %q does not exist", target)
	}

	cur.IsOpen = false
	cur.IsClosed = true
	cur.Rows = nil
	cur.Pos = 0

	return &QueryResult{
		Success:    true,
		Command:    "CLOSE CURSOR",
		Messages:   []string{fmt.Sprintf("Cursor %q closed.", target)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}
