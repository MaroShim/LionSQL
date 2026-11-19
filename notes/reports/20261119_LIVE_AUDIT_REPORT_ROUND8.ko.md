================================================================================
 LionSQL Live Engine Audit Report - Round 8 (100 New Findings)
 Date: 2026-11-19
 Target: LionSQL Database Server (Live Daemon v0.93.0, commit 005a18ce, Port 5433, WiredTiger)
 Method: psql 16.15 live differential probe scripts (zero overlap with Round 1~7)
 Related files:
   - 20261119_lionsql_live_audit_report.txt        (Round 1 - 10 findings)
   - 20261119_lionsql_live_audit_report_round2.txt (Round 2 - 12 findings)
   - 20261119_lionsql_live_audit_report_round3.txt (Round 3 - 50 findings)
   - 20261119_lionsql_live_audit_report_round4.txt (Round 4 - 50 findings)
   - 20261119_lionsql_live_audit_report_round5.txt (Round 5 - 100 findings)
   - 20261119_lionsql_live_audit_report_round6.txt (Round 6 - 100 findings)
   - 20261119_lionsql_live_audit_report_round7.txt (Round 7 - 100 findings)
================================================================================

[총괄 요약: 신규 발굴 100건 (치명적 2 / 높음 15 / 중간 83 / 낮음 0)]
--------------------------------------------------------------------------------
  영역 1. 계산 컬럼 및 DDL 제약 모순 (R8-001 ~ R8-018) : 18건
  영역 2. 윈도우 프레임 및 함수 검증 (R8-019 ~ R8-030) : 12건
  영역 3. 날짜 변환 및 미구현 표준 함수 (R8-031 ~ R8-040) : 10건
  영역 4. 내장 함수 Arity 검증 Part 2 (R8-041 ~ R8-085) : 45건
  영역 5. 시스템 및 메타데이터 함수 검증 (R8-086 ~ R8-100) : 15건
--------------------------------------------------------------------------------


================================================================================
 1. 계산 컬럼 및 DDL 제약 모순 (18건: R8-001 ~ R8-018)
================================================================================

R8-001. [치명적] 계산 컬럼(Computed Column: a AS (id + 1))에 직접 명시적 값을 INSERT 허용
  - 재현:
      CREATE TABLE r8_inscomp (id INT PRIMARY KEY, a AS (id + 1));
      INSERT INTO r8_inscomp (id, a) VALUES (1, 999);
  - 실제: 계산 컬럼 `a`에 대한 직접 값 삽입이 에러 없이 `INSERT 0 1` 성공
  - 기대: `The column "a" cannot be modified because it is a computed column` 에러 거부

R8-002. [치명적] 계산 컬럼(Computed Column: a AS (id + 1))을 UPDATE SET a = 999로 직접 수정 허용
  - 재현:
      CREATE TABLE r8_updcomp (id INT PRIMARY KEY, a AS (id + 1));
      INSERT INTO r8_updcomp (id) VALUES (1); UPDATE r8_updcomp SET a = 999 WHERE id = 1;
  - 실제: 계산 컬럼 `a`에 대한 직접 UPDATE가 에러 없이 성공
  - 기대: 계산 컬럼은 직접 UPDATE할 수 없으므로 에러 거부

R8-003. [높음] CREATE TABLE에서 IDENTITY(1, 0) 증분값(Increment) 0인 IDENTITY 컬럼 정의 허용
  - 재현:
      CREATE TABLE r8_id0 (id INT IDENTITY(1, 0) PRIMARY KEY);
  - 실제: 증분값이 0임에도 에러 없이 `CREATE TABLE` 성공
  - 기대: `Increment value cannot be 0 for identity column` 에러 거부

R8-004. [높음] 단일 컬럼에 DEFAULT 제약과 IDENTITY 속성을 동시에 선언 허용
  - 재현:
      CREATE TABLE r8_defid (id INT DEFAULT 10 IDENTITY(1, 1) PRIMARY KEY);
  - 실제: 상충하는 두 속성이 동시에 선언되었음에도 에러 없이 `CREATE TABLE` 성공
  - 기대: `Cannot specify a DEFAULT constraint on an identity column` 에러 거부

R8-005. [높음] 단일 컬럼에 계산 컬럼 수식 AS (...)과 DEFAULT 절을 동시에 선언 허용
  - 재현:
      CREATE TABLE r8_compdef (id INT, a AS (id + 1) DEFAULT 5);
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 계산 컬럼에는 DEFAULT 제약을 지정할 수 없으므로 에러 거부

R8-006. [높음] 단일 컬럼에 계산 컬럼 수식 AS (...)과 IDENTITY(1, 1) 속성을 동시에 선언 허용
  - 재현:
      CREATE TABLE r8_compid (id INT, a AS (id + 1) IDENTITY(1, 1));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 계산 컬럼에는 IDENTITY 속성을 지정할 수 없으므로 에러 거부

R8-007. [높음] IF EXISTS 없이 존재하지 않는 제약조건 이름에 대해 ALTER TABLE DROP CONSTRAINT 실행 시 에러 없이 성공
  - 재현:
      CREATE TABLE r8_dc_ne (id INT);
      ALTER TABLE r8_dc_ne DROP CONSTRAINT no_such_constraint_xyz;
  - 실제: 존재하지 않는 제약조건임에도 에러 없이 `ALTER TABLE` 성공
  - 기대: `constraint "no_such_constraint_xyz" of relation "r8_dc_ne" does not exist` 에러 발생

