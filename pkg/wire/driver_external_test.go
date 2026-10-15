package wire_test

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/wire"
)

func TestExternalDriver_Python_Psycopg(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("skipping test: uv executable not found in PATH")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)

	srv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start PG wire server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port

	pyScript := fmt.Sprintf(`
import sys
import psycopg

conn_str = "host=127.0.0.1 port=%d user=postgres password=secret dbname=master sslmode=disable"
print("Connecting with psycopg to port %d...")

with psycopg.connect(conn_str, autocommit=True) as conn:
    with conn.cursor() as cur:
        # 1. Simple query
        cur.execute("SELECT 1 + 1, 'psycopg_connected'")
        row = cur.fetchone()
        assert row[0] == 2, f"expected 2, got {row[0]}"
        assert row[1] == "psycopg_connected", f"expected 'psycopg_connected', got {row[1]}"
        print("Simple query passed:", row)

        # 2. Schema DDL
        cur.execute("CREATE TABLE py_items (id INT PRIMARY KEY, name VARCHAR(50), price FLOAT, active BOOL);")

        # 3. DML INSERT with values and NULL
        cur.execute("INSERT INTO py_items VALUES (1, 'Widget', 19.99, TRUE);")
        cur.execute("INSERT INTO py_items VALUES (2, 'Gadget', 49.50, FALSE);")
        cur.execute("INSERT INTO py_items VALUES (3, NULL, NULL, NULL);")

        # 4. SELECT and verification
        cur.execute("SELECT id, name, price, active FROM py_items ORDER BY id;")
        rows = cur.fetchall()
        assert len(rows) == 3, f"expected 3 rows, got {len(rows)}"
        assert rows[0][0] == 1 and rows[0][1] == 'Widget', f"unexpected row 0: {rows[0]}"
        assert rows[1][0] == 2 and rows[1][1] == 'Gadget', f"unexpected row 1: {rows[1]}"
        assert rows[2][0] == 3 and rows[2][1] is None and rows[2][2] is None, f"unexpected row 2: {rows[2]}"
        print("CRUD test passed:", rows)

        # 4-1. Parameterized SELECT
        cur.execute("SELECT id, name, price FROM py_items WHERE id = %%s;", (2,))
        p_row = cur.fetchone()
        assert p_row is not None and p_row[0] == 2 and p_row[1] == 'Gadget', f"unexpected param row: {p_row}"
        print("Parameterized SELECT passed:", p_row)

        # 4-2. Parameterized INSERT
        cur.execute("INSERT INTO py_items VALUES (%%s, %%s, %%s, %%s);", (4, 'ParamWidget', 79.99, True))
        cur.execute("SELECT name, price FROM py_items WHERE id = %%s;", (4,))
        p_row4 = cur.fetchone()
        assert p_row4 is not None and p_row4[0] == 'ParamWidget', f"unexpected row 4: {p_row4}"
        print("Parameterized INSERT passed:", p_row4)

# 5. Transactions (Rollback & Commit)
with psycopg.connect(conn_str, autocommit=False) as conn:
    with conn.cursor() as cur:
        cur.execute("INSERT INTO py_items VALUES (10, 'RollbackItem', 5.0, TRUE);")
        conn.rollback()

        cur.execute("SELECT COUNT(*) FROM py_items WHERE id = 10;")
        cnt = cur.fetchone()[0]
        assert cnt == 0, f"expected 0 rows after rollback, got {cnt}"
        print("Transaction ROLLBACK verified!")

        cur.execute("INSERT INTO py_items VALUES (20, 'CommitItem', 77.0, FALSE);")
        conn.commit()

        cur.execute("SELECT COUNT(*) FROM py_items WHERE id = 20;")
        cnt = cur.fetchone()[0]
        assert cnt == 1, f"expected 1 row after commit, got {cnt}"
        print("Transaction COMMIT verified!")

print("ALL PSYCOPG TESTS PASSED!")
`, port, port)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "uv", "run", "--with", "psycopg[binary]", "python3", "-c", pyScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("psycopg test failed: %v\nOutput:\n%s", err, string(out))
	}
	t.Logf("psycopg output:\n%s", string(out))
}
