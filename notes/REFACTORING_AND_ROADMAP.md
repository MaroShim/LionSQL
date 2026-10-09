# SQLion Refactoring History & Next Trajectory (`notes/REFACTORING_AND_ROADMAP.md`)

이 문서는 최근 수행된 대규모 코드 분할 및 리팩토링 내역과, 사용자 요청에 의해 **차주(Next Week)에 착수하기로 합의된 핵심 과제들**, 그리고 CBO 최적화 로드맵의 진행 상태를 정리합니다.

---

## 1. 최근 완료된 리팩토링 내역 (Completed Refactoring)

### ① 데드 조인 코드 540라인 제거 & `sqlexec_dml.go` 분할
* **배경**: 과거 Volcano 파이프라인 도입 이전에 작성되었던 비최적화 레거시 조인 루프(`execJoin`, `execNestedLoopJoin`, `execHashJoin`) 540라인이 방치되어 있었음.
* **조치**:
  * 데드 코드를 전면 삭제(Deletion-first)하여 빌드 바이너리 및 유지보수 부담 완화.
  * 3,000+ 라인의 거대 모놀리식 파일 `pkg/api/sqlexec_dml.go`를 도메인별 5개 모듈로 깔끔하게 분할:
    * `sqlexec_select.go`: SELECT 쿼리 처리 및 GroupBy/Having 집계.
    * `sqlexec_pivot.go`: PIVOT, UNPIVOT, FOR JSON PATH 쿼리.
    * `sqlexec_explain.go`: EXPLAIN, ANALYZE, 재귀 CTE.
    * `sqlexec_eval.go`: WHERE 조건식 및 연산자 평가.
    * `sqlexec_dml.go`: 순수 INSERT, UPDATE, DELETE, MERGE DML만 집중 유지.

### ② `pkg/storage/memengine/memengine.go` 분할
* **배경**: 인메모리 OCC 스토리지의 모든 로직(락, 테이블 데이터, WAL/AOF 복구, 트랜잭션, DDL)이 1,800라인 단일 파일에 몰려 있었음.
* **조치**:
  * `table_data.go`: 스트라이프 잠금(`tableDataLocks`) 및 샤드 슬라이스 데이터 관리.
  * `persistence.go`: WAL/AOF 압축, 복구, 체크포인트, RDB 스냅샷.
  * `ddl.go`: 테이블 및 인덱스 DDL 작업.
  * `tx.go`: 트랜잭션 세션 및 프라이빗 워크스페이스 변이.
  * `memengine.go`: 스토리지 엔진 생성, 라이프사이클 및 옵션 관리.

### ③ `pkg/api/server.go` HTTP 핸들러 모듈화
* **배경**: 웹 API 서버의 60여 개 엔드포인트 핸들러가 한 파일에 모여 있었음.
* **조치**: 도메인별 전용 모듈(`handlers_auth.go`, `handlers_admin.go`, `handlers_tables.go`, `handlers_records.go`, `handlers_import_export.go`, `handlers_procs.go`)로 완전 분리.

### ④ 문서 및 SDK 가이드 개편
* `doc/SDK.md` ➔ `doc/WEB_SDK.md`로 리네임 및 섹션 9(웹 & HTML 임베딩 레시피: Vanilla `fetch()`, SDK+SSE, HTMX, T-SQL `FOR JSON PATH`) 추가.
* `doc/ADR.md`에 ADR-017(HA 복제) 및 ADR-018(듀얼 트랙 UDF) 추가.

---

## 2. 차주 핵심 리팩토링 과제 (Next Week Architecture Backlog)

다음 주에 우선적으로 진행하기로 합의된 핵심 아키텍처 및 엔진 과제 3가지입니다:

### 과제 1: [완료] VDBE 바이트코드 디스패처 모듈화 (`pkg/proc/runtime/vm.go`, `vm_dispatcher.go`)
* **현황 & 완료 내용**: `VMFrame.Execute` 내 800+ 라인의 거대 `switch op.Code` 블록을 6대 기능군별 전용 헬퍼 메서드(`execFlowOp`, `execComputeOp`, `execTxOp`, `execStorageOp`, `execCursorAndCallOp`, `execRelOp`) 및 중앙 집중식 `handleCatch`로 분리 완료. 함수의 순환 복잡도(Cyclomatic Complexity)를 획기적으로 낮추고 pprof 프로파일링 추적성을 극대화함.

### 과제 2: 아키텍처 계층 정립: SQL 실행 코디네이터 분리 (`pkg/api` ➔ `pkg/engine`)
* **현황**: `pkg/api` 패키지가 HTTP REST 핸들러뿐만 아니라 15,000+ 라인에 달하는 SQL 실행 오케스트레이션 로직(`sqlexec*.go`)을 함께 품고 있음. 이로 인해 PGWire(`pkg/wire`)나 TDS(`pkg/wire/tds`)가 코어 엔진이 아닌 `pkg/api`에 의존하는 계층 역전 현상이 존재.
* **계획**: SQL 실행 오케스트레이션 로직을 `pkg/engine` 또는 `pkg/coordinator` 코어 레이어로 독립 승격.
  * `pkg/api`(HTTP/웹 스튜디오), `pkg/wire`(PGWire), `pkg/wire/tds`(TDS) 3개 어댑터가 동등하게 코어 엔진 인터페이스를 단방향으로 호출하도록 클린 아키텍처 확립.

