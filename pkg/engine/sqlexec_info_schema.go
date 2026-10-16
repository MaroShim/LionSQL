package engine

import (
	"fmt"
	"strings"

	"sqlion/pkg/storage"
)

func init() {
	registerCatalogView("information_schema.tables", (*Coordinator).generateInfoSchemaTables)
	registerCatalogView("information_schema.columns", (*Coordinator).generateInfoSchemaColumns)
	registerCatalogView("information_schema.schemata", (*Coordinator).generateInfoSchemaSchemata)
	registerCatalogView("information_schema.routines", (*Coordinator).generateInfoSchemaRoutines)
	registerCatalogView("information_schema.parameters", (*Coordinator).generateInfoSchemaParameters)
	registerCatalogView("information_schema.table_constraints", (*Coordinator).generateInfoSchemaTableConstraints)
	registerCatalogView("information_schema.key_column_usage", (*Coordinator).generateInfoSchemaKeyColumnUsage)
	registerCatalogView("information_schema.referential_constraints", (*Coordinator).generateInfoSchemaReferentialConstraints)
	registerCatalogView("information_schema.constraint_column_usage", (*Coordinator).generateInfoSchemaConstraintColumnUsage)
	registerCatalogView("information_schema.views", (*Coordinator).generateInfoSchemaViews)
	registerCatalogView("information_schema.constraint_table_usage", (*Coordinator).generateInfoSchemaConstraintTableUsage)
	registerCatalogView("information_schema.check_constraints", (*Coordinator).generateInfoSchemaCheckConstraints)
	registerCatalogView("information_schema.role_table_grants", (*Coordinator).generateInfoSchemaRoleTableGrants)
	registerCatalogView("information_schema.role_column_grants", (*Coordinator).generateInfoSchemaRoleColumnGrants)
	registerCatalogView("information_schema.sequences", (*Coordinator).generateInfoSchemaSequences)
	registerCatalogView("information_schema.enabled_roles", (*Coordinator).generateInfoSchemaEnabledRoles)
	registerCatalogView("information_schema.applicable_roles", (*Coordinator).generateInfoSchemaApplicableRoles)
}

