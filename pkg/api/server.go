package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/auth"
	"sqlion/pkg/catalog"
	"sqlion/pkg/proc"
	_ "sqlion/pkg/proc/parser/antlr"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/ui"
)

// ClientSession maintains interactive connection state: open transaction, session variables.
type ClientSession struct {
	ID      string
	Session storage.Session
	Tx      storage.Transaction
	Vars    map[string]any
	mu      sync.Mutex
}

// Server provides the PocketBase-style HTTP API and Embedded Web Dashboard.
type Server struct {
	catalog    *catalog.Catalog
	engine     storage.Engine
	planCache  *proc.PlanCache
	executor   *runtime.Executor
	auth       *auth.AuthService
	enableAuth bool
	mux        *http.ServeMux
	sessions   map[string]*ClientSession
	sessMu     sync.RWMutex
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
	s := &Server{
		catalog:    cat,
		engine:     eng,
		planCache:  proc.NewPlanCache(),
		executor:   runtime.NewExecutor(cache),
		auth:       authSvc,
		enableAuth: enableAuth,
		mux:        http.NewServeMux(),
		sessions:   make(map[string]*ClientSession),
	}
	s.routes()
	return s
}

// AuthService returns the underlying authentication service.
func (s *Server) AuthService() *auth.AuthService {
	return s.auth
}

// SetEnableAuth toggles authentication enforcement for protected API routes.
func (s *Server) SetEnableAuth(enable bool) {
	s.enableAuth = enable
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

	sess, err := s.engine.NewSession()
	if err != nil {
		return nil, err
	}
	cs := &ClientSession{
		ID:      id,
		Session: sess,
		Vars:    make(map[string]any),
	}
	s.sessions[id] = cs
	return cs, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
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
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": "1.0.0"})
	})

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

	// Tables API (PocketBase-style Collections & Records CRUD)
	s.mux.HandleFunc("GET /api/tables", s.wrapAuth(s.handleListTables, false, ""))
	s.mux.HandleFunc("POST /api/tables", s.wrapSuperuser(s.handleCreateTable))
	s.mux.HandleFunc("DELETE /api/tables/{name}", s.wrapSuperuser(s.handleDropTable))
	s.mux.HandleFunc("GET /api/tables/{name}/schema", s.wrapTableAuth(s.handleGetTableSchema, false))
	s.mux.HandleFunc("GET /api/tables/{name}/records", s.wrapTableAuth(s.handleGetRecords, false))
	s.mux.HandleFunc("POST /api/tables/{name}/records", s.wrapTableAuth(s.handleCreateRecord, true))
	s.mux.HandleFunc("PATCH /api/tables/{name}/records/{pk}", s.wrapTableAuth(s.handleUpdateRecord, true))
	s.mux.HandleFunc("DELETE /api/tables/{name}/records/{pk}", s.wrapTableAuth(s.handleDeleteRecord, true))

	// Stored Procedures API
	s.mux.HandleFunc("GET /api/procs", s.wrapAuth(s.handleListProcs, false, ""))
	s.mux.HandleFunc("POST /api/procs", s.wrapAuth(s.handleRegisterProc, true, ""))
	s.mux.HandleFunc("POST /api/procs/{name}/exec", s.wrapAuth(s.handleExecProc, false, ""))
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
		tableName := r.PathValue("name")
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
}

func (s *Server) handleExecuteSQL(w http.ResponseWriter, r *http.Request) {
	var req execSQLReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := s.ExecuteSQL(req.Query)
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
	tableName := r.PathValue("name")
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, toTableDTO(schema))
}

func (s *Server) handleGetRecords(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
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
		row := make(map[string]any)
		for k, v := range rec {
			if v.IsNull {
				row[k] = nil
			} else {
				switch v.Type {
				case storage.TypeInt:
					row[k] = v.IVal
				case storage.TypeVarChar:
					row[k] = v.SVal
				case storage.TypeFloat:
					row[k] = v.FVal
				case storage.TypeBool:
					row[k] = v.BVal
				default:
					row[k] = v.String()
				}
			}
		}
		records = append(records, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"table":   tableName,
		"records": records,
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

	writeJSON(w, http.StatusCreated, toTableDTO(schema))
}

func (s *Server) handleDropTable(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	if err := s.engine.DropTable(tableName); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "table dropped"})
}

func (s *Server) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
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
		if !exists || v == nil {
			if col.PrimaryKey {
				// Auto-generate numeric ID if not provided
				v = time.Now().UnixNano() / int64(time.Millisecond)
			} else if !col.Nullable {
				writeError(w, http.StatusBadRequest, fmt.Errorf("field %q cannot be null", col.Name))
				return
			}
		}
		rec[col.Name] = convertValue(v, col.Type)
	}

	if err := tx.Insert(tableName, rec); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Format created record
	respRow := make(map[string]any)
	for k, v := range rec {
		if v.IsNull {
			respRow[k] = nil
		} else {
			respRow[k] = v.String()
		}
	}

	writeJSON(w, http.StatusCreated, respRow)
}

func (s *Server) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	pkStr := r.PathValue("pk")

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
		writeError(w, http.StatusNotFound, fmt.Errorf("record with pk %s not found: %w", pkStr, err))
		return
	}

	for k, v := range updates {
		col, _, found := schema.FindColumn(k)
		if !found {
			continue
		}
		existing[k] = convertValue(v, col.Type)
	}

	if err := tx.Update(tableName, pkVal, existing); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	respRow := make(map[string]any)
	for k, v := range existing {
		if v.IsNull {
			respRow[k] = nil
		} else {
			respRow[k] = v.String()
		}
	}
	writeJSON(w, http.StatusOK, respRow)
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	pkStr := r.PathValue("pk")

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

	if err := tx.Delete(tableName, pkVal); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "record deleted"})
}

func (s *Server) handleSeed(w http.ResponseWriter, r *http.Request) {
	// 1. Create accounts table if not exists
	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = s.engine.CreateTable(schema)

	// 2. Insert sample rows
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
	_ = tx.Commit()

	writeJSON(w, http.StatusOK, map[string]string{"message": "Sample accounts table created and seeded successfully!"})
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
	var procName string
	var paramDefs []catalog.ParamDef
	var jsCode string

	if strings.HasPrefix(strings.ToUpper(trimmed), "CREATE ") {
		plan, err := s.planCache.GetOrCompile(req.Script)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		procName = plan.Name
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
		writeError(w, http.StatusBadRequest, fmt.Errorf("procedure must begin with CREATE PROCEDURE (T-SQL)"))
		return
	}

	procMeta := &catalog.Procedure{
		Name:   procName,
		Params: paramDefs,
		Body:   req.Script,
		JSCode: jsCode,
	}

	if err := s.catalog.RegisterProcedure(procMeta); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"name":    procName,
		"params":  paramDefs,
		"js_code": jsCode,
		"message": "Procedure registered and bytecode compiled successfully",
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
		}
	case storage.TypeBool:
		if b, ok := v.(bool); ok {
			return storage.BoolValue(b)
		}
	}
	return storage.StringValue(fmt.Sprintf("%v", v))
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
