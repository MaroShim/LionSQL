# SQLion Engineering Roadmap & Core Technical Milestones

This document charts the development milestones, verified implementation status, and future engineering challenges for the SQLion high-concurrency embedded database engine.
(For actionable engineering tasks and domain backlogs, refer to [TODO.ko.md](TODO.ko.md).)

---

## 1. Milestones & Release Progression Overview

SQLion follows a phased evolutionary roadmap from an autonomous single-node OLTP engine to an enterprise-grade, high-availability hybrid database system:

```
[ Phase 1: Core Foundation (v0.1.0 - v0.8.0) ] ────► COMPLETED
  - Dual Wire Protocols (PGWire :5433 + TDS :1433)
  - Dual Storage Engine (WiredTiger B+Tree + In-Memory OCC)
  - Dual Parser (Native Pratt + ANTLR4 Fallback)
  - VDBE Bytecode Virtual Machine & Volcano Relational Pipeline
  - CBO Cost-Based Optimizer, Histograms & Predicate Pushdown

[ Phase 2: SQL & Analytical Depth (v0.9.0 - v0.10.0) ] ──► COMPLETED
  - Window Functions & Frames (ROW_NUMBER, RANK, LEAD, LAG, OVER)
  - Advanced Aggregations (GROUP BY ROLLUP, CUBE, GROUPING)
  - Percentile / Quantile Analytics (PERCENTILE_CONT, T-Digest)
  - Log Operators (GREP / REGEXP_LIKE, AWK / SPLIT_PART)
  - Transaction Savepoints (SAVEPOINT, ROLLBACK TO SAVEPOINT)
  - View Subsystem (CREATE VIEW, Materialized / Aggregation Views)
  - Advanced DML (ON CONFLICT DO NOTHING / UPDATE)
  - SQL Prepared Statements (PREPARE, EXECUTE, DEALLOCATE)
  - In-Memory Ephemeral Engine Mode (--ephemeral, Zero-Disk)

[ Phase 3: Replication, Official Drivers & Zero-Allocation (v0.11.0 - v0.12.0) ] ──► COMPLETED
  - 1-Writer Multi-Reader WAL Streaming Replication & Failover Promotion
  - Microsoft SQL Server Official Client Driver Compatibility (go-mssqldb)
  - PostgreSQL Official Client Driver Compatibility (lib/pq)
  - CBO Table Statistics Disk Persistence (cbo_stats.json)
  - Go Native In-Process Scalar Function SPI (proc.RegisterScalarFunction)
  - VDBE OpRelQuery Physical PlanNode Caching & Zero-Allocation Streaming
  - Wire Protocol Response Packet Buffer Pooling (sync.Pool)
  - storage.Value Integer Fast-Path & Hot-Path Formatting
  - ANALYZE TABLE Multi-Core Parallel Statistics & Histograms (sync.WaitGroup)

[ Phase 4: Modern Ecosystem, GUI Tool Interop & VDBE Acceleration (v0.13.0 - v0.15.0) ] ──► COMPLETED
  - PostgreSQL Modern Driver Compatibility: pgx/v5 & Batch Pipelining (Flush)
  - TDS Protocol Async Attention Query Cancellation (0x06) & Real-Time Query Tracing (-dev)
  - Beekeeper Studio, DbGate & DBeaver Multi-Tool Catalog Interoperability
  - ANSI/MSSQL Pagination (OFFSET ... FETCH NEXT ... ROWS ONLY, SELECT TOP N)
  - IETF RFC 9562 Standard UUIDv7 (NEWID7, UUIDV7, GEN_RANDOM_UUID(7))
  - VDBE Local Variable Slot Indexing (O(1) Array Direct Access)
  - VDBE Integer Opcode Specialization & Dedicated Logical Operators (OpAddInt ~ OpGteInt, OpAnd, OpOr)
  - VDBE Fused Compare-and-Branch & Peephole Trampoline Jump Optimization
  - VDBE Dispatcher Modularization (Sub-Domain Execution Helpers & Centralized Catch-Stack)
  - TPC-C VDBE Peak 21,073 TPS / Overall 18,542 TPS (63.8% Native Parity)

[ Phase 5: Enterprise Governance, Security & Engine Decoupling (v0.16.0 - v0.19.0) ] ──► ACTIVE
  - [x] VDBE Instruction Struct 16-Byte Compaction (4 instructions per 64B L1 Cache Line)
  - [x] SQL Coordinator Package Separation (pkg/api -> pkg/engine)
  - [x] Storage WAL Group Commit & Fsync Acceleration (100x commit latency reduction)
  - [x] T-SQL & ANSI Full Syntax Coverage (APPLY, FULL/RIGHT JOIN, Derived Tables, EXISTS, INSERT SELECT)
  - [x] PlanCache Bounded LRU Cache & Memory Defense (Golden Rule 29)
  - [x] Granular SQL Object-Level RBAC (CREATE ROLE/USER, GRANT/REVOKE)
  - [x] CBO Boolean Tree & OR Selectivity Modeling (C-6)
  - [x] CBO Optimizer Hints Override (D-9)
  - [x] Sort-Merge Join Operator (B-3)
  - [x] Fast SQL Fingerprinting & Simple PK Query Bypass (P-5)
  - [x] TDS NBCROW (0xD2) Row Compression
  - [x] Binary Tuple Lazy Decoding & Covering Index Non-Key Columns (P-2)
  - [x] Volcano Chunk-at-a-Time Vectorized Batch Streaming (P-4)
  - [ ] Transparent Data Encryption (TDE: AES-256-GCM) & Immutable Audit Log
  - [ ] BaaS Expansion: Declarative Schema Migration CLI (sqlion migrate) & OAuth2 Social Login

[ Phase 6: High Availability Always On, Decoupled HTAP & Production Hardening (v1.0.0 LTS) ] ──► PLANNED
  - Always On Style Automatic Failover & Leader Election
  - Transactional Outbox & CDC Event Streaming (Kafka / RabbitMQ)
  - Shared Memory (POSIX shm / mmap) Apache Arrow IPC Snapshots (DuckDB Zero-Copy)
  - [Postponed] Pure Go Persistent Storage Engine Backend (Pebble / bbolt, CGO_ENABLED=0)
  - Dynamic Programming (DP / Selinger) Large Join Order Optimization (N > 4)
  - Extended Multi-Column Statistics, MCV & Optimizer Hints
  - T-SQL Specialized TDS Serialization (UNIQUEIDENTIFIER, DATETIME2, PLP LOB)
  - Multi-Language Client Certification (Python psycopg3/pymssql, Node.js pg/tedious, .NET SqlClient)
  - Storage to Wire Zero-Copy Direct Serialization (P-3)
  - Background MVCC Vacuuming & Undo Log Version Compaction
  - Wasm Runtime Sandbox UDF (wazero)
  - Local & S3-Compatible File Storage Module & Thumbnail Resizing
```

