# LionSQL Unified Superset SQL Compatibility Guide (`doc/SQL_COMPATIBILITY_GUIDE.md`)

> - **Created**: 2026-11-02  
> - **Last Updated**: 2026-11-20  
> [한국어 문서](SQL_COMPATIBILITY_GUIDE.ko.md)  
>  
> - **Document Status**: Production Ready / 100% Verified  
> - **Core Architectural Principles**:  
>   - `[Invariant 17]`: Single Unified SQL Grammar & Zero-Protocol-Branch Invariant (`[RULE:ZeroProtocolBranch]`)  
>   - `[Invariant 4]`: Single Static Binary (TDS `:1433`, PGWire `:5433`, HTTP `:8090`)  
>   - `[RULE:RadicalKISS]`: Avoid configuration mode hell; permanently Spec-Out heavy MySQL-specific engine features  
> - **Regression Tests**: `pkg/engine/sqlexec_mysql_syntax_test.go`, `pkg/proc/builtins_test.go`, `pkg/proc/runtime/cursor_test.go`  

---

## 1. Overview & Single Unified Superset Philosophy

LionSQL adopts a high-concurrency T-SQL VDBE bytecode virtual machine and non-blocking ACID storage (In-Memory OCC / WiredTiger MVCC) as its core execution engine.  
LionSQL's official core protocols and SQL dialects are **Microsoft SQL Server T-SQL (TDS `:1433`)** and **PostgreSQL (PGWire v3 `:5433`)**.

LionSQL's **Unified Superset Architecture** strictly eliminates dialect fragmentation (`sql_mode`), ensuring that **regardless of client wire protocol, both T-SQL and PostgreSQL conveniences are accepted uniformly**.

---

## 2. Core Practical Compatibility Functions

Core scalar functions commonly relied upon in multi-RDBMS and framework environments are natively supported as zero-overhead aliases to eliminate typing friction and accommodate framework migrations.

### 2.1. NULL Handling: `IFNULL(a, b)` & `NVL(a, b)`
- **Behavior**: Returns `b` if `a` is NULL, otherwise `a`. (High-performance zero-cost alias for T-SQL `ISNULL(a, b)`)
- **Example**:
  ```sql
  SELECT id, IFNULL(nickname, 'Guest') FROM users;
  SELECT id, NVL(bonus, 0) FROM employees;
  SELECT id, COALESCE(phone, email, 'None') FROM members;
  ```

### 2.2. Standard Date/Time: `CURRENT_DATE`, `CURDATE()`, `CURRENT_TIME`, `CURTIME()`
- **Behavior**:
  - `CURRENT_DATE`, `CURDATE()`: Returns current date (`'YYYY-MM-DD'`) without time.
  - `CURRENT_TIME`, `CURTIME()`: Returns current time (`'HH:MM:SS'`) without date.
- **Example**:
  ```sql
  SELECT CURRENT_DATE AS today, CURRENT_TIME AS now_time;
  ```

### 2.3. Modulo Arithmetic: `MOD(n, m)`
- **Behavior**: Returns `n % m` remainder. (Ensures compatibility with mathematical functions and ORMs)
- **Example**:
  ```sql
  SELECT MOD(10, 3); -- Result: 1
  ```

