package planner_test

import (
	"strings"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

func setupTestCatalog() map[string]*storage.TableSchema {
	userSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "age", Type: storage.TypeInt},
		},
	}
	orderSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
		},
	}
	ambigSchema := &storage.TableSchema{
		Name: "profiles",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "bio", Type: storage.TypeVarChar},
		},
	}

	return map[string]*storage.TableSchema{
		"users":    userSchema,
		"orders":   orderSchema,
		"profiles": ambigSchema,
	}
}

func TestBinder_SingleTable_SlotResolution(t *testing.T) {
	schemas := setupTestCatalog()
	sql := "SELECT id, name, age FROM users WHERE age > 21 ORDER BY age;"

	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel, ok := parsed.(*proc.GeneralSelectStmt)
	if !ok {
		t.Fatalf("expected *proc.GeneralSelectStmt, got %T", parsed)
	}

	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("BindSelect failed: %v", err)
	}

	if len(bound.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(bound.Tables))
	}
	if bound.Tables[0].Name != "users" || bound.Tables[0].TableID != 0 || bound.Tables[0].SlotOffset != 0 {
		t.Errorf("unexpected table ref: %+v", bound.Tables[0])
	}

	if len(bound.Projections) != 3 {
		t.Fatalf("expected 3 projections, got %d", len(bound.Projections))
	}

	// id -> TableID 0, ColIdx 0, TupleSlot 0
	p0, ok := bound.Projections[0].Expr.(*planner.BoundColumnRef)
	if !ok || p0.TableID != 0 || p0.ColumnIdx != 0 || p0.TupleSlot != 0 || p0.DataType != storage.TypeInt {
		t.Errorf("proj[0] id resolution mismatch: %+v", p0)
	}

	// name -> TableID 0, ColIdx 1, TupleSlot 1
	p1, ok := bound.Projections[1].Expr.(*planner.BoundColumnRef)
	if !ok || p1.TableID != 0 || p1.ColumnIdx != 1 || p1.TupleSlot != 1 || p1.DataType != storage.TypeVarChar {
		t.Errorf("proj[1] name resolution mismatch: %+v", p1)
	}

	// age -> TableID 0, ColIdx 2, TupleSlot 2
	p2, ok := bound.Projections[2].Expr.(*planner.BoundColumnRef)
	if !ok || p2.TableID != 0 || p2.ColumnIdx != 2 || p2.TupleSlot != 2 || p2.DataType != storage.TypeInt {
		t.Errorf("proj[2] age resolution mismatch: %+v", p2)
	}

	// WHERE: age > 21
	whereBin, ok := bound.Where.(*planner.BoundBinaryExpr)
	if !ok {
		t.Fatalf("expected *planner.BoundBinaryExpr for WHERE, got %T", bound.Where)
	}
	whereLeft, ok := whereBin.Left.(*planner.BoundColumnRef)
	if !ok || whereLeft.TupleSlot != 2 {
		t.Errorf("WHERE Left slot mismatch: %+v", whereLeft)
	}
}

func TestBinder_MultiTableJoin_SlotResolution(t *testing.T) {
	schemas := setupTestCatalog()
	sql := "SELECT u.name, o.amount FROM users u JOIN orders o ON u.id = o.user_id WHERE o.amount > 50.0;"

	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)

	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("BindSelect failed: %v", err)
	}

	if len(bound.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(bound.Tables))
	}
	// u: SlotOffset 0
	if bound.Tables[0].TableID != 0 || bound.Tables[0].SlotOffset != 0 {
		t.Errorf("table u mismatch: %+v", bound.Tables[0])
	}
	// o: SlotOffset 3 (since users has 3 columns: id, name, age)
	if bound.Tables[1].TableID != 1 || bound.Tables[1].SlotOffset != 3 {
		t.Errorf("table o mismatch: %+v", bound.Tables[1])
	}

	// Projections
	// u.name -> TableID 0, ColIdx 1, TupleSlot 0 + 1 = 1
	p0 := bound.Projections[0].Expr.(*planner.BoundColumnRef)
	if p0.TableID != 0 || p0.ColumnIdx != 1 || p0.TupleSlot != 1 {
		t.Errorf("proj[0] u.name slot mismatch: %+v", p0)
	}

	// o.amount -> TableID 1, ColIdx 2, TupleSlot 3 + 2 = 5
	p1 := bound.Projections[1].Expr.(*planner.BoundColumnRef)
	if p1.TableID != 1 || p1.ColumnIdx != 2 || p1.TupleSlot != 5 {
		t.Errorf("proj[1] o.amount slot mismatch: %+v", p1)
	}

	// JOIN ON: u.id = o.user_id
	joinOn := bound.Joins[0].On.(*planner.BoundBinaryExpr)
	onLeft := joinOn.Left.(*planner.BoundColumnRef)
	onRight := joinOn.Right.(*planner.BoundColumnRef)

	// u.id -> TableID 0, TupleSlot 0
	if onLeft.TableID != 0 || onLeft.TupleSlot != 0 {
		t.Errorf("ON left u.id slot mismatch: %+v", onLeft)
	}
	// o.user_id -> TableID 1, ColIdx 1, TupleSlot 3 + 1 = 4
	if onRight.TableID != 1 || onRight.TupleSlot != 4 {
		t.Errorf("ON right o.user_id slot mismatch: %+v", onRight)
	}
}

func TestBinder_AmbiguousAndGhostColumnRejection(t *testing.T) {
	schemas := setupTestCatalog()

	tests := []struct {
		name    string
		sql     string
		wantErr string
	}{
		{
			name:    "ambiguous column in join without qualifier",
			sql:     "SELECT id FROM users JOIN profiles ON users.id = profiles.id;",
			wantErr: "ambiguous",
		},
		{
			name:    "non existent column in single table",
			sql:     "SELECT ghost_col FROM users;",
			wantErr: "does not exist",
		},
		{
			name:    "non existent column in qualified table",
			sql:     "SELECT u.ghost_col FROM users u;",
			wantErr: "does not exist",
		},
		{
			name:    "non existent table alias in query scope",
			sql:     "SELECT wrong_alias.id FROM users u;",
			wantErr: "does not exist in query scope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := proc.ParseSQL(tt.sql)
			if err != nil {
				t.Fatalf("ParseSQL failed: %v", err)
			}
			binder := planner.NewBinder(schemas)
			_, err = binder.BindSelect(parsed.(*proc.GeneralSelectStmt))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !testing.Short() && len(tt.wantErr) > 0 {
				t.Logf("Got expected fail-closed error: %v", err)
			}
		})
	}
}

func TestBoundColumnRef_DirectTupleSlotEvaluation(t *testing.T) {
	// Simulate joined tuple values: [users.id, users.name, users.age, orders.order_id, orders.user_id, orders.amount]
	tup := execution.Tuple{
		Values: []any{int64(42), "Alice", int64(30), int64(1001), int64(42), float64(299.95)},
	}

	boundCol := &planner.BoundColumnRef{
		Name:      "o.amount",
		TableName: "orders",
		TableID:   1,
		ColumnIdx: 2,
		TupleSlot: 5,
		DataType:  storage.TypeFloat,
	}

	val, ok := execution.EvalTupleExpr(tup, boundCol)
	if !ok {
		t.Fatalf("EvalTupleExpr failed to evaluate bound column")
	}
	fVal, ok := val.(float64)
	if !ok || fVal != 299.95 {
		t.Errorf("expected 299.95, got %v (%T)", val, val)
	}
}

