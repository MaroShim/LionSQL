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
	"lionsql/pkg/sqlutil"
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
	MaxConnections       int           // Maximum concurrent client connections limit (default 200)
	DialectPreference    string        // Server SQL dialect preference: "sqlserver" (default) or "postgres"
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
		MaxConnections:       200,
		DialectPreference:    "sqlserver",
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
	identSlots              sync.Map // lowerTableName (string) -> *TableIdentity
	lastGlobalIdentity      atomic.Int64
	lastGlobalScopeIdentity atomic.Int64
	schemaVersion           atomic.Uint64
	autoAnalyzer         *AutoAnalyzer
	startTime            time.Time
	tolerantDDL          atomic.Bool
	clientSessionIdleTTL atomic.Int64
	maxConnections       atomic.Int64
	dialectPreference    string
	dbMailInitOnce       sync.Once
	dbMailInitErr        error
	dbMailItemCounter    atomic.Int64
	dbMailLogCounter     atomic.Int64
	agentInitOnce        sync.Once
	agentInitErr         error
	agentHistoryCounter  atomic.Int64
	agentScheduleCounter atomic.Int64
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
	maxConns := cfg.MaxConnections
	if maxConns <= 0 {
		maxConns = 200
	}
	coord.maxConnections.Store(int64(maxConns))
	dialect := strings.ToLower(strings.TrimSpace(cfg.DialectPreference))
	if dialect == "" {
		dialect = "sqlserver"
	}
	coord.dialectPreference = dialect

	coord.bgWg.Add(1)
	go coord.idleSessionReapLoop(sweepInterval)

	coord.autoAnalyzer = NewAutoAnalyzer(coord, DefaultAutoAnalyzeConfig())
	executor.SetIdentityProvider(coord)
	executor.SetViewRewriter(coord.rewriteViewsAsCTE)

	// [ENG-011] Register global sequence handler for NEXTVAL / SETVAL built-in calls
	proc.RegisterGlobalSequenceHandler(func(op string, seqName string, val int64) (int64, error) {
		if coord.catalog == nil {
			return 0, fmt.Errorf("catalog not initialized")
		}
		cleanName := strings.Trim(seqName, "'\"[]`")
		if dotIdx := strings.LastIndex(cleanName, "."); dotIdx != -1 {
			cleanName = cleanName[dotIdx+1:]
		}
		seq, err := coord.catalog.GetSequence(cleanName)
		if err != nil {
			return 0, err
		}
		if strings.EqualFold(op, "SETVAL") {
			if err := seq.Restart(val); err != nil {
				return 0, err
			}
			return val, nil
		}
		return seq.Next()
	})

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
		trimmed := strings.TrimSpace(sql)
		var res *QueryResult
		var err error
		if sqlutil.HasPrefixKeyword(trimmed, "MERGE") {
			res, err = coord.execMerge(trimmed, time.Now(), cs)
		} else if sqlutil.HasPrefixKeyword(trimmed, "ALTER") {
			res, err = coord.execAlterTable(trimmed, time.Now(), cs)
		} else {
			res, err = coord.execSelect(sql, time.Now(), cs)
		}
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
	return c.AllocTableIdentityWithStep(tableName, count, 1, 1, func() (int64, bool) {
		if initFn == nil {
			return 0, false
		}
		val := initFn()
		return val, val > 0
	})
}

// AllocTableIdentityWithStep atomically allocates a contiguous range of count IDs for tableName with custom seed and step.
func (c *Coordinator) AllocTableIdentityWithStep(tableName string, count int64, seed int64, step int64, initFn func() (int64, bool)) int64 {
	if count <= 0 {
		count = 1
	}
	if step == 0 {
		step = 1
	}
	slot := c.getTableIdentity(tableName)
	if !slot.initialized.Load() {
		slot.initMu.Lock()
		if !slot.initialized.Load() {
			initVal := seed - step
			if initFn != nil {
				if maxVal, hasRows := initFn(); hasRows {
					initVal = maxVal
				}
			}
			slot.seq.Store(initVal)
			slot.initialized.Store(true)
		}
		slot.initMu.Unlock()
	}
	end := slot.seq.Add(count * step)
	c.RecordLastIdentity(end, end)
	return end - (count * step) + step
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
	if c.lastGlobalIdentity.Load() == 0 {
		c.lastGlobalIdentity.Store(id)
	}
	if c.lastGlobalScopeIdentity.Load() == 0 {
		c.lastGlobalScopeIdentity.Store(id)
	}
}

