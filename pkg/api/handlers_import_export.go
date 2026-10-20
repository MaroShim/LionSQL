package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"lionsql/pkg/storage"
)

func (s *Server) handleExportTable(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "csv"
	}

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	cursor, err := tx.Scan(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer cursor.Close()

	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", tableName+".json"))
		var rows []map[string]any
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				continue
			}
			row := make(map[string]any)
			for _, col := range schema.Columns {
				v, exists := rec[col.Name]
				if !exists || v.IsNull {
					row[col.Name] = nil
				} else {
					switch v.Type {
					case storage.TypeInt:
						row[col.Name] = v.IVal
					case storage.TypeVarChar:
						row[col.Name] = v.SVal
					case storage.TypeFloat:
						row[col.Name] = v.FVal
					case storage.TypeBool:
						row[col.Name] = v.BVal
					default:
						row[col.Name] = v.String()
					}
				}
			}
			rows = append(rows, row)
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(rows)
		return
	}

	// CSV Export
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", tableName+".csv"))
	csvWriter := csv.NewWriter(w)
	defer csvWriter.Flush()

	// Header row
	header := make([]string, len(schema.Columns))
	for i, col := range schema.Columns {
		header[i] = col.Name
	}
	_ = csvWriter.Write(header)

	// Data rows
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			continue
		}
		row := make([]string, len(schema.Columns))
		for i, col := range schema.Columns {
			v, exists := rec[col.Name]
			if !exists || v.IsNull {
				row[i] = ""
			} else {
				row[i] = v.String()
			}
		}
		_ = csvWriter.Write(row)
	}
}

func (s *Server) handleImportTable(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	var reader io.Reader
	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("failed to parse multipart form: %w", err))
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("file field is required in multipart upload: %w", err))
			return
		}
		defer file.Close()
		reader = file
	} else if strings.Contains(contentType, "application/json") {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}
		var recordList []map[string]any
		switch v := body.(type) {
		case []any:
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					recordList = append(recordList, m)
				}
			}
		case map[string]any:
			if recs, ok := v["records"].([]any); ok {
				for _, item := range recs {
					if m, ok := item.(map[string]any); ok {
						recordList = append(recordList, m)
					}
				}
			} else if csvStr, ok := v["csv"].(string); ok {
				reader = strings.NewReader(csvStr)
			}
		}

		if recordList != nil {
			session, err := s.engine.NewSession()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			defer session.Close()

			tx, err := session.BeginTx(storage.TxOptions{ReadOnly: false})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			defer tx.Rollback()

			imported := 0
			for idx, row := range recordList {
				rec := make(storage.Record)
				for _, col := range schema.Columns {
					val, exists := row[col.Name]
					if !exists || val == nil {
						if col.PrimaryKey && col.Type == storage.TypeInt {
							val = time.Now().UnixNano()/int64(time.Millisecond) + int64(idx)
						} else if !col.Nullable {
							writeError(w, http.StatusBadRequest, fmt.Errorf("row %d: field %q cannot be null", idx+1, col.Name))
							return
						}
					}
					rec[col.Name] = convertValue(val, col.Type)
				}
				if err := tx.Insert(tableName, rec); err != nil {
					writeError(w, http.StatusBadRequest, fmt.Errorf("row %d insert error: %w", idx+1, err))
					return
				}
				imported++
			}
			if err := tx.Commit(); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			if s.broker != nil {
				s.broker.Broadcast(RealtimeEvent{
					Action: ActionImport,
					Table:  tableName,
				})
			}
			writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "table": tableName})
			return
		}
	}

	if reader == nil {
		reader = r.Body
	}

	csvReader := csv.NewReader(reader)
	csvReader.TrimLeadingSpace = true

	headers, err := csvReader.Read()
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("failed to read CSV header: %w", err))
		return
	}

	type colMap struct {
		csvIdx int
		col    storage.Column
	}
	var mappings []colMap
	var pkCol *storage.Column
	for i := range schema.Columns {
		if schema.Columns[i].PrimaryKey {
			pkCol = &schema.Columns[i]
		}
	}

	for csvIdx, h := range headers {
		cleanH := strings.TrimSpace(h)
		for _, col := range schema.Columns {
			if strings.EqualFold(col.Name, cleanH) {
				mappings = append(mappings, colMap{csvIdx: csvIdx, col: col})
				break
			}
		}
	}

	if len(mappings) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("no matching columns found between CSV headers and table %q", tableName))
		return
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{ReadOnly: false})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	imported := 0
	rowIdx := 0
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("CSV parse error at line %d: %w", rowIdx+2, err))
			return
		}
		rowIdx++

		rec := make(storage.Record)
		for _, m := range mappings {
			if m.csvIdx < len(record) {
				rawVal := strings.TrimSpace(record[m.csvIdx])
				if rawVal == "" && m.col.Nullable && !m.col.PrimaryKey {
					rec[m.col.Name] = storage.NullValue(m.col.Type)
				} else {
					rec[m.col.Name] = convertValue(rawVal, m.col.Type)
				}
			}
		}

		// If PK is missing or empty, auto-generate if INT
		if pkCol != nil {
			curPK, exists := rec[pkCol.Name]
			if !exists || curPK.IsNull || (pkCol.Type == storage.TypeInt && curPK.IVal == 0) {
				if pkCol.Type == storage.TypeInt {
					rec[pkCol.Name] = storage.IntValue(time.Now().UnixNano()/int64(time.Millisecond) + int64(rowIdx))
				}
			}
		}

		if err := tx.Insert(tableName, rec); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("line %d insert error: %w", rowIdx+1, err))
			return
		}
		imported++
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionImport,
			Table:  tableName,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"table":    tableName,
	})
}
