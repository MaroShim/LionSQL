# LionSQL 엔진 버그 통합 수정 및 회귀 테스트 계획서
**문서 식별자**: `notes/20261118_BUG_REMEDIATION_PLAN.ko.md`  
**작성일시**: 2026-11-18  
**상태**: COMPLETED  
**관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`, `[RULE:PipelineVerification]`, `[RULE:ExplicitError]`, `[RULE:ZeroOracleTampering]`, `[RULE:ModularAbstraction]`

---

## 1. 개요 및 대상 버그 목록

본 계획서는 2026-11-18 기준 LionSQL 데이터베이스 엔진의 DML 조인 전파, 윈도우 슬라이딩 집계, HAVING 시맨틱 유효성 검증, T-SQL 내장 함수 호환성, 3-Valued Logic(3VL) 및 스토리지 타입 검증/정규화 전반의 핵심 결함을 해결하고 재발 방지 회귀 테스트 스위트를 구축하기 위한 통합 엔지니어링 실행 계획서이다.

| 번호 | 결함 ID | 도메인 | 심각도 | 핵심 결함 내용 | 상태 |
|---|---|---|---|---|---|
| **1** | `FIX-UPDATE-FROM-JOIN-PROPAGATION-1` | Engine / DML | **높음** | `UPDATE ... FROM JOIN` 및 `DELETE ... FROM JOIN` 구문에서 조인 조건이 실제 타깃 테이블에 반영되지 않고 0건 수정/삭제되는 결함 (Silent Failure) | **완료 (DONE)** |
| **2** | `FIX-WINDOW-AVG-SLIDING-FRAME-1` | Engine / Window | **보통** | `AVG(...) OVER (ORDER BY ...)` 슬라이딩/누적 프레임에서 윈도우 집계 평균이 아닌 현재 단건 행의 값이 반환되는 결함 | **완료 (DONE)** |
| **3** | `FIX-HAVING-NON-AGG-VALIDATION-1` | Engine / Semantic | **보통** | `HAVING` 절에 비집계 컬럼 및 GROUP BY 미포함 컬럼이 단독 사용될 때 컴파일 에러를 내지 않고 조용히 0 rows를 반환하는 시맨틱 검증 결함 | **완료 (DONE)** |
| **4** | `FIX-TSQL-LEN-TRAILING-SPACES-1` | Engine / Builtin | **보통** | T-SQL 공식 스펙상 `LEN()`은 후행 공백(Trailing Spaces)을 무시해야 하나, PG `LENGTH()`와 동일하게 공백을 포함하여 길이를 계산하는 결함 | **완료 (DONE)** |
| **5** | `FIX-TSQL-ISNULL-TRUNCATION-1` | Engine / Builtin | **낮음** | T-SQL 공식 스펙상 `ISNULL(check, repl)`은 첫 번째 인자의 데이터 타입과 길이에 고정(Truncate)되어야 하나, COALESCE처럼 긴 쪽으로 승격되는 결함 | **완료 (DONE)** |
| **6** | `FEAT-TYPE-VALIDATOR-AND-NORMALIZER-1` | Storage / Engine | **높음** | 컬럼 타입별 데이터 검증기 및 정규화 엔진 부재로 인한 제약 위반 (VARCHAR 길이 초과, DECIMAL 정밀도 오버플로, TINYINT 범위 초과, CHAR 고정 공백 패딩 누락) | **완료 (DONE)** |
| **7** | `FIX-3VL-AND-OR-NULL-LOGIC-1` | Engine / VDBE | **보통** | VDBE 바이트코드 `OpAnd`/`OpOr`에서 ANSI SQL 3-Valued Logic(3VL) 미준수로 `TRUE AND NULL`이 `false`로 평가되는 결함 | **완료 (DONE)** |
| **8** | `FIX-GROUPBY-ORDINAL-POSITION-1` | Planner / Engine | **높음** | `GROUP BY 1` 등 정수 위치 지정 시 SELECT 프로젝션 서수 컬럼이 아닌 상수 리터럴로 취급되어 전체 행이 1개 그룹으로 압축되는 결함 | **완료 (DONE)** |
| **9** | `FIX-GROUPBY-OUTPUT-ALIAS-1` | Planner / Engine | **보통** | `GROUP BY <alias>` 지정 시 출력 별칭을 해석하지 못하고 `nil`로 평가되어 전체 행이 1개 그룹으로 압축되는 결함 | **완료 (DONE)** |
| **10** | `FIX-SETOP-INTERSECT-PRECEDENCE-1` | Planner / SetOp | **높음** | 집합 연산자 결합 시 `INTERSECT`의 높은 우선순위가 무시되고 좌측 우선 순차 평가되어 잘못된 결과셋 도출 결함 | **완료 (DONE)** |
| **11** | `FIX-SQL-TRIM-BOTH-FROM-PARSING-1` | Engine / Parser | **보통** | ANSI SQL 표준 `TRIM([BOTH/LEADING/TRAILING] FROM)` 문법 미인식으로 식 전체가 원문 문자열 값으로 반환되는 결함 | **완료 (DONE)** |
| **12** | `FIX-WINDOW-FIRST-LAST-VALUE-1` | Execution / Window | **높음** | 윈도우 함수 `FIRST_VALUE(...) OVER (...)`, `LAST_VALUE(...) OVER (...)` 실행기 미구현으로 조용히 `NULL` 반환 결함 | **완료 (DONE)** |
| **13** | `FIX-SUBSTRING-TWO-ARGUMENTS-1` | Engine / Builtin | **보통** | `SUBSTRING(str, pos)` 2인자 표준 호출 시 길이 생략을 처리하지 못하고 조용히 `NULL`을 반환하는 결함 | **완료 (DONE)** |
| **14** | `FIX-OPEN-ERROR-CURSOR-LEAK-1` | Execution / Storage | **치명적** | Volcano 및 집계 실행기 `Open()` 중 에러 발생 시 기오픈된 자식 이터레이터/스토리지 커서가 미정리되어 테이블 잠금 및 핸들 누수 발생 결함 | **완료 (DONE)** |
| **15** | `FIX-SUBQUERY-WILDCARD-PROJECTION-LEAK-1` | Planner / Projection | **높음** | IN / NOT IN 서브쿼리 사용 시 SEMI/ANTI 조인 릴레이션의 컬럼이 SELECT * 와일드카드 프로젝션에 강제 누출되는 결함 | **완료 (DONE)** |
| **16** | `FIX-GROUPBY-INVALID-COLUMN-VALIDATION-1` | Planner / Execution | **치명적** | GROUP BY 절에 테이블에 존재하지 않는 비유효 컬럼 지정 시 컴파일 에러 없이 조용히 전 행이 1개 그룹으로 뭉개지는 결함 | **완료 (DONE)** |
| **17** | `FIX-CTE-DML-UPDATABLE-TARGET-1` | Engine / DML | **보통** | WITH CTE AS (...) DELETE/UPDATE 실행 시 CTE 임시 뷰 미인식 및 원본 테이블 갱신/삭제 실패 결함 | **완료 (DONE)** |
| **18** | `FIX-OPEN-ERROR-ITERATOR-LEAK-ALL-1` | Execution / Storage | **치명적** | Volcano 반복자 전반(Window, Sort) Open 에러 시 하위 커서 미정리 누수 결함 | **완료 (DONE)** |
| **19** | `FIX-WINDOW-INVALID-COLUMN-VALIDATION-1` | Execution / Window | **보통** | 윈도우 함수 PARTITION BY / ORDER BY에 존재하지 않는 컬럼 지정 시 사일런트 뭉개짐 결함 | **완료 (DONE)** |
| **20** | `FIX-COMPOSITE-PK-DML-OVERWRITE-1` | Engine / DML | **치명적** | 복합 기본키(Composite PK) 테이블 DML(CTE, JOIN UPDATE/DELETE) 실행 시 엉뚱한 행 덮어쓰기 및 데이터 오염 결함 (Data Corruption) | **완료 (DONE)** |
| **21** | `FIX-WINDOW-ARG-INVALID-COLUMN-1` | Execution / Window | **보통** | 윈도우 함수 내부 인자 표현식(예: `AVG(bad_col) OVER (...)`)에 비유효 컬럼 지정 시 사일런트 NULL 반환 결함 | **완료 (DONE)** |
| **22** | `FIX-ORDERBY-INVALID-COLUMN-AND-ORDINAL-1` | Planner / Execution | **보통** | ORDER BY 절 비유효 컬럼 및 범위 초과 서수(Ordinal 0 또는 프로젝션 수 초과) 지정 시 사일런트 무시 결함 | **완료 (DONE)** |
| **23** | `FIX-SUBSTRING-NONPOSITIVE-POS-1` | Engine / Builtin | **보통** | `SUBSTRING(str, pos)` 2인자 호출 시 `pos <= 0`일 때 문자열 끝부분이 슬라이싱 길이 공식 오류로 절삭되는 결함 | **완료 (DONE)** |
| **24** | `FIX-STR-COMPARE-CANONICAL-STRIP-1` | Execution / Eval | **치명적** | 문자열 값 비교 시 `CanonicalIdent` 대괄호(`[...]`)/따옴표 무단 제거로 `'[ADMIN]' = 'ADMIN'` 오매칭 결함 (Data Distortion) | **완료 (DONE)** |
| **25** | `FIX-SP-NULL-EQUAL-3VL-1` | Engine / SP | **치명적** | 저장 프로시저(SP) 내 NULL 비교 연산 시 `Value.Equal`의 스토리지 레벨 true 반환으로 인해 `IF @a = @b`가 TRUE 평가되는 ANSI 3VL 파괴 결함 | **완료 (DONE)** |
| **26** | `FIX-DIV-BY-ZERO-ERROR-VISIBILITY-1` | Engine / VDBE | **보통** | 나눗셈 연산자(`/`)의 0 나누기 시 `division by zero` 에러 은폐 및 사일런트 NULL 반환 결함 | **완료 (DONE)** |
| **27** | `FIX-DATEDIFF-BOUNDARY-CROSSING-1` | Engine / Builtin | **보통** | T-SQL 공식 스펙상 `DATEDIFF`의 단위 경계선(Boundary) 통과 횟수 계산 스펙 위반 결함 (단순 경과 시간 나눗셈으로 인한 0 반환) | **완료 (DONE)** |
| **28** | `FIX-ARITHMETIC-OVERFLOW-DETECTION-1` | Execution / Eval | **보통** | 정수 곱셈/덧셈 산술 연산 시 64비트(BIGINT) 최댓값 초과 오버플로 미검출 및 음수 래핑(`-2`) 결함 | **완료 (DONE)** |
| **29** | `FIX-WT-INDEX-COMPOSITE-PK-SCAN-1` | Storage / WiredTiger | **치명적** | WiredTiger 보조 인덱스 스캔(`wtIndexBTreeCursor`) 시 복합 PK 파싱 실패(`ParsePKValue`)로 조회 0건 증발 결함 | **완료 (DONE)** |
| **30** | `FIX-WT-FK-COMPOSITE-PK-CASCADE-1` | Storage / WiredTiger | **치명적** | WiredTiger 외래키 `ON DELETE CASCADE / SET NULL` 시 복합 PK 자식 키 추출 누락으로 고아 레코드 잔존 결함 | **완료 (DONE)** |
| **31** | `FIX-MERGE-INTO-COMPOSITE-PK-1` | Engine / DML | **높음** | `MERGE INTO` 구문에서 복합 PK 타깃 테이블 매칭/갱신/삭제 시 첫 번째 컬럼만 추출하여 오동작하는 결함 | **완료 (DONE)** |
| **32** | `FIX-PITR-WAL-COMPOSITE-PK-1` | Storage / PITR | **높음** | PITR(시점 복구) WAL 재생 시 복합 PK 테이블 `UPDATE/DELETE` 복구 유실 결함 | **완료 (DONE)** |
| **33** | `FIX-HASH-JOIN-KEY-CANONICAL-STRIP-1` | Execution / Join | **치명적** | Hash Join 조인 키 생성기(`formatKeyVal`)의 `CanonicalIdent` 호출로 인한 대괄호/따옴표 제거 및 이종 문자열 오조인 결함 | **완료 (DONE)** |
| **34** | `FIX-SETOP-OPEN-ERROR-CURSOR-LEAK-1` | Execution / SetOp | **높음** | `SetOpIterator.Open()` 우측 자식 Materialization 에러 시 양쪽 커서 누수 결함 | **완료 (DONE)** |
| **35** | `FIX-MERGE-JOIN-OPEN-ERROR-CURSOR-LEAK-1` | Execution / Join | **높음** | `MergeJoinIterator.Open()` 초기 튜플 Pull 에러 시 양쪽 자식 커서 누수 결함 | **완료 (DONE)** |

---

## 2. 결함별 근본 원인 분석 및 상세 수정 아키텍처

### 2.1 결함 1: `UPDATE / DELETE ... FROM JOIN` 조인 결과 타깃 테이블 반영 결함 (`FIX-UPDATE-FROM-JOIN-PROPAGATION-1`)

#### 1) 근본 원인
1. **DML 파서의 FROM 절 무단 절삭 및 유실**:
   - `pkg/engine/dml_parser.go:274`의 `splitOutputWhere` 함수에서 `fromIdx := sqlutil.FindTopLevelKeyword(head, "FROM")` 감지 시 `head[:fromIdx]`로 잘라내어 `FROM` 절 이하 모든 조인 토큰을 완전히 폐기함.
   - `pkg/proc/parser_dml.go:224`(`parseUpdate`) 및 `line 290`(`parseDelete`)에서 `p.curTokenIs(TokenFrom)` 감지 시 단순히 토큰을 루프로 건너뛰어 AST 노드(`UpdateStmt`, `DeleteStmt`, `UpdateWhereStmt`, `DeleteWhereStmt`)에 FROM 절을 전혀 보관하지 않음.
2. **실행기 라우팅 바이패스 및 WHERE 바인딩 불능**:
   - `pkg/engine/sqlexec_dml.go:1087`(`execUpdate`) 및 `line 1341`(`execDelete`)에서 `OUTPUT`이나 `RETURNING`이 없으면 단일 테이블 VDBE(`OpUpdateWhere`/`OpDeleteWhere`)로 쿼리를 보냄.
   - VDBE는 `FROM` 절을 인지하지 못한 채 타깃 테이블 단독 스캔을 수행하며, WHERE 절 내의 조인 테이블 컬럼(예: `_b_del_cust.tier = 'VIP'`)을 평가할 때 해당 컬럼이 타깃 테이블에 없으므로 `nil`로 평가되어 조건 불일치(0건 수정/삭제)가 조용히 발생함.

#### 2) 수정 아키텍처
1. **AST 및 DML 파서 확장**:
   - `pkg/engine/dml_parser.go`: `DMLStatement` 구조체에 `From string` 필드 추가. `splitOutputWhere` 및 `parseUpdateBody`, `parseDeleteBody`에서 `FROM ...` 절을 보존 및 추출.
   - `pkg/proc/ast.go`: `UpdateStmt`, `UpdateWhereStmt`, `DeleteStmt`, `DeleteWhereStmt`에 `From string` 필드 추가.
   - `pkg/proc/parser_dml.go`: `FROM` 토큰 이후의 조인 텍스트를 파싱하여 AST 노드에 저장.
2. **FROM 절 존재 시 단일 테이블 VDBE 바이패스 및 조인 실행기 (`execUpdateFromJoin`, `execDeleteFromJoin`) 분기**:
   - `execUpdate` / `execDelete`: `dml.From != ""`인 경우 VDBE 단일 테이블 엔진을 바이패스하고 조인 전용 파이프라인으로 직결.
   - **드라이빙 SELECT 쿼리 합성**:
     - 타깃 테이블의 PK 컬럼(`targetPK`) 식별.
     - 타깃 테이블 별칭(`targetRef`) 식별.
     - `UPDATE`: `SELECT <targetRef>.<targetPK> AS __lionsql_pk__, (<assignment.Value>) AS __lionsql_upd_0__, ... FROM <dml.From> WHERE <dml.Where>`
     - `DELETE`: `SELECT <targetRef>.<targetPK> AS __lionsql_pk__ FROM <dml.From> WHERE <dml.Where>`
     - 합성된 SELECT 쿼리를 기존 `s.execSelect`를 통해 트랜잭션 문맥 내에서 실행하여 조인 결과셋 도출.
   - **타깃 테이블 배치 적용**:
     - 호출자의 트랜잭션 핸들 `tx`를 사용하여 `__lionsql_pk__`를 기반으로 타깃 레코드를 검색(`tx.Get`), 필드 갱신(`tx.Update`) 또는 삭제(`tx.Delete`) 수행.
     - 트랜잭션 무결성 및 인덱스 정합성 완벽 유지.

---

### 2.2 결함 2: `AVG(...) OVER (ORDER BY ...)` 슬라이딩/누적 프레임 단건 값 계산 결함 (`FIX-WINDOW-AVG-SLIDING-FRAME-1`)

#### 1) 근본 원인
1. **`evalRowFunction` 후보 키 누락 및 스칼라 단건 폴백**:
   - `pkg/proc/eval.go:1220`에서 `fn.HasOver == true`일 때 `WindowIterator`가 계산하여 튜플에 주입한 윈도우 결과 컬럼명(`fn.String()`, `fn.OverClause`)을 `candidates` 맵에 등록하지 않음.
   - `pkg/proc/eval.go:1259`의 집계 함수 기본 폴백(`return EvalRowExpr(args[0], row)`)으로 빠져서, 윈도우 집계 평균 대신 **현재 행의 단건 인자 값**(`args[0]`)을 그대로 반환함.
2. **프로젝션 이터레이터의 조기 스칼라 평가 간섭**:
   - `pkg/execution/project.go:245`에서 튜플에 이미 계산된 윈도우 컬럼 값(`tuple.GetOK(f.alias)`)을 확인하기 전에 `f.parsed != nil`을 먼저 평가하여 스칼라 단건 폴백이 윈도우 결과를 덮어씀.

#### 2) 수정 아키텍처
1. `pkg/proc/eval.go`:
   - `fn.HasOver == true`인 경우 `candidates`에 `fn.String()`, `fn.Name + " OVER (" + fn.OverClause + ")"` 등을 추가하여 튜플 내 계산 결과 우선 조회.
   - 윈도우 함수(`fn.HasOver == true`)인 경우 `EvalRowExpr(args[0], row)` 단건 폴백 진입을 완전히 차단.
2. `pkg/execution/project.go`:
   - 프로젝션 평가 시 `tuple.GetOK(f.alias)` 또는 `tuple.GetOK(f.rawExpr)`를 최우선 조회하여 기계산된 윈도우 집계 값을 온전히 프로젝션.

---

### 2.3 결함 3: `HAVING` 절 비집계 컬럼 시맨틱 검증 누락 (`FIX-HAVING-NON-AGG-VALIDATION-1`)

#### 1) 근본 원인
- 표준 SQL, T-SQL, PostgreSQL 규격상 `HAVING` 절에 등장하는 컬럼은 반드시 `GROUP BY` 목록에 포함되어 있거나, `SUM()`, `COUNT()` 등 집계 함수로 래핑되어 있어야 함.
- LionSQL의 쿼리 플래너(`pkg/planner/planner.go`) 및 집계 이터레이터(`pkg/execution/aggregate.go`)는 `HAVING` 조건의 AST를 파싱할 때 비집계 컬럼이 `GROUP BY` 키에 속해 있는지 검증하지 않음.
- 그룹별 평가 맵에는 `GROUP BY` 키와 집계 결과만 존재하므로, 비집계 컬럼(`salary`)은 `nil`로 평가되어 `nil > 100` -> `false`로 처리되어 조용히 0건의 행을 반환함.

#### 2) 수정 아키텍처
1. **HAVING 시맨틱 검증기 (`validateHavingClause`) 구현**:
   - `pkg/planner/planner.go` 및 `pkg/execution/aggregate.go`에서 `havingCond` AST 트리를 재귀 순회.
   - `proc.FunctionCallExpr` 중 집계 함수(`COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, `STRING_AGG` 등) 내부의 인자는 집계 컨텍스트이므로 검증 통과.
   - 집계 함수 외부에 노출된 `proc.ColumnRefExpr`에 대해:
     - 컬럼명(및 테이블 접두사 제거 컬럼명)이 `groupKeys` 목록에 존재하는지 확인.
     - 존재하지 않는 경우 즉시 명시적 시맨틱 컴파일 에러 반환:
       `fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function", colRef.Name)`
