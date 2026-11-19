package sqlutil

import "testing"

func TestIsSQLTypeName(t *testing.T) {
	validTypes := []string{
		"int", "INT", "INTEGER", "bigint", "SMALLINT", "tinyint",
		"FLOAT", "double", "precision", "real", "decimal", "numeric",
		"varchar", "NVARCHAR", "char", "nchar", "text", "ntext",
		"date", "time", "datetime", "datetime2", "timestamp",
		"bit", "boolean", "bool", "uuid", "uniqueidentifier",
		"json", "jsonb", "xml", "bytea", "binary", "varbinary",
	}

	for _, vt := range validTypes {
		if !IsSQLTypeName(vt) {
			t.Errorf("IsSQLTypeName(%q) = false, want true", vt)
		}
	}

	invalidTypes := []string{
		"id", "user_id", "name", "email", "total_amount", "orders",
		"unknown_type", "select", "where", "", "  ",
	}

	for _, it := range invalidTypes {
		if IsSQLTypeName(it) {
			t.Errorf("IsSQLTypeName(%q) = true, want false", it)
		}
	}
}
