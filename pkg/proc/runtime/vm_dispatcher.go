package runtime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

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
func (f *VMFrame) execFlowOp(inst *proc.Instruction, pc *int) (halt bool, retVal any, err error) {
	switch inst.Op {
	case proc.OpHalt:
		return true, nil, nil

	case proc.OpNop:
		return false, nil, nil

	case proc.OpReturn:
		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			return true, valToAny(f.regs[inst.A]), nil
		}
		return true, nil, nil

	case proc.OpJump:
		*pc = int(inst.B)
		return false, nil, nil

	case proc.OpJumpIfFalse:
		if !isTruthy(f.regs[inst.A]) {
			*pc = int(inst.B)
		}
		return false, nil, nil

	case proc.OpJumpIfTrue:
		if isTruthy(f.regs[inst.A]) {
			*pc = int(inst.B)
		}
		return false, nil, nil

	case proc.OpJumpIfEqInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal == vb.IVal {
				*pc = int(inst.C)
			}
		} else if storageEq(va, vb) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfNeqInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal != vb.IVal {
				*pc = int(inst.C)
			}
		} else if !storageEq(va, vb) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfLtInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal < vb.IVal {
				*pc = int(inst.C)
			}
		} else if storageLt(va, vb) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfLteInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal <= vb.IVal {
				*pc = int(inst.C)
			}
		} else if storageLte(va, vb) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfGtInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal > vb.IVal {
				*pc = int(inst.C)
			}
		} else if storageGt(va, vb) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfGteInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal >= vb.IVal {
				*pc = int(inst.C)
			}
		} else if storageGte(va, vb) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfEq:
		if storageEq(f.regs[inst.A], f.regs[inst.B]) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfNeq:
		if !storageEq(f.regs[inst.A], f.regs[inst.B]) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfLt:
		if storageLt(f.regs[inst.A], f.regs[inst.B]) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfLte:
		if storageLte(f.regs[inst.A], f.regs[inst.B]) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfGt:
		if storageGt(f.regs[inst.A], f.regs[inst.B]) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpJumpIfGte:
		if storageGte(f.regs[inst.A], f.regs[inst.B]) {
			*pc = int(inst.C)
		}
		return false, nil, nil

	case proc.OpPushCatch:
		f.catchStack = append(f.catchStack, int(inst.B))
		return false, nil, nil

	case proc.OpPopCatch:
		if len(f.catchStack) > 0 {
			f.catchStack = f.catchStack[:len(f.catchStack)-1]
		}
		return false, nil, nil

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
			// If severity <= 10, it is informational
			if sev <= 10 {
				f.logs = append(f.logs, fmt.Sprintf("%v", f.aux(inst)))
				return false, nil, nil
			}
		}

		f.lastError = errNum
		msg := fmt.Sprintf("%v", f.aux(inst))
		f.lastErrorMsg = msg
		if pc != nil {
			f.lastErrorLine = *pc
		}
		if len(f.catchStack) > 0 {
			catchPC := f.catchStack[len(f.catchStack)-1]
			f.catchStack = f.catchStack[:len(f.catchStack)-1]
			*pc = catchPC
			return false, nil, nil
		}
		return false, nil, &proc.ProcError{
			Number:   int32(errNum),
			Severity: byte(sev),
			State:    state,
			Message:  msg,
		}

	case proc.OpPrint:
		v := f.regs[inst.A]
		f.logs = append(f.logs, valToString(v))
		return false, nil, nil

	default:
		return false, nil, fmt.Errorf("unexpected flow opcode: %v", inst.Op)
	}
}

