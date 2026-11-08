# SQLLogicTest 확장 스위트(`evidence`, `index`, `random`) 4대 결함 원인 분석 및 완치 계획서 (`notes/SLT_EXTENDED_SUITE_FIXES_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **상태**: 완료 (Completed) — `SLT-FIX-1` ~ `SLT-FIX-4` 구현 완료, 단위/회귀 테스트 전수 PASS, `go test -race ./...` 100% 통과
> - **과제 식별 코드**: `SLT-FIX-1` ~ `SLT-FIX-4` (상위 과제: `GA-VALID-1` / `DURA-SLT-2`)
> - **관련 문서**: [`notes/GA_VALIDATION_HANDOFF_PLAN.ko.md`](GA_VALIDATION_HANDOFF_PLAN.ko.md), [`notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md`](SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md)

---

## 1. 배경 및 근본 원인 회고 (Why Did These 4 Code Patterns Exist?)

`GA-VALID-1` 1차 탐색에서 SQLite 공식 `evidence/*`, `index/*`, `random/*` 15개 스위트를 실행한 결과, 기존 `select1~5.test` (10,706 쿼리)와 10개 실전 오픈소스 애플리케이션(Gitea, Grafana, Metabase, Vaultwarden, Casdoor, Answer, Umami, Woodpecker, Vikunja, IOTA-SDK)에서는 한 번도 드러나지 않았던 4가지 사각지대가 도출되었습니다.

사람이 작성하는 SQL 및 ORM(Prisma, XORM, Diesel, Hibernate/JDBC)이 생성하는 쿼리와 **기계식 무작위 문법 생성기(Random Grammar Generator)**가 생성하는 쿼리 사이의 구조적 차이에서 기인한 각 항목의 발생 원인은 다음과 같습니다.

### 1.1 `SLT-FIX-1`: SLT 러너의 `onlyif` / `skipif` 조건부 지시자 무시 (`pkg/slt/parser.go`)
- **발생 배경**:
  - `pkg/slt/parser.go`는 `DURA-SLT-1` 초기 구축 당시 자체 작성한 `.slt` 스위트(`test/slt/suites/core/*.slt`)와 `select1~5.test`를 실행하기 위해 설계되었습니다.
  - `select1~5.test`에는 파일 상단 조건부 `halt`가 없었기 때문에, 당시 파서는 `onlyif <db>`와 `skipif <db>` 줄을 단순 무시(`continue`)하고 `halt`를 만나면 무조건 파싱을 중단하도록 작성되었습니다.
- **문제 현상**:
  - 공식 `evidence/in1.test`, `evidence/slt_lang_aggfunc.test` 등은 상단에 `onlyif mssql` + `halt` (또는 `skipif sqlite` + `halt`)를 두어 특정 DBMS에서만 조기 종료하도록 제어합니다.
  - 조건 지시자를 무시하고 `halt`만 실행하면 "특정 타겟에서만 멈추라"는 명령이 "무조건 멈추라"로 둔갑하여 초반 0~5문장만 실행되고 종료됩니다.

### 1.2 `SLT-FIX-2`: 중첩 괄호 `AND` 및 동일 컬럼 복합 범위 조건 덮어쓰기 (`pkg/planner/bounds.go`, `pkg/planner/planner.go`)
- **발생 배경**:
  - `SplitOnAnd` (`pkg/planner/bounds.go`)는 `BETWEEN a AND b`나 `(A OR B)` 내부의 `AND`를 잘못 쪼개지 않기 위해 의도적으로 최상위 괄호 깊이(`depth == 0`)에서만 `AND`를 분리하도록 설계되었습니다.
  - 일반 애플리케이션 쿼리는 `WHERE col3 > 7 AND col1 >= 9.96 AND col1 <= 2.20`처럼 평탄하게 작성하지만, 공식 `index/between/*` 스위트는 `WHERE col3 > 7 AND (col1 >= 9.96 AND col1 <= 2.20)`처럼 `AND`절을 불필요한 소괄호로 한 번 더 감싸서 생성합니다.