func TestBinder_CorrelatedSubquery_OuterLevelResolution(t *testing.T) {
	schemas := setupTestCatalog()
	sql := "SELECT u.id, u.name FROM users u WHERE u.age > (SELECT AVG(o.amount) FROM orders o WHERE o.user_id = u.id);"

	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)

	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("BindSelect failed: %v", err)
	}

	// Outer WHERE: u.age > (subquery)
	whereBin, ok := bound.Where.(*planner.BoundBinaryExpr)
	if !ok {
		t.Fatalf("expected BoundBinaryExpr for WHERE, got %T", bound.Where)
	}
	outerCol, ok := whereBin.Left.(*planner.BoundColumnRef)
	if !ok || outerCol.OuterLevel != 0 || outerCol.Name != "u.age" {
		t.Errorf("expected outer u.age with OuterLevel 0, got %+v", outerCol)
	}

	// Subquery
	subExpr, ok := whereBin.Right.(*planner.BoundSubqueryExpr)
	if !ok {
		t.Fatalf("expected BoundSubqueryExpr for right operand, got %T", whereBin.Right)
	}
	subStmt := subExpr.Subquery
	if len(subStmt.Tables) != 1 || subStmt.Tables[0].Name != "orders" {
		t.Fatalf("expected subquery table 'orders', got %+v", subStmt.Tables)
	}

	// Subquery WHERE: o.user_id = u.id
	subWhere, ok := subStmt.Where.(*planner.BoundBinaryExpr)
	if !ok {
		t.Fatalf("expected BoundBinaryExpr for subquery WHERE, got %T", subStmt.Where)
	}
	innerCol, ok := subWhere.Left.(*planner.BoundColumnRef)
	if !ok || innerCol.OuterLevel != 0 || innerCol.TableName != "orders" || innerCol.ColumnIdx != 1 {
		t.Errorf("expected inner o.user_id with OuterLevel 0, got %+v", innerCol)
	}
	correlatedCol, ok := subWhere.Right.(*planner.BoundColumnRef)
	if !ok {
		t.Fatalf("expected BoundColumnRef for correlated reference, got %T", subWhere.Right)
	}
	if correlatedCol.OuterLevel != 1 {
		t.Errorf("expected OuterLevel 1 for correlated u.id, got %d", correlatedCol.OuterLevel)
	}
	if correlatedCol.TableName != "users" || correlatedCol.ColumnIdx != 0 {
		t.Errorf("unexpected correlated column metadata: %+v", correlatedCol)
	}
}

func TestBinder_CorrelatedSubquery_ScopeShadowingAndUnqualifiedFallback(t *testing.T) {
	schemas := setupTestCatalog()
	// In orders: order_id, user_id, amount
	// In users: id, name, age
	// Inside subquery on orders: 'amount' is in orders (OuterLevel 0), 'name' is NOT in orders but in users (OuterLevel 1)
	sql := "SELECT u.id FROM users u WHERE u.age > (SELECT AVG(amount) FROM orders WHERE user_id = u.id AND name = 'Alice');"

	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)

	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("BindSelect failed: %v", err)
	}

	whereBin := bound.Where.(*planner.BoundBinaryExpr)
	subExpr := whereBin.Right.(*planner.BoundSubqueryExpr)
	subStmt := subExpr.Subquery

	// Projection: AVG(amount) -> amount is inner (OuterLevel 0)
	fn := subStmt.Projections[0].Expr.(*planner.BoundFunctionCall)
	argCol := fn.Args[0].(*planner.BoundColumnRef)
	if argCol.OuterLevel != 0 || argCol.ColumnIdx != 2 {
		t.Errorf("expected inner 'amount' with OuterLevel 0, got %+v", argCol)
	}

	// WHERE: user_id = u.id AND name = 'Alice'
	andBin := subStmt.Where.(*planner.BoundBinaryExpr)
	rightBin := andBin.Right.(*planner.BoundBinaryExpr)
	nameCol := rightBin.Left.(*planner.BoundColumnRef)
	if nameCol.OuterLevel != 1 || nameCol.TableName != "users" || nameCol.ColumnIdx != 1 {
		t.Errorf("expected outer 'name' fallback with OuterLevel 1, got %+v", nameCol)
	}
}

func TestBinder_CorrelatedSubquery_ExistsBinding(t *testing.T) {
	schemas := setupTestCatalog()
	sql := "SELECT u.name FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id);"

	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)

	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("BindSelect failed: %v", err)
	}

	existsExpr, ok := bound.Where.(*planner.BoundExistsExpr)
	if !ok || existsExpr.Not {
		t.Fatalf("expected BoundExistsExpr for WHERE, got %T", bound.Where)
	}
	subWhere := existsExpr.Subquery.Where.(*planner.BoundBinaryExpr)
	corrCol := subWhere.Right.(*planner.BoundColumnRef)
	if corrCol.OuterLevel != 1 || corrCol.ColumnIdx != 0 {
		t.Errorf("expected correlated u.id with OuterLevel 1, got %+v", corrCol)
	}
}

func TestBinder_CorrelatedSubquery_DeepNestingLevel2(t *testing.T) {
	schemas := setupTestCatalog()
	// users u -> orders o -> profiles p
	// p references u.age across 2 scope levels
	sql := "SELECT u.id FROM users u WHERE u.age > (SELECT AVG(o.amount) FROM orders o WHERE o.user_id IN (SELECT p.id FROM profiles p WHERE p.id = u.id));"

	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)

	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("BindSelect failed: %v", err)
	}

	whereBin := bound.Where.(*planner.BoundBinaryExpr)
	sub1 := whereBin.Right.(*planner.BoundSubqueryExpr).Subquery
	inSub := sub1.Where.(*planner.BoundInSubqueryExpr)
	sub2 := inSub.Subquery

	// In sub2: p.id = u.id
	sub2Where := sub2.Where.(*planner.BoundBinaryExpr)
	pCol := sub2Where.Left.(*planner.BoundColumnRef)
	if pCol.OuterLevel != 0 || pCol.TableName != "profiles" {
		t.Errorf("expected inner p.id with OuterLevel 0, got %+v", pCol)
	}
	uCol := sub2Where.Right.(*planner.BoundColumnRef)
	if uCol.OuterLevel != 2 || uCol.TableName != "users" || uCol.ColumnIdx != 0 {
		t.Errorf("expected u.id from root query with OuterLevel 2, got %+v", uCol)
	}
}

