# SQLion Internals: Storage Engine Mechanics (WiredTiger MVCC & Pure-Go OCC)

This document details the low-level data structures, concurrency control mechanisms, index layouts, and crash persistence strategies implemented in SQLion's storage subsystem (`pkg/storage/`).

---

## 1. The Unified `storage.Engine` Contract

Both storage backends adhere strictly to the `storage.Engine` and `storage.Transaction` interface abstractions:

```go
type Engine interface {
    Open(dir string) error
    Close() error
    NewSession() (Session, error)
    CreateTable(schema *TableSchema) error
    DropTable(tableName string) error
    GetTable(tableName string) (*TableSchema, error)
    ListTables() ([]*TableSchema, error)
    CreateIndex(index *IndexSchema) error
    DropIndex(tableName, indexName string) error
}

type Transaction interface {
    Insert(tableName string, record Record) error
    Update(tableName string, pk Value, record Record) error
    Delete(tableName string, pk Value) error
    Get(tableName string, pk Value) (Record, error)
    Scan(tableName string) (Cursor, error)
    IndexScan(tableName string, indexName string, low, high *Value) (Cursor, error)
    Commit() error
    Rollback() error
}
```

---

## 2. WiredTiger Storage Backend (`pkg/storage/wiredtiger/`)

WiredTiger is the default production disk storage engine for SQLion, integrated via Cgo to MongoDB's libwiredtiger.

```
                  +----------------------------------------------+
                  |         WiredTiger Engine (Cgo Bridge)       |
                  +----------------------------------------------+
                                         │
                    ┌────────────────────┼────────────────────┐
                    ▼                    ▼                    ▼
             [WT_CONNECTION]        [WT_SESSION]         [WT_CURSOR]
             Process-wide handle    Per-worker context   Key-Value iterator
                    │                    │                    │
                    └────────────────────┼────────────────────┘
                                         │
                                         ▼
                  +----------------------------------------------+
                  |     Lock-Free Concurrent B+Tree Storage      |
                  |  - Hazard pointer memory protection          |
                  |  - MVCC snapshot isolation                   |
                  |  - Checkpoint manager & write-ahead logging  |
                  +----------------------------------------------+
```

### 2.1. Cgo Bridge Architecture
- `Engine` maintains a global `*C.WT_CONNECTION` pointer.
- `WTSession` encapsulates a `*C.WT_SESSION` opened on-demand from the connection handle.
- Each transaction begins with `begin_transaction("isolation=snapshot")`, guaranteeing true Multi-Version Concurrency Control (MVCC) without table-level read locks.
- Table data is mapped to WiredTiger table URIs: `table:<table_name>`.

### 2.2. Row & Key Encoding
- **Primary Key**: Encoded as raw string format (`String()` representation of `storage.Value`).
- **Record Values**: Serialized to compact JSON strings or binary representations passed to `cursor.set_value()`.
- **Secondary Indexes**: Stored in auxiliary WiredTiger index tables (`index:<table_name>:<index_name>`) mapping indexed column values to primary keys.

### 2.3. Zero-Cgo Fallback Stub (`wt_stub.go`)
When compiled without `-tags wiredtiger`, `wt_stub.go` provides stub methods returning clear diagnostic errors prompting the user to run with `-engine=memory` or install `libwiredtiger-dev`.

---

## 3. Pure-Go In-Memory Engine (`pkg/storage/memengine/`)

The pure-Go in-memory engine provides zero-Cgo portability, instant startup, and predictable performance across all platforms.

```
                       [ memengine.Engine (In-Memory) ]
                                      │
              ┌───────────────────────┴───────────────────────┐
              │                                               │
              ▼                                               ▼
       [ TableData Map ]                             [ Hybrid Persistence ]
  - Rows: map[pk]Record                         - Base Snapshot: tables.mpk
  - Versions: map[pk]int64 (OCC)                - Write-Ahead Log: wal.aof
  - Secondary Indexes: map[name]*IndexData      - Auto Compaction: RewriteAOF
```

### 3.1. Optimistic Concurrency Control (OCC) Architecture

Rather than maintaining heavy lock queues, `memengine` uses version-validated OCC:

1. **Read Phase**:
   - When a transaction reads a row via `Get()` or `Scan()`, it copies the record and records its base version:
     $$\text{baseVer} = \text{td.Versions}[\text{pk}]$$
   - All mutations (`Insert`, `Update`, `Delete`) are staged in private transaction-local buffers (`tx.local`, `tx.deleted`).