- **문제 현상**:
  1. `SplitOnAnd`가 `(col1 >= 9.96 AND col1 <= 2.20)`를 하나의 덩어리로 유지한 채 `ParseWhereConditions`로 넘기면, `ParseWhereConditions`는 바깥 괄호를 벗긴 뒤 `BinaryExpr(Op: TokenAnd)`를 마주치지만 비교 연산자(`=`, `>`, `>=`, `<`, `<=`)만 검사하여 해당 조건 전체를 인덱스 바운드에서 누락시켰습니다.
  2. 설령 분리되더라도 `ParseWhereConditions`가 동일 컬럼에 대해 여러 `MinVal`/`MaxVal`을 만날 때 기존 바운드와 교집합(더 좁은 범위)을 구하지 않고 마지막 값으로 단순 대입(`r.MinVal = val`)했습니다.
  3. 또한 `planner.go`에서 인덱스 스캔 노드를 생성한 뒤 잔여 필터(`remainingPrimary`)를 구성할 때, 인덱스 컬럼에 걸린 복합 범위 조건(`col1 >= 9.96 AND col1 <= 2.20`과 같은 모순 범위 포함)이 후단 `NodeFilter`에서 재검증되지 않거나 하한/상한이 유실되어 0행 대신 1행이 반환되었습니다.

### 1.3 `SLT-FIX-3`: `SELECT ALL`, 단항 `+` 연산자, `CAST(... AS SIGNED)`, `DIV` 정수 나눗셈 (`pkg/proc/`)
- **발생 배경**:
  - SQL 표준에서 `SELECT`의 기본 수량자는 `ALL`이므로, 사람이나 ORM은 절대 `SELECT ALL col1 FROM ...`이나 `+ + 38` 같은 중복 양수 부호를 작성하지 않습니다.
  - 반면 SLT `random/*` 스위트는 BNF 문법 트리를 난수로 순회하며 `[DISTINCT | ALL]`에서 `ALL`을 선택하고, `[+ | -]` 단항 연산자를 중첩 생성하며, MySQL/SQLite 호환 방언인 `20 DIV - - 96` 및 `CAST(90 AS SIGNED)`를 혼합 출력합니다.
- **문제 현상**:
  - `parseGeneralSelect` (`pkg/proc/parser_dml.go`)는 `DISTINCT`와 `TOP`만 소비하고 `ALL`을 처리하지 않아 `ALL`을 컬럼 식별자로 오인했습니다.
  - `parsePrefix` (`pkg/proc/parser_expr.go`)는 `TokenMinus`(`-`)만 전위 연산자로 등록하고 `TokenPlus`(`+`)는 중위 연산자로만 등록하여 `+ 39` 파싱이 실패했습니다.
  - `parseDataType` / `parseTypeSpec`은 `INT`, `INTEGER`, `BIGINT`만 지원하고 `SIGNED` / `UNSIGNED` 타입 별칭을 인식하지 못했으며, 중위 정수 나눗셈 연산자 `DIV`가 지원되지 않았습니다.

### 1.4 `SLT-FIX-4`: `GROUP BY` 수식 프로젝션 누락 및 `matchColName` 오탐 (`pkg/execution/aggregate.go`)
- **발생 배경**:
  - `AggregateIterator.parseFields` (`pkg/execution/aggregate.go`)에서 집계 함수가 아닌 일반 프로젝션 컬럼이 수식인지 판별할 때, 기존 코드는 `if strings.Contains(exprPart, "->") || strings.Contains(exprPart, "(")`만 검사했습니다.
  - 일반 실무 쿼리의 복합 프로젝션은 `COALESCE(SUM(x), 0)`이나 `CAST(a AS INT)`처럼 항상 함수 호출 괄호 `(`를 포함했기 때문에 통과했으나, `SELECT - tab1.col0 * 84 + + 38 AS col0 FROM tab1 GROUP BY tab1.col0`처럼 **괄호가 전혀 없는 순수 사칙연산 수식**은 `parsedExpr` 파싱을 건너뛰었습니다.
