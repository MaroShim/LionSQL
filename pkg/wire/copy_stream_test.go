package wire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

func TestWire_PG_CopyStream_LargeBatch_E2E(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)

	schema := &storage.TableSchema{
		Name: "stream_bench",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "score", Type: storage.TypeFloat, Nullable: false},
			{Name: "status", Type: storage.TypeVarChar, Nullable: true},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_name", Columns: []string{"name"}},
			{Name: "idx_score", Columns: []string{"score"}},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table stream_bench: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)
	doHandshake(t, clientConn)

	// 1. Initiate COPY FROM STDIN
	sendSimpleQuery(t, clientConn, "COPY stream_bench (id, name, score, status) FROM STDIN WITH (FORMAT CSV, HEADER);")

	hdr := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, hdr); err != nil {
		t.Fatalf("failed to read CopyInResponse header: %v", err)
	}
	if hdr[0] != MsgCopyInResponse {
		t.Fatalf("expected MsgCopyInResponse ('G'), got %c", hdr[0])
	}
	rLen := binary.BigEndian.Uint32(hdr[1:5])
	_, _ = io.ReadFull(clientConn, make([]byte, rLen-4))

	// 2. Stream 3,500 rows in multiple CopyData chunks (to test multi-chunk boundary handling & BatchInsert)
	const totalRows = 3500
	var csvBuf bytes.Buffer
	csvBuf.WriteString("id,name,score,status\n")

	chunkSize := 500
	for i := 1; i <= totalRows; i++ {
		fmt.Fprintf(&csvBuf, "%d,user_%05d,%.2f,active\n", i, i, float64(i)*1.5)
		if i%chunkSize == 0 || i == totalRows {
			data := csvBuf.Bytes()
			msg := make([]byte, 5+len(data))
			msg[0] = MsgCopyData
			binary.BigEndian.PutUint32(msg[1:5], uint32(4+len(data)))
			copy(msg[5:], data)
			if _, err := clientConn.Write(msg); err != nil {
				t.Fatalf("failed to write CopyData at row %d: %v", i, err)
			}
			csvBuf.Reset()
		}
	}

	// 3. Send CopyDone
	copyDoneMsg := []byte{MsgCopyDone, 0, 0, 0, 4}
	if _, err := clientConn.Write(copyDoneMsg); err != nil {
		t.Fatalf("failed to write CopyDone: %v", err)
	}

	// 4. Read CommandComplete and ReadyForQuery
	var gotComplete bool
	for {
		mHdr := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, mHdr); err != nil {
			t.Fatalf("failed to read completion header: %v", err)
		}
		mType := mHdr[0]
		mLen := binary.BigEndian.Uint32(mHdr[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(clientConn, payload); err != nil {
			t.Fatalf("failed to read completion payload: %v", err)
		}

		if mType == MsgCommandComplete {
			gotComplete = true
			expected := fmt.Sprintf("COPY %d\x00", totalRows)
			if string(payload) != expected {
				t.Fatalf("expected CommandComplete %q, got %q", expected, string(payload))
			}
		} else if mType == MsgReadyForQuery {
			break
		} else if mType == MsgErrorResponse {
			t.Fatalf("unexpected error response during COPY: %s", string(payload))
		}
	}

	if !gotComplete {
		t.Fatal("expected CommandComplete message")
	}

	// 5. Verify total count via SELECT
	countRows := sendPGQuery(t, clientConn, "SELECT COUNT(*) FROM stream_bench;")
	if len(countRows) != 1 || !strings.Contains(string(countRows[0]), strconv.Itoa(totalRows)) {
		t.Fatalf("expected %d rows in stream_bench, got %s", totalRows, string(countRows[0]))
	}

	// 6. Test COPY TO STDOUT with CSV & HEADER
	sendSimpleQuery(t, clientConn, "COPY stream_bench (id, name, score, status) TO STDOUT WITH (FORMAT CSV, HEADER);")

	outHdr := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, outHdr); err != nil {
		t.Fatalf("failed to read CopyOutResponse header: %v", err)
	}
	if outHdr[0] != MsgCopyOutResponse {
		t.Fatalf("expected MsgCopyOutResponse ('H'), got %c", outHdr[0])
	}
	outLen := binary.BigEndian.Uint32(outHdr[1:5])
	_, _ = io.ReadFull(clientConn, make([]byte, outLen-4))

	var outData bytes.Buffer
	for {
		mHdr := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, mHdr); err != nil {
			t.Fatalf("failed to read CopyData header: %v", err)
		}
		mType := mHdr[0]
		mLen := binary.BigEndian.Uint32(mHdr[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(clientConn, payload); err != nil {
			t.Fatalf("failed to read CopyData payload: %v", err)
		}

		if mType == MsgCopyData {
			outData.Write(payload)
		} else if mType == MsgCopyDone {
			// COPY TO STDOUT finished
		} else if mType == MsgCommandComplete {
			// complete
		} else if mType == MsgReadyForQuery {
			break
		} else if mType == MsgErrorResponse {
			t.Fatalf("unexpected error during COPY TO STDOUT: %s", string(payload))
		}
	}

	lines := strings.Split(strings.TrimSpace(outData.String()), "\n")
	// 1 header line + totalRows data lines = totalRows + 1
	if len(lines) != totalRows+1 {
		t.Fatalf("expected %d lines from COPY TO STDOUT, got %d", totalRows+1, len(lines))
	}
	if lines[0] != "id,name,score,status" {
		t.Fatalf("unexpected header from COPY TO STDOUT: %s", lines[0])
	}
}

