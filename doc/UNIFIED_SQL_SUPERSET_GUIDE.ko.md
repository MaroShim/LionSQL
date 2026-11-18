# LionSQL 단일 통합 슈퍼셋 SQL 가이드 (`doc/UNIFIED_SQL_SUPERSET_GUIDE.ko.md`)

> - **최초 작성일**: 2026-10-25
> - **최종 갱신일**: 2026-11-19
> - **상태**: 공식 가이드 및 통합 슈퍼셋 시맨틱 원칙 반영
> - **한 줄 요약**: LionSQL에서는 PostgreSQL이나 SQL Server 중 어떤 클라이언트로 접속하든 상관없이, 두 데이터베이스의 편리한 문법을 손에 익은 그대로 자유롭게 섞어 쓸 수 있습니다. 시맨틱 충돌이 발생할 때는 SQL Server라는 단단한 닻(Default Preference)을 기준으로 삼아 데이터 정합성을 철저히 지킵니다.

---

## 1. "포트가 달라도 문법은 하나입니다"

기존 데이터베이스들은 접속하는 프로토콜에 따라 문법이 엄격하게 나뉘어 있었습니다:
- SQL Server(TDS `:1433`)로 접속하면 `TOP`, `GETDATE()`, `[table]`만 써야 하고,
- PostgreSQL(PGWire `:5433`)로 접속하면 `LIMIT`, `NOW()`, `"table"`만 써야 했습니다.

LionSQL은 이 불편함을 완전히 없앴습니다.  
LionSQL의 SQL 파서는 **단일 통합 슈퍼셋(Unified Superset)**으로 동작하므로, DBeaver, psql, SSMS, 웹 스튜디오 등 어떤 도구로 접속하든 **SQL Server와 PostgreSQL의 대표적이고 유용한 문법을 모두 그대로 이해하고 실행**합니다.

```
       [PostgreSQL 클라이언트 (psql, DBeaver, Prisma)] ──:5433─┐
                                                               │
       [SQL Server 클라이언트 (SSMS, Azure Data Studio)] ─:1433─┼─▶ [LionSQL 단일 통합 엔진]
                                                               │   (모든 문법 100% 동일 실행)
       [웹 브라우저 관리도구 (Lion Web Studio)] ─────────:8090─┘
```

---

## 2. 슈퍼셋 설계 원칙: 3대 분류와 단단한 닻(Anchor)

모든 문법을 무차별적으로 섞다 보면 *"키워드는 똑같은데 두 진영의 기대 동작(시맨틱)이 정반대인 경우"*를 마주치게 됩니다. LionSQL은 복잡한 가상화나 포트 갈라치기(Babelfish 방식)로 엔진을 망가뜨리는 대신, 명쾌하고 실용적인 **3대 원칙**으로 이를 해결했습니다.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     LionSQL Pragmatic Superset Strategy                     │
├─────────────────────────────────────────────────────────────────────────────┤
│ 원칙 1. 문법(Syntax)은 언제나 관대하게 다 받아준다.                         │
│         한쪽 진영에만 있는 유용한 문법은 포트 불문 전역 수용 (Universal)     │
│                                                                             │
│ 원칙 2. 시맨틱 충돌이 생길 때는 '서버 Preference'라는 단단한 닻을 내린다.   │
│         기본 닻(Default)은 LionSQL의 본체인 SQL Server이다.                  │
│                                                                             │
│ 원칙 3. 명시적인 의도를 담은 문법은 처음부터 확실하게 수용한다.             │
│         ILIKE 처럼 의도가 100% 명확한 전용 문법은 즉시 실행                  │
└─────────────────────────────────────────────────────────────────────────────┘
```

### [Category 1] 양쪽 공통 문법 ➔ 자연스럽게 공통 실행
- 표준 `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `JOIN`, `GROUP BY`, `HAVING`, 서브쿼리 등.
- 양 진영에서 의미와 동작이 완전히 일치하므로 코어 엔진에서 아무런 갈등 없이 투명하게 실행됩니다.

