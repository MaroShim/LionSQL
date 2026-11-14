package virtual

import (
	"runtime"
	"strings"
	"time"

	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
)

// GenerateSysCatalogRows produces column schemas and raw rows for single-table sys.* and SQL Server virtual catalog queries.
// It returns handled = true when the normalized tableName matches a registered sys catalog table/view.
func GenerateSysCatalogRows(src Source, norm string, currDb string) (columns []string, allRows []map[string]any, handled bool, err error) {
	var tables []*storage.TableSchema
	if src != nil {
		tables = src.GetSortedTables()
		if len(tables) == 0 {
			if tblList, listErr := src.ListTables(); listErr == nil {
				tables = tblList
			}
		}
	}

	defaultDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	switch norm {
	case "sys.tables", "systables", "sys_tables":
		columns = []string{
			"table_name", "name", "object_id", "schema_id", "table_type", "table_schema",
			"column_count", "create_date", "modify_date", "principal_id", "is_ms_shipped",
			"is_replicated", "max_column_id_used", "lob_data_space_id", "filestream_data_space_id",
			"is_memory_optimized", "durability", "durability_desc", "temporal_type", "temporal_type_desc",
			"is_filetable", "is_external", "is_node", "is_edge", "is_tracked_by_cdc", "lock_on_bulk_load",
			"uses_ansi_nulls", "is_published", "is_auto_cleanup_types",
		}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"table_name":               t.Name,
				"name":                     t.Name,
				"object_id":                tableObjectID(i),
				"schema_id":                1,
				"table_type":               "BASE TABLE",
				"table_schema":             "dbo",
				"column_count":             len(t.Columns),
				"create_date":              defaultDate,
				"modify_date":              defaultDate,
				"principal_id":             nil,
				"is_ms_shipped":            false,
				"is_replicated":            false,
				"max_column_id_used":       len(t.Columns),
				"lob_data_space_id":        0,
				"filestream_data_space_id": 0,
				"is_memory_optimized":      false,
				"durability":               0,
				"durability_desc":          "SCHEMA_AND_DATA",
				"temporal_type":            0,
				"temporal_type_desc":       "NON_TEMPORAL_TABLE",
				"is_filetable":             false,
				"is_external":              false,
				"is_node":                  false,
				"is_edge":                  false,
				"is_tracked_by_cdc":        false,
				"lock_on_bulk_load":        false,
				"uses_ansi_nulls":          true,
				"is_published":             false,
				"is_auto_cleanup_types":    false,
			})
		}
		return columns, allRows, true, nil

	case "sys.columns", "syscolumns", "sys_columns", "sys.all_columns", "sysallcolumns", "sys_all_columns":
		columns = []string{"table_name", "column_name", "name", "object_id", "column_id", "system_type_id", "user_type_id", "data_type", "is_nullable", "is_identity", "is_primary_key", "ordinal_position", "max_length", "precision", "scale"}
		for tIdx, t := range tables {
			for i, c := range t.Columns {
				nullableStr := "YES"
				if !c.Nullable {
					nullableStr = "NO"
				}
				systemTypeID := 56
				userTypeID := 56
				maxLength := 4
				prec := 10
				scale := 0
				dataTypeStr := c.Type.String()
				if c.IsRowVersion {
					systemTypeID, userTypeID, maxLength, prec, scale = 189, 189, 8, 0, 0
					dataTypeStr = "rowversion"
				} else {
					switch c.Type {
					case storage.TypeInt:
						systemTypeID, userTypeID, maxLength, prec = 56, 56, 4, 10
					case storage.TypeReal:
						systemTypeID, userTypeID, maxLength, prec = 59, 59, 4, 24
					case storage.TypeFloat:
						systemTypeID, userTypeID, maxLength, prec = 62, 62, 8, 53
					case storage.TypeVarChar:
						systemTypeID, userTypeID, maxLength, prec = 167, 167, 8000, 0
					case storage.TypeBool:
						systemTypeID, userTypeID, maxLength, prec = 104, 104, 1, 1
					case storage.TypeDateTime:
						systemTypeID, userTypeID, maxLength, prec, scale = 61, 61, 8, 23, 3
					case storage.TypeDecimal:
						p, s := c.Precision, c.Scale
						if p <= 0 {
							p = 18
						}
						if s < 0 {
							s = 2
						}
						systemTypeID, userTypeID, maxLength, prec, scale = 108, 108, 17, p, s
					}
				}
				allRows = append(allRows, map[string]any{
					"table_name":       t.Name,
					"column_name":      c.Name,
					"name":             c.Name,
					"object_id":        tableObjectID(tIdx),
					"column_id":        i + 1,
					"system_type_id":   systemTypeID,
					"user_type_id":     userTypeID,
					"data_type":        dataTypeStr,
					"is_nullable":      nullableStr,
					"is_identity":      false,
					"is_primary_key":   c.PrimaryKey,
					"ordinal_position": i + 1,
					"max_length":       maxLength,
					"precision":        prec,
					"scale":            scale,
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.indexes", "sysindexes", "sys_indexes":
		columns = []string{
			"object_id", "name", "index_name", "index_id", "type", "type_desc",
			"is_unique", "data_space_id", "ignore_dup_key", "is_primary_key",
			"is_unique_constraint", "fill_factor", "is_padded", "is_disabled",
			"is_hypothetical", "allow_row_locks", "allow_page_locks", "has_filter",
			"filter_definition", "auto_created", "table_name", "columns",
		}
		for i, t := range tables {
			objID := tableObjectID(i)
			hasPK := false
			if pkCol, _ := t.PrimaryKeyColumn(); pkCol != nil {
				hasPK = true
				allRows = append(allRows, map[string]any{
					"object_id":            objID,
					"name":                 "PK_" + t.Name,
					"index_name":           "PK_" + t.Name,
					"index_id":             1,
					"type":                 1,
					"type_desc":            "CLUSTERED",
					"is_unique":            true,
					"data_space_id":        1,
					"ignore_dup_key":       false,
					"is_primary_key":       true,
					"is_unique_constraint": true,
					"fill_factor":          0,
					"is_padded":            false,
					"is_disabled":          false,
					"is_hypothetical":      false,
					"allow_row_locks":      true,
					"allow_page_locks":     true,
					"has_filter":           false,
					"filter_definition":    nil,
					"auto_created":         false,
					"table_name":           t.Name,
					"columns":              pkCol.Name,
				})
			}
			if !hasPK {
				allRows = append(allRows, map[string]any{
					"object_id":            objID,
					"name":                 nil,
					"index_name":           nil,
					"index_id":             0,
					"type":                 0,
					"type_desc":            "HEAP",
					"is_unique":            false,
					"data_space_id":        1,
					"ignore_dup_key":       false,
					"is_primary_key":       false,
					"is_unique_constraint": false,
					"fill_factor":          0,
					"is_padded":            false,
					"is_disabled":          false,
					"is_hypothetical":      false,
					"allow_row_locks":      true,
					"allow_page_locks":     true,
					"has_filter":           false,
					"filter_definition":    nil,
					"auto_created":         false,
					"table_name":           t.Name,
					"columns":              "",
				})
			}
			for idxSeq, idx := range t.Indexes {
				allRows = append(allRows, map[string]any{
					"object_id":            objID,
					"name":                 idx.Name,
					"index_name":           idx.Name,
					"index_id":             2 + idxSeq,
					"type":                 2,
					"type_desc":            "NONCLUSTERED",
					"is_unique":            idx.Unique,
					"data_space_id":        1,
					"ignore_dup_key":       false,
					"is_primary_key":       false,
					"is_unique_constraint": false,
					"fill_factor":          0,
					"is_padded":            false,
					"is_disabled":          false,
					"is_hypothetical":      false,
					"allow_row_locks":      true,
					"allow_page_locks":     true,
					"has_filter":           false,
					"filter_definition":    nil,
					"auto_created":         false,
					"table_name":           t.Name,
					"columns":              strings.Join(idx.Columns, ", "),
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.databases", "sysdatabases", "sys_databases":
		columns = []string{
			"name", "schema_name", "database_id", "source_database_id", "owner_sid",
			"create_date", "compatibility_level", "collation_name", "user_access",
			"user_access_desc", "is_read_only", "is_auto_close_on", "is_auto_shrink_on",
			"state", "state_desc", "is_in_standby", "snapshot_isolation_state",
			"snapshot_isolation_state_desc", "is_read_committed_snapshot_on",
			"recovery_model", "recovery_model_desc", "page_verify_option",
			"page_verify_option_desc", "is_auto_create_stats_on", "is_auto_update_stats_on",
			"containment", "containment_desc", "is_fulltext_enabled", "is_distributor",
			"is_cdc_enabled", "is_encrypted", "catalog_collation_type", "catalog_collation_type_desc",
		}
		dbList := []struct {
			name string
			id   int
		}{
			{"master", 1},
			{"tempdb", 2},
			{"model", 3},
			{"msdb", 4},
			{"lionsql", 5},
		}
		for _, db := range dbList {
			allRows = append(allRows, map[string]any{
				"name":                          db.name,
				"schema_name":                   "dbo",
				"database_id":                   db.id,
				"source_database_id":            nil,
				"owner_sid":                     nil,
				"create_date":                   defaultDate,
				"compatibility_level":           150,
				"collation_name":                "SQL_Latin1_General_CP1_CI_AS",
				"user_access":                   0,
				"user_access_desc":              "MULTI_USER",
				"is_read_only":                  false,
				"is_auto_close_on":              false,
				"is_auto_shrink_on":             false,
				"state":                         0,
				"state_desc":                    "ONLINE",
				"is_in_standby":                 false,
				"snapshot_isolation_state":      1,
				"snapshot_isolation_state_desc": "ON",
				"is_read_committed_snapshot_on": true,
				"recovery_model":                1,
				"recovery_model_desc":           "FULL",
				"page_verify_option":            2,
				"page_verify_option_desc":       "CHECKSUM",
				"is_auto_create_stats_on":       true,
				"is_auto_update_stats_on":       true,
				"containment":                   0,
				"containment_desc":              "NONE",
				"is_fulltext_enabled":           false,
				"is_distributor":                false,
				"is_cdc_enabled":                false,
				"is_encrypted":                  false,
				"catalog_collation_type":        int64(0),
				"catalog_collation_type_desc":   "DATABASE_DEFAULT",
			})
		}
		return columns, allRows, true, nil

	case "sys.resource_governor_configuration", "sysresourcegovernorconfiguration", "sys_resource_governor_configuration":
		columns = []string{
			"classifier_function_id", "is_enabled", "max_outstanding_io_per_volume",
		}
		allRows = []map[string]any{
			{
				"classifier_function_id":        int32(0),
				"is_enabled":                    false,
				"max_outstanding_io_per_volume": int32(0),
			},
		}
		return columns, allRows, true, nil

	case "sys.procedures", "sysprocedures", "sys_procedures":
		columns = []string{
			"name", "object_id", "principal_id", "schema_id", "parent_object_id",
			"type", "type_desc", "create_date", "modify_date",
			"is_ms_shipped", "is_published", "is_schema_published",
			"is_auto_executed", "is_execution_replicated",
			"routine_name", "routine_type", "routine_schema", "created_date",
		}
		if src != nil {
			for pIdx, p := range src.ListProcedures() {
				createdDate := p.CreatedAt
				if createdDate.IsZero() {
					createdDate = defaultDate
				}
				updatedDate := p.UpdatedAt
				if updatedDate.IsZero() {
					updatedDate = createdDate
				}
				allRows = append(allRows, map[string]any{
					"name":                    p.Name,
					"object_id":               int32(procObjectID(pIdx)),
					"principal_id":            nil,
					"schema_id":               int32(1),
					"parent_object_id":        int32(0),
					"type":                    "P ",
					"type_desc":               "SQL_STORED_PROCEDURE",
					"create_date":             createdDate,
					"modify_date":             updatedDate,
					"is_ms_shipped":           false,
					"is_published":            false,
					"is_schema_published":     false,
					"is_auto_executed":        false,
					"is_execution_replicated": false,
					"routine_name":            p.Name,
					"routine_type":            "PROCEDURE",
					"routine_schema":          "dbo",
					"created_date":            createdDate,
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.parameters", "sysparameters", "sys_parameters", "sys.all_parameters", "sysallparameters", "sys_all_parameters":
		columns = []string{
			"object_id", "name", "parameter_id", "system_type_id", "user_type_id",
			"max_length", "precision", "scale", "is_output", "is_cursor_ref",
			"has_default_value", "is_xml_document", "default_value",
		}
		if src != nil {
			for pIdx, p := range src.ListProcedures() {
				objID := int32(procObjectID(pIdx))
				for paramIdx, param := range p.Params {
					paramID := int32(paramIdx + 1)
					allRows = append(allRows, map[string]any{
						"object_id":         objID,
						"name":              param.Name,
						"parameter_id":      paramID,
						"system_type_id":    int32(56),
						"user_type_id":      int32(56),
						"max_length":        int16(8),
						"precision":         uint8(10),
						"scale":             uint8(0),
						"is_output":         false,
						"is_cursor_ref":     false,
						"has_default_value": false,
						"is_xml_document":   false,
						"default_value":     nil,
					})
				}
			}
		}
		return columns, allRows, true, nil

	case "sys.table_stats", "systablestats", "sys_table_stats":
		columns = []string{"table_name", "row_count", "page_count", "last_analyzed"}
		if statProvider, ok := src.(interface{ ListTableStats() map[string]*planner.TableStats }); ok {
			for _, st := range statProvider.ListTableStats() {
				allRows = append(allRows, map[string]any{
					"table_name":    st.TableName,
					"row_count":     st.RowCount,
					"page_count":    st.PageCount,
					"last_analyzed": st.AnalyzedAt.Format(time.RFC3339),
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.column_stats", "syscolumnstats", "sys_column_stats":
		columns = []string{"table_name", "column_name", "null_count", "distinct_count", "min_value", "max_value", "histogram_buckets"}
		if statProvider, ok := src.(interface{ ListTableStats() map[string]*planner.TableStats }); ok {
			for _, st := range statProvider.ListTableStats() {
				for _, col := range st.Columns {
					minStr := ""
					if !col.MinVal.IsNull {
						minStr = col.MinVal.String()
					}
					maxStr := ""
					if !col.MaxVal.IsNull {
						maxStr = col.MaxVal.String()
					}
					allRows = append(allRows, map[string]any{
						"table_name":        st.TableName,
						"column_name":       col.Name,
						"null_count":        col.NullCount,
						"distinct_count":    col.DistinctCount,
						"min_value":         minStr,
						"max_value":         maxStr,
						"histogram_buckets": len(col.Histogram),
					})
				}
			}
		}
		return columns, allRows, true, nil

	case "sys.schemas", "sysschemas", "sys_schemas":
		columns = []string{"name", "schema_id", "principal_id"}
		allRows = append(allRows,
			map[string]any{"name": "dbo", "schema_id": 1, "principal_id": 1},
			map[string]any{"name": "guest", "schema_id": 2, "principal_id": 2},
			map[string]any{"name": "sys", "schema_id": 3, "principal_id": 3},
			map[string]any{"name": "information_schema", "schema_id": 4, "principal_id": 4},
		)
		return columns, allRows, true, nil

	case "sys.objects", "sysobjects", "sys_objects", "sys.all_objects", "sysallobjects", "sys_all_objects":
		columns = []string{
			"name", "object_id", "id", "uid", "principal_id", "schema_id", "parent_object_id",
			"type", "xtype", "type_desc", "create_date", "modify_date",
			"is_ms_shipped", "is_published", "is_schema_published",
		}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"name":                t.Name,
				"object_id":           tableObjectID(i),
				"id":                  tableObjectID(i),
				"uid":                 1,
				"principal_id":        nil,
				"schema_id":           1,
				"parent_object_id":    0,
				"type":                "U ",
				"xtype":               "U ",
				"type_desc":           "USER_TABLE",
				"create_date":         defaultDate,
				"modify_date":         defaultDate,
				"is_ms_shipped":       false,
				"is_published":        false,
				"is_schema_published": false,
			})
		}
		if src != nil {
			for vIdx, v := range src.ListViews() {
				allRows = append(allRows, map[string]any{
					"name":                v.Name,
					"object_id":           viewObjectID(vIdx),
					"id":                  viewObjectID(vIdx),
					"uid":                 1,
					"principal_id":        nil,
					"schema_id":           1,
					"parent_object_id":    0,
					"type":                "V ",
					"xtype":               "V ",
					"type_desc":           "VIEW",
					"create_date":         defaultDate,
					"modify_date":         defaultDate,
					"is_ms_shipped":       false,
					"is_published":        false,
					"is_schema_published": false,
				})
			}
			for pIdx, p := range src.ListProcedures() {
				allRows = append(allRows, map[string]any{
					"name":                p.Name,
					"object_id":           procObjectID(pIdx),
					"id":                  procObjectID(pIdx),
					"uid":                 1,
					"principal_id":        nil,
					"schema_id":           1,
					"parent_object_id":    0,
					"type":                "P ",
					"xtype":               "P ",
					"type_desc":           "SQL_STORED_PROCEDURE",
					"create_date":         p.CreatedAt,
					"modify_date":         p.CreatedAt,
					"is_ms_shipped":       false,
					"is_published":        false,
					"is_schema_published": false,
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.views", "sysviews", "sys_views", "sys.all_views", "sysallviews", "sys_all_views":
		columns = []string{"name", "object_id", "schema_id", "create_date", "modify_date"}
		if src != nil {
			for vIdx, v := range src.ListViews() {
				allRows = append(allRows, map[string]any{
					"name":        v.Name,
					"object_id":   viewObjectID(vIdx),
					"schema_id":   1,
					"create_date": defaultDate,
					"modify_date": defaultDate,
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.types", "systypes", "sys_types":
		columns = []string{"name", "system_type_id", "user_type_id", "schema_id", "max_length", "precision", "scale", "is_nullable"}
		allRows = append(allRows,
			map[string]any{"name": "int", "system_type_id": 56, "user_type_id": 56, "schema_id": 4, "max_length": 4, "precision": 10, "scale": 0, "is_nullable": true},
			map[string]any{"name": "real", "system_type_id": 59, "user_type_id": 59, "schema_id": 4, "max_length": 4, "precision": 24, "scale": 0, "is_nullable": true},
			map[string]any{"name": "timestamp", "system_type_id": 189, "user_type_id": 189, "schema_id": 4, "max_length": 8, "precision": 0, "scale": 0, "is_nullable": false},
			map[string]any{"name": "rowversion", "system_type_id": 189, "user_type_id": 189, "schema_id": 4, "max_length": 8, "precision": 0, "scale": 0, "is_nullable": false},
			map[string]any{"name": "varchar", "system_type_id": 167, "user_type_id": 167, "schema_id": 4, "max_length": 8000, "precision": 0, "scale": 0, "is_nullable": true},
			map[string]any{"name": "float", "system_type_id": 62, "user_type_id": 62, "schema_id": 4, "max_length": 8, "precision": 53, "scale": 0, "is_nullable": true},
			map[string]any{"name": "bit", "system_type_id": 104, "user_type_id": 104, "schema_id": 4, "max_length": 1, "precision": 1, "scale": 0, "is_nullable": true},
			map[string]any{"name": "datetime", "system_type_id": 61, "user_type_id": 61, "schema_id": 4, "max_length": 8, "precision": 23, "scale": 3, "is_nullable": true},
		)
		return columns, allRows, true, nil

	case "sys.partitions", "system.partitions", "syspartitions", "sys_partitions":
		columns = []string{"table_name", "partition", "row_count"}
		if partProvider, ok := src.(interface {
			GetPartitions(tableName string) ([]storage.PartitionInfo, error)
		}); ok {
			for _, t := range tables {
				if t.IsPartitioned() {
					if parts, pErr := partProvider.GetPartitions(t.Name); pErr == nil {
						for _, p := range parts {
							allRows = append(allRows, map[string]any{
								"table_name": t.Name,
								"partition":  p.Name,
								"row_count":  p.RowCount,
							})
						}
					}
				}
			}
		}
		return columns, allRows, true, nil

	case "sys.computed_columns", "syscomputedcolumns", "sys_computed_columns":
		columns = []string{"object_id", "name", "column_id", "definition", "is_persisted"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sys.triggers", "systriggers", "sys_triggers":
		columns = []string{"name", "object_id", "parent_id", "type", "type_desc", "create_date"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sys.foreign_keys", "sysforeignkeys", "sys_foreign_keys":
		columns = []string{"name", "object_id", "parent_object_id", "referenced_object_id"}
		for tIdx, t := range tables {
			for fkIdx, fk := range t.ForeignKeys {
				allRows = append(allRows, map[string]any{
					"name":                 fk.Name,
					"object_id":            3000 + tIdx*100 + fkIdx,
					"parent_object_id":     tableObjectID(tIdx),
					"referenced_object_id": 1000,
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.foreign_key_columns", "sysforeignkeycolumns", "sys_foreign_key_columns":
		columns = []string{"constraint_object_id", "parent_object_id", "parent_column_id", "referenced_object_id", "referenced_column_id"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sys.configurations", "sysconfigurations", "sys_configurations":
		columns = []string{
			"configuration_id", "name", "value", "minimum", "maximum",
			"value_in_use", "description", "is_dynamic", "is_advanced",
		}
		allRows = []map[string]any{
			{"configuration_id": 101, "name": "recovery interval (min)", "value": 0, "minimum": 0, "maximum": 32767, "value_in_use": 0, "description": "Maximum recovery interval in minutes", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 103, "name": "max worker threads", "value": 0, "minimum": 128, "maximum": 65535, "value_in_use": 0, "description": "Maximum worker threads", "is_dynamic": false, "is_advanced": true},
			{"configuration_id": 109, "name": "network packet size (B)", "value": 4096, "minimum": 512, "maximum": 32767, "value_in_use": 4096, "description": "Network packet size", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 115, "name": "nested triggers", "value": 1, "minimum": 0, "maximum": 1, "value_in_use": 1, "description": "Allow triggers to be invoked recursively", "is_dynamic": true, "is_advanced": false},
			{"configuration_id": 503, "name": "max server memory (MB)", "value": 2147483647, "minimum": 128, "maximum": 2147483647, "value_in_use": 2147483647, "description": "Maximum size of server memory (MB)", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 518, "name": "show advanced options", "value": 0, "minimum": 0, "maximum": 1, "value_in_use": 0, "description": "Show advanced options", "is_dynamic": true, "is_advanced": false},
			{"configuration_id": 1538, "name": "remote access", "value": 1, "minimum": 0, "maximum": 1, "value_in_use": 1, "description": "Allow remote access", "is_dynamic": false, "is_advanced": false},
			{"configuration_id": 1568, "name": "clr enabled", "value": 0, "minimum": 0, "maximum": 1, "value_in_use": 0, "description": "CLR integration enabled", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 1576, "name": "remote admin connections", "value": 0, "minimum": 0, "maximum": 1, "value_in_use": 0, "description": "Dedicated Admin Connection (DAC) allowed from remote clients", "is_dynamic": true, "is_advanced": true},
			{"configuration_id": 1579, "name": "filestream access level", "value": 0, "minimum": 0, "maximum": 2, "value_in_use": 0, "description": "FileStream access level", "is_dynamic": true, "is_advanced": true},
		}
		return columns, allRows, true, nil

	case "sys.dm_exec_sessions", "sysdmexecsessions", "sys_dm_exec_sessions":
		columns = []string{
			"session_id", "login_time", "host_name", "program_name", "host_process_id",
			"client_version", "client_interface_name", "security_id", "login_name",
			"nt_domain", "nt_user_name", "status", "cpu_time", "memory_usage",
			"total_scheduled_time", "total_elapsed_time", "endpoint_id",
			"last_request_start_time", "last_request_end_time", "reads", "writes",
			"logical_reads", "is_user_process", "text_size", "language", "date_format",
			"date_first", "quoted_identifier", "arithabort", "ansi_null_dflt_on",
			"ansi_defaults", "ansi_warnings", "ansi_padding", "ansi_nulls",
			"concat_null_yields_null", "transaction_isolation_level", "lock_timeout",
			"deadlock_priority", "row_count", "prev_error", "original_login_name", "database_id",
		}
		allRows = append(allRows, map[string]any{
			"session_id":                  int16(51),
			"login_time":                  defaultDate,
			"host_name":                   "localhost",
			"program_name":                "Microsoft SQL Server Management Studio",
			"host_process_id":             1000,
			"client_version":              0,
			"client_interface_name":       "Microsoft.Data.SqlClient",
			"security_id":                 nil,
			"login_name":                  "sa",
			"nt_domain":                   "",
			"nt_user_name":                "",
			"status":                      "running",
			"cpu_time":                    0,
			"memory_usage":                1024,
			"total_scheduled_time":        0,
			"total_elapsed_time":          0,
			"endpoint_id":                 1,
			"last_request_start_time":     defaultDate,
			"last_request_end_time":       defaultDate,
			"reads":                       int64(0),
			"writes":                      int64(0),
			"logical_reads":               int64(0),
			"is_user_process":             true,
			"text_size":                   4096,
			"language":                    "us_english",
			"date_format":                 "mdy",
			"date_first":                  int16(7),
			"quoted_identifier":           true,
			"arithabort":                  true,
			"ansi_null_dflt_on":           true,
			"ansi_defaults":               false,
			"ansi_warnings":               true,
			"ansi_padding":                true,
			"ansi_nulls":                  true,
			"concat_null_yields_null":     true,
			"transaction_isolation_level": int16(2),
			"lock_timeout":                -1,
			"deadlock_priority":           0,
			"row_count":                   int64(0),
			"prev_error":                  0,
			"original_login_name":         "sa",
			"database_id":                 int16(1),
		})
		return columns, allRows, true, nil

	case "sys.dm_exec_connections", "sysdmexecconnections", "sys_dm_exec_connections":
		columns = []string{
			"session_id", "most_recent_session_id", "connect_time", "net_transport",
			"protocol_type", "protocol_version", "endpoint_id", "encrypt_option",
			"auth_scheme", "node_affinity", "num_reads", "num_writes",
			"last_read", "last_write", "net_packet_size", "client_net_address",
			"client_tcp_port", "local_net_address", "local_tcp_port", "connection_id",
		}
		allRows = append(allRows, map[string]any{
			"session_id":             51,
			"most_recent_session_id": 51,
			"connect_time":           defaultDate,
			"net_transport":          "TCP",
			"protocol_type":          "TSQL",
			"protocol_version":       0x74000004,
			"endpoint_id":            1,
			"encrypt_option":         "TRUE",
			"auth_scheme":            "SQL",
			"node_affinity":          int16(0),
			"num_reads":              1,
			"num_writes":             1,
			"last_read":              defaultDate,
			"last_write":             defaultDate,
			"net_packet_size":        4096,
			"client_net_address":     "127.0.0.1",
			"client_tcp_port":        1433,
			"local_net_address":      "127.0.0.1",
			"local_tcp_port":         1433,
			"connection_id":          "00000000-0000-0000-0000-000000000001",
		})
		return columns, allRows, true, nil

	case "sys.database_principals", "sysdatabaseprincipals", "sys_database_principals":
		columns = []string{
			"name", "principal_id", "type", "type_desc", "default_schema_name",
			"create_date", "modify_date", "owning_principal_id", "sid",
			"is_fixed_role", "authentication_type", "authentication_type_desc",
			"default_language_name", "default_language_lcid",
		}
		allRows = []map[string]any{
			{"name": "dbo", "principal_id": 1, "type": "S", "type_desc": "SQL_USER", "default_schema_name": "dbo", "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 2, "authentication_type_desc": "INSTANCE", "default_language_name": "us_english", "default_language_lcid": 1033},
			{"name": "guest", "principal_id": 2, "type": "S", "type_desc": "SQL_USER", "default_schema_name": "guest", "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
			{"name": "INFORMATION_SCHEMA", "principal_id": 3, "type": "S", "type_desc": "SQL_USER", "default_schema_name": nil, "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
			{"name": "sys", "principal_id": 4, "type": "S", "type_desc": "SQL_USER", "default_schema_name": nil, "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
			{"name": "public", "principal_id": 0, "type": "R", "type_desc": "DATABASE_ROLE", "default_schema_name": nil, "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": nil, "sid": nil, "is_fixed_role": false, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
			{"name": "db_owner", "principal_id": 16384, "type": "R", "type_desc": "DATABASE_ROLE", "default_schema_name": nil, "create_date": defaultDate, "modify_date": defaultDate, "owning_principal_id": 1, "sid": nil, "is_fixed_role": true, "authentication_type": 0, "authentication_type_desc": "NONE", "default_language_name": nil, "default_language_lcid": nil},
		}
		return columns, allRows, true, nil

	case "sys.server_principals", "sysserverprincipals", "sys_server_principals":
		columns = []string{
			"name", "principal_id", "sid", "type", "type_desc", "is_disabled",
			"create_date", "modify_date", "default_database_name", "default_language_name",
			"is_fixed_role",
		}
		allRows = []map[string]any{
			{"name": "sa", "principal_id": 1, "sid": nil, "type": "S", "type_desc": "SQL_LOGIN", "is_disabled": false, "create_date": defaultDate, "modify_date": defaultDate, "default_database_name": "master", "default_language_name": "us_english", "is_fixed_role": false},
			{"name": "public", "principal_id": 2, "sid": nil, "type": "R", "type_desc": "SERVER_ROLE", "is_disabled": false, "create_date": defaultDate, "modify_date": defaultDate, "default_database_name": nil, "default_language_name": nil, "is_fixed_role": false},
			{"name": "sysadmin", "principal_id": 3, "sid": nil, "type": "R", "type_desc": "SERVER_ROLE", "is_disabled": false, "create_date": defaultDate, "modify_date": defaultDate, "default_database_name": nil, "default_language_name": nil, "is_fixed_role": true},
		}
		return columns, allRows, true, nil

	case "sys.dm_os_sys_info", "sysdmosysinfo", "sys_dm_os_sys_info":
		columns = []string{
			"cpu_count", "hyperthread_ratio", "physical_memory_kb", "virtual_memory_kb",
			"sqlserver_start_time", "affinity_type", "affinity_type_desc", "os_priority_class",
		}
		allRows = []map[string]any{
			{
				"cpu_count":            runtime.NumCPU(),
				"hyperthread_ratio":    runtime.NumCPU(),
				"physical_memory_kb":   int64(16 * 1024 * 1024),
				"virtual_memory_kb":    int64(16 * 1024 * 1024),
				"sqlserver_start_time": defaultDate,
				"affinity_type":        1,
				"affinity_type_desc":   "AUTO",
				"os_priority_class":    32,
			},
		}
		return columns, allRows, true, nil

	case "sys.dm_os_host_info", "sysdmoshostinfo", "sys_dm_os_host_info":
		columns = []string{
			"host_platform", "host_distribution", "host_release",
			"host_service_pack_level", "os_architecture", "host_sku",
		}
		// [RULE:Invariant11] Permanent "Windows" Host Platform Invariant
		platform := "Windows"
		dist := "Windows"
		release := "10.0"
		arch := "64-bit"
		if runtime.GOARCH == "386" || runtime.GOARCH == "arm" {
			arch = "32-bit"
		}
		allRows = []map[string]any{
			{
				"host_platform":           platform,
				"host_distribution":       dist,
				"host_release":            release,
				"host_service_pack_level": "",
				"os_architecture":         arch,
				"host_sku":                48,
			},
		}
		return columns, allRows, true, nil

	case "sys.endpoints", "sysendpoints", "sys_endpoints":
		columns = []string{
			"endpoint_id", "principal_id", "name", "protocol", "protocol_desc",
			"type", "type_desc", "state", "state_desc", "is_admin_endpoint",
		}
		allRows = []map[string]any{
			{"endpoint_id": 1, "principal_id": 1, "name": "Dedicated Admin Connection", "protocol": 2, "protocol_desc": "TCP", "type": 2, "type_desc": "TSQL", "state": 0, "state_desc": "STARTED", "is_admin_endpoint": true},
			{"endpoint_id": 2, "principal_id": 1, "name": "TSQL Default TCP", "protocol": 2, "protocol_desc": "TCP", "type": 2, "type_desc": "TSQL", "state": 0, "state_desc": "STARTED", "is_admin_endpoint": false},
		}
		return columns, allRows, true, nil

	case "sys.data_spaces", "sysdataspaces", "sys_data_spaces":
		columns = []string{"data_space_id", "name", "type", "type_desc", "is_default", "is_system"}
		allRows = []map[string]any{
			{"data_space_id": 1, "name": "PRIMARY", "type": "FG", "type_desc": "ROWS_FILEGROUP", "is_default": true, "is_system": false},
		}
		return columns, allRows, true, nil

	case "sys.extended_properties", "sysextendedproperties", "sys_extended_properties":
		columns = []string{"class", "class_desc", "major_id", "minor_id", "name", "value"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sys.synonyms", "syssynonyms", "sys_synonyms":
		columns = []string{
			"name", "object_id", "principal_id", "schema_id", "parent_object_id",
			"type", "type_desc", "create_date", "modify_date", "is_ms_shipped",
			"is_published", "is_schema_published", "base_object_name",
		}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sys.sql_modules", "syssqlmodules", "sys_sql_modules",
		"sys.all_sql_modules", "sysallsqlmodules", "sys_all_sql_modules",
		"sys.system_sql_modules", "syssystemsqlmodules", "sys_system_sql_modules":
		columns = []string{
			"object_id", "definition", "uses_ansi_nulls", "uses_quoted_identifier",
			"is_schema_bound", "uses_database_collation", "is_recompiled",
			"null_on_null_input", "execute_as_principal_id", "uses_native_compilation",
		}
		allRows = []map[string]any{}
		if src != nil {
			views := src.GetSortedViews()
			if len(views) == 0 {
				views = src.ListViews()
			}
			for vIdx, v := range views {
				allRows = append(allRows, map[string]any{
					"object_id":               int32(viewObjectID(vIdx)),
					"definition":              v.Query,
					"uses_ansi_nulls":         true,
					"uses_quoted_identifier":   true,
					"is_schema_bound":         false,
					"uses_database_collation": false,
					"is_recompiled":           false,
					"null_on_null_input":      false,
					"execute_as_principal_id": nil,
					"uses_native_compilation": int32(0),
				})
			}
			procs := src.GetSortedProcedures()
			if len(procs) == 0 {
				procs = src.ListProcedures()
			}
			for pIdx, p := range procs {
				allRows = append(allRows, map[string]any{
					"object_id":               int32(procObjectID(pIdx)),
					"definition":              p.Body,
					"uses_ansi_nulls":         true,
					"uses_quoted_identifier":   true,
					"is_schema_bound":         false,
					"uses_database_collation": false,
					"is_recompiled":           false,
					"null_on_null_input":      false,
					"execute_as_principal_id": nil,
					"uses_native_compilation": int32(0),
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.syscomments", "syscomments", "sys_syscomments":
		columns = []string{"id", "number", "colid", "status", "ctext", "texttype", "language", "encrypted", "compressed", "text"}
		allRows = []map[string]any{}
		if src != nil {
			views := src.GetSortedViews()
			if len(views) == 0 {
				views = src.ListViews()
			}
			for vIdx, v := range views {
				allRows = append(allRows, map[string]any{
					"id":         int32(viewObjectID(vIdx)),
					"number":     int32(1),
					"colid":      int32(1),
					"status":     int32(0),
					"ctext":      nil,
					"texttype":   int32(0),
					"language":   int32(0),
					"encrypted":  false,
					"compressed": false,
					"text":       v.Query,
				})
			}
			procs := src.GetSortedProcedures()
			if len(procs) == 0 {
				procs = src.ListProcedures()
			}
			for pIdx, p := range procs {
				allRows = append(allRows, map[string]any{
					"id":         int32(procObjectID(pIdx)),
					"number":     int32(1),
					"colid":      int32(1),
					"status":     int32(0),
					"ctext":      nil,
					"texttype":   int32(0),
					"language":   int32(0),
					"encrypted":  false,
					"compressed": false,
					"text":       p.Body,
				})
			}
		}
		return columns, allRows, true, nil

	case "sys.database_files", "sysdatabasefiles", "sys_database_files":
		columns = []string{"file_id", "file_guid", "type", "type_desc", "data_space_id", "name", "physical_name", "state", "state_desc", "size", "max_size", "growth", "is_media_read_only", "is_read_only", "is_sparse", "is_percent_growth"}
		allRows = append(allRows,
			map[string]any{
				"file_id":            1,
				"file_guid":          nil,
				"type":               0,
				"type_desc":          "ROWS",
				"data_space_id":      1,
				"name":               "lionsql_data",
				"physical_name":      "lionsql.mdf",
				"state":              0,
				"state_desc":         "ONLINE",
				"size":               2048,
				"max_size":           -1,
				"growth":             1024,
				"is_media_read_only": false,
				"is_read_only":       false,
				"is_sparse":          false,
				"is_percent_growth":  false,
			},
			map[string]any{
				"file_id":            2,
				"file_guid":          nil,
				"type":               1,
				"type_desc":          "LOG",
				"data_space_id":      0,
				"name":               "lionsql_log",
				"physical_name":      "lionsql_log.ldf",
				"state":              0,
				"state_desc":         "ONLINE",
				"size":               1024,
				"max_size":           -1,
				"growth":             1024,
				"is_media_read_only": false,
				"is_read_only":       false,
				"is_sparse":          false,
				"is_percent_growth":  false,
			},
		)
		return columns, allRows, true, nil

	case "syspolicy_configuration", "dbo.syspolicy_configuration", "sys.syspolicy_configuration":
		columns = []string{"name", "current_value", "description"}
		allRows = []map[string]any{
			{"name": "Enabled", "current_value": false, "description": "Determines whether Policy-Based Management is enabled"},
			{"name": "HistoryRetentionInDays", "current_value": int32(0), "description": "Number of days to retain history"},
			{"name": "LogOnSuccess", "current_value": false, "description": "Whether to log successful policy evaluations"},
		}
		return columns, allRows, true, nil

	case "syspolicy_policies", "dbo.syspolicy_policies", "sys.syspolicy_policies":
		columns = []string{
			"policy_id", "name", "condition_id", "root_condition_id", "date_created",
			"execution_mode", "policy_category", "schedule_uid", "description",
			"help_text", "help_link", "object_set_id", "is_enabled", "job_id",
		}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "syspolicy_conditions", "dbo.syspolicy_conditions", "sys.syspolicy_conditions":
		columns = []string{
			"condition_id", "name", "facet_id", "expression", "is_name_condition",
			"date_created", "modify_date", "created_by", "modified_by",
		}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "syspolicy_facets", "dbo.syspolicy_facets", "sys.syspolicy_facets":
		columns = []string{"facet_id", "name", "description"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "syspolicy_policy_categories", "dbo.syspolicy_policy_categories", "sys.syspolicy_policy_categories":
		columns = []string{"policy_category_id", "name"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "syspolicy_target_sets", "dbo.syspolicy_target_sets", "sys.syspolicy_target_sets":
		columns = []string{"target_set_id", "object_set_id", "type_skeleton", "type"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "syspolicy_target_set_levels", "dbo.syspolicy_target_set_levels", "sys.syspolicy_target_set_levels":
		columns = []string{"target_set_level_id", "target_set_id", "level_name", "condition_id"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "syspolicy_policy_execution_history", "dbo.syspolicy_policy_execution_history", "sys.syspolicy_policy_execution_history":
		columns = []string{"history_id", "policy_id", "start_date", "end_date", "result"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "syspolicy_policy_execution_history_details", "dbo.syspolicy_policy_execution_history_details", "sys.syspolicy_policy_execution_history_details":
		columns = []string{"detail_id", "history_id", "target_query_expression", "result", "exception_message", "exception"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sysjobs", "dbo.sysjobs", "sys.sysjobs":
		columns = []string{"job_id", "name", "enabled", "description", "start_step_id", "category_id", "owner_sid", "notify_level_eventlog", "notify_level_email", "date_created", "date_modified", "version_number"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "syscategories", "dbo.syscategories", "sys.syscategories":
		columns = []string{"category_id", "category_type", "name"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sysjobsteps", "dbo.sysjobsteps", "sys.sysjobsteps":
		columns = []string{"job_id", "step_id", "step_name", "subsystem", "command", "flags"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sysschedules", "dbo.sysschedules", "sys.sysschedules":
		columns = []string{"schedule_id", "schedule_uid", "name", "enabled", "freq_type", "freq_interval"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sysjobhistory", "dbo.sysjobhistory", "sys.sysjobhistory":
		columns = []string{"instance_id", "job_id", "step_id", "step_name", "sql_message_id", "sql_severity", "message", "run_status", "run_date", "run_time", "run_duration"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sysalerts", "dbo.sysalerts", "sys.sysalerts":
		columns = []string{"id", "name", "message_id", "severity", "enabled", "delay_between_responses", "last_occurrence_date", "last_occurrence_time"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sysoperators", "dbo.sysoperators", "sys.sysoperators":
		columns = []string{"id", "name", "enabled", "email_address", "last_email_date", "last_email_time"}
		allRows = []map[string]any{}
		return columns, allRows, true, nil

	case "sys.master_files", "sysmasterfiles", "sys_master_files":
		columns = []string{"database_id", "file_id", "file_guid", "type", "type_desc", "data_space_id", "name", "physical_name", "state", "state_desc", "size", "max_size", "growth", "is_media_read_only", "is_read_only", "is_sparse", "is_percent_growth"}
		allRows = append(allRows,
			map[string]any{
				"database_id":        1,
				"file_id":            1,
				"file_guid":          nil,
				"type":               0,
				"type_desc":          "ROWS",
				"data_space_id":      1,
				"name":               "master",
				"physical_name":      "master.mdf",
				"state":              0,
				"state_desc":         "ONLINE",
				"size":               2048,
				"max_size":           -1,
				"growth":             1024,
				"is_media_read_only": false,
				"is_read_only":       false,
				"is_sparse":          false,
				"is_percent_growth":  false,
			},
			map[string]any{
				"database_id":        1,
				"file_id":            2,
				"file_guid":          nil,
				"type":               1,
				"type_desc":          "LOG",
				"data_space_id":      0,
				"name":               "mastlog",
				"physical_name":      "mastlog.ldf",
				"state":              0,
				"state_desc":         "ONLINE",
				"size":               1024,
				"max_size":           -1,
				"growth":             1024,
				"is_media_read_only": false,
				"is_read_only":       false,
				"is_sparse":          false,
				"is_percent_growth":  false,
			},
		)
		return columns, allRows, true, nil
	}

	return nil, nil, false, nil
}
