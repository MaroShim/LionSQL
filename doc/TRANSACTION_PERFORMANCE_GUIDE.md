# LionSQL Transaction Performance & Workload Tuning Guide (`doc/TRANSACTION_PERFORMANCE_GUIDE.md`)

> - **Created**: 2026-11-02
> - **Last Updated**: 2026-11-11
> [한국어 문서 (Korean Version)](TRANSACTION_PERFORMANCE_GUIDE.ko.md)

This guide analyzes how LionSQL's storage backends, WAL synchronization policies, memory governor, and worker pool configurations impact transaction throughput (TPS), latency, and data durability (RPO), and provides recommended configuration profiles for common production workloads.

---

## 1. Overview & Performance-Durability Trade-off Matrix

Database transaction performance can vary by an order of magnitude depending on the choice of **physical disk synchronization barrier (`fsync`)** and **concurrency control model (OCC vs. MVCC)**. Within a single standalone binary, LionSQL spans the full spectrum from sub-millisecond in-memory caching to strict zero-data-loss (`RPO = 0`) financial ledger durability.

```
[ High Throughput / Sub-Millisecond Latency ]
   ▲  -engine memory, -ephemeral, -fsync off       (20,000 ~ 25,000+ TPS / <1ms P99)
   │  -engine memory, -fsync everysec              (14,000 ~ 16,000 TPS / ~1ms P99)
   │  -engine wiredtiger, -fsync everysec (Default)(12,000 ~ 16,800 TPS / <10ms P99)
   │  -engine wiredtiger, -fsync always            (1,700 ~ 1,860 TPS / 15~25ms P99)
   ▼
[ Strict Durability / RPO = 0 (Zero Data Loss) ]
```

---

## 2. Four Recommended Workload Profiles

### 2.1. Profile 1: Ultra Low-Latency In-Memory Cache & Real-Time Sessions
Eliminates disk I/O bottlenecks for workloads where sub-millisecond response time is the top priority.

* **Recommended Flags**:
  ```bash
  lionsql serve \
    -disk compact \
    -ephemeral \
    -fsync off \
    -max-memory 4GB \
    -max-workers 256
  ```
  > (The legacy `-disk none` and `-engine memory` flags map transparently to `-disk compact` for backward compatibility.)
* **Performance Characteristics**:
  - **Throughput**: **20,000 ~ 25,000+ TPS** (> 540,000 tpmC on TPC-C)
  - **Latency**: Median P50 **0.05 ms**, Tail P99 **< 1.0 ms**
  - **Durability (RPO)**: **Volatile (N/A)** (data is not persisted across process crashes)
* **Ideal Use Cases**:
  - Real-time gaming leaderboards, player sessions, and lobby state
  - High-frequency shopping cart and auth token caching layers
  - Fast ephemeral CI/CD unit and integration testing environments
  - Transient microservice message queuing and aggregation buffers

### 2.2. Profile 2: Standard Production OLTP (Recommended Default)
Provides the optimal balance between high concurrency throughput and crash durability for general enterprise services.

* **Recommended Flags**:
  ```bash
  lionsql serve \
    -dir /var/lib/lionsql/data \
    -engine wiredtiger \
    -fsync everysec \
    -max-memory 4GB \
    -work-mem 64MB \
    -max-workers 256 \
    -max-idle-sessions 64
  ```
* **Performance Characteristics**:
  - **Throughput**: **12,000 ~ 16,800+ TPS** (~ 457,000 tpmC on TPC-C)
  - **Latency**: Median P50 **0.12 ~ 0.85 ms**, Tail P99 **< 10 ms**
  - **Durability (RPO)**: **Up to 1 second (1s Periodic Durability)** via background `fsync`
* **Ideal Use Cases**:
  - E-commerce orders, checkout workflows, and product catalogs
  - Multi-tenant SaaS application databases
  - Logistics and shipment tracking systems
  - Enterprise ERP / CRM transactional workloads

### 2.3. Profile 3: Strict Zero-Loss Financial Core Ledger (`RPO = 0`)
Guarantees that not a single committed transaction is lost even across sudden power outages, hardware panics, or OS kernel crashes.

