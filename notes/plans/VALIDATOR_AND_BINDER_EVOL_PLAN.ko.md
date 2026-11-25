# VALIDATOR-AND-BINDER-EVOL: Semantic Gatekeeper First Architecture & Binder Evolution Plan

> **문서 상태**: Phase 1 ~ 8 구현 완료 및 Phase 9/10 Spec-Out 종결 (Completed)  
> **최초 작성일**: 2026-11-24  
> **최종 갱신일**: 2026-11-25  
> **관련 마일스톤**: `v0.95.0`, `v0.96.0`  
> **선행 아키텍처 및 계보 문서 링크**:
> 1. [BOUND_AST_AND_DEDICATED_BINDER_SPEC.ko.md](file:///Users/maro/Projects/SQLion/doc/internal/BOUND_AST_AND_DEDICATED_BINDER_SPEC.ko.md) (전용 바인더 진화 아키텍처 및 5대 핵심 결함 분석 사양서)
> 2. [SEMANTIC_GATEKEEPER_ARCHITECTURE.ko.md](file:///Users/maro/Projects/SQLion/doc/internal/SEMANTIC_GATEKEEPER_ARCHITECTURE.ko.md) (시맨틱 게이트키퍼 수문장 및 Scope 격리 설계)
> 3. [BINDER_AND_TX_TO_BE_ARCHITECTURE.ko.md](file:///Users/maro/Projects/SQLion/doc/internal/BINDER_AND_TX_TO_BE_ARCHITECTURE.ko.md) (바인더 및 트랜잭션 관리자 To-Be 아키텍처)
> 4. [SQL_SEMANTIC_INTEGRITY_SPEC.ko.md](file:///Users/maro/Projects/SQLion/doc/internal/SQL_SEMANTIC_INTEGRITY_SPEC.ko.md) (SQL 포괄적 무결성 및 시맨틱 표준 사양)
> 5. [VALIDATOR_GATEKEEPER_HARDENING_PLAN.ko.md](file:///Users/maro/Projects/SQLion/notes/plans/VALIDATOR_GATEKEEPER_HARDENING_PLAN.ko.md) (게이트키퍼 5대 표준 SQL 불변식 강화 계획)
> **핵심 엔지니어링 규칙**: `[RULE:PlanPersistence]`, `[RULE:RadicalKISS]`, `[RULE:PipelineVerification]`, `[RULE:ExplicitError]`, `[RULE:StyleIntegrity]`

---

## 1. 개요 및 목적
본 문서는 LionSQL **Validator(시맨틱 유효성 검증 및 표준 SQL 방화벽)**와 **Binder(심볼 바인딩 및 슬롯 매핑)**의 핵심 의미론적 불변식과 결함을 전면적으로 해결하기 위한 통합 진화 설계 및 구현 계획을 정의합니다.

> **아키텍처 대원칙: Validator First (Gatekeeper Primacy)**
> 1. 모든 쿼리는 물리적 슬롯 오프셋 계산이나 심볼 바인딩에 진입하기 전에, **Validator가 1차 수문장(Gatekeeper)으로서 표준 SQL 시맨틱 규칙을 철저히 검사(Validate)하여 불량 쿼리를 즉각 Fail-Closed 차단**한다.
> 2. **Binder는 Validator를 통과한 정상 쿼리만을 대상으로** 테이블/컬럼 심볼을 식별하고, Hot-path는 `TupleSlot` 오프셋을 배정하며, 복잡한 식은 `BoundGenericExpr`로 감싸 안전하게 실행기(`eval.go`)에 위임한다.

### 12대 핵심 시맨틱 검증 및 바인딩 불변식 체크리스트
1. **별칭 섀도잉 (Scope Shadowing)**: `FROM users u`처럼 별칭이 부여되었을 때 원본 테이블명 `users.id` 접근 차단.
2. **셀프 조인 모호성 (Self-Join Ambiguity) & 무별칭 중복 차단**: `TableInstance` 기반 모호성 카운트 및 `FROM emp JOIN emp` 중복 테이블 명시 차단.
3. **TableLookup 정합성 (TableLookup Safety)**: `cur.TableLookup`의 덮어쓰기 방지 및 별칭 섀도잉 반영.
4. **와일드카드 완전 전개 (Wildcard Expansion)**: `SELECT *` 및 `u.*` 바인딩 시 스코프 내 개별 물리 컬럼들로 전개하여 정확한 `TupleSlot` 및 `DataType` 부여.
5. **GROUP BY 프로젝션 비집계 컬럼 검증 (SQLSTATE 42803)**: `GROUP BY`가 있을 때 집계되지 않고 `GROUP BY` 항목에 없는 일반 컬럼 프로젝션 차단.
6. **서브쿼리 내부 불법 식 재귀 검증 & WalkAST 경계 정비**: 서브쿼리 내부 `WHERE COUNT(*) > 0` 차단 및 `InExpr.Query` 경계 명문화.
7. **산술 연산자(-, *, /, %) 비호환 타입 검증 (SQLSTATE 42883)**: 비숫자형 피연산자 간 산술 연산 시 즉각 Fail-Closed 차단.
8. **스칼라 및 IN 서브쿼리 단일 컬럼 보장 검증 (SQLSTATE 42601)**: 단일 컬럼을 요구하는 서브쿼리에서 다중 컬럼 반환 시 컴파일 시점 차단.
9. **알 수 없는 함수 방치 차단 (SQLSTATE 42883)**: 내장/집계/윈도우 함수 목록에 없는 함수 호출 시 조용히 통과하지 않고 즉각 에러 반환.
10. **HAVING 절 프로젝션 별칭 참조 및 집계식 바인딩 지원**: HAVING 절에서 프로젝션 별칭(`COUNT(*) AS total_emp` -> `HAVING total_emp > 10`) 해석 및 유효성 검증.
11. **중첩 집계함수 금지 검증 (SQLSTATE 42803)**: 집계 함수 내부에 또 다른 집계 함수 호출(`SUM(AVG(x))`, `COUNT(MAX(y))`) 엄격 차단.
12. **GROUP BY 별칭과 컬럼명의 우선순위 역전 해결 (Table Column Primacy)**: `GROUP BY` 대상 해석 시 테이블 물리 컬럼을 프로젝션 별칭보다 항상 최우선으로 검증/바인딩.
13. **DISTINCT ORDER BY 프로젝션 일치 검증 (SQLSTATE 42P10)**: `SELECT DISTINCT` 시 ORDER BY 식이 프로젝션에 없으면 차단 (기완료).
14. **음수 LIMIT 및 OFFSET 리터럴 차단 (SQLSTATE 2201W / 2201X)**: `LIMIT -1`, `OFFSET -5` 컴파일 타임 차단 (기완료).
15. **중첩 윈도우 함수 및 집계 내 윈도우 함수 차단 (SQLSTATE 42P20)**: `ROW_NUMBER() OVER (...)` 중첩 차단 (기완료).

---

## 2. 세부 설계 및 개선 명세

### 2.1 TableScope 인스턴스 기반 섀도잉 및 셀프 조인 모호성
- `TableInstance` 도입:
  ```go
  type TableInstance struct {
      Alias        string
      OriginalName string
      Schema       *storage.TableSchema
      IsAliased    bool
  }
  ```
- `TableScope.instances []TableInstance`로 인스턴스 단위 추적.
- `Register(table, alias string, schema *storage.TableSchema) error`:
  - `alias != ""`: `schemas[cleanAlias] = schema` 등록. 원본 테이블명은 등록하지 않음 (**Shadowing**). 동일 스코프 내 중복 별칭은 에러 반환.
  - `alias == ""`: 이미 동일 원본명의 무별칭 인스턴스가 존재할 경우 `table name %q specified more than once` 에러 반환.
- 비한정 컬럼 모호성 검사:
  - `s.instances`를 순회하며 `inst.Schema.FindColumn(col)` 성공 횟수(`matchCount`)를 카운트.
  - 셀프 조인(`FROM emp e1 JOIN emp e2`)의 경우 `e1`, `e2` 2개 인스턴스에서 매칭되므로 `matchCount = 2` -> `column reference %q is ambiguous` 즉시 반환.

### 2.2 Binder TableLookup 정합성 및 섀도잉 반영
- `binder.go`에서 `stmt.From` 및 `stmt.Joins` 처리 시:
  - `cleanAlias != ""`: `cur.TableLookup[cleanAlias] = tableID`만 등록 (원본 테이블명은 섀도잉).
  - `cleanAlias == ""`: `cur.TableLookup[cleanTbl]` 및 `bareTbl` 등록 전 이미 존재하는지 검사하여 중복 등록 차단.

### 2.3 `SELECT *` 및 `u.*` 와일드카드 프로젝션 완전 전개 (Wildcard Expansion)
- `bindSelect`에서 `stmt.Projections` 바인딩 시:
  - `p.Expr`가 `*proc.ColumnRefExpr`:
    - `col.Name == "*"` (글로벌 와일드카드):
      - 현재 스코프(`cur.Tables`)의 모든 유효한 테이블 인스턴스를 순회.
      - 각 테이블 스키마의 모든 컬럼에 대해 `BoundColumnRef`를 생성하고 `TupleSlot: s.SlotOffsets[tableID] + colIdx`를 바인딩하여 `boundProjections`에 순서대로 추가.
    - `strings.HasSuffix(col.Name, ".*")` (한정자 와일드카드: e.g. `u.*`):
      - 한정자 `tblPart`를 `cur.TableLookup`에서 조회하여 해당 테이블의 모든 컬럼을 전개.
    - 일반 컬럼: 기존 단일 바인딩 수행.

### 2.4 GROUP BY 존재 시 비집계 컬럼 프로젝션 검증 (SQLSTATE 42803)
- `ValidateQueryASTWithParent`에서 `len(stmt.GroupBy) > 0`인 경우:
  - 각 프로젝션 식 `p.Expr`에 대해 검증 수행:
    - 상수 식(`proc.IsConstantExpr`)은 허용.
    - 프로젝션 식이 `stmt.GroupBy` 목록에 정확히 일치하거나 포함되어 있는 경우 허용.
    - 포함되지 않은 경우, 식 내부의 모든 컬럼 참조가 집계 함수(`proc.IsAggregateFunctionName`) 내부나 윈도우 함수 내부에 감싸져 있는지 검증.
    - 감싸져 있지 않은 컬럼이 발견되면 즉시 `column %q must appear in the GROUP BY clause or be used in an aggregate function` 에러(SQLSTATE 42803) 반환.

### 2.5 WalkAST 경계 정비 및 재귀적 서브쿼리 검증
- `WalkAST`:
  - `case *proc.InExpr:`: `e.Expr`, `e.List`만 순회하고 `e.Query`는 서브쿼리 경계로 내부 미진입 (`return nil`).
  - `case *proc.QuantifiedComparisonExpr:`: `e.Expr`, `e.List`만 순회하고 `e.Query`는 서브쿼리 경계로 내부 미진입 (`return nil`).
- `collectSubqueriesFromQuery(stmt *proc.GeneralSelectStmt) []*proc.GeneralSelectStmt`:
  - `From.Subquery`, `Joins[i].Table.Subquery` 및 모든 절의 식(`Projections`, `Where`, `Having`, `Joins[i].On`, `OrderBy`)에서 스칼라/EXISTS/IN/ANY 서브쿼리 AST 수집.
  - 수집된 서브쿼리들에 대해 `validateQueryASTInternal(sub, schemas, currentScope, depth+1)` 재귀 호출.

### 2.6 산술 연산자 타입 호환성 검증 (SQLSTATE 42883)
- `inferBinaryOpType(op, left, right)`:
  - `-`, `*`, `/`, `%`: 피연산자가 비숫자형(`!isNumericType(t)`)인 경우 즉시 `operator does not exist: %v %s %v (SQLSTATE 42883)` 반환.
  - `+`: `VarChar`가 아니고 비숫자형인 경우 에러 반환. 문자열 연결(`VarChar`)과 수치 덧셈 엄격 구분.

### 2.7 스칼라 및 IN 서브쿼리 단일 컬럼 보장 검증 (SQLSTATE 42601)
- `ValidateSubqueryColumnCount`:
  - `ScalarSubqueryExpr`, `InExpr`, `QuantifiedComparisonExpr`에서 `Query.Projections` 컬럼 수가 1개를 초과할 경우:
  - `subquery must return only one column, returned %d (SQLSTATE 42601)` 즉각 반환.

### 2.8 알 수 없는 함수 방치 차단 (SQLSTATE 42883)
- `inferFunctionReturnType`:
  - 내장 스칼라 함수(`proc.IsBuiltinFunction`), 집계 함수(`proc.IsAggregateFunctionName`), 윈도우 함수, 조건 함수(`ISNULL`, `COALESCE`, `NULLIF`, `IIF`)에 속하지 않는 함수명 호출 시:
  - `function %q does not exist (SQLSTATE 42883)` 에러 반환.

### 2.9 HAVING 절 프로젝션 별칭 바인딩 및 유효성 검증
- `ValidateHavingExpr`: `validateExprInternal(expr, true)` 호출하여 HAVING 절 내 프로젝션 별칭 허용.
- `bindHaving`: HAVING 절 내 식 바인딩 시 프로젝션 별칭을 스코프 내 프로젝션 식(`BoundProjection.Expr`)으로 안전하게 치환/바인딩.

### 2.10 중첩 집계함수 금지 검증 (SQLSTATE 42803)
- `ValidateNoNestedAggregates`: 집계 함수 인자 내부에 또 다른 집계 함수가 포함되어 있는지 재귀 검사.
- `Binder.bindExpr`: `inAggregate` 플래그를 통해 집계 함수 바인딩 도중 내부 집계 함수 발견 시 `aggregate function calls cannot be nested (SQLSTATE 42803)` 반환.

### 2.11 GROUP BY 별칭과 컬럼명의 우선순위 역전 해결 (Table Column Primacy)
- `ValidateExpr` 및 `validateExprInternal`:
  - 비한정 컬럼 식별 시 `s.instances`(입력 테이블 컬럼)을 먼저 확인.
  - 테이블 컬럼에 존재하면 해당 컬럼으로 바인딩하며, `matchCount == 0`인 경우에만 프로젝션 별칭(fallback)으로 바인딩.
- `bindGroupBy`:
  - `b.bindExpr(g)`를 통해 테이블 스키마에 우선 바인딩.
  - 테이블 스키마에 존재하지 않을 때만 `projections`의 별칭을 검색하여 바인딩.
  - `planner.go`: 프로젝션 별칭을 컬럼명보다 앞서 무조건 대입하던 로직을 테이블 컬럼 우선으로 교정.

---

## 3. 검증 계획
1. `TestBinder_Phase7_ScopeShadowing`: 별칭 사용 시 원본 테이블명 접근 거부 (`users.id FROM users u` -> FAIL).
2. `TestBinder_Phase7_SelfJoin_Ambiguity`: 셀프 조인 시 비한정자 컬럼 모호성 거부 (`id FROM emp e1 JOIN emp e2` -> FAIL, `e1.id` -> PASS).
3. `TestBinder_Phase7_SelfJoin_DuplicateUnAliased`: 별칭 없는 셀프 조인 거부 (`FROM emp JOIN emp` -> FAIL).
4. `TestBinder_Phase7_WildcardExpansion_Global`: `SELECT * FROM users` -> 3개 컬럼 전개 및 0, 1, 2 슬롯 바인딩 확인.
5. `TestBinder_Phase7_WildcardExpansion_TableQualified`: `SELECT u.* FROM users u JOIN orders o` -> users 컬럼만 정확한 슬롯으로 전개 확인.
6. `TestBinder_Phase7_GroupBy_UnaggregatedColumnRejection`: `SELECT id, name, COUNT(*) FROM users GROUP BY id` -> `name` 거부 확인.
7. `TestBinder_Phase7_Subquery_IllegalWhereAggregate`: `WHERE x IN (SELECT a FROM t WHERE COUNT(*) > 1)` 거부 확인.
8. `TestBinder_Phase7_ArithmeticTypeIncompatibility`: `'abc' - 'def'` -> FAIL (42883).
9. `TestBinder_Phase7_ScalarSubquery_MultiColumnRejection`: `SELECT (SELECT id, name FROM users)` -> FAIL (42601).
10. `TestBinder_Phase7_UnknownFunctionRejection`: `SELECT fake_fn(123) FROM users` -> FAIL (42883).
11. `TestBinder_Phase7_HavingProjectionAlias`: `SELECT dept, COUNT(*) AS total_emp FROM emp GROUP BY dept HAVING total_emp > 10` -> PASS.
12. `TestBinder_Phase7_NestedAggregateRejection`: `SELECT SUM(COUNT(*)) FROM users` -> FAIL (42803).
13. `TestBinder_Phase7_GroupBy_TableColumnPrimacy`: 테이블 컬럼명이 프로젝션 별칭과 겹칠 때 테이블 컬럼 우선 바인딩 확인.
14. 3-Stage Pipeline: `go build`, `go test -v -race ./...`, 커밋 및 푸시.

---

## 4. Phase 7 완료 요약 및 교훈 (Gatekeeper Consolidated Pass & Scan-Filter Fusion Hardening)

### 4.1 Clause별 검증 계약 단일화 완료
- `pkg/planner/validator.go` 내의 파편화된 중복 AST 순회를 제거하고, 각 SQL 절별 독립 검증 함수로 통합:
  1. `validateLimitOffset`: 음수 리터럴 컴파일 타임 차단 (`SQLSTATE 2201W / 2201X`).
  2. `setupTableScopeAndValidateJoins`: FROM/JOIN 서브쿼리 무별칭 차단 (`SQLSTATE 42601`), 중복 별칭 및 중복 무별칭 테이블 차단, Incremental JOIN ON 윈도우/집계 불가 검증.
  3. `validateWhereClause`: WHERE 집계(`42803`)/윈도우(`42P20`) 불가, 스칼라/IN 서브쿼리 단일 컬럼 보장(`42601`), 도메인 바운드 검증.
  4. `validateGroupByClause`: 집계/윈도우 불가, 음수/초과 위치 인덱스 차단, 순수 집계/상수 SELECT 시 상수 키 그룹화 허용 (`SELECT count(*) FROM t GROUP BY 1`), **Table Column Primacy (테이블 물리 컬럼이 프로젝션 별칭보다 항상 우선)**.
  5. `validateHavingClause`: 윈도우 불가(`42P20`), 중첩 집계 불가(`42803`), 단독 HAVING 비집계 컬럼 프로젝션 차단 (`ValidateProjectionUnderHaving`).
  6. `validateProjections`: 중첩 집계 불가(`42803`), 집계 내 윈도우 불가(`42P20`), 중첩 윈도우 불가(`42P20`), NTILE(0)/PERCENTILE_CONT(1.5) 등 도메인 바운드 초과(`22003`) 차단.
  7. `validateOrderByClause`: DISTINCT 프로젝션 불일치(`42P10`), 중첩 집계(`42803`), 중첩 윈도우(`42P20`), GROUP BY 비집계 정렬 차단.
  8. `validateQueryASTInternal`: 재귀 서브쿼리 AST 심층 검증.

### 4.2 8대 계약 테스트 스위트 완비 (`pkg/planner/validator_contract_test.go`)
- `TestContract_LimitOffset` (PASS)
- `TestContract_FromJoins` (PASS)
- `TestContract_WhereClause` (PASS)
- `TestContract_GroupByClause` (PASS)
- `TestContract_HavingClause` (PASS)
- `TestContract_ProjectionsClause` (PASS)
- `TestContract_OrderByClause` (PASS)
- `TestContract_RecursiveSubqueries` (PASS)
- 전수 100% PASS 및 -race 0 race 확인.

### 4.3 핵심 버그 진단 및 수정 내역
1. **Scan-Filter Fusion Pushdown의 sessionVars 유실 결함**:
   - 증상: `sp_CopyItems` 저장 프로시저에서 `INSERT INTO dest SELECT ... WHERE qty >= @p_min_qty` 실행 시 0 row 복사 (`TestStoredProcedureInsertSelect` 실패).
   - 원인: Scan-filter fusion이 `@p_min_qty`를 포함한 WHERE 필터 조건을 스토리지 반복자(`SeqScanIterator`, `IndexScanIterator`, `GatherIterator`)로 pushdown했으나, 해당 반복자들이 `sessionVars` 컨텍스트를 전달받지 못해 `nil`로 평가되어 모든 행을 drop함.
   - 조치: `SeqScanIterator`, `IndexScanIterator`, `GatherIterator`에 `sessionVars` 필드 및 `SetSessionVars` 인터페이스를 추가하고, `fillScanChunk`에서 `EvalTupleBoolExprWithContextErr` 호출 시 `sessionVars`를 주입하도록 전면 보완.
2. **HAVING without GROUP BY 비집계 검증 누락 수정**:
   - `ValidateProjectionUnderHaving`이 `scope.HasSchemas()` 내부에만 위치하여 AST 단계에서 차단되지 않던 결함을 시정하고, `pkg/engine/sqlexec_eval.go:ValidateSelectScope`에도 가드를 추가.
3. **ROWVERSION / Hidden / Computed 컬럼 NOT NULL 오탐지 수정**:
   - DML 유효성 검사기(`sqlexec_dml.go`)에서 시스템 자동 관리 컬럼을 NOT NULL 필수 검사 대상에서 제외.
4. **V2 바이너리 튜플 및 DDL 세이브포인트 스키마 동기화 수정**:
   - 세이브포인트 DDL 트랜잭션 도중 `ShadowCatalog.MarkTableAltered` 호출을 추가하여 롤백 후 스키마 불일치 방지.
5. **문자열 정렬 시 수치 파싱 역전 버그 수정**:
   - `SortIterator`의 `toInt64Fast`에서 `case string:` 분기를 제거하여 문자열 컬럼(`VARCHAR`)이 사전식 정렬을 유지하도록 복원.

---

## 5. Phase 8: DML 검증 및 바인딩 계약 단일화 (DML Validation & Binding Consolidation)

### 5.1 목표 및 설계 원칙
SELECT 절에서 달성한 "Clause별 검증 계약 단일화"를 DML(`INSERT`, `UPDATE`, `DELETE`, `MERGE`) 전반에 동일하게 적용하여, `sqlexec_dml.go` 곳곳에 파편화되어 흩어져 있던 검사 로직을 **단일 게이트키퍼 계약 함수**로 통합한다.

### 5.2 DML 6대 핵심 검증 계약
1. **[DML-1] 타겟 테이블 정합성 및 시스템 카탈로그 뷰 보호**:
   - 수정 대상 테이블의 카탈로그 존재 여부 확인.
   - `sys.*`, `information_schema.*` 등 가상 시스템 카탈로그 뷰에 대한 DML(INSERT/UPDATE/DELETE/MERGE) 즉각 Fail-Closed 차단 (`cannot modify system catalog view`).
2. **[DML-2] INSERT Arity / Column Count 불변식**:
   - 명시적 컬럼 지정 시: 컬럼 수와 VALUES 각 튜플의 표현식 수가 일치하지 않으면 즉각 차단 (`column count mismatch`).
   - 무컬럼 INSERT 시: 타겟 테이블의 비-계산/비-Hidden 컬럼 수와 일치해야 함.
   - `INSERT ... SELECT`: 타겟 테이블의 컬럼 개수와 SELECT 프로젝션 컬럼 수가 불일치하면 즉각 차단.
3. **[DML-3] UPDATE SET 절 컬럼 및 수정 불가 컬럼 제약**:
   - `SET col = expr`에서 `col`이 타겟 테이블에 존재하지 않으면 `column %q does not exist` 즉각 차단.
   - 읽기 전용 계산 컬럼(`IsComputed`), `ROWVERSION` 컬럼을 직접 `SET`으로 수정하려 할 경우 차단.
   - 동일 컬럼을 SET 절에서 중복 수정(`SET a = 1, a = 2`) 시 방어.
4. **[DML-4] DML WHERE 절 무결성 계약**:
   - UPDATE / DELETE의 WHERE 절 내 집계 함수(`42803`) 및 윈도우 함수(`42P20`) 금지.
   - 스칼라 서브쿼리 및 IN 서브쿼리의 단일 컬럼 반환 보장 (`SQLSTATE 42601`).
   - 존재하지 않는 컬럼 참조 Fail-Closed 차단.
5. **[DML-5] MERGE INTO Target/Source 계약**:
   - Target 테이블과 Source (테이블 또는 서브쿼리) 존재 및 ON 조건 유효성.
   - `WHEN MATCHED THEN UPDATE`의 SET 컬럼이 Target에 존재하는지 검증.
   - `WHEN NOT MATCHED THEN INSERT`의 컬럼 및 VALUES 개수 일치 검증.
6. **[DML-6] RETURNING / OUTPUT 프로젝션 유효성**:
   - RETURNING / OUTPUT 절의 컬럼이 타겟 테이블 또는 유효한 표현식인지 검증.

### 5.1 Phase 8 구현 완료 및 계약 검증 성과 (100% PASS)
- **전용 TDD 계약 테스트 스위트 완비**: [`pkg/planner/validator_dml_contract_test.go`](file:///Users/maro/Projects/SQLion/pkg/planner/validator_dml_contract_test.go) (5대 핵심 계약군 27개 서브케이스 100% 통과).
  1. `TestContract_DML_TargetTableProtection`: 비존재 테이블 및 시스템 카탈로그 뷰(`sys.*`, `information_schema.*`) 수정 Fail-Closed 차단.
  2. `TestContract_DML_InsertArityAndColumns`: 컬럼수-값 불일치, 다중행 arity 불일치, 유령 컬럼, 중복 컬럼, NOT NULL 누락, 계산 컬럼 직접 INSERT 차단, INSERT ... SELECT arity 불일치 차단.
  3. `TestContract_DML_UpdateSetClause`: SET 유령 컬럼 차단, 계산 컬럼/ROWVERSION 직접 수정 차단, SET 절 내 집계/윈도우 함수 차단(`ScopeUpdateSet`), 동일 컬럼 중복 SET 차단.
  4. `TestContract_DML_WhereClause`: UPDATE/DELETE WHERE 절 내 집계 함수(`42803`), 윈도우 함수(`42P20`), 유령 컬럼, 다중 컬럼 서브쿼리(`42601`) 차단.
  5. `TestContract_DML_PositiveValidStatements`: 정상 DML 10종에 대한 무오류 통과 검증.
- **게이트키퍼 엔진 연동**:
  - `pkg/planner/validator.go`: `ValidateDMLStmt` 및 `ScopeUpdateSet` 구현 완료. `Hidden` 컬럼(예: synthetic `_row_id`) 자동 제외 인라인 반영.
  - `pkg/engine/dml_validator.go`: `ValidateDMLStatementAST` 구현 및 멀티 테이블 JOIN DML 예외 분기 연동.
  - `pkg/engine/sqlexec_dml.go`: `execInsert`, `execUpdate`, `execDelete` 진입점에 AST 레벨 1차 수문장 검증 배포 완료.

---

## 6. Phase 9 & 10 아키텍처 종결 (Spec-Out 불변식 확정)

### 6.1 Phase 9: 파생 서브쿼리 인라인 가상 슬롯화 $\rightarrow$ [SPEC-OUT] 확정
- **결정**: 임의의 중첩/복합 서브쿼리(윈도우, 스칼라 연산, 피벗 등)에 대한 인라인 가상 스키마 컴파일러 구축은 **영구 Spec-Out(배제)**한다.
- **아키텍처 근거 (`[RULE:RadicalKISS]`)**:
  - LionSQL은 이미 `resolveDerivedTables`를 통해 복잡한 파생 테이블을 인메모리 임시 테이블로 실체화(Materialize)하여 실제 `TableSchema` 기반으로 100% 안전하게 실행하고 있다.
  - 여기에 슬롯 완벽주의를 위해 컴파일러급 정밀도/타입 추론 엔진을 도입하는 것은 수천 줄의 예외 처리 코드를 증식시키고 엣지 케이스 결함을 유발하는 안티패턴이다.
  - 따라서 파생 서브쿼리는 검증된 기존 실체화 경로에 안전하게 위임하고, 단일 테이블 및 물리 다중 조인 핫패스(`TupleSlot`)만 슬롯화하는 **하이브리드 바인딩 모델(Hybrid Binding Model)**을 영구 표준으로 확정한다.

### 6.2 Phase 10: Volcano 실행기 완전 순수 BoundAST 직결 $\rightarrow$ [SPEC-OUT] 확정
- **결정**: Volcano 반복자 전체(`pkg/execution/`)를 순수 `BoundAST` 전용 인터페이스로 빅뱅 재작성(Big Bang Rewrite)하는 작업은 **영구 Spec-Out(배제)**한다.
- **아키텍처 근거 (`[RULE:RadicalKISS]`, `[RULE:ZeroAllocation]`)**:
  - 현재 Volcano 실행기는 행(row)마다 파싱하지 않으며, 쿼리 시작 시 단 1회 파싱된 AST(`f.parsed`)를 캐싱하여 메모리에서 즉시 평가한다.
  - 수만 줄의 검증된 스토리지/조인/집계/정렬 반복자를 모두 뜯어고치는 것은 시스템 안정성을 위협하며 위험 대비 얻는 성능 이득(쿼리당 최초 수 µs)이 무의미하다.
  - 따라서 기존의 단 1회 파싱 AST 캐시 + 슬롯 핫패스(`t.GetIdx`) 하이브리드 실행 구조를 최종 완성형으로 유지한다.
