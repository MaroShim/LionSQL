package wire

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
)

type pgActiveConn struct {
	sessID    string
	secretKey uint32
}

// Server implements a PostgreSQL Wire Protocol v3 listener.
type Server struct {
	addr        string
	listener    net.Listener
	engine      storage.Engine
	catalog     *catalog.Catalog
	apiServer   *api.Server
	planCache   *proc.PlanCache
	executor    *runtime.Executor
	connCounter uint64
	activeConns map[uint32]*pgActiveConn
	tlsConfig   *tls.Config
	requireSSL  bool
	mu          sync.Mutex
	closed      bool
}

// NewServer creates a new PostgreSQL wire protocol server.
func NewServer(addr string, cat *catalog.Catalog, eng storage.Engine) *Server {
	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	if eng != nil {
		executor.SetTableGetter(eng.GetTable)
	}
	return &Server{
		addr:        addr,
		catalog:     cat,
		engine:      eng,
		apiServer:   api.NewServer(cat, eng),
		planCache:   proc.NewPlanCache(),
		executor:    executor,
		activeConns: make(map[uint32]*pgActiveConn),
	}
}

// SetTLSConfig sets the TLS configuration for encrypted SSL connections.
func (s *Server) SetTLSConfig(cfg *tls.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tlsConfig = cfg
}

// SetRequireSSL specifies whether plaintext connections should be rejected when SSL is enabled.
func (s *Server) SetRequireSSL(require bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requireSSL = require
}

// TLSConfig returns the current TLS configuration.
func (s *Server) TLSConfig() *tls.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tlsConfig
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
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetNoDelay(true)
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
		}
		go s.handleConnection(conn)
	}
}

type PreparedStatement struct {
	Name       string
	Query      string
	ParamTypes []int32
	Program    *proc.Program
}

type Portal struct {
	Name      string
	Statement *PreparedStatement
	Params    []string
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	connID := atomic.AddUint64(&s.connCounter, 1)
	sessID := fmt.Sprintf("pg-%d", connID)
	defer s.apiServer.CloseSession(sessID)

	// 1. Initial Handshake: SSL or GSSENC or Cancel or Startup
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return
	}

	code := binary.BigEndian.Uint32(buf[4:8])
	if code == 80877102 {
		// CancelRequest (length=16, code=80877102, PID=4B, SecretKey=4B)
		cancelBuf := make([]byte, 8)
		if _, err := io.ReadFull(conn, cancelBuf); err != nil {
			return
		}
		targetPID := binary.BigEndian.Uint32(cancelBuf[:4])
		targetKey := binary.BigEndian.Uint32(cancelBuf[4:8])

		s.mu.Lock()
		active, exists := s.activeConns[targetPID]
		var targetSessID string
		if exists && active.secretKey == targetKey {
			targetSessID = active.sessID
		}
		s.mu.Unlock()

		if targetSessID != "" {
			s.apiServer.CloseSession(targetSessID)
		}
		return
	}

	// GSSENCRequest: 80877104 -> Reply 'N' (no GSS encryption)
	if code == 80877104 {
		if _, err := conn.Write([]byte{'N'}); err != nil {
			return
		}
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		code = binary.BigEndian.Uint32(buf[4:8])
	}

	if code == 80877103 {
		s.mu.Lock()
		tlsCfg := s.tlsConfig
		s.mu.Unlock()

		if tlsCfg != nil {
			// Acknowledge SSL upgrade: Reply 'S'
			if _, err := conn.Write([]byte{'S'}); err != nil {
				return
			}
			tlsConn := tls.Server(conn, tlsCfg)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
		} else {
			// SSLRequest: Reply 'N' (no SSL)
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return
			}
		}

		// Read actual startup message: 4 bytes length + 4 bytes protocol version
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		code = binary.BigEndian.Uint32(buf[4:8])
		if code == 80877102 {
			cancelBuf := make([]byte, 8)
			if _, err := io.ReadFull(conn, cancelBuf); err != nil {
				return
			}
			targetPID := binary.BigEndian.Uint32(cancelBuf[:4])
			targetKey := binary.BigEndian.Uint32(cancelBuf[4:8])

			s.mu.Lock()
			active, exists := s.activeConns[targetPID]
			var targetSessID string
			if exists && active.secretKey == targetKey {
				targetSessID = active.sessID
			}
			s.mu.Unlock()

			if targetSessID != "" {
				s.apiServer.CloseSession(targetSessID)
			}
			return
		}
	} else {
		s.mu.Lock()
		reqSSL := s.requireSSL && s.tlsConfig != nil
		s.mu.Unlock()

		if reqSSL {
			_ = WriteError(conn, "SSL is required to connect to this server")
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

	pid := uint32(connID)
	secretKey := uint32(connID*10007 + 42)
	s.mu.Lock()
	s.activeConns[pid] = &pgActiveConn{sessID: sessID, secretKey: secretKey}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.activeConns, pid)
		s.mu.Unlock()
	}()

	// 2. Send Auth OK, BackendKeyData & Server Parameters
	if err := WriteAuthOK(conn); err != nil {
		return
	}
	if err := WriteBackendKeyData(conn, pid, secretKey); err != nil {
		return
	}
	_ = WriteParameterStatus(conn, "server_version", "15.0 (SQLion)")
	_ = WriteParameterStatus(conn, "server_encoding", "UTF8")
	_ = WriteParameterStatus(conn, "client_encoding", "UTF8")
	_ = WriteParameterStatus(conn, "application_name", "")
	_ = WriteParameterStatus(conn, "is_superuser", "on")
	_ = WriteParameterStatus(conn, "session_authorization", "postgres")
	_ = WriteParameterStatus(conn, "integer_datetimes", "on")
	_ = WriteParameterStatus(conn, "DateStyle", "ISO, MDY")
	_ = WriteParameterStatus(conn, "IntervalStyle", "postgres")
	_ = WriteParameterStatus(conn, "standard_conforming_strings", "on")
	_ = WriteParameterStatus(conn, "TimeZone", "UTC")
	if err := WriteReadyForQuery(conn); err != nil {
		return
	}

	statements := make(map[string]*PreparedStatement)
	portals := make(map[string]*Portal)

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
			trimmed := strings.TrimSpace(query)
			if strings.HasPrefix(strings.ToUpper(trimmed), "COPY ") {
				s.handleCopy(conn, trimmed, sessID)
			} else {
				s.handleSimpleQuery(conn, query, sessID)
			}

		case 'P': // Parse
			s.handleParse(conn, payload, statements)

		case 'B': // Bind
			s.handleBind(conn, payload, statements, portals)

		case 'D': // Describe
			s.handleDescribe(conn, payload, statements, portals)

		case 'E': // Execute
			s.handleExecute(conn, payload, portals, sessID)

		case 'S': // Sync
			_ = WriteReadyForQuery(conn)

		case 'C': // Close
			s.handleClose(conn, payload, statements, portals)

		case 'H': // Flush
			// No-op

		case 'X': // Terminate
			return

		default:
			// Stay ready
			_ = WriteReadyForQuery(conn)
		}
	}
}

