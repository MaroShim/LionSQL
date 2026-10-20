package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
)

var apiPKCounter atomic.Uint64

func getTableName(r *http.Request) string {
	if t := r.PathValue("table"); t != "" {
		return t
	}
	return r.PathValue("name")
}

func getRecordPK(r *http.Request) string {
	if id := r.PathValue("id"); id != "" {
		return id
	}
	return r.PathValue("pk")
}

func recordToJSONMap(rec storage.Record, schema *storage.TableSchema) map[string]any {
	row := make(map[string]any, len(rec))
	if schema != nil {
		for _, col := range schema.Columns {
			v, exists := rec[col.Name]
			if !exists || v.IsNull {
				row[col.Name] = nil
				continue
			}
			switch v.Type {
			case storage.TypeInt:
				row[col.Name] = v.IVal
			case storage.TypeFloat:
				row[col.Name] = v.FVal
			case storage.TypeBool:
				row[col.Name] = v.BVal
			case storage.TypeDecimal:
				row[col.Name] = json.Number(v.String())
			case storage.TypeDateTime:
				row[col.Name] = v.TVal.Format(time.RFC3339)
			case storage.TypeDate:
				row[col.Name] = v.TVal.Format("2006-01-02")
			case storage.TypeTime:
				row[col.Name] = v.TVal.Format("15:04:05")
			default:
				if v.SVal != "" {
					row[col.Name] = v.SVal
				} else {
					row[col.Name] = v.String()
				}
			}
		}
		return row
	}
	for k, v := range rec {
		if v.IsNull {
			row[k] = nil
		} else {
			switch v.Type {
			case storage.TypeInt:
				row[k] = v.IVal
			case storage.TypeFloat:
				row[k] = v.FVal
			case storage.TypeBool:
				row[k] = v.BVal
			case storage.TypeDecimal:
				row[k] = json.Number(v.String())
			case storage.TypeDateTime:
				row[k] = v.TVal.Format(time.RFC3339)
			case storage.TypeDate:
				row[k] = v.TVal.Format("2006-01-02")
			case storage.TypeTime:
				row[k] = v.TVal.Format("15:04:05")
			default:
				row[k] = v.String()
			}
		}
	}
	return row
}

