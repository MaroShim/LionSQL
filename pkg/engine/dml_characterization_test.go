package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// dmlCase is one characterization case for the INSERT/UPDATE/DELETE statement
// recognizers. It pins the observable behavior (final table state, affected
// rows, error/no-error) that any replacement of the reDml* regexes must keep.
//
// Each case runs against a fresh table t(id INT PRIMARY KEY, name VARCHAR(50), qty INT)
// seeded with (1,'a',10) and (2,'b',20).
type dmlCase struct {
	name string
	sql  string
	// wantRows is the final "id|name|qty" content ordered by id.
	wantRows []string
	// wantAffected is the expected RowsAffected; -1 skips the check.
	wantAffected int64
	wantErr      bool
	// knownBug marks a case whose current behavior diverges from the SQL
	// standard. The case is tracked (not hidden): it must still fail while the
	// marker is set, and the marker must be removed once the bug is fixed.
	knownBug string
}

func runDMLCases(t *testing.T, cases []dmlCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			eng := memengine.New()
			defer eng.Close()
			coord := NewCoordinator(catalog.New(eng), eng, DefaultConfig())
			defer coord.Close()

			for _, setup := range []string{
				"CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(50), qty INT);",
				"INSERT INTO t (id, name, qty) VALUES (1, 'a', 10);",
				"INSERT INTO t (id, name, qty) VALUES (2, 'b', 20);",
			} {
				if _, err := coord.ExecuteSQL(setup); err != nil {
					t.Fatalf("setup %q failed: %v", setup, err)
				}
			}

			problem := checkDMLCase(coord, tc)
			switch {
			case tc.knownBug != "" && problem == "":
				t.Fatalf("known bug %q no longer reproduces: remove the knownBug marker", tc.knownBug)
			case tc.knownBug != "":
				t.Skipf("known bug %s: %s", tc.knownBug, problem)
			case problem != "":
				t.Fatal(problem)
			}
		})
	}
}

// checkDMLCase executes the case and returns a description of the first
// mismatch, or "" when the observed behavior matches the expectation.
func checkDMLCase(coord *Coordinator, tc dmlCase) string {
	res, err := coord.ExecuteSQL(tc.sql)
	if tc.wantErr {
		if err == nil {
			return fmt.Sprintf("expected error for %q, got none", tc.sql)
		}
		return ""
	}
	if err != nil {
		return fmt.Sprintf("unexpected error for %q: %v", tc.sql, err)
	}
	if tc.wantAffected >= 0 && res.RowsAffected != tc.wantAffected {
		return fmt.Sprintf("rows affected: want %d, got %d", tc.wantAffected, res.RowsAffected)
	}

	sel, err := coord.ExecuteSQL("SELECT id, name, qty FROM t ORDER BY id;")
	if err != nil {
		return fmt.Sprintf("verification select failed: %v", err)
	}
	got := make([]string, 0, len(sel.Rows))
	for _, r := range sel.Rows {
		got = append(got, fmt.Sprintf("%v|%v|%v", r["id"], r["name"], r["qty"]))
	}
	if strings.Join(got, ";") != strings.Join(tc.wantRows, ";") {
		return fmt.Sprintf("final rows: want %v, got %v", tc.wantRows, got)
	}
	return ""
}

var seedRows = []string{"1|a|10", "2|b|20"}

func withRows(extra ...string) []string {
	return append(append([]string{}, seedRows...), extra...)
}

