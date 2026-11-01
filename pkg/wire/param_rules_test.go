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
