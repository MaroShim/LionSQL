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
	"sort"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/catalog"
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
		"information_schema.parameters",
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
			map[string]any{"catalog_name": "sqlion", "schema_name": "dbo", "schema_owner": "sa", "default_character_set_catalog": nil, "default_character_set_schema": nil, "default_character_set_name": nil, "sql_path": nil},
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
		defSchema := "public"
		if strings.Contains(strings.ToLower(whereClause), "'dbo'") {
			defSchema = "dbo"
		}
		if s.catalog != nil {
			for _, p := range s.catalog.ListProcedures() {
				allRows = append(allRows, map[string]any{
					"specific_catalog": "sqlion",
					"specific_schema":  defSchema,
					"specific_name":    p.Name,
					"routine_catalog":  "sqlion",
					"routine_schema":   defSchema,
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
	// information_schema.parameters
	// ------------------------------------------------------------------ //
	case "information_schema.parameters":
		columns = []string{
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

func isHeidiSQLCatalogJoinQuery(sql string) bool {
	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "SYS.COLUMNS") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "SYS.TYPES")) {
		return true
	}
	if strings.Contains(upper, "SYS.INDEXES") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "IS_PRIMARY_KEY")) {
		return true
	}
	if strings.Contains(upper, "SYS.INDEX_COLUMNS") {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "SCHEMANAME")) {
		return true
	}
	if strings.Contains(upper, "SYS.PARAMETERS") && strings.Contains(upper, "JOIN") {
		return true
	}
	if strings.Contains(upper, "SYS.TABLES") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "TABLEROWCOUNT") || strings.Contains(upper, "OBJECTCOMMENT")) {
		return true
	}
	if strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "TRIGGERNAME") || strings.Contains(upper, "O.TYPE = 'TR'") || strings.Contains(upper, "O.TYPE='TR'") || strings.Contains(upper, "JOIN")) {
		return true
	}
	if (strings.Contains(upper, "SYS.OBJECTS") || strings.Contains(upper, "SYS.TABLES")) && strings.Contains(upper, "SYS.SCHEMAS") {
		return true
	}
	if strings.Contains(upper, "SYS.SYSCOMMENTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "CODETEXT")) {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(upper, "SYS.COMPUTED_COLUMNS") {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS") {
		return true
	}
	if strings.Contains(upper, "PG_CONSTRAINT") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "UNNEST") || strings.Contains(upper, "SRC_TBL") || strings.Contains(upper, "CONKEY")) {
		return true
	}
	if strings.Contains(upper, "PG_INDEX") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "PG_NAMESPACE") || strings.Contains(upper, "INDEX_NAME") || strings.Contains(upper, "INDKEY")) {
		return true
	}
	if strings.Contains(upper, "PG_TRIGGER") && strings.Contains(upper, "JOIN") {
		return true
	}
	if strings.Contains(upper, "PG_CLASS") && (strings.Contains(upper, "PG_NAMESPACE") || strings.Contains(upper, "VIEW_DEFS") || strings.Contains(upper, "RELNAMESPACE")) {
		return true
	}
	if strings.Contains(upper, "PG_PROC") && strings.Contains(upper, "PG_NAMESPACE") {
		return true
	}
	if strings.Contains(upper, "PG_ATTRIBUTE") && (strings.Contains(upper, "PG_CLASS") || strings.Contains(upper, "JOIN")) {
		return true
	}
	return false
}

