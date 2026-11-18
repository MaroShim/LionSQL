# LionSQL 단일 통합 슈퍼셋 SQL 가이드 (`doc/UNIFIED_SQL_SUPERSET_GUIDE.ko.md`)

> **한 줄 요약**: LionSQL에서는 PostgreSQL이나 SQL Server 중 어떤 클라이언트로 접속하든 상관없이, 두 데이터베이스의 편리한 문법을 손에 익은 그대로 자유롭게 섞어 쓸 수 있습니다.

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

## 2. 한눈에 보는 대표 호환 문법 비교

두 데이터베이스 진영에서 널리 쓰이는 주요 표현들을 LionSQL에서는 **모두 기본 지원**합니다.

| 기능 | SQL Server 스타일 | PostgreSQL 스타일 | LionSQL에서의 동작 |
| :--- | :--- | :--- | :--- |
| **결과 행 제한 (Paging)** | `TOP 10` | `LIMIT 10 OFFSET 20` | **둘 다 지원** (혼용 가능) |
| **데이터 타입 변환** | `CAST(v AS INT)` | `v::int` | **둘 다 지원** |
| **현재 시각 구하기** | `GETDATE()`, `SYSDATETIME()` | `NOW()`, `CURRENT_TIMESTAMP` | **둘 다 지원** |
| **NULL 대체** | `ISNULL(a, b)` | `COALESCE(a, b)` | **둘 다 지원** (`IFNULL`, `NVL`도 지원) |
| **문자열 검색 (대소문자 무시)** | `LIKE '%abc%'` (기본 대소문자 무시) | `ILIKE '%abc%'` | **둘 다 지원** |
| **JSON 필드 조회** | `JSON_VALUE(data, '$.name')` | `data->>'name'` | **둘 다 지원** |
| **테이블/컬럼 감싸기** | `[order details]` | `"order details"` | **둘 다 지원** (`` `backtick` ``도 지원) |
| **자동 증가 번호 (PK)** | `id INT IDENTITY(1,1)` | `id SERIAL` 또는 `nextval()` | **둘 다 지원** |
| **트랜잭션 시작** | `BEGIN TRANSACTION` | `BEGIN` | **둘 다 지원** |

---

## 3. 대표적인 활용 예시

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

### 3) 편리한 대소문자 무시 검색: `ILIKE`
사용자 이메일이나 이름을 검색할 때 대소문자 변환 함수(`LOWER()`)를 일일이 씌우지 않아도 됩니다.

```sql
-- 대소문자를 구분하지 않고 깔끔하게 검색
SELECT * FROM users WHERE email ILIKE '%@GMAIL.COM';

-- 기존 LIKE도 당연히 정상 동작합니다
SELECT * FROM users WHERE status LIKE 'ACTIVE';
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

## 4. 가장 멋진 점: "한 쿼리 안에서 섞어 써도 됩니다"

LionSQL에서는 문법을 엄격히 양자택일할 필요가 없습니다.  
하나의 쿼리 문장 안에서 SQL Server와 PostgreSQL의 편리한 표현을 자연스럽게 조합해서 작성할 수 있습니다:

```sql
SELECT 
    [user_id], 
    profile->>'nickname' AS nickname,
    ISNULL(points, 0)::int AS points
FROM "user_accounts"
WHERE email ILIKE '%@company.com'
ORDER BY created_at DESC
LIMIT 10;
```

위 쿼리는:
- 식별자로 `[user_id]`(SQL Server)와 `"user_accounts"`(Postgres)를 섞어 썼고,
- JSON 추출에 `->>`(Postgres)를 썼으며,
- NULL 처리에 `ISNULL()`(SQL Server)을 썼고,
- 타입 변환에 `::int`(Postgres)를 썼으며,
- 검색에 `ILIKE`(Postgres), 페이징에 `LIMIT 10`(Postgres)을 썼습니다.

LionSQL은 이 모든 것을 아무런 설정 변경이나 프로토콜 제약 없이 **단 하나의 자연스러운 쿼리로 매끄럽게 처리**합니다.

---

## 5. 결론: 개발자를 배려하는 편안한 데이터베이스

- **마이그레이션이 편안합니다**: 기존 시스템에서 쓰던 쿼리를 LionSQL로 옮겨올 때 문법을 일일이 고치느라 시간을 낭비할 필요가 없습니다.
- **다양한 클라이언트가 즉시 붙습니다**: 회사 내에서 누구는 DBeaver(PostgreSQL 드라이버)를 쓰고, 누구는 SSMS(SQL Server 드라이버)를 써도 동일한 데이터베이스에서 각자의 손에 익은 방식으로 작업할 수 있습니다.
- **개발 생산성이 올라갑니다**: "이 DB에서는 문자열 자를 때 함수 이름이 뭐였더라?", "날짜 가져오는 함수가 뭐였지?" 고민할 필요 없이 머릿속에 떠오른 첫 번째 함수를 입력하면 작동합니다.
