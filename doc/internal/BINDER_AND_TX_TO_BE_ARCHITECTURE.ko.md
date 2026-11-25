# 전용 바인더(Dedicated Binder) 및 트랜잭션(Tx) 모듈 완성 시 시스템 To-Be 상태 아키텍처 명세서

> **문서 식별자**: `BINDER-AND-TX-TO-BE-ARCHITECTURE-PLAN-V1`  
> **관련 마일스톤**: `v0.95.0`, `v0.96.0`, `v0.97.0` ➔ `v1.0-GA`  
> **핵심 주제**: 컴파일러 계층(Dedicated Binder / BoundAST)과 런타임 상태 머신(Tx Module Rigor) 통합 완료 시의 시스템 상태(To-Be) 정의  
> **준수 불변식 및 아키텍처 규칙**:
> - `[RULE:PlanPersistence]`: 설계 및 계획의 물리 파일 영구 보존
> - `[RULE:RadicalKISS]`: 중복 코드 삭제 우선 및 단순성 극대화
> - `[RULE:ExplicitError]`: 침묵하는 오류 금지 및 Fail-Closed 원칙
> - `[RULE:ZeroAllocation]`: 핫패스 무할당 및 $O(1)$ 정수 슬롯 바인딩
> - `[RULE:LockHierarchy]`: 엄격한 락 계층 및 데드락 방지
> - `[RULE:HandleHygiene]`: 리소스 누수 방지 및 닫기 멱등성
> - `[Invariant 12]`: SQL 문자열 무지성 파싱 금지 및 `sqlutil` 절대 원칙
> - `[Invariant 13]`: V2 바이너리 튜플 코덱 규격 준수
> - `[Invariant 15]`: 단일 B-Tree 다중 인덱스 및 정렬 보존 바이너리 인코딩

---

## 1. 개요 및 패러다임 전환 (Executive Summary & Paradigm Shift)

LionSQL은 그동안 단일 정적 실행 파일 내에 고성능 스토리지(In-Memory OCC / WiredTiger B+Tree), 다중 와이어 프로토콜(PostgreSQL :5433, TDS :1433), SQLite VDBE 스타일 바이트코드 가상 머신, Volcano 반복자 엔진을 안착시키며 급속도로 발전해 왔다.

그러나 컴파일 단계와 런타임 단계에 각각 남아있던 구조적 과도기 상태(임시방편 `if` 검증, 문자열 기반 컬럼 해석, 산발적 트랜잭션 에러 처리)로 인해 상용 DBMS 수준의 결정론적 엄밀성에 도달하는 데 제약이 존재했다.

본 명세서는 어제 확립된 **"BoundAST 기반 전용 바인더(Dedicated Binder)"**와 오늘 확립된 **"트랜잭션(Tx) 모듈 엄밀성 6단계 검증(Stages 1~6)"**이 모두 완성되었을 때 LionSQL이 도달하는 **최종 To-Be 시스템 상태**를 아키텍처, 컴파일러, 런타임, 스토리지, 성능의 5대 차원에서 명확히 규정한다.

```
[ As-Is: 과도기적 하이브리드 엔진 ]
- 파서 ➔ Raw AST ➔ 문자열 기반 부분 검증 ➔ 런타임 해시맵 컬럼 검색 & 매번 검증
- 트랜잭션 상태 머신의 T-SQL/ANSI 세부 엣지 케이스 산발적 대응
- 다운스트림(Volcano/VDBE)의 중복 방어 코드 산재

                 ▼  Dedicated Binder & Tx Rigor 완성 (To-Be)

[ To-Be: 상용 엔터프라이즈급 컴파일드 RDBMS ]
- 파서 ➔ 전용 바인더(BoundAST) ➔ CBO 플래너 ➔ 엄격한 Tx 관리자 ➔ O(1) 정수 슬롯 고속 실행
- 컴파일 타임 100% Fail-Closed (런타임 컬럼/타입 검증 코드 전면 삭제)
- @@TRANCOUNT, 세이브포인트, 트랜잭셔널 DDL, #temp vs @var, 커서 생명주기, 풀 리셋 완벽 결정론화
- 성능 3종 신기(Top-N 힙, 스캔-필터 직결, MIN/MAX Seek)와 결합된 서브마이크로초 OLTP
```

