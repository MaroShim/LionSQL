================================================================================
 LionSQL Live Engine Audit Report - Round 4 (50 New Findings)
 Date: 2026-11-19
 Target: LionSQL Database Server (Live Daemon v0.93.0, commit 3354889f, Port 5433, WiredTiger)
 Method: psql 16.15 live differential testing (zero overlap with Round 1 / 2 / 3)
 Related files:
   - 20261119_lionsql_live_audit_report.txt        (Round 1)
   - 20261119_lionsql_live_audit_report_round2.txt (Round 2)
   - 20261119_lionsql_live_audit_report_round3.txt (Round 3 - 50 findings)
================================================================================

[총괄 요약: 신규 발굴 50건 (치명적 15 / 높음 18 / 중간 16 / 낮음 1)]
--------------------------------------------------------------------------------
[치명적 - Critical (15건)]
  R4-01. WHERE (a, b) IN ((10, 100), (30, 300)) 튜플 비교 필터 풀림 (전체 행 반환)
  R4-04. HAVING SUM(s) > (SELECT AVG(s) FROM ...) 스칼라 서브쿼리 미평가 (0행 반환)
  R4-12. AFTER DELETE 트리거 내 INSERT INTO log SELECT ... FROM DELETED 사일런트 0건 삽입
  R4-17. SELECT *, expr / SELECT t.*, expr 에서 * 미확장 (컬럼명 '*'에 NULL 1컬럼 출력)
  R4-18. ALTER COLUMN v TYPE INT 시 비숫자 문자열('not_a_num')을 에러 없이 -5 가비지 정수로 오염
  R4-22. WHERE id = ANY(ARRAY[1, 3]) 배열 필터 통째로 풀림 (미매칭 행 포함 전체 반환)
  R4-26. 프로시저 내 UPDATE t SET ... FROM tbl t JOIN ... 실패 (table not found: t)
  R4-28. 프로시저 내 DELETE t FROM tbl t JOIN ... 실패 (table not found: t)
  R4-29. 비등가 LEFT JOIN / RIGHT JOIN (ON a.id < b.id) 시 미매칭 외곽 행 통째 증발
  R4-30. UPDATE t SET v = v + (SELECT MAX(v) FROM t) 비상관 서브쿼리 수식 UPDATE 시 전 행 NULL 오염
  R4-31. 다중 컬럼 LEFT JOIN (ON k1 AND k2) 시 첫 키 일치/둘째 키 불일치 좌측 행 증발
  R4-32. LEFT JOIN ON 절에 좌측 테이블 조건(AND a.a = 20) 포함 시 미매칭 좌측 행 증발
  R4-33. UPDATE ... WHERE id IN (SELECT ...) 사일런트 0건 갱신
  R4-36. DELETE ... WHERE id IN / NOT IN (SELECT ...) 사일런트 0건 삭제
  R4-37. 상관 UPDATE ... WHERE EXISTS (SELECT ...) 사일런트 0건 갱신

[높음 - High (18건)]
  R4-02. 다중 컬럼 반환 IN (SELECT a, b FROM t) 에러 미발생 (조용히 0행 반환)
  R4-03. GROUP BY GROUPING SETS ((d), (r), ()) 실패 (column "GROUPING" does not exist)
  R4-05. TO_CHAR(date, 'YYYY-MM-DD')에서 DD 포맷 치환 누락 ('2026-11-DD' 반환)
  R4-07. BOOL_AND / BOOL_OR / ARRAY_AGG / JSON_AGG 집계 미인식 (원본 행 수만큼 NULL 반환)
  R4-13. MERGE INTO ... OUTPUT $action, INSERTED.id, DELETED.id 결과셋 완전 무시
  R4-14. ALTER TABLE ... ALTER COLUMN v SET NOT NULL이 기존 NULL 행이 있어도 성공
  R4-16. BEGIN CATCH THROW; END CATCH (re-throw) 시 원본 에러 메시지 유실 ('An error occurred')
  R4-19. Inline TVF (CREATE FUNCTION ... RETURNS TABLE) 생성 후 호출 실패 (table not found)
  R4-23. SELECT TOP (1 + 1) id, a FROM t 결과 컬럼 깨짐 ('+ 1' 1컬럼, 1행만 반환)
  R4-24. 프로시저 내 SELECT TOP (@n) 동적 변수 TOP 파싱 실패
  R4-25. PG JSONB 연산자 @>, #>>, ? 사용 시 후속 컬럼 및 연산 통째 누락
  R4-27. 프로시저 내 SELECT ... INTO #tmp 생성 실패 (후속 조회 시 table not found)
  R4-34. FOREIGN KEY ... ON DELETE SET DEFAULT 미동작 (FK 위반 에러로 거부)
  R4-35. GROUP BY 쿼리 SELECT 리스트의 상관 스칼라 서브쿼리 전 행 NULL 반환
  R4-38. 단순 Updatable VIEW에 대한 UPDATE 미지원 (table not found)
  R4-47. GROUP BY 누락/비집계 컬럼 SELECT 혼합 (SELECT id, COUNT(*), GROUP BY d에 r 포함) 미거부
  R4-48. ALTER TABLE ... ADD CONSTRAINT CHECK 시 기존 위반 행(-5) 검증 누락
  R4-49. WHERE COUNT(*) > 0 집계 함수 직접 사용 미거부 (전체 행 반환)

