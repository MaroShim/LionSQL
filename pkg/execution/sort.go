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
	"strconv"
	"strings"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

type sortType uint8

const (
	sortTypeUnknown sortType = iota
	sortTypeInt
	sortTypeFloat
	sortTypeString
)

type sortItem struct {
	key        string
	descending bool
	nullsFirst *bool
	parsed     proc.Expr
	colIdx     int      // Pre-bound 0-based column slot (-1 if parsed expr or position)
	isPos      bool     // True if 1-based position (e.g. ORDER BY 1)
	posIdx     int      // 0-based position index (pos - 1)
	fastInt    bool     // True if single int64 column fast-path
	sType      sortType // Pre-bound primitive type classification
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

// topNHeap implements container/heap.Interface for bounded Top-N sort.
// It maintains a max-heap where the root element items[0] is the "worst" tuple
// according to the user-specified sort order, allowing O(1) worst-rejection
// and O(log k) replacement.
type topNHeap struct {
	items []Tuple
	cmp   func(tA, tB Tuple) int
}

func (h topNHeap) Len() int           { return len(h.items) }
func (h topNHeap) Less(i, j int) bool { return h.cmp(h.items[i], h.items[j]) > 0 } // root is worst
func (h topNHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *topNHeap) Push(x any)        { h.items = append(h.items, x.(Tuple)) }
func (h *topNHeap) Pop() any {
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
	limit      int64 // Maximum number of sorted rows needed (Top-N heapsort)
	runFiles   []string
	openFiles  []*os.File
	decoders   []*json.Decoder
	mHeap      *tupleMergeHeap
	spillCount int
	spillBytes int64
	sortMethod string // "quicksort" or "external merge"
	peakMemory     int64
	outChunk       Chunk
	outIdx         int
	arena          *storage.Arena
	singleIntKey   bool // True if sorting by exactly 1 key which is int64
	singleIntIdx   int  // Physical column index for single int64 column
	singleIntDesc  bool
	singleIntNulls *bool
	childIndexMap  *ColumnIndexMap
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
		cleanKey := sqlutil.CleanIdentifier(item.Column)
		var parsed proc.Expr
		colIdx := -1
		isPos := false
		posIdx := -1
		if pos, err := strconv.Atoi(cleanKey); err == nil && pos >= 1 {
			parsed = nil
			isPos = true
			posIdx = pos - 1
		} else if strings.ContainsAny(cleanKey, " +-*/%()") || strings.Contains(cleanKey, "->") {
			p := proc.NewParser(proc.NewLexer(cleanKey))
			e := p.ParseExpr()
			if e != nil && len(p.Errors()) == 0 {
				parsed = e
			}
		}
		s.items = append(s.items, sortItem{
			key:        cleanKey,
			descending: item.Desc,
			nullsFirst: item.NullsFirst,
			parsed:     parsed,
			colIdx:     colIdx,
			isPos:      isPos,
			posIdx:     posIdx,
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

// SetLimit configures the maximum number of sorted tuples required (enabling Top-N heapsort).
func (s *SortIterator) SetLimit(limit int64) {
	if limit > 0 {
		s.limit = limit
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

func toInt64Fast(val any) (int64, bool) {
	switch v := val.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case storage.Value:
		if v.Type == storage.TypeInt && !v.IsNull {
			return v.IVal, true
		}
	case int32:
		return int64(v), true
	case int16:
		return int64(v), true
	case int8:
		return int64(v), true
	case uint64:
		return int64(v), true
	case uint32:
		return int64(v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i, true
		}
	case float64:
		return int64(v), true
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

// [PERF-ORDER-BY-PREBOUND-MULTI-SLOT-1] bindSortSlots dynamically inspects the first tuple
// to bind single int64 fast path and multi-column primitive types (Int, Float, String) for reflection-free comparison.
func (s *SortIterator) bindSortSlots(firstTuple Tuple) {
	if len(s.items) == 1 {
		item := &s.items[0]
		targetIdx := -1
		if item.colIdx >= 0 {
			targetIdx = item.colIdx
		} else if item.isPos {
			targetIdx = item.posIdx
		}
		if targetIdx >= 0 {
			val, ok := firstTuple.GetIdx(targetIdx)
			if ok && val != nil {
				if _, isInt := toInt64Fast(val); isInt {
					s.singleIntKey = true
					s.singleIntIdx = targetIdx
					s.singleIntDesc = item.descending
					s.singleIntNulls = item.nullsFirst
					item.sType = sortTypeInt
					return
				}
			}
		}
	}

	for i := range s.items {
		item := &s.items[i]
		targetIdx := -1
		if item.colIdx >= 0 {
			targetIdx = item.colIdx
		} else if item.isPos {
			targetIdx = item.posIdx
		}
		if targetIdx >= 0 {
			val, ok := firstTuple.GetIdx(targetIdx)
			if ok && val != nil {
				switch val.(type) {
				case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
					item.sType = sortTypeInt
				case float32, float64:
					item.sType = sortTypeFloat
				case string:
					item.sType = sortTypeString
				}
			}
		}
	}
}

func (s *SortIterator) compareTuples(tA, tB Tuple) int {
	// [PERF-SORT-PREBOUND-COMPARATOR-1] Fast-path single int64 column comparison:
	// Completely bypasses strconv.Atoi, lookupVal string searching, and dynamic CompareValues reflection.
	if s.singleIntKey {
		idx := s.singleIntIdx
		vA, _ := tA.GetIdx(idx)
		vB, _ := tB.GetIdx(idx)
		if vA == nil && vB == nil {
			return 0
		}
		if (vA == nil || vB == nil) && s.singleIntNulls != nil {
			if *s.singleIntNulls {
				if vA == nil {
					return -1
				}
				return 1
			}
			if vA == nil {
				return 1
			}
			return -1
		}
		if vA == nil {
			return 1
		}
		if vB == nil {
			return -1
		}
		iA, isIntA := toInt64Fast(vA)
		iB, isIntB := toInt64Fast(vB)
		if isIntA && isIntB {
			if iA < iB {
				if s.singleIntDesc {
					return 1
				}
				return -1
			}
			if iA > iB {
				if s.singleIntDesc {
					return -1
				}
				return 1
			}
			return 0
		}
	}

	for i := range s.items {
		item := &s.items[i]
		var vA, vB any
		if item.colIdx >= 0 {
			vA, _ = tA.GetIdx(item.colIdx)
			vB, _ = tB.GetIdx(item.colIdx)
		} else if item.isPos {
			if item.posIdx < len(tA.Values) {
				vA = tA.Values[item.posIdx]
			}
			if item.posIdx < len(tB.Values) {
				vB = tB.Values[item.posIdx]
			}
		} else if item.parsed != nil {
			vA, _ = EvalTupleExpr(tA, item.parsed)
			vB, _ = EvalTupleExpr(tB, item.parsed)
		} else {
			vA, _ = lookupVal(tA, item.key)
			vB, _ = lookupVal(tB, item.key)
		}
		if vA == nil && vB == nil {
			continue
		}
		if (vA == nil || vB == nil) && item.nullsFirst != nil {
			if *item.nullsFirst {
				if vA == nil {
					return -1
				}
				return 1
			}
			if vA == nil {
				return 1
			}
			return -1
		}

		var cmp int
		switch item.sType {
		case sortTypeInt:
			iA, isA := toInt64Fast(vA)
			iB, isB := toInt64Fast(vB)
			if isA && isB {
				if iA < iB {
					cmp = -1
				} else if iA > iB {
					cmp = 1
				}
			} else {
				cmp = CompareValues(vA, vB)
			}
		case sortTypeFloat:
			fA, isA := toFloat64(vA)
			fB, isB := toFloat64(vB)
			if isA && isB {
				if fA < fB {
					cmp = -1
				} else if fA > fB {
					cmp = 1
				}
			} else {
				cmp = CompareValues(vA, vB)
			}
		case sortTypeString:
			sA, isA := vA.(string)
			sB, isB := vB.(string)
			if isA && isB {
				if sA < sB {
					cmp = -1
				} else if sA > sB {
					cmp = 1
				}
			} else {
				cmp = CompareValues(vA, vB)
			}
		default:
			cmp = CompareValues(vA, vB)
		}

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

func (s *SortIterator) alignDecodedTuple(t Tuple) Tuple {
	if s.childIndexMap == nil {
		return t
	}
	cols := s.childIndexMap.Columns()
	newVals := make([]any, len(cols))
	for i, col := range cols {
		newVals[i] = t.Get(col)
	}
	return Tuple{
		Values: newVals,
		Index:  s.childIndexMap,
	}
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

func (s *SortIterator) Open(ctx context.Context) (retErr error) {
	if s.child == nil {
		return fmt.Errorf("sort: child iterator is nil")
	}
	if err := s.child.Open(ctx); err != nil {
		return err
	}

	// [RULE:HandleHygiene] Any failure after spilling must release run files and handles
	// immediately, because callers do not Close() an iterator whose Open() failed.
	defer func() {
		if retErr != nil {
			s.releaseSpillResources()
			if s.child != nil {
				_ = s.child.Close()
			}
		}
	}()

	childCols := s.child.Schema()
	childIndexMap := s.child.ColIndexMap()
	if childIndexMap == nil && len(childCols) > 0 {
		childIndexMap = NewColumnIndexMapFromCols(childCols)
	}
	s.childIndexMap = childIndexMap

	if childIndexMap != nil {
		for _, item := range s.items {
			if pos, err := strconv.Atoi(item.key); err == nil {
				if pos < 1 || pos > len(childCols) {
					return fmt.Errorf("ORDER BY position %d is not in select list", pos)
				}
				continue
			}
			cleanKey := strings.TrimSpace(cleanIdentifierParts(item.key))
			if cleanKey == "" {
				continue
			}
			if item.parsed != nil {
				refs := planner.ExtractColumnRefs(item.key)
				for _, ref := range refs {
					col := ref.Column
					if _, ok := childIndexMap.Index(col); !ok {
						qualified := col
						if ref.TableOrAlias != "" {
							qualified = ref.TableOrAlias + "." + col
						}
						if _, ok2 := childIndexMap.Index(qualified); !ok2 {
							if sqlutil.IsSQLTypeName(col) {
								continue
							}
							return fmt.Errorf("column %q does not exist", col)
						}
					}
				}
				continue
			}
			if _, ok := childIndexMap.Index(cleanKey); !ok {
				bare := cleanKey
				if dot := strings.Index(bare, "."); dot != -1 {
					bare = bare[dot+1:]
				}
				if _, ok2 := childIndexMap.Index(bare); !ok2 {
					return fmt.Errorf("column %q does not exist", item.key)
				}
			}
		}

		// [PERF-SORT-PREBOUND-COMPARATOR-1] Pre-bind column index for O(1) direct slot comparison:
		// Avoids repetitive strconv.Atoi and lookupVal string searches during O(N log N) sort comparisons.
		for i := range s.items {
			item := &s.items[i]
			if item.isPos || item.parsed != nil {
				continue
			}
			cleanKey := strings.TrimSpace(cleanIdentifierParts(item.key))
			if idx, ok := childIndexMap.Index(cleanKey); ok && idx >= 0 {
				item.colIdx = idx
			} else if dot := strings.Index(cleanKey, "."); dot != -1 {
				bare := cleanKey[dot+1:]
				if idx, ok2 := childIndexMap.Index(bare); ok2 && idx >= 0 {
					item.colIdx = idx
				}
			}
		}
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

	// Top-N Heapsort Optimization:
	// When a limit k is specified, maintain a bounded max-heap of size at most k.
	// Non-qualifying incoming tuples are rejected in O(1) without expanding memory or spilling to disk.
	if s.limit > 0 {
		h := &topNHeap{cmp: s.compareTuples}
		heap.Init(h)
		firstBound := false

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
				if !firstBound {
					s.bindSortSlots(tuple)
					firstBound = true
				}
				tSize := EstimateTupleSize(tuple)
				if int64(h.Len()) < s.limit {
					heap.Push(h, tuple)
					curMem += tSize
				} else if s.compareTuples(tuple, h.items[0]) < 0 {
					oldSize := EstimateTupleSize(h.items[0])
					h.items[0] = tuple
					heap.Fix(h, 0)
					curMem = curMem - oldSize + tSize
				}
				if curMem > s.peakMemory {
					s.peakMemory = curMem
				}
			}

			if err == io.EOF {
				break
			}
		}

		s.tuples = h.items
		s.sortSlice(s.tuples)
		s.sortMethod = "top-N heapsort"
		return nil
	}

	// Materialize incoming child tuples in chunks
	firstBound := false
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
			if !firstBound {
				s.bindSortSlots(tuple)
				firstBound = true
			}
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
		if len(s.tuples) > 0 {
			s.bindSortSlots(s.tuples[0])
		}
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
		if err := dec.Decode(&firstTuple); err != nil {
			if err != io.EOF {
				return fmt.Errorf("sort merge decode run file %q failed: %w", runPath, err)
			}
		} else {
			firstTuple = s.alignDecodedTuple(firstTuple)
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
		if err := s.decoders[top.runIdx].Decode(&nextTuple); err != nil {
			if err != io.EOF {
				return fmt.Errorf("sort merge decode run file failed: %w", err)
			}
		} else {
			nextTuple = s.alignDecodedTuple(nextTuple)
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

	s.releaseSpillResources()

	if s.child != nil {
		return s.child.Close()
	}
	return nil
}

// releaseSpillResources closes open run-file handles and removes all temporary run files.
func (s *SortIterator) releaseSpillResources() {
	for _, f := range s.openFiles {
		_ = f.Close()
	}
	s.openFiles = nil
	s.decoders = nil

	for _, path := range s.runFiles {
		_ = os.Remove(path)
	}
	s.runFiles = nil
	s.mHeap = nil
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
