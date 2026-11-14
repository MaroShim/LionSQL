# 1세션 1트랜잭션 불변식 확립 및 세션 명령 직렬화 아키텍처 계획서
(`notes/plans/REFACTOR_SESSION_TRANSACTION_INVARIANT_PLAN.ko.md`)

> **문서 상태**: Approved (백로그 정식 등록)  
> **태스크 ID**: `REFACTOR-SESSION-TRANSACTION-INVARIANT-1`  
> **마일스톤**: `v1.10.0` (Post-1.0 Core Architecture)  
> **관련 규칙**: `[RULE:ConcurrencySafety]`, `[RULE:LockHierarchy]`, `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`, `[RULE:ZeroOracleTampering]`

---

## 1. 추진 배경 및 결함 분석 (Problem Statement)

### 1.1 결함 현상
LionSQL의 세션-트랜잭션 생명주기 관리에서 **단일 클라이언트 세션 ID 하에서 2개 이상의 독립 물리 트랜잭션이 동시에 실행**될 수 있는 구조적 결함이 존재합니다.

### 1.2 발생 원인
1. **Autocommit 동시 실행 시 세션 풀 우회 (`coordinator_impl.go:453-475`)**:
   - `ClientSession.Tx == nil`인 상태에서 HTTP/Web Studio 또는 비동기 환경에서 동일 `session_id`로 2개 이상의 쿼리가 동시 인입될 때:
   - 첫 번째 쿼리가 `cs.Session`을 점유(`cs.sessInUse = true`)하고 트랜잭션 $T_1$을 시작함.
   - 두 번째 쿼리가 `cs.sessInUse == true`를 확인하고, 전역 풀(`c.pool`)에서 **별도의 새로운 스토리지 세션을 빌려와 또 다른 트랜잭션 $T_2$를 동시에 실행**함.
   - 이로 인해 동일한 클라이언트 세션인데 서로 다른 트랜잭션 격리 스냅샷을 보게 되며, 세션 스코프 상태(임시 테이블, 세션 변수)가 오염됨.
2. **Volcano 서브쿼리 평가 시 재귀 호출 (`sqlexec_select.go:149-160`)**:
   - 메인 트랜잭션이 진행 중인 상태에서 상관 서브쿼리를 실행할 때 `ExecuteSessionSQLContext(..., sessID, q)`를 재귀 호출하여 트랜잭션 컨텍스트가 분리되거나 세션 뮤텍스 경합/교착 위험 발생.
3. **스토리지 엔진 트랜잭션의 스레드 비안전성**:
   - WiredTiger의 `WT_SESSION` 및 B-Tree 커서는 멀티스레드 안전(Thread-safe)하지 않음. 동일 세션/트랜잭션 핸들에 여러 고루틴이 동시 접근하면 C 레벨 메모리 오염 및 크래시 유발.

---

## 2. 목표 아키텍처 및 불변식 (Architectural Invariant)

> **핵심 불변식 (Single Active Transaction Invariant)**:  
> **"단일 클라이언트 세션(`ClientSession`)은 특정 시점에 오직 단 하나의 활성 스토리지 세션 및 물리 트랜잭션 문맥만을 가지며, 세션에 속한 모든 명령은 반드시 직렬(Serial)로 실행된다."**

```mermaid
flowchart TD
    subgraph ClientSession ["ClientSession (cs.ID)"]
        Q1["Query 1 인입"] --> Lock["Session Execution Mutex (cs.execMu)"]
        Q2["Query 2 인입 (동시)"] -->|대기 (Block/Queue)| Lock
        Lock --> Exec["단일 쿼리 순차 실행"]
        Exec --> Tx["단일 트랜잭션 문맥 (cs.Tx / cs.Session)"]
    end
```

1. **세션 수준 명령 직렬화 (Session Execution Serialization)**:
   - `ClientSession` 구조체에 세션 전용 명령 실행 락(`execMu sync.Mutex`) 또는 직렬 채널 큐를 도입.
   - 동일 세션 ID로 유입되는 모든 쿼리/배치는 진입 단계에서 상호 배타적으로 직렬화되어 동시성 충돌을 원천 차단.
2. **세션 풀 우회(Bypass) 영구 제거**:
   - `acquireSessionForQuery`에서 `cs.sessInUse`일 때 전역 풀에서 다른 세션을 빌려오던 편법 로직을 완전히 삭제.
   - 단일 세션은 오직 자신에게 할당된 단 하나의 `cs.Session`만을 사용하며, 선행 쿼리가 완료되어 반납할 때까지 후행 쿼리가 안전하게 대기.
3. **서브쿼리 실행 시 상위 트랜잭션 컨텍스트 직결 상속**:
   - Volcano 서브쿼리 실행 시 `ExecuteSessionSQL` 재귀 호출을 중단하고, 상위 트랜잭션의 `tx.Scan` 커서를 직접 상속받아 평가.

---

## 3. 세부 구현 단계 (Implementation Phases)

### Phase 1: `ClientSession` 명령 직렬화 락 도입 (`pkg/engine/`)
- `ClientSession`에 `execMu sync.Mutex` 추가.
- `ExecuteSessionSQLContext` 진입 시 `cs.execMu.Lock()`을 획득하고, 쿼리 및 다중 배치 완료 시 `defer cs.execMu.Unlock()`으로 해제.
- TDS/PGWire의 네트워크 스트림뿐 아니라 Web Studio / HTTP REST API 요청에 대해서도 세션 단위 완전 직렬화 보장.

### Phase 2: `acquireSessionForQuery` 단일 세션 강제 및 풀 우회 삭제 (`pkg/engine/`)
- `coordinator_impl.go:453`의 풀 대여 fallback 브랜치 제거.
- 세션 실행이 직렬화되었으므로 `cs.Session`은 언제나 단일 고루틴에 의해서만 접근됨을 보장.

### Phase 3: 서브쿼리 트랜잭션 상속 리팩토링 (`pkg/engine/sqlexec_select.go`)
- `opts.SubqueryExecutor`가 `ExecuteSessionSQL`을 호출하지 않고, 부모 컨텍스트의 `tx`를 전달받아 엔진 내부 플래너로 직접 행을 조회하도록 전환.

### Phase 4: 동시성 검증 및 소크 테스트 (`test/`)
- 동일 세션 ID로 100개의 고루틴이 동시 INSERT/SELECT/UPDATE를 호출하는 공격적 동시성 스트레스 테스트 작성 (`session_concurrency_test.go`).
- 0 데이터 레이스, 0 메모리 오염, 100% 직렬 트랜잭션 정합성 검증 (`go test -v -race`).

---

## 4. 기대 효과 (Engineering Impact)
1. **격리성 무결성(ACID Isolation)**: 세션 내 트랜잭션 스냅샷 불일치 및 미커밋 데이터 누락 버그의 완전 박멸.
2. **엔진 안전성(Crash Safety)**: WiredTiger C 세션/커서의 다중 고루틴 동시 접근 위험을 원천 제거하여 데몬 안정성 극대화.
3. **아키텍처 일관성**: T-SQL/Postgres 표준 세션 모델과의 100% 합치.
