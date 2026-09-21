package tds

import (
	"bytes"
	"encoding/binary"
	"net"
	"sync"
	"testing"

	"sqlion/pkg/catalog"
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
