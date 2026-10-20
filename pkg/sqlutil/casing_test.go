package sqlutil

import (
	"testing"
)

func TestCanonicalIdent(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"users", "users"},
		{"Users", "users"},
		{"USERS", "users"},
		{"[Users]", "users"},
		{"`users`", "users"},
		{`"Users"`, "users"},
		{"'Users'", "users"},
		{"  [dbo]  ", "dbo"},
	}

	for _, tt := range tests {
		got := CanonicalIdent(tt.input)
		if got != tt.expected {
			t.Errorf("CanonicalIdent(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestEqualFoldIdent(t *testing.T) {
	if !EqualFoldIdent("[Users]", "users") {
		t.Errorf("EqualFoldIdent failed for [Users] and users")
	}
	if !EqualFoldIdent(`"MyTable"`, "[mytable]") {
		t.Errorf("EqualFoldIdent failed for \"MyTable\" and [mytable]")
	}
	if EqualFoldIdent("Users", "Orders") {
		t.Errorf("EqualFoldIdent should be false for Users and Orders")
	}
}

func TestSliceContainsFold(t *testing.T) {
	slice := []string{"[ID]", "UserName", "Email"}
	if !SliceContainsFold(slice, "id") {
		t.Errorf("SliceContainsFold expected true for id")
	}
	if !SliceContainsFold(slice, "username") {
		t.Errorf("SliceContainsFold expected true for username")
	}
	if !SliceContainsFold(slice, "[EMAIL]") {
		t.Errorf("SliceContainsFold expected true for [EMAIL]")
	}
	if SliceContainsFold(slice, "non_existent") {
		t.Errorf("SliceContainsFold expected false for non_existent")
	}
}

func TestMapHelpersFold(t *testing.T) {
	m := make(map[string]int)
	MapSetFold(m, "Users", 100)

	// Get with different casing and brackets
	if val, ok := MapGetFold(m, "users"); !ok || val != 100 {
		t.Errorf("MapGetFold with 'users' failed, got (%v, %v)", val, ok)
	}
	if val, ok := MapGetFold(m, "[USERS]"); !ok || val != 100 {
		t.Errorf("MapGetFold with '[USERS]' failed, got (%v, %v)", val, ok)
	}

	// Find key
	if k, ok := MapFindKeyFold(m, "users"); !ok || k != "Users" {
		t.Errorf("MapFindKeyFold expected 'Users', got %q", k)
	}

	// Update existing key
	MapSetFold(m, "users", 200)
	if m["Users"] != 200 {
		t.Errorf("MapSetFold should update existing casing key 'Users', got %d", m["Users"])
	}
	if len(m) != 1 {
		t.Errorf("MapSetFold should not add duplicate key, len is %d", len(m))
	}

	// Delete
	if !MapDeleteFold(m, "[users]") {
		t.Errorf("MapDeleteFold failed")
	}
	if len(m) != 0 {
		t.Errorf("Map should be empty after delete, len is %d", len(m))
	}
}

func TestCIMap(t *testing.T) {
	ci := NewCIMap[string]()

	ci.Set("[Customers]", "cust_data")
	ci.Set("Orders", "order_data")

	if ci.Len() != 2 {
		t.Fatalf("expected len 2, got %d", ci.Len())
	}

	// Test Get
	if val, ok := ci.Get("customers"); !ok || val != "cust_data" {
		t.Errorf("ci.Get('customers') failed, got (%v, %v)", val, ok)
	}
	if val, ok := ci.Get("[CUSTOMERS]"); !ok || val != "cust_data" {
		t.Errorf("ci.Get('[CUSTOMERS]') failed, got (%v, %v)", val, ok)
	}
	if !ci.Has("orders") {
		t.Errorf("ci.Has('orders') expected true")
	}

	// Test OriginalKey
	if orig, ok := ci.OriginalKey("customers"); !ok || orig != "Customers" {
		t.Errorf("ci.OriginalKey('customers') expected 'Customers', got %q", orig)
	}

	// Update preserves original key
	ci.Set("CUSTOMERS", "updated_cust_data")
	if val, ok := ci.Get("customers"); !ok || val != "updated_cust_data" {
		t.Errorf("expected updated value, got %v", val)
	}
	if orig, _ := ci.OriginalKey("customers"); orig != "Customers" {
		t.Errorf("expected original key 'Customers' preserved, got %q", orig)
	}
	if ci.Len() != 2 {
		t.Errorf("expected len 2 after update, got %d", ci.Len())
	}

	// Delete
	if !ci.Delete("CUSTOMERS") {
		t.Errorf("ci.Delete failed")
	}
	if ci.Has("customers") {
		t.Errorf("customers should be deleted")
	}
	if ci.Len() != 1 {
		t.Errorf("expected len 1, got %d", ci.Len())
	}
}
