package wire

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func setupTestWireServer(t *testing.T) (*Server, storage.Engine, *catalog.Catalog) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = eng.CreateTable(schema)

	session, _ := eng.NewSession()
	tx, _ := session.BeginTx(storage.TxOptions{})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Commit()
	_ = session.Close()

	srv := NewServer("127.0.0.1:0", cat, eng)
	return srv, eng, cat
}

// connectPGClient creates a paired client/server connection, starts the connection handler,
// and completes the protocol v3.0 startup handshake up to ReadyForQuery ('Z').
func connectPGClient(t *testing.T, srv *Server) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go srv.handleConnection(serverConn)
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	startupBuf := make([]byte, 8)
	binary.BigEndian.PutUint32(startupBuf[:4], 8)
	binary.BigEndian.PutUint32(startupBuf[4:8], 196608) // 3.0
	if _, err := clientConn.Write(startupBuf); err != nil {
		t.Fatalf("Failed to write startup: %v", err)
	}

	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, header); err != nil {
			t.Fatalf("Failed to read header: %v", err)
		}
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(clientConn, payload); err != nil {
			t.Fatalf("Failed to read payload: %v", err)
		}
		if header[0] == MsgReadyForQuery {
			break
		}
	}
	return clientConn
}

// sendPGQuery sends a Simple Query ('Q') and returns all DataRow payloads.
func sendPGQuery(t *testing.T, conn net.Conn, sql string) [][]byte {
	t.Helper()
	if !strings.HasSuffix(sql, "\x00") {
		sql += "\x00"
	}
	queryBuf := make([]byte, 1+4+len(sql))
	queryBuf[0] = 'Q'
	binary.BigEndian.PutUint32(queryBuf[1:5], uint32(4+len(sql)))
	copy(queryBuf[5:], sql)

	if _, err := conn.Write(queryBuf); err != nil {
		t.Fatalf("Failed to send query %q: %v", sql, err)
	}

	var dataRows [][]byte
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Fatalf("Failed to read response header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("Failed to read response payload: %v", err)
		}

		if msgType == MsgDataRow {
			dataRows = append(dataRows, payload)
		}
		if msgType == MsgReadyForQuery {
			break
		}
	}
	return dataRows
}

// authenticatePGClient connects to srv with the given user and password and returns (success, errorPayload).
func authenticatePGClient(t *testing.T, srv *Server, user, password string) (bool, string) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	params := map[string]string{"user": user, "database": "sqlion"}
	var payload []byte
	for k, v := range params {
		payload = append(payload, []byte(k)...)
		payload = append(payload, 0)
		payload = append(payload, []byte(v)...)
		payload = append(payload, 0)
	}
	payload = append(payload, 0)
	msgLen := 8 + len(payload)
	buf := make([]byte, msgLen)
	binary.BigEndian.PutUint32(buf[:4], uint32(msgLen))
	binary.BigEndian.PutUint32(buf[4:8], 196608)
	copy(buf[8:], payload)
	if _, err := clientConn.Write(buf); err != nil {
		t.Fatalf("failed to send startup: %v", err)
	}

	header := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, header); err != nil {
		t.Fatalf("failed to read auth header: %v", err)
	}
	if header[0] == MsgErrorResponse {
		errLen := binary.BigEndian.Uint32(header[1:5])
		errPayload := make([]byte, errLen-4)
		_, _ = io.ReadFull(clientConn, errPayload)
		return false, string(errPayload)
	}
	if header[0] != MsgAuth {
		t.Fatalf("expected MsgAuth 'R', got %c", header[0])
	}
	authPayload := make([]byte, 4)
	if _, err := io.ReadFull(clientConn, authPayload); err != nil {
		t.Fatalf("failed to read auth code: %v", err)
	}
	if binary.BigEndian.Uint32(authPayload) != 3 {
		t.Fatalf("expected CleartextPassword code 3, got %d", binary.BigEndian.Uint32(authPayload))
	}

	pwMsg := password + "\x00"
	pwBuf := make([]byte, 1+4+len(pwMsg))
	pwBuf[0] = 'p'
	binary.BigEndian.PutUint32(pwBuf[1:5], uint32(4+len(pwMsg)))
	copy(pwBuf[5:], pwMsg)
	if _, err := clientConn.Write(pwBuf); err != nil {
		t.Fatalf("failed to send password: %v", err)
	}

	if _, err := io.ReadFull(clientConn, header); err != nil {
		t.Fatalf("failed to read auth response header: %v", err)
	}
	if header[0] == MsgErrorResponse {
		errLen := binary.BigEndian.Uint32(header[1:5])
		errPayload := make([]byte, errLen-4)
		_, _ = io.ReadFull(clientConn, errPayload)
		return false, string(errPayload)
	}
	if header[0] == MsgAuth {
		if _, err := io.ReadFull(clientConn, authPayload); err != nil {
			t.Fatalf("failed to read auth ok code: %v", err)
		}
		if binary.BigEndian.Uint32(authPayload) == 0 {
			return true, ""
		}
	}
	return false, "unexpected response"
}

