package execution

import (
	"bufio"
	"container/heap"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

type sortItem struct {
	key        string
	descending bool
}

// tupleMergeHeap implements container/heap.Interface for N-way merge of sorted run files.
type heapItem struct {
	tuple  Tuple
	runIdx int
}

type tupleMergeHeap struct {
	items []heapItem
	cmp   func(tA, tB Tuple) int
}

func (h tupleMergeHeap) Len() int           { return len(h.items) }
func (h tupleMergeHeap) Less(i, j int) bool { return h.cmp(h.items[i].tuple, h.items[j].tuple) < 0 }
func (h tupleMergeHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *tupleMergeHeap) Push(x any)        { h.items = append(h.items, x.(heapItem)) }
func (h *tupleMergeHeap) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	h.items = old[0 : n-1]
	return item
}

// SortIterator buffers incoming tuples, sorts them by one or more order keys, and streams them out.
// When memory exceeds workMem, it automatically spills sorted runs to temporary disk files
// and merges them on-demand via an N-way Min-Heap (External Merge Sort).
type SortIterator struct {
	child      Iterator
	items      []sortItem
	tuples     []Tuple
	idx        int
	workMem    int64
	tempDir    string
	runFiles   []string
	openFiles  []*os.File
	decoders   []*json.Decoder
	mHeap      *tupleMergeHeap
	spillCount int
	spillBytes int64
	sortMethod string // "quicksort" or "external merge"
	peakMemory int64
	outChunk   Chunk
	outIdx     int
	arena      *storage.Arena
}

// SetArena configures an optional memory arena for sort buffer and tuple allocations.
func (s *SortIterator) SetArena(a *storage.Arena) {
	s.arena = a
}

// NewSortIterator creates a new sort operator from an ORDER BY clause string (e.g. "age DESC, name ASC").
func NewSortIterator(child Iterator, orderByClause string) *SortIterator {
	s := &SortIterator{
		child:      child,
		workMem:    DefaultWorkMem,
		tempDir:    os.TempDir(),
		sortMethod: "quicksort",
	}

	for _, item := range sqlutil.ParseOrderBy(orderByClause) {
		s.items = append(s.items, sortItem{
			key:        sqlutil.CleanIdentifier(item.Column),
			descending: item.Desc,
		})
	}

	return s
}

// SetWorkMem sets the maximum in-memory budget in bytes before spilling to disk.
func (s *SortIterator) SetWorkMem(bytes int64) {
	if bytes > 0 {
		s.workMem = bytes
	}
}

// SetTempDir sets the scratch directory where temporary run files are created.
func (s *SortIterator) SetTempDir(dir string) {
	if dir != "" {
		s.tempDir = dir
	}
}

// SortMethod returns the execution method used ("quicksort" or "external merge").
func (s *SortIterator) SortMethod() string {
	return s.sortMethod
}

// SpillCount returns the number of temporary run files spilled to disk.
func (s *SortIterator) SpillCount() int {
	return s.spillCount
}

// SpillBytes returns the total bytes of data spilled to disk runs.
func (s *SortIterator) SpillBytes() int64 {
	return s.spillBytes
}

// PeakMemory returns the peak in-memory bytes used during sorting.
func (s *SortIterator) PeakMemory() int64 {
	return s.peakMemory
}

func (s *SortIterator) compareTuples(tA, tB Tuple) int {
	for _, item := range s.items {
		vA, _ := lookupVal(tA, item.key)
		vB, _ := lookupVal(tB, item.key)
		cmp := CompareValues(vA, vB)
		if cmp != 0 {
			if item.descending {
				return -cmp
			}
			return cmp
		}
	}
	return 0
}

func (s *SortIterator) sortSlice(tuples []Tuple) {
	if len(s.items) == 0 || len(tuples) <= 1 {
		return
	}
	sort.SliceStable(tuples, func(i, j int) bool {
		return s.compareTuples(tuples[i], tuples[j]) < 0
	})
}

func (s *SortIterator) spillRun() error {
	s.sortSlice(s.tuples)

	f, err := os.CreateTemp(s.tempDir, "lionsql-sort-run-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create sort spill file: %w", err)
	}

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, t := range s.tuples {
		if err := enc.Encode(t); err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return fmt.Errorf("failed to encode tuple in sort spill: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return fmt.Errorf("failed to flush sort spill: %w", err)
	}
	_ = f.Close()

	s.runFiles = append(s.runFiles, f.Name())
	s.spillCount++
	s.spillBytes += s.peakMemory
	s.tuples = s.tuples[:0]
	return nil
}

