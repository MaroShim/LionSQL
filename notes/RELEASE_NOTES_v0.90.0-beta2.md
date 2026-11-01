# LionSQL v0.90.0-beta2 Release Notes (`notes/RELEASE_NOTES_v0.90.0-beta2.md`)

> - **Release Date**: 2026-10-21
> - **Version**: `v0.90.0-beta2`
> - **Theme**: **Token-Based DML Architecture, 7 Hot-Path Performance Optimizations, and Beekeeper Studio Enterprise Interoperability**

---

## 1. Release Highlights

LionSQL `v0.90.0-beta2` delivers a structural architectural milestone replacing legacy regular expressions with a **token-based one-pass DML compiler (`ARCH-DML-1`)**, introduces **7 hot-path zero-allocation optimizations (`VPERF-2` ~ `VPERF-14`)**, resolves critical enterprise GUI interoperability with **Beekeeper Studio**, and enhances Web Studio with a real-time **System Overview & Monitoring Dashboard**.

- **Token-Based Single-Pass DML Parser (`ARCH-DML-1`, `ARCH-LEX-1`)**: Replaced 8 fragile regular expressions (`reDml*`) and ~300 lines of brittle manual scanner loops with an integrated token-aware DML parser and unified lexical scanning engine (`pkg/sqlutil`).
- **7 Hot-Path Zero-Allocation Optimizations (VPERF Track)**:
  - Persistent session reuse for autocommit queries keeping WiredTiger CGO sessions alive.
  - Reverse foreign key cache (`fkCache`) on MemEngine enabling $O(1)$ mutation validation.
  - Dynamic TDS packet sizing (up to 32KB) and direct UTF-16LE serialization.
  - Lock contention reduction in `PlanCache.GetOrCompile` via raw string keys and RLock fast-paths.
  - Trigger bypass flags avoiding allocations on trigger-free tables.
- **Beekeeper Studio Enterprise Interoperability (`BUG-TDS-BEEKEEPER-RPC-STATEMENT-1`)**: Supported standard ordinal positional fallback for TDS RPC stored procedure handlers, eliminating handshake connection errors with `node-mssql` and Beekeeper Studio.
- **Web Studio Real-time Overview Dashboard (`UI-OVERVIEW-1`, `UI-14`)**:
  - Live metric cards (engine, active sessions, total throughput TPS, uptime) with auto-refresh controllers (5s, 10s, 30s, 60s).
  - 4-section structured sidebar navigation (`DATABASE & QUERY`, `AUTOMATION & STREAM`, `DEVELOPER & ACCESS`, `MONITORING & SYSTEM`).
- **Dual-Format Single-Binary CLI Versioning (`CLI-VERSION-1`)**: Structured build report (`--version`) and compact single-line descriptor displayed on foreground startup.

---

## 2. Detailed Technical Improvements

### 2.1. Parser Architecture & Lexical Modernization
- **DML Compiler Unification (`ARCH-DML-1`)**: Unified `INSERT`, `UPDATE`, and `DELETE` parsing into a single AST representation, verified with a 55-case characterization safety net.
- **SQL Lexical Scanning Primitive (`ARCH-LEX-1`)**: Unified token extraction, nested parenthesis handling, and arithmetic operator splitting in `pkg/sqlutil`.
- **Literal Isolation (`REV-ENG-006`)**: Prevented `WHERE` keywords embedded inside string literals (`'a WHERE b'`) from corrupting assignment clause splitting.

### 2.2. Vertical Performance (VPERF Track)
- Eliminated heap allocations during autocommit queries by persisting `ClientSession.Session`.
- Enabled $O(1)$ validation on parent table updates/deletes when no child FK references exist.
- Hoisted catalog lookups out of row iteration loops in `execUpdateWhere` and `execDeleteWhere`.
- Optimized `INSERT ... ON CONFLICT` to utilize secondary index seeks instead of full table scans.

### 2.3. Wire Protocols, COPY & Authentication Remediation
- **Admin Password Bypass Removal (`BUG-AUTH-ADMIN-PW-BYPASS-1`)**: Enforced strict password authentication on default superuser accounts whenever registered.
- **Transactional COPY Protocol (`BUG-WIRE-COPY-TX-BYPASS-1`)**: Bound `COPY IN` and `COPY OUT` directly to active client transactions with rollback safety.
- **PGWire Multi-Digit Parameter Fix (`BUG-PGWIRE-PARAM-1`)**: Prevented `$1` from corrupting multi-digit parameters like `$10`.
- **COPY Escaping & Error Handling (`BUG-WIRE-COPY-SILENT-CORRUPT-1`)**: Returned structured errors for malformed numeric/date inputs and handled standard tab/newline escape codes.

