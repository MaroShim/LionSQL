# LionSQL Architecture & Engineering Guidelines (`GEMINI.md`)

## 1. Project Overview
LionSQL is an autonomous, high-concurrency embedded OLTP database engine implemented in Go and packaged into a single standalone static executable. It provides enterprise-grade Microsoft SQL Server T-SQL stored procedure execution, ACID transactional durability, multi-wire protocol listeners, and an embedded web management studio.

- **Storage Engines**: In-Memory OCC (Optimistic Concurrency Control with WAL/AOF compaction) and WiredTiger (Lock-Free B+Tree, MVCC with Hazard Pointers).
- **Wire Protocols**:
  - PostgreSQL Wire Protocol v3 (default TCP `:5433`)
  - Microsoft SQL Server TDS 7.x/8.0 (default TCP `:1433`)
  - REST & JSON HTTP API / Web Studio (default TCP `:8090` / `/_/`)
- **Execution Engine**: SQLite VDBE-style T-SQL bytecode virtual machine with `sync.Pool` register recycling and sub-microsecond interactive execution.

---

## 2. Core Architectural Invariants

### Invariant 1: Dual-Parser Architecture & Automatic Fallback
LionSQL enforces a permanent two-tier parser architecture to achieve sub-microsecond parsing latency for high-frequency queries while guaranteeing full Microsoft T-SQL syntax compatibility:
1. **Tier 1 (Fast-Path Native Pratt Parser)** (`pkg/proc/lexer.go`, `parser.go`): Zero-allocation, sub-microsecond recursive-descent parser handling standard DDL/DML, control flow (`IF`, `WHILE`, `TRY...CATCH`), and transaction statements.
2. **Tier 2 (Full-Grammar ANTLR4 Fallback)** (`pkg/proc/parser/antlr/`): Official Microsoft T-SQL ANTLR4 grammar parser invoked automatically on syntax errors or vendor-specific clauses (`WITH RECOMPILE`, `[dbo].[Proc]`, complex hints).
3. **Pluggable Registration**: Decoupled via `ProcedureFallbackParser` SPI in `pkg/proc` with zero circular import dependencies.
4. **Permanent Invariance**: Both parsers and automatic fallback mechanisms must be continuously maintained and tested in `pkg/proc/runtime/regression_test.go`.

### Invariant 2: Instance-Based Database Isolation & Tolerant Virtual `USE`
1. **Single-Catalog per Process**: Each LionSQL instance services exactly one isolated database catalog per data directory (`--data-dir`). Cross-database multiplexing inside a single kernel is explicitly **Spec-Out** to prevent noisy-neighbor memory leaks, lock contention, and security blast radius.
2. **Tolerant Virtual `USE <dbname>` No-Op**: For 100% compatibility with SSMS, DBeaver, and migration scripts, `USE <dbname>` (`USE master`, `USE [SalesDB]`) is accepted as a tolerant virtual no-op, synchronizing TDS environment tokens (`0xE3 ENVCHANGE`) and virtual session contexts (`DB_NAME()`, `CURRENT_DATABASE()`) without syntax errors.

### Invariant 3: Non-Blocking Read Committed & Snapshot Isolation
1. **Non-Blocking Reads**: Both storage engines eliminate traditional 2PL shared read locks. Reads never block writes, and writes never block reads.
2. **Private Workspace Isolation**: Mutations are buffered in transaction-private workspaces until commit, guaranteeing zero dirty reads or uncommitted leakages.
3. **Engine Concurrency Profiles**:
   - **In-Memory OCC (`pkg/storage/memengine`)**: Monotonic version counter (`Version uint64`). Write-write conflicts trigger immediate fast-fail rollback (`ErrWriteConflict`).
   - **WiredTiger MVCC (`pkg/storage/wiredtiger`)**: Hazard pointer-protected disk B+Tree with transaction timestamps and `WT_ROLLBACK` detection.
4. **Spec-Out**: Heavy 2PL, distributed serializable graphs (SSI), and dirty-read modes (`READ UNCOMMITTED`) are permanently excluded.

### Invariant 4: Standalone Self-Contained Binary (Zero Sidecars)
LionSQL packages the database kernel, wire listeners (:5433, :1433), HTTP engine (:8090), embedded Web Studio (`embed.FS`), connection pooler, cron scheduler, and mailer into a single executable. External daemon dependencies (PgBouncer, Redis, Nginx, ZooKeeper, etcd) are strictly **Spec-Out**.

