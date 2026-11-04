package virtual

import (
	"testing"
)

func TestCleanIdentifierMultipart(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"[master].[sys].[tables]", "tables"},
		{"master.sys.tables", "tables"},
		{"[sys].[columns]", "columns"},
		{"[Users]", "Users"},
		{`"Products"`, "Products"},
		{"db.schema.tbl.col", "col"},
	}

	for _, tt := range tests {
		got := cleanIdentifier(tt.input)
		if got != tt.expected {
			t.Errorf("cleanIdentifier(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestVirtualCatalogInterceptorBrackets(t *testing.T) {
	sql1 := "SELECT * FROM [sys].[tables] t JOIN [sys].[schemas] s ON t.schema_id = s.schema_id"
	if !IsVirtualCatalogJoinQuery(sql1) {
		t.Errorf("IsVirtualCatalogJoinQuery failed for bracketed sys.tables join")
	}

	sql2 := "SELECT * FROM [sys].[columns] c JOIN [sys].[types] ty ON c.user_type_id = ty.user_type_id"
	if !IsVirtualCatalogJoinQuery(sql2) {
		t.Errorf("IsVirtualCatalogJoinQuery failed for bracketed sys.columns join")
	}
}

func TestMssqlRegexUnicodeAndBrackets(t *testing.T) {
	// 1. Bracketed column name with Unicode literal N'...'
	query1 := "SELECT * FROM sys.columns WHERE [t].[name] = N'Orders'"
	tbl1, ok1 := ExtractMssqlTableNameEq(query1)
	if !ok1 || tbl1 != "Orders" {
		t.Errorf("ExtractMssqlTableNameEq failed on query1, got: %v, %v", tbl1, ok1)
	}

	// 2. Standard single quote without Unicode prefix
	query2 := "SELECT * FROM sys.columns WHERE t.name = 'Customers'"
	tbl2, ok2 := ExtractMssqlTableNameEq(query2)
	if !ok2 || tbl2 != "Customers" {
		t.Errorf("ExtractMssqlTableNameEq failed on query2, got: %v, %v", tbl2, ok2)
	}

	// 3. Schema name with Unicode literal
	query3 := "SELECT * FROM sys.schemas WHERE [s].[name] = N'dbo'"
	sch3, ok3 := ExtractMssqlSchemaNameEq(query3)
	if !ok3 || sch3 != "dbo" {
		t.Errorf("ExtractMssqlSchemaNameEq failed on query3, got: %v, %v", sch3, ok3)
	}
}
