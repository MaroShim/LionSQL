# LionSQL Engineering Roadmap & Strategic Release Horizons

This document defines the overarching architectural trajectory, versioned milestones, release criteria, and strategic horizons of **LionSQL**—a high-concurrency embedded OLTP database engine and multi-protocol stored procedure server.

> [!NOTE]
> For active daily engineering tasks, prioritized sprint backlogs, and detailed implementation histories, refer to the operational backlog in [TODO.md](TODO.md).

---

## 1. Release Evolution & Milestone Progression

LionSQL follows an evolutionary roadmap designed to scale single-node hardware to its physical limits while maintaining zero-dependency single-binary distribution:

```
[ Phase 1: Core Foundation ] ────────────► v0.1.0 - v0.8.0   [COMPLETED]
  Dual Wire Protocols (PGWire/TDS) + Dual Storage (WiredTiger/OCC) + VDBE Bytecode VM

[ Phase 2: SQL & Analytical Depth ] ─────► v0.9.0 - v0.10.0  [COMPLETED]
  Window Functions + T-Digest Quantiles + Log Operators + Savepoints + Views

[ Phase 3: Replication & Drivers ] ──────► v0.11.0 - v0.12.0 [COMPLETED]
  1-Writer Multi-Reader WAL Streaming + Official Drivers + CBO Stats Persistence

[ Phase 4: Ecosystem & VDBE Acceleration ] v0.13.0 - v0.15.0 [COMPLETED]
  pgx/v5 Pipelining + Multi-Tool Interop + VDBE Opcode Specialization (21k TPS)

[ Phase 5: Enterprise Governance ] ──────► v0.16.0 - v0.24.0 [COMPLETED]
  Engine Decoupling (pkg/engine) + Group Commit + RBAC + CBO DP Join Order + Prepared LRU Cache

[ Phase 6: Production Hardening & Feature Freeze ] ► v0.90.0 (Betas 0~3) [COMPLETED]
  Unified CLI (lionsql) + Web Studio Suite + Memory Governor + OCC Tuning + TDS Positional + 2-Tier Engine

[ Phase 7A: P0 Zero-Defect Reliability Baseline ] ─► v0.92.0 [ACTIVE / BATCH 5 IN PROGRESS]
  100-Scope Pre-Release Audit P0 (Must-Fix 31 Items) 100% Resolved + Zero OOM/Crash/Corruption/Security Bypass Baseline

[ Phase 7B: P1 Hardening & Pre-Release Polish ] ───► v0.93.0 - v0.95.0 (RC1) [SCHEDULED]
  P1 (53 Items) Enterprise Polish + 6 Golden Release Gates Automation + Soak Test Verification

[ Phase 8: Official 1.0 LTS Production Release ] ────► v1.0.0 LTS [TARGET Q4 2026]
  6 Golden Release Gates Passed + 100-Step Audit Sign-Off + Always On Failover + Lossless CDC + LTS Packaging (WiredTiger MVCC + In-Memory OCC)

[ Phase 9: Scale-Up Query & Pure-Go Storage Preview ] ──► v1.1.0 - v1.2.0 [TARGET Q1-Q2 2027]
  Flat Hash Join & Mini TPC-H + OCC-Dense Default Graduation + Pure-Go Native Engine Preview (--storage=purego) + Immutable Audit Log

[ Phase 10: Multi-Engine Expansion: Pure-Go LTS ] ────► v1.5.0 LTS [TARGET Q3-Q4 2027]
  Native Pure-Go Storage Production Graduation + Dual Persistent Engine Spectrum (WiredTiger CGo + Pure-Go CGo-Free) + Parallel OLAP

[ Phase 11: Distributed Scale-Out & Next-Gen Cloud ] ─► v2.0.0+ [TARGET 2028+]
  Raft Consensus Multi-Node Replication + Distributed Sharding + Dynamic Partition Rebalancing (Major Breaking Architecture)
```

### High-Level Milestone Dashboard

