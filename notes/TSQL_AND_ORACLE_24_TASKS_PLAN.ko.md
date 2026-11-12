# T-SQL / TDS 호환성 및 오라클 검증 대기 목록 상세 원인 분석 및 해결 설계서 (`#2` ~ `#25`)

> **작성일**: 2026-11-13
> **목적**: 전체 대기 목록 24건(기존 `#1 FEAT-DATADIR-ENGINE-MANIFEST-1` 완료 후 잔여 `#2` ~ `#24` 및 추가 등록된 CBO 통계 Bounded 태스크 `#25`)에 대해, 각 문제의 **정확한 발생 원인(Root Cause)**, **관련 소스코드 위치(Target Files)**, **구체적인 해결법 및 아키텍처 설계안(Proposed Solution)**을 단일 문서로 영속화하여 추적 및 Red-Green TDD 실행의 기준점(SSOT)으로 삼는다 (`[RULE:PlanPersistence]`).

---

## 1. 전체 목록 요약 매트릭스 (`#2` ~ `#25`)

| 순번 | 태스크 ID | 상태 | 도메인 | 우선순위 | 핵심 요약 |
| :---: | :--- | :---: | :---: | :---: | :--- |
| **#2** | **`FIX-GROUPBY-STRING-AGG-1`** | `DONE` (`1651a780`) | Execution / Planner | P1 | `GROUP BY` + `ORDER BY` 및 `WITHIN GROUP (ORDER BY ...)`에서 `STRING_AGG`가 `NULL`을 반환하는 버그 해결 |
| **#3** | **`FIX-PROC-TEMP-TABLE-DDL-1`** | `DONE` | Proc / VM | P1 | 저장 프로시저 본문 내부 `CREATE/DROP TABLE #temp` 및 `CREATE/DROP INDEX` DDL 컴파일·실행 지원 |
| **#4** | **`FIX-PROC-EXEC-RETURN-VAR-1`** | `PENDING` | Proc / VM / Engine | P1 | `EXEC @rc = sp_name ...` 프로시저 반환 상태 코드(`RETURN <int>`) 변수 캡처 바인딩 지원 |
| **#5** | **`FIX-SQL-PAREN-SELECT-1`** | `PENDING` | Parser / Engine / Planner | P1 | 괄호로 감싸진 `(SELECT ...)` 최상위 쿼리, 집합 연산 브랜치, 뷰/CTE 본문 실행 지원 |
| **#6** | **`FIX-CURSOR-LIFECYCLE-1`** | `PENDING` | Engine / VM | P1 | 커서 `DEALLOCATE` 및 배치 종료 시 세션 커서 레지스트리 정리 및 동일 이름 재선언 충돌 해결 |
| **#7** | **`FIX-VM-HIDDEN-ROWID-1`** | `PENDING` | Proc / VM | P1 | 명시적 PK가 없는 테이블에 대해 VDBE VM `OpInsert` 실행 시 숨은 `_row_id` 자동 발번 누락 수정 |
| **#8** | **`FIX-TSQL-INSERT-EXEC-1`** | `PENDING` | Parser / VM / Engine | P1 | `INSERT INTO <table> [(cols)] EXEC <proc_name> [args]` 프로시저 결과셋 테이블 적재 지원 |
| **#9** | **`FIX-SQL-DERIVED-VALUES-1`** | `PENDING` | Engine / Planner | P1 | `FROM (VALUES (...), (...)) AS alias(col1, col2)` 인라인 테이블 값 생성자 및 컬럼 별칭 파싱·실행 지원 |
| **#10** | **`FIX-TRIGGER-FOR-KEYWORD-1`** | `PENDING` | Parser / Engine | P1 | `CREATE TRIGGER ... FOR INSERT, UPDATE, DELETE` (`AFTER` 동의어) 구문 파싱 지원 |
| **#11** | **`FIX-TSQL-XACT-STATE-1`** | `PENDING` | Proc / Engine | P1 | `TRY...CATCH` 블록 내 트랜잭션 상태 반환 스칼라 함수 `XACT_STATE()` (`1`, `0`, `-1`) 지원 |
| **#12** | **`FIX-TSQL-CAST-MAX-AND-MONEY-1`** | `PENDING` | Parser / Eval | P1 | `CAST(... AS NVARCHAR(MAX)/VARCHAR(MAX)/VARBINARY(MAX))` 미평가 원문 에코 버그 및 `MONEY`/`SMALLMONEY` 4자리 소수점 스케일 보정 |
| **#13** | **`FIX-TDS-RPC-OUTPUT-TOKEN-1`** | `PENDING` | Wire (TDS) | P1 | TDS `RPCRequest`(`0x03`) 직접 프로시저 호출 시 `OUTPUT` 파라미터 `RETURNVALUE`(`0xAC`) 토큰 반환 및 기본값(`DEFAULT`) 바인딩 지원 |
| **#14** | **`FIX-TDS-RPC-RETURN-STATUS-1`** | `PENDING` | Wire (TDS) / Engine | P1 | TDS RPC 프로시저 `RETURN <int>` 실행 시 하드코딩된 `0` 대신 실제 종료 코드를 `RETURNSTATUS`(`0x79`) 토큰으로 전송 |
| **#15** | **`FIX-TSQL-TOPLEVEL-SELECT-ASSIGN-1`** | `PENDING` | Engine | P1 | 최상위 배치 및 `sp_executesql`에서 `SELECT @var = expr [FROM ...]` 구문을 비교식이 아닌 T-SQL 변수 대입으로 실행 |
| **#16** | **`FIX-TDS-PRINT-INFO-TOKEN-1`** | `PENDING` | Wire (TDS) | P1 | `PRINT` 문 실행 결과(`res.Messages`)를 TDS `INFO`(`0xAB`) 토큰으로 클라이언트에 실시간 전송 |
| **#17** | **`FIX-TDS-ERROR-NUMBER-MAP-1`** | `PENDING` | Wire (TDS) | P1 | PK/Unique/FK/Check 제약 조건 위반 시 SQL Server 표준 에러 번호(`2627`, `2601`, `547`) 매핑 반환 |
| **#18** | **`FIX-TSQL-JSON-FUNCTIONS-EXT-1`** | `PENDING` | Proc / Engine | P1 | `mssql-jdbc` 호환성: SQL Server `JSON` 컬럼 타입 별칭 및 `JSON_OBJECT`, `JSON_ARRAY`, `JSON_PATH_EXISTS` 내장 함수 지원 |
| **#19** | **`TEST-TDS-GOMSSQLDB-ORACLE-1`** | `PENDING` | Wire / Test | P1 | 공식 `microsoft/go-mssqldb` 드라이버 테스트 스위트 라이브 TDS 통합 오라클 이식 및 검증 |
| **#20** | **`TEST-TDS-PYMSSQL-ORACLE-1`** | `PENDING` | Protocol / Test | P1 | `pymssql` 공식 테스트 스위트(`test_sprocs.py`, `test_types.py`, `test_queries.py`) TDS/Gate 5 오라클 포팅 및 검증 |
| **#21** | **`TEST-TDS-MSSQL-JDBC-ORACLE-1`** | `PENDING` | Protocol / Test | P1 | `microsoft/mssql-jdbc` 공식 테스트 스위트 핵심 T-SQL 및 카탈로그 오라클 수확 및 검증 |
| **#22** | **`TEST-SQLGLOT-TSQL-ORACLE-1`** | `PENDING` | Engine / Test | P1 | `tobymao/sqlglot` `test_tsql.py` (526개 validate 케이스) T-SQL 표현식·내장함수·날짜약어·집합연산 오라클 검증 |
| **#23** | **`TEST-TDS-TEDIOUS-ORACLE-1`** | `PENDING` | Protocol / Test | P1 | `tediousjs/tedious` 통합 테스트(`rpc-test`, `datatypes-in-results`, `parameterised-statements`) 오라클 포팅 및 검증 |
| **#24** | **`TEST-GORM-SQLSERVER-ORACLE-1`** | `PENDING` | Engine / Test | P1 | `go-gorm/sqlserver` 드라이버 핵심 쿼리(`MERGE USING VALUES`, `OUTPUT INSERTED`, `sp_rename`, `sys.*` 조인) 오라클 검증 |
| **#25** | **`PERF-CBO-STATS-BOUNDED-1`** | `PENDING` | Planner / Storage | P2 | WiredTiger 원자적 실시간 `TableRowCount` 카운터 도입 및 `AnalyzeTable` 30,000행 Vitter 저수지 샘플링 상한 강제 |