### [Category 2] 한쪽 전용 문법 ➔ 포트 불문 전역 수용 (Universal Tolerance)
- 한쪽에만 존재하는 편리한 고유 문법:
  - PostgreSQL 진영: `LIMIT / OFFSET`, `RETURNING`, `ILIKE`, `::type`, `->>`, `json_build_object`
  - SQL Server 진영: `TOP`, `OUTPUT`, `OPENJSON`, `@세션변수`, `GETDATE()`
- **동작 방식**: 접속 포트에 따라 문법 에러를 내지 않습니다.
  - TDS 포트(SSMS)에서 `LIMIT 10`이나 `ILIKE`를 써도 정상 실행됩니다.
  - PGWire 포트(psql)에서 `SELECT TOP 10`이나 `@var`를 써도 정상 실행됩니다.

### [Category 3] 동일 문법 시맨틱 충돌 ➔ 서버 Preference (기본: SQL Server)
- 동일한 키워드인데 양 진영의 동작 기대치가 상충할 때, 포트마다 다른 결과를 내놓으면 데이터 정합성이 깨집니다.
- 따라서 **서버 전체 설정(`dialect_preference`)**을 단일 진실원천으로 삼습니다.
- **기본값**: LionSQL의 기본 닻인 **`sqlserver`**가 기준입니다. 기존 T-SQL 업무와 배치 프로그램의 호환성을 100% 보장합니다.

---

## 3. 대표적인 시맨틱 충돌 해결 사례: `LIKE`와 `ILIKE`

`LIKE` 연산자는 두 진영의 상식이 정면으로 충돌하는 대표적인 사례입니다:
- **T-SQL 개발자**: `LIKE 'alice%'`는 당연히 대소문자를 구분하지 않고(CI) 찾아줄 것으로 기대합니다.
- **PostgreSQL 개발자**: `LIKE 'alice%'`는 당연히 대소문자를 엄격히 구분(CS)할 것으로 기대합니다.

LionSQL은 3대 원칙에 따라 이 충돌을 가장 우아하게 해결합니다:

1. **`ILIKE`는 전역 즉시 수용 (Category 2)**:
   - `ILIKE`는 Postgres 전용 문법으로, "대소문자를 무시해 달라"는 개발자의 의도가 100% 명확합니다.
   - 기존 T-SQL 문법과 겹치지 않으므로, 포트 상관없이 처음부터 관대하게 전역 지원합니다.
2. **`LIKE`는 SQL Server 표준을 유지 (Category 3)**:
   - `LIKE`를 대소문자 구분으로 강제 변경하면 기존 수많은 T-SQL 프로시저와 쿼리의 결과가 깨집니다.
   - 서버의 기본 설정이 `sqlserver`이므로, `LIKE`는 기본적으로 대소문자를 가리지 않고(CI) 검색합니다.
3. **대소문자를 엄격히 구별하고 싶을 때는 국제 표준 `COLLATE` 사용**:
   - 대소문자를 칼같이 가려내야 할 때는 방언 꼼수가 아닌 ANSI SQL 표준인 `COLLATE` 구문을 사용합니다:
     ```sql
     -- 대소문자 구분 Collation (CS) 지정
     SELECT * FROM users WHERE name COLLATE Latin1_General_CS_AS LIKE 'Alice%';
     
     -- 바이너리 바이트 1:1 비교
     SELECT * FROM users WHERE name COLLATE Latin1_General_BIN = 'Alice';
     ```

---

## 4. 한눈에 보는 대표 호환 문법 비교 (Cheat Sheet)

두 데이터베이스 진영에서 널리 쓰이는 주요 표현들을 LionSQL에서는 **모두 기본 지원**합니다.

