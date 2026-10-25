# LionSQL Engineering Dashboard & Backlog (`TODO.md`)

This dashboard tracks active engineering sprint tasks, domain backlog matrices, and milestone completion histories for LionSQL.

> [!NOTE]
> - Strategic architecture roadmap: [ROADMAP.md](ROADMAP.md)
> - Defect resolution archive (107 cases): [BUG_ARCHIVE.ko.md](BUG_ARCHIVE.ko.md)
> - Historical P1 specifications archive: [notes/TODO_SPEC_ARCHIVE.ko.md](notes/TODO_SPEC_ARCHIVE.ko.md)
> - Taxonomy & task identifier definitions: [notes/TAXONOMY.ko.md](notes/TAXONOMY.ko.md)

---

## 1. Taxonomy Legend

| Prefix / Code | Domain | Range | Description |
| :---: | :--- | :---: | :--- |
| **`P1` ~ `P3`** | **Priority** | `P1` ~ `P3` | Urgency (P1: Active Sprint, P2: Next Sprint, P3: Ecosystem Verification) |
| **`P-1` ~ `P-17`**| **Performance** | `P-1` ~ `P-17` | Storage, decoding, wire, caching, regex, JSON, and memory pooling tracks |
| **`R-1` ~ `R-5`** | **Refactoring** | `R-1` ~ `R-5` | Monolith decomposition, SRP, DRY deduplication, virtual view registry |
| **`A` ~ `D`**     | **CBO Optimizer**| `A-1` ~ `D-9` | Statistics collection (A), Joins (B), Selectivity (C), Runtime controls (D) |
| **`S-1` ~ `S-7`** | **SQL Syntax** | `S-1` ~ `S-7` | T-SQL and ANSI SQL syntax parity (All 7 completed) |
| **`S3-1` ~ `S3-4`**| **Object Storage**| `S3-1` ~ `S3-4`| S3 pre-signed URL direct upload and SigV4 signer (Post-1.0) |
| **`OBS-1` ~ `OBS-3`**| **Observability**| `OBS-1` ~ `OBS-3`| Prometheus metrics, EXPLAIN ANALYZE, query profiler |
| **`SEC-1` ~ `SEC-2`**| **Security** | `SEC-1` ~ `SEC-2`| TDS Pre-Login TLS negotiation, dynamic certificate engine |
| **`LOC-1` ~ `LOC-4`**| **Collation** | `LOC-1` ~ `LOC-4`| MSSQL Collation, locale support, COLLATE parsing (Post-1.0) |
| **`PREP-1`**     | **Prepared Cache**| `PREP-1` | Bounded session LRU cache for prepared statements |
| **`AUTH-1`**     | **Authentication**| `AUTH-1` | Client-Side OIDC Token Exchange endpoint (Post-1.0) |
| **`HTTP-1` ~ `HTTP-4`**| **HTTP API** | `HTTP-1` ~ `HTTP-4`| Global CORS, Gzip compression, payload limit (32MB), security headers |
| **`UI-1` ~ `UI-16`** | **Web Studio** | `UI-1` ~ `UI-16`| Realtime stream, Overview dashboard, dark/light theme, query studio |
| **`SHUTDOWN-1`** | **Lifecycle** | `SHUTDOWN-1` | Graceful shutdown without WiredTiger CGo SIGSEGV, connection draining |
| **`VPERF-1` ~ `VPERF-18`**| **Vertical Perf**| `VPERF-1` ~ `VPERF-18`| End-to-end hot-path optimizations (TDS ➔ Coordinator ➔ VDBE ➔ Storage) |

---

## 2. v0.90.0 Milestone Completion Summary (100% Completed)

The `v0.90.0` Feature Freeze & Production Hardening sprint (`v0.90.0-beta0` through `v0.90.0-beta3`) is **100% completed (`[x]`)**. All five production exit criteria gates are satisfied:

| Quality Gate | Description | Verified Status |
| :--- | :--- | :---: |
| **Gate 1: Multi-Wire Compatibility** | TDS (SSMS, DBeaver, Beekeeper, sqlcmd) & PGWire v3 (psql) live query parity | **100% PASS (`[x]`)** |
| **Gate 2: ACID & Zero-Race Concurrency** | In-Memory OCC 1,046k tpmC / WiredTiger 833k tpmC, 0 data races under `-race` | **100% PASS (`[x]`)** |
| **Gate 3: High Availability & CDC** | 1-Writer Multi-Reader WAL replication streaming, real-time CDC, live failover | **100% PASS (`[x]`)** |
| **Gate 4: Enterprise Security & Governance** | SQL RBAC Default Deny, TLS dynamic certificates, heap memory governor circuit breaker | **100% PASS (`[x]`)** |
| **Gate 5: Unified Binary & Operational Tools**| Single executable (`lionsql`), Web Studio suite, Windows standalone (`lionsql.exe`, 1ms timer) | **100% PASS (`[x]`)** |

### Core Tracks Delivered in v0.90.0:
1. **Wire Protocols & Tool Interoperability**: TDS RPC 100% ordinal positional parameter mapping (`TDS-RPC-AUDIT-1`), Unicode N-prefix UTF-16LE fix, anonymous VDBE procedural block execution (`IF/WHILE`), legacy metadata procedures (`sp_tables`, `sp_columns`), and session bounded prepared statement LRU cache (`PREP-1`).
2. **Storage Engines & OCC Vertical Performance**: Hyper-scale 256-shard partitioning, 64B cacheline padding, 256-bit lock ordering, small int string interning, zero-alloc `InsertRow`/`UpdateRow` fast-paths (`VPERF-19`), selective column tuple extraction (`VPERF-21`), 256-shard O(1) hash map restoration (`VPERF-22`), delivering **1,046,121 tpmC (39,020 TPS)** in storage kernel and **603,241 tpmC (22,294 TPS)** in VDBE runtime.
3. **Query Engine & 2-Tier CBO**: 2-Tier query engine consolidation (Tier 1 VDBE with direct row updates and point-lookup shortcut, Tier 2 Volcano CBO), Sort-Merge Join iterator (`MergeJoinIterator`), dynamic programming join enumeration, covering index-only scans, and expression functional indexes.
4. **Reliability, Security & Lifecycle**: Windows cross-platform daemon decoupling (`daemon_windows.go`, `daemon_unix.go`), Windows 1ms high-resolution timer (`timeBeginPeriod(1)` via `winmm.dll`), graceful shutdown with 1.5s bounded drain deadline, and PBKDF2/SHA-256 authentication.
5. **Benchmarks & Integration Suites**: Network TPC-C benchmark client (`cmd/tpcc_client`), 3-tier VDBE TPC-C progression (603k tpmC), and 13-domain extended smoke test suite (`scripts/verify_report.sh`).
6. **Web Studio & Observability**: Embedded Web Studio dashboard (`/_/`), 6-metric Overview panel with instant delta, real-time SSE stream, Prometheus text `/metrics` and OTLP `/v1/metrics`, and `EXPLAIN ANALYZE` execution profiler.

---

## 3. Active Release Queue & Post-1.0 Roadmap

### 3.1. v1.0.0 LTS Production Release Queue (Active)

| Task Code | Domain | Core Milestone Deliverable | Target Output | Status |
| :---: | :---: | :--- | :--- | :---: |
| **`REL-1.0-TAG`** | Release/Tag | **Official `v1.0.0` Production Tag & GitHub Release** | Git Tag `v1.0.0`, GitHub Release | **Ready** |
| **`REL-1.0-BIN`** | Build/Pkg | **Multi-Platform Production Binaries (macOS, Linux, Windows)** | `bin/lionsql*`, `Dockerfile` | **Ready** |
| **`REL-1.0-DOC`** | Docs/Release| **Official 1.0 Release Notes & User Migration Guide** | `CHANGELOG.md`, `README.md` | **Ready** |

### 3.2. Post-1.0 (`v1.1.0+`) Roadmap Queue

