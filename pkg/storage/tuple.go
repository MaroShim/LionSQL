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

		if offset > len(data) {
			return Row{}, fmt.Errorf("%w at col %d (%s)", ErrTupleBufferTooShort, i, col.Name)
		}

		switch col.Type {
		case TypeInt:
			iVal, n := binary.Varint(data[offset:])
			if n <= 0 {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			offset += n
			vals[i] = IntValue(iVal)

		case TypeFloat:
			if offset+8 > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			bits := binary.LittleEndian.Uint64(data[offset : offset+8])
			offset += 8
			vals[i] = FloatValue(math.Float64frombits(bits))

		case TypeBool:
			if offset+1 > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			bVal := data[offset] != 0
			offset += 1
			vals[i] = BoolValue(bVal)

		case TypeDateTime:
			if offset+8 > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			nano := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
			offset += 8
			vals[i] = DateTimeValue(time.Unix(0, nano).UTC())

		case TypeDate:
			if offset+8 > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			sec := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
			offset += 8
			vals[i] = DateValue(time.Unix(sec, 0).UTC())

		case TypeTime:
			strLen, n := binary.Uvarint(data[offset:])
			if n <= 0 || offset+n+int(strLen) > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			offset += n
			sVal := string(data[offset : offset+int(strLen)])
			offset += int(strLen)
			vals[i] = Value{Type: TypeTime, SVal: sVal}

		case TypeBinary:
			binLen, n := binary.Uvarint(data[offset:])
			if n <= 0 || offset+n+int(binLen) > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			offset += n
			bVal := data[offset : offset+int(binLen)]
			offset += int(binLen)
			vals[i] = BinaryValue(bVal)

		case TypeDecimal:
			decLen, n := binary.Uvarint(data[offset:])
			if n <= 0 || offset+n+int(decLen) > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			offset += n
			sVal := string(data[offset : offset+int(decLen)])
			offset += int(decLen)
			vals[i] = DecimalValue(sVal)

		case TypeVarChar:
			strLen, n := binary.Uvarint(data[offset:])
			if n <= 0 || offset+n+int(strLen) > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			offset += n
			sVal := string(data[offset : offset+int(strLen)])
			offset += int(strLen)
			vals[i] = StringValue(sVal)

		case TypeJSON:
			strLen, n := binary.Uvarint(data[offset:])
			if n <= 0 || offset+n+int(strLen) > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			offset += n
			sVal := string(data[offset : offset+int(strLen)])
			offset += int(strLen)
			vals[i] = JSONValue(sVal)

		case TypeUUID:
			strLen, n := binary.Uvarint(data[offset:])
			if n <= 0 || offset+n+int(strLen) > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			offset += n
			sVal := string(data[offset : offset+int(strLen)])
			offset += int(strLen)
			vals[i] = UUIDValue(sVal)

		default:
			strLen, n := binary.Uvarint(data[offset:])
			if n <= 0 || offset+n+int(strLen) > len(data) {
				return Row{}, fmt.Errorf("%w: at %s", ErrTupleBufferTooShort, col.Name)
			}
			offset += n
			sVal := string(data[offset : offset+int(strLen)])
			offset += int(strLen)
			vals[i] = StringValue(sVal)
		}
	}

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
