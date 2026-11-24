# SQL 시맨틱 및 무결성 유효성 검증 마스터 아키텍처 플랜 (SQL Comprehensive Integrity and Semantic Plan)

> **작성일자**: 2026-11-24  
> **상태**: 완료 (Completed)  
> **문서 버전**: v1.1  
> **관련 마일스톤**: v0.95.0  
> **대상 이슈**: `/tmp/sql.txt`, `/tmp/sql2.txt`, `/tmp/sql3.txt`, `/tmp/sql4.txt`, `/tmp/sql5.txt`

## 1. 개요 및 배경
본 문서는 `/tmp/sql.txt`부터 `/tmp/sql5.txt`에 걸쳐 제기된 총 11건의 SQL 의미론(Semantic), 제약조건(Integrity Constraint), 날짜/산술 유효성 검증 결함을 집대성한 단일 마스터 아키텍처 계획서(SSOT)이다.

LionSQL 엔진 전반에 걸쳐 Fail-Closed 원칙에 입각하여, 문법적/의미론적으로 거부되어야 하는 쿼리가 조용히 통과하여 잘못된 결과(침묵의 데이터 오염, 임의 행 출력, 0건 출력, NULL 덮어쓰기)를 발생시키지 않도록 엄격한 유효성 검증 체계를 수립한다.

---

## 2. 결함 목록 및 태스크 매핑 (전체 11건)

### Part 1: SQL 시맨틱 및 서브쿼리 검증 (`/tmp/sql.txt`)
| 태스크 ID | 파일 및 순번 | 증상 요약 | 표준 동작 (Fail-Closed) | 상태 |
|---|---|---|---|---|
| `FEAT-SQL-SEMANTIC-VALIDATION-FAIL-CLOSED-1` | `sql.txt` #1 | `SELECT id, MAX(name) FROM t;` 시 GROUP BY가 없음에도 첫 행 id와 집계치를 조용히 반환 | `column "..." must appear in the GROUP BY clause or be used in an aggregate function` (SQLSTATE 42803) | 완료 (`52fab8a8`) |
| `FEAT-SQL-SEMANTIC-VALIDATION-FAIL-CLOSED-1` | `sql.txt` #2 | `SELECT count(*) FROM t GROUP BY 1;` 시 서수 1을 집계 함수로 오진단하여 반려 | 집계/상수만 존재 시 상수 그룹핑 정상 실행(3건), 비집계 혼재 시 명확한 진단 에러 | 완료 (`52fab8a8`) |
| `FEAT-SQL-SEMANTIC-VALIDATION-FAIL-CLOSED-1` | `sql.txt` #3 | `WHERE val IN (SELECT id, val FROM t)` 시 다중 컬럼 서브쿼리가 잘못된 세미 조인으로 변환되어 조용히 0건 반환 | `subquery must return only one column, returned 2` (SQLSTATE 42601) 에러 반환 | 완료 (`52fab8a8`) |

