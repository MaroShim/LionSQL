package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"sqlion/pkg/storage"
)

// ExecutionResult captures the output and metrics of a procedure or function execution.
type ExecutionResult struct {
	ReturnValue any
	Logs        []string
	Duration    time.Duration
}

// TriggerEntry holds metadata and precompiled bytecode for a trigger.
type TriggerEntry struct {
	Name      string
	TableName string
	Event     string // "INSERT", "UPDATE", "DELETE"
	Program   *goja.Program
}

// UDFEntry holds metadata and precompiled bytecode for a user-defined function.
type UDFEntry struct {
	Name    string
	Program *goja.Program
}

// PlanCache stores precompiled Goja bytecode programs for zero-recompile execution.
type PlanCache struct {
	mu       sync.RWMutex
	programs map[string]*goja.Program
	byName   map[string]*goja.Program
}

// NewPlanCache creates an execution plan cache for Goja bytecode programs.
func NewPlanCache() *PlanCache {
	return &PlanCache{
		programs: make(map[string]*goja.Program),
		byName:   make(map[string]*goja.Program),
	}
}

// GetOrCompile retrieves a cached bytecode program or compiles and caches the JS script.
func (c *PlanCache) GetOrCompile(script string) (*goja.Program, error) {
	h := hashScript(script)

	c.mu.RLock()
	if prog, exists := c.programs[h]; exists {
		c.mu.RUnlock()
		return prog, nil
	}
	c.mu.RUnlock()

	prog, err := goja.Compile("", script, true)
	if err != nil {
		return nil, fmt.Errorf("bytecode compilation error: %w", err)
	}

	c.mu.Lock()
	c.programs[h] = prog
	c.mu.Unlock()

	return prog, nil
}

// PutByName stores a compiled bytecode program indexed by procedure or function name.
func (c *PlanCache) PutByName(name string, prog *goja.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName[cleanSymbol(name)] = prog
}

// GetByName retrieves a compiled bytecode program by procedure or function name.
func (c *PlanCache) GetByName(name string) (*goja.Program, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	prog, ok := c.byName[cleanSymbol(name)]
	return prog, ok
}

// Executor manages execution of compiled stored procedures, functions, and triggers within ambient ACID transactions.
type Executor struct {
	cache    *PlanCache
	mu       sync.RWMutex
	udfs     map[string]*UDFEntry
	triggers map[string]map[string][]*TriggerEntry // table (lower) -> event (upper) -> entries
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
	}
}

// Cache returns the underlying bytecode PlanCache.
func (e *Executor) Cache() *PlanCache {
	return e.cache
}

// RegisterUDF registers a user-defined function for preloading into execution runtimes.
func (e *Executor) RegisterUDF(name string, prog *goja.Program) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sym := cleanSymbol(name)
	e.udfs[sym] = &UDFEntry{
		Name:    sym,
		Program: prog,
	}
}

// RegisterTrigger registers a trigger for the specified table and event.
func (e *Executor) RegisterTrigger(name string, tableName string, event string, prog *goja.Program) {
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

// Run executes a precompiled bytecode program with ambient transaction semantics.
func (e *Executor) Run(prog *goja.Program, procName string, params map[string]any, session storage.Session) (*ExecutionResult, error) {
	start := time.Now()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to begin ambient transaction: %w", err)
	}

	vm := goja.New()
	bridge := NewBridge(tx)
	bridge.SetExecutor(e)

	// Attach trigger execution hook
	bridge.SetTriggerHandler(func(tableName string, event string, inserted []map[string]any, deleted []map[string]any) error {
		trgs := e.GetTriggers(tableName, event)
		if len(trgs) == 0 {
			return nil
		}
		for _, trg := range trgs {
			if _, err := vm.RunProgram(trg.Program); err != nil {
				return fmt.Errorf("failed to load trigger %s: %w", trg.Name, err)
			}
			fnVal := vm.Get(trg.Name)
			if fnVal == nil || goja.IsUndefined(fnVal) {
				return fmt.Errorf("trigger function %s not found in VM scope", trg.Name)
			}
			fn, ok := goja.AssertFunction(fnVal)
			if !ok {
				return fmt.Errorf("trigger symbol %s is not callable", trg.Name)
			}
			insVal := vm.ToValue(inserted)
			delVal := vm.ToValue(deleted)
			dbVal := vm.Get("db")
			if _, err := fn(goja.Null(), insVal, delVal, dbVal); err != nil {
				return fmt.Errorf("trigger %s error: %w", trg.Name, err)
			}
		}
		return nil
	})

	if err := bridge.Bind(vm); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("failed to bind storage bridge: %w", err)
	}

	// 1. Preload registered UDF programs
	e.mu.RLock()
	for _, udf := range e.udfs {
		if _, err := vm.RunProgram(udf.Program); err != nil {
			e.mu.RUnlock()
			_ = tx.Rollback()
			return nil, fmt.Errorf("failed to load UDF %s: %w", udf.Name, err)
		}
	}
	e.mu.RUnlock()

	// 2. Run the bytecode to define the target function in the VM scope
	_, runErr := vm.RunProgram(prog)
	if runErr != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("runtime error loading program: %w", runErr)
	}

	// 3. Retrieve target procedure or function
	cleanName := cleanSymbol(procName)
	fnVal := vm.Get(cleanName)
	if fnVal == nil || goja.IsUndefined(fnVal) {
		_ = tx.Rollback()
		return nil, fmt.Errorf("function %q not found in compiled scope", cleanName)
	}

	callable, ok := goja.AssertFunction(fnVal)
	if !ok {
		_ = tx.Rollback()
		return nil, fmt.Errorf("symbol %q is not callable", cleanName)
	}

	// 4. Prepare parameters
	paramsObj := vm.NewObject()
	for k, v := range params {
		_ = paramsObj.Set(cleanSymbol(k), v)
	}
	dbObj := vm.Get("db")

	// 5. Call procedure / function: procName(params, db)
	retVal, callErr := callable(goja.Null(), paramsObj, dbObj)
	if callErr != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("execution error (rolled back): %w", callErr)
	}

	// 6. Commit transaction on clean return
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit ambient transaction: %w", err)
	}

	var exportRet any
	if retVal != nil && !goja.IsUndefined(retVal) && !goja.IsNull(retVal) {
		exportRet = retVal.Export()
	}

	return &ExecutionResult{
		ReturnValue: exportRet,
		Logs:        bridge.Logs(),
		Duration:    time.Since(start),
	}, nil
}

func hashScript(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func cleanSymbol(name string) string {
	name = strings.TrimPrefix(name, "@")
	name = strings.Trim(name, "[]\"'`")
	if strings.HasPrefix(strings.ToLower(name), "dbo.") {
		name = name[4:]
	}
	name = strings.Trim(name, "[]\"'`")
	return name
}
