package runtime_test

import (
	"strings"
	"testing"

	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func setupTriggerTestDB(t *testing.T) storage.Engine {
	eng := memengine.New()

	// Orders table
	err := eng.CreateTable(&storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "item", Type: storage.TypeVarChar},
			{Name: "amount", Type: storage.TypeFloat},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable orders failed: %v", err)
	}

	// Audit log table
	err = eng.CreateTable(&storage.TableSchema{
		Name: "order_audit",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "event", Type: storage.TypeVarChar},
			{Name: "order_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable order_audit failed: %v", err)
	}

	return eng
}

func TestTrigger_AfterInsertAudit(t *testing.T) {
	eng := setupTriggerTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// 1. Create and register trigger: trg_OrdersInsert
	trgSQL := `
CREATE TRIGGER trg_OrdersInsert
ON orders
AFTER INSERT
AS
BEGIN
    DECLARE @OId INT;
    SET @OId = (SELECT id FROM inserted);
    DECLARE @Amt FLOAT;
    SET @Amt = (SELECT amount FROM inserted);
    INSERT INTO order_audit (id, event, order_id, amount) VALUES (@OId, 'INSERTED', @OId, @Amt);
END
`
	trgStmt, err := proc.ParseTrigger(trgSQL)
	if err != nil {
		t.Fatalf("ParseTrigger failed: %v", err)
	}

	trgJS, err := proc.TranspileTrigger(trgStmt)
	if err != nil {
		t.Fatalf("TranspileTrigger failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	trgProg, err := cache.GetOrCompile(trgJS)
	if err != nil {
		t.Fatalf("GetOrCompile Trigger failed: %v", err)
	}

	executor := runtime.NewExecutor(cache)
	executor.RegisterTrigger(trgStmt.Name, trgStmt.Table, trgStmt.Events[0], trgProg)

	// 2. Create procedure that inserts into orders
	procSQL := `
CREATE PROCEDURE dbo.sp_CreateOrder
    @Id INT,
    @Item VARCHAR(50),
    @Amount FLOAT
AS
BEGIN
    INSERT INTO orders (id, item, amount) VALUES (@Id, @Item, @Amount);
    RETURN 1;
END
`
	procStmt, err := proc.Parse(procSQL)
	if err != nil {
		t.Fatalf("Parse Stored Procedure failed: %v", err)
	}

	procJS, err := proc.Transpile(procStmt)
	if err != nil {
		t.Fatalf("Transpile Stored Procedure failed: %v", err)
	}

	procProg, err := cache.GetOrCompile(procJS)
	if err != nil {
		t.Fatalf("GetOrCompile Procedure failed: %v", err)
	}

	// 3. Execute procedure
	_, err = executor.Run(procProg, procStmt.Name, map[string]any{
		"Id":     101,
		"Item":   "Database Server",
		"Amount": 499.95,
	}, session)
	if err != nil {
		t.Fatalf("Executor.Run failed: %v", err)
	}

	// 4. Verify orders table
	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	orderRec, err := tx.Get("orders", storage.IntValue(101))
	if err != nil {
		t.Fatalf("orders get failed: %v", err)
	}
	if orderRec["item"].SVal != "Database Server" {
		t.Fatalf("expected item 'Database Server', got %v", orderRec["item"].SVal)
	}

	// 5. Verify trigger automatically populated order_audit
	auditRec, err := tx.Get("order_audit", storage.IntValue(101))
	if err != nil {
		t.Fatalf("order_audit get failed: %v", err)
	}
	if auditRec["event"].SVal != "INSERTED" {
		t.Fatalf("expected audit event 'INSERTED', got %v", auditRec["event"].SVal)
	}
	if auditRec["order_id"].IVal != 101 {
		t.Fatalf("expected audit order_id 101, got %v", auditRec["order_id"].IVal)
	}
}

func TestTrigger_AfterUpdateAndDeletedDelta(t *testing.T) {
	eng := setupTriggerTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// Seed an order
	initTx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = initTx.Insert("orders", storage.Record{
		"id":     storage.IntValue(202),
		"item":   storage.StringValue("Storage Node"),
		"amount": storage.FloatValue(200.0),
	})
	if err := initTx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 1. Create and register UPDATE trigger
	trgSQL := `
CREATE TRIGGER trg_OrdersUpdate
ON orders
AFTER UPDATE
AS
BEGIN
    DECLARE @OldAmt FLOAT;
    SET @OldAmt = (SELECT amount FROM deleted);
    DECLARE @NewAmt FLOAT;
    SET @NewAmt = (SELECT amount FROM inserted);
    DECLARE @OId INT;
    SET @OId = (SELECT id FROM inserted);
    INSERT INTO order_audit (id, event, order_id, amount) VALUES (@OId + 1000, 'UPDATED', @OId, @NewAmt - @OldAmt);
END
`
	trgStmt, err := proc.ParseTrigger(trgSQL)
	if err != nil {
		t.Fatalf("ParseTrigger failed: %v", err)
	}

	trgJS, err := proc.TranspileTrigger(trgStmt)
	if err != nil {
		t.Fatalf("TranspileTrigger failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	trgProg, err := cache.GetOrCompile(trgJS)
	if err != nil {
		t.Fatalf("GetOrCompile Trigger failed: %v", err)
	}

	executor := runtime.NewExecutor(cache)
	executor.RegisterTrigger(trgStmt.Name, trgStmt.Table, trgStmt.Events[0], trgProg)

	// 2. Create procedure to update order amount
	procSQL := `
CREATE PROCEDURE dbo.sp_UpdateOrderAmount
    @Id INT,
    @NewAmount FLOAT
AS
BEGIN
    UPDATE orders SET amount = @NewAmount WHERE id = @Id;
    RETURN 1;
END
`
	procStmt, err := proc.Parse(procSQL)
	if err != nil {
		t.Fatalf("Parse Stored Procedure failed: %v", err)
	}

	procJS, err := proc.Transpile(procStmt)
	if err != nil {
		t.Fatalf("Transpile Stored Procedure failed: %v", err)
	}

	procProg, err := cache.GetOrCompile(procJS)
	if err != nil {
		t.Fatalf("GetOrCompile Procedure failed: %v", err)
	}

	// 3. Execute update: 200.0 -> 350.0 (delta: +150.0)
	_, err = executor.Run(procProg, procStmt.Name, map[string]any{
		"Id":        202,
		"NewAmount": 350.0,
	}, session)
	if err != nil {
		t.Fatalf("Executor.Run failed: %v", err)
	}

	// 4. Verify order_audit has delta 150.0
	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	auditRec, err := tx.Get("order_audit", storage.IntValue(1202))
	if err != nil {
		t.Fatalf("order_audit get failed: %v", err)
	}
	if auditRec["event"].SVal != "UPDATED" {
		t.Fatalf("expected 'UPDATED', got %v", auditRec["event"].SVal)
	}
	if auditRec["amount"].FVal != 150.0 {
		t.Fatalf("expected delta amount 150.0, got %v", auditRec["amount"].FVal)
	}
}

func TestTrigger_AtomicRollbackOnThrow(t *testing.T) {
	eng := setupTriggerTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// 1. Create guard trigger that prevents orders over 1000
	trgSQL := `
CREATE TRIGGER trg_ValidateOrderLimit
ON orders
AFTER INSERT
AS
BEGIN
    DECLARE @Amt FLOAT;
    SET @Amt = (SELECT amount FROM inserted);
    IF @Amt > 1000
    BEGIN
        THROW 50000, 'Order amount exceeds credit limit', 1;
    END
END
`
	trgStmt, err := proc.ParseTrigger(trgSQL)
	if err != nil {
		t.Fatalf("ParseTrigger failed: %v", err)
	}

	trgJS, err := proc.TranspileTrigger(trgStmt)
	if err != nil {
		t.Fatalf("TranspileTrigger failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	trgProg, err := cache.GetOrCompile(trgJS)
	if err != nil {
		t.Fatalf("GetOrCompile Trigger failed: %v", err)
	}

	executor := runtime.NewExecutor(cache)
	executor.RegisterTrigger(trgStmt.Name, trgStmt.Table, trgStmt.Events[0], trgProg)

	// 2. Procedure inserting order
	procSQL := `
CREATE PROCEDURE dbo.sp_CreateOrderStrict
    @Id INT,
    @Item VARCHAR(50),
    @Amount FLOAT
AS
BEGIN
    INSERT INTO orders (id, item, amount) VALUES (@Id, @Item, @Amount);
    RETURN 1;
END
`
	procStmt, err := proc.Parse(procSQL)
	if err != nil {
		t.Fatalf("Parse Stored Procedure failed: %v", err)
	}

	procJS, err := proc.Transpile(procStmt)
	if err != nil {
		t.Fatalf("Transpile Stored Procedure failed: %v", err)
	}

	procProg, err := cache.GetOrCompile(procJS)
	if err != nil {
		t.Fatalf("GetOrCompile Procedure failed: %v", err)
	}

	// 3. Attempt inserting order with amount = 5000 (must be rejected by trigger)
	_, err = executor.Run(procProg, procStmt.Name, map[string]any{
		"Id":     303,
		"Item":   "High-End Cluster",
		"Amount": 5000.0,
	}, session)

	if err == nil {
		t.Fatalf("expected error from trigger THROW, got nil")
	}

	if !strings.Contains(err.Error(), "Order amount exceeds credit limit") {
		t.Fatalf("expected 'Order amount exceeds credit limit', got: %v", err)
	}

	// 4. Verify ambient transaction rolled back: order 303 must NOT exist in database!
	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	_, err = tx.Get("orders", storage.IntValue(303))
	if err == nil {
		t.Fatalf("expected order 303 to not exist after rollback, but it was found")
	}
}
