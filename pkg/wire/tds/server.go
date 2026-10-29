package tds

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
)

// Server implements a native Microsoft SQL Server TDS 7.x/8.0 wire protocol listener.
type Server struct {
	addr        string
	listener    net.Listener
	engine      storage.Engine
	catalog     *catalog.Catalog
	coordinator engine.SQLCoordinator
	planCache   *proc.PlanCache
	byteCache   *runtime.PlanCache
	executor    *runtime.Executor
	spidCounter uint32
	txCounter   uint64
	tlsConfig   *tls.Config
	requireSSL  bool
	devMode     bool
	mu          sync.Mutex
	closed      bool
	wg          sync.WaitGroup
	activeConns           map[net.Conn]struct{}
	maxPreparedPerSession int
}

// NewServer creates a new TDS wire protocol server.
func NewServer(addr string, cat *catalog.Catalog, eng storage.Engine) *Server {
	return NewServerWithCache(addr, cat, eng, runtime.NewPlanCache())
}

// NewServerWithCache creates a new TDS wire protocol server with an existing bytecode PlanCache.
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
		spidCounter: 50, // Standard MSSQL user SPIDs begin at 51
		txCounter:   1000,
	}
}

// NewServerWithCoordinator creates a TDS wire protocol server with an explicit SQLCoordinator.
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
	for c := range s.activeConns {
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

// SetTLSConfig sets the TLS configuration for encrypted SSL/TLS connections.
func (s *Server) SetTLSConfig(cfg *tls.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tlsConfig = cfg
}

// SetRequireSSL specifies whether unencrypted connections should be rejected when TLS is enabled.
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

// RequireSSL returns whether SSL/TLS is strictly required.
func (s *Server) RequireSSL() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requireSSL
}

// SetDevMode enables or disables developer debug logging for TDS wire batches and RPCs.
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
		if s.activeConns == nil {
			s.activeConns = make(map[net.Conn]struct{})
		}
		s.activeConns[conn] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.activeConns, c)
				s.mu.Unlock()
			}()
			s.handleConnection(c)
		}(conn)
	}
}

// TdsPreparedStatement stores metadata for a prepared TDS statement.
type TdsPreparedStatement struct {
	Handle  int32
	Params  string
	Query   string
	Program *proc.Program
}

// DefaultMaxPreparedPerSession is the maximum number of prepared statements retained per connection session.
const DefaultMaxPreparedPerSession = 256

type tdsPreparedEntry struct {
	handle int32
	stmt   *TdsPreparedStatement
}

// TdsPreparedStatementCache implements a thread-safe bounded LRU cache for prepared statements (Golden Rule 23).
type TdsPreparedStatementCache struct {
	mu       sync.Mutex
	capacity int
	items    map[int32]*list.Element
	lruList  *list.List
}

// NewTdsPreparedStatementCache creates a new bounded LRU cache for prepared statements.
func NewTdsPreparedStatementCache(capacity int) *TdsPreparedStatementCache {
	if capacity <= 0 {
		capacity = DefaultMaxPreparedPerSession
	}
	return &TdsPreparedStatementCache{
		capacity: capacity,
		items:    make(map[int32]*list.Element),
		lruList:  list.New(),
	}
}

// Put inserts or updates a prepared statement in the cache, evicting the least recently used statement if capacity is reached.
func (c *TdsPreparedStatementCache) Put(stmt *TdsPreparedStatement) {
	if stmt == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, exists := c.items[stmt.Handle]; exists {
		elem.Value.(*tdsPreparedEntry).stmt = stmt
		c.lruList.MoveToFront(elem)
		return
	}

	if c.capacity > 0 && c.lruList.Len() >= c.capacity {
		back := c.lruList.Back()
		if back != nil {
			c.lruList.Remove(back)
			old := back.Value.(*tdsPreparedEntry)
			delete(c.items, old.handle)
		}
	}

	elem := c.lruList.PushFront(&tdsPreparedEntry{
		handle: stmt.Handle,
		stmt:   stmt,
	})
	c.items[stmt.Handle] = elem
}

// Get retrieves a prepared statement by handle and marks it as most recently used.
func (c *TdsPreparedStatementCache) Get(handle int32) (*TdsPreparedStatement, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, exists := c.items[handle]
	if !exists {
		return nil, false
	}
	c.lruList.MoveToFront(elem)
	return elem.Value.(*tdsPreparedEntry).stmt, true
}

// Delete removes a prepared statement by handle from the cache.
func (c *TdsPreparedStatementCache) Delete(handle int32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, exists := c.items[handle]; exists {
		c.lruList.Remove(elem)
		delete(c.items, handle)
	}
}

// Len returns the current number of cached prepared statements.
func (c *TdsPreparedStatementCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lruList.Len()
}

// Capacity returns the maximum capacity of the cache.
func (c *TdsPreparedStatementCache) Capacity() int {
	return c.capacity
}

var (
	staticParamP  [64]string
	staticParamAt [64]string
)

func init() {
	for i := 0; i < 64; i++ {
		pIdx := i + 1
		staticParamP[i] = fmt.Sprintf("p%d", pIdx)
		staticParamAt[i] = fmt.Sprintf("@p%d", pIdx)
	}
}

func getParamNames(pIdx int) (string, string) {
	if pIdx >= 1 && pIdx <= 64 {
		return staticParamP[pIdx-1], staticParamAt[pIdx-1]
	}
	return fmt.Sprintf("p%d", pIdx), fmt.Sprintf("@p%d", pIdx)
}

// RPCParam describes a parameter passed in an RPC call.
type RPCParam struct {
	Name     string
	IsOutput bool
	Type     byte
	Value    any
}

type parsedRPC struct {
	ProcID      uint16
	ProcName    string
	OptionFlags uint16
	Params      []RPCParam
}

// ConnectionSession holds state for a single active TDS client connection.
type ConnectionSession struct {
	SPID          uint16
	SessionID     string
	HostName      string
	UserName      string
	Password      string
	AppName       string
	Database      string
	PacketSize    uint32
	TDSVersion    uint32
	inTx          bool
	txDescriptor  uint64
	prepMu        sync.Mutex
	prepCounter   int32
	prepared      *TdsPreparedStatementCache
	clientSession *engine.ClientSession
}

var tdsReaderPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(nil, 8192)
	},
}

func (s *Server) getClientSession(sess *ConnectionSession) *engine.ClientSession {
	if sess.clientSession != nil {
		return sess.clientSession
	}
	if s.coordinator != nil && sess.SessionID != "" {
		cs := s.coordinator.GetSession(sess.SessionID)
		sess.clientSession = cs
		return cs
	}
	return nil
}

func (sess *ConnectionSession) writePacket(w io.Writer, pType byte, payload []byte) error {
	pktSize := sess.PacketSize
	if pktSize == 0 {
		pktSize = DefaultPacketSize
	}
	return WritePacketWithSize(w, pType, sess.SPID, payload, pktSize)
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	s.mu.Lock()
	tlsCfg := s.tlsConfig
	s.mu.Unlock()

	// 1. Detect direct TLS (TDS 8.0) vs TDS 7.x prelogin
	var firstByte [1]byte
	if _, err := io.ReadFull(conn, firstByte[:]); err != nil {
		return
	}

	conn = &bufferedConn{
		Conn: conn,
		r:    io.MultiReader(bytes.NewReader(firstByte[:]), conn),
	}

	if firstByte[0] == 0x16 {
		// TDS 8.0: Direct TLS Handshake
		if tlsCfg == nil {
			return
		}
		tlsConn := tls.Server(conn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn = tlsConn
	}

	spid := uint16(atomic.AddUint32(&s.spidCounter, 1))
	sess := &ConnectionSession{
		SPID:       spid,
		SessionID:  fmt.Sprintf("tds-%d", spid),
		Database:   "master",
		PacketSize: DefaultPacketSize,
		TDSVersion: 0x74000004, // Default to TDS 7.4
		prepared:   NewTdsPreparedStatementCache(s.getMaxPreparedPerSession()),
	}
	defer s.coordinator.CloseSession(sess.SessionID)

	// 2. Read initial packet (usually PacketPrelogin or direct Login7)
	firstPkt, err := ReadPacket(conn)
	if err != nil {
		return
	}

	if firstPkt.Type == PacketPrelogin {
		conn, err = s.handlePrelogin(conn, firstPkt, sess)
		if err != nil {
			return
		}
	}

	stopChan := make(chan struct{})
	defer close(stopChan)

	packetChan := make(chan *Packet, 8)
	errChan := make(chan error, 1)

	// If the initial packet was not Prelogin, queue it for processing
	if firstPkt.Type != PacketPrelogin {
		packetChan <- firstPkt
	}

	go func() {
		br := tdsReaderPool.Get().(*bufio.Reader)
		br.Reset(conn)
		defer func() {
			br.Reset(nil)
			tdsReaderPool.Put(br)
		}()

		for {
			p, err := ReadPacket(br)
			if err != nil {
				select {
				case <-stopChan:
				case errChan <- err:
				}
				return
			}
			select {
			case <-stopChan:
				return
			case packetChan <- p:
			}
		}
	}()

	var pendingPkt *Packet
	for {
		var p *Packet
		if pendingPkt != nil {
			p = pendingPkt
			pendingPkt = nil
		} else {
			select {
			case <-errChan:
				return
			case p = <-packetChan:
			}
		}

		switch p.Type {
		case PacketPrelogin:
			var err error
			conn, err = s.handlePrelogin(conn, p, sess)
			if err != nil {
				return
			}

		case PacketLogin7:
			s.handleLogin7(conn, p, sess)

		case PacketSQLBatch:
			pendingPkt = s.runQueryWithAttention(conn, p, sess, packetChan, errChan, func(ctx context.Context) {
				s.handleSQLBatch(conn, p, sess, ctx)
			})

		case PacketRPC:
			pendingPkt = s.runQueryWithAttention(conn, p, sess, packetChan, errChan, func(ctx context.Context) {
				s.handleRPC(conn, p, sess, ctx)
			})

		case PacketTransactionManager:
			s.handleTransactionManager(conn, p, sess)

		case PacketAttention:
			s.handleAttention(conn, p, sess)

		default:
			// Fallback: send simple DONE
			doneBytes := BuildDone(TokenDone, DoneFinal, 0, 0)
			_ = sess.writePacket(conn, PacketTabular, doneBytes)
		}
	}
}

func (s *Server) runQueryWithAttention(
	conn net.Conn,
	pkt *Packet,
	sess *ConnectionSession,
	packetChan <-chan *Packet,
	errChan <-chan error,
	execFn func(ctx context.Context),
) *Packet {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		execFn(ctx)
	}()

	select {
	case <-queryDone:
		return nil

	case nextPkt := <-packetChan:
		// Prioritize checking if query was already finished
		select {
		case <-queryDone:
			if nextPkt.Type == PacketAttention {
				s.handleAttention(conn, nextPkt, sess)
				return nil
			}
			return nextPkt
		default:
		}

		if nextPkt.Type == PacketAttention {
			cancel()
			<-queryDone
			s.handleAttention(conn, nextPkt, sess)
			return nil
		}

		// A subsequent command packet arrived before queryDone unblocked; wait for completion and preserve packet
		<-queryDone
		return nextPkt

	case <-errChan:
		cancel()
		<-queryDone
		return nil
	}
}

