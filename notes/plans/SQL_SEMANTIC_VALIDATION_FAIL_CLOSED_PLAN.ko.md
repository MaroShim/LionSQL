# SQL 시맨틱 유효성 검증 Fail-Closed 아키텍처 및 구현 설계서

## 1. 개요 및 배경

`/tmp/sql.txt`에 보고된 세 가지 문제는 LionSQL의 SQL 시맨틱 유효성 검증(Semantic Validation)에서 문법적으로 거부되어야 하는 쿼리가 조용히 통과하여 잘못된 결과(임의 행 출력, 0건 출력)를 내거나 부정확한 에러를 반환하는 결함이다.

1. **Issue 1 (`SELECT id, count(*) FROM t;`)**: `GROUP BY` 절이 없을 때 집계 함수와 비집계 컬럼이 함께 프로젝션에 등장하면 조용히 첫 번째 행의 `id`와 집계 값을 반환함 (Fail-Closed 위반, MySQL 레거시 동작). ANSI SQL / PostgreSQL / SQL Server 표준에 따라 `ERROR: column "..." must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)`을 발생시켜야 함.
2. **Issue 2 (`SELECT count(*) FROM t GROUP BY 1;`)**: 서수 1이 `count(*)`로 치환된 후 `aggregate functions are not allowed in GROUP BY` 및 `invalid or unsupported SELECT syntax:` 에러로 반려됨. 프로젝션에 비집계 컬럼이 없고 집계/상수만 존재하는 경우 상수 `1`은 전체 행을 단일 그룹으로 묶는 상수 집계로 정상 실행(count = 3)되어야 하며, 비집계 컬럼이 혼재된 상태에서 집계 위치를 가리키는 서수인 경우 명확한 진단 에러(`cannot group by aggregate column position %d`)를 제공해야 함.
3. **Issue 3 (`WHERE val IN (SELECT id, val FROM t)`)**: 단일 컬럼 `val`과 비교하는 `IN (SELECT ...)` 서브쿼리가 다중 컬럼(2개)을 반환할 때, `decorrelateIn`이 컬럼 개수를 검증하지 않고 잘못된 세미 조인(`RightKey: t.id, val`)으로 변환하여 조용히 0건을 반환하거나, Volcano 런타임 평가에서 1번째 컬럼만 취하는 결함. 즉시 `ERROR: subquery must return only one column, returned 2 (SQLSTATE 42601)`로 Fail-Closed 반려해야 함.

---

## 2. 근본 원인 분석 (Root Cause Analysis)

### 2.1 Issue 1: `SELECT id, COUNT(*) FROM t;` 누락 검증
- **위치**: `pkg/execution/aggregate.go` (`AggregateIterator.Open`)
- **원인**:
  기존 코드는 `if len(a.groupKeys) > 0`일 때만 비집계 프로젝션 필드를 검사하도록 되어 있었음.
  `GROUP BY` 절이 없으면 `len(a.groupKeys) == 0`이 되므로, 비집계 컬럼 검증 블록이 통째로 건너뛰어짐.
  이로 인해 `AggregateIterator`는 전역 단일 그룹 어큐뮬레이터에 모든 행을 누적한 후, `isLiteral: true`인 필드 `id`에 대해 `acc.firstTuple`의 1번째 행 값을 가져와 `id=1, count=3`을 출력함.

### 2.2 Issue 2: `SELECT count(*) FROM t GROUP BY 1;` 오진단
- **위치**: `pkg/planner/planner.go` (line 320~335)
- **원인**:
  `GROUP BY`의 정수 리터럴 `1`을 1번째 프로젝션인 `count(*)`의 서수로 일률 치환함.
  `count(*)`는 집계 함수이므로 `hasTopLevelAggregateExpr`에 걸려 `aggregate functions are not allowed in GROUP BY`를 발생시키고, 상위 `sqlexec_select.go`에서 `invalid or unsupported SELECT syntax:`로 래핑되어 반환됨.
  T-SQL 및 SQL 표준에서 프로젝션이 집계 함수로만 구성된 경우 `GROUP BY <constant>`는 모든 행을 단일 그룹으로 묶는 상수 집계(Constant Grouping)의 의미를 가짐.

### 2.3 Issue 3: 다중 컬럼 서브쿼리 비교 침묵
- **위치**:
  1. `pkg/planner/decorrelate.go` (`decorrelateIn`): `subProj`가 `id, val`일 때 컬럼 개수를 검증하지 않고 `RightKey: subAlias + "." + subProj`(`t_sub2.id, val`)로 세미 조인을 생성. 해당 조인 키가 매칭되지 않아 결과가 0건으로 끝남.
  2. `pkg/execution/eval.go` 및 `pkg/proc/eval.go` (`*proc.InExpr` / `*proc.ScalarSubqueryExpr`): `execSubquery` 결과를 소비할 때 `len(cols) > 1`인지 검사하지 않고 `cols[0]`만 참조함.
  3. `pkg/engine/sqlexec_explain.go` (`resolveSubqueriesWithSession`): `execSubquery` 실패 시 에러를 전파하지 않고 `continue`로 건너뜀.

---

## 3. 수정 설계 및 구현 계획

### 3.1 Issue 1 수정 (`pkg/execution/aggregate.go`)
- `AggregateIterator.Open()`에서 `hasAggFuncs` 여부를 확인:
  ```go
  hasAggFuncs := false
  for _, f := range a.fields {
      if !f.isLiteral && f.fnType != aggGrouping {
          hasAggFuncs = true
          break
      }
  }
  if len(a.groupKeys) > 0 || hasAggFuncs {
      for _, f := range a.fields {
          exprToCheck := f.fnExpr
          if exprToCheck == nil {
              exprToCheck = f.parsed
          }
          if exprToCheck == nil && f.rawExpr != "" {
              exprToCheck = parseExprOrLiteral(f.rawExpr)
          }
          if exprToCheck != nil && !exprHasAgg(exprToCheck) {
              if err := validateHavingClause(exprToCheck, a.groupKeys, nil); err != nil {
                  return err
              }
          }
      }
  }
  ```