| 기능 | SQL Server 스타일 | PostgreSQL 스타일 | LionSQL에서의 동작 |
| :--- | :--- | :--- | :--- |
| **결과 행 제한 (Paging)** | `TOP 10` | `LIMIT 10 OFFSET 20` | **둘 다 지원** (혼용 가능) |
| **데이터 타입 변환** | `CAST(v AS INT)` | `v::int` | **둘 다 지원** |
| **현재 시각 구하기** | `GETDATE()`, `SYSDATETIME()` | `NOW()`, `CURRENT_TIMESTAMP` | **둘 다 지원** |
| **NULL 대체** | `ISNULL(a, b)` | `COALESCE(a, b)` | **둘 다 지원** (`IFNULL`, `NVL`도 지원) |
| **문자열 검색 (대소문자 무시)** | `LIKE '%abc%'` (기본 대소문자 무시) | `ILIKE '%abc%'` | **둘 다 지원** |
| **문자열 검색 (대소문자 구별)** | `COLLATE Latin1_General_CS_AS` | `COLLATE "C"` | **표준 COLLATE 지원** |
| **JSON 필드 조회** | `JSON_VALUE(data, '$.name')` | `data->>'name'` | **둘 다 지원** |
| **테이블/컬럼 감싸기** | `[order details]` | `"order details"` | **둘 다 지원** (`` `backtick` ``도 지원) |
| **자동 증가 번호 (PK)** | `id INT IDENTITY(1,1)` | `id SERIAL` 또는 `nextval()` | **둘 다 지원** |
| **트랜잭션 시작** | `BEGIN TRANSACTION` | `BEGIN` | **둘 다 지원** |

---

## 5. 대표적인 실전 활용 예시

### 1) 페이징(Paging): `TOP`과 `LIMIT / OFFSET`
원하는 방식으로 데이터를 끊어서 가져올 수 있습니다.

```sql
-- SQL Server 스타일 TOP
SELECT TOP 10 id, title FROM posts ORDER BY created_at DESC;

-- PostgreSQL 스타일 LIMIT / OFFSET
SELECT id, title FROM posts ORDER BY created_at DESC LIMIT 10 OFFSET 20;

-- SQL Server 표준 FETCH NEXT 구문도 지원
SELECT id, title FROM posts ORDER BY id OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY;
```

---

### 2) 간결한 타입 변환: `::type`과 `CAST()`
PostgreSQL 개발자들에게 가장 사랑받는 간결한 콜론 두 개(`::`) 변환을 모든 곳에서 쓸 수 있습니다.

```sql
-- 간결한 PostgreSQL 스타일 (::int, ::text, ::date)
SELECT '123'::int + 10 AS total, now()::date AS today;

-- 전통적인 SQL 표준 CAST
SELECT CAST('123' AS INT) + 10 AS total;
```

---

### 3) 패턴 검색: 편리한 `ILIKE`와 엄격한 `COLLATE`
대소문자를 무시할 때는 `ILIKE`나 기본 `LIKE`를 편하게 사용하고, 엄격한 구분이 필요할 때는 표준 `COLLATE`를 붙입니다.

```sql
-- 대소문자를 구분하지 않고 깔끔하게 검색 (PostgreSQL 전용 문법 수용)
SELECT * FROM users WHERE email ILIKE '%@GMAIL.COM';

-- 기존 SQL Server 스타일 LIKE (기본 대소문자 무시)
SELECT * FROM users WHERE status LIKE 'active';

-- 대소문자를 칼같이 구별해야 할 때 (ANSI 표준 COLLATE)
SELECT * FROM users WHERE code COLLATE Latin1_General_CS_AS LIKE 'AB%';
```

---

### 4) 손쉬운 JSON 접근: `->>` 화살표 연산자
테이블에 저장된 JSON 컬럼에서 원하는 값을 추출할 때, 긴 함수 대신 직관적인 화살표 기호를 쓸 수 있습니다.

```sql
-- PostgreSQL 스타일 화살표: 텍스트 형태로 바로 꺼내기
SELECT id, metadata->>'author' AS author FROM documents;

-- SQL Server 스타일 JSON 함수도 그대로 지원
SELECT id, JSON_VALUE(metadata, '$.author') AS author FROM documents;
```

---

### 5) 날짜와 NULL 처리: 손에 익은 함수 그대로
어떤 DB에서 넘어왔든 기존에 쓰던 함수를 그대로 사용하시면 됩니다.

