# LionSQL 통합 슈퍼셋 SQL 호환성 가이드 (`doc/SQL_COMPATIBILITY_GUIDE.ko.md`)

> - **최초 작성일**: 2026-10-29  
> - **최종 갱신일**: 2026-11-20  
> [English Version](SQL_COMPATIBILITY_GUIDE.md)  
>  
> - **문서 상태**: 정식 지원 (Production Ready / 100% Verified)  
> - **코어 아키텍처 원칙**:  
>   - `[Invariant 17]`: 단일 통합 SQL 문법 & 프로토콜 무분기 불변식 (`[RULE:ZeroProtocolBranch]`)  
>   - `[Invariant 4]`: 단일 정적 바이너리 (TDS `:1433`, PGWire `:5433`, HTTP `:8090`)  
>   - `[RULE:RadicalKISS]`: 불필요한 방언 모드 지옥 배제 및 MySQL 고유 엔진 기능 영구 기각(Spec-Out)  
> - **회귀 테스트**: `pkg/engine/sqlexec_mysql_syntax_test.go`, `pkg/proc/builtins_test.go`, `pkg/proc/runtime/cursor_test.go`  

---

## 1. 개요 및 단일 통합 슈퍼셋 철학 (Single Unified Superset Philosophy)

LionSQL은 코어 실행 엔진으로 고성능 T-SQL VDBE 바이트코드 가상 머신과 비차단 ACID 스토리지(In-Memory OCC / WiredTiger MVCC)를 채택하고 있습니다.  
LionSQL의 공식 지원 프로토콜 및 코어 SQL 방언은 **Microsoft SQL Server T-SQL (TDS `:1433`)**과 **PostgreSQL (PGWire v3 `:5433`)**입니다.

LionSQL의 **단일 통합 슈퍼셋(Unified Superset) 아키텍처**는 특정 프로토콜이나 방언 모드(`sql_mode`)에 따라 코드가 갈라지는 파편화를 원천 차단하며, **어떤 클라이언트로 접속하든 T-SQL과 PostgreSQL의 편리한 문법을 100% 동일하게 수용**합니다.

---

## 2. 필수 실용 호환 내장 함수 (Core Practical Compatibility Functions)

개발자가 다중 RDBMS 환경에서 자주 사용하는 핵심 스칼라 함수들을 네이티브 별칭 및 빌트인으로 제공하여 타이핑 오류를 방지하고 프레임워크 쿼리를 매끄럽게 수용합니다.

### 2.1. NULL 제어 함수: `IFNULL(a, b)` & `NVL(a, b)`
- **동작**: 첫 번째 인자 `a`가 NULL이면 `b`를 반환하고, NULL이 아니면 `a`를 반환합니다. (T-SQL `ISNULL(a, b)`의 고성능 제로-코스트 별칭)
- **예시**:
  ```sql
  SELECT id, IFNULL(nickname, 'Guest') FROM users;
  SELECT id, NVL(bonus, 0) FROM employees;
  SELECT id, COALESCE(phone, email, 'None') FROM members;
  ```

### 2.2. 표준 날짜/시간 함수: `CURRENT_DATE`, `CURDATE()`, `CURRENT_TIME`, `CURTIME()`
- **동작**:
  - `CURRENT_DATE`, `CURDATE()`: 시분초 없이 오늘 날짜(`'YYYY-MM-DD'`)만 반환
  - `CURRENT_TIME`, `CURTIME()`: 날짜 없이 현재 시각(`'HH:MM:SS'`)만 반환
- **예시**:
  ```sql
  SELECT CURRENT_DATE AS today, CURRENT_TIME AS now_time;
  ```

### 2.3. 나머지 연산 함수: `MOD(n, m)`
- **동작**: `n % m` 나머지 값을 반환합니다. (수학 식별자 및 ORM 함수 호출 호환)
- **예시**:
  ```sql
  SELECT MOD(10, 3); -- 결과: 1
  ```

