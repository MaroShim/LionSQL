package tds

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"regexp"
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

// Server implements a native Microsoft SQL Server TDS 7.x/8.0 wire protocol listener.
type Server struct {
	addr        string
	listener    net.Listener
	engine      storage.Engine
	catalog     *catalog.Catalog
	apiServer   *api.Server
	planCache   *proc.PlanCache
	executor    *runtime.Executor
	spidCounter uint32
	mu          sync.Mutex
	closed      bool
	wg          sync.WaitGroup
}

// NewServer creates a new TDS wire protocol server.
func NewServer(addr string, cat *catalog.Catalog, eng storage.Engine) *Server {
	cache := runtime.NewPlanCache()
	return &Server{
		addr:        addr,
		catalog:     cat,
		engine:      eng,
		apiServer:   api.NewServer(cat, eng),
		planCache:   proc.NewPlanCache(),
		executor:    runtime.NewExecutor(cache),
		spidCounter: 50, // Standard MSSQL user SPIDs begin at 51
	}
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

// Close gracefully terminates the listener and waits for active connections.
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
	s.mu.Unlock()

	s.wg.Wait()
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

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleConnection(c)
		}(conn)
	}
}

// TdsPreparedStatement stores metadata for a prepared TDS statement.
type TdsPreparedStatement struct {
	Handle int32
	Params string
	Query  string
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
	SPID        uint16
	SessionID   string
	HostName    string
	UserName    string
	AppName     string
	Database    string
	PacketSize  uint32
	prepMu      sync.Mutex
	prepCounter int32
	prepared    map[int32]*TdsPreparedStatement
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	spid := uint16(atomic.AddUint32(&s.spidCounter, 1))
	sess := &ConnectionSession{
		SPID:       spid,
		SessionID:  fmt.Sprintf("tds-%d", spid),
		Database:   "master",
		PacketSize: DefaultPacketSize,
		prepared:   make(map[int32]*TdsPreparedStatement),
	}
	defer s.apiServer.CloseSession(sess.SessionID)

	for {
		pkt, err := ReadPacket(conn)
		if err != nil {
			return
		}

		switch pkt.Type {
		case PacketPrelogin:
			s.handlePrelogin(conn, pkt, sess)

		case PacketLogin7:
			s.handleLogin7(conn, pkt, sess)

		case PacketSQLBatch:
			s.handleSQLBatch(conn, pkt, sess)

		case PacketRPC:
			s.handleRPC(conn, pkt, sess)

		case PacketAttention:
			s.handleAttention(conn, pkt, sess)

		default:
			// Fallback: send simple DONE
			doneBytes := BuildDone(TokenDone, DoneFinal, 0, 0)
			_ = WritePacket(conn, PacketTabular, sess.SPID, doneBytes)
		}
	}
}

func (s *Server) handlePrelogin(conn net.Conn, pkt *Packet, sess *ConnectionSession) {
	// Respond with PRELOGIN options: Version, Encryption (EncryptNotSup), MARS (0)
	respPayload := BuildPreloginResponse(EncryptNotSup)
	_ = WritePacket(conn, PacketTabular, sess.SPID, respPayload)
}

func (s *Server) handleLogin7(conn net.Conn, pkt *Packet, sess *ConnectionSession) {
	s.parseLogin7(pkt.Payload, sess)

	var resp bytes.Buffer

	// 1. LOGINACK token (0xAD)
	loginAck := BuildLoginAck("SQLion", 15, 0, 2000)
	resp.Write(loginAck)

	// 2. ENVCHANGE for Database (0xE3)
	envDB := BuildEnvChangeDatabase(sess.Database, "master")
	resp.Write(envDB)

	// 3. ENVCHANGE for PacketSize (0xE3)
	pktSizeStr := fmt.Sprintf("%d", sess.PacketSize)
	envPkt := BuildEnvChangePacketSize(pktSizeStr, "4096")
	resp.Write(envPkt)

	// 4. DONE token (0xFD)
	done := BuildDone(TokenDone, DoneFinal, 0, 0)
	resp.Write(done)

	_ = WritePacket(conn, PacketTabular, sess.SPID, resp.Bytes())
}

