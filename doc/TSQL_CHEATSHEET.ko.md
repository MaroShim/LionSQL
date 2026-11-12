# LionSQL T-SQL 함수 및 문법 3초 검색 치트시트 (`doc/TSQL_CHEATSHEET.ko.md`)

> - **최초 작성일**: 2026-11-12
> - **최종 갱신일**: 2026-11-12
> - **버전 기준**: LionSQL v1.0-GA
> - **영문 버전**: [doc/TSQL_CHEATSHEET.md](TSQL_CHEATSHEET.md)

---

이 문서는 개발자가 일상적으로 쿼리 및 프로시저를 작성할 때 필요한 핵심 T-SQL 함수, 제어문, 시스템 변수를 알파벳(A~Z) 순서로 3초 만에 빠르게 검색할 수 있도록 정리한 1페이지 압축 레퍼런스입니다.

---

## 1. T-SQL 내장 스칼라 및 집계 함수 A-Z 일람

| 함수명 | 반환 타입 | 지원 버전 | 문법 및 간략 예시 | 미지원 시 대체 권장안 |
|---|---|---|---|---|
| **`ABS(n)`** | `NUMERIC` | `v0.1.0` | `SELECT ABS(-42);` ➔ `42` | - |
| **`ACOS(n)` / `ASIN(n)` / `ATAN(n)`** | `FLOAT` | `v0.8.0` | `SELECT ACOS(0.5);` | - |
| **`APPROX_COUNT_DISTINCT(col)`** | `BIGINT` | `v0.8.0` | `SELECT APPROX_COUNT_DISTINCT(user_id) FROM logs;` | `COUNT(DISTINCT col)` |
| **`ASCII(char)`** | `INT` | `v0.1.0` | `SELECT ASCII('A');` ➔ `65` | - |
| **`AVG(col)`** | `NUMERIC/FLOAT` | `v0.1.0` | `SELECT AVG(salary) FROM emp;` | - |
| **`CAST(val AS type)`** | 지정 타입 | `v0.1.0` | `SELECT CAST('123' AS INT);` ➔ `123` | - |
| **`CEILING(n)`** | `NUMERIC` | `v0.1.0` | `SELECT CEILING(4.2);` ➔ `5` | - |
| **`CHAR(code)`** | `VARCHAR` | `v0.1.0` | `SELECT CHAR(65);` ➔ `'A'` | - |
| **`CHARINDEX(sub, str [, start])`** | `INT` | `v0.1.0` | `SELECT CHARINDEX('bar', 'foobar');` ➔ `4` | `POSITION` (PG) |
| **`CHOOSE(index, val1, val2, ...)`** | `ANY` | `v0.8.0` | `SELECT CHOOSE(2, 'A', 'B', 'C');` ➔ `'B'` | `CASE ... WHEN` |
| **`COALESCE(v1, v2, ...)`** | `ANY` | `v0.1.0` | `SELECT COALESCE(NULL, NULL, 'OK');` ➔ `'OK'` | `ISNULL` |
| **`COMPRESS(string)`** | `VARBINARY` | `v0.92.0` | `SELECT COMPRESS('Large text data');` | GZIP 압축 바이너리 생성 |
| **`CONCAT(s1, s2, ...)`** | `VARCHAR` | `v0.1.0` | `SELECT CONCAT('A', '-', 'B');` ➔ `'A-B'` | `s1 + s2` |
| **`CONCAT_WS(sep, s1, s2, ...)`** | `VARCHAR` | `v0.8.0` | `SELECT CONCAT_WS(',', 'A', 'B', 'C');` ➔ `'A,B,C'` | - |
| **`CONVERT(type, val [, style])`** | 지정 타입 | `v0.1.0` | `SELECT CONVERT(VARCHAR, GETDATE(), 120);` | `CAST` |
| **`COS(n)` / `COT(n)`** | `FLOAT` | `v0.8.0` | `SELECT COS(0);` ➔ `1.0` | - |
| **`COUNT(col / *)`** | `BIGINT` | `v0.1.0` | `SELECT COUNT(*) FROM orders;` | - |
| **`CURRENT_TIMESTAMP`** | `DATETIME` | `v0.1.0` | `SELECT CURRENT_TIMESTAMP;` | `GETDATE()` |
| **`DATEADD(part, num, date)`** | `DATETIME` | `v0.1.0` | `SELECT DATEADD(day, 7, GETDATE());` | - |
| **`DATEDIFF(part, start, end)`** | `BIGINT` | `v0.1.0` | `SELECT DATEDIFF(day, '2026-01-01', '2026-01-10');` ➔ `9` | - |
| **`DATENAME(part, date)`** | `VARCHAR` | `v0.8.0` | `SELECT DATENAME(month, '2026-11-12');` ➔ `'November'` | - |
| **`DATEPART(part, date)`** | `INT` | `v0.1.0` | `SELECT DATEPART(year, GETDATE());` ➔ `2026` | `YEAR()`, `MONTH()` |
| **`DAY(date)`** | `INT` | `v0.1.0` | `SELECT DAY('2026-11-12');` ➔ `12` | `DATEPART(day, ...)` |
| **`DB_NAME()`** | `VARCHAR` | `v0.5.0` | `SELECT DB_NAME();` ➔ `'master'` | `CURRENT_DATABASE()` |
| **`DECOMPRESS(varbinary)`** | `VARBINARY` | `v0.92.0` | `SELECT CAST(DECOMPRESS(COMPRESS('hello')) AS VARCHAR);` | `CAST(DECOMPRESS(...) AS VARCHAR)` |
| **`DENSE_RANK() OVER (...)`** | `BIGINT` | `v0.8.0` | `SELECT DENSE_RANK() OVER (ORDER BY score DESC) FROM t;` | - |
| **`EOMONTH(date [, add_months])`** | `DATE` | `v0.8.0` | `SELECT EOMONTH('2026-02-10');` ➔ `'2026-02-28'` | - |
| **`EXP(n)`** | `FLOAT` | `v0.8.0` | `SELECT EXP(1);` ➔ `2.71828...` | - |
| **`FLOOR(n)`** | `NUMERIC` | `v0.1.0` | `SELECT FLOOR(4.9);` ➔ `4` | - |
| **`FORMAT(val, fmt)`** | `VARCHAR` | `v0.8.0` | `SELECT FORMAT(1234567, '#,###');` | - |
| **`GETDATE()`** | `DATETIME` | `v0.1.0` | `SELECT GETDATE();` | `CURRENT_TIMESTAMP` |
| **`GETUTCDATE()`** | `DATETIME` | `v0.5.0` | `SELECT GETUTCDATE();` | `SYSUTCDATETIME()` |
| **`IIF(cond, t_val, f_val)`** | `ANY` | `v0.1.0` | `SELECT IIF(1 > 0, 'YES', 'NO');` ➔ `'YES'` | `CASE WHEN cond THEN ...` |
| **`ISDATE(expr)`** | `INT` (0/1) | `v0.8.0` | `SELECT ISDATE('2026-11-12');` ➔ `1` | `TRY_CAST` |
| **`ISJSON(json_str)`** | `INT` (0/1) | `v0.6.0` | `SELECT ISJSON('{"id": 1}');` ➔ `1` | - |
| **`ISNULL(check_expr, repl)`** | `ANY` | `v0.1.0` | `SELECT ISNULL(NULL, 'Default');` ➔ `'Default'` | `COALESCE` |
| **`ISNUMERIC(expr)`** | `INT` (0/1) | `v0.8.0` | `SELECT ISNUMERIC('123.45');` ➔ `1` | `TRY_CAST` |
| **`JSON_MODIFY(json, path, val)`**| `VARCHAR` | `v0.6.0` | `SELECT JSON_MODIFY('{"a":1}', '$.b', 2);` | - |
| **`JSON_QUERY(json [, path])`** | `VARCHAR` | `v0.6.0` | `SELECT JSON_QUERY('{"a":[1,2]}', '$.a');` | - |
| **`JSON_VALUE(json, path)`** | `VARCHAR` | `v0.6.0` | `SELECT JSON_VALUE('{"user":"kim"}', '$.user');` ➔ `'kim'` | - |
| **`LEFT(str, len)`** | `VARCHAR` | `v0.1.0` | `SELECT LEFT('LionSQL', 4);` ➔ `'Lion'` | `SUBSTRING` |
| **`LEN(str)`** | `INT` | `v0.1.0` | `SELECT LEN('LionSQL');` ➔ `7` | `LENGTH` |
| **`LOWER(str)`** | `VARCHAR` | `v0.1.0` | `SELECT LOWER('LION');` ➔ `'lion'` | - |
| **`LTRIM(str)`** | `VARCHAR` | `v0.1.0` | `SELECT LTRIM('   abc');` ➔ `'abc'` | `TRIM` |
| **`MAX(col)` / `MIN(col)`** | `ANY` | `v0.1.0` | `SELECT MAX(price), MIN(price) FROM products;` | - |
| **`MONTH(date)`** | `INT` | `v0.1.0` | `SELECT MONTH('2026-11-12');` ➔ `11` | `DATEPART(month, ...)` |
| **`NCHAR(code)`** | `NVARCHAR`| `v0.5.0` | `SELECT NCHAR(44032);` ➔ `'가'` | `CHAR` |
| **`NULLIF(e1, e2)`** | `ANY` | `v0.1.0` | `SELECT NULLIF(10, 10);` ➔ `NULL` | `CASE WHEN e1=e2 THEN NULL` |
| **`OBJECT_ID(name)`** | `INT` | `v0.5.0` | `SELECT OBJECT_ID('accounts');` | - |
| **`OBJECT_NAME(id)`** | `VARCHAR` | `v0.5.0` | `SELECT OBJECT_NAME(1001);` | - |
| **`POWER(base, exp)`** | `FLOAT` | `v0.1.0` | `SELECT POWER(2, 8);` ➔ `256.0` | - |
| **`RAND([seed])`** | `FLOAT` | `v0.8.0` | `SELECT RAND();` ➔ `0.6543...` | - |
| **`RANK() OVER (...)`** | `BIGINT` | `v0.8.0` | `SELECT RANK() OVER (ORDER BY score DESC) FROM t;` | - |
| **`REPLACE(str, from, to)`** | `VARCHAR` | `v0.1.0` | `SELECT REPLACE('abc', 'b', 'x');` ➔ `'axc'` | - |
| **`REPLICATE(str, count)`** | `VARCHAR` | `v0.1.0` | `SELECT REPLICATE('A', 3);` ➔ `'AAA'` | `REPEAT` |
| **`REVERSE(str)`** | `VARCHAR` | `v0.1.0` | `SELECT REVERSE('abc');` ➔ `'cba'` | - |
| **`RIGHT(str, len)`** | `VARCHAR` | `v0.1.0` | `SELECT RIGHT('LionSQL', 3);` ➔ `'SQL'` | `SUBSTRING` |
| **`ROUND(n, len [, func])`** | `NUMERIC` | `v0.1.0` | `SELECT ROUND(123.456, 2);` ➔ `123.46` | - |
| **`ROW_NUMBER() OVER (...)`** | `BIGINT` | `v0.8.0` | `SELECT ROW_NUMBER() OVER (ORDER BY id) FROM users;` | - |
| **`RTRIM(str)`** | `VARCHAR` | `v0.1.0` | `SELECT RTRIM('abc   ');` ➔ `'abc'` | `TRIM` |
| **`SERVERPROPERTY(prop)`** | `SQL_VARIANT` | `v0.5.0` | `SELECT SERVERPROPERTY('ProductVersion');` | - |
| **`SIGN(n)`** | `INT` | `v0.1.0` | `SELECT SIGN(-15);` ➔ `-1` | - |
| **`SPACE(count)`** | `VARCHAR` | `v0.1.0` | `SELECT 'A' + SPACE(3) + 'B';` ➔ `'A   B'` | `REPLICATE(' ', n)` |
| **`SQRT(n)`** | `FLOAT` | `v0.1.0` | `SELECT SQRT(16);` ➔ `4.0` | - |
| **`STRING_AGG(col, sep)`** | `VARCHAR` | `v0.8.0` | `SELECT STRING_AGG(name, ', ') FROM users;` | - |
| **`STUFF(str, start, len, new)`** | `VARCHAR` | `v0.5.0` | `SELECT STUFF('abcdef', 2, 3, 'XYZ');` ➔ `'aXYZef'` | - |
| **`SUBSTRING(str, start, len)`**| `VARCHAR` | `v0.1.0` | `SELECT SUBSTRING('LionSQL', 5, 3);` ➔ `'SQL'` | - |
| **`SUM(col)`** | `NUMERIC` | `v0.1.0` | `SELECT SUM(amount) FROM tx;` | - |
| **`TRIM(str)`** | `VARCHAR` | `v0.1.0` | `SELECT TRIM('   abc   ');` ➔ `'abc'` | - |
| **`UPPER(str)`** | `VARCHAR` | `v0.1.0` | `SELECT UPPER('lion');` ➔ `'LION'` | - |
| **`YEAR(date)`** | `INT` | `v0.1.0` | `SELECT YEAR('2026-11-12');` ➔ `2026` | `DATEPART(year, ...)` |

