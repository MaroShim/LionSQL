package execution

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
)

type hashMatch struct {
	tuple Tuple
	idx   int
}

// HashJoinIterator implements in-memory Hash Join between two relations.
// The right child (inner) is buffered into a hash map during Open(), and
// the left child (outer) is probed tuple-by-tuple during Next().
type HashJoinIterator struct {
	left           Iterator // Outer / Probe
	right          Iterator // Inner / Build
	joinType       string   // "INNER", "LEFT", "RIGHT", "FULL", "SEMI", "ANTI"
	leftKey        string
	rightKey       string
	hashTable      map[string][]hashMatch
	allRightTuples []Tuple
	matchedRight   []bool
	unmatchedIdx   int
	currentProbe   Tuple
	currentMatches []hashMatch
	matchIdx       int
	emittedAny     bool
	workMem        int64
	buildMemory    int64
	colIndexMap    *ColumnIndexMap
	inChunk        Chunk
	inIdx          int
	outChunk       Chunk
	outIdx         int
	arena          *storage.Arena
}

// NewHashJoinIterator creates a new Hash Join operator.
func NewHashJoinIterator(left, right Iterator, joinType, leftKey, rightKey string) *HashJoinIterator {
	jType := strings.ToUpper(strings.TrimSpace(joinType))
	if jType == "" {
		jType = "INNER"
	}
	return &HashJoinIterator{
		left:      left,
		right:     right,
		joinType:  jType,
		leftKey:   strings.TrimSpace(leftKey),
		rightKey:  strings.TrimSpace(rightKey),
		hashTable: make(map[string][]hashMatch),
		workMem:   DefaultWorkMem,
	}
}

// SetArena configures an optional memory arena for zero-GC key string and merged tuple allocation.
func (h *HashJoinIterator) SetArena(a *storage.Arena) {
	h.arena = a
}

// SetWorkMem sets the maximum memory budget for the hash table.
func (h *HashJoinIterator) SetWorkMem(bytes int64) {
	if bytes > 0 {
		h.workMem = bytes
	}
}

// BuildMemory returns the bytes used by the hash table.
func (h *HashJoinIterator) BuildMemory() int64 {
	return h.buildMemory
}

