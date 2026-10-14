package wire

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"sqlion/pkg/catalog"
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

func TestWire_HandshakeAndSelectVersion(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// 1. Send StartupMessage (length 8: 8, protocol version 3.0: 196608)
	startupBuf := make([]byte, 8)
	binary.BigEndian.PutUint32(startupBuf[:4], 8)
	binary.BigEndian.PutUint32(startupBuf[4:8], 196608) // 3.0
	if _, err := clientConn.Write(startupBuf); err != nil {
		t.Fatalf("Failed to write startup: %v", err)
	}

	// Read until ReadyForQuery ('Z')
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, header); err != nil {
			t.Fatalf("Failed to read header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(clientConn, payload); err != nil {
			t.Fatalf("Failed to read payload: %v", err)
		}
		if msgType == MsgReadyForQuery {
			break
		}
	}

	// 2. Send Simple Query: 'Q' "SELECT version();\0"
	query := "SELECT version();\x00"
	queryBuf := make([]byte, 1+4+len(query))
	queryBuf[0] = 'Q'
	binary.BigEndian.PutUint32(queryBuf[1:5], uint32(4+len(query)))
	copy(queryBuf[5:], query)

	if _, err := clientConn.Write(queryBuf); err != nil {
		t.Fatalf("Failed to send query: %v", err)
	}

	// 3. Receive RowDescription, DataRow, CommandComplete, ReadyForQuery
	var versionOutput string
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, header); err != nil {
			t.Fatalf("Failed to read response header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(clientConn, payload); err != nil {
			t.Fatalf("Failed to read response payload: %v", err)
		}

		if msgType == MsgDataRow {
			versionOutput = string(payload)
		}
		if msgType == MsgReadyForQuery {
			break
		}
	}

	if !strings.Contains(versionOutput, "SQLion") {
		t.Errorf("Expected version to contain SQLion, got: %s", versionOutput)
	}
}

func TestWire_SelectTableRecords(t *testing.T) {
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

	// Wait for Ready
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

	// Query: SELECT * FROM accounts
	query := "SELECT * FROM accounts\x00"
	queryBuf := make([]byte, 1+4+len(query))
	queryBuf[0] = 'Q'
	binary.BigEndian.PutUint32(queryBuf[1:5], uint32(4+len(query)))
	copy(queryBuf[5:], query)
	_, _ = clientConn.Write(queryBuf)

	var rowFound bool
	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)

		if header[0] == MsgDataRow {
			rowFound = true
			if !strings.Contains(string(payload), "Alice") {
				t.Errorf("Expected Alice in row, got %s", string(payload))
			}
		}
		if header[0] == MsgReadyForQuery {
			break
		}
	}

	if !rowFound {
		t.Errorf("Expected at least one data row returned")
	}
}

