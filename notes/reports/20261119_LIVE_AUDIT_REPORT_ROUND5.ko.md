================================================================================
 LionSQL Live Engine Audit Report - Round 5 (100 New Findings)
 Date: 2026-11-19
 Target: LionSQL Database Server (Live Daemon v0.93.0, commit 3354889f, Port 5433, WiredTiger)
 Method: psql 16.15 live automated differential probe scripts (zero overlap with Round 1~4)
 Related files:
   - 20261119_lionsql_live_audit_report.txt        (Round 1)
   - 20261119_lionsql_live_audit_report_round2.txt (Round 2)
   - 20261119_lionsql_live_audit_report_round3.txt (Round 3 - 50 findings)
   - 20261119_lionsql_live_audit_report_round4.txt (Round 4 - 50 findings)
================================================================================

[총괄 요약: 신규 발굴 100건 (치명적 16 / 높음 33 / 중간 42 / 낮음 9)]
--------------------------------------------------------------------------------
  영역 1. 엔진 패닉 / 치명적 데이터 파괴 및 무결성 붕괴 (R5-001 ~ R5-016) : 16건
  영역 2. 쿼리 실행 / JOIN / Window / Subquery / CTE / SetOp (R5-017 ~ R5-042) : 26건
  영역 3. DDL / 제약조건 / 인덱스 / 시퀀스 / 트리거 / 뷰 (R5-043 ~ R5-064) : 22건
  영역 4. Stored Procedure / 변수 타입 / OUTPUT 파라미터 (R5-065 ~ R5-070) : 6건
  영역 5. 타입 / 수학 / 비트 / CAST / TRY_CAST (R5-071 ~ R5-079) : 9건
  영역 6. 날짜 / 시간 함수 및 날짜 산술 (R5-080 ~ R5-089) : 10건
  영역 7. 문자열 / JSON 내장 함수 (R5-090 ~ R5-100) : 11건
--------------------------------------------------------------------------------


================================================================================
 1. 엔진 패닉 및 치명적 결함 (Critical - 16건: R5-001 ~ R5-016)
================================================================================

R5-001. [치명적] WHERE 절 단락 평가(1/0 포함 AND/OR) 시 엔진 내부 패닉(Panic) 발생
  - 재현:
      SELECT id FROM b3_sp_t WHERE id = 99 AND 1/0 = 1;
      SELECT id FROM b3_sp_t WHERE id = 1 OR 1/0 = 1;
  - 실제: `ERROR: internal engine panic during query execution: interface conversion: proc.Expr is *proc.BinaryExpr, not *proc.LiteralExpr`
  - 기대: 단락 평가 또는 정상적인 `division by zero` 에러 (패닉 절대 금지).

R5-002. [치명적] PRIMARY KEY 컬럼을 NULL로 UPDATE 시 에러 없이 성공
  - 재현: `UPDATE p5_d1 SET id = NULL WHERE id = 1;`
  - 실제: `UPDATE 1` 성공 (PK 컬럼에 NULL 허용).
  - 기대: NOT NULL / PK 제약 위반 에러.

R5-003. [치명적] INSERT 컬럼 리스트에서 NOT NULL(무기본값) 컬럼 생략 시 NULL로 삽입 성공
  - 재현: `CREATE TABLE b2_def (id INT PRIMARY KEY, s VARCHAR(10) DEFAULT 'hello', n INT NOT NULL); INSERT INTO b2_def (id) VALUES (2);`
  - 실제: `(2, 'hello', NULL)` 삽입 성공 (명시적 NULL은 막으나 생략된 NOT NULL은 뚫림).
  - 기대: NOT NULL 제약 위반 에러.