---

## 2. Verified Implementation Status (Current Verification)

### 2.1. Core Execution Engine
- [x] **VDBE Instruction Struct 16-Byte Compaction (`pkg/proc/bytecode.go`)**: Dense 16-byte fixed layout (`Op uint8, Flags uint8, Aux uint16, A int32, B int32, C int32`, zero padding) externalizing metadata to `Program.AuxData []any`, loading exactly 4 instructions per 64-byte L1 Cache Line (`unsafe.Sizeof == 16`).
- [x] **VDBE Bytecode Virtual Machine (`pkg/proc/runtime`)**: SQLite-style 3-address register virtual machine with `sync.Pool` register recycling, executing stored procedures, functions, triggers, cursors, and interactive queries.
- [x] **VDBE Local Variable Slot Indexing (`pkg/proc`, `pkg/proc/runtime`)**: Compile-time variable slot allocation (`Program.VarSlots`) and runtime $O(1)$ direct slice indexing (`VMFrame.slots[inst.B]`), eliminating string hashing and map lookups.
- [x] **VDBE Integer Opcode Specialization & Logical Operators (`pkg/proc`, `pkg/proc/runtime`)**: Specialized integer arithmetic/comparison opcodes (`OpAddInt` ~ `OpGteInt`) with direct ALU execution, plus first-class `OpAnd` and `OpOr` opcodes eliminating dynamic interface comparisons.
- [x] **VDBE Fused Compare-and-Branch & Peephole Jump Optimization (`pkg/proc`, `pkg/proc/runtime`)**: Fused compare-and-branch instructions (`OpJumpIfEqInt`, etc.), trampoline jump threading, and adjacent fall-through jump elimination, cutting condition instructions by 50%.
- [x] **VDBE Dispatcher Modularization (`pkg/proc/runtime/vm_dispatcher.go`)**: Decomposed the monolithic 800+ line switch into domain helpers (`execFlowOp`, `execComputeOp`, `execTxOp`, `execStorageOp`, `execCursorAndCallOp`, `execRelOp`) and centralized catch-stack unwinding (`handleCatch`).
- [x] **PlanCache Bounded LRU Cache & Memory Defense (Golden Rule 29) (`pkg/proc/runtime`)**: `container/list` based bounded LRU cache (`DefaultPlanCacheCapacity = 1024`), automatic eviction upon capacity saturation, and long-lived connection memory defense.
- [x] **T-SQL & ANSI SQL Core Syntaxes & Execution Engine (`S-1` ~ `S-7`)**:
  - `OFFSET ... FETCH NEXT ... ROWS ONLY` and `SELECT TOP (n)` standard pagination.
  - FROM-clause derived tables (`FROM (SELECT ...) AS t`) and inline subqueries with isolated materialization (`__dt_*`).
  - `INSERT INTO ... SELECT ...` bulk batch ingestion with IDENTITY generation and OUTPUT delta tracking.
  - `CROSS APPLY` and `OUTER APPLY` join pipelines.
  - `FULL [OUTER] JOIN` and `RIGHT [OUTER] JOIN` Volcano hash joins with unmatched row draining.
  - `CAST`, `TRY_CAST`, `CONVERT`, `TRY_CONVERT`, PostgreSQL `::` regular AST parsing and VDBE `OpCast` integration.
  - `CASE WHEN` and `COALESCE`/`ISNULL` short-circuit evaluation.
  - `WHERE [NOT] EXISTS` correlated subquery Semi/Anti-Join CBO transformation and uncorrelated early evaluation.
