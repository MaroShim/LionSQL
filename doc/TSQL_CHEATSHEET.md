# LionSQL T-SQL Function & Syntax 3-Second Cheatsheet (`doc/TSQL_CHEATSHEET.md`)

> - **Created**: 2026-11-12
> - **Last Updated**: 2026-11-12
> - **Target Release**: LionSQL v1.0-GA
> - **Korean Version**: [doc/TSQL_CHEATSHEET.ko.md](TSQL_CHEATSHEET.ko.md)

---

This document provides a single-page condensed cheatsheet of core T-SQL functions, system variables, and procedural control flow constructs sorted alphabetically (A-Z) for ultra-fast lookup.

---

## 1. T-SQL Built-in Scalar & Aggregate Functions (A-Z)

| Function | Return Type | Introduced | Syntax & Example | Recommended Alternative |
|---|---|---|---|---|
| **`ABS(n)`** | `NUMERIC` | `v0.1.0` | `SELECT ABS(-42);` ➔ `42` | - |
| **`ACOS(n)` / `ASIN(n)` / `ATAN(n)`** | `FLOAT` | `v0.8.0` | `SELECT ACOS(0.5);` | - |
| **`APPROX_COUNT_DISTINCT(col)`** | `BIGINT` | `v0.8.0` | `SELECT APPROX_COUNT_DISTINCT(user_id) FROM logs;` | `COUNT(DISTINCT col)` |
| **`ASCII(char)`** | `INT` | `v0.1.0` | `SELECT ASCII('A');` ➔ `65` | - |
| **`AVG(col)`** | `NUMERIC/FLOAT` | `v0.1.0` | `SELECT AVG(salary) FROM emp;` | - |
| **`CAST(val AS type)`** | Target Type | `v0.1.0` | `SELECT CAST('123' AS INT);` ➔ `123` | - |
| **`CEILING(n)`** | `NUMERIC` | `v0.1.0` | `SELECT CEILING(4.2);` ➔ `5` | - |
| **`CHAR(code)`** | `VARCHAR` | `v0.1.0` | `SELECT CHAR(65);` ➔ `'A'` | - |
| **`CHARINDEX(sub, str [, start])`** | `INT` | `v0.1.0` | `SELECT CHARINDEX('bar', 'foobar');` ➔ `4` | `POSITION` (PG) |
| **`CHOOSE(index, val1, val2, ...)`** | `ANY` | `v0.8.0` | `SELECT CHOOSE(2, 'A', 'B', 'C');` ➔ `'B'` | `CASE ... WHEN` |
| **`COALESCE(v1, v2, ...)`** | `ANY` | `v0.1.0` | `SELECT COALESCE(NULL, NULL, 'OK');` ➔ `'OK'` | `ISNULL` |
| **`COMPRESS(string)`** | `VARBINARY` | `v0.92.0` | `SELECT COMPRESS('Large text data');` | Generates GZIP varbinary |
| **`CONCAT(s1, s2, ...)`** | `VARCHAR` | `v0.1.0` | `SELECT CONCAT('A', '-', 'B');` ➔ `'A-B'` | `s1 + s2` |
| **`CONCAT_WS(sep, s1, s2, ...)`** | `VARCHAR` | `v0.8.0` | `SELECT CONCAT_WS(',', 'A', 'B', 'C');` ➔ `'A,B,C'` | - |
| **`CONVERT(type, val [, style])`** | Target Type | `v0.1.0` | `SELECT CONVERT(VARCHAR, GETDATE(), 120);` | `CAST` |
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

## 2. T-SQL System Session Variables (`@@VAR`)

| Variable | Return Type | Description & Default Value |
|---|---|---|
| **`@@VERSION`** | `NVARCHAR` | LionSQL engine build information and Microsoft SQL Server compatibility banner |
| **`@@DATEFIRST`** | `TINYINT` | Start day of week (Default: `7` = Sunday) |
| **`@@DBTS`** | `VARBINARY(8)` | Monotonically increasing database timestamp (Rowversion) |
| **`@@IDENTITY`** | `NUMERIC` | Most recent `IDENTITY` column value inserted in the current session |
| **`@@ROWCOUNT`** | `INT` | Number of rows affected or returned by the previous SQL statement |
| **`@@ERROR`** | `INT` | Error code of the previous statement (`0` on success) |
| **`@@TRANCOUNT`** | `INT` | Active transaction nesting depth (`+1` on `BEGIN TRAN`, `-1` on `COMMIT`) |
| **`@@SPID`** | `SMALLINT` | Current client connection session process ID (SPID) |
| **`@@SERVERNAME`** | `NVARCHAR` | Current LionSQL server instance name |

---

## 3. T-SQL Control Flow & Procedural Syntax Cheatsheet

```sql
-- 1. Variable Declaration and Assignment
DECLARE @UserId INT = 100;
DECLARE @TotalSalary DECIMAL(18, 2);
SET @TotalSalary = (SELECT SUM(salary) FROM emp WHERE dept_id = @UserId);

-- 2. Conditional Branching (IF ... ELSE)
IF @TotalSalary > 1000000
BEGIN
    PRINT 'High Department Budget';
END
ELSE
BEGIN
    PRINT 'Standard Budget';
END

-- 3. Loop Iteration (WHILE ... BREAK / CONTINUE)
DECLARE @Counter INT = 0;
WHILE @Counter < 5
BEGIN
    SET @Counter = @Counter + 1;
    IF @Counter = 3 CONTINUE;
    IF @Counter > 4 BREAK;
END

-- 4. Exception Handling (TRY ... CATCH)
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
