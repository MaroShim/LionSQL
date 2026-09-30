package api_test

import (
	"strings"
	"testing"
)

func TestRBAC_EndToEndLifecycleAndPermissionEnforcement(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// 1. Initial table setup under default administrator (dbo)
	stmts := []string{
		"CREATE TABLE payroll (id INT PRIMARY KEY, employee VARCHAR(50), salary INT)",
		"INSERT INTO payroll VALUES (1, 'Alice', 120000)",
		"INSERT INTO payroll VALUES (2, 'Bob', 95000)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup statement failed (%q): %v", stmt, err)
		}
	}

	// 2. CREATE LOGIN and ALTER LOGIN
	t.Run("create and alter login", func(t *testing.T) {
		res, err := server.ExecuteSQL("CREATE LOGIN analyst WITH PASSWORD = 'InitialPassword1!'")
		if err != nil || !res.Success {
			t.Fatalf("CREATE LOGIN failed: %v", err)
		}

		resAlter, err := server.ExecuteSQL("ALTER LOGIN analyst WITH PASSWORD = 'UpdatedPassword2@'")
		if err != nil || !resAlter.Success {
			t.Fatalf("ALTER LOGIN failed: %v", err)
		}
	})

	// 3. CREATE USER for LOGIN
	t.Run("create database user", func(t *testing.T) {
		res, err := server.ExecuteSQL("CREATE USER analyst_user FOR LOGIN analyst")
		if err != nil || !res.Success {
			t.Fatalf("CREATE USER failed: %v", err)
		}
	})

	// 4. Permission enforcement: unprivileged user should be denied access
	t.Run("unprivileged user denied SELECT and INSERT", func(t *testing.T) {
		// Switch to analyst_user
		if _, err := server.ExecuteSQL("SETUSER 'analyst_user'"); err != nil {
			t.Fatalf("SETUSER failed: %v", err)
		}

		// SELECT should be denied
		_, err := server.ExecuteSQL("SELECT * FROM payroll")
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected permission denied on SELECT, got: %v", err)
		}

		// INSERT should be denied
		_, err = server.ExecuteSQL("INSERT INTO payroll VALUES (3, 'Charlie', 80000)")
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected permission denied on INSERT, got: %v", err)
		}

		// Revert to admin
		if _, err := server.ExecuteSQL("REVERT"); err != nil {
			t.Fatalf("REVERT failed: %v", err)
		}
	})

	// 5. GRANT SELECT and verify read-only access
	t.Run("grant SELECT enables read but denies write", func(t *testing.T) {
		if _, err := server.ExecuteSQL("GRANT SELECT ON payroll TO analyst_user"); err != nil {
			t.Fatalf("GRANT SELECT failed: %v", err)
		}

		if _, err := server.ExecuteSQL("EXECUTE AS USER = 'analyst_user'"); err != nil {
			t.Fatalf("EXECUTE AS USER failed: %v", err)
		}

		// SELECT should now succeed
		selRes, err := server.ExecuteSQL("SELECT * FROM payroll")
		if err != nil {
			t.Fatalf("SELECT failed after GRANT: %v", err)
		}
		if len(selRes.Rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(selRes.Rows))
		}

		// UPDATE should still be denied
		_, err = server.ExecuteSQL("UPDATE payroll SET salary = 200000 WHERE id = 1")
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected permission denied on UPDATE, got: %v", err)
		}

		// Revert to admin
		if _, err := server.ExecuteSQL("SETUSER"); err != nil {
			t.Fatalf("SETUSER revert failed: %v", err)
		}
	})

	// 6. GRANT ALL with DENY DELETE (Deny precedence)
	t.Run("deny precedence over grant", func(t *testing.T) {
		if _, err := server.ExecuteSQL("GRANT ALL ON payroll TO analyst_user"); err != nil {
			t.Fatalf("GRANT ALL failed: %v", err)
		}
		if _, err := server.ExecuteSQL("DENY DELETE ON payroll TO analyst_user"); err != nil {
			t.Fatalf("DENY DELETE failed: %v", err)
		}

		if _, err := server.ExecuteSQL("SETUSER 'analyst_user'"); err != nil {
			t.Fatalf("SETUSER failed: %v", err)
		}

		// INSERT should succeed (allowed by GRANT ALL)
		insRes, err := server.ExecuteSQL("INSERT INTO payroll VALUES (3, 'Charlie', 80000)")
		if err != nil || !insRes.Success {
			t.Fatalf("INSERT failed after GRANT ALL: %v", err)
		}

		// DELETE should fail because DENY takes precedence
		_, err = server.ExecuteSQL("DELETE FROM payroll WHERE id = 3")
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected permission denied on DELETE due to DENY, got: %v", err)
		}

		// Revert to admin
		if _, err := server.ExecuteSQL("REVERT"); err != nil {
			t.Fatalf("REVERT failed: %v", err)
		}
	})

	// 7. REVOKE and cleanup
	t.Run("revoke permissions and drop principals", func(t *testing.T) {
		if _, err := server.ExecuteSQL("REVOKE SELECT ON payroll FROM analyst_user"); err != nil {
			t.Fatalf("REVOKE failed: %v", err)
		}

		if _, err := server.ExecuteSQL("DROP USER IF EXISTS analyst_user"); err != nil {
			t.Fatalf("DROP USER failed: %v", err)
		}

		if _, err := server.ExecuteSQL("DROP LOGIN IF EXISTS analyst"); err != nil {
			t.Fatalf("DROP LOGIN failed: %v", err)
		}
	})
}
