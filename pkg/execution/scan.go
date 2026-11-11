package execution

import (
	"context"
	"fmt"
	"io"

	"lionsql/pkg/storage"
)

// SeqScanIterator scans a table sequentially row-by-row or in vectorized chunks directly from a storage.Cursor.
type SeqScanIterator struct {
	tx          storage.Transaction
	tableName   string
	alias       string
	schema      *storage.TableSchema
	colIndexMap *ColumnIndexMap
	cursor      storage.Cursor
	partitions  []string
	bufChunk    Chunk
	bufIdx      int
	eof         bool
	arena       *storage.Arena
}

// SetArena configures an optional memory arena for zero-GC tuple allocation.
func (s *SeqScanIterator) SetArena(a *storage.Arena) {
	s.arena = a
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

func fillScanChunk(ctx context.Context, cursor storage.Cursor, schema *storage.TableSchema, alias string, colMap *ColumnIndexMap, arena *storage.Arena, eof *bool, dest *Chunk) error {
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

	for dest.Len() < DefaultChunkSize {
		if !cursor.Next() {
			*eof = true
			if dest.Len() > 0 {
				return nil
			}
			return io.EOF
		}

		if hasRaw {
			if rawData, ok := rawCur.RawTuple(); ok {
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
				dest.Tuples = append(dest.Tuples, FormatRowWithArena(row, schema, alias, colMap, arena))
				continue
			}
		}

		if row, err := cursor.Row(); err == nil {
			dest.Tuples = append(dest.Tuples, FormatRowWithArena(row, schema, alias, colMap, arena))
			continue
		}

		rec, err := cursor.Record()
		if err != nil {
			if dest.Len() > 0 {
				return nil
			}
			return err
		}
		dest.Tuples = append(dest.Tuples, FormatRecord(rec, schema, alias, colMap))
	}
	return nil
}

func (s *SeqScanIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	return fillScanChunk(ctx, s.cursor, s.schema, s.alias, s.colIndexMap, s.arena, &s.eof, dest)
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
	tx          storage.Transaction
	tableName   string
	alias       string
	indexName   string
	low         *storage.Value
	high        *storage.Value
	schema      *storage.TableSchema
	colIndexMap *ColumnIndexMap
	cursor      storage.Cursor
	IndexOnly   bool
	Desc        bool
	bufChunk    Chunk
	bufIdx      int
	eof         bool
	arena       *storage.Arena
}

// SetArena configures an optional memory arena for zero-GC tuple allocation.
func (s *IndexScanIterator) SetArena(a *storage.Arena) {
	s.arena = a
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
	return fillScanChunk(ctx, s.cursor, s.schema, s.alias, s.colIndexMap, s.arena, &s.eof, dest)
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
