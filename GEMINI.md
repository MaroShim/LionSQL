# SQLion Project Architecture & Engineering Guidelines

(한국어 공식 가이드라인 문서는 [GEMINI.ko.md](GEMINI.ko.md)를 참조하세요.)

## 1. Project Overview
SQLion is a high-concurrency embedded OLTP database engine written in Go, packaged as a single standalone executable. It delivers enterprise-grade T-SQL stored procedure execution, ACID transaction durability, multiple wire protocols, and an embedded web management studio.

- **Storage Backends**: In-Memory OCC (Optimistic Concurrency Control with WAL & AOF compaction) and WiredTiger (Lock-free B+Tree with MVCC & hazard pointers).
- **Wire Protocols**:
  - PostgreSQL Wire Protocol v3 (TCP `:5433` by default)
  - Microsoft SQL Server TDS 7.x/8.0 Protocol (TCP `:1433` by default)
  - REST & JSON HTTP API / Web Studio (TCP `:8090` by default)
- **Execution Engine**: Native SQLite VDBE-style T-SQL Bytecode Virtual Machine with `sync.Pool` register reuse.

---

## 2. Core Architectural Invariant: Dual-Parser & Automatic Fallback Strategy

SQLion enforces a **Dual-Parser Architecture** to simultaneously achieve sub-microsecond parsing latency and 100% full-spec Microsoft T-SQL syntax compatibility:

```
                  [ T-SQL Client Script / Query ]
                                │
                                ▼
                 [ Primary: Native Pratt Parser ]
                 (pkg/proc/lexer.go, parser.go)
                                │
                    ┌───────────┴───────────┐
                 (Success)               (Failure)
                    │                       │
                    │                       ▼
                    │           [ Secondary: ANTLR4 Fallback ]
                    │           (pkg/proc/parser/antlr/)
                    │           - Official TSqlParser.g4
                    │           - ASTBuilder substring slice
                    │                       │
                    └───────────┬───────────┘
                                │
                                ▼
                 [ Unified AST: *proc.CreateProcStmt ]
                                │
                                ▼
                 [ Native VDBE Bytecode Compiler ]
                                │
                                ▼
                 [ Bytecode VM & Storage Execution ]
```

### Key Requirements & Principles:
1. **Primary Fast-Path (Native Pratt / Recursive Descent Parser)**:
   - Location: `pkg/proc/lexer.go`, `pkg/proc/parser.go`
   - Purpose: Zero-allocation, sub-microsecond parsing for high-frequency queries, standard DDL/DML, and core procedure control flow statements (`IF`, `WHILE`, `TRY...CATCH`, `SELECT...INTO`, `UPDATE`, `INSERT`, `DELETE`).
2. **Secondary Full-Spec Fallback (Official Microsoft T-SQL ANTLR4 Parser)**:
   - Location: `pkg/proc/parser/antlr/`
   - Purpose: Invoked automatically as a runtime fallback whenever the primary parser encounters advanced, vendor-specific, or legacy MSSQL syntax (e.g. `WITH RECOMPILE`, `WITH ENCRYPTION`, schema prefixes `dbo.ProcName`, bracket escapes `[dbo].[Proc]`, vendor options).
3. **Pluggable Decoupling (Circular Dependency Immunity)**:
   - `pkg/proc` defines the `ProcedureFallbackParser` hook and `RegisterProcedureFallback()` / `GetProcedureFallback()` API.
   - `pkg/proc/parser/antlr/ast_builder.go` registers itself via `init()`.
   - `pkg/proc` never directly imports `pkg/proc/parser/antlr`, preserving strict architectural hierarchy and zero circular imports.
4. **Permanent Invariance**:
   - Both parsers and the fallback mechanism **must be permanently maintained and tested**.
   - Any new syntax extension should first be verified against ANTLR, and dual parity must be confirmed in `pkg/proc/runtime/regression_test.go`.

---

## 3. Core Architectural Invariant: Database Isolation Strategy (Instance-Based Isolation)

SQLion adopts **Instance-Level Isolation (Process / Container per Database)** as a core architectural invariant, rather than multi-database catalog multiplexing (`USE <dbname>`) inside a single process:

1. **Architectural Rationale**:
   - **Fault & Resource Isolation**: Prevents runaway queries, memory leaks, or file descriptor saturation in one database from impacting others.
   - **Security & Blast Radius**: Eliminates cross-database data leaks and ensures strict physical isolation of data files, WAL/AOF logs, TDE encryption keys, and network listening ports.
   - **Independent Lifecycle**: Enables independent scaling, upgrading, point-in-time recovery (PITR), and restarts per database.
   - **KISS & Zero Redundant Complexity**: Eliminates cross-database catalog locking, session database context switching overhead, and distributed lock contention in the database kernel.
2. **Permanent Specification Invariant**:
   - **`USE <dbname>` Syntax**: Explicitly **Spec Out**. Each SQLion process serves exactly one isolated database catalog per data directory (`--data-dir`).
   - **Multi-Tenant Deployment Standard**: Multi-tenancy and multiple databases are deployed via lightweight SQLion standalone processes/containers with dedicated ports and directories (`--port`, `--data-dir`).

---

## 4. Core Architectural Invariant: Transaction Isolation & Concurrency Model

SQLion defines **Non-Blocking Snapshot Isolation / Read Committed with Optimistic/Hazard Concurrency Control** as its canonical transaction processing invariant across both storage engines:

1. **Architectural Principles**:
   - **Non-Blocking Reads (Readers Never Block Writers, Writers Never Block Readers)**: Both backends completely eliminate traditional 2PL (Two-Phase Locking) read locks. Queries always read from consistent snapshots without acquiring shared locks.
   - **Private Workspace Isolation**: All mutation operations (INSERT, UPDATE, DELETE) are buffered inside transaction-private workspaces until commit, guaranteeing zero dirty reads and zero uncommitted leakage.
   - **Storage Engine Concurrency Profiles**:
     - **In-Memory OCC (`pkg/storage/memengine`)**: Pure OCC (Optimistic Concurrency Control) using per-record monotonic integer versions (`Version uint64`). Concurrency conflicts are validated atomically at the commit phase (`Commit()`), triggering immediate fail-fast rollback on write-write skew (`ErrWriteConflict`).
     - **WiredTiger MVCC (`pkg/storage/wiredtiger`)**: Disk-based MVCC B+Tree with hazard pointers and transaction timestamps. Concurrent conflicting writes on identical keys are detected during write/commit operations (`WT_ROLLBACK`).
2. **Permanent Specification Invariant**:
   - **Standard Isolation Level**: Canonical Snapshot / Read Committed semantics are enforced across all wire protocols (PGWire, TDS, HTTP).
   - **Pessimistic Read Locks (`READ UNCOMMITTED`, Heavy Distributed 2PL, SSI Graph Tracking)**: Explicitly **Spec Out / Deferred**. Advanced isolation levels that introduce false abort penalties, serializable dependency graphs, or dirty read compromises are excluded in favor of high-throughput non-blocking OLTP execution.

---

## 5. Core Architectural Invariant: Self-Contained Single Binary (Zero-External-Dependency)

SQLion is architected as an **Autonomous, Zero-External-Dependency Standalone Static Executable**:

1. **Architectural Principles**:
   - **Full-Stack Self-Containment**: Database kernel, multi-protocol wire listeners (PGWire :5433, TDS :1433), HTTP/REST engine (:8090), embedded Web Studio (`embed.FS`), Goroutine connection pooler, cron job scheduler, and SMTP mailer run out of a single static binary.
   - **Zero Operational Scaffolding**: Eliminates external sidecars or daemons (No PgBouncer, No Redis cache layer, No external web server like Nginx, No OS crontab dependencies).
2. **Permanent Specification Invariant**:
   - **External Distributed Coordinators**: ZooKeeper, etcd, Consul, or external message brokers (Kafka, RabbitMQ) are explicitly **Spec Out** from the core database kernel.
   - **Deployment Simplicity**: A fully operational database node requires nothing more than copying the single executable and running it against a target directory (`--data-dir`).

---

## 6. Core Architectural Invariant: Scale-Up Primacy & WAL Replication Topology

SQLion prioritizes **Vertical Scale-Up Hardware Saturation** and **1-Writer Multi-Reader Asynchronous/Semi-Synchronous WAL Replication** over complex distributed multi-master sharding:

1. **Architectural Principles**:
   - **Single-Node Hardware Saturation**: Focuses engineering effort on maximizing multi-core CPU concurrency, lock-free data structures (Concurrent SkipList, Hazard Pointers), NVMe I/O efficiency, and sub-microsecond VDBE bytecode execution on a single host.
   - **Zero Distributed Consensus Penalty on Writes**: Eliminates distributed 2PC (Two-Phase Commit) network round-trip latency on write paths, preserving raw single-node transaction throughput (16,000+ TPS).
2. **Standard High-Availability & Read-Scale Topology (1-Writer Multi-Reader)**:
   - **Primary Node (Single Writer)**: Serves all write mutations (INSERT, UPDATE, DELETE), enforces OCC/MVCC validation, generates canonical WAL/AOF log streams, and handles read queries.
   - **Replica Nodes (Multi-Reader)**: Continuously stream and replay WAL records from the Primary via LSN tracking, providing scale-out read capacity (SELECT) without lock contention.
   - **Failover Promotion**: High availability is achieved by promoting the most up-to-date LSN Replica to Primary upon primary failure, rather than complex runtime distributed consensus.
3. **Permanent Specification Invariant**:
   - **Multi-Master Distributed Sharding & 2PC Graphs**: Explicitly **Spec Out / Deferred**. Core engine complexity remains bounded to single-node ACID execution and linear WAL log stream shipping.

---

## 7. Core Architectural Invariant: Benchmark & Transaction Modeling Fidelity (Atomic ACID Invariance)

SQLion mandates **Strict Atomic ACID Transaction Modeling** across all benchmarks (TPC-C, TPC-H, microbenchmarks), test harnesses, and stored procedure workloads:

1. **All-or-Nothing Transaction Fidelity**:
   - Multi-statement business transactions (e.g. TPC-C New-Order consisting of warehouse read, district counter increment, customer lookup, order/new_order insertion, and 5~10 order line/stock updates) **must strictly execute within a single atomic ACID transaction boundary** (`sess.BeginTx()` -> `tx.Commit()`).
   - Splitting a single logical business transaction across multiple autocommit statements or separate ambient transactions (e.g. looping with `exec.Run()`) is **strictly prohibited**.
2. **VDBE Bytecode VM Transaction Coupling**:
   - Multi-step stored procedure sequences or partitioned VDBE bytecode programs running under benchmark harnesses must execute via `exec.RunInTx(prog, params, tx, nil)`, sharing the explicit transaction handle `tx`.
   - Any transaction failure, conflict abort, or rollback inside a step must immediately trigger `tx.Rollback()`, leaving no partial writes or dangling records.

---

## 8. Engineering Guidelines & Development Pipeline

### 3-Stage Verification Pipeline (Build ➔ Test ➔ Commit)
All modifications, refactoring, and feature additions must strictly pass the following 3 stages before committing:

1. **Build**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
   ```
2. **Test (Race-Free Verification)**:
   ```bash
   go test -tags wiredtiger -count=1 -v -race ./...
   ```
   Must achieve 100% PASS across all packages with 0 data races.
3. **Commit**:
   - Conventional Commits format (`feat(...)`, `fix(...)`, `refactor(...)`, `docs(...)`, etc.).
   - Strictly avoid decorative emojis in commit messages, comments, and code.

> **Exception (Documentation-Only Commits)**: For pure markdown (`*.md`) and text documentation modifications (`docs: ...`) that do not impact configurations or runtime behavior, the build and test steps may be bypassed and committed immediately.

---

## 9. Developer & AI Handover Context Notes (`notes/`) & Roadmap
When working across different development environments or initiating a new AI agent session, refer to the following documentation for engineering context, guidelines, and backlogs:
- [`notes/ENGINEERING_CONTEXT.md`](notes/ENGINEERING_CONTEXT.md): Architectural invariants, why alternatives were spec'd out, and collaboration principles.
- [`notes/PITFALLS_AND_TRICKS.md`](notes/PITFALLS_AND_TRICKS.md): Circular import immunity, wiredtiger build tags, sandbox network handling, Decimal JSON serialization, and VDBE transaction coupling.
- [`TODO.ko.md`](TODO.ko.md): Active engineering backlog, immediate priorities (P1, P2, P3), CBO & performance matrices, and verified milestones.
- [`ROADMAP.md`](ROADMAP.md) ([`ROADMAP.ko.md`](ROADMAP.ko.md)): High-level release milestones (Phase 1~6), architectural evolution, and verified implementation status.


