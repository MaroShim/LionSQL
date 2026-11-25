# 트랜잭션(Tx) 모듈 엄밀성 강화를 위한 종합 테스트 계획서 (Test Plan)

> **문서 식별자**: `TX-MODULE-RIGOROUS-TEST-PLAN-V2`  
> **관련 아키텍처 규칙**: `[RULE:AtomicSaves]`, `[RULE:LockHierarchy]`, `[RULE:DoubleCloseSafety]`, `[RULE:HandleHygiene]`, `[RULE:DeterministicTesting]`, `[RULE:PipelineVerification]`  
> **대상 패키지**: `pkg/engine/`, `pkg/storage/`, `pkg/storage/memengine/`, `pkg/storage/wiredtiger/`, `pkg/wire/`  
> **용어 체계 정의**:
> - **Stage (실행 단계)**: 실제 작업이 순차적으로 진행되는 단일 실행 단위 (Stage 1 ➔ Stage 6).
> - **TDD Red-Green 원칙**: 모든 Stage의 테스트는 **실패하는 테스트(Red)를 먼저 작성**하여 결함/미비점을 입증한 후, 프로덕션 코드를 수정하여 통과(Green)시키고, `-race` 검증을 거쳐 커밋한다.
> - **기존 Tx 모듈 보존 원칙**: 현재 가동 중인 `ClientSession`, `TxManager`, `SessionCursor`, `storage.Transaction` 아키텍처를 전면 재작성하지 않고 그대로 계승/확장한다.

---

## 1. 개요 및 목적
LionSQL의 트랜잭션(`tx`) 모듈은 세션(`ClientSession`), 커서(`SessionCursor`, `storage.Cursor`), 임시 객체(`#temp`, `@var`), DDL 카탈로그, 스토리지 엔진(In-Memory OCC / WiredTiger MVCC)과 긴밀하게 결합되어 있습니다.

본 계획서는 기본 트랜잭션 흐름뿐만 아니라 **임시 객체 차등 롤백, 트랜잭셔널 DDL, 커넥션 풀 리셋, T-SQL 세이브포인트 특이점, XACT_STATE() 3단 전이, 카오스 동시성 스트레스**까지 상용 DBMS(SQL Server, PostgreSQL) 수준의 엄밀성을 단계별(Stage 1 ~ Stage 6)로 완벽하게 검증 및 안착시키는 것을 목표로 합니다.

---

## 2. 통합 아키텍처 구조도

```
+-----------------------------------------------------------------------------------+
|                        ClientSession (네트워크/세션 컨텍스트)                     |
|  - 세션 직렬화 락 (cs.Mu)                                                         |
|  - 트랜잭션 카운트 (TranCount: @@TRANCOUNT)                                        |
|  - 트랜잭션 실패 상태 플래그 (isTxFailed: SQLSTATE 25P02, XACT_STATE: -1/0/1)      |
|  - 임시 객체 저장소 (#temp: 트랜잭션 롤백 참여 vs @var: 트랜잭션 롤백 비참여)     |
+-----------------------------------------------------------------------------------+
                                        | (1:1 Binding)
+-----------------------------------------------------------------------------------+
|                        TxManager / storage.Transaction                            |
|  - BeginTx / Commit / Rollback / Savepoint / RollbackTo / ReleaseSavepoint         |
|  - Transactional DDL (테이블 생성/드롭/알터 롤백 시 카탈로그 및 스토리지 복원)       |
|  - Read-Only 트랜잭션 쓰기 차단 (SQLSTATE 25006)                                   |
|  - 커넥션 풀 반환 시 Dangling Tx 강제 롤백 (sp_reset_connection / DISCARD ALL)    |
+-----------------------------------------------------------------------------------+
         |                                                 |
         v (Cursors Binding)                               v (Storage Parity)
+----------------------------------+             +----------------------------------+
|    Server-Side & Storage Cursors |             |     Storage Engine Isolation     |
| - SessionCursor (WITH/WITHOUT)   |             | - In-Memory OCC (Version Stamp)  |
| - storage.Cursor (Low-level)     |             | - WiredTiger MVCC (Hazard Ptr)   |
| - Commit 시 비홀드 커서 Close     |             | - Write-Write 충돌 즉시 감지     |
| - Rollback 시 전체 커서 Annihilate|            | - 읽기-쓰기 Non-Blocking 불변성  |
+----------------------------------+             +----------------------------------+
```

---

## 3. 단계별 세부 테스트 계획 (Stages 1 ~ 6)

### Stage 1: 세션-트랜잭션 상태 머신 & T-SQL 시맨틱 엄밀성
- **T1.1: 오토커밋(Autocommit) ⇋ 명시적 트랜잭션 경계 전이**
  - 암묵적 오토커밋 모드에서 단일 DML 즉시 엔진 반영.
  - `BEGIN TRANSACTION` 진입 시 오토커밋 해제 및 트랜잭션 전용 워크스페이스 격리 확인.
