package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/execution"
	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

// ExecutionResult captures the output and metrics of a procedure, function, or query execution.
type ExecutionResult struct {
	ReturnValue  any
	Logs         []string
	Duration     time.Duration
	Rows         []map[string]any // Relational result tuples for OpRelQuery
	Columns      []string         // Result projection column names
	RowsAffected int64
	LastIdentity int64
}

// TriggerEntry holds metadata and precompiled bytecode for a trigger.
type TriggerEntry struct {
	Name      string
	TableName string
	Event     string // "INSERT", "UPDATE", "DELETE"
	Program   *proc.Program
}

// UDFEntry holds metadata and precompiled bytecode for a user-defined function.
type UDFEntry struct {
	Name    string
	Program *proc.Program
}

// PlanCache stores precompiled native T-SQL bytecode programs for zero-recompile execution.
type PlanCache struct {
	mu       sync.RWMutex
	programs map[string]*proc.Program
	byName   map[string]*proc.Program
}

// NewPlanCache creates an execution plan cache for bytecode programs.
func NewPlanCache() *PlanCache {
	return &PlanCache{
		programs: make(map[string]*proc.Program),
		byName:   make(map[string]*proc.Program),
	}
}

// GetOrCompile retrieves a cached bytecode program or compiles and caches the T-SQL script.
func (c *PlanCache) GetOrCompile(script string) (*proc.Program, error) {
	h := hashScript(script)

	c.mu.RLock()
	if prog, exists := c.programs[h]; exists {
		c.mu.RUnlock()
		return prog, nil
	}
	c.mu.RUnlock()

	prog, err := proc.Compile(script)
	if err != nil {
		return nil, fmt.Errorf("bytecode compilation error: %w", err)
	}

	c.mu.Lock()
	c.programs[h] = prog
	c.byName[cleanSymbol(prog.Name)] = prog
	c.mu.Unlock()

	return prog, nil
}

// PutByName stores a compiled bytecode program indexed by procedure or function name.
func (c *PlanCache) PutByName(name string, prog *proc.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName[cleanSymbol(name)] = prog
}

// GetByName retrieves a compiled bytecode program by procedure or function name.
func (c *PlanCache) GetByName(name string) (*proc.Program, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	prog, ok := c.byName[cleanSymbol(name)]
	return prog, ok
}

// Executor manages execution of compiled stored procedures, functions, and triggers within ambient ACID transactions.
type Executor struct {
	cache       *PlanCache
	mu          sync.RWMutex
	udfs        map[string]*UDFEntry
	triggers    map[string]map[string][]*TriggerEntry // table (lower) -> event (upper) -> entries
	regPool     sync.Pool
	tableGetter execution.TableGetter
}

// NewExecutor creates a procedure and trigger executor.
func NewExecutor(cache *PlanCache) *Executor {
	if cache == nil {
		cache = NewPlanCache()
	}
	return &Executor{
		cache:    cache,
		udfs:     make(map[string]*UDFEntry),
		triggers: make(map[string]map[string][]*TriggerEntry),
		regPool: sync.Pool{
			New: func() any {
				r := make([]storage.Value, 64)
				return &r
			},
		},
	}
}

// SetTableGetter configures the schema lookup function for relational queries.
func (e *Executor) SetTableGetter(tg execution.TableGetter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tableGetter = tg
}

func (e *Executor) getTableGetter() execution.TableGetter {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.tableGetter
}

// Cache returns the underlying bytecode PlanCache.
func (e *Executor) Cache() *PlanCache {
	return e.cache
}

// RegisterUDF registers a user-defined function for preloading into execution runtimes.
func (e *Executor) RegisterUDF(name string, prog *proc.Program) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sym := cleanSymbol(name)
	e.udfs[sym] = &UDFEntry{
		Name:    sym,
		Program: prog,
	}
}

// RegisterTrigger registers a trigger for the specified table and event.
func (e *Executor) RegisterTrigger(name string, tableName string, event string, prog *proc.Program) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sym := cleanSymbol(name)
	tbl := strings.ToLower(tableName)
	evt := strings.ToUpper(event)

	if e.triggers[tbl] == nil {
		e.triggers[tbl] = make(map[string][]*TriggerEntry)
	}

	entry := &TriggerEntry{
		Name:      sym,
		TableName: tbl,
		Event:     evt,
		Program:   prog,
	}

	existing := e.triggers[tbl][evt]
	found := false
	for i, t := range existing {
		if t.Name == sym {
			existing[i] = entry
			found = true
			break
		}
	}
	if !found {
		e.triggers[tbl][evt] = append(existing, entry)
	}
}

// GetTriggers retrieves all registered triggers for a table and event.
func (e *Executor) GetTriggers(tableName string, event string) []*TriggerEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tbl := strings.ToLower(tableName)
	evt := strings.ToUpper(event)

	if tblMap, ok := e.triggers[tbl]; ok {
		if trgs, ok := tblMap[evt]; ok {
			result := make([]*TriggerEntry, len(trgs))
			copy(result, trgs)
			return result
		}
	}
	return nil
}

// VMFrame maintains the execution context for a bytecode invocation.
type VMFrame struct {
	executor        *Executor
	tx              storage.Transaction
	prog            *proc.Program
	regs            []storage.Value
	vars            map[string]storage.Value
	cursors         map[string]*cursorState
	transientTables map[string][]map[string]any
	catchStack      []int
	logs            []string
	rows            []map[string]any
	columns         []string
	rowCount        int64
	fetchStatus     int
	tranCount       int
	lastIdentity    int64
	lastError       int
}

type cursorState struct {
	name      string
	table     string
	columns   []string
	whereExpr proc.Expr
	rows      []storage.Record
	idx       int
}