R8-008. [중간] CREATE TRIGGER 선언 시 동일한 DML 이벤트 중복 지정(AFTER INSERT, INSERT) 허용
  - 재현:
      CREATE TABLE r8_trdup (id INT);
      CREATE TRIGGER tr_r8_d ON r8_trdup AFTER INSERT, INSERT AS BEGIN SELECT 1; END;
  - 실제: 동일 이벤트 `INSERT`가 중복 지정되었음에도 에러 없이 트리거 생성 성공
  - 기대: 트리거 이벤트 중복 지정 구문 에러 거부

R8-009. [중간] CREATE SEQUENCE에서 상충하는 CYCLE NO CYCLE 및 중복 MINVALUE 옵션 동시 지정 허용
  - 재현:
      CREATE SEQUENCE r8_seq_conf MINVALUE 1 MINVALUE 10 CYCLE NO CYCLE;
  - 실제: 중복 및 모순 옵션이 포함되었음에도 에러 없이 `SEQUENCE` 생성 성공
  - 기대: `conflicting or redundant options` 구문 에러 거부

R8-010. [중간] CREATE TABLE에서 길이가 0인 VARCHAR(0) 및 NVARCHAR(0) 컬럼 정의 허용
  - 재현:
      CREATE TABLE r8_vc0 (a VARCHAR(0), b NVARCHAR(0));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 문자열 타입 길이는 최소 1 이상이어야 하므로 에러 거부

R8-011. [중간] CREATE TABLE에서 길이가 0인 VARBINARY(0) 및 BINARY(0) 컬럼 정의 허용
  - 재현:
      CREATE TABLE r8_vb0 (a VARBINARY(0), b BINARY(0));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 바이너리 타입 길이는 최소 1 이상이어야 하므로 에러 거부

R8-012. [중간] CREATE TABLE에서 음수 길이인 VARBINARY(-5) 및 BINARY(-5) 컬럼 정의 허용
  - 재현:
      CREATE TABLE r8_vbn (a VARBINARY(-5), b BINARY(-5));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 음수 바이트 길이 정의 에러 거부

R8-013. [중간] CREATE TABLE에서 음수 길이인 CHAR(-5) 및 NCHAR(-5) 컬럼 정의 허용
  - 재현:
      CREATE TABLE r8_chn (a CHAR(-5), b NCHAR(-5));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 음수 문자 길이 정의 에러 거부

R8-014. [중간] CREATE TABLE에서 최대 정밀도(38)를 초과하는 NUMERIC(100, 2) 컬럼 정의 허용
  - 재현:
      CREATE TABLE r8_num100 (a NUMERIC(100, 2));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: `The precision (100) must be between 1 and 38` 에러 거부

R8-015. [중간] CREATE TABLE에서 음수 스케일(Scale)인 NUMERIC(10, -5) 컬럼 정의 허용
  - 재현:
      CREATE TABLE r8_numneg (a NUMERIC(10, -5));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 스케일은 `0 <= scale <= precision` 범위여야 하므로 에러 거부

R8-016. [중간] CREATE TABLE에서 음수 정밀도(Precision)인 NUMERIC(-10, 2) 컬럼 정의 허용
  - 재현:
      CREATE TABLE r8_numpneg (a NUMERIC(-10, 2));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 음수 정밀도 정의 에러 거부

R8-017. [중간] CREATE TABLE에서 유효 범위(1~53)를 벗어난 FLOAT(0) 및 FLOAT(100) 가수부 정밀도 정의 허용
  - 재현:
      CREATE TABLE r8_flt (a FLOAT(0), b FLOAT(100));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: FLOAT 정밀도 범위(1~53) 위반 에러 거부

R8-018. [중간] CURRENT_TIMESTAMP(99)에서 유효 범위(0~7)를 초과하는 초 소수점 정밀도 지정 허용
  - 재현:
      SELECT CURRENT_TIMESTAMP(99) AS v;
  - 실제: 에러 없이 타임스탬프 반환
  - 기대: 타임스탬프 정밀도 상한 초과 에러 발생


================================================================================
 2. 윈도우 프레임 및 함수 검증 (12건: R8-019 ~ R8-030)
================================================================================

R8-019. [높음] 윈도우 프레임 ROWS BETWEEN 5 FOLLOWING AND 1 FOLLOWING (시작 오프셋 > 종료 오프셋 역전) 허용
  - 재현:
      SELECT SUM(v) OVER (ORDER BY v ROWS BETWEEN 5 FOLLOWING AND 1 FOLLOWING) FROM (SELECT 10 AS v) t;
  - 실제: 역전된 프레임 경계임에도 에러 없이 `NULL` 반환
  - 기대: 프레임 시작점이 종료점보다 뒤에 올 수 없으므로 구문/실행 에러 발생

R8-020. [높음] 윈도우 프레임 ROWS BETWEEN 1 FOLLOWING AND 1 PRECEDING (FOLLOWING이 PRECEDING보다 선행) 허용
  - 재현:
      SELECT SUM(v) OVER (ORDER BY v ROWS BETWEEN 1 FOLLOWING AND 1 PRECEDING) FROM (SELECT 10 AS v) t;
  - 실제: 역전된 프레임 경계임에도 에러 없이 `NULL` 반환
  - 기대: `Window frame start cannot be FOLLOWING if frame end is PRECEDING` 에러 거부

