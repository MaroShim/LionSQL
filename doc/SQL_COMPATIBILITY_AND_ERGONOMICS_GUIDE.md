# LionSQL Unified Superset SQL Compatibility & Developer Ergonomics Guide (`doc/SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.md`)

> - **Created**: 2026-11-02  
> - **Last Updated**: 2026-11-18  
> [한국어 문서](SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md)  
>  
> - **Document Status**: Production Ready / 100% Verified  
> - **Core Architectural Principles**:  
>   - `[Invariant 17]`: Single Unified SQL Grammar & Zero-Protocol-Branch Invariant (`[RULE:ZeroProtocolBranch]`)  
>   - `[Invariant 4]`: Single Static Binary (TDS `:1433`, PGWire `:5433`, HTTP `:8090`)  
>   - `[RULE:RadicalKISS]`: Avoid configuration mode hell; permanently Spec-Out heavy MySQL-specific engine features  
> - **Regression Tests**: `pkg/engine/sqlexec_mysql_syntax_test.go`, `pkg/proc/builtins_test.go`, `pkg/proc/runtime/cursor_test.go`  

---

## 1. Overview & Single Unified Superset Philosophy

LionSQL is powered by a high-concurrency T-SQL VDBE bytecode virtual machine and non-blocking ACID storage engines (In-Memory OCC and WiredTiger MVCC).  
LionSQL's official core dialects and wire listeners are **Microsoft SQL Server T-SQL (TDS `:1433`)** and **PostgreSQL (PGWire v3 `:5433`)**.

LionSQL's **Single Unified Superset Architecture** permanently prevents UX fragmentation caused by protocol-dependent branching (`IsPostgres()`) or dialect mode toggles (`sql_mode`). Regardless of which client or port you connect with, LionSQL's parser accepts and executes the convenient syntax of both T-SQL and PostgreSQL identically.

---

## 2. Lightweight Syntax Ergonomics & MySQL Spec-Out Boundary

To minimize typing friction from web ORMs (GORM, Prisma, TypeORM, MyBatis) and open-source migration DDL scripts, LionSQL incorporates a strictly bounded set of **non-invasive, lightweight ergonomics** within the unified superset parser.  
**Heavy MySQL-specific engine subsystems, wire protocols, and execution modes are permanently Spec-Out.**

### 2.1. Permanently Spec-Out MySQL Features (Negative Knowledge)
In adherence to enterprise OLTP integrity (`[RULE:RadicalKISS]`, `doc/SPEC_OUT_DECISIONS.ko.md`, `doc/DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md`), the following MySQL features are permanently excluded:

1. **MySQL Wire Protocol (TCP `:3306`) Rejected**:
   - Wire listeners are strictly standardized on enterprise standards: TDS (`:1433`) and PGWire (`:5433`). A 3rd wire listener is permanently rejected.
2. **MySQL-Specific DML (`ON DUPLICATE KEY UPDATE`, `REPLACE INTO`) Rejected**:
   - PostgreSQL-standard `ON CONFLICT DO UPDATE` and ANSI/T-SQL `MERGE` are fully implemented. Supporting a 3rd conflicting upsert branch is permanently rejected to avoid combinatorial complexity.
3. **8-Column Legacy Flat `EXPLAIN` Rejected**:
   - MySQL 5.x tabular 8-column EXPLAIN breaks hierarchical CBO plan trees. LionSQL standardizes on indented text trees and JSON query plans.
4. **MySQL-Style `sql_mode` Configuration Branching Rejected**:
   - LionSQL eliminates dialect configuration hell; a single predictable unified superset parser runs across all sessions.

### 2.2. Lightweight Ergonomics Accepted in the Unified Parser

#### 1) Backtick Identifier Quoting (`` `column` ``)
- **Behavior**: At the zero-allocation lexer level, backtick-quoted identifiers (`` `...` ``) are treated identically to T-SQL square brackets (`[...]`) and ANSI/PostgreSQL double quotes (`"..."`).
- **Official Recommendations**:
  - **T-SQL Standard**: Square brackets (`[table]`, `[column]`)
  - **ANSI / PostgreSQL Standard**: Double quotes (`"table"`, `"column"`)
- **Example**:
  ```sql
  SELECT `id`, `name`, `balance` FROM `accounts` WHERE `status` = 'ACTIVE';
  ```

#### 2) Concise Schema Inspection: `DESC` / `DESCRIBE <table>`
- **Behavior**: Returns column names, data types, nullability, primary key status, default values, and extra attributes in a normalized 6-column tabular grid.
- **Example**:
  ```sql
  DESC accounts;
  -- or
  DESCRIBE accounts;
  ```

#### 3) Intuitive Catalog Discovery: `SHOW TABLES`
- **Behavior**: Lists all user tables in the current database catalog.
- **Example**:
  ```sql
  SHOW TABLES;
  ```

---

## 3. High-Value Cross-RDBMS Built-in Functions Best 7

LionSQL provides native built-in support and aliases for the most frequently used scalar functions across relational databases.

