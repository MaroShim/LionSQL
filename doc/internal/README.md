# LionSQL Core Engine Internals (`doc/internal/`)

> [한국어 문서](README.ko.md)

This directory contains low-level architectural blueprints, binary format specifications, virtual machine instruction set architectures (ISA), and engine concurrency invariants for **LionSQL** core database developers and engine maintainers.

---

## Technical Specifications Index

### 1. Storage & Persistence
* **[`STORAGE.md`](STORAGE.md)**
  * Deep dive into the `storage.Engine` interface contract.
  * In-Memory OCC (monotonically increasing integer versions) vs WiredTiger lock-free B+Tree page structures.
  * Checkpoints, AOF log compaction, physical storage directory layout, and metadata persistence (`catalog.json`, `cbo_stats.json`).
* **[`PHYSICAL_TABLE_FORMAT.ko.md`](PHYSICAL_TABLE_FORMAT.ko.md)**
  * Unified Physical Table Format Specification (V2 Tuple Binary Codec & Multiplexed Index B-Tree Layout).
  * 4-byte aligned header, `NullBitmap`, 1,024 column ceiling (`storage.MaxColumnsPerTable = 1024`), $O(1)$ Instant Tail DDL invariant.
* **[`WIREDTIGER_FILE_LAYOUT_AND_INDEX_MULTIPLEXING.ko.md`](WIREDTIGER_FILE_LAYOUT_AND_INDEX_MULTIPLEXING.ko.md)**
  * WiredTiger physical file layout and secondary index multiplexing specification (`_idx_<Table>.wt`, `Invariant 15`, `ARCH-WT-FILE-LAYOUT-1`).
* **[`LOGICAL_NAMESPACE_DATABASE_PREFIX.ko.md`](LOGICAL_NAMESPACE_DATABASE_PREFIX.ko.md)**
  * Model B: Logical 3-Tier Composite Key Architecture Specification (`Invariant 2`, `ARCH-LOGICAL-NAMESPACE-1`).
* **[`RECORD_BINARY_FORMAT.md`](RECORD_BINARY_FORMAT.md)**
  * Schema-Aware Compact Binary Tuple serialization specification (`pkg/storage/tuple.go`).
  * Bit-level layout: `0x01` version header, $\lceil N/8 \rceil$ null bitmap, and dense ZigZag varint payloads.
  * Lineage: PostgreSQL heap tuple `t_bits`, CockroachDB/TiDB KV encoding, and Protobuf varints.
* **[`ACID_CORE_INTEGRITY_SPEC.ko.md`](ACID_CORE_INTEGRITY_SPEC.ko.md)**
  * ACID core transaction integrity, WAL crash recovery (Fail-Closed Zero-Truncate Invariant 9), and storage checkpoint specification.
* **[`SESSION_TX_CURSOR_LIFECYCLE.ko.md`](SESSION_TX_CURSOR_LIFECYCLE.ko.md)**
  * Session, Transaction, and Server-Side Cursor lifecycle transition specification.
* **[`TX_MODULE_RIGOROUS_TEST_SPEC.ko.md`](TX_MODULE_RIGOROUS_TEST_SPEC.ko.md)**
  * Session-Transaction-Cursor state machine & 6-stage rigor testing specification (`TX-MODULE-RIGOROUS-TEST-PLAN-V2`).
* **[`TRANSACTION_CONCURRENCY.md`](TRANSACTION_CONCURRENCY.md)**
  * Transaction isolation and concurrency model specification across both storage backends.
  * Non-blocking snapshot reads and private workspace isolation.
  * In-Memory OCC monotonic versioning vs WiredTiger MVCC with hazard pointers and transaction timestamps.
  * VDBE transaction coupling (`exec.RunInTx`) and benchmark transaction fidelity.

### 2. High-Performance Execution & Memory
* **[`SEMANTIC_GATEKEEPER_ARCHITECTURE.ko.md`](SEMANTIC_GATEKEEPER_ARCHITECTURE.ko.md)**
  * Single semantic validation gateway (Scope Gatekeeper & DML Contract) architecture specification.
