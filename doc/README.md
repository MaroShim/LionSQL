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
- ARCHITECTURE.md   - INTERNALS_VDBE.md        - INTERNALS_WIRE.md   - REST_API_AUTO_GENERATED.md
- INTERNALS_STOR.md - INTERNALS_CBO_VOLCANO.md - TSQL_REFERENCE.md   - WEBHOOKS_AND_RLS.md
- TX_CONCURRENCY.md - PERCENTILE_AND_QUANT.md  - CLIENT_TOOL_INT.md  - CRON_AND_MAILER.md
- SNAPSHOT_DUCKDB   - LOG_PROC_GREP_AWK.md     - TDS_COMPAT_REPORT   - CDC_DUCKDB_INT.md
- RECORD_BINARY.md  - EXTENSIONS.md            - TEST_VERIF_REPORT   - REST_API_REFERENCE.md
- DATA_DICTIONARY   - HIGH_AVAIL.md                                  - WEB_SDK.md
```

---

## 2. Directory of Documents

### 2.0. Official Manuals (공식 매뉴얼)
* [`USER_MANUAL.ko.md`](USER_MANUAL.ko.md)
  * 설치, 기동, 클라이언트 도구 연동, 데이터베이스 개발 및 운영 유지보수 전 과정을 안내하는 공식 사용자 매뉴얼.
* [`REFERENCE_MANUAL.ko.md`](REFERENCE_MANUAL.ko.md)
  * CLI 플래그, 설정 파일, SQL 문법 및 구문, 데이터 타입, 내장 함수, 가상 카탈로그 뷰, REST API 및 와이어 프로토콜 기술 사양을 총망라하는 공식 레퍼런스 매뉴얼.

### 2.1. Architecture & Core Database Engine
* [`WHY_TSQL_GO_WIREDTIGER.md`](WHY_TSQL_GO_WIREDTIGER.md)
  * Strategic synthesis of the foundational triad: T-SQL procedural VM, Go runtime concurrency, and WiredTiger lock-free B+Tree.
  * Comprehensive rationale on Zero-Vacuum persistence, zero-RTT stored procedures, counterarguments, and the single static binary form under 100MB.
* [`FOUNDATIONAL_DESIGN_DECISIONS.md`](FOUNDATIONAL_DESIGN_DECISIONS.md)
  * Genesis and foundational strategic architectural decisions of LionSQL.
  * Deep dive into early design choices: WiredTiger lock-free B+Tree disk storage, pure-Go OCC in-memory companion engine, Go language runtime concurrency, Microsoft T-SQL procedural core with VDBE bytecode VM, PostgreSQL wire protocol ecosystem reach, embedded HTTP/REST BaaS engine, and Antigravity AI pair-programming engineering methodology.
* [`ZERO_ALLOCATION_ARCHITECTURE.md`](ZERO_ALLOCATION_ARCHITECTURE.md)
  * Zero-allocation memory architecture, buffer reuse strategies, and GC mitigation enabling 20,000+ TPS (In-Memory OCC) and 16,800+ TPS (WiredTiger).
  * Layer-by-layer breakdown: TDS single-frame pooling, PGWire binary bind direct decoding, zero-alloc ASCII catalog pre-filtering (`containsFold`), VDBE register recycling, Volcano chunk streaming (`TupleChunk` sync.Pool), and WiredTiger `WT_CURSOR` pooling/reset.
* [`ARCHITECTURE.md`](ARCHITECTURE.md)
  * End-to-end architectural blueprint of LionSQL.
  * Dual-storage backend (Pure-Go OCC memory engine vs WiredTiger lock-free B+Tree with MVCC).
  * Ambient ACID transactions, isolation levels, and goroutine connection pooler.
* [`INTERNALS_STORAGE.md`](INTERNALS_STORAGE.md)
  * Deep dive into the storage engine interface contract.
  * SkipList in-memory indexing vs WiredTiger page structures, checkpoints, and write-ahead logging (WAL).
  * Physical storage directory layout, `catalog.json` metadata persistence, `cbo_stats.json`, and secondary index persistence.
* [`RECORD_BINARY_FORMAT.md`](RECORD_BINARY_FORMAT.md)
  * Schema-Aware Compact Binary Tuple serialization specification (`pkg/storage/tuple.go`).
  * Bit-level layout: `0x01` version header, $\lceil N/8 \rceil$ null bitmap, and dense ZigZag varint payloads.
  * Architectural lineage: PostgreSQL heap tuple `t_bits`, CockroachDB/TiDB KV encoding, and Protobuf varints.
  * WiredTiger B+Tree disk value persistence vs in-memory OCC WAL durability.
* [`TRANSACTION_CONCURRENCY.md`](TRANSACTION_CONCURRENCY.md)
  * Transaction isolation and concurrency model specification.
  * Non-blocking snapshot reads and private workspace isolation.
  * In-Memory OCC with monotonic integer versioning vs WiredTiger MVCC with hazard pointers and transaction timestamps.
  * VDBE transaction coupling (`exec.RunInTx`) and benchmark transaction fidelity.
* [`DATA_DICTIONARY.md`](DATA_DICTIONARY.md)
  * System catalogs, system tables, schema metadata persistence, and virtual catalog views (`sys.tables`, `INFORMATION_SCHEMA`).
* [`HIGH_AVAILABILITY.md`](HIGH_AVAILABILITY.md)
  * Crash safety, checkpointing, AOF compaction, and online hot backup/restore pipelines.
* [`ADR.md`](ADR.md)
  * Architectural Decision Records capturing foundational design tradeoffs (VDBE unification, dual-parser fallback, single-binary distribution, HA replication, dual-track UDFs, compact binary tuple codec, multi-protocol catalog interception, GPL v3 licensing, hybrid JSON expression B-Tree indexing: ADR-001 through ADR-022).

---

### 2.2. Query Compilation, Optimization & Virtual Machine
* [`INTERNALS_VDBE.md`](INTERNALS_VDBE.md)
  * SQLite-style register-based Virtual Database Engine (VDBE) architecture.
  * Bytecode opcodes specification (`OpInit`, `OpOpenRead`, `OpIndexScan`, `OpRelQuery`, `OpYield`).
  * `sync.Pool` register reuse and compiled program caching (`*runtime.PlanCache`).
* [`INTERNALS_CBO_VOLCANO.md`](INTERNALS_CBO_VOLCANO.md)
  * Volcano iterator pipeline (`SeqScan`, `IndexScan`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`).
  * Cost-Based Optimizer (CBO), selectivity models, and 64-bucket equi-depth histograms.
  * Constant folding, predicate pushdown, and correlated subquery decorrelation (`SEMI`/`ANTI` joins).
  * `work_mem` memory governor and disk-spilling external merge sort.
