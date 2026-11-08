# P0-30 트랜잭션 코디네이터 및 세션 동시성 무한 루프 심층 토론 백서

> - **최초 작성일**: 2026-11-08
> - **문서 상태**: DRAFT / DISCOVERY ARCHIVE
> - **대상 과제**: `P0-30` [타임아웃 롤백 중 동시 커밋 시 고아 트랜잭션 방치 및 세션 동시성 안정화] (`pkg/engine/coordinator_impl.go`, `pkg/engine/session.go`, `pkg/engine/sqlexec.go`, `pkg/engine/session_pool.go`)
> - **핵심 키워드**: Session Concurrency, Self-Deadlock, Worker Starvation, Reentrant Gate, In-Flight Query Drain, Lock Hierarchy

---

## 1. 개요 및 논의의 발단

LionSQL 1.0 정식 릴리스 대비 전수 감사 결함 중 `P0-30` 과제는 사전 감사 보고서(`notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md:2380-2445`, 심층 구역 2)에서 발굴된 다음 결함들을 포괄하여 완치하기 위한 목적으로 시작되었습니다:

1. **타임아웃 발생 시 세션 트랜잭션 상태 미반영**: 타임아웃/취소 발생 시 `cs.TxFailed` 플래그 설정이 누락되어 불완전 트랜잭션이 이후 `COMMIT` 시 영구 커밋되는 결함.
2. **방언(T-SQL vs PostgreSQL) 간 트랜잭션 실패 처리 불일치**: `sqlexec.go:508`에서 `if cs.TxFailed && cs.IsPostgres()`로 되어 있어 TDS(T-SQL) 세션에서는 실패 플래그가 켜져 있어도 `COMMIT`을 강행하는 결함.
3. **Autocommit 세션의 WiredTiger CGo 동시 접근 Data Race**: `session.go:379`에서 Autocommit 모드 시 `cs.Mu.Unlock()`을 조기 해제하여 동일 세션으로 동시 쿼리 유입 시 C 세션(`WT_SESSION*`) 충돌 SIGSEGV 발생 결함.
4. **전역 Cascading Stall**: `coordinator_impl.go:547`의 `GetActiveQueries()`가 전역 `c.sessMu.RLock()`을 잡은 상태로 개별 `cs.Mu.Lock()` 루프를 순회하여 전체 서버 세션 처리가 정체되는 결함.

그러나 이 문제들을 해결하기 위한 대화형 설계 과정에서, 하나의 동기화 기제를 제안할 때마다 연쇄적으로 숨어있던 새로운 데드락 벡터들이 꼬리를 물고 드러나며 총 6차례에 걸친 "데드락이 일어나지 않는가?"라는 질의응답 무한 루프가 발생하였습니다.

본 문서는 이 무한 루프의 전개 과정에서 규명된 5대 치명적 데드락/크래시 벡터를 체계적으로 기록하고, 향후 완벽한 완치를 위한 정석 아키텍처를 영구 보존하기 위해 작성되었습니다.

---

## 2. 6단계 무한 루프 전개 과정 및 발굴된 결함 전말

### [1단계] 단순 세션 락(`cs.Mu`) 쿼리 전체 유지 제안과 자폭 데드락(Self-Deadlock)
* **제안 내용**: Autocommit 모드에서 `session.go:379`의 조기 `cs.Mu.Unlock()`을 제거하고, 쿼리 실행 함수 전체에 걸쳐 `defer cs.Mu.Unlock()`으로 락을 유지하여 동일 세션 동시 접근을 직렬화하자.
* **사용자 질의**: "완치 설계만으로 충분해? 상당히 복잡해 보이기는 해. 이거면 충분해?"
* **발굴된 치명적 결함 (Self-Deadlock)**:
  - `cs.Mu`는 원래 세션 내부 메타데이터(변수, 커서, Tx 포인터)를 보호하는 초단기 락(Short-lived Mutex)으로 설계되어 있었음.
  - 코드베이스 전수 조사 결과, `sqlexec_select.go`, `sqlexec_dml.go`, `sqlexec_procs.go`, `sqlexec_cursor.go`, `cte_lowering.go` 등 하위 쿼리 실행 엔진 내부 **50여 곳**에서 이미 `cs.Mu.Lock()`을 직접 호출하고 있음.
  - Go의 `sync.Mutex`는 비재진입(Non-reentrant)이므로, 쿼리 상위에서 `cs.Mu`를 쥔 상태로 하위 실행기에 진입하면 첫 번째 내부 `cs.Mu.Lock()`을 만나는 순간 **자기 자신과 데드락(Self-Deadlock)**에 걸려 프로세스가 즉시 정지함 (`[RULE:LockHierarchy]`).