func TestWire_HandshakeAndSelectVersion(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	clientConn := connectPGClient(t, srv)

	rows := sendPGQuery(t, clientConn, "SELECT version();")
	if len(rows) == 0 {
		t.Fatalf("Expected at least one data row for SELECT version()")
	}
	if !strings.Contains(string(rows[0]), "SQLion") {
		t.Errorf("Expected version to contain SQLion, got: %s", string(rows[0]))
	}
}

func TestWire_SelectTableRecords(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	clientConn := connectPGClient(t, srv)

	rows := sendPGQuery(t, clientConn, "SELECT * FROM accounts")
	if len(rows) == 0 {
		t.Fatalf("Expected at least one data row from accounts")
	}
	if !strings.Contains(string(rows[0]), "Alice") {
		t.Errorf("Expected Alice in row, got: %s", string(rows[0]))
	}
}

func TestWire_BeekeeperDbGateQuery(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	clientConn := connectPGClient(t, srv)

	rows := sendPGQuery(t, clientConn, `SELECT "basetbl".* FROM "public"."accounts" "basetbl" LIMIT 50 OFFSET 0`)
	if len(rows) == 0 {
		t.Fatalf("Expected at least one data row returned for Beekeeper query")
	}
	if !strings.Contains(string(rows[0]), "Alice") {
		t.Errorf("Expected Alice in row, got: %s", string(rows[0]))
	}
}

