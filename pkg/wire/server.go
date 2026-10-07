package wire

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
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
	byteCache   *runtime.PlanCache
	executor    *runtime.Executor
	connCounter uint64
	activeConns map[uint32]*pgActiveConn
	tlsConfig   *tls.Config
	requireSSL  bool
	devMode     bool
	mu          sync.Mutex
	closed      bool
}

// NewServer creates a new PostgreSQL wire protocol server.
func NewServer(addr string, cat *catalog.Catalog, eng storage.Engine) *Server {
	return NewServerWithCache(addr, cat, eng, runtime.NewPlanCache())
}

// NewServerWithCache creates a new PostgreSQL wire protocol server with an existing bytecode PlanCache.
func NewServerWithCache(addr string, cat *catalog.Catalog, eng storage.Engine, cache *runtime.PlanCache) *Server {
	if cache == nil {
		cache = runtime.NewPlanCache()
	}
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
		byteCache:   cache,
		executor:    executor,
		activeConns: make(map[uint32]*pgActiveConn),
	}
}

// SetBytecodePlanCache sets or replaces the bytecode plan cache and updates the executor.
func (s *Server) SetBytecodePlanCache(cache *runtime.PlanCache) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cache == nil {
		cache = runtime.NewPlanCache()
	}
	s.byteCache = cache
	s.executor = runtime.NewExecutor(cache)
	if s.engine != nil {
		s.executor.SetTableGetter(s.engine.GetTable)
	}
}

// BytecodePlanCache returns the active bytecode plan cache.
func (s *Server) BytecodePlanCache() *runtime.PlanCache {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byteCache != nil {
		return s.byteCache
	}
	if s.executor != nil {
		return s.executor.Cache()
	}
	return nil
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

// SetDevMode enables or disables developer debug logging for wire queries and parameters.
func (s *Server) SetDevMode(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devMode = enabled
}

// DevMode reports whether developer debug logging is active.
func (s *Server) DevMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devMode
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

// Addr returns the listener's network address.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr()
	}
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
	Name          string
	Statement     *PreparedStatement
	Params        []string
	ResultFormats []int16
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
			s.apiServer.CancelSessionQuery(targetSessID)
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

	var startupPayload []byte
	startupLen := binary.BigEndian.Uint32(buf[:4])
	if startupLen > 8 {
		startupPayload = make([]byte, startupLen-8)
		if _, err := io.ReadFull(conn, startupPayload); err != nil {
			return
		}
	}

	startupParams := parseStartupParams(startupPayload)
	username := startupParams["user"]
	if username == "" {
		username = "postgres"
	}

	// 2. Authentication Handshake
	authSvc := s.apiServer.AuthService()
	needsPassword := false
	if authSvc != nil {
		if s.apiServer.IsAuthEnabled() {
			needsPassword = true
		} else if login, err := authSvc.GetLogin(username); err == nil && login != nil && login.PasswordHash != "" {
			needsPassword = true
		}
	}

	if needsPassword {
		// Request cleartext password from client
		if err := WriteAuthCleartextPassword(conn); err != nil {
			return
		}

		// Read client's PasswordMessage: 1 byte msgType ('p') + 4 bytes msgLen + password payload
		pwHeader := make([]byte, 5)
		if _, err := io.ReadFull(conn, pwHeader); err != nil {
			return
		}
		if pwHeader[0] != 'p' {
			_ = WriteErrorWithCode(conn, fmt.Sprintf("expected password response, got %q", string(pwHeader[0])), "08P01")
			return
		}
		pwLen := binary.BigEndian.Uint32(pwHeader[1:5])
		if pwLen < 4 {
			return
		}
		pwPayload := make([]byte, pwLen-4)
		if _, err := io.ReadFull(conn, pwPayload); err != nil {
			return
		}
		clientPassword := string(bytes.TrimRight(pwPayload, "\x00"))

		// Authenticate login
		if _, err := authSvc.AuthenticateLogin(username, clientPassword); err != nil {
			_ = WriteErrorWithCode(conn, fmt.Sprintf("password authentication failed for user %q", username), "28P01")
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

	// Send Auth OK, BackendKeyData & Server Parameters
	if err := WriteAuthOK(conn); err != nil {
		return
	}
	if err := WriteBackendKeyData(conn, pid, secretKey); err != nil {
		return
	}

	// Bind authenticated user to session
	s.apiServer.SetSessionUser(sessID, username)

	_ = WriteParameterStatus(conn, "server_version", "15.0 (SQLion)")
	_ = WriteParameterStatus(conn, "server_encoding", "UTF8")
	_ = WriteParameterStatus(conn, "client_encoding", "UTF8")
	_ = WriteParameterStatus(conn, "application_name", startupParams["application_name"])
	_ = WriteParameterStatus(conn, "is_superuser", "on")
	_ = WriteParameterStatus(conn, "session_authorization", username)
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
			_ = WriteReadyForQueryStatus(conn, s.apiServer.GetSessionTxStatus(sessID))

		case 'C': // Close
			s.handleClose(conn, payload, statements, portals)

		case 'H': // Flush
			// No-op

		case 'X': // Terminate
			return

		default:
			// Stay ready
			_ = WriteReadyForQueryStatus(conn, s.apiServer.GetSessionTxStatus(sessID))
		}
	}
}

