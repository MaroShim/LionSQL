# LionSQL v0.94.0 Release Notes (`notes/release_notes/RELEASE_NOTES_v0.94.0.md`)

> - **Initial Date**: 2026-11-23
> - **Release Version**: `v0.94.0` (Minor Release)
> - **Milestone Theme**: **6-Scenario Duality-Driven Cross-Verification Suite, 14-Round Live Engine Audit (500+ Defects Remediated), Unified Transaction Manager Session Lifecycle Hardening, and Canonical sqlutil Standardization**

---

## 1. Release Highlights

LionSQL `v0.94.0` is an enterprise milestone release encompassing 216 cumulative commits. It establishes an end-to-end **Transaction Management & Lexical Safety Architecture**, implements a **6-Scenario Duality-Driven Cross-Verification Harness** validating engine invariants across dual architectural axes, and completes an exhaustive **14-Round Live Engine Audit** resolving all real-world edge cases:

- **Transaction Management & Lexical Safety (Unified TxManager & Canonical sqlutil)**:
  - **Unified Transaction Manager & Session Lifecycle Hardening**: Eliminated transaction handle and lock leaks during abrupt client disconnects and network timeouts. Bound transaction control statements (`BEGIN`, `COMMIT`, `ROLLBACK`) strictly to the connection's session transaction manager (`cs.Tx()`), preventing ephemeral VDBE VM bytecode misrouting.
  - **Canonical `pkg/sqlutil` Standardization & Modular Deduplication**: Introduced `sqlutil.DeduplicateRowsAndValues` (`pkg/sqlutil/dedup.go`) to ensure identical `SELECT DISTINCT` behavior across simple and extended query protocols. Exported `DecodeQueryResult` with sequential column mapping (`col#1`, `col#2`) to resolve column name overwrites in self-joins and composite aggregates.
  - **Context-Blind `strings.*` SQL Parsing Ban**: Enforced Invariant 12: Total ban on context-blind `strings.*` SQL manipulation in favor of grammar-aware `pkg/sqlutil` primitives.

- **6-Scenario Duality-Driven Cross-Verification Architecture (`ARCH-DUALITY-CROSS-VERIFICATION-1`)**:
  - Moves beyond single-point test suites by coupling the engine's dual implementation axes into continuous symmetry verification:
    1. **Scenario A (Dual-Wire Differential SLT)**: Concurrently streams 10,706 core SQLLogicTest queries over both PGWire (`:5433`) and TDS (`:1433`) wire protocols, asserting 100% byte-for-byte result symmetry (`SLT-DUALITY-WIRE-1`).
    2. **Scenario B (Dual-Storage Twin-Engine SLT)**: Cross-executes 21,412 queries against both the In-Memory OCC storage kernel and the WiredTiger disk MVCC B+Tree engine, verifying zero relational divergence (`SLT-DUALITY-STORAGE-1`).
    3. **Scenario C (Dual-Parser Differential SLT)**: Cross-checks AST generation between the sub-microsecond Native Pratt parser and the official Microsoft T-SQL ANTLR4 grammar across 34 core query and 30 core procedural suites, proving 100% AST parity (`SLT-DUALITY-PARSER-1`).
    4. **Scenario D (T-SQL Stored Procedure Wire Duality)**: Cross-executes 30 core T-SQL procedural suites over TDS network wire (`:1433`) vs in-process execution, guaranteeing 100% RPC and procedural state symmetry (`SLT-DUALITY-TSQL-WIRE-1`).
    5. **Scenario E (Protocol Dual-Path Symmetry SLT)**: Verifies symmetry between PGWire Simple ('Q') vs Extended ('PBE') protocols, and TDS SQL Batch vs RPC `sp_executesql` paths (`SLT-DUALITY-DUALPATH-1`).
    6. **Scenario F (Cold Compilation vs Hot PlanCache Duality)**: Proves zero cache mutation, safe parameterization, and 100% result parity between cold query compilation and hot PlanCache hits (`SLT-DUALITY-PLANCACHE-1`).

- **14-Round Live Engine Audit & Remediation of 500+ Defects (`notes/reports/20261120_LIVE_AUDIT_ROUNDS_1_TO_14.ko.md`)**:
  - Executed adversarial live probing via real `psql` 16.15 and multi-session Go harnesses across 14 engine domains:
    - **Round 1 (T-SQL Procedure Lifecycle)**: Prevented silent data loss in `OUTPUT INTO @table_variable`, isolated `SCOPE_IDENTITY()` stack frames, cleaned up `#temp` tables on procedure exit, expanded in-proc CTE/VIEW/MERGE.
    - **Round 2 (Numeric and Date Arithmetic)**: Accurate `DATEADD`, `DATEDIFF`, float promotion, and window frame boundary clamping.
    - **Round 3~6 (Core Engine Clusters)**: Hardened DML, JOIN, Primary Key short-circuiting, and relational operator semantics.
    - **Round 7~8 (Builtin Functions)**: System function arity checks, date constructors, and window aggregate alignment.
    - **Round 9 (Security & Transactions)**: Formalized RBAC/DCL permissions, encryption, and transaction isolation.
    - **Round 10 (VM & Cursors)**: Optimized VDBE VM register recycling and cursor state machine transitions.
    - **Round 11 (Sequences & Indexes)**: Eliminated concurrency races in `NEXT VALUE FOR` and unified WiredTiger secondary index namespaces.
    - **Round 12 (JSON & Datatypes)**: Full RFC 8259 JSON strict mode compliance, XML method rejection, and CONVERT style parity.
    - **Round 13 (Aggregates & DML Scopes)**: Deferred tuple deserialization parity, MERGE cardinality guards, and DML subquery scope isolation.
    - **Round 14 (TDS Framing)**: Fixed byte offsets and framing for large variable-length PLP binary streams and synchronized scalar WHERE 1=0 metadata.

