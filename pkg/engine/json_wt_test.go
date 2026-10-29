//go:build cgo && wiredtiger

package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/planner"
	"lionsql/pkg/storage/wiredtiger"
)

func TestJSON_WiredTigerQueries(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_json_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err = coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, doc VARCHAR(500));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES 
		(1, '{"city": "Seoul", "age": 28, "tags": ["go", "db"]}'),
		(2, '{"city": "Busan", "age": 35, "tags": ["rust"]}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT id, doc->>'city' AS city, JSON_VALUE(doc, '$.age') AS age FROM users WHERE doc->>'city' = 'Seoul';")
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["city"] != "Seoul" || fmt.Sprintf("%v", res.Rows[0]["age"]) != "28" {
		t.Fatalf("expected 1 row with city='Seoul' age=28, got %+v", res.Rows)
	}
}

func TestWiredTiger_BranchOrderByQuery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_branch_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	createSQL := `CREATE TABLE branch (
		id BIGINT IDENTITY(1,1) PRIMARY KEY,
		repo_id BIGINT,
		name VARCHAR(255),
		commit_id VARCHAR(40),
		commit_message VARCHAR(500),
		pusher_id BIGINT,
		is_deleted BIT,
		deleted_by_id BIGINT,
		deleted_unix BIGINT,
		commit_time BIGINT,
		created_unix BIGINT,
		updated_unix BIGINT
	);`
	if _, err := coord.ExecuteSQL(createSQL); err != nil {
		t.Fatalf("CREATE TABLE branch failed: %v", err)
	}

	if _, err := coord.ExecuteSQL(`CREATE UNIQUE INDEX UQE_branch_s ON branch (repo_id, name);`); err != nil {
		t.Fatalf("CREATE UNIQUE INDEX failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`CREATE INDEX IDX_branch_is_deleted ON branch (is_deleted);`); err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	insertSQL := `INSERT INTO branch (repo_id, name, commit_id, commit_message, pusher_id, is_deleted, deleted_by_id, deleted_unix, commit_time, created_unix, updated_unix)
		VALUES (9, 'main', '1b5d64565c390a6129da33303c4113f1ea9eb901', 'Initial commit', 0, 0, 0, 0, 1730000000, 1730000000, 1730000000);`
	if _, err := coord.ExecuteSQL(insertSQL); err != nil {
		t.Fatalf("INSERT branch failed: %v", err)
	}

	q1 := `SELECT * FROM branch WHERE repo_id=9;`
	res1, err := coord.ExecuteSQL(q1)
	if err != nil {
		t.Fatalf("q1 failed: %v", err)
	}
	t.Logf("q1 (no ORDER BY) rows: %d", len(res1.Rows))

	q2 := `SELECT [id], [repo_id], [name], [commit_id], [commit_message], [pusher_id], [is_deleted], [deleted_by_id], [deleted_unix], [commit_time], [created_unix], [updated_unix] FROM [branch] WHERE repo_id=9 ORDER BY is_deleted ASC, commit_time DESC, name ASC;`
	p := planner.NewPlanner()
	qInfo, pErr := p.ParseSelect(q2)
	t.Logf("ParseSelect err: %v, qInfo: %+v", pErr, qInfo)
	tbl, _ := eng.GetTable("branch")
	plan := p.BuildMultiJoinPlanWithStats(qInfo, tbl, nil, nil, nil)
	t.Logf("Volcano plan: %+v", plan)
	if len(plan.Children) > 0 {
		t.Logf("  Child 0: %+v", plan.Children[0])
		if len(plan.Children[0].Children) > 0 {
			t.Logf("    Child 0.0: %+v", plan.Children[0].Children[0])
		}
	}

	res2, err := coord.ExecuteSQL(q2)
	if err != nil {
		t.Fatalf("q2 failed: %v", err)
	}
	t.Logf("q2 (with ORDER BY) rows: %d", len(res2.Rows))

	if len(res2.Rows) != 1 {
		t.Fatalf("expected 1 row from q2, got %d", len(res2.Rows))
	}

	q3 := `SELECT [id], [repo_id], [name], [commit_id], [commit_message], [pusher_id], [is_deleted], [deleted_by_id], [deleted_unix], [commit_time], [created_unix], [updated_unix] FROM [branch] WHERE (repo_id=9) AND (name='main') ORDER BY [id] ASC OFFSET 0 ROWS FETCH NEXT 1 ROWS ONLY;`
	qInfo3, pErr3 := p.ParseSelect(q3)
	t.Logf("ParseSelect q3 err: %v, qInfo: %+v", pErr3, qInfo3)
	plan3 := p.BuildMultiJoinPlanWithStats(qInfo3, tbl, nil, nil, nil)
	t.Logf("Volcano plan3: %+v", plan3)
	curr := plan3
	for i := 0; curr != nil && len(curr.Children) > 0; i++ {
		t.Logf("  Level %d child 0: type=%s, relation=%s, index=%s, indexCond=%q, filterCond=%q", i, curr.Children[0].Type, curr.Children[0].Relation, curr.Children[0].IndexName, curr.Children[0].IndexCond, curr.Children[0].FilterCond)
		curr = curr.Children[0]
	}
	res3, err3 := coord.ExecuteSQL(q3)
	if err3 != nil {
		t.Fatalf("q3 failed: %v", err3)
	}
	t.Logf("q3 rows returned: %d, rows: %+v", len(res3.Rows), res3.Rows)
	if len(res3.Rows) != 1 {
		t.Fatalf("expected 1 row from q3, got %d", len(res3.Rows))
	}
}

func TestVolcano_JoinRepoAndUser(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_join_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL(`CREATE TABLE [user] (id BIGINT PRIMARY KEY, lower_name VARCHAR(255));`); err != nil {
		t.Fatalf("CREATE TABLE user failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`CREATE TABLE [repository] (id BIGINT PRIMARY KEY, owner_id BIGINT, lower_name VARCHAR(255), name VARCHAR(255));`); err != nil {
		t.Fatalf("CREATE TABLE repo failed: %v", err)
	}

	if _, err := coord.ExecuteSQL(`INSERT INTO [user] (id, lower_name) VALUES (9, 'lionsql-core');`); err != nil {
		t.Fatalf("INSERT user failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO [repository] (id, owner_id, lower_name, name) VALUES (9, 9, 'lion-docs', 'lion-docs');`); err != nil {
		t.Fatalf("INSERT repo failed: %v", err)
	}

	q := `SELECT repository.* FROM [repository] INNER JOIN [user] ON [user].id = repository.owner_id WHERE (repository.lower_name = 'lion-docs') AND ([user].lower_name = 'lionsql-core') ORDER BY [repository].[id] ASC OFFSET 0 ROWS FETCH NEXT 1 ROWS ONLY;`
	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	t.Logf("JOIN rows returned: %d, rows: %+v", len(res.Rows), res.Rows)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row from JOIN, got %d", len(res.Rows))
	}
}

