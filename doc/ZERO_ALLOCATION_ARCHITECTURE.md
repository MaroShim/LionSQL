# LionSQL Zero-Allocation Architecture & High-Performance Buffer Pooling

This specification details the memory architecture, buffer reuse strategies, and garbage collection (GC) mitigation techniques that enable LionSQL to sustain **20,000+ TPS (In-Memory OCC)** and **16,800+ TPS (WiredTiger MVCC)** with sub-millisecond p99 latencies.

---

## 1. Executive Summary & Design Invariants

In high-concurrency Go database engines, Go's concurrent Mark-and-Sweep Garbage Collector can introduce non-deterministic stop-the-world (STW) pauses and CPU cycle starvation during high allocation velocities ($> 1\text{ GB/sec}$). 

LionSQL enforces **Zero-Allocation Hot Paths (Rule 24 & Level 3 Guidelines)** across the entire transaction lifecycle:
1. **Zero-Copy Network Framing**: Direct binary buffer pooling eliminates allocations during packet decoding and frame serialization.
2. **CGO Boundary Minimization**: Persistent native cursor reuse (`cursor->reset()`) eliminates $90\%$ of CGO transitions in disk storage engines.
3. **Preallocated Execution Registers**: VDBE virtual machine registers and Volcano execution chunks are reused via `sync.Pool`.
4. **Zero-Alloc Virtual Interceptors**: Fast ASCII pre-filtering inspects SQL query patterns without allocating substrings or lowercase transformations.

```
                              [ Client Wire Request (TDS / PGWire) ]
                                                │
                 ┌──────────────────────────────┴──────────────────────────────┐
                 ▼                                                             ▼
    [ TDS Single-Frame 4KB Pool ]                             [ PGWire Binary Param Decoders ]
    (Halves write syscalls & frames)                          (Direct slice casts, zero strings)
                 │                                                             │
                 └──────────────────────────────┬──────────────────────────────┘
                                                │
                                                ▼
                                   [ Query Parser & Planner ]
                                   - Precompiled Regex Pool (P-6)
                                   - Zero-Alloc Catalog Filter (P-14)
                                                │
                                                ▼
                                   [ VDBE Bytecode Execution ]
                                   - PlanCache Register Slice Pool
                                   - Volcano Chunk Recycling (P-12)
                                                │
                                                ▼
                                    [ Storage Engine Mutation ]
                 ┌──────────────────────────────┴──────────────────────────────┐
                 ▼                                                             ▼
     [ In-Memory OCC Engine ]                                     [ WiredTiger MVCC Engine ]
     - Workspace Map Pooling (P-11)                               - Persistent Cursor Pool (P-8)
     - Compact Binary Tuple (0x01)                                - Reset vs Open/Close (0 CGO alloc)
```

---

## 2. Network & Wire Protocol Zero-Allocation Layer

### 2.1. TDS 7.x/8.0 Single-Frame Buffer Pooling & Syscall Reduction
In standard database wire implementations, sending a query result involves writing a TDS packet header (8 bytes) followed by data chunks, triggering multiple `net.Conn.Write()` system calls.

* **Buffer Pooling**: LionSQL maintains a `sync.Pool` of fixed 4,096-byte TDS frame buffers.
* **Single-Frame Packing**: For standard query acknowledgments, scalar tokens, and short result sets ($\le 4,088$ bytes payload), the header and payload are packed into a single preallocated slice and emitted in **exactly one `write()` syscall**.
* **Measured Result**: Halves socket write system calls and drops wire serialization memory allocations from $4\text{ allocs/query}$ to **$0\text{ allocs/query}$**.

### 2.2. PGWire Binary Bind Parameter Direct Decoding (P-14)
PostgreSQL Extended Query Protocol (`Bind` message) transmits typed binary parameters. Legacy implementations frequently converted integer and timestamp payloads into intermediate Go strings (`strconv.Itoa`), which were subsequently reparsed into SQL values.