func (h *HashJoinIterator) Open(ctx context.Context) error {
	if h.left == nil || h.right == nil {
		return fmt.Errorf("hash join: child iterators cannot be nil")
	}

	// Defensive check: verify key orientation between left and right iterators.
	// If right relation contains leftKey but does NOT contain rightKey, swap them.
	if rMap := h.right.ColIndexMap(); rMap != nil {
		rightCol := h.rightKey
		if idx := strings.Index(rightCol, "."); idx != -1 {
			rightCol = rightCol[idx+1:]
		}
		leftCol := h.leftKey
		if idx := strings.Index(leftCol, "."); idx != -1 {
			leftCol = leftCol[idx+1:]
		}

		_, hasRight := rMap.Index(h.rightKey)
		if !hasRight {
			_, hasRight = rMap.Index(rightCol)
		}
		_, hasLeft := rMap.Index(h.leftKey)
		if !hasLeft {
			_, hasLeft = rMap.Index(leftCol)
		}

		if hasLeft && !hasRight {
			h.leftKey, h.rightKey = h.rightKey, h.leftKey
		}
	}

	// 1. Build phase: drain right child into hash table
	if err := h.right.Open(ctx); err != nil {
		return fmt.Errorf("hash join build open error: %w", err)
	}

	isFull := h.joinType == "FULL" || strings.HasPrefix(h.joinType, "FULL")
	isRight := h.joinType == "RIGHT" || strings.HasPrefix(h.joinType, "RIGHT")
	trackRight := isFull || isRight

	h.hashTable = make(map[string][]hashMatch)
	h.allRightTuples = nil
	h.matchedRight = nil
	h.unmatchedIdx = 0
	h.buildMemory = 0
	h.inChunk.Reset()
	h.inIdx = 0
	h.outChunk.Reset()
	h.outIdx = 0

	inChunk := GetChunk()
	defer PutChunk(inChunk)
	for {
		select {
		case <-ctx.Done():
			_ = h.right.Close()
			return ctx.Err()
		default:
		}

		err := PullChunk(ctx, h.right, inChunk, DefaultChunkSize)
		if err != nil && err != io.EOF {
			_ = h.right.Close()
			return fmt.Errorf("hash join build read error: %w", err)
		}
		if inChunk.Len() == 0 && err == io.EOF {
			break
		}

		for _, tuple := range inChunk.Tuples {
			tSize := EstimateTupleSize(tuple)
			if h.workMem > 0 && h.buildMemory+tSize > h.workMem {
				_ = h.right.Close()
				return fmt.Errorf("hash join memory budget exceeded: %s > work_mem (%s)",
					FormatMemSize(h.buildMemory+tSize), FormatMemSize(h.workMem))
			}
			h.buildMemory += tSize

			rIdx := -1
			if trackRight {
				rIdx = len(h.allRightTuples)
				h.allRightTuples = append(h.allRightTuples, tuple)
				h.matchedRight = append(h.matchedRight, false)
			}

			keyVal, found := lookupVal(tuple, h.rightKey)
			if !found || keyVal == nil {
				continue
			}
			var kStr string
			if sVal, ok := keyVal.(string); ok {
				if h.arena != nil {
					kStr = h.arena.AllocString(sVal)
				} else {
					kStr = sVal
				}
			} else {
				formatted := fmt.Sprintf("%v", keyVal)
				if h.arena != nil {
					kStr = h.arena.AllocString(formatted)
				} else {
					kStr = formatted
				}
			}
			h.hashTable[kStr] = append(h.hashTable[kStr], hashMatch{tuple: tuple, idx: rIdx})
		}

		if err == io.EOF {
			break
		}
	}
	_ = h.right.Close()

	// 2. Probe phase: open left child
	if err := h.left.Open(ctx); err != nil {
		return fmt.Errorf("hash join probe open error: %w", err)
	}

	if h.joinType == "SEMI" || h.joinType == "ANTI" {
		h.colIndexMap = h.left.ColIndexMap()
	} else {
		h.colIndexMap = MergeColumnIndexMaps(h.left.ColIndexMap(), h.right.ColIndexMap())
	}
	h.currentProbe = Tuple{}
	h.currentMatches = nil
	h.matchIdx = 0
	h.emittedAny = false
	return nil
}

func (h *HashJoinIterator) pullLeft(ctx context.Context) (Tuple, error) {
	if h.inIdx < len(h.inChunk.Tuples) {
		t := h.inChunk.Tuples[h.inIdx]
		h.inIdx++
		return t, nil
	}
	h.inChunk.Reset()
	h.inIdx = 0
	err := PullChunk(ctx, h.left, &h.inChunk, DefaultChunkSize)
	if err != nil && err != io.EOF {
		return Tuple{}, err
	}
	if len(h.inChunk.Tuples) == 0 && err == io.EOF {
		return Tuple{}, io.EOF
	}
	t := h.inChunk.Tuples[0]
	h.inIdx = 1
	return t, nil
}

