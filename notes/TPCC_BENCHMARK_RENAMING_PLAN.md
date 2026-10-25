# Migration Plan: Renaming TPC-C Benchmarks (`tpcc-kernel` & `tpcc-runtime`)

> **상태 (Status)**: 완료됨 (Completed) `[x]`  
> **적용 일자**: 2026-10-24  
> **구현 산출물**: `cmd/tpcc_kernel` (`bin/tpcc-kernel`), `cmd/tpcc_runtime` (`bin/tpcc-runtime`)

## 1. Background & Rationale
LionSQL provides two distinct in-process TPC-C benchmark drivers that measure different layers of the database architecture without network wire overhead:
1. **`cmd/tpcc`**: Directly invokes storage engine SPI (`storage.Engine`, `storage.Tx`) CRUD APIs (`tx.Get`, `tx.Insert`, `tx.Update`), testing pure in-memory OCC and WiredTiger B+Tree concurrency and storage limits without SQL parsing or bytecode execution.
2. **`cmd/tpcc_vdbe`**: Executes 5 standard TPC-C transactions as compiled Microsoft T-SQL stored procedures on the SQLite-style VDBE virtual machine (`exec.RunInTx(prog, params, tx, nil)`), testing procedure compilation, plan caching, register recycling (`sync.Pool`), and ACID transaction atomicity.

Neither benchmark tests external client wire protocols (PostgreSQL PGWire or MS SQL TDS). Renaming them to **`tpcc-kernel`** (pure storage engine layer) and **`tpcc-runtime`** (VDBE virtual machine runtime layer) accurately describes their architectural scope and eliminates user confusion.

---

## 2. Proposed Naming Standards

| Current Name | Proposed Directory | Proposed Binary Output | Target Component / Layer |
| :--- | :--- | :--- | :--- |
| `cmd/tpcc` | `cmd/tpcc_kernel` | `bin/tpcc-kernel` | Storage Engine Kernel SPI (`memengine` & `wiredtiger`) |
| `cmd/tpcc_vdbe` | `cmd/tpcc_runtime` | `bin/tpcc-runtime` | T-SQL Stored Procedure VDBE VM Runtime & Plan Cache |

---

## 3. Scope of Changes

### 3.1. Directory Restructuring (`git mv`)
- `git mv cmd/tpcc cmd/tpcc_kernel`
- `git mv cmd/tpcc_vdbe cmd/tpcc_runtime`
- Preserve `cmd/tpcc_runtime/atomicity_test.go` and update any package references/comments.

### 3.2. 3-Stage Verification Pipeline Updates (`GEMINI.md`)
- Update Stage 1 Build command:
  ```bash
  go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
  go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
  go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
  go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
  ```
- Update Rule 7 (ACID Transaction Modeling Integrity) and other references to `cmd/tpcc_runtime` and `bin/tpcc-runtime`.

### 3.3. Documentation & Developer Guides Synchronization
- [**`doc/TESTING.md`**](file:///Users/maro/Projects/SQLion/doc/TESTING.md):
  - Update Stage 1 build commands.
  - Update Section 5 (Test Map by Package) and Section 9 (TPC-C Performance Regression: `cmd/tpcc_runtime`, `bin/tpcc-runtime`).
  - Update `atomicity_test.go` package references.
- [**`doc/BENCHMARK.md`**](file:///Users/maro/Projects/SQLion/doc/BENCHMARK.md):
  - Section 3.4: Rename to `Storage Engine Kernel TPC-C Benchmark (cmd/tpcc_kernel / bin/tpcc-kernel)`.
  - Section 3.5: Rename to `T-SQL VDBE Runtime TPC-C Benchmark (cmd/tpcc_runtime / bin/tpcc-runtime)`.
- [**`doc/REFERENCE_MANUAL.ko.md`**](file:///Users/maro/Projects/SQLion/doc/REFERENCE_MANUAL.ko.md):
  - Section 1.3 & 1.5: Update `bin/tpcc-runtime` / `bin/tpcc-kernel` build and execution guidelines.
- [**`doc/USER_MANUAL.ko.md`**](file:///Users/maro/Projects/SQLion/doc/USER_MANUAL.ko.md):
  - Update Stage 1 compilation command examples.
- [**`README.md`**](file:///Users/maro/Projects/SQLion/README.md) & [**`README.ko.md`**](file:///Users/maro/Projects/SQLion/README.ko.md):
  - Update build instructions and benchmark run commands.
- [**`CODE_REVIEW.ko.md`**](file:///Users/maro/Projects/SQLion/CODE_REVIEW.ko.md), [**`doc/ARCHITECTURE.md`**](file:///Users/maro/Projects/SQLion/doc/ARCHITECTURE.md), [**`doc/ADR.md`**](file:///Users/maro/Projects/SQLion/doc/ADR.md), [**`notes/ENGINEERING_CONTEXT.ko.md`**](file:///Users/maro/Projects/SQLion/notes/ENGINEERING_CONTEXT.ko.md), [**`notes/PITFALLS_AND_TRICKS.ko.md`**](file:///Users/maro/Projects/SQLion/notes/PITFALLS_AND_TRICKS.ko.md):
  - Update historical pointers to the new directories and binary targets.
- [**`CHANGELOG.md`**](file:///Users/maro/Projects/SQLion/CHANGELOG.md) & [**`TODO.ko.md`**](file:///Users/maro/Projects/SQLion/TODO.ko.md):
  - Record the rename under `### Changed` in Unreleased.

---

## 4. Verification & Validation Steps
1. **Directory Move Check**: Verify `cmd/tpcc_kernel/main.go`, `cmd/tpcc_runtime/main.go`, and `cmd/tpcc_runtime/atomicity_test.go` exist.
2. **Build Verification**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc-kernel ./cmd/tpcc_kernel
   ```
3. **Atomicity & Regression Test**:
   ```bash
   go test -tags wiredtiger -count=1 -race -v ./cmd/tpcc_runtime/...
   ```
4. **Smoke Execution**:
   - Run short 1-second benchmark with `bin/tpcc-kernel -engine=memory -duration=1s`
   - Run short 1-second benchmark with `bin/tpcc-runtime -engine=memory -duration=1s`
5. **Atomic Commit**:
   - `refactor(bench): rename tpcc and tpcc_vdbe to tpcc_kernel and tpcc_runtime`
