package wire

import (
	"bytes"
	"testing"

	"lionsql/pkg/storage"
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

func TestDecodeTextParamValue_HexAndLeadingZeroStringsStayVarChar(t *testing.T) {
	for _, s := range []string{"83920194837261e2", "0091827364512345", "1234e56"} {
		v := decodeTextParamValue(s, 0)
		if v.Type != storage.TypeVarChar {
			t.Fatalf("decodeTextParamValue(%q, 0) got type %v, want TypeVarChar", s, v.Type)
		}
		formatted := formatParamForSQL(s, 0, v)
		want := "'" + s + "'"
		if formatted != want {
			t.Fatalf("formatParamForSQL(%q, 0) = %q, want %q", s, formatted, want)
		}
	}
}
