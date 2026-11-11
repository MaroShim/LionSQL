# LionSQL Reference Manual

> - **Created**: 2026-10-08
> - **Last Updated**: 2026-11-03


This document is the **Official Reference Manual** encompassing all CLI commands, configuration parameters, SQL syntax and grammar specifications, data types, system built-in functions, virtual catalog views, REST API endpoints, and wire protocol specifications for the LionSQL database engine.

For an introductory installation guide, quick start, and application tutorials, refer to the [User Manual (`USER_MANUAL.ko.md`)](USER_MANUAL.ko.md).

---

## Table of Contents

### Chapter 1. Command-Line Interface (CLI Reference)
* **1.1. Single Binary (`bin/lionsql`) Command Structure**
  * Foreground interactive launch: `lionsql [serve] [flags...]`
  * Background daemon management: `lionsql start [-dev]`, `stop`, `restart [-dev]`, `status`
  * Administrative and utility subcommands: `doctor`, `init-config`, `migrate`, `sql`, `backup`, `restore`, `version`
* **1.2. Global CLI Flag Specifications**
  * Network listeners: `-http`, `-pg`, `-tds`, `-tls`, `-tls-cert`, `-tls-key`, `-require-ssl`
  * Storage backend: `-dir`, `-engine` (`wiredtiger`, `memory`), `-ephemeral`, `-auto-seed`
  * Resource governor: `-max-memory` (`<size>` e.g. `2GB`, `4096MB`, default: 65% of physical RAM), `-work-mem` (default: `64MB`), `-max-workers`, `-max-idle-sessions`
  * Security and authentication: `-auth` (default: `true`), `-admin-email`, `-admin-pass`, `-jwt-secret`
  * Logging and tracing: `-dev`, `-slow-log`, `-slow-threshold-ms` (default: `100.0ms`), `-general-log`
  * High-availability replication: `-replica-of`, `-node-id`, `-peers`, `-self-url`, `-auto-failover`, `-heartbeat-timeout-ms`, `-cluster-token`
* **1.3. Benchmark and Load Verification Tools (`bin/bench`, `bin/tpcc-runtime`, `bin/tpcc-kernel`)**
  * TPC-C VDBE runtime harness (`bin/tpcc-runtime`): `-warehouses`, `-workers`, `-duration`, `-engine`, `-fsync` (`everysec`, `always`, `off`)
  * Storage kernel harness (`bin/tpcc-kernel`): `-warehouses`, `-workers`, `-duration`, `-engine`
  * OLTP microbenchmark (`bin/bench`): `-accounts`, `-workers`, `-ops`, `-engine`
* **1.4. Exit Codes & POSIX Signals**
  * Standard exit code definitions (0: Normal exit, 1: General error, 2: Configuration / usage error)
  * POSIX signal handling: `SIGINT`, `SIGTERM` (staged graceful shutdown within 1.5s), `SIGHUP`

---

### Chapter 2. Configuration File Reference
* **2.1. Complete `lionsql.json` Schema Specification**
* **2.2. Section-by-Section Configuration Keys**
  * `Server`: `engine`, `data_dir`, `ephemeral`, `auto_seed`, `pid_file`
  * `Network`: `http_addr`, `pg_addr`, `tds_addr`, `tls` (`enabled`, `cert_file`, `key_file`, `require_ssl`)
  * `Auth`: `enabled`, `admin_email` (default: `admin@lionsql.local`), `admin_pass`, `jwt_secret`
  * `Resources`: `max_memory` (default: 65% RAM), `work_mem` (default: `64MB`), `max_workers`, `max_idle_sessions`
  * `Logging`: `dev_mode`, `slow_query` (`enabled`, `threshold_ms`: `100.0`, `file`), `general_query` (`enabled`, `file`)
  * `Replication`: `enabled`, `role`, `node_id`, `primary_url`, `auto_failover`, `peers`, `self_url`, `heartbeat_timeout_ms`
* **2.3. Transaction Performance and Workload Profiles Summary by Option Combination**
  * Ultra-low-latency In-Memory, Standard Production OLTP, Zero-Loss Financial Core Ledger, High-Throughput Batch Profile

---

### Chapter 3. Data Types & Literals Reference
* **3.1. Numeric Data Types**
  * Integer types: `TINYINT` (8-bit), `SMALLINT` (16-bit), `INT` / `INTEGER` (32-bit), `BIGINT` (64-bit)
  * Floating-point: `FLOAT` / `REAL` (64-bit IEEE 754)
  * Fixed-point exact numeric: `DECIMAL(p, s)` / `NUMERIC(p, s)` (maximum precision 38)
* **3.2. Character & Text Data Types**
  * `VARCHAR(n)`: Variable-length character string (up to 8,000 chars)
  * `NVARCHAR(n)`: Unicode variable-length character string (UTF-16LE / UTF-8)
  * `TEXT`: Unlimited large-capacity text
* **3.3. Date and Time Data Types (Temporal Types)**
  * `DATE`: Year-month-day (YYYY-MM-DD)
  * `TIME`: Hour:minute:second (HH:MM:SS)
  * `DATETIME`: Millisecond-precision date and time (1900-01-01 to 9999-12-31)
* **3.4. Logical & Bit Data Types (Boolean Types)**
  * `BIT` / `BOOLEAN`: 0 (FALSE) or 1 (TRUE)
* **3.5. Binary & Identifier Data Types (Binary & UUID Types)**
  * `VARBINARY(n)` / `BYTEA`: Variable-length raw byte array
  * `UNIQUEIDENTIFIER` / `UUID`: 128-bit universally unique identifier (RFC 4122 / UUIDv7)
* **3.6. Semi-Structured Data Types**
  * `JSON`: JSON text validation and B-Tree indexing support

---

### Chapter 4. SQL Statement Reference
* **4.1. Data Definition Language (DDL)**
  * `CREATE TABLE` (supports automated rolling `PARTITION BY`), `ALTER TABLE` (supports `DROP PARTITION`)
  * `SHOW PARTITIONS` (inspect partition status and row count)
  * `DROP TABLE`, `TRUNCATE TABLE`
  * `CREATE INDEX`, `DROP INDEX`
  * `CREATE VIEW`, `DROP VIEW`
  * `CREATE PROCEDURE`, `ALTER PROCEDURE`, `DROP PROCEDURE`
  * `CREATE FUNCTION`, `ALTER FUNCTION`, `DROP FUNCTION`
  * `CREATE TRIGGER`, `ALTER TRIGGER`, `DROP TRIGGER`
* **4.2. Data Manipulation Language (DML)**
  * `SELECT`: Projection, aliasing (`AS`), aggregation, predicates
  * `INSERT INTO`: Single / multi-row insertion, `DEFAULT VALUES`
  * `UPDATE`: Single / multi-column modification, conditional update
  * `DELETE`: Conditional row deletion
  * `MERGE`: Conditional INSERT/UPDATE/DELETE atomic synchronization
  * `SELECT INTO`: Bind query results to new table or local variables
* **4.3. Query Clauses & Modifiers**
  * `FROM`: Tables, views, TVFs, derived tables (subquery in FROM)
  * `JOIN`: `INNER JOIN`, `LEFT [OUTER] JOIN`, `RIGHT JOIN`, `CROSS JOIN`, `CROSS APPLY`
  * `WHERE`: Search predicates, `AND`, `OR`, `NOT`, `IN`, `BETWEEN`, `LIKE`, `EXISTS`
  * `GROUP BY` & `HAVING`: Grouping summaries, `ROLLUP`, `CUBE`, `GROUPING SETS`
  * `ORDER BY`: Sort order (`ASC`/`DESC`), Null sorting order
  * `LIMIT` / `OFFSET` and `OFFSET ... FETCH NEXT ... ROWS ONLY`
  * `TOP (n) [PERCENT]`
* **4.4. Transaction Control Language (TCL)**
  * `BEGIN TRANSACTION` / `BEGIN TRAN`
  * `COMMIT TRANSACTION` / `COMMIT TRAN`
  * `ROLLBACK TRANSACTION` / `ROLLBACK TRAN`
  * `SAVE TRANSACTION <savepoint>` / `ROLLBACK TO <savepoint>`
* **4.5. Session & Control Language (SCL)**
  * `USE <database>`: Tolerant virtual no-op handling and TDS environment synchronization
  * `SET NOCOUNT ON/OFF`
  * `SET ANSI_NULLS ON/OFF`
  * `SET QUOTED_IDENTIFIER ON/OFF`
  * `SET TRANSACTION ISOLATION LEVEL`
  * `SET work_mem = '...'`
  * `SHOW <variable>`
* **4.6. Administrative Statements**
  * `ANALYZE TABLE <table_name>`: Generate CBO cost statistics and histograms
  * `EXPLAIN [ANALYZE] <query>`: Output query execution plan and actual profiling metrics
  * `DBCC CHECKDB`: Database integrity verification

---

### Chapter 5. Functions & Operators Reference
* **5.1. Operators**
  * Arithmetic operators: `+`, `-`, `*`, `/`, `%`
  * Comparison operators: `=`, `<>`, `!=`, `<`, `<=`, `>`, `>=`
  * Logical operators: `AND`, `OR`, `NOT`, `IS NULL`, `IS NOT NULL`
  * String concatenation: `+` (T-SQL), `||` (ANSI/PostgreSQL)
  * Pattern matching: `LIKE`, `NOT LIKE` (`%`, `_` wildcards)
* **5.2. Conditional Expressions**
  * `CASE WHEN ... THEN ... [ELSE ...] END` (Simple and searched forms)
  * `COALESCE(val1, val2, ...)`
  * `ISNULL(check_expr, replacement_value)`
  * `NULLIF(expr1, expr2)`
* **5.3. Aggregate & Analytical Functions**
  * `COUNT(*)`, `COUNT(col)`, `COUNT(DISTINCT col)`
  * `SUM(col)`, `AVG(col)`, `MIN(col)`, `MAX(col)`
  * Window functions: `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`
  * Percentile & quantile analytical functions: `PERCENTILE_CONT`, `PERCENTILE_DISC`
* **5.4. String Functions**
  * `LEN()`, `DATALENGTH()`, `SUBSTRING()`, `LEFT()`, `RIGHT()`
  * `LTRIM()`, `RTRIM()`, `TRIM()`, `UPPER()`, `LOWER()`
  * `REPLACE()`, `CONCAT()`, `QUOTENAME()`, `CHARINDEX()`, `STRING_SPLIT()`
* **5.5. Mathematical Functions**
  * `ABS()`, `ROUND()`, `CEILING()`, `FLOOR()`, `POWER()`, `SQRT()`, `SIGN()`
* **5.6. Date/Time Functions**
  * `GETDATE()`, `CURRENT_TIMESTAMP`, `SYSDATETIME()`
  * `DATEADD()`, `DATEDIFF()`, `DATEPART()`, `YEAR()`, `MONTH()`, `DAY()`
* **5.7. Type Conversion Functions**
  * `CAST(expression AS data_type)`
  * `CONVERT(data_type, expression [, style])`
* **5.8. System & Metadata Functions**
  * `@@VERSION`, `@@SPID`, `@@TRANCOUNT`, `@@IDENTITY`, `@@ROWCOUNT`
  * `SCOPE_IDENTITY()`, `SCHEMA_NAME([id])`, `SCHEMA_ID([name])`
  * `OBJECT_ID([name])`, `OBJECT_NAME([id])`, `SERVERPROPERTY([property])`
  * `DB_NAME()`, `CURRENT_DATABASE()`

---

### Chapter 6. System Catalogs Reference
* **6.1. MS SQL Server Compatible Catalog (`sys.*`)**
  * `sys.databases`: `master` single catalog and `master.mdf` metadata
  * `sys.tables`, `sys.columns`, `sys.indexes`, `sys.index_columns`
  * `sys.objects`, `sys.schemas`, `sys.partitions`, `sys.master_files`
  * `sys.types`, `sys.default_constraints`, `sys.computed_columns`, `sys.extended_properties`
* **6.2. ANSI / ISO Compatible Catalog (`INFORMATION_SCHEMA.*`)**
  * `INFORMATION_SCHEMA.TABLES` (default catalog `postgres`)
  * `INFORMATION_SCHEMA.COLUMNS`
  * `INFORMATION_SCHEMA.TABLE_CONSTRAINTS`
  * `INFORMATION_SCHEMA.KEY_COLUMN_USAGE`
  * `INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS`
* **6.3. PostgreSQL Compatible Catalog (`pg_catalog.*`)**
  * `pg_catalog.pg_database`: `postgres` single database exposure
  * `pg_catalog.pg_class`, `pg_attribute`, `pg_type`, `pg_index`
  * `pg_catalog.pg_constraint`, `pg_namespace`, `pg_proc`, `pg_stat_activity`
  * Virtual system functions: `current_schema()`, `current_setting()`, `version()`

---

### Chapter 7. REST API Reference
* **7.1. System & Real-Time Monitoring Endpoints**
  * `GET /api/health`: Server status and health-check ping
  * `GET /api/version`: Engine release version (`0.90.0`) and build metadata
  * `GET /api/metrics`: Prometheus-compatible real-time concurrency, QPS, heap memory metrics
  * `GET /api/realtime`: Server-Sent Events (SSE) real-time performance and query stream
* **7.2. Authentication & Session Management Endpoints**
  * `POST /api/auth/login`: Administrator email/password verification and JWT issuance
  * `POST /api/auth/refresh`: Session token renewal