### Part 2: SQL 무결성 및 런타임 제약조건 검증 (`/tmp/sql2.txt` ~ `/tmp/sql5.txt`)
| 태스크 ID | 파일 및 순번 | 증상 요약 | 표준 동작 (Fail-Closed) | 상태 |
|---|---|---|---|---|
| `BUG-DML-INSERT-COLUMN-COUNT-MISMATCH-1` | `sql2.txt` #1 | `INSERT INTO t (id, name) VALUES (999, 'Dave', 30);` 시 초과 값(30)이 경고 없이 누락 삽입됨 | `INSERT has more expressions than target columns` (SQLSTATE 42601) 에러 반환 | 완료 |
| `BUG-JOIN-AMBIGUOUS-COLUMN-VALIDATION-1` | `sql2.txt` #2, `sql3.txt` #3 | 조인 쿼리의 SELECT 프로젝션 또는 WHERE 절에서 중복 컬럼(`id`)이 한정자 없이 참조될 때 모호성 검증 누락 | `column reference "id" is ambiguous` (SQLSTATE 42702) 에러 반환 | 완료 |
| `BUG-DML-UPDATE-NON-EXISTENT-COLUMN-1` | `sql3.txt` #1 | `UPDATE t SET val = non_existent_col WHERE id = 1;` 시 미존재 컬럼이 nil로 평가되어 val을 NULL로 덮어씀 | `column "non_existent_col" does not exist` (SQLSTATE 42703) 에러 반환 | 완료 |
| `FEAT-FROM-DERIVED-TABLE-ALIAS-REQUIRED-1` | `sql3.txt` #2 | `SELECT id FROM (SELECT id FROM t);` 와 같이 FROM 파생 서브쿼리에 별칭(Alias)이 없어도 허용됨 | `subquery in FROM must have an alias` (SQLSTATE 42601) 에러 반환 | 완료 |
| `BUG-DML-INSERT-NOT-NULL-OMISSION-1` | `sql4.txt` #1 | `INSERT INTO t (id) VALUES (2);` 시 NOT NULL & no DEFAULT 컬럼(`val`)이 누락되어도 NULL로 삽입됨 | `null value in column "val" of relation "t" violates not-null constraint` (SQLSTATE 23502) 에러 반환 | 완료 |
| `BUG-DATE-LITERAL-VALIDATION-FAIL-CLOSED-1` | `sql4.txt` #2 | `INSERT INTO t_dt VALUES (1, '2026-02-31'), (2, 'not-a-date');` 시 부적절한 날짜 문자열이 NULL로 묵인됨 | `date/time field value out of range: "2026-02-31"` (SQLSTATE 22008) 또는 `invalid input syntax for type date: "not-a-date"` (SQLSTATE 22007) 에러 반환 | 완료 |
| `BUG-EXISTS-SUBQUERY-SCALAR-WHERE-EVAL-1` | `sql5.txt` #1 | `SELECT 1 WHERE EXISTS (SELECT 1 FROM t_sub1);` 와 같은 테이블리스 스칼라 SELECT에서 WHERE EXISTS가 false로 평가되어 0행 반환 | EXISTS 서브쿼리를 세션 엔진을 통해 실제 평가하여 true -> 1 반환 | 완료 |
| `BUG-MATH-MOD-ZERO-DIVISION-ERROR-1` | `sql5.txt` #2 | `SELECT MOD(10, 0);` 실행 시 NULL 반환 | `ERROR: division by zero: divide by zero error encountered` (SQLSTATE 22012) 에러 반환 | 완료 |

---

## 3. 상세 분석 및 설계

### 3.1 `sql.txt` 이슈 상세 (`FEAT-SQL-SEMANTIC-VALIDATION-FAIL-CLOSED-1`)
1. **비집계 컬럼 및 집계 함수 혼용 검증 (`SELECT id, count(*) FROM t;`)**:
   - `pkg/execution/aggregate.go`의 `AggregateIterator.Open()`에서 `hasAggFuncs` 여부를 확인.
   - `GROUP BY`가 지정되지 않았더라도 프로젝션에 집계 함수가 포함되어 있으면 비집계 컬럼에 대해 `validateHavingClause`를 호출하여 `column "%s" must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)`를 반환하도록 차단.
2. **GROUP BY 서수 및 상수 그룹핑 (`SELECT count(*) FROM t GROUP BY 1;`)**:
   - `pkg/planner/planner.go`에서 프로젝션이 집계 함수 및 상수로만 구성된 경우, `GROUP BY 1`의 상수 정수를 서수가 아닌 단일 그룹 상수 키(`Constant Grouping`)로 해석하여 정상 실행(count = 3).
   - 비집계 컬럼이 혼재된 상태에서 집계 위치를 가리키면 `aggregate functions are not allowed in GROUP BY (cannot group by aggregate column position %d)`로 명확히 에러 진단.
3. **다중 컬럼 IN 서브쿼리 비교 차단 (`WHERE val IN (SELECT id, val FROM t)`)**:
   - `pkg/planner/decorrelate.go`의 `decorrelateIn`, `pkg/execution/eval.go`, `pkg/proc/eval.go`에서 서브쿼리 결과 컬럼 수가 1개를 초과할 때 `subquery must return only one column, returned %d (SQLSTATE 42601)`로 즉시 반려.

