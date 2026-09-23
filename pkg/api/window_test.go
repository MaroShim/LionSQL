package api_test

import (
	"testing"
)

func TestWindow_RowNumber_PartitionAndOrder(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE emp (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		dept VARCHAR(50) NOT NULL,
		salary FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO emp (id, name, dept, salary) VALUES
		(1, 'Alice', 'Engineering', 1000.0),
		(2, 'Bob', 'Engineering', 1200.0),
		(3, 'Charlie', 'Engineering', 900.0),
		(4, 'Dave', 'Sales', 800.0),
		(5, 'Eve', 'Sales', 950.0);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	query := `SELECT name, dept, salary, ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) AS rn FROM emp;`
	res, err := server.ExecuteSQL(query)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v (res: %+v)", err, res)
	}

	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(res.Rows))
	}

	// Group rows by dept to check ranks
	engRanks := make(map[string]int64)
	salesRanks := make(map[string]int64)
	for _, r := range res.Rows {
		dept := r["dept"].(string)
		name := r["name"].(string)
		var rn int64
		switch v := r["rn"].(type) {
		case int64:
			rn = v
		case int:
			rn = int64(v)
		case float64:
			rn = int64(v)
		}
		if dept == "Engineering" {
			engRanks[name] = rn
		} else {
			salesRanks[name] = rn
		}
	}

	// Engineering: Bob (1200) -> 1, Alice (1000) -> 2, Charlie (900) -> 3
	if engRanks["Bob"] != 1 || engRanks["Alice"] != 2 || engRanks["Charlie"] != 3 {
		t.Fatalf("unexpected Engineering ranks: %+v", engRanks)
	}

	// Sales: Eve (950) -> 1, Dave (800) -> 2
	if salesRanks["Eve"] != 1 || salesRanks["Dave"] != 2 {
		t.Fatalf("unexpected Sales ranks: %+v", salesRanks)
	}
}

func TestWindow_RankAndDenseRank_Ties(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE scores (
		id INT PRIMARY KEY,
		player VARCHAR(20) NOT NULL,
		score INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO scores (id, player, score) VALUES
		(1, 'P1', 100),
		(2, 'P2', 90),
		(3, 'P3', 90),
		(4, 'P4', 80);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	query := `SELECT player, score,
		RANK() OVER (ORDER BY score DESC) AS rk,
		DENSE_RANK() OVER (ORDER BY score DESC) AS drk
		FROM scores;`
	res, err := server.ExecuteSQL(query)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v (res: %+v)", err, res)
	}

	rkMap := make(map[string]int64)
	drkMap := make(map[string]int64)
	for _, r := range res.Rows {
		p := r["player"].(string)
		var rk, drk int64
		switch v := r["rk"].(type) {
		case int64:
			rk = v
		case int:
			rk = int64(v)
		case float64:
			rk = int64(v)
		}
		switch v := r["drk"].(type) {
		case int64:
			drk = v
		case int:
			drk = int64(v)
		case float64:
			drk = int64(v)
		}
		rkMap[p] = rk
		drkMap[p] = drk
	}

	// P1 (100): rank=1, dense_rank=1
	if rkMap["P1"] != 1 || drkMap["P1"] != 1 {
		t.Fatalf("P1 rank expected 1/1, got %d/%d", rkMap["P1"], drkMap["P1"])
	}

	// P2 & P3 (90): rank=2, dense_rank=2
	if rkMap["P2"] != 2 || drkMap["P2"] != 2 {
		t.Fatalf("P2 rank expected 2/2, got %d/%d", rkMap["P2"], drkMap["P2"])
	}
	if rkMap["P3"] != 2 || drkMap["P3"] != 2 {
		t.Fatalf("P3 rank expected 2/2, got %d/%d", rkMap["P3"], drkMap["P3"])
	}

	// P4 (80): rank=4 (skip for tie), dense_rank=3 (no skip)
	if rkMap["P4"] != 4 {
		t.Fatalf("P4 rank with tie skip expected 4, got %d", rkMap["P4"])
	}
	if drkMap["P4"] != 3 {
		t.Fatalf("P4 dense_rank without tie skip expected 3, got %d", drkMap["P4"])
	}
}

