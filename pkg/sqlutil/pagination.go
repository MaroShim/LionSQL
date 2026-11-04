package sqlutil

import (
	"strconv"
	"strings"
)

// NormalizeMSSQLPagination rewrites MSSQL pagination constructs:
// - SELECT TOP <N> / SELECT TOP (<N>) -> canonical LIMIT <N>
// - SELECT DISTINCT TOP <N> -> canonical SELECT DISTINCT ... LIMIT <N>
// - OFFSET <O> ROWS FETCH NEXT/FIRST <N> ROWS ONLY -> canonical LIMIT <N> OFFSET <O>
// - OFFSET <O> ROWS -> canonical OFFSET <O>
// - FETCH NEXT/FIRST <N> ROWS ONLY -> canonical LIMIT <N>
// All operations are Dyck-language balanced and string-literal safe.
func NormalizeMSSQLPagination(sql string) string {
	sql = StripMSSQLNolock(sql)
	sql = StripMSSQLOption(sql)

	// 1. Process OFFSET ... FETCH clauses at depth 0
	sql = normalizeOffsetFetch(sql)

	// 2. Process TOP <N> at top-level SELECT
	sql = normalizeTop(sql)

	return sql
}

func StripMSSQLOption(sql string) string {
	trimmed := strings.TrimRight(sql, "; \t\r\n")
	optIdx := FindTopLevelKeyword(trimmed, "OPTION")
	if optIdx != -1 {
		rest := strings.TrimSpace(trimmed[optIdx+6:])
		if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
			return trimmed[:optIdx]
		}
	}
	return sql
}

func StripMSSQLNolock(sql string) string {
	for {
		upper := strings.ToUpper(sql)
		idx := strings.Index(upper, "WITH")
		if idx == -1 {
			break
		}
		if idx > 0 && !IsWordBoundary(upper[idx-1]) {
			break
		}
		rest := strings.TrimLeft(sql[idx+4:], " \t\r\n")
		if strings.HasPrefix(rest, "(") {
			closeP := FindMatchingParen(rest, 0)
			if closeP != -1 {
				inside := strings.ToUpper(strings.TrimSpace(rest[1:closeP]))
				if inside == "NOLOCK" || inside == "READUNCOMMITTED" {
					sql = sql[:idx] + rest[closeP+1:]
					continue
				}
			}
		}
		break
	}
	return sql
}

// normalizeOffsetFetch handles OFFSET <O> ROWS FETCH NEXT/FIRST <N> ROWS ONLY
func normalizeOffsetFetch(sql string) string {
	trimmed := strings.TrimRight(sql, "; \t\r\n")

	// Check if top-level OFFSET exists
	offsetIdx := FindTopLevelKeyword(trimmed, "OFFSET")
	if offsetIdx != -1 {
		rest := trimmed[offsetIdx+6:]
		tokens := SplitTopLevelTokens(rest)
		// Expected: <num> ROWS [FETCH NEXT/FIRST <num> ROWS ONLY]
		if len(tokens) >= 2 {
			offsetVal := tokens[0]
			if _, err := strconv.ParseInt(offsetVal, 10, 64); err == nil {
				second := strings.ToUpper(tokens[1])
				if second == "ROW" || second == "ROWS" {
					if len(tokens) >= 6 && strings.ToUpper(tokens[2]) == "FETCH" {
						dir := strings.ToUpper(tokens[3])
						if dir == "NEXT" || dir == "FIRST" {
							limitVal := tokens[4]
							if _, err := strconv.ParseInt(limitVal, 10, 64); err == nil {
								unit := strings.ToUpper(tokens[5])
								if unit == "ROW" || unit == "ROWS" {
									// Optional ONLY
									return strings.TrimSpace(trimmed[:offsetIdx]) + " LIMIT " + limitVal + " OFFSET " + offsetVal
								}
							}
						}
					}
					// Just OFFSET <O> ROWS
					return strings.TrimSpace(trimmed[:offsetIdx]) + " OFFSET " + offsetVal
				}
			}
		}
	}

	// Check standalone FETCH NEXT/FIRST <N> ROWS ONLY without OFFSET
	fetchIdx := FindTopLevelKeyword(trimmed, "FETCH")
	if fetchIdx != -1 {
		rest := trimmed[fetchIdx+5:]
		tokens := SplitTopLevelTokens(rest)
		if len(tokens) >= 4 {
			dir := strings.ToUpper(tokens[0])
			if dir == "NEXT" || dir == "FIRST" {
				limitVal := tokens[1]
				if _, err := strconv.ParseInt(limitVal, 10, 64); err == nil {
					unit := strings.ToUpper(tokens[2])
					if unit == "ROW" || unit == "ROWS" {
						return strings.TrimSpace(trimmed[:fetchIdx]) + " LIMIT " + limitVal
					}
				}
			}
		}
	}

	return sql
}

// normalizeTop handles SELECT TOP <N> or SELECT DISTINCT TOP <N>
func normalizeTop(sql string) string {
	if !ContainsKeyword(sql, "TOP", false) {
		return sql
	}
	if ContainsKeyword(sql, "LIMIT", false) {
		return sql
	}

	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") {
		return sql
	}

	// Tokenize top-level tokens from start of query up to FROM
	fromIdx := FindTopLevelKeyword(trimmed, "FROM")
	var head string
	var tail string
	if fromIdx != -1 {
		head = trimmed[:fromIdx]
		tail = trimmed[fromIdx:]
	} else {
		head = trimmed
		tail = ""
	}

	// Analyze head: e.g. "SELECT TOP 10 col1, col2" or "SELECT DISTINCT TOP (5) col1"
	tokens := SplitTopLevelTokens(head)
	if len(tokens) < 3 {
		return sql
	}

	tok0 := strings.ToUpper(tokens[0])
	if tok0 != "SELECT" {
		return sql
	}

	distinct := false
	topTokenIdx := 1
	if strings.ToUpper(tokens[1]) == "DISTINCT" {
		distinct = true
		topTokenIdx = 2
	}

	if len(tokens) <= topTokenIdx || strings.ToUpper(tokens[topTokenIdx]) != "TOP" {
		return sql
	}

	// The token after TOP is either <num> or (<num>)
	if len(tokens) <= topTokenIdx+1 {
		return sql
	}

	limitToken := tokens[topTokenIdx+1]
	limitVal := strings.Trim(limitToken, "() \t\r\n")
	if _, err := strconv.ParseInt(limitVal, 10, 64); err != nil {
		return sql // Not a static number
	}

	// Reconstruct projection without TOP <num>
	// Find where TOP <num> was in head
	topPos := FindTopLevelKeyword(head, "TOP")
	if topPos == -1 {
		return sql
	}

	// Find the end of the limit token in head after topPos
	afterTop := head[topPos+3:]
	idxLimit := strings.Index(afterTop, limitToken)
	if idxLimit == -1 {
		return sql
	}
	projStart := topPos + 3 + idxLimit + len(limitToken)
	proj := head[projStart:]

	var newHead string
	if distinct {
		newHead = "SELECT DISTINCT" + proj
	} else {
		newHead = "SELECT" + proj
	}

	if tail != "" {
		return strings.TrimSpace(newHead) + " " + strings.TrimSpace(tail) + " LIMIT " + limitVal
	}
	return strings.TrimSpace(newHead) + " LIMIT " + limitVal
}
