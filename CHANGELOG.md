# Changelog

All notable changes to SQLion are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Comprehensive Documentation Suite & Architecture Specifications**:
  - `doc/DATA_DICTIONARY.md`: Virtual system catalog views (`sys.tables`, `sys.columns`, `sys.indexes`, `sys.databases`, `sys.procedures`, `information_schema.*`) and CBO statistics views (`sys.table_stats`, `sys.column_stats`).
  - `doc/HIGH_AVAILABILITY.md`: Multi-node replication topology, Raft consensus clustering, automatic leader election, and disaster recovery SLAs.
  - `doc/EXTENSIONS.md`: Pluggable storage engine SPI (`storage.Engine`), procedure fallback parser registration, and wire protocol drivers.
  - `doc/METRICS.md`: Prometheus OpenMetrics telemetry dictionary, `/api/pool/stats`, `/api/health` probes, and Grafana alerting templates.
  - `doc/TSQL_REFERENCE.md`: Complete T-SQL procedural language, expressions, window functions, and catalog views reference.
  - `doc/API_REFERENCE.md`: OpenAPI-style HTTP REST endpoints, API key authentication, and realtime SSE specifications.
  - `doc/OPERATIONS.md`: Production deployment, systemd configuration, Docker orchestration, and backup procedures.
  - `doc/BENCHMARK.md`: High-concurrency OLTP banking benchmark suite, TPS measurement, and memory governor tuning.
  - `doc/SECURITY.md`: RBAC access control, SQL injection defense, secret management, and vulnerability disclosure policy.
  - `doc/MIGRATION.md`: Step-by-step migration runbooks from PostgreSQL, Microsoft SQL Server, SQLite, and MySQL.
  - `doc/ROADMAP.md`: Near-term and long-term architectural roadmap (Raft clustering, vector search, distributed CBO).
  - `doc/TROUBLESHOOTING.md`: Port binding conflicts, OCC serialization conflicts, slow query tuning, and FAQ.
  - `doc/INTERNALS_VDBE.md`, `doc/INTERNALS_STORAGE.md`, `doc/INTERNALS_CBO_VOLCANO.md`, `doc/INTERNALS_WIRE_PROTOCOLS.md`: Low-level subsystem internal architecture specifications.
  - `CODE_OF_CONDUCT.md`: Contributor Covenant v2.1 code of conduct.
  - `LICENSE`: Official GNU General Public License v2 / v3 dual licensing terms.

### Changed
- Standardized project and workspace path naming to `SQLion` across all architectural specifications and repository references.

---

## [0.3.0] - 2026-09-22

### Added
- **PostgreSQL High-Speed Bulk Copy Protocol (`COPY`)**:
  - Implemented wire-level framing for `COPY <table> FROM STDIN` and `COPY <table> TO STDOUT` (`MsgCopyInResponse 'G'`, `MsgCopyOutResponse 'H'`, `MsgCopyData 'd'`, `MsgCopyDone 'c'`, `MsgCopyFail 'f'`).
  - Full compatibility with `psql \copy` and `pg_dump` for high-throughput batch ingestion.
  - Supports standard CSV format and PostgreSQL tab-delimited text format with `\N` null representations.
- **Online Non-Blocking Snapshot Hot Backup & Restore**:
  - Live point-in-time snapshot isolation captured under read-only transactions without locking tables or pausing concurrent OLTP writes.
  - Self-contained `.tar.gz` archive containing `manifest.json`, `schemas.json`, `catalog/` (procedures, UDFs, triggers), and compressed `data/*.jsonl` rows.
  - Standalone CLI utilities: `sqlion -dir data -backup backup.tar.gz` and `sqlion -dir data -restore backup.tar.gz`.
  - HTTP REST endpoints: `GET/POST /api/backup` (streaming download) and `POST /api/restore` (multipart upload or server file reference).
  - Web Management Studio UI integration with one-click backup and restore modals.
- **Query Memory Limit Governor & Spill-to-Disk External Sort**:
  - Operator-level memory budget tracking (`EstimateTupleSize`) governed by `work_mem` (default `64MB`).
  - Automated spilling of sorted chunks to temporary run files (`sqlion-sort-run-*.tmp`).
  - Streaming N-way Min-Heap merge sort (`tupleMergeHeap`) inside `SortIterator.Next()`.
  - Dynamic session configuration: `SET work_mem = '128MB'`, `SHOW work_mem`, and `-work-mem` CLI flag.