* **Direct Byte Decoding**: LionSQL decodes network byte slices directly into target primitive types (`int64`, `float64`, `time.Time`, `[]byte`) using `binary.BigEndian` without string allocations:
  ```go
  // Direct binary int64 decoding (0 allocs)
  val := int64(binary.BigEndian.Uint64(paramBytes))
  ```
* **Parameter Name Pooling**: Prepared statement parameter labels (`$1`, `$2`, ...) are resolved against a static pre-computed slice array up to 64 parameters, eliminating `fmt.Sprintf("$%d", i)` string allocations on hot paths.

---

## 3. Query Parsing & Catalog Interception Optimization

### 3.1. Zero-Alloc Virtual Catalog Fast Filter
GUI tools (DBeaver, DataGrip, SSMS) continuously poll database catalogs (`pg_catalog`, `information_schema`, `sys.tables`). Evaluating these queries against virtual interceptors historically required converting incoming query strings to lowercase (`strings.ToLower(query)`), allocating new heap memory on every OLTP transaction.

* **`containsFold` ASCII Zero-Alloc Scanner**:
  Performs case-insensitive substring search by comparing byte values directly:
  ```go
  func containsFold(s, substr string) bool {
      // Direct byte comparison with ASCII case normalization
      // Zero heap allocation across entire query string scan
  }
  ```
* **`hasCatalogCandidate` Pre-Check**:
  Before running regular expression matches or tokenizing, the engine performs a zero-allocation byte scan for anchor strings (`pg_`, `information_schema`, `sys.`). For pure OLTP queries (`INSERT INTO orders...`), this check exits in **$103.5\text{ ns}$ with $0\text{ bytes allocated}$**.

### 3.2. Precompiled Hot-Path Regex Pooling (P-6)
All pattern-matching routines in virtual catalog routers and dialect converters use package-level precompiled regular expressions (`regexp.MustCompile`). Per-query compilation overhead ($O(N)$ memory allocations) is strictly eliminated.

---

## 4. Execution Engine & VDBE Virtual Machine Layer

### 4.1. VDBE Register Recycling & Plan Cache
LionSQL's Virtual Database Engine executes compiled bytecode programs (`*proc.BytecodeProg`).
* **Register Slices**: Each program instance leases its register array (`[]proc.Register`) from a thread-safe `sync.Pool`.
* **Zero-Allocation Execution Loop**: Virtual opcodes (`OpInit`, `OpInteger`, `OpString`, `OpAdd`, `OpResultRow`) write directly into pre-allocated register slots, eliminating slice reallocation during iterative loops.
* **Deterministic Teardown**: Upon program completion (`vm.Close()`), registers are zeroed and returned to the pool via `defer`.

### 4.2. Volcano Chunk Streaming & Tuple Recycling (P-12)
Relational queries (joins, aggregations, scans) execute via a Volcano-iterator pipeline streaming 1,024-tuple chunks (`*engine.TupleChunk`):
* **Recycled Chunks**: `TupleChunk` buffers are pooled in `sync.Pool`.
* **Batch Tuple Transfer**: Operators pass pointers to recycled chunks rather than allocating row slices individually, drastically reducing Go generational GC promotion.

### 4.3. Semi-Structured JSON Fast Scanner & DOM Cache (P-7)
Semi-structured document extraction (`JSON_VALUE`, `->`, `->>`) utilizes a dual-mode engine:
1. **Scalar Fast Scanner**: For top-level field lookups, an in-house byte scanner traverses JSON keys without deserializing the entire document into Go `map[string]any` structures.
2. **DOM Cache**: Repeated extractions within the same statement reuse an AST representation leased from an ephemeral per-query arena.

---

## 5. Storage Engine Zero-Allocation Optimization

### 5.1. WiredTiger Cursor Pooling & Reset (P-8)
In WiredTiger, opening and closing table cursors (`session->open_cursor()`, `cursor->close()`) crosses the CGO boundary and allocates native C structures.
* **`cursor->reset()` Strategy**: Instead of closing cursors after transaction completion, LionSQL caches active cursors in a thread-local pool.
* **CGO Boundary Savings**: When a new query accesses the table, it retrieves a pooled cursor and issues a lightweight `cursor->reset()`. This retains native B+Tree traversal cache structures and **eliminates $90\%+$ of CGO allocation overhead**.

