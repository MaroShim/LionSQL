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

---

## 4. 3단계 검증 및 커밋 파이프라인 (`[RULE:PipelineVerification]`)

1. **빌드 검증**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **테스트 검증**: `go test -v -race ./pkg/proc/... && go test -v -race ./pkg/execution/... && go test -v -race ./pkg/engine/...`
3. **커밋 및 문서화**:
   - `CHANGELOG.md` 갱신 (`[RULE:ChangelogDiscipline]`)
   - `./bin/sprint set <ID> DONE` 및 `TASKS.md` 백업 (`[RULE:ContinuousBacklog]`)
   - 무이모지 Conventional Commit 포맷 준수 (`[RULE:StyleIntegrity]`)