### Invariant 5: Scale-Up First & 1-Writer Multi-Reader WAL Replication
Prioritizes vertical single-node hardware saturation (20,000+ TPS) over distributed multi-master 2PC networks:
- **Primary Node**: Handles all mutations, enforces OCC/MVCC validation, writes WAL/AOF streams.
- **Replica Nodes**: Continually stream and replay WAL records via LSN tracking to scale out read capacity (SELECT) without lock contention.
- **Spec-Out**: Multi-master distributed sharding and 2PC consensus are permanently excluded.

### Invariant 6: Benchmark & Transaction Modeling Integrity (ACID Atomicity)
1. **All-or-Nothing Boundaries**: Multi-statement business transactions (e.g. TPC-C New-Order) must execute strictly within a single atomic ACID boundary (`sess.BeginTx()` -> `tx.Commit()`). Splitting a logical transaction across autocommit loops is forbidden.
2. **VDBE VM Transaction Binding**: Multi-step stored procedures in benchmarks must share an explicit transaction handle `tx` via `exec.RunInTx(prog, params, tx, nil)`. Any mid-step failure must trigger `tx.Rollback()`.

### Invariant 7: Zero-Regex SQL Parsing in Production Paths
1. **Unfitness of Regular Expressions for Nested & Recursive SQL**:
   - Regular expressions (Type 3 regular languages) lack stack memory and are mathematically incapable of parsing Context-Free Grammars (Type 2 CFG) with balanced parentheses (Dyck language), subqueries, nested expressions, and `CASE...WHEN` branches.
   - Applying regular expressions to SQL structures containing parentheses inevitably causes greedy/non-greedy truncation, quote/comment literal contamination (e.g. `'('`), and silent condition omission leading to silent data corruption.
2. **Strict Boundary: Permitted vs. Strictly Prohibited**:
   - **Permitted (Linear & Flat Format Validation Only)**: String validation with zero grammatical context and zero nesting depth:
     - Migration file pattern verification (`^\d{14}_.+\.(up|down)\.sql$`)
     - Built-in SQL functions that natively evaluate regex expressions (`REGEXP_LIKE`, `~`, `!~`)
     - Simple linear parameter substitutions (`$1`, `$2`) or static UUID/email formatting
   - **Strictly Prohibited (Parentheses, Nesting, Recursion & SQL Clause Parsing)**: Regular expressions must NEVER be used for SQL clause parsing, DDL/DML compilation, argument extraction, or query routing:
     - Subqueries and derived tables (`FROM (SELECT ...)`, `JOIN (SELECT ...)`)
     - Functions or system property calls with parentheses (`SERVERPROPERTY(...)`, `DBCC CHECKTABLE(...)`)
     - Nested operators and IN-lists (`WHERE x IN (...)`, `WHERE x = ANY(...)`)
     - Window / OVER clauses, CTE definitions, and CBO planner join/predicate trees
3. **Mandatory Deterministic Parsers**:
   - All production SQL query routing, clause compilation, condition evaluation, and subquery branching must exclusively utilize deterministic tokenizers (`proc.Lexer`, `sqlutil.FindMatchingParen`, `sqlutil.HasPrefixKeyword`, `sqlutil.FindTopLevelKeyword`) or 2-Tier AST parsers (Pratt / ANTLR4). Deep theoretical and architectural rationales are documented in [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md).

### Invariant 8: Proactive Modular Abstraction over Ad-Hoc Inlining (Rule of Two)
1. **Zero Ad-Hoc Heuristic Duplication**: Never copy-paste or write ad-hoc inline parsing, condition evaluation, or data transformation logic across multiple call sites.
2. **Canonical Domain Package Primacy**: Whenever a pattern or domain concept appears or is needed in 2 or more locations (e.g. SQL predicate parsing, identifier cleanup, paren extraction, data type formatting), immediately externalize and factor it into a cohesive, modular abstraction within the canonical domain package (`pkg/sqlutil`, `pkg/proc`, etc.) accompanied by isolated unit tests.
3. **KISS Call Sites through Encapsulated Complexity**: KISS and YAGNI mean simplifying caller call sites through clean, single-responsibility modules—not bloating multiple caller functions with copy-pasted inline string manipulation heuristics.

### Invariant 9: WAL Crash Recovery & Zero-Truncate Invariant (Fail-Closed Recovery)
1. **Zero Unverified Truncation**: Under no circumstances may the WAL recovery engine (`wal.go`) invoke `file.Truncate()` on I/O errors or mid-file corruption. Truncation is strictly restricted to verified partial torn frames at the physical tail of the file (`stat.Size() == validOffset + remainingBytes`).
2. **Fail-Closed on Read Failures**: Hardware or OS read errors (`syscall.EIO`, read timeouts, permission loss) must immediately halt engine startup with an explicit diagnostic error (`[RULE:ExplicitError]`), guaranteeing zero silent data loss and protecting all intact committed transactions.