func (s *SortIterator) Open(ctx context.Context) error {
	if s.child == nil {
		return fmt.Errorf("sort: child iterator is nil")
	}
	if err := s.child.Open(ctx); err != nil {
		return err
	}

	if s.workMem <= 0 {
		s.workMem = DefaultWorkMem
	}
	if s.tempDir == "" {
		s.tempDir = os.TempDir()
	}

	s.tuples = nil
	s.idx = 0
	s.spillCount = 0
	s.spillBytes = 0
	s.runFiles = nil
	s.openFiles = nil
	s.decoders = nil
	s.mHeap = nil
	s.sortMethod = "quicksort"
	s.peakMemory = 0
	s.outChunk.Reset()
	s.outIdx = 0

	var curMem int64 = 0
	inChunk := GetChunk()
	defer PutChunk(inChunk)

	// Materialize incoming child tuples in chunks
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := PullChunk(ctx, s.child, inChunk, DefaultChunkSize)
		if err != nil && err != io.EOF {
			return err
		}
		if inChunk.Len() == 0 && err == io.EOF {
			break
		}

		for _, tuple := range inChunk.Tuples {
			tSize := EstimateTupleSize(tuple)
			if curMem+tSize > s.workMem && len(s.tuples) > 0 {
				if err := s.spillRun(); err != nil {
					return err
				}
				curMem = 0
			}

			s.tuples = append(s.tuples, tuple)
			curMem += tSize
			if curMem > s.peakMemory {
				s.peakMemory = curMem
			}
		}

		if err == io.EOF {
			break
		}
	}

	// Case 1: In-Memory Fast Path (all data fits in workMem)
	if len(s.runFiles) == 0 {
		s.sortSlice(s.tuples)
		s.sortMethod = "quicksort"
		return nil
	}

	// Case 2: Spill to Disk Path (External Merge Sort)
	if len(s.tuples) > 0 {
		if err := s.spillRun(); err != nil {
			return err
		}
		s.tuples = nil
	}

	s.sortMethod = "external merge"

	s.mHeap = &tupleMergeHeap{
		cmp: s.compareTuples,
	}
	heap.Init(s.mHeap)

	for i, runPath := range s.runFiles {
		f, err := os.Open(runPath)
		if err != nil {
			return fmt.Errorf("failed to open sort run %q: %w", runPath, err)
		}
		s.openFiles = append(s.openFiles, f)
		dec := json.NewDecoder(bufio.NewReader(f))
		dec.UseNumber()
		s.decoders = append(s.decoders, dec)

		var firstTuple Tuple
		if err := dec.Decode(&firstTuple); err == nil {
			heap.Push(s.mHeap, heapItem{tuple: firstTuple, runIdx: i})
		}
	}

	return nil
}

func (s *SortIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// In-memory reading
	if len(s.runFiles) == 0 {
		if s.idx >= len(s.tuples) {
			return io.EOF
		}
		for dest.Len() < DefaultChunkSize && s.idx < len(s.tuples) {
			dest.Tuples = append(dest.Tuples, s.tuples[s.idx])
			s.idx++
		}
		return nil
	}

	// External merge reading via Min-Heap
	if s.mHeap == nil || s.mHeap.Len() == 0 {
		return io.EOF
	}

	for dest.Len() < DefaultChunkSize && s.mHeap.Len() > 0 {
		top := heap.Pop(s.mHeap).(heapItem)
		dest.Tuples = append(dest.Tuples, top.tuple)

		// Advance reader of the stream that just emitted top
		var nextTuple Tuple
		if err := s.decoders[top.runIdx].Decode(&nextTuple); err == nil {
			heap.Push(s.mHeap, heapItem{tuple: nextTuple, runIdx: top.runIdx})
		}
	}

	if dest.Len() == 0 {
		return io.EOF
	}
	return nil
}

func (s *SortIterator) Next(ctx context.Context) (Tuple, error) {
	if s.outIdx < len(s.outChunk.Tuples) {
		t := s.outChunk.Tuples[s.outIdx]
		s.outIdx++
		return t, nil
	}
	s.outChunk.Reset()
	s.outIdx = 0
	if err := s.NextChunk(ctx, &s.outChunk); err != nil {
		return Tuple{}, err
	}
	if len(s.outChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := s.outChunk.Tuples[0]
	s.outIdx = 1
	return t, nil
}

func (s *SortIterator) Close() error {
	s.tuples = nil
	s.idx = 0
	s.outChunk.Reset()
	s.outIdx = 0

	for _, f := range s.openFiles {
		_ = f.Close()
	}
	s.openFiles = nil
	s.decoders = nil

	// Clean up all temporary run files
	for _, path := range s.runFiles {
		_ = os.Remove(path)
	}
	s.runFiles = nil
	s.mHeap = nil

	if s.child != nil {
		return s.child.Close()
	}
	return nil
}

func (s *SortIterator) Schema() []string {
	if s.child != nil {
		return s.child.Schema()
	}
	return nil
}

func (s *SortIterator) ColIndexMap() *ColumnIndexMap {
	if s.child != nil {
		return s.child.ColIndexMap()
	}
	return nil
}