* **7.3. SQL & Table/Record CRUD Endpoints**
  * `POST /api/sql`: Arbitrary T-SQL/SQL query execution
  * `GET /api/tables`: Table list and column metadata
  * `POST /api/tables/:table/columns`: Non-destructive column addition DDL API
  * `GET /api/records/:table`: Table record pagination, search, and filtering
  * `POST /api/records/:table`, `PUT /api/records/:table`, `DELETE /api/records/:table`: Single/bulk CRUD
* **7.4. Activity Audit & Slow-Query Logging Endpoints**
  * `GET /api/logs/requests`: Recent 500 HTTP/SQL activity audit logs
  * `GET /api/slow-queries`: Slow-query log exceeding threshold (`100ms`)
* **7.5. Stored Procedure Invocation Endpoints**
  * `POST /api/procs/exec`: Remote RPC execution of stored procedures with parameter binding
* **7.6. Backup & Disaster Recovery Endpoints**
  * `POST /api/backup`: Online hot backup `.tar.gz` streaming download
  * `POST /api/restore`: Backup archive upload and full restoration
* **7.7. High-Availability Replication Management Endpoints**
  * `GET /api/replication/status`: Replication role (Primary/Replica) and LSN status
  * `GET /api/replication/stream`: Replica-facing WAL streaming connection (Server-Sent Events)
  * `POST /api/replication/promote`: Zero-downtime promotion of Replica node to Primary

---

### Chapter 8. Wire Protocol Specifications
* **8.1. PostgreSQL Wire Protocol v3 Specification**
  * Port: `5433` (default database `postgres`)
  * Supported packets: StartupMessage, SSLRequest, PasswordMessage, Query, Parse, Bind, Execute, Describe, Sync, Flush, Terminate
  * Supported responses: AuthenticationOk, ReadyForQuery, RowDescription, DataRow, CommandComplete, ErrorResponse, NoticeResponse
* **8.2. Microsoft SQL Server TDS 7.x / 8.0 Protocol Specification**
  * Port: `1433` (default database `master`)
  * Packet types: `0x01` (SQLBatch), `0x02` (PreTDS7Login), `0x03` (RPC), `0x04` (Tabular), `0x06` (Attention), `0x0E` (TransactionManager), `0x10` (Login7)
  * Data tokens: `COLMETADATA` (`0x81`), `ROW` (`0xD1`), `NBCROW` (`0xD2`), `DONE` (`0xFD`), `DONEPROC` (`0xFE`), `ENVCHANGE` (`0xE3`), `ERROR` (`0xAA`), `INFO` (`0xAB`), `RETURNSTATUS` (`0x79`), `RETURNVALUE` (`0xAC`)

---

### Chapter 9. Error Codes & Diagnostics
* **9.1. LionSQL Standard Internal Error Codes**
  * Internal error code, English message, ANSI SQLSTATE 5-character mapping table
* **9.2. TDS Error Codes & Severity Levels (TDS Error Numbers & Severity)**
  * Severity 1~10 (Informational / Warnings), Severity 11~16 (User Execution Errors), Severity 17~25 (System Fatal Errors)
* **9.3. PostgreSQL Error Severity & SQLSTATE (PG Error Severity)**
  * `08000` (Connection Exception), `23505` (Unique Violation), `40001` (Serialization Failure / Write Conflict), `42P01` (Undefined Table), `53200` (Out of Memory / Storage Capacity Limit Exceeded)

---

## Chapter 1. Command-Line Interface (CLI Reference)

### 1.1. Single Binary (`bin/lionsql`) Command Structure

LionSQL adopts the Subcommand Pattern to execute all operational tasks—ranging from server startup, background daemon control, system integrity diagnostics, schema migration, terminal query execution, to physical backup and recovery—using a single executable binary (`bin/lionsql`).

```bash
lionsql [command] [flags...]
```

* If you omit the command and pass only flags or invoke the binary without arguments, the default subcommand `serve` is automatically executed.
* All flags support single-dash (`-flag`) and double-dash (`--flag`) forms interchangeably.
* For boolean flags, specifying `-flag` sets the value to `true`, while disabling it requires explicit notation such as `-flag=false`.

---

### 1.2. Subcommand Specifications

#### 1. `serve` (Default Interactive Foreground Server Launch)
Launches the server as a foreground process in the current terminal session. Real-time engine logs are emitted via standard output (`stdout`) and standard error (`stderr`), and receiving `Ctrl+C` (`SIGINT`) or `SIGTERM` initiates a graceful shutdown sequence.

* **Syntax**:
  ```bash
  lionsql [serve] [flags...]
  ```
* **Supported Flags**:
  | Flag | Type | Default | Description |
  | :--- | :---: | :---: | :--- |
  | `-dir` | string | `data` | Physical directory path where data files and transaction logs (WAL/AOF) are stored |
  | `-http` | string | `:8090` | TCP address for the REST API and embedded Web Studio listener (disabled: `""`) |
  | `-pg` | string | `:5433` | TCP address for the PostgreSQL Wire Protocol v3 compatible listener (disabled: `""`) |
  | `-tds` | string | `:1433` | TCP address for the MS SQL Server TDS 7.x/8.0 compatible listener (disabled: `""`) |
  | `-engine` | string | `wiredtiger` | Primary storage backend selection (`wiredtiger` or `memory`) |
  | `-ephemeral` | bool | `false` | Ultra-fast pure in-memory volatile mode that completely bypasses disk file I/O |
  | `-dev` | bool | `false` | Enable developer mode (emits colored console traces for SQL parsing and execution plans) |
  | `-max-memory` | string | `""` | Engine global memory ceiling (e.g. `2GB`, `4096MB`). If empty, automatically restricted to 65% of host physical RAM |
  | `-work-mem` | string | `64MB` | Per-operator memory budget for external sorting (External Sort) and hash joins |
  | `-max-workers` | int | `256` | Maximum size of the goroutine worker pool handling client requests concurrently |
  | `-max-idle-sessions` | int | `64` | Maximum allowable number of idle session connections preserved in the pool |
  | `-auth` | bool | `true` | Whether the user authentication and RBAC authorization layer is enabled |
  | `-admin-email` | string | `admin@lionsql.local` | Superuser account email generated during initial bootstrap |
  | `-admin-pass` | string | `1234567890` | Superuser account password (strongly recommended to change after initial run) |
  | `-jwt-secret` | string | `""` | HMAC-SHA256 secret for signing REST API tokens (automatically generates 32 random bytes if omitted) |
  | `-auto-seed` | bool | `false` | Automatically populate E-Commerce sample tables/data when data directory is empty |
  | `-slow-log` | string | `data/slow_queries.log` | Storage path for dedicated slow-query diagnostic log file |
  | `-slow-threshold-ms` | float | `100.0` | Execution elapsed time threshold (in milliseconds) to categorize a query as slow |
  | `-general-log` | string | `""` | Storage path for audit log file recording all inbound SQL statements in real time |
* **Execution Example**:
  ```bash
  # Recommended production startup (2GB memory ceiling, WiredTiger engine, 50ms slow-query detection)
  lionsql serve -dir /var/lib/lionsql/data -max-memory 2GB -slow-threshold-ms 50.0
  ```

#### 2. `start` (Launch Background Daemon Process)
Forks and detaches the LionSQL server as an operating system background daemon process. Standard output and standard error are automatically redirected to the `$DATA_DIR/lionsql.log` file, and the process ID is atomically recorded in the `$DATA_DIR/lionsql.pid` file.

* **Syntax**:
  ```bash
  lionsql start [-dir <path>] [-dev] [serve flags...]
  ```
* **Operational Mechanics**:
  1. Checks whether an existing `lionsql.pid` file is present in the specified data directory.
  2. Verifies whether that PID represents a live process using POSIX `kill(pid, 0)` signal to prevent duplicate launches.
  3. Spawns the background process and detaches standard input/output descriptors from the terminal.
  4. Performs health-check probes for up to 5 seconds until the daemon successfully binds ports and opens listeners, then yields CLI control.
* **Execution Example and Output**:
  ```bash
  $ lionsql start -dir data
  LionSQL daemon started successfully. (PID: 99777)
  Logs redirected to: data/lionsql.log
  Web Studio URL: http://localhost:8090
  PostgreSQL v3: localhost:5433
  MS SQL Server TDS: localhost:1433
  ```

#### 3. `stop` (Safely Terminate Background Daemon)
Reads the process ID recorded in the `$DATA_DIR/lionsql.pid` file, sends a `SIGTERM` signal to the server, and supervises a safe graceful shutdown sequence.

* **Syntax**:
  ```bash
  lionsql stop [-dir <path>]
  ```
* **Operational Mechanics**:
  1. Reads the PID file to verify process existence.
  2. Sends a `SIGTERM` signal to the target process.
  3. Polls process termination status every 100ms for up to 1.5 seconds (complying with `[RULE:CleanTeardown]`).
  4. If the process does not terminate within 1.5 seconds, escalates to a `SIGKILL` signal for forceful termination to prevent resource leaks.
  5. Safely unlinks and removes the `lionsql.pid` file upon completed teardown.
* **Execution Example and Output**:
  ```bash
  $ lionsql stop -dir data
  Stopping LionSQL daemon (PID: 99777)...
  LionSQL daemon (PID: 99777) stopped cleanly.
  ```

#### 4. `restart` (Seamless Background Daemon Restart)
Safely terminates the running daemon, then immediately restarts the background daemon with identical configuration and directory arguments.

* **Syntax**:
  ```bash
  lionsql restart [-dir <path>] [-dev]
  ```
* **Execution Example and Output**:
  ```bash
  $ lionsql restart -dir data
  Stopping LionSQL daemon (PID: 99777)... Done.
  LionSQL daemon started successfully. (PID: 101234)
  ```

#### 5. `status` (Inspect Daemon Process Status)
Inspects the PID file in the data directory, the OS process table, and TCP port binding states, printing a terminal summary of the daemon operational state and resource utilization.

* **Syntax**:
  ```bash
  lionsql status [-dir <path>]
  ```
* **Sample Output**:
  ```bash
  $ lionsql status -dir data
  LionSQL Server Status: RUNNING
  ----------------------------------------
  PID               : 99777
  Data Directory    : ./data
  Storage Backend   : wiredtiger (Disk B+Tree MVCC)
  HTTP / Studio     : 127.0.0.1:8090 (HEALTHY)
  PostgreSQL v3     : 127.0.0.1:5433 (LISTENING)
  MS SQL Server TDS : 127.0.0.1:1433 (LISTENING)
  Uptime            : 4h 12m 35s
  Heap Allocated    : 142.5 MB / Max: 2048.0 MB (6.9%)
  Active Workers    : 8 / 256
  ```

#### 6. `doctor` (System Environment & Integrity Diagnostics)
Conducts thorough diagnostics across 5 subsystem domains—data directory integrity, WAL/AOF LSN sequence, OS file descriptor limits, port contention, and free disk space—either prior to server boot or during active operations.

* **Syntax**:
  ```bash
  lionsql doctor [-dir <path>]
  ```
* **Diagnostic Checkpoints**:
  - `[FILESYSTEM]`: Data directory permissions (read/write `0755`), available disk capacity (recommended: >= 1GB).
  - `[NETWORKING]`: Port occupancy status and loopback binding availability for `:8090`, `:5433`, and `:1433`.
  - `[CATALOG & METADATA]`: Header magic number (`LION_V1`) and checksum verification for `_schema.dat` and catalog files.
  - `[WAL / AOF]`: Monotonic increase of WAL segment Log Sequence Numbers (LSN) and crash recovery log validity.
  - `[RESOURCE GOVERNOR]`: OS `ulimit -n` file descriptor ceiling (recommended: >= 4096, ideally 65535), kernel virtual memory settings.
* **Sample Output**:
  ```bash
  $ lionsql doctor -dir data
  Checking LionSQL Environment & Storage Integrity...
  [OK] Data directory permissions: rwxr-xr-x (data)
  [OK] Free disk space: 42.8 GB available (Threshold: > 1.0 GB)
  [OK] TCP Port 8090 (HTTP/Studio): AVAILABLE
  [OK] TCP Port 5433 (PGWire): AVAILABLE
  [OK] TCP Port 1433 (TDS): AVAILABLE
  [OK] Catalog schema integrity: PASSED (14 tables, 8 procedures registered)
  [OK] WAL LSN continuity check: LSN=1849201 PASSED
  [OK] OS File Descriptor Limit: 10240 (Recommended: >= 4096)
  Overall Result: HEALTHY (0 errors, 0 warnings)
  ```

#### 7. `init-config` (Generate Standard Configuration Template)
Generates a `lionsql.json` configuration template pre-populated with comments and recommended default values for all configuration keys.

* **Syntax**:
  ```bash
  lionsql init-config [-out <filepath>]
  ```
  - `-out`: Target file path to generate (default: `lionsql.json`). Returns an error without overwriting if the file already exists.

#### 8. `migrate` (Declarative Schema Migration Tool)
CLI tool managing database schema revisions encoded as reproducible migration scripts.

* **Syntax**:
  ```bash
  lionsql migrate <action> [flags...]
  ```
  | Action | Description |
  | :--- | :--- |
  | `new <name>` | Generate a timestamped migration file pair (`V{timestamp}__{name}.up.sql`, `.down.sql`) |
  | `up` | Apply all pending upward migration files sequentially in transactional order |
  | `down` | Roll back the most recently applied migration (specify steps via `-steps <n>`) |
  | `status` | Display currently applied migration versions and pending file list |

#### 9. `sql` (Lightweight Inline Query Runner)
Executes SQL statements directly against a local LionSQL server and renders results as formatted terminal tables without requiring external client tools like `psql` or `sqlcmd`.

* **Syntax**:
  ```bash
  lionsql sql -query "<SQL_STATEMENT>" [-dir <path>]
  ```
