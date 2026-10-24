# LionSQL Engineering Roadmap & Strategic Release Horizons

This document defines the overarching architectural trajectory, versioned milestones, release criteria, and strategic horizons of **LionSQL**—a high-concurrency embedded OLTP database engine and multi-protocol stored procedure server.

> [!NOTE]
> For active daily engineering tasks, prioritized sprint backlogs, and detailed implementation histories, refer to the operational backlog in [TODO.ko.md](TODO.ko.md).

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

[ Phase 6: Production Hardening & Feature Freeze ] ► v0.90.0 [COMPLETED - FEATURE FREEZE]
  Unified CLI (lionsql) + Web Studio Suite + Memory Governor + Protocol Gateways (postgres/master)

[ Phase 7: Official 1.0 Release ] ────────► v1.0.0 LTS [RELEASE CANDIDATE STAGE]
  Always On Failover + Decoupled CDC/DuckDB + Certified Drivers + Final Documentation & Polish

[ Future Horizons ] ─────────────────────► v1.1.0 - v1.2.0+  [PLANNED]
  Pure-Go Pebble/bbolt Storage + Wasm Sandbox UDF + Multi-Core Parallel OLAP
```

### High-Level Milestone Dashboard

| Milestone | Target Version | Focus & Core Deliverables | Completion Status |
| :---: | :---: | :--- | :---: |
| **Phase 1** | `v0.1.0` - `v0.8.0` | **Core Foundation & Virtual Machine Architecture**<br>Dual wire protocols (:5433, :1433), dual storage engines, SQLite-style VDBE VM, CBO histograms. | **100% Completed** |
| **Phase 2** | `v0.9.0` - `v0.10.0` | **SQL & Analytical Depth, Aggregations & Views**<br>Window functions, T-Digest quantiles, in-engine log operators (GREP/AWK), savepoints, views. | **100% Completed** |
| **Phase 3** | `v0.11.0` - `v0.12.0` | **Replication, Official Drivers & Zero-Allocation**<br>1-Writer multi-reader WAL streaming replication, official driver compatibility, buffer pooling. | **100% Completed** |
| **Phase 4** | `v0.13.0` - `v0.15.0` | **Modern Ecosystem, GUI Tool Interop & VDBE Acceleration**<br>pgx/v5 pipelining, DBeaver/SSMS interop, UUIDv7, VDBE opcode specialization (21k TPS peak). | **100% Completed** |
| **Phase 5** | `v0.16.0` - `v0.24.0` | **Enterprise Governance, Security & Engine Decoupling**<br>Layer separation (`pkg/engine`), WAL group commit, RBAC, CBO DP join ordering, migrations CLI, prepared LRU cache. | **100% Completed** |
| **Phase 6** | `v0.90.0` | **Production Hardening & Feature Freeze Milestone**<br>Single-binary CLI (`lionsql`), Web Studio production suite, global memory governor, single-DB catalog standardization. | **100% Completed<br>(Feature Freeze)** |
| **Phase 7** | `v1.0.0 LTS` | **Official 1.0 Production Release & Long-Term Support**<br>Always On HA, decoupled HTAP, driver certification, zero-race verification, release candidate sign-off. | **Release Candidate<br>Preparation** |
| **Horizons** | `v1.1.0+` | **Post-1.0 Architectural Extensions**<br>Pure-Go persistent storage backend, Wasm UDF sandbox (`wazero`), multi-core parallel OLAP scans. | **Planned** |

---

## 2. LionSQL 1.0 / v1.0.0 LTS Production Readiness & Exit Criteria

To tag the official **LionSQL 1.0 / v1.0.0 LTS** release, the database engine must satisfy five non-negotiable exit criteria:

```
               [ LionSQL v1.0.0 LTS Quality Gates ]
                                 │
   ┌──────────────┬──────────────┼──────────────┬──────────────┐
   ▼              ▼              ▼              ▼              ▼
