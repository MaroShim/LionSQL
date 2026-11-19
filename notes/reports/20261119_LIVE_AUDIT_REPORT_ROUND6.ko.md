================================================================================
 LionSQL Live Engine Audit Report - Round 6 (100 New Findings)
 Date: 2026-11-19
 Target: LionSQL Database Server (Live Daemon v0.93.0, commit cc6d0075, Port 5433, WiredTiger)
 Method: psql 16.15 & Go pq multi-session concurrency live probes (zero overlap with Round 1~5)
 Related files:
   - 20261119_lionsql_live_audit_report.txt        (Round 1 - 10 findings)
   - 20261119_lionsql_live_audit_report_round2.txt (Round 2 - 12 findings)
   - 20261119_lionsql_live_audit_report_round3.txt (Round 3 - 50 findings)
   - 20261119_lionsql_live_audit_report_round4.txt (Round 4 - 50 findings)
   - 20261119_lionsql_live_audit_report_round5.txt (Round 5 - 100 findings)
================================================================================

[총괄 요약: 신규 발굴 100건 (치명적 12 / 높음 45 / 중간 43 / 낮음 0)]
--------------------------------------------------------------------------------
  영역 1. 동시성 및 트랜잭션 (R6-001 ~ R6-009) : 9건
  영역 2. 쿼리 실행 엔진 (R6-010 ~ R6-026) : 17건
  영역 3. DDL 및 스키마 무결성 (R6-027 ~ R6-055) : 29건
  영역 4. DML 및 데이터 변이 (R6-056 ~ R6-065) : 10건
  영역 5. 프로시저 및 T-SQL (R6-066 ~ R6-074) : 9건
  영역 6. 데이터 타입 및 내장 함수 (R6-075 ~ R6-097) : 23건
  영역 7. 시스템 카탈로그 (R6-098 ~ R6-100) : 3건
--------------------------------------------------------------------------------


================================================================================
 1. 동시성 및 트랜잭션 (9건: R6-001 ~ R6-009)
================================================================================

R6-001. [치명적] SELECT ... FOR UPDATE 배타적 행 락(Row Lock) 미획득으로 타 세션 동시 UPDATE 허용
  - 재현:
      Session 1: BEGIN; SELECT v FROM r6_cc2 WHERE id = 1 FOR UPDATE;
      Session 2: UPDATE r6_cc2 SET v = 500 WHERE id = 1;
  - 실제: Session 1이 FOR UPDATE를 유지 중임에도 Session 2의 UPDATE가 대기 없이 즉시 성공(`UPDATE 1`)
  - 기대: Session 1이 트랜잭션을 종료(COMMIT/ROLLBACK)할 때까지 Session 2가 블록되거나 락 타임아웃 발생

R6-002. [치명적] SET idle_in_transaction_session_timeout 설정이 현재 세션이 아닌 서버 전역(Global)에 적용
  - 재현:
      Session 1: SET idle_in_transaction_session_timeout = 5000;
      Session 2: SHOW idle_in_transaction_session_timeout;
  - 실제: Session 2에서도 `5000`이 반환되어 타 세션의 타임아웃 정책을 오염시킴
  - 기대: SET 명령은 현재 세션 스코프에만 적용되고 타 세션은 기본값(`0`) 유지

R6-003. [높음] 기본 격리 수준에서 Write Skew(쓰기 왜곡) 이상 현상 허용
  - 재현:
      Session 1: BEGIN; UPDATE t SET val='WHITE' WHERE val='BLACK';
      Session 2: BEGIN; UPDATE t SET val='BLACK' WHERE val='WHITE';
      양 세션 순차 COMMIT;
  - 실제: 양쪽 트랜잭션 모두 에러 없이 COMMIT 성공하여 서로의 값을 교차 반전시킴(`white=1, black=1`)
  - 기대: 스냅샷/직렬화 충돌 검출 또는 행 잠금을 통해 한쪽 트랜잭션 직렬화 보장

R6-004. [높음] 명시적 트랜잭션(BEGIN...COMMIT) 내부에서 Non-Repeatable Read 발생
  - 재현:
      Session 1: BEGIN; SELECT v FROM t WHERE id = 1; (10 반환)
      Session 2: UPDATE t SET v = 99 WHERE id = 1;
      Session 1: SELECT v FROM t WHERE id = 1; COMMIT;
  - 실제: 동일 트랜잭션 내 두 번째 SELECT에서 타 세션의 커밋값 `99`가 읽힘(`first=10, second=99`)
  - 기대: WiredTiger 스냅샷 격리 기반 일관된 읽기(`10`) 보장

R6-005. [높음] 미커밋 행에 대한 타 세션 동시 UPDATE 시 Lock Wait 대신 즉시 OCC 직렬화 에러(40001) 실패
  - 재현:
      Session 1: BEGIN; UPDATE t SET v = 20 WHERE id = 1;
      Session 2: UPDATE t SET v = 30 WHERE id = 1;
  - 실제: Session 2가 락 대기 없이 즉시 `ERROR: pq: serialization failure: concurrent update conflict (40001)` 반환
  - 기대: 표준 RDBMS 행 잠금 큐를 통해 Session 1 종료 시까지 대기 후 실행

R6-006. [높음] 동일 이름의 중첩 SAVEPOINT 생성 시 내부 세이브포인트가 외부 세이브포인트를 덮어쓰지 못함
  - 재현:
      CREATE TABLE r6_sp (id INT PRIMARY KEY); BEGIN; SAVEPOINT s1; INSERT INTO r6_sp VALUES (1); SAVEPOINT s1; INSERT INTO r6_sp VALUES (2); ROLLBACK TO SAVEPOINT s1; COMMIT; SELECT * FROM r6_sp;
  - 실제: 첫 번째 `s1` 시점으로 롤백되어 `0 rows` 반환 (`id=1`까지 함께 증발)
  - 기대: 가장 최근에 선언된 두 번째 `s1`으로 롤백되어 `id=1` 행은 보존되어야 함

R6-007. [높음] RELEASE SAVEPOINT 실행 후 이미 해제된 세이브포인트로 ROLLBACK TO 성공
  - 재현:
      BEGIN; SAVEPOINT s1; RELEASE SAVEPOINT s1; ROLLBACK TO SAVEPOINT s1; COMMIT;
  - 실제: 해제된 `s1`으로 에러 없이 `ROLLBACK` 성공
  - 기대: `savepoint "s1" does not exist` 에러 발생

