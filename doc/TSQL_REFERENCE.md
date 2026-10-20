# LionSQL T-SQL & SQL Syntax Reference Guide

This document provides the definitive syntax specification and usage reference for LionSQL's SQL and Microsoft T-SQL procedural dialect.

---

## 1. Supported Data Types

LionSQL supports the following core SQL and T-SQL primitive types:

| Type Name | Synonyms | Storage Representation | Default Literal Format | Nullable |
|---|---|---|---|---|
| `INT` | `INTEGER`, `INT4` | 64-bit signed integer | `100`, `-42` | Yes |
| `VARCHAR` | `NVARCHAR`, `TEXT`, `STRING` | UTF-8 variable-length string | `'Alice'`, `'Hello World'` | Yes |
| `FLOAT` | `DOUBLE`, `FLOAT8`, `REAL` | IEEE 754 64-bit float | `3.14159`, `-0.05` | Yes |
| `BOOL` | `BOOLEAN`, `BIT` | 1-byte boolean flag | `TRUE`, `FALSE`, `1`, `0` | Yes |
| `DATETIME` | `TIMESTAMP` | RFC3339 UTC timestamp | `'2024-09-22T12:00:00Z'` | Yes |
| `JSON` | `JSONB` | JSON binary representation (`storage.TypeJSON = 6`) | `'{"user":"Alice","score":95}'` | Yes |

### NULL Semantics
- `NULL` represents an absent or unknown value.
- Any arithmetic operation on `NULL` yields `NULL` (`10 + NULL = NULL`).
- Comparison with `NULL` using `=` or `!=` evaluates to unknown (false). Use `IS NULL` or `IS NOT NULL`.
- In sorting (`ORDER BY`), `NULL` values sort as smaller than any non-null value in ascending order.

---

## 2. Data Definition Language (DDL)

### 2.1. Creating Tables & Constraints

```sql
CREATE TABLE departments (
    id INT PRIMARY KEY,
    name VARCHAR NOT NULL
);

CREATE TABLE employees (
    id INT IDENTITY(1,1) PRIMARY KEY,
    dept_id INT NOT NULL,
    name VARCHAR NOT NULL,
    metadata JSON,
    created_at DATETIME NOT NULL,
    FOREIGN KEY (dept_id) REFERENCES departments(id) 
        ON DELETE CASCADE ON UPDATE CASCADE
);
```

- **Primary Key**: Exactly one primary key column per table.
- **Identity Column**: `IDENTITY(seed, increment)` or `AUTO_INCREMENT` auto-generates monotonic integers.
- **Foreign Keys**: `FOREIGN KEY ... REFERENCES ... [ON DELETE <action>] [ON UPDATE <action>]` supports `CASCADE`, `SET NULL`, `RESTRICT`, and `NO ACTION`.

### 2.2. Dropping Tables

```sql
DROP TABLE employees;
DROP TABLE IF EXISTS employees;
```

### 2.3. Truncating Tables

```sql
TRUNCATE TABLE employees;
```
- Fast $O(1)$ reset of table records, row versions, and secondary indexes while preserving table schema and foreign key definitions.

### 2.4. Temporary Tables (`#temp` and `##global_temp`)

```sql
-- Session-local temporary table (isolated per connection, automatically dropped on disconnect)
CREATE TABLE #temp_scores (
    user_id INT PRIMARY KEY,
    score INT
);

-- Global temporary table (shared across concurrent sessions, dropped when creator disconnects)
CREATE TABLE ##global_config (
    key VARCHAR PRIMARY KEY,
    val VARCHAR
);
```

### 2.5. Secondary Indexes

LionSQL supports unique and non-unique secondary indexes on single or multiple columns:

```sql
-- Create non-unique secondary index
CREATE INDEX idx_employees_dept ON employees (dept_id);

-- Create unique secondary index
CREATE UNIQUE INDEX idx_departments_name ON departments (name);

-- Drop secondary index
DROP INDEX idx_employees_dept ON employees;
```

---

## 3. Data Manipulation Language (DML)

### 3.1. INSERT

```sql
-- Single-row insert
INSERT INTO accounts (id, holder, balance, created_at)
VALUES (1, 'Alice', 1000, '2024-09-22T10:00:00Z');

-- Multi-row insert in a single atomic transaction
INSERT INTO accounts (id, holder, balance, created_at)
VALUES 
    (2, 'Bob', 500, '2024-09-22T10:05:00Z'),
    (3, 'Charlie', 750, '2024-09-22T10:10:00Z');
```

### 3.2. UPDATE

```sql
-- Update with predicate
UPDATE accounts 
SET balance = balance + 200, tier = 'Gold' 
WHERE id = 1;

-- Multi-row update
UPDATE accounts 
SET balance = balance * 1.05 
WHERE balance < 1000;
```

### 3.3. DELETE

