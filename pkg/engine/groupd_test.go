package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestGroupD_BUG_ADD_COL_NOT_NULL_1 verifies that ALTER TABLE ADD COLUMN with NOT NULL
// but no DEFAULT is rejected on non-empty tables, and populates the DEFAULT value when provided.
func TestGroupD_BUG_ADD_COL_NOT_NULL_1(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create table and insert existing data
	_, err := coord.ExecuteSQL(`CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO users (id, name) VALUES (1, 'Alice'), (2, 'Bob');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. ALTER TABLE ADD COLUMN NOT NULL without DEFAULT must fail on non-empty table
	_, err = coord.ExecuteSQL(`ALTER TABLE users ADD age INT NOT NULL;`)
	if err == nil {
		t.Fatalf("expected error adding NOT NULL column without DEFAULT to non-empty table, got nil")
	}
	if !strings.Contains(err.Error(), "cannot add NOT NULL column") {
		t.Errorf("unexpected error message: %v", err)
	}

	// 3. ALTER TABLE ADD COLUMN NOT NULL with DEFAULT must succeed and populate existing rows
	res, err := coord.ExecuteSQL(`ALTER TABLE users ADD age INT NOT NULL DEFAULT 30;`)
	if err != nil || !res.Success {
		t.Fatalf("ALTER TABLE with DEFAULT failed: %v", err)
	}

	selRes, err := coord.ExecuteSQL(`SELECT id, name, age FROM users ORDER BY id ASC;`)
	if err != nil || !selRes.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(selRes.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(selRes.Rows))
	}
	for _, row := range selRes.Rows {
		if row["age"] != int64(30) && row["age"] != 30 {
			t.Errorf("expected age=30 for existing row, got: %v", row["age"])
		}
	}

	// 4. On an empty table, adding NOT NULL without DEFAULT is allowed
	_, err = coord.ExecuteSQL(`CREATE TABLE empty_table (id INT PRIMARY KEY);`)
	if err != nil {
		t.Fatalf("CREATE TABLE empty_table failed: %v", err)
	}
	res2, err := coord.ExecuteSQL(`ALTER TABLE empty_table ADD required_code INT NOT NULL;`)
	if err != nil || !res2.Success {
		t.Fatalf("expected adding NOT NULL without DEFAULT to empty table to succeed, got: %v", err)
	}
}

// TestGroupD_BUG_INDEX_SCAN_ORDERBY_1 verifies that multiple ORDER BY columns are not
// incorrectly assumed to be satisfied by an index that only covers the first column or differing directions.
func TestGroupD_BUG_INDEX_SCAN_ORDERBY_1(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create table and single-column index on category
	_, err := coord.ExecuteSQL(`CREATE TABLE products (id INT PRIMARY KEY, category VARCHAR(20), price FLOAT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`CREATE INDEX idx_cat ON products(category);`)
	if err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	// Insert items in same category with un-ordered prices
	_, err = coord.ExecuteSQL(`INSERT INTO products (id, category, price) VALUES 
		(1, 'Electronics', 500.0),
		(2, 'Electronics', 100.0),
		(3, 'Electronics', 300.0),
		(4, 'Electronics', 200.0);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Query with ORDER BY category ASC, price ASC
	// Because idx_cat only covers category, it must sort by price as well
	res, err := coord.ExecuteSQL(`SELECT id, category, price FROM products WHERE category = 'Electronics' ORDER BY category ASC, price ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res.Rows))
	}
	expectedPrices := []float64{100.0, 200.0, 300.0, 500.0}
	for i, row := range res.Rows {
		p, _ := row["price"].(float64)
		if p != expectedPrices[i] {
			t.Errorf("row %d: expected price %v, got %v", i, expectedPrices[i], p)
		}
	}

	// 3. Query with ORDER BY category ASC, price DESC
	resDesc, err := coord.ExecuteSQL(`SELECT id, category, price FROM products WHERE category = 'Electronics' ORDER BY category ASC, price DESC;`)
	if err != nil || !resDesc.Success {
		t.Fatalf("SELECT DESC failed: %v", err)
	}
	expectedPricesDesc := []float64{500.0, 300.0, 200.0, 100.0}
	for i, row := range resDesc.Rows {
		p, _ := row["price"].(float64)
		if p != expectedPricesDesc[i] {
			t.Errorf("row %d: expected price %v, got %v", i, expectedPricesDesc[i], p)
		}
	}
}

