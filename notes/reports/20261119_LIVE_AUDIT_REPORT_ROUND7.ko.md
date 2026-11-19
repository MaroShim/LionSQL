================================================================================
 LionSQL Live Engine Audit Report - Round 7 (100 New Findings)
 Date: 2026-11-19
 Target: LionSQL Database Server (Live Daemon v0.93.0, commit 005a18ce, Port 5433, WiredTiger)
 Method: psql 16.15 live differential probe scripts & source cross-verification (zero overlap with Round 1~6)
 Related files:
   - 20261119_lionsql_live_audit_report.txt        (Round 1 - 10 findings)
   - 20261119_lionsql_live_audit_report_round2.txt (Round 2 - 12 findings)
   - 20261119_lionsql_live_audit_report_round3.txt (Round 3 - 50 findings)
   - 20261119_lionsql_live_audit_report_round4.txt (Round 4 - 50 findings)
   - 20261119_lionsql_live_audit_report_round5.txt (Round 5 - 100 findings)
   - 20261119_lionsql_live_audit_report_round6.txt (Round 6 - 100 findings)
================================================================================

[총괄 요약: 신규 발굴 100건 (치명적 6 / 높음 29 / 중간 65 / 낮음 0)]
--------------------------------------------------------------------------------
  영역 1. 스토리지 및 DML 바인딩 (R7-001 ~ R7-006) : 6건
  영역 2. DDL 및 네임스페이스 무결성 (R7-007 ~ R7-020) : 14건
  영역 3. 프로시저 및 커서 상태 머신 (R7-021 ~ R7-026) : 6건
  영역 4. 쿼리 스코프 및 윈도우 검증 (R7-027 ~ R7-032) : 6건
  영역 5. 캐스팅 및 날짜 검증 (R7-033 ~ R7-050) : 18건
  영역 6. 내장 및 집계 함수 Arity 검증 (R7-051 ~ R7-100) : 50건
--------------------------------------------------------------------------------


================================================================================
 1. 스토리지 및 DML 바인딩 (6건: R7-001 ~ R7-006)
================================================================================

R7-001. [치명적] PK 없는 테이블에 컬럼 리스트 생략 INSERT ... OUTPUT 실행 시 내부 _row_id 컬럼이 노출되어 컬럼 수 불일치 에러 발생 [코드 확인]
  - 재현:
      CREATE TABLE r7_npk_out (a INT);
      INSERT INTO r7_npk_out OUTPUT INSERTED.a VALUES (10);
  - 실제: `ERROR: column count mismatch: expected 2 values, got 1 in tuple (10)` (`pkg/engine/sqlexec_dml.go:336-358`에서 PGWire 세션 시 숨김 `_row_id` 컬럼을 `targetCols`에 포함)
  - 기대: 숨김 `_row_id` 컬럼은 `targetCols`에서 제외되어 `10`이 정상 삽입 및 OUTPUT 반환되어야 함

R7-002. [치명적] PK 없는 테이블에 제약/길이 초과 INSERT 실행 시 pk_bypass 실패 후 폴백에서 진짜 에러가 'expected 2 values, got 1'로 왜곡됨 [코드 확인]
  - 재현:
      CREATE TABLE r7_npk_vc (s VARCHAR(3));
      INSERT INTO r7_npk_vc VALUES ('too_long');
  - 실제: `string or binary data would be truncated` 대신 `ERROR: column count mismatch: expected 2 values, got 1 in tuple ('too_long')` 반환
  - 기대: 문자열 길이 초과 에러(`string or binary data would be truncated`)가 정확히 반환되어야 함

R7-003. [치명적] PK 없는 단일 컬럼 테이블에서 유일한 컬럼을 ALTER TABLE DROP COLUMN으로 삭제 허용 (0컬럼 유령 테이블 생성)
  - 재현:
      CREATE TABLE r7_1col (id INT);
      ALTER TABLE r7_1col DROP COLUMN id;
  - 실제: 에러 없이 유일한 사용자 컬럼 `id`가 삭제되어 사용자 컬럼이 0개인 기형 테이블이 남음
  - 기대: 테이블의 마지막 남은 컬럼은 삭제할 수 없도록 에러 거부(`cannot drop the only column of a table`)

R7-004. [치명적] BIT 컬럼에 0이나 1이 아닌 양의 정수(42) INSERT 시 T-SQL 표준(1/true)과 반대로 false(0)로 저장
  - 재현:
      CREATE TABLE r7_bit (b BIT); INSERT INTO r7_bit VALUES (42); SELECT b FROM r7_bit;
  - 실제: `false` 반환 (`1`이 아닌 모든 정수를 `false`로 잘못 평가)
  - 기대: T-SQL BIT 타입 규칙에 따라 0이 아닌 모든 정수는 `1`(`true`)로 정규화되거나 범위 에러를 반환해야 함

R7-005. [높음] 수치형 컬럼에서 비숫자 문자열에 대한 SUM(s) 및 AVG(s) 집계 실행 시 타입 변환 에러 없이 0 반환
  - 재현:
      CREATE TABLE r7_badagg (s VARCHAR(10)); INSERT INTO r7_badagg VALUES ('abc'), ('def');
      SELECT SUM(s), AVG(s) FROM r7_badagg;
  - 실제: 에러 없이 `0 | 0` 반환
  - 기대: `Operand data type varchar is invalid for sum/avg operator` 또는 숫자 변환 에러 발생

