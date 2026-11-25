package planner_test

import (
	"strings"
	"testing"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

func setupDMLContractCatalog() map[string]*storage.TableSchema {
	return map[string]*storage.TableSchema{
		"users": {
			Name: "users",
			Columns: []storage.Column{
				{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				{Name: "name", Type: storage.TypeVarChar, Nullable: false},
				{Name: "age", Type: storage.TypeInt, Nullable: true},
				{Name: "status", Type: storage.TypeVarChar, Default: "'active'"},
				{Name: "created_at", Type: storage.TypeDateTime, Default: "NOW()"},
				{Name: "full_desc", Type: storage.TypeVarChar, ComputedExpr: "name + ' (' + CAST(age AS VARCHAR) + ')'"},
				{Name: "ver", Type: storage.TypeInt, IsRowVersion: true},
			},
		},
		"orders": {
			Name: "orders",
			Columns: []storage.Column{
				{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true, Identity: true},
				{Name: "user_id", Type: storage.TypeInt, Nullable: false},
				{Name: "amount", Type: storage.TypeDecimal, Nullable: false},
			},
		},
	}
}

func parseAndValidateDML(t *testing.T, sqlStr string, schemas map[string]*storage.TableSchema) error {
	t.Helper()
	parsed, err := proc.ParseSQL(sqlStr)
	if err != nil {
		return err
	}
	return planner.ValidateDMLStmt(parsed, schemas)
}

// -----------------------------------------------------------------------------
// 1. DML Target Table & System Catalog Protection Contracts
// -----------------------------------------------------------------------------
func TestContract_DML_TargetTableProtection(t *testing.T) {
	schemas := setupDMLContractCatalog()

	cases := []struct {
		name        string
		sql         string
		expectedErr string
	}{
		{
			name:        "INSERT into non-existent table rejected",
			sql:         "INSERT INTO ghost_table (id) VALUES (1)",
			expectedErr: "does not exist",
		},
		{
			name:        "UPDATE non-existent table rejected",
			sql:         "UPDATE ghost_table SET id = 1 WHERE 1=1",
			expectedErr: "does not exist",
		},
		{
			name:        "DELETE non-existent table rejected",
			sql:         "DELETE FROM ghost_table WHERE 1=1",
			expectedErr: "does not exist",
		},
		{
			name:        "INSERT into sys.tables system view blocked",
			sql:         "INSERT INTO sys.tables (name) VALUES ('hack')",
			expectedErr: "cannot modify system catalog view",
		},
		{
			name:        "UPDATE sys.columns system view blocked",
			sql:         "UPDATE sys.columns SET name = 'hack' WHERE 1=1",
			expectedErr: "cannot modify system catalog view",
		},
		{
			name:        "DELETE from information_schema.tables blocked",
			sql:         "DELETE FROM information_schema.tables WHERE 1=1",
			expectedErr: "cannot modify system catalog view",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseAndValidateDML(t, tc.sql, schemas)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.expectedErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("expected error containing %q, got: %v", tc.expectedErr, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 2. INSERT Arity & Column Contracts
// -----------------------------------------------------------------------------
func TestContract_DML_InsertArityAndColumns(t *testing.T) {
	schemas := setupDMLContractCatalog()

	cases := []struct {
		name        string
		sql         string
		expectedErr string
	}{
		{
			name:        "INSERT columns count < values count",
			sql:         "INSERT INTO users (id, name) VALUES (1, 'Alice', 30)",
			expectedErr: "has more expressions than target columns",
		},
		{
			name:        "INSERT columns count > values count",
			sql:         "INSERT INTO users (id, name, age) VALUES (1, 'Alice')",
			expectedErr: "has more target columns than expressions",
		},
		{
			name:        "INSERT multi-row tuple arity mismatch in second row",
			sql:         "INSERT INTO users (id, name) VALUES (1, 'Alice'), (2, 'Bob', 25)",
			expectedErr: "has more expressions than target columns",
		},
		{
			name:        "INSERT non-existent column in target list",
			sql:         "INSERT INTO users (id, ghost_col) VALUES (1, 10)",
			expectedErr: "does not exist",
		},
		{
			name:        "INSERT duplicate column in target list",
			sql:         "INSERT INTO users (id, name, id) VALUES (1, 'Alice', 2)",
			expectedErr: "specified more than once",
		},
		{
			name:        "INSERT missing NOT NULL column without default",
			sql:         "INSERT INTO users (age) VALUES (30)",
			expectedErr: "violates not-null constraint",
		},
		{
			name:        "INSERT directly into computed column rejected",
			sql:         "INSERT INTO users (id, name, full_desc) VALUES (1, 'Alice', 'manual')",
			expectedErr: "cannot insert into computed column",
		},
		{
			name:        "INSERT ... SELECT column count mismatch",
			sql:         "INSERT INTO orders (user_id, amount) SELECT 1, 100.0, 'extra'",
			expectedErr: "column count mismatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseAndValidateDML(t, tc.sql, schemas)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.expectedErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("expected error containing %q, got: %v", tc.expectedErr, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 3. UPDATE SET Clause Contracts
// -----------------------------------------------------------------------------
func TestContract_DML_UpdateSetClause(t *testing.T) {
	schemas := setupDMLContractCatalog()

	cases := []struct {
		name        string
		sql         string
		expectedErr string
	}{
		{
			name:        "UPDATE non-existent column in SET rejected",
			sql:         "UPDATE users SET ghost_col = 99 WHERE id = 1",
			expectedErr: "does not exist",
		},
		{
			name:        "UPDATE computed column directly in SET rejected",
			sql:         "UPDATE users SET full_desc = 'hacked' WHERE id = 1",
			expectedErr: "cannot update computed column",
		},
		{
			name:        "UPDATE rowversion column directly in SET rejected",
			sql:         "UPDATE users SET ver = 123 WHERE id = 1",
			expectedErr: "cannot update timestamp/rowversion column",
		},
		{
			name:        "UPDATE aggregate function in SET expression rejected",
			sql:         "UPDATE users SET age = COUNT(*) WHERE id = 1",
			expectedErr: "aggregate functions are not allowed in UPDATE SET",
		},
		{
			name:        "UPDATE window function in SET expression rejected",
			sql:         "UPDATE users SET age = ROW_NUMBER() OVER () WHERE id = 1",
			expectedErr: "window functions are not allowed in UPDATE SET",
		},
		{
			name:        "UPDATE duplicate column in SET rejected",
			sql:         "UPDATE users SET age = 20, age = 30 WHERE id = 1",
			expectedErr: "specified more than once",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseAndValidateDML(t, tc.sql, schemas)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.expectedErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("expected error containing %q, got: %v", tc.expectedErr, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 4. DML WHERE Clause Contracts
// -----------------------------------------------------------------------------
func TestContract_DML_WhereClause(t *testing.T) {
	schemas := setupDMLContractCatalog()

	cases := []struct {
		name        string
		sql         string
		expectedErr string
	}{
		{
			name:        "UPDATE WHERE with aggregate function rejected",
			sql:         "UPDATE users SET age = 30 WHERE COUNT(*) > 0",
			expectedErr: "aggregate functions are not allowed in WHERE",
		},
		{
			name:        "UPDATE WHERE with window function rejected",
			sql:         "UPDATE users SET age = 30 WHERE ROW_NUMBER() OVER () = 1",
			expectedErr: "window functions are not allowed in WHERE",
		},
		{
			name:        "UPDATE WHERE non-existent column rejected",
			sql:         "UPDATE users SET age = 30 WHERE ghost_filter = 1",
			expectedErr: "does not exist",
		},
		{
			name:        "UPDATE WHERE multi-column subquery rejected",
			sql:         "UPDATE users SET age = 30 WHERE id IN (SELECT id, name FROM users)",
			expectedErr: "subquery must return only one column",
		},
		{
			name:        "DELETE WHERE with aggregate function rejected",
			sql:         "DELETE FROM users WHERE SUM(age) > 100",
			expectedErr: "aggregate functions are not allowed in WHERE",
		},
		{
			name:        "DELETE WHERE with window function rejected",
			sql:         "DELETE FROM users WHERE ROW_NUMBER() OVER () = 1",
			expectedErr: "window functions are not allowed in WHERE",
		},
		{
			name:        "DELETE WHERE non-existent column rejected",
			sql:         "DELETE FROM users WHERE ghost_col = 5",
			expectedErr: "does not exist",
		},
		{
			name:        "DELETE WHERE multi-column subquery rejected",
			sql:         "DELETE FROM users WHERE id IN (SELECT id, name FROM users)",
			expectedErr: "subquery must return only one column",
		},
		{
			name:        "UPDATE WHERE ANY multi-column subquery rejected",
			sql:         "UPDATE users SET age = 30 WHERE age = ANY (SELECT age, id FROM users)",
			expectedErr: "subquery must return only one column",
		},
		{
			name:        "DELETE WHERE ALL multi-column subquery rejected",
			sql:         "DELETE FROM users WHERE age > ALL (SELECT age, id FROM users)",
			expectedErr: "subquery must return only one column",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseAndValidateDML(t, tc.sql, schemas)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.expectedErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("expected error containing %q, got: %v", tc.expectedErr, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 5. Positive Tests: Valid DML Statements Must Pass Contract
// -----------------------------------------------------------------------------
func TestContract_DML_PositiveValidStatements(t *testing.T) {
	schemas := setupDMLContractCatalog()

	validSQLs := []string{
		"INSERT INTO users (id, name, age) VALUES (1, 'Alice', 30)",
		"INSERT INTO users (id, name) VALUES (2, 'Bob')",
		"INSERT INTO users (id, name) VALUES (3, 'Charlie'), (4, 'David')",
		"INSERT INTO orders (user_id, amount) VALUES (1, 99.50)",
		"UPDATE users SET age = 31, name = 'Alice Cooper' WHERE id = 1",
		"UPDATE users SET age = age + 1 WHERE status = 'active'",
		"UPDATE orders SET amount = amount * 1.1 WHERE user_id = 1",
		"DELETE FROM users WHERE id = 2",
		"DELETE FROM orders WHERE user_id = 1 AND amount < 10.0",
		"DELETE FROM users WHERE id IN (SELECT user_id FROM orders WHERE amount > 500.0)",
		"UPDATE users SET age = 30 WHERE age > ALL (SELECT age FROM users WHERE status = 'active')",
		"DELETE FROM users WHERE age = ANY (SELECT age FROM users WHERE status = 'inactive')",
	}

	for _, sqlStr := range validSQLs {
		t.Run(sqlStr, func(t *testing.T) {
			if err := parseAndValidateDML(t, sqlStr, schemas); err != nil {
				t.Fatalf("valid DML statement failed contract validation: %v", err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 6. VALUES Scope Validation Contract Tests
// -----------------------------------------------------------------------------
func TestContract_DML_ValuesScopeValidation(t *testing.T) {
	schemas := setupDMLContractCatalog()

	// 1. Aggregate in VALUES tuple -> rejected
	err := parseAndValidateDML(t, "INSERT INTO users (id, name, age) VALUES (1, 'Alice', COUNT(*))", schemas)
	if err == nil || !strings.Contains(err.Error(), "aggregate functions are not allowed in VALUES") {
		t.Errorf("expected aggregate in VALUES to be rejected, got: %v", err)
	}

	// 2. Window function in VALUES tuple -> rejected
	err = parseAndValidateDML(t, "INSERT INTO users (id, name, age) VALUES (1, 'Alice', ROW_NUMBER() OVER ())", schemas)
	if err == nil || !strings.Contains(err.Error(), "window functions are not allowed in VALUES") {
		t.Errorf("expected window function in VALUES to be rejected, got: %v", err)
	}

	// 3. Normal scalar functions in VALUES tuple -> permitted (PASS)
	if err := parseAndValidateDML(t, "INSERT INTO users (id, name, age) VALUES (1, UPPER('Alice'), ABS(-30))", schemas); err != nil {
		t.Errorf("expected scalar functions in VALUES to pass, got: %v", err)
	}
}

