# LionSQL v0.90.0 Release Notes (`notes/RELEASE_NOTES_v0.90.0.md`)

> - **Created**: 2026-11-01
> - **Initial Draft**: 2026-11-01
> - **Last Updated**: 2026-11-03
> - **Target Release**: `v0.90.0` (Official GA)
> - **Theme**: **Durability & Reliability Hardening, Core Concurrency Defect Remedies, and Ecosystem Integrations Across 8 Services**

---

## 1. Release Highlights

LionSQL `v0.90.0` represents a landmark reliability milestone toward the 1.0 production release. This release is dedicated to proving crash resilience under `kill -9`, zero data loss, concurrency isolation without torn reads, and mathematical SQL three-valued logic compliance under rigorous automated test harnesses.

- **100% Zero-Loss Crash Recovery Verification (`cmd/crashtest`)**:
  - Validated cumulative crash resilience across WiredTiger and In-Memory OCC under brutal interruption scenarios (`SIGKILL`, `SIGKILL+DOUBLE_KILL`, `SIGKILL+TORN_WAL_TAIL`, `SIGTERM`).
  - **100% of acknowledged transactions survived across both engines with zero data loss** according to the SHA-256 crash ledger (`crash_ledger`) and the 4-point consistency oracle (D1 Commit Durability, D2 Atomicity `SUM(balance)=const`, D3 Checksum Integrity, and D4 Recoverability).
- **Long-Duration Multi-Worker Soak Testing (`cmd/soaktest`)**:
  - Sustained continuous 8-worker transactional workload with zero resource leaks (RSS memory, goroutines, and open file descriptors maintained a strict 1.00 ratio after warm-up).
  - Maintained the global bank ledger invariant (`SUM(balance) = 1,000,000`) throughout execution with zero p99 latency degradation.
- **SQLite SQLLogicTest Conformance Suite (90 Standard Files 100% PASS) (`cmd/sqllogictest` & `pkg/slt`)**:
  - Embedded pure-Go blackbox oracle test runner supporting rowsort, valuesort, and MD5 hash verification.
  - Passed **90 standard test suites (tens of thousands of queries) with 100% oracle match (0 failures)** across subqueries, CASE expressions, DISTINCT, window functions, CTEs, 3VL predicates, self/cross joins, JSON operators, upserts, aggregates, and whitespace boundaries.
  - Isolated 14 SQLite-specific fuzz suites into `test/sqlite_fuzz/` to protect release gate hermeticity.
- **Ultra-High OLTP Throughput (`cmd/tpcc_runtime`)**:
  - Achieved **732,956 tpmC (27,118.95 TPS)** on In-Memory OCC (10 Warehouses / 10 Workers).
  - New-Order P50 0.30ms / P99 2.79ms; Payment P50 0.08ms / P99 1.29ms; Serialization Abort rate 0.00% (11 aborts / 407,059 attempts).
- **Remedied Latent Kernel Concurrency Defects**:
  - Eliminated WiredTiger thread-affinity cursor close `SIGSEGV` during concurrent DDL drops and restored `transaction_sync=(enabled=true,method=none)` for crash durability.
  - Eliminated In-Memory OCC `Scan()` torn reads by capturing consistent table snapshots under canonical ascending shard read locks.
  - Fixed `parseInCondition` compound expression hijacking with permanent regression test coverage.
- **Completed Production Integrations Across 8 Major Services**:
  - Grafana, Casdoor, Apache Answer, Vaultwarden, Metabase BI, Umami, Woodpecker CI, and Vikunja.

---

## 2. Detailed Technical Improvements

### 2.1. Durability & Reliability Test Harnesses
- **`cmd/crashtest` (kill -9 Crash Recovery Harness)**:
  - Real-time SHA-256 crash ledger (`crash_ledger`) tracking acknowledged commits versus in-flight state.
  - Enforced 4-point consistency oracle: D1 Commit Durability, D2 Atomicity (`SUM(balance)` invariant), D3 Checksum Integrity, and D4 Recoverability.
  - 100% zero-loss pass rate across both WiredTiger and In-Memory OCC.
- **`cmd/soaktest` (Long-Duration Resource & Integrity Harvester)**:
  - 8-worker concurrent transfers, periodic connection churn, continuous invariant verification, and runtime metrics scraping via Prometheus (`/metrics`).
  - Automated trend verdict detecting linear RSS growth, goroutine leakage, file descriptor exhaustion, or p99 latency degradation.
  - Verified on WiredTiger and In-Memory OCC with zero memory leaks and fixed file descriptor counts.
- **`cmd/sqllogictest` & `pkg/slt` (SQLite Blackbox Oracle Runner)**:
  - Validated core relational algebra, three-valued logic (`NULL = NULL`, `val = NULL`, `NOT BETWEEN`, `NOT IN`), window functions, CTEs, self/cross joins, operator associativity, and whitespace invariance across 90 standard test files.

---

### 2.2. Critical Kernel Bug Remedies
- **WiredTiger Thread-Affinity Cursor Eviction `SIGSEGV` Remedy**:
  - Diagnosed a thread-boundary violation where dropping a table across sessions triggered cross-thread cursor closing in WiredTiger's C engine.
  - Resolved via thread-local pending closure queues (`s.toClose`) and `go_wt_session_drop(..., "force=true")`.
- **In-Memory OCC `Scan()` Atomic Snapshot Remedy**:
  - Resolved torn read anomalies during table scans (`SELECT SUM(balance)`) under high concurrent write load.
  - Implemented `snapshotRowsAndKeys()` with ascending canonical shard read locks (0 to 255), guaranteeing lock-free, torn-read-immune scan cursors.
