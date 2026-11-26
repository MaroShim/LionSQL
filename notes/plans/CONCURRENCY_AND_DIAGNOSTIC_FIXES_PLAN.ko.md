# 동시성 트랜잭션 Write-Write 충돌 격리 및 진단 결함 종합 해결 계획 (`CONCURRENCY_AND_DIAGNOSTIC_FIXES_PLAN.ko.md`)

## 1. 개요 및 배경

본 문서는 최근 지적된 동시성 트랜잭션 Write-Write 충돌(Last-Write-Wins 현상) 및 4대 엔진 진단 결함(계산된 열 NULL, 커서 중복 오픈, 트리거 내부 숨김 컬럼 노출, DDL 확장성)을 체계적으로 해결하기 위한 기술 사양서 및 실행 계획이다.

특히, 동시성 트랜잭션 처리에서 **성능(초당 15만 TPS 제로 오버헤드)과 엔터프라이즈 정합성(순차 대기 보장)**을 동시에 달성하기 위해 2단계 로드맵을 확립한다:
- **Step 1 (기본 모드)**: Compact 및 WiredTiger 엔진 공통으로, 미커밋 행 동시 수정 시 즉시 `storage.ErrSerializationFailure` (SQLSTATE `40001` / MSSQL Error `3960`)로 거부하여 Last-Write-Wins(데이터 오염)를 0초 만에 Fail-Closed로 차단.
- **Step 2 (비관적 잠금 모드: `WITH (UPDLOCK)`)**: `WITH (UPDLOCK)`, `WITH (XLOCK)`, `FOR UPDATE` 힌트가 명시된 쿼리에 한해 표준 2PL 블로킹 행 잠금 대기(Lock-Wait) 및 타임아웃/데드락 방지 큐를 동작시킴.

---

## 2. 해결 과제 목록 및 심각도 분류

| ID | 우선순위 | 도메인 | 마일스톤 | 과제명 | 핵심 증상 및 목표 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `FIX-TX-WRITE-WRITE-CONFLICT-ISOLATION-1` | **P1 (Critical)** | Storage | v0.94.0 | 동시 트랜잭션 Write-Write 충돌 격리 (Last-Write-Wins 차단) | 미커밋 상태 행을 다른 세션이 덮어쓰지 못하도록 쓰기 시점 즉시 `ErrSerializationFailure` Fail-Closed 차단 (WT/Compact 엔진 동작 1:1 일치, 완료) |
| `FIX-COMPUTED-COLUMN-SELECT-EVAL-1` | **P1 (High)** | Engine | v0.94.0 | 계산된 열(Computed Column) SELECT 시 동적 수식 평가 | `create table #t (qty INT, price INT, total AS (qty * price));` 조회 시 `NULL` 대신 행 컨텍스트 기반 수식 동적 계산 |
| `FIX-CURSOR-DECLARE-OPEN-LIFECYCLE-1` | **P1 (High)** | Engine | v0.94.0 | 커서 DECLARE 및 OPEN 생명주기 분리 | `DECLARE cur CURSOR` 시점에는 선언만 유지하고 `OPEN cur` 시점에 쿼리를 실행하여 `already open` 에러 방지 |
| `FIX-TRIGGER-INSERTED-HIDDEN-COL-FILTER-1` | **P1 (High)** | Engine | v0.94.0 | 트리거 `INSERTED`/`DELETED` 숨김 컬럼(`_row_id`) 필터링 | 트리거 내에서 `INSERTED` 테이블 컬럼 수 조회 시 내부 관리용 숨김 컬럼이 노출되어 컬럼 수가 불일치하는 결함 수정 |
| `FEAT-ROW-LOCK-WAIT-MANAGER-1` | **P3** | Storage | Post-1.0 (v1.1.0) | `WITH (UPDLOCK)` 명시 힌트 기반 2PL 행 잠금 대기(Lock-Wait) 및 데드락 감지 매니저 | 1.0 이후 연기: WiredTiger C MVCC(`WT_ROLLBACK`)와의 물리적 불일치 및 In-Memory OCC 락프리 정체성 보호 (Step 2 상세 분석 참조) |
| `FEAT-ALTER-COLUMN-ONLINE-EXPANSION-1` | **P2** | Storage | v1.1.0 | `ALTER TABLE ALTER COLUMN` 가변길이 컬럼 온라인 확장 | `VARCHAR(100)` -> `VARCHAR(200)` 등 크기 확장을 $O(1)$ 온라인 메타데이터 갱신으로 지원 |
| `FEAT-TSQL-GOTO-LABEL-1` | **P3** | Engine | v1.1.0 | T-SQL `LABEL` 및 `GOTO` 흐름 제어 구문 지원 | 프로시저 내 레이블 점프 지원 |
| `FEAT-TSQL-CREATE-SYNONYM-1` | **P3** | Engine | v1.2.0 | T-SQL `CREATE SYNONYM` 동의어 객체 지원 | 객체 별칭 가상 라우팅 지원 |

