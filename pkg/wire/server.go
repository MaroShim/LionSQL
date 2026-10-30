package wire

import (
	"bufio"
	"bytes"
	"container/list"
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

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

type pgActiveConn struct {
	sessID    string
	secretKey uint32
}

var bufioWriterPool = sync.Pool{
	New: func() any {
		return bufio.NewWriterSize(nil, 32768)
	},
}

func getBufioWriter(w io.Writer) *bufio.Writer {
	bw := bufioWriterPool.Get().(*bufio.Writer)
	bw.Reset(w)
	return bw
}

func putBufioWriter(bw *bufio.Writer) {
	bw.Reset(nil)
	bufioWriterPool.Put(bw)
}

var select1SimpleQueryResponse = func() []byte {
	var buf bytes.Buffer
	cols := []ColumnMeta{{Name: "?column?", DataType: 23}}
	_ = WriteRowDescription(&buf, cols)
	_ = WriteDataRow(&buf, []string{"1"})
	_ = WriteCommandComplete(&buf, "SELECT 1")
	return buf.Bytes()
}()

func isSelectOne(upper string) bool {
	fields := strings.Fields(upper)
	return len(fields) == 2 && fields[0] == "SELECT" && fields[1] == "1"
}

// Server implements a PostgreSQL Wire Protocol v3 listener.
type Server struct {
	addr        string
	listener    net.Listener
	engine      storage.Engine
	catalog     *catalog.Catalog
	coordinator engine.SQLCoordinator
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
	clientConns           map[net.Conn]struct{}
	maxPreparedPerSession int
	wg                    sync.WaitGroup
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
		coordinator: engine.NewCoordinator(cat, eng, engine.DefaultConfig()),
		planCache:   proc.NewPlanCache(),
		byteCache:   cache,
		executor:    executor,
		activeConns: make(map[uint32]*pgActiveConn),
	}
}

// NewServerWithCoordinator creates a PostgreSQL wire protocol server with an explicit SQLCoordinator.
func NewServerWithCoordinator(addr string, cat *catalog.Catalog, eng storage.Engine, cache *runtime.PlanCache, coord engine.SQLCoordinator) *Server {
	s := NewServerWithCache(addr, cat, eng, cache)
	if coord != nil {
		s.coordinator = coord
	}
	return s
}

// SetCoordinator configures or overrides the SQL coordinator for query execution.
func (s *Server) SetCoordinator(coord engine.SQLCoordinator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.coordinator = coord
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

// SetMaxPreparedPerSession configures the per-session LRU prepared statement cache capacity.
func (s *Server) SetMaxPreparedPerSession(max int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxPreparedPerSession = max
}

func (s *Server) getMaxPreparedPerSession() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxPreparedPerSession <= 0 {
		return DefaultMaxPreparedPerSession
	}
	return s.maxPreparedPerSession
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

	s.wg.Add(1)
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

// Close gracefully terminates the listener and active connections within a 1-second bounded timeout.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	for c := range s.clientConns {
		_ = c.Close()
	}
	s.mu.Unlock()

	waitCh := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
	case <-time.After(1 * time.Second):
	}

	return err
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
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

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		if s.clientConns == nil {
			s.clientConns = make(map[net.Conn]struct{})
		}
		s.clientConns[conn] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go func(c net.Conn) {
			defer func() {
				s.mu.Lock()
				delete(s.clientConns, c)
				s.mu.Unlock()
				s.wg.Done()
			}()
			s.handleConnection(c)
		}(conn)
	}
}

type PreparedStatement struct {
	Name        string
	Query       string
	ParamTypes  []int32
	Program     *proc.Program
	ColumnNames []string
	ColumnOIDs  []int32
}

type Portal struct {
	Name          string
	Statement     *PreparedStatement
	Params        []string
	ParamValues   []storage.Value
	ResultFormats []int16
	// Stateful execution for server-side portal cursor
	Result   *engine.QueryResult
	Pos      int
	Executed bool
}

// DefaultMaxPreparedPerSession is the maximum number of prepared statements retained per connection session.
const DefaultMaxPreparedPerSession = 256

type pgPreparedEntry struct {
	name string
	stmt *PreparedStatement
}

// PreparedStatementCache implements a thread-safe bounded LRU cache for prepared statements in a PGWire session (Golden Rule 23).
type PreparedStatementCache struct {
	mu       sync.Mutex
	capacity int
	unnamed  *PreparedStatement
	items    map[string]*list.Element
	lruList  *list.List
}

// NewPreparedStatementCache creates a new bounded LRU cache for prepared statements.
func NewPreparedStatementCache(capacity int) *PreparedStatementCache {
	if capacity <= 0 {
		capacity = DefaultMaxPreparedPerSession
	}
	return &PreparedStatementCache{
		capacity: capacity,
		items:    make(map[string]*list.Element),
		lruList:  list.New(),
	}
}

// Put inserts or updates a prepared statement in the cache, evicting the least recently used statement if capacity is reached.
func (c *PreparedStatementCache) Put(name string, stmt *PreparedStatement) {
	if stmt == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if name == "" {
		c.unnamed = stmt
		return
	}

	if elem, exists := c.items[name]; exists {
		elem.Value.(*pgPreparedEntry).stmt = stmt
		c.lruList.MoveToFront(elem)
		return
	}

	if c.capacity > 0 && c.lruList.Len() >= c.capacity {
		back := c.lruList.Back()
		if back != nil {
			c.lruList.Remove(back)
			old := back.Value.(*pgPreparedEntry)
			delete(c.items, old.name)
		}
	}

	elem := c.lruList.PushFront(&pgPreparedEntry{
		name: name,
		stmt: stmt,
	})
	c.items[name] = elem
}

