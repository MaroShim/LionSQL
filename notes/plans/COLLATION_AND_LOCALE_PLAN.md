# LionSQL Collation, Locale & Multi-Language Architecture & Implementation Plan

> - **Created**: 2026-10-18
> - **Last Updated**: 2026-11-12


This technical specification details LionSQL's architectural strategy and phased implementation roadmap for Microsoft SQL Server Collation, Locale, and internationalization (i18n) compatibility. It establishes how LionSQL reconciles MSSQL's legacy CodePage and collation model with a modern, zero-allocation UTF-8 native storage engine while eradicating historical pain points such as "Collation Conflict (Error 468)."

---

## 1. Executive Summary & Design Invariants

Microsoft SQL Server's collation architecture bundles character encoding (CodePages such as CP949 or CP1252) with linguistic sorting and comparison rules (`CI_AS`, `CS_AI`, `_BIN`). While powerful, this legacy coupling produced severe operational friction—most notably table bloat from `NVARCHAR` (UTF-16) and fatal runtime collation conflicts when joining tables with mismatched collations.

LionSQL adopts a **pragmatic, dual-layered hybrid architecture** combining **TypeScript-style outer compatibility and default runtime collation erasure** with a **Go-native pre-bound function pointer SPI**:

```
[ Outer Client Layer (TDS / PGWire / REST / DDL & DML Compilation) ]
  - 100% MSSQL Syntax Compatibility: Accepts Korean_Wansung_CI_AS, SQL_Latin1_General_CP1_CI_AS, etc.
  - Protocol Emulation: Packed 5-byte TDS collation tokens and system functions (COLLATIONPROPERTY, fn_helpcollations).
  - Cold-Path Pre-Binding: Normalizes default to compareFn = nil, binds custom locales to &customUDF.
                          │
                          ▼ (Runtime Dispatch Branch)
[ Core Execution & Storage Engine (WiredTiger & In-Memory OCC) ]
  ├─ 99% Default Hot-Path (Collation Erasure, 0.5ns Zero-Alloc):
  │    - compareFn == nil branches directly to native strings.EqualFold / bytes.Compare.
  │    - Mirroring TypeScript's Type Erasure, legacy collation machinery is completely erased at runtime.
  └─ 1% Specialized Linguistic Paths (Pay-as-you-go Pre-Bound SPI):
       - Directly invokes compareFn(a, b) with zero map lookups and zero lock contention.
       - Accurately executes Korean NFD/NFC normalization, German DIN 5007-2, French backward accent sort, etc.
```

### Comparison with the Pure TypeScript Model:
- **Parallels to TypeScript (Outer Compatibility & Default Runtime Erasure)**:
  - Just as TypeScript provides developers with a rich compile-time type system while erasing all types before execution on the V8 engine (Type Erasure) to incur zero runtime overhead,
  - LionSQL accepts MSSQL's complex collation syntax and metadata at the outer boundary, but on the 99% default hot path, it completely erases legacy collation mechanics into sub-nanosecond (~0.5ns) native UTF-8 string comparisons.
- **Key Difference from Pure TypeScript (Conditional Runtime SPI)**:
  - TypeScript operates on 100% pure erasure, leaving zero type information or runtime type hooks in generated JavaScript.
  - In contrast, LionSQL provides a **conditional runtime SPI**: when specialized linguistic rules (`Korean_Chosung`, `German_PhoneBook`, `French_Backward`) are explicitly required, it dispatches cleanly through pre-resolved Go UDF function pointers without runtime hash-map lookups.

### Core Invariants:
1. **Universal Canonical UTF-8 Invariant**:
   - Zero physical storage in legacy code pages (no CP949, CP1252, CP932). All string datatypes (`CHAR`, `VARCHAR`, `NVARCHAR`, `TEXT`) are stored as clean UTF-8 byte sequences in both WiredTiger B+Tree and In-Memory OCC.
2. **Default Fast-Path ("No Collation") Zero-Overhead Invariant**:
   - 99% of query comparisons and index lookups execute through a zero-allocation, inline fast-path (`strings.EqualFold` or `bytes.Compare`), clocking sub-nanosecond latency (~0.5ns).
   - Default collations (`SQL_Latin1_General_CP1_CI_AS`, `DATABASE_DEFAULT`) are normalized to empty string (`""`) in cold paths (parser/planner) so the hot path requires only a single `collation == ""` check.
3. **Pluggable Go UDF Collation SPI**:
   - Complex multilingual collations (`Korean_Wansung_CI_AS`, custom sorting) are plugged in via lightweight in-process Go UDF callbacks (`RegisterCollation`) rather than bloating the engine core with multi-megabyte ICU tables.