> **보조 다중 방언 편의 함수 (Secondary Compatibility Functions)**:  
> LionSQL은 하위 호환 및 타 DB 마이그레이션 편의를 위해 `TRUNC(x, [d])` / `TRUNCATE(x, d)`, `STRPOS(str, sub)` / `INSTR(str, sub)`, `SHA256(str)`, `HEX(str)` / `UNHEX(hex)` 등도 엔진 수준에서 투명하게 지원합니다 (상세 매핑은 [섹션 6](#6-rdbms-방언별-퀵-매핑-요약-quick-reference-matrix) 참조).

---

## 3. T-SQL 단방향 읽기 전용 커서 (`PROC-CURSOR-1`)

레거시 Microsoft SQL Server 저장 프로시저의 70~80%에 존재하는 순차 행 순회 로직을 수정 없이 그대로 실행할 수 있도록 **"단방향 순차 읽기 전용 커서(Fast Forward-Only Read-Only Cursor)"**를 완벽 지원합니다.

### 3.1. 지원 문법 및 수명주기 5대 명령문
1. `DECLARE <cur_name> CURSOR FOR <select_statement>`: 커서 정의
2. `OPEN <cur_name>`: 결과 집합 활성화 및 인덱스 초기화
3. `FETCH NEXT FROM <cur_name> INTO @var1, @var2, ...`: 다음 행 순회 및 변수 대입
4. `CLOSE <cur_name>`: 커서 메모리 즉시 해제
5. `DEALLOCATE <cur_name>`: 세션 커서 맵에서 완전 삭제

### 3.2. 표준 실무 커서 루프 패턴
```sql
CREATE PROCEDURE ProcessAccountBalances
AS
BEGIN
    DECLARE @acc_id INT;
    DECLARE @acc_bal DECIMAL(18,2);

    DECLARE acc_cur CURSOR FOR
        SELECT id, balance FROM accounts WHERE balance > 0;

    OPEN acc_cur;
    FETCH NEXT FROM acc_cur INTO @acc_id, @acc_bal;

    WHILE @@FETCH_STATUS = 0
    BEGIN
        PRINT 'Processing Account ID: ' + CAST(@acc_id AS VARCHAR(20));
        FETCH NEXT FROM acc_cur INTO @acc_id, @acc_bal;
    END;

    CLOSE acc_cur;
    DEALLOCATE acc_cur;
END;
```

---

## 4. PostgreSQL 데이터 변경 CTE (Writable CTE: `WITH ... INSERT/UPDATE/DELETE ... RETURNING`)

LionSQL은 PostgreSQL 표준의 **데이터 변경 CTE(Writable CTE)** 및 `::` 타입 캐스트 연산자를 단일 통합 문법으로 지원하여 복합 트랜잭션을 단일 라운드트립으로 처리합니다.

```sql
WITH upd_inv AS (
    UPDATE inventories
    SET reserved_quantity = reserved_quantity + $2,
        updated_by = $3,
        updated_at = CURRENT_TIMESTAMP
    WHERE item_id = $1
      AND (quantity - reserved_quantity) >= $2
    RETURNING item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
), ins_tx AS (
    INSERT INTO inventory_transactions (
        id, item_id, warehouse_id, transaction_type, quantity, balance_after, reference_id, created_by, updated_by
    )
    SELECT $4, item_id, $5, 'RESERVE', $2, (quantity - reserved_quantity), $3, $3, $3
    FROM upd_inv
)
SELECT item_id, warehouse_id, quantity, reserved_quantity, safety_stock_threshold, created_at, created_by, updated_at, updated_by
FROM upd_inv;
```

---

## 5. 경량 편의 문법 및 타 DB(MySQL 등) 호환성/기각 명세

LionSQL은 웹 ORM(GORM, Prisma, TypeORM, MyBatis)이나 오픈소스 마이그레이션 DDL 스크립트에서 유입되는 개발자의 타이핑 피로도를 줄이기 위해 극히 일부의 **비침습적 경량 편의 문법(Lightweight Ergonomics)**만을 단일 슈퍼셋 파서에 포함하고 있습니다.  
동시에 불필요한 복잡도 증가를 방지하기 위해 **MySQL 고유의 무거운 엔진 및 프로토콜 기능은 영구 기각(Spec-Out)**되었습니다.

### 5.1. 단일 파서에 수용된 경량 손끝 편의 문법 (Lightweight Ergonomics)

#### 1) 백틱(Backtick) 식별자 인용 (`` `column` ``)
- **동작**: 렉서 레벨에서 백틱(`` `...` ``)을 T-SQL의 대괄호(`[...]`) 및 ANSI/PostgreSQL의 큰따옴표(`"..."`)와 완전히 동등한 식별자(Identifier)로 투명하게 처리합니다.
- **공식 권장 식별자 규격**:
  - **T-SQL 공식 권장**: 대괄호 (`[table]`, `[column]`)
  - **ANSI / PostgreSQL 공식 권장**: 큰따옴표 (`"table"`, `"column"`)
- **예시**:
  ```sql
  SELECT `id`, `name`, `balance` FROM `accounts` WHERE `status` = 'ACTIVE';
  ```

#### 2) 단촐한 스키마 조회: `DESC` / `DESCRIBE <table>`
- **동작**: 테이블의 컬럼명, 데이터 타입, Null 허용 여부, 기본키(PK) 여부, 기본값을 6컬럼 정규 테이블 그리드로 반환합니다.
- **예시**:
  ```sql
  DESC accounts;
  -- 또는
  DESCRIBE accounts;
  ```

#### 3) 직관적 카탈로그 탐색: `SHOW TABLES`
- **동작**: 현재 데이터베이스 카탈로그에 존재하는 사용자 테이블 목록을 직관적으로 반환합니다.
- **예시**:
  ```sql
  SHOW TABLES;
  ```

### 5.2. 영구 기각(Spec-Out)된 MySQL 기능 명세 (Negative Knowledge)
LionSQL은 엔터프라이즈 OLTP 무결성(`[RULE:RadicalKISS]`, `doc/SPEC_OUT_DECISIONS.ko.md`, `doc/DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md`)을 위해 다음 기능들을 영구 배제하였습니다:

1. **MySQL 전용 와이어 프로토콜 (TCP `:3306`) 기각**:
   - 와이어 리스너는 엔터프라이즈 표준인 TDS(`:1433`)와 PGWire(`:5433`)로 단일화하며, 제3의 와이어 리스너 증설을 영구 불허합니다.
2. **MySQL 고유 DML (`ON DUPLICATE KEY UPDATE`, `REPLACE INTO`) 기각**:
   - PostgreSQL 표준 `ON CONFLICT DO UPDATE` 및 ANSI/T-SQL 표준 `MERGE` 문이 이미 완비되어 있으므로, 충돌 처리 경로가 3개로 분기되어 복잡도가 폭증하는 것을 원천 차단합니다.
3. **8열 평면 레거시 `EXPLAIN` 기각**:
   - MySQL 5.x 시절의 납작한 8열 테이블형 EXPLAIN은 계층형 CBO 트리 구조를 훼손하므로 배제하고, 표준 인덴트 텍스트 및 JSON 플랜으로 일원화합니다.
4. **MySQL 스타일 `sql_mode` 설정 분기 기각**:
   - 설정에 따라 문법 해석이 달라지는 '모드 지옥(Configuration Hell)'을 배제하고 모든 연결에서 일관된 슈퍼셋 파서를 유지합니다.

---

## 6. RDBMS 방언별 퀵 매핑 요약 (Quick Reference Matrix)

> **안내**: 아래 표의 MySQL/Oracle 열은 타 DB에서 LionSQL로 마이그레이션하는 개발자의 참조용(Reference Only)이며, LionSQL의 코어 공식 지원 대상은 **T-SQL과 PostgreSQL의 단일 통합 슈퍼셋**입니다.

| 작업 목적 | LionSQL 공식 권장 표기 | MS SQL Server (T-SQL) | PostgreSQL | MySQL / Oracle (참조용 편의 지원) |
| :--- | :--- | :--- | :--- | :--- |
| **식별자 인용** | `[id]` 또는 `"id"` | `[id]` | `"id"` | `` `id` `` (백틱 투명 허용) |
| **스키마 확인** | `DESC tbl;` / `sp_help tbl` | `sp_help tbl` | `\d tbl` / `DESC tbl;` | `DESC tbl;` |
| **카탈로그 목록** | `SHOW TABLES;` | `SELECT * FROM sys.tables` | `\dt` | `SHOW TABLES;` |
| **NULL 대체** | `ISNULL(a, b)` / `COALESCE` | `ISNULL(a, b)` | `COALESCE(a, b)` | `IFNULL(a, b)` / `NVL(a, b)` |
| **오늘 날짜** | `CURRENT_DATE` / `GETDATE()` | `CAST(GETDATE() AS DATE)` | `CURRENT_DATE` | `CURDATE()` / `TRUNC(SYSDATE)` |
| **나머지 연산** | `%` 또는 `MOD(n, m)` | `%` | `%` 또는 `MOD(n, m)` | `MOD(n, m)` |
| **소수점 버림** | `TRUNC(x, d)` | `ROUND(x, d, 1)` | `TRUNC(x, d)` | `TRUNCATE(x, d)` |
| **문자열 위치** | `CHARINDEX()` / `INSTR()` | `CHARINDEX(sub, str)` | `STRPOS(str, sub)` | `INSTR(str, sub)` |
| **행 순회 커서**| `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `CURSOR cur IS` |