func (s *Server) execHeidiSQLCatalogJoinSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)

	// 1. DbGate SQL Server Primary Keys
	if strings.Contains(upper, "SYS.INDEXES") && (strings.Contains(upper, "IS_PRIMARY_KEY = 1") || strings.Contains(upper, "IS_PRIMARY_KEY=1")) {
		return s.execDbGateMSSQLPrimaryKeySelect(sql, start)
	}

	// 2. DbGate SQL Server Indexes
	if strings.Contains(upper, "SYS.INDEXES") && (strings.Contains(upper, "IS_PRIMARY_KEY=0") || strings.Contains(upper, "IS_PRIMARY_KEY = 0")) {
		return s.execDbGateMSSQLIndexSelect(sql, start)
	}

	// 3. DbGate SQL Server Index Columns
	if strings.Contains(upper, "SYS.INDEX_COLUMNS") {
		return s.execDbGateMSSQLIndexColumnSelect(sql, start)
	}

	// 4. DbGate & Modern GUI SQL Server column query:
	if strings.Contains(upper, "SYS.COLUMNS") && (strings.Contains(upper, "SYS.TYPES") || strings.Contains(upper, "ISIDENTITY") || strings.Contains(upper, "IS_IDENTITY") || strings.Contains(upper, "DATATYPE")) {
		return s.execDbGateMSSQLColumnSelect(sql, start)
	}

	// 5. DbGate SQL Server column comments (MS_Description)
	if strings.Contains(upper, "COLUMNCOMMENT") && strings.Contains(upper, "SYS.COLUMNS") {
		return s.execDbGateMSSQLColumnCommentSelect(sql, start)
	}

	// 6. Beekeeper Studio SQL Server column query:
	if strings.Contains(upper, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(upper, "SYS.COMPUTED_COLUMNS") {
		return s.execBeekeeperMSSQLColumnSelect(sql, start)
	}

	// 7. DbGate SQL Server Foreign Keys
	if strings.Contains(upper, "INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS") {
		return s.execDbGateMSSQLForeignKeySelect(sql, start)
	}

	// 8. DbGate SQL Server Table Row Count
	if strings.Contains(upper, "TABLEROWCOUNT") || (strings.Contains(upper, "SYS.TABLES") && strings.Contains(upper, "SYS.PARTITIONS")) {
		return s.execDbGateMSSQLRowCountSelect(sql, start)
	}

	// 9. DbGate SQL Server Parameters
	if strings.Contains(upper, "SYS.PARAMETERS") {
		return s.execDbGateMSSQLParamSelect(sql, start)
	}

	// 10. DbGate SQL Server Triggers
	if strings.Contains(upper, "TRIGGERNAME") || (strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "TYPE = 'TR'") || strings.Contains(upper, "TYPE='TR'"))) {
		return s.execDbGateMSSQLTriggerSelect(sql, start)
	}

	// 11. DbGate SQL Server SQL Code (loadSqlCode)
	if strings.Contains(upper, "SYS.SYSCOMMENTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "CODETEXT")) {
		return s.execDbGateMSSQLLoadSqlCodeSelect(sql, start)
	}

	// 12. DbGate SQL Server Programmables
	if strings.Contains(upper, "SQLOBJECTTYPE") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "('P', 'IF', 'FN', 'TF')")) {
		return s.execDbGateMSSQLProgrammablesSelect(sql, start)
	}

	// 13. DbGate SQL Server Views
	if strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && (strings.Contains(upper, "TYPE='V'") || strings.Contains(upper, "TYPE = 'V'")) {
		return s.execDbGateMSSQLViewSelect(sql, start)
	}

	// 14. DbGate SQL Server Tables (tables.js)
	if strings.Contains(upper, "SYS.TABLES") && strings.Contains(upper, "SYS.SCHEMAS") && (strings.Contains(upper, "PURENAME") || strings.Contains(upper, "OBJECTCOMMENT")) {
		return s.execDbGateMSSQLTableSelect(sql, start)
	}

	// 15. DbGate SQL Server Object Modifications / Master Object List (modifications.js)
	if strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && strings.Contains(upper, "PURENAME") {
		return s.execDbGateMSSQLObjectSelect(sql, start)
	}

	// 16. Beekeeper Studio Postgres table query:
	if strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS") {
		return s.execBeekeeperTableSelect(sql, start)
	}

	// 12. Constraints: Foreign keys, Primary keys, Unique constraints
	if strings.Contains(upper, "PG_CONSTRAINT") {
		if strings.Contains(upper, "SRC_TBL") || strings.Contains(upper, "CONTYPE = 'F'") || strings.Contains(upper, "REF_TABLE") || strings.Contains(upper, "CONFRELID") {
			return s.execHeidiSQLForeignKeySelect(sql, start)
		}
		return s.execDbGatePrimaryKeySelect(sql, start)
	}

	// 13. Indexes (DbGate indexes.js, indexcols.js)
	if strings.Contains(upper, "PG_INDEX") {
		return s.execDbGateIndexSelect(sql, start)
	}

	// 14. Triggers (DbGate triggers.js)
	if strings.Contains(upper, "PG_TRIGGER") {
		return s.execDbGateTriggerSelect(sql, start)
	}

	// 15. Column metadata query: pg_attribute + pg_class (DbGate columns.js, HeidiSQL)
	if strings.Contains(upper, "PG_ATTRIBUTE") && strings.Contains(upper, "PG_CLASS") {
		return s.execHeidiSQLColumnMetadataSelect(sql, start)
	}

	// 16. Procedure / Function metadata query
	if strings.Contains(upper, "PG_PROC") && strings.Contains(upper, "PG_NAMESPACE") {
		return s.execHeidiSQLProcSelect(sql, start)
	}

	// 17. Class + Namespace: Tables and Views (DbGate tableList.js, views.js, HeidiSQL)
	return s.execHeidiSQLClassSelect(sql, start)
}

