# Changelog

All notable changes to SQLion are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- **Storage & DDL: `TRUNCATE TABLE <table>` Engine (`pkg/storage`, `pkg/storage/memengine`, `pkg/storage/wiredtiger`, `pkg/api`)**:
  - Added `TruncateTable(tableName string) error` method across `storage.Engine` interface, `memengine`, and `wiredtiger`.
  - Implemented instant O(1) in-memory record, version, and index resets in `memengine` with foreign key dependency safeguards.
  - Added WAL logging and recovery replay for `OpTruncateTable` guaranteeing durability across server restarts.
  - Implemented native `WT_SESSION::truncate` in WiredTiger with automatic drop & recreate fallback for tables and secondary indexes.
  - Added comprehensive test coverage in `pkg/storage/memengine/memengine_test.go` and `pkg/api/sqlexec_test.go` (`TestSQLExecutor_TruncateTable`).
- **T-SQL `SELECT ... INTO #temp_table / table` Engine (`pkg/api`)**:
  - Implemented dynamic table creation and population via `SELECT <projections> INTO <target_table> FROM <source> [WHERE ...]`.
  - Added intelligent column type and primary key inference from source schemas and evaluated expression result sets.
  - Supported local temporary tables (`#table`), global temporary tables (`##table`), and permanent tables.
  - Integrated session-scoped lifecycle tracking for `#temp` tables with automatic teardown on `CloseSession`.
  - Added comprehensive test coverage in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_SelectInto`).
- **T-SQL Table Variables `DECLARE @table TABLE (...)` Engine (`pkg/api`)**:
  - Implemented session-scoped table variable declaration syntax `DECLARE @var TABLE (colDef, ...)`.
  - Added physical storage binding via isolated session-namespaced tables (`__tv_<sessionID>_<var>`).
  - Integrated dynamic query rewriter substituting `@var` references across `INSERT`, `SELECT`, `UPDATE`, `DELETE`, and `JOIN` clauses.
  - Implemented automatic lifecycle cleanup on `CloseSession`, dropping all associated physical tables from the storage engine.
  - Added comprehensive multi-session isolation unit tests in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_TableVariables`).
- **Wire Protocols: `TCP_NODELAY` & Socket Keep-Alive Optimization (`pkg/wire`, `pkg/wire/tds`)**:
  - Explicitly enabled `SetNoDelay(true)` across PostgreSQL Wire Protocol v3 and Microsoft SQL Server TDS 7.x/8.0 listeners, disabling Nagle's algorithm and eliminating up to 40ms delayed-ACK packet latency.
  - Enabled active TCP keep-alive with 30-second interval probe lifecycle preventing stale socket accumulation.
- **In-Memory OCC Engine: Fine-Grained Table-Level Striped Locking (`pkg/storage/memengine`)**:
  - Replaced single coarse-grained engine mutex in `Commit()` with per-table read/write mutexes (`TableData.mu`).
  - Implemented deadlock-free canonical lock acquisition (sorted table names) allowing concurrent transactions modifying disjoint tables to commit in parallel.
  - Slashed read blocking during commit phases across `getRecordAndVerUnderLock`, `Scan`, and `IndexScan`.
  - Skyrocketed In-Memory TPC-C performance from 892k to **1,251,489 tpmC (+40.1%)** and **43,832 TPS (+30.9%)**, with New-Order commit success rate reaching **99.8%** and average latency plummeting to **0.20ms**.
- **In-Memory OCC Engine: `cloneRecord` & Heap Allocation Optimization (`pkg/storage/memengine`)**:
  - Eliminated redundant double map cloning on row updates by retrieving unallocated snapshots in `getRecordAndVerUnderLock(tableName, pk, false)` and sizing updated maps in a single allocation.
  - Eliminated map cloning on `deleteLocked` for foreign key cascade checks.
  - Reused transaction-local staged records directly without defensive copies when re-read in the same transaction.
  - Elevated In-Memory TPC-C performance from 802k to **892,975 tpmC (+11.3%)** and **33,480 TPS (+11.0%)** while slashing sub-millisecond row latency (0.93ms New-Order, 0.36ms Payment).
