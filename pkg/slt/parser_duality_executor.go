package slt

import (
	"fmt"
	"strings"

	"lionsql/pkg/proc"
	antlrParser "lionsql/pkg/proc/parser/antlr"
	"lionsql/pkg/sqlutil"
)

// DualParserExecutor wraps an underlying SLT Executor and runs Tier-1 Pratt and Tier-2 ANTLR4
// parsers across all executed statements to enforce 1:1 parser differential equivalence (SLT-DUALITY-PARSER-1).
type DualParserExecutor struct {
	underlying Executor
	procsCount int
	stmtsCount int
}

// NewDualParserExecutor creates a new DualParserExecutor wrapping the given underlying executor.
func NewDualParserExecutor(underlying Executor) *DualParserExecutor {
	return &DualParserExecutor{
		underlying: underlying,
	}
}

// Execute runs the SQL through the underlying executor and validates Dual-Parser equivalence.
func (d *DualParserExecutor) Execute(sqlStr string) ([]string, []map[string]any, error) {
	d.stmtsCount++
	trimmed := strings.TrimSpace(sqlStr)

	// 1. Check if the statement is a Stored Procedure DDL
	isProcDDL := sqlutil.HasAnyPrefixKeyword(trimmed,
		"CREATE PROCEDURE", "CREATE PROC",
		"CREATE OR ALTER PROCEDURE", "CREATE OR ALTER PROC",
		"ALTER PROCEDURE", "ALTER PROC")

	if isProcDDL {
		d.procsCount++
		// Tier 1: Fast-Path Native Pratt Parser
		lexer := proc.NewLexer(sqlStr)
		parser := proc.NewParser(lexer)
		prattPlan, prattErr := parser.ParseProcedure()

		// Tier 2: Official Full-Grammar ANTLR4 Parser (with dialect normalizations for ANTLR grammar parity)
		normSql := normalizeScriptForAntlr(sqlStr)
		antlrPlan, antlrErr := antlrParser.BuildProcedure(normSql)

		// Dual-Parser Equivalence Check:
		// If either parser succeeds or fails unexpectedly, verify differential parity
		if (prattErr == nil) != (antlrErr == nil) {
			return nil, nil, &DualityMismatchError{
				SQL:        sqlStr,
				LabelA:     "Pratt_Tier1",
				LabelB:     "ANTLR4_Tier2",
				Divergence: fmt.Sprintf("Parser error presence mismatch:\n    Pratt: %v\n    ANTLR4: %v", prattErr, antlrErr),
			}
		}

		if prattErr == nil && antlrPlan != nil {
			// Compare Procedure Name
			if !strings.EqualFold(prattPlan.Name, antlrPlan.Name) {
				return nil, nil, &DualityMismatchError{
					SQL:        sqlStr,
					LabelA:     "Pratt_Tier1",
					LabelB:     "ANTLR4_Tier2",
					Divergence: fmt.Sprintf("Procedure name mismatch: Pratt=%q vs ANTLR4=%q", prattPlan.Name, antlrPlan.Name),
				}
			}

			// Compare Parameter Counts
			if len(prattPlan.Params) != len(antlrPlan.Params) {
				return nil, nil, &DualityMismatchError{
					SQL:        sqlStr,
					LabelA:     "Pratt_Tier1",
					LabelB:     "ANTLR4_Tier2",
					Divergence: fmt.Sprintf("Parameter count mismatch: Pratt=%d vs ANTLR4=%d", len(prattPlan.Params), len(antlrPlan.Params)),
				}
			}

			// Compare Parameter Definitions
			for i := range prattPlan.Params {
				pA := prattPlan.Params[i]
				pB := antlrPlan.Params[i]
				if !strings.EqualFold(pA.Name, pB.Name) || pA.Type != pB.Type || pA.Output != pB.Output {
					return nil, nil, &DualityMismatchError{
						SQL:        sqlStr,
						LabelA:     "Pratt_Tier1",
						LabelB:     "ANTLR4_Tier2",
						Divergence: fmt.Sprintf("Parameter %d mismatch:\n    Pratt:  %+v\n    ANTLR4: %+v", i, pA, pB),
					}
				}
			}

			// Both ASTs must successfully compile into executable bytecode
			progPratt, cErrA := proc.CompileProcedure(prattPlan)
			if cErrA != nil {
				return nil, nil, fmt.Errorf("Pratt CompileProcedure failed: %w", cErrA)
			}
			progAntlr, cErrB := proc.CompileProcedure(antlrPlan)
			if cErrB != nil {
				return nil, nil, fmt.Errorf("ANTLR4 CompileProcedure failed: %w", cErrB)
			}

			if len(progPratt.Instructions) == 0 || len(progAntlr.Instructions) == 0 {
				return nil, nil, &DualityMismatchError{
					SQL:        sqlStr,
					LabelA:     "Pratt_Tier1",
					LabelB:     "ANTLR4_Tier2",
					Divergence: fmt.Sprintf("Empty bytecode emitted: Pratt=%d instrs, ANTLR4=%d instrs", len(progPratt.Instructions), len(progAntlr.Instructions)),
				}
			}
		}
	}

	// Execute through underlying executor to verify runtime correctness
	return d.underlying.Execute(sqlStr)
}

