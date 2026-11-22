package spec_defects

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire/tds"
)

// TestSpecDefect_TDS_RPC_MultiSet verifies:
// Defect ID: DEFECT-TDS-RPC-MULTIPLE-RESULT-SETS-1
// Root Cause: When a stored procedure or prepared statement returning multiple result sets
// is invoked via TDS RPC (PacketRPC 0x03), server.go's respondExecutionResult only serializes
// the first result set (res.Columns/res.Rows) and completely ignores res.ResultSets[1:],
// immediately terminating the stream with ReturnStatus and DoneProc.
// As a result, client drivers (such as go-mssqldb, ADO.NET, EF Core) fail to retrieve
// subsequent result sets when calling rows.NextResultSet().
func TestSpecDefect_TDS_RPC_MultiSet(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	procSQL := `
CREATE PROCEDURE sp_dual_rpc_dataset
    @init INT
AS
BEGIN
    SELECT @init AS set_id, 'alpha' AS set_name;
    SELECT @init + 100 AS set_id, 'beta' AS set_name;
END;
`
	if _, err := db.ExecContext(ctx, procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// Invoking via RPC: passing sql.Named parameters to a procedure name triggers TDS PacketRPC (0x03)
	rows, err := db.QueryContext(ctx, "sp_dual_rpc_dataset", sql.Named("init", 10))
	if err != nil {
		t.Fatalf("RPC QueryContext failed: %v", err)
	}
	defer rows.Close()

	// Result Set 1
	if !rows.Next() {
		t.Fatalf("expected row in proc result set 1")
	}
	var setID int
	var setName string
	if err := rows.Scan(&setID, &setName); err != nil {
		t.Fatalf("scan proc result set 1 failed: %v", err)
	}
	if setID != 10 || setName != "alpha" {
		t.Fatalf("result set 1: expected (10, 'alpha'), got (%d, %s)", setID, setName)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in proc result set 1")
	}

	// Result Set 2: NextResultSet() MUST return true
	if !rows.NextResultSet() {
		t.Fatalf("expected NextResultSet() == true for proc result set 2, but got false (result set was dropped)")
	}
	if !rows.Next() {
		t.Fatalf("expected row in proc result set 2")
	}
	if err := rows.Scan(&setID, &setName); err != nil {
		t.Fatalf("scan proc result set 2 failed: %v", err)
	}
	if setID != 110 || setName != "beta" {
		t.Fatalf("result set 2: expected (110, 'beta'), got (%d, %s)", setID, setName)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in proc result set 2")
	}

	// No more result sets
	if rows.NextResultSet() {
		t.Fatalf("expected NextResultSet() == false after result set 2")
	}

	// 2. Multi-result set RPC with 3 different result sets and output parameter
	procMultiSQL := `
CREATE PROCEDURE sp_tri_rpc_dataset
    @factor INT,
    @outTotal INT OUTPUT
AS
BEGIN
    SELECT 1 AS num, 'one' AS word;
    SELECT 10 AS v1, 20 AS v2, 30 AS v3;
    SELECT @factor * 100 AS calc;
    SET @outTotal = @factor * 999;
END;
`
	if _, err := db.ExecContext(ctx, procMultiSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_tri_rpc_dataset failed: %v", err)
	}

	var outTotal int64
	rows2, err := db.QueryContext(ctx, "sp_tri_rpc_dataset",
		sql.Named("factor", 5),
		sql.Named("outTotal", sql.Out{Dest: &outTotal}),
	)
	if err != nil {
		t.Fatalf("RPC QueryContext sp_tri_rpc_dataset failed: %v", err)
	}
	defer rows2.Close()

	// Set 1: num, word
	if !rows2.Next() {
		t.Fatalf("expected row in set 1")
	}
	var n int
	var w string
	if err := rows2.Scan(&n, &w); err != nil || n != 1 || w != "one" {
		t.Fatalf("set 1 mismatch: n=%d, w=%s, err=%v", n, w, err)
	}
	if rows2.Next() {
		t.Fatalf("unexpected extra row in set 1")
	}

	// Set 2: v1, v2, v3
	if !rows2.NextResultSet() {
		t.Fatalf("expected set 2, rows2.Err: %v", rows2.Err())
	}
	if !rows2.Next() {
		t.Fatalf("expected row in set 2")
	}
	var v1, v2, v3 int
	if err := rows2.Scan(&v1, &v2, &v3); err != nil || v1 != 10 || v2 != 20 || v3 != 30 {
		t.Fatalf("set 2 mismatch: v1=%d, v2=%d, v3=%d, err=%v", v1, v2, v3, err)
	}
	if rows2.Next() {
		t.Fatalf("unexpected extra row in set 2")
	}

	// Set 3: calc
	if !rows2.NextResultSet() {
		t.Fatalf("expected set 3")
	}
	if !rows2.Next() {
		t.Fatalf("expected row in set 3")
	}
	var calc int
	if err := rows2.Scan(&calc); err != nil || calc != 500 {
		t.Fatalf("set 3 mismatch: calc=%d, err=%v", calc, err)
	}
	if rows2.Next() {
		t.Fatalf("unexpected extra row in set 3")
	}

	if rows2.NextResultSet() {
		t.Fatalf("expected no more result sets after set 3")
	}
	_ = rows2.Close()

	if outTotal != 4995 {
		t.Fatalf("expected outTotal=4995, got %d", outTotal)
	}
}
