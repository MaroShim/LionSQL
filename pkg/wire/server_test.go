package wire

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"sqlion/pkg/catalog"
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

	prog, err := server.compileQueryToProgram("SELECT id, holder FROM accounts WHERE id = $1")
	if err != nil {
		t.Fatalf("compileQueryToProgram failed: %v", err)
	}
	if prog == nil {
		t.Fatalf("expected non-nil program")
	}
	dis := prog.Disassemble()
	if !strings.Contains(dis, "REL_QUERY") {
		t.Fatalf("expected REL_QUERY opcode in program, got:\n%s", dis)
	}
}



