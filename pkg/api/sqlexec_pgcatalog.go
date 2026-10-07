package api

// sqlexec_pgcatalog.go — Virtual pg_catalog and information_schema catalog views.
//
// Provides the minimal set of catalog tables required by PostgreSQL-compatible
// GUI tools (psql, DBeaver, DataGrip, pgAdmin) and ORMs (SQLAlchemy,
// Hibernate, GORM) to enumerate schemas, tables, columns, constraints,
// indexes, types, and routines.
//
// Design notes:
//   - All OIDs are deterministic: table OIDs start at 16384 (matching PG
//     convention for user tables), column OIDs are table_oid*1000 + attnum.
//   - type OIDs follow the official PostgreSQL pg_type catalog values so that
//     JDBC / libpq binary-format decoders work without modification.
//   - pg_constraint and information_schema.table_constraints include PK, UNIQUE,
//     and FK rows so that DBeaver relationship diagrams render correctly.
//   - pg_proc returns an empty set (no user-defined functions) unless the
//     catalog has functions registered.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

// pgTypeOID maps a storage DataType to the canonical PostgreSQL type OID.
// These values must match pg_type.oid for JDBC / asyncpg binary decoding.
func pgTypeOID(t storage.DataType) int {
	switch t {
	case storage.TypeInt:
		return 23 // int4
	case storage.TypeFloat:
		return 701 // float8
	case storage.TypeDecimal:
		return 1700 // numeric
	case storage.TypeBool:
		return 16 // bool
	case storage.TypeDateTime:
		return 1114 // timestamp
	case storage.TypeDate:
		return 1082 // date
	case storage.TypeTime:
		return 1083 // time
	case storage.TypeUUID:
		return 2950 // uuid
	case storage.TypeBinary:
		return 17 // bytea
	case storage.TypeJSON:
		return 114 // json
	default:
		return 1043 // varchar
	}
}

// pgTypeName maps a storage DataType to the canonical pg_type.typname string.
func pgTypeName(t storage.DataType) string {
	switch t {
	case storage.TypeInt:
		return "int4"
	case storage.TypeFloat:
		return "float8"
	case storage.TypeDecimal:
		return "numeric"
	case storage.TypeBool:
		return "bool"
	case storage.TypeDateTime:
		return "timestamp"
	case storage.TypeDate:
		return "date"
	case storage.TypeTime:
		return "time"
	case storage.TypeUUID:
		return "uuid"
	case storage.TypeBinary:
		return "bytea"
	case storage.TypeJSON:
		return "json"
	default:
		return "varchar"
	}
}

// pgTypeLen maps a storage DataType to pg_type.typlen (-1 = varlena).
func pgTypeLen(t storage.DataType) int {
	switch t {
	case storage.TypeInt:
		return 4
	case storage.TypeFloat:
		return 8
	case storage.TypeBool:
		return 1
	case storage.TypeDateTime, storage.TypeDate, storage.TypeTime, storage.TypeDecimal:
		return 8
	case storage.TypeUUID:
		return 16
	default:
		return -1
	}
}

// pgTypeMod returns the atttypmod for a column (-1 = no modifier).
// For varchar this encodes max length as (len + 4); others return -1.
func pgTypeMod(t storage.DataType, maxLen int) int {
	if t == storage.TypeVarChar && maxLen > 0 {
		return maxLen + 4
	}
	return -1
}

// tableBaseOID returns a deterministic OID for a user table based on its index.
// We start at 16384 to avoid clashing with PG system catalog OIDs (<= 10000).
func tableBaseOID(idx int) int {
	return 16384 + idx
}

// isPgCatalogTable reports whether tableName identifies a pg_catalog or
// information_schema virtual view that this file handles.
func isPgCatalogTable(tableName string) bool {
	switch strings.ToLower(tableName) {
	case
		// pg_catalog core
		"pg_catalog.pg_database", "pg_database",
		"pg_catalog.pg_namespace", "pg_namespace",
		"pg_catalog.pg_tables", "pg_tables",
		"pg_catalog.pg_class", "pg_class",
		"pg_catalog.pg_type", "pg_type",
		"pg_catalog.pg_attribute", "pg_attribute",
		"pg_catalog.pg_roles", "pg_roles",
		"pg_catalog.pg_user", "pg_user",
		"pg_catalog.pg_am", "pg_am",
		"pg_catalog.pg_index", "pg_index",
		"pg_catalog.pg_settings", "pg_settings",
		"pg_catalog.pg_description", "pg_description",
		"pg_catalog.pg_constraint", "pg_constraint",
		"pg_catalog.pg_proc", "pg_proc",
		"pg_catalog.pg_stat_user_tables", "pg_stat_user_tables",
		"pg_catalog.pg_stat_user_indexes", "pg_stat_user_indexes",
		"pg_catalog.pg_statio_user_tables", "pg_statio_user_tables",
		"pg_catalog.pg_locks", "pg_locks",
		"pg_catalog.pg_stat_activity", "pg_stat_activity",
		"pg_catalog.pg_sequences", "pg_sequences",
		"pg_catalog.pg_aggregate", "pg_aggregate",
		"pg_catalog.pg_operator", "pg_operator",
		"pg_catalog.pg_depend", "pg_depend",
		"pg_catalog.pg_inherits", "pg_inherits",
		"pg_catalog.pg_trigger", "pg_trigger",
		"pg_catalog.pg_views", "pg_views",
		"pg_catalog.pg_matviews", "pg_matviews",
		"pg_catalog.pg_range", "pg_range",
		"pg_catalog.pg_enum", "pg_enum",
		"pg_catalog.pg_attrdef", "pg_attrdef",
		"pg_catalog.pg_partitioned_table", "pg_partitioned_table",
		"pg_catalog.pg_extension", "pg_extension",
		"pg_catalog.pg_available_extensions", "pg_available_extensions",
		// information_schema
		"information_schema.tables",
		"information_schema.columns",
		"information_schema.schemata",
		"information_schema.routines",
		"information_schema.table_constraints",
		"information_schema.key_column_usage",
		"information_schema.referential_constraints",
		"information_schema.constraint_column_usage",
		"information_schema.constraint_table_usage",
		"information_schema.check_constraints",
		"information_schema.views",
		"information_schema.role_table_grants",
		"information_schema.role_column_grants",
		"information_schema.sequences",
		"information_schema.enabled_roles",
		"information_schema.applicable_roles":
		return true
	}
	return false
}