R5-004. [치명적] UPDATE ... LIMIT N 실행 시 LIMIT 무시하고 전체 행 갱신
  - 재현: 4행 테이블에서 `UPDATE b3_vt SET v = 0 LIMIT 1;`
  - 실제: 에러 없이 `UPDATE 4`로 4행 전부 0으로 덮어쓰기 (대조: `DELETE ... LIMIT 1`은 구문 에러로 안전 차단).
  - 기대: 1행만 갱신하거나 미지원 시 구문 에러 거부.

R5-005. [치명적] LEFT JOIN ... ON 1 = 0 시 카테시안 곱(8행) 반환
  - 재현: 4행 x 2행 테이블에서 `SELECT a.id, b.w FROM b2_q1 a LEFT JOIN b2_q2 b ON 1 = 0;`
  - 실제: 8행(카테시안 곱) 반환.
  - 기대: 좌측 4행 + 우측 NULL.

R5-006. [치명적] FULL OUTER JOIN ... ON 1 = 0 시 카테시안 곱(8행) 반환
  - 재현: 4행 x 2행 테이블에서 `SELECT COUNT(*) FROM b2_q1 a FULL OUTER JOIN b2_q2 b ON 1 = 0;`
  - 실제: `8` 반환.
  - 기대: `4 + 2 = 6` 반환.

R5-007. [치명적] DELETE ... OUTPUT DELETED.* 컬럼 값 뒤바뀜(데이터 왜곡)
  - 재현: `(2, 20, 'y')` 행에 대해 `DELETE FROM p5_d1 OUTPUT DELETED.* WHERE id = 2;`
  - 실제: `2 | y | y` 출력 (`a` 컬럼 값 `20`이 `b` 컬럼 `'y'`로 오염).
  - 기대: `2 | 20 | y`.

R5-008. [치명적] INSERT INTO ... VALUES (1, (SELECT MAX(v) FROM t)) 스칼라 서브쿼리 NULL 삽입
  - 재현: `INSERT INTO b3_ins_sub VALUES (1, (SELECT MAX(v) FROM b3_sp_t));`
  - 실제: `(1, NULL)` 삽입 (기대값 `30`).

R5-009. [치명적] 윈도우 함수가 산술 표현식 안에 포함되면 전 행 NULL 반환
  - 재현: `SELECT id, v * 100 / SUM(v) OVER () AS pct FROM b3_sp_t ORDER BY id;`
  - 실제: 전 행 `NULL` 반환 (기대값: `16, 33, 50`).

R5-010. [치명적] IN 서브쿼리 내부의 LIMIT 1 무시 (전체 행 반환)
  - 재현: `SELECT id FROM b3_sp_t WHERE id IN (SELECT id FROM b3_sp_t ORDER BY v DESC LIMIT 1);`
  - 실제: `1, 2, 3` 전체 3행 반환 (기대값: `id=3` 1행).

R5-011. [치명적] TRY_CAST('999999999999999999999' AS INT) 오버플로 시 NULL 대신 MinInt64 반환
  - 재현: `SELECT TRY_CAST('999999999999999999999' AS INT);`
  - 실제: `-9223372036854775808` 가비지 정수 반환 (기대값: `NULL`).

R5-012. [치명적] DECIMAL(20, 0)에 99999999999999999999 삽입 시 MaxInt64로 조용히 잘림 및 산술 변질
  - 재현: `INSERT INTO b2_types (id, dec_big) VALUES (6, 99999999999999999999); SELECT dec_big, dec_big - 1 FROM b2_types WHERE id = 6;`
  - 실제: `9223372036854775807 | 9.223372036854776e+18` (20자리 정밀도 파괴 및 float64 변질).

R5-013. [치명적] PG nextval('seq') 반복 호출 시 시퀀스 증가 안 됨 (계속 1 반환, MAXVALUE 초과 미검출)
  - 재현: `CREATE SEQUENCE b2_seq_max START WITH 1 INCREMENT BY 1 MAXVALUE 2 NO CYCLE;` 후 `nextval('b2_seq_max')` 3회 호출.
  - 실제: `1`, `1`, `1` 반환 (시퀀스 상태 미갱신 및 MAXVALUE 초과 에러 없음).