R8-021. [높음] 윈도우 프레임 시작점에 UNBOUNDED FOLLOWING 지정(ROWS BETWEEN UNBOUNDED FOLLOWING AND CURRENT ROW) 허용
  - 재현:
      SELECT SUM(v) OVER (ORDER BY v ROWS BETWEEN UNBOUNDED FOLLOWING AND CURRENT ROW) FROM (SELECT 10 AS v) t;
  - 실제: 불법 프레임 시작점임에도 에러 없이 `10` 반환
  - 기대: `Window frame start cannot be UNBOUNDED FOLLOWING` 구문 에러 거부

R8-022. [높음] 윈도우 프레임 종료점에 UNBOUNDED PRECEDING 지정(ROWS BETWEEN CURRENT ROW AND UNBOUNDED PRECEDING) 허용
  - 재현:
      SELECT SUM(v) OVER (ORDER BY v ROWS BETWEEN CURRENT ROW AND UNBOUNDED PRECEDING) FROM (SELECT 10 AS v) t;
  - 실제: 불법 프레임 종료점임에도 에러 없이 `10` 반환
  - 기대: `Window frame end cannot be UNBOUNDED PRECEDING` 구문 에러 거부

R8-023. [중간] LAG(v, 1, 0, 99) 및 LEAD(v, 1, 0, 99)에 4개의 인자 전달 시 에러 없이 4번째 초과 인자 무시
  - 재현:
      SELECT LAG(v, 1, 0, 99) OVER (ORDER BY v) AS a, LEAD(v, 1, 0, 99) OVER (ORDER BY v) AS b FROM (SELECT 10 AS v) t;
  - 실제: 에러 없이 `0 | 0` 반환
  - 기대: `LAG`와 `LEAD`는 최대 3개의 인자만 허용하므로 에러 발생

R8-024. [중간] FIRST_VALUE(v, 99) 및 LAST_VALUE(v, 99)에 2개의 인자 전달 시 에러 없이 2번째 초과 인자 무시
  - 재현:
      SELECT FIRST_VALUE(v, 99) OVER (ORDER BY v) AS a, LAST_VALUE(v, 99) OVER (ORDER BY v) AS b FROM (SELECT 10 AS v) t;
  - 실제: 에러 없이 `10 | 10` 반환
  - 기대: 정확히 1개의 인자만 허용하므로 에러 발생

R8-025. [중간] 무인자 윈도우 함수 DENSE_RANK(123), PERCENT_RANK(123), CUME_DIST(123)에 인자 전달 시 에러 없이 무시
  - 재현:
      SELECT DENSE_RANK(123) OVER (ORDER BY v), PERCENT_RANK(123) OVER (ORDER BY v), CUME_DIST(123) OVER (ORDER BY v) FROM (SELECT 10 AS v) t;
  - 실제: 에러 없이 `1 | 0 | 1` 반환
  - 기대: 인자를 받지 않는 윈도우 함수이므로 구문 에러 발생

R8-026. [중간] NTILE(2, 99)에 2개의 인자 전달 시 에러 없이 2번째 초과 인자 무시
  - 재현:
      SELECT NTILE(2, 99) OVER (ORDER BY v) FROM (SELECT 10 AS v) t;
  - 실제: 에러 없이 `1` 반환
  - 기대: `NTILE`은 정확히 1개의 정수 인자만 허용하므로 에러 발생

R8-027. [중간] T-SQL에서 ORDER BY 절이 필수인 ROW_NUMBER() OVER ()를 빈 OVER ()로 호출 허용
  - 재현:
      SELECT ROW_NUMBER() OVER () FROM (SELECT 10 AS v) t;
  - 실제: 에러 없이 `1` 반환
  - 기대: T-SQL 규칙(`The function 'ROW_NUMBER' must have an OVER clause with ORDER BY`) 검증

R8-028. [중간] DENSE_RANK() OVER () 및 NTILE(2) OVER ()에서 필수 ORDER BY 절 생략 시 에러 없이 전 행 1 반환
  - 재현:
      SELECT DENSE_RANK() OVER (), NTILE(2) OVER () FROM (SELECT 10 AS v) t;
  - 실제: 에러 없이 `1 | 1` 반환
  - 기대: 순위/버킷 윈도우 함수의 OVER 절 내 ORDER BY 필수 검증

R8-029. [중간] LAG(v) OVER () 및 LEAD(v) OVER ()에서 필수 ORDER BY 절 생략 시 에러 없이 NULL 반환
  - 재현:
      SELECT LAG(v) OVER (), LEAD(v) OVER () FROM (SELECT 10 AS v) t;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 오프셋 윈도우 함수의 OVER 절 내 ORDER BY 필수 검증

R8-030. [중간] PERCENT_RANK() OVER () 및 CUME_DIST() OVER ()에서 필수 ORDER BY 절 생략 시 에러 없이 0, 1 반환
  - 재현:
      SELECT PERCENT_RANK() OVER (), CUME_DIST() OVER () FROM (SELECT 10 AS v) t;
  - 실제: 에러 없이 `0 | 1` 반환
  - 기대: 분포 윈도우 함수의 OVER 절 내 ORDER BY 필수 검증


