package wire

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

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
		dSub = strings.Trim(dSub, "(),; ")
		dSub = strings.Trim(dSub, "'\"")
		if len(dSub) > 0 {
			if dSub == "\\t" {
				stmt.Delimiter = '\t'
			} else {
				stmt.Delimiter = rune(dSub[0])
			}
		}
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

func (s *Server) handleCopy(conn net.Conn, query string, sessID string) {
	stmt, err := ParseCopyStmt(query)
	if err != nil {
		_ = WriteError(conn, err.Error())
		_ = WriteReadyForQuery(conn)
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
		_ = WriteReadyForQuery(conn)
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
				_ = WriteReadyForQuery(conn)
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
			_ = WriteReadyForQuery(conn)
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
			_ = WriteReadyForQuery(conn)
			return
		}
		if stmt.Header && len(parsed) > 0 {
			rows = parsed[1:]
		} else {
			rows = parsed
		}
	} else {
		// Tab-separated text format
		scanner := bufio.NewScanner(&dataBuf)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "\\." {
				break
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Split(line, string(stmt.Delimiter))
			rows = append(rows, fields)
		}
	}

	// 6. Insert rows inside an ACID transaction
	sess, err := s.engine.NewSession()
	if err != nil {
		_ = WriteError(conn, fmt.Sprintf("failed to open session: %v", err))
		_ = WriteReadyForQuery(conn)
		return
	}
	defer sess.Close()

	pkCol, _ := tbl.PrimaryKeyColumn()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		_ = WriteError(conn, fmt.Sprintf("failed to begin transaction: %v", err))
		_ = WriteReadyForQuery(conn)
		return
	}

	var insertedCount int64
	for rowIdx, fields := range rows {
		rec := make(storage.Record)
		for i, col := range targetCols {
			if i < len(fields) {
				raw := strings.TrimSpace(fields[i])
				if (stmt.Format == FormatText && raw == "\\N") || (stmt.Format == FormatCSV && raw == "" && col.Nullable && !col.PrimaryKey) {
					rec[col.Name] = storage.NullValue(col.Type)
				} else {
					rec[col.Name] = parseFieldVal(raw, col.Type)
				}
			} else {
				rec[col.Name] = storage.NullValue(col.Type)
			}
		}

		// Auto-generate PK if missing or zero
		if pkCol != nil {
			curPK, exists := rec[pkCol.Name]
			if !exists || curPK.IsNull || (pkCol.Type == storage.TypeInt && curPK.IVal == 0) {
				if pkCol.Type == storage.TypeInt {
					rec[pkCol.Name] = storage.IntValue(time.Now().UnixNano()/int64(time.Millisecond) + insertedCount + 1)
				}
			}
		}

		if err := tx.Insert(tbl.Name, rec); err != nil {
			_ = tx.Rollback()
			_ = WriteError(conn, fmt.Sprintf("line %d: %v", rowIdx+1, err))
			_ = WriteReadyForQuery(conn)
			return
		}
		insertedCount++
	}

	if err := tx.Commit(); err != nil {
		_ = WriteError(conn, fmt.Sprintf("failed to commit COPY transaction: %v", err))
		_ = WriteReadyForQuery(conn)
		return
	}

	// 7. Complete command
	_ = WriteCommandComplete(conn, fmt.Sprintf("COPY %d", insertedCount))
	_ = WriteReadyForQuery(conn)
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
		_ = WriteReadyForQuery(conn)
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
				_ = WriteReadyForQuery(conn)
				return
			}
			targetCols = append(targetCols, *col)
		}
	} else {
		targetCols = tbl.Columns
	}

	// 3. Open snapshot read-only transaction
	sess, err := s.engine.NewSession()
	if err != nil {
		_ = WriteError(conn, fmt.Sprintf("failed to open session: %v", err))
		_ = WriteReadyForQuery(conn)
		return
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		_ = WriteError(conn, fmt.Sprintf("failed to begin snapshot transaction: %v", err))
		_ = WriteReadyForQuery(conn)
		return
	}
	defer tx.Rollback()

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
		_ = WriteReadyForQuery(conn)
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
					fields[i] = val.String()
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
	_ = WriteReadyForQuery(conn)
}

func parseFieldVal(raw string, targetType storage.DataType) storage.Value {
	switch targetType {
	case storage.TypeInt:
		if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return storage.IntValue(i)
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return storage.IntValue(int64(f))
		}
		return storage.IntValue(0)
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return storage.FloatValue(f)
		}
		return storage.FloatValue(0)
	case storage.TypeBool:
		low := strings.ToLower(raw)
		return storage.BoolValue(low == "t" || low == "true" || low == "1" || low == "y")
	case storage.TypeDateTime:
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, raw); err == nil {
				return storage.DateTimeValue(t)
			}
		}
		return storage.DateTimeValue(time.Now())
	default:
		return storage.StringValue(raw)
	}
}