R6-008. [높음] 저장 프로시저 내 SET TRANSACTION ISOLATION LEVEL SERIALIZABLE 파싱 실패
  - 재현:
      CREATE PROCEDURE r6_sp_iso AS BEGIN SET TRANSACTION ISOLATION LEVEL SERIALIZABLE; SELECT 1; END;
  - 실제: `ERROR: invalid procedure body:line 1:48: unexpected token in SET statement: LEVEL`
  - 기대: 정상적인 프로시저 생성 및 트랜잭션 격리 수준 설정

R6-009. [중간] SET LOCAL timezone 구문 에러 시 16KB 규모의 내부 파서 토큰 덤프 노출
  - 재현:
      SET LOCAL timezone = 'Asia/Seoul';
  - 실제: `ERROR: line 1:19extraneous input '=' expecting {...,16KB 토큰 목록}` 반환
  - 기대: `SET LOCAL` 정상 지원 또는 내부 토큰 목록이 제거된 간결한 구문 에러 반환


================================================================================
 2. 쿼리 실행 엔진 (17건: R6-010 ~ R6-026)
================================================================================

R6-010. [치명적] CASE WHEN 표현식 조건에 ROW_NUMBER() OVER (...) 사용 시 전 행 NULL 반환
  - 재현:
      CREATE TABLE r6_cwin (id INT PRIMARY KEY); INSERT INTO r6_cwin VALUES (1), (2);
      SELECT id, CASE WHEN ROW_NUMBER() OVER (ORDER BY id) = 1 THEN 'FIRST' ELSE 'OTHER' END AS w FROM r6_cwin ORDER BY id;
  - 실제: `1 | NULL`, `2 | NULL` (CASE 표현식 전체가 NULL로 증발)
  - 기대: `1 | FIRST`, `2 | OTHER`

R6-011. [치명적] FULL OUTER JOIN의 ON 절에 비등가 추가 조건 포함 시 미매칭 외곽 행(Outer Row) 전부 누락
  - 재현:
      CREATE TABLE r6_fo1 (id INT PRIMARY KEY, v INT); CREATE TABLE r6_fo2 (id INT PRIMARY KEY, v INT);
      INSERT INTO r6_fo1 VALUES (1, 10), (2, 20); INSERT INTO r6_fo2 VALUES (1, 100), (3, 300);
      SELECT a.id, b.id FROM r6_fo1 a FULL OUTER JOIN r6_fo2 b ON a.id = b.id AND a.v < b.v;
  - 실제: `1 | 1` 1행만 반환 (`id=2` 좌측 행과 `id=3` 우측 행이 통째로 소실되어 INNER JOIN처럼 동작)
  - 기대: `(1, 1)`, `(2, NULL)`, `(NULL, 3)` 총 3행 반환

R6-012. [높음] 괄호로 묶인 중첩 집합 연산 ((q1 UNION q2) EXCEPT q3) 실행 시 내부 쿼리를 테이블명으로 오인
  - 재현:
      (SELECT 1 AS x UNION SELECT 2 AS x) EXCEPT SELECT 2 AS x;
  - 실제: `ERROR: table "SELECT 1 AS x UNION SELECT 2 AS x" not found`
  - 기대: 집합 연산 우선순위에 따라 `1` 반환

R6-013. [높음] SELECT TOP 0 실행 시 0행이 아닌 1행 반환
  - 재현:
      SELECT TOP 0 1 AS v;
  - 실제: `1` (1행 반환)
  - 기대: `0 rows` (빈 결과셋 반환)

R6-014. [높음] SELECT TOP (-1) 음수 행 제한 지정 시 에러 없이 1행 반환
  - 재현:
      SELECT TOP (-1) 1 AS v;
  - 실제: 에러 없이 `1` (1행 반환)
  - 기대: 음수 TOP 인자에 대한 런타임/구문 에러 발생

R6-015. [높음] FETCH FIRST 0 ROWS ONLY 실행 시 0행이 아닌 1행 반환
  - 재현:
      SELECT 1 AS v FETCH FIRST 0 ROWS ONLY;
  - 실제: `1` (1행 반환)
  - 기대: `0 rows` (빈 결과셋 반환)

R6-016. [높음] FETCH NEXT -1 ROWS ONLY 음수 행 패치 시 에러 없이 결과 행 반환
  - 재현:
      CREATE TABLE r6_fneg (id INT); INSERT INTO r6_fneg VALUES (1); SELECT * FROM r6_fneg FETCH NEXT -1 ROWS ONLY;
  - 실제: 에러 없이 `1` 반환
  - 기대: 음수 FETCH 행 수에 대한 에러 거부

R6-017. [높음] GROUP BY 1 상수/순번 그룹핑 시 컬럼 1 기준 그룹핑 대신 전체를 단일 그룹으로 병합
  - 재현:
      CREATE TABLE r6_gb1 (g VARCHAR(5), v INT); INSERT INTO r6_gb1 VALUES ('a', 10), ('b', 20);
      SELECT g, SUM(v) FROM r6_gb1 GROUP BY 1;
  - 실제: `a | 30` 단일 행으로 잘못 병합 반환
  - 기대: PG 모드에서는 첫 번째 컬럼 기준 `('a', 10), ('b', 20)` 2행 반환, T-SQL 모드에서는 상수 GROUP BY 에러 거부

R6-018. [높음] CTE 명시적 컬럼 리스트에 중복 컬럼명(WITH cte (a, a)) 허용 및 첫 컬럼 값 덮어쓰기 오염
  - 재현:
      WITH cte (a, a) AS (SELECT 1, 2) SELECT * FROM cte;
  - 실제: 에러 없이 `2 | 2` 반환 (첫 번째 컬럼 값 `1`이 `2`로 오염)
  - 기대: `column "a" specified more than once` 에러 거부

R6-019. [높음] 파생 테이블 별칭에 중복 컬럼명(AS v(a, a)) 허용 및 첫 컬럼 값 덮어쓰기 오염
  - 재현:
      SELECT * FROM (SELECT 1, 2) AS v(a, a);
  - 실제: 에러 없이 `2 | 2` 반환 (첫 번째 컬럼 값 `1`이 `2`로 오염)
  - 기대: 파생 테이블 중복 컬럼 별칭 에러 거부

R6-020. [높음] VALUES 절의 행별 컬럼 개수가 불일치(VALUES (1, 2), (3, 4, 5))해도 에러 없이 초과 컬럼 절삭
  - 재현:
      SELECT * FROM (VALUES (1, 2), (3, 4, 5)) AS v(a, b);
  - 실제: 에러 없이 `1|2`, `3|4` 반환 (두 번째 행의 세 번째 값 `5`를 조용히 버림)
  - 기대: `VALUES lists must all be the same length` 구문/계획 에러 발생

