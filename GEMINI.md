# LionSQL Project Architecture & Engineering Guidelines

(한국어 공식 가이드라인 문서는 [GEMINI.ko.md](GEMINI.ko.md)를 참조하세요.)

## 1. Project Overview
LionSQL is a high-concurrency embedded OLTP database engine written in Go, packaged as a single standalone executable. It delivers enterprise-grade T-SQL stored procedure execution, ACID transaction durability, multiple wire protocols, and an embedded web management studio.

- **Storage Backends**: In-Memory OCC (Optimistic Concurrency Control with WAL & AOF compaction) and WiredTiger (Lock-free B+Tree with MVCC & hazard pointers).
- **Wire Protocols**:
  - PostgreSQL Wire Protocol v3 (TCP `:5433` by default)
  - Microsoft SQL Server TDS 7.x/8.0 Protocol (TCP `:1433` by default)
  - REST & JSON HTTP API / Web Studio (TCP `:8090` by default)
- **Execution Engine**: Native SQLite VDBE-style T-SQL Bytecode Virtual Machine with `sync.Pool` register reuse.

---

## 2. Core Architectural Invariant: Dual-Parser & Automatic Fallback Strategy

LionSQL enforces a **Dual-Parser Architecture** to simultaneously achieve sub-microsecond parsing latency and 100% full-spec Microsoft T-SQL syntax compatibility:

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

LionSQL adopts **Instance-Level Isolation (Process / Container per Database)** as a core architectural invariant, rather than multi-database catalog multiplexing (`USE <dbname>`) inside a single process:

1. **Architectural Rationale**:
   - **Fault & Resource Isolation**: Prevents runaway queries, memory leaks, or file descriptor saturation in one database from impacting others.
   - **Security & Blast Radius**: Eliminates cross-database data leaks and ensures strict physical isolation of data files, WAL/AOF logs, TDE encryption keys, and network listening ports.
   - **Independent Lifecycle**: Enables independent scaling, upgrading, point-in-time recovery (PITR), and restarts per database.
   - **KISS & Zero Redundant Complexity**: Eliminates cross-database catalog locking, session database context switching overhead, and distributed lock contention in the database kernel.
2. **Permanent Specification Invariant**:
   - **True Multi-Database Catalog Multiplexing**: Explicitly **Spec Out**. Each LionSQL process serves exactly one isolated database catalog per data directory (`--data-dir`).
   - **`USE <dbname>` Tolerant Virtual No-Op Policy**: To guarantee 100% interoperability with MSSQL export scripts, database migrations, and tools like DBeaver, SSMS, and sqlcmd, the `USE <dbname>` (`USE master`, `USE [SalesDB]`) statement is accepted as a **Tolerant Virtual No-Op**. The server synchronizes session virtual database state (`ClientSession.Database`, `DB_NAME()`, `CURRENT_DATABASE()`) and emits official TDS environment change tokens (`0xE3 ENVCHANGE`), eliminating syntax errors without physical catalog switching.
   - **Multi-Tenant Deployment Standard**: Multi-tenancy and multiple databases are deployed via lightweight LionSQL standalone processes/containers with dedicated ports and directories (`--port`, `--data-dir`).

---

## 4. Core Architectural Invariant: Transaction Isolation & Concurrency Model

LionSQL defines **Non-Blocking Snapshot Isolation / Read Committed with Optimistic/Hazard Concurrency Control** as its canonical transaction processing invariant across both storage engines:

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

LionSQL is architected as an **Autonomous, Zero-External-Dependency Standalone Static Executable**:

1. **Architectural Principles**:
   - **Full-Stack Self-Containment**: Database kernel, multi-protocol wire listeners (PGWire :5433, TDS :1433), HTTP/REST engine (:8090), embedded Web Studio (`embed.FS`), Goroutine connection pooler, cron job scheduler, and SMTP mailer run out of a single static binary.
   - **Zero Operational Scaffolding**: Eliminates external sidecars or daemons (No PgBouncer, No Redis cache layer, No external web server like Nginx, No OS crontab dependencies).