4. **Zero Collation Conflict Policy (Tolerant Coercion)**:
   - LionSQL eliminates MSSQL Error 468 (`Cannot resolve collation conflict`). Mismatched collation comparisons are safely coerced to Unicode canonical comparison rather than aborting transactions.
5. **Wire-Level Protocol Fidelity**:
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

### 3.2. Pluggable Collation Architecture: Fast-Path No-Collation & Pre-Resolved Go UDF

Embedding massive Unicode Collation Algorithm (UCA) tables and full ICU libraries into the core database binary bloats executable size and regresses sub-microsecond index lookups. Furthermore, performing string hash map lookups on high-frequency comparison loops degrades hot-path throughput. LionSQL decouples collation processing into a **Two-Tier Pre-Resolved (Fast-Path Native vs. Pre-Bound Go UDF)** architecture:

```
[ Incoming SQL Query / Plan Compilation / DDL Binding (Cold-Path) ]
                               │
                               ▼
        Resolve Collation Name to Pre-Bound Function Pointer:
        - Default / Empty / SQL_Latin1_General  ──▶  CompareFn = nil
        - Custom Registered Collation           ──▶  CompareFn = &customUDF
                               │
                               ▼
         [ Critical Hot-Path Execution (CompareStrings) ]
         ┌────────────────────────────────────────────────────────┐
         │ if compareFn == nil {                                  │
         │     return fastCompareCI(a, b) // 0.5ns, Zero-Alloc    │
         │ }                                                      │
         │ // Pre-resolved function pointer: Zero map lookup!     │
         │ return compareFn(a, b)                                 │
         └────────────────────────────────────────────────────────┘
```

#### 1. Cold-Path Pre-Binding & Normalization (Parser / Planner / Binder)
- **Zero Map Lookups on Hot Paths**: Collation names are never resolved during active table scans or B+Tree traversals. At query planning and table schema compilation time, the engine resolves string collation identifiers against the UDF registry into direct function pointers (`CollationCompareFunc`) or pre-resolved integer slots (`[RULE:BoundedLookup]`).
- Default collations (`SQL_Latin1_General_CP1_CI_AS`, `DATABASE_DEFAULT`, `SERVER_DEFAULT`, or omitted) resolve to `compareFn = nil`.

#### 2. Hot-Path Zero-Overhead Fast-Path (0.5ns Invariant)
- Inside table scans, secondary B+Tree index searches, and hash joins, the string comparison function performs **only a single nil pointer check (`if compareFn == nil`)**.
- Resolves directly to inlined `strings.EqualFold` (for CI) or `bytes.Compare` (for `_BIN`/`_BIN2`), preserving LionSQL's sub-nanosecond latency budget (`[RULE:LatencyBudget]`).

#### 3. Pre-Bound Go UDF Collation Execution (Pay-as-you-go)
- Specialized multilingual sorting (e.g. `Korean_Wansung_CI_AS`, accent sensitivity, custom business collations) executes via direct invocation of the pre-resolved function pointer (`compareFn(a, b)`), eliminating runtime string hashing and synchronization locks.
- Developers register custom collation providers at startup via the in-process Go SPI:
  ```go
  // CollationCompareFunc returns -1, 0, or 1
  type CollationCompareFunc func(a, b string) int

  // Register custom or locale collation provider
  proc.RegisterCollation("KOREAN_WANSUNG_CI_AS", koreanCollateFunc)
  ```

### 3.3. Linguistic Case Studies: Korean, German, and French

Understanding real-world linguistic edge cases demonstrates why generic byte comparison fails in international databases and validates LionSQL's pluggable Go UDF architecture:

#### 1. Korean (`Korean_Wansung_CI_AS`, `Korean_100_CI_AS`)
- **macOS NFD vs Windows/Linux NFC Normalization Bug**:
  - macOS and iOS file systems/clipboards frequently generate Decomposed Hangul Jamo (NFD: `ㅎ` + `ㅗ` + `ㅇ` + `ㄱ` + `ㅣ` + `ㄹ` + `ㄷ` + `ㅗ` + `ㅇ`).
  - Windows and standard database clients generate Precomposed Hangul (NFC: `홍길동`).
  - Under binary equality (`valA == valB`), `WHERE name = '홍길동'` silently returns **0 rows**, causing severe cross-platform data missing bugs. LionSQL's collation normalizes NFD and NFC into unified Unicode code points.
