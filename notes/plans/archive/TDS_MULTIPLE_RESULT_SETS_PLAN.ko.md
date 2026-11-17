# TDS 다중 결과 집합 (Multiple Result Sets / Sequential NextResult) 스트리밍 반환 아키텍처 계획 (`TDS_MULTIPLE_RESULT_SETS_PLAN.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-11-14
> - **최종 업데이트**: 2026-11-14
> - **대상 릴리즈**: LionSQL 1.0-GA
> - **태스크 ID**: `FEAT-TDS-MULTIPLE-RESULT-SETS-1` (결함 107)
> - **관련 컴포넌트**: `pkg/wire/tds/server.go`, `pkg/engine/coordinator.go`, `pkg/engine/sqlexec.go`, `pkg/proc/runtime/vm.go`
> - **마일스톤**: `1.0-GA` (우선순위 P2)

---

## 1. 개요 및 배경 (Executive Summary)

Microsoft SQL Server TDS(Tabular Data Stream) 프로토콜 및 생태계 표준 클라이언트(ADO.NET `SqlDataReader.NextResult()`, JDBC `Statement.getMoreResults()`, Python `cursor.nextset()`, Go `rows.NextResultSet()`, SSMS 멀티 그리드 뷰)는 단일 SQL 배치 및 저장 프로시저 호출 시 **복수의 결과 집합(Multiple Result Sets)**을 순차적으로 스트리밍 반환하는 기능을 핵심적으로 요구합니다.

### 1.1 현재 LionSQL의 한계 및 문제 현상
1. **단일 결과 집합 덮어쓰기 (`sqlexec.go`)**:
   - `ExecuteSessionSQLContext` 루프에서 다중 쿼리(`SELECT 1 AS a; SELECT 2 AS b;`) 또는 프로시저 내부 다중 SELECT를 실행할 때, 이전 `res`를 덮어쓰고 마지막 `QueryResult`만 단독 반환함.
   - 첫 번째 결과 집합 및 중간 결과 집합이 메모리에서 폐기되어 클라이언트로 전송되지 않음.
2. **TDS 와이어 단일 직렬화 (`pkg/wire/tds/server.go`)**:
   - 단일 `res`만을 대상으로 `COLMETADATA (0x81) -> ROWs (0xD1) -> DONE (0xFD, DoneFinal)`을 구성하여 응답 스트림을 종료함.
   - `DoneMore (0x0001)` 플래그를 가진 중간 `DONE` 토큰이 방출되지 않아 ADO.NET `NextResult()` 호출 시 즉시 `false`가 반환되거나 첫 번째 데이터셋을 읽을 수 없음.

---

## 2. TDS 프로토콜 토큰 시퀀스 규격 (Token Sequencing Specification)

TDS 7.x/8.0 프로토콜에서 다중 결과 집합(Multiple Result Sets)의 토큰 패킷 스트림은 다음과 같은 표준 시퀀스를 엄격히 준수해야 합니다:

```mermaid
sequenceDiagram
    autonumber
    actor Client as Client Driver (ADO.NET / JDBC / SSMS)
    participant TDS as LionSQL TDS Listener (:1433)
    participant Engine as SQL Coordinator & VDBE VM

    Client->>TDS: SQLBatch (SELECT 1 AS a; SELECT 2 AS b;)
    TDS->>Engine: ExecuteSessionSQLContextBatch()
    Engine-->>TDS: QueryResult Chain (res1 -> res2)

    Note over TDS,Client: Result Set 1
    TDS->>Client: COLMETADATA (0x81, col: a)
    TDS->>Client: ROW (0xD1, val: 1)
    TDS->>Client: DONE (0xFD, Status: DoneMore | DoneCount, Count: 1)

    Note over TDS,Client: Result Set 2 (Final)
    TDS->>Client: COLMETADATA (0x81, col: b)
    TDS->>Client: ROW (0xD1, val: 2)
    TDS->>Client: DONE (0xFD, Status: DoneFinal | DoneCount, Count: 1)
```

### 2.1 토큰 상태 플래그 규격
- **중간 결과 집합 (Intermediate Result Set)**:
  - 토큰: `TokenDone (0xFD)` 또는 프로시저 내부의 경우 `TokenDoneInProc (0xFF)`
  - 상태: `DoneMore (0x0001)` | `DoneCount (0x0010)`
  - CurCmd: `0xC1` (SELECT) 또는 DML 코드 (`0xC3` INSERT, `0xC4` DELETE, `0xC5` UPDATE)
- **최종 결과 집합 (Final Result Set)**:
  - 토큰: `TokenDone (0xFD)` 또는 `TokenDoneProc (0xFE)`
  - 상태: `DoneFinal (0x0000)` | `DoneCount (0x0010)` (트랜잭션 활성 시 `DoneInXact 0x0004` 결합)
  - CurCmd: 마지막 문장의 실행 코드

