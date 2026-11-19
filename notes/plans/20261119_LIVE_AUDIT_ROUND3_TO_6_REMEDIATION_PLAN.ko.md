# LionSQL 라이브 엔진 감사 Round 3 ~ Round 6 (300건) 통합 조치 마스터 플랜 (`notes/plans/20261119_LIVE_AUDIT_ROUND3_TO_6_REMEDIATION_PLAN.ko.md`)

- **작성일자**: 2026-11-19
- **대상 버전**: LionSQL v0.94.0 / 1.0-GA
- **원본 감사 보고서**:
  - [`notes/reports/20261119_LIVE_AUDIT_REPORT_ROUND3.ko.md`](../reports/20261119_LIVE_AUDIT_REPORT_ROUND3.ko.md) (50건: Critical 11 / High 11 / Medium 24 / Low 4)
  - [`notes/reports/20261119_LIVE_AUDIT_REPORT_ROUND4.ko.md`](../reports/20261119_LIVE_AUDIT_REPORT_ROUND4.ko.md) (50건: Critical 15 / High 18 / Medium 16 / Low 1)
  - [`notes/reports/20261119_LIVE_AUDIT_REPORT_ROUND5.ko.md`](../reports/20261119_LIVE_AUDIT_REPORT_ROUND5.ko.md) (100건: Critical 16 / High 33 / Medium 42 / Low 9)
  - [`notes/reports/20261119_LIVE_AUDIT_REPORT_ROUND6.ko.md`](../reports/20261119_LIVE_AUDIT_REPORT_ROUND6.ko.md) (100건: Critical 12 / High 45 / Medium 43 / Low 0)
- **적용 규칙**: `[RULE:ExplicitError]`, `[RULE:ModularAbstraction]`, `[RULE:PipelineVerification]`, `[RULE:StyleIntegrity]`, `[RULE:ContinuousBacklog]`, `[RULE:PlanPersistence]`

---

## 1. 전체 현황 요약 (300건)

| 심각도 | Round 3 | Round 4 | Round 5 | Round 6 | 합계 | 목표 마일스톤 |
| :--- | :---: | :---: | :---: | :---: | :---: | :--- |
| **Critical (치명적)** | 11 | 15 | 16 | 12 | **54건** | `v0.94.0` (P1 최우선 전멸) |
| **High (높음)** | 11 | 18 | 33 | 45 | **107건** | `v0.94.0` / `1.0-GA` (P1/P2) |
| **Medium (중간)** | 24 | 16 | 42 | 43 | **125건** | `1.0-GA` (P2) |
| **Low (낮음)** | 4 | 1 | 9 | 0 | **14건** | `1.0-GA` / `Post-1.0` (P3) |
| **합계** | **50** | **50** | **100** | **100** | **300건** | — |

---

## 2. 8대 구조적 원인 클러스터 (Root-Cause Clusters) 및 스프린트 태스크 매핑

300건의 결함은 아래 **8개의 핵심 엔진 클러스터**로 수렴합니다. 각 클러스터는 단일 모듈/파이프라인의 구조적 결함을 해결함으로써 연관된 수십 건의 증상을 동시에 해소합니다.

### Cluster 1: `AUDIT-R36-C1-PANIC-AND-VDBE-SUBQUERY-DML` (P1 / Critical)
- **핵심 원인**:
  1. `WHERE id = 99 AND 1/0 = 1` 실행 시 VDBE 인덱스 추출기(`findMatchingIndex` / `extractSimpleEq`)에서 우변 `proc.Expr`를 `*proc.LiteralExpr`로 비방어적 타입 단언하여 패닉 발생 (`R5-001`).
  2. `UPDATE` / `DELETE` / `INSERT` 문 내부(`SET` 또는 `WHERE` 또는 `VALUES`)에 서브쿼리(`(SELECT ...)`, `IN (SELECT ...)`, `EXISTS (SELECT ...)`)가 포함될 때 VDBE Fast-Path(`execVBDEDML`)가 서브쿼리 실행 콜백 없이 평가하여 `NULL` 오염 또는 `0건` 처리 발생.
- **포함 항목**:
  - Critical: `R5-001`, `R3-05`, `R3-06`, `R4-30`, `R4-33`, `R4-36`, `R4-37`, `R5-008`, `R5-010`
  - High: `R3-08`, `R4-02`
