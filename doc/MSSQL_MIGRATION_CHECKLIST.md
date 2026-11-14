# Microsoft SQL Server ➔ LionSQL Migration Checklist (`doc/MSSQL_MIGRATION_CHECKLIST.md`)

> - **Created**: 2026-11-12
> - **Last Updated**: 2026-11-12
> - **Target Release**: LionSQL v1.0-GA
> - **한국어 문서**: [doc/MSSQL_MIGRATION_CHECKLIST.ko.md](MSSQL_MIGRATION_CHECKLIST.ko.md)

---

This document provides a practical checklist and transition guide for migrating schemas, stored procedures, views, indexes, and business data from Microsoft SQL Server (2016, 2017, 2019, 2022) to LionSQL.

---

## 1. Data Type Compatibility & Mapping Guide

LionSQL natively supports core Microsoft SQL Server data types.

| SQL Server Data Type | LionSQL Supported | Internal Storage Type | Migration Advice & Recommendation |
|---|:---:|---|---|
| `INT` / `INTEGER` | Yes | `int4` (4-byte signed int) | 100% compatible |
| `BIGINT` | Yes | `int8` (8-byte signed int) | 100% compatible |
| `SMALLINT` | Yes | `int2` (2-byte signed int) | 100% compatible |
| `TINYINT` | Yes | `uint8` (1-byte unsigned) | 100% compatible |
| `BIT` | Yes | `bool` (1-bit boolean) | `0`/`1` integer literals and boolean casting supported |
| `DECIMAL(p, s)` / `NUMERIC` | Yes | `numeric` (Fixed precision) | 100% exact math compatibility |
| `FLOAT` / `REAL` | Yes | `float8` / `float4` | IEEE 754 floating point standard |
| `VARCHAR(n)` / `CHAR(n)` | Yes | `varchar` (UTF-8 string) | LionSQL stores character data in UTF-8 |
| `NVARCHAR(n)` / `NCHAR(n)` | Yes | `varchar` (UTF-8 unicode) | Full international character support |
| `DATETIME` / `DATETIME2` | Yes | `timestamp` (microsecond resolution) | `GETDATE()`, `CURRENT_TIMESTAMP` compatible |
| `DATE` / `TIME` | Yes | `date` / `time` | 100% compatible |
| `VARBINARY(n)` / `BINARY` | Yes | `varbinary` (byte slice) | Supported with `COMPRESS()`, `DECOMPRESS()` |
| `UNIQUEIDENTIFIER` | Yes | `uuid` / `varchar(36)` | Compatible with `NEWID()` |
| `ROWVERSION` / `TIMESTAMP` | Yes | `varbinary(8)` | Monotonically increasing `@@DBTS` supported |
| `XML` | No | `JSON` or `VARCHAR` | **Recommend JSON** (`ISJSON`, `JSON_VALUE`) |
| `HIERARCHYID` / `GEOGRAPHY` | No | `VARCHAR` or normalized | Separate into numeric latitude/longitude columns |

---

## 2. DDL Schema Migration Checklist

### 2.1. Table & Column Constraints
- [x] **`IDENTITY(1, 1)` Auto-Increment Columns**:
  - Full support for T-SQL `IDENTITY`. Retrieve generated IDs via `@@IDENTITY`.
- [x] **`PRIMARY KEY` and `UNIQUE` Indexes**:
  - Automatically registered as storage engine indexes upon table creation.
- [x] **`FOREIGN KEY`**:
  - Referential integrity validation and `ON DELETE CASCADE` supported.
- [x] **`DEFAULT` Constraints**:
  - Literal and scalar function defaults (`GETDATE()`, `0`, `''`) supported.
- [x] **Instance Isolation & `USE <dbname>`**:
  - LionSQL manages one isolated catalog per data directory.
  - Script statements such as `USE master` or `USE [dbname]` are treated as tolerant No-Ops, requiring no script modifications.

---

## 3. Stored Procedure & Logic Conversion Checklist

