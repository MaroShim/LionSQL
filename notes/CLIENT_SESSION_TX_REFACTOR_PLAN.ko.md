# ClientSession Legacy Transaction State Deprecation Plan

## 1. Context & Objectives
- **Target**: Eliminate legacy transaction state fields (`cs.Tx`, `cs.TranCount`, `cs.TxFailed`) from `ClientSession` (`pkg/engine/session.go`).
- **Core Principle**: Unify transaction ownership under `cs.TxMgr` (`pkg/tx/manager.go`) as the **Single Source of Truth (SSOT)** adhering to `[RULE:RadicalKISS]`, `[RULE:ModularAbstraction]`, and `[RULE:LockHierarchy]`.
- **Key Invariants**:
  - Guarantee zero split-brain transaction states between session and engine.
  - Maintain 100% ANSI 3VL / PGWire 'I'/'T'/'E' status and T-SQL `@@TRANCOUNT` nesting parity.
  - Ensure zero deadlocks between `ClientSession.Mu` and `TxMgr.Lock()`.
  - Pass 100% test suite with 0 race conditions (`go test -v -race ./...`).

---

## 2. Technical Gap & Solution Design

### 2.1 Current Defect Architecture
1. `cs.Tx`, `cs.TranCount`, `cs.TxFailed` coexist with `cs.TxMgr`.
2. Some execution paths update `cs.Tx` directly without notifying `cs.TxMgr`, while others call `cs.TxMgr` and manually mirror changes into `cs.Tx`.
3. If an error occurs in one subsystem, `cs.TxFailed` may become desynchronized from `cs.TxMgr.State() == StateInError`.
4. `TranCount` nesting logic in T-SQL lives loosely in `sqlexec.go`, outside the transaction context.

### 2.2 Desired State Architecture
1. **`tx.Manager` & `tx.Context` as SSOT**:
   - `tx.Manager` encapsulates:
     - Active storage transaction (`StorageTx`)
     - State machine (`StateIdle`, `StateActive`, `StateInError`, `StateCommitted`, `StateAborted`)
     - T-SQL transaction nesting counter (`TranCount` / `NestLevel`)
     - Savepoints & Deferred physical actions
2. **`ClientSession` Encapsulation & Delegation**:
   - Remove raw fields `Tx`, `TranCount`, `TxFailed` from `ClientSession` struct.
   - Session methods (`cs.Tx()`, `cs.TranCount()`, `cs.IsTxFailed()`, `cs.InTransaction()`, `cs.TxStatus()`) delegate directly to `cs.TxMgr`.
   - Engine calls requesting the active storage transaction call `cs.Tx()` (or `cs.ActiveTx()`).

---

## 3. Step-by-Step Implementation Strategy

### Stage 1: Enhance `pkg/tx` with T-SQL Nesting & Direct Accessors
- Add `NestLevel int` (or `TranCount`) and `IncNestLevel()`, `DecNestLevel()`, `NestLevel()` to `tx.Context` and `tx.Manager`.
- Add `ActiveStorageTx() storage.Transaction` to `tx.Manager` to retrieve current storage transaction safely.
- Ensure `tx.Manager` provides all querying capabilities needed by `ClientSession` without leaking raw context internals.

### Stage 2: Convert `ClientSession` to Facade / Accessors
- In `pkg/engine/session.go`:
  - Convert `cs.Tx` from a field to a getter method `cs.Tx() storage.Transaction`.
  - Update `cs.TranCount` to `cs.TranCount() int`.
  - Update `cs.TxFailed` to `cs.IsTxFailed() bool`.
  - Refactor `cs.InTransaction()`, `cs.TxStatus()`, `cs.GetActiveTx()`, `cs.ExecuteProgram()`, and `cs.checkAndDrainClosedLocked()`.

### Stage 3: Refactor All Call Sites across `pkg/engine/`
- Refactor all references in:
  - `pkg/engine/sqlexec.go` (`BeginSessionTx`, `CommitSessionTx`, `RollbackSessionTx`, savepoints)
  - `pkg/engine/sqlexec_dml.go`, `sqlexec_select.go`, `sqlexec_ddl.go`, `sqlexec_merge.go`, `sqlexec_procs.go`, `sqlexec_explain.go`, `pk_bypass.go`, `cte_lowering.go`
  - `pkg/engine/coordinator_impl.go` (`CloseSession`, `ResetSessionState`)
  - `pkg/engine/*_test.go` regression test suites

### Stage 4: Verification & Final Cleanup
- Build: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
- Test: `go test -v -race ./pkg/tx/... ./pkg/engine/...`
- Full Suite: `go test -v -race ./...`
- Update `CHANGELOG.md` and commit with Conventional Commits.
