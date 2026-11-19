package wire

import (
	"encoding/binary"
	"io"
	"testing"
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