```sql
-- Delete with predicate
DELETE FROM accounts WHERE id = 3;

-- Delete all rows
DELETE FROM accounts;
```

### 3.4. SELECT ... INTO

Creates and populates a new permanent or temporary table from a query result:

```sql
-- Populate session-scoped local temporary table
SELECT id, holder, balance INTO #vip_accounts 
FROM accounts 
WHERE balance >= 1000;

-- Populate permanent table
SELECT id, holder, tier INTO accounts_backup 
FROM accounts;
```

### 3.5. Table Variables (`DECLARE @table TABLE`)

Declares an in-memory session-isolated table variable:

```sql
DECLARE @cart TABLE (
    item_id INT PRIMARY KEY,
    quantity INT NOT NULL,
    price FLOAT NOT NULL
);

INSERT INTO @cart VALUES (101, 2, 29.99), (102, 1, 15.50);

SELECT item_id, quantity * price AS total FROM @cart;
```

---

## 4. Relational Queries (DQL)

LionSQL executes relational queries using a streaming Volcano Iterator Pipeline with Cost-Based Optimization.

### 4.1. Filtering & Projection

```sql
SELECT id, holder, balance * 1.1 AS projected_balance
FROM accounts
WHERE balance >= 500 AND (holder = 'Alice' OR holder LIKE 'B%');
```

### 4.2. Joins (Hash Join & Index Nested Loop Join)

LionSQL supports `INNER JOIN`, `LEFT OUTER JOIN`, and `RIGHT OUTER JOIN`. The CBO evaluates whether to construct an in-memory hash join or probe secondary indexes:

```sql
SELECT a.id, a.holder, o.order_id, o.amount
FROM accounts a
INNER JOIN orders o ON a.id = o.user_id
WHERE o.amount > 100.0;
```

### 4.3. Aggregation (`GROUP BY` & `HAVING`)

Supported aggregate functions: `COUNT(*)`, `COUNT(col)`, `SUM(col)`, `AVG(col)`, `MIN(col)`, `MAX(col)`.

```sql
SELECT tier, COUNT(*) AS total_users, AVG(balance) AS avg_balance
FROM accounts
GROUP BY tier
HAVING COUNT(*) > 10;
```

### 4.4. Sorting, Memory Governor & Pagination

LionSQL bounds memory consumption during sorting. When accumulated rows exceed `work_mem`, an **External Merge Sort** automatically spills chunks to disk and merges them via an N-way Min-Heap:

```sql
-- Configure session-specific sort memory limit
SET work_mem = '64MB';

-- Query with multi-column order and pagination
SELECT id, holder, balance
FROM accounts
ORDER BY balance DESC, holder ASC
LIMIT 50 OFFSET 100;
```

### 4.5. Common Table Expressions (CTE) & Subqueries

```sql
-- Common Table Expression (WITH)
WITH HighValueAccounts AS (
    SELECT id, holder, balance
    FROM accounts
    WHERE balance >= 1000
)
SELECT h.holder, COUNT(o.order_id) AS orders_count
FROM HighValueAccounts h
LEFT JOIN orders o ON h.id = o.user_id
GROUP BY h.holder;

-- Subquery with IN and scalar evaluation
SELECT holder, balance
FROM accounts
WHERE id IN (SELECT user_id FROM orders WHERE amount > 500);
```

### 4.6. Window Functions

LionSQL supports ANSI/ISO SQL and T-SQL window ranking and offset functions via physical `WindowIterator`:

```sql
SELECT id, dept_id, salary,
       ROW_NUMBER() OVER (PARTITION BY dept_id ORDER BY salary DESC) AS rank_in_dept,
       RANK() OVER (ORDER BY salary DESC) AS overall_rank,
       DENSE_RANK() OVER (ORDER BY salary DESC) AS dense_rank,
       LAG(salary, 1) OVER (PARTITION BY dept_id ORDER BY salary) AS prev_salary,
       LEAD(salary, 1) OVER (PARTITION BY dept_id ORDER BY salary) AS next_salary
FROM employees;
```

### 4.7. JSON & JSONB Extraction Operators and Functions

```sql
-- PostgreSQL arrow extraction operators
SELECT metadata -> 'profile' ->> 'email' AS email FROM employees;

-- ANSI/T-SQL JSON extraction functions
SELECT JSON_VALUE(metadata, '$.profile.name') AS full_name,
       JSON_QUERY(metadata, '$.skills') AS skills_array
FROM employees
WHERE JSON_VALUE(metadata, '$.profile.city') = 'Seoul';
```

---

## 5. T-SQL Stored Procedures

Stored procedures are compiled by LionSQL into native register-based VDBE bytecode programs and executed under ambient ACID transactions.

### 5.1. Procedure Syntax & Local Variables