func (s *Server) handlePrelogin(conn net.Conn, pkt *Packet, sess *ConnectionSession) (net.Conn, error) {
	s.mu.Lock()
	tlsCfg := s.tlsConfig
	requireSSL := s.requireSSL
	s.mu.Unlock()

	// If connection is already wrapped with TLS (e.g. TDS 8.0 direct TLS)
	if _, isTLS := conn.(*tls.Conn); isTLS {
		respPayload := BuildPreloginResponse(EncryptOn)
		if err := sess.writePacket(conn, PacketTabular, respPayload); err != nil {
			return nil, err
		}
		return conn, nil
	}

	opts := ParsePrelogin(pkt.Payload)
	clientEncrypt := EncryptNotSup
	if enc, ok := opts[PreloginOptionEncryption]; ok && len(enc) > 0 {
		clientEncrypt = enc[0]
	}

	var serverEncrypt byte
	shouldDoTLS := false

	if tlsCfg == nil {
		if requireSSL || clientEncrypt == EncryptReq {
			return nil, fmt.Errorf("encryption required but server does not support TLS")
		}
		serverEncrypt = EncryptNotSup
	} else {
		// Server has TLS enabled
		if requireSSL {
			if clientEncrypt == EncryptNotSup {
				return nil, fmt.Errorf("server requires SSL but client does not support encryption")
			}
			serverEncrypt = EncryptReq
			shouldDoTLS = true
		} else {
			switch clientEncrypt {
			case EncryptOn, EncryptReq:
				serverEncrypt = EncryptOn
				shouldDoTLS = true
			case EncryptOff:
				// Negotiate encryption to protect credentials and connection
				serverEncrypt = EncryptOn
				shouldDoTLS = true
			default:
				// EncryptNotSup
				serverEncrypt = EncryptNotSup
			}
		}
	}

	// 1. Respond with PRELOGIN options: Version, Encryption, MARS
	respPayload := BuildPreloginResponse(serverEncrypt)
	if err := sess.writePacket(conn, PacketTabular, respPayload); err != nil {
		return nil, err
	}

	// 2. Perform TDS 7.x TLS Handshake if encryption was negotiated
	if shouldDoTLS {
		handshakeConn := NewTDSHandshakeConn(conn, sess.SPID)
		tlsConn := tls.Server(handshakeConn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			return nil, fmt.Errorf("TDS TLS handshake failed: %w", err)
		}
		handshakeConn.FinishHandshake()
		conn = tlsConn
	}

	return conn, nil
}

func (s *Server) handleLogin7(conn net.Conn, pkt *Packet, sess *ConnectionSession) {
	s.parseLogin7(pkt.Payload, sess)
	if sess.UserName == "" {
		sess.UserName = "sa"
	}

	// Verify authentication if coordinator auth is enabled or login requires password
	if s.coordinator != nil {
		authSvc := s.coordinator.AuthService()
		if authSvc != nil {
			needsAuth := false
			if s.coordinator.IsAuthEnabled() {
				needsAuth = true
			} else if login, err := authSvc.GetLogin(sess.UserName); err == nil && login != nil && login.PasswordHash != "" {
				needsAuth = true
			}

			if needsAuth {
				if _, err := authSvc.AuthenticateLogin(sess.UserName, sess.Password); err != nil {
					errMsg := fmt.Sprintf("Login failed for user '%s'.", sess.UserName)
					errToken := BuildError(18456, 1, 14, errMsg, "LionSQL", "", 1)
					doneToken := BuildDone(TokenDone, DoneError, 0, 0)

					var errResp bytes.Buffer
					errResp.Write(errToken)
					errResp.Write(doneToken)
					_ = sess.writePacket(conn, PacketTabular, errResp.Bytes())
					return
				}
			}
		}
		s.coordinator.SetSessionUser(sess.SessionID, sess.UserName)
		sess.clientSession = s.coordinator.GetSession(sess.SessionID)
	}

	var resp bytes.Buffer

	// 1. LOGINACK token (0xAD)
	loginAck := BuildLoginAck("LionSQL", 15, 0, 2000)
	resp.Write(loginAck)

	// 2. ENVCHANGE for Database (0xE3)
	envDB := BuildEnvChangeDatabase(sess.Database, "master")
	resp.Write(envDB)

	// 3. ENVCHANGE for Collation (0xE3)
	envCollation := BuildEnvChangeCollation(DefaultCollation)
	resp.Write(envCollation)

	// 4. ENVCHANGE for PacketSize (0xE3)
	pktSizeStr := fmt.Sprintf("%d", sess.PacketSize)
	envPkt := BuildEnvChangePacketSize(pktSizeStr, "4096")
	resp.Write(envPkt)

	// 5. DONE token (0xFD)
	done := BuildDone(TokenDone, DoneFinal, 0, 0)
	resp.Write(done)

	_ = sess.writePacket(conn, PacketTabular, resp.Bytes())
}

func (s *Server) parseLogin7(payload []byte, sess *ConnectionSession) {
	if len(payload) < 94 {
		return
	}

	// TDS Version negotiated by client (offset 4..8, DWORD Big-Endian per MS-TDS 2.2.6.4)
	tdsVer := binary.BigEndian.Uint32(payload[4:8])
	if tdsVer != 0 {
		sess.TDSVersion = tdsVer
	}

	// Packet Size requested by client
	packetSize := binary.LittleEndian.Uint32(payload[8:12])
	if packetSize >= 512 && packetSize <= 32768 {
		sess.PacketSize = packetSize
	}

	// Extract UTF-16LE strings using offset/length pairs
	sess.HostName = readLoginString(payload, 36)
	sess.UserName = readLoginString(payload, 40)
	sess.Password = readLoginPassword(payload, 44)
	sess.AppName = readLoginString(payload, 48)
	dbName := readLoginString(payload, 68)
	if dbName != "" {
		sess.Database = dbName
	}
}

// buildRow formats a row of column values, choosing NBCROW (0xD2) when the negotiated
// TDS version is 7.3 or newer and the row contains one or more NULL values,
// or ROW (0xD1) otherwise.
func (sess *ConnectionSession) buildRow(cols []ColumnInfo, values []any) []byte {
	if sess != nil && sess.TDSVersion >= 0x73000000 {
		for _, v := range values {
			if v == nil {
				return BuildNBCRow(cols, values)
			}
		}
	}
	return BuildRow(cols, values)
}

// writeRowDirect formats and appends a row to destBuf, choosing NBCROW (0xD2) when negotiated
// TDS version is 7.3 or newer, or ROW (0xD1) otherwise, with zero intermediate []any boxing (P-3).
func (sess *ConnectionSession) writeRowDirect(destBuf *bytes.Buffer, cols []ColumnInfo, row map[string]any, scratchVals []any) {
	if sess != nil && sess.TDSVersion >= 0x73000000 {
		for _, col := range cols {
			if row[col.Name] == nil {
				WriteNBCRowDirect(destBuf, cols, row)
				return
			}
		}
	}
	WriteRowDirect(destBuf, cols, row)
}

