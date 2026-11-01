package wire_test

import (
	"testing"

	"lionsql/pkg/wire"
)

func TestFormatPgCommandComplete_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		rowsAffected int64
		rowsCount    int
		want         string
	}{
		{"INSERT command tag", "INSERT INTO t VALUES (1)", 1, 0, "INSERT 0 1"},
		{"INSERT zero rows", "INSERT INTO t VALUES (1)", 0, 0, "INSERT 0 0"},
		{"DELETE command tag", "DELETE FROM t WHERE id = 1", 3, 0, "DELETE 3"},
		{"UPDATE command tag", "UPDATE t SET v = 1", 5, 0, "UPDATE 5"},
		{"SELECT command tag with count", "SELECT * FROM t", 0, 10, "SELECT 10"},
		{"SELECT command tag with rowsAffected fallback", "SELECT * FROM t", 7, 0, "SELECT 7"},
		{"MOVE command tag", "MOVE 10 FROM c", 10, 0, "MOVE 10"},
		{"FETCH command tag", "FETCH 5 FROM c", 5, 0, "FETCH 5"},
		{"COPY command tag", "COPY t TO STDOUT", 12, 0, "COPY 12"},
		{"BEGIN transaction tag", "BEGIN TRANSACTION", 0, 0, "BEGIN"},
		{"START transaction tag", "START TRANSACTION", 0, 0, "BEGIN"},
		{"COMMIT transaction tag", "COMMIT", 0, 0, "COMMIT"},
		{"ROLLBACK transaction tag", "ROLLBACK", 0, 0, "ROLLBACK"},
		{"SET command tag", "SET TIME ZONE 'UTC'", 0, 0, "SET"},
		{"SHOW command tag", "SHOW server_version", 0, 0, "SHOW"},
		{"CREATE TABLE command tag", "CREATE TABLE my_table (id INT)", 0, 0, "CREATE TABLE"},
		{"DROP TABLE command tag", "DROP TABLE my_table", 0, 0, "DROP TABLE"},
		{"ALTER TABLE command tag", "ALTER TABLE my_table ADD c INT", 0, 0, "ALTER TABLE"},
		{"DECLARE cursor tag", "DECLARE cur CURSOR FOR SELECT 1", 0, 0, "DECLARE CURSOR"},
		{"CLOSE cursor tag", "CLOSE cur", 0, 0, "CLOSE CURSOR"},
		{"LOCK table tag", "LOCK TABLE t", 0, 0, "LOCK TABLE"},
		{"Empty command defaults to OK", "   ", 0, 0, "OK"},
		{"Unknown custom verb passes verb as tag", "CUSTOM_VERB 123", 0, 0, "CUSTOM_VERB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wire.FormatPgCommandComplete(tt.command, tt.rowsAffected, tt.rowsCount)
			if got != tt.want {
				t.Errorf("FormatPgCommandComplete(%q, %d, %d) = %q; want %q", tt.command, tt.rowsAffected, tt.rowsCount, got, tt.want)
			}
		})
	}
}
