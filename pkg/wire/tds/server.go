package tds

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
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

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleConnection(c)
		}(conn)
	}
}

// ConnectionSession holds state for a single active TDS client connection.
type ConnectionSession struct {
	SPID       uint16
	SessionID  string
	HostName   string
	UserName   string
	AppName    string
	Database   string
	PacketSize uint32
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	spid := uint16(atomic.AddUint32(&s.spidCounter, 1))
	sess := &ConnectionSession{
		SPID:       spid,
		SessionID:  fmt.Sprintf("tds-%d", spid),
		Database:   "master",
		PacketSize: DefaultPacketSize,
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
	// Parse RPC payload
	query := extractRPCQuery(pkt.Payload)
	if query != "" {
		s.executeQueryAndRespond(conn, query, sess)
		return
	}

	// Default fallback: empty response
	done := BuildDone(TokenDone, DoneFinal, 0, 0)
	_ = WritePacket(conn, PacketTabular, sess.SPID, done)
}

func extractRPCQuery(payload []byte) string {
	if len(payload) < 2 {
		return ""
	}

	r := bytes.NewReader(payload)

	// Check for ALL_HEADERS in RPC
	var totalHeadersLen uint32
	if err := binary.Read(r, binary.LittleEndian, &totalHeadersLen); err == nil && totalHeadersLen >= 4 && int(totalHeadersLen) < len(payload) {
		// Seek past headers
		_, _ = r.Seek(int64(totalHeadersLen), io.SeekStart)
	} else {
		// No ALL_HEADERS, reset to beginning
		_, _ = r.Seek(0, io.SeekStart)
	}

	var nameLen uint16
	if err := binary.Read(r, binary.LittleEndian, &nameLen); err != nil {
		return ""
	}

	var procName string
	var procID uint16

	if nameLen == 0xFFFF {
		// ProcID follows (2 bytes)
		if err := binary.Read(r, binary.LittleEndian, &procID); err != nil {
			return ""
		}
	} else if nameLen > 0 {
		nameBytes := make([]byte, int(nameLen)*2)
		if _, err := io.ReadFull(r, nameBytes); err != nil {
			return ""
		}
		procName = DecodeUTF16LE(nameBytes)
	}

	var optionFlags uint16
	_ = binary.Read(r, binary.LittleEndian, &optionFlags)

	// If sp_executesql (ProcID 10 or procName "sp_executesql")
	if procID == 10 || strings.EqualFold(procName, "sp_executesql") {
		// First parameter is the SQL statement (NVARCHAR)
		return readRPCFirstStringParam(r)
	}

	// For custom procedure calls, e.g. "EXEC my_proc"
	if procName != "" {
		return fmt.Sprintf("EXEC %s", procName)
	}

	return ""
}

func readRPCFirstStringParam(r *bytes.Reader) string {
	// ParamName: B_VARCHAR
	paramName, err := ReadBVarChar(r)
	if err != nil {
		return ""
	}
	_ = paramName

	// StatusFlags (1B)
	_, _ = r.ReadByte()

	// TypeInfo
	typeByte, err := r.ReadByte()
	if err != nil {
		return ""
	}

	if typeByte == TypeNVarChar || typeByte == TypeVarChar || typeByte == TypeBigVarChar {
		// MaxLen (uint16)
		var maxLen uint16
		_ = binary.Read(r, binary.LittleEndian, &maxLen)

		// Collation (5 bytes)
		var collation [5]byte
		_, _ = io.ReadFull(r, collation[:])

		// String value length (uint16 LE)
		var valLen uint16
		if err := binary.Read(r, binary.LittleEndian, &valLen); err != nil || valLen == 0xFFFF {
			return ""
		}

		valBytes := make([]byte, valLen)
		if _, err := io.ReadFull(r, valBytes); err != nil {
			return ""
		}

		if typeByte == TypeNVarChar {
			return DecodeUTF16LE(valBytes)
		}
		return string(valBytes)
	}

	return ""
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