- **Zero-Tampering Oracle 4-Layer Lockdown & 1,289 T-SQL Procedural Suites 100% PASS**:
  - Validated 409,000+ SQLLogicTest queries and 1,289 T-SQL procedural suites (37,178 records) with 0 data races.

---

## 2. Detailed Changes

### 2.1. Transaction Management & Lexical Safety
- **Centralized Session Transaction Control**:
  - Routed all transaction control statements directly to `TxManager`.
- **Modular Deduplication (`pkg/sqlutil/dedup.go`)**:
  - Centralized row and value deduplication logic across VDBE and Volcano execution paths.
- **Eradication of Context-Blind `strings.*`**:
  - Replaced flat string operations with grammar-aware lexing primitives (`sqlutil.FindTopLevelKeyword`, `sqlutil.SplitTopLevel`).

### 2.2. 6-Scenario Duality-Driven Cross-Verification (`ARCH-DUALITY-CROSS-VERIFICATION-1`)
- **Dual-Wire Differential SLT (`SLT-DUALITY-WIRE-1`)**:
  - Implemented `pkg/slt/wire_duality_test.go` to stream identical SLT test scripts to PGWire and TDS endpoints, asserting byte-for-byte result symmetry across 10,706 queries.
- **Dual-Storage Twin-Engine SLT (`SLT-DUALITY-STORAGE-1`)**:
  - Implemented `pkg/slt/storage_duality_test.go` verifying 21,412 query executions across In-Memory OCC and WiredTiger MVCC engines.
- **Dual-Parser Differential SLT (`SLT-DUALITY-PARSER-1`)**:
  - Implemented `pkg/proc/parser_duality_test.go` proving 100% AST parity between Native Pratt and ANTLR4 parsers.
- **T-SQL Stored Procedure Wire Duality (`SLT-DUALITY-TSQL-WIRE-1`)**:
  - Implemented `test/slt_proc/proc_wire_duality_test.go` validating 30 core procedural suites across TDS wire and in-process executors.
- **Protocol Dual-Path Symmetry (`SLT-DUALITY-DUALPATH-1`)**:
  - Implemented `pkg/slt/dualpath_duality_test.go` testing parameter binding symmetry between Simple/Extended PGWire and Batch/RPC TDS paths.
- **PlanCache Cold vs Hot Duality (`SLT-DUALITY-PLANCACHE-1`)**:
  - Implemented `pkg/slt/plancache_duality_test.go` proving zero cache mutation and identical execution semantics across cold compilation and hot cache hits.

### 2.3. Core Engine Audit Fixes
- **Procedure DML `OUTPUT INTO @table_variable` Routing**:
  - Routed generated keys and output tuples into table variables in `pkg/engine/sqlexec_dml.go` and `pkg/proc/runtime/vm.go`.
- **Trigger `SCOPE_IDENTITY()` Frame Isolation**:
  - Separated identity counters per execution stack frame to prevent nested trigger insertions from overwriting caller identity scopes.
- **Automatic `#temp` Table Cleanup**:
  - Added deterministic cleanup hooks to session unwinding upon procedure completion.
- **TDS PLP Stream Framing**:
  - Fixed chunk offset calculations and termination headers for LOB and binary streams exceeding 8,000 bytes.

---

## 3. Verification & Compliance

1. **3-Stage Pipeline Verification (`[RULE:PipelineVerification]`)**:
   - Official release binaries compiled for macOS (`darwin-arm64`), Linux (`linux-amd64`), and Windows (`windows-amd64`).
   - `go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/... ./test/e2e/...` 100% PASS (0 data races).
2. **All Release Gates Verified**:
   - `[Gate 0/6]` Dual-AI Pre-Release Audit: PASS (0 unresolved P0 items).
   - `[Gate 0.5/6]` Zero-Tampering Oracle Invariant: PASS (1,289/1,289 hashes matched).
   - `[Gate 1/6]` Core Units, Regressions, E2E & Race Suites: PASS (100% across all packages).
   - `[Gate 2/6]` 13-Domain Extended Smoke (Memory & WiredTiger): PASS (psql 8-command smoke OK).
   - `[Gate 3/6]` TPC-C OLTP Benchmark (>600k tpmC): PASS (Memory & WiredTiger).
   - `[Gate 4/6]` SQLLogicTest Conformance (400k+ queries): PASS (Zero mismatches).
   - `[Gate 5/6]` T-SQL Stored Procedure 1,289-Suite Hermetic Conformance: PASS (37,178 records OK).
   - `[Gate 6/6]` Crash Durability & Resource Leakage Sentinels: PASS (Zero data loss, 0 leaks).