func (s *Server) handleSimpleQuery(conn net.Conn, query string, sessID string) {
	defer WriteReadyForQuery(conn)
	s.runAndWriteQuery(conn, query, true, sessID)
}

var pgParamRe = regexp.MustCompile(`\$(\d+)`)

func (s *Server) compileQueryToProgram(query string) (*proc.Program, error) {
	translated := pgParamRe.ReplaceAllString(query, "@p$1")
	return proc.Compile(translated)
}

func (s *Server) handleParse(conn net.Conn, payload []byte, statements map[string]*PreparedStatement) {
	r := bytes.NewReader(payload)
	stmtName, _ := readCString(r)
	queryStr, _ := readCString(r)

	var numParamTypes int16
	_ = binary.Read(r, binary.BigEndian, &numParamTypes)

	paramTypes := make([]int32, numParamTypes)
	for i := 0; i < int(numParamTypes); i++ {
		_ = binary.Read(r, binary.BigEndian, &paramTypes[i])
	}

	prep := &PreparedStatement{
		Name:       stmtName,
		Query:      queryStr,
		ParamTypes: paramTypes,
	}

	if prog, err := s.compileQueryToProgram(queryStr); err == nil && prog != nil {
		prep.Program = prog
	}

	statements[stmtName] = prep

	_ = WriteParseComplete(conn)
}

