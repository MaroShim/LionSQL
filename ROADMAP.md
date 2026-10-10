# SQLion Engineering Roadmap & Core Technical Milestones

This document charts the development milestones, verified implementation status, and future engineering challenges for the SQLion high-concurrency database engine.
(한국어 공식 문서는 [ROADMAP.ko.md](ROADMAP.ko.md)를 참조하세요.)

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

[ Phase 5: Enterprise Governance, Security & Engine Decoupling (v0.16.0 - v0.18.0) ] ──► NEXT IN PROGRESS
  - VDBE Instruction Struct 16-Byte Compaction (4 instructions per 64B L1 Cache Line)
  - SQL Execution Engine Coordinator Package Separation (pkg/api -> pkg/engine)
  - Prepared Statement & PlanCache Memory Defense Bounded LRU Cache (Golden Rule 29)
  - Granular SQL Object-Level RBAC (CREATE ROLE/USER, GRANT/REVOKE SELECT/DML)
  - Transparent Data Encryption (TDE: AES-256-GCM) & Enterprise Immutable Audit Log
  - TDS NBCROW (0xD2) Row Compression
  - CBO Sort-Merge Join Operator & Cost Modeling (MergeJoinIterator)
  - INSTEAD OF Trigger Runtime Interceptor (View / Table DML Substitution)
  - BaaS Expansion: Declarative Schema Migration CLI (sqlion migrate) & OAuth2 Social Login

[ Phase 6: High Availability Always On, Decoupled HTAP & Production Hardening (v1.0.0 LTS) ] ──► PLANNED
  - Always On Style Automatic Failover & Leader Election
  - Transactional Outbox & CDC Event Streaming (Kafka / RabbitMQ)
  - Shared Memory (POSIX shm / mmap) Apache Arrow IPC Snapshots (DuckDB Zero-Copy)
  - Pure Go Persistent Storage Engine Backend (Pebble / bbolt, CGO_ENABLED=0)
  - Dynamic Programming (DP / Selinger) Large Join Order Optimization (N > 4)
  - Extended Multi-Column Statistics, MCV & Optimizer Hints
  - T-SQL Specialized TDS Serialization (UNIQUEIDENTIFIER, DATETIME2, PLP LOB)
  - Multi-Language Client Certification (Python psycopg3/pymssql, Node.js pg/tedious, .NET SqlClient)
  - Volcano Chunk-at-a-Time Vectorized Batch Streaming (1,024-tuple vectors)
  - Background MVCC Vacuuming & Undo Log Version Compaction
  - Wasm Runtime Sandbox UDF (wazero)
  - Local & S3-Compatible File Storage Module & Thumbnail Resizing
