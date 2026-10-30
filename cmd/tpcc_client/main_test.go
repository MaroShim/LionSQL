package main

import (
	"fmt"
	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
	"sync"
	"testing"
	"time"
)

func TestCalcPercentile(t *testing.T) {
	durs := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		5 * time.Millisecond,
	}

	p50 := calcPercentile(durs, 0.50)
	if p50 != 3*time.Millisecond {
		t.Errorf("expected p50 to be 3ms, got %v", p50)
	}

	p99 := calcPercentile(durs, 0.99)
	if p99 != 5*time.Millisecond {
		t.Errorf("expected p99 to be 5ms, got %v", p99)
	}

	// Empty slice test
	if pEmpty := calcPercentile(nil, 0.50); pEmpty != 0 {
		t.Errorf("expected empty percentile to be 0, got %v", pEmpty)
	}
}

func TestTxStatsConcurrency(t *testing.T) {
	var s txStats
	var wg sync.WaitGroup

	numGoroutines := 16
	recordsPerGoroutine := 500

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				ok := (j%10 != 0) // 90% success, 10% abort
				retries := 0
				if !ok {
					retries = 2
				}
				s.record(time.Duration(j)*time.Microsecond, ok, retries)
			}
		}(i)
	}

	wg.Wait()

	expectedTotal := int64(numGoroutines * recordsPerGoroutine)
	if total := s.count.Load(); total != expectedTotal {
		t.Errorf("expected count %d, got %d", expectedTotal, total)
	}

	expectedAborts := int64(numGoroutines * (recordsPerGoroutine / 10))
	if aborts := s.abort.Load(); aborts != expectedAborts {
		t.Errorf("expected aborts %d, got %d", expectedAborts, aborts)
	}

	expectedSuccess := expectedTotal - expectedAborts
	if succ := s.success.Load(); succ != expectedSuccess {
		t.Errorf("expected success %d, got %d", expectedSuccess, succ)
	}

	s.mu.Lock()
	latencyLen := len(s.latencies)
	s.mu.Unlock()

	if latencyLen != int(expectedTotal) {
		t.Errorf("expected %d latencies recorded, got %d", expectedTotal, latencyLen)
	}
}

