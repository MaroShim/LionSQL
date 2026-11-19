package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// tableVarGlobalSeq guarantees strictly monotonic and unique naming across rapid table variable declarations.
var tableVarGlobalSeq atomic.Uint64

func cleanOutputColName(col string) string {
	lower := strings.ToLower(col)
	if strings.HasPrefix(lower, "inserted.") {
		return col[len("inserted."):]
	}
	if strings.HasPrefix(lower, "deleted.") {
		return col[len("deleted."):]
	}
	return col
}

func (f *VMFrame) routeOutputRow(data *proc.DMLData, oldRec, newRec storage.Record, rowValues []storage.Value, tableSchema *storage.TableSchema) error {
	if data == nil || len(data.OutputCols) == 0 {
		return nil
	}

	outputValues := make([]storage.Value, len(data.OutputCols))
	outputCleanCols := make([]string, len(data.OutputCols))

	for idx, outCol := range data.OutputCols {
		lower := strings.ToLower(outCol)
		cleanCol := cleanOutputColName(outCol)
		outputCleanCols[idx] = cleanCol

		var stVal storage.Value
		if strings.HasPrefix(lower, "deleted.") {
			if oldRec != nil {
				if v, ok := oldRec.Get(cleanCol); ok {
					stVal = v
				} else if tableSchema != nil {
					if colSchema, _, found := tableSchema.FindColumn(cleanCol); found {
						if v, ok := oldRec.Get(colSchema.Name); ok {
							stVal = v
						}
					}
				}
			}
		} else if strings.HasPrefix(lower, "inserted.") {
			if newRec != nil {
				if v, ok := newRec.Get(cleanCol); ok {
					stVal = v
				} else if tableSchema != nil {
					if colSchema, _, found := tableSchema.FindColumn(cleanCol); found {
						if v, ok := newRec.Get(colSchema.Name); ok {
							stVal = v
						}
					}
				}
			} else if len(rowValues) > 0 && tableSchema != nil {
				if _, colIdx, ok := tableSchema.FindColumn(cleanCol); ok && colIdx >= 0 && colIdx < len(rowValues) {
					stVal = rowValues[colIdx]
				}
			}
		} else {
			if newRec != nil {
				if v, ok := newRec.Get(cleanCol); ok {
					stVal = v
				} else if tableSchema != nil {
					if colSchema, _, found := tableSchema.FindColumn(cleanCol); found {
						if v, ok := newRec.Get(colSchema.Name); ok {
							stVal = v
						}
					}
				}
			} else if oldRec != nil {
				if v, ok := oldRec.Get(cleanCol); ok {
					stVal = v
				} else if tableSchema != nil {
					if colSchema, _, found := tableSchema.FindColumn(cleanCol); found {
						if v, ok := oldRec.Get(colSchema.Name); ok {
							stVal = v
						}
					}
				}
			} else if len(rowValues) > 0 && tableSchema != nil {
				if _, colIdx, ok := tableSchema.FindColumn(cleanCol); ok && colIdx >= 0 && colIdx < len(rowValues) {
					stVal = rowValues[colIdx]
				}
			}
		}
		outputValues[idx] = stVal
	}

	// Determine destination: Table / Table-Variable vs Scalar-Vars vs Result-Set
	intoTarget := data.IntoTable
	if intoTarget == "" && len(data.IntoVars) == 1 {
		cand := data.IntoVars[0]
		if f.tableVars != nil {
			if _, ok := f.tableVars[strings.ToLower(cand)]; ok {
				intoTarget = cand
			} else if _, ok := f.tableVars["@"+strings.ToLower(cand)]; ok {
				intoTarget = "@" + cand
			}
		}
		if intoTarget == "" {
			if schema, err := f.getTableSchema(cand); err == nil && schema != nil {
				intoTarget = cand
			}
		}
	}

	if intoTarget != "" {
		targetPhys := f.resolveTable(intoTarget)
		targetSchema, err := f.getTableSchema(targetPhys)
		if err == nil && targetSchema != nil {
			targetRec := make(storage.Record)
			if len(data.IntoTableCols) > 0 {
				for i, colName := range data.IntoTableCols {
					if i < len(outputValues) {
						targetRec.Set(colName, outputValues[i])
					}
				}
			} else {
				var targetCols []storage.Column
				for _, c := range targetSchema.Columns {
					if !c.Hidden {
						targetCols = append(targetCols, c)
					}
				}
				for i, col := range targetCols {
					if i < len(outputValues) {
						targetRec.Set(col.Name, outputValues[i])
					}
				}
			}

			// Defaults and identities for target table
			for _, col := range targetSchema.Columns {
				if val, exists := targetRec.Get(col.Name); exists {
					if !val.IsNull && val.Type != col.Type {
						converted := proc.ParseLiteralDefault(val.String(), col.Type)
						targetRec.Set(col.Name, converted)
					}
				} else {
					if col.Default != "" {
						targetRec.Set(col.Name, proc.ParseLiteralDefault(col.Default, col.Type))
					} else if col.Identity {
						seed := col.GetIdentitySeed()
						step := col.GetIdentityIncrement()
						var nextID int64 = seed
						if f.executor != nil {
							nextID = f.executor.AllocIdentityWithStep(targetPhys, 1, seed, step, func() (int64, bool) {
								var maxID int64 = 0
								hasRows := false
								if cur, err := f.tx.Scan(targetPhys); err == nil {
									for cur.Next() {
										if r, err := cur.Record(); err == nil {
											if v, ok := r.Get(col.Name); ok && v.Type == storage.TypeInt && !v.IsNull {
												if !hasRows || v.IVal > maxID {
													maxID = v.IVal
												}
												hasRows = true
											}
										}
									}
									cur.Close()
								}
								return maxID, hasRows
							})
						}
						targetRec.Set(col.Name, storage.IntValue(nextID))
					}
				}
			}

			if err := f.tx.Insert(targetPhys, targetRec); err != nil {
				return fmt.Errorf("OUTPUT INTO %s error: %w", intoTarget, err)
			}
			return nil
		}
	}

	if len(data.IntoVars) > 0 {
		for idx, stVal := range outputValues {
			if idx < len(data.IntoVars) {
				f.setVar(data.IntoVars[idx], stVal)
			}
		}
		return nil
	}

	// Bare OUTPUT clause -> append to query result set
	row := make(map[string]any, len(data.OutputCols))
	vals := make([]any, len(data.OutputCols))
	for idx, outCol := range data.OutputCols {
		cleanCol := outputCleanCols[idx]
		anyVal := valToAny(outputValues[idx])
		row[cleanCol] = anyVal
		if cleanCol != outCol {
			row[outCol] = anyVal
		}
		vals[idx] = anyVal
	}
	f.rows = append(f.rows, row)
	f.values = append(f.values, vals)
	f.columns = outputCleanCols
	f.rowCount = int64(len(f.rows))
	return nil
}

// handleCatch checks the active catchStack on error, routing execution to the catch handler if present.
func (f *VMFrame) handleCatch(err error, pc *int) error {
	if err == nil {
		return nil
	}
	f.lastError = 50000
	f.lastErrorMsg = err.Error()
	if pc != nil {
		f.lastErrorLine = *pc
	}
	if len(f.catchStack) > 0 {
		catchPC := f.catchStack[len(f.catchStack)-1]
		f.catchStack = f.catchStack[:len(f.catchStack)-1]
		*pc = catchPC
		if f.tranCount > 0 || f.tx != nil {
			f.txFailed = true
		}
		return nil
	}
	return err
}

// aux safely retrieves the auxiliary metadata object for the instruction from the program.
func (f *VMFrame) aux(inst *proc.Instruction) any {
	if f == nil || f.prog == nil {
		return nil
	}
	return f.prog.GetAux(inst.Aux)
}