```

---

## 2. Verified Implementation Status (as of v0.15.0)

### 2.1. Core Execution Engine
- [x] **VDBE Bytecode Virtual Machine (`pkg/proc/runtime`)**: SQLite-style 3-address register virtual machine with `sync.Pool` register slice recycling, executing stored procedures, functions, triggers, cursors, and interactive queries.
- [x] **VDBE Local Variable Slot Indexing (`pkg/proc`, `pkg/proc/runtime`)**: Compile-time variable slot allocation (`Program.VarSlots`) and runtime $O(1)$ direct slice indexing (`VMFrame.slots[inst.B]`), eliminating string hashing, case folding, and map lookup overhead on hot execution paths.
- [x] **VDBE Integer Opcode Specialization & Dedicated Logical Operators (`pkg/proc`, `pkg/proc/runtime`)**: Specialized integer arithmetic and comparison opcodes (`OpAddInt` ~ `OpGteInt`) with direct ALU execution, plus first-class `OpAnd` and `OpOr` opcodes eliminating runtime dynamic interface string comparisons.
- [x] **VDBE Fused Compare-and-Branch & Peephole Jump Optimization (`pkg/proc`, `pkg/proc/runtime`)**: Fused compare-and-branch instructions (`OpJumpIfEqInt`, etc.), recursive trampoline jump threading, and adjacent fall-through jump elimination, cutting condition instruction counts by 50%.
- [x] **VDBE Opcode Dispatcher Modularization (`pkg/proc/runtime/vm_dispatcher.go`)**: Decomposed the monolithic 800+ line switch statement into domain-specific execution helpers (`execFlowOp`, `execComputeOp`, `execTxOp`, `execStorageOp`, `execCursorAndCallOp`, `execRelOp`) and centralized error catch-stack unwinding (`handleCatch`).
- [x] **ANSI & MSSQL Standard Pagination Syntax (`pkg/proc`, `pkg/planner`, `pkg/execution`)**: Full AST parsing and execution integration for `OFFSET <O> ROWS FETCH NEXT <N> ROWS ONLY` and `SELECT TOP <N>`, integrated with Volcano `LimitIterator(limit, offset)`.
- [x] **VDBE `OpRelQuery` Physical Plan Caching (`pkg/proc`, `pkg/proc/runtime`, `pkg/planner`)**: AST parsing (`CachedQueryInfo`) and physical plan tree (`CachedPlan`) deep-clone caching with `sync.RWMutex` thread safety.
- [x] **In-Process Go Native Scalar UDF Registration SPI (`pkg/proc`, `pkg/api`)**: Thread-safe `proc.RegisterScalarFunction(name, fn)` SPI delivering zero-overhead, sub-microsecond in-process execution with built-in `--doctor` diagnostic probe.
- [x] **0-Allocation Row Streaming (`Tuple.ToRow()`) (`pkg/execution`)**: Direct low-level `storage.Value` slice conversion bypassing intermediate `map[string]any` heap allocation per row, cutting GC pressure by 70%.
- [x] **Volcano Iterator Relational Execution Pipeline (`pkg/execution`)**: Full streaming iterator engine (`SeqScan`, `IndexScan`, `IndexOnlyScan`, `Filter`, `Project`, `Sort`, `Limit`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`, `CTE`).
- [x] **Window Functions & Frame Analytic Engine (`pkg/execution/window.go`)**: Support for `OVER (PARTITION BY ... ORDER BY ... [ROWS BETWEEN])`, `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`, and aggregate window functions (`SUM`, `AVG`, `COUNT`, `MIN`, `MAX`).
- [x] **Built-in In-Engine Log Extraction Operators (`pkg/proc/builtins.go`)**: In-memory regex filtering `GREP(text, pattern)` / `REGEXP_LIKE` and field extraction `AWK(text, [delim], idx)` / `SPLIT_PART`.
- [x] **T-Digest Exact & Streaming Quantile Engine (`pkg/execution/tdigest.go`)**: High-accuracy tail percentile estimation with bounded $O(1)$ memory (`PERCENTILE_CONT`, `PERCENTILE_DISC`).
- [x] **Cooperative Query Cancellation (`pkg/proc/runtime`, `pkg/api`, `pkg/wire`)**: Non-destructive `context.Context` abort pipeline, T-SQL `KILL <spid>`, PostgreSQL `pg_cancel_backend()`, and wire `CancelRequest`.

### 2.2. Query Optimizer & CBO
- [x] **Cost-Based Optimizer (CBO) & Statistics Catalog (`pkg/planner/stats.go`)**: `ANALYZE TABLE` 64-bucket equi-depth histograms, cardinality, distinct values (NDV), null fractions, and automatic cost modeling (Hash Join vs Index Nested Loop Join).
- [x] **`ANALYZE TABLE` Multi-Core Parallel Statistics Collection (`pkg/planner/stats.go`)**: Parallelized column statistics gathering via `sync.WaitGroup` multi-core workers (`BuildTableStatsParallel`), accelerating analysis by 4~8x.
- [x] **CBO Statistics Disk Persistence (`pkg/catalog`, `cbo_stats.json`)**: Atomic file writes (Golden Rules 1 & 5) and lossless `storage.Decimal` JSON serialization, preserving statistics and optimized plans across server restarts.
- [x] **Compound Selectivity Estimation (`pkg/planner/stats.go`)**: Combined predicate selectivity computation based on probabilistic independence and dynamic live row counts.
- [x] **Predicate Pushdown (`pkg/planner/pushdown.go`)**: Complex filter conjunct pushdown to lower scan iterators before join materialization.
- [x] **Constant Folding & Boolean Simplification (`pkg/proc/fold.go`)**: Compile-time constant arithmetic, pre-computed scalar functions, and tautology/contradiction short-circuit reduction.
- [x] **Subquery Decorrelation (`pkg/planner/decorrelate.go`)**: Automatic rewrite of correlated `EXISTS`, `NOT EXISTS`, `IN`, and `NOT IN` predicates into relational `SEMI` / `ANTI` joins.