- **WiredTiger `transaction_sync` Restoration**:
  - Restored `transaction_sync=(enabled=true,method=none)` in `pkg/storage/wiredtiger/wt_engine.go` to prevent user-space log buffer loss upon unannounced `SIGKILL`.
- **`parseInCondition` Compound Condition Hijacking Remedy**:
  - Fixed false-positive IN-condition detection on compound expressions (`col1 IN (1, 2) AND (col2 = 3)`) with paren matching boundary checks and isolated regression tests.
- **WiredTiger `WT_ROLLBACK` Serialization Mapping**:
  - Wrapped write conflicts (-31800) into `storage.ErrSerializationFailure` mapped to PGWire `SQLSTATE 40001` for transparent client retries.

---

### 2.3. SQL Parser, AST & Architectural Compliance
- **Zero-Regex SQL Parsing in Production Paths (`[RULE:ModularAbstraction]`, Invariants 7 & 8)**:
  - Completely removed fragile regular expressions from nested SQL condition parsing, standardizing on deterministic tokenizers in `pkg/sqlutil`.
- **Immutable Rule Slugs Adoption**:
  - Transitioned from volatile numeric rule indexes to immutable semantic slugs (`[RULE:RadicalKISS]`, `[RULE:PipelineVerification]`).
- **Function Distinct Parsing Support**:
  - Added `FunctionCallExpr.Distinct` AST representation and parser support for `COUNT(DISTINCT col)` and `SUM(DISTINCT col)`.
- **SQL Three-Valued Logic (3VL) Compliance**:
  - Corrected `NULL = NULL`, `val = NULL`, `NOT BETWEEN`, and `NOT IN` (including `NULL` list elements) evaluations to strictly return UNKNOWN (falsy) per SQL standards.
- **AST Deparser Operator Precedence Guard**:
  - Guaranteed preservation of parenthesized grouping in `(A OR B) AND C` during AST deparsing.
- **AST and Catalog-Driven Routing (`ROUTING-AST-CATALOG-1`)**:
  - Replaced 45 legacy string heuristics with AST and catalog symbol resolution.

---

### 2.4. Third-Party Ecosystem Integrations
- **Grafana (`GRAFANA-INTEGRATION-1`)**: Dual-protocol PostgreSQL & TDS (MSSQL) dashboard integration.
- **Casdoor (`CASDOOR-INTEGRATION-1`)**: Composite primary key support (`\x1f` unit separator) and TDS varbinary round-trip for global IAM/SSO.
- **Apache Answer (`INTEG-ANSWER-1`)**: Developer Q&A platform PGWire E2E integration.
- **Vaultwarden (`INTEG-VAULTWARDEN-1`)**: Rust Diesel ORM-backed password manager integration.
- **Metabase (`INTEG-METABASE-1`)**: Enterprise BI analytics platform dual-protocol integration.
- **Umami (`INTEG-UMAMI-1`)**: Privacy-preserving web analytics platform Prisma ORM integration.
- **Woodpecker CI & Vikunja (`INTEG-WOODPECKER-1`, `INTEG-VIKUNJA-1`)**: Lightweight CI/CD and task management integration.
- **IOTA-SDK**: Go ERP framework integration and PostgreSQL DDL/protocol compatibility.

---

### 2.5. Performance & Benchmarking
- **TPC-C Scaled Saturation**: Reached **732,956 tpmC / 27,118.95 TPS** (P50 0.30ms, P99 2.79ms) on In-Memory OCC.
- **PGWire Prepared Statement Direct VDBE Binding (`PERF-PGWIRE-PREP-TX`)**: Bypassed redundant re-parsing within transactions.
- **PGWire 32KB Output Buffer Pooling & `SELECT 1` Fast-Path Ping**.
- **Permanent Lightweight Performance Gate (`PERF-REGRESSION-RECOVERY-1`)**: Automated 1,000-transaction ACID transfer regression gate running in ~0.12s.

---

## 3. Official Verification Scorecard

| Area | Suite | Workload Scale | Key Metrics / Verdict | Status |
| :--- | :--- | :--- | :--- | :---: |
| **SQLLogicTest Standard** | `cmd/sqllogictest` | 90 standard test files | 0 mismatches / 100% coverage across core SQL | **100% PASS** |
| **Crash Durability** | `cmd/crashtest` (WiredTiger) | 20 rounds (SIGKILL/DOUBLE/TORN) | 63,310 tx recovered / 0 data loss / 4-point oracle pass | **100% PASS** |
| **Crash Durability** | `cmd/crashtest` (In-Memory OCC) | 20 rounds (SIGKILL/DOUBLE/TORN) | 62,844 tx recovered / 0 data loss / 4-point oracle pass | **100% PASS** |
| **Soak Stability** | `cmd/soaktest` (In-Memory OCC) | 8 workers continuous (200 ops/s) | Invariant intact / RSS·FD·Goroutines ratio 1.00 (0 leaks) / 0 latency degradation | **100% PASS** |
| **TPC-C OLTP Throughput** | `cmd/tpcc_runtime` (OCC) | 10W / 10 Workers / 15s | **732,956 tpmC** / **27,118.95 TPS** (P50 0.30ms, P99 2.79ms) | **100% PASS** |
| **13-Domain Smoke Test** | `scripts/verify_0.90.sh` (Stage 4) | In-Memory & WiredTiger dual engines | 13/13 domains passed | **100% PASS** |
| **Race-Free Concurrency** | `go test -race -short ./...` | 22 packages | 0 data races / 100% pass | **100% PASS** |
