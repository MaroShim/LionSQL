package engine

import (
	"errors"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func openPersistentCompactCoord(t *testing.T, dir string) (*Coordinator, *memengine.Engine) {
	t.Helper()
	opts := memengine.DefaultOptions(dir)
	opts.Mode = memengine.MemoryCompact
	eng := memengine.NewWithOptions(opts)
	if err := eng.Open(dir); err != nil {
		t.Fatalf("eng.Open(%q) failed: %v", dir, err)
	}
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	return coord, eng
}

func TestCompactAudit_F1_SequentialSelectDoesNotCorruptPriorQueryResult(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL(`CREATE TABLE f1_users (id INT PRIMARY KEY, sso_name VARCHAR(32), full_name VARCHAR(128));`); err != nil {
		t.Fatalf("CREATE TABLE f1_users failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`CREATE TABLE f1_other (id INT PRIMARY KEY, sso_code VARCHAR(32), description VARCHAR(128));`); err != nil {
		t.Fatalf("CREATE TABLE f1_other failed: %v", err)
	}

	const (
		wantSSO  = "alice"
		wantFull = "alice_original_uncorrupted_name_0001"
	)
	if _, err := coord.ExecuteSQL(`INSERT INTO f1_users VALUES (1, 'alice', 'alice_original_uncorrupted_name_0001');`); err != nil {
		t.Fatalf("INSERT f1_users failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO f1_other VALUES (1, 'ZZZZZ', 'ZZZZZ_SECOND_QUERY_OVERWRITES_SLAB_9999');`); err != nil {
		t.Fatalf("INSERT f1_other failed: %v", err)
	}

	res1, err := coord.ExecuteSQL(`SELECT u.sso_name, u.full_name FROM f1_users u JOIN f1_users u2 ON u.id = u2.id;`)
	if err != nil || len(res1.Rows) != 1 || len(res1.Values) != 1 {
		t.Fatalf("SELECT 1 failed or missing rows/values: err=%v rows=%d values=%d", err, len(res1.Rows), len(res1.Values))
	}

	// Execute unrelated query reusing pooled Arena byte slabs after res1's PutArena.
	res2, err := coord.ExecuteSQL(`SELECT o.sso_code, o.description FROM f1_other o JOIN f1_other o2 ON o.id = o2.id;`)
	if err != nil || len(res2.Rows) != 1 || len(res2.Values) != 1 {
		t.Fatalf("SELECT 2 failed or missing rows/values: err=%v rows=%d values=%d", err, len(res2.Rows), len(res2.Values))
	}

	if got := res1.Rows[0]["u.sso_name"]; got != wantSSO {
		if got2 := res1.Rows[0]["sso_name"]; got2 != wantSSO {
			t.Fatalf("res1.Rows[0] sso_name corrupted after query 2: got (%v / %v), want %s", got, got2, wantSSO)
		}
	}
	if got := res1.Rows[0]["u.full_name"]; got != wantFull {
		if got2 := res1.Rows[0]["full_name"]; got2 != wantFull {
			t.Fatalf("res1.Rows[0] full_name corrupted after query 2: got (%v / %v), want %s", got, got2, wantFull)
		}
	}
	if got := res1.Values[0][0]; got != wantSSO {
		t.Fatalf("res1.Values[0][0] corrupted after query 2: got %v, want %s", got, wantSSO)
	}
	if got := res1.Values[0][1]; got != wantFull {
		t.Fatalf("res1.Values[0][1] corrupted after query 2: got %v, want %s", got, wantFull)
	}
}

func TestCompactAudit_F2_DropColumnMiddleAndReopen(t *testing.T) {
	dir := t.TempDir()
	coord, eng := openPersistentCompactCoord(t, dir)

	stmts := []string{
		`CREATE TABLE f2_items (id INT PRIMARY KEY, a INT, b VARCHAR(64), c VARCHAR(64));`,
		`CREATE INDEX idx_f2_b ON f2_items (b);`,
		`CREATE INDEX idx_f2_c ON f2_items (c);`,
		`INSERT INTO f2_items VALUES (1, 10, 'drop_me_1', 'keep_c_1');`,
		`INSERT INTO f2_items VALUES (2, 20, 'drop_me_2', 'keep_c_2');`,
	}
	for _, sql := range stmts {
		if _, err := coord.ExecuteSQL(sql); err != nil {
			t.Fatalf("setup failed (%s): %v", sql, err)
		}
	}

	if _, err := coord.ExecuteSQL(`ALTER TABLE f2_items DROP COLUMN b;`); err != nil {
		t.Fatalf("DROP COLUMN b failed: %v", err)
	}

	// Verify live schema cleaned up idx_f2_b while preserving idx_f2_c
	tbl, err := eng.GetTable("f2_items")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if _, _, hasBIdx := tbl.FindIndex("idx_f2_b"); hasBIdx {
		t.Fatalf("expected dependent secondary index idx_f2_b to be auto-dropped")
	}
	if _, _, hasCIdx := tbl.FindIndex("idx_f2_c"); !hasCIdx {
		t.Fatalf("expected unrelated secondary index idx_f2_c to remain")
	}

	res, err := coord.ExecuteSQL(`SELECT id, a, c FROM f2_items ORDER BY id;`)
	if err != nil {
		t.Fatalf("SELECT after DROP COLUMN failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["c"] != "keep_c_1" || res.Rows[1]["c"] != "keep_c_2" {
		t.Fatalf("unexpected row values after DROP COLUMN: %#v", res.Rows)
	}

	// Verify INSERT, UPDATE, and indexed lookup work on rebuilt layout
	if _, err := coord.ExecuteSQL(`INSERT INTO f2_items VALUES (3, 30, 'keep_c_3');`); err != nil {
		t.Fatalf("INSERT after DROP COLUMN failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`UPDATE f2_items SET c = 'updated_c_2' WHERE id = 2;`); err != nil {
		t.Fatalf("UPDATE after DROP COLUMN failed: %v", err)
	}
	resIdx, err := coord.ExecuteSQL(`SELECT id FROM f2_items WHERE c = 'updated_c_2';`)
	if err != nil || len(resIdx.Rows) != 1 || resIdx.Rows[0]["id"] != int64(2) {
		t.Fatalf("indexed SELECT after DROP COLUMN+UPDATE failed: err=%v rows=%#v", err, resIdx)
	}

	coord.Close()
	_ = eng.Close()

	// Reopen from WAL / snapshot and verify DROP COLUMN survived replay
	coord2, eng2 := openPersistentCompactCoord(t, dir)
	defer func() {
		coord2.Close()
		_ = eng2.Close()
	}()

	resReopen, err := coord2.ExecuteSQL(`SELECT id, a, c FROM f2_items ORDER BY id;`)
	if err != nil {
		t.Fatalf("SELECT after reopen failed: %v", err)
	}
	if len(resReopen.Rows) != 3 {
		t.Fatalf("expected 3 rows after reopen, got %d", len(resReopen.Rows))
	}
	if resReopen.Rows[0]["c"] != "keep_c_1" || resReopen.Rows[1]["c"] != "updated_c_2" || resReopen.Rows[2]["c"] != "keep_c_3" {
		t.Fatalf("unexpected row values after WAL reopen: %#v", resReopen.Rows)
	}
}

func TestCompactAudit_F2_DropColumnPKAndFKBlocked(t *testing.T) {
	coord, eng := openPersistentCompactCoord(t, t.TempDir())
	defer func() {
		coord.Close()
		_ = eng.Close()
	}()

	stmts := []string{
		`CREATE TABLE f2_parent (pid INT PRIMARY KEY, ref_code INT UNIQUE, pname VARCHAR(32));`,
		`CREATE TABLE f2_child (cid INT PRIMARY KEY, parent_id INT REFERENCES f2_parent(pid), note VARCHAR(32));`,
		`INSERT INTO f2_parent VALUES (1, 100, 'root');`,
		`INSERT INTO f2_child VALUES (10, 1, 'leaf');`,
	}
	for _, sql := range stmts {
		if _, err := coord.ExecuteSQL(sql); err != nil {
			t.Fatalf("setup failed (%s): %v", sql, err)
		}
	}

	// 1. Primary Key column drop must be rejected
	if _, err := coord.ExecuteSQL(`ALTER TABLE f2_parent DROP COLUMN pid;`); !errors.Is(err, storage.ErrDependentObjectsExist) {
		t.Fatalf("expected ErrDependentObjectsExist dropping PK column, got: %v", err)
	}

	// 2. Child FK column drop must be rejected
	if _, err := coord.ExecuteSQL(`ALTER TABLE f2_child DROP COLUMN parent_id;`); !errors.Is(err, storage.ErrDependentObjectsExist) {
		t.Fatalf("expected ErrDependentObjectsExist dropping child FK column, got: %v", err)
	}

	// 3. Parent column referenced by child FK (pid / unique) must be rejected
	// Create explicit non-PK parent unique column referenced by another child FK
	if _, err := coord.ExecuteSQL(`CREATE TABLE f2_child2 (cid INT PRIMARY KEY, ref INT REFERENCES f2_parent(ref_code));`); err != nil {
		t.Fatalf("CREATE f2_child2 failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`ALTER TABLE f2_parent DROP COLUMN ref_code;`); !errors.Is(err, storage.ErrDependentObjectsExist) {
		t.Fatalf("expected ErrDependentObjectsExist dropping parent FK-referenced column, got: %v", err)
	}
}

func TestCompactAudit_F2_AlterColumnTypeFailClosedAndReopen(t *testing.T) {
	dir := t.TempDir()
	coord, eng := openPersistentCompactCoord(t, dir)

	if _, err := coord.ExecuteSQL(`CREATE TABLE f2_types (id INT PRIMARY KEY, code VARCHAR(32), score INT);`); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO f2_types VALUES (1, '123', 45);`); err != nil {
		t.Fatalf("INSERT 1 failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO f2_types VALUES (2, 'abc', 90);`); err != nil {
		t.Fatalf("INSERT 2 failed: %v", err)
	}

	// Conversion failure ('abc' -> INT) must fail closed and preserve original data/schema
	if _, err := coord.ExecuteSQL(`ALTER TABLE f2_types ALTER COLUMN code TYPE INT;`); err == nil {
		t.Fatalf("expected ALTER COLUMN code TYPE INT to fail when row contains 'abc'")
	}

	resAfterFail, err := coord.ExecuteSQL(`SELECT id, code, score FROM f2_types ORDER BY id;`)
	if err != nil {
		t.Fatalf("SELECT after failed ALTER TYPE failed: %v", err)
	}
	if len(resAfterFail.Rows) != 2 || resAfterFail.Rows[0]["code"] != "123" || resAfterFail.Rows[1]["code"] != "abc" {
		t.Fatalf("expected original VARCHAR values preserved after rollback, got: %#v", resAfterFail.Rows)
	}

	// Remove non-numeric row and convert code VARCHAR -> INT, score INT -> VARCHAR
	if _, err := coord.ExecuteSQL(`DELETE FROM f2_types WHERE id = 2;`); err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`ALTER TABLE f2_types ALTER COLUMN code TYPE INT;`); err != nil {
		t.Fatalf("ALTER COLUMN code TYPE INT failed on numeric row: %v", err)
	}
	if _, err := coord.ExecuteSQL(`ALTER TABLE f2_types ALTER COLUMN score TYPE VARCHAR(32);`); err != nil {
		t.Fatalf("ALTER COLUMN score TYPE VARCHAR(32) failed: %v", err)
	}

	resConv, err := coord.ExecuteSQL(`SELECT id, code, score FROM f2_types WHERE code = 123;`)
	if err != nil {
		t.Fatalf("SELECT converted rows failed: %v", err)
	}
	if len(resConv.Rows) != 1 || resConv.Rows[0]["code"] != int64(123) || resConv.Rows[0]["score"] != "45" {
		t.Fatalf("expected converted types (int64(123), \"45\"), got: %#v", resConv.Rows)
	}

	coord.Close()
	_ = eng.Close()

	// Reopen from disk and confirm converted types persisted in WAL
	coord2, eng2 := openPersistentCompactCoord(t, dir)
	defer func() {
		coord2.Close()
		_ = eng2.Close()
	}()

	resReopen, err := coord2.ExecuteSQL(`SELECT id, code, score FROM f2_types WHERE code = 123;`)
	if err != nil {
		t.Fatalf("SELECT after reopen failed: %v", err)
	}
	if len(resReopen.Rows) != 1 || resReopen.Rows[0]["code"] != int64(123) || resReopen.Rows[0]["score"] != "45" {
		t.Fatalf("expected persisted converted types after reopen, got: %#v", resReopen.Rows)
	}
}

func TestCompactAudit_F2_RenameColumnPreservesDataAndIndex(t *testing.T) {
	dir := t.TempDir()
	coord, eng := openPersistentCompactCoord(t, dir)

	for _, sql := range []string{
		`CREATE TABLE f2_rename (id INT PRIMARY KEY, old_col VARCHAR(32), val INT);`,
		`CREATE INDEX idx_f2_rename ON f2_rename (old_col);`,
		`INSERT INTO f2_rename VALUES (1, 'alpha', 10), (2, 'beta', 20);`,
		`ALTER TABLE f2_rename RENAME COLUMN old_col TO new_col;`,
	} {
		if _, err := coord.ExecuteSQL(sql); err != nil {
			t.Fatalf("step failed (%s): %v", sql, err)
		}
	}

	res, err := coord.ExecuteSQL(`SELECT id, new_col, val FROM f2_rename WHERE new_col = 'beta';`)
	if err != nil {
		t.Fatalf("SELECT after RENAME COLUMN failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["new_col"] != "beta" || res.Rows[0]["id"] != int64(2) {
		t.Fatalf("unexpected rows after RENAME COLUMN: %#v", res.Rows)
	}

	coord.Close()
	_ = eng.Close()

	coord2, eng2 := openPersistentCompactCoord(t, dir)
	defer func() {
		coord2.Close()
		_ = eng2.Close()
	}()

	res2, err := coord2.ExecuteSQL(`SELECT id, new_col, val FROM f2_rename WHERE new_col = 'alpha';`)
	if err != nil {
		t.Fatalf("SELECT after reopen failed: %v", err)
	}
	if len(res2.Rows) != 1 || res2.Rows[0]["new_col"] != "alpha" {
		t.Fatalf("unexpected rows after RENAME COLUMN + reopen: %#v", res2.Rows)
	}
	tbl, err := eng2.GetTable("f2_rename")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	idx, _, ok := tbl.FindIndex("idx_f2_rename")
	if !ok || len(idx.Columns) != 1 || !strings.EqualFold(idx.Columns[0], "new_col") {
		t.Fatalf("expected index column updated to new_col after RENAME COLUMN, got ok=%v idx=%+v", ok, idx)
	}
}