| Milestone | Target Version | Focus & Core Deliverables | Completion Status |
| :---: | :---: | :--- | :---: |
| **Phase 1** | `v0.1.0` - `v0.8.0` | **Core Foundation & Virtual Machine Architecture**<br>Dual wire protocols (:5433, :1433), dual storage engines, SQLite-style VDBE VM, CBO histograms. | **100% Completed** |
| **Phase 2** | `v0.9.0` - `v0.10.0` | **SQL & Analytical Depth, Aggregations & Views**<br>Window functions, T-Digest quantiles, in-engine log operators (GREP/AWK), savepoints, views. | **100% Completed** |
| **Phase 3** | `v0.11.0` - `v0.12.0` | **Replication, Official Drivers & Zero-Allocation**<br>1-Writer multi-reader WAL streaming replication, official driver compatibility, buffer pooling. | **100% Completed** |
| **Phase 4** | `v0.13.0` - `v0.15.0` | **Modern Ecosystem, GUI Tool Interop & VDBE Acceleration**<br>pgx/v5 pipelining, DBeaver/SSMS interop, UUIDv7, VDBE opcode specialization (21k TPS peak). | **100% Completed** |
| **Phase 5** | `v0.16.0` - `v0.24.0` | **Enterprise Governance, Security & Engine Decoupling**<br>Layer separation (`pkg/engine`), WAL group commit, RBAC, CBO DP join ordering, migrations CLI, prepared LRU cache. | **100% Completed** |
| **Phase 6** | `v0.90.0`<br>(Betas 0~3) | **Production Hardening & Feature Freeze Baseline**<br>Single-binary CLI (`lionsql`), Web Studio suite, memory governor, OCC 5-point tuning, TDS positional mapping, 2-Tier query engine. | **100% Completed** |
| **Phase 7A** | `v0.92.0` | **P0 Zero-Defect Reliability Baseline**<br>Comprehensive 100-scope code audit with P0 (Must-Fix 31 items) 100% resolved. Zero OOM/crash/corruption/security injection guaranteed. Tagged as foundational reliability baseline. | **Active / 74% Complete** |
| **Phase 7B** | `v0.93.0` - `v0.95.0`<br>(RC1) | **P1 Hardening, Durability Verification & 6 Golden Release Gates**<br>P1 (53 items) enterprise stabilization, 10 production apps verified, `scripts/release.sh` 6 Golden Release Gates automated, `crashtest` (D1~D4), SQLLogicTest 409k, T-SQL 14-Suite, `soaktest` zero leaks. | **Scheduled** |
| **Phase 8** | `v1.0.0 LTS` | **Official 1.0 Production Release & Long-Term Support**<br>6 Golden Release Gates verified, 100-step audit sign-off, Always On HA, lossless CDC, certified drivers, LTS release sign-off (WiredTiger MVCC + In-Memory OCC). | **Target Q4 2026** |
| **Phase 9** | `v1.1.0` - `v1.2.0` | **Scale-Up Complex Query & Native Pure-Go Engine Preview**<br>Flat hash join, Mini TPC-H suite, `OCC-Dense` default graduation, Native Pure-Go B+Tree storage engine preview (`--storage=purego`, `CGO_ENABLED=0`), immutable audit log, multi-core `ParallelSeqScan`. | **Target Q1-Q2 2027** |
| **Phase 10** | `v1.5.0 LTS` | **Multi-Engine Portfolio Expansion: Pure-Go Production LTS**<br>Native Pure-Go storage engine promoted to first-class production tier, coexisting permanently alongside WiredTiger. Full static binary distribution without external C toolchains for lightweight edge/container deployments. | **Target Q3-Q4 2027** |
| **Phase 11** | `v2.0.0+` | **Distributed Scale-Out & Next-Gen Cloud Architecture**<br>Multi-node Raft consensus replication, distributed horizontal sharding, dynamic partition rebalancing, enterprise cross-datacenter active-active federation. | **Target 2028+** |

---

## 2. LionSQL 1.0 / v1.0.0 LTS Production Readiness & The 6 Golden Release Gates

To tag the official **LionSQL 1.0 / v1.0.0 LTS** release, every candidate build must strictly pass the **6 Golden Release Verification Gates** permanently automated in [`scripts/release.sh`](scripts/release.sh) (detailed in [`notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md`](notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md)), alongside enterprise ecosystem criteria:

```
                  [ The 6 Golden Release Verification Gates (scripts/release.sh) ]
                                                │
    ┌─────────────┬─────────────┬───────────────┴───────────────┬─────────────┬─────────────┐
    ▼             ▼             ▼                               ▼             ▼             ▼
[ Gate 1 ]    [ Gate 2 ]    [ Gate 3 ]                      [ Gate 4 ]    [ Gate 5 ]    [ Gate 6 ]
Core Unit &   13-Domain     TPC-C Extreme                   SQLLogicTest  T-SQL 14-     SIGKILL Crash
100k TPS      E2E Smoke     Load Benchmark                  409k Oracle   Proc Suites   & Soak Leak
(23 pkgs OK)  (Dual Engine) (>700k tpmC)                    (90 files OK) (100% PASS)   (D1~D4 / 0 FDs)
```

### 2.1. The 6 Automated Release Verification Gates (`scripts/release.sh`)
1. **Gate 1: Core Unit & Inline Performance Gate (23 Core Packages, 100k+ TPS Inline)**:
   - Executes `go test -tags wiredtiger ./pkg/...` across all 23 core packages.
   - Enforces the 100,000+ TPS inline execution speed gate (`BenchmarkFastPath`) and zero data races.
2. **Gate 2: 13-Domain E2E Integration Smoke Suite (Dual-Engine 100% PASS)**:
   - Automated via `scripts/verify_report.sh` across both In-Memory OCC and WiredTiger backends.
   - 13 domains verified: DDL/DML, Transactions, CBO Optimizer, Multi-Wire Listeners, BaaS REST APIs, RBAC, RLS, Hot Backup/Restore, CDC Streams, Connection Pooling, Cron Scheduler, and Mailer.
3. **Gate 3: TPC-C OLTP Extreme Load Benchmark (>700k tpmC Saturation)**:
   - Executes `bin/tpcc-runtime` under high-concurrency 100-terminal load.
   - Threshold: In-Memory OCC $\ge 750,000\text{ tpmC}$ and WiredTiger MVCC $\ge 680,000\text{ tpmC}$ under strict ACID boundaries.
4. **Gate 4: SQLLogicTest Relational Oracle (409,194 Queries, 100% Conformance)**:
   - Executes SQLite/PostgreSQL SQLLogicTest standard test files (`bin/sqllogictest test/slt/core/`).
   - 90 files and 409,194 queries pass with 100% identical row sets and zero syntax or type coercion errors.
5. **Gate 5: T-SQL Stored Procedure 14-Suite Convergence (100% PASS)**:
   - Executes `test/slt_proc/` covering Microsoft SQL Server procedural grammar:
   - Variables (`@var`), control flow (`IF/ELSE`, `WHILE`), `TRY...CATCH`, nested transactions, forward-only cursors, return codes, dynamic SQL (`sp_executesql`), table variables (`@table`), `GOTO`/labels, recursive CTEs, and parameter binds.
6. **Gate 6: `SIGKILL` Crash Recovery Oracle & Leak-Free Soak Sentinel**:
   - `bin/crashtest`: 4-point consistency oracles (D1 Commit Durability, D2 Atomicity, D3 Checksum Consistency, D4 Recoverability) under unexpected process termination (`kill -9`) with verified WiredTiger `transaction_sync` durability.
   - `bin/soaktest`: Verifies zero descriptor leaks (`open_fds`), zero memory/goroutine growth, and sub-1ms p99 latency under continuous workload.

