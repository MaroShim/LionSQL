# LionSQL Technical Documentation Directory

Welcome to the comprehensive technical documentation for **LionSQL**—a high-concurrency embedded OLTP database engine and modern Backend-as-a-Service (BaaS) packaged as a single standalone executable.

---

## 1. Documentation Map by Subject Area

```
                             [ LionSQL Documentation Root ]
                                          │
       ┌──────────────────┬───────────────┴───────────────┬──────────────────┐
       ▼                  ▼                               ▼                  ▼
[ Core & Storage ]  [ Query & VM ]             [ Wire Protocols ]    [ BaaS & APIs ]
- ARCHITECTURE.md   - INTERNALS_VDBE.md        - INTERNALS_WIRE.md   - AUTO_REST_API.md
- INTERNALS_STOR.md - INTERNALS_CBO_VOLCANO.md - TSQL_REFERENCE.md   - WEBHOOKS_AND_RLS.md
- TX_CONCURRENCY.md - PERCENTILE_AND_QUANT.md  - CLIENT_TOOL_INT.md  - CRON_AND_MAILER.md
- RECORD_BINARY.md  - LOG_PROC_GREP_AWK.md                           - WEB_SDK.md
- DATA_DICTIONARY   - EXTENSIONS.md                                  - API_REFERENCE.md
- HIGH_AVAIL.md                                                      - WEB_SDK.md
```

---

## 2. Directory of Documents

