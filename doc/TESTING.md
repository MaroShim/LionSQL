# LionSQL Local Testing and Regression Guide

This guide explains how to build, test, and regression-check LionSQL on a local machine. It consolidates the commands that were previously scattered across `CONTRIBUTING.md`, `README.md`, `GEMINI.md`, and `notes/ENGINEERING_CONTEXT.ko.md`.

- [1. Prerequisites](#1-prerequisites)
- [2. The 3-Stage Verification Pipeline](#2-the-3-stage-verification-pipeline)
- [3. Running Tests](#3-running-tests)
- [4. Build Tags and Conditional Tests](#4-build-tags-and-conditional-tests)
- [5. Test Map by Package](#5-test-map-by-package)
- [6. Regression Test Files by Work Item](#6-regression-test-files-by-work-item)
- [7. Writing New Regression Tests](#7-writing-new-regression-tests)
- [8. Go Micro-Benchmarks](#8-go-micro-benchmarks)
- [9. TPC-C Performance Regression](#9-tpc-c-performance-regression)
- [10. A/B Comparison Against a Previous Commit](#10-ab-comparison-against-a-previous-commit)
- [11. Troubleshooting](#11-troubleshooting)

---

## 1. Prerequisites

| Requirement | Detail |
| :--- | :--- |
| Go toolchain | Go 1.24 or newer (the race detector is required). The reference host used Go 1.27.x on `darwin/arm64`. |
| C toolchain | Required for the WiredTiger backend (`cgo`). On macOS install the Xcode command line tools. |
| WiredTiger | Linked through `cgo` when building with `-tags wiredtiger`. |
| `uv` (optional) | Only needed by the external Python driver tests. They are skipped automatically when `uv` is not on `PATH`. |

Check the toolchain:

```bash
go version
go env CGO_ENABLED   # must be 1 for the wiredtiger tag
```

The module cache must be readable and writable. In a restricted sandbox, `go test` can fail with errors such as `verifying ...: operation not permitted`. See [Troubleshooting](#11-troubleshooting).

---

## 2. The 3-Stage Verification Pipeline

Every code change must pass these stages in order before it is committed.

```
[Code Modifications]
        |
        v
[Stage 1: Build]  -> all 4 binaries compile
        |
        v
[Stage 2: Test]   -> 100% PASS across all packages, 0 data races
        |
        v
[Stage 3: Commit] -> Conventional Commits, no decorative emojis
```

### Stage 1: Build

```bash
go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
```

### Stage 2: Test with the race detector

```bash
go test -tags wiredtiger -count=1 -race ./...
```

- `-count=1` disables Go's test result cache, so every test really runs.
- `-race` enables the data race detector. Any race is a failure.
- Add `-v` for per-test output. Without it, only package-level `ok` and `FAIL` lines are printed.
- A full run takes roughly 2 minutes on the reference host.

Expected output is `ok` for every package. Packages without tests print `[no test files]`, which is normal (`cmd/bench`, `cmd/server`, `cmd/tpcc_kernel`, `scratch`).

### Stage 3: Commit

Use Conventional Commits with no decorative emojis:

```
feat(scope): add new capability
fix(scope): resolve bug
perf(scope): optimize hot path
refactor(scope): restructure internals
test(scope): add test coverage
docs(scope): update documentation
```

Documentation-only changes (`*.md` only, no source changes) may skip stages 1 and 2 and be committed directly.

---

## 3. Running Tests

### 3.1 One package

```bash
go test -tags wiredtiger -count=1 -race ./pkg/storage/memengine
```

### 3.2 One package tree

```bash
go test -tags wiredtiger -count=1 -race ./pkg/storage/...
go test -tags wiredtiger -count=1 -race ./pkg/wire/...
```

### 3.3 A single test or a group of tests (`-run`)

`-run` takes a regular expression matched against test function names.

```bash
# One exact test
go test -tags wiredtiger -count=1 -v ./pkg/storage/memengine -run '^TestBugTempTablePersistLeak_MemEngine$'

# All tests whose name contains a prefix
go test -tags wiredtiger -count=1 -v ./pkg/proc/runtime -run 'TestVPerf'

# Subtests use a slash
go test -tags wiredtiger -count=1 -v ./pkg/wire/tds -run 'TestVPerf_WritePacketWithSize/Large8192'
```

To list test names without running them:

```bash
go test -tags wiredtiger -list '.*' ./pkg/engine
```

### 3.4 Tests that touch several packages

When you change shared code such as `pkg/storage`, `pkg/sqlutil`, or `pkg/proc`, run the full suite. These packages are imported by `engine`, `api`, `wire`, and `catalog`, so a regression often shows up in a downstream package rather than the one you edited.

### 3.5 Running repeatedly to catch flakiness

```bash
go test -tags wiredtiger -count=20 -race ./pkg/storage/memengine
```

Tests must be deterministic. A test that fails intermittently is a defect in the test or in the code, not noise to ignore.

### 3.6 Coverage

```bash
go test -tags wiredtiger -count=1 -coverprofile=/tmp/cover.out ./pkg/engine
go tool cover -func=/tmp/cover.out | tail -n 20
go tool cover -html=/tmp/cover.out
```

### 3.7 Static checks

```bash
go vet -tags wiredtiger ./...
gofmt -l .
```

`gofmt -l .` prints files that need formatting. An empty output means all files are formatted.

---

## 4. Build Tags and Conditional Tests

The `wiredtiger` build tag matters for testing.

| Situation | Behavior |
| :--- | :--- |
| `-tags wiredtiger` and `cgo` enabled | WiredTiger tests are compiled and run. |
| No tag, or `cgo` disabled | Files marked `//go:build cgo && wiredtiger` are excluded. Their tests do not run, and the run still reports `ok`. |

Always pass `-tags wiredtiger` for verification. Without it, a green run does not cover the WiredTiger storage engine.

Files gated by the tag:

- `pkg/storage/wiredtiger/wt_engine_test.go`
- `pkg/storage/wiredtiger/groupc_wt_test.go`
- `pkg/engine/json_wt_test.go`
- `pkg/wire/driver_integration_wt_test.go`
- `pkg/wire/tds/driver_integration_wt_test.go`

Tests that skip themselves at runtime:

| Test file | Skip condition |
| :--- | :--- |
| `pkg/wire/driver_external_test.go`, `pkg/wire/tds/driver_external_test.go` | `uv` executable not found on `PATH`. |
| `pkg/engine/dml_characterization_test.go` | A case is marked with a known bug (`knownBug`). It is skipped and reports the bug id. |

Check skipped tests with `-v`:

```bash
go test -tags wiredtiger -count=1 -v ./pkg/wire/... 2>&1 | grep -E "^(=== RUN|--- SKIP|--- FAIL)"
```

---

## 5. Test Map by Package

| Package | Focus |
| :--- | :--- |
| `pkg/storage` | Value types, tuple and record codec, type promotion (`coerce`), decimal, JSON, arena, synthetic PK, casing. |
| `pkg/storage/memengine` | In-Memory OCC engine: transactions, indexes, skiplist, WAL, group commit, ephemeral mode, FK cache, temp table persistence. |
| `pkg/storage/wiredtiger` | WiredTiger MVCC engine: CRUD, indexes (including composite and descending), WAL hook, group commit, sessions. |
| `pkg/engine` | Coordinator and SQL execution: DML, DDL, PK bypass, upsert, MERGE, procedures, casing, review-loop fixes. |
| `pkg/proc` | Native Pratt lexer and parser, compiler, built-in functions, constant folding. |
| `pkg/proc/runtime` | VDBE bytecode VM: plan cache, triggers, UDFs, regression suite. |
| `pkg/proc/parser/antlr` | ANTLR4 fallback parser. |
| `pkg/planner` | Cost-based optimizer: pushdown, pruning, decorrelation, statistics, bounds. |
| `pkg/execution` | Volcano iterators: joins, set operations, sort spill, governor, t-digest. |
| `pkg/catalog`, `pkg/catalog/virtual` | System catalog and virtual `sys.*` views. |
| `pkg/wire` | PostgreSQL wire protocol: server, COPY, TLS, driver integration. |
| `pkg/wire/tds` | TDS protocol: packet and token codec, login and auth, TLS, RPC, driver integration. |
| `pkg/api` | HTTP and REST API: records, RBAC, RLS, webhooks, cron, mailer, backup, replication. |
| `pkg/auth` | Authentication and role-based access control. |
| `pkg/backup` | Backup, restore, point-in-time recovery. |
| `pkg/replication` | WAL replication and failover. |
| `pkg/cdc` | Change data capture: outbox and streamer. |
| `pkg/sqlutil` | Shared SQL tokenizer and scanning helpers, identifier casing. |
| `pkg/config`, `pkg/migrate`, `pkg/ui` | Configuration, schema migrations, embedded web studio. |
| `cmd/lionsql` | CLI, doctor command, server start-up. |
| `cmd/tpcc_runtime` | TPC-C atomicity test for the VDBE runtime harness. |

---

## 6. Regression Test Files by Work Item

Fixes are tied to dedicated regression tests. When touching an area, run the matching tests first.

| Area | Test files |
| :--- | :--- |
| Dual-parser parity (native vs ANTLR) | `pkg/proc/runtime/regression_test.go`, `pkg/proc/parser/antlr/antlr_test.go` |
| Group A: MemEngine storage, concurrency, WAL | `pkg/storage/memengine/groupa_test.go`, `group2_regression_test.go`, `wal_test.go` |
| Group B: wire protocols and security | `pkg/wire/groupb_wire_test.go`, `pkg/wire/group3_copy_test.go`, `pkg/wire/copy_test.go` |
| Group C: WiredTiger | `pkg/storage/wiredtiger/groupc_wt_test.go` |
| Group D: DDL, index ordering, procedure transactions | `pkg/engine/groupd_test.go` |
| Group 3 and 4: VDBE, DML | `pkg/proc/runtime/group3_vdbe_test.go`, `pkg/engine/group3_regression_test.go`, `group4_dml_test.go`, `group4_regression_test.go` |
| Engine review loop (`REV-ENG-*`) | `pkg/engine/rev_eng_test.go` |
| Identifier casing | `pkg/storage/memengine/group_casing_test.go`, `pkg/engine/group_casing_engine_test.go`, `pkg/sqlutil/casing_test.go`, `pkg/storage/record_casing_test.go` |
| Vertical performance track (`VPERF-*`) | `pkg/proc/runtime/vperf_test.go`, `pkg/engine/vperf_engine_test.go`, `pkg/storage/memengine/vperf_fk_test.go`, `pkg/wire/tds/vperf_tds_test.go` |
| Temporary table persistence leak | `pkg/storage/memengine/temp_table_leak_test.go`, `pkg/storage/wiredtiger/groupc_wt_test.go` |
| DML characterization | `pkg/engine/dml_characterization_test.go`, `pkg/engine/dml_parser_test.go` |
| Planner and execution regressions | `pkg/planner/group6_decorrelate_test.go`, `pkg/execution/group6_regression_test.go`, `pkg/execution/group2_regression_test.go` |
| Auth, catalog, backup | `pkg/auth/group5_rbac_test.go`, `pkg/catalog/group5_catalog_test.go`, `pkg/backup/group5_backup_test.go` |

Quick regression for a bug-fix area:

```bash
# Wire and security fixes
go test -tags wiredtiger -count=1 -race -v ./pkg/wire/... -run 'GroupB|Copy|Auth'

# Storage persistence
go test -tags wiredtiger -count=1 -race -v ./pkg/storage/... -run 'TempTable|WAL|Persist'

# VPERF changes
go test -tags wiredtiger -count=1 -race -v ./pkg/proc/runtime ./pkg/engine ./pkg/storage/memengine ./pkg/wire/tds -run 'TestVPerf'
```

---

## 7. Writing New Regression Tests

Every bug fix and behavior change needs a test that fails before the fix and passes after it.

1. **Name the test after the work item**, for example `TestBugTempTablePersistLeak_MemEngine`, so `-run` can target it.
2. **Put it next to the code** it covers. Use an external `_test` package when the public API is enough, or the internal package when you must reach unexported helpers.
3. **Gate WiredTiger-only tests** with `//go:build cgo && wiredtiger` on the first line.
4. **Keep it deterministic.** Do not use `time.Sleep` to wait for another goroutine. Use channels, `sync.WaitGroup`, or explicit polling with a bounded timeout.
5. **Keep it hermetic.** Use `t.TempDir()` for data directories. For wire protocol tests, use in-memory `net.Pipe()` rather than binding a physical TCP port, so parallel runs do not collide.
6. **Clean up.** Bind every engine, session, transaction, file, and connection to `defer Close()` or `t.Cleanup`.
7. **Cover restart behavior** for persistence work: write data, close the engine, reopen it from the same directory, and assert on what survived. See `temp_table_leak_test.go` for an example.
8. **Cover both storage engines** when the behavior is engine-visible. Add one test against `memengine` and one against WiredTiger.
9. **Assert on errors with `errors.Is`** against the sentinel errors in `pkg/storage` (for example `storage.ErrTableNotFound`) instead of comparing strings.
10. **Run the new test under `-race` and with `-count=20`** before relying on it.

When extending the native fast-path parser, add a parity case in `pkg/proc/runtime/regression_test.go` so the native Pratt parser and the ANTLR fallback are checked against each other.

---

## 8. Go Micro-Benchmarks

Benchmarks live in `*_test.go` files under:

- `pkg/proc/runtime/benchmark_test.go`
- `pkg/wire/tds/packet_test.go`, `server_test.go`
- `pkg/catalog/virtual/interceptor_test.go`
- `pkg/storage/tuple_test.go`, `arena_test.go`, `codec_test.go`
- `pkg/storage/memengine/memengine_test.go`, `wal_groupcommit_test.go`, `skiplist_test.go`
- `pkg/storage/wiredtiger/wt_engine_test.go`
- `pkg/execution/execution_test.go`
- `pkg/engine/query_utils_test.go`, `pk_bypass_test.go`

Run one benchmark with allocation statistics and no regular tests:

```bash
go test -tags wiredtiger -run '^$' -bench 'BenchmarkName' -benchmem -count=5 ./pkg/storage/memengine
```

- `-run '^$'` skips regular tests.
- `-benchmem` reports bytes and allocations per operation. Hot paths are expected to stay at or near 0 allocs/op.
- `-count=5` gives several samples so noise is visible.

Compare two runs with `benchstat` (install with `go install golang.org/x/perf/cmd/benchstat@latest`):

```bash
go test -tags wiredtiger -run '^$' -bench . -benchmem -count=10 ./pkg/wire/tds > /tmp/before.txt
# apply the change
go test -tags wiredtiger -run '^$' -bench . -benchmem -count=10 ./pkg/wire/tds > /tmp/after.txt
benchstat /tmp/before.txt /tmp/after.txt
```

Do not run benchmarks together with `-race`. The race detector changes timing and allocation behavior and makes the numbers meaningless.

---

## 9. TPC-C Performance Regression

`cmd/tpcc_runtime` runs a TPC-C workload through precompiled VDBE bytecode. Build it first (Stage 1), then run:

```bash
./bin/tpcc-runtime -engine memory     -duration 30s -dir /tmp/tpcc_mem
./bin/tpcc-runtime -engine wiredtiger -duration 30s -dir /tmp/tpcc_wt
```

### Flags

| Flag | Default | Meaning |
| :--- | :--- | :--- |
| `-engine` | `memory` | Storage backend: `memory` or `wiredtiger`. |
| `-duration` | `15s` | Benchmark length. |
| `-warehouses` | `2` | TPC-C scale factor. |
| `-workers` | `20` | Concurrent client workers. |
| `-customers` | `300` | Customers per district (full spec is 3000). |
| `-items` | `1000` | Unique items (full spec is 100000). |
| `-fsync` | `everysec` | WAL fsync policy: `off`, `none`, `everysec`, `always`. |
| `-max-retries` | `3` | Retries with backoff on serialization conflict. |
| `-dir` | `data/tpcc_runtime_wt` | Data directory. Use a fresh `/tmp` path for each run. |

Delete the data directory between runs so each run starts from a clean seed:

```bash
rm -rf /tmp/tpcc_mem /tmp/tpcc_wt
```

### What to read in the output

| Metric | Meaning |
| :--- | :--- |
| `tpmC` | New-Order transactions per minute. This is the headline number. |
| `Overall TPS` | All five transaction types per second. |
| `Serialization Abort` | Conflict aborts and the abort rate. A rising rate points to a concurrency regression. |
| `Conflict Retries` | Total retries performed. |
| P50 / P95 / P99 | Latency percentiles per transaction type. Watch the tail (P99). |

### Comparing against baselines

Recorded baselines are in `doc/BENCHMARK.md`, `doc/internal/TRANSACTION_CONCURRENCY.md`, and `CHANGELOG.md`. Only compare runs that use the same harness and parameters (for example VDBE, 2 warehouses, 20 workers). Native Go API results and different warehouse counts are not comparable with VDBE 2-warehouse results.

Reference VDBE results (2 warehouses, 20 workers):

| Engine | tpmC range recorded historically | Note |
| :--- | :--- | :--- |
| In-Memory OCC | about 430k to 480k | Peak intervals can exceed 550k. |
| WiredTiger | about 300k to 420k | Depends on fsync policy and group commit. |

### Reducing noise

A single 10 second run is not evidence of a regression. Performance numbers vary with machine state.

- Run each configuration at least 3 times, 30 seconds or longer.
- Use the median, and report the spread.
- Close other heavy processes. Do not run right after the full `-race` suite, because the machine may still be hot.
- Keep the power and thermal state the same between runs (plugged in, no throttling).
- Use the same `-dir` filesystem, and a fresh directory each time.
- Compare the abort rate as well. A tpmC drop with a higher abort rate suggests a concurrency change, while a drop with an unchanged abort rate suggests a per-transaction cost change.

### Atomicity test

`cmd/tpcc_runtime/atomicity_test.go` verifies that each business transaction runs inside a single ACID transaction boundary. It runs as part of the normal suite:

```bash
go test -tags wiredtiger -count=1 -race -v ./cmd/tpcc_runtime
```

---

## 10. A/B Comparison Against a Previous Commit

Use this to find out whether a change caused a performance regression. It builds the previous commit in a separate worktree so your working tree is untouched.

```bash
# 1. Build the baseline in a separate worktree
git worktree add /tmp/lionsql_base <BASELINE_COMMIT>
(cd /tmp/lionsql_base && go build -tags wiredtiger -buildvcs=false -o /tmp/tpcc_base ./cmd/tpcc_runtime)

# 2. Build the current tree
go build -tags wiredtiger -buildvcs=false -o /tmp/tpcc_head ./cmd/tpcc_runtime

# 3. Alternate runs so drift affects both equally
for i in 1 2 3; do
  rm -rf /tmp/tpcc_a /tmp/tpcc_b
  /tmp/tpcc_base -engine memory -duration 30s -dir /tmp/tpcc_a | grep -E "Throughput|Overall TPS|Abort"
  /tmp/tpcc_head -engine memory -duration 30s -dir /tmp/tpcc_b | grep -E "Throughput|Overall TPS|Abort"
done

# 4. Clean up
git worktree remove /tmp/lionsql_base
rm -rf /tmp/tpcc_a /tmp/tpcc_b /tmp/tpcc_base /tmp/tpcc_head
```

Alternating base and head within each iteration cancels out slow drift such as thermal throttling. Repeat the same procedure with `-engine wiredtiger`.

If a regression is confirmed, bisect by reverting changes one at a time, or use `git bisect run` with a script that builds the benchmark and exits non-zero when the median tpmC falls below a threshold.

---

## 11. Troubleshooting

| Symptom | Cause and fix |
| :--- | :--- |
| `verifying ...: operation not permitted` from `go test` | The Go module cache is not accessible, typically in a restricted sandbox. Run from a normal terminal, or allow access to `GOMODCACHE` (`go env GOMODCACHE`). |
| A run is green but WiredTiger tests never executed | `-tags wiredtiger` was omitted, or `CGO_ENABLED=0`. Re-run with the tag and `go env CGO_ENABLED` equal to 1. |
| `WARNING: DATA RACE` | Real defect. Fix it. Do not retry until it passes. |
| Test passes alone but fails in the full suite | Shared global state or leftover files. Use `t.TempDir()`, reset package-level state, and run with `-count=1`. |
| Stale results | Go cached a previous pass. Use `-count=1`. |
| Tests slow or hang | Run the package alone with `-v -timeout 120s` to find the blocked test. A goroutine dump is printed on timeout. |
| Port already in use | A test bound a physical port. Use `net.Pipe()` or an ephemeral listener (`127.0.0.1:0`). |
| External driver tests report `SKIP` | `uv` is not installed. This is expected and not a failure. |
| `git commit` fails with `index.lock` | Another git process holds the lock, or the directory is not writable from the current environment. Verify no git process is running, then retry from a normal terminal. |
| Benchmark numbers differ wildly between runs | See [Reducing noise](#reducing-noise). Do not draw conclusions from one run. |

---

## Related Documents

- [CONTRIBUTING.md](../CONTRIBUTING.md): contribution rules and the pull request checklist.
- [BENCHMARK.md](BENCHMARK.md): recorded benchmark results and methodology.
- [internal/TRANSACTION_CONCURRENCY.md](internal/TRANSACTION_CONCURRENCY.md): concurrency model and benchmark history.
- [TEST_VERIFICATION_REPORT.ko.md](TEST_VERIFICATION_REPORT.ko.md): past verification results.
- [internal/ZERO_ALLOCATION_ARCHITECTURE.md](internal/ZERO_ALLOCATION_ARCHITECTURE.md): hot-path allocation rules.