R7-006. [높음] SQL 레벨 PREPARE 및 EXECUTE 준비된 문장 구문 실행 실패
  - 재현:
      PREPARE r7_p1 AS SELECT 1; EXECUTE r7_p1;
  - 실제: `PREPARE` / `EXECUTE` SQL 구문 에러 또는 프로시저 호출로 오인하여 실패
  - 기대: PG 호환 SQL 레벨 `PREPARE`, `EXECUTE`, `DEALLOCATE` 정상 동작


================================================================================
 2. DDL 및 네임스페이스 무결성 (14건: R7-007 ~ R7-020)
================================================================================

R7-007. [치명적] 동일 스키마 내에 기존 TABLE과 완전히 동일한 이름의 VIEW 생성 허용 (네임스페이스 충돌)
  - 재현:
      CREATE TABLE r7_same_tv (id INT);
      CREATE VIEW r7_same_tv AS SELECT 1 AS id;
  - 실제: 동일 이름의 테이블이 이미 존재함에도 에러 없이 `CREATE VIEW` 성공
  - 기대: `there is already an object named 'r7_same_tv' in the database` 에러 거부

R7-008. [치명적] 동일 스키마 내에 기존 VIEW와 완전히 동일한 이름의 TABLE 생성 허용 (네임스페이스 충돌)
  - 재현:
      CREATE VIEW r7_same_vt AS SELECT 1 AS id;
      CREATE TABLE r7_same_vt (id INT);
  - 실제: 동일 이름의 뷰가 이미 존재함에도 에러 없이 `CREATE TABLE` 성공
  - 기대: `there is already an object named 'r7_same_vt' in the database` 에러 거부

R7-009. [높음] OR ALTER 없이 동일 이름의 CREATE FUNCTION 반복 실행 시 에러 없이 기존 함수를 덮어씀
  - 재현:
      CREATE FUNCTION r7_dupfn() RETURNS INT AS BEGIN RETURN 1; END;
      CREATE FUNCTION r7_dupfn() RETURNS INT AS BEGIN RETURN 2; END;
  - 실제: 이미 존재하는 함수임에도 에러 없이 두 번째 `CREATE FUNCTION`이 성공하며 덮어써짐
  - 기대: `there is already an object named 'r7_dupfn'` 에러 거부

R7-010. [높음] 동일 이름의 CREATE SEQUENCE 반복 실행 시 에러 없이 기존 시퀀스를 초기화/덮어씀
  - 재현:
      CREATE SEQUENCE r7_dupseq START WITH 10;
      CREATE SEQUENCE r7_dupseq START WITH 20;
  - 실제: 이미 존재하는 시퀀스임에도 에러 없이 두 번째 `CREATE SEQUENCE` 성공
  - 기대: `relation "r7_dupseq" already exists` 에러 거부

R7-011. [높음] IF EXISTS 없이 존재하지 않는 시퀀스에 대해 DROP SEQUENCE 실행 시 에러 없이 성공
  - 재현:
      DROP SEQUENCE r7_seq_never_existed;
  - 실제: 존재하지 않는 시퀀스임에도 에러 없이 `SEQUENCE` 태그 반환하며 성공
  - 기대: `sequence "r7_seq_never_existed" does not exist` 에러 발생

R7-012. [높음] CREATE TABLE 내에 동일한 제약조건 이름(CONSTRAINT c1 ... CONSTRAINT c1 ...) 중복 선언 허용
  - 재현:
      CREATE TABLE r7_dupcon (a INT, CONSTRAINT c1 CHECK (a > 0), CONSTRAINT c1 CHECK (a < 100));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 제약조건 이름(`c1`) 중복 에러 거부

R7-013. [높음] ALTER TABLE ADD CONSTRAINT로 이미 존재하는 제약조건 이름(c1) 중복 추가 허용
  - 재현:
      CREATE TABLE r7_alt_dupcon (a INT, CONSTRAINT c1 CHECK (a > 0));
      ALTER TABLE r7_alt_dupcon ADD CONSTRAINT c1 CHECK (a < 100);
  - 실제: 에러 없이 동일한 이름 `c1`의 제약조건 중복 추가 성공
  - 기대: `constraint "c1" for relation already exists` 에러 거부

R7-014. [높음] CREATE TABLE에서 존재하지 않는 컬럼을 참조하는 CHECK (no_such_col > 0) 제약 생성 허용
  - 재현:
      CREATE TABLE r7_ck_ne (a INT, CHECK (no_such_col > 0));
  - 실제: 테이블에 `no_such_col`이 없음에도 에러 없이 `CREATE TABLE` 성공
  - 기대: `column "no_such_col" does not exist` 에러 거부

R7-015. [높음] ALTER TABLE ADD CONSTRAINT에서 존재하지 않는 컬럼을 참조하는 CHECK (no_such_col > 0) 추가 허용
  - 재현:
      CREATE TABLE r7_ack_ne (a INT);
      ALTER TABLE r7_ack_ne ADD CONSTRAINT ck_ne CHECK (no_such_col > 0);
  - 실제: 에러 없이 `ALTER TABLE` 성공
  - 기대: 존재하지 않는 컬럼을 참조하는 CHECK 제약 추가 에러 거부

