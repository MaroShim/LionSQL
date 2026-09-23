# Changelog

All notable changes to SQLion are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- **Pure-Go Concurrent SkipList Secondary Index (`pkg/storage/memengine`)**:
  - High-concurrency, lock-aware SkipList indexing replacing $O(N)$ slice copy element shifting with $O(\log N)$ probabilistic skip links.
  - Sub-microsecond point lookup (~177ns/op) and bulk insertion (~584ns/op, 2.8M ops/sec).
  - Unbounded and bounded range scans (`ScanRange`) via Level-0 forward pointer iteration.
  - Seamless snapshot checkpoint serialization (`ToEntries` / `FromEntries`) preserving MessagePack and JSON backward compatibility.
- **Zero-Copy Cursor Tuple Iterator (`pkg/storage/memengine/memengine.go`)**:
  - Eliminated eager full-table clone allocations (`cloneRecord`) during `Scan()` and `IndexScan()`, switching to direct immutable record memory views.
  - Reduced per-row memory allocations to zero on table scan hot paths, cutting GC pressure and query latency.

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