- **문제 현상**:
  - `parsedExpr`가 생성되지 않은 상태에서 `matchColName(k, f.argCol)` (`aggregate.go:97`)이 호출되면, `strings.LastIndex(b, ".")`가 `"- tab1.col0 * 84 + + 38"` 내부의 `tab1.` 접두사를 잘라내어 `"col0 * 84 + + 38"`과 `"col0"`을 비교하고 실패하여 최종적으로 `NULL`을 반환했습니다(만약 수식이 `"- tab1.col0"`이었다면 `b[dot+1:]`이 `"col0"`이 되어 부호가 무시된 원본 컬럼값이 반환되는 잠재 버그도 함께 존재했습니다).

---

## 2. 세부 수정 설계 (`SLT-FIX-1` ~ `SLT-FIX-4`)

### 2.1 `SLT-FIX-1` & SLT 러너 5대 기술부채 전면 해소 (`pkg/slt/`, `cmd/sqllogictest`)
1. **지시자 누적 평가 및 다중 `onlyif`/`skipif` OR 시맨틱스 지원**:
   - `skipNext` 단일 불리언 상태 머신의 한계를 제거하고, `currentOnlyIfs []string`, `currentSkipIfs []string` 지시자 누적 버퍼를 도입합니다.
   - SQLLogicTest 공식 명세에 따라 하나의 레코드 직전에 여러 개의 `onlyif`가 선언되면 그 중 하나라도 매칭될 때 실행(OR 로직)하며, `skipif`와 매칭되면 스킵 처리합니다.
   - 평가 후 레코드(`halt`, `statement`, `query`) 소비 시점에 지시자 버퍼를 명시적으로 클리어합니다.
2. **타겟 방언 설정 추상화 (`ParseOptions`)**:
   - `pkg/slt/parser.go`에 하드코딩되어 있던 타겟 방언 판별을 `ParseOptions{TargetDialects: ...}` 구조체로 분리하고 `ParseWithOptions`, `ParseFileWithOptions`를 제공합니다.
   - `cmd/sqllogictest`에 `-target` CLI 플래그(기본값: `"sqlite,lionsql"`)를 신설하여 런타임에 유연하게 설정 가능하도록 지원합니다.
3. **부동소수점 포맷 표준화 (`FormatCell`)**:
   - SQLite SQLLogicTest 공식 사양(`sqllogictest.c`)에 따라 실비교 대상 컬럼(`colType == 'R'`) 및 일반 부동소수점(`float64`/`float32`)을 `fmt.Sprintf("%.3f", v)`로 포맷팅하여 부동소수점 반올림 정합성을 일치시킵니다.
4. **`statement error` 에러 패턴 매칭 엔진**:
   - `statement error [expected_pattern]` 구문에서 지정된 예상 에러 패턴을 `Record.ExpectedError`에 파싱하고, `slt.RunRecord` 실행 시 실제 에러 메시지와 대소문자 무관 부분 문자열 매칭을 수행합니다.
5. **다중 컬럼 결과 파서 정밀 토크나이저 (`splitRowFields`)**:
   - 기존의 휴리스틱 슬라이싱 대신 탭(`\t`), 따옴표(`"..."`, `'...'`), 이스케이프 문자(`\'`, `\"`)를 안전하게 처리하는 문자 단위 토크나이저를 구현하여 공백 포함 문자열 셀 분할 결함을 완치합니다.

