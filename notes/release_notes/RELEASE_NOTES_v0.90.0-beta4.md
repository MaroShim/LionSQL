# LionSQL v0.90.0-beta4 Release Notes (`notes/RELEASE_NOTES_v0.90.0-beta4.md`)

> - **Release Date**: 2026-10-30
> - **Version**: `v0.90.0-beta4`
> - **Theme**: **Gitea Dual-Protocol Integration, Server Pipeline Scaling to 135k tpmC, Star Schema Benchmark Suite, and Windows/Linux/macOS Cross-Compilation Packaging**

---

## 1. Release Highlights

LionSQL `v0.90.0-beta4` delivers seamless production-grade **dual-protocol (TDS/PGWire) compatibility for Gitea**, scales single-node network performance to **135,000 tpmC / 5,000 TPS** via socket read coalescing, introduces the **Star Schema Benchmark (SSB)** complex query engine suite, and automates standalone static binary distribution across **macOS, Linux, and Windows**.

- **Gitea Dual-Protocol Zero-Code-Change Integration**: Full support for XORM ORM migrations (40+ tables) and DML/DDL over both Microsoft SQL Server TDS (:1433) and PostgreSQL Wire Protocol (:5433).
- **Network Pipeline Scaling to 135k tpmC**: High-efficiency socket read coalescing, 32KB packet buffer pooling (`sync.Pool`), and zero-allocation RPC parameter binding saturating vertical hardware limits.
- **Star Schema Benchmark (SSB) Analytical Engine Suite**: Integrated `cmd/ssb` benchmark tool and zero-allocation tuple AST evaluator optimizing complex multi-table analytical joins.
- **T-SQL Forward-Only Cursor & High-ROI Dialect Built-ins**: Fast forward-only cursors (`PROC-CURSOR-1`) with `@@FETCH_STATUS`, alongside 7 essential cross-DBMS compatibility functions.
- **Triple-Platform Cross-Compilation Pipeline**: Unified release automation (`scripts/release.sh`) producing self-contained static executables for macOS arm64, Linux amd64, and Windows amd64 (`lionsql.exe`).

---

## 2. Detailed Technical Improvements

### 2.1. Gitea Dual-Protocol & ORM Migration Support
- **PostgreSQL Wire DDL/DML Compatibility (`GITEA-PGWIRE-1`)**:
  - Implemented `BIGSERIAL`, `SERIAL`, and `SMALLSERIAL` autoincrement primary key DDL execution.
  - Supported PostgreSQL-specific `INSERT ... RETURNING` runtime evaluation and `RowDescription` metadata propagation.
  - Constructed `pg_catalog.pg_indexes` virtual catalog view for XORM index reverse-engineering.
- **TDS MSSQL DML Compatibility**:
  - Supported `UPDATE <target> FROM <source> JOIN ...` and `DELETE FROM <target> FROM ...` multi-table joined mutations.
  - Eliminated variable prefix corruption and unmatched boundary parentheses in composite indexes.
- **Gitea Comprehensive Mock Seeder (`scripts/seed_gitea_mock_data.go`)**:
  - Seeded realistic enterprise workloads covering users, orgs, repositories, issues, labels, milestones, branches, commits, pull requests, reviews, and releases.

---

### 2.2. High-Performance Network Server Pipeline (`PERF-SERVER-PIPELINE`)
- **Socket I/O Coalescing & Packet Pooling (`Phase 1`)**:
  - Applied TCP socket read coalescing, 32KB packet buffer recycling (`sync.Pool`), and thread session pinning to eliminate OS context-switching overhead.
- **Zero-Allocation RPC Parameter Binding (`Phase 2`)**:
  - Eliminated heap allocations during TDS RPC parameter deserialization, dispatching directly to precompiled virtual machine execution plans (`PlanCache`).
  - Benchmarked network TPC-C performance at **135,000 tpmC / 5,000 TPS** on a single node.

---

### 2.3. Analytical Engine & Star Schema Benchmark (`BENCH-SSB-1`, `PERF-COMPLEX-QUERY-1`)
- **Star Schema Benchmark (SSB) CLI Tool**:
  - Built a standalone benchmarking suite measuring 13 complex analytical decision-support queries over dimensional star schemas.
- **Zero-Allocation Tuple AST Evaluator**:
  - Minimized garbage collection overhead during projection and filter evaluation via zero-copy tuple slicing and arithmetic aggregate specialization.
- **Nested Expression Operator Audit Suite (`AUDIT-EXPR-1`)**:
  - Verified deep mathematical expression evaluation across complex nested parentheses and unary/binary operators.

---

### 2.4. T-SQL Procedural & Multi-Dialect Ergonomics
- **T-SQL Fast Forward-Only Cursor (`PROC-CURSOR-1`)**:
  - Implemented read-only cursor lifecycle (`DECLARE CURSOR`, `OPEN`, `FETCH NEXT INTO`, `CLOSE`, `DEALLOCATE`) and `@@FETCH_STATUS` context tracking.
