package wire

import (
	"encoding/binary"
	"io"
	"net"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestGroup3_CopyIn_ZeroPKPreserved tests BUG-COPY-PK-1:
// PGWire COPY FROM STDIN preserves integer PK = 0 and does not overwrite it with a timestamp.
func TestGroup3_CopyIn_ZeroPKPreserved(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	srv := NewServer("127.0.0.1:0", cat, eng)

	schema := &storage.TableSchema{
		Name: "system_nodes",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "hostname", Type: storage.TypeVarChar, Nullable: false},
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

	sendSimpleQuery(t, clientConn, "COPY system_nodes FROM STDIN;")

	hdr := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, hdr); err != nil {
		t.Fatalf("read copy in hdr: %v", err)
	}
	if hdr[0] != MsgCopyInResponse {
		t.Fatalf("expected 'G', got %c", hdr[0])
	}
	rLen := binary.BigEndian.Uint32(hdr[1:5])
	_, _ = io.ReadFull(clientConn, make([]byte, rLen-4))

	// Send row with id = 0
	tsvData := []byte("0\tmaster-node\n1\tworker-node\n\\.\n")
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

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()
	defer sess.Close()

	rec, err := tx.Get("system_nodes", storage.IntValue(0))
	if err != nil {
		t.Fatalf("failed to get node with id=0 (was it overwritten by timestamp?): %v", err)
	}
	idVal, ok := rec["id"]
	if !ok || idVal.IVal != 0 {
		t.Fatalf("expected id=0 preserved, got: %+v", idVal)
	}
}
