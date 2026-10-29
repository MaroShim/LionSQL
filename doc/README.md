# LionSQL Technical Documentation Directory

> [한국어 문서 (Korean Version)](README.ko.md)

Welcome to the comprehensive technical documentation for **LionSQL**—a high-concurrency embedded OLTP database engine and modern Backend-as-a-Service (BaaS) packaged as a single standalone executable.

---

## 1. Documentation Map by Subject Area

```
                             [ LionSQL Documentation Root ]
                                          │
       ┌──────────────────┬───────────────┴───────────────┬──────────────────┐
       ▼                  ▼                               ▼                  ▼
[ Core & Storage ]  [ Query & VM ]             [ Wire Protocols ]    [ BaaS & APIs ]
- ARCHITECTURE.md   - internal/VDBE.md        - internal/WIRE.md   - REST_API_RECORDS_GUIDE.md
- internal/STORAGE.md - internal/CBO_VOLC.md - TSQL_REFERENCE.md   - WEBHOOKS_AND_RLS.md
- internal/TX_CONC  - PERCENTILE_ANALYTICS.md  - CLIENT_TOOL_INT.md  - CRON_AND_MAILER.md
- SNAPSHOT_ISOLATION - LOG_PROCESSING_GUIDE.md - TDS_COMPAT_REPORT   - CDC_DUCKDB_INT.md
- internal/RECORD   - EXTENSIONS.md            - PGWIRE_COMPAT_REP   - REST_API_REFERENCE.md
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
* [`internal/ZERO_ALLOCATION_ARCHITECTURE.md`](internal/ZERO_ALLOCATION_ARCHITECTURE.md)
  * Zero-allocation memory architecture, buffer reuse strategies, and GC mitigation enabling 20,000+ TPS (In-Memory OCC) and 16,800+ TPS (WiredTiger).
  * Layer-by-layer breakdown: TDS single-frame pooling, PGWire binary bind direct decoding, zero-alloc ASCII catalog pre-filtering (`containsFold`), VDBE register recycling, Volcano chunk streaming (`TupleChunk` sync.Pool), and WiredTiger `WT_CURSOR` pooling/reset.
* [`ARCHITECTURE.md`](ARCHITECTURE.md)
  * End-to-end architectural blueprint of LionSQL.
  * Dual-storage backend (Pure-Go OCC memory engine vs WiredTiger lock-free B+Tree with MVCC).
  * Ambient ACID transactions, isolation levels, and goroutine connection pooler.
* [`internal/STORAGE.md`](internal/STORAGE.md)
  * Deep dive into the storage engine interface contract.
  * SkipList in-memory indexing vs WiredTiger page structures, checkpoints, and write-ahead logging (WAL).
  * Physical storage directory layout, `catalog.json` metadata persistence, `cbo_stats.json`, and secondary index persistence.
* [`internal/RECORD_BINARY_FORMAT.md`](internal/RECORD_BINARY_FORMAT.md)
  * Schema-Aware Compact Binary Tuple serialization specification (`pkg/storage/tuple.go`).
  * Bit-level layout: `0x01` version header, $\lceil N/8 \rceil$ null bitmap, and dense ZigZag varint payloads.
  * Architectural lineage: PostgreSQL heap tuple `t_bits`, CockroachDB/TiDB KV encoding, and Protobuf varints.
  * WiredTiger B+Tree disk value persistence vs in-memory OCC WAL durability.
* [`internal/TRANSACTION_CONCURRENCY.md`](internal/TRANSACTION_CONCURRENCY.md)
  * Transaction isolation and concurrency model specification.
  * Non-blocking snapshot reads and private workspace isolation.
  * In-Memory OCC with monotonic integer versioning vs WiredTiger MVCC with hazard pointers and transaction timestamps.
  * VDBE transaction coupling (`exec.RunInTx`) and benchmark transaction fidelity.
* [`DATA_DICTIONARY.md`](DATA_DICTIONARY.md)
  * System catalogs, system tables, schema metadata persistence, and virtual catalog views (`sys.tables`, `INFORMATION_SCHEMA`).
* [`RANGE_PARTITIONING.md`](RANGE_PARTITIONING.md)
  * ClickHouse-style Auto Rolling Range Partitioning and $O(1)$ fast partition dropping.
  * Zero-management dynamic partition lifecycle (`toYYYYMM`, `MONTH`, `toYYYYMMDD`, `DAY`, `toYYYY`, `YEAR`), CBO partition pruning with 70%+ I/O reduction, graphical `EXPLAIN` visualization, and system catalog views (`system.partitions`).
* [`HIGH_AVAILABILITY.md`](HIGH_AVAILABILITY.md)
  * Crash safety, checkpointing, AOF compaction, and online hot backup/restore pipelines.
* [`STORAGE_ENCRYPTION_GUIDE.md`](STORAGE_ENCRYPTION_GUIDE.md)
  * Transparent Data Encryption (TDE) architecture for WiredTiger storage engine (AES-256-CBC, PBKDF2 key derivation, zero data-at-rest leaks).
* [`ADR.md`](ADR.md)
  * Architectural Decision Records capturing foundational design tradeoffs (VDBE unification, dual-parser fallback, single-binary distribution, HA replication, dual-track UDFs, compact binary tuple codec, multi-protocol catalog interception, GPL v3 licensing, hybrid JSON expression B-Tree indexing: ADR-001 through ADR-022).

---

### 2.2. Query Compilation, Optimization & Virtual Machine
* [`internal/VDBE.md`](internal/VDBE.md)
  * SQLite-style register-based Virtual Database Engine (VDBE) architecture.
  * Bytecode opcodes specification (`OpInit`, `OpOpenRead`, `OpIndexScan`, `OpRelQuery`, `OpYield`).
  * `sync.Pool` register reuse and compiled program caching (`*runtime.PlanCache`).
* [`internal/CBO_VOLCANO.md`](internal/CBO_VOLCANO.md)
  * Volcano iterator pipeline (`SeqScan`, `IndexScan`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`).
  * Cost-Based Optimizer (CBO), selectivity models, and 64-bucket equi-depth histograms.
  * Constant folding, predicate pushdown, and correlated subquery decorrelation (`SEMI`/`ANTI` joins).
  * `work_mem` memory governor and disk-spilling external merge sort.
