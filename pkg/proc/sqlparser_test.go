package proc_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
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

	// 2b. Create index with INCLUDE
	stmt, err = proc.ParseSQL(`CREATE INDEX idx_emp_dept ON employees (dept) INCLUDE (salary, title);`)
	if err != nil {
		t.Fatalf("Create index with INCLUDE parse error: %v", err)
	}
	ciInc, ok := stmt.(*proc.CreateIndexStmt)
	if !ok || ciInc.Name != "idx_emp_dept" || ciInc.Table != "employees" || len(ciInc.Columns) != 1 || ciInc.Columns[0] != "dept" || len(ciInc.IncludeCols) != 2 || ciInc.IncludeCols[0] != "salary" || ciInc.IncludeCols[1] != "title" {
		t.Errorf("unexpected CreateIndexStmt with INCLUDE: %+v", ciInc)
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

	// 5. Insert Into Select
	stmt, err = proc.ParseSQL(`INSERT INTO users_backup (id, name) SELECT id, name FROM users WHERE id > 0;`)
	if err != nil {
		t.Fatalf("Insert into select parse error: %v", err)
	}
	insSel, ok := stmt.(*proc.GeneralInsertStmt)
	if !ok || insSel.Select == nil || insSel.Table != "users_backup" || len(insSel.Columns) != 2 {
		t.Errorf("unexpected GeneralInsertStmt with Select: %+v", insSel)
	}

	// 6. Derived Table in FROM and JOIN
	stmt, err = proc.ParseSQL(`SELECT t.id, o.amount FROM (SELECT id, name FROM users) AS t JOIN (SELECT user_id, amount FROM orders) AS o ON t.id = o.user_id;`)
	if err != nil {
		t.Fatalf("Derived table parse error: %v", err)
	}
	dtSel, ok := stmt.(*proc.GeneralSelectStmt)
	if !ok || dtSel.From.Subquery == nil || dtSel.From.Alias != "t" {
		t.Errorf("unexpected GeneralSelectStmt for FROM subquery: %+v", dtSel)
	}
	if len(dtSel.Joins) != 1 || dtSel.Joins[0].Table.Subquery == nil || dtSel.Joins[0].Table.Alias != "o" {
		t.Errorf("unexpected GeneralSelectStmt for JOIN subquery: %+v", dtSel)
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

func TestParseSQL_PagingAndTop(t *testing.T) {
	// 1. Standard OFFSET ... ROWS FETCH NEXT ... ROWS ONLY
	sql1 := `SELECT id, balance FROM accounts ORDER BY balance DESC OFFSET 10 ROWS FETCH NEXT 5 ROWS ONLY;`
	stmt1, err := proc.ParseSQL(sql1)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel1, ok := stmt1.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected *GeneralSelectStmt, got %T", stmt1)
	}
	if sel1.Offset != 10 || sel1.Limit != 5 {
		t.Errorf("expected offset 10, limit 5, got offset %d, limit %d", sel1.Offset, sel1.Limit)
	}

	// 2. OFFSET only
	sql2 := `SELECT id FROM accounts ORDER BY id OFFSET 20 ROWS;`
	stmt2, err := proc.ParseSQL(sql2)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel2 := stmt2.(*proc.GeneralSelectStmt)
	if sel2.Offset != 20 || sel2.Limit != -1 {
		t.Errorf("expected offset 20, limit -1, got offset %d, limit %d", sel2.Offset, sel2.Limit)
	}

	// 3. FETCH FIRST ... ROWS ONLY without explicit OFFSET
	sql3 := `SELECT id FROM accounts ORDER BY id FETCH FIRST 15 ROWS ONLY;`
	stmt3, err := proc.ParseSQL(sql3)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel3 := stmt3.(*proc.GeneralSelectStmt)
	if sel3.Offset != 0 || sel3.Limit != 15 {
		t.Errorf("expected offset 0, limit 15, got offset %d, limit %d", sel3.Offset, sel3.Limit)
	}

	// 4. TOP n
	sql4 := `SELECT TOP 10 id, balance FROM accounts WHERE balance > 100;`
	stmt4, err := proc.ParseSQL(sql4)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel4 := stmt4.(*proc.GeneralSelectStmt)
	if sel4.Limit != 10 {
		t.Errorf("expected limit 10, got %d", sel4.Limit)
	}
	if len(sel4.Projections) != 2 {
		t.Errorf("expected 2 projections, got %d", len(sel4.Projections))
	}

	// 5. TOP (n) PERCENT WITH TIES
	sql5 := `SELECT TOP (25) PERCENT WITH TIES id FROM accounts;`
	stmt5, err := proc.ParseSQL(sql5)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel5 := stmt5.(*proc.GeneralSelectStmt)
	if sel5.Limit != 25 || !sel5.TopPercent || !sel5.TopWithTies {
		t.Errorf("expected limit 25, percent=true, ties=true, got %+v", sel5)
	}

	// 6. DISTINCT TOP n
	sql6 := `SELECT DISTINCT TOP 5 id FROM accounts;`
	stmt6, err := proc.ParseSQL(sql6)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel6 := stmt6.(*proc.GeneralSelectStmt)
	if sel6.Limit != 5 || !sel6.Distinct {
		t.Errorf("expected limit 5, distinct=true, got %+v", sel6)
	}
}

func TestParseSQL_CastAndConvert(t *testing.T) {
	// 1. CAST(expr AS type)
	sql1 := `SELECT CAST('12345' AS INT) AS num, CAST(100.5 AS VARCHAR(10)) AS str;`
	stmt1, err := proc.ParseSQL(sql1)
	if err != nil {
		t.Fatalf("ParseSQL failed for CAST: %v", err)
	}
	sel1 := stmt1.(*proc.GeneralSelectStmt)
	if len(sel1.Projections) != 2 {
		t.Fatalf("expected 2 projections, got %d", len(sel1.Projections))
	}
	cast1, ok := sel1.Projections[0].Expr.(*proc.CastExpr)
	if !ok || cast1.TargetType != storage.TypeInt || cast1.Try {
		t.Fatalf("unexpected cast1: %+v", cast1)
	}
	cast2, ok := sel1.Projections[1].Expr.(*proc.CastExpr)
	if !ok || cast2.TargetType != storage.TypeVarChar || cast2.Precision != 10 {
		t.Fatalf("unexpected cast2: %+v", cast2)
	}

	// 2. TRY_CAST
	sql2 := `SELECT TRY_CAST('invalid' AS INT) AS safe_num;`
	stmt2, err := proc.ParseSQL(sql2)
	if err != nil {
		t.Fatalf("ParseSQL failed for TRY_CAST: %v", err)
	}
	sel2 := stmt2.(*proc.GeneralSelectStmt)
	castTry, ok := sel2.Projections[0].Expr.(*proc.CastExpr)
	if !ok || !castTry.Try || castTry.TargetType != storage.TypeInt {
		t.Fatalf("unexpected try_cast: %+v", castTry)
	}

	// 3. CONVERT(type, expr, style)
	sql3 := `SELECT CONVERT(VARCHAR(20), '2026-10-11 22:00:00', 112) AS ymd, TRY_CONVERT(INT, '999') AS num;`
	stmt3, err := proc.ParseSQL(sql3)
	if err != nil {
		t.Fatalf("ParseSQL failed for CONVERT: %v", err)
	}
	sel3 := stmt3.(*proc.GeneralSelectStmt)
	conv1, ok := sel3.Projections[0].Expr.(*proc.CastExpr)
	if !ok || conv1.TargetType != storage.TypeVarChar || conv1.Style != 112 || conv1.Try {
		t.Fatalf("unexpected conv1: %+v", conv1)
	}
	conv2, ok := sel3.Projections[1].Expr.(*proc.CastExpr)
	if !ok || conv2.TargetType != storage.TypeInt || !conv2.Try {
		t.Fatalf("unexpected conv2: %+v", conv2)
	}

	// 4. PostgreSQL :: operator
	sql4 := `SELECT '42'::INT AS answer, 123::VARCHAR AS str;`
	stmt4, err := proc.ParseSQL(sql4)
	if err != nil {
		t.Fatalf("ParseSQL failed for :: operator: %v", err)
	}
	sel4 := stmt4.(*proc.GeneralSelectStmt)
	pgCast1, ok := sel4.Projections[0].Expr.(*proc.CastExpr)
	if !ok || pgCast1.TargetType != storage.TypeInt {
		t.Fatalf("unexpected pgCast1: %+v", pgCast1)
	}
	pgCast2, ok := sel4.Projections[1].Expr.(*proc.CastExpr)
	if !ok || pgCast2.TargetType != storage.TypeVarChar {
		t.Fatalf("unexpected pgCast2: %+v", pgCast2)
	}

	// 5. Evaluation test with EvalRowExpr
	v1, err := proc.EvalRowExpr(cast1, nil, nil)
	if err != nil || v1 != int64(12345) {
		t.Errorf("EvalRowExpr cast1 failed: val=%v, err=%v", v1, err)
	}

	// TRY_CAST invalid string returns nil (NULL) without error
	vTry, err := proc.EvalRowExpr(castTry, nil, nil)
	if err != nil || vTry != nil {
		t.Errorf("expected nil for failed TRY_CAST, got val=%v, err=%v", vTry, err)
	}

	// CONVERT date style 112 (YYYYMMDD)
	vConv, err := proc.EvalRowExpr(conv1, nil, nil)
	if err != nil || vConv != "20261011" {
		t.Errorf("expected 20261011 for style 112 CONVERT, got val=%v, err=%v", vConv, err)
	}

	// PostgreSQL :: evaluation
	vPg, err := proc.EvalRowExpr(pgCast1, nil, nil)
	if err != nil || vPg != int64(42) {
		t.Errorf("expected 42 for pgCast1, got val=%v, err=%v", vPg, err)
	}
}

func TestParseSQL_Apply_Hints_Output(t *testing.T) {
	// 1. CROSS APPLY with table hint and query hint
	sql1 := `SELECT e.id, ca.val FROM employees e WITH (NOLOCK) CROSS APPLY (SELECT val FROM details d WHERE d.emp_id = e.id) AS ca OPTION (RECOMPILE);`
	stmt1, err := proc.ParseSQL(sql1)
	if err != nil {
		t.Fatalf("ParseSQL failed for CROSS APPLY: %v", err)
	}
	sel1, ok := stmt1.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected GeneralSelectStmt, got %T", stmt1)
	}
	if len(sel1.From.Hints) == 0 || sel1.From.Hints[0] != "NOLOCK" {
		t.Errorf("expected NOLOCK hint, got %+v", sel1.From.Hints)
	}
	if len(sel1.Joins) != 1 || sel1.Joins[0].Type != "CROSS APPLY" {
		t.Fatalf("expected 1 CROSS APPLY join, got %+v", sel1.Joins)
	}
	if sel1.Joins[0].Table.Alias != "ca" || sel1.Joins[0].Table.Subquery == nil {
		t.Errorf("expected subquery alias ca, got %+v", sel1.Joins[0].Table)
	}
	if len(sel1.QueryHints) == 0 || sel1.QueryHints[0] != "RECOMPILE" {
		t.Errorf("expected RECOMPILE query hint, got %+v", sel1.QueryHints)
	}

	// 2. OUTER APPLY
	sql2 := `SELECT e.id, oa.val FROM employees e OUTER APPLY (SELECT val FROM details d WHERE d.emp_id = e.id) AS oa;`
	stmt2, err := proc.ParseSQL(sql2)
	if err != nil {
		t.Fatalf("ParseSQL failed for OUTER APPLY: %v", err)
	}
	sel2 := stmt2.(*proc.GeneralSelectStmt)
	if len(sel2.Joins) != 1 || sel2.Joins[0].Type != "OUTER APPLY" {
		t.Fatalf("expected 1 OUTER APPLY join, got %+v", sel2.Joins)
	}

	// 3. INSERT ... OUTPUT
	sql3 := `INSERT INTO accounts (id, bal) OUTPUT INSERTED.id, INSERTED.bal VALUES (1, 100);`
	stmt3, err := proc.ParseSQL(sql3)
	if err != nil {
		t.Fatalf("ParseSQL failed for INSERT OUTPUT: %v", err)
	}
	ins, ok := stmt3.(*proc.GeneralInsertStmt)
	if !ok {
		t.Fatalf("expected GeneralInsertStmt, got %T", stmt3)
	}
	if len(ins.OutputCols) != 2 || ins.OutputCols[0] != "INSERTED.id" {
		t.Errorf("unexpected OutputCols for INSERT: %+v", ins.OutputCols)
	}

	// 4. UPDATE ... OUTPUT
	sql4 := `UPDATE accounts SET bal = 200 OUTPUT DELETED.bal, INSERTED.bal WHERE id = 1;`
	stmt4, err := proc.ParseSQL(sql4)
	if err != nil {
		t.Fatalf("ParseSQL failed for UPDATE OUTPUT: %v", err)
	}
	upd, ok := stmt4.(*proc.UpdateStmt)
	if !ok {
		t.Fatalf("expected UpdateStmt, got %T", stmt4)
	}
	if len(upd.OutputCols) != 2 || upd.OutputCols[0] != "DELETED.bal" {
		t.Errorf("unexpected OutputCols for UPDATE: %+v", upd.OutputCols)
	}

	// 5. DELETE ... OUTPUT
	sql5 := `DELETE FROM accounts OUTPUT DELETED.id WHERE id = 1;`
	stmt5, err := proc.ParseSQL(sql5)
	if err != nil {
		t.Fatalf("ParseSQL failed for DELETE OUTPUT: %v", err)
	}
	del, ok := stmt5.(*proc.DeleteStmt)
	if !ok {
		t.Fatalf("expected DeleteStmt, got %T", stmt5)
	}
	if len(del.OutputCols) != 1 || del.OutputCols[0] != "DELETED.id" {
		t.Errorf("unexpected OutputCols for DELETE: %+v", del.OutputCols)
	}
}