---

## 3. 동시성 제어 2단계 아키텍처 상세 설계

```
                 [동시성 쓰기 / 잠금 요청]
                             │
            ┌────────────────┴────────────────┐
            ▼                                 ▼
   [일반 DML / 기본 트랜잭션]        [WITH (UPDLOCK) / FOR UPDATE 힌트]
   (초당 15만 TPS 핫패스 유지)       (엔터프라이즈 순차 대기 비즈니스 로직)
            │                                 │
   [Step 1: Fast-Fail OCC/MVCC]      [Step 2: 2PL RowLockManager 대기]
   - 미커밋 점유 키 발견 시           - Go 채널 기반 대기 (select <-granted)
   - 즉시 ErrSerializationFailure    - LOCK_TIMEOUT (기본 5초 또는 세션 설정)
   - Last-Write-Wins 원천 차단        - 앞 트랜잭션 커밋 시 깨어나 최신 행 갱신
   - WT와 Compact 엔진 동작 1:1 일치   - 데드락 그래프 검사 및 자동 희생자 선정
```

### 3.1 Step 1 (완료): 기본 Fast-Fail 직렬화 충돌 격리
1. **샤드 레벨 활성 쓰기 트랜잭션 추적기 (`activeWriters`)**:
   - `Compact` 및 임시 테이블 엔진(`tempEng`)의 각 샤드에 `activeWriters: map[key]txID`를 도입.
   - `InsertRow`, `UpdateRow`, `Delete` 진입 시 다른 활성 트랜잭션이 선점한 키이면 **즉시 `storage.ErrSerializationFailure`를 반환**하여 Fail-Closed 차단.
   - `WiredTiger` 디스크 B+Tree 엔진의 `WT_ROLLBACK (-31800)` 거동과 100% 동일하게 일치.
2. **트랜잭션 생명주기 일괄 해제**:
   - `Commit()` 또는 `Rollback()` 호출 시 자신이 획득한 키들을 `releaseAcquiredLocksLocked()`로 원자적 반환.
3. **Autocommit 안전 가드**:
   - 미커밋 락 충돌 시 단일 문장(Autocommit)이 무차별 재시도로 선행 세션의 커밋 직후 데이터를 덮어쓰는 Lost Update 방지.

### 3.2 Step 2 (Post-1.0 / v1.1.0으로 연기): `WITH (UPDLOCK)` 2PL 블로킹 대기 검토 및 아키텍처 한계 분석

> **[2026-11-26 1.0 안정화 판정: 1.0 이후(Post-1.0)로 연기 확정]**
> `FEAT-ROW-LOCK-WAIT-MANAGER-1`은 T-SQL 편의성 관점에서 기획되었으나, 다음과 같은 4가지 핵심 아키텍처 우려점으로 인해 1.0 코어 안정성을 위해 1.0 이후로 연기하여 재검토한다.
>
> 1. **WiredTiger(WT)와의 물리적 불일치 (Dual-Engine Invariance 위반)**:
>    - WiredTiger는 C 커널 수준에서 MVCC + 낙관적 롤백 아키텍처를 채택하여, 동일 키 동시 충돌 시 `WT_ROLLBACK (-31800)`을 즉시 발생시킵니다.
>    - WT 자체에 비관적 "대기열(Wait Queue)" API가 존재하지 않으므로, 상위 Go 레이어에서 대기 루프를 두더라도 대기가 풀린 후 WT C 커널에 쓰기를 시도하는 순간 스냅샷 타임스탬프 불일치로 인해 결국 `WT_ROLLBACK`이 발생하여 대기 효과가 무력화됩니다.
>    - OCC만 대기하고 WT는 즉시 롤백되는 스토리지 엔진별 동작 파편화가 발생합니다.
> 2. **In-Memory OCC 초고속 처리량 정체성 훼손**:
>    - In-Memory OCC 엔진의 핵심 강점은 읽기/쓰기 상호 비차단 및 원자적 버전 번호 기반의 Lock-Free 초고속 처리량(150,000+ TPS)입니다.
>    - 블로킹 대기 도입 시 비관적 2PL로 퇴행하여 락 경합, 컨텍스트 스위칭, 워커 풀 기아(Starvation)가 발생합니다.
> 3. **고루틴 누출(Orphan Waiters) 및 데드락 오버헤드**:
>    - 클라이언트가 트랜잭션을 미완료한 채 소켓을 끊거나 네트워크 타임아웃 발생 시 대기 중인 워커 고루틴이 영구 고립될 위험이 있습니다.
>    - 상호 대기 탐색을 위한 Wait-For Graph 및 전역 락이 엔진 핫패스의 새로운 병목이 됩니다.
> 4. **1.0 정책**:
>    - 1.0에서는 `WITH (UPDLOCK)` 구문을 파서/플래너에서 친절히 수용(Tolerant Accept)하되, 트랜잭션 충돌 시 즉시 Fail-Closed 롤백 후 클라이언트/드라이버의 지터 백오프 재시도를 유도하는 현대 분산/고성능 OLTP 표준을 유지합니다.

