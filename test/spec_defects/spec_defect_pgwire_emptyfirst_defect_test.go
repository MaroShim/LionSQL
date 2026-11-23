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

// TestDEFECT_PGWire_ExtendedQuery_EmptyFirstResultSet verifies:
// Defect ID: DEFECT-PGWIRE-EXT-EMPTY-COL-DROP-1
// When the first result set has len(res.Columns) == 0 (e.g. an UPDATE or INSERT statement
// inside a stored procedure or batch followed by a SELECT statement), handleExecute must NOT
// drop the subsequent result sets (res.Next).
func TestDEFECT_PGWire_ExtendedQuery_EmptyFirstResultSet(t *testing.T) {
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

	schemaSQL := `
CREATE TABLE t_dummy (id INT);
INSERT INTO t_dummy VALUES (42);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	procSQL := `
CREATE PROCEDURE sp_pg_update_then_select
AS
BEGIN
    UPDATE t_dummy SET id = id + 1;
    SELECT id, 'result' AS msg FROM t_dummy;
END;
`
	if _, err := db.ExecContext(ctx, procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

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

	// Extended Query batch: Parse -> Bind -> Describe -> Execute -> Sync
	var batch bytes.Buffer
	query := "UPDATE t_dummy SET id = id + 1; SELECT id, 'result' AS msg FROM t_dummy;"
	batch.WriteByte('P')
	binary.Write(&batch, binary.BigEndian, int32(4+1+len(query)+1+2))
	batch.WriteByte(0)
	batch.WriteString(query)
	batch.WriteByte(0)
	binary.Write(&batch, binary.BigEndian, int16(0))

	batch.WriteByte('B')
	binary.Write(&batch, binary.BigEndian, int32(4+1+1+2+2+2))
	batch.WriteByte(0)
	batch.WriteByte(0)
	binary.Write(&batch, binary.BigEndian, int16(0))
	binary.Write(&batch, binary.BigEndian, int16(0))
	binary.Write(&batch, binary.BigEndian, int16(0))

	batch.WriteByte('D')
	binary.Write(&batch, binary.BigEndian, int32(4+1+1))
	batch.WriteByte('P')
	batch.WriteByte(0)

	batch.WriteByte('E')
	binary.Write(&batch, binary.BigEndian, int32(4+1+4))
	batch.WriteByte(0)
	binary.Write(&batch, binary.BigEndian, int32(0))

	batch.WriteByte('S')
	binary.Write(&batch, binary.BigEndian, int32(4))

	if _, err := rawConn.Write(batch.Bytes()); err != nil {
		t.Fatalf("failed to write batch: %v", err)
	}

	var msgTypes []byte
	var dataRowsCount int
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
		if mType == 'E' {
			t.Logf("Error message received: %s", string(payload))
		}
		msgTypes = append(msgTypes, mType)
		if mType == 'D' {
			dataRowsCount++
		}
		if mType == 'Z' {
			break
		}
	}

	t.Logf("Extended Query messages: %c, dataRows: %d", msgTypes, dataRowsCount)
	if dataRowsCount != 1 {
		t.Fatalf("[DEFECT-PGWIRE-EXT-EMPTY-COL-DROP-1] Expected 1 DataRow ('D') for subsequent SELECT result set, but got %d! Messages: %c", dataRowsCount, msgTypes)
	}
}
