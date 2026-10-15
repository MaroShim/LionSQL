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
  - [x] TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET & UNIQUEIDENTIFIER Binary Serialization
  - [x] Multi-Language Client Certification Harness (Python psycopg3 & pymssql E2E)
  - [ ] Transparent Data Encryption (TDE: AES-256-GCM) & Immutable Audit Log
  - [x] BaaS Expansion: Declarative Schema Migration CLI (lionsql migrate) & OAuth2 Social Login

[ Phase 6: High Availability Always On, Decoupled HTAP & Production Hardening (v1.0.0 LTS) ] ──► PLANNED
  - [x] Always On Style Automatic Failover & Leader Election
  - [x] Tolerant Virtual No-Op USE Statement & Session Database Synchronization
  - [x] Decoupled Real-time CDC Event Streaming (GET /api/cdc/stream & DuckDB HTAP)
  - Transactional Outbox Broker Dispatcher (Kafka / RabbitMQ)
  - [Postponed] Pure Go Persistent Storage Engine Backend (Pebble / bbolt, CGO_ENABLED=0)
  - [x] Dynamic Programming (DP / Selinger) Large Join Order Optimization (N > 4) (B-4)
  - [x] Extended Multi-Column Statistics (C-5), MCV & Optimizer Hints
  - [x] T-SQL Specialized TDS Serialization (UNIQUEIDENTIFIER, DATETIME2, PLP LOB)
  - Multi-Language Client Certification (Python psycopg3/pymssql, Node.js pg/tedious, .NET SqlClient)
  - [x] Storage to Wire Zero-Copy Direct Serialization (P-3)
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
- [x] **Multi-Column Extended Statistics & Functional Dependency Modeling (`C-5`) (`pkg/planner`, `pkg/catalog`, `pkg/engine`)**: Multi-column NDV collection, functional dependency coefficient calculation ($dep \in [0.0, 1.0]$), `CREATE STATISTICS` / `DROP STATISTICS` DDL, and joint selectivity estimation ($dep \cdot \min(P_i) + (1-dep) \cdot \prod P_i$) eliminating cardinality underestimation on correlated columns.
- [x] **Selinger Dynamic Programming Join Reordering for $N > 4$ Chains (`B-4`) (`pkg/planner/planner.go`)**: Replaced $O(N!)$ permutation enumeration with Selinger DP and subset bitmask memoization ($O(N \cdot 2^{N-1})$), pruning Cartesian products and bounding state spaces to $\le 12$ relations for sub-millisecond planning on complex multi-table joins.
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
- [x] **Storage ➔ Wire Zero-Copy Direct Serialization (`P-3`) (`pkg/wire`, `pkg/wire/tds`)**: Implemented `WriteNBCRowDirect` in TDS wire protocol emitting `TokenNBCRow` (0xD2) and null bitmaps directly into wire buffers, eliminated per-row `[]any` and `[][]byte` boxing, and pre-allocated single scratch slices across TDS and PGWire streaming hot paths.
- [x] **TDS PLP (Partially Length-Prefixed) LOB Chunk Streaming (`pkg/wire/tds`)**: Implemented [MS-TDS] 2.2.5.2 PLP streaming encoding for `VARCHAR(MAX)`, `NVARCHAR(MAX)`, and `VARBINARY(MAX)` (> 8,000 bytes) with 8-byte total length headers, up to 8,000-byte chunk splits with 4-byte LE length headers, 4-byte zero terminal chunk, and 8-byte `0xFFFFFFFFFFFFFFFF` NULL values.
- [x] **TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET, and UNIQUEIDENTIFIER Binary Serialization (`pkg/wire/tds`)**: Complete SQL Server 2008 / TDS 7.3 wire binary serialization for `DATE` (`0x28`, 3-byte LE days since 0001-01-01), `TIME` (`0x29`, 100ns ticks with scale 0..7), `DATETIME2` (`0x2A`, ticks + date), `DATETIMEOFFSET` (`0x2B`, UTC datetime2 + 2-byte minute offset), and `UNIQUEIDENTIFIER` (`0x24`, Windows Mixed-Endian GUID byte swap). Result set column type inference auto-detects date/time/guid types and official `go-mssqldb` passes 100%.
- [x] **External Multi-Language Client Driver Certification Harness (`pkg/wire`, `pkg/wire/tds`)**: Automated hermetic end-to-end integration test suites for Python `psycopg3` (PostgreSQL Wire Protocol :5433) and `pymssql` (TDS Protocol :1433 / FreeTDS dblib), verifying DDL, CRUD, binary date/time/UUID parsing, NULL handling, and explicit transaction (`COMMIT`/`ROLLBACK`) atomicity via isolated `uv run` sandboxing.
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

