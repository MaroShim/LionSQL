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

### 과제 1: VDBE 바이트코드 디스패처 모듈화 (`pkg/proc/runtime/vm.go`)
* **현황**: `VMFrame.Execute` 메서드 내부에 824라인의 거대한 단일 `switch op.Code` 블록이 존재하여 함수의 순환 복잡도(Cyclomatic Complexity)가 매우 높고 프로파일링 스택 추적이 어려움.
* **계획**: 바이트코드 실행 인라이닝 성능과 제로 할당을 100% 보존하면서, 연산자 기능군별 private 헬퍼 메서드로 분리:
  * `execFlowOp`: JUMP, JUMP_IF_ZERO, RETURN, GOTO, YIELD 등 제어 흐름.
  * `execStorageOp`: OPEN_CURSOR, CLOSE_CURSOR, INSERT, UPDATE, DELETE, SEEK 등 스토리지 연산.
  * `execRelOp`: OP_REL_QUERY, REL_SCAN, REL_FILTER 등 관계형 연산.
  * `execArithOp`: ADD, SUB, MUL, DIV 등 수치/비트 산술 연산.

### 과제 2: 아키텍처 계층 정립: SQL 실행 코디네이터 분리 (`pkg/api` ➔ `pkg/engine`)
* **현황**: `pkg/api` 패키지가 HTTP REST 핸들러뿐만 아니라 15,000+ 라인에 달하는 SQL 실행 오케스트레이션 로직(`sqlexec*.go`)을 함께 품고 있음. 이로 인해 PGWire(`pkg/wire`)나 TDS(`pkg/wire/tds`)가 코어 엔진이 아닌 `pkg/api`에 의존하는 계층 역전 현상이 존재.
* **계획**: SQL 실행 오케스트레이션 로직을 `pkg/engine` 또는 `pkg/coordinator` 코어 레이어로 독립 승격.
  * `pkg/api`(HTTP/웹 스튜디오), `pkg/wire`(PGWire), `pkg/wire/tds`(TDS) 3개 어댑터가 동등하게 코어 엔진 인터페이스를 단방향으로 호출하도록 클린 아키텍처 확립.

### 과제 3: VDBE 바이트코드 엔진 성능 튜닝 & 제로 할당 고속화 (`pkg/proc/runtime`)
* **현황**: T-SQL 프로시저 실행(`BenchmarkTransferFunds`) 프로파일링 결과 1회 실행당 **6.34µs / 22.2KB 메모리 할당 / 171회 힙 할당** 발생 중. 로컬 변수 해시맵 조회, 수식 평가 시 중간 컨텍스트 맵 생성, 매 명령어 채널 `select` 등이 주요 병목으로 확인됨.
* **계획 (핵심 최적화 5대 과제)**:
  1. **로컬 변수 슬롯화 (Local Variable Slot Indexing)**: T-SQL 로컬 변수(`@from`, `@amount` 등) 접근 시 `map[string]storage.Value` 해시맵 조회 및 문자열 해싱/소문자 변환을 제거하고, 컴파일 시 슬롯 인덱스(0, 1, 2...)를 부여하여 `f.slots[inst.B]` 고정 슬라이스 $O(1)$ 직접 배열 접근으로 전환.
  2. **중간 컨텍스트 맵 생성(`buildRowEvalMap`) 제거 (Zero-Allocation Expression Evaluation)**: DML 및 수식 평가 시 매번 새 `map[string]any`를 생성하여 변수당 4개씩 키를 복사하고 `valToAny` 인터페이스 박싱을 하던 핵심 병목 제거 (현재 프로시저 실행당 22.2 KB / 171 allocs의 주원인 근절).
  3. **컨텍스트 취소 검사 배치화 (Batch Cancellation Check)**: 매 단일 바이트코드 명령마다 채널 `select / default`를 호출하여 발생하는 20~40ns 고정 오버헤드를 해소하기 위해 64 또는 128개 명령 주기(`if pc&63 == 0 && f.ctx != nil`)로 취소 검사 주기 최적화.
  4. **오프코드 특화(Specialization) 및 인터페이스 비교 제거**: `OpEq` 내 `inst.Data == "AND"` 등 동적 인터페이스 문자열 비교를 완전 제거하고 `OpAnd`, `OpOr` 독립 오프코드 분리 및 정수 핫패스 인라인 특화 오프코드(`OpAddInt`, `OpEqInt`) 도입.
  5. **인스트럭션 구조체 밀도 최적화 (CPU L1 Cache Locality)**: `Instruction` 구조체 내 `Data any` 인터페이스 헤더(16바이트)를 분리하고 레지스터 연산을 8~16바이트 고정 크기 레코드로 밀도 높게 패킹하여 CPU L1 Data Cache 적중률 극대화.
  * **관계형 스캔 벡터화 배치 파이프라이닝**: `OpRelScan`, `OpIndexScan`에서 행 단위 VM 왕복 오버헤드를 줄이기 위한 N개(64~1024건) 단위 청크 벡터화 스트리밍.
  * **마이크로 벤치마크 및 pprof 프로파일링 하네스**: `BenchmarkTransferFunds` (기준선: 6.34µs / 22.2KB / 171 allocs) 및 TPC-C New-Order 대상 단계별 성능 추적 및 검증.

---

## 3. CBO (Cost-Based Optimizer) 진행 현황 및 로드맵

| 번호 | 과제명 | 상태 | 설명 |
| :---: | :--- | :---: | :--- |
| **A-2** | **CBO 통계 메타데이터 디스크 영속화** | **완료 (`[x]`)** | `cbo_stats.json` 원자적 파일 쓰기 및 기동 시 자동 복원 연동 완료. |
| **A-1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | **대기 (`[ ]`)** | 컬럼별 정렬 및 히스토그램 수집을 `sync.WaitGroup` 병렬화 (v0.12.0 P1). |
| **B-3** | **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델** | **대기 (`[ ]`)** | 정렬된 입력에 대해 $O(M+N)$ 해시리스 스트리밍 조인(`MergeJoinIterator`). |
| **B-4** | **$N > 4$ 체인 조인 동적 계획법 (DP / Selinger)** | **대기 (`[ ]`)** | 5개 이상 테이블 조인 시 Left-Deep DP 및 Bushy Tree 동적 탐색. |
| **C-5** | **다중 컬럼 확장 통계 (Extended Statistics)** | **대기 (`[ ]`)** | 컬럼 상관관계로 인한 카디널리티 과소평가 방지. |
| **C-6** | **OR 조건 및 불리언 트리 선택도 계산** | **대기 (`[ ]`)** | 합집합 공식 기반 정밀 선택도 산출. |
| **C-7** | **MCV (Most Common Values) 최빈값 목록 결합** | **대기 (`[ ]`)** | 왜곡 데이터 최빈값 분리 처리. |
| **D-8** | **백그라운드 자동 통계 갱신 데몬** | **대기 (`[ ]`)** | DML 변동량(Dirty Tuples) 기반 자동 `ANALYZE` 트리거. |
| **D-9** | **CBO 옵티마이저 힌트 오버라이드 제어** | **대기 (`[ ]`)** | T-SQL 조인 및 인덱스 힌트 강제 반영. |
