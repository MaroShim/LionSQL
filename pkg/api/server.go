package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/auth"
	"sqlion/pkg/backup"
	"sqlion/pkg/catalog"
	"sqlion/pkg/execution"
	"sqlion/pkg/proc"
	_ "sqlion/pkg/proc/parser/antlr"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/ui"
)

// ClientSession maintains interactive connection state: open transaction, session variables, and query cancellation context.
type ClientSession struct {
	ID              string
	SPID            int
	Session         storage.Session
	Tx              storage.Transaction
	Vars            map[string]any
	TableVars       map[string]string // @table_var -> physical table name
	TempTables      []string          // session-scoped #temp_tables
	QueryCtx        context.Context
	CancelFunc      context.CancelFunc
	RunningQuery    string
	QueryStartTime  time.Time
	CancelRequested bool
	mu              sync.Mutex
}

// Server provides the PocketBase-style HTTP API and Embedded Web Dashboard.
type Server struct {
	catalog        *catalog.Catalog
	engine         storage.Engine
	planCache      *proc.PlanCache
	executor       *runtime.Executor
	auth           *auth.AuthService
	enableAuth     bool
	broker         *RealtimeBroker
	webhooks       *WebhookDispatcher
	rls            *RLSManager
	crons          *CronScheduler
	mailer         *Mailer
	pool           *SessionPool
	mux            *http.ServeMux
	sessions           map[string]*ClientSession
	sessMu             sync.RWMutex
	globalTempCreators map[string]string // lowercase table name -> creatorSessionID
	globalTempMu       sync.RWMutex
	defaultWorkMem     int64
	spidCounter        int
	metrics            *MetricsCollector
	slowQueries        *SlowQueryLogger
	generalLog         *GeneralLogger
	startTime          time.Time
}

// NewServer initializes a new Server with default in-memory auth service.
func NewServer(cat *catalog.Catalog, eng storage.Engine) *Server {
	authSvc := auth.NewAuthService(auth.Config{})
	_, _ = authSvc.EnsureDefaultSuperuser("admin@sqlion.local", "1234567890")
	return NewServerWithAuth(cat, eng, authSvc, false)
}

// NewServerWithAuth initializes a Server with explicit auth service and enforcement option.
func NewServerWithAuth(cat *catalog.Catalog, eng storage.Engine, authSvc *auth.AuthService, enableAuth bool) *Server {
	if authSvc == nil {
		authSvc = auth.NewAuthService(auth.Config{})
		_, _ = authSvc.EnsureDefaultSuperuser("admin@sqlion.local", "1234567890")
	}
	cache := runtime.NewPlanCache()
	pool := NewSessionPool(eng, DefaultPoolConfig())
	broker := NewRealtimeBroker()
	webhooks := NewWebhookDispatcher()
	webhooks.StartBrokerListener(broker)
	mailer := NewMailer(SMTPConfig{})
	s := &Server{
		catalog:            cat,
		engine:             eng,
		planCache:          proc.NewPlanCache(),
		executor:           runtime.NewExecutor(cache),
		auth:               authSvc,
		enableAuth:         enableAuth,
		broker:             broker,
		webhooks:           webhooks,
		rls:                NewRLSManager(),
		mailer:             mailer,
		pool:               pool,
		mux:                http.NewServeMux(),
		sessions:           make(map[string]*ClientSession),
		globalTempCreators: make(map[string]string),
		defaultWorkMem:     execution.DefaultWorkMem,
		metrics:            NewMetricsCollector(),
		slowQueries:        NewSlowQueryLogger(DefaultSlowQueryThreshold),
		generalLog:         NewGeneralLogger(false),
		startTime:          time.Now(),
	}
	s.executor.SetTableGetter(eng.GetTable)

	// Wire internal CronScheduler with ambient SQL & Procedure execution
	s.crons = NewCronScheduler(func(ctx context.Context, cmdType, cmd string) error {
		query := cmd
		if cmdType == "procedure" {
			trimmed := strings.TrimSpace(cmd)
			if !strings.HasPrefix(strings.ToUpper(trimmed), "EXEC") {
				query = "EXEC " + trimmed
			}
		}
		_, err := s.ExecuteSessionSQLContext(ctx, "cron", query)
		return err
	})
	s.crons.Start(context.Background())

	s.routes()
	return s
}

// SetDefaultWorkMem updates the server-wide default work_mem limit.
func (s *Server) SetDefaultWorkMem(bytes int64) {
	if bytes > 0 {
		s.defaultWorkMem = bytes
	}
}

// DefaultWorkMem returns the server-wide default work_mem limit.
func (s *Server) DefaultWorkMem() int64 {
	if s.defaultWorkMem <= 0 {
		return execution.DefaultWorkMem
	}
	return s.defaultWorkMem
}

// Pool returns the underlying goroutine connection & session pool.
func (s *Server) Pool() *SessionPool {
	return s.pool
}

// SetPoolConfig updates the connection pool configuration.
func (s *Server) SetPoolConfig(cfg PoolConfig) {
	if s.pool != nil {
		_ = s.pool.Close()
	}
	s.pool = NewSessionPool(s.engine, cfg)
}

// Broker returns the active realtime pub/sub broker.
func (s *Server) Broker() *RealtimeBroker {
	return s.broker
}

// AuthService returns the underlying authentication service.
func (s *Server) AuthService() *auth.AuthService {
	return s.auth
}

// Executor returns the underlying runtime VM executor.
func (s *Server) Executor() *runtime.Executor {
	return s.executor
}

// BytecodePlanCache returns the executor's bytecode plan cache.
func (s *Server) BytecodePlanCache() *runtime.PlanCache {
	if s.executor != nil {
		return s.executor.Cache()
	}
	return nil
}

// Webhooks returns the active webhook dispatch engine.
func (s *Server) Webhooks() *WebhookDispatcher {
	return s.webhooks
}

// RLS returns the declarative Row-Level Security manager.
func (s *Server) RLS() *RLSManager {
	return s.rls
}

// Crons returns the embedded cron recurring scheduler.
func (s *Server) Crons() *CronScheduler {
	return s.crons
}

// Mailer returns the embedded SMTP mailer engine.
func (s *Server) Mailer() *Mailer {
	return s.mailer
}

// SetEnableAuth toggles authentication enforcement for protected API routes.
func (s *Server) SetEnableAuth(enable bool) {
	s.enableAuth = enable
}

// Metrics returns the Prometheus metrics collector instance.
func (s *Server) Metrics() *MetricsCollector {
	return s.metrics
}

// SlowQueries returns the slow query logger instance.
func (s *Server) SlowQueries() *SlowQueryLogger {
	return s.slowQueries
}

// SetSlowQueryThreshold sets the minimum duration for a query to be recorded as slow.
func (s *Server) SetSlowQueryThreshold(d time.Duration) {
	if s.slowQueries != nil {
		s.slowQueries.SetThreshold(d)
	}
}

// SetSlowQueryLogFile configures disk file output for the slow query log.
func (s *Server) SetSlowQueryLogFile(filePath string) error {
	if s.slowQueries != nil {
		return s.slowQueries.SetLogFile(filePath)
	}
	return nil
}

