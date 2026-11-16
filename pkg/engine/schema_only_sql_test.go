package engine

import (
	"fmt"
	"testing"
)

func TestEngine_TSQL_DurabilitySchemaOnly_RestartZeroRows(t *testing.T) {
	tempDir := t.TempDir()

	// Phase 1: Create tables, insert data
	{
		coord, eng := openPersistentCompactCoord(t, tempDir)

		sqlCreateCache := `CREATE TABLE web_cache (
			id INT PRIMARY KEY,
			token VARCHAR(64)
		) WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_ONLY);`
		if _, err := coord.ExecuteSQL(sqlCreateCache); err != nil {
			t.Fatalf("CREATE TABLE web_cache failed: %v", err)
		}

		sqlCreateDurable := `CREATE TABLE app_users (
			id INT PRIMARY KEY,
			username VARCHAR(64)
		);`
		if _, err := coord.ExecuteSQL(sqlCreateDurable); err != nil {
			t.Fatalf("CREATE TABLE app_users failed: %v", err)
		}

		if _, err := coord.ExecuteSQL(`INSERT INTO web_cache VALUES (1, 'tok_1'), (2, 'tok_2');`); err != nil {
			t.Fatalf("INSERT web_cache failed: %v", err)
		}
		if _, err := coord.ExecuteSQL(`INSERT INTO app_users VALUES (1, 'alice'), (2, 'bob');`); err != nil {
			t.Fatalf("INSERT app_users failed: %v", err)
		}

		// Verify count before restart
		resCache, err := coord.ExecuteSQL(`SELECT COUNT(*) AS cnt FROM web_cache;`)
		if err != nil || len(resCache.Rows) == 0 || fmt.Sprintf("%v", resCache.Rows[0]["cnt"]) != "2" {
			t.Fatalf("expected 2 rows in web_cache before restart, got %v (err: %v)", resCache, err)
		}

		resUsers, err := coord.ExecuteSQL(`SELECT COUNT(*) AS cnt FROM app_users;`)
		if err != nil || len(resUsers.Rows) == 0 || fmt.Sprintf("%v", resUsers.Rows[0]["cnt"]) != "2" {
			t.Fatalf("expected 2 rows in app_users before restart, got %v (err: %v)", resUsers, err)
		}

		coord.Close()
		eng.Close()
	}

	// Phase 2: Reopen from disk, verify schema_only table has 0 rows and app_users has 2 rows
	{
		coord, eng := openPersistentCompactCoord(t, tempDir)
		defer coord.Close()
		defer eng.Close()

		resUsers, err := coord.ExecuteSQL(`SELECT COUNT(*) AS cnt FROM app_users;`)
		if err != nil || len(resUsers.Rows) == 0 || fmt.Sprintf("%v", resUsers.Rows[0]["cnt"]) != "2" {
			t.Fatalf("expected 2 rows in app_users after restart, got %v (err: %v)", resUsers, err)
		}

		resCache, err := coord.ExecuteSQL(`SELECT COUNT(*) AS cnt FROM web_cache;`)
		if err != nil || len(resCache.Rows) == 0 || fmt.Sprintf("%v", resCache.Rows[0]["cnt"]) != "0" {
			t.Fatalf("expected 0 rows in web_cache after restart (DURABILITY = SCHEMA_ONLY), got %v (err: %v)", resCache, err)
		}

		// Verify that web_cache schema is fully functional for new inserts after restart
		if _, err := coord.ExecuteSQL(`INSERT INTO web_cache VALUES (10, 'tok_new');`); err != nil {
			t.Fatalf("INSERT into web_cache after restart failed: %v", err)
		}

		resCacheAfter, err := coord.ExecuteSQL(`SELECT token FROM web_cache WHERE id = 10;`)
		if err != nil || len(resCacheAfter.Rows) == 0 || fmt.Sprintf("%v", resCacheAfter.Rows[0]["token"]) != "tok_new" {
			t.Fatalf("expected 'tok_new' from web_cache, got %v (err: %v)", resCacheAfter, err)
		}
	}
}

func TestEngine_TSQL_MemoryOptimized_PKRules(t *testing.T) {
	tempDir := t.TempDir()
	coord, eng := openPersistentCompactCoord(t, tempDir)
	defer coord.Close()
	defer eng.Close()

	// 1. SCHEMA_AND_DATA without PK must fail (Msg 41321)
	sqlNoPK_SchemaAndData := `CREATE TABLE fail_mem_data (
		msg VARCHAR(100)
	) WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_AND_DATA);`
	if _, err := coord.ExecuteSQL(sqlNoPK_SchemaAndData); err == nil {
		t.Fatalf("expected error creating memory-optimized table with SCHEMA_AND_DATA without PK, got nil")
	}

	// 2. Default DURABILITY (SCHEMA_AND_DATA) without PK must fail
	sqlNoPK_DefaultDurability := `CREATE TABLE fail_mem_default (
		msg VARCHAR(100)
	) WITH (MEMORY_OPTIMIZED = ON);`
	if _, err := coord.ExecuteSQL(sqlNoPK_DefaultDurability); err == nil {
		t.Fatalf("expected error creating memory-optimized table with default DURABILITY without PK, got nil")
	}

	// 3. SCHEMA_ONLY without PK must SUCCEED!
	sqlNoPK_SchemaOnly := `CREATE TABLE ok_mem_schema_only (
		msg VARCHAR(100)
	) WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_ONLY);`
	if _, err := coord.ExecuteSQL(sqlNoPK_SchemaOnly); err != nil {
		t.Fatalf("expected success creating memory-optimized table with SCHEMA_ONLY without PK, got err: %v", err)
	}

	// 4. Verify DML operations work on PK-less SCHEMA_ONLY table
	if _, err := coord.ExecuteSQL(`INSERT INTO ok_mem_schema_only VALUES ('hello schema only');`); err != nil {
		t.Fatalf("INSERT into ok_mem_schema_only failed: %v", err)
	}
	res, err := coord.ExecuteSQL(`SELECT msg FROM ok_mem_schema_only;`)
	if err != nil || len(res.Rows) == 0 || fmt.Sprintf("%v", res.Rows[0]["msg"]) != "hello schema only" {
		t.Fatalf("SELECT from ok_mem_schema_only failed: %v, err: %v", res, err)
	}

	// 5. SCHEMA_AND_DATA with PK must SUCCEED
	sqlWithPK_SchemaAndData := `CREATE TABLE ok_mem_data (
		id INT PRIMARY KEY,
		msg VARCHAR(100)
	) WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_AND_DATA);`
	if _, err := coord.ExecuteSQL(sqlWithPK_SchemaAndData); err != nil {
		t.Fatalf("expected success creating memory-optimized table with PK and SCHEMA_AND_DATA, got err: %v", err)
	}

	// 6. Normal disk/in-memory table without PK still succeeds as usual
	sqlNormalNoPK := `CREATE TABLE normal_no_pk (
		col1 INT,
		col2 VARCHAR(50)
	);`
	if _, err := coord.ExecuteSQL(sqlNormalNoPK); err != nil {
		t.Fatalf("expected success creating normal table without PK, got err: %v", err)
	}
}