// Run executes a precompiled bytecode program with ambient transaction semantics.
func (e *Executor) Run(prog *proc.Program, procName string, params map[string]any, session storage.Session) (*ExecutionResult, error) {
	start := time.Now()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to begin ambient transaction: %w", err)
	}

	frame := e.newFrame(prog, tx)
	defer e.releaseFrame(frame)

	// Bind parameters
	for k, v := range params {
		clean := cleanSymbol(k)
		stVal := toStorageValue(v)
		frame.vars[clean] = stVal
		frame.vars[strings.ToLower(clean)] = stVal
	}

	retVal, err := frame.Execute(0)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("execution error (rolled back): %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit ambient transaction: %w", err)
	}

	return &ExecutionResult{
		ReturnValue:  retVal,
		Logs:         frame.logs,
		Duration:     time.Since(start),
		Rows:         frame.rows,
		Columns:      frame.columns,
		RowsAffected: frame.rowCount,
		LastIdentity: frame.lastIdentity,
	}, nil
}

// RunInTx executes a program within an existing transaction context.
func (e *Executor) RunInTx(prog *proc.Program, params map[string]any, tx storage.Transaction, transientTables map[string][]map[string]any) (any, []string, error) {
	frame := e.newFrame(prog, tx)
	defer e.releaseFrame(frame)

	if transientTables != nil {
		for k, v := range transientTables {
			frame.transientTables[k] = v
		}
	}

	for k, v := range params {
		clean := cleanSymbol(k)
		stVal := toStorageValue(v)
		frame.vars[clean] = stVal
		frame.vars[strings.ToLower(clean)] = stVal
	}

	retVal, err := frame.Execute(0)
	return retVal, frame.logs, err
}

// ExecuteInTx executes a program within an existing transaction context and returns an ExecutionResult with Rows and Columns.
func (e *Executor) ExecuteInTx(prog *proc.Program, params map[string]any, tx storage.Transaction, transientTables map[string][]map[string]any) (*ExecutionResult, error) {
	start := time.Now()
	frame := e.newFrame(prog, tx)
	defer e.releaseFrame(frame)

	if transientTables != nil {
		for k, v := range transientTables {
			frame.transientTables[k] = v
		}
	}

	for k, v := range params {
		clean := cleanSymbol(k)
		stVal := toStorageValue(v)
		frame.vars[clean] = stVal
		frame.vars[strings.ToLower(clean)] = stVal
	}

	retVal, err := frame.Execute(0)
	if err != nil {
		return nil, err
	}

	return &ExecutionResult{
		ReturnValue:  retVal,
		Logs:         frame.logs,
		Duration:     time.Since(start),
		Rows:         frame.rows,
		Columns:      frame.columns,
		RowsAffected: frame.rowCount,
		LastIdentity: frame.lastIdentity,
	}, nil
}

func (e *Executor) newFrame(prog *proc.Program, tx storage.Transaction) *VMFrame {
	numRegs := int(prog.NumRegisters) + 16
	if numRegs < 64 {
		numRegs = 64
	}

	regPtr := e.regPool.Get().(*[]storage.Value)
	if len(*regPtr) < numRegs {
		*regPtr = make([]storage.Value, numRegs)
	}
	regs := (*regPtr)[:numRegs]
	for i := range regs {
		regs[i] = storage.NullValue(storage.TypeUnknown)
	}

	return &VMFrame{
		executor:        e,
		tx:              tx,
		prog:            prog,
		regs:            regs,
		vars:            make(map[string]storage.Value),
		cursors:         make(map[string]*cursorState),
		transientTables: make(map[string][]map[string]any),
		catchStack:      make([]int, 0),
		logs:            make([]string, 0),
		fetchStatus:     -1,
	}
}

func (e *Executor) releaseFrame(frame *VMFrame) {
	r := frame.regs
	e.regPool.Put(&r)
}