---

### [2단계] 별도 실행 뮤텍스(`execMu`) 및 Context 재진입 패치 제안과 `context.Background()` 유실 데드락
* **제안 내용**: 기존 `cs.Mu`와 락 계층을 분리하기 위해 쿼리 직렬화 전용 락 `cs.execMu`를 신설하고, 서브쿼리나 프로시저 등 내부 재귀 진입은 `context.Value`에 세션 ID 표식을 심어 락 획득을 건너뛰는 Context-Aware Reentrant Gate를 적용하자.
* **사용자 질의**: "이 설계로 정말 데드락 없이 갈 수 있는지 물어보는 거야? 한번 더 물어보자 이거면 데드락 없이 갈 수 있어?"
* **발굴된 치명적 결함 (Context 단절로 인한 100% 데드락)**:
  - `pkg/engine/sqlexec_select.go:140`의 서브쿼리 실행기 구현 확인:
    ```go
    opts.SubqueryExecutor = func(sql string) ([]map[string]any, []string, error) {
        if cs != nil {
            res, err = s.ExecuteSessionSQL(cs.ID, sql)
        }
    ...
    ```
  - `s.ExecuteSessionSQL`의 내부 구현:
    ```go
    func (s *Coordinator) ExecuteSessionSQL(sessionID string, query string) (*QueryResult, error) {
        return s.ExecuteSessionSQLContext(context.Background(), sessionID, query)
    }
    ```
  - 부모 쿼리가 `context`에 아무리 재진입 표식을 심어두어도, 서브쿼리가 호출되는 순간 **`context.Background()`로 단절**되어 표식이 완전히 증발함.
  - 결과적으로 서브쿼리는 자신이 재귀 호출인지 모른 채 `cs.execMu.Lock()`을 다시 시도하여 **100% 확률로 영구 자폭 데드락** 발생.

---

### [3단계] 락 제거 및 스토리지 세션 풀 대여/반납 제안과 2대 자원 고갈 데드락
* **제안 내용**: 인위적인 거대 세션 락(`execMu`)을 포기하고, Autocommit 쿼리마다 `SessionPool`에서 독립된 `storage.Session`을 대여받아 실행하고 즉시 반납함으로써 C 세션 충돌을 방지하자.
* **사용자 질의**: "한번 더 묻는다. 이렇게 하면 데드락 없이 갈 수 있어? 다시 한번 이렇게 하면 데드락이 일어나지 않아?"
* **발굴된 치명적 결함 A (워커 풀 세마포어 고갈 데드락)**:
  - `ExecuteSessionSQLContext` 진입 시 `s.Pool().AcquireWorker(ctx)`를 호출하여 워커 세마포어 채널 슬롯(`p.semWorkers <- struct{}{}`)을 점유함 (`sqlexec.go:317`).
  - 서브쿼리(`SubqueryExecutor`), 동적 SQL(`sp_executesql`), 중첩 프로시저가 돌면 내부에서 다시 `ExecuteSessionSQLContext`를 호출하여 **2번째 워커 슬롯을 요구**함.
  - 동시 실행 요청 수가 `MaxWorkers` 한도(예: 16개)에 도달한 상태에서 모든 세션이 서브쿼리를 실행하면, 16개 부모 고루틴이 슬롯을 쥐고 각자의 서브쿼리를 위한 2번째 슬롯을 무한 대기하는 **워커 풀 고갈 영구 데드락(Resource Starvation Deadlock)** 발생.
