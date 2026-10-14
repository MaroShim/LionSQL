package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	TupleCodecVersion byte = 0x01
)

var (
	ErrInvalidTupleHeader  = errors.New("invalid tuple header or version")
	ErrTupleBufferTooShort = errors.New("tuple buffer truncated or too short")

	tupleBufferPool = sync.Pool{
		New: func() any {
			b := make([]byte, 0, 512)
			return &b
		},
	}
)

// GetTupleBuffer borrows a scratch slice from the global buffer pool.
func GetTupleBuffer() *[]byte {
	return tupleBufferPool.Get().(*[]byte)
}

// PutTupleBuffer returns a scratch slice back to the global buffer pool.
func PutTupleBuffer(buf *[]byte) {
	if buf != nil {
		*buf = (*buf)[:0]
		tupleBufferPool.Put(buf)
	}
}

// Row represents an ordered sequence of column values aligned with TableSchema.Columns.
type Row struct {
	Values []Value
}

// RecordToRow converts a column-mapped Record into an ordered Row according to schema.
func (s *TableSchema) RecordToRow(rec Record) Row {
	if s == nil {
		return Row{}
	}
	vals := make([]Value, len(s.Columns))
	for i, col := range s.Columns {
		if v, ok := rec[col.Name]; ok {
			vals[i] = v
		} else {
			vals[i] = NullValue(col.Type)
		}
	}
	return Row{Values: vals}
}

// RowToRecord converts an ordered Row back into a column-mapped Record according to schema.
func (s *TableSchema) RowToRecord(row Row) Record {
	if s == nil {
		return nil
	}
	rec := make(Record, len(s.Columns))
	for i, col := range s.Columns {
		if i < len(row.Values) {
			rec[col.Name] = row.Values[i]
		} else {
			rec[col.Name] = NullValue(col.Type)
		}
	}
	return rec
}

// ApplyRecordToRow updates a Row with the fields present in rec according to schema.
func (s *TableSchema) ApplyRecordToRow(row Row, rec Record) Row {
	if s == nil {
		return row
	}
	vals := make([]Value, len(s.Columns))
	if len(row.Values) > 0 {
		copy(vals, row.Values)
	}
	for i, col := range s.Columns {
		if v, ok := rec[col.Name]; ok {
			vals[i] = v
		} else if i >= len(row.Values) {
			vals[i] = NullValue(col.Type)
		}
	}
	return Row{Values: vals}
}

// EncodeRow encodes an ordered Row into a compact schema-aware binary tuple.
func EncodeRow(schema *TableSchema, row Row) ([]byte, error) {
	if schema == nil {
		return nil, errors.New("nil table schema")
	}

	numCols := len(schema.Columns)
	nullBitmapLen := (numCols + 7) / 8

	buf := make([]byte, 1+nullBitmapLen, 64)
	return EncodeRowToBuffer(schema, row, buf)
}