- **High-ROI Multi-Dialect Built-in Functions (`FUNC-DIALECT-7`)**:
  - Added native implementations for `IFNULL`, `NVL`, `IIF`, `INSTR`, `SUBSTR`, `SPLIT_PART`, and `GENERATE_SERIES`.
- **MySQL-Style Syntax Ergonomics Phase 1 (`S-MYSQL-1-P1`)**:
  - Supported backtick (``` ` ```) identifier escaping, `DESC <table_name>`, and `SHOW TABLES`.

---

### 2.5. Release Automation & Cross-Platform Packaging
- **Self-Contained Static Binaries Across 3 Major OS Targets (`scripts/release.sh`)**:
  - macOS arm64 (`lionsql`)
  - Linux amd64 (`lionsql`)
  - Windows amd64 (`lionsql.exe`, pure Go, zero-CGo with high-resolution 1ms timer support)
- **Web Studio Tab Session Concurrency (`UI-CONCURRENCY-1`)**:
  - Enforced per-tab session isolation to prevent concurrent query collisions in browser environments.

---

## 3. Verification Scorecard

| Area | Suite / Workload | Key Verified Metrics | Notes |
| :--- | :--- | :--- | :--- |
| **OLTP Throughput** | TPC-C (TDS Network) | **135,000 tpmC / 5,000 TPS** | Verified with socket coalescing & packet pooling |
| **Analytical OLAP** | SSB (Star Schema) | 13 queries baseline measured | Powered by zero-alloc tuple AST evaluator |
| **Production Apps** | Gitea (v1.22+) | XORM migrations & DML 100% PASS | Dual-protocol support across TDS & PGWire |
| **Cross-Platform** | macOS / Linux / Windows | Static packaging 100% verified | Automated via single release script |

---

<details>
<summary><b>Detailed Changelog Delta</b></summary>

### Added
- support BIGSERIAL, SERIAL, and SMALLSERIAL autoincrement primary key DDL in Pratt parser and DDL executor (GITEA-PGWIRE-1)
- support PostgreSQL DML RETURNING clause evaluation and RowDescription schema propagation in PGWire server (GITEA-PGWIRE-1)
- implement pg_catalog.pg_indexes virtual catalog view for ORM index introspection (GITEA-PGWIRE-1)
- add comprehensive Gitea mock data seeder covering users, orgs, repos, issues, labels, milestones, branches, commits, PRs, reviews, merges, and releases (scripts/seed_gitea_mock_data.go)
- support MSSQL UPDATE and DELETE FROM source table syntax in Pratt parser and DML parser
- support aggregate function and keyword aliases after AS in SELECT projections
- support table-qualified wildcard projections (table.*) in multi-table joins to eliminate column collisions
- implement web studio tab session isolation and overview memory caching (UI-CONCURRENCY-1)
- add automated release and changelog grouping pipeline (scripts/release.sh)
- add Windows .exe cross-compilation targets to release pipeline
- integrate GitHub Release creation with downloadable assets in release pipeline
- standardize 3 official release targets (macOS arm64, Linux amd64, Windows amd64)
- add high-ROI multi-dialect built-in functions Best 7 (FUNC-DIALECT-7)
- add MySQL-style concise syntax ergonomics Phase 1 (S-MYSQL-1-P1)
- add T-SQL fast forward-only read-only cursor and FETCH_STATUS (PROC-CURSOR-1)
- add Star Schema Benchmark (SSB) suite and CLI tool (BENCH-SSB-1)
- record SSB complex query performance baseline metrics
- implement zero-alloc tuple AST evaluator and arithmetic aggregate optimization (PERF-COMPLEX-QUERY-1)
- implement operator nested expression audit and deep assertion test suite (AUDIT-EXPR-1)
- implement socket read coalescing, 32KB packet sync.Pool, and session pinning (PERF-SERVER-PIPELINE-P1)
- implement zero-alloc RPC parameter binding and direct PlanCache dispatch (PERF-SERVER-PIPELINE-P2)
- support conditional DDL for ORM migrations (ORM-MIGRATE-1)
- resolve XORM column introspection routing and max_length scan error (ORM-MIGRATE-2)
- support assignment aliasing, default_is_null bool scan, and fn_helpcollations (ORM-MIGRATE-3)

### Fixed
- prevent string literal parameter corruption by implementing single-pass quote-aware scanner in PGWire parameter substitution
- fix virtual catalog join interception ordering to prioritize pg_attribute column introspection over foreign keys
- decode untyped PGWire parameters (OID 0) to numeric types and coerce quoted string literals to integer/float/decimal in ParseLiteralDefault
- auto-resolve default wire port by protocol in tpcc-client
- resolve XORM GetIndexes interception collision on sys.columns queries with collation_name
- resolve composite index boundary parsing unmatched parentheses trap in bounds normalizer
- resolve variable assignment cleanSymbol prefix stripping in VDBE storage dispatch
- treat WT_NOTFOUND as no-op in cursor_remove rather than error
- make changelog insertion compatible with BSD awk on macOS
</details>
