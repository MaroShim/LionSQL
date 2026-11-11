package wire

import (
	"encoding/binary"
	"io"
	"log"
	"math"
	"strconv"
	"sync"

	"lionsql/pkg/storage"
)

const (
	pgStreamFlushBytes = 16 * 1024
	pgStreamFlushRows  = 512
	pgMaxPooledBufCap  = 128 * 1024
)

var pgChunkBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, pgStreamFlushBytes)
		return &b
	},
}

func getPgChunkBuf() *[]byte {
	bp := pgChunkBufPool.Get().(*[]byte)
	*bp = (*bp)[:0]
	return bp
}

func putPgChunkBuf(bp *[]byte) {
	if bp == nil {
		return
	}
	if cap(*bp) <= pgMaxPooledBufCap {
		*bp = (*bp)[:0]
		pgChunkBufPool.Put(bp)
	}
}

// appendColumnValueFast appends a formatted PostgreSQL column value directly into dst.
// Returns (newDst, handled, isNull). When handled is false, callers fall back to legacy formatColumnValue.
func appendColumnValueFast(dst []byte, val any, oid int32, isBinary bool) ([]byte, bool, bool) {
	if val == nil {
		return dst, true, true
	}

	if !isBinary {
		switch v := val.(type) {
		case string:
			return append(dst, v...), true, false
		case []byte:
			// Delegate []byte to legacy formatColumnValue when OID requires special hex escaping.
			if oid == 17 {
				return dst, false, false
			}
			return append(dst, v...), true, false
		case int:
			return strconv.AppendInt(dst, int64(v), 10), true, false
		case int64:
			return strconv.AppendInt(dst, v, 10), true, false
		case int32:
			return strconv.AppendInt(dst, int64(v), 10), true, false
		case int16:
			return strconv.AppendInt(dst, int64(v), 10), true, false
		case int8:
			return strconv.AppendInt(dst, int64(v), 10), true, false
		case uint:
			return strconv.AppendUint(dst, uint64(v), 10), true, false
		case uint64:
			return strconv.AppendUint(dst, v, 10), true, false
		case uint32:
			return strconv.AppendUint(dst, uint64(v), 10), true, false
		case uint16:
			return strconv.AppendUint(dst, uint64(v), 10), true, false
		case float64:
			return strconv.AppendFloat(dst, v, 'g', -1, 64), true, false
		case float32:
			return strconv.AppendFloat(dst, float64(v), 'g', -1, 32), true, false
		case bool:
			return strconv.AppendBool(dst, v), true, false
		case storage.Value:
			return appendStorageValueText(dst, v)
		case *storage.Value:
			if v == nil {
				return dst, true, true
			}
			return appendStorageValueText(dst, *v)
		default:
			return dst, false, false
		}
	}

	// Binary format fast paths (preserve exact PG binary framing per OID).
	switch v := val.(type) {
	case storage.Value:
		if v.IsNull {
			return dst, true, true
		}
		return appendStorageValueBinary(dst, v, oid)
	case *storage.Value:
		if v == nil || v.IsNull {
			return dst, true, true
		}
		return appendStorageValueBinary(dst, *v, oid)
	case int:
		return appendBinaryInt(dst, int64(v), oid)
	case int64:
		return appendBinaryInt(dst, v, oid)
	case int32:
		return appendBinaryInt(dst, int64(v), oid)
	case int16:
		return appendBinaryInt(dst, int64(v), oid)
	case float64:
		return appendBinaryFloat(dst, v, oid)
	case float32:
		return appendBinaryFloat(dst, float64(v), oid)
	case bool:
		if oid == 16 {
			if v {
				return append(dst, 1), true, false
			}
			return append(dst, 0), true, false
		}
		return dst, false, false
	case string:
		switch oid {
		case 25, 1043, 19, 1042:
			return append(dst, v...), true, false
		}
		return dst, false, false
	default:
		return dst, false, false
	}
}

func appendStorageValueText(dst []byte, v storage.Value) ([]byte, bool, bool) {
	if v.IsNull {
		return dst, true, true
	}
	switch v.Type {
	case storage.TypeInt:
		return strconv.AppendInt(dst, v.IVal, 10), true, false
	case storage.TypeFloat:
		return strconv.AppendFloat(dst, v.FVal, 'f', 6, 64), true, false
	case storage.TypeVarChar, storage.TypeJSON:
		return append(dst, v.SVal...), true, false
	case storage.TypeBool:
		return strconv.AppendBool(dst, v.BVal), true, false
	default:
		return dst, false, false
	}
}

func appendStorageValueBinary(dst []byte, v storage.Value, oid int32) ([]byte, bool, bool) {
	switch v.Type {
	case storage.TypeInt:
		return appendBinaryInt(dst, v.IVal, oid)
	case storage.TypeFloat:
		return appendBinaryFloat(dst, v.FVal, oid)
	case storage.TypeBool:
		if oid == 16 {
			if v.BVal {
				return append(dst, 1), true, false
			}
			return append(dst, 0), true, false
		}
		return dst, false, false
	case storage.TypeVarChar:
		switch oid {
		case 25, 1043, 19, 1042:
			return append(dst, v.SVal...), true, false
		}
		return dst, false, false
	default:
		return dst, false, false
	}
}