* **Recommended Flags**:
  ```bash
  lionsql serve \
    -dir /var/lib/lionsql/data \
    -engine wiredtiger \
    -fsync always \
    -max-memory 8GB \
    -work-mem 64MB \
    -max-workers 128 \
    -max-idle-sessions 128
  ```
* **Performance Characteristics**:
  - **Throughput**: **1,700 ~ 1,860+ TPS** (with Leader/Follower Group Commit enabled)
  - **Latency**: Median P50 **1.5 ~ 2.5 ms**, Tail P99 **15 ~ 25 ms**
  - **Durability (RPO)**: **RPO = 0 (Zero Data Loss)** via physical NVMe `F_FULLFSYNC` barrier synchronization
* **Ideal Use Cases**:
  - Core banking transfers, digital asset wallets, and payment settlement ledgers
  - Legally binding immutable audit trails and electronic contracts
  - Securities and foreign exchange trade execution records

### 2.4. Profile 4: Heavy Analytical Batch & Bulk Migration (ETL)
Optimized for bulk-loading millions of rows or executing large multi-table joins and sort-heavy analytical reports.

* **Recommended Flags**:
  ```bash
  lionsql serve \
    -dir /var/lib/lionsql/data \
    -engine wiredtiger \
    -work-mem 256MB \
    -max-memory 16GB \
    -max-workers 64
  ```
* **Performance Characteristics**:
  - Expands per-operator sort memory budget to `256MB` to prevent disk spills (External Merge Sort) and keep pipelines 100% in-memory.
  - Caps concurrent workers at `64` so CPU cores focus on analytical aggregation rather than context switching.
* **Ideal Use Cases**:
  - Initial bulk data migration from PostgreSQL, MSSQL, or Oracle
  - Nightly financial settlement and monthly statistical reporting batches
  - Large-scale index builds (`CREATE INDEX`) and schema reorganization

---

## 3. Core Parameter Impact Analysis

### 3.1. `-disk` / `-memory` (Storage Backend & In-Memory Layout Selection)
* **`-disk compact`** (Universal Default across macOS, Linux, and Windows; legacy alias `-disk none` / `-engine memory`):
  - Pure heap-based 256-shard concurrent Optimistic Concurrency Control (OCC) with pointer-free `FlatRecord` structure.
  - Strong ACID durability via sequential AOF (`wal.aof`) and compact RDB snapshots (`tables.mpk`).
  - Eliminates disk B+Tree serialization to achieve sub-microsecond (~100ns) point read/write latency and up to 1,040,000 tpmC.
  - Zero-CGo pure Go architecture for 100% portable single-binary execution on all operating systems.
* **`-disk wiredtiger`** (Explicit Opt-In; legacy alias `-engine wiredtiger`):
  - Disk-backed lock-free B+Tree and MVCC architecture.
  - Safely manages datasets much larger than available RAM (tens to hundreds of GBs).
  - Reduces disk footprint by 50~70% via Snappy page compression.
  - Session temporary tables (`#temp`) and table variables (`@tbl`) automatically bypass disk B+Tree overhead via hybrid in-memory execution.
