# 트랜잭션(Tx) 모듈 엄밀성 강화를 위한 종합 테스트 계획서 (Test Plan)

> **문서 식별자**: `TX-MODULE-RIGOROUS-TEST-PLAN-V1`  
> **관련 아키텍처 규칙**: `[RULE:AtomicSaves]`, `[RULE:LockHierarchy]`, `[RULE:DoubleCloseSafety]`, `[RULE:HandleHygiene]`, `[RULE:DeterministicTesting]`, `[RULE:PipelineVerification]`  
> **대상 패키지**: `pkg/engine/`, `pkg/storage/`, `pkg/storage/memengine/`, `pkg/storage/wiredtiger/`, `pkg/wire/`
> **엔지니어링 원칙**:
> 1. **기존 Tx 모듈 보존 및 점진적 확장 (Preserve & Extend Existing Architecture)**: 전면 재작성이나 불필요한 레이어 추가를 엄격히 금지하며, 현재 안정적으로 가동 중인 `ClientSession`, `TxManager`, `SessionCursor`, `storage.Transaction`의 계약과 구현을 그대로 계승하고 필요한 인터페이스/메서드만을 최소 침습적으로 확장한다.
> 2. **엄격한 TDD Red-Green 사이클 (Strict TDD Red-Green Discipline)**: 모든 경계 조건 및 결함 검증은 **반드시 실패하는 테스트(Red)를 먼저 작성**하여 기존 엔진의 결함이나 미비점을 입증한 후, 프로덕션 코드를 수정하여 통과(Green)시키고, `-race` 검증 및 리팩토링 파이프라인을 거쳐 커밋한다.

---

## 1. 개요 및 목적
LionSQL의 트랜잭션(`tx`) 모듈은 단독으로 동작하지 않고 **ClientSession(세션)**, **SessionCursor / Storage.Cursor(커서)**, **Virtual/Temp Table(임시 테이블)**, **스토리지 엔진(OCC / WiredTiger)**과 긴밀하게 결합되어 있습니다.

본 계획서는 트랜잭션 생명주기, 세션 바인딩, 커서 홀드/폐기, 세이브포인트 스택, 장애/비정상 종료 시 롤백 정합성을 극한의 경계 조건(Edge Cases)과 동시성 카오스(Concurrency Chaos) 상황에서 검증하여 **상용 DBMS(SQL Server, PostgreSQL) 수준의 엄밀성(Rigor)**을 확보하는 것을 목표로 합니다.

---

## 2. 핵심 검증 매트릭스 및 계층 구조

```
+-----------------------------------------------------------------------------------+
|                        ClientSession (네트워크/세션 컨텍스트)                     |
|  - 세션 직렬화 락 (cs.Mu)                                                         |
|  - 트랜잭션 카운트 (TranCount: @@TRANCOUNT)                                        |
|  - 트랜잭션 실패 상태 플래그 (isTxFailed: SQLSTATE 25P02)                          |
+-----------------------------------------------------------------------------------+
                                        | (1:1 Binding)
+-----------------------------------------------------------------------------------+
|                        TxManager / storage.Transaction                            |
|  - BeginTx / Commit / Rollback / Savepoint / RollbackTo / ReleaseSavepoint         |
|  - Read-Only 트랜잭션 쓰기 차단 (SQLSTATE 25006)                                   |
+-----------------------------------------------------------------------------------+
         |                                                 |
         v (Cursors Binding)                               v (Storage Parity)
+----------------------------------+             +----------------------------------+
|    Server-Side & Storage Cursors |             |     Storage Engine Isolation     |
| - SessionCursor (WITH/WITHOUT)   |             | - In-Memory OCC (Version Monotonic)
| - storage.Cursor (Low-level)     |             | - WiredTiger MVCC (Hazard Ptr)   |
| - Commit 시 비홀드 커서 Close     |             | - Write-Write 충돌 즉시 감지     |
| - Rollback 시 전체 커서 Annihilate|            | - 읽기-쓰기 Non-Blocking 불변성  |
+----------------------------------+             +----------------------------------+
```

---

## 3. 단계별 세부 테스트 계획 (Phased Test Plan)