// RecordLastIdentity updates the coordinator-wide last generated identity and scope identity values
// to support cross-connection CLI tools (e.g., sequential `psql -c` commands) while preserving
// per-session `cs.Vars` isolation when active within the same connection.
func (c *Coordinator) RecordLastIdentity(lastID, scopeID int64) {
	if lastID > 0 {
		c.lastGlobalIdentity.Store(lastID)
	}
	if scopeID > 0 {
		c.lastGlobalScopeIdentity.Store(scopeID)
	}
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
	dialect := DialectTSQL
	if strings.HasPrefix(id, "tds-") {
		defaultDB = "master"
	}
	if c.dialectPreference == "postgres" || strings.HasPrefix(id, "pg-") {
		defaultDB = "postgres"
		dialect = DialectPostgres
	}

	cs := &ClientSession{
		ID:              id,
		SPID:            c.spidCounter,
		Dialect:         dialect,
		Session:         sess,
		Database:        defaultDB,
		Vars:            make(map[string]any),
		VarTypes:        make(map[string]storage.DataType),
		VarLengths:      make(map[string]int),
		TableVars:       make(map[string]string),
		TempTableMap:    make(map[string]string),
		EphemeralTables: make(map[string]*EphemeralTable),
		PreparedStmts:   make(map[string]*PreparedStatement),
		Cursors:         make(map[string]*SessionCursor),
		User:            "dbo",
		OriginalUser:    "",
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

// ClientSessionIdleTTL returns the configured idle timeout for client sessions and uncommitted transactions.
func (c *Coordinator) ClientSessionIdleTTL() time.Duration {
	return time.Duration(c.clientSessionIdleTTL.Load())
}

// SetClientSessionIdleTTL updates the idle timeout for client sessions and uncommitted transactions.
func (c *Coordinator) SetClientSessionIdleTTL(d time.Duration) {
	c.clientSessionIdleTTL.Store(int64(d))
}

// MaxConnections returns the configured maximum concurrent client connections limit.
func (c *Coordinator) MaxConnections() int {
	mc := c.maxConnections.Load()
	if mc <= 0 {
		return 200
	}
	return int(mc)
}

// SetMaxConnections dynamically updates the maximum concurrent connections limit.
func (c *Coordinator) SetMaxConnections(n int) {
	if n <= 0 {
		n = 200
	}
	c.maxConnections.Store(int64(n))
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
		hasTx := cs.Tx() != nil
		threshold := idleTTL
		if hasTx && cs.idleInTxTimeout > 0 {
			threshold = cs.idleInTxTimeout
		}
		if !cs.closed && !cs.sessInUse && cs.activeQueries == 0 && cs.Session != nil && threshold > 0 && idleDur >= threshold {
			if hasTx {
				if cs.TxMgr != nil {
					_ = cs.TxMgr.Rollback(context.Background())
				} else {
					_ = cs.Tx().Rollback()
				}
				log.Printf("[TX-IDLE-TIMEOUT] session=%s forcibly rolled back uncommitted transaction after %v idle (threshold=%v)", cs.ID, idleDur.Round(time.Millisecond), threshold)
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
		var immediateTxMgr *tx.Manager
		if !inFlight {
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

		// Rollback immediate transaction outside lock strictly via TxMgr (SSOT)
		if immediateTxMgr != nil {
			_ = immediateTxMgr.Rollback(context.Background())
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
	opts := ResetOptionsAll()
	opts.ResetTx = resetTx
	return c.ResetSessionWithOptions(id, opts)
}

// ResetSessionWithOptions restores session state according to the given options.
// It executes the canonical 4-step tear-down sequence:
// 1. Session lock: snapshot resources and clear session metadata
// 2. Unlock session mutex ([RULE:LockHierarchy])
// 3. Rollback active transaction outside locks
// 4. Drop physical temporary/table-variable storage tables outside locks
func (c *Coordinator) ResetSessionWithOptions(id string, opts SessionResetOptions) error {
	if id == "" {
		return nil
	}

	cs := c.GetSession(id)
	if cs == nil {
		return nil
	}

	cs.Mu.Lock()
	cs.lastUsedAt = time.Now()

	// 1-1. Close and deallocate server-side cursors first (before transaction rollback)
	if opts.CloseCursors && cs.Cursors != nil {
		for _, cur := range cs.Cursors {
			cur.IsOpen = false
			cur.IsClosed = true
			cur.Rows = nil
			cur.Pos = 0
		}
		clear(cs.Cursors)
	}

	// 1-2. Snapshot transaction manager to rollback outside lock ([RULE:LockHierarchy])
	var txMgrToRollback *tx.Manager
	if opts.ResetTx && cs.Tx() != nil {
		txMgrToRollback = cs.TxMgr
		cs.ResetTranCount()
		cs.ClearTxFailed()
		delete(cs.Vars, "transaction_read_only")
	}

	// 1-3. Snapshot local temp tables and table variables to drop outside lock
	var toDropLocal []string
	if opts.DropTempTables {
		if opts.ClearPlans && opts.CloseCursors {
			toDropLocal = cs.drainStorageTablesToDropLocked()
		} else {
			toDropLocal = cs.drainTempTablesOnlyLocked()
		}
	}

	// 1-4. Clear prepared statements if requested
	if opts.ClearPlans {
		cs.PreparedStmts = nil
	}

	// 1-5. Reset user variables and restore default system variables
	if opts.ClearVars {
		cs.Vars = make(map[string]any)
		cs.LocalVars = nil
		cs.Vars["transaction_isolation"] = "read committed"
		cs.Vars["search_path"] = `"$user", public`
		cs.Vars["timezone"] = "UTC"
		cs.VarLengths = nil
		cs.VarTypes = nil
		cs.Database = "master"
	}

	// 1-6. Revert user impersonation (SETUSER / EXECUTE AS)
	if opts.ResetAuth && (cs.OriginalUser != "" || len(cs.ImpersonationStack) > 0) {
		if len(cs.ImpersonationStack) > 0 {
			cs.User = cs.ImpersonationStack[0]
		} else {
			cs.User = cs.OriginalUser
		}
		cs.OriginalUser = ""
		cs.ImpersonationStack = nil
	}

	cs.Mu.Unlock()

	// Step 3: Rollback transaction outside lock strictly via TxMgr (SSOT)
	if txMgrToRollback != nil {
		_ = txMgrToRollback.Rollback(context.Background())
	}

	// Step 4: Drop physical temp tables outside lock
	if len(toDropLocal) > 0 {
		c.dropSessionStorageTables(toDropLocal)
	}

	return nil
}

// DropSessionStorageTables drops the given list of physical temporary/table-variable storage tables.
func (c *Coordinator) dropSessionStorageTables(tables []string) {
	if c.engine == nil {
		return
	}
	for _, tbl := range tables {
		_ = c.engine.DropTable(tbl)
	}
}

// DiscardSessionTempTables drops all session-local temporary and ephemeral tables without altering other session state.
func (c *Coordinator) DiscardSessionTempTables(id string) error {
	return c.ResetSessionWithOptions(id, ResetOptionsTempOnly())
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
		cs.ImpersonationStack = nil
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

// DialectPreference returns the configured SQL dialect preference for the coordinator.
func (c *Coordinator) DialectPreference() string {
	if c == nil || c.dialectPreference == "" {
		return "sqlserver"
	}
	return c.dialectPreference
}

// SetDialectPreference sets the SQL dialect preference ("sqlserver" or "postgres").
func (c *Coordinator) SetDialectPreference(d string) {
	if c != nil {
		c.dialectPreference = strings.ToLower(strings.TrimSpace(d))
	}
}


