package wire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"sqlion/pkg/catalog"
	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
)

// Server implements a PostgreSQL Wire Protocol v3 listener.
type Server struct {
	addr      string
	listener  net.Listener
	engine    storage.Engine
	catalog   *catalog.Catalog
	planCache *proc.PlanCache
	executor  *runtime.Executor
	mu        sync.Mutex
	closed    bool
}

// NewServer creates a new PostgreSQL wire protocol server.
func NewServer(addr string, cat *catalog.Catalog, eng storage.Engine) *Server {
	cache := runtime.NewPlanCache()
	return &Server{
		addr:      addr,
		catalog:   cat,
		engine:    eng,
		planCache: proc.NewPlanCache(),
		executor:  runtime.NewExecutor(cache),
	}
}

// Start begins listening on the configured TCP address.
func (s *Server) Start() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = l

	go s.acceptLoop()
	return nil
}

// Close terminates the listener and active connections.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			isClosed := s.closed
			s.mu.Unlock()
			if isClosed {
				return
			}
			continue
		}
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// 1. Initial Handshake: SSL or Startup
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return
	}

	code := binary.BigEndian.Uint32(buf[4:8])
	if code == 80877103 {
		// SSLRequest: Reply 'N' (no SSL)
		if _, err := conn.Write([]byte{'N'}); err != nil {
			return
		}
		// Read actual startup message: 4 bytes length + 4 bytes protocol version
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
	}

	startupLen := binary.BigEndian.Uint32(buf[:4])
	if startupLen > 8 {
		payload := make([]byte, startupLen-8)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
	}

	// 2. Send Auth OK & Server Parameters
	if err := WriteAuthOK(conn); err != nil {
		return
	}
	_ = WriteParameterStatus(conn, "server_version", "15.0 (SQLion)")
	_ = WriteParameterStatus(conn, "client_encoding", "UTF8")
	_ = WriteParameterStatus(conn, "DateStyle", "ISO, MDY")
	if err := WriteReadyForQuery(conn); err != nil {
		return
	}

	// 3. Message Loop
	for {
		msgHeader := make([]byte, 5)
		if _, err := io.ReadFull(conn, msgHeader); err != nil {
			return
		}

		msgType := msgHeader[0]
		msgLen := binary.BigEndian.Uint32(msgHeader[1:5])

		if msgLen < 4 {
			return
		}
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		switch msgType {
		case 'Q': // Simple Query
			query := string(bytes.TrimRight(payload, "\x00"))
			s.handleQuery(conn, query)
		case 'X': // Terminate
			return
		default:
			// Ignore unsupported packets and stay ready
			_ = WriteReadyForQuery(conn)
		}
	}
}

func (s *Server) handleQuery(conn net.Conn, query string) {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	defer WriteReadyForQuery(conn)

	// Version check query
	if strings.Contains(upper, "SELECT VERSION()") || strings.Contains(upper, "VERSION()") {
		cols := []ColumnMeta{{Name: "version", DataType: 25}}
		_ = WriteRowDescription(conn, cols)
		_ = WriteDataRow(conn, []string{"PostgreSQL 15.0 (SQLion OLTP & Stored Procedure Server)"})
		_ = WriteCommandComplete(conn, "SELECT 1")
		return
	}

	// EXEC / CALL Stored Procedure: e.g. EXEC TransferFunds @from=1, @to=2, @amount=300
	if strings.HasPrefix(upper, "EXEC ") || strings.HasPrefix(upper, "CALL ") {
		s.handleExecQuery(conn, trimmed)
		return
	}

	// SELECT query: e.g. SELECT * FROM accounts
	if strings.HasPrefix(upper, "SELECT") && strings.Contains(upper, "FROM") {
		s.handleSelectQuery(conn, trimmed)
		return
	}

	// Default fallback
	_ = WriteCommandComplete(conn, "OK")
}

