# LionSQL Online Table Schema Evolution & Operational Guide (ADD, DROP & Copy-and-Switch)

> - **Document ID**: `GUIDE-SCHEMA-REORG-1`
> - **Version**: `v0.92.0`
> - **Last Updated**: 2026-11-12
> - **Target Audience**: Database Administrators (DBAs), Backend Systems Engineers, DevOps Engineers

---

## 1. Overview & Schema Evolution Philosophy

In enterprise production environments, database schemas evolve continuously alongside business requirements. However, altering table structures in an active production database carries severe risks if performed improperly, including exclusive table locks, query stalls, replication lag, and silent data corruption.

Built upon the **Single Canonical V2 Binary Tuple Codec (`0x02`)**, LionSQL delivers a predictable, deterministic, and fail-safe schema evolution mechanism:
1. **Adding Columns (`ADD COLUMN`)**: Executes instantaneously in **$O(1)$ time via catalog metadata updates alone**, with zero locks and zero disk rewrites.
2. **Dropping Columns (`DROP COLUMN`)**: Terminal (tail) columns are dropped instantaneously in $O(1)$; dangerous **runtime middle-column drops are safely rejected via fail-closed validation (`ErrDropColumnNotTail`)** to protect OLTP availability.
3. **Large-Scale Reorganizations**: Accomplished reliably via the zero-downtime, zero-restart **Copy-and-Switch** standard procedure.

---

## 2. Adding Columns (ADD COLUMN) — Instant DDL & Best Practices for DEFAULT

### 2.1. The Legacy RDBMS Dilemma: "Specifying DEFAULT Halts the Database"
In legacy RDBMS architectures (Oracle prior to 11g, MySQL prior to 8.0, PostgreSQL prior to 11):
- Running `ALTER TABLE tbl ADD col INT DEFAULT 0 NOT NULL;` forced the storage engine into an exhaustive **physical full-table rewrite**, reading and updating every single on-disk tuple to materialize the default value.
- On populated tables, this incurred prolonged exclusive table locks (X-Locks) lasting hours, stalling all active transactions. Consequently, DBAs adopted the defensive workaround of adding columns as nullable without defaults, subsequently backfilling values in piecemeal background batch scripts.

### 2.2. Modern Instant DDL Architecture (LionSQL V2 Tuples)
LionSQL (alongside modern standards like SQL Server 2012+, PostgreSQL 11+, and MySQL 8.0) completely eliminates this physical rewrite penalty through **Instant ADD COLUMN**:

```
[On-Disk Physical Tuple]    ───▶  [LionSQL V2 Tuple Decoder]  ───▶  [Application Layer]
(encodedCols = 3)                 "Schema column count N = 4"
                                  4th column padded dynamically
                                  from catalog DEFAULT 'ACTIVE'
                                  in-memory on-the-fly! ($O(1)$)
```

- **Zero Physical Disk I/O**: Existing on-disk records remain untouched. The engine simply appends the column metadata and its default expression (`col.Default`) to the catalog, completing in sub-millisecond time ($O(1)$).
- **On-The-Fly Memory Padding**: When the tuple decoder (`DecodeTupleToRowWithArena`) deserializes tuples written under earlier schema generations, it automatically pads missing tail columns with their metadata default expressions or `NULL` without allocating intermediate disk buffers.

### 2.3. Modern Best Practice: Why Specifying Constant DEFAULT Values is Strongly Recommended
Because physical table rewrites and lock stalls are entirely eliminated in LionSQL, specifying a deterministic `DEFAULT` value is now the enterprise gold standard for application stability:

1. **Elimination of NullPointerExceptions & Deserialization Panics**:
   - Adding bare nullable columns causes legacy application code (Go, Java, C#, Python) to encounter unexpected `null` values, triggering runtime crashes or deserialization failures.
   - Supplying a default (e.g. `DEFAULT 'ACTIVE'`) guarantees immediate backward compatibility across un-restarted application services.
2. **Prevention of SQL Three-Valued Logic (3VL) Anomalies**:
   - In standard SQL, comparisons against `NULL` yield `UNKNOWN`. Predicates such as `WHERE status <> 'CANCELLED'` unexpectedly discard `NULL` rows. Setting an explicit default ensures accurate business query semantics.
3. **Immediate `NOT NULL` Enforcement**:
   - Adding a `NOT NULL` column to a populated table requires a `DEFAULT` expression to maintain semantic and relational consistency without rejecting the DDL.

### 2.4. The Critical Exception: Non-Deterministic Defaults
- **Safe Constant Defaults ($O(1)$ Instant)**:
  - Fixed literals (`DEFAULT 0`, `DEFAULT 'ACTIVE'`, `DEFAULT '1970-01-01'`) are stored once in metadata and resolve in $O(1)$ time with zero overhead.
- **Dynamic Per-Row Expressions**:
  - Functions such as `DEFAULT NEWID()` or `DEFAULT UUIDV7()` that require generating distinct unique identifiers for each preexisting row cannot be satisfied by metadata pointer alone and require explicit row-by-row generation.

---

## 3. Dropping Columns (DROP COLUMN) — Tail O(1) & Middle Anti-Pattern

### 3.1. Terminal (Tail) Column Instant O(1) Deletion
Dropping the terminal column of a table never shifts prior column offsets. LionSQL's tuple decoders simply truncate or ignore trailing bytes beyond the active schema column count, enabling **instant $O(1)$ DDL via catalog metadata updates alone**.

```sql
-- Terminal (Tail) Column Drop: Instant O(1)
ALTER TABLE customers DROP COLUMN legacy_memo;
```

### 3.2. Operational Anti-Pattern of Dropping Middle Columns at Runtime
Executing `ALTER TABLE ... DROP COLUMN` on non-terminal (middle) columns in an active production database is a well-known **operational anti-pattern** fraught with significant production risks:

1. **Exclusive Table Locks & Write Stalls**:
   If an engine executes a physical full-table rewrite, tables with millions of rows suffer prolonged exclusive locks (X-Locks), stalling concurrent transactions and violating OLTP availability SLAs.
2. **Replication Lag & Stream Saturation**:
   Rewriting entire tables inundates WAL/AOF replication streams, causing catastrophic replication lag on read replicas.
3. **Application & ORM Serialization Mismatches**:
   Shifting column positions or removing fields before applications are fully redeployed introduces runtime deserialization panics and data corruption.

### 3.3. Fail-Closed Engine Invariant & Permanent Spec-Out (`PERF-OCC-INSTANT-DROP-COL-1`)
LionSQL thoroughly evaluated alternative engine implementations and permanently rejected (Spec-Out) non-terminal column drops:

- **Alternative A (Physical Full Table Rewrite)**: Disqualified due to multi-second runtime stalls and exclusive lock contention.
- **Alternative B (Schema Virtual Tombstone Masking)**: Maintaining complex indirection maps and deletion tombstones bloats kernel complexity, makes edge-case verification extremely fragile, and **permanently degrades LionSQL's core 0.5ns fixed-offset tuple decoding hot path**.

Consequently, LionSQL enforces a strict fail-closed policy (`ErrDropColumnNotTail`) when dropping middle columns on populated tables, prescribing the enterprise-standard **Copy-and-Switch** pattern as the canonical solution.

---

## 4. Concurrency Mechanics (OCC / MVCC)

When executing the Copy-and-Switch pattern, LionSQL's dual storage engines provide deterministic, lock-free operational guarantees.

### 4.1. Copy Phase (`CREATE TABLE ... AS SELECT`): Zero Write-Write Conflict
- **In-Memory OCC Engine**:
  Optimistic Concurrency Control validation (`ErrWriteConflict`) triggers exclusively when **concurrent transactions attempt to mutate the same row**. During the copy phase, the migration session performs a pure read on the source table (`old`) and sequential inserts into the target table (`new`). Even with thousands of concurrent write transactions on `old`, write-write conflicts remain exactly zero, guaranteeing zero aborted copy transactions.
- **WiredTiger MVCC Engine**:
  Governed by Invariant 3 ("Reads never block writes, writes never block reads"), the copy session operates against an isolated MVCC snapshot. It does not acquire shared table locks or introduce deadlock conditions with concurrent OLTP workloads.

### 4.2. Switch Phase (`ALTER TABLE ... RENAME`): Zero-Downtime Atomic Metadata Swap
- **Zero Server Restart Required**:
  Renaming tables does not move physical disk payloads. It is an $O(1)$ catalog metadata pointer swap.
- **Microsecond Atomic Cutover**:
  Executing table renames inside an atomic transaction block completes in single-digit microseconds ($\mu$s), seamlessly rerouting client connections to the newly structured table without dropping active TCP sessions.

---

## 5. Practical Operational Playbooks

### 5.1. Standard Maintenance Window Playbook (Recommended)
The cleanest and most reliable procedure for enterprise environments.

#### Step 1: Pre-Migration Inspection
Verify schemas, constraints, and secondary index definitions:
```sql
-- Inspect source table definition
DESC customers;

-- List existing secondary indexes
SELECT index_name, column_name FROM sys.indexes WHERE table_name = 'customers';
```

#### Step 2: Create New Table & Copy Filtered Projections
Selectively project desired columns while omitting deprecated middle columns (`legacy_notes`):
```sql
-- Create target table with desired column projection
CREATE TABLE customers_new AS 
SELECT 
    customer_id, 
    name, 
    email, 
    created_at 
FROM customers;
```

#### Step 3: Rebuild Secondary Indexes
Create necessary secondary indexes on the target table:
```sql
-- Recreate secondary indexes
CREATE INDEX idx_customers_email ON customers_new (email);
```

#### Step 4: Atomic Metadata Cutover (Switching)
Execute atomic table swaps inside a transaction boundary:
```sql
BEGIN TRANSACTION;
    -- Move original table to backup
    ALTER TABLE customers RENAME TO customers_backup_20261112;

    -- Promote new table to active service
    ALTER TABLE customers_new RENAME TO customers;
COMMIT;
```

#### Step 5: Verification & Safe Deprecation
Confirm queries route properly to the new table, then retire the backup after an agreed grace period:
```sql
-- Verify active table
SELECT COUNT(*) FROM customers;

-- Drop backup table post-verification
DROP TABLE customers_backup_20261112;
```

---

### 5.2. Large Table Chunked Copy Strategy
For large-scale tables containing tens of millions of rows, avoid large transient memory allocations by copying in primary key ranges:

```sql
-- 1. Create target table schema
CREATE TABLE orders_new (
    order_id INT PRIMARY KEY,
    customer_id INT,
    total_amount DECIMAL(18,2),
    created_at DATETIME
);

-- 2. Copy in bounded primary key ranges (e.g. 100,000 rows per batch)
INSERT INTO orders_new SELECT order_id, customer_id, total_amount, created_at 
FROM orders WHERE order_id >= 1 AND order_id < 100000;

INSERT INTO orders_new SELECT order_id, customer_id, total_amount, created_at 
FROM orders WHERE order_id >= 100000 AND order_id < 200000;

-- 3. Rebuild indexes and perform atomic switch
BEGIN TRANSACTION;
    ALTER TABLE orders RENAME TO orders_old;
    ALTER TABLE orders_new RENAME TO orders;
COMMIT;
```

---

### 5.3. 24/7 High-Availability Delta Catch-Up Strategy
For systems requiring zero-interruption OLTP writes throughout the copy duration:

```
[Client Traffic] ───────────────┐
                                │
                                ▼
                    ┌───────────────────────────┐
                    │     Source Table (old)    │
                    └─────────────┬─────────────┘
                                  │
         ┌────────────────────────┴────────────────────────┐
         │ 1. Initial Snapshot Bulk Copy                   │ 2. Concurrent Delta Inflow
         ▼                                                 ▼
┌───────────────────────────┐                     ┌───────────────────────────┐
│     Target Table (new)    │ ◄── [Catch-up Sync] ─│      CDC / Outbox Queue   │
└───────────────────────────┘                     └───────────────────────────┘
         ▲
         │ 3. Atomic RENAME Cutover when Delta converges to 0 ($O(1)$)
```

1. **Record Snapshot Boundary**: Capture the starting LSN or timestamp $T_0$.
2. **Execute Snapshot Copy**: Bulk insert data as of $T_0$ into the new table.
3. **Replay Inflow Deltas**: Apply all `INSERT`, `UPDATE`, and `DELETE` events occurring after $T_0$.
4. **Final Cutover**: Once the sync lag reaches near zero, execute `ALTER TABLE ... RENAME` within an atomic transaction.

---

## 6. Operational Checklist & FAQ

### 6.1. Operational Checklist
| Phase | Checkpoint | Verification |
| :--- | :--- | :--- |
| **Pre-Migration** | Secondary Indexes | Are all indexes duplicated on the new table? |
| | Disk Capacity | Is there at least 1.5x table capacity available for transient storage? |
| | Object Dependencies | Are there any views or stored procedures referencing omitted columns? |
| **Copy Phase** | Resource Governors | Are CPU, memory, and I/O metrics within nominal bounds? |
| | Query Latency | Are existing OLTP queries maintaining sub-millisecond latencies? |
| **Switch Phase** | Transactional Wrapping | Are both `ALTER TABLE ... RENAME` statements wrapped in a single transaction? |
| | Query Routing | Does the active application workload route to the new table structure? |
| **Post-Migration** | Backup Retention | Has the original table been preserved under a backup name for fast rollback? |

### 6.2. Frequently Asked Questions (FAQ)
#### Q1. Will concurrent writes to the source table abort the copy session with an OCC conflict?
**No.** The copy transaction reads from the source table and writes to the new table. Under LionSQL's OCC and MVCC models, reads and writes never block each other, and write-write conflicts only occur on concurrent updates to the same row.

#### Q2. Does table switching require a database restart?
**No.** Renaming tables is an instantaneous $O(1)$ catalog pointer update. It completes in microseconds without closing active connections or restarting the database process.

#### Q3. Why can terminal (tail) columns be dropped instantly without copying?
Tail columns reside at the physical end of tuple payloads. The decoder safely ignores trailing bytes without requiring column offset recalculations, enabling $O(1)$ instant DDL via catalog metadata updates alone.

---

## 7. Summary

Schema evolution in high-concurrency production databases is an operational engineering discipline balancing continuous uptime with rigorous data integrity:
1. **Adding Columns (`ADD COLUMN`)**: Fully instantaneous with zero locks and zero disk rewrites via **Instant $O(1)$ DDL**; specifying constant **`DEFAULT` values is the recommended modern best practice** for application safety.
2. **Dropping Middle Columns (`DROP COLUMN`)**: Dangerous runtime middle-column deletions are **safely rejected via fail-closed validation (`ErrDropColumnNotTail`)**, with the canonical **Copy-and-Switch** pattern providing zero-downtime, zero-restart reorganizations.