- [x] **In-Process Go Native Scalar UDF Registration SPI (`pkg/proc`, `pkg/api`)**: Thread-safe `proc.RegisterScalarFunction(name, fn)` SPI delivering zero-overhead, sub-microsecond in-process execution with built-in `--doctor` diagnostic probe.
- [x] **Volcano Vectorized Chunk-at-a-Time Batch Streaming (`P-4`) (`pkg/execution`, `pkg/engine`)**: 1,024-tuple vectorized chunk streaming (`ChunkedIterator`, `NextChunk`) reducing virtual dispatch and context cancellation checks by 99% across SeqScan, IndexScan, Filter, Project, Limit, Aggregate, Sort, HashJoin, IndexNestedLoopJoin, and SQL select coordinator, with transparent 100% backward compatibility.
- [x] **0-Allocation Row Streaming (`Tuple.ToRow()`) (`pkg/execution`)**: Direct low-level `storage.Value` slice conversion bypassing intermediate `map[string]any` heap allocation per row, cutting GC pressure by 70%.
- [x] **Volcano Iterator Relational Execution Pipeline (`pkg/execution`)**: Full streaming iterator engine (`SeqScan`, `IndexScan`, `IndexOnlyScan`, `Filter`, `Project`, `Sort`, `Limit`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`, `CTE`).
- [x] **Window Functions & Frame Analytic Engine (`pkg/execution/window.go`)**: Support for `OVER (PARTITION BY ... ORDER BY ... [ROWS BETWEEN])`, `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`, and aggregate window functions.
- [x] **T-Digest Exact & Streaming Quantile Engine (`pkg/execution/tdigest.go`)**: High-accuracy tail percentile estimation with bounded memory (`PERCENTILE_CONT`, `PERCENTILE_DISC`).
- [x] **Cooperative Query Cancellation (`pkg/proc/runtime`, `pkg/api`, `pkg/wire`)**: Non-destructive `context.Context` abort pipeline, T-SQL `KILL <spid>`, PostgreSQL `pg_cancel_backend()`, and wire `CancelRequest`.

### 2.2. Query Optimizer & CBO
- [x] **CBO Optimizer Hints Override (`D-9`) (`pkg/planner`, `pkg/proc`)**: Table hints (`WITH (INDEX(...))`) and query hints (`OPTION (HASH/MERGE/LOOP JOIN, FORCE ORDER)`) for deterministic execution plan override.
- [x] **Sort-Merge Join (SMJ) Operator & CBO Cost Modeling (`B-3`) (`pkg/execution`, `pkg/planner`)**: $O(M+N)$ single-pass streaming `MergeJoinIterator` for pre-sorted relations eliminating hash table overhead, with Mark & Restore buffer windowing for duplicates.
- [x] **CBO Boolean Tree AST Selectivity Estimation & OR Compound Probabilities (`C-6`) (`pkg/planner/stats.go`)**: Recursive AST boolean tree evaluation, mutual exclusion union ($P(A)+P(B)$), independent union ($P(A)+P(B)-P(A)P(B)$), NOT ($1-P$), IN, and BETWEEN estimation.
- [x] **Cost-Based Optimizer (CBO) & Statistics Catalog (`pkg/planner/stats.go`)**: `ANALYZE TABLE` 64-bucket equi-depth histograms, cardinality, distinct values (NDV), null fractions, and automatic cost modeling (Hash Join vs Index Nested Loop Join).
- [x] **`ANALYZE TABLE` Multi-Core Parallel Statistics Collection (`pkg/planner/stats.go`)**: Parallelized column statistics gathering via `sync.WaitGroup` multi-core workers (`BuildTableStatsParallel`), accelerating analysis by 4~8x.
- [x] **CBO Statistics Disk Persistence (`pkg/catalog`, `cbo_stats.json`)**: Atomic file writes (Golden Rules 1 & 5) and lossless `storage.Decimal` JSON serialization, preserving statistics and optimized plans across server restarts.
- [x] **Predicate Pushdown (`pkg/planner/pushdown.go`)**: Complex filter conjunct pushdown to lower scan iterators before join materialization.
- [x] **Constant Folding & Boolean Simplification (`pkg/proc/fold.go`)**: Compile-time constant arithmetic, pre-computed scalar functions, and tautology/contradiction short-circuit reduction.
- [x] **Subquery Decorrelation (`pkg/planner/decorrelate.go`)**: Automatic rewrite of correlated `EXISTS`, `NOT EXISTS`, `IN`, and `NOT IN` predicates into relational `SEMI` / `ANTI` joins.

### 2.3. Storage, Transactions & Replication
- [x] **Binary Tuple Lazy Decoding & Covering Index Non-Key Columns (`P-2`) (`pkg/storage`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**: Zero-allocation `SkipTupleColumn`, $O(1)$ offset caching `LazyTuple`, selective column decoding, and `CREATE INDEX ... INCLUDE(...)` covering non-key columns stored in secondary indexes for 100% table heap lookup bypass.
- [x] **Fast SQL Fingerprinting & Simple PK Query Bypass (`P-5`) (`pkg/engine`, `pkg/storage`)**: 64-bit FNV-1a fingerprint hashing of point PK queries (`SELECT ... WHERE id = ?`) routing directly to storage engine (`tx.Get`), bypassing regular parser, CBO planner, and VDBE stack with bounded cache and DDL invalidation.
- [x] **Storage Pipeline Group Commit & Fsync Acceleration**: Implemented leader/follower group commit across both `memengine` WAL (`file.Sync`) and `wiredtiger` (`WT_SESSION::log_flush("sync=on")`) along with lock-free commit fast-paths. `FsyncAlways` latency at 16/64 workers dropped 100x to 0.038ms per commit, and TPC-C VDBE reached 14,800+ TPS (422,000+ tpmC).
- [x] **1-Writer Multi-Reader WAL Streaming Replication & HA (`pkg/replication`, `cmd/server`)**: Primary-to-replica live TCP WAL log shipping, replica read-only enforcement (`ErrReadOnlyReplica`), and zero-downtime promotion.
- [x] **Dual Storage Engine Architecture (`pkg/storage`)**: Production disk-based WiredTiger (Cgo, lock-free B+Tree, MVCC) and pure-Go In-Memory OCC engine (MessagePack, WAL/AOF durability).
- [x] **Covering Index Scan (Index-Only Scan) (`pkg/planner`, `pkg/execution`, `pkg/storage/memengine`)**: Zero-heap-fetch relational execution when projections are covered by index keys and PKs (`NodeIndexOnlyScan` and `SkipList.ScanRangeEntries` synthesized records).
- [x] **IETF RFC 9562 Standard UUIDv7 (`pkg/storage`, `pkg/proc`)**: 48-bit monotonically increasing Unix Epoch timestamps for sequential B+Tree inserts, with `NEWID7()`, `UUIDV7()`, `GEN_RANDOM_UUID(7)` built-ins, and `DEFAULT NEWID7()` table PK defaults.
- [x] **ClickHouse-Style Rolling Range Table Partitioning (`pkg/storage`, `pkg/planner`, `pkg/api`)**: Automatic rolling partitions via `PARTITION BY toYYYYMM(col)`, `MONTH(col)`, partition pruning, and $O(1)$ fast partition dropping (`DROP PARTITION`).
- [x] **In-Memory Ephemeral Engine Mode (`cmd/server`, `pkg/storage/memengine`)**: `--ephemeral` flag disabling all disk logging for blazing-fast, RAM-only execution.
- [x] **Transaction Savepoints (`pkg/api/savepoint_test.go`, `pkg/storage`)**: T-SQL `SAVE TRANSACTION` / ANSI `SAVEPOINT`, partial rollback `ROLLBACK TO SAVEPOINT`, and `RELEASE SAVEPOINT`.
- [x] **Lock-Free Concurrent SkipList Index (`pkg/storage/memengine/skiplist.go`)**: Secondary index engine powered by concurrent SkipLists ($O(\log N)$, 2.8M inserts/sec).
- [x] **Query Memory Limit Governor (`work_mem`) (`pkg/execution/spill.go`)**: Automatic fallback to external merge sort on temporary disk files when memory thresholds are exceeded.
- [x] **Online Non-Blocking Hot Snapshot Backup (`pkg/backup`)**: Consistent point-in-time `.tar.gz` archive generation under read-only snapshot isolation (`POST /api/backup`) without write lock pauses.

### 2.4. Architecture & Security
- [x] **Architecture Layering: SQL Execution Coordinator Separation (`pkg/api` -> `pkg/engine`)**: Promoted over 14,000 lines of SQL execution orchestration (`sqlexec_*.go`) into a dedicated `pkg/engine` core layer. Wire listeners (`pkg/wire`, `pkg/wire/tds`) invoke `engine.SQLCoordinator` directly with zero dependencies on `pkg/api`, breaking layer inversion completely.
- [x] **Granular SQL Object-Level RBAC (`pkg/auth`, `pkg/catalog`, `pkg/api`)**: Complete role/user DDL (`CREATE LOGIN/USER`, `SETUSER`), granular privileges (`GRANT/REVOKE/DENY` for `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `EXECUTE`, `CONTROL`, `REFERENCES`), and pre-execution catalog interceptors (`checkTableAccess`).