R6-021. [중간] CTE 컬럼 리스트 개수(3개)와 내부 SELECT 컬럼 개수(2개) 불일치 허용
  - 재현:
      WITH cte (a, b, c) AS (SELECT 1, 2) SELECT * FROM cte;
  - 실제: 에러 없이 `1 | 2` 2개 컬럼만 반환
  - 기대: CTE 정의의 컬럼 수 불일치 에러 발생

R6-022. [중간] 파생 테이블 별칭 컬럼 리스트 개수(3개)와 서브쿼리 컬럼 개수(2개) 불일치 허용
  - 재현:
      SELECT * FROM (SELECT 1, 2) AS v(a, b, c);
  - 실제: 에러 없이 `1 | 2` 반환
  - 기대: 파생 테이블 컬럼 별칭 수 불일치 에러 발생

R6-023. [중간] ORDER BY 절에 SELECT 컬럼 범위를 초과하는 순번(ORDER BY 99) 지정 시 에러 없이 통과
  - 재현:
      SELECT 1 AS a ORDER BY 99;
  - 실제: 에러 없이 `1` 반환
  - 기대: `ORDER BY position 99 is not in select list` 에러 발생

R6-024. [중간] ORDER BY 0 및 ORDER BY -1(비양수 컬럼 순번) 지정 시 에러 없이 통과
  - 재현:
      SELECT 1 AS a ORDER BY 0; SELECT 1 AS a ORDER BY -1;
  - 실제: 에러 없이 `1` 반환
  - 기대: ORDER BY 순번은 1 이상이어야 하므로 에러 거부

R6-025. [중간] 재귀 CTE OPTION (MAXRECURSION 50000) 지정 시 T-SQL 한계(32767) 검증 누락
  - 재현:
      WITH r AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM r WHERE n < 5) SELECT MAX(n) FROM r OPTION (MAXRECURSION 50000);
  - 실제: 에러 없이 `5` 반환
  - 기대: MAXRECURSION 값은 0~32767 범위만 허용되어야 함

R6-026. [중간] ANSI IS DISTINCT FROM 조건식 파싱 실패
  - 재현:
      SELECT 1 IS DISTINCT FROM 2 AS v;
  - 실제: `ERROR: AST planning error: line 1:12: expected projection expression, got DISTINCT`
  - 기대: `true` (또는 `1`) 반환


================================================================================
 3. DDL 및 스키마 무결성 (29건: R6-027 ~ R6-055)
================================================================================

R6-027. [치명적] 다중 테이블 DROP TABLE IF EXISTS t1, t2 실행 시 첫 번째 테이블만 삭제되고 나머지 테이블 잔존
  - 재현:
      CREATE TABLE r6_dt1 (id INT PRIMARY KEY); CREATE TABLE r6_dt2 (id INT PRIMARY KEY);
      DROP TABLE IF EXISTS r6_dt1, r6_dt2;
      SELECT COUNT(*) FROM r6_dt2;
  - 실제: `r6_dt1`만 삭제되고 `r6_dt2`는 삭제되지 않아 `0` 반환
  - 기대: 나열된 모든 테이블(`r6_dt1`, `r6_dt2`)이 원자적으로 삭제되어야 함

R6-028. [치명적] CREATE TABLE의 CHECK 제약조건 내부에 서브쿼리 허용 및 INSERT 시 제약 검증 완전 무시
  - 재현:
      CREATE TABLE r6_ck_sub (id INT, CHECK (id IN (SELECT 1)));
      INSERT INTO r6_ck_sub VALUES (2); SELECT id FROM r6_ck_sub;
  - 실제: `CREATE TABLE`이 성공할 뿐 아니라 조건에 위배되는 `2`가 에러 없이 삽입됨
  - 기대: DDL 시점에 CHECK 내 서브쿼리를 문법 에러로 거부해야 함

R6-029. [치명적] CREATE TABLE의 DEFAULT 절에 서브쿼리 허용 및 INSERT 시 기본값 평가 대신 NULL 삽입
  - 재현:
      CREATE TABLE r6_def_sub (a INT, id INT DEFAULT (SELECT 1));
      INSERT INTO r6_def_sub (a) VALUES (10); SELECT a, id FROM r6_def_sub;
  - 실제: `CREATE TABLE`이 성공하며, INSERT 후 `id` 컬럼에는 `1`이 아닌 `NULL`이 저장됨(`10 | NULL`)
  - 기대: DDL 시점에 DEFAULT 절 내 서브쿼리를 거부해야 함

R6-030. [높음] 데이터가 존재하는 테이블에 DEFAULT 없이 ALTER TABLE ADD col INT NOT NULL 성공
  - 재현:
      CREATE TABLE r6_ann_ne (id INT PRIMARY KEY); INSERT INTO r6_ann_ne VALUES (1);
      ALTER TABLE r6_ann_ne ADD col INT NOT NULL; SELECT id, col FROM r6_ann_ne;
  - 실제: 에러 없이 `ALTER TABLE`이 성공하며 기존 행의 NOT NULL 컬럼에 빈 값이 들어감
  - 기대: 기존 행이 있는 테이블에 DEFAULT 없는 NOT NULL 컬럼 추가 시 에러 거부

R6-031. [높음] 이미 PRIMARY KEY가 존재하는 테이블에 ALTER TABLE ADD PRIMARY KEY 중복 실행 성공
  - 재현:
      CREATE TABLE r6_apk (id INT PRIMARY KEY, v INT); ALTER TABLE r6_apk ADD PRIMARY KEY (v);
  - 실제: 에러 없이 두 번째 PRIMARY KEY 추가 성공(`ALTER TABLE`)
  - 기대: 테이블당 하나의 PRIMARY KEY만 허용되므로 에러 거부

R6-032. [높음] PRIMARY KEY 컬럼에 대해 ALTER TABLE ALTER COLUMN id DROP NOT NULL 허용
  - 재현:
      CREATE TABLE r6_pk_dropnn (id INT PRIMARY KEY); ALTER TABLE r6_pk_dropnn ALTER COLUMN id DROP NOT NULL;
  - 실제: 에러 없이 PK 컬럼의 NOT NULL 속성 제거 성공(`ALTER TABLE`)
  - 기대: PRIMARY KEY 컬럼은 NULL을 허용할 수 없으므로 에러 거부