2. **`[RULE:ExplicitError]` 준수**:
   - 컴파일 타임 및 쿼리 플래닝 시점에 즉시 에러를 반환하여 잘못된 쿼리가 조용히 실패하는 현상 원천 차단.

---

### 2.4 결함 4: T-SQL `LEN()` 후행 공백(Trailing Spaces) 계산 오류 (`FIX-TSQL-LEN-TRAILING-SPACES-1`)

#### 1) 근본 원인
- `pkg/proc/builtins_string.go:320`에서 `LEN`과 `LENGTH`가 하나의 `case` 문으로 묶여 있음:
  ```go
  case "LEN", "LENGTH":
      if len(args) == 0 || args[0].IsNull {
          return storage.NullValue(storage.TypeInt)
      }
      return storage.IntValue(int64(len([]rune(valToString(args[0])))))
  ```
- T-SQL 공식 스펙: `LEN()`은 문자열 끝의 후행 공백(ASCII 0x20 `' '`)을 무시해야 함. (선행 공백은 포함).
- PostgreSQL 공식 스펙: `LENGTH()`는 후행 공백을 포함한 전체 문자 수를 반환해야 함.

#### 2) 수정 아키텍처
- `pkg/proc/builtins_string.go`에서 `LEN`과 `LENGTH`의 분기를 분리:
  ```go
  case "LEN":
      if len(args) == 0 || args[0].IsNull {
          return storage.NullValue(storage.TypeInt)
      }
      s := strings.TrimRight(valToString(args[0]), " ")
      return storage.IntValue(int64(len([]rune(s))))

  case "LENGTH":
      if len(args) == 0 || args[0].IsNull {
          return storage.NullValue(storage.TypeInt)
      }
      return storage.IntValue(int64(len([]rune(valToString(args[0])))))
  ```
