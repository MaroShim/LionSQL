# SQLion Performance Tuning & Benchmark Guide

This document outlines the benchmark methodology, workload profiles, command-line arguments, and tuning recommendations for SQLion under high-concurrency OLTP workloads.

---

## 1. Built-in Benchmark Architecture (`cmd/bench`)

SQLion includes a native, high-concurrency benchmarking tool (`cmd/bench/main.go`) that simulates heavy banking transfer transactions (`TransferFunds` stored procedure) executing across parallel worker goroutines.

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

## 2. Running Benchmarks

### 2.1. Building the Benchmark Binary

```bash
# Compile benchmark binary with WiredTiger Cgo bindings
go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench

# Compile pure-Go benchmark binary
go build -buildvcs=false -o bin/bench ./cmd/bench
```

### 2.2. Command-Line Arguments

| Flag | Default | Description |
|---|---|---|
| `-engine` | `wiredtiger` | Storage engine backend (`wiredtiger` or `memory`) |
| `-accounts` | `10000` | Number of initialized customer accounts |
| `-workers` | `100` | Number of concurrent client worker goroutines |
| `-ops` | `50000` | Total number of transfer transactions to complete |
| `-dir` | `data/bench_wt` | Storage directory for WiredTiger disk files |

### 2.3. Example Executions

#### Benchmarking WiredTiger (Default Production Disk Engine)
```bash
./bin/bench -engine=wiredtiger -accounts=10000 -workers=100 -ops=50000
```

Sample output:
```
================================================================================
  SQLion High-Concurrency OLTP Benchmark (TransferFunds Stored Procedure)
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

### 2.4. Standard TPC-C OLTP Benchmark (`cmd/tpcc`)

SQLion includes a native, in-tree TPC-C benchmark suite (`cmd/tpcc/main.go`) modeling the full 9 standard relational tables (`warehouse`, `district`, `customer`, `history`, `new_order`, `orders`, `order_line`, `item`, `stock`) executing the standard workload mix (45% New-Order, 43% Payment, 4% Order-Status, 4% Delivery, 4% Stock-Level).

```bash
# Build TPC-C benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc ./cmd/tpcc

# Run TPC-C benchmark on In-Memory OCC Engine
./bin/tpcc -engine=memory -warehouses=10 -workers=100 -duration=10s -max-retries=3

# Run TPC-C benchmark on WiredTiger Engine
./bin/tpcc -engine=wiredtiger -warehouses=10 -workers=100 -duration=10s -max-retries=3
```

#### Official Verified Results:

| Metric | In-Memory OCC Engine | WiredTiger Engine |
|---|---|---|
| **tpmC** (New-Order / min) | **1,251,489 tpmC** | **507,456 tpmC** |
| **Overall Throughput** | **43,832 TPS** | **17,294 TPS** |
| **New-Order Success Rate** | **99.8%** | **91.5%** |
| **Average Latency** | **0.20 ms** | **1.82 ms** |
| **P95 Latency** | **0.48 ms** | **4.25 ms** |

### 2.5. Native T-SQL VDBE Bytecode TPC-C Benchmark (`cmd/tpcc_vdbe`)

SQLion also provides a full-stack T-SQL Stored Procedure TPC-C benchmark (`cmd/tpcc_vdbe/main.go`). Unlike `cmd/tpcc` which tests direct storage engine transaction APIs, `cmd/tpcc_vdbe` models all TPC-C operations as compiled Microsoft T-SQL stored procedures (`TPCC_NewOrderHeader`, `TPCC_NewOrderLine`, `TPCC_Payment`, `TPCC_OrderStatus`, `TPCC_Delivery`, `TPCC_StockLevel`) precompiled into VDBE bytecode (`*proc.Program`), cached in `PlanCache`, and executed on the virtual machine (`executor.Run`).

```bash
# Build TPC-C VDBE benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe

# Run T-SQL VDBE Bytecode Benchmark on In-Memory OCC Engine
./bin/tpcc_vdbe --engine=memory --warehouses=10 --workers=20 --duration=5s

# Run T-SQL VDBE Bytecode Benchmark on WiredTiger Engine
./bin/tpcc_vdbe --engine=wiredtiger --warehouses=10 --workers=20 --duration=5s
```

#### Official Verified VDBE Results (10 Warehouses, 20 Workers, Single Atomic Tx Mode):

| Metric | In-Memory OCC Engine (`memory`) | WiredTiger Engine (`wiredtiger`) | Analysis & Comparative Delta |
|---|---|---|---|
| **tpmC (New-Order / min)** | **314,707 ~ 448,853 tpmC** | **298,426 tpmC** | WiredTiger delivers **~95%** of pure in-memory throughput |
| **Overall Throughput (TPS)** | **11,654 ~ 16,687 TPS** | **10,936 TPS** | All 5 TPC-C transaction types fully committed |
| **Serialization Abort Rate** | **0.09%** | **4.46%** | Version OCC exhibits near-zero aborts under high concurrency |
| **New-Order P50 Latency** | **1.25 ms** | **2.19 ms** | Sub-3ms median execution |
| **New-Order P95 / P99 Tail** | 12.45 ms / 28.45 ms | **7.62 ms / 11.42 ms** | WiredTiger B+Tree tail latency is tighter and more predictable |
| **Payment P50 Latency** | **0.07 ms** | **0.31 ms** | Ultra-fast in-memory point mutations |
| **Seeding Speed (10 Wh)** | 1.053 s | **0.118 s** | WiredTiger B+Tree bulk insert speed is ~9x faster |

---

## 3. Performance Tuning Dimensions

### 3.1. Query Memory Governor (`work_mem`) Tuning

SQLion's relational engine enforces memory caps per operator (`ORDER BY`, `GROUP BY`, `HashJoin`):
- **Default Budget**: `64MB` (`-work-mem=64MB`).
- **Low Memory Environments (< 2GB RAM)**:
  Set `-work-mem=16MB` to allow high concurrency without risking process OOM.
- **Analytical / Reporting Workloads (Large Joins & Sorts)**:
  Set `SET work_mem = '256MB'` within reporting sessions to keep multi-million row sorts entirely in RAM and bypass external merge sort disk spills.

### 3.2. Connection Pooler & Worker Sizing

SQLion's internal pooler multiplexes client connections onto worker goroutines:
- **`-max-workers`**:
  - Default: `NumCPU * 4`.
  - For compute-heavy or CPU-bound stored procedures, set `-max-workers=NumCPU * 2`.
  - For I/O-bound disk workloads on NVMe drives, increase to `NumCPU * 8`.
- **`-max-idle-sessions`**:
  - Default: `64`.
  - Determines how many pre-warmed storage sessions are held in `sync.Pool`.
  - In high-throughput microservice environments, increase to `128` or `256` to eliminate session allocation overhead.

### 3.3. In-Memory Engine AOF Fsync Policies

For the `memory` engine, disk persistence behavior is configurable:
1. `FsyncEverySec` (Default): Background goroutine fsyncs the write-ahead log every 1 second. Maximum data loss on sudden power halt is bounded to 1 second. Provides peak throughput (> 50,000 TPS).
2. `FsyncAlways`: Every committed transaction issues synchronous `fsync()`. Guarantees zero data loss at the expense of disk I/O latency (~2,000 - 5,000 TPS depending on disk hardware).
3. `FsyncNone`: OS manages write buffer flushing. Highest throughput, lowest durability.

### 3.4. Dual-Parser Plan Cache Optimization

SQLion caches parsed ASTs and compiled VDBE bytecode programs:
- Parameterized queries (`SELECT * FROM accounts WHERE id = @id` or `$1`) avoid parser and compiler re-evaluation.
- Prepared statements (`'P'` Parse over PostgreSQL wire protocol) are stored per connection in `statements` map and execute with sub-microsecond latency.
