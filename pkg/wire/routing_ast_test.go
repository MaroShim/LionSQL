package wire

import (
	"testing"

	"lionsql/pkg/proc"
)

func TestRoutingAST_VirtualCatalogAndVDBECompilation(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantVirtual bool
		wantVDBE    bool
	}{
		{
			name:        "standard user table select",
			query:       "SELECT * FROM users WHERE id = 1",
			wantVirtual: false,
			wantVDBE:    true,
		},
		{
			name:        "user table with sys prefix",
			query:       "SELECT * FROM sys_configs WHERE key = 'version'",
			wantVirtual: false,
			wantVDBE:    true,
		},
		{
			name:        "user table with pg prefix",
			query:       "SELECT * FROM pg_user_audit_logs WHERE user_id = 10",
			wantVirtual: false,
			wantVDBE:    true,
		},
		{
			name:        "string literal containing ON CONFLICT and RETURNING",
			query:       "SELECT * FROM logs WHERE message = 'ON CONFLICT DO UPDATE' AND tag = 'RETURNING'",
			wantVirtual: false,
			wantVDBE:    true,
		},
		{
			name:        "comment containing OUTPUT and ON CONFLICT",
			query:       "SELECT id FROM users /* OUTPUT INSERTED.id ON CONFLICT DO UPDATE */ WHERE id = 42",
			wantVirtual: false,
			wantVDBE:    true,
		},
		{
			name:        "insert with true RETURNING clause",
			query:       `INSERT INTO ciphers (name, user_id) VALUES ('item', 1) RETURNING "id"`,
			wantVirtual: false,
			wantVDBE:    false, // must route to sqlexec for returning rows
		},
		{
			name:        "insert with true ON CONFLICT clause",
			query:       `INSERT INTO devices (uuid, user_uuid) VALUES ('u1', 'u2') ON CONFLICT (uuid, user_uuid) DO UPDATE SET name = EXCLUDED.name`,
			wantVirtual: false,
			wantVDBE:    false, // must route to sqlexec for upsert
		},
		{
			name:        "insert with true OUTPUT clause",
			query:       `INSERT INTO users (name) OUTPUT INSERTED.id VALUES ('bob')`,
			wantVirtual: false,
			wantVDBE:    false, // must route to sqlexec for output rows
		},
		{
			name:        "virtual catalog sys.tables",
			query:       "SELECT * FROM sys.tables",
			wantVirtual: true,
			wantVDBE:    false, // must route to virtual catalog
		},
		{
			name:        "virtual catalog bracketed [sys].[columns]",
			query:       "SELECT * FROM [sys].[columns] WHERE object_id = 1",
			wantVirtual: true,
			wantVDBE:    false,
		},
		{
			name:        "virtual catalog information_schema.tables",
			query:       "SELECT table_name FROM information_schema.tables",
			wantVirtual: true,
			wantVDBE:    false,
		},
		{
			name:        "virtual catalog pg_catalog.pg_class",
			query:       "SELECT relname FROM pg_catalog.pg_class",
			wantVirtual: true,
			wantVDBE:    false,
		},
		{
			name:        "virtual catalog bare pg_tables",
			query:       "SELECT tablename FROM pg_tables WHERE schemaname = 'public'",
			wantVirtual: true,
			wantVDBE:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qc := proc.ExtractQueryCharacteristics(tt.query)
			isVirt := isVirtualCatalogQuery(tt.query, qc)
			if isVirt != tt.wantVirtual {
				t.Errorf("isVirtualCatalogQuery() = %v, want %v for query: %s", isVirt, tt.wantVirtual, tt.query)
			}
			canVDBE := canCompileToVDBE(tt.query, qc)
			if canVDBE != tt.wantVDBE {
				t.Errorf("canCompileToVDBE() = %v, want %v for query: %s", canVDBE, tt.wantVDBE, tt.query)
			}
		})
	}
}