- 유니코드 rune 처리를 온전히 유지하면서 T-SQL과 PGWire 각각의 규격 100% 만족.

---

### 2.5 결함 5: T-SQL `ISNULL()` 첫 번째 인자 길이 고정(Truncation) 결함 (`FIX-TSQL-ISNULL-TRUNCATION-1`)

#### 1) 근본 원인
- `pkg/proc/builtins_sys.go:183` 및 `pkg/proc/eval.go:1281`에서 `ISNULL`이 `COALESCE`와 100% 동일하게 매핑되어 있음.
- T-SQL 공식 스펙: `ISNULL(check_expression, replacement_value)`의 반환 데이터 타입과 길이는 **무조건 첫 번째 인자의 데이터 타입과 길이**에 종속됨.
  - 첫 번째 인자가 `VARCHAR(3)`이면 대체 값이 `'abcdefgh'`라도 `'abc'`로 잘려서(Truncated) 반환되어야 함.
  - 반면 `COALESCE`는 ANSI SQL 데이터 타입 우선순위(Type Precedence) 규칙에 따라 더 긴 쪽으로 승격됨.
- LionSQL의 `DeclareStmt` 및 `storage.Value`가 길이 정보(`Precision`)를 보존하지 않아 타입 길이에 따른 절삭이 누락됨.

#### 2) 수정 아키텍처
1. `pkg/proc/ast.go` & `pkg/proc/parser_stmt.go`:
   - `DeclareStmt`에 `Precision int` 필드 추가.
   - `DECLARE @var VARCHAR(n)` 구문 분석 시 괄호 안의 길이 `n`을 파싱하여 `Precision`에 저장.
2. `pkg/storage/engine.go`:
   - `storage.Value` 구조체에 `Precision int` 필드 추가.
3. `pkg/proc/evaluator.go`, `pkg/proc/compiler.go`, `pkg/proc/runtime/vm_dispatcher.go`:
   - 변수 선언 시 `storage.Value{Type: dt, Precision: prec, IsNull: true}`로 정밀도 보존.
4. `pkg/proc/builtins_sys.go` & `pkg/proc/eval.go`:
   - `ISNULL` 평가 시 첫 번째 인자가 NULL인 경우, 두 번째 인자의 문자열 길이가 첫 번째 인자의 `Precision`을 초과하면 `string([]rune(repl)[:prec])`로 Truncation 적용 후 반환.
   - 비문자열 타입의 경우 첫 번째 인자의 타입으로 형변환 수행.

---

### 2.6 결함 6: 컬럼 타입별 데이터 검증기 및 정규화 엔진 (`FEAT-TYPE-VALIDATOR-AND-NORMALIZER-1`)

#### 1) 근본 원인
- `pkg/proc/parser_ddl.go:1244`에서 `dt == storage.TypeVarChar` 파싱 시 `else { prec = 0; scale = 0; }` 분기로 인해 `VARCHAR(3)`의 길이(`prec=3`) 및 `CHAR(5)`의 길이(`prec=5`)가 0으로 덮어써져 유실됨.
- 테이블 DDL 및 스토리지 컬럼 메타데이터(`storage.Column`)에 원시 타입명(`RawType`: `TINYINT`, `SMALLINT`, `INT`, `BIGINT`, `CHAR`) 및 고정 길이 여부(`IsFixed`)가 보존되지 않음.
- 스토리지 엔진(`memengine`, `wiredtiger`) 쓰기 파이프라인(`Insert`, `InsertRow`, `Update`, `UpdateRow`)에 공통 타입 검증기(Type Validator & Normalizer)가 부재하여:
  - `VARCHAR(3)`에 10글자가 에러 없이 그대로 삽입됨.
  - `DECIMAL(5,2)`에 허용 정수 자릿수(5-2=3자리, 최대 999.99)를 초과하는 `9999999.99`가 오버플로 검증 없이 그대로 저장됨.
  - `TINYINT`에 부호 없는 8비트 허용 범위(0 ~ 255)를 초과하는 `999`가 그대로 저장됨.
  - `CHAR(5)`에 `'abc'` 저장 시 고정 5자리 공백 패딩(`'abc  '`)이 누락되어 3자리로 저장됨.

#### 2) 수정 아키텍처
1. **메타데이터 보존**:
   - `storage.Column` 및 `proc.ColumnDef`에 `RawType string`, `IsFixed bool` 추가.
   - `pkg/proc/parser_ddl.go`: `VARCHAR(n)`, `CHAR(n)`, `DECIMAL(p, s)`, `TINYINT` 파싱 시 `prec`, `scale`, `rawType`, `isFixed` 보존.
2. **중앙 타입 검증 및 정규화 모듈 (`pkg/storage/type_validator.go`) 구현**:
   - `ValidateAndNormalizeValue(col *Column, val Value, tableName string) (Value, error)`
   - `ValidateAndNormalizeRecord(schema *TableSchema, rec Record) (Record, error)`
   - `ValidateAndNormalizeRow(schema *TableSchema, row Row) (Row, error)`
   - **VARCHAR(n)**: 문자 수(`utf8.RuneCountInString`)가 `col.Precision` 초과 시 `string or binary data would be truncated` 에러 반환 (`[RULE:ExplicitError]`).
   - **CHAR(n)**: 문자 수가 `col.Precision` 미만인 경우 우측 공백 패딩(`strings.Repeat(" ", col.Precision - charLen)`)으로 5자리 정규화.
   - **DECIMAL(p, s)**: 정수부 자릿수가 `p - s` 초과 시 `arithmetic overflow converting numeric to data type numeric` 에러 반환. 소수부는 $s$ 자리로 정확히 Rescale.
   - **TINYINT**: 정수 값이 `0 ~ 255` 범위를 벗어날 경우 `arithmetic overflow converting to tinyint: value out of range [0, 255]` 에러 반환.
3. **스토리지 엔진 트랜잭션 계층 직결**:
   - `memengine` 및 `wiredtiger`의 `Insert`, `InsertRow`, `Update`, `UpdateRow` 진입점에서 무조건 `ValidateAndNormalize`를 수행하여 모든 SQL, VDBE, REST, Wire 경로의 데이터 무결성을 100% 보장.

### 2.7 결함 7: VDBE VM `OpAnd`/`OpOr` 3-Valued Logic(3VL) NULL 평가 결함 (`FIX-3VL-AND-OR-NULL-LOGIC-1`)

#### 1) 근본 원인
- `pkg/proc/runtime/vm_dispatcher.go:459`에서 VDBE 가상머신의 논리 연산자 바이트코드 실행기가 순수 2진 Go boolean(`&&`, `||`)으로 직결되어 있었음:
  ```go
  case proc.OpAnd:
      f.regs[inst.A] = storage.BoolValue(isTruthy(f.regs[inst.B]) && isTruthy(f.regs[inst.C]))
  case proc.OpOr:
      f.regs[inst.A] = storage.BoolValue(isTruthy(f.regs[inst.B]) || isTruthy(f.regs[inst.C]))
  ```
- `isTruthy(NULL)`은 `false`를 반환하므로:
  - `TRUE AND NULL` 실행 시 `true && false` -> `false` 반환 (ANSI SQL 규격: `NULL`)
  - `NULL AND NULL` 실행 시 `false && false` -> `false` 반환 (ANSI SQL 규격: `NULL`)
  - `NULL OR FALSE` 실행 시 `false || false` -> `false` 반환 (ANSI SQL 규격: `NULL`)