### 과제 3: VDBE 바이트코드 엔진 성능 튜닝 & 제로 할당 고속화 (`pkg/proc/runtime`)
* **진행 현황**:
  1. **[완료] 로컬 변수 슬롯화 (Local Variable Slot Indexing)**: 프로시저 파라미터 및 로컬 변수(`DECLARE`, `SET`, `SELECT INTO`, `FETCH INTO`) 접근 시 `map[string]storage.Value` 해시맵 조회 및 소문자 정규화 오버헤드를 완전히 제거하고, 컴파일 타임 슬롯 인덱스(0, 1, 2...)를 부여하여 `f.slots[inst.B]` 고정 슬라이스 배열 $O(1)$ 직접 접근으로 전환 완료.
  2. **[완료] 중간 컨텍스트 맵 생성(`buildRowEvalMap`) 제거 (Zero-Allocation Expression Evaluation)**: DML 및 수식 평가 시 `f.evalCtx` 사전 동기화 및 빈 행 캐싱을 적용하여 매번 새 `map[string]any` 생성/복사 병목 제거 완료 (프로시저 호출당 할당 171건 ➔ 122건으로 대폭 감소).
  3. **[완료] 컨텍스트 취소 검사 배치화 (Batch Cancellation Check)**: 매 단일 바이트코드 명령마다 채널 `select / default`를 호출하여 발생하는 20~40ns 고정 오버헤드를 해소하기 위해 64개 명령 주기(`if pc&63 == 0 && f.ctx != nil`)로 취소 검사 주기 최적화 완료.
  4. **[완료] 오프코드 특화(Specialization) 및 인터페이스 비교 제거**: `OpEq` 내 `inst.Data == "AND"` 등 동적 인터페이스 문자열 비교를 완전 제거하고 `OpAnd`, `OpOr` 독립 오프코드 분리 및 정수 핫패스 인라인 특화 오프코드(`OpAddInt`, `OpSubInt`, `OpMulInt`, `OpEqInt`, `OpNeqInt`, `OpLtInt`, `OpLteInt`, `OpGtInt`, `OpGteInt`) 도입 완료.
  5. **[완료] 비교-점프 융합 및 피프홀 점프 체이닝 (Fused Compare-and-Branch & Jump Threading)**: `IF`/`WHILE` 조건문 평가 시 비교 오프코드와 점프 명령어를 단일 융합 오프코드(`OpJumpIfLtInt`, `OpJumpIfGteInt` 등)로 결합하여 불리언 레지스터 쓰기 및 VM 루프 디스패치를 50% 절감하고, `finalizeProgram` 피프홀 패스에서 징검다리 점프 체이닝 및 인접 Fall-through 점프 제거 완료.
  6. **[대기] 인스트럭션 구조체 밀도 최적화 (CPU L1 Cache Locality)**: `Instruction` 구조체 내 `Data any` 인터페이스 헤더(16바이트)를 분리하고 레지스터 연산을 8~16바이트 고정 크기 레코드로 밀도 높게 패킹하여 CPU L1 Data Cache 적중률 극대화.
* **TPC-C 성능 검증 결과 (2 Warehouses, 20 Workers, 15s)**:
  * **Go Native API (`cmd/tpcc`)**: **763,496 tpmC / 28,300 TPS** (New-Order 평균 0.82ms, P95 3.01ms)
  * **T-SQL VDBE Bytecode VM (`cmd/tpcc_vdbe`)**: **475,781 tpmC / 17,803 TPS** (New-Order P50 0.56ms, P95 6.53ms, Payment P50 0.05ms)
  * VDBE 가상머신 위에서도 네이티브 드라이버 대비 **63% 성능 유지율**을 달성하며 단일 노드 1.78만 TPS 수준의 엔터프라이즈 처리량 달성 완료.

---

## 3. CBO (Cost-Based Optimizer) 진행 현황 및 로드맵

