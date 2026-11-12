package execution_test

import (
	"context"
	"io"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestStringAgg_GroupByWithOrderByAndWithinGroup(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "emp_tags",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "dept", Type: storage.TypeVarChar},
			{Name: "tag", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	rows := []storage.Record{
		{"id": storage.IntValue(1), "dept": storage.StringValue("eng"), "tag": storage.StringValue("go")},
		{"id": storage.IntValue(2), "dept": storage.StringValue("eng"), "tag": storage.StringValue("sql")},
		{"id": storage.IntValue(3), "dept": storage.StringValue("sales"), "tag": storage.StringValue("crm")},
	}
	for _, r := range rows {
		if err := tx.Insert("emp_tags", r); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	readTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read tx failed: %v", err)
	}
	defer readTx.Rollback()

	getTable := func(name string) (*storage.TableSchema, error) {
		return eng.GetTable(name)
	}

	t.Run("GroupByWithOrderByProjection", func(t *testing.T) {
		p := planner.NewPlanner()
		qInfo, err := p.ParseSelect("SELECT dept, STRING_AGG(tag, ',') AS tags FROM emp_tags GROUP BY dept ORDER BY dept ASC")
		if err != nil {
			t.Fatalf("ParseSelect failed: %v", err)
		}
		plan := p.BuildMultiJoinPlanWithStats(qInfo, schema, nil, nil, nil)
		iter, err := execution.BuildExecutorWithOptions(plan, readTx, getTable, execution.DefaultExecutorOptions())
		if err != nil {
			t.Fatalf("BuildExecutor failed: %v", err)
		}
		ctx := context.Background()
		if err := iter.Open(ctx); err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer iter.Close()

		var results []execution.Tuple
		for {
			tup, err := iter.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Next failed: %v", err)
			}
			results = append(results, tup)
		}

		if len(results) != 2 {
			t.Fatalf("expected 2 grouped rows, got %d", len(results))
		}
		if results[0].Values[0] != "eng" || results[0].Values[1] != "go,sql" {
			t.Fatalf("expected row 0 = [eng, go,sql], got %v", results[0].Values)
		}
		if results[1].Values[0] != "sales" || results[1].Values[1] != "crm" {
			t.Fatalf("expected row 1 = [sales, crm], got %v", results[1].Values)
		}
	})

	t.Run("StringAggWithinGroupOrderByDesc", func(t *testing.T) {
		p := planner.NewPlanner()
		qInfo, err := p.ParseSelect("SELECT dept, STRING_AGG(tag, '|') WITHIN GROUP (ORDER BY tag DESC) AS tags FROM emp_tags GROUP BY dept ORDER BY dept ASC")
		if err != nil {
			t.Fatalf("ParseSelect failed: %v", err)
		}
		plan := p.BuildMultiJoinPlanWithStats(qInfo, schema, nil, nil, nil)
		iter, err := execution.BuildExecutorWithOptions(plan, readTx, getTable, execution.DefaultExecutorOptions())
		if err != nil {
			t.Fatalf("BuildExecutor failed: %v", err)
		}
		ctx := context.Background()
		if err := iter.Open(ctx); err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer iter.Close()

		tup, err := iter.Next(ctx)
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		if tup.Values[0] != "eng" || tup.Values[1] != "sql|go" {
			t.Fatalf("expected row 0 = [eng, sql|go], got %v", tup.Values)
		}
	})

	t.Run("SingleGroupEqualityWhereWithStringAgg", func(t *testing.T) {
		p := planner.NewPlanner()
		qInfo, err := p.ParseSelect("SELECT STRING_AGG(tag, ',') AS tags FROM emp_tags WHERE dept = 'eng'")
		if err != nil {
			t.Fatalf("ParseSelect failed: %v", err)
		}
		plan := p.BuildMultiJoinPlanWithStats(qInfo, schema, nil, nil, nil)
		iter, err := execution.BuildExecutorWithOptions(plan, readTx, getTable, execution.DefaultExecutorOptions())
		if err != nil {
			t.Fatalf("BuildExecutor failed: %v", err)
		}
		ctx := context.Background()
		if err := iter.Open(ctx); err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer iter.Close()

		tup, err := iter.Next(ctx)
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		if tup.Values[0] != "go,sql" {
			t.Fatalf("expected [go,sql], got %v", tup.Values)
		}
	})
}