---

### 3.2 `sql2.txt` ~ `sql5.txt` 이슈 상세

#### 3.2.1 `BUG-DML-INSERT-COLUMN-COUNT-MISMATCH-1` (INSERT 컬럼 수 불일치)
- **원인 분석**:
  - `pkg/proc/parser_dml.go:1520` (`parseGeneralInsert`): VALUES 절 튜플 파싱 시 명시된 컬럼 개수 `len(cols)`와 값 개수 `len(vals)`를 비교하지 않고 통과시킴.
  - `pkg/proc/runtime/vm_dispatcher.go:1439` (`OpInsert`): 루프에서 `if i < len(data.ValExprs)` 조건으로 초과 값을 무시하고 필요한 만큼만 매핑함.
  - `pkg/engine/sqlexec_dml.go:328` (`execInsert`): DML 파서 경로에서도 컬럼 개수 불일치 검증 누락.
- **수정 설계**:
  - `pkg/proc/parser_dml.go`: `len(cols) > 0 && len(vals) != len(cols)`인 경우:
    - `len(vals) > len(cols)`: `INSERT has more expressions than target columns`
    - `len(vals) < len(cols)`: `INSERT has more target columns than expressions`
  - `pkg/proc/runtime/vm_dispatcher.go`: 파서를 거치지 않는 동적 경로 및 inferred columns 경로에서도 `len(cols) > 0 && len(data.ValExprs) != len(cols)` 검증 추가.
  - `pkg/engine/sqlexec_dml.go`: `parseInsertBody` 또는 `execInsert`에서 동일 검증 추가.

#### 3.2.2 `BUG-JOIN-AMBIGUOUS-COLUMN-VALIDATION-1` (조인 시 모호한 컬럼 참조 검증)
- **원인 분석**:
  - `pkg/engine/sqlexec_eval.go:195` (`ValidateWhereClauseWithSchemas`): WHERE 절의 비한정(Unqualified) `*ColumnRefExpr` 검증 시, `for _, sch := range schemas` 루프에서 매칭되는 첫 번째 스키마를 만나면 검증을 조기 종료(`ok = true`)하여 복수 스키마 매칭(`matches > 1`)을 감지하지 못함.
  - `pkg/engine/sqlexec_select.go:2288`: 복수 테이블 조인 시 SELECT 프로젝션의 비한정 컬럼에 대해 모호성 검증이 누락됨.
- **수정 설계**:
  - `pkg/engine/sqlexec_eval.go:195`: 비한정 컬럼(`Table == ""`) 검사 시 모든 스키마를 순회하며 매칭 카운트를 계산. `matchCount > 1`인 경우 `column reference %q is ambiguous` (SQLSTATE 42702) 에러 반환.
  - `pkg/engine/sqlexec_select.go`: 조인 쿼리(`len(schemas) > 1`) 프로젝션 유효성 검사 루프에서 동일하게 비한정 컬럼의 스키마 매칭 카운트를 검사하여 `matchCount > 1` 시 모호성 에러 반환.

#### 3.2.3 `BUG-DML-UPDATE-NON-EXISTENT-COLUMN-1` (UPDATE SET RHS 미존재 컬럼 오류)
- **원인 분석**:
  - `pkg/proc/runtime/vm.go:2250` (`execUpdateWhere`): SET 절의 LHS(`a.Column`)는 스키마 존재 여부를 검사하지만, RHS(`a.Value`)에 포함된 컬럼 참조는 사전 검증 없이 `EvalRowExpr`를 호출함.
  - `pkg/proc/eval.go:315` (`EvalRowExpr`): `*ColumnRefExpr` 평가 시 row에 컬럼이 없으면 에러를 반환하지 않고 `nil, nil`을 반환함.
- **수정 설계**:
  - `pkg/proc/runtime/vm.go:2250` 및 `pkg/engine/sqlexec_dml.go`: SET 절 실행 전 RHS 식에 존재하는 모든 컬럼 참조를 추출하여 대상 테이블 스키마(및 UPDATE FROM 조인 스키마)와 비교.
  - 미존재 시 `column %q does not exist` (SQLSTATE 42703) 에러 반환.