- **Mixed Korean-English Case-Insensitivity (CI)**:
  - Korean enterprise records constantly interleave English trademarks and personal names (`삼성 Galaxy S24 Ultra`, `John Park`).
  - A query `WHERE model = '삼성 galaxy s24 ultra'` requires case-insensitivity on Latin characters while maintaining Hangul integrity. Without CI collation, queries require `WHERE LOWER(model) = LOWER(...)`, breaking B+Tree index seeks and triggering costly full table scans.
- **Lexicographical Dictionary Sort vs Flat Byte Sort**:
  - Raw UTF-8 byte ordering sorts ASCII punctuation and numbers before Hangul, but isolates standalone jamo consonants (`ㄱ`, `ㄴ`) far from composite syllables (`가`, `나`).
  - Official Korean dictionary sorting mandates proper ordering of standalone consonants, vowels, and precomposed syllables in continuous sequence.
- **Killer Practical Extension: Chosung (Initial Consonant) Search**:
  - Korean mobile and enterprise apps (delivery, messengers, banking) ubiquitously search people or products by consonant acronyms (e.g. searching `홍길동` with `ㅎㄱㄷ`).
  - Rather than creating redundant redundant columns (`name_chosung VARCHAR(50)`) with manual trigger synchronizations, LionSQL's Go UDF SPI allows registering `Korean_Chosung`:
    ```sql
    SELECT * FROM users WHERE name COLLATE Korean_Chosung = 'ㅎㄱㄷ';
    ```

#### 2. German (`German_PhoneBook_CI_AS` vs `German_CI_AS`)
- **DIN 5007-1 (Dictionary) vs DIN 5007-2 (Phonebook / Directory)**:
  - German has two distinct official collation standards:
    - **DIN 5007-1 (Standard Dictionary)**: Umlauts (`ä`, `ö`, `ü`) sort as their base vowels (`a`, `o`, `u`). E.g., `Müller` sorts adjacent to `Muller`.
    - **DIN 5007-2 (Phonebook / Directory - `German_PhoneBook_CI_AS`)**: Umlauts expand to vowel + `e` (`ä` -> `ae`, `ö` -> `oe`, `ü` -> `ue`). E.g., `Müller` sorts as `Mueller`, appearing *after* `Muller`.
  - In telecommunication and CRM directories, DIN 5007-2 is legally required to avoid missing customer entries.
- **Sharp S / Eszett (`ß`) & Capitalization**:
  - In German, `ß` (Eszett) historically had no standard uppercase form and expanded to `SS` (e.g., `Straße` -> `STRASSE`).
  - Recently, the capital Eszett `ẞ` (U+1E9E) was formalized. German collation handles equivalence across `ß`, `SS`, and `ẞ` during case-insensitive (`CI`) comparisons.

#### 3. French (`French_CI_AS`, `French_CI_AI`)
- **Backward Accent Sorting (Unique Right-to-Left Accent Evaluation)**:
  - While almost all European languages evaluate accents from left to right, standard French lexicographical sorting evaluates secondary accent differences **from right to left (backward)**.
  - Classic Example: `cote`, `côte`, `coté`, `côté`
    - Standard left-to-right / byte sorting produces: `cote` < `coté` < `côte` < `côté` (evaluates accent on `e` before `o`).
    - Standard French dictionary sorting produces: `cote` < `côte` < `coté` < `côté` (evaluates the rightmost letter `e` first; `cote` and `côte` end in plain `e`, so they precede `coté` and `côté` which end in accented `é`).
  - Without backward accent collation, French dictionaries and administrative registries produce incorrect ordering.
- **Accent-Insensitive (`AI`) vs Accent-Sensitive (`AS`) Precision**:
  - In French, accents change meaning fundamentally (`peche` = fishing/sin, `pêche` = peach/fishing, `pêché` = sinned).
  - `French_CI_AI` enables tolerant user search across accents, while `French_CS_AS` guarantees exact legal/contractual precision.
- **Ligature Decomposition**:
  - Characters such as `œ` (`cœur`, `sœur`) and `æ` (`ex æquo`) must sort identically to their multi-letter expansions `oe` and `ae`.

---

### 3.4. Non-Compilation Declarative Custom Locale Architecture (Config-to-Closure Compilation)

To empower operators to define proprietary collations without installing a Go compiler or rebuilding binaries, LionSQL implements **"First-Class Function & Closure-Backed Declarative Rule Compilation."** Without relying on complex JIT compilers or fragile dynamic C libraries (`dlopen`), pure Go primitives deliver nanosecond-tier comparison speeds:

