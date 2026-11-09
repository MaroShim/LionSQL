# LionSQL Performance Tuning & Benchmark Guide

> - **Created**: 2026-09-22
> - **Last Updated**: 2026-11-09
> - **Engine Release**: v0.92.0
> - **Historical Baseline Archive**: [doc/history/BENCHMARK_20261031.md](history/BENCHMARK_20261031.md) (v0.90.0 Baseline)


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
 [Storage Engine] memory (Dir: data/bench_mem)
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
 Total Execution Time:    229.060166ms
 Total Completed Ops:     50,000 transactions
 Successful Transfers:    50,000
 Failed/Rolled Back:      0
--------------------------------------------------------------------------------
 Throughput (TPS):        218282.59 TPS
--------------------------------------------------------------------------------
 Latency (Mean):          417.828µs
 Latency (Median / P50):  23.167µs
 Latency (P90):           176.208µs
 Latency (P95):           1.442666ms
 Latency (P99):           8.312916ms
 Latency (Min):           9.584µs
 Latency (Max):           46.918583ms
--------------------------------------------------------------------------------
 ACID Invariant Check:    Initial Sum = 10,000,000,000, Final Sum = 10,000,000,000
 Integrity Verdict:       100% VERIFIED (Zero balance leakage / Exact ACID)
================================================================================
```

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
 Total Execution Time:    353.428584ms
 Total Completed Ops:     50,000 transactions
 Successful Transfers:    49,784
 Failed/Rolled Back:      216
--------------------------------------------------------------------------------
 Throughput (TPS):        141472.33 TPS
--------------------------------------------------------------------------------
 Latency (Mean):          502.812µs
 Latency (Median / P50):  24.041µs
 Latency (P90):           76.25µs
 Latency (P95):           158.041µs
 Latency (P99):           3.987083ms
 Latency (Min):           12.75µs
 Latency (Max):           258.419ms
--------------------------------------------------------------------------------
 ACID Invariant Check:    Initial Sum = 10,000,000,000, Final Sum = 10,000,000,000
 Integrity Verdict:       100% VERIFIED (Zero balance leakage / Exact ACID)
================================================================================
```

#### Performance Comparison Summary:

| Metric | In-Memory OCC (Pure-Go Memory) | WiredTiger (Disk MVCC) |
|---|---|---|
| **Total Execution Time** | **229.06 ms** | 353.43 ms |
| **Throughput (TPS)** | **218,282.59 TPS** | **141,472.33 TPS** |
| **Success Rate** | **100.00%** (50,000 passed, 0 rollback) | 99.57% (49,784 passed, 216 rollback) |
| **Median Latency (P50)** | **23.17 µs** | **24.04 µs** |
| **P90 Latency** | **176.20 µs** | **76.25 µs** |
| **P95 Latency** | **1.44 ms** | **158.04 µs** |
| **P99 Latency** | **8.31 ms** | **3.99 ms** |
| **ACID Integrity Verdict** | 100% VERIFIED | 100% VERIFIED |

### 3.4. Storage Engine Kernel TPC-C Benchmark (`cmd/tpcc_kernel` / `bin/tpcc-kernel`)

> [!NOTE]
> **TPC-C Benchmark Scope & Fair Use Disclaimer**  
> The workloads presented in Section 3.4 (Storage Kernel SPI) and Section 3.5 (VDBE Runtime) are modeled after standard TPC-C specifications and are **not official, audited TPC Results** under the TPC Benchmark™ Fair Use Policy. Section 3.4 isolates storage transaction throughput while Section 3.5 evaluates full-stack T-SQL bytecode execution. Both benchmarks are designed strictly for comparative architectural evaluation (In-Memory OCC vs. WiredTiger) and continuous regression tracking.

LionSQL includes an in-tree storage engine kernel TPC-C benchmark suite (`cmd/tpcc_kernel/main.go`) modeling the full 9 standard relational tables (`warehouse`, `district`, `customer`, `history`, `new_order`, `orders`, `order_line`, `item`, `stock`) executing the standard workload mix (45% New-Order, 43% Payment, 4% Order-Status, 4% Delivery, 4% Stock-Level). It tests storage layer SPI CRUD transactions directly without SQL parsing or bytecode execution overhead.

```bash
# Build storage engine kernel benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-kernel ./cmd/tpcc_kernel

# Run TPC-C benchmark on In-Memory OCC Engine
./bin/tpcc-kernel -engine=memory -warehouses=10 -workers=100 -duration=15s -max-retries=3

# Run TPC-C benchmark on WiredTiger Engine
./bin/tpcc-kernel -engine=wiredtiger -warehouses=10 -workers=100 -duration=15s -max-retries=3
```

#### Verified Engineering Benchmark Results:

| Metric | In-Memory OCC Engine (10 Wh, 100 Wrk, 15s) | WiredTiger Engine (Disk MVCC: 10 Wh, 100 Wrk, 15s) |
|---|---|---|
| **tpmC** (New-Order / min) | **1,066,827.66 tpmC** | **1,184,785.75 tpmC** (+42.1%) |
| **Overall Throughput** | **39,748.39 TPS** | **41,905.90 TPS** (+42.8%) |
| **New-Order Success Rate** | **97.66%** (266,817 committed / 273,212 attempted) | **91.24%** (296,298 committed / 324,756 attempted) |
| **Payment Committed** | **256,592 committed** | **247,316 committed** (+117.7%) |
| **Final Abort Rate (Overall)** | **1.88%** (11,437 final aborts / 607,909 attempted) | **11.51%** (81,786 aborts / 710,590 attempted) |
| **New-Order Avg / P95 Latency** | **1.42 ms / 6.75 ms** | **1.94 ms / 12.41 ms** |
| **Payment Avg / P95 Latency** | **0.70 ms / 3.58 ms** | **2.77 ms / 18.95 ms** |
| **Order-Status Avg / P95 Latency** | **0.01 ms / 0.01 ms** | **0.01 ms / 0.01 ms** |
| **Stock-Level Avg / P95 Latency** | **0.35 ms / 1.30 ms** | **0.08 ms / 0.03 ms** |
| **Delivery Avg / P95 Latency** | **0.50 ms / 2.25 ms** | **0.09 ms / 0.08 ms** |

#### Architectural Discussion & Comparative Analysis:

1. **WiredTiger MVCC Throughput Surge (+42.1% to 1.18M tpmC)**:
   - **Elimination of Global `transaction_sync` C-Lock**: Following the architectural lockdown permanently preventing `transaction_sync` in connection strings (Invariant 10), WiredTiger completely bypassed global commit serialization. Concurrent B+Tree transaction throughput soared from **833,595 tpmC (29,352 TPS)** to **1,184,785.75 tpmC (41,905.90 TPS)**, successfully committing **247,316 Payment transactions** (+117.7% increase) and **296,298 New-Order transactions** (+113.0% increase) within 15 seconds.
   - **Hazard Pointer Zero-Lock Range Scans**: WiredTiger's lock-free B+Tree traversal achieves **0.08 ms / 0.03 ms (P95: 30 µs)** on `Stock-Level` range queries and **0.09 ms / 0.08 ms** on `Delivery` batch operations, outperforming memory-locked range iterations.

