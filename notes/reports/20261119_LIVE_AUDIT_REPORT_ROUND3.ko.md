================================================================================
 LionSQL Live Engine Audit Report - Round 3 (50 New Findings)
 Date: 2026-11-19
 Target: LionSQL Database Server (Live Daemon v0.93.0, commit 3354889f, Port 5433, WiredTiger)
 Method: psql 16.15 live differential testing across DML, DDL, Window, Trigger, SP, UDF, JSON, PG/T-SQL Superset
 Related files:
   - 20261119_lionsql_live_audit_report.txt        (Round 1)
   - 20261119_lionsql_live_audit_report_round2.txt (Round 2)
================================================================================

[총괄 요약: 신규 발굴 50건 (치명적 11 / 높음 11 / 중간 24 / 낮음 4)]
--------------------------------------------------------------------------------
[치명적 - Critical (11건)]
  R3-01. SELECT <expr> AS <기존컬럼명> 표현식 무시 및 원본 컬럼값 반환 (코드 확인)
  R3-02. PK 컬럼 다중행 UPDATE 시 PRIMARY KEY 중복 생성 및 행 소실
  R3-03. 컬럼 인라인 UNIQUE 제약조건 완전 무시 (INSERT/UPDATE 중복 허용)
  R3-04. FOREIGN KEY ... ON UPDATE CASCADE 미동작 및 고아 레코드 발생
  R3-05. 상관 스칼라 서브쿼리 UPDATE 시 매칭 행까지 전부 NULL로 덮어쓰기
  R3-06. 상관 DELETE ... WHERE EXISTS 사일런트 0건 삭제
  R3-29. NEXT VALUE FOR <seq> 시퀀스 채번 대신 리터럴 문자열 그대로 반환
  R3-30. AFTER UPDATE 트리거 내 DELETED JOIN INSERTED 사일런트 0건 삽입
  R3-31. 테이블 SELECT 내 스칼라 UDF(dbo.fn(col)) 호출 시 전 행 NULL 반환
  R3-41. INSTEAD OF INSERT 트리거 가로채기 미동작 (원본 테이블 그대로 INSERT)
  R3-47. GROUP BY + RANK() OVER (ORDER BY SUM(v) DESC) 전 행 1 오평가

[높음 - High (11건)]
  R3-07. Computed 컬럼(AS (a + b)) INSERT/UPDATE 후 항상 NULL 반환
  R3-08. 다중 행 반환 스칼라 서브쿼리 에러 미발생 (첫 행 조용히 반환)
  R3-09. CREATE TABLE 중복 컬럼명 허용 (스키마 오염)
  R3-10. INSERT 컬럼 수 / VALUES 값 수 불일치 미검출 (누락 컬럼 NULL 삽입)
  R3-11. GROUP BY + SELECT 미포함 집계 함수 ORDER BY SUM(v) 실패
  R3-26. EXCEPT ALL 멀티셋 차감 무시 (중복 제거 EXCEPT로 동작해 0행 반환)
  R3-35. 필터드/부분 유니크 인덱스(CREATE UNIQUE INDEX ... WHERE ...) 조건 무시
  R3-37. UPDATE ... SET col = DEFAULT 시 기본값 대신 NULL 저장
  R3-43. ALTER TABLE ... DROP CONSTRAINT 성공 후에도 CHECK 제약 미삭제
  R3-48. 윈도우 PARTITION BY 표현식(PARTITION BY id % 2) 무시 (전체 합 반환)
  R3-49. SUM(BIGINT) 오버플로 시 에러 없이 MinInt64(-9223372036854775808) 래핑

