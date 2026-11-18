# LionSQL 라이브 엔진 감사 및 실무 호환성 검증 보고서 (`notes/reports/20261119_LIVE_AUDIT_REPORT.ko.md`)

> - **작성일자**: 2026-11-19
> - **감사 대상**: LionSQL Database Server (Live Daemon v0.93.0, Port 5433 / WiredTiger)
> - **감사 방법론**: psql 클라이언트를 통한 실무 사용자 관점 라이브 쿼리 및 고난도 엣지 케이스 정밀 감사
> - **조치 상태**: 신규 핵심 결함 10건 식별 및 스프린트 백로그 등록 진행

---

## 1. 총괄 개요

본 보고서는 2026-11-19 라이브 구동 중인 LionSQL 인스턴스(포트 5433, WiredTiger 스토리지 엔진)를 대상으로, 기존 단위 테스트 스위트에 의존하지 않고 실제 사용자 및 애플리케이션 개발자 관점에서 `psql` 클라이언트를 통해 T-SQL Stored Procedure와 ANSI SQL 융합 기능을 창의적인 실무 시나리오로 직접 테스트하며 발굴한 신규 핵심 결함(총 10건) 및 호환성 분석 결과입니다.

---

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