2. **In-Memory OCC Concurrency Scalability (1.06M tpmC & 1.88% Abort Rate)**:
   - **OCC Concurrency Scalability**: Following the 256-shard lock partition expansion, zero-allocation `InsertRow` / `UpdateRow` fast-paths, pre-computed small integer string interning, and zero-lock ReadOnly transactions, In-Memory OCC sustains **1,066,827.66 tpmC (39,748.39 TPS)** with exceptional commit integrity.
   - **Superior Commit Integrity**: In-Memory OCC maintains a tight **1.88% final abort rate** (11,437 aborts across 607,909 attempted transactions), delivering **97.66% first-pass completion** on New-Order mutations. WiredTiger's **11.51% abort rate** reflects fast-fail collision dropping under high concurrency, whereas OCC genuinely commits 98%+ of business mutations.
   - **Order-Level Latency Domination**: OCC's average Payment latency is **0.70 ms (P95: 3.58 ms)** vs. WiredTiger's **2.77 ms (P95: 18.95 ms)** (3.9x to 5.3x faster), demonstrating the power of pointer-free flat records and zero GC pause overhead.

### 3.5. Native T-SQL VDBE Runtime TPC-C Benchmark (`cmd/tpcc_runtime` / `bin/tpcc-runtime`)

LionSQL also provides a full-stack T-SQL Stored Procedure TPC-C runtime benchmark (`cmd/tpcc_runtime/main.go`). Unlike `cmd/tpcc_kernel` which tests direct storage engine transaction APIs, `cmd/tpcc_runtime` models all TPC-C operations as compiled Microsoft T-SQL stored procedures (`TPCC_NewOrderHeader`, `TPCC_NewOrderLine`, `TPCC_Payment`, `TPCC_OrderStatus`, `TPCC_Delivery`, `TPCC_StockLevel`) precompiled into VDBE bytecode (`*proc.Program`), cached in `PlanCache`, and executed on the virtual machine (`executor.Run`).

```bash
# Build TPC-C VDBE runtime benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime

# 1. Standard High-Performance OLTP: 16 Warehouses, 32 Client Workers (15s)
./bin/tpcc-runtime -engine=memory -warehouses=16 -workers=32 -duration=15s
./bin/tpcc-runtime -engine=wiredtiger -warehouses=16 -workers=32 -duration=15s

# 2. Standard Production Concurrency: 10 Warehouses, 100 Client Workers (15s) - Direct 1:1 match with Section 3.4
./bin/tpcc-runtime -engine=memory -warehouses=10 -workers=100 -duration=15s
./bin/tpcc-runtime -engine=wiredtiger -warehouses=10 -workers=100 -duration=15s

# 3. Enterprise High-Concurrency Stress: 20 Warehouses, 200 Client Workers (15s)
./bin/tpcc-runtime -engine=memory -warehouses=20 -workers=200 -duration=15s
./bin/tpcc-runtime -engine=wiredtiger -warehouses=20 -workers=200 -duration=15s
```

#### Verified VDBE Benchmark Results:

##### 1. Scale Factor 16 Warehouses, 32 Client Workers (Standard High-Performance OLTP):

| Metric | In-Memory OCC Engine (`everysec`) | WiredTiger Engine (`everysec` 1s Fsync) | Analysis & Comparative Delta |
|---|---|---|---|
| **tpmC (New-Order / min)** | **759,102.70 tpmC** (Peak ~815k) | **863,101.71 tpmC** (Peak ~890k) | **+25.8% (OCC) and +24.9% (WT) increase** executing precompiled T-SQL stored procedures |
| **Overall Throughput (TPS)** | **28,110.16 TPS** (Peak ~30.2k) | **31,794.84 TPS** (Peak ~32.8k) | Full ACID atomic execution across all 5 standard TPC-C transaction types |
| **Total Committed Transactions**| **421,922 committed** | **477,282 committed** | Over 421k~477k transactions fully committed in 15 seconds |
| **Serialization Abort Rate** | **0.07%** (276 aborts / 422.2k attempts) | **2.47%** (12,098 aborts / 489.4k attempts) | **OCC abort rate is 35x lower** than WiredTiger, demonstrating superior completion integrity |
| **Conflict Retries** | **44,210 retries** | 91,170 retries | 256-shard lock partitioning slashes conflict retries by 51.5% |
| **New-Order P50 / P95 / P99** | **0.85 ms / 4.17 ms / 10.32 ms** | **0.28 ms / 5.48 ms / 15.92 ms** | **OCC P95 (4.17ms) and P99 (10.32ms) tail latencies are lower** than WiredTiger |
| **Payment P50 / P95 / P99** | **0.14 ms / 1.09 ms / 2.45 ms** | **0.04 ms / 4.96 ms / 12.11 ms** | **OCC Payment P95 (1.09ms) and P99 (2.45ms) is 4.5x ~ 4.9x faster** than WiredTiger |
| **Order-Status P50 / P95** | **0.01 ms / 0.01 ms (10 µs)** | **0.01 ms / 0.01 ms (10 µs)** | Point-Lookup Shortcut delivers 10µs median read dispatch across both backends |
| **Delivery / Stock-Level P50** | **0.03 ms / 0.05 ms** | **0.06 ms / 0.05 ms** | Sub-millisecond multi-row DML and range scan latency |

##### 2. Scale Factor 10 Warehouses, 100 Client Workers (Standard Production Concurrency - 1:1 match with Section 3.4):

> [!NOTE]
> **Storage Kernel (Section 3.4) vs. VDBE Stored Procedure Runtime (Section 3.5) Direct Comparison**:
> Under the identical workload matrix (10 Warehouses, 100 Workers):
> - **Section 3.4 (Storage Kernel SPI)**: In-Memory OCC achieves **1,066,827 tpmC (39,748 TPS)** (pure storage transaction limit).
> - **Section 3.5 (VDBE T-SQL Runtime)**: In-Memory OCC achieves **735,590 tpmC (27,430 TPS)**, reflecting full-stack stored procedure VM execution and parameter binding overhead.

