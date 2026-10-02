package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

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
				f, err := strconv.ParseFloat(v.String(), 64)
				if err == nil {
					row[col.Name] = f
				} else {
					row[col.Name] = v.String()
				}
			default:
				row[col.Name] = v.SVal
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
	defer tx.Rollback()

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
	defer tx.Rollback()

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
	defer tx.Rollback()

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
				// Auto-generate numeric or string ID if not provided
				if col.Type == storage.TypeInt {
					v = time.Now().UnixNano() / int64(time.Millisecond)
				} else if col.Type == storage.TypeVarChar {
					v = fmt.Sprintf("%d", time.Now().UnixNano())
				}
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
	defer tx.Rollback()

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
	defer tx.Rollback()

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
	// 1. Create accounts table if not exists
	accountsSchema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	_ = s.engine.CreateTable(accountsSchema)

	// 2. Create customers table if not exists
	customersSchema := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeFloat, Nullable: false},
			{Name: "status", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	_ = s.engine.CreateTable(customersSchema)

	// 3. Insert sample rows
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

	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(500),
	})

	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(1),
		"name":    storage.StringValue("Alice"),
		"balance": storage.FloatValue(1000.5),
		"status":  storage.StringValue("active"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(2),
		"name":    storage.StringValue("Bob"),
		"balance": storage.FloatValue(450.0),
		"status":  storage.StringValue("pending"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(3),
		"name":    storage.StringValue("Charlie"),
		"balance": storage.FloatValue(800.0),
		"status":  storage.StringValue("active"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(4),
		"name":    storage.StringValue("Diana"),
		"balance": storage.FloatValue(200.0),
		"status":  storage.StringValue("inactive"),
	})
	_ = tx.Insert("customers", storage.Record{
		"id":      storage.IntValue(5),
		"name":    storage.StringValue("Evan"),
		"balance": storage.FloatValue(600.0),
		"status":  storage.StringValue("active"),
	})

	_ = tx.Commit()

	writeJSON(w, http.StatusOK, map[string]string{"message": "Sample accounts and customers tables seeded successfully!"})
}