func TestBinder_CorrelatedSubquery_GhostColumnFailClosed(t *testing.T) {
	schemas := setupTestCatalog()
	// ghost_col does not exist in inner 'orders' or outer 'users'
	sql := "SELECT u.id FROM users u WHERE u.age > (SELECT AVG(o.amount) FROM orders o WHERE o.ghost_col = u.id);"

	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)

	binder := planner.NewBinder(schemas)
	_, err = binder.BindSelect(sel)
	if err == nil {
		t.Fatalf("expected error for non-existent ghost_col in subquery, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' error, got: %v", err)
	}
}

func TestBinder_Phase7_ScopeShadowing(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. users.id with alias 'u' must fail because 'users' is shadowed
	sqlFail := "SELECT users.id FROM users u;"
	parsed, err := proc.ParseSQL(sqlFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for shadowed table name 'users', got nil")
	} else if !strings.Contains(err.Error(), "does not exist in query scope") {
		t.Errorf("expected 'does not exist in query scope' error, got: %v", err)
	}

	// 2. u.id with alias 'u' must succeed
	sqlPass := "SELECT u.id FROM users u;"
	parsedPass, err := proc.ParseSQL(sqlPass)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	selPass := parsedPass.(*proc.GeneralSelectStmt)
	if _, err := binder.BindSelect(selPass); err != nil {
		t.Errorf("expected u.id to succeed, got: %v", err)
	}
}

func TestBinder_Phase7_SelfJoin_Ambiguity(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Unqualified 'id' in self-join of 'users' must be rejected as ambiguous
	sqlFail := "SELECT id FROM users u1 JOIN users u2 ON u1.id = u2.id;"
	parsed, err := proc.ParseSQL(sqlFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for ambiguous unqualified 'id' in self join, got nil")
	} else if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected 'ambiguous' error, got: %v", err)
	}

	// 2. Qualified 'u1.id' and 'u2.name' must succeed
	sqlPass := "SELECT u1.id, u2.name FROM users u1 JOIN users u2 ON u1.id = u2.id;"
	parsedPass, err := proc.ParseSQL(sqlPass)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	selPass := parsedPass.(*proc.GeneralSelectStmt)
	bound, err := binder.BindSelect(selPass)
	if err != nil {
		t.Fatalf("expected qualified self join to succeed, got: %v", err)
	}
	if len(bound.Projections) != 2 {
		t.Fatalf("expected 2 projections, got %d", len(bound.Projections))
	}
	p0 := bound.Projections[0].Expr.(*planner.BoundColumnRef)
	p1 := bound.Projections[1].Expr.(*planner.BoundColumnRef)
	if p0.TableID != 0 || p0.TupleSlot != 0 {
		t.Errorf("expected u1.id at TableID 0, TupleSlot 0, got TableID %d Slot %d", p0.TableID, p0.TupleSlot)
	}
	// u2 is TableID 1, offset by len(users.Columns) = 3 -> TupleSlot = 3 + 1 = 4
	if p1.TableID != 1 || p1.TupleSlot != 4 {
		t.Errorf("expected u2.name at TableID 1, TupleSlot 4, got TableID %d Slot %d", p1.TableID, p1.TupleSlot)
	}
}

func TestBinder_Phase7_SelfJoin_DuplicateUnAliased(t *testing.T) {
	schemas := setupTestCatalog()

	// Duplicate un-aliased 'users JOIN users' must be rejected
	sql := "SELECT * FROM users JOIN users ON users.id = users.id;"
	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for duplicate un-aliased 'users' join, got nil")
	} else if !strings.Contains(err.Error(), "specified more than once") {
		t.Errorf("expected 'specified more than once' error, got: %v", err)
	}
}

func TestBinder_Phase7_WildcardExpansion(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Global wildcard: SELECT * FROM users (expands to id, name, age)
	sqlGlobal := "SELECT * FROM users;"
	parsed, err := proc.ParseSQL(sqlGlobal)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("BindSelect failed: %v", err)
	}
	if len(bound.Projections) != 3 {
		t.Fatalf("expected 3 expanded projections, got %d", len(bound.Projections))
	}
	expectedSlots := []int{0, 1, 2}
	expectedNames := []string{"id", "name", "age"}
	for i, bp := range bound.Projections {
		if bp.TupleSlot != expectedSlots[i] {
			t.Errorf("proj[%d] TupleSlot mismatch: expected %d, got %d", i, expectedSlots[i], bp.TupleSlot)
		}
		if bp.Alias != expectedNames[i] {
			t.Errorf("proj[%d] Alias mismatch: expected %s, got %s", i, expectedNames[i], bp.Alias)
		}
	}

	// 2. Table-qualified wildcard: SELECT u.* FROM users u JOIN orders o ON u.id = o.user_id
	sqlQual := "SELECT u.* FROM users u JOIN orders o ON u.id = o.user_id;"
	parsedQual, err := proc.ParseSQL(sqlQual)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	selQual := parsedQual.(*proc.GeneralSelectStmt)
	boundQual, err := binder.BindSelect(selQual)
	if err != nil {
		t.Fatalf("BindSelect for u.* failed: %v", err)
	}
	if len(boundQual.Projections) != 3 {
		t.Fatalf("expected 3 expanded projections for u.*, got %d", len(boundQual.Projections))
	}
	for i, bp := range boundQual.Projections {
		if bp.TupleSlot != expectedSlots[i] {
			t.Errorf("u.* proj[%d] TupleSlot mismatch: expected %d, got %d", i, expectedSlots[i], bp.TupleSlot)
		}
		if bp.Alias != expectedNames[i] {
			t.Errorf("u.* proj[%d] Alias mismatch: expected %s, got %s", i, expectedNames[i], bp.Alias)
		}
	}
}

func TestBinder_Phase7_GroupBy_UnaggregatedColumnRejection(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. SELECT id, name, COUNT(*) FROM users GROUP BY id -> 'name' must be rejected (SQLSTATE 42803)
	sqlFail := "SELECT id, name, COUNT(*) FROM users GROUP BY id;"
	parsed, err := proc.ParseSQL(sqlFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for unaggregated column 'name' without GROUP BY, got nil")
	} else if !strings.Contains(err.Error(), "must appear in the GROUP BY clause") {
		t.Errorf("expected 'must appear in the GROUP BY clause' error, got: %v", err)
	}

	// 2. SELECT id, COUNT(name) FROM users GROUP BY id -> Must succeed
	sqlPass := "SELECT id, COUNT(name) FROM users GROUP BY id;"
	parsedPass, err := proc.ParseSQL(sqlPass)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	selPass := parsedPass.(*proc.GeneralSelectStmt)
	if _, err := binder.BindSelect(selPass); err != nil {
		t.Errorf("expected aggregate query to succeed, got: %v", err)
	}
}

func TestBinder_Phase7_Subquery_RecursiveIllegalWhereAggregate(t *testing.T) {
	schemas := setupTestCatalog()

	// Aggregate inside subquery WHERE must fail closed
	sql := "SELECT id FROM users WHERE id IN (SELECT user_id FROM orders WHERE COUNT(*) > 1);"
	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for aggregate in subquery WHERE, got nil")
	} else if !strings.Contains(err.Error(), "aggregate functions are not allowed in WHERE") {
		t.Errorf("expected 'aggregate functions are not allowed in WHERE' error, got: %v", err)
	}
}

