package execution

import (
	"context"
	"fmt"
	"io"
	"strings"

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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		tuple, err := h.right.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = h.right.Close()
			return fmt.Errorf("hash join build read error: %w", err)
		}

		h.buildMemory += EstimateTupleSize(tuple)

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
		kStr := fmt.Sprintf("%v", keyVal)
		h.hashTable[kStr] = append(h.hashTable[kStr], hashMatch{tuple: tuple, idx: rIdx})
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

func (h *HashJoinIterator) Next(ctx context.Context) (Tuple, error) {
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
				return mergeTuples(h.currentProbe, m.tuple, h.colIndexMap), nil
			}
		}

		// Pull next probe tuple from outer relation
		probeTuple, err := h.left.Next(ctx)
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
						return mergeNullsWithTuple(leftColsCount, rTuple, h.colIndexMap), nil
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
			kStr := fmt.Sprintf("%v", keyVal)
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
			return mergeTuples(h.currentProbe, m.tuple, h.colIndexMap), nil
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
			return mergeTuplesWithNulls(probeTuple, rightColsCount, h.colIndexMap), nil
		}
	}
}

func (h *HashJoinIterator) Close() error {
	h.hashTable = nil
	h.allRightTuples = nil
	h.matchedRight = nil
	h.currentProbe = Tuple{}
	h.currentMatches = nil
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
	return nil
}

func (n *IndexNestedLoopJoinIterator) Next(ctx context.Context) (Tuple, error) {
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
		outerTuple, err := n.outer.Next(ctx)
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
		cur, err := n.tx.IndexScan(n.innerTable, n.innerIndex.Name, &stVal, &stVal)
		if err != nil {
			return Tuple{}, err
		}
		n.innerCursor = cur
	}
}

func (n *IndexNestedLoopJoinIterator) Close() error {
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
	vals := make([]any, len(left.Values)+len(right.Values))
	copy(vals, left.Values)
	copy(vals[len(left.Values):], right.Values)
	return Tuple{
		Values: vals,
		Index:  combinedIndex,
	}
}

func mergeTuplesWithNulls(left Tuple, rightColsCount int, combinedIndex *ColumnIndexMap) Tuple {
	vals := make([]any, len(left.Values)+rightColsCount)
	copy(vals, left.Values)
	return Tuple{
		Values: vals,
		Index:  combinedIndex,
	}
}

func mergeNullsWithTuple(leftColsCount int, right Tuple, combinedIndex *ColumnIndexMap) Tuple {
	vals := make([]any, leftColsCount+len(right.Values))
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