func readLoginString(payload []byte, offsetPos int) string {
	if offsetPos+4 > len(payload) {
		return ""
	}
	ib := int(binary.LittleEndian.Uint16(payload[offsetPos : offsetPos+2]))
	cch := int(binary.LittleEndian.Uint16(payload[offsetPos+2 : offsetPos+4]))
	byteLen := cch * 2

	if ib == 0 || cch == 0 || ib+byteLen > len(payload) {
		return ""
	}

	return DecodeUTF16LE(payload[ib : ib+byteLen])
}

// readLoginPassword extracts and de-obfuscates the TDS Login7 password per MS-TDS 2.2.6.4.
func readLoginPassword(payload []byte, offsetPos int) string {
	if offsetPos+4 > len(payload) {
		return ""
	}
	ib := int(binary.LittleEndian.Uint16(payload[offsetPos : offsetPos+2]))
	cch := int(binary.LittleEndian.Uint16(payload[offsetPos+2 : offsetPos+4]))
	byteLen := cch * 2

	if ib == 0 || cch == 0 || ib+byteLen > len(payload) {
		return ""
	}

	raw := payload[ib : ib+byteLen]
	decrypted := make([]byte, len(raw))
	for i, b := range raw {
		b1 := b ^ 0xA5
		decrypted[i] = ((b1 >> 4) & 0x0F) | ((b1 << 4) & 0xF0)
	}

	return DecodeUTF16LE(decrypted)
}

func (s *Server) handleSQLBatch(conn net.Conn, pkt *Packet, sess *ConnectionSession, ctx context.Context) {
	query := extractSQLBatchQuery(pkt.Payload)
	if s.DevMode() {
		log.Printf("[DEV][TDS][SQLBatch] %s", query)
	}
	s.executeQueryAndRespond(conn, query, sess, ctx)
}

func extractSQLBatchQuery(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}

	// Check for ALL_HEADERS (TDS 7.2+)
	if len(payload) >= 4 {
		totalHeadersLen := binary.LittleEndian.Uint32(payload[:4])
		if totalHeadersLen >= 4 && int(totalHeadersLen) < len(payload) {
			// Header contains transaction descriptor or query notifications
			sqlBytes := payload[totalHeadersLen:]
			return strings.TrimSpace(strings.Trim(DecodeUTF16LE(sqlBytes), "\x00"))
		}
	}

	// Direct UTF-16LE payload
	return strings.TrimSpace(strings.Trim(DecodeUTF16LE(payload), "\x00"))
}

func (s *Server) handleRPC(conn net.Conn, pkt *Packet, sess *ConnectionSession, ctx context.Context) {
	rpc, err := parseRPCRequest(pkt.Payload)
	if err != nil || rpc == nil {
		done := BuildDone(TokenDone, DoneFinal, 0, 0)
		_ = sess.writePacket(conn, PacketTabular, done)
		return
	}

	procID := rpc.ProcID
	switch strings.ToLower(rpc.ProcName) {
	case "sp_executesql":
		if procID == 0 {
			procID = 10
		}
	case "sp_prepare":
		if procID == 0 {
			procID = 11
		}
	case "sp_execute":
		if procID == 0 {
			procID = 12
		}
	case "sp_prepexec":
		if procID == 0 {
			procID = 13
		}
	case "sp_unprepare":
		if procID == 0 {
			procID = 15
		}
	}

	switch procID {
	case 10: // sp_executesql
		s.handleRPCExecuteSQL(conn, rpc, sess, ctx)
	case 11: // sp_prepare
		s.handleRPCPrepare(conn, rpc, sess)
	case 12: // sp_execute
		s.handleRPCExecute(conn, rpc, sess, ctx)
	case 13: // sp_prepexec
		s.handleRPCPrepExec(conn, rpc, sess, ctx)
	case 15: // sp_unprepare
		s.handleRPCUnprepare(conn, rpc, sess)
	default:
		if rpc.ProcName != "" {
			query := fmt.Sprintf("EXEC %s", rpc.ProcName)
			if len(rpc.Params) > 0 {
				var argStrs []string
				for _, p := range rpc.Params {
					valStr := formatSQLValue(p.Value)
					if p.Name != "" {
						argStrs = append(argStrs, fmt.Sprintf("%s = %s", p.Name, valStr))
					} else {
						argStrs = append(argStrs, valStr)
					}
				}
				query = fmt.Sprintf("EXEC %s %s", rpc.ProcName, strings.Join(argStrs, ", "))
			}
			s.executeRPCAndRespond(conn, query, sess, nil, ctx)
		} else {
			if rpc.ProcID != 0 && rpc.ProcName == "" {
				var buf bytes.Buffer
				buf.Write(BuildError(2812, 1, 16, fmt.Sprintf("Could not find stored procedure 'ProcID %d'.", rpc.ProcID), "LionSQL", "", 1))
				buf.Write(BuildReturnStatus(-1))
				buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
				_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
				return
			}
			done := BuildDoneProc(DoneFinal, 0, 0)
			_ = sess.writePacket(conn, PacketTabular, done)
		}
	}
}

func (s *Server) handleRPCPrepare(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession) {
	var paramsDecl string
	var stmt string

	if len(rpc.Params) == 2 {
		if str, ok := rpc.Params[1].Value.(string); ok {
			stmt = str
		}
	} else if len(rpc.Params) >= 3 {
		for _, p := range rpc.Params {
			name := strings.ToLower(strings.TrimSpace(p.Name))
			if name == "@params" {
				if str, ok := p.Value.(string); ok {
					paramsDecl = str
				}
			} else if name == "@stmt" {
				if str, ok := p.Value.(string); ok {
					stmt = str
				}
			}
		}
		if paramsDecl == "" {
			if str, ok := rpc.Params[1].Value.(string); ok {
				paramsDecl = str
			}
		}
		if stmt == "" {
			if str, ok := rpc.Params[2].Value.(string); ok {
				stmt = str
			}
		}
	}

	sess.prepMu.Lock()
	sess.prepCounter++
	handle := sess.prepCounter
	prep := &TdsPreparedStatement{
		Handle: handle,
		Params: paramsDecl,
		Query:  stmt,
	}
	if stmt != "" {
		if cache := s.BytecodePlanCache(); cache != nil {
			if prog, err := cache.GetOrCompile(stmt); err == nil && prog != nil {
				prep.Program = prog
			}
		} else if prog, err := proc.Compile(stmt); err == nil && prog != nil {
			prep.Program = prog
		}
	}
	sess.prepared.Put(prep)
	sess.prepMu.Unlock()

	var buf bytes.Buffer
	buf.Write(BuildReturnValueInt(1, "@handle", handle))
	buf.Write(BuildReturnStatus(0))
	buf.Write(BuildDoneProc(DoneFinal, 0, 0))
	_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
}

func (s *Server) handleRPCExecute(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession, ctx context.Context) {
	var handle int32
	if len(rpc.Params) > 0 {
		handle = toInt32(rpc.Params[0].Value)
	}

	prep, exists := sess.prepared.Get(handle)

	if !exists {
		var buf bytes.Buffer
		buf.Write(BuildError(8179, 1, 16, fmt.Sprintf("Could not find prepared statement with handle %d.", handle), "LionSQL", "", 1))
		buf.Write(BuildReturnStatus(-1))
		buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
		return
	}

	var boundParams []RPCParam
	if len(rpc.Params) > 1 {
		boundParams = rpc.Params[1:]
	}

	// Fast-path: Execute via precompiled VDBE bytecode Program if available
	if prep.Program != nil {
		paramMap := make(map[string]any, len(boundParams)*2)
		for i, p := range boundParams {
			if p.Name != "" {
				cleanName := strings.TrimPrefix(p.Name, "@")
				paramMap[cleanName] = p.Value
				paramMap["@"+cleanName] = p.Value
			}
			pIdx := i + 1
			pName, atName := getParamNames(pIdx)
			paramMap[pName] = p.Value
			paramMap[atName] = p.Value
		}

		cs := s.getClientSession(sess)

		var res *runtime.ExecutionResult
		var runErr error

		if cs != nil {
			cs.Mu.Lock()
			if cs.TxFailed {
				cs.Mu.Unlock()
				var buf bytes.Buffer
				buf.Write(BuildError(3903, 1, 16, "The current transaction cannot be committed and cannot support operations that write to the log file. Roll back the transaction.", "LionSQL", "", 1))
				buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
				_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
				return
			}
			if cs.Tx != nil {
				res, runErr = s.executor.ExecuteInTxWithSessionContext(ctx, prep.Program, paramMap, cs.Tx, cs.Session, nil)
				if runErr != nil {
					cs.TxFailed = true
				}
			}
			cs.Mu.Unlock()
		}

		if (cs == nil || cs.Tx == nil) && runErr == nil {
			var session storage.Session
			var shouldClose bool
			if cs != nil && cs.Session != nil {
				session = cs.Session
			} else {
				session, _ = s.engine.NewSession()
				shouldClose = true
			}
			if session != nil {
				if shouldClose {
					defer session.Close()
				}
				res, runErr = s.executor.RunContext(ctx, prep.Program, "TdsPrepared", paramMap, session)
			}
		}

		if cs != nil && cs.Tx != nil && runErr != nil {
			var buf bytes.Buffer
			num, state, sev, msg := extractTdsErrorInfo(runErr)
			buf.Write(BuildError(num, state, sev, msg, "LionSQL", "", 1))
			buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
			_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
			return
		}

		if runErr == nil && res != nil {
			s.respondExecutionResult(conn, res, sess, nil)
			return
		}
		if ctx.Err() != nil {
			return
		}
	}

	execSQL := s.buildBoundSQL(prep.Query, prep.Params, boundParams)
	s.executeRPCAndRespond(conn, execSQL, sess, nil, ctx)
}

