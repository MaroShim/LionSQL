package engine

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestQueryErrorLogging_FailClosedVisibility(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(t.TempDir()); err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{SlowThreshold: 50 * time.Millisecond})
	defer coord.Close()

	var buf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origOutput)

	// 1. Valid fast query -> Zero query logs (silence on success)
	buf.Reset()
	res, err := coord.ExecuteSQL("SELECT 1 AS val;")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got err=%v res=%+v", err, res)
	}
	if strings.Contains(buf.String(), "[QUERY-ERROR]") {
		t.Errorf("successful query must not produce [QUERY-ERROR], got: %s", buf.String())
	}
	if strings.Contains(buf.String(), "[SLOW-QUERY]") {
		t.Errorf("fast query must not produce [SLOW-QUERY], got: %s", buf.String())
	}

	// 2. Failing query -> Must output [QUERY-ERROR] with error text and query
	buf.Reset()
	_, err = coord.ExecuteSQL("SELECT * FROM non_existent_table_xyz_123;")
	if err == nil {
		t.Fatalf("expected error querying non-existent table, got nil")
	}

	output := buf.String()
	if !strings.Contains(output, "[QUERY-ERROR]") {
		t.Errorf("expected output to contain '[QUERY-ERROR]', got:\n%s", output)
	}
	if !strings.Contains(output, "non_existent_table_xyz_123") {
		t.Errorf("expected output to contain query text, got:\n%s", output)
	}

	// 3. Slow query (exceeding threshold) -> Must output [SLOW-QUERY]
	coord.SlowQueries().SetThreshold(1 * time.Nanosecond)
	buf.Reset()
	_, err = coord.ExecuteSQL("SELECT 100 AS num, 200 AS val;")
	if err != nil {
		t.Fatalf("unexpected query error: %v", err)
	}
	slowOutput := buf.String()
	if !strings.Contains(slowOutput, "[SLOW-QUERY]") {
		t.Errorf("expected output to contain '[SLOW-QUERY]', got:\n%s", slowOutput)
	}
}