func TestWire_BeekeeperDbGateQuery(t *testing.T) {
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

	// Wait for Ready
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

	// Beekeeper exact query
	query := "SELECT \"basetbl\".* FROM \"public\".\"accounts\" \"basetbl\" LIMIT 50 OFFSET 0\x00"
	queryBuf := make([]byte, 1+4+len(query))
	queryBuf[0] = 'Q'
	binary.BigEndian.PutUint32(queryBuf[1:5], uint32(4+len(query)))
	copy(queryBuf[5:], query)
	_, _ = clientConn.Write(queryBuf)

	var rowFound bool
	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)

		if header[0] == MsgDataRow {
			rowFound = true
			if !strings.Contains(string(payload), "Alice") {
				t.Errorf("Expected Alice in row, got %s", string(payload))
			}
		}
		if header[0] == MsgReadyForQuery {
			break
		}
	}

	if !rowFound {
		t.Errorf("Expected at least one data row returned for Beekeeper query")
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

			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()

			go srv.handleConnection(serverConn)

			// 1. Startup message
			startupBuf := make([]byte, 8)
			binary.BigEndian.PutUint32(startupBuf[:4], 8)
			binary.BigEndian.PutUint32(startupBuf[4:8], 196608) // 3.0
			if _, err := clientConn.Write(startupBuf); err != nil {
				t.Errorf("client %d failed write startup: %v", clientID, err)
				return
			}

			// Read until ReadyForQuery ('Z')
			header := make([]byte, 5)
			for {
				if _, err := io.ReadFull(clientConn, header); err != nil {
					t.Errorf("client %d handshake read header failed: %v", clientID, err)
					return
				}
				msgLen := binary.BigEndian.Uint32(header[1:5])
				payload := make([]byte, msgLen-4)
				if _, err := io.ReadFull(clientConn, payload); err != nil {
					t.Errorf("client %d handshake read payload failed: %v", clientID, err)
					return
				}
				if header[0] == MsgReadyForQuery {
					break
				}
			}

			// 2. Execute query
			query := "SELECT * FROM accounts;\x00"
			queryBuf := make([]byte, 1+4+len(query))
			queryBuf[0] = 'Q'
			binary.BigEndian.PutUint32(queryBuf[1:5], uint32(4+len(query)))
			copy(queryBuf[5:], query)

			if _, err := clientConn.Write(queryBuf); err != nil {
				t.Errorf("client %d send query failed: %v", clientID, err)
				return
			}

			gotComplete := false
			for {
				if _, err := io.ReadFull(clientConn, header); err != nil {
					t.Errorf("client %d query response read failed: %v", clientID, err)
					return
				}
				msgLen := binary.BigEndian.Uint32(header[1:5])
				payload := make([]byte, msgLen-4)
				if _, err := io.ReadFull(clientConn, payload); err != nil {
					t.Errorf("client %d payload read failed: %v", clientID, err)
					return
				}
				if header[0] == MsgCommandComplete {
					gotComplete = true
				}
				if header[0] == MsgReadyForQuery {
					break
				}
			}

			if !gotComplete {
				t.Errorf("client %d did not receive CommandComplete", clientID)
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

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// Startup
	startupBuf := make([]byte, 8)
	binary.BigEndian.PutUint32(startupBuf[:4], 8)
	binary.BigEndian.PutUint32(startupBuf[4:8], 196608)
	_, _ = clientConn.Write(startupBuf)

	// Wait for Ready
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

	// 1. Query pg_catalog.pg_class
	q := "SELECT relname FROM pg_catalog.pg_class WHERE relname = 'accounts'\x00"
	qBuf := make([]byte, 1+4+len(q))
	qBuf[0] = 'Q'
	binary.BigEndian.PutUint32(qBuf[1:5], uint32(4+len(q)))
	copy(qBuf[5:], q)
	_, _ = clientConn.Write(qBuf)

	foundRow := false
	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)
		if header[0] == MsgDataRow {
			foundRow = true
		}
		if header[0] == MsgReadyForQuery {
			break
		}
	}
	if !foundRow {
		t.Fatalf("expected to receive DataRow for pg_class query")
	}

	// 2. Query information_schema.columns
	q2 := "SELECT column_name, data_type FROM information_schema.columns WHERE table_name = 'accounts'\x00"
	q2Buf := make([]byte, 1+4+len(q2))
	q2Buf[0] = 'Q'
	binary.BigEndian.PutUint32(q2Buf[1:5], uint32(4+len(q2)))
	copy(q2Buf[5:], q2)
	_, _ = clientConn.Write(q2Buf)

	rowCount := 0
	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)
		if header[0] == MsgDataRow {
			rowCount++
		}
		if header[0] == MsgReadyForQuery {
			break
		}
	}
	// accounts has 3 columns: id, holder, balance
	if rowCount != 3 {
		t.Fatalf("expected 3 rows from information_schema.columns, got %d", rowCount)
	}
}