[중간 - Medium (24건)]
  R3-12. IIF() 단락 평가(Short-circuit) 실패 (division by zero 발생)
  R3-13. SELECT TOP N PERCENT에서 PERCENT 키워드 무시
  R3-14. 괄호 수식 DEFAULT (1+2) 조용히 무시 (NULL 저장)
  R3-15. 음수 OFFSET / LIMIT 에러 없이 조용히 통과
  R3-16. LAG(v, -1) 음수 오프셋 에러 미발생 및 양수 오프셋처럼 오동작
  R3-17. ORDER BY ... ASC NULLS LAST 지정 무시 (NULLS FIRST 고정)
  R3-18. 1행 테이블에서 모집단 표준편차/분산 STDEVP/VARP가 0 대신 NULL 반환
  R3-19. DATE 컬럼 비교 시 비패딩 날짜 리터럴('2026-1-5') 문자열 비교로 불일치
  R3-21. ABS(-9223372036854775808) (MinInt64) 오버플로 미검출 및 음수 반환
  R3-22. DATEFROMPARTS(2026, 2, 30) 유효하지 않은 날짜 검증 누락 ('2026-02-30' 반환)
  R3-23. REPLACE('aaa', '', 'b') 빈 문자열 치환 시 'bababab' 문자열 오염
  R3-27. JOIN ... USING (id) 구문 파싱 실패
  R3-28. COUNT(*) FILTER (WHERE ...) 집계 절 파싱 오류 (table "" not found)
  R3-32. JSON_VALUE(..., 'strict $.missing') strict 모드 에러 미발생 (NULL 반환)
  R3-33. RAISERROR('boom', 16, 7) 후 CATCH 내 ERROR_STATE()가 7 대신 항상 1 반환
  R3-34. 프로시저 내 SCROLL CURSOR FETCH LAST / PRIOR / FIRST 파싱 실패
  R3-36. INSERT INTO ... DEFAULT VALUES 구문 미지원
  R3-38. POWER(-2, 0.5) IEEE 754 NaN 그대로 누출
  R3-39. LEFT JOIN LATERAL 결과 컬럼 헤더에 별칭 접두사(a.id, sub.w) 노출
  R3-42. CREATE TABLE ... AS SELECT ... (CTAS) 구문 미지원
  R3-44. ILIKE / ~ 등 미지원 연산자 포함 표현식이 에러 대신 원문 문자열 그대로 반환
  R3-45. STRING_AGG('x', NULL) 구분자 NULL을 문자열 "NULL"로 결합해 'xNULLx' 반환
  R3-46. UPDATE ... FROM ... 실행 시 PGWire에 불필요한 rows_affected 결과셋 전송
  R3-50. CREATE TABLE t (id INT NULL PRIMARY KEY) 모순 DDL 허용

[낮음 - Low (4건)]
  R3-20. SELECT DISTINCT ... ORDER BY <미포함컬럼> 및 중첩 집계 SUM(COUNT(*)) 미거부
  R3-24. CHARINDEX('', 'abc')가 0 대신 1 반환
  R3-25. TRANSLATE('abc', 'ab', '1') 인자 길이 불일치 시 에러 대신 NULL 반환
  R3-40. NATURAL JOIN 미지원
--------------------------------------------------------------------------------


================================================================================
 1. 치명적 결함 상세 (Critical - 11건)
================================================================================

R3-01. SELECT <expr> AS <기존컬럼명> 시 표현식 무시 및 원본 컬럼값 반환 [코드 확인]
  - 위치: pkg/execution/project.go:257-262
  - 재현:
      CREATE TABLE r3_alias_bug (id INT PRIMARY KEY, name VARCHAR(20), x INT);
      INSERT INTO r3_alias_bug VALUES (1, 'hello', NULL);
      SELECT id + 100 AS id, UPPER(name) AS name, COALESCE(x, 999) AS x FROM r3_alias_bug;
  - 실제: id=1, name='hello', x=NULL (표현식 3개 모두 무시됨)
  - 기대: id=101, name='HELLO', x=999
  - 대조: FULL OUTER JOIN의 `SELECT COALESCE(f1.id, f2.id) AS id`도 우측 전용 행에서 NULL 반환.
          별칭을 `AS cid`로 바꾸면 정상 계산됨.
  - 원인: `project.go:257-262`에서 표현식 AST(`f.parsed`) 평가 전 `tuple.GetOK(f.alias)`를 먼저 조회함.

R3-02. PK 컬럼 다중행 UPDATE 시 PRIMARY KEY 중복 생성 및 행 소실
  - 재현:
      CREATE TABLE r3_pkupd (id INT PRIMARY KEY, v VARCHAR(10));
      CREATE INDEX idx_r3_pkupd_v ON r3_pkupd(v);
      INSERT INTO r3_pkupd VALUES (1,'a'),(2,'b');
      UPDATE r3_pkupd SET id = id + 10 WHERE id = 1; -- (2,'b'), (11,'a')
      UPDATE r3_pkupd SET id = 13 - id;
      SELECT * FROM r3_pkupd ORDER BY id;
  - 실제: (11,'a'), (11,'b') — PK 11 중복 생성, id=2 행 소실.
  - 기대: (2,'a'), (11,'b') 또는 충돌 시 원자적 롤백.

