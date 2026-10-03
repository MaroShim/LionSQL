package wire

import (
	"encoding/binary"
	"io"
	"strings"
	"sync"
)

// PostgreSQL Wire Protocol v3 backend message types
const (
	MsgAuth                 byte = 'R'
	MsgParameterStatus      byte = 'S'
	MsgReadyForQuery        byte = 'Z'
	MsgRowDescription       byte = 'T'
	MsgDataRow              byte = 'D'
	MsgCommandComplete      byte = 'C'
	MsgErrorResponse        byte = 'E'
	MsgParseComplete        byte = '1'
	MsgBindComplete         byte = '2'
	MsgCloseComplete        byte = '3'
	MsgNoData               byte = 'n'
	MsgParameterDescription byte = 't'
	MsgCopyInResponse       byte = 'G'
	MsgCopyOutResponse      byte = 'H'
	MsgCopyData             byte = 'd'
	MsgCopyDone             byte = 'c'
	MsgCopyFail             byte = 'f'
	MsgBackendKeyData       byte = 'K'
	MsgSimpleQuery          byte = 'Q'
)

// WriteBackendKeyData sends BackendKeyData ('K') with process ID and cancellation secret key.
func WriteBackendKeyData(w io.Writer, pid, secretKey uint32) error {
	buf := make([]byte, 13)
	buf[0] = MsgBackendKeyData
	binary.BigEndian.PutUint32(buf[1:5], 12)
	binary.BigEndian.PutUint32(buf[5:9], pid)
	binary.BigEndian.PutUint32(buf[9:13], secretKey)
	_, err := w.Write(buf)
	return err
}

// WriteParseComplete sends ParseComplete ('1') message.
func WriteParseComplete(w io.Writer) error {
	buf := []byte{MsgParseComplete, 0, 0, 0, 4}
	_, err := w.Write(buf)
	return err
}

// WriteBindComplete sends BindComplete ('2') message.
func WriteBindComplete(w io.Writer) error {
	buf := []byte{MsgBindComplete, 0, 0, 0, 4}
	_, err := w.Write(buf)
	return err
}

// WriteCloseComplete sends CloseComplete ('3') message.
func WriteCloseComplete(w io.Writer) error {
	buf := []byte{MsgCloseComplete, 0, 0, 0, 4}
	_, err := w.Write(buf)
	return err
}

// WriteNoData sends NoData ('n') message.
func WriteNoData(w io.Writer) error {
	buf := []byte{MsgNoData, 0, 0, 0, 4}
	_, err := w.Write(buf)
	return err
}

// WriteParameterDescription sends ParameterDescription ('t') with parameter OIDs.
func WriteParameterDescription(w io.Writer, oids []int32) error {
	payloadLen := 4 + 2 + len(oids)*4
	buf := make([]byte, 1+payloadLen)
	buf[0] = MsgParameterDescription
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint16(buf[5:7], uint16(len(oids)))

	offset := 7
	for _, oid := range oids {
		binary.BigEndian.PutUint32(buf[offset:], uint32(oid))
		offset += 4
	}
	_, err := w.Write(buf)
	return err
}

// WriteAuthOK sends AuthenticationOk message.
func WriteAuthOK(w io.Writer) error {
	buf := make([]byte, 9)
	buf[0] = MsgAuth
	binary.BigEndian.PutUint32(buf[1:5], 8)
	binary.BigEndian.PutUint32(buf[5:9], 0) // Auth OK
	_, err := w.Write(buf)
	return err
}

// WriteAuthCleartextPassword sends AuthenticationCleartextPassword message (code 3).
func WriteAuthCleartextPassword(w io.Writer) error {
	buf := make([]byte, 9)
	buf[0] = MsgAuth
	binary.BigEndian.PutUint32(buf[1:5], 8)
	binary.BigEndian.PutUint32(buf[5:9], 3) // CleartextPassword
	_, err := w.Write(buf)
	return err
}

// WriteAuthMD5Password sends AuthenticationMD5Password message (code 5) with a 4-byte random salt.
func WriteAuthMD5Password(w io.Writer, salt [4]byte) error {
	buf := make([]byte, 13)
	buf[0] = MsgAuth
	binary.BigEndian.PutUint32(buf[1:5], 12)
	binary.BigEndian.PutUint32(buf[5:9], 5) // MD5Password
	copy(buf[9:13], salt[:])
	_, err := w.Write(buf)
	return err
}

