# In-Memory OCC Performance Tuning Plan & Optimization Roadmap

This document defines the architectural analysis, root-cause investigation, and phased engineering roadmap for maximizing the transaction throughput (TPS) and eliminating latency spikes in LionSQL's In-Memory Optimistic Concurrency Control (OCC) engine (`pkg/storage/memengine/`).

---

## 1. Executive Summary & Context

LionSQL provides two production-grade storage backends:
1. **WiredTiger MVCC Engine (`pkg/storage/wiredtiger/`)**: Lock-free B+Tree with hazard pointers, transaction timestamps, and NVMe-optimized persistence.
2. **In-Memory OCC Engine (`pkg/storage/memengine/`)**: Pure in-memory row storage with monotonically increasing version counters, fine-grained sharding, and concurrent SkipList indices.

In high-concurrency OLTP benchmarks (TPC-C, VDBE), In-Memory OCC sustains 14,300 ~ 20,000+ TPS, while WiredTiger sustains 12,500 ~ 16,800 TPS. However, under multi-core server environments (high core-count Linux servers and dual-socket Xeon machines), the performance delta between In-Memory OCC and disk-backed WiredTiger is narrower than theoretically expected (~15% ~ 20% margin). 

Furthermore, periodic throughput drops and stepwise latency plateaus were observed during sustained write workloads. This document formalizes the root-cause diagnosis and presents a concrete 7-stage optimization plan.

---

## 2. Root Cause Analysis: The Synchronous Compaction Freeze

### 2.1. Synchronous AOF Rewrite on Commit Hot-Path
During high-frequency transaction writes, the In-Memory engine logs mutations to an Append-Only File (WAL/AOF). When WAL size reaches the 64MB compaction threshold (`aofThreshold`):
1. `finishWALCommitLocked()` in `wal.go` synchronously triggers `rewriteAOFLocked()` directly inside the active committing goroutine.
2. `rewriteAOFLocked()` invokes `saveDiskLocked()`, which calls `syncShardsToMapsLocked()` for every table.
3. `syncShardsToMapsLocked()` acquires an exclusive write lock (`td.mu.Lock()`) on the table, iterating across all 64 shards and **cloning every row and version into `td.Rows` and `td.Versions` maps**.
4. The cloned maps are serialized into MessagePack binary format and written to disk while holding the table lock.

```
[ Committing Transaction Goroutine ]
              │
              ▼
   WAL reaches 64MB threshold?
              │
      ┌───────┴───────┐
   (No)             (Yes)
      │               │
      │               ▼
      │    [ Synchronous rewriteAOFLocked() ]
      │               │
      │               ▼
      │    Acquires exclusive td.mu.Lock()
      │               │
      │               ▼
      │    Full-table copy to td.Rows / td.Versions (STW Freeze)
      │    - All concurrent transactions block on td.mu.RLock()
      │    - Cloned maps linger permanently in heap, bloating GC
      │               │
      ▼               ▼
[ Commit Complete / Released ]
```

### 2.2. Blast Radius & GC Bloat
- **Stop-the-World Stalls**: All concurrent read and write transactions block waiting for `td.mu.RLock()`, causing throughput to collapse for 100ms ~ 500ms intervals.
- **Permanent Heap Pollution**: The cloned `td.Rows` and `td.Versions` maps remain allocated on `TableData` even after disk persistence completes, permanently expanding the Go runtime heap and increasing pointer-scan overhead during every garbage collection cycle.

---

## 3. Structural Bottleneck Taxonomy (8 Identified Bottlenecks)

| # | Subsystem / Location | Structural Flaw | Performance Impact |
| :---: | :--- | :--- | :--- |
| **1** | `storage.Value` (`pkg/storage/`) | `Value` struct bundles `SVal`, `TVal` (`time.Time`), `DVal`, `BinVal` and interface pointers, exceeding **100+ bytes per value**. | A 20-column row occupies over 2KB on the Go heap with numerous pointers. WiredTiger stores compressed raw bytes outside the Go heap, giving WiredTiger a major GC advantage. |
| **2** | `IntValue()` (`pkg/storage/value.go`) | Every integer conversion instantiates `*big.Int` via `big.NewInt(n)` (`NewDecimalFromInt`). | Allocates heap objects and GC pointer targets for every integer column, filter comparison, and parameter binding. |
| **3** | `Commit()` (`pkg/storage/memengine/tx.go`) | Allocates fresh temporary structures (`tableSet`, `shardMap` nested maps, `seenUniqueKeys`) and slices on every single commit. Repeatedly calls `MapGetFold` 3~4 times per mutation. | High per-commit allocation count; extends lock hold times during validation. |
| **4** | `Commit()` (`pkg/storage/memengine/tx.go`) | Holds shard locks while serializing rows for WAL logging (`storage.EncodeRow`), followed by frame writes inside the global `walOrderMu`. | Prolongs lock retention; serializes all concurrent commits at the WAL write stage. |
| **5** | Shared Locks (`pkg/storage/memengine/`) | Every commit acquires `engine.mu.RLock()` and `td.mu.RLock()`. | `sync.RWMutex` read locks execute atomic operations on shared cache lines, causing severe CPU cacheline bouncing and lock contention on high-core NUMA architectures. |
| **6** | `tableShard` (`pkg/storage/memengine/table_data.go`) | Stores rows and versions in separate hash maps (`rows map[string]storage.Row` and `versions map[string]int64`), keyed by string PKs (`pk.String()`). | Each read requires 2 hash calculations and 2 map lookups, accompanied by string allocation for non-string primary keys. |
| **7** | `numTableShards` (`pkg/storage/memengine/table_data.go`) | Shard count is statically hardcoded to 64 (`const numTableShards = 64`). | Limits scaling when concurrent worker goroutines exceed 64 threads. |
| **8** | GC Configuration | `SetMemoryLimit` is active via the memory governor, but GC target (`GOGC`) remains at the Go default (100). | With a multi-gigabyte in-memory dataset, default GC triggers too frequently and steals CPU execution cycles from OLTP workers. |

