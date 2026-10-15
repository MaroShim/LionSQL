package tds_test

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/wire/tds"
)

func TestExternalDriver_Python_PyMSSQL(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("skipping test: uv executable not found in PATH")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port

	pyScript := fmt.Sprintf(`
import sys
import pymssql

print("Connecting with pymssql to port %d...")
conn = pymssql.connect(
    server='127.0.0.1',
    port=%d,
    user='sa',
    password='secret',
    database='master',
    autocommit=True
)

cursor = conn.cursor()

# 1. Simple query
cursor.execute("SELECT 100 + 42, 'pymssql_connected'")
row = cursor.fetchone()
assert row[0] == 142, f"expected 142, got {row[0]}"
assert row[1] == "pymssql_connected", f"expected 'pymssql_connected', got {row[1]}"
print("Simple query passed:", row)

# 2. Schema DDL
cursor.execute("CREATE TABLE py_tds_items (id INT PRIMARY KEY, name VARCHAR(50), price FLOAT, dt DATE, t TIME, uid UNIQUEIDENTIFIER, notes VARCHAR(100));")

# 3. DML INSERT
cursor.execute("INSERT INTO py_tds_items VALUES (1, 'Server', 1299.99, '2026-10-15', '14:30:00', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'primary');")
cursor.execute("INSERT INTO py_tds_items VALUES (2, 'Laptop', 899.50, '2026-11-20', '09:15:30', 'b1ffcd00-ad1c-4f09-cc7e-7cc0ce491b22', NULL);")

# 4. SELECT & Type verification
cursor.execute("SELECT id, name, price, dt, t, uid, notes FROM py_tds_items ORDER BY id;")
rows = cursor.fetchall()
assert len(rows) == 2, f"expected 2 rows, got {len(rows)}"
assert rows[0][0] == 1 and rows[0][1] == 'Server'
assert rows[1][0] == 2 and rows[1][6] is None, f"expected NULL notes, got {rows[1][6]}"
print("CRUD with DATE, TIME, GUID, NULL passed:", rows)

# 5. Transactions (Rollback & Commit)
conn.autocommit(False)
cursor.execute("INSERT INTO py_tds_items VALUES (3, 'RollbackItem', 10.0, '2026-01-01', '00:00:00', 'c200de11-be2d-401a-dd8f-8dd1df502c33', 'rollback me');")
conn.rollback()

cursor.execute("SELECT COUNT(*) FROM py_tds_items WHERE id = 3;")
cnt = cursor.fetchone()[0]
assert cnt == 0, f"expected 0 rows after rollback, got {cnt}"
print("Transaction ROLLBACK verified!")

cursor.execute("INSERT INTO py_tds_items VALUES (4, 'CommitItem', 99.0, '2026-05-05', '12:00:00', 'd311ef22-cf3e-412b-ee90-9ee2ea613d44', 'committed');")
conn.commit()

cursor.execute("SELECT COUNT(*) FROM py_tds_items WHERE id = 4;")
cnt = cursor.fetchone()[0]
assert cnt == 1, f"expected 1 row after commit, got {cnt}"
print("Transaction COMMIT verified!")

# 6. Parameterized query
conn.autocommit(True)
cursor.execute("SELECT id, name, price FROM py_tds_items WHERE id = %%d;", (1,))
p_row = cursor.fetchone()
assert p_row is not None and p_row[0] == 1 and p_row[1] == 'Server', f"unexpected param row: {p_row}"
print("Parameterized query passed:", p_row)

# 7. Stored Procedure
cursor.execute("CREATE PROCEDURE py_proc_update @target_id INT, @new_price FLOAT AS BEGIN UPDATE py_tds_items SET price = @new_price WHERE id = @target_id; END;")
cursor.execute("EXEC py_proc_update 1, 1499.00;")
cursor.execute("SELECT price FROM py_tds_items WHERE id = 1;")
proc_row = cursor.fetchone()
assert proc_row is not None and abs(proc_row[0] - 1499.00) < 0.01, f"unexpected proc row: {proc_row}"
print("Stored procedure execution passed:", proc_row)

conn.close()
print("ALL PYMSSQL TESTS PASSED!")
`, port, port)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "uv", "run", "--with", "pymssql", "python3", "-c", pyScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pymssql test failed: %v\nOutput:\n%s", err, string(out))
	}
	t.Logf("pymssql output:\n%s", string(out))
}
