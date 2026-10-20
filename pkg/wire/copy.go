package wire

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
)

// CopyDirection indicates whether data flows into or out of the server.
type CopyDirection int

const (
	CopyIn CopyDirection = iota // FROM STDIN
	CopyOut                     // TO STDOUT
)

// CopyFormat indicates the serialization format for streamed rows.
type CopyFormat int

const (
	FormatText CopyFormat = iota // Tab-delimited (default Postgres format)
	FormatCSV                    // Comma-separated
)

// CopyStmt holds parsed parameters of a COPY command.
type CopyStmt struct {
	Direction CopyDirection
	TableName string
	Columns   []string
	Format    CopyFormat
	Delimiter rune
	Header    bool
	NullStr   string
}

// ParseCopyStmt parses a PostgreSQL COPY command string.
func ParseCopyStmt(query string) (*CopyStmt, error) {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.TrimRight(trimmed, ";")
	trimmed = strings.TrimSpace(trimmed)

	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "COPY ") {
		return nil, fmt.Errorf("not a COPY statement")
	}

	stmt := &CopyStmt{
		Format:    FormatText,
		Delimiter: '\t',
		NullStr:   "\\N",
	}

	rest := strings.TrimSpace(trimmed[4:])

	// Check direction: FROM STDIN vs TO STDOUT
	var dirPart string
	upperRest := strings.ToUpper(rest)

	fromIdx := strings.Index(upperRest, " FROM STDIN")
	toIdx := strings.Index(upperRest, " TO STDOUT")

	if fromIdx != -1 {
		stmt.Direction = CopyIn
		dirPart = rest[:fromIdx]
		rest = rest[fromIdx+len(" FROM STDIN"):]
	} else if toIdx != -1 {
		stmt.Direction = CopyOut
		dirPart = rest[:toIdx]
		rest = rest[toIdx+len(" TO STDOUT"):]
	} else {
		// Also support lowercase/mixed
		if idx := strings.Index(upperRest, " FROM "); idx != -1 && strings.Contains(strings.ToUpper(rest[idx:]), "STDIN") {
			stmt.Direction = CopyIn
			dirPart = rest[:idx]
			rest = rest[idx+6:]
			if sIdx := strings.Index(strings.ToUpper(rest), "STDIN"); sIdx != -1 {
				rest = rest[sIdx+5:]
			}
		} else if idx := strings.Index(upperRest, " TO "); idx != -1 && strings.Contains(strings.ToUpper(rest[idx:]), "STDOUT") {
			stmt.Direction = CopyOut
			dirPart = rest[:idx]
			rest = rest[idx+4:]
			if sIdx := strings.Index(strings.ToUpper(rest), "STDOUT"); sIdx != -1 {
				rest = rest[sIdx+6:]
			}
		} else {
			return nil, fmt.Errorf("unsupported COPY direction (must be FROM STDIN or TO STDOUT)")
		}
	}

	// Parse table name and optional column list from dirPart
	dirPart = strings.TrimSpace(dirPart)
	if strings.Contains(dirPart, "(") && strings.Contains(dirPart, ")") {
		openParen := strings.Index(dirPart, "(")
		closeParen := strings.LastIndex(dirPart, ")")
		tableName := strings.TrimSpace(dirPart[:openParen])
		colsStr := dirPart[openParen+1 : closeParen]

		stmt.TableName = cleanTableName(tableName)
		for _, col := range strings.Split(colsStr, ",") {
			c := strings.TrimSpace(col)
			c = cleanIdentifier(c)
			if c != "" {
				stmt.Columns = append(stmt.Columns, c)
			}
		}
	} else {
		stmt.TableName = cleanTableName(dirPart)
	}

	if stmt.TableName == "" {
		return nil, fmt.Errorf("missing table name in COPY statement")
	}

	// Parse options in rest
	upperOptions := strings.ToUpper(strings.TrimSpace(rest))
	if strings.Contains(upperOptions, "CSV") || strings.Contains(upperOptions, "FORMAT CSV") {
		stmt.Format = FormatCSV
		stmt.Delimiter = ','
		stmt.NullStr = ""
	}
	if strings.Contains(upperOptions, "HEADER") {
		stmt.Header = true
	}

	// Custom delimiter
	if dIdx := strings.Index(upperOptions, "DELIMITER "); dIdx != -1 {
		dSub := strings.TrimSpace(rest[dIdx+10:])
		if len(dSub) > 1 && (dSub[0] == 'E' || dSub[0] == 'e') && (dSub[1] == '\'' || dSub[1] == '"') {
			dSub = dSub[1:]
		}
		var delimStr string
		if len(dSub) > 0 && (dSub[0] == '\'' || dSub[0] == '"') {
			quote := dSub[0]
			closeIdx := strings.IndexByte(dSub[1:], quote)
			if closeIdx != -1 {
				delimStr = dSub[1 : closeIdx+1]
			} else {
				delimStr = dSub[1:]
			}
		} else {
			endIdx := strings.IndexAny(dSub, " \t\r\n,);")
			if endIdx != -1 {
				delimStr = dSub[:endIdx]
			} else {
				delimStr = dSub
			}
		}
		if delimStr == "\\t" {
			stmt.Delimiter = '\t'
		} else if delimStr == "\\n" {
			stmt.Delimiter = '\n'
		} else if len(delimStr) > 0 {
			stmt.Delimiter = []rune(delimStr)[0]
		}
	}

	// Custom NULL representation
	if nIdx := strings.Index(upperOptions, "NULL "); nIdx != -1 {
		nSub := strings.TrimSpace(rest[nIdx+5:])
		if strings.HasPrefix(strings.ToUpper(nSub), "AS ") {
			nSub = strings.TrimSpace(nSub[3:])
		}
		var nullStr string
		if len(nSub) > 0 && (nSub[0] == '\'' || nSub[0] == '"') {
			quote := nSub[0]
			closeIdx := strings.IndexByte(nSub[1:], quote)
			if closeIdx != -1 {
				nullStr = nSub[1 : closeIdx+1]
			} else {
				nullStr = nSub[1:]
			}
		} else {
			endIdx := strings.IndexAny(nSub, " \t\r\n,);")
			if endIdx != -1 {
				nullStr = nSub[:endIdx]
			} else {
				nullStr = nSub
			}
		}
		stmt.NullStr = nullStr
	}

	return stmt, nil
}

