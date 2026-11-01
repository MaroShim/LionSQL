# SQLLogicTest 107개 풀세트 (551,217 쿼리) 100% 무결점 인증 완료 보고서 (`notes/SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md`)

> - **작성일**: 2026-11-02
> - **상태**: 완료 (100% PASS / 불일치 0건)
> - **과제 식별 코드**: `SLT-FULL-100` (상위 과제: `DURA-SLT-2`, `GA-VALID-1`)
> - **관련 계획서**: [`notes/SLT_FULL_SUITE_100_PERCENT_PLAN.ko.md`](SLT_FULL_SUITE_100_PERCENT_PLAN.ko.md)
> - **자동화 검증 게이트**: [`scripts/verify_0.90.sh`](../scripts/verify_0.90.sh)

---

## 1. 개요 및 최종 실측 요약 (Executive Summary)

LionSQL `v0.90.0` 릴리즈 품질 게이트의 핵심 과제인 **SQLLogicTest (SLT) 107개 풀세트 전수 정합성 검증**을 완료하였습니다. SQLite 공식 SLT 코퍼스(`full`, `index`, `random`, `evidence`)와 LionSQL 자체 고난도 확장 스위트(`core`, `boundary`, `whitespace`)를 포함한 **총 107개 파일, 19,211개 문장, 551,217개 쿼리**가 단 1건의 해시/값 불일치나 패닉 없이 **100% PASS**를 달성하였습니다.

### 1.1 종합 실행 지표
- **실행 명령**: `./bin/sqllogictest -dir test/slt -parallel 8`
- **총 검증 파일 수**: **107개** (`107 passed, 0 failed`)
- **총 실행 문장(Statements)**: **19,211개**
- **총 검증 쿼리(Queries)**: **551,217개**
- **총 소요 시간**: **47.225s** (8코어 병렬 워커 기준, 초당 약 11,670 쿼리 처리)
- **프로토콜 교차 검증**: In-Process (`CoordinatorExecutor`) 및 PostgreSQL v3 Wire Protocol (`PGWireExecutor`, `TestSQLLogicTest_AllSuites_PGWire`) 양쪽 모두 100% PASS, `go test -race` 데이터 레이스 0건.

### 1.2 카테고리별 세부 통과 현황

| 카테고리 | 경로 패턴 | 파일 수 | 문장(Stmts) | 쿼리(Queries) | 결과 | 주요 검증 범위 |
| :--- | :--- | :---: | :---: | :---: | :---: | :--- |
| **Core & Stress** | `test/slt/core/*.slt` | 33 | 228 | 175 | **100% PASS** | 3VL 불리언, 윈도우, CTE, JSON, 파생 조인, 서브쿼리, 트랜잭션 격리 |
| **Boundary & Whitespace** | `test/slt/boundary/*`, `whitespace/*` | 2 | 20 | 20 | **100% PASS** | 키워드 식별자 충돌, 멀티바이트 리터럴, 탭/개행/다중 공백 |
| **SQLite Evidence** | `test/slt/evidence/*.test` | 12 | 171 | 314 | **100% PASS** | SQL 표준 명세 경계 조건 (`IN`, `TOTAL`, `REPLACE`, `VIEW`, `TRIGGER`) |
| **Full Select (1~5)** | `test/slt/full/select1~5.test` | 5 | 1,822 | 8,884 | **100% PASS** | 다중 중첩 대수식, 복합 `WHERE`/`CASE`, 최대 64개 테이블 콤마 조인 |
| **Index Stress** | `test/slt/index/**` | 15 | 16,472 | 141,836 | **100% PASS** | `BETWEEN`, `IN`, 가환 법칙(`commute`), `DELETE` 후 인덱스 재스캔, `ORDER BY` |
| **Random Aggregates** | `test/slt/random/aggregates/*` | 10 | 120 | 100,000 | **100% PASS** | 난수 생성 `COUNT`/`SUM`/`AVG`/`MIN`/`MAX`, `DISTINCT`/`ALL`, `NULL` 조합 |
| **Random Expressions** | `test/slt/random/expr/*` | 10 | 120 | 100,000 | **100% PASS** | 테이블리스 스칼라 대수식, 중첩 `CASE WHEN`, `NOT BETWEEN`, 타입 캐스팅 |
| **Random GroupBy** | `test/slt/random/groupby/*` | 10 | 120 | 100,000 | **100% PASS** | 다중 컬럼 `GROUP BY`, 단항 부호 프로젝션, 복합 `HAVING` 필터 |
| **Random Select** | `test/slt/random/select/*` | 10 | 120 | 99,996 | **100% PASS** | 프로젝션 별칭과 원본 컬럼명 충돌, 셀프/크로스 조인, 서브쿼리 |
| **합계 (Total)** | `test/slt/**` | **107** | **19,211** | **551,217** | **100% PASS** | **전수 무결점 통과 (Zero Conformance Mismatches)** |