func TestWire_ExtendedQueryProtocol(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// 1. Startup
	startupBuf := make([]byte, 8)
	binary.BigEndian.PutUint32(startupBuf[:4], 8)
	binary.BigEndian.PutUint32(startupBuf[4:8], 196608)
	_, _ = clientConn.Write(startupBuf)

	// Wait for Ready ('Z')
	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)
		if header[0] == MsgReadyForQuery {
			break
		}
	}

	// 2. Send Parse ('P'): stmtName="stmt1", query="SELECT * FROM accounts WHERE id = $1", 1 param (oid 23)
	stmtName := "stmt1\x00"
	queryStr := "SELECT * FROM accounts WHERE id = $1\x00"
	parsePayload := append([]byte(stmtName), []byte(queryStr)...)
	numParamsBuf := make([]byte, 6)
	binary.BigEndian.PutUint16(numParamsBuf[:2], 1)
	binary.BigEndian.PutUint32(numParamsBuf[2:], 23) // INT4
	parsePayload = append(parsePayload, numParamsBuf...)

	parseMsg := make([]byte, 5+len(parsePayload))
	parseMsg[0] = 'P'
	binary.BigEndian.PutUint32(parseMsg[1:5], uint32(4+len(parsePayload)))
	copy(parseMsg[5:], parsePayload)
	_, _ = clientConn.Write(parseMsg)

	// Expect ParseComplete ('1')
	header := make([]byte, 5)
	_, _ = io.ReadFull(clientConn, header)
	if header[0] != MsgParseComplete {
		t.Fatalf("Expected ParseComplete ('1'), got %c", header[0])
	}

	// 3. Send Bind ('B'): portalName="p1", stmtName="stmt1", 1 param ("1")
	portalName := "p1\x00"
	bindPayload := append([]byte(portalName), []byte(stmtName)...)
	// 0 format codes
	bindPayload = append(bindPayload, 0, 0)
	// 1 param
	paramHeader := make([]byte, 6)
	binary.BigEndian.PutUint16(paramHeader[:2], 1) // 1 param
	binary.BigEndian.PutUint32(paramHeader[2:6], 1) // len 1
	bindPayload = append(bindPayload, paramHeader...)
	bindPayload = append(bindPayload, '1') // val "1"
	// 0 result format codes
	bindPayload = append(bindPayload, 0, 0)

	bindMsg := make([]byte, 5+len(bindPayload))
	bindMsg[0] = 'B'
	binary.BigEndian.PutUint32(bindMsg[1:5], uint32(4+len(bindPayload)))
	copy(bindMsg[5:], bindPayload)
	_, _ = clientConn.Write(bindMsg)

	// Expect BindComplete ('2')
	_, _ = io.ReadFull(clientConn, header)
	if header[0] != MsgBindComplete {
		t.Fatalf("Expected BindComplete ('2'), got %c", header[0])
	}

	// 4. Send Describe Portal ('D' 'P' "p1\0")
	descPayload := []byte{'P', 'p', '1', 0}
	descMsg := make([]byte, 5+len(descPayload))
	descMsg[0] = 'D'
	binary.BigEndian.PutUint32(descMsg[1:5], uint32(4+len(descPayload)))
	copy(descMsg[5:], descPayload)
	_, _ = clientConn.Write(descMsg)

	// Expect RowDescription ('T')
	_, _ = io.ReadFull(clientConn, header)
	msgLen := binary.BigEndian.Uint32(header[1:5])
	payload := make([]byte, msgLen-4)
	_, _ = io.ReadFull(clientConn, payload)
	if header[0] != MsgRowDescription {
		t.Fatalf("Expected RowDescription ('T'), got %c", header[0])
	}

	// 5. Send Execute ('E' "p1\0" maxRows=0)
	execPayload := []byte{'p', '1', 0, 0, 0, 0, 0}
	execMsg := make([]byte, 5+len(execPayload))
	execMsg[0] = 'E'
	binary.BigEndian.PutUint32(execMsg[1:5], uint32(4+len(execPayload)))
	copy(execMsg[5:], execPayload)
	_, _ = clientConn.Write(execMsg)

	// Read DataRow & CommandComplete from Execute
	var dataRowReceived bool
	for {
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)

		if header[0] == MsgDataRow {
			dataRowReceived = true
			if !strings.Contains(string(payload), "Alice") {
				t.Errorf("Expected Alice in data row, got %s", string(payload))
			}
		}
		if header[0] == MsgCommandComplete {
			break
		}
	}

	if !dataRowReceived {
		t.Fatalf("Expected DataRow from Execute portal")
	}

	// Send Sync ('S')
	syncMsg := []byte{'S', 0, 0, 0, 4}
	_, _ = clientConn.Write(syncMsg)

	// Receive ReadyForQuery ('Z')
	_, _ = io.ReadFull(clientConn, header)
	msgLen = binary.BigEndian.Uint32(header[1:5])
	payload = make([]byte, msgLen-4)
	_, _ = io.ReadFull(clientConn, payload)
	if header[0] != MsgReadyForQuery {
		t.Fatalf("Expected ReadyForQuery ('Z'), got %c", header[0])
	}

	// 6. Send Close Portal ('C' 'P' "p1\0")
	closePayload := []byte{'P', 'p', '1', 0}
	closeMsg := make([]byte, 5+len(closePayload))
	closeMsg[0] = 'C'
	binary.BigEndian.PutUint32(closeMsg[1:5], uint32(4+len(closePayload)))
	copy(closeMsg[5:], closePayload)
	_, _ = clientConn.Write(closeMsg)

	// Expect CloseComplete ('3')
	_, _ = io.ReadFull(clientConn, header)
	if header[0] != MsgCloseComplete {
		t.Fatalf("Expected CloseComplete ('3'), got %c", header[0])
	}
}

func TestWire_ConcurrentConnectionMultiplexing(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)

	const numClients = 30
	var wg sync.WaitGroup
	wg.Add(numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			defer wg.Done()

			clientConn := connectPGClient(t, srv)
			rows := sendPGQuery(t, clientConn, "SELECT * FROM accounts;")
			if len(rows) == 0 {
				t.Errorf("client %d did not receive data rows", clientID)
			}
		}(i)
	}

	wg.Wait()
}

