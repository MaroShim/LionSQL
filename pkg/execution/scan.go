package execution

import (
	"context"
	"fmt"
	"io"

	"sqlion/pkg/storage"
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

func (s *SeqScanIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	if s.cursor == nil {
		return io.EOF
	}
	dest.Reset()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	rawCur, hasRaw := s.cursor.(storage.RawTupleCursor)

	for dest.Len() < DefaultChunkSize {
		if !s.cursor.Next() {
			if dest.Len() > 0 {
				return nil
			}
			return io.EOF
		}

		if hasRaw {
			if rawData, ok := rawCur.RawTuple(); ok {
				if lazy, err := storage.NewLazyTuple(s.schema, rawData); err == nil {
					dest.Tuples = append(dest.Tuples, Tuple{
						Values: make([]any, len(s.schema.Columns)),
						Index:  s.colIndexMap,
						Lazy:   lazy,
					})
					continue
				}
			}
		}

		if row, err := s.cursor.Row(); err == nil {
			dest.Tuples = append(dest.Tuples, FormatRow(row, s.schema, s.alias, s.colIndexMap))
			continue
		}

		rec, err := s.cursor.Record()
		if err != nil {
			if dest.Len() > 0 {
				return nil
			}
			return err
		}
		dest.Tuples = append(dest.Tuples, FormatRecord(rec, s.schema, s.alias, s.colIndexMap))
	}
	return nil
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
	if s.cursor == nil {
		return io.EOF
	}
	dest.Reset()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	rawCur, hasRaw := s.cursor.(storage.RawTupleCursor)

	for dest.Len() < DefaultChunkSize {
		if !s.cursor.Next() {
			if dest.Len() > 0 {
				return nil
			}
			return io.EOF
		}

		if hasRaw {
			if rawData, ok := rawCur.RawTuple(); ok {
				if lazy, err := storage.NewLazyTuple(s.schema, rawData); err == nil {
					dest.Tuples = append(dest.Tuples, Tuple{
						Values: make([]any, len(s.schema.Columns)),
						Index:  s.colIndexMap,
						Lazy:   lazy,
					})
					continue
				}
			}
		}

		if row, err := s.cursor.Row(); err == nil {
			dest.Tuples = append(dest.Tuples, FormatRow(row, s.schema, s.alias, s.colIndexMap))
			continue
		}

		rec, err := s.cursor.Record()
		if err != nil {
			if dest.Len() > 0 {
				return nil
			}
			return err
		}
		dest.Tuples = append(dest.Tuples, FormatRecord(rec, s.schema, s.alias, s.colIndexMap))
	}
	return nil
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
