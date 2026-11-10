# LionSQL Internals: Transaction Isolation & Concurrency Model (`doc/TRANSACTION_CONCURRENCY.md`)

This document details the transaction processing invariants, concurrency control architectures, isolation guarantees, and virtual machine coupling implemented in LionSQL's storage engines and execution pipeline.

---

## 1. Foundational Invariants

LionSQL enforces **Non-Blocking Snapshot Isolation / Read Committed with Optimistic/Hazard Concurrency Control** as its canonical transaction processing invariant (GEMINI.md Section 4).

```
                      [ Client Transaction Lifecycle ]
                                     │
                                     ▼
                              [ sess.BeginTx() ]
                                     │
                    ┌────────────────┴────────────────┐
                    ▼                                 ▼
       [ Read Operation (SELECT) ]       [ Write Mutation (DML/DDL) ]
       - Reads from consistent snapshot  - Buffered in private workspace
       - Zero shared read locks (No 2PL) - Zero dirty read leakage
       - Readers NEVER block writers     - Writers NEVER block readers
                    │                                 │
                    └────────────────┬────────────────┘
                                     │
                                     ▼
                              [ tx.Commit() ]
                                     │
                  ┌──────────────────┴──────────────────┐
                  ▼                                     ▼
       [ In-Memory OCC Engine ]              [ WiredTiger MVCC Engine ]
       - Monotonic version validation        - B+Tree hazard pointer MVCC
       - Write-write skew detection          - Transaction timestamp check
       - Fail-fast abort (ErrWriteConflict)  - Rollback detection (WT_ROLLBACK)
```

### 1.1. Non-Blocking Reads (Readers Never Block Writers, Writers Never Block Readers)
Traditional two-phase locking (2PL) databases acquire shared locks for read operations, causing concurrent writes to stall until readers complete. LionSQL completely eliminates 2PL read locks across both backends:
- Queries always read from consistent point-in-time snapshots.
- Read operations never acquire shared locks, never wait on active transactions, and never prevent concurrent writers from modifying the same rows.

### 1.2. Private Workspace Isolation
All mutating operations (`INSERT`, `UPDATE`, `DELETE`) are strictly buffered inside transaction-private workspaces until commit:
- Uncommitted modifications are completely invisible to concurrent transactions.
- Zero dirty reads (`READ UNCOMMITTED`) are permitted.
- If a transaction aborts or encounters an error, the private workspace is discarded immediately, leaving zero partial writes or dangling records on storage.

---

## 2. Storage Engine Concurrency Profiles

LionSQL provides two distinct, production-grade storage engines optimized for different operational profiles:

### 2.1. In-Memory OCC Engine (`pkg/storage/memengine/`)

The in-memory engine implements pure **Optimistic Concurrency Control (OCC)** combined with striped fine-grained locking and concurrent SkipList secondary indices.

#### Record Versioning & State
Every record in `table_data.go` maintains an atomic, monotonically increasing 64-bit integer version:

```go
type TableRecord struct {
    Version uint64       // Monotonic OCC version counter
    Row     storage.Row  // Pre-parsed in-memory column values
}
```

#### The Three Phases of In-Memory OCC
1. **Read / Execution Phase**:
   - When a transaction reads a row, it captures the current `Version uint64` into its local transaction context.
   - Mutations (`INSERT`, `UPDATE`, `DELETE`) are staged into the transaction's private workspace without modifying the active table shards.
2. **Validation Phase (`Commit()`)**:
   - At commit time, the transaction acquires fine-grained shard write locks in canonical key order to prevent deadlocks (Golden Rule 10).
   - For every updated or deleted record, the engine validates whether the current record version matches the read version:
     $$\text{CurrentVersion} == \text{ReadVersion}$$
   - If any record was modified by another concurrent transaction between the read phase and commit phase (write-write skew), validation fails immediately.
3. **Write / Abort Phase**:
   - **On Validation Success**: The private workspace changes are atomically applied to the table shards, record versions are incremented ($\text{Version} \leftarrow \text{Version} + 1$), secondary SkipList indices are updated, and WAL records are serialized and flushed.
   - **On Validation Failure**: The transaction immediately rolls back with `storage.ErrWriteConflict`. Staged changes are discarded, and locks are released. Clients or benchmark harnesses retry the operation cleanly.

---

### 2.2. WiredTiger Storage Backend (`pkg/storage/wiredtiger/`)

WiredTiger operates as a disk-based, lock-free **Multi-Version Concurrency Control (MVCC)** B+Tree engine with hazard pointers and transaction timestamps.

#### B+Tree Hazard Pointer Protection
- Read operations traverse B+Tree nodes using hazard pointers—lock-free CPU memory barriers that guarantee memory safety without holding spinlocks or mutexes.
- Concurrent updates create new version records in WiredTiger's cache, linked via transaction timestamps.