func TestParseSQL_FullAndRightJoin(t *testing.T) {
	sql := `SELECT u.id, o.id FROM users u FULL OUTER JOIN orders o ON u.id = o.user_id;`
	stmt, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed for FULL OUTER JOIN: %v", err)
	}
	sel, ok := stmt.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected GeneralSelectStmt, got %T", stmt)
	}
	if len(sel.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(sel.Joins))
	}
	if sel.Joins[0].Type != "FULL" || sel.Joins[0].Table.Name != "orders" {
		t.Errorf("unexpected join in FULL OUTER JOIN: %+v", sel.Joins[0])
	}

	sqlRight := `SELECT u.id, o.id FROM users u RIGHT JOIN orders o ON u.id = o.user_id;`
	stmtRight, err := proc.ParseSQL(sqlRight)
	if err != nil {
		t.Fatalf("ParseSQL failed for RIGHT JOIN: %v", err)
	}
	selRight, ok := stmtRight.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected GeneralSelectStmt, got %T", stmtRight)
	}
	if len(selRight.Joins) != 1 || selRight.Joins[0].Type != "RIGHT" {
		t.Errorf("unexpected join in RIGHT JOIN: %+v", selRight.Joins[0])
	}
}

func TestParseSQL_CoalesceAndIsNull(t *testing.T) {
	sql := `SELECT COALESCE(price, 0.0) AS p, ISNULL(name, 'Unknown') AS n FROM items;`
	stmt, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed for COALESCE and ISNULL: %v", err)
	}
	sel, ok := stmt.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected GeneralSelectStmt, got %T", stmt)
	}
	if len(sel.Projections) != 2 {
		t.Fatalf("expected 2 projections, got %d", len(sel.Projections))
	}
	if sel.Projections[0].Alias != "p" || sel.Projections[1].Alias != "n" {
		t.Errorf("unexpected aliases: %+v", sel.Projections)
	}
}