func (s *Server) handleSimpleQuery(conn net.Conn, query string, sessID string) {
	if s.DevMode() {
		log.Printf("[DEV][PGWire][SimpleQuery] %s", strings.TrimSpace(query))
	}
	defer func() {
		_ = WriteReadyForQueryStatus(conn, s.apiServer.GetSessionTxStatus(sessID))
	}()
	s.runAndWriteQuery(conn, query, true, sessID)
}

var pgParamRe = regexp.MustCompile(`\$(\d+)`)

func (s *Server) compileQueryToProgram(query string) (*proc.Program, error) {
	translated := pgParamRe.ReplaceAllString(query, "@p$1")
	if cache := s.BytecodePlanCache(); cache != nil {
		return cache.GetOrCompile(translated)
	}
	return proc.Compile(translated)
}

func (s *Server) handleParse(conn net.Conn, payload []byte, statements map[string]*PreparedStatement) {
	r := bytes.NewReader(payload)
	stmtName, _ := readCString(r)
	queryStr, _ := readCString(r)
	if s.DevMode() {
		log.Printf("[DEV][PGWire][Parse] stmt=%q query=%s", stmtName, queryStr)
	}

	var numParamTypes int16
	_ = binary.Read(r, binary.BigEndian, &numParamTypes)

	paramTypes := make([]int32, numParamTypes)
	for i := 0; i < int(numParamTypes); i++ {
		_ = binary.Read(r, binary.BigEndian, &paramTypes[i])
	}

	// Infer parameter count if client passed 0 or fewer types than placeholders in queryStr
	matches := pgParamRe.FindAllStringSubmatch(queryStr, -1)
	maxIdx := 0
	for _, m := range matches {
		if len(m) == 2 {
			if idx, err := strconv.Atoi(m[1]); err == nil && idx > maxIdx {
				maxIdx = idx
			}
		}
	}
	if maxIdx > len(paramTypes) {
		expanded := make([]int32, maxIdx)
		copy(expanded, paramTypes)
		paramTypes = expanded
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
	if s.DevMode() {
		log.Printf("[DEV][PGWire][Bind] portal=%q stmt=%q", portalName, stmtName)
	}

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
			isBinary := false
			if len(formats) == 1 && formats[0] == 1 {
				isBinary = true
			} else if i < len(formats) && formats[i] == 1 {
				isBinary = true
			}
			if isBinary {
				var pType int32 = 0
				if i < len(stmt.ParamTypes) {
					pType = stmt.ParamTypes[i]
				}
				paramVals[i] = decodeBinaryParam(valBytes, pType)
			} else {
				paramVals[i] = string(valBytes)
			}
		}
	}

	var numResultFormats int16
	var resultFormats []int16
	if r.Len() >= 2 {
		_ = binary.Read(r, binary.BigEndian, &numResultFormats)
		if numResultFormats > 0 {
			resultFormats = make([]int16, numResultFormats)
			for i := 0; i < int(numResultFormats); i++ {
				_ = binary.Read(r, binary.BigEndian, &resultFormats[i])
			}
		}
	}

	portals[portalName] = &Portal{
		Name:          portalName,
		Statement:     stmt,
		Params:        paramVals,
		ResultFormats: resultFormats,
	}

	_ = WriteBindComplete(conn)
}