// Execute runs the VM instruction loop starting at startPC.
func (f *VMFrame) Execute(startPC int) (any, error) {
	instructions := f.prog.Instructions
	pc := startPC

	for pc < len(instructions) {
		inst := instructions[pc]
		pc++

		switch inst.Op {
		case proc.OpHalt:
			return nil, nil

		case proc.OpReturn:
			if inst.A >= 0 && int(inst.A) < len(f.regs) {
				return valToAny(f.regs[inst.A]), nil
			}
			return nil, nil

		case proc.OpLoadConst:
			if int(inst.B) < len(f.prog.Constants) {
				f.regs[inst.A] = f.prog.Constants[inst.B]
			}

		case proc.OpLoadVar:
			varName := inst.Data.(string)
			v, found := f.getVar(varName)
			if !found {
				v = storage.StringValue(varName)
			}
			f.regs[inst.A] = v

		case proc.OpStoreVar:
			varName := inst.Data.(string)
			f.setVar(varName, f.regs[inst.A])

		case proc.OpCopy:
			f.regs[inst.A] = f.regs[inst.B]

		case proc.OpJump:
			pc = int(inst.B)

		case proc.OpJumpIfFalse:
			if !isTruthy(f.regs[inst.A]) {
				pc = int(inst.B)
			}

		case proc.OpJumpIfTrue:
			if isTruthy(f.regs[inst.A]) {
				pc = int(inst.B)
			}

		case proc.OpPushCatch:
			f.catchStack = append(f.catchStack, int(inst.B))

		case proc.OpPopCatch:
			if len(f.catchStack) > 0 {
				f.catchStack = f.catchStack[:len(f.catchStack)-1]
			}

		case proc.OpThrow, proc.OpRaiserror:
			f.lastError = 50000
			msg := fmt.Sprintf("%v", inst.Data)
			if len(f.catchStack) > 0 {
				catchPC := f.catchStack[len(f.catchStack)-1]
				f.catchStack = f.catchStack[:len(f.catchStack)-1]
				pc = catchPC
				continue
			}
			return nil, fmt.Errorf("%s", msg)

		case proc.OpPrint:
			v := f.regs[inst.A]
			f.logs = append(f.logs, valToString(v))

		case proc.OpAdd:
			f.regs[inst.A] = storageAdd(f.regs[inst.B], f.regs[inst.C])

		case proc.OpSub:
			f.regs[inst.A] = storageSub(f.regs[inst.B], f.regs[inst.C])

		case proc.OpMul:
			f.regs[inst.A] = storageMul(f.regs[inst.B], f.regs[inst.C])

		case proc.OpDiv:
			res, err := storageDiv(f.regs[inst.B], f.regs[inst.C])
			if err != nil {
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, err
			}
			f.regs[inst.A] = res

		case proc.OpNeg:
			f.regs[inst.A] = storageNeg(f.regs[inst.B])

		case proc.OpNot:
			f.regs[inst.A] = storage.BoolValue(!isTruthy(f.regs[inst.B]))

		case proc.OpEq:
			if inst.Data == "AND" {
				f.regs[inst.A] = storage.BoolValue(isTruthy(f.regs[inst.B]) && isTruthy(f.regs[inst.C]))
			} else if inst.Data == "OR" {
				f.regs[inst.A] = storage.BoolValue(isTruthy(f.regs[inst.B]) || isTruthy(f.regs[inst.C]))
			} else {
				f.regs[inst.A] = storage.BoolValue(storageEq(f.regs[inst.B], f.regs[inst.C]))
			}

		case proc.OpNeq:
			f.regs[inst.A] = storage.BoolValue(!storageEq(f.regs[inst.B], f.regs[inst.C]))

		case proc.OpLt:
			f.regs[inst.A] = storage.BoolValue(storageLt(f.regs[inst.B], f.regs[inst.C]))

		case proc.OpLte:
			f.regs[inst.A] = storage.BoolValue(storageLte(f.regs[inst.B], f.regs[inst.C]))

		case proc.OpGt:
			f.regs[inst.A] = storage.BoolValue(storageGt(f.regs[inst.B], f.regs[inst.C]))

		case proc.OpGte:
			f.regs[inst.A] = storage.BoolValue(storageGte(f.regs[inst.B], f.regs[inst.C]))

		case proc.OpLike:
			str := valToString(f.regs[inst.B])
			pat := valToString(f.regs[inst.C])
			matched := matchLike(str, pat)
			if not, ok := inst.Data.(bool); ok && not {
				matched = !matched
			}
			f.regs[inst.A] = storage.BoolValue(matched)

		case proc.OpBetween:
			v := f.regs[inst.B]
			low := f.regs[inst.C]
			meta := inst.Data.(struct {
				HighReg int32
				Not     bool
			})
			high := f.regs[meta.HighReg]
			inRange := storageGte(v, low) && storageLte(v, high)
			if meta.Not {
				inRange = !inRange
			}
			f.regs[inst.A] = storage.BoolValue(inRange)

		case proc.OpIn:
			val := f.regs[inst.B]
			callData := inst.Data.(*proc.CallData)
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

		case proc.OpTxBegin:
			f.tranCount++

		case proc.OpTxCommit:
			if f.tranCount > 0 {
				f.tranCount--
			}
			if err := f.tx.Commit(); err != nil {
				return nil, err
			}

		case proc.OpTxRollback:
			if f.tranCount > 0 {
				f.tranCount--
			}
			if err := f.tx.Rollback(); err != nil {
				return nil, err
			}

		case proc.OpInsert:
			data := inst.Data.(*proc.DMLData)
			rec := make(storage.Record)
			rowMap := make(map[string]any)
			evalCtx := f.buildRowEvalMap(nil)

			cols := data.Columns
			if tg := f.executor.getTableGetter(); tg != nil {
				if schema, err := tg(data.Table); err == nil && schema != nil {
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
								return nil, fmt.Errorf("Cannot insert explicit value for identity column in table %q when IDENTITY_INSERT is set to OFF", data.Table)
							}
						}
					}
				}
			}

			for i, col := range cols {
				if i < len(data.ValExprs) {
					vAny, _ := proc.EvalRowExpr(data.ValExprs[i], evalCtx, nil)
					stVal := toStorageValue(vAny)
					rec[col] = stVal
					rowMap[col] = valToAny(stVal)
					if (strings.EqualFold(col, "id") || strings.HasSuffix(strings.ToLower(col), "id")) && stVal.Type == storage.TypeInt && !stVal.IsNull {
						f.lastIdentity = stVal.IVal
					}
				}
			}

			if err := f.tx.Insert(data.Table, rec); err != nil {
				f.lastError = 50000
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, fmt.Errorf("insert error on %s: %w", data.Table, err)
			}
			f.rowCount++
			if err := f.fireTriggers(data.Table, "INSERT", []map[string]any{rowMap}, nil); err != nil {
				return nil, err
			}

		case proc.OpUpdate:
			data := inst.Data.(*proc.DMLData)
			evalCtx := f.buildRowEvalMap(nil)
			pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
			pkVal := toStorageValue(pkAny)

			var oldRec storage.Record
			if !isTransientTable(data.Table) {
				oldRec, _ = f.tx.Get(data.Table, pkVal)
			}

			rowCtx := f.buildRowEvalMap(recordToMap(oldRec))
			rec := make(storage.Record)
			for _, a := range data.Assignments {
				valAny, _ := proc.EvalRowExpr(a.Value, rowCtx, nil)
				rec[a.Column] = toStorageValue(valAny)
			}

			if err := f.tx.Update(data.Table, pkVal, rec); err != nil {
				f.lastError = 50000
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, fmt.Errorf("update error on %s: %w", data.Table, err)
			}
			f.rowCount = 1

			if oldRec != nil {
				oldRow := recordToMap(oldRec)
				newRow := recordToMap(oldRec)
				for k, v := range rec {
					newRow[k] = valToAny(v)
				}
				if err := f.fireTriggers(data.Table, "UPDATE", []map[string]any{newRow}, []map[string]any{oldRow}); err != nil {
					return nil, err
				}
			}

		case proc.OpUpdateWhere:
			data := inst.Data.(*proc.DMLData)
			cnt, err := f.execUpdateWhere(data)
			if err != nil {
				f.lastError = 50000
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, err
			}
			f.rowCount = cnt

		case proc.OpDelete:
			data := inst.Data.(*proc.DMLData)
			evalCtx := f.buildRowEvalMap(nil)
			pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
			pkVal := toStorageValue(pkAny)

			var oldRec storage.Record
			if !isTransientTable(data.Table) {
				oldRec, _ = f.tx.Get(data.Table, pkVal)
			}

			if err := f.tx.Delete(data.Table, pkVal); err != nil {
				f.lastError = 50000
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, fmt.Errorf("delete error on %s: %w", data.Table, err)
			}
			f.rowCount = 1

			if oldRec != nil {
				oldRow := recordToMap(oldRec)
				if err := f.fireTriggers(data.Table, "DELETE", nil, []map[string]any{oldRow}); err != nil {
					return nil, err
				}
			}

		case proc.OpDeleteWhere:
			data := inst.Data.(*proc.DMLData)
			cnt, err := f.execDeleteWhere(data)
			if err != nil {
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, err
			}
			f.rowCount = cnt

		case proc.OpSelectInto:
			data := inst.Data.(*proc.SelectIntoData)
			val, err := f.execSelectInto(data)
			if err != nil {
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, err
			}
			if data.IntoVar != "" {
				f.setVar(data.IntoVar, val)
			}
			if inst.A >= 0 && int(inst.A) < len(f.regs) {
				f.regs[inst.A] = val
			}

		case proc.OpCursorOpen:
			switch data := inst.Data.(type) {
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
						return nil, err
					}
					cur.rows = rows
					cur.idx = 0
				}
			}

		case proc.OpCursorFetch:
			data := inst.Data.(*proc.CursorData)
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
			curName := cleanSymbol(inst.Data.(string))
			if cur, exists := f.cursors[curName]; exists {
				cur.idx = len(cur.rows)
			}

		case proc.OpCursorDealloc:
			curName := cleanSymbol(inst.Data.(string))
			delete(f.cursors, curName)

		case proc.OpCallBuiltin:
			callData := inst.Data.(*proc.CallData)
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
			} else {
				f.regs[inst.A] = proc.EvalBuiltin(callData.Name, args)
			}

		case proc.OpCallUDF:
			callData := inst.Data.(*proc.CallData)
			res, err := f.callUDF(callData.Name, callData.ArgRegs)
			if err != nil {
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, err
			}
			f.regs[inst.A] = res

		case proc.OpCallProc:
			callData := inst.Data.(*proc.CallData)
			if err := f.callProc(callData.Name, callData.ArgRegs); err != nil {
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, err
			}

		case proc.OpRelQuery:
			var queryData *proc.RelQueryData
			if data, ok := inst.Data.(*proc.RelQueryData); ok {
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

			rows, cols, err := f.execRelQuery(sql, queryData)
			if err != nil {
				f.lastError = 50000
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, err
			}

			f.rows = rows
			f.columns = cols
			f.rowCount = int64(len(rows))

			if queryData != nil && queryData.IntoVar != "" && len(rows) > 0 {
				for _, v := range rows[0] {
					f.setVar(queryData.IntoVar, toStorageValue(v))
					break
				}
			}
			if inst.A >= 0 && int(inst.A) < len(f.regs) {
				f.regs[inst.A] = storage.IntValue(int64(len(rows)))
			}

		case proc.OpIndexScan:
			data := inst.Data.(*proc.IndexScanData)
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
				f.lastError = 50000
				if len(f.catchStack) > 0 {
					catchPC := f.catchStack[len(f.catchStack)-1]
					f.catchStack = f.catchStack[:len(f.catchStack)-1]
					pc = catchPC
					continue
				}
				return nil, fmt.Errorf("OpIndexScan on %s (%s) error: %w", data.Table, data.IndexName, err)
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
				for _, v := range rows[0] {
					f.setVar(data.IntoVar, toStorageValue(v))
					break
				}
			}
			if inst.A >= 0 && int(inst.A) < len(f.regs) {
				f.regs[inst.A] = storage.IntValue(int64(len(rows)))
			}
		}
	}

	return nil, nil
}