```
[ config/collations.toml Declarative Rules ]
                  │
                  ▼ (One-Time Cold-Path at Boot)
[ BuildDeclarativeCollation Factory Compilation ]
  - Compiles rules into immutable flat rune lookup tables / tries.
  - Returns an anonymous Go closure capturing the pre-built lookup table.
                  │
                  ▼
[ proc.RegisterCollation() SPI Registration ]
  - Treated as an identical function pointer to hardcoded built-in collations.
                  │
                  ▼
[ Query Planner Pre-Binding ──▶ Hot-Path Zero-Alloc Streaming Traversal ]
```

#### 1. Go First-Class Functions & Closure Capture (Zero-JIT Function Factory)
- In Go, functions are first-class values that can be passed, returned, and retain references to their lexical scope (closures).
- The factory function `BuildDeclarativeCollation(cfg)` parses expansion/substitution rules, builds an immutable flat array or trie optimized for $O(1)$ rune lookup, and returns a `CollationCompareFunc` closure capturing that table:
  ```go
  // pkg/proc/collation_factory.go
  func BuildDeclarativeCollation(cfg CollationRuleConfig) CollationCompareFunc {
      // 1. Pre-build O(1) rune lookup structures from declarative rules
      lookup := buildStaticRuneLookup(cfg.Expansions, cfg.Substitutions)

      // 2. Return high-performance closure capturing the immutable table
      return func(a, b string) int {
          return streamingCompareWithTable(a, b, lookup, cfg.CaseSensitive)
      }
  }
  ```

#### 2. Zero-Allocation Streaming Comparison Engine (`[RULE:ZeroAllocation]`)
- When comparing strings `a` and `b`, the engine avoids allocating intermediate transformed strings in the Go heap.
- An inlined rune cursor advances through both strings, consulting the captured lookup table to emit and compare the next effective rune on-the-fly (Streaming Generator), ensuring zero memory allocations and eliminating garbage collection pressure.

#### 3. Declarative Configuration Specification (`config/collations.toml`)
Operators can activate new custom collations upon server restart simply by defining declarative TOML rules:
```toml
# config/collations.toml
[collations.German_Custom_PhoneBook]
case_sensitive = false
accent_sensitive = true
# Character expansion rules (Umlaut -> vowel + e)
expansions = [
  { from = "ä", to = "ae" },
  { from = "ö", to = "oe" },
  { from = "ü", to = "ue" },
  { from = "ß", to = "ss" }
]

[collations.Korean_Chosung_Search]
type = "hangul_chosung"
ignore_space = true
```

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

GUI administrative tools (SSMS, DBeaver) populate collation selection dropdowns via `fn_helpcollations()`.

- **Implementation**: Register virtual catalog function in `pkg/catalog/virtual/`.
- **Schema Output**:
  - `name (VARCHAR(128))`
  - `description (VARCHAR(500))`
- **Payload**: Pre-populate top 40 major international and SQL collations covering Korean, US English, Japanese, Chinese, German, Spanish, French, and UTF-8 variants.

---

## 5. Engineering Guidelines Compliance Matrix

| Guideline / Rule | Architectural Application |
| :--- | :--- |
| **[RULE:PlatformPrimitives] Platform Primitives First** | Leverages Go's native standard library UTF-8 and Unicode string comparison primitives without external heavy C libraries (ICU). |
| **[RULE:RadicalKISS] Radical KISS & DRY** | Avoids reimplementing complex code page conversion matrixes by standardizing 100% of internal storage on UTF-8. |
| **[RULE:LexicalSafety] Lexical Robustness** | The parser tolerates and absorbs `COLLATE` clauses across all standard DDL/DML contexts without syntax panics. |
| **[RULE:SafeDefaults] Safe Default Configurations** | Defaults to standard `SQL_Latin1_General_CP1_CI_AS` when configuration flags are omitted, guaranteeing backwards compatibility. |
| **[RULE:StaticBinary] Pure Static Binaries** | Implements all collation metadata and token generation in pure Go with zero CGo bindings. |
| **[RULE:BoundedLookup] Direct Slice Indexing & Bounded Lookups** | Pre-resolves collation functions in cold path, eliminating runtime map lookups on string comparison hot paths. |
| **[RULE:LatencyBudget] Responsive Latency & Performance Budget** | Guarantees sub-nanosecond (~0.5ns) zero-allocation comparison on the default fast-path. |

---

## Appendix: Ambiguities & Architectural Resolutions for Users and Implementers

To preempt runtime defects, data divergence, and operator confusion in production deployments, this appendix formalizes key edge cases across both user/DBA ergonomics and database engine internals:

### A. User & Operator Ambiguities and Resolutions

