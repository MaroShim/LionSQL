# LionSQL 실시간 엔진 감사 보고서 - Round 10 (근본 원인 압축 결함 목록)

**대상**: LionSQL v0.1.0-alpha (스토리지 엔진: WiredTiger 및 In-Memory OCC)  
**감사 중점 영역**: Volcano 실행 엔진, 비용 기반 최적화기(CBO), 윈도우 함수 및 저장 프로시저 가상 머신(VM)  
**식별된 고유 근본 결함 수**: 10건  
**감사 일시**: 2026-11-19  
**원본 출처**: `/private/tmp/20261119_lionsql_live_audit_report_round10.txt`

---

## 1. 개요 및 총평

Round 9에 이어 진행된 Round 10 실시간 라이브 감사에서는 고밀도 근본 결함 분석 방법론을 적용하였습니다.
실제 구동 중인 엔진을 대상으로 Volcano 실행 엔진, 윈도우 함수 연산, CBO 옵티마이저 구문 검증, T-SQL 저장 프로시저 가상 머신(VM) 영역에 대한 대화형 라이브 프로브를 실행하였으며, 총 10건의 핵심 아키텍처 결함이 실측 검증되었습니다.

테스트 과정에서 생성된 모든 임시 테이블, 뷰, 프로시저 및 커서는 안전하게 정리되었습니다.

---

## 2. Round 10 결함 상세 카탈로그 (10건)

### [ENG-001] 시스템 설정 변수 `@@NESTLEVEL`이 항상 빈 문자열 또는 하드코딩된 0을 반환하는 결함
- **대상 컴포넌트**: T-SQL 실행 엔진 및 프로시저 VM
- **관련 파일**: `pkg/engine/sqlexec_select.go:481-492`, `pkg/proc/builtins_sys.go:619-620`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  `sqlexec_select.go:481`의 임의 시스템 변수 `@@var` 처리 로직은 세션 변수 맵 `cs.Vars[varName]`을 조회합니다. 하지만 프로시저 호출이나 동적 SQL 실행 시 `cs.Vars`에 `@@nestlevel`이 갱신되지 않으며, `builtins_sys.go:619`의 폴백 또한 고정값 `storage.IntValue(0)`만 반환합니다. 이로 인해 실제 단독 질의(`SELECT @@NESTLEVEL;`) 실행 시 현재 프로시저 중첩 깊이 대신 빈 문자열(`""`)이 반환되어 재귀 호출 제한 및 중첩 깊이 방어 로직이 완전히 무력화됩니다.
- **재현 쿼리**:
  ```sql
  SELECT @@NESTLEVEL;
  ```
- **정상 동작 (Expected)**: 정수 `0` (중첩된 저장 프로시저 내부인 경우 `> 0`).
- **실제 동작 (Actual)**: 빈 문자열 `""` 반환.

---

### [ENG-002] `sp_executesql` 동적 SQL 실행 시 `OUTPUT` 파라미터가 호출자의 세션 변수로 역전파되지 않는 결함
- **대상 컴포넌트**: 저장 프로시저 엔진 / 동적 SQL
- **관련 파일**: `pkg/engine/sqlexec_procs.go:1385-1401`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  `execSpExecuteSQL`은 파라미터 선언과 인자 값을 파싱하여 결합된 배치 SQL 문자열(`buildSpExecuteSQL`)을 구성한 뒤 `ExecuteSessionSQLContext`로 위임 실행합니다. 그러나 동적 배치 내부에서 수정된 `OUTPUT` 파라미터 값을 외부 호출자의 세션 변수 컨텍스트(`cs.Vars`)에 다시 바인딩하거나 복사하는 로직이 완전히 누락되어 있습니다. 이로 인해 `EXEC sp_executesql ..., @out = @var OUTPUT` 패턴 사용 시 호출자의 변수 `@var`가 변경되지 않고 비어 있거나 NULL 상태로 남게 됩니다.
- **재현 쿼리**:
  ```sql
  DECLARE @res INT;
  EXEC sp_executesql N'SELECT @out = 99', N'@out INT OUTPUT', @out = @res OUTPUT;
  SELECT @res;
  ```
- **정상 동작 (Expected)**: `99` 반환.
- **실제 동작 (Actual)**: 빈 값 또는 NULL 반환.

---

