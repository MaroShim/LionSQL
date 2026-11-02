package engine

import (
	"fmt"
	"sync"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func setupTestCoordinator(t *testing.T) (*Coordinator, func()) {
	t.Helper()
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("open memengine failed: %v", err)
	}
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	cleanup := func() {
		_ = eng.Close()
	}
	return coord, cleanup
}

func TestWritableCTELowering_3WayOrderCreation(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	setupSQL := `
	CREATE TABLE orders (
		id VARCHAR(64) PRIMARY KEY,
		idempotency_key VARCHAR(64),
		customer_id VARCHAR(64),
		total_amount INT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE UNIQUE INDEX idx_orders_idempotency ON orders (idempotency_key);

	CREATE TABLE order_items (
		id VARCHAR(64) PRIMARY KEY,
		order_id VARCHAR(64),
		item_id VARCHAR(64),
		quantity INT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE outbox_events (
		id VARCHAR(64) PRIMARY KEY,
		aggregate_id VARCHAR(64),
		event_type VARCHAR(64),
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := coord.ExecuteSQL(setupSQL); err != nil {
		t.Fatalf("schema setup failed: %v", err)
	}

	createWithOutboxSQL := `
	WITH ins_order AS (
		INSERT INTO orders (id, idempotency_key, customer_id, total_amount)
		VALUES ('ord-1', 'idem-1', 'cust-1', 15000)
		RETURNING created_at, updated_at
	), ins_item AS (
		INSERT INTO order_items (id, order_id, item_id, quantity)
		VALUES ('oi-1', 'ord-1', 'sku-100', 2)
		RETURNING created_at, updated_at
	), ins_outbox AS (
		INSERT INTO outbox_events (id, aggregate_id, event_type)
		VALUES ('evt-1', 'ord-1', 'ORDER_CREATED')
		RETURNING created_at, updated_at
	)
	SELECT o.created_at, o.updated_at, i.created_at, i.updated_at, e.created_at, e.updated_at
	FROM ins_order o, ins_item i, ins_outbox e;
	`
	res, err := coord.ExecuteSQL(createWithOutboxSQL)
	if err != nil {
		t.Fatalf("CreateWithOutbox failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row from CreateWithOutbox, got %d", len(res.Rows))
	}
	if len(res.Columns) != 6 {
		t.Fatalf("expected 6 columns from CreateWithOutbox, got %d (%v)", len(res.Columns), res.Columns)
	}
	if len(res.Values) != 1 || len(res.Values[0]) != 6 {
		t.Fatalf("expected 6 values in row, got %v", res.Values)
	}
	for colIdx, val := range res.Values[0] {
		if val == nil || fmt.Sprintf("%v", val) == "" {
			t.Fatalf("column %d in CreateWithOutbox returned empty value: %v", colIdx, val)
		}
	}

	// Verify records were inserted in the actual tables
	oCheck, err := coord.ExecuteSQL("SELECT id, customer_id, total_amount FROM orders WHERE id = 'ord-1';")
	if err != nil || len(oCheck.Rows) != 1 {
		t.Fatalf("orders record not found: %v, rows=%d", err, len(oCheck.Rows))
	}
	oiCheck, err := coord.ExecuteSQL("SELECT id, order_id, item_id FROM order_items WHERE id = 'oi-1';")
	if err != nil || len(oiCheck.Rows) != 1 {
		t.Fatalf("order_items record not found: %v, rows=%d", err, len(oiCheck.Rows))
	}
	outCheck, err := coord.ExecuteSQL("SELECT id, aggregate_id, event_type FROM outbox_events WHERE id = 'evt-1';")
	if err != nil || len(outCheck.Rows) != 1 {
		t.Fatalf("outbox_events record not found: %v, rows=%d", err, len(outCheck.Rows))
	}
}

func TestWritableCTELowering_AtomicRollbackOnConstraintFailure(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	setupSQL := `
	CREATE TABLE orders (
		id VARCHAR(64) PRIMARY KEY,
		idempotency_key VARCHAR(64),
		customer_id VARCHAR(64),
		total_amount INT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE UNIQUE INDEX idx_orders_idempotency ON orders (idempotency_key);

	CREATE TABLE order_items (
		id VARCHAR(64) PRIMARY KEY,
		order_id VARCHAR(64),
		item_id VARCHAR(64),
		quantity INT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE outbox_events (
		id VARCHAR(64) PRIMARY KEY,
		aggregate_id VARCHAR(64),
		event_type VARCHAR(64),
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := coord.ExecuteSQL(setupSQL); err != nil {
		t.Fatalf("schema setup failed: %v", err)
	}

	// Pre-seed an outbox event with id 'evt-existing'
	_, err := coord.ExecuteSQL(`INSERT INTO outbox_events (id, aggregate_id, event_type) VALUES ('evt-existing', 'ord-old', 'OLD');`)
	if err != nil {
		t.Fatalf("pre-seed outbox event failed: %v", err)
	}

	// Attempt a Writable CTE where step 1 succeeds, step 2 succeeds, but step 3 collides on 'evt-existing'
	collidingSQL := `
	WITH ins_order AS (
		INSERT INTO orders (id, idempotency_key, customer_id, total_amount)
		VALUES ('ord-fail', 'idem-fail', 'cust-fail', 999)
		RETURNING created_at, updated_at
	), ins_item AS (
		INSERT INTO order_items (id, order_id, item_id, quantity)
		VALUES ('oi-fail', 'ord-fail', 'sku-fail', 1)
		RETURNING created_at, updated_at
	), ins_outbox AS (
		INSERT INTO outbox_events (id, aggregate_id, event_type)
		VALUES ('evt-existing', 'ord-fail', 'ORDER_CREATED')
		RETURNING created_at, updated_at
	)
	SELECT o.created_at, o.updated_at, i.created_at, i.updated_at, e.created_at, e.updated_at
	FROM ins_order o, ins_item i, ins_outbox e;
	`
	_, err = coord.ExecuteSQL(collidingSQL)
	if err == nil {
		t.Fatalf("expected duplicate key error, got nil")
	}

	// Verify atomicity: neither 'ord-fail' nor 'oi-fail' should exist!
	oCheck, _ := coord.ExecuteSQL("SELECT id FROM orders WHERE id = 'ord-fail';")
	if len(oCheck.Rows) != 0 {
		t.Fatalf("atomicity violated: orders table has uncommitted row 'ord-fail'")
	}
	oiCheck, _ := coord.ExecuteSQL("SELECT id FROM order_items WHERE id = 'oi-fail';")
	if len(oiCheck.Rows) != 0 {
		t.Fatalf("atomicity violated: order_items table has uncommitted row 'oi-fail'")
	}
}

func TestWritableCTELowering_ChainedUpdateAndInsert(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	setupSQL := `
	CREATE TABLE inventories (
		item_id VARCHAR(64) PRIMARY KEY,
		warehouse_id VARCHAR(64),
		quantity INT,
		reserved_quantity INT,
		safety_stock_threshold INT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		created_by VARCHAR(64),
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_by VARCHAR(64)
	);
	CREATE TABLE inventory_transactions (
		id VARCHAR(64) PRIMARY KEY,
		item_id VARCHAR(64),
		warehouse_id VARCHAR(64),
		transaction_type VARCHAR(32),
		quantity INT,
		balance_after INT,
		reference_id VARCHAR(64),
		created_by VARCHAR(64),
		updated_by VARCHAR(64)
	);
	`
	if _, err := coord.ExecuteSQL(setupSQL); err != nil {
		t.Fatalf("schema setup failed: %v", err)
	}

	_, err := coord.ExecuteSQL(`
		INSERT INTO inventories (item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_by, updated_by)
		VALUES ('sku-100', 'wh-1', 100, 20, 10, 'system', 'system');
	`)
	if err != nil {
		t.Fatalf("seed inventory failed: %v", err)
	}

	// 1. Sufficient stock: reserve 5 units (reserved: 20 -> 25)
	reserveSQL := `
	WITH upd_inv AS (
		UPDATE inventories
		SET reserved_quantity = reserved_quantity + 5,
		    updated_by = 'user-1',
		    updated_at = CURRENT_TIMESTAMP
		WHERE item_id = 'sku-100'
		  AND (quantity - reserved_quantity) >= 5
		RETURNING item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
	), ins_tx AS (
		INSERT INTO inventory_transactions (
			id, item_id, warehouse_id, transaction_type, quantity, balance_after, reference_id, created_by, updated_by
		)
		SELECT 'tx-1', item_id, 'wh-1', 'RESERVE', 5, (quantity - reserved_quantity), 'user-1', 'user-1', 'user-1'
		FROM upd_inv
	)
	SELECT item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
	FROM upd_inv;
	`
	res, err := coord.ExecuteSQL(reserveSQL)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["reserved_quantity"]) != "25" {
		t.Fatalf("expected reserved_quantity=25, got %v", res.Rows[0]["reserved_quantity"])
	}

	// Verify inventory_transactions record was inserted
	txCheck, err := coord.ExecuteSQL("SELECT id, balance_after FROM inventory_transactions WHERE id = 'tx-1';")
	if err != nil || len(txCheck.Rows) != 1 {
		t.Fatalf("inventory_transactions tx-1 not found: %v", err)
	}
	// balance_after should be 100 - 25 = 75
	if fmt.Sprintf("%v", txCheck.Rows[0]["balance_after"]) != "75" {
		t.Fatalf("expected balance_after=75, got %v", txCheck.Rows[0]["balance_after"])
	}

	// 2. Insufficient stock: try to reserve 80 units (available is 75 < 80)
	// Should update 0 rows, insert 0 transactions, and return 0 rows!
	reserveFailSQL := `
	WITH upd_inv AS (
		UPDATE inventories
		SET reserved_quantity = reserved_quantity + 80,
		    updated_by = 'user-2',
		    updated_at = CURRENT_TIMESTAMP
		WHERE item_id = 'sku-100'
		  AND (quantity - reserved_quantity) >= 80
		RETURNING item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
	), ins_tx AS (
		INSERT INTO inventory_transactions (
			id, item_id, warehouse_id, transaction_type, quantity, balance_after, reference_id, created_by, updated_by
		)
		SELECT 'tx-2', item_id, 'wh-1', 'RESERVE', 80, (quantity - reserved_quantity), 'user-2', 'user-2', 'user-2'
		FROM upd_inv
	)
	SELECT item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
	FROM upd_inv;
	`
	resFail, err := coord.ExecuteSQL(reserveFailSQL)
	if err != nil {
		t.Fatalf("reserveFail query failed: %v", err)
	}
	if len(resFail.Rows) != 0 {
		t.Fatalf("expected 0 rows for insufficient stock, got %d", len(resFail.Rows))
	}
	if len(resFail.Columns) != 9 {
		t.Fatalf("expected 9 columns even for 0 rows, got %d (%v)", len(resFail.Columns), resFail.Columns)
	}

	// Ensure tx-2 was NOT created
	tx2Check, _ := coord.ExecuteSQL("SELECT id FROM inventory_transactions WHERE id = 'tx-2';")
	if len(tx2Check.Rows) != 0 {
		t.Fatalf("tx-2 should not exist")
	}
}

func TestWritableCTELowering_HighConcurrency(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	setupSQL := `
	CREATE TABLE orders (
		id VARCHAR(64) PRIMARY KEY,
		idempotency_key VARCHAR(64),
		customer_id VARCHAR(64),
		total_amount INT
	);
	CREATE UNIQUE INDEX idx_orders_idempotency ON orders (idempotency_key);

	CREATE TABLE order_items (
		id VARCHAR(64) PRIMARY KEY,
		order_id VARCHAR(64),
		item_id VARCHAR(64)
	);
	`
	if _, err := coord.ExecuteSQL(setupSQL); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	const numWorkers = 50
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			q := fmt.Sprintf(`
			WITH ins_o AS (
				INSERT INTO orders (id, idempotency_key, customer_id, total_amount)
				VALUES ('ord-%d', 'idem-%d', 'cust-%d', %d)
				RETURNING id, customer_id
			), ins_i AS (
				INSERT INTO order_items (id, order_id, item_id)
				VALUES ('oi-%d', 'ord-%d', 'sku-%d')
				RETURNING id, order_id
			)
			SELECT o.id, o.customer_id, i.id, i.order_id
			FROM ins_o o, ins_i i;
			`, workerID, workerID, workerID, workerID*10, workerID, workerID, workerID)

			res, err := coord.ExecuteSQL(q)
			if err != nil {
				errCh <- fmt.Errorf("worker %d failed: %w", workerID, err)
				return
			}
			if len(res.Rows) != 1 {
				errCh <- fmt.Errorf("worker %d expected 1 row, got %d", workerID, len(res.Rows))
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	// Verify all 50 orders exist
	countRes, err := coord.ExecuteSQL("SELECT COUNT(*) AS total FROM orders;")
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if fmt.Sprintf("%v", countRes.Rows[0]["total"]) != "50" {
		t.Fatalf("expected 50 orders, got %v", countRes.Rows[0]["total"])
	}
}
