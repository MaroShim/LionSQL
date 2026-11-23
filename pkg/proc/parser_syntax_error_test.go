package proc

import (
	"testing"
)

func TestParser_FailClosedOnMalformedClauses(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{
			name: "DELETE with malformed WHERE expression",
			sql:  "DELETE FROM t WHERE id = %%%;",
		},
		{
			name: "DELETE with empty WHERE clause",
			sql:  "DELETE FROM t WHERE;",
		},
		{
			name: "UPDATE with malformed WHERE expression",
			sql:  "UPDATE t SET col = 1 WHERE id = %%%;",
		},
		{
			name: "UPDATE with empty WHERE clause",
			sql:  "UPDATE t SET col = 1 WHERE;",
		},
		{
			name: "UPDATE with empty SET expression",
			sql:  "UPDATE t SET col = WHERE id = 1;",
		},
		{
			name: "SELECT with malformed WHERE expression",
			sql:  "SELECT * FROM t WHERE id = %%%;",
		},
		{
			name: "SELECT with empty WHERE clause",
			sql:  "SELECT * FROM t WHERE;",
		},
		{
			name: "SELECT with empty JOIN ON clause",
			sql:  "SELECT * FROM t JOIN u ON ;",
		},
		{
			name: "SELECT with malformed JOIN ON expression",
			sql:  "SELECT * FROM t JOIN u ON %%%;",
		},
		{
			name: "SELECT with empty HAVING clause",
			sql:  "SELECT col FROM t GROUP BY col HAVING ;",
		},
		{
			name: "SELECT with malformed HAVING expression",
			sql:  "SELECT col FROM t GROUP BY col HAVING %%%;",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileStatement(tc.sql)
			if err == nil {
				t.Fatalf("expected syntax error for query %q, but got nil error", tc.sql)
			}
		})
	}
}
