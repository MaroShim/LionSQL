package wire_test

import (
	"testing"

	"lionsql/pkg/wire"
)

func TestLookupParamOID_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		isAny    bool
		wantOID  int32
	}{
		{"Scalar text token NSPNAME", "NSPNAME", false, 25},
		{"Array text token NSPNAME", "NSPNAME", true, 1009},
		{"Scalar text token RELNAME", "RELNAME", false, 25},
		{"Array text token RELNAME", "RELNAME", true, 1009},
		{"Scalar text token ATTNAME", "ATTNAME", false, 25},
		{"Array text token ATTNAME", "ATTNAME", true, 1009},
		{"Scalar text token TYPNAME", "TYPNAME", false, 25},
		{"Array text token TYPNAME", "TYPNAME", true, 1009},
		{"Scalar text token NAME", "NAME", false, 25},
		{"Array text token NAME", "NAME", true, 1009},
		{"Scalar oid token OID", "OID", false, 26},
		{"Array oid token OID", "OID", true, 1028},
		{"Scalar int4 token ID", "ID", false, 23},
		{"Array int4 token ID", "ID", true, 1007},
		{"Unknown token returns 0", "UNKNOWN_COLUMN", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wire.LookupParamOID(tt.token, tt.isAny)
			if got != tt.wantOID {
				t.Errorf("LookupParamOID(%q, %v) = %d; want %d", tt.token, tt.isAny, got, tt.wantOID)
			}
		})
	}
}

func TestInferParamOID_MultiParamCollision(t *testing.T) {
	// Query where $10 precedes $1
	query := "SELECT * FROM tbl WHERE id = $10 AND relname = $1"

	// $1 should be inferred as TEXT (25) from relname, NOT INT4 (23) from id
	got1 := wire.InferParamOID(query, 1)
	if got1 != 25 {
		t.Errorf("InferParamOID for $1 = %d; want 25 (TEXT)", got1)
	}

	// $10 should be inferred as INT4 (23) from id
	got10 := wire.InferParamOID(query, 10)
	if got10 != 23 {
		t.Errorf("InferParamOID for $10 = %d; want 23 (INT4)", got10)
	}
}

func TestInferParamOID_EdgeCases(t *testing.T) {
	// 1. $1 inside single-quoted string literal
	q1 := "SELECT * FROM tbl WHERE note = 'price is $1' AND relname = $1"
	if got := wire.InferParamOID(q1, 1); got != 25 {
		t.Errorf("InferParamOID with literal = %d; want 25", got)
	}

	// 2. $1 inside line comment
	q2 := "SELECT * FROM tbl WHERE id = 1 -- comment with $1\n AND relname = $1"
	if got := wire.InferParamOID(q2, 1); got != 25 {
		t.Errorf("InferParamOID with line comment = %d; want 25", got)
	}

	// 3. $1 inside block comment
	q3 := "SELECT * FROM tbl WHERE id = 1 /* $1 */ AND relname = $1"
	if got := wire.InferParamOID(q3, 1); got != 25 {
		t.Errorf("InferParamOID with block comment = %d; want 25", got)
	}

	// 4. $1 as ANY array parameter
	q4 := "SELECT * FROM tbl WHERE id = $10 AND relname = ANY($1)"
	if got := wire.InferParamOID(q4, 1); got != 1009 {
		t.Errorf("InferParamOID for ANY($1) = %d; want 1009 (TEXT[])", got)
	}

	// 5. Unquoted identifier ending with $1
	q5 := "SELECT * FROM tbl WHERE col$1 = 10 AND relname = $1"
	if got := wire.InferParamOID(q5, 1); got != 25 {
		t.Errorf("InferParamOID with ident col$1 = %d; want 25", got)
	}

	// 6. Multiple $1 where first occurrence is in non-comparison expr and second is equality
	q6 := "SELECT * FROM tbl WHERE coalesce(description, $1) IS NOT NULL AND relname = $1"
	if got := wire.InferParamOID(q6, 1); got != 25 {
		t.Errorf("InferParamOID with multiple $1 = %d; want 25", got)
	}
}
