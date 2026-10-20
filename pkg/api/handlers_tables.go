package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"lionsql/pkg/storage"
)

type ColumnDTO struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	PrimaryKey bool   `json:"primary_key"`
	Nullable   bool   `json:"nullable"`
}

type ForeignKeyDTO struct {
	Name          string   `json:"name"`
	ChildColumns  []string `json:"child_columns"`
	ParentTable   string   `json:"parent_table"`
	ParentColumns []string `json:"parent_columns"`
}

type TableSchemaDTO struct {
	Name        string          `json:"name"`
	Columns     []ColumnDTO     `json:"columns"`
	ForeignKeys []ForeignKeyDTO `json:"foreign_keys,omitempty"`
	DDL         string          `json:"ddl"`
}

func toTableDTO(t *storage.TableSchema) TableSchemaDTO {
	if t == nil {
		return TableSchemaDTO{}
	}
	cols := make([]ColumnDTO, len(t.Columns))
	var colDefs []string
	for i, c := range t.Columns {
		typeName := c.Type.String()
		cols[i] = ColumnDTO{
			Name:       c.Name,
			Type:       typeName,
			PrimaryKey: c.PrimaryKey,
			Nullable:   c.Nullable,
		}
		def := fmt.Sprintf("    %s %s", c.Name, typeName)
		if c.PrimaryKey {
			def += " PRIMARY KEY"
		} else if !c.Nullable {
			def += " NOT NULL"
		}
		colDefs = append(colDefs, def)
	}

	var fks []ForeignKeyDTO
	for _, fk := range t.ForeignKeys {
		fks = append(fks, ForeignKeyDTO{
			Name:          fk.Name,
			ChildColumns:  fk.ChildColumns,
			ParentTable:   fk.ParentTable,
			ParentColumns: fk.ParentColumns,
		})
	}

	ddl := fmt.Sprintf("CREATE TABLE %s (\n%s\n);", t.Name, strings.Join(colDefs, ",\n"))
	return TableSchemaDTO{
		Name:        t.Name,
		Columns:     cols,
		ForeignKeys: fks,
		DDL:         ddl,
	}
}

func (s *Server) handleListTables(w http.ResponseWriter, r *http.Request) {
	tables, err := s.engine.ListTables()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	dtos := make([]TableSchemaDTO, len(tables))
	for i, t := range tables {
		dtos[i] = toTableDTO(t)
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetTableSchema(w http.ResponseWriter, r *http.Request) {
	tableName := getTableName(r)
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, toTableDTO(schema))
}

type createTableReq struct {
	Name    string             `json:"name"`
	Columns []createColumnItem `json:"columns"`
}

type createColumnItem struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // INT, VARCHAR, FLOAT, BOOL, DATETIME
	PrimaryKey bool   `json:"primary_key"`
	Nullable   bool   `json:"nullable"`
}

func (s *Server) handleCreateTable(w http.ResponseWriter, r *http.Request) {
	var req createTableReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tableName := strings.TrimSpace(req.Name)
	if tableName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("table name is required"))
		return
	}
	if len(req.Columns) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("at least one column is required"))
		return
	}

	cols := make([]storage.Column, len(req.Columns))
	hasPK := false
	for i, c := range req.Columns {
		colName := strings.TrimSpace(c.Name)
		if colName == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("column name cannot be empty"))
			return
		}
		var dt storage.DataType
		rawType := strings.ToUpper(strings.TrimSpace(c.Type))
		baseType := rawType
		if idx := strings.Index(baseType, "("); idx != -1 {
			baseType = strings.TrimSpace(baseType[:idx])
		}
		switch baseType {
		case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT":
			dt = storage.TypeInt
		case "VARCHAR", "TEXT", "STRING", "CHAR", "NVARCHAR", "NCHAR":
			dt = storage.TypeVarChar
		case "FLOAT", "DOUBLE", "REAL":
			dt = storage.TypeFloat
		case "DECIMAL", "NUMERIC", "MONEY":
			dt = storage.TypeDecimal
		case "BOOL", "BOOLEAN":
			dt = storage.TypeBool
		case "DATETIME", "TIMESTAMP", "DATE", "TIME":
			dt = storage.TypeDateTime
		case "UUID", "GUID":
			dt = storage.TypeUUID
		case "JSON":
			dt = storage.TypeJSON
		default:
			writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported column type %q for column %q", c.Type, c.Name))
			return
		}
		if c.PrimaryKey {
			hasPK = true
		}
		cols[i] = storage.Column{
			Name:       colName,
			Type:       dt,
			PrimaryKey: c.PrimaryKey,
			Nullable:   c.Nullable,
		}
	}

	schema := &storage.TableSchema{
		Name:    tableName,
		Columns: cols,
	}
	if !hasPK {
		schema.EnsurePrimaryKey("_row_id")
	}

	if err := s.engine.CreateTable(schema); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{Action: ActionCreateTable, Table: tableName})
	}

	writeJSON(w, http.StatusCreated, toTableDTO(schema))
}

func (s *Server) handleDropTable(w http.ResponseWriter, r *http.Request) {
	tableName := r.PathValue("name")
	if err := s.engine.DropTable(tableName); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{Action: ActionDropTable, Table: tableName})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "table dropped"})
}
