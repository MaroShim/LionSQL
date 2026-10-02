package catalog

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
)

var (
	ErrProcExists      = errors.New("stored procedure already exists")
	ErrProcNotFound    = errors.New("stored procedure not found")
	ErrFuncExists      = errors.New("user defined function already exists")
	ErrFuncNotFound    = errors.New("user defined function not found")
	ErrTriggerExists   = errors.New("trigger already exists")
	ErrTriggerNotFound = errors.New("trigger not found")
	ErrViewExists      = errors.New("view already exists")
	ErrViewNotFound    = errors.New("view not found")
)

// ParamDef defines a parameter for a stored procedure or function.
type ParamDef struct {
	Name string           `json:"name"` // e.g. "@FromAcc"
	Type storage.DataType `json:"type"`
}

// Procedure represents a registered stored procedure.
type Procedure struct {
	Name      string     `json:"name"`
	Params    []ParamDef `json:"params"`
	Body      string     `json:"body"`
	JSCode    string     `json:"js_code"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Function represents a registered User-Defined Function (UDF) or Table-Valued Function (TVF).
type Function struct {
	Name          string           `json:"name"`
	Params        []ParamDef       `json:"params"`
	ReturnType    storage.DataType `json:"return_type"`
	IsTableValued bool             `json:"is_table_valued"`
	TableColumns  []storage.Column `json:"table_columns,omitempty"`
	Body          string           `json:"body"`
	JSCode        string           `json:"js_code"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// Trigger represents an event-driven table trigger.
type Trigger struct {
	Name      string    `json:"name"`
	Table     string    `json:"table"`
	Timing    string    `json:"timing"` // e.g. "AFTER", "INSTEAD OF"
	Events    []string  `json:"events"` // e.g. ["INSERT", "UPDATE", "DELETE"]
	Body      string    `json:"body"`
	JSCode    string    `json:"js_code"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// View represents a named SQL view backed by a SELECT query.
type View struct {
	Name      string    `json:"name"`
	Query     string    `json:"query"` // the SELECT body of the view
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Catalog manages database schemas, stored procedures, functions, triggers, and CBO statistics.
type Catalog struct {
	mu         sync.RWMutex
	engine     storage.Engine
	procedures map[string]*Procedure
	functions  map[string]*Function
	triggers   map[string]*Trigger
	views      map[string]*View
	tableStats map[string]*planner.TableStats
}

// New creates a Catalog instance attached to a storage engine.
func New(eng storage.Engine) *Catalog {
	return &Catalog{
		engine:     eng,
		procedures: make(map[string]*Procedure),
		functions:  make(map[string]*Function),
		triggers:   make(map[string]*Trigger),
		views:      make(map[string]*View),
		tableStats: make(map[string]*planner.TableStats),
	}
}

// Engine returns the underlying storage engine.
func (c *Catalog) Engine() storage.Engine {
	return c.engine
}

func cleanCatalogIdent(name string) string {
	s := strings.Trim(strings.TrimSpace(name), "[]\"'`")
	if strings.HasPrefix(strings.ToLower(s), "dbo.") {
		s = s[4:]
	}
	return strings.Trim(s, "[]\"'`")
}

// RegisterProcedure saves or updates a stored procedure in the catalog (upsert semantics).
func (c *Catalog) RegisterProcedure(proc *Procedure) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(proc.Name))
	now := time.Now()
	if existing, exists := c.procedures[key]; exists {
		proc.CreatedAt = existing.CreatedAt
		proc.UpdatedAt = now
	} else {
		proc.CreatedAt = now
		proc.UpdatedAt = now
	}
	c.procedures[key] = proc
	return nil
}

// CreateProcedure registers a new stored procedure in the catalog.
// If a procedure with the same name (case-insensitive) already exists, it returns ErrProcExists.
func (c *Catalog) CreateProcedure(proc *Procedure) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(proc.Name))
	if _, exists := c.procedures[key]; exists {
		return fmt.Errorf("%w: %s", ErrProcExists, proc.Name)
	}

	now := time.Now()
	proc.CreatedAt = now
	proc.UpdatedAt = now
	c.procedures[key] = proc
	return nil
}