R7-016. [높음] CREATE TABLE에서 존재하지 않는 컬럼을 참조하는 계산 컬럼(b AS (no_such_col + 1)) 정의 허용
  - 재현:
      CREATE TABLE r7_comp_ne (a INT, b AS (no_such_col + 1));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 계산 컬럼 수식 내 미존재 컬럼 참조 에러 거부

R7-017. [높음] CREATE TABLE에서 자기 자신을 순환 참조하는 계산 컬럼(b AS (b + 1)) 정의 허용
  - 재현:
      CREATE TABLE r7_comp_circ (a INT, b AS (b + 1));
  - 실제: 자기 순환 참조임에도 에러 없이 `CREATE TABLE` 성공
  - 기대: 계산 컬럼은 자기 자신을 참조할 수 없으므로 에러 거부

R7-018. [중간] CREATE INDEX CONCURRENTLY 구문 실행 시 파서 구문 에러로 실패
  - 재현:
      CREATE TABLE r7_idxc (id INT PRIMARY KEY, v INT); CREATE INDEX CONCURRENTLY idx_r7_c ON r7_idxc(v);
  - 실제: `ERROR: invalid CREATE INDEX syntax`
  - 기대: PG 호환 `CONCURRENTLY` 옵션 인식 및 인덱스 생성

R7-019. [중간] CREATE MATERIALIZED VIEW 구문 실행 시 파서 구문 에러로 실패
  - 재현:
      CREATE MATERIALIZED VIEW r7_mv AS SELECT 1 AS a;
  - 실제: `ERROR: line 1:7 no viable alternative at input 'CREATE MATERIALIZED'`
  - 기대: Materialized View 지원 또는 명확한 기능 미지원 안내

R7-020. [중간] CREATE TABLE ... AS SELECT ... WITH NO DATA 구문 파싱 실패
  - 재현:
      CREATE TABLE r7_ctas_nd AS SELECT 1 AS a WITH NO DATA;
  - 실제: `ERROR: line 1:24 missing '(' at 'AS'`
  - 기대: 스키마만 복제하는 빈 테이블 생성


================================================================================
 3. 프로시저 및 커서 상태 머신 (6건: R7-021 ~ R7-026)
================================================================================

R7-021. [높음] 스칼라 UDF(RETURNS INT)가 숫자가 아닌 문자열('abc')을 RETURN해도 타입 변환 에러 없이 문자열 반환
  - 재현:
      CREATE FUNCTION r7_fn_badret() RETURNS INT AS BEGIN RETURN 'abc'; END;
      SELECT dbo.r7_fn_badret();
  - 실제: 선언된 반환 타입이 `INT`임에도 에러 없이 문자열 `'abc'` 반환
  - 기대: `Conversion failed when converting the varchar value 'abc' to data type int` 에러 발생

R7-022. [높음] 스칼라 CREATE FUNCTION 본문에 RETURN 문이 전혀 없어도 함수 생성 성공
  - 재현:
      CREATE FUNCTION r7_fn_noret() RETURNS INT AS BEGIN DECLARE @x INT = 1; END;
  - 실제: RETURN 문이 누락된 스칼라 함수임에도 에러 없이 `CREATE FUNCTION` 성공
  - 기대: `The last statement included within a function must be a return statement` 에러 거부

R7-023. [높음] CREATE PROCEDURE에서 INT 매개변수에 숫자가 아닌 문자열 기본값(@a INT = 'bad') 선언 허용
  - 재현:
      CREATE PROCEDURE r7_sp_bdef @a INT = 'bad' AS BEGIN SELECT @a; END;
  - 실제: 에러 없이 `CREATE PROCEDURE` 성공
  - 기대: 매개변수 타입과 기본값 리터럴 타입 불일치 검증 및 에러 거부

R7-024. [높음] 선언되지 않은 스칼라 변수(SELECT @undeclared) 조회 시 에러(Msg 137) 대신 조용히 NULL 반환
  - 재현:
      EXEC sp_executesql N'SELECT @r7_never_declared AS v;';
  - 실제: 에러 없이 `NULL` 반환 (변수명 오타를 검출하지 못해 심각한 논리 버그 유발)
  - 기대: `Msg 137: Must declare the scalar variable "@r7_never_declared"` 에러 발생

R7-025. [중간] 선언된 적 없는 커서에 대해 DEALLOCATE 실행 시 에러 없이 성공
  - 재현:
      EXEC sp_executesql N'DEALLOCATE r7_no_cur;';
  - 실제: 존재하지 않는 커서임에도 에러 없이 `DEALLOCATE` 성공
  - 기대: `A cursor with the name 'r7_no_cur' does not exist` 에러 발생

R7-026. [중간] OPEN하지 않은 닫힌 커서에 대해 CLOSE 실행 시 에러 없이 성공
  - 재현:
      EXEC sp_executesql N'DECLARE c CURSOR FOR SELECT 1; CLOSE c; DEALLOCATE c;';
  - 실제: 열리지 않은 커서임에도 에러 없이 `CLOSE CURSOR` 성공
  - 기대: `The cursor is not open` 상태 머신 에러 발생


================================================================================
 4. 쿼리 스코프 및 윈도우 검증 (6건: R7-027 ~ R7-032)
================================================================================

