# LionSQL Engineering Roadmap & Strategic Release Horizons

This document defines the overarching architectural trajectory, versioned milestones, release criteria, and strategic horizons of **LionSQL**—a high-concurrency embedded OLTP database engine and multi-protocol stored procedure server.

> [!NOTE]
> For active daily engineering tasks, prioritized sprint backlogs, and detailed implementation histories, refer to the operational backlog in [TODO.md](TODO.md).

---

## 1. Release Evolution & Milestone Progression

LionSQL follows an evolutionary roadmap designed to scale single-node hardware to its physical limits while maintaining zero-dependency single-binary distribution:

```
[ 과거: 엔진 기능 완성기 ]
  Phase 1~5 : Core Engine & Governance (v0.1.0 ~ v0.24.0)   [COMPLETED]
    Dual Wire (PG/TDS) + Dual Storage (WiredTiger/OCC) + VDBE VM + Replication + RBAC
  Phase 6   : Production Baseline & Freeze (v0.90.0)         [COMPLETED]
    Unified CLI (lionsql) + Web Studio + Memory Governor + 2-Tier CBO/VDBE Engine
                   │
                   ▼
[ 현재: 실전 신뢰성 입증기 (Active Sprint) ]
  Phase 7   : Ecosystem & Durability Hardening (v0.95.0)     [IN PROGRESS / 90% DONE]
    10 Production Apps OK + kill -9 Oracle (D1~D4) + 24h Soak Zero-Leak + Table-Driven Rules + 1.04M tpmC
                   │
                   ▼
[ 목전: 공식 1.0 릴리즈 ]
  Phase 8   : Official 1.0 LTS Production Release (v1.0.0)   [READY FOR SIGN-OFF]
    6 Quality Gates Passed + Always On Failover + Lossless CDC + LTS Packaging
                   │
                   ▼
[ 미래: 초격차 확장기 (Post-1.0) ]
  Phase 9   : Scale-Up Query & Compact Storage (v1.1.0)      [PLANNED]
    Flat Hash Join & Mini TPC-H + OCC Compact Arena (5x) + Pure-Go Engine + Immutable Audit Log
  Phase 10  : Parallel OLAP & Extensibility (v1.2.0+)        [PLANNED]
    Multi-Core ParallelSeqScan + Wasm UDF Sandbox + Friendly SQL + Embedded SPA Hosting
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
| **Phase 7** | `v0.95.0`<br>(Beta 4+) | **Real-World Ecosystem & Autonomous Durability Hardening**<br>10 production apps verified (Gitea ~ Vikunja), `crashtest` (`kill -9` D1~D4), `soaktest` (leak-free), AST routing, Table-Driven rules, 1.04M tpmC. | **In Progress (90%)** |
| **Phase 8** | `v1.0.0 LTS` | **Official 1.0 Production Release & Long-Term Support**<br>6 Quality Gates verified, Always On HA, lossless CDC, certified drivers, release sign-off. | **Ready for Sign-off** |
| **Phase 9** | `v1.1.0` | **Scale-Up Complex Query & Compact Storage**<br>Flat hash join, Mini TPC-H suite, OCC compact arena (5x density), Pure-Go storage engine (`CGO_ENABLED=0`), immutable audit log. | **Planned** |
| **Phase 10** | `v1.2.0+` | **Parallel Analytical Execution & Extensibility**<br>Multi-core `ParallelSeqScan` workers, Wasm UDF sandbox (`wazero`), DuckDB-style friendly SQL, embedded SPA web server. | **Planned** |

---

## 2. LionSQL 1.0 / v1.0.0 LTS Production Readiness & Exit Criteria

To tag the official **LionSQL 1.0 / v1.0.0 LTS** release, the database engine must satisfy six non-negotiable quality gates:

```
                      [ LionSQL v1.0.0 LTS 6 Quality Gates ]
                                         │
    ┌─────────────┬─────────────┬────────┴────────┬─────────────┬─────────────┐
    ▼             ▼             ▼                 ▼             ▼             ▼