// Get retrieves a prepared statement by name and marks it as most recently used.
func (c *PreparedStatementCache) Get(name string) (*PreparedStatement, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if name == "" {
		if c.unnamed != nil {
			return c.unnamed, true
		}
		return nil, false
	}

	elem, exists := c.items[name]
	if !exists {
		return nil, false
	}
	c.lruList.MoveToFront(elem)
	return elem.Value.(*pgPreparedEntry).stmt, true
}

// Delete removes a prepared statement by name from the cache.
func (c *PreparedStatementCache) Delete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if name == "" {
		c.unnamed = nil
		return
	}

	if elem, exists := c.items[name]; exists {
		c.lruList.Remove(elem)
		delete(c.items, name)
	}
}

// Len returns the current number of cached prepared statements.
func (c *PreparedStatementCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.lruList.Len()
	if c.unnamed != nil {
		n++
	}
	return n
}

// Capacity returns the maximum capacity of the cache.
func (c *PreparedStatementCache) Capacity() int {
	return c.capacity
}

var (
	staticParamP      [64]string
	staticParamAt     [64]string
	staticParamDollar [64]string
)

func init() {
	for i := 0; i < 64; i++ {
		pIdx := i + 1
		staticParamP[i] = fmt.Sprintf("p%d", pIdx)
		staticParamAt[i] = fmt.Sprintf("@p%d", pIdx)
		staticParamDollar[i] = fmt.Sprintf("$%d", pIdx)
	}
}