R7-027. [높음] 단일 WITH 절 내에 동일한 CTE 이름 중복 정의(WITH cte AS (...), cte AS (...)) 허용
  - 재현:
      WITH cte AS (SELECT 1 AS a), cte AS (SELECT 2 AS a) SELECT * FROM cte;
  - 실제: 에러 없이 첫 번째 CTE만 참조하여 `1` 반환
  - 기대: `WITH query name "cte" specified more than once` 에러 거부

R7-028. [높음] 단일 FROM 절 내에 동일한 테이블 별칭 중복 사용(FROM t t1 JOIN t t1) 허용
  - 재현:
      CREATE TABLE r7_dupal (id INT); INSERT INTO r7_dupal VALUES (1);
      SELECT * FROM r7_dupal t1 JOIN r7_dupal t1 ON t1.id = t1.id;
  - 실제: 동일 스코프에 `t1` 별칭이 2번 선언되었음에도 에러 없이 `1 | 1` 반환
  - 기대: `table name "t1" specified more than once` 에러 거부

R7-029. [높음] NTILE(0) 및 NTILE(-2) 비양수 버킷 수 또는 무인자 NTILE() 호출 시 에러 없이 1 반환
  - 재현:
      SELECT NTILE() OVER (ORDER BY v) AS n0, NTILE(0) OVER (ORDER BY v) AS nz, NTILE(-2) OVER (ORDER BY v) AS nn FROM (SELECT 1 AS v) t;
  - 실제: 에러 없이 `1 | 1 | 1` 반환
  - 기대: NTILE의 인자는 1 이상의 양의 정수여야 하므로 도메인/구문 에러 발생

R7-030. [중간] 무인자 윈도우 순위 함수 ROW_NUMBER(123) 및 RANK(123)에 인자를 전달해도 에러 없이 무시
  - 재현:
      SELECT ROW_NUMBER(123) OVER (ORDER BY v) AS r, RANK(123) OVER (ORDER BY v) AS rk FROM (SELECT 1 AS v) t;
  - 실제: 에러 없이 `1 | 1` 반환
  - 기대: `ROW_NUMBER`와 `RANK`는 인자를 받지 않으므로 구문 에러 발생

R7-031. [중간] 필수 인자가 필요한 LEAD() 및 LAG() 윈도우 함수를 인자 0개로 호출 시 에러 없이 NULL 반환
  - 재현:
      SELECT LEAD() OVER (ORDER BY v) AS ld, LAG() OVER (ORDER BY v) AS lg FROM (SELECT 1 AS v) t;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 최소 1개의 대상 표현식 인자를 요구하는 구문 에러 발생

R7-032. [중간] 필수 인자가 필요한 FIRST_VALUE() 및 LAST_VALUE() 윈도우 함수를 인자 0개로 호출 시 에러 없이 NULL 반환
  - 재현:
      SELECT FIRST_VALUE() OVER (ORDER BY v) AS fv, LAST_VALUE() OVER (ORDER BY v) AS lv FROM (SELECT 1 AS v) t;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 정확히 1개의 인자를 요구하는 구문 에러 발생


================================================================================
 5. 캐스팅 및 날짜 검증 (18건: R7-033 ~ R7-050)
================================================================================

R7-033. [높음] CAST(12345.67 AS DECIMAL(4, 2)) 실행 시 정밀도(Precision 4: 최대 99.99) 초과 검증 없이 12345.67 그대로 반환
  - 재현:
      SELECT CAST(12345.67 AS DECIMAL(4, 2)) AS v;
  - 실제: 에러 없이 `12345.67` (정밀도 7) 반환
  - 기대: `Arithmetic overflow error converting numeric to data type numeric` 에러 발생

R7-034. [높음] TRY_CAST('12.34' AS INT) 실행 시 변환 실패(NULL) 대신 소수점을 임의로 절삭하여 정수 12 반환
  - 재현:
      SELECT TRY_CAST('12.34' AS INT) AS v;
  - 실제: `12` 반환 (`'12.34'`는 유효한 정수 문자열이 아님에도 절삭 변환)
  - 기대: T-SQL 표준에 따라 문자열->INT 변환 실패로 `NULL`을 반환해야 함

R7-035. [높음] TRY_CONVERT(INT, '12.34') 실행 시 변환 실패(NULL) 대신 소수점을 절삭하여 정수 12 반환
  - 재현:
      SELECT TRY_CONVERT(INT, '12.34') AS v;
  - 실제: `12` 반환
  - 기대: `NULL` 반환

R7-036. [높음] CAST('NaN' AS FLOAT) 및 CAST('Infinity' AS FLOAT) 실행 시 에러 없이 IEEE 754 NaN/+Inf 반환
  - 재현:
      SELECT CAST('NaN' AS FLOAT) AS a, CAST('Infinity' AS FLOAT) AS b;
  - 실제: `NaN | +Inf` 반환
  - 기대: T-SQL FLOAT 타입은 NaN/Infinity를 허용하지 않으므로 변환 에러 발생

R7-037. [높음] DATEFROMPARTS(2026, 4, 31) 실행 시 존재하지 않는 날짜(4월 31일)를 에러 없이 '2026-04-31'로 반환
  - 재현:
      SELECT DATEFROMPARTS(2026, 4, 31) AS v;
  - 실제: `2026-04-31` 반환
  - 기대: `Cannot construct data type date, some of the arguments have values which are not valid` 에러 발생