---

## 2. 항목별 상세 원인 분석 및 해결 설계안

---

### `#2` `FIX-GROUPBY-STRING-AGG-1` (`DONE` - `1651a780`)
- **현상**:
  1. `SELECT grp, STRING_AGG(val, ',') FROM t GROUP BY grp ORDER BY grp`와 같이 `GROUP BY`와 `ORDER BY`가 함께 쓰이거나 별칭이 붙은 경우 `STRING_AGG` 결과가 `NULL`로 반환됨.
  2. `SELECT grp, STRING_AGG(val, ',') WITHIN GROUP (ORDER BY val DESC) FROM t GROUP BY grp` 구문에서 `NULL`이 반환되거나 그룹 내부 정렬이 수행되지 않음.
- **근본 원인 (Root Cause)**:
  1. **집계 컬럼 키 손실 (`pkg/execution/aggregate.go`)**: `AggregateIterator`가 `STRING_AGG(val, ',')`를 파싱할 때 1번째 인자(`val`)만 `aggs[i].Col`에 넣는 바람에, 결과 튜플의 `colIndexMap` 및 `evalMap`에 `"string_agg(val)"`만 등록되고 원래 식인 `"string_agg(val, ',')"`가 누락됨.
  2. **ProjectionIterator의 스칼라 재평가 덮어쓰기 (`pkg/execution/project.go`)**: `ORDER BY`가 존재하면 `AggregateIterator` 상위에 `SortIterator` -> `ProjectionIterator`가 배치됨. `ProjectionIterator`는 `*ColumnRefExpr`가 아닌 모든 AST 노드(`*FunctionCallExpr`, `*WithinGroupExpr`)에 대해 `proc.EvalRowExpr(proj.Expr, evalMap)`를 재호출하는데, `evalRowFunction`이 `STRING_AGG`를 집계 함수로 인식하지 못해 `<nil>`을 반환하며 이미 계산된 집계 슬롯을 `NULL`로 덮어씀.
  3. **플래너 `WithinGroupExpr` 집계 미인식 (`pkg/planner/planner.go`)**: `hasTopLevelAggregateExpr`가 `*proc.FunctionCallExpr`만 검사하고 `*proc.WithinGroupExpr`를 누락하여 `GROUP BY` 없는 단일 그룹 `STRING_AGG(...) WITHIN GROUP (...)` 쿼리가 `AggregatePlan`을 생성하지 못함.
- **해결 및 적용 완료 내역**:
  - `pkg/planner/planner.go`: `hasTopLevelAggregateExpr`에 `*proc.WithinGroupExpr` 분기 추가.
  - `pkg/execution/aggregate.go`: `aggSpec`에 `origExpr`, `orderByCol`, `orderByDesc` 필드를 추가하여 `WITHIN GROUP (ORDER BY col [ASC|DESC])`를 파싱하고, 그룹 내 `(val, orderKey)` 쌍을 정렬한 뒤 구분자로 결합. 결과 `colIndexMap`과 `evalMap`에 `origExpr`와 `Alias`를 모두 등록.
  - `pkg/execution/project.go` & `pkg/proc/eval.go`: `ProjectionIterator`가 `EvalRowExpr`를 호출하기 전에 `srcTuple`에 이미 계산된 집계 컬럼/별칭 슬롯이 있는지 먼저 확인하여 즉시 반환하도록 개선.

---

### `#3` `FIX-PROC-TEMP-TABLE-DDL-1` (`DONE`)
- **현상**:
  저장 프로시저(`CREATE PROCEDURE ... AS BEGIN ... END`) 본문 안에서 `CREATE TABLE #temp (...)`, `CREATE INDEX idx ON #temp (...)`, `DROP INDEX ...`, `DROP TABLE #temp` 실행 시 `[42000] Valid T-SQL syntax recognized ('CREATE statement'), but this feature is not yet supported` 에러가 발생함.
- **근본 원인 (Root Cause)**:
  - 최상위 SQL 실행기(`pkg/engine/sqlexec_ddl.go`)는 `#temp` 테이블 생성 및 인덱싱을 지원하지만, 프로시저 바이트코드 파서인 `pkg/proc/parser_stmt.go`의 `parseStatement()` 스위치문에 `TokenCreate`와 `TokenDrop` 분기가 빠져 있었음.
  - 또한 VDBE 바이트코드(`pkg/proc/bytecode.go`, `compiler.go`, `runtime/vm_dispatcher.go`)에 `OpCreateTable`, `OpDropTable`, `OpCreateIndex`, `OpDropIndex` 명령어가 정의되지 않았음.