- **WiredTiger Engine: High-Performance Binary Record Codec (`pkg/storage/wiredtiger`)**:
  - Replaced reflection-based `json.Marshal` and `json.Unmarshal` with MessagePack binary serialization and direct CGO raw pointer passing (`WT_ITEM`, `value_format=u`).
  - Implemented zero-allocation `C.go_wt_cursor_set_val_raw` and `C.go_wt_cursor_get_val_raw`, eliminating string conversion and libc `malloc`/`free` on record writes.
  - Added seamless JSON fallback in `decodeRecord` ensuring backwards compatibility with existing table data.
  - Boosted TPC-C tpmC from 386k to **507,456 tpmC (+31.2%)** and overall TPS to **17,294 TPS (+33.0%)** while dropping p95 latency.
- **WiredTiger Engine: Cursor Caching & Reuse Optimization (`pkg/storage/wiredtiger`)**:
  - Implemented per-session table cursor caching (`s.cursors map[string]*C.WT_CURSOR`) in `WTSession`.
  - Replaced repetitive per-operation `open_cursor` and `cursor_close` allocations with `cursor->reset` recycling.
  - Added deterministic cursor resetting on transaction `Commit` and `Rollback`, and batch teardown on `WTSession.Close`.
  - Slashed CGO allocation overhead, improving TPC-C tpmC from 370k to 386k+ and reducing average row mutation latency.
- **Database Consistency & Diagnostics Engine: `DBCC` (`pkg/api`)**:
  - Implemented `DBCC USEROPTIONS`: inspects and returns active session settings (isolation level, textsize, lock_timeout, quoted_identifier, ansi_nulls).
  - Implemented `DBCC CHECKTABLE ('table_name')`: scans table records, validates primary key uniqueness, secondary index point-lookup consistency, and reports allocation/data integrity findings.
  - Implemented `DBCC CHECKDB`: runs consistency verification across all user tables and indexes in the catalog.
  - Implemented `DBCC SHRINKDATABASE ('db_name')`: reports database file allocation, current page counts, and estimated pages.
  - Added comprehensive test coverage in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_DBCCCommands`).
- **T-SQL Control Flow: `GOTO` & Labels Engine (`pkg/proc`, `pkg/proc/runtime`)**:
  - Added lexical tokens `TokenGoto` and `TokenColon` (`:`) with AST node definitions `GotoStmt` and `LabelStmt`.
  - Implemented 2-pass jump resolution in `Compiler` supporting forward and backward jumps with `OpJump`.
  - Added compile-time validation detecting undefined target labels and rejecting duplicate label definitions.
  - Added comprehensive bytecode compilation and VM execution regression tests in `pkg/proc/compiler_test.go` and `pkg/proc/runtime/regression_test.go`.
- **T-SQL Global Temporary Tables `##` Engine (`pkg/api`)**:
  - Implemented cross-session global temporary tables prefixed with `##` (`CREATE TABLE ##table (...)`).
  - Added shared table access, mutations, and query planning across multiple concurrent client sessions with case-insensitive naming parity.
  - Implemented session lifecycle garbage collection in `CloseSession`, automatically dropping global temporary tables when their creator session disconnects.
  - Supported explicit `DROP TABLE ##table` and integration with `sp_help` and catalog introspection.
  - Added comprehensive multi-session concurrency tests in `pkg/api/sqlexec_test.go`.
- **T-SQL `@@ERROR` Status Tracking & `SET IDENTITY_INSERT` Engine (`pkg/storage`, `pkg/api`, `pkg/proc/runtime`)**:
  - Added session-level `@@ERROR` tracking that resets to 0 on successful statements and sets to 50000 on execution or DML errors.
  - Implemented `Identity` column metadata in `storage.Column` and support for `IDENTITY(seed, incr)` and `AUTO_INCREMENT` in `CREATE TABLE`.
  - Implemented `SET IDENTITY_INSERT [schema.]table ON|OFF` session controls with validation against non-identity tables and single-table session mutual exclusion.
  - Added identity insertion enforcement in `INSERT INTO`, auto-generating monotonic IDs and updating watermark on explicit inserts.
  - Added evaluation support for `@@IDENTITY`, `SCOPE_IDENTITY()`, and `ERROR_NUMBER()`.
  - Added comprehensive unit and regression tests in `pkg/api/sqlexec_test.go`.