[ Gate 1 ]     [ Gate 2 ]     [ Gate 3 ]     [ Gate 4 ]     [ Gate 5 ]
Multi-Wire     ACID Durability High           Enterprise     Operational
Driver Cert    & Zero-Race    Availability   Security/Auth  Tooling & CLI
(100% PASS)    (20k+ TPS)     (Always On)    (TDE & RBAC)   (Hot Backup)
```

1. **Multi-Protocol & Driver Conformance (100% PASS)**:
   - Certified zero-regression compatibility across Go (`pgx/v5`, `lib/pq`, `go-mssqldb`), Python (`psycopg3`, `pymssql`), Java JDBC (`pgjdbc`), C ODBC (`psqlODBC`), and enterprise GUIs (DBeaver, SSMS, Azure Data Studio, Beekeeper Studio).
2. **ACID Transaction Durability & Concurrency**:
   - Zero data races (`go test -race ./...` 100% PASS across all 25 packages).
   - Sustained TPC-C throughput: $\ge 20,000\text{ TPS}$ (In-Memory OCC) and $\ge 16,800\text{ TPS}$ (WiredTiger MVCC) under full write durability (`FsyncAlways`).
3. **High Availability & Lossless CDC**:
   - Always On automatic failover with highest-LSN promotion and split-brain fencing.
   - Decoupled real-time CDC streaming (`GET /api/cdc/stream`) with lossless transactional outbox forwarding.
4. **Enterprise Security & Object Governance (100% PASS)**:
   - TDE Transparent Data Encryption, SQL object-level RBAC (`GRANT`/`REVOKE`/`DENY`), HTTP JWT/API-Key RBAC, and general/slow query logging.
   - *Note*: Cryptographic SHA-256 hash-chained audit logging (`pkg/audit`) is designated as a Post-1.0 (`v1.1.0`) compliance milestone ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md)).
5. **Autonomous Operational Tooling**:
   - In-tree declarative schema migration CLI (`lionsql migrate new/up/down/status`).
   - Online non-blocking snapshot hot backup/restore (`POST /api/backup`).

---

## 3. Active Milestone: Phase 6 Implementation Status

### 3.1. Completed Deliverables in Phase 6
* **Always On Style Automatic Failover & Leader Election (`pkg/replication`)**:
  Highest-LSN precedence rule, monotonic leader terms, automated replica promotion, and split-brain fencing.
* **Decoupled Real-Time CDC & Transactional Outbox (`pkg/cdc`, `pkg/storage`)**:
  Row-level mutation streaming, bounded ring buffers, and lossless transactional outbox forwarding to Kafka, RabbitMQ, and DuckDB.
* **CBO Advanced Statistics: Selinger DP, Multi-Column (`C-5`) & MCV (`C-7`) (`pkg/planner`)**:
  Sub-millisecond join reordering for $N > 4$ tables, functional dependency modeling ($dep$), and Top-20 MCV skew decoupling.
* **TDS Protocol Modernization (`pkg/wire/tds`)**:
  NBCROW row compression (`0xD2`), PLP chunk streaming for `VARCHAR(MAX)`, TDS 7.3 binary date/time/UUID serialization, and single-frame buffer pooling halving write syscalls.
* **Zero-Allocation Hot-Path Optimizations (P-1 through P-14)**:
  Direct binary bind parameter decoding (P-14), `WT_CURSOR` pooling/reset (P-8), OCC workspace map pooling (P-11), Volcano chunk streaming (P-12), and zero-alloc virtual catalog pre-filtering (`containsFold`).
* **Multi-Language Driver Certification Harness (`pkg/wire`, `pkg/wire/tds`)**:
  Hermetic integration test suites for Python (`psycopg3`, `pymssql`), Go (`pgx`, `mssql`), JDBC, and ODBC.
* **Single Standalone Binary & Unified CLI (`cmd/lionsql`, `UNIFY-CLI`)**:
  Consolidated CLI tools and server daemon under `bin/lionsql` with first-class subcommands (`serve`, `start`, `stop`, `restart`, `status`, `doctor`, `init-config`, `migrate`, `sql`, `backup`, `restore`, `version`).
* **Web Studio Production Feature Suite (`pkg/storage`, `pkg/engine`, `pkg/api`, `pkg/ui`)**:
  Added Realtime Live Stream SSE viewer (`/api/realtime`), Request Activity Logs latency profiler (`/api/logs/requests`), Visual Column Addition DDL modal (`+ Add Column`), multi-row bulk exports (CSV/JSON), and foreign key interactive jump links (`↗`).
* **Global Memory Governor & Query Circuit Breaker (`pkg/config`, `pkg/execution`, `cmd/server`)**:
  Implemented dynamic physical RAM budgeting (`max_memory`, 70% of RAM) and query-level `work_mem: 64MB` circuit breakers in hash joins and external merge sort pipelines to eliminate OS OOM kills.
* **Protocol Catalog Database Standardization (`pkg/catalog/virtual`, `pkg/engine`)**:
  Eliminated legacy database namespaces across virtual catalog and engine to strictly standardize on protocol-native single database namespaces: `postgres` for PGWire and `master` for TDS.
* **Test Suite Boilerplate Diet (`pkg/wire`, `pkg/api`)**:
  Net 291 lines of redundant socket handshake and setup boilerplate eliminated while preserving 100% test coverage.

### 3.2. Active Deliverables (Target: Official 1.0 Release Candidate)
* **Official 1.0 Release Candidate Sign-off & Polish (`pkg/api`, `pkg/engine`, `pkg/wire`)**:
  - **Brand Uniformity**: Complete brand standardization to `LionSQL` across webhook headers (`X-LionSQL-*`), JavaScript SDK (`/sdk/lionsql.js`), and system metadata functions.
  - **Injection & Lifecycle Hardening**: Session variable (`@var`) quote escaping and guaranteed table variable/temporary table cleanup upon session termination.
  - **Production Security Guards**: Prevent daemon startup on public interfaces (`0.0.0.0`) with default credentials.
  - **Zero-Race Verification**: 100% pass across all 25 packages under `go test -race ./...`.

---

## 4. Architectural Invariants & Explicit Spec-Out Decisions

To protect sub-microsecond transaction performance, prevent kernel bloat, and maintain single-binary self-containment, LionSQL explicitly designates the following architectural models as **Spec-Out (Excluded / Non-Goals)**:

| Spec-Out Architecture | Rejection Rationale | Recommended Enterprise Alternative | Reference |
| :--- | :--- | :--- | :--- |
| **In-Kernel PL/pgSQL Interpreter** | Stored procedures are unified under the high-speed VDBE T-SQL virtual machine. Embedding a second procedural interpreter violates DRY and doubles attack surfaces. | Execute procedures via VDBE T-SQL VM (`CREATE PROCEDURE`) | GEMINI §8.1 |
| **In-Kernel OAuth2 Social Login** | Database kernels should never initiate outbound internet HTTP calls (Google/GitHub) or store web secrets. Binary wire protocols (TDS/PGWire) cannot perform browser redirects. | Enterprise perimeter reverse proxies (Keycloak, OAuth2-Proxy, Nginx) | GEMINI §8.6, ADR-021 |
| **In-Kernel S3 Blob & Image Processing** | Multipart binary blob streaming and image resizing cause memory/CPU starvation, destroying OLTP p99 latencies. | External object storage (AWS S3, MinIO) via Pre-signed direct URLs | GEMINI §8.7, `doc/S3_PRESIGNED` |
| **In-Memory Dead-Tuple Vacuum Threads** | The in-memory OCC engine isolates mutations in private workspaces and swaps versions on commit, never creating dead-tuple chains. PostgreSQL-style vacuum workers are YAGNI. | Lean OCC workspace swapping and SkipList index versioning | GEMINI §8.3 |
| **External Distributed Coordinators** | Avoid distributed consensus write penalties (2PC network round-trips) in core OLTP paths. | Single-node hardware saturation + linear WAL replication | GEMINI §5, §6 |

---

## 5. Future Horizons: Post-1.0 Roadmap (`v1.1.0+` - `v1.2.0+`)

The following capabilities represent long-term architectural expansions evaluated for post-1.0 releases:

```
                  [ Post-1.0 Architectural Evolution ]
                                   │
         ┌─────────────────────────┼─────────────────────────┬─────────────────────────┐
         ▼                         ▼                         ▼                         ▼