### 2.5. Wire Protocols & Tool Interoperability
- [x] **TDS Wire Protocol NBCROW Row Compression (`0xD2`) (`pkg/wire/tds`)**: TDS 7.3+ standard `BuildNBCRow` encoding with null bitmap headers and 0-byte payload for NULL columns, reducing network bandwidth by 30%+ on sparse queries.
- [x] **PostgreSQL Modern Driver Compatibility: `pgx/v5` (`pkg/wire`)**: Full pass across `github.com/jackc/pgx/v5` test suite, binary parameter/result format decoding, and Extended Query `Flush` batch pipelining.
- [x] **Official Microsoft MSSQL Driver E2E Compatibility (`pkg/wire/tds`)**: 100% pass across `github.com/microsoft/go-mssqldb`, `PacketTransactionManager` (0x0E), Collation/Transaction `ENVCHANGE` tokens, and `DoneInXact` transaction state machine.
- [x] **Official PostgreSQL lib/pq Driver Compatibility (`pkg/wire`)**: 100% pass across `github.com/lib/pq`, dynamic parameter count inference, projection schema matching, and binary result formats.
- [x] **TDS Asynchronous Attention Query Cancellation (`pkg/wire/tds`)**: Instant response to client `SqlCommand.Cancel()` via `Type 0x06` attention packet decoupling, VDBE loop cancellation (`context.CancelFunc`), and `DoneAttn (0x0020)` acknowledgments.
- [x] **Developer Real-Time Wire Query Tracing (`-dev` flag) (`pkg/wire`, `pkg/wire/tds`)**: Real-time console logging of incoming queries, bound parameters, execution duration, and error traces across PGWire and TDS.
- [x] **GUI Client Tool Interoperability (DbGate, Beekeeper Studio, DBeaver) (`pkg/wire/tds`, `pkg/api`)**: Virtual system catalog joins, `@@VERSION` / `SERVERPROPERTY` edition detection interceptors, and deterministic object ID assignment.
- [x] **Embedded Web Management Studio (`pkg/ui`)**: Standalone web UI on `:8090/_/`, interactive query console, spreadsheet CRUD, and visual VDBE disassembler (`EXPLAIN BYTECODE`).