func TestWire_PG_CopyStream_CopyFail_AbortsCleanly(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)

	schema := &storage.TableSchema{
		Name: "cancel_bench",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "data", Type: storage.TypeVarChar, Nullable: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table cancel_bench: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)
	doHandshake(t, clientConn)

	sendSimpleQuery(t, clientConn, "COPY cancel_bench FROM STDIN;")

	hdr := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, hdr); err != nil {
		t.Fatalf("read copy in hdr: %v", err)
	}
	if hdr[0] != MsgCopyInResponse {
		t.Fatalf("expected 'G', got %c", hdr[0])
	}
	rLen := binary.BigEndian.Uint32(hdr[1:5])
	_, _ = io.ReadFull(clientConn, make([]byte, rLen-4))

	// Send some initial valid data
	tsvData := []byte("1\tfirst\n2\tsecond\n")
	copyDataMsg := make([]byte, 5+len(tsvData))
	copyDataMsg[0] = MsgCopyData
	binary.BigEndian.PutUint32(copyDataMsg[1:5], uint32(4+len(tsvData)))
	copy(copyDataMsg[5:], tsvData)
	_, _ = clientConn.Write(copyDataMsg)

	// Client aborts via CopyFail ('f')
	errMsg := "client aborted the import\x00"
	failMsg := make([]byte, 5+len(errMsg))
	failMsg[0] = MsgCopyFail
	binary.BigEndian.PutUint32(failMsg[1:5], uint32(4+len(errMsg)))
	copy(failMsg[5:], []byte(errMsg))
	_, _ = clientConn.Write(failMsg)

	// Expect ErrorResponse and ReadyForQuery
	var gotError, gotReady bool
	for !gotReady {
		mHdr := make([]byte, 5)
		if _, err := io.ReadFull(clientConn, mHdr); err != nil {
			t.Fatalf("failed to read header: %v", err)
		}
		mType := mHdr[0]
		mLen := binary.BigEndian.Uint32(mHdr[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(clientConn, payload); err != nil {
			t.Fatalf("failed to read payload: %v", err)
		}

		if mType == MsgErrorResponse {
			gotError = true
			if !strings.Contains(string(payload), "client aborted the import") {
				t.Fatalf("expected cancel reason in error response, got: %s", string(payload))
			}
		} else if mType == MsgReadyForQuery {
			gotReady = true
		}
	}

	if !gotError {
		t.Fatal("expected MsgErrorResponse on CopyFail")
	}

	// Verify table has 0 rows because transaction rolled back on abort
	rows := sendPGQuery(t, clientConn, "SELECT COUNT(*) FROM cancel_bench;")
	if len(rows) != 1 || !strings.Contains(string(rows[0]), "0") {
		t.Fatalf("expected 0 rows after COPY abort, got: %v", rows)
	}
}