* **Execution Example**:
  ```bash
  $ lionsql sql -query "SELECT table_name, row_count FROM sys.tables WHERE is_user_table = 1;"
  +--------------+-----------+
  | table_name   | row_count |
  +--------------+-----------+
  | customers    |      1250 |
  | orders       |      4820 |
  | order_items  |     15200 |
  +--------------+-----------+
  (3 rows affected in 0.82ms)
  ```

#### 10. `backup` (Online Non-Disruptive Hot Backup)
Packages the active data directory and WAL checkpoints into a compressed archive (`.tar.gz`) while guaranteeing full transaction consistency without stopping engine execution.

* **Syntax**:
  ```bash
  lionsql backup -out <archive_path.tar.gz> [-dir <path>]
  ```
* **Execution Example**:
  ```bash
  $ lionsql backup -out /backups/lionsql_20241019.tar.gz -dir data
  Initiating online storage checkpoint... Done.
  Archiving data directory to /backups/lionsql_20241019.tar.gz...
  Backup completed successfully. (Size: 45.2 MB, Checkpoint LSN: 1849300)
  ```

#### 11. `restore` (Physical Backup Archive Restoration)
Extracts a generated backup archive into the designated data directory and verifies storage integrity.

* **Syntax**:
  ```bash
  lionsql restore -in <archive_path.tar.gz> -dir <target_data_dir>
  ```
  > [!WARNING]
  > If the target data directory already exists, restoration aborts immediately to prevent accidental data loss. You must specify a new path or empty the existing directory beforehand.

#### 12. `version` (Engine Version & Build Metadata)
Prints the official release version, build tags, target OS/architecture, and Git commit hash of the LionSQL binary.

* **Syntax**:
  ```bash
  lionsql version
  ```
* **Sample Output**:
  ```bash
  LionSQL v0.90.0 (darwin/arm64) [tags: wiredtiger]
  Git Commit: 477a4cb6
  Go Version: go1.24.0
  Build Date: 2024-10-19T08:30:00Z
  Storage Backends: WiredTiger MVCC, In-Memory OCC
  ```

---

### 1.3. Global CLI Flag Specifications by Category

| Flag Name | Data Type | Default | Environment Variable Mapping | Detailed Description |
| :--- | :---: | :---: | :--- | :--- |
| **Network Configuration** | | | | |
| `-http` | string | `:8090` | `LION_HTTP_ADDR` | TCP listener address for HTTP REST API and Web Studio. Empty string (`""`) disables it |
| `-app` | string | `""` | `LIONSQL_APP` | Path to single HTML file or flat static SPA directory to host (embedded web server) |
| `-pg` | string | `:5433` | `LION_PG_ADDR` | TCP listener address for PostgreSQL Wire Protocol v3. Empty string disables it |
| `-tds` | string | `:1433` | `LION_TDS_ADDR` | TCP listener address for Microsoft SQL Server TDS 7.x/8.0. Empty string disables it |
| `-tls` | bool | `false` | `LION_TLS_ENABLED` | Enable Transport Layer Security (TLS/SSL) encryption layer |
| `-tls-cert` | string | `""` | `LION_TLS_CERT` | File path to X.509 public key certificate (PEM format) |
| `-tls-key` | string | `""` | `LION_TLS_KEY` | File path to private key (PEM format) |
| `-require-ssl` | bool | `false` | `LION_REQUIRE_SSL` | Forcefully reject unencrypted plain-text connections and require SSL |
| **Storage Configuration** | | | | |
| `-dir` | string | `data` | `LION_DATA_DIR` | Physical directory path for data files, catalog metadata, and WAL logs |
| `-engine` | string | `wiredtiger` | `LION_ENGINE` | Primary storage backend selection (`wiredtiger`: disk B+Tree MVCC, `memory`: in-memory OCC) |
| `-ephemeral` | bool | `false` | `LION_EPHEMERAL` | Pure in-memory volatile mode bypassing all disk file I/O |
| `-auto-seed` | bool | `false` | `LION_AUTO_SEED` | Automatically populate E-Commerce sample dataset on initial run |
| **Resource Governor** | | | | |
| `-max-memory` | string | `""` | `LION_MAX_MEMORY` | Engine global memory ceiling (e.g. `2GB`, `4096MB`). Defaults to 65% of available host RAM if omitted |
| `-work-mem` | string | `64MB` | `LION_WORK_MEM` | Memory budget for single query operator (External Merge Sort, Hash Join) |
| `-max-workers` | int | `256` | `LION_MAX_WORKERS` | Maximum concurrent workers in asynchronous goroutine worker pool |
| `-max-idle-sessions` | int | `64` | `LION_MAX_IDLE_SESSIONS` | Number of idle session connections preserved in pool |
| **Security & Authentication** | | | | |
| `-auth` | bool | `true` | `LION_AUTH_ENABLED` | Whether authentication and authorization security layer is enabled |
| `-admin-email` | string | `admin@lionsql.local`| `LION_ADMIN_EMAIL` | Administrator email account generated during bootstrap |
| `-admin-pass` | string | `1234567890` | `LION_ADMIN_PASS` | Administrator login password |
| `-jwt-secret` | string | `""` | `LION_JWT_SECRET` | Secret key for REST API JWT signing (auto-generated if omitted) |
| **Logging & Auditing** | | | | |
| `-dev` | bool | `false` | `LION_DEV_MODE` | Enable real-time colored query tracing and debug logging |
| `-slow-log` | string | `data/slow_queries.log` | `LION_SLOW_LOG` | Log file path dedicated to slow queries |
| `-slow-threshold-ms` | float | `100.0` | `LION_SLOW_THRESHOLD_MS` | Elapsed threshold (in ms) to classify a query as slow |
| `-general-log` | string | `""` | `LION_GENERAL_LOG` | Audit log file path recording all inbound queries |
| **High Availability & Replication** | | | | |
| `-replica-of` | string | `""` | `LION_REPLICA_OF` | Primary node URL to receive WAL streaming from (e.g. `http://10.0.0.1:8090`) |
| `-node-id` | string | `node-1` | `LION_NODE_ID` | Unique node identifier within the replication cluster |
| `-auto-failover` | bool | `false` | `LION_AUTO_FAILOVER` | Enable automatic zero-downtime promotion of Replica upon Primary failure |
| `-peers` | string | `""` | `LION_PEERS` | Comma-separated list of peer node URLs for health checking and quorum |
| `-heartbeat-timeout-ms` | int | `3000` | `LION_HEARTBEAT_TIMEOUT_MS` | Timeout (in ms) to declare leader heartbeat failure |
| `-cluster-token` | string | `""` | `LIONSQL_CLUSTER_TOKEN` | Pre-shared secret token for HA replication inter-node authentication |

---

### 1.4. Process Lifecycle & POSIX Signal Conventions

LionSQL complies strictly with `[RULE:PanicRecovery]` and `[RULE:CleanTeardown]` to manage all runtime termination scenarios and operating system signals safely.

* **Standard Exit Codes**:
  - `0 (EX_OK)`: Clean termination (graceful drain completed after receiving `SIGINT` or `SIGTERM`).
  - `1 (EX_GENERAL)`: General runtime error (TCP port bind failure, storage file locking failure, disk I/O error).
  - `2 (EX_USAGE)`: Invalid CLI flag, unrecognized parameter value, or illegal subcommand.
  - `3 (EX_DATAERR)`: Schema catalog integrity check failure, unrecoverable transaction log corruption.
* **POSIX Signal Handling Conventions**:
  - `SIGINT` (`Ctrl+C`) / `SIGTERM`:
    1. Immediately closes inbound TCP listeners (`:8090`, `:5433`, `:1433`) to block new connection intake.
    2. Waits up to 1.0 second for currently executing queries and active transactions to complete (drain phase).
    3. Flushes dirty pages in memory buffers and transaction WAL/AOF streams to persistent disk storage (`fsync`).
    4. Closes WiredTiger and memory storage engine handles and terminates the process cleanly.
    5. If the entire sequence exceeds 1.5 seconds, a background watcher goroutine escalates to `SIGKILL` to force-terminate the process cleanly without dangling resource handles.
  - `SIGHUP`:
    - Re-opens log file descriptors (`lionsql.log`, `slow_queries.log`) to support log rotation, and performs a hot reload of modified `lionsql.json` configuration values.

---

### 1.5. Benchmark and Load Verification CLI Specifications (`bin/bench`, `bin/tpcc-runtime`, `bin/tpcc-kernel`)

The LionSQL source repository provides 3 standalone benchmark binaries to verify high-concurrency OLTP performance and TPC-C transactional compliance.

#### 1. `bin/bench` (High-Concurrency Account Transfer OLTP Microbenchmark)
* **Purpose**: Executes tens of thousands of concurrent fund transfers across 10,000+ accounts to evaluate OCC/MVCC conflict resolution throughput and TPS.
* **Key Flags**:
  - `-accounts <int>`: Number of initial accounts to generate (default: `10000`)
  - `-workers <int>`: Number of client goroutine workers dispatching transactions concurrently (default: `100`)
  - `-ops <int>`: Total transaction operations to execute (default: `50000`)
  - `-engine <name>`: Target storage backend (`wiredtiger` or `memory`)
* **Execution Example**:
  ```bash
  go build -tags wiredtiger -o bin/bench ./cmd/bench
  ./bin/bench -workers 200 -ops 100000 -engine wiredtiger
  ```

#### 2. `bin/tpcc-runtime` (Official TPC-C T-SQL VDBE Runtime Benchmark)
* **Purpose**: Implements standard TPC-C business transactions (New-Order, Payment, Order-Status) as T-SQL stored procedures and executes them via single VDBE bytecode programs (`exec.RunInTx`) to verify full ACID atomicity.
* **Key Flags**:
  - `-warehouses <int>`: TPC-C scale factor (number of warehouses, default: `2`)
  - `-workers <int>`: Number of concurrent client workers (default: `20`)
  - `-duration <duration>`: Benchmark run duration (e.g. `30s`, `2m`, default: `15s`)
  - `-fsync <mode>`: WAL disk synchronization mode (`everysec`: sync once per second, `always`: fsync on every commit, `off`: OS buffering)
* **Execution Example**:
  ```bash
  go build -tags wiredtiger -o bin/tpcc-runtime ./cmd/tpcc_runtime
  ./bin/tpcc-runtime -warehouses 5 -workers 50 -duration 1m -fsync everysec
  ```

#### 3. `bin/tpcc-kernel` (Storage Engine Kernel TPC-C Benchmark)
* **Purpose**: Directly invokes storage layer SPI (`storage.Tx`) CRUD methods without SQL compilation or VDBE virtual machine overhead, measuring raw concurrency limits of underlying storage backends (OCC and WiredTiger B+Tree).
* **Execution Example**:
  ```bash
  go build -tags wiredtiger -o bin/tpcc-kernel ./cmd/tpcc_kernel
  ./bin/tpcc-kernel -warehouses 10 -workers 100 -duration 10s
  ```

---

## Chapter 2. Configuration File Reference

### 2.1. Configuration Precedence & Deterministic Hierarchy

LionSQL resolves and finalizes configuration values using a 4-tier deterministic hierarchy (complying with `[RULE:ConfigHierarchy]`):

$$\text{CLI Flags} > \text{Environment Variables} > \text{Config File (lionsql.json)} > \text{Built-in Defaults}$$

1. **CLI Flags**: Arguments specified on the command line take highest precedence.
2. **Environment Variables**: Environment variables prefixed with `LION_` override values specified in the configuration file.
3. **Configuration File (`lionsql.json`)**: Settings are parsed from a JSON file in the working directory or specified via `-config`.
4. **Built-in Defaults**: If all higher-tier configurations are omitted, safe internal engine defaults (`[RULE:SafeDefaults]`) are applied.

---

### 2.2. Complete `lionsql.json` Schema Specification

The following is the canonical schema for `lionsql.json` containing all configuration sections supported by LionSQL:

```json
{
  "server": {
    "engine": "wiredtiger",
    "data_dir": "data",
    "ephemeral": false,
    "auto_seed": false,
    "pid_file": "data/lionsql.pid",
    "graceful_timeout_seconds": 1.5
  },
  "network": {
    "http_addr": ":8090",
    "pg_addr": ":5433",
    "tds_addr": ":1433",
    "tls": {
      "enabled": false,
      "cert_file": "",
      "key_file": "",
      "require_ssl": false
    }
  },
  "auth": {
    "enabled": true,
    "admin_email": "admin@lionsql.local",
    "admin_pass": "",
    "jwt_secret": "",
    "jwt_expiry_hours": 72
  },
  "resources": {
    "max_memory": "",
    "work_mem": "64MB",
    "max_workers": 256,
    "max_idle_sessions": 64,
    "memory_pressure_threshold_pct": 85
  },
  "logging": {
    "dev_mode": false,
    "slow_query": {
      "enabled": true,
      "threshold_ms": 100.0,
      "file": "data/slow_queries.log"
    },
    "general_query": {
      "enabled": false,
      "file": "data/general_query.log"
    }
  },
  "replication": {
    "enabled": false,
    "role": "PRIMARY",
    "node_id": "node-1",
    "primary_url": "",
    "auto_failover": false,
    "peers": [],
    "self_url": "",
    "heartbeat_interval_ms": 1000,
    "heartbeat_timeout_ms": 3000
  },
  "smtp": {
    "host": "",
    "port": 587,
    "username": "",
    "password": "",
    "from": "",
    "tls": true
  }
}
```

---

### 2.3. Detailed Parameter Specifications by Section

