package wire

import (
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func execExtendedQuery(t *testing.T, srv *Server, stmtName string, query string, params []string) (paramOIDs []int32, rowDescCols []string, dataRows [][]string, errSQLState string, errMsg string) {
	t.Helper()
	conn := connectPGClient(t, srv)

	// 1. Parse ('P') with 0 client-specified param types so server runs inferParamTypes
	sName := stmtName + "\x00"
	qStr := query + "\x00"
	parsePayload := append([]byte(sName), []byte(qStr)...)
	parsePayload = append(parsePayload, 0, 0) // 0 explicit param types

	parseMsg := make([]byte, 5+len(parsePayload))
	parseMsg[0] = 'P'
	binary.BigEndian.PutUint32(parseMsg[1:5], uint32(4+len(parsePayload)))
	copy(parseMsg[5:], parsePayload)
	if _, err := conn.Write(parseMsg); err != nil {
		t.Fatalf("write Parse failed: %v", err)
	}

	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read ParseComplete failed: %v", err)
	}
	if header[0] != MsgParseComplete {
		t.Fatalf("expected ParseComplete ('1'), got %c", header[0])
	}

	// 2. Describe Statement ('D' 'S' stmtName)
	descStmtPayload := append([]byte{'S'}, []byte(sName)...)
	descStmtMsg := make([]byte, 5+len(descStmtPayload))
	descStmtMsg[0] = 'D'
	binary.BigEndian.PutUint32(descStmtMsg[1:5], uint32(4+len(descStmtPayload)))
	copy(descStmtMsg[5:], descStmtPayload)
	if _, err := conn.Write(descStmtMsg); err != nil {
		t.Fatalf("write Describe Statement failed: %v", err)
	}

	// Read ParameterDescription ('t')
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read ParameterDescription failed: %v", err)
	}
	pLen := binary.BigEndian.Uint32(header[1:5])
	pBuf := make([]byte, pLen-4)
	if _, err := io.ReadFull(conn, pBuf); err != nil {
		t.Fatalf("read ParameterDescription payload failed: %v", err)
	}
	if header[0] == MsgParameterDescription && len(pBuf) >= 2 {
		nParams := int(binary.BigEndian.Uint16(pBuf[:2]))
		for i := 0; i < nParams && 2+(i+1)*4 <= len(pBuf); i++ {
			oid := int32(binary.BigEndian.Uint32(pBuf[2+i*4 : 2+(i+1)*4]))
			paramOIDs = append(paramOIDs, oid)
		}
	}

	// Read RowDescription ('T') or NoData ('n')
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read RowDescription failed: %v", err)
	}
	rLen := binary.BigEndian.Uint32(header[1:5])
	rBuf := make([]byte, rLen-4)
	if _, err := io.ReadFull(conn, rBuf); err != nil {
		t.Fatalf("read RowDescription payload failed: %v", err)
	}
	if header[0] == MsgRowDescription && len(rBuf) >= 2 {
		nCols := int(binary.BigEndian.Uint16(rBuf[:2]))
		pos := 2
		for i := 0; i < nCols && pos < len(rBuf); i++ {
			nulIdx := pos
			for nulIdx < len(rBuf) && rBuf[nulIdx] != 0 {
				nulIdx++
			}
			rowDescCols = append(rowDescCols, string(rBuf[pos:nulIdx]))
			pos = nulIdx + 1 + 18
		}
	}

	// 3. Bind ('B')
	portalName := "p1\x00"
	bindPayload := append([]byte(portalName), []byte(sName)...)
	bindPayload = append(bindPayload, 0, 0) // 0 format codes (all text)
	numP := make([]byte, 2)
	binary.BigEndian.PutUint16(numP, uint16(len(params)))
	bindPayload = append(bindPayload, numP...)
	for _, p := range params {
		vLen := make([]byte, 4)
		binary.BigEndian.PutUint32(vLen, uint32(len(p)))
		bindPayload = append(bindPayload, vLen...)
		bindPayload = append(bindPayload, []byte(p)...)
	}
	bindPayload = append(bindPayload, 0, 0) // 0 result format codes (all text)

	bindMsg := make([]byte, 5+len(bindPayload))
	bindMsg[0] = 'B'
	binary.BigEndian.PutUint32(bindMsg[1:5], uint32(4+len(bindPayload)))
	copy(bindMsg[5:], bindPayload)
	if _, err := conn.Write(bindMsg); err != nil {
		t.Fatalf("write Bind failed: %v", err)
	}

	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read BindComplete failed: %v", err)
	}
	if header[0] != MsgBindComplete {
		t.Fatalf("expected BindComplete ('2'), got %c", header[0])
	}

	// 4. Execute ('E') + Sync ('S') sent asynchronously to avoid unbuffered net.Pipe deadlock
	go func() {
		execPayload := []byte{'p', '1', 0, 0, 0, 0, 0}
		execMsg := make([]byte, 5+len(execPayload))
		execMsg[0] = 'E'
		binary.BigEndian.PutUint32(execMsg[1:5], uint32(4+len(execPayload)))
		copy(execMsg[5:], execPayload)
		_, _ = conn.Write(execMsg)

		syncMsg := []byte{'S', 0, 0, 0, 4}
		_, _ = conn.Write(syncMsg)
	}()

	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Fatalf("read Execute response failed: %v", err)
		}
		mLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("read Execute payload failed: %v", err)
		}
		switch header[0] {
		case MsgDataRow:
			if len(payload) >= 2 {
				nCols := int(binary.BigEndian.Uint16(payload[:2]))
				pos := 2
				var rowVals []string
				for i := 0; i < nCols && pos+4 <= len(payload); i++ {
					cLen := int(int32(binary.BigEndian.Uint32(payload[pos : pos+4])))
					pos += 4
					if cLen == -1 {
						rowVals = append(rowVals, "NULL")
					} else {
						rowVals = append(rowVals, string(payload[pos:pos+cLen]))
						pos += cLen
					}
				}
				dataRows = append(dataRows, rowVals)
			}
		case MsgErrorResponse:
			pos := 0
			for pos < len(payload) && payload[pos] != 0 {
				fieldType := payload[pos]
				pos++
				end := pos
				for end < len(payload) && payload[end] != 0 {
					end++
				}
				val := string(payload[pos:end])
				if fieldType == 'C' {
					errSQLState = val
				} else if fieldType == 'M' {
					errMsg = val
				}
				pos = end + 1
			}
		case MsgReadyForQuery:
			return
		}
	}
}