* **[`SQL_SEMANTIC_INTEGRITY_SPEC.ko.md`](SQL_SEMANTIC_INTEGRITY_SPEC.ko.md)**
  * SQL standard and T-SQL semantic integrity rules and Fail-Closed SQLSTATE mapping specification.
* **[`BOUND_AST_AND_DEDICATED_BINDER_SPEC.ko.md`](BOUND_AST_AND_DEDICATED_BINDER_SPEC.ko.md)**
  * Bound AST-Driven Dedicated Binder Evolution Specification (5-stage pipeline, catalog binding, $O(1)$ positional integer slot indexing).
* **[`BINDER_AND_TX_TO_BE_ARCHITECTURE.ko.md`](BINDER_AND_TX_TO_BE_ARCHITECTURE.ko.md)**
  * Dedicated Binder (BoundAST) & Transaction Module Rigor Integration To-Be System Architecture Specification.
* **[`ZERO_ALLOCATION_ARCHITECTURE.md`](ZERO_ALLOCATION_ARCHITECTURE.md)**
  * Zero-allocation memory architecture, buffer reuse strategies, and GC mitigation enabling 20,000+ TPS.
  * Layer-by-layer breakdown: TDS single-frame pooling, PGWire binary bind direct decoding, zero-alloc ASCII catalog pre-filtering (`containsFold`), VDBE register recycling, Volcano chunk streaming (`TupleChunk` sync.Pool), and WiredTiger `WT_CURSOR` pooling/reset.
* **[`VDBE.md`](VDBE.md)**
  * SQLite-style register-based Virtual Database Engine (VDBE) architecture.
  * Bytecode opcodes specification (`OpInit`, `OpOpenRead`, `OpIndexScan`, `OpRelQuery`, `OpYield`).
  * `sync.Pool` register reuse and compiled program caching (`*runtime.PlanCache`).
* **[`CBO_VOLCANO.md`](CBO_VOLCANO.md)**
  * Volcano iterator pipeline (`SeqScan`, `IndexScan`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`).
  * Cost-Based Optimizer (CBO), selectivity models, and 64-bucket equi-depth histograms.
  * Constant folding, predicate pushdown, correlated subquery decorrelation (`SEMI`/`ANTI` joins), and disk-spilling external merge sort.

### 3. Wire Protocol Emulation
* **[`WIRE_PROTOCOLS.md`](WIRE_PROTOCOLS.md)**
  * **PostgreSQL Wire Protocol v3** (Port `5433`): Simple and Extended Query Protocols (`Parse`, `Bind`, `Execute`, `Sync`), parameter binding, and SSL/TLS.
  * **Microsoft SQL Server TDS 7.x/8.0** (Port `1433`): Prelogin, Login7, RPC packets, and `sp_prepexec` bytecode plan cache connection.
  * Client compatibility matrix and goroutine connection pooler.

### 4. Architectural Duality & Cross-Verification
* **[`DUALITY_DRIVEN_CROSS_VERIFICATION_SPEC.md`](DUALITY_DRIVEN_CROSS_VERIFICATION_SPEC.md)** ([Korean](DUALITY_DRIVEN_CROSS_VERIFICATION_SPEC.ko.md))
  * **4-Layer Architectural Duality as an Autonomous Ground Truth Specification (`ARCH-DUALITY-CROSS-VERIFICATION-1`)**.
  * Complete specification for 6-scenario cross-verification: Dual-Wire (TDS vs PGWire), Dual-Storage (OCC vs WiredTiger MVCC), Dual-Parser (Pratt vs ANTLR4), T-SQL Procedure (Wire vs In-Proc), Protocol Dual-Path (Simple vs Extended, Batch vs RPC), and PlanCache (Cold vs Hot).

---

## Navigation & SSOT

- **Technical Documentation Root**: [`doc/README.md`](../README.md)
- **Developer Context & Notes**: [`notes/README.md`](../../notes/README.md)
- **Official Reference Manual**: [`doc/REFERENCE_MANUAL.ko.md`](../REFERENCE_MANUAL.ko.md)
