# LionSQL v0.90.0-beta1 Release Notes (`notes/RELEASE_NOTES_v0.90.0-beta1.md`)

> - **Release Date**: 2026-10-20
> - **Version**: `v0.90.0-beta1`
> - **Theme**: **Production Security Guardrails, Strict SQL NULL Propagation, and 43 Engine Integrity & Concurrency Remedies**

---

## 1. Release Highlights

LionSQL `v0.90.0-beta1` delivers a massive hardening pass focused on **production security guardrails, SQL-standard type coercion, unified identifier casing, and the remediation of 43 critical engine integrity bugs** across the VDBE bytecode virtual machine, DML executor, and storage layers.

- **Production Superuser Security Guardrail (`PROD-SEC-1`)**: Enforced an uncompromising safety guard actively blocking server startup when bound to external interfaces (`0.0.0.0` or public IP) with default credentials (`1234567890`), requiring an explicit password change.
- **Strict SQL NULL Propagation Decorators (`SQL-STRICT-NULL-DECORATOR-1`)**: Standardized three-valued logic (3VL) NULL propagation across all built-in scalar and aggregate functions, eliminating silent default fallbacks.
- **Unified Type Promotion Lattice (`STORAGE-TYPE-PROMOTION-LATTICE-1`)**: Introduced an automated binary type coercion lattice (`storage.CoerceBinaryValues`) replacing 150+ lines of ad-hoc heterogeneous type matching.
- **Centralized Synthetic Row Identity Abstraction (`STORAGE-SYNTHETIC-PK-ABSTRACTION-1`)**: Unified hidden row identifiers (`_row_id`, `_dt_row_id`, `_cte_row_id`) into a single type-safe abstraction.
- **43 Deep Kernel Integrity Defect Remedies**: Comprehensive remediation across VDBE execution, MERGE statements, PIVOT/APPLY expressions, case-insensitive catalog resolution, and Point-in-Time Recovery.

---

## 2. Detailed Technical Improvements

### 2.1. Security, Authentication & Brand Unification
- Enforced production startup aborts when binding to public addresses with default credentials.
- Standardized all branding on LionSQL across client SDKs (`lionsql.js`), protocol banners (PostgreSQL v3 `15.0 (LionSQL)`, TDS `LionSQL Engine`), and diagnostic probes.
- Enhanced RBAC `HasTablePermission` with custom role inspection and strict `DENY` precedence.

### 2.2. VDBE Virtual Machine & T-SQL Parser Fixes
- **Simple CASE Syntax Restoration (`BUG-PROC-SIMPLE-CASE-1`)**: Supported `CASE expr WHEN val THEN ...` syntax with heterogeneous type coercion.
- **FROM-less Scalar Subquery Panic Remedy (`BUG-VM-SUBQ-NIL-1`)**: Resolved nil pointer panics on queries like `(SELECT 42)` and `(SELECT @val + 10)`.
- **Default Column Value Injection (`BUG-VM-INSERT-DEFAULT-1`)**: Evaluated column defaults (`GETDATE()`, `NEWID()`, literals) during inserts when fields are omitted.
- **Column Case Normalization (`BUG-VM-UPDATE-1`)**: Prevented silent update loss caused by bracketed or case-variant identifiers in UPDATE statements.
- **Composite Index Bound Selection (`BUG-VM-COMP-IDX-1`)**: Guarded single-column scans against accidental composite index matching.

### 2.3. DML Execution, MERGE & Derived Tables
- **MERGE IDENTITY Synchronization (`BUG-MERGE-IDENTITY-PK-1`)**: Synchronized table identity state during `MERGE ... INSERT`.
- **MERGE Source Self-Destruction Fix (`BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1`)**: Bound `WHEN NOT MATCHED BY SOURCE` to initial target counts, protecting newly inserted rows.
- **APPLY Token Replacement (`BUG-APPLY-REGEX-1`)**: Replaced non-deterministic regex matching with token-aware correlated identifier substitution.
- **Standard SQL PIVOT Count (`BUG-PIVOT-COUNT-NULL-1`)**: Returned `0` instead of `NULL` for empty categories in PIVOT aggregations.

