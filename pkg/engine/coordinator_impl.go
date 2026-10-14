package engine

import (
	"fmt"
	"os"
	"sync"
	"time"

	"sqlion/pkg/auth"
	"sqlion/pkg/catalog"
	"sqlion/pkg/execution"
	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/replication"
	"sqlion/pkg/storage"
)

// Config defines options for initializing a Coordinator.
type Config struct {
	DefaultWorkMem   int64
	SlowThreshold    time.Duration
	EnableGeneralLog bool
	AuthService      *auth.AuthService
	EnableAuth       bool
	PoolConfig       PoolConfig
}

// DefaultConfig returns default configuration parameters.
func DefaultConfig() Config {
	return Config{
		DefaultWorkMem:   execution.DefaultWorkMem,
		SlowThreshold:    DefaultSlowQueryThreshold,
		EnableGeneralLog: false,
		PoolConfig:       DefaultPoolConfig(),
	}
}

// Coordinator orchestrates SQL execution, plan caching, transaction contexts, and session pooling.
type Coordinator struct {
	catalog            *catalog.Catalog
	engine             storage.Engine
	planCache          *proc.PlanCache
	executor           *runtime.Executor
	auth               *auth.AuthService
	enableAuth         bool
	pool               *SessionPool
	sessions           map[string]*ClientSession
	sessMu             sync.RWMutex
	globalTempCreators map[string]string // lowercase table name -> creatorSessionID
	globalTempMu       sync.RWMutex
	defaultWorkMem     int64
	spidCounter        int
	metrics            *MetricsCollector
	slowQueries        *SlowQueryLogger
	generalLog         *GeneralLogger
	replication        *replication.Manager
	startTime          time.Time
}

// NewCoordinator creates a new SQL Coordinator instance.
func NewCoordinator(cat *catalog.Catalog, eng storage.Engine, cfg Config) *Coordinator {
	if cfg.DefaultWorkMem <= 0 {
		cfg.DefaultWorkMem = execution.DefaultWorkMem
	}
	if cfg.SlowThreshold <= 0 {
		cfg.SlowThreshold = DefaultSlowQueryThreshold
	}
	if cfg.AuthService == nil {
		cfg.AuthService = auth.NewAuthService(auth.Config{})
		_, _ = cfg.AuthService.EnsureDefaultSuperuser("admin@sqlion.local", "1234567890")
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	if eng != nil {
		executor.SetTableGetter(eng.GetTable)
	}

	pool := NewSessionPool(eng, cfg.PoolConfig)

	return &Coordinator{
		catalog:            cat,
		engine:             eng,
		planCache:          proc.NewPlanCache(),
		executor:           executor,
		auth:               cfg.AuthService,
		enableAuth:         cfg.EnableAuth,
		pool:               pool,
		sessions:           make(map[string]*ClientSession),
		globalTempCreators: make(map[string]string),
		defaultWorkMem:     cfg.DefaultWorkMem,
		metrics:            NewMetricsCollector(),
		slowQueries:        NewSlowQueryLogger(cfg.SlowThreshold),
		generalLog:         NewGeneralLogger(cfg.EnableGeneralLog),
		startTime:          time.Now(),
	}
}

// Ensure Coordinator implements SQLCoordinator.
var _ SQLCoordinator = (*Coordinator)(nil)

// Catalog returns the underlying system catalog.
func (c *Coordinator) Catalog() *catalog.Catalog {
	return c.catalog
}

// StorageEngine returns the underlying storage engine.
func (c *Coordinator) StorageEngine() storage.Engine {
	return c.engine
}

// Executor returns the underlying runtime VM executor.
func (c *Coordinator) Executor() *runtime.Executor {
	return c.executor
}

// BytecodePlanCache returns the executor's bytecode plan cache.
func (c *Coordinator) BytecodePlanCache() *runtime.PlanCache {
	if c.executor != nil {
		return c.executor.Cache()
	}
	return nil
}

// PlanCache returns the AST plan cache.
func (c *Coordinator) PlanCache() *proc.PlanCache {
	return c.planCache
}

func (c *Coordinator) newPlanner() *planner.Planner {
	p := planner.NewPlanner()
	p.PartitionGetter = func(table string) []string {
		if c.engine != nil {
			if parts, err := c.engine.GetPartitions(table); err == nil {
				names := make([]string, len(parts))
				for i, pt := range parts {
					names[i] = pt.Name
				}
				return names
			}
		}
		return nil
	}
	return p
}

// Pool returns the underlying session pool.
func (c *Coordinator) Pool() *SessionPool {
	return c.pool
}

// SetPoolConfig reconfigures the session pool.
func (c *Coordinator) SetPoolConfig(cfg PoolConfig) {
	if c.pool != nil {
		if err := c.pool.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "engine: failed to close previous session pool: %v\n", err)
		}
	}
	c.pool = NewSessionPool(c.engine, cfg)
}

