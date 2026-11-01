package slt

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CommandType indicates the directive type of an SLT record.
type CommandType int

const (
	CmdStatementOk CommandType = iota
	CmdStatementError
	CmdQuery
	CmdHalt
)

// SortMode specifies how query results are sorted before verification.
type SortMode int

const (
	SortNosort SortMode = iota
	SortRowsort
	SortValuesort
)

// Record represents a single executable directive parsed from an SLT file.
type Record struct {
	Type          CommandType
	TypeSignature string   // e.g. "I", "IT", "III"
	Sort          SortMode // nosort, rowsort, valuesort
	Label         string
	SQL           string
	ExpectedValues []string // flattened cell-by-cell values
	ExpectedHash  string
	ExpectedCount int
	ExpectedError string   // expected error substring for statement error (if specified)
	FilePath      string
	LineNumber    int
}

var (
	reStatement = regexp.MustCompile(`(?i)^statement\s+(ok|error)(?:\s+(.*))?$`)
	reQuery     = regexp.MustCompile(`(?i)^query\s+([A-Za-z]+)(?:\s+(nosort|rowsort|valuesort))?(?:\s+([a-zA-Z0-9_\-]+))?`)
	reHash      = regexp.MustCompile(`(?i)^(\d+)\s+values\s+hashing\s+to\s+([a-fA-F0-9]{32})`)
	reOnlyIf    = regexp.MustCompile(`(?i)^onlyif\s+(\S+)`)
	reSkipIf    = regexp.MustCompile(`(?i)^skipif\s+(\S+)`)
)

// ParseOptions controls parser behavior such as target database dialect filtering.
type ParseOptions struct {
	TargetDialects []string // e.g. ["sqlite", "lionsql", "postgresql"]
}

// DefaultParseOptions returns the standard target dialects for LionSQL.
func DefaultParseOptions() ParseOptions {
	return ParseOptions{
		TargetDialects: []string{"sqlite", "lionsql"},
	}
}

// IsTargetDialect reports whether target matches any configured target dialect.
func (opts ParseOptions) IsTargetDialect(target string) bool {
	norm := strings.ToLower(strings.TrimSpace(target))
	for _, d := range opts.TargetDialects {
		if strings.EqualFold(d, norm) {
			return true
		}
	}
	return false
}

// ParseFile parses an SLT test file from disk into a sequence of Records using DefaultParseOptions.
func ParseFile(path string) ([]Record, error) {
	return ParseFileWithOptions(path, DefaultParseOptions())
}

// ParseFileWithOptions parses an SLT test file with custom ParseOptions.
func ParseFileWithOptions(path string, opts ParseOptions) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseWithOptions(f, path, opts)
}

// Parse reads an SLT test stream and returns all parsed Records using DefaultParseOptions.
func Parse(r io.Reader, filePath string) ([]Record, error) {
	return ParseWithOptions(r, filePath, DefaultParseOptions())
}