// GeneralLog returns the general query logger instance.
func (s *Server) GeneralLog() *GeneralLogger {
	return s.generalLog
}

// SetGeneralLogEnabled toggles general query logging.
func (s *Server) SetGeneralLogEnabled(enabled bool) {
	if s.generalLog != nil {
		s.generalLog.SetEnabled(enabled)
	}
}

// SetGeneralLogFile configures disk file output for the general query log.
func (s *Server) SetGeneralLogFile(filePath string) error {
	if s.generalLog != nil {
		return s.generalLog.SetLogFile(filePath)
	}
	return nil
}

// Close gracefully flushes and closes server log writers and session pools.
func (s *Server) Close() error {
	var errs []error
	if s.slowQueries != nil {
		if err := s.slowQueries.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.generalLog != nil {
		if err := s.generalLog.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.pool != nil {
		if err := s.pool.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing server: %v", errs)
	}
	return nil
}

// GetOrCreateSession returns or registers a persistent client session.
func (s *Server) GetOrCreateSession(id string) (*ClientSession, error) {
	if id == "" {
		id = "default"
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()

	if cs, exists := s.sessions[id]; exists {
		return cs, nil
	}

	var sess storage.Session
	var err error
	if s.pool != nil {
		sess, err = s.pool.AcquireSession()
	} else {
		sess, err = s.engine.NewSession()
	}
	if err != nil {
		return nil, err
	}

	if s.spidCounter < 50 {
		s.spidCounter = 50
	}
	s.spidCounter++

	cs := &ClientSession{
		ID:        id,
		SPID:      s.spidCounter,
		Session:   sess,
		Vars:      make(map[string]any),
		TableVars: make(map[string]string),
	}
	s.sessions[id] = cs
	return cs, nil
}

// CloseSession gracefully closes and unregisters a client session, returning the underlying session to the pool.
func (s *Server) CloseSession(id string) {
	if id == "" || id == "default" {
		return
	}
	s.sessMu.Lock()
	cs, exists := s.sessions[id]
	if exists {
		delete(s.sessions, id)
	}
	s.sessMu.Unlock()

	// Clean up any global temporary tables created by this session
	s.globalTempMu.Lock()
	var toDrop []string
	for tbl, creatorID := range s.globalTempCreators {
		if creatorID == id {
			toDrop = append(toDrop, tbl)
			delete(s.globalTempCreators, tbl)
		}
	}
	s.globalTempMu.Unlock()

	for _, tbl := range toDrop {
		_ = s.engine.DropTable(tbl)
	}

	if exists && cs != nil {
		cs.mu.Lock()
		for _, physTable := range cs.TableVars {
			_ = s.engine.DropTable(physTable)
		}
		cs.TableVars = nil

		for _, tempTbl := range cs.TempTables {
			_ = s.engine.DropTable(tempTbl)
		}
		cs.TempTables = nil

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
		cs.mu.Unlock()

		if storageSess != nil {
			if s.pool != nil {
				s.pool.ReleaseSession(storageSess)
			} else {
				_ = storageSess.Close()
			}
		}
	}
}

// ActiveQueryInfo represents snapshot metadata of an in-flight query.
type ActiveQueryInfo struct {
	SessionID  string    `json:"session_id"`
	SPID       int       `json:"spid"`
	Query      string    `json:"query"`
	StartTime  time.Time `json:"start_time"`
	DurationUS int64     `json:"duration_us"`
}

// CancelSessionQuery triggers cooperative cancellation for the currently active statement on sessionID.
// Returns true if an active query was found and cancellation was signaled.
func (s *Server) CancelSessionQuery(sessionID string) bool {
	s.sessMu.RLock()
	cs, exists := s.sessions[sessionID]
	s.sessMu.RUnlock()

	if !exists || cs == nil {
		return false
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.CancelFunc != nil {
		cs.CancelRequested = true
		cs.CancelFunc()
		return true
	}
	return false
}

// CancelQueryBySPID cancels the active query executing on the connection identified by spid.
// Returns true if an active query was found and canceled.
func (s *Server) CancelQueryBySPID(spid int) bool {
	s.sessMu.RLock()
	var targetCS *ClientSession
	for _, cs := range s.sessions {
		if cs.SPID == spid {
			targetCS = cs
			break
		}
	}
	s.sessMu.RUnlock()

	if targetCS == nil {
		return false
	}

	targetCS.mu.Lock()
	defer targetCS.mu.Unlock()

	if targetCS.CancelFunc != nil {
		targetCS.CancelRequested = true
		targetCS.CancelFunc()
		return true
	}
	return false
}

// GetActiveQueries returns a snapshot list of all currently executing queries across all sessions.
func (s *Server) GetActiveQueries() []ActiveQueryInfo {
	s.sessMu.RLock()
	defer s.sessMu.RUnlock()

	now := time.Now()
	var active []ActiveQueryInfo
	for _, cs := range s.sessions {
		cs.mu.Lock()
		if cs.RunningQuery != "" && cs.CancelFunc != nil {
			active = append(active, ActiveQueryInfo{
				SessionID:  cs.ID,
				SPID:       cs.SPID,
				Query:      cs.RunningQuery,
				StartTime:  cs.QueryStartTime,
				DurationUS: now.Sub(cs.QueryStartTime).Microseconds(),
			})
		}
		cs.mu.Unlock()
	}
	return active
}


// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ServeHTTP implements http.Handler for direct HTTP serving.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	// Dashboard & Root Redirect
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/_/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	s.mux.HandleFunc("GET /_/", func(w http.ResponseWriter, r *http.Request) {
		html, err := ui.IndexHTML()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(html)
	})

	// API Health
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		engineType := "In-Memory OCC"
		if s.engine != nil {
			engineType = s.engine.Type()
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": "0.7.0",
			"engine":  engineType,
		})
	})

	// Connection Pool & Worker Metrics (Built-in Connection Pooler)
	s.mux.HandleFunc("GET /api/pool/stats", func(w http.ResponseWriter, r *http.Request) {
		if s.pool != nil {
			writeJSON(w, http.StatusOK, s.pool.Stats())
		} else {
			writeJSON(w, http.StatusOK, map[string]string{"status": "pool_disabled"})
		}
	})

	// Realtime SSE Change Stream (PocketBase-style Live Event Stream)
	s.mux.HandleFunc("GET /api/realtime", s.handleRealtimeStream)

	// Auth Endpoints (PocketBase compatible)
	s.mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	s.mux.HandleFunc("GET /api/auth/me", s.handleAuthMe)
	s.mux.HandleFunc("POST /api/auth/refresh", s.handleAuthRefresh)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)

	// API Keys Management Endpoints (Superuser only)
	s.mux.HandleFunc("GET /api/keys", s.wrapSuperuser(s.handleListAPIKeys))
	s.mux.HandleFunc("POST /api/keys", s.wrapSuperuser(s.handleCreateAPIKey))
	s.mux.HandleFunc("DELETE /api/keys/{id}", s.wrapSuperuser(s.handleRevokeAPIKey))

	// Seed Sample Data
	s.mux.HandleFunc("POST /api/seed", s.wrapAuth(s.handleSeed, false, ""))

	// SQL Query Execution API
	s.mux.HandleFunc("POST /api/sql", s.wrapAuth(s.handleExecuteSQL, false, ""))
	s.mux.HandleFunc("POST /api/query/cancel", s.handleQueryCancel)
	s.mux.HandleFunc("GET /api/queries/active", s.handleListActiveQueries)

	// Prometheus Metrics & Observability API
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Slow Queries Management API
	s.mux.HandleFunc("GET /api/slow-queries", s.handleGetSlowQueries)
	s.mux.HandleFunc("POST /api/slow-queries/threshold", s.handleSetSlowQueryThreshold)
	s.mux.HandleFunc("POST /api/slow-queries/logfile", s.handleSetSlowQueryLogFile)
	s.mux.HandleFunc("DELETE /api/slow-queries", s.handleClearSlowQueries)

	// General Queries Log API
	s.mux.HandleFunc("GET /api/general-queries", s.handleGetGeneralQueries)
	s.mux.HandleFunc("POST /api/general-queries/toggle", s.handleToggleGeneralLog)
	s.mux.HandleFunc("POST /api/general-queries/logfile", s.handleSetGeneralLogFile)
	s.mux.HandleFunc("DELETE /api/general-queries", s.handleClearGeneralQueries)

	// Tables API (PocketBase-style Collections & Records CRUD)
	s.mux.HandleFunc("GET /api/tables", s.wrapAuth(s.handleListTables, false, ""))
	s.mux.HandleFunc("POST /api/tables", s.wrapSuperuser(s.handleCreateTable))
	s.mux.HandleFunc("DELETE /api/tables/{name}", s.wrapSuperuser(s.handleDropTable))
	s.mux.HandleFunc("GET /api/tables/{name}/schema", s.wrapTableAuth(s.handleGetTableSchema, false))
	s.mux.HandleFunc("GET /api/tables/{name}/records", s.wrapTableAuth(s.handleGetRecords, false))
	s.mux.HandleFunc("GET /api/tables/{name}/records/{pk}", s.wrapTableAuth(s.handleGetSingleRecord, false))
	s.mux.HandleFunc("POST /api/tables/{name}/records", s.wrapTableAuth(s.handleCreateRecord, true))
	s.mux.HandleFunc("PATCH /api/tables/{name}/records/{pk}", s.wrapTableAuth(s.handleUpdateRecord, true))
	s.mux.HandleFunc("PUT /api/tables/{name}/records/{pk}", s.wrapTableAuth(s.handleUpdateRecord, true))
	s.mux.HandleFunc("DELETE /api/tables/{name}/records/{pk}", s.wrapTableAuth(s.handleDeleteRecord, true))
	s.mux.HandleFunc("GET /api/tables/{name}/export", s.wrapTableAuth(s.handleExportTable, false))
	s.mux.HandleFunc("POST /api/tables/{name}/import", s.wrapTableAuth(s.handleImportTable, true))

	// Auto-Generated RESTful Records CRUD API (Direct Frontend & SDK Integration)
	s.mux.HandleFunc("GET /api/records/{table}", s.wrapTableAuth(s.handleGetRecords, false))
	s.mux.HandleFunc("GET /api/records/{table}/{id}", s.wrapTableAuth(s.handleGetSingleRecord, false))
	s.mux.HandleFunc("POST /api/records/{table}", s.wrapTableAuth(s.handleCreateRecord, true))
	s.mux.HandleFunc("PATCH /api/records/{table}/{id}", s.wrapTableAuth(s.handleUpdateRecord, true))
	s.mux.HandleFunc("PUT /api/records/{table}/{id}", s.wrapTableAuth(s.handleUpdateRecord, true))
	s.mux.HandleFunc("DELETE /api/records/{table}/{id}", s.wrapTableAuth(s.handleDeleteRecord, true))

	// Stored Procedures API
	s.mux.HandleFunc("GET /api/procs", s.wrapAuth(s.handleListProcs, false, ""))
	s.mux.HandleFunc("POST /api/procs", s.wrapAuth(s.handleRegisterProc, true, ""))
	s.mux.HandleFunc("POST /api/procs/{name}/exec", s.wrapAuth(s.handleExecProc, false, ""))

	// Online Snapshot Hot Backup & Restore API
	s.mux.HandleFunc("POST /api/backup", s.wrapAuth(s.handleBackup, true, ""))
	s.mux.HandleFunc("GET /api/backup", s.wrapAuth(s.handleBackup, false, ""))
	s.mux.HandleFunc("POST /api/restore", s.wrapSuperuser(s.handleRestore))

	// Webhooks Management & Notification Dispatch API
	s.mux.HandleFunc("GET /api/webhooks", s.wrapSuperuser(s.handleListWebhooks))
	s.mux.HandleFunc("POST /api/webhooks", s.wrapSuperuser(s.handleCreateWebhook))
	s.mux.HandleFunc("GET /api/webhooks/deliveries", s.wrapSuperuser(s.handleListWebhookDeliveries))
	s.mux.HandleFunc("DELETE /api/webhooks/deliveries", s.wrapSuperuser(s.handleClearWebhookDeliveries))
	s.mux.HandleFunc("GET /api/webhooks/{id}", s.wrapSuperuser(s.handleGetWebhook))
	s.mux.HandleFunc("PATCH /api/webhooks/{id}", s.wrapSuperuser(s.handleUpdateWebhook))
	s.mux.HandleFunc("PUT /api/webhooks/{id}", s.wrapSuperuser(s.handleUpdateWebhook))
	s.mux.HandleFunc("DELETE /api/webhooks/{id}", s.wrapSuperuser(s.handleDeleteWebhook))
	s.mux.HandleFunc("POST /api/webhooks/{id}/test", s.wrapSuperuser(s.handleTestWebhook))

	// Row-Level Security (RLS) Rules Management API
	s.mux.HandleFunc("GET /api/tables/{name}/rules", s.wrapAuth(s.handleGetTableRules, false, ""))
	s.mux.HandleFunc("PUT /api/tables/{name}/rules", s.wrapSuperuser(s.handleSetTableRules))
	s.mux.HandleFunc("DELETE /api/tables/{name}/rules", s.wrapSuperuser(s.handleDeleteTableRules))

	// Cron Jobs Recurring Scheduler API
	s.mux.HandleFunc("GET /api/crons", s.wrapSuperuser(s.handleListCrons))
	s.mux.HandleFunc("POST /api/crons", s.wrapSuperuser(s.handleCreateCron))
	s.mux.HandleFunc("PUT /api/crons/{id}", s.wrapSuperuser(s.handleUpdateCron))
	s.mux.HandleFunc("DELETE /api/crons/{id}", s.wrapSuperuser(s.handleDeleteCron))
	s.mux.HandleFunc("POST /api/crons/{id}/run", s.wrapSuperuser(s.handleRunCronNow))

	// Mailer & SMTP Settings API
	s.mux.HandleFunc("GET /api/settings/mail", s.wrapSuperuser(s.handleGetMailSettings))
	s.mux.HandleFunc("PUT /api/settings/mail", s.wrapSuperuser(s.handleUpdateMailSettings))
	s.mux.HandleFunc("POST /api/settings/mail/test", s.wrapSuperuser(s.handleTestMail))
	s.mux.HandleFunc("GET /api/mail/logs", s.wrapSuperuser(s.handleGetMailLogs))
	s.mux.HandleFunc("DELETE /api/mail/logs", s.wrapSuperuser(s.handleDeleteMailLogs))
	s.mux.HandleFunc("POST /api/auth/request-password-reset", s.handleRequestPasswordReset)

	// Official Client SDK Static Distribution
	s.mux.HandleFunc("GET /sdk/sqlion.js", s.handleServeSDKJS)
	s.mux.HandleFunc("GET /sdk/sqlion.d.ts", s.handleServeSDKDTS)
}

