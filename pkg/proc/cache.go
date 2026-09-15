package proc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// PlanCache caches parsed stored procedure ASTs to avoid recompilation overhead.
type PlanCache struct {
	mu    sync.RWMutex
	plans map[string]*CreateProcStmt
}

// NewPlanCache creates an execution plan cache.
func NewPlanCache() *PlanCache {
	return &PlanCache{
		plans: make(map[string]*CreateProcStmt),
	}
}

// GetOrCompile retrieves a cached plan or compiles and caches the script.
func (c *PlanCache) GetOrCompile(script string) (*CreateProcStmt, error) {
	hash := hashScript(script)

	c.mu.RLock()
	if plan, exists := c.plans[hash]; exists {
		c.mu.RUnlock()
		return plan, nil
	}
	c.mu.RUnlock()

	// Parse procedure
	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		return nil, fmt.Errorf("compile error: %w", err)
	}

	c.mu.Lock()
	c.plans[hash] = plan
	// Also index by procedure name
	c.plans[plan.Name] = plan
	c.mu.Unlock()

	return plan, nil
}

// GetByName retrieves a cached plan by procedure name.
func (c *PlanCache) GetByName(name string) (*CreateProcStmt, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.plans[name]
	return p, ok
}

// PutByName caches a procedure AST by its name.
func (c *PlanCache) PutByName(name string, plan *CreateProcStmt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plans[name] = plan
}

func hashScript(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