---

## 3. Future Technical Milestones

### Phase 5: Enterprise Governance, Security & Engine Decoupling (v0.16.0 - v0.19.0 Target)

#### 5.1. CBO Optimizer Hints Override (`D-9`) & Sort-Merge Join Operator (`B-3`) (Completed)
- **Challenge**:
  - Parse `OPTION (HASH JOIN, LOOP JOIN, FORCE ORDER)` and `WITH (INDEX(<name>))` hints to override CBO plan selection (Completed `D-9`).
  - Implement an $O(M+N)$ single-pass `MergeJoinIterator` for pre-sorted relations and add sort-merge join cost models (Completed `B-3`).
- **Outcome**: Elimination of hash table overhead on sorted inputs and full DBA execution plan control.

#### 5.2. Fast SQL Fingerprinting & Simple PK Query Bypass (`P-5`) (Completed)
- **Challenge**: Bypass regular parsing, binding, CBO planning, and VDBE compilation for point PK lookups (`SELECT * FROM tbl WHERE id = ?`) by using Wyhash/FNV text fingerprinting and dispatching directly to `Engine.Get`.
- **Outcome**: 50%+ boost in point query TPS and 90% reduction in GC overhead.

#### 5.3. TDS Wire Protocol NBCROW Row Compression (`0xD2`) (Completed)
- **Challenge**: Implement TDS 7.3+ standard `NBCROW` (Null Bitmap Compressed Row, `0xD2`) encoding, sending NULL column values with 0 payload bytes.
- **Outcome**: 30%+ reduction in network bandwidth consumption for enterprise queries returning sparse tables.