func (s *Server) parseLogin7(payload []byte, sess *ConnectionSession) {
	if len(payload) < 94 {
		return
	}

	// Packet Size requested by client
	packetSize := binary.LittleEndian.Uint32(payload[8:12])
	if packetSize >= 512 && packetSize <= 32768 {
		sess.PacketSize = packetSize
	}

	// Extract UTF-16LE strings using offset/length pairs
	sess.HostName = readLoginString(payload, 36)
	sess.UserName = readLoginString(payload, 40)
	sess.AppName = readLoginString(payload, 48)
	dbName := readLoginString(payload, 68)
	if dbName != "" {
		sess.Database = dbName
	}
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

func (s *Server) handleSQLBatch(conn net.Conn, pkt *Packet, sess *ConnectionSession) {
	query := extractSQLBatchQuery(pkt.Payload)
	s.executeQueryAndRespond(conn, query, sess)
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

func (s *Server) handleRPC(conn net.Conn, pkt *Packet, sess *ConnectionSession) {
	rpc, err := parseRPCRequest(pkt.Payload)
	if err != nil || rpc == nil {
		done := BuildDone(TokenDone, DoneFinal, 0, 0)
		_ = WritePacket(conn, PacketTabular, sess.SPID, done)
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
		s.handleRPCExecuteSQL(conn, rpc, sess)
	case 11: // sp_prepare
		s.handleRPCPrepare(conn, rpc, sess)
	case 12: // sp_execute
		s.handleRPCExecute(conn, rpc, sess)
	case 13: // sp_prepexec
		s.handleRPCPrepExec(conn, rpc, sess)
	case 15: // sp_unprepare
		s.handleRPCUnprepare(conn, rpc, sess)
	default:
		if rpc.ProcName != "" {
			s.executeRPCAndRespond(conn, fmt.Sprintf("EXEC %s", rpc.ProcName), sess, nil)
		} else {
			done := BuildDoneProc(DoneFinal, 0, 0)
			_ = WritePacket(conn, PacketTabular, sess.SPID, done)
		}
	}
}

func (s *Server) handleRPCPrepare(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession) {
	var paramsDecl string
	var stmt string

	for i, p := range rpc.Params {
		name := strings.ToLower(p.Name)
		if name == "@params" || (name == "" && i == 1) {
			if str, ok := p.Value.(string); ok {
				paramsDecl = str
			}
		} else if name == "@stmt" || (name == "" && i == 2) {
			if str, ok := p.Value.(string); ok {
				stmt = str
			}
		}
	}
	if stmt == "" && len(rpc.Params) >= 3 {
		if str, ok := rpc.Params[1].Value.(string); ok && paramsDecl == "" {
			paramsDecl = str
		}
		if str, ok := rpc.Params[2].Value.(string); ok {
			stmt = str
		}
	} else if stmt == "" && len(rpc.Params) == 2 {
		if str, ok := rpc.Params[1].Value.(string); ok {
			stmt = str
		}
	}

	sess.prepMu.Lock()
	sess.prepCounter++
	handle := sess.prepCounter
	sess.prepared[handle] = &TdsPreparedStatement{
		Handle: handle,
		Params: paramsDecl,
		Query:  stmt,
	}
	sess.prepMu.Unlock()

	var buf bytes.Buffer
	buf.Write(BuildReturnValueInt(1, "@handle", handle))
	buf.Write(BuildReturnStatus(0))
	buf.Write(BuildDoneProc(DoneFinal, 0, 0))
	_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
}

func (s *Server) handleRPCExecute(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession) {
	var handle int32
	if len(rpc.Params) > 0 {
		handle = toInt32(rpc.Params[0].Value)
	}

	sess.prepMu.Lock()
	prep, exists := sess.prepared[handle]
	sess.prepMu.Unlock()

	if !exists {
		var buf bytes.Buffer
		buf.Write(BuildError(8179, 1, 16, fmt.Sprintf("Could not find prepared statement with handle %d.", handle), "SQLION", "", 1))
		buf.Write(BuildReturnStatus(-1))
		buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
		_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
		return
	}

	var boundParams []RPCParam
	if len(rpc.Params) > 1 {
		boundParams = rpc.Params[1:]
	}

	execSQL := s.buildBoundSQL(prep.Query, prep.Params, boundParams)
	s.executeRPCAndRespond(conn, execSQL, sess, nil)
}

func (s *Server) handleRPCPrepExec(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession) {
	var paramsDecl string
	var stmt string

	for i, p := range rpc.Params {
		name := strings.ToLower(p.Name)
		if name == "@params" || (name == "" && i == 1) {
			if str, ok := p.Value.(string); ok {
				paramsDecl = str
			}
		} else if name == "@stmt" || (name == "" && i == 2) {
			if str, ok := p.Value.(string); ok {
				stmt = str
			}
		}
	}
	if stmt == "" && len(rpc.Params) >= 3 {
		if str, ok := rpc.Params[1].Value.(string); ok && paramsDecl == "" {
			paramsDecl = str
		}
		if str, ok := rpc.Params[2].Value.(string); ok {
			stmt = str
		}
	}

	sess.prepMu.Lock()
	sess.prepCounter++
	handle := sess.prepCounter
	sess.prepared[handle] = &TdsPreparedStatement{
		Handle: handle,
		Params: paramsDecl,
		Query:  stmt,
	}
	sess.prepMu.Unlock()

	var boundParams []RPCParam
	if len(rpc.Params) > 3 {
		boundParams = rpc.Params[3:]
	}

	execSQL := s.buildBoundSQL(stmt, paramsDecl, boundParams)
	s.executeRPCAndRespond(conn, execSQL, sess, func(buf *bytes.Buffer) {
		buf.Write(BuildReturnValueInt(1, "@handle", handle))
	})
}

func (s *Server) handleRPCUnprepare(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession) {
	var handle int32
	if len(rpc.Params) > 0 {
		handle = toInt32(rpc.Params[0].Value)
	}

	sess.prepMu.Lock()
	delete(sess.prepared, handle)
	sess.prepMu.Unlock()

	var buf bytes.Buffer
	buf.Write(BuildReturnStatus(0))
	buf.Write(BuildDoneProc(DoneFinal, 0, 0))
	_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
}

func (s *Server) handleRPCExecuteSQL(conn net.Conn, rpc *parsedRPC, sess *ConnectionSession) {
	if len(rpc.Params) == 0 {
		done := BuildDoneProc(DoneFinal, 0, 0)
		_ = WritePacket(conn, PacketTabular, sess.SPID, done)
		return
	}

	stmt, _ := rpc.Params[0].Value.(string)
	var paramsDecl string
	var boundParams []RPCParam

	if len(rpc.Params) > 1 {
		if str, ok := rpc.Params[1].Value.(string); ok {
			paramsDecl = str
		}
		if len(rpc.Params) > 2 {
			boundParams = rpc.Params[2:]
		}
	}

	execSQL := s.buildBoundSQL(stmt, paramsDecl, boundParams)
	s.executeRPCAndRespond(conn, execSQL, sess, nil)
}

func (s *Server) executeRPCAndRespond(conn net.Conn, query string, sess *ConnectionSession, extraTokens func(buf *bytes.Buffer)) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		var buf bytes.Buffer
		if extraTokens != nil {
			extraTokens(&buf)
		}
		buf.Write(BuildReturnStatus(0))
		buf.Write(BuildDoneProc(DoneFinal, 0, 0))
		_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
		return
	}

	// System function / version short-circuit
	upper := strings.ToUpper(trimmed)
	if strings.Contains(upper, "@@VERSION") {
		cols := []ColumnInfo{
			{Name: "", Type: TypeNVarChar, Size: 4000, Nullable: false},
		}
		var buf bytes.Buffer
		buf.Write(BuildColMetaData(cols))
		buf.Write(BuildRow(cols, []any{"Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) - SQLion Engine"}))
		if extraTokens != nil {
			extraTokens(&buf)
		}
		buf.Write(BuildReturnStatus(0))
		buf.Write(BuildDoneProc(DoneFinal|DoneCount, 0xC1, 1))
		_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
		return
	}

	res, err := s.apiServer.ExecuteSessionSQL(sess.SessionID, trimmed)
	if err != nil {
		var buf bytes.Buffer
		buf.Write(BuildError(50000, 1, 16, err.Error(), "SQLION", "", 1))
		buf.Write(BuildReturnStatus(-1))
		buf.Write(BuildDoneProc(DoneFinal|DoneError, 0, 0))
		_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
		return
	}

	var buf bytes.Buffer

	// Emit any INFO messages
	for _, msg := range res.Messages {
		buf.Write(BuildInfo(0, 1, 10, msg, "SQLION", "", 1))
	}

	if len(res.Columns) > 0 {
		cols := s.inferColumnInfos(res)
		buf.Write(BuildColMetaData(cols))

		for _, row := range res.Rows {
			rowVals := make([]any, len(res.Columns))
			for i, colName := range res.Columns {
				if v, ok := row[colName]; ok {
					rowVals[i] = v
				} else {
					rowVals[i] = nil
				}
			}
			buf.Write(BuildRow(cols, rowVals))
		}

		buf.Write(BuildDone(TokenDoneInProc, DoneMore|DoneCount, 0xC1, uint64(len(res.Rows))))
		if extraTokens != nil {
			extraTokens(&buf)
		}
		buf.Write(BuildReturnStatus(0))
		buf.Write(BuildDoneProc(DoneFinal, 0xC1, uint64(len(res.Rows))))
	} else {
		cmdCode := uint16(0)
		switch strings.ToUpper(res.Command) {
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
		buf.Write(BuildDoneProc(DoneFinal|DoneCount, cmdCode, uint64(res.RowsAffected)))
	}

	_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
}

func (s *Server) buildBoundSQL(query string, paramsDecl string, params []RPCParam) string {
	query = strings.TrimSpace(query)
	query = replaceQuestionPlaceholders(query)
	if len(params) == 0 && strings.TrimSpace(paramsDecl) == "" {
		return query
	}

	decls := parseParamDeclarations(paramsDecl)
	usedParams := make(map[int]bool)

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

		query = substituteParamOutsideQuotes(query, decl.Name, formatSQLValue(val))
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
		query = substituteParamOutsideQuotes(query, paramName, formatSQLValue(p.Value))
	}

	return query
}