func (f *VMFrame) bindWhereVariables(where string) string {
	if where == "" || len(f.vars) == 0 {
		return where
	}
	res := where
	for k, v := range f.vars {
		varName := "@" + strings.TrimPrefix(k, "@")
		valStr := v.String()
		if v.Type == storage.TypeVarChar {
			valStr = "'" + strings.ReplaceAll(v.SVal, "'", "''") + "'"
		}
		res = strings.ReplaceAll(res, varName, valStr)
	}
	return res
}

func (f *VMFrame) execRelQuery(sql string, queryData *proc.RelQueryData) ([]map[string]any, []string, error) {
	p := planner.NewPlanner()
	qInfo, err := p.ParseSelect(sql)
	if err != nil {
		return nil, nil, fmt.Errorf("planner parse error in OpRelQuery: %w", err)
	}

	if qInfo.WhereClause != "" {
		qInfo.WhereClause = f.bindWhereVariables(qInfo.WhereClause)
	}

	getTable := f.executor.getTableGetter()
	if getTable == nil {
		return nil, nil, fmt.Errorf("no table getter configured for relational queries")
	}

	leftTable := cleanSymbol(qInfo.TableName)
	leftSchema, err := getTable(leftTable)
	if err != nil {
		return nil, nil, fmt.Errorf("table %q not found: %w", leftTable, err)
	}

	rightSchemas := make(map[string]*storage.TableSchema)
	var joins []*planner.JoinInfo
	if len(qInfo.Joins) > 0 {
		joins = qInfo.Joins
	} else if qInfo.Join != nil {
		joins = []*planner.JoinInfo{qInfo.Join}
	}

	for _, j := range joins {
		rightTable := cleanSymbol(j.Table)
		rSchema, err := getTable(rightTable)
		if err != nil {
			return nil, nil, fmt.Errorf("joined table %q not found: %w", rightTable, err)
		}
		rightSchemas[j.Table] = rSchema
		rightSchemas[rightTable] = rSchema
		if j.Alias != "" {
			rightSchemas[j.Alias] = rSchema
			rightSchemas[cleanSymbol(j.Alias)] = rSchema
		}
	}

	plan := p.BuildMultiJoinPlanWithStats(qInfo, leftSchema, nil, rightSchemas, nil)

	opts := execution.DefaultExecutorOptions()
	exec, err := execution.BuildExecutorWithOptions(plan, f.tx, getTable, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build volcano executor: %w", err)
	}

	ctx := context.Background()
	if err := exec.Open(ctx); err != nil {
		return nil, nil, fmt.Errorf("volcano execution open error: %w", err)
	}
	defer exec.Close()

	var rows []map[string]any
	for {
		tuple, err := exec.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, tuple.ToMap())
	}

	cols := exec.Schema()
	if len(cols) == 0 && len(rows) > 0 {
		for k := range rows[0] {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}

	return rows, cols, nil
}

