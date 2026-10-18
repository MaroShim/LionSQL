# Snapshot Isolation Across Storage Engines & DuckDB Analytical Integration

This technical whitepaper details the internal mechanics of Snapshot Isolation across LionSQL's dual storage engines (**WiredTiger MVCC** and **In-Memory OCC**), the fundamental distinctions in snapshot time-points between both engines, the architectural rationale behind Zero-Vacuum persistence, and the end-to-end consistency guarantees for **initial data synchronization and real-time CDC integration with DuckDB**.

---

## 1. Architectural Summary & Core Tenets

```
[ LionSQL High-Concurrency OLTP Core ]
  ├── 1. WiredTiger MVCC : Disk-based Lock-free B+Tree (Transaction-Level Frozen Snapshot)
  └── 2. In-Memory OCC   : Pure RAM Atomic Workspaces (Statement-Level Snapshot)
            │
            │ (Non-blocking Reads: Reads Never Block Writes)
            ▼
[ Initial Snapshot Dump ] ────(Based on Watermark LSN)──┐
                                                        │
                                                        ▼
[ Real-Time Async CDC Stream ] ──(HTTP NDJSON/SSE)────> [ DuckDB OLAP Analytical Engine ]
                                                        - Single-writer lock isolation
                                                        - INSERT OR REPLACE (Eventual Convergence)
```

1. **Readers Never Block Writers, Writers Never Block Readers**:
   - Even when DuckDB executes a prolonged full-table scan to dump tens of millions of records during initial synchronization, LionSQL guarantees **0% lock contention** and **0% latency overhead** on live OLTP transactions (orders, payments, inventory updates).
2. **Private Workspace Isolation (Zero Dirty Reads)**:
   - Uncommitted transaction modifications reside exclusively in session-private memory workspaces. DuckDB and concurrent reader sessions can never observe dirty or uncommitted tuples.
3. **Deterministic Eventual Convergence**:
   - Regardless of whether the underlying storage engine uses transaction-level frozen snapshots (WiredTiger) or statement-level snapshots (In-Memory OCC), combining an initial LSN watermark with real-time CDC streaming and idempotent (`INSERT OR REPLACE`) ingestion guarantees that **the final analytical state in DuckDB converges to 100% exact parity with the OLTP primary**.

---

## 2. Internal Mechanics of Snapshot Isolation

Traditional relational databases operating under two-phase locking (2PL) acquire shared read locks (S-Locks), stalling concurrent write transactions (X-Locks). LionSQL eliminates read-write lock contention entirely through **Multi-Version Concurrency Control (MVCC)** and **Optimistic Concurrency Control (OCC)**, maintaining versions and transaction timestamps without in-place overwrites.

### 2.1 WiredTiger MVCC: Transaction-Level Frozen Snapshots (Strict Snapshot Isolation)

WiredTiger maintains an in-memory linked list of updates (**`WT_UPDATE` chain**) for every distinct key within its B+Tree page buffers rather than storing a single physical value.

```text
[ Key: Account 101 ]
       │
       ▼
 ┌──────────────────────────┐
 │ WT_UPDATE Node (Latest)  │ ──> Commit Timestamp: 10:10 (Tx 300)
 │ Value: $2,000            │
 └─────────────┬────────────┘
               │ (Previous Version Link)
               ▼
 ┌──────────────────────────┐
 │ WT_UPDATE Node           │ ──> Commit Timestamp: 10:05 (Tx 200)
 │ Value: $1,500            │  ◀── [DuckDB Dump Tx Started: 10:06]
 └─────────────┬────────────┘      (DuckDB points to and reads this node)
               │ (Previous Version Link)
               ▼
 ┌──────────────────────────┐
 │ WT_UPDATE Node (Oldest)  │ ──> Commit Timestamp: 10:00 (Tx 100)
 │ Value: $1,000            │
 └──────────────────────────┘
```

- **Commit Phase Mechanics**:
  - Committed writes do not overwrite existing memory blocks. A new `WT_UPDATE` node is allocated and atomically linked to the head of the chain via atomic Compare-And-Swap (CAS).