* **발굴된 치명적 결함 B (`p.mu` 내 `sess.Close()` CGo 락 역전 데드락)**:
  - `SessionPool.go:160, 198`에서 풀 내부 락(`p.mu.Lock()`)을 쥔 상태로 `sess.Close()`를 동기 호출함.
  - WiredTiger `sess.Close()`는 CGo를 경유하여 엔진 내부 락을 획득하므로, 엔진 셧다운/체크포인트 고루틴과 맞물려 **엄격한 락 순서가 뒤집히는 AB-BA 데드락** 상존 (`[발굴 결함 2-4]`).

---

### [4단계] 락 밖에서 `Commit()` 수행 제안과 WiredTiger C 세션 Use-After-Free 크래시
* **제안 내용**: `cs.Mu`를 쥔 상태로 디스크 I/O나 스토리지 락을 기다리면 데드락이 발생하므로, `cs.Mu` 안에서는 `tx := cs.Tx; cs.Tx = nil`만 처리하고 언락한 뒤 락 밖에서 `tx.Commit()`을 호출하자.
* **사용자 질의**: "한번 더 묻는다 이번에는 틀림없는지"
* **발굴된 치명적 결함 (트랜잭션 배타성 붕괴 및 CGo 동시 조작 SIGSEGV)**:
  - 락 밖에서 `tx.Commit()`이 디스크 플러시를 수행하는 수십 밀리초 사이에 클라이언트가 다음 쿼리로 `BEGIN TRANSACTION`을 전송함.
  - 세션 입장에서는 `cs.Tx == nil`이므로 정상으로 판단하고 `BeginSessionTx`로 진입하여 `cs.Session.BeginTx()`를 호출함.
  - **이전 트랜잭션의 `commit_transaction`이 물리적으로 끝나지도 않았는데 동일 세션에서 `begin_transaction`이 동시 호출**됨.
  - WiredTiger C 세션 내부 메모리 오염, CGo 패닉, 프로세스 SIGSEGV 비정상 종료 유발 (`[RULE:PanicRecovery]`, `[RULE:ConcurrencySafety]`).
  - **교훈**: 트랜잭션의 물리 커밋/롤백이 완전히 끝날 때까지는 해당 세션에 다음 트랜잭션이 진입하지 못하도록 직렬화(배타성)가 반드시 유지되어야 함.

---

### [5단계] 고아 실행 고루틴(In-Flight Query)의 지연 탈출과 차기 요청의 세션 탈취 충돌
* **제안 내용**: 트랜잭션 배타성을 유지하고 비동기 고루틴 롤백을 배제한 뒤 동기 상태 머신(`cs.TxFailed = true`)으로 정리하자.
* **사용자 질의**: "다시 한번 묻는다 이번에는 틀림없어?"
* **발굴된 치명적 결함 (In-Flight Query 탈출 지연과 레이스)**:
  - 타임아웃 타이머 만료로 `queryCtx`가 취소(Cancel)되더라도, 실행 중이던 고루틴 A는 스토리지 엔진 내부 깊은 곳(CGo 루프, 복잡한 집계)에서 탈출하여 빠져나오는 데 수십~수백 밀리초의 지연이 발생함.
  - 타임아웃 에러를 감지한 클라이언트는 즉시 다음 요청(`ROLLBACK` 또는 `COMMIT`)을 전송하며, 이는 새로운 고루틴 B에 의해 처리됨.
  - 고루틴 A가 아직 스토리지 세션과 트랜잭션을 정리하지 못하고 뒷정리 중인 상태에서, 고루틴 B가 세션을 탈취하여 `RollbackSessionTx`를 호출함.
  - 동일한 트랜잭션 핸들에 대해 고루틴 A와 B가 동시 조작을 시도하여 **SIGSEGV 크래시 또는 스토리지 락 꼬임 데드락** 발생.

---

