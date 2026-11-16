package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestEngine_GenerateSeries(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	// 1. Basic FROM generate_series(1, 5)
	res, err := coord.ExecuteSQL("SELECT value FROM generate_series(1, 5);")
	if err != nil {
		t.Fatalf("SELECT FROM generate_series failed: %v", err)
	}
	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(res.Rows))
	}
	for i, r := range res.Rows {
		expected := fmt.Sprintf("%d", i+1)
		got := fmt.Sprintf("%v", r["value"])
		if got != expected {
			t.Errorf("row %d: expected %s, got %s", i, expected, got)
		}
	}

	// 2. Projection with generate_series in SELECT without FROM
	res2, err := coord.ExecuteSQL("SELECT cast(generate_series(1, 10) as int) AS num;")
	if err != nil {
		t.Fatalf("SELECT generate_series in projection failed: %v", err)
	}
	if len(res2.Rows) != 10 {
		t.Fatalf("expected 10 rows, got %d", len(res2.Rows))
	}
}
