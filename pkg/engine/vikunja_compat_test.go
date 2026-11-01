package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestVikunja_CTEWithUnionDerivedTable(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL(`CREATE TABLE projects (id INT PRIMARY KEY, owner_id INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE projects failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`CREATE TABLE users_projects (project_id INT, user_id INT, permission INT, PRIMARY KEY(project_id, user_id));`)
	if err != nil {
		t.Fatalf("CREATE TABLE users_projects failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`CREATE TABLE team_projects (project_id INT, team_id INT, permission INT, PRIMARY KEY(project_id, team_id));`)
	if err != nil {
		t.Fatalf("CREATE TABLE team_projects failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`CREATE TABLE team_members (team_id INT, user_id INT, PRIMARY KEY(team_id, user_id));`)
	if err != nil {
		t.Fatalf("CREATE TABLE team_members failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`CREATE TABLE project_ancestors (ancestor_id INT, project_id INT, depth INT, PRIMARY KEY(ancestor_id, project_id));`)
	if err != nil {
		t.Fatalf("CREATE TABLE project_ancestors failed: %v", err)
	}

	// Insert test data
	_, err = coord.ExecuteSQL(`INSERT INTO projects VALUES (1, 1);`)
	if err != nil {
		t.Fatalf("INSERT projects failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO project_ancestors VALUES (1, 1, 0);`)
	if err != nil {
		t.Fatalf("INSERT project_ancestors failed: %v", err)
	}

	q := `WITH grants (project_id, permission) AS (
    SELECT project_id, MAX(permission)
    FROM (
        SELECT id AS project_id, 2 AS permission FROM projects WHERE owner_id = 1
        UNION ALL
        SELECT project_id, permission FROM users_projects WHERE user_id = 1
        UNION ALL
        SELECT tp.project_id, tp.permission
        FROM team_projects tp
        INNER JOIN team_members tm ON tm.team_id = tp.team_id
        WHERE tm.user_id = 1
    ) direct_grants
    GROUP BY project_id
)
SELECT id,
    CASE WHEN MAX(permission) = 2 THEN 2
         ELSE MAX(CASE WHEN nearest = 1 THEN permission END)
    END AS permission
FROM (
    SELECT pa.project_id AS id, g.permission,
        ROW_NUMBER() OVER (PARTITION BY pa.project_id ORDER BY pa.depth) AS nearest
    FROM grants g
    INNER JOIN project_ancestors pa ON pa.ancestor_id = g.project_id
) ranked
GROUP BY id`

	// Execute the full Vikunja project permission query with CTE and derived UNION ALL
	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	idVal := fmt.Sprintf("%v", res.Rows[0]["id"])
	permVal := fmt.Sprintf("%v", res.Rows[0]["permission"])
	if idVal != "1" || permVal != "2" {
		t.Fatalf("unexpected result: %+v (types: %T, %T)", res.Rows[0], res.Rows[0]["id"], res.Rows[0]["permission"])
	}
}

func TestVikunja_RecursiveCTE(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL(`CREATE TABLE projects (id INT PRIMARY KEY, parent_project_id INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE projects failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO projects VALUES (1, 0);`)
	if err != nil {
		t.Fatalf("INSERT projects failed: %v", err)
	}

	q := `WITH RECURSIVE project_hierarchy AS (
    SELECT
        id,
        parent_project_id,
        0 AS level,
        id AS original_project_id
    FROM projects
    WHERE id IN (1)

    UNION ALL

    SELECT
        p.id,
        p.parent_project_id,
        ph.level + 1,
        ph.original_project_id
    FROM projects p
    INNER JOIN project_hierarchy ph ON p.id = ph.parent_project_id
)
SELECT id, parent_project_id, level, original_project_id
FROM project_hierarchy
ORDER BY id;`

	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("ExecuteSQL WITH RECURSIVE failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["id"]) != "1" {
		t.Fatalf("expected id=1, got %+v", res.Rows[0])
	}
}

func TestVikunja_WhereOrPredicate(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL(`CREATE TABLE users (id INT PRIMARY KEY, username VARCHAR(50), email VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE TABLE users failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`CREATE INDEX idx_users_username ON users(username);`)
	if err != nil {
		t.Fatalf("CREATE INDEX idx_users_username failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`CREATE INDEX idx_users_email ON users(email);`)
	if err != nil {
		t.Fatalf("CREATE INDEX idx_users_email failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES (1, 'maro', 'maro@example.com');`)
	if err != nil {
		t.Fatalf("INSERT users failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT id, username, email FROM users WHERE (username = 'maro' OR email = 'maro') LIMIT 1;`)
	if err != nil {
		t.Fatalf("ExecuteSQL WHERE OR failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for OR predicate with indexed columns, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["username"]) != "maro" {
		t.Fatalf("expected username=maro, got %+v", res.Rows[0])
	}
}