// WriteParameterStatus sends a ParameterStatus key-value pair.
func WriteParameterStatus(w io.Writer, key, val string) error {
	payloadLen := 4 + len(key) + 1 + len(val) + 1
	buf := make([]byte, 1+payloadLen)
	buf[0] = MsgParameterStatus
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))

	idx := 5
	copy(buf[idx:], key)
	idx += len(key)
	buf[idx] = 0
	idx++
	copy(buf[idx:], val)
	idx += len(val)
	buf[idx] = 0

	_, err := w.Write(buf)
	return err
}

// WriteReadyForQueryStatus sends ReadyForQuery message with explicit transaction status:
// 'I' = Idle (outside transaction)
// 'T' = In active transaction block
// 'E' = In failed transaction block
func WriteReadyForQueryStatus(w io.Writer, status byte) error {
	if status != 'I' && status != 'T' && status != 'E' {
		status = 'I'
	}
	buf := []byte{MsgReadyForQuery, 0, 0, 0, 5, status}
	_, err := w.Write(buf)
	return err
}

// WriteReadyForQuery sends ReadyForQuery status ('I' idle).
func WriteReadyForQuery(w io.Writer) error {
	return WriteReadyForQueryStatus(w, 'I')
}

// ColumnMeta holds metadata for a row column in RowDescription.
type ColumnMeta struct {
	Name     string
	DataType int32 // pg oid
}

// WriteRowDescription sends RowDescription message.
func WriteRowDescription(w io.Writer, cols []ColumnMeta) error {
	payloadLen := 4 + 2
	for _, c := range cols {
		payloadLen += len(c.Name) + 1 + 4 + 2 + 4 + 2 + 4 + 2
	}

	buf := make([]byte, 1+payloadLen)
	buf[0] = MsgRowDescription
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint16(buf[5:7], uint16(len(cols)))

	offset := 7
	for _, c := range cols {
		copy(buf[offset:], c.Name)
		offset += len(c.Name)
		buf[offset] = 0
		offset++

		binary.BigEndian.PutUint32(buf[offset:], 0) // table oid
		offset += 4
		binary.BigEndian.PutUint16(buf[offset:], 0) // column attr
		offset += 2
		binary.BigEndian.PutUint32(buf[offset:], uint32(c.DataType)) // type oid
		offset += 4
		binary.BigEndian.PutUint16(buf[offset:], 255) // type size
		offset += 2
		binary.BigEndian.PutUint32(buf[offset:], 0) // type modifier
		offset += 4
		binary.BigEndian.PutUint16(buf[offset:], 0) // format text
		offset += 2
	}

	_, err := w.Write(buf)
	return err
}

var packetBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 4096)
		return &b
	},
}

// WriteDataRow sends a single DataRow with string values, reusing pooled byte buffers.
func WriteDataRow(w io.Writer, values []string) error {
	payloadLen := 4 + 2
	for _, v := range values {
		payloadLen += 4 + len(v)
	}

	totalLen := 1 + payloadLen
	bp := packetBufferPool.Get().(*[]byte)
	var buf []byte
	if cap(*bp) >= totalLen {
		buf = (*bp)[:totalLen]
	} else {
		buf = make([]byte, totalLen)
	}

	buf[0] = MsgDataRow
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint16(buf[5:7], uint16(len(values)))

	offset := 7
	for _, v := range values {
		binary.BigEndian.PutUint32(buf[offset:], uint32(len(v)))
		offset += 4
		copy(buf[offset:], v)
		offset += len(v)
	}

	_, err := w.Write(buf)
	if cap(buf) <= 65536 {
		*bp = buf[:0]
		packetBufferPool.Put(bp)
	}
	return err
}

// WriteDataRowBytes sends a single DataRow with raw byte slices or nil (for NULL).
func WriteDataRowBytes(w io.Writer, values [][]byte) error {
	payloadLen := 4 + 2
	for _, v := range values {
		if v == nil {
			payloadLen += 4
		} else {
			payloadLen += 4 + len(v)
		}
	}

	totalLen := 1 + payloadLen
	bp := packetBufferPool.Get().(*[]byte)
	var buf []byte
	if cap(*bp) >= totalLen {
		buf = (*bp)[:totalLen]
	} else {
		buf = make([]byte, totalLen)
	}

	buf[0] = MsgDataRow
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint16(buf[5:7], uint16(len(values)))

	offset := 7
	for _, v := range values {
		if v == nil {
			binary.BigEndian.PutUint32(buf[offset:], 0xFFFFFFFF)
			offset += 4
		} else {
			binary.BigEndian.PutUint32(buf[offset:], uint32(len(v)))
			offset += 4
			copy(buf[offset:], v)
			offset += len(v)
		}
	}

	_, err := w.Write(buf)
	if cap(buf) <= 65536 {
		*bp = buf[:0]
		packetBufferPool.Put(bp)
	}
	return err
}