#### 2) 수정 아키텍처
- `pkg/proc/runtime/vm_dispatcher.go`: ANSI SQL 3-Valued Logic(3VL) 정밀 매핑:
  - **AND (OpAnd)**:
    - 한쪽이라도 `false`(`!v.IsNull && !isTruthy(v)`)이면 결과는 무조건 `false`.
    - 어느 한쪽이라도 `NULL`이면 결과는 `NULL` (`storage.NullValue(storage.TypeBool)`).
    - 둘 다 non-null truthy이면 `true`.
  - **OR (OpOr)**:
    - 한쪽이라도 `true`(`!v.IsNull && isTruthy(v)`)이면 결과는 무조건 `true`.
    - 어느 한쪽이라도 `NULL`이면 결과는 `NULL` (`storage.NullValue(storage.TypeBool)`).
    - 둘 다 non-null falsy이면 `false`.

---

### 2.8 결함 8 & 9: `GROUP BY` 서수 위치(Ordinal) 및 프로젝션 별칭(Alias) 미해석 결함 (`FIX-GROUPBY-ORDINAL-POSITION-1`, `FIX-GROUPBY-OUTPUT-ALIAS-1`)

#### 1) 근본 원인
- `GROUP BY 1, 2` 구문 파싱 시 `1`이 정수 리터럴로 컴파일되어 모든 행에서 동일한 상수 값 `1`로 평가됨. 결과적으로 테이블의 모든 행이 단 1개의 그룹으로 묶이고 집계(`COUNT(*)`)가 전체 행 개수로 합쳐지는 치명적 결함 발생.
- `GROUP BY <alias>` 구문 사용 시 플래너와 집계기가 출력 별칭을 기본 테이블 컬럼 목록에서만 조회하여 실패(`nil`), 모든 행이 `nil` 그룹으로 묶여 1개 행으로 왜곡됨.

#### 2) 수정 아키텍처
- `pkg/planner/planner.go` 및 `pkg/execution/aggregate.go`:
  - `GROUP BY` 항목이 정수 리터럴(`1`, `2`, ...)인 경우, 프로젝션 리스트의 1-기반 인덱스 표현식(`projections[idx-1]`)으로 치환.
  - `GROUP BY` 항목이 식별자인 경우, 프로젝션의 별칭 목록(`aliasMap`)과 먼저 대조하여 일치하는 프로젝션 표현식으로 바인딩.

---

### 2.9 결함 10: 집합 연산자 `INTERSECT` 우선순위(Precedence) 플래닝 결함 (`FIX-SETOP-INTERSECT-PRECEDENCE-1`)

#### 1) 근본 원인
- `pkg/planner/setop.go:243`의 `BuildSetOpPlan`에서 집합 연산 브랜치들을 좌측부터 단순 순차 결합(`Left-Deep PlanNode`)함.
- `A UNION B INTERSECT C`를 `(A UNION B) INTERSECT C`로 평가하여, ANSI SQL-92 및 PostgreSQL 표준(`A UNION (B INTERSECT C)`)을 위반하고 잘못된 행 집합을 반환함.

#### 2) 수정 아키텍처
- `pkg/planner/setop.go`:
  - 집합 연산자 우선순위 트리를 2계층으로 파싱/조립:
    - 1차: `INTERSECT` 연산자를 우선적으로 묶어 서브트리 노드(`NodeSetOp: INTERSECT`) 생성.
    - 2차: 상위 레벨에서 `UNION` / `UNION ALL` / `EXCEPT`를 결합.

---

### 2.10 결함 11: ANSI SQL `TRIM(BOTH/LEADING/TRAILING FROM)` 문법 미인식 결함 (`FIX-SQL-TRIM-BOTH-FROM-PARSING-1`)

#### 1) 근본 원인
- 파서가 `TRIM(BOTH 'x' FROM '...')`와 같은 표준 키워드 인자를 함수 호출로 파싱하지 못하고 리터럴 표현식으로 폴백하여 식 원문 문자열(`"TRIM(...)"`)을 컬럼 값으로 그대로 반환함.

#### 2) 수정 아키텍처
- `pkg/proc/parser_expr.go` 또는 `pkg/sqlutil`:
  - `TRIM` 전용 파서에서 `BOTH`, `LEADING`, `TRAILING` 및 `FROM` 키워드를 감지하여 타깃 문자열과 제거할 문자 집합을 표준 `TRIM/LTRIM/RTRIM` AST 함수로 정규화 변환.

---

### 2.11 결함 12: 윈도우 함수 `FIRST_VALUE` / `LAST_VALUE` 사일런트 NULL 결함 (`FIX-WINDOW-FIRST-LAST-VALUE-1`)

#### 1) 근본 원인
- `pkg/execution/window.go:182`의 함수 디스패처에 `FIRST_VALUE` 및 `LAST_VALUE` 핸들러가 누락되어 있어, 문법 파서를 통과한 후에도 아무 값도 계산하지 않고 조용히 `NULL`을 반환함 (`[RULE:ExplicitError]` 위반).

#### 2) 수정 아키텍처
- `pkg/execution/window.go`:
  - `case "FIRST_VALUE":` 파티션 내 첫 번째 유효 행의 인자 식 값 반환.
  - `case "LAST_VALUE":` 윈도우 프레임(`ROWS BETWEEN ...`) 범위 내의 마지막 행 인자 식 값 반환.

---

### 2.12 결함 13: `SUBSTRING(str, pos)` 2인자 표준 호출 결함 (`FIX-SUBSTRING-TWO-ARGUMENTS-1`)

#### 1) 근본 원인
- `pkg/proc/builtins_string.go:418`에서 `if len(args) < 3`일 때 무조건 `storage.NullValue`를 반환하도록 하드코딩되어 있어, 길이 인자를 생략하는 표준 2인자 호출(`SUBSTRING(s, 2)`)이 `NULL`로 처리됨.

#### 2) 수정 아키텍처
- `pkg/proc/builtins_string.go`:
  - `len(args) == 2`인 경우, `length := len(runes) - start`로 계산하여 시작 위치부터 문자열 끝까지의 슬라이스를 정상 반환.

---

### 2.13 결함 14: Volcano 및 집계 실행기 `Open()` 중 에러 시 자식 이터레이터/스토리지 커서 미정리 누수 결함 (`FIX-OPEN-ERROR-CURSOR-LEAK-1`)

#### 1) 근본 원인
- `pkg/execution/aggregate.go`:
  - `a.child.Open(ctx)` 호출 후 `validateHavingClause` 또는 메모리 한도 검사 등에서 에러 발생 시, 조기 반환(`return err`)으로 인해 `a.child.Close()`가 호출되지 않음.
- `pkg/engine/sqlexec_select.go:195` 및 `line 1681`:
  - `if err := exec.Open(ctx); err != nil { return nil, err }` 이후에 `defer exec.Close()`가 배치되어 있어, `exec.Open()` 호출 도중 에러가 반환되면 `exec.Close()`가 전혀 등록되지 않고 유실됨.
- 이로 인해 하위의 `SeqScanIterator` / `IndexScanIterator`가 오픈한 WiredTiger B+Tree 커서(`session->open_cursor`)가 닫히지 않고 프로세스 수명 동안 유지되어 테이블 잠금(`EBUSY`), 스키마 DDL 실패, 파일 디스크립터 누수가 발생함 (`[RULE:HandleHygiene]`, `[RULE:DoubleCloseSafety]` 위반).

#### 2) 수정 아키텍처
- `pkg/execution/aggregate.go`:
  - `Open(ctx context.Context) (retErr error)`에 `defer func() { if retErr != nil { _ = a.child.Close() } }()`를 적용하여, `Open()` 내부의 어떤 에러나 조기 탈출 시에도 하위 자식 이터레이터의 자원이 결정론적으로 즉시 해제되도록 보장.
- `pkg/engine/sqlexec_select.go`:
  - `exec.Open(ctx)` 및 `currIter.Open(ctx)` 에러 반환 시 즉시 `_ = exec.Close()` / `_ = currIter.Close()`를 명시적으로 호출하여 전체 실행기 트리 및 하위 스토리지 커서를 100% 정리한 뒤 에러를 반환.

### 2.14 결함 15: IN / NOT IN 서브쿼리 사용 시 SELECT * 와일드카드 프로젝션 컬럼 오염 결함 (`FIX-SUBQUERY-WILDCARD-PROJECTION-LEAK-1`)

#### 1) 근본 원인
- `pkg/planner/planner.go` 내 `BuildMultiJoinPlanWithStats` (약 1111라인):
  - `pkg/planner/decorrelate.go`가 `WHERE val IN (SELECT val FROM _t2)` 서브쿼리를 decorrelate하여 `SEMI` / `ANTI` 조인으로 변환 후 `q.Joins`에 등록함.
  - 플래너가 `SELECT *` 와일드카드를 실제 프로젝션 컬럼 목록으로 확장할 때, `q.Joins`의 모든 조인 대상을 순회하며 조인 타입(`SEMI`/`ANTI`)을 확인하지 않고 서브쿼리 내부 릴레이션의 모든 컬럼(`_t2.id`, `_t2.val`)을 `q.Projections`에 무조건 덧붙임.
  - 결과적으로 외부 테이블의 컬럼 외에 서브쿼리 테이블의 컬럼까지 클라이언트에게 노출되어 ORM 엔티티 매핑 및 스키마 정합성이 파괴됨.

#### 2) 수정 아키텍처
- `pkg/planner/planner.go`:
  - 와일드카드 `*` 확장 루프에서 조인 타입이 `SEMI` 또는 `ANTI`인 경우 (`j.Type == "SEMI" || j.Type == "ANTI"`) 프로젝션 스키마 확장 목록에서 제외하여 외부 주 릴레이션의 컬럼만 프로젝션에 유지되도록 가드 추가.

---

### 2.15 결함 16: GROUP BY 비유효 컬럼 지정 시 전 행 1개 그룹 뭉개짐 결함 (`FIX-GROUPBY-INVALID-COLUMN-VALIDATION-1`)