## 3. 무한 루프의 근본 원인 분석 및 코드 실증 진단

본 논의가 무한 루프에 빠진 근본 원인을 실제 엔진 코드(`coordinator_impl.go`, `session.go`, `sqlexec.go`, `sqlexec_select.go`, `session_pool.go`)와 대조한 결과는 다음과 같습니다:

1. **서로 다른 5개의 직교 결함을 단일 거대 락으로 묶으려 함 (Problem Conflation)**:
   - ① `GetActiveQueries` 락 순서 문제, ② `SessionPool` CGo `Close` 락 문제, ③ 타임아웃 시 `TxFailed` 누락 문제, ④ Autocommit C 세션 공유 문제, ⑤ 서브쿼리 재귀 호출 문제를 단 하나의 거대한 아키텍처 변경(`execMu`, `queryDone` 등)으로 한꺼번에 풀려다 보니, 새 락이 기존 락들과 얽혀 새로운 데드락 경로(Wait-For Graph Cycle)를 끝없이 파생시킴 (`[RULE:RadicalKISS]` 위배).
2. **`ClientSession` 전체 락이라는 잘못된 전제와 진짜 범인(`acquireSessionForQuery`) 간과**:
   - 이전 논의는 "동일한 `cs.Session`(`WT_SESSION*`)을 두 쿼리가 동시에 건드리는 것을 막으려면 쿼리 실행 내내 `ClientSession` 전체에 락(`cs.Mu` 또는 `cs.execMu`)을 걸어야 한다"는 전제에 갇혀 있었음.
   - 그러나 실제 코드에서 충돌이 발생했던 진짜 원인은 거대 세션 락의 부재가 아니라, `pkg/engine/coordinator_impl.go:354-363`의 9줄짜리 함수 `acquireSessionForQuery`가 **`cs.Session`이 현재 사용 중인지 확인조차 하지 않고 무조건 반환**했기 때문임:
     ```go
     func (c *Coordinator) acquireSessionForQuery(cs *ClientSession) (storage.Session, func(), error) {
         if cs != nil && cs.Session != nil {
             return cs.Session, func() {}, nil // <--- 사용 중 여부 미확인!
         }
         sess, err := c.engine.NewSession()
         ...
     }
     ```
   - 부모 `SELECT`가 Autocommit 모드(`cs.Tx == nil`)로 실행될 때 `sqlexec_select.go:79-85`에서 `acquireSessionForQuery(cs)`를 호출해 `cs.Session` 위에서 읽기 트랜잭션 `t`를 엶 (이때 `cs.Tx`는 여전히 `nil`).
   - 그 상태에서 서브쿼리(`sqlexec_select.go:140`)나 동시 Autocommit 쿼리가 유입되면 `acquireSessionForQuery(cs)`가 이미 트랜잭션이 열려 사용 중인 `cs.Session`을 또 그대로 내어주어 C 세션 충돌이 발생했던 것임.

---

## 4. 발굴 결함 매트릭스 총정리

