package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestPgCompat_SerialAndReturningAndPgIndexes(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. CREATE TABLE with BIGSERIAL PRIMARY KEY
	createSQL := `CREATE TABLE IF NOT EXISTS "public"."version" ("id" BIGSERIAL PRIMARY KEY NOT NULL, "version" BIGINT NULL);`
	if _, err := coord.ExecuteSQL(createSQL); err != nil {
		t.Fatalf("CREATE TABLE with BIGSERIAL failed: %v", err)
	}

	// 2. INSERT INTO with RETURNING "id"
	insertSQL := `INSERT INTO "public"."version" ("version") VALUES (343) RETURNING "id";`
	res, err := coord.ExecuteSQL(insertSQL)
	if err != nil {
		t.Fatalf("INSERT with RETURNING failed: %v", err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "id" {
		t.Fatalf("expected returning column ['id'], got %v", res.Columns)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 returning row, got %d", len(res.Rows))
	}
	idVal, ok := res.Rows[0]["id"]
	if !ok || idVal != int64(1) {
		t.Fatalf("expected returning id 1, got %v (%T)", idVal, idVal)
	}

	// 3. Second INSERT with RETURNING "id" to verify auto-increment sequence advances
	insertSQL2 := `INSERT INTO "public"."version" ("version") VALUES (344) RETURNING "id";`
	res2, err := coord.ExecuteSQL(insertSQL2)
	if err != nil {
		t.Fatalf("second INSERT with RETURNING failed: %v", err)
	}
	idVal2, ok := res2.Rows[0]["id"]
	if !ok || idVal2 != int64(2) {
		t.Fatalf("expected returning id 2, got %v (%T)", idVal2, idVal2)
	}

	// 4. Query pg_indexes virtual catalog
	idxSQL := `SELECT indexname, indexdef FROM pg_indexes WHERE tablename='version' AND schemaname='public';`
	resIdx, err := coord.ExecuteSQL(idxSQL)
	if err != nil {
		t.Fatalf("SELECT from pg_indexes failed: %v", err)
	}
	if len(resIdx.Rows) == 0 {
		t.Fatalf("expected at least 1 index row for version table in pg_indexes")
	}
	idxRow := resIdx.Rows[0]
	if idxRow["indexname"] != "version_pkey" {
		t.Fatalf("expected indexname version_pkey, got %v", idxRow["indexname"])
	}

	// 5. Query pg_attribute join query from Gitea / XORM
	attrSQL := `SELECT column_name, column_default, is_nullable, data_type, character_maximum_length, description,
    CASE WHEN p.contype = 'p' THEN true ELSE false END AS primarykey,
    CASE WHEN p.contype = 'u' THEN true ELSE false END AS uniquekey
FROM pg_attribute f
    JOIN pg_class c ON c.oid = f.attrelid JOIN pg_type t ON t.oid = f.atttypid
    LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = f.attnum
    LEFT JOIN pg_description de ON f.attrelid=de.objoid AND f.attnum=de.objsubid
    LEFT JOIN pg_namespace n ON n.oid = c.relnamespace
    LEFT JOIN pg_constraint p ON p.conrelid = c.oid AND f.attnum = ANY (p.conkey)
    LEFT JOIN pg_class AS g ON p.confrelid = g.oid
    LEFT JOIN INFORMATION_SCHEMA.COLUMNS s ON s.column_name=f.attname AND c.relname=s.table_name
WHERE n.nspname= s.table_schema AND c.relkind = 'r' AND c.relname = 'version' AND s.table_schema = 'public' AND f.attnum > 0 ORDER BY f.attnum;`
	resAttr, err := coord.ExecuteSQL(attrSQL)
	if err != nil {
		t.Fatalf("SELECT from pg_attribute join failed: %v", err)
	}
	t.Logf("resAttr rows count=%d: %+v", len(resAttr.Rows), resAttr.Rows)
	rClass, _ := coord.ExecuteSQL("SELECT relname, oid, relnamespace, relkind FROM pg_class WHERE relname = 'version';")
	t.Logf("pg_class: %+v", rClass.Rows)
	rNs, _ := coord.ExecuteSQL("SELECT nspname, oid FROM pg_namespace;")
	t.Logf("pg_namespace: %+v", rNs.Rows)
	rCols, _ := coord.ExecuteSQL("SELECT table_schema, table_name, column_name FROM INFORMATION_SCHEMA.COLUMNS WHERE table_name = 'version';")
	t.Logf("INFORMATION_SCHEMA.COLUMNS: %+v", rCols.Rows)
	qA, errA := coord.ExecuteSQL("SELECT f.attname FROM pg_attribute f JOIN pg_class c ON c.oid = f.attrelid;")
	t.Logf("qA err=%v count=%d", errA, len(qA.Rows))

	qB, errB := coord.ExecuteSQL("SELECT f.attname FROM pg_attribute f JOIN pg_class c ON c.oid = f.attrelid JOIN pg_type t ON t.oid = f.atttypid;")
	t.Logf("qB err=%v count=%d", errB, len(qB.Rows))

	qC, errC := coord.ExecuteSQL("SELECT f.attname, n.nspname FROM pg_attribute f JOIN pg_class c ON c.oid = f.attrelid JOIN pg_type t ON t.oid = f.atttypid LEFT JOIN pg_namespace n ON n.oid = c.relnamespace;")
	t.Logf("qC err=%v count=%d", errC, len(qC.Rows))

	qD, errD := coord.ExecuteSQL("SELECT f.attname, s.table_schema FROM pg_attribute f JOIN pg_class c ON c.oid = f.attrelid JOIN pg_type t ON t.oid = f.atttypid LEFT JOIN pg_namespace n ON n.oid = c.relnamespace LEFT JOIN INFORMATION_SCHEMA.COLUMNS s ON s.column_name=f.attname AND c.relname=s.table_name;")
	t.Logf("qD err=%v count=%d", errD, len(qD.Rows))

	j1, _ := coord.ExecuteSQL(`SELECT f.attname FROM pg_attribute f JOIN pg_class c ON c.oid = f.attrelid LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = f.attnum;`)
	t.Logf("j1 (pg_attrdef) count=%d", len(j1.Rows))

	j2, _ := coord.ExecuteSQL(`SELECT f.attname FROM pg_attribute f JOIN pg_class c ON c.oid = f.attrelid LEFT JOIN pg_description de ON f.attrelid=de.objoid AND f.attnum=de.objsubid;`)
	t.Logf("j2 (pg_description) count=%d", len(j2.Rows))

	j3, _ := coord.ExecuteSQL(`SELECT f.attname FROM pg_attribute f JOIN pg_class c ON c.oid = f.attrelid LEFT JOIN pg_constraint p ON p.conrelid = c.oid AND f.attnum = ANY (p.conkey);`)
	t.Logf("j3 (pg_constraint) count=%d", len(j3.Rows))

	j4, _ := coord.ExecuteSQL(`SELECT f.attname FROM pg_attribute f JOIN pg_class c ON c.oid = f.attrelid LEFT JOIN pg_constraint p ON p.conrelid = c.oid AND f.attnum = ANY (p.conkey) LEFT JOIN pg_class AS g ON p.confrelid = g.oid;`)
	t.Logf("j4 (pg_class g) count=%d", len(j4.Rows))

	qI, errI := coord.ExecuteSQL(`SELECT f.attname
FROM pg_attribute f
    JOIN pg_class c ON c.oid = f.attrelid JOIN pg_type t ON t.oid = f.atttypid
    LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = f.attnum
    LEFT JOIN pg_description de ON f.attrelid=de.objoid AND f.attnum=de.objsubid
    LEFT JOIN pg_namespace n ON n.oid = c.relnamespace
    LEFT JOIN pg_constraint p ON p.conrelid = c.oid AND f.attnum = ANY (p.conkey)
    LEFT JOIN pg_class AS g ON p.confrelid = g.oid
    LEFT JOIN INFORMATION_SCHEMA.COLUMNS s ON s.column_name=f.attname AND c.relname=s.table_name
WHERE n.nspname= s.table_schema AND c.relkind = 'r' AND c.relname = 'version' AND s.table_schema = 'public' AND f.attnum > 0;`)
	t.Logf("qI err=%v count=%d", errI, len(qI.Rows))
}

func TestPgCompat_QuotedIntegerInsert(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL(`CREATE TABLE "public"."org_test" (id BIGSERIAL PRIMARY KEY NOT NULL, name VARCHAR(255) NOT NULL, "type" INT NOT NULL, created_unix BIGINT NOT NULL);`); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Insert with quoted string literals for integer columns
	insertSQL := `INSERT INTO "public"."org_test" (name, "type", created_unix) VALUES ('my_org', '1', '1793321845') RETURNING id;`
	res, err := coord.ExecuteSQL(insertSQL)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["id"] != int64(1) {
		t.Fatalf("unexpected returning row: %v", res.Rows)
	}

	// Query row to verify values are correctly stored as integers
	selRes, err := coord.ExecuteSQL(`SELECT name, "type", created_unix FROM "public"."org_test" WHERE id = 1;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(selRes.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(selRes.Rows))
	}
	row := selRes.Rows[0]
	if row["type"] != int64(1) {
		t.Fatalf("expected type 1, got %v (%T)", row["type"], row["type"])
	}
	if row["created_unix"] != int64(1793321845) {
		t.Fatalf("expected created_unix 1793321845, got %v (%T)", row["created_unix"], row["created_unix"])
	}
}