func (s *Server) handleExecQuery(conn net.Conn, query string) {
	// e.g. EXEC TransferFunds 1, 2, 300
	fields := strings.Fields(query)
	if len(fields) < 2 {
		_ = WriteError(conn, "invalid EXEC syntax")
		return
	}

	procName := fields[1]
	procMeta, err := s.catalog.GetProcedure(procName)
	if err != nil {
		_ = WriteError(conn, fmt.Sprintf("procedure %s not found", procName))
		return
	}

	prog, found := s.executor.Cache().GetByName(procName)
	if !found {
		if procMeta.JSCode == "" {
			plan, err := s.planCache.GetOrCompile(procMeta.Body)
			if err != nil {
				_ = WriteError(conn, fmt.Sprintf("compile error: %v", err))
				return
			}
			jsCode, _ := proc.Transpile(plan)
			procMeta.JSCode = jsCode
		}
		prog, err = s.executor.Cache().GetOrCompile(procMeta.JSCode)
		if err != nil {
			_ = WriteError(conn, fmt.Sprintf("bytecode error: %v", err))
			return
		}
		s.executor.Cache().PutByName(procName, prog)
	}

	session, err := s.engine.NewSession()
	if err != nil {
		_ = WriteError(conn, err.Error())
		return
	}
	defer session.Close()

	// Extract positional arguments if provided
	args := make(map[string]any)
	argTokens := fields[2:]
	for i, param := range procMeta.Params {
		if i < len(argTokens) {
			raw := strings.Trim(argTokens[i], ",; '")
			if num, err := strconv.ParseInt(raw, 10, 64); err == nil {
				args[param.Name] = num
				args[strings.TrimPrefix(param.Name, "@")] = num
			} else {
				args[param.Name] = raw
				args[strings.TrimPrefix(param.Name, "@")] = raw
			}
		}
	}

	res, err := s.executor.Run(prog, procName, args, session)
	if err != nil {
		_ = WriteError(conn, fmt.Sprintf("execution failed (rolled back): %v", err))
		return
	}

	// Return logs and return code as a result row
	cols := []ColumnMeta{
		{Name: "return_code", DataType: 25},
		{Name: "duration_us", DataType: 20},
	}
	_ = WriteRowDescription(conn, cols)
	_ = WriteDataRow(conn, []string{
		fmt.Sprintf("%v", res.ReturnValue),
		fmt.Sprintf("%d", res.Duration.Microseconds()),
	})
	_ = WriteCommandComplete(conn, "EXEC 1")
}

func (s *Server) handleSelectQuery(conn net.Conn, query string) {
	// Parse simple table name from SELECT ... FROM <table_name>
	parts := strings.Split(strings.ToUpper(query), "FROM")
	if len(parts) < 2 {
		_ = WriteError(conn, "unsupported SELECT query")
		return
	}

	afterFrom := strings.Fields(parts[1])
	if len(afterFrom) == 0 {
		_ = WriteError(conn, "missing table name in SELECT")
		return
	}
	tableName := strings.Trim(afterFrom[0], ";, ")

	tbl, err := s.engine.GetTable(tableName)
	if err != nil {
		tbl, err = s.engine.GetTable(strings.ToLower(tableName))
		if err != nil {
			_ = WriteError(conn, fmt.Sprintf("table %s not found", tableName))
			return
		}
	}
	tableName = tbl.Name

	session, err := s.engine.NewSession()
	if err != nil {
		_ = WriteError(conn, err.Error())
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		_ = WriteError(conn, err.Error())
		return
	}
	defer tx.Rollback()

	cursor, err := tx.Scan(tableName)
	if err != nil {
		_ = WriteError(conn, err.Error())
		return
	}
	defer cursor.Close()

	cols := make([]ColumnMeta, len(tbl.Columns))
	for i, c := range tbl.Columns {
		cols[i] = ColumnMeta{Name: c.Name, DataType: 25}
	}
	_ = WriteRowDescription(conn, cols)

	rowCount := 0
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			continue
		}
		vals := make([]string, len(tbl.Columns))
		for i, c := range tbl.Columns {
			v, exists := rec[c.Name]
			if !exists || v.IsNull {
				vals[i] = ""
			} else {
				vals[i] = v.String()
			}
		}
		_ = WriteDataRow(conn, vals)
		rowCount++
	}

	_ = WriteCommandComplete(conn, fmt.Sprintf("SELECT %d", rowCount))
}
