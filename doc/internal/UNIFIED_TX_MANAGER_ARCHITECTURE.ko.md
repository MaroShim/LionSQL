# LionSQL 통합 트랜잭션 매니저 (Unified TxManager: DDL + DML) 아키텍처 설계서
`doc/internal/UNIFIED_TX_MANAGER_ARCHITECTURE.ko.md`

- **문서 식별자**: `ARCH-UNIFIED-TX-MANAGER-1`
- **상태**: 공식 코어 엔진 아키텍처 규격서 (Core Engine Architecture Specification SSOT)
- **최초 작성일**: 2026-11-16
- **최종 갱신일**: 2026-11-17
- **관련 황금률**: `[RULE:PlanPersistence]`, `[RULE:RadicalKISS]`, `[RULE:ZeroAllocation]`, `[RULE:AtomicSaves]`
- **아키텍처 불변성**: `GEMINI.md` Invariant 3 (Non-Blocking Read Committed & Snapshot Isolation), Invariant 6 (Benchmark & Transaction Modeling Integrity)

---

## 1. LionSQL 4대 코어 컴포넌트 아키텍처 (The Four Pillars)

LionSQL은 외부 데몬 의존성이 전무한 **단일 정적 독립 실행형(Zero-Sidecar) OLTP RDBMS**로서, 다음 4대 핵심 컴포넌트가 결합하여 완전한 엔터프라이즈 ACID와 서브-마이크로초 대화형 처리 성능을 달성합니다:

```
┌───────────────────────────────────────────────────────────────────────────┐
│                     LionSQL Autonomous Database Kernel                    │
├─────────────────────────────────────┬─────────────────────────────────────┤
│ 1. 2-Tier 파싱 엔진 (Parser Tier)    │ 2. VDBE & CBO 실행 엔진 (Execution)  │
│ - Pratt Recursive-Descent Fast-Path │ - SQLite 스타일 VDBE 바이트코드 VM  │
│ - ANTLR4 Full T-SQL/PG Fallback     │ - Volcano Iterator 모델 & 해시 조인 │
│ ➔ "서브-마이크로초 문법 완전 정복"  │ ➔ "sync.Pool 제로 할당 초고속 실행" │
├─────────────────────────────────────┼─────────────────────────────────────┤
│ 3. 통합 TxManager (Orchestrator)     │ 4. ARIES & 스토리지 (Storage & WAL) │
│ - 4대 트랜잭션 경계(명시/묵시/에러/단절) │ - ARIES Physiological Undo Logging │
│ - DDL 섀도우 카탈로그 롤백 오케스트레이션│ - In-Memory OCC & WiredTiger MVCC   │
│ ➔ "세션 트랜잭션 무결성의 단일 두뇌" │ ➔ "1바이트 오차 없는 물리 복구 손발"│
└─────────────────────────────────────┴─────────────────────────────────────┘
```

1. **2-Tier 파싱 엔진 (감각 기관)**: 수십 종의 프레임워크(Gitea, Casdoor, Grafana, Answer 등)가 생성하는 방대한 T-SQL/Postgres 방언을 서브-마이크로초 속도로 해석하는 관문.
2. **VDBE & CBO 실행 엔진 (심장)**: 레지스터 재활용(`sync.Pool`)과 제로 힙 할당으로 쿼리를 초고속 가상 머신 상에서 인터랙티브하게 태워내는 실행기.
3. **통합 TxManager (두뇌)**: 세션별 트랜잭션의 시작, 오류 격리, 세이브포인트 부분 롤백, DDL+DML 원자적 2단계 커밋을 한 치의 누수 없이 통제하는 총괄 지휘관.
4. **ARIES & 스토리지 엔진 (근육)**: 테이블 레코드와 B-Tree 인덱스를 Before/After 이미지(`PhysiologicalRecord`)로 물리적 역재생(Undo)하여 장애 복구와 세이브포인트 롤백을 오차 없이 완결하는 토대.

---

## 2. 추진 배경 및 당위성 (Why Unified TxManager)

### 2.1 현행 트랜잭션 처리 구조의 한계
현재 LionSQL의 트랜잭션 처리는 Wire 프로토콜 계층(`pgwire`, `tds`), 세션 관리 계층(`pkg/engine/coordinator.go`), 그리고 하부 스토리지 엔진(`pkg/storage/memengine`, `wiredtiger`) 사이에 다소 직결된 형태로 동작하고 있습니다.
1. **상태 관리의 파편화**: `inTx`, `autocommit`, `isolationLevel` 등의 제어 플래그가 프로토콜 핸들러와 코디네이터에 분산되어 있음.
2. **Savepoint 계층 구조 부재**: T-SQL 및 PostgreSQL 표준의 `SAVEPOINT sp1` ➔ `ROLLBACK TO sp1`과 같은 부분 롤백(Partial Rollback) 체인이 독립 모듈로 추상화되어 있지 않음.
3. **DDL과 DML의 원자적 복합 트랜잭션 미지원**:
   - PostgreSQL 엔터프라이즈 워크로드에서는 단일 트랜잭션 내에서 DDL로 테이블을 생성/변경하고 즉시 DML을 수행한 후, 오류 시 통째로 롤백하는 패턴(`BEGIN; CREATE TABLE ...; INSERT INTO ...; ROLLBACK;`)이 빈번함.
   - 카탈로그 변경(DDL)과 튜플 변경(DML)의 생명주기를 한곳에서 원자적으로 총괄하는 조정자(Coordinator)가 필요함.