---

## 2. 엔드-투-엔드 쿼리 수명주기 및 파이프라인 대조 (Pipeline Comparison)

### 2.1 As-Is vs To-Be 아키텍처 비교도

```mermaid
flowchart TD
    subgraph AsIs [As-Is: 문자열 재분석 및 산발적 런타임 방어]
        P1[Parser: Pratt / ANTLR4] --> AST1[Raw AST: GeneralSelectStmt]
        AST1 --> V1["ValidateQueryAST(stmt, nil)<br/>(스키마 없는 불완전 구문 검사)"]
        V1 --> QI["QueryInfo 추출<br/>(Projections, Where 문자열 재분해)"]
        QI --> V2["ValidateSelectScope<br/>(실행 직전 문자열/토큰 스코프 검사)"]
        V2 --> Exec1["실행기 (Volcano / VDBE)<br/>- row.Get('salary') 해시 검색<br/>- 수많은 if 방어 코드<br/>- 트랜잭션 롤백 시 객체 정합성 수동 처리"]
    end

    subgraph ToBe [To-Be: BoundAST 단일 관문 & 무검증 O(1) 정수 슬롯 직결]
        P2[Parser: Pratt / ANTLR4] --> AST2[Raw AST]
        AST2 --> Binder["Dedicated Binder (단일 관문)<br/>- Catalog / Scope / Alias / Type 전수 확정<br/>- Fail-Closed: 42703, 42702, 42803, 23502<br/>- O(1) Physical Column Index 매핑"]
        Catalog[(Catalog Schemas)] --> Binder
        Binder --> BoundAST["Bound AST (불변 계약 객체)<br/>BoundSelectStmt / BoundExpr"]
        BoundAST --> Planner["CBO 최적화 플래너<br/>(Top-N, Scan-Filter, Seek 융합)"]
        Planner --> TxGate["Strict Tx Manager<br/>(트랜잭션/세션/커서 상태머신 계약)"]
        TxGate --> Exec2["고속 실행기 (Volcano / VDBE)<br/>- Binary Tuple Direct Offset 인덱싱<br/>- Zero Defensive Ifs<br/>- 트랜잭셔널 DDL & #temp 완벽 롤백"]
    end
```

### 2.2 파이프라인 단계별 핵심 책임 정의

| 파이프라인 단계 | 주요 컴포넌트 | 책임 및 불변식 | 에러 발생 시 처리 |
| :--- | :--- | :--- | :--- |
| **1. 구문 파싱** | `pkg/proc/parser.go`<br/>`pkg/proc/parser/antlr/` | SQL 텍스트를 문법 규칙에 따라 2-Tier(Pratt Fast-Path ➔ ANTLR4 Fallback) Raw AST로 변환 | 문법 오류(`SQLSTATE 42601`) 즉시 반환 |
| **2. 전용 바인딩** | `pkg/planner/binder.go`<br/>`pkg/planner/validator.go` | **컴파일 타임 단일 관문(Gatekeeper)**:<br/>1. 식별자/테이블/별칭 카탈로그 실체 해석<br/>2. 표현식 타입 추론 및 불법 중첩 차단<br/>3. 모호성 해소 및 $O(1)$ 정수 슬롯(`ColumnIdx`, `TableID`) 확정<br/>4. `BoundAST` 생성 | 실체 미존재(`42703`, `42P01`), 모호성(`42702`), 집계 위반(`42803`), 제약조건 위반(`23502`) Fail-Closed |
| **3. CBO 플래닝** | `pkg/planner/cbo.go`<br/>`pkg/planner/optimizer.go` | `BoundAST`를 입력받아 카디널리티 및 비용 기반 물리 실행 계획 수립.<br/>Top-N Sort Fusion, Scan-Filter Pushdown, Min/Max Index Seek 결합 | 최적 물리 플랜 트리 생성 |
| **4. 트랜잭션 가드** | `pkg/engine/tx_manager.go`<br/>`pkg/engine/session.go` | 쿼리 실행 직전 트랜잭션 상태 머신 검증:<br/>- `XACT_STATE() == -1` (Doomed Tx) 시 쓰기 즉시 차단<br/>- `READ ONLY` 트랜잭션 쓰기 차단(`25006`)<br/>- 세션 뮤텍스 직렬화 및 커서 컨텍스트 바인딩 | 트랜잭션 실패 상태(`25P02`), 읽기전용 위반(`25006`) |
| **5. 고속 실행** | `pkg/execution/`<br/>`pkg/engine/sqlexec_*.go` | **검증이 완료된 메타데이터 계약 기반 무검증 실행**:<br/>- 문자열 파싱 0건<br/>- 해시맵 컬럼 검색 0건<br/>- Direct Slice Indexing / Pointer Arithmetic으로 서브마이크로초 실행 | 런타임 I/O 및 OCC 충돌(`ErrWriteConflict`) 외 0 실패 |