func TestWire_BackendKeyDataAndCancelRequest(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)

	// 1. Establish regular client connection
	c1Client, c1Server := net.Pipe()
	defer c1Client.Close()
	defer c1Server.Close()

	go srv.handleConnection(c1Server)

	// Send StartupMessage
	startup := make([]byte, 8)
	binary.BigEndian.PutUint32(startup[:4], 8)
	binary.BigEndian.PutUint32(startup[4:8], 196608) // 3.0
	if _, err := c1Client.Write(startup); err != nil {
		t.Fatalf("Failed to write startup: %v", err)
	}

	var gotBackendKeyData bool
	var pid, secretKey uint32
	params := make(map[string]string)

	header := make([]byte, 5)
	for {
		if _, err := io.ReadFull(c1Client, header); err != nil {
			t.Fatalf("Failed to read header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(c1Client, payload); err != nil {
			t.Fatalf("Failed to read payload: %v", err)
		}

		if msgType == MsgBackendKeyData {
			gotBackendKeyData = true
			pid = binary.BigEndian.Uint32(payload[:4])
			secretKey = binary.BigEndian.Uint32(payload[4:8])
		} else if msgType == MsgParameterStatus {
			parts := strings.Split(string(payload), "\x00")
			if len(parts) >= 2 {
				params[parts[0]] = parts[1]
			}
		} else if msgType == MsgReadyForQuery {
			break
		}
	}

	if !gotBackendKeyData {
		t.Fatalf("Expected MsgBackendKeyData ('K'), but did not receive it")
	}
	if pid == 0 || secretKey == 0 {
		t.Fatalf("Invalid pid=%d or secretKey=%d", pid, secretKey)
	}

	// Verify parameter status suite
	expectedParams := []string{
		"server_version", "server_encoding", "client_encoding",
		"is_superuser", "session_authorization", "integer_datetimes",
		"DateStyle", "IntervalStyle", "standard_conforming_strings", "TimeZone",
	}
	for _, k := range expectedParams {
		if _, ok := params[k]; !ok {
			t.Errorf("Missing expected ParameterStatus: %s", k)
		}
	}

	// 2. Send CancelRequest on a separate connection
	cancelClient, cancelServer := net.Pipe()
	defer cancelClient.Close()
	defer cancelServer.Close()

	go srv.handleConnection(cancelServer)

	cancelBuf := make([]byte, 16)
	binary.BigEndian.PutUint32(cancelBuf[0:4], 16)
	binary.BigEndian.PutUint32(cancelBuf[4:8], 80877102) // CancelRequest code
	binary.BigEndian.PutUint32(cancelBuf[8:12], pid)
	binary.BigEndian.PutUint32(cancelBuf[12:16], secretKey)

	if _, err := cancelClient.Write(cancelBuf); err != nil {
		t.Fatalf("Failed to write cancel request: %v", err)
	}

	// The server must process cancellation and immediately close the connection with EOF
	readBuf := make([]byte, 1)
	n, err := cancelClient.Read(readBuf)
	if err != io.EOF && n != 0 {
		t.Errorf("Expected EOF from CancelRequest connection, got n=%d, err=%v", n, err)
	}
}

func TestWire_CompileQueryToProgram(t *testing.T) {
	server, eng, _ := setupTestWireServer(t)
	defer eng.Close()
	defer server.Close()

	prog1, err := server.compileQueryToProgram("SELECT id, holder FROM accounts WHERE id = $1")
	if err != nil {
		t.Fatalf("compileQueryToProgram failed: %v", err)
	}
	if prog1 == nil {
		t.Fatalf("expected non-nil program")
	}
	dis := prog1.Disassemble()
	if !strings.Contains(dis, "POINT_SELECT") && !strings.Contains(dis, "REL_QUERY") {
		t.Fatalf("expected POINT_SELECT or REL_QUERY opcode in program, got:\n%s", dis)
	}

	// Repeated call must return identical cached program pointer
	prog2, err := server.compileQueryToProgram("SELECT id, holder FROM accounts WHERE id = $1")
	if err != nil {
		t.Fatalf("second compileQueryToProgram failed: %v", err)
	}
	if prog1 != prog2 {
		t.Fatalf("expected identical *proc.Program pointer from PlanCache, got different instances (%p vs %p)", prog1, prog2)
	}
}

func TestWire_PlanCacheSharing(t *testing.T) {
	server1, eng, cat := setupTestWireServer(t)
	defer eng.Close()
	defer server1.Close()

	sharedCache := runtime.NewPlanCache()
	server1.SetBytecodePlanCache(sharedCache)
	server2 := NewServerWithCache("127.0.0.1:0", cat, eng, sharedCache)
	defer server2.Close()

	if server1.BytecodePlanCache() != sharedCache {
		t.Fatalf("server1 BytecodePlanCache does not match sharedCache")
	}
	if server2.BytecodePlanCache() != sharedCache {
		t.Fatalf("server2 BytecodePlanCache does not match sharedCache")
	}

	query := "SELECT id, holder FROM accounts WHERE id = $1"
	p1, err := server1.compileQueryToProgram(query)
	if err != nil {
		t.Fatalf("server1 compile failed: %v", err)
	}

	p2, err := server2.compileQueryToProgram(query)
	if err != nil {
		t.Fatalf("server2 compile failed: %v", err)
	}

	if p1 != p2 {
		t.Fatalf("expected cross-server plan cache hit (%p == %p)", p1, p2)
	}
}