R3-03. 컬럼 인라인 UNIQUE 제약조건 완전 무시
  - 재현:
      CREATE TABLE r3_uniq_atom (id INT PRIMARY KEY, code VARCHAR(10) UNIQUE);
      INSERT INTO r3_uniq_atom VALUES (1,'A'),(2,'B'),(3,'C');
      UPDATE r3_uniq_atom SET code = 'C' WHERE id <= 2; -- 성공 (UPDATE 2)
      INSERT INTO r3_uniq_atom VALUES (4, 'C');         -- 성공 (INSERT 0 1)
  - 실제: id=1..4 모두 code='C' 중복 저장.
  - 대조: 테이블 레벨 `UNIQUE (code)`는 정상적으로 `unique constraint violation` 발생.

R3-04. FOREIGN KEY ... ON UPDATE CASCADE 미동작 및 고아 레코드 발생
  - 재현:
      CREATE TABLE r3_fk_up_p (id INT PRIMARY KEY, name VARCHAR(10));
      CREATE TABLE r3_fk_up_c (cid INT PRIMARY KEY, pid INT REFERENCES r3_fk_up_p(id) ON UPDATE CASCADE);
      INSERT INTO r3_fk_up_p VALUES (1, 'p1');
      INSERT INTO r3_fk_up_c VALUES (10, 1);
      UPDATE r3_fk_up_p SET id = 2 WHERE id = 1;
  - 실제: 부모 id=2, 자식 pid=1로 참조 무결성 파괴.
  - 기대: 자식 pid가 2로 연쇄 갱신.

R3-05. 상관 스칼라 서브쿼리 UPDATE 시 매칭 행까지 전부 NULL로 덮어쓰기
  - 재현:
      CREATE TABLE r4_upd_t (id INT PRIMARY KEY, v INT);
      CREATE TABLE r4_upd_s (id INT PRIMARY KEY, score INT);
      INSERT INTO r4_upd_t VALUES (1, 0), (2, 0), (3, 0);
      INSERT INTO r4_upd_s VALUES (1, 100), (2, 200);
      UPDATE r4_upd_t SET v = (SELECT s.score FROM r4_upd_s s WHERE s.id = r4_upd_t.id);
  - 실제: (1, NULL), (2, NULL), (3, NULL) — 매칭 행까지 전부 NULL 오염.
  - 기대: (1, 100), (2, 200), (3, NULL).

R3-06. 상관 DELETE ... WHERE EXISTS 사일런트 0건 삭제
  - 재현:
      CREATE TABLE r4_del_t (id INT PRIMARY KEY, v INT);
      CREATE TABLE r4_del_s (id INT PRIMARY KEY, active INT);
      INSERT INTO r4_del_t VALUES (1, 10), (2, 20), (3, 30);
      INSERT INTO r4_del_s VALUES (1, 1), (2, 0);
      DELETE FROM r4_del_t WHERE EXISTS (SELECT 1 FROM r4_del_s s WHERE s.id = r4_del_t.id AND s.active = 0);
  - 실제: 에러 없이 0건 삭제 (3행 모두 잔존).
  - 기대: id=2 행 삭제.

R3-29. NEXT VALUE FOR <seq> 시퀀스 채번 대신 리터럴 문자열 그대로 반환
  - 재현:
      CREATE SEQUENCE r5_seq START WITH 10 INCREMENT BY 5 MAXVALUE 20 CYCLE;
      SELECT NEXT VALUE FOR r5_seq AS s1, NEXT VALUE FOR r5_seq AS s2, NEXT VALUE FOR r5_seq AS s3, NEXT VALUE FOR r5_seq AS s4;
  - 실제: 4개 컬럼 모두 리터럴 문자열 `'NEXT VALUE FOR r5_seq'` 반환.
  - 기대: 10, 15, 20, 10 (CYCLE 순환).

R3-30. AFTER UPDATE 트리거 내 DELETED JOIN INSERTED 사일런트 0건 삽입
  - 재현:
      CREATE TABLE r5_tr_t (id INT PRIMARY KEY, v INT);
      CREATE TABLE r5_tr_log (old_v INT, new_v INT);
      CREATE TRIGGER r5_tr_upd ON r5_tr_t AFTER UPDATE AS
      BEGIN
          INSERT INTO r5_tr_log (old_v, new_v)
          SELECT d.v, i.v FROM DELETED d JOIN INSERTED i ON d.id = i.id;
      END;
      INSERT INTO r5_tr_t VALUES (1, 10), (2, 20);
      UPDATE r5_tr_t SET v = v + 5;
      SELECT * FROM r5_tr_log;
  - 실제: 0 rows (감사 로그 누락).
  - 기대: (10, 15), (20, 25) 2행 기록.

