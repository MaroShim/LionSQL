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
	}
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

	h.currentProbe = nil
	h.currentMatches = nil
	h.matchIdx = 0
	h.emittedAny = false
	return nil
}

func (h *HashJoinIterator) Next(ctx context.Context) (Tuple, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// If current probe tuple has pending matches, emit next match
		if h.currentProbe != nil && h.matchIdx < len(h.currentMatches) {
			matchTuple := h.currentMatches[h.matchIdx]
			h.matchIdx++
			h.emittedAny = true
			return mergeTuples(h.currentProbe, matchTuple), nil
		}

		// Pull next probe tuple from outer relation
		probeTuple, err := h.left.Next(ctx)
		if err != nil {
			return nil, err // propagates io.EOF or context error
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
				return mergeTuples(h.currentProbe, matchTuple), nil
			}
		}

		// If LEFT JOIN and no matches, emit probe tuple with NULLs
		if (h.joinType == "LEFT" || strings.HasPrefix(h.joinType, "LEFT")) && !h.emittedAny {
			h.currentProbe = nil
			return mergeTuplesWithNulls(probeTuple, h.right.Schema()), nil
		}
	}
}

func (h *HashJoinIterator) Close() error {
	h.hashTable = nil
	h.currentProbe = nil
	h.currentMatches = nil
	if h.left != nil {
		return h.left.Close()
	}
	return nil
}

func (h *HashJoinIterator) Schema() []string {
	var s []string
	if h.left != nil {
		s = append(s, h.left.Schema()...)
	}
	if h.right != nil {
		s = append(s, h.right.Schema()...)
	}
	return s
}

// IndexNestedLoopJoinIterator executes a nested loop join by using an index on the inner table.
type IndexNestedLoopJoinIterator struct {
	outer        Iterator
	tx           storage.Transaction
	innerTable   string
	innerAlias   string
	innerIndex   *storage.IndexSchema
	innerSchema  *storage.TableSchema
	leftKey      string
	joinType     string
	currentOuter Tuple
	innerCursor  storage.Cursor
	hasMatch     bool
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
	n.currentOuter = nil
	n.innerCursor = nil
	n.hasMatch = false
	return nil
}

func (n *IndexNestedLoopJoinIterator) Next(ctx context.Context) (Tuple, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if n.innerCursor != nil {
			if n.innerCursor.Next() {
				rec, err := n.innerCursor.Record()
				if err == nil {
					n.hasMatch = true
					innerTuple := FormatRecord(rec, n.innerSchema, n.innerAlias)
					return mergeTuples(n.currentOuter, innerTuple), nil
				}
			}
			// Inner cursor exhausted
			_ = n.innerCursor.Close()
			n.innerCursor = nil

			// If LEFT JOIN and had no match for this outer tuple
			if (n.joinType == "LEFT" || strings.HasPrefix(n.joinType, "LEFT")) && !n.hasMatch && n.currentOuter != nil {
				out := mergeTuplesWithNulls(n.currentOuter, n.innerSchemaColumns())
				n.currentOuter = nil
				return out, nil
			}
			n.currentOuter = nil
		}

		// Pull next outer tuple
		outerTuple, err := n.outer.Next(ctx)
		if err != nil {
			return nil, err // io.EOF or context cancellation
		}

		n.currentOuter = outerTuple
		n.hasMatch = false

		keyVal, found := lookupVal(outerTuple, n.leftKey)
		if !found || keyVal == nil {
			if n.joinType == "LEFT" || strings.HasPrefix(n.joinType, "LEFT") {
				n.currentOuter = nil
				return mergeTuplesWithNulls(outerTuple, n.innerSchemaColumns()), nil
			}
			continue
		}

		// Construct index lookup Value
		stVal := toStorageValFromAny(keyVal)
		cur, err := n.tx.IndexScan(n.innerTable, n.innerIndex.Name, &stVal, &stVal)
		if err != nil {
			return nil, err
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
	var s []string
	if n.outer != nil {
		s = append(s, n.outer.Schema()...)
	}
	s = append(s, n.innerSchemaColumns()...)
	return s
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

func mergeTuples(left, right Tuple) Tuple {
	res := make(Tuple, len(left)+len(right))
	for k, v := range left {
		res[k] = v
	}
	for k, v := range right {
		res[k] = v
	}
	return res
}

func mergeTuplesWithNulls(left Tuple, rightCols []string) Tuple {
	res := make(Tuple, len(left)+len(rightCols))
	for k, v := range left {
		res[k] = v
	}
	for _, c := range rightCols {
		res[c] = nil
	}
	return res
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