#### 1. `server` Section
* **`engine`** (string): Storage engine backend identifier.
  - `"wiredtiger"`: Disk-based lock-free B+Tree and MVCC engine (default).
  - `"memory"`: In-memory optimistic concurrency control (OCC) engine (ultra-low-latency workloads).
* **`data_dir`** (string): Target filesystem path for database data files, WAL logs, and catalogs (default: `"data"`).
* **`ephemeral`** (bool): When set to `true`, disables disk persistence completely and operates solely in volatile RAM (recommended for test environments).
* **`auto_seed`** (bool): When set to `true`, automatically creates and populates E-Commerce demo tables upon initialization (default: `false`).
* **`pid_file`** (string): File path to record the OS process ID when running in daemon mode (default: `"data/lionsql.pid"`).
* **`graceful_timeout_seconds`** (float): Maximum duration in seconds to await active transaction completion upon receiving termination signals (default: `1.5`).

#### 2. `network` Section
* **`http_addr`** (string): TCP listener address for REST API and embedded Web Studio (default: `":8090"`).
* **`app`** (string): Path to single HTML file or flat static SPA directory to host (default: `""`, disabled).
* **`pg_addr`** (string): TCP listener address for PostgreSQL Wire Protocol v3 (default: `":5433"`).
* **`tds_addr`** (string): TCP listener address for MS SQL Server TDS 7.x/8.0 (default: `":1433"`).
* **`tls.enabled`** (bool): Whether Transport Layer Security encryption is activated (default: `false`).
* **`tls.cert_file`** (string): File path to PEM-encoded X.509 public key certificate.
* **`tls.key_file`** (string): File path to PEM-encoded private key.
* **`tls.require_ssl`** (bool): Whether to unconditionally reject unencrypted plain-text connection attempts from clients (default: `false`).

#### 3. `auth` Section
* **`enabled`** (bool): Whether authentication and authorization layer is enforced (default: `true`).
* **`admin_email`** (string): Superuser administrator account email address (default: `"admin@lionsql.local"`).
* **`admin_pass`** (string): Administrator login password (if empty, preserves existing password or defaults to standard initial secret).
* **`jwt_secret`** (string): HMAC-SHA256 secret key utilized for signing JWT session tokens.
* **`jwt_expiry_hours`** (int): Lifetime validity duration for issued JWT session tokens in hours (default: `72`).

#### 4. `resources` Section (Memory Governor & Connection Pooling)
* **`max_memory`** (string): Absolute ceiling on heap memory that can be allocated across the engine process (e.g. `"2GB"`, `"4096MB"`). When omitted, automatically calculated as 65% of host physical RAM (with a 1GB OS reserve and 50% lower guard rail).
* **`work_mem`** (string): In-memory buffer budget allocated per query operator for sorting (External Merge Sort), index creation, and hash joins (default: `"64MB"`).
* **`max_workers`** (int): Concurrency pool size for asynchronous goroutines executing client requests (default: `256`).
* **`max_idle_sessions`** (int): Maximum allowable idle client session connections kept alive in pool (default: `64`).
* **`memory_pressure_threshold_pct`** (int): Memory pressure threshold percentage (default: `85`). Exceeding this threshold triggers aggressive cache eviction and throttles buffer expansion for incoming large queries.

#### 5. `logging` Section
* **`dev_mode`** (bool): When set to `true`, prints comprehensive terminal traces of SQL AST parsing trees, VDBE bytecode disassembly, and CBO Volcano execution plans.
* **`slow_query.enabled`** (bool): Enables logging queries that exceed the duration threshold to a dedicated file (default: `true`).
* **`slow_query.threshold_ms`** (float): Latency threshold in milliseconds to classify a query as slow (default: `100.0` ms).
* **`slow_query.file`** (string): File path where slow query diagnostics are written (default: `"data/slow_queries.log"`).
* **`general_query.enabled`** (bool): Enables audit logging for all inbound SQL queries (default: `false`).
* **`general_query.file`** (string): Audit log file path for general query logging.

#### 6. `replication` Section (High Availability & Replication)
* **`enabled`** (bool): Enables replication subsystem (default: `false`).
* **`role`** (string): Node replication role (`"PRIMARY"` or `"REPLICA"`).
* **`node_id`** (string): Unique identifier distinguishing this node within the cluster (e.g. `"node-1"`).
* **`primary_url`** (string): Primary node HTTP URL to stream WAL records from when running as `REPLICA`.
* **`auto_failover`** (bool): Whether this replica autonomously promotes itself to Primary upon leader heartbeat failure (default: `false`).
* **`peers`** (array of string): List of HTTP URLs of other peer cluster nodes for health checking and consensus quorum.
* **`heartbeat_interval_ms`** (int): Transmission interval for peer heartbeats in milliseconds (default: `1000`).
* **`heartbeat_timeout_ms`** (int): Duration in milliseconds without leader response before declaring leader failure (default: `3000`).

#### 7. `smtp` Section (Embedded Transaction Mailer)
* **`host`** (string): SMTP relay hostname or IP address (e.g. `"smtp.gmail.com"`).
* **`port`** (int): SMTP relay port number (default: `587`).
* **`username`** (string): SMTP authentication username.
* **`password`** (string): SMTP authentication password or application-specific password.
* **`from`** (string): Sender display email address (e.g. `"no-reply@lionsql.local"`).
* **`tls`** (bool): Whether STARTTLS encryption is enforced (default: `true`).

---

### 2.4. Transaction Performance & Workload Profiles Summary

LionSQL offers tailored transaction profiles ranging from ultra-low-latency in-memory operations to zero-loss financial ledgers depending on combinations of storage backend (`-engine`), WAL synchronization mode (`-fsync`), and working memory (`-work-mem`).

| Workload Profile | Recommended Option Combination | Transaction Throughput (TPS) | P99 Latency | Data Durability (RPO) | Primary Target Use Cases |
| :--- | :--- | :---: | :---: | :---: | :--- |
| **Ultra-Low-Latency In-Memory** | `-engine memory`<br>`-ephemeral`<br>`-fsync off` | **20,000 ~ 25,000+ TPS**<br>(> 540k tpmC) | **< 1.0 ms** | Volatile (N/A) | Gaming sessions, caching layers, high-speed CI test harnesses |
| **Standard Production OLTP**<br>(Recommended Default) | `-engine wiredtiger`<br>`-fsync everysec`<br>`-max-memory 4GB` | **12,000 ~ 16,800+ TPS**<br>(~ 457k tpmC) | **< 10 ms** | Up to 1 second (1s Fsync) | General SaaS applications, E-Commerce order/customer management |
| **Zero-Loss Financial Ledger** | `-engine wiredtiger`<br>`-fsync always`<br>`-max-workers 128` | **1,700 ~ 1,860+ TPS**<br>(With Group Commit) | **15 ~ 25 ms** | **RPO = 0 (Zero Loss)** | Financial transfers, payment authorizations, compliance audit ledgers |
| **Large-Capacity Analytics / Batch** | `-engine wiredtiger`<br>`-work-mem 256MB` | **Zero Disk Spill** | Batch-optimized | Up to 1 second | Large-scale ETL processing, closing settlement aggregation |

> [!TIP]
> For hardware-specific deployment matrices, Group Commit inner mechanics, and slow query optimization diagnostics, refer to the [Transaction Performance Guide (`TRANSACTION_PERFORMANCE_GUIDE.md`)](TRANSACTION_PERFORMANCE_GUIDE.md) (Korean edition: [`TRANSACTION_PERFORMANCE_GUIDE.ko.md`](TRANSACTION_PERFORMANCE_GUIDE.ko.md)).

---

## Chapter 3. Data Types & Literals Reference

### 3.1. Primitive Data Type Specifications

LionSQL provides comprehensive support for Microsoft SQL Server T-SQL and ANSI/PostgreSQL standard data types, employing compact encoding formats and variable-length integer serialization across memory and disk storage layers.

#### 1. Integer & Numeric Data Types
| Data Type | Physical Storage Bytes | Valid Range & Technical Specification | Literal Notation Example |
| :--- | :---: | :--- | :--- |
| **`TINYINT`** | 1 Byte | $0 \sim 255$ (Unsigned 8-bit integer) | `0`, `255` |
| **`SMALLINT`** | 2 Bytes | $-32,768 \sim 32,767$ (Signed 16-bit integer, ZigZag encoded) | `-100`, `32767` |
| **`INT` / `INTEGER`**| 4 Bytes | $-2,147,483,648 \sim 2,147,483,647$ (Signed 32-bit integer) | `1001`, `-42` |
| **`BIGINT`** | 8 Bytes | $-9,223,372,036,854,775,808 \sim 9,223,372,036,854,775,807$ (64-bit) | `9223372036854775807` |
| **`FLOAT` / `REAL`** | 8 Bytes | IEEE 754 64-bit double precision ($1.7 \times 10^{-308} \sim 1.7 \times 10^{308}$) | `3.141592`, `-0.001` |
| **`DECIMAL(p, s)`** | Variable | Exact fixed-point numeric up to 38 digits ($p$: precision $1 \sim 38$, $s$: scale $0 \sim p$) | `12345.67`, `0.0001` |
| **`NUMERIC(p, s)`** | Variable | Identical specification and precision to `DECIMAL(p, s)` | `99.99` |

* **`DECIMAL` Internal Architecture**: Represented as a sign bit (1 bit), a scale exponent (1 byte), and a 128-bit unsigned integer array, guaranteeing zero floating-point rounding errors for accounting and financial ledgers.

#### 2. Character & Large Text Types
| Data Type | Physical Storage Bytes | Valid Range & Technical Specification | Literal Notation Example |
| :--- | :---: | :--- | :--- |
| **`CHAR(n)`** | Fixed $n$ Bytes | Fixed-length string up to 8,000 bytes (trailing spaces preserved) | `'KR  '`, `'USD '` |
| **`VARCHAR(n)`** | Variable ($2 + L$) Bytes | Variable-length UTF-8 string up to 8,000 bytes ($L \le n$) | `'Hello World'`, `'LionSQL'` |
| **`NVARCHAR(n)`** | Variable ($2 + 2L$) Bytes| Unicode variable-length string up to 4,000 characters (T-SQL N-prefix supported) | `N'Hello'`, `N'Database'` |
| **`TEXT`** | Variable (PLP Chunks) | Unlimited large-capacity text up to 2GB (supports partial chunk streaming) | `'Very long description...'` |

#### 3. Date & Time (Temporal) Data Types
| Data Type | Physical Storage Bytes | Valid Range & Precision | Literal Notation Example |
| :--- | :---: | :--- | :--- |
| **`DATE`** | 4 Bytes | `0001-01-01` to `9999-12-31` (1-day resolution) | `'2024-10-19'` |
| **`TIME`** | 8 Bytes | `00:00:00.000000` to `23:59:59.999999` (Microsecond precision) | `'17:30:00.123456'` |
| **`DATETIME`** | 8 Bytes | `1753-01-01 00:00:00` to `9999-12-31 23:59:59` (Millisecond precision) | `'2024-10-19 17:30:00'` |
| **`TIMESTAMP`** | 8 Bytes | Unix epoch microsecond integer or row-version tracking token | `'2024-10-19 17:30:00.000'` |

#### 4. Logical & Unique Identifier Types
| Data Type | Physical Storage Bytes | Valid Range & Technical Specification | Literal Notation Example |
| :--- | :---: | :--- | :--- |
| **`BIT` / `BOOLEAN`**| 1 Byte | `0` (FALSE) or `1` (TRUE) | `1`, `0`, `TRUE`, `FALSE` |
| **`UUID`** | 16 Bytes | 128-bit universally unique identifier (RFC 9562 UUIDv7 chronological ordering optimized) | `'0192a5b6-7c8d-7e9f-8a0b-1c2d3e4f5a6b'` |
| **`JSON`** | Variable | Valid JSON string parsing, schema validation, and B-Tree virtual expression indexing | `'{"user_id": 101, "role": "admin"}'` |

---

### 3.2. Physical Binary Serialization Specification (`pkg/storage/tuple.go`)

LionSQL storage engines (both In-Memory OCC and WiredTiger) share a unified, highly optimized compressed binary tuple encoding layout designed to minimize disk occupancy and CPU serialization overhead:

```
+---------------+------------------------+--------------------------+-----------------------+
| Version (1B)  | Null Bitmap (ceil(N/8))| Fixed Columns (Byte-pad) | Variable-length Data  |
+---------------+------------------------+--------------------------+-----------------------+
```

1. **Header Byte (`0x01`)**: Identifies tuple encoding format and schema codec version.
2. **Null Bitmap**: Allocated as $\lceil N/8 \rceil$ bytes for $N$ columns. When a bit is `1`, the corresponding column holds a `NULL` value, occupying exactly 0 bytes in the subsequent payload areas.
3. **Fixed Columns**: Fixed-width fields (`TINYINT`, `INT`, `BIGINT`, `FLOAT`, `DATETIME`) are serialized with ZigZag variable-length integer encoding, allowing small integers (e.g. `1`, `42`) to compress down to 1~2 bytes on disk.
4. **Variable Data**: Variable-length columns (`VARCHAR`, `TEXT`, `DECIMAL`, `JSON`) are stored contiguously with a 2-byte length prefix preceding raw payload bytes, eliminating pointer indirection overhead.

---

### 3.3. Type Conversion Matrix