func decodeBinaryParam(b []byte, oid int32) string {
	switch oid {
	case 23: // INT4
		if len(b) >= 4 {
			return strconv.Itoa(int(int32(binary.BigEndian.Uint32(b))))
		}
	case 20: // INT8
		if len(b) >= 8 {
			return strconv.FormatInt(int64(binary.BigEndian.Uint64(b)), 10)
		}
	case 21: // INT2
		if len(b) >= 2 {
			return strconv.Itoa(int(int16(binary.BigEndian.Uint16(b))))
		}
	case 16: // BOOL
		if len(b) >= 1 {
			if b[0] != 0 {
				return "true"
			}
			return "false"
		}
	case 701: // FLOAT8
		if len(b) >= 8 {
			bits := binary.BigEndian.Uint64(b)
			return strconv.FormatFloat(math.Float64frombits(bits), 'f', -1, 64)
		}
	case 700: // FLOAT4
		if len(b) >= 4 {
			bits := binary.BigEndian.Uint32(b)
			return strconv.FormatFloat(float64(math.Float32frombits(bits)), 'f', -1, 32)
		}
	case 1114, 1184: // TIMESTAMP / TIMESTAMPTZ
		if len(b) >= 8 {
			micros := int64(binary.BigEndian.Uint64(b))
			pgEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			t := pgEpoch.Add(time.Duration(micros) * time.Microsecond)
			return t.Format("2006-01-02 15:04:05.999999")
		}
	case 1082: // DATE
		if len(b) >= 4 {
			days := int32(binary.BigEndian.Uint32(b))
			pgEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			t := pgEpoch.AddDate(0, 0, int(days))
			return t.Format("2006-01-02")
		}
	default:
		if len(b) == 4 {
			return strconv.Itoa(int(int32(binary.BigEndian.Uint32(b))))
		} else if len(b) == 8 {
			return strconv.FormatInt(int64(binary.BigEndian.Uint64(b)), 10)
		} else if len(b) == 2 {
			return strconv.Itoa(int(int16(binary.BigEndian.Uint16(b))))
		} else if len(b) == 1 {
			if b[0] == 0 {
				return "false"
			} else if b[0] == 1 {
				return "true"
			}
		}
	}
	return string(b)
}

func buildPreviewSQL(query string, paramTypes []int32) string {
	matches := pgParamRe.FindAllStringSubmatch(query, -1)
	if len(matches) == 0 {
		return query
	}
	res := query
	for _, m := range matches {
		if len(m) == 2 {
			idx, err := strconv.Atoi(m[1])
			if err == nil {
				paramOID := int32(0)
				if idx-1 < len(paramTypes) {
					paramOID = paramTypes[idx-1]
				}
				replacement := "1"
				switch paramOID {
				case 23, 20, 21:
					replacement = "1"
				case 701, 700, 1700:
					replacement = "1.0"
				case 16:
					replacement = "true"
				case 25, 1043:
					replacement = "'sample'"
				default:
					replacement = "1"
				}
				res = strings.ReplaceAll(res, m[0], replacement)
			}
		}
	}
	return res
}