- **해결 및 적용 완료 내역**:
  - `pkg/proc/parser_stmt.go`: `parseStatement()`에 `TokenCreate` (`parseCreateTable`, `ParseCreateIndex`) 및 `TokenDrop` (`parseDropTable`, `parseDropIndex`) 분기 추가.
  - `pkg/proc/bytecode.go` & `pkg/proc/compiler.go`: `OpCreateTable`, `OpDropTable`, `OpCreateIndex`, `OpDropIndex` 추가 및 컴파일 연결.
  - `pkg/proc/runtime/vm_dispatcher.go` & `vm.go`: `#temp` 로컬 임시 테이블 생성 시 프레임 고유 물리 테이블명(`__temp_<frame>_<ns>_<seq>_<name>`)을 부여하고 `f.tableVars[lowerName]`에 등록하여, 프로시저 내 `INSERT`/`UPDATE`/`DELETE`/`SELECT` 및 프로시저 종료 시 `resetForPool()` 자동 삭제(`DropTable`)와 WiredTiger 인메모리 바이패스(`PERF-HYBRID-TEMPMEM-1`)가 완벽히 연동되도록 구현.

---

### `#4` `FIX-PROC-EXEC-RETURN-VAR-1` (`PENDING`)
- **현상**:
  T-SQL 표준 문법인 `DECLARE @rc INT; EXEC @rc = sp_myproc @p1 = 10; SELECT @rc;` 실행 시 파싱 에러가 발생하거나 `@rc`에 프로시저의 `RETURN <int>` 종료 상태 코드가 저장되지 않음.
- **근본 원인 (Root Cause)**:
  1. **파서 (`pkg/proc/parser_stmt.go` `parseExec()`)**: `EXEC` 또는 `EXECUTE` 토큰 직후 프로시저 이름(식별자)만 기대하며, `@return_var = proc_name` 형태의 선행 `@변수 =` 토큰 시퀀스를 파싱하지 못함.
  2. **AST 및 바이트코드 (`pkg/proc/ast.go` `ExecStmt`, `pkg/proc/compiler.go`)**: `ExecStmt` 구조체에 반환값을 받을 변수명 필드(`ReturnVar string`)가 없으며, `OpExec` 실행기(`pkg/proc/runtime/vm_dispatcher.go`) 및 최상위 배치 실행기(`pkg/engine/sqlexec_procs.go`)가 하위 프로시저의 `Result.ReturnValue`를 호출자 변수 슬롯에 대입하지 않음.
- **해결 설계안 (Proposed Solution)**:
  1. `pkg/proc/ast.go`: `ExecStmt`에 `ReturnVar string` 필드 추가.
  2. `pkg/proc/parser_stmt.go`: `parseExec()`에서 `EXEC` 다음 토큰이 `@`로 시작하는 변수(`TokenIdent`)이고 그다음 토큰이 `=`(`TokenEq`)인 경우, 이를 `stmt.ReturnVar`로 캡처하고 뒤이은 프로시저 이름과 인자 목록을 파싱하도록 확장.
  3. `pkg/proc/runtime/vm_dispatcher.go` (`case proc.OpExec:`): 하위 프로시저 실행 완료 후 `stmt.ReturnVar != ""`이면 반환된 정수 상태 코드(기본값 `0`, `RETURN n` 실행 시 `n`)를 호출자 프레임의 `f.vars` / 레지스터 슬롯(`ReturnVar`)에 기록.
  4. `pkg/engine/sqlexec_procs.go`: 최상위 배치 또는 `sp_executesql`에서 `EXEC @rc = ...` 호출 시에도 세션 변수 맵(`sessionVars`)의 `@rc`에 `ReturnCode`를 기록하도록 동기화.

---

### `#5` `FIX-SQL-PAREN-SELECT-1` (`PENDING`)
- **현상**:
  1. `(SELECT 1 AS a)` 또는 `((SELECT a FROM t))`처럼 최상위 쿼리가 괄호로 감싸진 경우 실행 실패.
  2. `(SELECT 1 AS a) UNION ALL (SELECT 2 AS a) ORDER BY a`처럼 집합 연산(`UNION`/`INTERSECT`/`EXCEPT`)의 개별 브랜치가 괄호로 감싸진 경우 파싱/실행 실패.
  3. `CREATE VIEW v AS (SELECT ...)` 또는 `WITH cte AS ((SELECT ...))`에서 본문이 괄호로 감싸진 경우 실패.
- **근본 원인 (Root Cause)**:
  1. **쿼리 라우터 및 SELECT 진입점 (`pkg/engine/sqlexec.go`, `pkg/engine/sqlexec_select.go`)**: 최상위 문장 분류기가 첫 토큰이 `SELECT`나 `WITH`인지만 확인하고, 여는 괄호 `(`로 시작하는 SELECT 쿼리를 인식하지 못함.
  2. **집합 연산 분리기 (`pkg/engine/sqlexec_select.go`, `pkg/planner/setop.go`)**: `splitSetOperations` 및 각 브랜치 실행기가 브랜치 양끝의 매칭되는 외곽 괄호 `( ... )`를 벗겨내지(unwrap) 않아 하위 SELECT 파서가 `(`에서 구문 오류를 일으킴.
  3. **Pratt 파서 (`pkg/proc/parser_dml.go`)**: `parseSelect()`가 선행 여는 괄호 `(`로 감싸진 SELECT 문을 허용하지 않음.
- **해결 설계안 (Proposed Solution)**:
  1. `pkg/sqlutil`에 컨텍스트 인식 외곽 괄호 제거 함수 `sqlutil.UnwrapOuterParens(sql string) string`을 구현 (단, `(SELECT 1) UNION (SELECT 2)`처럼 첫 `(`가 중간에서 닫히고 뒤에 최상위 연산자가 이어지는 경우는 전체를 감싼 괄호가 아니므로 `sqlutil.FindMatchingParen`으로 정확히 끝 위치와 일치할 때만 벗김 — Invariant 7 & 12 준수).
  2. 최상위 쿼리 디스패처(`sqlexec.go`), `execSelectQuery`(`sqlexec_select.go`), 집합 연산 각 브랜치(`execSetOpSelect`), 그리고 뷰/CTE 본문 정규화 경로에서 `sqlutil.UnwrapOuterParens`를 적용하여 중첩 외곽 괄호를 안전하게 정규화.

---

### `#6` `FIX-CURSOR-LIFECYCLE-1` (`PENDING`)
- **현상**:
  동일 세션이나 배치 내에서 `DECLARE cur CURSOR FOR ...; OPEN cur; FETCH ...; CLOSE cur; DEALLOCATE cur;`를 수행한 뒤 다시 동일한 이름 `cur`로 `DECLARE cur CURSOR FOR ...`를 실행하면 `cursor already exists` 오류가 발생하거나 이전 커서 상태가 잔류함.
