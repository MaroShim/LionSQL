package execution

import (
	"context"
	"fmt"
	"io"

	"sqlion/pkg/storage"
)

// SeqScanIterator scans a table sequentially row-by-row directly from a storage.Cursor.
type SeqScanIterator struct {
	tx        storage.Transaction
	tableName string
	alias     string
	schema    *storage.TableSchema
	cursor    storage.Cursor
}

// NewSeqScanIterator creates a new sequential scan iterator.
func NewSeqScanIterator(tx storage.Transaction, tableName, alias string, schema *storage.TableSchema) *SeqScanIterator {
	if alias == "" {
		alias = tableName
	}
	return &SeqScanIterator{
		tx:        tx,
		tableName: tableName,
		alias:     alias,
		schema:    schema,
	}
}

func (s *SeqScanIterator) Open(ctx context.Context) error {
	if s.tx == nil {
		return fmt.Errorf("seq scan: transaction is nil")
	}
	cur, err := s.tx.Scan(s.tableName)
	if err != nil {
		return fmt.Errorf("seq scan on %q: %w", s.tableName, err)
	}
	s.cursor = cur
	return nil
}

func (s *SeqScanIterator) Next(ctx context.Context) (Tuple, error) {
	if s.cursor == nil {
		return nil, io.EOF
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if !s.cursor.Next() {
		return nil, io.EOF
	}

	rec, err := s.cursor.Record()
	if err != nil {
		return nil, err
	}

	return FormatRecord(rec, s.schema, s.alias), nil
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

// IndexScanIterator scans a table using a secondary index range.
type IndexScanIterator struct {
	tx        storage.Transaction
	tableName string
	alias     string
	indexName string
	low       *storage.Value
	high      *storage.Value
	schema    *storage.TableSchema
	cursor    storage.Cursor
}

// NewIndexScanIterator creates a new index scan iterator.
func NewIndexScanIterator(tx storage.Transaction, tableName, alias, indexName string, low, high *storage.Value, schema *storage.TableSchema) *IndexScanIterator {
	if alias == "" {
		alias = tableName
	}
	return &IndexScanIterator{
		tx:        tx,
		tableName: tableName,
		alias:     alias,
		indexName: indexName,
		low:       low,
		high:      high,
		schema:    schema,
	}
}

func (s *IndexScanIterator) Open(ctx context.Context) error {
	if s.tx == nil {
		return fmt.Errorf("index scan: transaction is nil")
	}
	cur, err := s.tx.IndexScan(s.tableName, s.indexName, s.low, s.high)
	if err != nil {
		return fmt.Errorf("index scan %q on %q: %w", s.indexName, s.tableName, err)
	}
	s.cursor = cur
	return nil
}

func (s *IndexScanIterator) Next(ctx context.Context) (Tuple, error) {
	if s.cursor == nil {
		return nil, io.EOF
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if !s.cursor.Next() {
		return nil, io.EOF
	}

	rec, err := s.cursor.Record()
	if err != nil {
		return nil, err
	}

	return FormatRecord(rec, s.schema, s.alias), nil
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