---

## 2. T-SQL 시스템 세션 변수 일람 (`@@VAR`)

| 변수명 | 반환 타입 | 설명 및 기본값 |
|---|---|---|
| **`@@VERSION`** | `NVARCHAR` | LionSQL 엔진 빌드 정보 및 Microsoft SQL Server 호환 식별 배너 |
| **`@@DATEFIRST`** | `TINYINT` | 주의 시작 요일 (기본값: `7` = 일요일 기준) |
| **`@@DBTS`** | `VARBINARY(8)` | 데이터베이스 전역 단조 증가 타임스탬프 (Rowversion) |
| **`@@IDENTITY`** | `NUMERIC` | 현재 세션에서 가장 최근에 INSERT된 `IDENTITY` 자동 증가 컬럼 값 |
| **`@@ROWCOUNT`** | `INT` | 직전 SQL 문장에 의해 영향받거나 반환된 행(Row) 수 |
| **`@@ERROR`** | `INT` | 직전 문장의 실행 에러 코드 (정상 시 `0`) |
| **`@@TRANCOUNT`** | `INT` | 현재 세션에 활성화된 트랜잭션의 중첩 깊이 (`BEGIN TRAN` 시 +1, `COMMIT` 시 -1) |
| **`@@SPID`** | `SMALLINT` | 현재 클라이언트 연결 세션 프로세스 ID (SPID) |
| **`@@SERVERNAME`** | `NVARCHAR` | 현재 LionSQL 인스턴스 서버 이름 |

---

## 3. T-SQL 제어 흐름 및 절차 구문 치트시트

```sql
-- 1. 변수 선언 및 할당
DECLARE @UserId INT = 100;
DECLARE @TotalSalary DECIMAL(18, 2);
SET @TotalSalary = (SELECT SUM(salary) FROM emp WHERE dept_id = @UserId);

-- 2. 조건 분기 (IF ... ELSE)
IF @TotalSalary > 1000000
BEGIN
    PRINT 'High Department Budget';
END
ELSE
BEGIN
    PRINT 'Standard Budget';
END

-- 3. 반복문 (WHILE ... BREAK / CONTINUE)
DECLARE @Counter INT = 0;
WHILE @Counter < 5
BEGIN
    SET @Counter = @Counter + 1;
    IF @Counter = 3 CONTINUE;
    IF @Counter > 4 BREAK;
END

-- 4. 예외 처리 (TRY ... CATCH)
BEGIN TRY
    BEGIN TRANSACTION;
    UPDATE accounts SET balance = balance - 100 WHERE id = 1;
    COMMIT TRANSACTION;
END TRY
BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK TRANSACTION;
    PRINT ERROR_MESSAGE();
END CATCH;
```
