package engine

import (
	"context"
	"strings"
	"testing"
)

func TestTransactionalDDL_CreateTable_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-create-rollback"

	// 1. Begin transaction
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	// 2. Create table inside transaction
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_users (id INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE inside tx failed: %v", err)
	}

	// 3. Insert row inside transaction
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO tx_users (id, name) VALUES (1, 'Alice');"); err != nil {
		t.Fatalf("INSERT inside tx failed: %v", err)
	}

	// Verify row is queryable inside transaction
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, name FROM tx_users;")
	if err != nil {
		t.Fatalf("SELECT inside tx failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row inside tx, got %d", len(res.Rows))
	}

	// 4. Rollback transaction
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// 5. Verify table is completely gone after rollback
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_users;")
	if err == nil {
		t.Fatalf("expected error querying tx_users after rollback, but query succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("expected 'not found' error after rollback, got: %v", err)
	}
}

func TestTransactionalDDL_CreateTable_Commit(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-create-commit"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_committed (id INT PRIMARY KEY, val VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO tx_committed (id, val) VALUES (100, 'KeepMe');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "COMMIT;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, val FROM tx_committed;")
	if err != nil {
		t.Fatalf("SELECT after commit failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["val"] != "KeepMe" {
		t.Fatalf("unexpected rows after commit: %+v", res.Rows)
	}
}

func TestTransactionalDDL_DropTable_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-drop-rollback"

	// 1. Setup table outside transaction with initial data
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_to_drop (id INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("initial CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO tx_to_drop VALUES (1, 'Alice'), (2, 'Bob');"); err != nil {
		t.Fatalf("initial INSERT failed: %v", err)
	}

	// 2. Begin transaction and DROP TABLE
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DROP TABLE tx_to_drop;"); err != nil {
		t.Fatalf("DROP TABLE in tx failed: %v", err)
	}

	// Verify table appears dropped inside transaction
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_to_drop;")
	if err == nil {
		t.Fatalf("expected error querying dropped table inside transaction, but query succeeded")
	}

	// 3. Rollback transaction
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// 4. Verify table and all rows are restored
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, name FROM tx_to_drop ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT after rollback failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 restored rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[1]["name"] != "Bob" {
		t.Fatalf("unexpected restored row values: %+v", res.Rows)
	}
}

func TestTransactionalDDL_DropTable_Commit(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-drop-commit"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_drop_permanent (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DROP TABLE tx_drop_permanent;"); err != nil {
		t.Fatalf("DROP TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "COMMIT;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	// Confirm table is permanently dropped
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_drop_permanent;")
	if err == nil {
		t.Fatalf("expected error querying permanently dropped table, but query succeeded")
	}
}

func TestTransactionalDDL_AlterTable_AddColumn_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-alter-add-col"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_alter_tbl (id INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO tx_alter_tbl VALUES (1, 'Alice');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ALTER TABLE tx_alter_tbl ADD age INT;"); err != nil {
		t.Fatalf("ALTER TABLE ADD age failed: %v", err)
	}

	// Verify column exists inside transaction
	tbl, err := coord.engine.GetTable("tx_alter_tbl")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if _, _, found := tbl.FindColumn("age"); !found {
		t.Fatalf("expected 'age' column to exist inside transaction")
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify column is gone after rollback
	tblAfter, err := coord.engine.GetTable("tx_alter_tbl")
	if err != nil {
		t.Fatalf("GetTable after rollback failed: %v", err)
	}
	if _, _, found := tblAfter.FindColumn("age"); found {
		t.Fatalf("expected 'age' column to be rolled back, but it still exists")
	}
	if len(tblAfter.Columns) != 2 {
		t.Fatalf("expected 2 columns after rollback, got %d", len(tblAfter.Columns))
	}
}

func TestTransactionalDDL_CreateIndex_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-create-index"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_idx_tbl (id INT PRIMARY KEY, email VARCHAR(100));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE INDEX ix_tx_email ON tx_idx_tbl (email);"); err != nil {
		t.Fatalf("CREATE INDEX in tx failed: %v", err)
	}

	tbl, err := coord.engine.GetTable("tx_idx_tbl")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if _, _, found := tbl.FindIndex("ix_tx_email"); !found {
		t.Fatalf("expected index ix_tx_email inside transaction")
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	tblAfter, err := coord.engine.GetTable("tx_idx_tbl")
	if err != nil {
		t.Fatalf("GetTable after rollback failed: %v", err)
	}
	if _, _, found := tblAfter.FindIndex("ix_tx_email"); found {
		t.Fatalf("expected index ix_tx_email to be rolled back, but it still exists")
	}
}