1. **의도와 철학 (`[RULE:RadicalKISS]`, `[RULE:LatencyBudget]`)**:
   - 평소 모든 쿼리에 무거운 잠금 관리자를 거치게 하면 14만~15만 TPS를 유지하는 고동시성 엔진 성능이 심각하게 훼손됨.
   - 금융/재고 등 순차 대기가 반드시 필요한 쿼리에 대해 개발자가 명시적으로 `SELECT ... WITH (UPDLOCK, ROWLOCK)` 또는 `SELECT ... FOR UPDATE` 힌트를 지정했을 때만 2PL Lock-Wait 경로를 활성화.
2. **구현 사양**:
   - `RowLockManager`:
     ```go
     type RowLock struct {
         mu      sync.Mutex
         ownerTx uint64
         waiting []chan struct{}
     }
     ```
   - 힌트 파싱: `WITH (UPDLOCK)`, `WITH (XLOCK)`, `WITH (ROWLOCK)`, `FOR UPDATE`를 파서 및 플래너가 감지하여 `ScanOptions.LockMode = LockUpdate`로 전파.
   - 락 획득 시 이미 다른 활성 트랜잭션이 점유 중이면:
     - `select { case <-granted: ... case <-ctx.Done(): ... case <-time.After(lockTimeout): ... }`로 대기.
     - 타임아웃 초과 시 SQL Server 표준 `Error 1222: Lock request time-out period exceeded` 반환.
   - 선행 트랜잭션이 커밋하면: 대기하던 세션이 깨어나서 최신 커밋된 행 데이터를 다시 읽고(Re-read / EPQ) 갱신 진행.

---

## 4. 진단 결함 3종 해결 사양 (P1 진행 중)

### 4.1 `FIX-COMPUTED-COLUMN-SELECT-EVAL-1` (계산된 열 수식 동적 평가)
- **원인**: `CREATE TABLE` 시 수식 메타데이터는 저장되지만, SELECT 프로젝션 및 스캔 포맷팅에서 해당 컬럼 값이 물리 레코드에 없어 `NULL`로 출력됨.
- **해결**:
  - `storage.Column`에 `IsComputed bool`, `ComputedExpr string` 명시.
  - `execution.FormatRowWithArena` 및 `FormatRecord`에서 스키마에 `HasComputedExpr()` 컬럼이 존재할 경우, 현재 행의 다른 컬럼 값들을 컨텍스트로 `execution.EvalTupleExpr`를 동적 평가하여 결과 슬라이스에 즉시 바인딩.

### 4.2 `FIX-CURSOR-DECLARE-OPEN-LIFECYCLE-1` (커서 생명주기 분리)
- **원인**: `DECLARE cur CURSOR` 시점에 쿼리를 즉시 실행하여 열린 상태로 만들어, 뒤이은 `OPEN cur`에서 `already open` 에러 발생.
- **해결**:
  - `DECLARE` 시점: 선언만 세션에 `CursorClosed`로 등록.
  - `OPEN` 시점: 쿼리를 실행하여 결과 셋을 열고 `CursorOpen`으로 전이.

### 4.3 `FIX-TRIGGER-INSERTED-HIDDEN-COL-FILTER-1` (트리거 숨김 컬럼 필터링)
- **원인**: 엔진 내부 관리용 숨김 컬럼(`_row_id` 등)이 트리거의 `INSERTED` 가상 테이블 스키마에 노출되어 컬럼 수 불일치 발생.
- **해결**:
  - 가상 테이블 생성 시 `col.Hidden == true` 또는 `strings.HasPrefix(col.Name, "_")` 컬럼 엄격히 제외.