- **조치 설계**:
  - 모든 타입 단언을 comma-ok 패턴으로 방어하고, `execVBDEDML`에서 서브쿼리(`qc.HasSubquery` 또는 `SELECT` 키워드) 포함 시 `sqlexec_dml` 서브쿼리 해석기로 라우팅하거나 `VMFrame`에 `SubqueryExecutor`를 바인딩.
  - 스칼라 서브쿼리가 2행 이상 반환 시 `Msg 512` (`Subquery returned more than 1 value`) 에러, `IN (SELECT a, b ...)` 다중 컬럼 서브쿼리 시 `Msg 116` 에러 반환.

### Cluster 2: `AUDIT-R36-C2-PROJECTION-ALIAS-STAR-WINDOW-UDF` (P1 / Critical)
- **핵심 원인**:
  1. `ProjectionIterator.projectTuple` (`pkg/execution/project.go:266`)에서 수식 AST(`f.parsed`)를 평가하기 전에 `tuple.GetOK(f.alias)`를 먼저 조회하여, `SELECT id + 100 AS id`처럼 별칭이 기존 컬럼명과 동일하면 수식이 무시되고 원본 컬럼값이 반환됨 (`R3-01`).
  2. `SELECT *, id + 1 AS id2` 또는 `SELECT t.*, ...`에서 `*`와 일반 표현식이 혼합된 경우 `*` 확장이 누락됨 (`R4-17`).
  3. 산술식(`v * 100 / SUM(v) OVER ()`) 또는 `CASE WHEN ROW_NUMBER() OVER (...) = 1` 내부에 중첩된 윈도우 함수 결과를 `WindowIterator`가 합성 컬럼으로 치환·주입하지 못함 (`R5-009`, `R6-010`).
  4. 테이블 `SELECT` 프로젝션에서 스칼라 UDF(`dbo.fn(col)`) 호출 시 Volcano 평가기에 UDF 콜백이 연결되지 않아 `NULL` 반환 (`R3-31`).
- **포함 항목**:
  - Critical: `R3-01`, `R4-17`, `R5-009`, `R6-010`, `R3-31`
  - High/Medium: `R6-067` (UDF `DEFAULT` 인자), `R4-19` (Inline TVF), `R6-068` (Multi-statement TVF)

### Cluster 3: `AUDIT-R36-C3-OUTER-JOIN-PREDICATES-AND-FILTERS` (P1 / Critical)
- **핵심 원인**:
  1. `LEFT / RIGHT / FULL OUTER JOIN`에서 비등가 조건(`ON a.id < b.id`), 다중 키(`ON k1 AND k2`), 좌측 테이블 필터(`AND a.a = 20`), 상수 거짓(`ON 1 = 0`)이 포함될 때 플래너가 `ON` 조건을 `WHERE` 필터로 잘못 푸시다운하거나 조인 연산자가 미매칭 외곽 행(Outer Unmatched Row)을 누락/카테시안 곱 처리함.
  2. `WHERE (a, b) IN ((10, 100), (30, 300))` 튜플 비교 및 `WHERE id = ANY(ARRAY[1, 3])` 배열 비교가 평가기에서 폴백 통과됨.
- **포함 항목**:
  - Critical: `R4-29`, `R4-31`, `R4-32`, `R5-005`, `R5-006`, `R6-011`, `R4-01`, `R4-22`
  - Medium/Low: `R3-27` (`JOIN ... USING`), `R3-39` (`LATERAL` 헤더), `R3-40` (`NATURAL JOIN`), `R5-037` (`CROSS JOIN ... ON` 거부)

### Cluster 4: `AUDIT-R36-C4-DML-ATOMIC-KEY-UPDATE-AND-CONSTRAINTS` (P1 / Critical)
- **핵심 원인**:
  1. 다중 행 `UPDATE`에서 PK 또는 UNIQUE 컬럼을 갱신(`SET id = 13 - id`, `SET id = id + 1`, `CASE` 스왑)할 때 행 단위 즉시 B-Tree 삽입/검증으로 중간 상태 키 충돌 또는 기존 행 덮어쓰기 소실 발생 (`R3-02`, `R6-057`, `R6-058`).
  2. 컬럼 인라인 `UNIQUE` 제약(`code VARCHAR(10) UNIQUE`)이 `TableSchema.Indexes`에 유니크 인덱스로 자동 등록되지 않음 (`R3-03`).
  3. PK 컬럼 `SET id = NULL` 허용(`R5-002`), `INSERT` 시 생략된 무기본값 `NOT NULL` 컬럼 허용(`R5-003`), `UPDATE ... LIMIT N`의 `LIMIT` 절 무시(`R5-004`), `DELETE ... OUTPUT DELETED.*` 컬럼 매핑 오류(`R5-007`), `UPDATE ... FROM` 조인 테이블 컬럼 참조 실패(`R6-056`), `FOREIGN KEY ... ON UPDATE CASCADE` / `ON DELETE SET DEFAULT` 미동작(`R3-04`, `R4-34`), Computed 컬럼(`AS (a + b)`) 및 `SET col = DEFAULT` 미평가(`R3-07`, `R3-37`).
