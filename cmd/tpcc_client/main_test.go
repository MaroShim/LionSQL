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