* **`-memory compact`** (Default, **Production Recommended**):
  - Encodes in-memory rows into pointer-free contiguous `FlatRecord` byte buffers.
  - Reduces heap footprint by **~60.8%** compared to raw pointer slices and eliminates per-row Go GC pointer graphs.
  - Under concurrent client-server OLTP, eliminating GC mark/STW pressure yields **+46% higher TPS** and substantially lower P99 tail latency than `-memory performance` ([`ADR-028`](ADR.md#adr-028-pointer-free-flat-record-compact-as-default-in-memory-layout-and-deprecation-of-raw-slice-mode-performance)).
* **`-memory performance`** (**Deprecated in v1.0, Scheduled for Removal Post-1.0**):
  - Legacy layout storing materialized Go `[]storage.Value` slices per row.
  - Underperforms `compact` in real multi-worker OLTP due to Go GC pointer-scanning overhead despite faster raw single-thread scan microbenchmarks. Retained in v1.0 solely for compatibility and deprecated prior to v1.0.

### 3.2. `-fsync` (WAL & Transaction Journal Synchronization Mode)
| Mode | Behavior | Relative TPS | Power-Loss RPO Risk |
| :--- | :--- | :---: | :--- |
| **`everysec`** (Default) | Background goroutine flushes WAL to disk every 1 second | **100% (Baseline)** | Up to 1 second of committed data |
| **`always`** | Synchronous physical `F_FULLFSYNC` disk barrier on every commit (combined with Group Commit) | **~12%** | **Zero Data Loss (RPO = 0)** |
| **`off`** | Delegates flushing to the OS kernel page cache | **>120%** | Several seconds on OS kernel crash |

> [!NOTE]
> When running with `-fsync always`, LionSQL automatically engages its **Leader/Follower Group Commit** batching engine. Concurrent commit requests arriving within the same disk flush window are coalesced into a single physical disk barrier, boosting throughput by over 4x compared to single-threaded synchronous flushing.

### 3.3. `-work-mem` (Sort & Hash Join Working Memory Budget)
* **Default**: `64MB`
* **Mechanism**: `ORDER BY`, `GROUP BY`, and `HashJoin` operators accumulate tuples in memory up to `work-mem`. When exceeded, operators transparently spill sorted runs to temporary disk files using **External Merge Sort**.
* **Tuning Guidelines**:
  - Memory-constrained environments (1~2GB RAM): Lower to `-work-mem 16MB` to prevent OOM kills when multiple sessions run concurrent sorts.
  - Analytical reporting sessions: Dynamically override per session via `SET work_mem = '256MB';` to keep large sorts entirely in memory.

### 3.4. `-max-memory` (Global Heap Memory Cap & Governor)
* LionSQL's Memory Governor monitors total process heap allocation.
* When usage reaches 85% of `-max-memory`, it throttles large new allocations and evicts cached execution plans.
* In containerized environments (Docker, Kubernetes), set `-max-memory` to **70~80% of the container cgroup limit** to prevent container OOM kills.

---

## 4. Hardware Sizing & Deployment Matrix

| Tier | Hardware Specification | Recommended LionSQL Flags | Expected Throughput |
| :--- | :--- | :--- | :---: |
| **Edge / Small VM** | 1~2 vCPU, 2GB RAM<br>(Cloud e2-small, t4g.small) | `-max-memory 1200MB`<br>`-work-mem 16MB`<br>`-max-workers 64`<br>`-max-idle-sessions 32` | **3,000 ~ 5,000 TPS** |
| **Standard Production** | 4~8 vCPU, 8~16GB RAM<br>NVMe SSD | `-max-memory 8GB`<br>`-work-mem 64MB`<br>`-max-workers 256`<br>`-max-idle-sessions 64`<br>`-fsync everysec` | **12,000 ~ 16,800 TPS** |
| **High-End Bare Metal** | 16~32 vCPU, 64GB RAM<br>PCIe 4.0 NVMe Enterprise | `-max-memory 32GB`<br>`-work-mem 128MB`<br>`-max-workers 512`<br>`-max-idle-sessions 256`<br>`-fsync everysec` | **25,000+ TPS** |

---

## 5. Production Monitoring & Diagnostics Checklist

1. **Slow Query Detection (`-slow-threshold-ms`)**:
   - Keep the default `100.0ms` for general production, or tighten to `-slow-threshold-ms 20.0` in latency-sensitive financial services to catch unindexed sequential scans early.
2. **Serialization Conflicts (`40001 ErrWriteConflict`)**:
   - Concurrent writes to the same key under OCC or WiredTiger snapshot isolation return SQLSTATE `40001`.
   - Application layers should implement exponential backoff retry loops for `40001` responses.
3. **Disk I/O Latency Tracking**:
   - Monitor the `lionsql_wal_sync_duration_seconds` histogram via `GET /metrics` (or `/api/metrics`) to detect underlying storage degradation early.