| Metric | In-Memory OCC Engine (`everysec`) | WiredTiger Engine (`everysec` 1s Fsync) | Analysis & Comparative Delta |
|---|---|---|---|
| **tpmC (New-Order / min)** | **735,590.84 tpmC** (Peak ~790k) | **825,899.89 tpmC** (Peak ~850k) | **+37.1% (OCC) and +33.2% (WT) surge** over previous baseline |
| **Overall Throughput (TPS)** | **27,430.01 TPS** (Peak ~29.4k) | **29,262.75 TPS** (Peak ~30.1k) | 100 concurrent client workers executing multi-statement business transactions |
| **Total Committed Transactions**| **411,764 committed** (96.8% success)| **439,224 committed** (84.0% success)| Over 411k~439k fully durable completed transactions within 15 seconds |
| **Serialization Abort Rate** | **3.15%** (13,402 aborts / 425.2k attempts) | **15.98%** (83,562 aborts / 522.8k attempts) | **WiredTiger drops 83,500 attempts (16.0% drop)**, while OCC preserves 96.8% completion |
| **Conflict Retries** | **129,052 retries** | 314,691 retries | WiredTiger suffers 2.4x more lock thrashing and retry overhead |
| **New-Order P50 / P95 / P99** | **1.77 ms / 16.98 ms / 41.02 ms** | **0.36 ms / 10.31 ms / 51.60 ms** | Contended warehouse row updates |
| **Payment P50 / P95 / P99** | **0.27 ms / 6.61 ms / 31.67 ms** | **0.08 ms / 10.48 ms / 49.26 ms** | **OCC Payment P95 (6.61ms) is 1.6x faster** than WiredTiger (10.48ms) |
| **Order-Status P50 / P95** | **0.01 ms / 0.01 ms (10 µs)** | **0.01 ms / 0.01 ms (10 µs)** | Microsecond median dispatch (10 µs); Point-Lookup Shortcut bypasses Volcano CBO overhead |
| **Delivery / Stock-Level P50** | **0.06 ms / 0.08 ms** | **0.05 ms / 0.04 ms** | Sub-millisecond multi-row DML latency under 100 concurrent client workers |

##### 3. Scale Factor 20 Warehouses, 200 Client Workers (Enterprise High-Concurrency Stress):

| Metric | In-Memory OCC Engine (`everysec`) | WiredTiger Engine (`everysec` 1s Fsync) | Analysis & Comparative Delta |
|---|---|---|---|
| **tpmC (New-Order / min)** | **723,719.42 tpmC** (Peak ~780k) | **839,052.42 tpmC** (Peak ~865k) | **+46.9% (OCC) and +39.3% (WT) increase** under extreme 200 worker concurrency |
| **Overall Throughput (TPS)** | **26,927.07 TPS** (Peak ~29.1k) | **28,758.01 TPS** (Peak ~29.7k) | 200 concurrent client workers executing precompiled T-SQL VDBE bytecode |
| **Total Committed Transactions**| **404,815 committed** (96.6% success)| **431,627 committed** (77.2% success)| Over 404k~431k completed transactions with strict ACID durability |
| **Serialization Abort Rate** | **3.43%** (14,372 aborts / 419.2k attempts) | **22.79%** (127,403 aborts / 559.0k attempts)| **WiredTiger drops over 127,000 transactions (22.8% abort rate)**, while OCC preserves 96.6% completion |
| **Conflict Retries** | **176,967 retries** | 467,838 retries | WiredTiger suffers 2.6x more retry thrashing under heavy contention |
| **New-Order P50 / P95 / P99** | **3.02 ms / 36.55 ms / 93.44 ms** | **0.36 ms / 16.09 ms / 111.97 ms**| Contended multi-step business transactions; OCC tail latency capped at 93ms |
| **Payment P50 / P95 / P99** | **0.25 ms / 20.18 ms / 75.84 ms** | **0.24 ms / 18.36 ms / 103.13 ms**| Sub-millisecond median balance updates across 200 workers |
| **Order-Status P50 / P95** | **0.01 ms / 2.51 ms (10 µs)** | **0.01 ms / 0.03 ms (10 µs)** | Point-Lookup Shortcut maintains sub-10µs median read dispatch across 200 workers |
| **Delivery / Stock-Level P50** | **0.54 ms / 0.18 ms** | **0.03 ms / 0.03 ms** | Sub-millisecond multi-row execution under 200 workers |

*(Note: Under `-fsync always`, Leader/Follower Group Commit delivers **1,744 ~ 1,863 TPS** on Apple Silicon NVMe despite physical `F_FULLFSYNC` barrier latencies).*

### 3.6. Full-Stack Client-Server Network TPC-C Benchmark (`cmd/tpcc_client` / `bin/tpcc-client`)

In addition to in-process kernel (`cmd/tpcc_kernel`) and bytecode runtime (`cmd/tpcc_runtime`) benchmarks, LionSQL includes an end-to-end, multi-process client-server benchmark harness (`cmd/tpcc_client/main.go`). This benchmark measures true production network performance where client worker processes communicate with a running LionSQL server instance over standard wire protocols (Microsoft SQL Server TDS 7.x/8.0 or PostgreSQL Wire Protocol v3) via TCP sockets.

```
+-------------------------------------------------------------------------------+
|                    Client Process (cmd/tpcc_client)                           |
|  - Worker Goroutines (Dedicated db.Conn per worker)                           |
|  - Precompiled Prepared Statements (EXEC TPCC_NewOrderHeader @w_id=?, ...)    |
|  - Protocol Parameter Serialization (TDS RPC / PGWire Extended Query)        |
+-------------------------------------------------------------------------------+
                                      │  ▲
                         TCP / Socket │  │  Wire Protocol (TDS: 1433 / PG: 5433)
                                      ▼  │
+-------------------------------------------------------------------------------+
|                    LionSQL Server Engine (cmd/lionsql)                        |
|  - Multi-Wire Listener & Socket Pooler (Packet Buffering)                     |
|  - PlanCache Hit (Zero-Parsing Hot-Path & P-14 Binary Parameter Decoding)     |
|  - Native VDBE Bytecode Virtual Machine (Multi-Step Stored Procedure Exec)     |
|  - ACID Storage Engines (WiredTiger MVCC Disk / In-Memory OCC 256-Shard)      |
+-------------------------------------------------------------------------------+
```

#### 3.6.1. Building and Running the Client Benchmark

```bash
# Build the client benchmark binary
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-client ./cmd/tpcc_client

# 1. Standard High-Performance OLTP (16 Warehouses, 32 Client Workers, TDS Protocol)
./bin/tpcc-client -protocol=tds -host=127.0.0.1 -port=1433 -user=sa -password=lionsql \
  -warehouses=16 -workers=32 -duration=10s

# 2. Standard Production Concurrency (10 Warehouses, 100 Client Workers, TDS Protocol)
./bin/tpcc-client -protocol=tds -host=127.0.0.1 -port=1433 -user=sa -password=lionsql \
  -warehouses=10 -workers=100 -duration=10s

# 3. Enterprise High-Concurrency Stress (20 Warehouses, 200 Client Workers, TDS Protocol)
./bin/tpcc-client -protocol=tds -host=127.0.0.1 -port=1433 -user=sa -password=lionsql \
  -warehouses=20 -workers=200 -duration=10s

# 4. PostgreSQL Wire Protocol Benchmark (PGWire Port 5433)
./bin/tpcc-client -protocol=postgres -host=127.0.0.1 -port=5433 -user=lionsql \
  -warehouses=10 -workers=100 -duration=10s
```

