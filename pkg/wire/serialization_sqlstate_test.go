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

func TestWriteError_IndexDoesNotExistUsesSQLState42704(t *testing.T) {
	testCases := []string{
		`index "nc_fr_file_url" does not exist`,
		`index idx_user_id not found`,
	}
	for _, msg := range testCases {
		var buf bytes.Buffer
		if err := WriteError(&buf, msg); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(buf.Bytes(), []byte("C42704\x00")) {
			t.Fatalf("for %q, expected SQLSTATE 42704 in ErrorResponse, got %q", msg, buf.String())
		}
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

func TestWriteError_ConstraintViolationSQLStates(t *testing.T) {
	cases := []struct {
		msg      string
		wantCode string
	}{
		{
			msg:      `check constraint violation: new row for relation "users" violates check constraint (age >= 18)`,
			wantCode: "23514",
		},
		{
			msg:      `check constraint violation on "orders": amount > 0`,
			wantCode: "23514",
		},
		{
			msg:      `foreign key constraint violation: key (user_id)=(99) is not present in table "users"`,
			wantCode: "23503",
		},
		{
			msg:      `not null constraint violation: column "email"`,
			wantCode: "23502",
		},
	}

	for _, tc := range cases {
		var buf bytes.Buffer
		if err := WriteError(&buf, tc.msg); err != nil {
			t.Fatal(err)
		}
		wantField := []byte("C" + tc.wantCode + "\x00")
		if !bytes.Contains(buf.Bytes(), wantField) {
			t.Fatalf("for %q, expected SQLSTATE %s in ErrorResponse, got %q", tc.msg, tc.wantCode, buf.String())
		}
	}
}