- **Built-in System Stored Procedures (`pkg/api`)**:
  - Implemented core administrative stored procedures: `sp_help`, `sp_spaceused`, `sp_who`, `sp_who2`, `sp_lock`, and `sp_rename`.
  - Added object and column detail introspection in `sp_help [object_name]`.
  - Added database and table row count/storage space usage reporting in `sp_spaceused [object_name]`.
  - Added active connection and transaction monitor in `sp_who` / `sp_who2`, and lock status inspection in `sp_lock`.
  - Added atomic table and column renaming utility with schema and data migration in `sp_rename 'old_name', 'new_name' [, 'COLUMN']`.
  - Direct execution support with or without `EXEC` prefix (`EXEC sp_help` and `sp_help`).
- **DBeaver & Beekeeper Studio GUI Client Introspection Compatibility (`pkg/api`)**:
  - Implemented table-less scalar `SELECT` evaluation for session functions and literal expressions (`SELECT 1`, `current_database()`, `current_schema()`, `version()`, `now()`, `pg_backend_pid()`, `@@VERSION`, `@@SPID`, `@@TRANCOUNT`, `@@ROWCOUNT`).
  - Added support for comprehensive T-SQL and PostgreSQL session configuration options in `SET` (`SET NOCOUNT ON`, `SET ANSI_NULLS ON`, `SET QUOTED_IDENTIFIER ON`, `SET TRANSACTION ISOLATION LEVEL ...`, `SET TIME ZONE ...`, `SET NAMES ...`, `SET extra_float_digits ...`).
  - Added multi-word and system parameters to `SHOW` (`SHOW search_path`, `SHOW transaction_isolation`, `SHOW timezone`, `SHOW server_version`, `SHOW max_connections`).
  - Added connection pool lifecycle commands (`DISCARD ALL`, `RESET ALL`, `DEALLOCATE ALL`, `UNLISTEN *`).
  - Added virtual catalog views for PostgreSQL inspection (`pg_catalog.pg_database`, `pg_catalog.pg_namespace`, `pg_catalog.pg_tables`, `pg_catalog.pg_class`, `pg_catalog.pg_type`, `pg_catalog.pg_attribute`, `pg_catalog.pg_roles`, `pg_catalog.pg_am`, `pg_catalog.pg_settings`).
  - Added virtual catalog views for SQL Server inspection (`sys.schemas`, `sys.objects`, `sys.views`, `sys.types`), with enhanced column aliases and bracketed/quoted schema normalization (`[dbo].[accounts]`, `[sys].[tables]`, `"pg_catalog"."pg_tables"`).
- **TPC-C Standard OLTP Benchmark Driver (`cmd/tpcc`, `pkg/storage/memengine`)**:
  - Implemented in-tree TPC-C benchmark suite modeling the full 9 standard relational tables (`warehouse`, `district`, `customer`, `history`, `new_order`, `orders`, `order_line`, `item`, `stock`).
  - Implemented standard TPC-C workload mix (45% New-Order, 43% Payment, 4% Order-Status, 4% Delivery, 4% Stock-Level).
  - Added configurable retry policy with exponential backoff and jitter (`-max-retries=3`) recovering from serialization and MVCC write-write lock contention.
  - High-precision metric measurement: official **tpmC** (New-Order Transactions Per Minute), overall **TPS** (Transactions Per Second), retry recovery statistics, and latency distribution percentiles (Min, Avg, p50, p90, p95, p99, Max).
  - Multi-engine verification with retries: achieves **802,542 tpmC (30,168 TPS)** on In-Memory OCC and **370,641 tpmC (12,460 TPS)** on WiredTiger B+Tree MVCC with 91.5% New-Order commit rate.
  - Hardened OCC record isolation in `memengine` to return cloned records on `tx.Get()` preventing concurrent map iteration/mutation data races.
