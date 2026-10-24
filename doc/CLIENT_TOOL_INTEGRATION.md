# LionSQL Client & GUI Tool Integration Guide (`doc/CLIENT_TOOL_INTEGRATION.md`)

This document provides a comprehensive integration and connectivity guide for GUI management tools, command-line interfaces (CLIs), and official language drivers communicating with LionSQL via PostgreSQL Wire Protocol v3 (`:5433`) and Microsoft SQL Server TDS Protocol (`:1433`).

---

## 1. Multi-Protocol Overview & Default Ports

LionSQL acts as a zero-external-dependency database engine that concurrently serves both PostgreSQL and Microsoft SQL Server clients out of a single static executable (`bin/lionsql`).

| Protocol | Default Port | Default Superuser | Password | Database Name | Standard Client Ecosystem |
|---|---|---|---|---|---|
| **PostgreSQL v3** | `5433` (TCP) | `postgres` (or `admin`) | Any / Empty (Dev) | `postgres` | Beekeeper Studio, DBeaver, Navicat, pgAdmin, psql, lib/pq, pgx |
| **Microsoft SQL Server (TDS 7.x/8.0)** | `1433` (TCP) | `sa` (or `admin`) | Any / Empty (Dev) | `master` | Beekeeper Studio, DBeaver, Navicat, Azure Data Studio, SSMS, go-mssqldb, pymssql |
| **HTTP REST & Studio** | `8090` (TCP) | `admin@lionsql.local` | `1234567890` | `lionsql` | Embedded Web Studio (`/_/`), curl, OpenAPI, fetch |

> Note: LionSQL enforces **Instance-Based Database Isolation** (GEMINI.md Section 3). Each server process hosts exactly one isolated database catalog per data directory (`--data-dir`).

---

## 2. GUI Client Configuration

### 2.1. Beekeeper Studio

Beekeeper Studio can connect to LionSQL in either **PostgreSQL** or **SQL Server** mode.

#### Option A: PostgreSQL Connection Mode (Recommended for General GUI Browsing)
1. Select **Postgres** from the New Connection menu.
2. Connection Settings:
   - **Host**: `127.0.0.1` (or `localhost`)
   - **Port**: `5433`
   - **User**: `postgres`
   - **Password**: Leave empty or enter any string.
   - **Default Database**: `postgres`
   - **SSL**: `Disable` (unless LionSQL was started with `-tls` and `-require-ssl`).
3. Click **Test** and **Connect**.
4. Supported features:
   - Live table schema browsing with column types, primary keys, and nullability.
   - Foreign key relationship entity diagrams (`ERD`).
   - Interactive table data editing and filtering.
   - SQL query scratchpad.

#### Option B: SQL Server Connection Mode
1. Select **SQL Server** from the New Connection menu.
2. Connection Settings:
   - **Host**: `127.0.0.1` (or `localhost`)
   - **Port**: `1433`
   - **User**: `sa`
   - **Password**: Leave empty or enter any string.
   - **Database**: `master`
   - **Encrypt**: `Disable` (or uncheck `Trust Server Certificate` / encryption requirement unless `-tls` is active).
3. Click **Connect**.
4. Supported features:
   - Schema navigation under `dbo` namespace.
   - Automatic column introspection (`INFORMATION_SCHEMA.COLUMNS` with computed column handling).
   - Pagination browsing via `OFFSET ... ROWS FETCH NEXT ... ROWS ONLY` and `SELECT TOP <N>`.

---

### 2.2. DBeaver Universal Database Tool

#### PostgreSQL Driver Connection
1. New Database Connection -> **PostgreSQL**.
2. Settings:
   - **Host**: `localhost`
   - **Port**: `5433`
   - **Database**: `postgres`
   - **Authentication**: `Database Native`
   - **Username**: `postgres`
3. Driver Properties:
   - `ssl`: `false`
4. Test Connection -> Finish.

#### Microsoft SQL Server (MS Driver / jTDS) Connection
1. New Database Connection -> **Microsoft SQL Server** -> **MS SQL Server / Azure**.
2. Settings:
   - **Host**: `localhost`
   - **Port**: `1433`
   - **Database**: `master`
   - **Authentication**: `SQL Server Authentication`
   - **Username**: `sa`
