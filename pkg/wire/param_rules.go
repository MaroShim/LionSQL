package wire

// ParamRule defines a lookup entry for catalog and table column parameter OID inference.
type ParamRule struct {
	ColumnToken string // Uppercase token, e.g. "NSPNAME", "RELNAME", "OID", "ID"
	ScalarOID   int32  // PostgreSQL OID for scalar value (e.g. 25 for text, 26 for oid, 23 for int4)
	ArrayOID    int32  // PostgreSQL OID for array value (e.g. 1009 for _text, 1028 for _oid, 1007 for _int4)
}

// defaultParamOIDRules is the immutable lookup table for inferring parameter OIDs in system catalog queries.
var defaultParamOIDRules = map[string]ParamRule{
	"OID": {
		ColumnToken: "OID",
		ScalarOID:   26,   // oid
		ArrayOID:    1028, // _oid
	},
	"NSPNAME": {
		ColumnToken: "NSPNAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"RELNAME": {
		ColumnToken: "RELNAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"ATTNAME": {
		ColumnToken: "ATTNAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"TYPNAME": {
		ColumnToken: "TYPNAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"NAME": {
		ColumnToken: "NAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"ID": {
		ColumnToken: "ID",
		ScalarOID:   23,   // int4
		ArrayOID:    1007, // _int4
	},
}

// LookupParamOID resolves the parameter OID for a given column token and array flag from the table.
func LookupParamOID(columnToken string, isAny bool) int32 {
	if rule, ok := defaultParamOIDRules[columnToken]; ok {
		if isAny {
			return rule.ArrayOID
		}
		return rule.ScalarOID
	}
	return 0
}
