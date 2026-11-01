package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestWoodpeckerCI_XORMMigrationQueries(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Table introspection via pg_tables
	res, err := coord.ExecuteSQL(`SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename = 'migration'`)
	if err != nil {
		t.Fatalf("introspection query failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows before creation, got %d", len(res.Rows))
	}

	// 2. CREATE TABLE IF NOT EXISTS
	_, err = coord.ExecuteSQL(`CREATE TABLE IF NOT EXISTS "public"."migration" ("id" VARCHAR(255) NULL, "description" VARCHAR(255) NULL);`)
	if err != nil {
		t.Fatalf("CREATE TABLE migration failed: %v", err)
	}

	// 3. Introspection should find table
	res, err = coord.ExecuteSQL(`SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename = 'migration'`)
	if err != nil {
		t.Fatalf("introspection after creation failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row after creation, got %d", len(res.Rows))
	}

	// 4. Check migration existence
	res, err = coord.ExecuteSQL(`SELECT count(*) FROM "migration" WHERE "id" IN ('SCHEMA_INIT')`)
	if err != nil {
		t.Fatalf("SELECT count(*) failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 count row, got %d", len(res.Rows))
	}

	// 5. INSERT migration record
	_, err = coord.ExecuteSQL(`INSERT INTO "migration" ("id","description") VALUES ('SCHEMA_INIT', 'initial schema')`)
	if err != nil {
		t.Fatalf("INSERT migration failed: %v", err)
	}

	// 6. Verify count is 1
	res, err = coord.ExecuteSQL(`SELECT count(*) FROM "migration" WHERE "id" IN ('SCHEMA_INIT')`)
	if err != nil {
		t.Fatalf("SELECT count(*) after insert failed: %v", err)
	}
	row := res.Rows[0]
	for _, v := range row {
		if v != int64(1) {
			t.Errorf("expected count=1, got %v (%T)", v, v)
		}
	}
}
