package memengine

import (
	"encoding/binary"
	"errors"
	"math"
	"time"
	"unsafe"

	"lionsql/pkg/storage"
)

var (
	ErrFlatRecordTruncated = errors.New("flat record buffer truncated")
	ErrFlatRecordInvalid   = errors.New("flat record invalid layout")
)

// Small String Optimization threshold: strings/bytes <= 7 bytes are stored inline in slot[0..6]
// with slot[7] holding the ssoFlag | length. Strings > 7 bytes are stored in the variable heap area
// with slot[0..3] holding uint32 offset, slot[4..6] holding uint24 length, and slot[7] holding 0x00 ([P0-01]).
const (
	ssoMaxLen = 7
	ssoFlag   = 0x80
)

// encodeVarSlot encodes variable-length data into an 8-byte fixed slot ([P0-01]).
// Prevents offset-tag collision where vOff byte 1 equaled 0xFF.
func encodeVarSlot(slot []byte, raw []byte, varHeap *[]byte) {
	if len(raw) <= ssoMaxLen {
		copy(slot[:len(raw)], raw)
		for j := len(raw); j < 7; j++ {
			slot[j] = 0
		}
		slot[7] = ssoFlag | byte(len(raw))
	} else {
		vOff := uint32(len(*varHeap))
		vLen := uint32(len(raw))
		binary.LittleEndian.PutUint32(slot[0:4], vOff)
		slot[4] = byte(vLen)
		slot[5] = byte(vLen >> 8)
		slot[6] = byte(vLen >> 16)
		slot[7] = 0x00 // Explicit Heap Tag: completely isolated from vOff
		*varHeap = append(*varHeap, raw...)
	}
}

// decodeVarSlot extracts variable-length data from an 8-byte fixed slot and variable heap area ([P0-01]).
func decodeVarSlot(slot []byte, varArea []byte) ([]byte, error) {
	if (slot[7] & ssoFlag) != 0 {
		inlineLen := int(slot[7] & 0x7F)
		if inlineLen > ssoMaxLen {
			return nil, ErrFlatRecordInvalid
		}
		return slot[:inlineLen], nil
	}

	vOff := binary.LittleEndian.Uint32(slot[0:4])
	vLen := uint32(slot[4]) | (uint32(slot[5]) << 8) | (uint32(slot[6]) << 16)
	if uint64(vOff)+uint64(vLen) > uint64(len(varArea)) {
		return nil, ErrFlatRecordTruncated
	}
	return varArea[vOff : vOff+vLen], nil
}

// FlatRecordLayout computes fixed offsets and sizes for columns of a TableSchema.
type FlatRecordLayout struct {
	NumColumns    int
	NullBitmapLen int
	FixedOffsets  []int // byte offset of fixed slot for column i
	FixedSizes    []int // size in bytes for fixed slot
	TotalFixedLen int   // total length of fixed header + fixed slots
}

// BuildFlatRecordLayout precomputes the fixed binary offsets for a table schema.
// Layout:
//   [NullBitmap: ceil(N/8) bytes]
//   [Fixed Column Slots (8-byte aligned / sized)]
//   [Variable Area: pairs of (offset uint16, len uint16) + raw payload bytes]
func BuildFlatRecordLayout(schema *storage.TableSchema) *FlatRecordLayout {
	if schema == nil {
		return nil
	}
	n := len(schema.Columns)
	bitmapLen := (n + 7) / 8

	offsets := make([]int, n)
	sizes := make([]int, n)

	currentOffset := bitmapLen
	// Align to 8 bytes if needed
	if rem := currentOffset % 8; rem != 0 {
		currentOffset += (8 - rem)
	}

	for i, col := range schema.Columns {
		offsets[i] = currentOffset
		sz := 8 // default slot size: 8 bytes
		switch col.Type {
		case storage.TypeInt, storage.TypeFloat, storage.TypeDateTime, storage.TypeDate:
			sz = 8
		case storage.TypeBool:
			sz = 8 // padded to 8 for alignment
		case storage.TypeVarChar, storage.TypeJSON, storage.TypeUUID, storage.TypeBinary, storage.TypeDecimal:
			// 8-byte slot: if len <= 8 (SSO), store bytes directly.
			// if len > 8, store [varOffset uint32, varLen uint32] in the 8-byte slot!
			sz = 8
		default:
			sz = 8
		}
		sizes[i] = sz
		currentOffset += sz
	}

	return &FlatRecordLayout{
		NumColumns:    n,
		NullBitmapLen: bitmapLen,
		FixedOffsets:  offsets,
		FixedSizes:    sizes,
		TotalFixedLen: currentOffset,
	}
}

