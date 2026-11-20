# LionSQL 라이브 엔진 감사 Round 1 ~ 14 전수 통합 기술 백서 (`notes/reports/20261120_LIVE_AUDIT_ROUNDS_1_TO_14.ko.md`)

> - **감사 대상**: LionSQL Database Server v0.93.0 ~ v0.94.0 (In-Memory OCC / WiredTiger B+Tree, Port 5433 / 1433)
> - **감사 기간**: 2026-11-19 ~ 2026-11-20
> - **감사 방법론**: psql 16.15 및 Go 다중 세션 적대적 프로브 스크립트 기반 라이브 differential test
> - **최종 상태**: Round 1 ~ 14 전건 결함 해결 완료 (100% 회귀 방지 테스트 통과)
> - **보존 정책**: 단일 통합 기술 백서(Single Source of Truth, SSOT)로 완전 영구화

---

## 1. 감사 개요 및 구조적 총괄 통계

LionSQL v0.94.0 및 1.0-GA 출시에 앞서 엔진 신뢰성을 완벽히 검증하기 위해 총 14개 라운드에 걸쳐 집중 라이브 감사를 수행하였습니다.
본 백서는 개별 쿼리 증상 단순 나열을 배제하고, **결함의 근본 원인이 된 핵심 소스 코드 모듈**, **실제 재현 쿼리**, **기대 동작 vs 실제 동작**, **코드 레벨 조치 내역**, 그리고 **회귀 방지 검증 테스트**를 총망라한 전수 기술 아카이브입니다.

| 감사 라운드 | 중점 감사 영역 | 결함 건수 | 핵심 원인 모듈 | 전담 회귀 검증 스위트 |
| :---: | :--- | :---: | :--- | :--- |
| **Round 1** | T-SQL 프로시저 수명주기 및 가상 객체 | 10건 | `pkg/proc/runtime/vm.go`, `pkg/engine/session.go` | `proc_view_query_test.go`, `proc_temp_table_lifecycle_test.go`, `output_into_table_var_test.go` |
| **Round 2** | 수치 연산, 날짜 산술 및 윈도우 함수 | 12건 | `pkg/proc/eval.go`, `pkg/execution/window.go` | `audit_round1_to_6_supplemental_test.go`, `easy_defects_test.go` |
| **Round 3~6** | 8대 코어 엔진 클러스터 (DML/JOIN/PK/단락평가) | 300건 | `pkg/execution/`, `pkg/engine/sqlexec_dml.go`, `pkg/proc/` | `spec_driven_batch1~3_test.go`, `audit_round1_to_6_supplemental_test.go` |
| **Round 7~8** | 내장 함수 Arity 검증, 날짜 생성자, 윈도우 프레임 | 200건 | `pkg/proc/builtins_sys.go`, `pkg/proc/eval.go`, `pkg/execution/` | `audit_round7_round8_test.go`, `easy_defects_test.go` |
| **Round 9** | 보안, RBAC/DCL 권한 체계, 트랜잭션 격리 | 17건 | `pkg/engine/sqlexec_auth.go`, `pkg/engine/tx_manager.go` | `audit_round9_test.go` (17개 전담 서브테스트) |
| **Round 10** | 프로시저 VM, 커서 상태 머신, CBO 검증 | 10건 | `pkg/engine/session.go`, `pkg/engine/sqlexec_select.go` | `audit_round10_test.go` (10개 전담 서브테스트) |
| **Round 11** | 시퀀스 엔진, Option B 인덱스, 플랜 캐시 무효화 | 7건 | `pkg/catalog/sequence.go`, `pkg/engine/sqlexec_sequence.go`, `pkg/storage/wiredtiger/` | `audit_round11_test.go` (7개 전담 서브테스트) |
| **Round 12** | JSON strict 모드, XML 메소드 차단, CONVERT 스타일 | 5건 | `pkg/storage/json.go`, `pkg/proc/eval.go`, `pkg/engine/sqlexec_select.go` | `audit_round12_test.go` (5개 전담 서브테스트) |
| **Round 13** | 집계 지연역직렬화, DML 서브쿼리 스코프, MERGE 카디널리티, 윈도우 프레임 | 5건 | `pkg/execution/aggregate.go`, `pkg/engine/sqlexec_dml.go`, `pkg/engine/sqlexec_merge.go`, `pkg/execution/window.go` | `audit_round13_test.go`, `aggregate_round13_test.go` |
| **Round 14** | TDS 대용량 바이너리/스트링 PLP, 스칼라 WHERE 1=0 메타데이터, 인덱스 동기화 | 3건 | `pkg/wire/tds/server.go`, `pkg/proc/compiler.go`, `pkg/engine/sqlexec_ddl.go` | `audit_round14_test.go` |

---

## 2. Round 1: T-SQL 프로시저 수명주기 및 가상 객체 확장 (10건)

## 2. 신규 발굴 핵심 결함 목록 (Total 10건)

| 번호 | 결함 요약 | 심각도 | 영향 범위 | 발생 원인 및 위치 |
| :--- | :--- | :--- | :--- | :--- |
| **결함 1** | `INSERT ... OUTPUT ... INTO @table_variable` 데이터 사일런트 유실 | **치명적 (Critical)** | DML 생성 키/데이터 캡처 로직 전반 | `pkg/engine/sqlexec_dml.go`, `pkg/proc/runtime/vm.go` (테이블 변수 라우팅 누락) |
| **결함 2** | 트리거 구동 시 `SCOPE_IDENTITY()` 스코프 격리 실패 | **치명적 (Critical)** | AFTER INSERT 트리거 연쇄 채번 환경 | `pkg/proc/runtime/vm.go` (스택 프레임별 ID 스코프 관리 부재) |
| **결함 3** | Stored Procedure 내부 CTE(`WITH`) 실행 시 `table not found` 오류 | **높음 (High)** | 프로시저 내 계층 순회 및 CTE 질의 | `pkg/proc/runtime/vm.go`, `pkg/engine/sqlexec_select.go` (VDBE CTE 바인딩 누락) |
| **결함 4** | Stored Procedure 내부 `VIEW` 조회 시 `table not found` 오류 | **높음 (High)** | 프로시저 내 가상 뷰 참조 로직 | `pkg/proc/runtime/vm.go` (VDBE 서브쿼리 뷰 확장 누락) |
| **결함 5** | Stored Procedure 내부 `MERGE INTO` 구문 파싱 거부 | **높음 (High)** | 프로시저 내 단일 문장 UPSERT 로직 | `pkg/proc/parser_procs.go` (허용 문장 시작 토큰 누락) |
| **결함 6** | 프로시저 로컬 `#temp` 테이블 수명주기 해제 누락 (2회차 충돌) | **높음 (High)** | 프로시저 내 로컬 임시 테이블 사용 패턴 | `pkg/engine/session.go`, `pkg/proc/runtime/vm.go` (프로시저 종료 스코프 정리 미흡) |
| **결함 7** | `IDENTITY(seed, increment)` 초기 시드값 및 증가분 무시 버그 | **높음 (High)** | 커스텀 채번 시드/스텝 지정 테이블 | `pkg/proc/parser_ddl.go`, `wt_engine.go` (시퀀스 생성기 하드코딩 1, 1) |
| **결함 8** | AFTER 트리거 내부에서 `@@ROWCOUNT` 초기값 누락 (0으로 유실) | **보통 (Medium)** | 트리거 내부 영향 행 수 검증 로직 | `pkg/proc/runtime/trigger.go` (트리거 프레임 affectedRows 미전달) |
| **결함 9** | Stored Procedure 내부 직접 DDL(`ALTER TABLE` 등) 토큰 거부 | **보통 (Medium)** | 마이그레이션/관리 목적 프로시저 | `pkg/proc/parser_procs.go` (정적 DDL 키워드 차단) |
| **결함 10** | 프로시저 매개변수와 로컬 변수의 동일 이름 재선언 허용 (섀도잉) | **보통 (Medium)** | 프로시저 컴파일 유효성 검증 | `pkg/proc/compiler.go` (심볼 테이블 중복 검증 누락) |

---

## 3. 세부 결함 분석 및 재현 시나리오

### [결함 1] `INSERT ... OUTPUT ... INTO @table_variable` 데이터 사일런트 유실
- **심각도**: 치명적 (Critical)
- **영향 범위**: DML 실행과 동시에 생성된 키/데이터를 테이블 변수로 캡처하는 모든 비즈니스 로직
- **재현 시나리오**:
```sql
CREATE TABLE t_output (
    id INT IDENTITY(1, 1) PRIMARY KEY,
    name VARCHAR(20),
    status VARCHAR(10)
);

CREATE PROCEDURE sp_TestInsertOutput AS
BEGIN
    DECLARE @inserted_ids TABLE (
        new_id INT,
        inserted_name VARCHAR(20)
    );

    INSERT INTO t_output (name, status)
    OUTPUT INSERTED.id, INSERTED.name INTO @inserted_ids
    VALUES ('Item1', 'ACTIVE'), ('Item2', 'PENDING');

    SELECT * FROM @inserted_ids;
END;

EXEC sp_TestInsertOutput;
```
- **관찰된 결과**: 타깃 테이블(`t_output`)에는 행이 정상 삽입되나, `@inserted_ids` 테이블 변수는 0건으로 비어 있음 (에러 없이 사일런트 실패).
- **근본 원인**: `OUTPUT ... INTO` 파싱 시 `INTO` 뒤의 식별자를 프로젝션 식 일부로 오인하거나, VDBE `OpInsert` 후 `INSERTED` 튜플을 세션 테이블 변수 물리 버퍼(`__tv_<session>_<var>`)로 파이프라인 전달하는 라우팅 누락.

---

### [결함 2] 트리거 구동 시 `SCOPE_IDENTITY()` 스코프 격리 실패
- **심각도**: 치명적 (Critical)
- **영향 범위**: AFTER INSERT 트리거가 연쇄 INSERT를 수행하는 환경에서 `SCOPE_IDENTITY()` 호출
- **재현 시나리오**:
```sql
CREATE TABLE t_scope_trig (id INT IDENTITY(1, 1) PRIMARY KEY, val VARCHAR(20));
CREATE TABLE t_audit_log (audit_id INT IDENTITY(100, 1) PRIMARY KEY, msg VARCHAR(50));

CREATE TRIGGER trg_after_insert_scope
ON t_scope_trig AFTER INSERT AS
BEGIN
    INSERT INTO t_audit_log (msg) VALUES ('inserted');
END;

INSERT INTO t_scope_trig (val) VALUES ('test1');
SELECT @@IDENTITY AS at_at_identity, SCOPE_IDENTITY() AS scope_identity;
```
- **관찰된 결과**: `SCOPE_IDENTITY()`가 상위 쿼리 스코프의 ID를 보존하지 못하고 세션 전역 `lastIdentity`를 공유하여 감사 로그 ID로 왜곡됨.
- **근본 원인**: 스코프별 ID 스택 프레임이 부재하여 트리거 내부 연쇄 INSERT가 상위 스코프 ID를 덮어씀.

