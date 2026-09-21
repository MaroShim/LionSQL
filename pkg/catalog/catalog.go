package catalog

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"sqlion/pkg/storage"
)

var (
	ErrProcExists     = errors.New("stored procedure already exists")
	ErrProcNotFound   = errors.New("stored procedure not found")
	ErrFuncNotFound   = errors.New("user defined function not found")
	ErrTriggerNotFound = errors.New("trigger not found")
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

// Catalog manages database schemas, stored procedures, functions, and triggers.
type Catalog struct {
	mu         sync.RWMutex
	engine     storage.Engine
	procedures map[string]*Procedure
	functions  map[string]*Function
	triggers   map[string]*Trigger
}

// New creates a Catalog instance attached to a storage engine.
func New(eng storage.Engine) *Catalog {
	return &Catalog{
		engine:     eng,
		procedures: make(map[string]*Procedure),
		functions:  make(map[string]*Function),
		triggers:   make(map[string]*Trigger),
	}
}

// Engine returns the underlying storage engine.
func (c *Catalog) Engine() storage.Engine {
	return c.engine
}

// RegisterProcedure saves or updates a stored procedure in the catalog.
func (c *Catalog) RegisterProcedure(proc *Procedure) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if existing, exists := c.procedures[proc.Name]; exists {
		proc.CreatedAt = existing.CreatedAt
		proc.UpdatedAt = now
	} else {
		proc.CreatedAt = now
		proc.UpdatedAt = now
	}
	c.procedures[proc.Name] = proc
	return nil
}

// GetProcedure retrieves a stored procedure by name.
func (c *Catalog) GetProcedure(name string) (*Procedure, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	proc, exists := c.procedures[name]
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

// DropProcedure removes a procedure.
func (c *Catalog) DropProcedure(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.procedures[name]; !exists {
		return fmt.Errorf("%w: %s", ErrProcNotFound, name)
	}
	delete(c.procedures, name)
	return nil
}

// RegisterFunction saves or updates a UDF in the catalog.
func (c *Catalog) RegisterFunction(fn *Function) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if existing, exists := c.functions[fn.Name]; exists {
		fn.CreatedAt = existing.CreatedAt
		fn.UpdatedAt = now
	} else {
		fn.CreatedAt = now
		fn.UpdatedAt = now
	}
	c.functions[fn.Name] = fn
	return nil
}

// GetFunction retrieves a UDF by name.
func (c *Catalog) GetFunction(name string) (*Function, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	fn, exists := c.functions[name]
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

	if _, exists := c.functions[name]; !exists {
		return fmt.Errorf("%w: %s", ErrFuncNotFound, name)
	}
	delete(c.functions, name)
	return nil
}

// RegisterTrigger saves or updates a trigger in the catalog.
func (c *Catalog) RegisterTrigger(trg *Trigger) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if existing, exists := c.triggers[trg.Name]; exists {
		trg.CreatedAt = existing.CreatedAt
		trg.UpdatedAt = now
	} else {
		trg.CreatedAt = now
		trg.UpdatedAt = now
	}
	c.triggers[trg.Name] = trg
	return nil
}

// GetTrigger retrieves a trigger by name.
func (c *Catalog) GetTrigger(name string) (*Trigger, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	trg, exists := c.triggers[name]
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
	for _, t := range c.triggers {
		if t.Table == tableName {
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

	if _, exists := c.triggers[name]; !exists {
		return fmt.Errorf("%w: %s", ErrTriggerNotFound, name)
	}
	delete(c.triggers, name)
	return nil
}