func TestDMLCharacterization_Insert(t *testing.T) {
	runDMLCases(t, []dmlCase{
		{name: "plain", sql: "INSERT INTO t (id, name, qty) VALUES (3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: 1},
		{name: "no_into", sql: "INSERT t (id, name, qty) VALUES (3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: 1},
		{name: "lowercase", sql: "insert into t (id, name, qty) values (3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: 1},
		{name: "tabs_and_newlines", sql: "INSERT\tINTO\nt\n(id,\tname,\nqty)\nVALUES\n(3,\t'c',\n30)",
			wantRows: withRows("3|c|30"), wantAffected: 1},
		{name: "no_column_list", sql: "INSERT INTO t VALUES (3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: 1},
		{name: "multi_row_values", sql: "INSERT INTO t (id, name, qty) VALUES (3, 'c', 30), (4, 'd', 40);",
			wantRows: withRows("3|c|30", "4|d|40"), wantAffected: 2},
		{name: "bracket_quoted_table", sql: "INSERT INTO [t] (id, name, qty) VALUES (3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: 1},
		{name: "double_quoted_table", sql: `INSERT INTO "t" (id, name, qty) VALUES (3, 'c', 30);`,
			wantRows: withRows("3|c|30"), wantAffected: 1},
		{name: "no_space_before_paren", sql: "INSERT INTO t(id, name, qty) VALUES(3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: 1},
		{name: "literal_with_parens_and_comma", sql: "INSERT INTO t (id, name, qty) VALUES (3, 'x),(y', 30);",
			wantRows: withRows("3|x),(y|30"), wantAffected: 1},
		{name: "literal_with_keyword_values", sql: "INSERT INTO t (id, name, qty) VALUES (3, 'a VALUES b', 30);",
			wantRows: withRows("3|a VALUES b|30"), wantAffected: 1},
		{name: "partial_columns", sql: "INSERT INTO t (id, name) VALUES (3, 'c');",
			wantRows: withRows("3|c|<nil>"), wantAffected: 1},

		// Regex-only path: OUTPUT / UPSERT / ON CONFLICT bypass the VDBE fast path.
		{name: "output_clause", sql: "INSERT INTO t (id, name, qty) OUTPUT INSERTED.id VALUES (3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: -1},
		{name: "upsert_into_new_row", sql: "UPSERT INTO t (id, name, qty) VALUES (3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: -1},
		{name: "upsert_without_into", sql: "UPSERT t (id, name, qty) VALUES (3, 'c', 30);",
			wantRows: withRows("3|c|30"), wantAffected: -1},
		{name: "upsert_existing_row", sql: "UPSERT INTO t (id, name, qty) VALUES (1, 'z', 99);",
			wantRows: []string{"1|z|99", "2|b|20"}, wantAffected: -1},
		{name: "upsert_tab_after_keyword", sql: "UPSERT\tINTO\tt (id, name, qty) VALUES (1, 'z', 99);",
			wantRows: []string{"1|z|99", "2|b|20"}, wantAffected: -1},
		{name: "on_conflict_do_nothing", sql: "INSERT INTO t (id, name, qty) VALUES (1, 'z', 99) ON CONFLICT (id) DO NOTHING;",
			wantRows: seedRows, wantAffected: -1},
		{name: "on_conflict_do_update", sql: "INSERT INTO t (id, name, qty) VALUES (1, 'z', 99) ON CONFLICT (id) DO UPDATE SET name = 'z';",
			wantRows: []string{"1|z|10", "2|b|20"}, wantAffected: -1},

		{name: "insert_select", sql: "INSERT INTO t (id, name, qty) SELECT id + 10, name, qty FROM t;",
			wantRows: withRows("11|a|10", "12|b|20"), wantAffected: 2},
		{name: "insert_select_no_into", sql: "INSERT t (id, name, qty) SELECT id + 10, name, qty FROM t;",
			wantRows: withRows("11|a|10", "12|b|20"), wantAffected: 2},
		{name: "insert_select_parenthesized", sql: "INSERT INTO t (id, name, qty) (SELECT id + 10, name, qty FROM t);",
			wantRows: withRows("11|a|10", "12|b|20"), wantAffected: 2},

		{name: "duplicate_pk_rejected", sql: "INSERT INTO t (id, name, qty) VALUES (1, 'z', 99);",
			wantErr: true},
		{name: "unknown_table_rejected", sql: "INSERT INTO missing (id) VALUES (1);",
			wantErr: true},
		{name: "missing_values_rejected", sql: "INSERT INTO t (id, name, qty);",
			wantErr: true},
	})
}

func TestDMLCharacterization_Update(t *testing.T) {
	runDMLCases(t, []dmlCase{
		{name: "with_where", sql: "UPDATE t SET qty = 11 WHERE id = 1;",
			wantRows: []string{"1|a|11", "2|b|20"}, wantAffected: 1},
		{name: "without_where", sql: "UPDATE t SET qty = 0;",
			wantRows: []string{"1|a|0", "2|b|0"}, wantAffected: 2},
		{name: "lowercase", sql: "update t set qty = 11 where id = 1;",
			wantRows: []string{"1|a|11", "2|b|20"}, wantAffected: 1},
		{name: "tabs_and_newlines", sql: "UPDATE\tt\nSET\nqty = 11\nWHERE\tid = 1",
			wantRows: []string{"1|a|11", "2|b|20"}, wantAffected: 1},
		{name: "multi_assignment", sql: "UPDATE t SET name = 'z', qty = 99 WHERE id = 2;",
			wantRows: []string{"1|a|10", "2|z|99"}, wantAffected: 1},
		{name: "arithmetic_assignment", sql: "UPDATE t SET qty = qty + 5 WHERE id = 1;",
			wantRows: []string{"1|a|15", "2|b|20"}, wantAffected: 1},
		{name: "bracket_quoted_table", sql: "UPDATE [t] SET qty = 11 WHERE id = 1;",
			wantRows: []string{"1|a|11", "2|b|20"}, wantAffected: 1},
		{name: "literal_with_comma", sql: "UPDATE t SET name = 'x, y' WHERE id = 1;",
			wantRows: []string{"1|x, y|10", "2|b|20"}, wantAffected: 1},
		{name: "where_in_list", sql: "UPDATE t SET qty = 0 WHERE id IN (1, 2);",
			wantRows: []string{"1|a|0", "2|b|0"}, wantAffected: 2},
		{name: "where_and_or", sql: "UPDATE t SET qty = 0 WHERE id = 1 AND name = 'a' OR id = 2;",
			wantRows: []string{"1|a|0", "2|b|0"}, wantAffected: 2},
		{name: "no_matching_row", sql: "UPDATE t SET qty = 0 WHERE id = 99;",
			wantRows: seedRows, wantAffected: 0},
		{name: "literal_with_keyword_where", sql: "UPDATE t SET name = 'a WHERE b' WHERE id = 1;",
			wantRows: []string{"1|a WHERE b|10", "2|b|20"}, wantAffected: 1},

		// Regex-only path: OUTPUT bypasses the VDBE fast path.
		{name: "output_clause", sql: "UPDATE t SET qty = 11 OUTPUT INSERTED.qty WHERE id = 1;",
			wantRows: []string{"1|a|11", "2|b|20"}, wantAffected: -1},
		{name: "output_clause_without_where", sql: "UPDATE t SET qty = 0 OUTPUT INSERTED.qty;",
			wantRows: []string{"1|a|0", "2|b|0"}, wantAffected: -1},
		{name: "output_literal_with_keyword_where", sql: "UPDATE t SET name = 'a WHERE b' OUTPUT INSERTED.id WHERE id = 1;",
			wantRows: []string{"1|a WHERE b|10", "2|b|20"}, wantAffected: -1,
			knownBug: "reDmlUpdate lazily splits SET at the first \\s+WHERE\\s+ even inside a string literal"},

		{name: "unknown_table_rejected", sql: "UPDATE missing SET qty = 1;",
			wantErr: true},
		{name: "missing_set_rejected", sql: "UPDATE t WHERE id = 1;",
			wantErr: true},
	})
}

func TestDMLCharacterization_Delete(t *testing.T) {
	runDMLCases(t, []dmlCase{
		{name: "with_where", sql: "DELETE FROM t WHERE id = 1;",
			wantRows: []string{"2|b|20"}, wantAffected: 1},
		{name: "without_from", sql: "DELETE t WHERE id = 1;",
			wantRows: []string{"2|b|20"}, wantAffected: 1},
		{name: "without_where", sql: "DELETE FROM t;",
			wantRows: []string{}, wantAffected: 2},
		{name: "lowercase", sql: "delete from t where id = 1;",
			wantRows: []string{"2|b|20"}, wantAffected: 1},
		{name: "tabs_and_newlines", sql: "DELETE\tFROM\nt\nWHERE\tid = 1",
			wantRows: []string{"2|b|20"}, wantAffected: 1},
		{name: "bracket_quoted_table", sql: "DELETE FROM [t] WHERE id = 1;",
			wantRows: []string{"2|b|20"}, wantAffected: 1},
		{name: "where_in_list", sql: "DELETE FROM t WHERE id IN (1, 2);",
			wantRows: []string{}, wantAffected: 2},
		{name: "where_literal_with_keyword", sql: "DELETE FROM t WHERE name = 'a OUTPUT b';",
			wantRows: seedRows, wantAffected: 0},
		{name: "no_matching_row", sql: "DELETE FROM t WHERE id = 99;",
			wantRows: seedRows, wantAffected: 0},

		// Regex-only path: OUTPUT bypasses the VDBE fast path.
		{name: "output_clause", sql: "DELETE FROM t OUTPUT DELETED.id WHERE id = 1;",
			wantRows: []string{"2|b|20"}, wantAffected: -1},
		{name: "output_clause_without_from", sql: "DELETE t OUTPUT DELETED.id WHERE id = 1;",
			wantRows: []string{"2|b|20"}, wantAffected: -1},
		{name: "output_clause_without_where", sql: "DELETE FROM t OUTPUT DELETED.id;",
			wantRows: []string{}, wantAffected: -1},

		{name: "unknown_table_rejected", sql: "DELETE FROM missing;",
			wantErr: true},
	})
}
