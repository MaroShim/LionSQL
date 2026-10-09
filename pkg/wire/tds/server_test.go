package tds

import (
	"bytes"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage/memengine"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)

	return NewServer("127.0.0.1:0", cat, eng)
}

func connectClient(t *testing.T, srv *Server) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go srv.handleConnection(serverConn)
	t.Cleanup(func() {
		_ = clientConn.Close()
	})
	return clientConn
}

// performHandshake sends Prelogin and Login7 packets and verifies the server's responses.
func performHandshake(t *testing.T, conn net.Conn) {
	t.Helper()

	// 1. Send Prelogin
	preloginPayload := BuildPreloginResponse(EncryptNotSup)
	if err := WritePacket(conn, PacketPrelogin, 0, preloginPayload); err != nil {
		t.Fatalf("failed to send Prelogin packet: %v", err)
	}

	// Read Prelogin response
	respPkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read Prelogin response: %v", err)
	}
	if respPkt.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in prelogin response, got 0x%02X", respPkt.Type)
	}
	if len(respPkt.Payload) == 0 {
		t.Fatalf("empty Prelogin response payload")
	}

	// 2. Send Login7
	var loginBuf bytes.Buffer
	// Total length (4B LE) placeholder
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	// TDS Version: 7.4 (0x74000004)
	_ = binary.Write(&loginBuf, binary.BigEndian, uint32(0x74000004))
	// PacketSize: 4096 (4B LE)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(4096))
	// ClientProgVer (4B LE)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1))
	// ClientPID (4B LE)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1234))
	// ConnectionID (4B LE)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	// Flags
	loginBuf.WriteByte(0x20)
	loginBuf.WriteByte(0x03)
	loginBuf.WriteByte(0x00)
	loginBuf.WriteByte(0x00)
	// TimeZone & LCID
	_ = binary.Write(&loginBuf, binary.LittleEndian, int32(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1033))

	// Variable length string offsets and lengths
	userName := "sa"
	appName := "sqlcmd"
	dbName := "master"

	// Header size: 94 bytes
	headerLen := uint16(94)

	u16User := EncodeUTF16LE(userName)
	u16App := EncodeUTF16LE(appName)
	u16DB := EncodeUTF16LE(dbName)

	ibUser := headerLen
	cchUser := uint16(len(userName))

	ibApp := ibUser + uint16(len(u16User))
	cchApp := uint16(len(appName))

	ibDB := ibApp + uint16(len(u16App))
	cchDB := uint16(len(dbName))

	// Write string offset/length pairs (36..94)
	// HostName (36..40)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// UserName (40..44)
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibUser)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchUser)
	// Password (44..48)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// AppName (48..52)
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibApp)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchApp)
	// ServerName (52..56)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// Extension (56..60)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// CltIntName (60..64)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// Language (64..68)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// Database (68..72)
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibDB)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchDB)
	// ClientID (72..78 - 6 bytes MAC)
	loginBuf.Write([]byte{0x00, 0x15, 0x5D, 0x12, 0x34, 0x56})
	// SSPI (78..82)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// AtchDBFile (82..86)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// ChangePassword (86..90)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// cbSSPILong (90..94)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))

	// Append string payload data
	loginBuf.Write(u16User)
	loginBuf.Write(u16App)
	loginBuf.Write(u16DB)

	// Set actual total length in first 4 bytes
	totalLen := uint32(loginBuf.Len())
	rawLogin := loginBuf.Bytes()
	binary.LittleEndian.PutUint32(rawLogin[:4], totalLen)

	if err := WritePacket(conn, PacketLogin7, 0, rawLogin); err != nil {
		t.Fatalf("failed to send Login7 packet: %v", err)
	}

	// Read Login7 response (Token stream: LOGINACK + ENVCHANGE + DONE)
	loginResp, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read Login7 response: %v", err)
	}
	if loginResp.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in Login7 response, got 0x%02X", loginResp.Type)
	}

	// Verify LOGINACK token exists in payload
	if !bytes.Contains(loginResp.Payload, []byte{TokenLoginAck}) {
		t.Fatalf("LOGINACK token (0xAD) missing in Login7 response")
	}
	// Verify DONE token exists in payload
	if !bytes.Contains(loginResp.Payload, []byte{TokenDone}) {
		t.Fatalf("DONE token (0xFD) missing in Login7 response")
	}
}

