package storage_test

import (
	"fmt"
	"testing"

	"sqlion/pkg/storage"
)

func TestJSON_IsValidJSON(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{`{"name": "Alice"}`, true},
		{`[1, 2, 3]`, true},
		{`"hello"`, true},
		{`123`, true},
		{`true`, true},
		{`null`, true},
		{`{"unclosed": `, false},
		{``, false},
		{`   `, false},
		{`invalid`, false},
	}

	for _, tt := range tests {
		got := storage.IsValidJSON(tt.input)
		if got != tt.valid {
			t.Errorf("IsValidJSON(%q) = %v; want %v", tt.input, got, tt.valid)
		}
	}
}

func TestJSON_ExtractOperators(t *testing.T) {
	doc := `{"user": {"name": "Bob", "age": 30, "admin": true}, "tags": ["go", "db"]}`

	// -> returns json string
	res, ok := storage.ExtractJSON(doc, "user")
	if !ok || res != `{"admin":true,"age":30,"name":"Bob"}` {
		t.Fatalf("ExtractJSON(user) = %q, %v", res, ok)
	}

	// ->> returns unquoted text
	resText, ok := storage.ExtractJSONText(doc, "user")
	if !ok || resText != `{"admin":true,"age":30,"name":"Bob"}` {
		t.Fatalf("ExtractJSONText(user) = %q, %v", resText, ok)
	}

	// Sub-element extraction
	resName, ok := storage.ExtractJSONText(res, "name")
	if !ok || resName != "Bob" {
		t.Fatalf("ExtractJSONText(name) = %q, %v; want Bob", resName, ok)
	}

	// Array index extraction
	tagDoc, ok := storage.ExtractJSON(doc, "tags")
	if !ok {
		t.Fatalf("ExtractJSON(tags) failed")
	}
	tag0, ok := storage.ExtractJSONText(tagDoc, 0)
	if !ok || tag0 != "go" {
		t.Fatalf("ExtractJSONText(0) = %q; want go", tag0)
	}
	tag1, ok := storage.ExtractJSONText(tagDoc, 1)
	if !ok || tag1 != "db" {
		t.Fatalf("ExtractJSONText(1) = %q; want db", tag1)
	}
}

func TestJSON_PathFunctions(t *testing.T) {
	doc := `{"user": {"name": "Alice", "scores": [95, 100]}, "active": true}`

	// JSONValueByPath - scalar returns string representation
	val, ok := storage.JSONValueByPath(doc, "$.user.name")
	if !ok || val != "Alice" {
		t.Fatalf("JSONValueByPath($.user.name) = %q, %v; want Alice", val, ok)
	}

	score0, ok := storage.JSONValueByPath(doc, "$.user.scores[0]")
	if !ok || fmt.Sprintf("%v", score0) != "95" {
		t.Fatalf("JSONValueByPath($.user.scores[0]) = %v, %v; want 95", score0, ok)
	}

	// Complex target returns false in JSON_VALUE
	_, ok = storage.JSONValueByPath(doc, "$.user")
	if ok {
		t.Fatalf("JSONValueByPath($.user) should fail for object")
	}

	// JSONQueryByPath - object returns formatted JSON
	qObj, ok := storage.JSONQueryByPath(doc, "$.user")
	if !ok || qObj == "" {
		t.Fatalf("JSONQueryByPath($.user) = %q, %v", qObj, ok)
	}

	// Scalar target returns false in JSON_QUERY
	_, ok = storage.JSONQueryByPath(doc, "$.user.name")
	if ok {
		t.Fatalf("JSONQueryByPath($.user.name) should fail for scalar")
	}
}

func TestJSON_ScanJSONTopLevelKey(t *testing.T) {
	doc := `{"city": "Seoul", "age": 28, "active": true, "score": 98.5, "extra": null, "nested": {"k": "v"}}`

	// 1. Unquoted string extraction
	val, ok := storage.ScanJSONTopLevelKey(doc, "city", true)
	if !ok || val != "Seoul" {
		t.Fatalf("expected Seoul, got %q, %v", val, ok)
	}

	// 2. Quoted string extraction
	val, ok = storage.ScanJSONTopLevelKey(doc, "city", false)
	if !ok || val != `"Seoul"` {
		t.Fatalf("expected \"Seoul\", got %q, %v", val, ok)
	}

	// 3. Integer extraction
	val, ok = storage.ScanJSONTopLevelKey(doc, "age", true)
	if !ok || val != "28" {
		t.Fatalf("expected 28, got %q, %v", val, ok)
	}

	// 4. Boolean extraction
	val, ok = storage.ScanJSONTopLevelKey(doc, "active", true)
	if !ok || val != "true" {
		t.Fatalf("expected true, got %q, %v", val, ok)
	}

	// 5. Float extraction
	val, ok = storage.ScanJSONTopLevelKey(doc, "score", true)
	if !ok || val != "98.5" {
		t.Fatalf("expected 98.5, got %q, %v", val, ok)
	}

	// 6. Null extraction
	val, ok = storage.ScanJSONTopLevelKey(doc, "extra", true)
	if !ok || val != "null" {
		t.Fatalf("expected null, got %q, %v", val, ok)
	}

	// 7. Non-existent key
	_, ok = storage.ScanJSONTopLevelKey(doc, "missing", true)
	if ok {
		t.Fatalf("expected false for missing key")
	}
}