### Invariant 10: WiredTiger High-Concurrency & Zero-`transaction_sync` Invariant
1. **Strict Prohibition of `transaction_sync` in `wiredtiger_open`**:
   - `transaction_sync` (including `(enabled=true,method=none)`) must **NEVER** be specified in WiredTiger connection configuration strings (`WiredTigerOpenConfig`).
   - In WiredTiger's C kernel, enabling `transaction_sync` forces all transaction commits through a single global C transaction lock/synchronization path, collapsing multi-core concurrent transaction throughput from 146,000 TPS to under 5,000 TPS (a 96% regression).
2. **Durability via Group Commit and Periodic Flushing**:
   - Durability is guaranteed through continuous write-ahead logging (`log=(enabled=true)`), WiredTiger's internal group commit, and background periodic log flushing (`startPeriodicSync` at 1s intervals).
   - Random `kill -9` crash recovery testing (`cmd/crashtest`) strictly confirms 100% durable recovery with zero transaction loss without `transaction_sync`.
3. **Multi-Layer Defensive Lockdown**:
   - Layer 1 (Runtime Panic): `wt_engine.go` panics during `Open()` if `transaction_sync` appears in the open config.
   - Layer 2 (Unit Invariant Test): `wt_engine_test.go` verifies `WiredTigerOpenConfig` omits `transaction_sync`.
   - Layer 3 (Performance Gate Test): `perf_gate_test.go` runs a concurrent transfer benchmark to ensure TPS never regresses below 50,000 TPS.
   - Layer 4 (Architecture Contract): Codified in `GEMINI.md` and `notes/ENGINEERING_CONTEXT.ko.md`.

### Invariant 11: TDS Windows Host Platform Parity Invariant (Permanent 'Windows' Emulation)
1. **Strict Prohibition of Returning 'macOS' or Host-Specific Non-Windows OS in TDS/System Views**:
   - TDS protocol (`:1433`), SSMS environment detection batches, and SQL Server system views (`sys.dm_os_host_info`) must **NEVER** inspect `runtime.GOOS` or return `"macOS"` or `"Linux"`.
   - In Microsoft SQL Server architecture, there has never existed a native macOS SQL Server engine, and client tools (SSMS, SMO, ADO.NET) have no enum or handling for `macOS`. Returning `"macOS"` causes SSMS `ObjectExplorerControl.BuildDataModel` to throw `System.InvalidOperationException: Operation is not valid due to the current state of the object`.
2. **Permanent 'Windows' Invariance for 100% Client Tool Compatibility**:
   - `sys.dm_os_host_info` must unconditionally return:
     - `host_platform`: `"Windows"`
     - `host_distribution`: `"Windows"`
     - `host_release`: `"10.0"`
   - TDS environment detection batch (`handleSsmsEnvironmentBatch`) must unconditionally return `host_platform = "Windows"`.
   - Regardless of whether LionSQL runs on macOS, Linux, or Windows, all TDS and SQL Server client tools must perceive the engine as a 100% standard Windows-hosted SQL Server.
3. **Multi-Layer Defensive Lockdown**:
   - Layer 1 (Unit Test Assertion): `pkg/wire/tds/server_test.go` (`TestTDS_SSMS_EnvironmentBatch`) asserts `host_platform` is strictly `"Windows"` and fails if `"macOS"` or `"Linux"` appears.
   - Layer 2 (Catalog Test Assertion): `pkg/engine/coordinator_test.go` (`TestCoordinator_SSMS_VirtualCatalogQueries`) asserts `sys.dm_os_host_info` returns `"Windows"`, `"Windows"`, `"10.0"`.
   - Layer 3 (Architecture Contract): Codified in `GEMINI.md` and `notes/ENGINEERING_CONTEXT.ko.md`.

---

## 3. Explicit Spec-Out Decisions (Engine Simplicity & KISS)
To preserve sub-microsecond latency and single-binary simplicity, the following features are permanently **Spec-Out** (detailed rationales preserved in [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md)):