#### 1) 근본 원인
- `pkg/planner/planner.go` 및 `pkg/execution/aggregate.go`:
  - `GROUP BY non_existent_col` 등 테이블 및 프로젝션에 존재하지 않는 컬럼명을 지정했을 때, 플래너에서 시맨틱 유효성 검사를 수행하지 않고 통과시킴.
  - `AggregateIterator`가 행 평가 시 `lookupVal(tuple, k)`를 호출하면 `k`를 찾지 못해 `(nil, false)`가 반환되고, 모든 행이 동일한 NULL 해시 키로 분류되어 단 1개의 그룹으로 뭉개짐.
  - 사용자는 쿼리 오타나 잘못된 컬럼을 인지하지 못한 채 완전히 왜곡된 통계 수치를 받게 됨 (`[RULE:ExplicitError]` 위반).

#### 2) 수정 아키텍처
- `pkg/planner/planner.go`:
  - `GROUP BY` 항목 검증 단계에서, 그룹 키가 프로젝션 별칭이나 입력 릴레이션(또는 CTE, 파생 테이블)의 실제 스키마 컬럼인지 검증.
  - 유효한 컬럼이나 별칭, 또는 정수 서수 위치가 아닌 경우 즉시 `column "%s" does not exist` 컴파일 에러를 반환하여 Fail-Closed 차단.

---

### 2.16 결함 17: WITH CTE 기반 DELETE / UPDATE 실행 시 기저 원본 테이블 매핑 지원 (`FIX-CTE-DML-UPDATABLE-TARGET-1`)

#### 1) 근본 원인
- `pkg/engine/sqlexec_dml.go`:
  - `WITH cte AS (...) DELETE FROM cte WHERE ...` 또는 `UPDATE cte SET ...` 구문 실행 시, CTE가 `#___cte_...` 형태의 가상 테이블로 파싱/생성되지만, DML 실행기는 실제 스토리지 물리 테이블 카탈로그에서만 대상을 조회하여 `table "#__cte_..." not found` 에러가 발생함.
  - T-SQL 및 표준 SQL의 Updatable CTE 스펙에 따라, CTE가 단일 기저 테이블을 프로젝션하는 경우 해당 기저 물리 테이블의 기본키(PK)를 추적하여 실제 물리 테이블의 레코드를 DELETE/UPDATE 처리해야 함.

#### 2) 수정 아키텍처
- `pkg/engine/sqlexec_dml.go`:
  - DELETE / UPDATE 대상 테이블이 CTE 뷰인 경우, CTE 정의 AST를 분석하여 기저 물리 테이블 이름 및 행 식별자(PK)를 도출.
  - CTE 필터 조건에 부합하는 PK 목록을 선별한 뒤, 기저 원본 물리 테이블에 대해 원자적 DELETE / UPDATE를 수행하도록 Updatable CTE DML 라우팅 파이프라인 구축.

### 2.17 결함 18: Volcano 반복자 전반(Window, Sort) Open 에러 시 하위 커서 미정리 누수 결함 (`FIX-OPEN-ERROR-ITERATOR-LEAK-ALL-1`)

#### 1) 근본 원인
- `pkg/execution/window.go:49` (`WindowIterator.Open`):
  - `w.child.Next(ctx)` 호출 루프 도중 I/O 에러가 발생하거나 `w.workMem` 초과 시 `_ = w.child.Close()`가 누락된 채 즉시 반환되는 경로 존재 (`line 65`).
- `pkg/execution/sort.go:212` (`SortIterator.Open`):
  - `retErr != nil` 시 `s.releaseSpillResources()`만 호출하고 `s.child.Close()`를 호출하지 않아 정렬 단계 에러 시 하위 테이블 커서가 미정리 상태로 남음.

#### 2) 수정 아키텍처
- `WindowIterator.Open`: `defer func() { if retErr != nil && w.child != nil { _ = w.child.Close() } }()` 적용.
- `SortIterator.Open`: `defer func() { if retErr != nil { s.releaseSpillResources(); if s.child != nil { _ = s.child.Close() } } }()` 적용.

---

### 2.18 결함 19: 윈도우 함수 PARTITION BY / ORDER BY 비유효 컬럼 시맨틱 검증 결함 (`FIX-WINDOW-INVALID-COLUMN-VALIDATION-1`)

#### 1) 근본 원인
- `pkg/execution/window.go:102`:
  - `winDef.PartitionKeys` 또는 `winDef.OrderByKeys`에 오타나 존재하지 않는 컬럼명이 들어올 경우, `t.Get(col)`이 조용히 `nil`을 반환하여 전 행이 단 1개의 파티션으로 묶이거나 정렬이 무시됨.

#### 2) 수정 아키텍처
- `WindowIterator.Open`:
  - 자식 릴레이션의 `Schema()` 및 `ColIndexMap()`을 검사하여, 파티션 키나 정렬 키가 유효하지 않은 경우 즉시 `column "%s" does not exist` 에러를 반환.

---

### 2.19 결함 20: 복합 기본키(Composite PK) 테이블 DML 실행 시 엉뚱한 행 덮어쓰기 (`FIX-COMPOSITE-PK-DML-OVERWRITE-1`)

#### 1) 근본 원인
- `pkg/engine/sqlexec_dml.go` (`execUpdateFromJoin`, `execDeleteFromJoin`) 및 `pkg/engine/sqlexec_explain.go` (`execUpdatableCTEDML`):
  1. 기저 테이블 스키마 조회 시 `schema.PrimaryKeyColumn()`을 호출하여 단일 컬럼(`k1`)만 수집함.
  2. 테이블이 복합 기본키 `PRIMARY KEY (k1, k2)`를 갖는 경우, 드라이빙 쿼리에서 `k1 AS __lionsql_pk__` 1개만 프로젝션함.
  3. 행 식별 및 변경 적용 단계에서 복합 키 전체 `(k1, k2)`가 아닌 `k1` 단일 컬럼만으로 대상을 조회/적용하여, 동일한 `k1` 값을 공유하는 다른 모든 튜플(예: `(1, 10)`, `(1, 20)`)이 의도치 않게 동일 값으로 덮어써지거나 부당 삭제되는 심각한 데이터 파괴(Data Corruption) 발생.

#### 2) 수정 아키텍처
1. **복합 기본키 전체 컬럼 수집 및 드라이빙 프로젝션**:
   - `baseSchema.PrimaryKeyColumns()` 또는 `baseSchema.PrimaryKeyColNames()`를 사용하여 모든 PK 컬럼 목록을 획득.
   - 드라이빙 SELECT 쿼리에서 모든 PK 컬럼을 각각 고유 별칭(`__lionsql_pk_0__`, `__lionsql_pk_1__`, ...)으로 프로젝션.
2. **복합 PK 키 조합 기반 1:1 행 식별 및 갱신/삭제**:
   - 행 갱신/삭제 시 각 행의 복합 PK 값들을 바인딩하여 복합 키 기반 레코드 조회(`tx.Get(compositeKey)` 또는 복합 조건 매칭)를 수행.
   - 조건에 부합하는 타깃 레코드만 정확히 1:1로 갱신/삭제되도록 격리하여 데이터 정합성 보장.

---

### 2.20 결함 21: 윈도우 함수 내부 인자에 비유효 컬럼 지정 시 사일런트 NULL 반환 (`FIX-WINDOW-ARG-INVALID-COLUMN-1`)

#### 1) 근본 원인
- `pkg/execution/window.go` (`WindowIterator.Open`):
  - `PARTITION BY` 및 `ORDER BY` 절의 컬럼명에 대해서는 유효성 검사가 추가되었으나, 윈도우 함수 본체(`WindowFunction`)의 인자 식(`winFn.Arg`, 예: `AVG(non_existent_col)`)에 대한 컬럼 유효성 검증기가 누락되어 있음.
  - 평가 시 `lookupVal`이 `nil`을 반환하여 컴파일 에러 없이 조용히 통과되고 전체 결과셋에 `NULL`이 채워짐.

#### 2) 수정 아키텍처
- `WindowIterator.Open()` 단계에서 `winDef.Functions`를 순회:
  - 각 윈도우 함수의 인자 식(`winFn.Arg`)에서 컬럼 식별자(함수 호출 제외 순수 컬럼 및 식별자)를 추출.
  - 자식 릴레이션의 스키마 및 `currIndexMap`에 해당 컬럼이 존재하는지 사전 검증.
  - 미존재 시 조용히 통과시키지 않고 즉시 `fmt.Errorf("column %q does not exist", colName)`를 반환하여 컴파일 단계에서 차단 (`[RULE:ExplicitError]`).

---

### 2.21 결함 22: ORDER BY 절 비유효 컬럼 및 범위 초과 서수(Ordinal) 미검증 (`FIX-ORDERBY-INVALID-COLUMN-AND-ORDINAL-1`)

#### 1) 근본 원인
- `pkg/planner/planner.go` (`FromAST` / `BuildPlan`) 및 `pkg/execution/sort.go`:
  - `GROUP BY`의 경우 서수 검증 및 비유효 컬럼 검증이 추가되었으나, `ORDER BY` 항목에 대해서는 서수 범위(`1 <= idx <= len(projections)`) 검증과 스키마/프로젝션 컬럼 존재 여부 검사가 누락됨.
  - `ORDER BY 0`, `ORDER BY 999`, `ORDER BY non_existent_col` 지정 시 정렬이 조용히 무시되거나 비정상 통과됨.

#### 2) 수정 아키텍처
1. **서수(Ordinal) 범위 엄격 검증**:
   - 정수 리터럴인 경우 `1 <= ord <= len(projections)` 범위를 확인하고, 범위를 벗어난 경우 즉시 `fmt.Errorf("ORDER BY position %d is not in select list", ord)` 에러 반환.