* [`JSON_SPEC_AND_INDEXING.md`](JSON_SPEC_AND_INDEXING.md)
  * Semi-structured JSON document querying and expression B-Tree indexing specification.
  * Comparative architectural analysis across PostgreSQL, MySQL 8.0, and Microsoft SQL Server.
  * Dual-protocol AST normalization (`*proc.JsonExtractExpr`), `JSON_VALUE`, `JSON_QUERY`, `ISJSON`, and PostgreSQL `->` / `->>` operators.
  * Single-statement functional B-Tree index DDL (`CREATE INDEX ... ON tbl ((data->>'key'))`) and CBO index matching.
* [`TSQL_REFERENCE.md`](TSQL_REFERENCE.md)
  * Complete Microsoft T-SQL syntax compatibility guide.
  * Stored procedures, user-defined functions (scalar & TVF), and triggers (`INSERTED`/`DELETED` delta tables).
  * Control flow statements (`IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO`, cursors).
* [`../notes/ANTLR_PARSER_COMPACTION_PLAN.md`](../notes/ANTLR_PARSER_COMPACTION_PLAN.md)
  * Technical roadmap and phased strategy for slimming down the generated T-SQL ANTLR4 parser (`pkg/proc/parser/antlr/`).
  * Analysis of 267k-line generated code, unused listener pruning, grammar subsetting, ATN packing, and long-term Native Pratt Parser unification.
* [`EXTENSIONS.md`](EXTENSIONS.md)
  * Advanced SQL capabilities: Window functions (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), JSON/JSONB operators, CTEs, Foreign Keys with cascading actions, and Dual-Track UDF architecture (Go native SPI + Wasm sandbox).
* [`PERCENTILE_AND_QUANTILE_ANALYTICS.md`](PERCENTILE_AND_QUANTILE_ANALYTICS.md)
  * Continuous and discrete quantiles (`PERCENTILE_CONT`, `PERCENTILE_DISC`), ANSI `WITHIN GROUP (ORDER BY ...)`, `MEDIAN`, and streaming approximate quantiles (`APPROX_PERCENTILE` via T-Digest).
