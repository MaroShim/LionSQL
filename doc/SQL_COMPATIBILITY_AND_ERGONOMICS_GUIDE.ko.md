# LionSQL 다중 방언 SQL 호환성 및 개발자 편의 문법 가이드 (`doc/SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md`)

> - **문서 상태**: [구현 대기 (Pending Implementation)]
> - **알림**: 본 가이드에 명시된 기능(S2 MySQL 편의 문법, S3 다중 방언 함수 Best 7, S4 T-SQL 단방향 커서)은 현재 `v1.0.0` 정식 릴리스 큐(`Active S2~S4`)에 등록되어 구현 대기 중입니다. 구현 완료 즉시 본 안내 문구는 제거되고 실측 동작 기준으로 정식 활성화됩니다.
> - **연관 과제 ID**: `S-MYSQL-1-P1`, `FUNC-DIALECT-7`, `PROC-CURSOR-1`

---

## 1. 개요 및 관용적 다중 방언 철학 (Tolerant Multi-Dialect Philosophy)

LionSQL은 코어 실행 엔진으로 고성능 T-SQL VDBE 바이트코드 가상 머신과 비차단 ACID 스토리지(In-Memory OCC / WiredTiger MVCC)를 채택하고 있습니다. 그러나 실제 개발 현장에서는 MySQL, Oracle, PostgreSQL, MS SQL Server 등 다양한 RDBMS 환경에서 작성된 SQL 쿼리와 저장 프로시저가 혼재되어 사용됩니다.

LionSQL의 **관용적 다중 방언(Tolerant Multi-Dialect) 아키텍처**는 코어 스토리지 엔진의 순수성과 초고속 무락 ACID 원칙을 100% 보존하면서, 파서 및 런타임 레이어에서 **개발자의 손끝 기억(Muscle Memory)과 타 DB 관용 표현을 자연스럽게 수용**하여 불필요한 구문 오류(Syntax Error)를 방지하는 것을 목표로 합니다.

---

## 2. MySQL 스타일 손끝 편의 문법 (`S-MYSQL-1-P1`)

일상적인 쿼리 작성 및 스키마 탐색 시 개발자의 타이핑 피로도를 최소화하기 위해 MySQL 스타일의 단촐한(Concise) 편의 문법을 지원합니다.

### 2.1. 백틱(Backtick) 식별자 인용 (`` `column` ``)
- **배경**: 웹 프레임워크(GORM, Prisma, TypeORM, MyBatis)나 오픈소스 DDL 스크립트는 식별자를 백틱(`` `table` ``)으로 감싸는 경우가 많습니다.
- **동작**: 렉서 레벨에서 백틱(`` `...` ``)을 T-SQL의 대괄호(`[...]`) 및 ANSI/Postgres의 큰따옴표(`"..."`)와 완전히 동등한 식별자(Identifier)로 투명하게 처리합니다.
- **예시**:
  ```sql
  SELECT `id`, `name`, `balance` FROM `accounts` WHERE `status` = 'ACTIVE';
  ```

### 2.2. 단촐한 스키마 조회: `DESC` / `DESCRIBE <table>`
- **배경**: T-SQL의 `sp_help`나 `INFORMATION_SCHEMA`는 출력이 장황하여 터미널 CLI나 GUI 도구에서 빠르게 컬럼 타입을 확인하기 번거롭습니다.
- **동작**: 테이블의 컬럼명, 데이터 타입, Null 허용 여부, 기본키(PK) 여부, 기본값을 6컬럼 정규 테이블 그리드로 반환합니다.
- **예시**:
  ```sql
  DESC accounts;
  -- 또는
  DESCRIBE accounts;
  ```
- **출력 그리드 규격**:
  | Field | Type | Null | Key | Default | Extra |
  | :--- | :--- | :---: | :---: | :--- | :--- |
  | `id` | `INT` | NO | PRI | NULL | auto_increment |
  | `balance` | `DECIMAL(18,2)` | NO | | `0.00` | |

### 2.3. 직관적 카탈로그 탐색: `SHOW TABLES`
- **동작**: 현재 데이터베이스 카탈로그에 존재하는 사용자 테이블 목록을 직관적으로 반환합니다.
- **예시**:
  ```sql
  SHOW TABLES;
  ```