---

## 4. Prioritized Optimization Roadmap

### Stage 1: Hermetic Profiling & Baseline Isolation (Zero-Risk)
- Add `-cpuprofile` and GC allocation profiling flags to benchmark harness (`cmd/bench/`).
- Benchmark with `-fsync off` (ephemeral mode) vs `-fsync everysec` to isolate WAL compaction stalls from pure in-memory execution latency.
- Establish baseline CPU flamegraphs across low-core (M-series Mac) and high-core (Xeon/EPYC Linux) environments.

### Stage 2: Runtime GC & Memory Limit Tuning (Immediate Win)
- For in-memory workloads with explicit memory budgets (`--max-memory`), increase `GOGC` (e.g., `GOGC=200` or `GOGC=off`) and rely primarily on Go 1.19+ `debug.SetMemoryLimit`.
- Prevents premature GC thrashing while large in-memory tables reside in the heap.

### Stage 3: Asynchronous Non-Blocking AOF Compaction (Eliminate Stalls)
- Eliminate `syncShardsToMapsLocked()`: Serialize shard data directly shard-by-shard using localized read locks (`shard.mu.RLock()`) instead of copying the entire table under `td.mu.Lock()`.
- Clear `td.Rows` and `td.Versions` immediately after persistence to prevent heap retention.
- Offload AOF rewrite to a detached background worker goroutine, decoupling commit path latency from disk compaction.

### Stage 4: Commit Path Allocation Elimination (Hot-Path Acceleration)
- **Bitmask Shard Tracking**: Replace nested `shardMap map[string]map[int]struct{}` with a `uint64` bitmask per table. Shard iteration in ascending bit order automatically guarantees canonical lock acquisition order without sorting or map allocations.
- **Pre-resolved Table Pointers**: Cache `*TableData` pointers during the mutation staging phase to eliminate repeated `MapGetFold` table lookups in the commit validation loop.
- **Out-of-Lock WAL Encoding**: Construct and encode WAL payloads (`storage.EncodeRow`) before acquiring shard locks, minimizing the critical section where table shards remain locked.

### Stage 5: Shared Lock Reduction (NUMA Scalability)
- Replace `engine.mu.RLock()` on read/commit paths with an `atomic.Pointer` snapshot of table catalogs (Copy-On-Write for DDL).
- Convert table-level DDL guards to epoch counters or per-CPU distributed read locks, eliminating atomic cacheline ping-pong across CPU sockets.

### Stage 6: Dynamic CPU-Proportional Sharding
- Dynamically scale `numTableShards` based on available CPU cores ($N = \text{NextPowerOf2}(\text{NumCPU} \times 2)$, minimum 64, maximum 1024) to reduce hash collisions under massive concurrency.

### Stage 7: `IntValue` BigInt Deferral
- Modify `storage.IntValue` to defer `big.Int` / `Decimal` creation. Store raw `int64` and instantiate `*big.Int` only when participating in arbitrary-precision decimal arithmetic.
- Eliminates millions of heap allocations per second during index seeks, primary key joins, and numeric filter evaluations.

---

## 5. Long-Term Architectural Re-engineering (Post-1.0 Horizons)

These structural transformations require extensive regression verification across all 25 packages and are designated for Post-1.0 (`v1.1.0+`):

1. **Unified Entry Map**:
   - Merge `shards[i].rows` and `shards[i].versions` into a single entry map:
     ```go
     type shardEntry struct {
         version int64
         row     storage.Row
     }
     ```
   - Halves hash map traversal overhead and improves CPU L1/L2 data cache locality during row lookups.
2. **Numeric Primary Key Fast-Path**:
   - Provide integer-keyed shard maps (`map[int64]*shardEntry`) for tables with integer primary keys, eliminating string conversion (`strconv.FormatInt`) and string hashing overhead entirely.
3. **Lean `storage.Value` Representation / Compact Row**:
   - Redesign `storage.Value` from a 100-byte fat struct into an 8-byte tag-and-payload union or compact binary tuple, reducing overall in-memory footprint by up to 60% and slashing GC scan overhead.

---

## 6. Verification Criteria & Guardrails

All tuning stages must satisfy the following invariant criteria before merging:
1. **Zero Data Corruption & Monotonic LSN**: LSN assignment order must strictly match physical WAL append order.
2. **ACID Serializability Integrity**: TPC-C New-Order transaction rollback rate must remain under 0.20% with zero dangling workspace writes.
3. **Flake-Free Test Invariance**: Strict passage across all 25 packages via `go test -tags wiredtiger -count=1 -race ./...` with zero data races.
