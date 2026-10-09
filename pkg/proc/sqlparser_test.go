package proc_test

import (
	"testing"

	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

func TestParseSQL_GeneralSelect(t *testing.T) {
	sql := `SELECT u.id, u.name AS username, count(*) AS total, a.balance + 100 AS new_bal
		FROM users u
		LEFT JOIN accounts a ON u.id = a.user_id
		WHERE u.role = 'admin' AND a.balance >= 1000
		GROUP BY u.id, u.name
		HAVING count(*) > 1
		ORDER BY new_bal DESC
		LIMIT 20;`

	stmt, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}

	sel, ok := stmt.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected *GeneralSelectStmt, got %T", stmt)
	}

	if len(sel.Projections) != 4 {
		t.Errorf("expected 4 projections, got %d", len(sel.Projections))
	}
	if sel.From.Name != "users" || sel.From.Alias != "u" {
		t.Errorf("unexpected from table: %+v", sel.From)
	}
	if len(sel.Joins) != 1 || sel.Joins[0].Type != "LEFT" || sel.Joins[0].Table.Name != "accounts" {
		t.Errorf("unexpected joins: %+v", sel.Joins)
	}
	if sel.Where == nil {
		t.Errorf("expected WHERE clause")
	}
	if len(sel.GroupBy) != 2 {
		t.Errorf("expected 2 group by expressions, got %d", len(sel.GroupBy))
	}
	if sel.Having == nil {
		t.Errorf("expected HAVING clause")
	}
	if len(sel.OrderBy) != 1 || !sel.OrderBy[0].Desc {
		t.Errorf("expected 1 DESC order by item, got %+v", sel.OrderBy)
	}
	if sel.Limit != 20 {
		t.Errorf("expected limit 20, got %d", sel.Limit)
	}
}

func TestParseSQL_CTEAndExplain(t *testing.T) {
	sql := `EXPLAIN ANALYZE WITH active_users AS (
		SELECT id, name FROM users WHERE active = TRUE
	)
	SELECT name FROM active_users WHERE id > 10;`

	stmt, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}

	sel, ok := stmt.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected *GeneralSelectStmt, got %T", stmt)
	}

	if !sel.IsExplain || !sel.IsAnalyze {
		t.Errorf("expected EXPLAIN ANALYZE to be true")
	}
	if len(sel.CTEs) != 1 || sel.CTEs[0].Name != "active_users" {
		t.Errorf("unexpected CTE: %+v", sel.CTEs)
	}
	if sel.From.Name != "active_users" {
		t.Errorf("expected FROM active_users, got %s", sel.From.Name)
	}
}

func TestParseSQL_DDLAndDML(t *testing.T) {
	// 1. Create table
	stmt, err := proc.ParseSQL(`CREATE TABLE IF NOT EXISTS accounts (id INT PRIMARY KEY, balance FLOAT NOT NULL);`)
	if err != nil {
		t.Fatalf("Create table parse error: %v", err)
	}
	ct, ok := stmt.(*proc.CreateTableStmt)
	if !ok || ct.Name != "accounts" || !ct.IfNotExists || len(ct.Columns) != 2 {
		t.Errorf("unexpected CreateTableStmt: %+v", ct)
	}

	// 2. Create index
	stmt, err = proc.ParseSQL(`CREATE UNIQUE INDEX idx_acc_id ON accounts (id);`)
	if err != nil {
		t.Fatalf("Create index parse error: %v", err)
	}
	ci, ok := stmt.(*proc.CreateIndexStmt)
	if !ok || ci.Name != "idx_acc_id" || !ci.Unique || ci.Table != "accounts" {
		t.Errorf("unexpected CreateIndexStmt: %+v", ci)
	}

	// 3. Drop table
	stmt, err = proc.ParseSQL(`DROP TABLE IF EXISTS accounts;`)
	if err != nil {
		t.Fatalf("Drop table parse error: %v", err)
	}
	dt, ok := stmt.(*proc.DropTableStmt)
	if !ok || dt.Name != "accounts" || !dt.IfExists {
		t.Errorf("unexpected DropTableStmt: %+v", dt)
	}

	// 4. Multi-tuple Insert
	stmt, err = proc.ParseSQL(`INSERT INTO users (id, name) VALUES (1, 'Alice'), (2, 'Bob');`)
	if err != nil {
		t.Fatalf("Insert parse error: %v", err)
	}
	ins, ok := stmt.(*proc.GeneralInsertStmt)
	if !ok || len(ins.Tuples) != 2 || ins.Table != "users" {
		t.Errorf("unexpected GeneralInsertStmt: %+v", ins)
	}
}