R3-31. 테이블 SELECT 내 스칼라 UDF 호출 시 전 행 NULL 반환
  - 재현:
      CREATE FUNCTION dbo.r5_fn_double(@x INT) RETURNS INT AS BEGIN RETURN @x * 2; END;
      SELECT id, dbo.r5_fn_double(v) AS dv FROM r5_a ORDER BY id;
  - 실제: (1, NULL), (2, NULL).
  - 기대: (1, 20), (2, 40).

R3-41. INSTEAD OF INSERT 트리거 가로채기 미동작
  - 재현:
      CREATE TABLE r5_io_t (id INT PRIMARY KEY, v INT);
      CREATE TABLE r5_io_log (id INT, v INT);
      CREATE TRIGGER r5_tr_io ON r5_io_t INSTEAD OF INSERT AS
      BEGIN
          INSERT INTO r5_io_log SELECT id, v FROM INSERTED;
      END;
      INSERT INTO r5_io_t VALUES (1, 100);
  - 실제: r5_io_t = 1행, r5_io_log = 0행 (트리거 무시되고 원본 테이블에 직접 삽입).
  - 기대: r5_io_t = 0행, r5_io_log = 1행.

R3-47. GROUP BY + RANK() OVER (ORDER BY SUM(v) DESC) 전 행 1 오평가
  - 재현:
      CREATE TABLE r5_win_grp (id INT PRIMARY KEY, g VARCHAR(5), v INT);
      INSERT INTO r5_win_grp VALUES (1,'a',10),(2,'a',20),(3,'b',50),(4,'c',5);
      SELECT g, SUM(v) AS sv, RANK() OVER (ORDER BY SUM(v) DESC) AS rk FROM r5_win_grp GROUP BY g ORDER BY rk;
  - 실제: (a, 30, 1), (b, 50, 1), (c, 5, 1) — 모든 그룹이 1위로 평가되고 정렬도 깨짐.
  - 기대: (b, 50, 1), (a, 30, 2), (c, 5, 3).


================================================================================
 2. 높음 결함 상세 (High - 11건)
================================================================================

R3-07. Computed 컬럼(AS (a + b)) INSERT/UPDATE 후 항상 NULL 반환
  - 재현: `CREATE TABLE t (id INT PRIMARY KEY, a INT, b INT, total AS (a + b)); INSERT INTO t (id,a,b) VALUES (1,10,20); UPDATE t SET a=50 WHERE id=1; SELECT * FROM t;`
  - 실제: total=NULL. 기대: total=70.

R3-08. 다중 행 반환 스칼라 서브쿼리 에러 미발생
  - 재현: 2행 테이블에서 `SELECT (SELECT v FROM r4_sub);` 및 `SELECT id FROM r4_sub WHERE v = (SELECT v FROM r4_sub);`
  - 실제: 첫 행 `10` / `id=1` 조용히 반환. 기대: `Subquery returned more than 1 value` 에러.

R3-09. CREATE TABLE 중복 컬럼명 허용
  - 재현: `CREATE TABLE r4_dupcol (id INT PRIMARY KEY, a INT, a INT);`
  - 실제: 성공. 기대: 중복 컬럼명 에러 거부.

R3-10. INSERT 컬럼 수 / VALUES 값 수 불일치 미검출
  - 재현: `CREATE TABLE r4_ins (id INT PRIMARY KEY, a INT, b INT); INSERT INTO r4_ins (id, a, b) VALUES (1, 10);`
  - 실제: `(1, 10, NULL)` 삽입. 기대: 컬럼 수(3) != 값 수(2) 에러.

R3-11. GROUP BY + SELECT 미포함 집계 함수 ORDER BY SUM(v) 실패
  - 재현: `SELECT g FROM r4_ordagg GROUP BY g ORDER BY SUM(v) DESC;`
  - 실제: `ERROR: volcano execution open error: column "v" does not exist`. 기대: 정상 정렬 반환.

R3-26. EXCEPT ALL 멀티셋 차감 무시
  - 재현: `r5_s1=(1,1,1,2)`, `r5_s2=(1,2,2)`에서 `SELECT v FROM r5_s1 EXCEPT ALL SELECT v FROM r5_s2;`
  - 실제: 0 rows (`EXCEPT`로 동작). 기대: `1, 1` (2행).