func TestWire_PgCatalogAndInformationSchema(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)
	defer eng.Close()
	defer srv.Close()

	clientConn := connectPGClient(t, srv)

	// 1. Query pg_catalog.pg_class
	rows := sendPGQuery(t, clientConn, "SELECT relname FROM pg_catalog.pg_class WHERE relname = 'accounts'")
	if len(rows) == 0 {
		t.Fatalf("expected to receive DataRow for pg_class query")
	}

	// 2. Query information_schema.columns (accounts has 3 columns: id, holder, balance)
	colRows := sendPGQuery(t, clientConn, "SELECT column_name, data_type FROM information_schema.columns WHERE table_name = 'accounts'")
	if len(colRows) != 3 {
		t.Fatalf("expected 3 rows from information_schema.columns, got %d", len(colRows))
	}
}

func TestWire_TransactionStatusAndSQLState(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)
	defer eng.Close()
	defer srv.Close()

	clientConn := connectPGClient(t, srv)

	// Helper to send query and receive ready status byte
	sendQuery := func(sql string) (byte, []byte, error) {
		q := sql + "\x00"
		qBuf := make([]byte, 1+4+len(q))
		qBuf[0] = 'Q'
		binary.BigEndian.PutUint32(qBuf[1:5], uint32(4+len(q)))
		copy(qBuf[5:], q)
		if _, err := clientConn.Write(qBuf); err != nil {
			return 0, nil, err
		}

		var lastStatus byte
		var lastErrorPayload []byte
		for {
			header := make([]byte, 5)
			if _, err := io.ReadFull(clientConn, header); err != nil {
				return 0, nil, err
			}
			msgType := header[0]
			msgLen := binary.BigEndian.Uint32(header[1:5])
			payload := make([]byte, msgLen-4)
			if _, err := io.ReadFull(clientConn, payload); err != nil {
				return 0, nil, err
			}
			if msgType == MsgErrorResponse {
				lastErrorPayload = payload
			}
			if msgType == MsgReadyForQuery {
				lastStatus = payload[0]
				break
			}
		}
		return lastStatus, lastErrorPayload, nil
	}

	// 1. Send BEGIN -> status should become 'T' (in transaction)
	status, _, err := sendQuery("BEGIN")
	if err != nil {
		t.Fatalf("failed to send BEGIN: %v", err)
	}
	if status != 'T' {
		t.Fatalf("expected status 'T' after BEGIN, got %c", status)
	}

	// 2. Send query with error inside transaction -> status should become 'E' (failed transaction)
	status, errPayload, err := sendQuery("SELECT * FROM non_existing_table_xyz")
	if err != nil {
		t.Fatalf("failed to send erroneous query: %v", err)
	}
	if status != 'E' {
		t.Fatalf("expected status 'E' after error in transaction, got %c", status)
	}
	// Verify ErrorResponse contains SQLSTATE '42P01' (undefined_table)
	errStr := string(errPayload)
	if !strings.Contains(errStr, "42P01") {
		t.Fatalf("expected ErrorResponse to contain SQLSTATE 42P01, got %q", errStr)
	}

	// 3. Send command in aborted transaction -> should be rejected and stay 'E'
	status, _, err = sendQuery("SELECT 1")
	if err != nil {
		t.Fatalf("failed to send query in aborted tx: %v", err)
	}
	if status != 'E' {
		t.Fatalf("expected status to stay 'E', got %c", status)
	}

	// 4. Send ROLLBACK -> transaction terminates, status should return to 'I' (idle)
	status, _, err = sendQuery("ROLLBACK")
	if err != nil {
		t.Fatalf("failed to send ROLLBACK: %v", err)
	}
	if status != 'I' {
		t.Fatalf("expected status 'I' after ROLLBACK, got %c", status)
	}

	// 5. Test COMMIT flow: BEGIN -> query -> COMMIT -> status 'I'
	status, _, _ = sendQuery("BEGIN")
	if status != 'T' {
		t.Fatalf("expected status 'T' after second BEGIN, got %c", status)
	}
	status, _, _ = sendQuery("COMMIT")
	if status != 'I' {
		t.Fatalf("expected status 'I' after COMMIT, got %c", status)
	}
}