* [`LOG_PROCESSING_GREP_AWK.md`](LOG_PROCESSING_GREP_AWK.md)
  * In-engine text & log processing operators (`GREP`, `AWK`, `SPLIT_PART`).
  * Zero-copy pushdown filtering, structured token extraction, and schema-free log analytics.

---

### 2.3. Wire Protocols & Client Compatibility
* [`INTERNALS_WIRE_PROTOCOLS.md`](INTERNALS_WIRE_PROTOCOLS.md)
  * **PostgreSQL Wire Protocol v3** (Port `5433`): Simple and Extended Query Protocols (`Parse`, `Bind`, `Execute`, `Sync`), parameter binding, and SSL/TLS.
  * **Microsoft SQL Server TDS 7.x/8.0** (Port `1433`): Prelogin, Login7, RPC packets, and `sp_prepexec` bytecode plan cache connection.
  * Client compatibility matrix (`psql`, DBeaver, GORM, Prisma, `sqlcmd`, SSMS, Azure Data Studio).
* [`../notes/PROTOCOL_EMULATION_GUIDELINES.md`](../notes/PROTOCOL_EMULATION_GUIDELINES.md)
  * Multi-protocol emulation boundary specification and architectural stop criteria.
  * Four-Layer Boundary Model: Wire Framing (100%), Virtual Catalog Handshake (90%), Common DML (80%), Dialect Quirks & Procedures (10~20%).
  * PostgreSQL stopping criteria, planned MySQL protocol specifications, and T-SQL single-kernel procedural invariance.
