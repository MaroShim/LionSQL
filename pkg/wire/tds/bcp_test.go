package tds

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"lionsql/pkg/storage"
)

func TestTDS_ParseInsertBulkStmt(t *testing.T) {
	tests := []struct {
		query        string
		expectedTbl  string
		expectedCols []string
		expectedOk   bool
	}{
		{
			query:        "INSERT BULK [dbo].[orders] (id INT, customer VARCHAR(50), total FLOAT);",
			expectedTbl:  "orders",
			expectedCols: []string{"id", "customer", "total"},
			expectedOk:   true,
		},
		{
			query:        "INSERT BULK accounts (id INT PRIMARY KEY, balance FLOAT);",
			expectedTbl:  "accounts",
			expectedCols: []string{"id", "balance"},
			expectedOk:   true,
		},
		{
			query:        "INSERT BULK [my_schema].[items];",
			expectedTbl:  "items",
			expectedCols: nil,
			expectedOk:   true,
		},
		{
			query:        "SELECT * FROM orders;",
			expectedTbl:  "",
			expectedCols: nil,
			expectedOk:   false,
		},
	}

	for _, tc := range tests {
		tbl, cols, ok := parseInsertBulkStmt(tc.query)
		if ok != tc.expectedOk {
			t.Errorf("[%s] expected ok %v, got %v", tc.query, tc.expectedOk, ok)
		}
		if tbl != tc.expectedTbl {
			t.Errorf("[%s] expected table %s, got %s", tc.query, tc.expectedTbl, tbl)
		}
		if len(cols) != len(tc.expectedCols) {
			t.Errorf("[%s] expected %d cols, got %d", tc.query, len(tc.expectedCols), len(cols))
		}
	}
}