```sql
CREATE PROCEDURE TransferFunds
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    DECLARE @sender_bal INT;
    DECLARE @receiver_bal INT;

    -- SELECT INTO variable assignment
    SELECT balance INTO @sender_bal FROM accounts WHERE id = @from;

    -- Conditional logic
    IF @sender_bal < @amount
    BEGIN
        RAISERROR('Insufficient funds in sender balance');
        RETURN -1;
    END;

    -- Atomic updates within ambient transaction
    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;

    PRINT 'Funds successfully transferred!';
    RETURN 0;
END;
```

### 5.2. Invoking Procedures

```sql
-- Via SQL statement
EXEC TransferFunds @from = 1, @to = 2, @amount = 250;

-- Via TDS / MSSQL RPC wire call
-- Handled transparently by TDS protocol listener on port 1433
```

### 5.3. Error Handling (`TRY...CATCH`)

```sql
CREATE PROCEDURE SafeAccountCredit
    @acc_id INT,
    @amount INT
AS
BEGIN
    BEGIN TRY
        IF @amount <= 0
        BEGIN
            RAISERROR('Credit amount must be positive');
        END;

        UPDATE accounts SET balance = balance + @amount WHERE id = @acc_id;
        RETURN 0;
    END TRY
    BEGIN CATCH
        PRINT 'Transaction failed during credit operation';
        RETURN -1;
    END CATCH;
END;
```

### 5.4. Loops (`WHILE`)

```sql
CREATE PROCEDURE BulkAccrueInterest
    @rate FLOAT,
    @iterations INT
AS
BEGIN
    DECLARE @i INT = 0;

    WHILE @i < @iterations
    BEGIN
        UPDATE accounts SET balance = balance * (1.0 + @rate);
        SET @i = @i + 1;
    END;

    RETURN @i;
END;
```

### 5.5. Cursors

```sql
CREATE PROCEDURE AuditAllAccounts
AS
BEGIN
    DECLARE @id INT;
    DECLARE @holder VARCHAR;
    DECLARE @bal INT;

    DECLARE acc_cursor CURSOR FOR
    SELECT id, holder, balance FROM accounts;

    OPEN acc_cursor;
    FETCH NEXT FROM acc_cursor INTO @id, @holder, @bal;

    WHILE @@FETCH_STATUS = 0
    BEGIN
        IF @bal < 0
        BEGIN
            PRINT 'Overdrawn account detected: ' + @holder;
        END;
        FETCH NEXT FROM acc_cursor INTO @id, @holder, @bal;
    END;

    CLOSE acc_cursor;
    DEALLOCATE acc_cursor;
END;
```

### 5.6. Flow Control: `GOTO` & Labels

LionSQL supports unconditional forward and backward jumps via `GOTO`:

```sql
CREATE PROCEDURE ProcessQueue
AS
BEGIN
    DECLARE @retries INT = 0;

RetryPoint:
    IF @retries > 3
        GOTO AbortPoint;

    -- Attempt processing operation...
    SET @retries = @retries + 1;
    GOTO RetryPoint;

AbortPoint:
    PRINT 'Max retries exceeded';
    RETURN -1;
END;
```

---

## 6. User-Defined Functions (UDF)

### 6.1. Scalar UDF

Scalar functions return a single value of a specified data type:

```sql
CREATE FUNCTION CalculateTax (
    @income FLOAT,
    @tax_rate FLOAT
)
RETURNS FLOAT
AS
BEGIN
    DECLARE @tax FLOAT;
    SET @tax = @income * @tax_rate;
    RETURN @tax;
END;
```

### 6.2. Table-Valued Functions (TVF)

Table-valued functions return a structured relation:

```sql
CREATE FUNCTION GetTopAccountsByTier (
    @target_tier VARCHAR,
    @min_bal INT
)
RETURNS TABLE (
    id INT,
    holder VARCHAR,
    balance INT
)
AS
BEGIN
    SELECT id, holder, balance
    FROM accounts
    WHERE tier = @target_tier AND balance >= @min_bal;
END;
```

---

## 7. Table Triggers & Virtual Delta Tables

LionSQL supports event-driven triggers attached to tables with `AFTER` and `INSTEAD OF` timing for `INSERT`, `UPDATE`, and `DELETE`. Triggers have access to virtual delta relations:
- **`INSERTED`**: Contains new rows being inserted or updated.
- **`DELETED`**: Contains superseded rows being updated or deleted.

```sql
CREATE TRIGGER trg_audit_balance_update
ON accounts
AFTER UPDATE
AS
BEGIN
    -- Log balance updates where delta exceeds 1000
    INSERT INTO audit_log (account_id, old_balance, new_balance, updated_at)
    SELECT d.id, d.balance, i.balance, '2024-09-22T12:00:00Z'
    FROM DELETED d
    INNER JOIN INSERTED i ON d.id = i.id
    WHERE i.balance != d.balance;
END;
```