- **PostgreSQL Wire Protocol SSL/TLS Encryption & Handshake (`pkg/wire`, `cmd/server`)**:
  - Implemented full PostgreSQL Wire v3 SSL/TLS encryption handshake (`SSLRequest` code `80877103` and `GSSENCRequest` code `80877104`).
  - Added support for both loaded X.509 certificate pairs (`--tls-cert`, `--tls-key`) and zero-config self-signed certificate generation (`pkg/wire/tls.go`).
  - Configurable strict encryption enforcement (`--require-ssl`) returning standard PostgreSQL FATAL ErrorResponse (`28000`) for unencrypted connections.
  - Seamless socket upgrade from plaintext to TLS stream with full protocol support (AuthenticationOk, ParameterStatuses, simple queries, extended queries, COPY).
  - Comprehensive unit and integration test suite (`pkg/wire/tls_test.go`).
- **JSON & JSONB Data Types with Extraction Operators (`pkg/storage`, `pkg/proc`, `pkg/execution`, `pkg/planner`, `pkg/api`)**:
  - Full ANSI/PostgreSQL/MSSQL compatible `JSON` and `JSONB` data type support (`storage.TypeJSON = 6`) with schema validation and syntax enforcement.
  - PostgreSQL extraction operators `->` (extract element as JSON string) and `->>` (extract element as unquoted text) with multi-level path chaining (e.g. `data -> 'user' ->> 'name'`).
  - Standard scalar and document extraction functions: `JSON_VALUE(doc, path)` (scalar extraction) and `JSON_QUERY(doc, path)` (object/array extraction) with support for standard dot-notation and array index paths (`$.user.scores[0]`).
  - Native Pratt parser and AST integration (`TokenJSONExtract`, `TokenJSONExtractText`) with tight operator precedence, enabling expressions in `SELECT` projections, `WHERE` / `HAVING` filters, `ORDER BY`, and stored procedures.
  - Pre-parsed AST caching in Volcano `ProjectionIterator` and `FilterIterator` for high-throughput zero-reparsing query evaluation.
  - Dedicated storage and API integration test suites (`pkg/storage/json_test.go`, `pkg/api/json_test.go`).
- **ANSI/ISO SQL & T-SQL Window Functions Engine (`pkg/planner`, `pkg/execution`, `pkg/api`)**:
  - Full relational window functions support integrated into Volcano execution pipeline: `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`, and partition-level window aggregates (`SUM`, `AVG`, `MIN`, `MAX`, `COUNT`).
  - Full syntax compatibility with `OVER ([PARTITION BY col1, col2, ...] [ORDER BY col [ASC|DESC], ...])` and projection aliases (`AS rn`).
  - Physical `WindowIterator` operator with partition grouping, multi-key in-partition sorting, tie-aware ranking, forward/backward offset peeking, and zero-allocation $O(1)$ `ColumnIndexMap` expansion.
  - Seamless execution inside Common Table Expressions (CTEs) and subqueries.
  - Comprehensive unit and integration test suite (`pkg/api/window_test.go`).
- **Foreign Key Constraints & Referential Integrity Cascading Engine (`pkg/storage`, `pkg/storage/memengine`, `pkg/api`)**:
  - Full relational referential integrity support with table-level, named, and column-inline `FOREIGN KEY ... REFERENCES ... [ON DELETE <action>] [ON UPDATE <action>]` syntax in `CREATE TABLE`.
  - Referential actions supported: `CASCADE`, `SET NULL`, `RESTRICT`, and `NO ACTION` across parent deletions and updates.
  - ANSI SQL MATCH SIMPLE compliant NULL-handling for composite and single foreign keys.
  - Dependency protection in `DROP TABLE` preventing accidental dropping of tables referenced by active foreign key constraints with `ErrDependentObjectsExist`.
  - Re-keying OCC mutations on parent primary key updates with bounded recursion depth (<= 16) preventing cyclic deadlock or stack overflow.
  - Comprehensive unit and integration test coverage (`pkg/api/foreign_key_test.go`).
