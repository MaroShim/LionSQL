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
	"lionsql/pkg/sqlutil"
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

	rem, ok := sqlutil.TrimPrefixKeyword(trimmed, "COPY")
	if !ok {
		return nil, fmt.Errorf("not a COPY statement")
	}

	stmt := &CopyStmt{
		Format:    FormatText,
		Delimiter: '\t',
		NullStr:   "\\N",
	}

	rest := strings.TrimSpace(rem)

	// Check direction: FROM STDIN vs TO STDOUT
	var dirPart string

	fromIdx := sqlutil.FindTopLevelKeyword(rest, "FROM STDIN")
	toIdx := sqlutil.FindTopLevelKeyword(rest, "TO STDOUT")

	if fromIdx != -1 {
		stmt.Direction = CopyIn
		dirPart = rest[:fromIdx]
		rest = rest[fromIdx+len("FROM STDIN"):]
	} else if toIdx != -1 {
		stmt.Direction = CopyOut
		dirPart = rest[:toIdx]
		rest = rest[toIdx+len("TO STDOUT"):]
	} else {
		// Also support arbitrary whitespace between FROM and STDIN / TO and STDOUT
		if idx := sqlutil.FindTopLevelKeyword(rest, "FROM"); idx != -1 && sqlutil.ContainsKeyword(rest[idx:], "STDIN", false) {
			stmt.Direction = CopyIn
			dirPart = rest[:idx]
			rest = rest[idx+4:]
			if sIdx := sqlutil.FindTopLevelKeyword(rest, "STDIN"); sIdx != -1 {
				rest = rest[sIdx+5:]
			}
		} else if idx := sqlutil.FindTopLevelKeyword(rest, "TO"); idx != -1 && sqlutil.ContainsKeyword(rest[idx:], "STDOUT", false) {
			stmt.Direction = CopyOut
			dirPart = rest[:idx]
			rest = rest[idx+2:]
			if sIdx := sqlutil.FindTopLevelKeyword(rest, "STDOUT"); sIdx != -1 {
				rest = rest[sIdx+6:]
			}
		} else {
			return nil, fmt.Errorf("unsupported COPY direction (must be FROM STDIN or TO STDOUT)")
		}
	}

	// Parse table name and optional column list from dirPart
	dirPart = strings.TrimSpace(dirPart)
	if openParen := sqlutil.FindTopLevelChar(dirPart, '('); openParen != -1 {
		closeParen := sqlutil.FindMatchingParen(dirPart, openParen)
		if closeParen > openParen {
			tableName := strings.TrimSpace(dirPart[:openParen])
			colsStr := dirPart[openParen+1 : closeParen]

			stmt.TableName = cleanTableName(tableName)
			for _, col := range sqlutil.SplitTopLevel(colsStr, ',') {
				c := strings.TrimSpace(col)
				c = cleanIdentifier(c)
				if c != "" {
					stmt.Columns = append(stmt.Columns, c)
				}
			}
		} else {
			stmt.TableName = cleanTableName(dirPart)
		}
	} else {
		stmt.TableName = cleanTableName(dirPart)
	}

	if stmt.TableName == "" {
		return nil, fmt.Errorf("missing table name in COPY statement")
	}

	// Parse options in rest
	if sqlutil.ContainsKeyword(rest, "CSV", true) {
		stmt.Format = FormatCSV
		stmt.Delimiter = ','
		stmt.NullStr = ""
	}
	if sqlutil.ContainsKeyword(rest, "HEADER", true) {
		stmt.Header = true
	}

	// Custom delimiter
	if dIdx := sqlutil.FindKeyword(rest, "DELIMITER", true); dIdx != -1 {
		dSub := strings.TrimSpace(rest[dIdx+len("DELIMITER"):])
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
	if nIdx := sqlutil.FindKeyword(rest, "NULL", true); nIdx != -1 {
		nSub := strings.TrimSpace(rest[nIdx+len("NULL"):])
		if sqlutil.HasPrefixKeyword(nSub, "AS") {
			nSub = strings.TrimSpace(nSub[2:])
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

	if s.coordinator != nil && sessID != "" {
		if cs := s.coordinator.GetSession(sessID); cs != nil && cs.TxMgr != nil {
			cs.TxMgr.Lock()
			defer cs.TxMgr.Unlock()
		}
	}

	if stmt.Direction == CopyIn {
		s.handleCopyIn(conn, stmt, sessID)
	} else {
		s.handleCopyOut(conn, stmt, sessID)
	}
}

type pgCopyReader struct {
	conn    net.Conn
	current []byte
	buf     []byte
	eof     bool
	err     error
}

func (r *pgCopyReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	for len(r.current) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		var msgHeader [5]byte
		if _, err := io.ReadFull(r.conn, msgHeader[:]); err != nil {
			r.err = err
			return 0, err
		}

		msgType := msgHeader[0]
		msgLen := binary.BigEndian.Uint32(msgHeader[1:5])
		if msgLen < 4 || msgLen > MaxPGMessageSize {
			r.err = fmt.Errorf("invalid COPY message length: %d", msgLen)
			return 0, r.err
		}

		payloadLen := int(msgLen - 4)
		if cap(r.buf) < payloadLen {
			r.buf = make([]byte, payloadLen)
		} else {
			r.buf = r.buf[:payloadLen]
		}
		if _, err := io.ReadFull(r.conn, r.buf); err != nil {
			r.err = err
			return 0, err
		}

		switch msgType {
		case MsgCopyData:
			r.current = r.buf
		case MsgCopyDone:
			r.eof = true
			return 0, io.EOF
		case MsgCopyFail:
			errMsg := string(bytes.TrimRight(r.buf, "\x00"))
			r.err = fmt.Errorf("COPY from stdin cancelled: %s", errMsg)
			return 0, r.err
		default:
			r.err = fmt.Errorf("unexpected message in COPY stream: %c", msgType)
			return 0, r.err
		}
	}

	n := copy(p, r.current)
	r.current = r.current[n:]
	return n, nil
}

func (r *pgCopyReader) Drain() {
	if r.eof || r.err != nil {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		_, err := r.Read(buf)
		if err != nil {
			break
		}
	}
}

func (s *Server) handleCopyIn(conn net.Conn, stmt *CopyStmt, sessID string) {
	var cs *engine.ClientSession
	if s.coordinator != nil && sessID != "" {
		cs = s.coordinator.GetSession(sessID)
	}

	targetTableName := stmt.TableName
	if cs != nil {
		targetTableName = cs.ResolveTempTableName(stmt.TableName)
	}

	// 1. Locate target table schema
	tbl, err := s.engine.GetTable(targetTableName)
	if err != nil {
		tbl, err = s.engine.GetTable(strings.ToLower(targetTableName))
	}
	if err != nil {
		tables, _ := s.engine.ListTables()
		for _, t := range tables {
			if strings.EqualFold(t.Name, targetTableName) {
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

	// 3. Resolve execution context (active transaction vs autocommit)
	var (
		tx        storage.Transaction
		inTx      bool
		localSess storage.Session
	)

	if cs != nil {
		if cs.IsTxFailed() {
			_ = WriteError(conn, "current transaction is aborted, commands ignored until end of transaction block")
			_ = s.writeReady(conn, sessID)
			return
		}
		if actTx := cs.Tx(); actTx != nil {
			tx = actTx
			inTx = true
		}
	}

	var committed bool
	var rolledBack bool
	rollbackOnce := func() {
		if !committed && !rolledBack && tx != nil {
			rolledBack = true
			_ = tx.Rollback()
		}
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

		var txOpts storage.TxOptions
		if s.coordinator != nil {
			if cs := s.coordinator.GetSession(sessID); cs != nil {
				txOpts = cs.TxOptions()
			}
		}
		tx, err = localSess.BeginTx(txOpts)
		if err != nil {
			_ = WriteError(conn, fmt.Sprintf("failed to begin transaction: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
		defer rollbackOnce()
	}

	// 4. Send CopyInResponse ('G')
	if err := WriteCopyInResponse(conn, len(targetCols), 0); err != nil {
		if !inTx {
			rollbackOnce()
		}
		return
	}

	copyReader := &pgCopyReader{conn: conn}
	pkCol, _ := tbl.PrimaryKeyColumn()

	const copyBatchSize = 1000
	batch := make([]storage.Record, 0, copyBatchSize)

	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := tx.BatchInsert(tbl.Name, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	var insertedCount int64

	processRow := func(fields []string, rowIdx int) error {
		rec := make(storage.Record, len(targetCols))
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
						return fmt.Errorf("line %d, column %q: %w", rowIdx, col.Name, err)
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
					var idVal int64
					if s.coordinator != nil {
						idVal = s.coordinator.AllocTableIdentity(tbl.Name, 1, func() int64 {
							var scanMax int64 = 0
							if cur, err := tx.Scan(tbl.Name); err == nil {
								for cur.Next() {
									if r, err := cur.Record(); err == nil {
										if v, ok := r[pkCol.Name]; ok && v.Type == storage.TypeInt && !v.IsNull && v.IVal > scanMax {
											scanMax = v.IVal
										}
									}
								}
								cur.Close()
							}
							return scanMax
						})
					} else {
						idVal = insertedCount + 1
					}
					rec[pkCol.Name] = storage.IntValue(idVal)
				}
			} else if curPK.Type == storage.TypeInt && !curPK.IsNull && s.coordinator != nil {
				s.coordinator.SetTableLastIdentity(tbl.Name, curPK.IVal)
			}
		}

		batch = append(batch, rec)
		insertedCount++
		if len(batch) >= copyBatchSize {
			if err := flushBatch(); err != nil {
				return fmt.Errorf("line %d: %w", rowIdx, err)
			}
		}
		return nil
	}

	var rowIdx int
	if stmt.Format == FormatCSV {
		r := csv.NewReader(copyReader)
		r.Comma = stmt.Delimiter
		r.FieldsPerRecord = -1
		firstRow := true
		for {
			fields, err := r.Read()
			if err == io.EOF {
				break
			}
			rowIdx++
			if err != nil {
				copyReader.Drain()
				if inTx && cs != nil {
					cs.MarkTxFailed()
				} else if !inTx {
					rollbackOnce()
				}
				_ = WriteError(conn, fmt.Sprintf("malformed CSV stream: %v", err))
				_ = s.writeReady(conn, sessID)
				return
			}
			if len(fields) == 1 && strings.TrimSpace(fields[0]) == "\\." {
				break
			}
			if stmt.Header && firstRow {
				firstRow = false
				continue
			}
			firstRow = false
			if err := processRow(fields, rowIdx); err != nil {
				copyReader.Drain()
				if inTx && cs != nil {
					cs.MarkTxFailed()
				} else if !inTx {
					rollbackOnce()
				}
				_ = WriteError(conn, err.Error())
				_ = s.writeReady(conn, sessID)
				return
			}
		}
	} else {
		scanner := bufio.NewScanner(copyReader)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "\\." {
				break
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			rowIdx++
			fields := splitCopyTextLine(line, stmt.Delimiter)
			if err := processRow(fields, rowIdx); err != nil {
				copyReader.Drain()
				if inTx && cs != nil {
					cs.MarkTxFailed()
				} else if !inTx {
					rollbackOnce()
				}
				_ = WriteError(conn, err.Error())
				_ = s.writeReady(conn, sessID)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			copyReader.Drain()
			if inTx && cs != nil {
				cs.MarkTxFailed()
			} else if !inTx {
				rollbackOnce()
			}
			_ = WriteError(conn, fmt.Sprintf("COPY scan error: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
	}

	// Flush any remaining records
	if err := flushBatch(); err != nil {
		copyReader.Drain()
		if inTx && cs != nil {
			cs.MarkTxFailed()
		} else if !inTx {
			rollbackOnce()
		}
		_ = WriteError(conn, fmt.Sprintf("line %d: %v", rowIdx, err))
		_ = s.writeReady(conn, sessID)
		return
	}

	copyReader.Drain()
	if copyReader.err != nil && copyReader.err != io.EOF {
		if inTx && cs != nil {
			cs.MarkTxFailed()
		} else if !inTx {
			rollbackOnce()
		}
		_ = WriteError(conn, copyReader.err.Error())
		_ = s.writeReady(conn, sessID)
		return
	}

	if !inTx {
		if err := tx.Commit(); err != nil {
			rollbackOnce()
			_ = WriteError(conn, fmt.Sprintf("failed to commit COPY transaction: %v", err))
			_ = s.writeReady(conn, sessID)
			return
		}
		committed = true
	}

	// Complete command
	_ = WriteCommandComplete(conn, fmt.Sprintf("COPY %d", insertedCount))
	_ = s.writeReady(conn, sessID)
}

func (s *Server) handleCopyOut(conn net.Conn, stmt *CopyStmt, sessID string) {
	var cs *engine.ClientSession
	if s.coordinator != nil && sessID != "" {
		cs = s.coordinator.GetSession(sessID)
	}

	targetTableName := stmt.TableName
	if cs != nil {
		targetTableName = cs.ResolveTempTableName(stmt.TableName)
	}

	// 1. Locate source table
	tbl, err := s.engine.GetTable(targetTableName)
	if err != nil {
		tbl, err = s.engine.GetTable(strings.ToLower(targetTableName))
	}
	if err != nil {
		tables, _ := s.engine.ListTables()
		for _, t := range tables {
			if strings.EqualFold(t.Name, targetTableName) {
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

	if cs != nil {
		if cs.IsTxFailed() {
			_ = WriteError(conn, "current transaction is aborted, commands ignored until end of transaction block")
			_ = s.writeReady(conn, sessID)
			return
		}
		if actTx := cs.Tx(); actTx != nil {
			tx = actTx
			inTx = true
		}
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

	cw := csv.NewWriter(&chunkBuf)
	cw.Comma = stmt.Delimiter
	fields := make([]string, len(targetCols))

	for cur.Next() {
		rec, err := cur.Record()
		if err != nil {
			break
		}

		if stmt.Format == FormatCSV {
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
	case storage.TypeReal:
		f, err := strconv.ParseFloat(raw, 32)
		if err != nil {
			return storage.NullValue(storage.TypeReal), fmt.Errorf("invalid input syntax for type real: %q", raw)
		}
		return storage.RealValue(float32(f)), nil
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
