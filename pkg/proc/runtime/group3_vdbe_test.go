package runtime_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestGroup3_BUG_PROC_SIMPLE_CASE_1 verifies that simple CASE expressions
// (CASE expr WHEN val1 THEN res1 ELSE res2 END) are parsed, compiled into VDBE bytecode,
// and evaluated correctly with type coercion.
func TestGroup3_BUG_PROC_SIMPLE_CASE_1(t *testing.T) {
	// 1. Direct Parser & AST verification
	p := proc.NewParser(proc.NewLexer("CASE @val WHEN 1 THEN 'One' WHEN 2 THEN 'Two' ELSE 'Other' END"))
	expr := p.ParseExpression(proc.PrecLowest)
	if expr == nil {
		t.Fatalf("Failed to parse simple CASE expression")
	}
	caseExpr, ok := expr.(*proc.CaseExpr)
	if !ok {
		t.Fatalf("Expected *proc.CaseExpr, got %T", expr)
	}
	if caseExpr.Operand == nil {
		t.Fatalf("Expected non-nil Operand in simple CaseExpr")
	}
	if len(caseExpr.Whens) != 2 {
		t.Fatalf("Expected 2 WHEN arms, got %d", len(caseExpr.Whens))
	}

	// 2. Direct Evaluator (EvalRowExpr) verification
	evalCtx := map[string]any{"val": int64(2)}
	res, err := proc.EvalRowExpr(caseExpr, evalCtx, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr error: %v", err)
	}
	if res != "Two" {
		t.Fatalf("Expected 'Two', got %v", res)
	}

	// Test ELSE branch
	evalCtx["val"] = int64(999)
	res, err = proc.EvalRowExpr(caseExpr, evalCtx, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr error on ELSE: %v", err)
	}
	if res != "Other" {
		t.Fatalf("Expected 'Other', got %v", res)
	}

	// 3. VDBE Procedure Execution verification
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	tsql := `
CREATE PROCEDURE SimpleCaseProc @val INT
AS
BEGIN
    DECLARE @out VARCHAR(50);
    SET @out = CASE @val
        WHEN 1 THEN 'One'
        WHEN 2 THEN 'Two'
        ELSE 'Other'
    END;
    RETURN @out;
END;
`
	prog, err := proc.Compile(tsql)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName("SimpleCaseProc", prog)
	exec := runtime.NewExecutor(cache)

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession error: %v", err)
	}
	defer sess.Close()

	// Case 1: @val = 1 -> 'One'
	runRes, err := exec.Run(prog, "SimpleCaseProc", map[string]any{"val": 1}, sess)
	if err != nil {
		t.Fatalf("Run error for val=1: %v", err)
	}
	if runRes.ReturnValue != "One" {
		t.Fatalf("Expected 'One', got %v", runRes.ReturnValue)
	}

	// Case 2: @val = 2 -> 'Two'
	runRes, err = exec.Run(prog, "SimpleCaseProc", map[string]any{"val": 2}, sess)
	if err != nil {
		t.Fatalf("Run error for val=2: %v", err)
	}
	if runRes.ReturnValue != "Two" {
		t.Fatalf("Expected 'Two', got %v", runRes.ReturnValue)
	}

	// Case 3: @val = 99 -> 'Other'
	runRes, err = exec.Run(prog, "SimpleCaseProc", map[string]any{"val": 99}, sess)
	if err != nil {
		t.Fatalf("Run error for val=99: %v", err)
	}
	if runRes.ReturnValue != "Other" {
		t.Fatalf("Expected 'Other', got %v", runRes.ReturnValue)
	}
}

