package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestTSQL_ISNULL_SQLIntegration(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. ISNULL(CAST(NULL AS VARCHAR(3)), 'abcdefgh') => 'abc'
	res, err := coord.ExecuteSQL("SELECT ISNULL(CAST(NULL AS VARCHAR(3)), 'abcdefgh') AS val;")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	val := fmt.Sprintf("%v", res.Rows[0]["val"])
	if val != "abc" {
		t.Errorf("expected 'abc', got %q", val)
	}

	// 2. COALESCE(CAST(NULL AS VARCHAR(3)), 'abcdefgh') => 'abcdefgh'
	resCoal, err := coord.ExecuteSQL("SELECT COALESCE(CAST(NULL AS VARCHAR(3)), 'abcdefgh') AS val;")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(resCoal.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resCoal.Rows))
	}
	valCoal := fmt.Sprintf("%v", resCoal.Rows[0]["val"])
	if valCoal != "abcdefgh" {
		t.Errorf("expected 'abcdefgh', got %q", valCoal)
	}
}
