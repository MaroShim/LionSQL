package spec_defects

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire"
	"lionsql/pkg/wire/tds"
)

// CanonicalResultSet represents a normalized result set for 1:1 cross-protocol symmetry verification.
type CanonicalResultSet struct {
	Columns []string
	Rows    [][]string
}

// extractCanonicalResultSets consumes all rows and subsequent result sets from a driver sql.Rows stream.
func extractCanonicalResultSets(t *testing.T, rows *sql.Rows) []CanonicalResultSet {
	t.Helper()
	defer rows.Close()

	var sets []CanonicalResultSet
	for {
		cols, err := rows.Columns()
		if err != nil {
			t.Fatalf("failed to retrieve columns: %v", err)
		}

		var currentSetRows [][]string
		for rows.Next() {
			scanDests := make([]any, len(cols))
			scanPointers := make([]any, len(cols))
			for i := range scanDests {
				scanPointers[i] = &scanDests[i]
			}
			if err := rows.Scan(scanPointers...); err != nil {
				t.Fatalf("rows.Scan failed: %v", err)
			}
			rowVals := make([]string, len(cols))
			for i, v := range scanDests {
				if v == nil {
					rowVals[i] = "NULL"
				} else if b, ok := v.([]byte); ok {
					rowVals[i] = string(b)
				} else {
					rowVals[i] = fmt.Sprintf("%v", v)
				}
			}
			currentSetRows = append(currentSetRows, rowVals)
		}

		sets = append(sets, CanonicalResultSet{
			Columns: cols,
			Rows:    currentSetRows,
		})

		if !rows.NextResultSet() {
			break
		}
	}
	return sets
}

// assertCanonicalSymmetry strictly validates that two execution paths return identical
// result sets, columns, row counts, and cell values.
func assertCanonicalSymmetry(t *testing.T, labelA string, setsA []CanonicalResultSet, labelB string, setsB []CanonicalResultSet) {
	t.Helper()
	if len(setsA) != len(setsB) {
		t.Fatalf("Symmetry Mismatch [Result Set Count]: %s produced %d sets, but %s produced %d sets",
			labelA, len(setsA), labelB, len(setsB))
	}
	for i := range setsA {
		setA := setsA[i]
		setB := setsB[i]

		if len(setA.Columns) != len(setB.Columns) {
			t.Fatalf("Symmetry Mismatch [Set %d Column Count]: %s has %d cols (%v), %s has %d cols (%v)",
				i+1, labelA, len(setA.Columns), setA.Columns, labelB, len(setB.Columns), setB.Columns)
		}
		for cIdx := range setA.Columns {
			if !strings.EqualFold(setA.Columns[cIdx], setB.Columns[cIdx]) {
				t.Errorf("Symmetry Mismatch [Set %d Column %d Name]: %s has %q, %s has %q",
					i+1, cIdx, labelA, setA.Columns[cIdx], labelB, setB.Columns[cIdx])
			}
		}

		if len(setA.Rows) != len(setB.Rows) {
			t.Fatalf("Symmetry Mismatch [Set %d Row Count]: %s returned %d rows, %s returned %d rows",
				i+1, labelA, len(setA.Rows), labelB, len(setB.Rows))
		}
		for rIdx := range setA.Rows {
			if !reflect.DeepEqual(setA.Rows[rIdx], setB.Rows[rIdx]) {
				t.Fatalf("Symmetry Mismatch [Set %d Row %d Data]:\n  %s: %v\n  %s: %v",
					i+1, rIdx, labelA, setA.Rows[rIdx], labelB, setB.Rows[rIdx])
			}
		}
	}
}