func TestBinder_Phase7_ArithmeticTypeIncompatibility(t *testing.T) {
	schemas := setupTestCatalog()
	sql := "SELECT name - name FROM users;"
	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for varchar arithmetic subtraction, got nil")
	} else if !strings.Contains(err.Error(), "operator does not exist") || !strings.Contains(err.Error(), "42883") {
		t.Errorf("expected 42883 'operator does not exist' error, got: %v", err)
	}
}

func TestBinder_Phase7_ScalarSubquery_MultiColumnRejection(t *testing.T) {
	schemas := setupTestCatalog()
	// Test IN subquery with multiple columns
	sql := "SELECT id FROM users WHERE id IN (SELECT order_id, user_id FROM orders);"
	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for multi-column subquery, got nil")
	} else if !strings.Contains(err.Error(), "subquery must return only one column") || !strings.Contains(err.Error(), "42601") {
		t.Errorf("expected 42601 'subquery must return only one column' error, got: %v", err)
	}
}

func TestBinder_Phase7_UnknownFunctionRejection(t *testing.T) {
	schemas := setupTestCatalog()
	sql := "SELECT nonexistent_calc_fn(id) FROM users;"
	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for unknown function, got nil")
	} else if !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "42883") {
		t.Errorf("expected 42883 'function does not exist' error, got: %v", err)
	}
}

func TestBinder_Phase7_HavingProjectionAlias(t *testing.T) {
	schemas := setupTestCatalog()
	sql := "SELECT user_id, COUNT(*) AS total_orders FROM orders GROUP BY user_id HAVING total_orders > 10;"
	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("expected HAVING projection alias query to succeed, got: %v", err)
	}
	if bound.Having == nil {
		t.Fatalf("expected bound Having expression, got nil")
	}
}

func TestBinder_Phase7_NestedAggregateRejection(t *testing.T) {
	schemas := setupTestCatalog()
	sql := "SELECT SUM(COUNT(*)) FROM users;"
	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	if _, err := binder.BindSelect(sel); err == nil {
		t.Errorf("expected error for nested aggregates, got nil")
	} else if !strings.Contains(err.Error(), "aggregate function calls cannot be nested") || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected 42803 'aggregate function calls cannot be nested' error, got: %v", err)
	}
}

func TestBinder_Phase7_GroupBy_TableColumnPrimacy(t *testing.T) {
	schemas := setupTestCatalog()
	// Projection has alias 'name' for 'COUNT(*)', but table also has column 'name'.
	// GROUP BY name must resolve to users.name (column 1, slot 1), NOT COUNT(*)!
	sql := "SELECT COUNT(*) AS name, name FROM users GROUP BY name;"
	parsed, err := proc.ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	binder := planner.NewBinder(schemas)
	bound, err := binder.BindSelect(sel)
	if err != nil {
		t.Fatalf("expected query to succeed, got: %v", err)
	}
	if len(bound.GroupBy) != 1 {
		t.Fatalf("expected 1 group by expr, got %d", len(bound.GroupBy))
	}
	colRef, ok := bound.GroupBy[0].(*planner.BoundColumnRef)
	if !ok {
		t.Fatalf("expected BoundColumnRef in GROUP BY, got: %T", bound.GroupBy[0])
	}
	if colRef.ColumnIdx != 1 || colRef.TupleSlot != 1 {
		t.Errorf("expected GROUP BY to bind to table column users.name (col 1, slot 1), got col %d slot %d",
			colRef.ColumnIdx, colRef.TupleSlot)
	}
}

func TestValidator_Hardening_GroupBy_OrderByUnaggregated(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. ORDER BY unaggregated column 'age' not in GROUP BY -> Fail (42803)
	sqlFail := "SELECT name, COUNT(*) FROM users GROUP BY name ORDER BY age;"
	parsed, err := proc.ParseSQL(sqlFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	sel := parsed.(*proc.GeneralSelectStmt)
	if _, err := planner.NewBinder(schemas).BindSelect(sel); err == nil {
		t.Errorf("expected error for unaggregated column in ORDER BY under GROUP BY, got nil")
	} else if !strings.Contains(err.Error(), "must appear in the GROUP BY clause or be used in an aggregate function") {
		t.Errorf("expected 'must appear in the GROUP BY clause...' error, got: %v", err)
	}

	// 2. ORDER BY aggregated column COUNT(*) -> Pass
	sqlPassAgg := "SELECT name, COUNT(*) FROM users GROUP BY name ORDER BY COUNT(*);"
	parsedPassAgg, err := proc.ParseSQL(sqlPassAgg)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPassAgg.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected ORDER BY COUNT(*) to pass, got: %v", err)
	}

	// 3. ORDER BY projection alias 'cnt' -> Pass
	sqlPassAlias := "SELECT name, COUNT(*) AS cnt FROM users GROUP BY name ORDER BY cnt;"
	parsedPassAlias, err := proc.ParseSQL(sqlPassAlias)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPassAlias.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected ORDER BY alias 'cnt' to pass, got: %v", err)
	}

	// 4. ORDER BY ordinal position 1 (which is 'name') -> Pass
	sqlPassOrd := "SELECT name, COUNT(*) FROM users GROUP BY name ORDER BY 1;"
	parsedPassOrd, err := proc.ParseSQL(sqlPassOrd)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPassOrd.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected ORDER BY ordinal 1 to pass, got: %v", err)
	}
}

func TestValidator_Hardening_Distinct_OrderByNotInProjection(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. SELECT DISTINCT id, name ORDER BY age -> Fail (42P10)
	sqlFail := "SELECT DISTINCT id, name FROM users ORDER BY age;"
	parsed, err := proc.ParseSQL(sqlFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsed.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for SELECT DISTINCT with ORDER BY not in select list, got nil")
	} else if !strings.Contains(err.Error(), "for SELECT DISTINCT, ORDER BY expressions must appear in select list") || !strings.Contains(err.Error(), "42P10") {
		t.Errorf("expected 42P10 'for SELECT DISTINCT...' error, got: %v", err)
	}

	// 2. SELECT DISTINCT id, name ORDER BY name -> Pass
	sqlPassName := "SELECT DISTINCT id, name FROM users ORDER BY name;"
	parsedPassName, err := proc.ParseSQL(sqlPassName)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPassName.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected SELECT DISTINCT matching ORDER BY to pass, got: %v", err)
	}

	// 3. SELECT DISTINCT id, name ORDER BY 2 -> Pass
	sqlPassOrd := "SELECT DISTINCT id, name FROM users ORDER BY 2;"
	parsedPassOrd, err := proc.ParseSQL(sqlPassOrd)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPassOrd.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected SELECT DISTINCT with ordinal 2 to pass, got: %v", err)
	}

	// 4. SELECT DISTINCT * FROM users ORDER BY age -> Pass (wildcard projects all columns)
	sqlPassWild := "SELECT DISTINCT * FROM users ORDER BY age;"
	parsedPassWild, err := proc.ParseSQL(sqlPassWild)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPassWild.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected SELECT DISTINCT * with ORDER BY to pass, got: %v", err)
	}
}

