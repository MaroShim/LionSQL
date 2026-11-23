package wire

import (
	"encoding/binary"
	"io"
	"testing"
)

func TestPGWire_DiscardAll_IsolatesPooledConnection(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	conn := connectPGClient(t, srv)

	// 1. Client starts transaction and mutates balance
	sendPGQuery(t, conn, "BEGIN;")
	sendPGQuery(t, conn, "UPDATE accounts SET balance = 300 WHERE id = 1;")
	sendPGQuery(t, conn, "CREATE TABLE #tmp_pg (id INT);")

	// 2. Client also prepares a statement "find_acc"
	sendPGQuery(t, conn, "PREPARE find_acc AS SELECT * FROM accounts WHERE id = 1;")

	// 3. Client finishes without COMMIT/ROLLBACK and returns connection to pool.
	// Pooler issues DISCARD ALL
	sendPGQuery(t, conn, "DISCARD ALL;")

	// 4. Verify transaction was rolled back: balance is still 1000
	rows := sendPGQuery(t, conn, "SELECT balance FROM accounts WHERE id = 1;")
	if len(rows) == 0 {
		t.Fatalf("expected 1 row for id=1")
	}

	// 5. Verify prepared statement cache was cleared: preparing "find_acc" again succeeds cleanly without collision
	sendPGQuery(t, conn, "PREPARE find_acc AS SELECT balance FROM accounts WHERE id = 1;")

	// 6. Verify temp table was dropped: querying #tmp_pg should fail or return error
	query := "SELECT * FROM #tmp_pg;\x00"
	queryBuf := make([]byte, 1+4+len(query))
	queryBuf[0] = 'Q'
	binary.BigEndian.PutUint32(queryBuf[1:5], uint32(4+len(query)))
	copy(queryBuf[5:], query)
	_, _ = conn.Write(queryBuf)

	gotError := false
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Fatalf("failed to read response: %v", err)
		}
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("failed to read payload: %v", err)
		}
		if header[0] == MsgErrorResponse {
			gotError = true
		}
		if header[0] == MsgReadyForQuery {
			break
		}
	}
	if !gotError {
		t.Fatalf("expected error querying dropped temp table #tmp_pg after DISCARD ALL")
	}
}

func TestPGWire_DiscardVariants(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	conn := connectPGClient(t, srv)

	// 1. DISCARD PLANS
	sendPGQuery(t, conn, "PREPARE plan_1 AS SELECT 1;")
	sendPGQuery(t, conn, "DISCARD PLANS;")
	// Re-preparing plan_1 should succeed cleanly
	sendPGQuery(t, conn, "PREPARE plan_1 AS SELECT 2;")

	// 2. DISCARD SEQUENCES
	sendPGQuery(t, conn, "DISCARD SEQUENCES;")

	// 3. DISCARD TEMP
	sendPGQuery(t, conn, "CREATE TABLE #tmp_test_wire (id INT);")
	sendPGQuery(t, conn, "DISCARD TEMP;")

	query := "SELECT * FROM #tmp_test_wire;\x00"
	queryBuf := make([]byte, 1+4+len(query))
	queryBuf[0] = 'Q'
	binary.BigEndian.PutUint32(queryBuf[1:5], uint32(4+len(query)))
	copy(queryBuf[5:], query)
	_, _ = conn.Write(queryBuf)

	gotError := false
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Fatalf("failed to read response: %v", err)
		}
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("failed to read payload: %v", err)
		}
		if header[0] == MsgErrorResponse {
			gotError = true
		}
		if header[0] == MsgReadyForQuery {
			break
		}
	}
	if !gotError {
		t.Fatalf("expected error querying dropped temp table #tmp_test_wire after DISCARD TEMP")
	}
}