func TestWire_PasswordAuthentication(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)
	defer eng.Close()
	defer srv.Close()

	authSvc := srv.coordinator.AuthService()
	if _, err := authSvc.CreateLogin("alice", "mypassword123"); err != nil {
		t.Fatalf("failed to create login: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		ok, errPayload := authenticatePGClient(t, srv, "alice", "mypassword123")
		if !ok {
			t.Fatalf("expected auth success, got error: %s", errPayload)
		}
	})

	t.Run("Failure", func(t *testing.T) {
		ok, errPayload := authenticatePGClient(t, srv, "alice", "wrong_password")
		if ok || !strings.Contains(errPayload, "28P01") {
			t.Fatalf("expected 28P01 failure, got ok=%v, payload=%q", ok, errPayload)
		}
	})

	t.Run("GlobalAuthSuperuser", func(t *testing.T) {
		coord, ok := srv.coordinator.(*engine.Coordinator)
		if ok {
			coord.SetAuthService(authSvc, true)
		}
		_, _ = authSvc.EnsureDefaultSuperuser("admin@lionsql.local", "SuperUnifiedPass99!")

		// 3.1: user=postgres with correct superuser password must succeed
		if ok, err := authenticatePGClient(t, srv, "postgres", "SuperUnifiedPass99!"); !ok {
			t.Fatalf("expected superuser auth success, got error: %s", err)
		}

		// 3.2: user=postgres with wrong password must be rejected
		if ok, _ := authenticatePGClient(t, srv, "postgres", "WrongPass!"); ok {
			t.Fatalf("expected auth failure for wrong superuser password")
		}
	})
}

func TestPGWire_ServerSideSQLCursor(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	clientConn := connectPGClient(t, srv)

	sendQuery := func(q string) (string, []string) {
		qBytes := append([]byte(q), 0)
		msg := make([]byte, 5+len(qBytes))
		msg[0] = 'Q'
		binary.BigEndian.PutUint32(msg[1:5], uint32(4+len(qBytes)))
		copy(msg[5:], qBytes)
		_, _ = clientConn.Write(msg)

		var completeTag string
		var rows []string

		for {
			hdr := make([]byte, 5)
			_, err := io.ReadFull(clientConn, hdr)
			if err != nil {
				t.Fatalf("failed to read response: %v", err)
			}
			l := binary.BigEndian.Uint32(hdr[1:5])
			pay := make([]byte, l-4)
			_, _ = io.ReadFull(clientConn, pay)

			if hdr[0] == MsgDataRow {
				rows = append(rows, string(pay))
			} else if hdr[0] == MsgCommandComplete {
				completeTag = string(bytes.TrimRight(pay, "\x00"))
			} else if hdr[0] == MsgReadyForQuery {
				break
			}
		}
		return completeTag, rows
	}

	sendQuery("INSERT INTO accounts (id, holder, balance) VALUES (2, 'Bob', 2000);")

	// 1. Begin transaction & Declare cursor
	tag, _ := sendQuery("BEGIN;")
	if tag != "BEGIN" {
		t.Fatalf("expected BEGIN, got %q", tag)
	}

	tag, _ = sendQuery("DECLARE c_cur CURSOR FOR SELECT id, holder FROM accounts ORDER BY id;")
	if tag != "DECLARE CURSOR" {
		t.Fatalf("expected DECLARE CURSOR, got %q", tag)
	}

	// 2. Fetch 1 row
	tag, rows := sendQuery("FETCH 1 FROM c_cur;")
	if tag != "FETCH 1" || len(rows) != 1 {
		t.Fatalf("expected FETCH 1 with 1 row, got tag=%q rows=%d", tag, len(rows))
	}
	if !strings.Contains(rows[0], "Alice") {
		t.Fatalf("expected Alice, got %s", rows[0])
	}

	// 3. Fetch 1 row (Bob)
	tag, rows = sendQuery("FETCH 1 FROM c_cur;")
	if tag != "FETCH 1" || len(rows) != 1 {
		t.Fatalf("expected FETCH 1 with 1 row, got tag=%q rows=%d", tag, len(rows))
	}
	if !strings.Contains(rows[0], "Bob") {
		t.Fatalf("expected Bob, got %s", rows[0])
	}

	// 4. Fetch next (EOF)
	tag, rows = sendQuery("FETCH 1 FROM c_cur;")
	if tag != "FETCH 0" || len(rows) != 0 {
		t.Fatalf("expected FETCH 0 with 0 rows, got tag=%q rows=%d", tag, len(rows))
	}

	// 5. Close cursor
	tag, _ = sendQuery("CLOSE c_cur;")
	if tag != "CLOSE CURSOR" {
		t.Fatalf("expected CLOSE CURSOR, got %q", tag)
	}

	sendQuery("COMMIT;")
}