| Task Code | Domain | Key Architecture Deliverable | Target Packages | Target Release | Reference Document |
| :---: | :---: | :--- | :--- | :---: | :--- |
| **`OCC-HYPER-2`**| Storage/OCC | **In-Memory OCC Compact Arena Memory Compaction (5x Higher Density)** | `pkg/storage/memengine`, `pkg/storage` | `v1.1.0` | [`notes/IN_MEMORY_OCC_BEAT_WIREDTIGER_PLAN.ko.md`](notes/IN_MEMORY_OCC_BEAT_WIREDTIGER_PLAN.ko.md) |
| **`R-6`** | Architecture/API | **Module Interface Abstraction & Protocol Boundary Decoupling** | `pkg/engine`, `pkg/wire`, `pkg/api` | `v1.1.0` | [`notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md`](notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md) |
| **`AUDIT-LOG-1`** | Security/Audit | **Cryptographic SHA-256 Immutable Audit Logging Subsystem** | `pkg/audit`, `cmd/lionsql` | `v1.1.0` | [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md) |
| **`TDS-MOD-1`** | Wire/Refactor | **TDS Monolith (`server.go` 2.6k lines) Decomposition** | `pkg/wire/tds` | `v1.1.0` | Post-1.0 Refactoring |
| **`TDS-COMPAT-6`**| Wire/ORM | **Multiple Result Sets (MARS) Streaming (Dapper `QueryMultiple`)** | `pkg/engine`, `pkg/wire/tds` | `v1.1.0` | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`S3-1` ~ `S3-4`** | Storage/Cloud | **Client-Side S3 Pre-signed URL Direct Upload & SigV4 Signer** | `pkg/storage/s3sign`, `pkg/api` | `v1.1.0` | [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md) |
| **`LOC-1` ~ `LOC-4`** | Collation | **MSSQL Collation, Locale Support & `::fn_helpcollations()`** | `pkg/proc`, `pkg/engine` | `v1.1.0` | [`notes/COLLATION_AND_LOCALE_PLAN.md`](notes/COLLATION_AND_LOCALE_PLAN.md) |
| **`SEC-1`** | Wire/Security | **TDS 8.0 / Pre-Login SSL/TLS Encryption Handshake Hardening** | `pkg/wire/tds` | `v1.1.0` | MS-TDS 2.2.6.4 Specification |
| **`AUTH-1`** | Auth/Tokens | **Client-Side OIDC Token Exchange (`POST /api/auth/token-exchange`)** | `pkg/auth`, `pkg/api` | `v1.1.0` | GEMINI.md Section 3.6 |

---

## 4. Architectural Invariants & Spec-Out Invariants
For architectural simplicity and sub-microsecond latency, the following features are permanently **Spec-Out** (detailed rationales in [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md) and [`GEMINI.md`](GEMINI.md)):
1. **Duplicate PL/pgSQL Interpreter**: Stored procedures unify on native T-SQL VDBE VM (`pkg/proc`).
2. **Legacy XML DML / XQuery**: Standardized on modern JSON functions (`ISJSON`, `JSON_VALUE`) and native Go parsers.
3. **In-Memory OCC Background Vacuum**: Workspace atomic swap/discard leaves zero dead tuples; vacuum workers rejected.
4. **POSIX Shared Memory (Arrow IPC) Snapshots**: Standardized on CDC/WAL event streams.
5. **Distributed 2PC Coordinators (XA / MS DTC)**: Standardized on 1-Writer Multi-Reader WAL replication.
6. **In-Kernel OAuth2 Web Flows**: Standardized on Client-Side OIDC Token Exchange.
7. **In-Kernel Blob File Storage**: Standardized on S3 pre-signed direct client uploads.
8. **Immature Pure-Go Storage Engines**: Focused exclusively on WiredTiger MVCC and In-Memory OCC.
9. **In-Kernel Slow-Query Webhooks**: Standardized on Prometheus `/metrics` and OpenTelemetry OTLP.
10. **In-Kernel Wasm Sandbox**: Standardized on zero-allocation Go Native UDF SPI (`RegisterScalarFunction`).
11. **Total Bytecode Translation for Complex Queries**: 2-Tier query engine (Tier 1 VDBE, Tier 2 Volcano CBO) invariant.
