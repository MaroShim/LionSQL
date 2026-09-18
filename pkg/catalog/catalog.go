package catalog

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"sqlion/pkg/storage"
)

var (
	ErrProcExists   = errors.New("stored procedure already exists")
	ErrProcNotFound = errors.New("stored procedure not found")
)

// ParamDef defines a parameter for a stored procedure.
type ParamDef struct {
	Name string           `json:"name"` // e.g. "@FromAcc"
	Type storage.DataType `json:"type"`
}

// Procedure represents a registered stored procedure.
type Procedure struct {
	Name       string     `json:"name"`
	Params     []ParamDef `json:"params"`
	Body       string     `json:"body"`
	JSCode     string     `json:"js_code"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Catalog manages database schemas and stored procedures.
type Catalog struct {
	mu         sync.RWMutex
	engine     storage.Engine
	procedures map[string]*Procedure
}

// New creates a Catalog instance attached to a storage engine.
func New(eng storage.Engine) *Catalog {
	return &Catalog{
		engine:     eng,
		procedures: make(map[string]*Procedure),
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
