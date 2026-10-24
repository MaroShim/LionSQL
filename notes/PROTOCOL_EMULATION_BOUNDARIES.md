# LionSQL Multi-Protocol Emulation Boundaries & Scope (`notes/PROTOCOL_EMULATION_BOUNDARIES.md`)

## 1. Overview & Core Architectural Identity

LionSQL is architected as an **autonomous, high-concurrency embedded OLTP database engine** featuring a single unified VDBE bytecode virtual machine executing Microsoft T-SQL stored procedures and ANSI SQL queries.

To maximize ecosystem reach, LionSQL exposes multiple database network listeners concurrently from its single static executable:
- **TDS 7.x/8.0 Protocol** (Default TCP `:1433`): Native Microsoft SQL Server tools, SSMS, Azure Data Studio, `go-mssqldb`.
- **PostgreSQL Wire Protocol v3** (Default TCP `:5433`): `psql`, `pgx/v5`, `lib/pq`, DBeaver, Beekeeper Studio, Navicat, DataGrip, GORM, Prisma.
- **MySQL Client/Server Protocol** (Planned TCP `:3306`): `mysql` CLI, Navicat, MySQL Workbench, `go-sql-driver/mysql`, PHP/Python MySQL drivers.
- **HTTP / REST & Web Studio** (Default TCP `:8090`): Web dashboard, health probes, automated REST API.

This document establishes the **permanent scope boundaries and architectural stop criteria** for PostgreSQL and MySQL protocol emulation to prevent engine bloat, semantic collisions, and infinite dialect rabbit holes.

---

## 2. Core Architectural Invariant: Protocol Gateway vs Engine Identity

### 2.1. The "Ecosystem Gateway" Principle (The 80/20 Rule)
Protocol emulation in LionSQL serves a single strategic objective:
> **"Allow developers to connect existing GUI tools, ORMs, and official client SDKs without installing proprietary drivers or deploying external proxies (PgBouncer, ProxySQL)."**

It is an explicit non-goal to turn LionSQL into a 100% clone of PostgreSQL or MySQL. Emulating every internal catalog table, procedural language, and dialect-specific quirk is an anti-pattern proven to produce fragile, unmaintainable codebases.

### 2.2. Precedents in Modern Systems
- **ClickHouse**: Listens on MySQL (:9004) and PostgreSQL (:9005) wire protocols to allow tool connectivity, but compiles all queries through ClickHouse's own vectorized engine with zero support for `PL/pgSQL` or MySQL stored routines.
- **CockroachDB**: Implements the PostgreSQL v3 wire protocol and virtualizes key `pg_catalog` tables for driver handshakes, but strictly defines its own transaction engine and rejects PostgreSQL internal procedural languages.
- **DuckDB**: Provides a PostgreSQL wire gateway for BI tool ingestion while executing queries exclusively on its internal vectorized columnar kernel.

---

## 3. Four-Layer Protocol Boundary Model

To maintain engineering discipline, every wire protocol implementation in LionSQL must strictly adhere to the Four-Layer Model:

```
+--------------------------------------------------------------------------+
| Layer 1: Wire Protocol & Framing (100% Fidelity)                         |
| - Handshake, Auth, Packet Encoding, Simple & Extended Query Protocol    |
+--------------------------------------------------------------------------+
                                    |
                                    v
+--------------------------------------------------------------------------+
| Layer 2: Virtual Catalog & Driver Handshake (90% Fidelity)               |
| - Handshake queries, metadata introspection, version probes              |
+--------------------------------------------------------------------------+
                                    |
                                    v
+--------------------------------------------------------------------------+
| Layer 3: Common DML & Standard Relational SQL (80% Fidelity)             |
| - SELECT, INSERT, UPDATE, DELETE, JOIN, WHERE, GROUP BY, ORDER BY        |
+--------------------------------------------------------------------------+
                                    |
                                    v
+--------------------------------------------------------------------------+
| Layer 4: Dialect-Specific Quirks & Procedures (10~20% Prudent Alias)     |
| - High-frequency scalar function aliases only                            |
| - Procedural languages: 100% T-SQL VM Invariance (PL/pgSQL & Routine Out)|
+--------------------------------------------------------------------------+
```

### Layer 1: Wire Protocol & Framing (100% Target)
- **Goal**: Client drivers connect, authenticate, and exchange data frames without socket disconnects or protocol errors.
- **Requirements**:
  - Connection lifecycle (TCP handshake, SSL negotiation pass/reject, authentication exchange).
  - ParameterStatus and ReadyForQuery packets.
  - Text and Binary tuple encoding (Big-Endian network byte order).
  - Extended Query Protocol (`Parse`, `Bind`, `Execute`, `Describe`, `Sync`, `Flush`, `Close`).

### Layer 2: Virtual Catalog & Driver Handshake (90% Target)
- **Goal**: Prevent GUI management tools and ORMs from failing during initial connection handshakes and schema tree inspection.
- **Requirements**:
  - Intercept and synthesize virtual responses for metadata queries:
    - PostgreSQL: `pg_catalog.pg_class`, `pg_attribute`, `pg_type`, `pg_namespace`, `pg_database`, `pg_roles`, `pg_settings`, `information_schema.*`.
    - MySQL: `SELECT @@version_comment`, `SHOW VARIABLES`, `SHOW TABLES`, `SHOW COLLATION`, `SELECT DATABASE()`.
  - Zero disk storage overhead: synthesize catalog responses dynamically from the active memory/B+Tree catalog.

### Layer 3: Common DML & Standard Relational SQL (80% Target)
- **Goal**: Seamlessly execute everyday application CRUD queries.
- **Requirements**:
  - Standard ANSI SQL queries are routed directly to the Volcano CBO planner or native VDBE compiler.
  - Transactions: `BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT` map directly to LionSQL's ACID transaction manager.