func TestPGWireWritableCTEGroveIntegration(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	srv := NewServer("127.0.0.1:0", cat, eng)

	conn := connectPGClient(t, srv)
	setupSQL := `
	CREATE TABLE orders (
		id VARCHAR(64) PRIMARY KEY,
		idempotency_key VARCHAR(64),
		customer_id VARCHAR(64),
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE UNIQUE INDEX idx_orders_idem ON orders (idempotency_key);
	CREATE TABLE order_items (
		id VARCHAR(64) PRIMARY KEY,
		order_id VARCHAR(64),
		item_id VARCHAR(64),
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
	INSERT INTO inventories (item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_by, updated_by)
	VALUES ('sku-1', 'wh-main', 50, 10, 5, 'admin', 'admin');
	`
	sendPGQuery(t, conn, setupSQL)

	// 1. Test CreateWithOutbox with parameter $1 reused across ins_order, ins_item, ins_outbox
	createWithOutboxQuery := `
	WITH ins_order AS (
		INSERT INTO orders (id, idempotency_key, customer_id)
		VALUES ($1, $2, $3)
		RETURNING created_at, updated_at
	), ins_item AS (
		INSERT INTO order_items (id, order_id, item_id)
		VALUES ($4, $1, $5)
		RETURNING created_at, updated_at
	), ins_outbox AS (
		INSERT INTO outbox_events (id, aggregate_id, event_type)
		VALUES ($6, $1, $7)
		RETURNING created_at, updated_at
	)
	SELECT o.created_at, o.updated_at, i.created_at, i.updated_at, e.created_at, e.updated_at
	FROM ins_order o, ins_item i, ins_outbox e
	`
	pOIDs, descCols, rows, sqlState, errMsg := execExtendedQuery(
		t, srv, "stmt_outbox", createWithOutboxQuery,
		[]string{"ord-100", "idem-100", "cust-100", "item-100", "sku-1", "evt-100", "ORDER_CREATED"},
	)
	if sqlState != "" || errMsg != "" {
		t.Fatalf("CreateWithOutbox failed: state=%s, msg=%s", sqlState, errMsg)
	}
	if len(pOIDs) != 7 {
		t.Fatalf("expected 7 inferred param OIDs, got %v", pOIDs)
	}
	for i, oid := range pOIDs {
		if oid != 1043 && oid != 25 { // VARCHAR(1043) or TEXT(25)
			t.Fatalf("expected param $%d OID=1043 or 25, got %d", i+1, oid)
		}
	}
	if len(descCols) != 6 {
		t.Fatalf("expected 6 RowDescription columns, got %v", descCols)
	}
	if len(rows) != 1 || len(rows[0]) != 6 {
		t.Fatalf("expected 1 row with 6 columns from CreateWithOutbox, got %v", rows)
	}

	// Verify Describe ('D') did NOT execute preview INSERT on orders!
	cntRows := sendPGQuery(t, conn, "SELECT COUNT(*) FROM orders;")
	if len(cntRows) == 0 || !strings.Contains(string(cntRows[0]), "1") {
		t.Fatalf("expected exactly 1 row in orders, got %s", string(cntRows[0]))
	}

	// 2. Test Atomic Rollback & SQLSTATE 23505 when 3rd CTE (ins_outbox) fails on duplicate key 'evt-100'
	_, _, _, sqlState, errMsg = execExtendedQuery(
		t, srv, "stmt_outbox_dup", createWithOutboxQuery,
		[]string{"ord-101", "idem-101", "cust-101", "item-101", "sku-1", "evt-100", "ORDER_CREATED"},
	)
	if sqlState != "23505" {
		t.Fatalf("expected PostgreSQL SQLSTATE 23505 on unique constraint violation, got state=%q msg=%q", sqlState, errMsg)
	}
	// Verify ord-101 from 1st CTE was rolled back atomically
	ordCheck := sendPGQuery(t, conn, "SELECT id FROM orders WHERE id = 'ord-101';")
	if len(ordCheck) != 0 {
		t.Fatalf("expected ord-101 to be atomically rolled back, got %d rows", len(ordCheck))
	}

	// 3. Test ReserveWithTxReturning WITHOUT :: type casts (Grove original SQL)
	reserveQuery := `
	WITH upd_inv AS (
		UPDATE inventories
		SET reserved_quantity = reserved_quantity + $2,
		    updated_by = $3,
		    updated_at = CURRENT_TIMESTAMP
		WHERE item_id = $1
		  AND (quantity - reserved_quantity) >= $2
		RETURNING item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
	), ins_tx AS (
		INSERT INTO inventory_transactions (
			id, item_id, warehouse_id, transaction_type, quantity, balance_after, reference_id, created_by, updated_by
		)
		SELECT $4, item_id, $5, 'RESERVE', $2, (quantity - reserved_quantity), $3, $3, $3
		FROM upd_inv
	)
	SELECT item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
	FROM upd_inv
	`
	pOIDs, descCols, rows, sqlState, errMsg = execExtendedQuery(
		t, srv, "stmt_reserve", reserveQuery,
		[]string{"sku-1", "15", "operator-1", "inv-tx-1", "wh-main"},
	)
	if sqlState != "" || errMsg != "" {
		t.Fatalf("ReserveWithTxReturning failed: state=%s, msg=%s", sqlState, errMsg)
	}
	// Verify inferred parameter OIDs for $1..$5:
	// $1 (item_id: VARCHAR=1043), $2 (quantity: INT4=23), $3 (updated_by: VARCHAR=1043), $4 (id: VARCHAR=1043), $5 (warehouse_id: VARCHAR=1043)
	expectedOIDs := []int32{1043, 23, 1043, 1043, 1043}
	if len(pOIDs) != 5 {
		t.Fatalf("expected 5 param OIDs, got %v", pOIDs)
	}
	for i, want := range expectedOIDs {
		if pOIDs[i] != want {
			t.Fatalf("param $%d OID mismatch: got %d, want %d", i+1, pOIDs[i], want)
		}
	}
	if len(rows) != 1 || len(rows[0]) != 9 {
		t.Fatalf("expected 1 row with 9 columns from ReserveWithTxReturning, got %v", rows)
	}
	// Post-update reserved_quantity (col 3) should be 10 + 15 = 25
	if rows[0][3] != "25" {
		t.Fatalf("expected post-update reserved_quantity=25, got %q", rows[0][3])
	}

	// Verify unreferenced CTE ins_tx was executed and recorded balance_after = 50 - 25 = 25
	txRows := sendPGQuery(t, conn, "SELECT balance_after FROM inventory_transactions WHERE id = 'inv-tx-1';")
	if len(txRows) != 1 || !strings.Contains(string(txRows[0]), "25") {
		t.Fatalf("expected balance_after=25 in inventory_transactions, got %v", txRows)
	}

	// 4. Test ReserveWithTxReturning 0-row match (insufficient stock) returns 0 rows cleanly (pgx.ErrNoRows)
	_, _, rows0, sqlState, errMsg := execExtendedQuery(
		t, srv, "stmt_reserve_insuff", reserveQuery,
		[]string{"sku-1", "999", "operator-2", "inv-tx-2", "wh-main"},
	)
	if sqlState != "" || errMsg != "" {
		t.Fatalf("ReserveWithTxReturning (0-row match) should not error: state=%s, msg=%s", sqlState, errMsg)
	}
	if len(rows0) != 0 {
		t.Fatalf("expected 0 rows on insufficient stock, got %v", rows0)
	}

	// 5. Test explicit :: type casts ($4::text, $2::int4) and ANY($1::text[])
	reserveCastQuery := `
	WITH upd_inv AS (
		UPDATE inventories
		SET reserved_quantity = reserved_quantity + $2::int4,
		    updated_by = $3::text,
		    updated_at = CURRENT_TIMESTAMP
		WHERE item_id = ANY($1::text[])
		  AND (quantity - reserved_quantity) >= $2::int4
		RETURNING item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
	), ins_tx AS (
		INSERT INTO inventory_transactions (
			id, item_id, warehouse_id, transaction_type, quantity, balance_after, reference_id, created_by, updated_by
		)
		SELECT $4::text, item_id, $5::text, 'RESERVE', $2::int4, (quantity - reserved_quantity), $3::text, $3::text, $3::text
		FROM upd_inv
	)
	SELECT item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
	FROM upd_inv
	`
	pOIDsCast, _, rowsCast, sqlState, errMsg := execExtendedQuery(
		t, srv, "stmt_reserve_cast", reserveCastQuery,
		[]string{"{sku-1}", "5", "operator-cast", "inv-tx-cast", "wh-main"},
	)
	if sqlState != "" || errMsg != "" {
		t.Fatalf("ReserveWithTxReturning with :: casts and ANY($1::text[]) failed: state=%s, msg=%s", sqlState, errMsg)
	}
	// Verify $1::text[] -> 1009 (_text), $2::int4 -> 23 (int4), $3::text -> 25 (text), $4::text -> 25, $5::text -> 25
	expectedCastOIDs := []int32{1009, 23, 25, 25, 25}
	for i, want := range expectedCastOIDs {
		if i >= len(pOIDsCast) || pOIDsCast[i] != want {
			t.Fatalf("cast param $%d OID mismatch: got %v, want %v", i+1, pOIDsCast, expectedCastOIDs)
		}
	}
	if len(rowsCast) != 1 || rowsCast[0][3] != "30" { // 25 + 5 = 30
		t.Fatalf("expected post-update reserved_quantity=30 with :: casts, got %v", rowsCast)
	}
}