- **T1.2: 중첩 트랜잭션(`@@TRANCOUNT`) 물리 커밋 불변성**
  - `@@TRANCOUNT > 1`에서 `COMMIT` 호출 시 카운터만 감소(`DecTranCount()`)하고 물리 스토리지는 미커밋 유지.
  - 최외곽 `COMMIT`(`@@TRANCOUNT == 0`) 시점에만 물리 스토리지 일괄 영구 반영.
- **T1.3: 다중 중첩 상태 단일 롤백 전면 무효화 (Total Annihilation)**
  - 깊은 중첩 상태(`@@TRANCOUNT = 5`)에서 단 1회의 `ROLLBACK TRANSACTION`으로 카운터 즉시 0 초기화 및 모든 중첩 변경 일괄 취소.
- **T1.4: T-SQL 세이브포인트 특이점 (`SAVE TRAN` vs `ROLLBACK TRAN <name>`)**
  - T-SQL 규격: `ROLLBACK TRANSACTION <savepoint_name>`은 **`@@TRANCOUNT`를 감소시키지 않고 유지**해야 함.
  - 이름 없는 `ROLLBACK TRANSACTION`만 `@@TRANCOUNT`를 0으로 초기화함을 대조 검증.
- **T1.5: ANSI 세이브포인트 스택 심도, 섀도잉 및 해제 후 롤백 차단**
  - 동일 이름 세이브포인트 재정의 시 최신 세이브포인트 섀도잉.
  - `RELEASE SAVEPOINT`로 해제된 세이브포인트로의 롤백 시도 시 `SQLSTATE 3B001` Fail-Closed 차단.
- **T1.6: 세미콜론 다중 구문 배치 (Semicolon Batch Atomicity)**
  - `BEGIN TRAN; INSERT ...; INSERT ...; COMMIT;`가 단일 네트워크 호출로 인입될 때 단일 트랜잭션 컨텍스트로 원자적 일괄 처리. 중간 실패 시 배치 전체 롤백.
- **T1.7: 읽기 전용 가드 (`READ ONLY` Fail-Closed)**
  - `SET TRANSACTION READ ONLY` 또는 `BEGIN TRANSACTION READ ONLY` 시 INSERT/UPDATE/DELETE/DDL 실행 즉시 `SQLSTATE 25006` 차단.

---

### Stage 2: 트랜잭셔널 DDL & 임시 객체(`#temp` vs `@var`) 차등 롤백
- **T2.1: `CREATE TABLE` / `DROP TABLE` 트랜잭션 롤백 무결성**
  - `BEGIN TRAN; CREATE TABLE t_new (...); INSERT ...; ROLLBACK;` ➔ 카탈로그 및 스토리지 양쪽에서 `t_new` 완전 증발.
  - `BEGIN TRAN; DROP TABLE t_exist; ROLLBACK;` ➔ 드롭되었던 기존 테이블과 데이터 100% 원복 보존.
- **T2.2: `ALTER TABLE ADD/DROP COLUMN` 롤백 시 스키마 복원**
  - 트랜잭션 내 컬럼 추가/삭제 후 롤백 시 메타데이터와 튜플 디코더가 직전 스키마 상태로 정확히 복원되는지 확인.
- **T2.3: 로컬 임시 테이블(`#temp`)의 트랜잭션 롤백 100% 참여**
  - `#temp` 테이블에 INSERT 후 트랜잭션 `ROLLBACK` 시 `#temp` 내부 데이터 롤백.
  - 트랜잭션 내 `CREATE TABLE #temp` 후 `ROLLBACK` 시 `#temp` 테이블 자체 파기.
- **T2.4: 테이블 변수(`@var`)의 트랜잭션 롤백 비참여 불변성**
  - T-SQL 공식 규격: 트랜잭션이 에러로 롤백되더라도 `@table_var`에 저장된 에러 로깅 레코드는 보존됨을 검증.

---

### Stage 3: 커서-트랜잭션 바인딩 및 생명주기 연동
- **T3.1: 일반 커서(WITHOUT HOLD) `COMMIT` 시 자동 폐기**
  - 트랜잭션 커밋 즉시 커서 상태가 `IsOpen=false`, `IsClosed=true`, `Rows=nil`로 폐기되고 이후 FETCH 차단.
- **T3.2: `WITH HOLD` 커서의 `COMMIT` 통과 및 연속 순회**
  - `DECLARE cur CURSOR WITH HOLD` 선언 시 `COMMIT` 이후에도 커서가 살아남아 다음 트랜잭션/오토커밋에서 순회 지속.
- **T3.3: `ROLLBACK` 시 `WITH HOLD` 포함 모든 커서 전면 무효화**
  - 롤백 시 데이터 스냅샷 오염 방지를 위해 `WITH HOLD` 커서도 예외 없이 즉시 강제 파괴.
- **T3.4: 저수준 스토리지 커서(`storage.Cursor`) 핸들 누수 방지 (`[RULE:HandleHygiene]`)**
  - 쿼리 실패, 타임아웃, 트랜잭션 중단 시 underlying B-Tree / OCC 커서가 방치되지 않고 `defer Close()` 및 `sync.Once`로 닫힘 보장.