- **Read Phase Mechanics**:
  - If a DuckDB extraction transaction initiates at 10:06, any subsequent transactions committing newer versions (e.g., at 10:10) are invisible to it. The cursor traverses the `WT_UPDATE` chain and reads the latest version committed prior to its start time (**Version 2: $1,500**).
  - Even if the analytical dump takes an hour to complete, DuckDB observes a perfectly frozen point-in-time view of the database as of 10:06—operating as a true "time-machine snapshot."

---

### 2.2 Why WiredTiger Eliminates the Need for VACUUM

In PostgreSQL, row updates physically append new tuples to the disk table heap, accumulating dead tuples that necessitate heavy background `VACUUM` processes to reclaim disk space. WiredTiger's architecture fundamentally eliminates the need for vacuuming:

| Comparison Metric | PostgreSQL MVCC | WiredTiger MVCC (LionSQL) |
| :--- | :--- | :--- |
| **Historical Version Storage** | **Disk heap files** (Physical dead-tuple bloat) | **In-memory linked lists** (`WT_UPDATE` chains in RAM) |
| **Version Pruning Trigger** | Background **`VACUUM`** worker scans | **On-the-fly reconciliation** during disk page flush |
| **Disk Space Reclamation** | Free Space Map (FSM) scanning | **Block Manager Free Extent List immediate reuse** |
| **Operational Impact** | Autovacuum I/O spikes, table bloat, TXID wraparound | **Zero VACUUM overhead, 0% I/O storms, 0% lock contention** |

1. **On-The-Fly Reconciliation During Disk Flush**:
   - When dirty B+Tree pages are written from memory cache to disk (`.wt`), WiredTiger evaluates the oldest active reader transaction across the engine. Any historical versions older than the oldest reader are **pruned and discarded directly in memory during the reconciliation phase**.
   - As a result, the disk image contains only the latest valid tuples in compressed form. No dead tuples are written to disk, completely eliminating disk-level garbage collection.
2. **Hazard Pointer Memory Safety**:
   - Historical version nodes in memory currently accessed by long-running queries (such as DuckDB table scans) are pinned by hazard pointers. Once the reader terminates, the memory is immediately released back to the allocator via standard C runtime memory deallocation (`free`).

---

### 2.3 In-Memory OCC: Statement-Level Snapshots (Read Committed Snapshot)

The In-Memory OCC engine (`pkg/storage/memengine`) operates entirely in physical RAM with no disk I/O. To guarantee high throughput (20,000+ TPS) while preventing out-of-memory (OOM) conditions, it uses statement-level snapshotting rather than long-lived multi-version chains.

```text
[ Live Write Session A ]                  [ Main In-Memory Table (Concurrent SkipList) ]
  Private Workspace (tx.local)                   │
  ┌──────────────────────┐                       │
  │ Key 101: Bal $2,000  │                       │ (Uncommitted writes never leak to main)
  └──────────────────────┘                       │
             │                                   ▼
             │ (Atomic CAS validation on Commit) Key 101: Bal $1,500 (Version 5)
             └───────────────────────────────>   Key 102: Bal $3,000 (Version 2)
                                                 Key 103: Bal $5,000 (Version 8)
                                                         ▲
                                                         │ (Traverses at native RAM speed without locks)
                                            [ DuckDB Initial Dump Cursor ]
```

1. **Private Workspace Isolation**:
   - Every `INSERT`, `UPDATE`, and `DELETE` is staged exclusively within a transaction-private map (`tx.local`).
   - The main SkipList index remains untouched until commit, guaranteeing 100% immunity against dirty reads.
2. **Statement-Level Snapshot (Zero Memory Bloat)**:
   - In a pure RAM engine, maintaining multi-version chains across long-running queries would rapidly exhaust host memory.
   - Instead, the memory engine maintains a single atomic value and a monotonically increasing 64-bit version (`Version uint64`) per record.
   - When DuckDB initiates a table scan, concurrent transactions committing during the scan become visible for records located ahead of the cursor (consistent with ANSI Read Committed semantics).

---

## 3. Storage Engine Snapshot Time-Point Invariants

