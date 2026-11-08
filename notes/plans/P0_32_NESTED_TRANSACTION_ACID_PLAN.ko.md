# LionSQL P0-32: 중첩 트랜잭션 조기 물리 커밋 방지 및 ACID 원자성 수호 아키텍처 (`notes/P0_32_NESTED_TRANSACTION_ACID_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **도메인**: 트랜잭션 매니저(Transaction Manager), T-SQL 중첩 트랜잭션(Nested Transactions), ACID 원자성(Atomicity), `@@TRANCOUNT`
> - **참조 불변식**: `[Invariant 6]` (Benchmark & Transaction Modeling Integrity, ACID Atomicity)
> - **참조 규칙**: `[RULE:AtomicSaves]`, `[RULE:ExplicitError]`
> - **관련 과제**: `P0-32` (`pkg/engine/sqlexec.go`, `pkg/engine/coordinator_impl.go`, `pkg/proc/runtime/vm.go`)

---

## 1. 개요 (The Nested Transaction Illusion)

Microsoft SQL Server T-SQL 생태계에서 다중 계층의 저장 프로시저(Stored Procedure)가 서로를 호출할 때, 각 프로시저 내부에서 독자적으로 `BEGIN TRANSACTION`과 `COMMIT TRANSACTION`을 선언하는 패턴은 매우 흔합니다.

이때 Microsoft SQL Server의 T-SQL 트랜잭션 의미론(Semantics)은 다음과 같이 엄격하게 정의되어 있습니다:
1. **내부 `COMMIT`은 물리적 커밋이 아니다**: 내부 프로시저의 `COMMIT`은 단순히 `@@TRANCOUNT` 카운터를 1 감소시킬 뿐, 디스크에 변경사항을 물리적으로 확정(Commit)하지 않습니다.
2. **최외곽 `COMMIT`만이 물리적 커밋이다**: `@@TRANCOUNT`가 1에서 0으로 떨어지는 최외곽 커밋 시점에만 단 한 번 물리적 디스크 커밋이 일어납니다.
3. **`ROLLBACK`은 언제나 올-오어-낫싱(All-or-Nothing)이다**: 중첩 깊이가 몇 단계이든 간에 `ROLLBACK TRANSACTION`이 호출되면 전체 물리 트랜잭션이 즉시 통째로 롤백되고 `@@TRANCOUNT`는 즉시 0으로 리셋됩니다.

그러나 기존 LionSQL의 트랜잭션 코디네이터에는 **"내부 `COMMIT`을 만났을 때 물리적 트랜잭션을 디스크에 즉시 커밋해버리는 치명적 결함"**이 존재했습니다.

---

## 2. 결함의 본질과 ACID 원자성 파괴 시나리오

### 2.1. 기존 결함 코드 동작
```sql
-- 실행 시나리오
BEGIN TRANSACTION;  -- 최외곽 트랜잭션 시작 (@@TRANCOUNT = 1, 물리 Tx 시작)
INSERT INTO accounts VALUES (1, 'Alice', 1000);

    -- 하위 프로시저 호출 내부
    BEGIN TRANSACTION;  -- 중첩 트랜잭션 시작 (@@TRANCOUNT = 2)
    INSERT INTO logs VALUES ('transfer initiated');
    COMMIT TRANSACTION; -- [치명적 결함 발생!] @@TRANCOUNT=1로 줄이면서 물리 Tx를 COMMIT해버림!

-- 상위 프로시저로 복귀 후 잔여 로직 실패
INSERT INTO accounts VALUES (2, 'Bob', 2000); -- 에러 발생!
ROLLBACK TRANSACTION; -- [원자성 파괴!] 상위 롤백을 시도했으나, 이미 물리 Tx가 커밋되어 Alice의 입금이 영구 잔류함!
```

### 2.2. 초래되는 재앙
- 비즈니스 트랜잭션의 **원자성(Atomicity)**이 전면 붕괴.
- 은행 송금, 결제 처리 등 복합 금융 비즈니스 로직에서 잔액 차감만 커밋되고 입금은 롤백되는 끔찍한 데이터 정합성 파괴 유발.

---

## 3. 해결책: T-SQL 표준 `@@TRANCOUNT` 동기화 및 단일 물리 커밋 바운더리