func getParamNames(pIdx int) (string, string, string) {
	if pIdx >= 1 && pIdx <= 64 {
		return staticParamP[pIdx-1], staticParamAt[pIdx-1], staticParamDollar[pIdx-1]
	}
	return fmt.Sprintf("p%d", pIdx), fmt.Sprintf("@p%d", pIdx), fmt.Sprintf("$%d", pIdx)
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	connID := atomic.AddUint64(&s.connCounter, 1)
	sessID := fmt.Sprintf("pg-%d", connID)
	defer s.coordinator.CloseSession(sessID)

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
			s.coordinator.CancelSessionQuery(targetSessID)
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
				s.coordinator.CloseSession(targetSessID)
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
	authSvc := s.coordinator.AuthService()
	needsPassword := false
	if authSvc != nil {
		if s.coordinator.IsAuthEnabled() {
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
	s.coordinator.SetSessionUser(sessID, username)

	_ = WriteParameterStatus(conn, "server_version", "15.0 (LionSQL)")
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

	statements := NewPreparedStatementCache(s.getMaxPreparedPerSession())
	portals := make(map[string]*Portal)

	// 3. Message Loop
	msgHeader := make([]byte, 5)
	for {
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
			_ = WriteReadyForQueryStatus(conn, s.coordinator.GetSessionTxStatus(sessID))

		case 'C': // Close
			s.handleClose(conn, payload, statements, portals)

		case 'H': // Flush
			// No-op

		case 'X': // Terminate
			return

		default:
			// Stay ready
			_ = WriteReadyForQueryStatus(conn, s.coordinator.GetSessionTxStatus(sessID))
		}
	}
}

func (s *Server) handleSimpleQuery(conn net.Conn, query string, sessID string) {
	if s.DevMode() {
		log.Printf("[DEV][PGWire][SimpleQuery] %s", strings.TrimSpace(query))
	}
	defer func() {
		_ = WriteReadyForQueryStatus(conn, s.coordinator.GetSessionTxStatus(sessID))
	}()

	cleaned := stripSQLComments(query)
	trimmed := strings.TrimRight(strings.TrimSpace(cleaned), ";")
	upper := strings.ToUpper(strings.TrimSpace(trimmed))

	// Fast-Path for simple transaction control commands
	switch upper {
	case "BEGIN", "START TRANSACTION", "BEGIN WORK", "BEGIN TRANSACTION":
		if err := s.coordinator.BeginSessionTx(sessID, storage.TxOptions{}); err != nil {
			_ = WriteError(conn, err.Error())
		} else {
			_ = WriteCommandComplete(conn, "BEGIN")
		}
		return

	case "COMMIT", "END", "COMMIT TRANSACTION", "COMMIT WORK":
		if err := s.coordinator.CommitSessionTx(sessID); err != nil {
			_ = WriteError(conn, err.Error())
		} else {
			_ = WriteCommandComplete(conn, "COMMIT")
		}
		return

	case "ROLLBACK", "ABORT", "ROLLBACK TRANSACTION", "ROLLBACK WORK":
		if err := s.coordinator.RollbackSessionTx(sessID); err != nil {
			_ = WriteError(conn, err.Error())
		} else {
			_ = WriteCommandComplete(conn, "ROLLBACK")
		}
		return
	}

	// Fast-Path for connection-pool health-checks (SELECT 1 / SELECT 1;)
	if isSelectOne(upper) {
		if s.coordinator.GetSessionTxStatus(sessID) == 'E' {
			_ = WriteError(conn, "current transaction is aborted, commands ignored until end of transaction block")
			return
		}
		_, _ = conn.Write(select1SimpleQueryResponse)
		return
	}

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

func (s *Server) handleParse(conn net.Conn, payload []byte, statements *PreparedStatementCache) {
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

	statements.Put(stmtName, prep)

	_ = WriteParseComplete(conn)
}

func (s *Server) handleBind(conn net.Conn, payload []byte, statements *PreparedStatementCache, portals map[string]*Portal) {
	r := bytes.NewReader(payload)
	portalName, _ := readCString(r)
	stmtName, _ := readCString(r)
	if s.DevMode() {
		log.Printf("[DEV][PGWire][Bind] portal=%q stmt=%q", portalName, stmtName)
	}

	stmt, exists := statements.Get(stmtName)
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
	paramStorageVals := make([]storage.Value, numParams)

	for i := 0; i < int(numParams); i++ {
		var paramLen int32
		_ = binary.Read(r, binary.BigEndian, &paramLen)
		if paramLen == -1 {
			paramVals[i] = "NULL"
			paramStorageVals[i] = storage.NullValue(storage.TypeUnknown)
		} else {
			valBytes := make([]byte, paramLen)
			_, _ = io.ReadFull(r, valBytes)
			isBinary := false
			if len(formats) == 1 && formats[0] == 1 {
				isBinary = true
			} else if i < len(formats) && formats[i] == 1 {
				isBinary = true
			}
			var pType int32 = 0
			if i < len(stmt.ParamTypes) {
				pType = stmt.ParamTypes[i]
			}
			if isBinary {
				stVal := decodeBinaryParamValue(valBytes, pType)
				paramStorageVals[i] = stVal
				paramVals[i] = formatParamString(stVal)
			} else {
				strVal := string(valBytes)
				paramVals[i] = strVal
				paramStorageVals[i] = decodeTextParamValue(strVal, pType)
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
		ParamValues:   paramStorageVals,
		ResultFormats: resultFormats,
	}

	_ = WriteBindComplete(conn)
}

func decodeBinaryParamValue(b []byte, oid int32) storage.Value {
	switch oid {
	case 23: // INT4
		if len(b) >= 4 {
			return storage.IntValue(int64(int32(binary.BigEndian.Uint32(b))))
		}
	case 20: // INT8
		if len(b) >= 8 {
			return storage.IntValue(int64(binary.BigEndian.Uint64(b)))
		}
	case 21: // INT2
		if len(b) >= 2 {
			return storage.IntValue(int64(int16(binary.BigEndian.Uint16(b))))
		}
	case 16: // BOOL
		if len(b) >= 1 {
			return storage.BoolValue(b[0] != 0)
		}
	case 701: // FLOAT8
		if len(b) >= 8 {
			bits := binary.BigEndian.Uint64(b)
			return storage.FloatValue(math.Float64frombits(bits))
		}
	case 700: // FLOAT4
		if len(b) >= 4 {
			bits := binary.BigEndian.Uint32(b)
			return storage.FloatValue(float64(math.Float32frombits(bits)))
		}
	case 1114, 1184: // TIMESTAMP / TIMESTAMPTZ
		if len(b) >= 8 {
			micros := int64(binary.BigEndian.Uint64(b))
			pgEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			t := pgEpoch.Add(time.Duration(micros) * time.Microsecond)
			return storage.DateTimeValue(t)
		}
	case 1082: // DATE
		if len(b) >= 4 {
			days := int32(binary.BigEndian.Uint32(b))
			pgEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			t := pgEpoch.AddDate(0, 0, int(days))
			return storage.DateValue(t)
		}
	case 17: // BYTEA
		return storage.BinaryValue(b)
	case 25, 1043: // TEXT, VARCHAR
		return storage.StringValue(string(b))
	case 2950: // UUID
		if len(b) == 16 {
			return storage.UUIDValue(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
		}
	default:
		if len(b) == 4 {
			return storage.IntValue(int64(int32(binary.BigEndian.Uint32(b))))
		} else if len(b) == 8 {
			return storage.IntValue(int64(binary.BigEndian.Uint64(b)))
		} else if len(b) == 2 {
			return storage.IntValue(int64(int16(binary.BigEndian.Uint16(b))))
		} else if len(b) == 1 {
			return storage.BoolValue(b[0] != 0)
		}
	}
	return storage.StringValue(string(b))
}

func decodeTextParamValue(s string, oid int32) storage.Value {
	if strings.EqualFold(s, "null") {
		return storage.NullValue(storage.TypeUnknown)
	}
	switch oid {
	case 23, 20, 21: // INT4, INT8, INT2
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(i)
		}
	case 700, 701: // FLOAT4, FLOAT8
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case 16: // BOOL
		if strings.EqualFold(s, "true") || s == "t" || s == "1" {
			return storage.BoolValue(true)
		} else if strings.EqualFold(s, "false") || s == "f" || s == "0" {
			return storage.BoolValue(false)
		}
	case 0: // Untyped parameter (lib/pq default)
		trimmed := strings.TrimSpace(s)
		if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return storage.IntValue(i)
		}
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return storage.FloatValue(f)
		}
		if strings.EqualFold(trimmed, "true") {
			return storage.BoolValue(true)
		} else if strings.EqualFold(trimmed, "false") {
			return storage.BoolValue(false)
		}
	}
	return storage.StringValue(s)
}

func formatParamString(val storage.Value) string {
	if val.IsNull {
		return "NULL"
	}
	switch val.Type {
	case storage.TypeInt:
		return strconv.FormatInt(val.IVal, 10)
	case storage.TypeFloat:
		return strconv.FormatFloat(val.FVal, 'f', -1, 64)
	case storage.TypeBool:
		if val.BVal {
			return "true"
		}
		return "false"
	case storage.TypeDateTime:
		return val.TVal.Format("2006-01-02 15:04:05.999999")
	case storage.TypeDate:
		return val.TVal.Format("2006-01-02")
	default:
		return val.String()
	}
}

func decodeBinaryParam(b []byte, oid int32) string {
	val := decodeBinaryParamValue(b, oid)
	return formatParamString(val)
}

func buildPreviewSQL(query string, paramTypes []int32) string {
	return pgParamRe.ReplaceAllStringFunc(query, func(match string) string {
		sub := pgParamRe.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		idx, err := strconv.Atoi(sub[1])
		if err != nil {
			return match
		}
		paramOID := int32(0)
		if idx-1 >= 0 && idx-1 < len(paramTypes) {
			paramOID = paramTypes[idx-1]
		}
		switch paramOID {
		case 23, 20, 21:
			return "1"
		case 701, 700, 1700:
			return "1.0"
		case 16:
			return "true"
		case 25, 1043:
			return "'sample'"
		default:
			if strings.ContainsAny(query, "+-*/") {
				return "1"
			}
			return "'sample'"
		}
	})
}

func (s *Server) handleDescribe(conn net.Conn, payload []byte, statements *PreparedStatementCache, portals map[string]*Portal) {
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
		stmt, exists := statements.Get(targetName)
		if !exists {
			_ = WriteError(conn, fmt.Sprintf("prepared statement %q does not exist", targetName))
			return
		}
		_ = WriteParameterDescription(conn, stmt.ParamTypes)
		s.writeSchemaDescription(conn, stmt.Query, stmt.ParamTypes, stmt)
	} else if targetType == 'P' {
		portal, exists := portals[targetName]
		if !exists {
			_ = WriteError(conn, fmt.Sprintf("portal %q does not exist", targetName))
			return
		}
		var paramTypes []int32
		var stmt *PreparedStatement
		if portal.Statement != nil {
			paramTypes = portal.Statement.ParamTypes
			stmt = portal.Statement
		}
		s.writeSchemaDescription(conn, portal.Statement.Query, paramTypes, stmt)
	} else {
		_ = WriteNoData(conn)
	}
}

func (s *Server) writeSchemaDescription(conn net.Conn, query string, paramTypes []int32, stmt *PreparedStatement) {
	recordCols := func(cols []ColumnMeta) {
		if stmt != nil && len(cols) > 0 {
			stmt.ColumnNames = make([]string, len(cols))
			stmt.ColumnOIDs = make([]int32, len(cols))
			for i, c := range cols {
				stmt.ColumnNames[i] = c.Name
				stmt.ColumnOIDs[i] = c.DataType
			}
		}
	}

	if stmt != nil && len(stmt.ColumnOIDs) > 0 {
		cols := make([]ColumnMeta, len(stmt.ColumnOIDs))
		for i, oid := range stmt.ColumnOIDs {
			name := "column"
			if i < len(stmt.ColumnNames) {
				name = stmt.ColumnNames[i]
			}
			cols[i] = ColumnMeta{Name: name, DataType: oid}
		}
		_ = WriteRowDescription(conn, cols)
		return
	}

	if retIdx := sqlutil.FindTopLevelKeyword(query, "RETURNING"); retIdx != -1 {
		retPart := strings.TrimSpace(query[retIdx+len("RETURNING"):])
		if dml, err := engine.ParseDMLCommand(query); err == nil && dml != nil {
			cleanTbl := cleanColumnIdentifier(dml.Table)
			tbl, err := s.engine.GetTable(cleanTbl)
			if err != nil {
				tables, _ := s.engine.ListTables()
				for _, t := range tables {
					if strings.EqualFold(t.Name, cleanTbl) {
						tbl = t
						break
					}
				}
			}
			if tbl != nil {
				rawCols := sqlutil.SplitTopLevel(retPart, ',')
				var cols []ColumnMeta
				for _, rawCol := range rawCols {
					colName := strings.TrimSpace(rawCol)
					if colName == "" {
						continue
					}
					var outColName string
					lookupName := colName
					if expr, alias, hasAs := sqlutil.SplitAsAlias(colName); hasAs {
						outColName = cleanColumnIdentifier(alias)
						lookupName = cleanColumnIdentifier(expr)
					} else {
						outColName = cleanColumnIdentifier(colName)
						lookupName = outColName
					}

					var colType storage.DataType = storage.TypeInt
					found := false
					for _, c := range tbl.Columns {
						if strings.EqualFold(c.Name, lookupName) {
							colType = c.Type
							found = true
							break
						}
					}
					var oid int32 = 20
					if found {
						oid = typeToPgOID(colType)
					}
					cols = append(cols, ColumnMeta{
						Name:     outColName,
						DataType: oid,
					})
				}
				if len(cols) > 0 {
					recordCols(cols)
					_ = WriteRowDescription(conn, cols)
					return
				}
			}
		}
	}

	upper := strings.ToUpper(strings.TrimSpace(query))
	fromIdx := sqlutil.FindTopLevelKeyword(query, "FROM")
	if fromIdx != -1 {
		selectPart := strings.TrimSpace(query[:fromIdx])
		if strings.HasPrefix(strings.ToUpper(selectPart), "SELECT") {
			selectPart = strings.TrimSpace(selectPart[6:])
		}
		afterFrom := query[fromIdx+4:]
		tblField := sqlutil.SplitTopLevelTokens(afterFrom)
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
				if selectPart == "*" {
					cols := make([]ColumnMeta, len(tbl.Columns))
					for i, c := range tbl.Columns {
						cols[i] = ColumnMeta{
							Name:     c.Name,
							DataType: typeToPgOID(c.Type),
						}
					}
					recordCols(cols)
					_ = WriteRowDescription(conn, cols)
					return
				}

				rawCols := sqlutil.SplitTopLevel(selectPart, ',')
				var cols []ColumnMeta
				for _, rawCol := range rawCols {
					colName := strings.TrimSpace(rawCol)
					if colName == "" {
						continue
					}
					var outColName string
					lookupName := colName
					if expr, alias, hasAs := sqlutil.SplitAsAlias(colName); hasAs {
						outColName = cleanColumnIdentifier(alias)
						lookupName = cleanColumnIdentifier(expr)
					} else {
						outColName = cleanColumnIdentifier(colName)
						lookupName = outColName
					}

					var colType storage.DataType = storage.TypeVarChar
					found := false
					for _, c := range tbl.Columns {
						if strings.EqualFold(c.Name, lookupName) || strings.EqualFold(c.Name, colName) {
							colType = c.Type
							found = true
							break
						}
					}
					var oid int32 = 25
					if found {
						oid = typeToPgOID(colType)
					} else if strings.HasPrefix(strings.ToUpper(lookupName), "COUNT") {
						oid = 20 // int8
					}
					cols = append(cols, ColumnMeta{
						Name:     outColName,
						DataType: oid,
					})
				}
					if len(cols) > 0 {
						recordCols(cols)
						_ = WriteRowDescription(conn, cols)
						return
					}
				}
			}
		}

	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH") {
		if strings.Contains(upper, "VERSION()") {
			cols := []ColumnMeta{{Name: "version", DataType: 25}}
			recordCols(cols)
			_ = WriteRowDescription(conn, cols)
			return
		}
		previewSQL := buildPreviewSQL(query, paramTypes)
		if res, err := s.coordinator.ExecuteSessionSQL("default", previewSQL); err == nil && len(res.Columns) > 0 {
			var sampleRow map[string]any
			if len(res.Rows) > 0 {
				sampleRow = res.Rows[0]
			}
			colOIDs := s.resolveColumnOIDs(previewSQL, res.Columns, sampleRow)
			cols := make([]ColumnMeta, len(res.Columns))
			for i, c := range res.Columns {
				cols[i] = ColumnMeta{Name: c, DataType: colOIDs[i]}
			}
			recordCols(cols)
			_ = WriteRowDescription(conn, cols)
			return
		}
	}

	if strings.HasPrefix(upper, "EXEC") || strings.HasPrefix(upper, "CALL") {
		procName, _ := engine.ParseProcArgs(query)
		if procName != "" {
			var prog *proc.Program
			if s.byteCache != nil {
				if p, found := s.byteCache.GetByName(procName); found && p != nil {
					prog = p
				} else if s.catalog != nil {
					if procMeta, err := s.catalog.GetProcedure(procName); err == nil && procMeta != nil {
						if compiled, err := s.byteCache.GetOrCompile(procMeta.Body); err == nil && compiled != nil {
							s.byteCache.PutByName(procName, compiled)
							prog = compiled
						}
					}
				}
			}

			if prog != nil {
				if cols := prog.ResultColumns(); len(cols) > 0 {
					querySQL := ""
					if s.catalog != nil {
						if procMeta, err := s.catalog.GetProcedure(procName); err == nil && procMeta != nil {
							querySQL = procMeta.Body
						}
					}
					colOIDs := s.resolveColumnOIDs(querySQL, cols, nil)
					colMetas := make([]ColumnMeta, len(cols))
					for i, c := range cols {
						oid := colOIDs[i]
						if oid == 0 {
							oid = 25
						}
						colMetas[i] = ColumnMeta{Name: c, DataType: oid}
					}
					_ = WriteRowDescription(conn, colMetas)
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
	var maxRows int32
	_ = binary.Read(r, binary.BigEndian, &maxRows)

	if s.DevMode() {
		log.Printf("[DEV][PGWire][Execute] portal=%q maxRows=%d", portalName, maxRows)
	}

	portal, exists := portals[portalName]
	if !exists {
		_ = WriteError(conn, fmt.Sprintf("portal %q does not exist", portalName))
		return
	}

	if sessID == "" {
		sessID = "default"
	}

	// 1. If not yet executed, execute the query and cache the result in the portal
	if !portal.Executed {
		// Fast-path: Execute via precompiled VDBE bytecode Program if available and full fetch requested
		if portal.Statement != nil && portal.Statement.Program != nil && maxRows == 0 {
			paramMap := make(map[string]any, len(portal.Params)*3)
			for i, paramVal := range portal.Params {
				pIdx := i + 1
				pName, atName, dollarName := getParamNames(pIdx)
				var anyVal any = paramVal
				if i < len(portal.ParamValues) {
					pv := portal.ParamValues[i]
					if !pv.IsNull || pv.Type != storage.TypeUnknown {
						anyVal = pv
					}
				}
				paramMap[pName] = anyVal
				paramMap[atName] = anyVal
				paramMap[dollarName] = anyVal
			}

			session, err := s.engine.NewSession()
			if err == nil {
				defer session.Close()
				res, err := s.executor.Run(portal.Statement.Program, "PreparedStatement", paramMap, session)
				if err == nil {
					s.writeExecutionResult(conn, res, portal.Statement.Query, portal.ResultFormats, portal.Statement)
					return
				}
			}
		}

		// Standard session SQL execution
		var pTypes []int32
		if portal.Statement != nil {
			pTypes = portal.Statement.ParamTypes
		}
		substitutedSQL := substituteParams(portal.Statement.Query, portal.Params, pTypes, portal.ParamValues)

		// Fast-Path for simple transaction control commands
		cleaned := stripSQLComments(substitutedSQL)
		trimmed := strings.TrimRight(strings.TrimSpace(cleaned), ";")
		upper := strings.ToUpper(strings.TrimSpace(trimmed))
		switch upper {
		case "BEGIN", "START TRANSACTION", "BEGIN WORK", "BEGIN TRANSACTION":
			if err := s.coordinator.BeginSessionTx(sessID, storage.TxOptions{}); err != nil {
				_ = WriteError(conn, err.Error())
				return
			}
			_ = WriteCommandComplete(conn, "BEGIN")
			portal.Executed = true
			return

		case "COMMIT", "END", "COMMIT TRANSACTION", "COMMIT WORK":
			if err := s.coordinator.CommitSessionTx(sessID); err != nil {
				_ = WriteError(conn, err.Error())
				return
			}
			_ = WriteCommandComplete(conn, "COMMIT")
			portal.Executed = true
			return

		case "ROLLBACK", "ABORT", "ROLLBACK TRANSACTION", "ROLLBACK WORK":
			if err := s.coordinator.RollbackSessionTx(sessID); err != nil {
				_ = WriteError(conn, err.Error())
				return
			}
			_ = WriteCommandComplete(conn, "ROLLBACK")
			portal.Executed = true
			return
		}

		res, err := s.coordinator.ExecuteSessionSQL(sessID, substitutedSQL)
		if err != nil {
			_ = WriteError(conn, err.Error())
			return
		}
		portal.Result = res
		portal.Pos = 0
		portal.Executed = true
	}

	res := portal.Result
	if res == nil {
		_ = WriteError(conn, "empty execution result")
		return
	}

	if len(res.Columns) == 0 {
		cmd := res.Command
		if cmd == "" {
			cmd = portal.Statement.Query
		}
		_ = WriteCommandComplete(conn, formatPgCommandComplete(cmd, res.RowsAffected, len(res.Rows)))
		return
	}

	totalRows := len(res.Rows)
	startIdx := portal.Pos
	endIdx := totalRows

	if maxRows > 0 {
		if startIdx+int(maxRows) < totalRows {
			endIdx = startIdx + int(maxRows)
		}
	}

	var batchRows []map[string]any
	if startIdx < totalRows {
		batchRows = res.Rows[startIdx:endIdx]
		portal.Pos = endIdx
	}

	var sampleRow map[string]any
	if len(res.Rows) > 0 {
		sampleRow = res.Rows[0]
	}
	queryForOIDs := portal.Statement.Query
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(queryForOIDs)), "EXEC") || strings.HasPrefix(strings.ToUpper(strings.TrimSpace(queryForOIDs)), "CALL") {
		procName, _ := engine.ParseProcArgs(queryForOIDs)
		if procName != "" && s.catalog != nil {
			if procMeta, err := s.catalog.GetProcedure(procName); err == nil && procMeta != nil {
				queryForOIDs = procMeta.Body
			}
		}
	}

	var colOIDs []int32
	if portal.Statement != nil && len(portal.Statement.ColumnOIDs) == len(res.Columns) {
		colOIDs = portal.Statement.ColumnOIDs
	} else {
		colOIDs = s.resolveColumnOIDs(queryForOIDs, res.Columns, sampleRow)
	}

	bw := getBufioWriter(conn)
	defer putBufioWriter(bw)
	scratchRowBytes := make([][]byte, len(res.Columns))
	for _, row := range batchRows {
		for i, colName := range res.Columns {
			isBinary := false
			if len(portal.ResultFormats) == 1 && portal.ResultFormats[0] == 1 {
				isBinary = true
			} else if i < len(portal.ResultFormats) && portal.ResultFormats[i] == 1 {
				isBinary = true
			}
			val := getRowValue(row, colName)
			scratchRowBytes[i] = formatColumnValue(val, colOIDs[i], isBinary)
		}
		_ = WriteDataRowBytes(bw, scratchRowBytes)
	}

	// 3. If there are still rows remaining and maxRows > 0, send PortalSuspended ('s')
	if maxRows > 0 && portal.Pos < totalRows {
		_ = bw.Flush()
		_ = WritePortalSuspended(conn)
		return
	}

	// 4. All rows delivered; send CommandComplete ('C')
	tag := formatPgCommandComplete(res.Command, res.RowsAffected, totalRows)
	_ = WriteCommandComplete(bw, tag)
	_ = bw.Flush()
}

func (s *Server) writeExecutionResult(conn net.Conn, res *runtime.ExecutionResult, query string, resultFormats []int16, stmt *PreparedStatement) {
	if len(res.Columns) > 0 {
		var colOIDs []int32
		if stmt != nil && len(stmt.ColumnOIDs) == len(res.Columns) {
			colOIDs = stmt.ColumnOIDs
		} else {
			var sampleRow map[string]any
			if len(res.Rows) > 0 {
				sampleRow = res.Rows[0]
			}
			colOIDs = s.resolveColumnOIDs(query, res.Columns, sampleRow)
		}

		bw := getBufioWriter(conn)
		defer putBufioWriter(bw)
		scratchRowBytes := make([][]byte, len(res.Columns))
		for _, row := range res.Rows {
			for i, colName := range res.Columns {
				isBinary := false
				if len(resultFormats) == 1 && resultFormats[0] == 1 {
					isBinary = true
				} else if i < len(resultFormats) && resultFormats[i] == 1 {
					isBinary = true
				}
				val := getRowValue(row, colName)
				scratchRowBytes[i] = formatColumnValue(val, colOIDs[i], isBinary)
			}
			_ = WriteDataRowBytes(bw, scratchRowBytes)
		}
		cmd := query
		if strings.HasPrefix(strings.ToUpper(cmd), "FETCH") {
			cmd = fmt.Sprintf("FETCH %d", len(res.Rows))
		}
		_ = WriteCommandComplete(bw, formatPgCommandComplete(cmd, int64(len(res.Rows)), len(res.Rows)))
		_ = bw.Flush()
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
	case "DECLARE":
		return "DECLARE CURSOR"
	case "CLOSE":
		return "CLOSE CURSOR"
	default:
		if firstWord != "" {
			return firstWord
		}
		return "OK"
	}
}

func (s *Server) handleClose(conn net.Conn, payload []byte, statements *PreparedStatementCache, portals map[string]*Portal) {
	if len(payload) >= 2 {
		targetType := payload[0]
		targetName := string(bytes.TrimRight(payload[1:], "\x00"))
		if targetType == 'S' {
			statements.Delete(targetName)
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
		_ = WriteDataRow(conn, []string{"PostgreSQL 15.0 (LionSQL OLTP & Stored Procedure Server)"})
		_ = WriteCommandComplete(conn, "SELECT 1")
		return
	}

	// Healthcheck / ping short-circuit (SELECT 1)
	if isSelectOne(strings.TrimRight(upper, ";")) {
		if s.coordinator.GetSessionTxStatus(sessID) == 'E' {
			_ = WriteError(conn, "current transaction is aborted, commands ignored until end of transaction block")
			return
		}
		cols := []ColumnMeta{{Name: "?column?", DataType: 23}}
		if writeRowDesc {
			_ = WriteRowDescription(conn, cols)
		}
		_ = WriteDataRow(conn, []string{"1"})
		_ = WriteCommandComplete(conn, "SELECT 1")
		return
	}

	if sessID == "" {
		sessID = "default"
	}
	res, err := s.coordinator.ExecuteSessionSQL(sessID, trimmed)
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

		bw := getBufioWriter(conn)
		defer putBufioWriter(bw)
		if writeRowDesc {
			cols := make([]ColumnMeta, len(res.Columns))
			for i, c := range res.Columns {
				cols[i] = ColumnMeta{Name: c, DataType: colOIDs[i]}
			}
			_ = WriteRowDescription(bw, cols)
		}

		scratchRowBytes := make([][]byte, len(res.Columns))
		for _, row := range res.Rows {
			for i, colName := range res.Columns {
				isBinary := false
				if len(resultFormats) == 1 && resultFormats[0] == 1 {
					isBinary = true
				} else if i < len(resultFormats) && resultFormats[i] == 1 {
					isBinary = true
				}
				val := getRowValue(row, colName)
				scratchRowBytes[i] = formatColumnValue(val, colOIDs[i], isBinary)
			}
			_ = WriteDataRowBytes(bw, scratchRowBytes)
		}
		cmd := res.Command
		if cmd == "" {
			cmd = fmt.Sprintf("SELECT %d", len(res.Rows))
		}
		tag := formatPgCommandComplete(cmd, res.RowsAffected, len(res.Rows))
		_ = WriteCommandComplete(bw, tag)
		_ = bw.Flush()
		return
	}

	cmd := res.Command
	if cmd == "" {
		cmd = trimmed
	}
	_ = WriteCommandComplete(conn, formatPgCommandComplete(cmd, res.RowsAffected, len(res.Rows)))
}

func cleanColumnIdentifier(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, ".") {
		parts := strings.Split(s, ".")
		s = parts[len(parts)-1]
	}
	s = strings.Trim(s, "[]\"'`")
	return strings.TrimSpace(s)
}