func (s *Server) handleRPCPrepExec(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession, ctx context.Context) {
	var paramsDecl string
	var stmt string

	if len(rpc.Params) == 2 {
		if str, ok := rpc.Params[1].Value.(string); ok {
			stmt = str
		}
	} else if len(rpc.Params) >= 3 {
		for _, p := range rpc.Params {
			name := strings.ToLower(strings.TrimSpace(p.Name))
			if name == "@params" {
				if str, ok := p.Value.(string); ok {
					paramsDecl = str
				}
			} else if name == "@stmt" {
				if str, ok := p.Value.(string); ok {
					stmt = str
				}
			}
		}
		if paramsDecl == "" {
			if str, ok := rpc.Params[1].Value.(string); ok {
				paramsDecl = str
			}
		}
		if stmt == "" {
			if str, ok := rpc.Params[2].Value.(string); ok {
				stmt = str
			}
		}
	}

	sess.prepMu.Lock()
	sess.prepCounter++
	handle := sess.prepCounter
	prep := &TdsPreparedStatement{
		Handle: handle,
		Params: paramsDecl,
		Query:  stmt,
	}
	if stmt != "" {
		if cache := s.BytecodePlanCache(); cache != nil {
			if prog, err := cache.GetOrCompile(stmt); err == nil && prog != nil {
				prep.Program = prog
			}
		} else if prog, err := proc.Compile(stmt); err == nil && prog != nil {
			prep.Program = prog
		}
	}
	sess.prepared.Put(prep)
	sess.prepMu.Unlock()

	var boundParams []RPCParam
	if len(rpc.Params) > 3 {
		boundParams = rpc.Params[3:]
	}

	// Fast-path: Execute via precompiled VDBE bytecode Program if available
	if prep.Program != nil {
		paramMap := make(map[string]any, len(boundParams)*2)
		for i, p := range boundParams {
			if p.Name != "" {
				cleanName := strings.TrimPrefix(p.Name, "@")
				paramMap[cleanName] = p.Value
				paramMap["@"+cleanName] = p.Value
			}
			pIdx := i + 1
			pName, atName := getParamNames(pIdx)
			paramMap[pName] = p.Value
			paramMap[atName] = p.Value
		}

		cs := s.getClientSession(sess)

		var res *runtime.ExecutionResult
		var runErr error

		if cs != nil {
			cs.Mu.Lock()
			if cs.TxFailed {
				cs.Mu.Unlock()
				var buf bytes.Buffer
				buf.Write(BuildError(3903, 1, 16, "The current transaction cannot be committed and cannot support operations that write to the log file. Roll back the transaction.", "LionSQL", "", 1))
				buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
				_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
				return
			}
			if cs.Tx != nil {
				res, runErr = s.executor.ExecuteInTxWithSessionContext(ctx, prep.Program, paramMap, cs.Tx, cs.Session, nil)
				if runErr != nil {
					cs.TxFailed = true
				}
			}
			cs.Mu.Unlock()
		}

		if (cs == nil || cs.Tx == nil) && runErr == nil {
			var session storage.Session
			var shouldClose bool
			if cs != nil && cs.Session != nil {
				session = cs.Session
			} else {
				session, _ = s.engine.NewSession()
				shouldClose = true
			}
			if session != nil {
				if shouldClose {
					defer session.Close()
				}
				res, runErr = s.executor.RunContext(ctx, prep.Program, "TdsPrepared", paramMap, session)
			}
		}

		if cs != nil && cs.Tx != nil && runErr != nil {
			var buf bytes.Buffer
			num, state, sev, msg := extractTdsErrorInfo(runErr)
			buf.Write(BuildError(num, state, sev, msg, "LionSQL", "", 1))
			buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
			_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
			return
		}

		if runErr == nil && res != nil {
			s.respondExecutionResult(conn, res, sess, func(buf *bytes.Buffer) {
				buf.Write(BuildReturnValueInt(1, "@handle", handle))
			})
			return
		}
		if ctx.Err() != nil {
			return
		}
	}

	execSQL := s.buildBoundSQL(stmt, paramsDecl, boundParams)
	s.executeRPCAndRespond(conn, execSQL, sess, func(buf *bytes.Buffer) {
		buf.Write(BuildReturnValueInt(1, "@handle", handle))
	}, ctx)
}

func (s *Server) respondExecutionResult(conn net.Conn, res *runtime.ExecutionResult, sess *ConnectionSession, extraTokens func(buf *bytes.Buffer)) {
	var buf bytes.Buffer

	for _, msg := range res.Logs {
		buf.Write(BuildInfo(0, 1, 10, msg, "LionSQL", "", 1))
	}

	if len(res.Columns) > 0 {
		qr := &engine.QueryResult{
			Columns: res.Columns,
			Rows:    res.Rows,
			Success: true,
		}
		cols := s.inferColumnInfos(qr)
		buf.Write(BuildColMetaData(cols))

		for _, row := range res.Rows {
			sess.writeRowDirect(&buf, cols, row, nil)
		}

		doneStatus := DoneFinal
		if sess.inTx {
			doneStatus |= DoneInXact
		}
		buf.Write(BuildDone(TokenDoneInProc, DoneMore|DoneCount, 0xC1, uint64(len(res.Rows))))
		if extraTokens != nil {
			extraTokens(&buf)
		}
		buf.Write(BuildReturnStatus(0))
		buf.Write(BuildDoneProc(doneStatus, 0xC1, uint64(len(res.Rows))))
	} else {
		if extraTokens != nil {
			extraTokens(&buf)
		}
		rowCount := uint64(len(res.Rows))
		if rowCount == 0 && res.RowsAffected > 0 {
			rowCount = uint64(res.RowsAffected)
		}
		doneStatus := DoneFinal
		if sess.inTx {
			doneStatus |= DoneInXact
		}
		buf.Write(BuildDoneProc(doneStatus|DoneCount, 0xC1, rowCount))
	}

	_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
}

func (s *Server) handleRPCUnprepare(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession) {
	var handle int32
	if len(rpc.Params) > 0 {
		handle = toInt32(rpc.Params[0].Value)
	}

	sess.prepared.Delete(handle)

	var buf bytes.Buffer
	buf.Write(BuildReturnStatus(0))
	buf.Write(BuildDoneProc(DoneFinal, 0, 0))
	_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
}

func (s *Server) handleRPCExecuteSQL(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession, ctx context.Context) {
	if len(rpc.Params) == 0 {
		done := BuildDoneProc(DoneFinal, 0, 0)
		_ = sess.writePacket(conn, PacketTabular, done)
		return
	}

	var stmt string
	var paramsDecl string
	var boundParams []RPCParam

	stmtFound := false
	paramsFound := false

	// First pass: check for explicitly named @stmt and @params
	for _, p := range rpc.Params {
		name := strings.ToLower(strings.TrimSpace(p.Name))
		if name == "@stmt" {
			if str, ok := p.Value.(string); ok {
				stmt = str
				stmtFound = true
			}
		} else if name == "@params" {
			if str, ok := p.Value.(string); ok {
				paramsDecl = str
				paramsFound = true
			}
		}
	}

	// Second pass: ordinal positional assignment
	// In MS SQL Server sp_executesql, parameter 0 is the statement text regardless of
	// whether the client sent an empty name, @stmt, or an arbitrary driver identifier.
	for i, p := range rpc.Params {
		name := strings.ToLower(strings.TrimSpace(p.Name))
		if name == "@stmt" || name == "@params" {
			continue
		}
		if !stmtFound && i == 0 {
			if str, ok := p.Value.(string); ok {
				stmt = str
				stmtFound = true
				continue
			}
		}
		if !paramsFound && i == 1 {
			if str, ok := p.Value.(string); ok {
				paramsDecl = str
				paramsFound = true
				continue
			}
		}
		boundParams = append(boundParams, p)
	}

	execSQL := s.buildBoundSQL(stmt, paramsDecl, boundParams)
	s.executeRPCAndRespond(conn, execSQL, sess, nil, ctx)
}