- `validateHavingClause`는 `a.groupKeys`가 비어있을 때 `*proc.ColumnRefExpr`을 만나면 `column "%s" must appear in the GROUP BY clause or be used in an aggregate function`을 반환하므로 완벽히 동작함.
- `pkg/wire/protocol.go`: `MUST APPEAR IN THE GROUP BY CLAUSE`를 PostgreSQL 표준 SQLSTATE `42803`으로 자동 매핑.

### 3.2 Issue 2 수정 (`pkg/planner/planner.go`, `pkg/proc/fold.go`)
- `pkg/proc/fold.go`: `IsConstantExpr(expr Expr) bool` 함수를 export하여 외부 패키지에서 재사용 가능하도록 함.
- `pkg/planner/planner.go`:
  ```go
  if lit, ok := g.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
      ord := int(lit.Val.IVal)
      hasNonAggTableCol := false
      for _, p := range stmt.Projections {
          if !hasTopLevelAggregateExpr(p.Expr) && !proc.IsConstantExpr(p.Expr) {
              hasNonAggTableCol = true
              break
          }
      }

      if !hasNonAggTableCol {
          // All projections are aggregates or constants: treat GROUP BY <constant> as a single-group constant key
          resolvedCol = g.String()
      } else {
          if ord < 1 || ord > len(stmt.Projections) {
              qErr = fmt.Errorf("GROUP BY position %d is not in select list", ord)
              break
          }
          proj := stmt.Projections[ord-1]
          if hasTopLevelAggregateExpr(proj.Expr) {
              qErr = fmt.Errorf("aggregate functions are not allowed in GROUP BY (cannot group by aggregate column position %d)", ord)
              break
          }
          resolvedCol = proj.Expr.String()
      }
  }
  ```
- 이로써:
  - `SELECT count(*) FROM t GROUP BY 1`: 단일 그룹 집계로 정상 실행되어 `count = 3` 반환.
  - `SELECT dept, count(*) FROM t GROUP BY 1`: `dept` 서수로 정상 그룹핑 (2개 그룹).
  - `SELECT dept, count(*) FROM t GROUP BY 2`: `cannot group by aggregate column position 2` 에러 반환.
  - `SELECT dept, count(*) FROM t GROUP BY 99`: `GROUP BY position 99 is not in select list` 에러 반환.

### 3.3 Issue 3 수정 (`pkg/planner/decorrelate.go`, `pkg/execution/eval.go`, `pkg/proc/eval.go`, `pkg/engine/sqlexec_explain.go`)
1. **`pkg/planner/decorrelate.go`**:
   `p.decorrelateIn`에서 `subProj`의 컬럼 수를 검증.
   ```go
   projCols := sqlutil.SplitTopLevel(subProj, ',')
   if len(projCols) > 1 {
       return fmt.Errorf("subquery must return only one column, returned %d (SQLSTATE 42601)", len(projCols))
   }
   ```
2. **`pkg/execution/eval.go`**:
   `case *proc.InExpr:`의 `e.Query != nil` 분기에서:
   ```go
   subRows, cols, err := execSubquery(e.Query.String())
   if err != nil {
       return err, true
   }
   if len(cols) > 1 {
       return fmt.Errorf("subquery must return only one column, returned %d (SQLSTATE 42601)", len(cols)), true
   }
   ```
3. **`pkg/proc/eval.go`**:
   - `*proc.InExpr`: `len(cols) > 1`일 때 에러 반환.
   - `*proc.ScalarSubqueryExpr`: `len(cols) > 1`일 때 에러 반환 (`subquery must return only one column, returned %d (SQLSTATE 42601)`).
4. **`pkg/wire/protocol.go`**:
   `SUBQUERY MUST RETURN ONLY ONE COLUMN` 또는 `SUBQUERY HAS TOO MANY COLUMNS`를 SQLSTATE `42601`로 자동 매핑.

---

## 4. 검증 계획 (Verification Plan)

### 4.1 단위 및 회귀 테스트 (`pkg/engine/sql_semantic_validation_test.go`)
- Test 1: `SELECT id, count(*) FROM t;` without `GROUP BY` -> SQLSTATE 42803 에러 발생 검증.
- Test 2: `SELECT 1, count(*) FROM t;` without `GROUP BY` -> 정상 실행 (상수 프로젝션 허용) 검증.
- Test 3: `SELECT count(*) FROM t GROUP BY 1;` -> count = 3 정상 실행 검증.
- Test 4: `SELECT dept, count(*) FROM t GROUP BY 2;` -> `aggregate functions are not allowed in GROUP BY` 에러 검증.
- Test 5: `SELECT * FROM t_sub1 WHERE val IN (SELECT id, val FROM t_sub2);` -> `subquery must return only one column, returned 2` (SQLSTATE 42601) 에러 검증.
- Test 6: `SELECT (SELECT id, val FROM t_sub2) FROM t_sub1;` -> 스칼라 서브쿼리 다중 컬럼 거부 검증.

### 4.2 3-Stage Pipeline 검증
1. **Build**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Test**: `go test -v -race ./pkg/engine/... ./pkg/execution/... ./pkg/planner/... ./pkg/proc/...`
3. **Oracle & SLT**:
   - `bash scripts/verify_no_slt_mutation.sh`
   - `./bin/sqllogictest -dir test/slt/core -parallel 16`