// sendSQLBatch sends a SQL batch command and returns the response payload.
func sendSQLBatch(t *testing.T, conn net.Conn, sql string) []byte {
	t.Helper()
	u16SQL := EncodeUTF16LE(sql)
	if err := WritePacket(conn, PacketSQLBatch, 1, u16SQL); err != nil {
		t.Fatalf("failed to write SQLBatch packet: %v", err)
	}

	resp, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read SQLBatch response: %v", err)
	}
	if resp.Type != PacketTabular {
		t.Fatalf("expected PacketTabular response, got 0x%02X", resp.Type)
	}
	return resp.Payload
}

func TestTDS_HandshakeAndVersion(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)

	performHandshake(t, conn)

	// Test SELECT @@VERSION
	payload := sendSQLBatch(t, conn, "SELECT @@VERSION")
	if !bytes.Contains(payload, []byte{TokenColMetaData}) {
		t.Fatalf("COLMETADATA token missing in SELECT @@VERSION response")
	}
	if !bytes.Contains(payload, []byte{TokenRow}) {
		t.Fatalf("ROW token missing in SELECT @@VERSION response")
	}
	if !bytes.Contains(payload, []byte{TokenDone}) {
		t.Fatalf("DONE token missing in SELECT @@VERSION response")
	}
}

func TestTDS_CRUDOperations(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)

	performHandshake(t, conn)

	// 1. CREATE TABLE
	createSQL := `CREATE TABLE employees (id INT PRIMARY KEY, name VARCHAR(50), salary INT, active BIT);`
	p1 := sendSQLBatch(t, conn, createSQL)
	if !bytes.Contains(p1, []byte{TokenDone}) {
		t.Fatalf("DONE token missing in CREATE TABLE response")
	}

	// 2. INSERT
	insertSQL := `INSERT INTO employees (id, name, salary, active) VALUES (1, 'Alice', 100000, 1), (2, 'Bob', 80000, 0);`
	p2 := sendSQLBatch(t, conn, insertSQL)
	if !bytes.Contains(p2, []byte{TokenDone}) {
		t.Fatalf("DONE token missing in INSERT response")
	}

	// 3. SELECT
	selectSQL := `SELECT id, name, salary, active FROM employees ORDER BY id ASC;`
	p3 := sendSQLBatch(t, conn, selectSQL)
	if !bytes.Contains(p3, []byte{TokenColMetaData}) {
		t.Fatalf("COLMETADATA token missing in SELECT response")
	}
	if !bytes.Contains(p3, []byte{TokenRow}) {
		t.Fatalf("ROW token missing in SELECT response")
	}
	if !bytes.Contains(p3, []byte{TokenDone}) {
		t.Fatalf("DONE token missing in SELECT response")
	}

	// 4. UPDATE
	updateSQL := `UPDATE employees SET salary = 120000 WHERE id = 1;`
	p4 := sendSQLBatch(t, conn, updateSQL)
	if !bytes.Contains(p4, []byte{TokenDone}) {
		t.Fatalf("DONE token missing in UPDATE response")
	}

	// 5. DELETE
	deleteSQL := `DELETE FROM employees WHERE id = 2;`
	p5 := sendSQLBatch(t, conn, deleteSQL)
	if !bytes.Contains(p5, []byte{TokenDone}) {
		t.Fatalf("DONE token missing in DELETE response")
	}
}

func TestTDS_StoredProceduresAndRPC(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)

	performHandshake(t, conn)

	// Create table and insert data
	_ = sendSQLBatch(t, conn, `CREATE TABLE accounts (id INT PRIMARY KEY, balance INT);`)
	_ = sendSQLBatch(t, conn, `INSERT INTO accounts (id, balance) VALUES (1, 500);`)

	// Create Stored Procedure
	procSQL := `CREATE PROCEDURE TransferFunds @from INT, @amount INT AS BEGIN UPDATE accounts SET balance = balance - @amount WHERE id = @from; END;`
	p1 := sendSQLBatch(t, conn, procSQL)
	if !bytes.Contains(p1, []byte{TokenDone}) {
		t.Fatalf("DONE token missing in CREATE PROCEDURE response")
	}

	// Execute Stored Procedure via EXEC
	execSQL := `EXEC TransferFunds @from = 1, @amount = 100;`
	p2 := sendSQLBatch(t, conn, execSQL)
	if !bytes.Contains(p2, []byte{TokenDone}) {
		t.Fatalf("DONE token missing in EXEC response")
	}

	// Verify balance updated
	checkSQL := `SELECT balance FROM accounts WHERE id = 1;`
	p3 := sendSQLBatch(t, conn, checkSQL)
	if !bytes.Contains(p3, []byte{TokenRow}) {
		t.Fatalf("ROW token missing in SELECT response after procedure exec")
	}
}

