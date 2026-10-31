package proc_test

import (
	"reflect"
	"testing"

	"lionsql/pkg/proc"
)

func TestExtractQueryCharacteristics(t *testing.T) {
	tests := []struct {
		name          string
		sql           string
		wantType      string
		wantTables    []string
		wantReturning bool
		wantOutput    bool
		wantConflict  bool
	}{
		{
			name:          "simple select",
			sql:           "SELECT * FROM users WHERE id = 1",
			wantType:      "SELECT",
			wantTables:    []string{"users"},
			wantReturning: false,
			wantOutput:    false,
			wantConflict:  false,
		},
		{
			name:          "bracketed table with schema",
			sql:           "SELECT * FROM [sys].[tables] WHERE name = 'foo'",
			wantType:      "SELECT",
			wantTables:    []string{"sys.tables"},
			wantReturning: false,
			wantOutput:    false,
			wantConflict:  false,
		},
		{
			name:          "parenthesized join in FROM",
			sql:           `SELECT * FROM ("org_policies" INNER JOIN "users" ON org_policies.user_id = users.id)`,
			wantType:      "SELECT",
			wantTables:    []string{"org_policies", "users"},
			wantReturning: false,
			wantOutput:    false,
			wantConflict:  false,
		},
		{
			name:          "literal with RETURNING and ON CONFLICT inside quotes",
			sql:           "SELECT * FROM logs WHERE message = 'ON CONFLICT DO UPDATE' AND status = 'RETURNING'",
			wantType:      "SELECT",
			wantTables:    []string{"logs"},
			wantReturning: false,
			wantOutput:    false,
			wantConflict:  false,
		},
		{
			name:          "comment containing keywords",
			sql:           "SELECT id FROM users /* ON CONFLICT RETURNING OUTPUT */ WHERE id = 5",
			wantType:      "SELECT",
			wantTables:    []string{"users"},
			wantReturning: false,
			wantOutput:    false,
			wantConflict:  false,
		},
		{
			name:          "insert with returning",
			sql:           `INSERT INTO ciphers (name, user_id) VALUES ('item', 1) RETURNING "id", "created_at"`,
			wantType:      "INSERT",
			wantTables:    []string{"ciphers"},
			wantReturning: true,
			wantOutput:    false,
			wantConflict:  false,
		},
		{
			name:          "insert with on conflict",
			sql:           `INSERT INTO devices (uuid, user_uuid) VALUES ('u1', 'u2') ON CONFLICT (uuid, user_uuid) DO UPDATE SET name = EXCLUDED.name`,
			wantType:      "INSERT",
			wantTables:    []string{"devices"},
			wantReturning: false,
			wantOutput:    false,
			wantConflict:  true,
		},
		{
			name:          "insert with output",
			sql:           `INSERT INTO users (name) OUTPUT INSERTED.id VALUES ('alice')`,
			wantType:      "INSERT",
			wantTables:    []string{"users"},
			wantReturning: false,
			wantOutput:    true,
			wantConflict:  false,
		},
		{
			name:          "update statement",
			sql:           `UPDATE "accounts" SET balance = balance + 100 WHERE id = 5`,
			wantType:      "UPDATE",
			wantTables:    []string{"accounts"},
			wantReturning: false,
			wantOutput:    false,
			wantConflict:  false,
		},
		{
			name:          "delete statement",
			sql:           `DELETE FROM "sessions" WHERE expired = 1`,
			wantType:      "DELETE",
			wantTables:    []string{"sessions"},
			wantReturning: false,
			wantOutput:    false,
			wantConflict:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qc := proc.ExtractQueryCharacteristics(tt.sql)
			if qc.StatementType != tt.wantType {
				t.Errorf("StatementType = %q, want %q", qc.StatementType, tt.wantType)
			}
			if !reflect.DeepEqual(qc.TargetTables, tt.wantTables) {
				t.Errorf("TargetTables = %v, want %v", qc.TargetTables, tt.wantTables)
			}
			if qc.HasReturning != tt.wantReturning {
				t.Errorf("HasReturning = %v, want %v", qc.HasReturning, tt.wantReturning)
			}
			if qc.HasOutput != tt.wantOutput {
				t.Errorf("HasOutput = %v, want %v", qc.HasOutput, tt.wantOutput)
			}
			if qc.HasOnConflict != tt.wantConflict {
				t.Errorf("HasOnConflict = %v, want %v", qc.HasOnConflict, tt.wantConflict)
			}
		})
	}
}

func BenchmarkExtractQueryCharacteristics_SimpleSelect(b *testing.B) {
	sql := "SELECT id, name, balance FROM users WHERE id = 42"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = proc.ExtractQueryCharacteristics(sql)
	}
}

func BenchmarkExtractQueryCharacteristics_ComplexJoin(b *testing.B) {
	sql := `SELECT u.name, c.name FROM ("users" u INNER JOIN "ciphers" c ON u.id = c.user_id) WHERE u.id = 1`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = proc.ExtractQueryCharacteristics(sql)
	}
}