func (f *VMFrame) getVar(name string) (storage.Value, bool) {
	clean := cleanSymbol(name)
	if strings.EqualFold(name, "@@ROWCOUNT") || strings.EqualFold(clean, "ROWCOUNT") || strings.EqualFold(clean, "@ROWCOUNT") {
		return storage.IntValue(f.rowCount), true
	}
	if strings.EqualFold(name, "@@FETCH_STATUS") || strings.EqualFold(clean, "FETCH_STATUS") || strings.EqualFold(clean, "@FETCH_STATUS") {
		return storage.IntValue(int64(f.fetchStatus)), true
	}
	if strings.EqualFold(name, "@@TRANCOUNT") || strings.EqualFold(clean, "TRANCOUNT") || strings.EqualFold(clean, "@TRANCOUNT") {
		return storage.IntValue(int64(f.tranCount)), true
	}
	if strings.EqualFold(name, "@@SPID") || strings.EqualFold(clean, "SPID") || strings.EqualFold(clean, "@SPID") {
		return storage.IntValue(50), true
	}
	if strings.EqualFold(name, "@@ERROR") || strings.EqualFold(clean, "ERROR") || strings.EqualFold(clean, "@ERROR") || strings.EqualFold(name, "ERROR_NUMBER()") || strings.EqualFold(clean, "ERROR_NUMBER") {
		return storage.IntValue(int64(f.lastError)), true
	}
	if strings.EqualFold(name, "@@SERVERNAME") || strings.EqualFold(clean, "SERVERNAME") || strings.EqualFold(clean, "@SERVERNAME") {
		return storage.StringValue("SQLION"), true
	}
	if strings.EqualFold(name, "@@VERSION") || strings.EqualFold(clean, "VERSION") || strings.EqualFold(clean, "@VERSION") {
		return storage.StringValue("Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) - SQLion Engine"), true
	}
	if strings.EqualFold(name, "@@IDENTITY") || strings.EqualFold(clean, "IDENTITY") || strings.EqualFold(clean, "@IDENTITY") || strings.EqualFold(name, "SCOPE_IDENTITY") || strings.EqualFold(clean, "SCOPE_IDENTITY") {
		if f.lastIdentity > 0 {
			return storage.IntValue(f.lastIdentity), true
		}
		return storage.NullValue(storage.TypeInt), true
	}

	if v, ok := f.vars[clean]; ok {
		return v, true
	}
	if v, ok := f.vars[strings.ToLower(clean)]; ok {
		return v, true
	}
	return storage.NullValue(storage.TypeUnknown), false
}

func (f *VMFrame) setVar(name string, val storage.Value) {
	clean := cleanSymbol(name)
	f.vars[clean] = val
	f.vars[strings.ToLower(clean)] = val
}

func (f *VMFrame) fireTriggers(tableName, event string, inserted, deleted []map[string]any) error {
	if isTransientTable(tableName) {
		return nil
	}
	trgs := f.executor.GetTriggers(tableName, event)
	if len(trgs) == 0 {
		return nil
	}

	transients := map[string][]map[string]any{
		"inserted": inserted,
		"deleted":  deleted,
	}

	for _, trg := range trgs {
		_, logs, err := f.executor.RunInTx(trg.Program, nil, f.tx, transients)
		if len(logs) > 0 {
			f.logs = append(f.logs, logs...)
		}
		if err != nil {
			return fmt.Errorf("trigger %s error: %w", trg.Name, err)
		}
	}
	return nil
}