### 2.4. Storage Stability & Session Lifecycle
- Added periodic 5-minute session reset in WiredTiger background sync, eliminating 60-minute sweep-server idle session warnings (`BUG-WT-SWEEP-2`).
- Prevented session-isolated temporary tables (`__temp_*`, `#*`) from leaking into disk snapshots or WAL records.
- Fixed reverse index scan ordering for descending queries (`WTTx.IndexScanDesc`).

---

## 3. Verification Scorecard

| Area | Scope | Key Verified Deliverables | Status |
| :--- | :--- | :--- | :---: |
| **DML Compiler** | `pkg/engine` (55 cases) | Zero regex reliance, 100% characterization pass rate | **PASS** |
| **Client Interop** | Beekeeper Studio | Handshake, schema navigation, and query execution verified | **PASS** |
| **Throughput Gains**| VPERF Track | Up to 32KB packet frames, zero-alloc PlanCache dispatch | **PASS** |
| **Authentication** | Wire listeners | 100% password enforcement on PGWire and TDS ports | **PASS** |

---

<details>
<summary><b>Detailed Changelog Delta</b></summary>

### Added
- Dual-format version and build information reporting in CLI (CLI-VERSION-1)
- Structured Web Studio left sidebar navigation into 4 logical sections (UI-14)
- System Overview and Real-time Monitoring dashboard in Web Studio (UI-OVERVIEW-1)
- Streamlined Web Studio branding and sub-feature navigation titles (UI-13)

### Performance
- Reused persistent ClientSession for autocommit queries (VPERF-2)
- Added reverse foreign key cache on MemEngine for O(1) checks (VPERF-4)
- Dynamic TDS packet sizing up to 32KB and direct UTF-16LE serialization (VPERF-7)
- Replaced SHA-256 hash computation with string keys in PlanCache (VPERF-8)
- Added HasTriggers bypass in VDBE execution (VPERF-9)
- Optimized INSERT ... ON CONFLICT to use IndexScan (VPERF-13)
- Hoisted schema lookups out of row loops in UPDATE/DELETE (VPERF-14)

### Refactored
- Replaced 8 reDml* regexes with token-based one-pass DML parser (ARCH-DML-1)
- Unified SQL lexical scanning and quote tracking into pkg/sqlutil (ARCH-LEX-1)

### Fixed
- Fixed TDS RPC stored procedure positional fallback for Beekeeper Studio (BUG-TDS-BEEKEEPER-RPC-STATEMENT-1)
- Added periodic session reset to eliminate WiredTiger sweep-server warnings (BUG-WT-SWEEP-2)
- Prevented string literals containing WHERE from breaking UPDATE clauses (REV-ENG-006)
- Prevented session temporary tables from leaking to disk snapshots (BUG-STORAGE-TEMP-TABLE-PERSIST-LEAK-1)
- Enforced password authentication for admin logins on PGWire and TDS (BUG-AUTH-ADMIN-PW-BYPASS-1)
- Fixed DELIMITER and NULL option parsing in COPY statements (BUG-COPY-DELIM-1)
- Prevented $1 from corrupting multi-digit parameters in PGWire (BUG-PGWIRE-PARAM-1)
- Bound COPY IN and COPY OUT directly to active client transactions (BUG-WIRE-COPY-TX-BYPASS-1)
- Returned structured errors for unparseable values in COPY (BUG-WIRE-COPY-SILENT-CORRUPT-1)
- Bound TDS RPC prepared execution to active transactions (BUG-TDS-RPC-PREP-TX-BYPASS-1)
- Added divisor validation in evalUpdateExpr to prevent divide-by-zero panics (REV-ENG-004)
- Fixed SkipList unique index covering column updates (BUG-MEM-SKIPLIST-UNIQUE-INCLUDE-COPY-1)
- Enforced intra-transaction unique collision tracking on commit (BUG-OCC-COMMIT-INTRA-TX-UNIQUE-1)
- Restored secondary index creation during WAL OpCreateTable replay (BUG-MEM-WAL-CREATE-TABLE-INDEX-OMISSION-1)
- Handled torn WAL writes with clean tail truncation (BUG-WAL-TORN-BOOT-1)
- Implemented reverse iterator in WiredTiger for IndexScanDesc (BUG-WT-INDEX-DESC-1)
</details>
