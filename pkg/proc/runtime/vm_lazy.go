package runtime

import (
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

type directEqCheck struct {
	colIdx int
	target storage.Value
}

// extractFastEqConditions prepares single/composite column direct equality checks for lazy tuple decoding.
func (f *VMFrame) extractFastEqConditions(schema *storage.TableSchema, where proc.Expr) []directEqCheck {
	if schema == nil || where == nil {
		return nil
	}
	evalCtx := f.buildRowEvalMap(nil)
	eqMap := extractAllEqConditions(where)
	if len(eqMap) == 0 {
		return nil
	}

	var fast []directEqCheck
	for colIdent, expr := range eqMap {
		if _, cIdx, found := schema.FindColumn(colIdent); found {
			if vAny, err := proc.EvalRowExpr(expr, evalCtx, nil); err == nil {
				fast = append(fast, directEqCheck{
					colIdx: cIdx,
					target: toStorageValue(vAny),
				})
			}
		}
	}
	return fast
}

// canSkipRawTuple reports whether the raw tuple fails fast equality checks.
func canSkipRawTuple(rawCur storage.RawTupleCursor, schema *storage.TableSchema, checks []directEqCheck) bool {
	if rawCur == nil || schema == nil || len(checks) == 0 {
		return false
	}
	rawBytes, ok := rawCur.RawTuple()
	if !ok || len(rawBytes) == 0 {
		return false
	}
	for _, chk := range checks {
		val, err := storage.ExtractColumnFromTuple(schema, rawBytes, chk.colIdx)
		if err != nil || !val.Equal(chk.target) {
			return true // skip because condition failed
		}
	}
	return false
}