### [ENG-003] T-SQL 커서 상태 변수 `@@FETCH_STATUS`가 sqlexec 엔진에 미구현되어 빈 문자열을 반환하는 결함
- **대상 컴포넌트**: T-SQL 커서 상태 머신
- **관련 파일**: `pkg/engine/sqlexec_select.go:481-492`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  커서 관리 로직에서 `FETCH NEXT`는 정상 처리되지만, 표준 T-SQL 커서 성공 여부 지표인 `@@FETCH_STATUS`가 `sqlexec_select.go`의 시스템 변수 디스패치 테이블에서 완전히 누락되어 있습니다. 그 결과 `SELECT @@FETCH_STATUS` 실행 시 정상 성공 코드 `0`, 범위 초과 `-1`, 누락 행 `-2` 대신 빈 문자열이 반환되어 표준적인 `WHILE @@FETCH_STATUS = 0` 루프가 동작하지 않거나 즉시 비정상 종료됩니다.
- **재현 쿼리**:
  ```sql
  DECLARE c CURSOR FOR SELECT 1;
  OPEN c;
  FETCH NEXT FROM c;
  SELECT @@FETCH_STATUS;
  CLOSE c;
  DEALLOCATE c;
  ```
- **정상 동작 (Expected)**: 정수 `0` 반환.
- **실제 동작 (Actual)**: 빈 문자열 `""` 반환.

---

### [ENG-004] 비-SCROLL (`FORWARD_ONLY`) 커서에서 역방향 탐색 연산이 허용되는 결함
- **대상 컴포넌트**: T-SQL 커서 엔진
- **관련 파일**: `pkg/engine/sqlexec_cursor.go`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  커서 실행 핸들러가 커서 선언 시 `SCROLL` 속성 여부를 검증하지 않습니다. 기본값인 `FORWARD_ONLY` 커서에 대해 `FETCH PRIOR`, `FETCH FIRST`, `FETCH LAST` 등의 역방향 탐색을 시도하더라도 오류 없이 조용히 성공하여 ANSI SQL 및 T-SQL 명세를 위반합니다.
- **재현 쿼리**:
  ```sql
  DECLARE c CURSOR FORWARD_ONLY FOR SELECT 1 UNION SELECT 2;
  OPEN c;
  FETCH LAST FROM c;
  CLOSE c;
  DEALLOCATE c;
  ```
- **정상 동작 (Expected)**: Msg 16911 오류 발생: `"A fetch type '%s' cannot be used with forward only cursor '%s'."`
- **실제 동작 (Actual)**: 에러 없이 마지막 행이 반환됨.

---

### [ENG-005] `CHECK` 제약조건 파싱 실패 시 조용히 오류를 무시하고 무조건 통과 처리하는 결함
- **대상 컴포넌트**: DDL 제약조건 평가기
- **관련 파일**: `pkg/proc/eval.go:138-144`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  `ValidateRecordChecks`의 내부 클로저 `evalCheck`는 `p.ParseExpr()`를 통해 제약조건 수식을 파싱합니다. 파싱에 실패하여 `expr == nil || len(p.Errors()) > 0`인 경우 오류를 보고하지 않고 `nil`을 반환(제약조건 통과)하도록 되어 있습니다. 이로 인해 유효하지 않거나 깨진 구문의 CHECK 제약조건을 가진 테이블이 문제없이 생성되며, 이후 INSERT/UPDATE 시 제약조건 검증이 무력화되어 데이터 무결성이 손상됩니다.
- **재현 쿼리**:
  ```sql
  CREATE TABLE t_bogus (id INT, val INT CHECK (INVALID SYNTAX %$#@!));
  INSERT INTO t_bogus VALUES (1, -999);
  ```
- **정상 동작 (Expected)**: CREATE TABLE 시 구문 오류로 거부되거나, INSERT 시 파싱 불가능한 제약조건 오류로 차단.
- **실제 동작 (Actual)**: 테이블 생성이 성공하고 잘못된 데이터가 검증 없이 삽입됨.

---

### [ENG-006] `NTILE` 윈도우 함수가 0 이하 및 음수 버킷 수를 에러 없이 조용히 1로 수용하는 결함
- **대상 컴포넌트**: Volcano 실행 엔진 / 윈도우 함수
- **관련 파일**: `pkg/execution/window.go:308-332`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  `NTILE` 윈도우 평가기는 `strconv.ParseInt(..., 10, 64); err == nil && n > 0` 조건을 확인하지만, `n <= 0`인 경우(예: `NTILE(0)` 또는 `NTILE(-5)`) 오류를 발생시키지 않고 `buckets = int64(1)`로 완화 처리합니다. SQL 표준 및 T-SQL 명세에 따르면 0 이하의 버킷 인자는 불법 인자 도메인 오류(Msg 7812)로 차단되어야 합니다.
- **재현 쿼리**:
  ```sql
  SELECT val, NTILE(0) OVER (ORDER BY val) FROM (VALUES (1), (2), (3)) AS t(val);
  ```
- **정상 동작 (Expected)**: ERROR 22003 / Msg 7812: `"The argument of NTILE must be greater than zero."`
- **실제 동작 (Actual)**: 에러 없이 모든 행에 버킷 번호 1이 반환됨.

---