* [`CLIENT_TOOL_INTEGRATION.md`](CLIENT_TOOL_INTEGRATION.md)
  * Comprehensive GUI & CLI client tool connection and integration guide.
  * Tested client matrix: Beekeeper Studio, DBeaver Universal Database Tool, Azure Data Studio, SSMS, `psql`, and `sqlcmd`.
  * Multi-protocol catalog interceptors, schema discovery routing, and pagination normalization (`OFFSET FETCH` / `TOP`).
  * Programming language driver connection strings and connection pooling guidelines (Go, Python, Node.js, C#, Rust, Java).
* [`TDS_COMPATIBILITY_REPORT.md`](TDS_COMPATIBILITY_REPORT.md)
  * Comprehensive Microsoft SQL Server TDS 7.x/8.0 (Port `1433`) live compatibility verification report across 123 enterprise T-SQL/ORM scenarios (**99.2% practical compatibility rate**: 122/123 passing).
* [`../notes/COLLATION_AND_LOCALE_PLAN.md`](../notes/COLLATION_AND_LOCALE_PLAN.md)
  * Microsoft SQL Server Collation, Locale & Multi-Language architecture and implementation plan (Tolerant `COLLATE` parser, `--collation` dynamic tokens, `COLLATIONPROPERTY`, and zero collation conflict policy).

---

### 2.4. Backend-as-a-Service (BaaS), REST & Client SDKs
* [`REST_API_AUTO_GENERATED.md`](REST_API_AUTO_GENERATED.md)
  * PocketBase-style instant zero-code RESTful CRUD endpoints (`/api/records/:table`).
  * Advanced URL query engine: comparisons, pattern matching (`LIKE`), string containment (`~`), multi-column sorting, and pagination.
* [`WEBHOOKS_AND_RLS.md`](WEBHOOKS_AND_RLS.md)
  * **Asynchronous Webhook Dispatcher**: Non-blocking worker pool, exponential backoff retries, and HMAC-SHA256 signature verification.
  * **Declarative Row-Level Security (RLS)**: Table-level access rules (`list`, `view`, `create`, `update`, `delete`) with `@auth.id`, `@auth.role`, `@auth.tenant_id` variable substitution.
* [`CRON_AND_MAILER.md`](CRON_AND_MAILER.md)
  * **Embedded Cron Jobs Scheduler**: Recurring SQL query and T-SQL stored procedure automation with standard 5-field cron syntax.
  * **Embedded SMTP Mailer**: Transactional HTML email templates (`welcome`, `password_reset`, `verification`) and delivery audit ring buffer.
* [`WEB_SDK.md`](WEB_SDK.md)
  * Official universal Web SDK & HTML embedding guide (`lionsql.js`, `lionsql.d.ts`).
  * Zero-dependency single-file HTML templates, vanilla `fetch()`, realtime SSE live sync, and HTMX recipes.
* [`REST_API_REFERENCE.md`](REST_API_REFERENCE.md)
  * Complete HTTP REST API and Server-Sent Events (SSE) specification across all endpoints.
* [`CDC_DUCKDB_INTEGRATION.md`](CDC_DUCKDB_INTEGRATION.md)
  * Real-time CDC streaming (`GET /api/cdc/stream`) and decoupled HTAP analytical integration guide with DuckDB (includes Python & DuckDB workers).
* [`SNAPSHOT_ISOLATION_AND_DUCKDB.md`](SNAPSHOT_ISOLATION_AND_DUCKDB.md)
  * Deep-dive technical whitepaper on dual storage engine snapshot isolation (WiredTiger MVCC vs In-Memory OCC), intrinsic snapshot time-point distinctions, Zero-Vacuum architecture, and DuckDB initial sync / real-time CDC consistency cutover.
* [`../notes/S3_PRESIGNED_STORAGE_PLAN.md`](../notes/S3_PRESIGNED_STORAGE_PLAN.md)
  * Pattern 1 S3 Pre-signed URL direct upload architecture and implementation plan (pure Go SigV4 signer, REST API, T-SQL built-in functions, CDC orphan asset cleanup).

---

### 2.5. Operations, Security & Benchmarks
* [`TEST_VERIFICATION_REPORT.ko.md`](TEST_VERIFICATION_REPORT.ko.md)
  * LionSQL 코어 엔진 13대 핵심 기능 영역(CBO 힌트/커버링 인덱스, JSON 표현식 B-Tree 인덱스, SQL RBAC, 스키마 마이그레이션 CLI, 롤링 파티셔닝 등) 전수 실시간 검증 및 기술 딥다이브 종합 보고서 (100% PASS).
* [`OPERATIONS.md`](OPERATIONS.md)
  * Production deployment guidelines, CLI flags, configuration parameters, and graceful shutdown.
* [`SCHEMA_MIGRATIONS_CLI.md`](SCHEMA_MIGRATIONS_CLI.md)
  * Declarative schema migration toolchain (`lionsql migrate new/up/down/status`), timestamp versioning, atomic transactional DDL safety, and CI/CD/Kubernetes patterns.
* [`METRICS.md`](METRICS.md)
  * Prometheus metrics (`/metrics`) and slow query logging (`/api/slow-queries`).
* [`SECURITY.md`](SECURITY.md)
  * Authentication models, JWT lifecycle, API key role scoping, and network encryption.
* [`ENTERPRISE_AUTHENTICATION_SPI.md`](ENTERPRISE_AUTHENTICATION_SPI.md)
  * Pluggable `AuthProvider` SPI for corporate directory services (LDAP/AD/PAM), multi-protocol identity convergence, and perimeter proxy security architecture.
* [`../notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](../notes/IMMUTABLE_AUDIT_LOG_PLAN.md)
  * Enterprise cryptographic audit logging architecture and implementation plan (PCI-DSS v4.0 Req 10, SOC 2 Type II, SHA-256 hash chaining, non-blocking MPSC ring buffer, and offline CLI verifier).
* [`BENCHMARK.md`](BENCHMARK.md)
  * Performance benchmarks (TPC-C, pgbench, read/write latencies, concurrency scaling).
* [`TESTING.md`](TESTING.md)
  * Local build, test, and regression guide: 3-stage pipeline, `-run` filtering, build tags, regression test map, Go benchmarks, TPC-C performance regression, and A/B comparison procedure.
* [`MIGRATION.md`](MIGRATION.md)
  * Migration guides from PostgreSQL, Microsoft SQL Server, SQLite, and PocketBase.
* [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
  * Common operational errors, connection diagnostics, and debugging techniques.
* [`../notes/UNIFIED_CLI_PLAN.md`](../notes/UNIFIED_CLI_PLAN.md)
  * PocketBase/CockroachDB style single-binary CLI unification architecture and lifecycle execution plan.
* [`../notes/DOCUMENTATION_CLEANUP_PLAN.md`](../notes/DOCUMENTATION_CLEANUP_PLAN.md)
  * 4-Stage comprehensive technical documentation cleanup and synchronization master plan for v0.90.0 Feature Freeze.
* [`ROADMAP.md`](../ROADMAP.md)
  * Versioned milestone progression from single-node OLTP to Always On HA and decoupled HTAP.
* [`TODO.ko.md`](../TODO.ko.md)
  * Daily actionable engineering backlog and task status tracker.
* [`CHANGELOG.md`](../CHANGELOG.md)
  * Release history and detailed changelogs following Keep a Changelog standard.

