# LionSQL Compatibility Specification & Matrix (`COMPATIBILITY.md`)

> - **Created**: 2026-11-06
> - **Last Updated**: 2026-11-06
> [한국어 문서 (Korean Version)](COMPATIBILITY.ko.md)

LionSQL seamlessly converges the Microsoft SQL Server T-SQL ecosystem with the PostgreSQL client/driver ecosystem within a single standalone static executable.  
This document serves as an immediate, dedicated technical reference for developers, DBAs, and software architects detailing **official target baseline versions, supported wire protocols, system introspection handshake values, and empirical verification matrices**.

---

## 1. Target Compatibility Baselines

| Ecosystem | Official Target Baseline | Supported Wire Protocols | Notes |
| :--- | :--- | :--- | :--- |
| **Microsoft SQL Server** | **SQL Server 2019 (Version 15.0)** | **TDS 7.4** (Default)<br>**TDS 8.0** (Direct TLS) | Full client compatibility across SQL Server 2012 - 2022 |
| **PostgreSQL** | **PostgreSQL 14 - 16 Parity** | **PostgreSQL Wire v3** | Strict binary protocol pass including `jackc/pgx/v5` |
| **REST & BaaS API** | **OpenAPI / JSON Standard** | **HTTP/1.1 REST & SSE** | Integrated single-binary SPA web server (`--app`) |

---

## 2. System Version & Handshake Introspection Values

Real-world response values returned when GUI tools (SSMS, Azure Data Studio, DBeaver, Beekeeper Studio) perform introspection:

```sql
-- 1. SQL Server Version Query
SELECT @@VERSION;
-- Response: Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) / LionSQL Embedded Engine

-- 2. SQL Server Properties
SELECT 
    SERVERPROPERTY('ProductVersion') AS ProductVersion,     -- '15.0.2000.5'
    SERVERPROPERTY('ProductMajorVersion') AS MajorVersion,  -- '15'
    SERVERPROPERTY('EngineEdition') AS EngineEdition,       -- 3 (Enterprise / Developer)
    SERVERPROPERTY('Collation') AS Collation;               -- 'SQL_Latin1_General_CP1_CI_AS'

-- 3. PostgreSQL Version Query (Port :5433)
SELECT version();
-- Response: PostgreSQL 15.0 (LionSQL Dual-Engine OLTP)
```

---

## 3. Core SQL & T-SQL Compatibility Matrix

### 3.1. Modern T-SQL Functions (SQL Server 2016 - 2019 Standard)
* **Modern String Functions**: `STRING_SPLIT()`, `STRING_AGG() [WITHIN GROUP]`, `TRIM()`, `CONCAT()`, `CONCAT_WS()`, `TRANSLATE()`, `FORMAT()`.
* **Native JSON Functions**: `ISJSON()`, `JSON_VALUE()`, `JSON_QUERY()`, `JSON_MODIFY()`.
* **Date & Time Functions**: `DATEADD()`, `DATEDIFF()`, `DATEPART()`, `EOMONTH()`, `GETDATE()`, `SYSDATETIME()`.
* **Conversion & Safe Evaluation**: `CONVERT()`, `TRY_CONVERT()`, `CAST()`, `TRY_CAST()`, `PARSE()`.
* **Window & Analytic Functions**: `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `NTILE()`, `OVER (PARTITION BY ... ORDER BY ...)`.

### 3.2. Procedural Extensions & Control Flow (VDBE Bytecode Engine)
* **Control Flow Statements**: `IF...ELSE`, `WHILE`, `BEGIN...END`, `RETURN`, `GOTO`.
* **Exception Blocks**: `BEGIN TRY ... END TRY BEGIN CATCH ... END CATCH` (`ERROR_NUMBER()`, `ERROR_MESSAGE()`, `ERROR_SEVERITY()`).
* **Variables & Cursors**: Local variables (`DECLARE @var`), table variables (`DECLARE @tbl TABLE`), inline cursors (`DECLARE CURSOR FOR ... OPEN ... FETCH ... CLOSE ... DEALLOCATE`).
* **Dynamic SQL Execution**: `sp_executesql` RPC calls (full ordinal positional and named parameter bindings).

---

## 4. Verified Client Tools & ORM Frameworks

### 4.1. Formally Verified GUI Clients
- **SQL Server Tools (Port :1433)**: SQL Server Management Studio (SSMS 19/20), Azure Data Studio, `sqlcmd`, DBeaver, Beekeeper Studio, DbGate.
- **PostgreSQL Tools (Port :5433)**: `psql`, pgAdmin 4, DBeaver, Metabase BI.

### 4.2. Formally Verified Drivers & ORMs
- **Go**: `database/sql`, `github.com/microsoft/go-mssqldb` (v1.11+), `jackc/pgx/v5`, `lib/pq`, GORM, Ent, SQLBoiler.
- **.NET (C#)**: Entity Framework Core (EF Core 8/9), Dapper, `Microsoft.Data.SqlClient`.
- **Node.js / TypeScript**: Prisma ORM, TypeORM, Drizzle ORM, `mssql`, `pg`.
- **Rust**: Diesel ORM, SQLx.
- **Python**: SQLAlchemy, `psycopg2`, `pyodbc`.

---

## 5. Real-World Applications Verified (100% Pass)

1. **Metabase BI**: Dual-wire introspection, visual query builder, MBQL aggregations, and joins.
2. **Vaultwarden (Rust Diesel)**: 47 sequential migrations, composite primary keys, and Bitwarden REST API pass.
3. **Apache Answer (Go)**: 35 tables, batch identity generation, and Q&A platform pass.
4. **Gitea (Go XORM)**: Dual-wire protocol (TDS :1433 & PGWire :5433) across 116 tables.
5. **Woodpecker CI & Vikunja**: Recursive CTEs, composite OR index branching, and DML command tags.
6. **Grafana**: 785 migrations, bytea envelope encryption, and dual datasource dashboards.
7. **Casdoor**: 44 tables, composite primary keys, and OIDC/OAuth2/RBAC integration.
8. **IOTA SDK ERP**: `pgx/v5` binary protocols and 47 sequential accounting migrations.
9. **SQLite SQLLogicTest (SLT)**: Official 107 full suites (19,211 statements, **551,217 queries**) 100% flawless pass.

---

## 6. Related References

- Comprehensive CLI & Configuration Reference: **[`doc/REFERENCE_MANUAL.md`](doc/REFERENCE_MANUAL.md)**
- TDS Protocol Empirical Verification Report: **[`notes/TDS_COMPATIBILITY_REPORT.md`](notes/reports/TDS_COMPATIBILITY_REPORT.md)**
- PGWire Protocol Empirical Verification Report: **[`notes/PGWIRE_COMPATIBILITY_REPORT.md`](notes/reports/PGWIRE_COMPATIBILITY_REPORT.md)**
- Production Open-Source Integration Guides: **[`guide/README.md`](guide/README.md)**