func (s *Server) handleBind(conn net.Conn, payload []byte, statements map[string]*PreparedStatement, portals map[string]*Portal) {
	r := bytes.NewReader(payload)
	portalName, _ := readCString(r)
	stmtName, _ := readCString(r)

	stmt, exists := statements[stmtName]
	if !exists {
		_ = WriteError(conn, fmt.Sprintf("prepared statement %q does not exist", stmtName))
		return
	}

	var numFormats int16
	_ = binary.Read(r, binary.BigEndian, &numFormats)
	formats := make([]int16, numFormats)
	for i := 0; i < int(numFormats); i++ {
		_ = binary.Read(r, binary.BigEndian, &formats[i])
	}

	var numParams int16
	_ = binary.Read(r, binary.BigEndian, &numParams)
	paramVals := make([]string, numParams)

	for i := 0; i < int(numParams); i++ {
		var paramLen int32
		_ = binary.Read(r, binary.BigEndian, &paramLen)
		if paramLen == -1 {
			paramVals[i] = "NULL"
		} else {
			valBytes := make([]byte, paramLen)
			_, _ = io.ReadFull(r, valBytes)
			paramVals[i] = string(valBytes)
		}
	}

	portals[portalName] = &Portal{
		Name:      portalName,
		Statement: stmt,
		Params:    paramVals,
	}

	_ = WriteBindComplete(conn)
}

func (s *Server) handleDescribe(conn net.Conn, payload []byte, statements map[string]*PreparedStatement, portals map[string]*Portal) {
	if len(payload) < 2 {
		_ = WriteNoData(conn)
		return
	}

	targetType := payload[0]
	targetName := string(bytes.TrimRight(payload[1:], "\x00"))

	if targetType == 'S' {
		stmt, exists := statements[targetName]
		if !exists {
			_ = WriteError(conn, fmt.Sprintf("prepared statement %q does not exist", targetName))
			return
		}
		_ = WriteParameterDescription(conn, stmt.ParamTypes)
		s.writeSchemaDescription(conn, stmt.Query)
	} else if targetType == 'P' {
		portal, exists := portals[targetName]
		if !exists {
			_ = WriteError(conn, fmt.Sprintf("portal %q does not exist", targetName))
			return
		}
		s.writeSchemaDescription(conn, portal.Statement.Query)
	} else {
		_ = WriteNoData(conn)
	}
}

func (s *Server) writeSchemaDescription(conn net.Conn, query string) {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.Contains(upper, "SELECT") && strings.Contains(upper, "FROM") {
		parts := strings.Split(upper, "FROM")
		if len(parts) >= 2 {
			tblField := strings.Fields(parts[1])
			if len(tblField) > 0 {
				tblName := strings.Trim(tblField[0], ";, ")
				tbl, err := s.engine.GetTable(tblName)
				if err != nil {
					tbl, err = s.engine.GetTable(strings.ToLower(tblName))
				}
				if err != nil {
					// Search all tables
					tables, _ := s.engine.ListTables()
					for _, t := range tables {
						if strings.EqualFold(t.Name, tblName) {
							tbl = t
							err = nil
							break
						}
					}
				}
				if err == nil && tbl != nil {
					cols := make([]ColumnMeta, len(tbl.Columns))
					for i, c := range tbl.Columns {
						cols[i] = ColumnMeta{
							Name:     c.Name,
							DataType: typeToPgOID(c.Type),
						}
					}
					_ = WriteRowDescription(conn, cols)
					return
				}
			}
		}
	}
	_ = WriteNoData(conn)
}

func (s *Server) handleExecute(conn net.Conn, payload []byte, portals map[string]*Portal, sessID string) {
	r := bytes.NewReader(payload)
	portalName, _ := readCString(r)

	portal, exists := portals[portalName]
	if !exists {
		_ = WriteError(conn, fmt.Sprintf("portal %q does not exist", portalName))
		return
	}

	if sessID == "" {
		sessID = "default"
	}

	// Fast-path: Execute via precompiled VDBE bytecode Program if available
	if portal.Statement != nil && portal.Statement.Program != nil {
		paramMap := make(map[string]any, len(portal.Params)*3)
		for i, paramVal := range portal.Params {
			pIdx := i + 1
			paramMap[fmt.Sprintf("p%d", pIdx)] = paramVal
			paramMap[fmt.Sprintf("@p%d", pIdx)] = paramVal
			paramMap[fmt.Sprintf("$%d", pIdx)] = paramVal
		}

		session, err := s.engine.NewSession()
		if err == nil {
			defer session.Close()
			res, err := s.executor.Run(portal.Statement.Program, "PreparedStatement", paramMap, session)
			if err == nil {
				s.writeExecutionResult(conn, res)
				return
			}
		}
	}

	// Fallback to text substitution
	substitutedSQL := substituteParams(portal.Statement.Query, portal.Params)
	s.runAndWriteQuery(conn, substitutedSQL, false, sessID)
}

