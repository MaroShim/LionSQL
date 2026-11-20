package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/sqlutil"
)

// execCreateSequence parses and executes CREATE SEQUENCE.
func (s *Coordinator) execCreateSequence(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	trimmed := strings.TrimSpace(query)
	rem, ok := sqlutil.TrimPrefixKeyword(trimmed, "CREATE SEQUENCE")
	if !ok {
		return nil, fmt.Errorf("invalid CREATE SEQUENCE statement")
	}
	rem = strings.TrimSpace(rem)

	// Check for IF NOT EXISTS
	ifNotExists := false
	if after, ok := sqlutil.TrimPrefixKeyword(rem, "IF NOT EXISTS"); ok {
		ifNotExists = true
		rem = strings.TrimSpace(after)
	}

	toks := strings.Fields(rem)
	if len(toks) == 0 {
		return nil, fmt.Errorf("missing sequence name in CREATE SEQUENCE")
	}

	rawName := toks[0]
	seqName := cleanIdentifier(rawName)
	if dotIdx := strings.LastIndex(seqName, "."); dotIdx != -1 {
		seqName = seqName[dotIdx+1:]
	}

	// Defaults for sequence
	seq := &catalog.SequenceDef{
		Name:      seqName,
		StartWith: 1,
		Increment: 1,
		MinValue:  1,
		MaxValue:  math.MaxInt64,
		Cycle:     false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	hasStartWith := false
	hasMin := false
	hasMax := false
	hasInc := false
	hasCycle := false
	hasNoCycle := false

	// Parse optional clauses
	idx := 1
	for idx < len(toks) {
		tok := strings.ToUpper(toks[idx])
		switch tok {
		case "AS":
			// AS data_type (e.g. AS INT, AS BIGINT)
			idx += 2
			continue
		case "START":
			if idx+2 < len(toks) && strings.EqualFold(toks[idx+1], "WITH") {
				val, err := strconv.ParseInt(toks[idx+2], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid START WITH value %q: %w", toks[idx+2], err)
				}
				seq.StartWith = val
				hasStartWith = true
				idx += 3
				continue
			}
			return nil, fmt.Errorf("syntax error near START in CREATE SEQUENCE")
		case "INCREMENT":
			if idx+2 < len(toks) && strings.EqualFold(toks[idx+1], "BY") {
				if hasInc {
					return nil, fmt.Errorf("conflicting or redundant options in CREATE SEQUENCE")
				}
				val, err := strconv.ParseInt(toks[idx+2], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid INCREMENT BY value %q: %w", toks[idx+2], err)
				}
				if val == 0 {
					return nil, fmt.Errorf("INCREMENT BY value cannot be zero")
				}
				seq.Increment = val
				hasInc = true
				idx += 3
				continue
			}
			return nil, fmt.Errorf("syntax error near INCREMENT in CREATE SEQUENCE")
		case "MINVALUE":
			if hasMin {
				return nil, fmt.Errorf("conflicting or redundant options: duplicate MINVALUE in CREATE SEQUENCE")
			}
			if idx+1 < len(toks) {
				val, err := strconv.ParseInt(toks[idx+1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid MINVALUE value %q: %w", toks[idx+1], err)
				}
				seq.MinValue = val
				hasMin = true
				idx += 2
				continue
			}
			return nil, fmt.Errorf("missing value for MINVALUE in CREATE SEQUENCE")
		case "NO":
			if idx+1 < len(toks) {
				sub := strings.ToUpper(toks[idx+1])
				if sub == "MINVALUE" {
					seq.MinValue = math.MinInt64
					hasMin = true
					idx += 2
					continue
				} else if sub == "MAXVALUE" {
					seq.MaxValue = math.MaxInt64
					hasMax = true
					idx += 2
					continue
				} else if sub == "CYCLE" {
					if hasCycle {
						return nil, fmt.Errorf("conflicting options: CYCLE and NO CYCLE cannot both be specified")
					}
					seq.Cycle = false
					hasNoCycle = true
					idx += 2
					continue
				}
			}
			return nil, fmt.Errorf("syntax error near NO in CREATE SEQUENCE")
		case "MAXVALUE":
			if hasMax {
				return nil, fmt.Errorf("conflicting or redundant options: duplicate MAXVALUE in CREATE SEQUENCE")
			}
			if idx+1 < len(toks) {
				val, err := strconv.ParseInt(toks[idx+1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid MAXVALUE value %q: %w", toks[idx+1], err)
				}
				seq.MaxValue = val
				hasMax = true
				idx += 2
				continue
			}
			return nil, fmt.Errorf("missing value for MAXVALUE in CREATE SEQUENCE")
		case "CYCLE":
			if hasNoCycle {
				return nil, fmt.Errorf("conflicting options: CYCLE and NO CYCLE cannot both be specified")
			}
			seq.Cycle = true
			hasCycle = true
			idx++
			continue
		case "CACHE":
			if idx+1 < len(toks) {
				val, err := strconv.ParseInt(toks[idx+1], 10, 64)
				if err != nil || val <= 0 {
					return nil, fmt.Errorf("CACHE value must be a positive integer, got %q", toks[idx+1])
				}
				idx += 2
				continue
			}
			return nil, fmt.Errorf("missing value for CACHE in CREATE SEQUENCE")
		case "NOCACHE":
			idx++
			continue
		default:
			idx++
		}
	}

	// Adjust defaults based on increment direction if bounds weren't explicitly supplied
	if !hasStartWith {
		if seq.Increment < 0 {
			seq.StartWith = seq.MaxValue
		} else {
			seq.StartWith = seq.MinValue
		}
	}

	// Boundary validations
	if seq.MinValue > seq.MaxValue {
		return nil, fmt.Errorf("MINVALUE (%d) cannot be greater than MAXVALUE (%d)", seq.MinValue, seq.MaxValue)
	}
	if seq.StartWith < seq.MinValue {
		return nil, fmt.Errorf("START value (%d) cannot be less than MINVALUE (%d)", seq.StartWith, seq.MinValue)
	}
	if seq.StartWith > seq.MaxValue {
		return nil, fmt.Errorf("START value (%d) cannot be greater than MAXVALUE (%d)", seq.StartWith, seq.MaxValue)
	}

	if s.catalog == nil {
		return nil, fmt.Errorf("catalog not initialized")
	}

	// Check existence
	existing, err := s.catalog.GetSequence(seqName)
	if err == nil && existing != nil {
		if ifNotExists {
			return &QueryResult{
				Success:    true,
				Command:    "CREATE SEQUENCE",
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}
		return nil, fmt.Errorf("relation %q already exists", seqName)
	}

	if err := s.catalog.RegisterSequence(seq); err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:    true,
		Command:    "CREATE SEQUENCE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Sequence %q created successfully.", seqName)},
	}, nil
}

// execAlterSequence parses and executes ALTER SEQUENCE.
func (s *Coordinator) execAlterSequence(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	trimmed := strings.TrimSpace(query)
	rem, ok := sqlutil.TrimPrefixKeyword(trimmed, "ALTER SEQUENCE")
	if !ok {
		return nil, fmt.Errorf("invalid ALTER SEQUENCE statement")
	}
	rem = strings.TrimSpace(rem)

	toks := strings.Fields(rem)
	if len(toks) == 0 {
		return nil, fmt.Errorf("missing sequence name in ALTER SEQUENCE")
	}

	seqName := cleanIdentifier(toks[0])
	if dotIdx := strings.LastIndex(seqName, "."); dotIdx != -1 {
		seqName = seqName[dotIdx+1:]
	}

	if s.catalog == nil {
		return nil, fmt.Errorf("catalog not initialized")
	}

	seq, err := s.catalog.GetSequence(seqName)
	if err != nil || seq == nil {
		return nil, fmt.Errorf("sequence %q does not exist", seqName)
	}

	idx := 1
	for idx < len(toks) {
		tok := strings.ToUpper(toks[idx])
		switch tok {
		case "RESTART":
			restartVal := seq.StartWith
			if idx+1 < len(toks) && strings.EqualFold(toks[idx+1], "WITH") {
				if idx+2 >= len(toks) {
					return nil, fmt.Errorf("missing value for RESTART WITH")
				}
				val, err := strconv.ParseInt(toks[idx+2], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid RESTART WITH value %q: %w", toks[idx+2], err)
				}
				restartVal = val
				idx += 3
			} else {
				idx++
			}
			if err := seq.Restart(restartVal); err != nil {
				return nil, err
			}
			continue
		case "INCREMENT":
			if idx+2 < len(toks) && strings.EqualFold(toks[idx+1], "BY") {
				val, err := strconv.ParseInt(toks[idx+2], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid INCREMENT BY value: %w", err)
				}
				if val == 0 {
					return nil, fmt.Errorf("INCREMENT BY value cannot be zero")
				}
				seq.Increment = val
				idx += 3
				continue
			}
			return nil, fmt.Errorf("syntax error near INCREMENT in ALTER SEQUENCE")
		case "MINVALUE":
			if idx+1 < len(toks) {
				val, err := strconv.ParseInt(toks[idx+1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid MINVALUE: %w", err)
				}
				if val > seq.MaxValue {
					return nil, fmt.Errorf("MINVALUE (%d) cannot be greater than MAXVALUE (%d)", val, seq.MaxValue)
				}
				seq.MinValue = val
				idx += 2
				continue
			}
			return nil, fmt.Errorf("missing value for MINVALUE")
		case "MAXVALUE":
			if idx+1 < len(toks) {
				val, err := strconv.ParseInt(toks[idx+1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid MAXVALUE: %w", err)
				}
				if val < seq.MinValue {
					return nil, fmt.Errorf("MAXVALUE (%d) cannot be less than MINVALUE (%d)", val, seq.MinValue)
				}
				seq.MaxValue = val
				idx += 2
				continue
			}
			return nil, fmt.Errorf("missing value for MAXVALUE")
		case "CYCLE":
			seq.Cycle = true
			idx++
			continue
		case "NO":
			if idx+1 < len(toks) && strings.EqualFold(toks[idx+1], "CYCLE") {
				seq.Cycle = false
				idx += 2
				continue
			}
			idx++
		default:
			idx++
		}
	}

	return &QueryResult{
		Success:    true,
		Command:    "ALTER SEQUENCE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Sequence %q altered successfully.", seqName)},
	}, nil
}

// execDropSequence parses and executes DROP SEQUENCE.
func (s *Coordinator) execDropSequence(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	trimmed := strings.TrimSpace(query)
	rem, ok := sqlutil.TrimPrefixKeyword(trimmed, "DROP SEQUENCE")
	if !ok {
		return nil, fmt.Errorf("invalid DROP SEQUENCE statement")
	}
	rem = strings.TrimSpace(rem)

	ifExists := false
	if after, ok := sqlutil.TrimPrefixKeyword(rem, "IF EXISTS"); ok {
		ifExists = true
		rem = strings.TrimSpace(after)
	}

	toks := strings.Fields(rem)
	if len(toks) == 0 {
		return nil, fmt.Errorf("missing sequence name in DROP SEQUENCE")
	}

	seqName := cleanIdentifier(toks[0])
	if dotIdx := strings.LastIndex(seqName, "."); dotIdx != -1 {
		seqName = seqName[dotIdx+1:]
	}

	if s.catalog == nil {
		return nil, fmt.Errorf("catalog not initialized")
	}

	if err := s.catalog.DropSequence(seqName, ifExists); err != nil {
		return nil, fmt.Errorf("sequence %q does not exist", seqName)
	}

	return &QueryResult{
		Success:    true,
		Command:    "DROP SEQUENCE",
		DurationUS: time.Since(start).Microseconds(),
		Messages:   []string{fmt.Sprintf("Sequence %q dropped successfully.", seqName)},
	}, nil
}
