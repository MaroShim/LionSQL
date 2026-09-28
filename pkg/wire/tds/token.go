package tds

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// TDS Token Identifiers
const (
	TokenReturnStatus byte = 0x79
	TokenColMetaData  byte = 0x81
	TokenOrder        byte = 0xA9
	TokenError        byte = 0xAA
	TokenInfo         byte = 0xAB
	TokenReturnValue  byte = 0xAC
	TokenLoginAck     byte = 0xAD
	TokenRow          byte = 0xD1
	TokenNBCRow       byte = 0xD2
	TokenEnvChange    byte = 0xE3
	TokenDone         byte = 0xFD
	TokenDoneProc     byte = 0xFE
	TokenDoneInProc   byte = 0xFF
)

// TDS Data Types
const (
	TypeNull       byte = 0x1F
	TypeInt1       byte = 0x30
	TypeBit        byte = 0x32
	TypeInt2       byte = 0x34
	TypeInt4       byte = 0x38
	TypeDateTime4  byte = 0x3A
	TypeFlt4       byte = 0x3B
	TypeMoney4     byte = 0x7A
	TypeDateTime   byte = 0x3D
	TypeFlt8       byte = 0x3E
	TypeMoney      byte = 0x3C
	TypeInt8       byte = 0x7F
	TypeIntN       byte = 0x26
	TypeBitN       byte = 0x68
	TypeDecimalN   byte = 0x6A
	TypeNumericN   byte = 0x6C
	TypeFltN       byte = 0x6D
	TypeMoneyN     byte = 0x6E
	TypeDateTimeN  byte = 0x6F
	TypeDateN      byte = 0x28
	TypeTimeN      byte = 0x29
	TypeDateTime2N byte = 0x2A
	TypeBigVarChar byte = 0xA7
	TypeVarChar    byte = 0xA7
	TypeNVarChar   byte = 0xE7
	TypeBigBinary  byte = 0xAD
	TypeBigVarBin  byte = 0xA5
)

// Done Status Flags
const (
	DoneFinal    uint16 = 0x0000
	DoneMore     uint16 = 0x0001
	DoneError    uint16 = 0x0002
	DoneInXact   uint16 = 0x0004
	DoneCount    uint16 = 0x0010
	DoneAttn     uint16 = 0x0020
	DoneSrvError uint16 = 0x0100
)

// EnvChange Types
const (
	EnvChangeDatabase          byte = 1
	EnvChangeLanguage          byte = 2
	EnvChangeCharacterSet      byte = 3
	EnvChangePacketSize        byte = 4
	EnvChangeCollation         byte = 7
	EnvChangeBeginTransaction  byte = 8
	EnvChangeCommitTransaction byte = 9
	EnvChangeRollbackTransaction byte = 10
	EnvChangeRouting           byte = 20
)

// Prelogin Option Tokens
const (
	PreloginOptionVersion         byte = 0x00
	PreloginOptionEncryption      byte = 0x01
	PreloginOptionInstOpt         byte = 0x02
	PreloginOptionThreadID        byte = 0x03
	PreloginOptionMARS            byte = 0x04
	PreloginOptionTraceID         byte = 0x05
	PreloginOptionFedAuthRequired byte = 0x06
	PreloginOptionNonce           byte = 0x07
	PreloginOptionTerminator      byte = 0xFF
)

// Prelogin Encryption Flags
const (
	EncryptOff    byte = 0x00
	EncryptOn     byte = 0x01
	EncryptNotSup byte = 0x02
	EncryptReq    byte = 0x03
)

// Default Collation: SQL_Latin1_General_CP1_CI_AS (0x09 0x04 0xD0 0x00 0x34)
var DefaultCollation = []byte{0x09, 0x04, 0xD0, 0x00, 0x34}

// ColumnInfo holds metadata for a result set column.
type ColumnInfo struct {
	Name     string
	Type     byte
	Size     int
	Nullable bool
}

