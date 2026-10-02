# SQLion Technical Documentation Directory

Welcome to the comprehensive technical documentation for **SQLion**—a high-concurrency embedded OLTP database engine and modern Backend-as-a-Service (BaaS) packaged as a single standalone executable.

---

## 1. Documentation Map by Subject Area

```
                             [ SQLion Documentation Root ]
                                          │
       ┌──────────────────┬───────────────┴───────────────┬──────────────────┐
       ▼                  ▼                               ▼                  ▼
[ Core & Storage ]  [ Query & VM ]             [ Wire Protocols ]    [ BaaS & APIs ]
- ARCHITECTURE.md   - INTERNALS_VDBE.md        - INTERNALS_WIRE.md   - AUTO_REST_API.md
- INTERNALS_STOR.md - INTERNALS_CBO_VOLCANO.md - TSQL_REFERENCE.md   - WEBHOOKS_AND_RLS.md
- DATA_DICTIONARY   - EXTENSIONS.md                                  - CRON_AND_MAILER.md
- HIGH_AVAIL.md                                                      - SDK.md
                                                                     - API_REFERENCE.md
```

---

## 2. Directory of Documents

### 2.1. Architecture & Core Database Engine
* [`ARCHITECTURE.md`](file:///Users/maro/Projects/sql/doc/ARCHITECTURE.md)
  * End-to-end architectural blueprint of SQLion.
  * Dual-storage backend (Pure-Go OCC memory engine vs WiredTiger lock-free B+Tree with MVCC).
  * Ambient ACID transactions, isolation levels, and goroutine connection pooler.
* [`INTERNALS_STORAGE.md`](file:///Users/maro/Projects/sql/doc/INTERNALS_STORAGE.md)
  * Deep dive into the storage engine interface contract.
  * SkipList in-memory indexing vs WiredTiger page structures, checkpoints, and write-ahead logging (WAL).
* [`DATA_DICTIONARY.md`](file:///Users/maro/Projects/sql/doc/DATA_DICTIONARY.md)
  * System catalogs, system tables, schema metadata persistence, and virtual catalog views (`sys.tables`, `INFORMATION_SCHEMA`).
* [`HIGH_AVAILABILITY.md`](file:///Users/maro/Projects/sql/doc/HIGH_AVAILABILITY.md)
  * Crash safety, checkpointing, AOF compaction, and online hot backup/restore pipelines.
* [`ADR.md`](file:///Users/maro/Projects/sql/doc/ADR.md)
  * Architectural Decision Records capturing foundational design tradeoffs (VDBE unification, dual-parser fallback, single-binary distribution).

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
* [`TSQL_REFERENCE.md`](file:///Users/maro/Projects/sql/doc/TSQL_REFERENCE.md)
  * Complete Microsoft T-SQL syntax compatibility guide.
  * Stored procedures, user-defined functions (scalar & TVF), and triggers (`INSERTED`/`DELETED` delta tables).
  * Control flow statements (`IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO`, cursors).
* [`EXTENSIONS.md`](file:///Users/maro/Projects/sql/doc/EXTENSIONS.md)
  * Advanced SQL capabilities: Window functions (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), JSON/JSONB operators, CTEs, Foreign Keys with cascading actions.
* [`LOG_PROCESSING_GREP_AWK.md`](file:///Users/maro/Projects/sql/doc/LOG_PROCESSING_GREP_AWK.md)
  * In-engine text & log processing operators (`GREP`, `AWK`, `SPLIT_PART`).
  * Zero-copy pushdown filtering, structured token extraction, and schema-free log analytics.

---

### 2.3. Wire Protocols & Client Compatibility
* [`INTERNALS_WIRE_PROTOCOLS.md`](file:///Users/maro/Projects/sql/doc/INTERNALS_WIRE_PROTOCOLS.md)
  * **PostgreSQL Wire Protocol v3** (Port `5433`): Simple and Extended Query Protocols (`Parse`, `Bind`, `Execute`, `Sync`), parameter binding, and SSL/TLS.
  * **Microsoft SQL Server TDS 7.x/8.0** (Port `1433`): Prelogin, Login7, RPC packets, and `sp_prepexec` bytecode plan cache connection.
  * Client compatibility matrix (`psql`, DBeaver, GORM, Prisma, `sqlcmd`, SSMS, Azure Data Studio).

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
* [`SDK.md`](file:///Users/maro/Projects/sql/doc/SDK.md)
  * Official universal JavaScript & TypeScript Client SDK (`sqlion.js`, `sqlion.d.ts`).
  * Browser CDN inclusion, npm/ESM imports, collection services, realtime SSE subscriptions, and typed models.
* [`API_REFERENCE.md`](file:///Users/maro/Projects/sql/doc/API_REFERENCE.md)
  * Complete HTTP REST API and Server-Sent Events (SSE) specification across all endpoints.

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
* [`ROADMAP.md`](file:///Users/maro/Projects/sql/doc/ROADMAP.md)
  * Completed milestones and future technical development initiatives.