#### 1. B+Tree Index Consistency upon Post-Creation Rule Mutation
- **Ambiguity**: If an operator creates a secondary B+Tree index on a column with a custom collation defined in `collations.toml` and subsequently edits expansion/substitution rules and restarts the server, physical B+Tree key sequences on disk will mismatch runtime search invariants, causing **index corruption / missing seeks**.
- **Resolution**:
  - `collations.toml` rulesets include mandatory `version = <int>` and SHA-256 integrity checksums.
  - Secondary index catalogs store the collation version/checksum at index build time. On startup, mismatches trigger an explicit diagnostic warning and isolate index seek paths until `REINDEX TABLE <name>` is executed (`[RULE:ExplicitError]`).

#### 2. Symmetry in Equality Checks vs Initial Consonant (Chosung) Search
- **Ambiguity**: Under `Korean_Chosung`, searching `WHERE name COLLATE Korean_Chosung = 'ㅎㄱㄷ'` naturally matches `홍길동`. However, does `WHERE name COLLATE Korean_Chosung = '홍길동'` match a raw database row containing `ㅎㄱㄷ`?
- **Resolution (SQL Equivalence Class Model)**:
  - Standard SQL `COLLATE` semantics dictate symmetric equivalence (`compareFn(a, b) == 0`). Both operands are normalized to initial consonants before comparison (`'ㅎㄱㄷ' == 'ㅎㄱㄷ'`), ensuring strict mathematical consistency between `WHERE` filters and `ORDER BY` sorting partitions.

#### 3. Priority Rules on Multi-Collation Column Joins (Coercion Hierarchy)
- **Ambiguity**: When joining `colA (Korean_Wansung)` with `colB (Latin1_General)`, legacy SQL Server raises Error 468. LionSQL guarantees zero query crashes, but which collation's comparison semantics take precedence?
- **Resolution (SQL-92 Coercion Standard)**:
  1. An explicit `COLLATE` clause in the query always wins unconditionally.
  2. If both are column defaults and one is a specialized locale while the other is the server default (`SQL_Latin1_General`), **the specialized locale takes precedence**.
  3. If two distinct specialized locales conflict (e.g. Korean vs German), the engine coerces safely to **Unicode canonical case-insensitive (CI) equality** without crashing.

---

### B. Engine Implementation Challenges and Resolutions

#### 4. Hash Join & GROUP BY Hash Code Invariance (Critical Invariant)
- **Challenge**: B+Tree seeks and `ORDER BY` operations rely solely on `compareFn(a, b)`. However, `GROUP BY`, `DISTINCT`, and `Hash Join` rely on hash functions (`hash(string) uint64`).
  - Mathematical Invariant: `compareFn(a, b) == 0` (equality) ➔ **must strictly imply `hash(a) == hash(b)`**.
  - E.g. If `Müller` == `Mueller` under German PhoneBook rules, their 64-bit hash values must be identical; otherwise hash joins will separate them into different buckets, causing silent join omissions.
- **Resolution**:
  - The Collation SPI mandates registering **collation key generators (`CollationKeyFunc`)** or **unified hash functions (`CollationHashFunc`)** alongside comparison functions.
  - Hash join planners bind collation-aware hash generators on partitioned columns, eliminating hash divergence.

#### 5. State Machine Complexity in 1:N Character Expansions
- **Challenge**: Expanding 1 rune into 2 runes (`ä` -> `ae`) within an inline zero-alloc streaming cursor requires maintaining cursor state plus remaining buffer characters, inflating branch mispredictions.
- **Resolution (`[RULE:BoundedBuffers]`, `[RULE:ZeroAllocation]`)**:
  - Over 99% of hot-path comparison keys are shorter than 128 bytes.
  - Rather than fragile inline generator state machines, the engine rents a fixed 256-byte buffer from `sync.Pool`, flattens the rune transformation in one linear pass, and executes fast memory comparison (`bytes.Compare`), guaranteeing zero heap allocation while maximizing CPU branch predictability.

#### 6. Latency Protection via Ingestion-Time Normalization
- **Challenge**: Executing dynamic Unicode NFC/NFD decomposition on every single row comparison during full table scans destroys the sub-nanosecond (0.5ns) latency budget.
- **Resolution (Ingestion-Time Normalization)**:
  - All incoming string payloads are normalized to **canonical Unicode NFC once during `INSERT` / `UPDATE`** before storage.
  - SQL query parameters (`WHERE name = '홍길동'`) are normalized once during parser/planner compilation (cold-path).
  - Because stored tuples and search arguments are already guaranteed to be in NFC form, **the hot-path comparison loop runs with zero normalization overhead at 0.5ns speed**.