- **근본 원인 (Root Cause)**:
  1. **세션/코디네이터 커서 맵 정리 누락 (`pkg/engine/sqlexec_cursor.go`)**: `DEALLOCATE [GLOBAL|LOCAL] cursor_name` 실행 시 커서 구조체의 열림 상태만 닫거나 레지스트리 맵(`c.cursors` 또는 세션 커서 맵)에서 키 삭제가 대소문자 정규화(`strings.ToLower`) 불일치 등으로 누락되는 경로가 존재함.
  2. **VDBE VM 프로시저 커서 정리 (`pkg/proc/runtime/vm_dispatcher.go`)**: 프로시저 내부에서 `OpCursorDeallocate` 실행 시 또는 프로시저 프레임 종료(`resetForPool`) 시 `LOCAL` 커서가 즉시 해제되지 않거나, 동일 프로시저 내 루프/분기에서 `DEALLOCATE` 후 재선언 시 기존 항목과 충돌함.
- **해결 설계안 (Proposed Solution)**:
  1. `pkg/engine/sqlexec_cursor.go` 및 `pkg/proc/runtime/vm_dispatcher.go`에서 커서 식별자 키를 항상 `strings.ToLower(strings.TrimSpace(name))`로 정규화.
  2. `DEALLOCATE` 실행 시 열린 리소스(`Close()`)를 즉시 정리하고 레지스트리 맵에서 `delete(cursors, key)`를 확정 수행하여 후속 `DECLARE`가 깨끗한 슬롯에서 실행되도록 보장.
  3. 프로시저 프레임 종료 시 프레임에 귀속된 로컬 커서를 전수 `Close()` 및 제거(`defensive teardown`).

---

### `#7` `FIX-VM-HIDDEN-ROWID-1` (`PENDING`)
- **현상**:
  명시적인 `PRIMARY KEY` 없이 생성된 테이블(예: `CREATE TABLE t (a INT, b VARCHAR(20))`)에 대해, 최상위 SQL에서는 `INSERT`가 잘 되지만 **저장 프로시저 내부(VDBE VM `OpInsert`)**에서 `INSERT INTO t VALUES (1, 'x')`를 실행하면 PK 누락 오류 또는 `_row_id` NULL/중복 충돌이 발생함.
- **근본 원인 (Root Cause)**:
  - LionSQL에서 명시적 PK가 없는 테이블은 `schema.EnsurePrimaryKey("_row_id")`를 통해 숨은 기본키 컬럼 `_row_id` (`Hidden: true, PrimaryKey: true`)를 자동으로 갖는다.
  - 최상위 DML 실행기(`pkg/engine/sqlexec_dml.go`)는 `INSERT` 시 `_row_id` 컬럼에 원자적 단조 증가 번호(`nextRowID` / `NextIdentityValue`)를 자동 주입하지만, VDBE VM의 `OpInsert` 핸들러(`pkg/proc/runtime/vm_dispatcher.go:915-1030`)는 `col.Identity`인 컬럼만 자동 발번하고 `col.Name == "_row_id"`(또는 `col.Hidden && col.PrimaryKey`)인 컬럼에 대한 자동 발번 분기가 누락되어 있음.
- **해결 설계안 (Proposed Solution)**:
  - `pkg/proc/runtime/vm_dispatcher.go`의 `case proc.OpInsert:`(단건/다건 `VALUES` 삽입 및 `execInsertSelect`)에서 테이블 스키마의 PK 컬럼이 `_row_id`(`col.Hidden` 또는 `strings.EqualFold(col.Name, "_row_id")`)이고 레코드에 값이 지정되지 않은 경우, `f.executor.NextIdentityValue(targetTable, 1, 1)` (또는 원자적 시퀀스 카운터)를 호출하여 고유한 `TypeInt` `_row_id`를 자동 할당하도록 수정.

---

### `#8` `FIX-TSQL-INSERT-EXEC-1` (`PENDING`)
- **현상**:
  T-SQL에서 매우 빈번히 쓰이는 `INSERT INTO target_table (col1, col2) EXEC sp_get_rows @p1 = 1;` 구문이 파싱 에러로 거부됨.
- **근본 원인 (Root Cause)**:
  1. **파서 (`pkg/proc/parser_dml.go` `parseInsert()`)**: `INSERT INTO <table> [(cols)]` 다음에 `VALUES`, `SELECT`, `DEFAULT VALUES`만 허용하고 `EXEC` / `EXECUTE` 토큰 분기를 처리하지 않음.
  2. **실행기 (`pkg/engine/sqlexec_dml.go` & `pkg/proc/runtime/vm_dispatcher.go`)**: 프로시저(또는 `sp_executesql`)를 실행하여 반환된 결과셋(`Rows`)을 대상 테이블의 컬럼 순서에 맞춰 트랜잭션 내부에서 `Insert`하는 실행 경로가 없음.
- **해결 설계안 (Proposed Solution)**:
  1. `pkg/proc/ast.go` (`InsertStmt`) 및 `pkg/proc/bytecode.go` (`DMLData`)에 `Exec *ExecStmt` 필드를 추가.
  2. `pkg/proc/parser_dml.go` (`parseInsert`): `VALUES` / `SELECT` 외에 `TokenExec` (`EXEC` / `EXECUTE`)가 오면 `parseExec()`를 호출하여 `InsertStmt.Exec`에 연결.
  3. `pkg/engine/sqlexec_dml.go` 및 `pkg/proc/runtime/vm_dispatcher.go` (`OpInsert`): `data.Exec != nil`일 때 대상 프로시저를 현재 트랜잭션 컨텍스트에서 실행하고, 반환된 결과셋 행들을 순서대로 대상 테이블에 삽입(스키마 타입 강제변환, `_row_id`/`IDENTITY` 자동 채번, 제약 조건 검사 포함)한 뒤 삽입된 행 수를 `@@ROWCOUNT`에 반영. (단, SQL Server 표준과 동일하게 중첩 `INSERT ... EXEC` 내부의 `INSERT ... EXEC`는 최대 깊이 가드 적용 `[RULE:BoundedRecursion]`).

---

### `#9` `FIX-SQL-DERIVED-VALUES-1` (`PENDING`)
- **현상**:
  `SELECT v.id, v.name FROM (VALUES (1, 'Alice'), (2, 'Bob')) AS v(id, name) WHERE v.id = 2`와 같은 인라인 테이블 값 생성자(Table Value Constructor) 파생 테이블 쿼리가 실행되지 않음.
