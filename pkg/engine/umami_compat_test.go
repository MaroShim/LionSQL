package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/engine"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage/memengine"
)

func TestUmami_PrismaCanConnectQuery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Prisma schema-engine can-connect-to-database query
	q := "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname = 'public'), version(), current_setting('server_version_num')::integer as numeric_version;"
	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	t.Logf("Result row: %+v", row)

	if exists, ok := row["exists"]; !ok || exists != true {
		t.Errorf("expected exists=true, got %v", row["exists"])
	}
	if ver, ok := row["version"]; !ok || ver == nil {
		t.Errorf("expected version to be populated, got %v", row["version"])
	}
	if numVer, ok := row["numeric_version"]; !ok || numVer != int64(150000) {
		t.Errorf("expected numeric_version=150000 (int64), got %v (%T)", row["numeric_version"], row["numeric_version"])
	}
}

func TestUmami_PrismaTableNamespaceQuery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE _prisma_migrations (id VARCHAR(36) PRIMARY KEY);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	q := `SELECT tbl.relname AS table_name, namespace.nspname AS table_namespace
FROM pg_class AS tbl
INNER JOIN pg_namespace AS namespace ON namespace.oid = tbl.relnamespace
WHERE tbl.relkind = 'r' AND namespace.nspname = ANY ( '{"public"}' );`

	whereStr := `tbl.relkind = 'r' AND namespace.nspname = ANY ( '{"public"}' )`
	p := proc.NewParser(proc.NewLexer(whereStr))
	expr := p.ParseExpr()
	t.Logf("expr: %v, errors: %v", expr, p.Errors())
	rec := map[string]any{"relkind": "r", "nspname": "public"}
	t.Logf("proc.EvalBoolExpr: %v", proc.EvalBoolExpr(expr, rec, nil))

	t.Logf("IsVirtualCatalogJoinQuery: %v", virtual.IsVirtualCatalogJoinQuery(q))

	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}
	t.Logf("Rows count: %d, rows: %+v", len(res.Rows), res.Rows)
	if len(res.Rows) == 0 {
		t.Fatalf("expected at least 1 row for _prisma_migrations, got 0")
	}
	found := false
	for _, r := range res.Rows {
		if r["table_name"] == "_prisma_migrations" {
			found = true
			if r["table_namespace"] != "public" {
				t.Errorf("expected table_namespace=public, got %v", r["table_namespace"])
			}
		}
	}
	if !found {
		t.Errorf("_prisma_migrations not found in rows")
	}
}

func TestUmami_CountDerivedTableQuery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL(`CREATE TABLE website (
		website_id VARCHAR(36) PRIMARY KEY,
		name VARCHAR(100),
		domain VARCHAR(100),
		user_id VARCHAR(36),
		deleted_at VARCHAR(50)
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO website (website_id, name, domain, user_id, deleted_at) VALUES ('w1', 'Test', 'test.com', 'u1', NULL);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	q := `SELECT COUNT(*) AS "_count$_all" FROM (SELECT "public"."website"."website_id" FROM "public"."website" WHERE ("public"."website"."user_id" = 'u1' AND "public"."website"."deleted_at" IS NULL) OFFSET 0) AS "sub"`
	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}
	t.Logf("Columns: %+v, Rows: %+v", res.Columns, res.Rows)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	val, ok := row["_count$_all"]
	if !ok {
		t.Fatalf("missing _count$_all in row: %+v", row)
	}
	t.Logf("val: %v (%T)", val, val)
	if val != int64(1) {
		t.Errorf("expected _count$_all to be int64(1), got %v (%T)", val, val)
	}
}