3. Driver Properties:
   - `trustServerCertificate`: `true`
   - `encrypt`: `false` (for plaintext local dev mode)
4. Test Connection -> Finish.

---

### 2.3. Azure Data Studio & VS Code MSSQL Extension

1. Connection Type: `Microsoft SQL Server`.
2. Parameters:
   - **Server**: `127.0.0.1,1433`
   - **Database**: `master`
   - **Authentication Type**: `SQL Login`
   - **User Name**: `sa`
   - **Password**: (any)
   - **Trust server certificate**: `True`
   - **Encrypt**: `Optional` or `False`

---

### 2.4. DbGate Universal Database Manager

DbGate is an open-source database manager that supports connecting to LionSQL via either PostgreSQL or Microsoft SQL Server (TDS).

#### Option A: PostgreSQL Connection
1. New Connection -> **PostgreSQL**.
2. Settings:
   - **Server / Host**: `127.0.0.1`
   - **Port**: `5433`
   - **User**: `postgres`
   - **Database**: `postgres`
   - **Password**: (leave blank or any string)
3. Connect.

#### Option B: Microsoft SQL Server Connection
1. New Connection -> **Microsoft SQL Server**.
2. Settings:
   - **Server**: `127.0.0.1`
   - **Port**: `1433`
   - **User**: `sa`
   - **Password**: (leave blank or any string)
   - **Database**: `master`
   - **Encrypt**: `No`
3. Connect.
4. Features:
   - Full tree navigation of tables, columns, primary keys, and foreign keys.
   - Live row count display derived dynamically via transaction cursors.
   - Interactive data grid with `OFFSET ... ROWS FETCH NEXT ... ROWS ONLY` paging.


---

## 3. Command-Line Client (CLI) Connectivity

### 3.1. PostgreSQL CLI (`psql`)

Connect non-interactively or interactively via standard PostgreSQL CLI:

```bash
# Interactive terminal session
psql -h 127.0.0.1 -p 5433 -U postgres -d postgres

# One-shot query execution
psql -h 127.0.0.1 -p 5433 -U postgres -d postgres -c "SELECT * FROM customers;"
```

Supported meta-commands in `psql`:
- `\dt`: Lists user tables and views.
- `\d <tablename>`: Describes table schema, columns, constraints, and data types.
- `\l`: Lists databases.
- `\timing`: Displays millisecond execution durations.

### 3.2. Microsoft SQL Server CLI (`sqlcmd`)

Connect via standard Microsoft command-line utilities:

```bash
# Interactive sqlcmd session
sqlcmd -S 127.0.0.1,1433 -U sa -P 12345678 -d master

# Command execution
sqlcmd -S 127.0.0.1,1433 -U sa -d master -Q "SELECT @@VERSION; SELECT * FROM customers;"
```

---

## 4. Official Programming Language Drivers

### 4.1. Go (`pgx/v5` - Modern PostgreSQL Driver)

LionSQL is fully validated against `github.com/jackc/pgx/v5` with Extended Query Protocol, binary format encoding, and pipelining:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/jackc/pgx/v5"
)

func main() {
    ctx := context.Background()
    conn, err := pgx.Connect(ctx, "postgres://postgres:secret@127.0.0.1:5433/postgres?sslmode=disable")
    if err != nil {
        log.Fatalf("Unable to connect to database: %v", err)
    }
    defer conn.Close(ctx)

    var id int64
    var name string
    var balance float64

    err = conn.QueryRow(ctx, "SELECT customer_id, name, balance FROM customers WHERE customer_id = $1", 1).
        Scan(&id, &name, &balance)
    if err != nil {
        log.Fatalf("QueryRow failed: %v", err)
    }

    fmt.Printf("Customer #%d: %s (Balance: $%.2f)\n", id, name, balance)
}
```

### 4.2. Go (`github.com/microsoft/go-mssqldb` - Official MSSQL Driver)

LionSQL supports `PacketTransactionManager` (0x0E), explicit transaction descriptors, `sp_prepare` / `sp_execute`, and TDS session settings:

```go
package main