func (s *Server) handleDescribe(conn net.Conn, payload []byte, statements map[string]*PreparedStatement, portals map[string]*Portal) {
	if len(payload) < 2 {
		_ = WriteNoData(conn)
		return
	}

	targetType := payload[0]
	targetName := string(bytes.TrimRight(payload[1:], "\x00"))
	if s.DevMode() {
		log.Printf("[DEV][PGWire][Describe] type=%c name=%q", targetType, targetName)
	}

	if targetType == 'S' {
		stmt, exists := statements[targetName]
		if !exists {
			_ = WriteError(conn, fmt.Sprintf("prepared statement %q does not exist", targetName))
			return
		}
		_ = WriteParameterDescription(conn, stmt.ParamTypes)
		s.writeSchemaDescription(conn, stmt.Query, stmt.ParamTypes)
	} else if targetType == 'P' {
		portal, exists := portals[targetName]
		if !exists {
			_ = WriteError(conn, fmt.Sprintf("portal %q does not exist", targetName))
			return
		}
		var paramTypes []int32
		if portal.Statement != nil {
			paramTypes = portal.Statement.ParamTypes
		}
		s.writeSchemaDescription(conn, portal.Statement.Query, paramTypes)
	} else {
		_ = WriteNoData(conn)
	}
}

func (s *Server) writeSchemaDescription(conn net.Conn, query string, paramTypes []int32) {
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
					selectPart := strings.TrimSpace(strings.TrimPrefix(parts[0], "SELECT"))
					if selectPart == "*" {
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

					rawCols := strings.Split(selectPart, ",")
					var cols []ColumnMeta
					for _, rawCol := range rawCols {
						colName := strings.TrimSpace(rawCol)
						if colName == "" {
							continue
						}
						if asIdx := strings.Index(strings.ToUpper(colName), " AS "); asIdx != -1 {
							colName = strings.TrimSpace(colName[asIdx+4:])
						}
						var colType storage.DataType = storage.TypeVarChar
						found := false
						for _, c := range tbl.Columns {
							if strings.EqualFold(c.Name, colName) {
								colType = c.Type
								colName = c.Name
								found = true
								break
							}
						}
						var oid int32 = 25
						if found {
							oid = typeToPgOID(colType)
						} else if strings.HasPrefix(strings.ToUpper(colName), "COUNT") {
							oid = 20 // int8
						}
						cols = append(cols, ColumnMeta{
							Name:     colName,
							DataType: oid,
						})
					}
					if len(cols) > 0 {
						_ = WriteRowDescription(conn, cols)
						return
					}
				}
			}
		}
	}

	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH") {
		if strings.Contains(upper, "VERSION()") {
			cols := []ColumnMeta{{Name: "version", DataType: 25}}
			_ = WriteRowDescription(conn, cols)
			return
		}
		previewSQL := buildPreviewSQL(query, paramTypes)
		if res, err := s.apiServer.ExecuteSessionSQL("default", previewSQL); err == nil && len(res.Columns) > 0 {
			var sampleRow map[string]any
			if len(res.Rows) > 0 {
				sampleRow = res.Rows[0]
			}
			colOIDs := s.resolveColumnOIDs(previewSQL, res.Columns, sampleRow)
			cols := make([]ColumnMeta, len(res.Columns))
			for i, c := range res.Columns {
				cols[i] = ColumnMeta{Name: c, DataType: colOIDs[i]}
			}
			_ = WriteRowDescription(conn, cols)
			return
		}
	}

	_ = WriteNoData(conn)
}

func (s *Server) handleExecute(conn net.Conn, payload []byte, portals map[string]*Portal, sessID string) {
	r := bytes.NewReader(payload)
	portalName, _ := readCString(r)
	if s.DevMode() {
		log.Printf("[DEV][PGWire][Execute] portal=%q", portalName)
	}

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
				s.writeExecutionResult(conn, res, portal.Statement.Query, portal.ResultFormats)
				return
			}
		}
	}

	// Fallback to text substitution
	substitutedSQL := substituteParams(portal.Statement.Query, portal.Params)
	s.runAndWriteQueryWithFormats(conn, substitutedSQL, false, sessID, portal.ResultFormats)
}