2. **Permanent Specification Invariant**:
   - **External Distributed Coordinators**: ZooKeeper, etcd, Consul, or external message brokers (Kafka, RabbitMQ) are explicitly **Spec Out** from the core database kernel.
   - **Deployment Simplicity**: A fully operational database node requires nothing more than copying the single executable and running it against a target directory (`--data-dir`).

---

## 6. Core Architectural Invariant: Scale-Up Primacy & WAL Replication Topology

LionSQL prioritizes **Vertical Scale-Up Hardware Saturation** and **1-Writer Multi-Reader Asynchronous/Semi-Synchronous WAL Replication** over complex distributed multi-master sharding:

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

LionSQL mandates **Strict Atomic ACID Transaction Modeling** across all benchmarks (TPC-C, TPC-H, microbenchmarks), test harnesses, and stored procedure workloads:

1. **All-or-Nothing Transaction Fidelity**:
   - Multi-statement business transactions (e.g. TPC-C New-Order consisting of warehouse read, district counter increment, customer lookup, order/new_order insertion, and 5~10 order line/stock updates) **must strictly execute within a single atomic ACID transaction boundary** (`sess.BeginTx()` -> `tx.Commit()`).
   - Splitting a single logical business transaction across multiple autocommit statements or separate ambient transactions (e.g. looping with `exec.Run()`) is **strictly prohibited**.
2. **VDBE Bytecode VM Transaction Coupling**:
   - Multi-step stored procedure sequences or partitioned VDBE bytecode programs running under benchmark harnesses must execute via `exec.RunInTx(prog, params, tx, nil)`, sharing the explicit transaction handle `tx`.
   - Any transaction failure, conflict abort, or rollback inside a step must immediately trigger `tx.Rollback()`, leaving no partial writes or dangling records.

---

## 8. Core Architectural Invariant: Engine Simplicity & Explicit Spec-Out Decisions

LionSQL permanently designates the following architectural models as **Spec Out (Excluded / Non-Goals)** to protect sub-microsecond transaction performance, single-node hardware saturation, and zero-dependency self-containment:

1. **Unified Procedural Runtime & PL/pgSQL Interpreter Exclusion (Spec-Out)**:
   - Stored procedure and UDF execution is strictly unified under the high-performance SQLite VDBE-style T-SQL bytecode virtual machine (`pkg/proc`).
   - PGWire functions solely as a client I/O gateway for standard PostgreSQL drivers, ORMs, and tools. Implementing a redundant second procedural interpreter (PL/pgSQL) inside the database kernel is explicitly **Spec Out** to prevent engine bloat, duplicated execution paths, and security attack surface duplication (KISS & DRY).
2. **Modern JSON Standardization & Legacy XML DML / XQuery Exclusion (Spec-Out)**:
   - Semi-structured document processing is standardized around T-SQL and ANSI JSON functions (`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`, `FOR JSON PATH / AUTO`) backed by Go's native high-speed parser.
   - 2000s legacy Microsoft XML schema validation, XML data types, and XML DML (`.modify()`, `.nodes()`, `.value()`, XQuery engines) are explicitly **Spec Out** to eliminate memory bloat and parser complexity.
3. **In-Memory OCC Storage: Background Vacuum Thread Exclusion (Spec-Out)**:
   - The in-memory OCC engine (`memengine`) buffers transaction mutations in private workspaces and atomically swaps versions on commit or discards workspaces on rollback, never accumulating dead-tuple chains in memory.
   - Background multi-version dead tuple cleanup threads (PostgreSQL-style Vacuum workers) that consume background goroutines, scan memory, and cause lock thrashing are explicitly **Spec Out** as YAGNI over-engineering.