- **Zero-Allocation Volcano Relational Execution Engine & Slice-Backed Tuple Pipeline (`pkg/execution`)**:
  - Refactored `execution.Tuple` from dynamic `map[string]any` to contiguous slice-backed struct (`Values []any`, `Index *ColumnIndexMap`), eliminating map bucket allocation on query execution hot paths.
  - Implemented precomputed `ColumnIndexMap` with $O(1)$ column offset lookups supporting case-insensitive table/alias prefixes and raw aggregate expressions (`COUNT(*)`, `AVG(col)`).
  - Extended `Iterator` interface with `ColIndexMap() *ColumnIndexMap` and introduced `MergeColumnIndexMaps` for seamless, zero-allocation schema and prefix propagation across join trees (`HashJoin`, `IndexNestedLoopJoin`) and upstream projection/sort/filter/aggregate operators.
  - Integrated `sync.Pool` buffer pooling (`AcquireValuesSlice` / `ReleaseValuesSlice`) to mitigate GC pressure during high-throughput query iteration.
  - Added `ToEvalMap()` for AST boolean expression evaluation and lossless JSON serialization for external merge sort spill runs.
- **Pure-Go Concurrent SkipList Secondary Index (`pkg/storage/memengine`)**:
  - High-concurrency, lock-aware SkipList indexing replacing $O(N)$ slice copy element shifting with $O(\log N)$ probabilistic skip links.
  - Sub-microsecond point lookup (~177ns/op) and bulk insertion (~584ns/op, 2.8M ops/sec).
  - Unbounded and bounded range scans (`ScanRange`) via Level-0 forward pointer iteration.
  - Seamless snapshot checkpoint serialization (`ToEntries` / `FromEntries`) preserving MessagePack and JSON backward compatibility.
- **Zero-Copy Cursor Tuple Iterator (`pkg/storage/memengine/memengine.go`)**:
  - Eliminated eager full-table clone allocations (`cloneRecord`) during `Scan()` and `IndexScan()`, switching to direct immutable record memory views.
  - Reduced per-row memory allocations to zero on table scan hot paths, cutting GC pressure and query latency.
- **T-SQL Built-in Function Library & VDBE System Variables (`pkg/proc/builtins.go`)**:
  - Centralized single-source-of-truth scalar function evaluator shared identically across VDBE Bytecode VM and Volcano relational expressions.
  - Date & Time functions: `DATEDIFF`, `DATEADD`, `DATEPART`, `YEAR`, `MONTH`, `DAY`, `GETDATE`, `NOW`, `GETUTCDATE`, `SYSDATETIME`.
  - String manipulation functions: `CONCAT`, `LEFT`, `RIGHT`, `CHARINDEX`, `LTRIM`, `RTRIM`, `REVERSE`, `SPACE`, `REPLICATE`.
  - Mathematical & logical functions: `POWER`, `SQRT`, `SIGN`, `PI`, `IIF`, `CHOOSE`.
  - System functions & global metadata: `NEWID()` (RFC 4122 UUID v4), `SCOPE_IDENTITY()`, `@@TRANCOUNT`, `@@SPID`, `@@ERROR`, `@@SERVERNAME`, `@@VERSION`.
  - Native parser support for keyword-colliding function calls (`LEFT`, `RIGHT`) and bare datepart identifiers (`day`, `month`, `year`).
- **Microsoft SQL Server TDS RPC Prepared Statement Pipeline (`pkg/wire/tds`)**:
  - Full RPC routing and lifecycle for `sp_prepare` (ProcID 11), `sp_execute` (ProcID 12), `sp_prepexec` (ProcID 13), `sp_unprepare` (ProcID 15), and parameterized `sp_executesql` (ProcID 10).
  - Accurate token framing: `TokenReturnValue` (0xAC) for statement `@handle`, `TokenReturnStatus` (0x79), and `TokenDoneProc` (0xFE).
  - Robust parameter deserializer supporting `INT`, `BIGINT`, `SMALLINT`, `TINYINT`, `BIT`, `FLOAT`, `REAL`, `VARCHAR`, `NVARCHAR`, `VARBINARY`, and Partially Length-Prefixed (`VARCHAR(MAX)` / `NVARCHAR(MAX)` PLP).
  - Out-of-quotes lexical parameter binding and `?` ODBC/JDBC placeholder replacement.
- **PostgreSQL Driver Protocol Hardening & Cancellation (`pkg/wire`)**:
  - `BackendKeyData` ('K') handshake message emitting backend process ID and secret key.
  - Complete `ParameterStatus` response suite (`server_version`, `server_encoding`, `client_encoding`, `application_name`, `is_superuser`, `session_authorization`, `integer_datetimes`, `DateStyle`, `IntervalStyle`, `standard_conforming_strings`, `TimeZone`).
  - Safe, non-blocking `CancelRequest` (code `80877102`) handling to abort executing client sessions.