### Phase 1: 세션-트랜잭션 상태 머신 및 오토커밋 엄밀성 (State Machine Rigor)
트랜잭션 시작, 중첩 카운트, 커밋, 롤백 시 세션 상태 머신의 전이 정합성을 검증합니다.

- **T1.1: 오토커밋(Autocommit) ⇋ 명시적 트랜잭션 경계 전이 테스트**
  - 암묵적 오토커밋 모드에서 단일 DML 실행 후 즉각적인 엔진 반영 확인.
  - `BEGIN TRANSACTION` 진입 시 오토커밋 해제 및 트랜잭션 전용 워크스페이스 활성화 확인.
  - 중복 `BEGIN TRANSACTION` 실행 시 SQL Server 호환 `@@TRANCOUNT` 증가 vs Postgres 모드 에러 처리 정합성.
- **T1.2: 중첩 트랜잭션(`@@TRANCOUNT`) 물리 커밋 불변성 테스트**
  - `@@TRANCOUNT > 1`인 상태에서 `COMMIT TRANSACTION` 호출 시, 물리 스토리지는 커밋되지 않고 오직 카운터만 감소(`DecTranCount()`)하는지 확인.
  - 카운터가 1에서 0으로 떨어지는 최외곽 `COMMIT` 시점에만 물리 스토리지에 데이터가 일괄 반영되는지 검증.
- **T1.3: 다중 중첩 상태에서 롤백의 전면 무효화(Total Annihilation) 테스트**
  - `@@TRANCOUNT = 5`인 깊은 중첩 상태에서 `ROLLBACK TRANSACTION` 단 1회 호출 시 카운터가 즉시 0으로 초기화되고, 모든 중첩 단계의 수정 사항이 100% 롤백되는지 검증.
- **T1.4: 세이브포인트(Savepoint) 스택 심도 및 섀도잉(Shadowing) 테스트**
  - 동일한 이름의 세이브포인트 재정의 시 최신 세이브포인트 섀도잉 검증 (`SAVEPOINT sp1 -> ... -> SAVEPOINT sp1 -> ROLLBACK TO sp1`).
  - 중간 세이브포인트 롤백 시 상위 세이브포인트 유지 및 하위 세이브포인트 무효화 확인.
  - `RELEASE SAVEPOINT` 호출 후 해당 세이브포인트로의 롤백 시도 시 명시적 에러(`3B001`) 반환 확인.
- **T1.5: 읽기 전용 트랜잭션 가드 (`READ ONLY` Fail-Closed)**
  - `SET TRANSACTION READ ONLY` 또는 `BEGIN TRANSACTION READ ONLY` 설정 후 `INSERT`, `UPDATE`, `DELETE`, `DDL` 실행 시 즉시 `SQLSTATE 25006` 차단 검증.

---

### Phase 2: 커서-트랜잭션 바인딩 및 생명주기 연동 (Cursor Pinning & Lifecycle)
트랜잭션 종료 시 서버 사이드 커서와 스토리지 저수준 커서의 안전한 폐기 및 누수 방지를 검증합니다.

- **T2.1: 일반 커서(WITHOUT HOLD)의 `COMMIT` 시 자동 폐기 불변성**
  - 명시적 트랜잭션 내에서 `DECLARE cur CURSOR FOR SELECT ...` 후 `OPEN` 및 일부 `FETCH`.
  - `COMMIT` 실행 즉시 커서 상태가 `IsOpen = false`, `IsClosed = true`, `Rows = nil`로 안전하게 폐기되는지 확인.
  - 커밋 후 `FETCH` 시도 시 명시적 에러(Cursor is closed) 확인.
- **T2.2: `WITH HOLD` 커서의 `COMMIT` 통과 및 연속 순회 검증**
  - `DECLARE cur CURSOR WITH HOLD FOR SELECT ...` 선언 후 트랜잭션 `COMMIT`.
  - 트랜잭션이 종료된 후에도 커서가 닫히지 않고 다음 트랜잭션이나 오토커밋 모드에서 정상적으로 `FETCH NEXT`가 이어지는지 검증.
