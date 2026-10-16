# LionSQL Migration Guide: Moving from Other Databases

This guide explains how to migrate schemas, data, and stored procedures to LionSQL from PostgreSQL, Microsoft SQL Server, SQLite, and MySQL.

---

## 1. Migrating from PostgreSQL

Because LionSQL natively speaks PostgreSQL Wire Protocol v3 and supports `COPY` streaming, migrating from PostgreSQL is fast and seamless.

### 1.1. Data Type Mapping

| PostgreSQL Type | LionSQL Type | Notes |
|---|---|---|
| `INT2`, `INT4`, `INT8`, `SERIAL`, `BIGSERIAL` | `INT` | 64-bit integer |
| `VARCHAR`, `TEXT`, `CHAR` | `VARCHAR` | UTF-8 variable-length string |
| `FLOAT4`, `FLOAT8`, `NUMERIC`, `DECIMAL` | `FLOAT` | 64-bit floating point |
| `BOOLEAN`, `BOOL` | `BOOL` | Boolean flag |
| `TIMESTAMP`, `TIMESTAMPTZ`, `DATE` | `DATETIME` | RFC3339 UTC timestamp |

### 1.2. Exporting Schema & Streaming Data

1. **Export DDL Schema**:
   ```bash
   pg_dump -h pg.host -U postgres -d mydb --schema-only --no-owner --no-privileges > schema.sql
   ```
2. **Apply DDL to LionSQL**:
   ```bash
   psql -h 127.0.0.1 -p 5433 -U admin -f schema.sql
   ```
3. **Stream Table Data Directly**:
   Stream table data from PostgreSQL directly into LionSQL over a Unix pipe without intermediate disk storage:
   ```bash
   psql -h pg.host -U postgres -d mydb -c "\copy accounts to stdout with (format csv, header)" | \
   psql -h 127.0.0.1 -p 5433 -U admin -c "\copy accounts from stdin with (format csv, header)"
   ```

---

## 2. Migrating from Microsoft SQL Server (MSSQL)

LionSQL natively understands Microsoft T-SQL syntax, making stored procedure migration straightforward.

### 2.1. Stored Procedures & Control Flow
- T-SQL procedures using `DECLARE @var TYPE`, `SELECT ... INTO`, `IF...ELSE`, `WHILE`, `TRY...CATCH`, `RAISERROR`, `PRINT`, and cursors execute directly without rewriting.
- Stored procedures can be registered via `POST /api/procs`, the Web Studio, or via `sqlcmd`:
  ```bash
  sqlcmd -S 127.0.0.1,1433 -U admin -i procedures.sql
  ```

### 2.2. Bulk Data Loading via BCP / CSV
1. **Export data from MSSQL using BCP**:
   ```bash
   bcp "SELECT * FROM accounts" queryout accounts.csv -c -t, -S mssql.host -U sa -P pass
   ```
2. **Import into LionSQL**:
   ```bash
   psql -h 127.0.0.1 -p 5433 -c "\copy accounts from 'accounts.csv' with (format csv)"
   ```

---

## 3. Migrating from SQLite

SQLite databases can be converted using SQL dumps or CSV exports.

### 3.1. Syntax Adjustments
1. **Auto-Incrementing Keys**:
   In SQLite: `id INTEGER PRIMARY KEY AUTOINCREMENT`.
   In LionSQL: `id INT PRIMARY KEY` (LionSQL auto-generates INT IDs if omitted during insert).
2. **Type Affinities**:
   SQLite allows storing arbitrary types in any column. Ensure columns in LionSQL match their intended schema (`INT`, `VARCHAR`, `FLOAT`, `BOOL`, `DATETIME`).

### 3.2. Migration Steps
1. Export SQLite table to CSV:
   ```bash
   sqlite3 mydatabase.db -header -csv "SELECT * FROM users;" > users.csv
   ```
2. Create matching table in LionSQL via Web Studio or `psql`:
   ```sql
   CREATE TABLE users (
       id INT PRIMARY KEY,
       name VARCHAR NOT NULL,
       score FLOAT,
       created_at DATETIME NOT NULL
   );
   ```
3. Import CSV:
   ```bash
   psql -h 127.0.0.1 -p 5433 -c "\copy users from 'users.csv' with (format csv, header)"
   ```

---

## 4. Migrating from MySQL / MariaDB

1. **Backticks to Quotes/Brackets**:
   Replace MySQL backticks (`` `col` ``) with standard double quotes (`"col"`) or T-SQL square brackets (`[col]`).
2. **Data Types**:
   - `TINYINT(1)` ➔ `BOOL`
   - `DATETIME`, `TIMESTAMP` ➔ `DATETIME`
   - `VARCHAR(...)`, `LONGTEXT` ➔ `VARCHAR`
3. **Stored Procedures**:
   Convert MySQL procedural syntax (`DELIMITER //`, `DECLARE CONTINUE HANDLER`) to LionSQL's standard T-SQL dialect (`BEGIN TRY...END TRY`, `@@FETCH_STATUS`).