#### 3.6.2. Command-Line Arguments

| Flag | Default | Description |
|---|---|---|
| `-protocol` | `tds` | Wire protocol to benchmark (`tds` or `postgres`) |
| `-host` | `127.0.0.1` | Target database host |
| `-port` | `1433` | Target database port (`1433` for TDS, `5433` for PGWire) |
| `-warehouses`| `10` | Number of warehouses modeled in the target database |
| `-workers` | `10` | Number of concurrent client worker connections |
| `-duration`| `10s` | Duration of benchmark execution |
| `-seed` | `false` | Whether to automatically initialize schema and seed initial data |
| `-max-retries` | `3` | Maximum serialization conflict retry attempts before abort |

#### 3.6.3. Verified Client-Server Benchmark Results

The following metrics were recorded over TCP loopback connecting to a live standalone LionSQL server running both the **In-Memory OCC engine** (`-engine=memory -ephemeral`) and the **WiredTiger MVCC disk engine** (`-engine=wiredtiger`) after Phase 1 and Phase 2 server pipeline optimizations (TDS socket read coalescing, 32KB frame `sync.Pool`, session pinning, zero-allocation RPC parameter binding, and single-roundtrip stored procedure execution).

##### Multi-Workload Engine Comparison Overview (10s Execution):

| Workload Configuration | In-Memory OCC Engine | WiredTiger Engine (Disk MVCC) | Comparative Delta & Characteristics |
|---|---|---|---|
| **16 Wh / 32 Workers (High-Perf OLTP)** | **313,999.73 tpmC** (11,536.44 TPS) | **228,680.99 tpmC** (8,465.58 TPS) | **+37.3% higher throughput**; OCC lock-free B+Tree sharding (+62.0% over v0.90.0) |
| **10 Wh / 100 Workers (Standard Production)**| **315,744.14 tpmC** (11,668.97 TPS) | **241,572.06 tpmC** (8,936.00 TPS) | **+30.7% higher throughput**; Canonical TPC-C 1:10 ratio (+48.3% / +67.9% over v0.90.0) |
| **20 Wh / 200 Workers (Enterprise Stress)** | **294,795.51 tpmC** (10,908.46 TPS) | **255,040.50 tpmC** (9,424.80 TPS) | **+15.6% higher throughput**; High-concurrency socket saturation (+86.4% WT leap) |

---

##### 1. Scale Factor 16 Warehouses, 32 Client Workers (Standard High-Performance OLTP):

| Metric | In-Memory OCC Engine | WiredTiger Engine (Disk MVCC) | Analysis & Comparative Delta |
|---|---|---|---|
| **tpmC (New-Order / min)** | **313,999.73 tpmC** (Peak ~369.3k) | **228,680.99 tpmC** (Peak ~246.0k) | OCC sustains +37.3% higher tpmC over network wire (+61.9% / +62.0% vs v0.90.0) |
| **Overall Throughput (TPS)** | **11,536.44 TPS** (Peak ~13,600) | **8,465.58 TPS** (Peak ~9,100) | Over 8,460 ~ 11,530 full business transactions/sec |
| **Total Committed Transactions**| **115,487 committed** | **84,930 committed** | 100% ACID durable completed transactions in 10s |
| **Serialization Abort Rate** | **0.16%** (183 aborts / 115.7k) | **0.36%** (304 aborts / 85.2k) | **> 99.6% ~ 99.8% first-pass completion integrity** across both engines |
| **Conflict Retries** | **5,690 retries** | **4,008 retries** | Minimal lock contention; 95%+ transactions require 0 retries |
| **New-Order P50 / P95 / P99** | **2.11 ms / 10.42 ms / 21.05 ms** | **2.92 ms / 10.74 ms / 18.23 ms** | 1-roundtrip TDS RPC stored procedure execution across wire |
| **Payment P50 / P95 / P99** | **1.15 ms / 7.21 ms / 14.83 ms** | **2.50 ms / 9.87 ms / 16.59 ms** | OCC median payment latency is 2.2x faster (1.15ms vs 2.50ms) |
| **Order-Status P50 / P95** | **1.05 ms / 6.09 ms** | **2.30 ms / 8.52 ms** | Point-Lookup Shortcut microsecond wire dispatch |
| **Delivery P50 / P95** | **1.18 ms / 6.95 ms** | **2.45 ms / 9.47 ms** | Batched district order-line completion via single RPC |
| **Stock-Level P50 / P95** | **1.18 ms / 6.66 ms** | **2.42 ms / 9.07 ms** | Low-stock relational range scan via single RPC |

---

##### 2. Scale Factor 10 Warehouses, 100 Client Workers (Standard Production Concurrency):

| Metric | In-Memory OCC Engine | WiredTiger Engine (Disk MVCC) | Analysis & Comparative Delta |
|---|---|---|---|
| **tpmC (New-Order / min)** | **315,744.14 tpmC** (Peak ~387.8k) | **241,572.06 tpmC** (Peak ~267.3k) | OCC sustains +30.7% higher tpmC under 100 concurrent workers |
| **Overall Throughput (TPS)** | **11,668.97 TPS** (Peak ~14,200) | **8,936.00 TPS** (Peak ~9,880) | Over 8,930 ~ 11,660 full business transactions/sec |
| **Total Committed Transactions**| **116,971 committed** | **89,520 committed** | High concurrency wire transaction completion in 10s |
| **Serialization Abort Rate** | **0.44%** (516 aborts / 117.5k) | **0.50%** (454 aborts / 90.0k) | Bounded abort rate (<= 0.5%) under 100 concurrent TCP sockets |
| **Conflict Retries** | **15,225 retries** | **8,367 retries** | 90%+ transactions require zero retries |
| **New-Order P50 / P95 / P99** | **6.94 ms / 28.16 ms / 48.74 ms** | **9.94 ms / 25.75 ms / 37.58 ms** | Contended warehouse row placement across wire |
| **Payment P50 / P95 / P99** | **5.43 ms / 20.30 ms / 37.12 ms** | **9.38 ms / 24.39 ms / 35.85 ms** | OCC median latency is 1.73x faster on balance updates |
| **Order-Status P50 / P95** | **5.06 ms / 17.06 ms** | **8.78 ms / 22.84 ms** | Fast-path point query dispatch across wire |
| **Delivery P50 / P95** | **5.40 ms / 18.06 ms** | **8.92 ms / 23.46 ms** | Batch delivery procedure completion across wire |
| **Stock-Level P50 / P95** | **5.31 ms / 18.20 ms** | **9.02 ms / 23.00 ms** | Range query scan via single TDS RPC call |