func TestTransactionalDDL_TempTable_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-temp-rollback"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE #tx_local_temp (id INT PRIMARY KEY, score INT);"); err != nil {
		t.Fatalf("CREATE TABLE #tx_local_temp in tx failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO #tx_local_temp VALUES (1, 95);"); err != nil {
		t.Fatalf("INSERT into #temp failed: %v", err)
	}

	// Queryable inside transaction
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, score FROM #tx_local_temp;")
	if err != nil {
		t.Fatalf("SELECT from #temp in tx failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row in #temp inside tx, got %d", len(res.Rows))
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify #temp is gone from session after rollback
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM #tx_local_temp;")
	if err == nil {
		t.Fatalf("expected error querying #temp after rollback, but query succeeded")
	}
}

func TestTransactionalDDL_NestedRollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-nested-rollback"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN 1 failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN 2 failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_nested_tbl (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("CREATE TABLE in nested tx failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "COMMIT;"); err != nil {
		t.Fatalf("inner COMMIT failed: %v", err)
	}

	// Outer rollback must roll back the nested table creation
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("outer ROLLBACK failed: %v", err)
	}

	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_nested_tbl;")
	if err == nil {
		t.Fatalf("expected table tx_nested_tbl to be rolled back, but query succeeded")
	}
}

func TestTransactionalDDL_AlterTable_RenameTable_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-rename-rollback"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_orig_name (id INT PRIMARY KEY, val VARCHAR(20));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO tx_orig_name VALUES (1, 'hello');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ALTER TABLE tx_orig_name RENAME TO tx_new_name;"); err != nil {
		t.Fatalf("RENAME TO failed: %v", err)
	}

	// Inside tx: old name gone, new name queryable
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_orig_name;"); err == nil {
		t.Fatalf("expected old name to be gone inside transaction")
	}
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, val FROM tx_new_name;")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("expected 1 row from renamed table inside tx: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// After rollback: old name restored, new name gone
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_new_name;"); err == nil {
		t.Fatalf("expected new name to be gone after rollback")
	}
	resAfter, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, val FROM tx_orig_name;")
	if err != nil || len(resAfter.Rows) != 1 || resAfter.Rows[0]["val"] != "hello" {
		t.Fatalf("expected original table and data restored after rollback: %v, %+v", err, resAfter)
	}
}

func TestTransactionalDDL_SpRename_Table_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-sp-rename-rollback"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_sp_orig (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_rename 'tx_sp_orig', 'tx_sp_new';"); err != nil {
		t.Fatalf("sp_rename failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_sp_new;"); err == nil {
		t.Fatalf("expected tx_sp_new to be gone after rollback")
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_sp_orig;"); err != nil {
		t.Fatalf("expected tx_sp_orig restored after rollback: %v", err)
	}
}

func TestTransactionalDDL_AlterTable_DropColumn_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-drop-col-rollback"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE tx_col_drop (id INT PRIMARY KEY, to_drop VARCHAR(20));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ALTER TABLE tx_col_drop DROP COLUMN to_drop;"); err != nil {
		t.Fatalf("DROP COLUMN failed: %v", err)
	}

	tbl, err := coord.engine.GetTable("tx_col_drop")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if _, _, found := tbl.FindColumn("to_drop"); found {
		t.Fatalf("expected column to_drop to be removed inside tx")
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	tblAfter, err := coord.engine.GetTable("tx_col_drop")
	if err != nil {
		t.Fatalf("GetTable after rollback failed: %v", err)
	}
	if _, _, found := tblAfter.FindColumn("to_drop"); !found {
		t.Fatalf("expected column to_drop restored after rollback")
	}
}

func TestTransactionalDDL_DropTempTable_Rollback(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "tx-ddl-drop-temp-rollback"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE #persist_temp (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("CREATE TABLE #temp failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DROP TABLE #persist_temp;"); err != nil {
		t.Fatalf("DROP TABLE #temp in tx failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Restored after rollback
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM #persist_temp;"); err != nil {
		t.Fatalf("expected #persist_temp to be restored after rollback: %v", err)
	}
}
