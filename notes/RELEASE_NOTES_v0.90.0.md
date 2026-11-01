# LionSQL v0.90.0 Release Notes (`notes/RELEASE_NOTES_v0.90.0.md`)

> - **Target Release**: `v0.90.0` (Official GA)
> - **Version**: `v0.90.0`
> - **Theme**: **Durability & Reliability Hardening, Core Concurrency Defect Remedies, and Ecosystem Integrations Across 8 Services**

---

## 1. Release Highlights

LionSQL `v0.90.0` represents a landmark reliability milestone toward the 1.0 production release. This release is dedicated to proving crash resilience under `kill -9`, zero data loss, concurrency isolation without torn reads, and mathematical SQL three-valued logic compliance under rigorous automated test harnesses.

- **100/100 Crash-Recovery Rounds Passed with Zero Data Loss (`crashtest`)**:
  - Validated cumulative crash resilience across WiredTiger (50 rounds) and In-Memory OCC (50 rounds) under brutal interruption scenarios (`SIGKILL`, `SIGKILL+DOUBLE_KILL`, `SIGKILL+TORN_WAL_TAIL`).
  - **100% of 340,934 acknowledged transactions survived with zero data loss and zero invariant violations**.
- **Long-Duration Multi-Worker Soak Testing (`soaktest`)**:
  - Sustained continuous 8-worker transactional workload with zero resource leaks (RSS memory, goroutines, and open file descriptors maintained a strict 1.00 ratio after warm-up).
  - Maintained the global bank ledger invariant (`SUM(balance) = 1,000,000`) throughout execution.
- **SQLite SQLLogicTest Conformance Suite (`sqllogictest`)**:
  - Embedded pure-Go blackbox oracle test runner supporting rowsort, valuesort, and MD5 hash verification.
  - Passed 20 core test suites (143 statements, 121 queries) across both In-Memory OCC and WiredTiger with 100% oracle match across subqueries, CASE expressions, DISTINCT, window functions, CTEs, 3VL predicates, aggregates, and whitespace boundaries.
- **Remedied Two Latent Kernel Concurrency Defects**:
  - Eliminated WiredTiger thread-affinity cursor close `SIGSEGV` during concurrent DDL drops via thread-local lazy eviction and forced drop.
  - Eliminated In-Memory OCC `Scan()` torn reads by capturing consistent table snapshots under canonical ascending shard read locks.
- **Completed Production Integrations Across 8 Major Services**:
  - Grafana, Casdoor, Apache Answer, Vaultwarden, Metabase BI, Umami, Woodpecker CI, and Vikunja.

---

## 2. Detailed Technical Improvements

### 2.1. Durability & Reliability Test Harnesses
- **`cmd/crashtest` (kill -9 Crash Recovery Harness)**:
  - Real-time SHA-256 crash ledger (`crash_ledger`) tracking acknowledged commits versus in-flight state.
  - Enforced 4-point consistency oracle: D1 Commit Durability, D2 Atomicity (`SUM(balance)` invariant), D3 Checksum Integrity, and D4 Recoverability.
  - 100/100 rounds verified across WiredTiger (170,477 tx durable) and In-Memory OCC (170,457 tx durable).
- **`cmd/soaktest` (Long-Duration Resource & Integrity Harvester)**:
  - 8-worker concurrent transfers, periodic connection churn, continuous invariant verification, and runtime metrics scraping via Prometheus (`/metrics`).
  - Automated trend verdict detecting linear RSS growth, goroutine leakage, file descriptor exhaustion, or p99 latency degradation.
  - Verified on WiredTiger (1-hour soak, 393k rows durable, 28 FDs pinned) and In-Memory OCC.
- **`cmd/sqllogictest` & `pkg/slt` (SQLite Blackbox Oracle Runner)**:
  - Validated core relational algebra, three-valued logic (`NULL = NULL`, `val = NULL`, `NOT BETWEEN`, `NOT IN`), window functions, CTEs, operator associativity, and whitespace invariance across 20 curated test suites (143 statements, 121 queries).

