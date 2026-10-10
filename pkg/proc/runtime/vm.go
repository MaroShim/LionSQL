package runtime

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// DefaultPlanCacheCapacity defines the maximum number of compiled bytecode plans retained in memory.
const DefaultPlanCacheCapacity = 1024

type planCacheEntry struct {
	hash    string
	program *proc.Program
}

// PlanCache stores precompiled native T-SQL bytecode programs with bounded LRU eviction (Golden Rule 29).
type PlanCache struct {
	mu       sync.RWMutex
	capacity int
	programs map[string]*list.Element
	lruList  *list.List
	byName   map[string]*proc.Program
}

// NewPlanCache creates an execution plan cache with DefaultPlanCacheCapacity (1024).
func NewPlanCache() *PlanCache {
	return NewPlanCacheWithCapacity(DefaultPlanCacheCapacity)
}

// NewPlanCacheWithCapacity creates an execution plan cache with a custom bounded capacity.
func NewPlanCacheWithCapacity(capacity int) *PlanCache {
	if capacity <= 0 {
		capacity = DefaultPlanCacheCapacity
	}
	return &PlanCache{
		capacity: capacity,
		programs: make(map[string]*list.Element),
		lruList:  list.New(),
		byName:   make(map[string]*proc.Program),
	}
}