### 2.2 `SLT-FIX-2`: 플래너 중첩 `AND` 평탄화 및 복합 범위 바운드 교집합 (`pkg/planner/bounds.go`, `pkg/planner/planner.go`)
1. **AST 기반 중첩 `AND` 재귀 평탄화 (`bounds.go`)**:
   - `SplitOnAnd` 또는 `ParseWhereConditions`에서 각 조건 조각의 바깥 괄호를 제거한 후, AST 파싱 결과가 `*proc.BinaryExpr`이고 `Op == proc.TokenAnd`인 경우 좌/우 자식 노드를 재귀적으로 분리(`flattenAndExprs`)하여 `(col1 >= 9.96 AND col1 <= 2.20)`가 개별 원자 조건 `col1 >= 9.96`, `col1 <= 2.20`로 완벽히 분해되도록 합니다.
   - 또한 `9.96 <= col1`처럼 리터럴이 좌변에 오고 컬럼이 우변에 오는 역방향 비교식도 정규화(`col1 >= 9.96`)합니다.
2. **동일 컬럼 다중 하한/상한 병합 시 교집합(Tightening) 적용 (`bounds.go`)**:
   - 동일 컬럼에 이미 `r.HasMin == true`가 설정된 상태에서 새로운 하한(`>` 또는 `>=`)이 들어오면, 기존 `MinVal`과 새 값을 비교(수치 비교 우선, 불가 시 문자열 비교)하여 **더 큰(더 엄격한) 하한**을 채택합니다.
   - 동일 컬럼에 이미 `r.HasMax == true`가 설정된 상태에서 새로운 상한(`<` 또는 `<=`)이 들어오면, 기존 `MaxVal`과 새 값을 비교하여 **더 작은(더 엄격한) 상한**을 채택합니다.
3. **인덱스 스캔 시 복합/범위 조건의 잔여 필터 보존 (`planner.go`)**:
   - `planner.go`에서 단일 조건 `chosenCond`로 인덱스를 선택했더라도, `chosenCond`가 괄호로 묶인 복합 `AND` 수식이었거나 동일 컬럼에 상·하한 범위 조건이 함께 존재하는 경우(또는 모순된 상·하한 범위인 경우), 후단 `NodeFilter`(`remainingPrimary`)에서 정확히 재검증되도록 보존합니다.

### 2.3 `SLT-FIX-3`: `SELECT ALL`, 단항 `+`, `CAST(... AS SIGNED)`, `DIV` 연산자 지원 (`pkg/proc/`, `pkg/execution/`)
1. **`SELECT ALL` 수량자 (`pkg/proc/parser_dml.go`, `pkg/engine/sqlexec_select.go`)**:
   - `parseGeneralSelect` 초반의 `DISTINCT` / `TOP` 루프에서 `p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "ALL")`인 경우 `p.nextToken()`으로 소비합니다(`ALL`은 SQL 기본 동작이므로 AST 변형 불필요, 예약어 충돌 없음).
   - `sqlexec_select.go`의 `FromAST` 및 스칼라 `SELECT` 경로에서도 `SELECT ALL`이 정상 처리되는지 확인합니다.
2. **단항 `+` 연산자 (`pkg/proc/parser_expr.go`, `eval.go`, `evaluator.go`, `compiler.go`, `pkg/execution/eval.go`)**:
   - `parsePrefix`에 `case TokenPlus:`를 추가하여 `&UnaryExpr{Op: TokenPlus, Expr: p.parseExpression(precPrefix)}`를 반환합니다.
   - `pkg/proc/eval.go`, `pkg/execution/eval.go`, `pkg/proc/evaluator.go`, `pkg/proc/compiler.go`, `pkg/proc/fold.go`의 `*UnaryExpr` 평가 분기에 `TokenPlus`를 추가합니다(`NULL`이면 `NULL` 유지, 수치/문자열 수치면 수치 변환 또는 원본 반환).