- **T2.3: `ROLLBACK` 시 모든 커서(WITH HOLD 포함) 무조건 파괴 검증**
  - `WITH HOLD`로 선언된 커서라 할지라도 트랜잭션이 `ROLLBACK`되면 데이터 스냅샷의 신뢰성이 상실되므로 **예외 없이 즉시 강제 폐기**되는지 검증.
- **T2.4: 세이브포인트 롤백과 커서 스냅샷 정합성**
  - 세이브포인트 생성 후 커서를 열었을 때, `ROLLBACK TO SAVEPOINT`가 실행되면 해당 세이브포인트 이후 열린 커서의 상태 처리 및 리소스 정리 확인.
- **T2.5: 스토리지 레벨 커서(`storage.Cursor`) 핸들 누수 방지 (`[RULE:HandleHygiene]`)**
  - 대량 스캔 중 트랜잭션이 에러나 취소로 중단되었을 때, underlying B-Tree / OCC 커서가 방치되지 않고 `defer Close()` 및 `sync.Once`로 누수 없이 닫히는지 추적.

---

### Phase 3: 오류 상태 격리 및 트랜잭션 오염 방지 (Aborted Tx & Error State)
쿼리 실행 중 문법/런타임 에러 발생 시 트랜잭션이 안전하게 실패 상태로 격리되는지 검증합니다.

- **T3.1: Postgres 모드 Aborted Transaction Guard (SQLSTATE `25P02`)**
  - 트랜잭션 블록 내에서 쿼리 오류(예: 고유 키 제약 위반, 0으로 나누기) 발생 시 세션이 `isTxFailed` 상태로 전환되는지 확인.
  - 이후 유입되는 모든 쿼리가 `current transaction is aborted, commands ignored until end of transaction block`으로 즉시 Fail-Closed 차단되는지 확인.
  - 오직 `ROLLBACK` 명령만 수용되어 세션이 정상 복구되는지 검증.
- **T3.2: T-SQL `XACT_ABORT` 모드별 롤백 경계 검증**
  - `SET XACT_ABORT ON`: 런타임 오류 시 전체 배치 및 트랜잭션 즉시 강제 롤백.
  - `SET XACT_ABORT OFF`: 구문 오류 시 오류 구문만 건너뛰고 트랜잭션 유지 후 사용자 `TRY...CATCH` 허용.
- **T3.3: 패닉 복구 및 터미널 구조 연동 (`[RULE:PanicRecovery]`)**
  - 쿼리 실행 중 고루틴 패닉 발생 시 상위 리커버리 훅이 트랜잭션을 강제 `Rollback()`하고 세션을 격리하여 프로세스 크래시 및 락 잔류를 방지하는지 검증.

---

### Phase 4: 다중 세션 동시성 및 격리 수준 불변성 (Concurrency & Isolation)
여러 세션이 동시에 트랜잭션을 수행할 때의 데이터 일관성을 검증합니다.

- **T4.1: 동일 세션 동시 쿼리 직렬화 불변성 (Single-Session Mutex)**
  - 동일한 `SessionID`를 가진 클라이언트 연결에서 50개 고루틴이 동시 쿼리를 던질 때, 세션 뮤텍스(`cs.Mu` / `TxManager`)를 통해 정확히 단일 트랜잭션 타임라인으로 직렬화되는지 검증 (Lost Update 0건).
- **T4.2: 비차단 읽기(Non-Blocking Read) 및 스냅샷 격리 불변성**
  - 세션 A가 대량 `UPDATE`를 수행 중(미커밋 상태)일 때, 세션 B의 `SELECT`가 블로킹되지 않고 직전 커밋된 스냅샷을 즉시 읽는지 확인 (읽기는 쓰기를 막지 않고, 쓰기는 읽기를 막지 않음).
- **T4.3: 쓰기-쓰기 충돌(Write-Write Conflict) Fast-Fail 검증**
  - 세션 A와 세션 B가 동일한 행을 동시에 수정하려 할 때, 한쪽이 지연 대기 후 데드락에 걸리지 않고 `ErrWriteConflict` / `ErrSerializationFailure`로 즉시 신속 실패(Fast-Fail)하는지 검증.