// BuildPreloginResponse generates a PRELOGIN reply packet payload.
func BuildPreloginResponse(encryptSupport byte) []byte {
	// Structure:
	// Header entries: [OptionType (1B)][Offset (2B BE)][Length (2B BE)]... [0xFF]
	// Followed by option data payloads at their respective offsets.

	var buf bytes.Buffer

	// Version: 6 bytes (Major 15, Minor 0, Build 2000 (0x07D0), SubBuild 0 (0x0000))
	versionData := []byte{15, 0, 0x07, 0xD0, 0x00, 0x00}
	// Encryption: 1 byte
	encryptData := []byte{encryptSupport}
	// MARS: 1 byte (0 = disabled)
	marsData := []byte{0x00}

	// Calculate offsets
	// Header: 3 options * 5 bytes + 1 terminator = 16 bytes
	headerLen := uint16(3*5 + 1)

	vOffset := headerLen
	vLen := uint16(len(versionData))

	eOffset := vOffset + vLen
	eLen := uint16(len(encryptData))

	mOffset := eOffset + eLen
	mLen := uint16(len(marsData))

	// Option 0: Version
	buf.WriteByte(PreloginOptionVersion)
	_ = binary.Write(&buf, binary.BigEndian, vOffset)
	_ = binary.Write(&buf, binary.BigEndian, vLen)

	// Option 1: Encryption
	buf.WriteByte(PreloginOptionEncryption)
	_ = binary.Write(&buf, binary.BigEndian, eOffset)
	_ = binary.Write(&buf, binary.BigEndian, eLen)

	// Option 2: MARS
	buf.WriteByte(PreloginOptionMARS)
	_ = binary.Write(&buf, binary.BigEndian, mOffset)
	_ = binary.Write(&buf, binary.BigEndian, mLen)

	// Terminator
	buf.WriteByte(PreloginOptionTerminator)

	// Payloads
	buf.Write(versionData)
	buf.Write(encryptData)
	buf.Write(marsData)

	return buf.Bytes()
}

// ParsePrelogin parses options from a PRELOGIN payload into a map of option types to raw bytes.
func ParsePrelogin(payload []byte) map[byte][]byte {
	options := make(map[byte][]byte)
	idx := 0
	type optHeader struct {
		optType byte
		offset  uint16
		length  uint16
	}
	var headers []optHeader
	for idx < len(payload) {
		optType := payload[idx]
		if optType == PreloginOptionTerminator {
			break
		}
		if idx+5 > len(payload) {
			break
		}
		offset := binary.BigEndian.Uint16(payload[idx+1 : idx+3])
		length := binary.BigEndian.Uint16(payload[idx+3 : idx+5])
		headers = append(headers, optHeader{optType: optType, offset: offset, length: length})
		idx += 5
	}
	for _, h := range headers {
		start := int(h.offset)
		end := start + int(h.length)
		if start >= 0 && end <= len(payload) && start <= end {
			options[h.optType] = payload[start:end]
		}
	}
	return options
}

// BuildLoginAck builds a LOGINACK token (0xAD) byte slice.
func BuildLoginAck(progName string, major, minor byte, build uint16) []byte {
	var buf bytes.Buffer
	buf.WriteByte(TokenLoginAck)

	var payload bytes.Buffer
	payload.WriteByte(0x01) // Interface: SQL Server

	// TDS Version: 7.4 (0x74000004)
	_ = binary.Write(&payload, binary.BigEndian, uint32(0x74000004))

	// ProgName: B_VARCHAR (1 byte character length + UTF-16LE)
	u16Name := EncodeUTF16LE(progName)
	payload.WriteByte(byte(len(progName)))
	payload.Write(u16Name)

	// Server Version
	payload.WriteByte(major)
	payload.WriteByte(minor)
	_ = binary.Write(&payload, binary.BigEndian, build)

	// Token length (uint16 LE)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())

	return buf.Bytes()
}

