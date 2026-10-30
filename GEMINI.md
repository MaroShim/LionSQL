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
13. **MySQL-Specific DML Extensions (`ON DUPLICATE KEY UPDATE`, Comma-based `LIMIT offset, count`) (`S-MYSQL-1-P3`)**: Permanently excluded. PostgreSQL `ON CONFLICT DO UPDATE` and standard ANSI/MSSQL `MERGE` are already complete; adding MySQL DML splits OCC/MVCC conflict handling into 3 divergent paths and introduces parser grammar ambiguities.

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
- **Production Unix Binary (macOS/Linux)**: `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql`
- **Pure-Go In-Memory Binary (Zero CGo)**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
- **Windows Standalone Binaries (Zero CGo, 1ms Timer Enabled)**:
  - amd64: `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql`
  - arm64: `CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -buildvcs=false -o bin/lionsql-arm64.exe ./cmd/lionsql`

### Version Single Source of Truth (SSOT)
When releasing or bumping versions, synchronize all four locations in lockstep:
1. `pkg/config/version.go` (`const Version = "..."`)
2. `CHANGELOG.md`
3. `README.md` and `README.ko.md`
4. `TODO.md`

### Continuous Backlog Discipline (`TODO.md` Auto-Sync)
When new requirements, ADRs, or implementation plans are agreed upon in conversation, proactively register them in `TODO.md` (Section 1 Priority Table and Section 2 Domain Backlog). Upon verification pass, immediately mark completed (`[x]`).

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
- [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md): In-Memory OCC performance optimization history, WiredTiger beat roadmap, and 104k tpmC architecture.
- [`notes/WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md`](notes/WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md): Windows daemon decoupling and 1ms timer architecture.
- [`notes/README.md`](notes/README.md) / [`notes/README.ko.md`](notes/README.ko.md): Master index of all completed architecture plans.
- [`TODO.md`](TODO.md): Active operational engineering dashboard.
- [`ROADMAP.md`](ROADMAP.md): Strategic release horizons and milestone gates.