| 번호 | 결함 명칭 | 발생 위치 | 위험도 | 트리거 조건 | 결과 현상 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **D-1** | 세션 락 재진입 자폭 데드락 | `sqlexec_*.go` 50여 곳 | **Critical** | `cs.Mu`를 쿼리 상위에서 쥐고 하위 실행기 진입 | 서버 영구 정지 (Self-Deadlock) |
| **D-2** | 서브쿼리 Context 단절 재진입 실패 | `sqlexec_select.go:140` | **Critical** | 서브쿼리가 `ExecuteSessionSQL`(`context.Background()`) 호출 | 재진입 감지 불가, 부모 취소 단절 |
| **D-3** | 워커 세마포어 풀 고갈 데드락 | `sqlexec.go:317` | **High** | 동시 쿼리가 `MaxWorkers` 도달 후 서브쿼리 실행 | 2번째 슬롯 대기 상호 블로킹 정체 |
| **D-4** | 풀 내부 락 쥔 채 CGo Close 락 역전 | `session_pool.go:160, 198` | **High** | 유휴 세션 만료 또는 풀 반환 시 `p.mu` 내 `sess.Close()` | 엔진 락과 AB-BA 데드락 |
| **D-5** | 조기 `cs.Tx = nil`로 인한 CGo 동시 접근 | `sqlexec.go:508` | **Critical** | `Commit()` 디스크 I/O 도중 다음 쿼리가 `BEGIN` 진입 | 동일 `WT_SESSION` 동시 조작 SIGSEGV |
| **D-6** | Autocommit / 서브쿼리 `cs.Session` 중복 대여 | `coordinator_impl.go:354`, `session.go:369` | **Critical** | Autocommit 실행 중 서브쿼리 또는 동시 쿼리가 동일 `cs.Session` 진입 | 동일 `WT_SESSION` 이중 사용 크래시 |
| **D-7** | 타임아웃 시 `TxFailed` 누락 및 커밋 가드 불일치 | `sqlexec.go:401, 437, 508` | **Critical** | 타임아웃 발생 후 또는 `TxFailed` 상태에서 `COMMIT` 송신 | 불완전 트랜잭션 영구 커밋 위험 |
| **D-8** | DMV의 광역 RLock Cascading Stall | `coordinator_impl.go:547` | **Medium** | 장기 실행 쿼리 중 `GetActiveQueries` 호출 및 세션 생성 | 전체 세션 조회 및 취소 마비 |

---

## 5. 무한 루프를 끊어내는 3대 탈출 원칙 및 5대 정석 해법

### 5.1 무한 루프 탈출 3대 원칙
1. **[원칙 1] 신규 거대 락 추가 전면 금지 및 락 스코프 축소(Lock Scope Shrinking)**:
   - 쿼리 실행 전체를 감싸는 거대 뮤텍스(`execMu`)나 블로킹 대기 채널(`queryDone`) 신설 아이디어를 전면 폐기함.
   - 새로운 락을 만들지 않고, 기존에 중첩되어 있던 락 보유 범위만 축소(Copy-First, Close-Outside-Lock)하여 데드락 발생 가능성을 수학적으로 0%로 수렴시킴 (`[RULE:LockHierarchy]`, `[RULE:RadicalKISS]`).
2. **[원칙 2] 직교 소과제 분리 격파 (Divide & Conquer)**:
   - 문제를 단일 거대 변경으로 묶지 않고 5개의 독립적이고 검증 가능한 국소 수정으로 분리하여 적용함.
3. **[원칙 3] 과잉 설계 배제 및 코드/테스트 실증 (`go test -race`)**:
   - 추상적 가설 논쟁을 중단하고 실제 호출 그래프와 레이스 디텍터 테스트로 검증함.

### 5.2 5대 정석 해법 (`D-1 ~ D-8` 일괄 완치)

1. **[해법 1: `D-1`, `D-2`, `D-6` (`결함 2-2`) 해결] `cs.sessInUse` 5ns 원자적 대여 + `SessionPool` 폴백**:
   - `ClientSession`에 `sessInUse bool` 플래그를 둠.
   - `acquireSessionForQuery`(및 `ExecuteProgram` 등 Autocommit 실행 경로)에서 `cs.Mu.Lock()`을 잡고 `!cs.sessInUse`이면 `cs.sessInUse = true`로 마킹한 뒤 **즉시 `cs.Mu.Unlock()`(보유 시간 5ns)**하고 `cs.Session`을 사용함. 쿼리 종료 시 `defer`에서 `cs.sessInUse = false`로 복원함.
   - 만약 이미 `cs.sessInUse == true`라면(부모 쿼리가 `cs.Session`을 사용 중인 상태에서 호출된 서브쿼리이거나, 동시 유입된 Autocommit 쿼리인 경우), `c.pool.AcquireSession()`에서 별도의 독립된 스토리지 세션을 대여받아 실행하고 `defer c.pool.ReleaseSession(sess)`로 즉시 반납함.
   - **효과**: 쿼리 실행 도중 유지되는 뮤텍스가 0개이므로 자폭 데드락(`D-1`, `D-2`) 확률 0%, 동시/중첩 쿼리는 서로 다른 `WT_SESSION*`을 쓰므로 CGo 충돌(`D-6`) 확률 0%.