func (s *Server) writeExecutionResult(conn net.Conn, res *runtime.ExecutionResult, query string, resultFormats []int16) {
	if len(res.Columns) > 0 {
		var sampleRow map[string]any
		if len(res.Rows) > 0 {
			sampleRow = res.Rows[0]
		}
		colOIDs := s.resolveColumnOIDs(query, res.Columns, sampleRow)

		for _, row := range res.Rows {
			rowBytes := make([][]byte, len(res.Columns))
			for i, colName := range res.Columns {
				isBinary := false
				if len(resultFormats) == 1 && resultFormats[0] == 1 {
					isBinary = true
				} else if i < len(resultFormats) && resultFormats[i] == 1 {
					isBinary = true
				}
				val := getRowValue(row, colName)
				rowBytes[i] = formatColumnValue(val, colOIDs[i], isBinary)
			}
			_ = WriteDataRowBytes(conn, rowBytes)
		}
		_ = WriteCommandComplete(conn, fmt.Sprintf("SELECT %d", len(res.Rows)))
		return
	}
	rowsCount := int64(len(res.Rows))
	if rowsCount == 0 && res.RowsAffected > 0 {
		rowsCount = res.RowsAffected
	}
	_ = WriteCommandComplete(conn, formatPgCommandComplete(query, rowsCount, len(res.Rows)))
}

func formatPgCommandComplete(command string, rowsAffected int64, rowsCount int) string {
	upper := strings.ToUpper(strings.TrimSpace(command))
	fields := strings.Fields(upper)
	firstWord := ""
	if len(fields) > 0 {
		firstWord = fields[0]
	}

	switch firstWord {
	case "INSERT":
		return fmt.Sprintf("INSERT 0 %d", rowsAffected)
	case "DELETE":
		return fmt.Sprintf("DELETE %d", rowsAffected)
	case "UPDATE":
		return fmt.Sprintf("UPDATE %d", rowsAffected)
	case "SELECT":
		cnt := rowsCount
		if cnt == 0 && rowsAffected > 0 {
			cnt = int(rowsAffected)
		}
		return fmt.Sprintf("SELECT %d", cnt)
	case "MOVE":
		return fmt.Sprintf("MOVE %d", rowsAffected)
	case "FETCH":
		return fmt.Sprintf("FETCH %d", rowsAffected)
	case "COPY":
		return fmt.Sprintf("COPY %d", rowsAffected)
	case "BEGIN", "START":
		return "BEGIN"
	case "COMMIT":
		return "COMMIT"
	case "ROLLBACK":
		return "ROLLBACK"
	case "SET":
		return "SET"
	case "SHOW":
		return "SHOW"
	case "CREATE":
		if len(fields) > 1 {
			return fmt.Sprintf("CREATE %s", fields[1])
		}
		return "CREATE"
	case "DROP":
		if len(fields) > 1 {
			return fmt.Sprintf("DROP %s", fields[1])
		}
		return "DROP"
	case "ALTER":
		if len(fields) > 1 {
			return fmt.Sprintf("ALTER %s", fields[1])
		}
		return "ALTER"
	default:
		if firstWord != "" {
			return firstWord
		}
		return "OK"
	}
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
	s.runAndWriteQueryWithFormats(conn, query, writeRowDesc, sessID, nil)
}

var blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

func stripSQLComments(query string) string {
	noBlock := blockCommentRe.ReplaceAllString(query, "")
	lines := strings.Split(noBlock, "\n")
	var sb strings.Builder
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "--") {
			continue
		}
		if idx := strings.Index(line, "--"); idx != -1 {
			line = line[:idx]
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return strings.TrimSpace(sb.String())
}