func (s *Server) wrapSuperuser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enableAuth {
			next(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("superuser authorization required"))
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		_, err := s.auth.ValidateToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized: %w", err))
			return
		}
		next(w, r)
	}
}

func (s *Server) wrapTableAuth(next http.HandlerFunc, isWrite bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tableName := getTableName(r)
		s.wrapAuth(next, isWrite, tableName)(w, r)
	}
}

func (s *Server) wrapAuth(next http.HandlerFunc, isWrite bool, tableName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enableAuth {
			next(w, r)
			return
		}

		// 1. Check API Key header: X-API-Key or Authorization: ApiKey <key>
		apiKey := r.Header.Get("X-API-Key")
		authHeader := r.Header.Get("Authorization")
		if apiKey == "" && strings.HasPrefix(authHeader, "ApiKey ") {
			apiKey = strings.TrimPrefix(authHeader, "ApiKey ")
		}

		if apiKey != "" {
			keyObj, err := s.auth.ValidateAPIKey(apiKey)
			if err != nil {
				writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid api key: %w", err))
				return
			}
			if tableName != "" {
				if err := keyObj.CheckTableAccess(tableName, isWrite); err != nil {
					writeError(w, http.StatusForbidden, err)
					return
				}
			} else if isWrite && keyObj.Role == auth.RoleReadOnly {
				writeError(w, http.StatusForbidden, fmt.Errorf("read-only API key cannot perform write operations"))
				return
			}
			next(w, r)
			return
		}

		// 2. Check Bearer JWT token (Superuser)
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid authorization header (expected Bearer <token> or X-API-Key)"))
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		_, err := s.auth.ValidateToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized: %w", err))
			return
		}
		next(w, r)
	}
}

