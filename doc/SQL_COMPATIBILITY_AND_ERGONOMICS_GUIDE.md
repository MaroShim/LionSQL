# LionSQL Multi-Dialect SQL Compatibility & Developer Ergonomics Guide (`doc/SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.md`)

> [한국어 문서 (Korean Version)](SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md)
>
> - **Document Status**: Production Ready / 100% Verified
> - **Related Task IDs**: `S-MYSQL-1-P1`, `FUNC-DIALECT-7`, `PROC-CURSOR-1`
> - **Regression Tests**: `pkg/engine/sqlexec_mysql_syntax_test.go`, `pkg/proc/builtins_test.go`, `pkg/proc/runtime/cursor_test.go`

---

## 1. Overview & Tolerant Multi-Dialect Philosophy

LionSQL is powered by a high-concurrency T-SQL VDBE bytecode virtual machine and non-blocking ACID storage engines (In-Memory OCC and WiredTiger MVCC). In real-world engineering environments, however, SQL queries and stored procedures often mix conventions from MySQL, Oracle, PostgreSQL, and Microsoft SQL Server.

LionSQL's **Tolerant Multi-Dialect Architecture** preserves the purity and sub-microsecond non-blocking ACID guarantees of the core storage engine while **naturally accommodating developer muscle memory and cross-DBMS idioms** at the parser and runtime layers to prevent unnecessary syntax errors.

---

## 2. MySQL-Style Developer Ergonomics (`S-MYSQL-1-P1`)

To minimize typing friction during interactive querying and schema exploration, LionSQL supports concise MySQL-style syntax idioms.

### 2.1. Backtick Identifier Quoting (`` `column` ``)
- **Background**: Web ORMs (GORM, Prisma, TypeORM, MyBatis) and open-source DDL scripts frequently quote identifiers with backticks (`` `table` ``).
- **Behavior**: At the lexer level, backtick-quoted identifiers (`` `...` ``) are treated identically to T-SQL square brackets (`[...]`) and ANSI/PostgreSQL double quotes (`"..."`).
- **Example**:
  ```sql
  SELECT `id`, `name`, `balance` FROM `accounts` WHERE `status` = 'ACTIVE';
  ```

### 2.2. Concise Schema Inspection: `DESC` / `DESCRIBE <table>`
- **Background**: T-SQL `sp_help` and `INFORMATION_SCHEMA` queries are verbose when quickly checking column types in a terminal CLI or GUI console.
- **Behavior**: Returns column names, data types, nullability, primary key status, default values, and extra attributes in a normalized 6-column tabular grid.
- **Example**:
  ```sql
  DESC accounts;
  -- or
  DESCRIBE accounts;
  ```
- **Output Grid Format**:
  | Field | Type | Null | Key | Default | Extra |
  | :--- | :--- | :---: | :---: | :--- | :--- |
  | `id` | `INT` | NO | PRI | NULL | auto_increment |
  | `balance` | `DECIMAL(18,2)` | NO | | `0.00` | |

### 2.3. Intuitive Catalog Discovery: `SHOW TABLES`
- **Behavior**: Lists all user tables in the current database catalog.
- **Example**:
  ```sql
  SHOW TABLES;
  ```

---

## 3. High-Value Multi-Dialect Built-in Functions (`FUNC-DIALECT-7`)

LionSQL provides native built-in support and aliases for the most frequently used scalar functions across MySQL, Oracle, and PostgreSQL.

### 3.1. NULL Handling Functions: `IFNULL(a, b)` & `NVL(a, b)`
- **Origin**: `IFNULL` (MySQL/SQLite), `NVL` (Oracle)
- **Behavior**: Returns `b` if the first argument `a` is `NULL`; otherwise returns `a` (identical to T-SQL `ISNULL(a, b)`).
- **Example**:
  ```sql
  SELECT id, IFNULL(nickname, 'Guest') FROM users;
  SELECT id, NVL(bonus, 0) FROM employees;
  ```