- **근본 원인 (Root Cause)**:
  - `pkg/engine/sqlexec_derived.go` (파생 테이블 구체화기) 및 `pkg/planner/planner.go`가 `FROM ( ... ) AS alias` 내부가 항상 `SELECT`나 `WITH`로 시작한다고 가정함.
  - 또한 파생 테이블 별칭 뒤의 괄호 컬럼 목록 `AS v(id, name)`에서 `(id, name)`을 파싱하여 생성된 파생 테이블의 컬럼 이름(`column1`, `column2` -> `id`, `name`)에 매핑하는 로직이 일부 경로에서 빠져 있음.
- **해결 설계안 (Proposed Solution)**:
  1. 파생 테이블 별칭 파서(`pkg/engine/sqlexec_derived.go`, `pkg/sqlutil`)에서 `AS alias (col1, col2, ...)` 형태의 컬럼 별칭 리스트를 추출하는 모듈화 헬퍼 추가 (`[RULE:ModularAbstraction]`).
  2. 파생 테이블 내부 쿼리가 `VALUES (...)`로 시작하는 경우, 각 튜플 식을 평가하여 행들을 생성하고 지정된 컬럼 별칭 `(col1, col2, ...)`(미지정 시 `column1, column2, ...`)으로 인메모리 파생 테이블 스키마 및 튜플을 구성.
  3. 일반 `FROM (SELECT 1, 2) AS t(a, b)`에 대해서도 컬럼 별칭 재매핑 `(a, b)`가 일관되게 동작하도록 통합.

---

### `#10` `FIX-TRIGGER-FOR-KEYWORD-1` (`PENDING`)
- **현상**:
  T-SQL 레거시 및 표준 구문인 `CREATE TRIGGER trg_audit ON orders FOR INSERT, UPDATE AS BEGIN ... END` 실행 시 `FOR` 키워드 위치에서 구문 에러가 발생함. (`AFTER INSERT`만 허용됨).
- **근본 원인 (Root Cause)**:
  - T-SQL에서 `FOR INSERT`는 `AFTER INSERT`와 완전히 동일한 동의어(Synonym)이나, `pkg/proc/parser_ddl.go` (`parseCreateTrigger`) 및 `pkg/engine/sqlexec_ddl.go`의 트리거 헤더 파서가 타이밍 키워드로 `BEFORE`, `AFTER`, `INSTEAD OF`만 검사하고 `FOR` 토큰을 `AFTER`로 매핑하지 않음.
- **해결 설계안 (Proposed Solution)**:
  - `pkg/proc/parser_ddl.go`와 `pkg/engine/sqlexec_ddl.go`의 트리거 정의 파서에서 `ON <table_name>` 다음 토큰이 `FOR`(`TokenFor`)인 경우 이를 `AFTER` 타이밍(`TriggerAfter`)으로 정규화하여 파싱하도록 수정.

---

### `#11` `FIX-TSQL-XACT-STATE-1` (`PENDING`)
- **현상**:
  저장 프로시저나 배치의 `BEGIN TRY ... END TRY BEGIN CATCH ... IF XACT_STATE() <> 0 ROLLBACK TRANSACTION; END CATCH` 구문에서 `XACT_STATE()` 호출 시 알 수 없는 함수 오류가 발생하거나 올바른 트랜잭션 상태값을 반환하지 않음.
- **근본 원인 (Root Cause)**:
  - SQL Server에서 `XACT_STATE()`는 현재 세션의 활성 트랜잭션 상태를 정수로 반환하는 필수 에러 핸들링 함수임:
    - `1`: 활성 사용자 트랜잭션이 존재하며 커밋 가능한 상태
    - `0`: 활성 사용자 트랜잭션이 없음 (Autocommit 상태)
    - `-1`: 활성 트랜잭션이 존재하지만 심각한 에러가 발생하여 커밋할 수 없고 `ROLLBACK`만 가능한 상태 (Uncommittable Transaction)
  - 현재 `pkg/proc/builtins.go` 및 VM/코디네이터 세션 컨텍스트에 `XACT_STATE()` 내장 함수가 연결되어 있지 않음.
- **해결 설계안 (Proposed Solution)**:
  1. VDBE VM 프레임(`pkg/proc/runtime/vm.go`) 및 엔진 세션(`pkg/engine/coordinator.go`)에서 현재 활성 트랜잭션 여부(`inTransaction` / `activeTx != nil`)와 언커밋 불가 플래그(`txUncommittable`)를 추적.
  2. `pkg/proc/builtins.go` 및 VM 표현식 평가기(`pkg/proc/runtime/vm.go`)에 `XACT_STATE()`를 등록하여 활성 트랜잭션이 있으면 `1`(언커밋 불가 시 `-1`), 없으면 `0`을 `storage.TypeInt`로 반환.

---

### `#12` `FIX-TSQL-CAST-MAX-AND-MONEY-1` (`PENDING`)
- **현상**:
  1. `SELECT CAST('hello' AS NVARCHAR(MAX))` 또는 `CONVERT(VARCHAR(MAX), 123)` 실행 시 변환된 값 `'hello'`가 나오는 대신, 미평가된 원문 SQL 문자열 `"CAST('hello' AS NVARCHAR(MAX))"`가 그대로 반환되거나 파싱 오류 발생.
  2. `CAST('1.23456' AS MONEY)` 또는 `SMALLMONEY` 변환 시 SQL Server 표준 고정 소수점 4자리(`1.2346` / 스케일 4) 대신 일반 부동소수점 또는 2자리 Decimal로 처리됨.
- **근본 원인 (Root Cause)**:
  1. **Pratt 파서 `parseTypeSpec` (`pkg/proc/parser_expr.go`)**: `CAST(expr AS type)` 및 `CONVERT(type, expr)` 내부에서 타입 뒤의 `(precision)`을 파싱할 때 정수 토큰(`TokenInt`)만 허용하고 `MAX` 식별자(`TokenIdent` `"MAX"`)를 처리하지 못해 파싱에 실패함. 파싱 실패 시 상위 폴백이 식 전체를 문자열 리터럴/원문으로 취급하여 에코 버그가 발생함.
  2. **`MONEY` / `SMALLMONEY` 스케일 (`pkg/proc/eval.go`, `pkg/proc/builtins.go`)**: `CAST`/`CONVERT` 타입 변환기에서 `MONEY`와 `SMALLMONEY`를 스케일 4인 `TypeDecimal`(또는 4자리 고정 반올림)로 정규화하지 않음.