// AlterProcedure updates an existing stored procedure in the catalog.
// If the procedure does not exist, it returns ErrProcNotFound.
func (c *Catalog) AlterProcedure(proc *Procedure) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(proc.Name))
	existing, exists := c.procedures[key]
	if !exists {
		return fmt.Errorf("%w: %s", ErrProcNotFound, proc.Name)
	}

	proc.CreatedAt = existing.CreatedAt
	proc.UpdatedAt = time.Now()
	c.procedures[key] = proc
	return nil
}

// GetProcedure retrieves a stored procedure by name (case-insensitive).
func (c *Catalog) GetProcedure(name string) (*Procedure, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := strings.ToLower(cleanCatalogIdent(name))
	proc, exists := c.procedures[key]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrProcNotFound, name)
	}
	cp := *proc
	return &cp, nil
}

// ListProcedures lists all registered stored procedures.
func (c *Catalog) ListProcedures() []*Procedure {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*Procedure, 0, len(c.procedures))
	for _, p := range c.procedures {
		cp := *p
		result = append(result, &cp)
	}
	return result
}

// DropProcedure removes a procedure (case-insensitive).
func (c *Catalog) DropProcedure(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(name))
	if _, exists := c.procedures[key]; !exists {
		return fmt.Errorf("%w: %s", ErrProcNotFound, name)
	}
	delete(c.procedures, key)
	return nil
}

// RegisterFunction saves or updates a UDF in the catalog.
func (c *Catalog) RegisterFunction(fn *Function) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(fn.Name))
	now := time.Now()
	if existing, exists := c.functions[key]; exists {
		fn.CreatedAt = existing.CreatedAt
		fn.UpdatedAt = now
	} else {
		fn.CreatedAt = now
		fn.UpdatedAt = now
	}
	c.functions[key] = fn
	return nil
}

// CreateFunction registers a new UDF in the catalog, returning ErrFuncExists if duplicate.
func (c *Catalog) CreateFunction(fn *Function) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(fn.Name))
	if _, exists := c.functions[key]; exists {
		return fmt.Errorf("%w: %s", ErrFuncExists, fn.Name)
	}

	now := time.Now()
	fn.CreatedAt = now
	fn.UpdatedAt = now
	c.functions[key] = fn
	return nil
}

// AlterFunction updates an existing UDF in the catalog.
func (c *Catalog) AlterFunction(fn *Function) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(fn.Name))
	existing, exists := c.functions[key]
	if !exists {
		return fmt.Errorf("%w: %s", ErrFuncNotFound, fn.Name)
	}

	fn.CreatedAt = existing.CreatedAt
	fn.UpdatedAt = time.Now()
	c.functions[key] = fn
	return nil
}

// GetFunction retrieves a UDF by name.
func (c *Catalog) GetFunction(name string) (*Function, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := strings.ToLower(cleanCatalogIdent(name))
	fn, exists := c.functions[key]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrFuncNotFound, name)
	}
	cp := *fn
	return &cp, nil
}

// ListFunctions lists all registered UDFs.
func (c *Catalog) ListFunctions() []*Function {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*Function, 0, len(c.functions))
	for _, f := range c.functions {
		cp := *f
		result = append(result, &cp)
	}
	return result
}

// DropFunction removes a UDF.
func (c *Catalog) DropFunction(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(name))
	if _, exists := c.functions[key]; !exists {
		return fmt.Errorf("%w: %s", ErrFuncNotFound, name)
	}
	delete(c.functions, key)
	return nil
}

// RegisterTrigger saves or updates a trigger in the catalog.
func (c *Catalog) RegisterTrigger(trg *Trigger) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(trg.Name))
	now := time.Now()
	if existing, exists := c.triggers[key]; exists {
		trg.CreatedAt = existing.CreatedAt
		trg.UpdatedAt = now
	} else {
		trg.CreatedAt = now
		trg.UpdatedAt = now
	}
	c.triggers[key] = trg
	return nil
}

// CreateTrigger registers a new trigger in the catalog, returning ErrTriggerExists if duplicate.
func (c *Catalog) CreateTrigger(trg *Trigger) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(trg.Name))
	if _, exists := c.triggers[key]; exists {
		return fmt.Errorf("%w: %s", ErrTriggerExists, trg.Name)
	}

	now := time.Now()
	trg.CreatedAt = now
	trg.UpdatedAt = now
	c.triggers[key] = trg
	return nil
}

// AlterTrigger updates an existing trigger in the catalog.
func (c *Catalog) AlterTrigger(trg *Trigger) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(trg.Name))
	existing, exists := c.triggers[key]
	if !exists {
		return fmt.Errorf("%w: %s", ErrTriggerNotFound, trg.Name)
	}

	trg.CreatedAt = existing.CreatedAt
	trg.UpdatedAt = time.Now()
	c.triggers[key] = trg
	return nil
}