// TestGroup3_BUG_VM_SUBQ_NIL_1 verifies that FROM-less scalar subqueries like (SELECT 42)
// or (SELECT @x + 10) do not cause nil pointer dereference panics on sel.From.Name,
// and that multi-column scalar subqueries are rejected.
func TestGroup3_BUG_VM_SUBQ_NIL_1(t *testing.T) {
	// 1. FROM-less subquery parsing
	p := proc.NewParser(proc.NewLexer("(SELECT 42)"))
	expr := p.ParseExpression(proc.PrecLowest)
	if expr == nil {
		t.Fatalf("Failed to parse FROM-less scalar subquery (SELECT 42)")
	}
	subq, ok := expr.(*proc.ScalarSubqueryExpr)
	if !ok {
		t.Fatalf("Expected *proc.ScalarSubqueryExpr, got %T", expr)
	}
	if subq.Table != "" {
		t.Fatalf("Expected empty Table for FROM-less subquery, got %q", subq.Table)
	}

	// 2. Direct EvalRowExpr
	res, err := proc.EvalRowExpr(subq, nil, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr error: %v", err)
	}
	if res != int64(42) {
		t.Fatalf("Expected 42, got %v", res)
	}

	// 3. VDBE Procedure Execution with FROM-less subquery
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	tsql := `
CREATE PROCEDURE FromLessSubqProc
AS
BEGIN
    DECLARE @val INT;
    SET @val = (SELECT 42);
    RETURN @val;
END;
`
	prog, err := proc.Compile(tsql)
	if err != nil {
		t.Fatalf("Compile error for FROM-less subquery procedure: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName("FromLessSubqProc", prog)
	exec := runtime.NewExecutor(cache)

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession error: %v", err)
	}
	defer sess.Close()

	runRes, err := exec.Run(prog, "FromLessSubqProc", nil, sess)
	if err != nil {
		t.Fatalf("Run error for FromLessSubqProc: %v", err)
	}
	if runRes.ReturnValue != int64(42) {
		t.Fatalf("Expected 42, got %v", runRes.ReturnValue)
	}

	// 4. Reject multi-column scalar subquery
	p2 := proc.NewParser(proc.NewLexer("(SELECT 1, 2)"))
	expr2 := p2.ParseExpression(proc.PrecLowest)
	if expr2 != nil {
		t.Fatalf("Expected parsing to fail on multi-column scalar subquery, but got: %v", expr2)
	}
}

// TestGroup3_BUG_VM_INSERT_DEFAULT_1 verifies that VDBE OpInsert properly evaluates
// and injects literal column default values (e.g. DEFAULT 42, DEFAULT 'active') when omitted.
func TestGroup3_BUG_VM_INSERT_DEFAULT_1(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "status", Type: storage.TypeVarChar, Default: "'active'"},
			{Name: "quantity", Type: storage.TypeInt, Default: "((100))"},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable error: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)

	// OpInsert with only id specified
	insertProg := &proc.Program{
		Name: "TestInsertDefault",
	}
	auxInsert := insertProg.AddAux(&proc.DMLData{
		Table:    "items",
		Columns:  []string{"id"},
		ValExprs: []proc.Expr{&proc.LiteralExpr{Val: storage.IntValue(1)}},
	})
	insertProg.Instructions = []proc.Instruction{
		{
			Op:  proc.OpInsert,
			Aux: auxInsert,
		},
		{Op: proc.OpHalt},
	}

	_, _, err = executor.RunInTx(insertProg, nil, tx, nil)
	if err != nil {
		t.Fatalf("RunInTx OpInsert error: %v", err)
	}

	// Verify record in storage has default values populated
	rec, err := tx.Get("items", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	statusVal, ok := rec.Get("status")
	if !ok || statusVal.SVal != "active" {
		t.Fatalf("Expected status 'active', got ok=%v, val=%+v", ok, statusVal)
	}
	qtyVal, ok := rec.Get("quantity")
	if !ok || qtyVal.IVal != 100 {
		t.Fatalf("Expected quantity 100, got ok=%v, val=%+v", ok, qtyVal)
	}
}

