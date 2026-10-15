package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// StatisticDef defines an extended multi-column statistics object (C-5).
type StatisticDef struct {
	Name      string    `json:"name"`
	TableName string    `json:"table_name"`
	Columns   []string  `json:"columns"`
	CreatedAt time.Time `json:"created_at"`
}

// Catalog manages database schemas, stored procedures, functions, triggers, and CBO statistics.
type Catalog struct {
	mu             sync.RWMutex
	engine         storage.Engine
	persistenceDir string
	procedures     map[string]*Procedure
	functions      map[string]*Function
	triggers       map[string]*Trigger
	views          map[string]*View
	tableStats     map[string]*planner.TableStats
	statistics     map[string]*StatisticDef
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
		statistics: make(map[string]*StatisticDef),
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
	if c.persistenceDir != "" {
		_ = c.persistTableStatsLocked()
	}
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
	if c.persistenceDir != "" {
		_ = c.persistTableStatsLocked()
	}
}

// SetPersistenceDir sets the directory where catalog statistics are persisted.
// If the directory contains existing statistics (cbo_stats.json), they are automatically loaded.
func (c *Catalog) SetPersistenceDir(dir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cleanDir := filepath.Clean(dir)
	c.persistenceDir = cleanDir
	statsFile := filepath.Join(cleanDir, "cbo_stats.json")
	if _, err := os.Stat(statsFile); err == nil {
		return c.loadTableStatsFileLocked(statsFile)
	}
	return nil
}

// PersistenceDir returns the currently configured persistence directory.
func (c *Catalog) PersistenceDir() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.persistenceDir
}

// SaveTableStatsToFile saves current table statistics atomically to targetPath.
func (c *Catalog) SaveTableStatsToFile(targetPath string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.saveStatsToPathLocked(targetPath)
}

// LoadTableStatsFromFile loads table statistics from targetPath.
func (c *Catalog) LoadTableStatsFromFile(targetPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadTableStatsFileLocked(targetPath)
}

func (c *Catalog) saveStatsToPathLocked(targetPath string) error {
	cleanPath := filepath.Clean(targetPath)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	data, err := json.MarshalIndent(c.tableStats, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize CBO table stats: %w", err)
	}
	data = append(data, '\n')

	tmpPath := fmt.Sprintf("%s.tmp.%d", cleanPath, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary stats file: %w", err)
	}

	if err := os.Rename(tmpPath, cleanPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to atomically rename stats file: %w", err)
	}
	return nil
}

func (c *Catalog) loadTableStatsFileLocked(targetPath string) error {
	cleanPath := filepath.Clean(targetPath)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return fmt.Errorf("failed to read CBO stats file: %w", err)
	}

	var statsMap map[string]*planner.TableStats
	if err := json.Unmarshal(data, &statsMap); err != nil {
		return fmt.Errorf("failed to parse CBO stats JSON: %w", err)
	}

	if c.tableStats == nil {
		c.tableStats = make(map[string]*planner.TableStats)
	}
	for k, v := range statsMap {
		c.tableStats[k] = v
	}
	return nil
}

func (c *Catalog) persistTableStatsLocked() error {
	if c.persistenceDir == "" {
		return nil
	}
	statsFile := filepath.Join(c.persistenceDir, "cbo_stats.json")
	return c.saveStatsToPathLocked(statsFile)
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

// CreateStatistics registers an extended statistics definition in the catalog (C-5).
func (c *Catalog) CreateStatistics(stat *StatisticDef) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(cleanCatalogIdent(stat.TableName) + "." + cleanCatalogIdent(stat.Name))
	c.statistics[key] = stat
	return nil
}

// DropStatistics removes an extended statistics definition from the catalog and cached table stats (C-5).
func (c *Catalog) DropStatistics(tableName, statName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cleanTbl := cleanCatalogIdent(tableName)
	cleanStat := cleanCatalogIdent(statName)

	if cleanTbl == "" {
		foundKey := ""
		for k, s := range c.statistics {
			if strings.EqualFold(s.Name, cleanStat) {
				foundKey = k
				cleanTbl = s.TableName
				break
			}
		}
		if foundKey != "" {
			delete(c.statistics, foundKey)
		}
	} else {
		key := strings.ToLower(cleanTbl + "." + cleanStat)
		delete(c.statistics, key)
	}

	if ts, ok := c.tableStats[cleanTbl]; ok && ts != nil {
		var remaining []*planner.MultiColumnStats
		for _, ms := range ts.MultiColumns {
			if !strings.EqualFold(ms.Name, cleanStat) {
				remaining = append(remaining, ms)
			}
		}
		ts.MultiColumns = remaining
		if c.persistenceDir != "" {
			_ = c.persistTableStatsLocked()
		}
	}
	return nil
}

// GetStatistics retrieves an extended statistics definition by table and stat name (C-5).
func (c *Catalog) GetStatistics(tableName, statName string) (*StatisticDef, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := strings.ToLower(cleanCatalogIdent(tableName) + "." + cleanCatalogIdent(statName))
	stat, ok := c.statistics[key]
	return stat, ok
}

// ListStatistics lists all extended statistics definitions for a given table (C-5).
func (c *Catalog) ListStatistics(tableName string) []*StatisticDef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cleanTbl := strings.ToLower(cleanCatalogIdent(tableName))
	var res []*StatisticDef
	for _, s := range c.statistics {
		if strings.ToLower(cleanCatalogIdent(s.TableName)) == cleanTbl {
			res = append(res, s)
		}
	}
	return res
}