#### 3.2.4 `FEAT-FROM-DERIVED-TABLE-ALIAS-REQUIRED-1` (FROM 파생 서브쿼리 별칭 필수화)
- **원인 분석**:
  - `pkg/proc/parser_dml.go:721` (FROM 절) 및 `line 963` (JOIN 절): `(SELECT ...)` 파생 테이블을 파싱한 후 `alias` 토큰이 없어도 빈 문자열 `""`로 파싱을 성공 처리함.
- **수정 설계**:
  - 서브쿼리 파싱 직후 `alias == ""`인 경우 `subquery in FROM must have an alias` (SQLSTATE 42601) 에러 반환.
  - 단, IN 서브쿼리나 EXISTS 서브쿼리, 스칼라 표현식 서브쿼리는 영향받지 않도록 `FROM / JOIN` 절의 파생 테이블 컨텍스트에만 엄격히 적용.

#### 3.2.5 `BUG-DML-INSERT-NOT-NULL-OMISSION-1` (INSERT NOT NULL 컬럼 생략 누락 검증)
- **원인 분석**:
  - `pkg/engine/sqlexec_dml.go:610` 및 `pkg/proc/runtime/vm_dispatcher.go:1491`: INSERT 레코드에 컬럼이 누락된 경우 기본값(`col.Default`)을 채우지만, 기본값이 없고 컬럼이 `NOT NULL`인 경우 아무런 검증 없이 통과시킴.
  - `pkg/storage/type_validator.go:159`: `ValidateAndNormalizeRecord`에서 `if val, exists := rec[col.Name]; exists`로 존재하는 컬럼만 NOT NULL을 검사함.
- **수정 설계**:
  - `pkg/storage/type_validator.go`: 레코드 검증 시 스키마의 모든 컬럼을 순회하여, 레코드에 없고 기본값(`Default`)도 없으며 `Identity`, `IsHidden`, `IsRowVersion`, `HasComputedExpr()`도 아닌 `!Nullable` 컬럼에 대해 즉시 `null value in column %q of relation %q violates not-null constraint` (SQLSTATE 23502) 반환.
  - DML 엔진(`sqlexec_dml.go`, `vm_dispatcher.go`)에서도 동일하게 누락된 NOT NULL 컬럼을 Fail-Closed 처리.

#### 3.2.6 `BUG-DATE-LITERAL-VALIDATION-FAIL-CLOSED-1` (DATE 리터럴 Fail-Closed 유효성 검증)
- **원인 분석**:
  - `pkg/storage/types_extended.go:120`: `DateValueFromString`에서 `ParseDate` 에러 시 `NullValue(TypeDate)`를 조용히 반환.
  - `pkg/storage/type_validator.go`: `TypeDate`, `TypeDateTime`에 대한 문자열 리터럴 형식 검증 분기가 없어 임의의 문자열이 저장소 계층까지 전달됨.
- **수정 설계**:
  - `pkg/storage/type_validator.go`: `ValidateAndNormalizeValue`에 `TypeDate` 및 `TypeDateTime` 처리 추가.
  - 날짜 문자열 파싱 시도 후 실패 시:
    - 날짜 범위를 벗어난 경우 (예: `2026-02-31`): `date/time field value out of range: %q` (SQLSTATE 22008)
    - 유효하지 않은 문자열 형식인 경우 (예: `not-a-date`): `invalid input syntax for type date: %q` (SQLSTATE 22007)
  - `pkg/storage/types_extended.go` 및 `tx.go`: 변환 시 에러를 묵살하지 않고 명시적으로 반환.

#### 3.2.7 `BUG-EXISTS-SUBQUERY-SCALAR-WHERE-EVAL-1` (스칼라 SELECT WHERE EXISTS 서브쿼리 평가)
- **원인 분석**:
  - `pkg/engine/sqlexec_select.go:1434` (`execScalarSelect`): `whereClause`를 평가할 때 `proc.EvalRowExpr(expr, varRow, nil)`로 호출하며 3번째 인자인 `execSubquery`를 `nil`로 전달함.
  - `pkg/proc/eval.go:708`: `*ExistsExpr` 평가 시 `execSubquery == nil`이면 무조건 `false, nil`을 반환하여 행이 필터링됨.