type createAPIKeyReq struct {
	Name          string          `json:"name"`
	Role          auth.APIKeyRole `json:"role"`
	AllowedTables []string        `json:"allowed_tables"`
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys := s.auth.ListAPIKeys()
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api key name is required"))
		return
	}
	keyObj, err := s.auth.CreateAPIKey(req.Name, req.Role, req.AllowedTables)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, keyObj)
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.auth.RevokeAPIKey(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "api key revoked"})
}

type authLoginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req authLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	u, token, err := s.auth.Authenticate(req.Email, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"record": map[string]any{
			"id":         u.ID,
			"email":      u.Email,
			"created_at": u.CreatedAt,
		},
	})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid authorization header"))
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	u, err := s.auth.ValidateToken(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         u.ID,
		"email":      u.Email,
		"created_at": u.CreatedAt,
	})
}

func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid authorization header"))
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	u, freshToken, err := s.auth.RefreshToken(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token": freshToken,
		"record": map[string]any{
			"id":         u.ID,
			"email":      u.Email,
			"created_at": u.CreatedAt,
		},
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid authorization header"))
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	u, err := s.auth.ValidateToken(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	if err := s.auth.RevokeAllTokens(u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "all sessions revoked successfully",
	})
}

type execSQLReq struct {
	Query string `json:"query"`
	SQL   string `json:"sql"`
}

func (s *Server) handleExecuteSQL(w http.ResponseWriter, r *http.Request) {
	var req execSQLReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	sqlStr := req.Query
	if strings.TrimSpace(sqlStr) == "" {
		sqlStr = req.SQL
	}

	result, err := s.ExecuteSQL(sqlStr)
	if err != nil {
		if result != nil {
			writeJSON(w, http.StatusOK, result)
			return
		}
		writeJSON(w, http.StatusOK, &QueryResult{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleQueryCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		SPID      int    `json:"spid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	canceled := false
	if req.SessionID != "" {
		canceled = s.CancelSessionQuery(req.SessionID)
	} else if req.SPID > 0 {
		canceled = s.CancelQueryBySPID(req.SPID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"canceled": canceled,
	})
}

func (s *Server) handleListActiveQueries(w http.ResponseWriter, r *http.Request) {
	active := s.GetActiveQueries()
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"queries": active,
		"total":   len(active),
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		http.Error(w, "metrics collector not initialized", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.metrics.FormatPrometheus(s)))
}

func (s *Server) handleGetSlowQueries(w http.ResponseWriter, r *http.Request) {
	if s.slowQueries == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"threshold_ms": 0.0,
			"count":        0,
			"entries":      []SlowQueryEntry{},
		})
		return
	}
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	entries := s.slowQueries.GetEntries(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"threshold_ms": float64(s.slowQueries.Threshold().Microseconds()) / 1000.0,
		"count":        len(entries),
		"log_file":     s.slowQueries.LogFilePath(),
		"entries":      entries,
	})
}

func (s *Server) handleSetSlowQueryThreshold(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ThresholdMS float64 `json:"threshold_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.ThresholdMS <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("threshold_ms must be greater than 0"))
		return
	}
	d := time.Duration(req.ThresholdMS * float64(time.Millisecond))
	s.SetSlowQueryThreshold(d)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"threshold_ms": req.ThresholdMS,
	})
}

func (s *Server) handleSetSlowQueryLogFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.SetSlowQueryLogFile(req.FilePath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"file_path": s.slowQueries.LogFilePath(),
	})
}

func (s *Server) handleClearSlowQueries(w http.ResponseWriter, r *http.Request) {
	if s.slowQueries != nil {
		s.slowQueries.Clear()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "slow queries cleared",
	})
}

func (s *Server) handleGetGeneralQueries(w http.ResponseWriter, r *http.Request) {
	if s.generalLog == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":  false,
			"count":    0,
			"log_file": "",
			"entries":  []GeneralLogEntry{},
		})
		return
	}
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	entries := s.generalLog.GetEntries(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  s.generalLog.IsEnabled(),
		"count":    len(entries),
		"log_file": s.generalLog.LogFilePath(),
		"entries":  entries,
	})
}

func (s *Server) handleToggleGeneralLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.SetGeneralLogEnabled(req.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"enabled": req.Enabled,
	})
}

func (s *Server) handleSetGeneralLogFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.SetGeneralLogFile(req.FilePath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"file_path": s.generalLog.LogFilePath(),
	})
}

func (s *Server) handleClearGeneralQueries(w http.ResponseWriter, r *http.Request) {
	if s.generalLog != nil {
		s.generalLog.Clear()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "general query log cleared",
	})
}

type ColumnDTO struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	PrimaryKey bool   `json:"primary_key"`
	Nullable   bool   `json:"nullable"`
}

type TableSchemaDTO struct {
	Name    string      `json:"name"`
	Columns []ColumnDTO `json:"columns"`
	DDL     string      `json:"ddl"`
}