func normalizeScriptForAntlr(sql string) string {
	res := sql

	// 1. Babelfish cursor syntax: DECLARE <name> LOCAL [FAST_FORWARD] CURSOR FOR
	// In official T-SQL grammar: DECLARE <name> CURSOR LOCAL [FAST_FORWARD] FOR
	for _, pattern := range []string{
		" LOCAL FAST_FORWARD CURSOR FOR",
		" LOCAL FORWARD_ONLY CURSOR FOR",
		" LOCAL SCROLL CURSOR FOR",
		" LOCAL CURSOR FOR",
		" GLOBAL FAST_FORWARD CURSOR FOR",
		" GLOBAL CURSOR FOR",
	} {
		if idx := strings.Index(res, pattern); idx != -1 {
			prefix := res[:idx]
			suffix := res[idx+len(pattern):]
			words := strings.Fields(pattern)
			// words: ["LOCAL", "FAST_FORWARD", "CURSOR", "FOR"]
			var reordered []string
			reordered = append(reordered, "CURSOR")
			for _, w := range words {
				if w != "CURSOR" && w != "FOR" {
					reordered = append(reordered, w)
				}
			}
			reordered = append(reordered, "FOR")
			res = prefix + " " + strings.Join(reordered, " ") + suffix
		}
	}

	// 2. Babelfish 1-arg THROW: THROW 'msg'
	// In official T-SQL grammar: THROW 50000, 'msg', 1
	var out strings.Builder
	idx := 0
	for {
		tIdx := strings.Index(res[idx:], "THROW")
		if tIdx == -1 {
			out.WriteString(res[idx:])
			break
		}
		absT := idx + tIdx
		out.WriteString(res[idx:absT])
		// Check if preceded by word char
		if absT > 0 && isIdentChar(res[absT-1]) {
			out.WriteString("THROW")
			idx = absT + 5
			continue
		}
		rest := res[absT+5:]
		trimmedRest := strings.TrimLeft(rest, " \t\r\n")
		if strings.HasPrefix(trimmedRest, "'") {
			// Found THROW '...'
			quoteEnd := strings.Index(trimmedRest[1:], "'")
			if quoteEnd != -1 {
				msg := trimmedRest[:quoteEnd+2]
				out.WriteString("THROW 50000, " + msg + ", 1")
				idx = absT + 5 + (len(rest) - len(trimmedRest)) + len(msg)
				continue
			}
		}
		out.WriteString("THROW")
		idx = absT + 5
	}
	res = out.String()

	// 3. Parameter NULL without =: @param TYPE NULL
	// In official T-SQL grammar: @param TYPE = NULL
	asIdx := strings.Index(strings.ToUpper(res), " AS\n")
	if asIdx == -1 {
		asIdx = strings.Index(strings.ToUpper(res), " AS\r\n")
	}
	if asIdx == -1 {
		asIdx = strings.Index(strings.ToUpper(res), " AS ")
	}
	if asIdx != -1 {
		header := res[:asIdx]
		body := res[asIdx:]
		// In header, replace " NULL," with " = NULL," and " NULL\n" with " = NULL\n"
		header = strings.ReplaceAll(header, " NULL,", " = NULL,")
		header = strings.ReplaceAll(header, " NULL\n", " = NULL\n")
		header = strings.ReplaceAll(header, " NULL\r\n", " = NULL\r\n")
		header = strings.ReplaceAll(header, " NULL)", " = NULL)")
		res = header + body
	}

	return res
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '@'
}