func (s *Server) executeRPCAndRespond(conn net.Conn, query string, sess *ConnectionSession, extraTokens func(buf *bytes.Buffer), ctx context.Context) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		var buf bytes.Buffer
		if extraTokens != nil {
			extraTokens(&buf)
		}
		buf.Write(BuildReturnStatus(0))
		buf.Write(BuildDoneProc(DoneFinal, 0, 0))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
		return
	}

	// System function / version short-circuit
	if cols, vals, ok := matchVersionQuery(trimmed); ok {
		if s.DevMode() {
			log.Printf("[DEV][TDS][Query] %s", trimmed)
		}
		var buf bytes.Buffer
		buf.Write(BuildColMetaData(cols))
		buf.Write(sess.buildRow(cols, vals))
		doneStatus := DoneFinal
		if sess.inTx {
			doneStatus |= DoneInXact
		}
		buf.Write(BuildDone(TokenDoneInProc, DoneMore|DoneCount, 0xC1, 1))
		if extraTokens != nil {
			extraTokens(&buf)
		}
		buf.Write(BuildReturnStatus(0))
		buf.Write(BuildDoneProc(doneStatus, 0xC1, 1))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
		if s.DevMode() {
			colNames := make([]string, len(cols))
			for i, c := range cols {
				colNames[i] = c.Name
			}
			log.Printf("[DEV][TDS][Success] query=%s cols=%v rows=1", trimmed, colNames)
		}
		return
	}

	if s.DevMode() {
		log.Printf("[DEV][TDS][Query] %s", trimmed)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	res, err := s.coordinator.ExecuteSessionSQLContext(ctx, sess.SessionID, trimmed)
	if err != nil {
		if ctx.Err() != nil {
			// Query canceled via Attention packet; do not write error packet
			return
		}
		if s.DevMode() {
			log.Printf("[DEV][TDS][Error] query=%s err=%v", trimmed, err)
		}
		var buf bytes.Buffer
		num, state, sev, msg := extractTdsErrorInfo(err)
		buf.Write(BuildError(num, state, sev, msg, "LionSQL", "", 1))
		buf.Write(BuildReturnStatus(-1))
		buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
		return
	}
	if s.DevMode() {
		log.Printf("[DEV][TDS][Success] query=%s cols=%v rows=%d duration=%dµs", trimmed, res.Columns, len(res.Rows), res.DurationUS)
	}

	var buf bytes.Buffer

	upperCmd := strings.ToUpper(res.Command)
	if upperCmd == "PRINT" {
		for _, msg := range res.Messages {
			buf.Write(BuildInfo(0, 1, 0, msg, "LionSQL", "", 1))
		}
	}
	if upperCmd == "BEGIN" {
		sess.inTx = true
		sess.txDescriptor = atomic.AddUint64(&s.txCounter, 1)
		buf.Write(BuildEnvChangeBeginTransaction(sess.txDescriptor))
	} else if upperCmd == "COMMIT" {
		if sess.inTx {
			buf.Write(BuildEnvChangeCommitTransaction(sess.txDescriptor))
			sess.inTx = false
			sess.txDescriptor = 0
		}
	} else if upperCmd == "ROLLBACK" {
		if sess.inTx {
			buf.Write(BuildEnvChangeRollbackTransaction(sess.txDescriptor))
			sess.inTx = false
			sess.txDescriptor = 0
		}
	}

	doneStatus := DoneFinal
	if sess.inTx {
		doneStatus |= DoneInXact
	}

	if len(res.Columns) > 0 {
		cols := s.inferColumnInfos(res)
		buf.Write(BuildColMetaData(cols))

		scratchVals := make([]any, len(res.Columns))
		for _, row := range res.Rows {
			sess.writeRowDirect(&buf, cols, row, scratchVals)
		}

		buf.Write(BuildDone(TokenDoneInProc, DoneMore|DoneCount, 0xC1, uint64(len(res.Rows))))
		if extraTokens != nil {
			extraTokens(&buf)
		}
		buf.Write(BuildReturnStatus(0))
		buf.Write(BuildDoneProc(doneStatus, 0xC1, uint64(len(res.Rows))))
	} else {
		cmdCode := uint16(0)
		switch upperCmd {
		case "SELECT":
			cmdCode = 0xC1
		case "INSERT":
			cmdCode = 0xC3
		case "DELETE":
			cmdCode = 0xC4
		case "UPDATE":
			cmdCode = 0xC5
		default:
			cmdCode = 0x00
		}
		if extraTokens != nil {
			extraTokens(&buf)
		}
		buf.Write(BuildReturnStatus(0))
		buf.Write(BuildDoneProc(doneStatus|DoneCount, cmdCode, uint64(res.RowsAffected)))
	}

	_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
}

func isIdentByte(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '#' || ch == '$'
}

func (s *Server) buildBoundSQL(query string, paramsDecl string, params []RPCParam) string {
	query = strings.TrimSpace(query)
	query = replaceQuestionPlaceholders(query)
	if len(params) == 0 && strings.TrimSpace(paramsDecl) == "" {
		return query
	}

	decls := parseParamDeclarations(paramsDecl)
	usedParams := make(map[int]bool)
	paramBindings := make(map[string]string)

	// 1. Bind parameters declared in paramsDecl
	for i, decl := range decls {
		cleanName := strings.TrimPrefix(decl.Name, "@")
		var val any = nil
		found := false

		for pIdx, p := range params {
			pClean := strings.TrimPrefix(p.Name, "@")
			if strings.EqualFold(cleanName, pClean) {
				val = p.Value
				found = true
				usedParams[pIdx] = true
				break
			}
		}

		if !found && i < len(params) && !usedParams[i] {
			val = params[i].Value
			found = true
			usedParams[i] = true
		}

		key := strings.ToLower(decl.Name)
		if !strings.HasPrefix(key, "@") {
			key = "@" + key
		}
		paramBindings[key] = formatSQLValue(val)
	}

	// 2. Bind any remaining parameters that weren't in paramsDecl
	for pIdx, p := range params {
		if usedParams[pIdx] {
			continue
		}
		paramName := p.Name
		if paramName == "" {
			paramName = fmt.Sprintf("@P%d", pIdx+1)
		}
		if !strings.HasPrefix(paramName, "@") {
			paramName = "@" + paramName
		}
		paramBindings[strings.ToLower(paramName)] = formatSQLValue(p.Value)
	}

	if len(paramBindings) == 0 {
		return query
	}

	return substituteParamsSinglePass(query, paramBindings)
}

func substituteParamsSinglePass(query string, bindings map[string]string) string {
	n := len(query)
	if n == 0 {
		return ""
	}

	var b strings.Builder
	b.Grow(n + len(bindings)*16)

	inString := false
	var quoteChar byte

	for i := 0; i < n; {
		ch := query[i]

		if inString {
			b.WriteByte(ch)
			if ch == quoteChar {
				// Handle escaped quote: '' or ""
				if i+1 < n && query[i+1] == quoteChar {
					b.WriteByte(query[i+1])
					i += 2
					continue
				}
				inString = false
			}
			i++
			continue
		}

		// String literal start
		if ch == '\'' || ch == '"' {
			inString = true
			quoteChar = ch
			b.WriteByte(ch)
			i++
			continue
		}

		// Line comment (-- ...)
		if ch == '-' && i+1 < n && query[i+1] == '-' {
			b.WriteByte(ch)
			b.WriteByte(query[i+1])
			i += 2
			for i < n && query[i] != '\n' {
				b.WriteByte(query[i])
				i++
			}
			continue
		}

		// Block comment (/* ... */)
		if ch == '/' && i+1 < n && query[i+1] == '*' {
			b.WriteByte(ch)
			b.WriteByte(query[i+1])
			i += 2
			for i < n {
				if query[i] == '*' && i+1 < n && query[i+1] == '/' {
					b.WriteByte('*')
					b.WriteByte('/')
					i += 2
					break
				}
				b.WriteByte(query[i])
				i++
			}
			continue
		}

		// Parameter substitution: @ident or @@ident
		if ch == '@' {
			isDoubleAt := i+1 < n && query[i+1] == '@'
			paramStart := i
			i++
			if isDoubleAt {
				i++
			}
			for i < n && isIdentByte(query[i]) {
				i++
			}
			ident := query[paramStart:i]
			lowerIdent := strings.ToLower(ident)

			if val, ok := bindings[lowerIdent]; ok {
				b.WriteString(val)
			} else {
				b.WriteString(ident)
			}
			continue
		}

		b.WriteByte(ch)
		i++
	}

	return b.String()
}

func substituteParamOutsideQuotes(query string, paramName string, formattedVal string) string {
	if !strings.HasPrefix(paramName, "@") {
		paramName = "@" + paramName
	}
	return substituteParamsSinglePass(query, map[string]string{strings.ToLower(paramName): formattedVal})
}

func parseParamDeclarations(decl string) []struct{ Name, Type string } {
	var res []struct{ Name, Type string }
	decl = strings.TrimSpace(decl)
	if decl == "" {
		return res
	}

	parts := splitDeclParts(decl)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		fields := strings.Fields(p)
		if len(fields) >= 2 {
			name := fields[0]
			typeName := strings.Join(fields[1:], " ")
			res = append(res, struct{ Name, Type string }{Name: name, Type: typeName})
		}
	}
	return res
}