func TestWindow_LeadAndLag(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE sales (
		id INT PRIMARY KEY,
		day_num INT NOT NULL,
		amount FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO sales (id, day_num, amount) VALUES
		(1, 1, 100.0),
		(2, 2, 150.0),
		(3, 3, 200.0);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	query := `SELECT day_num, amount,
		LAG(amount, 1, 0) OVER (ORDER BY day_num ASC) AS prev_amt,
		LEAD(amount, 1, 0) OVER (ORDER BY day_num ASC) AS next_amt
		FROM sales;`
	res, err := server.ExecuteSQL(query)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v (res: %+v)", err, res)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}

	for _, r := range res.Rows {
		day := int(r["day_num"].(int64))
		var prev, next float64
		switch v := r["prev_amt"].(type) {
		case float64:
			prev = v
		case int64:
			prev = float64(v)
		case int:
			prev = float64(v)
		}
		switch v := r["next_amt"].(type) {
		case float64:
			next = v
		case int64:
			next = float64(v)
		case int:
			next = float64(v)
		}

		switch day {
		case 1:
			if prev != 0.0 || next != 150.0 {
				t.Fatalf("day 1 expected prev=0 next=150, got prev=%v next=%v", prev, next)
			}
		case 2:
			if prev != 100.0 || next != 200.0 {
				t.Fatalf("day 2 expected prev=100 next=200, got prev=%v next=%v", prev, next)
			}
		case 3:
			if prev != 150.0 || next != 0.0 {
				t.Fatalf("day 3 expected prev=150 next=0, got prev=%v next=%v", prev, next)
			}
		}
	}
}

func TestWindow_GlobalWithoutPartition(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE items (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO items (id, name) VALUES
		(10, 'Pen'),
		(20, 'Notebook'),
		(30, 'Eraser');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	res, err := server.ExecuteSQL(`SELECT name, ROW_NUMBER() OVER (ORDER BY id ASC) AS rn FROM items;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
}

func TestWindow_WindowAggregates(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE departments_sal (
		id INT PRIMARY KEY,
		dept VARCHAR(50) NOT NULL,
		salary FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO departments_sal (id, dept, salary) VALUES
		(1, 'Eng', 1000.0),
		(2, 'Eng', 2000.0),
		(3, 'HR', 1500.0);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	res, err := server.ExecuteSQL(`SELECT id, dept, salary, SUM(salary) OVER (PARTITION BY dept) AS dept_total FROM departments_sal;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}

	for _, r := range res.Rows {
		dept := r["dept"].(string)
		var total float64
		switch v := r["dept_total"].(type) {
		case float64:
			total = v
		case int64:
			total = float64(v)
		}
		if dept == "Eng" && total != 3000.0 {
			t.Fatalf("expected Eng total 3000, got %v", total)
		}
		if dept == "HR" && total != 1500.0 {
			t.Fatalf("expected HR total 1500, got %v", total)
		}
	}
}

func TestWindow_InCTE(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE staff (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		dept VARCHAR(50) NOT NULL,
		salary FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO staff (id, name, dept, salary) VALUES
		(1, 'Alice', 'Engineering', 1000.0),
		(2, 'Bob', 'Engineering', 1200.0),
		(3, 'Charlie', 'Sales', 900.0),
		(4, 'Dave', 'Sales', 1100.0);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// CTE with window function filter
	cteQuery := `WITH ranked_staff AS (
		SELECT name, dept, salary, ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) AS rn FROM staff
	)
	SELECT name, dept, salary FROM ranked_staff WHERE rn = 1;`
	res, err := server.ExecuteSQL(cteQuery)
	if err != nil || !res.Success {
		t.Fatalf("CTE with window function failed: %v (res: %+v)", err, res)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 top earners, got %d: %+v", len(res.Rows), res.Rows)
	}

	topStaff := make(map[string]string)
	for _, r := range res.Rows {
		topStaff[r["dept"].(string)] = r["name"].(string)
	}

	if topStaff["Engineering"] != "Bob" {
		t.Fatalf("expected Bob as top Engineer, got %s", topStaff["Engineering"])
	}
	if topStaff["Sales"] != "Dave" {
		t.Fatalf("expected Dave as top Sales, got %s", topStaff["Sales"])
	}
}
