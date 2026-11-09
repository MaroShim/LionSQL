package execution_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"lionsql/pkg/execution"
)

// errAfterRowsIterator yields all buffered tuples and then fails with a non-EOF error.
type errAfterRowsIterator struct {
	*mockIterator
	err error
}

func (e *errAfterRowsIterator) Next(ctx context.Context) (execution.Tuple, error) {
	t, err := e.mockIterator.Next(ctx)
	if err == nil {
		return t, nil
	}
	return execution.Tuple{}, e.err
}

// TestP1_BatchM2_SortOpenErrorRemovesSpillFiles verifies Defect 4-2 / Scope-089: when
// SortIterator.Open fails after spilling runs to disk, the temp run files must be removed
// even if the caller never reaches Close().
func TestP1_BatchM2_SortOpenErrorRemovesSpillFiles(t *testing.T) {
	tempDir := t.TempDir()
	var rows []map[string]any
	// DefaultChunkSize is 1024 rows: the first chunk is consumed (and spilled) before the
	// child error surfaces on the second PullChunk.
	for i := 0; i < 1100; i++ {
		rows = append(rows, map[string]any{
			"id":      int64(2000 - i),
			"payload": strings.Repeat("x", 64),
		})
	}
	boom := errors.New("child failure after spill")
	child := &errAfterRowsIterator{
		mockIterator: newMockIterator([]string{"id", "payload"}, rows...),
		err:          boom,
	}
	sorter := execution.NewSortIterator(child, "id ASC")
	sorter.SetWorkMem(8192)
	sorter.SetTempDir(tempDir)

	err := sorter.Open(context.Background())
	if err == nil {
		t.Fatalf("expected Open to fail with child error")
	}

	entries, rerr := os.ReadDir(tempDir)
	if rerr != nil {
		t.Fatalf("ReadDir failed: %v", rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected spill files to be removed after failed Open, found %d leftover file(s)", len(entries))
	}
}