R3-35. 필터드/부분 유니크 인덱스(CREATE UNIQUE INDEX ... WHERE ...) 조건 무시
  - 재현:
      CREATE TABLE r5_pidx (id INT PRIMARY KEY, status VARCHAR(10), code VARCHAR(10));
      CREATE UNIQUE INDEX idx_r5_pidx_active ON r5_pidx(code) WHERE status = 'active';
      INSERT INTO r5_pidx VALUES (1, 'inactive', 'X'), (2, 'inactive', 'X');
  - 실제: `duplicate key "X" in unique index` 에러 발생 (필터 밖의 행까지 강제 유일성 적용).
  - 기대: `status = 'inactive'` 행들은 필터 제외이므로 삽입 성공.

R3-37. UPDATE ... SET col = DEFAULT 시 기본값 대신 NULL 저장
  - 재현: `CREATE TABLE r5_upd_def (id INT PRIMARY KEY, status VARCHAR(10) DEFAULT 'init'); INSERT INTO r5_upd_def VALUES (1, 'done'); UPDATE r5_upd_def SET status = DEFAULT WHERE id = 1;`
  - 실제: status=NULL. 기대: status='init'.

R3-43. ALTER TABLE ... DROP CONSTRAINT 성공 후에도 CHECK 제약 미삭제
  - 재현:
      CREATE TABLE r5_dropcon (id INT PRIMARY KEY, x INT CONSTRAINT ck_pos CHECK (x > 0));
      ALTER TABLE r5_dropcon DROP CONSTRAINT ck_pos;
      INSERT INTO r5_dropcon VALUES (1, -5);
  - 실제: `DROP CONSTRAINT`는 성공했으나 이후 `INSERT (-5)`가 `check constraint violation`으로 거부됨.
  - 기대: 제약 삭제 후 `INSERT (-5)` 성공.

R3-48. 윈도우 PARTITION BY 표현식(PARTITION BY id % 2) 무시
  - 재현: `(1,10),(2,20),(3,30),(4,40)`에서 `SELECT id, SUM(v) OVER (PARTITION BY id % 2) AS ps FROM r5_gexpr ORDER BY id;`
  - 실제: 전 행 `100` (파티션 미분리). 기대: 홀수 `40`, 짝수 `60`.

R3-49. SUM(BIGINT) 오버플로 시 에러 없이 음수 래핑
  - 재현: `(1, 9223372036854775807), (2, 10)`에서 `SELECT SUM(v) FROM r5_sum_ov;`
  - 실제: `-9223372036854775808` (MinInt64). 기대: 산술 오버플로 에러 또는 정밀 합계.


================================================================================
 3. 중간 및 낮음 결함 상세 (Medium 24건 / Low 4건)
================================================================================