func splitDeclParts(s string) []string {
	var parts []string
	var cur strings.Builder
	parenDepth := 0
	for _, r := range s {
		switch r {
		case '(':
			parenDepth++
			cur.WriteRune(r)
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
			cur.WriteRune(r)
		case ',':
			if parenDepth == 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			} else {
				cur.WriteRune(r)
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func formatSQLValue(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	case []byte:
		return "0x" + hex.EncodeToString(val)
	case bool:
		if val {
			return "1"
		}
		return "0"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", val)
	case float32, float64:
		return fmt.Sprintf("%g", val)
	case time.Time:
		return "'" + val.Format("2006-01-02 15:04:05.999") + "'"
	default:
		return fmt.Sprintf("'%v'", val)
	}
}

func inferSQLType(v any) string {
	if v == nil {
		return "NVARCHAR(MAX)"
	}
	switch v.(type) {
	case int, int32:
		return "INT"
	case int64:
		return "BIGINT"
	case int16:
		return "SMALLINT"
	case int8, uint8:
		return "TINYINT"
	case float32:
		return "REAL"
	case float64:
		return "FLOAT"
	case bool:
		return "BIT"
	case time.Time:
		return "DATETIME2"
	case []byte:
		return "VARBINARY(MAX)"
	default:
		return "NVARCHAR(MAX)"
	}
}

func replaceQuestionPlaceholders(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	paramIdx := 1
	inString := false
	var quoteChar rune

	runes := []rune(query)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if inString {
			b.WriteRune(ch)
			if ch == quoteChar {
				if i+1 < len(runes) && runes[i+1] == quoteChar {
					b.WriteRune(runes[i+1])
					i++
				} else {
					inString = false
				}
			}
		} else {
			if ch == '\'' || ch == '"' {
				inString = true
				quoteChar = ch
				b.WriteRune(ch)
			} else if ch == '?' {
				b.WriteString(fmt.Sprintf("@P%d", paramIdx))
				paramIdx++
			} else {
				b.WriteRune(ch)
			}
		}
	}
	return b.String()
}

func toInt32(v any) int32 {
	switch val := v.(type) {
	case int32:
		return val
	case int:
		return int32(val)
	case int64:
		return int32(val)
	case int16:
		return int32(val)
	case int8:
		return int32(val)
	case uint32:
		return int32(val)
	case uint64:
		return int32(val)
	case uint:
		return int32(val)
	default:
		return 0
	}
}

func parseRPCRequest(payload []byte) (*parsedRPC, error) {
	if len(payload) < 2 {
		return nil, io.ErrUnexpectedEOF
	}

	r := bytes.NewReader(payload)

	// Check for ALL_HEADERS in RPC
	if len(payload) >= 4 {
		var totalHeadersLen uint32
		if err := binary.Read(r, binary.LittleEndian, &totalHeadersLen); err == nil && totalHeadersLen >= 4 && int(totalHeadersLen) < len(payload) {
			_, _ = r.Seek(int64(totalHeadersLen), io.SeekStart)
		} else {
			_, _ = r.Seek(0, io.SeekStart)
		}
	}

	var nameLen uint16
	if err := binary.Read(r, binary.LittleEndian, &nameLen); err != nil {
		return nil, err
	}

	res := &parsedRPC{}
	if nameLen == 0xFFFF {
		if err := binary.Read(r, binary.LittleEndian, &res.ProcID); err != nil {
			return nil, err
		}
	} else if nameLen > 0 {
		nameBytes := make([]byte, int(nameLen)*2)
		if _, err := io.ReadFull(r, nameBytes); err != nil {
			return nil, err
		}
		res.ProcName = DecodeUTF16LE(nameBytes)
	}

	_ = binary.Read(r, binary.LittleEndian, &res.OptionFlags)

	for r.Len() > 0 {
		param, err := readRPCParam(r)
		if err != nil {
			break
		}
		res.Params = append(res.Params, *param)
	}

	return res, nil
}

func readRPCParam(r *bytes.Reader) (*RPCParam, error) {
	paramName, err := ReadBVarChar(r)
	if err != nil {
		return nil, err
	}

	statusFlag, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	isOutput := (statusFlag & 0x01) != 0

	typeByte, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	val, err := readRPCValue(r, typeByte)
	if err != nil {
		return nil, err
	}

	return &RPCParam{
		Name:     paramName,
		IsOutput: isOutput,
		Type:     typeByte,
		Value:    val,
	}, nil
}

func readRPCValue(r *bytes.Reader, typeByte byte) (any, error) {
	switch typeByte {
	case TypeNull:
		return nil, nil

	case TypeInt1:
		b, err := r.ReadByte()
		return int8(b), err

	case TypeInt2:
		var v int16
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err

	case TypeInt4:
		var v int32
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err

	case TypeInt8:
		var v int64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err

	case TypeIntN:
		_, err := r.ReadByte() // maxLen
		if err != nil {
			return nil, err
		}
		actualLen, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		switch actualLen {
		case 0:
			return nil, nil
		case 1:
			b, err := r.ReadByte()
			return int8(b), err
		case 2:
			var v int16
			err := binary.Read(r, binary.LittleEndian, &v)
			return v, err
		case 4:
			var v int32
			err := binary.Read(r, binary.LittleEndian, &v)
			return v, err
		case 8:
			var v int64
			err := binary.Read(r, binary.LittleEndian, &v)
			return v, err
		default:
			buf := make([]byte, actualLen)
			_, err := io.ReadFull(r, buf)
			return nil, err
		}

	case TypeBit:
		b, err := r.ReadByte()
		return b != 0, err

	case TypeBitN:
		_, _ = r.ReadByte() // maxLen
		actualLen, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if actualLen == 0 {
			return nil, nil
		}
		b, err := r.ReadByte()
		return b != 0, err

	case TypeFlt4:
		var v float32
		err := binary.Read(r, binary.LittleEndian, &v)
		return float64(v), err

	case TypeFlt8:
		var v float64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err

	case TypeFltN:
		_, _ = r.ReadByte() // maxLen
		actualLen, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		switch actualLen {
		case 0:
			return nil, nil
		case 4:
			var v float32
			err := binary.Read(r, binary.LittleEndian, &v)
			return float64(v), err
		case 8:
			var v float64
			err := binary.Read(r, binary.LittleEndian, &v)
			return v, err
		default:
			buf := make([]byte, actualLen)
			_, err := io.ReadFull(r, buf)
			return nil, err
		}

	case TypeVarChar: // 0xA7 (also TypeBigVarChar)
		var maxLen uint16
		_ = binary.Read(r, binary.LittleEndian, &maxLen)
		if maxLen == 0xFFFF {
			var plpLen uint64
			if err := binary.Read(r, binary.LittleEndian, &plpLen); err != nil {
				return nil, err
			}
			if plpLen == 0xFFFFFFFFFFFFFFFF {
				return nil, nil
			}
			var plpBuf bytes.Buffer
			for {
				var chunkLen uint32
				if err := binary.Read(r, binary.LittleEndian, &chunkLen); err != nil {
					return nil, err
				}
				if chunkLen == 0 {
					break
				}
				chunk := make([]byte, chunkLen)
				if _, err := io.ReadFull(r, chunk); err != nil {
					return nil, err
				}
				plpBuf.Write(chunk)
			}
			return plpBuf.String(), nil
		}
		var collation [5]byte
		_, _ = io.ReadFull(r, collation[:])
		var valLen uint16
		if err := binary.Read(r, binary.LittleEndian, &valLen); err != nil {
			return nil, err
		}
		if valLen == 0xFFFF {
			return nil, nil
		}
		buf := make([]byte, valLen)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf), nil

	case TypeNVarChar: // 0xE7
		var maxLen uint16
		_ = binary.Read(r, binary.LittleEndian, &maxLen)
		if maxLen == 0xFFFF {
			var plpLen uint64
			if err := binary.Read(r, binary.LittleEndian, &plpLen); err != nil {
				return nil, err
			}
			if plpLen == 0xFFFFFFFFFFFFFFFF {
				return nil, nil
			}
			var plpBuf bytes.Buffer
			for {
				var chunkLen uint32
				if err := binary.Read(r, binary.LittleEndian, &chunkLen); err != nil {
					return nil, err
				}
				if chunkLen == 0 {
					break
				}
				chunk := make([]byte, chunkLen)
				if _, err := io.ReadFull(r, chunk); err != nil {
					return nil, err
				}
				plpBuf.Write(chunk)
			}
			return DecodeUTF16LE(plpBuf.Bytes()), nil
		}
		var collation [5]byte
		_, _ = io.ReadFull(r, collation[:])
		var valLen uint16
		if err := binary.Read(r, binary.LittleEndian, &valLen); err != nil {
			return nil, err
		}
		if valLen == 0xFFFF {
			return nil, nil
		}
		buf := make([]byte, valLen)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return DecodeUTF16LE(buf), nil

	case TypeBigBinary, TypeBigVarBin: // 0xAD, 0xA5
		var maxLen uint16
		_ = binary.Read(r, binary.LittleEndian, &maxLen)
		var valLen uint16
		if err := binary.Read(r, binary.LittleEndian, &valLen); err != nil {
			return nil, err
		}
		if valLen == 0xFFFF {
			return nil, nil
		}
		buf := make([]byte, valLen)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return buf, nil

	default:
		return nil, nil
	}
}

func (s *Server) handleAttention(conn net.Conn, pkt *Packet, sess *ConnectionSession) {
	done := BuildDone(TokenDone, DoneFinal|DoneAttn, 0, 0)
	_ = sess.writePacket(conn, PacketTabular, done)
}

