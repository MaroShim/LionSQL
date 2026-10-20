package planner

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"lionsql/pkg/storage"
)

var (
	condEqRe      = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*=\s*['"]?([^'"]+)['"]?\s*$`)
	condGteRe     = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*>=\s*['"]?([^'"]+)['"]?\s*$`)
	condGtRe      = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*>\s*['"]?([^'"]+)['"]?\s*$`)
	condLteRe     = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*<=\s*['"]?([^'"]+)['"]?\s*$`)
	condLtRe      = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*<\s*['"]?([^'"]+)['"]?\s*$`)
	condBetweenRe = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s+BETWEEN\s+['"]?([^'"]+?)['"]?\s+AND\s+['"]?([^'"]+?)['"]?\s*$`)
	condInRe      = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s+IN\s*\(([^)]+)\)\s*$`)
)

// PrunePartitions evaluates filter conditions against a partitioned table's partition key
// and returns the subset of matching partition identifiers.
// If the table is not partitioned, or no partition conditions exist, it returns nil (scan all).
func PrunePartitions(schema *storage.TableSchema, conds []string, availablePartitions []string) []string {
	if schema == nil || !schema.IsPartitioned() || len(availablePartitions) == 0 {
		return nil
	}

	partCol := strings.ToLower(schema.Partition.Column)

	var exactPartitions map[string]bool
	var minPart, maxPart string
	foundPartitionFilter := false

	for _, cond := range conds {
		trimmed := strings.TrimSpace(cond)
		if trimmed == "" {
			continue
		}

		// 1. Equality: col = 'val'
		if m := condEqRe.FindStringSubmatch(trimmed); len(m) == 3 {
			col := strings.ToLower(extractCol(m[1]))
			if col == partCol {
				foundPartitionFilter = true
				if pKey, err := schema.ComputePartitionKeyFromValue(storage.StringValue(strings.TrimSpace(m[2]))); err == nil {
					exactPartitions = map[string]bool{pKey: true}
				}
				continue
			}
		}

		// 2. IN list: col IN ('val1', 'val2')
		if m := condInRe.FindStringSubmatch(trimmed); len(m) == 3 {
			col := strings.ToLower(extractCol(m[1]))
			if col == partCol {
				foundPartitionFilter = true
				exactPartitions = make(map[string]bool)
				parts := strings.Split(m[2], ",")
				for _, p := range parts {
					cleaned := strings.Trim(strings.TrimSpace(p), `'" `)
					if pKey, err := schema.ComputePartitionKeyFromValue(storage.StringValue(cleaned)); err == nil {
						exactPartitions[pKey] = true
					}
				}
				continue
			}
		}

		// 3. BETWEEN: col BETWEEN 'val1' AND 'val2'
		if m := condBetweenRe.FindStringSubmatch(trimmed); len(m) == 4 {
			col := strings.ToLower(extractCol(m[1]))
			if col == partCol {
				foundPartitionFilter = true
				v1 := strings.TrimSpace(m[2])
				v2 := strings.TrimSpace(m[3])
				if k1, err := schema.ComputePartitionKeyFromValue(storage.StringValue(v1)); err == nil {
					if minPart == "" || k1 > minPart {
						minPart = k1
					}
				}
				if k2, err := schema.ComputePartitionKeyFromValue(storage.StringValue(v2)); err == nil {
					if maxPart == "" || k2 < maxPart {
						maxPart = k2
					}
				}
				continue
			}
		}

		// 4. Greater than or equal: col >= 'val'
		if m := condGteRe.FindStringSubmatch(trimmed); len(m) == 3 {
			col := strings.ToLower(extractCol(m[1]))
			if col == partCol {
				foundPartitionFilter = true
				val := strings.TrimSpace(m[2])
				if k, err := schema.ComputePartitionKeyFromValue(storage.StringValue(val)); err == nil {
					if minPart == "" || k > minPart {
						minPart = k
					}
				}
				continue
			}
		}

		// 5. Greater than: col > 'val'
		if m := condGtRe.FindStringSubmatch(trimmed); len(m) == 3 {
			col := strings.ToLower(extractCol(m[1]))
			if col == partCol {
				foundPartitionFilter = true
				val := strings.TrimSpace(m[2])
				if k, err := schema.ComputePartitionKeyFromValue(storage.StringValue(val)); err == nil {
					if minPart == "" || k > minPart {
						minPart = k
					}
				}
				continue
			}
		}

		// 6. Less than or equal: col <= 'val'
		if m := condLteRe.FindStringSubmatch(trimmed); len(m) == 3 {
			col := strings.ToLower(extractCol(m[1]))
			if col == partCol {
				foundPartitionFilter = true
				val := strings.TrimSpace(m[2])
				if k, err := schema.ComputePartitionKeyFromValue(storage.StringValue(val)); err == nil {
					if maxPart == "" || k < maxPart {
						maxPart = k
					}
				}
				continue
			}
		}

		// 7. Less than: col < 'val'
		if m := condLtRe.FindStringSubmatch(trimmed); len(m) == 3 {
			col := strings.ToLower(extractCol(m[1]))
			if col == partCol {
				foundPartitionFilter = true
				val := strings.TrimSpace(m[2])
				// For < boundary, compute ceiling partition key
				ceilKey := computeCeilPartitionKey(schema, val)
				if ceilKey != "" {
					if maxPart == "" || ceilKey < maxPart {
						maxPart = ceilKey
					}
				}
				continue
			}
		}
	}

	if !foundPartitionFilter {
		return nil
	}

	var selected []string
	for _, p := range availablePartitions {
		if exactPartitions != nil {
			if !exactPartitions[p] {
				continue
			}
		}
		if minPart != "" && p < minPart {
			continue
		}
		if maxPart != "" && p > maxPart {
			continue
		}
		selected = append(selected, p)
	}

	sort.Strings(selected)
	return selected
}

func extractCol(raw string) string {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "."); idx != -1 {
		raw = raw[idx+1:]
	}
	return cleanIdent(raw)
}

func computeCeilPartitionKey(schema *storage.TableSchema, valStr string) string {
	fn := strings.ToUpper(schema.Partition.Func)
	// Try parsing as date
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02", "2006/01/02"}
	for _, l := range layouts {
		if t, err := time.Parse(l, valStr); err == nil {
			// Subtract 1 nanosecond to get the strict upper bound bucket
			prev := t.Add(-time.Nanosecond)
			switch fn {
			case "TOYYYYMM", "MONTH":
				return prev.Format("200601")
			case "TOYYYYMMDD", "DAY":
				return prev.Format("20060102")
			case "TOYYYY", "YEAR":
				return prev.Format("2006")
			}
		}
	}
	k, _ := schema.ComputePartitionKeyFromValue(storage.StringValue(valStr))
	return k
}