// execComputeOp handles constants, variables, arithmetic, comparisons, and boolean logic.
func (f *VMFrame) execComputeOp(inst *proc.Instruction, pc *int) error {
	switch inst.Op {
	case proc.OpLoadConst:
		if int(inst.B) < len(f.prog.Constants) {
			f.regs[inst.A] = f.prog.Constants[inst.B]
		}

	case proc.OpLoadVar:
		slot := inst.B
		if slot >= 0 && int(slot) < len(f.slots) {
			f.regs[inst.A] = f.slots[slot]
		} else {
			varName, _ := f.aux(inst).(string)
			v, found := f.getVar(varName)
			if !found {
				v = storage.StringValue(varName)
			}
			f.regs[inst.A] = v
		}

	case proc.OpStoreVar:
		val := f.regs[inst.A]
		slot := inst.B
		varName, _ := f.aux(inst).(string)
		if slot >= 0 && int(slot) < len(f.slots) {
			f.slots[slot] = val
			if varName != "" {
				f.setVar(varName, val)
			}
		} else if varName != "" {
			f.setVar(varName, val)
		}

	case proc.OpCopy:
		f.regs[inst.A] = f.regs[inst.B]

	case proc.OpAdd:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal + vc.IVal)
		} else {
			f.regs[inst.A] = storageAdd(vb, vc)
		}

	case proc.OpAddInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal + vc.IVal)
		} else {
			f.regs[inst.A] = storageAdd(vb, vc)
		}

	case proc.OpSub:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal - vc.IVal)
		} else {
			f.regs[inst.A] = storageSub(vb, vc)
		}

	case proc.OpSubInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal - vc.IVal)
		} else {
			f.regs[inst.A] = storageSub(vb, vc)
		}

	case proc.OpMul:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal * vc.IVal)
		} else {
			f.regs[inst.A] = storageMul(vb, vc)
		}

	case proc.OpMulInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.IntValue(vb.IVal * vc.IVal)
		} else {
			f.regs[inst.A] = storageMul(vb, vc)
		}

	case proc.OpDiv:
		res, err := storageDiv(f.regs[inst.B], f.regs[inst.C])
		if err != nil {
			return f.handleCatch(err, pc)
		}
		f.regs[inst.A] = res

	case proc.OpNeg:
		f.regs[inst.A] = storageNeg(f.regs[inst.B])

	case proc.OpNot:
		f.regs[inst.A] = storage.BoolValue(!isTruthy(f.regs[inst.B]))

	case proc.OpAnd:
		f.regs[inst.A] = storage.BoolValue(isTruthy(f.regs[inst.B]) && isTruthy(f.regs[inst.C]))

	case proc.OpOr:
		f.regs[inst.A] = storage.BoolValue(isTruthy(f.regs[inst.B]) || isTruthy(f.regs[inst.C]))

	case proc.OpEq:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal == vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageEq(vb, vc))
		}

	case proc.OpEqInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal == vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageEq(vb, vc))
		}

	case proc.OpNeq:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal != vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(!storageEq(vb, vc))
		}

	case proc.OpNeqInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal != vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(!storageEq(vb, vc))
		}

	case proc.OpLt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal < vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageLt(vb, vc))
		}

	case proc.OpLtInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal < vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageLt(vb, vc))
		}

	case proc.OpLte:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal <= vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageLte(vb, vc))
		}

	case proc.OpLteInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal <= vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageLte(vb, vc))
		}

	case proc.OpGt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal > vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageGt(vb, vc))
		}

	case proc.OpGtInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal > vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageGt(vb, vc))
		}

	case proc.OpGte:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal >= vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageGte(vb, vc))
		}

	case proc.OpGteInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt && !vb.IsNull && !vc.IsNull {
			f.regs[inst.A] = storage.BoolValue(vb.IVal >= vc.IVal)
		} else {
			f.regs[inst.A] = storage.BoolValue(storageGte(vb, vc))
		}

	case proc.OpLike:
		str := valToString(f.regs[inst.B])
		pat := valToString(f.regs[inst.C])
		matched := matchLike(str, pat)
		if inst.Flags&1 != 0 {
			matched = !matched
		}
		f.regs[inst.A] = storage.BoolValue(matched)

	case proc.OpBetween:
		v := f.regs[inst.A]
		low := f.regs[inst.B]
		high := f.regs[inst.C]
		inRange := storageGte(v, low) && storageLte(v, high)
		if inst.Flags&1 != 0 {
			inRange = !inRange
		}
		f.regs[inst.A] = storage.BoolValue(inRange)

	case proc.OpIn:
		val := f.regs[inst.B]
		callData := f.aux(inst).(*proc.CallData)
		found := false
		for _, regIdx := range callData.ArgRegs {
			if int(regIdx) < len(f.regs) && storageEq(val, f.regs[regIdx]) {
				found = true
				break
			}
		}
		f.regs[inst.A] = storage.BoolValue(found)

	case proc.OpIsNull:
		f.regs[inst.A] = storage.BoolValue(f.regs[inst.B].IsNull)

	case proc.OpIsNotNull:
		f.regs[inst.A] = storage.BoolValue(!f.regs[inst.B].IsNull)

	case proc.OpCast:
		srcVal := f.regs[inst.B]
		castData := f.aux(inst).(*proc.CastData)
		resVal, err := proc.EvalCast(srcVal, castData.TargetType, castData.Precision, castData.Scale, castData.Style, castData.Try)
		if err != nil {
			return f.handleCatch(err, pc)
		}
		f.regs[inst.A] = resVal

	default:
		return fmt.Errorf("unexpected compute opcode: %v", inst.Op)
	}
	return nil
}

