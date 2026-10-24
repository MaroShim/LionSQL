# LionSQL Performance Tuning & Benchmark Guide

This document outlines the benchmark methodology, workload profiles, command-line arguments, and tuning recommendations for LionSQL under high-concurrency OLTP workloads.

---

## 1. Built-in Benchmark Architecture (`cmd/bench`)

LionSQL includes a native, high-concurrency benchmarking tool (`cmd/bench/main.go`) that simulates heavy banking transfer transactions (`TransferFunds` stored procedure) executing across parallel worker goroutines.

```
                  +----------------------------------------------+
                  |         Benchmark Harness (cmd/bench)        |
                  +----------------------------------------------+
                                         │
                    ┌────────────────────┼────────────────────┐
                    ▼                    ▼                    ▼
             [Worker 1 (goroutine)] [Worker 2] ... [Worker N (goroutine)]
                    │                    │                    │
                    └────────────────────┼────────────────────┘
                                         │
                                         ▼
                  +----------------------------------------------+
                  |  Precompiled Plan Cache (Bytecode Program)   |
                  +----------------------------------------------+
                                         │
                                         ▼
                  +----------------------------------------------+
                  |    Ambient ACID Storage Transactions         |
                  |  - Read sender balance                       |
                  |  - Validate fund availability                |
                  |  - Debit sender & Credit receiver            |
                  |  - Commit transaction                        |
                  +----------------------------------------------+
                                         │
                                         ▼
                  +----------------------------------------------+
                  | ACID Balance Invariant Integrity Validation  |
                  | Total Initial Assets == Total Final Assets   |
                  +----------------------------------------------+
```

### Invariant Verification
Upon benchmark completion, the harness audits all accounts and asserts that **total system wealth is conserved to the exact cent**:
$$\sum \text{Initial Balances} \equiv \sum \text{Final Balances}$$
Any lost updates, phantom writes, or dirty reads immediately trigger an ACID violation error.

---

## 2. Benchmark Hardware & System Environment

All verified performance metrics, TPC-C tpmC rates, TPS throughput, and latency percentiles documented in this guide were measured and recorded on the primary development host:

| Hardware / System Component | Specification & Configuration |
|---|---|
| **System Host** | Apple iMac (24-inch, M1) |
| **Processor (CPU)** | Apple M1 (8 cores: 4 High-Performance cores @ 3.20 GHz + 4 High-Efficiency cores @ 2.06 GHz) |
| **Graphics (GPU)** | 8-Core integrated Apple M1 GPU |
| **Unified Memory (RAM)** | 16 GB Unified Memory (LPDDR4X, unified architecture, 68.25 GB/s bandwidth) |
| **Internal Storage (SSD)** | 500 GB Apple NVMe Solid State Drive (Apple APFS file system, hardware write caching) |
| **Operating System** | macOS (Darwin ARM64) |
| **Compiler & Runtime** | Go 1.24+ (`GOOS=darwin`, `GOARCH=arm64`, Cgo enabled for WiredTiger bindings) |

---

## 3. Running Benchmarks

### 3.1. Building the Benchmark Binary

```bash
# Compile benchmark binary with WiredTiger Cgo bindings
go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench

# Compile pure-Go benchmark binary
go build -buildvcs=false -o bin/bench ./cmd/bench
```

### 3.2. Command-Line Arguments

| Flag | Default | Description |
|---|---|---|
| `-engine` | `wiredtiger` | Storage engine backend (`wiredtiger` or `memory`) |
| `-accounts` | `10000` | Number of initialized customer accounts |
| `-workers` | `100` | Number of concurrent client worker goroutines |
| `-ops` | `50000` | Total number of transfer transactions to complete |
| `-dir` | `data/bench_wt` | Storage directory for WiredTiger disk files |

### 3.3. Example Executions

#### Benchmarking WiredTiger (Default Production Disk Engine)
```bash
./bin/bench -engine=wiredtiger -accounts=10000 -workers=100 -ops=50000
```