### 3.2. Date & Time Functions: `CURDATE()`, `CURRENT_DATE`, `CURTIME()`, `CURRENT_TIME`
- **Origin**: `CURDATE`/`CURTIME` (MySQL), `CURRENT_DATE`/`CURRENT_TIME` (ANSI SQL / PostgreSQL)
- **Behavior**:
  - `CURDATE()`, `CURRENT_DATE`: Returns the current date (`'YYYY-MM-DD'`) without time components.
  - `CURTIME()`, `CURRENT_TIME`: Returns the current time (`'HH:MM:SS'`) without date components.
- **Example**:
  ```sql
  SELECT CURDATE() AS today, CURRENT_TIME AS now_time;
  ```

### 3.3. Modulo Arithmetic Function: `MOD(n, m)`
- **Origin**: ANSI SQL, Oracle, PostgreSQL, MySQL
- **Behavior**: Returns the remainder of `n % m`.
- **Example**:
  ```sql
  SELECT MOD(10, 3); -- Result: 1
  ```

### 3.4. Decimal Truncation Functions: `TRUNC(x, [d])` & `TRUNCATE(x, d)`
- **Origin**: `TRUNC` (Oracle, PostgreSQL), `TRUNCATE` (MySQL)
- **Behavior**: Truncates `x` to `d` decimal places without rounding (`ROUND`).
- **Example**:
  ```sql
  SELECT TRUNC(123.456, 1);    -- Result: 123.4
  SELECT TRUNCATE(123.456, 2); -- Result: 123.45
  ```

### 3.5. Substring Position Search: `INSTR(str, sub)`
- **Origin**: Oracle, MySQL, SQLite
- **Behavior**: Returns the 1-based index of the first occurrence of `sub` inside `str` (equivalent to T-SQL `CHARINDEX(sub, str)`, with argument order `(haystack, needle)`).
- **Example**:
  ```sql
  SELECT INSTR('hello world', 'world'); -- Result: 7
  ```

### 3.6. Cryptographic Hash & Hex Encoding Functions: `SHA256(str)`, `HEX(str)`, `UNHEX(hex)`
- **Origin**: MySQL, MariaDB
- **Behavior**:
  - `SHA256('abc')`: Returns the 64-character lowercase SHA-256 hex digest (concise shorthand for T-SQL `HASHBYTES('SHA2_256', ...)`).
  - `HEX('abc')`: Encodes a string into hexadecimal (`'616263'`).
  - `UNHEX('616263')`: Decodes a hexadecimal string back into its original bytes/string (`'abc'`).
- **Example**:
  ```sql
  SELECT SHA256('secret');
  SELECT HEX('LionSQL'); -- Result: 4C696F6E53514C
  ```

---

## 4. T-SQL Fast Forward-Only Read-Only Cursors (`PROC-CURSOR-1`)

LionSQL supports **Fast Forward-Only Read-Only Cursors** so legacy Microsoft SQL Server stored procedures that rely on row-by-row iteration can execute without modification.

### 4.1. Supported Syntax & 5 Lifecycle Statements
1. **`DECLARE <cur_name> CURSOR FOR <select_statement>`**:
   - Defines the cursor and binds its result set query.
2. **`OPEN <cur_name>`**:
   - Executes the underlying query to materialize the snapshot result set and initializes the cursor position before the first row (`-1`).
3. **`FETCH NEXT FROM <cur_name> INTO @var1, @var2, ...`**:
   - Advances the cursor to the next row (`Index++`) and assigns column values into local variables.
4. **`CLOSE <cur_name>`**:
   - Releases the materialized result set rows from memory.
5. **`DEALLOCATE <cur_name>`**:
   - Removes the cursor definition from the session cursor map.

### 4.2. `@@FETCH_STATUS` Status Codes
LionSQL supports the `@@FETCH_STATUS` system variable for loop control:
- `0`: The `FETCH` statement was successful.
- `-1`: The `FETCH` statement failed or the row was beyond the result set (EOF or empty table).
- `-2`: The row fetched is missing.