> **Secondary Dialect Convenience Functions**:  
> For seamless compatibility and migrations from other engines, LionSQL also transparently supports `TRUNC(x, [d])` / `TRUNCATE(x, d)`, `STRPOS(str, sub)` / `INSTR(str, sub)`, `SHA256(str)`, and `HEX(str)` / `UNHEX(hex)` at the engine level (see [Section 6](#6-multi-rdbms-dialect-quick-reference-matrix) for mapping details).

---

## 3. T-SQL Forward-Only Read-Only Cursors (`PROC-CURSOR-1`)

Full support for **Fast Forward-Only Read-Only Cursors** allows executing the 70–80% of legacy Microsoft SQL Server stored procedures that rely on sequential cursor loops without code modifications.

### 3.1. Syntax & 5-Stage Lifecycle
1. `DECLARE <cur_name> CURSOR FOR <select_statement>`: Cursor definition
2. `OPEN <cur_name>`: Activate result set and initialize index
3. `FETCH NEXT FROM <cur_name> INTO @var1, @var2, ...`: Iterate row and assign
4. `CLOSE <cur_name>`: Release cursor memory
5. `DEALLOCATE <cur_name>`: Remove from session cursor map

### 3.2. Standard Cursor Loop Pattern
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

## 4. PostgreSQL Writable CTEs (`WITH ... INSERT/UPDATE/DELETE ... RETURNING`)

LionSQL supports PostgreSQL-standard **Writable CTEs** and `::` type cast operators within its single unified grammar, allowing complex multi-table mutations in a single round-trip.

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

## 5. Lightweight Ergonomics & External DB (MySQL, etc.) Compatibility/Spec-Out Boundaries

LionSQL includes non-invasive lightweight syntax conveniences to minimize developer fatigue from web ORMs (GORM, Prisma, TypeORM, MyBatis) and migration scripts.  
At the same time, **heavy MySQL-specific engine and protocol features are permanently Spec-Out** to preserve engine integrity.

### 5.1. Accepted Lightweight Ergonomics

#### 1) Backtick Identifier Quotes (`` `column` ``)
- **Behavior**: The lexer transparently treats backticks (`` `...` ``) identically to T-SQL brackets (`[...]`) and ANSI/PostgreSQL double quotes (`"..."`).
- **Official Recommendations**:
  - **T-SQL Recommended**: Square brackets (`[table]`, `[column]`)
  - **ANSI / PostgreSQL Recommended**: Double quotes (`"table"`, `"column"`)
- **Example**:
  ```sql
  SELECT `id`, `name`, `balance` FROM `accounts` WHERE `status` = 'ACTIVE';
  ```

#### 2) Quick Schema Inspection: `DESC` / `DESCRIBE <table>`
- **Behavior**: Returns a 6-column tabular grid displaying column names, data types, nullability, primary key status, and defaults.
- **Example**:
  ```sql
  DESC accounts;
  -- or
  DESCRIBE accounts;
  ```

#### 3) Catalog Exploration: `SHOW TABLES`
- **Behavior**: Lists user tables currently existing in the active database catalog.
- **Example**:
  ```sql
  SHOW TABLES;
  ```

### 5.2. Permanently Spec-Out MySQL Features (Negative Knowledge)
To uphold enterprise OLTP reliability (`[RULE:RadicalKISS]`), the following MySQL features are permanently excluded:

1. **MySQL Wire Protocol (TCP `:3306`) Spec-Out**:
   - Wire listeners are unified to enterprise-standard TDS (`:1433`) and PGWire (`:5433`).
2. **MySQL-Specific DML (`ON DUPLICATE KEY UPDATE`, `REPLACE INTO`) Spec-Out**:
   - PostgreSQL standard `ON CONFLICT DO UPDATE` and ANSI/T-SQL standard `MERGE` are fully supported; adding third dialect branches is rejected.
3. **8-Column Legacy Table `EXPLAIN` Spec-Out**:
   - Deprecated flat 8-column EXPLAIN breaks hierarchical CBO trees; unified on indented text and JSON plan formats.
4. **MySQL `sql_mode` Configuration Hell Spec-Out**:
   - Rejected in favor of a single deterministic parser across all connections.

---

## 6. Multi-RDBMS Dialect Quick Reference Matrix

> **Note**: MySQL/Oracle columns below are for reference only for developers migrating to LionSQL; LionSQL's core official support targets the **single unified superset of T-SQL and PostgreSQL**.

| Purpose | LionSQL Recommended | MS SQL Server (T-SQL) | PostgreSQL | MySQL / Oracle (Reference Only) |
| :--- | :--- | :--- | :--- | :--- |
| **Identifier Quoting** | `[id]` or `"id"` | `[id]` | `"id"` | `` `id` `` (Backticks transparently accepted) |
| **Schema Inspection** | `DESC tbl;` / `sp_help tbl` | `sp_help tbl` | `\d tbl` / `DESC tbl;` | `DESC tbl;` |
| **Catalog Listing** | `SHOW TABLES;` | `SELECT * FROM sys.tables` | `\dt` | `SHOW TABLES;` |
| **NULL Replacement** | `ISNULL(a, b)` / `COALESCE` | `ISNULL(a, b)` | `COALESCE(a, b)` | `IFNULL(a, b)` / `NVL(a, b)` |
| **Current Date** | `CURRENT_DATE` / `GETDATE()` | `CAST(GETDATE() AS DATE)` | `CURRENT_DATE` | `CURDATE()` / `TRUNC(SYSDATE)` |
| **Modulo Arithmetic** | `%` or `MOD(n, m)` | `%` | `%` or `MOD(n, m)` | `MOD(n, m)` |
| **Truncation** | `TRUNC(x, d)` | `ROUND(x, d, 1)` | `TRUNC(x, d)` | `TRUNCATE(x, d)` |
| **Substring Search** | `CHARINDEX()` / `INSTR()` | `CHARINDEX(sub, str)` | `STRPOS(str, sub)` | `INSTR(str, sub)` |
| **Row Iteration** | `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `DECLARE cur CURSOR` | `CURSOR cur IS` |