| Source \ Target | `INT` | `BIGINT` | `DECIMAL` | `FLOAT` | `VARCHAR` | `DATETIME` | `BIT` |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **`TINYINT` / `INT`** | - | Implicit | Implicit | Implicit | `CAST` | Error | Implicit ($0/1$) |
| **`BIGINT`** | Overflow Guard | - | Implicit | Implicit | `CAST` | Error | Implicit |
| **`DECIMAL`** | Truncate | Truncate | - | Implicit | `CAST` | Error | Error |
| **`FLOAT`** | Truncate | Truncate | `CAST` | - | `CAST` | Error | Error |
| **`VARCHAR`** | Parse | Parse | Parse | Parse | - | ISO Parse | `'1'`/`'true'` |
| **`DATETIME`** | Error | Unix Epoch | Error | Error | `CAST`/`CONVERT` | - | Error |

---

## Chapter 4. SQL Statement Reference

### 4.1. Data Definition Language (DDL)

#### 1. `CREATE TABLE` (Table Definition)
Defines a new physical table schema in the database catalog. Fully supports Primary Keys (`PRIMARY KEY`), monotonically increasing identity counters (`IDENTITY`), Check constraints (`CHECK`), Foreign Key (`FOREIGN KEY`) cascade behaviors, and **ClickHouse-style time-series automated rolling partitioning (`PARTITION BY`)**.

* **Syntax (BNF)**:
  ```sql
  CREATE TABLE [schema_name.]table_name (
      column_name data_type [NOT NULL | NULL]
          [IDENTITY [(seed, increment)]]
          [PRIMARY KEY]
          [DEFAULT default_expression]
          [CHECK (boolean_expression)],
      ...,
      [CONSTRAINT constraint_name PRIMARY KEY (col1, col2, ...)],
      [CONSTRAINT constraint_name FOREIGN KEY (col1, ...) REFERENCES ref_table(ref_col1, ...)
          [ON DELETE {CASCADE | SET NULL | NO ACTION}]
          [ON UPDATE {CASCADE | NO ACTION}]]
  ) [PARTITION BY partition_expression];
  ```

* **Partition Expression (`PARTITION BY`) Supported Functions**:
  * `toYYYYMM(col)` or `MONTH(col)`: `YYYYMM` format (e.g. `202610`) monthly automated rolling partitioning
  * `toYYYYMMDD(col)` or `DAY(col)`: `YYYYMMDD` format (e.g. `20261024`) daily automated rolling partitioning
  * `toYYYY(col)` or `YEAR(col)`: `YYYY` format (e.g. `2026`) annual automated rolling partitioning
  * `<column_name>`: Categorical / multi-tenant partitioning based on integer or string column values

* **Execution Examples**:
  ```sql
  -- Standard orders table
  CREATE TABLE orders (
      order_id INT IDENTITY(1,1) PRIMARY KEY,
      customer_id INT NOT NULL,
      order_date DATETIME DEFAULT GETDATE(),
      total_amount DECIMAL(18,2) NOT NULL CHECK (total_amount >= 0.00),
      status VARCHAR(20) DEFAULT 'PENDING',
      CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) 
          REFERENCES customers(customer_id) ON DELETE CASCADE
  );

  -- Automated time-series rolling partition table (Monthly)
  CREATE TABLE audit_logs (
      id INT PRIMARY KEY,
      created_at DATETIME NOT NULL,
      user_id VARCHAR(50),
      action VARCHAR(100)
  ) PARTITION BY toYYYYMM(created_at);
  ```

#### 2. `ALTER TABLE` (Schema Alteration & Partition Pruning)
Appends new columns to existing tables, drops deprecated columns, and prunes expired time-series partitions at $O(1)$ speed.

* **Syntax**:
  ```sql
  ALTER TABLE table_name ADD column_name data_type [DEFAULT default_value] [NOT NULL];
  ALTER TABLE table_name DROP COLUMN column_name;
  ALTER TABLE table_name DROP PARTITION 'partition_id';
  ```
* **Execution Examples**:
  ```sql
  ALTER TABLE customers ADD phone_number VARCHAR(30) DEFAULT 'N/A';
  ALTER TABLE customers DROP COLUMN legacy_fax;

  -- Instant O(1) physical drop of August 2026 partition (auto-cleans secondary indexes)
  ALTER TABLE audit_logs DROP PARTITION '202608';
  ```

#### 2.1. `SHOW PARTITIONS` (Inspect Partition Status & Row Count)
Inspects active physical partition identifiers and current live row counts for a partitioned table.

* **Syntax**:
  ```sql
  SHOW PARTITIONS [FROM] table_name;
  ```
* **Execution Example**:
  ```sql
  SHOW PARTITIONS audit_logs;
  ```
* **Output Format**:
  ```
  +------------+-----------+-----------+
  | table_name | partition | row_count |
  +------------+-----------+-----------+
  | audit_logs | 202608    | 15200     |
  | audit_logs | 202609    | 340500    |
  | audit_logs | 202610    | 412000    |
  +------------+-----------+-----------+
  ```
  *(Identical metrics can also be queried via the virtual view: `SELECT * FROM system.partitions WHERE table_name = 'audit_logs';`)*

#### 3. `DROP TABLE` / `TRUNCATE TABLE` (Table Deletion & Truncation)
* **`DROP TABLE`**: Completely destroys table catalog metadata and physical disk storage structures.
  ```sql
  DROP TABLE [IF EXISTS] table_name;
  ```
* **`TRUNCATE TABLE`**: Preserves table schema structures while immediately purging all row records and resetting `IDENTITY` counters to their initial seed.
  ```sql
  TRUNCATE TABLE table_name;
  ```

#### 4. `CREATE INDEX` / `DROP INDEX` (B+Tree Index Management)
Supports single-column, composite-key, unique indexes, and covering indexes with non-key columns in leaf nodes (`INCLUDE`).

* **Syntax**:
  ```sql
  CREATE [UNIQUE] INDEX index_name ON table_name (col1 [ASC|DESC], col2, ...)
      [INCLUDE (col3, col4, ...)];
  
  DROP INDEX index_name [ON table_name];
  ```
* **Execution Example**:
  ```sql
  -- Covering index optimization eliminating bookmark lookups
  CREATE INDEX idx_orders_cust_date ON orders (customer_id ASC, order_date DESC)
      INCLUDE (total_amount, status);
  ```

#### 5. `CREATE PROCEDURE` / `ALTER PROCEDURE` (T-SQL Stored Procedure Definition)
Declares T-SQL stored procedures, compiles them into VDBE bytecode, and registers them in the system catalog. Supports parameters and `OUTPUT` return bindings.

* **Syntax**:
  ```sql
  CREATE [OR ALTER] PROCEDURE [schema_name.]proc_name
      [@param1 data_type [= default_val] [OUTPUT], ...]
      [WITH RECOMPILE]
  AS
  BEGIN
      SET NOCOUNT ON;
      -- Procedure body (variable declarations, IF/WHILE control statements, DML, cursors, etc.)
  END;
  ```

---

### 4.2. Data Manipulation Language (DML)

#### 1. `SELECT` (Data Query)
Supports multi-table relational joins, expressions, window analytics, group aggregation, and offset/fetch pagination.

* **Syntax**:
  ```sql
  SELECT [TOP (n) [PERCENT]] [DISTINCT] select_list
  FROM table_source [WITH (INDEX(index_name))]
  [[INNER | LEFT | RIGHT | FULL] JOIN join_table ON join_condition ...]
  [WHERE search_condition]
  [GROUP BY group_by_list [HAVING having_condition]]
  [ORDER BY order_expression [ASC | DESC], ...]
  [OFFSET offset_value ROWS FETCH NEXT fetch_value ROWS ONLY];
  ```
* **Execution Example**:
  ```sql
  SELECT TOP 10 
      c.customer_id,
      c.name,
      COUNT(o.order_id) AS total_orders,
      SUM(o.total_amount) AS gross_spend
  FROM customers c
  INNER JOIN orders o ON c.customer_id = o.customer_id
  WHERE o.status = 'COMPLETED' AND o.order_date >= '2024-01-01'
  GROUP BY c.customer_id, c.name
  HAVING SUM(o.total_amount) >= 1000.00
  ORDER BY gross_spend DESC
  OFFSET 0 ROWS FETCH NEXT 10 ROWS ONLY;
  ```

#### 2. `INSERT` (Record Insertion)
Supports single-row, multi-row, subquery results insertion (`INSERT INTO ... SELECT`), and default values insertion.

* **Syntax**:
  ```sql
  INSERT INTO table_name [(column_list)] VALUES (value_list), (value_list_2), ...;
  INSERT INTO table_name [(column_list)] SELECT select_list FROM ...;
  INSERT INTO table_name DEFAULT VALUES;
  ```

#### 3. `UPDATE` (Record Modification)
Supports single-table updates with predicates, as well as ANSI-extended syntax joining against target tables for update evaluation.

* **Syntax**:
  ```sql
  UPDATE table_name
  SET col1 = expr1, col2 = expr2, ...
  [FROM table_name t1 JOIN other_table t2 ON t1.id = t2.id]
  [WHERE condition];
  ```
* **Execution Example**:
  ```sql
  UPDATE accounts
  SET balance = balance - 500.00, updated_at = GETDATE()
  WHERE account_id = 101 AND balance >= 500.00;
  ```

#### 4. `DELETE` (Record Deletion)
* **Syntax**:
  ```sql
  DELETE FROM table_name [WHERE condition];
  ```

#### 5. `SELECT ... INTO @variable` (Variable Binding)
Binds scalar expressions from a single-row query directly into declared local variables inside T-SQL stored procedures.
```sql
DECLARE @cust_name VARCHAR(100), @credit DECIMAL(18,2);
SELECT name, balance INTO @cust_name, @credit FROM customers WHERE customer_id = @target_id;
```

---

### 4.3. Session & Transaction Control Language (TCL / SCL)

#### 1. Explicit Transaction Control (TCL)
LionSQL guarantees strict ACID transaction atomicity, managing multi-statement procedures under a single atomic execution boundary (`exec.RunInTx`).

* **`BEGIN TRANSACTION [tran_name]`**: Begins a new transaction; increments the session global variable `@@TRANCOUNT` by 1.
* **`COMMIT TRANSACTION`**: Atomically flushes modifications in private workspace memory to the WAL stream and commits. On concurrency conflict, rolls back and returns `40001 (ErrWriteConflict)`.
* **`ROLLBACK TRANSACTION`**: Instantly discards all uncommitted modifications from workspace memory and resets the transaction state.

#### 2. Session Control & Tolerant Virtual No-Op (SCL)
* **`USE <database_name>`**:
  Per LionSQL's instance-level isolation architecture, each running process services exactly one database catalog (`postgres` or `master`). For 100% compatibility with DBeaver, SSMS, sqlcmd, and legacy migration scripts, `USE <db>` is accepted as a **Tolerant Virtual No-Op**, emitting TDS environment change tokens (`0xE3 ENVCHANGE`) to synchronize client session context without syntax errors.
* **`SET NOCOUNT {ON | OFF}`**:
  Controls whether row-count completion tokens (`DONE`) are returned per statement, minimizing round-trip network payload overhead.
* **`SET work_mem = 'value'`**:
  Dynamically scales the sorting and join memory budget for the active client session.
* **`SET TRANSACTION ISOLATION LEVEL READ COMMITTED`**:
  Explicitly sets session transaction isolation semantics.

---

## Chapter 5. Functions & Operators Reference

> **T-SQL & SQL Server Compatibility Baseline**:  
> LionSQL's T-SQL grammar, procedural statements, and built-in function catalog are standardized against **Microsoft SQL Server 2019 (Version 15.0)** as the official baseline.  
> System queries report `SERVERPROPERTY('ProductVersion')` as `'15.0.2000.5'`, `SERVERPROPERTY('EngineEdition')` as `3` (Enterprise / Developer), and `@@VERSION` in SQL Server 2019 RTM format. Network wire listeners fully support **TDS 7.4 (SQL Server 2012 - 2019)** as well as modern **TDS 8.0 (SQL Server 2022 direct TLS)**.  
> Modern functions introduced in SQL Server 2016-2019 (`STRING_AGG`, `STRING_SPLIT`, `TRIM`, `JSON_VALUE`, `TRY_CONVERT`, and window functions) are implemented as first-class primitives.

### 5.1. Operator Precedence

LionSQL's expression evaluation engine strictly honors ANSI SQL and T-SQL operator precedence (ordered from highest to lowest):

1. **Parentheses**: `( )` (Grouping)
2. **Unary Operators**: `+` (Positive), `-` (Negative), `~` (Bitwise NOT)
3. **Multiplication / Division / Modulo**: `*`, `/`, `%` (Integer remainder)
4. **Addition / Subtraction / Concatenation**: `+` (Addition or T-SQL string concatenation), `-` (Subtraction), `||` (ANSI string concatenation)
5. **Comparison Operators**: `=`, `<>`, `!=`, `<`, `<=`, `>`, `>=`
6. **Range & Membership Predicates**: `LIKE`, `NOT LIKE`, `BETWEEN ... AND ...`, `IN (...)`, `IS NULL`, `IS NOT NULL`
7. **Logical Negation**: `NOT`
8. **Logical Conjunction**: `AND`
9. **Logical Disjunction**: `OR`

---

### 5.2. Built-in Function Reference

#### 1. String Functions

##### `LEN(string_expression)`
* **Syntax**: `LEN(str)`
* **Arguments**: `str` (`VARCHAR` or `NVARCHAR`)
* **Return Value**: `INT` (Actual number of characters excluding trailing spaces)
* **Example**:
  ```sql
  SELECT LEN('LionSQL Engine   '); -- Returns: 14
  ```

##### `SUBSTRING(string_expression, start, length)`
* **Syntax**: `SUBSTRING(str, start, len)`
* **Arguments**: `str` (source string), `start` (1-based starting position), `len` (character count to extract)
* **Return Value**: `VARCHAR` (Extracted substring)
* **Example**:
  ```sql
  SELECT SUBSTRING('2024-10-19', 1, 4); -- Returns: '2024'
  ```