1. **Duplicate PL/pgSQL Interpreter**: Stored procedures unify on the native T-SQL VDBE VM (`pkg/proc`). PGWire serves strictly as a client protocol gateway.
2. **Legacy XML DML / XQuery**: Semi-structured processing standardizes on modern JSON functions (`ISJSON`, `JSON_VALUE`, `JSON_MODIFY`) and native Go parsers.
3. **In-Memory OCC Background Vacuum**: Workspace atomic swap/discard leaves zero dead-tuple chains; periodic full-memory vacuum threads are rejected (YAGNI).
4. **POSIX Shared Memory (Arrow IPC) Snapshots**: High OLTP serialization CPU spikes and OS portability limits (/dev/shm 64MB cap) are avoided by standardizing on CDC/WAL streams.
5. **Distributed 2PC Coordinators (XA / MS DTC)**: Rejected to preserve raw single-node throughput; distributed consistency relies on async/semi-sync WAL streams.
6. **In-Kernel OAuth2 Web Flows**: Avoids outbound HTTP calls and secret leakage by standardizing on Client-Side OIDC Token Exchange (`POST /api/auth/token-exchange`).
7. **In-Kernel Blob Storage & Image Resizing**: Prevents memory/CPU starvation by delegating file binaries to S3-compatible stores via pre-signed URLs.
8. **Immature Pure-Go Storage Engines (Pebble/bbolt)**: 1.0 production focuses exclusively on WiredTiger MVCC and In-Memory OCC (continuous monitoring).
9. **In-Kernel Real-Time Slow-Query Webhooks (`OBS-3`)**: Outbound HTTP calls inside the kernel are rejected; telemetry routes through Prometheus (`/metrics`) and OTLP.
10. **In-Kernel Wasm UDF Sandbox (`wazero`)**: Avoids binary bloat and JIT overhead; leverages zero-allocation Go Native UDF SPI (`RegisterScalarFunction`).
11. **Total Bytecode Translation for Complex Queries (`ARCH-2TIER-1`)**: Complex relational algebra (joins, window functions, CTEs) routes to the Volcano CBO engine (Tier 2); simple OLTP routes to VDBE bytecode (Tier 1).
12. **Pure Columnar Vectorization Engine Full Rewrite**: Avoids extreme code complexity and row-store I/O physical limits; standardizes on 1,024-row chunk streaming (`ChunkedIterator`) and zero-unboxing slot optimizations.
13. **MySQL-Specific DML & Legacy Tabular EXPLAIN Extensions (`ON DUPLICATE KEY UPDATE`, Comma `LIMIT offset, count`, 8-Column Tabular `EXPLAIN`) (`S-MYSQL-1-P2-P3`)**: Permanently excluded. PostgreSQL `ON CONFLICT DO UPDATE` and standard ANSI/MSSQL `MERGE` are already complete; adding MySQL DML splits OCC/MVCC conflict handling into 3 divergent paths and introduces parser grammar ambiguities. Flat 8-column tabular `EXPLAIN` flattens and destroys modern hierarchical Volcano CBO plan trees (which even MySQL 8.0 deprecated in favor of `FORMAT=TREE`) and breaks PostgreSQL GUI visualizers. LionSQL standardizes exclusively on modern indented tree text and JSON plan representations, keeping MySQL syntax support strictly at Phase 1 ergonomics (`DESC`, `SHOW TABLES`, backticks).
14. **In-Memory Varint/Bit-Packing Record Compression (Option B in `OCC-HYPER-2`)**: Permanently excluded. Bit-level variable-width compression acts like CPU-heavy zip compression, requiring sequential decoding loops (25~50ns) on every single column access and destroying the 0.5ns sub-nanosecond $O(1)$ read performance of the in-memory engine. LionSQL exclusively adopts Option A (Pointer-Free Flat Record) for zero GC overhead and instant fixed-offset reads.

---

## 4. Engineering Guidelines & Development Pipeline

