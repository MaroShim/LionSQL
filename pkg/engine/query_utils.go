package engine

import (
	"strconv"
	"strings"
	"time"
)

// SplitStatements splits a multi-statement SQL batch by semicolons, properly respecting quotes and BEGIN...END blocks.
func SplitStatements(query string) []string {
	var stmts []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	blockDepth := 0
	isProcOrFunc := false

	checkProc := func(s string) bool {
		sUpper := strings.ToUpper(strings.TrimSpace(s))
		return strings.HasPrefix(sUpper, "CREATE PROCEDURE") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER PROCEDURE") ||
			strings.HasPrefix(sUpper, "ALTER PROCEDURE") ||
			strings.HasPrefix(sUpper, "CREATE FUNCTION") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER FUNCTION") ||
			strings.HasPrefix(sUpper, "ALTER FUNCTION") ||
			strings.HasPrefix(sUpper, "CREATE TRIGGER") ||
			strings.HasPrefix(sUpper, "CREATE OR ALTER TRIGGER") ||
			strings.HasPrefix(sUpper, "ALTER TRIGGER")
	}

	for i := 0; i < len(query); i++ {
		ch := query[i]
		if inQuote {
			if ch == quoteChar && (i == 0 || query[i-1] != '\\') {
				inQuote = false
			}
			cur.WriteByte(ch)
		} else {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				cur.WriteByte(ch)
			} else {
				if !isProcOrFunc && checkProc(cur.String()) {
					isProcOrFunc = true
				}

				if isProcOrFunc {
					if (i == 0 || !isIdentChar(query[i-1])) && i+5 <= len(query) && strings.EqualFold(query[i:i+5], "BEGIN") && (i+5 == len(query) || !isIdentChar(query[i+5])) {
						blockDepth++
					} else if (i == 0 || !isIdentChar(query[i-1])) && i+4 <= len(query) && strings.EqualFold(query[i:i+4], "CASE") && (i+4 == len(query) || !isIdentChar(query[i+4])) {
						blockDepth++
					} else if (i == 0 || !isIdentChar(query[i-1])) && i+3 <= len(query) && strings.EqualFold(query[i:i+3], "END") && (i+3 == len(query) || !isIdentChar(query[i+3])) {
						if blockDepth > 0 {
							blockDepth--
						}
					}
				}

				if ch == ';' {
					if isProcOrFunc && blockDepth > 0 {
						cur.WriteByte(ch)
					} else {
						stmt := strings.TrimSpace(cur.String())
						if stmt != "" {
							stmts = append(stmts, stmt)
						}
						cur.Reset()
						isProcOrFunc = false
						blockDepth = 0
					}
				} else {
					cur.WriteByte(ch)
				}
			}
		}
	}
	if cur.Len() > 0 {
		stmt := strings.TrimSpace(cur.String())
		if stmt != "" {
			stmts = append(stmts, stmt)
		}
	}
	return stmts
}

func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '@' || ch == '#'
}

// ParseTimeoutDuration parses duration strings with units (ms, s, m) or plain integers as milliseconds.
func ParseTimeoutDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return 0, nil
		}
		return d, nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		if ms <= 0 {
			return 0, nil
		}
		return time.Duration(ms) * time.Millisecond, nil
	}
	return 0, nil
}