- **해결 설계안 (Proposed Solution)**:
  1. `pkg/proc/parser_expr.go` (`parseTypeSpec`) 및 `pkg/proc/parser_ddl.go`: 타입 길이 괄호 `( ... )` 내부에 `MAX` 토큰이 오면 정당한 가변 최대 길이(예: precision `-1` 또는 `0`)로 소비하고 `)`를 정상 닫도록 수정.
  2. `pkg/proc/eval.go` (`evalCast`): 대상 타입이 `MONEY` 또는 `SMALLMONEY`인 경우 소수점 4자리(`Scale: 4`) `Decimal`로 반올림 및 포맷팅하도록 보정.

---

### `#13` `FIX-TDS-RPC-OUTPUT-TOKEN-1` (`PENDING`)
- **현상**:
  `go-mssqldb`, `pymssql`, `tedious`, `mssql-jdbc` 등 표준 TDS 드라이버가 `SQLBatch`(`0x01`) 텍스트가 아닌 **바이너리 `RPCRequest`(`0x03`) 패킷**으로 저장 프로시저를 직접 호출할 때:
  1. `OUTPUT` / `INOUT` 파라미터의 변경된 값이 드라이버의 출력 변수에 채워지지 않음.
  2. 프로시저 정의에 기본값(`@p2 INT = 42`)이 있는 파라미터를 RPC 패킷에서 생략했을 때 `NULL`로 바인딩되어 기본값이 무시됨.
- **근본 원인 (Root Cause)**:
  1. **TDS `RETURNVALUE` (`0xAC`) 토큰 미전송 (`pkg/wire/tds.go`)**: `handleTDSRPC`에서 `sp_executesql`이 아닌 일반 사용자 저장 프로시저를 호출할 때, 실행 후 변경된 `OUTPUT` 파라미터들을 TDS `0xAC` (`RETURNVALUE`) 바이너리 토큰 스트림으로 직렬화하여 `RETURNSTATUS`(`0x79`) 및 `DONEPROC`(`0xFE`)와 함께 클라이언트에 내려주는 로직이 누락되어 있음.
  2. **생략된 기본값 파라미터 처리**: RPC 호출 시 전달되지 않은 파라미터나 `fDefaultValue` 플래그가 켜진 파라미터에 대해 프로시저 AST(`CreateProcedureStmt.Params`)의 `Default` 표현식을 평가하여 바인딩하는 과정이 빠져 있음.
- **해결 설계안 (Proposed Solution)**:
  1. `pkg/wire/tds.go` (`handleTDSRPC`): 직접 프로시저 호출 시 카탈로그의 프로시저 파라미터 목록을 조회하여, 호출자가 생략했거나 `Default` 플래그를 보낸 파라미터에 대해 정의된 기본값을 바인딩.
  2. `OUTPUT` 플래그(`status & 0x01 != 0`)가 설정된 파라미터들을 프로시저 실행 후 수집하여, 각 파라미터의 순서(ordinal), 이름(`@param`), 타입 메타데이터 및 변경된 값을 TDS `RETURNVALUE`(`0xAC`) 토큰 패킷으로 정확히 인코딩하여 응답 스트림에 기록.

---

### `#14` `FIX-TDS-RPC-RETURN-STATUS-1` (`PENDING`)
- **현상**:
  저장 프로시저가 `RETURN 42;`와 같이 비-0 상태 코드를 반환했음에도, TDS RPC 호출 클라이언트가 받는 `ReturnStatus` 값이 항상 `0`으로 고정됨.
- **근본 원인 (Root Cause)**:
  1. `pkg/engine/sqlexec_procs.go`가 VDBE VM 실행 결과(`runtime.Result.ReturnValue`)를 `engine.QueryResult` 구조체의 별도 필드(`ReturnStatus int32` / `HasReturnStatus bool`)로 상위에 전달하지 않음.
  2. `pkg/wire/tds.go`가 RPC 응답 패킷 끝부분에 `RETURNSTATUS`(`0x79`) 토큰을 쓸 때 `0`을 하드코딩하여 기록함 (`writeReturnStatus(0)`).
- **해결 설계안 (Proposed Solution)**:
  1. `pkg/engine/sqlexec.go`의 `QueryResult` 구조체에 `ReturnStatus int32` 및 `HasReturnStatus bool` 필드를 추가하고, `sqlexec_procs.go`에서 프로시저 VM 실행 완료 시 `vmResult.ReturnValue`를 정수 변환하여 설정.
  2. `pkg/wire/tds.go`에서 `RETURNSTATUS`(`0x79`, 4바이트 Little-Endian `int32`) 토큰을 기록할 때 `res.ReturnStatus` 값을 전송하도록 수정.

---

### `#15` `FIX-TSQL-TOPLEVEL-SELECT-ASSIGN-1` (`PENDING`)
- **현상**:
  저장 프로시저 내부가 아닌 **최상위 SQL 배치** 또는 `sp_executesql` 내에서 `DECLARE @x INT; SELECT @x = 100; SELECT @x;` 또는 `SELECT @x = price FROM items WHERE id = 1;`을 실행하면, `@x`에 값이 대입되지 않고 `@x = 100`이 boolean 동등 비교식(`NULL = 100`)으로 평가되어 불필요한 결과셋 행이 반환됨.
- **근본 원인 (Root Cause)**:
  - 저장 프로시저 컴파일러(`pkg/proc/compiler.go`)는 `SELECT @var = expr`를 변수 대입(`OpSelectIntoVars` / `OpAssign`)으로 처리하지만, 최상위 SELECT 실행기(`pkg/engine/sqlexec_select.go`)는 프로젝션 리스트의 `@var = expr` 패턴을 감지하지 않고 일반 스칼라 비교식 `BinaryExpr(Op: "=")`로 평가함.
- **해결 설계안 (Proposed Solution)**:
  - `pkg/engine/sqlexec_select.go`: 최상위 `SELECT` 프로젝션 항목들이 `@var = expr` 형태인지 컨텍스트 인식 파서(`sqlutil`)로 검사.
  - 변수 대입 `SELECT`인 경우:
    - `FROM` 절이 없으면 우변 `expr`를 평가하여 즉시 `sessionVars[varName]`에 대입하고 빈 결과셋(RowCount 1, 결과셋 컬럼 없음)을 반환.
    - `FROM` 절이 있으면 쿼리를 실행하되 결과 행(마지막 행 기준, 0행이면 기존 변수값 유지 — SQL Server 표준 시맨틱)의 우변 식 평가값을 `sessionVars[varName]`에 대입하고 클라이언트에는 결과셋 행을 반환하지 않음.

---