| Characteristic | WiredTiger MVCC | In-Memory OCC (`memengine`) |
| :--- | :--- | :--- |
| **Snapshot Baseline** | **Transaction Start Time** | **Statement Execution / Scan Time** |
| **Snapshot Lifespan** | Persists until transaction commit or rollback | Bound to the active query / cursor iteration |
| **Isolation Level** | **Strict Snapshot Isolation (Repeatable Read)** | **Read Committed Snapshot** |
| **Behavioral Scenario**<br>(Tx starts at 10:00;<br>Concurrent commit at 10:05) | Query at 10:10 returns **data as of 10:00**<br>(Frozen time-machine view) | Query at 10:10 returns **data committed at 10:05**<br>(Fresh committed state) |
| **Primary Design Goal** | Full point-in-time consistency with disk backing | **Zero memory bloat, maximum throughput (20,000+ TPS)** |

---

## 4. DuckDB Initial Synchronization & CDC Handover Protocol

To bridge both storage engines with DuckDB without operational inconsistencies, LionSQL provides a standardized **3-step cutover handover protocol**.

```
[ LionSQL Primary ] ──────────────────────────────────────── (High-concurrency OLTP commits)
     │
     ├─ [Step 1] Acquire Watermark LSN (e.g., LSN = 5000)
     │
     ├─ [Step 2] Non-Blocking Initial Dump (SELECT * FROM orders ➔ DuckDB Bulk Insert)
     │           * Live commits increment LSN (5000 ➔ 5500) safely without blocking
     │
     └─ [Step 3] Connect Real-Time CDC Stream (GET /api/cdc/stream?from_lsn=5000)
                 * DuckDB executes: INSERT OR REPLACE INTO orders ... (Upsert)
                 * All intermediate mutations converge to identical final state!
```

### Handover and Convergence Protocol

1. **Step 1: Watermark LSN Acquisition**:
   - Prior to initiating the snapshot export, the ingestion worker inspects LionSQL's current committed LSN (e.g., `LSN = 5000`).
2. **Step 2: Non-Blocking Initial Dump**:
   - **Under WiredTiger**: The worker dumps an exact frozen snapshot corresponding to LSN 5000.
   - **Under In-Memory OCC**: Mutations committed during the scan (LSN 5001 to 5500) may be partially incorporated into later sections of the dump stream.
3. **Step 3: CDC Stream Subscription & Idempotent Convergence**:
   - Upon completing the initial dump, the worker opens the CDC stream using the query parameter `from_lsn=5000`.
   - All transactions committed during the dump (LSN 5001 through 5500) are replayed in order.
   - The DuckDB ingestion pipeline applies records using primary key upserts:
     ```sql
     INSERT OR REPLACE INTO orders (id, customer_id, amount, status, updated_at)
     VALUES (?, ?, ?, ?, ?);
     ```
   - **Resulting Invariant**:
     - Whether a record in DuckDB originated from an earlier snapshot version (WiredTiger) or an in-flight committed version (In-Memory OCC), the incoming CDC stream overwrites identical primary keys with the latest committed state.
     - **The analytical state in DuckDB is guaranteed to converge to 100% bit-for-bit parity with the OLTP source**.

---

## 5. Operational Recommendations

1. **Zero Impact on OLTP Workloads**:
   - Both initial bulk dumps and real-time CDC consumption operate without acquiring table-level or row-level locks on LionSQL's OLTP path.
2. **Engine Selection Guidelines**:
   - **WiredTiger MVCC**: Recommended for standard enterprise deployments requiring strict transaction-level snapshot isolation (Repeatable Read), persistent disk durability, and zero-maintenance operations (Zero Vacuum).
   - **In-Memory OCC**: Recommended for extreme sub-millisecond in-memory caching and real-time streaming where statement-level snapshots are acceptable and raw throughput (>20,000 TPS) is the primary requirement.
3. **DuckDB Ingestion Best Practices**:
   - Always define explicit **Primary Keys** on DuckDB destination tables.
   - Apply incoming CDC event batches using **`INSERT OR REPLACE`** (or `ON CONFLICT DO UPDATE`) to maintain idempotency across network reconnections and backfill replays.