#### 5.4. Binary Tuple Lazy Decoding & Covering Index Non-Key Columns (`P-2`) (Completed)
- **Challenge**: Implement zero-allocation `SkipTupleColumn`, selective decoding `DecodeTupleSelected`, $O(1)$ offset caching `LazyTuple`, and `CREATE INDEX ... INCLUDE(...)` covering non-key column storage.
- **Outcome**: Zero heap allocation for unprojected columns during table scans and 100% table heap lookup elimination via Index-Only Scans.

#### 5.5. Volcano Vectorized Chunk-at-a-Time Batch Streaming (`P-4`) (Completed)
- **Challenge**: Implement 1,024-tuple batch streaming (`ChunkedIterator`, `NextChunk`) across relational operators to reduce virtual dispatch and context cancellation overhead.
- **Outcome**: 99% reduction in per-tuple function call overhead, tight vectorized filter/projection loops, and 100% backward compatibility for single-tuple consumers.

#### 5.6. Transparent Data Encryption (TDE) & Enterprise Immutable Audit Log (`pkg/storage`, `pkg/logger`)
- **Challenge**:
  - Encrypt WiredTiger data files, WAL/AOF journals, and backup archives at rest using AES-256-GCM (Data at Rest Encryption).
  - Implement tamper-evident immutable audit logging for authentication events, privilege changes (`GRANT/REVOKE`), and schema alterations.
- **Expected Outcome**: Complete defense against direct physical disk extraction and full enterprise audit traceability.

#### 5.7. `INSTEAD OF` Trigger Runtime Interceptor (`pkg/proc/runtime`, `pkg/api`)
- **Challenge**: Intercept `INSERT`, `UPDATE`, and `DELETE` operations on views and complex tables, substituting them with user-defined procedural routines.
- **Expected Outcome**: Virtual entity update capability on complex views and encapsulated business integrity enforcement.

#### 5.8. BaaS Developer Productivity: Migration CLI & OAuth2 Social Login (`cmd/server`, `pkg/migrate`, `pkg/api`)
- **Challenge**:
  - Declarative schema migration CLI: `sqlion migrate new <name>`, `sqlion migrate up / down`, and `_migrations` version history tracking.
  - Social login: Built-in Google and GitHub OAuth2 endpoints (`/api/auth/oauth2/callback`) with automatic user provisioning and JWT issuance.
- **Expected Outcome**: PocketBase-class developer productivity and turnkey deployment workflows.

---

### Phase 6: High Availability Always On, Decoupled HTAP & Production Hardening (v1.0.0 LTS Target)

#### 6.1. Always On Style Automatic Failover & Leader Election (`pkg/replication`)
- **Challenge**:
  - Automatically promote the replica with the lowest LSN lag to primary upon heartbeat timeout.
  - Implement term validation to prevent split-brain conditions and automate client routing handover.
- **Expected Outcome**: 99.999% high availability with zero-touch automatic recovery.

#### 6.2. Transactional Outbox & CDC Event Streaming (`pkg/storage`, `pkg/api`)
- **Challenge**:
  - Atomically generate row-level change delta events (inserted, updated, deleted) coupled with transaction commit boundaries.
  - Implement reliable outbox dispatchers forwarding events to external message brokers (Kafka, RabbitMQ) or webhooks.
- **Expected Outcome**: Lossless event-driven architecture integration without external dual-write anomalies.

#### 6.3. Shared Memory (POSIX shm / mmap) Apache Arrow IPC Snapshots (DuckDB Zero-Copy Integration) (`pkg/storage`)
- **Challenge**:
  - Serialize point-in-time table snapshots into Apache Arrow IPC RecordBatch columnar format in POSIX shared memory (`/dev/shm` or `mmap` files) via `SNAPSHOT TABLE <t> TO ARROW_SHM <name>`.
  - Pass file descriptors directly to external analytical engines (DuckDB, Python Polars, Pandas) for zero-copy vectorized processing.
  - Guarantee zero blast radius: heavy analytical queries cannot cause OOM or block OLTP write paths.