##### `CHARINDEX(substring, string_expression [, start_location])`
* **Syntax**: `CHARINDEX(sub, str [, start])`
* **Arguments**: `sub` (search target), `str` (enclosing string), `start` (optional 1-based start offset, default: 1)
* **Return Value**: `INT` (1-based starting index, or `0` if not found)
* **Example**:
  ```sql
  SELECT CHARINDEX('@', 'admin@lionsql.local'); -- Returns: 6
  ```

##### `REPLACE(string_expression, string_pattern, string_replacement)`
* **Syntax**: `REPLACE(str, from_str, to_str)`
* **Arguments**: Source string, search pattern, and target replacement string
* **Return Value**: Transformed `VARCHAR`
* **Example**:
  ```sql
  SELECT REPLACE('v1.0-alpha', 'alpha', 'release'); -- Returns: 'v1.0-release'
  ```

##### `UPPER(str)` / `LOWER(str)`
* **Syntax**: `UPPER(str)`, `LOWER(str)`
* **Return Value**: Uppercase or lowercase converted string
* **Example**:
  ```sql
  SELECT UPPER('lionsql'), LOWER('HELLO'); -- Returns: 'LIONSQL', 'hello'
  ```

##### `LTRIM(str)` / `RTRIM(str)`
* **Syntax**: `LTRIM(str)`, `RTRIM(str)`
* **Return Value**: String with leading (left) or trailing (right) spaces trimmed
* **Example**:
  ```sql
  SELECT LTRIM('  spaced  '); -- Returns: 'spaced  '
  ```

##### `CONCAT(string_val1, string_val2 [, string_valN ...])`
* **Syntax**: `CONCAT(v1, v2, ...)`
* **Return Value**: Concatenation of all argument strings (`NULL` is safely treated as an empty string)
* **Example**:
  ```sql
  SELECT CONCAT('User #', 101, ' (', NULL, 'active)'); -- Returns: 'User #101 (active)'
  ```

##### `STRING_SPLIT(string_expression, delimiter)` (Table-Valued Function, TVF)
* **Syntax**: `SELECT value FROM STRING_SPLIT(str, sep)`
* **Arguments**: `str` (string to split), `sep` (single-character delimiter)
* **Return Value**: Table result set containing split tokens (column name: `value`)
* **Example**:
  ```sql
  SELECT value FROM STRING_SPLIT('apple,banana,cherry', ',');
  -- Result:
  -- value
  -- ------
  -- apple
  -- banana
  -- cherry
  ```

---

#### 2. Date & Time Functions

##### `GETDATE()` / `CURRENT_TIMESTAMP`
* **Syntax**: `GETDATE()`, `CURRENT_TIMESTAMP`
* **Return Value**: Current local date and time of the database server (`DATETIME`, millisecond resolution)
* **Example**:
  ```sql
  SELECT GETDATE(); -- Returns: 2024-10-19 17:30:00.123
  ```

##### `DATEADD(datepart, number, date)`
* **Syntax**: `DATEADD(part, n, date)`
* **Arguments**: `part` (`year`, `month`, `day`, `hour`, `minute`, `second`), `n` (signed integer offset), `date` (target date/time)
* **Return Value**: Computed date and time (`DATETIME`)
* **Example**:
  ```sql
  SELECT DATEADD(day, 30, '2024-01-01'); -- Returns: 2024-01-31 00:00:00
  ```

##### `DATEDIFF(datepart, startdate, enddate)`
* **Syntax**: `DATEDIFF(part, start, end)`
* **Arguments**: Difference (`end - start`) measured in designated `part` boundaries
* **Return Value**: `BIGINT`
* **Example**:
  ```sql
  SELECT DATEDIFF(day, '2024-01-01', '2024-01-10'); -- Returns: 9
  ```

##### `DATEPART(datepart, date)` / `YEAR(date)` / `MONTH(date)` / `DAY(date)`
* **Syntax**: `DATEPART(part, date)`, `YEAR(date)`, `MONTH(date)`, `DAY(date)`
* **Return Value**: Extracted portion of date/time as integer (`INT`)
* **Example**:
  ```sql
  SELECT YEAR('2024-10-19'), MONTH('2024-10-19'), DAY('2024-10-19'); -- 2024, 10, 19
  ```

---

#### 3. Conditional & Control Flow Functions

##### `COALESCE(val1, val2 [, valN ...])`
* **Syntax**: `COALESCE(v1, v2, ...)`
* **Return Value**: Returns the first non-`NULL` expression in the list (or `NULL` if all are NULL)
* **Example**:
  ```sql
  SELECT COALESCE(NULL, NULL, 'Default Value'); -- Returns: 'Default Value'
  ```

##### `ISNULL(check_expression, replacement_value)` (T-SQL)
* **Syntax**: `ISNULL(check_expr, repl_val)`
* **Return Value**: If `check_expr` is NULL, returns `repl_val`; otherwise returns `check_expr`
* **Example**:
  ```sql
  SELECT ISNULL(balance, 0.00) FROM accounts;
  ```

##### `NULLIF(expression1, expression2)`
* **Syntax**: `NULLIF(v1, v2)`
* **Return Value**: Returns `NULL` if both expressions evaluate to equal values; otherwise returns `expression1`
* **Example**:
  ```sql
  SELECT 100.0 / NULLIF(total_items, 0) FROM cart; -- Prevents division-by-zero errors
  ```

##### `IIF(boolean_expression, true_value, false_value)`
* **Syntax**: `IIF(condition, val_true, val_false)`
* **Return Value**: Evaluates `condition` and returns `val_true` if true, or `val_false` if false
* **Example**:
  ```sql
  SELECT IIF(score >= 60, 'PASS', 'FAIL') FROM exams;
  ```

##### `CASE WHEN ... THEN ... ELSE ... END`
* **Syntax**:
  ```sql
  CASE 
      WHEN condition1 THEN result1
      WHEN condition2 THEN result2
      ELSE default_result
  END
  ```

---

#### 4. Mathematical Functions

* **`ABS(numeric_expr)`**: Computes absolute value. `ABS(-42)` ➔ `42`
* **`ROUND(numeric_expr, decimals)`**: Rounds to specified decimal places. `ROUND(123.456, 2)` ➔ `123.46`
* **`CEILING(numeric_expr)`**: Smallest integer greater than or equal to argument. `CEILING(4.1)` ➔ `5`
* **`FLOOR(numeric_expr)`**: Largest integer less than or equal to argument. `FLOOR(4.9)` ➔ `4`
* **`POWER(base, exponent)`**: Exponentiation. `POWER(2, 10)` ➔ `1024`
* **`SQRT(numeric_expr)`**: Square root. `SQRT(144)` ➔ `12`

---

#### 5. Conversion & Identifier Functions

##### `CAST(expression AS data_type)`
* **Syntax**: `CAST(expr AS target_type)`
* **Example**:
  ```sql
  SELECT CAST('42' AS INT), CAST(GETDATE() AS DATE);
  ```

##### `CONVERT(data_type, expression [, style])` (T-SQL)
* **Syntax**: `CONVERT(target_type, expr [, style])`
* **Example**:
  ```sql
  SELECT CONVERT(VARCHAR(20), GETDATE(), 120); -- Canonical ODBC style 'yyyy-mm-dd hh:mi:ss'
  ```

##### `NEWID7()` (RFC 9562 Chronological UUIDv7)
* **Syntax**: `NEWID7()`
* **Return Value**: 128-bit monotonically increasing UUID with 48-bit millisecond epoch timestamp in upper bits, preventing B+Tree index page splits.
* **Example**:
  ```sql
  SELECT NEWID7(); -- Returns: '0192a5b6-7c8d-7e9f-8a0b-1c2d3e4f5a6b'
  ```

---

#### 6. Aggregates & Window Functions

##### Aggregate Functions
* **`COUNT(*)` / `COUNT(col)`**: Aggregates total row count or non-NULL column count
* **`SUM(col)`**: Arithmetic sum of numeric column
* **`AVG(col)`**: Arithmetic mean of numeric column
* **`MIN(col)` / `MAX(col)`**: Minimum and maximum scalar values

##### Window Ranking & Offset Functions (`OVER (PARTITION BY ... ORDER BY ...)`)
* **`ROW_NUMBER()`**: Sequential integer starting from 1 within each window partition.
* **`RANK()`**: Same rank for ties, skipping subsequent ranks (`1, 2, 2, 4`).
* **`DENSE_RANK()`**: Same rank for ties, without skipping subsequent ranks (`1, 2, 2, 3`).
* **`LEAD(col [, offset [, default]])`**: Accesses a subsequent row relative to current row.
* **`LAG(col [, offset [, default]])`**: Accesses a preceding row relative to current row.

##### Quantile & Percentile Functions (`PERCENTILE_CONT`, `PERCENTILE_DISC`)
* **`PERCENTILE_CONT(percentile) WITHIN GROUP (ORDER BY sort_expression)`**:
  - Uses the T-Digest streaming algorithm to compute continuous linearly interpolated percentiles ($0.0 \sim 1.0$).
  - Example: `PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY response_time_ms)`

---

#### 7. System State & Metadata Functions

* **`DB_NAME()`**: Returns active session virtual database name (`'postgres'` or `'master'`).
* **`CURRENT_DATABASE()`**: ANSI standard current database name.
* **`USER_NAME()`**: Authenticated session user account name.
* **`@@IDENTITY`**: Last 64-bit integer identity generated by an `IDENTITY` column in current session.
* **`@@ROWCOUNT`**: Number of rows affected by the most recent statement.
* **`@@TRANCOUNT`**: Open transaction nesting depth in active session (`BEGIN TRAN` +1, `COMMIT` -1).
* **`@@ERROR`**: Internal error code of most recently executed statement (`0` on success).

---

## Chapter 6. System Catalogs Reference

While strictly adhering to the instance-level isolation invariant (Single Database per Process), LionSQL projects virtual system catalogs in memory to guarantee 100% transparent interoperability with ecosystem tools such as DBeaver, Beekeeper Studio, Navicat, SSMS, Azure Data Studio, psql, and sqlcmd.

---

### 6.1. MS SQL Server Compatible Catalog (`sys.*`)

Virtual catalog views queried by SSMS, Azure Data Studio, and sqlcmd when inspecting database structures, tables, and stored procedures.

#### 1. `sys.databases`
Provides metadata for the active database catalog.
| Column Name | Data Type | Description |
| :--- | :---: | :--- |
| `database_id` | `INT` | Unique database identifier (default: `1`) |
| `name` | `VARCHAR(128)` | Database name (`'master'`) |
| `compatibility_level`| `TINYINT` | Compatibility level (`150`: SQL Server 2019 compatibility) |
| `collation_name` | `VARCHAR(128)` | Default collation (`'Korean_Wansung_CI_AS'`) |
| `user_access_desc` | `VARCHAR(60)` | Access mode (`'MULTI_USER'`) |
| `is_read_only` | `BIT` | Read-only flag (`0`: Read/Write enabled) |

#### 2. `sys.tables`
Lists all user-defined physical tables registered in the database catalog.
| Column Name | Data Type | Description |
| :--- | :---: | :--- |
| `object_id` | `INT` | Unique object identifier of table |
| `name` | `VARCHAR(128)` | Physical table name |
| `schema_id` | `INT` | Schema ID (`1`: `dbo`) |
| `type` | `CHAR(2)` | Object type (`'U '`: User table) |
| `type_desc` | `VARCHAR(60)` | Object description (`'USER_TABLE'`) |
| `create_date` | `DATETIME` | Table creation timestamp |
| `modify_date` | `DATETIME` | Most recent schema modification timestamp |
| `is_user_table` | `BIT` | System vs user table flag (`1`: User table) |

#### 3. `sys.columns`
Provides data types and column attributes for all columns belonging to tables.
| Column Name | Data Type | Description |
| :--- | :---: | :--- |
| `object_id` | `INT` | Object ID of parent table |
| `name` | `VARCHAR(128)` | Column name |
| `column_id` | `INT` | 1-based column ordinal position |
| `system_type_id` | `TINYINT` | TDS system data type code (e.g. 56: INT, 167: VARCHAR) |
| `max_length` | `SMALLINT` | Maximum physical byte length |
| `precision` | `TINYINT` | Numeric total precision digits |
| `scale` | `TINYINT` | Numeric fractional scale digits |
| `is_nullable` | `BIT` | Nullability flag (`1`: Nullable, `0`: NOT NULL) |
| `is_identity` | `BIT` | Identity attribute flag (`1`: Auto-incrementing) |

#### 4. `sys.indexes` & `sys.index_columns`
Provides Primary Key and secondary B+Tree index structural metadata for tables.
* `object_id`: Target table object ID
* `name`: Index name (e.g. `PK_orders`, `idx_orders_date`)
* `index_id`: Index identifier (`1`: Clustered/PK, `2+`: Non-Clustered)
* `is_unique`: Unique index flag
* `is_primary_key`: Primary Key index flag
* `has_filter`: Filtered index flag

#### 5. `sys.procedures`
Provides object ID, name, and parameter signature details for all T-SQL stored procedures registered in the engine.

---

### 6.2. ANSI / ISO Standard Catalog (`INFORMATION_SCHEMA.*`)

Standard metadata views queried by ORM frameworks (Prisma, Hibernate, Dapper, GORM) and database management utilities.