- **Cost-Based Optimizer (CBO) & Statistics Catalog**:
  - `ANALYZE TABLE <name>` statistics collection computing row counts, data sizes, distinct values (`NDV`), null fractions, and 64-bucket equi-depth histograms.
  - Physical cost formulas weighting disk page I/O ($1.0$) and CPU tuple processing ($0.01$).
  - Intelligent plan selection: Sequential Scan vs. Index Scan, In-Memory Hash Join vs. Index Nested Loop Join.
  - `EXPLAIN` and `EXPLAIN ANALYZE` visual execution plan output.
- **Volcano Iterator Relational Execution Engine**:
  - Composable iterator pipeline (`Init`, `Next`, `Close`): `SeqScanIterator`, `IndexScanIterator`, `FilterIterator`, `ProjectIterator`, `HashJoinIterator`, `IndexNestedLoopJoinIterator`, `GroupByIterator`, `LimitIterator`.
- **Built-in Goroutine Connection Pooler (PgBouncer Replacement)**:
  - Lightweight Go goroutine multiplexing supporting 10,000+ client connections with ~4KB RAM overhead per connection.
  - Bounded worker execution pool (`MaxWorkers = NumCPU * 4`) and cached storage session pool (`MaxIdleSessions = 64`).
  - Pool saturation monitoring via `GET /api/pool/stats`.
- **Concurrent Dual Wire Protocols**:
  - PostgreSQL Wire Protocol v3 on port `5433` (Simple Query, Extended Query Protocol `Parse`/`Bind`/`Execute`/`Sync`, parameter status negotiation).
  - Microsoft SQL Server TDS 7.x/8.0 Protocol on port `1433` (PRELOGIN, LOGIN7, SQLBatch UTF-16LE, and RPC token stream decoding).
- **Dual-Parser Architecture with Automatic ANTLR4 Fallback**:
  - Hand-crafted zero-allocation Native Pratt parser for sub-microsecond parsing.
  - Official Microsoft T-SQL ANTLR4 grammar parser (`TSqlParser.g4`) automatically invoked on legacy syntax (`[dbo].[Proc]`, `WITH RECOMPILE`, vendor options).
  - Decoupled pluggable registration without circular imports.
- **Native VDBE Bytecode Virtual Machine**:
  - SQLite-style register-based execution engine with `sync.Pool` register slice reuse.
  - Full T-SQL procedural control flow: `IF...ELSE`, `WHILE`, `TRY...CATCH`, `PRINT`, `RAISERROR`, `RETURN`, and cursors.
  - Virtual delta relations (`INSERTED` and `DELETED`) for table triggers.
- **Dual Storage Backends**:
  - Default production engine: WiredTiger (Cgo, Lock-Free B+Tree, MVCC snapshot isolation, disk checkpointing).
  - Pure-Go engine: Memory Engine (OCC with row versioning, MessagePack binary snapshots `tables.mpk`, and append-only WAL/AOF log compaction).
- **Embedded Web Management Studio**:
  - PocketBase-style single-file dashboard (`pkg/ui/dashboard.html`) embedded via `embed.FS` on `/_/`.
  - Spreadsheet table viewer with inline cell editing, pagination, and multi-column sorting.
  - Interactive SQL Query Studio with execution history, saved queries, and DDL inspection.
  - Realtime Server-Sent Events (SSE) stream (`/api/realtime`) synchronizing client tables with database mutations.
  - Superuser JWT authentication and granular API key management.

---

## [0.2.0] - 2026-08-15

### Added
- Secondary index engine supporting unique and non-unique multi-column indexes.
- T-SQL User-Defined Functions (UDFs) and Table-Valued Functions (TVFs).
- Event-driven table triggers (`AFTER` and `INSTEAD OF`).
- Common Table Expressions (`WITH` CTE) and nested subqueries.
- Daemon background lifecycle controls (`-d`, `-status`, `-restart`, `-stop`).

---

## [0.1.0] - 2026-07-01

### Added
- Initial proof-of-concept OLTP engine.
- Prototype T-SQL JavaScript transpilation via Goja runtime.
- Initial WiredTiger Cgo storage engine integration.
- Basic HTTP REST API.
