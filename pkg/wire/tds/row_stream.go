package tds

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"sync"
	"unicode/utf8"

	"lionsql/pkg/storage"
)

const (
	tdsDefaultStreamCap = 16 * 1024
	tdsMaxPooledBufCap  = 128 * 1024
)

var tdsPayloadBufferPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, tdsDefaultStreamCap))
	},
}

func getTdsPayloadBuffer() *bytes.Buffer {
	buf := tdsPayloadBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

func putTdsPayloadBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	if buf.Cap() <= tdsMaxPooledBufCap {
		buf.Reset()
		tdsPayloadBufferPool.Put(buf)
	}
}

// tdsStreamWriter streams TDS token payloads in bounded packetSize chunks using pooled frame buffers,
// avoiding multi-megabyte full-result buffering on large SELECT queries.
type tdsStreamWriter struct {
	w          io.Writer
	pType      byte
	spid       uint16
	packetSize uint32
	maxPayload int
	packetID   byte
	buf        *bytes.Buffer
	wroteAny   bool
}

func newTdsStreamWriter(w io.Writer, pType byte, spid uint16, packetSize uint32) *tdsStreamWriter {
	if packetSize < 512 || packetSize > MaxTDSPacketSize {
		packetSize = DefaultPacketSize
	}
	return &tdsStreamWriter{
		w:          w,
		pType:      pType,
		spid:       spid,
		packetSize: packetSize,
		maxPayload: int(packetSize) - HeaderSize,
		packetID:   1,
		buf:        getTdsPayloadBuffer(),
	}
}

func (sess *ConnectionSession) newStreamWriter(w io.Writer, pType byte) *tdsStreamWriter {
	var spid uint16
	var pktSize uint32
	if sess != nil {
		spid = sess.SPID
		pktSize = sess.PacketSize
	}
	return newTdsStreamWriter(w, pType, spid, pktSize)
}

func (s *tdsStreamWriter) Buffer() *bytes.Buffer {
	return s.buf
}

func (s *tdsStreamWriter) writeFrame(chunk []byte, status byte) error {
	totalLen := HeaderSize + len(chunk)
	ptr, frame := getFrameBuffer(uint32(totalLen))
	frame[0] = s.pType
	frame[1] = status
	binary.BigEndian.PutUint16(frame[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(frame[4:6], s.spid)
	frame[6] = s.packetID
	frame[7] = 0
	copy(frame[HeaderSize:], chunk)

	_, err := s.w.Write(frame)
	putFrameBuffer(uint32(totalLen), ptr)
	s.packetID++
	s.wroteAny = true
	return err
}

// FlushIfReady emits complete maxPayload StatusNormal TDS frames when buffered tokens exceed maxPayload.
// Keeps at least 1 byte in s.buf until Close so the trailing packet carries StatusEOM.
func (s *tdsStreamWriter) FlushIfReady() error {
	for s.buf.Len() > s.maxPayload {
		raw := s.buf.Bytes()
		if err := s.writeFrame(raw[:s.maxPayload], StatusNormal); err != nil {
			return err
		}
		rem := copy(raw, raw[s.maxPayload:])
		s.buf.Truncate(rem)
	}
	return nil
}

// Close flushes remaining payload frames (with StatusEOM on the final packet) and returns pooled buffers.
func (s *tdsStreamWriter) Close() error {
	if s.buf == nil {
		return nil
	}
	payload := s.buf.Bytes()
	if len(payload) == 0 && !s.wroteAny {
		err := s.writeFrame(nil, StatusEOM)
		putTdsPayloadBuffer(s.buf)
		s.buf = nil
		return err
	}

	offset := 0
	var writeErr error
	for offset < len(payload) {
		end := offset + s.maxPayload
		status := byte(StatusNormal)
		if end >= len(payload) {
			end = len(payload)
			status = StatusEOM
		}
		if err := s.writeFrame(payload[offset:end], status); err != nil {
			writeErr = err
			break
		}
		offset = end
	}
	putTdsPayloadBuffer(s.buf)
	s.buf = nil
	return writeErr
}

// utf16ByteLen computes the exact UTF-16LE encoded byte length of s without allocations.
func utf16ByteLen(s string) int {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return len(s) * 2
	}
	n := 0
	for _, r := range s {
		if r >= 0x10000 && r <= 0x10FFFF {
			n += 4
		} else {
			n += 2
		}
	}
	return n
}

// writePLPUTF16LE writes a UTF-16LE string as a TDS PLP stream directly into buf with zero heap allocations.
func writePLPUTF16LE(buf *bytes.Buffer, s string) {
	const maxChunkBytes = 8000
	totalBytes := uint64(utf16ByteLen(s))

	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], totalBytes)
	buf.Write(lenBuf[:])

	if totalBytes == 0 {
		var zero [4]byte
		buf.Write(zero[:])
		return
	}

	var chunkHdr [4]byte
	start := 0
	for start < len(s) {
		chunkBytes := 0
		end := start
		for end < len(s) {
			r, size := utf8.DecodeRuneInString(s[end:])
			stepBytes := 2
			if r >= 0x10000 && r <= 0x10FFFF {
				stepBytes = 4
			}
			if chunkBytes+stepBytes > maxChunkBytes && chunkBytes > 0 {
				break
			}
			chunkBytes += stepBytes
			end += size
		}
		binary.LittleEndian.PutUint32(chunkHdr[:], uint32(chunkBytes))
		buf.Write(chunkHdr[:])
		WriteUTF16LEDirect(buf, s[start:end])
		start = end
	}

	var term [4]byte
	buf.Write(term[:])
}