// execFlowOp handles control flow, exception handling, and diagnostic logging opcodes.
// Execute runs the VM instruction loop starting at startPC.
// It executes via a single flattened direct dispatch loop (PERF-VDBE-FLATTEN-1),
// eliminating intermediate function calls and keeping pc in a hardware CPU register.
func (f *VMFrame) Execute(startPC int) (any, error) {
	instructions := f.prog.Instructions
	pc := startPC
	iterCount := 0

vmLoop:
	for pc < len(instructions) {
		iterCount++
		if (iterCount&63 == 0) && f.ctx != nil {
			select {
			case <-f.ctx.Done():
				return nil, f.ctx.Err()
			default:
			}
		}
		inst := instructions[pc]
		pc++

		switch inst.Op {

// --- Flow Control ---
	case proc.OpHalt:
		return nil, nil

	case proc.OpNop:
		continue vmLoop

	case proc.OpReturn:
		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			return valToAny(f.regs[inst.A]), nil
		}
		return nil, nil

	case proc.OpJump:
		pc = int(inst.B)
		continue vmLoop

	case proc.OpJumpIfFalse:
		if !isTruthy(f.regs[inst.A]) {
			pc = int(inst.B)
		}
		continue vmLoop

	case proc.OpJumpIfTrue:
		if isTruthy(f.regs[inst.A]) {
			pc = int(inst.B)
		}
		continue vmLoop

	case proc.OpJumpIfEqInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull {
			pc = int(inst.C)
		} else if va.Type == storage.TypeInt && vb.Type == storage.TypeInt {
			if va.IVal == vb.IVal {
				pc = int(inst.C)
			}
		} else if storageEq(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfNeqInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull {
			pc = int(inst.C)
		} else if va.Type == storage.TypeInt && vb.Type == storage.TypeInt {
			if va.IVal != vb.IVal {
				pc = int(inst.C)
			}
		} else if !storageEq(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfLtInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull {
			pc = int(inst.C)
		} else if va.Type == storage.TypeInt && vb.Type == storage.TypeInt {
			if va.IVal < vb.IVal {
				pc = int(inst.C)
			}
		} else if storageLt(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfLteInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull {
			pc = int(inst.C)
		} else if va.Type == storage.TypeInt && vb.Type == storage.TypeInt {
			if va.IVal <= vb.IVal {
				pc = int(inst.C)
			}
		} else if storageLte(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfGtInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull {
			pc = int(inst.C)
		} else if va.Type == storage.TypeInt && vb.Type == storage.TypeInt {
			if va.IVal > vb.IVal {
				pc = int(inst.C)
			}
		} else if storageGt(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfGteInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull {
			pc = int(inst.C)
		} else if va.Type == storage.TypeInt && vb.Type == storage.TypeInt {
			if va.IVal >= vb.IVal {
				pc = int(inst.C)
			}
		} else if storageGte(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfEq:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull || storageEq(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfNeq:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull || !storageEq(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfLt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull || storageLt(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfLte:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull || storageLte(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfGt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull || storageGt(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfGte:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.IsNull || vb.IsNull || storageGte(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpPushCatch:
		f.catchStack = append(f.catchStack, int(inst.B))
		continue vmLoop

	case proc.OpPopCatch:
		if len(f.catchStack) > 0 {
			f.catchStack = f.catchStack[:len(f.catchStack)-1]
		}
		continue vmLoop

	case proc.OpThrow, proc.OpRaiserror:
		sev := 16
		state := byte(1)
		errNum := 50000

		if inst.Op == proc.OpThrow {
			if inst.A > 0 {
				errNum = int(inst.A)
			}
			if inst.B > 0 {
				state = byte(inst.B)
			}
		} else { // OpRaiserror
			if inst.A > 0 {
				sev = int(inst.A)
			}
			if inst.B > 0 {
				state = byte(inst.B)
			}
			if inst.C > 0 {
				errNum = int(inst.C)
			}
		}

		msg := fmt.Sprintf("%v", f.aux(&inst))
		if rData, ok := f.aux(&inst).(*proc.RaiserrorData); ok && rData != nil {
			msg = rData.Message
			if len(rData.ArgRegs) > 0 && strings.Contains(msg, "%") {
				var fmtArgs []any
				for _, regIdx := range rData.ArgRegs {
					if regIdx >= 0 && int(regIdx) < len(f.regs) {
						fmtArgs = append(fmtArgs, valToString(f.regs[regIdx]))
					}
				}
				goFmt := strings.ReplaceAll(msg, "%s", "%v")
				goFmt = strings.ReplaceAll(goFmt, "%d", "%v")
				goFmt = strings.ReplaceAll(goFmt, "%i", "%v")
				goFmt = strings.ReplaceAll(goFmt, "%u", "%v")
				msg = fmt.Sprintf(goFmt, fmtArgs...)
			}
		}
		if inst.Op == proc.OpRaiserror && sev <= 10 {
			f.logs = append(f.logs, msg)
			continue vmLoop
		}

		f.lastError = errNum
		f.lastErrorMsg = msg
		if true {
			f.lastErrorLine = pc
		}
		if len(f.catchStack) > 0 {
			catchPC := f.catchStack[len(f.catchStack)-1]
			f.catchStack = f.catchStack[:len(f.catchStack)-1]
			pc = catchPC
			if f.tranCount > 0 || f.tx != nil {
				f.txFailed = true
			}
			continue vmLoop
		}
		return nil, &proc.ProcError{
			Number:   int32(errNum),
			Severity: byte(sev),
			State:    state,
			Message:  msg,
		}

	case proc.OpPrint:
		v := f.regs[inst.A]
		f.logs = append(f.logs, valToString(v))
		continue vmLoop

	case proc.OpWaitfor:
		v := f.regs[inst.A]
		isDelay := inst.B == 1
		dur, err := parseTsqlDelay(v, isDelay)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		if dur > 0 {
			ctx := f.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			timer := time.NewTimer(dur)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		continue vmLoop


// --- Compute, Variables, Registers ---
	case proc.OpLoadConst:
		if int(inst.B) < len(f.prog.Constants) {
			f.regs[inst.A] = f.prog.Constants[inst.B]
		}

	case proc.OpLoadVar:
		slot := inst.B
		varName, _ := f.aux(&inst).(string)
		if strings.HasPrefix(varName, "@@") {
			if v, found := f.getVar(varName); found {
				f.regs[inst.A] = v
				continue vmLoop
			}
		}
		if slot >= 0 && int(slot) < len(f.slots) {
			f.regs[inst.A] = f.slots[slot]
		} else {
			v, found := f.getVar(varName)
			if !found {
				v = storage.StringValue(varName)
			}
			f.regs[inst.A] = v
		}

	case proc.OpStoreVar:
		val := f.regs[inst.A]
		slot := inst.B
		if slot >= 0 && int(slot) < len(f.slots) {
			f.slots[slot] = val
			f.evalCtxDirty = true
		} else {
			varName, _ := f.aux(&inst).(string)
			if varName != "" {
				f.setVar(varName, val)
			}
		}

	case proc.OpCopy:
		f.regs[inst.A] = f.regs[inst.B]

	case proc.OpAdd, proc.OpAddInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			if (vc.IVal > 0 && vb.IVal > math.MaxInt64-vc.IVal) || (vc.IVal < 0 && vb.IVal < math.MinInt64-vc.IVal) {
				if catchErr := f.handleCatch(fmt.Errorf("arithmetic overflow error: bigint out of range"), &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			f.regs[inst.A] = storage.IntValue(vb.IVal + vc.IVal)
		} else {
			f.regs[inst.A] = storageAdd(vb, vc)
		}

	case proc.OpSub, proc.OpSubInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			if (vc.IVal < 0 && vb.IVal > math.MaxInt64+vc.IVal) || (vc.IVal > 0 && vb.IVal < math.MinInt64+vc.IVal) {
				if catchErr := f.handleCatch(fmt.Errorf("arithmetic overflow error: bigint out of range"), &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			f.regs[inst.A] = storage.IntValue(vb.IVal - vc.IVal)
		} else {
			f.regs[inst.A] = storageSub(vb, vc)
		}

	case proc.OpMul, proc.OpMulInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			if vb.IVal != 0 && vc.IVal != 0 {
				if (vb.IVal == -1 && vc.IVal == math.MinInt64) || (vc.IVal == -1 && vb.IVal == math.MinInt64) || (vb.IVal*vc.IVal)/vc.IVal != vb.IVal {
					if catchErr := f.handleCatch(fmt.Errorf("arithmetic overflow error: bigint out of range"), &pc); catchErr != nil {
						return nil, catchErr
					}
					continue vmLoop
				}
			}
			f.regs[inst.A] = storage.IntValue(vb.IVal * vc.IVal)
		} else {
			f.regs[inst.A] = storageMul(vb, vc)
		}

	case proc.OpDiv:
		res, err := storageDiv(f.regs[inst.B], f.regs[inst.C])
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.regs[inst.A] = res

	case proc.OpIntDiv:
		res, err := storageIntDiv(f.regs[inst.B], f.regs[inst.C])
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.regs[inst.A] = res

	case proc.OpMod:
		res, err := storageMod(f.regs[inst.B], f.regs[inst.C])
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.regs[inst.A] = res

	case proc.OpNeg:
		f.regs[inst.A] = storageNeg(f.regs[inst.B])

	case proc.OpNot:
		if f.regs[inst.B].IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else {
			f.regs[inst.A] = storage.BoolValue(!isTruthy(f.regs[inst.B]))
		}

	case proc.OpAnd:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if (!vb.IsNull && !isTruthy(vb)) || (!vc.IsNull && !isTruthy(vc)) {
			f.regs[inst.A] = storage.BoolValue(false)
		} else if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else {
			f.regs[inst.A] = storage.BoolValue(true)
		}

	case proc.OpOr:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if (!vb.IsNull && isTruthy(vb)) || (!vc.IsNull && isTruthy(vc)) {
			f.regs[inst.A] = storage.BoolValue(true)
		} else if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else {
			f.regs[inst.A] = storage.BoolValue(false)
		}

	case proc.OpBitOr:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal | vc.IVal)
		} else {
			f.regs[inst.A] = storage.NullValue(storage.TypeInt)
		}

	case proc.OpBitAnd:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal & vc.IVal)
		} else {
			f.regs[inst.A] = storage.NullValue(storage.TypeInt)
		}

	case proc.OpBitXor:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal ^ vc.IVal)
		} else {
			f.regs[inst.A] = storage.NullValue(storage.TypeInt)
		}

	case proc.OpBitNot:
		vb := f.regs[inst.B]
		if vb.Type == storage.TypeInt && !vb.IsNull {
			f.regs[inst.A] = storage.IntValue(^vb.IVal)
		} else {
			f.regs[inst.A] = storage.NullValue(storage.TypeInt)
		}

	case proc.OpEq, proc.OpEqInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt {
			f.regs[inst.A] = storage.BoolValue(vb.IVal == vc.IVal)
		} else if inst.Flags&1 != 0 {
			f.regs[inst.A] = storage.BoolValue(storageEqCS(vb, vc, true))
		} else {
			f.regs[inst.A] = storage.BoolValue(storageEq(vb, vc))
		}

	case proc.OpNeq, proc.OpNeqInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt {
			f.regs[inst.A] = storage.BoolValue(vb.IVal != vc.IVal)
		} else if inst.Flags&1 != 0 {
			f.regs[inst.A] = storage.BoolValue(!storageEqCS(vb, vc, true))
		} else {
			f.regs[inst.A] = storage.BoolValue(!storageEq(vb, vc))
		}

	case proc.OpLt, proc.OpLtInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt {
			f.regs[inst.A] = storage.BoolValue(vb.IVal < vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageLt(vb, vc))
		}

	case proc.OpLte, proc.OpLteInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt {
			f.regs[inst.A] = storage.BoolValue(vb.IVal <= vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageLte(vb, vc))
		}

	case proc.OpGt, proc.OpGtInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt {
			f.regs[inst.A] = storage.BoolValue(vb.IVal > vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageGt(vb, vc))
		}

	case proc.OpGte, proc.OpGteInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt {
			f.regs[inst.A] = storage.BoolValue(vb.IVal >= vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageGte(vb, vc))
		}

	case proc.OpLike:
		if f.regs[inst.B].IsNull || f.regs[inst.C].IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else {
			str := valToString(f.regs[inst.B])
			pat := valToString(f.regs[inst.C])
			esc := ""
			if auxData := f.aux(&inst); auxData != nil {
				hasEsc := false
				if s, ok := auxData.(string); ok {
					esc = s
					hasEsc = true
				} else if r, ok := auxData.(int32); ok && int(r) >= 0 && int(r) < len(f.regs) {
					if !f.regs[r].IsNull {
						esc = valToString(f.regs[r])
						hasEsc = true
					}
				}
				if hasEsc && len([]rune(esc)) != 1 {
					return nil, fmt.Errorf("The ESCAPE clause must be a single character")
				}
			}
			isCS := inst.Flags&2 != 0
			matched := sqlutil.MatchLikeEscape(pat, str, esc, !isCS)
			if inst.Flags&1 != 0 {
				matched = !matched
			}
			f.regs[inst.A] = storage.BoolValue(matched)
		}

	case proc.OpBetween:
		v := f.regs[inst.A]
		low := f.regs[inst.B]
		high := f.regs[inst.C]
		if !v.IsNull && !low.IsNull && storageLt(v, low) {
			inRange := false
			if inst.Flags&1 != 0 {
				inRange = true
			}
			f.regs[inst.A] = storage.BoolValue(inRange)
		} else if !v.IsNull && !high.IsNull && storageGt(v, high) {
			inRange := false
			if inst.Flags&1 != 0 {
				inRange = true
			}
			f.regs[inst.A] = storage.BoolValue(inRange)
		} else if v.IsNull || low.IsNull || high.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else {
			inRange := storageGte(v, low) && storageLte(v, high)
			if inst.Flags&1 != 0 {
				inRange = !inRange
			}
			f.regs[inst.A] = storage.BoolValue(inRange)
		}

	case proc.OpIn:
		val := f.regs[inst.B]
		callData, ok := f.aux(&inst).(*proc.CallData)
		if !ok || callData == nil || len(callData.ArgRegs) == 0 {
			f.regs[inst.A] = storage.BoolValue(false)
			break
		}
		if val.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
			break
		}
		found := false
		hasNull := false
		for _, regIdx := range callData.ArgRegs {
			if regIdx >= 0 && int(regIdx) < len(f.regs) {
				regVal := f.regs[regIdx]
				if regVal.IsNull {
					hasNull = true
					continue
				}
				if storageEq(val, regVal) {
					found = true
					break
				}
			}
		}
		if found {
			f.regs[inst.A] = storage.BoolValue(true)
		} else if hasNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else {
			f.regs[inst.A] = storage.BoolValue(false)
		}

	case proc.OpSubqueryHashSetIn:
		val := f.regs[inst.B]
		callData, ok := f.aux(&inst).(*proc.CallData)
		if !ok || callData == nil {
			f.regs[inst.A] = storage.BoolValue(false)
			break
		}
		if val.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
			break
		}
		set, found := proc.LookupSubqueryHashSet(callData.Name)
		if !found {
			f.regs[inst.A] = storage.BoolValue(false)
			break
		}
		normKey, isNull := proc.NormalizeSubqueryKey(val)
		res := set.Evaluate(normKey, isNull, false)
		f.regs[inst.A] = storage.BoolValue(res)

	case proc.OpIsNull:
		f.regs[inst.A] = storage.BoolValue(f.regs[inst.B].IsNull)

	case proc.OpIsNotNull:
		f.regs[inst.A] = storage.BoolValue(!f.regs[inst.B].IsNull)

	case proc.OpCast:
		srcVal := f.regs[inst.B]
		castData, ok := f.aux(&inst).(*proc.CastData)
		if !ok || castData == nil {
			return nil, fmt.Errorf("invalid OpCast metadata")
		}
		resVal, err := proc.EvalCast(srcVal, castData.TargetType, castData.Precision, castData.Scale, castData.Style, castData.Try, castData.IsFixed)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.regs[inst.A] = resVal


// --- Transactions ---
	case proc.OpTxBegin:
		f.tranCount++

	case proc.OpTxCommit:
		if f.txFailed {
			return nil, errors.New("[42000] Uncommittable transaction is detected at the end of the batch. The transaction is rolled back.")
		}
		if f.tranCount > 1 {
			// Nested transaction COMMIT: decrement tranCount only, do not commit physically!
			f.tranCount--
		} else if f.tranCount == 1 {
			// Outermost transaction COMMIT: commit physically to storage
			f.tranCount = 0
			if f.tx != nil {
				if err := f.tx.Commit(); err != nil && !errors.Is(err, storage.ErrTxClosed) {
					return nil, err
				}
			}
			if f.session != nil {
				newTx, err := f.session.BeginTx(storage.TxOptions{})
				if err != nil {
					return nil, err
				}
				f.tx = newTx
			}
		}

	case proc.OpTxRollback:
		// T-SQL Standard: ROLLBACK without savepoint unconditionally resets tranCount to 0 and rolls back physically
		f.tranCount = 0
		f.txFailed = false
		if f.tx != nil {
			if err := f.tx.Rollback(); err != nil && !errors.Is(err, storage.ErrTxClosed) {
				return nil, err
			}
		}
		if f.session != nil {
			newTx, err := f.session.BeginTx(storage.TxOptions{})
			if err != nil {
				return nil, err
			}
			f.tx = newTx
		}

	case proc.OpTxSavepoint:
		spName, _ := f.aux(&inst).(string)
		if err := f.tx.Savepoint(spName); err != nil {
			return nil, err
		}

	case proc.OpTxRollbackTo:
		spName, _ := f.aux(&inst).(string)
		if err := f.tx.RollbackTo(spName); err != nil {
			return nil, err
		}


// --- Storage DML ---
	case proc.OpDeclareTable:
		data, ok := f.aux(&inst).(*proc.TableVarData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpDeclareTable metadata")
		}
		varName := strings.ToLower(data.Name)
		if !strings.HasPrefix(varName, "@") {
			varName = "@" + varName
		}

		// 1. Drop existing table variable first before allocating new physical table (Drop-First)
		if f.tableVars != nil {
			if oldPhys, exists := f.tableVars[varName]; exists {
				if f.executor != nil {
					if tm := f.executor.getTableManager(); tm != nil {
						_ = tm.DropTable(oldPhys)
					}
				}
				delete(f.tableVars, varName)
			}
		} else {
			f.tableVars = make(map[string]string)
		}

		// 2. Monotonic atomic sequence guarantee to prevent any timestamp collision
		seq := tableVarGlobalSeq.Add(1)
		physTable := fmt.Sprintf("__tv_%p_%d_%d_%s", f, time.Now().UnixNano(), seq, strings.TrimPrefix(varName, "@"))

		cols := data.Columns
		if len(cols) == 0 && data.RawColDefs != "" {
			p := proc.NewParser(proc.NewLexer(data.RawColDefs))
			if parsedCols, err := p.ParseColumnDefs(); err == nil {
				cols = parsedCols
			}
		}

		schema := &storage.TableSchema{
			Name:    physTable,
			Columns: make([]storage.Column, len(cols)),
		}
		for i, c := range cols {
			schema.Columns[i] = storage.Column{
				Name:       c.Name,
				Type:       c.Type,
				PrimaryKey: c.PrimaryKey,
				Nullable:   c.Nullable,
				Precision:  c.Precision,
				Scale:      c.Scale,
			}
		}
		if !schema.HasPrimaryKey() {
			schema.EnsurePrimaryKey("_row_id")
		}

		if f.executor != nil {
			if tm := f.executor.getTableManager(); tm != nil {
				if err := tm.CreateTable(schema); err != nil {
					return nil, fmt.Errorf("failed to create table variable %s: %w", data.Name, err)
				}
			}
		}

		f.tableVars[varName] = physTable

	case proc.OpCreateTable:
		stmt, ok := f.aux(&inst).(*proc.CreateTableStmt)
		if !ok || stmt == nil {
			return nil, fmt.Errorf("invalid OpCreateTable metadata")
		}
		rawName := strings.TrimSpace(stmt.Name)
		lowerName := strings.ToLower(rawName)
		isLocalTemp := strings.HasPrefix(lowerName, "#") && !strings.HasPrefix(lowerName, "##")
		physTable := rawName
		if isLocalTemp {
			if f.tableVars != nil {
				if _, exists := f.tableVars[lowerName]; exists {
					if stmt.IfNotExists {
						continue vmLoop
					}
					if catchErr := f.handleCatch(fmt.Errorf("there is already an object named %q in the database", rawName), &pc); catchErr != nil {
						return nil, catchErr
					}
					continue vmLoop
				}
			} else {
				f.tableVars = make(map[string]string)
			}
			seq := tableVarGlobalSeq.Add(1)
			physTable = fmt.Sprintf("__temp_%p_%d_%d_%s", f, time.Now().UnixNano(), seq, strings.TrimPrefix(lowerName, "#"))
		}

		schema := &storage.TableSchema{
			Name:    physTable,
			Columns: make([]storage.Column, len(stmt.Columns)),
		}
		for i, c := range stmt.Columns {
			schema.Columns[i] = storage.Column{
				Name:       c.Name,
				Type:       c.Type,
				RawType:    c.RawType,
				IsFixed:    c.IsFixed,
				PrimaryKey: c.PrimaryKey,
				Nullable:   c.Nullable,
				Precision:  c.Precision,
				Scale:      c.Scale,
			}
		}
		if !schema.HasPrimaryKey() {
			schema.EnsurePrimaryKey("_row_id")
		}

		if f.executor != nil {
			if tm := f.executor.getTableManager(); tm != nil {
				if err := tm.CreateTable(schema); err != nil {
					if stmt.IfNotExists && errors.Is(err, storage.ErrTableExists) {
						continue vmLoop
					}
					if catchErr := f.handleCatch(err, &pc); catchErr != nil {
						return nil, catchErr
					}
					continue vmLoop
				}
			}
		}
		if isLocalTemp {
			f.tableVars[lowerName] = physTable
		}

	case proc.OpDropTable:
		stmt, ok := f.aux(&inst).(*proc.DropTableStmt)
		if !ok || stmt == nil {
			return nil, fmt.Errorf("invalid OpDropTable metadata")
		}
		rawName := strings.TrimSpace(stmt.Name)
		lowerName := strings.ToLower(rawName)
		physTable := f.resolveTable(rawName)
		if f.tableVars != nil {
			if mapped, exists := f.tableVars[lowerName]; exists {
				physTable = mapped
				delete(f.tableVars, lowerName)
			}
		}
		if f.executor != nil {
			if tm := f.executor.getTableManager(); tm != nil {
				if err := tm.DropTable(physTable); err != nil {
					if stmt.IfExists && errors.Is(err, storage.ErrTableNotFound) {
						continue vmLoop
					}
					if catchErr := f.handleCatch(err, &pc); catchErr != nil {
						return nil, catchErr
					}
					continue vmLoop
				}
			}
		}

	case proc.OpCreateIndex:
		stmt, ok := f.aux(&inst).(*proc.CreateIndexStmt)
		if !ok || stmt == nil {
			return nil, fmt.Errorf("invalid OpCreateIndex metadata")
		}
		physTable := f.resolveTable(stmt.Table)
		if f.executor != nil {
			if tm := f.executor.getTableManager(); tm != nil {
				if ic, ok := tm.(interface {
					CreateIndex(index *storage.IndexSchema) error
				}); ok {
					idxDef := &storage.IndexSchema{
						Name:        stmt.Name,
						TableName:   physTable,
						Columns:     stmt.Columns,
						IncludeCols: stmt.IncludeCols,
						Unique:      stmt.Unique,
						Expression:  stmt.Expression,
						ExprColumn:  stmt.ExprColumn,
						ExprPath:    stmt.ExprPath,
					}
					if err := ic.CreateIndex(idxDef); err != nil && !stmt.IfNotExists {
						if catchErr := f.handleCatch(err, &pc); catchErr != nil {
							return nil, catchErr
						}
						continue vmLoop
					}
				}
			}
		}

	case proc.OpDropIndex:
		stmt, ok := f.aux(&inst).(*proc.DropIndexStmt)
		if !ok || stmt == nil {
			return nil, fmt.Errorf("invalid OpDropIndex metadata")
		}
		physTable := f.resolveTable(stmt.Table)
		if f.executor != nil && physTable != "" {
			if tm := f.executor.getTableManager(); tm != nil {
				if id, ok := tm.(interface {
					DropIndex(tableName, indexName string) error
				}); ok {
					if err := id.DropIndex(physTable, stmt.Name); err != nil && !stmt.IfExists {
						if catchErr := f.handleCatch(err, &pc); catchErr != nil {
							return nil, catchErr
						}
						continue vmLoop
					}
				}
			}
		}

	case proc.OpInsert:
		data, ok := f.aux(&inst).(*proc.DMLData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpInsert metadata")
		}
		targetTable := f.resolveTable(data.Table)
		if data.Exec != nil {
			cnt, err := f.execInsertExec(data)
			if err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			f.rowCount = cnt
			continue vmLoop
		}
		if data.Select != nil {
			cnt, err := f.execInsertSelect(data)
			if err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			f.rowCount = cnt
			continue vmLoop
		}
		hasTriggers := f.executor != nil && f.executor.HasTriggers(targetTable, "INSERT")

		cols := data.Columns
		var tableSchema *storage.TableSchema
		var identityCol *storage.Column
		if tg := f.executor.getTableGetter(); tg != nil {
			if schema, err := tg(targetTable); err == nil && schema != nil {
				tableSchema = schema
				for idx := range schema.Columns {
					if schema.Columns[idx].Identity {
						identityCol = &schema.Columns[idx]
						break
					}
				}

				identityInsertAllowed := false
				if val, ok := f.vars["identity_insert_table"]; ok && val.Type == storage.TypeVarChar {
					if strings.EqualFold(val.SVal, targetTable) || strings.EqualFold(val.SVal, data.Table) {
						identityInsertAllowed = true
					}
				}
				if val, ok := f.vars["identity_insert_all"]; ok && val.Type == storage.TypeBool && val.BVal {
					identityInsertAllowed = true
				}

				if len(cols) == 0 {
					for _, c := range schema.Columns {
						if c.Identity && !identityInsertAllowed {
							continue
						}
						cols = append(cols, c.Name)
					}
				} else if identityCol != nil && !identityInsertAllowed {
					for _, c := range cols {
						if strings.EqualFold(c, identityCol.Name) {
							return nil, fmt.Errorf("Cannot insert explicit value for identity column in table %q when IDENTITY_INSERT is set to OFF", data.Table)
						}
					}
				}
			}
		}

		// Fast-path Direct Row Insert: bypass Record map allocations when schema matches and no triggers exist
		canDirectInsert := !isTransientTable(targetTable) && tableSchema != nil && !tableSchema.HasCheckConstraints() && !hasTriggers && len(cols) == len(tableSchema.Columns) && len(data.ValExprs) == len(tableSchema.Columns) && identityCol == nil && (len(data.OutputCols) == 0 || len(data.IntoVars) > 0 || data.IntoTable != "")
		if canDirectInsert {
			for i, col := range cols {
				if !strings.EqualFold(col, tableSchema.Columns[i].Name) {
					canDirectInsert = false
					break
				}
			}
		}
		if canDirectInsert {
			if cap(f.rowScratch) < len(tableSchema.Columns) {
				f.rowScratch = make([]storage.Value, len(tableSchema.Columns))
			} else {
				f.rowScratch = f.rowScratch[:len(tableSchema.Columns)]
			}
			allFast := true
			var notNullErr error
			for i := range tableSchema.Columns {
				stVal, ok := f.evalRowExprFast(data.ValExprs[i])
				if !ok {
					allFast = false
					break
				}
				colSchema := tableSchema.Columns[i]
				if !colSchema.Nullable && stVal.IsNull {
					notNullErr = fmt.Errorf("cannot insert the value NULL into column '%s', table '%s'; column does not allow nulls (%w)", colSchema.Name, targetTable, storage.ErrNotNullViolation)
					break
				}
				if !stVal.IsNull && colSchema.Type != stVal.Type {
					if colSchema.Type == storage.TypeDecimal && stVal.Type == storage.TypeFloat && colSchema.Scale >= 0 {
						stVal = proc.ParseLiteralDefault(strconv.FormatFloat(stVal.FVal, 'f', colSchema.Scale, 64), colSchema.Type)
					} else {
						stVal = proc.ParseLiteralDefault(stVal.String(), colSchema.Type)
					}
				}
				f.rowScratch[i] = stVal
				if (strings.EqualFold(colSchema.Name, "id") || strings.HasSuffix(strings.ToLower(colSchema.Name), "id")) && stVal.Type == storage.TypeInt && !stVal.IsNull {
					f.lastIdentity = stVal.IVal
				}
			}
			if notNullErr != nil {
				if catchErr := f.handleCatch(notNullErr, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			if allFast {
				if err := f.tx.InsertRow(targetTable, storage.Row{Values: f.rowScratch}); err != nil {
					if catchErr := f.handleCatch(fmt.Errorf("insert error on %s: %w", targetTable, err), &pc); catchErr != nil {
						return nil, catchErr
					}
					continue vmLoop
				}
				f.rowCount++
				if len(data.OutputCols) > 0 {
					if err := f.routeOutputRow(data, nil, nil, f.rowScratch, tableSchema); err != nil {
						if catchErr := f.handleCatch(err, &pc); catchErr != nil {
							return nil, catchErr
						}
						continue vmLoop
					}
				}
				continue vmLoop
			}
		}

		rec := make(storage.Record)
		var rowMap map[string]any
		if hasTriggers {
			rowMap = make(map[string]any, len(data.Columns))
		}
		var evalCtx map[string]any

		for i, col := range cols {
			if i < len(data.ValExprs) {
				valExpr := data.ValExprs[i]
				var stVal storage.Value
				isDefaultKw := proc.IsDefaultKeyword(valExpr)
				if isDefaultKw {
					if tableSchema != nil {
						if colSchema, _, ok := tableSchema.FindColumn(col); ok && colSchema.Default != "" {
							stVal = proc.ParseLiteralDefault(colSchema.Default, colSchema.Type)
						} else if ok {
							stVal = storage.NullValue(colSchema.Type)
						} else {
							stVal = storage.Value{IsNull: true}
						}
					} else {
						stVal = storage.Value{IsNull: true}
					}
				} else {
					var ok bool
					stVal, ok = f.evalRowExprFast(valExpr)
					if !ok {
						if evalCtx == nil {
							evalCtx = f.buildRowEvalMap(nil)
						}
						vAny, _ := proc.EvalRowExpr(valExpr, evalCtx, nil)
						stVal = toStorageValue(vAny)
					}
				}
				if tableSchema != nil {
					if colSchema, _, ok := tableSchema.FindColumn(col); ok {
						col = colSchema.Name
						if !isDefaultKw && colSchema.Type != stVal.Type {
							if colSchema.Type == storage.TypeDecimal && stVal.Type == storage.TypeFloat && colSchema.Scale >= 0 {
								stVal = proc.ParseLiteralDefault(strconv.FormatFloat(stVal.FVal, 'f', colSchema.Scale, 64), colSchema.Type)
							} else {
								stVal = proc.ParseLiteralDefault(stVal.String(), colSchema.Type)
							}
						}
					}
				}
				rec.Set(col, stVal)
				if hasTriggers {
					rowMap[col] = valToAny(stVal)
				}
				if (strings.EqualFold(col, "id") || strings.HasSuffix(strings.ToLower(col), "id")) && stVal.Type == storage.TypeInt && !stVal.IsNull {
					f.lastIdentity = stVal.IVal
				}
			}
		}

		if tableSchema != nil {
			for _, col := range tableSchema.Columns {
				if _, exists := rec[col.Name]; !exists && col.Default != "" {
					rec[col.Name] = proc.ParseLiteralDefault(col.Default, col.Type)
				}
			}
		}

		if identityCol != nil {
			if val, exists := rec.Get(identityCol.Name); exists && !val.IsNull && val.Type == storage.TypeInt {
				tableName := tableSchema.Name
				if f.executor != nil {
					if lastID, ok := f.executor.GetLastIdentity(tableName); !ok || val.IVal > lastID {
						f.executor.SetLastIdentity(tableName, val.IVal)
					}
				}
				f.lastIdentity = val.IVal
				if f.vars != nil {
					f.vars["identity"] = storage.IntValue(val.IVal)
					f.vars["scope_identity"] = storage.IntValue(val.IVal)
				}
			} else if !exists || val.IsNull {
				tableName := tableSchema.Name
				seed := identityCol.GetIdentitySeed()
				step := identityCol.GetIdentityIncrement()
				var nextID int64 = seed
				if f.executor != nil {
					nextID = f.executor.AllocIdentityWithStep(tableName, 1, seed, step, func() (int64, bool) {
						var maxID int64 = 0
						hasRows := false
						if cur, err := f.tx.Scan(data.Table); err == nil {
							for cur.Next() {
								if r, err := cur.Record(); err == nil {
									if v, ok := r.Get(identityCol.Name); ok && v.Type == storage.TypeInt && !v.IsNull {
										if !hasRows || v.IVal > maxID {
											maxID = v.IVal
										}
										hasRows = true
									}
								}
							}
							cur.Close()
						}
						return maxID, hasRows
					})
				}
				rec.Set(identityCol.Name, storage.IntValue(nextID))
				if hasTriggers {
					rowMap[identityCol.Name] = nextID
				}
				f.lastIdentity = nextID
				if f.vars != nil {
					f.vars["identity"] = storage.IntValue(nextID)
					f.vars["scope_identity"] = storage.IntValue(nextID)
				}
			}
		}

		if tableSchema != nil && tableSchema.HasCheckConstraints() {
			if err := proc.ValidateRecordChecks(tableSchema, rec); err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
		}

		if err := f.tx.Insert(targetTable, rec); err != nil {
			if errors.Is(err, storage.ErrKeyExists) || errors.Is(err, storage.ErrUniqueViolation) {
				if catchErr := f.handleCatch(fmt.Errorf("duplicate key value violates unique constraint on %q: %w", targetTable, err), &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			if catchErr := f.handleCatch(fmt.Errorf("insert error on %s: %w", targetTable, err), &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.rowCount++
		if len(data.OutputCols) > 0 {
			if err := f.routeOutputRow(data, nil, rec, nil, tableSchema); err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
		}
		if hasTriggers {
			if err := f.fireTriggers(targetTable, "INSERT", []map[string]any{rowMap}, nil); err != nil {
				return nil, err
			}
		}

	case proc.OpUpdate:
		data, ok := f.aux(&inst).(*proc.DMLData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpUpdate metadata")
		}
		targetTable := f.resolveTable(data.Table)
		if data.WhereCol != "" && !f.isPKCol(targetTable, data.WhereCol) {
			data.WhereExpr = &proc.BinaryExpr{
				Left:  &proc.ColumnRefExpr{Name: data.WhereCol},
				Op:    proc.TokenEq,
				Right: data.WhereVal,
			}
			cnt, err := f.execUpdateWhere(data)
			if err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			f.rowCount = cnt
			continue vmLoop
		}

		var pkVal storage.Value
		if v, ok := f.evalRowExprFast(data.WhereVal); ok {
			pkVal = v
		} else {
			evalCtx := f.buildRowEvalMap(nil)
			pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
			pkVal = toStorageValue(pkAny)
		}

		var tableSchema *storage.TableSchema
		if tg := f.executor.getTableGetter(); tg != nil {
			if schema, err := tg(targetTable); err == nil && schema != nil {
				tableSchema = schema
			}
		}

		if tableSchema != nil {
			for _, a := range data.Assignments {
				if _, _, ok := tableSchema.FindColumn(a.Column); !ok {
					if catchErr := f.handleCatch(fmt.Errorf("column %q does not exist in table %q", a.Column, targetTable), &pc); catchErr != nil {
						return nil, catchErr
					}
					continue vmLoop
				}
			}
		}

		hasTriggers := f.executor != nil && f.executor.HasTriggers(targetTable, "UPDATE")

		// Fast-path Direct Row Update: bypass Record map allocations when no triggers exist
		if !isTransientTable(targetTable) && tableSchema != nil && !tableSchema.HasCheckConstraints() && !hasTriggers {
			oldRow, err := f.tx.GetRow(targetTable, pkVal)
			if err != nil {
				if errors.Is(err, storage.ErrKeyNotFound) {
					f.rowCount = 0
					continue vmLoop
				}
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}

			if len(oldRow.Values) == len(tableSchema.Columns) {
				if cap(f.rowScratch) < len(oldRow.Values) {
					f.rowScratch = make([]storage.Value, len(oldRow.Values))
				} else {
					f.rowScratch = f.rowScratch[:len(oldRow.Values)]
				}
				copy(f.rowScratch, oldRow.Values)
				updatedRow := storage.Row{Values: f.rowScratch}

				var rowCtx map[string]any
				for _, a := range data.Assignments {
					stVal, ok := f.evalUpdateExprFast(a.Value, tableSchema, oldRow)
					if !ok {
						if rowCtx == nil {
							f.syncEvalCtx()
							rowCtx = make(map[string]any, len(f.evalCtx)+len(tableSchema.Columns)*2)
							for k, v := range f.evalCtx {
								rowCtx[k] = v
							}
							for i, col := range tableSchema.Columns {
								valAny := valToAny(oldRow.Values[i])
								rowCtx[col.Name] = valAny
								rowCtx[strings.ToLower(col.Name)] = valAny
							}
						}
						valAny, err := proc.EvalUpdateRowExpr(a.Value, rowCtx, nil)
						if err != nil {
							if catchErr := f.handleCatch(err, &pc); catchErr != nil {
								return nil, catchErr
							}
							continue vmLoop
						}
						stVal = toStorageValue(valAny)
					}
					if col, colIdx, ok := tableSchema.FindColumn(a.Column); ok && colIdx >= 0 && colIdx < len(updatedRow.Values) {
						if col.Type == storage.TypeFloat && stVal.Type == storage.TypeInt {
							stVal = storage.FloatValue(float64(stVal.IVal))
						} else if col.Type == storage.TypeInt && stVal.Type == storage.TypeFloat && stVal.FVal == float64(int64(stVal.FVal)) {
							stVal = storage.IntValue(int64(stVal.FVal))
						} else if col.Type == storage.TypeDecimal && (stVal.Type == storage.TypeInt || stVal.Type == storage.TypeFloat || stVal.Type == storage.TypeVarChar) {
							stVal = storage.DecimalValue(stVal.String())
							if col.Scale > 0 {
								stVal = storage.DecimalValueFromDecimal(stVal.DVal.Rescale(int32(col.Scale)))
							}
						} else if col.Type == storage.TypeBinary && stVal.Type == storage.TypeVarChar {
							stVal = storage.BinaryValueFromString(stVal.SVal)
						}
						updatedRow.Values[colIdx] = stVal
					}
				}

				if err := f.tx.UpdateRow(targetTable, pkVal, updatedRow); err != nil {
					if errors.Is(err, storage.ErrKeyNotFound) {
						f.rowCount = 0
						continue vmLoop
					}
					if catchErr := f.handleCatch(err, &pc); catchErr != nil {
						return nil, catchErr
					}
					continue vmLoop
				}
				f.rowCount = 1
				if len(data.OutputCols) > 0 {
					var oldRec storage.Record
					if tableSchema != nil {
						oldRec = tableSchema.RowToRecord(oldRow)
					}
					var newRec storage.Record
					if tableSchema != nil {
						newRec = tableSchema.RowToRecord(updatedRow)
					}
					if err := f.routeOutputRow(data, oldRec, newRec, nil, tableSchema); err != nil {
						if catchErr := f.handleCatch(err, &pc); catchErr != nil {
							return nil, catchErr
						}
						continue vmLoop
					}
				}
				continue vmLoop
			}
		}

		var oldRec storage.Record
		if !isTransientTable(targetTable) {
			oldRec, _ = f.tx.Get(targetTable, pkVal)
		}

		rowCtx := f.buildRowEvalMap(recordToMap(oldRec))
		rec := make(storage.Record)
		for _, a := range data.Assignments {
			valAny, err := proc.EvalUpdateRowExpr(a.Value, rowCtx, nil)
			if err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			colName := a.Column
			stVal := toStorageValue(valAny)
			if tableSchema != nil {
				if colSchema, _, ok := tableSchema.FindColumn(colName); ok {
					colName = colSchema.Name
					if colSchema.Type == storage.TypeBinary && stVal.Type == storage.TypeVarChar {
						stVal = storage.BinaryValueFromString(stVal.SVal)
					}
				}
			} else if oldRec != nil {
				for k := range oldRec {
					if sqlutil.EqualFoldIdent(k, colName) {
						colName = k
						break
					}
				}
			}
			rec.Set(colName, stVal)
		}

		if tableSchema != nil && tableSchema.HasCheckConstraints() {
			merged := make(storage.Record, len(oldRec)+len(rec))
			for k, v := range oldRec {
				merged[k] = v
			}
			for k, v := range rec {
				merged[k] = v
			}
			if err := proc.ValidateRecordChecks(tableSchema, merged); err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
		}

		if err := f.tx.Update(targetTable, pkVal, rec); err != nil {
			if errors.Is(err, storage.ErrKeyNotFound) {
				f.rowCount = 0
				continue vmLoop
			}
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.rowCount = 1
		if len(data.OutputCols) > 0 {
			merged := make(storage.Record, len(oldRec)+len(rec))
			for k, v := range oldRec {
				merged[k] = v
			}
			for k, v := range rec {
				merged[k] = v
			}
			if err := f.routeOutputRow(data, oldRec, merged, nil, tableSchema); err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
		}
		if oldRec != nil && hasTriggers {
			oldRow := recordToMap(oldRec)
			newRow := recordToMap(oldRec)
			for k, v := range rec {
				newRow[k] = valToAny(v)
			}
			if err := f.fireTriggers(targetTable, "UPDATE", []map[string]any{newRow}, []map[string]any{oldRow}); err != nil {
				return nil, err
			}
		}

	case proc.OpUpdateWhere:
		data, ok := f.aux(&inst).(*proc.DMLData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpUpdateWhere metadata")
		}
		cnt, err := f.execUpdateWhere(data)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.rowCount = cnt

	case proc.OpDelete:
		data, ok := f.aux(&inst).(*proc.DMLData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpDelete metadata")
		}
		targetTable := f.resolveTable(data.Table)
		if data.WhereCol != "" && !f.isPKCol(targetTable, data.WhereCol) {
			data.WhereExpr = &proc.BinaryExpr{
				Left:  &proc.ColumnRefExpr{Name: data.WhereCol},
				Op:    proc.TokenEq,
				Right: data.WhereVal,
			}
			cnt, err := f.execDeleteWhere(data)
			if err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			f.rowCount = cnt
			continue vmLoop
		}

		var pkVal storage.Value
		if v, ok := f.evalRowExprFast(data.WhereVal); ok {
			pkVal = v
		} else {
			evalCtx := f.buildRowEvalMap(nil)
			pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
			pkVal = toStorageValue(pkAny)
		}

		hasTriggersDelete := f.executor != nil && f.executor.HasTriggers(targetTable, "DELETE")
		var oldRec storage.Record
		if !isTransientTable(targetTable) && (len(data.OutputCols) > 0 || hasTriggersDelete) {
			oldRec, _ = f.tx.Get(targetTable, pkVal)
		}

		if err := f.tx.Delete(targetTable, pkVal); err != nil {
			if errors.Is(err, storage.ErrKeyNotFound) {
				f.rowCount = 0
				continue vmLoop
			}
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.rowCount = 1
		if len(data.OutputCols) > 0 && oldRec != nil {
			tableSchema, _ := f.getTableSchema(targetTable)
			if err := f.routeOutputRow(data, oldRec, nil, nil, tableSchema); err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
		}
		hasTriggersDel := f.executor != nil && f.executor.HasTriggers(targetTable, "DELETE")
		if oldRec != nil && hasTriggersDel {
			oldRow := recordToMap(oldRec)
			if err := f.fireTriggers(targetTable, "DELETE", nil, []map[string]any{oldRow}); err != nil {
				return nil, err
			}
		}

	case proc.OpDeleteWhere:
		data, ok := f.aux(&inst).(*proc.DMLData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpDeleteWhere metadata")
		}
		cnt, err := f.execDeleteWhere(data)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.rowCount = cnt

	case proc.OpTruncate:
		if inst.A < 0 || int(inst.A) >= len(f.prog.Constants) {
			return nil, fmt.Errorf("invalid OpTruncate constant index: %d", inst.A)
		}
		tableName := f.prog.Constants[inst.A].String()
		if err := f.execTruncate(tableName); err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}

	case proc.OpSelectInto:
		data, ok := f.aux(&inst).(*proc.SelectIntoData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpSelectInto metadata")
		}
		val, err := f.execSelectInto(data)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		if data.IntoVar != "" {
			slot := inst.B
			if slot >= 0 && int(slot) < len(f.slots) {
				f.slots[slot] = val
				f.evalCtxDirty = true
			} else {
				f.setVar(data.IntoVar, val)
			}
		}
		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			f.regs[inst.A] = val
		}

	case proc.OpSelectAssign:
		data, ok := f.aux(&inst).(*proc.SelectAssignData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpSelectAssign metadata")
		}
		if err := f.execSelectAssign(data); err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}

	case proc.OpPointSelect:
		data, ok := f.aux(&inst).(*proc.PointSelectData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpPointSelect metadata")
		}
		var pkVal storage.Value
		if v, ok := f.evalRowExprFast(data.WhereVal); ok {
			pkVal = v
		} else {
			evalCtx := f.buildRowEvalMap(nil)
			vAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
			pkVal = toStorageValue(vAny)
		}

		tableName := f.resolveTable(data.Table)
		if !f.isPKCol(tableName, data.WhereCol) {
			sql := f.prog.QuerySQL
			if sql == "" {
				sql = fmt.Sprintf("SELECT * FROM %s WHERE %s = %v", data.Table, data.WhereCol, pkVal.String())
			}
			rows, cols, vals, err := f.execRelQuery(sql, nil)
			if err != nil {
				return nil, err
			}
			f.rows = rows
			f.columns = cols
			f.values = vals
			f.rowCount = int64(len(rows))
			if inst.A >= 0 && int(inst.A) < len(f.regs) {
				f.regs[inst.A] = storage.IntValue(int64(len(rows)))
			}
			if len(rows) > 0 {
				lastRow := rows[len(rows)-1]
				for _, p := range data.Projections {
					if p.IsAssign {
						if v, ok := lastRow[p.Alias]; ok {
							f.setVar(p.Alias, toStorageValue(v))
						}
					}
				}
			}
			if len(data.Projections) > 0 {
				allVars := true
				for _, p := range data.Projections {
					if !p.IsAssign {
						allVars = false
						break
					}
				}
				if allVars {
					f.rows = nil
					f.columns = nil
					f.values = nil
					f.rowCount = 0
				}
			}
			continue vmLoop
		}

		var rowMap map[string]any
		rec, err := f.tx.Get(tableName, pkVal)
		if err == nil && rec != nil {
			rowMap = recordToMap(rec)
		}

		var rows []map[string]any
		var cols []string

		isStar := len(data.Projections) == 1 && (data.Projections[0].Expr.String() == "*" || data.Projections[0].Alias == "*")

		if rowMap != nil {
			outRow := make(map[string]any)
			rowCtx := f.buildRowEvalMap(rowMap)

			if isStar {
				for k := range rowMap {
					cols = append(cols, k)
				}
				sort.Strings(cols)
				for _, c := range cols {
					outRow[c] = rowMap[c]
				}
			} else {
				for _, p := range data.Projections {
					colName := p.Alias
					if colName == "" {
						if colRef, ok := p.Expr.(*proc.ColumnRefExpr); ok {
							colName = colRef.Name
						} else {
							colName = p.Expr.String()
						}
					}
					cleanName := cleanSymbol(colName)
					if cleanName == "" {
						cleanName = colName
					}
					cols = append(cols, cleanName)

					vAny, _ := proc.EvalRowExpr(p.Expr, rowCtx, nil)
					outRow[cleanName] = vAny
					if colName != cleanName {
						outRow[colName] = vAny
					}
					if p.IsAssign {
						f.setVar(colName, toStorageValue(vAny))
					}
				}
			}
			rows = append(rows, outRow)
		} else {
			if !isStar {
				for _, p := range data.Projections {
					colName := p.Alias
					if colName == "" {
						if colRef, ok := p.Expr.(*proc.ColumnRefExpr); ok {
							colName = colRef.Name
						} else {
							colName = p.Expr.String()
						}
					}
					cleanName := cleanSymbol(colName)
					if cleanName == "" {
						cleanName = colName
					}
					cols = append(cols, cleanName)
				}
			}
		}

		f.rows = rows
		f.columns = cols
		f.rowCount = int64(len(rows))

		if len(data.Projections) > 0 {
			allVars := true
			for _, p := range data.Projections {
				if !p.IsAssign {
					allVars = false
					break
				}
			}
			if allVars {
				f.rows = nil
				f.columns = nil
				f.values = nil
				f.rowCount = 0
			}
		}

		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			f.regs[inst.A] = storage.IntValue(f.rowCount)
		}


// --- Cursors and Calls ---
	case proc.OpCursorOpen:
		switch data := f.aux(&inst).(type) {
		case *proc.CursorData:
			curName := strings.ToLower(cleanSymbol(data.Name))
			f.cursors[curName] = &cursorState{
				name:      curName,
				table:     f.resolveTable(data.Table),
				columns:   data.Columns,
				whereExpr: data.WhereExpr,
				orderBy:   data.OrderBy,
				idx:       0,
				isOpen:    false,
			}
		case string:
			curName := strings.ToLower(cleanSymbol(data))
			cur, exists := f.cursors[curName]
			if !exists {
				err := fmt.Errorf("cursor %s does not exist", curName)
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			if cur.isOpen {
				err := fmt.Errorf("cursor %s is already open", curName)
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			rows, cols, err := f.scanCursorRows(cur.table, cur.whereExpr, cur.columns)
			if err != nil {
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			if len(cur.orderBy) > 0 && len(rows) > 1 {
				f.sortCursorRows(rows, cur.orderBy)
			}
			cur.rows = rows
			if len(cols) > 0 {
				cur.columns = cols
			}
			cur.idx = 0
			cur.isOpen = true
			f.lastOpenedCursor = curName
			f.cursorRows = int64(len(rows))
		}

	case proc.OpCursorFetch:
		switch data := f.aux(&inst).(type) {
		case *proc.CursorData:
			curName := strings.ToLower(cleanSymbol(data.Name))
			cur, exists := f.cursors[curName]
			if !exists || !cur.isOpen || cur.idx >= len(cur.rows) {
				f.fetchStatus = -1
			} else {
				row := cur.rows[cur.idx]
				cur.idx++
				f.fetchStatus = 0
				for i, varName := range data.IntoVars {
					if i < len(cur.columns) {
						col := cur.columns[i]
						if v, ok := row[col]; ok {
							f.setVar(varName, v)
						} else {
							// Case-insensitive fallback
							found := false
							for rk, rv := range row {
								if strings.EqualFold(rk, col) {
									f.setVar(varName, rv)
									found = true
									break
								}
							}
							if !found {
								f.setVar(varName, storage.NullValue(storage.TypeUnknown))
							}
						}
					}
				}
			}
		default:
			return nil, fmt.Errorf("invalid cursor fetch metadata")
		}

	case proc.OpCursorClose:
		rawName, ok := f.aux(&inst).(string)
		if !ok {
			return nil, fmt.Errorf("invalid cursor close target")
		}
		curName := strings.ToLower(cleanSymbol(rawName))
		if cur, exists := f.cursors[curName]; exists {
			if !cur.isOpen {
				err := fmt.Errorf("cursor %s is not open", curName)
				if catchErr := f.handleCatch(err, &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
			cur.isOpen = false
			cur.rows = nil // immediately release row buffer memory ([RULE:BoundedBuffers])
			cur.idx = 0
			if f.lastOpenedCursor == curName {
				f.cursorRows = 0
				f.lastOpenedCursor = ""
			}
		} else {
			err := fmt.Errorf("cursor %s does not exist", curName)
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}

	case proc.OpCursorDealloc:
		rawName, ok := f.aux(&inst).(string)
		if !ok {
			return nil, fmt.Errorf("invalid cursor deallocate target")
		}
		curName := strings.ToLower(cleanSymbol(rawName))
		if cur, exists := f.cursors[curName]; exists {
			cur.rows = nil
			delete(f.cursors, curName)
		}
		if f.lastOpenedCursor == curName {
			f.cursorRows = 0
			f.lastOpenedCursor = ""
		}

	case proc.OpCallBuiltin:
		callData, ok := f.aux(&inst).(*proc.CallData)
		if !ok || callData == nil {
			return nil, fmt.Errorf("invalid builtin call metadata")
		}
		var args []storage.Value
		for _, regIdx := range callData.ArgRegs {
			if regIdx >= 0 && int(regIdx) < len(f.regs) {
				args = append(args, f.regs[regIdx])
			}
		}
		if strings.EqualFold(callData.Name, "SCOPE_IDENTITY") {
			if f.lastIdentity > 0 {
				f.regs[inst.A] = storage.IntValue(f.lastIdentity)
			} else {
				f.regs[inst.A] = storage.NullValue(storage.TypeInt)
			}
		} else if strings.EqualFold(callData.Name, "ERROR_NUMBER") {
			f.regs[inst.A] = storage.IntValue(int64(f.lastError))
		} else if strings.EqualFold(callData.Name, "ERROR_MESSAGE") {
			f.regs[inst.A] = storage.StringValue(f.lastErrorMsg)
		} else if strings.EqualFold(callData.Name, "ERROR_LINE") {
			f.regs[inst.A] = storage.IntValue(int64(f.lastErrorLine))
		} else if strings.EqualFold(callData.Name, "ERROR_SEVERITY") {
			f.regs[inst.A] = storage.IntValue(16)
		} else if strings.EqualFold(callData.Name, "ERROR_STATE") {
			f.regs[inst.A] = storage.IntValue(1)
		} else if strings.EqualFold(callData.Name, "ERROR_PROCEDURE") {
			if f.prog != nil && f.prog.Name != "" {
				f.regs[inst.A] = storage.StringValue(f.prog.Name)
			} else {
				f.regs[inst.A] = storage.NullValue(storage.TypeVarChar)
			}
		} else if strings.EqualFold(callData.Name, "XACT_STATE") {
			if f.tranCount > 0 {
				if f.txFailed {
					f.regs[inst.A] = storage.IntValue(-1)
				} else {
					f.regs[inst.A] = storage.IntValue(1)
				}
			} else {
				f.regs[inst.A] = storage.IntValue(0)
			}
		} else if strings.EqualFold(callData.Name, "DB_NAME") || strings.EqualFold(callData.Name, "CURRENT_DATABASE") {
			if len(args) == 0 {
				if dbVal, ok := f.getVar("current_database"); ok && !dbVal.IsNull && dbVal.SVal != "" {
					f.regs[inst.A] = dbVal
				} else if dbVal, ok := f.getVar("db_name"); ok && !dbVal.IsNull && dbVal.SVal != "" {
					f.regs[inst.A] = dbVal
				} else {
					f.regs[inst.A] = proc.EvalBuiltin(callData.Name, args)
				}
			} else {
				f.regs[inst.A] = proc.EvalBuiltin(callData.Name, args)
			}
		} else {
			f.regs[inst.A] = proc.EvalBuiltin(callData.Name, args)
		}

	case proc.OpCallUDF:
		callData, ok := f.aux(&inst).(*proc.CallData)
		if !ok || callData == nil {
			return nil, fmt.Errorf("invalid OpCallUDF metadata")
		}
		res, err := f.callUDF(callData.Name, callData.ArgRegs)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.regs[inst.A] = res

	case proc.OpCallProc:
		callData, ok := f.aux(&inst).(*proc.CallData)
		if !ok || callData == nil {
			return nil, fmt.Errorf("invalid OpCallProc metadata")
		}
		retVal, err := f.callProc(callData.Name, callData.ArgRegs, callData.OutSlots)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		if inst.A >= 0 && int(inst.A) < len(f.slots) {
			f.slots[inst.A] = retVal
			f.evalCtxDirty = true
		}

	case proc.OpExecDynamic:
		val := f.regs[inst.A]
		sqlStr := val.String()
		if val.Type == storage.TypeVarChar {
			sqlStr = val.SVal
		}
		sqlStr = strings.TrimSpace(sqlStr)
		if sqlStr == "" {
			continue vmLoop
		}

		prog, err := f.executor.Cache().GetOrCompile(sqlStr)
		if err != nil {
			prog, err = proc.Compile(sqlStr)
			if err != nil {
				if catchErr := f.handleCatch(fmt.Errorf("dynamic SQL compile error: %w", err), &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
			}
		}

		res, err := f.executor.ExecuteInTx(prog, nil, f.tx, f.transientTables)
		if err != nil {
			if catchErr := f.handleCatch(fmt.Errorf("dynamic SQL execution error: %w", err), &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		if res != nil {
			if len(res.Logs) > 0 {
				f.logs = append(f.logs, res.Logs...)
			}
			if len(res.Rows) > 0 {
				f.rows = res.Rows
				f.columns = res.Columns
				f.rowCount = int64(len(res.Rows))
			}
		}


// --- Relational and Results ---
	case proc.OpRelQuery:
		var queryData *proc.RelQueryData
		if data, ok := f.aux(&inst).(*proc.RelQueryData); ok {
			queryData = data
		}
		sql := ""
		if queryData != nil {
			sql = queryData.SQL
		}
		if sql == "" {
			sql = f.prog.QuerySQL
		}
		if sql == "" {
			return nil, fmt.Errorf("OpRelQuery: empty query SQL")
		}

		if len(f.tableVars) > 0 || len(f.sessionTempTables) > 0 {
			for varName, physTable := range f.tableVars {
				if strings.HasPrefix(varName, "#") {
					sql = sqlutil.SubstituteParamOutsideQuotes(sql, varName, "["+physTable+"]")
				} else {
					sql = sqlutil.SubstituteParamOutsideQuotes(sql, "@"+strings.TrimPrefix(varName, "@"), "["+physTable+"]")
				}
			}
			for varName, physTable := range f.sessionTempTables {
				if _, overridden := f.tableVars[strings.ToLower(varName)]; !overridden {
					if strings.HasPrefix(varName, "#") {
						sql = sqlutil.SubstituteParamOutsideQuotes(sql, varName, "["+physTable+"]")
					} else {
						sql = sqlutil.SubstituteParamOutsideQuotes(sql, "#"+varName, "["+physTable+"]")
					}
				}
			}
			queryData = nil
		}

		rows, cols, vals, err := f.execRelQuery(sql, queryData)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}

		if len(f.columns) > 0 {
			f.resultSets = append(f.resultSets, VMResultSet{
				Columns: f.columns,
				Rows:    f.rows,
				Values:  f.values,
			})
		}
		f.rows = rows
		f.columns = cols
		f.values = vals
		f.rowCount = int64(len(rows))

		if queryData != nil && queryData.IntoVar != "" && len(rows) > 0 {
			var val any
			if len(cols) > 0 {
				if v, ok := rows[0][cols[0]]; ok {
					val = v
				} else {
					for k, v := range rows[0] {
						if strings.EqualFold(k, cols[0]) {
							val = v
							break
						}
					}
				}
			}
			if val == nil && len(cols) == 0 {
				for _, v := range rows[0] {
					val = v
					break
				}
			}
			f.setVar(queryData.IntoVar, toStorageValue(val))
			f.rows = nil
			f.columns = nil
			f.values = nil
			f.rowCount = 0
		}
		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			f.regs[inst.A] = storage.IntValue(int64(len(rows)))
		}

	case proc.OpIndexScan:
		data, ok := f.aux(&inst).(*proc.IndexScanData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid OpIndexScan metadata")
		}
		evalCtx := f.buildRowEvalMap(nil)

		var lowVal *storage.Value
		if data.LowVal != nil {
			vAny, _ := proc.EvalRowExpr(data.LowVal, evalCtx, nil)
			sv := toStorageValue(vAny)
			lowVal = &sv
		}

		var highVal *storage.Value
		if data.HighVal != nil {
			vAny, _ := proc.EvalRowExpr(data.HighVal, evalCtx, nil)
			sv := toStorageValue(vAny)
			highVal = &sv
		}

		var cursor storage.Cursor
		var err error
		if data.IndexOnly {
			cursor, err = f.tx.IndexOnlyScan(data.Table, data.IndexName, lowVal, highVal)
		} else {
			cursor, err = f.tx.IndexScan(data.Table, data.IndexName, lowVal, highVal)
		}
		if err != nil {
			if catchErr := f.handleCatch(fmt.Errorf("OpIndexScan on %s (%s) error: %w", data.Table, data.IndexName, err), &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}

		var rows []map[string]any
		var cols []string
		if len(data.Projections) > 0 {
			cols = data.Projections
		}

		var scanRecErr error
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				scanRecErr = fmt.Errorf("OpIndexScan record error on %s: %w", data.Table, err)
				break
			}
			rowMap := recordToMap(rec)

			if data.WhereExpr != nil {
				rowContext := f.buildRowEvalMap(rowMap)
				if !proc.EvalBoolExpr(data.WhereExpr, rowContext, nil) {
					continue
				}
			}

			if len(cols) == 0 {
				for k := range rec {
					cols = append(cols, k)
				}
				sort.Strings(cols)
			}

			outRow := make(map[string]any, len(cols))
			for _, c := range cols {
				if v, ok := rowMap[c]; ok {
					outRow[c] = v
				} else {
					outRow[c] = nil
				}
			}
			rows = append(rows, outRow)
		}
		_ = cursor.Close()
		if scanRecErr != nil {
			if catchErr := f.handleCatch(scanRecErr, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}

		f.rows = rows
		f.columns = cols
		f.rowCount = int64(len(rows))

		if data.IntoVar != "" && len(rows) > 0 {
			var val any
			if len(cols) > 0 {
				if v, ok := rows[0][cols[0]]; ok {
					val = v
				} else {
					for k, v := range rows[0] {
						if strings.EqualFold(k, cols[0]) {
							val = v
							break
						}
					}
				}
			}
			if val == nil && len(cols) == 0 {
				for _, v := range rows[0] {
					val = v
					break
				}
			}
			f.setVar(data.IntoVar, toStorageValue(val))
		}
		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			f.regs[inst.A] = storage.IntValue(int64(len(rows)))
		}

	case proc.OpResultRow:
		data, ok := f.aux(&inst).(*proc.ResultRowData)
		if !ok || data == nil || data.Count < 0 {
			return nil, fmt.Errorf("invalid OpResultRow metadata")
		}
		row := make(map[string]any, data.Count)
		vals := make([]any, data.Count)
		cols := make([]string, len(data.Columns))
		copy(cols, data.Columns)

		for i := int32(0); i < data.Count; i++ {
			var regIdx int32
			if len(data.Regs) > int(i) {
				regIdx = data.Regs[i]
			} else {
				regIdx = data.StartReg + i
			}
			var val any
			if regIdx >= 0 && int(regIdx) < len(f.regs) {
				val = valToAny(f.regs[regIdx])
			}
			vals[i] = val
			colName := fmt.Sprintf("col%d", i+1)
			if int(i) < len(data.Columns) && data.Columns[i] != "" {
				colName = data.Columns[i]
			}
			row[colName] = val
		}

		if len(f.columns) > 0 {
			f.resultSets = append(f.resultSets, VMResultSet{
				Columns: f.columns,
				Rows:    f.rows,
				Values:  f.values,
			})
			f.rows = nil
			f.values = nil
		}

		f.rows = append(f.rows, row)
		f.values = append(f.values, vals)
		f.columns = cols
		f.rowCount = int64(len(f.rows))

		default:
			return nil, fmt.Errorf("unsupported opcode: %v", inst.Op)
		}
	}
	return nil, nil
}

func parseTsqlDelay(val storage.Value, isDelay bool) (time.Duration, error) {
	if val.IsNull {
		return 0, fmt.Errorf("WAITFOR delay/time expression cannot be NULL")
	}
	str := strings.TrimSpace(valToString(val))
	if str == "" {
		return 0, fmt.Errorf("WAITFOR delay/time expression cannot be empty")
	}
	parts := strings.Split(str, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid time format for WAITFOR: %q (expected 'hh:mm:ss[.fff]')", str)
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil || hours < 0 || hours > 24 {
		return 0, fmt.Errorf("invalid hours in WAITFOR: %q (must be between 0 and 24)", parts[0])
	}
	mins, err := strconv.Atoi(parts[1])
	if err != nil || mins < 0 || mins >= 60 {
		return 0, fmt.Errorf("invalid minutes in WAITFOR: %q (must be between 0 and 59)", parts[1])
	}
	secParts := strings.Split(parts[2], ".")
	secs, err := strconv.Atoi(secParts[0])
	if err != nil || secs < 0 || secs >= 60 {
		return 0, fmt.Errorf("invalid seconds in WAITFOR: %q (must be between 0 and 59)", secParts[0])
	}
	var millis int
	if len(secParts) > 1 {
		fraction := secParts[1]
		if len(fraction) > 3 {
			fraction = fraction[:3]
		}
		for len(fraction) < 3 {
			fraction += "0"
		}
		var mErr error
		millis, mErr = strconv.Atoi(fraction)
		if mErr != nil || millis < 0 {
			return 0, fmt.Errorf("invalid milliseconds in WAITFOR: %q", secParts[1])
		}
	}

	if isDelay {
		if hours == 24 && (mins > 0 || secs > 0 || millis > 0) {
			return 0, fmt.Errorf("WAITFOR DELAY cannot exceed 24 hours")
		}
		dur := time.Duration(hours)*time.Hour +
			time.Duration(mins)*time.Minute +
			time.Duration(secs)*time.Second +
			time.Duration(millis)*time.Millisecond
		return dur, nil
	}

	// WAITFOR TIME 'hh:mm:ss'
	if hours == 24 {
		hours = 0
	}
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), hours, mins, secs, millis*1e6, now.Location())
	if target.Before(now) {
		target = target.Add(24 * time.Hour)
	}
	return target.Sub(now), nil
}

func slicesEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}