#### 5.8. BaaS Developer Productivity: Migration CLI & OAuth2 Social Login (`cmd/lionsql`, `pkg/migrate`, `cmd/server`, `pkg/api`, `pkg/auth`, `pkg/config`, `pkg/ui`)
- **Implemented**:
  - Declarative schema migration engine and `lionsql` unified CLI tool: `lionsql migrate new <name>`, `lionsql migrate up / down / status`, ad-hoc headless `lionsql sql "<query>"`, and atomic `_migrations` version history tracking.
  - Dual binary parity: identical migration syntax and flag handling across `lionsql migrate` and `server migrate`.
  - Built-in Google and GitHub OAuth2 social login: official endpoints (`/api/auth/oauth2/providers`, `/api/auth/oauth2/url`, `GET/POST /api/auth/oauth2/callback`), automatic user provisioning (`ProvisionOAuthUser`), JWT issuance, secret masking (`GET/PUT /api/settings/oauth2`), and embedded Web Studio modal popup integration.
- **Strategic Value**: PocketBase-class developer productivity, turnkey deployment workflows, and seamless administrative CLI automation.

---

### Phase 6: High Availability Always On, Decoupled HTAP & Production Hardening (v1.0.0 LTS Target)

#### 6.1. Always On Style Automatic Failover & Leader Election (`pkg/replication`, `cmd/server`, `pkg/config`, `pkg/ui`) (Completed)
- **Implemented**:
  - Always On automated failover coordinator (`pkg/replication/failover.go`) with highest-LSN precedence rule ensuring zero data loss.
  - Monotonic term tracking (`term int64`) across nodes, leader announcements (`POST /api/replication/leader`), and peer voting (`POST /api/replication/vote`).
  - Split-brain prevention (fencing): primary node immediately steps down to read-only replica upon detecting a higher term announcement or votes.
  - Primary heartbeat timeout detection in secondary replicas (`ReplicationClient.LastHeartbeatTime()`) with automatic election triggering upon primary failure.
  - Full CLI flag and configuration integration (`-auto-failover`, `-peers`, `-self-url`, `-heartbeat-timeout-ms`, `-election-timeout-ms`).
  - Embedded Web Studio (`pkg/ui/dashboard.html`) real-time term and auto-failover status visualization.
  - Comprehensive unit and integration test suite (`pkg/replication/failover_test.go`).
- **Strategic Value**: Enterprise 99.999% high availability with zero-touch automatic recovery and zero data loss.

#### 6.2. Decoupled Real-time CDC Streaming & Transactional Outbox (`pkg/cdc`, `pkg/storage`, `pkg/api`)
- **Implemented (`pkg/cdc`)**:
  - Decoupled real-time CDC event streaming engine (`ChangeEvent`, `Streamer`) with bounded ring-buffer historical replay (Golden Rule 23) and non-blocking backpressure fan-out (Golden Rule 29).
  - High-performance HTTP streaming endpoint `GET /api/cdc/stream` with real-time NDJSON and SSE support, table filtering (`?table=...`), and heartbeat keep-alive.
  - Multi-hook registration (`AddWALHook`) across In-Memory OCC (`memengine`) and WiredTiger (`wt_engine`, `wt_stub`) backends to synchronously tap commit boundaries.
  - Complete zero-dependency Python DuckDB micro-batch integration guide (`doc/CDC_DUCKDB_INTEGRATION.md`).
- **Pending Outbox Forwarders**:
  - Outbox dispatchers forwarding events to external message brokers (Kafka, RabbitMQ).
- **Expected Outcome**: Decoupled hybrid transactional/analytical processing (HTAP) with zero OLTP blast radius, alongside lossless event-driven broker integration.

#### 6.3. [Postponed] Pure-Go Persistent Storage Engine Option (Pebble / bbolt) (`pkg/storage`)
- **Challenge**: Add a pure-Go persistent storage backend under the `storage.Engine` interface, enabling static compilation (`CGO_ENABLED=0`, Golden Rule 36) without C compiler toolchains.
- **Expected Outcome**: 100% autonomous, cross-platform static binary distribution across any Linux/Windows/macOS environment.