// BuildEnvChangeDatabase builds an ENVCHANGE token (0xE3) for database switch.
func BuildEnvChangeDatabase(newDB, oldDB string) []byte {
	var payload bytes.Buffer
	payload.WriteByte(EnvChangeDatabase)

	// NewValue: B_VARCHAR (1B char count + UTF-16LE)
	payload.WriteByte(byte(len(newDB)))
	payload.Write(EncodeUTF16LE(newDB))

	// OldValue: B_VARCHAR
	payload.WriteByte(byte(len(oldDB)))
	payload.Write(EncodeUTF16LE(oldDB))

	var buf bytes.Buffer
	buf.WriteByte(TokenEnvChange)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildEnvChangePacketSize builds an ENVCHANGE token (0xE3) for packet size change.
func BuildEnvChangePacketSize(newSize, oldSize string) []byte {
	var payload bytes.Buffer
	payload.WriteByte(EnvChangePacketSize)

	payload.WriteByte(byte(len(newSize)))
	payload.Write(EncodeUTF16LE(newSize))

	payload.WriteByte(byte(len(oldSize)))
	payload.Write(EncodeUTF16LE(oldSize))

	var buf bytes.Buffer
	buf.WriteByte(TokenEnvChange)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildDone builds a DONE (0xFD) or DONEINPROC (0xFF) token.
func BuildDone(tokenType byte, status uint16, curCmd uint16, rowCount uint64) []byte {
	var buf bytes.Buffer
	buf.WriteByte(tokenType)
	_ = binary.Write(&buf, binary.LittleEndian, status)
	_ = binary.Write(&buf, binary.LittleEndian, curCmd)
	_ = binary.Write(&buf, binary.LittleEndian, rowCount)
	return buf.Bytes()
}

// BuildDoneProc builds a DONEPROC token (0xFE) indicating stored procedure or RPC completion.
func BuildDoneProc(status uint16, curCmd uint16, rowCount uint64) []byte {
	return BuildDone(TokenDoneProc, status, curCmd, rowCount)
}

// BuildReturnStatus builds a RETURNSTATUS token (0x79) with 4-byte LE return value.
func BuildReturnStatus(val int32) []byte {
	buf := make([]byte, 5)
	buf[0] = TokenReturnStatus
	binary.LittleEndian.PutUint32(buf[1:5], uint32(val))
	return buf
}


// BuildReturnValueInt builds a RETURNVALUE token (0xAC) for integer output parameters (such as statement @handle).
func BuildReturnValueInt(paramOrdinal uint16, paramName string, val int32) []byte {
	var payload bytes.Buffer

	// ParamOrdinal: uint16 LE
	_ = binary.Write(&payload, binary.LittleEndian, paramOrdinal)

	// ParamName: B_VARCHAR (1 byte char length + UTF-16LE characters)
	if paramName != "" {
		payload.WriteByte(byte(len(paramName)))
		payload.Write(EncodeUTF16LE(paramName))
	} else {
		payload.WriteByte(0)
	}

	// StatusFlags (1B): 0x01 = Output parameter
	payload.WriteByte(0x01)

	// UserType (4B LE): 0
	_ = binary.Write(&payload, binary.LittleEndian, uint32(0))

	// Flags (2B LE): 0
	_ = binary.Write(&payload, binary.LittleEndian, uint16(0))

	// TypeInfo: TypeInt4 (0x38), fixed length 4 bytes
	payload.WriteByte(TypeInt4)

	// Value: int32 LE
	_ = binary.Write(&payload, binary.LittleEndian, val)

	var buf bytes.Buffer
	buf.WriteByte(TokenReturnValue)
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildError builds an ERROR token (0xAA).
func BuildError(number int32, state byte, class byte, msg, serverName, procName string, lineNum int32) []byte {
	var payload bytes.Buffer

	_ = binary.Write(&payload, binary.LittleEndian, number)
	payload.WriteByte(state)
	payload.WriteByte(class) // Severity

	// MsgText: US_VARCHAR (uint16 char length + UTF-16LE)
	_ = binary.Write(&payload, binary.LittleEndian, uint16(len(msg)))
	payload.Write(EncodeUTF16LE(msg))

	// ServerName: B_VARCHAR
	payload.WriteByte(byte(len(serverName)))
	payload.Write(EncodeUTF16LE(serverName))

	// ProcName: B_VARCHAR
	payload.WriteByte(byte(len(procName)))
	payload.Write(EncodeUTF16LE(procName))

	// LineNumber: int32 LE (TDS 7.2+)
	_ = binary.Write(&payload, binary.LittleEndian, lineNum)

	var buf bytes.Buffer
	buf.WriteByte(TokenError)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildInfo builds an INFO token (0xAB).
func BuildInfo(number int32, state byte, class byte, msg, serverName, procName string, lineNum int32) []byte {
	var payload bytes.Buffer

	_ = binary.Write(&payload, binary.LittleEndian, number)
	payload.WriteByte(state)
	payload.WriteByte(class)

	_ = binary.Write(&payload, binary.LittleEndian, uint16(len(msg)))
	payload.Write(EncodeUTF16LE(msg))

	payload.WriteByte(byte(len(serverName)))
	payload.Write(EncodeUTF16LE(serverName))

	payload.WriteByte(byte(len(procName)))
	payload.Write(EncodeUTF16LE(procName))

	_ = binary.Write(&payload, binary.LittleEndian, lineNum)

	var buf bytes.Buffer
	buf.WriteByte(TokenInfo)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildColMetaData builds a COLMETADATA token (0x81).
func BuildColMetaData(cols []ColumnInfo) []byte {
	var buf bytes.Buffer
	buf.WriteByte(TokenColMetaData)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(cols)))

	for _, col := range cols {
		// UserType: uint32 LE (0)
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0))

		// Flags: uint16 LE (0x0001 if nullable)
		var flags uint16
		if col.Nullable {
			flags |= 0x0001
		}
		_ = binary.Write(&buf, binary.LittleEndian, flags)

		// TypeInfo
		buf.WriteByte(col.Type)

		switch col.Type {
		case TypeIntN:
			// MaxLength: 1B (e.g. 8 bytes for int64/int4)
			buf.WriteByte(byte(col.Size))
		case TypeFltN:
			// MaxLength: 1B (e.g. 8 bytes)
			buf.WriteByte(8)
		case TypeBitN:
			// MaxLength: 1B (1)
			buf.WriteByte(1)
		case TypeDateTimeN:
			// MaxLength: 1B (8)
			buf.WriteByte(8)
		case TypeNVarChar:
			// MaxLength: uint16 LE (e.g. 8000 or 0xFFFF)
			maxLen := uint16(col.Size * 2)
			if col.Size <= 0 || col.Size > 4000 {
				maxLen = 8000
			}
			_ = binary.Write(&buf, binary.LittleEndian, maxLen)
			buf.Write(DefaultCollation)
		case TypeVarChar:
			maxLen := uint16(col.Size)
			if col.Size <= 0 || col.Size > 8000 {
				maxLen = 8000
			}
			_ = binary.Write(&buf, binary.LittleEndian, maxLen)
			buf.Write(DefaultCollation)
		default:
			// Fallback to NVarChar
			_ = binary.Write(&buf, binary.LittleEndian, uint16(8000))
			buf.Write(DefaultCollation)
		}

		// ColName: B_VARCHAR (1B char count + UTF-16LE)
		buf.WriteByte(byte(len(col.Name)))
		buf.Write(EncodeUTF16LE(col.Name))
	}

	return buf.Bytes()
}

// BuildRow builds a ROW token (0xD1) for a row of values matching cols.
func BuildRow(cols []ColumnInfo, values []any) []byte {
	var buf bytes.Buffer
	buf.WriteByte(TokenRow)

	for i, col := range cols {
		var val any
		if i < len(values) {
			val = values[i]
		}

		if val == nil {
			switch col.Type {
			case TypeIntN, TypeFltN, TypeBitN, TypeDateTimeN:
				buf.WriteByte(0) // Length 0 indicates NULL
			case TypeNVarChar, TypeVarChar:
				_ = binary.Write(&buf, binary.LittleEndian, uint16(0xFFFF)) // 0xFFFF indicates NULL
			default:
				_ = binary.Write(&buf, binary.LittleEndian, uint16(0xFFFF))
			}
			continue
		}

		switch col.Type {
		case TypeIntN:
			intVal := toInt64(val)
			if col.Size == 1 {
				buf.WriteByte(1)
				buf.WriteByte(byte(intVal))
			} else if col.Size == 2 {
				buf.WriteByte(2)
				_ = binary.Write(&buf, binary.LittleEndian, int16(intVal))
			} else if col.Size == 4 {
				buf.WriteByte(4)
				_ = binary.Write(&buf, binary.LittleEndian, int32(intVal))
			} else {
				buf.WriteByte(8)
				_ = binary.Write(&buf, binary.LittleEndian, intVal)
			}

		case TypeFltN:
			fltVal := toFloat64(val)
			buf.WriteByte(8)
			_ = binary.Write(&buf, binary.LittleEndian, fltVal)

		case TypeBitN:
			bitVal := toBool(val)
			buf.WriteByte(1)
			if bitVal {
				buf.WriteByte(1)
			} else {
				buf.WriteByte(0)
			}

		case TypeDateTimeN:
			// Encode as DATETIME (4 bytes days since 1900-01-01, 4 bytes 300ths of second)
			t := toTime(val)
			days, ticks := encodeDateTime(t)
			buf.WriteByte(8)
			_ = binary.Write(&buf, binary.LittleEndian, days)
			_ = binary.Write(&buf, binary.LittleEndian, ticks)

		case TypeNVarChar:
			strVal := fmt.Sprintf("%v", val)
			u16 := EncodeUTF16LE(strVal)
			_ = binary.Write(&buf, binary.LittleEndian, uint16(len(u16)))
			buf.Write(u16)

		case TypeVarChar:
			strVal := fmt.Sprintf("%v", val)
			_ = binary.Write(&buf, binary.LittleEndian, uint16(len(strVal)))
			buf.WriteString(strVal)

		default:
			strVal := fmt.Sprintf("%v", val)
			u16 := EncodeUTF16LE(strVal)
			_ = binary.Write(&buf, binary.LittleEndian, uint16(len(u16)))
			buf.Write(u16)
		}
	}

	return buf.Bytes()
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

func toBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case int, int64:
		return toInt64(v) != 0
	case string:
		return strings.EqualFold(b, "true") || b == "1"
	default:
		return false
	}
}

func toTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		parsed, err := time.Parse(time.RFC3339, t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse("2006-01-02 15:04:05", t)
		if err == nil {
			return parsed
		}
		return time.Now()
	default:
		return time.Now()
	}
}

var epoch1900 = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

func encodeDateTime(t time.Time) (int32, uint32) {
	tUTC := t.UTC()
	days := int32(tUTC.Sub(epoch1900).Hours() / 24)
	midnight := time.Date(tUTC.Year(), tUTC.Month(), tUTC.Day(), 0, 0, 0, 0, time.UTC)
	ms := tUTC.Sub(midnight).Milliseconds()
	// Ticks: 300ths of a second (3.3333 ms per tick)
	ticks := uint32(math.Round(float64(ms) * 300.0 / 1000.0))
	return days, ticks
}