func TestEvalRowExpr_NullPropagation(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want any
	}{
		{name: "string plus null", sql: "SELECT 'Hello ' + CAST(NULL AS VARCHAR(10))", want: nil},
		{name: "null plus string", sql: "SELECT CAST(NULL AS VARCHAR(10)) + 'World'", want: nil},
		{name: "int plus null", sql: "SELECT 10 + CAST(NULL AS INT)", want: nil},
		{name: "null plus int", sql: "SELECT CAST(NULL AS INT) + 20", want: nil},
		{name: "int minus null", sql: "SELECT 10 - CAST(NULL AS INT)", want: nil},
		{name: "null minus int", sql: "SELECT CAST(NULL AS INT) - 5", want: nil},
		{name: "int mul null", sql: "SELECT 10 * CAST(NULL AS INT)", want: nil},
		{name: "null div int", sql: "SELECT CAST(NULL AS INT) / 2", want: nil},
		{name: "int lt null", sql: "SELECT 10 < CAST(NULL AS INT)", want: nil},
		{name: "null gte int", sql: "SELECT CAST(NULL AS INT) >= 5", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := proc.ParseSQL(tt.sql)
			if err != nil {
				t.Fatalf("ParseSQL failed for %s: %v", tt.sql, err)
			}
			sel := stmt.(*proc.GeneralSelectStmt)
			val, err := proc.EvalRowExpr(sel.Projections[0].Expr, nil, nil)
			if err != nil {
				t.Fatalf("EvalRowExpr returned error for %s: %v", tt.sql, err)
			}
			if val != tt.want {
				t.Errorf("expected %v, got %v for %s", tt.want, val, tt.sql)
			}
		})
	}
}