- **T4.4: 락 계층 구조 데드락 방지 검증 (`[RULE:LockHierarchy]`)**
  - 교차 테이블 갱신 시(세션 A: T1 -> T2, 세션 B: T2 -> T1) 정형화된 잠금 순서 준수로 데드락 발생 없이 정합성을 유지하는지 검증.

---

### Phase 5: 비정상 연결 종료 및 자원 누수 검증 (Disconnect & Teardown Rigor)
클라이언트가 트랜잭션 도중 비정상적으로 연결을 끊거나 타임아웃이 발생할 때의 리소스 수거를 검증합니다.

- **T5.1: 트랜잭션 진행 중 클라이언트 Disconnect 강제 롤백 검증**
  - `BEGIN TRANSACTION` 및 DML 수행 후 클라이언트 소켓이 강제 종료(`Close()`)되었을 때, 세션 풀 및 코디네이터가 이를 감지하여 1.5초 내에 즉시 물리 `Rollback()`을 호출하고 행 잠금을 해제하는지 검증.
- **T5.2: 유휴 트랜잭션 타임아웃 (`idle_in_transaction_session_timeout`)**
  - 트랜잭션을 열어두고 설정된 시간(예: 500ms) 동안 후속 쿼리가 없는 세션을 백그라운드 스위퍼가 자동으로 강제 롤백 및 종료하는지 검증.
- **T5.3: 자원 해제 멱등성 및 이중 Close 방지 (`[RULE:DoubleCloseSafety]`)**
  - 트랜잭션 및 커서 닫기 핸들러에 `sync.Once`가 완벽히 적용되어 중복 `Rollback()` 또는 중복 `Close()` 호출 시 패닉이 발생하지 않는지 검증.

---

### Phase 6: 스토리지 듀얼 백엔드 패리티 스트레스 (Dual Backend Parity)
동일한 트랜잭션 시나리오를 In-Memory OCC와 WiredTiger 양대 엔진에서 구동하여 100% 동일한 결과가 도출되는지 대조 검증합니다.

- **T6.1: In-Memory OCC vs WiredTiger MVCC 트랜잭션 패리티 검증**
  - 세이브포인트, 커서 홀드, 롤백, 격리 수준 테스트를 양쪽 엔진에 동일하게 실행하여 완벽한 동작 일치를 확인.
- **T6.2: 32개 워커 고동시성 카오스 스트레스 테스트 (Chaos Stress)**
  - 32개 병렬 고루틴이 무작위로 `BEGIN`, `INSERT`, `UPDATE`, `SAVEPOINT`, `CURSOR OPEN/FETCH`, `COMMIT`, `ROLLBACK`, `DISCONNECT`를 10,000회 연속 실행.
  - 데이터 레이스(`go test -race`) 0건, 고루틴 누수 0건, 언더라이잉 B-Tree/OCC 파일 무결성 100% 확인.

---

## 4. 실행 일정 및 단계별 진행 권장안

| 단계 | 목표 영역 | 핵심 검증 대상 | 예상 소요 |
| :--- | :--- | :--- | :--- |
| **Stage 1** | 세션-트랜잭션 상태 머신 | 오토커밋 전이, `@@TRANCOUNT`, 중첩 롤백, 세이브포인트 | 1 세션 |
| **Stage 2** | 커서 생명주기 바인딩 | `WITH HOLD` 커밋 생존, `ROLLBACK` 전체 파괴, 누수 방지 | 1 세션 |
| **Stage 3** | 에러 격리 & Disconnect | SQLSTATE 25P02, 연결 단절 자동 롤백, 유휴 타임아웃 | 1 세션 |
| **Stage 4** | 다중 세션 동시성 & 카오스 | 비차단 읽기, Write-Write 충돌, 32워커 카오스 레이스 | 1 세션 |

---

## 5. 결론 및 향후 조치
본 테스트 플랜을 기반으로 Stage 1부터 순차적으로 전용 테스트 스위트(`tx_session_statemachine_test.go`, `tx_cursor_lifecycle_test.go`, `tx_concurrency_chaos_test.go`)를 작성하고 발견되는 경계 결함을 즉시 보완해 나갈 것을 제안합니다.