// TestGroupD_BUG_EXEC_PROC_TX_BYPASS_1 verifies that stored procedures executed within
// an active client transaction share the transaction and roll back when the transaction is rolled back.
func TestGroupD_BUG_EXEC_PROC_TX_BYPASS_1(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create table and insert initial balance
	_, err := coord.ExecuteSQL(`CREATE TABLE accounts (id INT PRIMARY KEY, balance INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO accounts (id, balance) VALUES (1, 100);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Create procedure modifying accounts
	procSQL := `CREATE PROCEDURE DepositFunds @amount INT AS
BEGIN
	UPDATE accounts SET balance = balance + @amount WHERE id = 1;
END;`
	_, err = coord.ExecuteSQL(procSQL)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// 3. Create client session and begin transaction
	sessionID := "test-session-1"

	// Start explicit transaction
	_, err = coord.ExecuteSessionSQL(sessionID, `BEGIN TRANSACTION;`)
	if err != nil {
		t.Fatalf("BEGIN TRANSACTION failed: %v", err)
	}

	cs, err := coord.GetOrCreateSession(sessionID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	if cs.Tx == nil {
		t.Fatalf("expected active cs.Tx, got nil")
	}

	// Execute procedure inside transaction
	_, err = coord.ExecuteSessionSQL(sessionID, `EXEC DepositFunds @amount = 50;`)
	if err != nil {
		t.Fatalf("EXEC DepositFunds failed: %v", err)
	}

	// Rollback transaction
	_, err = coord.ExecuteSessionSQL(sessionID, `ROLLBACK TRANSACTION;`)
	if err != nil {
		t.Fatalf("ROLLBACK TRANSACTION failed: %v", err)
	}

	// 4. Verify outside transaction that balance is still 100 (not 150)
	checkRes, err := coord.ExecuteSQL(`SELECT balance FROM accounts WHERE id = 1;`)
	if err != nil || !checkRes.Success || len(checkRes.Rows) == 0 {
		t.Fatalf("SELECT failed: %v", err)
	}
	bal := checkRes.Rows[0]["balance"]
	if bal != int64(100) && bal != 100 {
		t.Fatalf("expected balance=100 after rollback, got: %v (procedure bypassed transaction!)", bal)
	}
}

// TestGroupD_BUG_MEM_INDEX_LOCAL_ORDER_1 verifies that IndexScan and IndexScanDesc in MemEngine
// properly merge and sort uncommitted local rows into the secondary index scan stream.
func TestGroupD_BUG_MEM_INDEX_LOCAL_ORDER_1(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create table with secondary index on score
	_, err := coord.ExecuteSQL(`CREATE TABLE leaderboard (id INT PRIMARY KEY, score INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`CREATE INDEX idx_score ON leaderboard(score);`)
	if err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	// Insert committed base rows
	_, err = coord.ExecuteSQL(`INSERT INTO leaderboard (id, score) VALUES (10, 100), (30, 300), (50, 500);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Open an uncommitted transaction and insert interleaved local rows
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	// Insert rows locally that fall before, between, and after committed rows
	err = tx.Insert("leaderboard", storage.Record{"id": storage.IntValue(5), "score": storage.IntValue(50)})
	if err != nil {
		t.Fatalf("local insert 1 failed: %v", err)
	}
	err = tx.Insert("leaderboard", storage.Record{"id": storage.IntValue(25), "score": storage.IntValue(250)})
	if err != nil {
		t.Fatalf("local insert 2 failed: %v", err)
	}
	err = tx.Insert("leaderboard", storage.Record{"id": storage.IntValue(40), "score": storage.IntValue(400)})
	if err != nil {
		t.Fatalf("local insert 3 failed: %v", err)
	}
	err = tx.Insert("leaderboard", storage.Record{"id": storage.IntValue(60), "score": storage.IntValue(600)})
	if err != nil {
		t.Fatalf("local insert 4 failed: %v", err)
	}

	// 3. Scan ascending: scores must be 50, 100, 250, 300, 400, 500, 600
	curAsc, err := tx.IndexScan("leaderboard", "idx_score", nil, nil)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer curAsc.Close()

	expectedAsc := []int64{50, 100, 250, 300, 400, 500, 600}
	var actualAsc []int64
	for curAsc.Next() {
		rec, err := curAsc.Record()
		if err != nil {
			t.Fatalf("Record() failed: %v", err)
		}
		actualAsc = append(actualAsc, rec["score"].IVal)
	}

	if len(actualAsc) != len(expectedAsc) {
		t.Fatalf("expected %d rows in ascending scan, got %d: %v", len(expectedAsc), len(actualAsc), actualAsc)
	}
	for i := range expectedAsc {
		if actualAsc[i] != expectedAsc[i] {
			t.Errorf("ascending pos %d: expected score %d, got %d", i, expectedAsc[i], actualAsc[i])
		}
	}

	// 4. Scan descending: scores must be 600, 500, 400, 300, 250, 100, 50
	curDesc, err := tx.IndexScanDesc("leaderboard", "idx_score", nil, nil)
	if err != nil {
		t.Fatalf("IndexScanDesc failed: %v", err)
	}
	defer curDesc.Close()

	expectedDesc := []int64{600, 500, 400, 300, 250, 100, 50}
	var actualDesc []int64
	for curDesc.Next() {
		rec, err := curDesc.Record()
		if err != nil {
			t.Fatalf("Record() failed: %v", err)
		}
		actualDesc = append(actualDesc, rec["score"].IVal)
	}

	if len(actualDesc) != len(expectedDesc) {
		t.Fatalf("expected %d rows in descending scan, got %d: %v", len(expectedDesc), len(actualDesc), actualDesc)
	}
	for i := range expectedDesc {
		if actualDesc[i] != expectedDesc[i] {
			t.Errorf("descending pos %d: expected score %d, got %d", i, expectedDesc[i], actualDesc[i])
		}
	}
}
