package proc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
)

// PlanCache caches parsed stored procedure ASTs to avoid recompilation overhead.
type PlanCache struct {
	mu     sync.RWMutex
	plans  map[string]*CreateProcStmt
	hits   atomic.Int64
	misses atomic.Int64
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
		c.hits.Add(1)
		return plan, nil
	}
	c.mu.RUnlock()
	c.misses.Add(1)

	// Parse procedure
	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		if fb := GetProcedureFallback(); fb != nil {
			if fbPlan, fbErr := fb(script); fbErr == nil && fbPlan != nil {
				plan = fbPlan
				err = nil
			}
		}
	}
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
	if ok {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	return p, ok
}

// Stats returns cumulative plan cache hits and misses.
func (c *PlanCache) Stats() (hits, misses int64) {
	return c.hits.Load(), c.misses.Load()
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