func TestTDS_ErrorHandling(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)

	performHandshake(t, conn)

	// Query invalid table
	errPayload := sendSQLBatch(t, conn, "SELECT * FROM non_existent_table_12345;")
	if !bytes.Contains(errPayload, []byte{TokenError}) {
		t.Fatalf("ERROR token (0xAA) missing in error response")
	}
	if !bytes.Contains(errPayload, []byte{TokenDone}) {
		t.Fatalf("DONE token (0xFD) missing in error response")
	}
}

func TestTDS_ConcurrentConnections(t *testing.T) {
	srv := setupTestServer(t)

	const numClients = 10
	var wg sync.WaitGroup
	wg.Add(numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			defer wg.Done()

			conn := connectClient(t, srv)
			performHandshake(t, conn)

			res := sendSQLBatch(t, conn, "SELECT @@VERSION")
			if !bytes.Contains(res, []byte{TokenDone}) {
				t.Errorf("client %d missing DONE token", clientID)
			}
		}(i)
	}

	wg.Wait()
}

func TestTDS_PacketFraming(t *testing.T) {
	// Test chunking of large payload across multiple TDS frames
	var buf bytes.Buffer

	largePayload := make([]byte, 10000)
	for i := range largePayload {
		largePayload[i] = byte(i % 256)
	}

	if err := WritePacket(&buf, PacketSQLBatch, 42, largePayload); err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	// Reassemble using ReadPacket
	pkt, err := ReadPacket(&buf)
	if err != nil {
		t.Fatalf("ReadPacket failed: %v", err)
	}

	if pkt.Type != PacketSQLBatch {
		t.Fatalf("expected PacketSQLBatch, got 0x%02X", pkt.Type)
	}
	if pkt.SPID != 42 {
		t.Fatalf("expected SPID 42, got %d", pkt.SPID)
	}
	if !bytes.Equal(pkt.Payload, largePayload) {
		t.Fatalf("reassembled payload does not match original payload")
	}
}

func sendRPC(t *testing.T, conn net.Conn, procID uint16, optionFlags uint16, paramsPayload []byte) []byte {
	t.Helper()
	var rpcBuf bytes.Buffer

	_ = binary.Write(&rpcBuf, binary.LittleEndian, uint16(0xFFFF))
	_ = binary.Write(&rpcBuf, binary.LittleEndian, procID)
	_ = binary.Write(&rpcBuf, binary.LittleEndian, optionFlags)
	rpcBuf.Write(paramsPayload)

	if err := WritePacket(conn, PacketRPC, 1, rpcBuf.Bytes()); err != nil {
		t.Fatalf("failed to send RPC packet: %v", err)
	}

	resp, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read RPC response: %v", err)
	}
	return resp.Payload
}

func buildNVarCharParam(name string, isOutput bool, val string) []byte {
	var buf bytes.Buffer
	if name != "" {
		buf.WriteByte(byte(len(name)))
		buf.Write(EncodeUTF16LE(name))
	} else {
		buf.WriteByte(0)
	}
	if isOutput {
		buf.WriteByte(0x01)
	} else {
		buf.WriteByte(0x00)
	}
	buf.WriteByte(TypeNVarChar)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(4000))
	buf.Write([]byte{0x09, 0x04, 0xD0, 0x00, 0x34})
	u16 := EncodeUTF16LE(val)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(u16)))
	buf.Write(u16)
	return buf.Bytes()
}

func buildIntParam(name string, isOutput bool, val int32) []byte {
	var buf bytes.Buffer
	if name != "" {
		buf.WriteByte(byte(len(name)))
		buf.Write(EncodeUTF16LE(name))
	} else {
		buf.WriteByte(0)
	}
	if isOutput {
		buf.WriteByte(0x01)
	} else {
		buf.WriteByte(0x00)
	}
	buf.WriteByte(TypeInt4)
	_ = binary.Write(&buf, binary.LittleEndian, val)
	return buf.Bytes()
}