// Metrics returns the atomic metrics collector.
func (c *Coordinator) Metrics() *MetricsCollector {
	return c.metrics
}

// SlowQueries returns the slow query logger.
func (c *Coordinator) SlowQueries() *SlowQueryLogger {
	return c.slowQueries
}

// GeneralLog returns the general query logger.
func (c *Coordinator) GeneralLog() *GeneralLogger {
	return c.generalLog
}

// AuthService returns the authentication service.
func (c *Coordinator) AuthService() *auth.AuthService {
	return c.auth
}

// SetAuthService updates the authentication service and enforcement flag.
func (c *Coordinator) SetAuthService(authSvc *auth.AuthService, enable bool) {
	c.auth = authSvc
	c.enableAuth = enable
}

// IsAuthEnabled reports whether authentication is enforced.
func (c *Coordinator) IsAuthEnabled() bool {
	return c.enableAuth
}

// SetReplicationManager configures replication.
func (c *Coordinator) SetReplicationManager(rm *replication.Manager) {
	c.replication = rm
}

// ReplicationManager returns the configured replication manager.
func (c *Coordinator) ReplicationManager() *replication.Manager {
	return c.replication
}

// IsReadOnly reports whether this node is operating as a read-only replica.
func (c *Coordinator) IsReadOnly() bool {
	if c.replication != nil {
		return c.replication.IsReadOnly()
	}
	return false
}

// SetDefaultWorkMem sets default work_mem bytes.
func (c *Coordinator) SetDefaultWorkMem(bytes int64) {
	c.defaultWorkMem = bytes
}

// DefaultWorkMem returns the default work_mem budget.
func (c *Coordinator) DefaultWorkMem() int64 {
	return c.defaultWorkMem
}

// GetOrCreateSession returns or registers a persistent client session.
func (c *Coordinator) GetOrCreateSession(id string) (*ClientSession, error) {
	if id == "" {
		id = "default"
	}
	c.sessMu.Lock()
	defer c.sessMu.Unlock()

	if cs, exists := c.sessions[id]; exists {
		return cs, nil
	}

	var sess storage.Session
	var err error
	if c.pool != nil {
		sess, err = c.pool.AcquireSession()
	} else if c.engine != nil {
		sess, err = c.engine.NewSession()
	}
	if err != nil {
		return nil, err
	}

	if c.spidCounter < 50 {
		c.spidCounter = 50
	}
	c.spidCounter++

	cs := &ClientSession{
		ID:            id,
		SPID:          c.spidCounter,
		Session:       sess,
		Vars:          make(map[string]any),
		TableVars:     make(map[string]string),
		PreparedStmts: make(map[string]*PreparedStatement),
		User:          "dbo",
		OriginalUser:  "dbo",
	}
	c.sessions[id] = cs
	return cs, nil
}

// CloseSession gracefully closes and unregisters a client session.
func (c *Coordinator) CloseSession(id string) {
	if id == "" || id == "default" {
		return
	}
	c.sessMu.Lock()
	cs, exists := c.sessions[id]
	if exists {
		delete(c.sessions, id)
	}
	c.sessMu.Unlock()

	// Clean up global temporary tables created by this session
	c.globalTempMu.Lock()
	var toDrop []string
	for tbl, creatorID := range c.globalTempCreators {
		if creatorID == id {
			toDrop = append(toDrop, tbl)
			delete(c.globalTempCreators, tbl)
		}
	}
	c.globalTempMu.Unlock()

	if c.engine != nil {
		for _, tbl := range toDrop {
			_ = c.engine.DropTable(tbl)
		}
	}

	if exists && cs != nil {
		cs.Mu.Lock()
		if c.engine != nil {
			for _, physTable := range cs.TableVars {
				_ = c.engine.DropTable(physTable)
			}
			for _, tempTbl := range cs.TempTables {
				_ = c.engine.DropTable(tempTbl)
			}
		}
		cs.TableVars = nil
		cs.TempTables = nil
		cs.PreparedStmts = nil

		if cs.CancelFunc != nil {
			cs.CancelRequested = true
			cs.CancelFunc()
		}
		if cs.Tx != nil {
			_ = cs.Tx.Rollback()
			cs.Tx = nil
		}
		storageSess := cs.Session
		cs.Session = nil
		cs.Mu.Unlock()

		if storageSess != nil {
			if c.pool != nil {
				c.pool.ReleaseSession(storageSess)
			} else {
				_ = storageSess.Close()
			}
		}
	}
}