R6-033. [높음] 존재하지 않는 컬럼을 대상으로 CREATE TABLE 테이블 레벨 PRIMARY KEY (no_such_col) 생성 성공
  - 재현:
      CREATE TABLE r6_tpk_ne (id INT, PRIMARY KEY (no_such_col));
  - 실제: 존재하지 않는 컬럼명임에도 에러 없이 `CREATE TABLE` 성공
  - 기대: `column "no_such_col" named in key does not exist` 에러 거부

R6-034. [높음] 존재하지 않는 컬럼을 대상으로 CREATE TABLE 테이블 레벨 UNIQUE (no_such_col) 생성 성공
  - 재현:
      CREATE TABLE r6_tuq_ne (id INT, UNIQUE (no_such_col));
  - 실제: 존재하지 않는 컬럼명임에도 에러 없이 `CREATE TABLE` 성공
  - 기대: `column "no_such_col" named in key does not exist` 에러 거부

R6-035. [높음] 존재하지 않는 컬럼을 대상으로 ALTER TABLE ADD PRIMARY KEY (no_such_col) 성공
  - 재현:
      CREATE TABLE r6_pk_ne (id INT); ALTER TABLE r6_pk_ne ADD PRIMARY KEY (no_such_col);
  - 실제: 에러 없이 `ALTER TABLE` 성공
  - 기대: 존재하지 않는 컬럼에 대한 PK 추가 에러 거부

R6-036. [높음] 존재하지 않는 컬럼을 대상으로 ALTER TABLE ADD CONSTRAINT UNIQUE (no_such_col) 성공
  - 재현:
      CREATE TABLE r6_uq_ne (id INT); ALTER TABLE r6_uq_ne ADD CONSTRAINT uq_ne UNIQUE (no_such_col);
  - 실제: 에러 없이 `ALTER TABLE` 성공
  - 기대: 존재하지 않는 컬럼에 대한 UNIQUE 제약 추가 에러 거부

R6-037. [높음] 복합 PRIMARY KEY 정의에 동일 컬럼 중복 지정(PRIMARY KEY (a, a)) 허용
  - 재현:
      CREATE TABLE r6_cpk_dup (a INT, b INT, PRIMARY KEY (a, a));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 키 제약 내 동일 컬럼 중복 지정 에러 거부

R6-038. [높음] 복합 UNIQUE 제약 정의에 동일 컬럼 중복 지정(UNIQUE (a, a)) 허용
  - 재현:
      CREATE TABLE r6_cuq_dup (a INT, b INT, UNIQUE (a, a));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: UNIQUE 제약 내 동일 컬럼 중복 지정 에러 거부

R6-039. [높음] FOREIGN KEY 자식 컬럼 수(2개)와 부모 참조 컬럼 수(1개) 불일치 DDL 허용
  - 재현:
      CREATE TABLE r6_fkm_p (id INT PRIMARY KEY);
      CREATE TABLE r6_fkm_c (a INT, b INT, FOREIGN KEY (a, b) REFERENCES r6_fkm_p(id));
  - 실제: 참조 컬럼 개수가 다름에도 에러 없이 `CREATE TABLE` 성공
  - 기대: 외래키 컬럼 수와 참조 컬럼 수 불일치 에러 거부

R6-040. [높음] FOREIGN KEY 자식 컬럼(VARCHAR)과 부모 PK 컬럼(INT) 간 데이터 타입 불일치 DDL 허용
  - 재현:
      CREATE TABLE r6_fkt_p (id INT PRIMARY KEY);
      CREATE TABLE r6_fkt_c (a VARCHAR(20) REFERENCES r6_fkt_p(id));
  - 실제: 호환되지 않는 타입 간 외래키 선언이 에러 없이 `CREATE TABLE` 성공
  - 기대: 외래키 참조 컬럼 간 타입 불일치 에러 거부

R6-041. [높음] 자기 참조(Self-Referencing) Foreign Key 테이블에서 자식이 없는 최상위 부모 행 DELETE 거부
  - 재현:
      CREATE TABLE r6_sr_fk (id INT PRIMARY KEY, pid INT REFERENCES r6_sr_fk(id));
      INSERT INTO r6_sr_fk VALUES (1, NULL); DELETE FROM r6_sr_fk WHERE id = 1;
  - 실제: 참조하는 자식 행이 전혀 없음에도 외래키 참조 무결성 에러로 삭제 실패 또는 잔존
  - 기대: `id=1` 행을 참조하는 다른 행이 없으므로 정상 삭제되어야 함

R6-042. [높음] INT 컬럼에 숫자가 아닌 문자열 기본값(DEFAULT 'not_an_int') 지정 시 CREATE TABLE 성공
  - 재현:
      CREATE TABLE r6_bad_def (id INT PRIMARY KEY, v INT DEFAULT 'not_an_int');
  - 실제: 타입 검증 없이 `CREATE TABLE` 성공
  - 기대: 컬럼 타입과 호환되지 않는 DEFAULT 리터럴 에러 거부

R6-043. [높음] 고정 길이 컬럼 합계가 8060바이트(Inline Row Limit)를 초과하는 테이블 생성 허용
  - 재현:
      CREATE TABLE r6_wide (c1 CHAR(3000), c2 CHAR(3000), c3 CHAR(3000));
  - 실제: 합계 9000바이트임에도 `ErrInlineRowTooLarge` 없이 `CREATE TABLE` 성공
  - 기대: 페이지 인라인 행 크기 한계(8060바이트) 초과 에러 거부

R6-044. [높음] 명시적 컬럼 리스트를 지정한 뷰(CREATE VIEW v (c1, c2)) 조회 시 전 컬럼 NULL 반환
  - 재현:
      CREATE VIEW r6_v_cols (c1, c2) AS SELECT 10 AS a, 20 AS b; SELECT c1, c2 FROM r6_v_cols;
  - 실제: `|` (두 컬럼 모두 빈 값/NULL 반환, 뷰 컬럼 매핑 소실)
  - 기대: `10 | 20` 반환

R6-045. [중간] CREATE VIEW 명시적 컬럼 리스트에 중복 컬럼명(CREATE VIEW v (a, a)) 허용
  - 재현:
      CREATE VIEW r6_vdup (a, a) AS SELECT 1, 2;
  - 실제: 에러 없이 `CREATE VIEW` 성공
  - 기대: 뷰 컬럼명 중복 에러 거부

R6-046. [중간] CREATE VIEW 명시적 컬럼 수(3개)와 SELECT 프로젝션 컬럼 수(2개) 불일치 허용
  - 재현:
      CREATE VIEW r6_vcnt (a, b, c) AS SELECT 1, 2;
  - 실제: 에러 없이 `CREATE VIEW` 성공
  - 기대: 뷰 컬럼 수와 쿼리 컬럼 수 불일치 에러 거부

