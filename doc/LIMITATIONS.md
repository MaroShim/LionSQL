# LionSQL Official Limits & Known Issues Specification
`doc/LIMITATIONS.md`

> - **Initial Date**: 2026-11-22
> - **Target Version**: LionSQL v0.94.0+ / 1.0-GA
> - **Purpose**: To provide engineering teams with an unvarnished, authoritative reference on physical/logical quotas, unsupported SQL constructs, concurrency trade-offs, engine constraints, and roadmap items for evaluating production adoption.
> - **Linked Invariants**: `GEMINI.md` [Invariant 2, 3, 4, 5, 7, 10, 13, 15] and `doc/SPEC_OUT_DECISIONS.ko.md`
> - [Korean Version](LIMITATIONS.ko.md)

---

## 1. Executive Evaluation Guide

LionSQL is engineered as a **single-binary, high-concurrency embedded OLTP database engine**. To deliver 20,000 ~ 34,000+ TPS on a single node and sub-microsecond T-SQL stored procedure execution, we deliberately exclude distributed 2PC coordinators and legacy enterprise bloat (`[RULE:RadicalKISS]`).

Engineering teams must understand these architectural trade-offs when evaluating LionSQL.

### 1.1 Recommended Workloads
- **High-Frequency OLTP Microservices**: Order processing, payment gateways, gaming backends, and session stores where single-node throughput matters.
- **T-SQL Stored Procedure Migrations**: Migrating complex SQL Server logic to a lightweight, standalone Go binary without enterprise licensing overhead.
- **Embedded & Edge Computing**: Edge nodes requiring wire listeners (PGWire/TDS), HTTP REST APIs, and Web Studio in a single `.exe` or ELF executable without Redis, PgBouncer, or Nginx sidecars.
- **Hybrid Wire Protocol Integration**: Environments concurrently connecting PostgreSQL clients (psql, Prisma, DBeaver) and TDS clients (SSMS, ADO.NET) to the same underlying data store.

### 1.2 When NOT to Use LionSQL
- **Petabyte-Scale Distributed DW / OLAP**: Workloads requiring distributed shuffle joins across dozens of compute nodes (ClickHouse, DuckDB recommended).
- **Multi-Master Active-Active Distributed Writing**: Globally distributed consensus-based 2PC replication (CockroachDB, Google Spanner recommended).
- **Legacy XML DML / XQuery Workloads**: Applications heavily reliant on legacy XML DML instead of JSON.

---

## 2. Capacity & Storage Limits

| Item | Official Limit | Enforcement & Error | Architectural Notes |
| :--- | :--- | :--- | :--- |
| **Max Database Size** | **16 TB** (WiredTiger recommended)<br>Physical RAM Limit (In-Memory OCC) | OS filesystem bounds | 64-bit B+Tree. In-memory engine bounded by physical RAM and `-max-memory`. |
| **Max Rows per Table** | Practically Unlimited ($2^{64}-1$) | - | 64-bit integer Record IDs and B+Tree indexes. |
| **Max Tables** | Practically Unlimited | - | Unified catalog metadata. Zero file descriptor leaks. |
| **Max Indexes per Table** | **64 Recommended** (Technically unlimited) | `ARCH-WT-FILE-LAYOUT-1` | Multiplexed into a single `_idx_<Table>.wt` file per table (`Invariant 15`). |
| **Max Index Key Size** | **1,700 Bytes** | `54000` / Msg 1946<br>`ErrIndexKeyTooLarge` | Guarantees B+Tree leaf node fan-out and search efficiency. |
| **Max Columns per Index** | **32 Columns** | `54011` / Msg 1904<br>`ErrTooManyIndexColumns` | Composite index column limit. |
| **Physical Multi-DB Separation** | **Single Storage Kernel per Process** | `ARCH-LOGICAL-NAMESPACE-1`<br>(Invariant 2) | Physical multi-DB partitioning is spec-out. `USE <dbname>` maps logically via composite keys (`__db_<dbname>_<schema>_<table>`). |

---

## 3. Column, Row & Identifier Limits

In accordance with `[Invariant 13: Single Canonical V2 Binary Tuple Codec]` and SQL Server physical specifications:

| Item | Official Limit | Enforcement Layer | Error Code (SQLSTATE / Msg) |
| :--- | :--- | :--- | :--- |
| **Max Columns per Table** | **1,024 Columns** | DDL & Tuple Codec | `54011` / Msg 1702 (`ErrTooManyColumns`) |
| **Max Inline Fixed Row Size** | **8,060 Bytes** | DDL (`CREATE`/`ALTER`) | `54000` / Msg 511 (`ErrInlineRowTooLarge`) |
| **Max Total Record Size** | **16 MB** | DML (`INSERT`/`UPDATE`) | `54000` / Msg 511 (`ErrRowTooLarge`) |
| **Max Single LOB Column** | **16 MB** | Storage Encoder | Applies to `VARCHAR(MAX)`, `NVARCHAR(MAX)`, `VARBINARY(MAX)`, `JSON`. |
| **Max Identifier Length** | **UTF-8 128 Characters** | DDL Parser & Catalog | `42622` / Msg 103 (`ErrIdentifierTooLong`) |
| **DROP COLUMN Constraint** | **Tail Columns Only**<br>(Middle DROP Fail-Closed) | DDL Executor | `54000` / `ErrDropColumnNotTail` |

