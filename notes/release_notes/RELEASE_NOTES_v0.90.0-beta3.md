# LionSQL v0.90.0-beta3 Release Notes (`notes/RELEASE_NOTES_v0.90.0-beta3.md`)

> - **Release Date**: 2026-10-25
> - **Version**: `v0.90.0-beta3`
> - **Theme**: **Hyper-Scale In-Memory OCC Exceeding 1M tpmC, Windows Native Daemon with 1ms Timer, and Web Studio Concurrency Isolation**

---

## 1. Release Highlights

LionSQL `v0.90.0-beta3` is a historic performance milestone where the **In-Memory OCC storage engine shattered the 1,000,000 tpmC barrier (1.04M~1.44M tpmC / 39k~53k TPS)**, while delivering **native Windows daemon cross-compilation with 1ms multimedia timer resolution**, **browser tab-isolated Web Studio concurrency**, and a dedicated **live network TPC-C benchmark client**.

- **Hyper-Scale In-Memory OCC Breakthrough (`VPERF-19` ~ `VPERF-22`)**:
  - Restored 256-shard Go Native hash map architecture, eliminating linked-list pointer-chasing bottlenecks on multi-million row tables (`order_line`).
  - Measured **1,046,121 tpmC (39,020 TPS)** sustained on the storage kernel and **603,241 tpmC (22,294 TPS)** on the Native T-SQL VDBE Runtime with a minuscule 0.08% abort rate.
  - Reached **1,442,347 tpmC (53,794 TPS)** peak kernel throughput (+73% over WiredTiger) and **210,934 TPS** on ACID bank transfers.
- **Native Windows Standalone Executables with 1ms Timer (`CLI-WINDOWS-DAEMON-1`)**:
  - Decoupled daemon process lifecycle into pure-Go Windows targets (`bin/lionsql.exe`, `bin/lionsql-arm64.exe`) requiring zero CGo dependencies.
  - Enabled 1ms high-resolution multimedia timer resolution (`timeBeginPeriod(1)` via `winmm.dll`), eliminating the 15.6ms Windows OS sleep quantum penalty.
- **Web Studio Browser Tab Session Isolation & Cancellation (`UI-CONCURRENCY-1`)**:
  - Bound client sessions to browser tab UUIDs (`sessionStorage`), isolating interactive transactions (`BEGIN TRAN`) and variables across simultaneous tabs.
  - Added live query cancellation signaling (`POST /api/query/cancel`) and 1-second memory stats caching to eliminate Stop-The-World latency.
- **Live Network TPC-C Benchmark Client (`BENCH-TPCC-CLIENT-1`, `BENCH-TPCC-CLIENT-2`)**:
  - Standalone benchmarking binary (`bin/tpcc-client`) executing concurrent multi-client TPC-C transactions over live TDS (:1433) and PGWire (:5433) sockets.
- **Extended 13-Domain Smoke Test Automation (`SMOKE-EXT-1`)**:
  - Automated 13 core architectural domains verified in ~3 seconds (`scripts/verify_report.sh`).

---

## 2. Detailed Technical Improvements

### 2.1. In-Memory OCC Hyper-Scale Engine Architecture
- **256-Shard Partition Parallelism (`table_data.go`)**: Expanded table partition array from 64 to 256 shards with 32-byte cacheline padding, cutting multi-worker lock contention under 100 concurrent workers by >70%.
- **Zero-Allocation Row Mutations**: Introduced direct `Row` mutation pathways for unindexed tables, bypassing `map[string]Value` instantiation on hot tables (`order_line`, `stock`).
- **Selective Column Extraction (`pkg/storage/tuple.go`)**: Extracted individual columns directly from compact binary tuples in 25.55 ns/op with 0 B/op and 0 allocs/op (9x faster than full decoding).
- **Small Integer Key Interning**: Built pre-computed string cache for integers `0..100000`, making `Value.String()` zero-allocation and sub-nanosecond for primary key resolution.
- **Bitmask Shard Locking Ordering (`tx.go`)**: Applied 256-bit bitmask (`[4]uint64`) lock tracking to enforce canonical ascending shard lock acquisition with zero heap allocations.