func TestTPCCProcedureParsing(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// Create tables
	setupSQL := []string{
		`CREATE TABLE warehouse (w_id INT PRIMARY KEY, w_name VARCHAR, w_tax FLOAT, w_ytd FLOAT);`,
		`CREATE TABLE district (d_key VARCHAR PRIMARY KEY, d_id INT, d_w_id INT, d_tax FLOAT, d_ytd FLOAT, d_next_o_id INT);`,
		`CREATE TABLE customer (c_key VARCHAR PRIMARY KEY, c_id INT, c_d_id INT, c_w_id INT, c_first VARCHAR, c_last VARCHAR, c_balance FLOAT, c_ytd_payment FLOAT, c_payment_cnt INT, c_discount FLOAT);`,
		`CREATE TABLE orders (o_key VARCHAR PRIMARY KEY, o_id INT, o_d_id INT, o_w_id INT, o_c_id INT, o_carrier_id INT, o_ol_cnt INT);`,
		`CREATE TABLE new_order (no_key VARCHAR PRIMARY KEY, no_o_id INT, no_d_id INT, no_w_id INT);`,
		`INSERT INTO warehouse VALUES (1, 'wh_1', 0.0825, 300000.0);`,
		`INSERT INTO district VALUES ('1_1', 1, 1, 0.05, 30000.0, 3001);`,
		`INSERT INTO customer VALUES ('1_1_1', 1, 1, 1, 'First', 'Last', -10.0, 10.0, 1, 0.05);`,
	}
	for _, sql := range setupSQL {
		if _, err := coord.ExecuteSQL(sql); err != nil {
			t.Fatalf("setup failed: %v", err)
		}
	}

	createProcSQL := `
CREATE PROCEDURE TPCC_NewOrderHeader
    @w_id INT,
    @d_key VARCHAR,
    @c_key VARCHAR,
    @o_key VARCHAR,
    @next_o_id INT,
    @d_id INT,
    @c_id INT,
    @ol_cnt INT
AS
BEGIN
    DECLARE @w_tax FLOAT;
    DECLARE @c_discount FLOAT;
    SELECT w_tax INTO @w_tax FROM warehouse WHERE w_id = @w_id;
    UPDATE district SET d_next_o_id = d_next_o_id + 1 WHERE d_key = @d_key;
    SELECT c_discount INTO @c_discount FROM customer WHERE c_key = @c_key;
    INSERT INTO orders (o_key, o_id, o_d_id, o_w_id, o_c_id, o_carrier_id, o_ol_cnt)
    VALUES (@o_key, @next_o_id, @d_id, @w_id, @c_id, 0, @ol_cnt);
    INSERT INTO new_order (no_key, no_o_id, no_d_id, no_w_id)
    VALUES (@o_key, @next_o_id, @d_id, @w_id);
END;
`
	if _, err := coord.ExecuteSQL(createProcSQL); err != nil {
		t.Fatalf("create proc failed: %v", err)
	}

	callSQL := `BEGIN TRANSACTION;
EXEC TPCC_NewOrderHeader @w_id=1, @d_key='1_1', @c_key='1_1_1', @o_key='1_1_1001', @next_o_id=1001, @d_id=1, @c_id=1, @ol_cnt=5;
COMMIT TRANSACTION;`

	if _, err := coord.ExecuteSessionSQL("sess-1", callSQL); err != nil {
		t.Fatalf("EXEC failed: %v", err)
	}

	checkRes, err := coord.ExecuteSessionSQL("sess-1", "SELECT COUNT(*) AS cnt FROM orders WHERE o_key = '1_1_1001';")
	if err != nil || len(checkRes.Rows) == 0 {
		t.Fatalf("SELECT check failed: %v", err)
	}
	if fmt.Sprintf("%v", checkRes.Rows[0]["cnt"]) != "1" {
		t.Errorf("expected 1 row in orders, got %v", checkRes.Rows[0]["cnt"])
	}
}

