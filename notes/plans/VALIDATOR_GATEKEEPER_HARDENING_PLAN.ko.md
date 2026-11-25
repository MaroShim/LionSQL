# Semantic Validator Gatekeeper Hardening Plan (`notes/plans/VALIDATOR_GATEKEEPER_HARDENING_PLAN.ko.md`)

## 1. 개요 및 목적
LionSQL의 SQL 쿼리 처리 파이프라인에서 **Validator (`pkg/planner/validator.go`)**는 단순한 구문 검사기를 넘어 데이터베이스 엔진 전체의 신뢰성을 지키는 **최전선 방어선(Gatekeeper)**입니다. 실행기(Volcano CBO / VDBE)는 정상적인 쿼리에 대해 높은 성능과 정확성을 발휘하지만, 시맨틱 규격을 벗어난 비정상 쿼리가 침투할 경우 런타임 침묵 누락(Silent Row Omission), 부정확한 계산 결과 반환, 또는 예기치 못한 패닉이 발생할 위험이 있습니다.

본 계획은 SQL 표준(ANSI SQL / PostgreSQL / SQL Server)에 부합하는 5대 핵심 시맨틱 방어 규칙을 Validator에 전면 구현하고 체계적으로 검증합니다.

---

## 2. 5대 핵심 강화 사양 및 불변식

### Invariant 1: GROUP BY 시 ORDER BY 비집계 컬럼 차단 (SQLSTATE 42803)
- **배경 및 원칙**: 
  - `GROUP BY` 절이 지정된 쿼리에서 `ORDER BY` 절에 명시된 표현식은 반드시 `GROUP BY` 절에 포함된 컬럼이거나, 프로젝션 별칭(Alias)으로 치환 가능하거나, 집계 함수(`SUM`, `COUNT` 등)로 감싸져 있어야 함.
  - 비집계 원본 컬럼(예: `SELECT dept, COUNT(*) FROM emp GROUP BY dept ORDER BY salary;`)은 그룹화된 결과셋의 정렬 키로서 수학적으로 정의되지 않으므로 즉각 Fail-Closed 차단해야 함.
- **에러 규격**:
  - `column "%s" must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)`
- **오디널 및 프로젝션 별칭 호환성**:
  - `ORDER BY 1`, `ORDER BY 2` (정수 오디널) 및 `ORDER BY cnt` (프로젝션 별칭)은 정상적으로 허용.

### Invariant 2: SELECT DISTINCT 시 ORDER BY 프로젝션 일치 검증 (SQLSTATE 42P10)
- **배경 및 원칙**:
  - `SELECT DISTINCT`는 프로젝션 목록에 명시된 컬럼들의 조합으로 중복을 제거함.
  - 프로젝션 목록에 없는 컬럼(예: `SELECT DISTINCT a FROM t ORDER BY b;`)으로 정렬할 경우, 동일한 `a` 값을 갖는 여러 행 중 어떤 `b` 값으로 정렬 순서를 결정해야 할지 모호하므로 표준 SQL에서 금지됨.
- **에러 규격**:
  - `for SELECT DISTINCT, ORDER BY expressions must appear in select list (SQLSTATE 42P10)`

### Invariant 3: FROM / JOIN 절 파생 테이블(Subquery) 무별칭 차단 (SQLSTATE 42601)
- **배경 및 원칙**:
  - `FROM (SELECT 1)` 또는 `JOIN (SELECT 2)`와 같이 FROM/JOIN 절에 위치하는 서브쿼리(Derived Table)는 반드시 명시적인 별칭(Alias)을 가져야 함.
  - 별칭이 없으면 상위 쿼리에서 해당 서브쿼리의 컬럼을 식별하거나 수식 참조할 수 없음.
- **에러 규격**:
  - `subquery in FROM must have an alias (SQLSTATE 42601)`

### Invariant 4: 음수 LIMIT / OFFSET 컴파일 타임 거부 (SQLSTATE 2201W / 2201X)
- **배경 및 원칙**:
  - `LIMIT -1` (리터럴 지정 시), `LIMIT -10`, `OFFSET -1` 등 음수 리터럴이 지정된 경우 런타임 오류가 아니라 컴파일 타임에 즉각 거부해야 함.
- **에러 규격**:
  - LIMIT: `LIMIT must not be negative (SQLSTATE 2201W)`
  - OFFSET: `OFFSET must not be negative (SQLSTATE 2201X)`

### Invariant 5: 중첩 윈도우 함수 호출 원천 차단 (SQLSTATE 42P20)
- **배경 및 원칙**:
  - 윈도우 함수 내부(`OVER (PARTITION BY ... ORDER BY ...)`)에 또 다른 윈도우 함수(`ROW_NUMBER() OVER (...)`)를 중첩 호출하는 것은 ANSI SQL 표준상 영구 금지됨.
- **에러 규격**:
  - `window function calls cannot be nested (SQLSTATE 42P20)`

---

## 3. 구현 대상 및 변경 지점
1. `pkg/planner/validator.go`:
   - `validateQueryASTInternal`:
     - Invariant 1 (GROUP BY + ORDER BY 검증): `len(stmt.GroupBy) > 0`일 때 `stmt.OrderBy`의 각 항목에 대해 `ValidateProjectionUnderGroupBy(ob.Expr, resolvedGroupBy, false)` 검증 적용.
     - Invariant 2 (DISTINCT + ORDER BY 검증): `stmt.Distinct`일 때 `stmt.OrderBy` 항목이 `stmt.Projections`에 존재하는지 검증.
     - Invariant 3 (Subquery without alias): `stmt.From.Subquery != nil && stmt.From.Alias == ""` 및 `j.Table.Subquery != nil && j.Table.Alias == ""` 즉시 에러 반환.
     - Invariant 4 (Negative LIMIT/OFFSET): `stmt.LimitExpr` 및 `stmt.OffsetExpr`가 음수 정수 리터럴인지 검증.
     - Invariant 5 (Nested Window Functions): `CheckDisallowedExprs` 및 전용 검증기 `ValidateNoNestedWindowFunctions`로 윈도우 함수 내부 윈도우 함수 중첩 차단.
2. `pkg/planner/binder_test.go`:
   - 5대 불변식에 대한 독립 회귀 테스트 케이스 5종 추가 (`TestValidator_Hardening_*`).
3. 3-Stage 파이프라인 검증:
   - Build ➔ Test ➔ Commit & Push.