func TestEvalRowExpr_AdvancedPredicates(t *testing.T) {
	row := map[string]any{
		"id":         int64(42),
		"name":       "Alice",
		"balance":    float64(2500.50),
		"role":       "admin",
		"status":     "ACTIVE",
		"null_field": nil,
	}

	// 1. LIKE
	likeExpr := &proc.LikeExpr{
		Expr:    &proc.ColumnRefExpr{Name: "name"},
		Pattern: &proc.LiteralExpr{Val: storage.StringValue("Al%")},
	}
	if !proc.EvalBoolExpr(likeExpr, row, nil) {
		t.Errorf("expected LIKE 'Al%%' to match 'Alice'")
	}

	// 2. BETWEEN
	betweenExpr := &proc.BetweenExpr{
		Expr: &proc.ColumnRefExpr{Name: "balance"},
		Low:  &proc.LiteralExpr{Val: storage.FloatValue(1000.0)},
		High: &proc.LiteralExpr{Val: storage.FloatValue(3000.0)},
	}
	if !proc.EvalBoolExpr(betweenExpr, row, nil) {
		t.Errorf("expected balance to be between 1000 and 3000")
	}

	// 3. IN list
	inExpr := &proc.InExpr{
		Expr: &proc.ColumnRefExpr{Name: "role"},
		List: []proc.Expr{
			&proc.LiteralExpr{Val: storage.StringValue("user")},
			&proc.LiteralExpr{Val: storage.StringValue("admin")},
		},
	}
	if !proc.EvalBoolExpr(inExpr, row, nil) {
		t.Errorf("expected role to be IN ('user', 'admin')")
	}

	// 4. IS NULL & IS NOT NULL
	isNull := &proc.IsNullExpr{Expr: &proc.ColumnRefExpr{Name: "null_field"}, Not: false}
	if !proc.EvalBoolExpr(isNull, row, nil) {
		t.Errorf("expected null_field IS NULL to be true")
	}
	isNotNull := &proc.IsNullExpr{Expr: &proc.ColumnRefExpr{Name: "name"}, Not: true}
	if !proc.EvalBoolExpr(isNotNull, row, nil) {
		t.Errorf("expected name IS NOT NULL to be true")
	}

	// 5. CASE WHEN
	caseExpr := &proc.CaseExpr{
		Whens: []proc.CaseWhen{
			{
				Condition: &proc.BinaryExpr{
					Left:  &proc.ColumnRefExpr{Name: "balance"},
					Op:    proc.TokenGt,
					Right: &proc.LiteralExpr{Val: storage.FloatValue(5000.0)},
				},
				Result: &proc.LiteralExpr{Val: storage.StringValue("VIP")},
			},
			{
				Condition: &proc.BinaryExpr{
					Left:  &proc.ColumnRefExpr{Name: "balance"},
					Op:    proc.TokenGt,
					Right: &proc.LiteralExpr{Val: storage.FloatValue(2000.0)},
				},
				Result: &proc.LiteralExpr{Val: storage.StringValue("Gold")},
			},
		},
		Else: &proc.LiteralExpr{Val: storage.StringValue("Standard")},
	}
	val, err := proc.EvalRowExpr(caseExpr, row, nil)
	if err != nil || val != "Gold" {
		t.Errorf("expected CASE WHEN to evaluate to 'Gold', got %v (err=%v)", val, err)
	}
}

func TestCleanSchemaPrefix_CompositeQualifiers(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"accounts", "accounts"},
		{"[accounts]", "accounts"},
		{"\"accounts\"", "accounts"},
		{"`accounts`", "accounts"},
		{"[dbo].[accounts]", "accounts"},
		{"dbo.accounts", "accounts"},
		{"public.accounts", "accounts"},
		{"guest.accounts", "accounts"},
		{"[mydb].[dbo].[accounts]", "accounts"},
		{"mydb.dbo.accounts", "accounts"},
		{"[mydb]..[accounts]", "accounts"},
		{"mydb..accounts", "accounts"},
		{"[server].[mydb].[dbo].[accounts]", "accounts"},
		{"sys.tables", "sys.tables"},
		{"[sys].[tables]", "sys.tables"},
		{"[mydb].[sys].[tables]", "sys.tables"},
		{"mydb.sys.tables", "sys.tables"},
		{"information_schema.columns", "information_schema.columns"},
		{"[mydb].[information_schema].[columns]", "information_schema.columns"},
		{"pg_catalog.pg_tables", "pg_catalog.pg_tables"},
		{"a.id", "a.id"},
		{"[a].[id]", "a.id"},
		{"##temp_orders", "##temp_orders"},
	}

	for _, tt := range tests {
		got := proc.CleanSchemaPrefix(tt.input)
		if got != tt.expected {
			t.Errorf("CleanSchemaPrefix(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseSQL_ImplicitAliasAndCompositeQualifiers(t *testing.T) {
	// 1. Implicit alias without AS in FROM and JOIN
	sql := `SELECT a.id, a.name, o.total FROM [mydb].[dbo].[accounts] a INNER JOIN [mydb]..[orders] o ON a.id = o.account_id WHERE a.balance > 100;`
	stmt, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel, ok := stmt.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected *GeneralSelectStmt, got %T", stmt)
	}

	if sel.From.Name != "accounts" || sel.From.Alias != "a" {
		t.Errorf("expected From {Name: accounts, Alias: a}, got %+v", sel.From)
	}
	if len(sel.Joins) != 1 || sel.Joins[0].Table.Name != "orders" || sel.Joins[0].Table.Alias != "o" {
		t.Errorf("expected Join {Name: orders, Alias: o}, got %+v", sel.Joins)
	}

	// 2. Implicit column alias without AS
	sql2 := `SELECT id id_col, name name_col FROM accounts WHERE id = 1;`
	stmt2, err := proc.ParseSQL(sql2)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel2, ok := stmt2.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected *GeneralSelectStmt, got %T", stmt2)
	}
	if len(sel2.Projections) != 2 || sel2.Projections[0].Alias != "id_col" || sel2.Projections[1].Alias != "name_col" {
		t.Errorf("unexpected projections: %+v", sel2.Projections)
	}
}