### 2.2 추진 방향: DDL + DML 통합 트랜잭션 관리자
DML 전용 TX와 DDL 전용 TX를 개별 모듈로 격리하지 않고, **단일 프로세스 내의 통합 트랜잭션 관리자(`pkg/tx`)**를 신설하여 두 영역을 단일 생명주기 아래에서 일관되게 제어합니다.

---

## 3. 핵심 아키텍처 설계 (`pkg/tx`)

### 3.1 디렉토리 및 패키지 구조
```
pkg/tx/
├── manager.go          // 글로벌 트랜잭션 매니저 (TxCoordinator): Begin, Commit, Rollback 총괄
├── context.go          // TxContext: XID, IsolationLevel, ReadTS, CommitTS, State 머신
├── savepoint.go        // SavepointStack: 계층형 LSN/오프셋 스택 및 부분 롤백 관리
├── dml_workspace.go    // DML Private Workspace & Write-Set 버퍼 관리
├── ddl_shadow.go       // DDL Shadow Catalog Snapshot 및 메타데이터 롤백 연계
├── visibility.go       // Read Committed vs Snapshot Isolation MVCC 가시성 필터
└── tx_test.go          // 단위 테스트 및 동시성 검증
```

### 3.2 트랜잭션 상태 머신 (TxState Lifecycle)
```
          [Begin]
             │
             ▼
        ┌─────────┐
        │ Active  │ ◄─────── DML/DDL Statements (Write-Set & Shadow Catalog Accumulation)
        └────┬────┘           └─► SAVEPOINT sp1 (Push Snapshot Marker)
             │                └─► ROLLBACK TO sp1 (Truncate to Marker)
      ┌──────┴──────┐
      ▼             ▼
  [Commit]      [Rollback]
      │             │
      ▼             ▼
┌───────────┐ ┌───────────┐
│ Committed │ │  Aborted  │
└───────────┘ └───────────┘
```

1. **Active**: 트랜잭션 활성 상태. DML은 트랜잭션 격리 워크스페이스(Private Workspace)에, DDL은 섀도우 카탈로그(Shadow Catalog)에 변경 사항을 누적.
2. **Prepared / Validating**: Commit 요청 시 OCC 검증(충돌 검사) 및 제약 조건(Unique, Foreign Key) 최종 유효성 검사.
3. **Committed**: 
   - Phase 1: 스토리지 엔진에 Write-Set 원자적 플러시 및 WAL 기록.
   - Phase 2: 섀도우 카탈로그를 시스템 카탈로그로 원자적 포인터 스왑.
4. **Aborted / RolledBack**:
   - Write-Set 즉시 폐기(Zero-Vacuum 메모리 해제).
   - 섀도우 카탈로그 폐기 및 활성 락 해제.

### 3.3 TxManager가 책임지는 4대 핵심 트랜잭션 경계 (Transaction Boundaries)

`ClientSession`은 오직 연결의 신원(Session ID, 인증 사용자, 소켓 상태)만 관리하며, 트랜잭션의 시작과 끝을 포함한 모든 경계(Boundary) 관리는 `TxManager`가 단일 책임(SRP)으로 전담합니다:

1. **명시적 트랜잭션 경계 (`BEGIN` ~ `COMMIT` / `ROLLBACK`)**:
   - 클라이언트의 `BEGIN` 요청 시 `TxManager.Begin(sessID)`가 `TxContext`를 발급하고 세션을 `InTransaction` 상태로 전환.
   - `COMMIT` 또는 `ROLLBACK` 시 DDL 섀도우 카탈로그와 ARIES 물리 언두 로그를 원자적으로 완결한 후 세션을 다시 Idle(`'I'`) 상태로 복귀.
2. **묵시적(Autocommit) 트랜잭션 경계**:
   - `autocommit = true` 상태에서 단일 DML/DDL 문장이 유입되면 문장 시작 직전 초경량 트랜잭션 컨텍스트를 스택에서 대여(`Begin`)하고, 문장 완료 즉시 `Commit` (에러 시 즉시 `Rollback`)하여 단일 문장 단위의 완벽한 ACID 원자성을 보장.
3. **오류 격리 경계 (Error & Aborted State Boundaries)**:
   - PostgreSQL 표준에 따라 트랜잭션 도중 문장 에러 발생 시 트랜잭션을 오류 상태(`Aborted`, Wire 상태 토큰 `'E'`)로 전이시키고, 사용자가 명시적 `ROLLBACK`을 호출하기 전까지 이후 모든 쿼리 실행을 안전하게 차단.