func (h *HashJoinIterator) nextTuple(ctx context.Context) (Tuple, error) {
	isFull := h.joinType == "FULL" || strings.HasPrefix(h.joinType, "FULL")
	isRight := h.joinType == "RIGHT" || strings.HasPrefix(h.joinType, "RIGHT")
	trackRight := isFull || isRight

	for {
		select {
		case <-ctx.Done():
			return Tuple{}, ctx.Err()
		default:
		}

		// If current probe tuple has pending matches, emit next match (only for standard joins)
		if h.joinType != "SEMI" && h.joinType != "ANTI" {
			if !h.currentProbe.IsNil() && h.matchIdx < len(h.currentMatches) {
				m := h.currentMatches[h.matchIdx]
				h.matchIdx++
				h.emittedAny = true
				if trackRight && m.idx >= 0 && m.idx < len(h.matchedRight) {
					h.matchedRight[m.idx] = true
				}
				return mergeTuplesWithArena(h.currentProbe, m.tuple, h.colIndexMap, h.arena), nil
			}
		}

		// Pull next probe tuple from outer relation
		probeTuple, err := h.pullLeft(ctx)
		if err != nil {
			if err == io.EOF && trackRight {
				for h.unmatchedIdx < len(h.allRightTuples) {
					idx := h.unmatchedIdx
					h.unmatchedIdx++
					if !h.matchedRight[idx] {
						rTuple := h.allRightTuples[idx]
						leftColsCount := 0
						if h.left.ColIndexMap() != nil {
							leftColsCount = len(h.left.ColIndexMap().Columns())
						} else {
							leftColsCount = len(h.left.Schema())
						}
						return mergeNullsWithTupleAndArena(leftColsCount, rTuple, h.colIndexMap, h.arena), nil
					}
				}
			}
			return Tuple{}, err // propagates io.EOF or context error
		}

		h.currentProbe = probeTuple
		h.currentMatches = nil
		h.matchIdx = 0
		h.emittedAny = false

		keyVal, found := lookupVal(probeTuple, h.leftKey)
		hasMatch := false
		if found && keyVal != nil {
			var kStr string
			if sVal, ok := keyVal.(string); ok {
				kStr = sVal
			} else {
				kStr = fmt.Sprintf("%v", keyVal)
			}
			if matches, exists := h.hashTable[kStr]; exists && len(matches) > 0 {
				hasMatch = true
				h.currentMatches = matches
			}
		}

		if h.joinType == "SEMI" {
			if hasMatch {
				h.currentProbe = Tuple{}
				return probeTuple, nil
			}
			continue
		}

		if h.joinType == "ANTI" {
			if !hasMatch {
				h.currentProbe = Tuple{}
				return probeTuple, nil
			}
			continue
		}

		if hasMatch {
			m := h.currentMatches[h.matchIdx]
			h.matchIdx++
			h.emittedAny = true
			if trackRight && m.idx >= 0 && m.idx < len(h.matchedRight) {
				h.matchedRight[m.idx] = true
			}
			return mergeTuplesWithArena(h.currentProbe, m.tuple, h.colIndexMap, h.arena), nil
		}

		// If LEFT JOIN or FULL JOIN and no matches, emit probe tuple with NULLs
		isLeftOrFull := h.joinType == "LEFT" || strings.HasPrefix(h.joinType, "LEFT") || isFull
		if isLeftOrFull && !h.emittedAny {
			h.currentProbe = Tuple{}
			rightColsCount := 0
			if h.right.ColIndexMap() != nil {
				rightColsCount = len(h.right.ColIndexMap().Columns())
			} else {
				rightColsCount = len(h.right.Schema())
			}
			return mergeTuplesWithNullsAndArena(probeTuple, rightColsCount, h.colIndexMap, h.arena), nil
		}
	}
}

func (h *HashJoinIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	for dest.Len() < DefaultChunkSize {
		t, err := h.nextTuple(ctx)
		if err != nil {
			if dest.Len() > 0 && err == io.EOF {
				return nil
			}
			return err
		}
		dest.Tuples = append(dest.Tuples, t)
	}
	return nil
}