2. **Validation & Commit Phase**:
   - During `tx.Commit()`, the engine acquires a brief write lock and verifies:
     $$\forall \text{pk} \in \text{modified}, \quad \text{td.Versions}[\text{pk}] == \text{tx.baseVers}[\text{pk}]$$
   - If another concurrent transaction modified any row in the write set ($\text{curVer} > \text{baseVer}$), the transaction aborts with:
     ```
     ErrSerializationFailure: serialization failure: concurrent update conflict
     ```
   - If validation succeeds:
     - Local changes are merged into `td.Rows`.
     - Row versions are incremented: `td.Versions[pk]++`.
     - Secondary indexes are updated incrementally.
     - A binary `WALRecord` is appended to `wal.aof`.

---

### 3.2. Secondary Index In-Memory Data Structure

Secondary indexes in `memengine` use sorted slices with binary search:

```go
type IndexEntry struct {
    Key storage.Value `json:"key" msgpack:"key"`
    PKs []string      `json:"pks" msgpack:"pks"`
}

type IndexData struct {
    Schema  storage.IndexSchema `json:"schema" msgpack:"schema"`
    Entries []IndexEntry        `json:"entries" msgpack:"entries"`
}
```

- **Insertion (`Insert`)**:
  - Uses `sort.Search()` ($O(\log N)$) to find the insertion point.
  - If `Schema.Unique` is true and an existing entry exists with a different PK, returns `ErrUniqueViolation`.
  - Appends to or splices into the `Entries` slice.
- **Deletion (`Delete`)**:
  - Removes the specific PK from `IndexEntry.PKs`. If `PKs` becomes empty, the entry is spliced out.
- **Range Scans (`ScanRange`)**:
  - Finds lower bound via `sort.Search(low)` and scans sequentially until `entry.Key > high`.

---

### 3.3. Hybrid Persistence & Log Compaction (`RewriteAOF`)

`memengine` combines baseline binary snapshots with an append-only transaction log:

```
[Server Startup]
       │
       ▼
1. Load Base Snapshot: data/tables.mpk (MessagePack decoded into e.tables)
       │
       ▼
2. Scan Append-Only Log: data/wal.aof (Binary framed WALRecord batches)
       │
       ▼
3. Replay Transactions with LSN > Snapshot LSN
       │
       ▼
[Engine Ready for Transactions]
```

#### Fsync Synchronization Policies
- **`FsyncEverySec` (Default)**: Background goroutine calls `file.Sync()` every 1 second. Limits data loss window to <= 1 second during power failure.
- **`FsyncAlways`**: Synchronous `file.Sync()` on every `tx.Commit()`. Guarantees strict zero-loss ACID durability.
- **`FsyncNone`**: Relies on OS kernel page cache flushing for maximum throughput.

#### Log Compaction Algorithm (`RewriteAOF`)
When the `wal.aof` file exceeds threshold (default 64MB):
1. Acquires engine lock: `e.mu.Lock()`.
2. Encodes all active tables into MessagePack binary bytes.
3. Stages bytes to atomic swap file `tables.mpk.tmp` with `Sync()`.
4. Atomically renames `tables.mpk.tmp` to `tables.mpk`.
5. Truncates `wal.aof` to zero length.
6. Releases engine lock.

---

## 4. Value Representation, Fast-Paths & Ephemeral Mode (v0.11.0)

### 4.1. `storage.Value` Integer Fast-Paths
Point lookups, primary key equality tests, and SkipList index searches account for the highest frequency operations in OLTP workloads. SQLion optimizes `storage.Value`:
- **`Value.Equal` Fast-Path**:
  ```go
  if v.Type == TypeInt && other.Type == TypeInt && !v.IsNull && !other.IsNull {
      return v.IVal == other.IVal
  }
  ```
  Bypasses null checks, type reflection, and fallback dispatch.
- **`Value.Compare` Fast-Path**:
  Directly compares `v.IVal` and `other.IVal` without decimal conversions or allocations.
- **`Value.String` Formatting**:
  Replaces `fmt.Sprintf` reflection on hot paths with `strconv.FormatInt(v.IVal, 10)` and `strconv.FormatBool(v.BVal)`.

### 4.2. In-Memory Ephemeral Engine Mode (`--ephemeral`)
For testing, embedded caches, and ephemeral compute pods, SQLion supports pure in-memory operation:
- Invoked via `--ephemeral` flag or `ephemeral: true` configuration.
- Completely suppresses WAL/AOF log disk writes, background fsyncs, and table snapshot creation.
- Inspected via `DBCC PERSISTENCE` and `DBCC USEROPTIONS`.