R5-014. [치명적] ALTER TABLE ADD UNIQUE DEFAULT 5가 다중행 테이블에 중복값 5를 그대로 저장
  - 재현: 2행 테이블에 `ALTER TABLE b2_alt_u ADD u INT UNIQUE DEFAULT 5;`
  - 실제: 에러 없이 `(1, 5), (2, 5)` 중복 저장.

R5-015. [치명적] 프로시저 OUTPUT 매개변수에 NULL 대입 시 호출자 변수 미갱신
  - 재현: `CREATE PROCEDURE p @o INT OUTPUT AS BEGIN SET @o = NULL; END; DECLARE @ov INT = 999; EXEC p @o = @ov OUTPUT; SELECT @ov;`
  - 실제: `999` 유지 (기대값: `NULL`).

R5-016. [치명적] SELECT ... UNION SELECT 'abc' 타입 불일치 시 'abc'를 정수 0으로 조용히 변조
  - 재현: `SELECT 1 AS x UNION SELECT 'abc' AS x;`
  - 실제: `1, 0` 반환 (기대: 타입 변환 실패 에러).


================================================================================
 2. 쿼리 실행 / JOIN / Window / Subquery / CTE / SetOp (26건: R5-017 ~ R5-042)
================================================================================

R5-017. [높음] UPDATE ... WHERE no_such_col = 1 존재하지 않는 컬럼 에러 대신 사일런트 UPDATE 0
R5-018. [높음] DELETE ... WHERE no_such_col = 1 존재하지 않는 컬럼 에러 대신 사일런트 DELETE 0
R5-019. [높음] SELECT ... WHERE no_such_col = 1 존재하지 않는 컬럼 에러 대신 사일런트 0 rows
R5-020. [높음] JOIN ON a.id = b.no_such_col 존재하지 않는 컬럼 에러 대신 사일런트 0 rows
R5-021. [높음] SELECT ghost.id FROM b2_q1 a 존재하지 않는 테이블 별칭 ghost.을 a.로 조용히 오해석
R5-022. [높음] ORDER BY no_such_tbl.id 존재하지 않는 테이블 별칭 에러 없이 무시
R5-023. [높음] GROUP BY no_such_tbl.g 존재하지 않는 테이블 별칭 에러 없이 통과 및 그룹 행 누락 ('b' 소실)
R5-024. [높음] COUNT(DISTINCT g) OVER () 윈도우 내 DISTINCT 집계가 2 대신 0 반환
R5-025. [높음] SUM(v) OVER (ORDER BY v RANGE BETWEEN 10 PRECEDING AND 10 FOLLOWING) 값 기반 RANGE 프레임 무시 (전 행 60)
R5-026. [높음] NTH_VALUE(v, 2) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) 전 행 NULL 반환
R5-027. [높음] GROUPING_ID(g) in GROUP BY CUBE(g) 전 행 NULL 반환 (기대: 0 또는 1)
R5-028. [높음] 파생 테이블 내부 WITH CTE (`FROM (WITH a AS (...) SELECT * FROM a) s`) 실패 (`table "a" not found`)
R5-029. [높음] SELECT id, v * 2 AS v2 ... ORDER BY v2 + 1 DESC 수식 내 SELECT 별칭 참조 실패 (`column "v2" does not exist`)
R5-030. [높음] SELECT id FROM p5_t1 JOIN p5_t2 ON p5_t1.id = p5_t2.id 모호한 컬럼명(ambiguous column) 미거부
R5-031. [높음] ORDER BY id (양쪽 조인 테이블에 id 존재 시) 모호한 컬럼명 미거부
R5-032. [중간] FIRST_VALUE(g) IGNORE NULLS OVER (...) 파싱 실패 시 엉뚱한 `table "" not found` 에러
R5-033. [중간] WHERE ROW_NUMBER() OVER (ORDER BY id) = 1 금지 에러 없이 조용히 0 rows 반환
R5-034. [중간] HAVING ROW_NUMBER() OVER (ORDER BY g) = 1 금지 에러 없이 조용히 0 rows 반환
R5-035. [중간] SELECT id AS my_id FROM t WHERE my_id = 1 금지 에러 없이 조용히 0 rows 반환
R5-036. [중간] FROM b2_q1 JOIN b2_q1 ON ... 별칭 없는 자기 조인 에러 없이 통과
R5-037. [중간] FROM b2_q1 a CROSS JOIN b2_q2 b ON a.id = b.id 불법 ON 절 허용 및 ON 조건 무시 (카테시안 곱 8행)
R5-038. [중간] RANK() OVER () ORDER BY 없는 순위 윈도우 함수 미거부 (전 행 1 반환)
R5-039. [중간] SELECT TOP 1 ... LIMIT 2 동시 사용 시 TOP 1이 무시되고 2행 반환
R5-040. [중간] ROWS BETWEEN -1 PRECEDING AND CURRENT ROW 음수 프레임 경계 미거부
R5-041. [중간] SELECT * FROM (SELECT 1 AS x, 2 AS x) sub 중복 컬럼 파생 테이블에서 첫 컬럼 값 소실 (`2 | 2`)
R5-042. [낮음] SELECT * FROM (SELECT 1 AS x) 별칭 없는 파생 테이블 미거부