func toTableDTO(t *storage.TableSchema) TableSchemaDTO {
	if t == nil {
		return TableSchemaDTO{}
	}
	cols := make([]ColumnDTO, len(t.Columns))
	var colDefs []string
	for i, c := range t.Columns {
		typeName := c.Type.String()
		cols[i] = ColumnDTO{
			Name:       c.Name,
			Type:       typeName,
			PrimaryKey: c.PrimaryKey,
			Nullable:   c.Nullable,
		}
		def := fmt.Sprintf("    %s %s", c.Name, typeName)
		if c.PrimaryKey {
			def += " PRIMARY KEY"
		} else if !c.Nullable {
			def += " NOT NULL"
		}
		colDefs = append(colDefs, def)
	}
	ddl := fmt.Sprintf("CREATE TABLE %s (\n%s\n);", t.Name, strings.Join(colDefs, ",\n"))
	return TableSchemaDTO{
		Name:    t.Name,
		Columns: cols,
		DDL:     ddl,
	}
}

func getTableName(r *http.Request) string {
	if t := r.PathValue("table"); t != "" {
		return t
	}
	return r.PathValue("name")
}

func getRecordPK(r *http.Request) string {
	if id := r.PathValue("id"); id != "" {
		return id
	}
	return r.PathValue("pk")
}

func recordToJSONMap(rec storage.Record, schema *storage.TableSchema) map[string]any {
	row := make(map[string]any, len(rec))
	if schema != nil {
		for _, col := range schema.Columns {
			v, exists := rec[col.Name]
			if !exists || v.IsNull {
				row[col.Name] = nil
				continue
			}
			switch v.Type {
			case storage.TypeInt:
				row[col.Name] = v.IVal
			case storage.TypeFloat:
				row[col.Name] = v.FVal
			case storage.TypeBool:
				row[col.Name] = v.BVal
			case storage.TypeDecimal:
				f, err := strconv.ParseFloat(v.String(), 64)
				if err == nil {
					row[col.Name] = f
				} else {
					row[col.Name] = v.String()
				}
			default:
				row[col.Name] = v.SVal
			}
		}
		return row
	}
	for k, v := range rec {
		if v.IsNull {
			row[k] = nil
		} else {
			switch v.Type {
			case storage.TypeInt:
				row[k] = v.IVal
			case storage.TypeFloat:
				row[k] = v.FVal
			case storage.TypeBool:
				row[k] = v.BVal
			default:
				row[k] = v.String()
			}
		}
	}
	return row
}

func (s *Server) handleListTables(w http.ResponseWriter, r *http.Request) {
	tables, err := s.engine.ListTables()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	dtos := make([]TableSchemaDTO, len(tables))
	for i, t := range tables {
		dtos[i] = toTableDTO(t)
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetTableSchema(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, toTableDTO(schema))
}

func (s *Server) handleGetRecords(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	// Parse query parameters
	page := 1
	if pStr := r.URL.Query().Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p >= 1 {
			page = p
		}
	}

	perPage := 30
	if ppStr := r.URL.Query().Get("perPage"); ppStr != "" {
		if pp, err := strconv.Atoi(ppStr); err == nil && pp > 0 {
			perPage = pp
		}
	} else if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			perPage = l
		}
	}
	if perPage > 500 {
		perPage = 500
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	sortParam := strings.TrimSpace(r.URL.Query().Get("sort"))

	// Parse filter expression (both ?filter=... and individual column params)
	filterExpr, err := ParseQueryParamsFilter(r.URL.Query(), schema)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Enforce declarative Row-Level Security (ListRule)
	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		rawFilter := r.URL.Query().Get("filter")
		effectiveFilter, rlsErr := s.rls.ApplyListRule(tableName, rawFilter, authCtx)
		if rlsErr != nil {
			writeError(w, http.StatusForbidden, rlsErr)
			return
		}
		if effectiveFilter != rawFilter {
			rlsExpr, err := ParseFilter(effectiveFilter)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("rls filter parse error: %w", err))
				return
			}
			if filterExpr == nil {
				filterExpr = rlsExpr
			} else {
				filterExpr = &AndFilter{Exprs: []FilterExpr{rlsExpr, filterExpr}}
			}
		}
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	cursor, err := tx.Scan(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer cursor.Close()

	records := make([]map[string]any, 0)
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			continue
		}
		row := recordToJSONMap(rec, schema)

		// Search filter
		if search != "" {
			searchLower := strings.ToLower(search)
			matched := false
			for _, v := range row {
				if v == nil {
					continue
				}
				str := fmt.Sprint(v)
				if strings.Contains(strings.ToLower(str), searchLower) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Filter expression
		if filterExpr != nil {
			if !filterExpr.Matches(row, schema) {
				continue
			}
		}

		records = append(records, row)
	}

	// Sort if sort parameter is provided (supports multi-column sort like sort=-id,name or sort=-balance)
	if sortParam != "" {
		type sortItem struct {
			col    string
			isDesc bool
			cType  storage.DataType
		}
		var sortItems []sortItem
		for _, part := range strings.Split(sortParam, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			isDesc := false
			colName := part
			if strings.HasPrefix(colName, "-") {
				isDesc = true
				colName = strings.TrimPrefix(colName, "-")
			} else if strings.HasPrefix(colName, "+") {
				colName = strings.TrimPrefix(colName, "+")
			}
			colName = strings.TrimSpace(colName)

			var dt storage.DataType = storage.TypeVarChar
			if schema != nil {
				if col, _, found := schema.FindColumn(colName); found {
					colName = col.Name
					dt = col.Type
				}
			}
			sortItems = append(sortItems, sortItem{col: colName, isDesc: isDesc, cType: dt})
		}

		if len(sortItems) > 0 {
			sort.SliceStable(records, func(i, j int) bool {
				for _, item := range sortItems {
					vi, hasI := records[i][item.col]
					vj, hasJ := records[j][item.col]
					if !hasI || vi == nil {
						if !hasJ || vj == nil {
							continue
						}
						return !item.isDesc
					}
					if !hasJ || vj == nil {
						return item.isDesc
					}

					var less bool
					var equal bool
					switch item.cType {
					case storage.TypeInt:
						var iVal, jVal int64
						switch v := vi.(type) {
						case int64:
							iVal = v
						case int:
							iVal = int64(v)
						case float64:
							iVal = int64(v)
						}
						switch v := vj.(type) {
						case int64:
							jVal = v
						case int:
							jVal = int64(v)
						case float64:
							jVal = int64(v)
						}
						if iVal == jVal {
							equal = true
						} else {
							less = iVal < jVal
						}
					case storage.TypeFloat, storage.TypeDecimal:
						var iVal, jVal float64
						switch v := vi.(type) {
						case float64:
							iVal = v
						case int64:
							iVal = float64(v)
						case int:
							iVal = float64(v)
						}
						switch v := vj.(type) {
						case float64:
							jVal = v
						case int64:
							jVal = float64(v)
						case int:
							jVal = float64(v)
						}
						if iVal == jVal {
							equal = true
						} else {
							less = iVal < jVal
						}
					case storage.TypeBool:
						bi, _ := vi.(bool)
						bj, _ := vj.(bool)
						if bi == bj {
							equal = true
						} else {
							less = !bi && bj
						}
					default:
						si := strings.ToLower(fmt.Sprint(vi))
						sj := strings.ToLower(fmt.Sprint(vj))
						if si == sj {
							equal = true
						} else {
							less = si < sj
						}
					}

					if equal {
						continue
					}
					if item.isDesc {
						return !less
					}
					return less
				}
				return false
			})
		}
	}

	// Pagination
	totalItems := len(records)
	totalPages := 0
	if totalItems > 0 {
		totalPages = (totalItems + perPage - 1) / perPage
	}

	var pagedRecords []map[string]any
	start := (page - 1) * perPage
	if start < totalItems {
		end := start + perPage
		if end > totalItems {
			end = totalItems
		}
		pagedRecords = records[start:end]
	} else {
		pagedRecords = make([]map[string]any, 0)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"table":      tableName,
		"page":       page,
		"perPage":    perPage,
		"limit":      perPage,
		"totalItems": totalItems,
		"totalPages": totalPages,
		"records":    pagedRecords,
		"items":      pagedRecords,
	})
}

