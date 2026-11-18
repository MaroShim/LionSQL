# Transactional DDL Undo & Commit Actions SSOT Refactoring Plan (`TRANSACTIONAL_DDL_UNDO_SSOT_PLAN.ko.md`)

## 1. 개요 및 배경 (Context & Goals)
- **대상**: `ClientSession`에 임시 저장되던 트랜잭셔널 DDL 언두 로그(`cs.ddlUndoLog []DDLUndoOp`) 및 수동 언두/커밋 실행 함수(`executeDDLUndoOps`, `finalizeDDLCommitOps`)
- **목표**: 트랜잭션 내에서 실행된 DDL의 언두(Rollback) 및 확정(Commit) 액션을 `tx.Context`와 `tx.Manager`로 완전 이전하여 **단일 진실 원천(SSOT)** 확립
- **핵심 원칙**:
  - `[RULE:RadicalKISS]`: 수동 `ddlOps` 슬라이스 복사/전달 및 상태 이중 관리 코드 완전 제거
  - `[RULE:ModularAbstraction]`: 트랜잭션 롤백/커밋 라이프사이클에 DDL 복구 로직을 선언적 클로저 액션(`AddRollbackAction`, `AddDeferredAction`)으로 통합
  - `[RULE:LockHierarchy]`: 락 외부에서 트랜잭션 롤백 시 안전하게 LIFO 언두 액션 실행
  - `[RULE:PipelineVerification]`: 3단계 검증 파이프라인 (Build ➔ Test ➔ Commit)

---

## 2. 현행 구조 분석 및 문제점 (Current Defect Architecture)

### 2.1 분산된 DDL 언두 상태 (`cs.ddlUndoLog`)
현재 `ClientSession` 구조체에 `ddlUndoLog []DDLUndoOp` 슬라이스가 보관됩니다.
```go
type ClientSession struct {
    ...
    ddlUndoLog []DDLUndoOp // LIFO stack of DDL operations executed within current transaction
}
```

### 2.2 수동 언두/커밋 호출의 취약점
- `CommitSessionTx`, `RollbackSessionTx`, `ResetSession`, `CloseSession`, 그리고 `sqlexec.go`의 panic recover 블록 등 **최소 6곳**에서 수동으로 `ddlOps := cs.ddlUndoLog; cs.ddlUndoLog = nil`을 꺼내어 `s.executeDDLUndoOps(cs, ddlOps)` 또는 `s.finalizeDDLCommitOps(ddlOps)`를 직접 호출하고 있습니다.
- 만약 세션 타임아웃, 유휴 세션 수거(`ReapIdleClientSessions`), 드라이버 레벨 강제 취소 등으로 `tx.Manager.Rollback()`이 직접 호출될 경우, `cs.ddlUndoLog`의 물리적 테이블 언두가 누락되는 치명적인 데이터 잔류 위험이 있습니다.

---

## 3. 목표 아키텍처 설계 (Target SSOT Architecture)

```
[ ClientSession / Coordinator ]
        │
        ▼ (Execute DDL inside Tx)
[ tx.Context ]
   ├── DeferredActions []func() error  (On Commit: drop tombstones, finalize DDL)
   └── RollbackActions []func() error  (On Rollback: LIFO drop created tables, restore tombstones)
        │
        ├── Commit() ──► txCtx.ExecuteDeferredActions()  [자동 실행, 0개 수동 호출]
        └── Rollback() ─► txCtx.ExecuteRollbackActions()  [자동 LIFO 실행, 0개 수동 호출]
```

### 3.1 `pkg/tx` 확장
1. `tx.Context`에 롤백 액션 스택 추가:
   - `RollbackActions []func() error`
   - `AddRollbackAction(fn func() error)`: 롤백 액션 등록
   - `ExecuteRollbackActions() error`: 등록된 롤백 액션을 **LIFO(역순)**으로 실행
   - `ClearRollbackActions()`: 액션 폐기
2. `tx.Manager.Rollback()` 시 자동 실행:
   - 물리 스토리지 트랜잭션 `txCtx.StorageTx.Rollback()` 완료 후, `txCtx.ExecuteRollbackActions()` 자동 호출.
   - `txCtx.ClearDeferredActions()`로 커밋 액션 폐기.
3. `tx.Manager.Commit()` 시 자동 실행:
   - 물리 스토리지 트랜잭션 `txCtx.StorageTx.Commit()` 완료 후, `txCtx.ExecuteDeferredActions()` 자동 호출 (톰스톤 드롭 등).
   - `txCtx.ClearRollbackActions()`로 롤백 액션 폐기.

### 3.2 `pkg/engine/sqlexec_ddl.go` & `sqlexec_procs.go` 리팩토링
- DDL 구문 성공 시, `cs.ddlUndoLog = append(...)` 대신 `txCtx := cs.TxMgr.Current()`를 얻어:
  - 생성된 테이블/인덱스/컬럼: `txCtx.AddRollbackAction(func() error { ... DropTable/DropIndex ... })`
  - 톰스톤으로 보존된 드롭 테이블:
    - 롤백 시: `txCtx.AddRollbackAction(func() error { ... RenameTable(tombstone, original) ... })`
    - 커밋 시: `txCtx.AddDeferredAction(func() error { ... DropTable(tombstone) ... })`
- `ClientSession.ddlUndoLog` 필드 및 `DDLUndoOp` 타입 완전 제거 (`[RULE:RadicalKISS]`).
- `Coordinator.executeDDLUndoOps`, `Coordinator.finalizeDDLCommitOps` 함수 완전 제거.

---

## 4. 단계별 실행 계획 (Step-by-Step Execution Plan)

### Step 1: `pkg/tx` 롤백 액션 인프라 구현 및 유닛 테스트
- `pkg/tx/context.go`: `RollbackActions`, `AddRollbackAction`, `ExecuteRollbackActions`, `ClearRollbackActions` 구현
- `pkg/tx/manager.go`: `Rollback()` 시 `ExecuteRollbackActions()` LIFO 실행 및 `Commit()` 시 `ClearRollbackActions()` 연동
- `pkg/tx/manager_test.go`: 롤백 시 역순 실행 및 커밋 시 폐기 검증 테스트 작성

### Step 2: DDL 실행기(`sqlexec_ddl.go`, `sqlexec_procs.go`) 액션 클로저 전환
- `CREATE TABLE`, `DROP TABLE`, `ALTER TABLE ADD/DROP/RENAME COLUMN`, `RENAME TABLE`, `CREATE/DROP INDEX`, `CREATE/DROP PROCEDURE/TRIGGER`의 언두 로직을 `txCtx.AddRollbackAction` 및 `txCtx.AddDeferredAction`으로 전환

### Step 3: `ClientSession` 및 `Coordinator` 잔여 수동 언두 로직 제거
- `ClientSession.ddlUndoLog` 필드 제거
- `Coordinator.executeDDLUndoOps`, `finalizeDDLCommitOps` 함수 및 호출부(Commit, Rollback, Reset, Close) 완전 삭제

### Step 4: 3단계 검증 파이프라인 수행
- Build: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
- Test: `go test -v -race ./pkg/tx/... ./pkg/engine/...`
- Commit: Conventional Commits 형식으로 커밋
