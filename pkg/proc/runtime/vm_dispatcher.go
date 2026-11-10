package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
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
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal == vb.IVal {
				pc = int(inst.C)
			}
		} else if storageEq(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfNeqInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal != vb.IVal {
				pc = int(inst.C)
			}
		} else if !storageEq(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfLtInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal < vb.IVal {
				pc = int(inst.C)
			}
		} else if storageLt(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfLteInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal <= vb.IVal {
				pc = int(inst.C)
			}
		} else if storageLte(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfGtInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal > vb.IVal {
				pc = int(inst.C)
			}
		} else if storageGt(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfGteInt:
		va, vb := f.regs[inst.A], f.regs[inst.B]
		if va.Type == storage.TypeInt && vb.Type == storage.TypeInt && !va.IsNull && !vb.IsNull {
			if va.IVal >= vb.IVal {
				pc = int(inst.C)
			}
		} else if storageGte(va, vb) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfEq:
		if storageEq(f.regs[inst.A], f.regs[inst.B]) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfNeq:
		if !storageEq(f.regs[inst.A], f.regs[inst.B]) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfLt:
		if storageLt(f.regs[inst.A], f.regs[inst.B]) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfLte:
		if storageLte(f.regs[inst.A], f.regs[inst.B]) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfGt:
		if storageGt(f.regs[inst.A], f.regs[inst.B]) {
			pc = int(inst.C)
		}
		continue vmLoop

	case proc.OpJumpIfGte:
		if storageGte(f.regs[inst.A], f.regs[inst.B]) {
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
			// If severity <= 10, it is informational
			if sev <= 10 {
				f.logs = append(f.logs, fmt.Sprintf("%v", f.aux(&inst)))
				continue vmLoop
			}
		}

		f.lastError = errNum
		msg := fmt.Sprintf("%v", f.aux(&inst))
		f.lastErrorMsg = msg
		if true {
			f.lastErrorLine = pc
		}
		if len(f.catchStack) > 0 {
			catchPC := f.catchStack[len(f.catchStack)-1]
			f.catchStack = f.catchStack[:len(f.catchStack)-1]
			pc = catchPC
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
		f.regs[inst.A] = storage.BoolValue(isTruthy(f.regs[inst.B]) && isTruthy(f.regs[inst.C]))

	case proc.OpOr:
		f.regs[inst.A] = storage.BoolValue(isTruthy(f.regs[inst.B]) || isTruthy(f.regs[inst.C]))

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
		} else {
			f.regs[inst.A] = storage.BoolValue(storageEq(vb, vc))
		}

	case proc.OpNeq, proc.OpNeqInt:
		vb, vc := f.regs[inst.B], f.regs[inst.C]
		if vb.IsNull || vc.IsNull {
			f.regs[inst.A] = storage.NullValue(storage.TypeBool)
		} else if vb.Type == storage.TypeInt && vc.Type == storage.TypeInt {
			f.regs[inst.A] = storage.BoolValue(vb.IVal != vc.IVal)
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
			matched := matchLike(str, pat)
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
		callData := f.aux(&inst).(*proc.CallData)
		if len(callData.ArgRegs) == 0 {
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
			if int(regIdx) < len(f.regs) {
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

	case proc.OpIsNull:
		f.regs[inst.A] = storage.BoolValue(f.regs[inst.B].IsNull)

	case proc.OpIsNotNull:
		f.regs[inst.A] = storage.BoolValue(!f.regs[inst.B].IsNull)

	case proc.OpCast:
		srcVal := f.regs[inst.B]
		castData := f.aux(&inst).(*proc.CastData)
		resVal, err := proc.EvalCast(srcVal, castData.TargetType, castData.Precision, castData.Scale, castData.Style, castData.Try)
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
		data := f.aux(&inst).(*proc.TableVarData)
		varName := strings.ToLower(data.Name)
		if !strings.HasPrefix(varName, "@") {
			varName = "@" + varName
		}
		physTable := fmt.Sprintf("__tv_%p_%d_%s", f, time.Now().UnixNano(), strings.TrimPrefix(varName, "@"))

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

		if f.tableVars == nil {
			f.tableVars = make(map[string]string)
		} else if oldPhys, exists := f.tableVars[varName]; exists {
			if f.executor != nil {
				if tm := f.executor.getTableManager(); tm != nil {
					_ = tm.DropTable(oldPhys)
				}
			}
		}
		f.tableVars[varName] = physTable

	case proc.OpInsert:
		data := f.aux(&inst).(*proc.DMLData)
		targetTable := f.resolveTable(data.Table)
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
		canDirectInsert := !isTransientTable(targetTable) && tableSchema != nil && !hasTriggers && len(cols) == len(tableSchema.Columns) && len(data.ValExprs) == len(tableSchema.Columns) && identityCol == nil
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
			for i := range tableSchema.Columns {
				stVal, ok := f.evalRowExprFast(data.ValExprs[i])
				if !ok {
					allFast = false
					break
				}
				colSchema := tableSchema.Columns[i]
				if colSchema.Type != stVal.Type {
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
			if allFast {
				if err := f.tx.InsertRow(targetTable, storage.Row{Values: f.rowScratch}); err != nil {
					if catchErr := f.handleCatch(fmt.Errorf("insert error on %s: %w", targetTable, err), &pc); catchErr != nil {
						return nil, catchErr
					}
					continue vmLoop
				}
				f.rowCount++
				if len(data.OutputCols) > 0 && len(data.IntoVars) > 0 {
					for idx, outCol := range data.OutputCols {
						if idx < len(data.IntoVars) {
							intoVar := data.IntoVars[idx]
							if _, colIdx, ok := tableSchema.FindColumn(outCol); ok && colIdx >= 0 && colIdx < len(f.rowScratch) {
								f.setVar(intoVar, f.rowScratch[colIdx])
							}
						}
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
				stVal, ok := f.evalRowExprFast(data.ValExprs[i])
				if !ok {
					if evalCtx == nil {
						evalCtx = f.buildRowEvalMap(nil)
					}
					vAny, _ := proc.EvalRowExpr(data.ValExprs[i], evalCtx, nil)
					stVal = toStorageValue(vAny)
				}
				if tableSchema != nil {
					if colSchema, _, ok := tableSchema.FindColumn(col); ok {
						col = colSchema.Name
						if colSchema.Type != stVal.Type {
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
				var nextID int64 = 1
				if f.executor != nil {
					if lastID, ok := f.executor.GetLastIdentity(tableName); ok {
						nextID = lastID + 1
					} else {
						var maxID int64 = 0
						if cur, err := f.tx.Scan(data.Table); err == nil {
							for cur.Next() {
								if r, err := cur.Record(); err == nil {
									if v, ok := r.Get(identityCol.Name); ok && v.Type == storage.TypeInt && !v.IsNull && v.IVal > maxID {
										maxID = v.IVal
									}
								}
							}
							cur.Close()
						}
						nextID = maxID + 1
					}
					f.executor.SetLastIdentity(tableName, nextID)
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
			if len(data.IntoVars) > 0 {
				for idx, outCol := range data.OutputCols {
					if idx < len(data.IntoVars) {
						intoVar := data.IntoVars[idx]
						if val, ok := rec.Get(outCol); ok {
							f.setVar(intoVar, val)
						} else if tableSchema != nil {
							if colSchema, _, found := tableSchema.FindColumn(outCol); found {
								if val, ok := rec.Get(colSchema.Name); ok {
									f.setVar(intoVar, val)
								}
							}
						}
					}
				}
			} else {
				row := make(map[string]any, len(data.OutputCols))
				vals := make([]any, len(data.OutputCols))
				cols := make([]string, len(data.OutputCols))
				for idx, outCol := range data.OutputCols {
					cols[idx] = outCol
					var val storage.Value
					if v, ok := rec.Get(outCol); ok {
						val = v
					} else if tableSchema != nil {
						if colSchema, _, found := tableSchema.FindColumn(outCol); found {
							if v, ok := rec.Get(colSchema.Name); ok {
								val = v
							}
						}
					}
					anyVal := valToAny(val)
					row[outCol] = anyVal
					vals[idx] = anyVal
				}
				f.rows = append(f.rows, row)
				f.values = append(f.values, vals)
				f.columns = cols
				f.rowCount = int64(len(f.rows))
			}
		}
		if hasTriggers {
			if err := f.fireTriggers(targetTable, "INSERT", []map[string]any{rowMap}, nil); err != nil {
				return nil, err
			}
		}

	case proc.OpUpdate:
		data := f.aux(&inst).(*proc.DMLData)
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
		if !isTransientTable(targetTable) && tableSchema != nil && !hasTriggers {
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
					if len(data.IntoVars) > 0 {
						for idx, outCol := range data.OutputCols {
							if idx < len(data.IntoVars) {
								intoVar := data.IntoVars[idx]
								if _, colIdx, ok := tableSchema.FindColumn(outCol); ok && colIdx >= 0 && colIdx < len(updatedRow.Values) {
									f.setVar(intoVar, updatedRow.Values[colIdx])
								}
							}
						}
					} else {
						row := make(map[string]any, len(data.OutputCols))
						vals := make([]any, len(data.OutputCols))
						cols := make([]string, len(data.OutputCols))
						for idx, outCol := range data.OutputCols {
							cols[idx] = outCol
							var stVal storage.Value
							if _, colIdx, ok := tableSchema.FindColumn(outCol); ok && colIdx >= 0 && colIdx < len(updatedRow.Values) {
								stVal = updatedRow.Values[colIdx]
							}
							anyVal := valToAny(stVal)
							row[outCol] = anyVal
							vals[idx] = anyVal
						}
						f.rows = append(f.rows, row)
						f.values = append(f.values, vals)
						f.columns = cols
						f.rowCount = int64(len(f.rows))
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
			if len(data.IntoVars) > 0 {
				for idx, outCol := range data.OutputCols {
					if idx < len(data.IntoVars) {
						intoVar := data.IntoVars[idx]
						if v, ok := merged.Get(outCol); ok {
							f.setVar(intoVar, v)
						} else if tableSchema != nil {
							if colSchema, _, found := tableSchema.FindColumn(outCol); found {
								if v, ok := merged.Get(colSchema.Name); ok {
									f.setVar(intoVar, v)
								}
							}
						}
					}
				}
			} else {
				row := make(map[string]any, len(data.OutputCols))
				vals := make([]any, len(data.OutputCols))
				cols := make([]string, len(data.OutputCols))
				for idx, outCol := range data.OutputCols {
					cols[idx] = outCol
					var stVal storage.Value
					if v, ok := merged.Get(outCol); ok {
						stVal = v
					} else if tableSchema != nil {
						if colSchema, _, found := tableSchema.FindColumn(outCol); found {
							if v, ok := merged.Get(colSchema.Name); ok {
								stVal = v
							}
						}
					}
					anyVal := valToAny(stVal)
					row[outCol] = anyVal
					vals[idx] = anyVal
				}
				f.rows = append(f.rows, row)
				f.values = append(f.values, vals)
				f.columns = cols
				f.rowCount = int64(len(f.rows))
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
		data := f.aux(&inst).(*proc.DMLData)
		cnt, err := f.execUpdateWhere(data)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.rowCount = cnt

	case proc.OpDelete:
		data := f.aux(&inst).(*proc.DMLData)
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
			if len(data.IntoVars) > 0 {
				for idx, outCol := range data.OutputCols {
					if idx < len(data.IntoVars) {
						intoVar := data.IntoVars[idx]
						if v, ok := oldRec.Get(outCol); ok {
							f.setVar(intoVar, v)
						}
					}
				}
			} else {
				row := make(map[string]any, len(data.OutputCols))
				vals := make([]any, len(data.OutputCols))
				cols := make([]string, len(data.OutputCols))
				for idx, outCol := range data.OutputCols {
					cols[idx] = outCol
					var stVal storage.Value
					if v, ok := oldRec.Get(outCol); ok {
						stVal = v
					}
					anyVal := valToAny(stVal)
					row[outCol] = anyVal
					vals[idx] = anyVal
				}
				f.rows = append(f.rows, row)
				f.values = append(f.values, vals)
				f.columns = cols
				f.rowCount = int64(len(f.rows))
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
		data := f.aux(&inst).(*proc.DMLData)
		cnt, err := f.execDeleteWhere(data)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.rowCount = cnt

	case proc.OpTruncate:
		tableName := f.prog.Constants[inst.A].String()
		if err := f.execTruncate(tableName); err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}

	case proc.OpSelectInto:
		data := f.aux(&inst).(*proc.SelectIntoData)
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

	case proc.OpPointSelect:
		data := f.aux(&inst).(*proc.PointSelectData)
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
					if strings.HasPrefix(p.Alias, "@") {
						if v, ok := lastRow[p.Alias]; ok {
							f.setVar(p.Alias, toStorageValue(v))
						}
					}
				}
			}
			if len(data.Projections) > 0 {
				allVars := true
				for _, p := range data.Projections {
					if !strings.HasPrefix(p.Alias, "@") {
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
				if !strings.HasPrefix(p.Alias, "@") {
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
			f.cursors[cleanSymbol(data.Name)] = &cursorState{
				name:      cleanSymbol(data.Name),
				table:     f.resolveTable(data.Table),
				columns:   data.Columns,
				whereExpr: data.WhereExpr,
				orderBy:   data.OrderBy,
				idx:       0,
				isOpen:    false,
			}
		case string:
			curName := cleanSymbol(data)
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
		data, ok := f.aux(&inst).(*proc.CursorData)
		if !ok || data == nil {
			return nil, fmt.Errorf("invalid cursor fetch metadata")
		}
		curName := cleanSymbol(data.Name)
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

	case proc.OpCursorClose:
		rawName, ok := f.aux(&inst).(string)
		if !ok {
			return nil, fmt.Errorf("invalid cursor close target")
		}
		curName := cleanSymbol(rawName)
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
		curName := cleanSymbol(rawName)
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
		callData := f.aux(&inst).(*proc.CallData)
		res, err := f.callUDF(callData.Name, callData.ArgRegs)
		if err != nil {
			if catchErr := f.handleCatch(err, &pc); catchErr != nil {
				return nil, catchErr
			}
			continue vmLoop
		}
		f.regs[inst.A] = res

	case proc.OpCallProc:
		callData := f.aux(&inst).(*proc.CallData)
		retVal, err := f.callProc(callData.Name, callData.ArgRegs)
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

		if len(f.tableVars) > 0 {
			for varName, physTable := range f.tableVars {
				sql = sqlutil.SubstituteParamOutsideQuotes(sql, "@"+strings.TrimPrefix(varName, "@"), "["+physTable+"]")
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
		}
		if inst.A >= 0 && int(inst.A) < len(f.regs) {
			f.regs[inst.A] = storage.IntValue(int64(len(rows)))
		}

	case proc.OpIndexScan:
		data := f.aux(&inst).(*proc.IndexScanData)
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
		defer cursor.Close()

		var rows []map[string]any
		var cols []string
		if len(data.Projections) > 0 {
			cols = data.Projections
		}

		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				if catchErr := f.handleCatch(fmt.Errorf("OpIndexScan record error on %s: %w", data.Table, err), &pc); catchErr != nil {
					return nil, catchErr
				}
				continue vmLoop
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
		data := f.aux(&inst).(*proc.ResultRowData)
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
			if int(regIdx) < len(f.regs) {
				val = valToAny(f.regs[regIdx])
			}
			vals[i] = val
			colName := fmt.Sprintf("col%d", i+1)
			if int(i) < len(data.Columns) && data.Columns[i] != "" {
				colName = data.Columns[i]
			}
			row[colName] = val
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