R6-047. [중간] NUMERIC(0, 0) 정밀도(Precision) 0인 컬럼 정의 허용
  - 재현:
      CREATE TABLE r6_num_z (v NUMERIC(0, 0));
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: NUMERIC 정밀도는 1~38 범위여야 하므로 에러 거부

R6-048. [중간] 단일 컬럼에 상충하는 NOT NULL NULL 제약 동시 지정 허용
  - 재현:
      CREATE TABLE r6_nn_n (a INT NOT NULL NULL);
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 상충하는 NULL 허용 여부 지정 에러 거부

R6-049. [중간] 단일 컬럼에 다중 DEFAULT 절(DEFAULT 1 DEFAULT 2) 중복 지정 허용
  - 재현:
      CREATE TABLE r6_2def (a INT DEFAULT 1 DEFAULT 2);
  - 실제: 에러 없이 `CREATE TABLE` 성공
  - 기대: 단일 컬럼에 다중 DEFAULT 선언 구문 에러 거부

R6-050. [중간] ALTER TABLE RENAME COLUMN a TO a 동일 컬럼명으로 이름 변경 시 에러 없이 성공
  - 재현:
      CREATE TABLE r6_rensame (a INT); ALTER TABLE r6_rensame RENAME COLUMN a TO a;
  - 실제: 에러 없이 `ALTER TABLE` 성공 (대조: 테이블 동일 이름 변경은 에러 처리됨)
  - 기대: 동일 컬럼명으로의 변경 거부

R6-051. [중간] CREATE SEQUENCE에서 START WITH 값이 MINVALUE보다 작은 모순 정의 허용
  - 재현:
      CREATE SEQUENCE r6_seq_stmin MINVALUE 50 START WITH 10;
  - 실제: 에러 없이 `SEQUENCE` 생성 성공
  - 기대: `START value (10) cannot be less than MINVALUE (50)` 에러 거부

R6-052. [중간] CREATE SEQUENCE에서 START WITH 값이 MAXVALUE보다 큰 모순 정의 허용
  - 재현:
      CREATE SEQUENCE r6_seq_gtmax MAXVALUE 10 START WITH 50;
  - 실제: 에러 없이 `SEQUENCE` 생성 성공
  - 기대: `START value (50) cannot be greater than MAXVALUE (10)` 에러 거부

R6-053. [중간] CREATE SEQUENCE에서 음수 CACHE 크기(CACHE -5) 허용
  - 재현:
      CREATE SEQUENCE r6_seq_cneg CACHE -5;
  - 실제: 에러 없이 `SEQUENCE` 생성 성공
  - 기대: CACHE 값은 양의 정수여야 하므로 에러 거부

R6-054. [중간] ALTER SEQUENCE를 통해 INCREMENT BY 0 및 MINVALUE > MAXVALUE로 변경 허용
  - 재현:
      CREATE SEQUENCE r6_seq_alt; ALTER SEQUENCE r6_seq_alt INCREMENT BY 0; ALTER SEQUENCE r6_seq_alt MINVALUE 100 MAXVALUE 10;
  - 실제: 두 ALTER SEQUENCE 명령 모두 에러 없이 성공
  - 기대: ALTER SEQUENCE 시에도 0 증분 및 MIN > MAX 역전 검증 필수

R6-055. [중간] ALTER SEQUENCE RESTART WITH 절에 정수가 아닌 문자열('bad') 허용
  - 재현:
      CREATE SEQUENCE r6_seq_rst; ALTER SEQUENCE r6_seq_rst RESTART WITH 'bad';
  - 실제: 에러 없이 `SEQUENCE` 변경 성공
  - 기대: 정수 리터럴이 아닌 재시작 값에 대해 구문/타입 에러 발생


================================================================================
 4. DML 및 데이터 변이 (10건: R6-056 ~ R6-065)
================================================================================

R6-056. [치명적] PG 구문 UPDATE ... FROM 실행 시 조인 테이블 컬럼 참조가 NULL로 평가되어 대상 컬럼을 NULL로 파괴
  - 재현:
      CREATE TABLE r6_upf1 (id INT PRIMARY KEY, v INT); CREATE TABLE r6_upf2 (id INT PRIMARY KEY, v INT);
      INSERT INTO r6_upf1 VALUES (1, 10); INSERT INTO r6_upf2 VALUES (1, 99);
      UPDATE r6_upf1 SET v = r6_upf2.v FROM r6_upf2 WHERE r6_upf1.id = r6_upf2.id; SELECT v FROM r6_upf1;
  - 실제: `v` 값이 `99`가 아닌 `NULL`로 덮어써짐
  - 기대: `99`로 정상 갱신

R6-057. [치명적] 단일 UPDATE 문으로 UNIQUE 컬럼의 두 행 값을 서로 맞바꿀 때(Swap) 중간 상태 중복 키 에러 발생
  - 재현:
      CREATE TABLE r6_uswap (id INT PRIMARY KEY, code VARCHAR(5), UNIQUE(code));
      INSERT INTO r6_uswap VALUES (1, 'A'), (2, 'B');
      UPDATE r6_uswap SET code = CASE WHEN id = 1 THEN 'B' ELSE 'A' END;
  - 실제: `ERROR: unique constraint violation` 발생 (행 단위 즉시 검증으로 인해 원자적 스왑 불가)
  - 기대: SQL 표준에 따라 구문(Statement) 종료 시점에 유일성을 검증하여 `1|B, 2|A`로 스왑 성공해야 함

R6-058. [치명적] 단일 UPDATE 문으로 PRIMARY KEY 값을 1씩 시프트(SET id = id + 1)할 때 중복 키 충돌로 실패
  - 재현:
      CREATE TABLE r6_pkshift (id INT PRIMARY KEY); INSERT INTO r6_pkshift VALUES (1), (2);
      UPDATE r6_pkshift SET id = id + 1;
  - 실제: `ERROR: duplicate key` 에러로 업데이트 실패
  - 기대: 구문 단위 원자적 키 갱신을 통해 `(2), (3)`으로 정상 갱신되어야 함

R6-059. [높음] PG 구문 INSERT ... ON CONFLICT DO NOTHING (충돌 타깃 생략) 실행 시 구문 에러 또는 미동작
  - 재현:
      CREATE TABLE r6_oc_notgt (id INT PRIMARY KEY, v INT); INSERT INTO r6_oc_notgt VALUES (1, 10);
      INSERT INTO r6_oc_notgt VALUES (1, 99), (2, 20) ON CONFLICT DO NOTHING;
  - 실제: 타깃 컬럼을 생략한 표준 PG `ON CONFLICT DO NOTHING`이 실패하여 `id=2`가 삽입되지 않음
  - 기대: `id=1` 충돌은 무시하고 `id=2`가 정상 삽입되어 총 2행이 되어야 함