4. **CDC Event Streaming Standardization & POSIX Shared Memory (Arrow IPC) Exclusion (Spec-Out)**:
   - Transaction-committed CDC/WAL event streaming is the unified pipeline for both DuckDB OLAP micro-batch ingestion and external message broker forwarding.
   - Static Apache Arrow IPC shared memory snapshotting (`/dev/shm`) that incurs massive table full-scan CPU overhead and OS non-portability (Docker container 64MB limits, lack of Windows support) is explicitly **Spec Out**.
5. **External Distributed Transaction Coordinators (XA / MS DTC) Exclusion (Spec-Out)**:
   - Network two-phase commit protocols like XA and Microsoft Distributed Transaction Coordinator (MS DTC) are explicitly **Spec Out** to preserve single-node 16,000+ TPS transaction throughput. Distributed consistency is achieved via asynchronous/semi-synchronous WAL replication and idempotent consumer processing.
6. **In-Kernel OAuth2 Web Flow Exclusion & Client-Side Token Exchange Standardization**:
   - Database kernel security and identity management is strictly unified around standard enterprise database authentication mechanisms: SCRAM-SHA-256 for PGWire, SQL Server standard/NTLM authentication for TDS, and secure token-based RBAC with JWT/API-Key for HTTP management.
   - Embedding external consumer OAuth2 web authentication (Google, GitHub OAuth2 HTTP endpoints, redirect handlers, client secret lifecycle management, and outbound internet token exchange calls) directly inside the database kernel binary is explicitly **Spec Out**. This eliminates outbound internet dependencies, prevents client secret exposure risks, minimizes the security attack surface, and maintains absolute database kernel purity (KISS & Golden Rule #15). Enterprise SSO/Identity Federation can alternatively be handled by standard perimeter proxies (Keycloak, OAuth2-Proxy).
   - **Client-Side Token Exchange Architecture (Recommended / Standard Pattern)**: When direct social login integration is desired, LionSQL standardizes on the **Client-Side OIDC Token Exchange** pattern (RFC 8693 / OpenID Connect ID Token verification):
     1. Client applications (browser, mobile app) authenticate directly with Identity Providers (Google, Apple, GitHub) using native SDKs.
     2. The client transmits the cryptographically signed OIDC ID Token (JWT) to LionSQL via `POST /api/auth/token-exchange` or `Authorization: Bearer <token>`.
     3. LionSQL kernel (`pkg/auth`) verifies the JWT cryptographic signature against the IdP's cached public keys (JWKS) and extracts user claims (`sub`, `email`) to issue internal database session tokens—requiring zero client secrets, zero browser redirect handlers, and zero session cookies in the database kernel.
7. **In-Kernel S3 Blob File Storage & Image Thumbnail Processing Exclusion (Spec-Out)**:
   - Embedding binary blob storage servers, multipart S3 streaming, and CPU-intensive image resizing/thumbnailing directly inside the high-concurrency database kernel process is explicitly **Spec Out** to preserve ultra-low latency OLTP execution and avoid runtime memory/CPU starvation (Golden Rules 14 & 15).
   - Object storage and media pipelines are standardly delegated to dedicated object storage services (AWS S3, MinIO, Cloudflare R2) and edge CDNs, while LionSQL stores the canonical URI/metadata pointers in relational tables.
8. **Premature Pure-Go Persistent Storage Engine (Pebble/bbolt) Adoption Exclusion (Spec-Out & Continuous Exploration)**:
   - Current open-source Pure-Go embedded key-value engines (Pebble, bbolt, Badger) are not yet mature enough to serve as production-grade alternatives to WiredTiger's lock-free multi-core MVCC B+Tree, hazard pointers, transaction timestamps, and strict ACID isolation. Introducing an immature third storage backend incurs unnecessary complexity, maintenance overhead, and performance compromises (Golden Rules 14 & 15).
   - Embedding an unviable Pure-Go storage engine is officially **Spec Out** for the 1.0 production release. LionSQL focuses engineering exclusively on its two proven backends: disk-based WiredTiger MVCC and pure in-memory OCC (`memengine`). Potential pure-Go storage engine candidates will continue to be evaluated and explored as the ecosystem matures.
9. **In-Kernel Slow Query Webhook Direct Dispatch Exclusion (`OBS-3`) (Spec-Out & Post-1.0 Tooling Idea)**:
   - Embedding outbound HTTP notification clients (Slack, Discord, generic webhooks) directly inside the database kernel process violates the Single Responsibility Principle, KISS, and engine purity (Golden Rules 14 & 15). Direct external HTTP calls from the kernel introduce network RTT latency, timeout handling, retry buffer queues, and potential thread starvation.
   - Standard observability pipelines are already unified around Prometheus metrics (`OBS-1`), runtime execution profiler (`OBS-2`, `EXPLAIN ANALYZE`), and immutable audit logging. External monitoring tools (Alertmanager, Grafana, Vector) handle alerting independently. In-kernel webhook dispatch is officially **Spec Out**, deferred as an external sidecar/CLI tooling idea for future exploration.
10. **In-Kernel WebAssembly (Wasm) UDF Sandbox (`wazero`) Exclusion (Spec-Out & Post-1.0 Exploratory Idea)**:
   - Embedding a full Wasm virtual machine and bytecode interpreter (`wazero`) inside the database kernel incurs substantial binary bloat, runtime memory overhead, and JIT/Wasm lifecycle management complexity with minimal real-world OLTP demand.
   - High-performance procedural and function execution is already unified under the SQLite VDBE-style T-SQL virtual machine (`pkg/proc`) and zero-allocation Go Native UDF SPI (`proc.RegisterScalarFunction`, <5ns). Embedding an unneeded third runtime is officially **Spec Out** for 1.0, to be re-evaluated as an exploratory idea in post-1.0 versions.

---

## 9. Engineering Guidelines & Development Pipeline

### 3-Stage Verification Pipeline (Build ➔ Test ➔ Commit)
All modifications, refactoring, and feature additions must strictly pass the following 3 stages before committing:

1. **Build**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
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

### Continuous Backlog & TODO Discipline (`TODO.ko.md` Auto-Sync Invariant)
Whenever new features, architectural decisions (ADRs), or technical requirements are discussed and agreed upon during a conversation, the AI agent must **proactively and automatically register them in `TODO.ko.md`** (under Section 1 Priority table and Section 2 Domain-specific backlog) within the same session without waiting for an explicit user command. Likewise, whenever a task or refactoring is completed and passes verification, the agent must immediately mark it as complete (`[x]`) and record a brief implementation summary.

---

## 10. Developer & AI Handover Context Notes (`notes/`) & Roadmap
When working across different development environments or initiating a new AI agent session, refer to the following documentation for engineering context, guidelines, and backlogs:
- [`notes/ENGINEERING_CONTEXT.md`](notes/ENGINEERING_CONTEXT.md): Architectural invariants, why alternatives were spec'd out, and collaboration principles.
- [`notes/PITFALLS_AND_TRICKS.md`](notes/PITFALLS_AND_TRICKS.md): Circular import immunity, wiredtiger build tags, sandbox network handling, Decimal JSON serialization, and VDBE transaction coupling.
- [`TODO.ko.md`](TODO.ko.md): Active engineering backlog, immediate priorities (P1, P2, P3), CBO & performance matrices, and verified milestones.
- [`CODE_REVIEW.ko.md`](CODE_REVIEW.ko.md): Quality audit log, code review findings, defect fixes, and 3-stage verification tracking.
- [`REFACTORING.ko.md`](REFACTORING.ko.md): Architectural abstraction specifications, modular layer decoupling, and deletion-first refactoring blueprints.
- [`ROADMAP.md`](ROADMAP.md): High-level release milestones (Phase 1~6), architectural evolution, and verified implementation status.