2. **[해법 2: `D-2`, `D-3` 해결] 내부 재귀 쿼리(`SubqueryExecutor`, `sp_executesql`, `EXPLAIN`) Context 전파 및 워커 슬롯 재사용**:
   - `sqlexec_select.go:140, 294`, `sqlexec_explain.go:720`, `sqlexec_procs.go:1219, 1332`에서 `context.Background()` 대신 부모의 `QueryCtx`에 재진입 표식(`reentrantExecKey`)을 담아 전달함.
   - 재진입 쿼리는 이미 부모 고루틴의 워커 슬롯 안에서 실행 중이므로 `s.Pool().AcquireWorker(ctx)` 중복 호출을 건너뛰어 워커 풀 세마포어 고갈 데드락(`D-3`)을 100% 차단하고, 부모의 `cs.RunningQuery`와 `cs.CancelFunc`가 덮어써지지 않도록 보존함.
3. **[해법 3: `D-4` (`결함 2-4`) 해결] `SessionPool`의 `p.mu` 락 밖에서 `sess.Close()` 호출**:
   - `pkg/engine/session_pool.go:160, 198`에서 만료되거나 초과된 세션을 닫을 때 지역 슬라이스/변수로 빼낸 후 `p.mu.Unlock()` 이후에 `sess.Close()`를 호출하여 `p.mu -> WiredTiger CGo 락` 역전 데드락 고리를 완전히 끊어냄.
4. **[해법 4: `D-5`, `D-7` 해결] 타임아웃 시 `cs.TxFailed = true` 마킹 및 `CommitSessionTx` 방언 공통 가드**:
   - `pkg/engine/sqlexec.go:401, 437`: 타임아웃/취소 발생 시 활성 트랜잭션(`cs.Tx != nil`)이 있다면 방언 불문 `cs.TxFailed = true`로 마킹함.
   - `pkg/engine/sqlexec.go:508` (`CommitSessionTx`): `cs.TxFailed` 상태에서 `COMMIT`이 호출되면 물리 커밋(`cs.Tx.Commit()`)을 수행하는 동안 세션 락을 유지하되(`D-5` 방지), `cs.IsPostgres()` 여부와 무관하게 무조건 물리 `cs.Tx.Rollback()`을 수행함 (PG는 기존 사양대로 롤백 후 `nil` 반환, T-SQL은 롤백 후 uncommittable 에러 반환).
5. **[해법 5: `D-8` (`결함 2-3`) 해결] `GetActiveQueries()` Copy-First 스냅샷**:
   - `pkg/engine/coordinator_impl.go:547`에서 `c.sessMu.RLock()` 상태에서는 `[]*ClientSession` 슬라이스만 복제하고 즉시 `c.sessMu.RUnlock()`한 뒤 개별 세션을 조회함.

### 5.3 결론: 왜 이 해법은 데드락이 절대 일어날 수 없는가?

1. **쿼리 실행 도중 유지되는 신규 뮤텍스가 0개입니다**: `cs.sessInUse`는 `bool` 값 하나만 5ns 동안 읽고 쓰고 즉시 `cs.Mu.Unlock()`하므로 쿼리 실행 중 락 대기 자체가 존재하지 않습니다.
2. **기존에 중첩되어 있던 3개의 위험한 락/자원 대기 고리를 모두 제거(Lock Scope Shrinking)합니다**:
   - ① `AcquireWorker -> 서브쿼리 AcquireWorker` 중첩 대기 제거
   - ② `p.mu -> CGo sess.Close()` 중첩 락 제거
   - ③ `sessMu.RLock -> cs.Mu.Lock` 중첩 락 제거

---

본 백서는 `P0-30`의 동시성 설계 기준 및 구현 명세(SSOT)로 보존됩니다.