[중간 - Medium (16건)]
  R4-06. EXTRACT(dow FROM date) 미지원 및 조용히 NULL 반환
  R4-08. SELECT DISTINCT ON (d) 미지원 (expected projection expression, got ON)
  R4-09. ANSI POSITION('b' IN 'abc') 미평가 및 원문 문자열 그대로 반환
  R4-10. DATETIMEFROMPARTS(..., 25, 0, 0, 0) 25시 범위 검증 누락 ('2026-11-19 25:00:00.000')
  R4-11. T-SQL PARSE / TRY_PARSE 미평가 및 원문 문자열 그대로 반환
  R4-15. 프로시저 변수 DECLARE @s VARCHAR(5) / DECIMAL(5,2) 길이 및 소수 자리수 미적용
  R4-20. 중첩 블록 주석 /* outer /* inner */ still comment */ 미지원
  R4-21. AT TIME ZONE 'UTC' AS tz_val 별칭 무시 (컬럼명이 'AT'로 출력)
  R4-39. EXPLAIN ANALYZE actual time=startup..total에서 startup > total 계측 오류
  R4-40. ANSI IS TRUE / IS FALSE / IS NOT TRUE 미인식 및 원문 문자열 그대로 반환
  R4-42. INSERT INTO t (id, v, v) VALUES (2, 10, 20) 중복 컬럼 리스트 허용
  R4-43. Named WINDOW w AS (...) 절 파싱 시 구문 에러 대신 엉뚱한 'table "" not found' 에러
  R4-44. CREATE TABLE t (d DECIMAL(2, 5)) scale > precision 모순 DDL 허용
  R4-45. ANSI SUBSTRING('abcdef' FROM 2 FOR 3) 미평가 및 원문 문자열 그대로 반환
  R4-46. CREATE TABLE t (s VARCHAR(-5)) 음수 길이 DDL 허용
  R4-50. CREATE VIEW v AS SELECT 1 AS a, 2 AS a 중복 컬럼 허용 및 첫 컬럼 값 덮어쓰기 (2, 2)

[낮음 - Low (1건)]
  R4-41. UPDATE t SET v = 10, v = 20 동일 컬럼 중복 대입 허용
--------------------------------------------------------------------------------


================================================================================
 1. 치명적 결함 상세 (Critical - 15건)
================================================================================

R4-01. WHERE (a, b) IN ((10, 100), (30, 300)) 튜플 비교 필터 풀림
  - 재현:
      CREATE TABLE r6_t (id INT PRIMARY KEY, a INT, b INT);
      INSERT INTO r6_t VALUES (1,10,100),(2,20,200),(3,30,300);
      SELECT id FROM r6_t WHERE (a, b) IN ((10, 100), (30, 300)) ORDER BY id;
  - 실제: id = 1, 2, 3 (매칭 안 되는 id=2까지 전체 반환).
  - 기대: id = 1, 3.