> [!IMPORTANT]
> **Middle DROP COLUMN Invariant & Best Practice (`Invariant 13`)**:
> Dropping non-terminal (middle) columns on populated tables is an operational anti-pattern that impairs online availability. To prevent locking stalls or decoding overhead from logical skip masks, LionSQL rejects middle-column drops with `ErrDropColumnNotTail`. Use the industry-standard **Copy-and-Switch** pattern (`CREATE TABLE new AS SELECT ...` followed by atomic rename).

---

## 4. Connections, Sessions & Resource Quotas

Per `[RULE:BoundedBuffers]`, explicit limits prevent resource exhaustion and goroutine explosion:

| Item | Default Value | CLI Flag / Range | Behavior on Exceeding |
| :--- | :--- | :--- | :--- |
| **Max Client Connections** | **200** (Concurrent) | `-max-connections` (up to 10,000) | Connection rejected with SQLSTATE `53300`. Prevents FD exhaustion. |
| **Max Query Workers** | **`NumCPU * 4`** | `-max-workers` | Caps goroutine scheduling overhead and preserves L1/L2 cache locality. |
| **Idle Session Pool** | **64 Sessions** | `-max-idle-sessions` | Sessions exceeding pool cap are closed immediately. |
| **Idle Tx Timeout** | **5 minutes (`5m`)** | `-tx-idle-timeout` (sweep 10s) | Automatically rolls back abandoned transactions (`25P03`). |
| **Global Heap Memory Budget** | **65% of RAM** | `-max-memory` (e.g. `4096MB`) | Monitored heap budget. Safely rejects operations on exhaustion. |
| **Operator Work Memory** | **64 MB** | `-work-mem` | Budget for hash joins, aggregations, and subquery materialization. |
| **In-Memory Undo Stack** | **100 Operations** | Fixed limit | Capped transactional DDL undo operations. |
| **Subquery HashSet Buffer** | **10,000 Rows** | Fixed limit (`SubqueryHashSet`) | In-memory materialization cap for uncorrelated `IN` subqueries. |
| **Prepared Statements per Session** | **256 Statements** | LRU Eviction / Error | Prevents session cache memory leaks in connection poolers. |

---

## 5. SQL Dialect Limitations & Spec-Out Decisions

To avoid accidental complexity, several constructs are restricted or permanently Spec-Out (`doc/SPEC_OUT_DECISIONS.ko.md`):

### 5.1 Unsupported or Restricted SQL Constructs
- **`FULL OUTER JOIN`**: Currently unsupported. Use `LEFT JOIN ... UNION ... RIGHT JOIN` instead. `INNER`, `LEFT`, `RIGHT`, and `CROSS` joins are fully supported.
- **Window Framing (`WINDOW FRAMING`)**: `OVER (PARTITION BY ... ORDER BY ...)` is supported with cumulative framing (`ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`). Complex dynamic `RANGE` offsets are restricted.
- **`MERGE` Statement**: Basic T-SQL `MERGE` forms are supported; multi-branch complex subquery predicates are restricted (use explicit `INSERT`/`UPDATE` statements).
- **Recursive CTE**: Single-level CTEs (`WITH cte AS (...)`) are supported. Self-referential recursive CTEs have bounded recursion depth (`[RULE:BoundedRecursion]`).

### 5.2 Permanently Spec-Out Items
1. **No Duplicate PL/pgSQL Interpreter**: All procedures run through the native T-SQL VDBE bytecode VM (`pkg/proc`). PL/pgSQL syntax is not embedded.
2. **No Legacy XML DML / XQuery**: Modern JSON functions (`JSON_VALUE`, `OPENJSON`) are standardized; legacy XML parsers are permanently excluded.
3. **No In-Memory OCC Background Vacuum**: OCC uses private workspace swapping and immediate discard on rollback, leaving zero dead tuples in memory.
4. **No Distributed 2PC Coordinator (XA / MS DTC)**: Vertical scale-up is prioritized over distributed two-phase commit network overhead.

---

## 6. Concurrency & Transaction Semantics

LionSQL replaces 2PL shared read locks with **Non-Blocking Snapshot Isolation** (`Invariant 3`).