---

## 2. 발굴 및 완치된 핵심 엔진 결함 (Root Causes & Resolutions)

55만여 개의 난수 및 경계 쿼리를 통과시키는 과정에서 기존 단위 테스트와 ORM 통합 테스트만으로는 포착하기 어려웠던 **심층 SQL 평가·플래닝·직렬화 결함 8건**을 발굴하여 근본 원인을 완치하였습니다.

### 2.1 프로젝션 별칭과 원본 컬럼명 동명 충돌 및 복수 동명 컬럼 덮어쓰기 완치
- **증상**:
  1. `SELECT - tab2.col2 - col1 * 40 AS col1 FROM tab2` 실행 시, 계산된 수식 결과 대신 원본 테이블의 `col1` 컬럼값이 반환됨 (`random/select/*`).
  2. `SELECT 52 AS col0, 13 AS col0` 또는 `SELECT dept_id, COUNT(*), COUNT(discount)` 실행 시, 결과 행을 `map[string]any`로만 전달하여 동일한 컬럼 키(`col0` 또는 `count`)의 앞선 값이 뒤의 값으로 덮어써짐 (`random/expr/*`, `core/agg_stress.slt` PGWire).
- **원인**:
  - Volcano [`ProjectIterator`](../pkg/execution/project.go)에서 원본 튜플의 컬럼명을 먼저 조회한 뒤 표현식을 평가하거나, VDBE [`OpResultRow`](../pkg/proc/runtime/vm_dispatcher.go) 및 PGWire [`server.go`](../pkg/wire/server.go)에서 위치 기반 슬라이스(`Values [][]any`) 없이 `map[string]any`만 사용해 동명 컬럼이 유실됨.
- **해결**:
  - `ProjectIterator`에서 명시적 수식/별칭 프로젝션이 원본 컬럼보다 우선하도록 평가 순서를 바로잡음.
  - VDBE `OpResultRow`, `execScalarSelect`, 그리고 PGWire 패킷 직렬화기(`handleExecute`, `writeExecutionResult`, `runAndWriteQueryWithFormats`)에 위치 기반 `Values [][]any` 슬롯을 일관되게 연결하고, SLT 러너([`runner.go`](../pkg/slt/runner.go))에서 동명 컬럼을 `col#1`, `col#2`로 분리 매핑하여 위치 무결성을 보장함.

### 2.2 VDBE 바이트코드 VM 비교 연산자 및 `OpBetween`의 SQL 3-Valued Logic (3VL) 정합성 완치
- **증상**:
  - `SELECT CASE WHEN NOT NULL < 71 THEN 1 ELSE 0 END` 쿼리가 `0` 대신 `1`을 반환함.
  - `SELECT * FROM tab0 WHERE 90 NOT BETWEEN NULL AND 88` 쿼리가 참(`90 <= 88`이 확정 거짓이므로 전체 `BETWEEN`은 거짓, 따라서 `NOT BETWEEN`은 참)이어야 함에도 행을 누락함 (`random/expr/*`, `random/aggregates/*`).