// GetSession returns the active ClientSession by ID, or nil if not found.
func (c *Coordinator) GetSession(id string) *ClientSession {
	c.sessMu.RLock()
	defer c.sessMu.RUnlock()
	return c.sessions[id]
}

// GetSessionTxStatus returns PostgreSQL transaction status byte:
// 'I' = Idle, 'T' = In transaction block, 'E' = In failed transaction block
func (c *Coordinator) GetSessionTxStatus(id string) byte {
	if id == "" || id == "default" {
		return 'I'
	}
	c.sessMu.RLock()
	cs, exists := c.sessions[id]
	c.sessMu.RUnlock()
	if !exists || cs == nil {
		return 'I'
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	if cs.Tx == nil {
		return 'I'
	}
	if cs.TxFailed {
		return 'E'
	}
	return 'T'
}

// SetSessionUser updates the active user principal on an interactive session.
func (c *Coordinator) SetSessionUser(id, user string) {
	if id == "" || id == "default" {
		return
	}
	c.sessMu.RLock()
	cs, exists := c.sessions[id]
	c.sessMu.RUnlock()
	if exists && cs != nil {
		cs.Mu.Lock()
		cs.User = user
		cs.OriginalUser = user
		cs.Mu.Unlock()
	}
}

// CancelSessionQuery triggers cooperative cancellation for sessionID.
func (c *Coordinator) CancelSessionQuery(sessionID string) bool {
	c.sessMu.RLock()
	cs, exists := c.sessions[sessionID]
	c.sessMu.RUnlock()

	if !exists || cs == nil {
		return false
	}

	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	if cs.CancelFunc != nil {
		cs.CancelRequested = true
		cs.CancelFunc()
		return true
	}
	return false
}

// CancelQueryBySPID cancels the active query executing on spid.
func (c *Coordinator) CancelQueryBySPID(spid int) bool {
	c.sessMu.RLock()
	var targetCS *ClientSession
	for _, cs := range c.sessions {
		if cs.SPID == spid {
			targetCS = cs
			break
		}
	}
	c.sessMu.RUnlock()

	if targetCS == nil {
		return false
	}

	targetCS.Mu.Lock()
	defer targetCS.Mu.Unlock()

	if targetCS.CancelFunc != nil {
		targetCS.CancelRequested = true
		targetCS.CancelFunc()
		return true
	}
	return false
}

// GetActiveQueries returns snapshot metadata of currently running queries.
func (c *Coordinator) GetActiveQueries() []ActiveQueryInfo {
	c.sessMu.RLock()
	defer c.sessMu.RUnlock()

	now := time.Now()
	var active []ActiveQueryInfo
	for _, cs := range c.sessions {
		cs.Mu.Lock()
		if cs.RunningQuery != "" && cs.CancelFunc != nil {
			active = append(active, ActiveQueryInfo{
				SessionID:  cs.ID,
				SPID:       cs.SPID,
				Query:      cs.RunningQuery,
				StartTime:  cs.QueryStartTime,
				DurationUS: now.Sub(cs.QueryStartTime).Microseconds(),
			})
		}
		cs.Mu.Unlock()
	}
	return active
}

// Close gracefully closes the coordinator, session pool, and active sessions.
func (c *Coordinator) Close() error {
	c.sessMu.Lock()
	sessIDs := make([]string, 0, len(c.sessions))
	for id := range c.sessions {
		sessIDs = append(sessIDs, id)
	}
	c.sessMu.Unlock()

	for _, id := range sessIDs {
		c.CloseSession(id)
	}

	var errs []error
	if c.slowQueries != nil {
		if err := c.slowQueries.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.generalLog != nil {
		if err := c.generalLog.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.pool != nil {
		if err := c.pool.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing coordinator: %v", errs)
	}
	return nil
}