func (h *HashJoinIterator) Next(ctx context.Context) (Tuple, error) {
	if h.outIdx < len(h.outChunk.Tuples) {
		t := h.outChunk.Tuples[h.outIdx]
		h.outIdx++
		return t, nil
	}
	h.outChunk.Reset()
	h.outIdx = 0
	if err := h.NextChunk(ctx, &h.outChunk); err != nil {
		return Tuple{}, err
	}
	if len(h.outChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := h.outChunk.Tuples[0]
	h.outIdx = 1
	return t, nil
}

func (h *HashJoinIterator) Close() error {
	h.hashTable = nil
	h.allRightTuples = nil
	h.matchedRight = nil
	h.currentProbe = Tuple{}
	h.currentMatches = nil
	h.inChunk.Reset()
	h.inIdx = 0
	h.outChunk.Reset()
	h.outIdx = 0
	if h.left != nil {
		return h.left.Close()
	}
	return nil
}

func (h *HashJoinIterator) Schema() []string {
	if h.joinType == "SEMI" || h.joinType == "ANTI" {
		if h.left != nil {
			return h.left.Schema()
		}
		return nil
	}
	if h.colIndexMap != nil {
		return h.colIndexMap.Columns()
	}
	var s []string
	if h.left != nil {
		s = append(s, h.left.Schema()...)
	}
	if h.right != nil {
		s = append(s, h.right.Schema()...)
	}
	return s
}

func (h *HashJoinIterator) ColIndexMap() *ColumnIndexMap {
	if h.joinType == "SEMI" || h.joinType == "ANTI" {
		if h.left != nil {
			return h.left.ColIndexMap()
		}
		return nil
	}
	if h.colIndexMap == nil && h.left != nil && h.right != nil {
		h.colIndexMap = MergeColumnIndexMaps(h.left.ColIndexMap(), h.right.ColIndexMap())
	}
	return h.colIndexMap
}

// IndexNestedLoopJoinIterator executes a nested loop join by using an index on the inner table.
type IndexNestedLoopJoinIterator struct {
	outer            Iterator
	tx               storage.Transaction
	innerTable       string
	innerAlias       string
	innerIndex       *storage.IndexSchema
	innerSchema      *storage.TableSchema
	leftKey          string
	joinType         string
	filterCond       string
	currentOuter     Tuple
	innerCursor      storage.Cursor
	hasMatch         bool
	colIndexMap      *ColumnIndexMap
	innerColIndexMap *ColumnIndexMap
	inChunk          Chunk
	inIdx            int
	outChunk         Chunk
	outIdx           int
	innerPlan        *planner.PlanNode
}

// NewIndexNestedLoopJoinIterator creates a new Index Nested Loop Join operator.
func NewIndexNestedLoopJoinIterator(outer Iterator, tx storage.Transaction, innerTable, innerAlias string, innerIndex *storage.IndexSchema, innerSchema *storage.TableSchema, leftKey, joinType string) *IndexNestedLoopJoinIterator {
	jType := strings.ToUpper(strings.TrimSpace(joinType))
	if jType == "" {
		jType = "INNER"
	}
	if innerAlias == "" {
		innerAlias = innerTable
	}
	return &IndexNestedLoopJoinIterator{
		outer:       outer,
		tx:          tx,
		innerTable:  innerTable,
		innerAlias:  innerAlias,
		innerIndex:  innerIndex,
		innerSchema: innerSchema,
		leftKey:     leftKey,
		joinType:    jType,
	}
}

// SetFilter sets an optional filter condition evaluated on inner tuples.
func (n *IndexNestedLoopJoinIterator) SetFilter(cond string) {
	n.filterCond = strings.TrimSpace(cond)
}

// SetInnerPlan sets an optional inner PlanNode for profiling inner loop execution.
func (n *IndexNestedLoopJoinIterator) SetInnerPlan(plan *planner.PlanNode) {
	n.innerPlan = plan
}

func (n *IndexNestedLoopJoinIterator) Open(ctx context.Context) error {
	if n.outer == nil {
		return fmt.Errorf("index nested loop join: outer iterator is nil")
	}
	if err := n.outer.Open(ctx); err != nil {
		return err
	}
	n.innerColIndexMap = NewColumnIndexMap(n.innerSchema, n.innerAlias)
	var outerMap *ColumnIndexMap
	if n.outer != nil {
		outerMap = n.outer.ColIndexMap()
	}
	if n.joinType == "SEMI" || n.joinType == "ANTI" {
		n.colIndexMap = outerMap
	} else {
		n.colIndexMap = MergeColumnIndexMaps(outerMap, n.innerColIndexMap)
	}
	n.currentOuter = Tuple{}
	n.innerCursor = nil
	n.hasMatch = false
	n.inChunk.Reset()
	n.inIdx = 0
	n.outChunk.Reset()
	n.outIdx = 0
	return nil
}

func (n *IndexNestedLoopJoinIterator) pullOuter(ctx context.Context) (Tuple, error) {
	if n.inIdx < len(n.inChunk.Tuples) {
		t := n.inChunk.Tuples[n.inIdx]
		n.inIdx++
		return t, nil
	}
	n.inChunk.Reset()
	n.inIdx = 0
	err := PullChunk(ctx, n.outer, &n.inChunk, DefaultChunkSize)
	if err != nil && err != io.EOF {
		return Tuple{}, err
	}
	if len(n.inChunk.Tuples) == 0 && err == io.EOF {
		return Tuple{}, io.EOF
	}
	t := n.inChunk.Tuples[0]
	n.inIdx = 1
	return t, nil
}

func (n *IndexNestedLoopJoinIterator) nextTuple(ctx context.Context) (Tuple, error) {
	for {
		select {
		case <-ctx.Done():
			return Tuple{}, ctx.Err()
		default:
		}

		if n.innerCursor != nil {
			for n.innerCursor.Next() {
				rec, err := n.innerCursor.Record()
				if err != nil {
					break
				}
				innerTuple := FormatRecord(rec, n.innerSchema, n.innerAlias, n.innerColIndexMap)
				if n.filterCond != "" && !EvalConditionTuple(innerTuple, n.filterCond) {
					continue
				}

				n.hasMatch = true
				if n.innerPlan != nil {
					n.innerPlan.ActualRows++
				}
				if n.joinType == "SEMI" {
					_ = n.innerCursor.Close()
					n.innerCursor = nil
					out := n.currentOuter
					n.currentOuter = Tuple{}
					return out, nil
				}
				if n.joinType == "ANTI" {
					_ = n.innerCursor.Close()
					n.innerCursor = nil
					n.currentOuter = Tuple{}
					break
				}
				return mergeTuples(n.currentOuter, innerTuple, n.colIndexMap), nil
			}

			if n.innerCursor != nil {
				_ = n.innerCursor.Close()
				n.innerCursor = nil
			}

			if n.joinType == "ANTI" {
				if !n.hasMatch && !n.currentOuter.IsNil() {
					out := n.currentOuter
					n.currentOuter = Tuple{}
					return out, nil
				}
				n.currentOuter = Tuple{}
				continue
			}

			// If LEFT JOIN and had no match for this outer tuple
			if (n.joinType == "LEFT" || strings.HasPrefix(n.joinType, "LEFT")) && !n.hasMatch && !n.currentOuter.IsNil() {
				out := mergeTuplesWithNulls(n.currentOuter, len(n.innerSchemaColumns()), n.colIndexMap)
				n.currentOuter = Tuple{}
				return out, nil
			}
			n.currentOuter = Tuple{}
		}

		// Pull next outer tuple
		outerTuple, err := n.pullOuter(ctx)
		if err != nil {
			return Tuple{}, err // io.EOF or context cancellation
		}

		n.currentOuter = outerTuple
		n.hasMatch = false

		keyVal, found := lookupVal(outerTuple, n.leftKey)
		if !found || keyVal == nil {
			if n.joinType == "ANTI" {
				n.currentOuter = Tuple{}
				return outerTuple, nil
			}
			if n.joinType == "LEFT" || strings.HasPrefix(n.joinType, "LEFT") {
				n.currentOuter = Tuple{}
				return mergeTuplesWithNulls(outerTuple, len(n.innerSchemaColumns()), n.colIndexMap), nil
			}
			continue
		}

		// Construct index lookup Value
		stVal := toStorageValFromAny(keyVal)
		scanStart := time.Now()
		cur, err := n.tx.IndexScan(n.innerTable, n.innerIndex.Name, &stVal, &stVal)
		if err != nil {
			return Tuple{}, err
		}
		if n.innerPlan != nil {
			n.innerPlan.ActualLoops++
			n.innerPlan.ActualDurationUS += time.Since(scanStart).Microseconds()
		}
		n.innerCursor = cur
	}
}

func (n *IndexNestedLoopJoinIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	for dest.Len() < DefaultChunkSize {
		t, err := n.nextTuple(ctx)
		if err != nil {
			if dest.Len() > 0 && err == io.EOF {
				return nil
			}
			return err
		}
		dest.Tuples = append(dest.Tuples, t)
	}
	return nil
}

func (n *IndexNestedLoopJoinIterator) Next(ctx context.Context) (Tuple, error) {
	if n.outIdx < len(n.outChunk.Tuples) {
		t := n.outChunk.Tuples[n.outIdx]
		n.outIdx++
		return t, nil
	}
	n.outChunk.Reset()
	n.outIdx = 0
	if err := n.NextChunk(ctx, &n.outChunk); err != nil {
		return Tuple{}, err
	}
	if len(n.outChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := n.outChunk.Tuples[0]
	n.outIdx = 1
	return t, nil
}

func (n *IndexNestedLoopJoinIterator) Close() error {
	n.inChunk.Reset()
	n.inIdx = 0
	n.outChunk.Reset()
	n.outIdx = 0
	if n.innerCursor != nil {
		_ = n.innerCursor.Close()
		n.innerCursor = nil
	}
	if n.outer != nil {
		return n.outer.Close()
	}
	return nil
}

func (n *IndexNestedLoopJoinIterator) Schema() []string {
	if n.joinType == "SEMI" || n.joinType == "ANTI" {
		if n.outer != nil {
			return n.outer.Schema()
		}
		return nil
	}
	if n.colIndexMap != nil {
		return n.colIndexMap.Columns()
	}
	var s []string
	if n.outer != nil {
		s = append(s, n.outer.Schema()...)
	}
	s = append(s, n.innerSchemaColumns()...)
	return s
}

func (n *IndexNestedLoopJoinIterator) ColIndexMap() *ColumnIndexMap {
	if n.joinType == "SEMI" || n.joinType == "ANTI" {
		if n.outer != nil {
			return n.outer.ColIndexMap()
		}
		return nil
	}
	if n.colIndexMap == nil {
		n.innerColIndexMap = NewColumnIndexMap(n.innerSchema, n.innerAlias)
		var outerMap *ColumnIndexMap
		if n.outer != nil {
			outerMap = n.outer.ColIndexMap()
		}
		n.colIndexMap = MergeColumnIndexMaps(outerMap, n.innerColIndexMap)
	}
	return n.colIndexMap
}

func (n *IndexNestedLoopJoinIterator) innerSchemaColumns() []string {
	if n.innerSchema == nil {
		return nil
	}
	cols := make([]string, len(n.innerSchema.Columns))
	for i, c := range n.innerSchema.Columns {
		cols[i] = c.Name
	}
	return cols
}

func mergeTuples(left, right Tuple, combinedIndex *ColumnIndexMap) Tuple {
	return mergeTuplesWithArena(left, right, combinedIndex, nil)
}

func mergeTuplesWithArena(left, right Tuple, combinedIndex *ColumnIndexMap, arena *storage.Arena) Tuple {
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(len(left.Values) + len(right.Values))
	} else {
		vals = make([]any, len(left.Values)+len(right.Values))
	}
	copy(vals, left.Values)
	copy(vals[len(left.Values):], right.Values)
	return Tuple{
		Values: vals,
		Index:  combinedIndex,
	}
}

func mergeTuplesWithNulls(left Tuple, rightColsCount int, combinedIndex *ColumnIndexMap) Tuple {
	return mergeTuplesWithNullsAndArena(left, rightColsCount, combinedIndex, nil)
}

func mergeTuplesWithNullsAndArena(left Tuple, rightColsCount int, combinedIndex *ColumnIndexMap, arena *storage.Arena) Tuple {
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(len(left.Values) + rightColsCount)
	} else {
		vals = make([]any, len(left.Values)+rightColsCount)
	}
	copy(vals, left.Values)
	return Tuple{
		Values: vals,
		Index:  combinedIndex,
	}
}

func mergeNullsWithTuple(leftColsCount int, right Tuple, combinedIndex *ColumnIndexMap) Tuple {
	return mergeNullsWithTupleAndArena(leftColsCount, right, combinedIndex, nil)
}

func mergeNullsWithTupleAndArena(leftColsCount int, right Tuple, combinedIndex *ColumnIndexMap, arena *storage.Arena) Tuple {
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(leftColsCount + len(right.Values))
	} else {
		vals = make([]any, leftColsCount+len(right.Values))
	}
	copy(vals[leftColsCount:], right.Values)
	return Tuple{
		Values: vals,
		Index:  combinedIndex,
	}
}

func toStorageValFromAny(v any) storage.Value {
	if v == nil {
		return storage.NullValue(storage.TypeUnknown)
	}
	switch val := v.(type) {
	case int64:
		return storage.IntValue(val)
	case int:
		return storage.IntValue(int64(val))
	case float64:
		return storage.FloatValue(val)
	case storage.Decimal:
		return storage.DecimalValueFromDecimal(val)
	case string:
		return storage.StringValue(val)
	case bool:
		return storage.BoolValue(val)
	default:
		return storage.StringValue(fmt.Sprintf("%v", val))
	}
}