- **Expected Outcome**: Decoupled hybrid transactional/analytical processing (HTAP) integrating the open-source analytical ecosystem with 0ms transfer latency.

#### 6.4. [Postponed] Pure-Go Persistent Storage Engine Option (Pebble / bbolt) (`pkg/storage`)
- **Challenge**: Add a pure-Go persistent storage backend under the `storage.Engine` interface, enabling static compilation (`CGO_ENABLED=0`, Golden Rule 86) without C compiler toolchains.
- **Expected Outcome**: 100% autonomous, cross-platform static binary distribution across any Linux/Windows/macOS environment.

#### 6.5. Dynamic Programming (DP / Selinger) Large Join Order Optimization ($N > 4$) (`pkg/planner`)
- **Challenge**: Implement a DPccp dynamic programming search algorithm evaluating left-deep and bushy join trees for complex queries joining 5 or more tables.
- **Expected Outcome**: Optimal execution plans for complex enterprise ERP and data mart join queries.

#### 6.6. Extended Multi-Column Statistics, MCV & Optimizer Hints (`pkg/planner`)
- **Challenge**: Collect multi-column distinct value (NDV) statistics to correct cardinality underestimation on correlated columns and maintain MCV lists.
- **Expected Outcome**: Elimination of estimation skew on correlated predicates.

#### 6.7. T-SQL Specialized TDS Serialization & PLP LOB Streaming (`pkg/wire/tds`)
- **Challenge**:
  - Full binary encoding for `UNIQUEIDENTIFIER` (16-byte GUID byte swapping), `DATETIME2` (`0x2A`), `DATE` (`0x28`), and `TIME` (`0x29`).
  - Partially Length-Prefixed (PLP) streaming for large `VARCHAR(MAX)` and `VARBINARY(MAX)` payloads exceeding 8KB.
- **Expected Outcome**: 100% wire-level data type compatibility for legacy enterprise SQL Server applications.

#### 6.8. Multi-Language Client Ecosystem Certification (`pkg/wire`, `pkg/wire/tds`)
- **Challenge**: Establish automated end-to-end certification test harnesses for Python (`psycopg3`, `pymssql`), Node.js (`pg`, `tedious`), and C#/.NET (`Microsoft.Data.SqlClient`).
- **Expected Outcome**: Guaranteed out-of-the-box compatibility across all major application development stacks.

#### 6.9. Volcano Chunked Batch Execution (Vectorized Chunk Iterator) (`pkg/execution`)
- **Challenge**: Transition row-by-row `Next()` calls to vector slice streaming (`NextBatch([64]Row)`), mitigating virtual function dispatch overhead.
- **Expected Outcome**: 3~5x throughput acceleration for full-table scans, aggregations, and analytical filters.

#### 6.10. Background MVCC Vacuuming & Version GC (`pkg/storage/memengine`)
- **Challenge**: Maintain version chains for modified rows and reclaim expired versions and deletion tombstones asynchronously via background vacuum workers.
- **Expected Outcome**: Prevention of memory fragmentation in 24/7 long-running in-memory workloads.

#### 6.11. Wasm Runtime Sandbox UDF (`pkg/extension`, `wazero`)
- **Challenge**: Embed pure-Go `wazero` to execute dynamically loaded WebAssembly functions (`CREATE FUNCTION ... AS WASM`) in isolated memory sandboxes.
- **Expected Outcome**: Safe in-engine execution of third-party plugins with zero host crash risk.

#### 6.12. Local & S3-Compatible File Storage Module (`pkg/api/handlers_files.go`)
- **Challenge**: Implement multipart file upload APIs (`POST /api/files/:table/:id`), static asset serving, asynchronous image thumbnail generation, and storage quotas.
- **Expected Outcome**: Turnkey all-in-one BaaS storage capability directly inside the single standalone binary.

#### 6.13. Lazy Tuple Materialization & Column Projection Pushdown (`pkg/storage`, `pkg/execution`)
- **Challenge**: Inspect row headers and null bitmaps to decode only the filter columns required by the query `WHERE` predicate, materializing remaining columns only for matching rows.
- **Expected Outcome**: 70~80% reduction in CPU cycles and GC heap allocations during large-scale table scans.