// writePLPStringRaw writes an ASCII/single-byte string as a TDS PLP stream directly into buf without allocation.
func writePLPStringRaw(buf *bytes.Buffer, s string) {
	const maxChunkBytes = 8000
	totalBytes := uint64(len(s))

	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], totalBytes)
	buf.Write(lenBuf[:])

	var chunkHdr [4]byte
	for offset := 0; offset < len(s); offset += maxChunkBytes {
		end := offset + maxChunkBytes
		if end > len(s) {
			end = len(s)
		}
		binary.LittleEndian.PutUint32(chunkHdr[:], uint32(end-offset))
		buf.Write(chunkHdr[:])
		buf.WriteString(s[offset:end])
	}

	var term [4]byte
	buf.Write(term[:])
}

func isNilTdsValue(v any) bool {
	if v == nil {
		return true
	}
	switch sv := v.(type) {
	case storage.Value:
		return sv.IsNull
	case *storage.Value:
		return sv == nil || sv.IsNull
	}
	return false
}

// formatTdsValueAsBytes appends val's string form into dst without heap allocations for primitive types.
func formatTdsValueAsBytes(dst []byte, val any) []byte {
	switch v := val.(type) {
	case string:
		return append(dst, v...)
	case []byte:
		return append(dst, v...)
	case int:
		return strconv.AppendInt(dst, int64(v), 10)
	case int64:
		return strconv.AppendInt(dst, v, 10)
	case int32:
		return strconv.AppendInt(dst, int64(v), 10)
	case int16:
		return strconv.AppendInt(dst, int64(v), 10)
	case float64:
		return strconv.AppendFloat(dst, v, 'g', -1, 64)
	case float32:
		return strconv.AppendFloat(dst, float64(v), 'g', -1, 32)
	case bool:
		return strconv.AppendBool(dst, v)
	case storage.Value:
		if v.IsNull {
			return dst
		}
		switch v.Type {
		case storage.TypeInt:
			return strconv.AppendInt(dst, v.IVal, 10)
		case storage.TypeVarChar, storage.TypeJSON:
			return append(dst, v.SVal...)
		case storage.TypeBool:
			return strconv.AppendBool(dst, v.BVal)
		default:
			return append(dst, v.String()...)
		}
	case *storage.Value:
		if v == nil || v.IsNull {
			return dst
		}
		return formatTdsValueAsBytes(dst, *v)
	default:
		return fmt.Appendf(dst, "%v", val)
	}
}

// resolveRowSlots extracts column values into positional slice slots using O(1) slice indexing
// when rowVals is available, or single-pass case-insensitive map lookup when only row map exists.
func resolveRowSlots(cols []ColumnInfo, row map[string]any, rowVals []any, scratch []any) ([]any, bool) {
	hasNull := false
	if len(rowVals) >= len(cols) {
		slots := rowVals[:len(cols)]
		for _, v := range slots {
			if isNilTdsValue(v) {
				hasNull = true
				break
			}
		}
		return slots, hasNull
	}

	slots := scratch[:len(cols)]
	for i, col := range cols {
		var val any
		if row != nil {
			val = getRowValueCaseInsensitive(row, col.Name)
		}
		slots[i] = val
		if isNilTdsValue(val) {
			hasNull = true
		}
	}
	return slots, hasNull
}

var (
	tdsNullPLP    = []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	tdsNullVarLen = []byte{0xFF, 0xFF}
)