R7-038. [높음] DATEFROMPARTS에서 범위를 벗어난 월(0월, 13월) 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT DATEFROMPARTS(2026, 0, 1) AS a, DATEFROMPARTS(2026, 13, 1) AS b;
  - 실제: 에러 없이 `|` (NULL) 반환
  - 기대: 월 범위(1~12) 초과 런타임 에러(Msg 289) 발생

R7-039. [높음] DATEFROMPARTS에서 0 이하의 연도(0년, -500년) 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT DATEFROMPARTS(0, 1, 1) AS a, DATEFROMPARTS(-500, 1, 1) AS b;
  - 실제: 에러 없이 `|` (NULL) 반환
  - 기대: 연도 범위(1~9999) 위반 런타임 에러(Msg 289) 발생

R7-040. [높음] DATEFROMPARTS(10000, 1, 1)에서 9999년을 초과하는 5자리 연도 전달 시 에러 없이 '10000-01-01' 반환
  - 재현:
      SELECT DATEFROMPARTS(10000, 1, 1) AS v;
  - 실제: `10000-01-01` 반환
  - 기대: 연도 상한(9999) 초과 에러 발생

R7-041. [높음] TIMEFROMPARTS에서 범위를 벗어난 60분, 60초 전달 시 에러 없이 '12:60:00', '12:00:60' 반환
  - 재현:
      SELECT TIMEFROMPARTS(12, 60, 0, 0, 0) AS a, TIMEFROMPARTS(12, 0, 60, 0, 0) AS b;
  - 실제: `12:60:00 | 12:00:60` 비정상 시각 문자열 반환
  - 기대: 분/초 범위(0~59) 초과 에러 발생

R7-042. [높음] TIMEFROMPARTS(-1, -5, -5, 0, 0) 음수 시/분/초 전달 시 에러 없이 '-1:-5:-5' 반환
  - 재현:
      SELECT TIMEFROMPARTS(-1, -5, -5, 0, 0) AS v;
  - 실제: `-1:-5:-5` 반환
  - 기대: 음수 시각 구성 요소 에러 거부

R7-043. [높음] DATETIMEFROMPARTS(2026, 4, 31, 12, 0, 0, 0) 존재하지 않는 날짜(4월 31일) 허용
  - 재현:
      SELECT DATETIMEFROMPARTS(2026, 4, 31, 12, 0, 0, 0) AS v;
  - 실제: `2026-04-31 12:00:00.000` 반환
  - 기대: 유효하지 않은 날짜 인자 에러 발생

R7-044. [중간] T-SQL 표준 DATETIME2FROMPARTS 내장 함수 미구현 에러
  - 재현:
      SELECT DATETIME2FROMPARTS(2026, 11, 19, 12, 0, 0, 0, 7) AS v;
  - 실제: `ERROR: unknown scalar function or invalid expression`
  - 기대: `2026-11-19 12:00:00.0000000` 반환

R7-045. [중간] T-SQL 표준 DATETIMEOFFSETFROMPARTS 내장 함수 미구현 에러
  - 재현:
      SELECT DATETIMEOFFSETFROMPARTS(2026, 11, 19, 12, 0, 0, 0, 9, 0, 7) AS v;
  - 실제: `ERROR: unknown scalar function or invalid expression`
  - 기대: `2026-11-19 12:00:00.0000000 +09:00` 반환

R7-046. [중간] DATEADD에 알 수 없는 날짜 단위(bogus_unit) 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT DATEADD(bogus_unit, 1, '2026-01-01') AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `'bogus_unit' is not a recognized dateadd option` 구문/인자 에러 발생

R7-047. [중간] DATEDIFF에 알 수 없는 날짜 단위(bogus_unit) 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT DATEDIFF(bogus_unit, '2026-01-01', '2026-01-10') AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `'bogus_unit' is not a recognized datediff option` 에러 발생

R7-048. [중간] DATEPART 및 DATENAME에 알 수 없는 날짜 단위(bogus_unit) 전달 시 에러 없이 NULL 반환
  - 재현:
      SELECT DATEPART(bogus_unit, '2026-01-01') AS a, DATENAME(bogus_unit, '2026-01-01') AS b;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: `'bogus_unit' is not a recognized datepart/datename option` 에러 발생

R7-049. [중간] PG 호환 DATE_TRUNC에 알 수 없는 단위('bogus_unit') 전달 시 에러 없이 원본 시각 문자열 그대로 반환
  - 재현:
      SELECT DATE_TRUNC('bogus_unit', '2026-01-01 12:34:56') AS v;
  - 실제: 에러 없이 `'2026-01-01 12:34:56'` 반환
  - 기대: `timestamp units "bogus_unit" not recognized` 에러 발생

R7-050. [중간] 무인자 함수 GETDATE(123), SYSDATETIME(123), NEWID(123), PI(123)에 인자를 전달해도 에러 없이 무시
  - 재현:
      SELECT GETDATE(123), SYSDATETIME(123), NEWID(123), PI(123);
  - 실제: 4개 무인자 함수 모두 초과 인자를 조용히 무시하고 결과 반환
  - 기대: `The function requires 0 argument(s)` 인자 개수 에러 발생


================================================================================
 6. 내장 및 집계 함수 Arity 검증 (50건: R7-051 ~ R7-100)
================================================================================

R7-051. [중간] 집계 함수 COUNT()를 인자 0개로 호출 시 구문 에러 없이 1 반환
  - 재현:
      SELECT COUNT() AS v;
  - 실제: 에러 없이 `1` 반환
  - 기대: `COUNT`는 `*` 또는 1개의 표현식 인자를 요구하므로 구문 에러 발생