---

## 3. BoundAST 컴파일러 계층 상태 (The BoundAST Compiler State)

### 3.1 O(1) 정수 슬롯 바인딩 (`BoundColumnRef`) 구조

바인더 통과 후 모든 AST 표현식은 구체적인 물리 슬롯 정보가 주입된 `BoundExpr`로 치환된다:

```go
type BoundColumnRef struct {
    OuterLevel int              // 0 = 현재 로컬 쿼리, 1 = 직전 상위 부모 쿼리 (상관 서브쿼리)
    TableID    int              // 조인 트리/실행 플랜 내의 테이블 고유 서열
    ColumnIdx  int              // 테이블 스키마 튜플 내의 O(1) 절대 물리 컬럼 오프셋
    Type       storage.DataType // 정적 데이터 타입 (런타임 타입 리플렉션 완전 제거)
    Name       string           // 원본 식별자 (EXPLAIN / 디버깅 전용)
}
```

### 3.2 100% 컴파일 타임 Fail-Closed 보장 체계

바인더가 성공적으로 `BoundAST`를 반환했다면, 다음 오류들은 런타임에 **수학적으로 0% 발생**함을 보장한다:
1. **존재하지 않는 컬럼 (`SQLSTATE 42703`)**: 모든 컬럼 참조(`t.col`, `col`)가 카탈로그 스키마에 실존함을 컴파일 타임에 검증 완료.
2. **모호한 컬럼 참조 (`SQLSTATE 42702`)**: 조인 대상 테이블 간 동일 컬럼명 존재 시 비한정 참조 차단 완료.
3. **존재하지 않는 테이블/별칭 (`SQLSTATE 42P01`)**: FROM/JOIN 스코프에 없는 테이블 한정자(`t3.col`) 사전 차단 완료.
4. **집계 및 윈도우 함수 위치 위반 (`SQLSTATE 42803`)**: WHERE, HAVING, JOIN ON, GROUP BY 내 불법 표현식 중첩을 `AggregateContextTracker`와 `CheckDisallowedExprs`로 완전 차단.
5. **NOT NULL 및 DML 계약 위반 (`SQLSTATE 23502`)**: INSERT 시 기본값 없는 NOT NULL 누락, UPDATE 대상 컬럼 부존재를 실행 진입 전 사전 차단.

### 3.3 다운스트림 코드 정리 (Radical KISS Primacy)

바인더가 단일 관문으로 확립됨에 따라, 다운스트림(`pkg/engine/sqlexec_select.go`, `sqlexec_eval.go`, `pkg/execution/`)에서 과거 수작업으로 작성되었던 방어 코드들이 전면 제거된다:
- **삭제 대상**:
  - `row.Get(colName)` 및 문자열 기반 컬럼 인덱스 검색 루프 전면 삭제 ➔ `row.Values[ref.ColumnIdx]` 또는 `ExtractColumnFromTuple` 즉시 접근.
  - `if col == nil`, `if tbl == nil` 등 수백 줄의 중복 방어 코드 제거.
  - 실행기 내부의 정규식/문자열 기반 절 분해 로직(`strings.Split`, `strings.Index`) 전면 박멸 (`[Invariant 12]`).

---

## 4. 트랜잭션(Tx) 및 세션 런타임 상태 (Enterprise Transaction & Session State)

트랜잭션 모듈의 엄밀성 검증(Stages 1~6)이 완료되면 LionSQL은 상용 엔터프라이즈 DBMS(SQL Server, PostgreSQL)와 100% 동등한 수준의 트랜잭션 상태 머신을 유지한다.

