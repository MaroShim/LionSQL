package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestTypeValidator_SQLIntegration(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. VARCHAR(3) truncation check
	if _, err := coord.ExecuteSQL("CREATE TABLE _t_varchartest (id INT PRIMARY KEY, code VARCHAR(3));"); err != nil {
		t.Fatalf("failed to create varchar test table: %v", err)
	}
	_, err := coord.ExecuteSQL("INSERT INTO _t_varchartest VALUES (1, '1234567890');")
	if err == nil {
		t.Fatalf("expected 10 chars into VARCHAR(3) to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("expected truncation error, got: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_varchartest VALUES (1, 'abc');"); err != nil {
		t.Fatalf("expected valid 3-char insert to succeed, got: %v", err)
	}

	// 2. DECIMAL(5,2) overflow check
	if _, err := coord.ExecuteSQL("CREATE TABLE _t_dectest (id INT PRIMARY KEY, price DECIMAL(5, 2));"); err != nil {
		t.Fatalf("failed to create decimal test table: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO _t_dectest VALUES (1, 9999999.99);")
	if err == nil {
		t.Fatalf("expected 9999999.99 into DECIMAL(5,2) to fail with overflow, but succeeded")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Errorf("expected overflow error, got: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_dectest VALUES (1, 999.99);"); err != nil {
		t.Fatalf("expected 999.99 into DECIMAL(5,2) to succeed, got: %v", err)
	}

	// 3. TINYINT range check
	if _, err := coord.ExecuteSQL("CREATE TABLE _t_tinytest (id INT PRIMARY KEY, age TINYINT);"); err != nil {
		t.Fatalf("failed to create tinyint test table: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO _t_tinytest VALUES (1, 999);")
	if err == nil {
		t.Fatalf("expected 999 into TINYINT to fail with overflow, but succeeded")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Errorf("expected overflow error, got: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_tinytest VALUES (1, 255);"); err != nil {
		t.Fatalf("expected 255 into TINYINT to succeed, got: %v", err)
	}

	// 4. CHAR(5) space padding check
	if _, err := coord.ExecuteSQL("CREATE TABLE _t_chartest (id INT PRIMARY KEY, code CHAR(5));"); err != nil {
		t.Fatalf("failed to create char test table: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_chartest VALUES (1, 'abc');"); err != nil {
		t.Fatalf("expected 'abc' into CHAR(5) to succeed, got: %v", err)
	}
	res, err := coord.ExecuteSQL("SELECT code FROM _t_chartest WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	val := fmt.Sprintf("%v", res.Rows[0]["code"])
	if val != "abc  " {
		t.Fatalf("expected 'abc  ' (len 5), got %q (len %d)", val, len(val))
	}
}
