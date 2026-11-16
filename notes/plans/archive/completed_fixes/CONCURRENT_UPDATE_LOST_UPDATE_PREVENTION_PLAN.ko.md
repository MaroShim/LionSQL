# FIX-CONCURRENT-UPDATE-LOST-UPDATE-1: 조건절 UPDATE 동시성 실행 시 최신 커밋 버전 검증 및 갱신 손실(Lost Update) 방지 계획서
(`notes/plans/CONCURRENT_UPDATE_LOST_UPDATE_PREVENTION_PLAN.ko.md`)

> - **문서 버전**: v1.0.0
> - **작성일자**: 2026-11-15
> - **과제 식별 코드**: `FIX-CONCURRENT-UPDATE-LOST-UPDATE-1` (결함 7)
> - **대상 마일스톤**: `0.93.0` (1.0-GA ACID 완결 게이트)
> - **연계 규칙**: `[RULE:AtomicSaves]`, `[RULE:ConflictPrevention]`, `[RULE:ExplicitError]`, `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`
> - **관련 파일**: `pkg/proc/runtime/vm.go`, `pkg/engine/sqlexec_dml.go`, `pkg/storage/memengine/tx.go`, `pkg/storage/wiredtiger/wt_engine.go`

---

## 1. 개요 및 배경 (Problem Statement)

고동시성 OLTP 환경에서 동일한 대상 행들에 대해 다중 트랜잭션이 조건절 `UPDATE` 구문(예: `UPDATE accounts SET balance = balance + 10 WHERE id = 1` 또는 `UPDATE tasks SET status = 'IN_PROGRESS' WHERE status = 'PENDING'`)을 동시에 실행할 때, 타 트랜잭션의 커밋된 갱신 내역이 덮어써져 영구 유실되는 **갱신 손실(Lost Update)** 및 **술어 위반 팬텀 갱신** 위험을 방어한다.

### 1.1 핵심 결함 분석

1. **VDBE 실행기(`pkg/proc/runtime/vm.go:execUpdateWhere`)의 UPDATE 오류 침묵 (`_ = err`)**:
   - `vm.go` 2137라인에서 `f.tx.Update(targetTable, item.pk, item.updates)` 호출 시, 에러가 발생해도 이를 무시하고 `updatedCount`만 증가시키지 않은 채 정상 반환함 (`[RULE:ExplicitError]` 위반).
   - 스토리지 엔진이 `storage.ErrSerializationFailure` (Write Conflict)를 반환해도 호출 클라이언트에 에러가 전달되지 않아 트랜잭션 충돌 재시도 로직이 동작하지 않음.

2. **In-Memory OCC (`pkg/storage/memengine/tx.go`)의 스냅샷 버전 덮어쓰기**:
   - 트랜잭션 A가 `tx.Scan`으로 특정 행을 $V_0$ 버전에서 읽어 연산을 계산한 후 `tx.Update`를 호출할 때:
   - `updateLocked` 내부에서 `td.getRowAndVer`를 다시 호출하면서, 그 사이 트랜잭션 B가 커밋하여 증가된 $V_1$을 읽어 `tx.setBaseVer(tableName, pkKey, V_1)`로 갱신해 버림.
   - 이로 인해 커밋 시점 OCC 검증(`curVer != ch.baseVer`)에서 충돌을 감지하지 못하고, 트랜잭션 B의 갱신 내용을 이전 스냅샷 기반의 계산값으로 덮어써서 갱신 손실 발생.

3. **조건절 UPDATE 실행 파이프라인의 2단계 분리 구조**:
   - `sqlexec_dml.go` 및 `vm.go`에서 1단계 스캔 시 과거 스냅샷 기준으로 WHERE 조건과 SET 표현식을 사전 계산하여 `toUpdate`에 정적으로 담아둔 뒤, 2단계 루프에서 단순 `tx.Update`를 수행함.
   - 그 사이 타 트랜잭션에 의해 대상 행의 컬럼이 바뀌었거나 WHERE 조건이 더 이상 만족되지 않는 상태(`status`가 이미 `IN_PROGRESS`로 변경됨)임에도 불구하고 무조건 덮어쓰는 문제 발생.

---

## 2. 해결 방안 및 아키텍처 설계

### 2.1 VDBE `vm.go` UPDATE 오류 전파
- `pkg/proc/runtime/vm.go`의 `execUpdateWhere`:
  - `f.tx.Update` 호출 에러를 엄격히 검사하고, `ErrKeyNotFound`, `ErrSerializationFailure`, `ErrUniqueViolation` 등 모든 에러를 호출자 프레임에 즉시 전파.

### 2.2 `memengine` 초기 관측 버전 불변성 확립
- `memCursor`로 행을 스캔하거나 `Get`으로 읽었을 때의 버전($V_{obs}$)을 트랜잭션의 관측 버전으로 보존.
- `updateLocked`에서 대상 행의 버전을 갱신할 때:
  - 이미 해당 트랜잭션에서 관측한 `baseVer`가 존재하는 경우, 현재 샤드의 최신 버전이 관측 버전보다 크면(`curVer > origBaseVer`) 즉시 `storage.ErrSerializationFailure` (또는 `storage.ErrWriteConflict`)를 반환하여 빠른 충돌 감지(Fast-Fail) 보장.
  - 이를 통해 First-Committer-Wins 원칙에 따라 갱신 손실을 원천 차단.

### 2.3 `sqlexec_dml.go` 및 `vm.go`의 최신 커밋 상태 재검증 (Current Read)
- `toUpdate`를 순회하며 실제 변경을 적용하기 직전, 읽기 전용 스냅샷과 현재 커밋된 로우 간 버전/상태 변경을 검증:
  - 버전이 변경되었거나 타 트랜잭션이 이미 커밋한 경우, 직렬화 실패 에러(`ErrSerializationFailure`)를 반환하여 클라이언트가 표준 트랜잭션 재시도를 수행할 수 있도록 유도.

---

## 3. 검증 전략 (`[RULE:PipelineVerification]`)

1. **동시성 갱신 손실 검증 테스트 작성**:
   - `pkg/engine/concurrent_update_lost_update_test.go`:
     - 10개의 고루틴이 동일 행의 `balance`를 동시에 `+10`씩 갱신하는 시나리오.
     - 직렬화 충돌 발생 시 트랜잭션 재시도 루프를 수행하여, 최종 커밋 후 `balance`가 정확히 `초기값 + 100`이 됨을 검증 (Lost Update 0건 확인).
   - WHERE 조건 경합 시나리오:
     - `UPDATE tasks SET status = 'IN_PROGRESS' WHERE status = 'PENDING'`을 2개의 세션이 동시 실행 시, 정확히 1개 세션만 1행을 변경하고 다른 세션은 0행 변경(또는 충돌 후 0행)됨을 검증.
2. **단위 테스트 및 레이스 검증**:
   - `go test -v -race ./pkg/storage/memengine/...`
   - `go test -v -race -tags wiredtiger ./pkg/storage/wiredtiger/...`
   - `go test -v -race ./pkg/engine/...`
   - `go test -v -race ./pkg/proc/...`
3. **커밋 메시지**:
   - `fix(engine): prevent lost updates and enforce write conflict detection on concurrent updates (FIX-CONCURRENT-UPDATE-LOST-UPDATE-1)`