func (f *VMFrame) execUpdateWhere(data *proc.DMLData) (int64, error) {
	var cursor storage.Cursor
	var err error

	if idxName, lowExpr, highExpr, ok := f.findMatchingIndex(data.Table, data.WhereExpr); ok {
		evalCtx := f.buildRowEvalMap(nil)
		var lowVal, highVal *storage.Value
		if lowExpr != nil {
			if v, evalErr := proc.EvalRowExpr(lowExpr, evalCtx, nil); evalErr == nil {
				sv := toStorageValue(v)
				lowVal = &sv
			}
		}
		if highExpr != nil {
			if v, evalErr := proc.EvalRowExpr(highExpr, evalCtx, nil); evalErr == nil {
				sv := toStorageValue(v)
				highVal = &sv
			}
		}
		cursor, err = f.tx.IndexScan(data.Table, idxName, lowVal, highVal)
	}

	if cursor == nil || err != nil {
		cursor, err = f.tx.Scan(data.Table)
		if err != nil {
			return 0, fmt.Errorf("updateWhere scan error on %s: %w", data.Table, err)
		}
	}
	defer cursor.Close()

	type updateItem struct {
		pk      storage.Value
		updates storage.Record
		oldRow  map[string]any
		newRow  map[string]any
	}
	var toUpdate []updateItem

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

		var pkVal storage.Value
		var hasPk bool
		if tg := f.executor.getTableGetter(); tg != nil {
			if schema, err := tg(data.Table); err == nil && schema != nil {
				for _, c := range schema.Columns {
					if c.PrimaryKey {
						if v, ok := rec[c.Name]; ok {
							pkVal = v
							hasPk = true
							break
						}
					}
				}
			}
		}
		if !hasPk {
			if v, ok := rec["id"]; ok {
				pkVal = v
				hasPk = true
			} else {
				for _, v := range rec {
					pkVal = v
					hasPk = true
					break
				}
			}
		}

		recUpdates := make(storage.Record)
		newRowMap := make(map[string]any)
		for k, v := range rowMap {
			newRowMap[k] = v
		}

		for _, assign := range data.Assignments {
			rowContext := f.buildRowEvalMap(rowMap)
			val, err := proc.EvalRowExpr(assign.Value, rowContext, nil)
			if err == nil {
				stVal := toStorageValue(val)
				recUpdates[assign.Column] = stVal
				newRowMap[assign.Column] = valToAny(stVal)
			}
		}

		if hasPk {
			toUpdate = append(toUpdate, updateItem{
				pk:      pkVal,
				updates: recUpdates,
				oldRow:  rowMap,
				newRow:  newRowMap,
			})
		}
	}

	updatedCount := int64(0)
	var oldRows []map[string]any
	var newRows []map[string]any
	for _, item := range toUpdate {
		if err := f.tx.Update(data.Table, item.pk, item.updates); err == nil {
			updatedCount++
			oldRows = append(oldRows, item.oldRow)
			newRows = append(newRows, item.newRow)
		}
	}

	if len(newRows) > 0 {
		_ = f.fireTriggers(data.Table, "UPDATE", newRows, oldRows)
	}
	return updatedCount, nil
}

func (f *VMFrame) execDeleteWhere(data *proc.DMLData) (int64, error) {
	var cursor storage.Cursor
	var err error

	if idxName, lowExpr, highExpr, ok := f.findMatchingIndex(data.Table, data.WhereExpr); ok {
		evalCtx := f.buildRowEvalMap(nil)
		var lowVal, highVal *storage.Value
		if lowExpr != nil {
			if v, evalErr := proc.EvalRowExpr(lowExpr, evalCtx, nil); evalErr == nil {
				sv := toStorageValue(v)
				lowVal = &sv
			}
		}
		if highExpr != nil {
			if v, evalErr := proc.EvalRowExpr(highExpr, evalCtx, nil); evalErr == nil {
				sv := toStorageValue(v)
				highVal = &sv
			}
		}
		cursor, err = f.tx.IndexScan(data.Table, idxName, lowVal, highVal)
	}

	if cursor == nil || err != nil {
		cursor, err = f.tx.Scan(data.Table)
		if err != nil {
			return 0, fmt.Errorf("deleteWhere scan error on %s: %w", data.Table, err)
		}
	}
	defer cursor.Close()

	type delItem struct {
		pk  storage.Value
		row map[string]any
	}
	var toDelete []delItem

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

		var pkVal storage.Value
		var hasPk bool
		if tg := f.executor.getTableGetter(); tg != nil {
			if schema, err := tg(data.Table); err == nil && schema != nil {
				for _, c := range schema.Columns {
					if c.PrimaryKey {
						if v, ok := rec[c.Name]; ok {
							pkVal = v
							hasPk = true
							break
						}
					}
				}
			}
		}
		if !hasPk {
			if v, ok := rec["id"]; ok {
				pkVal = v
				hasPk = true
			} else {
				for _, v := range rec {
					pkVal = v
					hasPk = true
					break
				}
			}
		}
		if hasPk {
			toDelete = append(toDelete, delItem{pk: pkVal, row: rowMap})
		}
	}

	deletedCount := int64(0)
	var deletedRows []map[string]any
	for _, item := range toDelete {
		if err := f.tx.Delete(data.Table, item.pk); err == nil {
			deletedCount++
			deletedRows = append(deletedRows, item.row)
		}
	}

	if len(deletedRows) > 0 {
		_ = f.fireTriggers(data.Table, "DELETE", nil, deletedRows)
	}
	return deletedCount, nil
}

func (f *VMFrame) execSelectInto(data *proc.SelectIntoData) (storage.Value, error) {
	lowerTable := strings.ToLower(data.Table)

	if rows, isTransient := f.transientTables[lowerTable]; isTransient {
		for _, r := range rows {
			if data.WhereVal != nil {
				rowContext := f.buildRowEvalMap(r)
				if !proc.EvalBoolExpr(data.WhereVal, rowContext, nil) {
					continue
				}
			}
			if val, ok := r[data.Column]; ok {
				return toStorageValue(val), nil
			}
			for k, v := range r {
				if strings.EqualFold(k, data.Column) {
					return toStorageValue(v), nil
				}
			}
		}
		return storage.NullValue(storage.TypeUnknown), nil
	}

	evalCtx := f.buildRowEvalMap(nil)

	if data.WhereCol != "" && data.WhereVal != nil && strings.EqualFold(data.WhereCol, "id") {
		pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
		pkVal := toStorageValue(pkAny)
		rec, err := f.tx.Get(data.Table, pkVal)
		if err == nil {
			if v, ok := rec[data.Column]; ok {
				return v, nil
			}
			for k, v := range rec {
				if strings.EqualFold(k, data.Column) {
					return v, nil
				}
			}
		}
		return storage.NullValue(storage.TypeUnknown), nil
	}

	cursor, err := f.tx.Scan(data.Table)
	if err != nil {
		return storage.NullValue(storage.TypeUnknown), err
	}
	defer cursor.Close()

	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}
		rowMap := recordToMap(rec)

		if data.WhereVal != nil {
			rowContext := f.buildRowEvalMap(rowMap)
			if !proc.EvalBoolExpr(data.WhereVal, rowContext, nil) {
				continue
			}
		}

		if v, ok := rec[data.Column]; ok {
			return v, nil
		}
		for k, v := range rec {
			if strings.EqualFold(k, data.Column) {
				return v, nil
			}
		}
	}
	return storage.NullValue(storage.TypeUnknown), nil
}