func (s *Server) handleTransactionManager(conn net.Conn, pkt *Packet, sess *ConnectionSession) {
	payload := pkt.Payload
	if len(payload) < 4 {
		done := BuildDone(TokenDone, DoneFinal, 0, 0)
		_ = sess.writePacket(conn, PacketTabular, done)
		return
	}

	body := payload
	totalHeadersLen := binary.LittleEndian.Uint32(payload[:4])
	if totalHeadersLen >= 4 && int(totalHeadersLen) < len(payload) {
		body = payload[totalHeadersLen:]
	}

	if len(body) < 2 {
		done := BuildDone(TokenDone, DoneFinal, 0, 0)
		_ = sess.writePacket(conn, PacketTabular, done)
		return
	}

	rqType := binary.LittleEndian.Uint16(body[:2])
	switch rqType {
	case 5: // tmBeginXact
		_, err := s.coordinator.ExecuteSessionSQL(sess.SessionID, "BEGIN TRANSACTION")
		if err != nil {
			var buf bytes.Buffer
			buf.Write(BuildError(50000, 1, 16, err.Error(), "LionSQL", "", 1))
			buf.Write(BuildDone(TokenDone, DoneFinal|DoneError, 0, 0))
			_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
			return
		}
		sess.inTx = true
		sess.txDescriptor = atomic.AddUint64(&s.txCounter, 1)

		var buf bytes.Buffer
		buf.Write(BuildEnvChangeBeginTransaction(sess.txDescriptor))
		buf.Write(BuildDone(TokenDone, DoneFinal|DoneInXact, 0, 0))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())

	case 7: // tmCommitXact
		txDesc := sess.txDescriptor
		sess.inTx = false
		sess.txDescriptor = 0
		_, err := s.coordinator.ExecuteSessionSQL(sess.SessionID, "COMMIT TRANSACTION")
		if err != nil {
			var buf bytes.Buffer
			buf.Write(BuildError(50000, 1, 16, err.Error(), "LionSQL", "", 1))
			buf.Write(BuildDone(TokenDone, DoneFinal|DoneError, 0, 0))
			_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
			return
		}

		var buf bytes.Buffer
		buf.Write(BuildEnvChangeCommitTransaction(txDesc))
		buf.Write(BuildDone(TokenDone, DoneFinal, 0, 0))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())

	case 8: // tmRollbackXact
		txDesc := sess.txDescriptor
		sess.inTx = false
		sess.txDescriptor = 0
		_, err := s.coordinator.ExecuteSessionSQL(sess.SessionID, "ROLLBACK TRANSACTION")
		if err != nil {
			var buf bytes.Buffer
			buf.Write(BuildError(50000, 1, 16, err.Error(), "LionSQL", "", 1))
			buf.Write(BuildDone(TokenDone, DoneFinal|DoneError, 0, 0))
			_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
			return
		}

		var buf bytes.Buffer
		buf.Write(BuildEnvChangeRollbackTransaction(txDesc))
		buf.Write(BuildDone(TokenDone, DoneFinal, 0, 0))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())

	default:
		done := BuildDone(TokenDone, DoneFinal, 0, 0)
		_ = sess.writePacket(conn, PacketTabular, done)
	}
}

func (s *Server) executeQueryAndRespond(conn net.Conn, query string, sess *ConnectionSession, ctx context.Context) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		done := BuildDone(TokenDone, DoneFinal, 0, 0)
		_ = sess.writePacket(conn, PacketTabular, done)
		return
	}

	// System function / version short-circuit
	if cols, vals, ok := matchVersionQuery(trimmed); ok {
		if s.DevMode() {
			log.Printf("[DEV][TDS][Query] %s", trimmed)
		}
		var buf bytes.Buffer
		buf.Write(BuildColMetaData(cols))
		buf.Write(sess.buildRow(cols, vals))
		doneStatus := DoneFinal
		if sess.inTx {
			doneStatus |= DoneInXact
		}
		buf.Write(BuildDone(TokenDone, doneStatus|DoneCount, 0xC1, 1))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
		if s.DevMode() {
			colNames := make([]string, len(cols))
			for i, c := range cols {
				colNames[i] = c.Name
			}
			log.Printf("[DEV][TDS][Success] query=%s cols=%v rows=1", trimmed, colNames)
		}
		return
	}

	if s.DevMode() {
		log.Printf("[DEV][TDS][Query] %s", trimmed)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	res, err := s.coordinator.ExecuteSessionSQLContext(ctx, sess.SessionID, trimmed)
	if err != nil {
		if ctx.Err() != nil {
			// Query canceled via Attention packet; do not write error packet
			return
		}
		if s.DevMode() {
			log.Printf("[DEV][TDS][Error] query=%s err=%v", trimmed, err)
		}
		var buf bytes.Buffer
		num, state, sev, msg := extractTdsErrorInfo(err)
		buf.Write(BuildError(num, state, sev, msg, "LionSQL", "", 1))
		buf.Write(BuildDone(TokenDone, DoneFinal|DoneError, 0, 0))
		_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
		return
	}
	if s.DevMode() {
		log.Printf("[DEV][TDS][Success] query=%s cols=%v rows=%d duration=%dµs", trimmed, res.Columns, len(res.Rows), res.DurationUS)
	}

	var buf bytes.Buffer

	upperCmd := strings.ToUpper(res.Command)
	if upperCmd == "PRINT" {
		for _, msg := range res.Messages {
			buf.Write(BuildInfo(0, 1, 0, msg, "LionSQL", "", 1))
		}
	}
	if upperCmd == "USE" {
		newDB := "master"
		if len(res.Messages) > 0 && strings.HasPrefix(res.Messages[0], "Database context set to ") {
			newDB = strings.TrimSuffix(strings.TrimPrefix(res.Messages[0], "Database context set to "), ".")
		}
		oldDB := sess.Database
		if oldDB == "" {
			oldDB = "master"
		}
		sess.Database = newDB
		buf.Write(BuildEnvChangeDatabase(newDB, oldDB))
	}
	if upperCmd == "BEGIN" {
		sess.inTx = true
		sess.txDescriptor = atomic.AddUint64(&s.txCounter, 1)
		buf.Write(BuildEnvChangeBeginTransaction(sess.txDescriptor))
	} else if upperCmd == "COMMIT" {
		if sess.inTx {
			buf.Write(BuildEnvChangeCommitTransaction(sess.txDescriptor))
			sess.inTx = false
			sess.txDescriptor = 0
		}
	} else if upperCmd == "ROLLBACK" {
		if sess.inTx {
			buf.Write(BuildEnvChangeRollbackTransaction(sess.txDescriptor))
			sess.inTx = false
			sess.txDescriptor = 0
		}
	}

	doneStatus := DoneFinal
	if sess.inTx {
		doneStatus |= DoneInXact
	}

	if len(res.Columns) > 0 {
		cols := s.inferColumnInfos(res)
		buf.Write(BuildColMetaData(cols))

		scratchVals := make([]any, len(res.Columns))
		for _, row := range res.Rows {
			sess.writeRowDirect(&buf, cols, row, scratchVals)
		}

		buf.Write(BuildDone(TokenDone, doneStatus|DoneCount, 0xC1, uint64(len(res.Rows))))
	} else {
		cmdCode := uint16(0)
		switch upperCmd {
		case "SELECT":
			cmdCode = 0xC1
		case "INSERT":
			cmdCode = 0xC3
		case "DELETE":
			cmdCode = 0xC4
		case "UPDATE":
			cmdCode = 0xC5
		default:
			cmdCode = 0x00
		}
		buf.Write(BuildDone(TokenDone, doneStatus|DoneCount, cmdCode, uint64(res.RowsAffected)))
	}

	_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
}

