package engine

import (
	"context"
	"fmt"
	"log"
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
	"lionsql/pkg/tx"
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
	SessionSweepInterval time.Duration // Interval for checking idle ClientSessions (default 10s)
}

// DefaultConfig returns default configuration parameters.
func DefaultConfig() Config {
	return Config{
		DefaultWorkMem:       execution.DefaultWorkMem,
		SlowThreshold:        DefaultSlowQueryThreshold,
		EnableGeneralLog:     false,
		PoolConfig:           DefaultPoolConfig(),
		ClientSessionIdleTTL: 5 * time.Minute,
		SessionSweepInterval: 10 * time.Second,
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
	identSlots         sync.Map // lowerTableName (string) -> *TableIdentity
	schemaVersion      atomic.Uint64
	autoAnalyzer         *AutoAnalyzer
	startTime            time.Time
	tolerantDDL          atomic.Bool
	clientSessionIdleTTL atomic.Int64
	dbMailInitOnce       sync.Once
	dbMailInitErr        error
	dbMailItemCounter    atomic.Int64
	dbMailLogCounter     atomic.Int64
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
		sweepInterval = 10 * time.Second
		if clientIdleTTL < sweepInterval {
			sweepInterval = clientIdleTTL
		}
		if sweepInterval < 10*time.Millisecond {
			sweepInterval = 10 * time.Millisecond
		}
	}

	coord := &Coordinator{
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
		pkBypassCache:      make(map[uint64]*PKLookupPlan, 256),
		startTime:          time.Now(),
		stopCh:             make(chan struct{}),
	}
	coord.clientSessionIdleTTL.Store(int64(clientIdleTTL))

	coord.bgWg.Add(1)
	go coord.idleSessionReapLoop(sweepInterval)

	coord.autoAnalyzer = NewAutoAnalyzer(coord, DefaultAutoAnalyzeConfig())
	executor.SetIdentityProvider(coord)
	executor.SetQueryExecutor(func(ctx context.Context, sql string, stx storage.Transaction, vars map[string]storage.Value) ([]map[string]any, []string, error) {
		cs := &ClientSession{
			ID:    "internal-vm-subquery",
			User:  "sa",
			TxMgr: tx.NewManager("internal-vm-subquery", nil, coord.engine, nil),
			Vars:  make(map[string]any, len(vars)*2),
		}
		if stx != nil {
			cs.TxMgr.SetActiveStorageTx(stx)
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
	executor.SetProcedureGetter(func(name string) (*proc.Program, error) {
		procMeta, err := coord.catalog.GetProcedure(name)
		if err != nil {
			return nil, err
		}
		prog, err := executor.Cache().GetOrCompile(procMeta.Body)
		if err != nil {
			return nil, err
		}
		return prog, nil
	})
	type walHookAdder interface {
		AddWALHook(fn func(rec memengine.WALRecord))
	}
	if wha, ok := eng.(walHookAdder); ok {
		wha.AddWALHook(coord.autoAnalyzer.OnWALRecord)
	}

	return coord
}

// TableIdentity manages the lock-free atomic sequence for a table's IDENTITY / AUTO_INCREMENT.
type TableIdentity struct {
	seq         atomic.Int64
	initialized atomic.Bool
	initMu      sync.Mutex
}

func (c *Coordinator) getTableIdentity(tableName string) *TableIdentity {
	lower := strings.ToLower(tableName)
	if val, ok := c.identSlots.Load(lower); ok {
		return val.(*TableIdentity)
	}
	slot := &TableIdentity{}
	actual, _ := c.identSlots.LoadOrStore(lower, slot)
	return actual.(*TableIdentity)
}

// AllocTableIdentity atomically allocates a contiguous range of count IDs for tableName.
// If the table sequence has not been initialized yet, initFn is invoked to fetch the initial max ID.
func (c *Coordinator) AllocTableIdentity(tableName string, count int64, initFn func() int64) int64 {
	if count <= 0 {
		count = 1
	}
	slot := c.getTableIdentity(tableName)
	if !slot.initialized.Load() {
		slot.initMu.Lock()
		if !slot.initialized.Load() {
			initVal := int64(0)
			if initFn != nil {
				initVal = initFn()
			}
			slot.seq.Store(initVal)
			slot.initialized.Store(true)
		}
		slot.initMu.Unlock()
	}
	end := slot.seq.Add(count)
	return end - count + 1
}

// SetTableLastIdentity records or ratchets the identity value generated for a table.
func (c *Coordinator) SetTableLastIdentity(tableName string, id int64) {
	slot := c.getTableIdentity(tableName)
	if id == 0 {
		slot.seq.Store(0)
		slot.initialized.Store(true)
		return
	}
	for {
		cur := slot.seq.Load()
		if id <= cur {
			break
		}
		if slot.seq.CompareAndSwap(cur, id) {
			break
		}
	}
	slot.initialized.Store(true)
}

// GetTableLastIdentity retrieves the most recent identity value generated for a table.
func (c *Coordinator) GetTableLastIdentity(tableName string) (int64, bool) {
	slot := c.getTableIdentity(tableName)
	if !slot.initialized.Load() {
		return 0, false
	}
	return slot.seq.Load(), true
}

// DropTableIdentity cleans up cached identity sequence slot on DROP/TRUNCATE.
func (c *Coordinator) DropTableIdentity(tableName string) {
	c.identSlots.Delete(strings.ToLower(tableName))
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
		VarLengths:      make(map[string]int),
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
	cs.TxMgr = tx.NewManager(id, sess, c.engine, nil)
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
				drainTx, drainSess, drainFn := cs.checkAndDrainClosedLocked()
				cs.Mu.Unlock()
				if drainTx != nil {
					_ = drainTx.Rollback()
				}
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

// ClientSessionIdleTTL returns the configured idle timeout for client sessions and uncommitted transactions.
func (c *Coordinator) ClientSessionIdleTTL() time.Duration {
	return time.Duration(c.clientSessionIdleTTL.Load())
}

// SetClientSessionIdleTTL updates the idle timeout for client sessions and uncommitted transactions.
func (c *Coordinator) SetClientSessionIdleTTL(d time.Duration) {
	c.clientSessionIdleTTL.Store(int64(d))
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
			c.ReapIdleClientSessions(c.ClientSessionIdleTTL())
		}
	}
}

// ReapIdleClientSessions detaches and returns pinned storage sessions from idle ClientSessions
// whose last activity exceeds idleTTL and which have no in-flight queries (!cs.sessInUse && cs.activeQueries == 0).
// If a session has an abandoned uncommitted transaction (cs.Tx != nil), it is forcibly rolled back before
// detachment to release storage locks and prevent memory leaks.
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
		idleDur := now.Sub(cs.lastUsedAt)
		if !cs.closed && !cs.sessInUse && cs.activeQueries == 0 && cs.Session != nil && idleDur >= idleTTL {
			if cs.Tx() != nil {
				if cs.TxMgr != nil {
					_ = cs.TxMgr.Rollback(context.Background())
				} else {
					_ = cs.Tx().Rollback()
				}
				log.Printf("[TX-IDLE-TIMEOUT] session=%s forcibly rolled back uncommitted transaction after %v idle (threshold=%v)", cs.ID, idleDur.Round(time.Millisecond), idleTTL)
			}
			detached = append(detached, cs.Session)
			cs.Session = nil
			if cs.TxMgr != nil {
				cs.TxMgr.SetSession(nil)
			}
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
		// 1. Immediately cancel in-flight queries.
		cs.CancelQuery()

		cs.Mu.Lock()
		inFlight := cs.activeQueries > 0 || cs.sessInUse
		var immediateTx storage.Transaction
		var immediateTxMgr *tx.Manager
		if !inFlight {
			immediateTx = cs.Tx()
			immediateTxMgr = cs.TxMgr
		}

		// 2. Snapshot table variable & temp table names to drop outside lock ([RULE:LockHierarchy])
		toDropLocal := cs.drainStorageTablesToDropLocked()

		cs.closed = true
		storageSess := cs.Session

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

		// Rollback immediate transaction outside lock ([RULE:LockHierarchy])
		if immediateTxMgr != nil {
			_ = immediateTxMgr.Rollback(context.Background())
		} else if immediateTx != nil {
			_ = immediateTx.Rollback()
		}

		// 3. Drop tables outside cs.Mu lock to prevent lock hierarchy deadlocks
		c.dropSessionStorageTables(toDropLocal)

		if storageSess != nil && !isDefault {
			if c.pool != nil {
				c.pool.ReleaseSession(storageSess)
			} else {
				_ = storageSess.Close()
			}
		}
	}
}

// ResetSession restores a persistent client session to a clean, isolated initial state for connection pooling reuse.
// If resetTx is true, any active uncommitted transaction is rolled back.
// All temporary tables (#temp), table variables, session cursors, and custom session variables are reset.
func (c *Coordinator) ResetSession(id string, resetTx bool) error {
	if id == "" {
		return nil
	}

	cs := c.GetSession(id)
	if cs == nil {
		return nil
	}

	cs.Mu.Lock()
	cs.lastUsedAt = time.Now()

	var (
		txToRollback    storage.Transaction
		txMgrToRollback *tx.Manager
	)

	if resetTx && cs.Tx() != nil {
		txToRollback = cs.Tx()
		txMgrToRollback = cs.TxMgr
	}

	// Snapshot local temp tables and table variables to drop outside lock ([RULE:LockHierarchy])
	toDropLocal := cs.drainStorageTablesToDropLocked()

	// Reset user variables and restore default system variables
	cs.Vars = make(map[string]any)
	cs.Vars["transaction_isolation"] = "read committed"
	cs.Vars["search_path"] = `"$user", public`
	cs.Vars["timezone"] = "UTC"
	cs.VarLengths = nil

	// Revert user impersonation (SETUSER / EXECUTE AS)
	if cs.OriginalUser != "" {
		cs.User = cs.OriginalUser
		cs.OriginalUser = ""
	}
	cs.Database = "master"

	cs.Mu.Unlock()

	// 1. Rollback transaction outside lock ([RULE:LockHierarchy])
	if txMgrToRollback != nil {
		_ = txMgrToRollback.Rollback(context.Background())
	} else if txToRollback != nil {
		_ = txToRollback.Rollback()
	}

	// 2. Drop physical temp tables outside lock
	c.dropSessionStorageTables(toDropLocal)

	return nil
}

// dropSessionStorageTables drops the given list of physical temporary/table-variable storage tables.
func (c *Coordinator) dropSessionStorageTables(tables []string) {
	if c.engine == nil {
		return
	}
	for _, tbl := range tables {
		_ = c.engine.DropTable(tbl)
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
	return cs.TxStatus()
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
		if defCs.Tx() != nil {
			if defCs.TxMgr != nil {
				_ = defCs.TxMgr.Rollback(context.Background())
			} else {
				_ = defCs.Tx().Rollback()
			}
		}
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

// getTable retrieves a table schema, prioritizing session-local ephemeral tables and transaction ShadowCatalog before falling back to the engine catalog.
func (c *Coordinator) getTable(cs *ClientSession, name string) (*storage.TableSchema, error) {
	if cs != nil {
		if eph := cs.GetEphemeralTable(name); eph != nil {
			return eph.Schema, nil
		}
		if shadow := cs.GetShadowCatalog(); shadow != nil {
			if shadow.IsTableDropped(name) {
				return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, name)
			}
			if sch, ok := shadow.GetTable(name); ok {
				return sch, nil
			}
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


