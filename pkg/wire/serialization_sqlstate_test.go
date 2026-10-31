package wire

import (
	"bytes"
	"testing"
)

func TestWriteError_SerializationFailureUsesSQLState40001(t *testing.T) {
	var buf bytes.Buffer
	msg := "serialization failure: concurrent update conflict (wiredtiger cursor_update code -31800)"
	if err := WriteError(&buf, msg); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("C40001\x00")) {
		t.Fatalf("expected SQLSTATE 40001 in ErrorResponse, got %q", buf.String())
	}
}