R7-052. [중간] 집계 함수 COUNT(1, 2)를 인자 2개로 호출 시 구문 에러 없이 1 반환
  - 재현:
      SELECT COUNT(1, 2) AS v;
  - 실제: 에러 없이 `1` 반환
  - 기대: `The count function requires 1 argument(s)` 에러 발생

R7-053. [중간] 집계 함수 SUM() 및 AVG()를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT SUM() AS s, AVG() AS a;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 필수 인자 누락 구문 에러 발생

R7-054. [중간] 집계 함수 SUM(1, 2) 및 AVG(1, 2)를 인자 2개로 호출 시 에러 없이 첫 번째 인자만 집계하여 1 반환
  - 재현:
      SELECT SUM(1, 2) AS s, AVG(1, 2) AS a;
  - 실제: 에러 없이 `1 | 1` 반환
  - 기대: 집계 함수 초과 인자 구문 에러 발생

R7-055. [중간] 집계 함수 MIN() 및 MAX()를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT MIN() AS mn, MAX() AS mx;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 필수 인자 누락 구문 에러 발생

R7-056. [중간] 집계 함수 MIN(1, 2) 및 MAX(1, 2)를 인자 2개로 호출 시 에러 없이 첫 번째 인자만 집계하여 1 반환
  - 재현:
      SELECT MIN(1, 2) AS mn, MAX(1, 2) AS mx;
  - 실제: 에러 없이 `1 | 1` 반환
  - 기대: 집계 함수 초과 인자 구문 에러 발생

R7-057. [중간] 집계 함수 STRING_AGG(v)를 구분자(2번째 인자) 없이 인자 1개로 호출 시 에러 없이 쉼표(',')로 임의 결합
  - 재현:
      CREATE TABLE r7_sagg (v VARCHAR(5)); INSERT INTO r7_sagg VALUES ('a'), ('b');
      SELECT STRING_AGG(v) FROM r7_sagg;
  - 실제: 구분자 인자가 누락되었음에도 에러 없이 `'a,b'` 반환
  - 기대: `STRING_AGG`는 표현식과 구분자 2개의 필수 인자를 요구하므로 에러 발생

R7-058. [중간] LEN() 함수를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT LEN() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The len function requires 1 argument(s)` 에러 발생

R7-059. [중간] LEN('abc', 'def') 함수를 인자 2개로 호출 시 에러 없이 두 번째 인자를 무시하고 3 반환
  - 재현:
      SELECT LEN('abc', 'def') AS v;
  - 실제: 에러 없이 `3` 반환
  - 기대: `The len function requires 1 argument(s)` 에러 발생

R7-060. [중간] DATALENGTH() 함수를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT DATALENGTH() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The datalength function requires 1 argument(s)` 에러 발생

R7-061. [중간] DATALENGTH('abc', 'def') 함수를 인자 2개로 호출 시 에러 없이 두 번째 인자를 무시하고 3 반환
  - 재현:
      SELECT DATALENGTH('abc', 'def') AS v;
  - 실제: 에러 없이 `3` 반환
  - 기대: `The datalength function requires 1 argument(s)` 에러 발생

R7-062. [중간] REVERSE() 함수를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT REVERSE() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The reverse function requires 1 argument(s)` 에러 발생

R7-063. [중간] REVERSE('abc', 'def') 함수를 인자 2개로 호출 시 에러 없이 두 번째 인자를 무시하고 'cba' 반환
  - 재현:
      SELECT REVERSE('abc', 'def') AS v;
  - 실제: 에러 없이 `'cba'` 반환
  - 기대: `The reverse function requires 1 argument(s)` 에러 발생

R7-064. [중간] LTRIM(), RTRIM(), TRIM() 함수를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT LTRIM() AS a, RTRIM() AS b, TRIM() AS c;
  - 실제: 에러 없이 `||` (모두 NULL) 반환
  - 기대: 최소 1개의 문자열 인자를 요구하는 구문 에러 발생

R7-065. [중간] FLOOR() 및 CEILING() 함수를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT FLOOR() AS a, CEILING() AS b;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 1개의 수치 인자를 요구하는 구문 에러 발생

R7-066. [중간] FLOOR(1.5, 2.5) 및 CEILING(1.5, 2.5) 함수를 인자 2개로 호출 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT FLOOR(1.5, 2.5) AS a, CEILING(1.5, 2.5) AS b;
  - 실제: 에러 없이 `1 | 2` 반환
  - 기대: 1개의 인자만 허용하도록 구문 에러 발생

R7-067. [중간] SIGN() 함수를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT SIGN() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The sign function requires 1 argument(s)` 에러 발생

R7-068. [중간] SIGN(5, 10) 함수를 인자 2개로 호출 시 에러 없이 두 번째 인자를 무시하고 1 반환
  - 재현:
      SELECT SIGN(5, 10) AS v;
  - 실제: 에러 없이 `1` 반환
  - 기대: `The sign function requires 1 argument(s)` 에러 발생