// WriteRowDirectValues appends a TDS ROW (0xD1) token directly into destBuf from positional slots.
func WriteRowDirectValues(destBuf *bytes.Buffer, cols []ColumnInfo, values []any) {
	destBuf.WriteByte(TokenRow)
	for i, col := range cols {
		var val any
		if i < len(values) {
			val = values[i]
		}
		if isNilTdsValue(val) {
			switch col.Type {
			case TypeIntN, TypeFltN, TypeBitN, TypeDateTimeN, TypeDateN, TypeTimeN, TypeDateTime2N, TypeDateTimeOffsetN, TypeGuid:
				destBuf.WriteByte(0)
			default:
				if col.IsPLP || col.Size == -1 {
					destBuf.Write(tdsNullPLP)
				} else {
					destBuf.Write(tdsNullVarLen)
				}
			}
			continue
		}
		writeColumnValue(destBuf, col, val)
	}
}

// WriteNBCRowDirectValues appends a TDS NBCROW (0xD2) token directly into destBuf from positional slots.
func WriteNBCRowDirectValues(destBuf *bytes.Buffer, cols []ColumnInfo, values []any) {
	destBuf.WriteByte(TokenNBCRow)

	bitmapLen := (len(cols) + 7) / 8
	var stackBitmap [16]byte
	var bitmap []byte
	if bitmapLen <= len(stackBitmap) {
		bitmap = stackBitmap[:bitmapLen]
		for i := range bitmap {
			bitmap[i] = 0
		}
	} else {
		bitmap = make([]byte, bitmapLen)
	}

	for i := range cols {
		var val any
		if i < len(values) {
			val = values[i]
		}
		if isNilTdsValue(val) {
			bitmap[i/8] |= 1 << uint(i%8)
		}
	}
	destBuf.Write(bitmap)

	for i := range cols {
		byteIdx := i / 8
		bitMask := byte(1 << uint(i%8))
		if bitmap[byteIdx]&bitMask != 0 {
			continue
		}
		var val any
		if i < len(values) {
			val = values[i]
		}
		writeColumnValue(destBuf, cols[i], val)
	}
}

// writeRowValuesDirect appends either NBCROW (0xD2) or ROW (0xD1) directly into destBuf without duplicate lookups.
func (sess *ConnectionSession) writeRowValuesDirect(destBuf *bytes.Buffer, cols []ColumnInfo, row map[string]any, rowVals []any, scratchVals []any) {
	if len(scratchVals) < len(cols) {
		scratchVals = make([]any, len(cols))
	}
	slots, hasNull := resolveRowSlots(cols, row, rowVals, scratchVals)
	if hasNull && sess != nil && sess.TDSVersion >= 0x73000000 {
		WriteNBCRowDirectValues(destBuf, cols, slots)
		return
	}
	WriteRowDirectValues(destBuf, cols, slots)
}

// streamQueryRows streams all result rows into stream, flushing complete TDS packets incrementally.
func (s *Server) streamQueryRows(stream *tdsStreamWriter, sess *ConnectionSession, cols []ColumnInfo, rows []map[string]any, values [][]any) error {
	numRows := len(rows)
	if len(values) > numRows {
		numRows = len(values)
	}
	var stackScratch [16]any
	var scratch []any
	if len(cols) <= len(stackScratch) {
		scratch = stackScratch[:len(cols)]
	} else {
		scratch = make([]any, len(cols))
	}

	buf := stream.Buffer()
	for rIdx := 0; rIdx < numRows; rIdx++ {
		var row map[string]any
		if rIdx < len(rows) {
			row = rows[rIdx]
		}
		var rowVals []any
		if rIdx < len(values) {
			rowVals = values[rIdx]
		}
		sess.writeRowValuesDirect(buf, cols, row, rowVals, scratch)
		if err := stream.FlushIfReady(); err != nil {
			return err
		}
	}
	return nil
}

// writeUTF16LEBytes writes UTF-8 encoded b as UTF-16LE into buf without allocating.
func writeUTF16LEBytes(buf *bytes.Buffer, b []byte) int {
	if len(b) == 0 {
		return 0
	}
	ascii := true
	for i := 0; i < len(b); i++ {
		if b[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		buf.Grow(len(b) * 2)
		for i := 0; i < len(b); i++ {
			buf.WriteByte(b[i])
			buf.WriteByte(0)
		}
		return len(b) * 2
	}
	buf.Grow(len(b) * 2)
	bytesWritten := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		b = b[size:]
		switch {
		case 0 <= r && r < 0xD800, 0xE000 <= r && r <= 0xFFFF:
			buf.WriteByte(byte(r))
			buf.WriteByte(byte(r >> 8))
			bytesWritten += 2
		case 0x10000 <= r && r <= 0x10FFFF:
			r1, r2 := utf16EncodeSurrogate(r)
			buf.WriteByte(byte(r1))
			buf.WriteByte(byte(r1 >> 8))
			buf.WriteByte(byte(r2))
			buf.WriteByte(byte(r2 >> 8))
			bytesWritten += 4
		default:
			buf.WriteByte(0xFD)
			buf.WriteByte(0xFF)
			bytesWritten += 2
		}
	}
	return bytesWritten
}

