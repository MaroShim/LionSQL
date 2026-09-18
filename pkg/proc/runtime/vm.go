package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
	"sqltiger/pkg/storage"
)

// ExecutionResult captures the output and metrics of a procedure execution.
type ExecutionResult struct {
	ReturnValue any
	Logs        []string
	Duration    time.Duration
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

// PutByName stores a compiled bytecode program indexed by procedure name.
func (c *PlanCache) PutByName(name string, prog *goja.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName[name] = prog
}

// GetByName retrieves a compiled bytecode program by procedure name.
func (c *PlanCache) GetByName(name string) (*goja.Program, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	prog, ok := c.byName[name]
	return prog, ok
}

// Executor manages execution of compiled stored procedures within ambient ACID transactions.
type Executor struct {
	cache *PlanCache
}

// NewExecutor creates a procedure executor.
func NewExecutor(cache *PlanCache) *Executor {
	if cache == nil {
		cache = NewPlanCache()
	}
	return &Executor{
		cache: cache,
	}
}

// Cache returns the underlying bytecode PlanCache.
func (e *Executor) Cache() *PlanCache {
	return e.cache
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
	if err := bridge.Bind(vm); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("failed to bind storage bridge: %w", err)
	}

	// 1. Run the bytecode to define the function in the VM scope
	_, runErr := vm.RunProgram(prog)
	if runErr != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("runtime error loading program: %w", runErr)
	}

	// 2. Retrieve procedure function
	fnVal := vm.Get(procName)
	if fnVal == nil || goja.IsUndefined(fnVal) {
		_ = tx.Rollback()
		return nil, fmt.Errorf("procedure function %q not found in compiled scope", procName)
	}

	callable, ok := goja.AssertFunction(fnVal)
	if !ok {
		_ = tx.Rollback()
		return nil, fmt.Errorf("symbol %q is not callable", procName)
	}

	// 3. Prepare parameters
	paramsObj := vm.NewObject()
	for k, v := range params {
		_ = paramsObj.Set(k, v)
	}
	dbObj := vm.Get("db")

	// 4. Call procedure: procName(params, db)
	retVal, callErr := callable(goja.Null(), paramsObj, dbObj)
	if callErr != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("procedure execution error (rolled back): %w", callErr)
	}

	// 5. Commit transaction on clean return
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
