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
	FilePath      string
	LineNumber    int
}

var (
	reStatement = regexp.MustCompile(`(?i)^statement\s+(ok|error)`)
	reQuery     = regexp.MustCompile(`(?i)^query\s+([A-Za-z]+)(?:\s+(nosort|rowsort|valuesort))?(?:\s+([a-zA-Z0-9_\-]+))?`)
	reHash      = regexp.MustCompile(`(?i)^(\d+)\s+values\s+hashing\s+to\s+([a-fA-F0-9]{32})`)
)

// ParseFile parses an SLT test file from disk into a sequence of Records.
func ParseFile(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f, path)
}

// Parse reads an SLT test stream and returns all parsed Records.
func Parse(r io.Reader, filePath string) ([]Record, error) {
	scanner := bufio.NewScanner(r)
	// Allow large SQL statements and results (up to 4MB per line)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	var records []Record
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip comments and blank lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Halt directive
		if strings.EqualFold(trimmed, "halt") {
			records = append(records, Record{
				Type:       CmdHalt,
				FilePath:   filePath,
				LineNumber: lineNo,
			})
			break
		}

		// statement ok / statement error
		if m := reStatement.FindStringSubmatch(trimmed); len(m) == 2 {
			cmdType := CmdStatementOk
			if strings.EqualFold(m[1], "error") {
				cmdType = CmdStatementError
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
			records = append(records, Record{
				Type:       cmdType,
				SQL:        strings.Join(sqlLines, "\n"),
				FilePath:   filePath,
				LineNumber: stmtLineNo,
			})
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
	if strings.Contains(line, "\t") {
		return strings.Split(line, "\t")
	}
	fields := strings.Fields(line)
	if len(fields) <= 1 || len(fields) == numCols || numCols <= 1 {
		if len(fields) == 0 {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				return nil
			}
			return []string{trimmed}
		}
		if len(fields) == 1 {
			return []string{fields[0]}
		}
		if len(fields) == numCols {
			return fields
		}
	}
	// If more tokens than numCols, split into exactly numCols preserving spaces in the last token
	parts := make([]string, 0, numCols)
	remaining := strings.TrimSpace(line)
	for i := 0; i < numCols-1; i++ {
		idx := strings.IndexAny(remaining, " \t")
		if idx == -1 {
			break
		}
		parts = append(parts, remaining[:idx])
		remaining = strings.TrimSpace(remaining[idx:])
	}
	if remaining != "" {
		parts = append(parts, remaining)
	}
	return parts
}