================================================================================
 3. DDL / 제약조건 / 인덱스 / 시퀀스 / 트리거 / 뷰 (22건: R5-043 ~ R5-064)
================================================================================

R5-043. [높음] INSERT INTO p5_d1 (id, no_such_col) VALUES (3, 1) 존재하지 않는 컬럼에 대한 INSERT 성공 (데이터 유실)
R5-044. [높음] ALTER TABLE b3_fk_c DROP CONSTRAINT fk_b3_pid 성공 후에도 FK 제약 미삭제 (이후 INSERT 여전히 거부)
R5-045. [높음] ALTER TABLE p5_d1 RENAME COLUMN a TO b 이미 존재하는 컬럼명 b로 변경 성공 (중복 컬럼 생성)
R5-046. [높음] ALTER TABLE b3_emp_pk DROP COLUMN id 빈 테이블에서 PRIMARY KEY 컬럼 직접 삭제 허용
R5-047. [높음] CREATE INDEX idx_p5_bad ON p5_d1(no_such_col) 존재하지 않는 컬럼에 인덱스 생성 성공
R5-048. [높음] CREATE TABLE b2_fk_nop (pid INT REFERENCES b2_no_such_parent(id)) 존재하지 않는 부모 테이블 FK 생성 허용
R5-049. [높음] CREATE TABLE b2_fk_noc (pid INT REFERENCES b2_fk_p(no_such_col)) 존재하지 않는 부모 컬럼 FK 생성 허용
R5-050. [높음] CREATE TABLE b2_fk_nonu (pid INT REFERENCES b2_fk_p(non_u)) 비유니크 부모 컬럼 FK 생성 허용
R5-051. [높음] ALTER TABLE b2_alt_ck ADD v INT DEFAULT -5 CHECK (v > 0) CHECK 위반 DEFAULT 백필 허용
R5-052. [높음] CREATE TRIGGER tr_b3_notbl ON b3_no_such_table_xyz 존재하지 않는 테이블에 트리거 생성 허용
R5-053. [높음] MERGE INTO 동일 타깃 행에 소스 중복 매칭 시 에러 대신 마지막 값으로 조용히 덮어쓰기
R5-054. [높음] ALTER SEQUENCE p5_seq2 RESTART WITH 10 미반영 (여전히 1부터 반환)
R5-055. [높음] CREATE VIEW ... WITH CHECK OPTION 후 INSERT INTO view 실패 (`table "p5_vco" not found`)
R5-056. [중간] CREATE TABLE p5_2pk (a INT PRIMARY KEY, b INT PRIMARY KEY) 별도 PK 컬럼 2개 선언 허용
R5-057. [중간] CREATE TABLE p5_2id (a INT IDENTITY PRIMARY KEY, b INT IDENTITY) 테이블당 IDENTITY 2개 허용
R5-058. [중간] CREATE VIEW b2_v_bad AS SELECT * FROM b2_no_such_tbl 존재하지 않는 테이블 참조 VIEW 생성 허용
R5-059. [중간] CREATE SEQUENCE b2_seq_bad1 MINVALUE 10 MAXVALUE 5 모순 시퀀스 생성 허용
R5-060. [중간] CREATE SEQUENCE b2_seq_bad2 INCREMENT BY 0 무효 시퀀스 생성 허용
R5-061. [중간] CREATE INDEX idx_b3_dup_cols ON b3_sp_t(v, v) 동일 인덱스 내 중복 컬럼 허용
R5-062. [중간] IDENTITY_INSERT ON 없이 명시적 IDENTITY 컬럼 INSERT 허용 (`INSERT INTO t (id, v) VALUES (99, 10)`)
R5-063. [낮음] UNIQUEIDENTIFIER 컬럼에 잘못된 UUID `'not-a-valid-uuid'` 그대로 저장 허용
R5-064. [낮음] TIME 컬럼에 잘못된 시각 `'25:99:99'` 그대로 저장 허용