- **T-SQL Variable Declaration Extension (`pkg/api/sqlexec.go`)**:
  - Multi-variable `DECLARE @var [type] [= expr]` statement support in script and session execution.

---

## [0.5.0] - 2026-09-23

### Added
- **Comprehensive Technical Documentation & Architecture Specification Suite**:
  - `doc/DATA_DICTIONARY.md`: Virtual system catalog views (`sys.tables`, `sys.columns`, `sys.indexes`, `sys.databases`, `sys.procedures`, `information_schema.*`) and CBO optimizer statistics views (`sys.table_stats`, `sys.column_stats`).
  - `doc/HIGH_AVAILABILITY.md`: Multi-node replication topology, Raft consensus clustering, automatic leader election, and disaster recovery SLAs.
  - `doc/EXTENSIONS.md`: Pluggable storage engine SPI (`storage.Engine`), secondary procedure fallback parser registration, and wire protocol drivers.
  - `doc/METRICS.md`: Prometheus OpenMetrics time-series telemetry dictionary, `/api/pool/stats`, `/api/health` probes, and Grafana alerting templates.
  - `CODE_OF_CONDUCT.md`: Contributor Covenant v2.1 code of conduct.
  - `LICENSE`: Official GNU General Public License v2 / v3 dual licensing terms.

### Changed
- Standardized project workspace paths and module references to `SQLion` across all architectural documentation.

---

## [0.4.0] - 2026-09-22

### Added
- **Microsoft SQL Server TDS 7.x/8.0 Wire Protocol**:
  - Native TDS listener on port `1433` handling PRELOGIN, LOGIN7, SQLBatch UTF-16LE, and RPC token stream decoding.
  - Native compatibility with SQL Server Management Studio (SSMS), Azure Data Studio, `sqlcmd`, and Microsoft ODBC/JDBC drivers.
- **PostgreSQL High-Speed Bulk Copy Protocol (`COPY`)**:
  - Wire-level framing for `COPY <table> FROM STDIN` and `COPY <table> TO STDOUT` (`MsgCopyInResponse`, `MsgCopyOutResponse`, `MsgCopyData`, `MsgCopyDone`, `MsgCopyFail`).
  - Full compatibility with `psql \copy` and `pg_dump` for high-throughput batch ingestion.
- **Volcano Iterator Relational Execution Engine**:
  - Composable iterator pipeline (`Init`, `Next`, `Close`): `SeqScanIterator`, `IndexScanIterator`, `FilterIterator`, `ProjectIterator`, `HashJoinIterator`, `IndexNestedLoopJoinIterator`, `GroupByIterator`, `LimitIterator`.
- **Cost-Based Optimizer (CBO) & Statistics Catalog**:
  - `ANALYZE TABLE <name>` statistics engine computing row counts, page counts, distinct values (`NDV`), null fractions, and 64-bucket equi-depth histograms.
  - Cost model evaluating sequential disk I/O vs. random B+Tree traversal and CPU tuple processing.
  - Plan visualizer via `EXPLAIN` and `EXPLAIN ANALYZE`.
- **Query Memory Limit Governor & Spill-to-Disk External Sort**:
  - Operator-level memory budget tracking (`EstimateTupleSize`) governed by `work_mem` (default `64MB`).
  - Automated spilling of sorted chunks to temporary run files (`sqlion-sort-run-*.tmp`).
  - Streaming N-way Min-Heap merge sort (`tupleMergeHeap`) inside `SortIterator.Next()`.
- **Online Non-Blocking Snapshot Hot Backup & Restore**:
  - Live point-in-time snapshot isolation captured under read-only transactions without locking tables or pausing concurrent OLTP writes.
  - Self-contained `.tar.gz` archive containing `manifest.json`, `schemas.json`, catalog routines, and compressed data rows.
  - CLI utilities: `sqlion -dir data -backup <path>` and `-restore <path>`.
  - HTTP REST endpoints: `GET/POST /api/backup` and `POST /api/restore`.
