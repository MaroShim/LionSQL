# LionSQL Collation, Locale & Multi-Language Architecture & Implementation Plan

This technical specification details LionSQL's architectural strategy and phased implementation roadmap for Microsoft SQL Server Collation, Locale, and internationalization (i18n) compatibility. It establishes how LionSQL reconciles MSSQL's legacy CodePage and collation model with a modern, zero-allocation UTF-8 native storage engine while eradicating historical pain points such as "Collation Conflict (Error 468)."

---

## 1. Executive Summary & Design Invariants

Microsoft SQL Server's collation architecture bundles character encoding (CodePages such as CP949 or CP1252) with linguistic sorting and comparison rules (`CI_AS`, `CS_AI`, `_BIN`). While powerful, this legacy coupling produced severe operational friction—most notably table bloat from `NVARCHAR` (UTF-16) and fatal runtime collation conflicts when joining tables with mismatched collations.

LionSQL adopts a **pragmatic, dual-layered architecture** mirroring the design philosophy of TypeScript:

```
[ Client Layer (TDS / PGWire / REST / SQL Scripts) ]
  - Emulates MSSQL Collations: Korean_Wansung_CI_AS, SQL_Latin1_General_CP1_CI_AS, etc.
  - Accepts COLLATE clauses in DDL and DML expressions without syntax errors.
  - Emulates 5-byte TDS Collation tokens and system metadata (COLLATIONPROPERTY, fn_helpcollations).
                          │
                          ▼ (Normalization / Wire Decoding)
[ Core Execution & Storage Engine (WiredTiger & In-Memory OCC) ]
  - Canonical UTF-8 Storage: All strings (CHAR, VARCHAR, NVARCHAR) stored as pure UTF-8.
  - Case-Insensitive (CI) Default: String equality defaults to zero-alloc strings.EqualFold.
  - Zero Collation Conflict: Coercive unicode comparison prevents runtime query aborts.
```

### Core Invariants:
1. **Universal Canonical UTF-8 Invariant**:
   - Zero physical storage in legacy code pages (no CP949, CP1252, CP932). All string datatypes (`CHAR`, `VARCHAR`, `NVARCHAR`, `TEXT`) are stored as clean UTF-8 byte sequences in both WiredTiger B+Tree and In-Memory OCC.
2. **Default Case-Insensitive (CI) Semantic Parity**:
   - T-SQL queries and stored procedures operate in Case-Insensitive, Accent-Sensitive (`CI_AS`) mode by default, matching standard SQL Server enterprise installations.
3. **Zero Collation Conflict Policy (Tolerant Coercion)**:
   - LionSQL eliminates MSSQL Error 468 (`Cannot resolve collation conflict`). Mismatched collation comparisons are safely coerced to Unicode canonical comparison rather than aborting transactions.
4. **Wire-Level Protocol Fidelity**:
   - TDS 7.x/8.0 handshakes (`LOGINACK`, `ENVCHANGE`) report valid 5-byte collation structures to satisfy client drivers (Microsoft JDBC, ADO.NET, SSMS, DBeaver, EF Core).

---

## 2. Legacy MSSQL Collation Anatomy & Pain Points

### 2.1 The CodePage & Collation Coupling Problem
In legacy SQL Server:
- `VARCHAR`: Tied to the column's CodePage (e.g. CP949 for Korean, CP1252 for Latin1). Cannot store characters outside the code page without mojibake (`?`).
- `NVARCHAR`: Mandatory 2-byte UCS-2/UTF-16 encoding to support multilingual text, doubling memory and disk footprints.
- SQL Server 2019 introduced `_UTF8` collations, but backward compatibility requirements leave millions of existing databases constrained to legacy types.

### 2.2 The Notorious "Collation Conflict" (Error 468)
In SQL Server, temporary tables (`#temp`) created in `tempdb` inherit the instance server collation. If a user database uses `Korean_Wansung_CI_AS` while the server was installed with `SQL_Latin1_General_CP1_CI_AS`, a simple join crashes at runtime:
```sql
-- Fails in MSSQL with Error 468:
SELECT * FROM #temp t JOIN users u ON t.username = u.username;
-- Requires clumsy manual patches:
SELECT * FROM #temp t JOIN users u ON t.username = u.username COLLATE database_default;
```
LionSQL treats this friction as an anti-pattern. Mismatched collation names on equality checks are resolved via Unicode casing rules rather than terminating execution.

---

## 3. Detailed Architectural Components

### 3.1 TDS Protocol 5-Byte Collation Structure
In Microsoft TDS 7.x/8.0, collations are exchanged as a packed 5-byte structure:

```
 0                   1                   2                   3                   4
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|             LCID (20 bits)            |      Collation Flags      |  Version ID | Collation ID|
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

- **Bytes 0–1 & partial Byte 2**: LCID (Locale ID in Little Endian).
  - `0x0409` (1033): en-US (US English)
  - `0x0412` (1042): ko-KR (Korean)
  - `0x0411` (1041): ja-JP (Japanese)
  - `0x0804` (2052): zh-CN (Simplified Chinese)
- **Byte 2 (High bits) & Byte 3**: Comparison flags (`IgnoreCase = 0x01`, `IgnoreAccent = 0x02`, `IgnoreKana = 0x04`, `IgnoreWidth = 0x08`, `Binary = 0x80`, etc.).
- **Byte 4**: SQL Collation ID (e.g. `0x34` = 52 for `SQL_Latin1_General_CP1_CI_AS`, `0x00` for Windows Collations).

#### Standard Collation Mapping Table

| Collation Name | LCID | CodePage | TDS 5-Byte Binary Token | Description |
| :--- | :--- | :--- | :--- | :--- |
| `SQL_Latin1_General_CP1_CI_AS` | `0x0409` (1033) | 1252 | `0x09 0x04 0xD0 0x00 0x34` | Default US English SQL Collation |
| `Korean_Wansung_CI_AS` | `0x0412` (1042) | 949 | `0x12 0x04 0xD0 0x00 0x00` | Standard Korean Windows Collation |
| `Latin1_General_100_CI_AS_SC_UTF8` | `0x0409` (1033) | 65001 | `0x09 0x04 0xD0 0x02 0x00` | Modern UTF-8 Supplementary Collation |
| `Japanese_CI_AS` | `0x0411` (1041) | 932 | `0x11 0x04 0xD0 0x00 0x00` | Standard Japanese Windows Collation |
| `Chinese_PRC_CI_AS` | `0x0804` (2052) | 936 | `0x04 0x08 0xD0 0x00 0x00` | Simplified Chinese Windows Collation |

---

## 4. Phased Implementation Roadmap

### Phase 1: Tolerant DDL & Expression Parsing (`pkg/proc`, `pkg/engine`)

1. **Lexer Extension (`pkg/proc/lexer.go`)**:
   - Register `COLLATE` as a reserved keyword token (`tokCOLLATE`).
2. **DDL Column Definition Parsing**:
   - In `CREATE TABLE` and `ALTER TABLE`, parse optional `COLLATE <ident>` following column type definitions:
     ```sql
     CREATE TABLE tbl (
         name VARCHAR(100) COLLATE Korean_Wansung_CI_AS NOT NULL
     );
     ```
   - Store the collation name in `storage.ColumnDef.Collation`. If omitted, default to the database collation.
3. **Expression Parsing**:
   - Support `COLLATE` as a postfix expression modifier in `WHERE`, `HAVING`, and `ORDER BY` clauses:
     ```sql
     SELECT * FROM tbl WHERE username = @user COLLATE Latin1_General_BIN;
     SELECT * FROM tbl ORDER BY name COLLATE Korean_Wansung_CI_AS;
     ```
4. **Execution Semantics**:
   - If the collation ends with `_BIN` or `_BIN2`, execute strict binary byte comparison (`valA == valB` or `bytes.Compare`).
   - Otherwise, execute zero-allocation case-folded comparison (`strings.EqualFold`).

### Phase 2: Dynamic Server Collation Configuration (`pkg/config`, `pkg/wire/tds`)

1. **CLI Flag & Config File Binding**:
   - CLI Flag: `--collation="Korean_Wansung_CI_AS"` (Default: `SQL_Latin1_General_CP1_CI_AS`).
   - `config.toml` setting:
     ```toml
     [server]
     collation = "Korean_Wansung_CI_AS"
     ```
2. **TDS Handshake Sync**:
   - In `pkg/wire/tds/server.go`, replace hardcoded `DefaultCollation` with the resolved 5-byte token corresponding to `cfg.Server.Collation`.
   - Propagate the active collation token during `LOGINACK` and `ENVCHANGE` (token type `0x07`).
3. **Metadata Synchronization**:
   - Ensure `SERVERPROPERTY('Collation')` and `DATABASEPROPERTYEX(db, 'Collation')` dynamically reflect the configured server collation.

### Phase 3: Built-in Collation System Functions (`pkg/proc/builtins_sys.go`)

Implement the standard T-SQL `COLLATIONPROPERTY` built-in function to support schema introspection in SSMS, DBeaver, and ORMs:

```sql
SELECT COLLATIONPROPERTY('Korean_Wansung_CI_AS', 'CodePage');   -- Returns 949
SELECT COLLATIONPROPERTY('Korean_Wansung_CI_AS', 'LCID');       -- Returns 1042
SELECT COLLATIONPROPERTY('Korean_Wansung_CI_AS', 'ComparisonStyle'); -- Returns 196609
SELECT COLLATIONPROPERTY('SQL_Latin1_General_CP1_CI_AS', 'CodePage'); -- Returns 1252
```

Supported property keys:
- `CodePage`: Corresponding ANSI code page (int).
- `LCID`: Windows Locale Identifier (int).
- `ComparisonStyle`: Comparison flags bitmask.
- `Version`: Collation version ID.

### Phase 4: Virtual Table-Valued Function `::fn_helpcollations()` (`pkg/catalog/virtual/`)

GUI administrative tools (SSMS, Azure Data Studio, DBeaver) populate collation selection dropdowns via `fn_helpcollations()`.

- **Implementation**: Register virtual catalog function in `pkg/catalog/virtual/`.
- **Schema Output**:
  - `name (VARCHAR(128))`
  - `description (VARCHAR(500))`
- **Payload**: Pre-populate top 40 major international and SQL collations covering Korean, US English, Japanese, Chinese, German, Spanish, French, and UTF-8 variants.

---

## 5. Engineering Guidelines Compliance Matrix

| Guideline / Rule | Architectural Application |
| :--- | :--- |
| **Golden Rule 14 (Platform Primitives First)** | Leverages Go's native standard library UTF-8 and Unicode string comparison primitives without external heavy C libraries (ICU). |
| **Golden Rule 15 (Radical KISS & DRY)** | Avoids reimplementing complex code page conversion matrixes by standardizing 100% of internal storage on UTF-8. |
| **Golden Rule 32 (Lexical Robustness)** | The parser tolerates and absorbs `COLLATE` clauses across all standard DDL/DML contexts without syntax panics. |
| **Golden Rule 33 (Safe Default Configurations)** | Defaults to standard `SQL_Latin1_General_CP1_CI_AS` when configuration flags are omitted, guaranteeing backwards compatibility. |
| **Golden Rule 36 (Pure Static Binaries)** | Implements all collation metadata and token generation in pure Go with zero CGo bindings. |
