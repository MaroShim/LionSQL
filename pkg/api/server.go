package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sqlion/pkg/auth"
	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/execution"
	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
	_ "sqlion/pkg/proc/parser/antlr"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/replication"
	"sqlion/pkg/storage"
	"sqlion/pkg/ui"
)

// Ensure Server satisfies the engine.SQLCoordinator interface.
var _ engine.SQLCoordinator = (*Server)(nil)

// ActiveQueryInfo represents snapshot metadata of an in-flight query (aliased from engine.ActiveQueryInfo).
type ActiveQueryInfo = engine.ActiveQueryInfo

// ClientSession maintains interactive connection state (aliased from engine.ClientSession).
type ClientSession = engine.ClientSession

// PreparedStatement stores compiled bytecode and parameter definitions (aliased from engine.PreparedStatement).
type PreparedStatement = engine.PreparedStatement

// Server provides the PocketBase-style HTTP API and Embedded Web Dashboard.
type Server struct {
	catalog     *catalog.Catalog
	engine      storage.Engine
	planCache   *proc.PlanCache
	executor    *runtime.Executor
	auth        *auth.AuthService
	enableAuth  bool
	broker      *RealtimeBroker
	webhooks    *WebhookDispatcher
	rls         *RLSManager
	crons       *CronScheduler
	mailer      *Mailer
	coordinator *engine.Coordinator
	mux         *http.ServeMux
	replication *replication.Manager
	startTime   time.Time
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
	broker := NewRealtimeBroker()
	webhooks := NewWebhookDispatcher()
	webhooks.StartBrokerListener(broker)
	mailer := NewMailer(SMTPConfig{})
	coord := engine.NewCoordinator(cat, eng, engine.Config{
		DefaultWorkMem:   execution.DefaultWorkMem,
		SlowThreshold:    DefaultSlowQueryThreshold,
		EnableGeneralLog: false,
		AuthService:      authSvc,
		EnableAuth:       enableAuth,
		PoolConfig:       DefaultPoolConfig(),
	})
	s := &Server{
		catalog:     cat,
		engine:      eng,
		planCache:   proc.NewPlanCache(),
		executor:    runtime.NewExecutor(cache),
		auth:        authSvc,
		enableAuth:  enableAuth,
		broker:      broker,
		webhooks:    webhooks,
		rls:         NewRLSManager(),
		mailer:      mailer,
		coordinator: coord,
		mux:         http.NewServeMux(),
		startTime:   time.Now(),
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
	s.coordinator.SetDefaultWorkMem(bytes)
}

// DefaultWorkMem returns the server-wide default work_mem limit.
func (s *Server) DefaultWorkMem() int64 {
	return s.coordinator.DefaultWorkMem()
}

func (s *Server) newPlanner() *planner.Planner {
	p := planner.NewPlanner()
	p.PartitionGetter = func(table string) []string {
		if s.engine != nil {
			if parts, err := s.engine.GetPartitions(table); err == nil {
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

// Pool returns the underlying goroutine connection & session pool.
func (s *Server) Pool() *SessionPool {
	return s.coordinator.Pool()
}

// Coordinator returns the underlying SQL execution coordinator.
func (s *Server) Coordinator() *engine.Coordinator {
	return s.coordinator
}

// SetPoolConfig updates the connection pool configuration.
func (s *Server) SetPoolConfig(cfg PoolConfig) {
	s.coordinator.SetPoolConfig(cfg)
}

// Broker returns the active realtime pub/sub broker.
func (s *Server) Broker() *RealtimeBroker {
	return s.broker
}

// AuthService returns the underlying authentication service.
func (s *Server) AuthService() *auth.AuthService {
	return s.auth
}

// IsAuthEnabled reports whether RBAC authentication and permission checks are enforced.
func (s *Server) IsAuthEnabled() bool {
	return s.enableAuth
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

// Replication returns the availability group replication manager, or nil if not configured.
func (s *Server) Replication() *replication.Manager {
	return s.replication
}

// SetReplicationManager configures the availability group replication manager and mounts replication endpoints.
func (s *Server) SetReplicationManager(rm *replication.Manager) {
	s.replication = rm
	s.coordinator.SetReplicationManager(rm)
	if rm != nil {
		rm.RegisterRoutes(s.mux)
	}
}

// IsReadOnly reports whether this server node is currently operating as a read-only secondary replica.
func (s *Server) IsReadOnly() bool {
	return s.coordinator.IsReadOnly()
}

// SetEnableAuth toggles authentication enforcement for protected API routes.
func (s *Server) SetEnableAuth(enable bool) {
	s.enableAuth = enable
	s.coordinator.SetAuthService(s.auth, enable)
}

// Metrics returns the Prometheus metrics collector instance.
func (s *Server) Metrics() *MetricsCollector {
	return s.coordinator.Metrics()
}

// SlowQueries returns the slow query logger instance.
func (s *Server) SlowQueries() *SlowQueryLogger {
	return s.coordinator.SlowQueries()
}

// SetSlowQueryThreshold sets the minimum duration for a query to be recorded as slow.
func (s *Server) SetSlowQueryThreshold(d time.Duration) {
	sq := s.coordinator.SlowQueries()
	if sq != nil {
		sq.SetThreshold(d)
	}
}

// SetSlowQueryLogFile configures disk file output for the slow query log.
func (s *Server) SetSlowQueryLogFile(filePath string) error {
	sq := s.coordinator.SlowQueries()
	if sq != nil {
		return sq.SetLogFile(filePath)
	}
	return nil
}

// GeneralLog returns the general query logger instance.
func (s *Server) GeneralLog() *GeneralLogger {
	return s.coordinator.GeneralLog()
}

// SetGeneralLogEnabled toggles general query logging.
func (s *Server) SetGeneralLogEnabled(enabled bool) {
	gl := s.coordinator.GeneralLog()
	if gl != nil {
		gl.SetEnabled(enabled)
	}
}

// SetGeneralLogFile configures disk file output for the general query log.
func (s *Server) SetGeneralLogFile(filePath string) error {
	gl := s.coordinator.GeneralLog()
	if gl != nil {
		return gl.SetLogFile(filePath)
	}
	return nil
}

// Close gracefully flushes and closes server log writers and session pools.
func (s *Server) Close() error {
	return s.coordinator.Close()
}

// GetOrCreateSession returns or registers a persistent client session.
func (s *Server) GetOrCreateSession(id string) (*ClientSession, error) {
	return s.coordinator.GetOrCreateSession(id)
}

// CloseSession gracefully closes and unregisters a client session.
func (s *Server) CloseSession(id string) {
	s.coordinator.CloseSession(id)
}

// GetSession returns the active ClientSession by ID, or nil if not found.
func (s *Server) GetSession(id string) *ClientSession {
	return s.coordinator.GetSession(id)
}

// GetSessionTxStatus returns PostgreSQL transaction status byte:
// 'I' = Idle, 'T' = In active transaction block, 'E' = In failed transaction block
func (s *Server) GetSessionTxStatus(id string) byte {
	return s.coordinator.GetSessionTxStatus(id)
}

// SetSessionUser updates the active user principal on an interactive session.
func (s *Server) SetSessionUser(id, user string) {
	s.coordinator.SetSessionUser(id, user)
}

// CancelSessionQuery triggers cooperative cancellation for the currently active statement on sessionID.
func (s *Server) CancelSessionQuery(sessionID string) bool {
	return s.coordinator.CancelSessionQuery(sessionID)
}

// CancelQueryBySPID cancels the active query executing on the connection identified by spid.
func (s *Server) CancelQueryBySPID(spid int) bool {
	return s.coordinator.CancelQueryBySPID(spid)
}

// GetActiveQueries returns a snapshot list of all currently executing queries across all sessions.
func (s *Server) GetActiveQueries() []ActiveQueryInfo {
	return s.coordinator.GetActiveQueries()
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
		if pool := s.Pool(); pool != nil {
			writeJSON(w, http.StatusOK, pool.Stats())

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