func TestPGWire_ExtendedQueryPortalBatching(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// Startup
	startupBuf := make([]byte, 8)
	binary.BigEndian.PutUint32(startupBuf[:4], 8)
	binary.BigEndian.PutUint32(startupBuf[4:8], 196608)
	_, _ = clientConn.Write(startupBuf)

	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)
		if header[0] == MsgReadyForQuery {
			break
		}
	}

	// Populate additional rows in accounts
	initQuery := "INSERT INTO accounts (id, holder, balance) VALUES (2, 'Bob', 200), (3, 'Charlie', 300), (4, 'David', 400), (5, 'Eve', 500);"
	initQBytes := append([]byte(initQuery), 0)
	qMsg := make([]byte, 5+len(initQBytes))
	qMsg[0] = 'Q'
	binary.BigEndian.PutUint32(qMsg[1:5], uint32(4+len(initQBytes)))
	copy(qMsg[5:], initQBytes)
	_, _ = clientConn.Write(qMsg)

	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)
		if header[0] == MsgReadyForQuery {
			break
		}
	}

	// 1. Send Parse ('P'): stmt="s_page", query="SELECT id, holder FROM accounts ORDER BY id;"
	stmtName := "s_page\x00"
	queryStr := "SELECT id, holder FROM accounts ORDER BY id\x00"
	parsePayload := append([]byte(stmtName), []byte(queryStr)...)
	parsePayload = append(parsePayload, 0, 0) // 0 params
	pMsg := make([]byte, 5+len(parsePayload))
	pMsg[0] = 'P'
	binary.BigEndian.PutUint32(pMsg[1:5], uint32(4+len(parsePayload)))
	copy(pMsg[5:], parsePayload)
	_, _ = clientConn.Write(pMsg)

	// Expect ParseComplete ('1')
	hdr := make([]byte, 5)
	_, _ = io.ReadFull(clientConn, hdr)
	if hdr[0] != MsgParseComplete {
		t.Fatalf("expected ParseComplete, got %c", hdr[0])
	}

	// 2. Send Bind ('B'): portal="p_page", stmt="s_page", 0 params, 0 format codes
	bindPayload := append([]byte("p_page\x00"), []byte(stmtName)...)
	bindPayload = append(bindPayload, 0, 0) // 0 format codes
	bindPayload = append(bindPayload, 0, 0) // 0 params
	bindPayload = append(bindPayload, 0, 0) // 0 result format codes
	bMsg := make([]byte, 5+len(bindPayload))
	bMsg[0] = 'B'
	binary.BigEndian.PutUint32(bMsg[1:5], uint32(4+len(bindPayload)))
	copy(bMsg[5:], bindPayload)
	_, _ = clientConn.Write(bMsg)

	// Expect BindComplete ('2')
	_, _ = io.ReadFull(clientConn, hdr)
	if hdr[0] != MsgBindComplete {
		t.Fatalf("expected BindComplete, got %c", hdr[0])
	}

	sendExecute := func(portal string, maxRows int32) (lastMsg byte, dataRows int, tag string) {
		pNameBytes := append([]byte(portal), 0)
		ePayload := make([]byte, len(pNameBytes)+4)
		copy(ePayload, pNameBytes)
		binary.BigEndian.PutUint32(ePayload[len(pNameBytes):], uint32(maxRows))

		eMsg := make([]byte, 5+len(ePayload))
		eMsg[0] = 'E'
		binary.BigEndian.PutUint32(eMsg[1:5], uint32(4+len(ePayload)))
		copy(eMsg[5:], ePayload)
		_, _ = clientConn.Write(eMsg)

		for {
			h := make([]byte, 5)
			_, _ = io.ReadFull(clientConn, h)
			l := binary.BigEndian.Uint32(h[1:5])
			p := make([]byte, l-4)
			_, _ = io.ReadFull(clientConn, p)

			if h[0] == MsgDataRow {
				dataRows++
			} else if h[0] == MsgPortalSuspended {
				return MsgPortalSuspended, dataRows, ""
			} else if h[0] == MsgCommandComplete {
				return MsgCommandComplete, dataRows, string(bytes.TrimRight(p, "\x00"))
			}
		}
	}

	// Batch 1: maxRows = 2 (Alice, Bob) -> expect PortalSuspended ('s')
	lastMsg, count, _ := sendExecute("p_page", 2)
	if lastMsg != MsgPortalSuspended || count != 2 {
		t.Fatalf("batch 1: expected PortalSuspended with 2 rows, got msg=%c count=%d", lastMsg, count)
	}

	// Batch 2: maxRows = 2 (Charlie, David) -> expect PortalSuspended ('s')
	lastMsg, count, _ = sendExecute("p_page", 2)
	if lastMsg != MsgPortalSuspended || count != 2 {
		t.Fatalf("batch 2: expected PortalSuspended with 2 rows, got msg=%c count=%d", lastMsg, count)
	}

	// Batch 3: maxRows = 2 (Eve, total=5) -> all rows finished, expect CommandComplete ('C')
	lastMsg, count, tag := sendExecute("p_page", 2)
	if lastMsg != MsgCommandComplete || count != 1 {
		t.Fatalf("batch 3: expected CommandComplete with 1 row, got msg=%c count=%d (tag: %s)", lastMsg, count, tag)
	}
	if tag != "SELECT 5" {
		t.Fatalf("expected tag SELECT 5, got %q", tag)
	}

	// Close portal
	closePayload := []byte{'P', 'p', 'p', 'a', 'g', 'e', 0}
	cMsg := make([]byte, 5+len(closePayload))
	cMsg[0] = 'C'
	binary.BigEndian.PutUint32(cMsg[1:5], uint32(4+len(closePayload)))
	copy(cMsg[5:], closePayload)
	_, _ = clientConn.Write(cMsg)

	_, _ = io.ReadFull(clientConn, hdr)
	if hdr[0] != MsgCloseComplete {
		t.Fatalf("expected CloseComplete, got %c", hdr[0])
	}
}