2. **컬럼 식별자 스키마/별칭 유효성 검증**:
   - 식별자인 경우 프로젝션 별칭, 프로젝션 원본 표현식, 그리고 기본 입력 테이블 스키마에 존재하는지 확인.
   - 어느 곳에도 존재하지 않는 경우 즉시 `fmt.Errorf("column %q does not exist", colName)` 에러 반환.

---

### 2.22 결함 23: SUBSTRING(str, pos) 2인자 호출 시 pos <= 0일 때 문자열 끝부분 절삭 오류 (`FIX-SUBSTRING-NONPOSITIVE-POS-1`)

#### 1) 근본 원인
- `pkg/proc/builtins_string.go` (431~454라인):
  - 2인자 `SUBSTRING(str, pos)` 호출 시 기본 길이로 `length = len(runes)`가 설정됨.
  - 그러나 3인자 슬라이싱 계산 공식 `if rawStart < 1 { start = 0; count = rawStart + length - 1 }`이 2인자 호출에도 무조건 적용됨.
  - 예를 들어 `SUBSTRING('hello', 0)`의 경우 `count = 0 + 5 - 1 = 4`가 되어 마지막 문자 `'o'`가 잘려 `'hell'`이 반환되고, `SUBSTRING('hello', -2)`는 `count = -2 + 5 - 1 = 2`가 되어 `'he'`만 반환됨.

#### 2) 수정 아키텍처
- 2인자 호출(`len(args) == 2`)의 시맨틱은 "시작 위치부터 문자열 끝까지"이므로, `rawStart <= 0`인 경우에도 추출 끝 인덱스는 항상 원본 문자열의 끝(`end = len(runes)`)이어야 함.
- `len(args) == 2`인 경우 `rawStart < 1`이더라도 길이를 감산하지 않고 `start = 0`, `end = len(runes)`로 슬라이싱하도록 분기 보정.

---

### 2.23 결함 24: 문자열 값 비교 시 대괄호/따옴표 무단 제거로 인한 오매칭 (`FIX-STR-COMPARE-CANONICAL-STRIP-1`)

#### 1) 근본 원인
- `pkg/execution/iterator.go` (688~692라인, `CompareValues`):
  ```go
  if isStrA && isStrB {
      cA := strings.TrimRight(sqlutil.CanonicalIdent(sa), " ")
      cB := strings.TrimRight(sqlutil.CanonicalIdent(sb), " ")
      return strings.Compare(cA, cB)
  }
  ```
  - 테이블 및 컬럼 식별자(Identifier) 전용 정규화 함수인 `sqlutil.CanonicalIdent`를 데이터 값(Data Value) 비교 함수에서 호출함.
  - `CanonicalIdent`가 데이터 내부의 대괄호(`[...]`), 백틱, 따옴표를 무조건 벗겨내어 `'[ADMIN]'`과 `'ADMIN'`이 동일한 문자열로 비교되어 잘못 매칭되는 치명적 데이터 왜곡(Data Distortion) 발생.

#### 2) 수정 아키텍처
- `CompareValues`에서 `sqlutil.CanonicalIdent` 호출을 전면 제거.
- 데이터 값 비교는 식별자 이스케이프 제거 없이 순수 문자열 값을 보존하여 대소문자 및 후행 공백 트림(`strings.TrimRight(sa, " ")`)만 수행하도록 수정 (`[RULE:ModularAbstraction]`, `[RULE:RadicalKISS]`).

---

### 2.24 결함 25: 저장 프로시저(SP) 내 NULL 비교 연산 시 ANSI 3-Valued Logic 파괴 (`FIX-SP-NULL-EQUAL-3VL-1`)

#### 1) 근본 원인
- `pkg/proc/evaluator.go` (399~401라인) 및 `pkg/storage/engine.go` (1191라인, `Value.Equal`):
  - `Value.Equal()`은 해시 맵 키나 세트 연산 등 스토리지 물리적 동등성을 위해 `v.IsNull && other.IsNull`일 때 `true`를 반환하도록 설계됨.
  - SP 내 표현식 평가기(`evaluator.go`)의 `TokenEqual`/`TokenNotEqual` 비교에서 이 함수를 그대로 호출하여, `IF @a = @b`가 `@a`, `@b`가 모두 NULL임에도 `TRUE`로 평가되어 비즈니스 로직 분기 오류 발생.

#### 2) 수정 아키텍처
- `pkg/proc/evaluator.go`의 비교 연산자(`TokenEqual`, `TokenNotEqual`, `<`, `<=`, `>`, `>=`) 처리 시 피연산자 중 하나라도 `IsNull`이면 즉시 `storage.NullValue(storage.TypeBool)`를 반환하도록 3VL 가드 적용.
- 조건문 평가(`evalCondition`)에서 조건식이 NULL이면 FALSE로 평가되어 ELSE 분기로 정상 진입하도록 보장.

---

### 2.25 결함 26: 나눗셈 연산자(/)의 0 나누기 에러 은폐 및 사일런트 NULL 반환 (`FIX-DIV-BY-ZERO-ERROR-VISIBILITY-1`)

#### 1) 근본 원인
- `pkg/proc/runtime/vm.go` (3471~3473라인, `storageDiv`) 및 `pkg/execution/eval.go` (585라인):
  - 정수 나눗셈(`DIV`)과 모듈로(`%`)는 0 나누기 시 `division by zero` 에러를 정상 발생시키지만, 슬래시(`/`) 나눗셈 연산자는 분모가 0일 때 조용히 `NULL`을 반환하도록 하드코딩되어 결함을 은폐함.

#### 2) 수정 아키텍처
- 분모가 0인 경우 사일런트 NULL 반환을 완전히 제거하고 `fmt.Errorf("division by zero")` (PostgreSQL 규격) 및 `fmt.Errorf("divide by zero error encountered")` (T-SQL 규격) 명시적 에러를 반환하도록 통일 (`[RULE:ExplicitError]`).

---

### 2.26 결함 27: T-SQL DATEDIFF 경계선(Boundary) 계산 스펙 위반 (`FIX-DATEDIFF-BOUNDARY-CROSSING-1`)

#### 1) 근본 원인
- `pkg/proc/builtins_date.go` (131~140라인, `evalDateDiff`):
  - T-SQL 공식 스펙상 `DATEDIFF(datepart, start, end)`는 해당 datepart의 경계선(Boundary) 통과 횟수를 계산해야 함.
  - 현재 구현은 단순 경과 시간을 `24*7` 또는 `3600`으로 나누어 반환하므로, 토요일 23:59에서 일요일 00:01(주 경계 통과) 또는 23:59에서 00:01(시간 경계 통과) 시 경계선을 통과했음에도 경과 시간이 부족하여 0을 반환함.

#### 2) 수정 아키텍처
- `week`, `hour`, `minute`, `second` datepart에 대해 각 일시를 해당 단위의 시작점(Truncate/Floor)으로 정렬한 후 단위 차이를 계산하도록 보정:
  - `week`: 두 일시를 일요일 00:00:00으로 Floor한 후 일수 차이 / 7
  - `hour`: 분/초/나노초를 0으로 Floor한 후 시간 차이 계산
  - `minute`: 초/나노초를 0으로 Floor한 후 분 차이 계산

---

### 2.27 결함 28: 정수 산술 연산 시 64비트 오버플로 미검출 및 음수 래핑 (`FIX-ARITHMETIC-OVERFLOW-DETECTION-1`)

#### 1) 근본 원인
- `pkg/execution/eval.go` (578~583라인, `evalTupleBinaryExprWithContext`):
  - Go의 원시 `int64` 연산자를 그대로 사용하여 64비트 정수 최댓값(BIGINT Max: 9,223,372,036,854,775,807)을 초과하는 곱셈/덧셈 연산 시 비트 래핑으로 인해 `-2` 등의 엉뚱한 음수가 반환됨.

#### 2) 수정 아키텍처
- 정수 덧셈 및 곱셈 연산 시 `math.MaxInt64`, `math.MinInt64` 범위 초과를 사전 검출하여 오버플로 발생 시 명시적 `fmt.Errorf("bigint out of range")` / `fmt.Errorf("arithmetic overflow error converting expression to data type bigint")` 에러 반환 (`[RULE:ExplicitError]`).

---

### 2.28 결함 29: WiredTiger 보조 인덱스 스캔 시 복합 PK 파싱 실패로 조회 0건 증발 (`FIX-WT-INDEX-COMPOSITE-PK-SCAN-1`)

#### 1) 근본 원인
- `pkg/storage/wiredtiger/wt_engine.go:3786-3828` (`wtIndexBTreeCursor`):
  - `Row()`, `Record()`, `RawTuple()`에서 복합 PK(`"1\x1f20"`)가 저장된 `c.currPK`에 대해 `c.tbl.PrimaryKeyColumn()`으로 첫 번째 컬럼 타입만 조회한 뒤 `storage.ParsePKValue(c.currPK, pkCol.Type)`를 호출함.
  - `strconv.ParseInt("1\x1f20", 10, 64)`가 실패하여 `IntValue(0)`으로 변환되어 메인 테이블 조회가 `ErrKeyNotFound`로 실패함.

#### 2) 수정 아키텍처
- `c.tbl.IsCompositePK()`가 `true`인 경우 `storage.ParsePKValue`를 거치지 않고 `storage.StringValue(c.currPK)`를 그대로 메인 테이블 조회 키로 전달.

---

### 2.29 결함 30: WiredTiger 외래키 `ON DELETE CASCADE / SET NULL` 복합 PK 자식 삭제 누락 (`FIX-WT-FK-COMPOSITE-PK-CASCADE-1`)

#### 1) 근본 원인
- `pkg/storage/wiredtiger/wt_engine.go:3260, 3294-3297` (`checkReferencingFKsOnDeleteLocked`):
  - 자식 테이블이 복합 PK를 가질 때 `cTbl.PrimaryKeyColumn()`으로 첫 번째 PK 컬럼 값만 추출하여 `tx.deleteLocked` / `tx.updateLocked`에 전달하므로 실제 복합 키(`"1\x1f10"`)와 불일치하여 자식 행 삭제/갱신이 누락됨.