// execTxOp handles transaction demarcation, savepoints, and rollbacks.
func (f *VMFrame) execTxOp(inst *proc.Instruction) error {
	switch inst.Op {
	case proc.OpTxBegin:
		f.tranCount++

	case proc.OpTxCommit:
		if f.tranCount > 0 {
			f.tranCount--
		}
		if f.tx != nil {
			if err := f.tx.Commit(); err != nil && !errors.Is(err, storage.ErrTxClosed) {
				return err
			}
		}
		if f.session != nil {
			newTx, err := f.session.BeginTx(storage.TxOptions{})
			if err != nil {
				return err
			}
			f.tx = newTx
		}

	case proc.OpTxRollback:
		if f.tranCount > 0 {
			f.tranCount--
		}
		if f.tx != nil {
			if err := f.tx.Rollback(); err != nil && !errors.Is(err, storage.ErrTxClosed) {
				return err
			}
		}
		if f.session != nil {
			newTx, err := f.session.BeginTx(storage.TxOptions{})
			if err != nil {
				return err
			}
			f.tx = newTx
		}

	case proc.OpTxSavepoint:
		spName, _ := f.aux(inst).(string)
		if err := f.tx.Savepoint(spName); err != nil {
			return err
		}

	case proc.OpTxRollbackTo:
		spName, _ := f.aux(inst).(string)
		if err := f.tx.RollbackTo(spName); err != nil {
			return err
		}

	default:
		return fmt.Errorf("unexpected tx opcode: %v", inst.Op)
	}
	return nil
}

