# LionSQL v0.90.1 Release Notes (`notes/RELEASE_NOTES_v0.90.1.md`)

> - **Initial Date**: 2026-11-04
> - **Release Version**: `v0.90.1` (Patch Release)
> - **Milestone Theme**: **T-SQL Stored Procedure & Recursive CTE Engine Hardening, SQLLogicTest Official Corpus Integration, and Web Studio Inline Grid Innovation**

---

## 1. Release Highlights

LionSQL `v0.90.1` is a major patch release following `v0.90.0` GA that resolves critical architectural and mathematical challenges in relational query execution. Through extensive black-box and stress testing against enterprise procedural suites (AWS Babelfish 2,900-Suite) and SQLite international standard SQLLogicTest corpora, the relational engine has reached an unprecedented level of maturity.

- **Standard SQL Recursive CTE (`WITH RECURSIVE`) Delta Semantics**:
  - Eliminates exponential row explosion by feeding only the newly produced delta rows (`workTempName`) into the next iteration, achieving mathematical convergence in milliseconds (reduced from infinite hangs to 4ms).
- **T-SQL Stored Procedure Calling Convention & Lifecycle**:
  - Added support for `EXEC @ret = procName` return status code variable binding.
  - Tolerant parameter syntax parsing: `@param AS INT = 100`, `@p VARCHAR(20) NULL`, and single-query parenthesized procedure bodies.
  - Implemented `DROP FUNCTION` / `DROP FUNCTION IF EXISTS` with dynamic catalog updates and executor UDF cache invalidation.
- **AWS Babelfish 2,900-Suite Verification**:
  - Executed all 2,900 files and 116,884 T-SQL batches without crashes or panics in 12s ~ 2m, achieving **67,981 passing batches (58.16% pass rate)**.
  - Comprehensive engineering report published in [`notes/BABELFISH_INTEGRATION_REPORT.ko.md`](BABELFISH_INTEGRATION_REPORT.ko.md).
- **SQLLogicTest (SLT) 107-Suite Full Corpus & 14 Procedural Suites**:
  - Fully integrated SQLite official large-scale suites (`full`, `evidence`, `random`, `index`).
  - Added 14 hermetic procedural suites (`test/slt_proc/`) covering stored procedures, CTE deltas, and UDF lifecycles with **100% PASS (0 data races)**.
- **Web Studio UX Innovation (Excel-Style Inline Grid)**:
  - Replaced disruptive modal popups with a spreadsheet-style inline row creation interface (`startInlineNewRow`) featuring seamless keyboard navigation (`Enter` to save, `Esc` to cancel, `Tab` to navigate).

---

## 2. Detailed Changes

### 2.1. Query & Procedural Engine
- **Recursive CTE Delta Working Table (`pkg/engine/sqlexec_explain.go`)**:
  - Implemented standard SQL recursion semantics: only newly generated rows from step `N` are visible to step `N+1` in `workTempName`, completely resolving hangs in `BABEL-3392` and `BABEL-RECURSIVE-CTE`.
- **Cooperative Timeout Context Propagation (`pkg/engine/sqlexec_select.go`)**:
  - Connected parent context deadlines directly to the VDBE VM execution loop via `ExecuteInTxContext(ctx, ...)` to guarantee cooperative aborts on runaway loops (`[RULE:CooperativeAbort]`).
- **Flexible T-SQL Parameter Declarations (`pkg/proc/parser_ddl.go`)**:
  - Accepted `TokenEq` (`=`) in addition to `TokenAssign` for parameter defaults, and relaxed parsing for `AS <type>` and `NULL` constraints.
- **Procedure Return Variable Binding (`pkg/proc/parser_stmt.go`)**:
  - Full parser and VDBE VM bytecode compiler support for `EXEC @ret = procName`.
- **Dynamic UDF Lifecycle Management (`pkg/engine/sqlexec_ddl.go`, `pkg/proc/runtime/vm.go`)**:
  - Added `DROP FUNCTION` statement handler, runtime `executor.UnregisterUDF`, and plan cache eviction.

### 2.2. Test Automation & Verification
- **High-Throughput Babelfish Runner (`cmd/babelfish_runner`)**:
  - 16-worker concurrent execution with individual 3-second channel timeout safety guards and real-time progress reporting.
  - Negative test annotation detection (`-- should fail`, `-- expected error`) matching SQLLogicTest semantics.
- **Hermetic Procedural SQLLogicTest Suites (`test/slt_proc/`)**:
  - `10_babelfish_proc_params.slt`: Parameter defaults and syntax tolerance.
  - `11_babel_iterative_exec_return.slt`: Iterative execution in loops and return code binding.
  - `12_babel_xp_qv.slt`: System procedure tolerance and query verification.
  - `13_recursive_cte_delta.slt`: Integer/string recursion and hierarchical tree search delta semantics.
  - `14_drop_function_and_params.slt`: `DROP FUNCTION` and dynamic UDF unregistration.

### 2.3. Embedded Web Studio
- **Inline Spreadsheet Row Addition (`pkg/ui/dashboard.html`)**:
  - Eliminates context loss caused by modal overlays. Allows users to inspect existing table records while entering new data in an inline row.
  - Keyboard shortcuts (`Enter`, `Esc`, `Tab`) and auto-focus / smooth-scroll support.

---

## 3. Verification & Compliance

1. **3-Stage Pipeline Verification (`[RULE:PipelineVerification]`)**:
   - Official release binaries built for macOS (`darwin-arm64`), Linux (`linux-amd64`), and Windows (`windows-amd64`).
   - `go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/...` **100% PASS (0 data races)**.
2. **Babelfish Metrics**:
   - Total Batches: 116,884
   - Passed Batches: **67,981** (58.16% pass rate)
   - Zero crashes, zero panics, zero memory leaks.