func TestTPCC_NewOrder_UnifiedProcedure(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	setupSQL := []string{
		`CREATE TABLE warehouse (w_id INT PRIMARY KEY, w_name VARCHAR, w_tax FLOAT, w_ytd FLOAT);`,
		`CREATE TABLE district (d_key VARCHAR PRIMARY KEY, d_id INT, d_w_id INT, d_tax FLOAT, d_ytd FLOAT, d_next_o_id INT);`,
		`CREATE TABLE customer (c_key VARCHAR PRIMARY KEY, c_id INT, c_d_id INT, c_w_id INT, c_first VARCHAR, c_last VARCHAR, c_balance FLOAT, c_ytd_payment FLOAT, c_payment_cnt INT, c_discount FLOAT);`,
		`CREATE TABLE orders (o_key VARCHAR PRIMARY KEY, o_id INT, o_d_id INT, o_w_id INT, o_c_id INT, o_carrier_id INT, o_ol_cnt INT);`,
		`CREATE TABLE new_order (no_key VARCHAR PRIMARY KEY, no_o_id INT, no_d_id INT, no_w_id INT);`,
		`CREATE TABLE stock (s_key VARCHAR PRIMARY KEY, s_i_id INT, s_w_id INT, s_quantity INT, s_ytd INT, s_order_cnt INT, s_remote_cnt INT);`,
		`CREATE TABLE order_line (ol_key VARCHAR PRIMARY KEY, ol_o_id INT, ol_d_id INT, ol_w_id INT, ol_number INT, ol_i_id INT, ol_amount FLOAT);`,
		`INSERT INTO warehouse VALUES (1, 'wh_1', 0.0825, 300000.0);`,
		`INSERT INTO district VALUES ('1_1', 1, 1, 0.05, 30000.0, 3001);`,
		`INSERT INTO customer VALUES ('1_1_1', 1, 1, 1, 'First', 'Last', -10.0, 10.0, 1, 0.05);`,
		`INSERT INTO stock VALUES ('1_1', 1, 1, 100, 0, 0, 0);`,
		`INSERT INTO stock VALUES ('1_2', 2, 1, 100, 0, 0, 0);`,
		`INSERT INTO stock VALUES ('1_3', 3, 1, 100, 0, 0, 0);`,
		`INSERT INTO stock VALUES ('1_4', 4, 1, 100, 0, 0, 0);`,
		`INSERT INTO stock VALUES ('1_5', 5, 1, 100, 0, 0, 0);`,
	}
	for _, sql := range setupSQL {
		if _, err := coord.ExecuteSQL(sql); err != nil {
			t.Fatalf("setup failed: %v", err)
		}
	}

	createProcSQL := `
CREATE PROCEDURE TPCC_NewOrder
    @w_id INT,
    @d_key VARCHAR,
    @c_key VARCHAR,
    @o_key VARCHAR,
    @next_o_id INT,
    @d_id INT,
    @c_id INT,
    @ol_cnt INT,
    @s_key1 VARCHAR, @i_id1 INT, @qty1 INT, @ol_key1 VARCHAR, @amt1 FLOAT,
    @s_key2 VARCHAR, @i_id2 INT, @qty2 INT, @ol_key2 VARCHAR, @amt2 FLOAT,
    @s_key3 VARCHAR, @i_id3 INT, @qty3 INT, @ol_key3 VARCHAR, @amt3 FLOAT,
    @s_key4 VARCHAR, @i_id4 INT, @qty4 INT, @ol_key4 VARCHAR, @amt4 FLOAT,
    @s_key5 VARCHAR, @i_id5 INT, @qty5 INT, @ol_key5 VARCHAR, @amt5 FLOAT,
    @s_key6 VARCHAR, @i_id6 INT, @qty6 INT, @ol_key6 VARCHAR, @amt6 FLOAT,
    @s_key7 VARCHAR, @i_id7 INT, @qty7 INT, @ol_key7 VARCHAR, @amt7 FLOAT,
    @s_key8 VARCHAR, @i_id8 INT, @qty8 INT, @ol_key8 VARCHAR, @amt8 FLOAT,
    @s_key9 VARCHAR, @i_id9 INT, @qty9 INT, @ol_key9 VARCHAR, @amt9 FLOAT,
    @s_key10 VARCHAR, @i_id10 INT, @qty10 INT, @ol_key10 VARCHAR, @amt10 FLOAT
AS
BEGIN
    DECLARE @w_tax FLOAT;
    DECLARE @c_discount FLOAT;
    SELECT w_tax INTO @w_tax FROM warehouse WHERE w_id = @w_id;
    UPDATE district SET d_next_o_id = d_next_o_id + 1 WHERE d_key = @d_key;
    SELECT c_discount INTO @c_discount FROM customer WHERE c_key = @c_key;
    INSERT INTO orders (o_key, o_id, o_d_id, o_w_id, o_c_id, o_carrier_id, o_ol_cnt)
    VALUES (@o_key, @next_o_id, @d_id, @w_id, @c_id, 0, @ol_cnt);
    INSERT INTO new_order (no_key, no_o_id, no_d_id, no_w_id)
    VALUES (@o_key, @next_o_id, @d_id, @w_id);

    UPDATE stock SET s_quantity = s_quantity - @qty1, s_ytd = s_ytd + @qty1, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key1;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key1, @next_o_id, @d_id, @w_id, 1, @i_id1, @amt1);

    UPDATE stock SET s_quantity = s_quantity - @qty2, s_ytd = s_ytd + @qty2, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key2;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key2, @next_o_id, @d_id, @w_id, 2, @i_id2, @amt2);

    UPDATE stock SET s_quantity = s_quantity - @qty3, s_ytd = s_ytd + @qty3, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key3;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key3, @next_o_id, @d_id, @w_id, 3, @i_id3, @amt3);

    UPDATE stock SET s_quantity = s_quantity - @qty4, s_ytd = s_ytd + @qty4, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key4;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key4, @next_o_id, @d_id, @w_id, 4, @i_id4, @amt4);

    UPDATE stock SET s_quantity = s_quantity - @qty5, s_ytd = s_ytd + @qty5, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key5;
    INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
    VALUES (@ol_key5, @next_o_id, @d_id, @w_id, 5, @i_id5, @amt5);

    IF @ol_cnt >= 6
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty6, s_ytd = s_ytd + @qty6, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key6;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key6, @next_o_id, @d_id, @w_id, 6, @i_id6, @amt6);
    END;

    IF @ol_cnt >= 7
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty7, s_ytd = s_ytd + @qty7, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key7;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key7, @next_o_id, @d_id, @w_id, 7, @i_id7, @amt7);
    END;

    IF @ol_cnt >= 8
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty8, s_ytd = s_ytd + @qty8, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key8;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key8, @next_o_id, @d_id, @w_id, 8, @i_id8, @amt8);
    END;

    IF @ol_cnt >= 9
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty9, s_ytd = s_ytd + @qty9, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key9;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key9, @next_o_id, @d_id, @w_id, 9, @i_id9, @amt9);
    END;

    IF @ol_cnt >= 10
    BEGIN
        UPDATE stock SET s_quantity = s_quantity - @qty10, s_ytd = s_ytd + @qty10, s_order_cnt = s_order_cnt + 1 WHERE s_key = @s_key10;
        INSERT INTO order_line (ol_key, ol_o_id, ol_d_id, ol_w_id, ol_number, ol_i_id, ol_amount)
        VALUES (@ol_key10, @next_o_id, @d_id, @w_id, 10, @i_id10, @amt10);
    END;
END;
`
	if _, err := coord.ExecuteSQL(createProcSQL); err != nil {
		t.Fatalf("create TPCC_NewOrder failed: %v", err)
	}

	callSQL := `BEGIN TRANSACTION;
EXEC TPCC_NewOrder @w_id=1, @d_key='1_1', @c_key='1_1_1', @o_key='1_1_2001', @next_o_id=2001, @d_id=1, @c_id=1, @ol_cnt=5,
    @s_key1='1_1', @i_id1=1, @qty1=2, @ol_key1='1_1_2001_1', @amt1=21.0,
    @s_key2='1_2', @i_id2=2, @qty2=3, @ol_key2='1_1_2001_2', @amt2=31.5,
    @s_key3='1_3', @i_id3=3, @qty3=1, @ol_key3='1_1_2001_3', @amt3=10.5,
    @s_key4='1_4', @i_id4=4, @qty4=5, @ol_key4='1_1_2001_4', @amt4=52.5,
    @s_key5='1_5', @i_id5=5, @qty5=2, @ol_key5='1_1_2001_5', @amt5=21.0,
    @s_key6='', @i_id6=0, @qty6=0, @ol_key6='', @amt6=0.0,
    @s_key7='', @i_id7=0, @qty7=0, @ol_key7='', @amt7=0.0,
    @s_key8='', @i_id8=0, @qty8=0, @ol_key8='', @amt8=0.0,
    @s_key9='', @i_id9=0, @qty9=0, @ol_key9='', @amt9=0.0,
    @s_key10='', @i_id10=0, @qty10=0, @ol_key10='', @amt10=0.0;
COMMIT TRANSACTION;`

	if _, err := coord.ExecuteSessionSQL("sess-2", callSQL); err != nil {
		t.Fatalf("EXEC TPCC_NewOrder failed: %v", err)
	}

	// Verify order created
	resOrder, err := coord.ExecuteSessionSQL("sess-2", "SELECT COUNT(*) AS cnt FROM orders WHERE o_key = '1_1_2001';")
	if err != nil || len(resOrder.Rows) == 0 || fmt.Sprintf("%v", resOrder.Rows[0]["cnt"]) != "1" {
		t.Fatalf("expected order to be created, got %v", resOrder)
	}

	// Verify order lines (5 created)
	resLines, err := coord.ExecuteSessionSQL("sess-2", "SELECT COUNT(*) AS cnt FROM order_line WHERE ol_o_id = 2001;")
	if err != nil || len(resLines.Rows) == 0 || fmt.Sprintf("%v", resLines.Rows[0]["cnt"]) != "5" {
		t.Fatalf("expected 5 order lines, got %v", resLines)
	}

	// Verify stock deducted for item 1 (100 - 2 = 98)
	resStock, err := coord.ExecuteSessionSQL("sess-2", "SELECT s_quantity AS qty FROM stock WHERE s_key = '1_1';")
	if err != nil || len(resStock.Rows) == 0 || fmt.Sprintf("%v", resStock.Rows[0]["qty"]) != "98" {
		t.Fatalf("expected stock quantity 98, got %v", resStock)
	}

	// Test DeliveryBatch
	createDeliverySQL := `
CREATE PROCEDURE TPCC_DeliveryBatch
    @k1 VARCHAR, @k2 VARCHAR, @k3 VARCHAR, @k4 VARCHAR, @k5 VARCHAR,
    @k6 VARCHAR, @k7 VARCHAR, @k8 VARCHAR, @k9 VARCHAR, @k10 VARCHAR
AS
BEGIN
    DELETE FROM new_order WHERE no_key = @k1;
    DELETE FROM new_order WHERE no_key = @k2;
    DELETE FROM new_order WHERE no_key = @k3;
    DELETE FROM new_order WHERE no_key = @k4;
    DELETE FROM new_order WHERE no_key = @k5;
    DELETE FROM new_order WHERE no_key = @k6;
    DELETE FROM new_order WHERE no_key = @k7;
    DELETE FROM new_order WHERE no_key = @k8;
    DELETE FROM new_order WHERE no_key = @k9;
    DELETE FROM new_order WHERE no_key = @k10;
END;
`
	if _, err := coord.ExecuteSQL(createDeliverySQL); err != nil {
		t.Fatalf("create TPCC_DeliveryBatch failed: %v", err)
	}
	delCallSQL := `EXEC TPCC_DeliveryBatch @k1='1_1_2001', @k2='', @k3='', @k4='', @k5='', @k6='', @k7='', @k8='', @k9='', @k10='';`
	if _, err := coord.ExecuteSessionSQL("sess-2", delCallSQL); err != nil {
		t.Fatalf("EXEC TPCC_DeliveryBatch failed: %v", err)
	}
	resDel, err := coord.ExecuteSessionSQL("sess-2", "SELECT COUNT(*) AS cnt FROM new_order WHERE no_key = '1_1_2001';")
	if err != nil || len(resDel.Rows) == 0 || fmt.Sprintf("%v", resDel.Rows[0]["cnt"]) != "0" {
		t.Fatalf("expected new_order to be deleted by delivery, got %v", resDel)
	}

	// Test StockLevelBatch
	createStockLevelSQL := `
CREATE PROCEDURE TPCC_StockLevelBatch
    @d_key VARCHAR,
    @s1 VARCHAR,
    @s2 VARCHAR,
    @s3 VARCHAR,
    @s4 VARCHAR,
    @s5 VARCHAR
AS
BEGIN
    DECLARE @next_o INT;
    DECLARE @qty INT;
    SELECT d_next_o_id INTO @next_o FROM district WHERE d_key = @d_key;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s1;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s2;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s3;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s4;
    SELECT s_quantity INTO @qty FROM stock WHERE s_key = @s5;
END;
`
	if _, err := coord.ExecuteSQL(createStockLevelSQL); err != nil {
		t.Fatalf("create TPCC_StockLevelBatch failed: %v", err)
	}
	slCallSQL := `EXEC TPCC_StockLevelBatch @d_key='1_1', @s1='1_1', @s2='1_2', @s3='1_3', @s4='1_4', @s5='1_5';`
	if _, err := coord.ExecuteSessionSQL("sess-2", slCallSQL); err != nil {
		t.Fatalf("EXEC TPCC_StockLevelBatch failed: %v", err)
	}
}