#### 6.4. Dynamic Programming (DP / Selinger) Large Join Order Optimization ($N > 4$) (`pkg/planner`) (Completed)
- **Implemented (`B-4`)**: Replaced $O(N!)$ permutation generation with Selinger dynamic programming evaluating subset bitmasks (`1 << n`) with flat-slice $O(1)$ indexing. Includes primary join-key connectivity pass (pruning Cartesian products) with fallback disconnected pass, bounded to $n \le 12$ relations. Validated sub-millisecond planning on 6-table and 8-table chain joins.
- **Outcome**: Sub-millisecond join reordering for complex multi-table queries, eliminating exponential combinatorial blowup.

#### 6.5. Extended Multi-Column Statistics (`C-5`), MCV (`C-7`) & Optimizer Hints (`pkg/planner`) (Completed)
- **Implemented (`C-5`, `C-7`)**:
  - `MultiColumnStats` (`C-5`): multi-column NDV collection, functional dependency coefficient calculation ($dep \in [0.0, 1.0]$), and joint selectivity calculation ($dep \cdot \min(P_i) + (1-dep) \cdot \prod P_i$) in `EstimateCompoundSelectivity` and `EstimateExprSelectivity` (`pkg/planner/stats.go`). Added DDL support for `CREATE STATISTICS stat_name ON tbl (col1, col2, ...)` and `DROP STATISTICS` (`pkg/engine`, `pkg/catalog`). Automatically collects multi-column statistics for composite indexes and registered statistics during `ANALYZE TABLE`.
  - `MCVItem` & Skew List Statistics (`C-7`): extracted Most Common Values (Top-20) with observed frequencies in `BuildColumnStats`. Decoupled selectivity evaluation between exact MCV matching and equi-depth histogram interpolation on the non-MCV tail for `=`, `!=`, `IN`, `BETWEEN`, and range predicates (`<`, `<=`, `>`, `>=`).
- **Outcome**: Elimination of cardinality underestimation on correlated columns and skewed categorical/numerical attributes.

#### 6.6. T-SQL Specialized TDS Serialization (Completed) & PLP LOB Streaming (`pkg/wire/tds`)
- **Implemented**: Full binary encoding and decoding for `UNIQUEIDENTIFIER` (16-byte Windows Mixed-Endian GUID byte swapping), `DATE` (`0x28`), `TIME` (`0x29`), `DATETIME2` (`0x2A`), and `DATETIMEOFFSET` (`0x2B`).
- **Pending**: Partially Length-Prefixed (PLP) streaming for large `VARCHAR(MAX)` and `VARBINARY(MAX)` payloads exceeding 8KB.
- **Expected Outcome**: 100% wire-level data type compatibility for legacy enterprise SQL Server applications.

#### 6.7. Multi-Language Client Ecosystem Certification (`pkg/wire`, `pkg/wire/tds`)
- **Implemented**: Automated hermetic E2E test harness for Python `psycopg3` (PGWire :5433) and `pymssql` (TDS :1433), validating DDL, CRUD, binary types, and transaction atomicity.
- **Pending**: Additional driver suites for Node.js (`pg`, `tedious`) and C#/.NET (`Microsoft.Data.SqlClient`).
- **Expected Outcome**: Guaranteed out-of-the-box compatibility across all major application development stacks.

#### 6.8. Volcano Chunked Batch Execution (Vectorized Chunk Iterator) (`pkg/execution`) (Completed)
- **Implemented (`P-4`)**: Deployed 1,024-tuple batch streaming (`ChunkedIterator`, `NextChunk`) across relational operators (`SeqScan`, `IndexScan`, `Filter`, `Projection`, `Limit`, `Aggregate`, `Sort`, `HashJoin`, `IndexNestedLoopJoin`), mitigating virtual dispatch overhead by 99% while maintaining 100% backward compatibility for single-tuple consumers.
- **Outcome**: 3~5x throughput acceleration for table scans, aggregations, and analytical filters.