### 4.3. Standard Cursor Loop Pattern
```sql
CREATE PROCEDURE ProcessAccountBalances
AS
BEGIN
    DECLARE @acc_id INT;
    DECLARE @acc_bal DECIMAL(18,2);

    -- 1. Declare cursor
    DECLARE acc_cur CURSOR FOR
        SELECT id, balance FROM accounts WHERE balance > 0;

    -- 2. Open cursor
    OPEN acc_cur;

    -- 3. Fetch first row
    FETCH NEXT FROM acc_cur INTO @acc_id, @acc_bal;

    -- 4. Iterate while rows remain
    WHILE @@FETCH_STATUS = 0
    BEGIN
        -- Execute business logic
        PRINT 'Processing Account ID: ' + CAST(@acc_id AS VARCHAR(20));

        -- Fetch next row
        FETCH NEXT FROM acc_cur INTO @acc_id, @acc_bal;
    END;

    -- 5. Clean up
    CLOSE acc_cur;
    DEALLOCATE acc_cur;
END;
```

---

## 5. PostgreSQL Data-Modifying CTEs (Writable CTE: `WITH ... INSERT/UPDATE/DELETE ... RETURNING`)

LionSQL natively supports PostgreSQL **Writable (Data-Modifying) CTEs** and the `::` type cast operator. Complex multi-table OLTP workflows (such as the Transactional Outbox pattern, double-entry journal postings, and atomic inventory reservation with ledger recording) execute in a single round-trip.

### 5.1 Core Guarantees
1. **Multiple Writable CTEs & Implicit Cross Joins**: Chain two or more `INSERT`/`UPDATE`/`DELETE ... RETURNING` CTEs in a single `WITH` clause and project aliased columns (`FROM ins_order o, ins_item i, ins_outbox e`) with exact positional mapping.
2. **Chained `RETURNING` Consumption**: Reference **post-update** rows returned by an earlier `UPDATE ... RETURNING` CTE inside a subsequent `INSERT ... SELECT ... FROM <cte>`.
3. **Zero-Row Match & Unreferenced CTE Materialization**: When a conditional `UPDATE` matches 0 rows, dependent `INSERT` CTEs and the outer `SELECT` cleanly return 0 rows (`pgx.ErrNoRows` / `sql.ErrNoRows`) without error. Write-only CTEs not referenced in the final `SELECT` are unconditionally materialized.
4. **Single-Statement Atomic Rollback (All-or-Nothing & `23505`)**: The entire `WITH` statement executes within an atomic transaction boundary (even in autocommit mode). If any CTE violates a `UNIQUE` constraint, all prior CTE mutations roll back and PostgreSQL SQLSTATE `23505` is returned.
5. **Bind Parameter (`$1 ~ $N`) Reuse & `::type` / `::type[]` Casts**: Supports parameter reuse across multiple CTEs, automatic target-column OID inference for parameters inside `SELECT $4, ...` projections, and PostgreSQL `::type` / `::type[]` casts (such as `x::int4`, `$4::text`, and `ANY($1::text[])`) mapped directly to `CastExpr` AST nodes.

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

## 6. Cross-RDBMS Dialect Quick Reference Matrix

| Operation | LionSQL Idiom | MS SQL Server (T-SQL) | MySQL / MariaDB | Oracle | PostgreSQL |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Identifier Quoting** | `` `id` ``, `[id]`, `"id"` | `[id]` | `` `id` `` | `"id"` | `"id"` |
| **Schema Inspection** | `DESC tbl;` | `sp_help tbl` | `DESC tbl;` | `DESC tbl;` | `\d tbl` |
| **NULL Coalescing** | `IFNULL(a, b)` | `ISNULL(a, b)` | `IFNULL(a, b)` | `NVL(a, b)` | `COALESCE(a, b)` |
| **Current Date** | `CURDATE()` | `CAST(GETDATE() AS DATE)` | `CURDATE()` | `TRUNC(SYSDATE)` | `CURRENT_DATE` |
| **Modulo Operator** | `MOD(n, m)`, `%` | `%` | `MOD(n, m)`, `%` | `MOD(n, m)` | `MOD(n, m)`, `%` |
| **Decimal Truncation** | `TRUNC(x, d)` | `ROUND(x, d, 1)` | `TRUNCATE(x, d)` | `TRUNC(x, d)` | `TRUNC(x, d)` |
| **Substring Search** | `INSTR(str, sub)` | `CHARINDEX(sub, str)` | `INSTR(str, sub)` | `INSTR(str, sub)` | `STRPOS(str, sub)` |
| **Row Iteration Cursor** | `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `CURSOR cur IS` | `DECLARE cur CURSOR` |
