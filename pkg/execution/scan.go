package execution

import (
	"context"
	"fmt"
	"io"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// SeqScanIterator scans a table sequentially row-by-row or in vectorized chunks directly from a storage.Cursor.
type SeqScanIterator struct {
	tx           storage.Transaction
	tableName    string
	alias        string
	schema       *storage.TableSchema
	colIndexMap  *ColumnIndexMap
	cursor       storage.Cursor
	partitions   []string
	bufChunk     Chunk
	bufIdx       int
	eof          bool
	arena        *storage.Arena
	requiredCols []int
	filterCond   string
	filterExpr   proc.Expr
}

// SetArena configures an optional memory arena for zero-GC tuple allocation.
func (s *SeqScanIterator) SetArena(a *storage.Arena) {
	s.arena = a
}

// SetRequiredCols configures selective column decoding for projection pushdown.
func (s *SeqScanIterator) SetRequiredCols(cols []int) {
	s.requiredCols = cols
}

// SetFilterCond configures a pushdown filter predicate for early row rejection in storage scan.
func (s *SeqScanIterator) SetFilterCond(cond string) {
	s.filterCond = strings.TrimSpace(cond)
	if s.filterCond != "" {
		p := proc.NewParser(proc.NewLexer(s.filterCond))
		if expr := p.ParseExpr(); expr != nil && len(p.Errors()) == 0 {
			s.filterExpr = proc.FoldConstants(expr)
		}
	}
}

// NewSeqScanIterator creates a new sequential scan iterator.
func NewSeqScanIterator(tx storage.Transaction, tableName, alias string, schema *storage.TableSchema) *SeqScanIterator {
	return NewSeqScanIteratorWithPartitions(tx, tableName, alias, schema, nil)
}

// NewSeqScanIteratorWithPartitions creates a sequential scan iterator restricted to specific partitions.
func NewSeqScanIteratorWithPartitions(tx storage.Transaction, tableName, alias string, schema *storage.TableSchema, partitions []string) *SeqScanIterator {
	if alias == "" {
		alias = tableName
	}
	return &SeqScanIterator{
		tx:          tx,
		tableName:   tableName,
		alias:       alias,
		schema:      schema,
		colIndexMap: NewColumnIndexMap(schema, alias),
		partitions:  partitions,
	}
}

func (s *SeqScanIterator) Open(ctx context.Context) error {
	if s.tx == nil {
		return fmt.Errorf("seq scan: transaction is nil")
	}
	s.colIndexMap = NewColumnIndexMap(s.schema, s.alias)
	s.bufChunk.Reset()
	s.bufIdx = 0
	s.eof = false
	var cur storage.Cursor
	var err error
	if len(s.partitions) > 0 {
		cur, err = s.tx.ScanPartitions(s.tableName, s.partitions)
	} else {
		cur, err = s.tx.Scan(s.tableName)
	}
	if err != nil {
		return fmt.Errorf("seq scan on %q: %w", s.tableName, err)
	}
	s.cursor = cur
	return nil
}

func fillScanChunk(ctx context.Context, cursor storage.Cursor, schema *storage.TableSchema, alias string, colMap *ColumnIndexMap, arena *storage.Arena, requiredCols []int, filterExpr proc.Expr, eof *bool, dest *Chunk) error {
	if cursor == nil || *eof {
		return io.EOF
	}
	dest.Reset()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	rawCur, hasRaw := cursor.(storage.RawTupleCursor)
	arenaCur, hasArena := cursor.(storage.ArenaRowCursor)
	hasSelection := len(requiredCols) > 0 && schema != nil && len(requiredCols) < len(schema.Columns)

	bufPtr := storage.GetTupleBuffer()
	defer storage.PutTupleBuffer(bufPtr)

	for dest.Len() < DefaultChunkSize {
		if !cursor.Next() {
			*eof = true
			if err := cursor.Err(); err != nil {
				return err
			}
			if dest.Len() > 0 {
				return nil
			}
			return io.EOF
		}

		if hasRaw {
			if rawCur.RawTupleBuffer(bufPtr) {
				// Scan-Filter Fusion: early predicate evaluation before heap allocations
				if filterExpr != nil {
					rawTuple := Tuple{
						Raw:    *bufPtr,
						Schema: schema,
						Index:  colMap,
					}
					matched, _ := EvalTupleBoolExprWithContextErr(rawTuple, filterExpr, nil, nil)
					if !matched {
						continue // Filter mismatch: 0 heap allocations, drop immediately
					}
				}

				if hasSelection {
					if row, err := storage.DecodeTupleSelectedWithArena(schema, *bufPtr, requiredCols, arena); err == nil {
						dest.Tuples = append(dest.Tuples, FormatRowWithSelectedCols(row, schema, alias, colMap, requiredCols, arena))
						continue
					}
				}
				var tupleBytes []byte
				if arena != nil {
					tupleBytes = arena.AllocBytes(*bufPtr)
				} else {
					tupleBytes = make([]byte, len(*bufPtr))
					copy(tupleBytes, *bufPtr)
				}
				if lazy, err := storage.NewLazyTuple(schema, tupleBytes); err == nil {
					var vals []any
					if arena != nil {
						vals = arena.AllocAnySlice(len(schema.Columns))
					} else {
						vals = make([]any, len(schema.Columns))
					}
					dest.Tuples = append(dest.Tuples, Tuple{
						Values: vals,
						Index:  colMap,
						Lazy:   lazy,
					})
					continue
				}
			} else if rawData, ok := rawCur.RawTuple(); ok {
				if filterExpr != nil {
					rawTuple := Tuple{
						Raw:    rawData,
						Schema: schema,
						Index:  colMap,
					}
					matched, _ := EvalTupleBoolExprWithContextErr(rawTuple, filterExpr, nil, nil)
					if !matched {
						continue
					}
				}
				if hasSelection {
					if row, err := storage.DecodeTupleSelectedWithArena(schema, rawData, requiredCols, arena); err == nil {
						dest.Tuples = append(dest.Tuples, FormatRowWithSelectedCols(row, schema, alias, colMap, requiredCols, arena))
						continue
					}
				}
				if lazy, err := storage.NewLazyTuple(schema, rawData); err == nil {
					var vals []any
					if arena != nil {
						vals = arena.AllocAnySlice(len(schema.Columns))
					} else {
						vals = make([]any, len(schema.Columns))
					}
					dest.Tuples = append(dest.Tuples, Tuple{
						Values: vals,
						Index:  colMap,
						Lazy:   lazy,
					})
					continue
				}
			}
		}

		if hasArena && arena != nil {
			if row, err := arenaCur.RowWithArena(arena); err == nil {
				var t Tuple
				if hasSelection {
					t = FormatRowWithSelectedCols(row, schema, alias, colMap, requiredCols, arena)
				} else {
					t = FormatRowWithArena(row, schema, alias, colMap, arena)
				}
				if filterExpr != nil {
					matched, _ := EvalTupleBoolExprWithContextErr(t, filterExpr, nil, nil)
					if !matched {
						continue
					}
				}
				dest.Tuples = append(dest.Tuples, t)
				continue
			}
		}

		if row, err := cursor.Row(); err == nil {
			var t Tuple
			if hasSelection {
				t = FormatRowWithSelectedCols(row, schema, alias, colMap, requiredCols, arena)
			} else {
				t = FormatRowWithArena(row, schema, alias, colMap, arena)
			}
			if filterExpr != nil {
				matched, _ := EvalTupleBoolExprWithContextErr(t, filterExpr, nil, nil)
				if !matched {
					continue
				}
			}
			dest.Tuples = append(dest.Tuples, t)
			continue
		}

		rec, err := cursor.Record()
		if err != nil {
			if dest.Len() > 0 {
				return nil
			}
			return err
		}
		t := FormatRecord(rec, schema, alias, colMap)
		if filterExpr != nil {
			matched, _ := EvalTupleBoolExprWithContextErr(t, filterExpr, nil, nil)
			if !matched {
				continue
			}
		}
		dest.Tuples = append(dest.Tuples, t)
	}
	return nil
}

// FormatRowWithSelectedCols converts a storage.Row into a Tuple, formatting only the columns in selectedCols.
// Unselected column slots in Tuple.Values remain nil.
func FormatRowWithSelectedCols(row storage.Row, schema *storage.TableSchema, alias string, colMap *ColumnIndexMap, selectedCols []int, arena *storage.Arena) Tuple {
	if schema == nil {
		return Tuple{}
	}
	if colMap == nil {
		colMap = NewColumnIndexMap(schema, alias)
	}

	n := len(schema.Columns)
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(n)
	} else {
		vals = make([]any, n)
	}
	numVals := len(row.Values)
	for _, i := range selectedCols {
		if i < 0 || i >= n || i >= numVals {
			continue
		}
		c := schema.Columns[i]
		stVal := row.Values[i]
		if !stVal.IsNull && stVal.Type == storage.TypeInt && c.Precision > 0 {
			stVal.Precision = c.Precision
			vals[i] = stVal
		} else {
			vals[i] = formatStorageVal(stVal)
		}
	}

	t := Tuple{
		Values: vals,
		Index:  colMap,
	}

	for _, i := range selectedCols {
		if i < 0 || i >= n {
			continue
		}
		c := schema.Columns[i]
		if c.HasComputedExpr() && (i >= numVals || row.Values[i].IsNull || vals[i] == nil) {
			if expr, err := getOrParseComputedExpr(c.ComputedExpr); err == nil && expr != nil {
				if compVal, ok := EvalTupleExpr(t, expr); ok {
					vals[i] = compVal
				}
			}
		}
	}

	return t
}