R7-069. [중간] EXP() 함수를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT EXP() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The exp function requires 1 argument(s)` 에러 발생

R7-070. [중간] EXP(1, 2) 함수를 인자 2개로 호출 시 에러 없이 두 번째 인자를 무시하고 e 반환
  - 재현:
      SELECT EXP(1, 2) AS v;
  - 실제: 에러 없이 `2.718281828459045` 반환
  - 기대: `The exp function requires 1 argument(s)` 에러 발생

R7-071. [중간] 삼각함수 SIN(), COS(), TAN(), ATAN()을 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT SIN() AS a, COS() AS b, TAN() AS c, ATAN() AS d;
  - 실제: 에러 없이 `|||` (모두 NULL) 반환
  - 기대: 1개의 라디안 수치 인자를 요구하는 구문 에러 발생

R7-072. [중간] DEGREES() 및 RADIANS() 함수를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT DEGREES() AS a, RADIANS() AS b;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 1개의 수치 인자를 요구하는 구문 에러 발생

R7-073. [중간] POWER(2)를 인자 1개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT POWER(2) AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The power function requires 2 argument(s)` 에러 발생

R7-074. [중간] POWER(2, 3, 4)를 인자 3개로 호출 시 에러 없이 세 번째 인자를 무시하고 8 반환
  - 재현:
      SELECT POWER(2, 3, 4) AS v;
  - 실제: 에러 없이 `8` 반환
  - 기대: `The power function requires 2 argument(s)` 에러 발생

R7-075. [중간] ROUND()를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT ROUND() AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The round function requires 2 to 3 arguments` 에러 발생

R7-076. [중간] ROUND(1.23, 1, 0, 99)를 인자 4개로 호출 시 에러 없이 네 번째 인자를 무시하고 1.2 반환
  - 재현:
      SELECT ROUND(1.23, 1, 0, 99) AS v;
  - 실제: 에러 없이 `1.2` 반환
  - 기대: `The round function requires 2 to 3 arguments` 에러 발생

R7-077. [중간] LEFT('abc') 및 RIGHT('abc')를 길이 인자 없이 인자 1개로 호출 시 에러 없이 NULL 반환
  - 재현:
      SELECT LEFT('abc') AS a, RIGHT('abc') AS b;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 문자열과 길이 2개의 인자를 요구하는 구문 에러 발생

R7-078. [중간] LEFT('abc', 1, 2) 및 RIGHT('abc', 1, 2)를 인자 3개로 호출 시 에러 없이 세 번째 인자 무시
  - 재현:
      SELECT LEFT('abc', 1, 2) AS a, RIGHT('abc', 1, 2) AS b;
  - 실제: 에러 없이 `a | c` 반환
  - 기대: 정확히 2개의 인자만 허용하는 구문 에러 발생

R7-079. [중간] REPLICATE('a')를 반복 횟수 없이 인자 1개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT REPLICATE('a') AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The replicate function requires 2 argument(s)` 에러 발생

R7-080. [중간] REPLICATE('a', 3, 99)를 인자 3개로 호출 시 에러 없이 세 번째 인자를 무시하고 'aaa' 반환
  - 재현:
      SELECT REPLICATE('a', 3, 99) AS v;
  - 실제: 에러 없이 `'aaa'` 반환
  - 기대: `The replicate function requires 2 argument(s)` 에러 발생

R7-081. [중간] SPACE()를 인자 0개로, SPACE(2, 3)을 인자 2개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT SPACE() AS a, SPACE(2, 3) AS b;
  - 실제: 에러 없이 `|  ` 반환
  - 기대: `The space function requires 1 argument(s)` 에러 발생

R7-082. [중간] CHARINDEX('a') 및 PATINDEX('%a%')를 대상 문자열 없이 인자 1개로 호출 시 에러 없이 NULL 반환
  - 재현:
      SELECT CHARINDEX('a') AS a, PATINDEX('%a%') AS b;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: 최소 2개의 인자를 요구하는 구문 에러 발생

R7-083. [중간] REPLACE('abc', 'a')를 치환 문자열 없이 인자 2개로 호출 시 에러 없이 NULL 반환
  - 재현:
      SELECT REPLACE('abc', 'a') AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `The replace function requires 3 argument(s)` 에러 발생

R7-084. [중간] REPLACE('abc', 'a', 'b', 'c')를 인자 4개로 호출 시 에러 없이 네 번째 인자를 무시하고 'bbc' 반환
  - 재현:
      SELECT REPLACE('abc', 'a', 'b', 'c') AS v;
  - 실제: 에러 없이 `'bbc'` 반환
  - 기대: `The replace function requires 3 argument(s)` 에러 발생

R7-085. [중간] STUFF('abc', 1, 1)를 인자 3개로, STUFF('abc', 1, 1, 'x', 'y')를 인자 5개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT STUFF('abc', 1, 1) AS a, STUFF('abc', 1, 1, 'x', 'y') AS b;
  - 실제: 에러 없이 `|xbc` 반환
  - 기대: `The stuff function requires 4 argument(s)` 에러 발생

R7-086. [중간] SUBSTRING('abc')를 인자 1개로, SUBSTRING('abc', 1, 2, 3)을 인자 4개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT SUBSTRING('abc') AS a, SUBSTRING('abc', 1, 2, 3) AS b;
  - 실제: 에러 없이 `|ab` 반환
  - 기대: `SUBSTRING` 인자 개수 불일치 구문 에러 발생

R7-087. [중간] CONCAT_WS()를 인자 0개로, CONCAT_WS(',')를 인자 1개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT CONCAT_WS() AS a, CONCAT_WS(',') AS b;
  - 실제: 에러 없이 빈 결과 반환
  - 기대: `The concat_ws function requires at least 3 arguments` (또는 PG 최소 2개) 에러 발생