func TestEvalRowExpr_CastBit(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want any
	}{
		{name: "cast int 1", sql: "SELECT CAST(1 AS BIT)", want: true},
		{name: "cast int 0", sql: "SELECT CAST(0 AS BIT)", want: false},
		{name: "cast int 100", sql: "SELECT CAST(100 AS BIT)", want: true},
		{name: "cast str 1", sql: "SELECT CAST('1' AS BIT)", want: true},
		{name: "cast str 0", sql: "SELECT CAST('0' AS BIT)", want: false},
		{name: "cast str true", sql: "SELECT CAST('true' AS BIT)", want: true},
		{name: "cast str false", sql: "SELECT CAST('false' AS BIT)", want: false},
		{name: "cast float nonzero", sql: "SELECT CAST(1.5 AS BIT)", want: true},
		{name: "cast null", sql: "SELECT CAST(NULL AS BIT)", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := proc.ParseSQL(tt.sql)
			if err != nil {
				t.Fatalf("ParseSQL failed for %s: %v", tt.sql, err)
			}
			sel := stmt.(*proc.GeneralSelectStmt)
			val, err := proc.EvalRowExpr(sel.Projections[0].Expr, nil, nil)
			if err != nil {
				t.Fatalf("EvalRowExpr returned error for %s: %v", tt.sql, err)
			}
			if val != tt.want {
				t.Errorf("expected %v, got %v for %s", tt.want, val, tt.sql)
			}
		})
	}
}




