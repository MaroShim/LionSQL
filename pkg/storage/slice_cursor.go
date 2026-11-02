package storage

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrCursorOutOfRange = errors.New("cursor out of range")

// SliceCursor is an in-memory cursor iterating over a slice of Rows and/or Records.
type SliceCursor struct {
	schema *TableSchema
	rows   []Row
	recs   []Record
	idx    int
}

// NewSliceCursor creates a new SliceCursor with the given schema, rows, and records.
func NewSliceCursor(schema *TableSchema, rows []Row, recs []Record) *SliceCursor {
	return &SliceCursor{
		schema: schema,
		rows:   rows,
		recs:   recs,
		idx:    -1,
	}
}

func (c *SliceCursor) Next() bool {
	c.idx++
	if len(c.rows) > 0 {
		return c.idx < len(c.rows)
	}
	return c.idx < len(c.recs)
}

func (c *SliceCursor) Row() (Row, error) {
	if c.idx >= 0 && c.idx < len(c.rows) {
		return c.rows[c.idx], nil
	}
	if c.idx >= 0 && c.idx < len(c.recs) && c.recs[c.idx] != nil && c.schema != nil {
		return c.schema.RecordToRow(c.recs[c.idx]), nil
	}
	return Row{}, ErrCursorOutOfRange
}

func (c *SliceCursor) Record() (Record, error) {
	if c.idx >= 0 && c.idx < len(c.recs) && c.recs[c.idx] != nil {
		return c.recs[c.idx], nil
	}
	if c.idx >= 0 && c.idx < len(c.rows) && c.schema != nil {
		return c.schema.RowToRecord(c.rows[c.idx]), nil
	}
	return nil, ErrCursorOutOfRange
}

func (c *SliceCursor) Close() error {
	c.rows = nil
	c.recs = nil
	return nil
}

// ValueFromAny converts an arbitrary Go value into a typed storage.Value according to targetType.
func ValueFromAny(v any, targetType DataType) Value {
	if v == nil {
		return NullValue(targetType)
	}
	if sv, ok := v.(Value); ok {
		return sv
	}
	switch targetType {
	case TypeInt:
		switch val := v.(type) {
		case int:
			return IntValue(int64(val))
		case int32:
			return IntValue(int64(val))
		case int64:
			return IntValue(val)
		case float64:
			return IntValue(int64(val))
		case string:
			if i, err := strconv.ParseInt(val, 10, 64); err == nil {
				return IntValue(i)
			}
		}
		return IntValue(0)
	case TypeFloat:
		switch val := v.(type) {
		case float64:
			return FloatValue(val)
		case float32:
			return FloatValue(float64(val))
		case int:
			return FloatValue(float64(val))
		case int64:
			return FloatValue(float64(val))
		case string:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				return FloatValue(f)
			}
		}
		return FloatValue(0)
	case TypeBool:
		switch val := v.(type) {
		case bool:
			return BoolValue(val)
		case int:
			return BoolValue(val != 0)
		case int64:
			return BoolValue(val != 0)
		case string:
			return BoolValue(strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "t"))
		}
		return BoolValue(false)
	case TypeDateTime, TypeDate, TypeTime:
		switch val := v.(type) {
		case time.Time:
			return DateTimeValue(val)
		case string:
			if t, err := time.Parse("2006-01-02 15:04:05", val); err == nil {
				return DateTimeValue(t)
			}
			return StringValue(val)
		}
	case TypeDecimal:
		switch val := v.(type) {
		case Decimal:
			return DecimalValueFromDecimal(val)
		case string:
			return DecimalValue(val)
		case float64:
			return DecimalValue(strconv.FormatFloat(val, 'f', 4, 64))
		case int64:
			return DecimalValue(strconv.FormatInt(val, 10))
		case int:
			return DecimalValue(strconv.Itoa(val))
		}
	case TypeJSON:
		return JSONValue(fmt.Sprintf("%v", v))
	case TypeBinary:
		if b, ok := v.([]byte); ok {
			return BinaryValue(b)
		}
	}
	return StringValue(fmt.Sprintf("%v", v))
}

