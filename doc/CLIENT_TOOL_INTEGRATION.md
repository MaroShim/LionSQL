# SQLion Client & GUI Tool Integration Guide (`doc/CLIENT_TOOL_INTEGRATION.md`)

This document provides a comprehensive integration and connectivity guide for GUI management tools, command-line interfaces (CLIs), and official language drivers communicating with SQLion via PostgreSQL Wire Protocol v3 (`:5433`) and Microsoft SQL Server TDS Protocol (`:1433`).

---

## 1. Multi-Protocol Overview & Default Ports

SQLion acts as a zero-external-dependency database engine that concurrently serves both PostgreSQL and Microsoft SQL Server clients out of a single static executable (`bin/server`).

| Protocol | Default Port | Default Superuser | Password | Database Name | Standard Client Ecosystem |
|---|---|---|---|---|---|
| **PostgreSQL v3** | `5433` (TCP) | `postgres` (or `admin`) | Any / Empty (Dev) | `sqlion` (or `default`) | Beekeeper Studio, DBeaver, pgAdmin, psql, lib/pq, pgx |
| **Microsoft SQL Server (TDS 7.x/8.0)** | `1433` (TCP) | `sa` (or `admin`) | Any / Empty (Dev) | `sqlion` (or `master`) | Beekeeper Studio, DBeaver, Azure Data Studio, SSMS, go-mssqldb, pymssql |
| **HTTP REST & Studio** | `8090` (TCP) | `admin@sqlion.local` | `1234567890` | `sqlion` | Embedded Web Studio (`/_/`), curl, OpenAPI, fetch |

> Note: SQLion enforces **Instance-Based Database Isolation** (GEMINI.md Section 3). Each server process hosts exactly one isolated database catalog per data directory (`--data-dir`).

---

## 2. GUI Client Configuration

### 2.1. Beekeeper Studio

Beekeeper Studio can connect to SQLion in either **PostgreSQL** or **SQL Server** mode.

#### Option A: PostgreSQL Connection Mode (Recommended for General GUI Browsing)
1. Select **Postgres** from the New Connection menu.
2. Connection Settings:
   - **Host**: `127.0.0.1` (or `localhost`)
   - **Port**: `5433`
   - **User**: `postgres`
   - **Password**: Leave empty or enter any string.
   - **Default Database**: `sqlion`
   - **SSL**: `Disable` (unless SQLion was started with `-tls` and `-require-ssl`).
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
   - **Database**: `sqlion` (or `master`)
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
   - **Database**: `sqlion`
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
   - **Database**: `sqlion`
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
   - **Database**: `sqlion`
   - **Authentication Type**: `SQL Login`
   - **User Name**: `sa`
   - **Password**: (any)
   - **Trust server certificate**: `True`
   - **Encrypt**: `Optional` or `False`

---

## 3. Command-Line Client (CLI) Connectivity

### 3.1. PostgreSQL CLI (`psql`)

Connect non-interactively or interactively via standard PostgreSQL CLI:

```bash
# Interactive terminal session
psql -h 127.0.0.1 -p 5433 -U postgres -d sqlion

# One-shot query execution
psql -h 127.0.0.1 -p 5433 -U postgres -d sqlion -c "SELECT * FROM customers;"
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
sqlcmd -S 127.0.0.1,1433 -U sa -P 12345678 -d sqlion

# Command execution
sqlcmd -S 127.0.0.1,1433 -U sa -d sqlion -Q "SELECT @@VERSION; SELECT * FROM customers;"
```

---

## 4. Official Programming Language Drivers

### 4.1. Go (`pgx/v5` - Modern PostgreSQL Driver)

SQLion is fully validated against `github.com/jackc/pgx/v5` with Extended Query Protocol, binary format encoding, and pipelining:

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
    conn, err := pgx.Connect(ctx, "postgres://postgres:secret@127.0.0.1:5433/sqlion?sslmode=disable")
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

SQLion supports `PacketTransactionManager` (0x0E), explicit transaction descriptors, `sp_prepare` / `sp_execute`, and TDS session settings:

```go
package main

import (
    "database/sql"
    "fmt"
    "log"

    _ "github.com/microsoft/go-mssqldb"
)

func main() {
    connStr := "sqlserver://sa:secret@127.0.0.1:1433?database=sqlion&encrypt=disable"
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
    dbname="sqlion",
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
    database="sqlion"
)
cursor = conn.cursor(as_dict=True)
cursor.execute("SELECT customer_id, name, balance FROM customers;")
for row in cursor:
    print(f"Customer: {row['name']}, Balance: {row['balance']}")
conn.close()
```

---

## 5. Architectural Machinery: Virtual Catalog Interceptors

GUI clients rely heavily on deep metadata introspection to display left-side object trees, column types, default values, and foreign keys. Rather than building heavy in-memory tables for hundreds of internal catalog tables, SQLion implements **Lightweight Virtual Catalog Interceptors** (`pkg/api/sqlexec_pgcatalog.go` and `sqlexec_dbcc.go`).

### 5.1. PostgreSQL Catalog Views
When clients query standard catalog tables, SQLion intercepts the queries before physical execution:
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
  SQLion intercepts this pattern, inspects table schemas from `engine.GetTable()`, and formats data types directly into canonical MSSQL representations (`int`, `varchar(255)`, `decimal(p,s)`, `bit`, `datetime`).
- **Virtual System Table Stubs**:
  `sys.tables`, `sys.columns`, `sys.indexes`, `sys.databases`, `sys.computed_columns`, `sys.triggers`, and `sys.foreign_keys` respond dynamically without maintaining physical disk storage.

### 5.3. T-SQL Pagination & Identifier Normalization
GUI clients often format table data pagination queries using T-SQL-specific syntax:
- **`OFFSET <O> ROWS FETCH NEXT <N> ROWS ONLY`**: Automatically rewritten by `normalizeMSSQLPagination` into canonical `LIMIT <N> OFFSET <O>`.
- **`SELECT TOP <N>`**: Normalized to `SELECT ... LIMIT <N>`.
- **Bracketed Identifiers (`[dbo].[table]`, `[column]`)**: Cleaned via `cleanIdent` and `ColumnIndexMap.Index`, ensuring zero-overhead field resolution.
- **Double-Quoted Identifiers (`"column_name"`)**: Fully parsed as `TokenIdent` by the native Pratt parser lexer (`pkg/proc/lexer.go`).

---

## 6. Troubleshooting Common Connectivity Issues

| Issue / Error Symptom | Root Cause | Solution |
|---|---|---|
| `connection refused on :5433 or :1433` | SQLion process is not running or listening on standard ports. | Run `./bin/server -engine wiredtiger -dir ./data` and check `lsof -i :5433 -i :1433`. |
| `encryption required but server does not support TLS` | Client driver requested strict SSL/TLS, but SQLion was started in plaintext mode. | Set `sslmode=disable` (PG) or `encrypt=disable` (TDS) in connection parameters, or launch SQLion with `-tls -tls-cert <cert> -tls-key <key>`. |
| `USE <dbname> statement failed` | SQLion intentionally excludes `USE <dbname>` syntax (GEMINI.md Section 3). | Connect directly to the target database in the initial connection string; deploy separate process instances for separate databases. |
| `table not found: dbo.mytable` | Schema prefix `dbo.` was not parsed. | Ensure running SQLion version with updated `cleanIdent` that strips `dbo.` and `public.` schema prefixes automatically. |
| `table content empty in GUI` | Legacy JSON-encoded `.wt` files from prior versions. | Drop and recreate table using modern binary tuple format (`0x01`). |
