# Duality-Driven Cross-Verification Methodology
## 4-Layer Architectural Duality as an Autonomous Ground Truth Specification (`ARCH-DUALITY-CROSS-VERIFICATION-1`)

> - **Initial Date**: 2026-11-22
> - **Last Updated**: 2026-11-23
> - **Domain**: Core Architecture & Testing Methodology
> - **Milestone**: `1.0-GA` / `v0.94.0`
> [Korean Version](DUALITY_DRIVEN_CROSS_VERIFICATION_SPEC.ko.md)

---

## 1. Executive Summary & Philosophy

### 1.1 The Test Oracle Problem in RDBMS Engineering
In database engine development, the most difficult and expensive challenge is the **"Test Oracle Problem"**:
- When executing arbitrary SQL queries, stored procedures, or concurrent transaction sequences, asserting whether the returned result set (tuples, column types, sort orders, NULL semantics, isolation level) is **True Ground Truth** requires an authoritative external benchmark (Oracle).
- Traditional RDBMS engineering spins up external commercial engines (MS SQL Server, PostgreSQL) in Docker containers to compare results. However, this incurs three fundamental limitations:
  1. **Network & Environment Noise**: Connection timeouts, container boot delays, and flaky CI runs.
  2. **Non-Determinism**: OS scheduler jitter and differing internal background timers.
  3. **Black-box Boundary**: External engines only expose outer wire results and cannot inspect internal VDBE registers, binary tuple codecs, or B-Tree slot structures.

### 1.2 The Discovery of Architectural Duality
A comprehensive survey of LionSQL reveals that every critical layer from top-level network wire protocols to disk storage is designed as **two independent, production-grade implementations (Dual Implementations)**:
- **Wire Protocols**: **Microsoft TDS 7.x/8.0** vs **PostgreSQL Wire v3**
- **SQL Parsers**: **Native Fast-Path Pratt Parser** vs **ANTLR4 Official T-SQL Grammar**
- **Execution Engines**: **VDBE Bytecode Virtual Machine** vs **Volcano CBO Query Planner**
- **Storage Engines**: **In-Memory OCC (FlatRecord + WAL)** vs **WiredTiger MVCC B-Tree**

This specification transforms these dual tracks into a **Self-Checking Living Oracle**: two divergent implementations that continuously cross-validate each other to eliminate even a 1-bit semantic deviation.

---

## 2. 4-Layer Architectural Duality Topology

```
===================================================================================================================
 Layer                       Track A (Alpha)                     Track B (Beta)                      Verification Invariant
===================================================================================================================
 Layer 1: Wire Protocol      Microsoft TDS 7.x/8.0 (:1433)       PostgreSQL Wire v3.0 (:5433)        CanonicalResultSet Parity
                             [PacketSQLBatch vs PacketRPC]       [Simple Query 'Q' vs Extended PBE]  (Zero protocol-level data loss)
-------------------------------------------------------------------------------------------------------------------
 Layer 2: SQL Parser         Tier 1: Native Pratt Lexer/Parser   Tier 2: ANTLR4 Official Grammar     Normalized AST Equivalence
                             (Sub-microsecond Zero-Alloc)        (Official T-SQL Formal Grammar)     (Zero syntax/precedence error)
-------------------------------------------------------------------------------------------------------------------
 Layer 3: Execution Engine   VDBE Bytecode Virtual Machine       Volcano CBO Query Planner           Tuple Stream & 3VL Semantics
                             (SQLite-style Register Bytecode)    (Iterator-based Operator Pipeline)  (Zero evaluation drift)
-------------------------------------------------------------------------------------------------------------------
 Layer 4: Storage Engine     In-Memory OCC Storage Engine        WiredTiger MVCC B+Tree Engine       State Checksum & Index Parity
                             (Version Counter + Memory Flat WAL) (Hazard Pointer Lock-Free B-Tree)   (Zero silent corruption)
===================================================================================================================
```

---

## 3. 6-Scenario Duality-Driven Cross-Verification Suite