================================================================================
 3. 날짜 변환 및 미구현 표준 함수 (10건: R8-031 ~ R8-040)
================================================================================

R8-031. [높음] YEAR('not_a_date'), MONTH('not_a_date'), DAY('not_a_date')에 유효하지 않은 날짜 문자열 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT YEAR('not_a_date') AS y, MONTH('not_a_date') AS m, DAY('not_a_date') AS d;
  - 실제: 변환 에러 없이 `||` (모두 NULL) 반환 (잘못된 날짜 데이터를 조용히 NULL로 은폐)
  - 기대: `Conversion failed when converting date and/or time from character string` 에러 발생

R8-032. [높음] EOMONTH('not_a_date')에 유효하지 않은 날짜 문자열 전달 시 변환 에러 없이 NULL 반환
  - 재현:
      SELECT EOMONTH('not_a_date') AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: 날짜 문자열 변환 실패 에러(Msg 241) 발생

R8-033. [높음] DATEADD(day, 1, 'not_a_date')에 유효하지 않은 날짜 문자열 전달 시 변환 에러 없이 NULL 반환
  - 재현:
      SELECT DATEADD(day, 1, 'not_a_date') AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: 날짜 문자열 변환 실패 에러(Msg 241) 발생

R8-034. [높음] DATEDIFF(day, 'not_a_date', '2026-01-01')에 유효하지 않은 날짜 문자열 전달 시 변환 에러 없이 NULL 반환
  - 재현:
      SELECT DATEDIFF(day, 'not_a_date', '2026-01-01') AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: 날짜 문자열 변환 실패 에러(Msg 241) 발생

R8-035. [높음] DATEPART(year, 'not_a_date') 및 DATENAME(month, 'not_a_date')에 유효하지 않은 날짜 문자열 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT DATEPART(year, 'not_a_date') AS a, DATENAME(month, 'not_a_date') AS b;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 날짜 문자열 변환 실패 에러(Msg 241) 발생

R8-036. [높음] HASHBYTES('NOT_AN_ALGO', 'abc')에 지원하지 않는 해시 알고리즘명 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT HASHBYTES('NOT_AN_ALGO', 'abc') AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: 유효하지 않은 알고리즘 지정 에러 또는 명시적 경고

R8-037. [중간] ANSI / PostgreSQL 표준 ATAN2(y, x) 수학 내장 함수 호출 시 미구현 에러 발생 (ATN2만 인식)
  - 재현:
      SELECT ATAN2(1, 2) AS v;
  - 실제: `ERROR: unknown scalar function or invalid expression: ATAN2(1, 2)`
  - 기대: `ATN2`와 동일하게 아크탄젠트 값(`0.4636476090008061`) 반환

R8-038. [중간] PostgreSQL 표준 BTRIM(string [, characters]) 문자열 내장 함수 호출 시 미구현 에러 발생
  - 재현:
      SELECT BTRIM('xyxabcxyx', 'xy') AS v;
  - 실제: `ERROR: unknown scalar function or invalid expression: BTRIM('xyxabcxyx', 'xy')`
  - 기대: `'abc'` 반환

R8-039. [중간] T-SQL 표준 NEWSEQUENTIALID() 순차 UUID 생성 내장 함수 미구현 에러
  - 재현:
      SELECT NEWSEQUENTIALID() AS v;
  - 실제: `ERROR: unknown scalar function or invalid expression: NEWSEQUENTIALID()`
  - 기대: 순차 증가형 UNIQUEIDENTIFIER(UUID) 반환

R8-040. [중간] T-SQL 표준 SESSION_CONTEXT(N'key') 세션 컨텍스트 조회 내장 함수 미구현 에러
  - 재현:
      SELECT SESSION_CONTEXT(N'user_id') AS v;
  - 실제: `ERROR: unknown scalar function or invalid expression: SESSION_CONTEXT(N'user_id')`
  - 기대: `sp_set_session_context`로 설정된 값 또는 `NULL` 반환


================================================================================
 4. 내장 함수 Arity 검증 Part 2 (45건: R8-041 ~ R8-085)
================================================================================

R8-041. [중간] ACOS()를 인자 0개로, ACOS(0.5, 1)을 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT ACOS() AS a, ACOS(0.5, 1) AS b;
  - 실제: 에러 없이 `|1.0471975511965976` 반환
  - 기대: `The acos function requires 1 argument(s)` 에러 발생

R8-042. [중간] ASIN()을 인자 0개로, ASIN(0.5, 1)을 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT ASIN() AS a, ASIN(0.5, 1) AS b;
  - 실제: 에러 없이 `|0.5235987755982989` 반환
  - 기대: `The asin function requires 1 argument(s)` 에러 발생

R8-043. [중간] COT()을 인자 0개로, COT(1, 2)를 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT COT() AS a, COT(1, 2) AS b;
  - 실제: 에러 없이 `|0.6420926159343308` 반환
  - 기대: `The cot function requires 1 argument(s)` 에러 발생

R8-044. [중간] ATN2(1)을 인자 1개로, ATN2(1, 2, 3)을 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT ATN2(1) AS a, ATN2(1, 2, 3) AS b;
  - 실제: 에러 없이 `|0.4636476090008061` 반환
  - 기대: `The atn2 function requires 2 argument(s)` 에러 발생