[ Gate 1 ]    [ Gate 2 ]    [ Gate 3 ]        [ Gate 4 ]    [ Gate 5 ]    [ Gate 6 ]
Multi-Wire    ACID Concur   Durability &      Ecosystem     Enterprise    Autonomous
Driver Cert   & Zero-Race   Crash Recovery    Conformance   Security      Ops & Soak
(100% PASS)   (20k+ TPS)    (kill -9 D1~D4)   (10 Apps OK)  (TDE & RBAC)  (24h Soak OK)
```

1. **Gate 1: Multi-Protocol & Driver Conformance (100% PASS)**:
   - Certified zero-regression compatibility across Go (`pgx/v5`, `lib/pq`, `go-mssqldb`), Python (`psycopg3`, `pymssql`), Java JDBC (`pgjdbc`, `mssql-jdbc`), C ODBC (`psqlODBC`), and enterprise GUIs (DBeaver, SSMS, Azure Data Studio, Beekeeper Studio).
2. **Gate 2: ACID Concurrency & Zero Data Race**:
   - Zero data races across all 25 packages (`go test -tags wiredtiger -count=1 -race ./...` 100% PASS).
   - Sustained TPC-C throughput: $\ge 20,000\text{ TPS}$ (In-Memory OCC) and $\ge 16,800\text{ TPS}$ (WiredTiger MVCC) under full write durability (`FsyncAlways`).
3. **Gate 3: Durability & Crash Recovery Oracle (D1~D4 100% PASS)**:
   - Automated crash verification via `cmd/crashtest`: 100% data integrity under non-graceful termination (`kill -9`), double-kill idempotency, torn WAL log-tail recovery, and DDL transaction interleaving.
   - Zero uncommitted leaks, zero committed row loss, and consistent checksum verification across both storage engines.
4. **Gate 4: Production Ecosystem Conformance (10 Real-World Apps Zero Code Change)**:
   - 10 stock, unmodified open-source applications running flawlessly against LionSQL:
     - Gitea (Git & Code Hosting), Woodpecker CI (CI/CD Pipelines)
     - Vaultwarden (Secret & Password Management), Casdoor (SSO & IAM)
     - Apache Answer (Q&A Knowledge Base), AFFiNE (Notion/Doc Workspace), Vikunja (Kanban & Task Management)
     - Ghost (Headless CMS & Publishing), Metabase (BI & Analytics, Dual-Protocol), Umami (Web Analytics)
   - Covering XORM, Diesel ORM, Prisma, Knex, and official JDBC drivers.
5. **Gate 5: Enterprise Security & Object Governance (100% PASS)**:
   - TDE Transparent Data Encryption, SQL object-level RBAC (`GRANT`/`REVOKE`/`DENY`), HTTP JWT/API-Key RBAC, and general/slow query logging.
6. **Gate 6: Autonomous Operational Tooling & Long-Term Soak Stability**:
   - In-tree declarative schema migration CLI (`lionsql migrate`) and online non-blocking snapshot hot backup/restore (`POST /api/backup`).
   - Sustained multi-client soak test verification via `cmd/soaktest`: zero memory leaks, bounded file descriptors (`open_fds` invariant), zero disk bloat, and stable p99 latency under 24h+ continuous workloads.

---

## 3. Milestone Execution Status

### 3.1. Phase 6: v0.90.0 Feature Freeze & Hardening Suite (Completed)
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

### 3.2. Phase 7: Real-World Ecosystem & Autonomous Durability Hardening (Active Milestone)
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

### 3.3. Phase 8: Official 1.0 LTS Production Release Readiness
With all Phase 6 deliverables, Phase 7 ecosystem integrations, and durability harnesses verified, all 6 Production Readiness Quality Gates are fully validated. LionSQL is staged for official `v1.0.0 LTS` tagging and long-term production distribution.

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

## 5. Future Horizons: Post-1.0 Roadmap (`v1.1.0+` - `v1.2.0+`)

The following capabilities represent strategic architectural expansions planned for post-1.0 releases:

```
                  [ Post-1.0 Architectural Evolution ]
                                   │
         ┌─────────────────────────┼─────────────────────────┬─────────────────────────┐
         ▼                         ▼                         ▼                         ▼
[ v1.1.0: Pure-Go Disk ]   [ v1.1.0: Complex Query ] [ v1.1.0: Audit Log ]     [ v1.2.0: Parallel OLAP ]
Native Pure-Go storage     Flat hash join & TPC-H    SHA-256 hash-chained log  ParallelSeqScan workers
CGO_ENABLED=0 distribution OCC compact arena (5x)    PCI-DSS v4.0 / SOC2       Multi-core analytical scans
```

### 5.1. Pure-Go Persistent Storage Engine Backend (`v1.1.0`)
- **Objective**: Implement a native pure-Go persistent storage backend satisfying the `storage.Engine` interface, enabling compilation with `CGO_ENABLED=0` without external C compiler toolchains.
- **Benefit**: 100% autonomous, zero-dependency static binary distribution across any operating system or lightweight container environment.

### 5.2. Scale-Up Complex Query Engine & Mini TPC-H Suite (`v1.1.0`)
- **Objective**: Implement open-addressing integer flat hash join and direct slot projection to target sub-50ms execution on complex analytical queries ([`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)).
- **Deliverables**: TPC-H Q1, Q3, Q4, Q6, Q9 suite and automated CBO subquery decorrelation (`EXISTS`/`IN` -> Semi-Join).