#### 2) 수정 아키텍처
- `memengine`과 동일하게 `cTbl.ResolveRowPK(row)`를 사용하여 복합 PK 전체가 결합된 `Value`를 추출하여 `matchingPKs`에 적재하도록 수정.

---

### 2.30 결함 31: `MERGE INTO` 구문에서 복합 PK 타깃 테이블 매칭/갱신/삭제 오동작 (`FIX-MERGE-INTO-COMPOSITE-PK-1`)

#### 1) 근본 원인
- `pkg/engine/sqlexec_merge.go:67, 135, 357, 370, 386, 478, 495`:
  - 타깃 레코드의 키를 `tRec[pkCol.Name]` 단일 컬럼으로만 추출하여 `tx.Update`, `tx.Delete`, `matchedTargetKeys`, `newlyInsertedKeys`에 사용함으로써 복합 PK 테이블에서 MERGE 연산이 오동작함.

#### 2) 수정 아키텍처
- `targetSchema.ResolveRecordPK(tRec)`을 일관되게 호출하여 단일 및 복합 PK 모두 정확한 키 `Value`와 문자열 식별자를 사용하도록 통일.

---

### 2.31 결함 32: PITR(시점 복구) WAL 재생 시 복합 PK 테이블 `UPDATE/DELETE` 복구 유실 (`FIX-PITR-WAL-COMPOSITE-PK-1`)

#### 1) 근본 원인
- `pkg/backup/pitr.go:228-257`:
  - WAL 재생 중 `OpUpdate`는 `rec[pkCol.Name]`만 추출하고 `OpDelete`는 `memengine.ParsePKValue(op.Key, pkCol.Type)`으로 복합 키 문자열 파싱을 시도하여 복구가 유실됨.

#### 2) 수정 아키텍처
- `tblSchema.IsCompositePK()`인 경우 `OpUpdate`는 `tblSchema.ResolveRecordPK(rec)`, `OpDelete`는 `storage.StringValue(op.Key)`를 사용하도록 수정.

---

### 2.32 결함 33: Hash Join 조인 키 생성기(`formatKeyVal`)의 대괄호/따옴표 제거로 인한 오조인 (`FIX-HASH-JOIN-KEY-CANONICAL-STRIP-1`)

#### 1) 근본 원인
- `pkg/execution/join.go:836-862` (`formatKeyVal`):
  - 해시 조인의 조인 키 정규화 시 `sqlutil.CanonicalIdent`를 호출하여 데이터 값 내부의 대괄호(`[...]`), 따옴표, 백틱을 무단 제거함에 따라 `'[code1]'`과 `'code1'`이 동일 해시 버킷으로 매칭되어 오조인 발생.

#### 2) 수정 아키텍처
- `formatKeyVal`에서 `sqlutil.CanonicalIdent` 호출을 제거하고 `strings.ToLower(strings.TrimRight(v, " "))`만 수행하도록 수정.

---

### 2.33 결함 34: `SetOpIterator.Open()` 우측 자식 Materialization 에러 시 양쪽 커서 누수 (`FIX-SETOP-OPEN-ERROR-CURSOR-LEAK-1`)

#### 1) 근본 원인
- `pkg/execution/setop.go:146-164` (`SetOpIterator.Open`):
  - `s.left.Open` 및 `s.right.Open` 성공 후 `INTERSECT`/`EXCEPT` 우측 자식 튜플 Materialization 도중 에러 또는 컨텍스트 취소 발생 시 이미 열린 `s.left`와 `s.right`를 닫지 않고 반환함.

#### 2) 수정 아키텍처
- `Open(ctx context.Context) (retErr error)`로 변경하고 `defer` 클린업 가드를 통해 `retErr != nil` 시 양쪽 자식 이터레이터를 즉시 `Close()`하도록 수정 (`[RULE:HandleHygiene]`).

---

### 2.34 결함 35: `MergeJoinIterator.Open()` 초기 튜플 Pull 에러 시 양쪽 자식 커서 누수 (`FIX-MERGE-JOIN-OPEN-ERROR-CURSOR-LEAK-1`)

#### 1) 근본 원인
- `pkg/execution/merge_join.go:131-152` (`MergeJoinIterator.Open`):
  - 양쪽 자식 `Open` 이후 초기 튜플 `Next(ctx)` 호출 중 에러 발생 시 이미 열린 `m.left`, `m.right` 커서를 닫지 않고 반환함.

#### 2) 수정 아키텍처
- `Open(ctx context.Context) (retErr error)`로 변경하고 `defer` 클린업 가드를 통해 `retErr != nil` 시 `m.left`와 `m.right`를 안전하게 닫도록 수정 (`[RULE:HandleHygiene]`).

---

## 3. 회귀 테스트(Regression Test) 구축 전략

각 결함별로 독립된 유닛 및 통합 테스트 파일을 작성하여 검증한다:
1. `pkg/engine/update_delete_from_join_test.go`:
   - `UPDATE ... FROM ... JOIN ... WHERE` 기본 조인 반영 검증 (PASS)
   - `DELETE ... FROM ... JOIN ... WHERE` 기본 조인 삭제 검증 (PASS)
   - 테이블 별칭(Alias) 사용 케이스 검증 (PASS)
   - 복합 조건(WHERE) 및 0건 일치 시 안전성 검증 (PASS)
2. `pkg/engine/window_sliding_avg_test.go`:
   - `AVG(val) OVER (ORDER BY id)` 누적 러닝 평균 검증 (PASS)
   - `AVG(val) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW)` 슬라이딩 프레임 평균 검증 (PASS)
   - 단건 행 폴백 오작동 재발 방지 검증 (PASS)
3. `pkg/engine/having_semantic_test.go`:
   - `SELECT dept_id FROM t GROUP BY dept_id HAVING salary > 100` -> 명시적 에러 반환 검증 (PASS)
   - `SELECT dept_id FROM t GROUP BY dept_id HAVING dept_id > 1` -> 정상 통과 검증 (PASS)
   - `SELECT dept_id FROM t GROUP BY dept_id HAVING AVG(salary) > 100` -> 정상 통과 검증 (PASS)
4. `pkg/proc/tsql_len_test.go`:
   - `LEN('abc   ')` -> 3 (PASS)
   - `LEN(' ')` -> 0 (PASS)
   - `LEN('   a')` -> 4 (선행 공백 유지, PASS)
   - `LENGTH('abc   ')` -> 6 (PG 호환성 유지, PASS)
   - 유니코드 한글 및 이모지 후행 공백 검증 (PASS)
5. `pkg/proc/tsql_isnull_test.go` & `pkg/engine/tsql_isnull_sql_test.go`:
   - `ISNULL(CAST(NULL AS VARCHAR(3)), 'abcdefgh')` -> `'abc'` (길이 Truncate 적용, PASS)
   - `COALESCE(CAST(NULL AS VARCHAR(3)), 'abcdefgh')` -> `'abcdefgh'` (ANSI SQL 타입 승격, PASS)
6. `pkg/storage/type_validator_test.go` & `pkg/engine/type_validator_sql_test.go`:
   - `VARCHAR(3)`에 10글자 삽입 시 Truncation 에러 검증 (PASS)
   - `DECIMAL(5, 2)`에 `9999999.99` 삽입 시 Arithmetic Overflow 에러 검증 (PASS)
   - `TINYINT`에 `999` 삽입 시 Range [0, 255] Overflow 에러 검증 (PASS)
   - `CHAR(5)`에 `'abc'` 삽입 시 `'abc  '` 5자리 패딩 정규화 검증 (PASS)
7. `pkg/engine/three_valued_logic_test.go`:
   - `TRUE AND NULL` -> `NULL`, `FALSE AND NULL` -> `false`, `NULL AND NULL` -> `NULL` (PASS)
   - `TRUE OR NULL` -> `true`, `FALSE OR NULL` -> `NULL`, `NULL OR NULL` -> `NULL` (PASS)
8. `pkg/engine/groupby_ordinal_alias_test.go`:
   - `GROUP BY 1` 서수 위치 매핑 및 2개 그룹 정상 분할 검증 (PASS)
   - `GROUP BY d` 프로젝션 별칭 매핑 및 2개 그룹 정상 분할 검증 (PASS)
   - `GROUP BY 99` 범위 초과 시 명시적 컴파일 에러 검증 (PASS)
   - `GROUP BY 2` (집계 함수) 지정 시 명시적 컴파일 에러 검증 (PASS)
9. `pkg/planner/setop_test.go`:
   - `BuildSetOpPlan_IntersectPrecedence`: `INTERSECT`의 높은 우선순위 트리 구성 검증 (PASS)
10. `pkg/engine/ansi_sql_regression_test.go`:
    - `TRIM(BOTH 'x' FROM 'xxxhelloxxx')` -> `'hello'` (PASS)
    - `TRIM(LEADING 'x' FROM 'xxxhelloxxx')` -> `'helloxxx'` (PASS)
    - `TRIM(TRAILING 'x' FROM 'xxxhelloxxx')` -> `'xxxhello'` (PASS)
    - `SUBSTRING('hello', 2)` -> `'ello'` (PASS)
    - `SELECT 1 AS val UNION ALL SELECT 2 INTERSECT SELECT 2` -> 2개 행 `[1, 2]` 반환 (PASS)
    - 윈도우 함수 `FIRST_VALUE` 및 `LAST_VALUE` 계산 검증 (PASS)
11. `pkg/engine/open_error_cursor_leak_test.go`:
    - `Aggregate_OpenError_ClosesChildIterator`: Open 실패 시 하위 child.Close() 호출 검증 (PASS)
    - `Engine_OpenError_VolcanoCleanTeardown`: 쿼리 실행 에러 직후 DROP TABLE 성공 검증 (커서 미누수, PASS)
