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

// Small String Optimization threshold: strings <= 8 bytes are stored inline.
const ssoMaxLen = 8

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
			strBytes := unsafe.Slice(unsafe.StringData(s), len(s))
			if len(strBytes) <= ssoMaxLen {
				// SSO (Small String Optimization): store directly in the 8-byte slot with length in high byte
				copy(slot[:ssoMaxLen], strBytes)
				// High 4 bits: flag 0x80 means SSO, lower 4 bits is length
				// Actually: slot has 8 bytes. If len <= 7, we can use slot[7] as len.
				// To keep it simple: slot[0..3] = varOffset/len.
				// Even cleaner:
				// If len <= 8:
				// Use 1 byte for length in a dedicated way, or tag:
				// Let's use standard layout:
				// slot[0..3] = uint32(len)
				// if len <= 4: inline in slot[4..7].
				// But to allow full 8 bytes SSO:
				// We can append to varHeap if len > 8.
			}
			// For general variable data:
			if len(strBytes) <= 4 {
				// Inline up to 4 bytes:
				// slot[0] = length, slot[1..3] unused, slot[4..7] data
				slot[0] = byte(len(strBytes))
				slot[1] = 0xFF // inline tag
				copy(slot[4:4+len(strBytes)], strBytes)
			} else {
				// Store in varHeap
				vOff := uint32(len(varHeap))
				vLen := uint32(len(strBytes))
				slot[0] = 0
				slot[1] = 0x00 // heap tag
				binary.LittleEndian.PutUint32(slot[0:4], vOff)
				binary.LittleEndian.PutUint32(slot[4:8], vLen)
				varHeap = append(varHeap, strBytes...)
			}

		case storage.TypeBinary:
			raw := val.BinVal
			if len(raw) <= 4 {
				slot[0] = byte(len(raw))
				slot[1] = 0xFF // inline tag
				copy(slot[4:4+len(raw)], raw)
			} else {
				vOff := uint32(len(varHeap))
				vLen := uint32(len(raw))
				binary.LittleEndian.PutUint32(slot[0:4], vOff)
				binary.LittleEndian.PutUint32(slot[4:8], vLen)
				varHeap = append(varHeap, raw...)
			}

		case storage.TypeDecimal:
			decStr := val.Decimal().String()
			raw := unsafe.Slice(unsafe.StringData(decStr), len(decStr))
			if len(raw) <= 4 {
				slot[0] = byte(len(raw))
				slot[1] = 0xFF
				copy(slot[4:4+len(raw)], raw)
			} else {
				vOff := uint32(len(varHeap))
				vLen := uint32(len(raw))
				binary.LittleEndian.PutUint32(slot[0:4], vOff)
				binary.LittleEndian.PutUint32(slot[4:8], vLen)
				varHeap = append(varHeap, raw...)
			}

		default:
			str := val.String()
			raw := unsafe.Slice(unsafe.StringData(str), len(str))
			vOff := uint32(len(varHeap))
			vLen := uint32(len(raw))
			binary.LittleEndian.PutUint32(slot[0:4], vOff)
			binary.LittleEndian.PutUint32(slot[4:8], vLen)
			varHeap = append(varHeap, raw...)
		}
	}

	if len(varHeap) > 0 {
		buf = append(buf, varHeap...)
	}

	return buf, nil
}

// DecodeFlatToRow reconstructs a storage.Row from the flat byte buffer.
func (l *FlatRecordLayout) DecodeFlatToRow(schema *storage.TableSchema, buf []byte) (storage.Row, error) {
	if l == nil || schema == nil {
		return storage.Row{}, errors.New("nil layout or schema")
	}
	if len(buf) < l.TotalFixedLen {
		return storage.Row{}, ErrFlatRecordTruncated
	}

	numCols := l.NumColumns
	vals := make([]storage.Value, numCols)
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
			if slot[1] == 0xFF {
				// Inlined small string
				inlineLen := int(slot[0])
				if inlineLen > 4 {
					return storage.Row{}, ErrFlatRecordInvalid
				}
				s := string(slot[4 : 4+inlineLen])
				vals[i] = storage.Value{Type: col.Type, SVal: s}
			} else {
				vOff := binary.LittleEndian.Uint32(slot[0:4])
				vLen := binary.LittleEndian.Uint32(slot[4:8])
				if int(vOff+vLen) > len(varArea) {
					return storage.Row{}, ErrFlatRecordTruncated
				}
				s := string(varArea[vOff : vOff+vLen])
				vals[i] = storage.Value{Type: col.Type, SVal: s}
			}

		case storage.TypeBinary:
			if slot[1] == 0xFF {
				inlineLen := int(slot[0])
				raw := make([]byte, inlineLen)
				copy(raw, slot[4:4+inlineLen])
				vals[i] = storage.BinaryValue(raw)
			} else {
				vOff := binary.LittleEndian.Uint32(slot[0:4])
				vLen := binary.LittleEndian.Uint32(slot[4:8])
				if int(vOff+vLen) > len(varArea) {
					return storage.Row{}, ErrFlatRecordTruncated
				}
				raw := make([]byte, vLen)
				copy(raw, varArea[vOff:vOff+vLen])
				vals[i] = storage.BinaryValue(raw)
			}

		case storage.TypeDecimal:
			var s string
			if slot[1] == 0xFF {
				inlineLen := int(slot[0])
				s = string(slot[4 : 4+inlineLen])
			} else {
				vOff := binary.LittleEndian.Uint32(slot[0:4])
				vLen := binary.LittleEndian.Uint32(slot[4:8])
				if int(vOff+vLen) > len(varArea) {
					return storage.Row{}, ErrFlatRecordTruncated
				}
				s = string(varArea[vOff : vOff+vLen])
			}
			vals[i] = storage.DecimalValue(s)

		default:
			vOff := binary.LittleEndian.Uint32(slot[0:4])
			vLen := binary.LittleEndian.Uint32(slot[4:8])
			if int(vOff+vLen) > len(varArea) {
				return storage.Row{}, ErrFlatRecordTruncated
			}
			s := string(varArea[vOff : vOff+vLen])
			vals[i] = storage.Value{Type: col.Type, SVal: s}
		}
	}

	return storage.Row{Values: vals}, nil
}
