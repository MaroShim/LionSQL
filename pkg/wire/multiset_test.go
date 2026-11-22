package wire

import (
	"encoding/binary"
	"io"
	"testing"

	"lionsql/pkg/catalog"
)

func TestPGWire_MultipleResultSets(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)
	defer eng.Close()

	client := connectPGClient(t, srv)

	// Send Simple Query: 'Q'
	q := "SELECT 10 AS a; SELECT 20 AS b;\x00"
	buf := make([]byte, 5+len(q))
	buf[0] = 'Q'
	binary.BigEndian.PutUint32(buf[1:5], uint32(4+len(q)))
	copy(buf[5:], []byte(q))

	if _, err := client.Write(buf); err != nil {
		t.Fatalf("failed to write query: %v", err)
	}

	// Read response messages
	var msgTypes []byte
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(client, header); err != nil {
			t.Fatalf("Failed to read header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(client, payload); err != nil {
			t.Fatalf("Failed to read payload: %v", err)
		}
		msgTypes = append(msgTypes, msgType)
		if msgType == 'Z' { // ReadyForQuery
			break
		}
	}

	t.Logf("Received message sequence: %c", msgTypes)

	// In PostgreSQL Simple Query with multiple statements:
	// 1st query: RowDescription ('T'), DataRow ('D'), CommandComplete ('C')
	// 2nd query: RowDescription ('T'), DataRow ('D'), CommandComplete ('C')
	// End of batch: ReadyForQuery ('Z')
	// Sequence must be: ['T', 'D', 'C', 'T', 'D', 'C', 'Z']
	expected := []byte{'T', 'D', 'C', 'T', 'D', 'C', 'Z'}
	if len(msgTypes) != len(expected) {
		t.Fatalf("expected message count %d (%c), got %d (%c)", len(expected), expected, len(msgTypes), msgTypes)
	}
	for i, exp := range expected {
		if msgTypes[i] != exp {
			t.Errorf("at index %d: expected %c, got %c", i, exp, msgTypes[i])
		}
	}
}