---

## 8. Query Optimization & Diagnostic Commands

### 8.1. `ANALYZE TABLE`

Collects statistics (row count, NDV, null count, equi-depth histograms) for the Cost-Based Optimizer:

```sql
ANALYZE TABLE accounts;
ANALYZE TABLE orders;
```

### 8.2. `EXPLAIN` and `EXPLAIN ANALYZE`

- `EXPLAIN <query>`: Displays estimated physical operator tree with cardinality and cost estimates.
- `EXPLAIN ANALYZE <query>`: Executes query and outputs estimated costs alongside actual wall-clock execution time and rows processed per operator.

```sql
EXPLAIN ANALYZE
SELECT a.holder, SUM(o.amount)
FROM accounts a
JOIN orders o ON a.id = o.user_id
WHERE a.balance > 500
GROUP BY a.holder;
```

### 8.3. `EXPLAIN BYTECODE`

Disassembles any SQL query (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) or procedure into its compiled VDBE bytecode instruction stream:

```sql
EXPLAIN BYTECODE
SELECT id, holder FROM accounts WHERE balance > 1000;
```

### 8.4. Session Configuration Variables

```sql
-- View active session settings
SHOW work_mem;
SHOW search_path;
SHOW transaction_isolation;
SELECT @@work_mem;

-- Modify session settings
SET work_mem = '128MB';
SET NOCOUNT ON;
SET ANSI_NULLS ON;
SET QUOTED_IDENTIFIER ON;
SET TRANSACTION ISOLATION LEVEL READ COMMITTED;
SET IDENTITY_INSERT accounts ON;
```

---

## 9. Database Diagnostics & System Stored Procedures

### 9.1. DBCC Commands

- **`DBCC USEROPTIONS`**: Returns active session isolation levels, text size, and ANSI settings.
- **`DBCC CHECKTABLE ('table_name')`**: Scans table rows, asserts primary key uniqueness, and validates secondary index point-lookup integrity.
- **`DBCC CHECKDB`**: Executes consistency checks across all user tables and indexes in the catalog.
- **`DBCC SHRINKDATABASE ('db_name')`**: Reports page allocations and file sizes.

### 9.2. Administrative Stored Procedures

LionSQL provides native implementations of Microsoft SQL Server administrative procedures:

- **`sp_help ['object_name']`**: Introspects table schema, columns, nullable status, and primary/secondary indexes.
- **`sp_spaceused ['object_name']`**: Reports table row counts, data storage footprint, and index allocations.
- **`sp_who` / `sp_who2`**: Monitors active client connections, session IDs (`spid`), and current executing status.
- **`sp_lock`**: Inspects active table read and write locks across transactions.
- **`sp_rename 'old_name', 'new_name' [, 'COLUMN']`**: Renames a table or column atomically with schema catalog synchronization.

---

## 10. Built-in Scalar Functions

### 10.1. String Functions
- `STUFF(string, start, length, new_string)`: Deletes `length` characters from `start` (1-based) and inserts `new_string`.
- `CHARINDEX(substring, string [, start_location])`: Locates 1-based character position.
- `REPLACE(string, old_string, new_string)`: Case-insensitive replacement.
- `REVERSE(string)`: Reverses multi-byte UTF-8 string.
- `CONCAT(str1, str2, ...)`: Null-safe string concatenation.
- `LEFT(string, n)` / `RIGHT(string, n)`: Extracts leading or trailing `n` runes.
- `LTRIM(string)` / `RTRIM(string)`: Trims leading or trailing whitespace.

### 10.2. Date & Time Functions
- `GETDATE()` / `NOW()` / `SYSDATETIME()`: Current UTC timestamp.
- `DATEADD(datepart, number, date)`: Adds interval (`year`, `month`, `day`, `hour`, `minute`, `second`).
- `DATEDIFF(datepart, startdate, enddate)`: Difference in units between two dates.
- `YEAR(date)` / `MONTH(date)` / `DAY(date)`: Integer date component extraction.

### 10.3. Mathematical & Logical Functions
- `POWER(base, exp)`, `SQRT(val)`, `SIGN(val)`, `PI()`
- `IIF(condition, true_val, false_val)`: Inline conditional expression.
- `CHOOSE(index, val1, val2, ...)`: 1-based element selection.

### 10.4. System Metadata & Variables
- `NEWID()`: Generates RFC 4122 UUID v4.
- `SCOPE_IDENTITY()` / `@@IDENTITY`: Last generated identity value within current scope.
- `@@ROWCOUNT`: Number of rows affected by previous DML or query.
- `@@ERROR`: Returns `0` on success or non-zero error code.
- `@@SPID`: Current connection session ID.
- `@@TRANCOUNT`: Active transaction nesting depth.
- `@@VERSION`: LionSQL build version, engine type, and OS platform.