func (s *Server) handleGetRecords(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	// Parse query parameters
	page := 1
	if pStr := r.URL.Query().Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p >= 1 {
			page = p
		}
	}

	perPage := 30
	if ppStr := r.URL.Query().Get("perPage"); ppStr != "" {
		if pp, err := strconv.Atoi(ppStr); err == nil && pp > 0 {
			perPage = pp
		}
	} else if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			perPage = l
		}
	}
	if perPage > 500 {
		perPage = 500
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	sortParam := strings.TrimSpace(r.URL.Query().Get("sort"))

	// Parse filter expression (both ?filter=... and individual column params)
	filterExpr, err := ParseQueryParamsFilter(r.URL.Query(), schema)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Enforce declarative Row-Level Security (ListRule)
	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		rawFilter := r.URL.Query().Get("filter")
		effectiveFilter, rlsErr := s.rls.ApplyListRule(tableName, rawFilter, authCtx)
		if rlsErr != nil {
			writeError(w, http.StatusForbidden, rlsErr)
			return
		}
		if effectiveFilter != rawFilter {
			rlsExpr, err := ParseFilter(effectiveFilter)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("rls filter parse error: %w", err))
				return
			}
			if filterExpr == nil {
				filterExpr = rlsExpr
			} else {
				filterExpr = &AndFilter{Exprs: []FilterExpr{rlsExpr, filterExpr}}
			}
		}
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
	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	cursor, err := tx.Scan(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer cursor.Close()

	records := make([]map[string]any, 0)
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			continue
		}
		row := recordToJSONMap(rec, schema)

		// Search filter
		if search != "" {
			searchLower := strings.ToLower(search)
			matched := false
			for _, v := range row {
				if v == nil {
					continue
				}
				str := fmt.Sprint(v)
				if strings.Contains(strings.ToLower(str), searchLower) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Filter expression
		if filterExpr != nil {
			if !filterExpr.Matches(row, schema) {
				continue
			}
		}

		records = append(records, row)
	}

	// Sort if sort parameter is provided (supports multi-column sort like sort=-id,name or sort=-balance)
	if sortParam != "" {
		type sortItem struct {
			col    string
			isDesc bool
			cType  storage.DataType
		}
		var sortItems []sortItem
		for _, part := range strings.Split(sortParam, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			isDesc := false
			colName := part
			if strings.HasPrefix(colName, "-") {
				isDesc = true
				colName = strings.TrimPrefix(colName, "-")
			} else if strings.HasPrefix(colName, "+") {
				colName = strings.TrimPrefix(colName, "+")
			}
			colName = strings.TrimSpace(colName)

			var dt storage.DataType = storage.TypeVarChar
			if schema != nil {
				if col, _, found := schema.FindColumn(colName); found {
					colName = col.Name
					dt = col.Type
				}
			}
			sortItems = append(sortItems, sortItem{col: colName, isDesc: isDesc, cType: dt})
		}

		if len(sortItems) > 0 {
			sort.SliceStable(records, func(i, j int) bool {
				for _, item := range sortItems {
					vi, hasI := records[i][item.col]
					vj, hasJ := records[j][item.col]
					if !hasI || vi == nil {
						if !hasJ || vj == nil {
							continue
						}
						return !item.isDesc
					}
					if !hasJ || vj == nil {
						return item.isDesc
					}

					var less bool
					var equal bool
					switch item.cType {
					case storage.TypeInt:
						var iVal, jVal int64
						switch v := vi.(type) {
						case int64:
							iVal = v
						case int:
							iVal = int64(v)
						case float64:
							iVal = int64(v)
						}
						switch v := vj.(type) {
						case int64:
							jVal = v
						case int:
							jVal = int64(v)
						case float64:
							jVal = int64(v)
						}
						if iVal == jVal {
							equal = true
						} else {
							less = iVal < jVal
						}
					case storage.TypeFloat, storage.TypeDecimal:
						var iVal, jVal float64
						switch v := vi.(type) {
						case float64:
							iVal = v
						case int64:
							iVal = float64(v)
						case int:
							iVal = float64(v)
						}
						switch v := vj.(type) {
						case float64:
							jVal = v
						case int64:
							jVal = float64(v)
						case int:
							jVal = float64(v)
						}
						if iVal == jVal {
							equal = true
						} else {
							less = iVal < jVal
						}
					case storage.TypeBool:
						bi, _ := vi.(bool)
						bj, _ := vj.(bool)
						if bi == bj {
							equal = true
						} else {
							less = !bi && bj
						}
					default:
						si := strings.ToLower(fmt.Sprint(vi))
						sj := strings.ToLower(fmt.Sprint(vj))
						if si == sj {
							equal = true
						} else {
							less = si < sj
						}
					}

					if equal {
						continue
					}
					if item.isDesc {
						return !less
					}
					return less
				}
				return false
			})
		}
	}

	// Pagination
	totalItems := len(records)
	totalPages := 0
	if totalItems > 0 {
		totalPages = (totalItems + perPage - 1) / perPage
	}

	var pagedRecords []map[string]any
	start := (page - 1) * perPage
	if start < totalItems {
		end := start + perPage
		if end > totalItems {
			end = totalItems
		}
		pagedRecords = records[start:end]
	} else {
		pagedRecords = make([]map[string]any, 0)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"table":      tableName,
		"page":       page,
		"perPage":    perPage,
		"limit":      perPage,
		"totalItems": totalItems,
		"totalPages": totalPages,
		"records":    pagedRecords,
		"items":      pagedRecords,
	})
}

func (s *Server) handleGetSingleRecord(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	pkStr := getRecordPK(r)

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	pkVal := convertValue(pkStr, pkCol.Type)

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
	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	rec, err := tx.Get(tableName, pkVal)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("record with id %q not found", pkStr))
		return
	}

	respRow := recordToJSONMap(rec, schema)
	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		if allowed, err := s.rls.CanView(tableName, respRow, authCtx); !allowed {
			writeError(w, http.StatusForbidden, fmt.Errorf("access denied by row-level security rule: %w", err))
			return
		}
	}

	writeJSON(w, http.StatusOK, respRow)
}

func (s *Server) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, err)
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
	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	rec := make(storage.Record)
	for _, col := range schema.Columns {
		v, exists := raw[col.Name]
		if !exists {
			for k, rawVal := range raw {
				if strings.EqualFold(k, col.Name) {
					v = rawVal
					exists = true
					break
				}
			}
		}
		if !exists || v == nil {
			if col.PrimaryKey {
				// Auto-generate numeric, UUID, or string ID if not provided with guaranteed uniqueness
				if col.Type == storage.TypeInt {
					seq := apiPKCounter.Add(1)
					v = (time.Now().UnixMicro() * 1000) + int64(seq%1000)
				} else if col.Type == storage.TypeUUID || col.Type == storage.TypeVarChar {
					v = uuid.New().String()
				}
			} else if col.Default != "" {
				rec[col.Name] = engine.ParseLiteralValue(col.Default, col.Type)
				continue
			} else if !col.Nullable {
				writeError(w, http.StatusBadRequest, fmt.Errorf("field %q cannot be null", col.Name))
				return
			}
		}
		rec[col.Name] = convertValue(v, col.Type)
	}

	candidateRow := recordToJSONMap(rec, schema)
	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		if allowed, err := s.rls.CanCreate(tableName, candidateRow, authCtx); !allowed {
			writeError(w, http.StatusForbidden, fmt.Errorf("create denied by row-level security rule: %w", err))
			return
		}
	}

	if err := tx.Insert(tableName, rec); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	committed = true

	respRow := recordToJSONMap(rec, schema)

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionCreate,
			Table:  tableName,
			Record: respRow,
		})
	}

	writeJSON(w, http.StatusCreated, respRow)
}