// execPgCatalogSelect handles SELECT queries against pg_catalog.* and
// information_schema.* virtual views.
func (s *Server) execPgCatalogSelect(tableName, projStr, whereClause, orderByClause string, limitCount int, start time.Time) (*QueryResult, error) {
	norm := strings.ToLower(strings.TrimSpace(tableName))

	tables, _ := s.engine.ListTables()

	var columns []string
	var allRows []map[string]any

	switch norm {
	// ------------------------------------------------------------------ //
	// pg_catalog.pg_database
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_database", "pg_database":
		columns = []string{"oid", "datname", "datdba", "encoding", "datcollate", "datctype",
			"datistemplate", "datallowconn", "datconnlimit", "datlastsysoid",
			"datfrozenxid", "datminmxid", "dattablespace", "datacl"}
		allRows = append(allRows,
			map[string]any{
				"oid":            1,
				"datname":        "sqlion",
				"datdba":         10,
				"encoding":       6,
				"datcollate":     "en_US.utf8",
				"datctype":       "en_US.utf8",
				"datistemplate":  false,
				"datallowconn":   true,
				"datconnlimit":   -1,
				"datlastsysoid":  14000,
				"datfrozenxid":   726,
				"datminmxid":     1,
				"dattablespace":  1663,
				"datacl":         nil,
			},
			map[string]any{
				"oid":            2,
				"datname":        "postgres",
				"datdba":         10,
				"encoding":       6,
				"datcollate":     "en_US.utf8",
				"datctype":       "en_US.utf8",
				"datistemplate":  false,
				"datallowconn":   true,
				"datconnlimit":   -1,
				"datlastsysoid":  14000,
				"datfrozenxid":   726,
				"datminmxid":     1,
				"dattablespace":  1663,
				"datacl":         nil,
			},
		)

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_namespace
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_namespace", "pg_namespace":
		columns = []string{"oid", "nspname", "nspowner", "nspacl"}
		allRows = append(allRows,
			map[string]any{"oid": 11, "nspname": "pg_catalog", "nspowner": 10, "nspacl": nil},
			map[string]any{"oid": 12, "nspname": "information_schema", "nspowner": 10, "nspacl": nil},
			map[string]any{"oid": 2200, "nspname": "public", "nspowner": 10, "nspacl": nil},
			map[string]any{"oid": 2201, "nspname": "dbo", "nspowner": 10, "nspacl": nil},
		)

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_class
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_class", "pg_class":
		columns = []string{
			"oid", "relname", "relnamespace", "reltype", "reloftype",
			"relowner", "relam", "relfilenode", "reltablespace",
			"relpages", "reltuples", "relallvisible", "reltoastrelid",
			"relhasindex", "relisshared", "relpersistence", "relkind",
			"relnatts", "relchecks", "relhasrules", "relhastriggers",
			"relhassubclass", "relrowsecurity", "relforcerowsecurity",
			"relispopulated", "relreplident", "relispartition",
			"relrewrite", "relfrozenxid", "relminmxid", "relacl", "reloptions",
		}
		for i, t := range tables {
			oid := tableBaseOID(i)
			allRows = append(allRows, map[string]any{
				"oid":               oid,
				"relname":           t.Name,
				"relnamespace":      2200,
				"reltype":           0,
				"reloftype":         0,
				"relowner":          10,
				"relam":             2,   // heap
				"relfilenode":       oid,
				"reltablespace":     1663,
				"relpages":          int64(10),
				"reltuples":         float64(1000),
				"relallvisible":     int64(0),
				"reltoastrelid":     0,
				"relhasindex":       len(t.Indexes) > 0,
				"relisshared":       false,
				"relpersistence":    "p",
				"relkind":           "r",
				"relnatts":          int16(len(t.Columns)),
				"relchecks":         int16(0),
				"relhasrules":       false,
				"relhastriggers":    false,
				"relhassubclass":    false,
				"relrowsecurity":    false,
				"relforcerowsecurity": false,
				"relispopulated":    true,
				"relreplident":      "d",
				"relispartition":    false,
				"relrewrite":        0,
				"relfrozenxid":      726,
				"relminmxid":        1,
				"relacl":            nil,
				"reloptions":        nil,
			})
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_tables
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_tables", "pg_tables":
		columns = []string{"schemaname", "tablename", "tableowner", "tablespace",
			"hasindexes", "hasrules", "hastriggers", "rowsecurity"}
		for _, t := range tables {
			allRows = append(allRows, map[string]any{
				"schemaname":  "public",
				"tablename":   t.Name,
				"tableowner":  "postgres",
				"tablespace":  nil,
				"hasindexes":  len(t.Indexes) > 0,
				"hasrules":    false,
				"hastriggers": false,
				"rowsecurity": false,
			})
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_attribute
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_attribute", "pg_attribute":
		columns = []string{
			"attrelid", "attname", "atttypid", "attstattarget", "attlen",
			"attnum", "attndims", "attcacheoff", "atttypmod", "attbyval",
			"attalign", "attstorage", "attcompression", "attnotnull",
			"atthasdef", "atthasmissing", "attidentity", "attgenerated",
			"attisdropped", "attislocal", "attinhcount", "attcollation",
			"attacl", "attoptions", "attfdwoptions", "attmissingval",
		}
		for tIdx, t := range tables {
			toid := tableBaseOID(tIdx)
			for cIdx, c := range t.Columns {
				oid := pgTypeOID(c.Type)
				allRows = append(allRows, map[string]any{
					"attrelid":       toid,
					"attname":        c.Name,
					"atttypid":       oid,
					"attstattarget":  -1,
					"attlen":         pgTypeLen(c.Type),
					"attnum":         int16(cIdx + 1),
					"attndims":       0,
					"attcacheoff":    -1,
					"atttypmod":      pgTypeMod(c.Type, c.Precision),
					"attbyval":       false,
					"attalign":       "i",
					"attstorage":     "x",
					"attcompression": "",
					"attnotnull":     !c.Nullable,
					"atthasdef":      false,
					"atthasmissing":  false,
					"attidentity":    "",
					"attgenerated":   "",
					"attisdropped":   false,
					"attislocal":     true,
					"attinhcount":    0,
					"attcollation":   0,
					"attacl":         nil,
					"attoptions":     nil,
					"attfdwoptions":  nil,
					"attmissingval":  nil,
				})
			}
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_type
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_type", "pg_type":
		columns = []string{
			"oid", "typname", "typnamespace", "typowner", "typlen",
			"typbyval", "typtype", "typcategory", "typispreferred",
			"typisdefined", "typdelim", "typrelid", "typsubscript",
			"typelem", "typarray", "typinput", "typoutput",
			"typreceive", "typsend", "typmodin", "typmodout",
			"typanalyze", "typalign", "typstorage", "typnotnull",
			"typbasetype", "typtypmod", "typndims", "typcollation",
			"typdefaultbin", "typdefault", "typacl",
		}
		baseTypes := []struct {
			oid      int
			name     string
			length   int
			category string
		}{
			{16, "bool", 1, "B"},
			{17, "bytea", -1, "U"},
			{20, "int8", 8, "N"},
			{21, "int2", 2, "N"},
			{23, "int4", 4, "N"},
			{25, "text", -1, "S"},
			{26, "oid", 4, "N"},
			{114, "json", -1, "U"},
			{700, "float4", 4, "N"},
			{701, "float8", 8, "N"},
			{1042, "bpchar", -1, "S"},
			{1043, "varchar", -1, "S"},
			{1082, "date", 4, "D"},
			{1083, "time", 8, "D"},
			{1114, "timestamp", 8, "D"},
			{1184, "timestamptz", 8, "D"},
			{1700, "numeric", -1, "N"},
			{2950, "uuid", 16, "U"},
			{3802, "jsonb", -1, "U"},
		}
		for _, bt := range baseTypes {
			allRows = append(allRows, map[string]any{
				"oid":           bt.oid,
				"typname":       bt.name,
				"typnamespace":  11,
				"typowner":      10,
				"typlen":        bt.length,
				"typbyval":      bt.length > 0 && bt.length <= 8,
				"typtype":       "b",
				"typcategory":   bt.category,
				"typispreferred": false,
				"typisdefined":  true,
				"typdelim":      ",",
				"typrelid":      0,
				"typsubscript":  "-",
				"typelem":       0,
				"typarray":      0,
				"typinput":      bt.name + "in",
				"typoutput":     bt.name + "out",
				"typreceive":    bt.name + "recv",
				"typsend":       bt.name + "send",
				"typmodin":      "-",
				"typmodout":     "-",
				"typanalyze":    "-",
				"typalign":      "i",
				"typstorage":    "x",
				"typnotnull":    false,
				"typbasetype":   0,
				"typtypmod":     -1,
				"typndims":      0,
				"typcollation":  0,
				"typdefaultbin": nil,
				"typdefault":    nil,
				"typacl":        nil,
			})
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_constraint
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_constraint", "pg_constraint":
		columns = []string{
			"oid", "conname", "connamespace", "contype", "condeferrable",
			"condeferred", "convalidated", "conrelid", "contypid",
			"conindid", "conparentid", "confrelid", "confupdtype",
			"confdeltype", "confmatchtype", "conislocal", "coninhcount",
			"connoinherit", "conkey", "confkey", "conpfeqop", "conppeqop",
			"conffeqop", "conexclop", "conbin",
		}
		conOID := 50000
		for tIdx, t := range tables {
			toid := tableBaseOID(tIdx)
			// PRIMARY KEY constraint
			var pkCols []int16
			for cIdx, c := range t.Columns {
				if c.PrimaryKey {
					pkCols = append(pkCols, int16(cIdx+1))
				}
			}
			if len(pkCols) > 0 {
				allRows = append(allRows, map[string]any{
					"oid":           conOID,
					"conname":       fmt.Sprintf("%s_pkey", t.Name),
					"connamespace":  2200,
					"contype":       "p",
					"condeferrable": false,
					"condeferred":   false,
					"convalidated":  true,
					"conrelid":      toid,
					"contypid":      0,
					"conindid":      toid + 100,
					"conparentid":   0,
					"confrelid":     0,
					"confupdtype":   " ",
					"confdeltype":   " ",
					"confmatchtype": " ",
					"conislocal":    true,
					"coninhcount":   0,
					"connoinherit":  true,
					"conkey":        pkCols,
					"confkey":       nil,
					"conpfeqop":     nil,
					"conppeqop":     nil,
					"conffeqop":     nil,
					"conexclop":     nil,
					"conbin":        nil,
				})
				conOID++
			}
			// UNIQUE INDEX constraints
			for _, idx := range t.Indexes {
				if !idx.Unique {
					continue
				}
				var ukCols []int16
				for _, idxCol := range idx.Columns {
					for cIdx, c := range t.Columns {
						if strings.EqualFold(c.Name, idxCol) {
							ukCols = append(ukCols, int16(cIdx+1))
							break
						}
					}
				}
				allRows = append(allRows, map[string]any{
					"oid":           conOID,
					"conname":       idx.Name,
					"connamespace":  2200,
					"contype":       "u",
					"condeferrable": false,
					"condeferred":   false,
					"convalidated":  true,
					"conrelid":      toid,
					"contypid":      0,
					"conindid":      toid + 200,
					"conparentid":   0,
					"confrelid":     0,
					"confupdtype":   " ",
					"confdeltype":   " ",
					"confmatchtype": " ",
					"conislocal":    true,
					"coninhcount":   0,
					"connoinherit":  true,
					"conkey":        ukCols,
					"confkey":       nil,
					"conpfeqop":     nil,
					"conppeqop":     nil,
					"conffeqop":     nil,
					"conexclop":     nil,
					"conbin":        nil,
				})
				conOID++
			}
			// FOREIGN KEY constraints
			for _, fk := range t.ForeignKeys {
				// Find referenced table OID
				refOID := 0
				for rIdx, rt := range tables {
					if strings.EqualFold(rt.Name, fk.ParentTable) {
						refOID = tableBaseOID(rIdx)
						break
					}
				}
				var fkCols, fkRefCols []int16
				for _, fkCol := range fk.ChildColumns {
					for cIdx, c := range t.Columns {
						if strings.EqualFold(c.Name, fkCol) {
							fkCols = append(fkCols, int16(cIdx+1))
							break
						}
					}
				}
				for _, refCol := range fk.ParentColumns {
					for rIdx, rt := range tables {
						if strings.EqualFold(rt.Name, fk.ParentTable) {
							for cIdx, c := range rt.Columns {
								if strings.EqualFold(c.Name, refCol) {
									_ = rIdx
									fkRefCols = append(fkRefCols, int16(cIdx+1))
									break
								}
							}
							break
						}
					}
				}
				allRows = append(allRows, map[string]any{
					"oid":           conOID,
					"conname":       fk.Name,
					"connamespace":  2200,
					"contype":       "f",
					"condeferrable": false,
					"condeferred":   false,
					"convalidated":  true,
					"conrelid":      toid,
					"contypid":      0,
					"conindid":      0,
					"conparentid":   0,
					"confrelid":     refOID,
					"confupdtype":   "a",
					"confdeltype":   "a",
					"confmatchtype": "s",
					"conislocal":    true,
					"coninhcount":   0,
					"connoinherit":  false,
					"conkey":        fkCols,
					"confkey":       fkRefCols,
					"conpfeqop":     nil,
					"conppeqop":     nil,
					"conffeqop":     nil,
					"conexclop":     nil,
					"conbin":        nil,
				})
				conOID++
			}
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_index
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_index", "pg_index":
		columns = []string{
			"indexrelid", "indrelid", "indnatts", "indnkeyatts",
			"indisunique", "indisprimary", "indisexclusion", "indimmediate",
			"indisclustered", "indisvalid", "indcheckxmin", "indisready",
			"indislive", "indisreplident", "indkey", "indcollation",
			"indclass", "indoption", "indexprs", "indpred",
		}
		idxOID := 20000
		for tIdx, t := range tables {
			toid := tableBaseOID(tIdx)
			// Implicit PK index
			var pkCols []int16
			for cIdx, c := range t.Columns {
				if c.PrimaryKey {
					pkCols = append(pkCols, int16(cIdx+1))
				}
			}
			if len(pkCols) > 0 {
				allRows = append(allRows, map[string]any{
					"indexrelid":     toid + 100,
					"indrelid":       toid,
					"indnatts":       int16(len(pkCols)),
					"indnkeyatts":    int16(len(pkCols)),
					"indisunique":    true,
					"indisprimary":   true,
					"indisexclusion": false,
					"indimmediate":   true,
					"indisclustered": false,
					"indisvalid":     true,
					"indcheckxmin":   false,
					"indisready":     true,
					"indislive":      true,
					"indisreplident": false,
					"indkey":         pkCols,
					"indcollation":   nil,
					"indclass":       nil,
					"indoption":      nil,
					"indexprs":       nil,
					"indpred":        nil,
				})
				idxOID++
			}
			// Explicit indexes
			for _, idx := range t.Indexes {
				var idxCols []int16
				for _, idxCol := range idx.Columns {
					for cIdx, c := range t.Columns {
						if strings.EqualFold(c.Name, idxCol) {
							idxCols = append(idxCols, int16(cIdx+1))
							break
						}
					}
				}
				allRows = append(allRows, map[string]any{
					"indexrelid":     idxOID,
					"indrelid":       toid,
					"indnatts":       int16(len(idxCols)),
					"indnkeyatts":    int16(len(idxCols)),
					"indisunique":    idx.Unique,
					"indisprimary":   false,
					"indisexclusion": false,
					"indimmediate":   true,
					"indisclustered": false,
					"indisvalid":     true,
					"indcheckxmin":   false,
					"indisready":     true,
					"indislive":      true,
					"indisreplident": false,
					"indkey":         idxCols,
					"indcollation":   nil,
					"indclass":       nil,
					"indoption":      nil,
					"indexprs":       nil,
					"indpred":        nil,
				})
				idxOID++
			}
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_roles / pg_user
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_roles", "pg_roles":
		columns = []string{
			"oid", "rolname", "rolsuper", "rolinherit", "rolcreaterole",
			"rolcreatedb", "rolcanlogin", "rolreplication", "rolconnlimit",
			"rolpassword", "rolvaliduntil", "rolbypassrls", "rolconfig",
		}
		allRows = append(allRows, map[string]any{
			"oid":           10,
			"rolname":       "postgres",
			"rolsuper":      true,
			"rolinherit":    true,
			"rolcreaterole": true,
			"rolcreatedb":   true,
			"rolcanlogin":   true,
			"rolreplication": true,
			"rolconnlimit":  -1,
			"rolpassword":   "********",
			"rolvaliduntil": nil,
			"rolbypassrls":  true,
			"rolconfig":     nil,
		})

	case "pg_catalog.pg_user", "pg_user":
		columns = []string{
			"usename", "usesysid", "usecreatedb", "usesuper", "userepl",
			"usebypassrls", "passwd", "valuntil", "useconfig",
		}
		allRows = append(allRows, map[string]any{
			"usename":      "postgres",
			"usesysid":     10,
			"usecreatedb":  true,
			"usesuper":     true,
			"userepl":      true,
			"usebypassrls": true,
			"passwd":       "********",
			"valuntil":     nil,
			"useconfig":    nil,
		})

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_am  (access methods)
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_am", "pg_am":
		columns = []string{"oid", "amname", "amhandler", "amtype"}
		allRows = append(allRows,
			map[string]any{"oid": 403, "amname": "btree", "amhandler": "bthandler", "amtype": "i"},
			map[string]any{"oid": 405, "amname": "hash", "amhandler": "hashhandler", "amtype": "i"},
			map[string]any{"oid": 783, "amname": "gist", "amhandler": "gisthandler", "amtype": "i"},
			map[string]any{"oid": 2742, "amname": "gin", "amhandler": "ginhandler", "amtype": "i"},
		)

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_settings
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_settings", "pg_settings":
		columns = []string{
			"name", "setting", "unit", "category", "short_desc",
			"extra_desc", "context", "vartype", "source",
			"min_val", "max_val", "enumvals", "boot_val", "reset_val",
			"sourcefile", "sourceline", "pending_restart",
		}
		settings := []struct {
			name     string
			setting  string
			category string
			desc     string
			vartype  string
		}{
			{"server_version", "15.0 (SQLion)", "Preset Options", "Shows the server version.", "string"},
			{"server_version_num", "150000", "Preset Options", "Shows the server version as an integer.", "integer"},
			{"client_encoding", "UTF8", "Client Connection Defaults / Locale and Formatting", "Sets the client's character set encoding.", "string"},
			{"server_encoding", "UTF8", "Client Connection Defaults / Locale and Formatting", "Shows the server (database) character set encoding.", "string"},
			{"TimeZone", "UTC", "Client Connection Defaults / Locale and Formatting", "Sets the time zone for displaying and interpreting time stamps.", "string"},
			{"DateStyle", "ISO, MDY", "Client Connection Defaults / Locale and Formatting", "Sets the display format for date and time values.", "string"},
			{"IntervalStyle", "postgres", "Client Connection Defaults / Locale and Formatting", "Sets the display format for interval values.", "string"},
			{"standard_conforming_strings", "on", "Version and Platform Compatibility / Previous PostgreSQL Versions", "Causes ... to be treated literally in strings.", "bool"},
			{"integer_datetimes", "on", "Preset Options", "Datetimes are integer based.", "bool"},
			{"max_connections", "100", "Connections and Authentication / Connection Settings", "Sets the maximum number of concurrent connections.", "integer"},
			{"work_mem", "4MB", "Resource Usage / Memory", "Sets the maximum memory to be used for query workspaces.", "integer"},
			{"search_path", "\"$user\", public", "Client Connection Defaults / Statement Behavior", "Sets the schema search order for names that are not schema-qualified.", "string"},
		}
		for _, st := range settings {
			allRows = append(allRows, map[string]any{
				"name":            st.name,
				"setting":         st.setting,
				"unit":            nil,
				"category":        st.category,
				"short_desc":      st.desc,
				"extra_desc":      nil,
				"context":         "user",
				"vartype":         st.vartype,
				"source":          "default",
				"min_val":         nil,
				"max_val":         nil,
				"enumvals":        nil,
				"boot_val":        st.setting,
				"reset_val":       st.setting,
				"sourcefile":      nil,
				"sourceline":      nil,
				"pending_restart": false,
			})
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_description
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_description", "pg_description":
		columns = []string{"objoid", "classoid", "objsubid", "description"}
		// No user-defined descriptions; return empty result set.

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_proc
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_proc", "pg_proc":
		columns = []string{
			"oid", "proname", "pronamespace", "proowner", "prolang",
			"procost", "prorows", "provariadic", "prosupport",
			"prokind", "prosecdef", "proleakproof", "proisstrict",
			"proretset", "provolatile", "proparallel", "pronargs",
			"pronargdefaults", "prorettype", "proargtypes",
			"proallargtypes", "proargmodes", "proargnames",
			"proargdefaults", "protrftypes", "prosrc", "probin",
			"proconfig", "proacl",
		}
		if s.catalog != nil {
			procOID := 30000
			for _, p := range s.catalog.ListProcedures() {
				allRows = append(allRows, map[string]any{
					"oid":             procOID,
					"proname":         p.Name,
					"pronamespace":    2200,
					"proowner":        10,
					"prolang":         14,
					"procost":         float32(100),
					"prorows":         float32(0),
					"provariadic":     0,
					"prosupport":      "-",
					"prokind":         "p",
					"prosecdef":       false,
					"proleakproof":    false,
					"proisstrict":     false,
					"proretset":       false,
					"provolatile":     "v",
					"proparallel":     "u",
					"pronargs":        int16(len(p.Params)),
					"pronargdefaults": int16(0),
					"prorettype":      2278, // void
					"proargtypes":     nil,
					"proallargtypes":  nil,
					"proargmodes":     nil,
					"proargnames":     nil,
					"proargdefaults":  nil,
					"protrftypes":     nil,
					"prosrc":          p.Body,
					"probin":          nil,
					"proconfig":       nil,
					"proacl":          nil,
				})
				procOID++
			}
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_stat_user_tables
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_stat_user_tables", "pg_stat_user_tables":
		columns = []string{
			"relid", "schemaname", "relname",
			"seq_scan", "seq_tup_read", "idx_scan", "idx_tup_fetch",
			"n_tup_ins", "n_tup_upd", "n_tup_del", "n_tup_hot_upd",
			"n_live_tup", "n_dead_tup", "n_mod_since_analyze",
			"n_ins_since_vacuum", "last_vacuum", "last_autovacuum",
			"last_analyze", "last_autoanalyze",
			"vacuum_count", "autovacuum_count",
			"analyze_count", "autoanalyze_count",
		}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"relid":               tableBaseOID(i),
				"schemaname":          "public",
				"relname":             t.Name,
				"seq_scan":            int64(0),
				"seq_tup_read":        int64(0),
				"idx_scan":            int64(0),
				"idx_tup_fetch":       int64(0),
				"n_tup_ins":           int64(0),
				"n_tup_upd":           int64(0),
				"n_tup_del":           int64(0),
				"n_tup_hot_upd":       int64(0),
				"n_live_tup":          int64(0),
				"n_dead_tup":          int64(0),
				"n_mod_since_analyze": int64(0),
				"n_ins_since_vacuum":  int64(0),
				"last_vacuum":         nil,
				"last_autovacuum":     nil,
				"last_analyze":        nil,
				"last_autoanalyze":    nil,
				"vacuum_count":        int64(0),
				"autovacuum_count":    int64(0),
				"analyze_count":       int64(0),
				"autoanalyze_count":   int64(0),
			})
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_stat_user_indexes
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_stat_user_indexes", "pg_stat_user_indexes":
		columns = []string{"relid", "indexrelid", "schemaname", "relname", "indexrelname",
			"idx_scan", "idx_tup_read", "idx_tup_fetch"}
		idxOID := 20000
		for i, t := range tables {
			toid := tableBaseOID(i)
			for _, idx := range t.Indexes {
				allRows = append(allRows, map[string]any{
					"relid":         toid,
					"indexrelid":    idxOID,
					"schemaname":    "public",
					"relname":       t.Name,
					"indexrelname":  idx.Name,
					"idx_scan":      int64(0),
					"idx_tup_read":  int64(0),
					"idx_tup_fetch": int64(0),
				})
				idxOID++
			}
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_statio_user_tables
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_statio_user_tables", "pg_statio_user_tables":
		columns = []string{
			"relid", "schemaname", "relname",
			"heap_blks_read", "heap_blks_hit",
			"idx_blks_read", "idx_blks_hit",
			"toast_blks_read", "toast_blks_hit",
			"tidx_blks_read", "tidx_blks_hit",
		}
		for i, t := range tables {
			allRows = append(allRows, map[string]any{
				"relid":           tableBaseOID(i),
				"schemaname":      "public",
				"relname":         t.Name,
				"heap_blks_read":  int64(0),
				"heap_blks_hit":   int64(0),
				"idx_blks_read":   int64(0),
				"idx_blks_hit":    int64(0),
				"toast_blks_read": int64(0),
				"toast_blks_hit":  int64(0),
				"tidx_blks_read":  int64(0),
				"tidx_blks_hit":   int64(0),
			})
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_locks
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_locks", "pg_locks":
		columns = []string{
			"locktype", "database", "relation", "page", "tuple",
			"virtualxid", "transactionid", "classid", "objid",
			"objsubid", "virtualtransaction", "pid", "mode", "granted", "fastpath",
		}
		// Return empty — no active locks in a quiesced state.

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_stat_activity
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_stat_activity", "pg_stat_activity":
		columns = []string{
			"datid", "datname", "pid", "leader_pid", "usesysid", "usename",
			"application_name", "client_addr", "client_hostname", "client_port",
			"backend_start", "xact_start", "query_start", "state_change",
			"wait_event_type", "wait_event", "state", "backend_xid",
			"backend_xmin", "query", "backend_type",
		}
		// Minimal self-row for the current session.
		allRows = append(allRows, map[string]any{
			"datid":            1,
			"datname":          "sqlion",
			"pid":              1,
			"leader_pid":       nil,
			"usesysid":         10,
			"usename":          "postgres",
			"application_name": "sqlion",
			"client_addr":      nil,
			"client_hostname":  nil,
			"client_port":      -1,
			"backend_start":    time.Now().Format("2006-01-02 15:04:05"),
			"xact_start":       nil,
			"query_start":      nil,
			"state_change":     nil,
			"wait_event_type":  nil,
			"wait_event":       nil,
			"state":            "active",
			"backend_xid":      nil,
			"backend_xmin":     nil,
			"query":            "",
			"backend_type":     "client backend",
		})

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_sequences
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_sequences", "pg_sequences":
		columns = []string{
			"schemaname", "sequencename", "sequenceowner",
			"data_type", "start_value", "min_value", "max_value",
			"increment_by", "cycle", "cache_size", "last_value",
		}
		// No sequences defined; return empty.

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_views
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_views", "pg_views":
		columns = []string{"schemaname", "viewname", "viewowner", "definition"}
		if s.catalog != nil {
			for _, v := range s.catalog.ListViews() {
				allRows = append(allRows, map[string]any{
					"schemaname": "public",
					"viewname":   v.Name,
					"viewowner":  "postgres",
					"definition": v.Query,
				})
			}
		}

	// ------------------------------------------------------------------ //
	// pg_catalog.pg_matviews
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_matviews", "pg_matviews":
		columns = []string{"schemaname", "matviewname", "matviewowner",
			"tablespace", "hasindexes", "ispopulated", "definition"}
		// No materialized views; return empty.

	// ------------------------------------------------------------------ //
	// Stub tables — return empty with correct column names
	// ------------------------------------------------------------------ //
	case "pg_catalog.pg_aggregate", "pg_aggregate":
		columns = []string{"aggfnoid", "aggkind", "aggnumdirectargs", "aggtransfn",
			"aggfinalfn", "aggcombinefn", "aggserialfn", "aggdeserialfn",
			"aggmtransfn", "aggminvtransfn", "aggmfinalfn",
			"aggfinalextra", "aggmfinalextra", "aggfinalmodify",
			"aggmfinalmodify", "aggsortop", "aggtranstype",
			"aggtransspace", "aggmtranstype", "aggmtransspace",
			"agginitval", "aggminitval"}

	case "pg_catalog.pg_operator", "pg_operator":
		columns = []string{"oid", "oprname", "oprnamespace", "oprowner",
			"oprkind", "oprcanmerge", "oprcanhash",
			"oprleft", "oprright", "oprresult", "oprcom",
			"oprnegate", "oprcode", "oprrest", "oprjoin"}

	case "pg_catalog.pg_depend", "pg_depend":
		columns = []string{"classid", "objid", "objsubid", "refclassid",
			"refobjid", "refobjsubid", "deptype"}

	case "pg_catalog.pg_inherits", "pg_inherits":
		columns = []string{"inhrelid", "inhparent", "inhseqno", "inhdetachpending"}

	case "pg_catalog.pg_trigger", "pg_trigger":
		columns = []string{"oid", "tgrelid", "tgparentid", "tgname", "tgfoid",
			"tgtype", "tgenabled", "tgisinternal", "tgconstrrelid",
			"tgconstrindid", "tgconstraint", "tgdeferrable",
			"tginitdeferred", "tgnargs", "tgattr", "tgargs",
			"tgqual", "tgoldtable", "tgnewtable"}

	case "pg_catalog.pg_range", "pg_range":
		columns = []string{"rngtypid", "rngsubtype", "rngmultitypid",
			"rngcollation", "rngsubopc", "rngcanonical", "rngsubdiff"}

	case "pg_catalog.pg_enum", "pg_enum":
		columns = []string{"oid", "enumtypid", "enumsortorder", "enumlabel"}

	case "pg_catalog.pg_attrdef", "pg_attrdef":
		columns = []string{"oid", "adrelid", "adnum", "adbin"}

	case "pg_catalog.pg_partitioned_table", "pg_partitioned_table":
		columns = []string{"partrelid", "partstrat", "partnatts", "partdefid",
			"partattrs", "partclass", "partcollation", "partexprs"}

	case "pg_catalog.pg_extension", "pg_extension":
		columns = []string{"oid", "extname", "extowner", "extnamespace",
			"extrelocatable", "extversion", "extconfig", "extcondition"}

	case "pg_catalog.pg_available_extensions", "pg_available_extensions":
		columns = []string{"name", "default_version", "installed_version", "comment"}

	// ================================================================== //
	// information_schema views
	// ================================================================== //

	// ------------------------------------------------------------------ //
	// information_schema.tables
	// ------------------------------------------------------------------ //
	case "information_schema.tables":
		columns = []string{
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

	// ------------------------------------------------------------------ //
	// information_schema.columns
	// ------------------------------------------------------------------ //
	case "information_schema.columns":
		columns = []string{
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

	// ------------------------------------------------------------------ //
	// information_schema.schemata
	// ------------------------------------------------------------------ //
	case "information_schema.schemata":
		columns = []string{
			"catalog_name", "schema_name", "schema_owner",
			"default_character_set_catalog", "default_character_set_schema",
			"default_character_set_name", "sql_path",
		}
		allRows = append(allRows,
			map[string]any{"catalog_name": "sqlion", "schema_name": "public", "schema_owner": "postgres", "default_character_set_catalog": nil, "default_character_set_schema": nil, "default_character_set_name": nil, "sql_path": nil},
			map[string]any{"catalog_name": "sqlion", "schema_name": "information_schema", "schema_owner": "postgres", "default_character_set_catalog": nil, "default_character_set_schema": nil, "default_character_set_name": nil, "sql_path": nil},
			map[string]any{"catalog_name": "sqlion", "schema_name": "pg_catalog", "schema_owner": "postgres", "default_character_set_catalog": nil, "default_character_set_schema": nil, "default_character_set_name": nil, "sql_path": nil},
		)

	// ------------------------------------------------------------------ //
	// information_schema.routines
	// ------------------------------------------------------------------ //
	case "information_schema.routines":
		columns = []string{
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
		if s.catalog != nil {
			for _, p := range s.catalog.ListProcedures() {
				allRows = append(allRows, map[string]any{
					"specific_catalog": "sqlion",
					"specific_schema":  "public",
					"specific_name":    p.Name,
					"routine_catalog":  "sqlion",
					"routine_schema":   "public",
					"routine_name":     p.Name,
					"routine_type":     "PROCEDURE",
					"routine_body":     "SQL",
					"routine_definition": p.Body,
					"external_language": "SQL",
					"is_deterministic": "NO",
					"sql_data_access":  "MODIFIES",
					"security_type":    "INVOKER",
					"created":          p.CreatedAt.Format("2006-01-02 15:04:05"),
					"last_altered":     p.CreatedAt.Format("2006-01-02 15:04:05"),
				})
			}
		}

	// ------------------------------------------------------------------ //
	// information_schema.table_constraints
	// ------------------------------------------------------------------ //
	case "information_schema.table_constraints":
		columns = []string{
			"constraint_catalog", "constraint_schema", "constraint_name",
			"table_catalog", "table_schema", "table_name",
			"constraint_type", "is_deferrable", "initially_deferred",
			"enforced", "nulls_distinct",
		}
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

	// ------------------------------------------------------------------ //
	// information_schema.key_column_usage
	// ------------------------------------------------------------------ //
	case "information_schema.key_column_usage":
		columns = []string{
			"constraint_catalog", "constraint_schema", "constraint_name",
			"table_catalog", "table_schema", "table_name",
			"column_name", "ordinal_position", "position_in_unique_constraint",
		}
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

	// ------------------------------------------------------------------ //
	// information_schema.referential_constraints
	// ------------------------------------------------------------------ //
	case "information_schema.referential_constraints":
		columns = []string{
			"constraint_catalog", "constraint_schema", "constraint_name",
			"unique_constraint_catalog", "unique_constraint_schema", "unique_constraint_name",
			"match_option", "update_rule", "delete_rule",
		}
		for _, t := range tables {
			for _, fk := range t.ForeignKeys {
				allRows = append(allRows, map[string]any{
					"constraint_catalog":      "sqlion",
					"constraint_schema":       "public",
					"constraint_name":         fk.Name,
					"unique_constraint_catalog": "sqlion",
					"unique_constraint_schema":  "public",
					"unique_constraint_name":    fmt.Sprintf("%s_pkey", fk.ParentTable),
					"match_option":            "NONE",
					"update_rule":             "NO ACTION",
					"delete_rule":             "NO ACTION",
				})
			}
		}

	// ------------------------------------------------------------------ //
	// information_schema.constraint_column_usage
	// ------------------------------------------------------------------ //
	case "information_schema.constraint_column_usage":
		columns = []string{
			"table_catalog", "table_schema", "table_name",
			"column_name", "constraint_catalog", "constraint_schema", "constraint_name",
		}
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

	// ------------------------------------------------------------------ //
	// information_schema.views
	// ------------------------------------------------------------------ //
	case "information_schema.views":
		columns = []string{
			"table_catalog", "table_schema", "table_name",
			"view_definition", "check_option", "is_updatable",
			"is_insertable_into", "is_trigger_updatable",
			"is_trigger_deletable", "is_trigger_insertable_into",
		}
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

	// ------------------------------------------------------------------ //
	// Stub information_schema views (empty but correct column names)
	// ------------------------------------------------------------------ //
	case "information_schema.constraint_table_usage":
		columns = []string{"table_catalog", "table_schema", "table_name",
			"constraint_catalog", "constraint_schema", "constraint_name"}

	case "information_schema.check_constraints":
		columns = []string{"constraint_catalog", "constraint_schema", "constraint_name", "check_clause"}

	case "information_schema.role_table_grants", "information_schema.role_column_grants":
		columns = []string{"grantor", "grantee", "table_catalog", "table_schema",
			"table_name", "privilege_type", "is_grantable", "with_hierarchy"}

	case "information_schema.sequences":
		columns = []string{"sequence_catalog", "sequence_schema", "sequence_name",
			"data_type", "numeric_precision", "numeric_precision_radix",
			"numeric_scale", "start_value", "minimum_value", "maximum_value",
			"increment", "cycle_option"}

	case "information_schema.enabled_roles", "information_schema.applicable_roles":
		columns = []string{"grantee", "role_name", "is_grantable"}
		allRows = append(allRows, map[string]any{
			"grantee":      "postgres",
			"role_name":    "postgres",
			"is_grantable": "YES",
		})

	default:
		return nil, fmt.Errorf("unknown pg_catalog or information_schema view: %s", tableName)
	}

	return buildVirtualCatalogResult(columns, allRows, projStr, whereClause, orderByClause, limitCount, start)
}

var heidiClassJoinRe = regexp.MustCompile(`(?is)FROM\s+pg_class\s+(?:AS\s+)?c\s+JOIN\s+pg_namespace\s+(?:AS\s+)?n\s+ON\s+n\.oid\s*=\s*c\.relnamespace`)
var heidiProcJoinRe = regexp.MustCompile(`(?is)FROM\s+pg_proc\s+(?:AS\s+)?p\s+JOIN\s+pg_namespace\s+(?:AS\s+)?n\s+ON\s+n\.oid\s*=\s*p\.pronamespace`)
var heidiAttrJoinRe = regexp.MustCompile(`(?is)FROM\s+pg_attribute\s+(?:AS\s+)?a\s+JOIN\s+pg_class\s+(?:AS\s+)?pgc\s+ON\s+pgc\.oid\s*=\s*a\.attrelid`)
var heidiFKJoinRe = regexp.MustCompile(`(?is)FROM\s+pg_constraint\s+(?:AS\s+)?con\s+JOIN\s+pg_class\s+(?:AS\s+)?src_tbl`)
var beekeeperTableJoinRe = regexp.MustCompile(`(?is)FROM\s+information_schema\.tables\s+(?:AS\s+)?t\s+JOIN\s+pg_class\s+(?:AS\s+)?pc\s+ON`)

func isHeidiSQLCatalogJoinQuery(sql string) bool {
	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS") {
		return beekeeperTableJoinRe.MatchString(sql)
	}
	if strings.Contains(upper, "PG_CONSTRAINT") && strings.Contains(upper, "SRC_TBL") {
		return heidiFKJoinRe.MatchString(sql)
	}
	if strings.Contains(upper, "PG_NAMESPACE") {
		return heidiClassJoinRe.MatchString(sql) || heidiProcJoinRe.MatchString(sql)
	}
	if strings.Contains(upper, "PG_ATTRIBUTE") && strings.Contains(upper, "PG_CLASS") {
		return heidiAttrJoinRe.MatchString(sql)
	}
	return false
}

func (s *Server) execHeidiSQLCatalogJoinSelect(sql string, start time.Time) (*QueryResult, error) {
	// Handle Beekeeper Studio table query:
	// FROM information_schema.tables AS t JOIN pg_class AS pc ON ...
	if beekeeperTableJoinRe.MatchString(sql) {
		return s.execBeekeeperTableSelect(sql, start)
	}

	// Handle foreign key query:
	// FROM pg_constraint con JOIN pg_class src_tbl ... WHERE con.contype = 'f'
	if heidiFKJoinRe.MatchString(sql) {
		return s.execHeidiSQLForeignKeySelect(sql, start)
	}

	// Handle column metadata query:
	// FROM pg_attribute AS a JOIN pg_class AS pgc ON pgc.oid = a.attrelid ... WHERE ...
	if heidiAttrJoinRe.MatchString(sql) {
		return s.execHeidiSQLColumnMetadataSelect(sql, start)
	}

	// Extract target schema from WHERE n.nspname = '...' if present, default to 'public'
	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"schema_name", "object_name", "object_kind",
		"data_length", "index_length", "reltuples",
		"comment", "proargtypes",
	}

	var allRows []map[string]any

	if heidiClassJoinRe.MatchString(sql) {
		// Tables and views
		tables, _ := s.engine.ListTables()
		for _, t := range tables {
			allRows = append(allRows, map[string]any{
				"schema_name":  targetSchema,
				"object_name":  t.Name,
				"object_kind":  "r",
				"data_length":  int64(16384),
				"index_length": int64(8192),
				"reltuples":    float32(1000),
				"comment":      nil,
				"proargtypes":  nil,
			})
		}
		if s.catalog != nil {
			for _, v := range s.catalog.ListViews() {
				allRows = append(allRows, map[string]any{
					"schema_name":  targetSchema,
					"object_name":  v.Name,
					"object_kind":  "v",
					"data_length":  int64(0),
					"index_length": int64(0),
					"reltuples":    float32(0),
					"comment":      nil,
					"proargtypes":  nil,
				})
			}
		}
	} else if heidiProcJoinRe.MatchString(sql) {
		// Procedures and functions
		if s.catalog != nil {
			for _, p := range s.catalog.ListProcedures() {
				allRows = append(allRows, map[string]any{
					"schema_name":  targetSchema,
					"object_name":  p.Name,
					"object_kind":  "p",
					"data_length":  nil,
					"index_length": nil,
					"reltuples":    nil,
					"comment":      nil,
					"proargtypes":  nil,
				})
			}
			for _, f := range s.catalog.ListFunctions() {
				allRows = append(allRows, map[string]any{
					"schema_name":  targetSchema,
					"object_name":  f.Name,
					"object_kind":  "f",
					"data_length":  nil,
					"index_length": nil,
					"reltuples":    nil,
					"comment":      nil,
					"proargtypes":  nil,
				})
			}
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execHeidiSQLColumnMetadataSelect(sql string, start time.Time) (*QueryResult, error) {
	// Look for target table name if specified, e.g. pgc.relname = 'accounts' or pgc.oid = ...
	targetTable := ""
	tblRe := regexp.MustCompile(`(?is)pgc\.relname\s*=\s*'([^']+)'`)
	if m := tblRe.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = m[1]
	}

	columns := []string{
		"column_name", "attnum", "atttypid", "data_type",
		"is_nullable", "column_comment", "column_default",
		"character_maximum_length",
	}

	var allRows []map[string]any
	tables, _ := s.engine.ListTables()

	for _, t := range tables {
		if targetTable != "" && !strings.EqualFold(t.Name, targetTable) {
			continue
		}
		for i, c := range t.Columns {
			oid := pgTypeOID(c.Type)
			dtName := pgTypeName(c.Type)
			isNullable := "YES"
			if c.PrimaryKey || !c.Nullable {
				isNullable = "NO"
			}
			var maxLen any = nil
			if c.Type == storage.TypeVarChar && c.Precision > 0 {
				maxLen = int64(c.Precision)
			}
			allRows = append(allRows, map[string]any{
				"column_name":              c.Name,
				"attnum":                   int16(i + 1),
				"atttypid":                 oid,
				"data_type":                dtName,
				"is_nullable":              isNullable,
				"column_comment":           nil,
				"column_default":           nil,
				"character_maximum_length": maxLen,
			})
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execHeidiSQLForeignKeySelect(sql string, start time.Time) (*QueryResult, error) {
	// Extract table name from src_tbl.relname = '...' if present
	targetTable := ""
	tblRe := regexp.MustCompile(`(?is)src_tbl\.relname\s*=\s*'([^']+)'`)
	if m := tblRe.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = m[1]
	}

	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)src_ns\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"constraint_name",
		"update_rule",
		"delete_rule",
		"table_schema",
		"table_name",
		"columns",
		"ref_schema",
		"ref_table",
		"ref_columns",
		"ord_position",
	}

	var allRows []map[string]any
	tables, _ := s.engine.ListTables()

	for _, t := range tables {
		if targetTable != "" && !strings.EqualFold(t.Name, targetTable) {
			continue
		}
		for _, fk := range t.ForeignKeys {
			updRule := "NO ACTION"
			switch fk.OnUpdate {
			case storage.ActionCascade:
				updRule = "CASCADE"
			case storage.ActionSetNull:
				updRule = "SET NULL"
			case storage.ActionSetDefault:
				updRule = "SET DEFAULT"
			case storage.ActionRestrict:
				updRule = "RESTRICT"
			}

			delRule := "NO ACTION"
			switch fk.OnDelete {
			case storage.ActionCascade:
				delRule = "CASCADE"
			case storage.ActionSetNull:
				delRule = "SET NULL"
			case storage.ActionSetDefault:
				delRule = "SET DEFAULT"
			case storage.ActionRestrict:
				delRule = "RESTRICT"
			}

			var posList []string
			for p := 1; p <= len(fk.ChildColumns); p++ {
				posList = append(posList, fmt.Sprintf("%d", p))
			}

			allRows = append(allRows, map[string]any{
				"constraint_name": fk.Name,
				"update_rule":     updRule,
				"delete_rule":     delRule,
				"table_schema":    targetSchema,
				"table_name":      t.Name,
				"columns":         strings.Join(fk.ChildColumns, ","),
				"ref_schema":      targetSchema,
				"ref_table":       fk.ParentTable,
				"ref_columns":     strings.Join(fk.ParentColumns, ","),
				"ord_position":    strings.Join(posList, ","),
			})
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execBeekeeperTableSelect(sql string, start time.Time) (*QueryResult, error) {
	// Beekeeper Studio queries:
	// SELECT t.table_schema as schema, t.table_name as name, pc.relkind as tabletype, parent_pc.relkind as parenttype
	// FROM information_schema.tables AS t
	// JOIN pg_class AS pc ON t.table_name = pc.relname AND quote_ident(t.table_schema) = pc.relnamespace::regnamespace::text
	// LEFT OUTER JOIN pg_inherits AS i ON pc.oid = i.inhrelid
	// LEFT OUTER JOIN pg_class AS parent_pc ON parent_pc.oid = i.inhparent
	// WHERE t.table_type NOT LIKE '%VIEW%'
	// [AND table_schema = '...']
	columns := []string{"schema", "name", "tabletype", "parenttype"}
	var allRows []map[string]any

	targetSchema := ""
	schemaRe := regexp.MustCompile(`(?is)table_schema\s*=\s*'([^']+)'`)
	if m := schemaRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	tables, _ := s.engine.ListTables()
	for _, t := range tables {
		// sqlion user tables live in public and dbo
		schemas := []string{"public"}
		if targetSchema != "" {
			schemas = []string{targetSchema}
		}
		for _, sch := range schemas {
			allRows = append(allRows, map[string]any{
				"schema":     sch,
				"name":       t.Name,
				"tabletype":  "r",
				"parenttype": "r",
			})
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}