---

## 3. 초가성비 다중 방언 내장 함수 Best 7 (`FUNC-DIALECT-7`)

MySQL, Oracle, PostgreSQL 개발자가 자연스럽게 사용하는 고빈도 스칼라 함수들을 별칭 및 네이티브 빌트인으로 제공합니다.

### 3.1. NULL 제어 함수: `IFNULL(a, b)` & `NVL(a, b)`
- **출처**: `IFNULL` (MySQL/SQLite), `NVL` (Oracle)
- **동작**: 첫 번째 인자 `a`가 NULL이면 `b`를 반환하고, NULL이 아니면 `a`를 반환합니다. (T-SQL의 `ISNULL(a, b)`과 100% 동일)
- **예시**:
  ```sql
  SELECT id, IFNULL(nickname, 'Guest') FROM users;
  SELECT id, NVL(bonus, 0) FROM employees;
  ```

### 3.2. 순수 날짜/시간 함수: `CURDATE()`, `CURRENT_DATE`, `CURTIME()`, `CURRENT_TIME`
- **출처**: `CURDATE`/`CURTIME` (MySQL), `CURRENT_DATE`/`CURRENT_TIME` (ANSI SQL / PostgreSQL)
- **동작**:
  - `CURDATE()`, `CURRENT_DATE`: 시분초 없이 오늘 날짜(`'YYYY-MM-DD'`)만 반환
  - `CURTIME()`, `CURRENT_TIME`: 날짜 없이 현재 시각(`'HH:MM:SS'`)만 반환
- **예시**:
  ```sql
  SELECT CURDATE() AS today, CURRENT_TIME AS now_time;
  ```

### 3.3. 수학 및 나머지 연산 함수: `MOD(n, m)`
- **출처**: ANSI SQL 표준, Oracle, PostgreSQL, MySQL
- **동작**: `n % m` 나머지 값을 반환합니다.
- **예시**:
  ```sql
  SELECT MOD(10, 3); -- 결과: 1
  ```

### 3.4. 소수점 자릿수 버림 함수: `TRUNC(x, [d])` & `TRUNCATE(x, d)`
- **출처**: `TRUNC` (Oracle, PostgreSQL), `TRUNCATE` (MySQL)
- **동작**: 반올림(`ROUND`)하지 않고 지정된 소수점 `d`자리 미만을 버립니다.
- **예시**:
  ```sql
  SELECT TRUNC(123.456, 1);    -- 결과: 123.4
  SELECT TRUNCATE(123.456, 2); -- 결과: 123.45
  ```

### 3.5. 부분 문자열 위치 탐색 함수: `INSTR(str, sub)`
- **출처**: Oracle, MySQL, SQLite
- **동작**: `str` 문자열 내에서 `sub`가 처음 나타나는 1-based 인덱스를 반환합니다. (T-SQL `CHARINDEX(sub, str)`와 동일 동작, 인자 순서: `(전체, 찾을대상)`)
- **예시**:
  ```sql
  SELECT INSTR('hello world', 'world'); -- 결과: 7
  ```

### 3.6. 단방향 해시 및 인코딩 함수: `SHA256(str)`, `HEX(str)`, `UNHEX(hex)`
- **출처**: MySQL, MariaDB
- **동작**:
  - `SHA256('abc')`: 64자리 소문자 SHA-256 16진수 문자열 반환 (T-SQL의 `HASHBYTES('SHA2_256', ...)` 단축형)
  - `HEX('abc')`: 문자열을 16진수(`'616263'`)로 인코딩
  - `UNHEX('616263')`: 16진수 문자열을 원본 바이트/문자열(`'abc'`)로 디코딩
- **예시**:
  ```sql
  SELECT SHA256('secret');
  SELECT HEX('LionSQL'); -- 결과: 4C696F6E53514C
  ```

---

## 4. T-SQL 단방향 읽기 전용 커서 (`PROC-CURSOR-1`)