### 2.2. Enterprise Ecosystem & Governance Criteria (100% Validated)
- **Multi-Protocol & Driver Conformance**: Certified zero-regression compatibility across Go (`pgx/v5`, `lib/pq`, `go-mssqldb`), Python (`psycopg3`, `pymssql`), Java JDBC (`pgjdbc`, `mssql-jdbc`), C ODBC (`psqlODBC`), and enterprise GUIs (DBeaver, SSMS, Azure Data Studio, Beekeeper Studio).
- **Production Ecosystem Conformance (10 Real-World Apps Zero Code Change)**:
  - 10 stock, unmodified open-source applications running flawlessly against LionSQL:
    - Gitea (Git & Code Hosting), Woodpecker CI (CI/CD Pipelines)
    - Vaultwarden (Secret & Password Management), Casdoor (SSO & IAM)
    - Apache Answer (Q&A Knowledge Base), AFFiNE (Notion/Doc Workspace), Vikunja (Kanban & Task Management)
    - Metabase (BI & Analytics, Dual-Protocol), Umami (Web Analytics), IOTA ERP (Enterprise Resource Planning)
  - Covering XORM, Diesel ORM, Prisma, Knex, and official JDBC drivers.
- **Enterprise Security & Object Governance**: TDE Transparent Data Encryption, SQL object-level RBAC (`GRANT`/`REVOKE`/`DENY`), HTTP JWT/API-Key RBAC, and general/slow query logging.
- **Autonomous Operational Tooling**: In-tree declarative schema migration CLI (`lionsql migrate`) and online non-blocking snapshot hot backup/restore (`POST /api/backup`).
- **100-Step Deep Code Review Sign-Off (`AUDIT-100-DUAL-REVIEW`)**: 199 Go source files across 100 micro-scopes audited via Gemini & Claude dual review for edge-case immunity prior to final LTS tagging.

---

## 3. Milestone Execution Status & Engineering History

### 3.1. Phase 1: Core Foundation & Virtual Machine Architecture (`v0.1.0` ~ `v0.8.0`, Completed)
* **Dual Wire Protocol Architecture (`pkg/wire`, `pkg/wire/tds`)**:
  Simultaneous listener architecture supporting PostgreSQL Wire Protocol v3 (`TCP :5433`) and Microsoft TDS 7.x/8.0 (`TCP :1433`) in a single executable.
* **Dual Storage Engines (`pkg/storage/memengine`, `pkg/storage/wiredtiger`)**:
  In-Memory OCC (Optimistic Concurrency Control with WAL/AOF durability) and WiredTiger (Lock-Free disk B+Tree with hazard pointers and transaction timestamps).
* **VDBE Bytecode Virtual Machine (`pkg/proc/runtime`)**:
  Register-based virtual machine executing Microsoft T-SQL stored procedures and triggers with sub-microsecond latency and `sync.Pool` register recycling.
* **Cost-Based Optimizer (CBO) & Histograms (`pkg/planner`)**:
  Equi-depth histograms, selectivity estimation, and Volcano iterator-style query execution.

### 3.2. Phase 2: SQL & Analytical Depth, Aggregations & Views (`v0.9.0` ~ `v0.10.0`, Completed)
* **Window Functions & Partitioning (`pkg/execution`)**:
  Standard SQL window functions (`ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`) over sliding windows and partitions.
* **Streaming Tail Quantiles (`PERCENTILE_CONT`, T-Digest)**:
  Centroid buffer approximation algorithm for tail percentile latency metrics without memory explosion.
* **In-Engine Log & Text Operators**:
  Embedded `GREP` and `AWK` operators for unstructured text analysis, nested transaction savepoints, and virtual relational views.

### 3.3. Phase 3: Replication, Official Drivers & Zero-Allocation (`v0.11.0` ~ `v0.12.0`, Completed)
* **1-Writer Multi-Reader WAL Streaming Replication (`pkg/replication`)**:
  Linear transaction stream replication via LSN tracking, enabling zero-contention read scale-out on replica nodes.
* **Official Client Driver Compatibility Certification**:
  Conducted driver certification across `go-mssqldb`, `lib/pq`, and `psqlODBC` with 100% parameter protocol compliance.
* **Buffer Pooling & CBO Stats Persistence**:
  Reusable scratch buffers for query execution and persistent CBO table statistics storage across restarts.

### 3.4. Phase 4: Modern Ecosystem, GUI Tool Interop & VDBE Acceleration (`v0.13.0` ~ `v0.15.0`, Completed)
* **PostgreSQL `pgx/v5` Extended Batch Pipelining (`pkg/wire`)**:
  Supported pipelined queries, binary parameter decoding, and high-throughput connection multiplexing.
* **Enterprise GUI Tool Conformance**:
  Achieved verified interoperability with SQL Server Management Studio (SSMS), Azure Data Studio, and DBeaver.
* **IETF RFC 9562 Monotonic UUIDv7 (`NEWID7()`)**:
  Time-ordered UUID generation guaranteeing sequential B+Tree indexing without random insertion fragmentation.
* **VDBE Instruction Specialization (21k TPS Peak)**:
  Specialized opcode dispatch loops achieving 21,000+ single-node transaction ops per second.

### 3.5. Phase 5: Enterprise Governance, Security & Engine Decoupling (`v0.16.0` ~ `v0.24.0`, Completed)
* **Core Engine Decoupling (`pkg/engine`)**:
  Clean architectural boundary separating protocol listeners, catalog coordination, and transaction sessions (`Coordinator` and `ClientSession`).
* **WAL Group Commit ($100\times$ Fsync Speedup)**:
  Adaptive group commit batching concurrent transaction sync calls into single disk flushes.
* **Granular SQL Object-Level RBAC (`pkg/auth`, `pkg/catalog`)**:
  `GRANT`, `REVOKE`, `DENY` security controls across tables, schemas, and stored procedures.