* [`JSON_INDEXING_GUIDE.md`](JSON_INDEXING_GUIDE.md)
  * Semi-structured JSON document querying and expression B-Tree indexing guide.
  * Comparative architectural analysis across PostgreSQL, MySQL 8.0, and Microsoft SQL Server.
  * Dual-protocol AST normalization (`*proc.JsonExtractExpr`), `JSON_VALUE`, `JSON_QUERY`, `ISJSON`, and PostgreSQL `->` / `->>` operators.
  * Single-statement functional B-Tree index DDL (`CREATE INDEX ... ON tbl ((data->>'key'))`) and CBO index matching.
* [`TSQL_REFERENCE.md`](TSQL_REFERENCE.md)
  * Complete Microsoft T-SQL syntax compatibility guide.
  * Stored procedures, user-defined functions (scalar & TVF), and triggers (`INSERTED`/`DELETED` delta tables).
  * Control flow statements (`IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO`, cursors).
* [`SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md`](SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md) (Korean)
  * Multi-dialect SQL compatibility and developer ergonomics guide.
  * MySQL-style developer ergonomics (backticks, `DESC`, `SHOW TABLES`), multi-dialect built-in functions Best 7 (`IFNULL`, `CURDATE`, `MOD`, `TRUNC`, `INSTR`, etc.), and T-SQL Fast Forward-Only Read-Only Cursors (`PROC-CURSOR-1`).
