package wire

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

func TestParseCopyStmt(t *testing.T) {
	tests := []struct {
		query       string
		expectedDir CopyDirection
		expectedTbl string
		expectedFmt CopyFormat
		expectedHdr bool
		expectedDel rune
		numCols     int
	}{
		{
			query:       "COPY accounts FROM STDIN;",
			expectedDir: CopyIn,
			expectedTbl: "accounts",
			expectedFmt: FormatText,
			expectedHdr: false,
			expectedDel: '\t',
			numCols:     0,
		},
		{
			query:       "COPY accounts (id, holder, balance) FROM STDIN WITH (FORMAT CSV, HEADER);",
			expectedDir: CopyIn,
			expectedTbl: "accounts",
			expectedFmt: FormatCSV,
			expectedHdr: true,
			expectedDel: ',',
			numCols:     3,
		},
		{
			query:       "COPY [accounts] (id, balance) TO STDOUT WITH CSV;",
			expectedDir: CopyOut,
			expectedTbl: "accounts",
			expectedFmt: FormatCSV,
			expectedHdr: false,
			expectedDel: ',',
			numCols:     2,
		},
		{
			query:       "COPY \"dbo\".\"users\" TO STDOUT;",
			expectedDir: CopyOut,
			expectedTbl: "users",
			expectedFmt: FormatText,
			expectedHdr: false,
			expectedDel: '\t',
			numCols:     0,
		},
	}

	for _, tc := range tests {
		stmt, err := ParseCopyStmt(tc.query)
		if err != nil {
			t.Fatalf("ParseCopyStmt(%q) failed: %v", tc.query, err)
		}
		if stmt.Direction != tc.expectedDir {
			t.Errorf("[%s] expected dir %v, got %v", tc.query, tc.expectedDir, stmt.Direction)
		}
		if stmt.TableName != tc.expectedTbl {
			t.Errorf("[%s] expected table %s, got %s", tc.query, tc.expectedTbl, stmt.TableName)
		}
		if stmt.Format != tc.expectedFmt {
			t.Errorf("[%s] expected format %v, got %v", tc.query, tc.expectedFmt, stmt.Format)
		}
		if stmt.Header != tc.expectedHdr {
			t.Errorf("[%s] expected header %v, got %v", tc.query, tc.expectedHdr, stmt.Header)
		}
		if stmt.Delimiter != tc.expectedDel {
			t.Errorf("[%s] expected delimiter %q, got %q", tc.query, tc.expectedDel, stmt.Delimiter)
		}
		if len(stmt.Columns) != tc.numCols {
			t.Errorf("[%s] expected %d cols, got %d", tc.query, tc.numCols, len(stmt.Columns))
		}
	}
}

func doHandshake(t *testing.T, conn net.Conn) {
	t.Helper()
	startupBuf := make([]byte, 8)
	binary.BigEndian.PutUint32(startupBuf[:4], 8)
	binary.BigEndian.PutUint32(startupBuf[4:8], 196608) // 3.0
	if _, err := conn.Write(startupBuf); err != nil {
		t.Fatalf("Failed to write startup: %v", err)
	}

	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Fatalf("Failed to read header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("Failed to read payload: %v", err)
		}
		if msgType == MsgReadyForQuery {
			break
		}
	}
}

func sendSimpleQuery(t *testing.T, conn net.Conn, sql string) {
	t.Helper()
	payload := append([]byte(sql), 0)
	buf := make([]byte, 1+4+len(payload))
	buf[0] = 'Q'
	binary.BigEndian.PutUint32(buf[1:5], uint32(4+len(payload)))
	copy(buf[5:], payload)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("Failed to write query: %v", err)
	}
}