### 2.3. Storage, Transactions & Replication
- [x] **1-Writer Multi-Reader WAL Streaming Replication & HA (`pkg/replication`, `cmd/server`)**: Primary-to-replica live TCP WAL log shipping, replica read-only enforcement (`ErrReadOnlyReplica`), and zero-downtime promotion.
- [x] **Dual Storage Engine Architecture (`pkg/storage`)**: Production disk-based WiredTiger (Cgo, lock-free B+Tree, MVCC) and pure-Go In-Memory OCC engine (MessagePack, WAL/AOF durability).
- [x] **Covering Index Scan (Index-Only Scan) (`pkg/planner`, `pkg/execution`, `pkg/storage/memengine`)**: Zero-heap-fetch relational execution when projections are covered by index keys and PKs (`NodeIndexOnlyScan` and `SkipList.ScanRangeEntries` synthesized records, commit `4f69eb6`).
- [x] **IETF RFC 9562 Standard UUIDv7 (`pkg/storage`, `pkg/proc`)**: 48-bit monotonically increasing Unix Epoch timestamps for sequential B+Tree inserts, with `NEWID7()`, `UUIDV7()`, `GEN_RANDOM_UUID(7)` built-ins, and `DEFAULT NEWID7()` table PK defaults.
- [x] **ClickHouse-Style Rolling Range Table Partitioning (`pkg/storage`, `pkg/planner`, `pkg/api`)**: Automatic rolling partitions via `PARTITION BY toYYYYMM(col)`, `MONTH(col)`, partition pruning, and $O(1)$ fast partition dropping (`DROP PARTITION`).
- [x] **In-Memory Ephemeral Engine Mode (`cmd/server`, `pkg/storage/memengine`)**: `--ephemeral` flag disabling all disk logging for blazing-fast, RAM-only execution.
- [x] **Integer Fast-Path in `storage.Value` (`pkg/storage/engine.go`)**: Fast comparison, arithmetic dispatch, and zero-allocation `strconv.FormatInt` formatting for `TypeInt`.
- [x] **Transaction Savepoints (`pkg/api/savepoint_test.go`, `pkg/storage`)**: T-SQL `SAVE TRANSACTION` / ANSI `SAVEPOINT`, partial rollback `ROLLBACK TO SAVEPOINT`, and `RELEASE SAVEPOINT`.
- [x] **Lock-Free Concurrent SkipList Index (`pkg/storage/memengine/skiplist.go`)**: Secondary index engine powered by concurrent SkipLists ($O(\log N)$, 2.8M inserts/sec).
- [x] **Query Memory Limit Governor (`work_mem`) (`pkg/execution/spill.go`)**: Automatic fallback to external merge sort on temporary disk files when memory thresholds are exceeded.
- [x] **Online Non-Blocking Hot Snapshot Backup (`pkg/backup`)**: Consistent point-in-time `.tar.gz` archive generation under read-only snapshot isolation (`POST /api/backup`) without write lock pauses.
- [x] **Foreign Key Integrity Constraints (`pkg/catalog`)**: Reference integrity enforcement with `ON DELETE/UPDATE CASCADE`, `SET NULL`, and `RESTRICT`.