* [`EXTENSIONS.md`](EXTENSIONS.md)
  * Advanced SQL capabilities: Window functions (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), JSON/JSONB operators, CTEs, Foreign Keys with cascading actions, and Dual-Track UDF architecture (Go native SPI + Wasm sandbox).
* [`PERCENTILE_ANALYTICS.md`](PERCENTILE_ANALYTICS.md)
  * Continuous and discrete quantiles (`PERCENTILE_CONT`, `PERCENTILE_DISC`), ANSI `WITHIN GROUP (ORDER BY ...)`, `MEDIAN`, and streaming approximate quantiles (`APPROX_PERCENTILE` via T-Digest).
* [`LOG_PROCESSING_GUIDE.md`](LOG_PROCESSING_GUIDE.md)
  * In-engine text & log processing operators (`GREP`, `AWK`, `SPLIT_PART`).
  * Zero-copy pushdown filtering, structured token extraction, and schema-free log analytics.

---

### 2.3. Wire Protocols & Client Compatibility
* [`internal/WIRE_PROTOCOLS.md`](internal/WIRE_PROTOCOLS.md)
  * **PostgreSQL Wire Protocol v3** (Port `5433`): Simple and Extended Query Protocols (`Parse`, `Bind`, `Execute`, `Sync`), parameter binding, and SSL/TLS.
  * **Microsoft SQL Server TDS 7.x/8.0** (Port `1433`): Prelogin, Login7, RPC packets, and `sp_prepexec` bytecode plan cache connection.
  * Client compatibility matrix (`psql`, DBeaver, GORM, Prisma, `sqlcmd`, SSMS, Azure Data Studio).