// EncodeRowToBuffer encodes an ordered Row into the provided destination slice, reusing its capacity.
func EncodeRowToBuffer(schema *TableSchema, row Row, buf []byte) ([]byte, error) {
	if schema == nil {
		return nil, errors.New("nil table schema")
	}

	numCols := len(schema.Columns)
	nullBitmapLen := (numCols + 7) / 8

	neededLen := 1 + nullBitmapLen
	if cap(buf) < neededLen {
		buf = make([]byte, neededLen, neededLen+numCols*8)
	} else {
		buf = buf[:neededLen]
		for i := 0; i < neededLen; i++ {
			buf[i] = 0
		}
	}
	buf[0] = TupleCodecVersion

	var scratch [10]byte

	for i, col := range schema.Columns {
		var val Value
		if i < len(row.Values) {
			val = row.Values[i]
		} else {
			val = NullValue(col.Type)
		}
		if val.IsNull {
			buf[1+(i/8)] |= 1 << (i % 8)
			continue
		}

		switch col.Type {
		case TypeInt:
			n := binary.PutVarint(scratch[:], val.IVal)
			buf = append(buf, scratch[:n]...)

		case TypeFloat:
			bits := math.Float64bits(val.FVal)
			binary.LittleEndian.PutUint64(scratch[:8], bits)
			buf = append(buf, scratch[:8]...)

		case TypeBool:
			if val.BVal {
				buf = append(buf, 1)
			} else {
				buf = append(buf, 0)
			}

		case TypeDateTime:
			binary.LittleEndian.PutUint64(scratch[:8], uint64(val.TVal.UnixNano()))
			buf = append(buf, scratch[:8]...)

		case TypeDate:
			binary.LittleEndian.PutUint64(scratch[:8], uint64(val.TVal.Unix()))
			buf = append(buf, scratch[:8]...)

		case TypeTime:
			str := val.SVal
			n := binary.PutUvarint(scratch[:], uint64(len(str)))
			buf = append(buf, scratch[:n]...)
			buf = append(buf, str...)

		case TypeBinary:
			raw := val.BinVal
			n := binary.PutUvarint(scratch[:], uint64(len(raw)))
			buf = append(buf, scratch[:n]...)
			buf = append(buf, raw...)

		case TypeDecimal:
			str := val.Decimal().String()
			n := binary.PutUvarint(scratch[:], uint64(len(str)))
			buf = append(buf, scratch[:n]...)
			buf = append(buf, str...)

		case TypeVarChar, TypeJSON, TypeUUID:
			str := val.SVal
			n := binary.PutUvarint(scratch[:], uint64(len(str)))
			buf = append(buf, scratch[:n]...)
			buf = append(buf, str...)

		default:
			str := val.String()
			n := binary.PutUvarint(scratch[:], uint64(len(str)))
			buf = append(buf, scratch[:n]...)
			buf = append(buf, str...)
		}
	}

	return buf, nil
}

// EncodeTuple encodes a Record into a compact schema-aware binary tuple.
// Format Layout:
//   [1 byte Version]
//   [ceil(N/8) bytes NullBitmap]
//   [Sequential Payload for non-null columns]
func EncodeTuple(schema *TableSchema, rec Record) ([]byte, error) {
	if schema == nil {
		return nil, errors.New("nil table schema")
	}
	return EncodeRow(schema, schema.RecordToRow(rec))
}

// EncodeTupleToBuffer encodes a Record into the provided destination slice, reusing its capacity.
func EncodeTupleToBuffer(schema *TableSchema, rec Record, buf []byte) ([]byte, error) {
	if schema == nil {
		return nil, errors.New("nil table schema")
	}
	return EncodeRowToBuffer(schema, schema.RecordToRow(rec), buf)
}


// DecodeTupleToRow decodes a compact binary tuple directly into a Row without map allocations.
func DecodeTupleToRow(schema *TableSchema, data []byte) (Row, error) {
	if schema == nil {
		return Row{}, errors.New("nil table schema")
	}
	if len(data) < 1 {
		return Row{}, ErrTupleBufferTooShort
	}
	if data[0] != TupleCodecVersion {
		return Row{}, fmt.Errorf("%w: got 0x%02x, expected 0x%02x", ErrInvalidTupleHeader, data[0], TupleCodecVersion)
	}

	numCols := len(schema.Columns)
	nullBitmapLen := (numCols + 7) / 8
	if len(data) < 1+nullBitmapLen {
		return Row{}, ErrTupleBufferTooShort
	}

	bitmap := data[1 : 1+nullBitmapLen]
	offset := 1 + nullBitmapLen

	vals := make([]Value, numCols)

	for i, col := range schema.Columns {
		isNull := (bitmap[i/8] & (1 << (i % 8))) != 0
		if isNull {
			vals[i] = NullValue(col.Type)
			continue
		}

		v, nextOffset, err := DecodeTupleValue(col.Type, data, offset)
		if err != nil {
			return Row{}, fmt.Errorf("%w at col %d (%s)", err, i, col.Name)
		}
		vals[i] = v
		offset = nextOffset
	}

	return Row{Values: vals}, nil
}