* **Dynamic Programming Join Reordering (Selinger DP)**:
  Optimal join tree ordering for $N \le 4$ and $N > 4$ relations, and in-tree schema migration CLI (`lionsql migrate`).

### 3.6. Phase 6: Production Hardening & Feature Freeze Baseline (`v0.90.0`, Completed)
* **Single Standalone Binary & Unified CLI (`cmd/lionsql`, `UNIFY-CLI`)**:
  Consolidated database kernel and administration tools under `bin/lionsql` with 12 subcommands (`serve`, `start`, `stop`, `restart`, `status`, `doctor`, `init-config`, `migrate`, `sql`, `backup`, `restore`, `version`).
* **Web Studio Production Feature Suite (`pkg/storage`, `pkg/engine`, `pkg/api`, `pkg/ui`)**:
  Realtime Live Stream SSE viewer (`/api/realtime`), Request Activity Logs latency profiler (`/api/logs/requests`), Visual Column Addition DDL modal (`+ Add Column`), and foreign key interactive jump links (`↗`).
* **Global Memory Governor & Query Circuit Breaker (`pkg/config`, `pkg/execution`, `cmd/server`)**:
  Implemented dynamic RAM budgeting (`max_memory`, 65% upper bound with 1GB OS reserve) and `work_mem: 64MB` circuit breakers in hash joins and external merge sort pipelines to prevent OS OOM kills.
* **2-Tier Query Execution Architecture Consolidation (`KISS-5`, `ARCH-2TIER-1`)**:
  Consolidated execution paths into Tier 1 fast-pass (<1µs VDBE) and Tier 2 Volcano CBO iterator, establishing permanent architectural simplicity.
* **TDS RPC Positional Mapping & Tool Audit (`TDS-RPC-AUDIT-1`, `TOOL-AUDIT-1`)**:
  Eliminated parameter name dependencies in `sp_prepare` / `sp_prepexec`, achieving verified compatibility with Beekeeper Studio, SSMS, DbGate, and DBeaver under 300+ concurrent connection stress tests.

### 3.7. Phase 7: Real-World Ecosystem, Durability Hardening & v0.91.0 Release (`v0.91.0` ~ `v0.95.0`, Active Milestone)
* **Official Release `v0.91.0` & Multi-Platform Static Binaries**:
  Packaged and published official standalone static binaries for macOS (arm64), Linux (amd64), and Windows (amd64) with zero external dynamic dependencies.
* **The 6 Golden Release Verification Gates (`scripts/release.sh`)**:
  Permanently integrated 6 mandatory automated verification gates into `scripts/release.sh` (Core Unit & 100k TPS, 13-Domain Smoke, TPC-C >700k tpmC, SQLLogicTest 409k queries, T-SQL 14-Suite, and SIGKILL Crash/Leak Sentinels), accompanied by technical whitepaper ([`notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md`](notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md)).
* **WiredTiger Crash Durability Hotfix (`crashtest`)**:
  Real-time detection and resolution of `transaction_sync=(enabled=true,method=none)` in `pkg/storage/wiredtiger/wt_engine.go` during Gate 6 crash testing, achieving 100% data integrity and zero committed row loss across 3 rounds of 7,853 transactions under non-graceful `kill -9` termination.
* **Single-Binary Embedded SPA Web Server Hosting (`WEB-SPA-1`)**:
  Delivered ahead of schedule (pulled forward from Phase 10). Implemented `--app=<path>` smart flag and `LIONSQL_APP` env var supporting single-file HTML and flat directory hosting with HTML5 History API fallback and strict Flat Directory Invariant (path traversal immunity).
* **Pre-Release 100-Step Dual AI Code Review (`AUDIT-100-DUAL-REVIEW`)**:
  Initiated comprehensive source code audit across all 199 pure Go files divided into 100 micro-scopes via Gemini (deep semantic reasoning) and Claude (adversarial edge cases). Scopes 001~020 completed with 18 defects cataloged and scheduled in [`notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md`](notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md).
* **10 Real-World Production App Integrations (Zero Code Change)**:
  Completed end-to-end integration and permanent hermetic regression test suites for 10 production applications:
  - **Gitea** (116 tables, XORM, dual TDS/PGWire)
  - **Vaultwarden** (47 Diesel ORM migrations, composite primary keys, cipher sync)
  - **Casdoor** (44 XORM tables, Casbin RBAC, OIDC/JWKS, dual TDS/PGWire)
  - **Ghost** (Node.js Bookshelf/Knex, dual-dialect, headless publishing)
  - **Apache Answer** (35 XORM tables, Q&A CRUD, user karma)
  - **AFFiNE** (Prisma ORM, recursive doc trees, block sync)
  - **Metabase** (Clojure/JDBC, MBQL visual query builder, multi-table derived joins, dual TDS/PGWire)
  - **Umami** (Next.js 16, Prisma ORM, 26 migrations, event tracking, stats aggregation)
  - **Woodpecker CI** (19 XORM tables, 32 migrations, CI/CD pipeline server)
  - **Vikunja** (80+ XORM migrations, recursive CTEs `WITH RECURSIVE`, JWT auth, Kanban task management)
* **Automated Crash Recovery Harness (`cmd/crashtest`, `DURA-KILL-1`)**:
  Implemented multi-worker crash test harness executing aggressive `kill -9` process kills, testing 4-point consistency oracles (D1 Commit Durability, D2 Atomicity, D3 Checksum Consistency, D4 Recoverability), double-kill idempotency, and torn WAL log-tail truncation recovery.
* **Long-Duration Soak Test Harness (`cmd/soaktest`, `DURA-SOAK-1`)**:
  Implemented 8-worker mixed PGWire workload generator collecting Prometheus metrics and tracking RSS/FD/disk/p99 trends, confirming strict handle hygiene and zero memory leak over sustained execution.