### 2.4. Wire Protocols & Tool Interoperability
- [x] **PostgreSQL Modern Driver Compatibility: `pgx/v5` (`pkg/wire`)**: Full pass across `github.com/jackc/pgx/v5` test suite, binary parameter/result format decoding, and Extended Query `Flush` batch pipelining.
- [x] **Official Microsoft MSSQL Driver E2E Compatibility (`pkg/wire/tds`)**: 100% pass across `github.com/microsoft/go-mssqldb`, `PacketTransactionManager` (0x0E), Collation/Transaction `ENVCHANGE` tokens, and `DoneInXact` transaction state machine.
- [x] **Official PostgreSQL lib/pq Driver Compatibility (`pkg/wire`)**: 100% pass across `github.com/lib/pq`, dynamic parameter count inference, projection schema matching, binary result formats, and interactive transaction tagging.
- [x] **TDS Asynchronous Attention Query Cancellation (`pkg/wire/tds`)**: Instant response to client `SqlCommand.Cancel()` via `Type 0x06` attention packet decoupling, VDBE loop cancellation (`context.CancelFunc`), and `DoneAttn (0x0020)` acknowledgments.
- [x] **Developer Real-Time Wire Query Tracing (`-dev` flag) (`pkg/wire`, `pkg/wire/tds`)**: Real-time console logging of incoming queries, bound parameters, execution duration, and error traces across PGWire and TDS.
- [x] **GUI Client Tool Interoperability (DbGate, Beekeeper Studio, DBeaver) (`pkg/wire/tds`, `pkg/api`)**: Virtual system catalog joins, `@@VERSION` / `SERVERPROPERTY` edition detection interceptors, and deterministic object ID assignment.
- [x] **Dual Wire Protocol Listeners (`pkg/wire`, `pkg/wire/tds`)**: Concurrent listening on PostgreSQL Wire Protocol v3 (`:5433`) and Microsoft SQL Server TDS 7.x/8.0 (`:1433`) with TLS encryption.
- [x] **Packet Buffer Pooling (`sync.Pool`) (`pkg/wire/protocol.go`, `pkg/wire/tds/token.go`)**: 4KB PGWire packet buffers and TDS row token buffers recycled across streaming loops.
- [x] **SQL Prepared Statements (`pkg/api/sqlexec_prepare.go`)**: `PREPARE`, `EXECUTE`, `DEALLOCATE` statements, placeholder normalization (`$1, ?` -> `@p1`), and VDBE PlanCache coupling.
- [x] **PostgreSQL High-Speed Bulk Copy (`pkg/wire/copy.go`)**: `COPY FROM STDIN` and `COPY TO STDOUT` streaming protocols (CSV and text formats).
- [x] **Embedded Web Management Studio (`pkg/ui`)**: Standalone web UI on `:8090/_/`, interactive query console, spreadsheet CRUD, and visual VDBE disassembler (`EXPLAIN BYTECODE`).

---

## 3. Future Technical Milestones

### Phase 5: Enterprise Governance, Security & Engine Decoupling (v0.16.0 - v0.18.0 Target)

#### 5.1. [COMPLETED] VDBE Instruction Struct 16-Byte Compaction (`pkg/proc/bytecode.go`)
- **Implemented**: Re-architected `Instruction` into a dense 16-byte fixed layout (`Op uint8, Flags uint8, Aux uint16, A int32, B int32, C int32`) with zero struct padding, externalizing metadata objects to `Program.AuxData []any` indexed via `inst.Aux`.
- **Verified**: Confirmed `unsafe.Sizeof == 16` via `TestInstruction_Size16Bytes`, loading exactly 4 instructions per 64-byte L1 Data Cache line.