func (s *Server) runAndWriteQueryWithFormats(conn net.Conn, query string, writeRowDesc bool, sessID string, resultFormats []int16) {
	cleaned := stripSQLComments(query)
	if cleaned == "" || cleaned == ";" {
		if s.DevMode() {
			log.Printf("[DEV][PGWire][EmptyQuery] responding with EmptyQueryResponse ('I')")
		}
		_ = WriteEmptyQueryResponse(conn)
		return
	}
	trimmed := cleaned
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
		if s.DevMode() {
			log.Printf("[DEV][PGWire][Error] query=%s err=%v", trimmed, err)
		}
		_ = WriteError(conn, err.Error())
		return
	}
	if s.DevMode() {
		log.Printf("[DEV][PGWire][Success] query=%s cols=%v rows=%d duration=%dµs", trimmed, res.Columns, len(res.Rows), res.DurationUS)
	}

	if len(res.Columns) > 0 {
		var sampleRow map[string]any
		if len(res.Rows) > 0 {
			sampleRow = res.Rows[0]
		}
		colOIDs := s.resolveColumnOIDs(query, res.Columns, sampleRow)

		if writeRowDesc {
			cols := make([]ColumnMeta, len(res.Columns))
			for i, c := range res.Columns {
				cols[i] = ColumnMeta{Name: c, DataType: colOIDs[i]}
			}
			_ = WriteRowDescription(conn, cols)
		}

		for _, row := range res.Rows {
			rowBytes := make([][]byte, len(res.Columns))
			for i, colName := range res.Columns {
				isBinary := false
				if len(resultFormats) == 1 && resultFormats[0] == 1 {
					isBinary = true
				} else if i < len(resultFormats) && resultFormats[i] == 1 {
					isBinary = true
				}
				val := getRowValue(row, colName)
				rowBytes[i] = formatColumnValue(val, colOIDs[i], isBinary)
			}
			_ = WriteDataRowBytes(conn, rowBytes)
		}
		_ = WriteCommandComplete(conn, fmt.Sprintf("SELECT %d", len(res.Rows)))
		return
	}

	cmd := res.Command
	if cmd == "" {
		cmd = trimmed
	}
	_ = WriteCommandComplete(conn, formatPgCommandComplete(cmd, res.RowsAffected, len(res.Rows)))
}

func (s *Server) resolveColumnOIDs(query string, colNames []string, sampleRow map[string]any) []int32 {
	oids := make([]int32, len(colNames))
	upper := strings.ToUpper(strings.TrimSpace(query))
	var tbl *storage.TableSchema
	if strings.Contains(upper, "SELECT") && strings.Contains(upper, "FROM") {
		parts := strings.Split(upper, "FROM")
		if len(parts) >= 2 {
			tblField := strings.Fields(parts[1])
			if len(tblField) > 0 {
				tblName := strings.Trim(tblField[0], ";, ")
				t, err := s.engine.GetTable(tblName)
				if err != nil {
					t, err = s.engine.GetTable(strings.ToLower(tblName))
				}
				if err == nil {
					tbl = t
				}
			}
		}
	}

	for i, colName := range colNames {
		var oid int32 = 0
		if tbl != nil {
			for _, c := range tbl.Columns {
				if strings.EqualFold(c.Name, colName) {
					oid = typeToPgOID(c.Type)
					break
				}
			}
		}
		if oid == 0 {
			upperCol := strings.ToUpper(colName)
			if strings.HasPrefix(upperCol, "COUNT") {
				oid = 20 // int8
			} else if sampleRow != nil {
				val := getRowValue(sampleRow, colName)
				if val != nil {
					oid = inferPgOID(val)
				}
			}
			if oid == 0 {
				oid = 25 // text
			}
		}
		oids[i] = oid
	}
	return oids
}

func getRowValue(row map[string]any, colName string) any {
	if val, ok := row[colName]; ok {
		return val
	}
	for k, v := range row {
		if strings.EqualFold(k, colName) {
			return v
		}
	}
	return nil
}