### [ENG-007] 미지원 윈도우 함수 `RATIO_TO_REPORT`가 구문 오류 대신 조용히 NULL을 반환하는 결함
- **대상 컴포넌트**: Volcano 실행 엔진 / 윈도우 함수
- **관련 파일**: `pkg/execution/window.go:420-426`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  윈도우 평가기 `computeWindowAggregate`의 switch 문에서 지원 함수들을 매칭합니다. 파서는 `RATIO_TO_REPORT`를 유효한 윈도우 함수로 파싱하지만 실행 엔진에 구현 분기가 없어 default 분기로 빠지며, 초기화되지 않은 `nil` 상태로 결과를 채워 반환합니다.
- **재현 쿼리**:
  ```sql
  SELECT val, RATIO_TO_REPORT(val) OVER () FROM (VALUES (1), (2)) AS t(val);
  ```
- **정상 동작 (Expected)**: 오류 발생: `"function ratio_to_report does not exist or is unsupported"`.
- **실제 동작 (Actual)**: 에러 없이 모든 행에 NULL이 반환됨.

---

### [ENG-008] `PERCENTILE_CONT` 윈도우 함수가 유효 범위(0.0 ~ 1.0)를 벗어난 분수 인자를 에러 없이 클램핑하는 결함
- **대상 컴포넌트**: Volcano 실행 엔진 / 연속 백분위수
- **관련 파일**: `pkg/execution/window.go:461-465`, `pkg/execution/tdigest.go:148-153`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  SQL 표준상 `PERCENTILE_CONT(p)`의 백분위수 인자 `p`는 반드시 `0.0 <= p <= 1.0`을 만족해야 합니다. `tdigest.go:148-153`에서는 `p <= 0`이면 `values[0]`, `p >= 1.0`이면 `values[n-1]`을 반환하여, `PERCENTILE_CONT(1.5)`나 `PERCENTILE_CONT(-0.5)` 같은 범위를 벗어난 인자를 거부하지 않고 경계값으로 강제 보정합니다.
- **재현 쿼리**:
  ```sql
  SELECT PERCENTILE_CONT(1.5) WITHIN GROUP (ORDER BY v) OVER () FROM (VALUES (10), (20), (30)) AS t(v);
  ```
- **정상 동작 (Expected)**: ERROR 22003 / Msg 8704: `"Percentile value 1.5 must be between 0.0 and 1.0."`
- **실제 동작 (Actual)**: 오류 없이 30이 반환됨.

---

### [ENG-009] 상수 표현식(`GROUP BY NULL` 또는 문자열 리터럴) 그룹화 시 누락 컬럼 오류가 발생하는 결함
- **대상 컴포넌트**: Volcano 실행 엔진 / 집계(Aggregation)
- **관련 파일**: `pkg/execution/aggregate.go`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  표준 SQL에서 상수 리터럴 또는 `GROUP BY NULL`은 단일 전역 집계 그룹(global aggregation group)을 나타냅니다. 그러나 LionSQL 집계 연산자는 수식 텍스트 `"NULL"`을 컬럼 식별자로 간주하여 릴레이션 스키마에서 찾으려고 시도하므로 `ERROR: column "NULL" does not exist` 오류를 발생시키며 실패합니다.
- **재현 쿼리**:
  ```sql
  SELECT SUM(x) FROM (VALUES (1), (2), (3)) AS t(x) GROUP BY NULL;
  ```
- **정상 동작 (Expected)**: 단일 전역 그룹 집계 결과 `6` 반환.
- **실제 동작 (Actual)**: `column "NULL" does not exist` 오류 발생.

---

### [ENG-010] `SELECT DISTINCT` 사용 시 `ORDER BY` 절에 미투영 컬럼이 포함되어도 허용되는 결함
- **대상 컴포넌트**: Volcano 실행 엔진 / 프로젝션 및 정렬
- **관련 파일**: `pkg/engine/sqlexec_select.go`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  SQL 표준 및 PostgreSQL 명세에 따르면, `DISTINCT`를 지정할 경우 중복 제거의 결정론성(determinism)을 보장하기 위해 `ORDER BY`의 모든 항목이 반드시 `SELECT` 목록(프로젝션)에 명시되어야 합니다. LionSQL은 미투영 컬럼으로 먼저 정렬한 후 사후 DISTINCT를 적용함으로써 관계 대수의 비결정론성을 유발합니다.
- **재현 쿼리**:
  ```sql
  CREATE TABLE t_dist (a INT, b INT);
  INSERT INTO t_dist VALUES (1, 20), (1, 10);
  SELECT DISTINCT a FROM t_dist ORDER BY b;
  ```
- **정상 동작 (Expected)**: ERROR 42P10 발생: `"for SELECT DISTINCT, ORDER BY expressions must appear in select list"`
- **실제 동작 (Actual)**: 제약 검증 없이 쿼리가 실행되고 결과를 반환함.
