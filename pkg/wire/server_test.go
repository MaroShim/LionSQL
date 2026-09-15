package wire

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"sqltiger/pkg/catalog"
	"sqltiger/pkg/storage"
	"sqltiger/pkg/storage/memengine"
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

	if !strings.Contains(versionOutput, "SqlTiger") {
		t.Errorf("Expected version to contain SqlTiger, got: %s", versionOutput)
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
