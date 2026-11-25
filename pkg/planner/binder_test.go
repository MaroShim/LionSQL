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