---

### [결함 3] Stored Procedure 내부 CTE(`WITH`) 실행 시 `table not found` 오류
- **심각도**: 높음 (High)
- **영향 범위**: 프로시저 내부에서 계층 순회(재귀 CTE) 및 WITH 절 전처리 쿼리 사용 시
- **재현 시나리오**:
```sql
CREATE PROCEDURE sp_OrgAnalysis @root_id INT AS
BEGIN
    WITH OrgCTE AS (
        SELECT id, name, manager_id, salary FROM emp WHERE id = @root_id
        UNION ALL
        SELECT e.id, e.name, e.manager_id, e.salary
        FROM emp e INNER JOIN OrgCTE o ON e.manager_id = o.id
    )
    SELECT * FROM OrgCTE;
END;

EXEC sp_OrgAnalysis @root_id = 1;
```
- **관찰된 결과**: `ERROR: table "OrgCTE" not found: table not found: OrgCTE` (동일 쿼리를 프로시저 밖에서 실행하면 정상 작동).
- **근본 원인**: 최상위 쿼리 엔진은 `WITH`를 사전에 감지하여 `EphemeralTable`로 실체화하나, VDBE 내부 루프에서는 이를 거치지 않고 스토리지 카탈로그를 직접 조회함.

---

### [결함 4] Stored Procedure 내부 `VIEW` 조회 시 `table not found` 오류
- **심각도**: 높음 (High)
- **영향 범위**: 프로시저 내부에서 가상 뷰(VIEW)를 SELECT하는 모든 로직
- **재현 시나리오**:
```sql
CREATE VIEW v_test_base AS SELECT id, val, val * 2 AS double_val FROM t_view_base;

CREATE PROCEDURE sp_TestViewInsideProc AS
BEGIN
    SELECT * FROM v_test_base WHERE double_val >= 20;
END;

EXEC sp_TestViewInsideProc;
```
- **관찰된 결과**: `ERROR: table "v_test_base" not found: table not found: v_test_base`
- **근본 원인**: VDBE 내부 질의 디스패처가 뷰 카탈로그 확장(View Expansion)을 거치지 않고 오직 물리 테이블만 조회함.

---

### [결함 5] Stored Procedure 내부 `MERGE INTO` 구문 파싱 거부
- **심각도**: 높음 (High)
- **영향 범위**: 프로시저 내에서 원자적 UPSERT 수행 시
- **관찰된 결과**: `ERROR: parse procedure error: unexpected token in statement: MERGE at 7:5`
- **근본 원인**: 프로시저 파서의 허용 시작 토큰 목록에 `MERGE` 키워드가 누락됨.

---

### [결함 6] 프로시저 로컬 임시 테이블(`#temp`) 수명주기 해제 누락 및 2회차 충돌
- **심각도**: 높음 (High)
- **영향 범위**: 프로시저 내부에서 `CREATE TABLE #local_temp`를 생성하는 패턴
- **재현 시나리오**:
```sql
CREATE PROCEDURE sp_TestLocalTempTableMultiCall AS
BEGIN
    CREATE TABLE #multi_temp (id INT PRIMARY KEY);
    INSERT INTO #multi_temp VALUES (1);
    SELECT * FROM #multi_temp;
END;

EXEC sp_TestLocalTempTableMultiCall; -- 1회차 성공
EXEC sp_TestLocalTempTableMultiCall; -- 2회차 실패!
```
- **관찰된 결과**: `ERROR: table already exists: __temp_pg_xxxx_multi_temp`
- **근본 원인**: 프로시저 종료 시 로컬 임시 테이블 파기 훅이 누락되어 세션 맵에 잔존.

---

### [결함 7] `IDENTITY(seed, increment)` 초기 시드값 및 증가분 무시 버그
- **심각도**: 높음 (High)
- **영향 범위**: 초기 채번 시드나 스텝 증가분(예: 100, 10)을 지정한 모든 테이블
- **재현 시나리오**:
```sql
CREATE TABLE t_seed_check (id INT IDENTITY(100, 10) PRIMARY KEY, name VARCHAR(20));
INSERT INTO t_seed_check (name) VALUES ('a'), ('b');
SELECT * FROM t_seed_check;
-- 결과: 1, 2 반환 (기대값: 100, 110)
```
- **근본 원인**: 스토리지 카운터 초기화 시 seed/increment 파라미터가 전달되지 않고 `1, 1`로 하드코딩됨.

---

### [결함 8] AFTER 트리거 내부에서 `@@ROWCOUNT` 초기값 누락 (0으로 유실)
- **심각도**: 보통 (Medium)
- **영향 범위**: 트리거 내부에서 변경 행 수를 검사하는 로직
- **관찰된 결과**: UPDATE 2건 후 진입한 트리거 내에서 `@@ROWCOUNT`가 `0`으로 평가됨.
- **근본 원인**: 트리거 진입 프레임 생성 시 직전 DML의 `affectedRows`를 넘기지 않고 0으로 리셋.

---

### [결함 9] Stored Procedure 내부 직접 DDL(`ALTER TABLE` 등) 토큰 거부
- **심각도**: 보통 (Medium)
- **관찰된 결과**: `ERROR: parse procedure error: unexpected token in statement: ALTER at 4:5`
- **근본 원인**: 프로시저 파서가 정적 DDL 키워드를 불필요하게 차단함.

---

### [결함 10] 프로시저 매개변수와 로컬 변수의 동일 이름 재선언 허용 (섀도잉 결함)
- **심각도**: 보통 (Medium)
- **관찰된 결과**: `@id` 매개변수가 있는데 본문에서 `DECLARE @id INT`를 해도 컴파일 에러(Msg 134) 없이 파라미터를 덮어씀.
- **근본 원인**: 프로시저 컴파일러가 심볼 테이블의 파라미터와 로컬 변수 간 중복 검증을 수행하지 않음.

---

## 4. 정상 작동이 확인된 실무 고난도 복합 기능 (14선)

1. 기본 DML 및 파라미터 전달 프로시저 (`sp_TransferMoney`)
2. 조건 분기(`IF`) 및 예외 제어(`THROW`)와 트랜잭션 자동 롤백
3. 예외 포착 및 회복 블록 (`BEGIN TRY ... BEGIN CATCH`)
4. 출력 매개변수 (`OUTPUT` Parameter)
5. 커서 순회 제어 (`DECLARE CURSOR ... FETCH NEXT ... WHILE @@FETCH_STATUS = 0`)
6. 인메모리 테이블 변수 (`DECLARE @tmp TABLE`)
7. T-SQL + JSON 함수 융합 (`JSON_VALUE`)
8. 다단계 중첩 프로시저 호출 및 `RETURN` 상태 코드 바인딩
9. 상관 서브쿼리 및 `CROSS APPLY` / `OUTER APPLY` 융합
10. T-SQL 고유 패턴: `SELECT @list = @list + tag + ','` 문자열 누적 연산
11. 외래키 위반 시 CATCH 블록 포착
12. 트랜잭셔널 `TRUNCATE` 및 롤백 정합성
13. `LIKE @pattern ESCAPE '!'` 와일드카드 이스케이프 질의
14. 날짜/시간 내장 함수 (`DATEPART`)


---

## 3. Round 2: 수치 정밀도, 날짜 산술 및 윈도우 함수 (12건)

## 1. 신규 발굴 결함 목록 및 조치 현황 (Total 12건)

| 번호 | 결함 요약 | 심각도 | 조치 상태 | 해결 커밋 및 검증 스위트 |
| :--- | :--- | :--- | :--- | :--- |
| **N-1** | 배치/프로시저 실행 시 첫 번째 결과 집합만 반환 | **높음 (High)** | 검증 완료 (PASS) | `d4ed8cbe` (`pkg/wire/multiset_test.go`) |
| **N-2** | 자기 상관 `EXISTS` / `NOT EXISTS` 0건 반환 | **높음 (High)** | 해결 완료 (PASS) | `ca61c777` (`pkg/engine/coordinator_test.go:494`) |
| **N-3** | 윈도우 집계 인자 내부 `CAST` 실패 (컬럼 미존재 오류) | **높음 (High)** | 검증 완료 (PASS) | 기해결 (`pkg/execution/window_test.go`) |
| **N-4** | `NTILE`, `PERCENT_RANK`, `CUME_DIST` 윈도우 함수 NULL 반환 | **높음 (High)** | 해결 완료 (PASS) | `80bff3e1` (`pkg/execution/window_ntile_test.go`) |
| **N-5** | 소수 리터럴 연산이 `float64` 부동소수점으로 계산되어 정밀도 오차 발생 | **높음 (High)** | 해결 완료 (PASS) | `b591a784` (`pkg/engine/decimal_literal_test.go`) |
| **N-6** | 문자열 동등 비교와 GROUP BY 간 대소문자 규칙 불일치 (Collation 불일치) | **높음 (High)** | 해결 완료 (PASS) | `0fb9788f` (`pkg/engine/collation_groupby_test.go`) |
| **N-7** | `DATEADD` 월말 연산 시 말일 클램핑 대신 익월 오버플로 발생 | **보통 (Medium)** | 해결 완료 (PASS) | `f5258e71` (`pkg/engine/dateadd_clamp_test.go`) |
| **N-8** | INT 산술 오버플로 시 에러 없이 64비트로 사일런트 확장 | **보통 (Medium)** | 해결 완료 (PASS) | `ce5198b8` (`pkg/engine/int_overflow_test.go`) |
| **N-9** | `CAST(... AS INT)` 범위 초과 검증 누락 | **보통 (Medium)** | 해결 완료 (PASS) | `4bab6331` (`pkg/engine/cast_overflow_test.go`) |
| **N-10** | 수학 함수 정의역 밖 입력 시 NULL 반환 | **보통 (Medium)** | 해결 완료 (PASS) | `ff473892` (`pkg/engine/easy_defects_test.go`) |
| **N-11** | `@@IDENTITY`가 트리거 내부 생성 ID를 미반영 | **보통 (Medium)** | 해결 완료 (PASS) | `d4d33b54` (`pkg/engine/trigger_scope_identity_test.go`) |
| **N-12** | `LEFT` 함수 음수 길이 인자 시 빈 문자열 반환 | **낮음 (Low)** | 해결 완료 (PASS) | `ff473892` (`pkg/engine/easy_defects_test.go`) |