- **포함 항목**:
  - Critical: `R3-02`, `R3-03`, `R3-04`, `R5-002`, `R5-003`, `R5-004`, `R5-007`, `R6-056`, `R6-057`, `R6-058`
  - High: `R3-07`, `R3-10`, `R3-35`, `R3-37`, `R4-13`, `R4-34`, `R4-38`, `R5-043`, `R5-053`, `R6-059`, `R6-060`, `R6-061`, `R6-062`, `R6-063`, `R6-064`

### Cluster 5: `AUDIT-R36-C5-TRIGGER-TRANSIENT-TABLES-AND-PROCS` (P1 / Critical)
- **핵심 원인**:
  1. `AFTER UPDATE` / `AFTER DELETE` 트리거 내부에서 `INSERT INTO log SELECT ... FROM DELETED [JOIN INSERTED]` 실행 시 `queryExecutor`로 전달되는 `ClientSession`에 `f.transientTables`(`inserted`, `deleted`)가 `EphemeralTables`로 바인딩되지 않아 0건 조회됨 (`R3-30`, `R4-12`).
  2. `INSTEAD OF INSERT` 트리거 메타데이터 구분 및 원본 DML 바이패스 누락 (`R3-41`).
  3. 프로시저 내 `UPDATE/DELETE t FROM tbl t JOIN ...` 타깃 별칭 해석(`R4-26`, `R4-28`), `SELECT INTO #tmp`(`R4-27`), `OUTPUT` 매개변수 `NULL` 전달(`R5-015`), `@tv TABLE`의 `IDENTITY(1,1)` 채번(`R6-066`), `EXEC @rc = proc` 반환값 바인딩(`R6-070`), `THROW;` 원본 에러 메시지 보존(`R4-16`), `ERROR_STATE()` 보존(`R3-33`).
- **포함 항목**:
  - Critical: `R3-30`, `R3-41`, `R4-12`, `R4-26`, `R4-28`, `R5-015`, `R6-066`
  - High/Medium: `R3-33`, `R3-34`, `R4-15`, `R4-16`, `R4-24`, `R4-27`, `R5-065~069`, `R6-069~074`

### Cluster 6: `AUDIT-R36-C6-DDL-SCHEMA-AND-SEQUENCE-INTEGRITY` (P1 / Critical & High)
- **핵심 원인**:
  1. `FixedRowSize()`에 `CHAR(n)` 고정 길이 바이트 누락 (`R6-043` / `LIMITS-INLINE-ROW-CHAR-FOOTPRINT-1`).
  2. `ALTER COLUMN v TYPE INT` 시 변환 실패 문자열을 에러 없이 가비지 정수로 덮어씀 (`R4-18`), `ALTER TABLE ADD UNIQUE DEFAULT 5` 중복 백필 허용 (`R5-014`), `DROP TABLE IF EXISTS t1, t2` 다중 테이블 루프 누락 (`R6-027`), `CHECK`/`DEFAULT` 내 서브쿼리 미차단 (`R6-028`, `R6-029`).
  3. `NEXT VALUE FOR <seq>` 및 `nextval('<seq>')` 시퀀스 상태 전진 및 `MAXVALUE`/`MINVALUE`/`INCREMENT 0`/`ALTER SEQUENCE RESTART` 검증 (`R3-29`, `R5-013`, `R5-054`, `R5-059`, `R5-060`, `R6-051~055`).
  4. `CREATE/ALTER TABLE` 시 중복 컬럼명, 미존재 컬럼 참조(PK/UNIQUE/FK/INDEX), FK 컬럼 수 및 타입 불일치, `DROP CONSTRAINT` 시 컬럼 인라인 `CheckExpr`/FK 제거(`R3-43`, `R5-044`), `SET NOT NULL` / `ADD CONSTRAINT CHECK` 시 기존 데이터 전수 검증(`R4-14`, `R4-48`, `R6-030`).