// DecodeTupleValue decodes a single column value of colType from data starting at offset.
// It returns the decoded Value and the next offset in data.
func DecodeTupleValue(colType DataType, data []byte, offset int) (Value, int, error) {
	if offset > len(data) {
		return Value{}, offset, ErrTupleBufferTooShort
	}
	switch colType {
	case TypeInt:
		iVal, n := binary.Varint(data[offset:])
		if n <= 0 {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		return IntValue(iVal), offset + n, nil

	case TypeFloat:
		if offset+8 > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		bits := binary.LittleEndian.Uint64(data[offset : offset+8])
		return FloatValue(math.Float64frombits(bits)), offset + 8, nil

	case TypeBool:
		if offset+1 > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		bVal := data[offset] != 0
		return BoolValue(bVal), offset + 1, nil

	case TypeDateTime:
		if offset+8 > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		nano := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
		return DateTimeValue(time.Unix(0, nano).UTC()), offset + 8, nil

	case TypeDate:
		if offset+8 > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		sec := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
		return DateValue(time.Unix(sec, 0).UTC()), offset + 8, nil

	case TypeTime:
		strLen, n := binary.Uvarint(data[offset:])
		if n <= 0 || offset+n+int(strLen) > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		sVal := string(data[offset+n : offset+n+int(strLen)])
		return Value{Type: TypeTime, SVal: sVal}, offset + n + int(strLen), nil

	case TypeBinary:
		binLen, n := binary.Uvarint(data[offset:])
		if n <= 0 || offset+n+int(binLen) > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		bVal := data[offset+n : offset+n+int(binLen)]
		return BinaryValue(bVal), offset + n + int(binLen), nil

	case TypeDecimal:
		decLen, n := binary.Uvarint(data[offset:])
		if n <= 0 || offset+n+int(decLen) > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		sVal := string(data[offset+n : offset+n+int(decLen)])
		return DecimalValue(sVal), offset + n + int(decLen), nil

	case TypeVarChar:
		strLen, n := binary.Uvarint(data[offset:])
		if n <= 0 || offset+n+int(strLen) > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		sVal := string(data[offset+n : offset+n+int(strLen)])
		return StringValue(sVal), offset + n + int(strLen), nil

	case TypeJSON:
		strLen, n := binary.Uvarint(data[offset:])
		if n <= 0 || offset+n+int(strLen) > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		sVal := string(data[offset+n : offset+n+int(strLen)])
		return JSONValue(sVal), offset + n + int(strLen), nil

	case TypeUUID:
		strLen, n := binary.Uvarint(data[offset:])
		if n <= 0 || offset+n+int(strLen) > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		sVal := string(data[offset+n : offset+n+int(strLen)])
		return UUIDValue(sVal), offset + n + int(strLen), nil

	default:
		strLen, n := binary.Uvarint(data[offset:])
		if n <= 0 || offset+n+int(strLen) > len(data) {
			return Value{}, offset, ErrTupleBufferTooShort
		}
		sVal := string(data[offset+n : offset+n+int(strLen)])
		return StringValue(sVal), offset + n + int(strLen), nil
	}
}

// SkipTupleColumn skips over the column value at offset without any string or heap allocations.
// It returns the next offset in data.
func SkipTupleColumn(colType DataType, data []byte, offset int) (int, error) {
	if offset > len(data) {
		return offset, ErrTupleBufferTooShort
	}
	switch colType {
	case TypeInt:
		_, n := binary.Varint(data[offset:])
		if n <= 0 || offset+n > len(data) {
			return offset, ErrTupleBufferTooShort
		}
		return offset + n, nil

	case TypeFloat, TypeDateTime, TypeDate:
		if offset+8 > len(data) {
			return offset, ErrTupleBufferTooShort
		}
		return offset + 8, nil

	case TypeBool:
		if offset+1 > len(data) {
			return offset, ErrTupleBufferTooShort
		}
		return offset + 1, nil

	case TypeTime, TypeBinary, TypeDecimal, TypeVarChar, TypeJSON, TypeUUID:
		fallthrough
	default:
		strLen, n := binary.Uvarint(data[offset:])
		if n <= 0 || offset+n+int(strLen) > len(data) {
			return offset, ErrTupleBufferTooShort
		}
		return offset + n + int(strLen), nil
	}
}

// DecodeTupleSelected decodes only the specified column indices from a binary tuple,
// skipping unselected columns without allocating strings or heap memory.
func DecodeTupleSelected(schema *TableSchema, data []byte, selectedCols []int) (Row, error) {
	if schema == nil {
		return Row{}, errors.New("nil table schema")
	}
	if len(data) < 1 {
		return Row{}, ErrTupleBufferTooShort
	}
	if data[0] != TupleCodecVersion {
		return Row{}, fmt.Errorf("%w: got 0x%02x, expected 0x%02x", ErrInvalidTupleHeader, data[0], TupleCodecVersion)
	}

	numCols := len(schema.Columns)
	nullBitmapLen := (numCols + 7) / 8
	if len(data) < 1+nullBitmapLen {
		return Row{}, ErrTupleBufferTooShort
	}

	bitmap := data[1 : 1+nullBitmapLen]
	offset := 1 + nullBitmapLen

	needed := make([]bool, numCols)
	for _, idx := range selectedCols {
		if idx >= 0 && idx < numCols {
			needed[idx] = true
		}
	}

	vals := make([]Value, numCols)

	for i, col := range schema.Columns {
		isNull := (bitmap[i/8] & (1 << (i % 8))) != 0
		if isNull {
			vals[i] = NullValue(col.Type)
			continue
		}

		if !needed[i] {
			nextOffset, err := SkipTupleColumn(col.Type, data, offset)
			if err != nil {
				return Row{}, fmt.Errorf("%w at col %d (%s)", err, i, col.Name)
			}
			vals[i] = NullValue(col.Type)
			offset = nextOffset
		} else {
			v, nextOffset, err := DecodeTupleValue(col.Type, data, offset)
			if err != nil {
				return Row{}, fmt.Errorf("%w at col %d (%s)", err, i, col.Name)
			}
			vals[i] = v
			offset = nextOffset
		}
	}

	return Row{Values: vals}, nil
}

// LazyTuple represents a lazily-decoded binary tuple where columns are decoded on demand.
type LazyTuple struct {
	mu            sync.Mutex
	schema        *TableSchema
	data          []byte
	decoded       []Value
	isDecoded     []bool
	colOffsets    []int
	hasOffset     []bool
	nullBitmapLen int
	nextColToScan int
	currOffset    int
}

// NewLazyTuple initializes a LazyTuple from binary tuple data.
func NewLazyTuple(schema *TableSchema, data []byte) (*LazyTuple, error) {
	if schema == nil {
		return nil, errors.New("nil table schema")
	}
	if len(data) < 1 {
		return nil, ErrTupleBufferTooShort
	}
	if data[0] != TupleCodecVersion {
		return nil, fmt.Errorf("%w: got 0x%02x, expected 0x%02x", ErrInvalidTupleHeader, data[0], TupleCodecVersion)
	}

	numCols := len(schema.Columns)
	nullBitmapLen := (numCols + 7) / 8
	if len(data) < 1+nullBitmapLen {
		return nil, ErrTupleBufferTooShort
	}

	return &LazyTuple{
		schema:        schema,
		data:          data,
		decoded:       make([]Value, numCols),
		isDecoded:     make([]bool, numCols),
		colOffsets:    make([]int, numCols),
		hasOffset:     make([]bool, numCols),
		nullBitmapLen: nullBitmapLen,
		nextColToScan: 0,
		currOffset:    1 + nullBitmapLen,
	}, nil
}

// Get returns the decoded Value for column colIdx, decoding it on-demand if not already cached.
func (lt *LazyTuple) Get(colIdx int) (Value, error) {
	if lt == nil || lt.schema == nil {
		return Value{}, errors.New("nil lazy tuple")
	}
	if colIdx < 0 || colIdx >= len(lt.schema.Columns) {
		return Value{}, ErrColumnNotFound
	}

	lt.mu.Lock()
	defer lt.mu.Unlock()

	if lt.isDecoded[colIdx] {
		return lt.decoded[colIdx], nil
	}

	// Check null bitmap
	isNull := (lt.data[1+(colIdx/8)] & (1 << (colIdx % 8))) != 0
	if isNull {
		lt.decoded[colIdx] = NullValue(lt.schema.Columns[colIdx].Type)
		lt.isDecoded[colIdx] = true
		return lt.decoded[colIdx], nil
	}

	// If we already know the exact byte offset for this column:
	if lt.hasOffset[colIdx] {
		val, _, err := DecodeTupleValue(lt.schema.Columns[colIdx].Type, lt.data, lt.colOffsets[colIdx])
		if err != nil {
			return Value{}, fmt.Errorf("%w at col %d (%s)", err, colIdx, lt.schema.Columns[colIdx].Name)
		}
		lt.decoded[colIdx] = val
		lt.isDecoded[colIdx] = true
		return val, nil
	}

	// Scan forward from nextColToScan up to colIdx
	for c := lt.nextColToScan; c <= colIdx; c++ {
		colIsNull := (lt.data[1+(c/8)] & (1 << (c % 8))) != 0
		if colIsNull {
			lt.decoded[c] = NullValue(lt.schema.Columns[c].Type)
			lt.isDecoded[c] = true
			continue
		}

		lt.colOffsets[c] = lt.currOffset
		lt.hasOffset[c] = true

		if c == colIdx {
			val, nextOffset, err := DecodeTupleValue(lt.schema.Columns[c].Type, lt.data, lt.currOffset)
			if err != nil {
				return Value{}, fmt.Errorf("%w at col %d (%s)", err, c, lt.schema.Columns[c].Name)
			}
			lt.decoded[c] = val
			lt.isDecoded[c] = true
			lt.currOffset = nextOffset
		} else {
			nextOffset, err := SkipTupleColumn(lt.schema.Columns[c].Type, lt.data, lt.currOffset)
			if err != nil {
				return Value{}, fmt.Errorf("%w at col %d (%s)", err, c, lt.schema.Columns[c].Name)
			}
			lt.currOffset = nextOffset
		}
	}

	if colIdx >= lt.nextColToScan {
		lt.nextColToScan = colIdx + 1
	}

	return lt.decoded[colIdx], nil
}

// IsDecoded returns true if column colIdx has been decoded.
func (lt *LazyTuple) IsDecoded(colIdx int) bool {
	if lt == nil || colIdx < 0 || colIdx >= len(lt.isDecoded) {
		return false
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return lt.isDecoded[colIdx]
}

// DecodedValues returns a snapshot of currently decoded values.
func (lt *LazyTuple) DecodedValues() []Value {
	if lt == nil {
		return nil
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	res := make([]Value, len(lt.decoded))
	copy(res, lt.decoded)
	return res
}

// MaterializeRow ensures all columns are decoded and returns the complete Row.
func (lt *LazyTuple) MaterializeRow() (Row, error) {
	if lt == nil || lt.schema == nil {
		return Row{}, errors.New("nil lazy tuple")
	}
	numCols := len(lt.schema.Columns)
	for c := 0; c < numCols; c++ {
		if _, err := lt.Get(c); err != nil {
			return Row{}, err
		}
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	vals := make([]Value, numCols)
	copy(vals, lt.decoded)
	return Row{Values: vals}, nil
}

// DecodeTuple decodes a compact binary tuple back into a Record.
func DecodeTuple(schema *TableSchema, data []byte) (Record, error) {
	row, err := DecodeTupleToRow(schema, data)
	if err != nil {
		return nil, err
	}
	return schema.RowToRecord(row), nil
}
