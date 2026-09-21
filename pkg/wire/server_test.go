package wire

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
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