12. `pkg/engine/subquery_wildcard_leak_test.go`:
    - `SELECT * FROM _t1 WHERE val IN (SELECT val FROM _t2)` -> 오직 `_t1`의 컬럼만 반환 검증 (PASS)
    - `SELECT * FROM _t1 WHERE val NOT IN (SELECT val FROM _t2)` -> 오직 `_t1`의 컬럼만 반환 검증 (PASS)
13. `pkg/engine/groupby_invalid_col_test.go`:
    - `SELECT dept_id, COUNT(*) FROM _t GROUP BY non_existent_col` -> `column "non_existent_col" does not exist` 컴파일 에러 검증 (PASS)
    - `SELECT id, ROW_NUMBER() OVER (PARTITION BY bad_col ...)` -> `column "bad_col" does not exist` 컴파일 에러 검증 (PASS)
    - `SELECT id, ROW_NUMBER() OVER (ORDER BY invalid_ord_col)` -> `column "invalid_ord_col" does not exist` 컴파일 에러 검증 (PASS)
    - `SELECT id, AVG(non_existent_col) OVER (PARTITION BY val)` -> `column "non_existent_col" does not exist` 컴파일 에러 검증 (PASS)
14. `pkg/engine/cte_dml_test.go`:
    - `WITH dupes AS (...) DELETE FROM dupes WHERE rn > 1` -> 기저 테이블 중복 행 삭제 검증 (PASS)
    - `WITH target AS (...) UPDATE target SET val = 999 WHERE id = 1` -> 기저 테이블 갱신 검증 (PASS)
15. `pkg/engine/composite_pk_dml_test.go`:
    - 복합 기본키 테이블에서 CTE UPDATE 실행 시 타깃 조건에 해당하는 특정 행만 갱신되고 다른 행이 보존되는지 검증 (PASS)
    - 복합 기본키 테이블에서 CTE DELETE 실행 시 특정 행만 삭제되고 다른 행이 보존되는지 검증 (PASS)
    - 복합 기본키 테이블에서 JOIN UPDATE/DELETE 실행 시 행 덮어쓰기 및 오염 방지 검증 (PASS)
16. `pkg/engine/window_arg_invalid_col_test.go`:
    - `SELECT id, AVG(non_existent_col) OVER (PARTITION BY dept_id) FROM t` -> 명시적 `column "non_existent_col" does not exist` 에러 검증 (PASS)
17. `pkg/engine/orderby_validation_test.go`:
    - `SELECT id, val FROM t ORDER BY non_existent_col` -> 명시적 `column "non_existent_col" does not exist` 에러 검증 (PASS)
    - `SELECT id, val FROM t ORDER BY 0` -> 명시적 `ORDER BY position 0 is not in select list` 에러 검증 (PASS)
    - `SELECT id, val FROM t ORDER BY 999` -> 명시적 범위 초과 에러 검증 (PASS)
18. `pkg/proc/builtins_string_test.go`:
    - `SUBSTRING('hello', 1)` -> `'hello'`, `SUBSTRING('hello', 0)` -> `'hello'`, `SUBSTRING('hello', -2)` -> `'hello'` 정상 반환 검증 (PASS)
19. `pkg/engine/audit_round4_regression_test.go`:
    - `SELECT * FROM _t WHERE tag = 'ADMIN'` 실행 시 `'[ADMIN]'`이 오매칭되지 않고 오직 `'ADMIN'`만 반환되는지 검증 (PASS)
    - SP 내 `DECLARE @a INT = NULL; DECLARE @b INT = NULL; IF @a = @b ... ELSE ...` 실행 시 3VL에 의해 ELSE 블록 진입 검증 (PASS)
    - `SELECT 10 / 0` 실행 시 사일런트 NULL이 아닌 `division by zero` 명시적 에러 반환 검증 (PASS)
    - `DATEDIFF(week/hour/minute)` 경계선 통과 횟수 `(1, 1, 1)` 계산 검증 (PASS)
    - `SELECT 9223372036854775807 * 2` 및 `+ 1` 실행 시 음수 래핑 없이 `arithmetic overflow error: bigint out of range` 명시적 에러 반환 검증 (PASS)
    - 복합 PK 타깃 테이블에 대한 `MERGE INTO` (`WHEN MATCHED THEN UPDATE`, `WHEN NOT MATCHED THEN INSERT`, `WHEN NOT MATCHED BY SOURCE THEN DELETE`) 정합성 검증 (PASS)
    - `'[code1]'`과 `'code1'` Hash Join 시 오조인(False-Positive Join) 미발생 검증 (PASS)
20. `pkg/storage/wiredtiger/wt_composite_pk_test.go`:
    - WiredTiger 복합 PK 보조 인덱스 스캔(`SELECT * FROM t WHERE val = 500`) 시 누락 없이 2건 반환 검증 (PASS)
    - WiredTiger 복합 PK 자식 테이블 `ON DELETE CASCADE` 정상 동작 검증 (PASS)
21. `pkg/backup/pitr_composite_pk_test.go`:
    - 복합 PK 테이블의 WAL `UPDATE`/`DELETE` 기록 재생(PITR) 정합성 검증 (PASS)
22. `pkg/engine/open_error_cursor_leak_test.go` (`TestSetOpAndMergeJoin_OpenError_ClosesChildren`):
    - `SetOpIterator.Open()` 및 `MergeJoinIterator.Open()` 도중 자식 에러 발생 시 양쪽 자식 `Close()` 호출 검증 (PASS)
23. `pkg/engine/ansi_sql_regression_test.go` (`TestAnsiSQL_NewBugsRegression` - Bug 23):
    - `SUBSTRING('hello', 0)` 및 `SUBSTRING('hello', -2)` 등 음수/0 위치 지정 시 문자열 끝이 절삭되지 않고 끝까지 정상 반환 검증 (PASS)
24. `pkg/engine/audit_round4_regression_test.go` (`TestAuditRound4_BracketStringAndHashJoin` - Bug 24):
    - `'[ADMIN]'`과 `'ADMIN'` 비교 시 대괄호가 무단 제거되어 오매칭되지 않고 분리 평가 검증 (PASS)
25. `pkg/engine/audit_round4_regression_test.go` (`TestAuditRound4_StoredProcNull3VL` - Bug 25):
    - 저장 프로시저 내 `IF @a = @b` 및 `IF @a <> 10` (둘 다 NULL) 비교 시 3VL(Unknown)로 평가되어 ELSE 분기 진입 검증 (PASS)
26. `pkg/engine/audit_round4_regression_test.go` (`TestAuditRound4_DivZeroAndOverflowAndDateDiff` - Bug 26):
    - `SELECT 10 / 0` 실행 시 사일런트 NULL이 아닌 명시적 `division by zero` 에러 반환 검증 (PASS)
27. `pkg/engine/audit_round4_regression_test.go` (`TestAuditRound4_DivZeroAndOverflowAndDateDiff` - Bug 27):
    - `DATEDIFF(week/hour/minute)` 경계선 통과 횟수 `(1, 1, 1)` 계산 공식 정합성 검증 (PASS)
28. `pkg/engine/audit_round4_regression_test.go` (`TestAuditRound4_DivZeroAndOverflowAndDateDiff` - Bug 28):
    - `9223372036854775807 * 2` 및 `+ 1` 오버플로 시 음수 래핑 없이 `arithmetic overflow: bigint out of range` 에러 반환 검증 (PASS)
29. `pkg/storage/wiredtiger/wt_composite_pk_test.go` (`TestWiredTiger_CompositePK_IndexScanAndFKCascade` - Bug 29):
    - WiredTiger 보조 인덱스 스캔 시 복합 PK 파싱 및 2건 온전 반환 검증 (PASS)
30. `pkg/storage/wiredtiger/wt_composite_pk_test.go` (`TestWiredTiger_CompositePK_IndexScanAndFKCascade` - Bug 30):
    - WiredTiger 복합 PK 외래키 `ON DELETE CASCADE` 연쇄 삭제 정합성 검증 (PASS)
31. `pkg/engine/audit_round4_regression_test.go` (`TestAuditRound4_MergeIntoCompositePK` - Bug 31):
    - 복합 PK 타깃 테이블에 대한 `MERGE INTO` (MATCHED UPDATE, NOT MATCHED INSERT, NOT MATCHED BY SOURCE DELETE) 정합성 검증 (PASS)
32. `pkg/backup/pitr_composite_pk_test.go` (`TestPITR_CompositePK_UpdateAndDelete` - Bug 32):
    - 복합 PK 테이블의 WAL `UPDATE`/`DELETE` 기록 PITR 복구 시 무결성 검증 (PASS)
33. `pkg/engine/audit_round4_regression_test.go` (`TestAuditRound4_BracketStringAndHashJoin` - Bug 33):
    - `'[code1]'`과 `'code1'` Hash Join 시 대괄호 제거로 인한 오조인(False Join) 방지 검증 (PASS)
34. `pkg/engine/open_error_cursor_leak_test.go` (`TestSetOpAndMergeJoin_OpenError_ClosesChildren` - Bug 34):
    - `SetOpIterator.Open()` 도중 우측 자식 에러 발생 시 양쪽 커서 안전 닫힘 검증 (PASS)
35. `pkg/engine/open_error_cursor_leak_test.go` (`TestSetOpAndMergeJoin_OpenError_ClosesChildren` - Bug 35):
    - `MergeJoinIterator.Open()` 초기 Pull 에러 시 양쪽 자식 커서 안전 닫힘 검증 (PASS)

---

## 4. 3단계 검증 및 커밋 파이프라인 (`[RULE:PipelineVerification]`)

1. **빌드 검증**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **테스트 검증**: `go test -v -race ./pkg/proc/... && go test -v -race ./pkg/execution/... && go test -v -race ./pkg/engine/...`
3. **커밋 및 문서화**:
   - `CHANGELOG.md` 갱신 (`[RULE:ChangelogDiscipline]`)
   - `./bin/sprint set <ID> DONE` 및 `TASKS.md` 백업 (`[RULE:ContinuousBacklog]`)
   - 무이모지 Conventional Commit 포맷 준수 (`[RULE:StyleIntegrity]`)