### `#16` `FIX-TDS-PRINT-INFO-TOKEN-1` (`PENDING`)
- **현상**:
  T-SQL `PRINT 'Hello World'` 또는 `RAISERROR('info msg', 10, 1)` (심각도 <= 10 정보성 메시지) 실행 시, 엔진 내부 `QueryResult.Messages` 슬라이스에는 메시지가 담기지만 TDS 와이어 프로토콜(`pkg/wire/tds.go`)이 이를 클라이언트로 내려보내지 않아 SSMS 메시지 탭이나 드라이버 `InfoMessage` 콜백에서 출력이 누락됨.
- **근본 원인 (Root Cause)**:
  - `pkg/wire/tds.go`의 배치 및 RPC 응답 작성기가 `res.Columns`와 `res.Rows`만 직렬화하고, `res.Messages` 배열을 TDS `INFO` 토큰(`0xAB`)으로 변환하여 기록하는 단계가 빠져 있음.
- **해결 설계안 (Proposed Solution)**:
  - `pkg/wire/tds.go`에 `writeTDSInfoToken(buf *bytes.Buffer, number int32, state byte, class byte, msg string)` 헬퍼를 구현하여, `res.Messages`에 담긴 각 메시지를 TDS `0xAB` (`INFO` 토큰: 에러 번호 `0`, 상태 `1`, 심각도 클래스 `0`, UTF-16LE 메시지 문자열)로 직렬화하여 응답 패킷에 포함.

---

### `#17` `FIX-TDS-ERROR-NUMBER-MAP-1` (`PENDING`)
- **현상**:
  PK 중복, Unique 인덱스 위반, Foreign Key 위반, Check 제약 위반 발생 시 TDS 에러 토큰(`0xAA`)의 에러 번호(`Number`)가 모두 일반 번호(`50000`)로 내려가서, 클라이언트 ORM(`GORM`, `Entity Framework`, `JDBC`, `go-mssqldb`)이 제약 조건 위반 종류를 구분하지 못함.
- **근본 원인 (Root Cause)**:
  - `pkg/wire/tds.go`의 `writeTDSError` 호출부가 에러 메시지나 `storage.Err*` 센티넬 에러를 SQL Server 표준 에러 번호로 매핑하지 않고 고정 번호(`50000`)를 사용하고 있음.
- **해결 설계안 (Proposed Solution)**:
  - `pkg/wire/tds.go`에 에러 분류 매퍼 `mapErrorToTDSErrorNumber(err error) (number int32, class byte, state byte)`를 추가하여 SQL Server 표준 번호를 정확히 반환:
    - `storage.ErrKeyExists` / PRIMARY KEY 위반 -> **`2627`** (Severity `14`)
    - `storage.ErrUniqueViolation` / UNIQUE 인덱스 위반 -> **`2601`** (Severity `14`)
    - `storage.ErrForeignKeyViolation` / `storage.ErrCheckViolation` -> **`547`** (Severity `16`)
    - `storage.ErrNotNullViolation` -> **`515`** (Severity `16`)
    - `storage.ErrTableNotFound` -> **`208`** (`Invalid object name`, Severity `16`)
    - `storage.ErrTableExists` -> **`2714`** (`There is already an object named ...`, Severity `16`)
    - 데드락 / 직렬화 충돌(`IsWriteConflict`) -> **`1205`** (Severity `13`)

---

### `#18` `FIX-TSQL-JSON-FUNCTIONS-EXT-1` (`PENDING`)
- **현상**:
  SQL Server 2022 / Azure SQL 및 `mssql-jdbc` `JSONFunctionTest.java`에서 사용하는 최신 표준 JSON 함수인 `JSON_OBJECT('key': val, ...)`, `JSON_ARRAY(v1, v2, ...)`, `JSON_PATH_EXISTS(json_doc, '$.path')` 및 `JSON` 네이티브 컬럼 타입 선언이 지원되지 않거나 일부 구문(`'k': v` 콜론 키-값 문법, `NULL ON NULL` / `ABSENT ON NULL`)에서 실패함.
- **근본 원인 (Root Cause)**:
  1. `pkg/proc/parser_expr.go`: `JSON_OBJECT` 내부의 `'key': value` 콜론(`:`) 구분자 및 `NULL ON NULL` / `ABSENT ON NULL` 옵션 절을 함수 인자 파서가 처리하지 못함.
  2. `pkg/proc/builtins.go`: `JSON_OBJECT`, `JSON_ARRAY`, `JSON_PATH_EXISTS` 내장 함수 구현이 누락되었거나 SQL Server 반환 규칙(`JSON_PATH_EXISTS`는 존재 시 `1`, 미존재/에러 시 `0` `INT` 반환)과 차이가 있음.
- **해결 설계안 (Proposed Solution)**:
  1. `pkg/proc/parser_expr.go`: `JSON_OBJECT(...)` 및 `JSON_ARRAY(...)` 파싱 시 `key : value` 쌍과 `NULL ON NULL` / `ABSENT ON NULL` 옵션을 정규 인자 시퀀스로 변환하도록 지원.
  2. `pkg/proc/builtins.go`:
     - `JSON_OBJECT`: 키-값 인자 쌍들을 받아 유효한 JSON 객체 문자열 생성 (`ABSENT ON NULL`이 기본값인 SQL Server 동작 및 `NULL ON NULL` 옵션 준수).
     - `JSON_ARRAY`: 가변 인자들을 받아 JSON 배열 문자열 생성 (기본적으로 `NULL` 원소는 생략, `NULL ON NULL` 지정 시 `null` 포함).
     - `JSON_PATH_EXISTS(expr, path)`: 경로가 존재하면 `1`, 존재하지 않거나 유효하지 않은 JSON이면 `0` (입력 `expr` 또는 `path`가 `NULL`이면 `NULL`) 반환.

---

### `#19` ~ `#24` 외부 공식 드라이버 및 파서 코퍼스 오라클 검증 (`TEST-*-ORACLE-1` 6건)
위의 `#2` ~ `#18` 엔진·파서·TDS 프로토콜 결함 패치가 완료되면, 이미 `test/corpora/`에 벤더링된 5대 외부 코퍼스 및 공식 Go TDS 드라이버를 대상으로 실제 오라클 스위트 검증을 수행한다:

1. **`#19` `TEST-TDS-GOMSSQLDB-ORACLE-1`**:
   - **원인/배경**: Go 생태계 표준 SQL Server 드라이버인 `microsoft/go-mssqldb`를 통해 라이브 TDS 포트(`:1433`)에 연결했을 때 파라미터 바인딩(`@p1`), `sql.Out{Dest: &outVar}`, 프로시저 반환 코드, 트랜잭션 롤백, 에러 번호(`mssql.Error.Number`)가 100% 호환되는지 검증 필요.
   - **해결법**: `pkg/wire/tds_gomssqldb_oracle_test.go` 통합 테스트를 구성하여 RPC 호출, `OUTPUT` 파라미터, `RETURN` 상태값, `PRINT` 메시지, 제약 조건 에러 번호(`2627`, `2601`, `547`), 대용량 `NVARCHAR(MAX)` 스트리밍을 전수 검증.