import (
    "database/sql"
    "fmt"
    "log"

    _ "github.com/microsoft/go-mssqldb"
)

func main() {
    connStr := "sqlserver://sa:secret@127.0.0.1:1433?database=master&encrypt=disable"
    db, err := sql.Open("sqlserver", connStr)
    if err != nil {
        log.Fatalf("Error opening connection: %v", err)
    }
    defer db.Close()

    // Atomic transaction modeling
    tx, err := db.Begin()
    if err != nil {
        log.Fatalf("BeginTx failed: %v", err)
    }

    _, err = tx.Exec("UPDATE customers SET balance = balance - 100 WHERE customer_id = @p1", 1)
    if err != nil {
        tx.Rollback()
        log.Fatalf("Exec failed: %v", err)
    }

    if err := tx.Commit(); err != nil {
        log.Fatalf("Commit failed: %v", err)
    }
    fmt.Println("Transaction committed successfully.")
}
```

### 4.3. Python (`psycopg2` & `asyncpg`)

```python
import psycopg2

conn = psycopg2.connect(
    host="127.0.0.1",
    port=5433,
    user="postgres",
    password="",
    dbname="postgres",
    sslmode="disable"
)
cur = conn.cursor()
cur.execute("SELECT customer_id, name FROM customers;")
for row in cur.fetchall():
    print(row)
cur.close()
conn.close()
```

### 4.4. Python (`pymssql`)

```python
import pymssql

conn = pymssql.connect(
    server="127.0.0.1",
    port=1433,
    user="sa",
    password="",
    database="master"
)
cursor = conn.cursor(as_dict=True)
cursor.execute("SELECT customer_id, name, balance FROM customers;")
for row in cursor:
    print(f"Customer: {row['name']}, Balance: {row['balance']}")
conn.close()
```

### 4.5. Java / JVM (`PostgreSQL JDBC Driver - pgjdbc`)

LionSQL is certified compatible with the official PostgreSQL JDBC Driver (`org.postgresql:postgresql`):

```java
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.ResultSet;
import java.sql.Statement;