func (f *VMFrame) findMatchingIndex(tableName string, whereExpr proc.Expr) (string, proc.Expr, proc.Expr, bool) {
	if whereExpr == nil {
		return "", nil, nil, false
	}
	tg := f.executor.getTableGetter()
	if tg == nil {
		return "", nil, nil, false
	}
	schema, err := tg(tableName)
	if err != nil || schema == nil || len(schema.Indexes) == 0 {
		return "", nil, nil, false
	}

	switch e := whereExpr.(type) {
	case *proc.BinaryExpr:
		if e.Op == proc.TokenEq {
			var colName string
			var valExpr proc.Expr
			if leftCol, ok := e.Left.(*proc.ColumnRefExpr); ok {
				colName = cleanSymbol(leftCol.Name)
				valExpr = e.Right
			} else if rightCol, ok := e.Right.(*proc.ColumnRefExpr); ok {
				colName = cleanSymbol(rightCol.Name)
				valExpr = e.Left
			}
			if colName != "" && valExpr != nil {
				for _, idx := range schema.Indexes {
					if len(idx.Columns) > 0 && strings.EqualFold(idx.Columns[0], colName) {
						return idx.Name, valExpr, valExpr, true
					}
				}
			}
		}
	case *proc.BetweenExpr:
		if colRef, ok := e.Expr.(*proc.ColumnRefExpr); ok {
			colName := cleanSymbol(colRef.Name)
			for _, idx := range schema.Indexes {
				if len(idx.Columns) > 0 && strings.EqualFold(idx.Columns[0], colName) {
					return idx.Name, e.Low, e.High, true
				}
			}
		}
	}

	return "", nil, nil, false
}

func (f *VMFrame) scanCursorRows(tableName string, whereExpr proc.Expr) ([]storage.Record, error) {
	var cursor storage.Cursor
	var err error

	if idxName, lowExpr, highExpr, ok := f.findMatchingIndex(tableName, whereExpr); ok {
		evalCtx := f.buildRowEvalMap(nil)
		var lowVal, highVal *storage.Value
		if lowExpr != nil {
			if v, evalErr := proc.EvalRowExpr(lowExpr, evalCtx, nil); evalErr == nil {
				sv := toStorageValue(v)
				lowVal = &sv
			}
		}
		if highExpr != nil {
			if v, evalErr := proc.EvalRowExpr(highExpr, evalCtx, nil); evalErr == nil {
				sv := toStorageValue(v)
				highVal = &sv
			}
		}
		cursor, err = f.tx.IndexScan(tableName, idxName, lowVal, highVal)
	}

	if cursor == nil || err != nil {
		cursor, err = f.tx.Scan(tableName)
		if err != nil {
			return nil, err
		}
	}
	defer cursor.Close()

	var result []storage.Record
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			break
		}
		if whereExpr != nil {
			rowContext := f.buildRowEvalMap(recordToMap(rec))
			if !proc.EvalBoolExpr(whereExpr, rowContext, nil) {
				continue
			}
		}
		cp := make(storage.Record)
		for k, v := range rec {
			cp[k] = v
		}
		result = append(result, cp)
	}
	return result, nil
}

func (f *VMFrame) buildRowEvalMap(row map[string]any) map[string]any {
	ctx := make(map[string]any)
	for k, v := range f.vars {
		anyVal := valToAny(v)
		ctx["@"+k] = anyVal
		ctx["@"+strings.ToLower(k)] = anyVal
		ctx[k] = anyVal
		ctx[strings.ToLower(k)] = anyVal
	}
	if row != nil {
		for k, v := range row {
			ctx[k] = v
			ctx[strings.ToLower(k)] = v
		}
	}
	return ctx
}

func (f *VMFrame) callUDF(fnName string, argRegs []int32) (storage.Value, error) {
	f.executor.mu.RLock()
	udf, ok := f.executor.udfs[cleanSymbol(fnName)]
	f.executor.mu.RUnlock()

	if !ok {
		return storage.NullValue(storage.TypeUnknown), fmt.Errorf("UDF %s not found", fnName)
	}

	params := make(map[string]any)
	for i, p := range udf.Program.Params {
		if i < len(argRegs) {
			regIdx := argRegs[i]
			if int(regIdx) < len(f.regs) {
				params[p.Name] = valToAny(f.regs[regIdx])
			}
		}
	}

	retVal, logs, err := f.executor.RunInTx(udf.Program, params, f.tx, f.transientTables)
	if len(logs) > 0 {
		f.logs = append(f.logs, logs...)
	}
	if err != nil {
		return storage.NullValue(storage.TypeUnknown), err
	}
	return toStorageValue(retVal), nil
}

func (f *VMFrame) callProc(procName string, argRegs []int32) error {
	prog, found := f.executor.Cache().GetByName(procName)
	if !found {
		return fmt.Errorf("procedure %s not found", procName)
	}

	params := make(map[string]any)
	for i, p := range prog.Params {
		if i < len(argRegs) {
			regIdx := argRegs[i]
			if int(regIdx) < len(f.regs) {
				params[p.Name] = valToAny(f.regs[regIdx])
			}
		}
	}

	retVal, logs, err := f.executor.RunInTx(prog, params, f.tx, f.transientTables)
	if len(logs) > 0 {
		f.logs = append(f.logs, logs...)
	}
	_ = retVal
	return err
}

