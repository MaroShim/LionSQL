package api_test

import (
	"fmt"
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func setupEmployeesTestServer(t *testing.T) (*api.Server, func()) {
	server, cleanup := setupMemServer(t)
	execStatements(t, server,
		`CREATE TABLE employees (id INT PRIMARY KEY, dept VARCHAR(50), salary INT);`,
		`INSERT INTO employees (id, dept, salary) VALUES
			(1, 'Eng', 10), (2, 'Eng', 20), (3, 'Eng', 30), (4, 'Eng', 40),
			(5, 'Sales', 50), (6, 'Sales', 100), (7, 'Sales', 150);`,
	)
	return server, cleanup
}

func TestSQLExecutor_Percentile_GroupBy(t *testing.T) {
	server, cleanup := setupEmployeesTestServer(t)
	defer cleanup()

	// 1. ANSI WITHIN GROUP syntax
	q1 := `SELECT dept, 
		PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY salary) AS med_cont, 
		PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY salary) AS med_disc 
		FROM employees GROUP BY dept;`
	res1, err := server.ExecuteSQL(q1)
	if err != nil || !res1.Success {
		t.Fatalf("Query 1 failed: %v, res: %+v", err, res1)
	}
	if len(res1.Rows) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(res1.Rows))
	}

	for _, row := range res1.Rows {
		dept := fmt.Sprintf("%v", row["dept"])
		cont := fmt.Sprintf("%v", row["med_cont"])
		disc := fmt.Sprintf("%v", row["med_disc"])

		if dept == "Eng" {
			if cont != "25" {
				t.Errorf("Eng expected med_cont=25, got %v", cont)
			}
			if disc != "20" {
				t.Errorf("Eng expected med_disc=20, got %v", disc)
			}
		} else if dept == "Sales" {
			if cont != "100" {
				t.Errorf("Sales expected med_cont=100, got %v", cont)
			}
			if disc != "100" {
				t.Errorf("Sales expected med_disc=100, got %v", disc)
			}
		}
	}

	// 2. 2-argument standard function syntax
	q2 := `SELECT dept, 
		PERCENTILE_CONT(salary, 0.5) AS med_cont, 
		PERCENTILE_DISC(salary, 0.5) AS med_disc 
		FROM employees GROUP BY dept;`
	res2, err := server.ExecuteSQL(q2)
	if err != nil || !res2.Success {
		t.Fatalf("Query 2 failed: %v, res: %+v", err, res2)
	}
	if len(res2.Rows) != 2 {
		t.Fatalf("expected 2 groups for Q2, got %d", len(res2.Rows))
	}

	for _, row := range res2.Rows {
		dept := fmt.Sprintf("%v", row["dept"])
		cont := fmt.Sprintf("%v", row["med_cont"])
		if dept == "Eng" && cont != "25" {
			t.Errorf("Eng expected med_cont=25, got %v", cont)
		}
	}
}

func TestSQLExecutor_ApproxPercentileAndMedian(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)
	defer eng.Close()

	_, err := server.ExecuteSQL(`CREATE TABLE api_logs (
		id INT PRIMARY KEY,
		endpoint VARCHAR(50),
		latency INT
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Latency values: 10, 20, 30, 40, 50, 60, 70, 80, 90, 100
	for i := 1; i <= 10; i++ {
		_, err = server.ExecuteSQL(fmt.Sprintf("INSERT INTO api_logs (id, endpoint, latency) VALUES (%d, '/api/v1', %d);", i, i*10))
		if err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
	}

	// 1. APPROX_PERCENTILE and MEDIAN global aggregation
	q := `SELECT APPROX_PERCENTILE(latency, 0.90) AS p90, MEDIAN(latency) AS med FROM api_logs;`
	res, err := server.ExecuteSQL(q)
	if err != nil || !res.Success {
		t.Fatalf("Query failed: %v, res: %+v", err, res)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	med := fmt.Sprintf("%v", res.Rows[0]["med"])
	p90 := fmt.Sprintf("%v", res.Rows[0]["p90"])

	// 10 values: [10..100], median = (50+60)/2 = 55
	if med != "55" {
		t.Errorf("expected median=55, got %v", med)
	}
	// P90 = 91 or 90
	if p90 != "91" && p90 != "90" {
		t.Errorf("expected p90 approx 91 or 90, got %v", p90)
	}
}

func TestSQLExecutor_Percentile_WindowFunction(t *testing.T) {
	server, cleanup := setupEmployeesTestServer(t)
	defer cleanup()

	// Window function with OVER (PARTITION BY dept)
	q := `SELECT id, dept, salary, 
		PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY salary) OVER (PARTITION BY dept) AS dept_med,
		MEDIAN(salary) OVER (PARTITION BY dept) AS dept_med2
		FROM employees;`
	res, err := server.ExecuteSQL(q)
	if err != nil || !res.Success {
		t.Fatalf("Window query failed: %v, res: %+v", err, res)
	}
	if len(res.Rows) != 7 {
		t.Fatalf("expected 7 rows, got %d", len(res.Rows))
	}

	for _, row := range res.Rows {
		dept := fmt.Sprintf("%v", row["dept"])
		med := fmt.Sprintf("%v", row["dept_med"])
		med2 := fmt.Sprintf("%v", row["dept_med2"])

		if dept == "Eng" {
			if med != "25" || med2 != "25" {
				t.Errorf("Eng window median expected 25, got med=%v, med2=%v", med, med2)
			}
		} else if dept == "Sales" {
			if med != "100" || med2 != "100" {
				t.Errorf("Sales window median expected 100, got med=%v, med2=%v", med, med2)
			}
		}
	}
}