---

## 2. 권장 해결 순서 (Suggested Fix Order - 완료)

1. **N-3 (우선순위 1)**: `pkg/execution/window.go` 인자 검증 로직 결함 검증 완료.
2. **N-1 (우선순위 2)**: 다중 결과 집합 PGWire 스트리밍 회귀 테스트 구축 및 검증 완료.
3. **N-2 (우선순위 3)**: 자기 상관 `EXISTS`/`NOT EXISTS` 쿼리 오작동 해결 완료.
4. **N-5, N-6 (우선순위 4)**: 소수 리터럴 정밀도(`storage.Decimal`) 및 대소문자 Collation 일관성 조치 완료.
5. **N-4, N-7, N-8, N-9, N-10, N-11, N-12 (우선순위 5)**: 개별 내장 함수 및 연산 격리 조치 완료.

---

## 3. 세부 결함 조치 결과 및 검증 커밋 상세 (Total 12건)

### [N-1] 다중 결과 집합 스트리밍 검증
- **증상**: 다중 SELECT 배치 실행 시 첫 번째 결과 집합만 클라이언트로 전달된다는 의심 제기.
- **조치 및 검증**: PGWire Simple Query (`'Q'`) 프로토콜 핸들러를 검증한 결과, 연속된 `RowDescription ➔ DataRow ➔ CommandComplete` 시퀀스를 통해 다중 결과 집합을 정상적으로 순차 스트리밍하고 있음을 확인.
- **해결 커밋**: `d4ed8cbe` (`test(wire): add PGWire multiple result sets sequential streaming regression test`)
- **검증 스위트**: `pkg/wire/multiset_test.go` (`TestWire_MultipleResultSetsSequentialStreaming`)

### [N-2] 자기 상관 `EXISTS` / `NOT EXISTS` 0건 반환
- **증상**: 외부 테이블과 서브쿼리 내부 테이블이 동일할 때(`WHERE EXISTS (SELECT 1 FROM r4 x WHERE x.id = r4.id)`) 외부 별칭이 없으면 0건 반환.
- **조치 내용**: 플래너 상관 관계 분석 시 테이블 이름과 동일한 타깃 참조를 해석하도록 보완.
- **해결 커밋**: `ca61c777` (`fix(planner,execution): support self-correlated EXISTS and NOT EXISTS subqueries without table alias`)
- **검증 스위트**: `pkg/engine/coordinator_test.go:494` (`TestCoordinator_SelfCorrelatedExists`)

### [N-3] 윈도우 집계 인자 내부 `CAST` 실패 (컬럼 미존재 오류)
- **증상**: `SUM(CAST(id AS INT)) OVER ()` 실행 시 `column "INT" does not exist` 에러 발생.
- **조치 및 검증**: 윈도우 함수 인자 유효성 검사기에서 CAST 식 및 함수 호출 표현식을 일반 식별자와 분리하여 처리.
- **검증 스위트**: `pkg/execution/window_test.go` (`TestWindow_CastInAggregate`)

### [N-4] `NTILE`, `PERCENT_RANK`, `CUME_DIST` 윈도우 함수 NULL 반환
- **증상**: 순위 및 분포 윈도우 함수가 항상 NULL을 반환.
- **조치 내용**: `pkg/execution/window.go`에 `NTILE(k)`, `PERCENT_RANK()`, `CUME_DIST()`에 대한 전용 윈도우 계산 로직 구현.
- **해결 커밋**: `80bff3e1` (`fix(execution): implement NTILE, PERCENT_RANK, and CUME_DIST window functions`)
- **검증 스위트**: `pkg/execution/window_ntile_test.go`

### [N-5] 소수 리터럴 연산 정밀도 오차 (`float64` 열화)
- **증상**: `SELECT 1.1 + 2.2` 결과가 `3.3000000000000003`으로 반환.
- **조치 내용**: 지수부(`e`/`E`)가 없는 소수 리터럴을 `float64` 대신 정확한 `storage.Decimal`로 파싱 및 바인딩하고, 산술 연산기에서 고정소수점 정밀도를 보존하도록 수정.
- **해결 커밋**: `b591a784` (`fix(proc,engine): parse non-exponent decimal literals as DECIMAL and preserve arithmetic precision`)
- **검증 스위트**: `pkg/engine/decimal_literal_test.go`

### [N-6] 문자열 동등 비교와 GROUP BY 간 대소문자 규칙 불일치
- **증상**: `WHERE` 절은 기본적으로 대소문자를 구분하지 않는데 `GROUP BY`는 대소문자를 구분하여 그룹이 나뉘는 불일치 발생.
- **조치 내용**: `GROUP BY` 그룹화 키 추출 시 기본적으로 대소문자 무시(case-insensitive) 정규화를 적용하고, 명시적 Collation 지정 시 이를 준수하도록 수정.
- **해결 커밋**: `0fb9788f` (`fix(execution): enforce case-insensitive grouping by default and respect collation in GROUP BY`)
- **검증 스위트**: `pkg/engine/collation_groupby_test.go`

### [N-7] `DATEADD` 월말 연산 시 말일 클램핑 대신 익월 오버플로 발생
- **증상**: `DATEADD(month, 1, '2026-01-31')` 연산 시 `2026-02-28` 대신 `2026-03-03` 반환.
- **조치 내용**: Go 표준 `time.AddDate`의 날짜 오버플로를 감지하고, 대상 월의 마지막 일자로 클램핑하는 `addMonthsClamped` 함수 도입 (`pkg/proc/builtins_date.go`).
- **해결 커밋**: `f5258e71` (`fix(proc): clamp month end days in DATEADD for year, quarter, and month parts`)
- **검증 스위트**: `pkg/engine/dateadd_clamp_test.go`

### [N-8] INT 산술 오버플로 시 사일런트 64비트 확장
- **증상**: `2147483647 + 1` 수행 시 에러 없이 `2147483648`로 반환.
- **조치 내용**: 32비트 정수 리터럴에 `Precision: 4`를 바인딩하고, 식 평가기(`pkg/proc/eval.go`) 및 VDBE 디스패처(`pkg/proc/runtime/vm_dispatcher.go`)에서 32비트 연산 오버플로 시 Msg 8115 (`Arithmetic overflow error converting expression to data type int.`) 에러를 즉시 발생시키도록 차단.
- **해결 커밋**: `ce5198b8` (`fix(proc,engine): enforce Msg 8115 arithmetic overflow error on 32-bit INT arithmetic`)
- **검증 스위트**: `pkg/engine/int_overflow_test.go`

### [N-9] `CAST(... AS INT)` 범위 초과 검증 누락
- **증상**: `CAST(3000000000 AS INT)` 실행 시 범위 초과 에러 없이 통과.
- **조치 내용**: `EvalCast`에 `TINYINT` (1바이트 `[0, 255]`), `SMALLINT` (2바이트 `[-32768, 32767]`), `INT` (4바이트 `[-2147483648, 2147483647]`)에 대한 정밀 경계 검사를 적용하고 범위 초과 시 Msg 8115 발생. 파서의 `TypeSpec`에 정수 크기별 정밀도를 보존하도록 수정.
- **해결 커밋**: `4bab6331` (`fix(proc,engine): enforce bounds check and arithmetic overflow error on integer casts`)
- **검증 스위트**: `pkg/engine/cast_overflow_test.go`

### [N-10] 수학 함수 정의역 밖 입력 시 오류 미발생 (사일런트 NULL)
- **증상**: `SQRT(-1)`, `LOG(0)` 등 정의역 밖 입력 시 에러 없이 NULL 반환.
- **조치 내용**: `pkg/proc/builtins_math.go`에 사전 입력 검증 래퍼 `evalMathBuiltinWithErr`를 도입하여 유효하지 않은 부동소수점 인자에 대해 Msg 3623 에러를 명시적으로 발생.
- **해결 커밋**: `ff473892` (`fix(proc,engine): enforce errors on negative string lengths, math domain violations, and duplicate proc variable declarations`)
- **검증 스위트**: `pkg/engine/easy_defects_test.go` (`TestEngine_MathDomainErrors`)

### [N-11] `@@IDENTITY`가 트리거 내부 생성 ID를 미반영
- **증상**: 트리거가 타깃 테이블에 행을 삽입할 때 `SCOPE_IDENTITY()`와 `@@IDENTITY`의 스코프 분리가 완전하지 않음.
- **조치 내용**: 세션 수준 `lastIdentity`를 최신 트리거 발생 ID로 갱신하면서, 상위 프로시저/배치 스코프의 `scopeIdentity`는 프레임별로 보존.
- **해결 커밋**: `d4d33b54` (`fix(engine,proc): enforce SCOPE_IDENTITY isolation and propagate @@IDENTITY across triggers`)
- **검증 스위트**: `pkg/engine/trigger_scope_identity_test.go`

### [N-12] `LEFT`, `RIGHT`, `SUBSTRING` 음수 길이 인자 시 에러 미발생
- **증상**: `LEFT('hello', -1)` 호출 시 에러 없이 빈 문자열 반환.
- **조치 내용**: `pkg/proc/builtins_string.go`에 `evalStringBuiltinWithErr`를 추가하여 음수 길이가 전달될 경우 Msg 536 (`Invalid length parameter passed to the LEFT or SUBSTRING function.`) 에러를 즉시 발생.
- **해결 커밋**: `ff473892` (`fix(proc,engine): enforce errors on negative string lengths, math domain violations, and duplicate proc variable declarations`)
- **검증 스위트**: `pkg/engine/easy_defects_test.go` (`TestEngine_NegativeLengthStringFunctions`)

---

## 4. 라운드 1 결함 연계 조치 현황

라운드 1 보고서(`notes/reports/20261119_LIVE_AUDIT_REPORT.ko.md`)에 등록되었던 10건의 결함에 대한 조치 및 검증 상태는 다음과 같습니다:

1. **[결함 1] `INSERT ... OUTPUT ... INTO @table_variable`**: VDBE `OUTPUT INTO` 파이프라인 연계 완료.
2. **[결함 2] `SCOPE_IDENTITY()` 스코프 격리**: C-1 정정에 따라 N-11로 재정의 후 해결 완료 (`d4d33b54`).
3. **[결함 3] Stored Procedure 내부 CTE(`WITH`)**: VDBE 내 CTE 바인딩 지원 완료 (`8a73a202`).
4. **[결함 4] Stored Procedure 내부 `VIEW` 조회**: VDBE 내 뷰 확장 지원 완료 (`8a73a202`).
5. **[결함 5] Stored Procedure 내부 `MERGE INTO` 파싱 거부**: 프로시저 파서에 MERGE 문장 지원 및 트랜잭션 바인딩 완료 (`cb8a0acc`).
6. **[결함 6] 프로시저 로컬 `#temp` 테이블 수명주기 해제 누락**: 프레임 종료 시 세션 맵 임시 테이블 자동 정리 완료 (`b6356d29`).
7. **[결함 7] `IDENTITY(seed, increment)` 초기 시드값 무시**: DDL 파서 및 시퀀스 할당기 시드/스텝 반영 완료 (`97654e68`, `pkg/engine/identity_ddl_test.go`).
8. **[결함 8] AFTER 트리거 내부 `@@ROWCOUNT` 누락**: 트리거 진입 프레임 생성 시 이전 DML의 영향 행 수 전달 완료.
9. **[결함 9] Stored Procedure 내부 직접 DDL 허용**: 프로시저 내부 DDL 파싱 및 실행 지원 완료.
10. **[결함 10] 프로시저 매개변수와 로컬 변수의 동일 이름 재선언 허용**: `c.declaredVars` 중복 검증을 통해 Msg 134 컴파일 에러 발생 완료 (`ff473892`, `pkg/engine/easy_defects_test.go`).


---

## 4. Round 3 ~ 6: 8대 코어 아키텍처 원인 클러스터 (300건)

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


---

## 5. Round 7 & 8: 내장 함수 Arity 검증, 날짜 생성자 및 윈도우 프레임 경계 (200건)

### 1) 내장 및 집계 함수 Arity 전수 검증 (`ValidateBuiltinArity`)
- **결함 원인**: `LEN()`, `GETDATE(1)`, `COUNT()`, `SUM(1, 2)`, `NULLIF(1)`, `IIF(1, 2)` 등 함수 인자 수 검증 누락으로 사일런트 오류 또는 가비지 평가 발생.
- **해결 조치**: `pkg/proc/builtins_sys.go`에 `ValidateBuiltinArity(funcName, argCount)` 검증기를 탑재하여 인자 수가 맞지 않으면 컴파일 즉시 Fail-Closed 에러 반환.
- **재현 예시**:
```sql
SELECT LEN();       -- [오류 발생] requires 1 argument
SELECT GETDATE(1);  -- [오류 발생] requires 0 argument
SELECT SUM(1, 2);   -- [오류 발생] requires 1 argument
SELECT IIF(1, 2);   -- [오류 발생] requires 3 arguments
```

### 2) 날짜/시간 생성자 6대 함수 유효성 검증
- **검증 대상**: `DATEFROMPARTS`, `TIMEFROMPARTS`, `DATETIMEFROMPARTS`, `SMALLDATETIMEFROMPARTS`, `DATETIME2FROMPARTS`, `DATETIMEOFFSETFROMPARTS`
- **결함 원인**: `DATEFROMPARTS(2026, 2, 30)` 등 그레고리력 범위 밖의 날짜 입력 시 에러 없이 통과하거나 가비지 날짜 반환.
- **해결 조치**: `pkg/proc/eval.go`에 윤년 및 월별 일수 검증 로직 연결. 유효하지 않은 날짜/시간 입력 시 `Cannot construct data type date/datetime` 에러 반환.