### 5.3. In-Memory OCC Compact Arena Memory Compaction (`v1.1.0`)
- **Objective**: Compact arena memory compression providing $5\times$ higher density for in-memory transactional workloads ([`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)).

### 5.4. Enterprise Cryptographic Immutable Audit Log (`pkg/audit`) (`v1.1.0`)
- **Objective**: Implement SHA-256 hash-chained tamper-evident audit logging ($H_n = \text{SHA256}(H_{n-1} \parallel R_n)$) with lock-free MPSC ring buffers and offline verification CLI (`lionsql audit verify`).
- **Compliance Scope**: PCI-DSS v4.0 Req 10, SOC 2 Type II, HIPAA §164.312(b) ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md)).

### 5.5. Multi-Core Parallel Analytical Query Execution (`v1.2.0`)
- **Objective**: Multi-threaded Volcano execution (`ParallelSeqScan`) partitioning large table scans across worker pools.
- **Benefit**: Accelerated analytical query execution on multi-core servers without impacting concurrent OLTP transactions.

### 5.6. WebAssembly Runtime Sandbox UDF (`wazero`) (`v1.2.0`)
- **Objective**: Embed the pure-Go `wazero` Wasm runtime to execute dynamically loaded WebAssembly scalar functions (`CREATE FUNCTION ... AS WASM`) in isolated memory sandboxes.

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
| **Phase 7** | AST 심볼 테이블 기반 쿼리 라우팅 리팩토링 (`ROUTING-AST-CATALOG-1`) | Query Routing | `v0.95.0` | **Completed** | [`notes/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](notes/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md) |
| **Phase 7** | 호환성 규칙 Table-Driven 정식화 (`COMPAT-TABLE-DRIVEN-1`) | Compatibility | `v0.95.0` | **In Progress** | [`notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`](notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md) |
| **Phase 7** | In-Memory OCC 104만 tpmC 초격차 성능 달성 | Storage Engine | `v0.95.0` | **Completed** | [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md) |
| **Phase 8** | 공식 LionSQL v1.0.0 LTS 출시 및 6대 게이트 최종 사인오프 | Release | `v1.0.0 LTS` | **Ready for Sign-off** | [`CHANGELOG.md`](CHANGELOG.md) |
| **Phase 9** | Mini TPC-H 벤치마크 및 CBO 서브쿼리 Decorrelation | Query Planner | `v1.1.0` | **Planned** | [`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md) |
| **Phase 9** | In-Memory OCC 컴팩트 아레나 메모리 압축 (`OCC-HYPER-2`) | Storage Engine | `v1.1.0` | **Planned** | [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md) |
| **Phase 9** | Pure-Go 네이티브 영속 스토리지 엔진 백엔드 | Storage Engine | `v1.1.0` | **Planned** | [`ROADMAP.md`](ROADMAP.md#51-pure-go-persistent-storage-engine-backend-v110) |
| **Phase 9** | Enterprise Cryptographic Immutable Audit Log (`pkg/audit`) | Security & Audit | `v1.1.0` | **Planned** | [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md) |
| **Phase 10** | Multi-Core Parallel Analytical Query Execution | Query Execution | `v1.2.0` | **Planned** | [`ROADMAP.md`](ROADMAP.md#55-multi-core-parallel-analytical-query-execution-v120) |
| **Phase 10** | WebAssembly Runtime Sandbox UDF (`wazero`) | Extensibility | `v1.2.0` | **Planned** | [`ROADMAP.md`](ROADMAP.md#56-webassembly-runtime-sandbox-udf-wazero-v120) |
| **Spec-Out** | In-Kernel OAuth2 Social Web Login | Auth & BaaS | - | **Spec-Out** | [`doc/ENTERPRISE_AUTHENTICATION_SPI.md`](doc/ENTERPRISE_AUTHENTICATION_SPI.md) |
| **Spec-Out** | In-Kernel S3 Blob File Storage & Image Resizing | BaaS Storage | - | **Spec-Out** | [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md) |
| **Spec-Out** | MySQL-Specific DML & 8-Column Tabular EXPLAIN | Query Engine | - | **Spec-Out** | [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md) |