func TestTDS_PreparedStatementRPC(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	// Create test table
	_ = sendSQLBatch(t, conn, `CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50));`)

	// 1. sp_prepare (ProcID 11): Prepare INSERT statement
	var prepParams bytes.Buffer
	prepParams.Write(buildIntParam("@handle", true, 0))
	prepParams.Write(buildNVarCharParam("@params", false, "@p1 INT, @p2 VARCHAR(50)"))
	prepParams.Write(buildNVarCharParam("@stmt", false, "INSERT INTO items (id, name) VALUES (@p1, @p2);"))

	respPrep := sendRPC(t, conn, 11, 0, prepParams.Bytes())
	if !bytes.Contains(respPrep, []byte{TokenReturnValue}) {
		t.Fatalf("sp_prepare: expected TokenReturnValue (0xAC) for @handle")
	}
	if !bytes.Contains(respPrep, []byte{TokenDoneProc}) {
		t.Fatalf("sp_prepare: expected TokenDoneProc (0xFE)")
	}

	// Extract the handle from ReturnValue payload
	rvIdx := bytes.Index(respPrep, []byte{TokenReturnValue})
	// ReturnValue format: TokenReturnValue(1B) + Ordinal(2B) + BVarChar Name + Status(1B) + UserType(4B) + Flags(2B) + Type(1B: 0x38) + Int4(4B)
	// After @handle (len=7 -> 1+14=15 bytes), status(1), usertype(4), flags(2), type(1), val(4)
	valOffset := rvIdx + 1 + 2 + 1 + 14 + 1 + 4 + 2 + 1
	var handle int32 = 1
	if len(respPrep) >= valOffset+4 {
		handle = int32(binary.LittleEndian.Uint32(respPrep[valOffset : valOffset+4]))
	}

	// 2. sp_execute (ProcID 12): Execute with handle and values
	var execParams bytes.Buffer
	execParams.Write(buildIntParam("@handle", false, handle))
	execParams.Write(buildIntParam("@p1", false, 101))
	execParams.Write(buildNVarCharParam("@p2", false, "Gadget"))

	respExec := sendRPC(t, conn, 12, 0, execParams.Bytes())
	if !bytes.Contains(respExec, []byte{TokenDoneProc}) {
		t.Fatalf("sp_execute: expected TokenDoneProc (0xFE)")
	}

	// Verify insertion
	selResp := sendSQLBatch(t, conn, "SELECT name FROM items WHERE id = 101;")
	if !bytes.Contains(selResp, []byte{TokenRow}) {
		t.Fatalf("sp_execute: inserted record not found")
	}

	// 3. sp_prepexec (ProcID 13): Prepare and execute in one round-trip
	var prepexecParams bytes.Buffer
	prepexecParams.Write(buildIntParam("@handle", true, 0))
	prepexecParams.Write(buildNVarCharParam("@params", false, "@id INT"))
	prepexecParams.Write(buildNVarCharParam("@stmt", false, "SELECT id, name FROM items WHERE id = @id;"))
	prepexecParams.Write(buildIntParam("@id", false, 101))

	respPrepExec := sendRPC(t, conn, 13, 0, prepexecParams.Bytes())
	if !bytes.Contains(respPrepExec, []byte{TokenColMetaData}) {
		t.Fatalf("sp_prepexec: expected TokenColMetaData (0x81)")
	}
	if !bytes.Contains(respPrepExec, []byte{TokenRow}) {
		t.Fatalf("sp_prepexec: expected TokenRow (0xD1)")
	}
	if !bytes.Contains(respPrepExec, []byte{TokenReturnValue}) {
		t.Fatalf("sp_prepexec: expected TokenReturnValue (0xAC) for new handle")
	}
	if !bytes.Contains(respPrepExec, []byte{TokenDoneProc}) {
		t.Fatalf("sp_prepexec: expected TokenDoneProc (0xFE)")
	}

	// 4. Parameterized sp_executesql (ProcID 10)
	var execSqlParams bytes.Buffer
	execSqlParams.Write(buildNVarCharParam("@stmt", false, "SELECT name FROM items WHERE id = @p1;"))
	execSqlParams.Write(buildNVarCharParam("@params", false, "@p1 INT"))
	execSqlParams.Write(buildIntParam("@p1", false, 101))

	respExecSql := sendRPC(t, conn, 10, 0, execSqlParams.Bytes())
	if !bytes.Contains(respExecSql, []byte{TokenRow}) {
		t.Fatalf("sp_executesql: expected TokenRow (0xD1)")
	}
	if !bytes.Contains(respExecSql, []byte{TokenDoneProc}) {
		t.Fatalf("sp_executesql: expected TokenDoneProc (0xFE)")
	}

	// 5. sp_unprepare (ProcID 15): Unprepare handle
	var unprepParams bytes.Buffer
	unprepParams.Write(buildIntParam("@handle", false, handle))

	respUnprep := sendRPC(t, conn, 15, 0, unprepParams.Bytes())
	if !bytes.Contains(respUnprep, []byte{TokenDoneProc}) {
		t.Fatalf("sp_unprepare: expected TokenDoneProc (0xFE)")
	}

	// 6. Verify subsequent sp_execute on deleted handle returns error 8179
	var invalidExecParams bytes.Buffer
	invalidExecParams.Write(buildIntParam("@handle", false, handle))
	invalidExecParams.Write(buildIntParam("@p1", false, 999))
	invalidExecParams.Write(buildNVarCharParam("@p2", false, "Ghost"))

	respInvalid := sendRPC(t, conn, 12, 0, invalidExecParams.Bytes())
	if !bytes.Contains(respInvalid, []byte{TokenError}) {
		t.Fatalf("sp_execute on unprepared handle: expected TokenError (0xAA)")
	}
}