func appendBinaryInt(dst []byte, n int64, oid int32) ([]byte, bool, bool) {
	switch oid {
	case 21: // INT2
		return binary.BigEndian.AppendUint16(dst, uint16(int16(n))), true, false
	case 23: // INT4
		return binary.BigEndian.AppendUint32(dst, uint32(int32(n))), true, false
	case 20: // INT8
		return binary.BigEndian.AppendUint64(dst, uint64(n)), true, false
	default:
		return dst, false, false
	}
}

func appendBinaryFloat(dst []byte, f float64, oid int32) ([]byte, bool, bool) {
	switch oid {
	case 700: // FLOAT4
		return binary.BigEndian.AppendUint32(dst, math.Float32bits(float32(f))), true, false
	case 701: // FLOAT8
		return binary.BigEndian.AppendUint64(dst, math.Float64bits(f)), true, false
	default:
		return dst, false, false
	}
}

// appendDataRowFromRow encodes a single PostgreSQL DataRow ('D') directly into dst without per-cell allocations.
func appendDataRowFromRow(dst []byte, colNames []string, colOIDs []int32, formats []int16, row map[string]any, rowVals []any) []byte {
	msgStart := len(dst)
	dst = append(dst, MsgDataRow, 0, 0, 0, 0, 0, 0)
	binary.BigEndian.PutUint16(dst[msgStart+5:msgStart+7], uint16(len(colNames)))

	singleFormatAllBinary := len(formats) == 1 && formats[0] == 1
	hasPerColFormats := len(formats) > 1
	hasVals := len(rowVals) > 0

	for i, colName := range colNames {
		isBinary := singleFormatAllBinary
		if !isBinary && hasPerColFormats && i < len(formats) && formats[i] == 1 {
			isBinary = true
		}

		var val any
		if hasVals && i < len(rowVals) {
			val = rowVals[i]
		} else if row != nil {
			val = getRowValue(row, colName)
		}

		var oid int32
		if i < len(colOIDs) {
			oid = colOIDs[i]
		}

		lenPos := len(dst)
		dst = append(dst, 0, 0, 0, 0)
		valStart := len(dst)

		newDst, handled, isNull := appendColumnValueFast(dst, val, oid, isBinary)
		if handled {
			dst = newDst
			if isNull {
				dst[lenPos] = 0xFF
				dst[lenPos+1] = 0xFF
				dst[lenPos+2] = 0xFF
				dst[lenPos+3] = 0xFF
				continue
			}
		} else {
			raw := formatColumnValue(val, oid, isBinary)
			if raw == nil {
				dst[lenPos] = 0xFF
				dst[lenPos+1] = 0xFF
				dst[lenPos+2] = 0xFF
				dst[lenPos+3] = 0xFF
				continue
			}
			dst = append(dst, raw...)
		}

		colLen := len(dst) - valStart
		binary.BigEndian.PutUint32(dst[lenPos:valStart], uint32(colLen))
	}

	payloadLen := len(dst) - (msgStart + 1)
	binary.BigEndian.PutUint32(dst[msgStart+1:msgStart+5], uint32(payloadLen))
	return dst
}

// writePgDataRows streams query rows in bounded 16KB / 512-row pooled chunks directly to w.
func writePgDataRows(w io.Writer, colNames []string, colOIDs []int32, formats []int16, rows []map[string]any, rowValsSlice [][]any, devMode bool) error {
	if len(rows) == 0 && len(rowValsSlice) == 0 {
		return nil
	}
	numRows := len(rows)
	if len(rowValsSlice) > numRows {
		numRows = len(rowValsSlice)
	}

	bp := getPgChunkBuf()
	buf := *bp
	rowsInChunk := 0

	var devRowBytes [][]byte
	if devMode {
		devRowBytes = make([][]byte, len(colNames))
	}

	for rIdx := 0; rIdx < numRows; rIdx++ {
		var row map[string]any
		if rIdx < len(rows) {
			row = rows[rIdx]
		}
		var rowVals []any
		if rIdx < len(rowValsSlice) {
			rowVals = rowValsSlice[rIdx]
		}

		if devMode {
			for i, colName := range colNames {
				isBinary := (len(formats) == 1 && formats[0] == 1) || (i < len(formats) && formats[i] == 1)
				var val any
				if i < len(rowVals) {
					val = rowVals[i]
				} else {
					val = getRowValue(row, colName)
				}
				var oid int32
				if i < len(colOIDs) {
					oid = colOIDs[i]
				}
				devRowBytes[i] = formatColumnValue(val, oid, isBinary)
			}
			log.Printf("[DEV][PGWire][ExecuteDataRows] cols=%v colOIDs=%v formats=%v rowBytes=%+v", colNames, colOIDs, formats, devRowBytes)
		}

		buf = appendDataRowFromRow(buf, colNames, colOIDs, formats, row, rowVals)
		rowsInChunk++

		if len(buf) >= pgStreamFlushBytes || rowsInChunk >= pgStreamFlushRows {
			if _, err := w.Write(buf); err != nil {
				*bp = buf
				putPgChunkBuf(bp)
				return err
			}
			buf = buf[:0]
			rowsInChunk = 0
		}
	}

	var writeErr error
	if len(buf) > 0 {
		_, writeErr = w.Write(buf)
	}
	*bp = buf
	putPgChunkBuf(bp)
	return writeErr
}