func (s *Server) resolveColumnOIDs(query string, colNames []string, sampleRow map[string]any) []int32 {
	oids := make([]int32, len(colNames))
	var tbl *storage.TableSchema
	fromIdx := sqlutil.FindTopLevelKeyword(query, "FROM")
	if fromIdx != -1 {
		afterFrom := query[fromIdx+4:]
		tblField := sqlutil.SplitTopLevelTokens(afterFrom)
		if len(tblField) > 0 {
			tblName := strings.Trim(tblField[0], ";, ")
			tblName = cleanColumnIdentifier(tblName)
			t, err := s.engine.GetTable(tblName)
			if err != nil {
				t, err = s.engine.GetTable(strings.ToLower(tblName))
			}
			if err == nil {
				tbl = t
			}
		}
	}

	for i, colName := range colNames {
		var oid int32 = 0
		cleanCol := cleanColumnIdentifier(colName)
		if tbl != nil {
			for _, c := range tbl.Columns {
				if strings.EqualFold(c.Name, cleanCol) || strings.EqualFold(c.Name, colName) {
					oid = typeToPgOID(c.Type)
					break
				}
			}
		}
		if oid == 0 {
			upperCol := strings.ToUpper(cleanCol)
			if strings.HasPrefix(upperCol, "COUNT") {
				oid = 20 // int8
			} else if sampleRow != nil {
				val := getRowValue(sampleRow, colName)
				if val == nil {
					val = getRowValue(sampleRow, cleanCol)
				}
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

func isPureIntegerString(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	if len(s)-start > 1 && s[start] == '0' {
		return false // Leading zero: formatted string, zip code, or code ID
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isPureFloatString(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	dotCount := 0
	for i := start; i < len(s); i++ {
		if s[i] == '.' {
			dotCount++
			if dotCount > 1 {
				return false
			}
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return dotCount == 1 && len(s)-start > 1
}

func formatParamForSQL(val string, pType int32, sv storage.Value) string {
	if strings.EqualFold(val, "null") || sv.IsNull {
		return "NULL"
	}

	// 1. Check explicit OID type from statement description
	switch pType {
	case 20, 21, 23: // INT8, INT2, INT4
		if i, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err == nil {
			return fmt.Sprintf("%d", i)
		}
	case 700, 701, 1700: // FLOAT4, FLOAT8, NUMERIC
		if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			return fmt.Sprintf("%g", f)
		}
	case 16: // BOOL
		if strings.EqualFold(val, "true") || val == "t" || val == "1" {
			return "true"
		} else if strings.EqualFold(val, "false") || val == "f" || val == "0" {
			return "false"
		}
	case 25, 1043, 18, 1042, 1082, 1114, 1184, 2950: // TEXT, VARCHAR, CHAR, BPCHAR, DATE, TIMESTAMP, UUID
		escaped := strings.ReplaceAll(val, "'", "''")
		return fmt.Sprintf("'%s'", escaped)
	}

	// 2. Check decoded storage.Value type
	switch sv.Type {
	case storage.TypeInt:
		return fmt.Sprintf("%d", sv.IVal)
	case storage.TypeFloat:
		return fmt.Sprintf("%g", sv.FVal)
	case storage.TypeBool:
		if sv.BVal {
			return "true"
		}
		return "false"
	case storage.TypeVarChar, storage.TypeUUID, storage.TypeDateTime, storage.TypeDate, storage.TypeTime:
		escaped := strings.ReplaceAll(val, "'", "''")
		return fmt.Sprintf("'%s'", escaped)
	}

	// 3. Fallback for un-typed parameters (OID 0)
	trimmed := strings.TrimSpace(val)
	if isPureIntegerString(trimmed) {
		return trimmed
	}
	if isPureFloatString(trimmed) {
		return trimmed
	}
	if strings.EqualFold(trimmed, "true") || strings.EqualFold(trimmed, "false") {
		return trimmed
	}

	// Default to safely quoted string literal
	escaped := strings.ReplaceAll(val, "'", "''")
	return fmt.Sprintf("'%s'", escaped)
}

func substituteParams(sql string, params []string, paramTypes []int32, paramValues []storage.Value) string {
	n := len(sql)
	if n == 0 || len(params) == 0 {
		return sql
	}

	// Pre-format parameters once
	formattedParams := make([]string, len(params))
	for i := 0; i < len(params); i++ {
		val := params[i]
		var pType int32
		if i < len(paramTypes) {
			pType = paramTypes[i]
		}
		var sv storage.Value
		if i < len(paramValues) {
			sv = paramValues[i]
		}
		formattedParams[i] = formatParamForSQL(val, pType, sv)
	}

	var b strings.Builder
	b.Grow(n + len(params)*16)

	inString := false
	var quoteChar byte

	for i := 0; i < n; {
		ch := sql[i]

		if inString {
			b.WriteByte(ch)
			if ch == quoteChar {
				// Handle escaped quote: ''
				if i+1 < n && sql[i+1] == quoteChar {
					b.WriteByte(sql[i+1])
					i += 2
					continue
				}
				inString = false
			}
			i++
			continue
		}

		// String literal start
		if ch == '\'' {
			inString = true
			quoteChar = ch
			b.WriteByte(ch)
			i++
			continue
		}

		// Line comment (-- ...)
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			b.WriteByte(ch)
			b.WriteByte(sql[i+1])
			i += 2
			for i < n && sql[i] != '\n' {
				b.WriteByte(sql[i])
				i++
			}
			continue
		}

		// Block comment (/* ... */)
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			b.WriteByte(ch)
			b.WriteByte(sql[i+1])
			i += 2
			for i < n {
				if sql[i] == '*' && i+1 < n && sql[i+1] == '/' {
					b.WriteByte('*')
					b.WriteByte('/')
					i += 2
					break
				}
				b.WriteByte(sql[i])
				i++
			}
			continue
		}

		// Dollar parameter: $1, $2, ...
		if ch == '$' && i+1 < n && sql[i+1] >= '1' && sql[i+1] <= '9' {
			j := i + 1
			for j < n && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			numStr := sql[i+1 : j]
			if idx, err := strconv.Atoi(numStr); err == nil && idx >= 1 && idx <= len(formattedParams) {
				b.WriteString(formattedParams[idx-1])
				i = j
				continue
			}
		}

		b.WriteByte(ch)
		i++
	}

	return b.String()
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
	switch val := v.(type) {
	case int8, int16, uint8, uint16:
		return 21 // int2
	case int, int32:
		return 23 // int4
	case uint, uint32:
		return 20 // int8
	case int64:
		if val >= -2147483648 && val <= 2147483647 {
			return 23 // int4
		}
		return 20 // int8
	case uint64:
		if val <= 2147483647 {
			return 23 // int4
		}
		return 20 // int8
	case float32:
		return 700 // float4
	case float64:
		return 701 // float8
	case bool:
		return 16 // bool
	case time.Time:
		return 1114 // timestamp
	case []byte:
		return 17 // bytea
	case storage.Decimal:
		return 1700 // numeric
	case *storage.Decimal:
		return 1700 // numeric
	case storage.Value:
		if val.IsNull {
			return typeToPgOID(val.Type)
		}
		switch val.Type {
		case storage.TypeInt:
			if val.IVal >= -2147483648 && val.IVal <= 2147483647 {
				return 23
			}
			return 20
		case storage.TypeFloat:
			return 701
		case storage.TypeBool:
			return 16
		case storage.TypeDateTime:
			return 1114
		case storage.TypeDate:
			return 1082
		case storage.TypeTime:
			return 1083
		case storage.TypeDecimal:
			return 1700
		case storage.TypeUUID:
			return 2950
		case storage.TypeBinary:
			return 17
		default:
			return 25
		}
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