#### 5.2. Architecture Layering: SQL Execution Coordinator Separation (`pkg/api` -> `pkg/engine`)
- **Current State**: `pkg/api` contains over 15,000 lines of core SQL execution orchestration (`sqlexec_*.go`), causing an architectural inversion where PGWire and TDS depend on the HTTP API layer.
- **Challenge**:
  - Promote SQL execution orchestration into a dedicated `pkg/engine` core layer.
  - Establish a clean unidirectional architecture where `pkg/api`, `pkg/wire`, and `pkg/wire/tds` invoke the engine interface uniformly.
- **Expected Outcome**: Reduced cyclomatic complexity, improved separation of concerns, and clean testability for all wire protocol adapters.

#### 5.3. Granular SQL Object-Level RBAC (Role-Based Access Control) (`pkg/catalog`, `pkg/api`, `pkg/wire`)
- **Current State**: Access control is currently based on a single superuser and API key authentication; granular database-level permissions are missing.
- **Challenge**:
  - Implement SQL standard role and user DDL: `CREATE ROLE <role>`, `CREATE USER <user> WITH PASSWORD = '...'`, `DROP ROLE`.
  - Object-level privilege management: `GRANT SELECT, INSERT, UPDATE, DELETE ON <table> TO <role/user>`, `REVOKE`, `DENY`.
  - Pre-execution permission verification via catalog interceptors with session user context binding.
- **Expected Outcome**: Compliance with enterprise internal control and regulatory standards (ISMS, financial security regulations).

#### 5.4. Transparent Data Encryption (TDE) & Enterprise Immutable Audit Log (`pkg/storage`, `pkg/logger`)
- **Challenge**:
  - Encrypt WiredTiger data files, WAL/AOF journals, and backup archives at rest using AES-256-GCM (Data at Rest Encryption).
  - Implement tamper-evident immutable audit logging for authentication events, privilege changes (`GRANT/REVOKE`), and schema alterations.
- **Expected Outcome**: Complete defense against direct physical disk extraction and full enterprise audit traceability.

#### 5.5. [COMPLETED] PlanCache Bounded LRU Cache & Memory Defense (Golden Rule 29) (`pkg/proc/runtime`)
- **Implemented**: Replaced unbounded map storage in `PlanCache` with a bounded Least Recently Used (`container/list`) cache with `DefaultPlanCacheCapacity = 1024`.
- **Verified**: Added deterministic LRU eviction on capacity saturation, thread-safe access promotion, case-insensitive symbol lookup, and 100% PASS on `plancache_test.go`. Enforces bounded resource discipline and mitigates OOM risks during long-running client workloads.

#### 5.6. TDS Wire Protocol NBCROW Row Compression (`pkg/wire/tds`)
- **Challenge**: Implement TDS 7.3+ standard `NBCROW` (Null Bitmap Compressed Row, `0xD2`) encoding, sending NULL column values with 0 payload bytes.
- **Expected Outcome**: 30%+ reduction in network bandwidth consumption for enterprise queries returning sparse tables.

#### 5.7. CBO Sort-Merge Join Operator & Cost Modeling (`pkg/execution`, `pkg/planner`)
- **Challenge**:
  - Implement a $O(M+N)$ single-pass `MergeJoinIterator` for pre-sorted inputs (index order or `ORDER BY`).
  - Add sort-merge join cost models to the CBO plan selector.
- **Expected Outcome**: Elimination of hash table materialization overhead and dramatic speedups for pre-sorted relations.

#### 5.8. `INSTEAD OF` Trigger Runtime Interceptor (`pkg/proc/runtime`, `pkg/api`)
- **Challenge**: Intercept `INSERT`, `UPDATE`, and `DELETE` operations on views and complex tables, substituting them with user-defined procedural routines.
- **Expected Outcome**: Virtual entity update capability on complex views and encapsulated business integrity enforcement.

#### 5.9. BaaS Developer Productivity: Migration CLI & OAuth2 Social Login (`cmd/server`, `pkg/migrate`, `pkg/api`)
- **Challenge**:
  - Declarative schema migration CLI: `sqlion migrate new <name>`, `sqlion migrate up / down`, and `_migrations` version history tracking.
  - Social login: Built-in Google and GitHub OAuth2 endpoints (`/api/auth/oauth2/callback`) with automatic user provisioning and JWT issuance.
