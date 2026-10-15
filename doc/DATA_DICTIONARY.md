# SQLion Data Dictionary & Virtual System Catalog

SQLion provides built-in virtual system catalog views adhering to both Microsoft SQL Server (`sys.*`) and ANSI/ISO standard (`information_schema.*`) specifications. These virtual views allow standard database client tools—such as DBeaver, JetBrains DataGrip, SQL Server Management Studio (SSMS), Azure Data Studio, Navicat, and `psql`—to inspect schemas, tables, columns, indexes, procedures, and query optimizer statistics seamlessly.

---

## 1. Architecture & Emulation Model

All catalog queries against `sys.*` and `information_schema.*` are intercepted dynamically in the query execution layer (`pkg/api/sqlexec.go`). Rather than persisting static tables, SQLion constructs catalog representations on-the-fly from the underlying storage metadata, procedure catalog (`pkg/proc/catalog.go`), and cost-based optimizer table statistics (`pkg/cbo/statistics.go`).

Supported naming conventions include dot notation (`sys.tables`, `information_schema.tables`), underscore notation (`sys_tables`), unified shorthand (`systables`), and schema prefixes (`dbo.sys.tables`).

```
  Client Catalog Query (e.g. SELECT * FROM sys.tables)
                          │
                          ▼
            [ SQL Parser & Dispatcher ]
                          │
                          ├─► Is Virtual Catalog Table?
                          │         │ (Yes)
                          │         ▼
                          │   [ execVirtualCatalogSelect ]
                          │         │
                          │         ├─► Storage Metadata (Tables, Columns, Indexes)
                          │         ├─► Procedure Catalog (Routines)
                          │         └─► Cost-Based Optimizer (Table & Column Stats)
                          │
                          ▼
                 [ Virtual Row Projection ]
                          │
                          ▼
              Filtered & Sorted Result Set
```

