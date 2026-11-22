package spec_defects

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire"
)

// TestSpecDefect_PGWire_ExtendedQuery_MultiSet verifies:
// Defect ID: DEFECT-PGWIRE-EXTENDED-QUERY-MULTIPLE-RESULT-SETS-1
// Root Cause: When a stored procedure or batch returning multiple result sets is executed
// via PostgreSQL Extended Query protocol (Parse -> Bind -> Describe -> Execute -> Sync),
// 1. Describe ('D') returned NoData ('n') because ResultColumns() in bytecode.go ignored *RelQueryData.
// 2. handleExecute ('E') in server.go only streamed the first result set (portal.Result) and
//    terminated without streaming res.Next (subsequent result sets), omitting RowDescription,
//    DataRow, and CommandComplete for subsequent sets.
// 3. ResolvePreparedProcedure ignored all user procedures starting with "sp_".
//
// With the fix, the server emits the full PostgreSQL Extended Query message sequence:
// ParseComplete ('1'), BindComplete ('2'), RowDescription ('T'), DataRow ('D'), CommandComplete ('C'),
// RowDescription ('T'), DataRow ('D'), CommandComplete ('C'), ReadyForQuery ('Z').
func TestSpecDefect_PGWire_ExtendedQuery_MultiSet(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)

	srv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start PGWire server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("sql.Open pgx failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	procSQL := `
CREATE PROCEDURE sp_pg_multi_dataset
AS
BEGIN
    SELECT 101 AS num, 'alpha' AS label;
    SELECT 202 AS num, 'beta' AS label;
END;
`
	if _, err := db.ExecContext(ctx, procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// Connect raw client to verify Extended Query packet framing
	rawConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("failed to dial raw client: %v", err)
	}
	defer rawConn.Close()

	// 1. Startup message
	startupBuf := new(bytes.Buffer)
	binary.Write(startupBuf, binary.BigEndian, int32(196608)) // Protocol v3.0
	startupBuf.WriteString("user\x00postgres\x00database\x00master\x00\x00")
	msgLen := int32(startupBuf.Len() + 4)
	fullStartup := new(bytes.Buffer)
	binary.Write(fullStartup, binary.BigEndian, msgLen)
	fullStartup.Write(startupBuf.Bytes())
	if _, err := rawConn.Write(fullStartup.Bytes()); err != nil {
		t.Fatalf("startup write failed: %v", err)
	}

	// Consume startup response until ReadyForQuery ('Z')
	for {
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(rawConn, hdr); err != nil {
			t.Fatalf("startup read header failed: %v", err)
		}
		mType := hdr[0]
		mLen := binary.BigEndian.Uint32(hdr[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(rawConn, payload); err != nil {
			t.Fatalf("startup read payload failed: %v", err)
		}
		if mType == 'Z' {
			break
		}
	}

	// 2. Send Extended Query batch: Parse ('P') -> Bind ('B') -> Describe ('D') -> Execute ('E') -> Sync ('S')
	var extBatch bytes.Buffer
	// Parse ('P'): stmtName="", query="EXEC sp_pg_multi_dataset;"
	pQuery := "EXEC sp_pg_multi_dataset;\x00"
	pLen := int32(4 + 1 + len(pQuery) + 2)
	extBatch.WriteByte('P')
	binary.Write(&extBatch, binary.BigEndian, pLen)
	extBatch.WriteByte(0) // stmtName = ""
	extBatch.WriteString(pQuery)
	binary.Write(&extBatch, binary.BigEndian, int16(0)) // 0 param types

	// Bind ('B'): portalName="", stmtName=""
	bLen := int32(4 + 1 + 1 + 2 + 2 + 2)
	extBatch.WriteByte('B')
	binary.Write(&extBatch, binary.BigEndian, bLen)
	extBatch.WriteByte(0)                               // portalName = ""
	extBatch.WriteByte(0)                               // stmtName = ""
	binary.Write(&extBatch, binary.BigEndian, int16(0)) // 0 param formats
	binary.Write(&extBatch, binary.BigEndian, int16(0)) // 0 params
	binary.Write(&extBatch, binary.BigEndian, int16(0)) // 0 result formats

	// Describe ('D'): 'P' (portal), name=""
	dLen := int32(4 + 1 + 1)
	extBatch.WriteByte('D')
	binary.Write(&extBatch, binary.BigEndian, dLen)
	extBatch.WriteByte('P')
	extBatch.WriteByte(0)

	// Execute ('E'): portalName="", maxRows=0
	eLen := int32(4 + 1 + 4)
	extBatch.WriteByte('E')
	binary.Write(&extBatch, binary.BigEndian, eLen)
	extBatch.WriteByte(0)
	binary.Write(&extBatch, binary.BigEndian, int32(0))

	// Sync ('S')
	extBatch.WriteByte('S')
	binary.Write(&extBatch, binary.BigEndian, int32(4))

	if _, err := rawConn.Write(extBatch.Bytes()); err != nil {
		t.Fatalf("failed to write Extended Query batch: %v", err)
	}

	// 3. Collect response tokens until ReadyForQuery ('Z')
	var msgTypes []byte
	for {
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(rawConn, hdr); err != nil {
			t.Fatalf("failed to read response header: %v", err)
		}
		mType := hdr[0]
		mLen := binary.BigEndian.Uint32(hdr[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(rawConn, payload); err != nil {
			t.Fatalf("failed to read response payload: %v", err)
		}
		msgTypes = append(msgTypes, mType)
		if mType == 'Z' {
			break
		}
	}

	t.Logf("Extended Query message sequence: %c", msgTypes)

	// Expected sequence:
	// '1': ParseComplete
	// '2': BindComplete
	// 'T': RowDescription (Set 1, from Describe)
	// 'D': DataRow (Set 1)
	// 'C': CommandComplete (Set 1)
	// 'T': RowDescription (Set 2, from Execute)
	// 'D': DataRow (Set 2)
	// 'C': CommandComplete (Set 2)
	// 'Z': ReadyForQuery
	expected := []byte{'1', '2', 'T', 'D', 'C', 'T', 'D', 'C', 'Z'}
	if len(msgTypes) != len(expected) {
		t.Fatalf("expected message sequence %d (%c), got %d (%c)", len(expected), expected, len(msgTypes), msgTypes)
	}
	for i, exp := range expected {
		if msgTypes[i] != exp {
			t.Errorf("at index %d: expected %c, got %c", i, exp, msgTypes[i])
		}
	}

	// 4. Test CALL syntax parity
	var callBatch bytes.Buffer
	cQuery := "CALL sp_pg_multi_dataset();\x00"
	cLen := int32(4 + 1 + len(cQuery) + 2)
	callBatch.WriteByte('P')
	binary.Write(&callBatch, binary.BigEndian, cLen)
	callBatch.WriteByte(0)
	callBatch.WriteString(cQuery)
	binary.Write(&callBatch, binary.BigEndian, int16(0))

	callBatch.WriteByte('B')
	binary.Write(&callBatch, binary.BigEndian, int32(4+1+1+2+2+2))
	callBatch.WriteByte(0)
	callBatch.WriteByte(0)
	binary.Write(&callBatch, binary.BigEndian, int16(0))
	binary.Write(&callBatch, binary.BigEndian, int16(0))
	binary.Write(&callBatch, binary.BigEndian, int16(0))

	callBatch.WriteByte('D')
	binary.Write(&callBatch, binary.BigEndian, int32(4+1+1))
	callBatch.WriteByte('P')
	callBatch.WriteByte(0)

	callBatch.WriteByte('E')
	binary.Write(&callBatch, binary.BigEndian, int32(4+1+4))
	callBatch.WriteByte(0)
	binary.Write(&callBatch, binary.BigEndian, int32(0))

	callBatch.WriteByte('S')
	binary.Write(&callBatch, binary.BigEndian, int32(4))

	if _, err := rawConn.Write(callBatch.Bytes()); err != nil {
		t.Fatalf("failed to write CALL batch: %v", err)
	}

	var callMsgTypes []byte
	for {
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(rawConn, hdr); err != nil {
			t.Fatalf("failed to read CALL response header: %v", err)
		}
		mType := hdr[0]
		mLen := binary.BigEndian.Uint32(hdr[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(rawConn, payload); err != nil {
			t.Fatalf("failed to read CALL response payload: %v", err)
		}
		callMsgTypes = append(callMsgTypes, mType)
		if mType == 'Z' {
			break
		}
	}

	t.Logf("CALL Extended Query message sequence: %c", callMsgTypes)
	if len(callMsgTypes) != len(expected) {
		t.Fatalf("expected CALL message sequence %d (%c), got %d (%c)", len(expected), expected, len(callMsgTypes), callMsgTypes)
	}
	for i, exp := range expected {
		if callMsgTypes[i] != exp {
			t.Errorf("CALL at index %d: expected %c, got %c", i, exp, callMsgTypes[i])
		}
	}
}