R8-045. [중간] LOG10()을 인자 0개로, LOG10(10, 2)를 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT LOG10() AS a, LOG10(10, 2) AS b;
  - 실제: 에러 없이 `|1` 반환
  - 기대: `The log10 function requires 1 argument(s)` 에러 발생

R8-046. [중간] LOG()를 인자 0개로, LOG(10, 2, 3)을 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT LOG() AS a, LOG(10, 2, 3) AS b;
  - 실제: 에러 없이 `|3.3219280948873626` 반환
  - 기대: `The log function requires 1 to 2 arguments` 에러 발생

R8-047. [중간] SQUARE()를 인자 0개로, SQUARE(5, 2)를 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT SQUARE() AS a, SQUARE(5, 2) AS b;
  - 실제: 에러 없이 `|25` 반환
  - 기대: `The square function requires 1 argument(s)` 에러 발생

R8-048. [중간] RAND(1, 2)를 인자 2개로 호출 시 에러 없이 두 번째 인자 무시
  - 재현:
      SELECT RAND(1, 2) AS v;
  - 실제: 에러 없이 난수 `0.713873...` 반환
  - 기대: `The rand function requires 0 to 1 arguments` 에러 발생

R8-049. [중간] ABS()를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT ABS() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The abs function requires 1 argument(s)` 에러 발생

R8-050. [중간] SQRT(4, 2)를 인자 2개로 호출 시 에러 없이 두 번째 인자를 무시하고 2 반환
  - 재현:
      SELECT SQRT(4, 2) AS v;
  - 실제: 에러 없이 `2` 반환
  - 기대: `The sqrt function requires 1 argument(s)` 에러 발생

R8-051. [중간] SIN(1, 2), COS(1, 2), TAN(1, 2), ATAN(1, 2)를 인자 2개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT SIN(1, 2), COS(1, 2), TAN(1, 2), ATAN(1, 2);
  - 실제: 4종 삼각함수 모두 두 번째 초과 인자를 무시하고 계산 결과 반환
  - 기대: 1개의 인자만 허용하는 구문 에러 발생

R8-052. [중간] DEGREES(1, 2) 및 RADIANS(1, 2)를 인자 2개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT DEGREES(1, 2), RADIANS(1, 2);
  - 실제: 에러 없이 `57.295779... | 0.017453...` 반환
  - 기대: 1개의 인자만 허용하는 구문 에러 발생

R8-053. [중간] ASCII('a', 'b') 및 UNICODE('a', 'b')를 인자 2개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT ASCII('a', 'b') AS a, UNICODE('a', 'b') AS u;
  - 실제: 에러 없이 `97 | 97` 반환
  - 기대: 1개의 인자만 허용하는 구문 에러 발생

R8-054. [중간] CHAR(65, 66) 및 NCHAR(65, 66)을 인자 2개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT CHAR(65, 66) AS c, NCHAR(65, 66) AS n;
  - 실제: 에러 없이 `A | A` 반환
  - 기대: 1개의 인자만 허용하는 구문 에러 발생

R8-055. [중간] UPPER('a', 'b')를 인자 2개로, LOWER()를 인자 0개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT UPPER('a', 'b') AS u, LOWER() AS l;
  - 실제: 에러 없이 `A |` 반환
  - 기대: `UPPER`와 `LOWER`는 정확히 1개의 인자를 요구하므로 구문 에러 발생

R8-056. [중간] SOUNDEX('a', 'b')를 인자 2개로 호출 시 에러 없이 두 번째 인자 무시
  - 재현:
      SELECT SOUNDEX('a', 'b') AS v;
  - 실제: 에러 없이 `'A000'` 반환
  - 기대: `The soundex function requires 1 argument(s)` 에러 발생

R8-057. [중간] DIFFERENCE()를 인자 0개로, DIFFERENCE('a', 'b', 'c')를 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT DIFFERENCE() AS a, DIFFERENCE('a', 'b', 'c') AS b;
  - 실제: 에러 없이 `|3` 반환
  - 기대: `The difference function requires 2 argument(s)` 에러 발생

R8-058. [중간] QUOTENAME('a', '[', 'extra')를 인자 3개로 호출 시 에러 없이 세 번째 인자 무시
  - 재현:
      SELECT QUOTENAME('a', '[', 'extra') AS v;
  - 실제: 에러 없이 `'[a]'` 반환
  - 기대: `The quotename function requires 1 to 2 arguments` 에러 발생

R8-059. [중간] PARSENAME()을 인자 0개로, PARSENAME('a.b', 1, 'extra')을 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT PARSENAME() AS a, PARSENAME('a.b', 1, 'extra') AS b;
  - 실제: 에러 없이 `|b` 반환
  - 기대: `The parsename function requires 2 argument(s)` 에러 발생

R8-060. [중간] CHARINDEX('a', 'abc', 1, 99)를 인자 4개로, PATINDEX('%a%', 'abc', 'extra')를 인자 3개로 호출 허용
  - 재현:
      SELECT CHARINDEX('a', 'abc', 1, 99) AS a, PATINDEX('%a%', 'abc', 'extra') AS b;
  - 실제: 에러 없이 `1 | 1` 반환
  - 기대: `CHARINDEX`(2~3개), `PATINDEX`(2개) 초과 인자 구문 에러 발생

R8-061. [중간] EOMONTH('2026-01-01', 1, 2)를 인자 3개로 호출 시 에러 없이 세 번째 인자 무시
  - 재현:
      SELECT EOMONTH('2026-01-01', 1, 2) AS v;
  - 실제: 에러 없이 `'2026-02-28'` 반환
  - 기대: `The eomonth function requires 1 to 2 arguments` 에러 발생

R8-062. [중간] ISDATE()를 인자 0개로, ISDATE('2026-01-01', 'extra')를 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT ISDATE() AS a, ISDATE('2026-01-01', 'extra') AS b;
  - 실제: 에러 없이 `0 | 1` 반환
  - 기대: `The isdate function requires 1 argument(s)` 에러 발생

R8-063. [중간] ISNUMERIC()을 인자 0개로, ISNUMERIC('123', 'extra')을 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT ISNUMERIC() AS a, ISNUMERIC('123', 'extra') AS b;
  - 실제: 에러 없이 `0 | 1` 반환
  - 기대: `The isnumeric function requires 1 argument(s)` 에러 발생

R8-064. [중간] ISJSON('{"a":1}', 'extra1', 'extra2')를 인자 3개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT ISJSON('{"a":1}', 'extra1', 'extra2') AS v;
  - 실제: 에러 없이 `1` 반환
  - 기대: `ISJSON` 초과 인자 구문 에러 발생

R8-065. [중간] JSON_VALUE 및 JSON_QUERY를 인자 3개로 호출 시 에러 없이 세 번째 초과 인자 무시
  - 재현:
      SELECT JSON_VALUE('{"a":1}', '$.a', 'extra') AS a, JSON_QUERY('{"a":[1]}', '$.a', 'extra') AS b;
  - 실제: 에러 없이 `1 | [1]` 반환
  - 기대: `JSON_VALUE`(2개), `JSON_QUERY`(1~2개) 초과 인자 구문 에러 발생

R8-066. [중간] JSON_MODIFY('{"a":1}', '$.a', 2, 'extra')를 인자 4개로 호출 시 에러 없이 네 번째 인자 무시
  - 재현:
      SELECT JSON_MODIFY('{"a":1}', '$.a', 2, 'extra') AS v;
  - 실제: 에러 없이 `'{"a":2}'` 반환
  - 기대: `The json_modify function requires 3 argument(s)` 에러 발생

R8-067. [중간] NULLIF()를 인자 0개로, NULLIF(1, 2, 3)을 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT NULLIF() AS a, NULLIF(1, 2, 3) AS b;
  - 실제: 에러 없이 `|1` 반환
  - 기대: `NULLIF`는 정확히 2개의 인자를 요구하므로 구문 에러 발생

R8-068. [중간] DATEFROMPARTS(2026, 1)를 인자 2개로, DATEFROMPARTS(2026, 1, 1, 99)를 인자 4개로 호출 허용
  - 재현:
      SELECT DATEFROMPARTS(2026, 1) AS a, DATEFROMPARTS(2026, 1, 1, 99) AS b;
  - 실제: 에러 없이 `|2026-01-01` 반환
  - 기대: `The datefromparts function requires 3 argument(s)` 에러 발생

R8-069. [중간] TIMEFROMPARTS(12, 0)를 인자 2개로, TIMEFROMPARTS(12, 0, 0, 0, 0, 99)를 인자 6개로 호출 허용
  - 재현:
      SELECT TIMEFROMPARTS(12, 0) AS a, TIMEFROMPARTS(12, 0, 0, 0, 0, 99) AS b;
  - 실제: 에러 없이 `|12:00:00` 반환
  - 기대: `The timefromparts function requires 5 argument(s)` 에러 발생

R8-070. [중간] DATETIMEFROMPARTS(2026, 1, 1)를 인자 3개로 및 인자 8개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT DATETIMEFROMPARTS(2026, 1, 1) AS a, DATETIMEFROMPARTS(2026, 1, 1, 0, 0, 0, 0, 99) AS b;
  - 실제: 에러 없이 `|2026-01-01 00:00:00.000` 반환
  - 기대: `The datetimefromparts function requires 7 argument(s)` 에러 발생

R8-071. [중간] SMALLDATETIMEFROMPARTS(2026, 1)를 인자 2개로 및 인자 6개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT SMALLDATETIMEFROMPARTS(2026, 1) AS a, SMALLDATETIMEFROMPARTS(2026, 1, 1, 0, 0, 99) AS b;
  - 실제: 에러 없이 `|2026-01-01 00:00:00` 반환
  - 기대: `The smalldatetimefromparts function requires 5 argument(s)` 에러 발생

R8-072. [중간] SWITCHOFFSET을 인자 1개로 및 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT SWITCHOFFSET('2026-01-01 00:00:00 +00:00') AS a, SWITCHOFFSET('2026-01-01 00:00:00 +00:00', '+09:00', 'extra') AS b;
  - 실제: 에러 없이 `|2026-01-01 09:00:00.0000000 +09:00` 반환
  - 기대: `The switchoffset function requires 2 argument(s)` 에러 발생

R8-073. [중간] TODATETIMEOFFSET을 인자 1개로 및 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT TODATETIMEOFFSET('2026-01-01 00:00:00') AS a, TODATETIMEOFFSET('2026-01-01 00:00:00', '+09:00', 'extra') AS b;
  - 실제: 에러 없이 `|2026-01-01 00:00:00.0000000 +09:00` 반환
  - 기대: `The todatetimeoffset function requires 2 argument(s)` 에러 발생

R8-074. [중간] DATEDIFF_BIG을 인자 2개로 및 인자 4개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT DATEDIFF_BIG(day, '2026-01-01') AS a, DATEDIFF_BIG(day, '2026-01-01', '2026-01-02', 'extra') AS b;
  - 실제: 에러 없이 `|1` 반환
  - 기대: `The datediff_big function requires 3 argument(s)` 에러 발생

R8-075. [중간] FORMAT(123)을 포맷 없이 인자 1개로, FORMAT(123, 'N', 'en-US', 'extra')을 인자 4개로 호출 허용
  - 재현:
      SELECT FORMAT(123) AS a, FORMAT(123, 'N', 'en-US', 'extra') AS b;
  - 실제: 에러 없이 `|123.00` 반환
  - 기대: `The format function requires 2 to 3 arguments` 에러 발생

R8-076. [중간] TO_CHAR('2026-01-01')를 인자 1개로 및 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT TO_CHAR('2026-01-01') AS a, TO_CHAR('2026-01-01', 'YYYY', 'extra') AS b;
  - 실제: 에러 없이 `|2026` 반환
  - 기대: `TO_CHAR`는 정확히 2개의 인자를 요구하므로 에러 발생

R8-077. [중간] TO_DATE('2026-01-01')를 인자 1개로 및 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT TO_DATE('2026-01-01') AS a, TO_DATE('2026-01-01', 'YYYY-MM-DD', 'extra') AS b;
  - 실제: 에러 없이 `2026-01-01 | 2026-01-01` 반환
  - 기대: `TO_DATE`는 정확히 2개의 인자를 요구하므로 에러 발생

R8-078. [중간] DATE_TRUNC('day')를 인자 1개로, DATE_TRUNC(..., 'extra1', 'extra2')를 인자 4개로 호출 허용
  - 재현:
      SELECT DATE_TRUNC('day') AS a, DATE_TRUNC('day', '2026-01-01', 'extra1', 'extra2') AS b;
  - 실제: 에러 없이 `|2026-01-01 00:00:00` 반환
  - 기대: `DATE_TRUNC` 인자 개수 불일치 에러 발생

R8-079. [중간] LPAD('a') 및 RPAD('a')를 인자 1개로, LPAD('a', 3, 'x', 'extra')를 인자 4개로 호출 허용
  - 재현:
      SELECT LPAD('a') AS a, RPAD('a') AS b, LPAD('a', 3, 'x', 'extra') AS c;
  - 실제: 에러 없이 `||xxa` 반환
  - 기대: `LPAD`와 `RPAD`는 2~3개의 인자만 허용하므로 에러 발생

R8-080. [중간] SPLIT_PART('a-b', '-')를 인자 2개로 및 인자 4개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT SPLIT_PART('a-b', '-') AS a, SPLIT_PART('a-b', '-', 1, 'extra') AS b;
  - 실제: 에러 없이 `|a` 반환
  - 기대: `SPLIT_PART`는 정확히 3개의 인자를 요구하므로 에러 발생

R8-081. [중간] STRPOS('abc')를 인자 1개로, STRPOS('abc', 'b', 'extra')를 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT STRPOS('abc') AS a, STRPOS('abc', 'b', 'extra') AS b;
  - 실제: 에러 없이 `|2` 반환
  - 기대: `STRPOS`는 정확히 2개의 인자를 요구하므로 에러 발생

R8-082. [중간] CHR()를 인자 0개로, CHR(65, 66)을 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT CHR() AS a, CHR(65, 66) AS b;
  - 실제: 에러 없이 `|A` 반환
  - 기대: `CHR`는 정확히 1개의 정수 인자를 요구하므로 에러 발생

R8-083. [중간] MD5()를 인자 0개로, MD5('a', 'b')를 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT MD5() AS a, MD5('a', 'b') AS b;
  - 실제: 에러 없이 `|0cc175b9c0f1b6a831c399e269772661` 반환
  - 기대: `MD5`는 정확히 1개의 인자를 요구하므로 에러 발생

R8-084. [중간] HASHBYTES()를 인자 0~1개로, HASHBYTES('SHA2_256', 'abc', 'extra')를 인자 3개로 호출 허용
  - 재현:
      SELECT HASHBYTES() AS a, HASHBYTES('SHA2_256') AS b, HASHBYTES('SHA2_256', 'abc', 'extra') AS c;
  - 실제: 에러 없이 `||0xBA7816BF...` 반환
  - 기대: `The hashbytes function requires 2 argument(s)` 에러 발생

R8-085. [중간] FORMATMESSAGE()를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT FORMATMESSAGE() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `FORMATMESSAGE`는 최소 1개 이상의 인자를 요구하므로 에러 발생


================================================================================
 5. 시스템 및 메타데이터 함수 검증 (15건: R8-086 ~ R8-100)
================================================================================

R8-086. [중간] OBJECT_ID()를 인자 0개로, OBJECT_ID('a', 'U', 'extra')를 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT OBJECT_ID() AS a, OBJECT_ID('a', 'U', 'extra') AS b;
  - 실제: 에러 없이 `|` 반환
  - 기대: `The object_id function requires 1 to 2 arguments` 에러 발생

R8-087. [중간] OBJECT_NAME()을 인자 0개로, OBJECT_NAME(1, 2, 3)을 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT OBJECT_NAME() AS a, OBJECT_NAME(1, 2, 3) AS b;
  - 실제: 에러 없이 `|` 반환
  - 기대: `The object_name function requires 1 to 2 arguments` 에러 발생

R8-088. [중간] COL_LENGTH()를 인자 0~1개로, COL_LENGTH('t', 'c', 'extra')를 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT COL_LENGTH() AS a, COL_LENGTH('t') AS b, COL_LENGTH('t', 'c', 'extra') AS c;
  - 실제: 에러 없이 `||50` 반환 (존재하지 않는 테이블 `'t'`, 컬럼 `'c'`에 대해 고정값 `50` 반환 포함)
  - 기대: `The col_length function requires 2 argument(s)` 에러 발생 및 미존재 테이블에 `NULL` 반환

R8-089. [중간] DB_NAME(1, 2) 및 DB_ID('master', 'extra')를 인자 2개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT DB_NAME(1, 2) AS a, DB_ID('master', 'extra') AS b;
  - 실제: 에러 없이 `LionSQL | 1` 반환
  - 기대: `DB_NAME`과 `DB_ID`는 최대 1개의 인자만 허용하므로 에러 발생

R8-090. [중간] SCHEMA_NAME(1, 2) 및 SCHEMA_ID('dbo', 'extra')를 인자 2개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT SCHEMA_NAME(1, 2) AS a, SCHEMA_ID('dbo', 'extra') AS b;
  - 실제: 에러 없이 `dbo | 1` 반환
  - 기대: `SCHEMA_NAME`과 `SCHEMA_ID`는 최대 1개의 인자만 허용하므로 에러 발생

R8-091. [중간] 무인자 시스템 함수 SCOPE_IDENTITY(123), HOST_NAME(123), APP_NAME(123)에 인자 전달 시 에러 없이 무시
  - 재현:
      SELECT SCOPE_IDENTITY(123), HOST_NAME(123), APP_NAME(123);
  - 실제: 에러 없이 호스트명과 앱 이름 반환
  - 기대: 인자를 받지 않는 함수이므로 구문 에러 발생

R8-092. [중간] SUSER_NAME(1, 2) 및 USER_NAME(1, 2)을 인자 2개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT SUSER_NAME(1, 2), USER_NAME(1, 2);
  - 실제: 에러 없이 `sa | dbo` 반환
  - 기대: 최대 1개의 인자만 허용하므로 구문 에러 발생

R8-093. [중간] IDENT_CURRENT()를 인자 0개로, IDENT_CURRENT('t', 'extra')를 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT IDENT_CURRENT() AS a, IDENT_CURRENT('t', 'extra') AS b;
  - 실제: 에러 없이 `|` 반환
  - 기대: `The ident_current function requires 1 argument(s)` 에러 발생

R8-094. [중간] CHECKSUM()을 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT CHECKSUM() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `CHECKSUM`은 `*` 또는 최소 1개 이상의 인자를 요구하므로 에러 발생

R8-095. [중간] BINARY_CHECKSUM()을 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT BINARY_CHECKSUM() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `BINARY_CHECKSUM`은 `*` 또는 최소 1개 이상의 인자를 요구하므로 에러 발생

R8-096. [중간] 무인자 시각 함수 GETUTCDATE(123), SYSUTCDATETIME(123), SYSDATETIMEOFFSET(123)에 인자 전달 허용
  - 재현:
      SELECT GETUTCDATE(123), SYSUTCDATETIME(123), SYSDATETIMEOFFSET(123);
  - 실제: 에러 없이 현재 UTC 시각 반환
  - 기대: 무인자 함수에 인자 전달 시 구문 에러 발생

R8-097. [중간] 무인자 에러 조회 함수 ERROR_NUMBER(123), ERROR_MESSAGE(123), ERROR_SEVERITY(123)에 인자 전달 허용
  - 재현:
      SELECT ERROR_NUMBER(123), ERROR_MESSAGE(123), ERROR_SEVERITY(123);
  - 실제: 에러 없이 `0 | | 16` 반환
  - 기대: 무인자 함수에 인자 전달 시 구문 에러 발생

R8-098. [중간] 무인자 에러 조회 함수 ERROR_STATE(123), ERROR_LINE(123), ERROR_PROCEDURE(123)에 인자 전달 허용
  - 재현:
      SELECT ERROR_STATE(123), ERROR_LINE(123), ERROR_PROCEDURE(123);
  - 실제: 에러 없이 `1 | 0 | INTERACTIVE_QUERY` 반환
  - 기대: 무인자 함수에 인자 전달 시 구문 에러 발생

R8-099. [중간] 무인자 트랜잭션 상태 함수 XACT_STATE(123)에 인자 전달 시 에러 없이 0 반환
  - 재현:
      SELECT XACT_STATE(123) AS v;
  - 실제: 에러 없이 `0` 반환
  - 기대: `The xact_state function requires 0 argument(s)` 에러 발생

R8-100. [중간] 무인자 세션 바이너리 컨텍스트 함수 CONTEXT_INFO(123)에 인자 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT CONTEXT_INFO(123) AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The context_info function requires 0 argument(s)` 에러 발생

================================================================================
