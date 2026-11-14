package proc

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestJSON_Object_Builtin(t *testing.T) {
	tests := []struct {
		name     string
		args     []storage.Value
		expected string
	}{
		{
			name: "basic key-value pairs",
			args: []storage.Value{
				storage.StringValue("name"),
				storage.StringValue("John"),
				storage.StringValue("age"),
				storage.IntValue(30),
			},
			expected: `{"name":"John","age":30}`,
		},
		{
			name: "absent on null default",
			args: []storage.Value{
				storage.StringValue("name"),
				storage.StringValue("John"),
				storage.StringValue("dept"),
				storage.NullValue(storage.TypeVarChar),
			},
			expected: `{"name":"John"}`,
		},
		{
			name: "null on null explicit option",
			args: []storage.Value{
				storage.StringValue("name"),
				storage.StringValue("John"),
				storage.StringValue("dept"),
				storage.NullValue(storage.TypeVarChar),
				storage.StringValue("__OPT:NULL"),
			},
			expected: `{"name":"John","dept":null}`,
		},
		{
			name: "nested json object preservation",
			args: []storage.Value{
				storage.StringValue("user"),
				storage.JSONValue(`{"first":"A","last":"B"}`),
			},
			expected: `{"user":{"first":"A","last":"B"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := evalJSONBuiltin("JSON_OBJECT", tc.args)
			if res.SVal != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, res.SVal)
			}
		})
	}
}

func TestJSON_Array_Builtin(t *testing.T) {
	tests := []struct {
		name     string
		args     []storage.Value
		expected string
	}{
		{
			name: "basic elements",
			args: []storage.Value{
				storage.StringValue("hello"),
				storage.IntValue(42),
				storage.BoolValue(true),
			},
			expected: `["hello",42,true]`,
		},
		{
			name: "absent on null default",
			args: []storage.Value{
				storage.StringValue("a"),
				storage.NullValue(storage.TypeVarChar),
				storage.StringValue("b"),
			},
			expected: `["a","b"]`,
		},
		{
			name: "null on null explicit option",
			args: []storage.Value{
				storage.StringValue("a"),
				storage.NullValue(storage.TypeVarChar),
				storage.StringValue("b"),
				storage.StringValue("__OPT:NULL"),
			},
			expected: `["a",null,"b"]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := evalJSONBuiltin("JSON_ARRAY", tc.args)
			if res.SVal != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, res.SVal)
			}
		})
	}
}

func TestJSON_Path_Exists(t *testing.T) {
	doc := `{"info":{"address":{"town":"Paris"}},"tags":["t1","t2"]}`

	// Existing path
	r1 := evalJSONBuiltin("JSON_PATH_EXISTS", []storage.Value{
		storage.StringValue(doc),
		storage.StringValue("$.info.address.town"),
	})
	if r1.IVal != 1 {
		t.Fatalf("expected 1, got %v", r1)
	}

	// Non-existing path
	r2 := evalJSONBuiltin("JSON_PATH_EXISTS", []storage.Value{
		storage.StringValue(doc),
		storage.StringValue("$.info.address.zipcode"),
	})
	if r2.IVal != 0 {
		t.Fatalf("expected 0, got %v", r2)
	}

	// Null document
	r3 := evalJSONBuiltin("JSON_PATH_EXISTS", []storage.Value{
		storage.NullValue(storage.TypeVarChar),
		storage.StringValue("$.a"),
	})
	if !r3.IsNull {
		t.Fatalf("expected NULL, got %v", r3)
	}

	// Invalid document returns 0
	r4 := evalJSONBuiltin("JSON_PATH_EXISTS", []storage.Value{
		storage.StringValue("invalid json doc"),
		storage.StringValue("$.a"),
	})
	if r4.IVal != 0 {
		t.Fatalf("expected 0, got %v", r4)
	}
}