4. **세션 단절 안전 경계 (Session Teardown & Fail-Safe Boundary)**:
   - 클라이언트 소켓 강제 종료(`disconnect`) 또는 프로세스 사망 시 `TxManager.AbortSession(sessID)`가 미커밋 트랜잭션을 즉시 감지하여 ARIES 언두 로그를 가동하고 원자적 롤백을 수행, 고아 트랜잭션(Orphan Transaction) 및 좀비 락을 원천 차단.

> **하부 ARIES 엔진과의 협업 관계**:
> 상위 `TxManager`가 논리적 트랜잭션 경계와 상태를 통제하면, 하부 **ARIES 물리 언두 로깅(`pkg/storage/aries_log.go`, `PhysiologicalRecord`)**은 `TxManager`가 하달하는 세이브포인트 부분 롤백(`RollbackTo`) 및 세션 단절 강제 롤백을 스토리지 B-Tree 레벨에서 Before/After 이미지 역재생(Reverse Undo)을 통해 1바이트의 오차도 없이 물리적으로 원상 복구하는 완벽한 실행 손발(Muscle) 역할을 수행합니다.

---

## 4. DML TX 모듈 상세 명세

### 4.1 MVCC 가시성 제어 (Visibility Manager)
- **XID & Timestamp 발행**:
  - `uint64` 단조 증가 글로벌 트랜잭션 카운터(`atomic.Uint64`).
- **Read Committed**:
  - 매 문장(Statement) 시작 직전 최신 Commit Timestamp를 스냅샷 타임스탬프로 재획득.
- **Snapshot Isolation (Repeatable Read)**:
  - 트랜잭션 시작(`BeginTx`) 시점의 Timestamp를 트랜잭션 종료 시까지 고정 유지.
  - 트랜잭션 외부에서 커밋된 최신 데이터라도 시작 시점 이후의 튜플은 마스킹.

### 4.2 계층형 세이브포인트 (Savepoint Stack)
- 각 Savepoint는 다음 정보를 캡처:
  - Savepoint 명칭 (`string`)
  - DML Write-Set 오프셋 (현재 버퍼링된 레코드 개수)
  - 섀도우 카탈로그 리비전 번호
- `ROLLBACK TO <savepoint>` 호출 시:
  - 지정된 오프셋 이후에 누적된 DML Write-Set을 잘라냄(Slice Truncate).
  - 섀도우 카탈로그를 해당 리비전으로 롤백.
  - 하위 세이브포인트들은 자동 무효화.

### 4.3 OCC 충돌 감지 및 정책화 (Conflict Policy)
- In-Memory OCC 엔진의 `ErrWriteConflict` 발생 시:
  - `PolicyFailFast` (기본값): 즉시 사용자에게 직렬화 실패 에러 반환 (PostgreSQL 40001 호환).
  - `PolicyAutoRetry`: 단순 단일 문장 트랜잭션의 경우 최대 $N$회 지수 백오프 기반 무중단 자동 재시도.

---

## 5. DDL TX 모듈과의 통합 인터페이스

### 5.1 Copy-on-Write 섀도우 카탈로그 스왑
```go
type TxCoordinator interface {
    Begin(ctx context.Context, opts TxOptions) (*TxContext, error)
    Commit(tx *TxContext) error
    Rollback(tx *TxContext) error
    Savepoint(tx *TxContext, name string) error
    RollbackTo(tx *TxContext, name string) error
}

type TxContext struct {
    ID             uint64
    IsolationLevel IsolationLevel
    ReadTS         uint64
    CommitTS       uint64
    State          TxState
    
    // DML Workspace
    Workspace      storage.Workspace
    
    // DDL Shadow Catalog (COW)
    ShadowCatalog  catalog.ShadowCatalog
    
    // Savepoints
    Savepoints     []Savepoint
}
```

### 5.2 원자성 보장 (2-Phase Commit in Single Process)
1. DML 작업은 `tx.Workspace`에 격리되어 타 세션에 노출되지 않음.
2. DDL 작업은 `tx.ShadowCatalog`에 격리되어 타 세션의 카탈로그 조회에 영향을 주지 않음.
3. 커밋 시점에만 글로벌 카탈로그 락(짧은 뮤텍스 획득) 하에서:
   - 스토리지 WAL 커밋 레코드 기록
   - `GlobalCatalog.Swap(tx.ShadowCatalog)`
   - 변경된 테이블 튜플 가시화

---

## 6. 비기능적 요구사항 및 불변성 준수

1. **`[RULE:RadicalKISS]` (단일 프로세스 원칙)**:
   - 무거운 분산 2PC(Spec-Out)나 데드락 감지 분산 그래프를 배제하고, 인메모리 슬라이스 및 포인터 교체 수준으로 극도의 단순성 유지.
2. **`[RULE:ZeroAllocation]` (Hot-Path 무할당 최적화)**:
   - Autocommit 단일 쿼리의 경우 `sync.Pool`로부터 `TxContext`를 대여/반납하여 힙 할당 0을 달성.
3. **`[RULE:StyleIntegrity]`**:
   - 모든 주석, 로그, 문서에 장식용 이모지 일체 배제.