2. **`#20` `TEST-TDS-PYMSSQL-ORACLE-1`**:
   - **원인/배경**: `test/corpora/pymssql/tests/`(`test_sprocs.py`의 17개 고정/가변 타입 `OUTPUT` 프로시저, `test_types.py`, `test_queries.py`)에서 추출한 T-SQL 프로시저 및 타입 변환 케이스 검증.
   - **해결법**: `#4`, `#12`, `#13`, `#14` 패치 후 `test/slt_proc/corpora/pymssql_*.slt` 및 TDS RPC 테스트를 확장하여 100% 통과 확인.
3. **`#21` `TEST-TDS-MSSQL-JDBC-ORACLE-1`**:
   - **원인/배경**: `test/corpora/mssql-jdbc/`의 `JSONFunctionTest`, `CallableStatementTest`, `PreparedStatementTest`, `DatabaseMetaDataTest` 핵심 쿼리 호환성 검증.
   - **해결법**: `#18`(`JSON_OBJECT`, `JSON_ARRAY`, `JSON_PATH_EXISTS`) 및 카탈로그/RPC 패치 후 `mssql_jdbc_*.slt` 오라클 스위트 확장 및 100% 통과 확인.
4. **`#22` `TEST-SQLGLOT-TSQL-ORACLE-1`**:
   - **원인/배경**: `test/corpora/sqlglot/test_tsql.py` (3,274줄, 526개 T-SQL 변환/검증 케이스) 중 순수 OLTP T-SQL 내장 함수, 날짜 약어(`dd`, `mm`, `yyyy`), `CAST`/`CONVERT`, `STRING_AGG`, 파생 `VALUES` 테이블 케이스 검증.
   - **해결법**: `#2`, `#5`, `#9`, `#12` 패치 후 `sqlglot_tsql_*.slt` 오라클 스위트를 확장하여 실행 결과값 대조 검증.
5. **`#23` `TEST-TDS-TEDIOUS-ORACLE-1`**:
   - **원인/배경**: Node.js 표준 TDS 드라이버 `test/corpora/tedious/`의 `datatypes-in-results`, `parameterised-statements`, `rpc-test`, `transactions`(`XACT_STATE`, `TRY...CATCH`) 검증.
   - **해결법**: `#11`, `#13`, `#14`, `#15`, `#16` 패치 후 `tedious_*.slt` 및 TDS 와이어 테스트로 100% 통과 검증.
6. **`#24` `TEST-GORM-SQLSERVER-ORACLE-1`**:
   - **원인/배경**: Go ORM `test/corpora/gorm-sqlserver/`가 생성하는 `MERGE INTO ... USING (VALUES ...) AS excluded(...)`, `OUTPUT INSERTED.*`, `sp_rename`, `sys.indexes`/`sys.foreign_keys`/`sys.check_constraints` 스키마 인트로스펙션 조인 쿼리 검증.
   - **해결법**: `#9`(파생 `VALUES` 컬럼 별칭) 패치와 연동하여 `gorm_sqlserver_*.slt` 및 통합 테스트로 GORM 마이그레이션·CRUD 패턴 100% 통과 검증.

---

### `#25` `PERF-CBO-STATS-BOUNDED-1` (`PENDING`)
- **현상 및 배경 (MySQL 통계 지연/메모리 스파이크 대비)**:
  MySQL(InnoDB)은 대량 데이터 적재 직후 `ANALYZE TABLE`이 제때 돌지 않으면 과거 통계(예: 0건~수건)에 고착되어 잘못된 조인 순서나 풀스캔을 선택하는 성능 저하가 빈번함.
  반면 LionSQL은:
  1. `memengine`에서는 `TableRowCount` 인터페이스로 실시간 $O(1)$ 행 수를 즉시 CBO에 제공하지만, **`wiredtiger` 엔진은 아직 `TableRowCount(tableName string) (int, bool)`을 구현하지 않아** `ANALYZE` 전까지 기본값 `1,000`행으로 추정함.
  2. `pkg/engine/sqlexec_explain.go`의 `AnalyzeTable()`이 테이블 전체 행을 `var allValues [][]storage.Value` 슬라이스에 무제한 적재(`append`)하고 있어, 수백만 행 테이블에서 `ANALYZE` 실행 시 순간적인 메모리 스파이크가 발생할 수 있음 (`[RULE:BoundedBuffers]` 위반 소지).
- **근본 원인 (Root Cause)**:
  1. `pkg/storage/wiredtiger/wt_engine.go`: 테이블별 실시간 행 수 원자적 카운터 및 `TableRowCount(tableName string) (int, bool)` 메서드가 미구현됨.
  2. `pkg/engine/sqlexec_explain.go` (`AnalyzeTable`): 히스토그램 산출을 위해 최대 샘플 크기 상한 없이 전체 행을 슬라이스에 보관함.
- **해결 설계안 (Proposed Solution)**:
  1. **WiredTiger 실시간 `TableRowCount` 구현 (`pkg/storage/wiredtiger/`)**:
     - 테이블 메타데이터/인스턴스별로 원자적 카운터(`atomic.Int64`)를 두고 `Insert`/`Delete`/`Truncate` 커밋 시 증감하며, `TableRowCount(tableName string) (int, bool)`을 노출하여 `ANALYZE`가 아직 돌지 않은 직후에도 CBO 플래너(`pkg/planner/cost.go`)가 항상 실시간 행 수를 즉시 반영하도록 개선.
  2. **`AnalyzeTable` 30,000행 Vitter 저수지 샘플링(Reservoir Sampling) 상한 적용 (`pkg/engine/sqlexec_explain.go`)**:
     - PostgreSQL 기본 통계 샘플링 표준(`300 * default_statistics_target = 30,000`행)과 동일하게 `const maxAnalyzeSampleRows = 30000` 상한을 적용.
     - 전체 행 수(`totalRows`)와 `DistinctCount`는 스트리밍 스캔 중 정확히 집계하되, 히스토그램용 샘플 슬라이스(`sampleRows`)는 최대 30,000행까지만 유지하고 이후 행은 Vitter's Algorithm R 확률 교체(`j := storage.FastRandN(totalRows); if j < maxAnalyzeSampleRows { sampleRows[j] = row }`)로 갱신하여 천만 건 테이블에서도 메모리 사용량을 수 MB 이내로 엄격히 제한 (`[RULE:BoundedBuffers]`).