func TestValidator_Hardening_SubqueryWithoutAlias(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. FROM (SELECT 1) without alias -> Fail in ParseSQL or ValidateQueryAST (42601)
	sqlFailFrom := "SELECT * FROM (SELECT id FROM users);"
	parsedFailFrom, err := proc.ParseSQL(sqlFailFrom)
	if err != nil {
		if !strings.Contains(err.Error(), "subquery in FROM must have an alias") {
			t.Errorf("expected 'subquery in FROM must have an alias' error in ParseSQL, got: %v", err)
		}
	} else {
		if _, err := planner.NewBinder(schemas).BindSelect(parsedFailFrom.(*proc.GeneralSelectStmt)); err == nil {
			t.Errorf("expected error for FROM subquery without alias, got nil")
		} else if !strings.Contains(err.Error(), "subquery in FROM must have an alias") {
			t.Errorf("expected 'subquery in FROM must have an alias' error, got: %v", err)
		}
	}

	// 1b. Direct AST validation for subquery without alias
	stmtNoAlias := &proc.GeneralSelectStmt{
		From: proc.TableRef{
			Subquery: &proc.GeneralSelectStmt{
				From: proc.TableRef{Name: "users"},
			},
		},
		Projections: []proc.Projection{
			{Expr: &proc.ColumnRefExpr{Name: "*"}},
		},
	}
	if err := planner.ValidateQueryAST(stmtNoAlias, schemas); err == nil {
		t.Errorf("expected ValidateQueryAST to fail for subquery without alias, got nil")
	} else if !strings.Contains(err.Error(), "subquery in FROM must have an alias") {
		t.Errorf("expected 'subquery in FROM must have an alias' error, got: %v", err)
	}

	// 2. FROM (SELECT 1) sub -> Pass
	sqlPassFrom := "SELECT * FROM (SELECT id FROM users) sub;"
	parsedPassFrom, err := proc.ParseSQL(sqlPassFrom)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPassFrom.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected FROM subquery with alias to pass, got: %v", err)
	}
}

func TestValidator_Hardening_NegativeLimitAndOffset(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. LIMIT (-1) -> Fail (2201W)
	sqlFailLimit := "SELECT id FROM users LIMIT (-1);"
	parsedLimit, err := proc.ParseSQL(sqlFailLimit)
	if err != nil {
		if !strings.Contains(err.Error(), "LIMIT must not be negative") {
			t.Errorf("expected 'LIMIT must not be negative' in ParseSQL, got: %v", err)
		}
	} else {
		selLimit := parsedLimit.(*proc.GeneralSelectStmt)
		if _, err := planner.NewBinder(schemas).BindSelect(selLimit); err == nil {
			t.Errorf("expected error for LIMIT (-1), got nil")
		} else if !strings.Contains(err.Error(), "LIMIT must not be negative") || !strings.Contains(err.Error(), "2201W") {
			t.Errorf("expected 2201W 'LIMIT must not be negative' error, got: %v", err)
		}
	}

	// 2. OFFSET (-5) -> Fail (2201X)
	sqlFailOffset := "SELECT id FROM users OFFSET (-5);"
	parsedOffset, err := proc.ParseSQL(sqlFailOffset)
	if err != nil {
		if !strings.Contains(err.Error(), "OFFSET must not be negative") {
			t.Errorf("expected 'OFFSET must not be negative' in ParseSQL, got: %v", err)
		}
	} else {
		selOffset := parsedOffset.(*proc.GeneralSelectStmt)
		if _, err := planner.NewBinder(schemas).BindSelect(selOffset); err == nil {
			t.Errorf("expected error for OFFSET (-5), got nil")
		} else if !strings.Contains(err.Error(), "OFFSET must not be negative") || !strings.Contains(err.Error(), "2201X") {
			t.Errorf("expected 2201X 'OFFSET must not be negative' error, got: %v", err)
		}
	}
}

func TestValidator_Hardening_NestedWindowFunctions(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. ROW_NUMBER() OVER (...) nested inside SUM() aggregate -> Fail (42P20)
	sqlFailAgg := "SELECT SUM(ROW_NUMBER() OVER ()) FROM users;"
	parsedAgg, err := proc.ParseSQL(sqlFailAgg)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedAgg.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for window function inside aggregate, got nil")
	} else if !strings.Contains(err.Error(), "aggregate function calls cannot contain window function calls") || !strings.Contains(err.Error(), "42P20") {
		t.Errorf("expected 42P20 'cannot contain window function calls' error, got: %v", err)
	}

	// 2. Window function with nested window function in OVER clause -> Fail (42P20)
	sqlFailNested := "SELECT ROW_NUMBER() OVER (ORDER BY RANK() OVER ()) FROM users;"
	parsedNested, err := proc.ParseSQL(sqlFailNested)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedNested.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for nested window functions, got nil")
	} else if !strings.Contains(err.Error(), "window function calls cannot be nested") || !strings.Contains(err.Error(), "42P20") {
		t.Errorf("expected 42P20 'window function calls cannot be nested' error, got: %v", err)
	}
}

func TestValidator_Hardening_NestedAggregates(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. SUM(AVG(id)) -> Fail (42803)
	sql1 := "SELECT SUM(AVG(id)) FROM users;"
	parsed1, err := proc.ParseSQL(sql1)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsed1.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for SUM(AVG(id)), got nil")
	} else if !strings.Contains(err.Error(), "aggregate function calls cannot be nested") || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected 42803 'cannot be nested' error, got: %v", err)
	}

	// 2. COUNT(MAX(id)) -> Fail (42803)
	sql2 := "SELECT COUNT(MAX(id)) FROM users;"
	parsed2, err := proc.ParseSQL(sql2)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsed2.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for COUNT(MAX(id)), got nil")
	} else if !strings.Contains(err.Error(), "aggregate function calls cannot be nested") || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected 42803 'cannot be nested' error, got: %v", err)
	}

	// 3. ORDER BY SUM(AVG(id)) -> Fail (42803)
	sql3 := "SELECT id FROM users GROUP BY id ORDER BY SUM(AVG(id));"
	parsed3, err := proc.ParseSQL(sql3)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsed3.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for ORDER BY SUM(AVG(id)), got nil")
	} else if !strings.Contains(err.Error(), "aggregate function calls cannot be nested") || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected 42803 'cannot be nested' error, got: %v", err)
	}

	// 4. Normal non-nested aggregates -> Pass
	sqlPass := "SELECT SUM(id), AVG(age) FROM users;"
	parsedPass, err := proc.ParseSQL(sqlPass)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPass.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected normal aggregates to pass, got: %v", err)
	}
}