// ParseWithOptions reads an SLT test stream and returns all parsed Records using custom ParseOptions.
func ParseWithOptions(r io.Reader, filePath string, opts ParseOptions) ([]Record, error) {
	scanner := bufio.NewScanner(r)
	// Allow large SQL statements and results (up to 4MB per line)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	var records []Record
	lineNo := 0
	var currentOnlyIfs []string
	var currentSkipIfs []string

	shouldSkip := func() bool {
		if len(currentOnlyIfs) > 0 {
			matched := false
			for _, d := range currentOnlyIfs {
				if opts.IsTargetDialect(d) {
					matched = true
					break
				}
			}
			if !matched {
				return true
			}
		}
		if len(currentSkipIfs) > 0 {
			for _, d := range currentSkipIfs {
				if opts.IsTargetDialect(d) {
					return true
				}
			}
		}
		return false
	}

	clearDirectives := func() {
		currentOnlyIfs = currentOnlyIfs[:0]
		currentSkipIfs = currentSkipIfs[:0]
	}

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip comments and blank lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Conditional directives: onlyif <target> / skipif <target>
		if m := reOnlyIf.FindStringSubmatch(trimmed); len(m) == 2 {
			currentOnlyIfs = append(currentOnlyIfs, m[1])
			continue
		}
		if m := reSkipIf.FindStringSubmatch(trimmed); len(m) == 2 {
			currentSkipIfs = append(currentSkipIfs, m[1])
			continue
		}

		// Halt directive
		if strings.EqualFold(trimmed, "halt") {
			skip := shouldSkip()
			clearDirectives()
			if skip {
				continue
			}
			records = append(records, Record{
				Type:       CmdHalt,
				FilePath:   filePath,
				LineNumber: lineNo,
			})
			break
		}

		// statement ok / statement error [expected_error_pattern]
		if m := reStatement.FindStringSubmatch(trimmed); len(m) >= 2 {
			cmdType := CmdStatementOk
			expectedErr := ""
			if strings.EqualFold(m[1], "error") {
				cmdType = CmdStatementError
				if len(m) >= 3 {
					expectedErr = strings.TrimSpace(m[2])
				}
			}
			stmtLineNo := lineNo

			// Read SQL lines until blank line or next directive
			var sqlLines []string
			for scanner.Scan() {
				lineNo++
				subLine := scanner.Text()
				subTrim := strings.TrimSpace(subLine)
				if subTrim == "" {
					break
				}
				if strings.HasPrefix(subTrim, "#") {
					continue
				}
				sqlLines = append(sqlLines, subLine)
			}

			skip := shouldSkip()
			clearDirectives()
			if !skip {
				records = append(records, Record{
					Type:          cmdType,
					SQL:           strings.Join(sqlLines, "\n"),
					ExpectedError: expectedErr,
					FilePath:      filePath,
					LineNumber:    stmtLineNo,
				})
			}
			continue
		}

		// query <types> [sort] [label]
		if m := reQuery.FindStringSubmatch(trimmed); len(m) >= 2 {
			typeSig := strings.ToUpper(m[1])
			sortMode := SortNosort
			if len(m) >= 3 && m[2] != "" {
				switch strings.ToLower(m[2]) {
				case "rowsort":
					sortMode = SortRowsort
				case "valuesort":
					sortMode = SortValuesort
				}
			}
			label := ""
			if len(m) >= 4 {
				label = m[3]
			}
			queryLineNo := lineNo

			// Read SQL lines until delimiter "----"
			var sqlLines []string
			for scanner.Scan() {
				lineNo++
				subLine := scanner.Text()
				subTrim := strings.TrimSpace(subLine)
				if subTrim == "----" {
					break
				}
				if subTrim == "" || strings.HasPrefix(subTrim, "#") {
					continue
				}
				sqlLines = append(sqlLines, subLine)
			}

			// Read expected results until blank line
			var resLines []string
			expectedHash := ""
			expectedCount := 0

			for scanner.Scan() {
				lineNo++
				resLine := scanner.Text()
				resTrim := strings.TrimSpace(resLine)
				if resTrim == "" {
					break
				}
				if strings.HasPrefix(resTrim, "#") {
					continue
				}

				if hm := reHash.FindStringSubmatch(resTrim); len(hm) == 3 {
					count, _ := strconv.Atoi(hm[1])
					expectedCount = count
					expectedHash = strings.ToLower(hm[2])
					break
				}

				resLines = append(resLines, resLine)
			}

			skip := shouldSkip()
			clearDirectives()
			if !skip {
				expectedValues := parseResultLines(resLines, len(typeSig), strings.HasSuffix(filePath, ".test"))
				records = append(records, Record{
					Type:           CmdQuery,
					TypeSignature:  typeSig,
					Sort:           sortMode,
					Label:          label,
					SQL:            strings.Join(sqlLines, "\n"),
					ExpectedValues: expectedValues,
					ExpectedHash:   expectedHash,
					ExpectedCount:  expectedCount,
					FilePath:       filePath,
					LineNumber:     queryLineNo,
				})
			}
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	return records, nil
}

// ComputeHash computes the MD5 digest of flattened result values formatted per SLT spec.
func ComputeHash(values []string) string {
	h := md5.New()
	for _, v := range values {
		_, _ = fmt.Fprintf(h, "%s\n", v)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// SortResults sorts flattened cell values according to the specified SortMode and column count.
func SortResults(values []string, numCols int, mode SortMode) []string {
	if len(values) == 0 || mode == SortNosort || numCols <= 0 {
		return values
	}

	if mode == SortValuesort {
		sorted := make([]string, len(values))
		copy(sorted, values)
		sort.Strings(sorted)
		return sorted
	}

	if mode == SortRowsort {
		numRows := len(values) / numCols
		if numRows <= 1 {
			return values
		}

		type rowItem struct {
			cells []string
			key   string
		}

		rows := make([]rowItem, numRows)
		for r := 0; r < numRows; r++ {
			cSlice := values[r*numCols : (r+1)*numCols]
			rows[r] = rowItem{
				cells: cSlice,
				key:   strings.Join(cSlice, "\x00"),
			}
		}

		sort.Slice(rows, func(i, j int) bool {
			return rows[i].key < rows[j].key
		})

		sorted := make([]string, 0, len(values))
		for _, r := range rows {
			sorted = append(sorted, r.cells...)
		}
		return sorted
	}

	return values
}

func parseResultLines(lines []string, numCols int, isOfficialTest bool) []string {
	if len(lines) == 0 {
		return nil
	}
	if isOfficialTest || numCols <= 1 {
		res := make([]string, len(lines))
		for i, l := range lines {
			res[i] = strings.TrimSpace(l)
		}
		return res
	}

	var values []string
	for _, l := range lines {
		values = append(values, splitRowFields(l, numCols)...)
	}
	return values
}

func splitRowFields(line string, numCols int) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if numCols <= 1 {
		return []string{line}
	}
	if strings.Contains(line, "\t") {
		parts := strings.Split(line, "\t")
		result := make([]string, len(parts))
		for i, p := range parts {
			result[i] = strings.TrimSpace(p)
		}
		return result
	}

	// Tokenize line respecting quotes ("..." or '...')
	var tokens []string
	var cur strings.Builder
	inSingleQuote := false
	inDoubleQuote := false

	runes := []rune(line)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		if inSingleQuote {
			if r == '\'' {
				// Check for escaped quote ''
				if i+1 < n && runes[i+1] == '\'' {
					cur.WriteRune('\'')
					i++
				} else {
					inSingleQuote = false
				}
			} else {
				cur.WriteRune(r)
			}
		} else if inDoubleQuote {
			if r == '"' {
				// Check for escaped quote "" or \"
				if i+1 < n && runes[i+1] == '"' {
					cur.WriteRune('"')
					i++
				} else {
					inDoubleQuote = false
				}
			} else {
				cur.WriteRune(r)
			}
		} else {
			switch r {
			case '\'':
				inSingleQuote = true
			case '"':
				inDoubleQuote = true
			case ' ', '\t':
				if cur.Len() > 0 {
					tokens = append(tokens, cur.String())
					cur.Reset()
				}
			default:
				cur.WriteRune(r)
			}
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}

	if len(tokens) == numCols {
		return tokens
	}

	// Fallback if token count didn't match numCols:
	// If fewer tokens, return what was parsed
	if len(tokens) < numCols {
		return tokens
	}

	// If more tokens than numCols, combine remaining into the last column
	res := make([]string, numCols)
	copy(res, tokens[:numCols-1])
	res[numCols-1] = strings.Join(tokens[numCols-1:], " ")
	return res
}