### 2.0. Official Manuals (공식 매뉴얼)
* [`USER_MANUAL.ko.md`](file:///Users/maro/Projects/sql/doc/USER_MANUAL.ko.md)
  * 설치, 기동, 클라이언트 도구 연동, 데이터베이스 개발 및 운영 유지보수 전 과정을 안내하는 공식 사용자 매뉴얼.
* [`REFERENCE_MANUAL.ko.md`](file:///Users/maro/Projects/sql/doc/REFERENCE_MANUAL.ko.md)
  * CLI 플래그, 설정 파일, SQL 문법 및 구문, 데이터 타입, 내장 함수, 가상 카탈로그 뷰, REST API 및 와이어 프로토콜 기술 사양을 총망라하는 공식 레퍼런스 매뉴얼.

### 2.1. Architecture & Core Database Engine
* [`ARCHITECTURE.md`](file:///Users/maro/Projects/sql/doc/ARCHITECTURE.md)
  * End-to-end architectural blueprint of LionSQL.
  * Dual-storage backend (Pure-Go OCC memory engine vs WiredTiger lock-free B+Tree with MVCC).
  * Ambient ACID transactions, isolation levels, and goroutine connection pooler.
* [`INTERNALS_STORAGE.md`](file:///Users/maro/Projects/sql/doc/INTERNALS_STORAGE.md)
  * Deep dive into the storage engine interface contract.
  * SkipList in-memory indexing vs WiredTiger page structures, checkpoints, and write-ahead logging (WAL).
* [`RECORD_BINARY_FORMAT.md`](file:///Users/maro/Projects/sql/doc/RECORD_BINARY_FORMAT.md)
  * Schema-Aware Compact Binary Tuple serialization specification (`pkg/storage/tuple.go`).
  * Bit-level layout: `0x01` version header, $\lceil N/8 \rceil$ null bitmap, and dense ZigZag varint payloads.
  * Architectural lineage: PostgreSQL heap tuple `t_bits`, CockroachDB/TiDB KV encoding, and Protobuf varints.
  * WiredTiger B+Tree disk value persistence vs in-memory OCC WAL durability.
* [`TRANSACTION_CONCURRENCY.md`](file:///Users/maro/Projects/sql/doc/TRANSACTION_CONCURRENCY.md)
  * Transaction isolation and concurrency model specification.
  * Non-blocking snapshot reads and private workspace isolation.
  * In-Memory OCC with monotonic integer versioning vs WiredTiger MVCC with hazard pointers and transaction timestamps.
  * VDBE transaction coupling (`exec.RunInTx`) and benchmark transaction fidelity.
* [`DATA_DICTIONARY.md`](file:///Users/maro/Projects/sql/doc/DATA_DICTIONARY.md)
  * System catalogs, system tables, schema metadata persistence, and virtual catalog views (`sys.tables`, `INFORMATION_SCHEMA`).
* [`HIGH_AVAILABILITY.md`](file:///Users/maro/Projects/sql/doc/HIGH_AVAILABILITY.md)
  * Crash safety, checkpointing, AOF compaction, and online hot backup/restore pipelines.
* [`ADR.md`](file:///Users/maro/Projects/sql/doc/ADR.md)
  * Architectural Decision Records capturing foundational design tradeoffs (VDBE unification, dual-parser fallback, single-binary distribution, HA replication, dual-track UDFs, compact binary tuple codec, multi-protocol catalog interception, GPL v3 licensing, hybrid JSON expression B-Tree indexing: ADR-001 through ADR-022).

---

### 2.2. Query Compilation, Optimization & Virtual Machine
* [`INTERNALS_VDBE.md`](file:///Users/maro/Projects/sql/doc/INTERNALS_VDBE.md)
  * SQLite-style register-based Virtual Database Engine (VDBE) architecture.
  * Bytecode opcodes specification (`OpInit`, `OpOpenRead`, `OpIndexScan`, `OpRelQuery`, `OpYield`).
  * `sync.Pool` register reuse and compiled program caching (`*runtime.PlanCache`).
* [`INTERNALS_CBO_VOLCANO.md`](file:///Users/maro/Projects/sql/doc/INTERNALS_CBO_VOLCANO.md)
  * Volcano iterator pipeline (`SeqScan`, `IndexScan`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`).
  * Cost-Based Optimizer (CBO), selectivity models, and 64-bucket equi-depth histograms.
  * Constant folding, predicate pushdown, and correlated subquery decorrelation (`SEMI`/`ANTI` joins).
  * `work_mem` memory governor and disk-spilling external merge sort.
* [`JSON_SPEC_AND_INDEXING.md`](file:///Users/maro/Projects/sql/doc/JSON_SPEC_AND_INDEXING.md)
  * Semi-structured JSON document querying and expression B-Tree indexing specification.
  * Comparative architectural analysis across PostgreSQL, MySQL 8.0, and Microsoft SQL Server.
  * Dual-protocol AST normalization (`*proc.JsonExtractExpr`), `JSON_VALUE`, `JSON_QUERY`, `ISJSON`, and PostgreSQL `->` / `->>` operators.
  * Single-statement functional B-Tree index DDL (`CREATE INDEX ... ON tbl ((data->>'key'))`) and CBO index matching.
* [`TSQL_REFERENCE.md`](file:///Users/maro/Projects/sql/doc/TSQL_REFERENCE.md)
  * Complete Microsoft T-SQL syntax compatibility guide.
  * Stored procedures, user-defined functions (scalar & TVF), and triggers (`INSERTED`/`DELETED` delta tables).
  * Control flow statements (`IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO`, cursors).
* [`ANTLR_PARSER_COMPACT_STRATEGY.md`](file:///Users/maro/Projects/sql/doc/ANTLR_PARSER_COMPACT_STRATEGY.md)
  * Technical roadmap and phased strategy for slimming down the generated T-SQL ANTLR4 parser (`pkg/proc/parser/antlr/`).
  * Analysis of 267k-line generated code, unused listener pruning, grammar subsetting, ATN packing, and long-term Native Pratt Parser unification.
* [`EXTENSIONS.md`](file:///Users/maro/Projects/sql/doc/EXTENSIONS.md)
  * Advanced SQL capabilities: Window functions (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), JSON/JSONB operators, CTEs, Foreign Keys with cascading actions, and Dual-Track UDF architecture (Go native SPI + Wasm sandbox).
* [`PERCENTILE_AND_QUANTILE_ANALYTICS.md`](file:///Users/maro/Projects/sql/doc/PERCENTILE_AND_QUANTILE_ANALYTICS.md)
  * Continuous and discrete quantiles (`PERCENTILE_CONT`, `PERCENTILE_DISC`), ANSI `WITHIN GROUP (ORDER BY ...)`, `MEDIAN`, and streaming approximate quantiles (`APPROX_PERCENTILE` via T-Digest).
* [`LOG_PROCESSING_GREP_AWK.md`](file:///Users/maro/Projects/sql/doc/LOG_PROCESSING_GREP_AWK.md)
  * In-engine text & log processing operators (`GREP`, `AWK`, `SPLIT_PART`).
  * Zero-copy pushdown filtering, structured token extraction, and schema-free log analytics.

---

### 2.3. Wire Protocols & Client Compatibility
* [`INTERNALS_WIRE_PROTOCOLS.md`](file:///Users/maro/Projects/sql/doc/INTERNALS_WIRE_PROTOCOLS.md)
  * **PostgreSQL Wire Protocol v3** (Port `5433`): Simple and Extended Query Protocols (`Parse`, `Bind`, `Execute`, `Sync`), parameter binding, and SSL/TLS.
  * **Microsoft SQL Server TDS 7.x/8.0** (Port `1433`): Prelogin, Login7, RPC packets, and `sp_prepexec` bytecode plan cache connection.
  * Client compatibility matrix (`psql`, DBeaver, GORM, Prisma, `sqlcmd`, SSMS, Azure Data Studio).
* [`PROTOCOL_EMULATION_GUIDELINES.md`](file:///Users/maro/Projects/sql/doc/PROTOCOL_EMULATION_GUIDELINES.md)
  * Multi-protocol emulation boundary specification and architectural stop criteria.
  * Four-Layer Boundary Model: Wire Framing (100%), Virtual Catalog Handshake (90%), Common DML (80%), Dialect Quirks & Procedures (10~20%).
  * PostgreSQL stopping criteria, planned MySQL protocol specifications, and T-SQL single-kernel procedural invariance.
* [`CLIENT_TOOL_INTEGRATION.md`](file:///Users/maro/Projects/sql/doc/CLIENT_TOOL_INTEGRATION.md)
  * Comprehensive GUI & CLI client tool connection and integration guide.
  * Tested client matrix: Beekeeper Studio, DBeaver Universal Database Tool, Azure Data Studio, SSMS, `psql`, and `sqlcmd`.
  * Multi-protocol catalog interceptors, schema discovery routing, and pagination normalization (`OFFSET FETCH` / `TOP`).
  * Programming language driver connection strings and connection pooling guidelines (Go, Python, Node.js, C#, Rust, Java).

---

### 2.4. Backend-as-a-Service (BaaS), REST & Client SDKs
* [`AUTO_REST_API.md`](file:///Users/maro/Projects/sql/doc/AUTO_REST_API.md)
  * PocketBase-style instant zero-code RESTful CRUD endpoints (`/api/records/:table`).
  * Advanced URL query engine: comparisons, pattern matching (`LIKE`), string containment (`~`), multi-column sorting, and pagination.
* [`WEBHOOKS_AND_RLS.md`](file:///Users/maro/Projects/sql/doc/WEBHOOKS_AND_RLS.md)
  * **Asynchronous Webhook Dispatcher**: Non-blocking worker pool, exponential backoff retries, and HMAC-SHA256 signature verification.
  * **Declarative Row-Level Security (RLS)**: Table-level access rules (`list`, `view`, `create`, `update`, `delete`) with `@auth.id`, `@auth.role`, `@auth.tenant_id` variable substitution.
* [`CRON_AND_MAILER.md`](file:///Users/maro/Projects/sql/doc/CRON_AND_MAILER.md)
  * **Embedded Cron Jobs Scheduler**: Recurring SQL query and T-SQL stored procedure automation with standard 5-field cron syntax.
  * **Embedded SMTP Mailer**: Transactional HTML email templates (`welcome`, `password_reset`, `verification`) and delivery audit ring buffer.
* [`WEB_SDK.md`](file:///Users/maro/Projects/sql/doc/WEB_SDK.md)
  * Official universal Web SDK & HTML embedding guide (`sqlion.js`, `sqlion.d.ts`).
  * Zero-dependency single-file HTML templates, vanilla `fetch()`, realtime SSE live sync, and HTMX recipes.
* [`API_REFERENCE.md`](file:///Users/maro/Projects/sql/doc/API_REFERENCE.md)
  * Complete HTTP REST API and Server-Sent Events (SSE) specification across all endpoints.
* [`CDC_DUCKDB_INTEGRATION.md`](file:///Users/maro/Projects/sql/doc/CDC_DUCKDB_INTEGRATION.md)
  * LionSQL 실시간 CDC 스트림(`GET /api/cdc/stream`) 및 DuckDB 분리형(Decoupled) HTAP 분석 연동 가이드 (Python & DuckDB 샘플 포함).

---

### 2.5. Operations, Security & Benchmarks
* [`OPERATIONS.md`](file:///Users/maro/Projects/sql/doc/OPERATIONS.md)
  * Production deployment guidelines, CLI flags, configuration parameters, and graceful shutdown.
* [`METRICS.md`](file:///Users/maro/Projects/sql/doc/METRICS.md)
  * Prometheus metrics (`/metrics`) and slow query logging (`/api/slow-queries`).
* [`SECURITY.md`](file:///Users/maro/Projects/sql/doc/SECURITY.md)
  * Authentication models, JWT lifecycle, API key role scoping, and network encryption.
* [`BENCHMARK.md`](file:///Users/maro/Projects/sql/doc/BENCHMARK.md)
  * Performance benchmarks (TPC-C, pgbench, read/write latencies, concurrency scaling).
* [`MIGRATION.md`](file:///Users/maro/Projects/sql/doc/MIGRATION.md)
  * Migration guides from PostgreSQL, Microsoft SQL Server, SQLite, and PocketBase.
* [`TROUBLESHOOTING.md`](file:///Users/maro/Projects/sql/doc/TROUBLESHOOTING.md)
  * Common operational errors, connection diagnostics, and debugging techniques.
* [`ROADMAP.md`](../ROADMAP.md)
  * Versioned milestone progression from single-node OLTP to Always On HA and decoupled HTAP.
* [`TODO.ko.md`](file:///Users/maro/Projects/sql/TODO.ko.md)
  * Daily actionable engineering backlog and task status tracker.
* [`CHANGELOG.md`](file:///Users/maro/Projects/sql/CHANGELOG.md)
  * Release history and detailed changelogs following Keep a Changelog standard.