> [!NOTE]
> While catalog queries are emulated dynamically through virtual views, physical schema definitions and optimizer statistics are persisted to disk via `catalog.json` and `cbo_stats.json` in the instance data directory. For low-level details on physical directory layouts, atomic persistence, and secondary index structures, see [`INTERNALS_STORAGE.md`](file:///Users/maro/Projects/SQLion/doc/INTERNALS_STORAGE.md#5-physical-storage-directory-layout-metadata--secondary-index-persistence).

---

## 2. Table Catalog Views

### 2.1 `sys.tables` / `information_schema.tables`

Presents the catalog of user-defined base tables.

- **Aliases**: `sys.tables`, `systables`, `sys_tables`, `information_schema.tables`
- **Scope**: User base tables defined in the active database engine.

| Column Name | Data Type | Nullable | Description |
|---|---|---|---|
| `table_name` | `VARCHAR(128)` | NO | Name of the table. |
| `table_type` | `VARCHAR(64)` | NO | Table category; returns `'BASE TABLE'`. |
| `table_schema` | `VARCHAR(64)` | NO | Schema name; returns `'dbo'`. |
| `column_count` | `INTEGER` | NO | Total number of columns defined on the table. |
| `create_date` | `VARCHAR(32)` | NO | Creation timestamp in ISO 8601 format. |

#### Example Query:
```sql
SELECT table_name, column_count, create_date
FROM sys.tables
ORDER BY table_name ASC;
```

---

## 3. Column Catalog Views

### 3.1 `sys.columns` / `information_schema.columns`

Provides structural information regarding all columns across all registered tables.

- **Aliases**: `sys.columns`, `syscolumns`, `sys_columns`, `information_schema.columns`

| Column Name | Data Type | Nullable | Description |
|---|---|---|---|
| `table_name` | `VARCHAR(128)` | NO | Name of the parent table. |
| `column_name` | `VARCHAR(128)` | NO | Name of the column. |
| `data_type` | `VARCHAR(64)` | NO | SQLion data type representation (`INT`, `BIGINT`, `VARCHAR(n)`, `FLOAT`, `BOOLEAN`, `JSON`, `BLOB`, `DATETIME`). |
| `is_nullable` | `VARCHAR(3)` | NO | `'YES'` if column permits NULL values, `'NO'` otherwise. |
| `is_primary_key` | `BOOLEAN` | NO | `true` if column forms part of the primary key, `false` otherwise. |
| `ordinal_position` | `INTEGER` | NO | 1-based sequential position of the column within the table. |

#### Example Query:
```sql
SELECT column_name, data_type, is_nullable, is_primary_key
FROM sys.columns
WHERE table_name = 'users'
ORDER BY ordinal_position ASC;
```

---

## 4. Index Catalog Views

### 4.1 `sys.indexes`

Details secondary and primary indexes defined across engine tables.

- **Aliases**: `sys.indexes`, `sysindexes`, `sys_indexes`

| Column Name | Data Type | Nullable | Description |
|---|---|---|---|
| `table_name` | `VARCHAR(128)` | NO | Name of the indexed table. |
| `index_name` | `VARCHAR(128)` | NO | Identifier of the index. |
| `is_unique` | `BOOLEAN` | NO | `true` if index enforces uniqueness constraint. |
| `columns` | `VARCHAR(256)` | NO | Comma-separated list of column names covered by the index. |

#### Example Query:
```sql
SELECT index_name, is_unique, columns
FROM sys.indexes
WHERE table_name = 'orders';
```

---

## 5. Database & Schema Catalog Views

### 5.1 `sys.databases` / `information_schema.schemata`

Lists the databases hosted by the current SQLion instance.

- **Aliases**: `sys.databases`, `sysdatabases`, `sys_databases`, `information_schema.schemata`

| Column Name | Data Type | Nullable | Description |
|---|---|---|---|
| `name` | `VARCHAR(128)` | NO | Database name (`'sqlion'`). |
| `database_id` | `INTEGER` | NO | Internal database numeric identifier (`1`). |
| `create_date` | `VARCHAR(32)` | NO | Instance initialization timestamp. |
| `state_desc` | `VARCHAR(32)` | NO | Current availability state; returns `'ONLINE'`. |

#### Example Query:
```sql
SELECT name, database_id, state_desc FROM sys.databases;
```

---

## 6. Stored Procedure Catalog Views

### 6.1 `sys.procedures` / `information_schema.routines`

Provides metadata for all registered T-SQL stored procedures managed by the bytecode virtual machine engine.

- **Aliases**: `sys.procedures`, `sysprocedures`, `sys_procedures`, `information_schema.routines`

| Column Name | Data Type | Nullable | Description |
|---|---|---|---|
| `routine_name` | `VARCHAR(128)` | NO | Stored procedure name. |
| `routine_type` | `VARCHAR(64)` | NO | Routine category; returns `'PROCEDURE'`. |
| `routine_schema` | `VARCHAR(64)` | NO | Schema name; returns `'dbo'`. |
| `created_date` | `VARCHAR(32)` | NO | Creation or registration timestamp in RFC 3339 format. |

#### Example Query:
```sql
SELECT routine_name, routine_schema, created_date
FROM sys.procedures
ORDER BY routine_name ASC;
```

---

## 7. Cost-Based Optimizer Statistics Views

SQLion exposes its internal Volcano Cost-Based Optimizer (CBO) catalog statistics through virtual views to allow query tuning and execution plan analysis.

### 7.1 `sys.table_stats`

Exposes table-level cardinality and storage page counts populated during `ANALYZE TABLE <name>`.

- **Aliases**: `sys.table_stats`, `systablestats`, `sys_table_stats`

| Column Name | Data Type | Nullable | Description |
|---|---|---|---|
| `table_name` | `VARCHAR(128)` | NO | Table name. |
| `row_count` | `BIGINT` | NO | Estimated total number of rows. |
| `page_count` | `BIGINT` | NO | Storage page allocation footprint. |
| `last_analyzed` | `VARCHAR(32)` | NO | Timestamp of the most recent statistical analysis pass. |

### 7.2 `sys.column_stats`

Exposes column-level statistical distributions, null fractions, distinct value estimates (NDV), min/max boundaries, and equi-depth histogram bucketing.

- **Aliases**: `sys.column_stats`, `syscolumnstats`, `sys_column_stats`

| Column Name | Data Type | Nullable | Description |
|---|---|---|---|
| `table_name` | `VARCHAR(128)` | NO | Target table name. |
| `column_name` | `VARCHAR(128)` | NO | Target column name. |
| `null_count` | `BIGINT` | NO | Total count of null values. |
| `distinct_count` | `BIGINT` | NO | Number of distinct values (NDV) sampled or calculated. |
| `min_value` | `VARCHAR(256)` | YES | Minimum scalar value observed in sample. |
| `max_value` | `VARCHAR(256)` | YES | Maximum scalar value observed in sample. |
| `histogram_buckets` | `INTEGER` | NO | Number of equi-depth histogram buckets generated. |

#### Example Query:
```sql
SELECT column_name, distinct_count, null_count, min_value, max_value
FROM sys.column_stats
WHERE table_name = 'customers';
```

---

## 8. Client Tool Compatibility Matrix

| Client Tool | Protocol | Catalog Query Strategy | SQLion Compatibility |
|---|---|---|---|
| **DBeaver** | PostgreSQL (`:5433`) | Queries `information_schema.tables`, `information_schema.columns` | Full native tree navigation |
| **DataGrip** | PostgreSQL (`:5433`) | Queries `information_schema` and `pg_catalog` | Full schema inspection |
| **SSMS** | TDS (`:1433`) | Queries `sys.databases`, `sys.tables`, `sys.columns`, `sys.procedures` | Full Object Explorer browsing |
| **Azure Data Studio** | TDS (`:1433`) | Queries `sys.tables`, `sys.columns`, `sys.indexes` | Full query & schema editing |
| **Navicat** | PostgreSQL (`:5433`) | Queries `information_schema.tables` and column definitions | Supported |
| **psql CLI** | PostgreSQL (`:5433`) | `\dt`, `\d <table>`, `SELECT * FROM information_schema.*` | Supported |
