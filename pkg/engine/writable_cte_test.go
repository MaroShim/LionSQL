package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestWritableCTEGroveChecklists(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})

	// Setup schemas matching Grove's CreateWithOutbox, CreateJournal, and ReserveWithTxReturning
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
		transaction_type VARCHAR(64),
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

	// 1. Checklist #1: Multiple Writable CTEs in a single WITH clause (CreateWithOutbox)
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
	res1, err := coord.ExecuteSQL(createWithOutboxSQL)
	if err != nil {
		t.Fatalf("CreateWithOutbox failed: %v", err)
	}
	if len(res1.Rows) != 1 {
		t.Fatalf("expected 1 row from CreateWithOutbox, got %d", len(res1.Rows))
	}
	if len(res1.Columns) != 6 {
		t.Fatalf("expected 6 columns from CreateWithOutbox, got %d (%v)", len(res1.Columns), res1.Columns)
	}
	if len(res1.Values) != 1 || len(res1.Values[0]) != 6 {
		t.Fatalf("expected 6 positional values from CreateWithOutbox, got %v", res1.Values)
	}
	for colIdx, val := range res1.Values[0] {
		if val == nil || fmt.Sprintf("%v", val) == "" {
			t.Fatalf("column %d in CreateWithOutbox returned empty value: %v", colIdx, val)
		}
	}

	// 2. Checklist #3: Single Atomic Transaction Guarantee (All-or-Nothing Rollback on 23505 Unique Violation)
	// Attempt to insert a new order ('ord-2') that fails in the 2nd or 1st CTE due to duplicate key.
	// Here let's make ins_order succeed ('ord-2', 'idem-2') and ins_outbox fail on duplicate primary key ('evt-1')!
	atomicRollbackSQL := `
	WITH ins_order AS (
		INSERT INTO orders (id, idempotency_key, customer_id, total_amount)
		VALUES ('ord-2', 'idem-2', 'cust-2', 20000)
		RETURNING created_at, updated_at
	), ins_item AS (
		INSERT INTO order_items (id, order_id, item_id, quantity)
		VALUES ('oi-2', 'ord-2', 'sku-100', 1)
		RETURNING created_at, updated_at
	), ins_outbox AS (
		INSERT INTO outbox_events (id, aggregate_id, event_type)
		VALUES ('evt-1', 'ord-2', 'ORDER_CREATED')
		RETURNING created_at, updated_at
	)
	SELECT o.created_at, o.updated_at, i.created_at, i.updated_at, e.created_at, e.updated_at
	FROM ins_order o, ins_item i, ins_outbox e;
	`
	_, err = coord.ExecuteSQL(atomicRollbackSQL)
	if err == nil {
		t.Fatalf("expected unique constraint violation error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "duplicate key") && !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected duplicate key / unique constraint message, got: %v", err)
	}

	// Verify that 'ord-2' and 'oi-2' from earlier CTEs were atomically rolled back!
	chkOrd, err := coord.ExecuteSQL("SELECT id FROM orders WHERE id = 'ord-2';")
	if err != nil {
		t.Fatalf("check orders failed: %v", err)
	}
	if len(chkOrd.Rows) != 0 {
		t.Fatalf("expected ord-2 to be rolled back atomically, but found %d rows", len(chkOrd.Rows))
	}
	chkItem, err := coord.ExecuteSQL("SELECT id FROM order_items WHERE id = 'oi-2';")
	if err != nil {
		t.Fatalf("check order_items failed: %v", err)
	}
	if len(chkItem.Rows) != 0 {
		t.Fatalf("expected oi-2 to be rolled back atomically, but found %d rows", len(chkItem.Rows))
	}

	// Also verify unique index violation on orders.idempotency_key ('idem-1')
	dupIdemSQL := `
	WITH ins_order AS (
		INSERT INTO orders (id, idempotency_key, customer_id, total_amount)
		VALUES ('ord-3', 'idem-1', 'cust-3', 30000)
		RETURNING created_at, updated_at
	)
	SELECT created_at, updated_at FROM ins_order;
	`
	_, err = coord.ExecuteSQL(dupIdemSQL)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
		t.Fatalf("expected duplicate key error on orders.idempotency_key, got: %v", err)
	}

	// 3. Checklist #2: ReserveWithTxReturning (Post-Update RETURNING + Unreferenced CTE + 0-Row Match)
	_, err = coord.ExecuteSQL(`
		INSERT INTO inventories (item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_by, updated_by)
		VALUES ('sku-100', 'wh-1', 100, 20, 10, 'system', 'system');
	`)
	if err != nil {
		t.Fatalf("seed inventory failed: %v", err)
	}

	// 3a. Sufficient stock: reserve 5 units (reserved_quantity: 20 -> 25, balance_after: 100 - 25 = 75)
	reserveOKSQL := `
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
	resReserve, err := coord.ExecuteSQL(reserveOKSQL)
	if err != nil {
		t.Fatalf("ReserveWithTxReturning (sufficient stock) failed: %v", err)
	}
	if len(resReserve.Rows) != 1 {
		t.Fatalf("expected 1 row from ReserveWithTxReturning, got %d", len(resReserve.Rows))
	}
	if fmt.Sprintf("%v", resReserve.Rows[0]["reserved_quantity"]) != "25" {
		t.Fatalf("expected post-update reserved_quantity=25, got %v", resReserve.Rows[0]["reserved_quantity"])
	}
	if fmt.Sprintf("%v", resReserve.Rows[0]["updated_by"]) != "user-1" {
		t.Fatalf("expected post-update updated_by='user-1', got %v", resReserve.Rows[0]["updated_by"])
	}

	// Verify unreferenced CTE (ins_tx) was materialized and inserted post-update balance_after = 75
	txCheck, err := coord.ExecuteSQL("SELECT id, quantity, balance_after, created_by, updated_by FROM inventory_transactions WHERE id = 'tx-1';")
	if err != nil {
		t.Fatalf("check inventory_transactions failed: %v", err)
	}
	if len(txCheck.Rows) != 1 {
		t.Fatalf("expected unreferenced CTE ins_tx to insert 1 row into inventory_transactions, got %d", len(txCheck.Rows))
	}
	if fmt.Sprintf("%v", txCheck.Rows[0]["balance_after"]) != "75" {
		t.Fatalf("expected post-update balance_after=75, got %v", txCheck.Rows[0]["balance_after"])
	}
	if fmt.Sprintf("%v", txCheck.Rows[0]["updated_by"]) != "user-1" {
		t.Fatalf("expected updated_by='user-1', got %v", txCheck.Rows[0]["updated_by"])
	}

	// 3b. Insufficient stock (0-row match): try to reserve 80 units when only 75 are available
	reserveInsufficientSQL := `
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
	resInsuff, err := coord.ExecuteSQL(reserveInsufficientSQL)
	if err != nil {
		t.Fatalf("ReserveWithTxReturning (0-row match) should not error, got: %v", err)
	}
	if len(resInsuff.Rows) != 0 {
		t.Fatalf("expected 0 rows on insufficient stock, got %d", len(resInsuff.Rows))
	}

	// Verify ins_tx inserted 0 rows when upd_inv matched 0 rows
	txCheck2, err := coord.ExecuteSQL("SELECT id FROM inventory_transactions WHERE id = 'tx-2';")
	if err != nil {
		t.Fatalf("check inventory_transactions tx-2 failed: %v", err)
	}
	if len(txCheck2.Rows) != 0 {
		t.Fatalf("expected 0 rows inserted for tx-2, got %d", len(txCheck2.Rows))
	}
}