// GetOrCompile retrieves a cached bytecode program or compiles and caches the T-SQL script with LRU promotion.
func (c *PlanCache) GetOrCompile(script string) (*proc.Program, error) {
	h := hashScript(script)

	c.mu.Lock()
	if elem, exists := c.programs[h]; exists {
		c.lruList.MoveToFront(elem)
		prog := elem.Value.(*planCacheEntry).program
		c.mu.Unlock()
		return prog, nil
	}
	c.mu.Unlock()

	prog, err := proc.Compile(script)
	if err != nil {
		return nil, fmt.Errorf("bytecode compilation error: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check under lock in case another goroutine compiled it concurrently
	if elem, exists := c.programs[h]; exists {
		c.lruList.MoveToFront(elem)
		return elem.Value.(*planCacheEntry).program, nil
	}

	// LRU eviction if capacity reached
	if c.capacity > 0 && c.lruList.Len() >= c.capacity {
		back := c.lruList.Back()
		if back != nil {
			c.lruList.Remove(back)
			oldEntry := back.Value.(*planCacheEntry)
			delete(c.programs, oldEntry.hash)
		}
	}

	elem := c.lruList.PushFront(&planCacheEntry{
		hash:    h,
		program: prog,
	})
	c.programs[h] = elem
	if prog.Name != "" {
		c.byName[strings.ToLower(cleanSymbol(prog.Name))] = prog
	}

	return prog, nil
}

// PutByName stores a compiled bytecode program indexed by procedure or function name.
func (c *PlanCache) PutByName(name string, prog *proc.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName[strings.ToLower(cleanSymbol(name))] = prog
}

// GetByName retrieves a compiled bytecode program by procedure or function name.
func (c *PlanCache) GetByName(name string) (*proc.Program, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	prog, ok := c.byName[strings.ToLower(cleanSymbol(name))]
	return prog, ok
}

// Len returns the current number of cached script programs.
func (c *PlanCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.programs)
}

// Capacity returns the maximum capacity of the plan cache.
func (c *PlanCache) Capacity() int {
	return c.capacity
}

// Clear flushes all cached bytecode programs.
func (c *PlanCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.programs = make(map[string]*list.Element)
	c.lruList = list.New()
	c.byName = make(map[string]*proc.Program)
}

// Executor manages execution of compiled stored procedures, functions, and triggers within ambient ACID transactions.
type Executor struct {
	cache       *PlanCache
	mu          sync.RWMutex
	udfs        map[string]*UDFEntry
	triggers    map[string]map[string][]*TriggerEntry // table (lower) -> event (upper) -> entries
	framePool   sync.Pool
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
		framePool: sync.Pool{
			New: func() any {
				return &VMFrame{
					regs:            make([]storage.Value, 64),
					slots:           make([]storage.Value, 16),
					vars:            make(map[string]storage.Value, 16),
					evalCtx:         make(map[string]any, 32),
					cursors:         make(map[string]*cursorState),
					transientTables: make(map[string][]map[string]any),
					catchStack:      make([]int, 0, 8),
					logs:            make([]string, 0, 8),
				}
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
	session         storage.Session
	tx              storage.Transaction
	prog            *proc.Program
	regs            []storage.Value
	slots           []storage.Value // Fast O(1) indexed variable slots
	vars            map[string]storage.Value
	evalCtx         map[string]any
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
	ctx             context.Context
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
	return e.RunContext(context.Background(), prog, procName, params, session)
}

// RunContext executes a precompiled bytecode program with ambient transaction and cancellation semantics.
func (e *Executor) RunContext(ctx context.Context, prog *proc.Program, procName string, params map[string]any, session storage.Session) (*ExecutionResult, error) {
	start := time.Now()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to begin ambient transaction: %w", err)
	}

	frame := e.newFrame(prog, tx)
	frame.session = session
	frame.ctx = ctx
	defer e.releaseFrame(frame)

	// Bind parameters
	for k, v := range params {
		clean := cleanSymbol(k)
		stVal := toStorageValue(v)
		frame.setVar(clean, stVal)
	}

	retVal, err := frame.Execute(0)
	if err != nil {
		if frame.tx != nil {
			_ = frame.tx.Rollback()
		}
		return nil, fmt.Errorf("execution error (rolled back): %w", err)
	}

	if frame.tx != nil {
		if err := frame.tx.Commit(); err != nil && !errors.Is(err, storage.ErrTxClosed) {
			return nil, fmt.Errorf("failed to commit ambient transaction: %w", err)
		}
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
		frame.setVar(clean, stVal)
	}

	retVal, err := frame.Execute(0)
	return retVal, frame.logs, err
}

// ExecuteInTx executes a program within an existing transaction context and returns an ExecutionResult with Rows and Columns.
func (e *Executor) ExecuteInTx(prog *proc.Program, params map[string]any, tx storage.Transaction, transientTables map[string][]map[string]any) (*ExecutionResult, error) {
	return e.ExecuteInTxContext(context.Background(), prog, params, tx, transientTables)
}

// ExecuteInTxContext executes a program within an existing transaction and cancellation context.
func (e *Executor) ExecuteInTxContext(ctx context.Context, prog *proc.Program, params map[string]any, tx storage.Transaction, transientTables map[string][]map[string]any) (*ExecutionResult, error) {
	start := time.Now()
	frame := e.newFrame(prog, tx)
	frame.ctx = ctx
	defer e.releaseFrame(frame)

	if transientTables != nil {
		for k, v := range transientTables {
			frame.transientTables[k] = v
		}
	}

	for k, v := range params {
		clean := cleanSymbol(k)
		stVal := toStorageValue(v)
		frame.setVar(clean, stVal)
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

	numSlots := int(prog.SlotCount)
	if numSlots < 8 {
		numSlots = 8
	}

	f := e.framePool.Get().(*VMFrame)
	if cap(f.regs) < numRegs {
		f.regs = make([]storage.Value, numRegs)
	} else {
		f.regs = f.regs[:numRegs]
	}

	if cap(f.slots) < numSlots {
		f.slots = make([]storage.Value, numSlots)
	} else {
		f.slots = f.slots[:numSlots]
	}
	for i := range f.slots {
		f.slots[i] = storage.NullValue(storage.TypeUnknown)
	}

	// Reset structures for clean re-execution
	clear(f.vars)
	clear(f.evalCtx)
	clear(f.cursors)
	clear(f.transientTables)
	if f.evalCtx == nil {
		f.evalCtx = make(map[string]any, 32)
	}
	f.catchStack = f.catchStack[:0]
	f.logs = f.logs[:0]

	f.executor = e
	f.tx = tx
	f.prog = prog
	f.ctx = nil
	f.rows = nil
	f.columns = nil
	f.rowCount = 0
	f.tranCount = 0
	f.lastIdentity = 0
	f.lastError = 0
	f.fetchStatus = -1
	for i := range f.regs {
		f.regs[i] = storage.NullValue(storage.TypeUnknown)
	}

	return f
}

func (e *Executor) releaseFrame(frame *VMFrame) {
	if frame == nil {
		return
	}
	frame.tx = nil
	frame.session = nil
	frame.prog = nil
	e.framePool.Put(frame)
}

// Execute runs the VM instruction loop starting at startPC.
func (f *VMFrame) Execute(startPC int) (any, error) {
	instructions := f.prog.Instructions
	pc := startPC
	iterCount := 0

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
		case proc.OpHalt, proc.OpNop, proc.OpReturn, proc.OpJump, proc.OpJumpIfFalse, proc.OpJumpIfTrue,
			proc.OpJumpIfEq, proc.OpJumpIfNeq, proc.OpJumpIfLt, proc.OpJumpIfLte, proc.OpJumpIfGt, proc.OpJumpIfGte,
			proc.OpJumpIfEqInt, proc.OpJumpIfNeqInt, proc.OpJumpIfLtInt, proc.OpJumpIfLteInt, proc.OpJumpIfGtInt, proc.OpJumpIfGteInt,
			proc.OpPushCatch, proc.OpPopCatch, proc.OpThrow, proc.OpRaiserror, proc.OpPrint:
			halt, retVal, err := f.execFlowOp(&inst, &pc)
			if err != nil {
				return nil, err
			}
			if halt {
				return retVal, nil
			}

		case proc.OpLoadConst, proc.OpLoadVar, proc.OpStoreVar, proc.OpCopy,
			proc.OpAdd, proc.OpSub, proc.OpMul, proc.OpDiv, proc.OpNeg, proc.OpNot,
			proc.OpAnd, proc.OpOr,
			proc.OpEq, proc.OpNeq, proc.OpLt, proc.OpLte, proc.OpGt, proc.OpGte,
			proc.OpAddInt, proc.OpSubInt, proc.OpMulInt,
			proc.OpEqInt, proc.OpNeqInt, proc.OpLtInt, proc.OpLteInt, proc.OpGtInt, proc.OpGteInt,
			proc.OpLike, proc.OpBetween, proc.OpIn, proc.OpIsNull, proc.OpIsNotNull:
			if err := f.execComputeOp(&inst, &pc); err != nil {
				return nil, err
			}

		case proc.OpTxBegin, proc.OpTxCommit, proc.OpTxRollback, proc.OpTxSavepoint, proc.OpTxRollbackTo:
			if err := f.execTxOp(&inst); err != nil {
				return nil, err
			}

		case proc.OpInsert, proc.OpUpdate, proc.OpUpdateWhere, proc.OpDelete, proc.OpDeleteWhere,
			proc.OpSelectInto, proc.OpPointSelect:
			if err := f.execStorageOp(&inst, &pc); err != nil {
				return nil, err
			}

		case proc.OpCursorOpen, proc.OpCursorFetch, proc.OpCursorClose, proc.OpCursorDealloc,
			proc.OpCallBuiltin, proc.OpCallUDF, proc.OpCallProc, proc.OpExecDynamic:
			if err := f.execCursorAndCallOp(&inst, &pc); err != nil {
				return nil, err
			}

		case proc.OpRelQuery, proc.OpIndexScan, proc.OpResultRow:
			if err := f.execRelOp(&inst, &pc); err != nil {
				return nil, err
			}

		default:
			return nil, fmt.Errorf("unsupported opcode: %v", inst.Op)
		}
	}

	return nil, nil
}

func (f *VMFrame) bindWhereVariables(where string) string {
	if where == "" || len(f.vars) == 0 {
		return where
	}
	res := where
	type varBinding struct {
		name string
		val  string
	}
	seen := make(map[string]bool)
	var bindings []varBinding

	for k, v := range f.vars {
		clean := cleanSymbol(k)
		varName := "@" + clean
		lower := strings.ToLower(varName)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		valStr := v.String()
		if v.Type == storage.TypeVarChar {
			valStr = "'" + strings.ReplaceAll(v.SVal, "'", "''") + "'"
		}
		bindings = append(bindings, varBinding{name: varName, val: valStr})
	}

	sort.Slice(bindings, func(i, j int) bool {
		return len(bindings[i].name) > len(bindings[j].name)
	})

	for _, b := range bindings {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(b.name) + `\b`)
		res = re.ReplaceAllString(res, b.val)
	}
	return res
}

