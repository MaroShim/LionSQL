# LionSQL P0-31: RLS AST 격리 결합 및 sp_executesql Single-Pass 매개변수 치환 아키텍처 (`notes/P0_31_RLS_AST_ISOLATION_AND_DYNAMIC_SQL_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **도메인**: RLS (Row-Level Security), 동적 SQL (`sp_executesql`), 쿼리 파서(AST Parser), 테넌트 격리(Multi-Tenancy)
> - **참조 규칙**: `[RULE:ExecveSafety]`, `[RULE:LexicalSafety]`, `[RULE:RadicalKISS]`
> - **관련 과제**: `P0-31` (`pkg/api/rls.go`, `pkg/api/handlers_records.go`, `pkg/api/records_filter.go`, `pkg/engine/sqlexec_procs.go`)

---

## 1. 개요 (The Trap of String Interpolation)

다중 테넌트(Multi-Tenant) 데이터베이스 환경에서 **행 수준 보안(RLS, Row-Level Security)**과 **동적 매개변수 바인딩(`sp_executesql`)**은 테넌트 간 데이터 침범과 SQL 인젝션을 막는 절대적인 방화벽입니다.

그러나 기존 구현에는 **"문자열 조립(String Concatenation)"**에 의존하는 안티패턴이 잔류하여 두 가지 치명적인 보안 결함이 발생했습니다:
1. **RLS 문자열 괄호 탈출 인젝션**: 사용자가 조작된 쿼리 필터를 입력했을 때 테넌트 격리가 무력화되어 타 테넌트 데이터가 유출됨.
2. **동적 SQL 연쇄 이중 치환(Cascading Double Expansion)**: `@p1` 매개변수가 치환된 문자열 내에 우연히 `@p2`가 포함되어 있을 때, 후속 루프에서 연쇄적으로 2차 치환되어 데이터가 오염됨.

---

## 2. 결함의 본질 및 공격 시나리오

### 2.1. RLS 필터 문자열 조립 탈출 취약점
- 기존 코드:
  ```go
  combinedFilter := fmt.Sprintf("(%s) && (%s)", rlsRule, userFilter)
  ```
- 공격 시나리오:
  - 테넌트 격리 룰: `tenant_id = 'alice'`
  - 악의적인 사용자 필터: `secret_data = 'foo') || (1 = 1`
  - 조립된 최종 필터: `(tenant_id = 'alice') && (secret_data = 'foo') || (1 = 1)`
  - 연산자 우선순위에 의해 맨 뒤의 `|| (1 = 1)`이 참이 되면서, Bob이나 Charlie의 극비 데이터까지 전량 유출!
- 추가 원인: `records_filter.go`의 `ParseFilter()`가 토큰 파싱 후 맨 끝의 닫는 괄호 초과(`status = 'active')`)나 잔여 쓰레기 토큰을 검사하지 않고 무음 통과시킴.

### 2.2. `sp_executesql`의 Map 순회 다중 패스 연쇄 치환
- 기존 코드:
  ```go
  for paramName, val := range params {
      sql = strings.ReplaceAll(sql, paramName, valStr)
  }
  ```
- 공격 및 오류 시나리오:
  - SQL: `SELECT * FROM tbl WHERE a = @p1 AND b = @p2`
  - 매개변수: `@p1 = '@p2'`, `@p2 = '999'`
  - 1차 패스(`@p1` 치환): `SELECT * FROM tbl WHERE a = @p2 AND b = @p2`
  - 2차 패스(`@p2` 치환): `SELECT * FROM tbl WHERE a = 999 AND b = 999`
  - 원치 않게 `@p1` 자리까지 `999`로 오염되는 Cascading 치환 발생! 또한 Go의 `map` 순회 순서 비결정성으로 인해 실행할 때마다 결과가 달라지는 비결정적 버그 유발.

---

## 3. 해결책: 순수 AST 구조적 결합 & Single-Pass 선형 스캐너

### (1) RLS 순수 AST 구조적 결합 (`pkg/api/rls.go`, `handlers_records.go`)
- 문자열 조립(`fmt.Sprintf`)을 전면 폐기.
- RLS 규칙을 사전에 파싱하여 `FilterExpr` AST 노드로 확보(`GetListRuleExpr`).
- 클라이언트 필터도 파서 검증을 거친 독립 AST 노드로 확보.
- 두 AST를 루트 수준에서 **`&AndFilter{Exprs: []FilterExpr{rlsExpr, clientExpr}}`**로 구조적 결합.
- 클라이언트 문자열이 아무리 많은 괄호나 `||` 연산자를 포함하더라도, AST 트리 구조상 하위 서브트리에 완전히 격리되므로 테넌트 룰을 탈출하는 것이 수학적으로 불가능함!
- `ParseFilter` 끝단에 `if p.peek().typ != tokEOF { return nil, err }` 가드를 신설하여 문법 미완성 쿼리 즉각 거부.

### (2) $O(N)$ Single-Pass 선형 매개변수 스캐너 (`pkg/engine/sqlexec_procs.go`)
- 다중 패스 루프(`for range map`)를 완전히 제거.
- 원본 SQL 문자열을 앞에서부터 단 한 번만 훑는 **Single-Pass 상태머신 스캐너(`substituteParamsSinglePass`)** 도입:
  1. 따옴표 리터럴(`'...'`) 및 주석 구간을 건너뛰어 리터럴 내부의 `@param` 오치환 방지.
  2. 식별자 경계(`\b@param\b`)를 정확히 판별하여 `@p10`이 `@p1`에 의해 부분 절삭되는 문제 방지.
  3. 치환된 결과 문자열은 다시 스캔 대상에 포함되지 않고 출력 버퍼(`strings.Builder`)로만 직행하여 연쇄 치환(Cascading Expansion) 원천 차단.

---

## 4. TDD 회귀 검증 및 결론

- **RLS 인젝션 탈출 차단 테스트**: [`pkg/api/rls_test.go:TestRLS_Regression_P0_31_FilterInjectionBreakout`](file:///Users/maro/Projects/SQLion/pkg/api/rls_test.go) (PASS)
- **잔여 토큰 가드 테스트**: [`pkg/api/records_filter_test.go:TestRecordsFilter_InvalidSyntaxAndTrailingTokens`](file:///Users/maro/Projects/SQLion/pkg/api/records_filter_test.go) (PASS)
- **연쇄 치환 방지 테스트**: [`pkg/engine/proc_bind_test.go:TestBuildSpExecuteSQL_NoCascadingSubstitution`](file:///Users/maro/Projects/SQLion/pkg/engine/proc_bind_test.go) (PASS)

문자열 조작 안티패턴을 배격하고 정규 AST 구조화 및 선형 토크나이저로 전환함으로써, 멀티테넌트 환경의 보안성과 동적 SQL의 결정론적 신뢰성을 완벽하게 확립했습니다.
