package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/replication"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// Config defines options for initializing a Coordinator.
type Config struct {
	DefaultWorkMem       int64
	SlowThreshold        time.Duration
	EnableGeneralLog     bool
	AuthService          *auth.AuthService
	EnableAuth           bool
	PoolConfig           PoolConfig
	ClientSessionIdleTTL time.Duration // Idle duration after which an inactive ClientSession releases its pinned storage session (default 5m)
	SessionSweepInterval time.Duration // Interval for checking idle ClientSessions (default 5m)
}

// DefaultConfig returns default configuration parameters.
func DefaultConfig() Config {
	return Config{
		DefaultWorkMem:       execution.DefaultWorkMem,
		SlowThreshold:        DefaultSlowQueryThreshold,
		EnableGeneralLog:     false,
		PoolConfig:           DefaultPoolConfig(),
		ClientSessionIdleTTL: 5 * time.Minute,
		SessionSweepInterval: 5 * time.Minute,
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
	pkBypassCache      map[uint64]*PKLookupPlan
	pkBypassMu         sync.RWMutex
	lastIdentities     map[string]int64
	lastIdentMu        sync.RWMutex
	schemaVersion      atomic.Uint64
	autoAnalyzer         *AutoAnalyzer
	startTime            time.Time
	tolerantDDL          atomic.Bool
	clientSessionIdleTTL time.Duration
	stopCh               chan struct{}
	bgWg                 sync.WaitGroup
	closeOnce            sync.Once
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
		_, _ = cfg.AuthService.EnsureDefaultSuperuser("admin@lionsql.local", "1234567890")
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	if eng != nil {
		executor.SetTableGetter(eng.GetTable)
		executor.SetTableManager(eng)
	}

	pool := NewSessionPool(eng, cfg.PoolConfig)

	clientIdleTTL := cfg.ClientSessionIdleTTL
	if clientIdleTTL <= 0 {
		clientIdleTTL = 5 * time.Minute
	}
	sweepInterval := cfg.SessionSweepInterval
	if sweepInterval <= 0 {
		sweepInterval = 5 * time.Minute
		if clientIdleTTL < sweepInterval {
			sweepInterval = clientIdleTTL
		}
		if sweepInterval < 10*time.Millisecond {
			sweepInterval = 10 * time.Millisecond
		}
	}

	coord := &Coordinator{
		catalog:              cat,
		engine:               eng,
		planCache:            proc.NewPlanCache(),
		executor:             executor,
		auth:                 cfg.AuthService,
		enableAuth:           cfg.EnableAuth,
		pool:                 pool,
		sessions:             make(map[string]*ClientSession),
		globalTempCreators:   make(map[string]string),
		defaultWorkMem:       cfg.DefaultWorkMem,
		metrics:              NewMetricsCollector(),
		slowQueries:          NewSlowQueryLogger(cfg.SlowThreshold),
		generalLog:           NewGeneralLogger(cfg.EnableGeneralLog),
		pkBypassCache:        make(map[uint64]*PKLookupPlan, 256),
		lastIdentities:       make(map[string]int64),
		startTime:            time.Now(),
		clientSessionIdleTTL: clientIdleTTL,
		stopCh:               make(chan struct{}),
	}

	coord.bgWg.Add(1)
	go coord.idleSessionReapLoop(sweepInterval)

	coord.autoAnalyzer = NewAutoAnalyzer(coord, DefaultAutoAnalyzeConfig())
	executor.SetIdentityProvider(coord)
	executor.SetQueryExecutor(func(ctx context.Context, sql string, tx storage.Transaction, vars map[string]storage.Value) ([]map[string]any, []string, error) {
		cs := &ClientSession{
			ID:   "internal-vm-subquery",
			User: "sa",
			Tx:   tx,
			Vars: make(map[string]any, len(vars)*2),
		}
		for k, v := range vars {
			cs.Vars[k] = v.Interface()
			if !strings.HasPrefix(k, "@") {
				cs.Vars["@"+k] = v.Interface()
			}
		}
		res, err := coord.execSelect(sql, time.Now(), cs)
		if err != nil {
			return nil, nil, err
		}
		return res.Rows, res.Columns, nil
	})
	type walHookAdder interface {
		AddWALHook(fn func(rec memengine.WALRecord))
	}
	if wha, ok := eng.(walHookAdder); ok {
		wha.AddWALHook(coord.autoAnalyzer.OnWALRecord)
	}

	return coord
}

// SetTableLastIdentity records the most recent identity value generated for a table.
func (c *Coordinator) SetTableLastIdentity(tableName string, id int64) {
	c.lastIdentMu.Lock()
	if c.lastIdentities == nil {
		c.lastIdentities = make(map[string]int64)
	}
	c.lastIdentities[strings.ToLower(tableName)] = id
	c.lastIdentMu.Unlock()
}

// GetTableLastIdentity retrieves the most recent identity value generated for a table.
func (c *Coordinator) GetTableLastIdentity(tableName string) (int64, bool) {
	c.lastIdentMu.RLock()
	defer c.lastIdentMu.RUnlock()
	if c.lastIdentities == nil {
		return 0, false
	}
	v, ok := c.lastIdentities[strings.ToLower(tableName)]
	return v, ok
}

// InvalidatePKBypassCache clears cached fast-path PK lookup plans and increments schema version.
func (c *Coordinator) InvalidatePKBypassCache() {
	c.schemaVersion.Add(1)
	c.pkBypassMu.Lock()
	c.pkBypassCache = make(map[uint64]*PKLookupPlan, 256)
	c.pkBypassMu.Unlock()
	if c.executor != nil && c.executor.Cache() != nil {
		c.executor.Cache().Clear()
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

	defaultDB := "postgres"
	dialect := DialectDefault
	if strings.HasPrefix(id, "tds-") {
		defaultDB = "master"
		dialect = DialectTSQL
	} else if strings.HasPrefix(id, "pg-") {
		dialect = DialectPostgres
	}

	cs := &ClientSession{
		ID:              id,
		SPID:            c.spidCounter,
		Dialect:         dialect,
		Session:         sess,
		Database:        defaultDB,
		Vars:            make(map[string]any),
		TableVars:       make(map[string]string),
		TempTableMap:    make(map[string]string),
		EphemeralTables: make(map[string]*EphemeralTable),
		PreparedStmts:   make(map[string]*PreparedStatement),
		Cursors:         make(map[string]*SessionCursor),
		User:            "dbo",
		OriginalUser:    "dbo",
		IsReadOnlyFn:    c.IsReadOnly,
		lastUsedAt:      time.Now(),
	}
	c.sessions[id] = cs
	return cs, nil
}

func (c *Coordinator) acquireSessionForQuery(cs *ClientSession) (storage.Session, func(), error) {
	if cs != nil {
		cs.Mu.Lock()
		cs.lastUsedAt = time.Now()
		if cs.Session != nil && !cs.sessInUse {
			cs.sessInUse = true
			sess := cs.Session
			cs.Mu.Unlock()
			return sess, func() {
				cs.Mu.Lock()
				cs.sessInUse = false
				cs.lastUsedAt = time.Now()
				drainSess, drainFn := cs.checkAndDrainClosedLocked()
				cs.Mu.Unlock()
				if drainFn != nil {
					drainFn(drainSess)
				}
			}, nil
		}
		cs.Mu.Unlock()
	}
	if c.pool != nil {
		sess, err := c.pool.AcquireSession()
		if err != nil {
			return nil, nil, err
		}
		return sess, func() { c.pool.ReleaseSession(sess) }, nil
	}
	sess, err := c.engine.NewSession()
	if err != nil {
		return nil, nil, err
	}
	return sess, func() { _ = sess.Close() }, nil
}

func (c *Coordinator) idleSessionReapLoop(interval time.Duration) {
	defer c.bgWg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.ReapIdleClientSessions(c.clientSessionIdleTTL)
		}
	}
}