---

### 2.2. Critical Kernel Bug Remedies
- **WiredTiger Thread-Affinity Cursor Eviction `SIGSEGV` Remedy**:
  - Diagnosed a thread-boundary violation where dropping a table across sessions triggered cross-thread cursor closing in WiredTiger's C engine.
  - Resolved via thread-local pending closure queues (`s.toClose`) and `go_wt_session_drop(..., "force=true")`.
- **In-Memory OCC `Scan()` Atomic Snapshot Remedy**:
  - Resolved torn read anomalies during table scans (`SELECT SUM(balance)`) under high concurrent write load.
  - Implemented `snapshotRowsAndKeys()` with ascending canonical shard read locks (0 to 255), guaranteeing lock-free, torn-read-immune scan cursors.
- **WiredTiger `WT_ROLLBACK` Serialization Mapping**:
  - Wrapped write conflicts (-31800) into `storage.ErrSerializationFailure` mapped to PGWire `SQLSTATE 40001` for transparent client retries.
- **Crash Recovery Edge-Case Fixes**:
  - Prevented user buffer truncation in WiredTiger via explicit `transaction_sync`, eliminated `CREATE TABLE` constraint prefix collisions, and fixed numeric parameter inference in PGWire.

---

### 2.3. SQL Parser, AST & Three-Valued Logic Compliance
- **Function Distinct Parsing Support**:
  - Added `FunctionCallExpr.Distinct` AST representation and parser support for `COUNT(DISTINCT col)` and `SUM(DISTINCT col)`.
- **SQL Three-Valued Logic (3VL) Compliance**:
  - Corrected `NULL = NULL`, `val = NULL`, `NOT BETWEEN`, and `NOT IN` (including `NULL` list elements) evaluations to strictly return UNKNOWN (falsy) per SQL standards.
- **AST Deparser Operator Precedence Guard**:
  - Guaranteed preservation of parenthesized grouping in `(A OR B) AND C` during AST deparsing.
- **Whitespace-Invariant Keyword Scanning (`sqlutil`)**:
  - Standardized multi-word keyword scanning (`ScanKeyword`, `ContainsKeyword`, `HasPrefixKeyword`) across arbitrary tabs, newlines, and multi-space intervals, replacing naive substring matching.
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
- **PGWire Prepared Statement Direct VDBE Binding (`PERF-PGWIRE-PREP-TX`)**: Bypassed redundant re-parsing within transactions.
- **PGWire 32KB Output Buffer Pooling & `SELECT 1` Fast-Path Ping**.
- **TPC-C 3.6 Symmetrical Benchmark Matrix**: Documented performance metrics across 32, 100, and 200 concurrent workers for WiredTiger and In-Memory OCC.

---

## 3. Verification Scorecard

| Area | Suite | Workload Scale | Key Metrics / Verdict | Status |
| :--- | :--- | :--- | :--- | :---: |
| **Crash Recovery** | `crashtest` (WiredTiger) | 50 rounds (kill -9) | 170,477 tx durable / 0 data loss | **PASS** |
| **Crash Recovery** | `crashtest` (In-Memory OCC) | 50 rounds (kill -9) | 170,457 tx durable / 0 data loss | **PASS** |
| **Soak Stability** | `soaktest` (WiredTiger) | 8 workers continuous | Invariant intact / 28 FDs pinned (0 leak) | **PASS** |
| **Soak Stability** | `soaktest` (In-Memory OCC) | 8 workers continuous | Invariant intact / RSS ratio 1.00 (0 leak) | **PASS** |
| **Blackbox Oracle** | `sqllogictest` | 20 files / 121 queries | 0 discrepancies / 7 kernel bugs fixed | **PASS** |
| **Data Race Freedom** | `go test -race ./...` | Full repository | 0 race conditions across all packages | **PASS** |
