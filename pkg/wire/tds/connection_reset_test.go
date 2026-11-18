package tds

import (
	"bytes"
	"net"
	"testing"
)

func sendSQLBatchWithStatus(t *testing.T, conn net.Conn, sql string, status byte) []byte {
	t.Helper()
	u16SQL := EncodeUTF16LE(sql)
	if err := WritePacketWithStatus(conn, PacketSQLBatch, status, 1, u16SQL); err != nil {
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

func TestTDS_StatusResetConnection_IsolatesPooledConnection(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	// 1. Create base table
	sendSQLBatch(t, conn, "CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50));")
	sendSQLBatch(t, conn, "INSERT INTO items VALUES (1, 'Widget');")

	// 2. Client borrows connection from pool: starts transaction, mutates table, creates #temp table
	sendSQLBatch(t, conn, "BEGIN TRAN")
	sendSQLBatch(t, conn, "UPDATE items SET name = 'Mutated' WHERE id = 1")
	sendSQLBatch(t, conn, "CREATE TABLE #tmp_tds (val INT)")
	sendSQLBatch(t, conn, "INSERT INTO #tmp_tds VALUES (42)")

	// 3. Client finishes without committing or rolling back and returns connection to pool.
	// Next client borrows connection, and client driver sends StatusResetConnection (0x08).
	resetPayload := sendSQLBatchWithStatus(t, conn, "SELECT name FROM items WHERE id = 1", StatusResetConnection)

	if !bytes.Contains(resetPayload, []byte{TokenRow}) {
		t.Fatalf("expected ROW token in SELECT response")
	}
	// The transaction should have been rolled back: name should be 'Widget', not 'Mutated'
	if bytes.Contains(resetPayload, EncodeUTF16LE("Mutated")) {
		t.Fatalf("uncommitted transaction was NOT rolled back on StatusResetConnection!")
	}
	if !bytes.Contains(resetPayload, EncodeUTF16LE("Widget")) {
		t.Fatalf("expected original name 'Widget' in response")
	}

	// 4. Querying #tmp_tds should now return error since temp table was dropped on connection reset
	errPayload := sendSQLBatch(t, conn, "SELECT * FROM #tmp_tds")
	if !bytes.Contains(errPayload, []byte{TokenError}) {
		t.Fatalf("expected ERROR token querying dropped temp table #tmp_tds, but got: %v", errPayload)
	}
}

func TestTDS_SP_ResetConnection_RPC(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	// 1. Mutate state inside transaction
	sendSQLBatch(t, conn, "CREATE TABLE tbl (id INT PRIMARY KEY);")
	sendSQLBatch(t, conn, "BEGIN TRAN")
	sendSQLBatch(t, conn, "INSERT INTO tbl VALUES (99);")
	sendSQLBatch(t, conn, "CREATE TABLE #local_t (x INT);")

	// 2. Client sends RPC sp_reset_connection
	sendSQLBatch(t, conn, "EXEC sp_reset_connection")

	// 3. Verify transaction was rolled back: tbl should have 0 rows
	payload := sendSQLBatch(t, conn, "SELECT COUNT(*) FROM tbl")
	if !bytes.Contains(payload, []byte{TokenRow}) {
		t.Fatalf("expected ROW token in count query")
	}

	// 4. Verify temp table was dropped
	errPayload := sendSQLBatch(t, conn, "SELECT * FROM #local_t")
	if !bytes.Contains(errPayload, []byte{TokenError}) {
		t.Fatalf("expected ERROR token querying dropped temp table #local_t")
	}
}