* [`CLIENT_TOOL_INTEGRATION.md`](CLIENT_TOOL_INTEGRATION.md)
  * Comprehensive GUI & CLI client tool connection and integration guide.
  * Tested client matrix: Beekeeper Studio, DBeaver Universal Database Tool, Azure Data Studio, SSMS, `psql`, and `sqlcmd`.
  * Multi-protocol catalog interceptors, schema discovery routing, and pagination normalization (`OFFSET FETCH` / `TOP`).
  * Programming language driver connection strings and connection pooling guidelines (Go, Python, Node.js, C#, Rust, Java).
* [`TDS_COMPATIBILITY_REPORT.md`](TDS_COMPATIBILITY_REPORT.md)
  * Comprehensive Microsoft SQL Server TDS 7.x/8.0 (Port `1433`) live compatibility verification report across 123 enterprise T-SQL/ORM scenarios (**99.2% practical compatibility rate**: 122/123 passing).
* [`PGWIRE_COMPATIBILITY_REPORT.md`](PGWIRE_COMPATIBILITY_REPORT.md)
  * Comprehensive PostgreSQL Wire Protocol v3 (Port `5433`) live compatibility verification report across ORM frameworks and GUI tools.

---

### 2.4. Backend-as-a-Service (BaaS), REST & Client SDKs
* [`WEB_STUDIO_GUIDE.md`](WEB_STUDIO_GUIDE.md) ([한국어](WEB_STUDIO_GUIDE.ko.md))
  * Complete browser-based all-in-one Web Studio guide: schema modeling, data grid CRUD, SQL query console, live query audit logs, and hot backup/restore.
* [`REST_API_RECORDS_GUIDE.md`](REST_API_RECORDS_GUIDE.md)
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
* [`REST_API_COMPATIBILITY_REPORT.md`](REST_API_COMPATIBILITY_REPORT.md)
  * End-to-end verification report of the HTTP REST engine, OpenAPI endpoints, SSE real-time streaming, and developer API preview.
* [`CDC_DUCKDB_INTEGRATION.md`](CDC_DUCKDB_INTEGRATION.md)
  * Real-time CDC streaming (`GET /api/cdc/stream`) and decoupled HTAP analytical integration guide with DuckDB (includes Python & DuckDB workers).
* [`SNAPSHOT_ISOLATION_ARCHITECTURE.md`](SNAPSHOT_ISOLATION_ARCHITECTURE.md)
  * Deep-dive technical whitepaper on dual storage engine snapshot isolation (WiredTiger MVCC vs In-Memory OCC), intrinsic snapshot time-point distinctions, Zero-Vacuum architecture, and DuckDB initial sync / real-time CDC consistency cutover.

---

### 2.5. Operations, Security & Benchmarks
* [`TRANSACTION_PERFORMANCE_GUIDE.ko.md`](TRANSACTION_PERFORMANCE_GUIDE.ko.md)
  * Transaction performance, latency-durability trade-offs, and workload-specific parameter tuning guide (Ultra low-latency in-memory cache to zero-loss financial ledger).
* [`OPERATIONS.md`](OPERATIONS.md)
  * Production deployment guidelines, CLI flags, configuration parameters, and graceful shutdown.
* [`SCHEMA_MIGRATION_GUIDE.md`](SCHEMA_MIGRATION_GUIDE.md)
  * Declarative schema migration toolchain (`lionsql migrate new/up/down/status`), timestamp versioning, atomic transactional DDL safety, and CI/CD/Kubernetes patterns.
* [`METRICS.md`](METRICS.md)
  * Prometheus metrics (`/metrics`) and slow query logging (`/api/slow-queries`).
* [`SECURITY.md`](SECURITY.md)
  * Authentication models, JWT lifecycle, API key role scoping, and network encryption.
* [`ENTERPRISE_AUTHENTICATION_SPI.md`](ENTERPRISE_AUTHENTICATION_SPI.md)
  * Pluggable `AuthProvider` SPI for corporate directory services (LDAP/AD/PAM), multi-protocol identity convergence, and perimeter proxy security architecture.
* [`BENCHMARK.md`](BENCHMARK.md)
  * Performance benchmarks (TPC-C, pgbench, read/write latencies, concurrency scaling).
* [`COMPLEX_QUERY_BENCHMARK_GUIDE.md`](COMPLEX_QUERY_BENCHMARK_GUIDE.md) ([Korean](COMPLEX_QUERY_BENCHMARK_GUIDE.ko.md))
  * Star Schema Benchmark (SSB) complex analytical query and Volcano CBO engine benchmark guide, CLI usage (`bin/ssb`), cross-DBMS performance comparison (DuckDB, PostgreSQL, SQLite), and architectural analysis.
* [`TESTING.md`](TESTING.md)
  * Local build, test, and regression guide: 3-stage pipeline, `-run` filtering, build tags, regression test map, Go benchmarks, TPC-C performance regression, and A/B comparison procedure.
* [`DATABASE_MIGRATION_GUIDE.md`](DATABASE_MIGRATION_GUIDE.md)
  * Migration guides from PostgreSQL, Microsoft SQL Server, SQLite, and PocketBase.
* [`RELEASE_AUTOMATION_GUIDE.ko.md`](RELEASE_AUTOMATION_GUIDE.ko.md)
  * One-click release automation pipeline (`scripts/release.sh`), 4-location SSOT version sync, Keep a Changelog classification, Windows .exe cross-compilation, and tagging guide.
* [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
  * Common operational errors, connection diagnostics, and debugging techniques.

---

### 2.6. Developer Handover, Engineering Notes & Backlog
* [`../notes/README.md`](../notes/README.md)
  * Engineering context, core architectural invariants, landmines/pitfalls, and domain execution plans (`*_PLAN.md`).
* [`../ROADMAP.md`](../ROADMAP.md)
  * Versioned milestone progression from single-node OLTP to Always On HA and decoupled HTAP.
* [`../TODO.ko.md`](../TODO.ko.md)
  * Daily actionable engineering backlog and task status tracker.
* [`../CHANGELOG.md`](../CHANGELOG.md)
  * Release history and detailed changelogs following Keep a Changelog standard.

