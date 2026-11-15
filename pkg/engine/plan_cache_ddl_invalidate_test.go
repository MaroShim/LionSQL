package engine

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestCoordinator_VDBEPlanCache_DDLInvalidate(t *testing.T) {
	memEng := memengine.New()
	cat := catalog.New(memEng)
	coord := NewCoordinator(cat, memEng, Config{})
	defer coord.Close()

	ctx := context.Background()

	// 1. Create initial table and procedure
	_, err := coord.ExecuteSessionSQLContext(ctx, "default", "CREATE TABLE cache_t1 (id INT PRIMARY KEY, val INT);")
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQLContext(ctx, "default", `
CREATE PROCEDURE sp_get_cache_t1
AS
BEGIN
    SELECT id, val FROM cache_t1;
END;
`)
	if err != nil {
		t.Fatalf("create procedure failed: %v", err)
	}

	// 2. Execute procedure -> caches compiled bytecode in PlanCache
	res, err := coord.ExecuteSessionSQLContext(ctx, "default", "EXEC sp_get_cache_t1;")
	if err != nil || !res.Success {
		t.Fatalf("exec procedure failed: %v", err)
	}

	cache := coord.BytecodePlanCache()
	if cache == nil {
		t.Fatalf("expected non-nil BytecodePlanCache")
	}

	if _, ok := cache.GetByName("sp_get_cache_t1"); !ok {
		t.Fatalf("expected sp_get_cache_t1 to be cached in PlanCache after execution")
	}

	// 3. Perform DDL on table cache_t1 (ALTER TABLE ADD COLUMN)
	_, err = coord.ExecuteSessionSQLContext(ctx, "default", "ALTER TABLE cache_t1 ADD extra INT;")
	if err != nil {
		t.Fatalf("alter table failed: %v", err)
	}

	// 4. Verify PlanCache was automatically invalidated and cleared
	if _, ok := cache.GetByName("sp_get_cache_t1"); ok {
		t.Fatalf("expected sp_get_cache_t1 to be invalidated from PlanCache after ALTER TABLE DDL")
	}
	if cache.Len() != 0 {
		t.Fatalf("expected PlanCache to be empty after DDL invalidation, got Len=%d", cache.Len())
	}

	// 5. Subsequent execution recompiles and executes successfully against modified schema
	res, err = coord.ExecuteSessionSQLContext(ctx, "default", "EXEC sp_get_cache_t1;")
	if err != nil || !res.Success {
		t.Fatalf("exec procedure after recompile failed: %v", err)
	}

	if _, ok := cache.GetByName("sp_get_cache_t1"); !ok {
		t.Fatalf("expected sp_get_cache_t1 to be re-cached after execution")
	}

	// 6. Perform DROP TABLE DDL -> verifies PlanCache is cleared again
	_, err = coord.ExecuteSessionSQLContext(ctx, "default", "DROP TABLE cache_t1;")
	if err != nil {
		t.Fatalf("drop table failed: %v", err)
	}

	if _, ok := cache.GetByName("sp_get_cache_t1"); ok {
		t.Fatalf("expected PlanCache to be invalidated after DROP TABLE")
	}
}