우리는 `[Invariant 6]`(ACID Atomicity)을 수호하기 위해 트랜잭션 라이프사이클을 전면 재설계했습니다:

```
[Nested Transaction State Machine]

         BEGIN TRANSACTION
   ┌───────────────────────────┐
   │  Is Tx nil?               │
   │  - YES: BeginTx() (물리 시작), cs.TranCount = 1
   │  - NO:  cs.TranCount++ (가상 중첩, 물리 Tx 유지)
   └───────────────────────────┘
                 │
                 ├── COMMIT TRANSACTION
                 │   ├── cs.TranCount > 1:
                 │   │   └── cs.TranCount-- 만 수행! 물리 커밋 절대 금지! (NO-OP to Engine)
                 │   └── cs.TranCount == 1:
                 │       └── 비로소 tx.Commit() 실행! (물리 디스크 확정), cs.TranCount = 0
                 │
                 └── ROLLBACK TRANSACTION
                     └── cs.TranCount 값과 무관하게 즉시 tx.Rollback()!
                         모든 변경사항 무조건 취소, cs.TranCount = 0 강제 리셋!
```

---

## 4. 핵심 코드 구현 (`pkg/engine/sqlexec.go`)

```go
// CommitSessionTx commits the active transaction on the session adhering to @@TRANCOUNT semantics.
func (s *Coordinator) CommitSessionTx(sessionID string) error {
    cs, err := s.GetOrCreateSession(sessionID)
    if err != nil {
        return err
    }
    cs.Mu.Lock()
    defer cs.Mu.Unlock()

    if cs.Tx == nil {
        return fmt.Errorf("no active transaction on session %s", sessionID)
    }

    if cs.TxFailed {
        _ = cs.Tx.Rollback()
        cs.Tx = nil
        cs.TranCount = 0
        cs.TxFailed = false
        return fmt.Errorf("current transaction is aborted, commands ignored until end of transaction block")
    }

    // T-SQL Semantics: If nested, decrement @@TRANCOUNT and DO NOT physically commit!
    if !cs.IsPostgres() && cs.TranCount > 1 {
        cs.TranCount--
        return nil
    }

    // Outermost commit: Physically commit to disk storage
    err = cs.Tx.Commit()
    cs.Tx = nil
    cs.TranCount = 0
    cs.TxFailed = false
    return err
}

// RollbackSessionTx rolls back the active transaction and resets @@TRANCOUNT to 0 unconditionally.
func (s *Coordinator) RollbackSessionTx(sessionID string) error {
    cs, err := s.GetOrCreateSession(sessionID)
    if err != nil {
        return err
    }
    cs.Mu.Lock()
    defer cs.Mu.Unlock()

    if cs.Tx == nil {
        return nil // No-op
    }

    err = cs.Tx.Rollback()
    cs.Tx = nil
    cs.TranCount = 0
    cs.TxFailed = false
    return err
}
```

---

## 5. TDD 회귀 검증 및 결론

- **중첩 트랜잭션 롤백 테스트**: [`pkg/engine/coordinator_nested_tx_test.go:TestCoordinator_NestedTx_RollbackAfterInnerCommit`](file:///Users/maro/Projects/SQLion/pkg/engine/coordinator_nested_tx_test.go) (PASS)
  - 내부 프로시저에서 `COMMIT`을 호출한 뒤 상위에서 `ROLLBACK`을 실행했을 때, 내부 작업까지 전량 롤백되어 테이블에 0개 행만 남음을 완벽히 입증.
- **VDBE VM 연동 테스트**: [`pkg/proc/runtime/nested_tx_regression_test.go`](file:///Users/maro/Projects/SQLion/pkg/proc/runtime/nested_tx_regression_test.go) (PASS)
  - T-SQL 저장 프로시저 호출 체인 내에서 `@@TRANCOUNT`가 1 ➔ 2 ➔ 1 ➔ 0으로 정확하게 감산되고 물리적 원자성이 수호됨을 확인.

이로써 LionSQL은 Microsoft SQL Server 엔터프라이즈 환경에서 마이그레이션된 복합 다계층 저장 프로시저를 100% 안전하게 실행할 수 있는 ACID 원자성 방패를 완성했습니다.