// Storage arithmetic & comparison helpers

func storageAdd(a, b storage.Value) storage.Value {
	if a.IsNull || b.IsNull {
		if !a.IsNull {
			return a
		}
		return b
	}
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		sum := n1 + n2
		if sum == math.Trunc(sum) {
			return storage.IntValue(int64(sum))
		}
		return storage.FloatValue(sum)
	}
	return storage.StringValue(valToString(a) + valToString(b))
}

func storageSub(a, b storage.Value) storage.Value {
	n1, _ := toFloatNum(a)
	n2, _ := toFloatNum(b)
	diff := n1 - n2
	if diff == math.Trunc(diff) {
		return storage.IntValue(int64(diff))
	}
	return storage.FloatValue(diff)
}

func storageMul(a, b storage.Value) storage.Value {
	n1, _ := toFloatNum(a)
	n2, _ := toFloatNum(b)
	prod := n1 * n2
	if prod == math.Trunc(prod) {
		return storage.IntValue(int64(prod))
	}
	return storage.FloatValue(prod)
}

func storageDiv(a, b storage.Value) (storage.Value, error) {
	n1, _ := toFloatNum(a)
	n2, _ := toFloatNum(b)
	if n2 == 0 {
		return storage.NullValue(storage.TypeUnknown), fmt.Errorf("division by zero")
	}
	div := n1 / n2
	if div == math.Trunc(div) {
		return storage.IntValue(int64(div)), nil
	}
	return storage.FloatValue(div), nil
}

func storageNeg(a storage.Value) storage.Value {
	if n, ok := toFloatNum(a); ok {
		neg := -n
		if neg == math.Trunc(neg) {
			return storage.IntValue(int64(neg))
		}
		return storage.FloatValue(neg)
	}
	return a
}

func storageEq(a, b storage.Value) bool {
	if a.IsNull && b.IsNull {
		return true
	}
	if a.IsNull || b.IsNull {
		return false
	}
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		return n1 == n2
	}
	return strings.EqualFold(valToString(a), valToString(b))
}

func storageLt(a, b storage.Value) bool {
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		return n1 < n2
	}
	return valToString(a) < valToString(b)
}

func storageLte(a, b storage.Value) bool {
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		return n1 <= n2
	}
	return valToString(a) <= valToString(b)
}

func storageGt(a, b storage.Value) bool {
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		return n1 > n2
	}
	return valToString(a) > valToString(b)
}

func storageGte(a, b storage.Value) bool {
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		return n1 >= n2
	}
	return valToString(a) >= valToString(b)
}

func toFloatNum(v storage.Value) (float64, bool) {
	if v.IsNull {
		return 0, false
	}
	switch v.Type {
	case storage.TypeInt:
		return float64(v.IVal), true
	case storage.TypeFloat:
		return v.FVal, true
	case storage.TypeBool:
		if v.BVal {
			return 1, true
		}
		return 0, true
	case storage.TypeVarChar:
		if f, err := strconv.ParseFloat(v.SVal, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func isTruthy(v storage.Value) bool {
	if v.IsNull {
		return false
	}
	switch v.Type {
	case storage.TypeBool:
		return v.BVal
	case storage.TypeInt:
		return v.IVal != 0
	case storage.TypeFloat:
		return v.FVal != 0
	case storage.TypeVarChar:
		return len(v.SVal) > 0 && !strings.EqualFold(v.SVal, "false") && v.SVal != "0"
	default:
		return true
	}
}

func valToString(v storage.Value) string {
	if v.IsNull {
		return "NULL"
	}
	switch v.Type {
	case storage.TypeInt:
		return strconv.FormatInt(v.IVal, 10)
	case storage.TypeFloat:
		return strconv.FormatFloat(v.FVal, 'f', -1, 64)
	case storage.TypeBool:
		return strconv.FormatBool(v.BVal)
	case storage.TypeDateTime:
		return v.TVal.Format("2006-01-02 15:04:05")
	default:
		return v.SVal
	}
}

func valToAny(v storage.Value) any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeBool:
		return v.BVal
	case storage.TypeDateTime:
		return v.TVal
	default:
		return v.SVal
	}
}

func toStorageValue(v any) storage.Value {
	if v == nil {
		return storage.NullValue(storage.TypeUnknown)
	}
	switch val := v.(type) {
	case storage.Value:
		return val
	case int64:
		return storage.IntValue(val)
	case int:
		return storage.IntValue(int64(val))
	case float64:
		if val == math.Trunc(val) {
			return storage.Value{Type: storage.TypeInt, IVal: int64(val), FVal: val}
		}
		return storage.FloatValue(val)
	case string:
		return storage.StringValue(val)
	case bool:
		return storage.BoolValue(val)
	default:
		return storage.StringValue(fmt.Sprintf("%v", v))
	}
}

func recordToMap(rec storage.Record) map[string]any {
	if rec == nil {
		return nil
	}
	m := make(map[string]any, len(rec))
	for k, v := range rec {
		m[k] = valToAny(v)
	}
	return m
}

func matchLike(str, pattern string) bool {
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		case '.', '+', '?', '^', '$', '(', ')', '[', ']', '{', '}', '|', '\\':
			b.WriteString("\\")
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return strings.EqualFold(str, pattern)
	}
	return re.MatchString(str)
}

func isTransientTable(name string) bool {
	lower := strings.ToLower(name)
	return lower == "inserted" || lower == "deleted"
}

func hashScript(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func cleanSymbol(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "@@") {
		return name
	}
	name = strings.TrimPrefix(name, "@")
	name = strings.Trim(name, "[]\"'`")
	if strings.HasPrefix(strings.ToLower(name), "dbo.") {
		name = name[4:]
	}
	name = strings.Trim(name, "[]\"'`")
	return name
}