func (s *Coordinator) generateInfoSchemaTables(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"table_catalog", "table_schema", "table_name", "table_type",
		"self_referencing_column_name", "reference_generation",
		"user_defined_type_catalog", "user_defined_type_schema",
		"user_defined_type_name", "is_insertable_into", "is_typed",
		"commit_action",
	}
	defSchema := "public"
	if strings.Contains(strings.ToLower(whereClause), "'dbo'") {
		defSchema = "dbo"
	}
	tables, _ := s.engine.ListTables()
	var allRows []map[string]any
	for _, t := range tables {
		allRows = append(allRows, map[string]any{
			"table_catalog":                "sqlion",
			"table_schema":                 defSchema,
			"table_name":                   t.Name,
			"table_type":                   "BASE TABLE",
			"self_referencing_column_name": nil,
			"reference_generation":         nil,
			"user_defined_type_catalog":    nil,
			"user_defined_type_schema":     nil,
			"user_defined_type_name":       nil,
			"is_insertable_into":           "YES",
			"is_typed":                     "NO",
			"commit_action":                nil,
		})
	}
	// Also include views
	if s.catalog != nil {
		for _, v := range s.catalog.ListViews() {
			allRows = append(allRows, map[string]any{
				"table_catalog":                "sqlion",
				"table_schema":                 "public",
				"table_name":                   v.Name,
				"table_type":                   "VIEW",
				"self_referencing_column_name": nil,
				"reference_generation":         nil,
				"user_defined_type_catalog":    nil,
				"user_defined_type_schema":     nil,
				"user_defined_type_name":       nil,
				"is_insertable_into":           "NO",
				"is_typed":                     "NO",
				"commit_action":                nil,
			})
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaColumns(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"table_catalog", "table_schema", "table_name",
		"column_name", "ordinal_position", "column_default",
		"is_nullable", "data_type", "character_maximum_length",
		"character_octet_length", "numeric_precision",
		"numeric_precision_radix", "numeric_scale",
		"datetime_precision", "interval_type", "interval_precision",
		"character_set_catalog", "character_set_schema",
		"character_set_name", "collation_catalog", "collation_schema",
		"collation_name", "domain_catalog", "domain_schema",
		"domain_name", "udt_catalog", "udt_schema", "udt_name",
		"scope_catalog", "scope_schema", "scope_name",
		"maximum_cardinality", "dtd_identifier", "is_self_referencing",
		"is_identity", "identity_generation", "identity_start",
		"identity_increment", "identity_maximum", "identity_minimum",
		"identity_cycle", "is_generated", "generation_expression",
		"is_updatable",
	}
	defSchema := "public"
	if strings.Contains(strings.ToLower(whereClause), "'dbo'") {
		defSchema = "dbo"
	}
	tables, _ := s.engine.ListTables()
	var allRows []map[string]any
	for _, t := range tables {
		for i, c := range t.Columns {
			nullable := "YES"
			if !c.Nullable {
				nullable = "NO"
			}
			dataType := pgTypeName(c.Type)
			var charMaxLen any
			var numPrec, numPrecRad, numScale any
			var dtPrec any
			switch c.Type {
			case storage.TypeVarChar:
				if c.Precision > 0 {
					charMaxLen = c.Precision
				} else {
					charMaxLen = 255
				}
				numPrec = nil
			case storage.TypeInt:
				numPrec = 32
				numPrecRad = 2
				numScale = 0
			case storage.TypeFloat:
				numPrec = 53
				numPrecRad = 2
			case storage.TypeDecimal:
				numPrec = c.Precision
				numPrecRad = 10
				numScale = c.Scale
			case storage.TypeDateTime:
				dtPrec = 6
			case storage.TypeDate, storage.TypeTime:
				dtPrec = 0
			}
			allRows = append(allRows, map[string]any{
				"table_catalog":            "sqlion",
				"table_schema":             defSchema,
				"table_name":               t.Name,
				"column_name":              c.Name,
				"ordinal_position":         i + 1,
				"column_default":           nil,
				"is_nullable":              nullable,
				"data_type":                dataType,
				"character_maximum_length": charMaxLen,
				"character_octet_length":   charMaxLen,
				"numeric_precision":        numPrec,
				"numeric_precision_radix":  numPrecRad,
				"numeric_scale":            numScale,
				"datetime_precision":       dtPrec,
				"interval_type":            nil,
				"interval_precision":       nil,
				"character_set_catalog":    nil,
				"character_set_schema":     nil,
				"character_set_name":       nil,
				"collation_catalog":        nil,
				"collation_schema":         nil,
				"collation_name":           nil,
				"domain_catalog":           nil,
				"domain_schema":            nil,
				"domain_name":              nil,
				"udt_catalog":              "sqlion",
				"udt_schema":               "pg_catalog",
				"udt_name":                 dataType,
				"scope_catalog":            nil,
				"scope_schema":             nil,
				"scope_name":               nil,
				"maximum_cardinality":      nil,
				"dtd_identifier":           fmt.Sprintf("%d", i+1),
				"is_self_referencing":      "NO",
				"is_identity":              "NO",
				"identity_generation":      nil,
				"identity_start":           nil,
				"identity_increment":       nil,
				"identity_maximum":         nil,
				"identity_minimum":         nil,
				"identity_cycle":           "NO",
				"is_generated":             "NEVER",
				"generation_expression":    nil,
				"is_updatable":             "YES",
			})
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaSchemata(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"catalog_name", "schema_name", "schema_owner",
		"default_character_set_catalog", "default_character_set_schema",
		"default_character_set_name", "sql_path",
	}
	allRows := []map[string]any{
		{"catalog_name": "sqlion", "schema_name": "dbo", "schema_owner": "sa", "default_character_set_catalog": nil, "default_character_set_schema": nil, "default_character_set_name": nil, "sql_path": nil},
		{"catalog_name": "sqlion", "schema_name": "public", "schema_owner": "postgres", "default_character_set_catalog": nil, "default_character_set_schema": nil, "default_character_set_name": nil, "sql_path": nil},
		{"catalog_name": "sqlion", "schema_name": "information_schema", "schema_owner": "postgres", "default_character_set_catalog": nil, "default_character_set_schema": nil, "default_character_set_name": nil, "sql_path": nil},
		{"catalog_name": "sqlion", "schema_name": "pg_catalog", "schema_owner": "postgres", "default_character_set_catalog": nil, "default_character_set_schema": nil, "default_character_set_name": nil, "sql_path": nil},
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaRoutines(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"specific_catalog", "specific_schema", "specific_name",
		"routine_catalog", "routine_schema", "routine_name",
		"routine_type", "module_catalog", "module_schema", "module_name",
		"udt_catalog", "udt_schema", "udt_name",
		"data_type", "character_maximum_length", "character_octet_length",
		"character_set_catalog", "character_set_schema", "character_set_name",
		"collation_catalog", "collation_schema", "collation_name",
		"numeric_precision", "numeric_precision_radix", "numeric_scale",
		"datetime_precision", "interval_type", "interval_precision",
		"type_udt_catalog", "type_udt_schema", "type_udt_name",
		"scope_catalog", "scope_schema", "scope_name",
		"maximum_cardinality", "dtd_identifier",
		"routine_body", "routine_definition", "external_name",
		"external_language", "parameter_style", "is_deterministic",
		"sql_data_access", "is_null_call", "sql_path",
		"schema_level_routine", "max_dynamic_result_sets",
		"is_user_defined_cast", "is_implicitly_invocable",
		"security_type", "to_sql_specific_catalog",
		"to_sql_specific_schema", "to_sql_specific_name",
		"as_locator", "created", "last_altered",
	}
	defSchema := "public"
	if strings.Contains(strings.ToLower(whereClause), "'dbo'") {
		defSchema = "dbo"
	}
	var allRows []map[string]any
	if s.catalog != nil {
		for _, p := range s.catalog.ListProcedures() {
			allRows = append(allRows, map[string]any{
				"specific_catalog":   "sqlion",
				"specific_schema":    defSchema,
				"specific_name":      p.Name,
				"routine_catalog":    "sqlion",
				"routine_schema":     defSchema,
				"routine_name":       p.Name,
				"routine_type":       "PROCEDURE",
				"routine_body":       "SQL",
				"routine_definition": p.Body,
				"external_language":  "SQL",
				"is_deterministic":   "NO",
				"sql_data_access":    "MODIFIES",
				"security_type":      "INVOKER",
				"created":            p.CreatedAt.Format("2006-01-02 15:04:05"),
				"last_altered":       p.CreatedAt.Format("2006-01-02 15:04:05"),
			})
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaParameters(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"specific_catalog", "specific_schema", "specific_name",
		"ordinal_position", "parameter_mode", "is_result",
		"as_locator", "parameter_name", "data_type",
		"character_maximum_length", "character_octet_length",
		"numeric_precision", "numeric_precision_radix", "numeric_scale",
		"datetime_precision", "interval_type", "interval_precision",
	}
	defSchema := "public"
	if strings.Contains(strings.ToLower(whereClause), "'dbo'") {
		defSchema = "dbo"
	}
	var allRows []map[string]any
	if s.catalog != nil {
		for _, p := range s.catalog.ListProcedures() {
			for i, param := range p.Params {
				dtName := pgTypeName(param.Type)
				allRows = append(allRows, map[string]any{
					"specific_catalog":         "sqlion",
					"specific_schema":          defSchema,
					"specific_name":            p.Name,
					"ordinal_position":         i + 1,
					"parameter_mode":           "IN",
					"is_result":                "NO",
					"as_locator":               "NO",
					"parameter_name":           param.Name,
					"data_type":                dtName,
					"character_maximum_length": nil,
					"character_octet_length":   nil,
					"numeric_precision":        nil,
					"numeric_precision_radix":  nil,
					"numeric_scale":            nil,
					"datetime_precision":       nil,
					"interval_type":            nil,
					"interval_precision":       nil,
				})
			}
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaTableConstraints(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"constraint_catalog", "constraint_schema", "constraint_name",
		"table_catalog", "table_schema", "table_name",
		"constraint_type", "is_deferrable", "initially_deferred",
		"enforced", "nulls_distinct",
	}
	tables, _ := s.engine.ListTables()
	var allRows []map[string]any
	for _, t := range tables {
		// PRIMARY KEY
		hasPK := false
		for _, c := range t.Columns {
			if c.PrimaryKey {
				hasPK = true
				break
			}
		}
		if hasPK {
			allRows = append(allRows, map[string]any{
				"constraint_catalog": "sqlion",
				"constraint_schema":  "public",
				"constraint_name":    fmt.Sprintf("%s_pkey", t.Name),
				"table_catalog":      "sqlion",
				"table_schema":       "public",
				"table_name":         t.Name,
				"constraint_type":    "PRIMARY KEY",
				"is_deferrable":      "NO",
				"initially_deferred": "NO",
				"enforced":           "YES",
				"nulls_distinct":     "YES",
			})
		}
		// UNIQUE
		for _, idx := range t.Indexes {
			if !idx.Unique {
				continue
			}
			allRows = append(allRows, map[string]any{
				"constraint_catalog": "sqlion",
				"constraint_schema":  "public",
				"constraint_name":    idx.Name,
				"table_catalog":      "sqlion",
				"table_schema":       "public",
				"table_name":         t.Name,
				"constraint_type":    "UNIQUE",
				"is_deferrable":      "NO",
				"initially_deferred": "NO",
				"enforced":           "YES",
				"nulls_distinct":     "YES",
			})
		}
		// FOREIGN KEY
		for _, fk := range t.ForeignKeys {
			allRows = append(allRows, map[string]any{
				"constraint_catalog": "sqlion",
				"constraint_schema":  "public",
				"constraint_name":    fk.Name,
				"table_catalog":      "sqlion",
				"table_schema":       "public",
				"table_name":         t.Name,
				"constraint_type":    "FOREIGN KEY",
				"is_deferrable":      "NO",
				"initially_deferred": "NO",
				"enforced":           "YES",
				"nulls_distinct":     "YES",
			})
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaKeyColumnUsage(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"constraint_catalog", "constraint_schema", "constraint_name",
		"table_catalog", "table_schema", "table_name",
		"column_name", "ordinal_position", "position_in_unique_constraint",
	}
	tables, _ := s.engine.ListTables()
	var allRows []map[string]any
	for _, t := range tables {
		// PRIMARY KEY columns
		pkPos := 1
		for cIdx, c := range t.Columns {
			if c.PrimaryKey {
				allRows = append(allRows, map[string]any{
					"constraint_catalog":            "sqlion",
					"constraint_schema":             "public",
					"constraint_name":               fmt.Sprintf("%s_pkey", t.Name),
					"table_catalog":                 "sqlion",
					"table_schema":                  "public",
					"table_name":                    t.Name,
					"column_name":                   c.Name,
					"ordinal_position":              cIdx + 1,
					"position_in_unique_constraint": pkPos,
				})
				pkPos++
			}
		}
		// UNIQUE index columns
		for _, idx := range t.Indexes {
			if !idx.Unique {
				continue
			}
			for pos, idxCol := range idx.Columns {
				for cIdx, c := range t.Columns {
					if strings.EqualFold(c.Name, idxCol) {
						allRows = append(allRows, map[string]any{
							"constraint_catalog":            "sqlion",
							"constraint_schema":             "public",
							"constraint_name":               idx.Name,
							"table_catalog":                 "sqlion",
							"table_schema":                  "public",
							"table_name":                    t.Name,
							"column_name":                   c.Name,
							"ordinal_position":              cIdx + 1,
							"position_in_unique_constraint": pos + 1,
						})
						break
					}
				}
			}
		}
		// FOREIGN KEY columns
		for _, fk := range t.ForeignKeys {
			for pos, fkCol := range fk.ChildColumns {
				for cIdx, c := range t.Columns {
					if strings.EqualFold(c.Name, fkCol) {
						allRows = append(allRows, map[string]any{
							"constraint_catalog":            "sqlion",
							"constraint_schema":             "public",
							"constraint_name":               fk.Name,
							"table_catalog":                 "sqlion",
							"table_schema":                  "public",
							"table_name":                    t.Name,
							"column_name":                   c.Name,
							"ordinal_position":              cIdx + 1,
							"position_in_unique_constraint": pos + 1,
						})
						break
					}
				}
			}
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaReferentialConstraints(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"constraint_catalog", "constraint_schema", "constraint_name",
		"unique_constraint_catalog", "unique_constraint_schema", "unique_constraint_name",
		"match_option", "update_rule", "delete_rule",
	}
	tables, _ := s.engine.ListTables()
	var allRows []map[string]any
	for _, t := range tables {
		for _, fk := range t.ForeignKeys {
			allRows = append(allRows, map[string]any{
				"constraint_catalog":        "sqlion",
				"constraint_schema":         "public",
				"constraint_name":           fk.Name,
				"unique_constraint_catalog": "sqlion",
				"unique_constraint_schema":  "public",
				"unique_constraint_name":    fmt.Sprintf("%s_pkey", fk.ParentTable),
				"match_option":              "NONE",
				"update_rule":               "NO ACTION",
				"delete_rule":               "NO ACTION",
			})
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaConstraintColumnUsage(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"table_catalog", "table_schema", "table_name",
		"column_name", "constraint_catalog", "constraint_schema", "constraint_name",
	}
	tables, _ := s.engine.ListTables()
	var allRows []map[string]any
	for _, t := range tables {
		// PK columns
		for _, c := range t.Columns {
			if c.PrimaryKey {
				allRows = append(allRows, map[string]any{
					"table_catalog":      "sqlion",
					"table_schema":       "public",
					"table_name":         t.Name,
					"column_name":        c.Name,
					"constraint_catalog": "sqlion",
					"constraint_schema":  "public",
					"constraint_name":    fmt.Sprintf("%s_pkey", t.Name),
				})
			}
		}
		// FK referenced columns
		for _, fk := range t.ForeignKeys {
			for _, refCol := range fk.ParentColumns {
				allRows = append(allRows, map[string]any{
					"table_catalog":      "sqlion",
					"table_schema":       "public",
					"table_name":         fk.ParentTable,
					"column_name":        refCol,
					"constraint_catalog": "sqlion",
					"constraint_schema":  "public",
					"constraint_name":    fk.Name,
				})
			}
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaViews(whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"table_catalog", "table_schema", "table_name",
		"view_definition", "check_option", "is_updatable",
		"is_insertable_into", "is_trigger_updatable",
		"is_trigger_deletable", "is_trigger_insertable_into",
	}
	var allRows []map[string]any
	if s.catalog != nil {
		for _, v := range s.catalog.ListViews() {
			allRows = append(allRows, map[string]any{
				"table_catalog":              "sqlion",
				"table_schema":               "public",
				"table_name":                 v.Name,
				"view_definition":            v.Query,
				"check_option":               "NONE",
				"is_updatable":               "NO",
				"is_insertable_into":         "NO",
				"is_trigger_updatable":       "NO",
				"is_trigger_deletable":       "NO",
				"is_trigger_insertable_into": "NO",
			})
		}
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaConstraintTableUsage(whereClause string) ([]string, []map[string]any) {
	columns := []string{"table_catalog", "table_schema", "table_name",
		"constraint_catalog", "constraint_schema", "constraint_name"}
	return columns, []map[string]any{}
}

func (s *Coordinator) generateInfoSchemaCheckConstraints(whereClause string) ([]string, []map[string]any) {
	columns := []string{"constraint_catalog", "constraint_schema", "constraint_name", "check_clause"}
	return columns, []map[string]any{}
}

func (s *Coordinator) generateInfoSchemaRoleTableGrants(whereClause string) ([]string, []map[string]any) {
	columns := []string{"grantor", "grantee", "table_catalog", "table_schema",
		"table_name", "privilege_type", "is_grantable", "with_hierarchy"}
	return columns, []map[string]any{}
}

func (s *Coordinator) generateInfoSchemaRoleColumnGrants(whereClause string) ([]string, []map[string]any) {
	columns := []string{"grantor", "grantee", "table_catalog", "table_schema",
		"table_name", "privilege_type", "is_grantable", "with_hierarchy"}
	return columns, []map[string]any{}
}

func (s *Coordinator) generateInfoSchemaSequences(whereClause string) ([]string, []map[string]any) {
	columns := []string{"sequence_catalog", "sequence_schema", "sequence_name",
		"data_type", "numeric_precision", "numeric_precision_radix",
		"numeric_scale", "start_value", "minimum_value", "maximum_value",
		"increment", "cycle_option"}
	return columns, []map[string]any{}
}

func (s *Coordinator) generateInfoSchemaEnabledRoles(whereClause string) ([]string, []map[string]any) {
	columns := []string{"grantee", "role_name", "is_grantable"}
	allRows := []map[string]any{
		{
			"grantee":      "postgres",
			"role_name":    "postgres",
			"is_grantable": "YES",
		},
	}
	return columns, allRows
}

func (s *Coordinator) generateInfoSchemaApplicableRoles(whereClause string) ([]string, []map[string]any) {
	return s.generateInfoSchemaEnabledRoles(whereClause)
}
