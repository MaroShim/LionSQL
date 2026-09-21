package wire

import (
	"encoding/binary"
	"io"
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
)

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

// WriteReadyForQuery sends ReadyForQuery status ('I' idle).
func WriteReadyForQuery(w io.Writer) error {
	buf := []byte{MsgReadyForQuery, 0, 0, 0, 5, 'I'}
	_, err := w.Write(buf)
	return err
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

// WriteDataRow sends a single DataRow with string values.
func WriteDataRow(w io.Writer, values []string) error {
	payloadLen := 4 + 2
	for _, v := range values {
		payloadLen += 4 + len(v)
	}

	buf := make([]byte, 1+payloadLen)
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

// WriteError sends an ErrorResponse.
func WriteError(w io.Writer, message string) error {
	// S: Severity (ERROR), M: Message
	payloadLen := 4 + 1 + 5 + 1 + 1 + len(message) + 1 + 1
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
