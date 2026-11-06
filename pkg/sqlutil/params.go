package sqlutil

import (
	"strconv"
	"strings"
)

// PgParamToken represents the location and index of a $N parameter placeholder in SQL.
type PgParamToken struct {
	Index int // 1-based parameter index (e.g. 1 for $1)
	Start int // byte offset of '$'
	End   int // byte offset immediately after the last digit
}

// ScanPgParams scans sql for top-level $1, $2, ... parameter placeholders outside
// single-quoted strings, double-quoted identifiers, dollar-quoted strings, and comments.
// It invokes onParam for each valid parameter found.
func ScanPgParams(sql string, onParam func(token PgParamToken)) {
	n := len(sql)
	if n < 2 || !strings.ContainsRune(sql, '$') {
		return
	}

	for i := 0; i < n; {
		ch := sql[i]

		// 1. Single-quoted string literal: '...' with '' escape
		if ch == '\'' {
			i++
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}

		// 2. Double-quoted identifier: "..." with "" escape
		if ch == '"' {
			i++
			for i < n {
				if sql[i] == '"' {
					if i+1 < n && sql[i+1] == '"' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}

		// 3. Line comment: -- ...
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			if i < n && sql[i] == '\n' {
				i++
			}
			continue
		}

		// 4. Block comment: /* ... */
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			for i < n {
				if sql[i] == '*' && i+1 < n && sql[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
			continue
		}

		// 5. Dollar parameter ($1, $2...) or Dollar-quoted string ($$...$$ / $tag$...$tag$)
		if ch == '$' {
			// Check if this is a parameter: $ followed by digits 1-9
			if i+1 < n && sql[i+1] >= '1' && sql[i+1] <= '9' {
				j := i + 1
				for j < n && sql[j] >= '0' && sql[j] <= '9' {
					j++
				}
				if idx, err := strconv.Atoi(sql[i+1 : j]); err == nil && idx > 0 {
					onParam(PgParamToken{
						Index: idx,
						Start: i,
						End:   j,
					})
					i = j
					continue
				}
			}

			// Check if this is an opening dollar-quoted string: $$ or $tag$
			if tagEnd := findDollarTagEnd(sql, i); tagEnd != -1 {
				tag := sql[i : tagEnd+1]
				i = tagEnd + 1
				// Scan until closing tag
				if closeIdx := strings.Index(sql[i:], tag); closeIdx != -1 {
					i += closeIdx + len(tag)
				} else {
					// Unclosed dollar quote: consume rest
					i = n
				}
				continue
			}
		}

		i++
	}
}

// findDollarTagEnd checks if sql[start] starts a valid dollar quote tag ($$ or $tag$)
// and returns the index of the closing '$' of the opening tag, or -1 if invalid.
func findDollarTagEnd(sql string, start int) int {
	n := len(sql)
	if start >= n || sql[start] != '$' {
		return -1
	}
	// Case 1: $$
	if start+1 < n && sql[start+1] == '$' {
		return start + 1
	}
	// Case 2: $tag$ where tag starts with letter or underscore and contains alphanumeric/underscore
	j := start + 1
	if j < n && (isTagLetter(sql[j]) || sql[j] == '_') {
		j++
		for j < n && (isTagLetter(sql[j]) || isTagDigit(sql[j]) || sql[j] == '_') {
			j++
		}
		if j < n && sql[j] == '$' {
			return j
		}
	}
	return -1
}

func isTagLetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isTagDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

// ReplacePgParams replaces all top-level $1, $2... parameter placeholders in sql
// with the string returned by replacer(idx). Placeholders inside string literals,
// dollar-quoted strings, and comments are preserved untouched.
func ReplacePgParams(sql string, replacer func(idx int) string) string {
	if !strings.ContainsRune(sql, '$') {
		return sql
	}

	var b strings.Builder
	b.Grow(len(sql) + 16)
	last := 0
	hasParams := false

	ScanPgParams(sql, func(token PgParamToken) {
		hasParams = true
		b.WriteString(sql[last:token.Start])
		b.WriteString(replacer(token.Index))
		last = token.End
	})

	if !hasParams {
		return sql
	}

	if last < len(sql) {
		b.WriteString(sql[last:])
	}
	return b.String()
}

// TranslatePgParamsToAtParams replaces top-level $1, $2... with @p1, @p2...
// Placeholders inside string literals, dollar quotes, and comments are preserved.
func TranslatePgParamsToAtParams(sql string) string {
	return ReplacePgParams(sql, func(idx int) string {
		return "@p" + strconv.Itoa(idx)
	})
}

// MaxPgParamIndex returns the maximum parameter index (e.g. 5 for $5) found
// at top-level outside string literals, dollar quotes, and comments.
// If no valid parameter is found, it returns 0.
func MaxPgParamIndex(sql string) int {
	maxIdx := 0
	ScanPgParams(sql, func(token PgParamToken) {
		if token.Index > maxIdx {
			maxIdx = token.Index
		}
	})
	return maxIdx
}