Verified output:
```text
================================================================================
  LionSQL High-Concurrency OLTP Benchmark (TransferFunds Stored Procedure)
================================================================================
 [Target]         Stored Procedure (T-SQL -> Native VDBE Bytecode -> ACID Tx)
 [Storage Engine] wiredtiger (Dir: data/bench_wt)
 [Accounts]       10000 accounts (Initial balance: 1,000,000 each)
 [Concurrency]    100 concurrent workers
 [Total Ops]      50000 transactions
--------------------------------------------------------------------------------
Initializing wiredtiger storage engine and seeding 10000 accounts... Done. (Total assets: 10,000,000,000)
Starting benchmark execution...
Verifying database ACID integrity across all accounts...
================================================================================
                              BENCHMARK RESULTS                                 
================================================================================
 Total Execution Time:    414.491708ms
 Total Completed Ops:     50,000 transactions
 Successful Transfers:    49,768
 Failed/Rolled Back:      232
--------------------------------------------------------------------------------
 Throughput (TPS):        120629.67 TPS
--------------------------------------------------------------------------------
 Latency (Mean):          589.094µs
 Latency (Median / P50):  26.208µs
 Latency (P90):           86.958µs
 Latency (P95):           250.667µs
 Latency (P99):           4.319666ms
 Latency (Min):           14.042µs
 Latency (Max):           278.21525ms
--------------------------------------------------------------------------------
 ACID Invariant Check:    Initial Sum = 10,000,000,000, Final Sum = 10,000,000,000
 Integrity Verdict:       100% VERIFIED (Zero balance leakage / Exact ACID)
================================================================================
```

#### Benchmarking Pure-Go In-Memory OCC Engine
```bash
./bin/bench -engine=memory -accounts=10000 -workers=100 -ops=50000
```

Verified output:
```text
================================================================================
  LionSQL High-Concurrency OLTP Benchmark (TransferFunds Stored Procedure)
================================================================================
 [Target]         Stored Procedure (T-SQL -> Native VDBE Bytecode -> ACID Tx)
 [Storage Engine] memory (Dir: data/bench_wt)
 [Accounts]       10000 accounts (Initial balance: 1,000,000 each)
 [Concurrency]    100 concurrent workers
 [Total Ops]      50000 transactions
--------------------------------------------------------------------------------
Initializing memory storage engine and seeding 10000 accounts... Done. (Total assets: 10,000,000,000)
Starting benchmark execution...
Verifying database ACID integrity across all accounts...
================================================================================
                              BENCHMARK RESULTS                                 
================================================================================
 Total Execution Time:    267.713916ms
 Total Completed Ops:     50,000 transactions
 Successful Transfers:    49,996
 Failed/Rolled Back:      4
--------------------------------------------------------------------------------
 Throughput (TPS):        186766.53 TPS
--------------------------------------------------------------------------------
 Latency (Mean):          476.548µs
 Latency (Median / P50):  27.584µs
 Latency (P90):           722.666µs
 Latency (P95):           2.21625ms
 Latency (P99):           9.748833ms
 Latency (Min):           10.583µs
 Latency (Max):           50.129833ms
--------------------------------------------------------------------------------
 ACID Invariant Check:    Initial Sum = 10,000,000,000, Final Sum = 10,000,000,000
 Integrity Verdict:       100% VERIFIED (Zero balance leakage / Exact ACID)
================================================================================
```

#### Performance Comparison Summary:

| Metric | WiredTiger (Disk MVCC) | In-Memory OCC (Pure-Go Memory) |
|---|---|---|
| **Total Execution Time** | 414.49 ms | **267.71 ms** |
| **Throughput (TPS)** | **120,629.67 TPS** | **186,766.53 TPS** |
| **Success Rate** | 99.53% (49,768 passed) | **99.99%** (49,996 passed) |
| **Median Latency (P50)** | **26.21 µs** | **27.58 µs** |
| **P95 Latency** | **250.67 µs** | 2.22 ms |
| **P99 Latency** | **4.32 ms** | 9.75 ms |
| **ACID Integrity Verdict** | 100% VERIFIED | 100% VERIFIED |

