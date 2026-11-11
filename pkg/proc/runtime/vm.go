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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// ExecutionResult captures the output and metrics of a procedure, function, or query execution.
type ExecutionResult struct {
	ReturnValue  any
	Logs         []string
	Duration     time.Duration
	Rows         []map[string]any // Relational result tuples for OpRelQuery
	Columns      []string         // Result projection column names
	Values       [][]any          // Positional values per row for duplicate column preservation
	RowsAffected int64
	LastIdentity int64
	Vars         map[string]any // Final variable states after execution
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

// PlanCache stores precompiled native T-SQL bytecode programs with bounded LRU eviction ([RULE:BoundedBuffers]).
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
	key := script

	c.mu.RLock()
	if elem, exists := c.programs[key]; exists {
		prog := elem.Value.(*planCacheEntry).program
		c.mu.RUnlock()
		return prog, nil
	}
	c.mu.RUnlock()

	prog, err := proc.Compile(script)
	if err != nil {
		return nil, fmt.Errorf("bytecode compilation error: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check under lock in case another goroutine compiled it concurrently
	if elem, exists := c.programs[key]; exists {
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
		hash:    key,
		program: prog,
	})
	c.programs[key] = elem
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

// Invalidate removes a compiled bytecode program by procedure or function name.
func (c *PlanCache) Invalidate(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byName, strings.ToLower(cleanSymbol(name)))
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

// TableManager defines the storage interface for managing physical tables for table variables.
type TableManager interface {
	CreateTable(schema *storage.TableSchema) error
	DropTable(tableName string) error
	GetTable(tableName string) (*storage.TableSchema, error)
}

// IdentityProvider defines the interface for reading and updating table identity/sequence values.
type IdentityProvider interface {
	GetTableLastIdentity(tableName string) (int64, bool)
	SetTableLastIdentity(tableName string, id int64)
	AllocTableIdentity(tableName string, count int64, initFn func() int64) int64
}

// Executor manages execution of compiled stored procedures, functions, and triggers within ambient ACID transactions.
type Executor struct {
	cache          *PlanCache
	mu             sync.RWMutex
	udfs           map[string]*UDFEntry
	triggers       map[string]map[string][]*TriggerEntry // table (lower) -> event (upper) -> entries
	framePool      sync.Pool
	tableGetter    execution.TableGetter
	tableManager   TableManager
	queryExecutor  QueryExecutor
	identProvider  IdentityProvider
	identMu        sync.RWMutex
	lastIdentities map[string]int64
}

// NewExecutor creates a procedure and trigger executor.
func NewExecutor(cache *PlanCache) *Executor {
	if cache == nil {
		cache = NewPlanCache()
	}
	return &Executor{
		cache:          cache,
		udfs:           make(map[string]*UDFEntry),
		triggers:       make(map[string]map[string][]*TriggerEntry),
		lastIdentities: make(map[string]int64),
		framePool: sync.Pool{
			New: func() any {
				return &VMFrame{
					regs:            make([]storage.Value, 64),
					slots:           make([]storage.Value, 16),
					rowScratch:      make([]storage.Value, 32),
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

// QueryExecutor defines a callback to execute a full SQL SELECT statement in an existing transaction.
type QueryExecutor func(ctx context.Context, sql string, tx storage.Transaction, vars map[string]storage.Value) ([]map[string]any, []string, error)

// SetQueryExecutor registers a subquery execution callback for evaluation inside the VDBE VM.
func (e *Executor) SetQueryExecutor(qe QueryExecutor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queryExecutor = qe
}

func (e *Executor) getQueryExecutor() QueryExecutor {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.queryExecutor
}

// SetIdentityProvider registers an external IdentityProvider (such as Coordinator).
func (e *Executor) SetIdentityProvider(p IdentityProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.identProvider = p
}

// GetLastIdentity retrieves the last generated identity value for a table.
func (e *Executor) GetLastIdentity(tableName string) (int64, bool) {
	e.mu.RLock()
	p := e.identProvider
	e.mu.RUnlock()
	if p != nil {
		return p.GetTableLastIdentity(tableName)
	}
	e.identMu.RLock()
	defer e.identMu.RUnlock()
	if e.lastIdentities == nil {
		return 0, false
	}
	v, ok := e.lastIdentities[strings.ToLower(tableName)]
	return v, ok
}

// SetLastIdentity records the last generated identity value for a table.
func (e *Executor) SetLastIdentity(tableName string, id int64) {
	e.mu.RLock()
	p := e.identProvider
	e.mu.RUnlock()
	if p != nil {
		p.SetTableLastIdentity(tableName, id)
		return
	}
	e.identMu.Lock()
	defer e.identMu.Unlock()
	if e.lastIdentities == nil {
		e.lastIdentities = make(map[string]int64)
	}
	e.lastIdentities[strings.ToLower(tableName)] = id
}

// AllocIdentity atomically allocates a contiguous block of count IDs for tableName.
func (e *Executor) AllocIdentity(tableName string, count int64, initFn func() int64) int64 {
	e.mu.RLock()
	p := e.identProvider
	e.mu.RUnlock()
	if p != nil {
		return p.AllocTableIdentity(tableName, count, initFn)
	}
	e.identMu.Lock()
	defer e.identMu.Unlock()
	if e.lastIdentities == nil {
		e.lastIdentities = make(map[string]int64)
	}
	lower := strings.ToLower(tableName)
	cur, exists := e.lastIdentities[lower]
	if !exists {
		if initFn != nil {
			cur = initFn()
		}
	}
	e.lastIdentities[lower] = cur + count
	return cur + 1
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

// SetTableManager configures the physical table lifecycle manager for table variables.
func (e *Executor) SetTableManager(tm TableManager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tableManager = tm
}

func (e *Executor) getTableManager() TableManager {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.tableManager
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

// UnregisterUDF removes a user-defined function from execution runtimes.
func (e *Executor) UnregisterUDF(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sym := cleanSymbol(name)
	delete(e.udfs, sym)
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

// UnregisterTrigger removes all triggers registered under the specified name.
func (e *Executor) UnregisterTrigger(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sym := cleanSymbol(name)
	for tbl, evtMap := range e.triggers {
		for evt, entries := range evtMap {
			filtered := entries[:0]
			for _, entry := range entries {
				if entry.Name != sym {
					filtered = append(filtered, entry)
				}
			}
			e.triggers[tbl][evt] = filtered
		}
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

// HasTriggers returns true if there is at least one trigger registered for tableName and event.
func (e *Executor) HasTriggers(tableName, event string) bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	tbl := strings.ToLower(tableName)
	evt := strings.ToUpper(event)

	if tblMap, ok := e.triggers[tbl]; ok {
		if trgs, ok := tblMap[evt]; ok && len(trgs) > 0 {
			return true
		}
	}
	return false
}

// VMFrame maintains the execution context for a bytecode invocation.
type VMFrame struct {
	executor        *Executor
	session         storage.Session
	tx              storage.Transaction
	prog            *proc.Program
	regs            []storage.Value
	slots           []storage.Value // Fast O(1) indexed variable slots
	rowScratch      []storage.Value // Reusable scratch buffer for DML Row mutations (Phase 2)
	evalCtxDirty    bool            // True when f.slots or f.vars have been modified since last evalCtx sync
	vars            map[string]storage.Value
	evalCtx         map[string]any
	cursors         map[string]*cursorState
	transientTables map[string][]map[string]any
	tableVars       map[string]string
	catchStack      []int
	logs            []string
	rows            []map[string]any
	columns         []string
	values          [][]any
	rowCount        int64
	cursorRows       int64
	lastOpenedCursor string
	fetchStatus     int
	tranCount       int
	lastIdentity    int64
	lastError       int
	lastErrorMsg    string
	lastErrorLine   int
	callDepth       int
	ctx             context.Context
}

func (f *VMFrame) bindParams(params map[string]any) {
	if len(params) == 0 {
		return
	}
	for k, v := range params {
		clean := cleanSymbol(k)
		stVal := toStorageValue(v)
		slot := int32(-1)
		if f.prog != nil && f.prog.VarSlots != nil {
			if s, ok := f.prog.VarSlots[clean]; ok {
				slot = s
			} else if s, ok := f.prog.VarSlots[strings.ToLower(clean)]; ok {
				slot = s
			}
		}
		if slot >= 0 && int(slot) < len(f.slots) {
			f.slots[slot] = stVal
		} else {
			if f.vars == nil {
				f.vars = make(map[string]storage.Value, 8)
			}
			f.vars[clean] = stVal
			f.vars[strings.ToLower(clean)] = stVal
		}
	}
	f.evalCtxDirty = true
}

func (f *VMFrame) getStorageVarsMap() map[string]storage.Value {
	varEst := len(f.vars)
	if f.prog != nil {
		varEst += len(f.prog.VarNames)
	}
	vars := make(map[string]storage.Value, varEst)
	if f.vars != nil {
		for k, v := range f.vars {
			vars[k] = v
		}
	}
	if f.prog != nil && len(f.prog.VarNames) > 0 {
		for slot, varName := range f.prog.VarNames {
			if slot < len(f.slots) {
				stVal := f.slots[slot]
				if !stVal.IsNull || stVal.Type != storage.TypeUnknown {
					vars[varName] = stVal
				}
			}
		}
	}
	return vars
}

func (f *VMFrame) resolveTable(name string) string {
	raw := strings.TrimSpace(name)
	if f.tableVars != nil {
		lower := strings.ToLower(raw)
		if phys, ok := f.tableVars[lower]; ok {
			return phys
		}
		if !strings.HasPrefix(lower, "@") {
			if phys, ok := f.tableVars["@"+lower]; ok {
				return phys
			}
		}
	}
	return cleanSymbol(raw)
}

func (f *VMFrame) getTableSchema(tableName string) (*storage.TableSchema, error) {
	resolved := f.resolveTable(tableName)
	if f.tx != nil {
		if tg, ok := f.tx.(interface{ GetTable(string) (*storage.TableSchema, error) }); ok {
			if schema, err := tg.GetTable(resolved); err == nil && schema != nil {
				return schema, nil
			}
		}
	}
	if f.executor != nil {
		if tm := f.executor.getTableManager(); tm != nil {
			if schema, err := tm.GetTable(resolved); err == nil && schema != nil {
				return schema, nil
			}
		}
		if tg := f.executor.getTableGetter(); tg != nil {
			return tg(resolved)
		}
	}
	return nil, storage.ErrTableNotFound
}

func (f *VMFrame) getTableGetter() execution.TableGetter {
	return func(name string) (*storage.TableSchema, error) {
		return f.getTableSchema(name)
	}
}

type cursorState struct {
	name      string
	table     string
	columns   []string
	whereExpr proc.Expr
	orderBy   []proc.OrderByItem
	rows      []storage.Record
	idx       int
	isOpen    bool
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
	if tx != nil {
		frame.tranCount = 1
	}
	defer e.releaseFrame(frame)

	// Bind parameters
	frame.bindParams(params)

	retVal, err := frame.Execute(0)
	if err != nil {
		if frame.tx != nil {
			_ = frame.tx.Rollback()
		}
		return nil, fmt.Errorf("execution error (rolled back): %w", err)
	}

	if frame.tx != nil {
		if err := frame.tx.Commit(); err != nil && !errors.Is(err, storage.ErrTxClosed) {
			_ = frame.tx.Rollback()
			return nil, fmt.Errorf("failed to commit ambient transaction: %w", err)
		}
	}

	return &ExecutionResult{
		ReturnValue:  retVal,
		Logs:         frame.logs,
		Duration:     time.Since(start),
		Rows:         frame.rows,
		Columns:      frame.columns,
		Values:       frame.values,
		RowsAffected: frame.rowCount,
		LastIdentity: frame.lastIdentity,
		Vars:         collectFrameVars(frame),
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

	frame.bindParams(params)

	retVal, err := frame.Execute(0)
	return retVal, frame.logs, err
}

// ExecuteInTx executes a program within an existing transaction context and returns an ExecutionResult with Rows and Columns.
func (e *Executor) ExecuteInTx(prog *proc.Program, params map[string]any, tx storage.Transaction, transientTables map[string][]map[string]any) (*ExecutionResult, error) {
	return e.ExecuteInTxContext(context.Background(), prog, params, tx, transientTables)
}

// ExecuteInTxContext executes a program within an existing transaction and cancellation context.
func (e *Executor) ExecuteInTxContext(ctx context.Context, prog *proc.Program, params map[string]any, tx storage.Transaction, transientTables map[string][]map[string]any) (*ExecutionResult, error) {
	return e.ExecuteInTxWithSessionContext(ctx, prog, params, tx, nil, transientTables)
}

// ExecuteInTxWithSessionContext executes a program within an existing transaction, optional session, and cancellation context.
func (e *Executor) ExecuteInTxWithSessionContext(ctx context.Context, prog *proc.Program, params map[string]any, tx storage.Transaction, session storage.Session, transientTables map[string][]map[string]any) (*ExecutionResult, error) {
	start := time.Now()
	frame := e.newFrame(prog, tx)
	frame.session = session
	frame.ctx = ctx
	if tx != nil {
		frame.tranCount = 1
	}
	defer e.releaseFrame(frame)

	if transientTables != nil {
		for k, v := range transientTables {
			frame.transientTables[k] = v
		}
	}

	frame.bindParams(params)

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
		Values:       frame.values,
		RowsAffected: frame.rowCount,
		LastIdentity: frame.lastIdentity,
		Vars:         collectFrameVars(frame),
	}, nil
}

func collectFrameVars(frame *VMFrame) map[string]any {
	varEst := len(frame.vars)
	if frame.prog != nil {
		varEst += len(frame.prog.VarNames)
	}
	if varEst == 0 {
		return nil
	}
	vars := make(map[string]any, varEst)
	if frame.vars != nil {
		for k, v := range frame.vars {
			clean := cleanSymbol(k)
			vars[clean] = valToAny(v)
		}
	}
	if frame.prog != nil && len(frame.prog.VarNames) > 0 {
		for slot, varName := range frame.prog.VarNames {
			if slot < len(frame.slots) {
				stVal := frame.slots[slot]
				if !stVal.IsNull || stVal.Type != storage.TypeUnknown {
					vars[varName] = valToAny(stVal)
				}
			}
		}
	}
	return vars
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
	if f.vars != nil {
		clear(f.vars)
	}
	f.evalCtxDirty = true
	f.rowScratch = f.rowScratch[:0]
	clear(f.cursors)
	clear(f.transientTables)
	if f.tableVars == nil {
		f.tableVars = make(map[string]string)
	} else {
		clear(f.tableVars)
	}
	f.catchStack = f.catchStack[:0]
	f.logs = f.logs[:0]

	f.executor = e
	f.tx = tx
	f.prog = prog
	f.ctx = nil
	f.rows = nil
	f.columns = nil
	f.values = nil
	f.rowCount = 0
	f.tranCount = 0
	f.lastIdentity = 0
	f.lastError = 0
	f.lastErrorMsg = ""
	f.lastErrorLine = 0
	f.fetchStatus = -1
	for i := range f.regs {
		f.regs[i] = storage.NullValue(storage.TypeUnknown)
	}

	return f
}

func (f *VMFrame) resetForPool() {
	if f == nil {
		return
	}
	// 1. Clear slice backing arrays up to capacity to prevent GC leaks of previous pointers ([RULE:ZeroAllocation])
	if cap(f.regs) > 0 {
		clear(f.regs[:cap(f.regs)])
		f.regs = f.regs[:0]
	}
	if cap(f.slots) > 0 {
		clear(f.slots[:cap(f.slots)])
		f.slots = f.slots[:0]
	}
	if cap(f.rowScratch) > 0 {
		clear(f.rowScratch[:cap(f.rowScratch)])
		f.rowScratch = f.rowScratch[:0]
	}

	// 2. Clear or downsize maps if oversized ([RULE:BoundedBuffers])
	if len(f.vars) > 64 {
		f.vars = make(map[string]storage.Value, 16)
	} else if f.vars != nil {
		clear(f.vars)
	}

	if len(f.evalCtx) > 64 {
		f.evalCtx = make(map[string]any, 32)
	} else if f.evalCtx != nil {
		clear(f.evalCtx)
	}

	// 3. Clean up open cursors and transient tables ([RULE:HandleHygiene])
	clear(f.cursors)
	clear(f.transientTables)

	// Clean up table variables and drop physical tables ([RULE:CleanTeardown])
	if len(f.tableVars) > 0 {
		if f.executor != nil {
			if tm := f.executor.getTableManager(); tm != nil {
				for _, phys := range f.tableVars {
					_ = tm.DropTable(phys)
				}
			}
		}
		clear(f.tableVars)
	}

	// 4. Detach all execution results, sessions, and contexts
	f.rows = nil
	f.columns = nil
	f.values = nil
	f.ctx = nil
	f.tx = nil
	f.session = nil
	f.prog = nil
	f.executor = nil
	f.catchStack = f.catchStack[:0]
	f.logs = f.logs[:0]
	f.rowCount = 0
	f.tranCount = 0
	f.lastIdentity = 0
	f.lastError = 0
	f.lastErrorMsg = ""
	f.lastErrorLine = 0
	f.fetchStatus = -1
	f.evalCtxDirty = true
}

func (e *Executor) releaseFrame(frame *VMFrame) {
	if frame == nil {
		return
	}
	frame.resetForPool()
	e.framePool.Put(frame)
}

// (VMFrame.Execute is implemented in vm_dispatcher.go as a single flattened direct dispatch loop)

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
		valStr := v.String()
		if v.Type == storage.TypeVarChar {
			valStr = "'" + strings.ReplaceAll(v.SVal, "'", "''") + "'"
		}

		clean := cleanSymbol(k)
		if clean != "" {
			varName := "@" + clean
			lower := strings.ToLower(varName)
			if !seen[lower] {
				seen[lower] = true
				bindings = append(bindings, varBinding{name: varName, val: valStr})
			}

			dollarName := "$" + clean
			lowerDollar := strings.ToLower(dollarName)
			if !seen[lowerDollar] {
				seen[lowerDollar] = true
				bindings = append(bindings, varBinding{name: dollarName, val: valStr})
			}
		}

		if strings.HasPrefix(k, "$") {
			lowerK := strings.ToLower(k)
			if !seen[lowerK] {
				seen[lowerK] = true
				bindings = append(bindings, varBinding{name: k, val: valStr})
			}
		}
	}

	sort.Slice(bindings, func(i, j int) bool {
		return len(bindings[i].name) > len(bindings[j].name)
	})

	for _, b := range bindings {
		res = replaceVariableBoundaries(res, b.name, b.val)
	}
	return res
}

func replaceVariableBoundaries(s, target, replacement string) string {
	n := len(s)
	tLen := len(target)
	if n < tLen {
		return s
	}

	var sb strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inBracket := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < n; i++ {
		ch := s[i]

		// Comments
		if inLineComment {
			sb.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			sb.WriteByte(ch)
			if ch == '*' && i+1 < n && s[i+1] == '/' {
				sb.WriteByte('/')
				i++
				inBlockComment = false
			}
			continue
		}
		if ch == '-' && i+1 < n && s[i+1] == '-' {
			sb.WriteString("--")
			i++
			inLineComment = true
			continue
		}
		if ch == '/' && i+1 < n && s[i+1] == '*' {
			sb.WriteString("/*")
			i++
			inBlockComment = true
			continue
		}

		// Quotes
		if inSingleQuote {
			sb.WriteByte(ch)
			if ch == '\'' {
				if i+1 < n && s[i+1] == '\'' {
					sb.WriteByte('\'')
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			sb.WriteByte(ch)
			if ch == '"' {
				if i+1 < n && s[i+1] == '"' {
					sb.WriteByte('"')
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracket {
			sb.WriteByte(ch)
			if ch == ']' {
				inBracket = false
			}
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			sb.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			sb.WriteByte(ch)
			continue
		}
		if ch == '[' {
			inBracket = true
			sb.WriteByte(ch)
			continue
		}

		// Check word boundary matching
		if i+tLen <= n && strings.EqualFold(s[i:i+tLen], target) {
			// Boundary before: start of string or non-ident
			leftOk := (i == 0 || !sqlutil.IsIdentChar(s[i-1]))
			// Boundary after: end of string or non-ident
			rightOk := (i+tLen == n || !sqlutil.IsIdentChar(s[i+tLen]))
			if leftOk && rightOk {
				sb.WriteString(replacement)
				i += tLen - 1
				continue
			}
		}

		sb.WriteByte(ch)
	}

	return sb.String()
}

func (f *VMFrame) execRelQuery(sql string, queryData *proc.RelQueryData) ([]map[string]any, []string, [][]any, error) {
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
			return nil, nil, nil, fmt.Errorf("planner parse error in OpRelQuery: %w", err)
		}
		if queryData != nil {
			queryData.SetCachedQueryInfo(parsed.Clone())
		}
		qInfo = parsed
	}

	if qInfo.WhereClause != "" {
		qInfo.WhereClause = f.bindWhereVariables(qInfo.WhereClause)
	}

	getTable := f.getTableGetter()
	if getTable == nil {
		return nil, nil, nil, fmt.Errorf("no table getter configured for relational queries")
	}

	leftTable := cleanSymbol(qInfo.TableName)
	leftSchema, err := getTable(leftTable)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("table %q not found: %w", leftTable, err)
	}

	// Fast-path Point-Lookup Shortcut: bypass Volcano CBO and iterators for single-row PK lookups
	if f.tx != nil && len(qInfo.Joins) == 0 && qInfo.Join == nil && len(qInfo.GroupByCols) == 0 &&
		!qInfo.HasAggregates && !qInfo.Distinct && len(qInfo.WindowDefs) == 0 && len(qInfo.CTEs) == 0 &&
		qInfo.OrderByClause == "" && leftSchema.HasPrimaryKey() {
		if rows, cols, ok := tryPointLookup(f.tx, qInfo, leftSchema); ok {
			var vals [][]any
			for _, r := range rows {
				rowVals := make([]any, len(cols))
				for ci, col := range cols {
					rowVals[ci] = r[col]
				}
				vals = append(vals, rowVals)
			}
			return rows, cols, vals, nil
		}
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
			return nil, nil, nil, fmt.Errorf("joined table %q not found: %w", rightTable, err)
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
		return nil, nil, nil, fmt.Errorf("failed to build volcano executor: %w", err)
	}

	ctx := context.Background()
	if err := exec.Open(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf("volcano execution open error: %w", err)
	}
	defer exec.Close()

	var rows []map[string]any
	var values [][]any
	for {
		tuple, err := exec.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, nil, err
		}
		tuple.Materialize()
		vals := make([]any, len(tuple.Values))
		copy(vals, tuple.Values)
		for vi, v := range vals {
			if dec, ok := v.(storage.Decimal); ok {
				vals[vi] = dec.String()
			}
		}
		values = append(values, vals)
		rows = append(rows, tuple.ToMap())
	}

	cols := exec.Schema()
	if len(cols) == 0 && len(rows) > 0 {
		for k := range rows[0] {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}

	return rows, cols, values, nil
}

func (f *VMFrame) getVar(name string) (storage.Value, bool) {
	if strings.HasPrefix(name, "@@") {
		clean := cleanSymbol(name)
		if strings.EqualFold(name, "@@ROWCOUNT") || strings.EqualFold(clean, "ROWCOUNT") || strings.EqualFold(clean, "@ROWCOUNT") {
			return storage.IntValue(f.rowCount), true
		}
		if strings.EqualFold(name, "@@FETCH_STATUS") || strings.EqualFold(clean, "FETCH_STATUS") || strings.EqualFold(clean, "@FETCH_STATUS") {
			return storage.IntValue(int64(f.fetchStatus)), true
		}
		if strings.EqualFold(name, "@@CURSOR_ROWS") || strings.EqualFold(clean, "CURSOR_ROWS") || strings.EqualFold(clean, "@CURSOR_ROWS") {
			return storage.IntValue(f.cursorRows), true
		}
		if strings.EqualFold(name, "@@TRANCOUNT") || strings.EqualFold(clean, "TRANCOUNT") || strings.EqualFold(clean, "@TRANCOUNT") {
			return storage.IntValue(int64(f.tranCount)), true
		}
		if strings.EqualFold(name, "@@SPID") || strings.EqualFold(clean, "SPID") || strings.EqualFold(clean, "@SPID") {
			return storage.IntValue(50), true
		}
		if strings.EqualFold(name, "@@ERROR") || strings.EqualFold(clean, "ERROR") || strings.EqualFold(clean, "@ERROR") {
			return storage.IntValue(int64(f.lastError)), true
		}
		if strings.EqualFold(name, "@@SERVERNAME") || strings.EqualFold(clean, "SERVERNAME") || strings.EqualFold(clean, "@SERVERNAME") {
			return storage.StringValue("LIONSQL"), true
		}
		if strings.EqualFold(name, "@@VERSION") || strings.EqualFold(clean, "VERSION") || strings.EqualFold(clean, "@VERSION") {
			return storage.StringValue("Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) - LionSQL Engine"), true
		}
		if strings.EqualFold(name, "@@IDENTITY") || strings.EqualFold(clean, "IDENTITY") || strings.EqualFold(clean, "@IDENTITY") {
			if f.lastIdentity > 0 {
				return storage.IntValue(f.lastIdentity), true
			}
			return storage.NullValue(storage.TypeInt), true
		}
	} else if strings.EqualFold(name, "SCOPE_IDENTITY") || strings.EqualFold(name, "SCOPE_IDENTITY()") {
		if f.lastIdentity > 0 {
			return storage.IntValue(f.lastIdentity), true
		}
		return storage.NullValue(storage.TypeInt), true
	} else if strings.EqualFold(name, "ERROR_NUMBER()") || strings.EqualFold(name, "ERROR_NUMBER") {
		return storage.IntValue(int64(f.lastError)), true
	}

	clean := cleanSymbol(name)
	if f.prog != nil && f.prog.VarSlots != nil {
		if slot, ok := f.prog.VarSlots[clean]; ok && int(slot) >= 0 && int(slot) < len(f.slots) {
			val := f.slots[slot]
			if !val.IsNull || val.Type != storage.TypeUnknown {
				return val, true
			}
		}
		lower := strings.ToLower(clean)
		if slot, ok := f.prog.VarSlots[lower]; ok && int(slot) >= 0 && int(slot) < len(f.slots) {
			val := f.slots[slot]
			if !val.IsNull || val.Type != storage.TypeUnknown {
				return val, true
			}
		}
	}

	if f.vars != nil {
		if v, ok := f.vars[clean]; ok {
			return v, true
		}
		if v, ok := f.vars[strings.ToLower(clean)]; ok {
			return v, true
		}
	}
	return storage.NullValue(storage.TypeUnknown), false
}

func (f *VMFrame) setVar(name string, val storage.Value) {
	clean := cleanSymbol(name)
	slot := int32(-1)
	if f.prog != nil && f.prog.VarSlots != nil {
		if s, ok := f.prog.VarSlots[clean]; ok {
			slot = s
		} else if s, ok := f.prog.VarSlots[strings.ToLower(clean)]; ok {
			slot = s
		}
	}

	if slot >= 0 && int(slot) < len(f.slots) {
		f.slots[slot] = val
		f.evalCtxDirty = true
		return
	}

	if f.vars == nil {
		f.vars = make(map[string]storage.Value, 8)
	}
	f.vars[clean] = val
	lower := strings.ToLower(clean)
	f.vars[lower] = val
	f.evalCtxDirty = true
}

func (f *VMFrame) fireTriggers(tableName, event string, inserted, deleted []map[string]any) error {
	if isTransientTable(tableName) {
		return nil
	}
	if f.callDepth >= 32 {
		return fmt.Errorf("maximum trigger recursion depth (32) exceeded on table %q", tableName)
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
		subFrame := f.executor.newFrame(trg.Program, f.tx)
		subFrame.callDepth = f.callDepth + 1
		for k, v := range transients {
			subFrame.transientTables[k] = v
		}
		_, err := subFrame.Execute(0)
		logs := subFrame.logs
		f.executor.releaseFrame(subFrame)

		if len(logs) > 0 {
			f.logs = append(f.logs, logs...)
		}
		if err != nil {
			return fmt.Errorf("trigger %s error: %w", trg.Name, err)
		}
	}
	return nil
}

func (f *VMFrame) execInsertSelect(data *proc.DMLData) (int64, error) {
	if data.Select == nil {
		return 0, nil
	}
	if f.executor == nil {
		return 0, fmt.Errorf("executor not initialized")
	}
	qe := f.executor.getQueryExecutor()
	if qe == nil {
		return 0, fmt.Errorf("query executor not configured for INSERT INTO ... SELECT in VM")
	}

	selectSQL := data.Select.String()
	if len(f.tableVars) > 0 {
		for varName, physTable := range f.tableVars {
			selectSQL = sqlutil.SubstituteParamOutsideQuotes(selectSQL, "@"+strings.TrimPrefix(varName, "@"), "["+physTable+"]")
		}
	}
	rows, cols, err := qe(f.ctx, selectSQL, f.tx, f.getStorageVarsMap())
	if err != nil {
		return 0, fmt.Errorf("INSERT INTO ... SELECT query execution failed: %w", err)
	}

	if len(rows) == 0 {
		return 0, nil
	}

	targetTable := f.resolveTable(data.Table)
	tableSchema, err := f.getTableSchema(targetTable)
	if err != nil {
		return 0, fmt.Errorf("table %q not found: %w", data.Table, err)
	}

	targetCols := data.Columns
	if len(targetCols) == 0 {
		for _, c := range tableSchema.Columns {
			if !c.Identity {
				targetCols = append(targetCols, c.Name)
			}
		}
	}

	if len(cols) != len(targetCols) {
		return 0, fmt.Errorf("column count mismatch: target table %q expects %d columns, but SELECT produced %d columns", data.Table, len(targetCols), len(cols))
	}

	hasTriggers := f.executor != nil && f.executor.HasTriggers(targetTable, "INSERT")
	var newRows []map[string]any
	if hasTriggers {
		newRows = make([]map[string]any, 0, len(rows))
	}

	insertedCount := int64(0)
	for _, row := range rows {
		rec := make(storage.Record, len(targetCols))
		rowMap := make(map[string]any, len(targetCols))

		for i, targetCol := range targetCols {
			srcCol := cols[i]
			val := row[srcCol]
			stVal := toStorageValue(val)
			if colSchema, _, ok := tableSchema.FindColumn(targetCol); ok {
				targetCol = colSchema.Name
				if colSchema.Type != stVal.Type {
					stVal = proc.ParseLiteralDefault(stVal.String(), colSchema.Type)
				}
			}
			rec.Set(targetCol, stVal)
			if hasTriggers {
				rowMap[targetCol] = valToAny(stVal)
			}
		}

		// Apply default values for missing columns
		for _, col := range tableSchema.Columns {
			if _, exists := rec[col.Name]; !exists && col.Default != "" {
				rec[col.Name] = proc.ParseLiteralDefault(col.Default, col.Type)
			}
		}

		if err := f.tx.Insert(targetTable, rec); err != nil {
			return insertedCount, fmt.Errorf("insert error into %s: %w", data.Table, err)
		}
		insertedCount++
		if hasTriggers {
			newRows = append(newRows, rowMap)
		}
	}

	if hasTriggers && len(newRows) > 0 {
		if err := f.fireTriggers(data.Table, "INSERT", newRows, nil); err != nil {
			return insertedCount, fmt.Errorf("trigger error on %s: %w", data.Table, err)
		}
	}

	return insertedCount, nil
}

func (f *VMFrame) execUpdateWhere(data *proc.DMLData) (int64, error) {
	targetTable := f.resolveTable(data.Table)
	if schema, err := f.getTableSchema(targetTable); err == nil && schema != nil {
		for _, a := range data.Assignments {
			if _, _, ok := schema.FindColumn(a.Column); !ok {
				return 0, fmt.Errorf("column %q does not exist in table %q", a.Column, targetTable)
			}
		}
	}

	var pkValExpr proc.Expr
	if colName, valExpr, ok := extractSimpleEq(data.WhereExpr); ok && f.isPKCol(targetTable, colName) {
		pkValExpr = valExpr
	} else if data.WhereExpr != nil {
		eqs := extractAllEqConditions(data.WhereExpr)
		for col, expr := range eqs {
			if f.isPKCol(targetTable, col) {
				pkValExpr = expr
				break
			}
		}
	}

	if pkValExpr != nil {
		evalCtx := f.buildRowEvalMap(nil)
		pkAny, _ := proc.EvalRowExpr(pkValExpr, evalCtx, nil)
		pkVal := toStorageValue(pkAny)
		oldRec, err := f.tx.Get(targetTable, pkVal)
		if err == nil && oldRec != nil {
			oldRowMap := recordToMap(oldRec)
			rowCtx := f.buildRowEvalMap(oldRowMap)
			if data.WhereExpr != nil && !proc.EvalBoolExpr(data.WhereExpr, rowCtx, nil) {
				return 0, nil
			}
			recUpdates := make(storage.Record, len(data.Assignments))
			newRowMap := make(map[string]any, len(oldRowMap))
			for k, v := range oldRowMap {
				newRowMap[k] = v
			}
			tableSchema, _ := f.getTableSchema(targetTable)
			for _, a := range data.Assignments {
				vAny, err := proc.EvalUpdateRowExpr(a.Value, rowCtx, nil)
				if err != nil {
					return 0, err
				}
				stVal := toStorageValue(vAny)
				colName := a.Column
				if tableSchema != nil {
					if colSchema, _, ok := tableSchema.FindColumn(colName); ok {
						colName = colSchema.Name
						if colSchema.Type == storage.TypeBinary && stVal.Type == storage.TypeVarChar {
							stVal = storage.BinaryValueFromString(stVal.SVal)
						}
					}
				} else {
					for k := range oldRowMap {
						if sqlutil.EqualFoldIdent(k, colName) {
							colName = k
							break
						}
					}
				}
				recUpdates.Set(colName, stVal)
				newRowMap[colName] = valToAny(stVal)
			}
			if err := f.tx.Update(targetTable, pkVal, recUpdates); err != nil {
				return 0, fmt.Errorf("update error on %s: %w", targetTable, err)
			}
			if len(data.OutputCols) > 0 {
				if len(data.IntoVars) > 0 {
					for idx, outCol := range data.OutputCols {
						if idx < len(data.IntoVars) {
							intoVar := data.IntoVars[idx]
							if v, ok := recUpdates.Get(outCol); ok {
								f.setVar(intoVar, v)
							} else if v, ok := oldRec.Get(outCol); ok {
								f.setVar(intoVar, v)
							} else if tableSchema != nil {
								if colSchema, _, found := tableSchema.FindColumn(outCol); found {
									if v, ok := recUpdates.Get(colSchema.Name); ok {
										f.setVar(intoVar, v)
									} else if v, ok := oldRec.Get(colSchema.Name); ok {
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
						if v, ok := recUpdates.Get(outCol); ok {
							stVal = v
						} else if v, ok := oldRec.Get(outCol); ok {
							stVal = v
						} else if tableSchema != nil {
							if colSchema, _, found := tableSchema.FindColumn(outCol); found {
								if v, ok := recUpdates.Get(colSchema.Name); ok {
									stVal = v
								} else if v, ok := oldRec.Get(colSchema.Name); ok {
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
			if err := f.fireTriggers(targetTable, "UPDATE", []map[string]any{newRowMap}, []map[string]any{oldRowMap}); err != nil {
				return 0, fmt.Errorf("trigger error on %s: %w", targetTable, err)
			}
			return 1, nil
		}
		return 0, nil
	}

	var cursor storage.Cursor
	var err error

	if idxName, lowVal, highVal, ok := f.findMatchingIndex(targetTable, data.WhereExpr); ok {
		cursor, err = f.tx.IndexScan(targetTable, idxName, lowVal, highVal)
	}

	if cursor == nil || err != nil {
		cursor, err = f.tx.Scan(targetTable)
		if err != nil {
			return 0, fmt.Errorf("updateWhere scan error on %s: %w", targetTable, err)
		}
	}
	defer cursor.Close()

	tableSchema, _ := f.getTableSchema(targetTable)
	hasTriggers := f.executor != nil && f.executor.HasTriggers(targetTable, "UPDATE")

	type updateItem struct {
		pk      storage.Value
		updates storage.Record
		oldRow  map[string]any
		newRow  map[string]any
	}
	var toUpdate []updateItem

	// P-2 Optimization: Lazy decoding & selective filter for RawTupleCursor
	fastChecks := f.extractFastEqConditions(tableSchema, data.WhereExpr)
	rawCur, hasRaw := cursor.(storage.RawTupleCursor)

	for cursor.Next() {
		if hasRaw && canSkipRawTuple(rawCur, tableSchema, fastChecks) {
			continue
		}

		rec, err := cursor.Record()
		if err != nil {
			return 0, fmt.Errorf("failed to read record during UPDATE scan: %w", err)
		}
		rowMap := recordToMap(rec)

		var rowContext map[string]any
		if data.WhereExpr != nil {
			rowContext = f.buildRowEvalMap(rowMap)
			if !proc.EvalBoolExpr(data.WhereExpr, rowContext, nil) {
				continue
			}
		}

		var pkVal storage.Value
		var hasPk bool
		if tableSchema != nil {
			pkVal, hasPk = tableSchema.ResolveRecordPK(rec)
		}
		if !hasPk {
			if v, ok := rec.Get("id"); ok {
				pkVal = v
				hasPk = true
			} else if v, ok := rec.Get(storage.SyntheticPKRowIDName); ok {
				pkVal = v
				hasPk = true
			}
		}

		if rowContext == nil {
			rowContext = f.buildRowEvalMap(rowMap)
		}

		recUpdates := make(storage.Record, len(data.Assignments))
		var newRowMap map[string]any
		if hasTriggers {
			newRowMap = make(map[string]any, len(rowMap))
			for k, v := range rowMap {
				newRowMap[k] = v
			}
		}

		for _, assign := range data.Assignments {
			val, err := proc.EvalUpdateRowExpr(assign.Value, rowContext, nil)
			if err != nil {
				return 0, err
			}
			stVal := toStorageValue(val)
			colName := assign.Column
			if tableSchema != nil {
				if colSchema, _, ok := tableSchema.FindColumn(colName); ok {
					colName = colSchema.Name
					if colSchema.Type == storage.TypeBinary && stVal.Type == storage.TypeVarChar {
						stVal = storage.BinaryValueFromString(stVal.SVal)
					}
				}
			} else {
				for k := range rowMap {
					if sqlutil.EqualFoldIdent(k, colName) {
						colName = k
						break
					}
				}
			}
			recUpdates.Set(colName, stVal)
			if hasTriggers {
				newRowMap[colName] = valToAny(stVal)
			}
		}

		if hasPk {
			item := updateItem{
				pk:      pkVal,
				updates: recUpdates,
			}
			if hasTriggers {
				item.oldRow = rowMap
				item.newRow = newRowMap
			}
			toUpdate = append(toUpdate, item)
		}
	}

	updatedCount := int64(0)
	var oldRows []map[string]any
	var newRows []map[string]any
	if hasTriggers {
		oldRows = make([]map[string]any, 0, len(toUpdate))
		newRows = make([]map[string]any, 0, len(toUpdate))
	}
	for _, item := range toUpdate {
		if err := f.tx.Update(targetTable, item.pk, item.updates); err == nil {
			updatedCount++
			if hasTriggers {
				oldRows = append(oldRows, item.oldRow)
				newRows = append(newRows, item.newRow)
			}
		}
	}

	if hasTriggers && updatedCount > 0 {
		if err := f.fireTriggers(targetTable, "UPDATE", newRows, oldRows); err != nil {
			return 0, fmt.Errorf("trigger error on %s: %w", targetTable, err)
		}
	}
	return updatedCount, nil
}

func (f *VMFrame) execDeleteWhere(data *proc.DMLData) (int64, error) {
	targetTable := f.resolveTable(data.Table)
	if colName, valExpr, ok := extractSimpleEq(data.WhereExpr); ok && f.isPKCol(targetTable, colName) {
		evalCtx := f.buildRowEvalMap(nil)
		pkAny, _ := proc.EvalRowExpr(valExpr, evalCtx, nil)
		pkVal := toStorageValue(pkAny)
		oldRec, err := f.tx.Get(targetTable, pkVal)
		if err == nil && oldRec != nil {
			if err := f.tx.Delete(targetTable, pkVal); err != nil {
				return 0, fmt.Errorf("delete error on %s: %w", targetTable, err)
			}
			if err := f.fireTriggers(targetTable, "DELETE", nil, []map[string]any{recordToMap(oldRec)}); err != nil {
				return 0, fmt.Errorf("trigger error on %s: %w", targetTable, err)
			}
			return 1, nil
		}
		return 0, nil
	}

	var cursor storage.Cursor
	var err error

	if idxName, lowVal, highVal, ok := f.findMatchingIndex(targetTable, data.WhereExpr); ok {
		cursor, err = f.tx.IndexScan(targetTable, idxName, lowVal, highVal)
	}

	if cursor == nil || err != nil {
		cursor, err = f.tx.Scan(targetTable)
		if err != nil {
			return 0, fmt.Errorf("deleteWhere scan error on %s: %w", targetTable, err)
		}
	}
	defer cursor.Close()

	tableSchema, _ := f.getTableSchema(targetTable)
	hasTriggers := f.executor != nil && f.executor.HasTriggers(targetTable, "DELETE")

	type delItem struct {
		pk  storage.Value
		row map[string]any
	}
	var toDelete []delItem

	// P-2 Optimization: Lazy decoding & selective filter for RawTupleCursor
	fastChecks := f.extractFastEqConditions(tableSchema, data.WhereExpr)
	rawCur, hasRaw := cursor.(storage.RawTupleCursor)

	for cursor.Next() {
		if hasRaw && canSkipRawTuple(rawCur, tableSchema, fastChecks) {
			continue
		}

		rec, err := cursor.Record()
		if err != nil {
			return 0, fmt.Errorf("failed to read record during DELETE scan: %w", err)
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
		if tableSchema != nil {
			pkVal, hasPk = tableSchema.ResolveRecordPK(rec)
		}
		if !hasPk {
			if v, ok := rec.Get("id"); ok {
				pkVal = v
				hasPk = true
			} else if v, ok := rec.Get(storage.SyntheticPKRowIDName); ok {
				pkVal = v
				hasPk = true
			}
		}
		if hasPk {
			item := delItem{pk: pkVal}
			if hasTriggers {
				item.row = rowMap
			}
			toDelete = append(toDelete, item)
		}
	}

	deletedCount := int64(0)
	var deletedRows []map[string]any
	if hasTriggers {
		deletedRows = make([]map[string]any, 0, len(toDelete))
	}
	for _, item := range toDelete {
		if err := f.tx.Delete(targetTable, item.pk); err == nil {
			deletedCount++
			if hasTriggers {
				deletedRows = append(deletedRows, item.row)
			}
		}
	}

	if hasTriggers && len(deletedRows) > 0 {
		if err := f.fireTriggers(targetTable, "DELETE", nil, deletedRows); err != nil {
			return 0, fmt.Errorf("trigger error on %s: %w", targetTable, err)
		}
	}
	return deletedCount, nil
}

func (f *VMFrame) execTruncate(tableName string) error {
	tblName := cleanSymbol(tableName)
	if f.tx == nil {
		return fmt.Errorf("no active transaction to truncate %s", tblName)
	}

	cursor, err := f.tx.Scan(tblName)
	if err != nil {
		return fmt.Errorf("truncate scan error on %s: %w", tblName, err)
	}
	defer cursor.Close()

	tableSchema, _ := f.getTableSchema(tblName)
	isCompPK := tableSchema != nil && tableSchema.IsCompositePK()
	var pkColName string
	if !isCompPK && tableSchema != nil {
		if pkCol, perr := tableSchema.PrimaryKeyColumn(); perr == nil && pkCol != nil {
			pkColName = pkCol.Name
		}
	}

	var pks []storage.Value
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			return fmt.Errorf("failed to read record during TRUNCATE scan: %w", err)
		}
		if rec == nil {
			continue
		}
		if isCompPK {
			if pkVal, ok := tableSchema.ResolveRecordPK(rec); ok {
				pks = append(pks, pkVal)
			}
			continue
		}
		if pkColName != "" {
			if v, ok := rec[pkColName]; ok {
				pks = append(pks, v)
				continue
			}
		}
		if v, ok := rec["id"]; ok {
			pks = append(pks, v)
		}
	}

	for _, pk := range pks {
		if err := f.tx.Delete(tblName, pk); err != nil {
			return fmt.Errorf("truncate delete error on %s: %w", tblName, err)
		}
	}

	if f.executor != nil {
		f.executor.SetLastIdentity(tblName, 0)
	}
	f.rowCount = int64(len(pks))
	return nil
}

func (f *VMFrame) execSelectInto(data *proc.SelectIntoData) (storage.Value, error) {
	targetTable := f.resolveTable(data.Table)
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

	if data.WhereCol != "" && data.WhereVal != nil {
		var pkVal storage.Value
		if v, ok := f.evalRowExprFast(data.WhereVal); ok {
			pkVal = v
		} else {
			evalCtx := f.buildRowEvalMap(nil)
			pkAny, _ := proc.EvalRowExpr(data.WhereVal, evalCtx, nil)
			pkVal = toStorageValue(pkAny)
		}
		if f.isPKCol(targetTable, data.WhereCol) {
			if rec, err := f.tx.Get(targetTable, pkVal); err == nil && rec != nil {
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

	cursor, err := f.tx.Scan(targetTable)
	if err != nil {
		return storage.NullValue(storage.TypeUnknown), err
	}
	defer cursor.Close()

	// P-2 Optimization: Lazy decoding & selective filter for RawTupleCursor
	tableSchema, _ := f.getTableSchema(targetTable)
	fastChecks := f.extractFastEqConditions(tableSchema, data.WhereVal)
	rawCur, hasRaw := cursor.(storage.RawTupleCursor)

	for cursor.Next() {
		if hasRaw && canSkipRawTuple(rawCur, tableSchema, fastChecks) {
			continue
		}

		rec, err := cursor.Record()
		if err != nil {
			return storage.NullValue(storage.TypeUnknown), fmt.Errorf("failed to read record during scan: %w", err)
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
			if schema.IsCompositePK() {
				return false
			}
			for _, c := range schema.Columns {
				if c.PrimaryKey {
					return strings.EqualFold(c.Name, clean)
				}
			}
		}
	}
	return strings.EqualFold(clean, "id")
}

func extractAllEqConditions(expr proc.Expr) map[string]proc.Expr {
	res := make(map[string]proc.Expr)
	var walk func(e proc.Expr)
	walk = func(e proc.Expr) {
		if e == nil {
			return
		}
		if bin, ok := e.(*proc.BinaryExpr); ok {
			if bin.Op == proc.TokenAnd {
				walk(bin.Left)
				walk(bin.Right)
				return
			}
			if bin.Op == proc.TokenEq {
				if colRef, ok := bin.Left.(*proc.ColumnRefExpr); ok {
					res[sqlutil.CanonicalIdent(colRef.Name)] = bin.Right
				} else if colRef, ok := bin.Right.(*proc.ColumnRefExpr); ok {
					res[sqlutil.CanonicalIdent(colRef.Name)] = bin.Left
				}
			}
		}
	}
	walk(expr)
	return res
}

func (f *VMFrame) findMatchingIndex(tableName string, whereExpr proc.Expr) (string, *storage.Value, *storage.Value, bool) {
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

	evalCtx := f.buildRowEvalMap(nil)

	// 1. Check composite indexes when all composite columns have equality conditions
	eqMap := extractAllEqConditions(whereExpr)
	if len(eqMap) > 1 {
		for _, idx := range schema.Indexes {
			if len(idx.Columns) > 1 {
				allMatched := true
				var vals []storage.Value
				for _, col := range idx.Columns {
					valExpr, ok := eqMap[sqlutil.CanonicalIdent(col)]
					if !ok {
						allMatched = false
						break
					}
					vAny, err := proc.EvalRowExpr(valExpr, evalCtx, nil)
					if err != nil {
						allMatched = false
						break
					}
					vals = append(vals, toStorageValue(vAny))
				}
				if allMatched && len(vals) == len(idx.Columns) {
					var sb strings.Builder
					for i, v := range vals {
						if i > 0 {
							sb.WriteByte(0)
						}
						sb.WriteString(storage.EncodeIndexKeyPart(v))
					}
					encVal := storage.StringValue(sb.String())
					return idx.Name, &encVal, &encVal, true
				}
			}
		}
	}

	// 2. Check single-column indexes (len(idx.Columns) == 1) for equality or BETWEEN
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
					if len(idx.Columns) == 1 && sqlutil.EqualFoldIdent(idx.Columns[0], colName) {
						if vAny, err := proc.EvalRowExpr(valExpr, evalCtx, nil); err == nil {
							stVal := toStorageValue(vAny)
							return idx.Name, &stVal, &stVal, true
						}
					}
				}
			}
		}
	case *proc.BetweenExpr:
		if colRef, ok := e.Expr.(*proc.ColumnRefExpr); ok {
			colName := cleanSymbol(colRef.Name)
			for _, idx := range schema.Indexes {
				if len(idx.Columns) == 1 && sqlutil.EqualFoldIdent(idx.Columns[0], colName) {
					var lowVal, highVal *storage.Value
					if vLow, err := proc.EvalRowExpr(e.Low, evalCtx, nil); err == nil {
						sv := toStorageValue(vLow)
						lowVal = &sv
					}
					if vHigh, err := proc.EvalRowExpr(e.High, evalCtx, nil); err == nil {
						sv := toStorageValue(vHigh)
						highVal = &sv
					}
					return idx.Name, lowVal, highVal, true
				}
			}
		}
	}

	return "", nil, nil, false
}

func (f *VMFrame) scanCursorRows(tableName string, whereExpr proc.Expr, userCols []string) ([]storage.Record, []string, error) {
	var cursor storage.Cursor
	var err error

	if idxName, lowVal, highVal, ok := f.findMatchingIndex(tableName, whereExpr); ok {
		cursor, err = f.tx.IndexScan(tableName, idxName, lowVal, highVal)
	}

	if cursor == nil || err != nil {
		cursor, err = f.tx.Scan(tableName)
		if err != nil {
			return nil, nil, err
		}
	}
	defer cursor.Close()

	// P-2 Optimization: Lazy decoding & selective filter for RawTupleCursor
	tableSchema, _ := f.getTableSchema(tableName)
	fastChecks := f.extractFastEqConditions(tableSchema, whereExpr)
	rawCur, hasRaw := cursor.(storage.RawTupleCursor)

	var result []storage.Record
	for cursor.Next() {
		if hasRaw && canSkipRawTuple(rawCur, tableSchema, fastChecks) {
			continue
		}

		rec, err := cursor.Record()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read record during cursor scan: %w", err)
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

	cols := userCols
	if len(cols) == 0 || (len(cols) == 1 && cols[0] == "*") {
		if f.executor != nil {
			if tg := f.executor.getTableGetter(); tg != nil {
				if schema, err := tg(tableName); err == nil && schema != nil {
					cols = make([]string, len(schema.Columns))
					for i, c := range schema.Columns {
						cols[i] = c.Name
					}
				}
			}
		}
		if (len(cols) == 0 || (len(cols) == 1 && cols[0] == "*")) && len(result) > 0 {
			cols = make([]string, 0, len(result[0]))
			for k := range result[0] {
				cols = append(cols, k)
			}
			sort.Strings(cols)
		}
	}

	return result, cols, nil
}

func (f *VMFrame) sortCursorRows(rows []storage.Record, orderBy []proc.OrderByItem) {
	sort.SliceStable(rows, func(i, j int) bool {
		rA := rows[i]
		rB := rows[j]
		mapA := f.buildRowEvalMap(recordToMap(rA))
		mapB := f.buildRowEvalMap(recordToMap(rB))
		for _, ob := range orderBy {
			rawA, _ := proc.EvalRowExpr(ob.Expr, mapA, nil)
			rawB, _ := proc.EvalRowExpr(ob.Expr, mapB, nil)
			valA := toStorageValue(rawA)
			valB := toStorageValue(rawB)
			cmp := valA.Compare(valB)
			if cmp != 0 {
				if ob.Desc {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return false
	})
}

func (f *VMFrame) syncEvalCtx() {
	if !f.evalCtxDirty {
		return
	}
	f.evalCtxDirty = false
	if f.evalCtx == nil {
		f.evalCtx = make(map[string]any, 32)
	} else {
		clear(f.evalCtx)
	}
	// 1. Populate from slots
	if f.prog != nil && len(f.prog.VarNames) > 0 {
		for slot, varName := range f.prog.VarNames {
			if slot < len(f.slots) {
				stVal := f.slots[slot]
				if !stVal.IsNull || stVal.Type != storage.TypeUnknown {
					anyVal := valToAny(stVal)
					clean := cleanSymbol(varName)
					lower := strings.ToLower(clean)
					f.evalCtx["@"+clean] = anyVal
					f.evalCtx["@"+lower] = anyVal
					f.evalCtx[clean] = anyVal
					f.evalCtx[lower] = anyVal
				}
			}
		}
	}
	// 2. Populate from unslotted vars
	if f.vars != nil {
		for k, v := range f.vars {
			anyVal := valToAny(v)
			clean := cleanSymbol(k)
			lower := strings.ToLower(clean)
			f.evalCtx["@"+clean] = anyVal
			f.evalCtx["@"+lower] = anyVal
			f.evalCtx[clean] = anyVal
			f.evalCtx[lower] = anyVal
		}
	}
}

func (f *VMFrame) buildRowEvalMap(row map[string]any) map[string]any {
	f.syncEvalCtx()
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

// evalRowExprFast evaluates simple literal or variable expressions directly without building evalCtx map.
func (f *VMFrame) evalRowExprFast(expr proc.Expr) (storage.Value, bool) {
	if expr == nil {
		return storage.NullValue(storage.TypeUnknown), false
	}
	switch e := expr.(type) {
	case *proc.LiteralExpr:
		return e.Val, true
	case *proc.VariableExpr:
		return f.getVar(e.Name)
	default:
		return storage.NullValue(storage.TypeUnknown), false
	}
}

// evalUpdateExprFast evaluates common arithmetic assignment expressions (col = col +/- val, col = val)
// directly against oldRow values and VM slots without allocating rowCtx maps or interface boxing.
func (f *VMFrame) evalUpdateExprFast(expr proc.Expr, tableSchema *storage.TableSchema, oldRow storage.Row) (storage.Value, bool) {
	if expr == nil {
		return storage.NullValue(storage.TypeUnknown), false
	}
	switch e := expr.(type) {
	case *proc.LiteralExpr:
		return e.Val, true

	case *proc.VariableExpr:
		return f.getVar(e.Name)

	case *proc.ColumnRefExpr:
		if tableSchema != nil {
			if _, colIdx, ok := tableSchema.FindColumn(e.Name); ok && colIdx >= 0 && colIdx < len(oldRow.Values) {
				return oldRow.Values[colIdx], true
			}
		}
		return storage.NullValue(storage.TypeUnknown), false

	case *proc.BinaryExpr:
		left, okL := f.evalUpdateExprFast(e.Left, tableSchema, oldRow)
		if !okL {
			return storage.NullValue(storage.TypeUnknown), false
		}
		right, okR := f.evalUpdateExprFast(e.Right, tableSchema, oldRow)
		if !okR {
			return storage.NullValue(storage.TypeUnknown), false
		}

		// Fast integer arithmetic
		if left.Type == storage.TypeInt && right.Type == storage.TypeInt && !left.IsNull && !right.IsNull {
			switch e.Op {
			case proc.TokenPlus:
				return storage.IntValue(left.IVal + right.IVal), true
			case proc.TokenMinus:
				return storage.IntValue(left.IVal - right.IVal), true
			case proc.TokenAsterisk:
				return storage.IntValue(left.IVal * right.IVal), true
			case proc.TokenSlash, proc.TokenDiv:
				if right.IVal == 0 {
					return storage.NullValue(storage.TypeUnknown), false
				}
				return storage.IntValue(left.IVal / right.IVal), true
			case proc.TokenPercent:
				if right.IVal == 0 {
					return storage.NullValue(storage.TypeUnknown), false
				}
				return storage.IntValue(left.IVal % right.IVal), true
			}
		}

		// Fast float arithmetic
		if (left.Type == storage.TypeFloat || right.Type == storage.TypeFloat || left.Type == storage.TypeInt || right.Type == storage.TypeInt) && !left.IsNull && !right.IsNull {
			if left.Type == storage.TypeFloat || right.Type == storage.TypeFloat {
				lVal := left.FVal
				if left.Type == storage.TypeInt {
					lVal = float64(left.IVal)
				}
				rVal := right.FVal
				if right.Type == storage.TypeInt {
					rVal = float64(right.IVal)
				}
				switch e.Op {
				case proc.TokenPlus:
					return storage.FloatValue(lVal + rVal), true
				case proc.TokenMinus:
					return storage.FloatValue(lVal - rVal), true
				case proc.TokenAsterisk:
					return storage.FloatValue(lVal * rVal), true
				case proc.TokenSlash, proc.TokenDiv:
					if rVal == 0 {
						return storage.NullValue(storage.TypeUnknown), false
					}
					return storage.FloatValue(lVal / rVal), true
				}
			}
		}

		// Decimal arithmetic
		if left.Type == storage.TypeDecimal || right.Type == storage.TypeDecimal {
			lDec := left.Decimal()
			rDec := right.Decimal()
			switch e.Op {
			case proc.TokenPlus:
				return storage.DecimalValueFromDecimal(lDec.Add(rDec)), true
			case proc.TokenMinus:
				return storage.DecimalValueFromDecimal(lDec.Sub(rDec)), true
			case proc.TokenAsterisk:
				return storage.DecimalValueFromDecimal(lDec.Mul(rDec)), true
			case proc.TokenSlash, proc.TokenDiv:
				if rDec.IsZero() {
					return storage.NullValue(storage.TypeUnknown), false
				}
				if divDec, err := lDec.Div(rDec, lDec.Scale()); err == nil {
					return storage.DecimalValueFromDecimal(divDec), true
				}
				return storage.NullValue(storage.TypeUnknown), false
			}
		}

		return storage.NullValue(storage.TypeUnknown), false

	default:
		return storage.NullValue(storage.TypeUnknown), false
	}
}

func (f *VMFrame) callUDF(fnName string, argRegs []int32) (storage.Value, error) {
	if f.callDepth >= 64 {
		return storage.NullValue(storage.TypeUnknown), fmt.Errorf("maximum UDF call depth (64) exceeded (possible infinite recursion)")
	}

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
			if regIdx >= 0 && int(regIdx) < len(f.regs) {
				params[p.Name] = valToAny(f.regs[regIdx])
			}
		}
	}

	subFrame := f.executor.newFrame(udf.Program, f.tx)
	subFrame.callDepth = f.callDepth + 1
	if f.transientTables != nil {
		for k, v := range f.transientTables {
			subFrame.transientTables[k] = v
		}
	}
	subFrame.bindParams(params)
	retVal, err := subFrame.Execute(0)
	logs := subFrame.logs
	f.executor.releaseFrame(subFrame)

	if len(logs) > 0 {
		f.logs = append(f.logs, logs...)
	}
	if err != nil {
		return storage.NullValue(storage.TypeUnknown), err
	}
	return toStorageValue(retVal), nil
}

func (f *VMFrame) callProc(procName string, argRegs []int32) (storage.Value, error) {
	if f.callDepth >= 64 {
		return storage.NullValue(storage.TypeUnknown), fmt.Errorf("maximum stored procedure call depth (64) exceeded (possible infinite recursion)")
	}

	if strings.EqualFold(procName, "sp_executesql") {
		err := f.callSpExecuteSql(argRegs)
		return storage.Value{Type: storage.TypeInt, IVal: 0}, err
	}

	prog, found := f.executor.Cache().GetByName(procName)
	if !found {
		return storage.NullValue(storage.TypeUnknown), fmt.Errorf("procedure %s not found", procName)
	}

	params := make(map[string]any)
	for i, p := range prog.Params {
		if i < len(argRegs) {
			regIdx := argRegs[i]
			if regIdx >= 0 && int(regIdx) < len(f.regs) {
				params[p.Name] = valToAny(f.regs[regIdx])
			}
		}
	}

	subFrame := f.executor.newFrame(prog, f.tx)
	subFrame.callDepth = f.callDepth + 1
	if f.transientTables != nil {
		for k, v := range f.transientTables {
			subFrame.transientTables[k] = v
		}
	}
	subFrame.bindParams(params)
	retVal, err := subFrame.Execute(0)
	logs := subFrame.logs
	f.executor.releaseFrame(subFrame)

	if len(logs) > 0 {
		f.logs = append(f.logs, logs...)
	}
	return toStorageValue(retVal), err
}

func (f *VMFrame) callSpExecuteSql(argRegs []int32) error {
	if len(argRegs) == 0 {
		return fmt.Errorf("sp_executesql requires at least 1 parameter (@stmt)")
	}
	regIdx := argRegs[0]
	if regIdx < 0 || int(regIdx) >= len(f.regs) {
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
			if reg >= 0 && int(reg) < len(f.regs) {
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
		return storage.NullValue(storage.TypeUnknown)
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
	if a.IsNull || b.IsNull {
		return storage.NullValue(storage.TypeUnknown)
	}
	n1, _ := toFloatNum(a)
	n2, _ := toFloatNum(b)
	diff := n1 - n2
	if diff == math.Trunc(diff) {
		return storage.IntValue(int64(diff))
	}
	return storage.FloatValue(diff)
}

func storageMul(a, b storage.Value) storage.Value {
	if a.IsNull || b.IsNull {
		return storage.NullValue(storage.TypeUnknown)
	}
	n1, _ := toFloatNum(a)
	n2, _ := toFloatNum(b)
	prod := n1 * n2
	if prod == math.Trunc(prod) {
		return storage.IntValue(int64(prod))
	}
	return storage.FloatValue(prod)
}

func storageDiv(a, b storage.Value) (storage.Value, error) {
	if a.IsNull || b.IsNull {
		return storage.NullValue(storage.TypeUnknown), nil
	}
	if a.Type == storage.TypeInt && b.Type == storage.TypeInt {
		if b.IVal == 0 {
			return storage.NullValue(storage.TypeUnknown), nil
		}
		return storage.IntValue(a.IVal / b.IVal), nil
	}
	n1, _ := toFloatNum(a)
	n2, _ := toFloatNum(b)
	if n2 == 0 {
		return storage.NullValue(storage.TypeUnknown), nil
	}
	div := n1 / n2
	if div == math.Trunc(div) {
		return storage.IntValue(int64(div)), nil
	}
	return storage.FloatValue(div), nil
}

func storageIntDiv(a, b storage.Value) (storage.Value, error) {
	if a.IsNull || b.IsNull {
		return storage.NullValue(storage.TypeUnknown), nil
	}
	n1, _ := toFloatNum(a)
	n2, _ := toFloatNum(b)
	i1 := int64(n1)
	i2 := int64(n2)
	if i2 == 0 {
		return storage.NullValue(storage.TypeUnknown), fmt.Errorf("divide by zero error encountered")
	}
	if i1 == math.MinInt64 && i2 == -1 {
		return storage.NullValue(storage.TypeUnknown), fmt.Errorf("arithmetic overflow error encountered")
	}
	return storage.IntValue(i1 / i2), nil
}

func storageMod(a, b storage.Value) (storage.Value, error) {
	if a.IsNull || b.IsNull {
		return storage.NullValue(storage.TypeUnknown), nil
	}
	n1, _ := toFloatNum(a)
	n2, _ := toFloatNum(b)
	i1 := int64(n1)
	i2 := int64(n2)
	if i2 == 0 {
		return storage.NullValue(storage.TypeUnknown), fmt.Errorf("divide by zero error encountered")
	}
	if i1 == math.MinInt64 && i2 == -1 {
		return storage.IntValue(0), nil
	}
	return storage.IntValue(i1 % i2), nil
}

func storageNeg(a storage.Value) storage.Value {
	if a.IsNull {
		return a
	}
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
	if a.IsNull || b.IsNull {
		return false
	}
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		return n1 < n2
	}
	return valToString(a) < valToString(b)
}

func storageLte(a, b storage.Value) bool {
	if a.IsNull || b.IsNull {
		return false
	}
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		return n1 <= n2
	}
	return valToString(a) <= valToString(b)
}

func storageGt(a, b storage.Value) bool {
	if a.IsNull || b.IsNull {
		return false
	}
	n1, ok1 := toFloatNum(a)
	n2, ok2 := toFloatNum(b)
	if ok1 && ok2 {
		return n1 > n2
	}
	return valToString(a) > valToString(b)
}

func storageGte(a, b storage.Value) bool {
	if a.IsNull || b.IsNull {
		return false
	}
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
	case storage.TypeFloat, storage.TypeDecimal:
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
	case storage.TypeFloat, storage.TypeDecimal:
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
	case []byte:
		return storage.BinaryValue(val)
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
	return sqlutil.MatchLike(pattern, str, true)
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
	if strings.IndexByte(name, '.') == -1 && strings.IndexByte(name, '[') == -1 && strings.IndexByte(name, '"') == -1 && strings.IndexByte(name, '`') == -1 {
		return name
	}
	cleaned := proc.CleanSchemaPrefix(name)
	if dot := strings.IndexByte(cleaned, '.'); dot != -1 {
		lower := strings.ToLower(cleaned[:dot+1])
		if lower == "sys." || lower == "information_schema." || lower == "pg_catalog." || lower == "system." {
			return cleaned
		}
		return cleaned[strings.LastIndexByte(cleaned, '.')+1:]
	}
	return cleaned
}

var pointLookupDisallowedKeywords = []string{
	"AND", "OR", "LIKE", "IN", "BETWEEN",
}

func tryPointLookup(tx storage.Transaction, qInfo *planner.QueryInfo, schema *storage.TableSchema) ([]map[string]any, []string, bool) {
	where := strings.TrimSpace(qInfo.WhereClause)
	if where == "" {
		return nil, nil, false
	}
	for _, kw := range pointLookupDisallowedKeywords {
		if sqlutil.ContainsKeyword(where, kw, true) {
			return nil, nil, false
		}
	}
	if strings.ContainsAny(where, "<>!") {
		return nil, nil, false
	}

	parts := strings.Split(where, "=")
	if len(parts) != 2 {
		return nil, nil, false
	}

	colPart := strings.TrimSpace(parts[0])
	valPart := strings.TrimSpace(parts[1])

	if schema.IsCompositePK() {
		return nil, nil, false
	}

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil || pkCol == nil {
		return nil, nil, false
	}

	if !sqlutil.EqualFoldIdent(cleanSymbol(colPart), pkCol.Name) {
		if sqlutil.EqualFoldIdent(cleanSymbol(valPart), pkCol.Name) {
			colPart, valPart = valPart, colPart
		} else {
			return nil, nil, false
		}
	}

	var pkVal storage.Value
	if strings.HasPrefix(valPart, "'") && strings.HasSuffix(valPart, "'") && len(valPart) >= 2 {
		sVal := valPart[1 : len(valPart)-1]
		sVal = strings.ReplaceAll(sVal, "''", "'")
		pkVal = storage.StringValue(sVal)
	} else if iVal, err := strconv.ParseInt(valPart, 10, 64); err == nil {
		pkVal = storage.IntValue(iVal)
	} else {
		return nil, nil, false
	}

	row, err := tx.GetRow(schema.Name, pkVal)
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return nil, qInfo.Projections, true
		}
		return nil, nil, false
	}

	if len(row.Values) == 0 {
		return nil, qInfo.Projections, true
	}

	rowMap := make(map[string]any, len(qInfo.Projections))
	cols := make([]string, 0, len(qInfo.Projections))
	for _, proj := range qInfo.Projections {
		projClean := cleanSymbol(proj)
		if projClean == "*" {
			for i, col := range schema.Columns {
				if i < len(row.Values) {
					rowMap[col.Name] = valToAny(row.Values[i])
					cols = append(cols, col.Name)
				}
			}
			break
		}
		if col, idx, ok := schema.FindColumn(projClean); ok && idx >= 0 && idx < len(row.Values) {
			rowMap[col.Name] = valToAny(row.Values[idx])
			cols = append(cols, col.Name)
		}
	}

	return []map[string]any{rowMap}, cols, true
}
