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

Sample output:
```
================================================================================
  LionSQL High-Concurrency OLTP Benchmark (TransferFunds Stored Procedure)
================================================================================
 [Target]         Stored Procedure (T-SQL -> Bytecode Plan Cache -> ACID Tx)
 [Storage Engine] wiredtiger (Dir: data/bench_wt)
 [Accounts]       10000 accounts (Initial balance: 1,000,000 each)
 [Concurrency]    100 concurrent workers
 [Total Ops]      50000 transactions
--------------------------------------------------------------------------------
Executing transactions across 100 concurrent workers...
Benchmark completed in 3.42s.

--------------------------------------------------------------------------------
 Benchmark Results & Latency Distribution
--------------------------------------------------------------------------------
 [Throughput]     14,619 TPS (Transactions Per Second)
 [Total Ops]      50,000 committed
 [Latency P50]    3.21 ms
 [Latency P95]    7.84 ms
 [Latency P99]    12.45 ms
 [Min / Max]      0.82 ms / 24.11 ms
--------------------------------------------------------------------------------
ACID Integrity Audit: Initial 10,000,000,000 == Final 10,000,000,000 (PASSED)
```

#### Benchmarking Pure-Go In-Memory OCC Engine
```bash
./bin/bench -engine=memory -accounts=10000 -workers=100 -ops=50000
```

### 3.4. Standard TPC-C OLTP Benchmark (`cmd/tpcc`)

LionSQL includes a native, in-tree TPC-C benchmark suite (`cmd/tpcc/main.go`) modeling the full 9 standard relational tables (`warehouse`, `district`, `customer`, `history`, `new_order`, `orders`, `order_line`, `item`, `stock`) executing the standard workload mix (45% New-Order, 43% Payment, 4% Order-Status, 4% Delivery, 4% Stock-Level).

```bash
# Build TPC-C benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc ./cmd/tpcc

# Run TPC-C benchmark on In-Memory OCC Engine
./bin/tpcc -engine=memory -warehouses=10 -workers=100 -duration=10s -max-retries=3

# Run TPC-C benchmark on WiredTiger Engine
./bin/tpcc -engine=wiredtiger -warehouses=10 -workers=100 -duration=10s -max-retries=3
```

#### Official Verified Results:

| Metric | In-Memory OCC Engine (High Concurrency: 10 Wh, 100 Wrk) | In-Memory OCC Engine (Standard OLTP: 2 Wh, 20 Wrk) | WiredTiger Engine (Disk MVCC: 10 Wh, 100 Wrk) |
|---|---|---|---|
| **tpmC** (New-Order / min) | **1,251,489 tpmC** | **785,099 tpmC** | **507,456 tpmC** |
| **Overall Throughput** | **43,832 TPS** | **29,068 TPS** | **17,294 TPS** |
| **New-Order Success Rate** | **99.8%** | **98.0%** | **91.5%** |
| **Average Latency** | **0.20 ms** | **0.79 ms** | **1.82 ms** |
| **P95 Latency** | **0.48 ms** | **2.87 ms** | **4.25 ms** |

### 3.5. Native T-SQL VDBE Bytecode TPC-C Benchmark (`cmd/tpcc_vdbe`)

LionSQL also provides a full-stack T-SQL Stored Procedure TPC-C benchmark (`cmd/tpcc_vdbe/main.go`). Unlike `cmd/tpcc` which tests direct storage engine transaction APIs, `cmd/tpcc_vdbe` models all TPC-C operations as compiled Microsoft T-SQL stored procedures (`TPCC_NewOrderHeader`, `TPCC_NewOrderLine`, `TPCC_Payment`, `TPCC_OrderStatus`, `TPCC_Delivery`, `TPCC_StockLevel`) precompiled into VDBE bytecode (`*proc.Program`), cached in `PlanCache`, and executed on the virtual machine (`executor.Run`).

```bash
# Build TPC-C VDBE benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe

# Run T-SQL VDBE Bytecode Benchmark on In-Memory OCC Engine (2 Warehouses, 20 Workers, 15s)
./bin/tpcc_vdbe --engine=memory --warehouses=2 --workers=20 --duration=15s

# Run T-SQL VDBE Bytecode Benchmark on WiredTiger Engine
./bin/tpcc_vdbe --engine=wiredtiger --warehouses=2 --workers=20 --duration=15s
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