func substituteParamOutsideQuotes(query string, paramName string, formattedVal string) string {
	if !strings.HasPrefix(paramName, "@") {
		paramName = "@" + paramName
	}
	re := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(paramName) + `\b`)

	var b strings.Builder
	inString := false
	var quoteChar rune
	runes := []rune(query)

	for i := 0; i < len(runes); {
		ch := runes[i]
		if inString {
			b.WriteRune(ch)
			if ch == quoteChar {
				if i+1 < len(runes) && runes[i+1] == quoteChar {
					b.WriteRune(runes[i+1])
					i += 2
					continue
				}
				inString = false
			}
			i++
		} else {
			if ch == '\'' || ch == '"' {
				inString = true
				quoteChar = ch
				b.WriteRune(ch)
				i++
			} else if ch == '@' {
				remainder := string(runes[i:])
				loc := re.FindStringIndex(remainder)
				if len(loc) == 2 && loc[0] == 0 {
					b.WriteString(formattedVal)
					matchedRunes := len([]rune(remainder[:loc[1]]))
					i += matchedRunes
				} else {
					b.WriteRune(ch)
					i++
				}
			} else {
				b.WriteRune(ch)
				i++
			}
		}
	}
	return b.String()
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
	_ = WritePacket(conn, PacketTabular, sess.SPID, done)
}