func (s *Server) execDbGatePrimaryKeySelect(sql string, start time.Time) (*QueryResult, error) {
	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"constraint_schema", "constraint_name", "schema_name",
		"pure_name", "table_name", "column_name",
	}

	var allRows []map[string]any
	tables, _ := s.engine.ListTables()
	for _, t := range tables {
		for _, c := range t.Columns {
			if c.PrimaryKey {
				allRows = append(allRows, map[string]any{
					"constraint_schema": targetSchema,
					"constraint_name":   "pk_" + t.Name,
					"schema_name":       targetSchema,
					"pure_name":         t.Name,
					"table_name":        t.Name,
					"column_name":       c.Name,
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

func (s *Server) execDbGateIndexSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)
	targetSchema := "public"

	// DbGate indexcols.js: SELECT a.attname as "column_name", a.attnum as "attnum", a.attrelid as "oid" ...
	if strings.Contains(upper, "ATTNAME") && strings.Contains(upper, "ATTNUM") {
		columns := []string{"column_name", "attnum", "oid"}
		var allRows []map[string]any
		tables, _ := s.engine.ListTables()
		for tIdx, t := range tables {
			toid := tableBaseOID(tIdx)
			for i, c := range t.Columns {
				allRows = append(allRows, map[string]any{
					"column_name": c.Name,
					"attnum":      int16(i + 1),
					"oid":         toid,
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

	// DbGate indexes.js:
	columns := []string{
		"table_name", "pure_name", "schema_name", "index_name",
		"is_primary", "is_unique", "indkey", "indoption", "oid",
	}
	var allRows []map[string]any
	tables, _ := s.engine.ListTables()
	for tIdx, t := range tables {
		toid := tableBaseOID(tIdx)
		hasPK := false
		for _, c := range t.Columns {
			if c.PrimaryKey {
				hasPK = true
				break
			}
		}
		if hasPK {
			allRows = append(allRows, map[string]any{
				"table_name":  t.Name,
				"pure_name":   t.Name,
				"schema_name": targetSchema,
				"index_name":  "pk_" + t.Name,
				"is_primary":  true,
				"is_unique":   true,
				"indkey":      "1",
				"indoption":   "0",
				"oid":         toid,
			})
		}
		for _, idx := range t.Indexes {
			allRows = append(allRows, map[string]any{
				"table_name":  t.Name,
				"pure_name":   t.Name,
				"schema_name": targetSchema,
				"index_name":  idx.Name,
				"is_primary":  false,
				"is_unique":   idx.Unique,
				"indkey":      "1",
				"indoption":   "0",
				"oid":         toid,
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

func (s *Server) execDbGateTriggerSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"schema_name", "pure_name", "table_name", "trigger_name",
		"timing", "event", "action_statement",
	}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         []map[string]any{},
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execHeidiSQLProcSelect(sql string, start time.Time) (*QueryResult, error) {
	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"schema_name", "pure_name", "object_name", "table_name", "object_kind",
		"size_bytes", "data_length", "index_length", "reltuples",
		"comment", "viewdef", "content", "hash", "proargtypes",
	}
	var allRows []map[string]any
	if s.catalog != nil {
		for _, p := range s.catalog.ListProcedures() {
			allRows = append(allRows, map[string]any{
				"schema_name":  targetSchema,
				"pure_name":    p.Name,
				"object_name":  p.Name,
				"table_name":   p.Name,
				"object_kind":  "p",
				"size_bytes":   nil,
				"data_length":  nil,
				"index_length": nil,
				"reltuples":    nil,
				"comment":      nil,
				"viewdef":      p.Body,
				"content":      p.Body,
				"hash":         "proc_hash",
				"proargtypes":  nil,
			})
		}
		for _, f := range s.catalog.ListFunctions() {
			allRows = append(allRows, map[string]any{
				"schema_name":  targetSchema,
				"pure_name":    f.Name,
				"object_name":  f.Name,
				"table_name":   f.Name,
				"object_kind":  "f",
				"size_bytes":   nil,
				"data_length":  nil,
				"index_length": nil,
				"reltuples":    nil,
				"comment":      nil,
				"viewdef":      f.Body,
				"content":      f.Body,
				"hash":         "func_hash",
				"proargtypes":  nil,
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

func (s *Server) execHeidiSQLClassSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)
	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"schema_name", "pure_name", "object_name", "table_name", "object_kind",
		"size_bytes", "data_length", "index_length", "reltuples",
		"comment", "viewdef", "content", "hash", "proargtypes",
	}

	var allRows []map[string]any

	// If query targets views specifically (DbGate views.js, HeidiSQL relkind = 'v', matviews)
	if strings.Contains(upper, "RELKIND = 'V'") || strings.Contains(upper, "VIEW_DEFS") || strings.Contains(upper, "MATVIEW") {
		if s.catalog != nil {
			for _, v := range s.catalog.ListViews() {
				allRows = append(allRows, map[string]any{
					"schema_name":  targetSchema,
					"pure_name":    v.Name,
					"object_name":  v.Name,
					"table_name":   v.Name,
					"object_kind":  "v",
					"size_bytes":   int64(0),
					"data_length":  int64(0),
					"index_length": int64(0),
					"reltuples":    float32(0),
					"comment":      nil,
					"viewdef":      v.Query,
					"content":      v.Query,
					"hash":         "view_hash",
					"proargtypes":  nil,
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

	// Standard tables query (DbGate tableList.js, HeidiSQL)
	tables, _ := s.engine.ListTables()
	for _, t := range tables {
		allRows = append(allRows, map[string]any{
			"schema_name":  targetSchema,
			"pure_name":    t.Name,
			"object_name":  t.Name,
			"table_name":   t.Name,
			"object_kind":  "r",
			"size_bytes":   int64(16384),
			"data_length":  int64(16384),
			"index_length": int64(8192),
			"reltuples":    float32(1000),
			"comment":      nil,
			"viewdef":      nil,
			"content":      nil,
			"hash":         "table_hash",
			"proargtypes":  nil,
		})
	}
	if s.catalog != nil {
		for _, v := range s.catalog.ListViews() {
			allRows = append(allRows, map[string]any{
				"schema_name":  targetSchema,
				"pure_name":    v.Name,
				"object_name":  v.Name,
				"table_name":   v.Name,
				"object_kind":  "v",
				"size_bytes":   int64(0),
				"data_length":  int64(0),
				"index_length": int64(0),
				"reltuples":    float32(0),
				"comment":      nil,
				"viewdef":      v.Query,
				"content":      v.Query,
				"hash":         "view_hash",
				"proargtypes":  nil,
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

func (s *Server) execHeidiSQLColumnMetadataSelect(sql string, start time.Time) (*QueryResult, error) {
	// Look for target table name if specified, e.g. pgc.relname = 'accounts' or c.relname = '...'
	targetTable := ""
	tblRe := regexp.MustCompile(`(?is)(?:pgc|c)\.relname\s*=\s*'([^']+)'`)
	if m := tblRe.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = m[1]
	}

	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"schema_name", "pure_name", "table_name",
		"postgres_table_id", "column_name", "postgres_column_id",
		"attnum", "atttypid", "data_type", "is_nullable",
		"char_max_length", "character_maximum_length",
		"numeric_precision", "numeric_scale",
		"default_value", "column_default", "column_comment",
	}

	var allRows []map[string]any
	tables, _ := s.engine.ListTables()

	for tIdx, t := range tables {
		if targetTable != "" && !strings.EqualFold(t.Name, targetTable) {
			continue
		}
		toid := tableBaseOID(tIdx)
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
			var numPrec, numScale any = nil, nil
			if c.Type == storage.TypeDecimal {
				numPrec = int64(c.Precision)
				numScale = int64(c.Scale)
			} else if c.Type == storage.TypeInt {
				numPrec = int64(32)
				numScale = int64(0)
			}
			allRows = append(allRows, map[string]any{
				"schema_name":              targetSchema,
				"pure_name":                t.Name,
				"table_name":               t.Name,
				"postgres_table_id":        toid,
				"column_name":              c.Name,
				"postgres_column_id":       int16(i + 1),
				"attnum":                   int16(i + 1),
				"atttypid":                 oid,
				"data_type":                dtName,
				"is_nullable":              isNullable,
				"char_max_length":          maxLen,
				"character_maximum_length": maxLen,
				"numeric_precision":        numPrec,
				"numeric_scale":            numScale,
				"default_value":            nil,
				"column_default":           nil,
				"column_comment":           nil,
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

func formatMSSQLDataType(c storage.Column) string {
	switch c.Type {
	case storage.TypeInt:
		return "int"
	case storage.TypeFloat:
		return "float"
	case storage.TypeDecimal:
		if c.Precision > 0 && c.Scale > 0 {
			return fmt.Sprintf("decimal(%d,%d)", c.Precision, c.Scale)
		} else if c.Precision > 0 {
			return fmt.Sprintf("decimal(%d)", c.Precision)
		}
		return "decimal(18,2)"
	case storage.TypeVarChar:
		prec := c.Precision
		if prec <= 0 {
			prec = 255
		}
		return fmt.Sprintf("varchar(%d)", prec)
	case storage.TypeBool:
		return "bit"
	case storage.TypeDateTime:
		return "datetime"
	case storage.TypeDate:
		return "date"
	case storage.TypeTime:
		return "time(0)"
	case storage.TypeUUID:
		return "uniqueidentifier"
	case storage.TypeBinary:
		return "varbinary(max)"
	case storage.TypeJSON:
		return "nvarchar(max)"
	default:
		return "varchar(255)"
	}
}

func (s *Server) execBeekeeperMSSQLColumnSelect(sql string, start time.Time) (*QueryResult, error) {
	// Beekeeper Studio queries:
	// SELECT table_schema as "table_schema", table_name as "table_name", column_name as "column_name",
	//        ordinal_position as "ordinal_position", column_default as "column_default", ic.is_nullable as "is_nullable",
	//        CASE WHEN sc.definition is not null THEN 'YES' ELSE 'NO' END as "is_generated",
	//        CASE ... END as "data_type"
	// FROM INFORMATION_SCHEMA.COLUMNS ic
	// LEFT JOIN sys.computed_columns sc ON OBJECT_ID(...) = sc.object_id AND ic.COLUMN_NAME = sc.name
	// WHERE table_name = 'customers' AND table_schema = 'dbo'
	// ORDER BY table_schema, table_name, ordinal_position
	columns := []string{
		"table_schema", "table_name", "column_name", "ordinal_position",
		"column_default", "is_nullable", "is_generated", "data_type",
	}

	targetTable := ""
	tblRe := regexp.MustCompile(`(?is)(?:ic\.)?table_name\s*=\s*'([^']+)'`)
	if m := tblRe.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = cleanIdentifier(m[1])
	}

	targetSchema := "dbo"
	schRe := regexp.MustCompile(`(?is)(?:ic\.)?table_schema\s*=\s*'([^']+)'`)
	if m := schRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = cleanIdentifier(m[1])
	}

	tables, _ := s.engine.ListTables()
	var allRows []map[string]any

	for _, t := range tables {
		if targetTable != "" && !strings.EqualFold(t.Name, targetTable) {
			continue
		}
		for i, c := range t.Columns {
			nullable := "NO"
			if c.Nullable {
				nullable = "YES"
			}
			allRows = append(allRows, map[string]any{
				"table_schema":     targetSchema,
				"table_name":       t.Name,
				"column_name":      c.Name,
				"ordinal_position": i + 1,
				"column_default":   nil,
				"is_nullable":      nullable,
				"is_generated":     "NO",
				"data_type":        formatMSSQLDataType(c),
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

func (s *Server) getSortedTables() []*storage.TableSchema {
	if s.engine == nil {
		return nil
	}
	tables, _ := s.engine.ListTables()
	sort.Slice(tables, func(i, j int) bool {
		return tables[i].Name < tables[j].Name
	})
	return tables
}

func (s *Server) getSortedViews() []*catalog.View {
	if s.catalog == nil {
		return nil
	}
	views := s.catalog.ListViews()
	sort.Slice(views, func(i, j int) bool {
		return views[i].Name < views[j].Name
	})
	return views
}

func (s *Server) getSortedProcedures() []*catalog.Procedure {
	if s.catalog == nil {
		return nil
	}
	procs := s.catalog.ListProcedures()
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].Name < procs[j].Name
	})
	return procs
}

func parseTargetObjectIDs(sql string) map[int]bool {
	inRe := regexp.MustCompile(`(?is)(?:[a-z0-9_]+\.)?object_id\s+in\s*\(([^)]+)\)`)
	if m := inRe.FindStringSubmatch(sql); len(m) == 2 {
		res := make(map[int]bool)
		for _, part := range strings.Split(m[1], ",") {
			cleaned := strings.Trim(strings.TrimSpace(part), "'\" ")
			if id, err := strconv.Atoi(cleaned); err == nil {
				res[id] = true
			}
		}
		if len(res) > 0 {
			return res
		}
	}
	eqRe := regexp.MustCompile(`(?is)(?:[a-z0-9_]+\.)?object_id\s*=\s*['"]?(\d+)['"]?`)
	if m := eqRe.FindStringSubmatch(sql); len(m) == 2 {
		if id, err := strconv.Atoi(m[1]); err == nil {
			return map[int]bool{id: true}
		}
	}
	return nil
}

func (s *Server) execDbGateMSSQLColumnSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"columnName", "dataType", "objectId", "isIdentity",
		"maxLength", "precision", "scale", "isNullable",
		"charMaxLength", "defaultValue", "defaultConstraint",
		"computedExpression", "isPersisted", "columnId",
		"numericPrecision", "numericScale", "isSparse", "columnComment",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for cIdx, c := range t.Columns {
			dataType := "varchar"
			maxLen := 255
			prec := 0
			scale := 0
			var charMaxLen any = nil
			var numPrec any = nil
			var numScale any = nil

			switch c.Type {
			case storage.TypeInt:
				dataType = "int"
				maxLen = 4
				prec = 10
				numPrec = 10
				numScale = 0
			case storage.TypeFloat:
				dataType = "float"
				maxLen = 8
				prec = 53
				numPrec = 53
			case storage.TypeDecimal:
				dataType = "decimal"
				maxLen = 9
				prec = c.Precision
				if prec <= 0 {
					prec = 18
				}
				scale = c.Scale
				if scale < 0 {
					scale = 2
				}
				numPrec = prec
				numScale = scale
			case storage.TypeVarChar:
				dataType = "varchar"
				maxLen = c.Precision
				if maxLen <= 0 {
					maxLen = 255
				}
				charMaxLen = maxLen
			case storage.TypeBool:
				dataType = "bit"
				maxLen = 1
				prec = 1
			case storage.TypeDateTime:
				dataType = "datetime"
				maxLen = 8
				prec = 23
				scale = 3
			case storage.TypeDate:
				dataType = "date"
				maxLen = 3
				prec = 10
			case storage.TypeTime:
				dataType = "time"
				maxLen = 5
				prec = 16
				scale = 7
			case storage.TypeUUID:
				dataType = "uniqueidentifier"
				maxLen = 16
			case storage.TypeBinary:
				dataType = "varbinary"
				maxLen = 8000
			case storage.TypeJSON:
				dataType = "nvarchar"
				maxLen = -1
			}

			allRows = append(allRows, map[string]any{
				"columnName":         c.Name,
				"dataType":           dataType,
				"objectId":           objID,
				"isIdentity":         false,
				"maxLength":          maxLen,
				"precision":          prec,
				"scale":              scale,
				"isNullable":         c.Nullable,
				"charMaxLength":      charMaxLen,
				"defaultValue":       nil,
				"defaultConstraint":  nil,
				"computedExpression": nil,
				"isPersisted":        nil,
				"columnId":           cIdx + 1,
				"numericPrecision":   numPrec,
				"numericScale":       numScale,
				"isSparse":           false,
				"columnComment":      nil,
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

func (s *Server) execDbGateMSSQLPrimaryKeySelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"objectId", "pureName", "schemaName", "columnName", "constraintName",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for _, c := range t.Columns {
			if c.PrimaryKey {
				allRows = append(allRows, map[string]any{
					"objectId":       objID,
					"pureName":       t.Name,
					"schemaName":     "dbo",
					"columnName":     c.Name,
					"constraintName": "PK_" + t.Name,
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

func (s *Server) execDbGateMSSQLIndexSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"object_id", "constraintName", "indexType", "isUnique",
		"index_id", "is_unique_constraint", "filterDefinition",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for idxNum, idx := range t.Indexes {
			allRows = append(allRows, map[string]any{
				"object_id":            objID,
				"constraintName":       idx.Name,
				"indexType":            "NONCLUSTERED",
				"isUnique":             idx.Unique,
				"index_id":             idxNum + 2,
				"is_unique_constraint": false,
				"filterDefinition":     nil,
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

func (s *Server) execDbGateMSSQLIndexColumnSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"object_id", "index_id", "column_id", "columnName", "isDescending", "isIncludedColumn",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for idxNum, idx := range t.Indexes {
			for _, colName := range idx.Columns {
				colID := 1
				for cIdx, c := range t.Columns {
					if strings.EqualFold(c.Name, colName) {
						colID = cIdx + 1
						break
					}
				}
				allRows = append(allRows, map[string]any{
					"object_id":        objID,
					"index_id":         idxNum + 2,
					"column_id":        colID,
					"columnName":       colName,
					"isDescending":     false,
					"isIncludedColumn": false,
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

func (s *Server) execDbGateMSSQLForeignKeySelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"schemaName", "pureName", "columnName", "refSchemaName",
		"refTableName", "refColumnName", "constraintName",
		"updateAction", "deleteAction", "objectId",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for _, fk := range t.ForeignKeys {
			allRows = append(allRows, map[string]any{
				"schemaName":     "dbo",
				"pureName":       t.Name,
				"columnName":     strings.Join(fk.ChildColumns, ", "),
				"refSchemaName":  "dbo",
				"refTableName":   fk.ParentTable,
				"refColumnName":  strings.Join(fk.ParentColumns, ", "),
				"constraintName": fk.Name,
				"updateAction":   "NO ACTION",
				"deleteAction":   "NO ACTION",
				"objectId":       objID,
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

func (s *Server) execDbGateMSSQLRowCountSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"objectId", "tableRowCount"}
	tables := s.getSortedTables()
	var allRows []map[string]any

	var sess storage.Session
	if s.engine != nil {
		sess, _ = s.engine.NewSession()
	}
	if sess != nil {
		defer sess.Close()
	}

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		var cnt int64 = 0
		if s.catalog != nil {
			if st, ok := s.catalog.GetTableStats(t.Name); ok && st != nil && st.RowCount > 0 {
				cnt = st.RowCount
			}
		}
		if cnt == 0 && sess != nil {
			tx, err := sess.BeginTx(storage.TxOptions{})
			if err == nil {
				cursor, err := tx.Scan(t.Name)
				if err == nil {
					for cursor.Next() {
						cnt++
					}
					cursor.Close()
				}
				_ = tx.Rollback()
			}
		}
		allRows = append(allRows, map[string]any{
			"objectId":      objID,
			"tableRowCount": cnt,
		})
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

func (s *Server) execDbGateMSSQLColumnCommentSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"objectId", "columnComment", "columnId"}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         []map[string]any{},
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execDbGateMSSQLParamSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"parentObjectId", "objectId", "parameterObjectId", "pureName",
		"parameterName", "dataType", "charMaxLength", "parameterMode",
		"numericPrecision", "numericScale", "parameterIndex", "schemaName",
	}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         []map[string]any{},
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execDbGateMSSQLTriggerSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"modifyDate", "objectId", "triggerName", "schemaName",
		"tableName", "triggerTiming", "eventType", "definition",
	}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         []map[string]any{},
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Server) execDbGateMSSQLObjectSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"objectId", "modifyDate", "type", "pureName", "schemaName"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	tables := s.getSortedTables()
	for tIdx, t := range tables {
		allRows = append(allRows, map[string]any{
			"objectId":   1000 + tIdx,
			"modifyDate": defaultDate,
			"type":       "U",
			"pureName":   t.Name,
			"schemaName": "dbo",
		})
	}

	views := s.getSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"objectId":   2000 + vIdx,
			"modifyDate": defaultDate,
			"type":       "V",
			"pureName":   v.Name,
			"schemaName": "dbo",
		})
	}
	procs := s.getSortedProcedures()
	for pIdx, p := range procs {
		allRows = append(allRows, map[string]any{
			"objectId":   3000 + pIdx,
			"modifyDate": defaultDate,
			"type":       "P",
			"pureName":   p.Name,
			"schemaName": "dbo",
		})
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

func (s *Server) execDbGateMSSQLTableSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"pureName", "schemaName", "objectId", "createDate", "modifyDate", "objectComment"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	tables := s.getSortedTables()
	for tIdx, t := range tables {
		allRows = append(allRows, map[string]any{
			"pureName":      t.Name,
			"schemaName":    "dbo",
			"objectId":      1000 + tIdx,
			"createDate":    defaultDate,
			"modifyDate":    defaultDate,
			"objectComment": nil,
		})
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

func (s *Server) execDbGateMSSQLViewSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"pureName", "schemaName", "objectId", "createDate", "modifyDate"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	views := s.getSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"pureName":   v.Name,
			"schemaName": "dbo",
			"objectId":   2000 + vIdx,
			"createDate": defaultDate,
			"modifyDate": defaultDate,
		})
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

func (s *Server) execDbGateMSSQLProgrammablesSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"pureName", "schemaName", "objectId", "createDate", "modifyDate", "sqlObjectType"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	procs := s.getSortedProcedures()
	for pIdx, p := range procs {
		allRows = append(allRows, map[string]any{
			"pureName":      p.Name,
			"schemaName":    "dbo",
			"objectId":      3000 + pIdx,
			"createDate":    defaultDate,
			"modifyDate":    defaultDate,
			"sqlObjectType": "P",
		})
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

func (s *Server) execDbGateMSSQLLoadSqlCodeSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"pureName", "schemaName", "codeText"}
	var allRows []map[string]any

	if s.catalog != nil {
		for _, v := range s.catalog.ListViews() {
			allRows = append(allRows, map[string]any{
				"pureName":   v.Name,
				"schemaName": "dbo",
				"codeText":   v.Query,
			})
		}
		for _, p := range s.catalog.ListProcedures() {
			allRows = append(allRows, map[string]any{
				"pureName":   p.Name,
				"schemaName": "dbo",
				"codeText":   p.Body,
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


