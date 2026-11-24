package tds

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"time"

	"lionsql/pkg/engine"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// parseInsertBulkStmt parses an "INSERT BULK <TableName> (<cols>) [WITH (...)]" statement.
func parseInsertBulkStmt(query string) (string, []string, bool) {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.TrimRight(trimmed, ";")
	trimmed = strings.TrimSpace(trimmed)

	rem, ok := sqlutil.TrimPrefixKeyword(trimmed, "INSERT BULK")
	if !ok {
		return "", nil, false
	}
	rem = strings.TrimSpace(rem)

	openParen := sqlutil.FindTopLevelChar(rem, '(')
	if openParen == -1 {
		tbl := cleanTableName(rem)
		return tbl, nil, true
	}

	tableName := cleanTableName(rem[:openParen])
	closeParen := sqlutil.FindMatchingParen(rem, openParen)
	if closeParen <= openParen {
		return tableName, nil, true
	}

	colsPart := rem[openParen+1 : closeParen]
	var cols []string
	for _, colDef := range sqlutil.SplitTopLevel(colsPart, ',') {
		colDef = strings.TrimSpace(colDef)
		if colDef == "" {
			continue
		}
		parts := strings.Fields(colDef)
		if len(parts) > 0 {
			cols = append(cols, cleanIdentifier(parts[0]))
		}
	}
	return tableName, cols, true
}