### 3) 윈도우 프레임 경계(ROWS BETWEEN) 및 정렬 필수 규칙
- **결함 원인**:
  1. `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `NTILE()` 윈도우 함수가 `OVER ()` 내 `ORDER BY` 절 없이 호출되는 것을 방치.
  2. `ROWS BETWEEN 5 PRECEDING AND 10 PRECEDING`과 같이 시작 오프셋이 종료 오프셋보다 큰 비정상 프레임 허용.
- **해결 조치**:
  1. 정렬 필수 윈도우 함수에서 `ORDER BY` 누락 시 `Msg 4112` (`The function requires an ORDER BY clause in the OVER clause`) 에러 반환.
  2. 프레임 시작 오프셋과 종료 오프셋의 정렬 순서 검증 추가.

---

## 6. Round 9: 보안, RBAC/DCL 권한 체계 및 트랜잭션 격리 (17건)

PART I. SECURITY, RBAC & AUTHORIZATION ROOT CAUSES (9 DEFECTS)
====================================================================================================

[SEC-001]
Component: Authentication & Impersonation Engine
File: pkg/engine/sqlexec_auth.go:506-511
Title: REVERT in Direct Session Causes Instant Superuser Escalation to "dbo"
Severity: CRITICAL
Root Cause:
  In `execRevert`, when `cs.OriginalUser == ""` (meaning the user never executed `EXECUTE AS` or `SETUSER`),
  the engine defaults `cs.User = "dbo"` instead of returning an error or maintaining the current identity.
  This allows any unprivileged user (e.g. `guest`, `app_user`) to instantly elevate their session to `dbo`
  simply by sending the command `REVERT;`.
Repro Query:
  -- Login as guest
  SELECT CURRENT_USER; -- Returns 'guest'
  REVERT;
  SELECT CURRENT_USER; -- Returns 'dbo'
Expected Behavior:
  Return error Msg 15151: "The REVERT statement cannot be executed because the security context has not been changed."
Actual Behavior:
  Session identity unconditionally changes to 'dbo' with full administrative privileges.

----------------------------------------------------------------------------------------------------
[SEC-002]
Component: Impersonation & Security Context
File: pkg/engine/sqlexec_auth.go:520-543
Title: EXECUTE AS and SETUSER Bypass IMPERSONATE Permission Checks & Lack Execution Stack
Severity: HIGH
Root Cause:
  `execExecuteAs` and `execSetUser` unconditionally overwrite `cs.User = targetUser` without verifying
  whether the caller possesses `IMPERSONATE` permission on the target principal.
  Furthermore, `cs.OriginalUser` is stored as a single flat string rather than an execution stack.
  Executing nested `EXECUTE AS` (e.g. A -> B -> C) permanently loses the original identity A on the first `REVERT`.
Repro Query:
  -- Login as unprivileged user
  EXECUTE AS USER = 'sa';
  SELECT CURRENT_USER; -- returns 'sa' without error
Expected Behavior:
  Raise permission error Msg 15151: "Cannot execute as user 'sa', because it does not exist or you do not have permission."
Actual Behavior:
  Any user can impersonate 'sa' or 'dbo' without authorization.

----------------------------------------------------------------------------------------------------
[SEC-003]
Component: DCL & User Management Engine
File: pkg/engine/sqlexec_auth.go:230-492
Title: Missing Authorization Checks on All DCL Handlers (CREATE/ALTER/DROP LOGIN/USER, GRANT/REVOKE/DENY)
Severity: CRITICAL
Root Cause:
  The internal handler functions `execCreateLogin`, `execAlterLogin`, `execDropLogin`, `execCreateUser`,
  `execDropUser`, `execGrant`, `execRevoke`, and `execDeny` do not inspect caller permissions (e.g. `SECURITYADMIN`,
  `ACCESSADMIN`, or `GRANT OPTION`). The calling session's identity (`cs`) is not validated prior to mutating
  the engine's credential store or permission maps.
Repro Query:
  -- As unprivileged user:
  GRANT ALL TO guest;
  ALTER LOGIN sa WITH PASSWORD = 'hacked123';
Expected Behavior:
  Raise permission denial error.
Actual Behavior:
  Unprivileged user successfully changes administrative passwords and self-grants all permissions.

----------------------------------------------------------------------------------------------------
[SEC-004]
Component: DCL Error Handling & Parser Integration
File: pkg/engine/sqlexec_auth.go:374-454
Title: execGrant and execRevoke Silently Mask Invalid Privileges and Missing Principals
Severity: MEDIUM
Root Cause:
  `execGrant` and `execRevoke` silently swallow errors during privilege parsing. If an administrator attempts to
  grant or revoke a non-existent privilege (e.g. `REVOKE FLY_TO_MOON ON t FROM u;`), or references a non-existent
  principal, the engine returns `Success: true` with zero rows affected. Administrators believe permissions have
  been revoked when no security operation took place.
Repro Query:
  REVOKE NON_EXISTENT_PERM ON table1 FROM guest;
Expected Behavior:
  Return error: Msg 15151 "Cannot find the object 'table1', or you do not have permission."
Actual Behavior:
  Returns success with "Command(s) completed successfully."

----------------------------------------------------------------------------------------------------
[SEC-005]
Component: Query Engine / MERGE Execution
File: pkg/engine/sqlexec_merge.go:1-250
Title: MERGE Statement Completely Bypasses checkTableAccess
Severity: CRITICAL
Root Cause:
  Unlike `SELECT`, `INSERT`, `UPDATE`, and `DELETE` handlers which call `cs.checkTableAccess(table, perm)`,
  the `execMerge` handler never calls `checkTableAccess` on either the target table or source table.
  A user with explicit `DENY ALL` on a table can read and mutate all records using `MERGE INTO target USING source`.
Repro Query:
  DENY SELECT, INSERT, UPDATE, DELETE ON sec_t1 TO guest;
  -- Run as guest:
  MERGE INTO sec_t1 AS tgt USING (SELECT 1 AS val) AS src ON 1=0 WHEN NOT MATCHED THEN INSERT VALUES (src.val);
Expected Behavior:
  Msg 229: "The INSERT permission was denied on the object 'sec_t1'."
Actual Behavior:
  Query succeeds and inserts data into the denied table.

----------------------------------------------------------------------------------------------------
[SEC-006]
Component: Query Engine / Subquery Permission Checker
File: pkg/engine/sqlexec_select.go:1990-2005
Title: Subqueries in WHERE, JOIN, and SELECT Projections Bypass RBAC Table Access Checks
Severity: HIGH
Root Cause:
  The permission verification hook in `execSelect` only extracts table references from top-level `FROM` and
  outer `JOIN` clauses. Subqueries embedded in `WHERE col IN (SELECT ...)`, `WHERE EXISTS (SELECT ...)`, or
  scalar subqueries in column projection lists are never traversed for `checkTableAccess`.
Repro Query:
  DENY SELECT ON confidential_salaries TO guest;
  -- Run as guest:
  SELECT (SELECT MAX(salary) FROM confidential_salaries);
Expected Behavior:
  Msg 229: "The SELECT permission was denied on the object 'confidential_salaries'."
Actual Behavior:
  Subquery executes and leaks the confidential data.

----------------------------------------------------------------------------------------------------
[SEC-007]
Component: View Expansion & Object Ownership Chaining
File: pkg/engine/sqlexec_select.go:340-420
Title: View Expansion Ignores DENY Permissions on Underlying Base Tables
Severity: HIGH
Root Cause:
  LionSQL expands view AST nodes into the main query AST prior to performing permission verification, but
  fails to implement ownership chaining semantics. If an administrator issues `DENY SELECT ON base_table TO guest`,
  yet grants access to a view that selects from `base_table`, or vice versa, the engine either exposes denied
  data or rejects legitimate owner-chained views due to evaluating permissions on the wrong node level.
Repro Query:
  DENY SELECT ON v_secret TO guest;
  -- But guest has SELECT on base_t. Guest executes SELECT * FROM v_secret.
Expected Behavior:
  Direct access to `v_secret` must be rejected if denied on the view.
Actual Behavior:
  Access check only evaluates `base_t` after AST view expansion, bypassing view-level `DENY`.

----------------------------------------------------------------------------------------------------
[SEC-008]
Component: DDL Engine / Table Renaming
File: pkg/engine/sqlexec_ddl.go:1300-1600
Title: ALTER TABLE RENAME Desynchronizes RBAC Grant/Deny Maps
Severity: HIGH
Root Cause:
  When a table is renamed via `ALTER TABLE t1 RENAME TO t2;` or `sp_rename`, the engine updates the storage schema
  and catalog metadata, but does not update the permission maps in the security manager. All existing `DENY`
  and `REVOKE` rules associated with `t1` are orphaned, leaving `t2` completely unprotected or with default permissions.
Repro Query:
  DENY SELECT ON secret_tbl TO guest;
  ALTER TABLE secret_tbl RENAME TO public_tbl;
  -- Guest selects from public_tbl.
Expected Behavior:
  Permissions, denies, and grants must either follow the renamed object or renaming must be restricted.
Actual Behavior:
  All security restrictions on the table are wiped upon renaming.

----------------------------------------------------------------------------------------------------
[SEC-009]
Component: Session & Administrative Commands
File: pkg/engine/sqlexec.go:941
Title: KILL Command Executable by Unprivileged Users
Severity: MEDIUM
Root Cause:
  The `KILL <session_id>` command handler in `sqlexec.go` directly terminates client connections without verifying
  that the calling user has `ALTER ANY CONNECTION` or administrative role (`sysadmin`). Any unprivileged client
  can terminate other users' active transactions and connections, resulting in denial of service.
Repro Query:
  -- As guest:
  KILL 12;
Expected Behavior:
  Msg 224: "User does not have permission to use the KILL statement."
Actual Behavior:
  The targeted SPID is forcefully terminated.


====================================================================================================
PART II. TRANSACTION MANAGEMENT & ACID ROOT CAUSES (8 DEFECTS)
====================================================================================================

[TX-001]
Component: Transaction Manager / Savepoint & DDL Actions
File: pkg/tx/manager.go:288-303, pkg/engine/sqlexec.go:1086-1107
Title: ROLLBACK TO SAVEPOINT Does Not Roll Back Deferred DDL Actions (Permanent Object Deletion)
Severity: CRITICAL
Root Cause:
  LionSQL schedules physical table deletions as deferred commit actions (`tx.AddCommitAction`).
  When `ROLLBACK TO SAVEPOINT sp1` is invoked, `manager.go` rolls back WiredTiger record logs up to the savepoint,
  but fails to truncate or clear the slice of deferred commit/rollback actions recorded after the savepoint.
  As a consequence, if a table is dropped after a savepoint, and the user executes `ROLLBACK TO SAVEPOINT`,
  subsequent `COMMIT` of the outer transaction executes the deferred drop action and destroys the table!
Repro Query:
  BEGIN TRANSACTION;
  CREATE TABLE keep_me (id INT);
  SAVEPOINT sp1;
  DROP TABLE keep_me;
  ROLLBACK TO SAVEPOINT sp1;
  COMMIT;
  SELECT * FROM keep_me;
Expected Behavior:
  `keep_me` exists and is intact.
Actual Behavior:
  `keep_me` is permanently dropped on COMMIT because the drop action was not removed during rollback to savepoint.

----------------------------------------------------------------------------------------------------
[TX-002]
Component: DDL Rollback Handlers
File: pkg/engine/sqlexec_ddl.go:1-169, pkg/engine/sqlexec_auth.go
Title: Non-Table DDL and DCL Statements Lack Rollback Actions in Transactions
Severity: HIGH
Root Cause:
  `CREATE/DROP TABLE` and `CREATE/DROP INDEX` register rollback actions, but `CREATE/DROP VIEW`, `SEQUENCE`,
  `PROCEDURE`, `FUNCTION`, `TRIGGER`, table constraints (`ADD CONSTRAINT CHECK/DEFAULT/FK`), and all DCL commands
  (`GRANT/REVOKE/LOGIN/USER`) do not invoke `tx.AddRollbackAction(...)`.
  If executed within a transaction that subsequently issues `ROLLBACK`, these changes are NOT reverted.
Repro Query:
  BEGIN TRANSACTION;
  CREATE VIEW v_temp AS SELECT 1 AS x;
  ROLLBACK;
  SELECT * FROM v_temp;
Expected Behavior:
  Msg 208: "Invalid object name 'v_temp'."
Actual Behavior:
  `v_temp` still exists in the catalog despite the transaction being rolled back.

----------------------------------------------------------------------------------------------------
[TX-003]
Component: Transaction Isolation / Read-Only Mode
File: pkg/engine/sqlexec_ddl.go, pkg/engine/sqlexec_auth.go
Title: BEGIN TRANSACTION READ ONLY Allows DDL and DCL Statement Execution
Severity: HIGH
Root Cause:
  The `ReadOnly` transaction mode check is implemented solely inside the record storage write path (`wt_engine.go`).
  DDL handlers (`CREATE TABLE`, `DROP TABLE`, `ALTER TABLE`) and security handlers (`CREATE USER`, `GRANT`)
  never verify `cs.Tx().ReadOnly`.
Repro Query:
  BEGIN TRANSACTION READ ONLY;
  CREATE TABLE ro_leak (id INT);
  COMMIT;
Expected Behavior:
  Msg 3923: "Cannot execute DDL statements in a read-only transaction."
Actual Behavior:
  DDL executes and commits successfully within a read-only transaction.

----------------------------------------------------------------------------------------------------
[TX-004]
Component: Session Variable Synchronization
File: pkg/engine/sqlexec.go:1151-1160
Title: SET TRANSACTION READ ONLY Inside Active Transaction Fails to Mutate Active Tx State
Severity: MEDIUM
Root Cause:
  When `SET TRANSACTION READ ONLY` is executed while a transaction is already active, `sqlexec.go` updates
  `cs.Vars["transaction_read_only"]` but does not update the current transaction's internal flags (`cs.Tx().ReadOnly`).
  Subsequent write operations (`INSERT`, `UPDATE`, `DELETE`) in that same transaction continue to succeed.
Repro Query:
  BEGIN TRANSACTION;
  SET TRANSACTION READ ONLY;
  INSERT INTO t VALUES (1); -- succeeds!
Expected Behavior:
  SQLSTATE 25001 or error preventing data modifications in read-only mode.
Actual Behavior:
  Modifications succeed because only session variable map was modified.

----------------------------------------------------------------------------------------------------
[TX-005]
Component: Transaction Isolation Level Configuration
File: pkg/engine/sqlexec.go:1138
Title: SET TRANSACTION ISOLATION LEVEL Accepts Arbitrary Invalid Strings Without Error
Severity: MEDIUM
Root Cause:
  The parser and execution handler for `SET TRANSACTION ISOLATION LEVEL` extract the isolation level string
  and store it blindly without validating against supported levels (READ UNCOMMITTED, READ COMMITTED,
  REPEATABLE READ, SERIALIZABLE, SNAPSHOT). Furthermore, it does not propagate isolation configurations
  to WiredTiger transaction options (`wt_session:begin_transaction`).
Repro Query:
  SET TRANSACTION ISOLATION LEVEL FOOBAR_LEVEL_XYZ;
Expected Behavior:
  Syntax/Configuration error: invalid isolation level.
Actual Behavior:
  Command returns success.

----------------------------------------------------------------------------------------------------
[TX-006]
Component: Session State Machine / DISCARD ALL
File: pkg/engine/sqlexec.go:1121
Title: DISCARD ALL Allowed Inside Active Transaction (Protocol State Machine Violation)
Severity: MEDIUM
Root Cause:
  `DISCARD ALL` resets session variables, temporary tables, and prepared statements. In SQL standards and
  PostgreSQL protocol, `DISCARD ALL` inside an active transaction block is forbidden (SQLSTATE 25001).
  LionSQL executes `DISCARD ALL` inside active transactions, wiping internal metadata while leaving
  the underlying storage transaction open in a corrupted state.
Repro Query:
  BEGIN TRANSACTION;
  DISCARD ALL;
Expected Behavior:
  ERROR 25001: "DISCARD ALL cannot run inside a transaction block"
Actual Behavior:
  Session state is wiped while transaction remains dangling.

----------------------------------------------------------------------------------------------------
[TX-007]
Component: T-SQL Table Variable Semantics
File: pkg/engine/sqlexec.go:908
Title: T-SQL Table Variables Incorrectly Rolled Back on ROLLBACK
Severity: MEDIUM
Root Cause:
  LionSQL implements table variables (`DECLARE @t TABLE(...)`) by rewriting them to temporary physical tables
  enrolled in the standard transaction rollback journal. In standard T-SQL (MS SQL Server), table variables
  are explicitly out-of-scope for transaction rollbacks (designed specifically for audit logging and error tracking).
Repro Query:
  DECLARE @tbl TABLE (id INT);
  BEGIN TRANSACTION;
  INSERT INTO @tbl VALUES (10);
  ROLLBACK;
  SELECT COUNT(*) FROM @tbl;
Expected Behavior:
  Returns 1 (table variables are immune to transaction rollback).
Actual Behavior:
  Returns 0 (table variable is wiped by transaction rollback).

----------------------------------------------------------------------------------------------------
[TX-008]
Component: WiredTiger Savepoint Stack
File: pkg/storage/wiredtiger/wt_engine.go:4113-4125, pkg/tx/savepoint.go:38-44
Title: Redeclaring Existing Savepoint Name Corrupts Savepoint Resolution Stack
Severity: HIGH
Root Cause:
  When a savepoint name is reused (`SAVEPOINT s1; SAVEPOINT s2; SAVEPOINT s1;`), the high-level `SavepointStack`
  updates the existing entry's position, but `WTTx.spNames` in `wt_engine.go` either appends or fails to match
  the index properly. Subsequent calls to `ROLLBACK TO SAVEPOINT s2` report `savepoint not found: s2` or
  rollback to an invalid storage state.
Repro Query:
  BEGIN TRANSACTION;
  SAVEPOINT s1;
  SAVEPOINT s2;
  SAVEPOINT s1;
  ROLLBACK TO SAVEPOINT s2;
Expected Behavior:
  Msg 3926: "Cannot roll back to savepoint 's2' because savepoint s1 was re-established after s2."
Actual Behavior:
  Internal panic or "savepoint not found: s2" due to storage savepoint array index desynchronization.

====================================================================================================
END OF ROUND 9 AUDIT REPORT
====================================================================================================


---

## 7. Round 10: 프로시저 VM, 커서 상태 머신, CBO 검증 (10건)

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


---

## 8. Round 11: 시퀀스 엔진, Option B 인덱스, 플랜 캐시 무효화 (7건)

## 2. Round 11 결함 상세 카탈로그 (7건)

### [ENG-011] 시퀀스 엔진이 미구현 스텁 상태이며, `NEXTVAL`이 항상 고정값 1을 반환하는 결함
- **대상 컴포넌트**: 시퀀스 엔진 및 내장 함수
- **관련 파일**: `pkg/proc/builtins_sys.go:350-351`, `pkg/engine/sqlexec.go:1928-1935`
- **심각도**: 치명적 (CRITICAL)
- **근본 원인**:
  `sqlexec.go:1928`에서 `CREATE SEQUENCE`, `ALTER SEQUENCE`, `DROP SEQUENCE` 구문이 "PG 호환성 허용 스텁"으로 처리되어 카탈로그나 영구 시퀀스 생성기를 할당하지 않고 성공을 반환합니다. 또한 `builtins_sys.go:350`에서 `NEXTVAL` 호출 시 항상 고정 정수 `storage.IntValue(1)`을 반환하도록 하드코딩되어 있습니다. 이에 따라 `NEXTVAL('seq')`를 반복 호출하더라도 항상 1만 반환되며, 표준 T-SQL 구문인 `NEXT VALUE FOR seq` 역시 평가되지 않고 리터럴 문자열로 반환됩니다.
- **재현 쿼리**:
  ```sql
  CREATE SEQUENCE test_seq START WITH 1 INCREMENT BY 1;
  SELECT NEXTVAL('test_seq'); -- 1 반환
  SELECT NEXTVAL('test_seq'); -- 1 반환 (기대값: 2)
  SELECT NEXTVAL('test_seq'); -- 1 반환 (기대값: 3)
  ```
- **정상 동작 (Expected)**: `NEXTVAL`은 시퀀스 생성기 상태에 따라 단조 증가(monotonic advance)해야 함.
- **실제 동작 (Actual)**: 항상 정수 1만 고정 반환.

---

### [ENG-012] `CreateIndex` 시 `IndexSchema.Validate` 호출이 누락되어 Option B 인덱스 제한이 미적용되는 결함
- **대상 컴포넌트**: 스토리지 엔진 / 인덱스 생성 (Option B 명세)
- **관련 파일**: `pkg/storage/wiredtiger/wt_engine.go:2019-2065`, `pkg/storage/memengine/ddl.go:110-145`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  Option B 물리 제약(`MaxIndexColumns = 32`, `MaxIndexKeyBytes = 1,700B`)을 위한 `IndexSchema.Validate(tableSchema)` 메서드가 구현되었으나, `WTEngine.CreateIndex` 및 `MemEngine.CreateIndex` 내부에서 인덱스 생성 전 이를 호출하지 않아 33개 이상의 컬럼이나 1,700바이트를 초과하는 인덱스가 생성 통과되는 결함입니다. (※ 직전 커밋 `6d32c4bd`에서 유효성 검증 로직 연결 완료).
- **재현 쿼리**:
  ```sql
  CREATE TABLE t_idx (c0 INT, c1 INT, ... c34 INT);
  CREATE INDEX idx_35 ON t_idx (c0, c1, ... c34); -- 35개 컬럼
  ```
- **정상 동작 (Expected)**: 에러 발생: "index has 35 columns (max 32)" (`ErrTooManyIndexColumns`, Msg 1904).
- **실제 동작 (Actual)**: 인덱스 생성이 성공함.

---

### [ENG-013] `DROP COLUMN` 후 Prepared Statement 캐시가 무효화되지 않고 오래된 컬럼 오프셋을 참조하여 빈 문자열을 반환하는 결함
- **대상 컴포넌트**: 쿼리 플랜 캐시 & Prepared Statements
- **관련 파일**: `pkg/engine/sqlexec_prepare.go`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  `ALTER TABLE ... DROP COLUMN`으로 컬럼이 삭제되었을 때, 세션에 이미 준비된 문장(`PREPARE p AS SELECT dropped_col ...`)이 무효화되거나 재컴파일되지 않습니다. 삭제된 컬럼을 조회하는 Prepared Statement 실행 시 컬럼 부재 오류를 내지 않고 오래된 튜플 오프셋으로 실행되어 빈 문자열 또는 손상된 프로젝션 결과를 조용히 반환합니다.
- **재현 쿼리**:
  ```sql
  CREATE TABLE t_prep (id INT PRIMARY KEY, c1 INT, c2 INT);
  INSERT INTO t_prep VALUES (1, 10, 20);
  PREPARE p_sel AS SELECT c2 FROM t_prep WHERE id = $1;
  EXECUTE p_sel(1); -- 20 반환
  ALTER TABLE t_prep DROP COLUMN c2;
  EXECUTE p_sel(1); -- 에러 대신 빈 문자열 "" 반환!
  ```
- **정상 동작 (Expected)**: Msg 207: "Invalid column name 'c2'." 또는 스키마 버전 불일치 오류.
- **실제 동작 (Actual)**: 성공적으로 실행되며 빈 문자열 반환.

---

### [ENG-014] 동시 쓰기-쓰기 충돌(Write-Write Conflict) 발생 시 직렬화 실패 없이 덮어쓰는 결함
- **대상 컴포넌트**: 스토리지 동시성 제어 & MVCC
- **관련 파일**: `pkg/storage/wiredtiger/wt_engine.go`
- **심각도**: 높음 (HIGH)
- **근본 원인**:
  PostgreSQL / MSSQL 스냅샷 격리(Snapshot Isolation) 수준에서는 트랜잭션 A와 B가 동일한 행(`WHERE id = 1`)을 동시에 수정하려 할 때, 두 번째 트랜잭션은 대기하거나 직렬화 오류(`could not serialize access due to concurrent update`, SQLSTATE 40001)로 중단되어야 합니다. 현재 엔진은 두 동시 트랜잭션이 순차적으로 커밋되는 것을 허용하여, 늦게 커밋된 트랜잭션이 먼저 커밋된 쓰기를 덮어쓰는 갱신 손실(lost update)이 발생합니다.
- **재현 쿼리**:
  ```sql
  -- 세션 1: BEGIN; UPDATE t SET val = 101 WHERE id = 1; SELECT sleep(1); COMMIT;
  -- 세션 2: BEGIN; UPDATE t SET val = 202 WHERE id = 1; COMMIT;
  ```
- **정상 동작 (Expected)**: 세션 2는 세션 1의 충돌을 감지하고 직렬화 실패 오류를 반환해야 함.
- **실제 동작 (Actual)**: 두 세션 모두 정상 커밋되며 세션 2가 세션 1의 데이터를 조용히 덮어씀.

---

### [ENG-015] `SET work_mem`, `statement_timeout`, `lock_timeout`이 음수 및 잘못된 값을 에러 없이 수용하는 결함
- **대상 컴포넌트**: 세션 설정 & 메모리 거버너
- **관련 파일**: `pkg/engine/sqlexec.go:1150-1175`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  세션 `SET` 명령 처리기가 범위나 타입 검증 없이 값을 직접 `cs.Vars`에 저장합니다. `work_mem = '-100MB'`, `statement_timeout = -5000`, `statement_timeout = 'garbage'`와 같이 유효하지 않은 설정값을 지정하더라도 오류(SQLSTATE 22023)를 내지 않고 정상 "SET" 응답을 반환합니다.
- **재현 쿼리**:
  ```sql
  SET work_mem = '-100MB';
  SET statement_timeout = 'not_a_number';
  ```
- **정상 동작 (Expected)**: ERROR 22023: `"invalid value for parameter \"work_mem\""`
- **실제 동작 (Actual)**: 에러 없이 "SET" 성공 반환.

---

### [ENG-016] `ALTER TABLE ADD CONSTRAINT` 실행 시 동일 테이블 내 중복된 제약조건 이름을 허용하는 결함
- **대상 컴포넌트**: DDL 테이블 제약조건
- **관련 파일**: `pkg/engine/sqlexec_ddl.go:1300-1400`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  `ALTER TABLE t ADD CONSTRAINT name CHECK (...)` 실행 시 동일한 `name`의 제약조건이 이미 해당 테이블이나 스키마에 존재하는지 확인하지 않고 추가합니다. 동일한 이름의 제약조건이 중복 등록되면 ANSI SQL 고유성 요건을 위반하며 `DROP CONSTRAINT` 시 결정론적 처리가 불가능해집니다.
- **재현 쿼리**:
  ```sql
  ALTER TABLE t ADD CONSTRAINT chk_id CHECK (id > 0);
  ALTER TABLE t ADD CONSTRAINT chk_id CHECK (id < 100); -- 성공함!
  ```
- **정상 동작 (Expected)**: Msg 2714: `"There is already an object named 'chk_id' in the database."`
- **실제 동작 (Actual)**: 동일한 이름으로 두 제약조건이 모두 추가됨.

---

### [ENG-017] 존재하지 않는 테이블에 대해 `LOCK TABLE`이 조용히 성공하는 결함
- **대상 컴포넌트**: 허용적(Tolerant) DDL 엔진
- **관련 파일**: `pkg/engine/sqlexec.go:1937-1945`
- **심각도**: 보통 (MEDIUM)
- **근본 원인**:
  `sqlexec.go:1937`의 `LOCK TABLE` 처리가 무조건 성공을 반환하는 PG 호환성 스텁으로 구현되어 있어, 대상 테이블이 실제 존재하는지 확인하지 않고 `LOCK TABLE executed successfully.` 메시지를 반환합니다.
- **재현 쿼리**:
  ```sql
  LOCK TABLE non_existent_table_xyz IN ACCESS EXCLUSIVE MODE;
  ```
- **정상 동작 (Expected)**: ERROR 42P01: `"relation \"non_existent_table_xyz\" does not exist"`
- **실제 동작 (Actual)**: 성공 메시지 반환: `"LOCK TABLE executed successfully."`


---

## 9. Round 12: JSON strict 모드, XML 메소드 차단, CONVERT 스타일 (5건)

## 2. 결함 상세 목록 (ENG-018 ~ ENG-022)

### [ENG-018] JSON_VALUE & JSON_QUERY가 "strict" 경로 모드를 무시하고 누락된 키에 대해 사일런트 NULL 반환
- **컴포넌트**: JSON Path Engine
- **해당 파일**: `pkg/proc/builtins_json.go:12-56`, `pkg/storage/json.go:452-468`
- **심각도**: MEDIUM
- **근본 원인**:
  - `storage/json.go:456`의 `navigatePath` 함수에서 접두사 `"STRICT "`를 제거하지만, `strict` 모드가 요청되었다는 플래그를 기록하지 않습니다.
  - SQL:2016 표준 및 Microsoft SQL Server 명세에 따르면, `strict` 경로 모드에서 존재하지 않는 키를 참조하거나 유효하지 않은 경로를 쿼리하면 반드시 오류(`Msg 13608: Property cannot be found on the specified JSON path`)가 발생해야 합니다.
  - 현재 LionSQL은 `strict` 모드를 `lax` 모드와 동일하게 취급하여 오류 없이 조용히 `NULL`을 반환합니다.
- **재현 쿼리**:
  ```sql
  SELECT JSON_VALUE('{"a": 1}', 'strict $.b');
  ```
- **기대 동작**: 오류 발생 (`Msg 13608: Property 'b' cannot be found on the specified JSON path.`)
- **실제 동작**: 오류 없이 `NULL` 반환

---

### [ENG-019] JSON_MODIFY의 "append" 수식어가 스칼라 또는 객체에 적용될 때 JSON 구조를 오염시킴
- **컴포넌트**: JSON Modification Engine
- **해당 파일**: `pkg/proc/builtins_json.go:12-56`
- **심각도**: HIGH
- **근본 원인**:
  - `modifyJSONByPath` 함수에서 경로 문자열을 파싱할 때 `append`와 같은 수식어 키워드를 검사하지 않습니다.
  - `append $.key`가 전달되면 엔진은 `append`를 액션 수식어로 추출하지 못하고, 리터럴 키 `"append $": {"key": value}`를 가진 손상된 JSON 객체를 생성합니다.
- **재현 쿼리**:
  ```sql
  SELECT JSON_MODIFY('{"a": 100}', 'append $.a', 200);
  ```
- **기대 동작**: 대상이 배열이 아닌 경우 오류를 반환하거나 배열로 추가(`{"a": [100, 200]}`)해야 함.
- **실제 동작**: 비정상적인 JSON 생성 (`{"a":100,"append $":{"a":200}}`)

---

### [ENG-020] 변수 메소드 호출(@x.value(), @x.query())이 선언된 변수 원본 값을 그대로 반환
- **컴포넌트**: Expression Evaluator / Variable Method Dispatch
- **해당 파일**: `pkg/engine/sqlexec_select.go:1018-1032`
- **심각도**: MEDIUM
- **근본 원인**:
  - `sqlexec_select.go:1019`는 변수 식별자를 파싱하지만, 점(dot) 구문이 포함된 메소드 호출(예: XML 메소드 `@x.value(...)` 또는 UDT 메소드 호출)이 들어왔을 때 메소드를 호출하지 않고 변수 `@x`를 찾아 원시 문자열을 그대로 반환하거나 미지원 오류를 내지 않습니다.
- **재현 쿼리**:
  ```sql
  DECLARE @x XML = '<root><val>123</val></root>';
  SELECT @x.value('(/root/val)[1]', 'INT');
  ```
- **기대 동작**: 파싱된 값 `123` 반환 또는 XML 인스턴스 메소드 미지원 오류 발생.
- **실제 동작**: 원시 XML 문자열(`<root><val>123</val></root>`)을 사일런트하게 반환.

---

### [ENG-021] CONVERT(VARCHAR, datetime, style)가 유효하지 않은 스타일에 대해 기본값으로 사일런트 폴백
- **컴포넌트**: Type Conversion Engine (CONVERT)
- **해당 파일**: `pkg/proc/eval.go:2158-2191`
- **심각도**: MEDIUM
- **근본 원인**:
  - `formatDateTimeByStyle`의 `default` 분기에서 무조건 `t.Format("2006-01-02 15:04:05")`를 반환합니다.
  - SQL Server 명세에 따르면, 지원되지 않거나 유효하지 않은 스타일 정수(예: `99999`)가 `CONVERT`에 전달되면 반드시 `Msg 281: Cannot convert date and/or time to character string using style %d.`를 발생시켜야 합니다.
  - 현재 LionSQL은 유효하지 않은 스타일을 무시하고 표준 ISO 타임스탬프 형식으로 출력합니다.
- **재현 쿼리**:
  ```sql
  SELECT CONVERT(VARCHAR, CAST('2026-11-19 12:34:56' AS DATETIME), 99999);
  ```
- **기대 동작**: `Msg 281: "Cannot convert date and/or time to character string using style 99999."`
- **실제 동작**: 오류 없이 `'2026-11-19 12:34:56'` 반환.

---

### [ENG-022] PARSE 및 TRY_PARSE 함수가 원문 표현식 문자열을 그대로 반환
- **컴포넌트**: Cultural & Number Parser
- **해당 파일**: `pkg/proc/eval.go`
- **심각도**: MEDIUM
- **근본 원인**:
  - T-SQL의 `PARSE(string_expr AS data_type [USING culture])` 및 `TRY_PARSE(...)` 문법이 토큰 스트림에서는 인식되지만, 평가기(Evaluator)에서 실제 파싱을 수행하지 않고 인식할 수 없는 표현식으로 취급하여 원문 SQL 텍스트(`PARSE('123.45' AS DECIMAL(10,2) USING 'en-US')`)를 그대로 반환합니다.
- **재현 쿼리**:
  ```sql
  SELECT PARSE('123.45' AS DECIMAL(10,2) USING 'en-US');
  ```
- **기대 동작**: 숫자값 `123.45` 반환.
- **실제 동작**: 원시 문자열 `PARSE('123.45' AS DECIMAL(10,2) USING 'en-US')` 반환.


---

## 10. Round 13: 집계 역직렬화, DML 서브쿼리 스코프, MERGE 카디널리티 및 윈도우 프레임 (5건)

### [ENG-023] LazyTuple 최적화 경로에서 GROUP BY 해시 키 추출 시 지연 역직렬화 누락으로 인한 전체 그룹 붕괴
- **컴포넌트**: Execution Engine / HashAggregate
- **해당 파일**: `pkg/execution/aggregate.go`
- **심각도**: CRITICAL
- **근본 원인**:
  - `SeqScanIterator`는 성능 최적화를 위해 WiredTiger 원시 바이트를 `storage.NewLazyTuple(schema, rawData)`에 담아 `tuple.Lazy`로 전달하고 `tuple.Values` 슬라이스는 nil 원소들로 둡니다.
  - `HashAggregate` 그룹화 해시 키 빌더에서 `tuple.Values` 인덱스에 직접 접근하면서 지연 역직렬화(`lookupVal`)가 트리거되지 않아 모든 그룹 키가 `\x00NULL`로 묶이고 단일 그룹으로 오집계되었습니다.
- **재현 쿼리**:
  ```sql
  CREATE TABLE g_t (cat INT, val INT);
  INSERT INTO g_t VALUES (1, 10), (1, 20), (2, 30);
  SELECT cat, SUM(val) FROM g_t GROUP BY cat;
  ```
- **기대 동작**: cat=1 (30), cat=2 (30) 2개 행 반환.
- **실제 동작**: cat=1 (60) 단 1개 행으로 모든 그룹이 축약 병합됨.
- **해결 방안**: `aggregate.go`에서 `tuple.Lazy != nil` 검사를 통해 지연 역직렬화 헬퍼 `lookupVal`을 경유하도록 수정.

---

### [ENG-024] DML (UPDATE / DELETE) WHERE 절 서브쿼리 매개변수화 시 내부 컬럼 오염
- **컴포넌트**: DML Engine / ParameterizeSubquery
- **해당 파일**: `pkg/engine/sqlexec_dml.go`
- **심각도**: HIGH
- **근본 원인**:
  - `UPDATE`/`DELETE`의 WHERE 절 서브쿼리 매개변수 치환 시 비한정 컬럼(`v = 10`)이 전달되어 서브쿼리 내부 컬럼(`AVG(v)`)까지 덮어쓰여 서브쿼리가 잘못된 상수(`AVG(10)`)로 치환되는 결함.
- **재현 쿼리**:
  ```sql
  CREATE TABLE d_tgt (id INT, v INT);
  INSERT INTO d_tgt VALUES (1, 10), (2, 20), (3, 30);
  DELETE FROM d_tgt WHERE v > (SELECT AVG(v) FROM d_tgt);
  ```
- **기대 동작**: 평균 20보다 큰 id=3 삭제 (2행 잔존).
- **실제 동작**: 0건 삭제 (3행 잔존).
- **해결 방안**: `sqlexec_dml.go`에서 외부 행 맵에 테이블 한정자 접두사(`tableName + "." + k`)를 주입하여 내부 비한정 컬럼과의 충돌을 방지.

---

### [ENG-025] STRING_AGG NULL 구분자(Separator) 리터럴 전달 시 표준 위반
- **컴포넌트**: Aggregation / Builtin String Functions
- **해당 파일**: `pkg/execution/aggregate.go`
- **심각도**: MEDIUM
- **근본 원인**:
  - `STRING_AGG(val, NULL)` 실행 시 NULL 리터럴이 문자열 `"NULL"`로 누출되어 `aNULLb` 형태로 병합되던 표준 위반.
- **재현 쿼리**:
  ```sql
  SELECT STRING_AGG(val, NULL) FROM s_agg_t;
  ```
- **기대 동작**: `Msg 8711: The separator expression for STRING_AGG cannot be NULL` 오류 반환.
- **실제 동작**: 정상 성공하며 문자열 `"NULL"`로 결합.
- **해결 방안**: 구분자 인자가 NULL인 경우 `Msg 8711, Level 16, State 1`을 즉시 반환하도록 방어 가드 추가.

---

### [ENG-026] MERGE INTO 구문에서 복수 소스 행 매칭 시 카디널리티 위반 미검출
- **컴포넌트**: DML Engine / MERGE Executor
- **해당 파일**: `pkg/engine/sqlexec_merge.go`, `pkg/proc/runtime/vm_dispatcher.go`
- **심각도**: HIGH
- **근본 원인**:
  - `MERGE INTO`에서 동일한 타깃 행에 복수의 소스 행이 매칭될 때 `matchedTargetKeys` 중복 체크 누락 및 VDBE `OpResultCols`/`OpResultRow`의 다중 결과셋 처리 버그로 테이블 미참조 서브쿼리(`SELECT ... UNION ALL ...`) 행 유실 발생.
- **재현 쿼리**:
  ```sql
  MERGE INTO m_dup AS tgt
  USING (SELECT 1 AS id, 100 AS v UNION ALL SELECT 1, 200) AS src
  ON tgt.id = src.id
  WHEN MATCHED THEN UPDATE SET tgt.v = src.v;
  ```
- **기대 동작**: `Msg 8672: The MERGE statement attempted to UPDATE or DELETE the same row more than once.`
- **실제 동작**: 에러 없이 최종 소스 행으로 덮어씀.
- **해결 방안**: `sqlexec_merge.go`에서 중복 타깃 변경 감지 시 즉시 `Msg 8672` 반환 및 VDBE `OpResultRow` 결과셋 스트리밍 정합성 복구.

---

### [ENG-027] 윈도우 프레임 사양의 ANSI EXCLUDE CURRENT ROW 절 무시 결함
- **컴포넌트**: Planner / Window Frame Aggregator
- **해당 파일**: `pkg/planner/planner.go`, `pkg/execution/window.go`
- **심각도**: MEDIUM
- **근본 원인**:
  - 윈도우 프레임 구문에서 `EXCLUDE CURRENT ROW` 토큰이 파싱되지 않고 사일런트 무시되어 현재 행이 프레임 계산에 계속 포함됨.
- **재현 쿼리**:
  ```sql
  SELECT id, SUM(v) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW) FROM t_wf;
  ```
- **기대 동작**: `[50, 40, 30]` 반환 (각 행에서 본인 값을 제외한 나머지 합계).
- **실제 동작**: `[60, 60, 60]` 반환.
- **해결 방안**: `planner.go`에서 `EXCLUDE CURRENT ROW`를 인식하여 `WindowFrame.ExcludeCurrentRow = true`로 설정하고, `window.go`의 집계 루프에서 현재 인덱스(`k == i`)를 배제하도록 구현.

---

## 11. Round 14: TDS 통신 계층 대용량 데이터 전송, 스칼라 메타데이터 및 스키마 인덱스 동기화 (3건)

### [ENG-028] TDS 대용량 바이너리/문자열 결과셋 직렬화 시 비-PLP 헤더 오인코딩으로 인한 드라이버 슬라이스 패닉
- **컴포넌트**: TDS Wire Protocol (:1433) / TokenColMetaData
- **해당 파일**: `pkg/wire/tds/server.go`
- **심각도**: CRITICAL
- **근본 원인**:
  - TDS(:1433) 클라이언트에서 4,000자(UTF-16 기준 8,000바이트) 초과 문자열 또는 8,000바이트 초과 헥사/바이너리 데이터 조회 시, 결과셋 행이 `storage.Value` 타입으로 전달되어 `inferColumnInfos`의 `switch val := v.(type)`에서 언랩되지 않고 기본 텍스트(`TypeNVarChar, colSize = 4000`)로 유추됨.
  - go-mssqldb 등 클라이언트는 8,000바이트 고정 슬라이스를 할당했으나 서버가 2바이트 길이 헤더로 대용량 데이터를 보내 드라이버 슬라이스 패닉(`slice bounds out of range`) 및 연결 강제 종료 발생.
- **재현 쿼리**:
  ```go
  h := "0x" + strings.Repeat("41", 12000)
  db.QueryRowContext(ctx, "SELECT " + h).Scan(&out)
  ```
- **기대 동작**: PLP 스트리밍 모드(`colSize = -1`, `0xFFFF`)로 청크 분할 전송.
- **실제 동작**: `runtime error: slice bounds out of range [:24000] with capacity 8000` 드라이버 크래시.
- **해결 방안**: `server.go`에서 `unwrapTdsOutputValue`를 통해 `storage.Value`를 원시 바이트로 언랩하고, 8,000바이트 초과 데이터에 대해 컬럼명과 무관하게 무조건 `TypeBigVarBin` / `TypeNVarChar`의 `colSize = -1` (`col.IsPLP = true`)을 적용.

---

### [ENG-029] 테이블 없는 조건 불만족 스칼라 쿼리(WHERE 1=0)에서 빈 컬럼 메타데이터 반환
- **컴포넌트**: Query Compiler / TDS Wire Protocol
- **해당 파일**: `pkg/proc/compiler.go`, `pkg/proc/bytecode.go`, `pkg/proc/runtime/vm_dispatcher.go`
- **심각도**: HIGH
- **근본 원인**:
  - Entity Framework, Dapper, GORM 등 ORM이 모델 스키마 매핑을 위해 실행하는 `SELECT ... WHERE 1=0` 스칼라 쿼리에서 0개 행 반환 시, VDBE 엔진이 `OpResultRow`를 거치지 않아 프레임의 `f.columns`가 초기화되지 않고 빈 토큰이 TDS 클라이언트에 전송됨.
- **재현 쿼리**:
  ```sql
  SELECT 1 AS col_a, 'str' AS col_b WHERE 1=0;
  ```
- **기대 동작**: 행은 0개이나 `col_a`, `col_b` 메타데이터(TokenColMetaData) 반환.
- **실제 동작**: 빈 슬라이스 `[]` 반환으로 ORM 모델 매핑 예외 발생.
- **해결 방안**: VDBE 컴파일러에 `OpResultCols` 명령어를 신설하여 WHERE 조건 점프(`OpJumpIfFalse`) 이전에 컬럼 메타데이터를 선제 선언하도록 보장.

---

### [ENG-030] ALTER TABLE RENAME COLUMN 실행 시 인덱스 컬럼 참조 미동기화로 인한 스키마 검증 거부
- **컴포넌트**: DDL Engine / Schema Catalog
- **해당 파일**: `pkg/engine/sqlexec_ddl.go`
- **심각도**: HIGH
- **근본 원인**:
  - 인덱스가 생성된 컬럼에 대해 `ALTER TABLE ... RENAME COLUMN old_name TO new_name` 실행 시 `sch.Columns`만 갱신되고 `sch.Indexes[j].Columns` 및 `IncludeCols`는 이전 컬럼명을 유지.
  - 스토리지 엔진 커밋 시 `index.Validate`에서 `column not found: old_name on table t_ren` 에러를 반환하며 DDL 실패.
- **재현 쿼리**:
  ```sql
  CREATE TABLE t_ren (id INT PRIMARY KEY, old_name VARCHAR(20));
  CREATE INDEX idx_old ON t_ren (old_name);
  ALTER TABLE t_ren RENAME COLUMN old_name TO new_name;
  ```
- **기대 동작**: 인덱스 정의와 함께 원자적으로 컬럼명 변경 완료.
- **실제 동작**: `ERROR: column not found: old_name on table t_ren` 트랜잭션 실패.
- **해결 방안**: `sqlexec_ddl.go`의 컬럼 리네임 콜백에서 해당 테이블의 모든 보조 인덱스 및 Include 컬럼 참조명을 일괄 동기화.

---

## 12. 통합 회귀 방지 테스트 스위트 매핑 (100% PASS)

- `pkg/engine/audit_round1_to_6_supplemental_test.go`: Round 1 ~ 6 핵심 결함 회귀 테스트 (12개 서브테스트 PASS)
- `pkg/engine/audit_round4_regression_test.go`: Round 4 브래킷/해시조인/3VL/복합PK MERGE 회귀 테스트 (PASS)
- `pkg/engine/audit_round7_round8_test.go`: Round 7 & 8 Arity, 날짜 검증, 윈도우 프레임 회귀 테스트 (PASS)
- `pkg/engine/audit_round9_test.go`: Round 9 보안(SEC-001~009) 및 트랜잭션(TX-001~008) 회귀 테스트 (PASS)
- `pkg/engine/audit_round10_test.go`: Round 10 CBO/커서/프로시저(ENG-001~010) 회귀 테스트 (PASS)
- `pkg/engine/audit_round11_test.go`: Round 11 시퀀스/Option B 인덱스/충돌(ENG-011~017) 회귀 테스트 (PASS)
- `pkg/engine/audit_round12_test.go`: Round 12 JSON/XML/CONVERT(ENG-018~022) 회귀 테스트 (PASS)
- `pkg/engine/audit_round13_test.go`: Round 13 지연역직렬화/서브쿼리/카디널리티/윈도우프레임(ENG-023~027) 회귀 테스트 (PASS)
- `pkg/execution/aggregate_round13_test.go`: Round 13 LazyTuple 해시집계 격리 단위 테스트 (PASS)
- `pkg/wire/tds/audit_round14_test.go`: Round 14 PLP 대용량 직렬화/스칼라 메타데이터/인덱스 동기화(ENG-028~030) 회귀 테스트 (PASS)
- `pkg/engine/spec_driven_batch1_test.go` ~ `batch3_test.go`: 표준 SQL 및 트랜잭션 불변식 스위트 (79개 테스트 PASS)