### 3.1. NULL Handling: `IFNULL(a, b)`, `NVL(a, b)`, `COALESCE(a, b)`
- **Behavior**: Returns `b` if `a` is `NULL`; otherwise returns `a` (identical to T-SQL `ISNULL(a, b)`).
- **Example**:
  ```sql
  SELECT id, IFNULL(nickname, 'Guest') FROM users;
  SELECT id, NVL(bonus, 0) FROM employees;
  SELECT id, COALESCE(phone, email, 'None') FROM members;
  ```

### 3.2. Date & Time: `CURDATE()`, `CURRENT_DATE`, `CURTIME()`, `CURRENT_TIME`
- **Behavior**:
  - `CURDATE()`, `CURRENT_DATE`: Returns current date (`'YYYY-MM-DD'`) without time.
  - `CURTIME()`, `CURRENT_TIME`: Returns current time (`'HH:MM:SS'`) without date.
- **Example**:
  ```sql
  SELECT CURDATE() AS today, CURRENT_TIME AS now_time;
  ```

### 3.3. Modulo Arithmetic: `MOD(n, m)`
- **Behavior**: Returns the remainder of `n % m`.
- **Example**:
  ```sql
  SELECT MOD(10, 3); -- Result: 1
  ```

### 3.4. Decimal Truncation: `TRUNC(x, [d])` & `TRUNCATE(x, d)`
- **Behavior**: Truncates `x` to `d` decimal places without rounding (`ROUND`).
- **Example**:
  ```sql
  SELECT TRUNC(123.456, 1);    -- Result: 123.4
  SELECT TRUNCATE(123.456, 2); -- Result: 123.45
  ```

### 3.5. Substring Position: `INSTR(str, sub)`
- **Behavior**: Returns the 1-based index of `sub` in `str` (identical to T-SQL `CHARINDEX(sub, str)`).
- **Example**:
  ```sql
  SELECT INSTR('hello world', 'world'); -- Result: 7
  ```

### 3.6. One-Way Hash & Hex: `SHA256(str)`, `HEX(str)`, `UNHEX(hex)`
- **Behavior**:
  - `SHA256('abc')`: 64-char lowercase SHA-256 hex string (shortcut for `HASHBYTES('SHA2_256', ...)`).
  - `HEX('abc')`: Encodes string to hex.
  - `UNHEX('616263')`: Decodes hex string to raw string/bytes.
- **Example**:
  ```sql
  SELECT SHA256('secret');
  SELECT HEX('LionSQL'); -- Result: 4C696F6E53514C
  ```

---

## 4. T-SQL Forward-Only Read-Only Cursors (`PROC-CURSOR-1`)

To execute legacy Microsoft SQL Server stored procedures without modification, LionSQL provides native support for fast forward-only read-only cursors.

### 4.1. Lifecycle Statements
1. `DECLARE <cur> CURSOR FOR <select>`: Cursor declaration
2. `OPEN <cur>`: Result set snapshot initialization
3. `FETCH NEXT FROM <cur> INTO @v1, @v2, ...`: Sequential row advance
4. `CLOSE <cur>`: Immediate result set deallocation
5. `DEALLOCATE <cur>`: Session cursor map deregistration

### 4.2. Standard Cursor Loop Pattern
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

## 5. PostgreSQL Writable CTEs (`WITH ... INSERT/UPDATE/DELETE ... RETURNING`)

LionSQL natively supports PostgreSQL-style data-modifying CTEs and `::` type casts within the single unified grammar.

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

## 6. Cross-RDBMS Quick Reference Matrix

> **Note**: The MySQL/Oracle column below is for developer migration reference only. LionSQL's official core dialects are **T-SQL and PostgreSQL Unified Superset**.

| Goal | LionSQL Official Standard | MS SQL Server (T-SQL) | PostgreSQL | MySQL / Oracle (Reference Only) |
| :--- | :--- | :--- | :--- | :--- |
| **Identifier Quoting** | `[id]` or `"id"` | `[id]` | `"id"` | `` `id` `` (transparently accepted) |
| **Schema Inspection** | `DESC tbl;` / `sp_help tbl` | `sp_help tbl` | `\d tbl` / `DESC tbl;` | `DESC tbl;` |
| **Catalog Listing** | `SHOW TABLES;` | `SELECT * FROM sys.tables` | `\dt` | `SHOW TABLES;` |
| **NULL Substitution** | `ISNULL(a, b)` / `COALESCE` | `ISNULL(a, b)` | `COALESCE(a, b)` | `IFNULL(a, b)` / `NVL(a, b)` |
| **Current Date** | `CURRENT_DATE` / `GETDATE()` | `CAST(GETDATE() AS DATE)` | `CURRENT_DATE` | `CURDATE()` / `TRUNC(SYSDATE)` |
| **Modulo** | `%` or `MOD(n, m)` | `%` | `%` or `MOD(n, m)` | `MOD(n, m)` |
| **Truncation** | `TRUNC(x, d)` | `ROUND(x, d, 1)` | `TRUNC(x, d)` | `TRUNCATE(x, d)` |
| **String Position** | `CHARINDEX()` / `INSTR()` | `CHARINDEX(sub, str)` | `STRPOS(str, sub)` | `INSTR(str, sub)` |
| **Row Cursor** | `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `CURSOR cur IS` |