// execStorageOp handles table DML, single-table point lookups, and SELECT INTO.
func (f *VMFrame) execStorageOp(inst *proc.Instruction, pc *int) error {
	switch inst.Op {
	case proc.OpInsert:
		data := f.aux(inst).(*proc.DMLData)
		rec := make(storage.Record)
		rowMap := make(map[string]any)
		evalCtx := f.buildRowEvalMap(nil)

		cols := data.Columns
		var tableSchema *storage.TableSchema
		if tg := f.executor.getTableGetter(); tg != nil {
			if schema, err := tg(data.Table); err == nil && schema != nil {
				tableSchema = schema
				var identityCol *storage.Column
				for idx := range schema.Columns {
					if schema.Columns[idx].Identity {
						identityCol = &schema.Columns[idx]
						break
					}
				}

				identityInsertAllowed := false
				if val, ok := f.vars["identity_insert_table"]; ok && val.Type == storage.TypeVarChar {
					if strings.EqualFold(val.SVal, data.Table) {
						identityInsertAllowed = true
					}
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
							return fmt.Errorf("Cannot insert explicit value for identity column in table %q when IDENTITY_INSERT is set to OFF", data.Table)
						}
					}
				}
			}
		}

		for i, col := range cols {
			if i < len(data.ValExprs) {
				vAny, _ := proc.EvalRowExpr(data.ValExprs[i], evalCtx, nil)
				stVal := toStorageValue(vAny)
				if tableSchema != nil {
					if colSchema, _, ok := tableSchema.FindColumn(col); ok {
						col = colSchema.Name
					}
				}
				rec.Set(col, stVal)
				rowMap[col] = valToAny(stVal)
				if (strings.EqualFold(col, "id") || strings.HasSuffix(strings.ToLower(col), "id")) && stVal.Type == storage.TypeInt && !stVal.IsNull {
					f.lastIdentity = stVal.IVal
				}
			}
		}

		if tableSchema != nil {
			for _, col := range tableSchema.Columns {
				if _, exists := rec[col.Name]; !exists && col.Default != "" {
					upper := strings.ToUpper(strings.TrimSpace(col.Default))
					switch upper {
					case "NEWID7()", "UUIDV7()", "UUID_GENERATE_V7()":
						rec[col.Name] = storage.Value{Type: storage.TypeUUID, SVal: proc.GenerateUUIDv7()}
					case "NEWID()", "GEN_RANDOM_UUID()":
						rec[col.Name] = storage.Value{Type: storage.TypeUUID, SVal: proc.GenerateUUIDv4()}
					case "GETDATE()", "NOW()", "CURRENT_TIMESTAMP":
						rec[col.Name] = storage.DateTimeValue(time.Now())
					case "GETUTCDATE()", "SYSDATETIME()":
						rec[col.Name] = storage.DateTimeValue(time.Now().UTC())
					}
				}
			}
		}

		if err := f.tx.Insert(data.Table, rec); err != nil {
			if errors.Is(err, storage.ErrKeyExists) || errors.Is(err, storage.ErrUniqueViolation) {
				return f.handleCatch(fmt.Errorf("duplicate key value violates unique constraint on %q: %w", data.Table, err), pc)
			}
			return f.handleCatch(fmt.Errorf("insert error on %s: %w", data.Table, err), pc)
		}
		f.rowCount++
		if err := f.fireTriggers(data.Table, "INSERT", []map[string]any{rowMap}, nil); err != nil {
			return err
		}

	case proc.OpUpdate:
		data := f.aux(inst).(*proc.DMLData)
		if data.WhereCol != "" && !f.isPKCol(data.Table, data.WhereCol) {
			data.WhereExpr = &proc.BinaryExpr{
				Left:  &proc.ColumnRefExpr{Name: data.WhereCol},
				Op:    proc.TokenEq,
				Right: data.WhereVal,
			}
			cnt, err := f.execUpdateWhere(data)
			if err != nil {
				return f.handleCatch(err, pc)
			}
			f.rowCount = cnt
			return nil
		}

		evalCtx := f.buildRowEvalMap(nil)
		pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
		pkVal := toStorageValue(pkAny)

		var oldRec storage.Record
		if !isTransientTable(data.Table) {
			oldRec, _ = f.tx.Get(data.Table, pkVal)
		}

		var tableSchema *storage.TableSchema
		if tg := f.executor.getTableGetter(); tg != nil {
			if schema, err := tg(data.Table); err == nil && schema != nil {
				tableSchema = schema
			}
		}

		rowCtx := f.buildRowEvalMap(recordToMap(oldRec))
		rec := make(storage.Record)
		for _, a := range data.Assignments {
			valAny, _ := proc.EvalRowExpr(a.Value, rowCtx, nil)
			colName := a.Column
			if tableSchema != nil {
				if colSchema, _, ok := tableSchema.FindColumn(colName); ok {
					colName = colSchema.Name
				}
			}
			rec.Set(colName, toStorageValue(valAny))
		}

		if err := f.tx.Update(data.Table, pkVal, rec); err != nil {
			return f.handleCatch(err, pc)
		}
		f.rowCount = 1
		if oldRec != nil {
			oldRow := recordToMap(oldRec)
			newRow := recordToMap(oldRec)
			for k, v := range rec {
				newRow[k] = valToAny(v)
			}
			if err := f.fireTriggers(data.Table, "UPDATE", []map[string]any{newRow}, []map[string]any{oldRow}); err != nil {
				return err
			}
		}

	case proc.OpUpdateWhere:
		data := f.aux(inst).(*proc.DMLData)
		cnt, err := f.execUpdateWhere(data)
		if err != nil {
			return f.handleCatch(err, pc)
		}
		f.rowCount = cnt

	case proc.OpDelete:
		data := f.aux(inst).(*proc.DMLData)
		if data.WhereCol != "" && !f.isPKCol(data.Table, data.WhereCol) {
			data.WhereExpr = &proc.BinaryExpr{
				Left:  &proc.ColumnRefExpr{Name: data.WhereCol},
				Op:    proc.TokenEq,
				Right: data.WhereVal,
			}
			cnt, err := f.execDeleteWhere(data)
			if err != nil {
				return f.handleCatch(err, pc)
			}
			f.rowCount = cnt
			return nil
		}

		evalCtx := f.buildRowEvalMap(nil)
		pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
		pkVal := toStorageValue(pkAny)

		var oldRec storage.Record
		if !isTransientTable(data.Table) {
			oldRec, _ = f.tx.Get(data.Table, pkVal)
		}

		if err := f.tx.Delete(data.Table, pkVal); err != nil {
			return f.handleCatch(err, pc)
		}
		f.rowCount = 1
		if oldRec != nil {
			oldRow := recordToMap(oldRec)
			if err := f.fireTriggers(data.Table, "DELETE", nil, []map[string]any{oldRow}); err != nil {
				return err
			}
		}

	case proc.OpDeleteWhere:
		data := f.aux(inst).(*proc.DMLData)
		cnt, err := f.execDeleteWhere(data)
		if err != nil {
			return f.handleCatch(err, pc)
		}
		f.rowCount = cnt

	case proc.OpSelectInto:
		data := f.aux(inst).(*proc.SelectIntoData)
		val, err := f.execSelectInto(data)
		if err != nil {
			return f.handleCatch(err, pc)
		}
		if data.IntoVar != "" {
			f.setVar(data.IntoVar, val)
		}
		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			f.regs[inst.A] = val
		}

	case proc.OpPointSelect:
		data := f.aux(inst).(*proc.PointSelectData)
		evalCtx := f.buildRowEvalMap(nil)

		vAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
		pkVal := toStorageValue(vAny)

		tableName := cleanSymbol(data.Table)
		if !f.isPKCol(tableName, data.WhereCol) {
			sql := f.prog.QuerySQL
			if sql == "" {
				sql = fmt.Sprintf("SELECT * FROM %s WHERE %s = %v", data.Table, data.WhereCol, pkVal.String())
			}
			rows, cols, err := f.execRelQuery(sql, nil)
			if err != nil {
				return err
			}
			f.rows = rows
			f.columns = cols
			f.rowCount = int64(len(rows))
			if inst.A >= 0 && int(inst.A) < len(f.regs) {
				f.regs[inst.A] = storage.IntValue(int64(len(rows)))
			}
			if len(rows) > 0 {
				lastRow := rows[len(rows)-1]
				for _, p := range data.Projections {
					if strings.HasPrefix(p.Alias, "@") {
						if v, ok := lastRow[p.Alias]; ok {
							f.setVar(p.Alias, toStorageValue(v))
						}
					}
				}
			}
			return nil
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
					cols = append(cols, colName)

					vAny, _ := proc.EvalRowExpr(p.Expr, rowCtx, nil)
					outRow[colName] = vAny
					if strings.HasPrefix(colName, "@") {
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
					cols = append(cols, colName)
				}
			}
		}

		f.rows = rows
		f.columns = cols
		f.rowCount = int64(len(rows))

		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			f.regs[inst.A] = storage.IntValue(f.rowCount)
		}

	default:
		return fmt.Errorf("unexpected storage opcode: %v", inst.Op)
	}
	return nil
}