R4-04. HAVING SUM(s) > (SELECT AVG(s) FROM ...) 스칼라 서브쿼리 미평가
  - 재현:
      CREATE TABLE r6_gs (d VARCHAR(5), r VARCHAR(5), s INT);
      INSERT INTO r6_gs VALUES ('a','x',10),('a','y',20),('b','x',30);
      SELECT d, SUM(s) AS tot FROM r6_gs GROUP BY d HAVING SUM(s) > (SELECT AVG(s) FROM r6_gs);
  - 실제: 0 rows (AVG(s)=20이고 두 그룹 모두 SUM=30 > 20인데 전멸).
  - 기대: ('a', 30), ('b', 30).

R4-12. AFTER DELETE 트리거 내 DELETED 가상 테이블 사일런트 0건 삽입
  - 재현:
      CREATE TABLE r6_del_tr (id INT PRIMARY KEY, v INT);
      CREATE TABLE r6_del_log (id INT, v INT);
      CREATE TRIGGER r6_tr_ad ON r6_del_tr AFTER DELETE AS
      BEGIN INSERT INTO r6_del_log SELECT id, v FROM DELETED; END;
      INSERT INTO r6_del_tr VALUES (1, 100), (2, 200);
      DELETE FROM r6_del_tr WHERE id = 1;
      SELECT * FROM r6_del_log;
  - 실제: 0 rows. 기대: (1, 100).

R4-17. SELECT *, expr / SELECT t.*, expr 에서 * 미확장
  - 재현:
      SELECT *, id + 1 AS id2 FROM r6_t WHERE id = 1;
      SELECT t.*, t.id + 1 AS id2 FROM r6_t t WHERE t.id = 1;
  - 실제: 컬럼 헤더 `* | id2`, 값 `NULL | 2` (원본 컬럼 id, a, b 전부 증발).
  - 기대: `id | a | b | id2`, 값 `1 | 10 | 100 | 2`.

R4-18. ALTER COLUMN TYPE INT 시 비숫자 문자열을 가비지 정수로 오염
  - 재현:
      CREATE TABLE r6_alt_type (id INT PRIMARY KEY, v VARCHAR(10));
      INSERT INTO r6_alt_type VALUES (1, 'not_a_num');
      ALTER TABLE r6_alt_type ALTER COLUMN v TYPE INT;
      SELECT * FROM r6_alt_type;
  - 실제: 에러 없이 ALTER TABLE 성공 후 `(1, -5)` 반환 (데이터 파괴).
  - 기대: 타입 변환 실패 에러로 ALTER 거부 및 기존 데이터 보존.

R4-22. WHERE id = ANY(ARRAY[1, 3]) 배열 필터 통째로 풀림
  - 재현: `SELECT id FROM r6_t WHERE id = ANY(ARRAY[1, 3]) ORDER BY id;`
  - 실제: id = 1, 2, 3 (전체 행 반환).
  - 기대: id = 1, 3.

R4-26. 프로시저 내 UPDATE t SET ... FROM tbl t JOIN ... 실패
  - 재현:
      CREATE PROCEDURE r6_sp_updjoin AS
      BEGIN UPDATE t SET v = t.v + s.add_v FROM r6_sp_uj1 t JOIN r6_sp_uj2 s ON t.id = s.id; END;
      EXEC r6_sp_updjoin;
  - 실제: `ERROR: procedure execution error: updateWhere scan error on t: table not found: t`.
  - 기대: 최상위 쿼리와 동일하게 별칭 `t`를 `r6_sp_uj1`로 해석하여 정상 갱신.

R4-28. 프로시저 내 DELETE t FROM tbl t JOIN ... 실패
  - 재현:
      CREATE PROCEDURE r6_sp_deljoin AS
      BEGIN DELETE t FROM r6_sp_uj1 t JOIN r6_sp_uj2 s ON t.id = s.id; END;
      EXEC r6_sp_deljoin;
  - 실제: `ERROR: procedure execution error: deleteWhere scan error on t: table not found: t`.
  - 기대: 최상위 쿼리와 동일하게 정상 삭제.