### 3.4. Storage Engine Kernel TPC-C Benchmark (`cmd/tpcc_kernel` / `bin/tpcc-kernel`)

LionSQL includes an in-tree storage engine kernel TPC-C benchmark suite (`cmd/tpcc_kernel/main.go`) modeling the full 9 standard relational tables (`warehouse`, `district`, `customer`, `history`, `new_order`, `orders`, `order_line`, `item`, `stock`) executing the standard workload mix (45% New-Order, 43% Payment, 4% Order-Status, 4% Delivery, 4% Stock-Level). It tests storage layer SPI CRUD transactions directly without SQL parsing or bytecode execution overhead.

```bash
# Build storage engine kernel benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-kernel ./cmd/tpcc_kernel

# Run TPC-C benchmark on In-Memory OCC Engine
./bin/tpcc-kernel -engine=memory -warehouses=10 -workers=100 -duration=10s -max-retries=3

# Run TPC-C benchmark on WiredTiger Engine
./bin/tpcc-kernel -engine=wiredtiger -warehouses=10 -workers=100 -duration=10s -max-retries=3
```

#### Official Verified Results:

| Metric | In-Memory OCC Engine (10 Wh, 100 Wrk) | WiredTiger Engine (Disk MVCC: 10 Wh, 100 Wrk) |
|---|---|---|
| **tpmC** (New-Order / min) | **779,641.87 tpmC** | **833,595.29 tpmC** |
| **Overall Throughput** | **28,881.96 TPS** | **29,352.93 TPS** |
| **New-Order Success Rate** | **96.76%** (130,304 committed / 134,673 attempted) | **89.71%** (139,099 committed / 155,049 attempted) |
| **Payment Committed** | **123,122 committed** | **113,589 committed** |
| **Final Abort Rate (Overall)** | **3.64%** (10,951 aborts) | **14.67%** (50,532 aborts) |
| **New-Order Avg / P95 Latency** | **3.77 ms / 16.76 ms** | **2.93 ms / 18.71 ms** |
| **Payment Avg / P95 Latency** | **2.99 ms / 17.48 ms** | **3.62 ms / 22.25 ms** |
| **Order-Status Avg / P95 Latency** | **0.48 ms / 3.17 ms** | **0.04 ms / 0.01 ms** |
| **Stock-Level Avg / P95 Latency** | **0.36 ms / 1.97 ms** | **0.13 ms / 0.04 ms** |
| **Delivery Avg / P95 Latency** | **1.01 ms / 4.03 ms** | **0.12 ms / 0.09 ms** |

#### Architectural Discussion & Comparative Analysis:

1. **Transactional Integrity vs. Fast-Fail Aborts (tpmC vs. Success Rate)**:
   - **WiredTiger Fast-Fail**: WiredTiger records a higher raw tpmC (**833,595 tpmC** vs. OCC's **779,641 tpmC**) because its conflict detector triggers immediate rollback (`WT_ROLLBACK`) upon write collisions. This results in a high overall abort rate of **14.67% (50,532 final aborts)**. By dropping conflicted transactions quickly without extended backoff, worker loops iterate faster, increasing the raw New-Order counter.
   - **OCC High Integrity & Lower Abort Rate**: In-Memory OCC maintains a **3.64% final abort rate**, and successfully committed **123,122 Payment transactions**—nearly 10,000 more completed transactions than WiredTiger (113,589). In high-concurrency OLTP workloads where business updates must genuinely commit rather than fail-fast, OCC achieves significantly higher end-to-end completion integrity.

2. **Read Latency Profiles (MVCC Hazard Pointers vs. Sharded RWMutex)**:
   - **WiredTiger MVCC Zero-Lock Reads**: WiredTiger employs Hazard Pointer-based MVCC B+Trees where read transactions (`Order-Status`, `Stock-Level`) never acquire shared locks on data pages. Even while 43 concurrent workers execute heavy writes on the `customer` table, readers access snapshot versions in parallel without blocking, achieving **0.04 ms (40 µs)** read latency.
   - **In-Memory OCC Sharded Mutex Contention**: In-Memory OCC partitions tables across 64 shards protected by `sync.RWMutex`. While point reads take under 1 µs in isolation, under 100 concurrent workers updating customer balances and warehouse districts, read transactions (`Order-Status`) acquiring shard `RLock()` must occasionally wait for in-flight `Payment` transactions holding shard exclusive `Lock()`, resulting in an average latency of **0.48 ms (480 µs)**.

### 3.5. Native T-SQL VDBE Runtime TPC-C Benchmark (`cmd/tpcc_runtime` / `bin/tpcc-runtime`)

LionSQL also provides a full-stack T-SQL Stored Procedure TPC-C runtime benchmark (`cmd/tpcc_runtime/main.go`). Unlike `cmd/tpcc_kernel` which tests direct storage engine transaction APIs, `cmd/tpcc_runtime` models all TPC-C operations as compiled Microsoft T-SQL stored procedures (`TPCC_NewOrderHeader`, `TPCC_NewOrderLine`, `TPCC_Payment`, `TPCC_OrderStatus`, `TPCC_Delivery`, `TPCC_StockLevel`) precompiled into VDBE bytecode (`*proc.Program`), cached in `PlanCache`, and executed on the virtual machine (`executor.Run`).

```bash
# Build TPC-C VDBE runtime benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime

# Run T-SQL VDBE Runtime Benchmark on In-Memory OCC Engine (2 Warehouses, 20 Workers, 15s)
./bin/tpcc-runtime --engine=memory --warehouses=2 --workers=20 --duration=15s

# Run T-SQL VDBE Runtime Benchmark on WiredTiger Engine
./bin/tpcc-runtime --engine=wiredtiger --warehouses=2 --workers=20 --duration=15s
```

#### Official Verified VDBE Results:

##### 1. Scale Factor 10 Warehouses, 20 Client Workers (Balanced Contention & Pooling Accelerated):

| Metric | In-Memory OCC Engine (`fsync off`) | WiredTiger Engine (`everysec` 1s Fsync) | Analysis & Production Invariants |
|---|---|---|---|
| **tpmC (New-Order / min)** | **541,708.93 tpmC** | **457,021.04 tpmC** | Peak performance: In-Memory OCC breaks 540k tpmC; WiredTiger achieves >457k tpmC |
| **Overall Throughput (TPS)** | **20,087.70 TPS** (Peak 25,298 TPS) | **16,814.17 TPS** (Peak 16,815 TPS) | Full ACID atomic execution across all 5 standard TPC-C transaction types |
| **Serialization Abort Rate** | **0.10%** | **3.73%** | P-11/P-12 map & chunk pooling slash aborts and eliminate GC pauses |
| **New-Order P50 / P99 Latency** | **0.69 ms / 19.33 ms** | **0.85 ms / 9.46 ms** | Sub-millisecond median; WiredTiger provides ultra-flat P99 tail latency |
| **Payment P50 / P99 Latency** | **0.05 ms / 5.38 ms** | **0.12 ms / 7.22 ms** | O(1) slot variable lookup, balance mutation, and NBCROW zero-copy framing |
| **Order-Status P50 / P99** | **0.01 ms / 0.09 ms** | **0.01 ms / 0.67 ms** | Microsecond-level read-only query dispatch across both backends |
| **Delivery / Stock-Level P50** | **0.06 ms / 0.05 ms** | **0.06 ms / 0.07 ms** | Sub-millisecond multi-row DML and range scan latency |

##### 2. Scale Factor 2 Warehouses, 20 Client Workers (Dense District Contention):

| Metric | In-Memory OCC Engine (`everysec`) | WiredTiger Engine (`everysec`) | Analysis & Comparative Delta |
|---|---|---|---|
| **tpmC (New-Order / min)** | **365,912.96 tpmC** | **371,341.77 tpmC** | High contention resilience: Both engines deliver >365,000 tpmC with 1s periodic durability |
| **Overall Throughput (TPS)** | **13,543.59 TPS** | **12,893.16 TPS** | Consistent sustained throughput under 20 workers competing for 2 warehouses |
| **Serialization Abort Rate** | **3.22%** | **13.79%** | OCC private workspaces minimize conflict retries; WT MVCC handles B+Tree concurrency |
| **New-Order P50 / P95 Latency** | **0.79 ms / 6.85 ms** | **1.15 ms / 4.66 ms** | Sub-millisecond to ~1ms median execution for multi-table business transactions |
| **Payment P50 / P95 Latency** | **0.14 ms / 4.11 ms** | **0.69 ms / 4.16 ms** | Ultra-fast O(1) slot variable lookup and point mutations |
| **Order-Status P50 Latency** | **0.01 ms** | **0.01 ms** | Microsecond-level read-only query dispatch across both backends |
| **Delivery / Stock-Level P50** | **0.03 ms / 0.05 ms** | **0.06 ms / 0.06 ms** | Sub-millisecond multi-row DML and range scan latency |

*(Note: Under `-fsync always`, Leader/Follower Group Commit delivers **1,744 ~ 1,863 TPS** on Apple Silicon NVMe despite physical `F_FULLFSYNC` barrier latencies).*

---

## 4. Performance Tuning Dimensions

### 4.1. Query Memory Governor (`work_mem`) Tuning

LionSQL's relational engine enforces memory caps per operator (`ORDER BY`, `GROUP BY`, `HashJoin`):
- **Default Budget**: `64MB` (`-work-mem=64MB`).
- **Low Memory Environments (< 2GB RAM)**:
  Set `-work-mem=16MB` to allow high concurrency without risking process OOM.
- **Analytical / Reporting Workloads (Large Joins & Sorts)**:
  Set `SET work_mem = '256MB'` within reporting sessions to keep multi-million row sorts entirely in RAM and bypass external merge sort disk spills.

### 4.2. Connection Pooler & Worker Sizing

LionSQL's internal pooler multiplexes client connections onto worker goroutines:
- **`-max-workers`**:
  - Default: `NumCPU * 4`.
  - For compute-heavy or CPU-bound stored procedures, set `-max-workers=NumCPU * 2`.
  - For I/O-bound disk workloads on NVMe drives, increase to `NumCPU * 8`.
- **`-max-idle-sessions`**:
  - Default: `64`.
  - Determines how many pre-warmed storage sessions are held in `sync.Pool`.
  - In high-throughput microservice environments, increase to `128` or `256` to eliminate session allocation overhead.

### 4.3. Storage Engine Fsync Policies & Group Commit

Disk persistence and durability behavior is configurable across both `memory` and `wiredtiger` engines:
1. `FsyncEverySec` (Default): Background goroutine flushes the write-ahead log / journal every 1 second. Maximum data loss on sudden power halt is bounded to 1 second. Provides peak throughput (> 50,000 TPS for memory, > 14,000 TPS for WiredTiger).
2. `FsyncAlways`: Every committed transaction is made physically durable on disk via **Leader/Follower Group Commit**. Zero data loss ACID durability is guaranteed, while multiple concurrent commits are batched into a single hardware disk barrier (`fcntl(fd, F_FULLFSYNC)` or `log_flush("sync=on")`).
3. `FsyncNone`: OS manages write buffer flushing without synchronous disk barriers. Highest throughput, lowest durability.

### 4.4. Dual-Parser Plan Cache Optimization

LionSQL caches parsed ASTs and compiled VDBE bytecode programs:
- Parameterized queries (`SELECT * FROM accounts WHERE id = @id` or `$1`) avoid parser and compiler re-evaluation.
- Prepared statements (`'P'` Parse over PostgreSQL wire protocol) are stored per connection in `statements` map and execute with sub-microsecond latency.