- **원인**:
  - [`vm_dispatcher.go`](../pkg/proc/runtime/vm_dispatcher.go)의 `OpEq`, `OpNeq`, `OpLt`, `OpLte`, `OpGt`, `OpGte`, `OpLike` 오프코드가 피연산자 중 하나가 `NULL`일 때 `storage.NullValue(storage.TypeBool)`(UNKNOWN) 대신 `storage.BoolValue(false)`를 레지스터에 기록하여, 직후 실행되는 `OpNot`이 이를 `TRUE`로 반전시킴.
  - `OpBetween` 역시 상한(`val <= high`)이 확정 `FALSE`일 때 좌변 `NULL` 여부를 불문하고 `FALSE`로 단락 평가(short-circuit)해야 하는 3VL 규칙(`UNKNOWN AND FALSE = FALSE`)을 누락함.
- **해결**:
  - 모든 비교 오프코드가 `NULL` 피연산자 입력 시 `storage.NullValue(storage.TypeBool)`을 반환하도록 수정하고, `OpBetween`에 3VL 단락 평가(`lowCmp > 0 || highCmp < 0`이면 확정 `false`)를 구현함.

### 2.3 `HAVING` 및 프로젝션 내 `IS NULL` / `LIKE` 중첩 집계 함수 추출 완치
- **증상**:
  - `SELECT col0 FROM tab1 GROUP BY col0 HAVING NOT AVG(DISTINCT col0) IS NULL` 실행 시 0행이 반환됨 (`random/groupby/slt_good_8.test`).
- **원인**:
  - [`aggregate.go`](../pkg/execution/aggregate.go)의 AST 순회기 `extractAggFromAST`에 `*proc.IsNullExpr`, `*proc.LikeExpr`, `*proc.WithinGroupExpr` 케이스가 누락되어 `IS NULL` 내부의 `AVG(DISTINCT col0)`이 보조 집계 필드로 등록되지 않음.
- **해결**:
  - `extractAggFromAST`에 세 AST 노드 타입 순회를 추가하고, Pratt 파서 [`parser_expr.go`](../pkg/proc/parser_expr.go)의 `parseFunctionCall`에서 `DISTINCT`뿐만 아니라 `ALL` 수량자(`AVG(ALL col0)`, `COUNT(ALL col0)`)도 파싱하도록 보강함.

### 2.4 테이블리스 스칼라 쿼리(`SELECT` without `FROM`)의 집계 함수 및 단항 부호 처리 완치
- **증상**:
  - `SELECT DISTINCT COUNT(*)`, `SELECT - SUM(DISTINCT - 57)` 등 `FROM` 절이 없는 스칼라 집계 쿼리에서 `COUNT(*)`가 `0`, `SUM(-57)`이 `NULL`로 반환됨 (`random/expr/*`).
- **원인**:
  - SQL 표준에서 `FROM` 절 없는 `SELECT`는 단일 암묵적 행(1 implicit row)에 대해 평가되므로 `COUNT(*)`는 `1`, `SUM(x)`/`AVG(x)`/`MIN(x)`/`MAX(x)`는 `x` 자체를 반환해야 하나, 스칼라 평가기 [`eval.go`](../pkg/proc/eval.go)의 `evalRowFunction`이 `0` 또는 `nil`을 반환함. 또한 `execScalarSelect`가 `SELECT DISTINCT` 접두사를 제거하지 못함.
- **해결**:
  - `execScalarSelect`에서 `ALL`/`DISTINCT` 수량자 루프 스트리핑을 추가하고, `evalRowFunction`이 단일 행 스칼라 문맥에서 표준 SQL 집계 결과를 반환하도록 수정함.

### 2.5 `OR` 술어 + 서브쿼리 결합 시 인덱스 바운드 과도 푸시다운 차단
- **증상**:
  - `WHERE (col0 > 130 AND col0 BETWEEN 186 AND 519) OR col4 IN (SELECT col1 FROM tab0 ...)` 실행 시 우변 서브쿼리를 만족하는 행이 누락됨 (`index/between/100/*`, `index/commute/*`).
- **원인**:
  - 최상위 조건절에 `OR`(`HasDisjunction`)가 존재함에도 좌변의 범위 조건만으로 단일 인덱스 스캔 바운드를 좁혀 스캔 단계에서 우변 대상 행을 조기 탈락시킴.