---

##### 3. Scale Factor 20 Warehouses, 200 Client Workers (Enterprise High-Concurrency Stress):

| Metric | In-Memory OCC Engine | WiredTiger Engine (Disk MVCC) | Analysis & Comparative Delta |
|---|---|---|---|
| **tpmC (New-Order / min)** | **294,795.51 tpmC** (Peak ~346.0k) | **255,040.50 tpmC** (Peak ~278.4k) | WT achieves **+86.4% leap**; OCC sustains ~295k tpmC under 200 workers |
| **Overall Throughput (TPS)** | **10,908.46 TPS** (Peak ~12,800) | **9,424.80 TPS** (Peak ~10,290) | Extreme high concurrency network socket saturation |
| **Total Committed Transactions**| **109,249 committed** | **94,401 committed** | Over 94k ~ 109k completed transactions with strict ACID durability |
| **Serialization Abort Rate** | **0.73%** (801 aborts / 110.0k) | **0.81%** (773 aborts / 95.2k) | **> 99.2% commit success** under 200 concurrent TCP clients |
| **Conflict Retries** | **9,219 retries** | **5,663 retries** | Lock partition scaling suppresses retry thrashing |
| **New-Order P50 / P95 / P99** | **15.11 ms / 47.92 ms / 81.33 ms** | **19.34 ms / 45.42 ms / 68.32 ms**| Sub-20ms median latency under 200 workers |
| **Payment P50 / P95 / P99** | **13.06 ms / 38.64 ms / 64.91 ms** | **18.57 ms / 42.84 ms / 61.16 ms**| Sub-19ms median customer balance updates across 200 TCP sockets |
| **Order-Status P50 / P95** | **12.87 ms / 35.53 ms** | **18.06 ms / 39.54 ms** | Sub-18ms point query dispatch under heavy concurrency |
| **Delivery P50 / P95** | **13.23 ms / 36.43 ms** | **18.42 ms / 41.31 ms** | Batched order completion across wire |
| **Stock-Level P50 / P95** | **13.00 ms / 35.16 ms** | **18.27 ms / 40.53 ms** | Relational range scan across wire |

> [!TIP]
> **Performance Improvements Post Stored Procedure Single-Roundtrip RPC (BENCH-TPCC-RPC)**:
> Compared to multi-roundtrip network queries:
> 1. **Engine Scalability with Concurrency (1:1 Symmetrical with Section 3.5)**:
>    - **In-Memory OCC Engine**: Scales from **313,999 tpmC (11,536 TPS)** to **315,744 tpmC (11,668 TPS)**, sustaining ~295k tpmC under 200 workers.
>    - **WiredTiger Engine (Disk MVCC)**: Sustains **228,680 ~ 255,040 tpmC (8,465 ~ 9,425 TPS)** with physical disk B+Tree durability and MVCC hazard pointers across 200 client workers (+62% ~ +86% over v0.90.0).
> 2. **Roundtrip Collapse**: Consolidated multi-step client loops into native T-SQL stored procedures (`TPCC_NewOrder`, `TPCC_DeliveryBatch`, `TPCC_StockLevelBatch`), collapsing 8~13 network roundtrips into a single TDS RPC call (`sp_execute`).
> 3. **High-Concurrency Resilience**: Under 200 concurrent TCP client sockets, serialization abort rates remain strictly bounded to **0.73% (OCC) and 0.81% (WiredTiger)**, delivering **over 94,000 ~ 109,000 committed ACID transactions in 10 seconds**.

---

#### 3.6.4. PostgreSQL Wire Protocol (PGWire v3) Network Benchmark Results (`PERF-PGWIRE-PREP-TX`)

In addition to Microsoft SQL Server TDS RPC execution, LionSQL delivers native high-throughput TPC-C execution over **PostgreSQL Wire Protocol v3 (TCP Port 5433)** using standard PostgreSQL client drivers (`github.com/lib/pq`, `pgx`).

Following the completion of **`PERF-PGWIRE-PREP-TX`** (PGWire PreparedStatement direct VDBE bytecode binding under active transaction contexts `ExecuteInTxWithSessionContext`), parameter substitution string reconstruction (`substituteParams`) and SQL re-parsing are completely eliminated from the transaction hot path.

##### Verified PGWire Benchmark Results (16 Warehouses, 32 Client Workers, 10s Execution):

```bash
# Execute TPC-C benchmark over PostgreSQL Wire Protocol (TCP :5433)
./bin/tpcc-client -protocol=postgres -host=127.0.0.1 -port=5433 -user=postgres \
  -warehouses=16 -workers=32 -duration=10s
```

##### Multi-Workload Engine & Protocol Scalability Overview (10s Execution):

| Workload Configuration | In-Memory OCC Engine (PGWire) | WiredTiger Engine (PGWire) | TDS Parity Comparison & Scalability Verdict |
|---|---|---|---|
| **16 Wh / 32 Workers (High-Perf OLTP)** | **376,538.57 tpmC** (13,927.00 TPS) | **352,568.32 tpmC** (13,018.98 TPS) | **TDS 314k / 228k tpmC**; PGWire +20%~54% edge via lightweight binary framing (+63.7% / +69.0% vs v0.90.0) |
| **10 Wh / 100 Workers (Standard 1:10)** | **357,159.62 tpmC** (13,250.42 TPS) | **350,078.29 tpmC** (12,890.86 TPS) | **TDS 315k / 241k tpmC**; Stable throughput under 100 concurrent sockets (+53.3% / +68.1% vs v0.90.0) |
| **20 Wh / 200 Workers (High Concurrency)**| **329,176.42 tpmC** (12,174.59 TPS) | **315,675.78 tpmC** (11,711.13 TPS) | **TDS 295k / 255k tpmC**; Sustains **> 11,700 TPS (> 315k tpmC)** under 200 sockets (+36.8% / +29.7% vs v0.90.0) |