* **Structural Query Routing Refactoring (`ROUTING-AST-CATALOG-1`)**:
  Eliminated 45 regular expression and string matching heuristics in `pkg/wire`, replacing them with C typedef style AST query characteristics extraction (`proc.ExtractQueryCharacteristics`) and symbol table lookups (527ns dispatch).
* **Table-Driven Compatibility Architecture (`COMPAT-TABLE-DRIVEN-1`)**:
  Formalizing ad-hoc wire/catalog/engine branches into Go static struct dispatch tables (Parameter OID inference, Command tag normalization, Virtual view mapping, Dialect rewrite rules).
* **In-Memory OCC Hyper-Scaling**:
  Achieved 1,040,000 tpmC throughput under monotonic versioning, lockless catalog snapshots, and 0-alloc lazy serialization.
* **High-Density In-Memory Engine: `OCC-Dense` (`OCC-HYPER-2`)**:
  Implemented pointer-free flat record architecture (`pkg/storage/memengine`), flattening row data into CPU cacheline-friendly byte buffers with 8-byte aligned slots, NULL bitmasks, and Small String Optimization (SSO). Achieved $5.95\times$ single-row compression (1,017B ➔ 171B, 83.2% reduction) and 67.5% heap reduction on 100k rows (68.9MB ➔ 22.3MB) while reducing GC-tracked heap pointers to exactly 0 (complete elimination of GC pause jitter). Safely isolated behind an A/B dual-mode architecture keeping `OCCModeRaw` (original 1.04M tpmC engine) as the production default (`[RULE:SafeDefaults]`), while actively verifying `OCC-Dense` against 4 mandatory reliability gates (crash recovery, secondary indexes/DDL, full SLT/T-SQL suites, and 24h soak).
* **WiredTiger VDBE Lazy Tuple Decoding (`P-2`)**:
  Integrated `RawTupleCursor` with zero-allocation direct column extraction (`ExtractColumnFromTuple`) in VDBE scan/DML paths (`scanCursorRows`, `execUpdateWhere`, `execDeleteWhere`, `execSelectInto`), accelerating selective table scans by 6.59x, reducing heap allocations by 87.5%, and driving WiredTiger TPC-C throughput past 813,000 tpmC.

### 3.8. Phase 8: Official 1.0 LTS Production Release Readiness
With all Phase 1~6 deliverables, Phase 7 ecosystem integrations, and durability harnesses verified, all 6 Production Readiness Quality Gates are fully validated. LionSQL is staged for official `v1.0.0 LTS` tagging and long-term production distribution.

---

## 4. Architectural Invariants & Explicit Spec-Out Decisions

To protect sub-microsecond transaction performance, prevent kernel bloat, and maintain single-binary self-containment, LionSQL explicitly designates the following architectural models as **Spec-Out (Permanently Excluded / Non-Goals)**:

| Spec-Out Architecture | Rejection Rationale | Recommended Enterprise Alternative | Reference |
| :--- | :--- | :--- | :--- |
| **In-Kernel PL/pgSQL Interpreter** | Stored procedures are unified under the high-speed VDBE T-SQL virtual machine. Embedding a second procedural interpreter violates DRY and doubles attack surfaces. | Execute procedures via VDBE T-SQL VM (`CREATE PROCEDURE`) | GEMINI §3.1 |
| **In-Kernel OAuth2 Social Login** | Database kernels should never initiate outbound internet HTTP calls (Google/GitHub) or store web secrets. Binary wire protocols (TDS/PGWire) cannot perform browser redirects. | Client-side OIDC token exchange (`POST /api/auth/token-exchange`) | GEMINI §3.6 |
| **In-Kernel S3 Blob & Image Processing** | Multipart binary blob streaming and image resizing cause memory/CPU starvation, destroying OLTP p99 latencies. | External object storage (AWS S3, MinIO) via Pre-signed direct URLs | GEMINI §3.7 |
| **In-Memory Dead-Tuple Vacuum Threads** | The in-memory OCC engine isolates mutations in private workspaces and swaps versions on commit, never creating dead-tuple chains. PostgreSQL-style vacuum workers are YAGNI. | Lean OCC workspace swapping and SkipList index versioning | GEMINI §3.3 |
| **External Distributed Coordinators** | Avoid distributed consensus write penalties (2PC network round-trips) in core OLTP paths. | Single-node hardware saturation + linear WAL replication | GEMINI §3.5 |
| **MySQL-Specific DML & Tabular EXPLAIN** | PostgreSQL `ON CONFLICT DO UPDATE` and ANSI `MERGE` are fully implemented; MySQL `ON DUPLICATE KEY UPDATE` splits conflict handling into divergent paths. Flat 8-column tabular `EXPLAIN` flattens hierarchical CBO plan trees. | ANSI `MERGE`, PostgreSQL `ON CONFLICT`, and hierarchical indented/JSON plans | GEMINI §3.13 |

---

## 5. Future Horizons: Post-1.0 Roadmap (`v1.1.0+` - `v2.0.0+`)

The following capabilities represent strategic architectural expansions planned for post-1.0 releases:

```
                  [ Post-1.0 Architectural Evolution ]
                                   │
         ┌─────────────────────────┼─────────────────────────┬─────────────────────────┐
         ▼                         ▼                         ▼                         ▼
[ v1.1.0-v1.2.0: Scale-Up ] [ v1.2.0: Pure-Go Preview ] [ v1.5.0 LTS: Multi-Engine ] [ v2.0.0+: Cloud Scale ]
Flat Hash Join & TPC-H     Native Go B+Tree engine   Dual Persistent Engines    Raft Consensus Cluster
OCC-Dense Default          Opt-in (--storage=purego) WiredTiger + Pure-Go       Distributed Sharding
```