- **Built-in Goroutine Connection Pooler (PgBouncer Replacement)**:
  - Go goroutine multiplexing supporting 10,000+ client connections with minimal RAM overhead.
  - Bounded worker execution pool (`MaxWorkers = NumCPU * 4`) and cached storage session pool (`MaxIdleSessions = 64`).
  - Monitoring via `GET /api/pool/stats`.
- **Virtual System Catalog Views (`sys.*` and `information_schema.*`)**:
  - Dynamic virtual catalog projection enabling schema inspection by external database tools (DBeaver, DataGrip, Navicat).
- **Embedded Web Management Studio**:
  - PocketBase-style single-file dashboard on `/_/` with spreadsheet inline editing, table builder, and SQL Query Studio.
  - Realtime Server-Sent Events (SSE) data change stream (`/api/realtime`) with live web UI synchronization.
  - Superuser authentication, JWT revocation, and granular API key management.
- **Core Architecture & Operational Documentation Suite**:
  - `doc/ARCHITECTURE.md`, `doc/ADR.md` (ADR-001 ~ ADR-014), `doc/TSQL_REFERENCE.md`, `doc/API_REFERENCE.md`, `doc/OPERATIONS.md`, `doc/BENCHMARK.md`, `doc/INTERNALS_*.md`, `doc/SECURITY.md`, `doc/MIGRATION.md`, `doc/ROADMAP.md`, `doc/TROUBLESHOOTING.md`.

---

## [0.3.0] - 2026-09-21

### Added
- **Native VDBE Bytecode Virtual Machine**:
  - SQLite-style register-based execution engine with `sync.Pool` register slice reuse, replacing the prototype JavaScript runtime.
  - Procedural control flow: `IF...ELSE`, `WHILE`, `TRY...CATCH`, `PRINT`, `RAISERROR`, `RETURN`, and cursors.
- **Secondary Index Engine & Query Optimizations**:
  - Unique and non-unique secondary indexes with range scan support.
  - In-memory Hash Join and Index Nested Loop Join execution engines.
  - `GROUP BY` aggregation engine and `HAVING` clause filtering.
  - Common Table Expressions (`WITH` CTE) and nested subqueries.
  - Temporary tables (`#temp`) and Table-Valued Functions (TVFs).
  - T-SQL User-Defined Functions (UDFs) and trigger engine with virtual delta tables (`INSERTED` / `DELETED`).
- **PostgreSQL Extended Query Protocol**:
  - Prepared statements and binary parameter binding (`Parse`, `Bind`, `Describe`, `Execute`, `Sync`) for ORM and GUI client compatibility.
- **In-Memory OCC Persistence & Durability**:
  - Write-Ahead Logging (WAL) and Append-Only File (AOF) crash recovery.
  - MessagePack binary serialization for fast snapshot checkpoints.
  - Interactive session transactions and session variables.

---

## [0.2.0] - 2026-09-18

### Added
- **Official ANTLR4 T-SQL Parser Integration**:
  - Microsoft T-SQL ANTLR4 grammar (`TSqlParser.g4`) integration for full-spec syntax parsing.
- **Language Extensions & Runtime Robustness**:
  - Expanded T-SQL grammar: `TRY...CATCH`, `CASE...WHEN`, built-in scalar functions, and multi-row DML.
  - Cursor execution loops and ambient transaction context.
- **Storage & Engine Enhancements**:
  - WiredTiger set as the default production storage engine backend.
  - Atomic file-backed disk persistence and crash recovery across restarts.
  - Background daemon lifecycle controls (`-d`, `-status`, `-restart`, `-stop`).
- **High-Concurrency Benchmarking**:
  - OLTP banking benchmark harness measuring transactions per second (TPS) and verifying serializability under contention.
  - WiredTiger session tuning and disk I/O optimization.

### Changed
- Project, Go module, and repository rebranded and standardized to `SQLion`.

---

## [0.1.0] - 2026-09-15

### Added
- **Initial OLTP Database Engine**:
  - Standalone Go server executable delivering high-concurrency embedded transaction processing.
  - PostgreSQL Wire Protocol v3 listener on port `5433`.
  - Prototype T-SQL stored procedure execution engine via Goja JavaScript runtime transpilation.
  - Initial WiredTiger Cgo storage engine implementation.
  - Basic HTTP REST API for table schemas, records, and stored procedure execution.