### Layer 4: Dialect-Specific Features & Procedural Logic (10~20% Selective)
- **Goal**: Prevent dialect fragmentation while offering maximum ergonomic convenience.
- **Invariants**:
  - **Single Procedural Engine**: LionSQL's procedural engine is permanently unified on **Microsoft T-SQL VDBE bytecode execution**. PL/pgSQL (`DO $$ ... $$`) and MySQL stored routines (`DELIMITER //`) are explicitly **Spec-Out**.
  - **Function Aliases**: Support only the 20~30 most common scalar function synonyms by aliasing them directly to existing built-ins in `pkg/proc/builtins.go`.

---

## 4. PostgreSQL Protocol Boundary Specification (`pkg/wire/`, Port :5433)

### 4.1. Current State (Production-Ready)
- Layer 1 (Wire Protocol): 100% complete. Extended query protocol, binary formatting, and session pool integration verified across `pgx/v5`, `lib/pq`, and `psql`.
- Layer 2 (Catalog Handshake): 30+ virtual system tables (`pg_class`, `pg_attribute`, `pg_namespace`, `pg_type`, `pg_index`, `pg_constraint`, `pg_proc`, `pg_stat_user_tables`) fully virtualized.
- Verified Client Tools: DBeaver, Beekeeper Studio, Navicat, DataGrip, DbGate, HeidiSQL.

### 4.2. Implementation Stopping Criteria
To preserve architectural purity, PostgreSQL support shall halt after implementing the following high-value ergonomics:

1. **Common Function Aliases**:
   - `REPEAT(str, n)` -> Alias to `REPLICATE`
   - `SUBSTR(str, start, len)` -> Alias to `SUBSTRING`
   - `STRPOS(str, sub)` -> Alias to `CHARINDEX(sub, str)`
   - `MD5(str)` -> Alias to `HASHBYTES('MD5', str)`
   - `OCTET_LENGTH(str)` -> Alias to byte length calculation
2. **Double-Colon Type Casting Syntax**:
   - Normalize `col::text`, `col::int4`, `col::bigint` in the expression parser to standard `CAST(col AS type)`.
3. **Explicitly Spec-Out / Deferred (Do Not Implement)**:
   - PL/pgSQL procedural grammar and language extensions.
   - Postgres JSONB indexing operators (`->`, `->>`, `#>`, `?|`, `?&`).
   - Complex geometric, network (`cidr`, `inet`), and multidimensional array slicing operators (`@>`, `<@`, `&&`).
   - Large Objects API (`lo_create`, `lo_open`).

---

## 5. MySQL Protocol Boundary Specification (`pkg/wire/mysql/`, Port :3306)

When implementing the MySQL wire listener, the following strict bounds must be maintained:

### 5.1. Scope of Implementation (In-Scope)
1. **Network Listener & Handshake**:
   - Listen on TCP `:3306`.
   - Implement MySQL Protocol 4.1 Initial Handshake packet (Protocol version 10, server capabilities, auth plugin `mysql_native_password`).
   - Handshake Response packet parser with SHA1 password verification.
   - Standard packet header handling (3-byte payload length + 1-byte sequence ID).
2. **Command Dispatcher**:
   - `COM_QUIT` (0x01): Clean connection termination.
   - `COM_INIT_DB` (0x02): No-op acceptance (isolated instance model).
   - `COM_QUERY` (0x03): Text-based SQL statement execution.
   - `COM_PING` (0x0E): Keep-alive probe returning OK packet.
   - `COM_STMT_PREPARE` (0x16), `COM_STMT_EXECUTE` (0x17), `COM_STMT_CLOSE` (0x19): Prepared statement protocol.
3. **Handshake Interception & System Variables**:
   - `SELECT @@version_comment, @@version`
   - `SELECT @@collation_server, @@character_set_server`
   - `SHOW VARIABLES LIKE ...`
   - `SHOW STATUS LIKE ...`
   - `SHOW TABLES` and `SHOW COLUMNS FROM ...`

### 5.2. Explicitly Spec-Out / Deferred (Do Not Implement)
1. **MySQL Stored Procedures & Functions**:
   - `CREATE PROCEDURE ... BEGIN ... END` using `DELIMITER` syntax is Spec-Out.
2. **Replication & Binary Logging**:
   - `COM_BINLOG_DUMP`, `COM_REGISTER_SLAVE`, and GTID event streaming are Spec-Out (LionSQL uses native WAL replication).
3. **Administrative & Maintenance Commands**:
   - `FLUSH PRIVILEGES`, `OPTIMIZE TABLE`, `ANALYZE TABLE`, `RESET MASTER` are Spec-Out.
4. **Non-Standard Implicit Coercions**:
   - Silent truncation or zero-date quirks (`0000-00-00`) are Spec-Out.

---

## 6. Summary: Architectural Checklist for Future Protocol Extensions

Before adding any new wire protocol feature or SQL dialect syntax, evaluate against this checklist:

1. **Does this prevent standard client drivers or GUI tools from connecting?**
   - If **Yes** -> Implement in Layer 1 (Framing) or Layer 2 (Virtual Catalog Handshake).
2. **Is this a common CRUD expression that can be normalized to ANSI SQL?**
   - If **Yes** -> Implement in Layer 3 or add a function alias in `pkg/proc/builtins.go`.
3. **Does this introduce a competing procedural language or conflict with T-SQL semantics?**
   - If **Yes** -> **Reject and Spec-Out**. Preserve T-SQL VDBE single-kernel invariance.