func (s *Server) inferColumnInfos(res *engine.QueryResult) []ColumnInfo {
	cols := make([]ColumnInfo, len(res.Columns))

	for i, colName := range res.Columns {
		colType := TypeNVarChar
		colSize := 4000
		scale := 0
		nullable := true

		lowerCol := strings.ToLower(colName)
		if lowerCol == "create_date" || lowerCol == "modify_date" || lowerCol == "createdate" || lowerCol == "modifydate" {
			colType = TypeDateTimeN
			colSize = 8
		} else if strings.Contains(lowerCol, "datetimeoffset") || strings.Contains(lowerCol, "offset") {
			colType = TypeDateTimeOffsetN
			colSize = 10
			scale = 7
		} else if strings.Contains(lowerCol, "datetime2") {
			colType = TypeDateTime2N
			colSize = 8
			scale = 7
		} else if lowerCol == "getdate" || lowerCol == "now" || lowerCol == "current_timestamp" || lowerCol == "getutcdate" || lowerCol == "sysdatetime" || lowerCol == "sysutcdatetime" || strings.HasSuffix(lowerCol, "datetime") || strings.HasSuffix(lowerCol, "timestamp") {
			colType = TypeDateTimeN
			colSize = 8
		} else if strings.HasSuffix(lowerCol, "date") {
			colType = TypeDateN
			colSize = 3
		} else if lowerCol == "time" || strings.HasSuffix(lowerCol, "_time") || strings.HasSuffix(lowerCol, "time_only") {
			colType = TypeTimeN
			colSize = 5
			scale = 7
		} else if strings.HasSuffix(lowerCol, "time") || lowerCol == "created" || lowerCol == "modified" {
			colType = TypeDateTimeN
			colSize = 8
		} else if strings.HasSuffix(lowerCol, "guid") || strings.HasSuffix(lowerCol, "uuid") || lowerCol == "uniqueidentifier" {
			colType = TypeGuid
			colSize = 16
		} else if lowerCol == "object_id" || lowerCol == "objectid" || lowerCol == "column_id" || lowerCol == "columnid" || lowerCol == "schema_id" || lowerCol == "schemaid" || lowerCol == "parent_object_id" || lowerCol == "parentobjectid" || lowerCol == "principal_id" || lowerCol == "principalid" || lowerCol == "index_id" || lowerCol == "indexid" {
			colType = TypeIntN
			colSize = 4
		} else if lowerCol == "type" {
			colType = TypeVarChar
			colSize = 2
		}

		// Scan non-null sample values from rows
		for _, row := range res.Rows {
			if v, ok := row[colName]; ok && v != nil {
				switch val := v.(type) {
				case int, int64, int32, int16, int8:
					colType = TypeIntN
					colSize = 8
					if lowerCol == "object_id" || lowerCol == "objectid" || lowerCol == "column_id" || lowerCol == "columnid" || lowerCol == "schema_id" || lowerCol == "schemaid" || lowerCol == "parent_object_id" || lowerCol == "parentobjectid" || lowerCol == "principal_id" || lowerCol == "principalid" || lowerCol == "index_id" || lowerCol == "indexid" {
						colSize = 4
					}
				case float64, float32:
					colType = TypeFltN
					colSize = 8
				case bool:
					colType = TypeBitN
					colSize = 1
				case time.Time:
					if strings.Contains(lowerCol, "datetimeoffset") || strings.Contains(lowerCol, "offset") {
						colType = TypeDateTimeOffsetN
						colSize = 10
						scale = 7
					} else if strings.Contains(lowerCol, "datetime2") {
						colType = TypeDateTime2N
						colSize = 8
						scale = 7
					} else if lowerCol == "time" || strings.HasSuffix(lowerCol, "_time") || strings.HasSuffix(lowerCol, "time_only") {
						colType = TypeTimeN
						colSize = 5
						scale = 7
					} else if lowerCol == "getdate" || lowerCol == "now" || lowerCol == "current_timestamp" || lowerCol == "getutcdate" || lowerCol == "sysdatetime" || lowerCol == "sysutcdatetime" || lowerCol == "create_date" || lowerCol == "modify_date" || lowerCol == "createdate" || lowerCol == "modifydate" || strings.HasSuffix(lowerCol, "datetime") || strings.HasSuffix(lowerCol, "timestamp") || lowerCol == "created" || lowerCol == "modified" {
						colType = TypeDateTimeN
						colSize = 8
					} else if strings.HasSuffix(lowerCol, "date") {
						colType = TypeDateN
						colSize = 3
					} else {
						colType = TypeDateTimeN
						colSize = 8
					}
				case string:
					if isUUIDString(val) {
						colType = TypeGuid
						colSize = 16
					} else if isDateString(val) && lowerCol != "create_date" && lowerCol != "modify_date" && lowerCol != "createdate" && lowerCol != "modifydate" {
						colType = TypeDateN
						colSize = 3
					} else if isTimeString(val) {
						colType = TypeTimeN
						colSize = 5
						scale = 7
					} else if (strings.Contains(lowerCol, "datetimeoffset") || strings.Contains(lowerCol, "offset")) && (strings.Contains(val, "+") || strings.Contains(val, "-")) {
						colType = TypeDateTimeOffsetN
						colSize = 10
						scale = 7
					} else if lowerCol == "type" {
						colType = TypeVarChar
						colSize = 2
					} else if colType != TypeDateTimeN && colType != TypeDateTime2N && colType != TypeDateN && colType != TypeTimeN && colType != TypeDateTimeOffsetN && colType != TypeGuid {
						if len(val) > 4000 || strings.Contains(lowerCol, "max") || strings.Contains(lowerCol, "lob") {
							colType = TypeNVarChar
							colSize = -1
						} else {
							colType = TypeNVarChar
							colSize = 4000
						}
					}
				case []byte:
					if len(val) > 8000 || strings.Contains(lowerCol, "max") || strings.Contains(lowerCol, "lob") {
						colType = TypeBigVarBin
						colSize = -1
					} else {
						colType = TypeBigVarBin
						colSize = len(val)
					}
				}
				break
			}
		}

		cols[i] = ColumnInfo{
			Name:     colName,
			Type:     colType,
			Size:     colSize,
			Scale:    scale,
			Nullable: nullable,
			IsPLP:    colSize == -1,
		}
	}

	return cols
}

func isUUIDString(s string) bool {
	cleaned := strings.Trim(strings.TrimSpace(s), "'\"{}")
	if len(cleaned) == 36 && cleaned[8] == '-' && cleaned[13] == '-' && cleaned[18] == '-' && cleaned[23] == '-' {
		for i, c := range cleaned {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				continue
			}
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
		return true
	}
	return false
}

func isDateString(s string) bool {
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		for i, c := range s {
			if i == 4 || i == 7 {
				continue
			}
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func isTimeString(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) >= 8 && s[2] == ':' && s[5] == ':' {
		for i, c := range s {
			if i == 2 || i == 5 || (i == 8 && c == '.') {
				continue
			}
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func matchVersionQuery(trimmed string) ([]ColumnInfo, []any, bool) {
	upper := strings.ToUpper(trimmed)
	if !strings.Contains(upper, "SELECT") {
		return nil, nil, false
	}

	// 1. DbGate 4-column version & edition detection query (requires @@VERSION, PRODUCTVERSION, ENGINEEDITION, and VERSIONTEXT)
	if strings.Contains(upper, "@@VERSION") && strings.Contains(upper, "PRODUCTVERSION") && strings.Contains(upper, "ENGINEEDITION") && strings.Contains(upper, "VERSIONTEXT") {
		cols := []ColumnInfo{
			{Name: "version", Type: TypeNVarChar, Size: 4000, Nullable: false},
			{Name: "productVersion", Type: TypeNVarChar, Size: 128, Nullable: false},
			{Name: "engineEdition", Type: TypeIntN, Size: 4, Nullable: true},
			{Name: "versionText", Type: TypeNVarChar, Size: 128, Nullable: false},
		}
		vals := []any{
			"Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) - LionSQL Engine",
			"15.0.2000.5",
			int32(3),
			"SQL Server 2019",
		}
		return cols, vals, true
	}

	// 2. Standard standalone single @@VERSION probe
	if strings.Contains(upper, "@@VERSION") && !strings.Contains(upper, ",") && !strings.Contains(upper, "FROM") {
		colName := "version"
		if asIdx := strings.Index(upper, " AS "); asIdx != -1 {
			alias := strings.TrimSpace(trimmed[asIdx+4:])
			if commaIdx := strings.Index(alias, ","); commaIdx != -1 {
				alias = alias[:commaIdx]
			}
			alias = strings.Trim(alias, ";, []\"'\r\n\t")
			if alias != "" {
				colName = alias
			}
		}
		cols := []ColumnInfo{
			{Name: colName, Type: TypeNVarChar, Size: 4000, Nullable: false},
		}
		vals := []any{
			"Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) - LionSQL Engine",
		}
		return cols, vals, true
	}

	// 3. Standalone single-expression SERVERPROPERTY(...) probe only
	if strings.Contains(upper, "SERVERPROPERTY") && !strings.Contains(upper, ",") && !strings.Contains(upper, "FROM") {
		colName := "property"
		if asIdx := strings.Index(upper, " AS "); asIdx != -1 {
			alias := strings.TrimSpace(trimmed[asIdx+4:])
			if commaIdx := strings.Index(alias, ","); commaIdx != -1 {
				alias = alias[:commaIdx]
			}
			alias = strings.Trim(alias, ";, []\"'\r\n\t")
			if alias != "" {
				colName = alias
			}
		} else if m := regexp.MustCompile(`(?i)SERVERPROPERTY\s*\(\s*['"]?([a-zA-Z0-9_]+)['"]?\s*\)`).FindStringSubmatch(trimmed); len(m) == 2 {
			colName = m[1]
		}
		var val any = "15.0.2000.5"
		colType := TypeNVarChar
		colSize := 128
		if strings.Contains(upper, "ENGINEEDITION") {
			val = int32(3)
			colType = TypeIntN
			colSize = 4
		} else if strings.Contains(upper, "COLLATION") {
			val = "SQL_Latin1_General_CP1_CI_AS"
		} else if strings.Contains(upper, "EDITION") {
			val = "Developer Edition (64-bit)"
		} else if strings.Contains(upper, "PRODUCTLEVEL") {
			val = "RTM"
		}
		cols := []ColumnInfo{
			{Name: colName, Type: colType, Size: colSize, Nullable: true},
		}
		vals := []any{val}
		return cols, vals, true
	}

	return nil, nil, false
}

// tdsErrorDescriber is an interface implemented by errors providing structured TDS error information.
type tdsErrorDescriber interface {
	TdsErrorInfo() (int32, byte, byte)
}

// extractTdsErrorInfo extracts TDS error fields: number, state, severity (class), and message.
func extractTdsErrorInfo(err error) (int32, byte, byte, string) {
	if err == nil {
		return 0, 0, 0, ""
	}
	var desc tdsErrorDescriber
	if errors.As(err, &desc) {
		num, sev, state := desc.TdsErrorInfo()
		return num, state, sev, err.Error()
	}
	return 50000, 1, 16, err.Error()
}