// TestGroup3_BUG_VM_UPDATE_1 verifies that VDBE OpUpdate and execUpdateWhere normalize
// assignment column names (handling brackets and differing casing) to avoid silent update loss.
func TestGroup3_BUG_VM_UPDATE_1(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "Price", Type: storage.TypeInt},
			{Name: "Description", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable error: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	initialRec := storage.Record{
		"id":          storage.IntValue(1),
		"Price":       storage.IntValue(100),
		"Description": storage.StringValue("Widget"),
	}
	if err := tx.Insert("products", initialRec); err != nil {
		t.Fatalf("Insert error: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)

	// 1. Point UPDATE with bracketed lowercase [price]
	progPoint := &proc.Program{
		Name: "TestPointUpdate",
	}
	auxPoint := progPoint.AddAux(&proc.DMLData{
		Table:    "products",
		WhereCol: "id",
		WhereVal: &proc.LiteralExpr{Val: storage.IntValue(1)},
		Assignments: []proc.UpdateAssignment{
			{Column: "[price]", Value: &proc.LiteralExpr{Val: storage.IntValue(250)}},
		},
	})
	progPoint.Instructions = []proc.Instruction{
		{
			Op:  proc.OpUpdate,
			Aux: auxPoint,
		},
		{Op: proc.OpHalt},
	}

	_, _, err = executor.RunInTx(progPoint, nil, tx, nil)
	if err != nil {
		t.Fatalf("Point UPDATE error: %v", err)
	}

	updatedRec, err := tx.Get("products", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	priceVal, ok := updatedRec.Get("Price")
	if !ok || priceVal.IVal != 250 {
		t.Fatalf("Expected Price to be updated to 250, got ok=%v, val=%+v", ok, priceVal)
	}

	// 2. OpUpdateWhere with uppercase "DESCRIPTION"
	progWhere := &proc.Program{
		Name: "TestWhereUpdate",
	}
	auxWhere := progWhere.AddAux(&proc.DMLData{
		Table: "products",
		WhereExpr: &proc.BinaryExpr{
			Left:  &proc.ColumnRefExpr{Name: "id"},
			Op:    proc.TokenEq,
			Right: &proc.LiteralExpr{Val: storage.IntValue(1)},
		},
		Assignments: []proc.UpdateAssignment{
			{Column: "DESCRIPTION", Value: &proc.LiteralExpr{Val: storage.StringValue("Super Widget")}},
		},
	})
	progWhere.Instructions = []proc.Instruction{
		{
			Op:  proc.OpUpdateWhere,
			Aux: auxWhere,
		},
		{Op: proc.OpHalt},
	}

	_, _, err = executor.RunInTx(progWhere, nil, tx, nil)
	if err != nil {
		t.Fatalf("OpUpdateWhere error: %v", err)
	}

	updatedRec2, err := tx.Get("products", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	descVal, ok := updatedRec2.Get("Description")
	if !ok || descVal.SVal != "Super Widget" {
		t.Fatalf("Expected Description to be updated to 'Super Widget', got ok=%v, val=%+v", ok, descVal)
	}
}

// TestGroup3_BUG_VM_COMP_IDX_1 verifies that VDBE VM does not mistake a composite index
// for a single-column index when scanning, and correctly resolves composite indexes when
// all composite columns are provided.
func TestGroup3_BUG_VM_COMP_IDX_1(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "tenant_id", Type: storage.TypeInt},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "balance", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:      "IX_tenant_user",
				TableName: "accounts",
				Columns:   []string{"tenant_id", "user_id"},
				Unique:    false,
			},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable error: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	// Insert test data
	rows := []storage.Record{
		{"id": storage.IntValue(1), "tenant_id": storage.IntValue(10), "user_id": storage.IntValue(100), "balance": storage.IntValue(1000)},
		{"id": storage.IntValue(2), "tenant_id": storage.IntValue(10), "user_id": storage.IntValue(200), "balance": storage.IntValue(2000)},
		{"id": storage.IntValue(3), "tenant_id": storage.IntValue(20), "user_id": storage.IntValue(100), "balance": storage.IntValue(3000)},
	}
	for _, r := range rows {
		if err := tx.Insert("accounts", r); err != nil {
			t.Fatalf("Insert error: %v", err)
		}
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)

	// 1. UPDATE WHERE only tenant_id = 10
	// Previously, VDBE picked IX_tenant_user with unencoded integer 10, returning 0 rows updated!
	progSingleCol := &proc.Program{
		Name: "TestSingleColCompIdx",
	}
	auxSingle := progSingleCol.AddAux(&proc.DMLData{
		Table: "accounts",
		WhereExpr: &proc.BinaryExpr{
			Left:  &proc.ColumnRefExpr{Name: "tenant_id"},
			Op:    proc.TokenEq,
			Right: &proc.LiteralExpr{Val: storage.IntValue(10)},
		},
		Assignments: []proc.UpdateAssignment{
			{Column: "balance", Value: &proc.LiteralExpr{Val: storage.IntValue(9999)}},
		},
	})
	progSingleCol.Instructions = []proc.Instruction{
		{
			Op:  proc.OpUpdateWhere,
			Aux: auxSingle,
		},
		{Op: proc.OpHalt},
	}

	_, _, err = executor.RunInTx(progSingleCol, nil, tx, nil)
	if err != nil {
		t.Fatalf("Run single col update error: %v", err)
	}

	// Verify both tenant_id = 10 rows were updated to 9999
	rec1, _ := tx.Get("accounts", storage.IntValue(1))
	b1, _ := rec1.Get("balance")
	if b1.IVal != 9999 {
		t.Fatalf("Row 1 balance expected 9999, got %d (single col update failed due to composite index misinterpretation)", b1.IVal)
	}
	rec2, _ := tx.Get("accounts", storage.IntValue(2))
	b2, _ := rec2.Get("balance")
	if b2.IVal != 9999 {
		t.Fatalf("Row 2 balance expected 9999, got %d", b2.IVal)
	}
	rec3, _ := tx.Get("accounts", storage.IntValue(3))
	b3, _ := rec3.Get("balance")
	if b3.IVal != 3000 {
		t.Fatalf("Row 3 balance expected untouched 3000, got %d", b3.IVal)
	}

	// 2. UPDATE WHERE tenant_id = 10 AND user_id = 200 (all composite columns present)
	progComposite := &proc.Program{
		Name: "TestCompositeColIdx",
	}
	auxComposite := progComposite.AddAux(&proc.DMLData{
		Table: "accounts",
		WhereExpr: &proc.BinaryExpr{
			Left: &proc.BinaryExpr{
				Left:  &proc.ColumnRefExpr{Name: "tenant_id"},
				Op:    proc.TokenEq,
				Right: &proc.LiteralExpr{Val: storage.IntValue(10)},
			},
			Op: proc.TokenAnd,
			Right: &proc.BinaryExpr{
				Left:  &proc.ColumnRefExpr{Name: "user_id"},
				Op:    proc.TokenEq,
				Right: &proc.LiteralExpr{Val: storage.IntValue(200)},
			},
		},
		Assignments: []proc.UpdateAssignment{
			{Column: "balance", Value: &proc.LiteralExpr{Val: storage.IntValue(7777)}},
		},
	})
	progComposite.Instructions = []proc.Instruction{
		{
			Op:  proc.OpUpdateWhere,
			Aux: auxComposite,
		},
		{Op: proc.OpHalt},
	}

	_, _, err = executor.RunInTx(progComposite, nil, tx, nil)
	if err != nil {
		t.Fatalf("Run composite update error: %v", err)
	}

	rec2After, _ := tx.Get("accounts", storage.IntValue(2))
	b2After, _ := rec2After.Get("balance")
	if b2After.IVal != 7777 {
		t.Fatalf("Row 2 balance expected 7777, got %d (composite index scan failed)", b2After.IVal)
	}
}