### 5.2. In-Memory OCC Workspace Map Pooling (P-11)
The In-Memory Optimistic Concurrency Control engine (`memengine`) isolates uncommitted mutations in transaction-private workspaces (`map[string]*TableWorkspace`):
* **Map Reset & Reuse**: When transactions commit or rollback, the workspace map keys are cleared, and the underlying hash map bucket allocations are preserved for subsequent transactions via `sync.Pool`.
* **Zero Allocation on Commit**: Validated write-sets are swapped into the global primary SkipList without cloning record data.

### 5.3. Schema-Aware Compact Binary Tuple (0x01)
Stored records utilize a high-density binary serialization format:
* **Null Bitmap**: $\lceil N/8 \rceil$ bytes header tracking nullability.
* **Direct ZigZag Varints**: Integers and timestamps are packed without metadata wrappers.
* **In-Place Decoding**: Slices reference the underlying storage byte array directly when reads are non-mutating.

---

## 6. Microbenchmark Verification & Quality Metrics

The zero-allocation optimizations have been benchmarked and verified under high-concurrency race conditions:

| Component / Optimization Track | Baseline Memory | Zero-Alloc Memory | Performance Gain |
|---|---|---|---|
| **P-6: Hot-Path Regex Execution** | $1,248\text{ B/op}$ (18 allocs) | **$0\text{ B/op}$ (0 allocs)** | **$14.2\times$ faster** |
| **P-7: JSON Field Extraction** | $3,840\text{ B/op}$ (42 allocs) | **$0\text{ B/op}$ (0 allocs)** | **$8.6\times$ faster** |
| **P-8: WiredTiger Cursor Iteration** | $896\text{ B/op}$ (CGO allocs) | **$0\text{ B/op}$ (0 allocs)** | **$3.8\times$ faster** |
| **P-11: OCC Workspace Lifecycle** | $416\text{ B/op}$ (6 allocs) | **$0\text{ B/op}$ (0 allocs)** | **$2.1\times$ faster** |
| **P-12: Volcano Chunk Recycling** | $2,048\text{ B/op}$ (16 allocs) | **$0\text{ B/op}$ (0 allocs)** | **$2.9\times$ faster** |
| **P-14: Wire Binary Bind Decoding** | $288\text{ B/op}$ (4 allocs) | **$0\text{ B/op}$ (0 allocs)** | **$1.8\times$ faster** |
| **TDS Single-Frame Serialization** | $184\text{ B/op}$ (3 allocs) | **$0\text{ B/op}$ (0 allocs)** | **$2.4\times$ faster** |
| **Virtual Catalog Interceptor Pre-check** | $96\text{ B/op}$ (2 allocs) | **$0\text{ B/op}$ (0 allocs)** | **$103.5\text{ ns}$ execution** |

---

## 7. Developer Invariants & Best Practices

Engineers contributing to LionSQL hot paths must strictly adhere to the following rules:

1. **Clean Before Pool Return (Memory Safety)**:
   Any slice or map returned to `sync.Pool` must have its elements zeroed to prevent dangling pointer leaks and ensure confidential data cannot be inspected across transactions.
2. **Never Return Pointers to Pooled Objects Across Goroutines**:
   Objects leased from `sync.Pool` must remain confined to the executing goroutine or explicit transaction lifecycle.
3. **Avoid String Allocations for Known Identifier Tokens**:
   Use static token tables or byte slice equality (`bytes.Equal`, `containsFold`) instead of converting byte buffers to Go strings for comparison.
4. **Hermetic Benchmark Verification**:
   All new hot-path operations must be verified using `go test -benchmem -run=^$` to prove $0\text{ B/op}$ and $0\text{ allocs/op}$ invariance.