R4-29. 비등가 LEFT JOIN / RIGHT JOIN (ON a.id < b.id) 시 미매칭 외곽 행 증발
  - 재현:
      SELECT a.id, COUNT(b.id) AS gt_cnt FROM r6_t a LEFT JOIN r6_t b ON a.id < b.id GROUP BY a.id ORDER BY a.id;
      SELECT a.id AS aid, b.id AS bid FROM r6_t a RIGHT JOIN r6_t b ON a.id < b.id ORDER BY b.id, a.id;
  - 실제: LEFT JOIN에서 id=3(0건) 증발, RIGHT JOIN에서 bid=1(aid=NULL) 증발 (INNER JOIN처럼 동작).
  - 기대: 외곽 미매칭 행 보존.

R4-30. UPDATE SET v = v + (SELECT MAX(v) FROM t) 비상관 서브쿼리 수식 UPDATE 전 행 NULL 오염
  - 재현:
      CREATE TABLE r6_self_upd (id INT PRIMARY KEY, v INT);
      INSERT INTO r6_self_upd VALUES (1,10),(2,20),(3,30);
      UPDATE r6_self_upd SET v = v + (SELECT MAX(v) FROM r6_self_upd);
  - 실제: (1, NULL), (2, NULL), (3, NULL) — 기존 데이터 전멸.
  - 기대: (1, 40), (2, 50), (3, 60).

R4-31. 다중 컬럼 LEFT JOIN (ON m1.k1 = m2.k1 AND m1.k2 = m2.k2) 좌측 행 증발
  - 재현:
      m1 = (1,10,'a'), (1,20,'b');  m2 = (1,10,'X'), (1,30,'Y');
      SELECT m1.k1, m1.k2, m1.v1, m2.v2 FROM r6_mcj1 m1 LEFT JOIN r6_mcj2 m2 ON m1.k1 = m2.k1 AND m1.k2 = m2.k2;
  - 실제: (1,10,'a','X') 1행만 반환 — (1,20,'b',NULL) 행 증발.
  - 기대: 2행 반환.

R4-32. LEFT JOIN ON 절에 좌측 테이블 조건 포함 시 미매칭 좌측 행 증발
  - 재현:
      SELECT a.id, a.a, b.w FROM r6_t a LEFT JOIN r5_b b ON a.id = b.id AND a.a = 20 ORDER BY a.id;
  - 실제: (2, 20, 200) 1행만 반환 — id=1, 3 좌측 행 증발 (WHERE로 잘못 푸시다운됨).
  - 기대: (1,10,NULL), (2,20,200), (3,30,NULL) 3행 반환.

R4-33. UPDATE ... WHERE id IN (SELECT ...) 사일런트 0건 갱신
  - 재현: `UPDATE r6_upd_in SET v = 999 WHERE id IN (SELECT id FROM r6_upd_in WHERE v >= 20);`
  - 실제: 에러 없이 0건 갱신 (10, 20, 30 그대로). 기대: id=2, 3이 999로 갱신.

R4-36. DELETE ... WHERE id IN / NOT IN (SELECT ...) 사일런트 0건 삭제
  - 재현:
      DELETE FROM r6_upd_in WHERE id IN (SELECT id FROM r6_upd_in WHERE v >= 20);
      DELETE FROM r6_din1 WHERE id NOT IN (SELECT id FROM r6_din2);
  - 실제: 두 쿼리 모두 에러 없이 0건 삭제. 기대: 조건 매칭 행 삭제.

R4-37. 상관 UPDATE ... WHERE EXISTS (SELECT ...) 사일런트 0건 갱신
  - 재현: `UPDATE r6_upd_in SET v = 999 WHERE EXISTS (SELECT 1 FROM r6_t WHERE r6_t.id = r6_upd_in.id AND r6_t.a >= 20);`
  - 실제: 에러 없이 0건 갱신. 기대: id=2, 3이 999로 갱신.


================================================================================
 2. 높음 결함 상세 (High - 18건)
================================================================================

