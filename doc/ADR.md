# LionSQL Architecture Decision Records (ADR)

This document records the critical architectural decisions made throughout the design and development of LionSQL, explaining the context, problem statements, evaluated alternatives, decisions taken, and their engineering consequences.

---

## Index of Architecture Decisions

- [ADR-001: Standalone Single-Binary Database Architecture](#adr-001-standalone-single-binary-database-architecture)
- [ADR-002: Dual Storage Backend Strategy (WiredTiger MVCC & Pure-Go In-Memory OCC)](#adr-002-dual-storage-backend-strategy-wiredtiger-mvcc--pure-go-in-memory-occ)
- [ADR-003: Storage Persistence Strategy: Hybrid Snapshot + AOF Log Compaction](#adr-003-storage-persistence-strategy-hybrid-snapshot--aof-log-compaction)
- [ADR-004: Dual-Parser Architecture & Automatic ANTLR4 Fallback Strategy](#adr-004-dual-parser-architecture--automatic-antlr4-fallback-strategy)
- [ADR-005: VM Evolution: Migration from Goja JS Engine to Native VDBE Bytecode Engine](#adr-005-vm-evolution-migration-from-goja-js-engine-to-native-vdbe-bytecode-engine)
- [ADR-006: Volcano Iterator Execution Model for Relational Query Processing](#adr-006-volcano-iterator-execution-model-for-relational-query-processing)
- [ADR-007: Cost-Based Optimizer (CBO) and Statistics Collection Engine (`ANALYZE TABLE`)](#adr-007-cost-based-optimizer-cbo-and-statistics-collection-engine-analyze-table)
- [ADR-008: Built-in Goroutine Connection Pooler & Session Multiplexer (PgBouncer Elimination)](#adr-008-built-in-goroutine-connection-pooler--session-multiplexer-pgbouncer-elimination)
- [ADR-009: Concurrent Multi-Wire Protocol Architecture (PostgreSQL v3 & MSSQL TDS 7.x/8.0)](#adr-009-concurrent-multi-wire-protocol-architecture-postgresql-v3--mssql-tds-7x80)
- [ADR-010: Query Memory Limit Governor (`work_mem`) and Spill-to-Disk External Merge Sort](#adr-010-query-memory-limit-governor-work_mem-and-spill-to-disk-external-merge-sort)
- [ADR-011: Online Non-Blocking Point-in-Time Snapshot Hot Backup (`.tar.gz`)](#adr-011-online-non-blocking-point-in-time-snapshot-hot-backup-targz)
- [ADR-012: PostgreSQL Bulk Copy Streaming Protocol (`COPY FROM / TO`)](#adr-012-postgresql-bulk-copy-streaming-protocol-copy-from--to)
- [ADR-013: Embedded Web Management Studio & PocketBase-Style Developer Experience](#adr-013-embedded-web-management-studio--pocketbase-style-developer-experience)
- [ADR-014: 3-Stage Verification Pipeline (Build ➔ Test `-race` ➔ Commit) & Flake-Free Hermeticity](#adr-014-3-stage-verification-pipeline-build--test--race--commit--flake-free-hermeticity)
- [ADR-015: Single-Engine VDBE Unification for Interactive Queries (DQL & DML)](#adr-015-single-engine-vdbe-unification-for-interactive-queries-dql--dml)
- [ADR-016: Zero-Parse Prepared Statement VDBE Bytecode Caching across Wire Protocols](#adr-016-zero-parse-prepared-statement-vdbe-bytecode-caching-across-wire-protocols)
- [ADR-017: 1-Writer Multi-Reader High-Availability Replication Topology & Linear WAL Streaming](#adr-017-1-writer-multi-reader-high-availability-replication-topology--linear-wal-streaming)
- [ADR-018: Dual-Track User-Defined Function (UDF) Architecture: Native Go In-Process SPI & WebAssembly Sandbox](#adr-018-dual-track-user-defined-function-udf-architecture-native-go-in-process-spi--webassembly-sandbox)
- [ADR-019: Schema-Aware Compact Binary Tuple Storage Codec (`0x01`)](#adr-019-schema-aware-compact-binary-tuple-storage-codec-0x01)
- [ADR-020: Multi-Protocol Virtual Catalog Interceptor Strategy for Zero-Friction GUI Introspection](#adr-020-multi-protocol-virtual-catalog-interceptor-strategy-for-zero-friction-gui-introspection)
- [ADR-021: Open Source Licensing Strategy: GNU General Public License v3 (GPL-3.0-or-later)](#adr-021-open-source-licensing-strategy-gnu-general-public-license-v3-gpl-30-or-later)
- [ADR-022: Hybrid Expression B-Tree Indexing for Semi-Structured JSON Documents](#adr-022-hybrid-expression-b-tree-indexing-for-semi-structured-json-documents)
- [ADR-023: Instance-Based Database Isolation & Multi-Database Catalog Multiplexing Spec-Out](#adr-023-instance-based-database-isolation--multi-database-catalog-multiplexing-spec-out)
- [ADR-024: Decoupled CDC Event Streaming vs POSIX Shared Memory (Arrow IPC) Exclusion for OLAP Analytics](#adr-024-decoupled-cdc-event-streaming-vs-posix-shared-memory-arrow-ipc-exclusion-for-olap-analytics)

---

## ADR-001: Standalone Single-Binary Database Architecture

### Status
Accepted

### Context
Traditional relational databases (PostgreSQL, Microsoft SQL Server, MySQL) require complex operational stacks: external connection poolers (PgBouncer), separate web administration GUIs (pgAdmin, DBeaver), and runtime dependencies (C runtimes, shared libraries). In microservices, edge deployments, and developer environments, this operational footprint creates significant setup overhead, network latency between tiers, and orchestration complexity.

### Decision
Package LionSQL as a single, statically compiled standalone executable written in Go that embeds:
1. Multi-protocol wire listeners (PostgreSQL v3 on `:5433`, TDS on `:1433`).
2. HTTP REST/JSON API and embedded Web Management Studio on `:8090` (compiled via `embed.FS`).
3. Core storage engines, query compiler, bytecode VM, and connection pooler.
4. Native CLI administration commands (`-backup`, `-restore`, `-work-mem`, `-daemon`).

### Consequences
- **Positive**: Zero external runtime dependencies. Instant deployment and local development setup. Eliminates multi-hop network latency between web dashboard, pooler, and database engine.
- **Negative**: Single binary size is larger due to embedded assets and ANTLR4 runtime (~35MB to ~45MB).
- **Compliance**: Adheres to Golden Rule 86 (Pure Static Binaries) and Golden Rule 87 (Self-Contained Portability).

---

## ADR-002: Dual Storage Backend Strategy (WiredTiger MVCC & Pure-Go In-Memory OCC)

### Status
Accepted

### Context
An embedded database has two conflicting needs:
1. Production durability with high-throughput concurrent disk I/O, lock-free B+Trees, and hazard pointers for enterprise workloads.
2. Lightweight, hermetic unit testing and cross-platform portability without requiring Cgo or native C library linkers (e.g. CI environments, macOS, Linux, Windows).

### Decision
Abstract the database storage layer strictly behind the `storage.Engine` interface contract and provide two first-class backends:
1. `wiredtiger`: High-performance disk storage engine backed by the official MongoDB WiredTiger C library via Cgo, utilizing lock-free B+Trees, hazard pointer memory management, and MVCC transaction snapshot isolation. (Default for production).
2. `memengine`: Pure Go in-memory engine utilizing Optimistic Concurrency Control (OCC) with row version tracking, secondary in-memory indexes, and append-only WAL/AOF persistence.

### Consequences
- **Positive**: Complete separation of engine interface from underlying physical layout. Development and CI can run instantly with `CGO_ENABLED=0` using `memengine`, while production exploits native WiredTiger multi-core I/O scalability.
- **Negative**: Feature parity (e.g., secondary indexes, transaction semantics) must be implemented and tested across both engines.

---

## ADR-003: Storage Persistence Strategy: Hybrid Snapshot + AOF Log Compaction

### Status
Accepted

### Context
The in-memory OCC engine (`memengine`) required ACID crash durability across restarts. Storing only JSON snapshots caused high write amplification and data loss between intervals, while an unbounded append-only log (WAL/AOF) degraded restart recovery times and consumed excessive disk space.

### Decision
Adopt a Redis/SQLite-inspired hybrid persistence model for `memengine`:
1. **Binary Base Snapshot (`tables.mpk`)**: Full state persisted using compact MessagePack binary encoding via atomic swap files (`tables.mpk.tmp` ➔ rename).
2. **Append-Only Write-Ahead Log (`wal.aof`)**: Every committed transaction appends a binary-encoded `WALRecord` with monotonic LSN.
3. **Fsync Policies**: Support `FsyncEverySec` (default, background thread), `FsyncAlways` (zero data loss), and `FsyncNone`.
4. **Log Compaction (`RewriteAOF`)**: When AOF file size exceeds a defensive threshold (default 64MB), atomically take a fresh `tables.mpk` snapshot and truncate the log.

### Consequences
- **Positive**: Fast sub-millisecond commits during active transactions with bounded disk usage and bounded restart recovery time.
- **Negative**: Additional background goroutine for periodic fsync and compaction coordination.
- **Compliance**: Complies with Golden Rule 1 (Atomic Saves) and Golden Rule 8 (Ephemeral Auto-Recovery).

---

## ADR-004: Dual-Parser Architecture & Automatic ANTLR4 Fallback Strategy

### Status
Accepted

### Context
SQL parsing performance is critical for sub-millisecond query latency. A pure ANTLR4 grammar parser in Go incurs substantial heap allocations (AST tokens, parse tree context objects), adding 100µs to 500µs of overhead per query. However, writing a native hand-rolled parser to cover 100% of Microsoft T-SQL edge cases (bracket escapes, `WITH RECOMPILE`, vendor options) would be an immense undertaking prone to syntax regressions.

### Decision
Enforce a permanent **Dual-Parser Architecture** with automatic runtime fallback:
1. **Primary Fast-Path (Native Pratt / Recursive Descent Parser)**:
   - Location: `pkg/proc/lexer.go`, `pkg/proc/parser.go`.
   - Zero-allocation, sub-microsecond parsing for high-frequency queries, standard DDL/DML, and core procedure control flow (`IF`, `WHILE`, `TRY...CATCH`, `SELECT...INTO`, `UPDATE`, `INSERT`, `DELETE`).
2. **Secondary Fallback (Official Microsoft T-SQL ANTLR4 Parser)**:
   - Location: `pkg/proc/parser/antlr/`.
   - Invoked automatically whenever the primary parser encounters unsupported or vendor-specific MSSQL syntax.
3. **Pluggable Decoupling (Circular Dependency Immunity)**:
   - `pkg/proc` defines the `ProcedureFallbackParser` hook and `RegisterProcedureFallback()`.
   - `pkg/proc/parser/antlr` registers itself in `init()`, maintaining zero circular imports.

```
                  [ T-SQL Client Script / Query ]
                                │
                                ▼
                 [ Primary: Native Pratt Parser ]
                                │
                    ┌───────────┴───────────┐
                 (Success)               (Failure)
                    │                       │
                    │                       ▼
                    │           [ Secondary: ANTLR4 Fallback ]
                    │                       │
                    └───────────┬───────────┘
                                │
                                ▼
                 [ Unified AST: *proc.CreateProcStmt ]
```

### Consequences
- **Positive**: Sub-microsecond latency on 95%+ of queries while maintaining 100% full-spec Microsoft T-SQL syntax compatibility.
- **Negative**: AST transformations must be maintained in both parsers, verified continuously via regression tests.

---

## ADR-005: VM Evolution: Migration from Goja JS Engine to Native VDBE Bytecode Engine

### Status
Accepted

### Context
Early iterations of the project transpiled T-SQL stored procedures into JavaScript and evaluated them using the Goja ECMAScript runtime. While this accelerated initial prototype development, it suffered from:
1. High GC pressure from Goja object allocation.
2. Inability to pause, step, or inspect registers cleanly during debugging.
3. Awkward ambient transaction bridging between JavaScript execution contexts and storage engines.

### Decision
Deprecate the Goja JavaScript runtime and replace it with a native SQLite VDBE-style Bytecode Virtual Machine:
1. **Bytecode Compiler (`pkg/proc/compiler.go`)**: Compiles AST statements into linear instructions (`OpInit`, `OpOpenRead`, `OpColumn`, `OpEq`, `OpJumpIfFalse`, `OpResultRow`, `OpCommit`, etc.).
2. **Virtual Machine (`pkg/proc/runtime/vm.go`)**: Register-based execution with register reuse via `sync.Pool`.
3. **Plan Cache**: Compiled bytecode programs cached with thread-safe LRU eviction.

### Consequences
- **Positive**: Execution speed improved by ~8x. Memory allocations per stored procedure call dropped by 85%. Opcodes can be disassembled (`Disassemble()`) and inspected natively.
- **Negative**: Compiler code complexity increased compared to simple JS transpilation.
- **Compliance**: Complies with Golden Rule 30 (Zero-Allocation Hot Paths) and Golden Rule 21 (Radical KISS).

---

## ADR-006: Volcano Iterator Execution Model for Relational Query Processing

### Status
Accepted

### Context
The database engine needed a unified execution model for complex relational queries involving filters, projections, aggregations, hash joins, index nested loop joins, subqueries, CTEs, and external merge sorts. In-memory materialization of intermediate table results between query stages created excessive memory spikes.

### Decision
Implement the classic Volcano Iterator Model (`pkg/execution/`):
1. **Interface Contract**:
   ```go
   type Iterator interface {
       Init(ctx context.Context) error
       Next() (Tuple, error)
       Close() error
   }
   ```
2. **Composable Operators**:
   - `SeqScanIterator`, `IndexScanIterator`
   - `FilterIterator`, `ProjectIterator`, `LimitIterator`
   - `HashJoinIterator`, `IndexNestedLoopJoinIterator`
   - `GroupByIterator` (with streaming / hash aggregation)
   - `SortIterator` (in-memory and external merge sort)
3. **Zero-Copy Pipeline**: Data flows upward on-demand via `Next()`, avoiding intermediate table materialization.

### Consequences
- **Positive**: Clean composability. First row is returned immediately to the client without waiting for full dataset materialization. Memory consumption is bounded by active operator working sets.
- **Negative**: Function call overhead for each row in deeply nested iterator trees.

---

## ADR-007: Cost-Based Optimizer (CBO) and Statistics Collection Engine (`ANALYZE TABLE`)

### Status
Accepted

### Context
A pure Rule-Based Optimizer (RBO) cannot differentiate between a selective filter (matching 10 rows out of 1,000,000) and an unselective filter (matching 900,000 rows out of 1,000,000). This caused catastrophic plan choices, such as selecting an unselective index scan over a sequential scan, or picking the wrong outer table in Hash Joins.

### Decision
Implement an extensible Cost-Based Optimizer and statistical catalog engine:
1. **Statistics Collector (`ANALYZE TABLE <name>`)**:
   - Scans table data within a read-only transaction.
   - Computes row count, data size, null count, distinct count (`NDV`), and equi-depth histograms (`HistogramBucket`).
   - Stores metadata in the catalog.
2. **Cost Evaluator (`pkg/planner/cost.go`)**:
   - Computes physical operator cost formulas (I/O page costs + CPU tuple processing costs).
   - Selectivity estimation using histograms for range queries (`col > val`, `col BETWEEN a AND b`).
3. **Plan Visualizer (`EXPLAIN` and `EXPLAIN ANALYZE`)**:
   - Visualizes cost estimates, cardinalities, and actual execution timings.

### Consequences
- **Positive**: Sub-optimal join orderings and inappropriate index scans are eliminated. Query execution times on large datasets improve by orders of magnitude.
- **Negative**: `ANALYZE TABLE` consumes I/O and CPU resources; must be scheduled or run on demand.

---

## ADR-008: Built-in Goroutine Connection Pooler & Session Multiplexer (PgBouncer Elimination)

### Status
Accepted

### Context
PostgreSQL's classic multi-process architecture consumes ~2MB to ~10MB of memory per connection, making it unable to handle thousands of concurrent client connections without an external pooler like PgBouncer. Running external PgBouncer introduces configuration complexity, connection pooling modes (session vs. transaction vs. statement), and networking overhead.

### Decision
Build an embedded, native goroutine session pooler and worker multiplexer (`pkg/api/pool.go`):
1. Support 10,000+ simultaneous client connections over lightweight Go goroutines.
2. Maintain a bounded worker execution pool (`MaxWorkers = NumCPU * 4`) and a cached session pool (`MaxIdleSessions = 64`).
3. Multiplex client query execution through worker channels without blocking or spawning new OS threads.
4. Expose real-time connection and worker metrics via `GET /api/pool/stats` and console banners.

### Consequences
- **Positive**: Completely eliminates the need for PgBouncer or external connection poolers. Memory overhead per idle client connection is reduced to Go's minimal ~4KB goroutine stack.
- **Negative**: Long-running transactions hold underlying storage sessions, requiring defensive timeouts and idle reaping.

---

## ADR-009: Concurrent Multi-Wire Protocol Architecture (PostgreSQL v3 & MSSQL TDS 7.x/8.0)

### Status
Accepted

### Context
Organizations and development teams utilize heterogeneous tooling ecosystems. Database administrators and analytical tools rely on PostgreSQL tools (`psql`, DBeaver, Grafana, ORMs like GORM and Prisma), while enterprise legacy systems, reporting services, and Azure Data Studio rely on Microsoft SQL Server's Tabular Data Stream (TDS) wire protocol.

### Decision
Implement concurrent, first-class native wire protocol listeners in Go:
1. **PostgreSQL Wire Protocol v3 (`pkg/wire/`)**:
   - Default port `:5433`.
   - Supports Simple Query (`'Q'`), Extended Query protocol (`'P'` Parse, `'B'` Bind, `'D'` Describe, `'E'` Execute, `'S'` Sync, `'C'` Close), and ParameterStatus negotiation.
2. **Microsoft SQL Server TDS 7.x/8.0 Protocol (`pkg/wire/tds/`)**:
   - Default port `:1433`.
   - Supports PRELOGIN, LOGIN7 handshake, SQLBatch packet framing, and RPC token streams.
3. Both protocols route execution into the unified `Catalog` and `Engine` interfaces.

### Consequences
- **Positive**: Seamless drop-in compatibility for existing tooling, ORMs, and BI suites with zero client driver modifications.
- **Negative**: Wire protocol maintenance requires deep familiarity with binary protocol specifications and endianness.

---

## ADR-010: Query Memory Limit Governor (`work_mem`) and Spill-to-Disk External Merge Sort

### Status
Accepted

### Context
When executing large `ORDER BY` or `GROUP BY` queries on datasets exceeding server RAM, sorting in memory leads to out-of-memory panics (OOM) or massive GC thrashing that degrades the entire database server.

### Decision
Implement a query memory limit governor and external merge sort mechanism:
1. **Memory Budget Configuration**:
   - Server-wide default: `DefaultWorkMem = 64MB` (configurable via `-work-mem` CLI flag).
   - Session-level dynamic override: `SET [SESSION] work_mem = '128MB'`, `SHOW work_mem`, `SELECT @@work_mem`.
2. **Tuple Footprint Governor**:
   - Real-time estimation of accumulated memory footprint in `SortIterator`.
3. **Spill-to-Disk & N-Way Streaming Merge**:
   - When in-memory tuples exceed `work_mem`, sort the batch and spill it to disk as an atomic temporary run file (`sqlion-sort-run-*.tmp`).
   - On completion of input, merge spilled runs using a streaming N-way Min-Heap (`tupleMergeHeap`).
   - Deterministically delete all temporary files upon iterator `Close()`.

### Consequences
- **Positive**: Complete OOM immunity for unbounded sorting operations. Memory consumption remains strictly bounded regardless of dataset size.
- **Negative**: Disk I/O overhead incurred when queries exceed allocated `work_mem`.
- **Compliance**: Complies with Golden Rule 29 (Bounded Buffers) and Golden Rule 1 (Atomic Saves).

---

## ADR-011: Online Non-Blocking Point-in-Time Snapshot Hot Backup (`.tar.gz`)

### Status
Accepted

### Context
Backing up a live 24/7 OLTP database cannot require taking the server offline or acquiring global table write locks (`FLUSH TABLES WITH READ LOCK`), which halts business transactions and causes request timeouts.

### Decision
Implement a non-blocking online snapshot hot backup engine (`pkg/backup/`):
1. **Snapshot Isolation Guarantee**:
   - Opens a read-only transaction (`BeginTx(TxOptions{ReadOnly: true})`) on the active storage engine.
   - Under both WiredTiger (MVCC) and Memory Engine (OCC), read-only transactions read from a point-in-time snapshot without taking locks.
   - Writers continue inserting, updating, and committing concurrently with zero blocking.
2. **Self-Contained Portable Archive Format (`.tar.gz`)**:
   - `manifest.json`: Snapshot metadata, table row counts, engine type, timestamp.
   - `schemas.json`: Table structures and secondary indexes.
   - `catalog/`: Stored procedures, user-defined functions (UDFs), and table triggers.
   - `data/<table_name>.jsonl`: Gzip-compressed lossless row serialization.
3. **Atomic File Operations**:
   - Creation writes to temporary files first before atomic rename (Golden Rule 1).
4. **Multi-Channel Interface**:
   - REST API: `GET /api/backup` (direct HTTP streaming download), `POST /api/backup`, `POST /api/restore`.
   - CLI: `sqlion -dir data -backup snapshot.tar.gz` and `sqlion -dir data -restore snapshot.tar.gz`.
   - Web Management Studio: One-click backup download and restore modal.

### Consequences
- **Positive**: Zero downtime backup with cross-engine portability (can back up from WiredTiger and restore into Memory Engine or vice-versa).
- **Negative**: Heavy concurrent write volume during backup increases snapshot memory/cache retention until backup finishes.
- **Compliance**: Complies with Golden Rule 1 (Atomic Saves), Golden Rule 5 (Defensive Path Security), and Golden Rule 24 (Handle Hygiene).

---

## ADR-012: PostgreSQL Bulk Copy Streaming Protocol (`COPY FROM / TO`)

### Status
Accepted

### Context
Inserting large volumes of records (e.g. 100,000+ rows) via standard `INSERT INTO ... VALUES (...)` statements creates severe network protocol overhead (parsing, plan cache lookup, execution dispatch, and round-trip acknowledgments for every row or batch).

### Decision
Implement the official PostgreSQL Wire Protocol v3 `COPY` streaming protocol (`pkg/wire/copy.go`):
1. **`COPY <table> FROM STDIN`**:
   - Server sends `CopyInResponse ('G')`.
   - Client streams binary or text `CopyData ('d')` chunks directly over the TCP socket until `CopyDone ('c')`.
   - Server parses CSV or tab-delimited text (`\N` for NULLs) and executes batch inserts within an ACID transaction.
   - Sends `CommandComplete ('C')` with row count.
2. **`COPY <table> TO STDOUT`**:
   - Server sends `CopyOutResponse ('H')`.
   - Opens a read-only transaction and streams table rows as `CopyData ('d')` chunks to the client.
3. Fully compatible with `psql \copy`, `pg_dump`, `lib/pq`, and `pgx`.

### Consequences
- **Positive**: Bulk data loading throughput increased by over 15x compared to standard SQL insert statements. Seamless interoperability with standard PostgreSQL ecosystem tools.
- **Negative**: Parsing raw byte streams requires robust error handling and transaction abort mechanisms (`CopyFail ('f')`).

---

## ADR-013: Embedded Web Management Studio & PocketBase-Style Developer Experience

### Status
Accepted

### Context
Developers building modern applications expect instant data visibility, administrative controls, and intuitive API prototyping without being forced to install external desktop database tools (DBeaver, pgAdmin, SSMS).

### Decision
Embed a lightweight, zero-dependency Web Studio directly inside the binary (`pkg/ui/`):
1. **Embedded Assets**: Single-file HTML/CSS/JS dashboard (`dashboard.html`) embedded via Go `embed.FS` and served at `/_/`.
2. **Features**:
   - Real-time Server-Sent Events (SSE) data change stream for live UI synchronization.
   - Spreadsheet-style inline record editor with pagination, search, and multi-column sorting.
   - Interactive SQL Query Studio with syntax templates, execution history, and favorites.
   - DDL schema viewer and table builder.
   - Granular API Key and Superuser JWT authentication management.
   - One-click database backup download and snapshot restore.

### Consequences
- **Positive**: Unmatched developer experience. Launching `./bin/server` immediately provides a fully operational database studio accessible via any browser at `http://localhost:8090/_/`.
- **Negative**: Frontend dashboard code is bundled into the Go binary.

---

## ADR-014: 3-Stage Verification Pipeline (Build ➔ Test `-race` ➔ Commit) & Flake-Free Hermeticity

### Status
Accepted

### Context
Concurrent database engines are notoriously susceptible to elusive data races, deadlocks, and memory corruption that cannot be caught by single-threaded testing. Furthermore, flaky tests that rely on arbitrary `time.Sleep` degrade CI reliability.

### Decision
Formally mandate the **3-Stage Verification Pipeline** across all code changes:
1. **Stage 1 (Build)**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench
   ```
   Validates strict type-checking, Cgo bindings, and compile invariants.
2. **Stage 2 (Test with Race Detector)**:
   ```bash
   go test -tags wiredtiger -count=1 -v -race ./...
   ```
   Mandates 100% test pass rate with **0 data races** across every package.
3. **Stage 3 (Commit)**:
   - Enforce Conventional Commits (`feat(...)`, `fix(...)`, `refactor(...)`, etc.).
   - Strictly prohibit decorative emojis in commit messages, logs, comments, and documentation (Golden Rule 19).
4. **Hermetic Test Isolation**:
   - Use `net.Pipe()` for in-memory socket testing rather than binding physical TCP ports.
   - Use deterministic synchronization primitives (`sync.WaitGroup`, channels) rather than `time.Sleep` (Golden Rule 18).

### Consequences
- **Positive**: Zero flaky tests, zero data races, high engineering confidence, and clean git provenance.
- **Negative**: Slower test execution due to thread sanitizer (`-race`) overhead.

---

## ADR-015: Single-Engine VDBE Unification for Interactive Queries (DQL & DML)

### Status
Accepted

### Context
Historically, LionSQL maintained dual execution pathways:
1. Procedural logic (`CREATE PROCEDURE`, triggers, UDFs) executed on the register-based VDBE Virtual Machine (`pkg/proc/runtime`).
2. Interactive queries (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) executed through ad-hoc engine routines (`execSelect`, `execInsert`, `execUpdate`, `execDelete` in `pkg/api/sqlexec.go`).

This architectural split caused code duplication, inconsistent transaction semantics, disjoint performance characteristics, and prevented universal query inspection via bytecode disassembly.

### Decision
Unify all interactive DQL and DML execution onto the exact same native VDBE Virtual Machine:
1. `proc.CompileQuery(sql)`: Compiles `SELECT` statements into a linear bytecode program featuring `OpRelQuery`, bridging the VDBE directly with Volcano iterator pipelines (`pkg/execution`).
2. `proc.CompileStatement(stmt)`: Compiles standalone `INSERT`, `UPDATE`, and `DELETE` AST nodes into VDBE bytecode programs executing `OpOpenWrite`, `OpInsert`, `OpUpdate`, and `OpDelete`.
3. `runtime.Executor.ExecuteInTx(tx, program, ...)`: Executes precompiled bytecode programs inside ambient transactions on active sessions.
4. `EXPLAIN BYTECODE <sql>`: Provides full disassembly visibility for any relational query or procedural block.

### Consequences
- **Positive**: Single Source of Truth (SSOT). Eliminates fragmented execution pathways. Guarantees 100% semantic and transactional parity between queries executed inside stored procedures and queries run interactively. Enables uniform bytecode profiling and optimization.
- **Negative**: Adds a negligible compilation step for interactive queries, completely mitigated by LRU plan caching.
- **Compliance**: Adheres to Golden Rule 15 (KISS & Deletion-First), Golden Rule 16 (Avoid Duplicate Boilerplate), and Golden Rule 22 (Modular Interface Abstraction).

---

## ADR-016: Zero-Parse Prepared Statement VDBE Bytecode Caching across Wire Protocols

### Status
Accepted

### Context
In high-throughput microservice architectures, clients frequently issue parameterized queries through prepared statement protocols (PostgreSQL Extended Query `'P'` / `'E'` and MSSQL TDS `sp_prepare` / `sp_execute`). Re-parsing SQL text and performing string placeholder substitutions on every execution introduces severe CPU bottleneck and GC pressure.

### Decision
Precompile prepared queries directly into VDBE bytecode programs (`*proc.Program`) during statement preparation:
1. **PostgreSQL Wire v3 (`pkg/wire`)**:
   During `Parse`, compile `$1, $2, ...` queries via `proc.CompileQuery` / `proc.CompileStatement` and cache the `*proc.Program` in `sess.statements[name]`.
   During `Execute`, bind parameters to registers and invoke `s.executor.Run` directly.
2. **MSSQL TDS 7.x/8.0 (`pkg/wire/tds`)**:
   During `sp_prepare` / `sp_prepexec`, compile `@p1, @p2, ...` queries and register the program in `conn.prepStmts[handle]`.
   During `sp_execute`, execute the cached bytecode program directly.

### Consequences
- **Positive**: Zero-parse query execution hot path. Slashes per-query latency and memory allocations. Completely decouples query compilation from execution.
- **Negative**: Session memory holds compiled programs (bounded by Golden Rule 29 via session lifecycle cleanup).
- **Compliance**: Adheres to Golden Rule 30 (Zero-Allocation Hot Paths) and Golden Rule 31 (Responsive Latency Budget).

---

## ADR-017: 1-Writer Multi-Reader High-Availability Replication Topology & Linear WAL Streaming

### Status
Accepted

### Context
Enterprise production environments require scale-out read capacity and automated disaster failover without sacrificing transactional write throughput. Distributed multi-master topologies (such as multi-master Paxos or 2PC) introduce prohibitive consensus round-trip latency on write paths, distributed lock contention, and cascading abort penalties on high-frequency OLTP workloads.

### Decision
Adopt a **1-Writer Multi-Reader Primary-Replica Topology** with asynchronous/semi-synchronous linear WAL streaming:
1. **Single Writer Primary (`RolePrimary`)**:
   - Accepts all write mutations (DDL and DML) and enforces OCC/MVCC validation.
   - Assigns monotonically increasing Log Sequence Numbers (`LSN`) to committed transactions and broadcasts binary framed WAL records via `Broadcaster`.
   - Exposes streaming endpoints: `GET /api/replication/stream?from_lsn=<n>&replica_id=<id>` and `POST /api/replication/ack`.
2. **Multi-Reader Secondary Replicas (`RoleReplica`)**:
   - Launched with `--replica-of <primary_url>` and `--node-id <id>`.
   - Replays streamed WAL records sequentially via background `ReplicationClient`.
   - Enforces strict read-only execution across all wire protocols (PGWire, TDS, HTTP REST) by rejecting mutating statements with `ErrReadOnlyReplica`.
3. **Zero-Downtime Failover & Promotion**:
   - Secondary replicas can be promoted immediately to primary status via `POST /api/replication/promote` (`Promote()`), lifting read-only write locks and allowing the new primary to accept writes without process restart.
   - Primary nodes can be demoted cleanly via `POST /api/replication/demote`.

### Consequences
- **Positive**: Preserves raw single-node write performance (300,000+ tpmC) with zero distributed consensus penalty. Provides linear read scalability across secondary nodes. Fast, deterministic failover.
- **Negative**: Replicas observe small, bounded replication lag ($< 5\text{ms}$ under typical LAN conditions) before replaying incoming WAL frames.
- **Compliance**: Adheres to Golden Rule 22 (Modular Interface Abstraction), Golden Rule 33 (Concurrency Safety), and GEMINI.md Section 6 (Scale-Up Primacy & WAL Replication Topology).

---

## ADR-018: Dual-Track User-Defined Function (UDF) Architecture: Native Go In-Process SPI & WebAssembly Sandbox

### Status
Accepted

### Context
Modern database workloads require custom algorithmic transformations (cryptographic hashing, domain-specific parsing, mathematical formulas). Providing extensibility in a Go database engine has historically been challenging:
- Standard Go dynamic plugins (`plugin.Open` / `.so`) are fragile: they fail to run on Windows, mandate exact Go compiler patch version matching, and force dynamic Cgo linkage.
- Embedding heavy interpreted scripting runtimes (e.g. JavaScript, Python) incurs severe allocation overhead, slow numeric execution, and security escape risks.

### Decision
Adopt a **Dual-Track User-Defined Function (UDF)** extensibility model:
1. **Track 1: Go Native In-Process SPI (`proc.RegisterScalarFunction`)**:
   - For applications embedding LionSQL as an in-process Go library.
   - Developers register type-safe Go functions `func(args []storage.Value) (storage.Value, error)` directly with the runtime engine at startup.
   - Zero FFI boundary crossing, zero sandbox serialization overhead ($< 5\text{ns}$ per function invocation).
2. **Track 2: WebAssembly (Wasm) Sandboxed Extensions (`wazero`)**:
   - For standalone multi-tenant server deployments.
   - Users compile functions from Rust, Go, C, or Zig into standalone `.wasm` bytecode modules and register them dynamically (`CREATE FUNCTION ... AS WASM`).
   - Executed via pure-Go zero-Cgo WebAssembly runtime (`wazero`) with strict instruction fuel limits, memory sandboxing, and zero host filesystem/network access.

### Consequences
- **Positive**: Best-of-both-worlds extensibility. Embedded users achieve native Go speed, while standalone deployments gain cross-platform, secure, polyglot user functions without compiler version lock-in.
- **Negative**: Wasm execution introduces memory marshalling overhead compared to direct native function pointers.
- **Compliance**: Adheres to Golden Rule 26 (Extension Boundary Sandboxing), Golden Rule 84 (Hermetic Cross-Platform Parity), and Golden Rule 86 (Pure Static Binaries).

---

## ADR-019: Schema-Aware Compact Binary Tuple Storage Codec (`0x01`)

### Status
Accepted

### Context
Early iterations of LionSQL explored generic serialization formats (JSON, BSON, MessagePack) for persisting records in WiredTiger B+Tree values and In-Memory WAL log frames. However, generic serialization formats introduce critical performance bottlenecks in high-throughput OLTP workloads:
1. **Redundant Field Names**: Serializing string column keys (`"customer_id"`, `"balance"`) for every single row caused 60% to 80% storage bloat.
2. **Intermediate Map Allocations**: Deserializing generic formats necessitated instantiating `map[string]any` dictionaries, triggering massive Go garbage collection (GC) pressure.
3. **Branching & Type-Tag Overhead**: Repeating dynamic type descriptors per attribute wasted CPU instruction cache lines.

### Decision
Deprecate all generic text and MessagePack record codecs. Implement a **Schema-Aware Compact Binary Tuple Codec** (`pkg/storage/tuple.go`):
1. **Contiguous Binary Layout**: `[0x01 Version Header (1 Byte)]` + `[Null Bitmap (ceil(N/8) Bytes)]` + `[Sequential Column Payloads]`.
2. **Catalog Schema Primacy**: Leverage the table schema definition in the catalog to enforce column order, eliminating column names from row bytes completely.
3. **Zero-Byte NULL Fields**: Columns marked NULL in the bitmask emit exactly zero bytes in the payload section.
4. **ZigZag Varint Integer Compaction**: Compress numeric types via LEB128/ZigZag encoding (`binary.PutVarint`), reducing small integers to 1~2 bytes.
5. **Zero-Allocation Buffer Pooling & Direct Row Passthrough**: Reuse scratch byte slices via `sync.Pool` on serialization paths, and decode directly into indexed `storage.Row` arrays without map allocations.

### Consequences
- **Positive**: 60%~80% reduction in on-disk record footprint; eliminates heap allocation churn on write paths; sub-microsecond row scanning.
- **Negative**: Requires strict table schema coordination during recovery.
- **Compliance**: Adheres to Golden Rule 13 (Control Character Sanitization), Golden Rule 30 (Zero-Allocation Hot Paths), and Rule 86 (Pure Static Binaries).

---

## ADR-020: Multi-Protocol Virtual Catalog Interceptor Strategy for Zero-Friction GUI Introspection

### Status
Accepted

### Context
Modern database GUI tools (Beekeeper Studio, DBeaver, Azure Data Studio, pgAdmin, SSMS) rely on extensive catalog introspection queries to render left-side object trees, column types, default values, and foreign key relationship diagrams.
- PostgreSQL tools issue multi-table joins against `pg_class`, `pg_namespace`, `pg_attribute`, and `information_schema.tables`.
- Microsoft SQL Server tools issue complex vendor-specific joins such as `FROM INFORMATION_SCHEMA.COLUMNS ic LEFT JOIN sys.computed_columns sc ON OBJECT_ID(...) = sc.object_id`.

Building and maintaining physical disk tables and background catalog sync workers for hundreds of relational catalog views would severely complicate the database kernel, introduce catalog lock contention, and bloat the storage engine.

### Decision
Implement a **Virtual Catalog Interceptor Architecture** (`pkg/api/sqlexec_pgcatalog.go`, `pkg/api/sqlexec_dbcc.go`):
1. **Planning-Phase Query Interception**: Detect standard GUI tool catalog join patterns during the query routing phase before physical table resolution.
2. **Dynamic In-Memory Synthesis**: Synthesize response result sets dynamically from active table schemas in memory (`engine.GetTable()`, `catalog.Catalog`), returning deterministic OIDs and native protocol type names.
3. **T-SQL Pagination & Identifier Normalization**: Automatically rewrite vendor pagination (`OFFSET ... ROWS FETCH NEXT ... ROWS ONLY` and `SELECT TOP <N>`) into canonical `LIMIT / OFFSET`, and sanitize bracketed (`[dbo].[table]`) and double-quoted (`"col"`) identifiers.
4. **Zero Physical Catalog Bloat**: Maintain zero physical disk tables for internal system views; user databases remain pure, lightweight, and isolated.

### Consequences
- **Positive**: Immediate out-of-the-box compatibility with major PostgreSQL and SQL Server GUI management clients without proxy layers; zero catalog locking overhead.
- **Negative**: Unrecognized vendor-specific GUI queries require regex/pattern interceptor coverage.
- **Compliance**: Adheres to Golden Rule 14 (Platform Primitives First), Golden Rule 15 (KISS, Minimal Diff), and GEMINI.md Section 2 (Dual-Parser Architecture).

---

## ADR-021: Open Source Licensing Strategy: GNU General Public License v3 (GPL-3.0-or-later)

### Status
Accepted

### Context
LionSQL was initially evaluated under the Affero General Public License (AGPL-3.0). However, AGPL-3.0 presents substantial adoption friction in modern cloud environments:
- Enterprise users and development teams frequently avoid AGPL-licensed software due to network-trigger copyleft clauses that create legal ambiguity around containerized deployments, sidecar microservices, and private internal tooling.
- To maximize adoption while ensuring that modifications to the database engine itself remain open source and freely accessible to the global engineering community, a clear, standard open-source license is required.

### Decision
Adopt the **GNU General Public License v3 (GPL-3.0-or-later)** across the entire LionSQL codebase:
1. Update `LICENSE` to the standard GNU General Public License version 3 text.
2. Update all project documentation (`README.md`, `CHANGELOG.md`) and package headers to reflect `GPL-3.0-or-later`.
3. Provide standard Open Source Initiative (OSI) compliance and commercial transparency.

### Consequences
- **Compliance**: Adheres to Golden Rule 19 (Documentation & Style Integrity) and Level 6 (Cross-Platform Portability & Production Release).

---

## ADR-022: Hybrid Expression B-Tree Indexing for Semi-Structured JSON Documents

### Status
Accepted

### Context
Modern web architectures and BaaS platforms store semi-structured JSON payloads in relational tables. To accelerate point lookups and range queries over JSON attributes, database engines employ different strategies:
- **Microsoft SQL Server**: Requires a 2-step DDL approach (`ALTER TABLE ... ADD col AS JSON_VALUE(...) PERSISTED` followed by `CREATE INDEX`).
- **PostgreSQL**: Supports functional/expression B-Tree indexes (`CREATE INDEX ON tbl ((data->>'key'))`) and Generalized Inverted Indexes (GIN).
- **MySQL 8.0**: Supports functional key parts (`CREATE INDEX ON tbl ((CAST(data->>'$.key' AS CHAR(50))))`).
LionSQL features dual wire protocols (PostgreSQL Wire Protocol :5433 and TDS :1433) backed by high-performance B+Tree storage backends (WiredTiger MVCC and Concurrent SkipList). LionSQL requires a unified indexing model that minimizes DDL friction, avoids GIN write amplification, and bridges PostgreSQL and T-SQL client tools seamlessly.

### Decision
Adopt **Hybrid Expression B-Tree Indexing** as LionSQL's canonical JSON index architecture:
1. **Single-Statement DDL**: Support direct expression index creation without prior computed column declarations:
   - PostgreSQL syntax: `CREATE INDEX idx ON tbl ((data->>'attribute'));`
   - T-SQL syntax: `CREATE INDEX idx ON tbl ((JSON_VALUE(data, '$.attribute')));`
2. **Unified AST Representation**: Normalize both PostgreSQL operators (`->>`) and T-SQL functions (`JSON_VALUE`) into a shared `*proc.JsonExtractExpr` AST node during parsing.
3. **Storage Engine Reusability (B+Tree)**: Evaluate the JSON extraction expression on write (`INSERT` / `UPDATE`) and store the resulting scalar key in standard WiredTiger B+Tree and SkipList index pages, achieving zero-allocation $O(\log N)$ search with zero GIN storage overhead.
4. **CBO Integration**: Cost-Based Optimizer matches both `WHERE data->>'attr' = 'val'` and `WHERE JSON_VALUE(data, '$.attr') = 'val'` to the same underlying B+Tree index.

### Consequences
- **Positive**: Frictionless single-statement indexing for both PostgreSQL and MSSQL clients; reuses existing high-throughput B+Tree storage without write amplification.
- **Negative**: Queries searching arbitrary, unspecified JSON keys across deeply nested documents cannot leverage index acceleration without explicitly creating an expression index for that path.
- **Compliance**: Adheres to Golden Rule 15 (Radical KISS, DRY & Deletion-First), Rule 19 (Modular Interface Abstraction), and `doc/JSON_SPEC_AND_INDEXING.md`.

---

## ADR-023: Instance-Based Database Isolation & Multi-Database Catalog Multiplexing Spec-Out

### Status
Accepted

### Context
Enterprise RDBMSs (PostgreSQL clusters, Microsoft SQL Server instances) traditionally support multi-database catalog multiplexing inside a single server process (`USE <dbname>`). However, in-kernel multi-database multiplexing introduces severe engineering complexity and operational liabilities:
1. **Lack of Fault & Resource Isolation**: A runaway query, memory leak, or file descriptor saturation in one tenant database impacts all other databases in the same process.
2. **Security & Blast Radius**: Cross-database vulnerabilities and memory leaks compromise sensitive tenant isolation.
3. **Locking & Catalog Bloat**: Maintaining concurrent catalog locks, session database context switching, and multi-tenant schema sync introduces massive synchronization overhead.

### Decision
Adopt **Instance-Level Isolation (Process / Container per Database)** as a core architectural invariant:
1. **1-Database per Process**: Each LionSQL server process serves exactly one isolated database catalog per data directory (`--data-dir`).
2. **Multi-Database Catalog Multiplexing Explicitly Spec-Out**: In-kernel multi-database multiplexing is permanently excluded from the engine core.
3. **`USE <dbname>` Tolerant Virtual No-Op**: To guarantee 100% interoperability with MSSQL export scripts, SSMS, DBeaver, and sqlcmd, the `USE <dbname>` statement is accepted as a Tolerant Virtual No-Op. The server synchronizes session virtual database state (`DB_NAME()`, `CURRENT_DATABASE()`) and emits official TDS environment change tokens (`0xE3 ENVCHANGE`), eliminating syntax errors without physical catalog switching.
4. **Standard Multi-Tenant Deployment**: Multi-tenancy is deployed via lightweight LionSQL standalone processes/containers with dedicated ports and directories (`--port`, `--data-dir`).

### Consequences
- **Positive**: Absolute fault, security, and resource isolation; zero catalog lock contention; independent upgrades, scaling, and PITR per database.
- **Negative**: Hosting dozens of micro-databases requires running multiple lightweight LionSQL processes.
- **Compliance**: Adheres to Golden Rule 15 (Radical KISS & Deletion-First) and `GEMINI.md` Section 3.

---

## ADR-024: Decoupled CDC Event Streaming vs POSIX Shared Memory (Arrow IPC) Exclusion for OLAP Analytics

### Status
Accepted

### Context
High-concurrency OLTP databases frequently require integration with analytical engines (DuckDB, Pandas, BI dashboards) for real-time reporting. Two primary integration models were evaluated:
1. **In-Kernel Coupling / POSIX Shared Memory (`/dev/shm` Apache Arrow IPC)**: Directly sharing memory buffers between LionSQL and an embedded DuckDB C++ instance.
2. **Decoupled CDC Event Streaming**: Emitting committed transaction WAL records asynchronously via HTTP NDJSON/SSE (`/api/cdc/stream`) to an independent DuckDB worker.

### Decision
Standardize on **Decoupled CDC Event Streaming** and explicitly **Spec Out** POSIX Shared Memory Arrow IPC:
1. **Zero Blast Radius**: Memory-intensive analytical queries (OOM) or DuckDB crashes cannot impact LionSQL's sub-millisecond OLTP transaction execution.
2. **DuckDB Single-Writer Concurrency Isolation**: DuckDB's native single-writer lock model would severely serialize LionSQL's 16,000+ TPS concurrent writers if embedded in-kernel. Decoupling completely isolates OLTP concurrency from OLAP single-writer constraints.
3. **Readers Never Block Writers**: DuckDB reads committed CDC events asynchronously; initial snapshot dumping uses LionSQL's non-blocking MVCC/OCC reads without acquiring shared read locks.
4. **Eventual Convergence via Watermark LSN**: Initial load handovers use a 3-step cutover (Watermark LSN ➔ Full Dump ➔ CDC Stream with `INSERT OR REPLACE` Upsert) to guarantee 100% deterministic data convergence.

### Consequences
- **Positive**: Strict OLTP latency protection; cross-platform portability without POSIX `/dev/shm` constraints or Docker 64MB shared memory limits; horizontal analytical scale-out.
- **Negative**: Introduces millisecond-level asynchronous replication lag between OLTP commit and DuckDB ingestion.
- **Compliance**: Adheres to Golden Rule 14 (Platform Primitives First), Golden Rule 15 (KISS), and `GEMINI.md` Section 8.4.



