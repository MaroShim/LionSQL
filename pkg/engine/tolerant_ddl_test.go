package engine

import (
	"strings"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestTolerantDDLMode(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})

	// 1. Initial State: Strict mode (TolerantDDL = false)
	if coord.TolerantDDL() {
		t.Fatalf("expected TolerantDDL to be false by default")
	}

	// In strict mode: DROP INDEX on non-existent index must fail
	_, err := coord.ExecuteSQL("DROP INDEX idx_non_existent;")
	if err == nil {
		t.Fatalf("expected error dropping non-existent index in strict mode, got nil")
	}

	// Create table t1
	_, err = coord.ExecuteSQL("CREATE TABLE t1 (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("failed to create table t1: %v", err)
	}

	// In strict mode: duplicate CREATE TABLE without IF NOT EXISTS must fail
	_, err = coord.ExecuteSQL("CREATE TABLE t1 (id INT PRIMARY KEY);")
	if err == nil {
		t.Fatalf("expected error on duplicate CREATE TABLE in strict mode, got nil")
	}

	// In strict mode: duplicate ADD COLUMN without IF NOT EXISTS must fail
	_, err = coord.ExecuteSQL("ALTER TABLE t1 ADD COLUMN name VARCHAR(50);")
	if err == nil {
		t.Fatalf("expected error on duplicate ADD COLUMN in strict mode, got nil")
	}

	// 2. Enable Tolerant DDL Mode dynamically
	coord.SetTolerantDDL(true)
	if !coord.TolerantDDL() {
		t.Fatalf("expected TolerantDDL to be true after SetTolerantDDL(true)")
	}

	// Tolerant: DROP INDEX on non-existent index succeeds
	res, err := coord.ExecuteSQL("DROP INDEX idx_non_existent;")
	if err != nil {
		t.Fatalf("expected DROP INDEX to succeed in tolerant mode, got error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected res.Success to be true")
	}

	// Tolerant: duplicate CREATE TABLE succeeds with notice
	res, err = coord.ExecuteSQL("CREATE TABLE t1 (id INT PRIMARY KEY);")
	if err != nil {
		t.Fatalf("expected duplicate CREATE TABLE to succeed in tolerant mode, got error: %v", err)
	}
	if !res.Success || len(res.Messages) == 0 || !strings.Contains(res.Messages[0], "already exists (skipped)") {
		t.Fatalf("expected skipped message, got %v", res.Messages)
	}

	// Tolerant: duplicate ADD COLUMN succeeds without error
	res, err = coord.ExecuteSQL("ALTER TABLE t1 ADD COLUMN name VARCHAR(50);")
	if err != nil {
		t.Fatalf("expected duplicate ADD COLUMN to succeed in tolerant mode, got error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected res.Success to be true")
	}

	// Tolerant: DROP non-existent table succeeds
	res, err = coord.ExecuteSQL("DROP TABLE non_existent_tbl;")
	if err != nil {
		t.Fatalf("expected DROP non_existent table to succeed in tolerant mode, got error: %v", err)
	}

	// Tolerant: DROP CONSTRAINT / DROP FOREIGN KEY non-existent succeeds
	res, err = coord.ExecuteSQL("ALTER TABLE t1 DROP CONSTRAINT non_existent_fk;")
	if err != nil {
		t.Fatalf("expected DROP CONSTRAINT to succeed, got: %v", err)
	}

	res, err = coord.ExecuteSQL("ALTER TABLE t1 DROP FOREIGN KEY non_existent_fk;")
	if err != nil {
		t.Fatalf("expected DROP FOREIGN KEY to succeed, got: %v", err)
	}

	// Tolerant: ALTER TABLE SET storage options succeeds
	res, err = coord.ExecuteSQL("ALTER TABLE t1 SET (autovacuum_vacuum_scale_factor = 0.1);")
	if err != nil {
		t.Fatalf("expected ALTER TABLE SET to succeed, got: %v", err)
	}

	// 3. Switch back to Strict Mode dynamically
	coord.SetTolerantDDL(false)
	if coord.TolerantDDL() {
		t.Fatalf("expected TolerantDDL to be false after SetTolerantDDL(false)")
	}

	// Verify that strict error behavior returns immediately
	_, err = coord.ExecuteSQL("DROP INDEX idx_non_existent_2;")
	if err == nil {
		t.Fatalf("expected error dropping non-existent index after disabling tolerant mode")
	}
	_ = time.Now()
}