func TestTDS_PreparedStatement_BytecodeProgram(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	_ = sendSQLBatch(t, conn, "CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(100));")
	_ = sendSQLBatch(t, conn, "INSERT INTO items (id, name) VALUES (101, 'Mechanical Keyboard');")

	// 1. Prepare a SELECT statement via sp_prepare (ProcID 11)
	var prepParams bytes.Buffer
	prepParams.Write(buildIntParam("@handle", true, 0))
	prepParams.Write(buildNVarCharParam("@params", false, "@id INT"))
	prepParams.Write(buildNVarCharParam("@stmt", false, "SELECT id, name FROM items WHERE id = @id;"))

	respPrep := sendRPC(t, conn, 11, 0, prepParams.Bytes())
	if !bytes.Contains(respPrep, []byte{TokenReturnValue}) {
		t.Fatalf("sp_prepare: expected TokenReturnValue (0xAC)")
	}

	rvIdx := bytes.Index(respPrep, []byte{TokenReturnValue})
	if rvIdx < 0 {
		t.Fatalf("sp_prepare: cannot find TokenReturnValue")
	}
	valOffset := rvIdx + 1 + 2 + 1 + 14 + 1 + 4 + 2 + 1
	var handle int32 = 1
	if len(respPrep) >= valOffset+4 {
		handle = int32(binary.LittleEndian.Uint32(respPrep[valOffset : valOffset+4]))
	}

	// 2. Execute via sp_execute (ProcID 12)
	var execParams bytes.Buffer
	execParams.Write(buildIntParam("@handle", false, handle))
	execParams.Write(buildIntParam("@id", false, 101))

	respExec := sendRPC(t, conn, 12, 0, execParams.Bytes())
	if !bytes.Contains(respExec, []byte{TokenColMetaData}) {
		t.Fatalf("sp_execute: expected TokenColMetaData (0x81)")
	}
	if !bytes.Contains(respExec, []byte{TokenRow}) {
		t.Fatalf("sp_execute: expected TokenRow (0xD1)")
	}
	if !bytes.Contains(respExec, []byte{TokenDoneProc}) {
		t.Fatalf("sp_execute: expected TokenDoneProc (0xFE)")
	}
}

func TestTDS_PlanCacheSharing(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)

	sharedCache := runtime.NewPlanCache()
	srv1 := NewServerWithCache("127.0.0.1:0", cat, eng, sharedCache)
	srv2 := NewServer("127.0.0.1:0", cat, eng)
	srv2.SetBytecodePlanCache(sharedCache)

	if srv1.BytecodePlanCache() != sharedCache {
		t.Fatalf("srv1 BytecodePlanCache does not match sharedCache")
	}
	if srv2.BytecodePlanCache() != sharedCache {
		t.Fatalf("srv2 BytecodePlanCache does not match sharedCache")
	}

	conn1 := connectClient(t, srv1)
	performHandshake(t, conn1)
	conn2 := connectClient(t, srv2)
	performHandshake(t, conn2)

	_ = sendSQLBatch(t, conn1, "CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(100));")
	_ = sendSQLBatch(t, conn1, "INSERT INTO products (id, name) VALUES (1, 'Widget');")

	stmt := "SELECT id, name FROM products WHERE id = @id;"

	// 1. Prepare on srv1
	var prepParams1 bytes.Buffer
	prepParams1.Write(buildIntParam("@handle", true, 0))
	prepParams1.Write(buildNVarCharParam("@params", false, "@id INT"))
	prepParams1.Write(buildNVarCharParam("@stmt", false, stmt))
	resp1 := sendRPC(t, conn1, 11, 0, prepParams1.Bytes())
	if !bytes.Contains(resp1, []byte{TokenReturnValue}) {
		t.Fatalf("sp_prepare srv1: expected TokenReturnValue")
	}

	// 2. Prepare identical query on srv2
	var prepParams2 bytes.Buffer
	prepParams2.Write(buildIntParam("@handle", true, 0))
	prepParams2.Write(buildNVarCharParam("@params", false, "@id INT"))
	prepParams2.Write(buildNVarCharParam("@stmt", false, stmt))
	resp2 := sendRPC(t, conn2, 11, 0, prepParams2.Bytes())
	if !bytes.Contains(resp2, []byte{TokenReturnValue}) {
		t.Fatalf("sp_prepare srv2: expected TokenReturnValue")
	}

	// Verify that shared PlanCache contains the compiled script
	cachedProg, err := sharedCache.GetOrCompile(stmt)
	if err != nil {
		t.Fatalf("sharedCache.GetOrCompile failed: %v", err)
	}
	if cachedProg == nil {
		t.Fatalf("expected non-nil cached program in shared plan cache")
	}
}