#### 6.9. Wasm Runtime Sandbox UDF (`pkg/extension`, `wazero`)
- **Challenge**: Embed pure-Go `wazero` to execute dynamically loaded WebAssembly functions (`CREATE FUNCTION ... AS WASM`) in isolated memory sandboxes.
- **Expected Outcome**: Safe in-engine execution of third-party plugins with zero host crash risk.

#### 6.10. Local & S3-Compatible File Storage Module (`pkg/api/handlers_files.go`)
- **Challenge**: Implement multipart file upload APIs (`POST /api/files/:table/:id`), static asset serving, asynchronous image thumbnail generation, and storage quotas.
- **Expected Outcome**: Turnkey all-in-one BaaS storage capability directly inside the single standalone binary.

#### 6.11. Lazy Tuple Materialization & Column Projection Pushdown (`pkg/storage`, `pkg/execution`) (Completed)
- **Implemented (`P-2`)**: Selective decoding `DecodeTupleSelected`, zero-allocation `SkipTupleColumn`, and $O(1)$ offset caching `LazyTuple`, combined with covering index `INCLUDE(...)` payload storage to eliminate 100% of table heap lookups via Index-Only Scans.
- **Outcome**: 70~80% reduction in CPU cycles and GC heap allocations during large-scale table scans.

#### 6.12. Per-Query Arena Memory Allocator & GC Elimination (`pkg/execution`, `pkg/storage`)
- **Challenge**: Allocate dedicated memory arena slabs tied to the lifecycle of individual analytical queries and transactions, reclaiming memory with a single pointer reset.
- **Expected Outcome**: Complete elimination of Go runtime GC pauses and flat p99/p99.9 latency during heavy analytical queries.

---

## 4. Pending Task Matrix

| Milestone | Priority | Task Description | Domain | Estimated Effort | Strategic Benefit |
| :---: | :---: | :--- | :---: | :---: | :--- |
| **Phase 5** | **P2** | **Transparent Data Encryption (TDE: AES-256-GCM)** | Storage & Security | Medium (250~350 lines) | Transparent data-at-rest encryption and immutable audit logging |
| **Phase 5** | **P2** | **`INSTEAD OF` Trigger Runtime Interceptor** | T-SQL Runtime | Medium (200~300 lines) | Virtual entity DML update routing on complex views |
| **Phase 5** | **P3** | **[Completed] Declarative Schema Migration CLI** | BaaS & Tooling | Medium (250~350 lines) | `lionsql migrate` atomic DDL version history tracking |
| **Phase 5** | **P3** | **[Completed] OAuth2 Social Login (Google, GitHub)** | Auth & API | Medium (200~300 lines) | Turnkey authentication, user auto-provisioning, and JWT issuance |
| **Phase 6** | **P1** | **[Completed] Always On Style Automatic Failover** | HA Replication | High (400~500 lines) | Automatic promotion of lowest-lag replica, zero downtime |
| **Phase 6** | **P1** | **Per-Query Arena Memory Allocator** | Memory Governor | Medium (300~400 lines) | Eliminates Go GC pauses during large joins/sorts/aggregations |
| **Phase 6** | **P2** | **Transactional Outbox Broker Dispatcher (Kafka/RabbitMQ)** | Storage & Events | Medium (250~350 lines) | Reliable outbox forwarding to external brokers (Core CDC streaming complete) |
| **Phase 6** | **P2** | **[Completed] MCV (Most Common Values) Skew List Statistics (`C-7`)** | CBO & Planner | Medium (250~350 lines) | Corrects non-uniform skew distribution estimation error (MCV + Histogram complete) |
| **Phase 6** | **P3** | **Multi-Language Client Harness (Node.js & .NET)** | Wire Protocols | Medium (200~300 lines) | Automated integration suites for tedious, pg, and SqlClient (Python complete) |
| **Phase 6** | **P3** | **[Postponed] Pure-Go Persistent Engine Backend (Pebble/bbolt)** | Storage Backend | High (500~700 lines) | 100% static standalone executable (`CGO_ENABLED=0`) distribution |
| **Phase 6** | **P3** | **Wasm Runtime Sandbox UDF (`wazero`)** | Plugins & Sandbox | Medium (300~400 lines) | Secure, crash-isolated in-engine execution of third-party plugins |
| **Phase 6** | **P3** | **Local & S3-Compatible File Storage & Thumbnails** | BaaS Storage | Medium (250~350 lines) | Multipart file upload and automatic image thumbnail generation |