---

### Stage 4: 오류 상태 격리, `XACT_STATE()` & 비정상 연결 단절 회수
- **T4.1: Postgres 모드 Aborted Transaction Guard (SQLSTATE `25P02`)**
  - 쿼리 에러 발생 시 `isTxFailed` 상태로 전이되어 `ROLLBACK` 외 모든 명령 차단.
- **T4.2: T-SQL `XACT_STATE()` 함수 3단 전이 검증**
  - 트랜잭션 없음: `0`
  - 활성 정상 커밋 가능: `1`
  - 런타임 오류 발생 직후 (Doomed Tx): `-1` (DML 차단, 오직 ROLLBACK만 수용)
  - ROLLBACK 완료 후: `0`으로 복구.
- **T4.3: 커넥션 풀 반환 시 Dangling 트랜잭션 강제 정리 (`sp_reset_connection` / `DISCARD ALL`)**
  - 클라이언트가 트랜잭션을 연 채 커넥션 풀에 반환했을 때, 미완료 Tx 강제 롤백, 열린 커서 전면 deallocate, `@@TRANCOUNT = 0` 초기화.
- **T4.4: 클라이언트 Disconnect 강제 롤백 검증**
  - 트랜잭션 도중 소켓 단절 시 1.5초 내 물리 `Rollback()` 및 행 잠금 해제.
- **T4.5: 유휴 트랜잭션 타임아웃 (`idle_in_transaction_session_timeout`)**
  - 트랜잭션을 방치한 유휴 세션을 백그라운드 스위퍼가 자동 롤백 및 종료.
- **T4.6: 트랜잭션/커서 이중 해제 멱등성 (`[RULE:DoubleCloseSafety]`)**
  - 롤백 또는 세션 종료 중복 호출 시 패닉 없는 안전한 멱등성 보장.

---

### Stage 5: 다중 세션 격리 수준 & 동시성 불변성
- **T5.1: 동일 세션 동시 쿼리 직렬화 불변성 (Single-Session Mutex)**
  - 동일 SessionID로 50개 고루틴이 동시 쿼리 인입 시 세션 뮤텍스를 통해 완벽한 순차 실행 보장 (Lost Update 0건).
- **T5.2: 비차단 읽기(Non-Blocking Read) 및 스냅샷 격리**
  - 세션 A의 대량 쓰기 중에도 세션 B의 읽기는 블로킹 없이 커밋된 스냅샷 즉시 반환.
- **T5.3: 쓰기-쓰기 충돌(Write-Write Conflict) Fast-Fail**
  - 동일 행 동시 수정 시 데드락 지연 없이 `ErrWriteConflict` / `ErrSerializationFailure` 즉각 반환.
- **T5.4: 격리 수준 설정 및 전이 (`SET TRANSACTION ISOLATION LEVEL`)**
  - `READ COMMITTED`, `SNAPSHOT` 정상 지원 및 트랜잭션 진행 중 변경 시도 차단.

---

### Stage 6: 스토리지 듀얼 백엔드 패리티 및 32워커 카오스 스트레스
- **T6.1: In-Memory OCC vs WiredTiger MVCC 트랜잭션 패리티**
  - 모든 트랜잭션 시나리오를 양대 스토리지에서 구동하여 100% 동일한 결과 검증.
- **T6.2: 32개 워커 고동시성 카오스 스트레스 테스트 (Chaos Stress)**
  - 32개 병렬 고루틴이 무작위로 `BEGIN / DML / SAVEPOINT / CURSOR / COMMIT / ROLLBACK / DISCONNECT`를 10,000회 연속 실행하여 데이터 레이스(`go test -race`) 0건 및 파일 무결성 확인.

---

## 4. 최종 실행 단계 매핑 (Execution Roadmap)

| 실행 단계 | 목표 영역 | 핵심 검증 대상 |
| :--- | :--- | :--- |
| **Stage 1** | 상태 머신 & T-SQL 시맨틱 | 오토커밋 전이, `@@TRANCOUNT`, T-SQL/ANSI 세이브포인트, 세미콜론 배치, Read-Only |
| **Stage 2** | DDL & 임시 객체 롤백 | `CREATE/DROP/ALTER TABLE` 롤백, `#temp` 롤백 참여 vs `@var` 롤백 비참여 |
| **Stage 3** | 커서 생명주기 바인딩 | `WITH HOLD` 커밋 생존, `ROLLBACK` 전체 파괴, 스토리지 커서 핸들 누수 방지 |
| **Stage 4** | 에러 격리 & 풀 리셋 | SQLSTATE 25P02, `XACT_STATE()`, `sp_reset_connection`, Disconnect 자동 롤백 |
| **Stage 5** | 다중 세션 격리 & 동시성 | 단일 세션 직렬화, Non-Blocking Read, Write-Write 충돌 Fast-Fail |
| **Stage 6** | 스토리지 패리티 & 카오스 | OCC vs WiredTiger 100% 일치, 32워커 동시성 카오스 레이스 검증 |