func TestJSON_ExpressionIndex_WiredTiger(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_json_expr_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err = coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, doc VARCHAR(500));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES 
		(1, '{"city": "Seoul", "country": "KR"}'),
		(2, '{"city": "Busan", "country": "KR"}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	res, err := coord.ExecuteSQL("CREATE INDEX idx_wt_city ON users ((doc->>'city'));")
	if err != nil || !res.Success {
		t.Fatalf("CREATE INDEX ((doc->>'city')) failed: %v, res: %+v", err, res)
	}

	res, err = coord.ExecuteSQL("CREATE INDEX idx_wt_country ON users ((JSON_VALUE(doc, '$.country')));")
	if err != nil || !res.Success {
		t.Fatalf("CREATE INDEX ((JSON_VALUE(...))) failed: %v, res: %+v", err, res)
	}

	tbl, err := eng.GetTable("users")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}

	idxCity, found := tbl.FindExpressionIndex("doc", "$.city")
	if !found || !idxCity.IsExpression() || idxCity.ExprPath != "$.city" {
		t.Fatalf("expected idx_wt_city expression index, got: %+v", idxCity)
	}

	idxCountry, found := tbl.FindExpressionIndex("doc", "$.country")
	if !found || !idxCountry.IsExpression() || idxCountry.ExprPath != "$.country" {
		t.Fatalf("expected idx_wt_country expression index, got: %+v", idxCountry)
	}

	// Test EXPLAIN selecting idx_wt_city on WiredTiger
	resExp, err := coord.ExecuteSQL("EXPLAIN SELECT id, doc FROM users WHERE doc->>'city' = 'Seoul';")
	if err != nil || !resExp.Success {
		t.Fatalf("EXPLAIN failed on WT: %v", err)
	}
	expText := fmt.Sprintf("%v", resExp.Rows)
	if !strings.Contains(expText, "Index Scan") || !strings.Contains(expText, "idx_wt_city") {
		t.Fatalf("expected Index Scan on WT, got: %s", expText)
	}

	// Test SELECT with WHERE condition on WiredTiger
	resQuery, err := coord.ExecuteSQL("SELECT id, doc->>'city' AS city FROM users WHERE doc->>'city' = 'Seoul';")
	if err != nil || !resQuery.Success {
		t.Fatalf("SELECT failed on WT: %v", err)
	}
	if len(resQuery.Rows) != 1 || resQuery.Rows[0]["city"] != "Seoul" {
		t.Fatalf("expected 1 row with city='Seoul' on WT, got: %+v", resQuery.Rows)
	}
}