### 4.1 세션-트랜잭션 상태 머신 요약표

| 영역 | 상태 전이 및 동작 규칙 | 보장되는 엔터프라이즈 불변식 |
| :--- | :--- | :--- |
| **중첩 트랜잭션** | `BEGIN TRAN` 시마다 `TranCount++`<br/>`COMMIT` 시마다 `TranCount--`<br/>`ROLLBACK` 시 무조건 `TranCount = 0` | **물리 커밋 지연**: `TranCount == 0`일 때만 엔진 물리 커밋 수행.<br/>**전면 무효화**: 다중 중첩 상태(`TranCount = 5`)에서도 단 1회 롤백으로 전체 취소. |
| **T-SQL 세이브포인트** | `SAVE TRAN sp1` ➔ 스택 푸시<br/>`ROLLBACK TRAN sp1` ➔ sp1 시점으로 언두, **`@@TRANCOUNT` 불변 유지** | ANSI와 T-SQL의 규격 분리 준수. 이름 없는 롤백만 `TranCount`를 0으로 초기화. |
| **세미콜론 일괄 배치** | `BEGIN; INSERT 1; INSERT 2; COMMIT;` | 단일 네트워크 호출로 인입된 복합 배치를 단일 물리 트랜잭션으로 원자적 묶음 실행. 중간 실패 시 배치 전체 자동 롤백. |
| **트랜잭셔널 DDL** | `BEGIN; CREATE/DROP/ALTER TABLE; ROLLBACK;` | 롤백 시 카탈로그 메타데이터와 물리 B-Tree/OCC 스토리지가 이전 상태로 100% 원복. |
| **임시 객체 차등 롤백** | `#temp` 테이블: 롤백 시 데이터 및 테이블 정의 취소<br/>`@var` 테이블 변수: 롤백 시에도 데이터 보존 | SQL Server 공식 규격 준수: 오류 로깅용 `@var` 변수 보존 불변성 확립. |
| **커서 생명주기** | `WITHOUT HOLD`: COMMIT 시 자동 deallocate<br/>`WITH HOLD`: COMMIT 통과 생존, FETCH 지속<br/>`ROLLBACK`: **모든 커서 예외 없이 강제 파괴** | 데이터 스냅샷 오염 원천 차단. 비정상 커서 방치로 인한 메모리 누수 0건. |
| **XACT_STATE()** | `0`: 트랜잭션 없음<br/>`1`: 활성 정상 커밋 가능<br/>`-1`: 런타임 오류 발생 (Doomed Tx, 쓰기 차단) | Postgres `SQLSTATE 25P02`와 T-SQL `XACT_STATE() = -1` 완벽 대응. CATCH 블록에서 안전한 정리 보장. |
| **커넥션 풀 리셋** | `sp_reset_connection` / `DISCARD ALL` 인입 시 | 열린 채 반환된 미완료 트랜잭션 강제 롤백, 커서 deallocate, `TranCount = 0` 초기화로 풀 누수 원천 박멸. |
| **동시성 & 격리** | 세션 단일 뮤텍스 직렬화, Non-Blocking Snapshot Read, Write-Write 충돌 Fast-Fail | 32개 워커 카오스 스트레스 테스트 하에서 Data Race 0건, 데드락 0건. |

### 4.2 스토리지 듀얼 백엔드 패리티 (Dual-Engine Parity)

LionSQL의 양대 스토리지 백엔드는 트랜잭션 규격에 대해 100% 동일한 의미론을 제공한다:
- **In-Memory OCC (`pkg/storage/memengine`)**:
  - 단조 증가 버전 카운터(`Version uint64`)를 통한 워크스페이스 격리.
  - 쓰기 충돌 발생 시 즉시 `ErrWriteConflict`를 반환하여 데드락 없는 롤백 보장.
- **WiredTiger MVCC (`pkg/storage/wiredtiger`)**:
  - 해저드 포인터(Hazard Pointer) 기반 Lock-Free B+Tree 및 MVCC 타임스탬프.
  - `transaction_sync` 금지 불변식(`[Invariant 10]`) 준수로 140,000+ TPS 고동시성 유지.
  - 비정상 프로세스 강제 종료(`kill -9`) 후 재기동 시 WAL 및 주기적 체크포인트를 통한 무손실 복구(`[Invariant 9]`).