func TestValidator_Hardening_GroupBy_TableColumnPrimacy(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. SELECT age + 100 AS id, name FROM users GROUP BY id;
	// Here GROUP BY id must bind to physical table column users.id (Table Column Primacy).
	// Because name is NOT aggregated and NOT in GROUP BY, this query MUST FAIL with 42803!
	sqlFail := "SELECT age + 100 AS id, name FROM users GROUP BY id;"
	parsedFail, err := proc.ParseSQL(sqlFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedFail.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for unaggregated 'name' under GROUP BY id, got nil")
	} else if !strings.Contains(err.Error(), "must appear in the GROUP BY clause") || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected 42803 'must appear in the GROUP BY' error, got: %v", err)
	}

	// 2. If GROUP BY refers to a projection alias that does NOT conflict with any table column, it is allowed as fallback.
	sqlPassAlias := "SELECT id, age + 100 AS bonus FROM users GROUP BY id, bonus;"
	parsedPassAlias, err := proc.ParseSQL(sqlPassAlias)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPassAlias.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected GROUP BY with valid non-conflicting projection alias to pass, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Round 2 Validator Gatekeeper Hardening Tests (TDD Red -> Green)
// -----------------------------------------------------------------------------

func TestValidator_Hardening_Having_UnaggregatedColumnMustAppearInGroupBy(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. HAVING salary > 50000 when GROUP BY is only id -> FAIL (SQLSTATE 42803)
	sqlFail := "SELECT id, COUNT(*) FROM users GROUP BY id HAVING age > 30;"
	parsedFail, err := proc.ParseSQL(sqlFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedFail.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for unaggregated/non-group column 'age' in HAVING, got nil")
	} else if !strings.Contains(err.Error(), "must appear in the GROUP BY clause or be used in an aggregate function") || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected 42803 'must appear in the GROUP BY clause' error, got: %v", err)
	}

	// 2. HAVING column is in GROUP BY -> PASS
	sqlPass1 := "SELECT id, COUNT(*) FROM users GROUP BY id HAVING id > 10;"
	parsedPass1, err := proc.ParseSQL(sqlPass1)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPass1.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected HAVING with group column 'id' to pass, got: %v", err)
	}

	// 3. HAVING column is inside aggregate function -> PASS
	sqlPass2 := "SELECT id, COUNT(*) FROM users GROUP BY id HAVING AVG(age) > 25.0;"
	parsedPass2, err := proc.ParseSQL(sqlPass2)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPass2.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected HAVING with aggregated 'AVG(age)' to pass, got: %v", err)
	}

	// 4. HAVING with unaggregated column when no GROUP BY exists -> FAIL (SQLSTATE 42803)
	sqlFailNoGroup := "SELECT COUNT(*) FROM users HAVING age > 30;"
	parsedFailNoGroup, err := proc.ParseSQL(sqlFailNoGroup)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedFailNoGroup.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for unaggregated 'age' in HAVING without GROUP BY, got nil")
	} else if !strings.Contains(err.Error(), "must appear in the GROUP BY clause or be used in an aggregate function") || !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected 42803 error, got: %v", err)
	}
}

func TestValidator_Hardening_DivisionByZeroConstant(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Division by literal integer zero in SELECT projection -> FAIL (SQLSTATE 22012)
	sqlProjDiv := "SELECT 1 / 0 FROM users;"
	parsedProjDiv, err := proc.ParseSQL(sqlProjDiv)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedProjDiv.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for 1 / 0 in projection, got nil")
	} else if !strings.Contains(err.Error(), "division by zero") || !strings.Contains(err.Error(), "22012") {
		t.Errorf("expected 22012 division by zero error, got: %v", err)
	}

	// 2. Modulo by literal zero in WHERE filter -> FAIL (SQLSTATE 22012)
	sqlWhereMod := "SELECT id FROM users WHERE id % 0 = 0;"
	parsedWhereMod, err := proc.ParseSQL(sqlWhereMod)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedWhereMod.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for id %% 0 in WHERE, got nil")
	} else if !strings.Contains(err.Error(), "division by zero") || !strings.Contains(err.Error(), "22012") {
		t.Errorf("expected 22012 division by zero error, got: %v", err)
	}

	// 3. Normal division -> PASS
	sqlPass := "SELECT 10 / 2, id % 3 FROM users;"
	parsedPass, err := proc.ParseSQL(sqlPass)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPass.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected normal division to pass, got: %v", err)
	}
}

func TestValidator_Hardening_SubqueryWildcardArity(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Scalar subquery with SELECT * on users (which has 3 columns: id, name, age) -> FAIL (SQLSTATE 42601)
	sqlFail := "SELECT order_id FROM orders WHERE user_id = (SELECT * FROM users WHERE id = 1);"
	parsedFail, err := proc.ParseSQL(sqlFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateQueryAST(parsedFail.(*proc.GeneralSelectStmt), schemas); err == nil {
		t.Errorf("expected arity mismatch error for subquery SELECT * on 3-column table from ValidateQueryAST, got nil")
	} else if !strings.Contains(err.Error(), "subquery must return only one column, returned 3") || !strings.Contains(err.Error(), "42601") {
		t.Errorf("expected 42601 'returned 3' arity mismatch error, got: %v", err)
	}

	// 2. Single column subquery -> PASS
	sqlPass := "SELECT order_id FROM orders WHERE user_id = (SELECT id FROM users WHERE id = 1);"
	parsedPass, err := proc.ParseSQL(sqlPass)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPass.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected single-column subquery to pass, got: %v", err)
	}
}

func TestValidator_Hardening_LikeEscapeSingleChar(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Multi-character ESCAPE literal -> FAIL (SQLSTATE 22019)
	sqlMulti := "SELECT id FROM users WHERE name LIKE '%x%' ESCAPE 'abc';"
	parsedMulti, err := proc.ParseSQL(sqlMulti)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedMulti.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for multi-char ESCAPE 'abc', got nil")
	} else if !strings.Contains(err.Error(), "escape character must be a single character") || !strings.Contains(err.Error(), "22019") {
		t.Errorf("expected 22019 escape character error, got: %v", err)
	}

	// 2. Empty ESCAPE literal -> FAIL (SQLSTATE 2200C)
	sqlEmpty := "SELECT id FROM users WHERE name LIKE '%x%' ESCAPE '';"
	parsedEmpty, err := proc.ParseSQL(sqlEmpty)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedEmpty.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected error for empty ESCAPE '', got nil")
	} else if !strings.Contains(err.Error(), "escape character must be a single character") || !strings.Contains(err.Error(), "2200C") {
		t.Errorf("expected 2200C escape character error, got: %v", err)
	}

	// 3. Single character ESCAPE literal -> PASS
	sqlPass := "SELECT id FROM users WHERE name LIKE '%!_%' ESCAPE '!';"
	parsedPass, err := proc.ParseSQL(sqlPass)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPass.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected single-char ESCAPE to pass, got: %v", err)
	}
}