#### 1. `INFORMATION_SCHEMA.TABLES`
* `TABLE_CATALOG`: `'postgres'` (PGWire) or `'master'` (TDS)
* `TABLE_SCHEMA`: `'public'` (PGWire) or `'dbo'` (TDS)
* `TABLE_NAME`: Table name
* `TABLE_TYPE`: `'BASE TABLE'` (physical table) or `'VIEW'` (virtual view)

#### 2. `INFORMATION_SCHEMA.COLUMNS`
* `TABLE_CATALOG`, `TABLE_SCHEMA`, `TABLE_NAME`: Owning table identifiers
* `COLUMN_NAME`: Column name
* `ORDINAL_POSITION`: 1-based column position
* `COLUMN_DEFAULT`: Default expression text
* `IS_NULLABLE`: `'YES'` or `'NO'`
* `DATA_TYPE`: Standard type identifier (`int`, `varchar`, `datetime`, etc.)
* `CHARACTER_MAXIMUM_LENGTH`: Maximum allowable string characters
* `NUMERIC_PRECISION`, `NUMERIC_SCALE`: Numeric precision and scale

#### 3. `INFORMATION_SCHEMA.KEY_COLUMN_USAGE` & `REFERENTIAL_CONSTRAINTS`
Accurately reports Primary Key columns and Foreign Key parent-child relationship definitions.

---

### 6.3. PostgreSQL Compatible Catalog (`pg_catalog.*`)

Internal system catalog views queried by `psql`, pgAdmin, and Beekeeper Studio during connection establishment.

#### 1. `pg_catalog.pg_database`
* `oid`: Database OID (`1`)
* `datname`: Database name (`'postgres'`)
* `datdba`: Owner OID (`10`)
* `encoding`: Encoding format (`6`: UTF-8)
* `datcollate`, `datctype`: Collation rules (`'C'`)

#### 2. `pg_catalog.pg_class`
* `oid`: Object OID
* `relname`: Table, index, or sequence name
* `relnamespace`: Schema namespace OID (`2200`: `public`)
* `relkind`: Object classification (`'r'`: regular relation table, `'i'`: index, `'v'`: view)
* `relpages`: Disk page count estimated by Volcano optimizer
* `reltuples`: Total record count statistics in table

#### 3. `pg_catalog.pg_attribute`
* `attrelid`: Table OID
* `attname`: Column name
* `atttypid`: PostgreSQL type OID (`23`: int4, `20`: int8, `1043`: varchar, `1114`: timestamp, etc.)
* `attnum`: 1-based column number (negative values for system columns)
* `attnotnull`: `NOT NULL` constraint flag

#### 4. `pg_catalog.pg_stat_activity`
Emits real-time connection status (PID, username, client IP, connection start time, active query text) for all connected sessions.

---

## Chapter 7. REST API Reference

LionSQL features an embedded high-performance HTTP server on port `:8090` providing metric monitoring, administrative authentication, remote SQL and stored procedure RPCs, zero-configuration RESTful CRUD, hot backups, and replication controls.

* **Default Base URL**: `http://localhost:8090`
* **Common Request Headers**:
  - `Content-Type: application/json`
  - `Authorization: Bearer <JWT_TOKEN>` (Mandatory for secure endpoints)

---

### 7.1. System & Real-Time Monitoring Endpoints

#### 1. `GET /api/health` (Health Check)
Returns engine status, running version, and active storage backend. Used for load balancers and Kubernetes liveness probes.
* **Authentication Required**: `No`
* **Response (200 OK)**:
  ```json
  {
    "status": "ok",
    "version": "0.90.0",
    "engine": "wiredtiger",
    "uptime_seconds": 15155,
    "timestamp": 1729326600
  }
  ```

#### 2. `GET /api/version` (Version & Build Metadata)
* **Authentication Required**: `No`
* **Response (200 OK)**:
  ```json
  {
    "name": "LionSQL",
    "version": "0.90.0",
    "go_version": "go1.24.0",
    "git_commit": "477a4cb6",
    "build_tags": ["wiredtiger"],
    "os_arch": "darwin/arm64"
  }
  ```

#### 3. `GET /api/metrics` (Prometheus Metrics Scrape)
Emits QPS, heap memory, connection pools, and transaction commit/conflict counters in standard Prometheus text format.
* **Authentication Required**: `No`
* **Response Header**: `Content-Type: text/plain; version=0.0.4`
* **Sample Output**:
  ```text
  # HELP lionsql_qps_current Current queries per second.
  # TYPE lionsql_qps_current gauge
  lionsql_qps_current 14250.00
  # HELP lionsql_active_connections Number of active client connections.
  # TYPE lionsql_active_connections gauge
  lionsql_active_connections 18
  # HELP lionsql_heap_bytes Current Go runtime heap alloc bytes.
  # TYPE lionsql_heap_bytes gauge
  lionsql_heap_bytes 149422080
  # HELP lionsql_transactions_committed_total Total committed ACID transactions.
  # TYPE lionsql_transactions_committed_total counter
  lionsql_transactions_committed_total 850420
  ```

#### 4. `GET /api/realtime` (Server-Sent Events Real-Time Stream)
Permanent unidirectional SSE stream transmitting CPU utilization, QPS, and memory governor metrics to the Web Studio dashboard every second.
* **Authentication Required**: `No`
* **Response Headers**: `Content-Type: text/event-stream`, `Cache-Control: no-cache`
* **Event Format**:
  ```text
  data: {"qps":12450,"heap_mb":142.5,"active_workers":8,"total_queries":184920}

  ```

---

### 7.2. Authentication & Session Management Endpoints

#### 1. `POST /api/auth/login` (Administrator Login)
Validates administrator email and password, issuing an HMAC-SHA256 signed JWT session token.
* **Authentication Required**: `No`
* **Request Body**:
  ```json
  {
    "email": "admin@lionsql.local",
    "password": "your_password"
  }
  ```
* **Response (200 OK)**:
  ```json
  {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in": 259200,
    "user": {
      "email": "admin@lionsql.local",
      "role": "admin"
    }
  }
  ```
* **Failure Response (401 Unauthorized)**:
  ```json
  {
    "error": "invalid email or password"
  }
  ```

#### 2. `POST /api/auth/refresh` (Session Token Renewal)
Renews an existing valid JWT session token.
* **Header**: `Authorization: Bearer <VALID_JWT>`
* **Response (200 OK)**: `{"token": "new_jwt_string...", "expires_in": 259200}`

---

### 7.3. Raw SQL & Stored Procedure Execution Endpoints

#### 1. `POST /api/sql` (Arbitrary SQL / T-SQL Execution)
Executes parameterized SQL queries or multi-statement batches, returning column metadata and structured result arrays.
* **Authentication Required**: `Yes` (`Bearer Token`)
* **Request Body**:
  ```json
  {
    "query": "SELECT customer_id, name, balance FROM customers WHERE balance >= @min_bal ORDER BY balance DESC;",
    "params": {
      "min_bal": 1000.00
    }
  }
  ```
* **Response (200 OK)**:
  ```json
  {
    "columns": ["customer_id", "name", "balance"],
    "column_types": ["INT", "VARCHAR", "DECIMAL"],
    "rows": [
      [101, "Acme Corp", 15420.50],
      [104, "Global Tech", 2400.00]
    ],
    "rows_affected": 2,
    "execution_time_ms": 0.42
  }
  ```

#### 2. `POST /api/procs/exec` (T-SQL Stored Procedure Remote RPC)
Invokes a compiled and cached VDBE stored procedure.
* **Authentication Required**: `Yes` (`Bearer Token`)
* **Request Body**:
  ```json
  {
    "name": "TransferMoney",
    "params": {
      "from_id": 101,
      "to_id": 102,
      "amount": 500.00
    }
  }
  ```
* **Response (200 OK)**:
  ```json
  {
    "procedure": "TransferMoney",
    "return_status": 0,
    "outputs": {
      "new_balance": 14920.50
    },
    "execution_time_ms": 0.85
  }
  ```

---

### 7.4. Zero-Configuration REST API (`/api/records/:table`)

PocketBase-style RESTful resource endpoints automatically activated upon table creation.

#### 1. `GET /api/records/:table` (List Records & Pagination)
* **Query Parameters**:
  - `page`: Page index (default: `1`)
  - `perPage`: Items per page (default: `30`, max: `500`)
  - `sort`: Sort criteria (ascending `col`, descending `-col`)
  - `filter`: Filter expression (e.g. `status = 'ACTIVE' AND balance > 500`)
* **Response (200 OK)**:
  ```json
  {
    "page": 1,
    "perPage": 30,
    "totalItems": 1250,
    "totalPages": 42,
    "items": [
      {"id": 1, "name": "Alice", "balance": 1500.00, "status": "ACTIVE"}
    ]
  }
  ```

#### 2. `POST /api/records/:table` (Create Record)
* **Request Body**:
  ```json
  {
    "name": "Bob",
    "balance": 2500.00,
    "status": "ACTIVE"
  }
  ```
* **Response (201 Created)**: Returns entire created record object (including auto-generated `id`).

#### 3. `PUT /api/records/:table` (Update Record)
* **Request Body**: JSON object containing Primary Key field (`id`) and fields to modify.

#### 4. `DELETE /api/records/:table` (Delete Record)
* **Request Body**: `{"id": 1}`

---

### 7.5. Administrative, Audit & Replication Control Endpoints

* **`GET /api/logs/requests`**: Returns the recent 500 entries from the circular request audit log buffer.
* **`GET /api/slow-queries`**: Returns history and execution duration of queries exceeding `-slow-threshold-ms`.
* **`POST /api/backup`**: Initiates streaming download of active database snapshot archive (`.tar.gz`).
* **`POST /api/restore`**: Restores database files from uploaded `.tar.gz` archive via `multipart/form-data`.
* **`GET /api/replication/status`**: Returns node replication role (`PRIMARY` or `REPLICA`), current LSN, and replication lag.
* **`POST /api/replication/promote`**: Instantly promotes a Replica node to Primary.

---

## Chapter 8. Wire Protocol Specifications

To ensure native interoperability with third-party client libraries and GUI clients, LionSQL implements two industry-standard wire protocols concurrently within a single executable process.

---

### 8.1. PostgreSQL Wire Protocol v3 Specification (`TCP :5433`)

PostgreSQL wire frames start with a 1-byte message type identifier followed by a 4-byte big-endian packet length field:
```
+-------------+----------------+-------------------------------+
| Type (1B)   | Length (4B)    | Payload Data (Length - 4)     |
+-------------+----------------+-------------------------------+
```

#### 1. Connection Handshake
1. **`SSLRequest` (`code: 80877103`)**: Client probes whether SSL encryption is available. Server replies with 1 byte: `'N'` (plain-text) or `'S'` (SSL supported).
2. **`StartupMessage` (`proto: 196608` [v3.0])**: Client passes `user`, `database` (`'postgres'`), and `client_encoding` (`'UTF8'`).
3. **`AuthenticationCleartextPassword` (`'R'`, code `3`)** or **`AuthenticationOk` (`'R'`, code `0`)**: Confirms password verification.
4. **`ParameterStatus` (`'S'`)**: Transmits `server_version=14.0 (LionSQL v0.90.0)`, `client_encoding=UTF8`, `standard_conforming_strings=on`.
5. **`ReadyForQuery` (`'Z'`, status `'I'`)**: Signals ready state for next transaction block.

#### 2. Simple Query Protocol
* **`Query` (`'Q'`)**: Submits raw plain-text SQL query string.
* **`RowDescription` (`'T'`)**: Column count, names, table OID, type OID (INT4: 23, VARCHAR: 1043), format code (0: Text).
* **`DataRow` (`'D'`)**: Column byte length (4B) followed by raw text bytes.
* **`CommandComplete` (`'C'`)**: Completion tag (`SELECT 10`, `INSERT 0 1`).
* **`ReadyForQuery` (`'Z'`)**: Query finished; ready for next command.

#### 3. Extended Query Protocol
Reuses prepared statements and cached execution plans:
* **`Parse` (`'P'`)**: Compiles SQL into VDBE bytecode and caches execution statement.
* **`Bind` (`'B'`)**: Binds parameter arguments ($1, $2) to portal.
* **`Describe` (`'D'`)**: Requests result set metadata.
* **`Execute` (`'E'`)**: Executes Volcano iterator up to `max_rows`.
* **`Sync` (`'S'`)**: Commits transaction boundaries and emits `ReadyForQuery`.

---

### 8.2. Microsoft SQL Server TDS 7.x / 8.0 Protocol Specification (`TCP :1433`)

TDS frames maintain an **8-byte Common Header** at the start of every packet:

```
+-----------+------------+-------------+--------------+--------------+-------------+
| Type (1B) | Status (1B)| Length (2B) | Channel (2B) | PacketID (1B)| Window (1B) |
+-----------+------------+-------------+--------------+--------------+-------------+
```

* **Core Packet Types**:
  - `0x01` (`SQLBatch`): Standard T-SQL batch statement.
  - `0x03` (`RPC`): Remote stored procedure call (`sp_prepexec`, `sp_executesql`).
  - `0x04` (`TabularResult`): Tabular result set token stream emitted to client.
  - `0x06` (`Attention`): Client cancellation request.
  - `0x0E` (`TransactionManager`): Distributed transaction or isolation level synchronization.
  - `0x10` (`Login7`): User authentication and session initialization token.
  - `0x12` (`PreLogin`): Client-server pre-negotiation for encryption, versioning, and TLS handshake.

