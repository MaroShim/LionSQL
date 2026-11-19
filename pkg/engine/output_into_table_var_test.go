package engine_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestCoordinator_InsertOutputIntoTableVariable_InProc(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sessID := "tsql-sess-output-into-tv"

	if _, err := coord.ExecuteSessionSQL(sessID, `
CREATE TABLE t_output (
    id INT IDENTITY(1, 1) PRIMARY KEY,
    name VARCHAR(20),
    status VARCHAR(10)
);`); err != nil {
		t.Fatalf("CREATE TABLE t_output failed: %v", err)
	}

	t.Run("InsideProcedure", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_TestInsertOutput AS
BEGIN
    DECLARE @inserted_ids TABLE (
        new_id INT,
        inserted_name VARCHAR(20)
    );

    INSERT INTO t_output (name, status)
    OUTPUT INSERTED.id, INSERTED.name INTO @inserted_ids
    VALUES ('Item1', 'ACTIVE'), ('Item2', 'PENDING');

    SELECT * FROM @inserted_ids;
END;`

		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE sp_TestInsertOutput failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_TestInsertOutput;")
		if err != nil {
			t.Fatalf("EXEC sp_TestInsertOutput failed: %v", err)
		}

		t.Logf("Proc Result rows: %+v", res.Rows)
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows in @inserted_ids, got %d", len(res.Rows))
		}
	})

	t.Run("TopLevelBatch", func(t *testing.T) {
		batch := `
DECLARE @top_inserted TABLE (
    new_id INT,
    inserted_name VARCHAR(20)
);

INSERT INTO t_output (name, status)
OUTPUT INSERTED.id, INSERTED.name INTO @top_inserted
VALUES ('Item3', 'ACTIVE'), ('Item4', 'PENDING');

SELECT * FROM @top_inserted;
`
		stmts := engine.SplitStatements(batch)
		t.Logf("Split stmts count: %d", len(stmts))
		for i, s := range stmts {
			t.Logf("stmt %d: %q", i, s)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, batch)
		if err != nil {
			t.Fatalf("Top-level batch failed: %v", err)
		}

		t.Logf("Top-level Result: Command=%s Columns=%+v Rows=%+v Next=%+v", res.Command, res.Columns, res.Rows, res.Next)
		if res.Next != nil {
			t.Logf("Next Result: Command=%s Columns=%+v Rows=%+v", res.Next.Command, res.Next.Columns, res.Next.Rows)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows in @top_inserted, got %d", len(res.Rows))
		}
	})
}
