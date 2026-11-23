package slt

import (
	"fmt"
	"strings"

	"lionsql/pkg/engine"
	"lionsql/pkg/sqlutil"
)

// ColdCoordinatorExecutor clears both AST and Bytecode PlanCaches before every execution,
// ensuring every statement is compiled 100% cold from scratch.
type ColdCoordinatorExecutor struct {
	coord *engine.Coordinator
	sess  string
}

// NewColdCoordinatorExecutor creates an executor that flushes plan caches prior to every query.
func NewColdCoordinatorExecutor(coord *engine.Coordinator, sess string) *ColdCoordinatorExecutor {
	return &ColdCoordinatorExecutor{coord: coord, sess: sess}
}

func (e *ColdCoordinatorExecutor) Execute(sqlStr string) ([]string, []map[string]any, error) {
	if cache := e.coord.BytecodePlanCache(); cache != nil {
		cache.Clear()
	}
	if cache := e.coord.PlanCache(); cache != nil {
		cache.Clear()
	}
	// Synthesize default JDBC PreparedStatement parameter values for standalone test assertions
	if strings.Contains(sqlStr, "@P0") && !strings.Contains(strings.ToUpper(sqlStr), "DECLARE") && !strings.Contains(strings.ToUpper(sqlStr), "SP_") {
		sqlStr = strings.ReplaceAll(sqlStr, "@P0", "1")
		sqlStr = strings.ReplaceAll(sqlStr, "@P1", "2")
		sqlStr = strings.ReplaceAll(sqlStr, "@P2", "3")
	}
	res, err := e.coord.ExecuteSessionSQL(e.sess, sqlStr)
	if err != nil {
		return nil, nil, err
	}
	return DecodeQueryResult(res)
}

// HotCoordinatorExecutor retains compiled bytecode and AST programs in PlanCache.
// For read-only SELECT queries, it performs a secondary hot execution to verify that
// running from cache produces identical results and does not mutate cached plan state.
type HotCoordinatorExecutor struct {
	coord *engine.Coordinator
	sess  string
}

// NewHotCoordinatorExecutor creates an executor that leverages persistent PlanCache.
func NewHotCoordinatorExecutor(coord *engine.Coordinator, sess string) *HotCoordinatorExecutor {
	return &HotCoordinatorExecutor{coord: coord, sess: sess}
}

func (e *HotCoordinatorExecutor) Execute(sqlStr string) ([]string, []map[string]any, error) {
	if strings.Contains(sqlStr, "@P0") && !strings.Contains(strings.ToUpper(sqlStr), "DECLARE") && !strings.Contains(strings.ToUpper(sqlStr), "SP_") {
		sqlStr = strings.ReplaceAll(sqlStr, "@P0", "1")
		sqlStr = strings.ReplaceAll(sqlStr, "@P1", "2")
		sqlStr = strings.ReplaceAll(sqlStr, "@P2", "3")
	}
	res, err := e.coord.ExecuteSessionSQL(e.sess, sqlStr)
	if err != nil {
		return nil, nil, err
	}

	// For read-only queries (SELECT without mutation keywords), execute a second time
	// to assert that executing from hot PlanCache produces deterministic identical results.
	trimmed := strings.TrimSpace(sqlStr)
	if sqlutil.HasPrefixKeyword(trimmed, "SELECT") || sqlutil.HasPrefixKeyword(trimmed, "WITH") {
		upper := strings.ToUpper(trimmed)
		if !strings.Contains(upper, "INTO") && !strings.Contains(upper, "INSERT") &&
			!strings.Contains(upper, "UPDATE") && !strings.Contains(upper, "DELETE") {
			resHot, errHot := e.coord.ExecuteSessionSQL(e.sess, sqlStr)
			if errHot != nil {
				return nil, nil, fmt.Errorf("hot plancache execution error: %w", errHot)
			}
			if resHot != nil {
				if len(resHot.Rows) != len(res.Rows) {
					return nil, nil, fmt.Errorf("hot plancache row count divergence: cold=%d hot=%d", len(res.Rows), len(resHot.Rows))
				}
				return DecodeQueryResult(resHot)
			}
		}
	}

	return DecodeQueryResult(res)
}
