package planner

import (
	"sort"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
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

		p := proc.NewParser(proc.NewLexer(trimmed))
		expr := p.ParseExpr()
		if expr == nil || len(p.Errors()) > 0 {
			continue
		}

		switch e := expr.(type) {
		case *proc.BinaryExpr:
			rawCol, isCol := extractSimpleColIdent(e.Left)
			if !isCol {
				continue
			}
			col := strings.ToLower(extractCol(rawCol))
			if col != partCol {
				continue
			}
			val := strings.TrimSpace(extractExprRawVal(e.Right))
			switch e.Op {
			case proc.TokenEq, proc.TokenAssign:
				foundPartitionFilter = true
				if pKey, err := schema.ComputePartitionKeyFromValue(storage.StringValue(val)); err == nil {
					exactPartitions = map[string]bool{pKey: true}
				}
			case proc.TokenGte, proc.TokenGt:
				foundPartitionFilter = true
				if k, err := schema.ComputePartitionKeyFromValue(storage.StringValue(val)); err == nil {
					if minPart == "" || k > minPart {
						minPart = k
					}
				}
			case proc.TokenLte:
				foundPartitionFilter = true
				if k, err := schema.ComputePartitionKeyFromValue(storage.StringValue(val)); err == nil {
					if maxPart == "" || k < maxPart {
						maxPart = k
					}
				}
			case proc.TokenLt:
				foundPartitionFilter = true
				ceilKey := computeCeilPartitionKey(schema, val)
				if ceilKey != "" {
					if maxPart == "" || ceilKey < maxPart {
						maxPart = ceilKey
					}
				}
			}

		case *proc.InExpr:
			if e.Not || len(e.List) == 0 {
				continue
			}
			rawCol, isCol := extractSimpleColIdent(e.Expr)
			if !isCol {
				continue
			}
			col := strings.ToLower(extractCol(rawCol))
			if col == partCol {
				foundPartitionFilter = true
				exactPartitions = make(map[string]bool)
				for _, item := range e.List {
					cleaned := strings.Trim(strings.TrimSpace(extractExprRawVal(item)), `'" `)
					if pKey, err := schema.ComputePartitionKeyFromValue(storage.StringValue(cleaned)); err == nil {
						exactPartitions[pKey] = true
					}
				}
			}

		case *proc.BetweenExpr:
			if e.Not {
				continue
			}
			rawCol, isCol := extractSimpleColIdent(e.Expr)
			if !isCol {
				continue
			}
			col := strings.ToLower(extractCol(rawCol))
			if col == partCol {
				foundPartitionFilter = true
				v1 := strings.TrimSpace(extractExprRawVal(e.Low))
				v2 := strings.TrimSpace(extractExprRawVal(e.High))
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
