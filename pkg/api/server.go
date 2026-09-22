package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

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
	catalog   *catalog.Catalog
	engine    storage.Engine
	planCache *proc.PlanCache
	executor  *runtime.Executor
	mux       *http.ServeMux
	sessions  map[string]*ClientSession
	sessMu    sync.RWMutex
}

// NewServer initializes a new Server.
func NewServer(cat *catalog.Catalog, eng storage.Engine) *Server {
	cache := runtime.NewPlanCache()
	s := &Server{
		catalog:   cat,
		engine:    eng,
		planCache: proc.NewPlanCache(),
		executor:  runtime.NewExecutor(cache),
		mux:       http.NewServeMux(),
		sessions:  make(map[string]*ClientSession),
	}
	s.routes()
	return s
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

	// Seed Sample Data
	s.mux.HandleFunc("POST /api/seed", s.handleSeed)

	// SQL Query Execution API
	s.mux.HandleFunc("POST /api/sql", s.handleExecuteSQL)

	// Tables API
	s.mux.HandleFunc("GET /api/tables", s.handleListTables)
	s.mux.HandleFunc("GET /api/tables/{name}/schema", s.handleGetTableSchema)
	s.mux.HandleFunc("GET /api/tables/{name}/records", s.handleGetRecords)

	// Stored Procedures API
	s.mux.HandleFunc("GET /api/procs", s.handleListProcs)
	s.mux.HandleFunc("POST /api/procs", s.handleRegisterProc)
	s.mux.HandleFunc("POST /api/procs/{name}/exec", s.handleExecProc)
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