레거시 Microsoft SQL Server 저장 프로시저의 70~80%에 존재하는 순차 행 순회 로직을 수정 없이 그대로 실행할 수 있도록 **"단방향 순차 읽기 전용 커서(Fast Forward-Only Read-Only Cursor)"**를 완벽 지원합니다.

### 4.1. 지원 문법 및 수명주기 5대 명령문
1. **`DECLARE <cur_name> CURSOR FOR <select_statement>`**:
   - 커서 정의 및 결과 집합 쿼리 바인딩
2. **`OPEN <cur_name>`**:
   - 커서 쿼리를 실행하여 스냅샷 결과 집합을 활성화하고 인덱스를 첫 행 이전(`-1`)으로 초기화
3. **`FETCH NEXT FROM <cur_name> INTO @var1, @var2, ...`**:
   - 다음 행으로 커서를 전진(`Index++`)시키고 컬럼 값을 로컬 변수에 대입
4. **`CLOSE <cur_name>`**:
   - 커서 결과 행 메모리를 즉시 해제
5. **`DEALLOCATE <cur_name>`**:
   - 세션 커서 맵에서 커서 객체를 완전 삭제

### 4.2. `@@FETCH_STATUS` 상태 코드
루프 제어를 위한 시스템 전역 변수 `@@FETCH_STATUS`를 지원합니다:
- `0`: 행 패치 성공 (정상 데이터 반환)
- `-1`: 결과 집합의 끝을 지남 (EOF 도달 또는 빈 테이블)
- `-2`: 패치된 행이 누락됨

### 4.3. 표준 실무 커서 루프 패턴
```sql
CREATE PROCEDURE ProcessAccountBalances
AS
BEGIN
    DECLARE @acc_id INT;
    DECLARE @acc_bal DECIMAL(18,2);

    -- 1. 커서 선언
    DECLARE acc_cur CURSOR FOR
        SELECT id, balance FROM accounts WHERE balance > 0;

    -- 2. 커서 오픈
    OPEN acc_cur;

    -- 3. 첫 행 패치
    FETCH NEXT FROM acc_cur INTO @acc_id, @acc_bal;

    -- 4. 순회 루프
    WHILE @@FETCH_STATUS = 0
    BEGIN
        -- 비즈니스 로직 수행
        PRINT 'Processing Account ID: ' + CAST(@acc_id AS VARCHAR(20));

        -- 다음 행 패치
        FETCH NEXT FROM acc_cur INTO @acc_id, @acc_bal;
    END;

    -- 5. 정리
    CLOSE acc_cur;
    DEALLOCATE acc_cur;
END;
```

---

## 5. RDBMS 방언별 퀵 매핑 요약 (Quick Reference Matrix)

| 작업 목적 | LionSQL 권장 표기 | MS SQL Server (T-SQL) | MySQL / MariaDB | Oracle | PostgreSQL |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **식별자 인용** | `` `id` ``, `[id]` | `[id]` | `` `id` `` | `"id"` | `"id"` |
| **스키마 확인** | `DESC tbl;` | `sp_help tbl` | `DESC tbl;` | `DESC tbl;` | `\d tbl` |
| **NULL 대체** | `IFNULL(a, b)` | `ISNULL(a, b)` | `IFNULL(a, b)` | `NVL(a, b)` | `COALESCE(a, b)` |
| **오늘 날짜** | `CURDATE()` | `CAST(GETDATE() AS DATE)`| `CURDATE()` | `TRUNC(SYSDATE)`| `CURRENT_DATE` |
| **나머지 연산** | `MOD(n, m)`, `%` | `%` | `MOD(n, m)`, `%`| `MOD(n, m)` | `MOD(n, m)`, `%` |
| **소수점 버림** | `TRUNC(x, d)` | `ROUND(x, d, 1)` | `TRUNCATE(x, d)` | `TRUNC(x, d)` | `TRUNC(x, d)` |
| **문자열 위치** | `INSTR(str, sub)` | `CHARINDEX(sub, str)` | `INSTR(str, sub)`| `INSTR(str, sub)`| `STRPOS(str, sub)` |
| **행 순회 커서**| `DECLARE cur CURSOR`| `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `CURSOR cur IS` | `DECLARE cur CURSOR` |