#### 6.14. Per-Query Arena Memory Allocator & GC Elimination (`pkg/execution`, `pkg/storage`)
- **Challenge**: Allocate dedicated memory arena slabs tied to the lifecycle of individual analytical queries and transactions, reclaiming memory with a single pointer reset.
- **Expected Outcome**: Complete elimination of Go runtime GC pauses and flat p99/p99.9 latency during heavy analytical queries.

---

## 4. Pending Task Matrix

| Milestone | Priority | Task Description | Domain | Estimated Effort | Strategic Benefit |
| :---: | :---: | :--- | :---: | :---: | :--- |
| **Phase 5** | **P2** | **Fast SQL Fingerprinting & Simple PK Bypass (`P-5`)** | Engine & Storage | Medium (250~350 lines) | Bypasses parser/planner for point PK lookups, 50%+ TPS boost |
| **Phase 5** | **P2** | **TDS NBCROW Row Compression (`0xD2`)** | TDS Protocol | Medium (200~300 lines) | 30%+ network bandwidth savings on sparse/NULL tables |
| **Phase 5** | **P2** | **Transparent Data Encryption (TDE: AES-256-GCM)** | Storage & Security | Medium (250~350 lines) | Transparent data-at-rest encryption and immutable audit logging |
| **Phase 5** | **P2** | **`INSTEAD OF` Trigger Runtime Interceptor** | T-SQL Runtime | Medium (200~300 lines) | Virtual entity DML update routing on complex views |
| **Phase 5** | **P3** | **Multi-Language Client Certification Harness** | Wire Protocols | Medium (200~300 lines) | Automated integration suites for Python, Node.js, and .NET |
| **Phase 5** | **P3** | **Declarative Schema Migration CLI** | BaaS & Tooling | Medium (250~350 lines) | `sqlion migrate` atomic DDL version history tracking |
| **Phase 5** | **P3** | **OAuth2 Social Login (Google, GitHub)** | Auth & API | Medium (200~300 lines) | Turnkey authentication and JWT provisioning |
| **Phase 6** | **P1** | **Always On Style Automatic Failover** | HA Replication | High (400~500 lines) | Automatic promotion of lowest-lag replica, zero downtime |
| **Phase 6** | **P1** | **Transactional Outbox & CDC Streaming** | Storage & Events | Medium (300~400 lines) | Atomic row-level change event streams dispatched to Kafka/RabbitMQ |
| **Phase 6** | **P1** | **Shared-Memory Apache Arrow IPC Snapshots (DuckDB)** | Analytics & HTAP | Medium (300~400 lines) | Zero-copy, zero-I/O decoupled OLAP engine integration |
| **Phase 6** | **P3** | **[Postponed] Pure-Go Persistent Engine Backend (Pebble/bbolt)** | Storage Backend | High (500~700 lines) | 100% static standalone executable (`CGO_ENABLED=0`) distribution |
| **Phase 6** | **P1** | **Lazy Tuple Materialization & Projection Pushdown** | Storage & Executor | Medium (300~400 lines) | 70~80% CPU & GC reduction on large table scans |
| **Phase 6** | **P1** | **Per-Query Arena Memory Allocator** | Memory Governor | Medium (300~400 lines) | Eliminates Go GC pauses during large joins/sorts/aggregations |
| **Phase 6** | **P2** | **$N > 4$ Join Dynamic Programming (DP / Selinger)** | CBO & Planner | High (400~600 lines) | Optimal join ordering for complex multi-table queries |
| **Phase 6** | **P2** | **Multi-Column Statistics, MCV & Optimizer Hints** | CBO & Planner | Medium (300~400 lines) | Corrects cardinality estimation skew on correlated columns |
| **Phase 6** | **P2** | **T-SQL Specialized TDS Serialization & PLP LOB** | TDS Protocol | Medium (300~400 lines) | Complete binary support for UNIQUEIDENTIFIER, DATETIME2, LOB |
| **Phase 6** | **P2** | **Volcano Chunked Batch Execution (Vectorized)** | Executor & SIMD | High (600~800 lines) | 3~5x acceleration for table scans and aggregate filters |
| **Phase 6** | **P3** | **Background MVCC Vacuum / Version GC** | Concurrency & RAM | High (400~500 lines) | Eliminates memory fragmentation in 24/7 long-running in-memory tables |
| **Phase 6** | **P3** | **Wasm Runtime Sandbox UDF (`wazero`)** | Plugins & Sandbox | Medium (300~400 lines) | Secure, crash-isolated in-engine execution of third-party plugins |
| **Phase 6** | **P3** | **Local & S3-Compatible File Storage & Thumbnails** | BaaS Storage | Medium (250~350 lines) | Multipart file upload and automatic image thumbnail generation |