R4-02. 다중 컬럼 반환 IN (SELECT a, b FROM r6_t) 에러 미발생 -> 조용히 0행 반환 (기대: 단일 컬럼 에러).
R4-03. GROUP BY GROUPING SETS ((d), (r), ()) 실패 -> `column "GROUPING" does not exist`.
R4-05. TO_CHAR(CAST('2026-11-19' AS DATE), 'YYYY-MM-DD') -> `'2026-11-DD'` 반환 (`DD` 치환 누락).
R4-07. BOOL_AND / BOOL_OR / ARRAY_AGG / JSON_AGG -> 집계 미인식으로 원본 3행 전부 NULL 반환.
R4-13. MERGE INTO ... OUTPUT $action, INSERTED.id, DELETED.id -> 결과셋 미반환 (`MERGE` 태그만 반환).
R4-14. ALTER TABLE r6_alt_nn ALTER COLUMN v SET NOT NULL -> 기존 행에 NULL 존재함에도 성공.
R4-16. BEGIN CATCH THROW; END CATCH -> 원본 에러 메시지 `'original boom'` 소실, `'An error occurred'` 반환.
R4-19. Inline TVF `CREATE FUNCTION dbo.r6_tvf(@min_a INT) RETURNS TABLE AS RETURN (...)` -> 생성 성공 후 `SELECT * FROM dbo.r6_tvf(20)` 시 `table "r6_tvf(20)" not found`.
R4-23. `SELECT TOP (1 + 1) id, a FROM r6_t ORDER BY id;` -> 컬럼 `+ 1` 하나에 값 `1` 1행만 반환 (컬럼/행 모두 깨짐).
R4-24. 프로시저 내 `SELECT TOP (@n) id, a FROM r6_t ORDER BY id;` -> `parse procedure error: unexpected token in statement: )`.
R4-25. PG JSONB 연산자 `'{"a":1,"b":2}'::jsonb @> '{"a":1}'::jsonb AS contains_ok, ...` -> 후속 2개 컬럼 및 `@>` 연산 통째 누락.
R4-27. 프로시저 내 `SELECT id, a INTO #sp_tmp_1 FROM r6_t;` -> 후속 `SELECT * FROM #sp_tmp_1` 시 `table "__temp_..._sp_tmp_1" not found`.
R4-34. `FOREIGN KEY ... ON DELETE SET DEFAULT` -> 부모 삭제 시 `DEFAULT 0`으로 갱신하지 못하고 FK 위반 에러로 거부.
R4-35. `SELECT grp, (SELECT SUM(v) FROM t x WHERE x.grp = t.grp) FROM t GROUP BY grp` -> 전 행 `NULL` 반환 (기대: 30, 30).
R4-38. 단순 Updatable VIEW에 대한 `UPDATE r6_uv SET v = 99 WHERE id = 1;` -> `table "r6_uv" not found`.
R4-47. `SELECT id, COUNT(*) FROM r6_t;` 및 `SELECT d, r, SUM(s) FROM r6_gs GROUP BY d;` -> 비집계 컬럼 혼합 에러 미발생 (임의 행 값 반환).
R4-48. `ALTER TABLE r6_addcon_bad ADD CONSTRAINT ck_bad_pos CHECK (v > 0);` -> 기존 행 `v = -5` 존재함에도 성공.
R4-49. `SELECT id FROM r6_t WHERE COUNT(*) > 0;` -> WHERE 절 집계 함수 금지 에러 대신 전체 3행 반환.


================================================================================
 3. 중간 및 낮음 결함 상세 (Medium 16건 / Low 1건)
================================================================================