R6-060. [높음] 자기 자신 테이블 복사 INSERT INTO t SELECT * FROM t 실행 시 컬럼 수 불일치 에러 발생
  - 재현:
      CREATE TABLE r6_self_ins (id INT, v INT); INSERT INTO r6_self_ins VALUES (1, 10);
      INSERT INTO r6_self_ins SELECT * FROM r6_self_ins;
  - 실제: `ERROR: column count mismatch` 또는 내부 실행 에러로 자기 복제 실패
  - 기대: 동일 스키마 테이블의 `*` 프로젝션이므로 2행으로 정상 복제되어야 함

R6-061. [높음] 다중 행 INSERT 문에서 행별 튜플 값 개수가 불일치(VALUES (1, 10), (2))해도 에러 없이 삽입 성공
  - 재현:
      CREATE TABLE r6_jag (id INT PRIMARY KEY, v INT); INSERT INTO r6_jag VALUES (1, 10), (2);
  - 실제: 두 번째 행의 두 번째 컬럼 값이 누락되었음에도 에러 없이 `INSERT 0 2` 성공
  - 기대: 다중 행 VALUES 내 튜플 길이 불일치 구문 에러 거부

R6-062. [높음] T-SQL 표준 UPDATE TOP (1) 구문 파싱 실패
  - 재현:
      CREATE TABLE r6_utop (id INT PRIMARY KEY, v INT); INSERT INTO r6_utop VALUES (1, 10), (2, 20);
      UPDATE TOP (1) r6_utop SET v = 99;
  - 실제: `ERROR: invalid UPDATE syntax`
  - 기대: 1행만 `v = 99`로 정상 갱신

R6-063. [높음] T-SQL 표준 DELETE TOP (1) 구문 파싱 실패
  - 재현:
      CREATE TABLE r6_dtop (id INT PRIMARY KEY); INSERT INTO r6_dtop VALUES (1), (2);
      DELETE TOP (1) FROM r6_dtop;
  - 실제: `ERROR: invalid DELETE syntax`
  - 기대: 1행만 정상 삭제

R6-064. [높음] PG SERIAL 의사 타입(Pseudo-type) 컬럼에 DEFAULT 생략 INSERT 시 자동 채번 미동작
  - 재현:
      CREATE TABLE r6_ser (id SERIAL PRIMARY KEY, name VARCHAR(10));
      INSERT INTO r6_ser (name) VALUES ('a'), ('b');
  - 실제: SERIAL 컬럼이 자동 증가하지 않아 NULL 또는 중복 키 에러로 실패
  - 기대: `1, 2`로 자동 시퀀스 채번되어 삽입 성공

R6-065. [중간] CREATE TABLE ... AS SELECT ... WITH NO DATA 및 CREATE MATERIALIZED VIEW, CREATE TYPE AS ENUM, CREATE INDEX CONCURRENTLY 구문 미지원
  - 재현:
      CREATE INDEX CONCURRENTLY idx_c ON t(id);
      CREATE MATERIALIZED VIEW mv AS SELECT 1;
      CREATE TYPE mood AS ENUM ('sad', 'ok');
      CREATE TABLE ctas_nodata AS SELECT 1 AS a WITH NO DATA;
  - 실제: 4종 모두 파서 구문 에러(`invalid syntax` / `no viable alternative` / `missing '('`) 발생
  - 기대: PG 호환 DDL 구문 파싱 또는 명시적 기능 미지원 에러 코드 반환


================================================================================
 5. 프로시저 및 T-SQL (9건: R6-066 ~ R6-074)
================================================================================

R6-066. [치명적] 테이블 변수 DECLARE @tv TABLE (id INT IDENTITY(1,1))에 INSERT 시 IDENTITY 값이 전혀 생성되지 않음
  - 재현:
      EXEC sp_executesql N'DECLARE @tv TABLE (id INT IDENTITY(1,1), v VARCHAR(10)); INSERT INTO @tv (v) VALUES (''a''); SELECT id FROM @tv;';
  - 실제: `id`가 `1`이 아닌 빈 값(`NULL`)으로 반환됨
  - 기대: 테이블 변수에서도 IDENTITY(1,1)이 정상 채번되어 `1` 반환

R6-067. [높음] 스칼라 UDF 호출 시 인자로 DEFAULT 키워드를 전달하면 기본값 대신 문자열 'DEFAULT'로 평가
  - 재현:
      CREATE FUNCTION r6_fn_def (@a INT, @b VARCHAR(10) = 'xyz') RETURNS VARCHAR(20) AS BEGIN RETURN CAST(@a AS VARCHAR(10)) + @b; END;
      SELECT dbo.r6_fn_def(5, DEFAULT) AS v;
  - 실제: `5DEFAULT` 반환 (`DEFAULT` 키워드를 매개변수 기본값 `'xyz'`가 아닌 리터럴 문자열 `'DEFAULT'`로 처리)
  - 기대: `5xyz` 반환

R6-068. [높음] 다중 문장 테이블 반환 함수(Multi-statement TVF: RETURNS @res TABLE) 생성 구문 파싱 실패
  - 재현:
      CREATE FUNCTION r6_fn_tvf (@x INT) RETURNS @res TABLE (id INT) AS BEGIN INSERT INTO @res VALUES (@x); RETURN; END;
  - 실제: `ERROR: expected return data type after RETURNS`
  - 기대: 다중 문장 TVF 정상 생성 및 테이블 변수 반환 지원

R6-069. [높음] 트리거 본문 내 T-SQL 표준 IF UPDATE(col) 조건절 파싱 실패
  - 재현:
      CREATE TABLE r6_tr_upd (id INT PRIMARY KEY, a INT);
      CREATE TRIGGER r6_tr_u ON r6_tr_upd AFTER UPDATE AS BEGIN IF UPDATE(a) BEGIN SELECT 1; END END;
  - 실제: `ERROR: invalid trigger body:line 1:9: expected condition in IF`
  - 기대: `UPDATE(col)` 함수를 IF 조건식으로 정상 파싱 및 컬럼 변경 여부 판별

R6-070. [높음] 저장 프로시저 반환 코드 캡처(EXEC @rc = proc) 실행 시 RETURN 값을 잃어버리고 항상 0 반환
  - 재현:
      CREATE PROCEDURE r6_sp_ret AS BEGIN RETURN 42; END;
      DECLARE @rc INT = 0; EXEC @rc = r6_sp_ret; SELECT @rc;
  - 실제: `0` 반환 (프로시저가 반환한 `42`가 `@rc`에 바인딩되지 않음)
  - 기대: `42` 반환