R3-12. [중간] IIF() 단락 평가 실패: `SELECT IIF(1=1, 1, 1/0);` -> division by zero (CASE WHEN/COALESCE는 정상).
R3-13. [중간] SELECT TOP 50 PERCENT 무시: 4행 테이블에서 `TOP 50 PERCENT`가 4행 전부 반환 (기대 2행).
R3-14. [중간] 괄호 수식 `DEFAULT (1+2)` 무시: `NULL` 저장 (`DEFAULT -1`, `DEFAULT CURRENT_TIMESTAMP`는 정상).
R3-15. [중간] 음수 `OFFSET -5` / `LIMIT -1` 미거부: 0 또는 무제한으로 조용히 통과.
R3-16. [중간] `LAG(v, -1)` 음수 오프셋 에러 미발생: `LAG(v, 1)`처럼 동작.
R3-17. [중간] `ORDER BY v ASC NULLS LAST` 무시: NULL 행이 여전히 맨 앞에 출력.
R3-18. [중간] 1행 테이블에서 `STDEVP(v)` / `VARP(v)`가 `0` 대신 `NULL` 반환.
R3-19. [중간] `DATE` 컬럼 비교 시 비패딩 날짜(`'2026-1-5'`) 불일치: 문자열 비교로 0행 반환.
R3-21. [중간] `ABS(-9223372036854775808)` MinInt64 오버플로 미검출: 음수 그대로 반환.
R3-22. [중간] `DATEFROMPARTS(2026, 2, 30)` 유효하지 않은 날짜 검증 누락: `'2026-02-30'` 반환.
R3-23. [중간] `REPLACE('aaa', '', 'b')` 빈 문자열 치환 오염: `'bababab'` 반환 (기대 `'aaa'`).
R3-27. [중간] `JOIN ... USING (id)` 파싱 실패: `expected ON after JOIN table, got (`.
R3-28. [중간] `COUNT(*) FILTER (WHERE v > 10)` 파싱 오류: `table "" not found` (WHERE 절 오분할).
R3-32. [중간] `JSON_VALUE('{"a":1}', 'strict $.missing')` strict 모드 에러 미발생 (`NULL` 반환).
R3-33. [중간] `RAISERROR('boom', 16, 7)` 후 CATCH의 `ERROR_STATE()`가 `7` 대신 항상 `1` 반환.
R3-34. [중간] 프로시저 내 `SCROLL CURSOR` `FETCH LAST`/`PRIOR`/`FIRST` 파싱 실패 (`expected INTO in FETCH, got FROM`).
R3-36. [중간] `INSERT INTO t DEFAULT VALUES` 미지원 (`invalid INSERT INTO syntax`).
R3-38. [중간] `POWER(-2, 0.5)` IEEE 754 `NaN` 누출 (`ACOS(2)`/`ASIN(2)`는 `NULL`로 불일치).
R3-39. [중간] `LEFT JOIN LATERAL` 결과 컬럼 헤더에 별칭 접두사(`a.id`, `sub.w`) 그대로 노출.
R3-42. [중간] `CREATE TABLE t AS SELECT ...` (CTAS) 미지원 (`missing opening parenthesis`).
R3-44. [중간] `ILIKE` / `~` 등 미지원 연산자 포함 표현식이 에러 대신 원문 문자열(`CASE WHEN 'Hello' ILIKE ...`) 반환.
R3-45. [중간] `STRING_AGG('x', NULL)`이 NULL 구분자를 문자열 `"NULL"`로 결합해 `'xNULLx'` 반환.
R3-46. [중간] `UPDATE ... FROM ...` 실행 시 PGWire에 불필요한 `rows_affected` 결과셋 패킷 추가 전송.
R3-50. [중간] `CREATE TABLE t (id INT NULL PRIMARY KEY)` 모순 DDL 허용.
R3-20. [낮음] `SELECT DISTINCT ... ORDER BY <미포함컬럼>` 및 중첩 집계 `SUM(COUNT(*))` 미거부.
R3-24. [낮음] `CHARINDEX('', 'abc')`가 `0` 대신 `1` 반환.
R3-25. [낮음] `TRANSLATE('abc', 'ab', '1')` 인자 길이 불일치 시 에러 대신 `NULL` 반환.
R3-40. [낮음] `NATURAL JOIN` 미지원.


================================================================================
 4. 함께 검증된 정상 동작 항목 (Pass)
================================================================================
- DML/트랜잭션: LEFT JOIN ON 필터, SAVE TRAN/ROLLBACK TRAN (최상위 및 SP 내부 모두), 자기 자신 INSERT SELECT,
  ON CONFLICT DO UPDATE (단일/복합 PK), ON CONFLICT DO NOTHING RETURNING, INSERT/UPDATE RETURNING,
  SELECT INTO, WITH CTE INSERT SELECT, 다중행 INSERT PK 위반 원자적 롤백, XACT_ABORT ON + XACT_STATE()=-1
- DDL/제약: CHECK(NULL 허용, 다중 컬럼 CHECK(a < b)), 테이블 레벨 UNIQUE, 자기참조 FK,
  TRUNCATE/DROP TABLE의 FK 참조 차단, ALTER TABLE ADD DEFAULT 백필, ALTER TABLE ADD NOT NULL 차단,
  ALTER TABLE RENAME COLUMN / RENAME TO(인덱스 유지), 중복 인덱스명 차단, 예약어 대괄호 식별자([order]/[select])
- 프로시저/분석: 재귀 프로시저(OUTPUT), CREATE OR ALTER PROCEDURE, GOTO, WHILE CONTINUE/BREAK,
  RAISERROR severity 10 계속 실행, PIVOT, CTE 컬럼 리스트, FROM (VALUES ...), GENERATE_SERIES 음수 스텝,
  OPTION (MAXRECURSION), 중첩 VIEW, GROUP BY 표현식/ROLLUP/GROUPING(), COUNT/SUM/AVG(DISTINCT),
  HAVING 별칭, ORDER BY 표현식(-v, CASE WHEN), Window RANGE/ROWS 프레임, OPENJSON + WITH, FOR JSON PATH,
  STRING_SPLIT ordinal, PG :: 캐스트, || 결합, jsonb ->>, ALL/ANY with NULL, 복합/DESC 인덱스 스캔
================================================================================