### 3.1. Fully Compatible Syntax (Zero Modification Required)
1. **Variable Declaration**: `DECLARE @name VARCHAR(50) = 'Lion'; SET @id = 1;`
2. **Conditional Logic**: `IF ... ELSE ... BEGIN ... END`
3. **Loops**: `WHILE ... BEGIN ... BREAK; CONTINUE; END`
4. **Exception Handling & Error Raising**: `BEGIN TRY ... END TRY BEGIN CATCH ... END CATCH`, `THROW 50001, 'Error message', 1;`, standalone `THROW;` (re-throw), and `RAISERROR('Error message', 16, 1);`
5. **Transactions**: `BEGIN TRAN`, `COMMIT TRAN`, `ROLLBACK TRAN`, `SAVE TRAN`, `@@TRANCOUNT`
6. **Session Temporary Tables & Table Variables**:
   - `CREATE TABLE #temp (...)`, `SELECT * INTO #temp FROM ...`, and `DECLARE @tbl TABLE (...)` work out of the box without modification.
   - Even when running in WiredTiger disk mode, `#temp` tables, `@tbl` table variables, and CTEs (`WITH cte AS (...)`) are all accelerated in-memory via the pure-Go OCC engine without disk I/O overhead—so you can freely use whichever construct best fits your query structure (CTEs for single-query readability; `#temp` or `@tbl` when reusing intermediate results across multiple procedure steps).
7. **Scalar Functions**: `LEN`, `SUBSTRING`, `CHARINDEX`, `REPLACE`, `CONCAT`, `ISNULL`, `COALESCE`, `GETDATE()`, `DATEDIFF`, `DATEADD`, `COMPRESS`, `DECOMPRESS`

### 3.2. Recommended Transformation Patterns
1. **Cursors ➔ Set-Based Operations**:
   - While LionSQL supports `FORWARD_ONLY` / `FAST_FORWARD` cursors without syntax changes, set-based queries (`UPDATE ... FROM`, `INSERT ... SELECT`, window functions) are recommended over row-by-row cursor loops for maximum OLTP throughput.
2. **Legacy XML (`FOR XML`, `XQuery`) ➔ Modern JSON (`FOR JSON`, `JSON_VALUE`, `OPENJSON`)**:
   - Replace legacy XML DML with native JSON functions (`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`, `OPENJSON`, `FOR JSON PATH/AUTO`).

---

## 4. Bulk Data Migration Procedure

### 4.1. Export Data from Microsoft SQL Server (BCP)
```bash
bcp "SalesDB.dbo.Customers" out "customers.csv" -c -t"," -r"\n" -S mssql-host -U sa -P password
```

### 4.2. High-Speed Bulk Import into LionSQL (`psql \copy`)
Stream data into LionSQL via the PostgreSQL Wire Protocol (`:5433`):
```bash
psql -h 127.0.0.1 -p 5433 -U postgres -d postgres -c "\copy customers FROM 'customers.csv' WITH (FORMAT csv, HEADER false);"
```
*Note: LionSQL streams rows through VDBE transactions at 50,000 ~ 100,000+ rows/second.*

---

## 5. Application Connection Strings

| Framework / Client | Legacy MSSQL Connection String | LionSQL Recommended Connection String |
|---|---|---|
| **Go (`go-mssqldb`)** | `sqlserver://sa:pwd@host:1433?database=SalesDB` | `sqlserver://sa:@127.0.0.1:1433?database=master&encrypt=disable` |
| **Go (`pgx/v5`) [Recommended]** | - | `postgres://postgres:@127.0.0.1:5433/postgres?sslmode=disable` |
| **C# / .NET (ADO.NET)**| `Server=host;Database=SalesDB;User Id=sa;Password=pwd;` | `Server=127.0.0.1,1433;Database=master;User Id=sa;Password=;TrustServerCertificate=True;Encrypt=False;` |
| **Python (`psycopg2`)** | `DRIVER={ODBC};SERVER=host;DATABASE=SalesDB;...` | `host='127.0.0.1' port=5433 dbname='postgres' user='postgres' sslmode='disable'` |
| **Node.js (`node-postgres`)** | `mssql://sa:pwd@host:1433/SalesDB` | `postgresql://postgres:@127.0.0.1:5433/postgres` |

---

## 6. Post-Migration Verification

1. **Table & Row Count Check**:
   ```sql
   SELECT name, type_desc FROM sys.objects WHERE type = 'U';
   ```
2. **Stored Procedure Test**:
   ```sql
   EXEC sp_TransferFunds @from=1, @to=2, @amount=500;
   ```
3. **CBO Statistics Refresh**:
   Execute `ANALYZE TABLE <tablename>;` across user tables to populate equi-depth histograms and optimal index scan plans.