### Scenario A: Dual-Wire Differential SLT (`SLT-DUALITY-WIRE-1`)
- **Harness**: `pkg/slt/wire_duality_test.go`
- **Mechanism**: Concurrently executes identical test scripts over real TDS (`:1433`, `go-mssqldb`) and PGWire (`:5433`, `pgx`) TCP connections.
- **Coverage**: All 34 core SQLLogicTest suites (10,706 queries).
- **Result**: 100% byte-for-byte result symmetry achieved.

### Scenario B: Dual-Storage Twin-Engine SLT (`SLT-DUALITY-STORAGE-1`)
- **Harness**: `pkg/slt/storage_duality_test.go`
- **Mechanism**: Injects identical DDL and DML workloads into isolated In-Memory OCC and WiredTiger B+Tree instances, comparing rowsets and B-Tree secondary index ordering.
- **Coverage**: 21,412 cross-engine query executions.
- **Result**: 100% relational consistency and identical index scan order.

### Scenario C: Dual-Parser Differential SLT (`SLT-DUALITY-PARSER-1`)
- **Harness**: `pkg/proc/parser_duality_test.go`
- **Mechanism**: Cross-checks ASTs generated by the sub-microsecond Native Pratt parser and ANTLR4 official grammar.
- **Coverage**: 34 core query suites + 30 core stored procedure suites.
- **Result**: 100% AST node equivalence and verified fail-closed fallback.

### Scenario D: T-SQL Stored Procedure Wire Duality (`SLT-DUALITY-TSQL-WIRE-1`)
- **Harness**: `test/slt_proc/proc_wire_duality_test.go`
- **Mechanism**: Executes 30 core T-SQL stored procedure suites across TDS network wire (`:1433`) vs in-process coordinator.
- **Coverage**: 30 core procedural suites.
- **Result**: 100% RPC packet framing, output parameter, and return value symmetry.

### Scenario E: Protocol Dual-Path Symmetry SLT (`SLT-DUALITY-DUALPATH-1`)
- **Harness**: `pkg/slt/dualpath_duality_test.go`
- **Mechanism**: Asserts result symmetry between PGWire Simple ('Q') vs Extended ('PBE') paths, and TDS SQL Batch vs RPC `sp_executesql` paths.
- **Coverage**: 34 core query suites + 30 core procedural suites.
- **Result**: 100% parameter binding and statement splitting symmetry.

### Scenario F: Cold Compilation vs Hot PlanCache Duality (`SLT-DUALITY-PLANCACHE-1`)
- **Harness**: `pkg/slt/plancache_duality_test.go`
- **Mechanism**: Validates that 1st execution (Cold compilation) and 2nd+ execution (Hot PlanCache hit) produce identical results with zero in-place AST/bytecode mutations.
- **Coverage**: 34 core query suites + 30 core procedural suites.
- **Result**: 100% mutation safety verified with 0 data races.

---

## 4. Engineering Comparison

| Criterion | External Docker DB Oracle | LionSQL Duality Cross-Verification |
| :--- | :--- | :--- |
| **External Dependencies** | Requires Docker, network, MS SQL / PG instances | **Completely self-contained standalone static binary** |
| **Execution Speed** | Seconds per suite (network latency & socket handshakes) | **Milliseconds (in-memory & direct loopback)** |
| **Determinism** | Potential flakes due to OS scheduling & external clock drift | **100% Hermetic & deterministic (`[RULE:DeterministicTesting]`)** |
| **Observability** | Black-box wire responses only | **Full white-box inspection across Wire, Parser, VM, Storage** |
| **Sensitivity** | Coarse-grained crash detection | **Detects 1-bit type OID, alignment, or ordering differences** |

---

## 5. Verification Status

All 6 Duality Cross-Verification scenarios are integrated into the automated release pipeline (`scripts/release.sh`) and continuous integration test suites.
- `gate_protocol_symmetry_test.go`: Verified in Gate 1.
- `wire_duality_test.go`: 10,706 queries verified.
- `storage_duality_test.go`: 21,412 queries verified.
- `parser_duality_test.go`: 64 suites verified.
- `proc_wire_duality_test.go`: 30 suites verified.
- `dualpath_duality_test.go`: 64 suites verified.
- `plancache_duality_test.go`: 64 suites verified.