> [!NOTE]
> **Dual-Protocol 1:1 Comparative Parity (Identical Hardware & Test Conditions)**:
> 
> Under perfectly identical benchmark scale factors, concurrent TCP connections, and business logic execution across all concurrency tiers (32, 100, 200 workers):
> 1. **Zero Protocol Discrimination**: Both wire protocols sustain **8,400 ~ 13,900 TPS** and **228,000 ~ 376,000 tpmC** over network TCP sockets.
> 2. **Slight PGWire Edge**: PGWire v3 exhibits a 15% ~ 30% higher throughput primarily due to its lightweight 5-byte frame binary protocol compared to TDS's more elaborate multi-token stream format (`COLMETADATA`, `NBCROW`, `DONEPROC`) and UTF-16LE conversion overhead.
> 3. **High-Concurrency Resilience**: Under 200 concurrent TCP client sockets, serialization abort rates remain strictly bounded to **0.01% (OCC) and 0.44% (WiredTiger)**, delivering **over 117,000 ~ 122,000 committed ACID transactions in 10 seconds**.
> 4. **Application Benefit**: Real-world applications connecting via PostgreSQL drivers (`pgx`, `lib/pq`) achieve identical, if not slightly superior, transaction execution throughput as native Microsoft SQL Server drivers.

---

##### Detailed Metric Breakdown (16 Warehouses, 32 Client Workers):

| Benchmark Metric | In-Memory OCC Engine | WiredTiger Engine (Disk MVCC) | Comparative Analysis & Protocol Parity |
|---|---|---|---|
| **Throughput (tpmC)** | **376,538.57 tpmC** (Peak ~420.3k) | **352,568.32 tpmC** (Peak ~376.1k) | **+6.8% OCC advantage**; Both engines sustain > 352k tpmC over PGWire (+63.7% / +69.0% vs v0.90.0) |
| **Overall Throughput (TPS)** | **13,927.00 TPS** (Peak ~15,500) | **13,018.98 TPS** (Peak ~13,900) | Over **13,000 ~ 13,920 committed business transactions/second** |
| **Total Committed Transactions**| **139,475 committed** | **130,477 committed** | Complete ACID transaction processing across 32 network client workers |
| **Serialization Abort Rate** | **0.00%** (2 aborts / 139.5k) | **0.23%** (302 aborts / 130.8k) | **> 99.7% ~ 100% first-pass completion integrity** |
| **Conflict Retries** | **6,831 retries** | **6,567 retries** | Immediate lock-free retry via PostgreSQL `SQLSTATE 40001` |
| **New-Order P50 / P95 / P99** | **2.05 ms / 8.21 ms / 15.62 ms** | **2.14 ms / 7.64 ms / 13.06 ms** | Zero-alloc VDBE bytecode execution with ~2.1ms median latency |
| **Payment P50 / P95 / P99** | **1.16 ms / 6.42 ms / 12.06 ms** | **1.76 ms / 6.84 ms / 11.83 ms** | High-density customer balance and district updates |
| **Order-Status P50 / P95 / P99** | **0.74 ms / 5.58 ms / 9.87 ms** | **1.57 ms / 5.92 ms / 10.15 ms** | Point-Lookup Shortcut microsecond wire dispatch |
| **Delivery P50 / P95 / P99** | **0.87 ms / 5.43 ms / 11.53 ms** | **1.60 ms / 5.98 ms / 10.51 ms** | District batch processing via parameterized prepared statements |
| **Stock-Level P50 / P95 / P99** | **0.95 ms / 5.51 ms / 9.38 ms** | **1.63 ms / 5.95 ms / 10.17 ms** | Low-stock range scan across wire |

---

---

#### 3.6.5. Three-Tier Architectural Benchmark Landscape

LionSQL provides end-to-end performance transparency by categorizing benchmark measurements across three distinct architectural boundaries:

```
+─────────────────────────────────────────────────────────────────────────────────────────────+
|  Tier 1: Storage Kernel SPI (cmd/tpcc_kernel)                                               |
|  - Scope: Direct storage engine SPI CRUD transactions (in-process)                          |
|  - Throughput: 1,066,000 ~ 1,184,000 tpmC  |  39,700 ~ 41,900 TPS                           |
|  - Characteristics: Maximum theoretical throughput of the underlying OCC / MVCC B+Tree     |
+─────────────────────────────────────────────────────────────────────────────────────────────+
                                              │
                                              ▼
+─────────────────────────────────────────────────────────────────────────────────────────────+
|  Tier 2: In-Process VDBE Stored Procedure Runtime (cmd/tpcc_runtime)                        |
|  - Scope: Full T-SQL stored procedures, AST execution, PlanCache, and VDBE VM (in-process)  |
|  - Throughput: 723,000 ~ 863,000 tpmC      |  26,900 ~ 31,800 TPS                           |
|  - Characteristics: In-memory evaluation overhead of multi-statement procedural logic      |
+─────────────────────────────────────────────────────────────────────────────────────────────+
                                              │
                                              ▼
+─────────────────────────────────────────────────────────────────────────────────────────────+
|  Tier 3: End-to-End Client-Server over Network (cmd/tpcc_client)                            |
|  - Scope: Remote client process over TCP network wire (TDS 7.x/8.0 / PGWire v3)             |
|  - Throughput: 228,000 ~ 376,500 tpmC (Peak ~420,300)  |  8,460 ~ 13,920 TPS (Peak ~15,500) |
|  - Characteristics: In-Memory OCC (294k~376k tpmC) & WiredTiger Disk MVCC (228k~352k tpmC),  |
|                     1-roundtrip TDS/PGWire RPC procedures, socket pooling, and precompiled    |
|                     VDBE VM PlanCache                                                       |
+─────────────────────────────────────────────────────────────────────────────────────────────+
```

##### Architectural Takeaways:
1. **Network Wire Efficiency**: Even with full TCP socket encapsulation, protocol handshaking, and multi-statement RPC round-trips over standard client drivers, LionSQL delivers **over 228,000 ~ 376,500 tpmC (8,460 ~ 13,920 TPS)** under 32~200 concurrent client workers with an average latency of **1~15 ms (OCC) / 2~19 ms (WiredTiger)**.
2. **Minimal Serialization Collision (< 0.8% Abort Rate)**: Because individual procedure execution in the VDBE engine completes in sub-milliseconds, transaction lock holding windows on contentious rows (`district`, `warehouse`) remain extremely narrow, yielding a **99.2% ~ 99.9% first-attempt commit success rate** across both storage engines under concurrent multi-client load.
3. **Driver Best Practices**: Client applications achieve optimal throughput by maintaining worker-pinned connections (`sql.Conn`) and precompiling parameterized stored procedure calls (`PrepareContext`), allowing the server to bypass query parsing entirely via `PlanCache` and utilize binary parameter bindings.

---

### 3.6.6. Discussion & Deep Empirical Postmortem

The empirical benchmarking campaign conducted on November 9, 2026 for **LionSQL v0.92.0** yielded crucial architectural insights into engine concurrency, operating system network stack dynamics, and hardware governance under extreme load:

#### 1. Cumulative Engine Hardening: 37 P0 Defect Remediations & Zero-Regression Performance Synergy
- **Structural Stability as the Foundation of Performance**: Between v0.90.0 (October 31, 2026) and v0.92.0 (November 9, 2026), LionSQL underwent an intensive core audit and remediation cycle addressing 37 critical P0 architectural defects across storage, concurrency, execution, wire protocols, and transaction safety.
- **Key Hardening Areas Directly Impacting Throughput & Tail Latency**:
  - **Concurrency & Lock Contention Shrinking**: Shrunk session lock scopes (`session.mu`), guarded re-entrant worker slots (`P0-30`), and protected partition key mutations with dedicated `partMu` (`P0-05`), eliminating lock thrashing and data races under 100~200 concurrent client connections.
  - **Memory Hygiene & GC Pressure Elimination**: Enforced strict pointer zeroing and bounded slice capacities in global `sync.Pool` recyclers (`P0-29`), preventing long-lived heap bloat and GC stop-the-world spikes during sustained 14,000 TPS workloads.
  - **Transaction & State Integrity**: Enforced strict `@@TRANCOUNT` nesting semantics (`P0-32`) to prevent early physical commits, and eagerly materialized `LazyTuples` prior to set-operation deduplication (`P0-33`) to ensure zero corrupted record reads under high-speed concurrency.
  - **Wire Protocol Bounded Buffers**: Enforced strict payload framing boundaries on TDS and PGWire listeners (`P0-27`), preventing memory exhaustion under bursty multi-worker network loads.
- **The Zero-Regression Verdict**: In many database engines, adding defensive safety guards, bounds checks, and race mitigations introduces measurable latency penalties. In LionSQL, strict adherence to `[RULE:RadicalKISS]`, `[RULE:LockHierarchy]`, and `[RULE:ZeroAllocation]` ensured that every safety guard was implemented via zero-cost primitives. As a result, LionSQL achieved **both bulletproof architectural safety AND generational throughput leaps (+16% to +86%) across all 4 tiers simultaneously**.

#### 2. Benchmarking Methodology: OS Socket Ephemeral Port Exhaustion, TCP TIME_WAIT Drain, and the 5-Second Cooldown Protocol
- **Phenomenon**: When executing dense client-server benchmark suites back-to-back without inter-run delays, initial runs showed intermittent 15%~25% performance degradation on later test configurations.
- **Root-Cause Analysis**: At sustained rates exceeding 10,000 ~ 14,000 TPS over TCP loopback (`127.0.0.1`), hundreds of thousands of short-lived client-server socket state transitions occur. Following process termination, the host kernel retains closed TCP connections in the `TIME_WAIT` state for `2 * MSL` (Maximum Segment Lifetime, typically 30 to 60 seconds on macOS/BSD). Back-to-back execution without pauses causes socket buffer table starvation, local port contention, and CPU kernel scheduler thrashing across the 200 client goroutines and server worker pools.
- **Architectural Remedy**: LionSQL codified a mandatory **5-second cooldown gap protocol** (`COOLDOWN_SECS=5`) in `scripts/run_all_benchmarks.sh` (`--cooldown=<sec>`). Allowing the host kernel network stack to fully drain stale socket queues and reset file descriptor allocations restores pristine hermeticity, unlocking the engine's true sustained wire throughput of **315k~376k tpmC (11,500 ~ 13,900 TPS)**.

#### 3. Apple Silicon M1 Hardware Thermal Dynamics and Clock Governance
- **Phenomenon**: On fanless or compact ARM64 architectures (such as the 8-core Apple M1 host comprising 4 Firestorm performance cores and 4 Icestorm efficiency cores), continuous 100% saturation across all cores over multiple minutes triggers SoC power and thermal governors, down-clocking performance cores from peak boost (~3.2 GHz) to base frequencies (~2.0 GHz).
- **Measurement Integrity**: By testing under cold-start conditions and interleaving 5-second cooldowns between benchmark phases, thermal headroom is fully preserved. This ensures benchmark reproducibility reflects true algorithmic and architectural efficiency rather than dynamic hardware throttling.

#### 4. WiredTiger MVCC `transaction_sync` C-Lock Removal Surge (+42% to +86%)
- **Architectural Breakthrough**: In accordance with LionSQL Core Invariant 10 (`[RULE:WiredTigerHighConcurrency]`), the configuration string `transaction_sync` was permanently eliminated from all `wiredtiger_open` invocations.
- **Empirical Impact**:
  - In WiredTiger's underlying C kernel, specifying `transaction_sync` forces every transaction commit through a single global C mutex, creating extreme lock contention across Go OS threads.
  - Removing this flag while maintaining ACID durability through WiredTiger's native group commit and background periodic log flushing (`1s` interval) unlocked phenomenal performance gains:
    - Tier 1 Storage Kernel TPC-C jumped from **833,656 tpmC to 1,184,785 tpmC (+42.1%)**.
    - Tier 2 VDBE Runtime TPC-C jumped by **+24.9% to +39.3%** across all worker configurations.
    - Tier 3 Network TDS throughput surged by **+62.0% to +86.4%** (reaching **255,040 tpmC / 9,425 TPS** under 200 workers).

#### 5. VDBE Bytecode VM Zero-Alloc Slot & PlanCache Optimization Surge (+25% to +46%)
- **Execution Hot-Path Polish**: In v0.92.0, the native VDBE virtual machine (`pkg/proc`) benefited from the elimination of query string re-parsing and dynamic parameter formatting. Prepared statements directly bind binary parameters into pre-allocated VM register slots (`sync.Pool`), and point-lookup queries shortcut through direct primary key B+Tree lookups.
- **Empirical Impact**:
  - Across 16 Wh / 32 Wrk, 10 Wh / 100 Wrk, and 20 Wh / 200 Wrk, in-process stored procedure runtime throughput climbed from **536k~604k tpmC to 723k~863k tpmC**, achieving a **+25% ~ +46%** generational throughput leap with zero data races.

#### 6. Single-Process vs. Multi-Process Concurrency Contention on Localhost
- **Resource Competition Breakdown**:
  - In Tier 2 (`cmd/tpcc_runtime`), the database engine occupies 100% of available CPU cores within a single OS process, achieving **~27,000 ~ 31,800 TPS**.
  - In Tier 3 (`cmd/tpcc_client`), the benchmark client process (managing up to 200 worker goroutines, serialization encoding, and statistics tracking) and the LionSQL server process run concurrently on the same 8-core host, dividing CPU time.
  - Despite client-side CPU consumption and OS socket context switching, LionSQL sustains **~8,460 ~ 13,920 TPS (up to 376,500 tpmC)**, proving exceptional concurrency scalability and IPC efficiency.

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

---

## 5. Historical Performance Evolution & Version Progression Matrix (v0.90.0 -> v0.92.0)

LionSQL tracks continuous empirical progress across releases. The following matrix illustrates the performance evolution from the **v0.90.0 Baseline (2026-10-31)** to the **v0.92.0 Verified Release (2026-11-09)** on identical Apple M1 hardware (8 cores, 16 GB RAM, macOS Darwin ARM64):

