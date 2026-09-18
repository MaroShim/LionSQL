package parser_test

import (
	"testing"

	parser "sqlion/pkg/proc/parser/antlr"
)

func TestParseTSql_ValidQueries(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{
			name: "Simple SELECT",
			sql:  "SELECT id, name, balance FROM users WHERE balance > 1000 ORDER BY id DESC;",
		},
		{
			name: "CTE with Window Function",
			sql: `
WITH RankedSalaries AS (
    SELECT emp_id, department_id, salary,
           ROW_NUMBER() OVER(PARTITION BY department_id ORDER BY salary DESC) as rank_num
    FROM employees
)
SELECT emp_id, department_id, salary
FROM RankedSalaries
WHERE rank_num <= 3;
`,
		},
		{
			name: "Complex Joins and Aggregations",
			sql: `
SELECT 
    c.customer_name,
    COUNT(o.order_id) AS total_orders,
    SUM(oi.quantity * oi.unit_price) AS total_spent,
    AVG(oi.unit_price) AS avg_item_price
FROM customers c
INNER JOIN orders o ON c.customer_id = o.customer_id
LEFT JOIN order_items oi ON o.order_id = oi.order_id
WHERE o.order_date >= '2026-01-01'
GROUP BY c.customer_name
HAVING COUNT(o.order_id) > 5
ORDER BY total_spent DESC;
`,
		},
		{
			name: "Stored Procedure with TRY CATCH and Transactions",
			sql: `
CREATE PROCEDURE TransferFunds
    @from_acc INT,
    @to_acc INT,
    @amount DECIMAL(18,2)
AS
BEGIN
    SET NOCOUNT ON;
    BEGIN TRY
        BEGIN TRANSACTION;

        IF (SELECT balance FROM accounts WHERE id = @from_acc) < @amount
        BEGIN
            THROW 50001, 'Insufficient funds', 1;
        END

        UPDATE accounts SET balance = balance - @amount WHERE id = @from_acc;
        UPDATE accounts SET balance = balance + @amount WHERE id = @to_acc;

        COMMIT TRANSACTION;
    END TRY
    BEGIN CATCH
        IF @@TRANCOUNT > 0
            ROLLBACK TRANSACTION;
        THROW;
    END CATCH
END;
`,
		},
		{
			name: "CASE WHEN and Control Flow",
			sql: `
SELECT 
    id, 
    score,
    CASE 
        WHEN score >= 90 THEN 'A'
        WHEN score >= 80 THEN 'B'
        WHEN score >= 70 THEN 'C'
        ELSE 'F'
    END AS grade
FROM student_scores;
`,
		},
		{
			name: "DDL and Constraints",
			sql: `
CREATE TABLE orders (
    order_id INT IDENTITY(1,1) PRIMARY KEY,
    customer_id INT NOT NULL,
    order_date DATETIME DEFAULT GETDATE(),
    status VARCHAR(20) CHECK (status IN ('PENDING', 'COMPLETED', 'CANCELLED')),
    FOREIGN KEY (customer_id) REFERENCES customers(customer_id)
);
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parser.ParseTSql(tt.sql)
			if err != nil {
				t.Fatalf("unexpected error parsing %s: %v", tt.name, err)
			}
			if result == nil || result.Tree == nil {
				t.Fatalf("expected non-nil tree for %s", tt.name)
			}
		})
	}
}

func TestParseTSql_SyntaxError(t *testing.T) {
	invalidSql := "SELECT FROM WHERE;"
	result, err := parser.ParseTSql(invalidSql)
	if err == nil {
		t.Fatalf("expected syntax error for invalid SQL, but got nil")
	}
	if len(result.Errors) == 0 {
		t.Fatalf("expected error listener to record syntax errors")
	}
}