func formatColumnValue(val any, oid int32, isBinary bool) []byte {
	if val == nil {
		return nil
	}
	if !isBinary {
		return []byte(fmt.Sprintf("%v", val))
	}
	switch oid {
	case 23: // INT4
		var n int32
		switch v := val.(type) {
		case int:
			n = int32(v)
		case int32:
			n = v
		case int64:
			n = int32(v)
		default:
			if i, err := strconv.ParseInt(fmt.Sprintf("%v", val), 10, 32); err == nil {
				n = int32(i)
			}
		}
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(n))
		return b
	case 20: // INT8
		var n int64
		switch v := val.(type) {
		case int:
			n = int64(v)
		case int64:
			n = v
		default:
			if i, err := strconv.ParseInt(fmt.Sprintf("%v", val), 10, 64); err == nil {
				n = i
			}
		}
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(n))
		return b
	case 21: // INT2
		var n int16
		switch v := val.(type) {
		case int:
			n = int16(v)
		case int16:
			n = v
		default:
			if i, err := strconv.ParseInt(fmt.Sprintf("%v", val), 10, 16); err == nil {
				n = int16(i)
			}
		}
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, uint16(n))
		return b
	case 17: // BYTEA
		if b, ok := val.([]byte); ok {
			return b
		}
		return []byte(fmt.Sprintf("%v", val))
	case 16: // BOOL
		b := byte(0)
		switch v := val.(type) {
		case bool:
			if v {
				b = 1
			}
		default:
			str := strings.ToLower(fmt.Sprintf("%v", val))
			if str == "true" || str == "1" || str == "t" {
				b = 1
			}
		}
		return []byte{b}
	case 701: // FLOAT8
		var f float64
		switch v := val.(type) {
		case float64:
			f = v
		case float32:
			f = float64(v)
		case int:
			f = float64(v)
		case int64:
			f = float64(v)
		default:
			if parsed, err := strconv.ParseFloat(fmt.Sprintf("%v", val), 64); err == nil {
				f = parsed
			}
		}
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, math.Float64bits(f))
		return b
	case 700: // FLOAT4
		var f float32
		switch v := val.(type) {
		case float32:
			f = v
		case float64:
			f = float32(v)
		case int:
			f = float32(v)
		default:
			if parsed, err := strconv.ParseFloat(fmt.Sprintf("%v", val), 32); err == nil {
				f = float32(parsed)
			}
		}
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, math.Float32bits(f))
		return b
	case 1114, 1184: // TIMESTAMP / TIMESTAMPTZ
		var t time.Time
		switch v := val.(type) {
		case time.Time:
			t = v
		default:
			parsed, err := time.Parse(time.RFC3339, fmt.Sprintf("%v", val))
			if err != nil {
				parsed, _ = time.Parse("2006-01-02 15:04:05", fmt.Sprintf("%v", val))
			}
			t = parsed
		}
		pgEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		micros := t.UTC().Sub(pgEpoch).Microseconds()
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(micros))
		return b
	case 1082: // DATE
		var t time.Time
		switch v := val.(type) {
		case time.Time:
			t = v
		default:
			parsed, _ := time.Parse("2006-01-02", fmt.Sprintf("%v", val))
			t = parsed
		}
		pgEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		days := int32(t.UTC().Sub(pgEpoch).Hours() / 24)
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(days))
		return b
	case 25, 1043: // TEXT, VARCHAR
		return []byte(fmt.Sprintf("%v", val))
	default:
		return []byte(fmt.Sprintf("%v", val))
	}
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
	case storage.TypeDecimal:
		return 1700 // NUMERIC
	case storage.TypeBool:
		return 16 // BOOL
	case storage.TypeDateTime:
		return 1114 // TIMESTAMP
	case storage.TypeDate:
		return 1082 // DATE
	case storage.TypeTime:
		return 1083 // TIME
	case storage.TypeUUID:
		return 2950 // UUID
	case storage.TypeBinary:
		return 17 // BYTEA
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

func inferPgOID(v any) int32 {
	if v == nil {
		return 25 // text
	}
	switch v := v.(type) {
	case int, int32:
		return 23 // int4
	case int64:
		if v >= -2147483648 && v <= 2147483647 {
			return 23 // int4
		}
		return 20 // int8
	case float32, float64:
		return 701 // float8
	case bool:
		return 16 // bool
	case time.Time:
		return 1114 // timestamp
	case []byte:
		return 17 // bytea
	default:
		return 25 // text
	}
}

func parseStartupParams(payload []byte) map[string]string {
	params := make(map[string]string)
	if len(payload) == 0 {
		return params
	}
	r := bytes.NewReader(payload)
	for {
		key, err := readCString(r)
		if err != nil || key == "" {
			break
		}
		val, err := readCString(r)
		if err != nil {
			break
		}
		params[key] = val
	}
	return params
}