### 3-Stage Verification Pipeline (Build ➔ Test ➔ Commit)
All code modifications, refactorings, and feature additions must strictly pass all 3 stages in order before committing:
1. **Build**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
   ```
2. **Test (Race-Free Integrity)**:
   ```bash
   go test -tags wiredtiger -count=1 -v -race ./...
   ```
   100% PASS across all packages and 0 data races required.
3. **Commit**:
   - Conventional Commits format (`feat(...)`, `fix(...)`, `refactor(...)`, `docs(...)`).
   - Zero decorative emojis in code, comments, logs, and commit messages.

> **Exception (Pure Documentation Commits)**: Markdown-only modifications (`*.md`, `*.ko.md`) without Go source code changes may bypass build and test stages and commit immediately.

### Build Tags & Platform Targets Cheat Sheet
To ensure `(built: ..., commit: ...)` is stamped into binary metadata across all targets:
```bash
LDFLAGS="-s -w -X 'lionsql/pkg/config.BuildDate=$(date "+%Y-%m-%d %H:%M:%S %Z")' -X 'lionsql/pkg/config.GitCommit=$(git rev-parse --short HEAD)'"
```
- **Production Unix Binary (macOS/Linux)**: `go build -tags wiredtiger -ldflags="$LDFLAGS" -buildvcs=false -o bin/lionsql ./cmd/lionsql`
- **Pure-Go In-Memory Binary (Zero CGo)**: `go build -ldflags="$LDFLAGS" -buildvcs=false -o bin/lionsql ./cmd/lionsql`
- **Windows Standalone Binaries (Zero CGo, 1ms Timer Enabled)**:
  - amd64: `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$LDFLAGS" -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql`
  - arm64: `CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -ldflags="$LDFLAGS" -buildvcs=false -o bin/lionsql-arm64.exe ./cmd/lionsql`

### Version Single Source of Truth (SSOT)
When releasing or bumping versions, synchronize all four locations in lockstep:
1. `pkg/config/version.go` (`const Version = "..."`)
2. `CHANGELOG.md`
3. `README.md` and `README.ko.md`
4. `TODO.md`

### Continuous Backlog Discipline (`TODO.md` & Sprint DB Dual-Sync)
When new requirements, ADRs, or implementation plans are agreed upon in conversation, **never retain them solely in ephemeral memory or markdown notes**.
1. **Mandatory DB Registration**: Proactively register every new task into the SQLite sprint database via `./bin/sprint add <ID> "<TITLE>" <DOMAIN> <PRIO> [PLAN_DOC]`, which automatically syncs to [`TASKS.md`](TASKS.md).
2. **Dashboard Synchronization**: Synchronize active tasks and candidates into [`TODO.md`](TODO.md) under explicit taxonomy IDs.
3. **Completion Lifecycle**: Upon 3-stage verification pass, update the status via `./bin/sprint set <ID> DONE` and mark completed (`[x]`) in `TODO.md`.

### Documentation Naming & Language Suffix Invariance (`*.ko.md`)
- **Korean Markdown Files**: Any markdown document with Korean body text must strictly use the **`*.ko.md`** extension (e.g. `notes/*_PLAN.ko.md`, `doc/*_GUIDE.ko.md`, `README.ko.md`).
- **English Markdown Files**: Reserved exclusively for pure `*.md` files.

### Unconditional `/plan` Disk-First Persistence Discipline
1. **`/plan` Direct Persistence**: When invoked with `/plan` or tasked with architectural design, the resulting plan must be **unconditionally persisted immediately to disk in `notes/`** (`notes/*_PLAN.ko.md` for Korean, `notes/*_PLAN.md` for English).
2. **Initial Creation Date Header**: Always include the initial creation date (and status metadata) at the top of the PLAN document right below the title H1 (e.g. `> - **최초 작성일**: YYYY-MM-DD` / `> - **Created**: YYYY-MM-DD`).
3. **No Memory-Only Retention**: Never leave plan specifications solely in ephemeral conversation memory or UI-only artifacts.
4. **Manual Pruning**: AI agents must never unilaterally delete plan documents; pruning and archival remain strictly at user discretion.

---

## 5. Developer & AI Handover Context Index (`notes/`)
For deep architectural context, negative knowledge, and historical postmortems, consult the persistent knowledge base:
- [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md): Core invariants, detailed spec-out rationales, and engineering philosophies.
- [`notes/PITFALLS_AND_TRICKS.md`](notes/PITFALLS_AND_TRICKS.md): Circular import prevention, CGo tags, sandbox isolation, Decimal JSON safety.
- [`notes/TAXONOMY.ko.md`](notes/TAXONOMY.ko.md): Complete task identification taxonomy (`P1~P3`, `VPERF-1~18`, `A~D`).
- [`notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md): 2-Tier query engine specification.
- [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/plans/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md): In-Memory OCC performance optimization history, WiredTiger beat roadmap, and 104k tpmC architecture.
- [`notes/WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md`](notes/plans/WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md): Windows daemon decoupling and 1ms timer architecture.
- [`notes/README.md`](notes/README.md) / [`notes/README.ko.md`](notes/README.ko.md): Master index of all completed architecture plans.
- [`TODO.md`](TODO.md): Active operational engineering dashboard.
- [`ROADMAP.md`](ROADMAP.md): Strategic release horizons and milestone gates.