// EncodeRowToFlat serializes a storage.Row into a pointer-free flat byte slice.
func (l *FlatRecordLayout) EncodeRowToFlat(schema *storage.TableSchema, row storage.Row) ([]byte, error) {
	if l == nil || schema == nil {
		return nil, errors.New("nil layout or schema")
	}

	// 1. Initial fixed area allocation
	buf := make([]byte, l.TotalFixedLen, l.TotalFixedLen+64)

	// Variable heap scratch
	var varHeap []byte

	for i, col := range schema.Columns {
		var val storage.Value
		if i < len(row.Values) {
			val = row.Values[i]
		} else {
			val = storage.NullValue(col.Type)
		}

		if val.IsNull {
			// Set NULL bit
			buf[i/8] |= (1 << (i % 8))
			continue
		}

		offset := l.FixedOffsets[i]
		slot := buf[offset : offset+8]

		switch col.Type {
		case storage.TypeInt:
			binary.LittleEndian.PutUint64(slot, uint64(val.IVal))

		case storage.TypeFloat:
			binary.LittleEndian.PutUint64(slot, math.Float64bits(val.FVal))

		case storage.TypeBool:
			b := val.BVal
			if val.Type != storage.TypeBool {
				b = storage.BoolValueFromAny(val).BVal
			}
			if b {
				slot[0] = 1
			} else {
				slot[0] = 0
			}

		case storage.TypeDateTime:
			tVal := val.TVal
			binary.LittleEndian.PutUint64(slot, uint64(tVal.UnixNano()))

		case storage.TypeDate:
			tVal := val.TVal
			binary.LittleEndian.PutUint64(slot, uint64(tVal.Unix()))

		case storage.TypeVarChar, storage.TypeJSON, storage.TypeUUID:
			s := val.SVal
			raw := unsafe.Slice(unsafe.StringData(s), len(s))
			encodeVarSlot(slot, raw, &varHeap)

		case storage.TypeBinary:
			raw := unsafe.Slice(unsafe.StringData(val.BinVal), len(val.BinVal))
			encodeVarSlot(slot, raw, &varHeap)

		case storage.TypeDecimal:
			decStr := val.Decimal().String()
			raw := unsafe.Slice(unsafe.StringData(decStr), len(decStr))
			encodeVarSlot(slot, raw, &varHeap)

		default:
			str := val.String()
			raw := unsafe.Slice(unsafe.StringData(str), len(str))
			encodeVarSlot(slot, raw, &varHeap)
		}
	}

	if len(varHeap) > 0 {
		buf = append(buf, varHeap...)
	}

	return buf, nil
}

// DecodeFlatToRow reconstructs a storage.Row from the flat byte buffer.
func (l *FlatRecordLayout) DecodeFlatToRow(schema *storage.TableSchema, buf []byte) (storage.Row, error) {
	return l.DecodeFlatToRowWithArena(schema, buf, nil)
}

// DecodeFlatToRowWithArena reconstructs a storage.Row using arena slab pooling when arena is non-nil.
func (l *FlatRecordLayout) DecodeFlatToRowWithArena(schema *storage.TableSchema, buf []byte, arena *storage.Arena) (storage.Row, error) {
	if l == nil || schema == nil {
		return storage.Row{}, errors.New("nil layout or schema")
	}
	if len(buf) < l.TotalFixedLen {
		return storage.Row{}, ErrFlatRecordTruncated
	}

	numCols := l.NumColumns
	vals := arena.AllocValues(numCols)
	varArea := buf[l.TotalFixedLen:]

	for i, col := range schema.Columns {
		// 1. Check NULL bitmap
		if (buf[i/8] & (1 << (i % 8))) != 0 {
			vals[i] = storage.NullValue(col.Type)
			continue
		}

		offset := l.FixedOffsets[i]
		slot := buf[offset : offset+8]

		switch col.Type {
		case storage.TypeInt:
			v := int64(binary.LittleEndian.Uint64(slot))
			vals[i] = storage.IntValue(v)

		case storage.TypeFloat:
			bits := binary.LittleEndian.Uint64(slot)
			vals[i] = storage.FloatValue(math.Float64frombits(bits))

		case storage.TypeBool:
			vals[i] = storage.BoolValue(slot[0] != 0)

		case storage.TypeDateTime:
			nanos := int64(binary.LittleEndian.Uint64(slot))
			vals[i] = storage.Value{
				Type: storage.TypeDateTime,
				TVal: time.Unix(0, nanos).UTC(),
			}

		case storage.TypeDate:
			secs := int64(binary.LittleEndian.Uint64(slot))
			vals[i] = storage.Value{
				Type: storage.TypeDate,
				TVal: time.Unix(secs, 0).UTC(),
			}

		case storage.TypeVarChar, storage.TypeJSON, storage.TypeUUID:
			raw, err := decodeVarSlot(slot, varArea)
			if err != nil {
				return storage.Row{}, err
			}
			vals[i] = storage.Value{Type: col.Type, SVal: arena.AllocStringBytes(raw)}

		case storage.TypeBinary:
			raw, err := decodeVarSlot(slot, varArea)
			if err != nil {
				return storage.Row{}, err
			}
			vals[i] = storage.BinaryValue(raw)

		case storage.TypeDecimal:
			raw, err := decodeVarSlot(slot, varArea)
			if err != nil {
				return storage.Row{}, err
			}
			vals[i] = storage.DecimalValue(arena.AllocStringBytes(raw))

		default:
			raw, err := decodeVarSlot(slot, varArea)
			if err != nil {
				return storage.Row{}, err
			}
			vals[i] = storage.Value{Type: col.Type, SVal: arena.AllocStringBytes(raw)}
		}
	}

	return storage.Row{Values: vals}, nil
}