- **포함 항목**:
  - Critical: `R3-29`, `R4-18`, `R5-013`, `R5-014`, `R6-027`, `R6-028`, `R6-029`, `R6-098`
  - High/Medium: `R3-09`, `R3-43`, `R3-50`, `R4-14`, `R4-44`, `R4-46`, `R4-48`, `R4-50`, `R5-044~064`, `R6-030~055`, `R6-099`, `R6-100`

### Cluster 7: `AUDIT-R36-C7-GROUPBY-WINDOW-SETOP-AND-CONCURRENCY` (P1 / Critical & High)
- **핵심 원인**:
  1. `GROUP BY`와 윈도우 함수(`RANK() OVER (ORDER BY SUM(v) DESC)`) 결합 시 집계 완료 후 윈도우 정렬 평가 파이프라인 연결 누락 (`R3-47`), `HAVING` 절 내 스칼라 서브쿼리 미평가 (`R4-04`).
  2. `SELECT ... FOR UPDATE` 배타적 행 락(`activeWriters`) 선점 연동(`R6-001`), `SHOW idle_in_transaction_session_timeout` 세션 로컬 값 반환(`R6-002`), 중첩 `SAVEPOINT` 덮어쓰기 및 `RELEASE SAVEPOINT` 제거(`R6-006`, `R6-007`).
  3. `EXCEPT ALL` 멀티셋 차감(`R3-26`), 괄호 중첩 집합 연산(`R6-012`), `GROUP BY` 미포함 집계 `ORDER BY SUM(v)`(`R3-11`), 윈도우 `PARTITION BY` 수식(`R3-48`), `GROUPING SETS`(`R4-03`), `TOP 0` / 음수 `TOP`/`LIMIT`/`OFFSET`/`FETCH`(`R3-13`, `R3-15`, `R6-013~016`), 미존재 컬럼/별칭 엄격 검증(`R5-017~031`).
- **포함 항목**:
  - Critical: `R3-47`, `R4-04`, `R6-001`, `R6-002`
  - High/Medium: `R3-11`, `R3-13`, `R3-15~18`, `R3-26`, `R3-28`, `R3-48`, `R4-03`, `R4-07`, `R4-08`, `R4-23`, `R4-35`, `R4-47`, `R4-49`, `R5-017~042`, `R6-003~009`, `R6-012~026`

### Cluster 8: `AUDIT-R36-C8-TYPES-CAST-MATH-DATE-STRING-JSON` (P1 / Critical & High)
- **핵심 원인**:
  1. `TRY_CAST` 정수 오버플로 시 에러를 삼키지 않고 `MinInt64`를 반환(`R5-011`), 20자리 `DECIMAL(20, 0)` 리터럴 INSERT 시 `int64` 파싱 절삭(`R5-012`), `UNION` 상이한 타입 결합 시 `'abc'`를 `0`으로 강제 변환(`R5-016`).
  2. `SUM(BIGINT)`(`R3-49`), `ABS(MinInt64)`(`R3-21`), `-MinInt64`(`R6-075`), `SQUARE`(`R5-071`), `POWER`/`EXP` `+Inf`/`NaN` 누출(`R3-38`, `R5-072`, `R5-073`, `R6-078`), `ACOS`/`ASIN`/`COT` 도메인 에러(`R6-079`, `R6-080`).
  3. `IIF` 단락 평가(`R3-12`), `REPLACE` 빈 문자열(`R3-23`), `STRING_AGG` NULL 구분자(`R3-45`), `TO_CHAR`/`DATEFROMPARTS`/`DATETIMEFROMPARTS`/`TIMEFROMPARTS` 범위 검증 및 날짜 산술(`R3-22`, `R4-05`, `R4-10`, `R5-080~097`, `R6-076~097`).
- **포함 항목**:
  - Critical: `R5-011`, `R5-012`, `R5-016`
  - High/Medium/Low: `R3-12`, `R3-14`, `R3-19`, `R3-21~25`, `R3-32`, `R3-38`, `R3-44`, `R3-45`, `R3-49`, `R4-05`, `R4-06`, `R4-09~11`, `R4-25`, `R5-071~100`, `R6-075~097`