R4-06. [중간] `EXTRACT(dow FROM CAST('2026-11-19' AS DATE))` -> `NULL` 반환 (기대: `4`).
R4-08. [중간] `SELECT DISTINCT ON (d) d, r, s FROM r6_gs ORDER BY d, s DESC;` -> `expected projection expression, got ON`.
R4-09. [중간] `SELECT POSITION('b' IN 'abc')` -> 원문 문자열 `'POSITION('b' IN 'abc')'` 그대로 반환.
R4-10. [중간] `DATETIMEFROMPARTS(2026, 11, 19, 25, 0, 0, 0)` -> 범위 검증 없이 `'2026-11-19 25:00:00.000'` 반환.
R4-11. [중간] `PARSE('2026-11-19' AS DATE)`, `TRY_PARSE('not-a-date' AS DATE)` -> 원문 문자열 그대로 반환.
R4-15. [중간] 프로시저 변수 `DECLARE @s VARCHAR(5) = '123456789'`, `DECLARE @d DECIMAL(5,2) = 12.345` -> 절삭/반올림 없이 길이 9, 소수 3자리 그대로 유지.
R4-20. [중간] 중첩 블록 주석 `/* outer /* inner */ still comment */ SELECT 1;` -> 구문 에러.
R4-21. [중간] `CAST('2026-11-19 12:00:00' AS DATETIME) AT TIME ZONE 'UTC' AS tz_val` -> 별칭 `tz_val` 무시, 컬럼명 `AT`로 출력.
R4-39. [중간] `EXPLAIN ANALYZE` `actual time=0.106..0.009` -> `startup > total` 타이밍 계측 오류.
R4-40. [중간] `NULL IS NOT TRUE`, `true IS TRUE`, `false IS FALSE` -> 원문 문자열 그대로 반환.
R4-42. [중간] `INSERT INTO r6_dup_set (id, v, v) VALUES (2, 10, 20);` -> 중복 컬럼 리스트 에러 없이 허용.
R4-43. [중간] `SELECT id, SUM(a) OVER w FROM r6_t WINDOW w AS (ORDER BY id)` -> 엉뚱한 `table "" not found` 에러.
R4-44. [중간] `CREATE TABLE r6_bad_dec (id INT PRIMARY KEY, d DECIMAL(2, 5));` -> `scale > precision` 모순 DDL 허용.
R4-45. [중간] `SELECT SUBSTRING('abcdef' FROM 2 FOR 3)` -> 원문 문자열 그대로 반환.
R4-46. [중간] `CREATE TABLE r6_bad_vc (id INT PRIMARY KEY, s VARCHAR(-5));` -> 음수 길이 DDL 허용.
R4-50. [중간] `CREATE VIEW r6_vdup AS SELECT 1 AS a, 2 AS a;` -> 중복 컬럼 허용 및 조회 시 `(2, 2)`로 첫 컬럼 값 소실.
R4-41. [낮음] `UPDATE r6_dup_set SET v = 10, v = 20 WHERE id = 1;` -> 동일 컬럼 중복 대입 허용.


================================================================================
 4. 함께 검증된 정상 동작 항목 (Pass)
================================================================================
- 트랜잭셔널 DDL 롤백 (`BEGIN; CREATE TABLE ...; ROLLBACK;` 및 `BEGIN; DROP TABLE ...; ROLLBACK;`)
- `UNPIVOT`, `GREATEST`/`LEAST` with NULL, `SPLIT_PART`, `LPAD`, `RPAD`, `INITCAP`, `NEWID()`/`RAND()` 행별 고유 평가
- `SOUNDEX`, `DIFFERENCE`, `FORMATMESSAGE`, `JSON_PATH_EXISTS`, `HASHBYTES`, `MD5`, `CHECKSUM`, `COMPRESS`/`DECOMPRESS`, `JSON_OBJECT`, `JSON_ARRAY`
- 동시 `UPDATE SET a = a + 1, b = a + 1`, `UPDATE OUTPUT DELETED/INSERTED`, `DELETE RETURNING`, 복합 FK `ON DELETE SET NULL`
- `SELECT @v1 = a WHERE 1=0` 유지 vs `SET @v2 = (SELECT ...)` NULL, `@tv TABLE` PK 제약, `sp_executesql`, `FETCH FIRST 1 ROWS WITH TIES`
- `ALTER COLUMN SET DEFAULT`/`DROP DEFAULT`, `ALTER TABLE ADD CONSTRAINT CHECK`, `PREPARE`/`EXECUTE`/`DEALLOCATE`, 복합 PK 부분 UPDATE + 보조 인덱스
- `3VL NOT (20 BETWEEN NULL AND 10)`, `NOT (1=2 OR NULL)`, `0x4142`, `1.5e2`, `SUM(SUM(s)) OVER ()`, `DISTINCT` + Window, `TRIM(LEADING ... FROM ...)`
- 중복 `SAVEPOINT` 섀도잉, `CREATE OR REPLACE VIEW`, PG 최상위 `DECLARE CURSOR` / `FETCH 1`, `EXEC` 미존재 파라미터 Error 8145 거부
================================================================================