3. **`CAST(... AS SIGNED [INTEGER])` 및 `UNSIGNED` (`pkg/proc/parser.go`, `pkg/proc/parser_expr.go`)**:
   - `parseDataType` (`parser.go`)의 `storage.TypeInt` 케이스에 `"SIGNED"`, `"UNSIGNED"`를 추가합니다.
   - `parseTypeSpec` (`parser_expr.go`)에서 `SIGNED` 또는 `UNSIGNED` 뒤에 선택적으로 `INTEGER` 또는 `INT`가 따라오는 경우(`CAST(x AS SIGNED INTEGER)`) 해당 토큰까지 함께 소비하고 `"INT"` 타입으로 매핑합니다.
4. **`DIV` 정수 나눗셈 연산자 (`pkg/proc/token.go`, `parser_expr.go`, `ast.go`, `eval.go`, `evaluator.go`, `compiler.go`, `pkg/execution/eval.go`)**:
   - `TokenDiv` (또는 문맥 기반 중위 `DIV` 연산자)를 곱셈/나눗셈 우선순위(`precProduct`)로 처리하여, 좌/우 피연산자를 정수로 나눈 몫(`int64(l) / int64(r)`, 0으로 나누기 시 `NULL` 반환)을 계산하도록 구현합니다. 일반 식별자 `div`(컬럼명/테이블명)와의 충돌을 막기 위해 중위 연산자 위치에서 대소문자 무관 `"DIV"` `TokenIdent`를 `TokenDiv` 이항 연산자로 파싱합니다.

### 2.4 `SLT-FIX-4`: `GROUP BY` 사칙연산 프로젝션 및 `matchColName` 방어 (`pkg/execution/aggregate.go`)
1. **괄호 없는 사칙연산 프로젝션 파싱 (`aggregate.go:290`)**:
   - 비집계 프로젝션 표현식의 `parsedExpr` 생성 조건을 기존 `strings.Contains(exprPart, "->") || strings.Contains(exprPart, "(")`에서 `strings.ContainsAny(exprPart, " +-*/%|") || strings.Contains(exprPart, "->") || strings.Contains(exprPart, "(")`로 확장하여 `- tab1.col0 * 84 + + 38` 같은 순수 대수식이 항상 AST로 파싱·평가되게 합니다.
2. **`matchColName` 수식 오탐 차단 (`aggregate.go:97`)**:
   - `matchColName(a, b)`에서 두 문자열이 정확히 같지 않은 경우(`!strings.EqualFold(a, b)`), `a` 또는 `b`에 연산자/공백/괄호(`" +-*/%|()"`)가 포함되어 있다면 테이블 접두사 `.` 제거(`b[dot+1:]`)를 수행하지 않고 즉시 `false`를 반환합니다. 이를 통해 `"- tab1.col0"` 같은 수식이 단순 그룹 키 `"tab1.col0"`으로 오판되는 현상을 원천 차단합니다.

---

## 3. 검증 계획 (3-Stage Verification Pipeline)

1. **단위 및 SLT 회귀 테스트 추가**:
   - `pkg/slt/parser_test.go`: `onlyif` / `skipif` 단일 및 중첩 지시자 + `halt` / `statement` / `query` 동작 검증.
   - `pkg/planner/planner_test.go`: 중첩 괄호 `AND` 범위 조건(`WHERE col3 > 7 AND (col1 >= 9.96 AND col1 <= 2.20)`) 및 역방향 비교식 바운드 병합 검증.
   - `test/slt/suites/core/`: `SELECT ALL`, 단항 `+`, `CAST(... AS SIGNED)`, `DIV`, `GROUP BY` 단항/사칙연산 프로젝션 쿼리 검증.
   - 공식 SQLite 스위트(`evidence/*`, `index/between/1/slt_good_0.test`, `random/*`) 재실행 검증.
2. **3단계 파이프라인 실행**:
   - Stage 1 (Build): `lionsql`, `bench`, `tpcc-runtime`, `sqllogictest` 전체 빌드.
   - Stage 2 (Test): `go test -tags wiredtiger -count=1 -v -race ./...` 전 패키지 100% PASS 및 0 data races 확인.
   - Stage 3 (Commit): Conventional Commits 포맷 준수 커밋.
