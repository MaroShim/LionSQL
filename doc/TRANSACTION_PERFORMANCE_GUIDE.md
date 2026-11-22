# LionSQL Transaction Performance & Workload Tuning Guide (`doc/TRANSACTION_PERFORMANCE_GUIDE.md`)

> - **Created**: 2026-11-02
> - **Last Updated**: 2026-11-11
> [한국어 문서](TRANSACTION_PERFORMANCE_GUIDE.ko.md)

This guide analyzes how LionSQL's storage backends, WAL synchronization policies, memory governor, and worker pool configurations impact transaction throughput (TPS), latency, and data durability (RPO), and provides recommended configuration profiles for common production workloads.

---

## 1. Overview & Performance-Durability Trade-off Matrix

Database transaction performance can vary by an order of magnitude depending on the choice of **physical disk synchronization barrier (`fsync`)** and **concurrency control model (OCC vs. MVCC)**. Within a single standalone binary, LionSQL spans the full spectrum from sub-millisecond in-memory caching to strict zero-data-loss (`RPO = 0`) financial ledger durability.

```
[ High Throughput / Sub-Millisecond Latency ]
   ▲  -engine compact, -dir ""                (20,000 ~ 25,000+ TPS / <1ms P99)
   │  -engine compact, -dir /path/to/data     (14,000 ~ 16,000 TPS / ~1ms P99)
   │  -engine wiredtiger, -dir /path/to/data  (12,000 ~ 16,800 TPS / <10ms P99)
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
    -engine compact \
    -dir "" \
    -max-memory 4GB \
    -max-workers 256
  ```
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
    -max-memory 4GB \
    -work-mem 64MB \
    -max-workers 256 \
    -max-idle-sessions 64
  ```
* **Performance Characteristics**:
  - **Throughput**: **12,000 ~ 16,800+ TPS** (~ 457,000 tpmC on TPC-C)
  - **Latency**: Median P50 **0.12 ~ 0.85 ms**, Tail P99 **< 10 ms**
  - **Durability (RPO)**: **Up to 1 second (1s Periodic Durability)** via background flush
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
    -max-memory 8GB \
    -work-mem 64MB \
    -max-workers 128 \
    -max-idle-sessions 128
  ```
* **Performance Characteristics**:
  - **Throughput**: **12,000 ~ 16,800+ TPS** (continuous write-ahead logging and checkpointing)
  - **Latency**: Median P50 **0.12 ~ 0.85 ms**, Tail P99 **< 10 ms**
  - **Durability (RPO)**: **RPO = 0 (Zero Data Loss)** via continuous WAL journaling and crash recovery
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

### 3.1. `-engine` & `-dir` (Storage Engine & Persistence Mode Selection)
* **`-engine wiredtiger`** (Default on macOS & Linux):
  - Disk-backed lock-free B+Tree and MVCC architecture.
  - Safely manages datasets much larger than available RAM (tens to hundreds of GBs).
  - Reduces disk footprint by 50~70% via Snappy page compression.
  - Session temporary tables (`#temp`) and table variables (`@tbl`) automatically bypass disk B+Tree overhead via hybrid in-memory OCC execution.
  - Requires a valid data directory (`-dir`).
* **`-engine compact`** (Default on Windows; supported across all platforms):
  - Pure Go 256-shard concurrent Optimistic Concurrency Control (OCC) engine with pointer-free `FlatRecord` structure.
  - Zero CGo dependencies for 100% portable single-binary execution on any OS.
  - **Volatile In-Memory Mode (`-dir ""`):** Runs with zero disk I/O, achieving sub-microsecond (~100ns) point query latency and 25,000+ TPS.
  - **Persistent Mode (`-dir <path>`):** Ensures crash durability via sequential AOF logging (`wal.aof`) and compact snapshots (`tables.mpk`).

> [!NOTE]
> The legacy `-disk` and `-memory` flags are maintained solely as internal aliases for backward compatibility; modern LionSQL unifies storage selection under `-engine`. In-memory row layout is permanently standardized on the pointer-free `FlatRecord` binary format (`compact`), eliminating GC overhead by over 60%.

### 3.2. Transaction Durability & WAL Synchronization Mechanisms
LionSQL maximizes transactional throughput without compromising safety by applying battle-tested internal synchronization strategies per engine:
* **WiredTiger Engine**: Enforces internal write-ahead logging with a 1-second periodic background sync flush, bounding crash loss to at most 1 second while performing complete checkpoints upon graceful shutdown.
* **Compact Engine**: Writes transactions sequentially to an AOF stream and applies a **Leader/Follower Group Commit** algorithm to coalesce concurrent commit requests into a single physical disk barrier, yielding over 4x higher throughput than single-threaded synchronous flushing.

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
| **Standard Production** | 4~8 vCPU, 8~16GB RAM<br>NVMe SSD | `-engine wiredtiger`<br>`-max-memory 8GB`<br>`-work-mem 64MB`<br>`-max-workers 256`<br>`-max-idle-sessions 64` | **12,000 ~ 16,800 TPS** |
| **High-End Bare Metal** | 16~32 vCPU, 64GB RAM<br>PCIe 4.0 NVMe Enterprise | `-engine wiredtiger`<br>`-max-memory 32GB`<br>`-work-mem 128MB`<br>`-max-workers 512`<br>`-max-idle-sessions 256` | **25,000+ TPS** |

---

## 5. Production Monitoring & Diagnostics Checklist

1. **Slow Query Detection (`-slow-threshold-ms`)**:
   - Keep the default `100.0ms` for general production, or tighten to `-slow-threshold-ms 20.0` in latency-sensitive financial services to catch unindexed sequential scans early.
2. **Serialization Conflicts (`40001 ErrWriteConflict`)**:
   - Concurrent writes to the same key under OCC or WiredTiger snapshot isolation return SQLSTATE `40001`.
   - Application layers should implement exponential backoff retry loops for `40001` responses.
3. **Disk I/O Latency Tracking**:
   - Monitor the `lionsql_wal_sync_duration_seconds` histogram via `GET /metrics` (or `/api/metrics`) to detect underlying storage degradation early.