### 2.4. Storage Casing, Type Coercion & PITR Durability
- **Unified Case-Insensitive Map (`CIMap`)**: Centralized identifier resolution across storage and catalog lookup.
- **Catalog Object Durability Across Restarts (`BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1`)**: Serialized stored procedures, functions, triggers, and views to `catalog_objects.json` on mutation.
- **PITR DDL Error Propagation (`BUG-PITR-DDL-SILENT-SWALLOW-1`)**: Eliminated silent error swallowing during Point-in-Time Recovery DDL execution.
- **Int/Float and Date/String Coercion (`BUG-STORAGE-CMP-INT-FLOAT-1`)**: Supported automatic cross-type comparison in index scans and equality bounds.

---

## 3. Verification Scorecard

| Area | Scope | Key Verified Deliverables | Status |
| :--- | :--- | :--- | :---: |
| **Integrity Fixes** | Kernel remediation | 43 critical defects resolved across VM, DML, storage, and catalog | **PASS** |
| **3VL Compliance** | NULL propagation | Standardized strict NULL decorators across string and date functions | **PASS** |
| **Security Guard** | Startup listener | External binding blocked with default credentials | **PASS** |
| **Catalog Durability**| Persistence | 100% preservation of views, procs, and triggers across restarts | **PASS** |

---

<details>
<summary><b>Detailed Changelog Delta</b></summary>

### Added
- Production Superuser Security Guardrail blocking external binding with default password (PROD-SEC-1)
- 1.0 Pre-Release Architecture Review Report (doc/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md)
- SQL strict NULL propagation functional decorators in pkg/proc/strict.go
- Unified Type Promotion Lattice and binary coercion engine in pkg/storage/coerce.go
- Centralized synthetic row identity abstraction in pkg/storage/synthetic_pk.go

### Changed
- Standardized all branding on LionSQL across Web SDK, webhook headers, and protocol banners

### Performance
- Eliminated runtime metrics allocation on write commit with atomic heap cache in MemEngine (VPERF-1)
- Replaced full table scan with cached sequence for IDENTITY insert (VPERF-3)

### Fixed
- Supported Simple CASE syntax in Pratt parser and VDBE execution (BUG-PROC-SIMPLE-CASE-1)
- Resolved nil pointer panics on FROM-less scalar subqueries (BUG-VM-SUBQ-NIL-1)
- Ensured schema column default value injection on omitted insert fields (BUG-VM-INSERT-DEFAULT-1)
- Normalized assignment column names against schema to prevent silent update loss (BUG-VM-UPDATE-1)
- Guarded composite index selection on single-column scans (BUG-VM-COMP-IDX-1)
- Synchronized IDENTITY counters during MERGE INSERT operations (BUG-MERGE-IDENTITY-PK-1)
- Protected newly inserted rows from MERGE NOT MATCHED BY SOURCE deletion (BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1)
- Replaced fragile regex with token-aware replacement in CROSS/OUTER APPLY (BUG-APPLY-REGEX-1)
- Returned 0 instead of NULL for empty categories in PIVOT COUNT (BUG-PIVOT-COUNT-NULL-1)
- Stripped unwanted table qualifiers in FOR JSON projections (BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1)
- Preserved multi-table JOIN projected columns in derived query rewriting (BUG-DERIVED-JOIN-STAR-1)
- Serialized in-memory procedures, triggers, and views to catalog_objects.json across restarts (BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1)
- Eliminated silent error swallowing during Point-in-Time Recovery WAL replay (BUG-PITR-DDL-SILENT-SWALLOW-1)
- Enhanced RBAC HasTablePermission with custom role validation and DENY precedence (BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1)
- Injected column defaults during REST API record creation (BUG-API-CREATE-DEFAULT-1)
- Serialized view metadata into backup snapshot archives (BUG-BACKUP-RESTORE-VIEW-OMISSION-1)
- Normalized string join keys in HashJoinIterator and MergeJoinIterator (BUG-EXEC-JOIN-STR-CASE-1)
- Supported composite equijoin keys in ParseJoinCond (BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1)
- Implemented dynamic peer row frame expansion for RANGE BETWEEN window queries (BUG-WINDOW-RANGE-PEER-EVAL-1)
- Prevented truncation of identifiers ending in 'and' during subquery decorrelation (BUG-DECORRELATE-TRIM-1)
- Prohibited illegal transformation of OR-branch subqueries into semi joins (BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1)
- Introduced CIMap generic case-insensitive map and normalized table/index casing (SQL-CASING-ABSTRACTION-1)
- Supported automatic numeric coercion between Int and Float in storage.Value.Compare (BUG-STORAGE-CMP-INT-FLOAT-1)
- Enforced NULL propagation across string and date built-in functions (BUG-STR-NULL-1, BUG-STR-CHARINDEX-NULL-ZERO-1)
</details>