// ReapIdleClientSessions detaches and returns pinned storage sessions from idle ClientSessions
// that have no active transaction (cs.Tx == nil) and no in-flight queries (!cs.sessInUse).
func (c *Coordinator) ReapIdleClientSessions(idleTTL time.Duration) int {
	if idleTTL <= 0 {
		return 0
	}
	now := time.Now()

	c.sessMu.RLock()
	snapshot := make([]*ClientSession, 0, len(c.sessions))
	for _, cs := range c.sessions {
		if cs != nil {
			snapshot = append(snapshot, cs)
		}
	}
	c.sessMu.RUnlock()

	var detached []storage.Session
	for _, cs := range snapshot {
		cs.Mu.Lock()
		if !cs.closed && cs.Tx == nil && !cs.sessInUse && cs.activeQueries == 0 && cs.Session != nil && now.Sub(cs.lastUsedAt) >= idleTTL {
			detached = append(detached, cs.Session)
			cs.Session = nil
		}
		cs.Mu.Unlock()
	}

	for _, sess := range detached {
		if c.pool != nil {
			c.pool.ReleaseSession(sess)
		} else {
			_ = sess.Close()
		}
	}
	return len(detached)
}

// CloseSession gracefully closes and unregisters a client session.
func (c *Coordinator) CloseSession(id string) {
	if id == "" {
		return
	}
	isDefault := (id == "default")

	c.sessMu.Lock()
	cs, exists := c.sessions[id]
	if exists && !isDefault {
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
		// 1. Immediately cancel in-flight queries and rollback active transaction first
		// to guarantee zero leaked storage locks and instant release of resources.
		cs.CancelQuery()

		cs.Mu.Lock()
		if cs.Tx != nil {
			_ = cs.Tx.Rollback()
			cs.Tx = nil
		}
		cs.TxFailed = false

		// 2. Snapshot table variable & temp table names to drop outside lock ([RULE:LockHierarchy])
		var toDropLocal []string
		for _, physTable := range cs.TableVars {
			toDropLocal = append(toDropLocal, physTable)
		}
		for _, tempTbl := range cs.TempTables {
			toDropLocal = append(toDropLocal, tempTbl)
		}

		cs.TableVars = nil
		cs.TempTables = nil
		cs.TempTableMap = nil
		cs.EphemeralTables = nil
		cs.PreparedStmts = nil
		if cs.Cursors != nil {
			clear(cs.Cursors)
		}

		cs.closed = true
		storageSess := cs.Session
		inFlight := cs.activeQueries > 0 || cs.sessInUse

		if !isDefault {
			if inFlight {
				// In-flight queries are actively running on this session!
				// Delegate release to the query cleanup defer so other connections
				// cannot acquire this WT_SESSION* while it is still in-flight.
				cs.releaseOnDrain = func(sess storage.Session) {
					if c.pool != nil {
						c.pool.ReleaseSession(sess)
					} else {
						_ = sess.Close()
					}
				}
				storageSess = nil // do not release immediately; wait for in-flight drain
			} else {
				cs.Session = nil
				cs.sessInUse = false
			}
		}
		cs.Mu.Unlock()

		// 3. Drop tables outside cs.Mu lock to prevent lock hierarchy deadlocks
		if c.engine != nil {
			for _, tbl := range toDropLocal {
				_ = c.engine.DropTable(tbl)
			}
		}

		if storageSess != nil && !isDefault {
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

// SetSessionDialect explicitly sets the SQL dialect family for a session.
func (c *Coordinator) SetSessionDialect(id string, dialect Dialect) {
	if id == "" || id == "default" {
		return
	}
	c.sessMu.RLock()
	cs, exists := c.sessions[id]
	c.sessMu.RUnlock()
	if exists && cs != nil {
		cs.Mu.Lock()
		cs.Dialect = dialect
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

	return cs.CancelQuery()
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

	return targetCS.CancelQuery()
}

// GetActiveQueries returns snapshot metadata of currently running queries.
func (c *Coordinator) GetActiveQueries() []ActiveQueryInfo {
	c.sessMu.RLock()
	sessions := make([]*ClientSession, 0, len(c.sessions))
	for _, cs := range c.sessions {
		if cs != nil {
			sessions = append(sessions, cs)
		}
	}
	c.sessMu.RUnlock()

	now := time.Now()
	var active []ActiveQueryInfo
	for _, cs := range sessions {
		hasCancel := cs.HasCancelFunc()
		cs.Mu.Lock()
		if cs.RunningQuery != "" && hasCancel {
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

// AutoAnalyzer returns the background auto-analyze daemon instance.
func (c *Coordinator) AutoAnalyzer() *AutoAnalyzer {
	return c.autoAnalyzer
}

// Close gracefully closes the coordinator, session pool, and active sessions.
func (c *Coordinator) Close() error {
	c.closeOnce.Do(func() {
		if c.stopCh != nil {
			close(c.stopCh)
		}
		c.bgWg.Wait()
	})

	c.sessMu.Lock()
	sessIDs := make([]string, 0, len(c.sessions))
	for id := range c.sessions {
		sessIDs = append(sessIDs, id)
	}
	c.sessMu.Unlock()

	for _, id := range sessIDs {
		c.CloseSession(id)
	}

	// Ensure default session is completely closed and released
	c.sessMu.Lock()
	if defCs, ok := c.sessions["default"]; ok && defCs != nil {
		defCs.Mu.Lock()
		if defCs.Session != nil {
			if c.pool != nil {
				c.pool.ReleaseSession(defCs.Session)
			} else {
				_ = defCs.Session.Close()
			}
			defCs.Session = nil
		}
		defCs.Mu.Unlock()
		delete(c.sessions, "default")
	}
	c.sessMu.Unlock()

	var errs []error
	if c.autoAnalyzer != nil {
		c.autoAnalyzer.Close()
	}
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

// getTable retrieves a table schema, prioritizing session-local ephemeral tables before falling back to the engine catalog.
func (c *Coordinator) getTable(cs *ClientSession, name string) (*storage.TableSchema, error) {
	if cs != nil {
		if eph := cs.GetEphemeralTable(name); eph != nil {
			return eph.Schema, nil
		}
	}
	if c.engine == nil {
		return nil, storage.ErrTableNotFound
	}
	return c.engine.GetTable(name)
}

// getTableGetter returns an execution.TableGetter closure scoped to the provided ClientSession.
func (c *Coordinator) getTableGetter(cs *ClientSession) execution.TableGetter {
	return func(name string) (*storage.TableSchema, error) {
		return c.getTable(cs, name)
	}
}

// TolerantDDL returns whether tolerant DDL mode is currently active.
func (c *Coordinator) TolerantDDL() bool {
	return c.tolerantDDL.Load()
}

// SetTolerantDDL atomically toggles the tolerant DDL bootstrap mode in memory.
func (c *Coordinator) SetTolerantDDL(enabled bool) {
	c.tolerantDDL.Store(enabled)
}