func (s *Server) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	pkStr := getRecordPK(r)

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	pkVal := convertValue(pkStr, pkCol.Type)

	var updates map[string]any
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, err)
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
	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	existing, err := tx.Get(tableName, pkVal)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("record with id %s not found: %w", pkStr, err))
		return
	}

	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		existingJSON := recordToJSONMap(existing, schema)
		if allowed, err := s.rls.CanUpdate(tableName, existingJSON, updates, authCtx); !allowed {
			writeError(w, http.StatusForbidden, fmt.Errorf("update denied by row-level security rule: %w", err))
			return
		}
	}

	for k, v := range updates {
		col, _, found := schema.FindColumn(k)
		if !found {
			continue
		}
		existing[col.Name] = convertValue(v, col.Type)
	}

	if err := tx.Update(tableName, pkVal, existing); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	committed = true

	respRow := recordToJSONMap(existing, schema)

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionUpdate,
			Table:  tableName,
			ID:     pkStr,
			Record: respRow,
		})
	}

	writeJSON(w, http.StatusOK, respRow)
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	pkStr := getRecordPK(r)

	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	pkVal := convertValue(pkStr, pkCol.Type)

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
	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	existing, err := tx.Get(tableName, pkVal)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("record with id %s not found: %w", pkStr, err))
		return
	}

	if s.rls != nil {
		authCtx := s.extractAuthContext(r)
		existingJSON := recordToJSONMap(existing, schema)
		if allowed, err := s.rls.CanDelete(tableName, existingJSON, authCtx); !allowed {
			writeError(w, http.StatusForbidden, fmt.Errorf("delete denied by row-level security rule: %w", err))
			return
		}
	}

	if err := tx.Delete(tableName, pkVal); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	committed = true

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionDelete,
			Table:  tableName,
			ID:     pkStr,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "record deleted", "id": pkStr})
}

func (s *Server) handleSeed(w http.ResponseWriter, r *http.Request) {
	// 0. Safety invariant: Seed sample data is only permitted on an empty database
	tables, err := s.engine.ListTables()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to check existing tables: %w", err))
		return
	}
	if len(tables) > 0 {
		names := make([]string, 0, len(tables))
		for _, t := range tables {
			names = append(names, t.Name)
		}
		writeError(w, http.StatusConflict, fmt.Errorf("sample data can only be seeded on an empty database; %d table(s) already exist: %s", len(tables), strings.Join(names, ", ")))
		return
	}

	// 1. Create accounts table
	accountsSchema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := s.engine.CreateTable(accountsSchema); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to create 'accounts' table: %w", err))
		return
	}

	// 2. Create customers table
	customersSchema := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeFloat, Nullable: false},
			{Name: "status", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	if err := s.engine.CreateTable(customersSchema); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to create 'customers' table: %w", err))
		return
	}

	// 3. Insert sample rows within a single atomic ACID transaction
	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	accountsRecords := []storage.Record{
		{"id": storage.IntValue(1), "holder": storage.StringValue("Alice"), "balance": storage.IntValue(1000)},
		{"id": storage.IntValue(2), "holder": storage.StringValue("Bob"), "balance": storage.IntValue(500)},
	}
	for _, rec := range accountsRecords {
		if err := tx.Insert("accounts", rec); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to insert record into 'accounts': %w", err))
			return
		}
	}

	customerRecords := []storage.Record{
		{"id": storage.IntValue(1), "name": storage.StringValue("Alice"), "balance": storage.FloatValue(1000.5), "status": storage.StringValue("active")},
		{"id": storage.IntValue(2), "name": storage.StringValue("Bob"), "balance": storage.FloatValue(450.0), "status": storage.StringValue("pending")},
		{"id": storage.IntValue(3), "name": storage.StringValue("Charlie"), "balance": storage.FloatValue(800.0), "status": storage.StringValue("active")},
		{"id": storage.IntValue(4), "name": storage.StringValue("Diana"), "balance": storage.FloatValue(200.0), "status": storage.StringValue("inactive")},
		{"id": storage.IntValue(5), "name": storage.StringValue("Evan"), "balance": storage.FloatValue(600.0), "status": storage.StringValue("active")},
	}
	for _, rec := range customerRecords {
		if err := tx.Insert("customers", rec); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to insert record into 'customers': %w", err))
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to commit sample data transaction: %w", err))
		return
	}
	committed = true

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Sample tables ('accounts', 'customers') seeded successfully with 7 records!",
		"tables":  []string{"accounts", "customers"},
		"records": 7,
	})
}

