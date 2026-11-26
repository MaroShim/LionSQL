package sqlutil

import (
	"strings"
)

// SelectClauses holds the top-level clauses extracted from a SELECT query via a single-pass scan.
type SelectClauses struct {
	Projection string // Projections between SELECT and FROM (defaults to "*" if unspecified)
	From       string // Table/view expression between FROM and WHERE/GROUP BY/ORDER BY
	Where      string // Condition expression between WHERE and GROUP BY/ORDER BY
	OrderBy    string // Order expression after ORDER BY
}

// SplitSelectClauses scans the SQL query string in a single linear pass (O(N), One-Pass)
// to extract the top-level SELECT, FROM, WHERE, and ORDER BY clauses.
// It strictly respects parentheses depth ((...)), square brackets ([...]),
// single/double quotes ('...', "..."), and both line/block comments (-- ..., /* ... */),
// guaranteeing that subqueries and string literals do not trigger false-positive clause boundaries.
func SplitSelectClauses(sql string) SelectClauses {
	n := len(sql)
	if n == 0 {
		return SelectClauses{Projection: "*"}
	}

	var (
		depth          int
		inSingleQuote  bool
		inDoubleQuote  bool
		inBracket      bool
		inLineComment  bool
		inBlockComment bool

		selectStart = -1
		selectEnd   = -1
		fromStart   = -1
		fromEnd     = -1
		whereStart  = -1
		whereEnd    = -1
		orderStart  = -1
	)

	// Helper to check word boundaries for keywords
	isBoundary := func(pos int) bool {
		if pos < 0 || pos >= n {
			return true
		}
		ch := sql[pos]
		return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' ||
			ch == ',' || ch == ';' || ch == '(' || ch == ')' || ch == '[' || ch == ']'
	}

	for i := 0; i < n; i++ {
		ch := sql[i]

		// 1. Comments
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < n && sql[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if !inSingleQuote && !inDoubleQuote && !inBracket {
			if ch == '-' && i+1 < n && sql[i+1] == '-' {
				inLineComment = true
				i++
				continue
			}
			if ch == '/' && i+1 < n && sql[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		}

		// 2. Quotes
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && sql[i+1] == '\'' {
					i++ // Skip escaped ''
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < n && sql[i+1] == '"' {
					i++ // Skip escaped ""
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracket {
			if ch == ']' {
				if i+1 < n && sql[i+1] == ']' {
					i++
					continue
				}
				inBracket = false
			}
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			continue
		}
		if ch == '[' {
			inBracket = true
			continue
		}

		// 3. Parentheses depth (nested subqueries or functions)
		if ch == '(' {
			depth++
			continue
		}
		if ch == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}

		// 4. Keyword detection at top level (depth == 0)
		if depth == 0 {
			prevOk := isBoundary(i - 1)

			// Match SELECT
			if selectStart == -1 && prevOk && i+6 <= n && isBoundary(i+6) && strings.EqualFold(sql[i:i+6], "SELECT") {
				selectStart = i + 6
				i += 5
				continue
			}

			// Match FROM
			if fromStart == -1 && prevOk && i+4 <= n && isBoundary(i+4) && strings.EqualFold(sql[i:i+4], "FROM") {
				if selectStart != -1 && selectEnd == -1 {
					selectEnd = i
				}
				fromStart = i + 4
				i += 3
				continue
			}

			// Match WHERE
			if fromStart != -1 && whereStart == -1 && orderStart == -1 && prevOk && i+5 <= n && isBoundary(i+5) && strings.EqualFold(sql[i:i+5], "WHERE") {
				if fromEnd == -1 {
					fromEnd = i
				}
				whereStart = i + 5
				i += 4
				continue
			}

			// Match ORDER BY
			if prevOk && i+8 <= n && isBoundary(i+8) && strings.EqualFold(sql[i:i+8], "ORDER BY") {
				if whereStart != -1 && whereEnd == -1 {
					whereEnd = i
				} else if fromStart != -1 && fromEnd == -1 {
					fromEnd = i
				}
				orderStart = i + 8
				i += 7
				continue
			}
		}
	}

	var res SelectClauses

	// Finalize projection
	if selectStart != -1 {
		if selectEnd != -1 {
			res.Projection = strings.TrimSpace(sql[selectStart:selectEnd])
		} else if fromStart == -1 {
			res.Projection = strings.TrimSpace(sql[selectStart:])
		}
	}
	if res.Projection == "" {
		res.Projection = "*"
	}

	// Finalize FROM
	if fromStart != -1 {
		if fromEnd != -1 {
			res.From = strings.TrimSpace(sql[fromStart:fromEnd])
		} else if whereStart == -1 && orderStart == -1 {
			res.From = strings.TrimSpace(sql[fromStart:])
		}
	}

	// Finalize WHERE
	if whereStart != -1 {
		if whereEnd != -1 {
			res.Where = strings.TrimSpace(sql[whereStart:whereEnd])
		} else if orderStart == -1 {
			res.Where = strings.TrimSpace(sql[whereStart:])
		}
	}

	// Finalize ORDER BY
	if orderStart != -1 {
		res.OrderBy = strings.TrimSpace(sql[orderStart:])
	}

	return res
}