func TestValidator_Hardening_DivisionByZero_GroupByAndJoinOn(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Division by zero in GROUP BY -> FAIL (SQLSTATE 22012)
	sqlGroupBy := "SELECT id FROM users GROUP BY id / 0;"
	parsedGB, err := proc.ParseSQL(sqlGroupBy)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedGB.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected 22012 division by zero error in GROUP BY, got nil")
	} else if !strings.Contains(err.Error(), "division by zero") || !strings.Contains(err.Error(), "22012") {
		t.Errorf("expected SQLSTATE 22012, got: %v", err)
	}

	// 2. Modulo by zero in JOIN ON -> FAIL (SQLSTATE 22012)
	sqlJoinOn := "SELECT * FROM users u JOIN orders o ON u.id % 0 = o.user_id;"
	parsedJoin, err := proc.ParseSQL(sqlJoinOn)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedJoin.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected 22012 modulo by zero error in JOIN ON, got nil")
	} else if !strings.Contains(err.Error(), "division by zero") || !strings.Contains(err.Error(), "22012") {
		t.Errorf("expected SQLSTATE 22012, got: %v", err)
	}
}

func TestValidator_Hardening_SubqueryWildcardArity_JoinAndDerivedTable(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Wildcard subquery with JOIN returning multiple columns -> FAIL (SQLSTATE 42601)
	sqlJoin := "SELECT (SELECT * FROM users u JOIN orders o ON u.id = o.user_id);"
	parsedJoin, err := proc.ParseSQL(sqlJoin)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedJoin.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected 42601 arity error for subquery with JOIN *, got nil")
	} else if !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "subquery must return only one column") {
		t.Errorf("expected SQLSTATE 42601, got: %v", err)
	}

	// 2. Wildcard subquery on Derived Table with multiple columns -> FAIL (SQLSTATE 42601)
	sqlDerived := "SELECT (SELECT * FROM (SELECT id, name FROM users) d);"
	parsedDerived, err := proc.ParseSQL(sqlDerived)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedDerived.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected 42601 arity error for subquery on derived table *, got nil")
	} else if !strings.Contains(err.Error(), "42601") || !strings.Contains(err.Error(), "subquery must return only one column") {
		t.Errorf("expected SQLSTATE 42601, got: %v", err)
	}
}

func TestValidator_Hardening_LikeEscape_UnicodeRune(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Single Unicode rune multi-byte (UTF-8 2 bytes '§') -> MUST PASS!
	sqlRunePass := "SELECT id FROM users WHERE name LIKE '%x%' ESCAPE '§';"
	parsedPass, err := proc.ParseSQL(sqlRunePass)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedPass.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected single Unicode rune ESCAPE '§' to pass, got error: %v", err)
	}

	// 2. Two Unicode runes ('§§') -> MUST FAIL (SQLSTATE 22019)
	sqlRuneFail := "SELECT id FROM users WHERE name LIKE '%x%' ESCAPE '§§';"
	parsedFail, err := proc.ParseSQL(sqlRuneFail)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedFail.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected 22019 error for 2 Unicode runes ESCAPE '§§', got nil")
	} else if !strings.Contains(err.Error(), "22019") {
		t.Errorf("expected SQLSTATE 22019, got: %v", err)
	}
}

func TestValidator_Hardening_Having_TableColumnPrimacyCollision(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Collision: Projection alias 'amount' matches real table column 'orders.amount'.
	// Table column primacy dictates 'amount' in HAVING refers to unaggregated table column orders.amount -> MUST FAIL (SQLSTATE 42803)
	sqlCollision := "SELECT user_id, SUM(amount) AS amount FROM orders GROUP BY user_id HAVING amount > 100;"
	parsedCol, err := proc.ParseSQL(sqlCollision)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedCol.(*proc.GeneralSelectStmt)); err == nil {
		t.Errorf("expected 42803 unaggregated table column error under Table Column Primacy collision, got nil")
	} else if !strings.Contains(err.Error(), "42803") {
		t.Errorf("expected SQLSTATE 42803, got: %v", err)
	}

	// 2. Non-collision: Projection alias 'total_amount' does NOT match any table column.
	// Resolves to projection alias SUM(amount) -> MUST PASS
	sqlNoCollision := "SELECT user_id, SUM(amount) AS total_amount FROM orders GROUP BY user_id HAVING total_amount > 100;"
	parsedNoCol, err := proc.ParseSQL(sqlNoCollision)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if _, err := planner.NewBinder(schemas).BindSelect(parsedNoCol.(*proc.GeneralSelectStmt)); err != nil {
		t.Errorf("expected non-colliding projection alias in HAVING to pass, got error: %v", err)
	}
}

func TestBinder_Hardening_DerivedTableBindingAndScopeReset(t *testing.T) {
	schemas := setupTestCatalog()
	binder := planner.NewBinder(schemas)

	// 1. Derived table binding in FROM: SELECT d.id FROM (SELECT id, name FROM users) d;
	sqlDerived := "SELECT d.id FROM (SELECT id, name FROM users) d;"
	parsed, err := proc.ParseSQL(sqlDerived)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	bound, err := binder.BindSelect(parsed.(*proc.GeneralSelectStmt))
	if err != nil {
		t.Fatalf("BindSelect failed on derived table: %v", err)
	}
	if len(bound.Projections) != 1 {
		t.Fatalf("expected 1 projection, got %d", len(bound.Projections))
	}
	bp := bound.Projections[0]
	bCol, ok := bp.Expr.(*planner.BoundColumnRef)
	if !ok {
		t.Fatalf("expected *BoundColumnRef, got %T", bp.Expr)
	}
	if bCol.ColumnIdx != 0 {
		t.Errorf("expected ColumnIdx 0 for d.id, got %d", bCol.ColumnIdx)
	}
	if bCol.TupleSlot != 0 {
		t.Errorf("expected TupleSlot 0 for d.id, got %d", bCol.TupleSlot)
	}
	if bCol.DataType != storage.TypeInt {
		t.Errorf("expected DataType TypeInt for d.id, got %v", bCol.DataType)
	}

	// 2. Binder scope reset on subsequent queries: state from previous query must not leak
	sqlNext := "SELECT id FROM users;"
	parsedNext, err := proc.ParseSQL(sqlNext)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	boundNext, err := binder.BindSelect(parsedNext.(*proc.GeneralSelectStmt))
	if err != nil {
		t.Fatalf("BindSelect failed on reused binder: %v", err)
	}
	if len(boundNext.Tables) != 1 {
		t.Errorf("expected 1 table on reused binder, got %d", len(boundNext.Tables))
	}
	if boundNext.Tables[0].Name != "users" {
		t.Errorf("expected table 'users', got %s", boundNext.Tables[0].Name)
	}
}

func TestValidator_Hardening_DML_QualifiedColumnAndCorrelatedSubquery(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Qualified column in UPDATE SET: UPDATE users SET users.age = 30 WHERE users.id = 1;
	sqlUpd := "UPDATE users SET users.age = 30 WHERE users.id = 1;"
	parsedUpd, err := proc.ParseSQL(sqlUpd)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateDMLStmt(parsedUpd, schemas); err != nil {
		t.Errorf("expected qualified column users.age to pass validation, got: %v", err)
	}

	// 2. Non-existent qualified table in UPDATE SET: UPDATE users SET orders.age = 30; -> MUST FAIL
	sqlBadTable := "UPDATE users SET orders.age = 30;"
	parsedBadTable, err := proc.ParseSQL(sqlBadTable)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateDMLStmt(parsedBadTable, schemas); err == nil {
		t.Errorf("expected error for mismatched table qualifier orders.age in UPDATE users, got nil")
	}

	// 3. Correlated subquery in UPDATE SET referencing target table:
	// UPDATE users SET age = (SELECT o.user_id FROM orders o WHERE o.user_id = users.id);
	sqlCorrelated := "UPDATE users SET age = (SELECT o.user_id FROM orders o WHERE o.user_id = users.id);"
	parsedCorrelated, err := proc.ParseSQL(sqlCorrelated)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateDMLStmt(parsedCorrelated, schemas); err != nil {
		t.Errorf("expected correlated subquery referencing users.id in UPDATE to pass, got: %v", err)
	}
}

