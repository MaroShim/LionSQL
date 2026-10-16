# LionSQL Internals: Storage Engine Mechanics (WiredTiger MVCC & Pure-Go OCC)

This document details the low-level data structures, concurrency control mechanisms, index layouts, and crash persistence strategies implemented in LionSQL's storage subsystem (`pkg/storage/`).

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

WiredTiger is the default production disk storage engine for LionSQL, integrated via Cgo to MongoDB's libwiredtiger.

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

### 2.4. Journal Fsync & Group Commit Architecture
WiredTiger durability is controlled via `SetFsyncPolicy()` and `FsyncPolicy()`:
- **`FsyncEverySec` (Default)**: A dedicated background goroutine (`startPeriodicSync`) runs on a 1-second ticker using a private session to call `session->log_flush(session, "sync=on")`. This guarantees a maximum data loss window of $\le 1$ second on sudden power loss while completely eliminating disk barrier latency from client commit hot paths.
- **`FsyncAlways`**: Enforces strict zero-loss durability using a **Leader/Follower Group Commit** mechanism. Concurrent commits serialize on `syncMu`; the leader thread issues `session->log_flush(session, "sync=on")` covering all accumulated sequence writes, and follower threads wake up immediately once their target write sequence is synced without issuing redundant physical disk barriers.
- **`FsyncNone`**: Journal entries are written to OS cache without explicit `log_flush("sync=on")`.

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
Point lookups, primary key equality tests, and SkipList index searches account for the highest frequency operations in OLTP workloads. LionSQL optimizes `storage.Value`:
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
For testing, embedded caches, and ephemeral compute pods, LionSQL supports pure in-memory operation:
- Invoked via `--ephemeral` flag or `ephemeral: true` configuration.
- Completely suppresses WAL/AOF log disk writes, background fsyncs, and table snapshot creation.
- Inspected via `DBCC PERSISTENCE` and `DBCC USEROPTIONS`.

---

## 5. Physical Storage Directory Layout, Metadata & Secondary Index Persistence

LionSQL organizes its physical data directory (`--data-dir`) cleanly, decoupling table data, metadata catalogs, query optimizer statistics, and secondary indexes.

### 5.1. Data Directory Physical File Layout

Depending on the configured storage backend (`-engine=wiredtiger` or `-engine=memory`), the data directory contains the following file structures:

```
<data-dir>/
├── catalog.json              # Schema & metadata catalog (tables, columns, indexes, constraints)
├── cbo_stats.json            # Cost-Based Optimizer statistics (histograms, NDV, row counts)
│
├── [WiredTiger Storage Engine: -engine=wiredtiger]
│   ├── WiredTiger            # Engine version and metadata lock file
│   ├── WiredTiger.basecfg    # Base WiredTiger configuration parameters
│   ├── WiredTiger.lock       # Multi-process exclusion lock
│   ├── WiredTiger.wt         # System metadata B+Tree (catalog of all physical .wt files)
│   ├── WiredTigerLAS.wt      # Lookaside table for long-running MVCC snapshot rollback
│   ├── WiredTiger.turtle     # Root bootstrap file locating WiredTiger.wt
│   ├── journal/              # Write-Ahead Log directory (WiredTigerLog.XXXXXXXXXX)
│   │   ├── WiredTigerLog.0000000001
│   │   └── WiredTigerPreAlloc.0000000001
│   ├── <table_name>.wt       # Per-table B+Tree data file (e.g., users.wt, orders.wt)
│   └── index:<table_name>:<index_name>.wt  # Secondary index B+Tree files
│
└── [In-Memory OCC Engine: -engine=memory]
    ├── tables.mpk            # Atomic binary snapshot of all TableData, rows, and IndexData
    ├── tables.mpk.tmp        # Temporary staging file for atomic snapshot rotation (RewriteAOF)
    └── wal.aof               # Append-Only File (continuous binary transaction WAL log)
```

---

### 5.2. Metadata Persistence Architecture (`catalog.json`)

Table definitions, column structures, constraints, partition boundaries, and secondary index definitions are not scattered across individual data files; they are centralized in `catalog.json` located at the root of the data directory.

#### Schema Representation
`catalog.json` persists a JSON array of `storage.TableSchema` objects:

```json
[
  {
    "name": "orders",
    "columns": [
      {
        "name": "id",
        "type": 1,
        "nullable": false,
        "primary_key": true,
        "auto_increment": true
      },
      {
        "name": "customer_id",
        "type": 1,
        "nullable": false
      },
      {
        "name": "payload",
        "type": 8,
        "nullable": true
      }
    ],
    "indexes": [
      {
        "name": "idx_orders_customer",
        "table_name": "orders",
        "columns": ["customer_id"],
        "unique": false
      }
    ],
    "foreign_keys": [],
    "check_constraints": [],
    "partition_scheme": null
  }
]
```

#### Lifecycle & Durability Guarantees
1. **Startup (`loadCatalog`)**:
   - When the engine opens (`Engine.Open(dir)`), it reads and unmarshals `catalog.json` into an in-memory map (`e.tables[name]`).
   - Query execution and VDBE compilation look up table schemas from this memory cache in $O(1)$ time with zero disk I/O.
2. **DDL Execution (`saveCatalog`)**:
   - DDL statements (`CREATE TABLE`, `DROP TABLE`, `ALTER TABLE`, `CREATE INDEX`, `DROP INDEX`, `TRUNCATE TABLE`) acquire the engine catalog write lock (`e.mu.Lock()`).
   - The updated catalog is serialized with indentation and written to disk (`os.WriteFile`) with non-executable permissions (`0644`).
   - For replication and CDC streams, a corresponding DDL WAL operation (`OpCreateTable`, `OpDropTable`, `OpCreateIndex`, `OpDropIndex`) is recorded.

---

### 5.3. Cost-Based Optimizer Statistics Persistence (`cbo_stats.json`)

Statistics gathered via `ANALYZE TABLE <name>` are separated from schema definitions and stored in `cbo_stats.json`:
- **Contents**: Cardinality estimates, page footprint, column null counts, Number of Distinct Values (`NDV`), minimum/maximum value bounds, and 64-bucket equi-depth histograms (`planner.TableStats`).
- **Atomic Persistence**: Serialized through temporary staging files (`cbo_stats.json.tmp.<nanos>`) and committed using atomic file renames (`os.Rename`), ensuring zero corruption if power fails during `ANALYZE TABLE`.
- **Automatic Loading**: When `Catalog.SetPersistenceDir(dir)` is invoked on startup, existing statistics are loaded into memory and referenced by the Volcano Cost-Based Optimizer.

---

### 5.4. Secondary Index Storage & Persistence Mechanics

LionSQL provides secondary indexing across both storage engines, using engine-optimized persistence strategies:

#### 1. WiredTiger MVCC Disk Engine
- **Metadata Registration**: Index definitions (`storage.IndexSchema`) are stored in `catalog.json` within the table's schema.
- **Physical B+Tree Pages**:
  - Secondary indexes are mapped to individual WiredTiger URI endpoints: `index:<table_name>:<index_name>`.
  - Keys in secondary index B+Trees consist of the indexed column values, pointing to the corresponding primary key string.
  - WiredTiger manages secondary index pages, hazard pointers, dirty page eviction, and MVCC visibility automatically within `<table_name>.wt` or dedicated index files.
- **Crash Recovery**: Checkpoints and journal logs (`journal/WiredTigerLog.*`) guarantee consistent recovery of both table rows and secondary index B+Trees to the same point-in-time.

#### 2. In-Memory OCC Engine (`memengine`)
- **In-Memory Representation (SkipList)**:
  - Secondary indexes are maintained in memory as concurrent lock-free SkipLists (`*SkipList`).
  - Supports $O(\log N)$ search, forward/reverse range iterations (`ScanRange`, `ScanRangeEntries`), and unique constraint checks.
- **Snapshot Persistence (`tables.mpk`)**:
  - During disk snapshotting (`saveDiskLocked()`) or AOF log compaction (`RewriteAOF()`), `idx.SyncEntries()` exports the active SkipList nodes into a serializable slice of `IndexEntry`:
    ```go
    type IndexEntry struct {
        Key           storage.Value     `json:"key"`
        PKs           []string          `json:"pks"`
        IncludeValues [][]storage.Value `json:"include_values,omitempty"`
    }
    ```
  - The entries are packed into `TableData.Indexes` and written atomically to `tables.mpk` via MessagePack.
- **Incremental WAL Durability (`wal.aof`)**:
  - DDL operations (`OpCreateIndex`, `OpDropIndex`) and row mutations (`OpInsert`, `OpUpdate`, `OpDelete`) append binary WAL records to `wal.aof`.
  - When replaying the WAL during server startup, `OpCreateIndex` dynamically builds the SkipList by scanning current rows, while subsequent DML ops keep the index synchronized.

