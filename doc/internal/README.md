# LionSQL Core Engine Internals (`doc/internal/`)

> [한국어 문서 (Korean Version)](README.ko.md)

This directory contains low-level architectural blueprints, binary format specifications, virtual machine instruction set architectures (ISA), and engine concurrency invariants for **LionSQL** core database developers and engine maintainers.

---

## Technical Specifications Index

### 1. Storage & Persistence
* **[`STORAGE.md`](STORAGE.md)**
  * Deep dive into the `storage.Engine` interface contract.
  * In-Memory OCC (monotonically increasing integer versions) vs WiredTiger lock-free B+Tree page structures.
  * Checkpoints, AOF log compaction, physical storage directory layout, and metadata persistence (`catalog.json`, `cbo_stats.json`).
* **[`RECORD_BINARY_FORMAT.md`](RECORD_BINARY_FORMAT.md)**
  * Schema-Aware Compact Binary Tuple serialization specification (`pkg/storage/tuple.go`).
  * Bit-level layout: `0x01` version header, $\lceil N/8 \rceil$ null bitmap, and dense ZigZag varint payloads.
  * Lineage: PostgreSQL heap tuple `t_bits`, CockroachDB/TiDB KV encoding, and Protobuf varints.
* **[`TRANSACTION_CONCURRENCY.md`](TRANSACTION_CONCURRENCY.md)**
  * Transaction isolation and concurrency model specification across both storage backends.
  * Non-blocking snapshot reads and private workspace isolation.
  * In-Memory OCC monotonic versioning vs WiredTiger MVCC with hazard pointers and transaction timestamps.
  * VDBE transaction coupling (`exec.RunInTx`) and benchmark transaction fidelity.

### 2. High-Performance Execution & Memory
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

---

## Navigation & SSOT

- **Technical Documentation Root**: [`doc/README.md`](../README.md)
- **Developer Context & Notes**: [`notes/README.md`](../../notes/README.md)
- **Official Reference Manual**: [`doc/REFERENCE_MANUAL.ko.md`](../REFERENCE_MANUAL.ko.md)