---

## 5. 성능 3종 신기와의 시너지 (Synergy with Performance Triad)

이미 구현 및 검증이 완료된 성능 3종 신기는 BoundAST 전용 바인더와 결합하여 폭발적인 시너지를 발휘한다:

```
[ BoundAST (컴파일 타임 메타데이터 확정) ]
  ├── 1. ColumnIdx (물리 튜플 내 정수 오프셋)
  ├── 2. IndexKeyPrefix & IndexSchema
  └── 3. Bounded Projection / Filter Context
             │
             ├──► [ PERF-TOP-N-SORT-FUSION-1 ]
             │    BoundAST의 정렬 키 ColumnIdx를 기반으로 고정 크기 Min/Max 힙 즉시 구성.
             │    대규모 페이징 쿼리에서 메모리 사용량 99% 절감, 디스크 스필 0건, 50배 가속.
             │
             ├──► [ PERF-SCAN-FILTER-FUSION-1 ]
             │    BoundAST의 필터 조건 ColumnIdx를 스토리지 엔진으로 직결.
             │    바이너리 튜플(*bufPtr)에서 직접 오프셋 추출. 불일치 행의 GC 힙 할당 0건(Zero Allocation).
             │
             └──► [ PERF-MIN-MAX-INDEX-SEEK-1 ]
                  BoundAST의 집계 대상 컬럼 인덱스 메타데이터를 감지.
                  테이블 전체 스캔 없이 B-Tree 리프 노드 단방향 O(1) 탐색으로 10,000배 즉시 가속.
```

---

## 6. v1.0-GA 상용 프로덕션 출시 준비 상태 (Production Readiness)

두 핵심 축(BoundAST 전용 바인더 + 엄밀한 Tx 모듈)이 완성된 시점에서 LionSQL은 완벽한 프로덕션 릴리스 기준을 충족한다:

1. **무결점 동시성 보장 (`go test -race` 100% 통과)**:
   - 커넥션, 세션, 트랜잭션, 커서, 스토리지 전 계층에서 데이터 레이스 0건 입증.
2. **핫패스 무할당 및 문자열 파싱 제로 (`Zero-Regex & Zero-String Parsing`)**:
   - 쿼리 실행 핫패스에서 정규식 및 무지성 문자열 분해 완전 박멸 (`[Invariant 7]`, `[Invariant 12]`).
   - $O(1)$ 정수 슬롯 바인딩으로 서브마이크로초 대화형 트랜잭션 레이턴시 달성.
3. **상용 클라이언트 도구 100% 호환 (SSMS, DBeaver, psql)**:
   - TDS 가상 Windows 플랫폼 불변식(`[Invariant 11]`) 및 가상 `USE <db>` 인프라(`[Invariant 2]`) 안착.
   - SQL Server Management Studio(SSMS) 및 DBeaver에서 에러 없는 스키마 탐색 및 스크립트 실행 보장.
4. **결정론적 회복탄력성 (Fail-Closed & Chaos Resilient)**:
   - 비정상 소켓 단절, Doomed Transaction, 카오스 동시성 부하에서도 데이터 오염 없는 원자적 자가 치유.

---

## 7. 향후 실행 순서 및 규율 (Execution Discipline)

1. **본 명세서의 영구 보존**:
   - 본 문서는 `[RULE:PlanPersistence]`에 따라 `notes/plans/BINDER_AND_TX_TO_BE_ARCHITECTURE_PLAN.ko.md`에 영구 보존되며, 임의로 축약되거나 삭제되지 않는다.
2. **TDD Red-Green 실행 원칙**:
   - 트랜잭션 엄밀성 테스트(Stage 1 ~ Stage 6) 및 BoundAST 바인더 구현은 반드시 **실패하는 테스트(Red)를 먼저 작성**하여 결함을 입증한 후 프로덕션을 수정하여 통과(Green)시키는 TDD 사이클을 엄격히 준수한다.
3. **사용자 승인 대기**:
   - 사용자의 명시적 지시가 있을 때까지 실제 Stage 1 코드 수정 및 구현을 시작하지 않고 대기한다.