func (s *SeqScanIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	return fillScanChunk(ctx, s.cursor, s.schema, s.alias, s.colIndexMap, s.arena, s.requiredCols, s.filterExpr, &s.eof, dest)
}

func (s *SeqScanIterator) Next(ctx context.Context) (Tuple, error) {
	if s.bufIdx < len(s.bufChunk.Tuples) {
		t := s.bufChunk.Tuples[s.bufIdx]
		s.bufIdx++
		return t, nil
	}
	s.bufChunk.Reset()
	s.bufIdx = 0
	if err := s.NextChunk(ctx, &s.bufChunk); err != nil {
		return Tuple{}, err
	}
	if len(s.bufChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := s.bufChunk.Tuples[0]
	s.bufIdx = 1
	return t, nil
}

func (s *SeqScanIterator) Close() error {
	if s.cursor != nil {
		err := s.cursor.Close()
		s.cursor = nil
		return err
	}
	return nil
}

func (s *SeqScanIterator) Schema() []string {
	if s.schema == nil {
		return nil
	}
	cols := make([]string, len(s.schema.Columns))
	for i, c := range s.schema.Columns {
		cols[i] = c.Name
	}
	return cols
}

func (s *SeqScanIterator) ColIndexMap() *ColumnIndexMap {
	if s.colIndexMap == nil {
		s.colIndexMap = NewColumnIndexMap(s.schema, s.alias)
	}
	return s.colIndexMap
}

// IndexScanIterator scans a table using a secondary index range in row-by-row or vectorized chunk mode.
type IndexScanIterator struct {
	tx           storage.Transaction
	tableName    string
	alias        string
	indexName    string
	low          *storage.Value
	high         *storage.Value
	schema       *storage.TableSchema
	colIndexMap  *ColumnIndexMap
	cursor       storage.Cursor
	IndexOnly    bool
	Desc         bool
	bufChunk     Chunk
	bufIdx       int
	eof          bool
	arena        *storage.Arena
	requiredCols []int
	filterCond   string
	filterExpr   proc.Expr
}

// SetArena configures an optional memory arena for zero-GC tuple allocation.
func (s *IndexScanIterator) SetArena(a *storage.Arena) {
	s.arena = a
}

// SetRequiredCols configures selective column decoding for projection pushdown.
func (s *IndexScanIterator) SetRequiredCols(cols []int) {
	s.requiredCols = cols
}

// SetFilterCond configures a pushdown filter predicate for early row rejection in storage index scan.
func (s *IndexScanIterator) SetFilterCond(cond string) {
	s.filterCond = strings.TrimSpace(cond)
	if s.filterCond != "" {
		p := proc.NewParser(proc.NewLexer(s.filterCond))
		if expr := p.ParseExpr(); expr != nil && len(p.Errors()) == 0 {
			s.filterExpr = proc.FoldConstants(expr)
		}
	}
}

// NewIndexScanIterator creates a new index scan iterator.
func NewIndexScanIterator(tx storage.Transaction, tableName, alias, indexName string, low, high *storage.Value, schema *storage.TableSchema) *IndexScanIterator {
	if alias == "" {
		alias = tableName
	}
	return &IndexScanIterator{
		tx:          tx,
		tableName:   tableName,
		alias:       alias,
		indexName:   indexName,
		low:         low,
		high:        high,
		schema:      schema,
		colIndexMap: NewColumnIndexMap(schema, alias),
	}
}

func (s *IndexScanIterator) Open(ctx context.Context) error {
	if s.tx == nil {
		return fmt.Errorf("index scan: transaction is nil")
	}
	s.colIndexMap = NewColumnIndexMap(s.schema, s.alias)
	s.bufChunk.Reset()
	s.bufIdx = 0
	s.eof = false
	var cur storage.Cursor
	var err error
	if s.Desc {
		cur, err = s.tx.IndexScanDesc(s.tableName, s.indexName, s.low, s.high)
	} else if s.IndexOnly {
		cur, err = s.tx.IndexOnlyScan(s.tableName, s.indexName, s.low, s.high)
	} else {
		cur, err = s.tx.IndexScan(s.tableName, s.indexName, s.low, s.high)
	}
	if err != nil {
		return fmt.Errorf("index scan %q on %q: %w", s.indexName, s.tableName, err)
	}
	s.cursor = cur
	return nil
}

func (s *IndexScanIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	return fillScanChunk(ctx, s.cursor, s.schema, s.alias, s.colIndexMap, s.arena, s.requiredCols, s.filterExpr, &s.eof, dest)
}

func (s *IndexScanIterator) Next(ctx context.Context) (Tuple, error) {
	if s.bufIdx < len(s.bufChunk.Tuples) {
		t := s.bufChunk.Tuples[s.bufIdx]
		s.bufIdx++
		return t, nil
	}
	s.bufChunk.Reset()
	s.bufIdx = 0
	if err := s.NextChunk(ctx, &s.bufChunk); err != nil {
		return Tuple{}, err
	}
	if len(s.bufChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := s.bufChunk.Tuples[0]
	s.bufIdx = 1
	return t, nil
}

func (s *IndexScanIterator) Close() error {
	if s.cursor != nil {
		err := s.cursor.Close()
		s.cursor = nil
		return err
	}
	return nil
}

func (s *IndexScanIterator) Schema() []string {
	if s.schema == nil {
		return nil
	}
	cols := make([]string, len(s.schema.Columns))
	for i, c := range s.schema.Columns {
		cols[i] = c.Name
	}
	return cols
}

func (s *IndexScanIterator) ColIndexMap() *ColumnIndexMap {
	if s.colIndexMap == nil {
		s.colIndexMap = NewColumnIndexMap(s.schema, s.alias)
	}
	return s.colIndexMap
}