- **해결**:
  - [`bounds.go`](../pkg/planner/bounds.go) 및 [`planner.go`](../pkg/planner/planner.go)에서 최상위 `OR` 결합 시 단일 인덱스 레인지 바운드 축소를 차단하고 전체 필터 평가를 거치도록 방어함.

### 2.6 SQLite 관용 문법 5종 수용 (`evidence/*`)
- **내용**:
  1. `total(x)` 집계 함수 지원 (빈 집합에 대해 `NULL` 대신 `0.0`을 반환하는 부동소수점 합계).
  2. 빈 `IN ()` / `NOT IN ()` 구문 파싱 및 3VL 평가 (`x IN ()` ➔ `false`, `x NOT IN ()` ➔ `true`).
  3. `CREATE [TEMP|TEMPORARY] VIEW` 구문 허용.
  4. `val IN tablename` (괄호 없는 테이블 단축 표기) ➔ `val IN (SELECT * FROM tablename)` 자동 변환.
  5. `CREATE TRIGGER ... UPDATE ON` (`BEFORE`/`AFTER` 생략 시 기본 타이밍 허용).

### 2.7 `SELECT` vs `UPDATE` 0 나눗셈(Divide-by-Zero) 시맨틱 분리
- **내용**:
  - SQLite/SLT 호환 `SELECT` 수식에서는 `x / 0`이 에러 대신 `NULL`로 평가되어야 하는 반면, 엔터프라이즈 T-SQL `UPDATE` 문(`UPDATE products SET qty = qty / 0`)에서는 즉시 에러와 함께 DML 롤백이 발생해야 함(`TestGroup5_UpdateDivideByZero_DML_Rollback`).
  - [`eval.go`](../pkg/proc/eval.go)에 `EvalUpdateRowExpr`을 분리하여 VDBE [`vm.go`](../pkg/proc/runtime/vm.go) 및 [`vm_dispatcher.go`](../pkg/proc/runtime/vm_dispatcher.go)의 `UPDATE` 할당 루프에서는 0 나눗셈 시 엄격한 에러 및 롤백이 수행되도록 양립시킴.

### 2.8 SLT 정수형(`I`) 컬럼의 부동소수점 반올림 오차 제거
- **내용**:
  - SLT 표준 러너(C 구현체)는 쿼리 타입 시그니처가 `I`(Integer)일 때 부동소수점 결과를 반올림(`%.0f`)하지 않고 0 방향으로 절사(`(int64)v`, truncation toward zero)함. [`runner.go`](../pkg/slt/runner.go)의 `FormatCell`에서 `colType == 'I'`일 때 `strconv.FormatInt(int64(v), 10)`으로 절사하도록 수정하여 음수/소수 나눗셈 오차를 완전히 제거함.

---

## 3. `v0.90.0` 통합 검증 게이트 스크립트 (`scripts/verify_0.90.sh`)

릴리즈 직전 언제든 동일한 품질 기준을 원클릭으로 재현할 수 있도록 [`scripts/verify_0.90.sh`](../scripts/verify_0.90.sh)를 구축하였습니다:

1. **Stage 1 (Build)**: `bin/lionsql`, `bin/sqllogictest`, `bin/bench`, `bin/tpcc-runtime` (`-tags wiredtiger`) 정적 바이너리 컴파일
2. **Stage 2 (Unit & Race Test)**: 전 패키지 `go test -tags wiredtiger -count=1 -short -race ./...` (데이터 레이스 0건 검증)
3. **Stage 3 (SQLLogicTest Full Suite)**: `./bin/sqllogictest -dir test/slt -parallel 8` (107개 파일, 551,217 쿼리 전수 검증)
4. **Stage 4 (13-Domain Extended Smoke Test)**: `bash scripts/verify_report.sh --engine=memory` 및 `--engine=wiredtiger` (TDS/PGWire/HTTP 트라이브릿지, SIGKILL 크래시 복구, RBAC, 마이그레이션, 파티셔닝 13개 도메인 검증)
