package wire

import (
	"bytes"
	"net"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

type dummyConn struct {
	net.Conn
	buf bytes.Buffer
}

func (d *dummyConn) Write(b []byte) (n int, err error) {
	return d.buf.Write(b)
}

func (d *dummyConn) Read(b []byte) (n int, err error) {
	return 0, nil
}

func (d *dummyConn) Close() error {
	return nil
}

func (d *dummyConn) SetDeadline(t time.Time) error      { return nil }
func (d *dummyConn) SetReadDeadline(t time.Time) error  { return nil }
func (d *dummyConn) SetWriteDeadline(t time.Time) error { return nil }

func TestPGWire_StatementMetadataCaching(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := NewServer("127.0.0.1:0", cat, eng)

	// Create table
	_, err := server.coordinator.ExecuteSQL("CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50), price INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	prep := &PreparedStatement{
		Name:  "test_stmt",
		Query: "SELECT id, name, price FROM items WHERE price > $1",
	}

	conn1 := &dummyConn{}
	// First describe - should resolve and populate ColumnNames and ColumnOIDs
	server.writeSchemaDescription(conn1, prep.Query, []int32{23}, prep, nil)

	if len(prep.ColumnNames) != 3 || len(prep.ColumnOIDs) != 3 {
		t.Fatalf("expected 3 cached columns, got %d names, %d oids", len(prep.ColumnNames), len(prep.ColumnOIDs))
	}

	if prep.ColumnNames[0] != "id" || prep.ColumnNames[1] != "name" || prep.ColumnNames[2] != "price" {
		t.Fatalf("unexpected cached names: %v", prep.ColumnNames)
	}

	// Second describe - should use cached ColumnOIDs directly
	conn2 := &dummyConn{}
	server.writeSchemaDescription(conn2, prep.Query, []int32{23}, prep, nil)

	if conn2.buf.Len() == 0 {
		t.Fatalf("expected RowDescription message in conn2 buffer, got 0 bytes")
	}

	// Test NoData caching for UPDATE without RETURNING
	prepUpdate := &PreparedStatement{
		Name:  "test_update",
		Query: "UPDATE items SET price = price + 1 WHERE id = $1",
	}

	conn3 := &dummyConn{}
	server.writeSchemaDescription(conn3, prepUpdate.Query, []int32{23}, prepUpdate, nil)
	if !prepUpdate.NoData {
		t.Fatalf("expected prepUpdate.NoData to be true after describe")
	}

	conn4 := &dummyConn{}
	server.writeSchemaDescription(conn4, prepUpdate.Query, []int32{23}, prepUpdate, nil)
	// Byte 'n' (0x6e) is NoData message in PostgreSQL wire protocol
	if conn4.buf.Bytes()[0] != 'n' {
		t.Fatalf("expected NoData byte 'n', got %c", conn4.buf.Bytes()[0])
	}
}