func (s *Server) handleGetSingleRecord(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	pkStr := getRecordPK(r)

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	pkVal := convertValue(pkStr, pkCol.Type)

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	rec, err := tx.Get(tableName, pkVal)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("record with id %q not found", pkStr))
		return
	}

	respRow := recordToJSONMap(rec, schema)
	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		if allowed, err := s.rls.CanView(tableName, respRow, authCtx); !allowed {
			writeError(w, http.StatusForbidden, fmt.Errorf("access denied by row-level security rule: %w", err))
			return
		}
	}

	writeJSON(w, http.StatusOK, respRow)
}

func (s *Server) handleExportTable(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "csv"
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	cursor, err := tx.Scan(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer cursor.Close()

	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", tableName+".json"))
		var rows []map[string]any
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				continue
			}
			row := make(map[string]any)
			for _, col := range schema.Columns {
				v, exists := rec[col.Name]
				if !exists || v.IsNull {
					row[col.Name] = nil
				} else {
					switch v.Type {
					case storage.TypeInt:
						row[col.Name] = v.IVal
					case storage.TypeVarChar:
						row[col.Name] = v.SVal
					case storage.TypeFloat:
						row[col.Name] = v.FVal
					case storage.TypeBool:
						row[col.Name] = v.BVal
					default:
						row[col.Name] = v.String()
					}
				}
			}
			rows = append(rows, row)
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(rows)
		return
	}

	// CSV Export
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", tableName+".csv"))
	csvWriter := csv.NewWriter(w)
	defer csvWriter.Flush()

	// Header row
	header := make([]string, len(schema.Columns))
	for i, col := range schema.Columns {
		header[i] = col.Name
	}
	_ = csvWriter.Write(header)

	// Data rows
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			continue
		}
		row := make([]string, len(schema.Columns))
		for i, col := range schema.Columns {
			v, exists := rec[col.Name]
			if !exists || v.IsNull {
				row[i] = ""
			} else {
				row[i] = v.String()
			}
		}
		_ = csvWriter.Write(row)
	}
}

func (s *Server) handleImportTable(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	var reader io.Reader
	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("failed to parse multipart form: %w", err))
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("file field is required in multipart upload: %w", err))
			return
		}
		defer file.Close()
		reader = file
	} else if strings.Contains(contentType, "application/json") {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}
		var recordList []map[string]any
		switch v := body.(type) {
		case []any:
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					recordList = append(recordList, m)
				}
			}
		case map[string]any:
			if recs, ok := v["records"].([]any); ok {
				for _, item := range recs {
					if m, ok := item.(map[string]any); ok {
						recordList = append(recordList, m)
					}
				}
			} else if csvStr, ok := v["csv"].(string); ok {
				reader = strings.NewReader(csvStr)
			}
		}

		if recordList != nil {
			session, err := s.engine.NewSession()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			defer session.Close()

			tx, err := session.BeginTx(storage.TxOptions{ReadOnly: false})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			defer tx.Rollback()

			imported := 0
			for idx, row := range recordList {
				rec := make(storage.Record)
				for _, col := range schema.Columns {
					val, exists := row[col.Name]
					if !exists || val == nil {
						if col.PrimaryKey && col.Type == storage.TypeInt {
							val = time.Now().UnixNano()/int64(time.Millisecond) + int64(idx)
						} else if !col.Nullable {
							writeError(w, http.StatusBadRequest, fmt.Errorf("row %d: field %q cannot be null", idx+1, col.Name))
							return
						}
					}
					rec[col.Name] = convertValue(val, col.Type)
				}
				if err := tx.Insert(tableName, rec); err != nil {
					writeError(w, http.StatusBadRequest, fmt.Errorf("row %d insert error: %w", idx+1, err))
					return
				}
				imported++
			}
			if err := tx.Commit(); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			if s.broker != nil {
				s.broker.Broadcast(RealtimeEvent{
					Action: ActionImport,
					Table:  tableName,
				})
			}
			writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "table": tableName})
			return
		}
	}

	if reader == nil {
		reader = r.Body
	}

	csvReader := csv.NewReader(reader)
	csvReader.TrimLeadingSpace = true

	headers, err := csvReader.Read()
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("failed to read CSV header: %w", err))
		return
	}

	type colMap struct {
		csvIdx int
		col    storage.Column
	}
	var mappings []colMap
	var pkCol *storage.Column
	for i := range schema.Columns {
		if schema.Columns[i].PrimaryKey {
			pkCol = &schema.Columns[i]
		}
	}

	for csvIdx, h := range headers {
		cleanH := strings.TrimSpace(h)
		for _, col := range schema.Columns {
			if strings.EqualFold(col.Name, cleanH) {
				mappings = append(mappings, colMap{csvIdx: csvIdx, col: col})
				break
			}
		}
	}

	if len(mappings) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("no matching columns found between CSV headers and table %q", tableName))
		return
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: false})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	imported := 0
	rowIdx := 0
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("CSV parse error at line %d: %w", rowIdx+2, err))
			return
		}
		rowIdx++

		rec := make(storage.Record)
		for _, m := range mappings {
			if m.csvIdx < len(record) {
				rawVal := strings.TrimSpace(record[m.csvIdx])
				if rawVal == "" && m.col.Nullable && !m.col.PrimaryKey {
					rec[m.col.Name] = storage.NullValue(m.col.Type)
				} else {
					rec[m.col.Name] = convertValue(rawVal, m.col.Type)
				}
			}
		}

		// If PK is missing or empty, auto-generate if INT
		if pkCol != nil {
			curPK, exists := rec[pkCol.Name]
			if !exists || curPK.IsNull || (pkCol.Type == storage.TypeInt && curPK.IVal == 0) {
				if pkCol.Type == storage.TypeInt {
					rec[pkCol.Name] = storage.IntValue(time.Now().UnixNano()/int64(time.Millisecond) + int64(rowIdx))
				}
			}
		}

		if err := tx.Insert(tableName, rec); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("line %d insert error: %w", rowIdx+1, err))
			return
		}
		imported++
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionImport,
			Table:  tableName,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"table":    tableName,
	})
}

type createTableReq struct {
	Name    string             `json:"name"`
	Columns []createColumnItem `json:"columns"`
}