* **Core Data Stream Tokens**:
  - **`COLMETADATA (0x81)`**: Result set column schemas (User Type ID, Flags, Data Type, Column Name).
  - **`ROW (0xD1)`**: Standard row data (field byte lengths and raw values).
  - **`NBCROW (0xD2, Null Bitmap Compressed Row)`**:
    Optimized compressed row layout prepending an $\lceil N/8 \rceil$ byte null bitmap, completely omitting data bytes for `NULL` fields.
  - **`ENVCHANGE (0xE3)`**:
    Session environment change notification (synchronizes database context to `'master'`, packet size adjustments).
  - **`DONE (0xFD)` / `DONEPROC (0xFE)`**:
    Command or procedure batch completion token accompanied by affected row counts (64-bit integer).
  - **`ERROR (0xAA)` / `INFO (0xAB)`**:
    Emits error number, severity, state, and human-readable diagnostic message.

---

## Chapter 9. Error Codes & Diagnostics

### 9.1. LionSQL Standard Internal Error Codes & ANSI SQLSTATE Mapping

| Internal Error Code | ANSI SQLSTATE | TDS Error | Standard English Message | Cause & Recommended Action |
| :--- | :---: | :---: | :--- | :--- |
| `ERR_TABLE_NOT_FOUND` | `42P01` | `208` | `table does not exist` | Referenced table does not exist. Check table name spelling and schema qualification |
| `ERR_COLUMN_NOT_FOUND` | `42703` | `207` | `column does not exist` | Invalid column reference. Check table schema definition |
| `ERR_UNIQUE_VIOLATION` | `23505` | `2627` | `unique constraint violation: duplicate key` | Insertion of duplicate key on PRIMARY KEY or UNIQUE index |
| `ERR_WRITE_CONFLICT` | `40001` | `1205` | `serialization failure: write-write conflict` | OCC or MVCC concurrent write-write conflict. Client retry required |
| `ERR_FOREIGN_KEY` | `23503` | `547` | `foreign key violation: referenced key not found` | Foreign key referential constraint violation. Parent row missing |
| `ERR_MEMORY_BUDGET` | `53200` | `701` | `memory budget exceeded: ... > work_mem` | Operator memory exceeded `work_mem`. Add filters or increase work_mem |
| `ERR_STORAGE_FULL` | `53200` | `1105` | `table data limit exceeded: memory limit exceeded` | In-memory engine reached heap allocation ceiling (`--max-memory`) |
| `ERR_SYNTAX_ERROR` | `42601` | `102` | `syntax error in SQL statement` | Grammatical or syntax error in SQL query |
| `ERR_AUTH_FAILED` | `28P01` | `18456` | `password authentication failed` | Administrator email or password mismatch |
| `ERR_CONNECTION_LOST` | `08006` | `10054` | `connection failure: client aborted` | Client socket prematurely disconnected or timed out |

---

### 9.2. TDS Error Severity Levels

* **Severity 1 ~ 10 (Informational & Warnings)**:
  - `PRINT` output, non-fatal operational notifications. Connection remains active.
* **Severity 11 ~ 16 (User Execution Errors)**:
  - Constraint violations, syntax errors, `RAISERROR` user exceptions. Aborts active SQL batch; connection remains open.
* **Severity 17 ~ 19 (Resource Exhaustion Errors)**:
  - Exceeded `work_mem`, memory governor throttle. Active transaction is rolled back.
* **Severity 20 ~ 25 (System Fatal Errors)**:
  - Storage I/O failure, internal corruption. Client connection is forcefully closed immediately.

---

### 9.3. PostgreSQL Error Severity

* **`NOTICE`**: DDL automatic cascade notifications, informational status messages.
* **`WARNING`**: Warnings such as rollback attempts outside transaction blocks.
* **`ERROR`**: Query failure, constraint violation (transitions transaction to Aborted state, requiring explicit `ROLLBACK`).
* **`FATAL`**: Authentication failure, administrative disconnect (terminates client socket).
* **`PANIC`**: System-level emergency shutdown (aborts entire server process).

---

## Chapter 10. Appendix: System Architecture & Technical Documentation Index

#### 10.1. Architecture & Storage Internals
* [`ARCHITECTURE.md`](ARCHITECTURE.md)
  - **Comprehensive Architectural Blueprint**: Dual storage engines (In-Memory OCC vs WiredTiger B+Tree), dual-parser strategy (Pratt fast-path + ANTLR4 fallback), VDBE bytecode virtual machine, high-performance goroutine connection pooler, multi-wire protocol single-binary unified architecture.
* [`internal/STORAGE.md`](internal/STORAGE.md)
  - **Storage Engine Interface Specification**: Core contract definitions for `Engine`, `Tx`, and `Cursor`. Concurrent SkipList in-memory indexing, WiredTiger B+Tree page layouts, checkpoints, and WAL (Write-Ahead Logging) durability design.
* [`internal/RECORD_BINARY_FORMAT.md`](internal/RECORD_BINARY_FORMAT.md)
  - **Binary Tuple Serialization Specification**: Schema-based compressed binary tuple format (`pkg/storage/tuple.go`). `0x01` version header, $\lceil N/8 \rceil$ null bitmap, ZigZag variable-length integer encoding, and disk/WAL space reduction design.
* [`internal/TRANSACTION_CONCURRENCY.md`](internal/TRANSACTION_CONCURRENCY.md)
  - **Transaction Isolation & Concurrency Control**: Non-blocking Snapshot Isolation and Read Committed models. In-memory OCC version validation, WiredTiger MVCC hazard pointers, and VDBE transaction coupling (`exec.RunInTx`) conventions.
* [`DATA_DICTIONARY.md`](DATA_DICTIONARY.md)
  - **Data Dictionary & System Catalogs**: Schema metadata persistence layout, system tables, and virtual views (`sys.*`, `INFORMATION_SCHEMA.*`, `pg_catalog.*`) mapping and resolution principles.
* [`HIGH_AVAILABILITY.md`](HIGH_AVAILABILITY.md)
  - **High Availability & Zero-Downtime Backup/Restore**: Crash safety guarantees, LSN-based WAL streaming 1-Writer Multi-Reader replication, zero-downtime Primary promotion, and online hot backup (`.tar.gz`) pipelines.
* [`ADR.md`](ADR.md)
  - **Architectural Decision Records**: Key design decisions, tradeoffs, and architectural rationales (ADR-001 through ADR-021) covering VDBE integration, dual-parser fallback, single-binary distribution, single-writer replication, compact tuple codec, and multi-protocol catalog interception.

#### 10.2. Query Engine & VM Internals
* [`internal/VDBE.md`](internal/VDBE.md)
  - **Bytecode Virtual Machine (VDBE)**: SQLite-style register-based VDBE virtual machine architecture. Core opcode specifications, `sync.Pool` register recycling, hot-loop optimizations, and compiled plan cache (`*runtime.PlanCache`) lifecycle.
* [`internal/CBO_VOLCANO.md`](internal/CBO_VOLCANO.md)
  - **Cost-Based Optimizer & Volcano Iterator**: Volcano Iterator pipeline, Cost-Based Optimizer (CBO), 64-bucket equi-height histogram selectivity modeling, constant folding, predicate pushdown, and external merge sort spilling upon exceeding `work_mem`.
* [`TSQL_REFERENCE.md`](TSQL_REFERENCE.md)
  - **T-SQL Syntax & Grammar Comprehensive Reference**: Microsoft SQL Server compatible stored procedures, scalar and inline table-valued functions (TVFs), DML triggers (`INSERTED`/`DELETED`), control-of-flow statements (`IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO`), and cursor mechanics.
* [`EXTENSIONS.md`](EXTENSIONS.md)
  - **SQL Extensions & UDF Architecture**: Window functions (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), CTEs (Common Table Expressions), cascading foreign keys, Go native SPI, and dual WebAssembly (Wasm) user-defined function extension specifications.
* [`PERCENTILE_ANALYTICS.md`](PERCENTILE_ANALYTICS.md)
  - **Percentile & Streaming Quantile Analytics Functions**: `PERCENTILE_CONT`, `PERCENTILE_DISC`, ANSI `WITHIN GROUP (ORDER BY ...)`, `MEDIAN`, and T-Digest based streaming approximate quantile (`APPROX_PERCENTILE`) algorithm specifications.
* [`LOG_PROCESSING_GUIDE.md`](LOG_PROCESSING_GUIDE.md)
  - **Engine-Native Log & Text Processing Operators**: Unix-pipeline-friendly `GREP`, `AWK`, and `SPLIT_PART` operators, zero-copy pushdown regex filtering, and unstructured log analytics pipelines.

#### 10.3. Protocols & Tool Integration
* [`internal/WIRE_PROTOCOLS.md`](internal/WIRE_PROTOCOLS.md)
  - **Wire Protocol Implementation Details**: PostgreSQL Wire Protocol v3 (port 5433) simple/extended packet handling and MS SQL Server TDS 7.x/8.0 (port 1433) Prelogin, Login7, RPC, and `sp_prepexec` plan cache integration.
* [`CLIENT_TOOL_INTEGRATION.md`](CLIENT_TOOL_INTEGRATION.md)
  - **GUI/CLI Tool Integration & Catalog Interception**: Integration guide for Beekeeper Studio, DBeaver, DbGate, Azure Data Studio, SSMS, `psql`, and `sqlcmd`. Per-tool catalog interception, pagination translation, and language driver connection string recipes.

#### 10.4. BaaS Features, Web SDK & REST API
* [`REST_API_RECORDS_GUIDE.md`](REST_API_RECORDS_GUIDE.md)
  - **Zero-Configuration Automatic REST API**: PocketBase-style automated `/api/records/:table` CRUD endpoints, URL query operators, sorting, and pagination filtering specifications.
* [`WEBHOOKS_AND_RLS.md`](WEBHOOKS_AND_RLS.md)
  - **Webhook Dispatcher & Row-Level Security (RLS)**: Asynchronous worker pools, HMAC-signed webhook delivery, and declarative table row-level security policies with `@auth.id` and `@auth.role` token substitutions.
* [`CRON_AND_MAILER.md`](CRON_AND_MAILER.md)
  - **Embedded Cron Scheduler & Transaction Mailer**: Automated SQL/T-SQL batch scheduling via standard 5-field cron expressions and embedded HTML SMTP transactional email dispatcher with circular audit log buffers.
* [`WEB_SDK.md`](WEB_SDK.md)
  - **Universal Web SDK & Frontend Integration**: Browser-embedded single-file library (`lionsql.js`, `lionsql.d.ts`), vanilla JavaScript, Server-Sent Events (SSE) live updates, and HTMX integration recipes.
* [`REST_API_REFERENCE.md`](REST_API_REFERENCE.md)
  - **HTTP REST & SSE API Reference**: Detailed endpoints for system health, Prometheus metrics, JWT authentication, table CRUD, stored procedure RPCs, backups/restores, and replication streaming.

#### 10.5. Operations, Security, Performance & Troubleshooting
* [`TRANSACTION_PERFORMANCE_GUIDE.md`](TRANSACTION_PERFORMANCE_GUIDE.md) (Korean: [`TRANSACTION_PERFORMANCE_GUIDE.ko.md`](TRANSACTION_PERFORMANCE_GUIDE.ko.md))
  - **Transaction Performance & Workload Option Tuning**: 4 major workload profiles from in-memory OCC to zero-loss financial ledgers, Fsync Group Commit mechanics, deployment recipes per hardware tier, and operational diagnostic checklists.
* [`OPERATIONS.md`](OPERATIONS.md)
  - **Production Operations & Deployment Guide**: System prerequisites, daemon supervision, zero-downtime reload, graceful shutdown, and memory governor tuning.
* [`METRICS.md`](METRICS.md)
  - **Monitoring & Metrics**: Prometheus-compatible `/metrics` endpoint specifications (QPS, latency, connection pools, heap allocations) and slow-query logging diagnostics.
* [`SECURITY.md`](SECURITY.md)
  - **Security Architecture & Access Control**: Built-in administrator account, JWT lifecycle, API key scoped roles, and TLS/SSL transport layer encryption.
* [`BENCHMARK.md`](BENCHMARK.md)
  - **Performance Benchmarks & Verification Methodology**: TPC-C benchmark measurements, pgbench results, and T-SQL stored procedure microbenchmark throughput (TPS) and latency profiles.
* [`DATA_IMPORT_AND_MIGRATION.md`](DATA_IMPORT_AND_MIGRATION.md)
  - **Heterogeneous Database Migration Guide**: Step-by-step procedures to migrate schemas and records from PostgreSQL, Microsoft SQL Server, SQLite, and PocketBase into LionSQL.
* [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
  - **Diagnostic & Troubleshooting Guide**: Resolving frequent issues including port collisions, permission errors, protocol handshake failures, transaction serialization conflicts (`40001`), and client cache invalidation.

#### 10.6. Developer Context, Backlog & Engineering Principles
* [`ROADMAP.md`](../ROADMAP.md)
  - Milestone achievements across versions (Phases 1 through 6) and future strategic roadmaps.
* [`TODO.md`](../TODO.md) (Korean: [`TODO.ko.md`](../notes/archive/TODO.ko.md))
  - Operational engineering dashboard tracking prioritized tasks (P1, P2, P3), CBO metrics, domain backlogs, and recent milestone status.
* [`CHANGELOG.md`](../CHANGELOG.md)
  - Release changelogs documenting feature additions, fixes, and refactoring across versions following Keep a Changelog standards.
* [`notes/ENGINEERING_CONTEXT.ko.md`](../notes/ENGINEERING_CONTEXT.ko.md)
  - Architectural invariants, detailed spec-out rationales, and collaborative engineering principles.
* [`notes/PITFALLS_AND_TRICKS.md`](../notes/PITFALLS_AND_TRICKS.md)
  - Engineering caveats covering circular import prevention, WiredTiger build tags, sandbox network isolation, and decimal JSON serialization.