R6-071. [중간] TRY...CATCH 블록에서 시스템 산술 에러(1/0) 발생 시 ERROR_NUMBER()가 8134 대신 50000을 하드코딩 반환
  - 재현:
      EXEC sp_executesql N'BEGIN TRY DECLARE @x INT = 1 / 0; END TRY BEGIN CATCH SELECT ERROR_NUMBER() AS en; END CATCH';
  - 실제: `50000` (사용자 정의 기본 에러 번호) 반환
  - 기대: 0으로 나누기 시스템 에러 번호인 `8134` 반환

R6-072. [중간] WAITFOR DELAY 구문 실행 시 런타임 미구현 에러([42000]) 발생
  - 재현:
      WAITFOR DELAY '00:00:00.005';
  - 실제: `ERROR: [42000] Valid T-SQL syntax recognized ('WAITFOR statement'), but runtime execution is not yet implemented in this release.`
  - 기대: 지정된 시간만큼 대기 후 정상 완료

R6-073. [중간] 독립 배치에서 DECLARE @var INT 변수 선언 시 PGWire 명령 완료 태그로 'DECLARE CURSOR' 오반환
  - 재현:
      DECLARE @i INT = 0;
  - 실제: 커서 선언이 아닌 스칼라 변수 선언임에도 `DECLARE CURSOR` 명령 태그 반환
  - 기대: 스칼라 변수 선언에 맞는 명령 태그(`DECLARE` 또는 `SET`) 반환

R6-074. [중간] @@LANGUAGE 및 @@NESTLEVEL 시스템 전역 변수가 현재 세션 상태 대신 NULL 반환
  - 재현:
      SELECT @@LANGUAGE AS l, @@NESTLEVEL AS n;
  - 실제: `|` (두 컬럼 모두 NULL 반환)
  - 기대: `us_english | 0` 반환


================================================================================
 6. 데이터 타입 및 내장 함수 (23건: R6-075 ~ R6-097)
================================================================================

R6-075. [높음]  단항 음수 연산자 -(-9223372036854775808) (MinInt64) 실행 시 오버플로 에러 없이 음수 자기 자신 반환
  - 재현:
      SELECT -(-9223372036854775808) AS v;
  - 실제: 에러 없이 `-9223372036854775808` 반환 (`-x == x` 수학적 모순 발생)
  - 기대: 64비트 부호 있는 정수 최대값(`9223372036854775807`)을 초과하므로 산술 오버플로 에러 발생

R6-076. [높음] CHECKSUM_AGG 집계 함수가 데이터가 있는 정수 테이블에서 NULL 반환
  - 재현:
      CREATE TABLE r6_chk (v INT); INSERT INTO r6_chk VALUES (1), (2), (3); SELECT CHECKSUM_AGG(v) FROM r6_chk;
  - 실제: `NULL` (빈 값) 반환
  - 기대: 정수들의 XOR 체크섬 집계값 반환

R6-077. [높음] GENERATE_SERIES(1, 5, 2) 호출 시 수열 값 대신 빈(NULL) 행들만 반환
  - 재현:
      SELECT * FROM GENERATE_SERIES(1, 5, 2);
  - 실제: 값이 비어 있는 행 반환
  - 기대: `1, 3, 5` 3개 정수 행 반환

R6-078. [높음] POWER(0, -1) (0의 음수 거듭제곱 = 1/0) 실행 시 Division by Zero 에러 대신 IEEE 754 '+Inf' 반환
  - 재현:
      SELECT POWER(0, -1) AS v;
  - 실제: `+Inf` 반환
  - 기대: 0의 음수 거듭제곱은 0으로 나누기이므로 도메인/산술 에러 발생

R6-079. [중간] ACOS(2) 및 ASIN(2) 정의역([-1, 1]) 초과 입력 시 도메인 에러(Msg 3623) 대신 조용히 NULL 반환
  - 재현:
      SELECT ACOS(2) AS a, ASIN(2) AS b;
  - 실제: 에러 없이 `|` (둘 다 NULL) 반환 (대조: `SQRT(-1)`, `LOG(0)`은 `Msg 3623` 에러 발생)
  - 기대: `Msg 3623: An invalid floating point operation occurred` 도메인 에러 발생

R6-080. [중간] COT(0) (코탄젠트 0 = 1/0) 호출 시 도메인 에러(Msg 3623) 대신 조용히 NULL 반환
  - 재현:
      SELECT COT(0) AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: 0으로 나누기 또는 부동소수점 도메인 에러(`Msg 3623`) 발생

R6-081. [중간] 20자리 정수 문자열을 BIGINT로 CAST 시 오버플로 에러 없이 MinInt64로 조용히 클램핑
  - 재현:
      SELECT CAST('99999999999999999999' AS BIGINT) AS v;
  - 실제: 에러 없이 `-9223372036854775808` (또는 MaxInt64) 반환
  - 기대: BIGINT 범위를 초과하므로 산술 오버플로 에러 발생

R6-082. [중간] 유효하지 않은 JSON 문자열을 JSON 타입으로 CAST 시 파싱 에러 없이 그대로 반환
  - 재현:
      SELECT CAST('{invalid json' AS JSON) AS v;
  - 실제: 에러 없이 `{invalid json` 반환
  - 기대: `invalid input syntax for type json` 에러 발생

R6-083. [중간] CAST('hello' AS VARCHAR(0)) 실행 시 길이 0 제약이 무시되고 원본 문자열 'hello' 전체 반환
  - 재현:
      SELECT CAST('hello' AS VARCHAR(0)) AS v;
  - 실제: `hello` (5글자 전체 반환)
  - 기대: 길이가 0인 VARCHAR는 구문 에러로 거부하거나 빈 문자열 `''`로 절삭해야 함

R6-084. [중간] CAST('2026-11-19 14:30:00 +09:00' AS DATETIMEOFFSET) 실행 시 타임존 오프셋(+09:00)이 조용히 증발
  - 재현:
      SELECT CAST('2026-11-19 14:30:00 +09:00' AS DATETIMEOFFSET) AS v;
  - 실제: `2026-11-19 14:30:00` (타임존 오프셋이 잘려 나감)
  - 기대: `2026-11-19 14:30:00.0000000 +09:00` 오프셋 보존

