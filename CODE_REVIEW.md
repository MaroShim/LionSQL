# LionSQL Code Review & Defect Audit Tracker (`CODE_REVIEW.md`)

This document tracks systematic static analysis, defect auditing, regression testing, and 3-stage pipeline verifications (**Build ➔ Race-Test ➔ Commit**) across the LionSQL codebase.

> [!NOTE]
> - Detailed Korean audit tracker: [`CODE_REVIEW.ko.md`](CODE_REVIEW.ko.md)
> - Defect resolution archive (107 cases): [`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md)
> - Subsystem modular API contract plan: [`notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md`](notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md)

---

## 1. 3-Stage Verification Pipeline

All code reviews and bug remediations must strictly pass all 3 stages in order before committing:
1. **Build**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
   ```
2. **Race-Free Verification**:
   ```bash
   go test -tags wiredtiger -count=1 -race ./...
   ```
   (100% PASS across all 22 packages with 0 data races)
3. **Commit**:
   - Conventional Commits standard (`fix(...)`, `feat(...)`, `refactor(...)`, `docs(...)`)
   - Zero decorative emojis

---

## 2. Status Legend

- `[ ]` : Identified & logged (pending fix)
- `[~]` : In progress & under test development
- `[x]` : Verified through 3-stage pipeline & committed

---

## 3. Engine Layer (`pkg/engine`)

| ID | Priority | Category | Target Location | Description & Remediation | Status | Reference |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-ENG-001** | P1 | Bug | [`pkg/engine/sqlexec_dml.go:18`](pkg/engine/sqlexec_dml.go#L18) | `reDmlInsertValues` mandatory `\s+INTO\s+` rejected valid `INSERT tbl VALUES (...)` syntax. Remedied by making `INTO` optional. | `[x]` | `db57751` |
| **REV-ENG-002** | P3 | Message | [`pkg/engine/sqlexec_dml.go:1248`](pkg/engine/sqlexec_dml.go#L1248) | Copy-paste error in DELETE execution reporting `identify row for update`. Fixed to `identify row for delete`. | `[x]` | `db57751` |
| **REV-ENG-003** | P2 | Vulnerability | [`pkg/engine/sqlexec_dml.go:248`](pkg/engine/sqlexec_dml.go#L248) | Fragile multi-whitespace/tab handling in UPSERT prefix routing. Replaced with normalized regex routing. | `[x]` | `db57751` |
| **REV-ENG-004** | P1 | Bug/Panic | [`pkg/engine/sqlexec_eval.go:930`](pkg/engine/sqlexec_eval.go#L930) | Integer division by zero panic in `evalUpdateExpr`. Added defensive zero divisor guards. | `[x]` | Golden Rule 2 |
| **REV-ENG-005** | P2 | Correctness | [`pkg/engine/sqlexec_eval.go:137`](pkg/engine/sqlexec_eval.go#L137) | Escaped single-quote (`''`) handling in `splitLogicalConditions` causing split errors. Added quote escape scanner. | `[x]` | SQL Grammar |
| **REV-ENG-006** | P2 | Bug | [`pkg/engine/sqlexec_dml.go:21`](pkg/engine/sqlexec_dml.go#L21) | Regex lazy matching in `UPDATE ... SET col = 'a WHERE b' OUTPUT ...` splitting inside literal. Solved by `ARCH-DML-1`. | `[x]` | `ParseDMLCommand` |
| **REV-ENG-007** | P1 | Over-spec | [`pkg/engine/sqlexec_procs.go:1263`](pkg/engine/sqlexec_procs.go#L1263) | Parameter name switch in `sp_executesql` dropping queries on non-standard parameter names. Simplified to 100% ordinal slot mapping. | `[x]` | `57e51aa` |
| **REV-ENG-008** | P2 | Architecture | [`pkg/engine/sqlexec_select.go`](pkg/engine/sqlexec_select.go) | Triple-execution path debt from legacy ad-hoc SELECT interpreter. Replaced with 2-Tier CBO/VDBE query engine. | `[x]` | `ARCH-2TIER-1` |
| **REV-ENG-009** | P2 | Anti-pattern | [`pkg/storage/coerce.go:1`](pkg/storage/coerce.go) | Heuristic type guessing on string literals causing silent precision loss. Replaced with schema-driven explicit coercion. | `[x]` | `TYPE-GUESSING-1` |
| **REV-ENG-010** | P1 | Performance/GC| [`pkg/execution/governor.go:20`](pkg/execution/governor.go) | Default `GOGC=100` triggering excessive GC cycles under high OLTP. Tuned to default `GOGC=200` with soft memory limit. | `[x]` | `VPERF-16` |

---

## 4. Storage Layer (`pkg/storage`, `pkg/storage/memengine`, `pkg/storage/wiredtiger`)

| ID | Priority | Category | Target Location | Description & Remediation | Status | Reference |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-STOR-001** | P2 | Performance | [`pkg/storage/memengine/tx.go:1395`](pkg/storage/memengine/tx.go#L1395) | `fmt.Sscanf` reflection overhead and silent error swallowing in PK parsing. Replaced with zero-allocation `strconv` conversions. | `[x]` | Golden Rule 7 |
| **REV-STOR-002** | P2 | Safety | [`pkg/storage/memengine/memengine.go:360`](pkg/storage/memengine/memengine.go#L360) | Silent swallowing of AOF compaction errors during startup. Propagated errors explicitly to halt corrupted initialization. | `[x]` | Golden Rule 7 |
| **REV-STOR-003** | P1 | Data Race | [`pkg/storage/memengine/tx.go:440`](pkg/storage/memengine/tx.go#L440) | Race condition where `defer tx.Rollback()` ran on reused transaction objects in `txPool`. Fixed by pool return strictly in `Rollback()`. | `[x]` | `VPERF-18` |
| **REV-STOR-004** | P1 | Lock Cacheline | [`pkg/storage/memengine/memengine.go:50`](pkg/storage/memengine/memengine.go#L50) | Global `e.mu.RLock()` contention during table catalog lookups. Replaced with `atomic.Pointer[map[string]*TableData]` snapshot. | `[x]` | `VPERF-18` |
| **REV-STOR-005** | P2 | Commit Stall | [`pkg/storage/memengine/tx.go:280`](pkg/storage/memengine/tx.go#L280) | In-lock MessagePack serialization causing multi-core commit stalls. Implemented Out-of-Lock WAL Pre-encoding. | `[x]` | `VPERF-17` |
| **REV-STOR-006** | P2 | Cache/CPU | [`pkg/storage/memengine/table_data.go:25`](pkg/storage/memengine/table_data.go#L25) | Dual shard maps (`rows` and `versions`) doubling map lookups. Consolidated into single `entries map[string]shardEntry`. | `[x]` | `VPERF-17` |
| **REV-STOR-007** | P2 | Lock Deadlock | [`pkg/storage/memengine/tx.go:120`](pkg/storage/memengine/tx.go#L120) | Nested map allocations for canonical shard lock ordering. Replaced with `uint64` bitmask iteration with zero heap allocations. | `[x]` | `VPERF-15` |
| **REV-STOR-008** | P1 | STW Freeze | [`pkg/storage/memengine/memengine.go:410`](pkg/storage/memengine/memengine.go#L410) | Synchronous AOF compaction inside commit path causing 100~500ms freezes. Offloaded to background worker goroutine. | `[x]` | `VPERF-15` |
| **REV-STOR-009** | P1 | Data Safety | [`pkg/storage/benchdir.go:1`](pkg/storage/benchdir.go) | Destructive `os.RemoveAll(*dataDir)` deleting user parent directories. Implemented isolated subdirectory sandbox `PrepareBenchmarkDir`. | `[x]` | `BENCH-DIR-SAFETY-1` |
| **REV-STOR-010** | P1 | Concurrency | [`pkg/storage/memengine/table_data.go:55`](pkg/storage/memengine/table_data.go#L55) | Static 64-shard bottleneck causing hash collisions under high worker counts. Expanded to 256 shards with `[4]uint64` bitmask ordering, direct `Row` mutation fast-paths, integer key interning, and zero-lock reads, achieving 1,442,347 tpmC / 53,794 TPS. | `[x]` | `VPERF-19` |
| **REV-STOR-011** | P1 | Lock-Free | [`pkg/storage/memengine/table_data.go:62`](pkg/storage/memengine/table_data.go#L62) | Readers acquiring `shard.mu.RLock()` causing read latency stalls during write commits. Implemented 16,384 lock-free hash buckets and `atomic.Pointer[RowVersion]` nodes. | `[x]` | `VPERF-20` |

---

## 5. Procedure & VDBE VM Layer (`pkg/proc`, `pkg/proc/runtime`)

| ID | Priority | Category | Target Location | Description & Remediation | Status | Reference |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-PROC-001** | P1 | Integrity | [`pkg/proc/runtime/vm.go:910`](pkg/proc/runtime/vm.go#L910) | Legacy VM path swallowing trigger execution errors (`_ = f.fireTriggers`). Propagated trigger errors and enforced rollback. | `[x]` | Golden Rule 7 |
| **REV-PROC-002** | P2 | Performance/GC| [`pkg/proc/runtime/vm.go:170`](pkg/proc/runtime/vm.go#L170) | Allocating VM stack frames and register slices per query. Recycled via `sync.Pool` (`VMFrame`) with pre-resolved slots. | `[x]` | `VPERF-1`~`VPERF-5` |
| **REV-PROC-003** | P2 | Zero-Copy | [`cmd/tpcc_kernel/main.go:140`](cmd/tpcc_kernel/main.go#L140) | Allocating 21-column Go `Record` maps on simple existence checks. Replaced with zero-copy `tx.GetRow` verification. | `[x]` | `VPERF-18` |
| **REV-PROC-004** | P2 | Decoupling | [`pkg/proc/parser.go:30`](pkg/proc/parser.go#L30) | Tight coupling and circular import risks between Pratt parser and ANTLR4. Decoupled via `ProcedureFallbackParser` SPI. | `[x]` | `PROC-FALLBACK-1` |
| **REV-PROC-005** | P1 | Performance/GC| [`pkg/proc/runtime/vm_dispatcher.go:642, 1056`](pkg/proc/runtime/vm_dispatcher.go#L642) | VM executing 4-5 map allocations on `OpUpdate` and traversing Volcano CBO iterator tree on single-row `SELECT INTO`. Resolved via `GetRow`/`UpdateRow` direct paths and Point-Lookup Shortcut. | `[x]` | `VPERF-19` |

---

## 6. Wire Protocols & API Layer (`pkg/wire`, `pkg/wire/tds`, `pkg/api`)

| ID | Priority | Category | Target Location | Description & Remediation | Status | Reference |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-WIRE-001** | P1 | Defect | [`pkg/wire/tds/server.go:1344`](pkg/wire/tds/server.go#L1344) | RPC parameter mapping dropping parameter declarations on non-standard `@params` names. Fixed with 100% ordinal Slot 1 binding. | `[x]` | [MS-TDS] 2.2.6.6 |
| **REV-WIRE-002** | P1 | Defect | [`pkg/wire/tds/server.go:947`](pkg/wire/tds/server.go#L947) | Slice length fallback confusion in `sp_prepare` / `sp_prepexec`. Standardized on strict Slot Index positional separation. | `[x]` | [MS-TDS] 2.2.6.6 |
| **REV-WIRE-003** | P2 | Over-spec | [`pkg/wire/tds/server.go:2530`](pkg/wire/tds/server.go#L2530) | Artificial version query interception bypassing VDBE scalar engine. Restricted strictly to DbGate single signature. | `[x]` | `TDS-INTERCEPT-1` |
| **REV-WIRE-004** | P1 | Defect | [`pkg/wire/tds/server.go:2536`](pkg/wire/tds/server.go#L2536) | Queries with 2+ `SERVERPROPERTY` calls hijacked into synthetic 4-column result sets. Removed hijacking in favor of VDBE engine. | `[x]` | `57e51aa` |
| **REV-WIRE-005** | P2 | Defect | [`pkg/wire/tds/server.go:937`](pkg/wire/tds/server.go#L937) | Unsupported numeric `ProcID` returning silent `DoneFinal` without errors. Returned explicit `TokenError` (Msg 2812). | `[x]` | [MS-TDS] Protocol |
| **REV-WIRE-006** | P1 | Parameter Bug | [`pkg/wire/tds/server.go:1030`](pkg/wire/tds/server.go#L1030) | Parameter case mismatches (`@p1` vs `@P1`) dropping bound values. Standardized on ordinal positional mapping. | `[x]` | `TDS-RPC-AUDIT-1` |
| **REV-WIRE-007** | P2 | Bandwidth | [`pkg/wire/tds/tokens.go:120`](pkg/wire/tds/tokens.go#L120) | Standard ROW tokens wasting bandwidth on NULL fields. Implemented TDS 7.2+ NBCROW (`0xD2`) bitmap compression. | `[x]` | `TDS-NBCROW-1` |
| **REV-WIRE-008** | P2 | Resource Leak | [`pkg/wire/server.go:1210`](pkg/wire/server.go#L1210) | Unbounded prepared statements per session risking memory leaks. Enforced bounded LRU cache (`maxPreparedPerSession = 256`). | `[x]` | `PREP-1` |
| **REV-WIRE-009** | P1 | Atomicity | [`pkg/wire/copy.go:350`](pkg/wire/copy.go#L350) | `COPY FROM STDIN` operating outside ambient session transaction. Enforced transaction binding and rollback safety. | `[x]` | `COPY-SAFETY-1` |
| **REV-API-001** | P3 | Bad Request | [`pkg/api/handlers_procs.go:132`](pkg/api/handlers_procs.go#L132) | Silently ignoring JSON decode errors in procedure execution HTTP endpoint. Returned explicit HTTP 400 Bad Request. | `[x]` | Golden Rule 7 |
| **REV-API-002** | P1 | Security | [`cmd/lionsql/server.go:320`](cmd/lionsql/server.go#L320) | Permitting startup on public interfaces (`0.0.0.0`) with default password (`1234567890`). Added Production Security Guard. | `[x]` | `PROD-SEC-1` |

---

## 7. Catalog & System Views Layer (`pkg/catalog`)

| ID | Priority | Category | Target Location | Description & Remediation | Status | Reference |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-CAT-001** | P2 | Anti-pattern | [`pkg/catalog/virtual/mssql.go`](pkg/catalog/virtual/mssql.go) | Hardcoding external tool names (`isDBeaver`) in virtual views. Refactored into objective schema-based predicates. | `[x]` | `57e51aa` |
| **REV-CAT-002** | P2 | Atomic Write | [`pkg/catalog/catalog.go:420`](pkg/catalog/catalog.go#L420) | Non-atomic disk writes for CBO statistics and procedure metadata. Enforced temporary swap files and atomic renames. | `[x]` | Golden Rule 1 |

---

## 8. CLI, Daemon & OS Integration (`cmd/lionsql`)

| ID | Priority | Category | Target Location | Description & Remediation | Status | Reference |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-CLI-001** | P2 | Cross-Platform | [`cmd/lionsql/daemon_unix.go`](cmd/lionsql/daemon_unix.go) | POSIX-only process controls (`syscall.Kill`, `Setsid`) failing on Windows. Decoupled via `//go:build` tags. | `[x]` | `CLI-WINDOWS-DAEMON-1` |
| **REV-CLI-002** | P2 | Timer Resolution | [`cmd/lionsql/daemon_windows.go:20`](cmd/lionsql/daemon_windows.go#L20) | Windows 15.625ms scheduler sleep quantums degrading OLTP query latency. Enabled 1ms high-res timer via `winmm.dll`. | `[x]` | `CLI-WINDOWS-DAEMON-1` |
| **REV-CLI-003** | P1 | Graceful Stop | [`cmd/lionsql/server.go:785`](cmd/lionsql/server.go#L785) | SIGSEGV risks on rapid shutdown from pending WiredTiger handles. Enforced 1.5s bounded drain and idempotent cleanup. | `[x]` | `SHUTDOWN-1` |

---

## 9. Architecture & Contracts Layer (`notes/`, `pkg/engine`)

| ID | Priority | Category | Target Location | Description & Remediation | Status | Reference |
| :--- | :---: | :---: | :--- | :--- | :---: | :--- |
| **REV-ARCH-001** | P2 | Encapsulation | [`notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md`](notes/MODULAR_ARCHITECTURE_API_CONTRACT_PLAN.ko.md) | Wire protocol adapters directly accessing storage engine and session mutex locks. Designed `R-6` SPI decoupled contracts. | `[x]` | Post-1.0 (`v1.1.0+`) |

---

## 10. Audit Summary & Quality Metrics

- **Total Audited Items**: **31 cases**
- **Resolved & Verified (`[x]`)**: **31 cases (100%)**
- **Unresolved / Open Deficiencies (`[ ]`)**: **0 cases (0%)**
- **Concurrency & Race Verification**: `go test -tags wiredtiger -race ./...` (100% PASS across 22 packages, 0 data races)
- **Multi-Platform Compilation**: macOS, Linux, and Windows standalone executables pass with 0 warnings