func cleanTableName(s string) string {
	s = strings.TrimSpace(s)
	// Handle [dbo].[table] or "dbo"."table"
	if strings.Contains(s, ".") {
		parts := strings.Split(s, ".")
		s = parts[len(parts)-1]
	}
	return cleanIdentifier(s)
}

func cleanIdentifier(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "[]\"'`")
	return strings.TrimSpace(s)
}

func (s *Server) writeReady(conn net.Conn, sessID string) error {
	if s.coordinator != nil && sessID != "" {
		return WriteReadyForQueryStatus(conn, s.coordinator.GetSessionTxStatus(sessID))
	}
	return WriteReadyForQuery(conn)
}

func (s *Server) handleCopy(conn net.Conn, query string, sessID string) {
	stmt, err := ParseCopyStmt(query)
	if err != nil {
		_ = WriteError(conn, err.Error())
		_ = s.writeReady(conn, sessID)
		return
	}

	if stmt.Direction == CopyIn {
		s.handleCopyIn(conn, stmt, sessID)
	} else {
		s.handleCopyOut(conn, stmt, sessID)
	}
}

func (s *Server) handleCopyIn(conn net.Conn, stmt *CopyStmt, sessID string) {
	// 1. Locate target table schema
	tbl, err := s.engine.GetTable(stmt.TableName)
	if err != nil {
		tbl, err = s.engine.GetTable(strings.ToLower(stmt.TableName))
	}
	if err != nil {
		tables, _ := s.engine.ListTables()
		for _, t := range tables {
			if strings.EqualFold(t.Name, stmt.TableName) {
				tbl = t
				err = nil
				break
			}
		}
	}
	if err != nil || tbl == nil {
		_ = WriteError(conn, fmt.Sprintf("relation %q does not exist", stmt.TableName))
		_ = s.writeReady(conn, sessID)
		return
	}

	// 2. Resolve columns
	var targetCols []storage.Column
	if len(stmt.Columns) > 0 {
		for _, colName := range stmt.Columns {
			col, _, found := tbl.FindColumn(colName)
			if !found {
				// Check case-insensitive match
				for _, c := range tbl.Columns {
					if strings.EqualFold(c.Name, colName) {
						col = &c
						found = true
						break
					}
				}
			}
			if !found {
				_ = WriteError(conn, fmt.Sprintf("column %q of relation %q does not exist", colName, tbl.Name))
				_ = s.writeReady(conn, sessID)
				return
			}
			targetCols = append(targetCols, *col)
		}
	} else {
		targetCols = tbl.Columns
	}

	// 3. Send CopyInResponse ('G')
	if err := WriteCopyInResponse(conn, len(targetCols), 0); err != nil {
		return
	}

	// 4. Stream CopyData ('d') packets until CopyDone ('c') or CopyFail ('f')
	var dataBuf bytes.Buffer
	for {
		msgHeader := make([]byte, 5)
		if _, err := io.ReadFull(conn, msgHeader); err != nil {
			return
		}

		msgType := msgHeader[0]
		msgLen := binary.BigEndian.Uint32(msgHeader[1:5])
		if msgLen < 4 {
			return
		}
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		if msgType == MsgCopyData {
			dataBuf.Write(payload)
		} else if msgType == MsgCopyDone {
			break
		} else if msgType == MsgCopyFail {
			errMsg := string(bytes.TrimRight(payload, "\x00"))
			_ = WriteError(conn, fmt.Sprintf("COPY from stdin cancelled: %s", errMsg))
			_ = s.writeReady(conn, sessID)
			return
		} else {
			break
		}
	}

	// 5. Parse streamed data into rows
	var rows [][]string
	if stmt.Format == FormatCSV {
		r := csv.NewReader(&dataBuf)
		r.Comma = stmt.Delimiter
		r.FieldsPerRecord = -1
		parsed, err := r.ReadAll()
		if err != nil {
			_ = WriteError(conn, fmt.Sprintf("malformed CSV stream: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
		if stmt.Header && len(parsed) > 0 {
			rows = parsed[1:]
		} else {
			rows = parsed
		}
	} else {
		// Tab-separated or delimited text format
		scanner := bufio.NewScanner(&dataBuf)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "\\." {
				break
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := splitCopyTextLine(line, stmt.Delimiter)
			rows = append(rows, fields)
		}
	}

	// 6. Resolve execution context (active transaction vs autocommit)
	var (
		tx        storage.Transaction
		inTx      bool
		localSess storage.Session
	)

	var cs *engine.ClientSession
	if s.coordinator != nil && sessID != "" {
		cs = s.coordinator.GetSession(sessID)
	}

	if cs != nil {
		cs.Mu.Lock()
		if cs.TxFailed {
			cs.Mu.Unlock()
			_ = WriteError(conn, "current transaction is aborted, commands ignored until end of transaction block")
			_ = s.writeReady(conn, sessID)
			return
		}
		if cs.Tx != nil {
			tx = cs.Tx
			inTx = true
		}
		cs.Mu.Unlock()
	}

	if !inTx {
		var err error
		localSess, err = s.engine.NewSession()
		if err != nil {
			_ = WriteError(conn, fmt.Sprintf("failed to open session: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
		defer localSess.Close()

		tx, err = localSess.BeginTx(storage.TxOptions{})
		if err != nil {
			_ = WriteError(conn, fmt.Sprintf("failed to begin transaction: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
	}

	pkCol, _ := tbl.PrimaryKeyColumn()

	var insertedCount int64
	for rowIdx, fields := range rows {
		rec := make(storage.Record)
		for i, col := range targetCols {
			if i < len(fields) {
				raw := strings.TrimSpace(fields[i])
				if (stmt.Format == FormatText && raw == "\\N") || (stmt.NullStr != "" && raw == stmt.NullStr) || (stmt.Format == FormatCSV && raw == "" && col.Nullable && !col.PrimaryKey) {
					rec[col.Name] = storage.NullValue(col.Type)
				} else {
					fieldVal := raw
					if stmt.Format == FormatText {
						fieldVal = unescapeCopyText(raw)
					}
					v, err := parseFieldVal(fieldVal, col)
					if err != nil {
						if inTx && cs != nil {
							cs.Mu.Lock()
							cs.TxFailed = true
							cs.Mu.Unlock()
						} else if !inTx {
							_ = tx.Rollback()
						}
						_ = WriteError(conn, fmt.Sprintf("line %d, column %q: %v", rowIdx+1, col.Name, err))
						_ = s.writeReady(conn, sessID)
						return
					}
					rec[col.Name] = v
				}
			} else {
				rec[col.Name] = storage.NullValue(col.Type)
			}
		}

		// Auto-generate PK if missing or null
		if pkCol != nil {
			curPK, exists := rec[pkCol.Name]
			if !exists || curPK.IsNull {
				if pkCol.Type == storage.TypeInt {
					rec[pkCol.Name] = storage.IntValue(time.Now().UnixNano()/int64(time.Millisecond) + insertedCount + 1)
				}
			}
		}

		if err := tx.Insert(tbl.Name, rec); err != nil {
			if inTx && cs != nil {
				cs.Mu.Lock()
				cs.TxFailed = true
				cs.Mu.Unlock()
			} else if !inTx {
				_ = tx.Rollback()
			}
			_ = WriteError(conn, fmt.Sprintf("line %d: %v", rowIdx+1, err))
			_ = s.writeReady(conn, sessID)
			return
		}
		insertedCount++
	}

	if !inTx {
		if err := tx.Commit(); err != nil {
			_ = WriteError(conn, fmt.Sprintf("failed to commit COPY transaction: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
	}

	// 7. Complete command
	_ = WriteCommandComplete(conn, fmt.Sprintf("COPY %d", insertedCount))
	_ = s.writeReady(conn, sessID)
}

func (s *Server) handleCopyOut(conn net.Conn, stmt *CopyStmt, sessID string) {
	// 1. Locate source table
	tbl, err := s.engine.GetTable(stmt.TableName)
	if err != nil {
		tbl, err = s.engine.GetTable(strings.ToLower(stmt.TableName))
	}
	if err != nil {
		tables, _ := s.engine.ListTables()
		for _, t := range tables {
			if strings.EqualFold(t.Name, stmt.TableName) {
				tbl = t
				err = nil
				break
			}
		}
	}
	if err != nil || tbl == nil {
		_ = WriteError(conn, fmt.Sprintf("relation %q does not exist", stmt.TableName))
		_ = s.writeReady(conn, sessID)
		return
	}

	// 2. Resolve columns
	var targetCols []storage.Column
	if len(stmt.Columns) > 0 {
		for _, colName := range stmt.Columns {
			col, _, found := tbl.FindColumn(colName)
			if !found {
				for _, c := range tbl.Columns {
					if strings.EqualFold(c.Name, colName) {
						col = &c
						found = true
						break
					}
				}
			}
			if !found {
				_ = WriteError(conn, fmt.Sprintf("column %q does not exist", colName))
				_ = s.writeReady(conn, sessID)
				return
			}
			targetCols = append(targetCols, *col)
		}
	} else {
		targetCols = tbl.Columns
	}

	// 3. Resolve execution context (active transaction vs read-only snapshot)
	var (
		tx        storage.Transaction
		inTx      bool
		localSess storage.Session
	)

	var cs *engine.ClientSession
	if s.coordinator != nil && sessID != "" {
		cs = s.coordinator.GetSession(sessID)
	}

	if cs != nil {
		cs.Mu.Lock()
		if cs.TxFailed {
			cs.Mu.Unlock()
			_ = WriteError(conn, "current transaction is aborted, commands ignored until end of transaction block")
			_ = s.writeReady(conn, sessID)
			return
		}
		if cs.Tx != nil {
			tx = cs.Tx
			inTx = true
		}
		cs.Mu.Unlock()
	}

	if !inTx {
		var err error
		localSess, err = s.engine.NewSession()
		if err != nil {
			_ = WriteError(conn, fmt.Sprintf("failed to open session: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
		defer localSess.Close()

		tx, err = localSess.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			_ = WriteError(conn, fmt.Sprintf("failed to begin snapshot transaction: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
		defer tx.Rollback()
	}

	// 4. Send CopyOutResponse ('H')
	if err := WriteCopyOutResponse(conn, len(targetCols), 0); err != nil {
		return
	}

	// 5. If Header requested, send header line as CopyData
	if stmt.Header {
		var hdrBuf bytes.Buffer
		if stmt.Format == FormatCSV {
			cw := csv.NewWriter(&hdrBuf)
			cw.Comma = stmt.Delimiter
			names := make([]string, len(targetCols))
			for i, c := range targetCols {
				names[i] = c.Name
			}
			_ = cw.Write(names)
			cw.Flush()
		} else {
			names := make([]string, len(targetCols))
			for i, c := range targetCols {
				names[i] = c.Name
			}
			hdrBuf.WriteString(strings.Join(names, string(stmt.Delimiter)) + "\n")
		}
		_ = WriteCopyData(conn, hdrBuf.Bytes())
	}

	// 6. Scan table rows and stream as CopyData
	cur, err := tx.Scan(tbl.Name)
	if err != nil {
		_ = WriteError(conn, fmt.Sprintf("failed to scan table: %v", err))
		_ = s.writeReady(conn, sessID)
		return
	}
	defer cur.Close()

	var rowCount int64
	var chunkBuf bytes.Buffer

	for cur.Next() {
		rec, err := cur.Record()
		if err != nil {
			break
		}

		if stmt.Format == FormatCSV {
			cw := csv.NewWriter(&chunkBuf)
			cw.Comma = stmt.Delimiter
			fields := make([]string, len(targetCols))
			for i, c := range targetCols {
				if val, exists := rec[c.Name]; exists && !val.IsNull {
					fields[i] = val.String()
				} else {
					fields[i] = ""
				}
			}
			_ = cw.Write(fields)
			cw.Flush()
		} else {
			fields := make([]string, len(targetCols))
			for i, c := range targetCols {
				if val, exists := rec[c.Name]; exists && !val.IsNull {
					fields[i] = escapeCopyText(val.String(), stmt.Delimiter)
				} else {
					fields[i] = "\\N"
				}
			}
			chunkBuf.WriteString(strings.Join(fields, string(stmt.Delimiter)) + "\n")
		}

		rowCount++

		// Send buffer in ~32KB chunks
		if chunkBuf.Len() >= 32*1024 {
			_ = WriteCopyData(conn, chunkBuf.Bytes())
			chunkBuf.Reset()
		}
	}

	if chunkBuf.Len() > 0 {
		_ = WriteCopyData(conn, chunkBuf.Bytes())
	}

	// 7. Complete COPY TO STDOUT stream
	_ = WriteCopyDone(conn)
	_ = WriteCommandComplete(conn, fmt.Sprintf("COPY %d", rowCount))
	_ = s.writeReady(conn, sessID)
}

func escapeCopyText(val string, delim rune) string {
	var sb strings.Builder
	sb.Grow(len(val))
	for _, r := range val {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case delim:
			sb.WriteRune('\\')
			sb.WriteRune(delim)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func unescapeCopyText(val string) string {
	if !strings.ContainsRune(val, '\\') {
		return val
	}
	var sb strings.Builder
	sb.Grow(len(val))
	runes := []rune(val)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			i++
			switch runes[i] {
			case 'n':
				sb.WriteRune('\n')
			case 'r':
				sb.WriteRune('\r')
			case 't':
				sb.WriteRune('\t')
			case 'b':
				sb.WriteRune('\b')
			case 'f':
				sb.WriteRune('\f')
			case 'v':
				sb.WriteRune('\v')
			case '\\':
				sb.WriteRune('\\')
			default:
				sb.WriteRune(runes[i])
			}
		} else {
			sb.WriteRune(runes[i])
		}
	}
	return sb.String()
}

func splitCopyTextLine(line string, delim rune) []string {
	var fields []string
	var cur strings.Builder
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && i+1 < len(runes) {
			cur.WriteRune(r)
			i++
			cur.WriteRune(runes[i])
		} else if r == delim {
			fields = append(fields, cur.String())
			cur.Reset()
		} else {
			cur.WriteRune(r)
		}
	}
	fields = append(fields, cur.String())
	return fields
}

func parseFieldVal(raw string, col storage.Column) (storage.Value, error) {
	switch col.Type {
	case storage.TypeInt:
		if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return storage.IntValue(i), nil
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil && f == float64(int64(f)) {
			return storage.IntValue(int64(f)), nil
		}
		return storage.NullValue(storage.TypeInt), fmt.Errorf("invalid input syntax for type integer: %q", raw)
	case storage.TypeFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return storage.NullValue(storage.TypeFloat), fmt.Errorf("invalid input syntax for type float: %q", raw)
		}
		return storage.FloatValue(f), nil
	case storage.TypeBool:
		low := strings.ToLower(raw)
		if low == "t" || low == "true" || low == "1" || low == "y" || low == "yes" {
			return storage.BoolValue(true), nil
		}
		if low == "f" || low == "false" || low == "0" || low == "n" || low == "no" {
			return storage.BoolValue(false), nil
		}
		return storage.NullValue(storage.TypeBool), fmt.Errorf("invalid input syntax for type boolean: %q", raw)
	case storage.TypeDateTime:
		for _, layout := range []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
			"2006-01-02",
		} {
			if t, err := time.Parse(layout, raw); err == nil {
				return storage.DateTimeValue(t), nil
			}
		}
		return storage.NullValue(storage.TypeDateTime), fmt.Errorf("invalid input syntax for type timestamp: %q", raw)
	case storage.TypeDate:
		t, err := storage.ParseDate(raw)
		if err != nil {
			return storage.NullValue(storage.TypeDate), fmt.Errorf("invalid input syntax for type date: %q", raw)
		}
		return storage.DateValue(t), nil
	case storage.TypeTime:
		t, _, err := storage.ParseTime(raw)
		if err != nil {
			return storage.NullValue(storage.TypeTime), fmt.Errorf("invalid input syntax for type time: %q", raw)
		}
		return storage.TimeValue(t), nil
	case storage.TypeDecimal:
		dec, err := storage.ParseDecimal(raw)
		if err != nil {
			return storage.NullValue(storage.TypeDecimal), fmt.Errorf("invalid input syntax for type numeric: %q", raw)
		}
		if col.Scale > 0 {
			dec = dec.Rescale(int32(col.Scale))
		}
		return storage.DecimalValueFromDecimal(dec), nil
	case storage.TypeUUID:
		norm, err := storage.ParseUUID(raw)
		if err != nil {
			return storage.NullValue(storage.TypeUUID), fmt.Errorf("invalid input syntax for type uuid: %q", raw)
		}
		return storage.UUIDValue(norm), nil
	case storage.TypeBinary:
		b, err := storage.ParseBinaryBytes(raw)
		if err != nil {
			return storage.NullValue(storage.TypeBinary), fmt.Errorf("invalid input syntax for type bytea: %q", raw)
		}
		return storage.BinaryValue(b), nil
	case storage.TypeJSON:
		var js any
		if err := json.Unmarshal([]byte(raw), &js); err != nil {
			return storage.NullValue(storage.TypeJSON), fmt.Errorf("invalid input syntax for type json: %q", raw)
		}
		return storage.JSONValue(raw), nil
	default:
		return storage.StringValue(raw), nil
	}
}
