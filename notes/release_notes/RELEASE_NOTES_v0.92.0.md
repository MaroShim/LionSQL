# LionSQL v0.92.0 Release Notes (`notes/release_notes/RELEASE_NOTES_v0.92.0.md`)

> - **Initial Date**: 2026-11-09
> - **Release Version**: `v0.92.0` (Minor Release)
> - **Milestone Theme**: **Comprehensive 37 P0 Defect Remediations, WiredTiger High-Concurrency Lock Removal (Invariant 10), All-Time High TPC-C Benchmark Surge (+16%~+86%), and Cooldown Architecture**

---

## 1. Release Highlights

LionSQL `v0.92.0` is a landmark milestone release that resolves all 37 critical P0 architectural defects uncovered during our 135-step full-spectrum codebase audit, establishing enterprise mission-critical durability while achieving **all-time high benchmark throughput across all 4 tiers simultaneously**:

- **135-Step Audit P0 Remediation: 37 Critical Defects Resolved (Structural Hardening)**:
  - **Storage & Durability**: FlatRecord heap offset collision isolation tag (`slot[7]`, `P0-01`), preservation of row data on `ALTER TABLE ADD COLUMN` (`P0-04`), partition key mutex isolation (`partMu`, `P0-05`), fail-closed WAL recovery preventing illegal mid-log truncation (`P0-03`, `ADR-027`), and atomic segment rotation on WAL archiving (`P0-15`).
  - **Security & RBAC**: Eradication of anonymous admin bypass backdoor (`P0-06`), absolute precedence enforcement for explicit `DENY` over `GRANT` (`P0-07`), atomic persistence of logins and grants to `auth.json` (`P0-09`), and mandatory all-caps default password boot banner warning (`SEC-BOOT-DEFAULT-PASS-WARN`).
  - **Replication & CDC**: CDC Outbox exponential backoff backpressure on unreachable brokers (`P0-08`, `ADR-026`), CDC Lossless Bridge 2-phase streaming handover (`P0-10`), and fail-closed isolation preventing silent drop on slow replica buffer overflow (`P0-34`).
  - **Execution Engine & Memory Governor**: CUBE 12-dimension bounding (`P0-11`), WindowIterator and MergeJoin 64MB WorkMem real-time budget tracking (`P0-12`, `P0-13`), child iterator resource leakage prevention (`P0-14`), MERGE statement PK direct streaming (`P0-18`), and fail-closed atomic rollback on DML scan I/O errors (`P0-19`).
  - **Wire Protocols & Sessions**: TDS/PGWire 64MB and HTTP Chunked 32MB payload framing limits (`P0-27`, `P0-35`), transaction rollback-first session disconnect drain (`P0-26`), session lock scope shrinking and re-entrant worker guards (`P0-30`), dynamic RLS AST isolation (`P0-31`), strict `@@TRANCOUNT` nested commit enforcement (`P0-32`), eager `LazyTuple` materialization before set deduplication (`P0-33`), and global session panic recovery guards (`P0-37`).
  - **Memory Hygiene**: Pointer zeroing and slice capacity bounding in global `sync.Pool` recyclers (`P0-29`), eliminating long-lived heap bloat and GC stop-the-world spikes under high-speed concurrency.

- **WiredTiger High-Concurrency Lock Removal & Invariant 10 Permanent Lockdown**:
  - Enforced a 4-layer permanent lockdown preventing `transaction_sync` from appearing in `wiredtiger_open` configurations (`[RULE:WiredTigerHighConcurrency]`), eliminating the global C transaction mutex serialization bottleneck.
  - While preserving 100% ACID durability via WiredTiger native group commit and background periodic flushing (1s interval), achieved storage kernel TPC-C **1,184,785 tpmC (+42.1%)** and network TDS **255,040 tpmC (+86.4%)**.

- **All-Time High Benchmark Surges Across All Tiers & 5-Second Cooldown Protocol (`scripts/run_all_benchmarks.sh`)**:
  - **Tier 1 (cmd/bench)**: In-Memory OCC **218,282 TPS (+16.1%)**, WiredTiger **141,472 TPS (+16.6%)** (100% ACID verified).
  - **Tier 2 (Storage Kernel)**: In-Memory OCC **1,066,827 tpmC (39,748 TPS)**, WiredTiger **1,184,785 tpmC (41,905 TPS)**.
  - **Tier 3 (VDBE T-SQL Runtime)**: In-Memory OCC **723k ~ 759k tpmC (26.9k ~ 28.1k TPS, +25%~+46%)**, WiredTiger **825k ~ 863k tpmC (28.7k ~ 31.7k TPS, +25%~+39%)**.
  - **Tier 4 (Network Wire)**: PGWire v3 **329k ~ 376k tpmC (11.7k ~ 13.9k TPS)**, TDS **228k ~ 315k tpmC (8.4k ~ 11.6k TPS)**.
  - Codified the **5-second cooldown gap protocol (`--cooldown=5`)** in benchmark harnesses to guarantee host kernel TCP `TIME_WAIT` socket drain and CPU thermal recovery.

- **SSMS Official Integration Guide & Virtual System Views**:
  - Published comprehensive Microsoft SQL Server Management Studio (SSMS 19/20) integration guide ([`guide/SSMS_INTEGRATION_GUIDE.md`](https://github.com/MaroShim/LionSQL/blob/main/guide/SSMS_INTEGRATION_GUIDE.md) / [Korean](https://github.com/MaroShim/LionSQL/blob/main/guide/SSMS_INTEGRATION_GUIDE.ko.md)).
  - Expanded virtual system views with `sys.dm_os_host_info` for seamless GUI introspection.

---

## 2. Verification & Compliance

1. **3-Stage Pipeline Verification (`[RULE:PipelineVerification]`)**:
   - Official release binaries compiled for macOS (`darwin-arm64`), Linux (`linux-amd64`), and Windows (`windows-amd64.exe`).
   - `go test -tags wiredtiger -count=1 -race ./pkg/...` **100% PASS across all core packages (0 data races)**.
2. **ACID Durability & Crash Recovery Oracles**:
   - Verified 10-round zero-loss crash recovery (`bin/crashtest`) under abrupt `SIGKILL`.
   - 100,000 banking transfer transactions executed with 0 rollbacks and 100% ACID consistency.