### 5.1. Native Pure-Go B+Tree Storage Engine (Experimental `v1.2.0` -> Production LTS `v1.5.0`)
- **Objective & Multi-Engine Architectural Spectrum**:
  - Implement a 100% pure-Go persistent storage backend satisfying the canonical `storage.Engine` interface, featuring a lock-free hazard-pointer B+Tree, MVCC timestamped transaction isolation, and autonomous block caching.
  - **Permanent Multi-Engine Coexistence (Zero Deprecation)**:
    - **WiredTiger MVCC Engine (CGo / Enterprise High-Performance Tier)**: Permanently retained as LionSQL's battle-tested, high-throughput storage flagship for hardware-saturating on-premises and enterprise production workloads.
    - **Native Pure-Go Storage Engine (CGo-Free / Cloud-Native Tier)**: Introduced as an autonomous, zero-dependency alternative enabling pure `CGO_ENABLED=0` compilation, simplified cross-platform deployment, and frictionless operation in lightweight distroless containers and Windows environments.
- **Phased Graduation Trajectory**:
  - **`v1.2.0` (Experimental Developer Preview)**:
    Shipped as an opt-in storage backend (`--storage=purego` or `LIONSQL_STORAGE=purego`) under `CGO_ENABLED=0` builds, enabling field testing, crash durability benchmarking, and community verification across edge platforms.
  - **`v1.5.0 LTS` (Production Tier Graduation)**:
    Upon satisfying the 6 Golden Release Verification Gates and crash-test oracles, the Native Pure-Go engine graduates to a full first-class production engine alongside WiredTiger, establishing a complete multi-tier storage portfolio.
- **SemVer Versioning Rationale (`v1.5.0` vs. `v2.0.0`)**:
  - Expanding storage engine choices while preserving 100% binary wire protocol (PGWire v3, TDS 7.x/8.0), SQL procedural syntax, and client driver compatibility introduces **zero breaking changes** to client applications.
  - Adhering to Semantic Versioning (SemVer), this milestone is designated as **`v1.5.0 LTS`**—a milestone symbolizing architectural maturity and multi-engine flexibility without imposing enterprise upgrade friction.

### 5.2. Scale-Up Complex Query Engine & Mini TPC-H Suite (`v1.1.0`)
- **Objective**: Implement open-addressing integer flat hash join and direct slot projection to target sub-50ms execution on complex analytical queries ([`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)).
- **Deliverables**: TPC-H Q1, Q3, Q4, Q6, Q9 suite and automated CBO subquery decorrelation (`EXISTS`/`IN` -> Semi-Join).

### 5.3. High-Density In-Memory Engine: `OCC-Dense` Default Graduation (`v1.1.0`)
- **Objective**: Following the Phase 7 A/B implementation of pointer-free flat records (`OCC-Dense`, 5.95x row compression, 67.5% heap reduction, Zero-GC pointers), graduate `OCC-Dense` to the default storage engine mode upon completion of the 4 mandatory reliability gates (crash recovery, secondary indexes/DDL, full SLT/T-SQL suites, and 24h soak) ([`notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md) & [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)).

### 5.4. Enterprise Cryptographic Immutable Audit Log (`pkg/audit`) (`v1.1.0`)
- **Objective**: Implement SHA-256 hash-chained tamper-evident audit logging ($H_n = \text{SHA256}(H_{n-1} \parallel R_n)$) with lock-free MPSC ring buffers and offline verification CLI (`lionsql audit verify`).
- **Compliance Scope**: PCI-DSS v4.0 Req 10, SOC 2 Type II, HIPAA §164.312(b) ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md)).

### 5.5. Multi-Core Parallel Analytical Query Execution (`v1.2.0`)
- **Objective**: Multi-threaded Volcano execution (`ParallelSeqScan`) partitioning large table scans across worker pools.
- **Benefit**: Accelerated analytical query execution on multi-core servers without impacting concurrent OLTP transactions.

### 5.6. WebAssembly Runtime Sandbox UDF (`wazero`) (`v1.2.0`)
- **Objective**: Embed the pure-Go `wazero` Wasm runtime to execute dynamically loaded WebAssembly scalar functions (`CREATE FUNCTION ... AS WASM`) in isolated memory sandboxes.

### 5.7. Distributed Scale-Out & Next-Gen Cloud Architecture (`v2.0.0+`)
- **Objective**: Implement multi-node Raft consensus replication and distributed horizontal sharding for large-scale enterprise deployments.
- **Scope & Breaking Boundary**:
  - Cluster membership management, distributed 2PC/Saga coordination for cross-partition transactions, dynamic range rebalancing, and multi-datacenter active-active quorum.
  - Represents LionSQL's formal **`v2.0.0` Major Milestone**, introducing distributed cluster topology and multi-node operational semantics.

---

## 6. Master Strategic Deliverable Matrix

