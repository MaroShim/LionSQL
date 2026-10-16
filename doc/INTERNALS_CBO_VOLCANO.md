# LionSQL Internals: Cost-Based Optimizer (CBO) & Volcano Iterator Pipeline

This document describes the internal algorithms, physical cost models, statistical estimation techniques, and memory governor mechanics implemented in LionSQL's query planner (`pkg/planner/`) and relational execution engine (`pkg/execution/`).

---

## 1. Relational Execution Pipeline Architecture

Relational SQL queries (`SELECT`, `JOIN`, `GROUP BY`, `ORDER BY`, CTEs) execute using the classic Volcano Iterator Model. Intermediate rows stream upward on demand without materializing full relations in memory:

```
                      [ Client Application / Wire Protocol ]
                                         │ Next()
                                         ▼
                                [ ProjectIterator ]
                                         │ Next()
                                         ▼
                                [ FilterIterator ]
                                         │ Next()
                                         ▼
                         [ HashJoinIterator (Physical Join) ]
                                ╱                  ╲
                    Build Phase╱                    ╲Probe Phase
                              ▼                      ▼
                    [ SeqScanIterator ]     [ IndexScanIterator ]
                      (Inner Table)             (Outer Table)
```

### 1.1. The Volcano Iterator Contract

Defined in `pkg/execution/iterator.go`:

```go
type Iterator interface {
    Init(ctx context.Context) error
    Next() (Tuple, error)
    Close() error
}
```

- **`Init(ctx)`**: Allocates working memory, binds child iterators, and builds hash tables or sort runs where necessary.
- **`Next()`**: Pulls the next matching `Tuple` ($O(1)$ amortized). Returns `nil, nil` upon reaching end-of-stream (EOF).
- **`Close()`**: Releases memory buffers to `sync.Pool`, unlinks temporary spill files, and closes underlying table cursors.

---

## 2. Memory Governor & External Merge Sort (`SortIterator`)

To eliminate Out-Of-Memory (OOM) panics caused by large sorting operations, `SortIterator` enforces strict operator-level memory budgets governed by `work_mem`.

```
                    [ Incoming Tuple Stream from Child ]
                                     │
                                     ▼
                   ┌───────────────────────────────────┐
                   │   Accumulate in Memory Buffer     │
                   └───────────────────────────────────┘
                                     │
                         Is Memory >= work_mem?
                                ╱         ╲
                             Yes╱          ╲No
                               ▼            ▼
                   ┌───────────────────┐  Keep buffering
                   │ Sort Memory Chunk │  in RAM
                   └───────────────────┘
                               │
                               ▼
                   ┌───────────────────────────────────┐
                   │ Spill Sorted Run to Disk          │
                   │ (sqlion-sort-run-*.tmp)           │
                   └───────────────────────────────────┘
                               │
                               ▼
                   ┌───────────────────────────────────┐
                   │ Reset In-Memory Buffer for Next   │
                   └───────────────────────────────────┘
                               │
                      All Input Consumed?
                               │
                               ▼
                   ┌───────────────────────────────────┐
                   │ Initialize N-Way Min-Heap Merge   │
                   │ (tupleMergeHeap with heap.Push)   │
                   └───────────────────────────────────┘
                               │
                               ▼ Next()
                   [ Stream Smallest Tuple to Parent ]
```

### 2.1. Memory Footprint Estimation

The memory governor calculates live tuple memory usage via `EstimateTupleSize`:

```go
func EstimateTupleSize(t Tuple) int64 {
    size := int64(32) // Map header and pointer overhead
    for k, v := range t {
        size += int64(len(k)) + 48 // String header + Value struct overhead
        if v.Type == storage.TypeVarChar {
            size += int64(len(v.SVal))
        }
    }
    return size
}
```

### 2.2. N-Way Streaming Merge Heap

When reading sorted output during `Next()`:
1. Each spilled run file is wrapped in an active `bufio.Reader` and `json.Decoder`.
2. The initial tuple of each run file (and any remaining in-memory slice) is pushed into a Min-Heap (`tupleMergeHeap`).
3. `Next()` pops the smallest tuple from the heap, reads the next row from that tuple's source run, and pushes it back into the heap.
4. Total memory overhead during the merge phase is strictly $O(K)$, where $K$ is the number of run files.

---

## 3. Cost-Based Optimizer (CBO) Internals

The query planner evaluates alternative physical execution plans using cost models grounded in statistical table profiles.

### 3.1. Statistics Catalog & `ANALYZE TABLE`

When `ANALYZE TABLE <name>` runs, LionSQL computes:

```go
type TableStats struct {
    TableName    string
    RowCount     int64
    TotalBytes   int64
    Columns      map[string]*ColumnStats
    LastAnalyzed time.Time
}

type ColumnStats struct {
    ColumnName   string
    NullCount    int64
    DistinctVals int64 // NDV (Number of Distinct Values)
    Histograms   []HistogramBucket
}

type HistogramBucket struct {
    LowerBound    storage.Value
    UpperBound    storage.Value
    Count         int64
    DistinctCount int64
}
```

- **Equi-Depth Histograms**: Tables are divided into 64 equal-depth buckets. Each bucket contains approximately $\frac{\text{RowCount}}{64}$ values, providing accurate selectivity estimation even under heavy data skew.

---

### 3.2. Selectivity Estimation Formulas

Given a predicate on column $A$:

1. **Equality (`A = const`)**:
   $$\text{Selectivity}(A = c) = \frac{1}{\text{NDV}(A)}$$
2. **Range Predicate (`A > const` or `A BETWEEN c1 AND c2`)**:
   - The optimizer locates the target histogram buckets containing $c1$ and $c2$.
   - Uses linear interpolation within boundary buckets:
     $$\text{Fraction} = \frac{\text{const} - \text{LowerBound}}{\text{UpperBound} - \text{LowerBound}}$$
   - Sums all fully enclosed bucket counts.
   - Divides by total non-null row count.

---

### 3.3. Physical Operator Cost Models

Physical plan cost ($C$) is evaluated as a weighted sum of Page I/O and CPU evaluation costs:
$$C = (\text{Page I/O} \times W_{\text{io}}) + (\text{Tuples Processed} \times W_{\text{cpu}})$$
Where $W_{\text{io}} = 1.0$ and $W_{\text{cpu}} = 0.01$.

#### Sequential Scan vs. Index Scan
- **Sequential Scan**:
  $$C_{\text{seq}} = \text{Pages} \times W_{\text{io}} + \text{RowCount} \times W_{\text{cpu}}$$
- **Index Scan**:
  $$C_{\text{idx}} = (\text{Selectivity} \times \text{Pages}) \times W_{\text{io}} + (\text{Selectivity} \times \text{RowCount}) \times (W_{\text{cpu}} + W_{\text{idx\_lookup}})$$
- If $\text{Selectivity} < 0.15$ (highly selective), the CBO selects `IndexScanIterator`; otherwise, it favors `SeqScanIterator` to avoid random I/O thrashing.

#### Hash Join vs. Index Nested Loop Join
- **Hash Join**:
  - The CBO selects the relation with smaller total bytes as the **Build Relation**.
  - Cost incorporates building the in-memory hash table plus streaming the outer probe table.
- **Index Nested Loop Join**:
  - If the inner table has a secondary index on the join column, and the outer table is small, the CBO chooses `IndexNestedLoopJoinIterator` to avoid building full hash tables.
