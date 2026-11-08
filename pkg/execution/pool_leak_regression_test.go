package execution

import (
	"fmt"
	"sync"
	"testing"
)

// Regression test for P0-29: Chunk and ValuesSlice pool pointer retention & GC leak prevention
func TestExecution_Regression_P0_29_ChunkPoolCapZeroing(t *testing.T) {
	c := AcquireChunk()

	// Fill chunk with dummy tuples containing string data
	for i := 0; i < 50; i++ {
		c.Tuples = append(c.Tuples, Tuple{
			Values: []any{fmt.Sprintf("tuple_val_%d", i)},
		})
	}

	capBefore := cap(c.Tuples)
	// Truncate length before PutChunk to simulate filtering / partial batch
	c.Tuples = c.Tuples[:5]

	PutChunk(c)

	// Verify the entire capacity of c.Tuples is zeroed out
	tuplesFull := c.Tuples[:capBefore]
	for i, tup := range tuplesFull {
		if tup.Values != nil || tup.Index != nil || tup.Lazy != nil {
			t.Fatalf("c.Tuples[%d] not zeroed out at cap boundary: %v", i, tup)
		}
	}
}

func TestExecution_Regression_P0_29_ValuesSliceCapZeroing(t *testing.T) {
	s := AcquireValuesSlice(32)
	for i := 0; i < 32; i++ {
		s[i] = fmt.Sprintf("dynamic_query_val_%d", i)
	}

	capBefore := cap(s)
	// Truncate length before release
	s = s[:4]

	ReleaseValuesSlice(s)

	// Check underlying array capacity is zeroed out
	sFull := s[:capBefore]
	for i, val := range sFull {
		if val != nil {
			t.Fatalf("values slice[%d] not cleared up to cap: %v", i, val)
		}
	}
}

func TestExecution_Regression_P0_29_ConcurrentChunkRecycle(t *testing.T) {
	const numWorkers = 8
	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				c := AcquireChunk()
				c.Tuples = append(c.Tuples, Tuple{
					Values: []any{workerID},
				})
				ReleaseChunk(c)

				s := AcquireValuesSlice(16)
				s[0] = workerID
				ReleaseValuesSlice(s)
			}
		}(w)
	}

	wg.Wait()
}