func (s *Server) writeExecutionResult(conn net.Conn, res *runtime.ExecutionResult) {
	if len(res.Columns) > 0 {
		cols := make([]ColumnMeta, len(res.Columns))
		for i, c := range res.Columns {
			cols[i] = ColumnMeta{Name: c, DataType: 25}
		}
		_ = WriteRowDescription(conn, cols)

		for _, row := range res.Rows {
			vals := make([]string, len(res.Columns))
			for i, colName := range res.Columns {
				if v, ok := row[colName]; ok && v != nil {
					vals[i] = fmt.Sprintf("%v", v)
				} else {
					vals[i] = ""
				}
			}
			_ = WriteDataRow(conn, vals)
		}
		_ = WriteCommandComplete(conn, fmt.Sprintf("SELECT %d", len(res.Rows)))
		return
	}
	rowsCount := int64(len(res.Rows))
	if rowsCount == 0 && res.RowsAffected > 0 {
		rowsCount = res.RowsAffected
	}
	_ = WriteCommandComplete(conn, fmt.Sprintf("OK %d", rowsCount))
}

func (s *Server) handleClose(conn net.Conn, payload []byte, statements map[string]*PreparedStatement, portals map[string]*Portal) {
	if len(payload) >= 2 {
		targetType := payload[0]
		targetName := string(bytes.TrimRight(payload[1:], "\x00"))
		if targetType == 'S' {
			delete(statements, targetName)
		} else if targetType == 'P' {
			delete(portals, targetName)
		}
	}
	_ = WriteCloseComplete(conn)
}

func (s *Server) runAndWriteQuery(conn net.Conn, query string, writeRowDesc bool, sessID string) {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	// Builtin pg check
	if strings.Contains(upper, "VERSION()") {
		cols := []ColumnMeta{{Name: "version", DataType: 25}}
		if writeRowDesc {
			_ = WriteRowDescription(conn, cols)
		}
		_ = WriteDataRow(conn, []string{"PostgreSQL 15.0 (SQLion OLTP & Stored Procedure Server)"})
		_ = WriteCommandComplete(conn, "SELECT 1")
		return
	}

	if sessID == "" {
		sessID = "default"
	}
	res, err := s.apiServer.ExecuteSessionSQL(sessID, trimmed)
	if err != nil {
		_ = WriteError(conn, err.Error())
		return
	}

	if len(res.Columns) > 0 {
		if writeRowDesc {
			cols := make([]ColumnMeta, len(res.Columns))
			for i, c := range res.Columns {
				cols[i] = ColumnMeta{Name: c, DataType: 25}
			}
			_ = WriteRowDescription(conn, cols)
		}

		for _, row := range res.Rows {
			vals := make([]string, len(res.Columns))
			for i, colName := range res.Columns {
				if v, ok := row[colName]; ok && v != nil {
					vals[i] = fmt.Sprintf("%v", v)
				} else {
					vals[i] = ""
				}
			}
			_ = WriteDataRow(conn, vals)
		}
		_ = WriteCommandComplete(conn, fmt.Sprintf("SELECT %d", len(res.Rows)))
		return
	}

	cmd := res.Command
	if cmd == "" {
		cmd = "OK"
	}
	_ = WriteCommandComplete(conn, fmt.Sprintf("%s %d", cmd, res.RowsAffected))
}

func substituteParams(sql string, params []string) string {
	res := sql
	for i := len(params); i >= 1; i-- {
		placeholder := fmt.Sprintf("$%d", i)
		val := params[i-1]
		if _, err := strconv.ParseFloat(val, 64); err == nil {
			res = strings.ReplaceAll(res, placeholder, val)
		} else if strings.EqualFold(val, "true") || strings.EqualFold(val, "false") || strings.EqualFold(val, "null") {
			res = strings.ReplaceAll(res, placeholder, val)
		} else {
			escaped := strings.ReplaceAll(val, "'", "''")
			res = strings.ReplaceAll(res, placeholder, fmt.Sprintf("'%s'", escaped))
		}
	}
	return res
}

func typeToPgOID(t storage.DataType) int32 {
	switch t {
	case storage.TypeInt:
		return 23 // INT4
	case storage.TypeFloat:
		return 701 // FLOAT8
	case storage.TypeBool:
		return 16 // BOOL
	case storage.TypeDateTime:
		return 1114 // TIMESTAMP
	default:
		return 1043 // VARCHAR
	}
}

func readCString(r *bytes.Reader) (string, error) {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return string(buf), err
		}
		if b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf), nil
}