#### Write Conflict Detection
- When concurrent transactions attempt to modify the identical Primary Key key-value pair, WiredTiger detects conflicting uncommitted transaction timestamps at the cursor write level.
- Conflicting writes immediately return `WT_ROLLBACK`.
- LionSQL intercepts `WT_ROLLBACK` and translates it into standard `storage.ErrWriteConflict` for uniform error handling across protocols.

---

## 3. VDBE Virtual Machine Transaction Coupling

In enterprise benchmarks (TPC-C, TPC-H) and multi-step T-SQL stored procedures, transactions frequently comprise sequences of multiple queries and updates:
- Reading customer balance.
- Incrementing district order sequence.
- Inserting order and order-line items.
- Decrementing stock quantities.

### 3.1. The Single Transaction Handle Invariant
LionSQL strictly enforces **Atomic ACID Transaction Modeling** across the VDBE bytecode virtual machine (GEMINI.md Section 7):

```go
// Correct: Single atomic transaction boundary
sess.BeginTx()
tx := sess.CurrentTx()

err := exec.RunInTx(prog1, params1, tx, nil)
if err != nil {
    tx.Rollback()
    return err
}

err = exec.RunInTx(prog2, params2, tx, nil)
if err != nil {
    tx.Rollback()
    return err
}

return tx.Commit()
```

- Splitting a single logical business transaction across multiple autocommit statements or separate ambient transactions is **strictly prohibited**.
- All bytecode subroutines within a stored procedure or benchmark run share the explicit transaction handle `tx`.
- Any error, validation abort (`ErrWriteConflict`), or constraint violation triggers an immediate `tx.Rollback()`.

---

## 4. Benchmark Concurrency & Retry Architecture

Under extreme concurrency (e.g. TPC-C with 100 workers and 10 warehouses running at 40,000+ TPS), write-write conflicts on contested rows (e.g., district order counters `d_next_o_id`) are a natural mathematical property of ACID serialization.

LionSQL benchmarks incorporate an explicit **Exponential Backoff Retry Strategy**:

```go
for attempt := 0; attempt <= maxRetries; attempt++ {
    tx, err := engine.BeginTx()
    if err != nil {
        continue
    }

    err = executeBusinessLogic(tx)
    if err == nil {
        err = tx.Commit()
        if err == nil {
            break // Successfully committed
        }
    }

    tx.Rollback()
    if errors.Is(err, storage.ErrWriteConflict) && attempt < maxRetries {
        // Exponential jittered backoff to alleviate hot-spot contention
        time.Sleep(time.Duration(1<<attempt) * time.Millisecond)
        continue
    }
    return err
}
```

- **In-Memory OCC Benchmark Results**:
  - Storage Kernel harness (`cmd/tpcc_kernel`, 10 Warehouses, 100 Workers): Delivers **1,066,827 tpmC** and **39,748 TPS** (97.7% New-Order completion, 1.88% overall abort rate).
  - T-SQL VDBE Runtime harness (`cmd/tpcc_runtime`, 16 Warehouses, 32 Workers): Delivers **759,102 tpmC** and **28,110 TPS** on precompiled T-SQL VDBE bytecode VM with 0.07% serialization abort rate.
  - Banking Transfer SP (`cmd/bench`): Delivers **218,282 TPS** (P50: 23 µs).
- **WiredTiger MVCC Benchmark Results**:
  - Storage Kernel harness (`cmd/tpcc_kernel`, 10 Warehouses, 100 Workers): Delivers **1,184,785 tpmC** and **41,905 TPS** with Invariant 10 zero-lock B+Tree scaling.
  - T-SQL VDBE Runtime harness (`cmd/tpcc_runtime`, 16 Warehouses, 32 Workers): Delivers **863,101 tpmC** and **31,794 TPS** with sub-millisecond median latency.
  - Banking Transfer SP (`cmd/bench`): Delivers **141,472 TPS** (P50: 24 µs).

---

## 5. Architectural Non-Goals & Spec-Out Decisions

To preserve sub-microsecond latency and single-node hardware saturation, the following models are explicitly **Spec Out** from LionSQL's core database kernel (GEMINI.md Section 4):

1. **`READ UNCOMMITTED` Dirty Reads**: Compromising data integrity to avoid lock contention is unnecessary because LionSQL's snapshot reads are already completely non-blocking.
2. **Heavy Distributed Two-Phase Locking (2PL)**: Shared read locking tables and distributed lock managers introduce catastrophic false abort penalties, deadlock graph detection overhead, and severe CPU thrashing.
3. **Serializable Snapshot Isolation (SSI) Graph Tracking**: Tracking read-write anti-dependency graphs (`siREAD` locks) in memory incurs up to 40% throughput penalty. High-concurrency OLTP applications achieve equivalent consistency via explicit OCC version checking or optimistic fail-fast retries.
