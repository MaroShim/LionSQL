package engine

import "testing"

func TestParseDMLCommand(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want DMLStatement
		err  bool
	}{
		{name: "insert_values", sql: "INSERT INTO [t] (a, b) VALUES (1, 'x VALUES y');",
			want: DMLStatement{kind: dmlInsert, Table: "[t]", Columns: "a, b", Values: "(1, 'x VALUES y')"}},
		{name: "upsert_no_into", sql: "upsert t(a) values(1)",
			want: DMLStatement{kind: dmlInsert, Upsert: true, Table: "t", Columns: "a", Values: "(1)"}},
		{name: "insert_select_paren_no_cols", sql: "INSERT t (SELECT a FROM u)",
			want: DMLStatement{kind: dmlInsert, Table: "t", Select: "(SELECT a FROM u)"}},
		{name: "insert_output", sql: "INSERT INTO t (a) OUTPUT INSERTED.a VALUES (1)",
			want: DMLStatement{kind: dmlInsert, Table: "t", Columns: "a", Output: "INSERTED.a", Values: "(1)"}},
		{name: "update_literal_where", sql: "UPDATE t SET n = 'a WHERE b' OUTPUT INSERTED.id WHERE id = 1",
			want: DMLStatement{kind: dmlUpdate, Table: "t", Set: "n = 'a WHERE b'", Output: "INSERTED.id", Where: "id = 1"}},
		{name: "update_subquery_where", sql: "UPDATE t SET n = (SELECT 1 FROM u WHERE u.id = 1) WHERE id = 2",
			want: DMLStatement{kind: dmlUpdate, Table: "t", Set: "n = (SELECT 1 FROM u WHERE u.id = 1)", Where: "id = 2"}},
		{name: "delete_literal_output", sql: "DELETE FROM t WHERE n = 'a OUTPUT b'",
			want: DMLStatement{kind: dmlDelete, Table: "t", Where: "n = 'a OUTPUT b'"}},
		{name: "delete_no_from_output", sql: "DELETE t OUTPUT DELETED.id WHERE id = 1",
			want: DMLStatement{kind: dmlDelete, Table: "t", Output: "DELETED.id", Where: "id = 1"}},
		{name: "update_missing_set", sql: "UPDATE t WHERE id = 1", err: true},
		{name: "delete_trailing_junk", sql: "DELETE FROM t junk", err: true},
		{name: "insert_no_source", sql: "INSERT INTO t (a)", err: true},
		{name: "not_dml", sql: "SELECT 1", err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDMLCommand(tc.sql)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *got != tc.want {
				t.Fatalf("got %+v, want %+v", *got, tc.want)
			}
		})
	}
}