func TestPGWire_BinaryBindDirectValueDecoding(t *testing.T) {
	// 1. INT4 (OID 23)
	int4Buf := make([]byte, 4)
	binary.BigEndian.PutUint32(int4Buf, uint32(int32(12345)))
	vInt4 := decodeBinaryParamValue(int4Buf, 23)
	if vInt4.Type != storage.TypeInt || vInt4.IVal != 12345 {
		t.Fatalf("expected INT4 12345, got %v (%s)", vInt4, vInt4.Type)
	}

	// 2. INT8 (OID 20)
	int8Buf := make([]byte, 8)
	binary.BigEndian.PutUint64(int8Buf, uint64(int64(9876543210)))
	vInt8 := decodeBinaryParamValue(int8Buf, 20)
	if vInt8.Type != storage.TypeInt || vInt8.IVal != 9876543210 {
		t.Fatalf("expected INT8 9876543210, got %v (%s)", vInt8, vInt8.Type)
	}

	// 3. FLOAT8 (OID 701)
	float8Buf := make([]byte, 8)
	binary.BigEndian.PutUint64(float8Buf, math.Float64bits(3.141592))
	vFloat8 := decodeBinaryParamValue(float8Buf, 701)
	if vFloat8.Type != storage.TypeFloat || math.Abs(vFloat8.FVal-3.141592) > 1e-6 {
		t.Fatalf("expected FLOAT8 3.141592, got %v", vFloat8)
	}

	// 4. BOOL (OID 16)
	vBoolTrue := decodeBinaryParamValue([]byte{1}, 16)
	if vBoolTrue.Type != storage.TypeBool || !vBoolTrue.BVal {
		t.Fatalf("expected BOOL true, got %v", vBoolTrue)
	}
	vBoolFalse := decodeBinaryParamValue([]byte{0}, 16)
	if vBoolFalse.Type != storage.TypeBool || vBoolFalse.BVal {
		t.Fatalf("expected BOOL false, got %v", vBoolFalse)
	}

	// 5. Text parameter decoding
	vTextInt := decodeTextParamValue("42", 23)
	if vTextInt.Type != storage.TypeInt || vTextInt.IVal != 42 {
		t.Fatalf("expected parsed INT4 42, got %v", vTextInt)
	}
	vTextFloat := decodeTextParamValue("123.45", 701)
	if vTextFloat.Type != storage.TypeFloat || math.Abs(vTextFloat.FVal-123.45) > 1e-6 {
		t.Fatalf("expected parsed FLOAT8 123.45, got %v", vTextFloat)
	}

	// 6. Static param name lookup
	p, at, dollar := getParamNames(1)
	if p != "p1" || at != "@p1" || dollar != "$1" {
		t.Fatalf("unexpected param names: %s, %s, %s", p, at, dollar)
	}
	p64, at64, dollar64 := getParamNames(64)
	if p64 != "p64" || at64 != "@p64" || dollar64 != "$64" {
		t.Fatalf("unexpected param names for 64: %s, %s, %s", p64, at64, dollar64)
	}
	p65, at65, dollar65 := getParamNames(65)
	if p65 != "p65" || at65 != "@p65" || dollar65 != "$65" {
		t.Fatalf("unexpected param names for 65: %s, %s, %s", p65, at65, dollar65)
	}
}