R7-088. [중간] TRANSLATE('abc', 'a')를 인자 2개로, TRANSLATE('abc', 'a', 'b', 'c')를 인자 4개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT TRANSLATE('abc', 'a') AS a, TRANSLATE('abc', 'a', 'b', 'c') AS b;
  - 실제: 에러 없이 `|bbc` 반환
  - 기대: `The translate function requires 3 argument(s)` 에러 발생

R7-089. [중간] DATEADD(day, 1)를 인자 2개로, DATEADD(day, 1, '2026-01-01', 'extra')를 인자 4개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT DATEADD(day, 1) AS a, DATEADD(day, 1, '2026-01-01', 'extra') AS b;
  - 실제: 에러 없이 `|2026-01-02 00:00:00` 반환
  - 기대: `The dateadd function requires 3 argument(s)` 에러 발생

R7-090. [중간] DATEDIFF(day, '2026-01-01')를 인자 2개로, DATEDIFF(..., 'extra')를 인자 4개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT DATEDIFF(day, '2026-01-01') AS a, DATEDIFF(day, '2026-01-01', '2026-01-02', 'extra') AS b;
  - 실제: 에러 없이 `|1` 반환
  - 기대: `The datediff function requires 3 argument(s)` 에러 발생

R7-091. [중간] DATEPART(year) 및 DATENAME(month)를 인자 1개로 또는 인자 3개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT DATEPART(year) AS a, DATENAME(month) AS b, DATEPART(year, '2026-01-01', 'extra') AS c;
  - 실제: 에러 없이 `||2026` 반환
  - 기대: `The datepart/datename function requires 2 argument(s)` 에러 발생

R7-092. [중간] YEAR(), MONTH(), DAY(), EOMONTH()를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT YEAR() AS y, MONTH() AS m, DAY() AS d, EOMONTH() AS e;
  - 실제: 에러 없이 `|||` (모두 NULL) 반환
  - 기대: 필수 날짜 인자 누락 구문 에러 발생

R7-093. [중간] YEAR('2026-01-01', 'extra'), MONTH(..., 'extra'), DAY(..., 'extra')를 인자 2개로 호출 시 초과 인자 무시
  - 재현:
      SELECT YEAR('2026-01-01', 'extra') AS y, MONTH('2026-01-01', 'extra') AS m, DAY('2026-01-01', 'extra') AS d;
  - 실제: 에러 없이 `2026 | 1 | 1` 반환
  - 기대: `The year/month/day function requires 1 argument(s)` 에러 발생

R7-094. [중간] ISNULL(1)을 인자 1개로, ISNULL(1, 2, 3)을 인자 3개로 호출 시 구문 에러 없이 1 반환
  - 재현:
      SELECT ISNULL(1) AS a, ISNULL(1, 2, 3) AS b;
  - 실제: 에러 없이 `1 | 1` 반환
  - 기대: `The isnull function requires 2 argument(s)` 에러 발생

R7-095. [중간] IIF(1=1, 'a')를 인자 2개로, IIF(1=1, 'a', 'b', 'c')를 인자 4개로 호출 시 구문 에러 미발생
  - 재현:
      SELECT IIF(1=1, 'a') AS a, IIF(1=1, 'a', 'b', 'c') AS b;
  - 실제: 에러 없이 `|a` 반환
  - 기대: `IIF`는 정확히 3개의 인자를 요구하므로 구문 에러 발생

R7-096. [중간] CHOOSE(1)을 후보 값 목록 없이 인자 1개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT CHOOSE(1) AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `CHOOSE`는 최소 2개 이상의 인자를 요구하므로 구문 에러 발생

R7-097. [중간] UNICODE(), CHAR(), NCHAR()를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT UNICODE() AS u, CHAR() AS c, NCHAR() AS n;
  - 실제: 에러 없이 `||` (모두 NULL) 반환
  - 기대: 정확히 1개의 인자를 요구하는 구문 에러 발생

R7-098. [중간] SOUNDEX()를 인자 0개로, DIFFERENCE('a')를 인자 1개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT SOUNDEX() AS s, DIFFERENCE('a') AS d;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: `SOUNDEX`는 1개, `DIFFERENCE`는 2개의 인자를 요구하는 구문 에러 발생

R7-099. [중간] QUOTENAME()을 인자 0개로, PARSENAME('a.b')을 인자 1개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT QUOTENAME() AS q, PARSENAME('a.b') AS p;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환
  - 기대: `QUOTENAME`(1~2개), `PARSENAME`(2개) 인자 개수 에러 발생

R7-100. [중간] JSON 내장 함수 ISJSON(), JSON_VALUE('{"a":1}'), JSON_QUERY(), JSON_MODIFY('{"a":1}', '$.a') 필수 인자 누락 호출 허용
  - 재현:
      SELECT ISJSON() AS j1, JSON_VALUE('{"a":1}') AS j2, JSON_QUERY() AS j3, JSON_MODIFY('{"a":1}', '$.a') AS j4;
  - 실제: 4종 모두 인자 누락 에러 없이 `|||` (모두 NULL) 반환
  - 기대: 각 JSON 내장 함수별 필수 인자 개수 검증 및 구문 에러 발생

================================================================================