// GetTrigger retrieves a trigger by name.
func (c *Catalog) GetTrigger(name string) (*Trigger, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := strings.ToLower(cleanCatalogIdent(name))
	trg, exists := c.triggers[key]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrTriggerNotFound, name)
	}
	cp := *trg
	return &cp, nil
}

// ListTriggers lists all registered triggers.
func (c *Catalog) ListTriggers() []*Trigger {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*Trigger, 0, len(c.triggers))
	for _, t := range c.triggers {
		cp := *t
		result = append(result, &cp)
	}
	return result
}

// GetTableTriggers retrieves all triggers attached to a given table.
func (c *Catalog) GetTableTriggers(tableName string) []*Trigger {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*Trigger, 0)
	cleanTbl := strings.ToLower(cleanCatalogIdent(tableName))
	for _, t := range c.triggers {
		if strings.ToLower(cleanCatalogIdent(t.Table)) == cleanTbl {
			cp := *t
			result = append(result, &cp)
		}
	}
	return result
}

// DropTrigger removes a trigger.
func (c *Catalog) DropTrigger(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(cleanCatalogIdent(name))
	if _, exists := c.triggers[key]; !exists {
		return fmt.Errorf("%w: %s", ErrTriggerNotFound, name)
	}
	delete(c.triggers, key)
	return nil
}

// SetTableStats stores or updates CBO statistics for a table.
func (c *Catalog) SetTableStats(tableName string, stats *planner.TableStats) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tableStats[tableName] = stats
}

// GetTableStats retrieves CBO statistics for a table, falling back to dynamic live count if unanalyzed.
func (c *Catalog) GetTableStats(tableName string) (*planner.TableStats, bool) {
	c.mu.RLock()
	stats, found := c.tableStats[tableName]
	c.mu.RUnlock()
	if found && stats != nil {
		return stats, true
	}

	// Dynamic baseline fallback using engine live row count
	type rowCounter interface {
		TableRowCount(tableName string) (int64, error)
	}
	if c.engine != nil {
		if rc, ok := c.engine.(rowCounter); ok {
			if cnt, err := rc.TableRowCount(tableName); err == nil {
				return planner.NewTableStats(tableName, cnt), true
			}
		}
	}

	return nil, false
}

// ListTableStats returns all cached table statistics.
func (c *Catalog) ListTableStats() map[string]*planner.TableStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := make(map[string]*planner.TableStats, len(c.tableStats))
	for k, v := range c.tableStats {
		res[k] = v
	}
	return res
}

// InvalidateTableStats clears cached statistics for a table upon DDL mutations.
func (c *Catalog) InvalidateTableStats(tableName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tableStats, tableName)
}

// CreateView registers a new view. Returns ErrViewExists if a view with that name already exists.
func (c *Catalog) CreateView(v *View) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(cleanCatalogIdent(v.Name))
	if _, exists := c.views[key]; exists {
		return fmt.Errorf("%w: %s", ErrViewExists, v.Name)
	}
	now := time.Now()
	v.CreatedAt = now
	v.UpdatedAt = now
	c.views[key] = v
	return nil
}

// CreateOrReplaceView registers a new view or replaces an existing one.
func (c *Catalog) CreateOrReplaceView(v *View) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(cleanCatalogIdent(v.Name))
	now := time.Now()
	if existing, exists := c.views[key]; exists {
		v.CreatedAt = existing.CreatedAt
	} else {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	c.views[key] = v
}

// GetView retrieves a view by name.
func (c *Catalog) GetView(name string) (*View, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := strings.ToLower(cleanCatalogIdent(name))
	v, exists := c.views[key]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrViewNotFound, name)
	}
	return v, nil
}

// DropView removes a view by name. Returns ErrViewNotFound if not registered.
func (c *Catalog) DropView(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(cleanCatalogIdent(name))
	if _, exists := c.views[key]; !exists {
		return fmt.Errorf("%w: %s", ErrViewNotFound, name)
	}
	delete(c.views, key)
	return nil
}

// ListViews returns all registered views.
func (c *Catalog) ListViews() []*View {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*View, 0, len(c.views))
	for _, v := range c.views {
		result = append(result, v)
	}
	return result
}