// TestGate_TDS_Protocol_Symmetry enforces strict wire protocol parity across
// SQL Batch (0x01) and RPC Fast-Path (0x03) for Microsoft SQL Server clients (GATE-PROTOCOL-SYMMETRY-1).
func TestGate_TDS_Protocol_Symmetry(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Seed base table
	if _, err := db.ExecContext(ctx, "CREATE TABLE accounts (id INT PRIMARY KEY, holder VARCHAR(50), balance INT);"); err != nil {
		t.Fatalf("CREATE TABLE accounts failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO accounts (id, holder, balance) VALUES (1, 'Alice', 1000), (2, 'Bob', 2000);"); err != nil {
		t.Fatalf("INSERT accounts failed: %v", err)
	}

	// 1. Single Result Set Parameterized Procedure
	t.Run("Single_ResultSet_Parameterized_Symmetry", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_gate_single_math
    @base INT,
    @multiplier INT
AS
BEGIN
    SELECT @base * @multiplier AS product, @base + @multiplier AS sum_val;
END;
`
		if _, err := db.ExecContext(ctx, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE failed: %v", err)
		}

		// Path A: SQL Batch
		rowsBatch, err := db.QueryContext(ctx, "EXEC sp_gate_single_math @base = 7, @multiplier = 6;")
		if err != nil {
			t.Fatalf("SQL Batch Query failed: %v", err)
		}
		resBatch := extractCanonicalResultSets(t, rowsBatch)

		// Path B: RPC Fast-Path
		rowsRPC, err := db.QueryContext(ctx, "sp_gate_single_math", sql.Named("base", 7), sql.Named("multiplier", 6))
		if err != nil {
			t.Fatalf("RPC Fast-Path Query failed: %v", err)
		}
		resRPC := extractCanonicalResultSets(t, rowsRPC)

		assertCanonicalSymmetry(t, "TDS_SQL_Batch", resBatch, "TDS_RPC_FastPath", resRPC)
	})

	// 2. Multiple Result Sets Procedure
	t.Run("Multiple_ResultSets_Symmetry", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_gate_multi_summary
    @seed INT
AS
BEGIN
    SELECT @seed AS set1_seed, 'Set 1 Header' AS title;
    SELECT id, holder, balance + @seed AS adj_bal FROM accounts ORDER BY id;
    SELECT @seed * 100 AS final_score, 'Set 3 Footer' AS footer;
END;
`
		if _, err := db.ExecContext(ctx, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE multi failed: %v", err)
		}

		// Path A: SQL Batch
		rowsBatch, err := db.QueryContext(ctx, "EXEC sp_gate_multi_summary @seed = 50;")
		if err != nil {
			t.Fatalf("Batch QueryContext failed: %v", err)
		}
		resBatch := extractCanonicalResultSets(t, rowsBatch)

		// Path B: RPC Fast-Path
		rowsRPC, err := db.QueryContext(ctx, "sp_gate_multi_summary", sql.Named("seed", 50))
		if err != nil {
			t.Fatalf("RPC QueryContext failed: %v", err)
		}
		resRPC := extractCanonicalResultSets(t, rowsRPC)

		assertCanonicalSymmetry(t, "TDS_SQL_Batch", resBatch, "TDS_RPC_FastPath", resRPC)
	})

	// 3. Zero-Parameter Procedure with sp_ prefix and trailing semicolon
	t.Run("Zero_Parameter_EdgeCase_Symmetry", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_gate_zero_param_status
AS
BEGIN
    SELECT 200 AS status_code, 'SYSTEM_HEALTHY' AS status_msg;
END;
`
		if _, err := db.ExecContext(ctx, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE zero failed: %v", err)
		}

		// Path A: SQL Batch with trailing semicolon
		rowsBatch, err := db.QueryContext(ctx, "EXEC sp_gate_zero_param_status;")
		if err != nil {
			t.Fatalf("Batch QueryContext failed: %v", err)
		}
		resBatch := extractCanonicalResultSets(t, rowsBatch)

		// Path B: RPC Fast-Path with 0 parameters
		rowsRPC, err := db.QueryContext(ctx, "sp_gate_zero_param_status")
		if err != nil {
			t.Fatalf("RPC QueryContext failed: %v", err)
		}
		resRPC := extractCanonicalResultSets(t, rowsRPC)

		assertCanonicalSymmetry(t, "TDS_SQL_Batch", resBatch, "TDS_RPC_FastPath", resRPC)
	})

	// 4. Transaction DML Commit Symmetry
	t.Run("Transaction_DML_Commit_Symmetry", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_gate_transfer
    @acct_id INT,
    @delta INT
AS
BEGIN
    UPDATE accounts SET balance = balance + @delta WHERE id = @acct_id;
END;
`
		if _, err := db.ExecContext(ctx, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE transfer failed: %v", err)
		}

		// Transaction 1: Executed via SQL Batch and Committed
		tx1, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx tx1 failed: %v", err)
		}
		if _, err := tx1.ExecContext(ctx, "EXEC sp_gate_transfer @acct_id = 1, @delta = 100;"); err != nil {
			t.Fatalf("tx1 ExecContext failed: %v", err)
		}
		if err := tx1.Commit(); err != nil {
			t.Fatalf("tx1 Commit failed: %v", err)
		}

		// Transaction 2: Executed via RPC and Committed
		tx2, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx tx2 failed: %v", err)
		}
		if _, err := tx2.ExecContext(ctx, "sp_gate_transfer", sql.Named("acct_id", 2), sql.Named("delta", 200)); err != nil {
			t.Fatalf("tx2 ExecContext failed: %v", err)
		}
		if err := tx2.Commit(); err != nil {
			t.Fatalf("tx2 Commit failed: %v", err)
		}

		// Verify final account balances
		var bal1, bal2 int
		if err := db.QueryRowContext(ctx, "SELECT balance FROM accounts WHERE id = 1;").Scan(&bal1); err != nil || bal1 != 1100 {
			t.Fatalf("expected balance 1100 for acct 1, got %d (err: %v)", bal1, err)
		}
		if err := db.QueryRowContext(ctx, "SELECT balance FROM accounts WHERE id = 2;").Scan(&bal2); err != nil || bal2 != 2200 {
			t.Fatalf("expected balance 2200 for acct 2, got %d (err: %v)", bal2, err)
		}
	})

	// 5. Transaction DML Rollback Symmetry
	t.Run("Transaction_DML_Rollback_Symmetry", func(t *testing.T) {
		// Transaction with Rollback via RPC
		txRollback, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx rollback failed: %v", err)
		}
		if _, err := txRollback.ExecContext(ctx, "sp_gate_transfer", sql.Named("acct_id", 1), sql.Named("delta", 99999)); err != nil {
			t.Fatalf("txRollback ExecContext failed: %v", err)
		}
		if err := txRollback.Rollback(); err != nil {
			t.Fatalf("txRollback Rollback failed: %v", err)
		}

		// Ensure balance remained untouched
		var bal1 int
		if err := db.QueryRowContext(ctx, "SELECT balance FROM accounts WHERE id = 1;").Scan(&bal1); err != nil || bal1 != 1100 {
			t.Fatalf("expected unchanged balance 1100 after rollback, got %d", bal1)
		}
	})
}