```sql
-- 어떤 함수를 쓰더라도 오늘 날짜와 현재 시각을 반환합니다
SELECT GETDATE(), NOW(), CURRENT_TIMESTAMP, CURDATE();

-- NULL 값을 기본값으로 채울 때도 익숙한 이름을 쓰세요
SELECT ISNULL(phone, 'N/A') FROM users;    -- SQL Server 스타일
SELECT COALESCE(phone, 'N/A') FROM users;  -- ANSI / PostgreSQL 스타일
SELECT IFNULL(phone, 'N/A') FROM users;    -- MySQL 스타일
SELECT NVL(phone, 'N/A') FROM users;       -- Oracle 스타일
```

---

### 6) 식별자 감싸기: 대괄호, 큰따옴표, 백틱 모두 수용
공백이나 특수문자가 들어간 테이블 이름이나 예약어를 감쌀 때 클라이언트 도구가 무엇을 쓰든 에러가 나지 않습니다.

```sql
-- SQL Server 스타일 대괄호
SELECT [id], [order date] FROM [orders];

-- PostgreSQL 스타일 큰따옴표
SELECT "id", "order date" FROM "orders";

-- MySQL 스타일 백틱
SELECT `id`, `order date` FROM `orders`;
```

---

## 6. 가장 멋진 점: "한 쿼리 안에서 섞어 써도 됩니다"

LionSQL에서는 문법을 엄격히 양자택일할 필요가 없습니다.  
하나의 쿼리 문장 안에서 SQL Server와 PostgreSQL의 편리한 표현을 자연스럽게 조합해서 작성할 수 있습니다:

```sql
SELECT 
    [user_id],                                 -- SQL Server: 편리한 대괄호
    profile->>'nickname' AS nickname,          -- PostgreSQL: 직관적인 화살표 JSON 추출
    ISNULL(points, 0)::int AS points,          -- SQL Server ISNULL + PostgreSQL ::int 변환
    GETDATE() AS queried_at                    -- SQL Server: 익숙한 GETDATE()
FROM "user_accounts"                           -- PostgreSQL: 큰따옴표 식별자
WHERE email ILIKE '%@company.com'              -- PostgreSQL: 대소문자 무시 ILIKE
ORDER BY created_at DESC
LIMIT 10 OFFSET 0;                             -- PostgreSQL: 직관적인 LIMIT / OFFSET
```

위 쿼리는:
- 식별자로 `[user_id]`(SQL Server)와 `"user_accounts"`(Postgres)를 섞어 썼고,
- JSON 추출에 `->>`(Postgres)를 썼으며,
- NULL 처리에 `ISNULL()`(SQL Server)을 썼고,
- 타입 변환에 `::int`(Postgres)를 썼으며,
- 검색에 `ILIKE`(Postgres), 페이징에 `LIMIT 10`(Postgres)을 썼습니다.

LionSQL은 이 모든 것을 아무런 설정 변경이나 프로토콜 제약 없이 **단 하나의 자연스러운 쿼리로 매끄럽게 처리**합니다.

---

## 7. 결론: LionSQL이 지키는 가치

1. **개발자에게는 무한한 자유를 (Freedom of Syntax)**:
   - 손에 익은 어떤 문법이든 편안하게 써라. LionSQL 파서가 알아서 다 받아준다.
   - 마이그레이션이 편안하고, DBeaver든 SSMS든 원하는 클라이언트를 자유롭게 붙일 수 있다.
2. **데이터에는 확고한 정합성을 (Soundness of Semantics)**:
   - 시맨틱이 충돌할 때는 SQL Server라는 확고한 닻(Default Preference)을 내려 데이터의 흔들림을 막는다.
3. **아키텍처에는 극도의 단순성을 (Radical KISS)**:
   - 복잡한 포트 갈라치기나 가상화 레이어로 자멸하지 않고, 단일 진실원천 Preference에 기반한 단순하고 견고한 코어 엔진을 유지한다.