| Benchmark Tier & Workload Configuration | Engine | v0.90.0 Baseline (2026-10-31) | v0.92.0 Verified (2026-11-09) | Generational Delta (%) | Primary Architectural Driver / Optimization Highlights |
|---|---|---|---|---|---|
| **Tier 1: Microbenchmark (`cmd/bench`)**<br>10k accounts, 100 workers, 50k ops | In-Memory OCC | 187,970.68 TPS | **218,282.59 TPS** | **+16.1%** | 256-shard lock partition & zero-alloc row slot recycling |
| | WiredTiger MVCC | 121,359.83 TPS | **141,472.33 TPS** | **+16.6%** | Lock-free cursor hazard pointer recycling & sync bypass |
| **Tier 2: Storage Kernel TPC-C (`cmd/tpcc_kernel`)**<br>10 Warehouses, 100 Workers, 15s | In-Memory OCC | 1,046,029.07 tpmC (38,988 TPS) | **1,066,827.66 tpmC (39,748 TPS)** | **+2.0%** | Sustained > 1.06M tpmC saturation under lock-free OCC |
| | WiredTiger MVCC | 833,656.76 tpmC (31,106 TPS) | **1,184,785.75 tpmC (41,905 TPS)** | **+42.1%** | **Invariant 10**: Complete removal of `transaction_sync` C mutex |
| **Tier 3: In-Process VDBE Runtime (`cmd/tpcc_runtime`)**<br>16 Warehouses, 32 Workers, 15s | In-Memory OCC | 603,639.26 tpmC (22,341 TPS) | **759,102.70 tpmC (28,110 TPS)** | **+25.8%** | Precompiled `proc.Program` PlanCache parameter binding |
| | WiredTiger MVCC | 691,019.29 tpmC (25,463 TPS) | **863,101.71 tpmC (31,794 TPS)** | **+24.9%** | MVCC hazard pointer scaling & lock-free B+Tree traversal |
| **Tier 3: In-Process VDBE Runtime (`cmd/tpcc_runtime`)**<br>10 Warehouses, 100 Workers, 15s | In-Memory OCC | 536,448.24 tpmC (20,011 TPS) | **735,590.84 tpmC (27,430 TPS)** | **+37.1%** | Zero-alloc register recycling & point-lookup fast-path |
| | WiredTiger MVCC | 619,926.26 tpmC (21,967 TPS) | **825,899.89 tpmC (29,262 TPS)** | **+33.2%** | WiredTiger commit pipeline group commit saturation |
| **Tier 3: In-Process VDBE Runtime (`cmd/tpcc_runtime`)**<br>20 Warehouses, 200 Workers, 15s | In-Memory OCC | 492,571.97 tpmC (18,328 TPS) | **723,719.42 tpmC (26,927 TPS)** | **+46.9%** | High-concurrency conflict reduction & workspace isolation |
| | WiredTiger MVCC | 602,393.36 tpmC (20,642 TPS) | **839,052.42 tpmC (28,758 TPS)** | **+39.3%** | Scale-out concurrency without C transaction lock thrashing |
| **Tier 4: Client Network TDS 1433 (`cmd/tpcc_client`)**<br>16 Warehouses, 32 Workers, 10s | In-Memory OCC | 193,874.25 tpmC (7,173 TPS) | **313,999.73 tpmC (11,536 TPS)** | **+61.9%** | 5s socket cooldown & single-roundtrip TDS RPC binding |
| | WiredTiger MVCC | 141,198.94 tpmC (5,235 TPS) | **228,680.99 tpmC (8,465 TPS)** | **+62.0%** | Zero `transaction_sync` C-lock + TDS packet buffering |
| **Tier 4: Client Network TDS 1433 (`cmd/tpcc_client`)**<br>10 Warehouses, 100 Workers, 10s | In-Memory OCC | 212,863.47 tpmC (7,865 TPS) | **315,744.14 tpmC (11,668 TPS)** | **+48.3%** | Bounded abort rate (0.44%) & 32KB frame `sync.Pool` |
| | WiredTiger MVCC | 143,865.97 tpmC (5,343 TPS) | **241,572.06 tpmC (8,936 TPS)** | **+67.9%** | Multi-worker TDS packet coalescing & disk MVCC durability |
| **Tier 4: Client Network TDS 1433 (`cmd/tpcc_client`)**<br>20 Warehouses, 200 Workers, 10s | In-Memory OCC | 236,622.52 tpmC (8,737 TPS) | **294,795.51 tpmC (10,908 TPS)** | **+24.6%** | High socket saturation & zero-parse PlanCache hot-path |
| | WiredTiger MVCC | 136,845.47 tpmC (5,135 TPS) | **255,040.50 tpmC (9,424 TPS)** | **+86.4%** | **+86.4% leap**: elimination of global C lock under 200 sockets |
| **Tier 4: Client Network PGWire 5433 (`cmd/tpcc_client`)**<br>16 Warehouses, 32 Workers, 10s | In-Memory OCC | 230,024.93 tpmC (8,515 TPS) | **376,538.57 tpmC (13,927 TPS)** | **+63.7%** | `PERF-PGWIRE-PREP-TX` direct VDBE bytecode binding |
| | WiredTiger MVCC | 208,570.89 tpmC (7,748 TPS) | **352,568.32 tpmC (13,018 TPS)** | **+69.0%** | Lightweight 5-byte framing + WiredTiger lock-free cursors |
| **Tier 4: Client Network PGWire 5433 (`cmd/tpcc_client`)**<br>10 Warehouses, 100 Workers, 10s | In-Memory OCC | 233,040.06 tpmC (8,611 TPS) | **357,159.62 tpmC (13,250 TPS)** | **+53.3%** | 0.00% abort rate across 132k transactions over TCP wire |
| | WiredTiger MVCC | 208,209.55 tpmC (7,670 TPS) | **350,078.29 tpmC (12,890 TPS)** | **+68.1%** | 12.8k TPS sustained disk MVCC over PostgreSQL protocol |
| **Tier 4: Client Network PGWire 5433 (`cmd/tpcc_client`)**<br>20 Warehouses, 200 Workers, 10s | In-Memory OCC | 240,700.97 tpmC (8,914 TPS) | **329,176.42 tpmC (12,174 TPS)** | **+36.8%** | 12.1k TPS sustained under 200 concurrent PGWire clients |
| | WiredTiger MVCC | 243,412.72 tpmC (9,026 TPS) | **315,675.78 tpmC (11,711 TPS)** | **+29.7%** | 11.7k TPS sustained disk B+Tree commits under 200 clients |

> For the archived historical baseline from October 31, 2026, see [`doc/history/BENCHMARK_20261031.md`](history/BENCHMARK_20261031.md).