func (s *Server) executeQueryAndRespond(conn net.Conn, query string, sess *ConnectionSession) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		done := BuildDone(TokenDone, DoneFinal, 0, 0)
		_ = WritePacket(conn, PacketTabular, sess.SPID, done)
		return
	}

	// System function / version short-circuit
	upper := strings.ToUpper(trimmed)
	if strings.Contains(upper, "@@VERSION") {
		cols := []ColumnInfo{
			{Name: "", Type: TypeNVarChar, Size: 4000, Nullable: false},
		}
		var buf bytes.Buffer
		buf.Write(BuildColMetaData(cols))
		buf.Write(BuildRow(cols, []any{"Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) - SQLion Engine"}))
		buf.Write(BuildDone(TokenDone, DoneFinal|DoneCount, 0xC1, 1))
		_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
		return
	}

	res, err := s.apiServer.ExecuteSessionSQL(sess.SessionID, trimmed)
	if err != nil {
		var buf bytes.Buffer
		buf.Write(BuildError(50000, 1, 16, err.Error(), "SQLION", "", 1))
		buf.Write(BuildDone(TokenDone, DoneFinal|DoneError, 0, 0))
		_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
		return
	}

	var buf bytes.Buffer

	// Emit any INFO messages (e.g. PRINT statements or proc logs)
	for _, msg := range res.Messages {
		buf.Write(BuildInfo(0, 1, 10, msg, "SQLION", "", 1))
	}

	if len(res.Columns) > 0 {
		cols := s.inferColumnInfos(res)
		buf.Write(BuildColMetaData(cols))

		for _, row := range res.Rows {
			rowVals := make([]any, len(res.Columns))
			for i, colName := range res.Columns {
				if v, ok := row[colName]; ok {
					rowVals[i] = v
				} else {
					rowVals[i] = nil
				}
			}
			buf.Write(BuildRow(cols, rowVals))
		}

		buf.Write(BuildDone(TokenDone, DoneFinal|DoneCount, 0xC1, uint64(len(res.Rows))))
	} else {
		cmdCode := uint16(0)
		switch strings.ToUpper(res.Command) {
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
		buf.Write(BuildDone(TokenDone, DoneFinal|DoneCount, cmdCode, uint64(res.RowsAffected)))
	}

	_ = WritePacket(conn, PacketTabular, sess.SPID, buf.Bytes())
}

func (s *Server) inferColumnInfos(res *api.QueryResult) []ColumnInfo {
	cols := make([]ColumnInfo, len(res.Columns))

	for i, colName := range res.Columns {
		colType := TypeNVarChar
		colSize := 4000
		nullable := true

		// Scan non-null sample values from rows
		for _, row := range res.Rows {
			if v, ok := row[colName]; ok && v != nil {
				switch v.(type) {
				case int, int64, int32, int16, int8:
					colType = TypeIntN
					colSize = 8
				case float64, float32:
					colType = TypeFltN
					colSize = 8
				case bool:
					colType = TypeBitN
					colSize = 1
				case time.Time:
					colType = TypeDateTimeN
					colSize = 8
				case string:
					colType = TypeNVarChar
					colSize = 4000
				}
				break
			}
		}

		cols[i] = ColumnInfo{
			Name:     colName,
			Type:     colType,
			Size:     colSize,
			Nullable: nullable,
		}
	}

	return cols
}