func TestTDS_BulkLoad_Packet0x07_E2E(t *testing.T) {
	srv := setupTestServer(t)
	eng := srv.engine

	schema := &storage.TableSchema{
		Name: "bcp_stream",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "score", Type: storage.TypeFloat, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_bcp_name", Columns: []string{"name"}},
			{Name: "idx_bcp_score", Columns: []string{"score"}},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	clientConn := connectClient(t, srv)
	performHandshake(t, clientConn)

	// 1. Send INSERT BULK SQLBatch
	resp1 := sendSQLBatch(t, clientConn, "INSERT BULK bcp_stream (id INT, name VARCHAR(50), score FLOAT);")
	if !bytes.Contains(resp1, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone in INSERT BULK response, got %x", resp1)
	}

	// 2. Build PacketBulkLoad (0x07) containing 2,500 NBCROW records
	const totalRows = 2500
	var bulkPayload bytes.Buffer

	for i := 1; i <= totalRows; i++ {
		bulkPayload.WriteByte(TokenNBCRow)
		// 3 columns -> ceil(3/8) = 1 byte null bitmap. All non-null -> 0x00
		bulkPayload.WriteByte(0x00)

		// col 0: id (INT, 4 bytes)
		bulkPayload.WriteByte(4)
		_ = binary.Write(&bulkPayload, binary.LittleEndian, int32(i))

		// col 1: name (VARCHAR, 2B length + chars)
		nameStr := fmt.Sprintf("bcp_user_%05d", i)
		_ = binary.Write(&bulkPayload, binary.LittleEndian, uint16(len(nameStr)))
		bulkPayload.WriteString(nameStr)

		// col 2: score (FLOAT, 8 bytes)
		bulkPayload.WriteByte(8)
		_ = binary.Write(&bulkPayload, binary.LittleEndian, float64(i)*2.5)
	}

	// Append terminating TokenDone
	bulkPayload.Write(BuildDone(TokenDone, DoneFinal, 0, 0))

	// Send PacketBulkLoad (0x07)
	if err := WritePacket(clientConn, PacketBulkLoad, 1, bulkPayload.Bytes()); err != nil {
		t.Fatalf("client write bulk packet failed: %v", err)
	}

	// 3. Read server completion packet
	compPkt, err := ReadPacket(clientConn)
	if err != nil {
		t.Fatalf("read bulk load completion failed: %v", err)
	}
	if !bytes.Contains(compPkt.Payload, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone in bulk completion, got: %x", compPkt.Payload)
	}

	// 4. Verify data via SQLBatch SELECT
	selResp := sendSQLBatch(t, clientConn, "SELECT COUNT(*) FROM bcp_stream;")
	if !bytes.Contains(selResp, []byte{TokenColMetaData}) {
		t.Fatalf("expected TokenColMetaData in SELECT response: %x", selResp)
	}

	// 5. Verify direct engine storage & secondary index lookup
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()
	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	// Verify row 1
	rec1, err := tx.Get("bcp_stream", storage.IntValue(1))
	if err != nil {
		t.Fatalf("failed to get row 1: %v", err)
	}
	if rec1["name"].String() != "bcp_user_00001" {
		t.Errorf("expected bcp_user_00001, got %v", rec1["name"])
	}

	// Verify row 2500
	recLast, err := tx.Get("bcp_stream", storage.IntValue(totalRows))
	if err != nil {
		t.Fatalf("failed to get row %d: %v", totalRows, err)
	}
	if recLast["name"].String() != fmt.Sprintf("bcp_user_%05d", totalRows) {
		t.Errorf("expected bcp_user_%05d, got %v", totalRows, recLast["name"])
	}
	if math.Abs(recLast["score"].FVal-float64(totalRows)*2.5) > 0.001 {
		t.Errorf("expected score %.2f, got %v", float64(totalRows)*2.5, recLast["score"])
	}

	// Verify secondary index search
	searchKey := storage.StringValue("bcp_user_01000")
	idxCur, err := tx.IndexScan("bcp_stream", "idx_bcp_name", &searchKey, &searchKey)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer idxCur.Close()
	if !idxCur.Next() {
		t.Fatalf("expected to find bcp_user_01000 in index")
	}
	idxRec, err := idxCur.Record()
	if err != nil || idxRec["id"].IVal != 1000 {
		t.Fatalf("expected PK 1000 from index, got %v (err: %v)", idxRec["id"], err)
	}
}

func TestTDS_BulkLoad_InTransaction_Rollback(t *testing.T) {
	srv := setupTestServer(t)
	eng := srv.engine

	schema := &storage.TableSchema{
		Name: "bcp_rollback",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "note", Type: storage.TypeVarChar, Nullable: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	clientConn := connectClient(t, srv)
	performHandshake(t, clientConn)

	// 1. Begin transaction
	sendSQLBatch(t, clientConn, "BEGIN TRAN;")

	// 2. INSERT BULK
	sendSQLBatch(t, clientConn, "INSERT BULK bcp_rollback (id INT, note VARCHAR(50));")

	// 3. Send 10 rows via PacketBulkLoad (0x07)
	var bulkPayload bytes.Buffer
	for i := 1; i <= 10; i++ {
		bulkPayload.WriteByte(TokenNBCRow)
		bulkPayload.WriteByte(0x00) // null bitmap

		// col 0: id (INT, 4 bytes)
		bulkPayload.WriteByte(4)
		_ = binary.Write(&bulkPayload, binary.LittleEndian, int32(i))

		// col 1: note (VARCHAR, 2B length)
		noteStr := fmt.Sprintf("note_%d", i)
		_ = binary.Write(&bulkPayload, binary.LittleEndian, uint16(len(noteStr)))
		bulkPayload.WriteString(noteStr)
	}
	bulkPayload.Write(BuildDone(TokenDone, DoneFinal, 0, 0))

	if err := WritePacket(clientConn, PacketBulkLoad, 1, bulkPayload.Bytes()); err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}
	_, _ = ReadPacket(clientConn)

	// 4. Rollback transaction
	sendSQLBatch(t, clientConn, "ROLLBACK TRAN;")

	// 5. Verify table is completely empty
	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()

	cur, err := tx.Scan("bcp_rollback")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	defer cur.Close()
	if cur.Next() {
		t.Fatalf("expected table to be empty after ROLLBACK TRAN, but found rows")
	}
}

func TestTDS_BulkLoad_RowToken_D1_And_Nulls(t *testing.T) {
	srv := setupTestServer(t)
	eng := srv.engine

	schema := &storage.TableSchema{
		Name: "bcp_nulls",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val_str", Type: storage.TypeVarChar, Nullable: true},
			{Name: "val_flt", Type: storage.TypeFloat, Nullable: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	clientConn := connectClient(t, srv)
	performHandshake(t, clientConn)

	sendSQLBatch(t, clientConn, "INSERT BULK bcp_nulls (id INT, val_str VARCHAR(50), val_flt FLOAT);")

	var bulkPayload bytes.Buffer
	// Row 1: non-null
	bulkPayload.WriteByte(TokenRow)
	bulkPayload.WriteByte(4)
	_ = binary.Write(&bulkPayload, binary.LittleEndian, int32(101))
	_ = binary.Write(&bulkPayload, binary.LittleEndian, uint16(4))
	bulkPayload.WriteString("test")
	bulkPayload.WriteByte(8)
	_ = binary.Write(&bulkPayload, binary.LittleEndian, float64(99.5))

	// Row 2: with nulls (len 0 for float, 0xFFFF for varchar)
	bulkPayload.WriteByte(TokenRow)
	bulkPayload.WriteByte(4)
	_ = binary.Write(&bulkPayload, binary.LittleEndian, int32(102))
	_ = binary.Write(&bulkPayload, binary.LittleEndian, uint16(0xFFFF)) // null varchar
	bulkPayload.WriteByte(0)                                           // null float

	bulkPayload.Write(BuildDone(TokenDone, DoneFinal, 0, 0))

	if err := WritePacket(clientConn, PacketBulkLoad, 1, bulkPayload.Bytes()); err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	compPkt, err := ReadPacket(clientConn)
	if err != nil {
		t.Fatalf("ReadPacket failed: %v", err)
	}
	if !bytes.Contains(compPkt.Payload, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone, got: %x", compPkt.Payload)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()

	rec2, err := tx.Get("bcp_nulls", storage.IntValue(102))
	if err != nil {
		t.Fatalf("failed to get row 102: %v", err)
	}
	if !rec2["val_str"].IsNull {
		t.Errorf("expected val_str to be NULL, got %v", rec2["val_str"])
	}
	if !rec2["val_flt"].IsNull {
		t.Errorf("expected val_flt to be NULL, got %v", rec2["val_flt"])
	}
}

func TestTDS_BulkLoad_AutoGeneratePK(t *testing.T) {
	srv := setupTestServer(t)
	eng := srv.engine

	schema := &storage.TableSchema{
		Name: "bcp_auto_pk",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "data", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	clientConn := connectClient(t, srv)
	performHandshake(t, clientConn)

	// INSERT BULK only specifies 'data', omitting 'id'
	sendSQLBatch(t, clientConn, "INSERT BULK bcp_auto_pk (data VARCHAR(50));")

	var bulkPayload bytes.Buffer
	for _, val := range []string{"alpha", "beta", "gamma"} {
		bulkPayload.WriteByte(TokenNBCRow)
		bulkPayload.WriteByte(0x00) // 1 column non-null
		_ = binary.Write(&bulkPayload, binary.LittleEndian, uint16(len(val)))
		bulkPayload.WriteString(val)
	}
	bulkPayload.Write(BuildDone(TokenDone, DoneFinal, 0, 0))

	if err := WritePacket(clientConn, PacketBulkLoad, 1, bulkPayload.Bytes()); err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}
	_, _ = ReadPacket(clientConn)

	sess, _ := eng.NewSession()
	defer sess.Close()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()

	r1, err := tx.Get("bcp_auto_pk", storage.IntValue(1))
	if err != nil || r1["data"].String() != "alpha" {
		t.Fatalf("expected alpha at id=1, got %v (err: %v)", r1, err)
	}
	r2, err := tx.Get("bcp_auto_pk", storage.IntValue(2))
	if err != nil || r2["data"].String() != "beta" {
		t.Fatalf("expected beta at id=2, got %v (err: %v)", r2, err)
	}
	r3, err := tx.Get("bcp_auto_pk", storage.IntValue(3))
	if err != nil || r3["data"].String() != "gamma" {
		t.Fatalf("expected gamma at id=3, got %v (err: %v)", r3, err)
	}
}

func TestTDS_BulkLoad_ErrorHandling(t *testing.T) {
	srv := setupTestServer(t)
	clientConn := connectClient(t, srv)
	performHandshake(t, clientConn)

	// Send PacketBulkLoad (0x07) without prior INSERT BULK
	var bulkPayload bytes.Buffer
	bulkPayload.WriteByte(TokenRow)
	bulkPayload.Write(BuildDone(TokenDone, DoneFinal, 0, 0))

	_ = WritePacket(clientConn, PacketBulkLoad, 1, bulkPayload.Bytes())
	resp, err := ReadPacket(clientConn)
	if err != nil {
		t.Fatalf("ReadPacket failed: %v", err)
	}
	if !bytes.Contains(resp.Payload, []byte{TokenError}) {
		t.Fatalf("expected TokenError for missing table, got: %x", resp.Payload)
	}
}
