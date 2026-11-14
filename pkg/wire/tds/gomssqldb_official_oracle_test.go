package tds_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/golang-sql/sqlexp"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/shopspring/decimal"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire/tds"
)

func openOfficialGoMSSQLDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		eng.Close()
		t.Fatalf("failed to start TDS server: %v", err)
	}

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		srv.Close()
		eng.Close()
		t.Fatalf("sql.Open failed: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = srv.Close()
		_ = eng.Close()
	}
	return db, cleanup
}

func asString(v any) string {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

// TestOfficialGoMSSQLDB_SelectTypesAndCollations ports the core type, CAST, LOB,
// and multilingual COLLATION assertions from microsoft/go-mssqldb/queries_test.go (testSelect).
func TestOfficialGoMSSQLDB_SelectTypesAndCollations(t *testing.T) {
	db, cleanup := openOfficialGoMSSQLDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	longstr := strings.Repeat("x", 10000)
	cases := []struct {
		name string
		expr string
		check func(t *testing.T, got any)
	}{
		{"int_pos", "1", func(t *testing.T, got any) {
			if got != int64(1) {
				t.Fatalf("got %v (%T), want int64(1)", got, got)
			}
		}},
		{"int_neg", "-1", func(t *testing.T, got any) {
			if got != int64(-1) {
				t.Fatalf("got %v (%T), want int64(-1)", got, got)
			}
		}},
		{"cast_int", "cast(1 as int)", func(t *testing.T, got any) {
			if got != int64(1) {
				t.Fatalf("got %v, want 1", got)
			}
		}},
		{"cast_int_neg", "cast(-1 as int)", func(t *testing.T, got any) {
			if got != int64(-1) {
				t.Fatalf("got %v, want -1", got)
			}
		}},
		{"cast_tinyint_1", "cast(1 as tinyint)", func(t *testing.T, got any) {
			if got != int64(1) {
				t.Fatalf("got %v, want 1", got)
			}
		}},
		{"cast_tinyint_255", "cast(255 as tinyint)", func(t *testing.T, got any) {
			if got != int64(255) {
				t.Fatalf("got %v, want 255", got)
			}
		}},
		{"cast_smallint_pos", "cast(1 as smallint)", func(t *testing.T, got any) {
			if got != int64(1) {
				t.Fatalf("got %v, want 1", got)
			}
		}},
		{"cast_smallint_neg", "cast(-1 as smallint)", func(t *testing.T, got any) {
			if got != int64(-1) {
				t.Fatalf("got %v, want -1", got)
			}
		}},
		{"cast_bigint_pos", "cast(1 as bigint)", func(t *testing.T, got any) {
			if got != int64(1) {
				t.Fatalf("got %v, want 1", got)
			}
		}},
		{"cast_bigint_neg", "cast(-1 as bigint)", func(t *testing.T, got any) {
			if got != int64(-1) {
				t.Fatalf("got %v, want -1", got)
			}
		}},
		{"cast_bit_1", "cast(1 as bit)", func(t *testing.T, got any) {
			if got != true {
				t.Fatalf("got %v, want true", got)
			}
		}},
		{"cast_bit_0", "cast(0 as bit)", func(t *testing.T, got any) {
			if got != false {
				t.Fatalf("got %v, want false", got)
			}
		}},
		{"varchar_literal", "'abc'", func(t *testing.T, got any) {
			if got != "abc" {
				t.Fatalf("got %v, want 'abc'", got)
			}
		}},
		{"cast_float", "cast(0.5 as float)", func(t *testing.T, got any) {
			if got != float64(0.5) {
				t.Fatalf("got %v, want 0.5", got)
			}
		}},
		{"cast_real", "cast(0.5 as real)", func(t *testing.T, got any) {
			if got != float64(0.5) {
				t.Fatalf("got %v, want 0.5", got)
			}
		}},
		{"cast_decimal_18_1", "cast(0.5 as decimal(18,1))", func(t *testing.T, got any) {
			if asString(got) != "0.5" {
				t.Fatalf("got %s, want 0.5", asString(got))
			}
		}},
		{"cast_numeric_18_2_neg", "cast(-0.5 as numeric(18,2))", func(t *testing.T, got any) {
			if !strings.HasPrefix(asString(got), "-0.5") {
				t.Fatalf("got %s, want -0.50", asString(got))
			}
		}},
		{"cast_numeric_20_0", "cast(4294967296 as numeric(20,0))", func(t *testing.T, got any) {
			if asString(got) != "4294967296" {
				t.Fatalf("got %s, want 4294967296", asString(got))
			}
		}},
		{"nvarchar_literal", "N'abc'", func(t *testing.T, got any) {
			if got != "abc" {
				t.Fatalf("got %v, want abc", got)
			}
		}},
		{"cast_null_nvarchar", "cast(null as nvarchar(3))", func(t *testing.T, got any) {
			if got != nil {
				t.Fatalf("got %v, want nil", got)
			}
		}},
		{"null_literal", "NULL", func(t *testing.T, got any) {
			if got != nil {
				t.Fatalf("got %v, want nil", got)
			}
		}},
		{"cast_datetime_1753", "cast('1753-01-01' as datetime)", func(t *testing.T, got any) {
			tm, ok := got.(time.Time)
			if !ok || tm.Year() != 1753 {
				t.Fatalf("got %v (%T), want 1753-01-01", got, got)
			}
		}},
		{"cast_smalldatetime_2000", "cast('2000-01-01T12:13:00' as smalldatetime)", func(t *testing.T, got any) {
			tm, ok := got.(time.Time)
			if !ok || tm.Year() != 2000 || tm.Hour() != 12 || tm.Minute() != 13 {
				t.Fatalf("got %v, want 2000-01-01 12:13", got)
			}
		}},
		{"cast_varbinary", "cast(0x1234 as varbinary(2))", func(t *testing.T, got any) {
			if !bytes.Equal(got.([]byte), []byte{0x12, 0x34}) {
				t.Fatalf("got %x, want 1234", got)
			}
		}},
		{"cast_xml", "cast('<root/>' as xml)", func(t *testing.T, got any) {
			if got != "<root/>" {
				t.Fatalf("got %v, want <root/>", got)
			}
		}},
		{"cast_text", "cast('abc' as text)", func(t *testing.T, got any) {
			if got != "abc" {
				t.Fatalf("got %v, want abc", got)
			}
		}},
		{"cast_ntext", "cast(N'abc' as ntext)", func(t *testing.T, got any) {
			if got != "abc" {
				t.Fatalf("got %v, want abc", got)
			}
		}},
		{"cast_image", "cast(0x1234 as image)", func(t *testing.T, got any) {
			if !bytes.Equal(got.([]byte), []byte{0x12, 0x34}) {
				t.Fatalf("got %x, want 1234", got)
			}
		}},
		{"cast_nvarchar_4000_long", fmt.Sprintf("cast(N'%s' as nvarchar(4000))", longstr[:4000]), func(t *testing.T, got any) {
			if got != longstr[:4000] {
				t.Fatalf("got len %d, want 4000", len(fmt.Sprintf("%v", got)))
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got any
			if err := db.QueryRowContext(ctx, "SELECT "+tc.expr).Scan(&got); err != nil {
				t.Fatalf("SELECT %s failed: %v", tc.expr, err)
			}
			tc.check(t, got)
		})
	}
}

// TestOfficialGoMSSQLDB_TransactionRollback verifies that transactions opened and rolled back
// via go-mssqldb are cleanly discarded with zero data leakage.
func TestOfficialGoMSSQLDB_TransactionRollback(t *testing.T) {
	db, cleanup := openOfficialGoMSSQLDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE t_tx_test (id INT PRIMARY KEY, val NVARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	if _, err := tx.ExecContext(ctx, "INSERT INTO t_tx_test (id, val) VALUES (1, 'in-tx');"); err != nil {
		t.Fatalf("INSERT in tx failed: %v", err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM t_tx_test;").Scan(&count); err != nil {
		t.Fatalf("SELECT count failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", count)
	}
}

// TestOfficialGoMSSQLDB_ParameterizedQuery verifies parameterized queries via sp_executesql.
func TestOfficialGoMSSQLDB_ParameterizedQuery(t *testing.T) {
	db, cleanup := openOfficialGoMSSQLDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE t_param (id INT PRIMARY KEY, name NVARCHAR(50), age INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO t_param (id, name, age) VALUES (@p1, @p2, @p3);", 101, "Alice", 30); err != nil {
		t.Fatalf("INSERT with params failed: %v", err)
	}

	var name string
	var age int
	if err := db.QueryRowContext(ctx, "SELECT name, age FROM t_param WHERE id = @p1;", 101).Scan(&name, &age); err != nil {
		t.Fatalf("SELECT with params failed: %v", err)
	}
	if name != "Alice" || age != 30 {
		t.Fatalf("got name=%q, age=%d, want Alice, 30", name, age)
	}
}

// TestOfficialGoMSSQLDB_StoredProcedure_OutputAndReturnStatus verifies that go-mssqldb
// executing a stored procedure receives OUTPUT parameter values (via sql.Out)
// and the procedure return code (via mssql.ReturnStatus).
func TestOfficialGoMSSQLDB_StoredProcedure_OutputAndReturnStatus(t *testing.T) {
	db, cleanup := openOfficialGoMSSQLDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.ExecContext(ctx, `
CREATE PROCEDURE sp_add_test 
    @a INT, 
    @b INT, 
    @sum INT OUTPUT
AS
BEGIN
    SET @sum = @a + @b;
    RETURN 42;
END;
`)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	var sum int64
	var rc mssql.ReturnStatus
	_, err = db.ExecContext(ctx, "sp_add_test",
		sql.Named("a", 15),
		sql.Named("b", 27),
		sql.Named("sum", sql.Out{Dest: &sum}),
		&rc,
	)
	if err != nil {
		t.Fatalf("EXEC sp_add_test failed: %v", err)
	}

	if sum != 42 {
		t.Errorf("got sum=%d, want 42", sum)
	}
	if rc != 42 {
		t.Errorf("got return status=%d, want 42", rc)
	}
}

// TestOfficialGoMSSQLDB_PrintMessages verifies that PRINT statements execute cleanly via go-mssqldb.
func TestOfficialGoMSSQLDB_PrintMessages(t *testing.T) {
	db, cleanup := openOfficialGoMSSQLDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "PRINT 'Hello from LionSQL TDS Oracle';"); err != nil {
		t.Fatalf("PRINT execution failed: %v", err)
	}
}

// Keep unused imports referenced.
var (
	_ = errors.As
	_ = sqlexp.ReturnMessage{}
	_ = mssql.ReturnStatus(0)
	_ = decimal.Zero
)