### 6.1 Zero Read-Write Blocking
- **Reads never block writes, and writes never block reads.**
- Minimum isolation is always **Read Committed** or **Snapshot Isolation**.
- **`READ UNCOMMITTED` (Dirty Read) is Spec-Out**: Requests are automatically upgraded to Read Committed to prevent reading corrupt, uncommitted data.

### 6.2 Write-Write Conflict Handling
When concurrent transactions attempt to modify the same record:
- **In-Memory OCC (`compact`)**:
  - Employs Optimistic Concurrency Control.
  - Conflicts trigger an immediate fail-fast rollback (`ErrWriteConflict`).
  - **Client Requirement**: Client applications/ORMs must implement a standard retry loop on write conflict errors.
- **WiredTiger MVCC (`wiredtiger`)**:
  - Row-level latches and transaction snapshots detect conflicts early.
  - Transactions encountering uncommitted changes fail-fast with `WT_ROLLBACK`.
- **2PL Row Lock Waiting (`WITH (UPDLOCK)`)**:
  - Explicit hints currently operate in fail-fast detection mode. Multi-second lock-wait queues are scheduled for v1.1.0.

---

## 7. Storage Engine Constraints

### 7.1 In-Memory OCC Engine (`-engine compact`)
1. **Memory Residency**: All data and indexes must fit in physical RAM. Inserts fail when memory budget is exhausted.
2. **Recovery Latency**: Crash recovery loads the snapshot RDB (`tables.json`) and replays the write-ahead log (`wal.aof`). Datasets with tens of millions of rows may require seconds to tens of seconds during startup.

### 7.2 WiredTiger MVCC Engine (`-engine wiredtiger`)
1. **Cgo Dependency**: Requires Cgo on macOS and Linux. On Windows, the binary embeds and self-extracts a native MSVC DLL at runtime.
2. **Prohibition of `transaction_sync` (`Invariant 10`)**:
   - Synchronous per-transaction `fsync` (`transaction_sync`) is permanently banned because it degrades concurrent throughput by 96%.
   - Durability is guaranteed through write-ahead logging, group commit, and 1-second periodic background flushes. (For zero-loss guarantees across catastrophic hardware power loss, synchronous WAL replication is recommended).

---

## 8. Known Issues & Roadmap

| Domain | Issue / Feature | Workaround | Target Milestone |
| :---: | :--- | :--- | :---: |
| **PostgreSQL** | Universal `expr::type` cast | Use standard `CAST(expr AS type)` | `v1.1.0` |
| **PostgreSQL** | `SELECT DISTINCT ON (...)` | Use `ROW_NUMBER() OVER (PARTITION BY ...)` | `v1.1.0` |
| **PostgreSQL** | `FILTER (WHERE ...)` aggregate clause | Use `CASE WHEN ... THEN val END` | `v1.1.0` |
| **PostgreSQL** | `ILIKE` and POSIX regex (`~`, `!~`) | Use `LOWER(col) LIKE LOWER(pattern)` | `v1.1.0` |
| **PostgreSQL** | `CREATE SEQUENCE` and `SERIAL` | Fully supported via `INT IDENTITY(1,1)` | `v1.1.0` |
| **Storage** | `ALTER COLUMN` online variable-length expansion | Use Copy-and-Switch pattern | `v1.1.0` |
| **Concurrency** | `WITH (UPDLOCK)` 2PL lock-wait queue manager | Currently operates in fail-fast mode | `v1.1.0` |
| **Performance** | Unified Tuple Protocol (`ARCH-UNIFIED-TUPLE-PROTOCOL-1`) | Eliminates `map[string]any` across VM and Wire (100k TPS) | `v1.10.0` |
| **Enterprise** | Materialized View virtual alias & refresh | Enterprise dashboard compatibility layer | `v1.20.0` |
| **AI / Search** | Pure-Go Vector Search (`pgvector` compatible) | IVFFlat / HNSW vector indexing | `v1.60.0` |

---

## 9. Adoption Decision Checklist

Evaluate these architectural criteria before deploying LionSQL:

- [ ] Does your workload scale vertically on a high-core single node (20k~34k+ TPS)? (Yes ➔ Recommended)
- [ ] Can your application operate without distributed 2PC (XA) transactions? (Yes ➔ Recommended)
- [ ] Are individual rows within 16 MB and inline fixed columns within 8,060 Bytes? (Yes ➔ Recommended)
- [ ] Can your queries substitute `FULL OUTER JOIN` with `LEFT/RIGHT JOIN` or `UNION`? (Yes ➔ Recommended)
- [ ] For In-Memory OCC, does the entire dataset fit within available RAM? (Yes ➔ Recommended)
- [ ] Can schema reorganization use the Copy-and-Switch pattern for non-tail column drops? (Yes ➔ Recommended)

If your system answers **Yes** to these criteria, LionSQL eliminates external dependencies and provides **unrivaled single-binary deployment simplicity, extreme OLTP speed, and seamless dual T-SQL/PostgreSQL wire interoperability**.