### 2.2. Windows Platform Parity & Native Daemon
- Decoupled process detachment using `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS` for background daemon management.
- Implemented Windows process inspection (`syscall.OpenProcess`, `syscall.GetExitCodeProcess`) and safe termination.
- Enabled native compilation for both AMD64 and ARM64 Windows architectures.

### 2.3. Web Studio Multi-Tab Concurrency
- Isolated interactive transactions per tab, preventing accidental commits or rollbacks across multiple developer windows.
- Implemented double-checked locked `memStatsCache` on `/api/overview`, eliminating `runtime.ReadMemStats` pauses during dashboard polling.

### 2.4. Safety & Virtual Catalog Hardening
- **Benchmark Base Directory Safety (`BENCH-DIR-SAFETY-1`)**: Eliminated destructive `os.RemoveAll` calls, scoping benchmark directory cleanup strictly to isolated temporary subdirectories (`os.MkdirTemp`).
- **Client GUI Tool Audit (`TOOL-AUDIT-1`)**: Normalized virtual system metadata for dual naming conventions (snake_case and camelCase), enabling full compatibility with DbGate, DBeaver, and SSMS.

---

## 3. Verification & Benchmark Scorecard

| Area | Suite / Workload | Verified Metric | Notes |
| :--- | :--- | :--- | :--- |
| **Kernel OLTP** | In-Memory OCC Kernel | **1,046,121 tpmC (39,020 TPS)** | 256-shard hash map restoration (VPERF-22) |
| **VDBE OLTP** | TPC-C VDBE Runtime | **603,241 tpmC (22,294 TPS)** | 16 Wh / 32 Workers, 0.08% abort rate |
| **ACID Transfer** | TransferFunds Stress | **210,934 TPS** | 100% ACID verified across 50,000 tx |
| **Windows Daemon**| `lionsql.exe` | 1ms timer resolution | Zero CGo static compilation |
| **Smoke Suite** | 13 Architectural Domains | 100% PASS in ~3s | Self-contained automated script |

---

<details>
<summary><b>Detailed Changelog Delta</b></summary>

### Added
- Web Studio browser tab session isolation and query cancellation (UI-CONCURRENCY-1)
- Windows native daemon process control and 1ms timer resolution (CLI-WINDOWS-DAEMON-1)
- Self-contained extended integration smoke test suite (SMOKE-EXT-1)
- Relocated 107-defect resolution archive to project root (DOC-ARCHIVE-1)
- Live network TPC-C benchmark client binary (BENCH-TPCC-CLIENT-1, BENCH-TPCC-CLIENT-2)
- 3-tier realistic enterprise scale-factor progression in BENCHMARK.md (BENCH-VDBE-TPCC-1)

### Performance
- Restored 256-shard Go native O(1) hash map architecture breaking 1.04M tpmC (VPERF-22)
- Compact binary tuple selective column extraction in 25.55 ns (VPERF-21)
- 16,384-bucket lock-free hash architecture and zero-lock snapshot reads (VPERF-20)
- 256-shard lock partition expansion and VDBE direct fast-paths (VPERF-19)
- Atomic immutable table pointer snapshot eliminating RWMutex contention (VPERF-18)
- Out-of-lock WAL pre-encoding and dual shard map unification (VPERF-17)
- Go runtime GC tuning with GOGC=200 and soft memory limits (VPERF-16)
- Bitmask shard tracking and non-blocking background AOF compaction (VPERF-15)

### Fixed
- Eliminated destructive benchmark directory cleanup protecting user data (BENCH-DIR-SAFETY-1)
- Normalized virtual system metadata for dual naming conventions in DbGate/SSMS (TOOL-AUDIT-1)
</details>