- **Expected Outcome**: PocketBase-class developer productivity and turnkey deployment workflows.

---

### Phase 6: High Availability Always On, Decoupled HTAP & Production Hardening (v1.0.0 LTS Target)

#### 6.1. Always On Style Automatic Failover & Leader Election (`pkg/replication`)
- **Current State**: 1-Writer Multi-Reader WAL streaming and manual failover promotion are supported, but automatic leader election is required upon primary failure.
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
- **Expected Outcome**: Decoupled hybrid transactional/analytical processing (HTAP) integrating the best-in-class open-source analytical ecosystem with 0ms transfer latency.

#### 6.4. Pure-Go Persistent Storage Engine Option (Pebble / bbolt) (`pkg/storage`)
- **Challenge**: Add a pure-Go persistent storage backend under the `storage.Engine` interface, enabling static compilation (`CGO_ENABLED=0`, Golden Rule 86) without C compiler toolchains.
- **Expected Outcome**: 100% autonomous, cross-platform static binary distribution across any Linux/Windows/macOS environment.

#### 6.5. Dynamic Programming (DP / Selinger) Large Join Order Optimization ($N > 4$) (`pkg/planner`)
- **Challenge**: Implement a DPccp dynamic programming search algorithm evaluating left-deep and bushy join trees for complex queries joining 5 or more tables.
- **Expected Outcome**: Optimal execution plans for complex enterprise ERP and data mart join queries without exponential search explosions.

#### 6.6. Extended Multi-Column Statistics, MCV & Optimizer Hints (`pkg/planner`)
- **Challenge**:
  - Collect multi-column distinct value (NDV) statistics to correct cardinality underestimation on correlated columns.
  - Implement Most Common Values (MCV) lists alongside histograms for skewed distributions.
  - Support optimizer hint overrides: `WITH (INDEX(...))`, `OPTION (HASH JOIN)`, `OPTION (FORCE ORDER)`.
- **Expected Outcome**: Elimination of estimation skew and deterministic plan control for database administrators.

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
- **Expected Outcome**: Prevention of memory fragmentation and unbounded growth in 24/7 long-running in-memory workloads.

#### 6.11. Wasm Runtime Sandbox UDF (`pkg/extension`, `wazero`)
- **Challenge**: Embed the pure-Go `wazero` runtime to execute dynamically loaded WebAssembly functions (`CREATE FUNCTION ... AS WASM`) in isolated memory sandboxes (Golden Rule 26).
- **Expected Outcome**: Safe in-engine execution of third-party plugins with zero host crash risk.

#### 6.12. Local & S3-Compatible File Storage Module (`pkg/api/handlers_files.go`)
- **Challenge**: Implement multipart file upload APIs (`POST /api/files/:table/:id`), static asset serving, asynchronous image thumbnail generation, and storage quotas.
- **Expected Outcome**: Turnkey all-in-one BaaS storage capability directly inside the single standalone binary.

---

## 4. Prioritized Engineering Matrix