type createColumnItem struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // INT, VARCHAR, FLOAT, BOOL, DATETIME
	PrimaryKey bool   `json:"primary_key"`
	Nullable   bool   `json:"nullable"`
}

func (s *Server) handleCreateTable(w http.ResponseWriter, r *http.Request) {
	var req createTableReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tableName := strings.TrimSpace(req.Name)
	if tableName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("table name is required"))
		return
	}
	if len(req.Columns) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("at least one column is required"))
		return
	}

	cols := make([]storage.Column, len(req.Columns))
	hasPK := false
	for i, c := range req.Columns {
		colName := strings.TrimSpace(c.Name)
		if colName == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("column name cannot be empty"))
			return
		}
		var dt storage.DataType
		switch strings.ToUpper(strings.TrimSpace(c.Type)) {
		case "INT", "INTEGER", "BIGINT":
			dt = storage.TypeInt
		case "VARCHAR", "TEXT", "STRING":
			dt = storage.TypeVarChar
		case "FLOAT", "DOUBLE", "REAL", "NUMERIC":
			dt = storage.TypeFloat
		case "BOOL", "BOOLEAN":
			dt = storage.TypeBool
		case "DATETIME", "TIMESTAMP":
			dt = storage.TypeDateTime
		default:
			dt = storage.TypeVarChar
		}
		if c.PrimaryKey {
			hasPK = true
		}
		cols[i] = storage.Column{
			Name:       colName,
			Type:       dt,
			PrimaryKey: c.PrimaryKey,
			Nullable:   c.Nullable,
		}
	}

	if !hasPK {
		// Default to first column as PK if none specified
		cols[0].PrimaryKey = true
	}

	schema := &storage.TableSchema{
		Name:    tableName,
		Columns: cols,
	}

	if err := s.engine.CreateTable(schema); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{Action: ActionCreateTable, Table: tableName})
	}

	writeJSON(w, http.StatusCreated, toTableDTO(schema))
}

func (s *Server) handleDropTable(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	if err := s.engine.DropTable(tableName); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{Action: ActionDropTable, Table: tableName})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "table dropped"})
}

func (s *Server) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: false})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	rec := make(storage.Record)
	for _, col := range schema.Columns {
		v, exists := raw[col.Name]
		if !exists {
			for k, rawVal := range raw {
				if strings.EqualFold(k, col.Name) {
					v = rawVal
					exists = true
					break
				}
			}
		}
		if !exists || v == nil {
			if col.PrimaryKey {
				// Auto-generate numeric or string ID if not provided
				if col.Type == storage.TypeInt {
					v = time.Now().UnixNano() / int64(time.Millisecond)
				} else if col.Type == storage.TypeVarChar {
					v = fmt.Sprintf("%d", time.Now().UnixNano())
				}
			} else if !col.Nullable {
				writeError(w, http.StatusBadRequest, fmt.Errorf("field %q cannot be null", col.Name))
				return
			}
		}
		rec[col.Name] = convertValue(v, col.Type)
	}

	candidateRow := recordToJSONMap(rec, schema)
	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		if allowed, err := s.rls.CanCreate(tableName, candidateRow, authCtx); !allowed {
			writeError(w, http.StatusForbidden, fmt.Errorf("create denied by row-level security rule: %w", err))
			return
		}
	}

	if err := tx.Insert(tableName, rec); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	respRow := recordToJSONMap(rec, schema)

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionCreate,
			Table:  tableName,
			Record: respRow,
		})
	}

	writeJSON(w, http.StatusCreated, respRow)
}

func (s *Server) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	pkStr := getRecordPK(r)

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	pkVal := convertValue(pkStr, pkCol.Type)

	var updates map[string]any
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: false})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	existing, err := tx.Get(tableName, pkVal)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("record with id %s not found: %w", pkStr, err))
		return
	}

	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		existingJSON := recordToJSONMap(existing, schema)
		if allowed, err := s.rls.CanUpdate(tableName, existingJSON, updates, authCtx); !allowed {
			writeError(w, http.StatusForbidden, fmt.Errorf("update denied by row-level security rule: %w", err))
			return
		}
	}

	for k, v := range updates {
		col, _, found := schema.FindColumn(k)
		if !found {
			continue
		}
		existing[col.Name] = convertValue(v, col.Type)
	}

	if err := tx.Update(tableName, pkVal, existing); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	respRow := recordToJSONMap(existing, schema)

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionUpdate,
			Table:  tableName,
			ID:     pkStr,
			Record: respRow,
		})
	}

	writeJSON(w, http.StatusOK, respRow)
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	pkStr := getRecordPK(r)

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	pkVal := convertValue(pkStr, pkCol.Type)

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: false})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	existing, err := tx.Get(tableName, pkVal)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("record with id %s not found: %w", pkStr, err))
		return
	}

	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		existingJSON := recordToJSONMap(existing, schema)
		if allowed, err := s.rls.CanDelete(tableName, existingJSON, authCtx); !allowed {
			writeError(w, http.StatusForbidden, fmt.Errorf("delete denied by row-level security rule: %w", err))
			return
		}
	}

	if err := tx.Delete(tableName, pkVal); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionDelete,
			Table:  tableName,
			ID:     pkStr,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "record deleted", "id": pkStr})
}