func (f *VMFrame) execRelQuery(sql string, queryData *proc.RelQueryData) ([]map[string]any, []string, error) {
	p := planner.NewPlanner()
	var qInfo *planner.QueryInfo
	if queryData != nil {
		if cached, ok := queryData.GetCachedQueryInfo().(*planner.QueryInfo); ok && cached != nil {
			qInfo = cached.Clone()
		}
	}
	if qInfo == nil {
		parsed, err := p.ParseSelect(sql)
		if err != nil {
			return nil, nil, fmt.Errorf("planner parse error in OpRelQuery: %w", err)
		}
		if queryData != nil {
			queryData.SetCachedQueryInfo(parsed.Clone())
		}
		qInfo = parsed
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

	var plan *planner.PlanNode
	// If query has no variable where clause or variable was already bound, check CachedPlan
	if queryData != nil && qInfo.WhereClause == "" {
		if cachedPlan, ok := queryData.GetCachedPlan().(*planner.PlanNode); ok && cachedPlan != nil {
			plan = cachedPlan.Clone()
		}
	}

	if plan == nil {
		plan = p.BuildMultiJoinPlanWithStats(qInfo, leftSchema, nil, rightSchemas, nil)
		if queryData != nil && qInfo.WhereClause == "" {
			queryData.SetCachedPlan(plan.Clone())
		}
	}

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

	if f.prog != nil && f.prog.VarSlots != nil {
		lower := strings.ToLower(clean)
		if slot, ok := f.prog.VarSlots[lower]; ok && int(slot) >= 0 && int(slot) < len(f.slots) {
			val := f.slots[slot]
			if !val.IsNull || val.Type != storage.TypeUnknown {
				return val, true
			}
		}
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
	lower := strings.ToLower(clean)
	f.vars[lower] = val

	if f.prog != nil && f.prog.VarSlots != nil {
		if slot, ok := f.prog.VarSlots[lower]; ok && int(slot) >= 0 && int(slot) < len(f.slots) {
			f.slots[slot] = val
		} else if slot, ok := f.prog.VarSlots[clean]; ok && int(slot) >= 0 && int(slot) < len(f.slots) {
			f.slots[slot] = val
		}
	}

	if f.evalCtx != nil {
		anyVal := valToAny(val)
		f.evalCtx["@"+clean] = anyVal
		f.evalCtx["@"+lower] = anyVal
		f.evalCtx[clean] = anyVal
		f.evalCtx[lower] = anyVal
	}
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
	if colName, valExpr, ok := extractSimpleEq(data.WhereExpr); ok && f.isPKCol(data.Table, colName) {
		evalCtx := f.buildRowEvalMap(nil)
		pkAny, _ := proc.EvalRowExpr(valExpr, evalCtx, nil)
		pkVal := toStorageValue(pkAny)
		oldRec, err := f.tx.Get(data.Table, pkVal)
		if err == nil && oldRec != nil {
			oldRowMap := recordToMap(oldRec)
			rowCtx := f.buildRowEvalMap(oldRowMap)
			recUpdates := make(storage.Record, len(data.Assignments))
			newRowMap := make(map[string]any, len(oldRowMap))
			for k, v := range oldRowMap {
				newRowMap[k] = v
			}
			for _, a := range data.Assignments {
				vAny, _ := proc.EvalRowExpr(a.Value, rowCtx, nil)
				stVal := toStorageValue(vAny)
				recUpdates[a.Column] = stVal
				newRowMap[a.Column] = valToAny(stVal)
			}
			if err := f.tx.Update(data.Table, pkVal, recUpdates); err != nil {
				return 0, fmt.Errorf("update error on %s: %w", data.Table, err)
			}
			_ = f.fireTriggers(data.Table, "UPDATE", []map[string]any{newRowMap}, []map[string]any{oldRowMap})
			return 1, nil
		}
		return 0, nil
	}

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
	if colName, valExpr, ok := extractSimpleEq(data.WhereExpr); ok && f.isPKCol(data.Table, colName) {
		evalCtx := f.buildRowEvalMap(nil)
		pkAny, _ := proc.EvalRowExpr(valExpr, evalCtx, nil)
		pkVal := toStorageValue(pkAny)
		oldRec, err := f.tx.Get(data.Table, pkVal)
		if err == nil && oldRec != nil {
			if err := f.tx.Delete(data.Table, pkVal); err != nil {
				return 0, fmt.Errorf("delete error on %s: %w", data.Table, err)
			}
			_ = f.fireTriggers(data.Table, "DELETE", nil, []map[string]any{recordToMap(oldRec)})
			return 1, nil
		}
		return 0, nil
	}

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

	if data.WhereCol != "" && data.WhereVal != nil {
		pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
		pkVal := toStorageValue(pkAny)
		if f.isPKCol(data.Table, data.WhereCol) {
			if rec, err := f.tx.Get(data.Table, pkVal); err == nil && rec != nil {
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

func extractSimpleEq(expr proc.Expr) (string, proc.Expr, bool) {
	if expr == nil {
		return "", nil, false
	}
	if bin, ok := expr.(*proc.BinaryExpr); ok && bin.Op == proc.TokenEq {
		if colRef, ok := bin.Left.(*proc.ColumnRefExpr); ok {
			return cleanSymbol(colRef.Name), bin.Right, true
		} else if colRef, ok := bin.Right.(*proc.ColumnRefExpr); ok {
			return cleanSymbol(colRef.Name), bin.Left, true
		}
	}
	return "", nil, false
}

func (f *VMFrame) isPKCol(tableName, colName string) bool {
	clean := cleanSymbol(colName)
	if tg := f.executor.getTableGetter(); tg != nil {
		if schema, err := tg(tableName); err == nil && schema != nil {
			for _, c := range schema.Columns {
				if c.PrimaryKey {
					return strings.EqualFold(c.Name, clean)
				}
			}
		}
	}
	return strings.EqualFold(clean, "id")
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
	if len(row) == 0 {
		return f.evalCtx
	}
	ctx := make(map[string]any, len(f.evalCtx)+len(row)*2)
	for k, v := range f.evalCtx {
		ctx[k] = v
	}
	for k, v := range row {
		ctx[k] = v
		ctx[strings.ToLower(k)] = v
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
	if strings.EqualFold(procName, "sp_executesql") {
		return f.callSpExecuteSql(argRegs)
	}

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

func (f *VMFrame) callSpExecuteSql(argRegs []int32) error {
	if len(argRegs) == 0 {
		return fmt.Errorf("sp_executesql requires at least 1 parameter (@stmt)")
	}
	regIdx := argRegs[0]
	if int(regIdx) >= len(f.regs) {
		return fmt.Errorf("invalid register for sp_executesql statement")
	}
	val := f.regs[regIdx]
	stmt := val.String()
	if val.Type == storage.TypeVarChar {
		stmt = val.SVal
	}
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return nil
	}

	prog, err := f.executor.Cache().GetOrCompile(stmt)
	if err != nil {
		prog, err = proc.Compile(stmt)
		if err != nil {
			return fmt.Errorf("sp_executesql compile error: %w", err)
		}
	}

	paramMap := make(map[string]any)
	if len(argRegs) > 2 {
		for i, reg := range argRegs[2:] {
			if int(reg) < len(f.regs) {
				pVal := valToAny(f.regs[reg])
				pIdx := i + 1
				paramMap[fmt.Sprintf("p%d", pIdx)] = pVal
				paramMap[fmt.Sprintf("@p%d", pIdx)] = pVal
			}
		}
	}

	res, err := f.executor.ExecuteInTx(prog, paramMap, f.tx, f.transientTables)
	if err != nil {
		return err
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
	return nil
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
	cleaned := proc.CleanSchemaPrefix(name)
	// If it is a system schema like sys.tables, preserve it
	lower := strings.ToLower(cleaned)
	if strings.HasPrefix(lower, "sys.") || strings.HasPrefix(lower, "information_schema.") || strings.HasPrefix(lower, "pg_catalog.") || strings.HasPrefix(lower, "system.") {
		return cleaned
	}
	if dot := strings.LastIndex(cleaned, "."); dot != -1 {
		return cleaned[dot+1:]
	}
	return cleaned
}