| Milestone | Priority | Task Description | Domain | Estimated Effort | Strategic Benefit |
| :---: | :---: | :--- | :---: | :---: | :--- |
| **Phase 5** | **P1** | **VDBE Instruction Struct 16-Byte Compaction** | Virtual Machine | Low (150~200 lines) | Fits 4 instructions per 64B L1 Cache Line, reduces VM latency |
| **Phase 5** | **P1** | **SQL Execution Coordinator Separation (`pkg/engine`)** | Architecture | Medium (400~500 lines) | Unidirectional clean architecture, breaks API-to-wire layer inversion |
| **Phase 5** | **P1** | **Granular SQL Object-Level RBAC** | Security & Catalog | Medium (300~400 lines) | `CREATE ROLE/USER`, `GRANT/REVOKE` enterprise access control |
| **Phase 5** | **P1** | **Transparent Data Encryption (TDE: AES-256-GCM)** | Storage & Security | Medium (250~350 lines) | Transparent data-at-rest encryption and immutable audit logging |
| **Phase 5** | **P2** | **Prepared Statement Bounded LRU Cache** | Memory Governor | Low (100~150 lines) | Rule 29 compliance, prevents unbounded map growth on long-lived connections |
| **Phase 5** | **P2** | **CBO Sort-Merge Join Operator** | Executor & Planner | Medium (250~350 lines) | Eliminates hash table overhead for pre-sorted inputs |
| **Phase 5** | **P2** | **TDS NBCROW Row Compression (0xD2)** | TDS Protocol | Medium (200~300 lines) | 30%+ network bandwidth savings on sparse/NULL-heavy tables |
| **Phase 5** | **P2** | **`INSTEAD OF` Trigger Runtime Interceptor** | T-SQL Runtime | Medium (200~300 lines) | Virtual entity DML update routing on complex views |
| **Phase 5** | **P3** | **Declarative Schema Migration CLI** | BaaS & Tooling | Medium (250~350 lines) | `sqlion migrate` atomic DDL version history tracking |
| **Phase 5** | **P3** | **OAuth2 Social Login (Google, GitHub)** | Auth & API | Medium (200~300 lines) | Turnkey authentication and JWT provisioning for web/mobile apps |
| **Phase 6** | **P1** | **Always On Style Automatic Failover** | HA Replication | High (400~500 lines) | Automatic promotion of lowest-lag replica, zero-downtime availability |
| **Phase 6** | **P1** | **Transactional Outbox & CDC Streaming** | Storage & Events | Medium (300~400 lines) | Atomic row-level change event streams dispatched to Kafka/RabbitMQ |
| **Phase 6** | **P1** | **Shared-Memory Apache Arrow IPC Snapshots (DuckDB)** | Analytics & HTAP | Medium (300~400 lines) | Zero-copy, zero-I/O decoupled OLAP engine integration |
| **Phase 6** | **P1** | **Pure-Go Persistent Engine Backend (Pebble/bbolt)** | Storage Backend | High (500~700 lines) | 100% static standalone executable (`CGO_ENABLED=0`) distribution |
| **Phase 6** | **P2** | **$N > 4$ Join Dynamic Programming (DP / Selinger)** | CBO & Planner | High (400~600 lines) | Optimal join ordering for complex multi-table queries |
| **Phase 6** | **P2** | **Multi-Column Statistics, MCV & Optimizer Hints** | CBO & Planner | Medium (300~400 lines) | Corrects cardinality estimation skew, guarantees DBA execution plan control |
| **Phase 6** | **P2** | **T-SQL Specialized TDS Serialization & PLP LOB** | TDS Protocol | Medium (300~400 lines) | Complete binary support for UNIQUEIDENTIFIER, DATETIME2, and large LOBs |
| **Phase 6** | **P2** | **Volcano Chunked Batch Execution (Vectorized)** | Executor & SIMD | High (600~800 lines) | 3~5x acceleration for table scans and aggregate filters |
| **Phase 6** | **P3** | **Background MVCC Vacuum / Version GC** | Concurrency & RAM | High (400~500 lines) | Eliminates memory fragmentation in 24/7 long-running in-memory tables |
| **Phase 6** | **P3** | **Wasm Runtime Sandbox UDF (`wazero`)** | Plugins & Sandbox | Medium (300~400 lines) | Secure, crash-isolated in-engine execution of third-party plugins |
| **Phase 6** | **P3** | **Multi-Language Client Certification Harness** | Wire Protocols | Medium (200~300 lines) | Automated integration suites for Python, Node.js, and .NET drivers |
| **Phase 6** | **P3** | **Local & S3-Compatible File Storage & Thumbnails** | BaaS Storage | Medium (250~350 lines) | Multipart file upload and automatic image thumbnail generation |