- **수정 설계**:
  - `execScalarSelect` 실행 전 `whereClause`에 대해 `s.resolveSubqueriesWithSession(whereClause, cs)`를 호출하여 서브쿼리를 사전 바인딩하고,
  - `EvalRowExpr` 호출 시 `s.createSubqueryExecutor(cs)`를 전달하여 EXISTS / IN 서브쿼리가 정상 실행되도록 지원.

#### 3.2.8 `BUG-MATH-MOD-ZERO-DIVISION-ERROR-1` (MOD 0 나눗셈 에러 반환)
- **원인 분석**:
  - `pkg/proc/builtins_math.go:76`: `MOD` 함수 구현부에서 `args[1].IVal == 0` 또는 `n2 == 0`인 경우 `storage.NullValue`를 반환하고 있음.
- **수정 설계**:
  - `pkg/proc/builtins_math.go`: 두 번째 인자가 0인 경우 `fmt.Errorf("division by zero: divide by zero error encountered")` (SQLSTATE 22012) 에러 반환.
  - 내장 함수 평가기 및 VDBE 런타임에서 이 에러가 상위로 전파되어 트랜잭션 롤백 및 클라이언트에 명시적 오류가 전달되도록 보장.

---

## 4. 검증 계획 및 템플릿 기반 무작위 생성 테스트 (Verification Plan & Fuzz Suites)
- **단위 및 회귀 테스트**:
  - Part 1: `pkg/engine/sql_semantic_validation_test.go` (165 lines, 6 cases - 완료)
  - Part 2: `pkg/engine/sql_comprehensive_integrity_test.go` (8개 결함 1:1 회귀 테스트 - 100% PASS)
- **템플릿 기반 무작위 생성 테스트 스위트 (Fuzz Test Suites)**:
  1. `pkg/engine/fuzz_dml_constraint_negative_test.go`:
     - 시드 기반 무작위 DML 제약조건 네거티브 스위트:
       - 컬럼 수 불일치 INSERT (25개 랜덤 스키마/값 생성)
       - NOT NULL 컬럼 생략 INSERT (25개 랜덤 테이블/컬럼 생성)
       - 유효하지 않은 날짜 리터럴 삽입 (윤년, 13월, 32일, 잘못된 포맷 등 14개 케이스)
       - UPDATE SET 우변 미존재 컬럼 참조 (4개 케이스)
       - 다중 테이블 JOIN 모호한 컬럼 참조 (15개 케이스)
       - MOD/나눗셈 0 제로 연산 (5개 케이스)
     - 검증 결과: 100% PASS with `-race`
  2. `pkg/engine/fuzz_two_tier_boundary_test.go`:
     - 2-Tier 파서 (Pratt Fast-Path + ANTLR4 Fallback) 경계 검증 스위트:
       - 최대 20단 중첩 괄호 수식 연산 정합성 검증 (`(1 + (2 + ...))`)
       - 다중 CTE 체인 및 복합 서브쿼리 연산 정합성 검증
       - T-SQL 벤더 확장 힌트 (`WITH (NOLOCK, READPAST)`) 정상 라우팅 검증
       - 문법 오류(닫히지 않은 괄호, 닫는 괄호 초과, 빈 WHERE/ORDER BY 절, 별칭 없는 서브쿼리, 빈 프로젝션 SELECT)에 대한 Fail-Closed 즉시 거부 검증
     - 검증 결과: 100% PASS with `-race`
- **3단계 파이프라인 검증 (`[RULE:PipelineVerification]`)**:
  1. `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql` (성공)
  2. `go test -race ./pkg/engine/... ./pkg/proc/... ./pkg/storage/...` (100% PASS)
  3. 전체 회귀 테스트 및 SLT 오라클 무결성 검증 (`verify_no_slt_mutation.sh` 100% PASS, `sqllogictest` 32개 파일 100% PASS).