---

## 3. 핵심 아키텍처 설계 (Core Architectural Design)

### 3.1 `QueryResult` 연결 리스트 체이닝 (`pkg/engine/coordinator.go`)
하위 호환성을 100% 유지하면서 다중 결과 집합을 지원하기 위해 `QueryResult` 구조체에 `Next` 체이닝 포인터를 추가합니다:

```go
type QueryResult struct {
    Success      bool             `json:"success"`
    Command      string           `json:"command"`
    Columns      []string         `json:"columns,omitempty"`
    Rows         []map[string]any `json:"rows,omitempty"`
    Values       [][]any          `json:"values,omitempty"`
    RowsAffected int64            `json:"rows_affected"`
    DurationUS   int64            `json:"duration_us"`
    Messages     []string         `json:"messages,omitempty"`
    Error        string           `json:"error,omitempty"`

    // Next links subsequent result sets produced by multi-statement batches
    // or stored procedures emitting multiple SELECT queries.
    Next *QueryResult `json:"next,omitempty"`
}
```

### 3.2 `sqlexec.go` 배치 결과 수집 파이프라인
단일 세션 문장 실행 루프에서 각 문장의 유의미한 결과(`len(res.Columns) > 0` 또는 `res.RowsAffected > 0`)를 누락 없이 연결합니다:

```go
var headRes *QueryResult
var tailRes *QueryResult

for _, stmt := range stmts {
    res, err := s.execSingleStatement(cs, stmt)
    if err != nil {
        return headRes, err // 오류 발생 시 이전까지의 결과와 오류 반환
    }
    if res != nil {
        if headRes == nil {
            headRes = res
            tailRes = res
        } else {
            tailRes.Next = res
            tailRes = res
        }
    }
}
```

### 3.3 TDS 스트리밍 직렬화 파이프라인 (`pkg/wire/tds/server.go`)
단일 `res` 처리를 `for cur := res; cur != nil; cur = cur.Next` 순회로 확장하여, `cur.Next != nil` 여부에 따라 `DoneMore`를 동적으로 부여합니다.

---

## 4. 단계별 구현 계획 (Phased Implementation Plan)

1. **1단계: 코어 엔진 `QueryResult.Next` 체이닝 (`pkg/engine/`)**:
   - `pkg/engine/coordinator.go`에 `Next *QueryResult` 정의.
   - `pkg/engine/sqlexec.go`에서 다중 쿼리 실행 시 `Next` 연결 리스트 구성.
   - 기존 단일 결과 쿼리와의 100% 하위 호환성 유지 검증.
2. **2단계: TDS 와이어 다중 결과 스트리머 구현 (`pkg/wire/tds/`)**:
   - `pkg/wire/tds/server.go`의 배치 쿼리 응답기 및 RPC 핸들러에서 체인 순회.
   - 중간 결과셋에 `TokenDone` / `DoneMore | DoneCount` 기록.
   - 최종 결과셋에 `TokenDone` / `DoneFinal | DoneCount` 기록.
3. **3단계: VDBE VM 저장 프로시저 다중 SELECT 지원 (`pkg/proc/`)**:
   - 저장 프로시저 내부에서 순차적으로 호출되는 `SELECT` 문들의 결과셋 체이닝.
4. **4단계: 통합 오라클 회귀 테스트 작성**:
   - `pkg/wire/tds/multiple_results_test.go`:
     - `SELECT 1 AS a; SELECT 2 AS b;` 실행 후 응답 바이트 스트림 내 `COLMETADATA` 2회, `DoneMore` 1회, `DoneFinal` 1회 검증.
     - DML과 SELECT가 혼합된 배치(`INSERT INTO t ...; SELECT * FROM t;`) 검증.

---

## 5. 엔지니어링 규약 준수 (Golden Rule Alignment)

- **[RULE:RadicalKISS]**: 거대한 MARS 가상 채널 에뮬레이션 대신 표준 순차적 다중 결과셋(Sequential NextResult)을 깔끔한 단일 연결 리스트로 구현하여 복잡도 최소화.
- **[RULE:StyleIntegrity]**: 모든 문서, 커밋 메시지, 테스트 코드에 장식용 이모지 일체 배제.
- **[RULE:PipelineVerification]**: Build -> Test -> Commit 3단계 검증 파이프라인 준수.
- **[RULE:ContinuousBacklog]**: `.sprint.db`, `TASKS.md`, `ACID_CORE_INTEGRITY_STABILIZATION_PLAN.ko.md` 동시 동기화.