| Milestone | Deliverable / Task | Domain | Target Release | Status | Technical Documentation |
| :---: | :--- | :---: | :---: | :---: | :--- |
| **Phase 1** | Dual Wire Listeners (PGWire :5433, TDS :1433) | Wire Protocols | `v0.1.0` | **Completed** | [`doc/internal/WIRE_PROTOCOLS.md`](doc/internal/WIRE_PROTOCOLS.md) |
| **Phase 1** | Dual Storage Engines (WiredTiger + In-Memory OCC) | Storage Engine | `v0.2.0` | **Completed** | [`doc/internal/STORAGE.md`](doc/internal/STORAGE.md) |
| **Phase 1** | Register-Based VDBE Virtual Machine | Execution & VM | `v0.4.0` | **Completed** | [`doc/internal/VDBE.md`](doc/internal/VDBE.md) |
| **Phase 1** | Cost-Based Optimizer (CBO) & Histograms | Query Planner | `v0.6.0` | **Completed** | [`doc/internal/CBO_VOLCANO.md`](doc/internal/CBO_VOLCANO.md) |
| **Phase 2** | Window Functions (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`) | SQL Analytics | `v0.7.0` | **Completed** | [`doc/EXTENSIONS.md`](doc/EXTENSIONS.md) |
| **Phase 2** | Streaming Tail Quantiles (`PERCENTILE_CONT`, T-Digest) | SQL Analytics | `v0.8.0` | **Completed** | [`doc/PERCENTILE_ANALYTICS.md`](doc/PERCENTILE_ANALYTICS.md) |
| **Phase 3** | 1-Writer Multi-Reader WAL Replication & Promotion | High Availability | `v0.11.0` | **Completed** | [`doc/HIGH_AVAILABILITY.md`](doc/HIGH_AVAILABILITY.md) |
| **Phase 3** | Official Drivers Compatibility (`go-mssqldb`, `lib/pq`) | Client Ecosystem | `v0.12.0` | **Completed** | [`doc/CLIENT_TOOL_INTEGRATION.md`](doc/CLIENT_TOOL_INTEGRATION.md) |
| **Phase 4** | PostgreSQL `pgx/v5` & Extended Batch Pipelining | Wire Protocols | `v0.13.0` | **Completed** | [`doc/internal/WIRE_PROTOCOLS.md`](doc/internal/WIRE_PROTOCOLS.md) |
| **Phase 4** | IETF RFC 9562 Monotonic UUIDv7 (`NEWID7()`) | Data Types | `v0.14.0` | **Completed** | [`doc/REFERENCE_MANUAL.ko.md`](doc/REFERENCE_MANUAL.ko.md) |
| **Phase 4** | VDBE Instruction Optimization (21k TPS Peak) | Execution & VM | `v0.15.0` | **Completed** | [`doc/BENCHMARK.md`](doc/BENCHMARK.md) |
| **Phase 5** | Core Architecture Decoupling (`pkg/engine`) | Architecture | `v0.16.0` | **Completed** | [`doc/ARCHITECTURE.md`](doc/ARCHITECTURE.md) |
| **Phase 5** | Storage WAL Group Commit ($100\times$ Fsync Speedup) | Storage Engine | `v0.17.0` | **Completed** | [`doc/internal/STORAGE.md`](doc/internal/STORAGE.md) |
| **Phase 5** | Granular SQL Object-Level RBAC (`GRANT/REVOKE`) | Security & Auth | `v0.18.0` | **Completed** | [`doc/SECURITY.md`](doc/SECURITY.md) |
| **Phase 5** | Declarative Schema Migration CLI (`lionsql migrate`) | BaaS & Tooling | `v0.19.0` | **Completed** | [`doc/SCHEMA_MIGRATION_GUIDE.md`](doc/SCHEMA_MIGRATION_GUIDE.md) |
| **Phase 6** | Always On Style Automatic Failover & Leader Election | High Availability | `v0.90.0` | **Completed** | [`doc/HIGH_AVAILABILITY.md`](doc/HIGH_AVAILABILITY.md) |
| **Phase 6** | Decoupled Real-Time CDC & Transactional Outbox | Events & HTAP | `v0.90.0` | **Completed** | [`doc/CDC_DUCKDB_INTEGRATION.md`](doc/CDC_DUCKDB_INTEGRATION.md) |
| **Phase 6** | Selinger DP Large Join Reordering ($N > 4$) | Query Planner | `v0.90.0` | **Completed** | [`doc/internal/CBO_VOLCANO.md`](doc/internal/CBO_VOLCANO.md) |
| **Phase 6** | Zero-Allocation Memory & Buffer Pooling (P-1 ~ P-14) | Performance | `v0.90.0` | **Completed** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **Phase 6** | Enterprise Driver Certification (JDBC, ODBC, Python) | Client Ecosystem | `v0.90.0` | **Completed** | [`doc/CLIENT_TOOL_INTEGRATION.md`](doc/CLIENT_TOOL_INTEGRATION.md) |
| **Phase 6** | Enterprise Security & Object Governance (TDE, RBAC) | Security & Auth | `v0.90.0` | **Completed** | [`doc/SECURITY.md`](doc/SECURITY.md) |
| **Phase 6** | Single Standalone Binary CLI & Web Studio Suite | Tooling & UI | `v0.90.0` | **Completed** | [`notes/UNIFIED_CLI_PLAN.md`](notes/UNIFIED_CLI_PLAN.md) |
| **Phase 6** | Global Memory Governor & Circuit Breakers | Resource Mgmt | `v0.90.0` | **Completed** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **Phase 6** | TDS RPC Positional Mapping & Tool Audit (`TOOL-AUDIT-1`) | Wire Protocols | `v0.90.0` | **Completed** | [`notes/TOOL_AUDIT_PLAN.ko.md`](notes/TOOL_AUDIT_PLAN.ko.md) |
| **Phase 6** | 2-Tier Query Execution Architecture (`KISS-5`, `ARCH-2TIER-1`) | Query Engine | `v0.90.0` | **Completed** | [`notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md) |
| **Phase 6** | In-Memory OCC 5-Point Tuning (`VPERF-15` ~ `VPERF-18`) | Storage Engine | `v0.90.0` | **Completed** | [`doc/BENCHMARK.md`](doc/BENCHMARK.md) |
| **Phase 6** | 13-Domain Extended Integration Smoke Suite (`SMOKE-EXT-1`) | Quality & Testing | `v0.90.0` | **Completed** | [`scripts/README.md`](scripts/README.md) |
| **Phase 7** | 10대 실전 프로덕션 앱 생태계 연동 (Gitea ~ Vikunja) | Ecosystem | `v0.95.0` | **Completed** | [`notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md`](notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md) |
| **Phase 7** | `kill -9` 크래시 복구 내구성 오라클 하네스 (`DURA-KILL-1`) | Storage & WAL | `v0.95.0` | **Completed** | [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](notes/DURABILITY_VERIFICATION_PLAN.ko.md) |
| **Phase 7** | 24h 장기 소크 부하 안정성 및 자원 유계 검증 (`DURA-SOAK-1`) | Quality & Soak | `v0.95.0` | **Completed** | [`notes/SOAK_TEST_1H_VERIFICATION_REPORT.ko.md`](SOAK_TEST_1H_VERIFICATION_REPORT.ko.md) |
| **Phase 7** | AST 심볼 테이블 기반 쿼리 라우팅 리팩토링 (`ROUTING-AST-CATALOG-1`) | Query Routing | `v0.95.0` | **Completed** | [`notes/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md) |
| **Phase 7** | 호환성 규칙 Table-Driven 정식화 (`COMPAT-TABLE-DRIVEN-1`) | Compatibility | `v0.95.0` | **In Progress** | [`notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`](notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md) |
| **Phase 7** | In-Memory OCC 104만 tpmC 초격차 성능 달성 | Storage Engine | `v0.95.0` | **Completed** | [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md) |
| **Phase 7** | 고집적 포인터리스 인메모리 엔진 `OCC-Dense` A/B 구현 및 4대 신뢰성 게이트 검증 | Storage Engine | `v0.95.0` | **In Progress** | [`notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md) |
| **Phase 7** | `v0.91.0` 공식 릴리스 및 3대 플랫폼 정적 바이너리 배포 | Release | `v0.91.0` | **Completed** | [`CHANGELOG.md`](CHANGELOG.md) |
| **Phase 7** | 6대 골든 릴리즈 검증 게이트 자동화 (`scripts/release.sh`) | Quality & CI | `v0.91.0` | **Completed** | [`notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md`](notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md) |
| **Phase 7** | WiredTiger `SIGKILL` 크래시 내구성 핫픽스 (`wt_engine.go`) | Storage & WAL | `v0.91.0` | **Completed** | [`notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md`](notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md) |
| **Phase 7** | 단일 바이너리 내장 SPA 웹 서버 호스팅 (`WEB-SPA-1`) | Tooling & UI | `v0.91.0` | **Completed** | [`notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md) |
| **Phase 7A** | 100단계 듀얼 AI 전수 감사 P0(31선) 완치 및 신뢰성 베이스라인 태깅 | Quality & Audit | `v0.92.0` | **In Progress** | [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md) |
| **Phase 7B** | P1(53선) 엔터프라이즈 완결성 및 6대 게이트 최종 검증 (`v1.0.0-rc1`) | Quality & Hardening | `v0.95.0` | **Scheduled** | [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md) |
| **Phase 8** | 공식 LionSQL v1.0.0 LTS 출시 및 6대 게이트 최종 사인오프 | Release | `v1.0.0 LTS` | **Target Q4 2026** | [`CHANGELOG.md`](CHANGELOG.md) |
| **Phase 9** | Mini TPC-H 벤치마크 및 CBO 서브쿼리 Decorrelation | Query Planner | `v1.1.0` | **Planned** | [`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md) |
| **Phase 9** | `OCC-Dense` 디폴트 승격 및 4대 신뢰성 게이트 졸업 (`OCC-HYPER-2`) | Storage Engine | `v1.1.0` | **Planned** | [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md) |
| **Phase 9** | Enterprise Cryptographic Immutable Audit Log (`pkg/audit`) | Security & Audit | `v1.1.0` | **Planned** | [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md) |
| **Phase 9** | Native Pure-Go B+Tree 엔진 실험적 프리뷰 (`--storage=purego`, `CGO_ENABLED=0`) | Storage Engine | `v1.2.0` | **Planned** | [`ROADMAP.md`](ROADMAP.md#51-native-pure-go-btree-storage-engine-experimental-v120---production-lts-v150) |
| **Phase 9** | Multi-Core Parallel Analytical Query Execution (`ParallelSeqScan`) | Query Execution | `v1.2.0` | **Planned** | [`ROADMAP.md`](ROADMAP.md#55-multi-core-parallel-analytical-query-execution-v120) |
| **Phase 9** | WebAssembly Runtime Sandbox UDF (`wazero`) | Extensibility | `v1.2.0` | **Planned** | [`ROADMAP.md`](ROADMAP.md#56-webassembly-runtime-sandbox-udf-wazero-v120) |
| **Phase 10** | **Native Pure-Go 엔진 정식 프로덕션 승격 (WiredTiger 엔터프라이즈 티어와 영구 공존)** | Storage Engine | `v1.5.0 LTS` | **Target Q3-Q4 2027** | [`ROADMAP.md`](ROADMAP.md#51-native-pure-go-btree-storage-engine-experimental-v120---production-lts-v150) |
| **Post-1.0** | [초안/연구] In-Process Reboot 기반 물리 서브폴더 USE DB 전환 (`RESEARCH-INPROCESS-REBOOT-USE`) | Research & Engine | `Post-1.0` | **Draft Plan** | [`notes/IN_PROCESS_REBOOT_MULTI_DATABASE_PLAN.ko.md`](notes/IN_PROCESS_REBOOT_MULTI_DATABASE_PLAN.ko.md) |
| **Phase 11** | Raft Consensus 기반 멀티 노드 복제 클러스터링 및 분산 수평 샤딩 | Cloud & Scale-Out | `v2.0.0+` | **Target 2028+** | [`ROADMAP.md`](ROADMAP.md#57-distributed-scale-out--next-gen-cloud-architecture-v200) |
| **Spec-Out** | In-Kernel OAuth2 Social Web Login | Auth & BaaS | - | **Spec-Out** | [`doc/ENTERPRISE_AUTHENTICATION_SPI.md`](doc/ENTERPRISE_AUTHENTICATION_SPI.md) |
| **Spec-Out** | In-Kernel S3 Blob File Storage & Image Resizing | BaaS Storage | - | **Spec-Out** | [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md) |
| **Spec-Out** | MySQL-Specific DML & 8-Column Tabular EXPLAIN | Query Engine | - | **Spec-Out** | [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md) |