func TestTDS_InferColumnInfos_DateTime(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	srv := NewServer("127.0.0.1:0", cat, eng)

	mockRes := &api.QueryResult{
		Columns: []string{"id", "name", "create_date", "modify_date"},
		Rows: []map[string]any{
			{
				"id":          1,
				"name":        "Test",
				"create_date": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				"modify_date": "2026-01-01T00:00:00Z",
			},
		},
	}

	cols := srv.inferColumnInfos(mockRes)
	if len(cols) != 4 {
		t.Fatalf("expected 4 cols, got %d", len(cols))
	}
	if cols[2].Type != TypeDateTimeN || cols[2].Size != 8 {
		t.Fatalf("expected col create_date to be TypeDateTimeN with size 8, got type 0x%X size %d", cols[2].Type, cols[2].Size)
	}
	if cols[3].Type != TypeDateTimeN || cols[3].Size != 8 {
		t.Fatalf("expected col modify_date to be TypeDateTimeN with size 8, got type 0x%X size %d", cols[3].Type, cols[3].Size)
	}
}

func TestTDS_AttentionQueryCancellation(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)

	performHandshake(t, conn)

	// Create a procedure with an infinite loop to cancel
	createProcSQL := `CREATE PROCEDURE SpinCancel AS BEGIN DECLARE @c INT; SET @c = 0; WHILE 1=1 BEGIN SET @c = @c + 1; END; END;`
	payload := sendSQLBatch(t, conn, createProcSQL)
	if !bytes.Contains(payload, []byte{TokenDone}) {
		t.Fatalf("failed to create SpinCancel procedure")
	}

	// 1. Send EXEC SpinCancel;
	execSQL := "EXEC SpinCancel;"
	u16SQL := EncodeUTF16LE(execSQL)
	if err := WritePacket(conn, PacketSQLBatch, 1, u16SQL); err != nil {
		t.Fatalf("failed to write SQLBatch packet: %v", err)
	}

	// Wait briefly so the VM is executing inside the WHILE loop
	time.Sleep(30 * time.Millisecond)

	// 2. Send Attention packet (Type 0x06)
	if err := WritePacket(conn, PacketAttention, 1, nil); err != nil {
		t.Fatalf("failed to write Attention packet: %v", err)
	}

	// 3. Read Attention response
	attnResp, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read Attention response: %v", err)
	}
	if attnResp.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in attention response, got 0x%02X", attnResp.Type)
	}
	if len(attnResp.Payload) < 3 || attnResp.Payload[0] != TokenDone {
		t.Fatalf("expected TokenDone (0xFD) as first token in attention response, got %x", attnResp.Payload)
	}
	// Check status has DoneAttn (0x0020) bit set
	status := binary.LittleEndian.Uint16(attnResp.Payload[1:3])
	if status&DoneAttn == 0 {
		t.Fatalf("expected DoneAttn flag (0x0020) in token status, got status 0x%04X", status)
	}

	// 4. Verify connection remains functional for subsequent queries
	okPayload := sendSQLBatch(t, conn, "SELECT 123 AS num;")
	if !bytes.Contains(okPayload, []byte{TokenRow}) {
		t.Fatalf("subsequent query failed after attention packet; response: %x", okPayload)
	}
}