[ v1.1.0: Pure-Go Disk ]   [ v1.1.0: Wasm UDF ]      [ v1.1.0: Audit Log ]     [ v1.2.0: Parallel OLAP ]
Pebble / bbolt storage     wazero runtime sandbox    SHA-256 hash-chained log  ParallelSeqScan workers
CGO_ENABLED=0 distribution Safe third-party plugins  PCI-DSS v4.0 / SOC2       Multi-core analytical scans
```

### 5.1. Pure-Go Persistent Storage Engine Backend (Pebble / bbolt) (`v1.1.0`)
- **Objective**: Implement a pure-Go persistent storage backend satisfying the `storage.Engine` interface, enabling compilation with `CGO_ENABLED=0` without C compiler dependencies.
- **Benefit**: 100% autonomous static binary distribution across any operating system or lightweight container environment.

### 5.2. WebAssembly Runtime Sandbox UDF (`wazero`) (`v1.1.0`)
- **Objective**: Embed the pure-Go `wazero` Wasm runtime to execute dynamically loaded WebAssembly scalar functions (`CREATE FUNCTION ... AS WASM`) in isolated memory sandboxes.
- **Benefit**: Secure, crash-proof execution of untrusted third-party analytical functions.

### 5.3. Enterprise Cryptographic Immutable Audit Log (`pkg/audit`) (`v1.1.0`)
- **Objective**: Implement SHA-256 hash-chained tamper-evident audit logging ($H_n = \text{SHA256}(H_{n-1} \parallel R_n)$) with lock-free MPSC ring buffers and offline verification CLI (`lionsql audit verify`).
- **Compliance Scope**: PCI-DSS v4.0 Req 10, SOC 2 Type II, HIPAA §164.312(b) ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md)).

### 5.4. Multi-Core Parallel Analytical Query Execution (`v1.2.0`)
- **Objective**: Multi-threaded Volcano execution (`ParallelSeqScan`) partitioning large table scans across worker pools.
- **Benefit**: Accelerated analytical query execution on multi-core servers without impacting concurrent OLTP transactions.

---

## 6. Master Strategic Deliverable Matrix

| Milestone | Deliverable / Task | Domain | Target Release | Status | Technical Documentation |
| :---: | :--- | :---: | :---: | :---: | :--- |
| **Phase 1** | Dual Wire Listeners (PGWire :5433, TDS :1433) | Wire Protocols | `v0.1.0` | **Completed** | [`doc/internal/WIRE_PROTOCOLS.md`](doc/internal/WIRE_PROTOCOLS.md) |
| **Phase 1** | Dual Storage Engines (WiredTiger + In-Memory OCC) | Storage Engine | `v0.2.0` | **Completed** | [`doc/internal/STORAGE.md`](doc/internal/STORAGE.md) |
| **Phase 1** | Register-Based VDBE Virtual Machine | Execution & VM | `v0.4.0` | **Completed** | [`doc/internal/VDBE.md`](doc/internal/VDBE.md) |
| **Phase 1** | Cost-Based Optimizer (CBO) & Histograms | Query Planner | `v0.6.0` | **Completed** | [`doc/internal/CBO_VOLCANO.md`](doc/internal/CBO_VOLCANO.md) |
| **Phase 2** | Window Functions (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`) | SQL Analytics | `v0.9.0` | **Completed** | [`doc/EXTENSIONS.md`](doc/EXTENSIONS.md) |
| **Phase 2** | Streaming Tail Quantiles (`PERCENTILE_CONT`, T-Digest) | SQL Analytics | `v0.10.0` | **Completed** | [`doc/PERCENTILE_AND_QUANTILE_ANALYTICS.md`](doc/PERCENTILE_AND_QUANTILE_ANALYTICS.md) |
| **Phase 3** | 1-Writer Multi-Reader WAL Replication & Promotion | High Availability | `v0.11.0` | **Completed** | [`doc/HIGH_AVAILABILITY.md`](doc/HIGH_AVAILABILITY.md) |
| **Phase 3** | Official Drivers Compatibility (`go-mssqldb`, `lib/pq`) | Client Ecosystem | `v0.12.0` | **Completed** | [`doc/CLIENT_TOOL_INTEGRATION.md`](doc/CLIENT_TOOL_INTEGRATION.md) |
| **Phase 4** | PostgreSQL `pgx/v5` & Extended Batch Pipelining | Wire Protocols | `v0.13.0` | **Completed** | [`doc/internal/WIRE_PROTOCOLS.md`](doc/internal/WIRE_PROTOCOLS.md) |
| **Phase 4** | IETF RFC 9562 Monotonic UUIDv7 (`NEWID7()`) | Data Types | `v0.14.0` | **Completed** | [`doc/REFERENCE_MANUAL.ko.md`](doc/REFERENCE_MANUAL.ko.md) |
| **Phase 4** | VDBE Instruction Optimization (21k TPS Peak) | Execution & VM | `v0.15.0` | **Completed** | [`doc/BENCHMARK.md`](doc/BENCHMARK.md) |
| **Phase 5** | Core Architecture Decoupling (`pkg/engine`) | Architecture | `v0.16.0` | **Completed** | [`doc/ARCHITECTURE.md`](doc/ARCHITECTURE.md) |
| **Phase 5** | Storage WAL Group Commit ($100\times$ Fsync Speedup) | Storage Engine | `v0.17.0` | **Completed** | [`doc/internal/STORAGE.md`](doc/internal/STORAGE.md) |
| **Phase 5** | Granular SQL Object-Level RBAC (`GRANT/REVOKE`) | Security & Auth | `v0.18.0` | **Completed** | [`doc/SECURITY.md`](doc/SECURITY.md) |
| **Phase 5** | Declarative Schema Migration CLI (`lionsql migrate`) | BaaS & Tooling | `v0.19.0` | **Completed** | [`doc/SCHEMA_MIGRATIONS_CLI.md`](doc/SCHEMA_MIGRATIONS_CLI.md) |
| **Phase 6** | Always On Style Automatic Failover & Leader Election | High Availability | `v1.0.0 LTS` | **Completed** | [`doc/HIGH_AVAILABILITY.md`](doc/HIGH_AVAILABILITY.md) |
| **Phase 6** | Decoupled Real-Time CDC & Transactional Outbox | Events & HTAP | `v1.0.0 LTS` | **Completed** | [`doc/CDC_DUCKDB_INTEGRATION.md`](doc/CDC_DUCKDB_INTEGRATION.md) |
| **Phase 6** | Selinger DP Large Join Reordering ($N > 4$) | Query Planner | `v1.0.0 LTS` | **Completed** | [`doc/internal/CBO_VOLCANO.md`](doc/internal/CBO_VOLCANO.md) |
| **Phase 6** | Zero-Allocation Memory & Buffer Pooling (P-1 ~ P-14) | Performance | `v1.0.0 LTS` | **Completed** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **Phase 6** | Enterprise Driver Certification (JDBC, ODBC, Python) | Client Ecosystem | `v1.0.0 LTS` | **Completed** | [`doc/CLIENT_TOOL_INTEGRATION.md`](doc/CLIENT_TOOL_INTEGRATION.md) |
| **Phase 6** | Enterprise Security & Object Governance (TDE, RBAC) | Security & Auth | `v1.0.0 LTS` | **Completed** | [`doc/SECURITY.md`](doc/SECURITY.md) |
| **Post-1.0** | Enterprise Cryptographic Immutable Audit Log | Security & Audit | `v1.1.0` | **Postponed** | [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md) |
| **Post-1.0** | Pure-Go Persistent Storage Engine (Pebble / bbolt) | Storage Engine | `v1.1.0` | **Postponed** | [`doc/ROADMAP.md`](ROADMAP.md#51-pure-go-persistent-storage-engine-backend-pebble--bbolt-v110) |
| **Post-1.0** | WebAssembly Runtime Sandbox UDF (`wazero`) | Extensibility | `v1.1.0` | **Postponed** | [`doc/ROADMAP.md`](ROADMAP.md#52-webassembly-runtime-sandbox-udf-wazero-v110) |
| **Post-1.0** | Multi-Core Parallel Analytical Query Execution | Query Execution | `v1.2.0` | **Postponed** | [`doc/ROADMAP.md`](ROADMAP.md#54-multi-core-parallel-analytical-query-execution-v120) |
| **Spec-Out** | In-Kernel OAuth2 Social Web Login | Auth & BaaS | - | **Spec-Out** | [`doc/ENTERPRISE_AUTHENTICATION_SPI.md`](doc/ENTERPRISE_AUTHENTICATION_SPI.md) |
| **Spec-Out** | In-Kernel S3 Blob File Storage & Image Resizing | BaaS Storage | - | **Spec-Out** | [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md) |