R6-085. [중간] CAST('AB' AS VARBINARY(10)) 실행 시 ASCII 바이트(\x4142) 대신 16진수 리터럴(\xab)로 오해석
  - 재현:
      SELECT CAST('AB' AS VARBINARY(10)) AS v;
  - 실제: `\xab` (1바이트) 반환
  - 기대: 문자 'A'(0x41)와 'B'(0x42)의 바이트 배열인 `\x4142` (2바이트) 반환

R6-086. [중간] PG 이스케이프 문자열 리터럴 E'a\nb'가 개행 문자로 디코딩되지 않고 원문 그대로 평가
  - 재현:
      SELECT LENGTH(E'a\nb') AS len;
  - 실제: 개행 문자(1바이트)로 변환되지 않아 `3`이 아닌 다른 길이 또는 원문 반환
  - 기대: `3` 반환

R6-087. [중간] CBRT(세제곱근) 수학 내장 함수 호출 시 미구현 에러 발생
  - 재현:
      SELECT CBRT(27) AS v;
  - 실제: `ERROR: unknown scalar function or invalid expression: CBRT(27)`
  - 기대: `3` 반환

R6-088. [중간] T-SQL 표준 STR(float_expression, length, decimal) 문자열 변환 내장 함수 미구현 에러
  - 재현:
      SELECT STR(123.45, 6, 1) AS v;
  - 실제: `ERROR: unknown scalar function or invalid expression: STR(123.45, 6, 1)`
  - 기대: `' 123.5'` (또는 초과 시 `'**'`) 반환

R6-089. [중간] SUBSTRING('abcd', -1, 4) 음수 시작 위치 지정 시 T-SQL 1-기반 오프셋 보정 대신 인덱스 0부터 잘라냄
  - 재현:
      SELECT SUBSTRING('abcd', -1, 4) AS v;
  - 실제: `abcd` (4글자 전체 반환)
  - 기대: T-SQL 표준 공식(`start + length - 1 = -1 + 4 - 1 = 2`)에 따라 첫 2글자 `'ab'`만 반환해야 함

R6-090. [중간] UPPER('ß') 독일어 에스체트(Sharp S) 대문자 변환 시 표준 'SS'로 변환되지 않고 소문자 'ß' 유지
  - 재현:
      SELECT UPPER('ß') AS v;
  - 실제: `ß` 그대로 반환
  - 기대: 유니코드 표준 대문자 매핑인 `SS` 반환

R6-091. [중간] DATEPART(nanosecond, ...) 호출 시 나노초 값을 추출하지 못하고 NULL 반환
  - 재현:
      SELECT DATEPART(nanosecond, '2026-01-01 00:00:00.123456789') AS v;
  - 실제: `NULL` (빈 문자열) 반환
  - 기대: `123456789` 반환

R6-092. [중간] IIF(NULL, 'T', 'F') 조건식에 리터럴 NULL 직접 전달 시 구문 에러 대신 'F' 반환
  - 재현:
      SELECT IIF(NULL, 'T', 'F') AS v;
  - 실제: 구문 에러 없이 `F` 반환 (T-SQL에서 `IIF(NULL, ...)`은 불리언 표현식이 아니므로 구문 에러 대상)
  - 기대: 조건식에 유효한 불리언 비교식을 요구하는 구문 에러 발생

R6-093. [중간] CHOOSE(2.9, 'A', 'B', 'C') 실수 인덱스 전달 시 정수 절삭(2 -> 'B') 대신 반올림(3 -> 'C') 오동작
  - 재현:
      SELECT CHOOSE(2.9, 'A', 'B', 'C') AS v;
  - 실제: `C` (3번째 항목) 반환
  - 기대: T-SQL 표준에 따라 실수 인덱스는 정수로 절삭(Truncation)되어 2번째 항목인 `B`를 반환해야 함

R6-094. [중간] COALESCE(), GREATEST(), LEAST()를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT COALESCE() AS a, GREATEST() AS b, LEAST() AS c;
  - 실제: 에러 없이 `||` (3개 모두 NULL) 반환
  - 기대: 최소 1~2개 이상의 인자를 요구하는 구문 에러 발생

R6-095. [중간] NULLIF(1)를 인자 1개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT NULLIF(1) AS v;
  - 실제: 에러 없이 `NULL` 반환
  - 기대: `NULLIF`는 정확히 2개의 인자를 요구하므로 구문 에러 발생

R6-096. [중간] 단항 내장 함수(ASCII(), UPPER(), SQRT())를 인자 0개로 호출 시 구문 에러 없이 NULL 반환
  - 재현:
      SELECT ASCII() AS a, UPPER() AS b, SQRT() AS c;
  - 실제: 에러 없이 `||` (모두 NULL) 반환
  - 기대: 필수 인자 누락(Arity mismatch) 구문 에러 발생

R6-097. [중간] 단항 내장 함수(LOWER('ABC', 'DEF'), ABS(-5, 10))에 초과 인자 전달 시 에러 없이 초과 인자 무시
  - 재현:
      SELECT LOWER('ABC', 'DEF') AS l, ABS(-5, 10) AS a;
  - 실제: 에러 없이 `abc | 5` 반환 (두 번째 초과 인자를 조용히 무시)
  - 기대: `function requires 1 argument(s)` 인자 개수 불일치 에러 발생


================================================================================
 7. 시스템 카탈로그 (3건: R6-098 ~ R6-100)
================================================================================

R6-098. [치명적] 가상 시스템 카탈로그(information_schema.check_constraints 등)에 COUNT(*) 실행 시 집계 대신 다중 빈 행 출력
  - 재현:
      SELECT COUNT(*) FROM information_schema.check_constraints;
  - 실제: 단일 숫자 행 대신 빈 줄 또는 `1\n1\n...` 다중 행이 반환됨
  - 기대: 단일 집계 행(예: `0` 또는 제약 개수) 반환

R6-099. [높음] 가상 시스템 카탈로그(information_schema.tables) 조회 시 SELECT 리스트의 상수 리터럴 컬럼이 NULL로 증발
  - 재현:
      SELECT 'tag' AS t, table_name FROM information_schema.tables LIMIT 1;
  - 실제: `|<table>` (`'tag'` 상수 컬럼이 빈 값/NULL로 출력됨)
  - 기대: `tag|<table>` 반환

R6-100. [중간] EXPLAIN (FORMAT JSON) SELECT 1 실행 시 엉뚱한 'table "" not found' 에러 발생
  - 재현:
      EXPLAIN (FORMAT JSON) SELECT 1;
  - 실제: `ERROR: table "" not found`
  - 기대: JSON 형식 실행 계획 반환 또는 명확한 옵션 미지원 안내

================================================================================
