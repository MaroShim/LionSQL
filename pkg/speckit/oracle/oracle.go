package oracle

import (
	"fmt"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/grammar"
)

// DiscrepancyType classifies the oracle that detected the divergence.
type DiscrepancyType string

const (
	DiscrepancyNoREC      DiscrepancyType = "NoREC"
	DiscrepancyDualEngine DiscrepancyType = "DualEngine"
	DiscrepancyErrorDiff  DiscrepancyType = "ErrorDifferential"
)

// Discrepancy represents a detected behavioral anomaly between expected and actual results.
type Discrepancy struct {
	Type            DiscrepancyType
	Table           grammar.TableSchema
	TableDDL        string
	InsertDMLs      []string
	Query           string
	EquivalentQuery string
	Expected        string
	Actual          string
	Details         string
}

func (d *Discrepancy) String() string {
	return fmt.Sprintf("[%s Discrepancy]\nQuery: %s\nEquivalent: %s\nExpected: %s\nActual: %s\nDetails: %s",
		d.Type, d.Query, d.EquivalentQuery, d.Expected, d.Actual, d.Details)
}

// GenerateReproSQL generates a self-contained SQL script to reproduce the discrepancy.
func (d *Discrepancy) GenerateReproSQL() string {
	var sb stringsBuilder
	sb.WriteString(fmt.Sprintf("-- [DISCREPANCY: %s]\n", d.Type))
	sb.WriteString(fmt.Sprintf("-- Details: %s\n\n", d.Details))
	sb.WriteString(d.TableDDL + "\n")
	for _, dml := range d.InsertDMLs {
		sb.WriteString(dml + "\n")
	}
	sb.WriteString("\n-- Primary Query:\n" + d.Query + "\n")
	if d.EquivalentQuery != "" {
		sb.WriteString("\n-- Equivalent Query:\n" + d.EquivalentQuery + "\n")
	}
	return sb.String()
}

type stringsBuilder struct {
	buf []byte
}

func (b *stringsBuilder) WriteString(s string) {
	b.buf = append(b.buf, s...)
}

func (b *stringsBuilder) String() string {
	return string(b.buf)
}

// ExtractCount extracts an integer count from QueryResult safely.
func ExtractCount(res *engine.QueryResult) (int64, error) {
	if res == nil || len(res.Rows) == 0 {
		return 0, fmt.Errorf("empty result")
	}
	row := res.Rows[0]
	for _, v := range row {
		if v == nil {
			return 0, nil
		}
		switch val := v.(type) {
		case int64:
			return val, nil
		case int:
			return int64(val), nil
		case int32:
			return int64(val), nil
		case float64:
			return int64(val), nil
		default:
			var n int64
			if _, err := fmt.Sscanf(fmt.Sprintf("%v", v), "%d", &n); err == nil {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("no numeric value in row: %+v", row)
}