// execCursorAndCallOp handles cursors, built-in calls, UDFs, stored procedures, and dynamic SQL.
func (f *VMFrame) execCursorAndCallOp(inst *proc.Instruction, pc *int) error {
	switch inst.Op {
	case proc.OpCursorOpen:
		switch data := f.aux(inst).(type) {
		case *proc.CursorData:
			f.cursors[cleanSymbol(data.Name)] = &cursorState{
				name:      cleanSymbol(data.Name),
				table:     data.Table,
				columns:   data.Columns,
				whereExpr: data.WhereExpr,
				idx:       0,
			}
		case string:
			curName := cleanSymbol(data)
			if cur, exists := f.cursors[curName]; exists {
				rows, err := f.scanCursorRows(cur.table, cur.whereExpr)
				if err != nil {
					return err
				}
				cur.rows = rows
				cur.idx = 0
			}
		}

	case proc.OpCursorFetch:
		data := f.aux(inst).(*proc.CursorData)
		curName := cleanSymbol(data.Name)
		cur, exists := f.cursors[curName]
		if !exists || cur.idx >= len(cur.rows) {
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
						f.setVar(varName, storage.NullValue(storage.TypeUnknown))
					}
				}
			}
		}

	case proc.OpCursorClose:
		curName := cleanSymbol(f.aux(inst).(string))
		if cur, exists := f.cursors[curName]; exists {
			cur.idx = len(cur.rows)
		}

	case proc.OpCursorDealloc:
		curName := cleanSymbol(f.aux(inst).(string))
		delete(f.cursors, curName)

	case proc.OpCallBuiltin:
		callData := f.aux(inst).(*proc.CallData)
		var args []storage.Value
		for _, regIdx := range callData.ArgRegs {
			if int(regIdx) < len(f.regs) {
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
		callData := f.aux(inst).(*proc.CallData)
		res, err := f.callUDF(callData.Name, callData.ArgRegs)
		if err != nil {
			return f.handleCatch(err, pc)
		}
		f.regs[inst.A] = res

	case proc.OpCallProc:
		callData := f.aux(inst).(*proc.CallData)
		if err := f.callProc(callData.Name, callData.ArgRegs); err != nil {
			return f.handleCatch(err, pc)
		}

	case proc.OpExecDynamic:
		val := f.regs[inst.A]
		sqlStr := val.String()
		if val.Type == storage.TypeVarChar {
			sqlStr = val.SVal
		}
		sqlStr = strings.TrimSpace(sqlStr)
		if sqlStr == "" {
			return nil
		}

		prog, err := f.executor.Cache().GetOrCompile(sqlStr)
		if err != nil {
			prog, err = proc.Compile(sqlStr)
			if err != nil {
				return f.handleCatch(fmt.Errorf("dynamic SQL compile error: %w", err), pc)
			}
		}

		res, err := f.executor.ExecuteInTx(prog, nil, f.tx, f.transientTables)
		if err != nil {
			return f.handleCatch(fmt.Errorf("dynamic SQL execution error: %w", err), pc)
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

	default:
		return fmt.Errorf("unexpected cursor/call opcode: %v", inst.Op)
	}
	return nil
}

// execRelOp handles relational queries (Volcano engine), secondary index scans, and result row emission.
func (f *VMFrame) execRelOp(inst *proc.Instruction, pc *int) error {
	switch inst.Op {
	case proc.OpRelQuery:
		var queryData *proc.RelQueryData
		if data, ok := f.aux(inst).(*proc.RelQueryData); ok {
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
			return fmt.Errorf("OpRelQuery: empty query SQL")
		}

		rows, cols, err := f.execRelQuery(sql, queryData)
		if err != nil {
			return f.handleCatch(err, pc)
		}

		f.rows = rows
		f.columns = cols
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
		}
		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			f.regs[inst.A] = storage.IntValue(int64(len(rows)))
		}

	case proc.OpIndexScan:
		data := f.aux(inst).(*proc.IndexScanData)
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
			return f.handleCatch(fmt.Errorf("OpIndexScan on %s (%s) error: %w", data.Table, data.IndexName, err), pc)
		}
		defer cursor.Close()

		var rows []map[string]any
		var cols []string
		if len(data.Projections) > 0 {
			cols = data.Projections
		}

		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
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
		data := f.aux(inst).(*proc.ResultRowData)
		row := make(map[string]any, data.Count)
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
			if int(regIdx) < len(f.regs) {
				val = valToAny(f.regs[regIdx])
			}
			colName := fmt.Sprintf("col%d", i+1)
			if int(i) < len(data.Columns) && data.Columns[i] != "" {
				colName = data.Columns[i]
			}
			row[colName] = val
		}

		f.rows = append(f.rows, row)
		f.columns = cols
		f.rowCount = int64(len(f.rows))

	default:
		return fmt.Errorf("unexpected rel opcode: %v", inst.Op)
	}
	return nil
}
