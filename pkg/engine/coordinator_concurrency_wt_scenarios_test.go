//go:build cgo && wiredtiger

package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/wiredtiger"
)

// Scenario B: Both session 1 and session 2 use explicit transactions (BEGIN TRAN)
func TestWT_Concurrent_BothExplicitTx_UpdateSameRow(t *testing.T) {
	dir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE wt_scen_b (id INT PRIMARY KEY, val INT)")
	coord.ExecuteSQL("INSERT INTO wt_scen_b VALUES (1, 10)")

	s1 := "s1"
	s2 := "s2"

	coord.ExecuteSessionSQL(s1, "BEGIN TRAN")
	coord.ExecuteSessionSQL(s1, "UPDATE wt_scen_b SET val = 20 WHERE id = 1")

	coord.ExecuteSessionSQL(s2, "BEGIN TRAN")
	_, err2 := coord.ExecuteSessionSQL(s2, "UPDATE wt_scen_b SET val = 30 WHERE id = 1")
	t.Logf("Scenario B (Both Explicit) err2: %v", err2)
	if err2 == nil {
		t.Errorf("CRITICAL: Scenario B allowed Session 2 update without error!")
	}
}

// Scenario F: Non-PK WHERE condition (full table scan update)
func TestWT_Concurrent_NonPK_Update(t *testing.T) {
	dir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE wt_scen_f (id INT PRIMARY KEY, category VARCHAR(20), val INT)")
	coord.ExecuteSQL("INSERT INTO wt_scen_f VALUES (1, 'A', 10)")

	s1 := "s1"
	s2 := "s2"

	coord.ExecuteSessionSQL(s1, "BEGIN TRAN")
	coord.ExecuteSessionSQL(s1, "UPDATE wt_scen_f SET val = 20 WHERE category = 'A'")

	_, err2 := coord.ExecuteSessionSQL(s2, "UPDATE wt_scen_f SET val = 30 WHERE category = 'A'")
	t.Logf("Scenario F (Non-PK condition) err2: %v", err2)
	if err2 == nil {
		t.Errorf("CRITICAL: Scenario F allowed Session 2 update without error!")
	}
}

// Scenario G: Table without primary key
func TestWT_Concurrent_NoPK_Table(t *testing.T) {
	dir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE wt_scen_g (val INT)")
	coord.ExecuteSQL("INSERT INTO wt_scen_g VALUES (10)")

	s1 := "s1"
	s2 := "s2"

	coord.ExecuteSessionSQL(s1, "BEGIN TRAN")
	coord.ExecuteSessionSQL(s1, "UPDATE wt_scen_g SET val = 20")

	_, err2 := coord.ExecuteSessionSQL(s2, "UPDATE wt_scen_g SET val = 30")
	t.Logf("Scenario G (No-PK table) err2: %v", err2)
	if err2 == nil {
		t.Errorf("CRITICAL: Scenario G allowed Session 2 update without error!")
	}
}
