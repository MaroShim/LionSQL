# CloseSession 시 In-flight 쿼리 Drain 대기 보장 및 CGo UAF 크래시 방지 계획서
(`notes/plans/FIX_SESSION_DRAIN_CLOSE_UAF_PLAN.ko.md`)

> **문서 상태**: Approved (백로그 정식 등록)  
> **태스크 ID**: `FIX-SESSION-DRAIN-CLOSE-UAF-1`  
> **마일스톤**: `1.0-GA` (Core Stability & Crash Safety)  
> **관련 규칙**: `[RULE:CleanTeardown]`, `[RULE:ZeroOrphan]`, `[RULE:PanicRecovery]`, `[RULE:LockHierarchy]`, `[RULE:ContinuousBacklog]`

---

## 1. 추진 배경 및 결함 분석 (Problem Statement)

### 1.1 결함 현상
클라이언트 네트워크 연결이 강제 종료(단선)되거나 타임아웃으로 `CloseSession(id)`이 호출될 때, 해당 세션에서 실행 중이던 비동기 백그라운드 쿼리가 여전히 동작하고 있음에도 불구하고 트랜잭션(`cs.Tx`)이 조기 롤백 및 파기되어 **CGo Use-After-Free (UAF), nil pointer dereference, 또는 WiredTiger 세그멘테이션 폴트(SIGSEGV) 크래시**가 발생할 수 있습니다.

### 1.2 발생 위치 및 코드 분석 (`pkg/engine/coordinator_impl.go:571-623`)
```go
// pkg/engine/coordinator_impl.go:571
if exists && cs != nil {
    // 1. 비동기 컨텍스트 취소 요청 (고루틴은 아직 도는 중)
    cs.CancelQuery()

    cs.Mu.Lock()
    // [결함 지점]: in-flight 쿼리가 아직 끝나지 않았는데 cs.Tx.Rollback()을 즉시 호출하고 nil 처리!
    if cs.Tx != nil {
        _ = cs.Tx.Rollback()
        cs.Tx = nil
    }
    cs.TxFailed = false
    ...
    // 하단에서는 cs.Session에 대해서만 inFlight 검사를 수행함 (cs.Tx는 이미 파괴됨)
    inFlight := cs.activeQueries > 0 || cs.sessInUse
    if inFlight {
        cs.releaseOnDrain = ...
    }
    cs.Mu.Unlock()
}
```

### 1.3 위험성
1. **WiredTiger 동시성 위반**:
   - 실행 중인 고루틴이 `WT_CURSOR`를 통해 C 메모리를 스캔하고 있는 도중, `CloseSession`을 호출한 다른 스레드가 동일한 `WT_SESSION`에 대해 `session->rollback_transaction()`을 호출함.
   - WiredTiger C 커널은 동일 `WT_SESSION`에 대한 멀티스레드 동시 접근을 엄격히 금지하므로 `WT_PANIC` 또는 SIGSEGV 크래시 발생.
2. **Nil Pointer Dereference**:
   - 쿼리 고루틴이 에러를 감지하고 defer 구문에서 `cs.Tx.Rollback()`을 호출하려 할 때, 이미 `cs.Tx`가 nil로 지워져 있어 패닉 발생.

---

## 2. 해결 목표 (Target Architecture)

1. **In-flight 쿼리 완전 배출(Drain) 동기화 보장**:
   - `CloseSession` 시 `cs.CancelQuery()`를 호출하여 실행 중인 쿼리에 즉시 중단 신호를 보냄.
   - 트랜잭션(`cs.Tx`)을 강제로 조기 롤백하지 않고, 쿼리 실행 고루틴이 취소 신호를 수신하고 C 커서를 모두 닫고 안전하게 탈출(`activeQueries == 0 && !cs.sessInUse`)할 때까지 대기(Drain)하거나, 마지막 탈출 고루틴의 `defer`에서 롤백을 안전하게 인계받아 수행(`rollbackOnDrain`).
2. **Double-Rollback 및 Nil Dereference 원천 차단**:
   - `cs.Tx`의 생명주기를 `cs.activeQueries` 수명과 완벽히 동기화.

---

## 3. 세부 구현 단계 (Implementation Phases)

### Phase 1: `ClientSession`에 트랜잭션 인계 롤백 훅(`rollbackOnDrain`) 추가
- `pkg/engine/session.go`:
  - `cs.rollbackOnDrain func()` 필드 추가.
  - `checkAndDrainClosedLocked()`에서 활성 쿼리가 0이 되었을 때 `cs.Tx != nil`이면 안전하게 롤백하고 nil 처리하도록 동기화.

### Phase 2: `CloseSession`에서 In-flight 상태 분기 처리
- `pkg/engine/coordinator_impl.go:571-620`:
  - `inFlight := cs.activeQueries > 0 || cs.sessInUse`를 `cs.Tx.Rollback()` 이전으로 전진 배치.
  - 만약 `inFlight` 상태라면, 즉각적인 `cs.Tx.Rollback()`을 보류하고 `cs.rollbackOnDrain` 콜백으로 위임.
  - 쿼리 고루틴이 안전하게 루프를 빠져나와 `activeQueries`를 감소시킬 때 단일 스레드 안전하게 롤백 수행.

### Phase 3: 회귀 테스트 및 가혹 동시성 검증
- `pkg/engine/session_disconnect_rollback_regression_test.go` 확장:
  - 장기 실행 쿼리 도중 의도적으로 `CloseSession`을 호출하는 테스트에서 WiredTiger 패닉 및 nil dereference가 0건임을 입증.
  - `go test -v -race ./pkg/engine/...` 100% PASS 확인.

---

## 4. 기대 효과
- 네트워크 비정상 단선 시 엔진 프로세스 급작 크래시 원천 차단.
- WiredTiger C 커널과 Go 런타임 간의 메모리 안전성(Memory Safety) 확보.