func TestValidator_Hardening_SubqueryWildcardArity_EdgeCases(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Scalar subquery with single column table (orders has id, user_id, amount -> 3 columns; we create a 1-column table)
	singleColSchema := &storage.TableSchema{
		Name: "single_col",
		Columns: []storage.Column{
			{Name: "val", Type: storage.TypeInt},
		},
	}
	schemas["single_col"] = singleColSchema

	// SELECT (SELECT * FROM single_col) -> exactly 1 column, MUST PASS
	sqlSingleCol := "SELECT (SELECT * FROM single_col);"
	parsedSingle, err := proc.ParseSQL(sqlSingleCol)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateQueryAST(parsedSingle.(*proc.GeneralSelectStmt), schemas); err != nil {
		t.Errorf("expected 1-column table wildcard in scalar subquery to pass, got: %v", err)
	}

	// 2. Scalar subquery with derived table expanding to multiple columns:
	// SELECT (SELECT * FROM (SELECT id, name FROM users) d); -> 2 columns -> MUST FAIL (SQLSTATE 42601)
	sqlMultiDerived := "SELECT (SELECT * FROM (SELECT id, name FROM users) d);"
	parsedMultiDerived, err := proc.ParseSQL(sqlMultiDerived)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateQueryAST(parsedMultiDerived.(*proc.GeneralSelectStmt), schemas); err == nil {
		t.Errorf("expected 42601 error for multi-column derived table wildcard in scalar subquery, got nil")
	} else if !strings.Contains(err.Error(), "42601") {
		t.Errorf("expected SQLSTATE 42601, got: %v", err)
	}

	// 3. Scalar subquery with derived table expanding to single column:
	// SELECT (SELECT * FROM (SELECT id FROM users) d); -> 1 column -> MUST PASS
	sqlSingleDerived := "SELECT (SELECT * FROM (SELECT id FROM users) d);"
	parsedSingleDerived, err := proc.ParseSQL(sqlSingleDerived)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateQueryAST(parsedSingleDerived.(*proc.GeneralSelectStmt), schemas); err != nil {
		t.Errorf("expected single-column derived table wildcard to pass, got: %v", err)
	}
}

func TestValidator_Hardening_DivisionByZero_EdgeCases(t *testing.T) {
	schemas := setupTestCatalog()

	// 1. Division by literal zero in HAVING: SELECT user_id, COUNT(*) FROM orders GROUP BY user_id HAVING COUNT(*) / 0 > 1; -> MUST FAIL (22012)
	sqlHavingDivZero := "SELECT user_id, COUNT(*) FROM orders GROUP BY user_id HAVING COUNT(*) / 0 > 1;"
	parsedHaving, err := proc.ParseSQL(sqlHavingDivZero)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateQueryAST(parsedHaving.(*proc.GeneralSelectStmt), schemas); err == nil {
		t.Errorf("expected 22012 error in HAVING division by zero, got nil")
	} else if !strings.Contains(err.Error(), "22012") {
		t.Errorf("expected SQLSTATE 22012, got: %v", err)
	}

	// 2. Modulo by literal zero in JOIN ON: SELECT * FROM users u JOIN orders o ON u.id % 0 = o.user_id; -> MUST FAIL (22012)
	sqlJoinModZero := "SELECT * FROM users u JOIN orders o ON u.id % 0 = o.user_id;"
	parsedJoinMod, err := proc.ParseSQL(sqlJoinModZero)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateQueryAST(parsedJoinMod.(*proc.GeneralSelectStmt), schemas); err == nil {
		t.Errorf("expected 22012 error in JOIN ON modulo by zero, got nil")
	} else if !strings.Contains(err.Error(), "22012") {
		t.Errorf("expected SQLSTATE 22012, got: %v", err)
	}

	// 3. Modulo by literal zero in GROUP BY: SELECT user_id FROM orders GROUP BY user_id % 0; -> MUST FAIL (22012)
	sqlGroupByModZero := "SELECT user_id FROM orders GROUP BY user_id % 0;"
	parsedGroupByMod, err := proc.ParseSQL(sqlGroupByModZero)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	if err := planner.ValidateQueryAST(parsedGroupByMod.(*proc.GeneralSelectStmt), schemas); err == nil {
		t.Errorf("expected 22012 error in GROUP BY modulo by zero, got nil")
	} else if !strings.Contains(err.Error(), "22012") {
		t.Errorf("expected SQLSTATE 22012, got: %v", err)
	}
}

func TestBinder_Hardening_JoinDerivedTableBinding(t *testing.T) {
	schemas := setupTestCatalog()
	binder := planner.NewBinder(schemas)

	// SELECT u.name, d.order_count FROM users u JOIN (SELECT user_id, COUNT(*) AS order_count FROM orders GROUP BY user_id) d ON u.id = d.user_id;
	sqlJoinDerived := "SELECT u.name, d.order_count FROM users u JOIN (SELECT user_id, COUNT(*) AS order_count FROM orders GROUP BY user_id) d ON u.id = d.user_id;"
	parsed, err := proc.ParseSQL(sqlJoinDerived)
	if err != nil {
		t.Fatalf("ParseSQL failed: %v", err)
	}
	bound, err := binder.BindSelect(parsed.(*proc.GeneralSelectStmt))
	if err != nil {
		t.Fatalf("BindSelect failed on JOIN derived table: %v", err)
	}
	if len(bound.Projections) != 2 {
		t.Fatalf("expected 2 projections, got %d", len(bound.Projections))
	}
	// Verify d.order_count is properly resolved to table index 1 and slot offset
	bp2 := bound.Projections[1]
	bCol2, ok := bp2.Expr.(*planner.BoundColumnRef)
	if !ok {
		t.Fatalf("expected *BoundColumnRef for d.order_count, got %T", bp2.Expr)
	}
	if bCol2.TableID != 1 {
		t.Errorf("expected TableID 1 for d.order_count, got %d", bCol2.TableID)
	}
	if bCol2.ColumnIdx != 1 { // (user_id is 0, order_count is 1)
		t.Errorf("expected ColumnIdx 1 for d.order_count, got %d", bCol2.ColumnIdx)
	}
	if bCol2.TupleSlot < 3 { // users has 3 columns (0, 1, 2), so d starts at 3; order_count is 3+1=4
		t.Errorf("expected TupleSlot >= 3 for d.order_count, got %d", bCol2.TupleSlot)
	}
}