| 번호 | 과제명 | 상태 | 설명 |
| :---: | :--- | :---: | :--- |
| **A-2** | **CBO 통계 메타데이터 디스크 영속화** | **완료 (`[x]`)** | `cbo_stats.json` 원자적 파일 쓰기 및 기동 시 자동 복원 연동 완료. |
| **A-1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | **완료 (`[x]`)** | 컬럼별 정렬 및 히스토그램 수집을 `sync.WaitGroup` 병렬화(`BuildTableStatsParallel`) 완료. |
| **B-3** | **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델** | **대기 (`[ ]`)** | 정렬된 입력에 대해 $O(M+N)$ 해시리스 스트리밍 조인(`MergeJoinIterator`). |
| **B-4** | **$N > 4$ 체인 조인 동적 계획법 (DP / Selinger)** | **대기 (`[ ]`)** | 5개 이상 테이블 조인 시 Left-Deep DP 및 Bushy Tree 동적 탐색. |
| **C-5** | **다중 컬럼 확장 통계 (Extended Statistics)** | **대기 (`[ ]`)** | 컬럼 상관관계로 인한 카디널리티 과소평가 방지. |
| **C-6** | **OR 조건 및 불리언 트리 선택도 계산** | **대기 (`[ ]`)** | 합집합 공식 기반 정밀 선택도 산출. |
| **C-7** | **MCV (Most Common Values) 최빈값 목록 결합** | **대기 (`[ ]`)** | 왜곡 데이터 최빈값 분리 처리. |
| **D-8** | **백그라운드 자동 통계 갱신 데몬** | **대기 (`[ ]`)** | DML 변동량(Dirty Tuples) 기반 자동 `ANALYZE` 트리거. |
| **D-9** | **CBO 옵티마이저 힌트 오버라이드 제어** | **대기 (`[ ]`)** | T-SQL 조인 및 인덱스 힌트 강제 반영. |

---

## 4. SQL Syntax & Parser Coverage Backlog (T-SQL & ANSI SQL 문법 완비 과제)

| 번호 | 과제명 | 상태 | 설명 |
| :---: | :--- | :---: | :--- |
| **S-1** | **T-SQL & ANSI 표준 페이징 정규화 (`OFFSET ... FETCH NEXT`)** | **대기 (`[ ]`)** | `OFFSET n ROWS FETCH NEXT m ROWS ONLY` 및 `SELECT TOP (n)` AST 정규 통합. |
| **S-2** | **복합 한정자(Qualifier) 및 인라인 별칭(Alias) 파싱 정밀화** | **대기 (`[ ]`)** | `[db].[schema].[tbl].[col]`, `"schema"."tbl"` 및 `AS` 없는 인라인 별칭 파싱. |
| **S-3** | **FROM 절 파생 테이블(Derived Table) 및 인라인 서브쿼리** | **대기 (`[ ]`)** | `FROM (SELECT ...) AS t` 서브쿼리 AST 노드화 및 Volcano 실행 연동. |
| **S-4** | **T-SQL 내장 스칼라 함수 & 조건부 수식 정식 AST화** | **대기 (`[ ]`)** | `COALESCE`, `ISNULL`, `CAST`, `CONVERT`, `CASE WHEN` 정규 AST 표현 및 단락 평가. |
| **S-5** | **고급 조인(Join) 문법 확장 (`FULL OUTER`, `APPLY`)** | **대기 (`[ ]`)** | `FULL OUTER JOIN`, `CROSS JOIN`, T-SQL 특화 `CROSS/OUTER APPLY` 지원. |
| **S-6** | **세션 제어문(SET Statements) 정식 AST 파서 및 상태 바인딩** | **완료 (`[x]`)** | `SetOptionStmt` AST, VDBE 컴파일, 다중 옵션 쉼표 구분 및 세션 플래그 반영 완료. |
| **S-7** | **EXISTS / NOT EXISTS 상관 서브쿼리 술어 지원** | **대기 (`[ ]`)** | `WHERE EXISTS (...)` 조건의 Semi-Join / Anti-Join 변환 및 실행. |

---

## 5. Overall System & Database Engine Performance Optimization Roadmap (전체 엔진 성능 고속화 로드맵)

| 단계 | 전략 분야 | 핵심 구현 과제 | 상태 | 예상 효과 및 ROI |
| :---: | :--- | :--- | :---: | :--- |
| **P-1** | **스토리지/트랜잭션** | **배치형 그룹 커밋 (Group Commit)** & 읽기 전용 Fast-Path | **대기 (`[ ]`)** | 동시 쓰기 워크로드 TPS 2~4배 향상 (15K ➔ 50K+ TPS) |
| **P-2** | **스토리지/디코딩** | **바이너리 튜플 지연 역직렬화 (Lazy Decoding)** & 커버링 인덱스 스캔 | **대기 (`[ ]`)** | 스캔 속도 2배 향상, 불필요한 Varint 디코딩 힙 할당 제거 |
| **P-3** | **와이어 프로토콜** | **제로카피 와이어 인코딩** & I/O 버퍼 결합 (Write Combining) | **대기 (`[ ]`)** | 중간 `[]any` 레코드 박싱 제거, TCP syscall 횟수 대폭 감소 |
| **P-4** | **실행 엔진** | **벡터화 청크 반복자 (Chunked Batch Next: 64~256건)** | **대기 (`[ ]`)** | 볼케이노 함수 호출 오버헤드 감소, 대량 집계/스캔 지연 60% 단축 |
| **P-5** | **캐시 및 메모리** | **고속 SQL 지문 해싱** & **쿼리 아레나 할당기 (Per-Query Arena)** | **대기 (`[ ]`)** | 단순 PK 조회 파서 바이패스, GC 부하 90% 경감 및 p99 지연 안정화 |