func (s *Server) handleSeed(w http.ResponseWriter, r *http.Request) {
	// 1. Create accounts table if not exists
	accountsSchema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = s.engine.CreateTable(accountsSchema)

	// 2. Create customers table if not exists
	customersSchema := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeFloat, Nullable: false},
			{Name: "status", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	_ = s.engine.CreateTable(customersSchema)

	// 3. Insert sample rows
	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(500),
	})

	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(1),
		"name":    storage.StringValue("Alice"),
		"balance": storage.FloatValue(1000.5),
		"status":  storage.StringValue("active"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(2),
		"name":    storage.StringValue("Bob"),
		"balance": storage.FloatValue(450.0),
		"status":  storage.StringValue("pending"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(3),
		"name":    storage.StringValue("Charlie"),
		"balance": storage.FloatValue(800.0),
		"status":  storage.StringValue("active"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(4),
		"name":    storage.StringValue("Diana"),
		"balance": storage.FloatValue(200.0),
		"status":  storage.StringValue("inactive"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(5),
		"name":    storage.StringValue("Evan"),
		"balance": storage.FloatValue(600.0),
		"status":  storage.StringValue("active"),
	})

	_ = tx.Commit()

	writeJSON(w, http.StatusOK, map[string]string{"message": "Sample accounts and customers tables seeded successfully!"})
}

func (s *Server) handleListProcs(w http.ResponseWriter, r *http.Request) {
	procs := s.catalog.ListProcedures()
	writeJSON(w, http.StatusOK, procs)
}

type registerProcReq struct {
	Script string `json:"script"`
}

func (s *Server) handleRegisterProc(w http.ResponseWriter, r *http.Request) {
	var req registerProcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	trimmed := strings.TrimSpace(req.Script)
	upper := strings.ToUpper(trimmed)
	var procName string
	var paramDefs []catalog.ParamDef
	var jsCode string
	var orAlter bool
	var isAlter bool

	if strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "ALTER ") {
		plan, err := s.planCache.GetOrCompile(req.Script)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		procName = plan.Name
		orAlter = plan.OrAlter
		isAlter = plan.IsAlter
		paramDefs = make([]catalog.ParamDef, len(plan.Params))
		for i, p := range plan.Params {
			paramDefs[i] = catalog.ParamDef{Name: p.Name, Type: p.Type}
		}
		prog, compErr := proc.CompileProcedure(plan)
		if compErr != nil {
			writeError(w, http.StatusBadRequest, compErr)
			return
		}
		jsCode = prog.Disassemble()
		s.executor.Cache().PutByName(procName, prog)
	} else {
		writeError(w, http.StatusBadRequest, fmt.Errorf("procedure must begin with CREATE PROCEDURE, ALTER PROCEDURE, or CREATE OR ALTER PROCEDURE (T-SQL)"))
		return
	}

	procMeta := &catalog.Procedure{
		Name:   procName,
		Params: paramDefs,
		Body:   req.Script,
		JSCode: jsCode,
	}

	var regErr error
	if isAlter {
		regErr = s.catalog.AlterProcedure(procMeta)
	} else if orAlter {
		regErr = s.catalog.RegisterProcedure(procMeta)
	} else {
		regErr = s.catalog.CreateProcedure(procMeta)
	}

	if regErr != nil {
		if errors.Is(regErr, catalog.ErrProcExists) {
			writeError(w, http.StatusConflict, fmt.Errorf("procedure %q already exists. Use 'CREATE OR ALTER PROCEDURE' or 'ALTER PROCEDURE' to replace it", procName))
			return
		}
		if errors.Is(regErr, catalog.ErrProcNotFound) {
			writeError(w, http.StatusNotFound, fmt.Errorf("cannot alter procedure %q because it does not exist", procName))
			return
		}
		writeError(w, http.StatusInternalServerError, regErr)
		return
	}

	msg := fmt.Sprintf("Procedure %q registered and bytecode compiled successfully", procName)
	if isAlter || orAlter {
		msg = fmt.Sprintf("Procedure %q altered and bytecode compiled successfully", procName)
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"name":    procName,
		"params":  paramDefs,
		"js_code": jsCode,
		"message": msg,
	})
}

type execProcReq struct {
	Args map[string]any `json:"args"`
}

func (s *Server) handleExecProc(w http.ResponseWriter, r *http.Request) {
	procName := r.PathValue("name")

	prog, found := s.executor.Cache().GetByName(procName)
	if !found {
		// Try to compile from catalog
		procMeta, err := s.catalog.GetProcedure(procName)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Errorf("procedure %q not found", procName))
			return
		}
		prog, err = s.executor.Cache().GetOrCompile(procMeta.Body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.executor.Cache().PutByName(procName, prog)
	}

	var req execProcReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Args == nil {
		req.Args = make(map[string]any)
	}

	// Normalize arguments so both "@arg" and "arg" work seamlessly
	normalizedArgs := make(map[string]any)
	for k, v := range req.Args {
		normalizedArgs[k] = v
		normalizedArgs[strings.TrimPrefix(k, "@")] = v
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	res, err := s.executor.Run(prog, procName, normalizedArgs, session)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"return_code": res.ReturnValue,
		"duration_ms": float64(res.Duration.Microseconds()) / 1000.0,
		"logs":        res.Logs,
	})
}

func convertValue(v any, targetType storage.DataType) storage.Value {
	if v == nil {
		return storage.NullValue(targetType)
	}
	switch targetType {
	case storage.TypeInt:
		switch num := v.(type) {
		case float64:
			return storage.IntValue(int64(num))
		case int:
			return storage.IntValue(int64(num))
		case int64:
			return storage.IntValue(num)
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			var i int64
			fmt.Sscanf(num, "%d", &i)
			return storage.IntValue(i)
		}
	case storage.TypeVarChar:
		return storage.StringValue(fmt.Sprintf("%v", v))
	case storage.TypeFloat:
		switch num := v.(type) {
		case float64:
			return storage.FloatValue(num)
		case int:
			return storage.FloatValue(float64(num))
		case int64:
			return storage.FloatValue(float64(num))
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			f, _ := strconv.ParseFloat(num, 64)
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		switch num := v.(type) {
		case storage.Decimal:
			return storage.DecimalValueFromDecimal(num)
		case float64:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromFloat(num, 4))
		case int:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromInt(int64(num), 0))
		case int64:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromInt(num, 0))
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			return storage.DecimalValue(num)
		default:
			return storage.DecimalValue(fmt.Sprintf("%v", v))
		}
	case storage.TypeBool:
		switch b := v.(type) {
		case bool:
			return storage.BoolValue(b)
		case string:
			s := strings.ToLower(strings.TrimSpace(b))
			return storage.BoolValue(s == "true" || s == "1" || s == "t")
		case int, int64:
			return storage.BoolValue(fmt.Sprintf("%v", b) == "1")
		}
	case storage.TypeDateTime:
		switch t := v.(type) {
		case time.Time:
			return storage.DateTimeValue(t)
		case string:
			if t == "" {
				return storage.NullValue(targetType)
			}
			for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
				if parsed, err := time.Parse(layout, t); err == nil {
					return storage.DateTimeValue(parsed)
				}
			}
		}
	}
	return storage.StringValue(fmt.Sprintf("%v", v))
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	// If GET, or download parameter requested, stream backup directly
	if r.Method == http.MethodGet || r.URL.Query().Get("download") == "true" || r.URL.Query().Get("download") == "1" {
		filename := fmt.Sprintf("sqlion-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		_, _ = backup.CreateBackup(w, s.engine, s.catalog)
		return
	}

	// For POST, parse JSON body to see if a specific server target path is provided
	var req struct {
		Target string `json:"target"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if strings.TrimSpace(req.Target) == "" {
		filename := fmt.Sprintf("sqlion-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		_, _ = backup.CreateBackup(w, s.engine, s.catalog)
		return
	}

	manifest, err := backup.CreateBackupFile(req.Target, s.engine, s.catalog)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("backup failed: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "success",
		"target":   req.Target,
		"manifest": manifest,
	})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	opts := backup.DefaultRestoreOptions()

	// 1. Multipart form file upload
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(256 * 1024 * 1024); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("failed to parse multipart form: %w", err))
			return
		}
		file, _, err := r.FormFile("backup")
		if err != nil {
			file, _, err = r.FormFile("file")
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("missing backup archive file (field 'backup' or 'file')"))
			return
		}
		defer file.Close()

		res, err := backup.RestoreBackup(file, s.engine, s.catalog, opts)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("restore failed: %w", err))
			return
		}

		if s.broker != nil {
			s.broker.Broadcast(RealtimeEvent{
				Action: ActionCreate,
				Table:  "*",
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"result": res,
		})
		return
	}

	// 2. JSON request referencing server file path
	var req struct {
		Source       string `json:"source"`
		DropExisting *bool  `json:"drop_existing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json request: %w", err))
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("source file path is required"))
		return
	}
	if req.DropExisting != nil {
		opts.DropExisting = *req.DropExisting
	}

	res, err := backup.RestoreBackupFile(req.Source, s.engine, s.catalog, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("restore failed: %w", err))
		return
	}

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionCreate,
			Table:  "*",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"result": res,
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