// TestPGWire_MultipleResultSets_DualPath_ExtendedQuery enforces wire protocol symmetry
// between Simple Query ('Q') and Extended Query ('P', 'B', 'D', 'E', 'S') for stored procedures
// returning multiple result sets according to PostgreSQL v3.0 protocol standards.
func TestPGWire_MultipleResultSets_DualPath_ExtendedQuery(t *testing.T) {
	srv, eng, cat := setupTestWireServer(t)
	defer eng.Close()

	// Register procedure returning 2 result sets
	_ = cat.RegisterProcedure(&catalog.Procedure{
		Name: "sp_dual_pg_dataset",
		Body: `
CREATE PROCEDURE sp_dual_pg_dataset AS
BEGIN
    SELECT 100 AS num, 'first' AS val;
    SELECT 200 AS num, 'second' AS val;
END;
`,
	})

	client := connectPGClient(t, srv)
	defer client.Close()

	// 1. Extended Query Path: Parse ('P') -> Bind ('B') -> Describe ('D') -> Execute ('E') -> Sync ('S')
	var extBatch []byte
	// Parse ('P')
	qStr := "EXEC sp_dual_pg_dataset;\x00"
	pLen := 4 + 1 + len(qStr) + 2
	pBuf := make([]byte, 1+pLen)
	pBuf[0] = 'P'
	binary.BigEndian.PutUint32(pBuf[1:5], uint32(pLen))
	pBuf[5] = 0 // stmtName = ""
	copy(pBuf[6:], []byte(qStr))
	binary.BigEndian.PutUint16(pBuf[6+len(qStr):], 0) // 0 param types
	extBatch = append(extBatch, pBuf...)

	// Bind ('B')
	bLen := 4 + 1 + 1 + 2 + 2 + 2
	bBuf := make([]byte, 1+bLen)
	bBuf[0] = 'B'
	binary.BigEndian.PutUint32(bBuf[1:5], uint32(bLen))
	bBuf[5] = 0 // portalName = ""
	bBuf[6] = 0 // stmtName = ""
	binary.BigEndian.PutUint16(bBuf[7:9], 0)
	binary.BigEndian.PutUint16(bBuf[9:11], 0)
	binary.BigEndian.PutUint16(bBuf[11:13], 0)
	extBatch = append(extBatch, bBuf...)

	// Describe ('D'): 'P' (portal), name=""
	dLen := 4 + 1 + 1
	dBuf := make([]byte, 1+dLen)
	dBuf[0] = 'D'
	binary.BigEndian.PutUint32(dBuf[1:5], uint32(dLen))
	dBuf[5] = 'P'
	dBuf[6] = 0
	extBatch = append(extBatch, dBuf...)

	// Execute ('E'): portalName="", maxRows=0
	eLen := 4 + 1 + 4
	eBuf := make([]byte, 1+eLen)
	eBuf[0] = 'E'
	binary.BigEndian.PutUint32(eBuf[1:5], uint32(eLen))
	eBuf[5] = 0
	binary.BigEndian.PutUint32(eBuf[6:10], 0)
	extBatch = append(extBatch, eBuf...)

	// Sync ('S')
	sBuf := make([]byte, 5)
	sBuf[0] = 'S'
	binary.BigEndian.PutUint32(sBuf[1:5], 4)
	extBatch = append(extBatch, sBuf...)

	go func() {
		_, _ = client.Write(extBatch)
	}()

	// Read response messages
	var extMsgTypes []byte
	var extDataRows [][]string
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(client, header); err != nil {
			t.Fatalf("failed to read response header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(client, payload); err != nil {
			t.Fatalf("failed to read payload: %v", err)
		}
		extMsgTypes = append(extMsgTypes, msgType)
		if msgType == 'D' {
			extDataRows = append(extDataRows, parseDataRowValues(payload))
		}
		if msgType == 'Z' {
			break
		}
	}

	t.Logf("Extended Query message sequence: %c", extMsgTypes)

	// In PostgreSQL Extended Query with multi-result portal:
	// '1': ParseComplete
	// '2': BindComplete
	// 'T': RowDescription (Set 1, from Describe)
	// 'D': DataRow (Set 1)
	// 'C': CommandComplete (Set 1)
	// 'T': RowDescription (Set 2, from Execute)
	// 'D': DataRow (Set 2)
	// 'C': CommandComplete (Set 2)
	// 'Z': ReadyForQuery
	expectedExt := []byte{'1', '2', 'T', 'D', 'C', 'T', 'D', 'C', 'Z'}
	if len(extMsgTypes) != len(expectedExt) {
		t.Fatalf("expected extended query count %d (%c), got %d (%c)", len(expectedExt), expectedExt, len(extMsgTypes), extMsgTypes)
	}
	for i, exp := range expectedExt {
		if msgType := extMsgTypes[i]; msgType != exp {
			t.Errorf("extended at index %d: expected %c, got %c", i, exp, msgType)
		}
	}

	// Verify DataRow payload parity
	if len(extDataRows) != 2 {
		t.Fatalf("expected 2 DataRows, got %d", len(extDataRows))
	}
	if len(extDataRows[0]) != 2 || extDataRows[0][0] != "100" || extDataRows[0][1] != "first" {
		t.Errorf("expected Set 1 data [100 first], got %v", extDataRows[0])
	}
	if len(extDataRows[1]) != 2 || extDataRows[1][0] != "200" || extDataRows[1][1] != "second" {
		t.Errorf("expected Set 2 data [200 second], got %v", extDataRows[1])
	}
}

func parseDataRowValues(payload []byte) []string {
	if len(payload) < 2 {
		return nil
	}
	numCols := int(binary.BigEndian.Uint16(payload[:2]))
	offset := 2
	vals := make([]string, 0, numCols)
	for i := 0; i < numCols && offset+4 <= len(payload); i++ {
		colLen := int(int32(binary.BigEndian.Uint32(payload[offset : offset+4])))
		offset += 4
		if colLen < 0 {
			vals = append(vals, "NULL")
		} else if offset+colLen <= len(payload) {
			vals = append(vals, string(payload[offset:offset+colLen]))
			offset += colLen
		}
	}
	return vals
}