func utf16EncodeSurrogate(r rune) (rune, rune) {
	r -= 0x10000
	return 0xD800 + (r >> 10), 0xDC00 + (r & 0x3FF)
}

func writePLPBytesRaw(buf *bytes.Buffer, b []byte) {
	const maxChunkBytes = 8000
	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(b)))
	buf.Write(lenBuf[:])

	var chunkHdr [4]byte
	for offset := 0; offset < len(b); offset += maxChunkBytes {
		end := offset + maxChunkBytes
		if end > len(b) {
			end = len(b)
		}
		binary.LittleEndian.PutUint32(chunkHdr[:], uint32(end-offset))
		buf.Write(chunkHdr[:])
		buf.Write(b[offset:end])
	}
	var term [4]byte
	buf.Write(term[:])
}

func writePLPUTF16LEBytes(buf *bytes.Buffer, b []byte) {
	const maxChunkBytes = 8000
	ascii := true
	totalBytes := 0
	for i := 0; i < len(b); i++ {
		if b[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		totalBytes = len(b) * 2
	} else {
		tmp := b
		for len(tmp) > 0 {
			r, size := utf8.DecodeRune(tmp)
			tmp = tmp[size:]
			if r >= 0x10000 && r <= 0x10FFFF {
				totalBytes += 4
			} else {
				totalBytes += 2
			}
		}
	}

	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], uint64(totalBytes))
	buf.Write(lenBuf[:])

	if totalBytes == 0 {
		var zero [4]byte
		buf.Write(zero[:])
		return
	}

	var chunkHdr [4]byte
	start := 0
	for start < len(b) {
		chunkBytes := 0
		end := start
		for end < len(b) {
			r, size := utf8.DecodeRune(b[end:])
			stepBytes := 2
			if r >= 0x10000 && r <= 0x10FFFF {
				stepBytes = 4
			}
			if chunkBytes+stepBytes > maxChunkBytes && chunkBytes > 0 {
				break
			}
			chunkBytes += stepBytes
			end += size
		}
		binary.LittleEndian.PutUint32(chunkHdr[:], uint32(chunkBytes))
		buf.Write(chunkHdr[:])
		writeUTF16LEBytes(buf, b[start:end])
		start = end
	}

	var term [4]byte
	buf.Write(term[:])
}

// writeTdsTextColumn serializes TypeNVarChar/TypeVarChar/default values directly into buf with zero allocations on fast paths.
func writeTdsTextColumn(buf *bytes.Buffer, col ColumnInfo, val any, utf16 bool) {
	if s, ok := val.(string); ok {
		if col.IsPLP || col.Size == -1 {
			if utf16 {
				writePLPUTF16LE(buf, s)
			} else {
				writePLPStringRaw(buf, s)
			}
			return
		}
		if utf16 {
			startLen := buf.Len()
			buf.WriteByte(0)
			buf.WriteByte(0)
			nBytes := WriteUTF16LEDirect(buf, s)
			binary.LittleEndian.PutUint16(buf.Bytes()[startLen:startLen+2], uint16(nBytes))
		} else {
			var b [2]byte
			binary.LittleEndian.PutUint16(b[:], uint16(len(s)))
			buf.Write(b[:])
			buf.WriteString(s)
		}
		return
	}

	var stack [64]byte
	raw := formatTdsValueAsBytes(stack[:0], val)
	if col.IsPLP || col.Size == -1 {
		if utf16 {
			writePLPUTF16LEBytes(buf, raw)
		} else {
			writePLPBytesRaw(buf, raw)
		}
		return
	}
	if utf16 {
		startLen := buf.Len()
		buf.WriteByte(0)
		buf.WriteByte(0)
		nBytes := writeUTF16LEBytes(buf, raw)
		binary.LittleEndian.PutUint16(buf.Bytes()[startLen:startLen+2], uint16(nBytes))
	} else {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(len(raw)))
		buf.Write(b[:])
		buf.Write(raw)
	}
}