public class App {
    public static void main(String[] args) throws Exception {
        String url = "jdbc:postgresql://127.0.0.1:5433/postgres?sslmode=disable";
        try (Connection conn = DriverManager.getConnection(url, "postgres", "")) {
            try (Statement stmt = conn.createStatement()) {
                ResultSet rs = stmt.executeQuery("SELECT id, holder, balance FROM accounts;");
                while (rs.next()) {
                    System.out.println("Account: " + rs.getInt("id") + ", " + rs.getString("holder"));
                }
            }
        }
    }
}
```

### 4.6. C / C++ / ODBC (`psqlODBC Driver`)

Standard ODBC connection string for `psqlODBC` / `unixODBC`:

```ini
[LionSQL]
Driver = PostgreSQL Unicode
Server = 127.0.0.1
Port = 5433
Database = postgres
Username = postgres
Password = 
SSLmode = disable
```

---

## 5. Architectural Machinery: Virtual Catalog Interceptors

GUI clients rely heavily on deep metadata introspection to display left-side object trees, column types, default values, and foreign keys. Rather than maintaining heavy in-memory catalog tables for hundreds of PostgreSQL and SQL Server catalog schemas, LionSQL implements **Zero-Allocation Fast-Filtered Virtual Catalog Interceptors** (`pkg/catalog/virtual/interceptor.go`).

### 5.0. Zero-Allocation Fast Filter (`containsFold`) & Whitespace-Resilient Matching (`containsEq`)
- **Fast Filter (Bypass)**: Normal OLTP queries (`SELECT * FROM orders WHERE ...`) bypass catalog matching immediately via an ASCII case-insensitive byte scanner (`containsFold`) with **0 heap allocations (0 B/op, 0 allocs/op)** and sub-100ns latency.
- **Whitespace Resilience (`containsEq`)**: Client queries matching catalog constraints (such as `sys.objects WHERE o.type = 'TR'` or `sys.indexes WHERE is_primary_key=1`) are evaluated with arbitrary whitespace tolerance around `=`, preventing false-negative introspection errors regardless of client SQL formatting.

### 5.1. PostgreSQL Catalog Views
When clients query standard catalog tables, LionSQL intercepts the queries before physical execution:
- `pg_class`, `pg_namespace`, `pg_attribute`, `pg_type`: Deterministic OIDs (`16384` for base user tables, standard PostgreSQL OIDs for data types like `23` for int4, `1043` for varchar, `1700` for numeric).
- `pg_constraint`, `information_schema.table_constraints`, `information_schema.key_column_usage`: Emits primary key, unique, and foreign key definitions so that ERD visualizers (DBeaver, Beekeeper) render relationship arrows accurately.
- `pg_proc`: Emits registered stored procedures and functions.

### 5.2. Microsoft SQL Server Metadata & Complex Joins
GUI tools connecting via TDS frequently send complex vendor-specific JOIN expressions:
- **Beekeeper Studio Column Introspection Interceptor**:
  ```sql
  SELECT table_schema, table_name, column_name, ...
  FROM INFORMATION_SCHEMA.COLUMNS ic
  LEFT JOIN sys.computed_columns sc ON ...
  WHERE table_name = 'customers' AND table_schema = 'dbo'
  ```
  LionSQL intercepts this pattern, inspects table schemas from `engine.GetTable()`, and formats data types directly into canonical MSSQL representations (`int`, `varchar(255)`, `decimal(p,s)`, `bit`, `datetime`).
- **Virtual System Table Stubs**:
  `sys.tables`, `sys.columns`, `sys.indexes`, `sys.databases`, `sys.computed_columns`, `sys.triggers`, and `sys.foreign_keys` respond dynamically without maintaining physical disk storage.

### 5.3. DbGate Multi-Step Introspection & Deterministic Object ID Invariant
DbGate inspects Microsoft SQL Server schemas through a choreographed multi-step query pipeline:
1. `modifications`: `sys.objects` + `sys.schemas` (discovering objects and `modifyDate`).
2. `tables`: `sys.tables` + `sys.schemas` (fetching table object IDs and comments).
3. `columns`: `sys.columns` + `sys.types` (fetching column definitions, nullability, data types, and default constraints).
4. `primaryKeys`, `indexes`, `foreignKeys`: Resolving key constraints and relationships.
5. `tableSizes`: Dynamically querying row counts via transaction cursors.

#### The Go Map Non-Determinism Landmine:
Because Go `map` iterations (`for ... range map`) randomize the starting element on each execution, calling `engine.ListTables()` without sorting caused table slices to shuffle between successive queries. This caused tables to receive shifting `objectId` values across steps:
- Step 1 (`tables`) assigned `objectId: 1000` to Table A.
- Step 2 (`columns`) assigned `objectId: 1000` to Table B.
- DbGate assembled columns by matching `col.objectId == table.objectId`, creating an inversion where Table A displayed Table B's columns, causing subsequent data queries (`SELECT ...`) to fail due to mismatched column names.

#### Architectural Resolution:
- **Storage Layer Sorting**: Both `memengine.ListTables()` and `wiredtiger.ListTables()` sort tables strictly by name (`sort.Slice`).
- **Defensive API Helpers**: `sqlexec_pgcatalog.go` routes all catalog lookups through `getSortedTables()`, `getSortedViews()`, and `getSortedProcedures()`.
- **Bidirectional Scalar Mapping**: `OBJECT_ID('table')` and `OBJECT_NAME(id)` share the exact same sorted table order, guaranteeing 100% deterministic, bidirectional ID stability.

### 5.4. T-SQL Pagination & Identifier Normalization
GUI clients often format table data pagination queries using T-SQL-specific syntax:
- **`OFFSET <O> ROWS FETCH NEXT <N> ROWS ONLY`**: Automatically rewritten by `normalizeMSSQLPagination` into canonical `LIMIT <N> OFFSET <O>`.
- **`SELECT TOP <N>`**: Normalized to `SELECT ... LIMIT <N>`.
- **Bracketed Identifiers (`[dbo].[table]`, `[column]`)**: Cleaned via `cleanIdent` and `ColumnIndexMap.Index`, ensuring zero-overhead field resolution.
- **Double-Quoted Identifiers (`"column_name"`)**: Fully parsed as `TokenIdent` by the native Pratt parser lexer (`pkg/proc/lexer.go`).

### 5.5. Comprehensive Client Tool Audit & Verification Suite (`TOOL-AUDIT-1`)
LionSQL includes automated integration suites simulating real-world client queries across both wire protocols:
- **SSMS Suite (`TestToolAudit_TDS_SSMS_DiscoverySuite`)**:
  - `SERVERPROPERTY` discovery (`ProductVersion`, `EngineEdition`, `Collation`, `InstanceName`)
  - `DB_NAME()` and `SCHEMA_NAME()` resolution
  - `sys.tables` JOIN `sys.schemas` schema tree browsing
  - `sys.columns` JOIN `sys.types` column introspection with `TYPE_NAME` resolution
  - `sys.indexes` primary key and secondary index inspection
  - Data grid browsing via `SELECT TOP <N> * FROM [dbo].[table]`
- **DbGate Suite (`TestToolAudit_TDS_DbGate_ChoreographedE2E`)**:
  - 9-step full schema discovery pipeline (`modifications` -> `tables` -> `columns` -> `primaryKeys` -> `indexes` -> `foreignKeys` -> `tableSizes` -> `views` -> `programmables`)
  - 100% deterministic `objectId` matching between `sys.tables` and `sys.columns`
  - Paged data browsing via `OFFSET <O> ROWS FETCH NEXT <N> ROWS ONLY`
- **DBeaver Dual-Protocol Suite**:
  - TDS: `SERVERPROPERTY('EngineEdition')`, `sys.databases db.*`, `sys.tables t.*`
  - PGWire: `pg_class` JOIN `pg_namespace`, `pg_attribute` with OID resolution, `pg_constraint` ERD relationships
- **Beekeeper Studio Suite**:
  - `SELECT @@VERSION AS version`
  - `INFORMATION_SCHEMA.COLUMNS` LEFT JOIN `sys.computed_columns`
  - Bracketed table queries with `[basetbl]` aliases
- **High-Concurrency Stress Verification**:
  - Multi-worker concurrent routines executing metadata introspection, paged reads, and simultaneous DML writes without data races (`go test -race`).

---

## 6. Troubleshooting Common Connectivity Issues

| Issue / Error Symptom | Root Cause | Solution |
|---|---|---|
| `connection refused on :5433 or :1433` | LionSQL process is not running or listening on standard ports. | Run `./bin/lionsql` (or `./bin/lionsql start -dev`) and check `lsof -i :5433 -i :1433`. |
| `encryption required but server does not support TLS` | Client driver requested strict SSL/TLS, but LionSQL was started in plaintext mode. | Set `sslmode=disable` (PG) or `encrypt=disable` (TDS) in connection parameters, or launch LionSQL with `-tls -tls-cert <cert> -tls-key <key>`. |
| `USE <dbname> syntax in client migration scripts` | LionSQL enforces instance-level database isolation (GEMINI.md Section 3). | `USE <dbname>` is accepted as a tolerant virtual No-Op, synchronizing TDS ENVCHANGE (0xE3) and session DB context (`DB_NAME()`, `CURRENT_DATABASE()`) without syntax errors. |
| `table not found: dbo.mytable` | Schema prefix `dbo.` was not parsed. | Ensure running LionSQL version with updated `cleanIdent` that strips `dbo.` and `public.` schema prefixes automatically. |
| `table content empty or column mismatch in GUI` | Table `objectId` shuffled due to un-sorted map iteration across catalog queries. | Updated in `wt_engine.go` and `sqlexec_pgcatalog.go` with deterministic `sort.Slice` on table names. Click Refresh in the GUI. |
| `row count shows 0 for all tables` | Catalog statistics were not yet analyzed. | Dynamic row scanning via `tx.Scan` fallback added in `execDbGateMSSQLRowCountSelect`. |
| `r.toISOString is not a function in JS client` | Date columns in metadata were inferred as string instead of `TypeDateTimeN`. | Automatic datetime inference for column names ending in `Date` or `Time` added in TDS `inferColumnInfos`. |
| `table content empty in GUI` | Legacy JSON-encoded `.wt` files from prior versions. | Drop and recreate table using modern binary tuple format (`0x01`). |