func TestWire_CopyFromStdinAndCopyToStdout_CSV(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)

	// Create items table
	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "price", Type: storage.TypeFloat, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create items table: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	doHandshake(t, clientConn)

	// 1. Send COPY items FROM STDIN WITH CSV
	sendSimpleQuery(t, clientConn, "COPY items (id, name, price) FROM STDIN WITH (FORMAT CSV);")

	// Read CopyInResponse ('G')
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, hdr); err != nil {
		t.Fatalf("failed to read CopyInResponse header: %v", err)
	}
	if hdr[0] != MsgCopyInResponse {
		t.Fatalf("expected MsgCopyInResponse ('G'), got %c", hdr[0])
	}
	respLen := binary.BigEndian.Uint32(hdr[1:5])
	payload := make([]byte, respLen-4)
	if _, err := io.ReadFull(clientConn, payload); err != nil {
		t.Fatalf("failed to read CopyInResponse payload: %v", err)
	}

	// 2. Send CopyData ('d') with 3 rows
	csvData := []byte("101,Keyboard,89.5\n102,Mouse,49.9\n103,Monitor,299.0\n")
	copyDataMsg := make([]byte, 1+4+len(csvData))
	copyDataMsg[0] = MsgCopyData
	binary.BigEndian.PutUint32(copyDataMsg[1:5], uint32(4+len(csvData)))
	copy(copyDataMsg[5:], csvData)
	if _, err := clientConn.Write(copyDataMsg); err != nil {
		t.Fatalf("failed to write CopyData: %v", err)
	}

	// 3. Send CopyDone ('c')
	copyDoneMsg := []byte{MsgCopyDone, 0, 0, 0, 4}
	if _, err := clientConn.Write(copyDoneMsg); err != nil {
		t.Fatalf("failed to write CopyDone: %v", err)
	}

	// 4. Expect CommandComplete ('C') and ReadyForQuery ('Z')
	var gotComplete, gotReady bool
	var completeTag string

	for !gotReady {
		msgHdr := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, msgHdr); err != nil {
			t.Fatalf("failed to read after copy: %v", err)
		}
		mType := msgHdr[0]
		mLen := binary.BigEndian.Uint32(msgHdr[1:5])
		mPayload := make([]byte, mLen-4)
		if _, err := io.ReadFull(clientConn, mPayload); err != nil {
			t.Fatalf("failed to read payload: %v", err)
		}

		if mType == MsgCommandComplete {
			gotComplete = true
			completeTag = string(mPayload)
		} else if mType == MsgReadyForQuery {
			gotReady = true
		} else if mType == MsgErrorResponse {
			t.Fatalf("unexpected error response: %s", string(mPayload))
		}
	}

	if !gotComplete {
		t.Errorf("expected CommandComplete message")
	}
	if !strings.Contains(completeTag, "COPY 3") {
		t.Errorf("expected tag COPY 3, got %q", completeTag)
	}

	// Verify database rows inserted
	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	rec, err := tx.Get("items", storage.IntValue(102))
	if err != nil {
		t.Fatalf("failed to get item 102: %v", err)
	}
	if rec["name"].SVal != "Mouse" {
		t.Errorf("expected Mouse, got %s", rec["name"].SVal)
	}
	if rec["price"].FVal != 49.9 {
		t.Errorf("expected 49.9, got %f", rec["price"].FVal)
	}
	_ = tx.Rollback()
	_ = sess.Close()

	// 5. Now test COPY items TO STDOUT WITH (FORMAT CSV, HEADER)
	sendSimpleQuery(t, clientConn, "COPY items (id, name, price) TO STDOUT WITH (FORMAT CSV, HEADER);")

	// Read CopyOutResponse ('H')
	if _, err := io.ReadFull(clientConn, hdr); err != nil {
		t.Fatalf("failed to read CopyOutResponse header: %v", err)
	}
	if hdr[0] != MsgCopyOutResponse {
		t.Fatalf("expected MsgCopyOutResponse ('H'), got %c", hdr[0])
	}
	respLen = binary.BigEndian.Uint32(hdr[1:5])
	payload = make([]byte, respLen-4)
	if _, err := io.ReadFull(clientConn, payload); err != nil {
		t.Fatalf("failed to read CopyOutResponse payload: %v", err)
	}

	var streamedData strings.Builder
	gotDone := false
	gotComplete = false
	gotReady = false

	for !gotReady {
		msgHdr := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, msgHdr); err != nil {
			t.Fatalf("failed to read during copy out: %v", err)
		}
		mType := msgHdr[0]
		mLen := binary.BigEndian.Uint32(msgHdr[1:5])
		mPayload := make([]byte, mLen-4)
		if _, err := io.ReadFull(clientConn, mPayload); err != nil {
			t.Fatalf("failed to read copy out payload: %v", err)
		}

		if mType == MsgCopyData {
			streamedData.Write(mPayload)
		} else if mType == MsgCopyDone {
			gotDone = true
		} else if mType == MsgCommandComplete {
			gotComplete = true
		} else if mType == MsgReadyForQuery {
			gotReady = true
		}
	}

	if !gotDone {
		t.Errorf("expected CopyDone message")
	}
	if !gotComplete {
		t.Errorf("expected CommandComplete message")
	}

	output := streamedData.String()
	if !strings.Contains(output, "id,name,price") {
		t.Errorf("expected header row, got:\n%s", output)
	}
	if !strings.Contains(output, "101,Keyboard,89.5") {
		t.Errorf("expected row 101, got:\n%s", output)
	}
	if !strings.Contains(output, "103,Monitor,299.0") {
		t.Errorf("expected row 103, got:\n%s", output)
	}
}

func TestWire_CopyFromStdin_TextFormatWithNull(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)

	// Create nullable test table
	schema := &storage.TableSchema{
		Name: "people",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "nickname", Type: storage.TypeVarChar, Nullable: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)
	doHandshake(t, clientConn)

	// Tab-delimited text format with \N for null
	sendSimpleQuery(t, clientConn, "COPY people FROM STDIN;")

	hdr := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, hdr); err != nil {
		t.Fatalf("read copy in hdr: %v", err)
	}
	if hdr[0] != MsgCopyInResponse {
		t.Fatalf("expected 'G', got %c", hdr[0])
	}
	rLen := binary.BigEndian.Uint32(hdr[1:5])
	_, _ = io.ReadFull(clientConn, make([]byte, rLen-4))

	// Send row 1 with nickname and row 2 with \N null
	tsvData := []byte("1\tJohn\tJohnny\n2\tJane\t\\N\n\\.\n")
	copyDataMsg := make([]byte, 1+4+len(tsvData))
	copyDataMsg[0] = MsgCopyData
	binary.BigEndian.PutUint32(copyDataMsg[1:5], uint32(4+len(tsvData)))
	copy(copyDataMsg[5:], tsvData)
	_, _ = clientConn.Write(copyDataMsg)

	copyDoneMsg := []byte{MsgCopyDone, 0, 0, 0, 4}
	_, _ = clientConn.Write(copyDoneMsg)

	// Read until ready
	for {
		mHdr := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, mHdr)
		mType := mHdr[0]
		mLen := binary.BigEndian.Uint32(mHdr[1:5])
		_, _ = io.ReadFull(clientConn, make([]byte, mLen-4))
		if mType == MsgReadyForQuery {
			break
		}
	}

	// Verify Jane's nickname is NULL
	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()
	defer sess.Close()

	rec, err := tx.Get("people", storage.IntValue(2))
	if err != nil {
		t.Fatalf("failed to get Jane: %v", err)
	}
	if !rec["nickname"].IsNull {
		t.Errorf("expected nickname to be NULL for Jane, got: %s", rec["nickname"].String())
	}
}
