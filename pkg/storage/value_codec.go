package storage

import (
	"fmt"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

// EncodeMsgpack implements high-performance compact binary encoding for Value.
// Rather than writing all 9 struct fields and field name strings (80+ bytes per value),
// it encodes an ultra-compact 2-element array: [DataType, Payload] (~2-10 bytes).
func (v Value) EncodeMsgpack(enc *msgpack.Encoder) error {
	if err := enc.EncodeArrayLen(2); err != nil {
		return err
	}
	if err := enc.EncodeInt(int64(v.Type)); err != nil {
		return err
	}
	if v.IsNull {
		return enc.EncodeNil()
	}

	switch v.Type {
	case TypeInt:
		return enc.EncodeInt(v.IVal)
	case TypeFloat:
		return enc.EncodeFloat64(v.FVal)
	case TypeBool:
		return enc.EncodeBool(v.BVal)
	case TypeDateTime:
		return enc.EncodeTime(v.TVal)
	case TypeBinary:
		return enc.EncodeBytes([]byte(v.BinVal))
	case TypeDecimal:
		if v.DVal.value != nil {
			return enc.EncodeString(v.DVal.String())
		}
		if v.SVal != "" {
			return enc.EncodeString(v.SVal)
		}
		return enc.EncodeString("0")
	case TypeVarChar, TypeJSON, TypeUUID, TypeDate, TypeTime:
		return enc.EncodeString(v.SVal)
	default:
		return enc.EncodeString(v.String())
	}
}

// DecodeMsgpack implements backwards-compatible decoding for Value.
// It transparently decodes either the compact [Type, Payload] array or legacy map format.
func (v *Value) DecodeMsgpack(dec *msgpack.Decoder) error {
	code, err := dec.PeekCode()
	if err != nil {
		return err
	}

	// Legacy map format backwards-compatibility
	if msgpcode.IsFixedMap(code) || code == msgpcode.Map16 || code == msgpcode.Map32 {
		type legacyValue struct {
			Type   DataType
			IsNull bool
			IVal   int64
			SVal   string
			FVal   float64
			BVal   bool
			TVal   time.Time
			DVal   Decimal
			BinVal string
		}
		var leg legacyValue
		if err := dec.Decode(&leg); err != nil {
			return err
		}
		*v = Value{
			Type:   leg.Type,
			IsNull: leg.IsNull,
			IVal:   leg.IVal,
			SVal:   leg.SVal,
			FVal:   leg.FVal,
			BVal:   leg.BVal,
			TVal:   leg.TVal,
			DVal:   leg.DVal,
			BinVal: leg.BinVal,
		}
		return nil
	}

	// Compact array format
	arrLen, err := dec.DecodeArrayLen()
	if err != nil {
		return err
	}
	if arrLen < 2 {
		return fmt.Errorf("invalid compact value array length: %d", arrLen)
	}

	tInt, err := dec.DecodeInt()
	if err != nil {
		return err
	}
	v.Type = DataType(tInt)

	nextCode, err := dec.PeekCode()
	if err != nil {
		return err
	}
	if nextCode == msgpcode.Nil {
		_ = dec.DecodeNil()
		v.IsNull = true
		return nil
	}
	v.IsNull = false

	switch v.Type {
	case TypeInt:
		i, err := dec.DecodeInt64()
		if err != nil {
			return err
		}
		*v = IntValue(i)
	case TypeFloat:
		f, err := dec.DecodeFloat64()
		if err != nil {
			return err
		}
		*v = FloatValue(f)
	case TypeBool:
		b, err := dec.DecodeBool()
		if err != nil {
			return err
		}
		*v = BoolValue(b)
	case TypeDateTime:
		t, err := dec.DecodeTime()
		if err != nil {
			return err
		}
		*v = DateTimeValue(t)
	case TypeBinary:
		b, err := dec.DecodeBytes()
		if err != nil {
			return err
		}
		*v = BinaryValue(b)
	case TypeDecimal:
		s, err := dec.DecodeString()
		if err != nil {
			return err
		}
		*v = DecimalValue(s)
	case TypeDate:
		s, err := dec.DecodeString()
		if err != nil {
			return err
		}
		*v = DateValueFromString(s)
	case TypeTime:
		s, err := dec.DecodeString()
		if err != nil {
			return err
		}
		*v = Value{Type: TypeTime, SVal: s}
	case TypeUUID:
		s, err := dec.DecodeString()
		if err != nil {
			return err
		}
		*v = UUIDValue(s)
	case TypeJSON:
		s, err := dec.DecodeString()
		if err != nil {
			return err
		}
		*v = JSONValue(s)
	case TypeVarChar:
		s, err := dec.DecodeString()
		if err != nil {
			return err
		}
		*v = StringValue(s)
	default:
		s, err := dec.DecodeString()
		if err != nil {
			return err
		}
		v.SVal = s
	}

	return nil
}