================================================================================
 4. Stored Procedure / 변수 타입 (6건: R5-065 ~ R5-070)
================================================================================

R5-065. [높음] EXEC b2_sp_req 1 (필수 매개변수 @b 누락) 시 에러 없이 @b=NULL로 실행
R5-066. [높음] 프로시저 내 DECLARE @x INT; SET @x = 'abc'; 타입 변환 에러 없이 문자열 'abc' 저장
R5-067. [높음] 프로시저 내 테이블 변수(@tv TABLE)의 DEFAULT 'def_ok' 무시 (NULL 저장)
R5-068. [중간] 프로시저 내 DECLARE @d DATE = 'not-a-date' 변환 에러 없이 원문 문자열 저장
R5-069. [중간] 프로시저 내 RETURN 'abc' 문자열 반환 시 T-SQL 정수 변환 에러 미발생
R5-070. [낮음] DATE 컬럼에 유효하지 않은 날짜 `'2026-02-30'` INSERT 시 에러 대신 NULL 조용히 저장 (데이터 유실)


================================================================================
 5. 타입 / 수학 / 비트 / CAST (9건: R5-071 ~ R5-079)
================================================================================

R5-071. [높음] SQUARE(3037000500) int64 오버플로 미검출 및 음수 래핑 (`-9223372036709301616`)
R5-072. [중간] POWER(10, 309) 오버플로 에러 대신 IEEE 754 `+Inf` 누출
R5-073. [중간] EXP(1000) 오버플로 에러 대신 IEEE 754 `+Inf` 누출
R5-074. [중간] 비트 시프트 연산자 `(5 << 1)`, `(8 >> 1)` 미평가 및 원문 문자열 그대로 반환
R5-075. [중간] CAST('12.34' AS INT) 문자열 소수를 에러 없이 정수 `12`로 조용히 변환
R5-076. [중간] `'abc' + 5` 산술/결합 모호성: 숫자 변환 에러 대신 `'abc5'` 결합 (`'10' + 5`는 `15`로 불일치)
R5-077. [중간] `true + 1` BOOLEAN + INT 산술이 에러 없이 `2` 반환
R5-078. [낮음] `'a' LIKE 'a' ESCAPE '!!'` 2글자 이스케이프 시퀀스 에러 미발생 (`F` 반환)
R5-079. [낮음] CHAR(0) 컬럼 선언 및 문자열 `'a'` 삽입 허용