func TestWire_TransactionStatusAndSQLState(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)
	defer eng.Close()
	defer srv.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// Startup
	startupBuf := make([]byte, 8)
	binary.BigEndian.PutUint32(startupBuf[:4], 8)
	binary.BigEndian.PutUint32(startupBuf[4:8], 196608)
	_, _ = clientConn.Write(startupBuf)

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

	// Wait for initial ReadyForQuery
	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)
		if header[0] == MsgReadyForQuery {
			if payload[0] != 'I' {
				t.Fatalf("expected initial status 'I', got %c", payload[0])
			}
			break
		}
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
	srv, eng, cat := setupTestWireServer(t)
	defer eng.Close()
	defer srv.Close()

	// Register login with password in auth service
	authSvc := srv.coordinator.AuthService()
	_, err := authSvc.CreateLogin("alice", "mypassword123")
	if err != nil {
		t.Fatalf("failed to create login: %v", err)
	}

	// Helper to build StartupMessage with parameters
	buildStartup := func(params map[string]string) []byte {
		var payload []byte
		for k, v := range params {
			payload = append(payload, []byte(k)...)
			payload = append(payload, 0)
			payload = append(payload, []byte(v)...)
			payload = append(payload, 0)
		}
		payload = append(payload, 0) // terminating zero
		msgLen := 4 + 4 + len(payload)
		buf := make([]byte, msgLen)
		binary.BigEndian.PutUint32(buf[:4], uint32(msgLen))
		binary.BigEndian.PutUint32(buf[4:8], 196608) // 3.0
		copy(buf[8:], payload)
		return buf
	}

	// Subtest 1: Successful Password Authentication
	t.Run("Success", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		go srv.handleConnection(serverConn)

		// Send Startup with user: alice
		startup := buildStartup(map[string]string{"user": "alice", "database": "sqlion"})
		if _, err := clientConn.Write(startup); err != nil {
			t.Fatalf("failed to send startup: %v", err)
		}

		// Read Auth Request: expect MsgAuth ('R') code 3 (CleartextPassword)
		header := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, header); err != nil {
			t.Fatalf("failed to read auth request header: %v", err)
		}
		if header[0] != MsgAuth {
			t.Fatalf("expected MsgAuth 'R', got %c", header[0])
		}
		authPayload := make([]byte, 4)
		if _, err := io.ReadFull(clientConn, authPayload); err != nil {
			t.Fatalf("failed to read auth code: %v", err)
		}
		authCode := binary.BigEndian.Uint32(authPayload)
		if authCode != 3 {
			t.Fatalf("expected CleartextPassword code 3, got %d", authCode)
		}

		// Send PasswordMessage ('p') with correct password
		pwMsg := "mypassword123\x00"
		pwBuf := make([]byte, 1+4+len(pwMsg))
		pwBuf[0] = 'p'
		binary.BigEndian.PutUint32(pwBuf[1:5], uint32(4+len(pwMsg)))
		copy(pwBuf[5:], pwMsg)
		if _, err := clientConn.Write(pwBuf); err != nil {
			t.Fatalf("failed to send password: %v", err)
		}

		// Read AuthOK ('R' code 0)
		if _, err := io.ReadFull(clientConn, header); err != nil {
			t.Fatalf("failed to read auth ok header: %v", err)
		}
		if header[0] != MsgAuth {
			t.Fatalf("expected MsgAuth 'R' for AuthOK, got %c", header[0])
		}
		if _, err := io.ReadFull(clientConn, authPayload); err != nil {
			t.Fatalf("failed to read auth ok code: %v", err)
		}
		if binary.BigEndian.Uint32(authPayload) != 0 {
			t.Fatalf("expected AuthOK code 0, got %d", binary.BigEndian.Uint32(authPayload))
		}

		// Drain until ReadyForQuery ('Z')
		for {
			h := make([]byte, 5)
			if _, err := io.ReadFull(clientConn, h); err != nil {
				t.Fatalf("failed to drain until ready: %v", err)
			}
			msgLen := binary.BigEndian.Uint32(h[1:5])
			p := make([]byte, msgLen-4)
			if _, err := io.ReadFull(clientConn, p); err != nil {
				t.Fatalf("failed to read payload: %v", err)
			}
			if h[0] == MsgReadyForQuery {
				break
			}
		}
	})

	// Subtest 2: Failed Password Authentication
	t.Run("Failure", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		go srv.handleConnection(serverConn)

		// Send Startup with user: alice
		startup := buildStartup(map[string]string{"user": "alice", "database": "sqlion"})
		_, _ = clientConn.Write(startup)

		// Read Auth Request (code 3)
		header := make([]byte, 9)
		_, _ = io.ReadFull(clientConn, header)

		// Send wrong password
		pwMsg := "wrong_password\x00"
		pwBuf := make([]byte, 1+4+len(pwMsg))
		pwBuf[0] = 'p'
		binary.BigEndian.PutUint32(pwBuf[1:5], uint32(4+len(pwMsg)))
		copy(pwBuf[5:], pwMsg)
		_, _ = clientConn.Write(pwBuf)

		// Expect ErrorResponse ('E') with SQLSTATE '28P01'
		errHeader := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, errHeader); err != nil {
			t.Fatalf("failed to read error header: %v", err)
		}
		if errHeader[0] != MsgErrorResponse {
			t.Fatalf("expected MsgErrorResponse 'E', got %c", errHeader[0])
		}
		errLen := binary.BigEndian.Uint32(errHeader[1:5])
		errPayload := make([]byte, errLen-4)
		_, _ = io.ReadFull(clientConn, errPayload)
		if !strings.Contains(string(errPayload), "28P01") {
			t.Fatalf("expected 28P01 in error response, got %q", string(errPayload))
		}
	})

	_ = cat
}