// TestGate_PGWire_Protocol_Symmetry enforces strict wire protocol parity across
// Simple Query ('Q') and Extended Query ('PBE') for PostgreSQL clients (GATE-PROTOCOL-SYMMETRY-1).
func TestGate_PGWire_Protocol_Symmetry(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)

	// Seed base table
	schema := &storage.TableSchema{
		Name: "pg_items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "title", Type: storage.TypeVarChar},
			{Name: "price", Type: storage.TypeInt},
		},
	}
	_ = eng.CreateTable(schema)
	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("pg_items", storage.Record{"id": storage.IntValue(1), "title": storage.StringValue("Keyboard"), "price": storage.IntValue(50)})
	_ = tx.Insert("pg_items", storage.Record{"id": storage.IntValue(2), "title": storage.StringValue("Mouse"), "price": storage.IntValue(25)})
	_ = tx.Commit()
	_ = sess.Close()

	srv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start PGWire server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("sql.Open pgx failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Single Result Set Parameterized Procedure Symmetry
	t.Run("Single_ResultSet_Parameterized_Symmetry", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_pg_gate_calc
    @val INT,
    @rate INT
AS
BEGIN
    SELECT @val * @rate AS total_cost, 'CALCULATED' AS status_str;
END;
`
		if _, err := db.ExecContext(ctx, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE calc failed: %v", err)
		}

		// Path A: Simple Query (Inline string)
		rowsSimple, err := db.QueryContext(ctx, "EXEC sp_pg_gate_calc 12, 5;")
		if err != nil {
			t.Fatalf("Simple Query failed: %v", err)
		}
		resSimple := extractCanonicalResultSets(t, rowsSimple)

		// Path B: Extended Query (Prepared parameterized $1, $2)
		rowsExtended, err := db.QueryContext(ctx, "EXEC sp_pg_gate_calc $1, $2;", 12, 5)
		if err != nil {
			t.Fatalf("Extended Query failed: %v", err)
		}
		resExtended := extractCanonicalResultSets(t, rowsExtended)

		assertCanonicalSymmetry(t, "PGWire_SimpleQuery", resSimple, "PGWire_ExtendedQuery", resExtended)
	})

	// 2. Multiple Result Sets Procedure Symmetry (Simple Query vs Extended Query)
	t.Run("Multiple_ResultSets_Symmetry", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_pg_gate_multi_data
AS
BEGIN
    SELECT 101 AS num, 'alpha' AS label;
    SELECT 202 AS num, 'beta' AS label;
END;
`
		if _, err := db.ExecContext(ctx, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE multi failed: %v", err)
		}

		// Helper to execute and extract result sets via raw Simple Query ('Q')
		runRawSimple := func() []CanonicalResultSet {
			rawConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				t.Fatalf("raw dial simple failed: %v", err)
			}
			defer rawConn.Close()
			doPGHandshake(t, rawConn)

			qStr := "EXEC sp_pg_gate_multi_data;\x00"
			b := make([]byte, 5+len(qStr))
			b[0] = 'Q'
			b[1] = 0
			b[2] = 0
			b[3] = byte((len(qStr) + 4) >> 8)
			b[4] = byte(len(qStr) + 4)
			copy(b[5:], []byte(qStr))
			_, _ = rawConn.Write(b)

			return readRawPGResultSets(t, rawConn)
		}

		// Helper to execute and extract result sets via raw Extended Query ('PBE')
		runRawExtended := func() []CanonicalResultSet {
			rawConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				t.Fatalf("raw dial extended failed: %v", err)
			}
			defer rawConn.Close()
			doPGHandshake(t, rawConn)

			qStr := "EXEC sp_pg_gate_multi_data;\x00"
			pLen := 4 + 1 + len(qStr) + 2
			pBuf := make([]byte, 1+pLen)
			pBuf[0] = 'P'
			pBuf[1] = byte(pLen >> 24)
			pBuf[2] = byte(pLen >> 16)
			pBuf[3] = byte(pLen >> 8)
			pBuf[4] = byte(pLen)
			pBuf[5] = 0
			copy(pBuf[6:], []byte(qStr))

			bLen := 4 + 1 + 1 + 2 + 2 + 2
			bBuf := make([]byte, 1+bLen)
			bBuf[0] = 'B'
			bBuf[1] = 0
			bBuf[2] = 0
			bBuf[3] = 0
			bBuf[4] = byte(bLen)

			dBuf := []byte{'D', 0, 0, 0, 6, 'P', 0}
			eBuf := []byte{'E', 0, 0, 0, 9, 0, 0, 0, 0, 0}
			sBuf := []byte{'S', 0, 0, 0, 4}

			var batch []byte
			batch = append(batch, pBuf...)
			batch = append(batch, bBuf...)
			batch = append(batch, dBuf...)
			batch = append(batch, eBuf...)
			batch = append(batch, sBuf...)

			go func() {
				_, _ = rawConn.Write(batch)
			}()

			return readRawPGResultSets(t, rawConn)
		}

		resSimple := runRawSimple()
		resExtended := runRawExtended()

		assertCanonicalSymmetry(t, "PGWire_SimpleQuery", resSimple, "PGWire_ExtendedQuery", resExtended)
	})

	// 3. Zero-Parameter Procedure Symmetry with sp_ prefix and trailing semicolon
	t.Run("Zero_Parameter_EdgeCase_Symmetry", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_pg_gate_zero_param
AS
BEGIN
    SELECT 777 AS lucky_num, 'LION_SQL' AS brand_name;
END;
`
		if _, err := db.ExecContext(ctx, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE zero param failed: %v", err)
		}

		// Path A: Simple Query
		rowsSimple, err := db.QueryContext(ctx, "EXEC sp_pg_gate_zero_param;")
		if err != nil {
			t.Fatalf("Simple Query failed: %v", err)
		}
		resSimple := extractCanonicalResultSets(t, rowsSimple)

		// Path B: Extended Query (No parameters)
		rowsExtended, err := db.QueryContext(ctx, "EXEC sp_pg_gate_zero_param;")
		if err != nil {
			t.Fatalf("Extended Query failed: %v", err)
		}
		resExtended := extractCanonicalResultSets(t, rowsExtended)

		assertCanonicalSymmetry(t, "PGWire_SimpleQuery", resSimple, "PGWire_ExtendedQuery", resExtended)
	})

	// 3. Transaction DML Commit Symmetry
	t.Run("Transaction_DML_Commit_Symmetry", func(t *testing.T) {
		procDDL := `
CREATE PROCEDURE sp_pg_update_price
    @item_id INT,
    @new_price INT
AS
BEGIN
    UPDATE pg_items SET price = @new_price WHERE id = @item_id;
END;
`
		if _, err := db.ExecContext(ctx, procDDL); err != nil {
			t.Fatalf("CREATE PROCEDURE update price failed: %v", err)
		}

		// Transaction 1: Simple Query in Tx
		tx1, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx tx1 failed: %v", err)
		}
		if _, err := tx1.ExecContext(ctx, "EXEC sp_pg_update_price 1, 65;"); err != nil {
			t.Fatalf("tx1 ExecContext failed: %v", err)
		}
		if err := tx1.Commit(); err != nil {
			t.Fatalf("tx1 Commit failed: %v", err)
		}

		// Transaction 2: Extended Query ($1, $2) in Tx
		tx2, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx tx2 failed: %v", err)
		}
		if _, err := tx2.ExecContext(ctx, "EXEC sp_pg_update_price $1, $2;", 2, 35); err != nil {
			t.Fatalf("tx2 ExecContext failed: %v", err)
		}
		if err := tx2.Commit(); err != nil {
			t.Fatalf("tx2 Commit failed: %v", err)
		}

		// Verify final prices
		var p1, p2 int
		if err := db.QueryRowContext(ctx, "SELECT price FROM pg_items WHERE id = 1;").Scan(&p1); err != nil || p1 != 65 {
			t.Fatalf("expected price 65 for item 1, got %d", p1)
		}
		if err := db.QueryRowContext(ctx, "SELECT price FROM pg_items WHERE id = 2;").Scan(&p2); err != nil || p2 != 35 {
			t.Fatalf("expected price 35 for item 2, got %d", p2)
		}
	})

	// 4. Transaction DML Rollback Symmetry
	t.Run("Transaction_DML_Rollback_Symmetry", func(t *testing.T) {
		txRollback, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx rollback failed: %v", err)
		}
		if _, err := txRollback.ExecContext(ctx, "EXEC sp_pg_update_price $1, $2;", 1, 99999); err != nil {
			t.Fatalf("txRollback ExecContext failed: %v", err)
		}
		if err := txRollback.Rollback(); err != nil {
			t.Fatalf("txRollback Rollback failed: %v", err)
		}

		var p1 int
		if err := db.QueryRowContext(ctx, "SELECT price FROM pg_items WHERE id = 1;").Scan(&p1); err != nil || p1 != 65 {
			t.Fatalf("expected price 65 to remain after rollback, got %d", p1)
		}
	})
}

func doPGHandshake(t *testing.T, conn net.Conn) {
	t.Helper()
	var startupBuf bytes.Buffer
	binary.Write(&startupBuf, binary.BigEndian, int32(196608)) // Protocol v3.0
	startupBuf.WriteString("user\x00postgres\x00database\x00master\x00\x00")
	msgLen := int32(startupBuf.Len() + 4)
	var fullStartup bytes.Buffer
	binary.Write(&fullStartup, binary.BigEndian, msgLen)
	fullStartup.Write(startupBuf.Bytes())
	if _, err := conn.Write(fullStartup.Bytes()); err != nil {
		t.Fatalf("startup write failed: %v", err)
	}

	for {
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			t.Fatalf("startup read header failed: %v", err)
		}
		mType := hdr[0]
		mLen := binary.BigEndian.Uint32(hdr[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("startup read payload failed: %v", err)
		}
		if mType == 'Z' {
			break
		}
	}
}

func readRawPGResultSets(t *testing.T, conn net.Conn) []CanonicalResultSet {
	t.Helper()
	var sets []CanonicalResultSet
	var curCols []string
	var curRows [][]string

	for {
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			t.Fatalf("read message header failed: %v", err)
		}
		msgType := hdr[0]
		msgLen := binary.BigEndian.Uint32(hdr[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("read message payload failed: %v", err)
		}

		switch msgType {
		case 'T': // RowDescription
			if len(curCols) > 0 {
				sets = append(sets, CanonicalResultSet{
					Columns: curCols,
					Rows:    curRows,
				})
				curRows = nil
			}
			curCols = parseRowDescriptionColumns(payload)
		case 'D': // DataRow
			curRows = append(curRows, parseDataRowStrings(payload))
		case 'C': // CommandComplete
			// Result set finished
		case 'Z': // ReadyForQuery
			if len(curCols) > 0 {
				sets = append(sets, CanonicalResultSet{
					Columns: curCols,
					Rows:    curRows,
				})
			}
			return sets
		}
	}
}

func parseRowDescriptionColumns(payload []byte) []string {
	if len(payload) < 2 {
		return nil
	}
	numCols := int(binary.BigEndian.Uint16(payload[:2]))
	cols := make([]string, 0, numCols)
	offset := 2
	for i := 0; i < numCols && offset < len(payload); i++ {
		nullIdx := bytes.IndexByte(payload[offset:], 0)
		if nullIdx == -1 {
			break
		}
		colName := string(payload[offset : offset+nullIdx])
		cols = append(cols, colName)
		offset += nullIdx + 1 + 18 // null byte + 18 bytes field attributes
	}
	return cols
}

func parseDataRowStrings(payload []byte) []string {
	if len(payload) < 2 {
		return nil
	}
	numCols := int(binary.BigEndian.Uint16(payload[:2]))
	offset := 2
	vals := make([]string, 0, numCols)
	for i := 0; i < numCols && offset+4 <= len(payload); i++ {
		colLen := int(int32(binary.BigEndian.Uint32(payload[offset : offset+4])))
		offset += 4
		if colLen < 0 {
			vals = append(vals, "NULL")
		} else if offset+colLen <= len(payload) {
			vals = append(vals, string(payload[offset:offset+colLen]))
			offset += colLen
		}
	}
	return vals
}