// WriteCommandComplete sends CommandComplete tag.
func WriteCommandComplete(w io.Writer, tag string) error {
	payloadLen := 4 + len(tag) + 1
	buf := make([]byte, 1+payloadLen)
	buf[0] = MsgCommandComplete
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	copy(buf[5:], tag)
	buf[1+payloadLen-1] = 0
	_, err := w.Write(buf)
	return err
}

// WriteErrorWithCode sends an ErrorResponse with Severity ('S'), SQLSTATE Code ('C'), and Message ('M').
func WriteErrorWithCode(w io.Writer, message string, code string) error {
	if code == "" {
		code = "XX000" // internal_error
	}
	// S: Severity (ERROR\0) + C: Code (code\0) + M: Message (message\0) + null terminator
	payloadLen := 4 + 1 + 5 + 1 + 1 + len(code) + 1 + 1 + len(message) + 1 + 1
	buf := make([]byte, 1+payloadLen)
	buf[0] = MsgErrorResponse
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))

	idx := 5
	buf[idx] = 'S'
	idx++
	copy(buf[idx:], "ERROR")
	idx += 5
	buf[idx] = 0
	idx++

	buf[idx] = 'C'
	idx++
	copy(buf[idx:], code)
	idx += len(code)
	buf[idx] = 0
	idx++

	buf[idx] = 'M'
	idx++
	copy(buf[idx:], message)
	idx += len(message)
	buf[idx] = 0
	idx++

	buf[idx] = 0 // terminating zero

	_, err := w.Write(buf)
	return err
}

// WriteError sends an ErrorResponse with automatic standard PostgreSQL SQLSTATE code.
func WriteError(w io.Writer, message string) error {
	code := "XX000"
	upper := strings.ToUpper(message)
	if strings.Contains(upper, "NOT FOUND") || strings.Contains(upper, "DOES NOT EXIST") {
		code = "42P01" // undefined_table / undefined_relation
	} else if strings.Contains(upper, "SYNTAX") {
		code = "42601" // syntax_error
	} else if strings.Contains(upper, "PERMISSION DENIED") {
		code = "42501" // insufficient_privilege
	} else if strings.Contains(upper, "CURRENT TRANSACTION IS ABORTED") {
		code = "25P02" // in_failed_sql_transaction
	} else if strings.Contains(upper, "TIMEOUT") {
		code = "57014" // query_canceled
	} else if strings.Contains(upper, "DUPLICATE") || strings.Contains(upper, "UNIQUE") {
		code = "23505" // unique_violation
	}
	return WriteErrorWithCode(w, message, code)
}

// WriteCopyInResponse sends CopyInResponse ('G') message.
func WriteCopyInResponse(w io.Writer, numCols int, format byte) error {
	payloadLen := 4 + 1 + 2 + numCols*2
	buf := make([]byte, 1+payloadLen)
	buf[0] = MsgCopyInResponse
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	buf[5] = format
	binary.BigEndian.PutUint16(buf[6:8], uint16(numCols))
	offset := 8
	for i := 0; i < numCols; i++ {
		binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(format))
		offset += 2
	}
	_, err := w.Write(buf)
	return err
}

// WriteCopyOutResponse sends CopyOutResponse ('H') message.
func WriteCopyOutResponse(w io.Writer, numCols int, format byte) error {
	payloadLen := 4 + 1 + 2 + numCols*2
	buf := make([]byte, 1+payloadLen)
	buf[0] = MsgCopyOutResponse
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	buf[5] = format
	binary.BigEndian.PutUint16(buf[6:8], uint16(numCols))
	offset := 8
	for i := 0; i < numCols; i++ {
		binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(format))
		offset += 2
	}
	_, err := w.Write(buf)
	return err
}

// WriteCopyData sends a CopyData ('d') chunk.
func WriteCopyData(w io.Writer, data []byte) error {
	payloadLen := 4 + len(data)
	buf := make([]byte, 1+payloadLen)
	buf[0] = MsgCopyData
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	copy(buf[5:], data)
	_, err := w.Write(buf)
	return err
}

// WriteCopyDone sends a CopyDone ('c') message.
func WriteCopyDone(w io.Writer) error {
	buf := []byte{MsgCopyDone, 0, 0, 0, 4}
	_, err := w.Write(buf)
	return err
}