================================================================================
 6. 날짜 / 시간 함수 및 날짜 산술 (10건: R5-080 ~ R5-089)
================================================================================

R5-080. [높음] `CAST('2026-01-01' AS DATE) + 5` 날짜 덧셈 대신 문자열 결합 `'2026-01-015'` 반환
R5-081. [높음] `CAST('2026-01-10' AS DATE) - CAST('2026-01-01' AS DATE)` 날짜 차이 9 대신 `0` 반환
R5-082. [높음] `CAST('2026-01-01' AS DATE) + INTERVAL '5 days'` -> `'2026-01-01INTERVAL'` 반환
R5-083. [중간] `TIMEFROMPARTS(25, 0, 0, 0, 0)` 범위 초과 검증 누락 (`'25:00:00'` 반환)
R5-084. [중간] `SMALLDATETIMEFROMPARTS(2026, 2, 30, 0, 0)` 에러 대신 `'2026-03-02 00:00:00'` 자동 오버플로
R5-085. [중간] `DATEPART(iso_week, '2026-01-01')` 미지원 (`NULL` 반환, 기대값 `1`)
R5-086. [중간] `EXTRACT(isodow FROM CAST('2026-11-19' AS DATE))` 미지원 (`NULL` 반환, 기대값 `4`)
R5-087. [중간] `EXTRACT(doy FROM CAST('2026-12-31' AS DATE))` 미지원 (`NULL` 반환, 기대값 `365`)
R5-088. [중간] `DATEADD(millisecond, 500, '2026-01-01 00:00:00.000')` 밀리초 절삭 (`'2026-01-01 00:00:00'`)
R5-089. [중간] `DATE_TRUNC('week', '2026-11-19 14:30:00')` PG ISO 월요일(`2026-11-16`) 대신 일요일(`2026-11-15`) 반환


================================================================================
 7. 문자열 / JSON 내장 함수 (11건: R5-090 ~ R5-100)
================================================================================

R5-090. [높음] `JSON_MODIFY('{"arr":[1,2]}', 'append $.arr', 3)` 배열 추가 대신 엉뚱한 키 `"append $"` 생성으로 JSON 오염
R5-091. [높음] `JSON_MODIFY('{"a":1}', 'strict $.missing', 2)` strict 에러 대신 엉뚱한 키 `"strict $"` 생성으로 JSON 오염
R5-092. [높음] `TO_CHAR(CAST('2026-11-19 14:05:09' AS DATETIME), 'HH24:MI:SS')` -> `'1424:11I:SS'` 포맷 파괴
R5-093. [중간] `TO_DATE('19-11-2026', 'DD-MM-YYYY')` 커스텀 포맷 미인식 (`NULL` 반환)
R5-094. [중간] `TO_CHAR(1234.5, '9,999.99')` 숫자 포맷 무시 (`'1234.5'` 그대로 반환)
R5-095. [중간] 2인자 `LTRIM('xxabc', 'x')` / `RTRIM('abcxx', 'x')` 미지원 (`NULL` 반환)
R5-096. [중간] `STUFF('abc', 2, 1, NULL)` 구간 삭제(`'ac'`) 대신 전체 `NULL` 반환
R5-097. [중간] `FORMAT(255, 'X')` 16진수 포맷 `'FF'` 대신 `'255'` 반환
R5-098. [중간] `SELECT 'tag' AS t, key, value, type FROM OPENJSON('{"sub":{"x":10}}', '$.sub')` 첫 상수 컬럼이 `NULL`로 증발
R5-099. [중간] `OVERLAY('abcdef' PLACING 'XY' FROM 2 FOR 3)` 미평가 및 원문 문자열 그대로 반환
R5-100. [낮음] `SPLIT_PART('a-b-c', '-', -1)` 음수 인덱스 `NULL` 및 `LPAD('a', 5, '')` 빈 패드 문자열 공백 채움(`'    a'`)
================================================================================