func cleanTableName(s string) string {
	s = strings.TrimSpace(s)
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

// handleBulkLoad handles incoming TDS 0x07 (PacketBulkLoad) row streams.
func (s *Server) handleBulkLoad(conn net.Conn, pkt *Packet, sess *ConnectionSession) {
	targetTableName := sess.bulkTable
	if targetTableName == "" {
		s.sendBulkError(conn, sess, "no active bulk insert table specified (missing INSERT BULK)")
		return
	}

	cs := s.getClientSession(sess)
	if cs != nil {
		targetTableName = cs.ResolveTempTableName(targetTableName)
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
		s.sendBulkError(conn, sess, fmt.Sprintf("cannot find table %q for bulk load", sess.bulkTable))
		return
	}

	// 2. Resolve target columns
	var targetCols []storage.Column
	if len(sess.bulkCols) > 0 {
		for _, colName := range sess.bulkCols {
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
				s.sendBulkError(conn, sess, fmt.Sprintf("column %q of relation %q does not exist", colName, tbl.Name))
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
			s.sendBulkError(conn, sess, "current transaction is aborted, commands ignored until end of transaction block")
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
			s.sendBulkError(conn, sess, fmt.Sprintf("failed to open session: %v", err))
			return
		}
		defer localSess.Close()

		var txOpts storage.TxOptions
		if s.coordinator != nil {
			if cs := s.coordinator.GetSession(sess.SessionID); cs != nil {
				txOpts = cs.TxOptions()
			}
		}
		tx, err = localSess.BeginTx(txOpts)
		if err != nil {
			s.sendBulkError(conn, sess, fmt.Sprintf("failed to begin transaction: %v", err))
			return
		}
		defer rollbackOnce()
	}

	// 4. Stream rows from pkt.Payload
	r := bytes.NewReader(pkt.Payload)

	// Check and skip ALL_HEADERS if present
	if totalHeadersLen, ok := parseAllHeaders(pkt.Payload); ok {
		_, _ = r.Seek(int64(totalHeadersLen), io.SeekStart)
	}

	pkCol, _ := tbl.PrimaryKeyColumn()

	const bcpBatchSize = 1000
	batch := make([]storage.Record, 0, bcpBatchSize)

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

	for r.Len() > 0 {
		token, err := r.ReadByte()
		if err != nil {
			break
		}

		if token == TokenColMetaData {
			// Skip COLMETADATA token payload
			if err := skipColMetaDataToken(r); err != nil {
				if inTx && cs != nil {
					cs.MarkTxFailed()
				} else if !inTx {
					rollbackOnce()
				}
				s.sendBulkError(conn, sess, fmt.Sprintf("failed to parse COLMETADATA in bulk stream: %v", err))
				return
			}
			continue
		}

		if token == TokenDone || token == TokenDoneProc || token == TokenDoneInProc {
			// Done token: 2B status, 2B curCmd, 8B rowCount
			var doneBuf [12]byte
			_, _ = io.ReadFull(r, doneBuf[:])
			break
		}

		if token == TokenNBCRow {
			rec, err := readNBCRowRecord(r, targetCols)
			if err != nil {
				if inTx && cs != nil {
					cs.MarkTxFailed()
				} else if !inTx {
					rollbackOnce()
				}
				s.sendBulkError(conn, sess, fmt.Sprintf("failed to read NBCROW in bulk stream: %v", err))
				return
			}
			applyBCPIdentity(rec, pkCol, tbl.Name, s.coordinator, insertedCount)
			batch = append(batch, rec)
			insertedCount++
			if len(batch) >= bcpBatchSize {
				if err := flushBatch(); err != nil {
					if inTx && cs != nil {
						cs.MarkTxFailed()
					} else if !inTx {
						rollbackOnce()
					}
					s.sendBulkError(conn, sess, fmt.Sprintf("bulk insert failed: %v", err))
					return
				}
			}
			continue
		}

		if token == TokenRow {
			rec, err := readRowRecord(r, targetCols)
			if err != nil {
				if inTx && cs != nil {
					cs.MarkTxFailed()
				} else if !inTx {
					rollbackOnce()
				}
				s.sendBulkError(conn, sess, fmt.Sprintf("failed to read ROW in bulk stream: %v", err))
				return
			}
			applyBCPIdentity(rec, pkCol, tbl.Name, s.coordinator, insertedCount)
			batch = append(batch, rec)
			insertedCount++
			if len(batch) >= bcpBatchSize {
				if err := flushBatch(); err != nil {
					if inTx && cs != nil {
						cs.MarkTxFailed()
					} else if !inTx {
						rollbackOnce()
					}
					s.sendBulkError(conn, sess, fmt.Sprintf("bulk insert failed: %v", err))
					return
				}
			}
			continue
		}

		// Unknown token or raw row data without token prefix
		_ = r.UnreadByte()
		rec, err := readNBCRowRecord(r, targetCols)
		if err != nil {
			break
		}
		applyBCPIdentity(rec, pkCol, tbl.Name, s.coordinator, insertedCount)
		batch = append(batch, rec)
		insertedCount++
		if len(batch) >= bcpBatchSize {
			if err := flushBatch(); err != nil {
				if inTx && cs != nil {
					cs.MarkTxFailed()
				} else if !inTx {
					rollbackOnce()
				}
				s.sendBulkError(conn, sess, fmt.Sprintf("bulk insert failed: %v", err))
				return
			}
		}
	}

	// 5. Flush any remaining records in final batch
	if err := flushBatch(); err != nil {
		if inTx && cs != nil {
			cs.MarkTxFailed()
		} else if !inTx {
			rollbackOnce()
		}
		s.sendBulkError(conn, sess, fmt.Sprintf("bulk insert failed: %v", err))
		return
	}

	if !inTx {
		if err := tx.Commit(); err != nil {
			rollbackOnce()
			s.sendBulkError(conn, sess, fmt.Sprintf("failed to commit bulk transaction: %v", err))
			return
		}
		committed = true
	}

	// 6. Complete bulk load with DONE token
	doneStatus := DoneFinal | DoneCount
	if sess.inTx {
		doneStatus |= DoneInXact
	}
	done := BuildDone(TokenDone, doneStatus, 0xC1, uint64(insertedCount))
	_ = sess.writePacket(conn, PacketTabular, done)
}

func (s *Server) sendBulkError(conn net.Conn, sess *ConnectionSession, msg string) {
	var buf bytes.Buffer
	num, state, sev, errStr := extractTdsErrorInfo(fmt.Errorf("%s", msg))
	buf.Write(BuildError(num, state, sev, errStr, "LionSQL", "", 1))
	doneStatus := DoneFinal | DoneError
	if sess.inTx {
		doneStatus |= DoneInXact
	}
	buf.Write(BuildDone(TokenDone, doneStatus, 0, 0))
	_ = sess.writePacket(conn, PacketTabular, buf.Bytes())
}

func applyBCPIdentity(rec storage.Record, pkCol *storage.Column, tableName string, coord engine.SQLCoordinator, insertedCount int64) {
	if pkCol == nil {
		return
	}
	curPK, exists := rec[pkCol.Name]
	if !exists || curPK.IsNull {
		if pkCol.Type == storage.TypeInt {
			var idVal int64
			if coord != nil {
				idVal = coord.AllocTableIdentity(tableName, 1, func() int64 {
					return 0
				})
			} else {
				idVal = insertedCount + 1
			}
			rec[pkCol.Name] = storage.IntValue(idVal)
		}
	} else if curPK.Type == storage.TypeInt && !curPK.IsNull && coord != nil {
		coord.SetTableLastIdentity(tableName, curPK.IVal)
	}
}

func readNBCRowRecord(r *bytes.Reader, cols []storage.Column) (storage.Record, error) {
	numCols := len(cols)
	bitmapLen := (numCols + 7) / 8
	bitmap := make([]byte, bitmapLen)
	if _, err := io.ReadFull(r, bitmap); err != nil {
		return nil, err
	}

	rec := make(storage.Record, numCols)
	for i, col := range cols {
		isNull := (bitmap[i/8] & (1 << (i % 8))) != 0
		if isNull {
			rec[col.Name] = storage.NullValue(col.Type)
			continue
		}

		val, err := readBCPColumnValue(r, col)
		if err != nil {
			return nil, fmt.Errorf("col %q: %w", col.Name, err)
		}
		rec[col.Name] = val
	}
	return rec, nil
}

func readRowRecord(r *bytes.Reader, cols []storage.Column) (storage.Record, error) {
	numCols := len(cols)
	rec := make(storage.Record, numCols)
	for _, col := range cols {
		val, err := readBCPColumnValue(r, col)
		if err != nil {
			return nil, fmt.Errorf("col %q: %w", col.Name, err)
		}
		rec[col.Name] = val
	}
	return rec, nil
}

func readBCPColumnValue(r *bytes.Reader, col storage.Column) (storage.Value, error) {
	switch col.Type {
	case storage.TypeInt:
		actualLen, err := r.ReadByte()
		if err != nil {
			return storage.Value{}, err
		}
		switch actualLen {
		case 0:
			return storage.NullValue(storage.TypeInt), nil
		case 1:
			b, err := r.ReadByte()
			return storage.IntValue(int64(int8(b))), err
		case 2:
			var v int16
			err := binary.Read(r, binary.LittleEndian, &v)
			return storage.IntValue(int64(v)), err
		case 4:
			var v int32
			err := binary.Read(r, binary.LittleEndian, &v)
			return storage.IntValue(int64(v)), err
		case 8:
			var v int64
			err := binary.Read(r, binary.LittleEndian, &v)
			return storage.IntValue(v), err
		default:
			buf := make([]byte, actualLen)
			_, err := io.ReadFull(r, buf)
			return storage.Value{}, err
		}

	case storage.TypeFloat:
		actualLen, err := r.ReadByte()
		if err != nil {
			return storage.Value{}, err
		}
		switch actualLen {
		case 0:
			return storage.NullValue(storage.TypeFloat), nil
		case 4:
			var v float32
			err := binary.Read(r, binary.LittleEndian, &v)
			return storage.FloatValue(float64(v)), err
		case 8:
			var v float64
			err := binary.Read(r, binary.LittleEndian, &v)
			return storage.FloatValue(v), err
		default:
			buf := make([]byte, actualLen)
			_, err := io.ReadFull(r, buf)
			return storage.Value{}, err
		}

	case storage.TypeBool:
		actualLen, err := r.ReadByte()
		if err != nil {
			return storage.Value{}, err
		}
		if actualLen == 0 {
			return storage.NullValue(storage.TypeBool), nil
		}
		b, err := r.ReadByte()
		return storage.BoolValue(b != 0), err

	case storage.TypeVarChar:
		var lenPref uint16
		if err := binary.Read(r, binary.LittleEndian, &lenPref); err != nil {
			return storage.Value{}, err
		}
		if lenPref == 0xFFFF {
			// In TDS, 0xFFFF indicates NULL for standard VARCHAR/NVARCHAR.
			// Only LOB types (VARCHAR(MAX), NVARCHAR(MAX)) use PLP chunking.
			if strings.Contains(strings.ToUpper(col.RawType), "MAX") && r.Len() >= 8 {
				var plpLen uint64
				_ = binary.Read(r, binary.LittleEndian, &plpLen)
				if plpLen == 0xFFFFFFFFFFFFFFFF {
					return storage.NullValue(storage.TypeVarChar), nil
				}
				if plpLen != 0 {
					var plpBuf bytes.Buffer
					for {
						var chunkLen uint32
						if err := binary.Read(r, binary.LittleEndian, &chunkLen); err != nil {
							return storage.Value{}, err
						}
						if chunkLen == 0 {
							break
						}
						chunk := make([]byte, chunkLen)
						if _, err := io.ReadFull(r, chunk); err != nil {
							return storage.Value{}, err
						}
						plpBuf.Write(chunk)
					}
					data := plpBuf.Bytes()
					return storage.StringValue(decodeTDSString(data)), nil
				}
			}
			return storage.NullValue(storage.TypeVarChar), nil
		}
		if lenPref == 0 {
			return storage.StringValue(""), nil
		}
		buf := make([]byte, lenPref)
		if _, err := io.ReadFull(r, buf); err != nil {
			return storage.Value{}, err
		}
		return storage.StringValue(decodeTDSString(buf)), nil

	case storage.TypeDateTime:
		actualLen, err := r.ReadByte()
		if err != nil {
			return storage.Value{}, err
		}
		if actualLen == 0 {
			return storage.NullValue(storage.TypeDateTime), nil
		}
		if actualLen == 8 {
			var days int32
			var ticks uint32
			_ = binary.Read(r, binary.LittleEndian, &days)
			_ = binary.Read(r, binary.LittleEndian, &ticks)
			t := decodeDateTime(days, ticks)
			return storage.DateTimeValue(t), nil
		}
		if actualLen == 4 {
			var days uint16
			var minutes uint16
			_ = binary.Read(r, binary.LittleEndian, &days)
			_ = binary.Read(r, binary.LittleEndian, &minutes)
			t := decodeSmallDateTime(days, minutes)
			return storage.DateTimeValue(t), nil
		}
		buf := make([]byte, actualLen)
		_, _ = io.ReadFull(r, buf)
		return storage.DateTimeValue(time.Now()), nil

	default:
		// Fallback as string
		var lenPref uint16
		if err := binary.Read(r, binary.LittleEndian, &lenPref); err != nil {
			return storage.Value{}, err
		}
		if lenPref == 0xFFFF {
			return storage.NullValue(col.Type), nil
		}
		buf := make([]byte, lenPref)
		_, _ = io.ReadFull(r, buf)
		return storage.StringValue(string(buf)), nil
	}
}

func decodeTDSString(b []byte) string {
	if len(b) >= 2 && b[1] == 0 {
		return DecodeUTF16LE(b)
	}
	return string(b)
}

func decodeDateTime(days int32, ticks uint32) time.Time {
	t := epoch1900.AddDate(0, 0, int(days))
	ms := int64(math.Round(float64(ticks) * 1000.0 / 300.0))
	return t.Add(time.Duration(ms) * time.Millisecond)
}

func decodeSmallDateTime(days uint16, minutes uint16) time.Time {
	t := epoch1900.AddDate(0, 0, int(days))
	return t.Add(time.Duration(minutes) * time.Minute)
}

func skipColMetaDataToken(r *bytes.Reader) error {
	var colCount uint16
	if err := binary.Read(r, binary.LittleEndian, &colCount); err != nil {
		return err
	}
	for i := uint16(0); i < colCount; i++ {
		var userType uint32
		_ = binary.Read(r, binary.LittleEndian, &userType)
		var flags uint16
		_ = binary.Read(r, binary.LittleEndian, &flags)

		typeByte, err := r.ReadByte()
		if err != nil {
			return err
		}

		switch typeByte {
		case TypeInt1, TypeInt2, TypeInt4, TypeInt8, TypeBit, TypeFlt4, TypeFlt8:
			// Fixed size, no extra info
		case TypeIntN, TypeBitN, TypeFltN:
			_, _ = r.ReadByte() // maxLen
		case TypeVarChar, TypeNVarChar:
			var maxLen uint16
			_ = binary.Read(r, binary.LittleEndian, &maxLen)
			var collation [5]byte
			_, _ = io.ReadFull(r, collation[:])
		case TypeDecimalN, TypeNumericN:
			_, _ = r.ReadByte() // maxLen
			_, _ = r.ReadByte() // precision
			_, _ = r.ReadByte() // scale
		default:
			_, _ = r.ReadByte()
		}

		// ColName: B_VARCHAR
		if _, err := ReadBVarChar(r); err != nil {
			return err
		}
	}
	return nil
}
