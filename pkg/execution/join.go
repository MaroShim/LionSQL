package execution

import (
	"context"
	"fmt"
	"io"
	"strings"

	"sqlion/pkg/storage"
)

// HashJoinIterator implements in-memory Hash Join between two relations.
// The right child (inner) is buffered into a hash map during Open(), and
// the left child (outer) is probed tuple-by-tuple during Next().
type HashJoinIterator struct {
	left           Iterator // Outer / Probe
	right          Iterator // Inner / Build
	joinType       string   // "INNER" or "LEFT"
	leftKey        string
	rightKey       string
	hashTable      map[string][]Tuple
	currentProbe   Tuple
	currentMatches []Tuple
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
		hashTable: make(map[string][]Tuple),
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

	// 1. Build phase: drain right child into hash table
	if err := h.right.Open(ctx); err != nil {
		return fmt.Errorf("hash join build open error: %w", err)
	}

	h.hashTable = make(map[string][]Tuple)
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

		keyVal, found := lookupVal(tuple, h.rightKey)
		if !found || keyVal == nil {
			continue
		}
		kStr := fmt.Sprintf("%v", keyVal)
		h.hashTable[kStr] = append(h.hashTable[kStr], tuple)
	}
	_ = h.right.Close()

	// 2. Probe phase: open left child
	if err := h.left.Open(ctx); err != nil {
		return fmt.Errorf("hash join probe open error: %w", err)
	}

	h.colIndexMap = MergeColumnIndexMaps(h.left.ColIndexMap(), h.right.ColIndexMap())
	h.currentProbe = Tuple{}
	h.currentMatches = nil
	h.matchIdx = 0
	h.emittedAny = false
	return nil
}

func (h *HashJoinIterator) Next(ctx context.Context) (Tuple, error) {
	for {
		select {
		case <-ctx.Done():
			return Tuple{}, ctx.Err()
		default:
		}

		// If current probe tuple has pending matches, emit next match
		if !h.currentProbe.IsNil() && h.matchIdx < len(h.currentMatches) {
			matchTuple := h.currentMatches[h.matchIdx]
			h.matchIdx++
			h.emittedAny = true
			return mergeTuples(h.currentProbe, matchTuple, h.colIndexMap), nil
		}

		// Pull next probe tuple from outer relation
		probeTuple, err := h.left.Next(ctx)
		if err != nil {
			return Tuple{}, err // propagates io.EOF or context error
		}

		h.currentProbe = probeTuple
		h.currentMatches = nil
		h.matchIdx = 0
		h.emittedAny = false

		keyVal, found := lookupVal(probeTuple, h.leftKey)
		if found && keyVal != nil {
			kStr := fmt.Sprintf("%v", keyVal)
			if matches, exists := h.hashTable[kStr]; exists && len(matches) > 0 {
				h.currentMatches = matches
				matchTuple := h.currentMatches[h.matchIdx]
				h.matchIdx++
				h.emittedAny = true
				return mergeTuples(h.currentProbe, matchTuple, h.colIndexMap), nil
			}
		}

		// If LEFT JOIN and no matches, emit probe tuple with NULLs
		if (h.joinType == "LEFT" || strings.HasPrefix(h.joinType, "LEFT")) && !h.emittedAny {
			h.currentProbe = Tuple{}
			return mergeTuplesWithNulls(probeTuple, len(h.right.Schema()), h.colIndexMap), nil
		}
	}
}

func (h *HashJoinIterator) Close() error {
	h.hashTable = nil
	h.currentProbe = Tuple{}
	h.currentMatches = nil
	if h.left != nil {
		return h.left.Close()
	}
	return nil
}

func (h *HashJoinIterator) Schema() []string {
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
	if h.colIndexMap == nil && h.left != nil && h.right != nil {
		h.colIndexMap = MergeColumnIndexMaps(h.left.ColIndexMap(), h.right.ColIndexMap())
	}
	return h.colIndexMap
}

// IndexNestedLoopJoinIterator executes a nested loop join by using an index on the inner table.
type IndexNestedLoopJoinIterator struct {
	outer           Iterator
	tx              storage.Transaction
	innerTable      string
	innerAlias      string
	innerIndex      *storage.IndexSchema
	innerSchema     *storage.TableSchema
	leftKey         string
	joinType        string
	currentOuter    Tuple
	innerCursor     storage.Cursor
	hasMatch        bool
	colIndexMap     *ColumnIndexMap
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
	n.colIndexMap = MergeColumnIndexMaps(outerMap, n.innerColIndexMap)
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
			if n.innerCursor.Next() {
				rec, err := n.innerCursor.Record()
				if err == nil {
					n.hasMatch = true
					innerTuple := FormatRecord(rec, n.innerSchema, n.innerAlias, n.innerColIndexMap)
					return mergeTuples(n.currentOuter, innerTuple, n.colIndexMap), nil
				}
			}
			// Inner cursor exhausted
			_ = n.innerCursor.Close()
			n.innerCursor = nil

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
	case string:
		return storage.StringValue(val)
	case bool:
		return storage.BoolValue(val)
	default:
		return storage.StringValue(fmt.Sprintf("%v", val))
	}
}
