package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// isDeclareCursorQuery reports whether query is a SQL server-side cursor declaration.
func isDeclareCursorQuery(query string) bool {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if !strings.HasPrefix(upper, "DECLARE ") {
		return false
	}
	return strings.Contains(upper, " CURSOR ") || strings.HasSuffix(upper, " CURSOR")
}

// execDeclareCursor handles DECLARE <name> [BINARY] [INSENSITIVE] [NO SCROLL|SCROLL] CURSOR [WITH|WITHOUT HOLD] FOR <query>.
func (s *Coordinator) execDeclareCursor(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if cs == nil {
		return nil, fmt.Errorf("active session required for cursor declaration")
	}

	trimmed := strings.TrimSpace(query)
	trimmed = strings.TrimRight(trimmed, ";")

	// Squeeze "DECLARE "
	rest := strings.TrimSpace(trimmed[len("DECLARE "):])
	upperRest := strings.ToUpper(rest)

	// Split by " FOR "
	forIdx := strings.Index(upperRest, " FOR ")
	if forIdx == -1 {
		return nil, fmt.Errorf("syntax error: DECLARE CURSOR requires 'FOR <query>'")
	}

	header := strings.TrimSpace(rest[:forIdx])
	cursorQuery := strings.TrimSpace(rest[forIdx+len(" FOR "):])

	if cursorQuery == "" {
		return nil, fmt.Errorf("syntax error: empty query in DECLARE CURSOR")
	}

	headerTokens := strings.Fields(header)
	if len(headerTokens) == 0 {
		return nil, fmt.Errorf("syntax error: cursor name required")
	}

	cursorName := cleanIdentifier(headerTokens[0])
	if cursorName == "" {
		return nil, fmt.Errorf("syntax error: invalid cursor name")
	}

	var withHold bool
	var scroll bool
	var binary bool

	upperHeader := strings.ToUpper(header)
	if strings.Contains(upperHeader, "WITH HOLD") {
		withHold = true
	}
	if strings.Contains(upperHeader, "SCROLL") && !strings.Contains(upperHeader, "NO SCROLL") {
		scroll = true
	}
	if strings.Contains(upperHeader, "BINARY") {
		binary = true
	}

	cs.Mu.Lock()
	if cs.Cursors == nil {
		cs.Cursors = make(map[string]*SessionCursor)
	}
	if _, exists := cs.Cursors[cursorName]; exists {
		cs.Mu.Unlock()
		return nil, fmt.Errorf("cursor %q already exists", cursorName)
	}
	cs.Mu.Unlock()

	// Execute underlying query to materialize cursor snapshot
	res, err := s.execSelect(cursorQuery, start, cs)
	if err != nil {
		return nil, fmt.Errorf("cursor query execution failed: %w", err)
	}

	cs.Mu.Lock()
	cs.Cursors[cursorName] = &SessionCursor{
		Name:       cursorName,
		Query:      cursorQuery,
		Columns:    res.Columns,
		Rows:       res.Rows,
		Pos:        0,
		WithHold:   withHold,
		Scroll:     scroll,
		Binary:     binary,
		DeclaredAt: time.Now(),
	}
	cs.Mu.Unlock()

	return &QueryResult{
		Success:    true,
		Command:    "DECLARE CURSOR",
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}

// parseCursorParams extracts direction, count, and target cursor name from FETCH or MOVE arguments.
func parseCursorParams(raw string) (dir string, count int, isAll bool, cursorName string, err error) {
	raw = strings.TrimSpace(strings.TrimRight(raw, ";"))
	tokens := strings.Fields(raw)
	if len(tokens) == 0 {
		return "", 0, false, "", fmt.Errorf("syntax error: cursor name required")
	}

	// Last token is cursor name
	cursorName = cleanIdentifier(tokens[len(tokens)-1])
	tokens = tokens[:len(tokens)-1]

	// Strip optional FROM or IN
	if len(tokens) > 0 {
		lastTok := strings.ToUpper(tokens[len(tokens)-1])
		if lastTok == "FROM" || lastTok == "IN" {
			tokens = tokens[:len(tokens)-1]
		}
	}

	dir = "FORWARD"
	count = 1

	if len(tokens) == 0 {
		return dir, count, false, cursorName, nil
	}

	upper0 := strings.ToUpper(tokens[0])

	switch len(tokens) {
	case 1:
		switch upper0 {
		case "NEXT":
			return "NEXT", 1, false, cursorName, nil
		case "PRIOR":
			return "PRIOR", 1, false, cursorName, nil
		case "FIRST":
			return "FIRST", 1, false, cursorName, nil
		case "LAST":
			return "LAST", 1, false, cursorName, nil
		case "ALL":
			return "FORWARD", 0, true, cursorName, nil
		case "FORWARD":
			return "FORWARD", 1, false, cursorName, nil
		case "BACKWARD":
			return "BACKWARD", 1, false, cursorName, nil
		default:
			// Could be numeric count e.g. "10" or "-5"
			n, parseErr := strconv.Atoi(tokens[0])
			if parseErr != nil {
				return "", 0, false, "", fmt.Errorf("syntax error: unexpected token %q", tokens[0])
			}
			if n >= 0 {
				return "FORWARD", n, false, cursorName, nil
			}
			return "BACKWARD", -n, false, cursorName, nil
		}

	case 2:
		upper1 := strings.ToUpper(tokens[1])
		switch upper0 {
		case "FORWARD":
			if upper1 == "ALL" {
				return "FORWARD", 0, true, cursorName, nil
			}
			n, parseErr := strconv.Atoi(tokens[1])
			if parseErr != nil {
				return "", 0, false, "", fmt.Errorf("syntax error in FORWARD count: %w", parseErr)
			}
			return "FORWARD", n, false, cursorName, nil

		case "BACKWARD":
			if upper1 == "ALL" {
				return "BACKWARD", 0, true, cursorName, nil
			}
			n, parseErr := strconv.Atoi(tokens[1])
			if parseErr != nil {
				return "", 0, false, "", fmt.Errorf("syntax error in BACKWARD count: %w", parseErr)
			}
			return "BACKWARD", n, false, cursorName, nil

		case "ABSOLUTE":
			n, parseErr := strconv.Atoi(tokens[1])
			if parseErr != nil {
				return "", 0, false, "", fmt.Errorf("syntax error in ABSOLUTE count: %w", parseErr)
			}
			return "ABSOLUTE", n, false, cursorName, nil

		case "RELATIVE":
			n, parseErr := strconv.Atoi(tokens[1])
			if parseErr != nil {
				return "", 0, false, "", fmt.Errorf("syntax error in RELATIVE count: %w", parseErr)
			}
			return "RELATIVE", n, false, cursorName, nil

		default:
			return "", 0, false, "", fmt.Errorf("syntax error: unrecognized cursor direction %q", tokens[0])
		}

	default:
		return "", 0, false, "", fmt.Errorf("syntax error: too many arguments for cursor operation")
	}
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
			count = total - cur.Pos
			if count < 0 {
				count = 0
			}
		}
		if count <= 0 || cur.Pos >= total {
			return []map[string]any{}
		}
		end := cur.Pos + count
		if end > total {
			end = total
		}
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
		start := cur.Pos - count
		if start < 0 {
			start = 0
		}
		slice := cur.Rows[start:cur.Pos]
		// Reverse to match BACKWARD order
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
			// count == 0: position before first row
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

	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)
	rest := strings.TrimSpace(trimmed[len("FETCH"):])
	if strings.HasPrefix(upper, "FETCH ") {
		rest = strings.TrimSpace(trimmed[6:])
	}

	dir, count, isAll, cursorName, err := parseCursorParams(rest)
	if err != nil {
		return nil, err
	}

	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	cur, exists := cs.Cursors[cursorName]
	if !exists {
		return nil, fmt.Errorf("cursor %q does not exist", cursorName)
	}

	fetched := advanceCursor(cur, dir, count, isAll)

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

	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)
	rest := strings.TrimSpace(trimmed[len("MOVE"):])
	if strings.HasPrefix(upper, "MOVE ") {
		rest = strings.TrimSpace(trimmed[5:])
	}

	dir, count, isAll, cursorName, err := parseCursorParams(rest)
	if err != nil {
		return nil, err
	}

	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	cur, exists := cs.Cursors[cursorName]
	if !exists {
		return nil, fmt.Errorf("cursor %q does not exist", cursorName)
	}

	movedRows := advanceCursor(cur, dir, count, isAll)
	countMoved := len(movedRows)

	return &QueryResult{
		Success:      true,
		Command:      fmt.Sprintf("MOVE %d", countMoved),
		RowsAffected: int64(countMoved),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execCloseCursor handles CLOSE <name> or CLOSE ALL.
func (s *Coordinator) execCloseCursor(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	if cs == nil {
		return nil, fmt.Errorf("active session required for cursor close")
	}

	trimmed := strings.TrimSpace(query)
	trimmed = strings.TrimRight(trimmed, ";")
	upper := strings.ToUpper(trimmed)
	rest := strings.TrimSpace(trimmed[len("CLOSE"):])
	if strings.HasPrefix(upper, "CLOSE ") {
		rest = strings.TrimSpace(trimmed[6:])
	}

	target := cleanIdentifier(rest)
	upperTarget := strings.ToUpper(target)

	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	if upperTarget == "ALL" {
		cs.Cursors = make(map[string]*SessionCursor)
		return &QueryResult{
			Success:    true,
			Command:    "CLOSE CURSOR",
			Messages:   []string{"CLOSE ALL"},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if target == "" {
		return nil, fmt.Errorf("syntax error: cursor name required for CLOSE")
	}

	if _, exists := cs.Cursors[target]; !exists {
		return nil, fmt.Errorf("cursor %q does not exist", target)
	}

	delete(cs.Cursors, target)

	return &QueryResult{
		Success:    true,
		Command:    "CLOSE CURSOR",
		Messages:   []string{fmt.Sprintf("Cursor %q closed.", target)},
		DurationUS: time.Since(start).Microseconds(),
	}, nil
}
