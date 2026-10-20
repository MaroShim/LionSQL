# Changelog

All notable changes to LionSQL are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- `SQL-STRICT-NULL-DECORATOR-1`: Introduced standard SQL strict NULL propagation functional decorators (`Strict`, `StrictUnary`, `StrictBinary`, `RegisterStrictScalarFunction`, `HasAnyNull`, `HasNullAt`) in `pkg/proc/strict.go`.
- `STORAGE-TYPE-PROMOTION-LATTICE-1`: Introduced unified Type Promotion Lattice and binary coercion engine (`storage.CoerceBinaryValues`, `CompareFloatEpsilon`) in `pkg/storage/coerce.go`, eliminating 150+ lines of ad-hoc heterogeneous type matching in `Value.Equal` and `Value.Compare`.
- `STORAGE-SYNTHETIC-PK-ABSTRACTION-1`: Introduced centralized synthetic row identity abstraction (`storage.SyntheticPKKind`, `NewSyntheticPKColumn`, `IsSyntheticPKName`, `HasSyntheticPK`, `SyntheticPKColumn`) in `pkg/storage/synthetic_pk.go`, eliminating scattered magic strings (`_row_id`, `_dt_row_id`, `_cte_row_id`) across DDL, derived tables, CTEs, projection, and VDBE VM.

### Fixed
- **Group 3 VDBE Bytecode VM & T-SQL Parser Remediation (5 Critical Integrity & Procedural Defects)**:
  - `BUG-PROC-SIMPLE-CASE-1` & `BUG-PARSER-SIMPLE-CASE-OMISSION-1`: Added `Operand Expr` to AST `CaseExpr`, supported Simple CASE syntax (`CASE expr WHEN val THEN ... [ELSE ...] END`) in Pratt parser `parseCase`, and compiled operand comparison with `OpEq` and heterogeneous type coercion via `storage.Value.Equal`, restoring standard T-SQL simple case evaluation.
  - `BUG-VM-SUBQ-NIL-1`: Resolved nil pointer dereference panics on `sel.From.Name` for FROM-less scalar subqueries (`(SELECT 42)`, `(SELECT @val + 10)`) in parser, compiler, and evaluator by directly evaluating projection expressions, and rejected invalid multi-column scalar subqueries (`(SELECT 1, 2)`).
  - `BUG-VM-INSERT-DEFAULT-1`: Introduced unified `proc.ParseLiteralDefault` in `pkg/proc/literal.go` to parse scalar literals, functions (`GETDATE()`, `NEWID()`), and outer parentheses (e.g. `((0))`), and integrated it into VDBE `OpInsert`, guaranteeing proper injection of schema column default values when omitted.
  - `BUG-VM-UPDATE-1`: Normalized assignment column names against table schema and existing records using `sqlutil.EqualFoldIdent` in `TableSchema.FindColumn`, VDBE `OpUpdate`, and `execUpdateWhere` (both fast path and scan path), preventing silent update loss caused by bracketed or case-variant column identifiers.
  - `BUG-VM-COMP-IDX-1`: Enhanced `findMatchingIndex` in `pkg/proc/runtime/vm.go` to strictly check `len(idx.Columns) == 1` for single-column scans, preventing accidental selection of composite indexes with unencoded scalar values (which returned 0 rows), and implemented composite key encoding (`storage.EncodeIndexKeyPart`) when all composite index columns are present in equality conditions.
- **Group 4 DML Execution Engine, MERGE, PIVOT, APPLY & Derived Tables Remediation (6 Critical Integrity Defects)**:
  - `BUG-MERGE-IDENTITY-PK-1`: Implemented automatic IDENTITY generation, table identity tracking, and session identity synchronization (`@@identity`, `SCOPE_IDENTITY()`) during `MERGE ... WHEN NOT MATCHED THEN INSERT`, preventing missing primary key rejections.
  - `BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1`: Bound `WHEN NOT MATCHED BY SOURCE` evaluation strictly to target rows present prior to MERGE execution (`initialTargetCount`) and excluded newly inserted keys (`newlyInsertedKeys`), eliminating self-destruction of newly inserted rows.
  - `BUG-APPLY-REGEX-1`: Replaced non-deterministic map iteration and fragile regular expression substitution in `buildParameterizedApplyQuery` with deterministic token-aware identifier replacement (`replaceCorrelatedTokens`), preventing corruption of string literals or keywords during `CROSS/OUTER APPLY`.
  - `BUG-PIVOT-COUNT-NULL-1`: Guaranteed standard SQL behavior in `PIVOT` by returning `0` instead of `NULL` for `COUNT` aggregates on empty categories without matching records.
  - `BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1`: Extracted table names and table aliases in `execForJSONSelect` (`extractTableAliases`) and stripped table qualifiers from column names, preventing unwanted wrapper object nesting (e.g. `{"u": {"id": ...}}`) on unaliased projections.
  - `BUG-DERIVED-JOIN-STAR-1`: Constrained single-table `SELECT *` derived query rewriting so that multi-table `SELECT *` JOIN queries preserve all joined tables' projected columns while cleanly hiding synthetic PKs (`_dt_row_id`).
- **Group 5 Security, Backup/PITR, Catalog Persistence & REST API Remediation (6 Critical Integrity Defects)**:
  - `BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1`: Implemented atomic serialization of in-memory procedures, functions, triggers, and views to `catalog_objects.json` on all mutations and automatic reload upon server start (`catalog.SetPersistenceDir`), preventing permanent loss of catalog objects across restarts.
  - `BUG-PITR-DDL-SILENT-SWALLOW-1`: Eliminated silent error swallowing (`_ =`) during Point-in-Time Recovery and WAL replay DDL operations (`CreateTable`, `DropTable`, `TruncateTable`, `DropPartition`, `CreateIndex`, `DropIndex`) in `ApplyWALRecordWithOptions`, ensuring immediate error propagation and rollback on recovery failures.
  - `BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1`: Enhanced RBAC `HasTablePermission` to inspect user assigned custom roles (`u.Roles`) with standard DENY precedence, and updated `GrantTable`/`DenyTable` to validate and authorize role subjects.
  - `BUG-API-CREATE-DEFAULT-1`: Added automatic evaluation and injection of schema column default values (`col.Default != ""`) via `engine.ParseLiteralValue` during REST API record creation (`handleCreateRecord`), preventing 400 Bad Request `cannot be null` rejections when default-valued fields are omitted.
  - `BUG-BACKUP-RESTORE-VIEW-OMISSION-1`: Serialized view metadata into `catalog/views.json` within snapshot backup archives and restored views during database restore with `ViewsCount` and `ViewsRestored` tracking in backup/restore manifests.
  - `BUG-CATALOG-FK-MULTI-COL-ACTIONS-1`: Emitted individual metadata rows per column pair for composite foreign keys in MSSQL virtual catalog (`execDbGateMSSQLForeignKeySelect`) and bound actual `OnUpdate` and `OnDelete` actions (`CASCADE`, `SET NULL`), fixing DbGate/SSMS schema visualization.
- **Group 6 Execution Engine, Query Planner & Join Remediation (5 Critical Integrity & Optimization Defects)**:
  - `BUG-EXEC-JOIN-STR-CASE-1`: Normalized string join keys in `HashJoinIterator` and `MergeJoinIterator` with `sqlutil.CanonicalIdent` and `CompareValues`, eliminating missing join rows caused by case-variant string join keys (e.g., `'admin'` vs `'ADMIN'`).
  - `BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1`: Reinforced `ParseJoinCond` to extract composite equijoin keys (`a.k1 = b.k1 AND a.k2 = b.k2`) while ignoring non-column literals (e.g. `b.status = 1`), and equipped `HashJoinIterator` and `MergeJoinIterator` with composite delimiter-backed hash/comparison support.
  - `BUG-WINDOW-RANGE-PEER-EVAL-1`: Added `FrameUnit` (`FrameRows` vs `FrameRange`) to `WindowFrame` in planner and execution, and implemented SQL-standard dynamic peer row frame expansion in `WindowIterator` for `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`, guaranteeing identical cumulative aggregates for peer rows sharing identical `ORDER BY` values.
  - `BUG-DECORRELATE-TRIM-1`: Replaced raw `strings.TrimSuffix(before, "and")` in `removeClause` with token boundary and quote-aware keyword trimming (`trimKeywordSuffix`/`trimKeywordPrefix`), preventing truncation of literals or identifiers ending in "and" (e.g. `'Poland'` to `'Pol'`, `brand` to `br`).
  - `BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1`: Added `isInsideOrBranch` guard in `decorrelateExists` and `decorrelateIn`, prohibiting illegal transformation of `OR`-branch subqueries into top-level `SEMI`/`ANTI` joins and preserving exact SQL boolean semantics.
- **Identifier Casing, Canonical Abstraction & Virtual Catalog Remediation (11 Critical Storage/Engine/Catalog Bugs)**:
  - `SQL-CASING-ABSTRACTION-1`: Introduced unified identifier casing utilities and generic `sqlutil.CIMap[V]` case-insensitive hash map in `pkg/sqlutil` with `CanonicalIdent`, `EqualFoldIdent`, `MapGetFold`, `MapSetFold`, and `MapDeleteFold`.
  - `BUG-TABLE-CASE-1`: Ensured case-insensitive and quote-tolerant table lookups in `GetTable` and `DropTable` across `memengine` and `wiredtiger`, preventing `table not found` errors when casing differs.
  - `BUG-STORAGE-TABLE-CASE-SENSITIVITY-1`: Prevented duplicate case-variant table creation in `CreateTable` via `sqlutil.MapGetFold` existence verification.
  - `BUG-DROP-INDEX-CASE-1`: Removed indexes by canonical schema name (`foundIdx.Name`) in `DropIndex`, eliminating zombie index leaks in `td.Indexes`.
  - `BUG-MEM-TX-TABLE-CASE-1`: Normalized table names to canonical schema names in `insertLocked`, `updateLocked`, `deleteLocked` and performed case-insensitive table lookups during `Commit()`, eliminating silent write loss.
  - `BUG-MEM-TX-INDEX-CASE-1`: Implemented case-insensitive index matching fallback in `memengine.IndexScan` and `IndexOnlyScan`.
  - `BUG-TUPLE-RECORD-CASE-1`: Enhanced `storage.Record.Get`, `Set`, and `Delete` with `sqlutil.EqualFoldIdent`, ensuring seamless bracketed (`[Price]`), quoted, and case-variant field resolution without NULL value corruption in `RecordToRow`.
  - `BUG-SP-EXEC-SUFFIX-OUT-1`: Added preceding whitespace and word boundary validation in `cleanOutputSuffix`, preventing variable names ending in "out" (such as `@timeout`, `@layout`, `@shout`) from being truncated.
  - `BUG-STRING-SPLIT-SEP-1`: Rejected empty string separators in `STRING_SPLIT` with standard SQL error instead of silently replacing them with commas.
  - `BUG-CATALOG-IDENT-1`: Resolved multi-part identifier parsing corruption (e.g. `[master].[sys].[tables]`) in virtual catalog `cleanIdentifier` by splitting by dot and cleaning the terminal segment.
  - `BUG-CATALOG-INTERCEPT-BRACKET-1`: Stripped bracketed escaping in `hasCatalogCandidate`, `IsVirtualCatalogJoinQuery`, and `Execute`, enabling 100% intercept fidelity for bracketed queries from SSMS and sqlcmd (e.g. `[sys].[tables]`).
  - `BUG-CATALOG-MSSQL-REGEX-UNICODE-1`: Enhanced virtual catalog regex patterns (`reMssqlTableNameEq`, `reMssqlSchemaNameEq`) to support Unicode literals (`N'...'`) and bracketed table/column names.
- **Group 5 Type Coercion, NULL 3-Valued Logic & Scalar Function Remediation (13 Critical Storage/Builtins/Eval/VM Bugs)**:
  - `BUG-STORAGE-CMP-INT-FLOAT-1`: Supported automatic numeric coercion in `storage.Value.Compare` between `TypeInt` and `TypeFloat`, fixing range scan inversions and point lookup anomalies (e.g., `IntValue(100) < FloatValue(200.5)`).
  - `BUG-STORAGE-CMP-DATETIME-STR-1`: Supported bi-directional date/datetime coercion with string literals in `storage.Value.Compare` and `storage.Value.Equal`, preventing comparison reversals and 0-row query results.
  - `BUG-STORAGE-EQUAL-FLOAT-STR-1`: Replaced string formatting allocation in `storage.Value.Equal` for `TypeFloat` with native IEEE-754 and epsilon precision comparison (`math.Abs(v1 - v2) < 1e-9`).
  - `BUG-STR-NULL-1`: Restored SQL-standard NULL propagation across string built-in functions (`UPPER`, `LOWER`, `TRIM`, `LTRIM`, `RTRIM`, `LEFT`, `RIGHT`, `SUBSTRING`, `REPLACE`, `REVERSE`, `LEN`, `SPACE`, `REPLICATE`).
  - `BUG-STR-CHARINDEX-NULL-ZERO-1`: Restored SQL three-valued logic for `CHARINDEX` and `STRPOS` when receiving NULL arguments, returning `NULL` instead of `0`.
  - `BUG-DATEADD-NULL-1`: Enforced NULL propagation in `DATEADD` and `EOMONTH` when receiving NULL increments or NULL dates, eliminating silent fallback to 0.
  - `BUG-DATE-TODATETIMEOFFSET-INT-MINS-1`: Interpreted colon-less integer time zone offsets in `TODATETIMEOFFSET` as total minutes (e.g. `-120` -> `-02:00`, `+330` -> `+05:30`), resolving a 60x hour distortion.
  - `BUG-EVAL-NEQ-NULL-1`: Enforced SQL three-valued logic in `evalRowBinaryOp` for inequality comparisons (`<>`, `!=`) by returning `(nil, nil)` on NULL operands, preventing NULL rows from leaking into WHERE clauses.
  - `BUG-ARITH-EXPR-ZERO-1`: Introduced robust operator splitting and operand resolution in `evalUpdateExpr`, supporting bracketed column identifiers (e.g., `[Price] + 10`), constant-leading arithmetic (`10 + [Price]`), and column-to-column expressions (`salary + bonus`).
  - `BUG-DML-EVAL-UPDATE-ARITH-CORRUPT-1`: Added explicit divide-by-zero detection (`divide by zero error encountered`) in coordinator `UPDATE` and VDBE VM `OpUpdate`/`OpUpdateWhere`, preventing silent zero resets and rollback failures.
  - `BUG-AGG-MIXED-NUM-DEC-1`: Preserved integer and float sums during Volcano `AggregateIterator` execution when mixed with Decimal values in the same group, dynamically promoting and accumulating into `decSums`.
  - `BUG-API-JSON-DATETIME-EMPTY-STR-1`: Initialized `DateTimeValue.SVal` with ISO-8601 formatting and enforced RFC3339 serialization in REST API record handlers, preventing empty string (`""`) emissions.
  - `BUG-API-JSON-DECIMAL-PRECISION-LOSS-1`: Serialized DECIMAL values as `json.Number` in REST API handlers, eliminating floating-point IEEE-754 precision loss during JSON marshalling.
- **Group 4 Tokenization, Comma Parsing & Smart Split Remediation (15 Critical Engine/Parser/Planner/Wire Bugs)**:
  - `SQL-TOKENIZER-1`: Established zero-allocation unified SQL clause tokenizer abstraction (`pkg/sqlutil`) with quotes (`'`, `"`, `''`), bracket identifier (`[...]`), comment (`--`, `/* */`), and nested parenthesis depth tracking.
  - `BUG-SPLIT-VALUES-1`: Handled commas inside functions (e.g. `COALESCE(a, b)`) and strings in `INSERT INTO ... VALUES` without column count mismatch.
  - `BUG-MERGE-SET-1`: Preserved multi-argument expressions and commas in strings in MERGE statement `UPDATE SET` clause via `sqlutil.SplitSetClauses`.
  - `BUG-UPDATE-SET-SPLIT-1`: Eliminated silent expression loss in coordinator `ExecuteUpdate` `SET` clause when values contain commas in functions or expressions.
  - `BUG-UPDATE-SET-COMMA-SPLIT-1`: Prevented string literal truncation in coordinator `UPDATE SET` when assigning values containing commas (e.g. `'Seoul, Korea'`).
  - `BUG-EVAL-IN-COMMA-SPLIT-1`: Prevented condition splitting of string literals with commas inside `WHERE IN (...)` lists via top-level comma splitting.
  - `BUG-ORDER-BY-SPLIT-1`: Resolved multi-column sort dropping, `DESC,` comma parse errors, and casing failures across coordinator SELECT using `sqlutil.ParseOrderBy`.
  - `BUG-CATALOG-ORDERBY-1`: Enabled multi-column, case-insensitive, type-aware sorting in virtual catalog and DBCC queries.
  - `BUG-PLANNER-ORDERBY-1`: Fixed CBO plan node and SetOp trailing `ORDER BY` generation by preserving full multi-column sort keys.
  - `BUG-PIVOT-ORDER-BY-SPLIT-1`: Supported multi-column sorting with distinct column directions in PIVOT and UNPIVOT queries.
  - `BUG-DDL-SPLIT-QUOTE-1`: Prevented schema corruption on `CREATE TABLE` when column `DEFAULT` definitions contain commas or parentheses inside quotes (e.g. `'draft, pending'`).
  - `BUG-DDL-COMPOSITE-PK-PARSE-1`: Correctly parsed all columns in table-level composite primary keys (e.g. `PRIMARY KEY (col1, col2)`).
  - `BUG-SP-PARAM-DECIMAL-1`: Preserved parameterized types with commas (e.g. `DECIMAL(10, 2)`) in stored procedure parameter declarations without generating phantom parameters.
  - `BUG-JOIN-PARSE-1`: Supported multi-condition equijoins in `execution.ParseJoinCond` and eliminated false table matching in PGWire schema descriptions on queries containing `SUBSTRING(... FROM ...)`.
  - `BUG-PLANNER-SPLIT-AND-QUOTE-ESCAPE-1`: Prevented false `AND` splitting inside string literals with escaped quotes (e.g. `'O''Reilly AND Sons'`) and bracket identifiers in planner decorrelation and pushdown.
  - `BUG-PROJ-CAST-AS-1`: Prevented `CAST(col AS type)` expressions from being truncated at inner `" AS "` keywords during projection schema compilation.
- **Group 3 Primary Key Heuristics, Guessing & Lifecycle Remediation (10 Critical Engine/Storage/Wire Bugs)**:
  - `BUG-DDL-PK-1`: Eliminated arbitrary `Columns[0]` promotion to Primary Key on PK-less `CREATE TABLE`; introduced clean synthetic `_row_id INT IDENTITY PRIMARY KEY` with `Hidden: true` metadata flag.
  - `BUG-HTTP-TABLE-PK-1`: Eliminated `cols[0]` promotion in HTTP table creation; normalized parameterized types (e.g. `VARCHAR(100)`) and rejected unsupported types with 400 Bad Request.
  - `BUG-SELECT-INTO-PK-1`: Eliminated arbitrary unique column or `Columns[0]` PK guessing in `SELECT ... INTO`; assigned synthetic `_row_id` when source lacks a natural primary key.
  - `BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1`: Eliminated `Columns[0]` promotion in 0-row or duplicate derived tables; always injected synthetic `_dt_row_id INT IDENTITY PRIMARY KEY`.
  - `BUG-CTE-MATERIALIZE-1`: Eliminated forcing column 0 as PK and `VARCHAR(255)` coercion in materialized CTEs; injected synthetic `_cte_row_id` and preserved underlying query column types.
  - `BUG-DML-PK-RANDOM-1`: Eliminated guessing `"id"` in coordinator `ExecuteUpdate` and `ExecuteDelete`; resolved primary key via `schema.ResolveRecordPK(rec)`.
  - `BUG-DML-PK-RANDOM-GUESS-1`: Eliminated guessing `"id"` in VDBE `OpUpdate` and `OpDelete`; resolved primary key via `schema.ResolveRecordPK(rec)`.
  - `BUG-VM-DML-PK-NOT-FOUND-CRASH-1`: Handled `storage.ErrKeyNotFound` in VDBE `OpUpdate` and `OpDelete` as 0 rows affected (`f.rowCount = 0`, nil error) per standard SQL specification instead of crashing stored procedures.
  - `BUG-COPY-PK-1`: Preserved integer primary key `0` in PGWire `COPY FROM STDIN` without overwriting with timestamps.
  - `BUG-API-PK-GEN-1`: Prevented timestamp collisions in HTTP REST record creation by combining microsecond timestamp with atomic counter (`atomic.Uint64`) and UUID fallback, and resolved txPool use-after-free rollback race.
- **Group 2 Unified SQL Abstraction Remediation (14 Critical Engine/Storage/Wire Bugs)**:
  - `BUG-STORAGE-UPDATE-CASE-1`: Normalized column keys against table schema in `memengine` and `wiredtiger` `updateLocked` to eliminate silent update omissions under mixed casing.
  - `BUG-VM-DML-COL-CASE-1`: Normalized VDBE `OpInsert` and `OpUpdate` column names using schema metadata to prevent NULL value overwrites.
  - `BUG-INDEX-KEY-CASE-1`: Extracted secondary index keys case-insensitively via unified `storage.ExtractIndexKeyFromRecord`.
  - `BUG-WT-UNIQUE-INDEX-UNENFORCED-1`: Enforced WiredTiger secondary unique index constraints via lock-free cursor pre-checks (`checkUniqueIndexesLocked`).
  - `BUG-FK-CASE-1`: Enabled case-insensitive parent column lookups in `memengine` `findMatchingChildPKsUnderLock` to prevent illegitimate parent deletions.
  - `BUG-FK-CHILD-CASE-1`: Applied `childRecord.Get` in `validateForeignKeysUnderLock` to prevent bogus foreign key insertions.
  - `BUG-WT-FK-1`: Built full foreign key integrity verification and `ON DELETE CASCADE/RESTRICT` cascading in the WiredTiger CGO backend.
  - `BUG-FK-PARENT-TABLE-CASE-1`: Guaranteed case-insensitive parent and child table resolution across DDL and transaction validations.
  - `BUG-TDS-ROW-CASE-1`: Added case-insensitive column resolution (`getRowValueCaseInsensitive`) to `WriteRowDirect` and `WriteNBCRowDirect` in TDS token serialization.
  - `BUG-SELECT-AGG-CASE-1`: Guaranteed case-insensitive `GROUP BY` grouping and aggregate argument evaluation in the coordinator.
  - `BUG-COORD-AGG-MIN-MAX-NON-NUMERIC-NIL-1`: Added generic non-numeric value comparison (`compareAggValues`) to coordinator MIN/MAX aggregates.
  - `BUG-EXEC-AGG-DISTINCT-IGNORED-1`: Preserved the DISTINCT flag in parser and Volcano `AggregateIterator` with per-group deduplication caching.
  - `BUG-JOIN-IDX-1`: Replaced arbitrary `Indexes[0]` selection with safe HashJoin fallback when join conditions lack matching indexes.
  - `BUG-PLANNER-CBO-SELECTIVITY-CASE-SENSITIVE-1`: Integrated `findColumnStats` into `EstimateSelectivity` for case-insensitive column histogram lookups.
- **Session Variable Quote Escaping (`pkg/engine/sqlexec.go`, `pkg/engine/query_utils.go`)**: Fixed single quote unescaping in `ParseSessionVarValue` and implemented proper doubled single quote (`''`) escaping during `@var` query substitution to prevent SQL syntax errors and injection vulnerabilities.
- **Session Table Variable & Temporary Table Lifecycle Cleanup (`pkg/engine/sqlexec.go`, `pkg/engine/coordinator_impl.go`)**: Ensured all physical table variables (`__tv_*`) and temporary tables are cleanly dropped from the storage engine on `CloseSession` (including `default` session on `Coordinator.Close`) and on connection pool resets (`DISCARD ALL`, `RESET ALL`).

### Added
- **Production Superuser Security Guardrail (`PROD-SEC-1`) (`cmd/lionsql`, `cmd/server`)**: Enforced production safety guard that actively blocks server startup when binding to non-localhost / external interfaces (e.g. `0.0.0.0` or public IP) with the default administrative superuser password (`1234567890`), requiring explicit password change (`admin_password`) or localhost-only binding (`127.0.0.1`).
- **1.0 Pre-Release Architecture Review Report (`doc/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`)**: Documented comprehensive technical feedback and 5-point action plan covering brand consolidation, escape vulnerability fixes, query execution paths, and production security guards before 1.0 LTS release.

### Changed
- **Brand Unification & Clean SDK Standard (`BRAND-UNIFY-1`) (`pkg/api`, `pkg/ui`, `pkg/wire`, `pkg/proc`, `cmd/tpcc`, `cmd/lionsql`)**: Standardized all remaining branding to LionSQL across JavaScript/TypeScript Web SDK (`lionsql.js`, `lionsql.d.ts`, `class LionSQL`), webhook & CDC outbox HTTP headers (`X-LionSQL-Signature`, `X-LionSQL-Event`, `X-LionSQL-Delivery`), wire protocol server version and banners (PostgreSQL v3 `15.0 (LionSQL)`, TDS 7.x/8.0 `LionSQL Engine`, `@@SERVERNAME: LIONSQL`), and diagnostic probe files. Cleanly eliminated legacy `lionsql` aliases per 1.0 production specification.

## [0.90.0]

### Added
- **Documentation Overhaul & README Overview Refinement**: Refined and modernized the executive overview at the very top of both `README.md` and `README.ko.md` to prominently foreground LionSQL's five core architectural pillars: (1) Native Microsoft SQL Server T-SQL and Stored Procedure Execution on VDBE, (2) Broad PostgreSQL Wire Protocol v3 and SQL dialect compatibility, (3) Embedded zero-dependency Web Studio & REST BaaS API, (4) Ultra-compact single static standalone executable (< 100MB, ~50MB), and (5) High-performance OLTP throughput (16k~28k+ TPS) with Non-Blocking Snapshot Isolation.
- **Exhaustive Manuals & Performance Guides (`doc/USER_MANUAL.ko.md`, `doc/REFERENCE_MANUAL.ko.md`, `doc/TRANSACTION_PERFORMANCE_GUIDE.ko.md`)**: Published an independent transactional performance optimization guide and deeply expanded both the User Manual (1,307 lines) and Reference Manual (1,770 lines) into exhaustive, production-grade enterprise specifications.
- **Comprehensive Web Studio User Manual (`doc/WEB_STUDIO_GUIDE.ko.md`)**: Published dedicated, full-scope user guide and reference manual covering access & authentication, overview dashboard & real-time monitoring, table & schema management (DDL viewer, safe drop confirmation), data grid & CRUD operations, CSV/JSON import/export, SQL console & EXPLAIN plan visualization, Developer API Preview (cURL, Fetch, Web SDK), live query & audit logs viewer, and hot backup/restore.
- **Web Studio Production Feature Suite (`UI-DDL-1`, `UI-REALTIME-1`, `UI-DATA-1`, `UI-LOGS-1`) (`pkg/storage`, `pkg/engine`, `pkg/api`, `pkg/ui`)**:
  - **Visual Column Addition GUI (`UI-DDL-1`)**: Added `+ Add Column` modal to Schema Structure view, allowing dynamic addition of columns with types and nullable/default constraints without manual SQL typing. Backed by `AddColumn` interface on `storage.Engine`, In-Memory OCC WAL (`OpAlterTable`) logging/replay, WiredTiger lock-free B+Tree cursor scan and tuple rewrite (`C.go_wt_cursor_update_raw`), and unified `ALTER TABLE <table> ADD [COLUMN] ...` SQL executor.
  - **Realtime CDC / SSE Stream Inspector Viewer (`UI-REALTIME-1`)**: Added dedicated `Realtime Live Stream` view in sidebar connected directly to `/api/realtime` SSE stream. Features bounded 200-event circular ring buffer, live visual event cards with semantic action badges (`CREATE`, `UPDATE`, `DELETE`, `DDL`), live table & action filtering, real-time query search, pause/resume controls, and one-click JSON payload diff clipboard copy.
  - **Record Table Multi-Row Bulk Actions & Foreign Key Jump (`UI-DATA-1`)**: Added multi-row checkbox selection with bulk action bar (`Delete Selected`, `Export Selected (CSV)`, and `Export Selected (JSON)`). Exposed foreign key metadata in `TableSchemaDTO.ForeignKeys` and heuristic relation detection, rendering interactive jump links (`↗`) on FK cells with popup details modal (`#fk-relation-modal`) and one-click navigation to the referenced parent table.
  - **Realtime HTTP API Request Activity Logs Viewer (`UI-LOGS-1`)**: Added thread-safe bounded circular ring buffer (`RequestActivityLogger`, 200 entries) and request logging middleware capturing HTTP method, path, status, latency in microseconds/milliseconds, and client IP. Exposed via `GET /api/logs/requests` and `DELETE /api/logs/requests`, with dedicated `Request Logs` Web Studio view featuring 3-second auto-refresh polling, live pause/resume toggle, method/status/search filters, and instant log purge.
- **HTTP Server Hardening & Frontend Connectivity (`HTTP-1` ~ `HTTP-4`) (`pkg/api`)**:
  - Implemented zero-dependency global CORS middleware (`corsAndSecurityMiddleware` in `pkg/api/middleware.go`) supporting wildcard origins (`*`), full REST methods (`GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`), and explicit preflight fast-path (`204 No Content`), enabling seamless client fetch from Vite, React, Vue, Svelte, and Next.js dev servers.
  - Implemented transparent HTTP Gzip response compression (`gzipMiddleware`) with `sync.Pool` writer pooling, reducing Web Studio SPA payload transfer size by ~77% (198KB ➔ ~45KB) while cleanly bypassing streaming endpoints (`/api/cdc/stream`, `/api/realtime`).
  - Added request body size limit guard (`http.MaxBytesReader`, default 32MB) to prevent denial-of-service memory starvation on malformed payloads in compliance with Golden Rules 1 and 23.
  - Hardened default security response headers: `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`, and `Referrer-Policy: strict-origin-when-cross-origin`.
- **Web Studio Management UI/UX Modernization & Developer API Preview (`UI-1` ~ `UI-10`) (`pkg/ui/dashboard.html`, `pkg/ui/embed.go`, `pkg/ui/embed_test.go`, `pkg/api/server.go`, `pkg/api/handlers_records.go`)**:
  - Replaced all 13 legacy blocking browser `alert()` popups with a modern, non-intrusive Toast notification component (`showToast`) featuring emerald success, rose error, and slide-in/fade-out animations (`UI-1`).
  - Added responsive layout support (`@media (max-width: 900px)`) with mobile sticky navigation bar and drawer hamburger toggle (`toggleSidebar`), ensuring clean readability and horizontal scrolling on laptops and split-screen windows (`UI-2`).
  - Fully eliminated legacy brand references (`LionSQL`, `X-LionSQL-Signature`, `lionsql serve` CLI commands) and standardized administrative account on `admin@lionsql.local` (`UI-3`).
  - Added real-time instant search filter (`#sidebar-quick-search` / `onSidebarQuickSearch`) in the sidebar for immediate filtering across large table and stored procedure catalogs (`UI-4`).
  - Added password visibility eye toggle button (`togglePasswordVisibility`) with clean inline SVG icons to login modal and SMTP settings for effortless typo inspection (`UI-5`).
  - Implemented interactive Drop Table safety confirmation modal (`#drop-table-modal` / `dropCurrentTable` / `executeDropTable`) requiring the user to type the exact table name before activating the destructive action button, preventing accidental data loss in production (`UI-6`).
  - Hardened sample data seeding (`POST /api/seed` / `seedSampleData()`) with strict empty database validation: rejects seeding with `409 Conflict` and descriptive error toast when user tables already exist, executes atomic ACID transaction seeding on empty databases with success toast, and automatically selects and navigates to the first seeded table (`accounts`) (`UI-7`).
  - Upgraded Developer API Preview modal (`#api-preview-modal` / `openAPIPreviewModal`) with PocketBase-grade dynamic schema awareness: automatically analyzes currently selected table columns (`getDummyRecordForTable`) to exclude auto-increment PKs and generate realistic dummy payloads typed according to column definitions (INT, VARCHAR, DECIMAL, DATETIME, JSON, BOOL) and semantic column names (`UI-8`).
  - Added first-class LionSQL Official SDK tab (`#api-lang-sdk` / `lionsql.js`) to the Developer API Preview modal, providing instant copy-paste ready code snippets for `import LionSQL`, `db.collection('...').getList()`, `.getOne()`, `.create()`, and `db.query()` alongside cURL, Fetch API, Python, and Go (`UI-9`).
  - Added Light / Dark mode theme switcher (`html[data-theme="light"]`, `#theme-toggle-btn`, `toggleTheme()`) with `localStorage` persistence and inline FOUC prevention script, resolved primary key (`id`) yellow text washout by transitioning to high-contrast deep amber (`.cell-pk-val`), refreshed column tags and table hover contrast, and enforced strict `Cache-Control: no-cache, no-store, must-revalidate` headers on `GET /_/` to permanently prevent stale browser caching across daemon updates (`UI-10`).
- **HTTP REST & Web Management Port 8090 Functional Verification Suite (`HTTP-TEST-1`) (`pkg/api`, `doc/HTTP_API_FUNCTIONALITY_REPORT.md`)**:
  - Implemented end-to-end integration test suite (`pkg/api/http_port_integration_test.go`) over live TCP socket listeners covering 37 comprehensive scenarios across all 8 core domains.
  - Verified 100% operational pass rate across: System Health/Version SSOT (`GET /api/health`), Web Studio Dashboard UI (`GET /_/`), Static Client SDK Distribution (`GET /sdk/lionsql.js`, `GET /sdk/lionsql.d.ts`), Authentication & API Key RBAC (`/api/auth/*`, `/api/keys`), Declarative Table Schema DDL (`/api/tables/*`), Auto-Generated RESTful Records CRUD & Filtering (`/api/records/*`), SQL Query & T-SQL Stored Procedure Execution (`/api/sql`, `/api/procs/*`), Transactional CDC & Realtime Streaming (`/api/cdc/stream`, `/api/realtime`), Prometheus & OpenTelemetry Metrics (`/metrics`, `/v1/metrics`), and High-Concurrency Multiplexed Traffic (20 parallel workers, 200 concurrent requests, 0 data races).
  - Authored official comprehensive verification report in `doc/HTTP_API_FUNCTIONALITY_REPORT.md`, achieving complete tripartite port parity alongside PostgreSQL Wire (`:5433`) and TDS MSSQL (`:1433`).
- **Global Memory Governor & Query Circuit Breaker (`MEM-1`) (`pkg/config`, `pkg/execution`, `cmd/server`)**:
  - Implemented dynamic global memory budget management allocating up to 70% of physical RAM (`max_memory`, auto-detected via OS primitives) and per-query operational memory bounds (`work_mem: 64MB`).
  - Added reactive memory circuit breaker in hash joins and external sort pipelines that halts runaway queries exceeding system bounds with descriptive SQLSTATE errors instead of triggering OS OOM-killer panics.
  - Added real-time memory budget logging to server startup banner (`[Memory Governor] max_memory: ...`).
- **Unified Single Binary CLI & Operational Lifecycle (`UNIFY-CLI`) (`cmd/lionsql`, `cmd/server`, `pkg/config`)**:
  - Consolidated server and CLI tools into a single autonomous binary (`bin/lionsql`), adopting the modern PocketBase/CockroachDB CLI model where running without arguments or with flags defaults immediately to starting the server in foreground (`lionsql`, `lionsql -dir=./data`).
  - Added modern first-class subcommands: `serve`, `start` (daemon), `stop`, `restart`, `status`, `doctor`, `init-config`, `migrate`, `sql`, `backup`, `restore`, and `version`.
  - Added authoritative single source of truth for release versioning in `pkg/config/version.go` (`const Version = "0.90.0"`).
  - Converted `cmd/server/main.go` into a lightweight, backward-compatible delegation wrapper emitting deprecation guidance (`[LionSQL Notice] 'bin/server' is deprecated...`) and passing all arguments directly to `bin/lionsql`.
  - Added startup security warning banner reminder (`[SECURITY WARNING]`) when default administrative superuser credentials (`1234567890`) remain in use.

### Fixed
- **Virtual Catalog Database Standardization (`CATALOG-CLEANUP`) (`pkg/catalog/virtual`, `pkg/engine`)**:
  - Removed legacy database namespaces across virtual catalog and SQL engine to strictly standardize on protocol canonical catalogs: `postgres` (OID: 1) for PGWire and `master` (database_id: 1) for TDS.
  - Aligned database listing metadata (`pg_database`, `INFORMATION_SCHEMA.SCHEMATA`, `sys.databases`) to present only official protocol databases in third-party database GUI clients (Beekeeper Studio, DBeaver, Navicat).
- **Long-Lived SSE Stream Timeout & Latency Profiling Immunity (`SSE-STREAM-1`) (`cmd/lionsql`, `pkg/api`)**:
  - Replaced global HTTP `WriteTimeout: 15s` with `ReadHeaderTimeout: 15s` and `IdleTimeout: 60s`, preventing forced SSE disconnections.
  - Added explicit `rc.SetWriteDeadline(time.Time{})` in `/api/realtime` and `/api/cdc/stream` handlers to ensure infinite keep-alive streaming.
  - Excluded streaming endpoints from the HTTP request activity logger to prevent artificially skewed 15+ second latency averages in the Web Studio monitor.
- **Graceful Shutdown Race Condition & WiredTiger SIGSEGV Prevention (`SHUTDOWN-1`) (`pkg/wire/server.go`, `pkg/storage/wiredtiger/wt_engine.go`, `pkg/storage/wiredtiger/wt_engine_test.go`)**:
  - Fixed a race condition where PGWire client connection handler goroutines exiting during shutdown invoked session close on a WiredTiger connection that was concurrently being closed or already freed by the engine shutdown coordinator, triggering a CGO segmentation fault (`SIGSEGV: addr=0xad0`).
  - Added `sync.WaitGroup` lifecycle tracking and a 1-second bounded drain in `pkg/wire/server.go` (`wire.Server.Close()`), ensuring all active connection handlers and their deferred session releases complete before `wireServer.Close()` returns.
  - Added active session pre-drain in `pkg/storage/wiredtiger/wt_engine.go` (`Engine.Close()`) to cleanly close all active `WTSession`s before `C.go_wt_close(e.conn)`.
  - Added atomic `connClosed` guard in `WTSession.Close()` to safely bypass CGO calls if the underlying connection was already terminated.
  - Added unit test `TestWiredTiger_SessionCloseAfterEngineClose_NoPanic` verifying 100% crash-free session teardown.
- **Web Studio UI Light Mode High-Contrast & Readability Overhaul (`UI-11`) (`pkg/ui/dashboard.html`, `pkg/ui/embed_test.go`)**:
  - Eliminated low-contrast pastel washouts across all modals, badges, alerts, and tables in Light Mode, achieving full WCAG AAA compliant contrast ratios (>= 7:1).
  - Overhauled Drop Table Confirmation modal warning box and prompt text from illegible pastel pink (`#fca5a5`) to semantic alert tokens with deep burgundy text (`--alert-danger-text: #991b1b`) and bold crimson prompt highlight (`--alert-danger-strong: #b91c1c`).
  - Replaced low-contrast yellow primary key (`id`) table values and tags with authoritative, crisp high-contrast blue tokens (`--pk-val-color: #1d4ed8` in Light Mode, `#60a5fa` in Dark Mode).
  - Standardized all error and alert banners (`create-table-error`, `login-error`, `create-key-error`, `create-cron-error`, `create-webhook-error`, `import-csv-error`, `promote-replica-modal`) on `.alert-danger-box` and `.btn-danger-subtle`.
  - Converted all JavaScript dynamic table renderers (`renderWebhookDeliveries`, `renderCrons`, `renderMailLogs`, `renderReplicasTable`, `renderReplicationStandalone`, `updateReplicationSidebarBadge`) from hardcoded inline styles to theme-aware semantic classes (`.badge-success`, `.badge-danger`, `.badge-warning`, `.badge-info`, `.badge-purple`, `.badge-muted`).
  - Resolved dark-on-dark washout in Developer API Preview modal by eliminating hardcoded `#1e293b` on unselected endpoint and language tabs, replacing with semantic tokens (`--tab-inactive-bg: #ffffff` in Light Mode, `--tab-inactive-color: #334155`) and crisp border styling.
- **Web Studio Default Active Status & View Isolation Overhaul (`UI-12`) (`pkg/ui/dashboard.html`, `pkg/ui/embed_test.go`)**:
  - Fixed a structural HTML nesting bug where `#view-sdk` was missing its closing `</div>` tag, inadvertently causing `#view-replication`, `#view-realtime`, and `#view-logs` to be nested inside the hidden SDK view (`display: none`), which rendered the entire right-hand pane blank when switching views.
  - Added unit test `TestDashboardViewSeparationAndStructure` to strictly enforce balanced opening and closing tags between adjacent view panels.
  - **Replication (HA)**: Maintained connected replicas card visibility in standalone mode, displaying an active `STANDALONE PRIMARY` status row (`standalone-primary (Local)`, `127.0.0.1`, `ACTIVE / WRITER`, `0x0001 (Local LSN)`, `0 (Authoritative)`) alongside high-availability 1-Writer Multi-Reader architecture and deployment guide.
  - **Realtime Live Stream**: Pre-seeded live stream circular buffer with an active system SSE listener card (`STREAM_LISTENER_ACTIVE`, `/api/realtime`, protocols: `HTTP / PGWire (:5433) / TDS (:1433)`) with semantic `READY` badge and expandable JSON payload inspection.
  - **Request Logs**: Updated static HTML placeholder and JavaScript empty fallback to immediately display an active `GET /api/health (Engine Active & Ready)` status row on initial boot.
- **Version Single-Source-of-Truth Synchronization (`pkg/api`, `pkg/backup`, `pkg/ui`)**:
  - Synchronized `GET /api/health` JSON response to use `config.Version` instead of legacy hardcoded `"0.7.0"`.
  - Synchronized backup and WAL archive manifest metadata (`backup.go`, `wal_archive.go`) to reference `config.Version`.
  - Updated Web Studio dashboard HTML SDK download badge to dynamically reflect `v0.90.0`.

### Changed
- **Branding & Global Security Defaults (`UNIFY-CLI`) (`pkg/config`, `pkg/api`, `pkg/engine`, `pkg/ui`)**:
  - Standardized all superuser accounts, documentation, and UI templates on `admin@lionsql.local` (standardized on `admin@lionsql.local`).
  - Strengthened default REST authentication policy to `auth.enabled: true` (Secure by Default).
  - Increased slow query recording threshold default from `50.0ms` to `100.0ms` (`cfg.Logging.SlowQuery.ThresholdMS`).
  - Standardized configuration file discovery strictly on `lionsql.json` and PID files on `lionsql.pid` / `lionsql.log`.
  - Implemented strict legacy configuration rejection: detection of `lionsql.json` emits a warning and is ignored without silent fallback to prevent configuration hijacking.

### Fixed
- **Multi-Table Relational JOIN Column Resolution & LazyTuple Decoding (`PG-COMPAT-3` / `PG-ORM-9`) (`pkg/execution`, `pkg/wire`)**:
  - Implemented `Tuple.Materialize()` in `pkg/execution/iterator.go` to force decoding of underlying `storage.LazyTuple` (used by the WiredTiger engine) into the `Values` slice before merging tuples or evaluating projections, resolving an issue where unprojected columns in joined relations evaluated to `nil`/`NULL`.
  - Ensured all join implementations (`mergeTuplesWithArena`, `mergeTuplesWithNullsAndArena`, `mergeNullsWithTupleAndArena` in `pkg/execution/join.go` and `pkg/execution/merge_join.go`) materialize child tuples prior to combining value slices.
  - Corrected projection field resolution order in `ProjectionIterator.projectTuple` (`pkg/execution/project.go`) to prioritize qualified expressions (`f.expr`, e.g. `o.amount`, `u.name`) over unqualified aliases (`f.alias`), preventing column shadowing when multiple joined tables share identical column names.
  - Defensively hardened `MergeColumnIndexMaps` with fallback `leftLen` boundary calculation when `leftCols` is empty.
  - Added E2E driver integration tests in `pkg/wire/driver_integration_test.go` (`TestDriverIntegration_LibPQ_RelationalJoin`) and `pkg/wire/driver_integration_wt_test.go` (`TestDriverIntegration_WiredTiger_RelationalJoin`), raising PGWire compliance across all 57 test scenarios to 100.0%.

## [0.24.0]

### Added
- **Per-Session Prepared Statement Bounded LRU Cache (`PREP-1`) (`pkg/wire`, `pkg/wire/tds`)**:
  - Implemented thread-safe `TdsPreparedStatementCache` (`pkg/wire/tds/server.go`) and `PreparedStatementCache` (`pkg/wire/server.go`) based on standard library `container/list` and mapped list elements, enforcing a default bound of 256 prepared statements per session (`DefaultMaxPreparedPerSession`).
  - Added LRU promotion on `Get` / `Put` access and automatic eviction of least recently used statements when session capacity is reached, preventing unbound map growth and out-of-memory (OOM) risks across long-lived connection pools (DBeaver, GORM, HikariCP) in compliance with Golden Rule 23.
  - Added `SetMaxPreparedPerSession` configuration method on TDS and PGWire server instances.
  - Added unit and E2E eviction integration tests in `pkg/wire/tds/lru_test.go` and `pkg/wire/lru_test.go` verifying LRU eviction behavior, MRU preservation, concurrent safety, and protocol Error 8179 handling upon accessing evicted statement handles.

### Fixed
- **PGWire Untyped Parameter Evaluation Wire Length & Schema Metadata Synchronization (`PG-COMPAT-1`) (`pkg/wire`)**:
  - Added cached `ColumnNames []string` and `ColumnOIDs []int32` to `PreparedStatement` struct in `pkg/wire/server.go`, ensuring resolved column metadata from Describe stage (`writeSchemaDescription`) is retained and strictly reused during Execute stage (`writeExecutionResult`, `executePortal`).
  - Refined `buildPreviewSQL` to default untyped parameter placeholders to text (`'sample'`) matching PostgreSQL standard behavior when no arithmetic operators are present, resolving the issue where untyped placeholders were prematurely inferred as `int4` (OID 23) in `RowDescription` and then serialized as 3-byte strings (e.g. `"100"`), which caused `lib/pq` client runtime out-of-range slice panics.
  - Added comprehensive E2E driver integration test `TestDriverIntegration_LibPQ_UntypedParameter` in `pkg/wire/driver_integration_test.go` verifying integer, string, and multi-parameter binding ($1, $2).
- **Duplicate Primary Key Unique Constraint Enforcement (`PG-COMPAT-2` / SQLSTATE 23505) (`pkg/storage/wiredtiger`, `pkg/engine`, `pkg/proc`, `pkg/wire`)**:
  - WiredTiger B+Tree insertion (`pkg/storage/wiredtiger/wt_engine.go` `insertLocked`) now explicitly verifies key existence via `go_wt_cursor_get` before raw insertion, eliminating silent overwrites and returning `storage.ErrKeyExists`.
  - Formatted unique key constraint violation errors in `pkg/engine/sqlexec_dml.go` and `pkg/proc/runtime/vm_dispatcher.go` as `duplicate key value violates unique constraint on "<table_name>"`.
  - Mapped unique violation errors to standard PostgreSQL error code `23505` (`unique_violation`) in `pkg/wire/protocol.go` (`WriteError`).
  - Added unit test in `pkg/storage/wiredtiger/wt_engine_test.go` (`TestWiredTiger_DuplicatePrimaryKey_ErrKeyExists`) and E2E driver integration test in `pkg/wire/driver_integration_test.go` (`TestDriverIntegration_LibPQ_DuplicatePrimaryKey`).
- **WiredTiger `TRUNCATE TABLE` Transaction Isolation & Assertion Crash Prevention (`TDS-COMPAT-1` / Scenario 39-1) (`pkg/storage/wiredtiger`, `pkg/engine`, `pkg/wire/tds`)**:
  - Implemented active session tracking (`e.sessions map[*WTSession]struct{}`) and target table idle cursor eviction (`e.evictAllCursors(tableName)`) in `pkg/storage/wiredtiger/wt_engine.go` prior to truncation, eliminating WiredTiger C library cursor contention.
  - Removed unsupported `index:` URI truncation loop that caused C library `unsupported object operation` errors.
  - Enhanced `execTruncateTable` in `pkg/engine/sqlexec_ddl.go` to receive `cs *ClientSession` and perform atomic, transaction-isolated record deletion within `cs.Tx` when called during an active transaction (`cs.Tx != nil`), guaranteeing full removal upon `COMMIT` and complete restoration upon `ROLLBACK` in accordance with T-SQL and ACID invariants.
  - Added physical table mapping for temporary tables (`cs.TempTableMap`) during `TRUNCATE TABLE #temp`.
  - Added storage engine unit tests in `pkg/storage/wiredtiger/wt_engine_test.go` (`TestWiredTiger_TruncateTable`) and E2E TDS driver integration tests in `pkg/wire/tds/driver_integration_wt_test.go` (`TestDriverIntegration_WiredTiger_TruncateTable`) verifying autocommit, explicit in-transaction commit, and in-transaction rollback integrity.
- **Native DATETIME Binary Serialization for `GETDATE()` and Timestamp Built-ins (`TDS-TYPE-1` / Scenario 29) (`pkg/engine`, `pkg/proc`, `pkg/wire/tds`)**:
  - Updated `evalScalarExpr` in `pkg/engine/sqlexec_select.go` and `evalDateBuiltin` in `pkg/proc/builtins_date.go` to return Go `time.Time` and `storage.DateTimeValue(time.Now())` for `GETDATE()`, `CURRENT_TIMESTAMP`, `GETUTCDATE()`, and `SYSDATETIME()` instead of formatted strings.
  - Enhanced T-SQL Pratt expression parser in `pkg/proc/parser_expr.go` to parse zero-argument SQL standard functions `CURRENT_TIMESTAMP`, `CURRENT_DATE`, and `CURRENT_TIME` without parentheses as `FunctionCallExpr`.
  - Configured TDS wire serialization in `pkg/wire/tds/server.go` to assign `TypeDateTimeN` (0x6F, 8-byte) metadata for `getdate`, `now`, and `current_timestamp` columns, enabling client drivers (`go-mssqldb`) to natively scan into `*time.Time` while maintaining string scan compatibility.
  - Added unit test `TestSQLExecutor_GetDateNativeTimestamp` in `pkg/api/sqlexec_test.go` and E2E driver integration test `TestDriverIntegration_GoMSSQLDB_GetDateNativeScan` in `pkg/wire/tds/driver_integration_test.go`.
- **Unicode N-prefix (`N'...'`) String Literal Support in T-SQL Lexer and Engine (`TDS-COMPAT-2`) (`pkg/proc`, `pkg/engine`, `pkg/wire/tds`)**:
  - Fixed T-SQL lexer (`pkg/proc/lexer.go`) to recognize `N'...'` and `n'...'` as `TokenString` rather than tokenizing `N`/`n` as an identifier, eliminating parsing errors and driver stream deserialization failures.
  - Enhanced `isSingleQuotedLiteral` and `evalScalarExpr` in `pkg/engine/sqlexec_select.go` to unquote `N'...'` literals in SELECT expressions, string concatenations (`+`), and projections.
  - Enhanced DML literal parser (`parseLiteralValue`) and condition evaluators (`evaluateSingleConditionMap`, `parseEqualityConditions`) in `pkg/engine/sqlexec_eval.go` to support `N'...'` literals across all storage types and WHERE predicates (`=`, `<>`, `IN`).
  - Added unit and DML regression tests in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_NPrefixUnicodeLiterals`) and official Go MSSQL driver integration test in `pkg/wire/tds/driver_integration_test.go` (`TestDriverIntegration_GoMSSQLDB_NPrefixUnicodeLiterals`).
- **BIT Column Numeric/String Literal Boolean Coercion Across Full Data Lifecycle (`TDS-COMPAT-3`) (`pkg/storage`, `pkg/engine`, `pkg/proc`, `pkg/execution`, `pkg/wire/tds`)**:
  - Implemented `storage.BoolValueFromAny(v Value) Value` supporting cross-type conversion from `TypeInt`, `TypeFloat`, `TypeDecimal`, and `TypeVarChar`/`TypeJSON` (`'1'`, `'0'`, `'true'`, `'false'`, `'t'`).
  - Added cross-type equality (`Value.Equal`) and ordering (`Value.Compare`) between `TypeBool` and `TypeInt`, enabling seamless predicate evaluation when comparing boolean fields against numeric literals.
  - Added automatic boolean type coercion in `memengine` (`Insert` and `Update`) to prevent `ErrInvalidDataType` when passing numeric `1`/`0` to `BIT` columns.
  - Fixed WiredTiger binary tuple encoding in `tuple.go` (`EncodeRowToBuffer`) by coercing non-`TypeBool` values into booleans before writing byte `1`/`0`, eliminating silent false (`0`) serialization.
  - Enhanced DML literal parser (`parseLiteralValue`) and condition evaluators (`evaluateSingleConditionMap`, including `IN` clauses) in `pkg/engine/sqlexec_eval.go` to coerce `1`/`0` and `'1'`/`'0'` literals for boolean columns.
  - Enhanced `EvalCast` and `toFloat` in `pkg/proc/eval.go` to support `TypeBool` casting from integers, decimals, and quoted string representations.
  - Enhanced `CompareValues`, `toInt64`, and `toFloat64` in `pkg/execution/iterator.go` to handle cross-comparisons between booleans, integers, floats, and strings in Volcano execution operators.
  - Added unit test `TestTupleCodec_BoolNumericCoercion` (`pkg/storage`), expression test `TestEvalRowExpr_CastBit` (`pkg/proc`), and driver integration test `TestDriverIntegration_GoMSSQLDB_BitColumnNumericCoercion` (`pkg/wire/tds`).
- **Binary Arithmetic & String Concatenation NULL Propagation (`TDS-COMPAT-8`) (`pkg/proc`, `pkg/engine`)**:
  - Enforced ANSI and T-SQL standard `CONCAT_NULL_YIELDS_NULL ON` three-valued logic (3VL) NULL propagation in `pkg/proc/eval.go` (`evalRowBinaryOp`) across binary operators (`+`, `-`, `*`, `/`, `<`, `<=`, `>`, `>=`), preventing invalid string concatenation and negative formatting on NULL inputs.
  - Added NULL operand guards in `storageAdd`, `storageSub`, `storageMul`, `storageDiv`, `storageNeg`, `storageLt`, `storageLte`, `storageGt`, and `storageGte` in `pkg/proc/runtime/vm.go`.
  - Updated `Coordinator.evalScalarExpr` in `pkg/engine/sqlexec_select.go` to legitimately return `nil` (`NULL`) for non-column AST expressions instead of falling through to raw query strings.
  - Supported T-SQL `SELECT @var = <expr>` variable assignment projections in `parseGeneralSelect` (`pkg/proc/parser_dml.go`) and wired variable updates in `OpPointSelect` (`pkg/proc/runtime/vm_dispatcher.go`).
  - Added `storage.TypeDecimal` support to `toFloatNum` and `isTruthy` in `pkg/proc/runtime/vm.go`.
  - Added test suites `TestEvalRowExpr_NullPropagation` (`pkg/proc`) and `TestDriverIntegration_GoMSSQLDB_ConcatNullYieldsNull` (`pkg/wire/tds`).

### Added
- **Ad-hoc Top-Level Anonymous Control-Flow Statements `IF...ELSE` and `WHILE` (`TDS-COMPAT-4` / Scenarios 64 & 65) (`pkg/engine`, `pkg/proc`, `pkg/wire/tds`)**:
  - Enhanced `SplitStatements` in `pkg/engine/query_utils.go` to track compound `BEGIN...END` block nesting across all statements and look ahead for trailing `ELSE` tokens via `hasNextTokenElse`, preventing premature statement splits on semicolons inside `IF...ELSE` branches and `WHILE` loops.
  - Refined `isBeginTransaction` to accurately differentiate standalone `BEGIN;` transaction starts from compound control-flow blocks.
  - Extended `parseIf` and `parseWhile` in `pkg/proc/parser_stmt.go` to flexibly consume optional trailing semicolons after `thenStmt`, `elseStmt`, and loop bodies.
  - Added `proc.CompileAnonymousBlock` in `pkg/proc/compiler.go` to compile top-level control-flow statements into executable VDBE bytecode programs.
  - Added `Vars map[string]any` to `ExecutionResult` and implemented `collectFrameVars` in `pkg/proc/runtime/vm.go` to propagate updated register/slot variables back to callers.
  - Added `execAnonymousBlock` in `pkg/engine/sqlexec_procs.go` and integrated top-level routing in `pkg/engine/sqlexec.go`, binding session variables (`cs.Vars`), running blocks in transactions, synchronizing updated variable states, and returning relational result sets.
  - Added unit test suites `TestSQLExecutor_AdHocIfElseControlFlow` and `TestSQLExecutor_AdHocWhileLoop` (`pkg/api/sqlexec_test.go`) and E2E driver integration tests `TestDriverIntegration_Scenario64_AdHocIfElse` and `TestDriverIntegration_Scenario65_AdHocWhile` (`pkg/wire/tds/driver_integration_test.go`).
- **User Exception Statements `RAISERROR` and `THROW` with TDS Error Tokens (`TDS-COMPAT-9`) (`pkg/proc`, `pkg/engine`, `pkg/wire/tds`)**:
  - Extended `RaiseErrorStmt` and `ThrowStmt` in `pkg/proc/ast.go` with structured fields: `Number`, `Severity`, `State`, and `Message`, implementing `TdsErrorInfo() (int32, byte, byte)`.
  - Added AST parser support in `pkg/proc/parser_stmt.go` (`parseRaiserror`, `parseThrow`) for both parameterized and message/severity/state arguments.
  - Implemented VDBE bytecode compilation (`OpRaiserror`, `OpThrow`) in `pkg/proc/compiler.go` and runtime handling in `pkg/proc/runtime/vm_dispatcher.go`, integrating with `TRY...CATCH` exception handling blocks and propagating `*proc.ProcError` outside catch stacks.
  - Added Ad-hoc SQL batch execution support in `pkg/engine/sqlexec.go` (`execRaiserror`, `execThrow`), supporting C-style format specifiers (`%s`, `%d`), informational messages (severity <= 10) returning `PRINT` commands, and synchronizing global `@@ERROR`.
  - Implemented `tdsErrorDescriber` interface and `extractTdsErrorInfo` in `pkg/wire/tds/server.go`, returning official TDS `0xAA ERROR Token` with exact error number, severity, state, and message to client drivers (`go-mssqldb`).
  - Added unit test `TestSQLExecutor_RaiserrorAndThrow` in `pkg/api/sqlexec_test.go` and driver integration test `TestDriverIntegration_GoMSSQLDB_RaiserrorAndThrow` in `pkg/wire/tds/driver_integration_test.go`.
- **Legacy Metadata Stored Procedures `sp_tables` and `sp_columns` (`TDS-COMPAT-5`) (`pkg/engine`, `pkg/wire/tds`)**:
  - Implemented built-in `sp_tables` in `pkg/engine/sqlexec_procs.go` supporting `@table_name`, `@table_owner`, `@table_qualifier`, and `@table_type` parameters with wildcard (`%`) filtering, returning standard `TABLE_QUALIFIER`, `TABLE_OWNER`, `TABLE_NAME`, `TABLE_TYPE`, and `REMARKS` columns for tables and views.
  - Implemented built-in `sp_columns` returning standard 19-column ODBC/OLEDB metadata (`DATA_TYPE`, `TYPE_NAME`, `PRECISION`, `LENGTH`, `SCALE`, `RADIX`, `NULLABLE`, `COLUMN_DEF`, `ORDINAL_POSITION`, `IS_NULLABLE`, `SS_DATA_TYPE`, etc.) mapped from storage table column definitions with `@table_name`, `@column_name`, `@table_owner`, and `@table_qualifier` filtering.
  - Added `parseProcParams` and `cleanTableNameParam` supporting both named (`@param = val`) and positional arguments, with bracket-escaped and schema-qualified name sanitization.
  - Enhanced TDS `handleRPC` default handler in `pkg/wire/tds/server.go` to format RPC parameters into SQL `EXEC` statements with literal escaping via `formatSQLValue`, enabling seamless RPC invocation from GUI tools (DBeaver, SSMS) and database drivers.
  - Added unit tests in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_BuiltinSystemStoredProcedures`) and official Go MSSQL driver integration test in `pkg/wire/tds/driver_integration_test.go` (`TestDriverIntegration_GoMSSQLDB_SystemProcs_TablesAndColumns`).
- **T-SQL Stored Procedure Inline Variable Declaration and Initialization (`TDS-COMPAT-7`) (`pkg/proc`, `pkg/engine`)**:
  - Added `Init Expr` to `proc.DeclareStmt` AST and extended `parseDeclare` in `pkg/proc/parser_stmt.go` to parse inline initializers (`DECLARE @var Type = <expr>`) with comma-separated multi-variable chaining.
  - Implemented VDBE bytecode compilation in `pkg/proc/compiler.go` (`case *DeclareStmt:`) to evaluate initializers sequentially and store values immediately via `OpStoreVar` and `OpCast`, supporting forward references in subsequent declarations.
  - Added expression evaluation for `DeclareStmt.Init` in `pkg/proc/evaluator.go`.
  - Implemented `SplitDeclarations` in `pkg/engine/query_utils.go` to safely split comma-separated variable declarations in Ad-hoc batches without breaking on commas inside parentheses or string literals.
  - Added unit test `TestCompiler_DeclareInlineInit` (`pkg/proc`), `TestSplitDeclarations` (`pkg/engine`), and driver integration test `TestDriverIntegration_GoMSSQLDB_InlineVariableDeclaration` (`pkg/wire/tds`).
- **Volcano Execution Engine Runtime Profiler for EXPLAIN ANALYZE (`OBS-2`) (`pkg/planner`, `pkg/execution`, `pkg/engine`)**:
  - Implemented `InstrumentedIterator` in `pkg/execution/instrument.go` wrapping Volcano physical operators, capturing independent per-node `ActualStartupUS`, `ActualDurationUS`, `ActualRows`, and `ActualLoops` metrics across both row-by-row `Next()` and vectorized `NextChunk()` streaming paths.
  - Implemented sub-microsecond precision profiling with nanosecond accumulation (`totalNanos`), preventing quantization truncation on fast in-memory scans and limit operators.
  - Wired recursive instrumentation via `BuildInstrumentedExecutor` in `pkg/execution/builder.go`, and integrated inner index probe loop tracking in `IndexNestedLoopJoinIterator` (`SetInnerPlan`).
  - Integrated instrumented execution into `Coordinator.execExplain` (`pkg/engine/sqlexec_explain.go`) with read-only transaction isolation and resilient fallback to standard execution.
  - Supported canonical PostgreSQL format for both Text (`(actual time=%.3f..%.3f rows=%d loops=%d)`) and JSON (`Actual Startup Time`, `Actual Total Time`, `Actual Rows`, `Actual Loops`).
  - Added unit and regression tests in `pkg/execution/instrument_test.go`, `pkg/planner/planner_test.go`, and `pkg/api/sqlexec_test.go`.
- **Prometheus Extended Observability & OpenTelemetry OTLP JSON Exposition (`OBS-1`) (`pkg/engine`, `pkg/api`)**:
  - Enhanced Prometheus text exposition (`GET /metrics`) with storage engine backend (`lionsql_storage_info{engine="..."}`), replication cluster status (`lionsql_replication_role{role="..."}`, `lionsql_replication_lag_bytes`, `lionsql_replication_current_lsn`), transaction conflict counters (`lionsql_transactions_total{type="conflict"}`), and configured global memory limits (`lionsql_memory_limit_bytes`).
  - Implemented OpenTelemetry OTLP standard JSON endpoints (`GET /v1/metrics`, `GET /api/v1/metrics`) with service and resource attributes (`service.name="lionsql"`, `storage.engine`, `replication.role`) and cumulative/gauge scope metrics.
  - Added comprehensive test suites in `pkg/api/metrics_test.go` verifying OTLP JSON structure, write-write transaction conflict counting, and extended Prometheus metric exposition.
- **In-Memory OCC Engine Storage Capacity Limit Guard (`MEM-3`) (`pkg/storage/memengine`, `cmd/server`)**:
  - Implemented zero stop-the-world heap capacity monitoring via standard library `runtime/metrics` (`/memory/classes/heap/objects:bytes`).
  - Added `MaxDataBytes` setting to `memengine.Options` and `Engine.SetMaxDataBytes(bytes)`, automatically wired to the global memory governor (`--max-memory`).
  - Enforced fail-fast circuit breaker in `tx.Commit()` on data mutations (`changeInsert`, `changeUpdate`) when heap usage exceeds capacity limit, returning `storage.ErrTableFull (SQLSTATE 53200)` and preventing process OOM.
  - Added unit test `TestMemEngine_CapacityGuard` in `pkg/storage/memengine/memengine_test.go` verifying fail-fast rejection when limit is exceeded and normal execution when limit is relaxed.

### Removed
- **Dead Code Elimination & Architecture Diet (`KISS-1`) (`pkg/proc`, `pkg/catalog`, `pkg/api`, `pkg/engine`)**:
  - Completely deleted obsolete legacy JavaScript transpiler `pkg/proc/transpiler.go` (782 lines) and its tests `pkg/proc/transpiler_test.go` (387 lines), removing 1,169 lines of dead code after unifying the execution runtime exclusively around the SQLite VDBE Bytecode Virtual Machine.
  - Renamed legacy `JSCode` fields across `catalog.Procedure`, `catalog.Function`, and `catalog.Trigger` structs to `BytecodeDisasm`, eliminating confusing misleading naming while preserving backward-compatible JSON aliases for external clients.
  - Updated ANTLR procedure compilation tests in `pkg/proc/parser/antlr/antlr_test.go` to directly verify VDBE bytecode generation.

### Refactored
- **Critical Path Statement Splitter, Parameter Binding & TDS Zero-Alloc Acceleration (`PERF-1`) (`pkg/engine`, `pkg/wire/tds`)**:
  - Rewrote `SplitStatements` in `pkg/engine/query_utils.go` as a clean, index-based slice scanner (`query[start:i]`), eliminating `strings.Builder` byte-by-byte copies and removing the O(N^2) `checkProc(cur.String())` character loop. Added fast-path for single queries (`strings.IndexByte(query, ';') == -1`), cutting allocations to 16 B/op (16 ns/op).
  - Promoted local `regexp.MustCompile` declarations inside `execSelect`, `execCreateTable`, `execCreateView`, `execDropView`, `execAlterTable`, and `execCreateStatistics` to package-level `var` blocks, eliminating 15µs ~ 30µs of regex compilation overhead per query.
  - Implemented single-pass O(N) parameter substitution (`substituteParamsSinglePass`) for `sp_executesql`, `sp_execute`, and `sp_prepexec` in `pkg/wire/tds/server.go`, eliminating per-parameter `regexp.MustCompile` and `[]rune` allocations and achieving 50x speedup (< 1µs, 927 ns/op).
  - Implemented `WriteRowDirect` streaming in `pkg/wire/tds/token.go` and `pkg/wire/tds/server.go`, eliminating intermediate `make([]byte, ...)` buffer copies and achieving **0 B/op, 0 allocs/op (53.49 ns/op)** row serialization.
  - Eliminated `binary.Write` reflection and interface boxing across integer, float, and timestamp column serialization in `writeColumnValue`, replacing them with stack-allocated `binary.LittleEndian.PutUint16/32/64` machine instructions, and added `val.(string)` type assertions before string formatting.
  - Accelerated `EncodeUTF16LE` and `DecodeUTF16LE` with 7-bit ASCII fast-paths and direct `strings.Builder` streaming in `pkg/wire/tds/packet.go`, slashing allocation overhead from 3 allocs/op to 1 alloc/op (121.9 ns/op encode, 266.3 ns/op decode).
  - Documented post-mortem and 3 recurrence prevention invariants in `notes/PITFALLS_AND_TRICKS.md` (Section 13).
- **Simplify In-Memory SkipList PRNG with Go Standard Library (`KISS-4`) (`pkg/storage/memengine`)**:
  - Replaced legacy `math/rand` instance and its dedicated `rndMu sync.Mutex` in `SkipList` with Go 1.22+ lock-free `math/rand/v2`.
  - Completely eliminated redundant double locking (`sl.mu` + `sl.rndMu`) during index insertion, removed 16 bytes of struct memory overhead, and eliminated timestamp-based seed initialization.
- **Unify Virtual PG Catalog & Information Schema Generators (`KISS-2`) (`pkg/catalog/virtual`, `pkg/engine`)**:
  - Migrated all 35 `pg_catalog.*` and 17 `information_schema.*` virtual table generators from `pkg/engine/` into `pkg/catalog/virtual/` (`pg.go` and `info_schema.go`).
  - Deleted legacy files `pkg/engine/sqlexec_pgcatalog.go` (1,102 lines) and `pkg/engine/sqlexec_info_schema.go` (588 lines) entirely, decoupling all virtual metadata generation behind the `virtual.Source` interface.
  - Simplified `Coordinator.execVirtualCatalogSelect` in `pkg/engine/sqlexec_dbcc.go` to delegate directly to `virtual.IsPgCatalogTable` and `virtual.ExecPgCatalogSelect`.

---

## [0.23.0]

### Added
- **Virtual Catalog Query Interceptor Zero-Alloc Fast Filter & Whitespace-Resilient Matcher (`pkg/catalog/virtual`)**:
  - Implemented zero-allocation ASCII case-insensitive substring scanner `containsFold` and `hasCatalogCandidate` in `pkg/catalog/virtual/interceptor.go`, enabling non-catalog OLTP `SELECT` queries to bypass catalog checks with 0 heap allocations (`0 B/op`, `0 allocs/op`) and 103ns dispatch latency.
  - Implemented `containsEq` helper to match key-value equality conditions across arbitrary whitespace variations (`= `, ` =`, ` = `, `=`, `\t=\n`, `   =   `) with identifier word-boundary guards, resolving potential false-negative matching bugs for triggers (`o.type = 'TR'`), views (`type = 'V'`), primary keys (`is_primary_key = 1/0`), and foreign key constraints (`contype = 'F'`).
  - Streamlined `IsVirtualCatalogJoinQuery` with `matchAny`, cutting pattern boilerplate from 63 lines to 35 lines.
  - Added unit tests in `interceptor_internal_test.go` and `interceptor_test.go` verifying 100% whitespace permutations and benchmark memory metrics.
- **PostgreSQL JDBC / psqlODBC Driver Enterprise Compatibility Certification (`pkg/wire`)**:
  - Validated and certified end-to-end compatibility for official PostgreSQL JDBC (`pgjdbc`) and `psqlODBC` drivers.
  - Verified driver session handshake parameter controls (`SET extra_float_digits = 3`, `SET application_name = 'PostgreSQL JDBC Driver'`, `SET client_min_messages = 'warning'`, `SET client_encoding = 'UTF8'`).
  - Verified connection pool session status queries (`SHOW transaction_isolation`, `SHOW standard_conforming_strings`).
  - Certified metadata introspection queries against `pg_type`, `information_schema.tables`, and `SELECT current_schema()` with unit test `TestDriverIntegration_LibPQ_JDBC_ODBC_Compatibility`.
- **Prepared Statement Binary Bind Direct Value Decoding & Param Name Pooling (`P-14`) (`pkg/wire`, `pkg/wire/tds`)**:
  - Implemented `decodeBinaryParamValue` and `decodeTextParamValue` in `pkg/wire/server.go`, directly decoding incoming client parameter byte streams into typed `storage.Value` (`IntValue`, `FloatValue`, `BoolValue`, `StringValue`) and bypassing redundant binary-to-string-to-type double conversions (`strconv.Itoa`).
  - Added typed parameter values (`ParamValues []storage.Value`) to PGWire `Portal` struct, directly populating VDBE parameter map slots in `handleExecute`.
  - Added precomputed static parameter name lookup tables (`staticParamP`, `staticParamAt`, `staticParamDollar`) for up to 64 parameters in `pkg/wire/server.go` and `pkg/wire/tds/server.go`, completely eliminating `fmt.Sprintf` heap allocations during prepared statement bind/execute hot paths.
  - Added comprehensive test coverage in `pkg/wire/server_test.go` (`TestPGWire_BinaryBindDirectValueDecoding`) verifying zero-allocation decoding and type fidelity across integer, float, boolean, and text types.
- **TDS Single-Frame Buffer Pooling & 50% Syscall Reduction (`pkg/wire/tds`)**:
  - Implemented `sync.Pool`-backed 4,096-byte frame buffer (`tdsFramePool`) in `pkg/wire/tds/packet.go` to pack the 8-byte TDS header and chunk payload contiguously into a single byte slice.
  - Eliminated the split two-call `Write(hdr)` and `Write(payload)` pattern, cutting OS socket system calls by exactly 50% across all TDS client responses with zero heap allocation.
  - Added unit test `TestTDS_WritePacket_SingleSyscallPerPacket` to deterministically verify single-syscall emission across empty, single-frame, and multi-frame packets.
- **TPC-C VDBE Bytecode Engine 20k TPS Milestone & Benchmark Verification (`doc/BENCHMARK.md`, `README.md`)**:
  - Reached **20,087.70 TPS** (541,708.93 tpmC, peak 25,298 TPS) on the In-Memory OCC engine with 10 warehouses and 20 concurrent client workers, achieving sub-millisecond median latencies (New-Order P50 0.69ms, Payment P50 0.05ms) and slashing serialization abort rate to **0.10%**.
  - Advanced WiredTiger disk B+Tree MVCC throughput to **16,814.17 TPS** (457,021.04 tpmC, +13.2% gain) with flat P99 tail latency (New-Order P99 9.46ms), powered by P-8 cursor pooling and P-12 chunk pooling.
- **Enterprise Architecture & Technical Documentation Suite (`doc/`, `README.ko.md`)**:
  - Authored [`doc/FOUNDATIONAL_DESIGN_DECISIONS.md`](doc/FOUNDATIONAL_DESIGN_DECISIONS.md): genesis and technical rationale behind core architecture (WiredTiger, Go, T-SQL VDBE VM, dual wire listeners, single binary packaging, Antigravity AI pair programming).
  - Authored [`doc/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/ZERO_ALLOCATION_ARCHITECTURE.md): memory layout, buffer pooling (`sync.Pool`), arena slabs, and hot-path GC avoidance strategies (`P-1` ~ `P-14`).
  - Authored [`doc/ENTERPRISE_AUTHENTICATION_SPI.md`](doc/ENTERPRISE_AUTHENTICATION_SPI.md): unified superuser model, `AuthProvider` SPI interface, LDAP/AD directory architecture, and perimeter proxy security topology.
  - Authored [`doc/SCHEMA_MIGRATIONS_CLI.md`](doc/SCHEMA_MIGRATIONS_CLI.md): declarative schema migrations guide, `lionsql migrate` workflow, and zero-downtime deployment patterns.
  - Authored [`doc/AUDIT_LOG_SPECIFICATION.md`](doc/AUDIT_LOG_SPECIFICATION.md): enterprise immutable cryptographic audit log specification (HMAC-SHA256 hash chains, tamper-evident verification, GDPR/HIPAA/SOX compliance).
  - Authored [`README.ko.md`](README.ko.md): comprehensive Korean documentation covering architecture, features, wire protocols, CLI, benchmarks, and production quickstart.

### Refactored
- **Test Suite Boilerplate Diet & Helper Unification (-291 Lines) (`pkg/wire`, `pkg/api`)**:
  - Implemented `authenticatePGClient` in `pkg/wire/server_test.go`, eliminating verbose raw socket packet writing and response draining across password authentication subtests (`Success`, `Failure`, `GlobalAuthSuperuser`) (-140 lines).
  - Streamlined `TestWire_ConcurrentConnectionMultiplexing` using `connectPGClient` and `sendPGQuery` (-65 lines).
  - Simplified `TestPGWire_ServerSideSQLCursor` connection lifecycle using `connectPGClient` (-20 lines).
  - Enhanced `setupMemServer` in `pkg/api/common_test.go` with automatic `t.Cleanup` memory engine teardown, replacing repetitive 4-line setup boilerplate across 31 unit tests in `pkg/api/sqlexec_test.go` with a single `setupMemServer(t)` call (-120 lines).
  - Net deletion of 291 lines of repetitive test code while preserving 100% test coverage and zero data races.
- **Strategic Blueprint & Operational Backlog Restructuring (`ROADMAP.md`, `TODO.ko.md`, `README.md`)**:
  - Restructured `ROADMAP.md` as an authoritative strategic blueprint detailing Phases 1 through 6, v1.0 LTS exit criteria, active Phase 6 deliverables, explicit spec-out decisions, and post-1.0 horizons.
  - Transformed `TODO.ko.md` into a high-velocity operational cockpit with clear taxonomy tags, active P1 sprint queue, domain-specific backlog matrices, and post-1.0 deferred backlog, eliminating over 870 lines of circular redundant logs.
  - Grouped Section 2 (Key Features) across `README.md` and `README.ko.md` into 5 coherent architectural pillars.

---

## [0.22.0]

### Added
- **Volcano Vectorized Batch Chunk Pooling (`P-12`) (`pkg/execution`)**:
  - Implemented `sync.Pool`-backed vector batch chunk pooling (`GetChunk`, `PutChunk`, `AcquireChunk`, `ReleaseChunk`) in `pkg/execution/chunk.go`.
  - Cleared tuple references (`Tuple.Values`, `Lazy`, `Index`) upon chunk recycling to prevent GC memory leaks, bounding pooled chunk capacity to `DefaultChunkSize * 4` (Golden Rule 23).
  - Replaced ad-hoc `NewChunk` allocations in `aggregate.go`, `join.go`, `sort.go`, and `sqlexec_select.go` with pooled chunk acquisition and deterministic `defer ReleaseChunk` recycling.
- **In-Memory OCC Transaction Object & Workspace Map Pooling (`P-11`) (`pkg/storage/memengine`)**:
  - Implemented `sync.Pool`-backed `txPool` in `pkg/storage/memengine/tx.go` to eliminate heap allocations for `Transaction` and its 6 workspace maps (`tableCache`, `baseVers`, `local`, `deleted`, `changes`, `savepoints`) on every transaction begin.
  - Leveraged Go 1.21+ built-in `clear(m)` in `releaseToPoolLocked()` to wipe map contents in $O(1)$ time while preserving underlying hash table bucket arrays.
  - Added bounded resource guard against oversized transaction collections (> 1,024 changes) to prevent memory retention spikes.
  - Achieved zero-allocation transaction creation for high-frequency OLTP workloads (16,000~20,000+ TPS).
- **Semi-Structured JSON Same-Tuple DOM Cache & Zero-Allocation Key Scanner (`P-7`) (`pkg/storage`, `pkg/engine`)**:
  - Implemented zero-allocation streaming byte scanner `ScanJSONTopLevelKey` in `pkg/storage/json.go` for direct extraction of top-level scalar keys (`doc->>'city'`, `JSON_VALUE(doc, '$.city')`) without unmarshaling or building an AST/DOM.
  - Implemented bounded FIFO DOM cache (`globalDOMCache`, capacity 1,024 entries) with thread-safe `sync.RWMutex` to parse JSON documents once per tuple when querying multiple complex paths.
  - Integrated fast scanner and DOM cache across `ExtractJSON`, `ExtractJSONText`, `JSONValueByPath`, `JSONQueryByPath`, and `navigatePath`.
- **Production Storage & Filesystem Encryption Guide (`doc/STORAGE_ENCRYPTION_GUIDE.md`, `doc/SECURITY.md`)**:
  - Authored comprehensive guide detailing data-at-rest encryption standard using OS/Cloud hardware-accelerated block storage subsystems (Linux LUKS2 / dm-crypt with AES-NI, AWS EBS Encryption, GCP CMEK, Azure Managed Disks).
  - Postponed in-kernel software-level TDE in favor of native platform primitives to prevent double-encryption NVMe I/O degradation and isolate key management outside database process memory (Golden Rules 14 & 15).
  - Documented NVMe dm-crypt workqueue bypass performance tuning, unattended TPM 2.0 key binding, encrypted Kubernetes StorageClasses, and AES-256-GCM off-site backup export.
- **5-Table E-Commerce & Banking Demo Seed Database (`scripts/demo_seed.sql`)**:
  - Implemented standalone sample database script featuring 5 standard tables and rich sample datasets:
    - `customers`: Customer personal information (ID, name, email, phone, city, created_at).
    - `accounts`: Bank accounts and balances (ID, customer_id, account_number, balance, account_type, status, created_at).
    - `products`: Product catalog and inventory (ID, name, category, price, stock_qty, created_at).
    - `sales`: Order master records (ID, customer_id, order_date, total_amount, status).
    - `sales_details`: Order line items (ID, sales_id, product_id, quantity, unit_price, subtotal).
  - Implemented 2 production-grade T-SQL stored procedures:
    - `TransferFunds`: Multi-step banking fund transfer with balance verification, transaction commit/rollback, and error throwing.
    - `ProcessOrder`: E-commerce order placement with product inventory check, stock decrement, sales/sales_details record insertion, and transaction commit.
  - Directly loaded and executed from `scripts/demo_seed.sql` upon `--seed` flag, adhering to KISS and eliminating duplicate embedded assets.

### Changed
- **Virtual Catalog Multi-Table Cross-Join Interceptor Layer Isolation (`pkg/catalog/virtual`, `pkg/engine`)**:
  - Isolated multi-table cross-join query interceptors for GUI tools (DBeaver, DbGate, Beekeeper Studio) from `pkg/engine/sqlexec_gui_interop.go` and `pkg/engine/sqlexec_mssql_sys.go` into a dedicated package `pkg/catalog/virtual`.
  - Defined `virtual.Source` interface for decoupled metadata access (`ListTables`, `GetTable`, `ListProcedures`, `ListFunctions`, `ListViews`, `GetView`, `GetSortedTables`, `GetSortedViews`, `GetSortedProcedures`) and optional `GetTableRowCount`.
  - Implemented zero-dependency `virtual.FormatResult` row filtering, ordering, pagination, and projection evaluator.
  - Implemented `virtual.IsVirtualCatalogJoinQuery` predicate and `virtual.Execute` router handling PostgreSQL and MSSQL virtual catalog cross-joins.
  - Replaced legacy `HeidiSQL` terminology across all catalog join handlers, query matchers, and test cases with standard `VirtualCatalog`.
  - Deleted obsolete `sqlexec_gui_interop.go` and pruned `sqlexec_mssql_sys.go` (1,300+ lines removed), adding `sqlexec_virtual.go` to bridge `Coordinator` to `virtual.Source`.
- **Default AutoSeed Disabled & Hardcoded Go Seeding Removal (`cmd/server`, `pkg/config`)**:
  - Changed default `--seed` CLI flag and `cfg.Server.AutoSeed` configuration from `true` to `false` for enterprise safety.
  - Deleted legacy hardcoded Go struct seed methods (`seedSampleAccounts`, `seedSampleProcedure`, 140 lines) from `cmd/server/main.go`, replacing them with unified `server.ExecuteSQL(seedSQL)` execution.
  - Enhanced `SplitStatements` (`pkg/engine/query_utils.go`) and statement router (`pkg/engine/sqlexec.go`):
    - Added `StripLeadingComments` to safely ignore SQL line (`--`) and block (`/* */`) comments when detecting command types.
    - Fixed `blockDepth` tracking to distinguish `BEGIN TRANSACTION` / `BEGIN TRAN` from block delimiters `BEGIN ... END`.
  - Enhanced T-SQL parser (`pkg/proc/parser_ddl.go`, `pkg/proc/parser_stmt.go`):
    - Added support for multi-argument type modifiers (e.g. `DECIMAL(15, 2)`, `NUMERIC(10, 4)`) in procedure/function parameter lists, return types, and `DECLARE` variable statements.
  - Enhanced procedure executor (`pkg/engine/sqlexec_procs.go`):
    - Added support for both named parameter calls (`EXEC Proc @Param = Val`) and positional parameter calls.
- **Unified Index Bounds & Condition Resolution Engine (`R-3`) (`pkg/planner`, `pkg/execution`, `pkg/engine`)**:
  - Implemented centralized `pkg/planner/bounds.go` module eliminating code duplication (DRY) across Volcano plan tree builder and single-table fast-path coordinator:
    - `ParseWhereConditions`: $O(N)$ single-pass condition parser with parentheses/quote depth tracking and package-level precompiled regex singletons supporting `=`, `BETWEEN ... AND ...`, `>=`, `>`, `<=`, `<`.
    - `ParseIndexCond`: unified index condition bound extractor supporting single/composite indexes, range expressions, and functional JSON expression predicates (`doc->>'key' = 'val'`).
    - `ParseValueToType`: centralized string-to-storage-value parser supporting `TypeInt`, `TypeFloat`, `TypeDecimal`, `TypeBool`, `TypeDateTime`, `TypeDate`, `TypeTime`, `TypeUUID`, `TypeBinary`, `TypeVarChar`.
    - `BuildCompositePrefixKey` & `FindIndexAndBounds`: shared composite index prefix and candidate index resolver.
  - Refactored `pkg/execution/builder.go`: removed duplicated parsing logic and unneeded `regexp`/`proc` imports, enhancing Volcano index scan iterators with full range bound capabilities.
  - Refactored `pkg/engine/sqlexec_eval.go`: removed over 260 lines of duplicated bounds extraction logic and delegated to `planner.Bounds`.
  - Added comprehensive test suites in `pkg/planner/bounds_test.go` and verified 100% race-free pass across all packages.
- **Pratt Parser & Built-in Functions Domain Modularization (`R-2`) (`pkg/proc`)**:
  - Modularized monolithic `pkg/proc/parser.go` (3,241 lines) into 5 focused domain modules:
    - `parser.go` (280 lines, 90%+ reduction): Core `Parser` struct, `NewParser`, token advancement, and syntax helpers.
    - `parser_ddl.go` (599 lines): `CREATE`/`ALTER`/`DROP` procedures, functions, triggers, tables, and indexes.
    - `parser_dml.go` (1,141 lines): `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `WITH` (CTE), and `EXPLAIN` query parsing.
    - `parser_stmt.go` (656 lines): T-SQL procedural control statements (`IF`/`WHILE`/`TRY...CATCH`/`BEGIN...END`), transactions, cursors, and `EXEC`.
    - `parser_expr.go` (615 lines): Pratt operator precedence expressions, function calls, `CASE WHEN`, and JSON extraction expressions.
  - Modularized monolithic `pkg/proc/builtins.go` (2,880 lines) into 6 domain-specific modules:
    - `builtins.go` (153 lines, 94%+ reduction): Scalar function SPI registration, domain dispatchers (`EvalBuiltin`, `IsBuiltinFunction`), and common type converters.
    - `builtins_date.go` (765 lines): T-SQL & ANSI date/time functions and date parsing/truncation/bucketing helpers.
    - `builtins_string.go` (885 lines): String manipulation, regex matching, `PATINDEX`, `SOUNDEX`, and number formatting helpers.
    - `builtins_math.go` (352 lines): Mathematical, trigonometric, and bitwise manipulation functions.
    - `builtins_json.go` (179 lines): JSON scalar functions (`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`) and path modifiers.
    - `builtins_sys.go` (697 lines): System context, metadata introspection, UUIDv7 generation, error functions, conditional logic (`ISNULL`, `COALESCE`), and cryptographic hashing.
  - Preserved 100% syntactic and runtime parity with zero data races verified across all 25 packages.
- **Virtual System Catalog Modularization & Registry Pattern Architecture (`R-1`) (`pkg/engine`)**:
  - Decomposed monolithic 3,284-line `sqlexec_pgcatalog.go` into domain-specific modules:
    - `sqlexec_pgcatalog.go` (634 lines): Core PostgreSQL `pg_catalog.pg_*` virtual views and centralized registry dispatcher.
    - `sqlexec_mssql_sys.go` (554 lines): SQL Server / TDS virtual catalog views and DMVs (`sys.tables`, `sys.columns`, `sys.dm_*`, `sys.database_files`, etc.).
    - `sqlexec_info_schema.go` (420 lines): ANSI standard information schema views (`information_schema.tables`, `columns`, `table_constraints`, etc.).
    - `sqlexec_gui_interop.go` (477 lines): Multi-table catalog cross-join query interceptors for GUI tools (HeidiSQL, Beekeeper Studio, DbGate, DBeaver).
  - Implemented pluggable `VirtualCatalogView` type and $O(1)$ dispatch registry (`catalogViewRegistry`), replacing the 60-line `isPgCatalogTable` branch and 1,500-line `execPgCatalogSelect` switch while adhering to the Open-Closed Principle (OCP).
  - Preserved 100% interoperability with external GUI database administration tools and verified race-free execution across all packages.

### Security
- **Unified Multi-Protocol Superuser Authentication & Single-Source-of-Truth RBAC (`pkg/auth`, `pkg/wire`, `pkg/wire/tds`)**:
  - Unified administrator password storage across HTTP REST/Web Studio, PostgreSQL Wire Protocol (`:5433`), and Microsoft SQL Server TDS (`:1433`):
    - `pkg/auth`: Stored administrator credentials in a single source of truth (`s.defaultAdminEmail` / `s.users[s.defaultAdminEmail]`); routed built-in administrative logins (`sa`, `postgres`, `admin`) in `AuthenticateLogin` directly to the unified superuser password hash.
    - Synchronized `AlterLoginPassword` for built-in logins to automatically update the superuser password and keep HTTP, PG, and TDS administrator credentials identical without divergence.
    - `pkg/wire`: Updated PGWire handshake to allow trust login for built-in accounts when `--auth=false`, while strictly validating against the unified superuser password when `--auth=true`.
    - `pkg/wire/tds`: Implemented MS-TDS 2.2.6.4 password de-obfuscation (`readLoginPassword`: nibble swap + XOR `0xA5`) in `parseLogin7`; enforced authentication verification in `handleLogin7` when `--auth=true`, returning standard SQL Server Login Failed error token (Number `18456`, Severity `14`, State `1`) on bad credentials.
    - Implemented pluggable `AuthProvider` SPI interface (`pkg/auth/provider.go`) and chain-of-responsibility authentication resolution (`s.RegisterProvider`, `s.Providers`), enabling future enterprise LDAP / Active Directory directory integrations without external dependencies.
    - Added comprehensive unit and integration tests in `pkg/auth/rbac_test.go`, `pkg/wire/server_test.go`, and `pkg/wire/tds/auth_test.go` with 100% race-free verification across all packages.

### Removed
- **In-Kernel OAuth2 Social Login & Third-Party SaaS Auth Exclusion (Spec-Out) (`pkg/api`, `pkg/auth`, `pkg/config`, `pkg/ui`)**:
  - Excluded consumer OAuth2 social login (Google, GitHub OAuth2 endpoints, redirect handlers, client secret lifecycle, and outbound HTTP token exchanges) to adhere to Golden Rule 15 (KISS & DRY) and single responsibility principle:
    - Removed `pkg/api/oauth.go`, `pkg/api/handlers_oauth.go`, and `pkg/api/oauth_test.go`.
    - Removed `s.oauth`, `NewOAuthManager`, `OAuth()`, `SetOAuthConfig()`, and routes `/api/auth/oauth2/*`, `/api/settings/oauth2` from `pkg/api/server.go`.
    - Removed `ProvisionOAuthUser`, `GetUserByOAuth`, and OAuth fields from `pkg/auth/auth.go`.
    - Removed OAuth configuration structs and environment variable bindings (`LIONSQL_OAUTH_*`) from `pkg/config/config.go`.
    - Removed OAuth provider login buttons and message event listeners from Web Dashboard modal (`pkg/ui/dashboard.html`).
  - Recorded architectural invariant in `GEMINI.md`, `GEMINI.ko.md` (Section 8.6), and `notes/ENGINEERING_CONTEXT.md` (Section 1.12): enterprise SSO and third-party identity federation are delegated to standard perimeter reverse proxies (Keycloak, OAuth2-Proxy, Nginx) while keeping the database kernel self-contained and attack-surface minimal.

### Performance
- **SQL Parser & Engine Hot-Path Regular Expression Precompilation (`P-6`) (`pkg/engine`)**:
  - Eliminated over 40 dynamic `regexp.MustCompile(...)` invocations across query routing, DML parsing, and row-level evaluation loops in `pkg/engine/`:
    - `sqlexec.go`: Promoted session variables (`SET`, `SELECT @var`, `SELECT @@var`), temporary table declarations, and administration commands (`KILL`, `USE`, `SHOW`) to package-level singletons.
    - `sqlexec_dml.go`: Precompiled DML syntax verification, `INSERT INTO ... SELECT`, `VALUES`, `UPDATE ... SET`, and `DELETE` pattern matchers (`reDmlInsertCheck`, `reDmlInsertSelect`, `reDmlInsertValues`, `reDmlUpdateCheck`, `reDmlUpdate`, `reDmlDeleteCheck`, `reDmlDelete`).
    - `sqlexec_pivot.go`: Precompiled `hasPivot`, `hasUnpivot`, and `hasForJSON` predicates, body matchers (`rePivotBody`, `reUnpivotBody`), and row-level condition evaluators (`rePivotCond`), eliminating regex compilation inside per-row filter loops.
    - `sqlexec_apply.go`: Implemented thread-safe regex cache (`rePatternCache sync.Map` via `getCompiledRegexp`) for outer row parameter substitutions, eliminating thousands of DFA compilations during nested loop joins.
    - `sqlexec_derived.go`, `sqlexec_eval.go`, `sqlexec_explain.go`, `sqlexec_mssql_sys.go`, `sqlexec_gui_interop.go`, `sqlexec_string_split.go`, `sqlexec_procs.go`: Promoted all static patterns to package singletons and dynamic identifier patterns to cached regex instances.
  - Reduced query dispatch and row evaluation latency by eliminating 5~15µs compilation overhead and associated heap allocations per regular expression per query.
- **WiredTiger Session Cursor Pooling & CGo Boundary Overhead Reduction (`P-8`) (`pkg/storage/wiredtiger`)**:
  - Implemented session-level cursor pooling (`WTSession.acquireCursor` and `releaseCursor`) keyed by table URI (`table:<name>`), recycling cursors with `C.go_wt_cursor_reset` instead of closing and reopening them via CGo on every query.
  - Implemented unified C bridge helpers (`go_wt_cursor_get`, `go_wt_cursor_insert_raw`, `go_wt_cursor_update_raw`, `go_wt_cursor_remove_key`) in C preamble, collapsing multi-step CGo crossings (`set_key` -> `search`/`operation` -> `get_val_raw`) into a single CGo invocation (6x reduction in boundary transitions).
  - Added zero-allocation C string conversion (`fastCString`) backed by a 256-byte stack buffer, eliminating C `malloc`/`free` during primary key lookups and mutations.
  - Reduced point lookup latency to **894 ns/op** (1,130,000+ OPS) and 100-row table scans with decoding to **27 µs/op** (36,000+ scans/sec).
  - Verified 100% race-free concurrency across all 25 packages with zero memory leaks and safe nested scan isolation.

---

## [0.21.0]

### Added
- **Semi-Structured JSON Functional Expression Indexing & Built-in Operators (ADR-022) (`pkg/proc`, `pkg/storage`, `pkg/planner`, `pkg/execution`, `pkg/engine`)**:
  - Implemented T-SQL & ANSI standard JSON built-in functions: `ISJSON(val)`, `JSON_VALUE(col, '$.path')`, `JSON_QUERY(col, '$.path')`, `JSON_MODIFY(col, '$.path', new_val)`.
  - Added PostgreSQL JSON operators `->` (JSON object/array extraction) and `->>` (scalar text extraction) to Pratt parser and Volcano/VDBE query execution engines.
  - Added support for functional B-Tree expression index DDL: `CREATE [UNIQUE] INDEX ... ON table ((col->>'key'))` and `CREATE INDEX ... ON table ((JSON_VALUE(col, '$.path')))`.
  - Implemented sub-microsecond JSON index key extraction (`storage.ExtractJSONIndexKey`) and real-time synchronization in In-Memory OCC SkipList and WiredTiger B+Tree engines on row `INSERT`, `UPDATE`, and `DELETE`.
  - Enforced ANSI SQL UNIQUE constraint semantics for expression indexes: multiple NULL keys are permitted without collision, while duplicate non-null keys trigger atomic rollback (`ErrUniqueViolation`).
  - Integrated CBO Cost-Based Optimizer (`pkg/planner/planner.go`) expression index detection and matching for pushed-down predicates (`WHERE doc->>'city' = 'Seoul'`, `WHERE JSON_VALUE(doc, '$.city') = 'Seoul'`), generating cost-optimal `NodeIndexScan` query plans.
  - Enhanced Volcano plan tree builder (`pkg/execution/builder.go`) to parse expression conditions and extract index scan lookup bounds (`lowVal`, `highVal`).
  - Enabled single-table fast-path (`pkg/engine/sqlexec_eval.go`) and WiredTiger filtered cursors (`pkg/storage/wiredtiger/wt_engine.go`) for functional expression index range scans with automatic Volcano delegation for complex projections.
  - Validated with `EXPLAIN` plan verification and microbenchmarks in `pkg/engine/json_query_test.go` and `pkg/engine/json_wt_test.go`, demonstrating **6.5x ~ 7.3x query acceleration** over sequential scans.
- **Per-Query Arena Memory Allocator for Zero-GC Execution (`pkg/storage`, `pkg/execution`, `pkg/engine`)**:
  - Implemented high-performance query/session-scoped memory arena slab allocator (`pkg/storage/arena.go`) with bump-pointer allocation and 8-byte alignment, eliminating Go runtime garbage collection overhead and heap fragmentation during heavy analytical queries.
  - Slabs pooled via `sync.Pool` across raw bytes (`DefaultByteSlabSize = 64KB`), storage values (`DefaultValueSlabSize = 2048 Values`), and interface slices (`DefaultAnySlabSize = 2048 []any`).
  - Implemented $O(1)$ single-pointer reset (`Reset()`, `Release()`, `PutArena()`), recycling slabs back to global pool without leaks.
  - Integrated with `pkg/execution` operators:
    - `ExecutorOptions.Arena *storage.Arena`: wired into Volcano plan tree builder (`BuildExecutorWithOptions`).
    - `SeqScanIterator` and `IndexScanIterator`: arena-allocated tuple value buffers and `FormatRowWithArena`.
    - `HashJoinIterator`: zero-allocation key interning (`h.arena.AllocString`) and merged tuple allocations (`mergeTuplesWithArena`, `mergeTuplesWithNullsAndArena`, `mergeNullsWithTupleAndArena`).
    - `SortIterator`: arena-backed sorting buffers.
    - `AggregateIterator`: arena-allocated group result tuples.
    - `execVolcanoSelect`: borrow and return query-scoped arena via `storage.GetArena()` and `defer storage.PutArena(arena)`.
  - Added unit test suites and benchmarks in `pkg/storage/arena_test.go` (14.0 ns/op, 0 B/op, 0 allocs/op) and `pkg/execution/arena_test.go`.
- **PostgreSQL Wire Protocol T-SQL Stored Procedure Execution Compatibility (`pkg/wire`, `pkg/proc`, `pkg/engine`)**:
  - Added full support for executing T-SQL stored procedures over PostgreSQL wire protocol (:5433) using both `EXEC <proc>` and PostgreSQL standard `CALL <proc>(args)` syntax.
  - Added procedure result row schema propagation in `writeSchemaDescription` and `handleExecute`, enabling extended query protocol drivers (`pgx`, `psycopg3`, `node-postgres`) to describe, bind, and scan procedure result rows without OID mismatches.
  - Updated native Pratt parser (`pkg/proc/parser.go`) to parse general `SELECT` statements inside stored procedure scripts alongside `SELECT INTO`.
  - Propagated execution result rows and columns in `execProcedureCall` (`pkg/engine/sqlexec_procs.go`).
  - Added end-to-end driver integration test in `pkg/wire/pgx_integration_test.go` (`TestDriverIntegration_PGX_TSQLStoredProcedure`).
- **T-SQL `STRING_SPLIT` Table-Valued Function & Query Resolution Compatibility (`pkg/engine`)**:
  - Implemented official Microsoft T-SQL `STRING_SPLIT(string, separator [, enable_ordinal])` table-valued function in `pkg/engine/sqlexec_string_split.go`.
  - Added full support for direct invocation (`SELECT value [, ordinal] FROM STRING_SPLIT(...) [AS alias]`), national string literals (`N'...'`), session variables (`@var`), `WHERE` filtering, `ORDER BY`, `DISTINCT`, aggregate `COUNT(*)`, and standard SQL pagination (`LIMIT` / `OFFSET` and T-SQL `OFFSET ... ROWS FETCH NEXT ... ROWS ONLY`).
  - Added seamless subquery integration (`WHERE col IN (SELECT value FROM STRING_SPLIT(@ids, ','))`) via session-aware subquery resolution (`resolveSubqueriesWithSession`).
  - Added full `CROSS APPLY STRING_SPLIT(col, ',')` and `OUTER APPLY` support across joined table rows.
  - Added comprehensive test suites in `pkg/engine/sqlexec_string_split_test.go` verifying basic splits, custom delimiters, ordinal tracking, subquery evaluation, and `CROSS APPLY` joins.

---

## [0.20.0]

### Fixed
- **DBeaver Table Introspection & Sizing Compatibility across PostgreSQL and TDS (`pkg/engine`, `pkg/wire/tds`)**:
  - Fixed infinite query polling loop in DBeaver SQL Server connections caused by mismatched table size and row count projection columns. DBeaver issues `SELECT t.name, p.rows, SUM(a.total_pages) * 8 AS totalSize, SUM(a.used_pages) * 8 AS usedSize FROM sys.tables t ... WHERE t.schema_id = 1 AND t.object_id = <id> GROUP BY t.name, p.rows`; updated `execDbGateMSSQLRowCountSelect` to project `name`, `rows`, `totalSize`, and `usedSize` matching the requested `object_id`.
  - Fixed empty table hierarchy in DBeaver PostgreSQL connections where table introspection queries filtered on `pg_class.relnamespace` (`WHERE c.relnamespace=2201` for `dbo` or `2200` for `public`). Updated `execHeidiSQLClassSelect` to emit metadata across both schemas and execute whereClause projection filtering.
  - Added support for PostgreSQL system catalogs queried by DBeaver, including `pg_conversion`, `pg_shdescription`, `pg_tablespace`, and `pg_collation`.
### Added
- **Transactional Outbox Broker Dispatcher & Lossless Event Streaming (`pkg/cdc`, `pkg/config`, `pkg/api`, `cmd/server`)**:
  - Implemented `OutboxDispatcher` (`pkg/cdc/outbox.go`) subscribing to transaction-committed CDC event streams and reliably dispatching messages to external message brokers.
  - Implemented normalized `OutboxMessage` formatting supporting both general CDC table mutation streaming (topic prefix `lionsql.<table_name>`) and dedicated outbox tables (`outbox` / `_outbox`) extracting `topic`, `payload`, `key`, and `headers`.
  - Added at-least-once delivery guarantees with atomic checkpoint persistence (`outbox_checkpoint.json`, Golden Rules 1 & 5) tracking `last_ack_lsn` and resuming from last acknowledged LSN upon server restarts.
  - Implemented exponential backoff retry loop (`MaxRetries`, `InitialBackoff`, `MaxBackoff`) and cooperative cancellation.
  - Built-in pluggable message broker sinks (`pkg/cdc/outbox_sinks.go`):
    - `MemorySink`: Thread-safe in-memory sink for embedded streaming and hermetic testing.
    - `WebhookSink`: HTTP POST webhook forwarding with custom headers and authorization tokens.
    - `KafkaRESTSink`: Confluent / Strimzi / Upstash Kafka REST Proxy forwarding (`/topics/{topic}`).
    - `RabbitMQHTTPSink`: RabbitMQ Management HTTP API publishing (`/api/exchanges/{vhost}/{exchange}/publish`).
  - Added REST observability endpoint `GET /api/cdc/outbox/status` reporting live dispatch metrics (`dispatched_count`, `failed_count`, `retried_count`, `last_ack_lsn`, `sink_count`, `active`).
  - Added CLI flags (`-outbox`, `-outbox-table`, `-outbox-topic-prefix`, `-outbox-webhook`, `-outbox-kafka`, `-outbox-rabbitmq`) and JSON configuration (`cfg.Outbox`).
  - Added comprehensive unit and integration tests across all sinks, dedicated outbox tables, retries, checkpointing, and REST endpoints (`pkg/cdc/outbox_test.go`, `pkg/api/outbox_api_test.go`).
- **PostgreSQL Server-Side Cursors & Extended Query Portal Batching (`pkg/engine`, `pkg/wire`)**:
  - Implemented full SQL-level server-side cursors in `pkg/engine`: `DECLARE <name> [SCROLL|NO SCROLL] [BINARY] CURSOR [{WITH|WITHOUT} HOLD] FOR <query>`, `FETCH [direction] [count] [FROM|IN] <name>`, `MOVE [direction] [count] [FROM|IN] <name>`, `CLOSE <name>`, and `CLOSE ALL`.
  - Added directional positioning for `FETCH` and `MOVE`: `NEXT`, `PRIOR`, `FIRST`, `LAST`, `ALL`, `FORWARD`, `BACKWARD`, `ABSOLUTE <n>`, `RELATIVE <n>`, and count-based chunking.
  - Enforced strict PostgreSQL transaction boundary semantics: standard (without hold) cursors are destroyed on `COMMIT`; `WITH HOLD` cursors persist across `COMMIT`; all cursors are immediately destroyed on `ROLLBACK`.
  - Implemented Extended Query Protocol portal batching in `pkg/wire`: parses `maxRows int32` from `Execute ('E')` payloads, streams up to `maxRows` tuples per batch, emits `MsgPortalSuspended ('s')` when rows remain, and emits standard `MsgCommandComplete ('C')` (`SELECT <total>` / `FETCH <count>`) upon completion.
  - Added comprehensive test suites in `pkg/engine/sqlexec_cursor_test.go` (`TestServerSideCursor_DeclareFetchMoveClose`, `TestServerSideCursor_TransactionHoldAndRollback`) and `pkg/wire/server_test.go` (`TestPGWire_ServerSideSQLCursor`, `TestPGWire_ExtendedQueryPortalBatching`).
- **Tolerant Virtual No-Op `USE <database>` Statement & Dynamic Session Database Synchronization (`pkg/engine`, `pkg/wire/tds`, `pkg/proc/runtime`)**:
  - Implemented tolerant virtual no-op acceptance for `USE <database>` statements (`USE master`, `USE [SalesDB]`, `USE "Analytics_DB"`, etc.) without syntax errors or multi-database catalog multiplexing overhead, preserving Instance-Level Isolation (Process per Database).
  - Added session database context tracking in `ClientSession.Database` and `ClientSession.Vars["current_database"]` / `["db_name"]`. Standard sessions default to `"postgres"`, while TDS sessions default to `"master"`.
  - Seamlessly synchronized scalar functions `DB_NAME()`, `CURRENT_DATABASE()`, `DB_ID()`, and `ORIGINAL_DB_NAME()` across table-less scalar SQL executions, VDBE virtual machine procedure runs (`OpCallBuiltin`), and `ClientSession` state.
  - Implemented official TDS `TokenEnvChange (0xE3)` (`EnvChangeDatabase`) token emission upon executing `USE <database>` over TDS connections, fully satisfying SSMS, DBeaver, and legacy MSSQL migration script requirements.
  - Added regression test suites in `pkg/engine/coordinator_test.go` (`TestTolerantUseDatabase`) and `pkg/wire/tds/server_test.go` (`TestTDS_TolerantUseDatabase`).
- **Always On Style Automatic Failover & Leader Election (`pkg/replication`, `cmd/server`, `pkg/config`, `pkg/ui`)**:
  - Implemented Always On style automated leader election and failover coordinator (`pkg/replication/failover.go`) with highest-LSN precedence, guaranteeing zero data loss.
  - Implemented monotonic term tracking (`term int64`) across nodes, leader announcements (`POST /api/replication/leader`), and peer voting (`POST /api/replication/vote`).
  - Added primary split-brain prevention (fencing): primary node immediately steps down to read-only replica upon detecting a higher term announcement or votes.
  - Added primary heartbeat timeout detection in secondary replicas (`ReplicationClient.LastHeartbeatTime()`) with automatic election triggering upon primary failure.
  - Added CLI flags and configuration support: `-auto-failover`, `-peers`, `-self-url`, `-heartbeat-timeout-ms`, and `-election-timeout-ms`.
  - Updated embedded Web Studio (`pkg/ui/dashboard.html`) to display cluster term and auto-failover topology state.
  - Added comprehensive unit and integration tests in `pkg/replication/failover_test.go` covering LSN precedence, split-brain prevention, quorum voting, and end-to-end auto-failover.
- **Declarative Schema Migration Engine & `lionsql` Unified CLI Tool (`cmd/lionsql`, `pkg/migrate`, `cmd/server`)**:
  - Implemented declarative database schema migration package `pkg/migrate` with automatic `_migrations` tracking table.
  - Implemented `lionsql` unified command-line executable (`cmd/lionsql`, `bin/lionsql`):
    - `lionsql migrate new <name>`: generates timestamped UTC migration pairs (`YYYYMMDDHHMMSS_<name>.up.sql` and `.down.sql`) with collision avoidance.
    - `lionsql migrate up`: executes pending migrations sequentially with atomic duration tracking and records applied versions into `_migrations`.
    - `lionsql migrate down`: rolls back applied migrations using `.down.sql` scripts and updates `_migrations`.
    - `lionsql migrate status`: displays a clean tabular view of migration versions, names, applied status, timestamps, and execution durations.
    - `lionsql sql "<query>"`: executes ad-hoc SQL statements headless directly against the embedded database engine and prints aligned tabular results.
    - `lionsql version` & `lionsql help`: displays database engine metadata, wire protocols, storage backends, and command syntax.
  - Added dual parity in `cmd/server/main.go` so `./bin/server migrate ...` and `./bin/lionsql migrate ...` operate with identical subcommands and flags.
  - Added unit and CLI lifecycle integration tests in `pkg/migrate/migrate_test.go` and `cmd/lionsql/main_test.go`.
- **Built-in Google & GitHub OAuth2 Social Login and Automatic User Provisioning (`pkg/api`, `pkg/auth`, `pkg/config`, `pkg/ui`)**:
  - Implemented standard OAuth2 authorization and token exchange for Google and GitHub identity providers with zero external dependencies (`pkg/api/oauth.go`, `handlers_oauth.go`).
  - Added REST endpoints: `GET /api/auth/oauth2/providers` (list active providers), `GET /api/auth/oauth2/url` (generate auth redirect URL with CSRF state), `POST /api/auth/oauth2/callback` (SPA / REST client exchange), and `GET /api/auth/oauth2/callback` (browser popup redirect with localStorage synchronization and `postMessage` event dispatching).
  - Implemented automatic superuser provisioning and account linking in `AuthService` (`ProvisionOAuthUser`): automatically provisions new accounts or links existing users by email or provider UID, issuing valid signed HS256 JWT tokens.
  - Added configuration support via `lionsql.json`, environment variables (`LIONSQL_OAUTH_GOOGLE_*`, `LIONSQL_OAUTH_GITHUB_*`), and administrative runtime management endpoints (`GET /api/settings/oauth2`, `PUT /api/settings/oauth2`) with automatic secret masking (`••••••••`).
  - Enhanced embedded Web Studio (`pkg/ui/dashboard.html`) login modal with Google and GitHub social login buttons and popup completion handlers.
  - Added comprehensive hermetic unit tests with mock OAuth2 servers in `pkg/api/oauth_test.go` and `pkg/auth/auth_test.go`.
- **Replica Node Zero-Allocation Read-Only Transaction Fast-Path (`pkg/engine`, `pkg/storage/memengine`)**:
  - Wired dynamic `IsReadOnlyFn` detection into `ClientSession`, automatically binding all sessions on secondary replicas to strict read-only transactions (`storage.TxOptions{ReadOnly: true}`).
  - Completely eliminated write-set buffers (`local`, `deleted`, `changes`, `savepoints`) and OCC base version tracking maps (`baseVers`) on read-only transactions, reducing memory allocations and GC overhead on high-frequency `SELECT` workloads.
  - Optimized `memengine` read operations (`Get`, `GetRow`, `Scan`, `IndexScan`, `ScanIndexOnly`, `ScanPartitions`) to bypass local change/delete map lookups and avoid cloning row data when `tx.opts.ReadOnly` is active.
  - Added test coverage in `pkg/engine/coordinator_test.go` (`TestReplicaSessionZeroAllocationFastPath`).
- **Embedded Web Studio HA Replication Monitoring & Promotion Dashboard (`pkg/ui`)**:
  - Implemented high-availability replication monitoring in embedded Web Studio (`:8090/_/`) with dedicated sidebar navigation and real-time status card (`view-replication`).
  - Added topology visualization displaying cluster role (`PRIMARY` / `REPLICA` / `STANDALONE`), active engine LSN offset, read/write transaction mode, and connected stream consumers.
  - Implemented connected secondary replica monitoring table detailing node ID, remote client address, streaming state (`STREAMING`, `CATCHING_UP`, `DISCONNECTED`), acked LSN, and physical replication lag.
  - Provided interactive failover promotion interface with confirmation modal dispatching `POST /api/replication/promote` to instantly promote secondary replicas to authoritative read-write primary.
  - Added 3-second auto-refresh polling loop with active view lifecycle management and graceful fallback for standalone instances.
  - Added unit test coverage in `pkg/ui/embed_test.go` (`TestReplicationMonitoringElements`).
- **MCV (Most Common Values) Skew List Statistics & Dual-Distribution Modeling (`C-7`) (`pkg/planner`)**:
  - Implemented `MCVItem` model and automatic top-frequent values extraction (`MaxMCVCount = 20`, frequency $\ge 1\%$ or $> 1.2\times$ average uniform frequency) in `BuildColumnStats` (`pkg/planner/stats.go`).
  - Decoupled selectivity estimation into an exact-match MCV phase and an equi-depth histogram interpolation phase on the non-MCV tail for `=`, `!=`, `IN`, `BETWEEN`, and range inequalities (`<`, `<=`, `>`, `>=`), resolving up to 10~100x cardinality estimation errors on non-uniformly distributed columns.
  - Ensured seamless persistence and deserialization of `MCVList` in `cbo_stats.json`.
  - Added comprehensive test coverage in `pkg/planner/stats_test.go` covering categorical skew, numerical skew with range queries, uniform distributions, single-value columns, and JSON roundtrip serialization.
- **Multi-Column Extended Statistics & Functional Dependency Modeling (`C-5`) (`pkg/planner`, `pkg/catalog`, `pkg/engine`)**:
  - Implemented `MultiColumnStats` and multi-column NDV collection in `pkg/planner/stats.go`, capturing joint column distributions to eliminate the Attribute Value Independence (AVI) cardinality underestimation on correlated predicates.
  - Implemented `ComputeFunctionalDependency` calculating dependency degree ($dep \in [0.0, 1.0]$) from multi-column NDV and single-column NDVs, bounding estimation between joint dependency and independent multiplication.
  - Integrated multi-column selectivity calculation in `EstimateCompoundSelectivity` and `EstimateExprSelectivity` (`flattenAndExpr`, `estimateAndConjunctsSelectivity`).
  - Added T-SQL DDL syntax support for `CREATE STATISTICS stat_name ON table_name (col1, col2, ...)` and `DROP STATISTICS` (`pkg/engine`, `pkg/catalog`).
  - Automatically analyzes and collects multi-column statistics for composite secondary indexes and registered statistic definitions during `ANALYZE TABLE`.
  - Added unit and integration tests in `pkg/planner/stats_test.go`, `pkg/catalog/catalog_test.go`, and `pkg/api/sqlexec_test.go`.
- **Selinger Dynamic Programming Join Order Optimization for $N > 4$ Tables (`B-4`) (`pkg/planner`)**:
  - Implemented `optimizeJoinOrderDP` using Selinger dynamic programming with subset bitmask state spaces (`1 << n`) and $O(1)$ flat-slice memoization, replacing exponential $O(N!)$ permutation generation.
  - Implemented primary connected pass pruning Cartesian cross products by validating join equality predicates against accumulated relation sets, with secondary fallback pass for queries with disconnected components.
  - Enforced bounded state space ($n \le 12$, Golden Rules 21 & 23) ensuring constant sub-millisecond CBO planning times on complex multi-table queries.
  - Added unit test coverage for 6-table and 8-table chain joins in `TestCBOJoinOrderOptimization_SelingerDP_LargeChain` and `TestCBOJoinOrderOptimization_SelingerDP_EightTables` (`pkg/planner/cbo_join_test.go`).
- **SSMS (SQL Server Management Studio) Object Explorer Virtual Catalogs & DMVs (`pkg/engine`)**:
  - Implemented SQL Server management catalog and DMV views required for SSMS Object Explorer navigation: `sys.configurations`, `sys.dm_exec_sessions`, `sys.dm_exec_connections`, `sys.database_principals`, `sys.server_principals`, `sys.dm_os_sys_info`, `sys.endpoints`, `sys.database_files`, and `sys.master_files`.
  - Added support for `sys.all_objects`, `sys.all_columns`, and `sys.all_views` with routine metadata integration.
  - Enriched `sys.databases` with database properties (`compatibility_level`, `collation_name`, `snapshot_isolation_state`, `recovery_model`, `page_verify_option`).
  - Added test coverage in `TestCoordinator_SSMS_VirtualCatalogQueries` (`pkg/engine/coordinator_test.go`).
- **Decoupled CDC (Change Data Capture) Event Streaming Framework (`pkg/cdc`, `pkg/api`, `cmd/server`)**:
  - Implemented decoupled CDC event model (`ChangeEvent`) and real-time streaming coordinator (`Streamer`) with bounded ring-buffer historical replay (Golden Rule 23) and non-blocking backpressure fan-out (Golden Rule 29).
  - Added HTTP streaming endpoint `GET /api/cdc/stream` supporting real-time Newline-Delimited JSON (NDJSON) and Server-Sent Events (SSE) with keep-alive heartbeat pings and table filtering (`?table=<name>&from_lsn=<lsn>`).
  - Added `AddWALHook` multiplexing across both In-Memory OCC (`memengine`) and WiredTiger (`wt_engine`, `wt_stub`) backends to synchronously hook storage commit boundaries.
  - Authored comprehensive integration guide and Python DuckDB micro-batch streaming recipe in `doc/CDC_DUCKDB_INTEGRATION.md`.
  - Added unit test coverage in `pkg/cdc/streamer_test.go` covering event conversion, ring-buffer replay, table filtering, and HTTP NDJSON streaming.
- **Asynchronous Auto-Analyze Background Statistics Daemon (`D-8`) (`pkg/engine`, `pkg/catalog`, `pkg/storage`)**:
  - Implemented `AutoAnalyzer` (`pkg/engine/auto_analyze.go`) tracking per-table mutation volumes via `AddWALHook` commit listeners.
  - Automatically triggers background parallel statistics analysis (`coordinator.AnalyzeTable`) when dirty row count crosses dynamic threshold (`min_threshold + base_rows * scale_factor`), keeping CBO column histograms fresh.
  - Ensured lock hierarchy immunity (Golden Rule 10) by keeping the transaction commit hot-path non-blocking and executing all catalog and storage inspections asynchronously in debounced background goroutines.
  - Added unit tests in `pkg/engine/auto_analyze_test.go` verifying threshold triggering, WAL hook listener, and atomic dirty counter resets.
- **Storage ➔ Wire Zero-Copy Direct Serialization (`P-3`) (`pkg/wire`, `pkg/wire/tds`)**:
  - Implemented `WriteNBCRowDirect` in TDS wire protocol emitting `TokenNBCRow` (0xD2) and null bitmaps directly into the network serialization buffer, eliminating per-row intermediate `[]any` slice allocations and buffer cloning.
  - Implemented single `scratchVals []any` allocation per result set outside row iteration in `executeRPCAndRespond` and `executeQueryAndRespond` (`pkg/wire/tds/server.go`).
  - Pre-allocated `scratchRowBytes [][]byte` outside the streaming loop in PostgreSQL Wire protocol `writeExecutionResult` and `runAndWriteQueryWithFormats` (`pkg/wire/server.go`), eliminating per-row heap allocations on the critical network streaming path.
- **T-SQL Core System Built-in Functions & Identity Tracking (`pkg/proc`, `pkg/engine`)**:
  - Implemented T-SQL system functions: `ISNUMERIC(expr)`, `CHARINDEX(pattern, str [, start])`, `PATINDEX(pattern, str)`, `STUFF(expr, start, len, replace)`, `IDENT_CURRENT('tableName')`, `COLUMNPROPERTY(id, col, prop)` (supporting `IsIdentity`, `AllowsNull`, `Precision`, `Scale`, `IsComputed`), and `SCOPE_IDENTITY()`.
  - Added per-table identity state tracking (`SetTableLastIdentity`, `GetTableLastIdentity`) in `Coordinator`.
  - Fixed session variable parameter substitution in `sqlexec.go` and `sqlexec_procs.go` to prevent single-at user variables from colliding with double-at system variables (`@@IDENTITY`).
  - Added tests across Go driver integration tests (`TestDriverIntegration_TSQLFunctions`), Python `pymssql` driver tests, and session identity tests (`TestSQLExecutor_ErrorAndIdentityInsert`).
- **TDS PLP (Partially Length-Prefixed) LOB Chunk Streaming (`pkg/wire/tds`)**:
  - Implemented [MS-TDS] 2.2.5.2 Partially Length-Prefixed (PLP) LOB stream encoding for `VARCHAR(MAX)`, `NVARCHAR(MAX)`, and `VARBINARY(MAX)` (> 8,000 bytes) payloads.
  - Configured `ColMetaData` to signal `0xFFFF` (65535) max length for unbounded LOB types.
  - Implemented `writePLPValue` streaming 8-byte Little-Endian total payload length, chunk headers (up to 8,000 bytes per chunk with 4-byte LE lengths), and 4-byte zero terminal chunk (`0x00000000`).
  - Added PLP 8-byte `0xFFFFFFFFFFFFFFFF` NULL value serialization across `TypeVarChar`, `TypeNVarChar`, and `TypeBigVarBin`.
  - Added unit test `TestTypePLP_LOBEncoding` (`token_test.go`), official Go driver integration test `TestDriverIntegration_PLP_LOBStreaming` (12,000 bytes), and Python `pymssql` driver verification (10,000 bytes).
- **TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET, and UNIQUEIDENTIFIER Binary Serialization (`pkg/wire/tds`)**:
  - Implemented complete SQL Server 2008 / TDS 7.3 wire compatibility for `DATE` (`0x28` / `TypeDateN`), `TIME` (`0x29` / `TypeTimeN`), `DATETIME2` (`0x2A` / `TypeDateTime2N`), `DATETIMEOFFSET` (`0x2B` / `TypeDateTimeOffsetN`), and `UNIQUEIDENTIFIER` (`0x24` / `TypeGuid`).
  - Serialized `DATE` values as 3-byte unsigned integers (Little-Endian days since `0001-01-01`) compliant with [MS-TDS] 2.2.5.5.1.2 without trailing metadata.
  - Serialized `TIME` values with scale specifier (0..7) and 3 to 5 bytes fractional seconds ticks since midnight according to [MS-TDS] 2.2.5.5.1.4.
  - Serialized `DATETIME2` values with scale specifier (0..7) and combined fractional seconds ticks since midnight followed by 3-byte Gregorian date in Little-Endian according to [MS-TDS] 2.2.5.5.1.5.
  - Serialized `DATETIMEOFFSET` values with scale specifier (0..7), UTC `DATETIME2` payload, and 2-byte signed Little-Endian timezone offset in minutes according to [MS-TDS] 2.2.5.5.1.6.
  - Implemented Windows GUID Mixed-Endian byte-swapping for `UNIQUEIDENTIFIER` (Little-Endian for Data1, Data2, and Data3, Big-Endian for Data4) ensuring zero data corruption when communicating with standard ADO.NET and Go MSSQL drivers.
  - Enhanced column inference in `inferColumnInfos` to accurately detect DATE, TIME, DATETIME2, DATETIMEOFFSET, and UUID columns from result sets while preserving standard sys table DATETIME semantics (`create_date`, `modify_date`).
  - Added unit tests in `pkg/wire/tds/token_test.go` and official driver integration tests in `pkg/wire/tds/driver_integration_test.go` verifying binary decoding using `github.com/microsoft/go-mssqldb` (`mssql.UniqueIdentifier` and `time.Time`).
- **External Multi-Language Client Driver E2E Integration Test Harness (`pkg/wire`, `pkg/wire/tds`)**:
  - Implemented hermetic, automated integration test suites validating external ecosystem client drivers: Python `psycopg3` (PostgreSQL Wire Protocol :5433) in `pkg/wire/driver_external_test.go` and Python `pymssql` (TDS Protocol :1433 / FreeTDS dblib) in `pkg/wire/tds/driver_external_test.go`.
  - Verified DDL table creation, CRUD operations, NULL value semantics, explicit transaction atomicity (`COMMIT` and `ROLLBACK`), and binary serialization of `DATE`, `TIME`, and `UNIQUEIDENTIFIER` into native Python types (`datetime.date`, `datetime.time`, `uuid.UUID`).
  - Fixed unsolicited `TokenInfo` emission on regular DDL queries in `pkg/wire/tds/server.go`, ensuring FreeTDS dblib and `pymssql` do not treat informational tokens as database errors.
  - Employed `uv run` isolated ephemeral sandboxing with automatic fallback/skipping when `uv` is unavailable (`exec.LookPath`), preserving deterministic, flakeless test execution (Golden Rule 17).

### Removed
- **Redundant In-Memory MVCC Vacuum & Shared-Memory Arrow Snapshot Backlog Pruning**:
  - Removed in-memory OCC background vacuum worker proposal from engineering backlogs. LionSQL's in-memory OCC engine commits mutations atomically via version swaps and discards aborted workspaces without accumulating multi-version dead tuple chains, rendering background vacuum worker threads redundant (YAGNI, Golden Rule 15).
  - Removed POSIX shared-memory (`/dev/shm`, `mmap`) Apache Arrow IPC table snapshot proposal. Serializing entire tables into Arrow IPC files incurs high CPU/memory scan overhead and OS portability friction; unified analytical ingestion (DuckDB, external brokers) behind the transaction-atomic CDC / WAL streaming pipeline.
  - Documented architectural rationale and spec-out criteria in `notes/ENGINEERING_CONTEXT.md` (Invariants 7 and 8).

---

## [0.19.0]

### Changed
- **Documentation Architecture Streamlining & Single Source of Truth (SSOT) Consolidation**:
  - Consolidated macro release roadmap into single source of truth at `ROADMAP.md` and removed duplicate `ROADMAP.ko.md`.
  - Consolidated engineering backlog into `TODO.ko.md` and deleted redundant `notes/REFACTORING_AND_ROADMAP.md`.
  - Synchronized verified Phase 5 achievements across all roadmaps and updated documentation index references in `GEMINI.md`, `GEMINI.ko.md`, `notes/README.md`, `README.md`, and `doc/REFERENCE_MANUAL.ko.md`.
### Fixed
- **Table-Qualified Wildcard Projections in Volcano Execution Engine (`pkg/execution`)**:
  - Fixed an issue where GUI database managers (such as Beekeeper Studio and DbGate) received null column values when browsing table contents.
  - GUI clients frequently generate table-qualified wildcard projections, such as `SELECT "basetbl".* FROM "public"."users" "basetbl" LIMIT 50 OFFSET 0` (PostgreSQL) or `SELECT TOP 50 [dbo].[users].* FROM [dbo].[users]` / `SELECT TOP 50 [t].* FROM [dbo].[users] [t]` (SQL Server TDS).
  - Enhanced `ProjectionIterator` in `pkg/execution/project.go` to recognize both unqualified (`*`) and table-qualified wildcards (`.*`, `alias.*`, `schema.table.*`).
  - Switched column alias extraction from `strings.Index` to `strings.LastIndex` to cleanly strip multi-part database and schema prefixes (e.g. `[dbo].[users].[col]`).
  - Added comprehensive regression tests across `pkg/engine/coordinator_test.go`, `pkg/wire/server_test.go`, and `pkg/wire/tds/driver_integration_test.go`.
- **Graceful Shutdown Active Connection Teardown & Bounded Teardown (`cmd/server`, `pkg/wire`, `pkg/wire/tds`)**:
  - Fixed an issue where graceful shutdown hung or took a long time when GUI clients (such as DbGate, Beekeeper) held idle TCP connections open.
  - TDS (`pkg/wire/tds/server.go`) was waiting indefinitely on `sync.WaitGroup` because listener closure alone did not unblock clients blocked in read loops.
  - Implemented active connection tracking across both TDS and PGWire listeners, closing active client sockets upon server teardown so reading goroutines unblock immediately.
  - Enforced a strict 1.5-second bounded shutdown deadline in `cmd/server/main.go` adhering to Golden Rule 4.
  - Added regression test `TestTDS_GracefulCloseWithActiveConnections` in `pkg/wire/tds/server_test.go`.
- **WiredTiger Cursor EOF Wrap-Around Prevention & Scan Latches (`pkg/storage/wiredtiger`, `pkg/execution`)**:
  - Fixed an infinite loop / loading hang when browsing tables in GUI database clients (Beekeeper Studio, DbGate) across both PostgreSQL and SQL Server wire protocols.
  - In WiredTiger C API, calling `cursor->next()` after reaching EOF (`WT_NOTFOUND`) wraps around and repositions the cursor back to the first row.
  - In vectorized chunk iteration (`NextChunk`), tables with fewer than 1024 rows returned the first batch successfully, but subsequent chunk pulls invoked `cursor.Next()`, re-scanning the table indefinitely and locking up `SortIterator`, `AggregateIterator`, and query execution.
  - Added an explicit `eof` boolean latch to `WTCursor` (`pkg/storage/wiredtiger/wt_engine.go`) as well as `SeqScanIterator` and `IndexScanIterator` (`pkg/execution/scan.go`), ensuring cursors permanently return `false` / `io.EOF` once depleted.
  - Added unit test `TestWiredTiger_CursorNextEOFNoWrapAround` and coordinator test `TestWiredTiger_Coordinator_DbGateTableQuery` in `pkg/storage/wiredtiger/wt_engine_test.go`, plus DbGate query regression tests in `pkg/engine/coordinator_test.go` and `pkg/wire/tds/driver_integration_test.go`.

### Added
- **Vectorized Chunk Iterator & Batch Volcano Execution (`P-4`) (`pkg/execution`, `pkg/engine`)**:
  - Implemented `Chunk` (preallocated slice of `Tuple`, `DefaultChunkSize = 1024`, `MinChunkSize = 64`) and `ChunkedIterator` interface with `NextChunk(ctx context.Context, dest *Chunk) error` in `pkg/execution/chunk.go`.
  - Implemented `PullChunk(ctx, it, dest, maxChunkSize)` providing fast-path zero-virtual-dispatch chunk pulling when child implements `ChunkedIterator`, with fallback buffering loop when child implements standard `Iterator`.
  - Upgraded core relational operators to vectorized chunk streaming:
    - `SeqScanIterator`: Ingests tuples directly into chunk buffers (`bufChunk`), eliminating per-tuple interface dispatch and supporting raw lazy tuple batching.
    - `IndexScanIterator`: Buffers index traversal hits into chunks.
    - `FilterIterator`: Evaluates WHERE/HAVING predicates in a vectorized inner loop over input chunks (`inChunk`), filtering batches directly into output chunks (`outChunk`).
    - `ProjectionIterator`: Projects expressions in a vectorized loop over input chunks (`inChunk`) into destination chunks (`outChunk`).
    - `LimitIterator`: Employs cursor index tracking (`inIdx`) over child chunks to guarantee precise LIMIT and OFFSET evaluation while strictly preserving Volcano short-circuiting.
    - `AggregateIterator`: Drains child relations in 1,024-tuple chunks into group accumulators (`groupAccumulator`) during `Open`, and emits materialized groups in chunks.
    - `SortIterator`: Materializes child records in batches during `Open`, spills runs, and emits sorted tuples (both in-memory quicksort and external merge min-heap) in chunks.
    - `HashJoinIterator`: Vectorizes right-side build phase ingestion and outer probe iteration via `pullLeft` and `NextChunk`.
    - `IndexNestedLoopJoinIterator`: Vectorizes outer relation pulling via `pullOuter` and streams joined tuples in chunks.
    - `pkg/engine/sqlexec_select.go`: Drains execution plan in 1,024-tuple batches via `execution.PullChunk`, reducing CPU loop overhead and context cancellation checking by over 99%.
  - Maintained 100% transparent backward compatibility: all operators retain standard `Next(ctx context.Context) (Tuple, error)` serving transparently from internal chunk buffers.
  - Added comprehensive unit tests and benchmarks in `pkg/execution/execution_test.go` (`TestVectorizedChunk_PipelineStreaming`, `TestVectorizedChunk_Aggregation`, `BenchmarkVectorizedChunkVsRowByRow`).
- **Binary Tuple Lazy Decoding & Covering Index Non-Key Column Support (`P-2`) (`pkg/storage`, `pkg/execution`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**:
  - Implemented `SkipTupleColumn` in `pkg/storage/tuple.go` enabling zero-allocation forward navigation through binary tuple columns without allocating string or heap buffers.
  - Implemented `DecodeTupleSelected(schema, data, selectedCols)` and `LazyTuple` on-demand column decoding in `pkg/storage/tuple.go`, caching column offsets on forward scans to support out-of-order random column access in $O(1)$ time.
  - Added `storage.RawTupleCursor` interface and wired `SeqScanIterator` and `IndexScanIterator` to construct `Tuple` with `LazyTuple`, allowing `FilterIterator` (WHERE predicate) to decode only predicate columns while completely bypassing decoding and heap allocations for rejected rows.
  - Implemented covering index syntax `CREATE INDEX ... INCLUDE (col1, col2, ...)` across `pkg/proc/parser.go`, `pkg/proc/ast.go`, and `pkg/engine/sqlexec_ddl.go`.
  - Added `IncludeCols` to `storage.IndexSchema` and extended `SkipList` / `IndexData` in `pkg/storage/memengine` to store non-key covering values (`IndexItem` / `SkipListEntry.IncludeValues`).
  - Extended CBO index-covering detection in `pkg/planner/planner.go` (`isCovering`) and `memengine.IndexOnlyScan` to resolve included non-key columns directly from the index payload without touching table heap storage.
  - Added comprehensive test suites verifying selective decoding, random access, out-of-order resolution, and covering index execution across `pkg/storage/tuple_test.go`, `pkg/planner/planner_test.go`, `pkg/execution/execution_test.go`, `pkg/proc/sqlparser_test.go`, and `pkg/engine/coordinator_test.go`.
- **TDS NBCROW Row Compression Token (`0xD2`) (`pkg/wire/tds`)**:
  - Implemented `BuildNBCRow(cols []ColumnInfo, values []any)` in `pkg/wire/tds/token.go` compliant with [MS-TDS] 2.2.7.13 NBCROW specification.
  - Added null bitmap header (`ceil(N / 8)` bytes) preceding row column data, setting bit $k$ ($k/8, k\%8$) to 1 for NULL columns and eliminating all payload bytes (0-byte encoding) for NULL fields.
  - Refactored `writeColumnValue` helper in `token.go` to unify non-NULL value serialization across `BuildRow` and `BuildNBCRow` with zero duplication.
  - Integrated dynamic row format negotiation in `pkg/wire/tds/server.go`: tracking negotiated TDS version (`sess.TDSVersion >= 0x73000000`) and automatically emitting `TokenNBCRow` (`0xD2`) when rows contain NULL values (`sess.buildRow`), retaining `TokenRow` (`0xD1`) for all-non-NULL rows.
  - Added comprehensive unit tests in `pkg/wire/tds/token_test.go` and `server_test.go` verifying single/multi-column null bitmaps, multi-byte bitmaps (10+ columns), and raw packet emission.
  - Added official client integration test in `pkg/wire/tds/driver_integration_test.go` verifying `go-mssqldb` scanning NULL values into `sql.NullString` and `sql.NullInt64`.
- **Fast-Path SQL Fingerprinting & Simple PK Lookup Storage Bypass (`P-5`) (`pkg/engine`, `pkg/storage`)**:
  - Implemented ultra-fast SQL template recognition and 64-bit FNV-1a fingerprint hashing in `pkg/engine/pk_bypass.go`.
  - Added zero-overhead bypass path routing simple single-PK point lookups (`SELECT ... FROM tbl WHERE id = ?`) directly to storage engine index lookup (`tx.Get`), bypassing regular Pratt parser, AST allocations, CBO planner, and VDBE bytecode compilation.
  - Added bounded plan cache `pkBypassCache` capped at 2,048 entries with automatic schema version tracking and invalidation on DDL mutations (`InvalidatePKBypassCache`).
  - Added Prometheus metric `lionsql_pk_bypass_hits_total` to track bypass execution hits.
  - Comprehensive support for composite qualifiers (`[db]..[tbl]`), implicit and explicit table aliases, bracketed/quoted identifiers, reverse operands (`WHERE 1 = id`), and `LIMIT 1` / `TOP 1` clauses.
  - Added comprehensive test suite `pkg/engine/pk_bypass_test.go` verifying integer PK, string PK, transaction isolation, non-PK fallback, complex query fallback, and DDL cache invalidation.
- **Sort-Merge Join (SMJ) Operator & CBO Cost Model (`B-3`) (`pkg/execution`, `pkg/planner`, `pkg/engine`)**:
  - Implemented `MergeJoinIterator` in `pkg/execution/merge_join.go` providing $O(M+N)$ single-pass streaming joins with zero in-memory hash table overhead.
  - Implemented Mark & Restore buffer windowing on the right-side relation to handle N:M duplicate join keys and duplicate Cartesian products.
  - Full join type support: `INNER`, `LEFT`, `RIGHT`, `FULL`, `SEMI`, and `ANTI` joins with streaming NULL extension and SQL-compliant NULL key handling.
  - Added `NodeMergeJoin` (`"Merge Join"`) plan node, `MergeCond` AST modeling, and plan text formatting in `pkg/planner/planner.go`.
  - Added sort property (Interesting Orders) detection and cost formulas (zero sort cost for pre-sorted inputs, $N \log N$ for unsorted inputs) competing against Hash Join and Index Nested Loop Join.
  - Automatic `NewSortIterator` wrapping in `pkg/execution/builder.go` for child inputs when relations are not pre-sorted.
  - Added unit test suite in `pkg/execution/merge_join_test.go`, planner test suite in `pkg/planner/cbo_join_test.go`, and E2E query tests in `pkg/engine/coordinator_test.go`.
- **CBO Optimizer Hints Override for Index and Join Strategies (`D-9`) (`pkg/planner`, `pkg/proc`, `pkg/engine`)**:
  - Implemented T-SQL table hints parsing and enforcement: `WITH (INDEX(<name>))` for forcing specific index scan, `WITH (INDEX(0))` / `WITH (INDEX())` for forcing full table scan, and comma-separated hints (e.g. `WITH (NOLOCK, INDEX(...))`).
  - Implemented query hints: `OPTION (HASH JOIN)`, `OPTION (LOOP JOIN)`, and `OPTION (FORCE ORDER)` in `pkg/proc/parser.go` and `pkg/planner/planner.go`.
  - Implemented inline join hints: `INNER HASH JOIN`, `INNER LOOP JOIN`, `LEFT HASH JOIN`, `LEFT LOOP JOIN`, etc.
  - Added join permutation bypass in CBO planner when `FORCE ORDER` is specified to evaluate joins in the written query order.
  - Added direct routing in execution coordinator for queries containing table or query hints.
  - Added unit and integration test coverage in `pkg/planner/cbo_join_test.go` and `pkg/engine/coordinator_test.go`, and marked `D-9` complete in `TODO.ko.md` and `ROADMAP.md` / `ROADMAP.ko.md`.
- **CBO Boolean Tree & OR Selectivity Estimation (`pkg/planner`)**:
  - Implemented `TableStats.EstimateExprSelectivity()` to recursively evaluate AST expression trees for complex boolean filters.
  - Added same-column mutually exclusive equality OR summation ($P(A \cup B) = P(A) + P(B)$) and general independent OR union ($P(A \cup B) = P(A) + P(B) - P(A) \cdot P(B)$).
  - Added exact selectivity estimation for `NOT` expressions ($1.0 - P(A)$), `IN (...)` predicates based on NDV, and `BETWEEN` range quantile bucket interpolation.
  - Enhanced `TableStats.EstimateCompoundSelectivity()` to parse incoming filter strings into AST expressions with fallback to regex and heuristic estimators.
  - Added unit test suite `TestTableStats_BooleanTreeAndORSelectivity` in `pkg/planner/stats_test.go` and marked `C-6` as complete in `notes/REFACTORING_AND_ROADMAP.md`.
- **`FULL OUTER JOIN` & `RIGHT OUTER JOIN`, `COALESCE` / `ISNULL` Short-Circuiting, and Syntax Backlog Completion (`pkg/engine`, `pkg/proc`, `pkg/planner`, `pkg/execution`)**:
  - Implemented `FULL [OUTER] JOIN` and `RIGHT [OUTER] JOIN` in Volcano `HashJoinIterator`, tracking inner/build side matches and draining unmatched right-side tuples with `mergeNullsWithTuple()`.
  - Extended Pratt parser `parseGeneralSelect()` with `TokenFull` and `TokenRight` keywords, supporting optional `OUTER` and required `JOIN`.
  - Updated CBO planner to select `HashJoin` for `FULL` and `RIGHT` outer joins and enriched `EXPLAIN` plan output with `(Right)` and `(Full)`.
  - Added short-circuit lazy evaluation for `COALESCE` and `ISNULL` in `evalRowFunction()`, and fixed column name fallback to preserve NULL column values.
  - Prioritized parsed expression evaluation in `ProjectionIterator` to ensure aliased calculated expressions take precedence over input column shadowing.
  - Added comprehensive unit and integration tests in `pkg/proc/sqlparser_test.go` and `pkg/engine/coordinator_test.go`.
  - Marked all 7 items (`S-1` through `S-7`) in Section 4 of `notes/REFACTORING_AND_ROADMAP.md` as 100% complete.
- **`CROSS APPLY` / `OUTER APPLY` Joins, Local `#temp` Session Isolation, Table/Query Hints, and DML `OUTPUT` (`pkg/engine`, `pkg/proc`, `pkg/planner`)**:
  - Implemented `CROSS APPLY` and `OUTER APPLY` join execution engine in `Coordinator.execApplySelect()` (`pkg/engine/sqlexec_apply.go`), supporting correlated subquery lateral joins and table-valued functions (TVFs) with outer row parameter binding.
  - Added enterprise-grade session isolation for local temporary tables (`#temp_table`) via automatic name mangling (`__temp_<sessionID>_<tableName>`), multi-session collision prevention, identifier rewriting in `RewriteSessionTempTables()`, and safe lifecycle teardown hooks on connection close.
  - Implemented T-SQL table hints parsing and execution (`WITH (NOLOCK)` as a non-blocking OCC no-op) and query hints (`OPTION (RECOMPILE, MAXDOP n)`) with plan cache bypass.
  - Fully wired DML `OUTPUT` clauses (`INSERT ... OUTPUT INSERTED.*`, `UPDATE ... OUTPUT DELETED.*, INSERTED.*`, `DELETE ... OUTPUT DELETED.*`) through Pratt AST (`OutputCols`) and execution coordinator.
  - Added comprehensive test suites in `pkg/proc/sqlparser_test.go` and `pkg/engine/coordinator_test.go`.
- **`FROM` Derived Table Subqueries & `WHERE EXISTS` Correlated Predicates (`pkg/engine`, `pkg/proc`, `pkg/planner`, `pkg/execution`)**:
  - Implemented `FROM (SELECT ...) AS t` and `JOIN (SELECT ...) AS o ON ...` inline derived table subqueries in `Coordinator.resolveDerivedTables()`.
  - Added recursive derived table resolution, dynamic schema and data type inference, automatic materialization into isolated ephemeral tables (`#__dt_*`), and clean transaction lifecycle teardown.
  - Extended Pratt parser `TableRef` with `Subquery *GeneralSelectStmt` to model derived tables as first-class AST nodes in `parseGeneralSelect()`.
  - Verified `WHERE [NOT] EXISTS` correlated subqueries converting to SEMI and ANTI joins via `DecorrelateSubqueries` and Volcano `HashJoinIterator` / `IndexNestedLoopJoinIterator`, and uncorrelated EXISTS via `resolveSubqueries()`.
  - Added comprehensive test suites in `pkg/proc/sqlparser_test.go` and `pkg/engine/coordinator_test.go` covering nested derived tables, aggregations, joins, EXPLAIN, and correlated/uncorrelated EXISTS predicates.
- **`INSERT INTO ... SELECT` Batch DML Loading & Expression Evaluation Hardening (`pkg/engine`, `pkg/proc`, `pkg/execution`)**:
  - Implemented `INSERT INTO <table> [(cols...)] [OUTPUT ...] SELECT ...` batch DML loading in `Coordinator.execInsertSelect()`.
  - Added schema projection, explicit target column mapping, dynamic data type coercion via `ConvertValue()`, and automatic column count validation.
  - Implemented identity column auto-increment generation and `SET IDENTITY_INSERT <table> ON/OFF` enforcement on batch insert operations.
  - Fully integrated `ON CONFLICT (...) DO NOTHING / DO UPDATE` (with `EXCLUDED.<col>` evaluation) and `OUTPUT INSERTED.*` delta projection with `INSERT INTO ... SELECT`.
  - Supported inter-table batch copies across persistent tables, self-inserts, and session-scoped temporary tables (`#temp`).
  - Extended Pratt parser `GeneralInsertStmt` to represent `Select` query branches and updated parser methods `parseInsert` and `parseGeneralInsert` to consume `SELECT` cleanly.
  - Fixed expression evaluation in `pkg/execution/project.go` `ProjectionIterator` to parse arithmetic and general expressions (`+-*/%()><=!|&,`) rather than skipping them, enabling full evaluation of computed columns in Volcano select queries.
  - Added comprehensive unit tests in `pkg/proc/sqlparser_test.go` and `pkg/engine/coordinator_test.go` covering full table copies, filtered inserts, identity management, empty result sets, conflict updates, and output clauses.
- **Full Architecture Layering: SQL Execution Coordinator Separation (`pkg/engine`, `pkg/api`, `pkg/wire`, `pkg/wire/tds`)**:
  - Migrated the entire SQL execution orchestration subsystem (13 `sqlexec_*.go` files, ~14,150 lines) from `pkg/api` to `pkg/engine`: `sqlexec.go`, `sqlexec_auth.go`, `sqlexec_dbcc.go`, `sqlexec_ddl.go`, `sqlexec_dml.go`, `sqlexec_eval.go`, `sqlexec_explain.go`, `sqlexec_merge.go`, `sqlexec_pgcatalog.go`, `sqlexec_pivot.go`, `sqlexec_prepare.go`, `sqlexec_procs.go`, and `sqlexec_select.go`.
  - Converted execution receivers to `(s *Coordinator)` and deleted transitional `exec_stub.go`.
  - Added `ConvertValue` helper in `pkg/engine/query_utils.go` and deduplicated conversion logic across `handlers_procs.go` and `sqlexec_select.go`.
  - Completely eliminated `lionsql/pkg/api` imports from `pkg/wire/server.go` and `pkg/wire/tds/server.go`, establishing a strict unidirectional dependency graph (`wire` -> `engine`, `api` -> `engine`).
  - Added `pkg/engine/coordinator_test.go` verifying standalone DDL, DML, DQL, transaction commit/rollback, and cancellation with zero HTTP dependencies.
  - `engine.Coordinator` now solely owns sessions, session pool, metrics, slow/general query loggers, work_mem, and global temp table tracking; `api.Server` delegates to it and maintains 100% backward compatibility for HTTP/studio endpoints and tests.

---

## [0.18.0]

### Added
- **Leader/Follower Group Commit & Concurrency Hardening for memengine WAL (`pkg/storage/memengine`)**:
  - Under `FsyncAlways`, `WALWriter.Append` builds a single contiguous `[4-byte length | payload]` frame outside the lock, performs a single `write(2)` syscall under `mu`, and releases `mu` before syncing; the first waiter on `syncMu` becomes the sync leader and one `fsync` makes every frame written so far durable while followers return once covered. Lock order is `syncMu` -> `mu`.
  - Enforced strict monotonic LSN and causal row-version ordering (`appendWALFrameLocked` + `finishWALCommitLocked`): `Engine.walOrderMu` atomically pairs `nextLSN.Add(1)`, `WALWriter.writeFrame`, and `walHook` invocation while shard locks are still held, and releases shard/table locks (`unlockAll()`) before waiting on group commit `fsync` (`syncForPolicy`) and AOF compaction.
  - Fixed `WALWriter.Truncate()` to preserve `os.O_APPEND` when reopening the truncated WAL file, and changed `WALWriter.policy` to `atomic.Int32` (`SetPolicy` / `Policy`) to prevent data races when `Append` reads `policy` outside `mu`.
  - Propagated `WALWriter.Append` and `rewriteAOFLocked` errors through `recordWALLocked` to `Transaction.Commit` and DDL methods (Rule 7 compliance).
  - Guarded concurrent AOF compaction under `Engine.mu.RLock()` with `compacting` atomic CAS, `diskMu` serialization, and canonical table/partition lock ordering (`td.mu` -> `td.partMu`) in `saveDiskLocked()` and `syncShardsToMapsLocked()` to eliminate map read/write data races during concurrent commits and snapshot rewrites.
  - Added `SyncCount()`, `TestMemEngine_GroupCommit_ConcurrentAOFCompaction`, `TestMemEngine_GroupCommit_StrictLSNOrdering`, and group commit unit/benchmark tests (`wal_groupcommit_test.go`), plus `-fsync` (`off`, `none`, `everysec`, `always`) flag support in `cmd/tpcc_vdbe` defaulting to `everysec` across all storage engines. Measured on Apple M1 (`FsyncAlways`): 16 workers 3.72ms -> 0.062ms/op, 64 workers 3.81ms -> 0.038ms/op; TPC-C VDBE reached 1,863 TPS (49,922 tpmC) under `-fsync always` and 16,015 TPS (430,321 tpmC) under `-fsync everysec`.
  - **WiredTiger Group Commit & Commit Fast-Path (`pkg/storage/wiredtiger`)**: Implemented `SetFsyncPolicy`, `FsyncPolicy`, `SyncCount`, and leader/follower group commit via `WT_SESSION::log_flush("sync=on")` in `wiredtiger.Engine`, eliminated global `engine.mu.Lock()` contention in `WTTx.Commit()`, and skipped redundant `walOps`/`undoLog` slice allocations when `walHook` or savepoints are not active. Decoupled `FsyncEverySec` periodic syncing into a dedicated background worker goroutine (`startPeriodicSync`) running on a 1-second ticker, completely offloading journal flush latency from client transaction commit hot paths. Defaulted WiredTiger engine fsync policy to `FsyncEverySec`, matching production durability standards while achieving 14,400~14,800 TPS (420,000+ tpmC) in TPC-C VDBE benchmarks, and 2,823 TPS (77,481 tpmC) under `-fsync always`.
- **Core Architecture: SQL Execution Coordinator Interface & Engine Foundation (`pkg/engine`, `pkg/wire`, `pkg/wire/tds`)**:
  - Introduced `engine.SQLCoordinator` interface contract and `engine.QueryResult` / `engine.ActiveQueryInfo` in `pkg/engine/coordinator.go` to eliminate layer inversion where wire protocol handlers depended directly on HTTP API server internals.
  - Implemented `engine.Coordinator` struct (`pkg/engine/coordinator_impl.go`) and interactive session management primitives (`engine.ClientSession`, `engine.PreparedStatement` in `pkg/engine/session.go`), managing connection pooling, query timeouts, cancellation contexts, SPID allocation, and session variables in the core database layer.
  - Implemented `engine.SQLCoordinator` on `api.Server`, aliasing `api.QueryResult` and `api.ActiveQueryInfo` to `engine` for 100% backward compatibility across tests and handlers, and added `Server.Coordinator()` accessor.
  - Decoupled PostgreSQL Wire Protocol (`pkg/wire/server.go`) and TDS 7.x/8.0 Protocol (`pkg/wire/tds/server.go`) listeners to depend strictly on `engine.SQLCoordinator`, adding `NewServerWithCoordinator` and `SetCoordinator` to allow clean standalone wire execution without HTTP coupling.

---

## [0.17.0]

### Added
- **Native T-SQL CAST, CONVERT, TRY_CAST, TRY_CONVERT & PostgreSQL `::` Operator (`pkg/proc`, `pkg/proc/runtime`)**:
  - **Syntax & Lexer**: Added keywords `CAST`, `TRY_CAST`, `CONVERT`, `TRY_CONVERT` and token `TokenDoubleColon` (`::`) with Pratt parser support for `CAST(expr AS target_type)`, `TRY_CAST(expr AS target_type)`, `CONVERT(target_type, expr [, style])`, `TRY_CONVERT(target_type, expr [, style])`, and PostgreSQL-compatible `expr::target_type`.
  - **AST & Compiler**: Added `CastExpr` node and VDBE opcode `OpCast` with `CastData` auxiliary payload (`TargetType`, `Precision`, `Scale`, `Style`, `Try`).
  - **Runtime & Evaluation**: Implemented `EvalCast()` supporting standard SQL and T-SQL conversion semantics across `INT`, `FLOAT`, `VARCHAR` (with style formatting and precision truncation), `BOOL`, `DATETIME`, `DATE`, `TIME`, `DECIMAL`, `UUID`, and `BINARY`.
  - **Safe Casting**: Supported fail-safe casting in `TRY_CAST` and `TRY_CONVERT` returning `NULL` on parsing failure without runtime panic or procedure abort, while `CAST` and `CONVERT` trigger standard execution errors.
  - **Testing**: Added unit test suite `TestParseSQL_CastAndConvert` in `pkg/proc/sqlparser_test.go` and VM integration test `TestVM_CastAndConvert` in `pkg/proc/runtime/builtins_vm_test.go`.
- **Advanced T-SQL Built-in Functions & Bitwise Operators (`pkg/proc`, `pkg/proc/runtime`)**:
  - **Logical Extremes**: Added `GREATEST(expr1, ...)` and `LEAST(expr1, ...)` supporting numeric and string lexical comparisons across arbitrary argument counts with NULL skipping.
  - **Message Formatting**: Added `FORMATMESSAGE(msg, params...)` supporting `%s`, `%d`, `%i`, `%u`, and `%%` escapes.
  - **Bit Manipulation (T-SQL 2022)**: Added `BIT_COUNT()`, `GET_BIT()`, `SET_BIT()`, `LEFT_SHIFT()`, and `RIGHT_SHIFT()`.
  - **Timezone Offsets**: Added `SWITCHOFFSET()` and `TODATETIMEOFFSET()` with ISO fixed offset calculations and timezone-aware layout parsing in `commonDateFormats`.
  - **Metadata & Properties**: Added `DATABASEPROPERTYEX()`, `OBJECTPROPERTY()`, `COLUMNPROPERTY()`, `INDEXPROPERTY()`, `OBJECT_SCHEMA_NAME()`, `TYPE_ID()`, and `TYPE_NAME()`.
  - **Security & Roles**: Added `IS_SRVROLEMEMBER()`, `IS_MEMBER()`, `HAS_PERMS_BY_NAME()`, `PWDENCRYPT()`, and `PWDCOMPARE()`.
  - **Global Session State**: Added `@@TRANCOUNT`, `@@LOCK_TIMEOUT`, `@@OPTIONS`, `@@NESTLEVEL`, `@@CPU_BUSY`, `@@IO_BUSY`, and `@@IDLE`.
  - Registered all new functions in `IsBuiltinFunction` and added comprehensive unit test suite `TestBuiltin_AdvancedTSQLFunctions` in `pkg/proc/builtins_test.go`.
- **Expanded T-SQL and System Built-in Scalar Functions (`pkg/proc`, `pkg/proc/runtime`)**:
  - **Date & Time**: Added `DATENAME()`, `CURRENT_TIMESTAMP`, `SYSUTCDATETIME()`, `SYSDATETIMEOFFSET()`, `SMALLDATETIMEFROMPARTS()`, and `DATEDIFF_BIG()`. Enhanced `normalizeDatePart` and `evalDatePart` with `weekday` (1=Sunday) and `dayofyear`.
  - **String Manipulation**: Added `DATALENGTH()` (raw byte counting), `PARSENAME()` (4-part object piece splitting with bracket preservation), `STRING_ESCAPE()` (JSON control characters escape), and enhanced `TRIM()` with custom cutset support.
  - **Math & Numeric**: Added `SQUARE()` for float and integer square computations.
  - **JSON Processing**: Added `ISJSON()` with optional type constraints (`VALUE`, `ARRAY`, `OBJECT`, `SCALAR`) and `JSON_MODIFY()` for dot-path property updates and key deletions.
  - **System & Security Context**: Added `HOST_NAME()`, `HOST_ID()`, `CURRENT_USER`, `USER_NAME()`, `SUSER_NAME()`, `SUSER_SNAME()`, `USER_ID()`, `SUSER_ID()`, `@@VERSION`, `@@SERVERNAME`, `@@SPID`, `@@MAX_CONNECTIONS`, `@@LANGUAGE`, and `COL_NAME()`.
  - Registered all new functions in `IsBuiltinFunction` and added comprehensive unit test suite `TestBuiltin_TSQLExpansions` in `pkg/proc/builtins_test.go`.

### Documentation
- **Temporary Tables & Table Variables Implementation Roadmap (`TODO.ko.md`, `ROADMAP.md`, `ROADMAP.ko.md`)**:
  - Structured 3-phase engineering roadmap for session-isolated local temporary tables (`#temp_table`) and execution-frame-scoped table variables (`DECLARE @tbl TABLE`).
  - Outlined name mangling isolation, Pratt parser grammar extensions, VDBE frame lifecycle integration, non-transactional rollback mechanics, and multi-protocol concurrency verification.

---

## [0.16.0]

### Added
- **Complete T-SQL Official Built-in Scalar Function Suite (Groups A~F) (`pkg/proc`, `pkg/proc/runtime`)**:
  - **Group A (Execution Context & Error Introspection)**: Added `ERROR_NUMBER()`, `ERROR_MESSAGE()`, `ERROR_LINE()`, `ERROR_SEVERITY()`, `ERROR_STATE()`, `ERROR_PROCEDURE()`, and `@@ERROR`. Wired error state tracking into `VMFrame` (`lastErrorMsg`, `lastErrorLine`) and `OpCallBuiltin` dispatcher inside `TRY...CATCH` blocks.
  - **Group B (String Manipulation & Search)**: Added `PATINDEX(pattern, string)` supporting T-SQL wildcards (`%`, `_`, `[a-z]`), `UNICODE()`, `NCHAR()`, `SOUNDEX()` and `DIFFERENCE()` phonetics, `TRANSLATE()`, and `FORMAT()` supporting date layouts and number/currency patterns.
  - **Group C (Math & Trigonometric Functions)**: Added `EXP()`, `LOG()`, `LOG10()`, `SIN()`, `COS()`, `TAN()`, `COT()`, `ASIN()`, `ACOS()`, `ATAN()`, `ATN2()`, `DEGREES()`, `RADIANS()`, and `RAND([seed])`.
  - **Group D (Metadata & Schema Introspection)**: Added `OBJECT_ID()`, `OBJECT_NAME()`, `COL_LENGTH()`, `SCHEMA_NAME()`, `SCHEMA_ID()`, `DB_NAME()`, `DB_ID()`, `APP_NAME()`, and `IDENT_CURRENT()`.
  - **Group E (Cryptography & Checksums)**: Added `HASHBYTES(algo, string)` (MD5, SHA, SHA1, SHA2_256, SHA2_512), `CHECKSUM()`, and `BINARY_CHECKSUM()`.
  - **Group F (Date Constructors)**: Added `DATEFROMPARTS()`, `TIMEFROMPARTS()`, and `DATETIMEFROMPARTS()`.
  - Added unit test suite `TestBuiltin_GroupsA_Through_F` in `pkg/proc/builtins_test.go` and regression tests `TestRegression_TryCatchErrorBuiltins` and `TestRegression_TSQLBuiltinsGroupsAThroughF` in `pkg/proc/runtime/regression_test.go`.
- **VDBE Instruction 16-Byte Compaction & AuxData Pool (`pkg/proc`, `pkg/proc/runtime`)**:
  - Re-architected `proc.Instruction` into a dense 16-byte fixed-size record (`Op OpCode, Flags uint8, Aux uint16, A int32, B int32, C int32`) with zero struct padding, fitting exactly 4 instructions per 64-byte CPU L1 Data Cache line.
  - Eliminated the `Data any` 16-byte interface header from `Instruction`, externalizing variable-sized metadata and complex structures (`DMLData`, `CallData`, `SelectIntoData`, `RelQueryData`, `IndexScanData`, `ResultRowData`) to `Program.AuxData []any` indexed via `inst.Aux` (`uint16`).
  - Added dedicated bitflag representations in `inst.Flags` for `OpLike` and `OpBetween` negation, eliminating heap allocations for between expressions during compilation.
  - Added strict unit test `TestInstruction_Size16Bytes` in `pkg/proc/compiler_test.go` enforcing `unsafe.Sizeof(proc.Instruction{}) == 16`.
- **Bounded LRU PlanCache with Memory Limits (`pkg/proc/runtime`, Golden Rule 29)**:
  - Replaced unbounded `PlanCache` map storage with a bounded Least Recently Used (`container/list`) cache (`DefaultPlanCacheCapacity = 1024`).
  - Added deterministic LRU eviction when cache capacity is exceeded, thread-safe cache lookup promotion, `Len()`, `Capacity()`, and `Clear()` methods.
  - Normalized procedure and function lookup keys to lowercase for robust case-insensitive `GetByName` resolution.
  - Added unit test suite `TestPlanCache_BoundedLRU` and `TestPlanCache_PutAndGetByName` in `pkg/proc/runtime/plancache_test.go`.
- **Expanded T-SQL Built-in Scalar Functions (`pkg/proc`)**:
  - Implemented `CONCAT_WS(separator, expr1, expr2, ...)` supporting arbitrary string formatting with NULL-skipping semantics.
  - Implemented `QUOTENAME(string [, quote_char])` supporting standard delimiters (`[]`, `'`, `"`, `()`, `{}`, `<>`) and escape doubling.
  - Implemented `ASCII(character_expression)` and `CHAR(integer_expression)` for byte/ASCII character conversions.
  - Implemented `NULLIF(expression1, expression2)` returning NULL on equality or expression1 otherwise.
  - Implemented `ISNUMERIC(expression)` checking valid numeric data types or string parseability without NaN/Inf leaks.
  - Implemented `EOMONTH(start_date [, month_to_add])` calculating the end of month date.
  - Implemented `ISDATE(expression)` validating date string format conformance.
  - Registered all new functions in `IsBuiltinFunction` and added comprehensive unit tests in `pkg/proc/builtins_test.go`.
- **Table-Driven T-SQL Expressions & Built-in Functions Test Suite (`pkg/proc/runtime`)**:
  - Implemented 116+ granular table-driven test cases covering arithmetic precedence, math builtins, string manipulation, date/time functions, boolean logic truth tables, predicates (LIKE, BETWEEN, IN, IS NULL), and searched CASE WHEN expressions.
- **Standard ANSI SQL:2008 & T-SQL Paging and TOP Syntax (`pkg/proc`, `pkg/planner`, `pkg/api`)**:
  - Implemented standard `OFFSET <n> [ROW|ROWS] FETCH {FIRST|NEXT} <m> [ROW|ROWS] ONLY` clauses in the native Pratt parser (`parseGeneralSelect`) and CBO clause splitter (`splitSelectClauses`).
  - Added support for `SELECT [DISTINCT] TOP (<n>|<n>) [PERCENT] [WITH TIES]` clause in the native parser and query planner, setting `LimitCount` and `TopPercent` / `TopWithTies` metadata.
  - Corrected `GeneralSelectStmt.String()` to emit `OFFSET` when `s.Offset > 0`.
  - Added comprehensive unit and execution test suites in `pkg/proc/sqlparser_test.go`, `pkg/planner/planner_test.go`, and `pkg/api/sqlexec_test.go`.
- **AdventureWorks, WideWorldImporters & nopCommerce E2E Stored Procedure Test Suite (`pkg/proc/runtime`)**:
  - Implemented end-to-end stored procedure test suites modeled directly after Microsoft AdventureWorks (`dbo.uspProcessWorkOrder`, `dbo.uspUpdateEmployeeRateAndVacation`), WideWorldImporters (`dbo.uspProcessCustomerInvoice`), and nopCommerce (`dbo.RewardPointsHistory_Add`, `dbo.CustomerBatchMaintenance`, `dbo.uspApplyPromotionalDiscounts`).
  - Validated complex control flow including nested transactions, `SAVE TRANSACTION` savepoints with partial rollbacks, `TRY...CATCH`, `THROW`, audit error logging tables, VIP tier promotions, customer invoicing ledger updates, and cursor traversal with `@@FETCH_STATUS`.

### Fixed
- **Post-Rollback & Post-Commit Transaction Continuity in VDBE Procedures (`pkg/proc/runtime`)**:
  - Bound active `storage.Session` to `VMFrame` and enabled seamless new transaction initialization following in-procedure `ROLLBACK TRANSACTION` or `COMMIT TRANSACTION`.
  - Fixed `ErrTxClosed` failure when running audit and error logging statements inside `BEGIN CATCH` blocks after rolling back failed business transactions.
- **VDBE Function Call Register Allocation & Projection Clobbering (`pkg/proc`, `pkg/api`)**:
  - Fixed register clobbering in multi-expression scalar queries where argument deallocation in `FunctionCallExpr` caused the register allocator to decrement `regCounter` below the returned result register, overwriting preceding projection registers in subsequent expressions.
  - Preserved catalog table introspection in table-less `OBJECT_ID` and `OBJECT_NAME` scalar queries by routing catalog introspection calls to `execScalarSelect`.

### Documentation
- **Multi-Protocol Emulation Boundaries & Scope Architecture Guidelines (`doc/PROTOCOL_EMULATION_GUIDELINES.md`)**:
  - Established Four-Layer Protocol Emulation Boundary Model (Wire Framing 100%, Virtual Catalog Handshake 90%, Common DML 80%, Dialect Quirks 10~20%).
  - Defined explicit scope stop criteria for PostgreSQL emulation, future MySQL (:3306) protocol implementation boundaries, and permanent single-kernel T-SQL VDBE procedural invariance.
- **Canonical FSF GPL v3 LICENSE Alignment & Commercial Dual Licensing (`LICENSE`, `README.md`)**:
  - Restored `LICENSE` file to canonical verbatim GNU General Public License v3 text for seamless automated open-source compliance scanning.
  - Relocated and structured commercial dual-licensing and proprietary embedding terms in `README.md`.

---

## [0.15.0]

### Added
- **VDBE Local Variable Slot Indexing (`pkg/proc`, `pkg/proc/runtime`)**:
  - Implemented compile-time variable slot allocation (`Program.VarSlots map[string]int32`, `Program.SlotCount int32`) and runtime $O(1)$ direct slice indexing (`VMFrame.slots[inst.B]`) for procedure parameters and local variables (`DECLARE`, `SET`, `SELECT INTO`), eliminating string hashing, case folding, and map lookup overhead on hot execution paths.
  - Added dedicated unit tests for variable slot indexing and expression evaluation in `pkg/proc/runtime/vm_slots_test.go`.
- **VDBE Integer Opcode Specialization & Dedicated Logical Operators (`pkg/proc`, `pkg/proc/runtime`)**:
  - Implemented specialized integer arithmetic and comparison opcodes (`OpAddInt`, `OpSubInt`, `OpMulInt`, `OpEqInt`, `OpNeqInt`, `OpLtInt`, `OpLteInt`, `OpGtInt`, `OpGteInt`) with compile-time type detection and direct ALU execution, bypassing `storage.Value` float conversion and `math.Trunc` truncation on hot execution paths.
  - Separated `OpAnd` and `OpOr` as first-class virtual machine opcodes, eliminating runtime dynamic interface string comparisons (`inst.Data == "AND"` / `"OR"`) within `OpEq`.
  - Added unit test suite verifying opcode emission, logical branching, and execution accuracy in `pkg/proc/runtime/vm_specialization_test.go`.
- **VDBE Fused Compare-and-Branch & Peephole Jump Optimization (`pkg/proc`, `pkg/proc/runtime`)**:
  - Implemented fused compare-and-branch instructions (`OpJumpIfEq`, `OpJumpIfNeq`, `OpJumpIfLt`, `OpJumpIfLte`, `OpJumpIfGt`, `OpJumpIfGte`, and integer variants `OpJumpIfEqInt` ~ `OpJumpIfGteInt`), fusing relational comparison and conditional branching into a single instruction and eliminating intermediate boolean register materialization and VM dispatch cycles.
  - Implemented compiler peephole optimizer (`optimizePeephole`) featuring fall-through jump elimination (converting `OpJump i+1` into `OpNop`) and recursive jump threading/chaining (eliminating intermediate trampoline jumps).
  - Added comprehensive verification test suite in `pkg/proc/runtime/vm_jump_opt_test.go`.

### Refactored
- **VDBE Opcode Dispatcher Modularization (`pkg/proc/runtime/vm.go`, `pkg/proc/runtime/vm_dispatcher.go`)**:
  - Modularized the 800+ line monolithic `switch inst.Op` in `VMFrame.Execute` into domain-specific execution helpers (`execFlowOp`, `execComputeOp`, `execTxOp`, `execStorageOp`, `execCursorAndCallOp`, `execRelOp`).
  - Unified repetitive error catch-stack unwinding logic into a centralized `handleCatch` helper, reducing cyclomatic complexity and improving profiling trace clarity.

### Performance & Benchmarks
- **Post-Optimization TPC-C & VDBE Benchmark Verification (`cmd/tpcc`, `cmd/tpcc_vdbe`, `doc/BENCHMARK.md`)**:
  - Re-benchmarked native Go driver and T-SQL VDBE bytecode engine on standard TPC-C workload (2 Warehouses, 20 Workers, 15s) across variable slot indexing, opcode specialization, and fused compare-and-branch optimizations.
  - **Go Native API Driver**: Achieved **28,299.66 TPS (763,495.89 tpmC)** across 425,002 transactions (New-Order Avg 0.82ms / P95 3.01ms, Payment Avg 0.53ms / P95 2.12ms).
  - **T-SQL VDBE Bytecode VM Engine**: Peak throughput reached **20,478.6 TPS (554,835 tpmC)**, overall sustained **17,563.10 TPS (471,372.34 tpmC)** (63% native parity) with significant tail latency reductions (Payment P95 dropped to 2.96ms, P99 to 11.64ms).

---

## [0.14.0]

### Fixed
- **Deterministic Object ID Assignment & Table Row Count Calculation in Catalog (`pkg/storage/wiredtiger`, `pkg/api`)**:
  - Sorted `ListTables` deterministically by name in WiredTiger storage engine (`wt_engine.go`), eliminating random Go map iteration order that caused table and column object IDs (`objectId`) to shuffle between queries in DbGate.
  - Added deterministic `getSortedTables()`, `getSortedViews()`, and `getSortedProcedures()` in `pkg/api/sqlexec_pgcatalog.go` ensuring 100% stable object IDs across all MSSQL virtual catalog handlers.
  - Added accurate dynamic row counting in `execDbGateMSSQLRowCountSelect` by scanning table rows via engine cursors.
  - Implemented scalar SQL expressions `OBJECT_ID(...)`, `OBJECT_NAME(...)`, `SCHEMA_NAME(...)`, `SCHEMA_ID(...)`, and `QUOTENAME(...)` in `pkg/api/sqlexec_select.go` for DbGate single-table introspection queries.
- **DbGate SQL Server GUI Introspection & Column Listing Parity (`pkg/api`, `pkg/wire/tds`)**:
  - Implemented specialized virtual catalog join handlers for DbGate's full MSSQL introspection pipeline:
    - Object modifications query (`sys.objects` + `sys.schemas` returning `objectId`, `modifyDate`, `type`, `pureName`, `schemaName`).
    - Tables catalog query (`sys.tables` + `sys.schemas` returning `pureName`, `schemaName`, `objectId`, `createDate`, `modifyDate`, `objectComment`).
    - Views query (`sys.objects` + `sys.schemas` WHERE `type = 'V'`).
    - Columns catalog query (`sys.columns` + `sys.types` returning `columnName`, `dataType`, `objectId`, `isIdentity`, `maxLength`, `precision`, `scale`, `isNullable`, `charMaxLength`, `columnId`, etc.).
    - Primary keys (`sys.indexes` + `sys.index_columns` + `sys.columns` WHERE `is_primary_key = 1`).
    - Non-primary indexes and index columns (`sys.indexes`, `sys.index_columns`).
    - Foreign keys (`INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS` joined with `TABLE_CONSTRAINTS`).
    - Table row count (`sys.tables` + `sys.indexes` + `sys.partitions`).
    - Extended property column comments (`MS_Description`).
    - Stored procedures / programmables and SQL code definitions (`sys.syscomments`).
  - Generalized `inferColumnInfos` in TDS server (`pkg/wire/tds/server.go`) to infer `TypeDateTimeN` for column names ending in `date` or `time` (including camelCase `modifyDate`, `createDate`), resolving driver error `r.toISOString is not a function` in DbGate.
- **DbGate & GUI Tool SQL Server Version & Edition Detection (`pkg/wire/tds`, `pkg/proc`, `pkg/api`)**:
  - Implemented multi-column version & edition detection interceptor (`matchVersionQuery`) in TDS server handling queries combining `@@VERSION`, `SERVERPROPERTY('productversion')`, `SERVERPROPERTY('EngineEdition')`, and `CASE ... END AS versionText`.
  - Added support for `SERVERPROPERTY` in T-SQL procedural built-in function dispatcher (`pkg/proc/builtins.go`) and scalar SQL executor (`pkg/api/sqlexec_select.go`).
  - Fixed TDS metadata serialization for integer columns to use `TypeIntN` (`0x26`), preventing driver error `Invalid type 0` on scalar expressions.
- **Beekeeper Studio & GUI Tool Column Introspection & Pagination Parity (`pkg/wire/tds`, `pkg/proc`, `pkg/api`, `pkg/planner`)**:
  - Implemented double-quoted identifier (`"col"`) parsing in native Pratt parser lexer (`pkg/proc/lexer.go`), preventing syntax errors on ANSI/MSSQL quoted column aliases (`AS "column_name"`).
  - Added specialized virtual catalog interceptor for Beekeeper Studio's SQL Server column query (`FROM INFORMATION_SCHEMA.COLUMNS ic LEFT JOIN sys.computed_columns sc ON ...`), extracting target tables/schemas and formatting data types to native MSSQL representations (`int`, `varchar(255)`, `decimal(p,s)`, `bit`, `datetime`).
  - Added MSSQL pagination normalization (`normalizeMSSQLPagination`) converting `OFFSET <O> ROWS FETCH NEXT <N> ROWS ONLY` and `SELECT TOP <N>` queries to canonical `LIMIT <N> OFFSET <O>`.
  - Added `OffsetCount` support to `pkg/planner.QueryInfo` and `pkg/planner.PlanNode`, hooked into `pkg/execution.NewLimitIterator(child, limit, offset)`.
  - Added bracket and quote stripping in `ColumnIndexMap.Index` (`pkg/execution/iterator.go`) and `cleanIdent` (`pkg/planner/planner.go`) for zero-friction resolution of `[dbo].[table]` and `[column]` references.
  - Added virtual catalog stubs for `sys.computed_columns`, `sys.triggers`, `sys.foreign_keys`, and `sys.foreign_key_columns` in `pkg/api/sqlexec_dbcc.go`.

### Added
- **Technical Documentation & Architecture Decision Records (`doc/`, `notes/`)**:
  - Authored `doc/CLIENT_TOOL_INTEGRATION.md`: Comprehensive connection and integration guide for Beekeeper Studio, DBeaver, Azure Data Studio, SSMS, `psql`, `sqlcmd`, and programming language drivers (Go, Python, Node.js, C#, Rust, Java).
  - Authored `doc/TRANSACTION_CONCURRENCY.md`: Complete specification for non-blocking snapshot reads, In-Memory OCC vs WiredTiger MVCC concurrency models, private workspace isolation, and VDBE transaction coupling (`exec.RunInTx`).
  - Authored `doc/RECORD_BINARY_FORMAT.md`: Schema-aware compact binary tuple storage format specification.
  - Added ADR-019 (Schema-Aware Compact Binary Tuple Storage Codec), ADR-020 (Multi-Protocol Virtual Catalog Interceptor Strategy), and ADR-021 (Open Source Licensing Strategy: GPL-3.0-or-later) to `doc/ADR.md`.
  - Documented Go map iteration non-determinism landmine and mitigation strategies in `notes/PITFALLS_AND_TRICKS.md` (Section 10) and `doc/CLIENT_TOOL_INTEGRATION.md`.
  - Adopted GNU General Public License v3 (`LICENSE`).
  - Updated `doc/README.md` indexing map and section listings with all new documents.

---

## [0.13.0]

### Added
- **PostgreSQL Modern Driver Compatibility: `pgx/v5` & Pipelining (`pkg/wire`)**:
  - Integrated official `github.com/jackc/pgx/v5` modern Go driver test suite (`pkg/wire/pgx_integration_test.go`) covering direct connection, stdlib `sql.Open("pgx", ...)`, CRUD, parameters, transactions, pipelined batch execution, and data types.
  - Implemented binary parameter decoding (`decodeBinaryParam`) supporting `int4`, `int8`, `int2`, `bool`, `float4`, `float8`, `timestamp`, `timestamptz`, `date`, and string types.
  - Extended binary column value serialization (`formatColumnValue`) supporting `BOOL` (1 byte), `FLOAT4` (4 bytes IEEE-754), `FLOAT8` (8 bytes IEEE-754), `TIMESTAMP`/`TIMESTAMPTZ` (microseconds since PG epoch), `DATE` (days since PG epoch), and text/varchar.
  - Enhanced `writeSchemaDescription` with `buildPreviewSQL` to accurately infer and describe column OIDs for non-table scalar expressions (`SELECT 40 + 2`, `SELECT $1 + $2`, aggregates like `COUNT(*)`) in response to Extended Query `Describe` ('D').
  - Implemented SQL comment stripping (`stripSQLComments`) and proper `EmptyQueryResponse` ('I') handling for comment-only queries (`-- ping`) sent by driver health-check probes.
  - Verified 100% pass across direct `pgx.Connect`, parameterized queries, table CRUD, atomic transaction commit/rollback, `pgx.Batch` pipelining, and `database/sql` stdlib.
- **Real-Time Wire Protocol Developer Query Tracing (`-dev` CLI flag for PGWire & TDS)**:
  - Added `-dev` command-line flag enabling real-time query logging with parameters, query execution timing, and error traces across both PostgreSQL Wire Protocol and Microsoft SQL Server TDS protocol.
- **Microsoft SQL Server Virtual Metadata Catalog Enhancements (`sys.databases`, `information_schema.parameters`, `NOT LIKE`)**:
  - Supported `sys.databases` returning `master` and active database catalogs, and default `dbo` schema in `information_schema.schemata`.
  - Supported `NOT LIKE` pattern matching in catalog expression evaluator.
  - Supported `information_schema.parameters` and procedural metadata introspection for client tools.

### Fixed
- **HeidiSQL PostgreSQL Virtual Catalog JOIN & Metadata Queries (`pkg/wire`, `pkg/api`)**:
  - Intercepted HeidiSQL foreign key catalog query and escaped string literals properly in `SplitProjections`.
  - Corrected `RowDescription` `DataTypeSize` and `atttypmod` for PostgreSQL table column metadata queries.
  - Supported HeidiSQL virtual catalog queries joining `pg_class`, `pg_namespace`, `pg_attribute`, and `pg_constraint`.
- **Beekeeper Studio PostgreSQL `pg_class` Table List Catalog Query (`pkg/api`)**:
  - Supported Beekeeper Studio's `pg_class` query filtering with `relkind` and namespace joins.
- **Sample Data Seeding Transaction Isolation per Table (`cmd/server`)**:
  - Isolated sample data seeding transactions per table to prevent long-running OCC workspaces and commit lock contention during startup.

---

## [0.12.0]

### Added
- **PostgreSQL Official Client Driver Compatibility (`pkg/wire`)**:
  - Integrated official client driver verification suite (`pkg/wire/driver_integration_test.go`) utilizing `github.com/lib/pq v1.12.3`.
  - Added parameter count inference in `handleParse` for client queries utilizing `$1`, `$2` parameter placeholders when client passes 0 explicit parameter types.
  - Implemented projected column resolution in `writeSchemaDescription` to return accurate `RowDescription` column counts matching queries instead of raw table definitions.
  - Added support for PostgreSQL Extended Query `Bind` result format codes with `WriteDataRowBytes` and big-endian binary encoding for `int4`, `int8`, `int2`, `bytea`, and null value handling (`0xFFFFFFFF`).
  - Added flexible interactive transaction syntax support (`BEGIN READ WRITE`, `BEGIN ISOLATION LEVEL ...`, `COMMIT WORK`) and standard PostgreSQL `CommandComplete` tag formatting (`BEGIN`, `COMMIT`, `ROLLBACK`, `SET`, `CREATE <type>`, `DROP <type>`).
  - Verified 100% pass across official driver integration tests: connection ping, query evaluation, CRUD, prepared statements, parameterized queries, session SET options, transactions (commit/rollback), and sys metadata catalog views.
- **Microsoft SQL Server Official Client Driver Compatibility (`pkg/wire/tds`)**:
  - Integrated official client driver verification suite (`pkg/wire/tds/driver_integration_test.go`) utilizing `github.com/microsoft/go-mssqldb v1.11.2`.
  - Added full support for `PacketTransactionManager` (0x0E) handling `tmBeginXact` (5), `tmCommitXact` (7), and `tmRollbackXact` (8) with transaction descriptors.
  - Implemented `ENVCHANGE` token builders for SQL collation (`BuildEnvChangeCollation`, Type 7), transaction initiation (`BuildEnvChangeBeginTransaction`, Type 8), commit (`BuildEnvChangeCommitTransaction`, Type 9), and rollback (`BuildEnvChangeRollbackTransaction`, Type 10).
  - Added connection session transaction state tracking (`inTx`, `txDescriptor`, `DoneInXact` status flag propagation) across SQL batches, RPC requests, and transaction manager packets.
  - Verified 100% pass across official driver integration tests: connection ping, query evaluation, CRUD, prepared statements via `sp_prepare`, parameterized filters, session SET options, transactions (commit/rollback), and sys metadata catalog views.
- **CBO Table Statistics Disk Persistence (`pkg/catalog`, `pkg/storage`, `cmd/server`)**:
  - Implemented `SetPersistenceDir`, `PersistenceDir`, `SaveTableStatsToFile`, and `LoadTableStatsFromFile` on `catalog.Catalog`.
  - Added atomic disk persistence (`*.tmp` write followed by atomic rename, Golden Rule 1 & 5) of `cbo_stats.json` triggered automatically on `SetTableStats` and `InvalidateTableStats`.
  - Added `MarshalJSON` and `UnmarshalJSON` support to `storage.Decimal` for lossless JSON serialization of decimal histogram buckets.
  - Connected `cat.SetPersistenceDir(cfg.Server.DataDir)` in standalone server startup (`cmd/server/main.go`), ensuring CBO stats and optimized execution plans survive server restarts.
  - Added unit test `TestCatalog_TableStatsPersistence` in `pkg/catalog/catalog_test.go`.
- **In-Process Go Native Scalar UDF Registration SPI (`pkg/proc`, `pkg/api`)**:
  - Implemented thread-safe `ScalarFunction` callback type with `RegisterScalarFunction`, `UnregisterScalarFunction`, and `HasScalarFunction` using `sync.Map` in `pkg/proc/builtins.go`.
  - Added zero-overhead fast-path lookup in `EvalBuiltin` executing registered custom Go callbacks ahead of standard built-in dispatch at sub-microsecond in-process speed.
  - Added unit test `TestCustomScalarFunctionSPI` in `pkg/proc/builtins_test.go` and integration test `TestCustomScalarFunction_SQLExec` in `pkg/api/sqlexec_test.go`.
- **System Diagnostic Health Verification Engine Probes (`cmd/server`)**:
  - Integrated dynamic probe 5.4 into `--doctor` CLI utility in `cmd/server/doctor.go` verifying dynamic Go scalar UDF registration, invocation, and unregistration lifecycle.
  - Validated all 17 diagnostic probes across host environment, filesystem write/read integrity, network port availability, storage engine MVCC/hazard pointers, dual-parser execution, and cryptographic subsystems.
- **VDBE Physical Execution PlanNode Caching (`pkg/planner`, `pkg/proc`, `pkg/proc/runtime`)**:
  - Added `PlanNode.Clone()` deep copy method in `pkg/planner/planner.go` to clone Volcano physical execution plan trees including projections, group keys, window definitions, and child plan nodes.
  - Implemented immutable `CachedPlan` caching on `RelQueryData` within `VMFrame.execRelQuery`, bypassing full CBO join planning and stats estimation on repeated invariant queries without where clauses.
  - Added thread-safe `mu sync.RWMutex` with `GetCachedQueryInfo`, `SetCachedQueryInfo`, `GetCachedPlan`, and `SetCachedPlan` accessors on `proc.RelQueryData` to ensure race-free concurrent execution across parallel worker threads.
- **Wire Protocol Zero-Allocation Buffer Pooling (`pkg/wire`, `pkg/wire/tds`)**:
  - Integrated `packetBufferPool = sync.Pool` with 4KB preallocated buffers for PostgreSQL Wire Protocol (`WriteDataRow`), eliminating per-row heap allocations during high-concurrency result streaming.
  - Added `tdsRowBufferPool = sync.Pool` for TDS token row serialization (`BuildRow`), reusing `bytes.Buffer` instances and passing buffer pointers directly to `binary.Write`.
- **Storage Value Integer Fast-Path Optimization (`pkg/storage`)**:
  - Implemented non-null integer fast-paths in `storage.Value.Equal` and `storage.Value.Compare` (`TypeInt`), bypassing type dispatch, null checks, and decimal conversions for high-frequency point lookups and SkipList index operations.
  - Optimized `storage.Value.String()` to use `strconv.FormatInt` and `strconv.FormatBool`, replacing `fmt.Sprintf` reflection overhead on hot formatting paths.

### Changed
- **Monolithic File Decomposition & Dead Code Elimination (`pkg/api`, `pkg/storage/memengine`)**:
  - Pruned 540 lines of dead legacy join execution code (`execJoin`, `execNestedLoopJoin`, `execHashJoin`) from `pkg/api/sqlexec_dml.go`.
  - Decomposed `pkg/api/sqlexec_dml.go` into domain files: `sqlexec_select.go` (SELECT & aggregation), `sqlexec_pivot.go` (PIVOT, UNPIVOT, FOR JSON), `sqlexec_explain.go` (EXPLAIN, ANALYZE, CTE), and `sqlexec_eval.go` (expression & condition evaluation), keeping pure INSERT/UPDATE/DELETE in `sqlexec_dml.go`.
  - Decomposed `pkg/storage/memengine/memengine.go` into `table_data.go` (striped locks & shards), `persistence.go` (WAL/AOF compaction & recovery), `ddl.go` (table/index DDL), `tx.go` (transactions & workspace mutations), and `memengine.go` (lifecycle & options).
  - Decomposed `pkg/api/server.go` into dedicated handler modules: `handlers_auth.go`, `handlers_admin.go`, `handlers_tables.go`, `handlers_records.go`, `handlers_import_export.go`, and `handlers_procs.go`.

### Documentation
- **Developer & AI Handover Context Knowledge Base (`notes/`)**:
  - Established persistent `notes/` directory as an engineering context handover mechanism across development machines and AI pair programming sessions.
  - Authored `notes/README.md`, `notes/ENGINEERING_CONTEXT.md` (core architectural invariants, spec-out rationales, collaboration principles), `notes/PITFALLS_AND_TRICKS.md` (circular imports, build tags, sandbox networking, Decimal JSON roundtrips, transaction coupling), and `notes/REFACTORING_AND_ROADMAP.md` (decomposition history, next week priorities, CBO status).
  - Linked `notes/` reference in `GEMINI.md` and `README.md`.
- **Architecture & Operational Documentation Audit (`README.md`, `doc/*`)**:
  - Added ADR-017 (1-Writer Multi-Reader High-Availability Replication Topology) and ADR-018 (Dual-Track UDF Architecture: Native Go In-Process SPI & WebAssembly Sandbox) in `doc/ADR.md`.
  - Added Section 12 for WAL replication endpoints (`/api/replication/stream`, `/api/replication/status`, `/api/replication/ack`, `/api/replication/promote`, `/api/replication/demote`) in `doc/API_REFERENCE.md`.
  - Added Section 7 for 1-Writer Multi-Reader High-Availability Cluster Operations and Failover Runbook in `doc/OPERATIONS.md`.
  - Updated root `README.md` and `doc/README.md` with replication CLI flags (`--replica-of`, `--node-id`, `--ephemeral`), endpoint reference tables, and links to `TODO.md` and `PERCENTILE_AND_QUANTILE_ANALYTICS.md`.
  - Updated system architecture diagram and module directory layout in `doc/ARCHITECTURE.md`.
- **Web SDK & HTML Embedding Guide (`doc/WEB_SDK.md`)**:
  - Renamed `doc/SDK.md` to `doc/WEB_SDK.md` to explicitly represent the web frontend and browser embedding capabilities.
  - Added Section 9 (Web & HTML Embedding Recipes) providing zero-dependency single-file HTML templates (`fetch()`), real-time SSE live synchronization, declarative HTMX embedding, and T-SQL `FOR JSON PATH` client interpolation.
  - Synchronized documentation references across `README.md` and `doc/README.md`.

---

## [0.11.0]

### Added
- **1-Writer Multi-Reader WAL Replication & High-Availability Server Integration (`cmd/server`, `pkg/replication`, `pkg/api`, `pkg/config`)**:
  - Integrated the availability group replication manager into the LionSQL standalone server, supporting 1-writer primary nodes and multi-reader secondary replicas.
  - Added CLI flags `--replica-of <primary_url>` and `--node-id <id>` with corresponding configuration hierarchy mappings in `lionsql.json`.
  - Exposed HTTP endpoints `/api/replication/stream`, `/api/replication/status`, `/api/replication/ack`, `/api/replication/promote`, and `/api/replication/demote`.
  - Enforced strict read-only invariants across all wire protocols and REST endpoints for secondary replicas (`IsReadOnly()` write rejection with `ErrReadOnlyReplica`), guarding DDL (`CREATE/ALTER/DROP/TRUNCATE`), DML (`INSERT/UPDATE/DELETE/MERGE`), and interactive `BEGIN TRANSACTION READ WRITE`.
  - Implemented seamless failover promotion (`Promote()`), instantly transitioning a replica into an active primary node capable of accepting writes.
  - Added end-to-end integration test `TestReplication_ServerIntegration` in `pkg/api/replication_integration_test.go`.

- **VDBE `OpRelQuery` Plan Caching & 0-Allocation Row Streaming (`pkg/proc`, `pkg/proc/runtime`, `pkg/execution`)**:
  - Implemented AST and QueryInfo plan caching directly in `proc.RelQueryData`, allowing repeated relational queries in stored procedures and loops to execute with zero SQL re-parsing overhead.
  - Added `QueryInfo.Clone()` to safely reuse cached relational parse structures while permitting parameter and session variable binding.
  - Added `Tuple.ToRow(schema)` to convert Volcano execution tuples into ordered `storage.Row` structs without allocating intermediate `map[string]any` per row.
  - Added unit test coverage in `pkg/proc/runtime/runtime_test.go` and `pkg/execution/execution_test.go`.

- **Parameterized Plan Cache & SQL Prepared Statements (`pkg/api`, `pkg/proc/runtime`)**:
  - Implemented SQL-level prepared statements: `PREPARE <name> [ (types) ] AS <stmt>`, `PREPARE <name> FROM '<stmt>'` (with string literals or session variables), `EXECUTE <name> [ (args) | USING @vars ]`, and `DEALLOCATE [PREPARE] <name>` / `DEALLOCATE ALL`.
  - Added quote- and comment-aware placeholder normalizer rewriting `$1, $2, ...` and `?` positional parameters into internal VDBE bytecode variables (`@p1, @p2, ...`) without corrupting string literals or comments.
  - Integrated `runtime.PlanCache` in `execVBDEDML` (for recurring `INSERT`, `UPDATE`, `DELETE`) and `execVDBESelect` (for recurring queries), avoiding repeated lexing, parsing, and compilation overhead.
  - Hardened `VMFrame.bindWhereVariables` against substring prefix collisions (e.g. `@p1` vs `@p10`) using word boundary regular expressions and descending key length sorting.
  - Added comprehensive test suite in `pkg/api/prepare_test.go` verifying basic SELECTs, MySQL-style FROM/USING, batch INSERT/UPDATE/DELETE, explicit transaction rollback atomicity, and plan cache throughput.

- **In-Memory Ephemeral Engine Mode & Zero-Disk Persistence (`cmd/server`, `pkg/config`, `pkg/storage/memengine`, `pkg/api`)**:
  - Added `--ephemeral` CLI flag and `ephemeral` configuration property for `memory` engine backend, completely disabling WAL/AOF logging, RDB snapshot generation, and disk directory creation for pure in-memory operation.
  - Implemented `IsEphemeral()` and `PersistenceSummary()` on `memengine.Engine`.
  - Added `DBCC PERSISTENCE` / `DBCC ENGINE` console commands and exposed active persistence state in `DBCC USEROPTIONS`.
  - Added unit test `TestEphemeralEngine` in `pkg/storage/memengine/ephemeral_test.go` and `TestDBCC_PersistenceAndEphemeral` in `pkg/api/ephemeral_test.go`.

- **CBO Compound Selectivity & Dynamic Live Row Count Estimation (`pkg/planner`, `pkg/catalog`, `pkg/storage/memengine`, `pkg/api`)**:
  - Implemented `EstimateCompoundSelectivity` on `TableStats` to accurately compute combined selectivity for multi-predicate filter clauses using statistical independence instead of falling back to static constants.
  - Added $O(1)$ `TableRowCount()` method to `memengine.Engine` across concurrent shards.
  - Enhanced `catalog.GetTableStats` with dynamic live row count fallback, giving the query planner instant cardinality awareness even before explicit `ANALYZE` commands are executed.
  - Added unit tests in `pkg/planner/stats_test.go`, `pkg/catalog/catalog_test.go`, and end-to-end integration test in `pkg/api/cbo_test.go`.

- **PostgreSQL-Style `INSERT ... ON CONFLICT` & CockroachDB-Style `UPSERT INTO` Syntax (`pkg/api`)**:
  - Implemented PostgreSQL-compatible `INSERT ... ON CONFLICT (target_col) DO NOTHING` and `ON CONFLICT DO NOTHING` to gracefully ignore unique/primary key conflicts.
  - Implemented `INSERT ... ON CONFLICT (target_col) DO UPDATE SET col = expr, ... [WHERE condition]` with full support for proposed values via `EXCLUDED.<col>` (and `excluded.<col>`) and existing row values via `<table_name>.<col>` or `<col>`.
  - Implemented `ON CONFLICT ON CONSTRAINT <constraint_name> DO ...` constraint target support.
  - Added native CockroachDB-style `UPSERT INTO <table_name> (...) VALUES (...)` shorthand updating non-primary-key columns on key conflicts.
  - Enhanced value tuple parser (`parseValueTuples`) with depth and quote tracking to reliably handle nested parentheses and functions in value expressions.
  - Added comprehensive test suite in `pkg/api/upsert_test.go` covering `DO NOTHING`, `DO UPDATE`, conditional updates via `WHERE`, multi-row batch inserts with intra-batch conflicts, and `UPSERT` shorthand.

- **In-Engine Exact & Streaming Quantile Analytics: `PERCENTILE_CONT`, `PERCENTILE_DISC`, `APPROX_PERCENTILE`, and `MEDIAN` (`pkg/execution`, `pkg/proc`, `pkg/planner`, `pkg/api`)**:
  - Implemented continuous percentile `PERCENTILE_CONT` (linear interpolation) and discrete percentile `PERCENTILE_DISC` supporting both ANSI `WITHIN GROUP (ORDER BY ...)` and 2-argument standard syntax.
  - Introduced streaming approximate percentile operator `APPROX_PERCENTILE` powered by the T-Digest clustering algorithm, achieving high tail accuracy (P90, P95, P99) with bounded $O(1)$ memory ($< 4\text{ KB}$ per group).
  - Added dedicated `MEDIAN` aggregate and window operator.
  - Seamlessly enabled all quantile functions in global aggregations, `GROUP BY` grouping dimensions, and `OVER (PARTITION BY ...)` analytic window partitions.
  - Added unit test suites in `pkg/execution/tdigest_test.go` and full E2E SQL tests in `pkg/api/percentile_test.go`.

- **In-Engine Log Extraction & Filtering Operators: `GREP` / `REGEXP_LIKE` and `AWK` (`pkg/proc`, `pkg/execution`, `pkg/api`)**:
  - Implemented high-performance scalar functions `GREP(text, pattern)` (with ANSI SQL standard alias `REGEXP_LIKE`) and `AWK(text, [delim], field_index)` (also aliased as `SPLIT_PART`).
  - Enables zero-copy, in-database text filtering and structured field extraction from unstructured raw logs and delimited strings without dumping to external terminal pipelines.
  - Supported `GREP`, `REGEXP_LIKE`, and `AWK` seamlessly in `SELECT` projections, `WHERE` predicate filters, and `GROUP BY` aggregation dimensions.
  - Added comprehensive test suites in `pkg/proc/builtins_test.go` and `pkg/api/sqlexec_test.go`.

- **Default Data Directory Resolution Relative to Executable Location (`cmd/server`)**:
  - Enhanced default `-dir` flag and server initialization to automatically resolve adjacent data directory (`<binary_dir>/../data`) when the server executable is located inside a `bin/` directory.
  - Prevents unintended database creation in disparate current working directories when executing `./bin/server` from arbitrary shell locations.
  - Added unit test `TestResolveDefaultDataDirFromExe` in `cmd/server/server_test.go`.

- **TPC-C VDBE Stored Procedure Single Atomic Transaction Encapsulation (`cmd/tpcc_vdbe`)**:
  - Refactored `runVDBENewOrder`, `runVDBEDelivery`, and `runVDBEStockLevel` in `cmd/tpcc_vdbe` to execute within explicit, atomic ACID transactions (`sess.BeginTx` and `exec.RunInTx`) rather than multi-statement ambient autocommit transactions.
  - Aligns VDBE TPC-C benchmark execution strictly with official TPC-C specification invariants (all order lines and district header mutations committed atomically in a single transaction).
  - Eliminates redundant multi-transaction Cgo begin/commit overhead across New-Order loops.

### Removed
- **Legacy Text JSON & Fallback Storage Codecs (`pkg/storage/wiredtiger`, `pkg/storage/memengine`)**:
  - Removed JSON and MessagePack fallback parsing from WiredTiger record decoding (`decodeRecord` and `decodeRow`), standardizing 100% on native binary tuple format (`0x01`).
  - Purged legacy `tables.json` disk loading, JSON-to-MessagePack auto-migration logic, and `legacyTableData` map conversion structures from the in-memory engine.
  - Removed legacy JSON-lines probing and scanning from `ReplayWAL`, standardizing AOF/WAL recovery exclusively on framed MessagePack payloads.
  - Standardized In-Memory OCC engine snapshot persistence strictly on `tables.mpk` (MessagePack metadata container) and OLTP row records on schema-ordered contiguous `Row` representations.

### Added
- **Foreign Key Dependency Validation on WiredTiger TruncateTable (`pkg/storage/wiredtiger`)**:
  - Enforced strict foreign key constraint checks in `TruncateTable` on the WiredTiger engine, matching the In-Memory engine's `ErrDependentObjectsExist` behavior when child tables reference the target.
  - Ensured atomic LSN progression on WiredTiger DDL truncations.

- **Direct Row Mutation Transaction APIs (`pkg/storage`, `pkg/storage/memengine`, `pkg/storage/wiredtiger`)**:
  - Added `InsertRow` and `UpdateRow` methods to the `storage.Transaction` interface contract across all storage backends.
  - Allows the VDBE bytecode VM and query executors to write contiguous `storage.Row` directly without allocating intermediate `map[string]Value` records.

- **Zero-Allocation UndoLog Optimization & Purge of Legacy Record Cloning (`pkg/storage/wiredtiger`)**:
  - Refactored `wtUndoOp` in `pkg/storage/wiredtiger/wt_engine.go` to store raw binary tuple bytes (`oldData []byte`) instead of cloned `map[string]Value` records.
  - Implemented `getRawBytesLocked` and `putRawBytesLocked` to achieve zero-copy savepoint rollbacks (`RollbackTo`) directly at the WiredTiger cursor level.
  - Purged redundant `cloneRecord` allocation helper to eliminate GC overhead on high-frequency `UPDATE` and `DELETE` paths.

- **Wire Protocol Session Read-Only Transaction Option Binding (`pkg/api`)**:
  - Bound session-scoped `transaction_read_only` variable and `SET TRANSACTION READ ONLY` directly into interactive and autocommit `BeginTx` transaction options.
  - Added `IsReadOnly()` and `TxOptions()` helper methods to `ClientSession` to ensure read-only transactions are strictly enforced down to the storage engine level.

- **Atomic Batched DML WAL Replay Optimization (`pkg/backup`)**:
  - Refactored `ApplyWALRecordWithOptions` in `pkg/backup/pitr.go` to batch contiguous DML mutations (`INSERT`, `UPDATE`, `DELETE`) within a single atomic transaction rather than allocating and committing per-row sessions.
  - Eliminates redundant begin/commit cycles and Cgo roundtrips across WAL replay loops, improving recovery and replication replay throughput.

- **Storage/Session Read-Only Defense in Depth & DDL Catalog Synchronization (`pkg/storage`, `pkg/catalog`, `pkg/backup`)**:
  - Enforced strict `ErrReadOnlyTransaction` and `ErrReadOnlyReplica` (SQLSTATE `25006`) errors across all storage mutations (`Insert`, `Update`, `Delete`) in In-Memory OCC and WiredTiger engines.
  - Propagated read-only error semantics cleanly to VDBE virtual machine and higher-layer SQL execution engines to prevent secondary replica data contamination.
  - Implemented `ApplyWALRecordWithOptions` in `pkg/backup/pitr.go` with `ddlHook` support and `OpDropPartition` replay handling.
  - Added `InvalidateTableStats` to `catalog.Catalog` for instantaneous CBO cache invalidation upon replicated DDL operations.

- **Transaction Lifecycle & MVCC Commit Timestamp / LSN Snapshot Binding (`pkg/storage`, `pkg/storage/memengine`, `pkg/storage/wiredtiger`)**:
  - Extended `storage.TxOptions` with `SnapshotLSN` and `SnapshotTimestamp` to enable deterministic consistent snapshot bounds across replication replicas.
  - Added `SnapshotLSN()`, `SnapshotTimestamp()`, `CommitLSN()`, and `CommitTimestamp()` to the `storage.Transaction` interface contract.
  - Implemented monotonic `CurrentLSN()` on `storage.Engine` across both In-Memory OCC and WiredTiger storage engines.
  - Ensured atomic assignment and exposure of committed LSN and unix nano timestamps upon transaction commit.

- **Zero-Allocation Compact Binary Tuple WAL & Replication Pipeline (`pkg/storage`, `pkg/storage/memengine`, `pkg/backup`)**:
  - Implemented `EncodeRow` and `EncodeRowToBuffer` in `pkg/storage/tuple.go` for schema-aware compact binary tuple encoding directly from contiguous `storage.Row` without `map[string]Value` conversions, achieving 29.75ns/op throughput (5x faster than map encoding).
  - Extended `WALOp` with `Data []byte`, allowing transaction commit (`tx.Commit`) to write raw binary tuples directly into WAL records with zero intermediate map allocations (`op.Record = nil`).
  - Optimized AOF replay (`ApplyWALRecord`) to decode binary tuples directly into `storage.Row` using `DecodeTupleToRow`, eliminating reflection and map allocations during crash recovery and replication log application.
  - Reduced WAL record serialization footprint by 60~70% and enabled zero-copy streaming for replication followers.

- **TPC-C VDBE Atomic Transaction Invariant Suite & Architecture Guidelines (`cmd/tpcc_vdbe`, `GEMINI.md`)**:
  - Added automated atomicity test suite `cmd/tpcc_vdbe/atomicity_test.go` verifying that TPC-C New-Order commits and rolls back atomically across both In-Memory and WiredTiger engines with zero state leakage.
  - Formalized Core Architectural Invariant Section 7 in `GEMINI.md` mandating strict All-or-Nothing atomic ACID transaction modeling across all benchmarks and multi-statement workloads.

- **Fine-Grained Sharded Locking Architecture for In-Memory OCC Engine (`pkg/storage/memengine`)**:
  - Replaced table-level exclusive locking during `Commit()` with 64-shard striped row locks (`[64]tableShard`), eliminating global table contention and serial blocking across concurrent write transactions.
  - Implemented hierarchical intent-shared locking (`td.mu.RLock()` for intent share vs `td.mu.Lock()` for DDL) and strictly ordered canonical lock acquisition (`tableNames` ascending, followed by `shardIdx` ascending) to mathematically eliminate transaction deadlocks (Rule 10).
  - Transitioned `GetRow`, `Get`, `hasKey`, and `memCursor.Next` to lightweight per-shard read locks, allowing concurrent point lookups and scans to proceed unblocked while other transactions commit on non-overlapping shards.
  - Accelerated transaction commit by unlocking memory shard locks immediately upon state mutation and prior to atomic WAL appending, minimizing critical sections and eliminating self-deadlocks during AOF rewrite compactions.

- **Tuple Serialization Buffer & VM Execution Frame Pooling via `sync.Pool` (`pkg/storage`, `pkg/proc/runtime`, `pkg/storage/wiredtiger`)**:
  - Implemented `tupleBufferPool` in `pkg/storage/tuple.go` (`GetTupleBuffer`, `PutTupleBuffer`, and `EncodeTupleToBuffer`) to eliminate heap allocations during high-frequency binary tuple encoding.
  - Integrated pooled byte buffers into WiredTiger's record serialization pipeline (`serializeRecordToBuffer`), avoiding byte slice allocations on every `putLocked` and `Update` operation.
  - Added `framePool` to T-SQL VDBE `Executor` in `pkg/proc/runtime/vm.go`, pooling and recycling `VMFrame` execution contexts across stored procedure invocations with comprehensive register and state cleanup.

- **Direct Row Passthrough Architecture for Query Execution & Storage Cursors (`pkg/storage`, `pkg/execution`)**:
  - Extended `storage.Cursor` interface with `Row() (storage.Row, error)` and `storage.Transaction` with `GetRow(tableName string, pk storage.Value) (storage.Row, error)` to pass raw slice-backed rows directly to execution engines without `map[string]Value` conversions.
  - Implemented `DecodeTupleToRow` in `pkg/storage/tuple.go` to unpack binary tuples directly into contiguous `Row` values, eliminating intermediate record allocations in WiredTiger cursor reads.
  - Added `FormatRow` in `pkg/execution/iterator.go` and updated `SeqScanIterator` and `IndexScanIterator` to format volcano execution tuples directly from `storage.Row` via O(1) column index projections.

- **In-Memory OCC Engine Contiguous Row Storage Transition (`pkg/storage/memengine`, `pkg/storage`)**:
  - Replaced map-based row storage (`map[string]storage.Record`) with contiguous slice-backed `Row` structures (`map[string]storage.Row`), drastically cutting Go pointer indirection, map hash computation, and GC tracing pressure across high-volume OLTP workloads.
  - Converted internal transaction mutation buffers (`local map[string]map[string]storage.Row`) and `rowChange` journals to operate on schema-ordered `Row` objects.
  - Optimized foreign key validation (`rowMatchesFK`) and secondary index key extraction (`extractIndexKeyFromRow`) to evaluate directly against ordered column slice indices without intermediate record map allocations.
  - Preserved transparent backward compatibility with legacy snapshots and WAL logs via automatic format detection and `legacyTableData` migration.

- **In-Memory OCC Zero-Copy Read-Only Optimization & Prefix Key Acceleration (`pkg/storage/memengine`, `pkg/api`)**:
  - Eliminated unnecessary map cloning in `Transaction.Get` for read-only transactions (`tx.opts.ReadOnly`), reducing heap allocations and GC pressure on high-frequency read paths.
  - Optimized `buildCompositePrefixKey` in `pkg/api/sqlexec_dml.go` using pre-allocated `strings.Builder` with capacity reservation to eliminate temporary slice and string allocations during composite index scans.

- **WiredTiger Engine Native Binary Tuple Codec Integration (`pkg/storage/wiredtiger`)**:
  - Connected `EncodeTuple` and `DecodeTuple` directly into WiredTiger's record serialization pipeline (`serializeRecord` and `decodeRecord` in `pkg/storage/wiredtiger/wt_engine.go`).
  - Records inserted and updated into WiredTiger B+Tree tables are now stored as ultra-compact binary tuples with zero column name repetition, halving the raw Cgo payload size transferred across the engine boundary.
  - Implemented multi-tier decoding fallback: checks version tag `0x01` to parse binary tuples first, gracefully falling back to MessagePack or JSON for 100% backward compatibility with preexisting tables.
  - Enhanced `WTCursor` with table schema context to allow direct zero-alloc tuple unpacking during sequential and index scans.

- **Schema-Aware Compact Binary Tuple Codec & Row Architecture (`pkg/storage`)**:
  - Implemented schema-aware binary tuple serialization (`EncodeTuple` and `DecodeTuple` in `pkg/storage/tuple.go`): features a 1-byte version tag, compact bit-packed null bitmap, and sequentially packed typed payloads (varint integers, IEEE-754 floats, unix nano timestamps, and length-prefixed strings).
  - Achieved a further 50% storage footprint reduction over compact MessagePack (103 bytes vs 204 bytes, and 92.7% reduction vs JSON 1413 bytes) with sub-100ns encoding throughput (76.6ns/op).
  - Introduced the ordered `Row` struct (`type Row struct { Values []Value }`) with bidirectional zero-loss conversion helpers `TableSchema.RecordToRow` and `TableSchema.RowToRecord`.
  - Added comprehensive round-trip tests and benchmarks in `pkg/storage/tuple_test.go`.

- **High-Performance Compact MessagePack Codec & Index Key Encoding Acceleration (`pkg/storage`, `pkg/storage/memengine`)**:
  - Implemented custom MessagePack `CustomEncoder` and `CustomDecoder` for `storage.Value` (`pkg/storage/value_codec.go`): encodes values into a compact 2-element array `[DataType, Payload]`, slashing serialized `Value` footprint from 89 bytes down to 3 bytes (96.6% reduction) and 3-column record footprint from 279 bytes to 35 bytes (87.5% reduction).
  - Maintained 100% backward compatibility with legacy map-based `tables.mpk` snapshots and WAL logs via automatic `msgpcode` format detection.
  - Accelerated composite index key encoding (`EncodeIndexKeyPart`) by replacing reflection-based `fmt.Sprintf("%016x")` with unrolled bit-shift hex formatting, boosting throughput by 5.1x (72ns -> 14ns per op).
  - Optimized composite index extraction (`extractIndexKey`) in `memengine` using `strings.Builder` to eliminate intermediate string allocations.
  - Added comprehensive round-trip tests and benchmarks across all data types in `pkg/storage/codec_test.go`.

- **ClickHouse-Style Auto Rolling Range Partitioning & O(1) Fast Drop Partition (`pkg/storage`, `pkg/planner`, `pkg/execution`, `pkg/api`)**:
  - Implemented intuitive ClickHouse-style range table partitioning via `CREATE TABLE ... PARTITION BY { toYYYYMM(col) | MONTH(col) | toYYYYMMDD(col) | DAY(col) | toYYYY(col) | YEAR(col) | col }`.
  - Added Auto Rolling partition management: partitions are created and tracked dynamically upon row insertion without requiring cumbersome manual partition scheme pre-creation.
  - Implemented $O(1)$ fast partition dropping via `ALTER TABLE <table_name> DROP PARTITION '<partition_id>'` (e.g. `ALTER TABLE logs DROP PARTITION 'p_archive'`), instantly purging rows and cleaning secondary indexes.
  - Added Cost-Based Optimizer partition pruning (`PrunePartitions` in `pkg/planner/pruning.go`) that evaluates equality, range (`<`, `<=`, `>`, `>=`), `BETWEEN`, and `IN` predicates on partition columns, skipping non-matching partitions with zero row comparisons.
  - Integrated partition metrics and pruning feedback into `EXPLAIN` text and PostgreSQL `EXPLAIN (FORMAT JSON)` for graphical GUI tools.
  - Added partition introspection via `SHOW PARTITIONS [FROM] <table_name>` and virtual system catalog views `system.partitions` / `sys.partitions`.
  - Supported persistence across disk snapshots (`tables.mpk`) and atomic crash recovery via WAL/AOF (`OpDropPartition`).
  - Added comprehensive test suites in `pkg/api/partition_test.go` and `pkg/planner/pruning_test.go`.

- **Comprehensive `pg_catalog` & `information_schema` Virtual Views for PostgreSQL GUI Tools and ORM Compatibility (`pkg/api/sqlexec_pgcatalog.go`, `pkg/api/sqlexec_dbcc.go`, `pkg/wire`)**:
  - Implemented virtual system views in `pg_catalog` (`pg_database`, `pg_namespace`, `pg_class`, `pg_type`, `pg_attribute`, `pg_constraint`, `pg_index`, `pg_roles`, `pg_user`, `pg_am`, `pg_settings`, `pg_stat_user_tables`, `pg_stat_user_indexes`, `pg_statio_user_tables`, `pg_locks`, `pg_stat_activity`, `pg_proc`, `pg_views`, and related stubs) with deterministic OIDs and canonical PostgreSQL data type mappings.
  - Implemented virtual views in `information_schema` (`tables`, `columns`, `schemata`, `routines`, `table_constraints`, `key_column_usage`, `referential_constraints`, `constraint_column_usage`, `views`, and role grants).
  - Accurately generates PRIMARY KEY (`contype='p'`), UNIQUE (`contype='u'`), and FOREIGN KEY (`contype='f'`) constraints to render schema relationship diagrams in GUI tools.
  - Enables out-of-the-box metadata introspection, schema navigation, and query execution for GUI tools (DBeaver, DataGrip, psql, pgAdmin) and ORM reflection engines (SQLAlchemy, Hibernate, GORM).
  - Extracted shared `buildVirtualCatalogResult` helper for unified projection, WHERE filtering, ORDER BY sorting, and LIMIT trimming.
  - Added end-to-end integration tests in `pkg/api/pgcatalog_test.go` and `pkg/wire/server_test.go`.

- **PGWire Transaction Status ('I'/'T'/'E') & Standard SQLSTATE Error Protocol (`pkg/wire`, `pkg/api`)**:
  - Implemented dynamic `ReadyForQuery` transaction status synchronization: emits `'I'` (Idle outside transaction), `'T'` (In active transaction block), and `'E'` (In aborted transaction block).
  - Enforced PostgreSQL transaction abort semantics on PGWire sessions: commands within an aborted transaction are rejected with `25P02` until explicit `ROLLBACK` is issued.
  - Enhanced `WriteErrorWithCode` with standard PostgreSQL SQLSTATE codes (`42P01`, `42601`, `42501`, `25P02`, `23505`, `57014`, `XX000`).
  - Added runtime column data type OID inference (`inferPgOID`) for Simple Query and Extended Query `RowDescription` packets.
  - Added full test coverage in `pkg/wire/server_test.go` (`TestWire_TransactionStatusAndSQLState`).

- **PostgreSQL Wire Protocol Password Authentication Handshake (`pkg/wire`, `pkg/auth`, `pkg/api`)**:
  - Implemented PostgreSQL v3 StartupMessage parameter parsing (`parseStartupParams`) for `user`, `database`, and `application_name`.
  - Added `AuthenticationCleartextPassword` (`'R'` code 3) and `AuthenticationMD5Password` (`'R'` code 5) protocol messages.
  - Integrated with `pkg/auth` RBAC server login verification (`AuthenticateLogin`), automatically challenging passwords for secured users and rejecting invalid credentials with SQLSTATE `28P01`.
  - Seamlessly bound authenticated username directly to `ClientSession.User` (`SetSessionUser`) for downstream RBAC table permission enforcement.
  - Added comprehensive integration tests in `pkg/wire/server_test.go` (`TestWire_PasswordAuthentication`).

- **Query Plan Visualizer & PostgreSQL Standard `EXPLAIN (FORMAT JSON)` Support (`pkg/planner/explain.go`, `pkg/api/sqlexec_dml.go`)**:
  - Implemented standard PostgreSQL `EXPLAIN [ ( option [, ...] ) ]` parser (`ParseExplainOptions`) supporting `ANALYZE`, `VERBOSE`, `COSTS`, `BUFFERS`, and `FORMAT {TEXT | JSON | YAML | XML}`.
  - Implemented canonical PostgreSQL `EXPLAIN (FORMAT JSON)` serializer (`FormatPostgresPlanJSON`) enabling full graphical visual plan tree rendering in GUI tools (DBeaver, DataGrip, pgAdmin).
  - Added DML statement EXPLAIN support (`EXPLAIN INSERT`, `EXPLAIN UPDATE`, `EXPLAIN DELETE`) with `NodeModifyTable` plan trees and primary key index scan resolution.
  - Added unit and regression tests in `pkg/api/explain_test.go`.

## [0.10.0]

### Changed
- **Open Source Licensing: GNU General Public License v3 (GPL-3.0-or-later)**:
  - Formally consolidated project license under the GNU General Public License v3 (`GPL-3.0-or-later`) across `LICENSE` and `README.md`.
  - Maintained commercial dual-licensing path for enterprise deployments requiring custom proprietary embedding terms.

- **Architectural Invariant: Database Isolation Strategy (Instance-Based Isolation)**:
  - Formally codified instance-level isolation (Process / Container per Database) in `GEMINI.md` as a permanent core architectural invariant.
  - Specified out `USE <dbname>` catalog multiplexing in favor of physical process/container separation, guaranteeing zero-shared blast radius, independent CPU/RAM/descriptor limits, crash resilience, and eliminating cross-database catalog lock contention.

- **Architectural Invariant: Transaction Isolation & Concurrency Model**:
  - Formally codified canonical Non-Blocking Snapshot Isolation / Read Committed with Optimistic/Hazard Concurrency Control in `GEMINI.md`.
  - Readers never block writers and writers never block readers, eliminating traditional 2PL shared read locks.
  - Formally specified out/deferred complex SSI dependency graphs and `READ UNCOMMITTED` levels in favor of high-throughput non-blocking OLTP execution.

- **Architectural Invariant: Self-Contained Single Binary (Zero-External-Dependency)**:
  - Formally codified full-stack self-containment in `GEMINI.md`: core DB, PGWire/TDS wire protocols, REST API, embedded Web Studio, pooler, cron, mailer run entirely from a single static Go binary.
  - Explicitly specified out external distributed orchestrators/brokers (ZooKeeper, etcd, Consul, Kafka, RabbitMQ) from the database kernel.

- **Architectural Invariant: Scale-Up Primacy & WAL Replication Topology**:
  - Formally codified vertical scale-up hardware saturation and 1-Writer Multi-Reader (Primary-Replica) WAL streaming topology in `GEMINI.md`.
  - Eliminated distributed 2PC write penalties to preserve sub-microsecond single-node transaction speed (16,000+ TPS).
  - Explicitly specified out distributed multi-master sharding in favor of high-availability WAL replication and failover promotion.

### Performance
- **VDBE Bytecode Engine Primary Key O(1) Fast-Path Optimization (`pkg/proc/runtime/vm.go`, `pkg/proc/parser.go`, `cmd/tpcc_vdbe`)**:
  - Replaced hardcoded `"id"` column checks in `execSelectInto`, `OpUpdate`, and `OpDelete` with dynamic primary key metadata resolution via `isPKCol` and `extractSimpleEq`.
  - Generalized single-column equality predicate parsing in `parseUpdate` and `parseDelete` so any column equality emits targeted `UpdateStmt` and `DeleteStmt` ASTs.
  - Implemented an O(1) point lookup fast-path using `f.tx.Get(table, pkVal)` in `execSelectInto`, `execUpdateWhere`, and `execDeleteWhere`, eliminating table-wide full scans on non-`id` primary key lookups (`w_id`, `d_key`, `c_key`, `s_key`, `no_key`).
### Refactored
- **Codebase Simplification & Deletion-First Cleanups (Rules 15, 16, 30)**:
  - **Daemon CLI Aliasing (`cmd/server/main.go`)**: Bound both `-d` and `-daemon` flags directly to a single `var daemon bool` variable via `flag.BoolVar`, removing duplicate flag pointers and compound boolean checks.
  - **Transaction & Savepoint Slicing Consolidation (`pkg/api/sqlexec.go`)**: Introduced `trimAnyPrefix` helper to unify prefix matching and slicing across `SAVEPOINT`, `RELEASE`, and `ROLLBACK`, eliminating redundant substring slicing loops.
  - **Query Log Configuration Alignment (`pkg/config/config.go`, `cmd/server/main.go`)**: Removed redundant `Enabled` boolean flags from `SlowQueryLogConfig` and `GeneralQueryLogConfig`, standardizing on empty path `""` as canonical disabled state. Deduplicated CLI resolution with `resolveLogFilePath`.
  - **Doctor Health Check Socket Safety (`cmd/server/doctor.go`)**: Checked for empty listener addresses before probing TCP ports, preventing unintended ephemeral socket allocations for disabled services.
  - **Zero-Allocation Hot Path Precompilation (`pkg/api/sqlexec_auth.go`)**: Hoisted function-level `regexp.MustCompile` invocations into static package-level variables for authentication DDL/DCL execution.

### Added
- **Subtotal & Grand Total Aggregation Engine (`GROUP BY ROLLUP`, `CUBE`, `GROUPING()`) (`pkg/proc`, `pkg/planner`, `pkg/execution`, `pkg/api`)**:
  - Implemented ANSI SQL / T-SQL `GROUP BY ROLLUP(...)` and `GROUP BY CUBE(...)` single-pass multi-grouping aggregation engine.
  - Supported T-SQL syntax forms: `GROUP BY ROLLUP(...)` / `GROUP BY CUBE(...)` and trailing `GROUP BY ... WITH ROLLUP` / `GROUP BY ... WITH CUBE`.
  - Added bitmask-driven single-pass accumulator mapping: generates $N+1$ hierarchical levels for `ROLLUP` and $2^N$ combinatorial grouping sets for `CUBE` without redundant table rescans.
  - Implemented standard `GROUPING(col)` aggregate function returning `1` for rolled-up/subtotal NULL dimensions and `0` for regular groupings.
  - Implemented deterministic hierarchical subtotal sorting so subtotals appear naturally grouped under parent dimensions followed by grand totals.
  - Integrated `HAVING` filters across all subtotals and grand totals.
  - Added dedicated integration test suite in `pkg/api/rollup_cube_test.go` covering single-column and multi-column ROLLUP, CUBE cross-tabulation, WITH ROLLUP syntax, and HAVING filters.
- **Window Frame Specification (`ROWS BETWEEN ... PRECEDING / FOLLOWING`) (`pkg/planner`, `pkg/execution`, `pkg/api`)**:
  - Implemented ANSI SQL / T-SQL window frame specifications: `ROWS BETWEEN <start> AND <end>` and shorthand forms (`ROWS <bound> PRECEDING`).
  - Supported frame bounds: `UNBOUNDED PRECEDING`, `N PRECEDING`, `CURRENT ROW`, `N FOLLOWING`, and `UNBOUNDED FOLLOWING`.
  - Added sliding window frame aggregation for `COUNT`, `SUM`, `AVG`, `MIN`, and `MAX` in the window execution engine.
  - Defaults to cumulative running total (`ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`) when `ORDER BY` is specified without an explicit frame clause.
  - Added dedicated test suite in `pkg/api/window_test.go` (`TestWindow_Frames_RunningTotalAndMovingAvg`) covering running totals, centered moving averages, and moving sums.
- **Real-Time Time-Bucket Analytics Engine (`DATETRUNC`, `DATE_BUCKET`) (`pkg/proc`, `pkg/execution`, `pkg/api`)**:
  - Implemented T-SQL 2022 and ANSI SQL `DATETRUNC(datepart, date)` and `DATE_TRUNC(datepart, date)` for date truncation (`year`, `quarter`, `month`, `week`, `day`, `hour`, `minute`, `second`, `millisecond`).
  - Implemented T-SQL `DATE_BUCKET(datepart, width, date [, origin])` for custom time-bucket aggregations (e.g. 5-minute, 15-minute, 2-hour intervals) aligned to clock boundaries.
  - Enabled pushdown time-bucket streaming aggregation in `GROUP BY DATE_BUCKET(minute, 5, timestamp)` with zero unnecessary string allocations.
  - Added unit test coverage in `pkg/proc/builtins_test.go` and query integration tests in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_TimeBucketGrouping`).
- **Multi-Protocol Listener Granular Opt-Out (`pkg/config`, `cmd/server`)**:
  - Implemented clean, zero-redundancy listener opt-out for PostgreSQL (`:5433`), TDS (`:1433`), and HTTP (`:8090`) following the Deletion-First / KISS rule.
  - Eliminated ad-hoc boolean flags by establishing a single universal convention: providing an empty address string (`""`) disables that listener across both CLI flags (`-pg=""`, `-tds=""`, `-http=""`) and JSON configuration (`"pg_addr": ""`).
  - Allowed running in specialized modes: pure REST/Web-only (both wire protocols disabled), headless DB-only (HTTP disabled), or single-protocol standalone with zero port conflicts.
  - Added unit test coverage in `pkg/config/config_test.go` (`TestNetworkConfig_OptOut`).

- **T-SQL `FOR JSON` Clause Engine (`FOR JSON PATH`, `AUTO`) (`pkg/api`)**:
  - Implemented native Microsoft T-SQL `FOR JSON PATH` and `FOR JSON AUTO` query clauses returning query results formatted directly as standard JSON strings.
  - Supported standard T-SQL JSON options:
    - `WITHOUT_ARRAY_WRAPPER`: Emits single un-enclosed JSON objects without square bracket array wrappers.
    - `ROOT('root_name')`: Wraps top-level JSON elements in a named parent object.
    - `INCLUDE_NULL_VALUES`: Preserves null keys in serialized JSON outputs instead of omitting them.
    - Dot notation nesting: `col AS [parent.child]` automatically creates nested JSON object hierarchies (`{"parent": {"child": ...}}`).
  - Standard column header identifier: Emits MSSQL standard `JSON_F52E2B61-18A1-11d1-B105-00805F49916B` single-row JSON string output.
  - Added dedicated test suite in `pkg/api/for_json_test.go` covering basic path arrays, without array wrapper, root elements, and dot-notation nested objects.

- **REST API Raw JSON Array Output Format (`POST /api/sql?format=array`) (`pkg/api`)**:
  - Implemented configurable JSON output formatting on `POST /api/sql` supporting both query parameter (`?format=array|records|raw`) and request body (`{"format": "array"}`).
  - Preserved the production-standard metadata envelope (`{ "success": true, "columns": [...], "rows": [...], "rows_affected": ... }`) as the default representation to guarantee robust error handling and DML observability.
  - Enabled clients to directly receive a pure JSON record array (`[ { ... }, { ... } ]`) bypassing outer wrappers for effortless frontend (`res.map()`) and microservice pipeline integration.
  - Added unit test coverage in `pkg/api/server_test.go` (`TestAPI_ExecuteSQL_FormatArray`) and updated `doc/API_REFERENCE.md`.

- **Composite Index Multi-Column Range Search & Descending Scan (`pkg/storage`, `pkg/execution`, `pkg/api`)**:
  - Implemented order-preserving key encoding `storage.EncodeIndexKeyPart` ensuring strict byte-level monotonic ordering for numeric (`int64`, `float64`), temporal (`date`, `time`, `datetime`), and textual composite index columns.
  - Implemented multi-column composite index range scanning (`findIndexAndBounds`, `parseWhereConditions`) combining leading equality predicates (`a = ?`) with range bounds (`b >= ?`, `b > ?`, `b <= ?`, `b < ?`, `b BETWEEN ? AND ?`) on the subsequent indexed column.
  - Added `IndexScanDesc` method to `storage.Transaction`, `memengine.Transaction`, and `wiredtiger.WTTx` for reverse cursor iteration.
  - Added `Desc` option to `IndexScanIterator` and optimized `execSelect` to bypass in-memory sorting passes when `ORDER BY ... DESC` matches index order.
  - Added comprehensive unit tests in `pkg/api/composite_index_test.go` verifying prefix equality, range bounds, and descending scans.

- **T-SQL PIVOT Clause Engine (`pkg/api`)**:
  - Implemented native Microsoft T-SQL `PIVOT` operator parser (`hasPivot`, `parsePivotQuery`) supporting direct table sources and derived subqueries (`(SELECT ...) AS src PIVOT (...) AS pvt`).
  - Implemented cross-tabulation and aggregation execution pipeline (`execPivotSelect`) supporting `SUM`, `COUNT`, `AVG`, `MIN`, and `MAX` aggregate functions across dynamic column value targets (`FOR col IN ([val1], [val2], ...)`).
  - Supported bracket column identifiers (`[Q1]`, `[Junior]`), outer projection aliases (`[Q1] AS FirstQuarter`), outer `WHERE` predicate filtering on pivoted columns, and outer `ORDER BY` sorting.
  - Added dedicated test suite in `pkg/api/pivot_test.go` covering `SUM`, `COUNT`, `AVG`, `MIN`, `MAX`, derived subqueries, table sources, aliases, and filtering.

- **T-SQL UNPIVOT Clause Engine (`pkg/api`)**:
  - Implemented native Microsoft T-SQL `UNPIVOT` operator parser (`hasUnpivot`, `parseUnpivotQuery`) supporting direct table sources and derived subqueries (`(SELECT ...) AS src UNPIVOT (...) AS unpvt`).
  - Implemented reverse-pivoting execution pipeline (`execUnpivotSelect`) transforming wide column structures into normalized name-value row pairs (`<val_col> FOR <name_col> IN (<col1>, <col2>, ...)`).
  - Preserved non-unpivoted columns per row, skipped NULL values according to T-SQL relational semantics, and handled bracket escapes (`[Math]`, `[English]`).
  - Supported outer projection expressions/aliases, outer `WHERE` filtering on unpivoted columns, and outer `ORDER BY` sorting.
  - Added comprehensive test coverage in `pkg/api/pivot_test.go` (`TestUnpivot_Basic`, `TestUnpivot_WithSubqueryAndBracketSyntax`).

- **Native VDBE Bytecode Direct Execution for Scalar & Point Queries (`pkg/proc`, `pkg/api`)**:
  - Implemented `OpResultRow` opcode in `pkg/proc/bytecode.go` and `pkg/proc/runtime/vm.go` to directly evaluate and emit rows for table-less scalar / constant SELECT queries (`SELECT 1 + 1`, `SELECT GETDATE()`, etc.) bypassing Volcano relational planner and execution trees.
  - Implemented `OpPointSelect` opcode for single-table Primary Key point lookups (`WHERE pk = ?`) executing directly via O(1) `tx.Get()` storage lookups and register projection.
  - Added primary key constraint verification (`isPKCol`) to ensure non-PK equality predicates safely delegate to relational execution (`OpRelQuery`) preserving multi-row correctness.
  - Routed table-less scalar queries from `api.execSelect` directly to VDBE bytecode execution (`execVDBESelect`), maintaining session variable isolation (`@@SPID`, `@@IDENTITY`).
  - Added comprehensive test suite in `pkg/api/vdbe_select_test.go` verifying scalar expressions, functions, aliases, session variable bindings, and bytecode disassemblies.

- **Dynamic SQL Execution & `sp_executesql` Engine (`pkg/api`)**:
  - Implemented dynamic SQL statement execution supporting direct string literals (`EXEC ('SELECT ...')`), session variable references (`EXEC (@sql)`, `EXEC @sql`), and string concatenation expressions with `+` (`EXEC ('SELECT ' + @col + ' FROM ' + @tbl)`).
  - Implemented full Microsoft T-SQL `sp_executesql` parameterization engine with support for positional and named parameters (`@stmt = N'...', @params = N'...', @id = 1`), `OUTPUT` parameter syntax, and safe session variable binding.
  - Hardened parameter and session variable substitution with word boundary detection (`\b`), quotation insulation, and length-descending sorting to prevent partial-token collisions.
  - Added comprehensive test suite in `pkg/api/dynamic_sql_test.go` covering literal execution, variable binding, `sp_executesql` parameterization, quote protection, string concatenation, and dynamic DDL/DML.
- **Extended Data Types Support (UUID, VARBINARY, DATE, TIME) (`pkg/storage`, `pkg/api`, `pkg/proc`, `pkg/wire`)**:
  - Extended core storage engine type system (`pkg/storage/engine.go`) with `TypeUUID`, `TypeBinary`, `TypeDate`, and `TypeTime`.
  - Maintained Go map key comparability invariant on `storage.Value` by storing binary representations in `BinVal string` and exposing bytes via `.Bytes() []byte`.
  - Added dedicated type parsing, serialization, and normalized comparisons in `pkg/storage/types_extended.go`.
  - Updated DDL/DML parsers (`pkg/api/sqlexec_ddl.go`, `pkg/api/sqlexec_dml.go`) and procedure parsers (`pkg/proc/parser.go`, `pkg/proc/parser/antlr/ast_builder.go`) for `UUID`/`UNIQUEIDENTIFIER`, `VARBINARY`/`BYTEA`/`BLOB`, `DATE`, and `TIME`.
  - Configured PostgreSQL wire protocol type OID mappings (`pkg/wire/server.go`) for `DATE` (1082), `TIME` (1083), `UUID` (2950), and `BYTEA` (17).
  - Added full test coverage in `pkg/storage/types_extended_test.go` and end-to-end DDL/DML verification in `pkg/api/extended_types_test.go`.
- **Continuous WAL Archiving & Point-In-Time Recovery (PITR) (`pkg/backup`, `cmd/server`)**:
  - Implemented continuous WAL archiver (`pkg/backup/wal_archive.go`) managing rotation, boundary detection (`MinLSN`, `MaxLSN`, timestamps), and atomic persistence to `wal_archive/` repository.
  - Implemented Point-In-Time Recovery engine (`pkg/backup/pitr.go`) restoring baseline snapshot archives and incrementally replaying committed transactions up to specific target timestamps or LSNs.
  - Exported thread-safe `ApplyWALRecord` and `ParsePKValue` in `pkg/storage/memengine` with cross-engine fallback supporting both In-Memory OCC and WiredTiger storage engines.
  - Added CLI flags `--wal-archive`, `--restore-time`, `--restore-lsn`, and `--archive-wal` in `cmd/server/main.go` for automated DR operations and disaster recovery pipelines.
  - Added comprehensive test suite in `pkg/backup/pitr_test.go` covering WAL segment rotation, manifest tracking, timestamp-based recovery, and LSN-targeted recovery.
- **Self-Diagnostic Health Verification CLI (`--doctor`) (`cmd/server`)**:
  - Implemented standalone system health diagnostic command `--doctor` complying with Rule 28.
  - Verifies host environment (OS, Arch, Go version, logical CPUs, memory footprint), filesystem permissions (atomic R/W probe, path normalization, latency), and network port bindings (HTTP, PostgreSQL, TDS).
  - Validates storage engines integrity across In-Memory OCC (DDL, transaction commit/rollback) and WiredTiger B+Tree (CGo build tag, ACID write/read).
  - Validates dual-parser architecture (Primary Pratt fast-path & Secondary ANTLR fallback) and VDBE bytecode compiler/VM execution.
  - Verifies TLS cryptography (dynamic self-signed certificate generation / keypair validity) and authentication hashing subsystem.
  - Added unit test suite in `cmd/server/doctor_test.go` verifying 100% test pass rate with 0 failures.

## [0.9.0]

### Performance
- **Zero-Copy Streaming Cursor (`pkg/storage/memengine`)**:
  - Eliminated full-table row materialization bottlenecks in `Scan`, `IndexScan`, and `IndexOnlyScan`.
  - Replaced intermediate `merged := make(map[string]storage.Record)` map and `records := make([]storage.Record, len(keys))` slice allocations with a streaming cursor (`memCursor`) that retains sorted primary keys and resolves records lazily on demand during `Next()`.
  - Added on-demand record synthesis via `resolver` closure for `IndexOnlyScan` to eliminate pre-allocation of covering index records.
  - Achieved ~80% reduction in memory allocations (from 79,216 B/op down to 16,480 B/op) and 75% fewer allocations (from 8 down to 2 allocs/op) in full-scan benchmarks (`BenchmarkMemCursor_Scan`).

### Added
- **Prometheus `/metrics` Observability & Plan Cache Statistics (`pkg/api`, `pkg/proc`)**:
  - Enhanced Prometheus `/metrics` endpoint with runtime Go metrics: `lionsql_memory_alloc_bytes`, `lionsql_memory_sys_bytes`, `lionsql_memory_heap_alloc_bytes`, and `lionsql_goroutines`.
  - Added atomic execution plan cache hit/miss tracking (`lionsql_plan_cache_hits_total`, `lionsql_plan_cache_misses_total`) in `PlanCache` (`pkg/proc/cache.go`).
  - Added test coverage in `pkg/api/metrics_test.go` verifying the exposition of all system and plan cache metrics.
- **Transaction Savepoints & Partial Rollback (`pkg/storage`, `pkg/api`)**:
  - Implemented multi-transaction savepoint support via `SAVE TRANSACTION <name>`, `SAVE TRAN <name>`, and `SAVEPOINT <name>`.
  - Implemented partial rollback to savepoints via `ROLLBACK TRANSACTION <name>`, `ROLLBACK TRAN <name>`, and `ROLLBACK TO [SAVEPOINT] <name>`.
  - Implemented ANSI SQL / PostgreSQL standard `RELEASE SAVEPOINT <name>` (and `RELEASE <name>`) across In-Memory OCC (`memengine`) and WiredTiger (`wt_engine`) storage engines.
  - Added `ReleaseSavepoint` contract to `storage.Transaction` interface.
  - Expanded test suite in `pkg/api/savepoint_test.go` covering basic, nested, error cases, stored procedures, and savepoint releases with 100% pass rate.
- **DML OUTPUT Clause Support (`pkg/proc`, `pkg/api`)**:
  - Implemented T-SQL `OUTPUT` clause for `INSERT`, `UPDATE`, and `DELETE` statements (`OUTPUT INSERTED.*`, `OUTPUT DELETED.*`, explicit column projections, and aliases).
  - Added `TokenOutput` token and AST extensions (`OutputCols []string` in `InsertStmt`, `UpdateStmt`, `DeleteStmt`).
  - Implemented `parseOutputClause()` and `evaluateOutputRow()` to project before/after row states into client query result sets (`QueryResult.Columns` and `QueryResult.Rows`).
  - Added arithmetic expression evaluation (`evalUpdateExpr`) in `UPDATE` statements (e.g., `SET price = price + 10`).
  - Added comprehensive integration test suite in `pkg/api/output_clause_test.go` covering single-row, multi-row, DELETED/INSERTED aliases, and wildcard projections across INSERT, UPDATE, and DELETE.
- **Native T-SQL VDBE Bytecode TPC-C Benchmark Driver (`cmd/tpcc_vdbe`)**:
  - Implemented standalone TPC-C benchmark binary (`cmd/tpcc_vdbe/main.go`) modeling all 9 TPC-C relational tables and transactions as precompiled Microsoft T-SQL stored procedures (`TPCC_NewOrderHeader`, `TPCC_NewOrderLine`, `TPCC_Payment`, `TPCC_OrderStatus`, `TPCC_Delivery`, `TPCC_StockLevel`).
  - Precompiles all T-SQL procedures into VDBE bytecode (`*proc.Program`), registers them into `runtime.PlanCache`, and executes concurrent client worker transactions strictly through the virtual machine runtime (`executor.Run`) with zero AST/graph scan overhead.
  - Supports both In-Memory OCC and WiredTiger storage engines, producing real-time tpmC, TPS, P50/P95/P99 latency distribution, and serialization conflict statistics.
- **RBAC Authorization & Permission Engine (`pkg/auth`, `pkg/api`)**:
  - Implemented SQL Server / ANSI SQL role-based access control with `Login`, `User`, `Privilege` (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `ALL`), and `GrantType` (`GRANT`, `DENY`).
  - Added support for `CREATE LOGIN <name> WITH PASSWORD = '...'`, `ALTER LOGIN <name> WITH PASSWORD = '...'`, and `DROP LOGIN [IF EXISTS] <name>`.
  - Added support for `CREATE USER <name> [FOR LOGIN <login>]` and `DROP USER [IF EXISTS] <name>`.
  - Added support for `GRANT <privilege> ON [TABLE] <tbl> TO <user>`, `REVOKE <privilege> ON [TABLE] <tbl> FROM <user>`, and `DENY <privilege> ON [TABLE] <tbl> TO <user>` with explicit DENY taking precedence.
  - Implemented execution context switching via `SETUSER '<user>'` / `SETUSER` (revert) and `EXECUTE AS USER = '<user>'` / `REVERT`.
  - Integrated `checkTableAccess()` across all DML operations (`SELECT`, `INSERT`, `UPDATE`, `DELETE`), ensuring unprivileged principals are prevented from accessing unauthorized relations.
  - Built-in administrator bypass for `sa`, `admin`, and `dbo` principals.
  - Added unit test suite in `pkg/auth/rbac_test.go` and full end-to-end integration test suite in `pkg/api/rbac_test.go`.
- **Session Query Timeout & Automatic Timeout Interrupt (`pkg/api`)**:
  - Implemented automatic query timeout enforcement in `ExecuteSessionSQLContext` (`pkg/api/sqlexec.go`) supporting session configuration variables `SET QUERY_TIMEOUT = <ms>` (and `SET statement_timeout = '<duration>'`).
  - Added HTTP REST API timeout support in `POST /api/sql`: accepts JSON field `timeout_ms`, HTTP request header `X-Query-Timeout`, and query parameter `?timeout_ms=<ms>`.
  - Propagates `context.WithTimeout` automatically across Volcano execution operators and VDBE VM instruction loops, terminating long-running queries gracefully with `canceling statement due to statement timeout` (Rule 34).
  - Added unit test suite in `pkg/api/query_cancel_test.go` covering session timeouts, timeout disabling via 0, statement_timeout aliases, and HTTP payload/header timeouts.
- **Multi-Table Chained Joins & CBO Join Tree Optimization (`pkg/planner`, `pkg/api`)**:
  - Implemented Cost-Based Join Reordering in `BuildMultiJoinPlanWithStats` (`pkg/planner/planner.go`) for chained multi-table queries (3+ tables).
  - Evaluates all valid permutations of contiguous INNER joins, filtering out disconnected cartesian products via `checkAndOrientJoinKeys` and automatic left/right key orientation.
  - Computes TotalCost for each candidate left-deep join tree (Hash Join vs Index Nested Loop Join) to select the execution order that minimizes intermediate cardinality and probe costs.
  - Added unit test suite in `pkg/planner/cbo_join_test.go` verifying that smaller tables and indexed relations are prioritized in join ordering.
  - Added end-to-end integration test suite in `pkg/api/cbo_join_test.go` verifying 3-table and 4-table chained joins (`customers` -> `orders` -> `order_items` -> `products`) with projection, WHERE filters, and EXPLAIN validation.
- **Composite Index Multi-Column Prefix Scan**: Added FindBestCompositeIndex, execIndexSelect, parseEqualityConditions, buildCompositePrefixKey for AND-predicate composite index prefix scans in pkg/storage, pkg/planner, pkg/api. Fires before VDBE for equality-only WHERE clauses. Tested with 7 sub-tests in composite_index_test.go.
- **Unified JSON Configuration & Deterministic Precedence Hierarchy (`pkg/config`, `cmd/server`)**:
  - Implemented `pkg/config` defining structured, hierarchical configuration schema (`Server`, `Network`, `Auth`, `Resources`, `Logging`) with production-ready zero-config defaults (`DefaultConfig()`).
  - Added atomic config persistence via swap files and atomic rename (`WriteToFileAtomic`) complying with Rule 1 and Rule 89.
  - Implemented auto-discovery mechanism (`AutoDiscoverConfig`): explicit path > `./lionsql.json` > `<dir>/lionsql.json` > Built-in Defaults (Rule 82, 87).
  - Enforced deterministic configuration precedence hierarchy (`flag.Visit`): CLI Arguments > JSON Config File > Built-in Defaults (Rule 88).
  - Added CLI flags `--config <path>` to specify custom config files and `--init-config [path]` to generate formatted `lionsql.json` template.
  - Added comprehensive unit tests in `pkg/config/config_test.go` covering defaults, partial overlays, atomic writes, and discovery paths.
- **Slow Query JSON Log File Persistence & General Query Log Engine (`pkg/api`, `cmd/server`)**:
  - Implemented `RotatingLogWriter` (`pkg/api/rotating_logger.go`) providing thread-safe append-only file persistence with bounded size caps (default 5MB) and automatic log rotation (`.1` backup) complying with Rule 40 and Rule 5.
  - Enhanced `SlowQueryLogger` (`pkg/api/slow_query.go`) with JSON Lines file output (`SetLogFile`), supporting structured JSON logging (`id`, `query`, `duration_ms`, `rows_affected`, `session_id`, `error`, `timestamp`).
  - Added new `GeneralLogger` (`pkg/api/general_log.go`) component capturing all executed queries in a bounded in-memory circular history buffer and optional rotating JSON Lines disk file output.
  - Integrated both loggers with `ExecuteSessionSQLContext` in `pkg/api/sqlexec.go` for zero-overhead, deferred query diagnostics.
  - Added RESTful management endpoints: `POST /api/slow-queries/logfile`, `GET /api/general-queries`, `POST /api/general-queries/toggle`, `POST /api/general-queries/logfile`, and `DELETE /api/general-queries`.
  - Added CLI flags to `cmd/server/main.go`: `--slow-log`, `--slow-threshold-ms`, and `--general-log`.
  - Added unit test suite in `pkg/api/log_test.go` verifying file output, size-based rotation, endpoint handling, and race-free concurrent recording.
- **T-SQL & ANSI SQL `MERGE INTO` Execution Engine (`pkg/api/sqlexec_merge.go`, `pkg/api/sqlexec.go`)**:
  - Implemented full-spec `MERGE INTO <target> USING <source> ON <condition>` query engine supporting table sources, inline `(VALUES ...)` sources, and `(SELECT ...)` subqueries.
  - Added support for multiple ordered `WHEN MATCHED [AND <cond>] THEN UPDATE / DELETE` clauses.
  - Added support for `WHEN NOT MATCHED [BY TARGET] [AND <cond>] THEN INSERT` and `WHEN NOT MATCHED BY SOURCE [AND <cond>] THEN UPDATE / DELETE` clauses.
  - Integrated with transaction context for atomic execution, `@@ROWCOUNT` reflection, and storage abstraction across In-Memory OCC and WiredTiger.
  - Added comprehensive verification test suite in `pkg/api/merge_test.go`.
- **`CREATE VIEW` / `DROP VIEW` DDL Support (`pkg/catalog`, `pkg/api`)**:
  - Added `catalog.View` struct (`Name`, `Query`, `CreatedAt`, `UpdatedAt`) with sentinel errors `ErrViewExists` / `ErrViewNotFound` in `pkg/catalog/catalog.go`.
  - Implemented `CreateView`, `CreateOrReplaceView`, `GetView`, `DropView`, `ListViews` CRUD methods with mutex-safe access.
  - Added `execCreateView` supporting `CREATE VIEW`, `CREATE OR REPLACE VIEW`, and `CREATE VIEW IF NOT EXISTS` syntaxes.
  - Added `execDropView` supporting `DROP VIEW` and `DROP VIEW IF EXISTS` syntaxes.
  - Added dispatcher routes in `pkg/api/sqlexec.go` for `CREATE VIEW` / `CREATE OR REPLACE VIEW` / `DROP VIEW`.
  - Implemented transparent view expansion via `rewriteViewsAsCTE()`: any `SELECT` referencing a registered view is automatically prepended with a `WITH <viewname> AS (<view body>)` CTE clause before execution. This leverages the existing CTE engine for full query pushdown (WHERE, ORDER BY, GROUP BY, aggregations).
  - Added `pkg/api/view_test.go` with 7 sub-test cases: basic select, additional WHERE, CREATE OR REPLACE, DROP, IF NOT EXISTS variants, and aggregation views.
- **`SELECT DISTINCT` Deduplication (`pkg/proc`, `pkg/planner`, `pkg/api`)**:
  - Added `TokenDistinct` keyword token and registered `"DISTINCT"` in the primary Pratt parser's keyword map (`pkg/proc/token.go`).
  - Extended `GeneralSelectStmt` AST node with `Distinct bool` field; `parseGeneralSelect()` detects and sets the flag on `TokenDistinct` (`pkg/proc/ast.go`, `pkg/proc/parser.go`).
  - Propagated `Distinct bool` through `planner.QueryInfo`; `FromAST()` and `ParseSelect()` both carry the flag (`pkg/planner/planner.go`).
  - Implemented `deduplicateRows()` helper using sorted-key serialization with `\x00` separators and `storage.Value.IsNull` aware NULL normalization (`pkg/api/sqlexec_dml.go`).
  - Applied deduplication at three execution paths: Volcano engine (post row-collection), VDBE fast-path result (pre-return in `execSelect`).
  - Added `pkg/api/distinct_test.go` with 5 sub-test cases: single-column, multi-column, WHERE filter, non-distinct baseline, and NULL deduplication.
- **Transaction Savepoints & Partial Rollback (`pkg/storage`, `pkg/api`, `pkg/proc`)**:
  - Added `Savepoint(name string) error` and `RollbackTo(name string) error` to core `storage.Transaction` interface.
  - Implemented copy-on-write snapshot delta savepoints in `pkg/storage/memengine` with full stack invalidation and state restoration.
  - Implemented reverse undo-log record unwinding in `pkg/storage/wiredtiger` for precise partial rollback without aborting the parent transaction.
  - Added text dispatchers in `pkg/api/sqlexec.go` for `SAVE TRANSACTION <name>`, `SAVE TRAN <name>`, `SAVEPOINT <name>`, and `ROLLBACK TRANSACTION <name>`, `ROLLBACK TRAN <name>`, `ROLLBACK TO SAVEPOINT <name>`.
  - Extended procedural lexer, parser, AST, compiler, and VDBE virtual machine with `OpTxSavepoint` and `OpTxRollbackTo` to support nested savepoints inside T-SQL stored procedures.
  - Added comprehensive verification test suite in `pkg/api/savepoint_test.go`.
## [0.8.0]

### Refactored
- **Monolithic SQL Execution Engine Decomposition (`pkg/api`)**:
  - Decomposed the 6,379-line monolith `pkg/api/sqlexec.go` into 5 high-cohesion, domain-specific modules without altering public interfaces or runtime semantics:
    - `pkg/api/sqlexec.go` (848 lines): Core session orchestrator, transaction lifecycle management, and statement dispatchers.
    - `pkg/api/sqlexec_ddl.go` (618 lines): Schema management (`CREATE`/`DROP`/`TRUNCATE TABLE`, `CREATE`/`DROP INDEX`, `CREATE PROCEDURE`/`FUNCTION`/`TRIGGER`) and DDL definition parsers.
    - `pkg/api/sqlexec_dml.go` (3,706 lines): Query execution engine (`INSERT`, `EXPLAIN [ANALYZE]`, CTEs, Volcano execution, scalar evaluation, VDBE fast-path, set operations, joins, aggregations, `UPDATE`, `DELETE`, and where predicate evaluation).
    - `pkg/api/sqlexec_procs.go` (573 lines): T-SQL procedure invocation, parameter binding, and built-in system stored procedures (`sp_who`, `sp_help`, `sp_spaceused`, `sp_tables`, `sp_columns`, etc.).
    - `pkg/api/sqlexec_dbcc.go` (685 lines): Database console commands (`DBCC CHECKDB`, `DBCC DROPCLEANBUFFERS`, `DBCC USEROPTIONS`) and virtual system catalog engine (`INFORMATION_SCHEMA`, `sys.*`).

### Added
- **Dynamic SQL Execution Engine & `sp_executesql` Text Dispatcher (`pkg/api`, `pkg/proc`)**:
  - Implemented T-SQL dynamic query execution supporting `EXEC ('...')`, `EXEC (N'...')`, and session variable evaluations `EXEC (@sqlVar)`.
  - Added native `sp_executesql` text dispatcher in `pkg/api/sqlexec_procs.go` supporting Unicode literals (`N'...'`), parameter declarations (`@params`), and named (`@id = 1`) or positional parameter bindings.
  - Extended Pratt parser (`pkg/proc/parser.go`), AST (`pkg/proc/ast.go`), bytecode compiler (`pkg/proc/compiler.go`), and VDBE runtime VM (`pkg/proc/runtime/vm.go`) with `OpExecDynamic` to execute dynamic SQL inside stored procedure bodies.
  - Added comprehensive test suite in `pkg/api/dynamic_sql_test.go`.
- **Official Universal JavaScript & TypeScript Client SDK (`sdk/lionsql.js`, `sdk/lionsql.d.ts`, `pkg/api/sdk.go`)**:
  - Implemented zero-dependency, PocketBase-compatible client library for Web Browsers, Node.js (v18+), Bun, Deno, and Edge environments.
  - Provided `client.collection(table)` services with full CRUD methods (`getList`, `getFullList`, `getOne`, `create`, `update`, `replace`, `delete`) and automatic URL query filter building.
  - Implemented real-time Server-Sent Events (SSE) subscriptions via `client.collection(table).subscribe(callback)` with automatic reconnection and topic dispatching.
  - Added native T-SQL stored procedure execution (`client.exec(proc, params)`) and arbitrary SQL querying (`client.query(sql)`).
  - Included full TypeScript definitions (`sdk/lionsql.d.ts`) supporting generic record typing (`client.collection<Customer>('customers')`).
  - Embedded static distribution into standalone binary and exposed via `GET /sdk/lionsql.js` and `GET /sdk/lionsql.d.ts`.
- **Embedded Cron Jobs Scheduler (`pkg/api/cron.go`, `pkg/api/cron_handlers.go`, `pkg/ui`)**:
  - Implemented embedded, non-blocking `CronScheduler` supporting standard 5-field cron syntax (`min hour dom month dow`, step syntax `*/N`, ranges, and lists).
  - Automated recurring background execution of arbitrary SQL queries and T-SQL stored procedures inside isolated ambient database sessions.
  - Tracked execution audit history, including last run timestamp, execution latency in milliseconds, success/error status, and error diagnostics.
  - Exposed RESTful management APIs: `GET /api/crons`, `POST /api/crons`, `PUT /api/crons/:id`, `DELETE /api/crons/:id`, and `POST /api/crons/:id/run` (manual trigger).
  - Added dedicated Web Studio dashboard tab for registering cron jobs, executing one-click manual test runs, and inspecting live job status.
  - Added comprehensive test suite in `pkg/api/cron_test.go`.
- **Embedded SMTP Mailer & Transactional Email Templates (`pkg/api/mailer.go`, `pkg/api/mailer_handlers.go`, `pkg/ui`)**:
  - Implemented lightweight SMTP client using Go standard library `net/smtp` with TLS/STARTTLS support and password masking.
  - Built-in customizable HTML transactional email templates for `welcome`, `password_reset`, and `verification` workflows.
  - Implemented thread-safe in-memory email delivery audit ring buffer (capped at 100 entries).
  - Exposed RESTful management APIs: `GET /api/settings/mail`, `PUT /api/settings/mail`, `POST /api/settings/mail/test`, `GET /api/mail/logs`, `DELETE /api/mail/logs`, and `POST /api/auth/request-password-reset`.
  - Added Web Studio dashboard view for SMTP credential configuration, test email dispatches, and delivery audit log inspection.
  - Added comprehensive test suite in `pkg/api/mailer_test.go`.
- **Asynchronous Webhook Dispatch Engine (`pkg/api`, `pkg/ui`)**:
  - Implemented asynchronous, non-blocking `WebhookDispatcher` (`pkg/api/webhook.go`) with dedicated goroutine worker pool (4 workers), bounded event channel queue (capacity 1024), and exponential backoff retry policy (up to 3 retries: 1s, 2s, 4s).
  - Added cryptographic payload integrity verification via HMAC-SHA256 signatures transmitted in the `X-LionSQL-Signature: sha256=<hex>` request header.
  - Implemented thread-safe, in-memory delivery audit ring buffer (capped at 200 entries) recording delivery attempt count, HTTP response status code, round-trip latency in milliseconds, and execution timestamps.
  - Integrated with `RealtimeBroker`, automatically fanning out database mutation events (`create`, `update`, `delete`, `import`) to matching webhooks across all database tables or specific table scopes.
  - Exposed RESTful management endpoints (`pkg/api/webhook_handlers.go`): `GET /api/webhooks`, `POST /api/webhooks`, `DELETE /api/webhooks/:id`, `POST /api/webhooks/:id/test` (instant test ping delivery), `GET /api/webhooks/deliveries`, and `DELETE /api/webhooks/deliveries`.
  - Added Web Studio UI dashboard tab for managing webhooks, triggering interactive test pings, inspecting live delivery status logs, and clearing delivery history.
  - Added comprehensive test suite in `pkg/api/webhook_test.go` covering CRUD, HMAC-SHA256 signature verification, exponential backoff retries, and automatic triggers on REST mutations.
- **Declarative Row-Level Security (RLS) Engine (`pkg/api`, `pkg/ui`)**:
  - Implemented declarative, PocketBase-style Row-Level Security engine (`pkg/api/rls.go`) configuring granular access control rules per table for `list`, `view`, `create`, `update`, and `delete` operations.
  - Added dynamic authentication context token substitution supporting `@auth.id`, `@auth.role`, `@auth.email`, and `@auth.tenant_id` resolved from JWT claims, API keys, or security request headers (`X-User-ID`, `X-Tenant-ID`).
  - Integrated `list_rule` filtering into `handleGetRecords` (`GET /api/records/:table` and `/api/tables/:table/records`) via forced conjunctive query rewriting: `(list_rule) && (client_filter)`, preventing unauthorized client bypasses.
  - Enforced strict authorization checks on single record fetches (`view_rule`), record insertions (`create_rule`), record updates (`update_rule`), and deletions (`delete_rule`), returning HTTP 403 Forbidden with clear error diagnostics upon authorization failure.
  - Exposed RESTful table security management endpoints (`pkg/api/rls_handlers.go`): `GET /api/tables/:name/rules`, `PUT /api/tables/:name/rules`, and `DELETE /api/tables/:name/rules`.
  - Added Web Studio UI subview in Tables & Records Studio for interactive RLS rule editing, including one-click presets for *Owner Only*, *Admin Only*, *Public Read / Auth Write*, and *Multi-Tenant Isolation*.
  - Added comprehensive test suite in `pkg/api/rls_test.go` verifying token substitution, multi-user row isolation, spoofing prevention, ownership enforcement, and rule management APIs.
- **Global Bytecode PlanCache Integration for PGWire & TDS Extended Query Protocols (`pkg/wire`, `pkg/wire/tds`, `pkg/api`, `cmd/server`)**:
  - Integrated PostgreSQL Wire Protocol (`pkg/wire/server.go`) Extended Query Protocol (`Parse`, `Bind`, `Execute`) with `*runtime.PlanCache`, converting parameter placeholders (`$1`, `$2` -> `@p1`, `@p2`) and executing compiled VDBE bytecode programs via thread-safe plan cache lookup (`GetOrCompile`) instead of recompiling per request.
  - Connected Microsoft SQL Server TDS 7.x/8.0 Protocol (`pkg/wire/tds/server.go`) RPC Prepare routines (`sp_prepare`, `sp_prepexec`) to `*runtime.PlanCache` for instant zero-recompile reuse of precompiled bytecode across sessions and client connections.
  - Unified bytecode plan caching across server boundaries by exposing `BytecodePlanCache()` on `api.Server` and injecting the shared plan cache into `wire.Server` and `tds.Server` in `cmd/server/main.go` via `NewServerWithCache` and `SetBytecodePlanCache`.
  - Added unit test suites verifying sub-microsecond PlanCache hits for identical queries and cross-server PlanCache sharing in `pkg/wire/server_test.go` (`TestWire_CompileQueryToProgram`, `TestWire_PlanCacheSharing`) and `pkg/wire/tds/server_test.go` (`TestTDS_PlanCacheSharing`).
- **Auto-Generated RESTful Records CRUD API (`pkg/api`, `pkg/ui`)**:
  - Implemented automatic, zero-code RESTful endpoints for database tables: `GET /api/records/:table` (list/filter/sort/pagination), `GET /api/records/:table/:id` (single record lookup), `POST /api/records/:table` (create record), `PATCH /api/records/:table/:id` and `PUT /api/records/:table/:id` (update record), and `DELETE /api/records/:table/:id` (delete record).
  - Implemented comprehensive URL query filter engine (`pkg/api/records_filter.go`) supporting comparison operators (`>`, `<`, `>=`, `<=`, `=`, `!=`, `<>`), string containment (`~`, `!~`), SQL pattern matching (`LIKE`), null checks (`IS NULL`, `IS NOT NULL`), logical combinators (`&&`, `||`, `,`, `AND`, `OR`), and nested parentheses `(...)`.
  - Added support for multi-column sorting (`sort=-id,balance` or `sort=-balance`), pagination (`page=1&limit=20` and `perPage`), and query parameter shorthand filters.
  - Formatted JSON responses with native data types (numbers, booleans, strings, nulls) matching table schemas and providing both `records`/`items` and `perPage`/`limit` for out-of-the-box frontend compatibility (React, Vue, Flutter, iOS, Android).
  - Integrated table-level API key role authentication (`read_only`, `read_write`, `admin`) and real-time SSE broadcast triggers across create, update, and delete actions.
  - Updated Web Studio developer API modal code snippets (cURL, JavaScript, Python, Go) to directly showcase the `/api/records/:table` endpoints.
  - Added test suites in `pkg/api/records_filter_test.go` and `pkg/api/records_api_test.go`.
- **Stored Procedure Duplicate Name Verification & `ALTER / CREATE OR ALTER` Support (`pkg/catalog`, `pkg/proc`, `pkg/api`)**:
  - Implemented strict duplicate object verification in `catalog.Catalog` (`CreateProcedure`, `CreateFunction`, `CreateTrigger`) returning `catalog.ErrProcExists` / HTTP 409 Conflict when a procedure with the same name already exists.
  - Added case-insensitive identifier normalization and indexing (`cleanCatalogIdent` and lowercase keying) in `catalog.Catalog`, eliminating accidental duplicate registrations caused by casing variations (e.g. `TransferFunds` vs `transferfunds`).
  - Extended T-SQL parser and AST (`pkg/proc/token.go`, `pkg/proc/ast.go`, `pkg/proc/parser.go`) to support `ALTER PROCEDURE` and `CREATE OR ALTER PROCEDURE` (SQL Server 2016+ standard).
  - Updated Web Studio / REST API (`POST /api/procs`) and SQL executor (`execCreateProcedure` in `pkg/api/sqlexec.go`) to reject duplicate `CREATE PROCEDURE` calls with HTTP 409 Conflict and clear user guidance to use `CREATE OR ALTER PROCEDURE` or `ALTER PROCEDURE`.
  - Added comprehensive test suites across `pkg/catalog/catalog_test.go`, `pkg/api/server_test.go`, and `pkg/api/sqlexec_test.go`.
- **Query Optimizer Predicate Pushdown (`pkg/planner`, `pkg/execution`, `pkg/api`)**:
  - Implemented conjunct extraction and table attribution analysis (`pkg/planner/pushdown.go`), partitioning complex `WHERE` conditions by table and join eligibility while distinguishing qualified and unqualified column identifiers.
  - Pushed down single-table filter predicates to primary and joined table scan iterators before join operations, reducing the number of candidate tuples streamed into `HashJoinIterator` and `IndexNestedLoopJoinIterator`.
  - Enabled secondary index matching (`NodeIndexScan` and `NodeIndexOnlyScan`) for primary tables within multi-table JOIN queries whenever pushed-down predicates reference an indexed column, eliminating full table scans.
  - Maintained SQL standards-compliant outer join semantics by safely evaluating non-null-rejecting outer table predicates post-join rather than pushing them down into nullable join branches.
  - Enhanced `pkg/execution/builder.go` to evaluate attached `FilterCond` conditions on `NodeIndexScan` and `NodeIndexOnlyScan` via `FilterIterator`.
  - Added unit test suite in `pkg/planner/pushdown_test.go` and integration test suite in `pkg/api/pushdown_test.go`.
- **Query Optimizer Constant Folding & Boolean Simplification (`pkg/proc`, `pkg/planner`, `pkg/execution`, `pkg/api`)**:
  - Implemented AST constant folding engine (`pkg/proc/fold.go`) evaluating compile-time literal arithmetic (`3000 * 12` -> `36000`), string operations, comparisons (`10 > 5` -> `true`), null checks (`IS NULL` / `IS NOT NULL`), and deterministic built-in scalar functions (`UPPER`, `LOWER`, `LEN`, `ABS`, `CONCAT`, etc.) ahead of execution.
  - Implemented logical boolean simplifications (`true AND X` -> `X`, `false AND X` -> `false`, `false OR X` -> `X`, `true OR X` -> `true`, double negation `NOT (NOT X)` -> `X`), eliminating redundant evaluation branches.
  - Preserved non-deterministic functions (`GETDATE()`, `NOW()`, `RAND()`, `NEWID()`, `UUID()`) from compile-time folding.
  - Integrated constant folding into Cost-Based Optimizer (`pkg/planner/planner.go`) in `FromAST` and `ParseSelect`, exposing folded literal predicates to secondary index scanning and CBO selectivity estimation.
  - Integrated constant folding into Volcano `FilterIterator` (`pkg/execution/filter.go`), short-circuiting tautological conditions (`1=1`, `true`) and unsatisfiable conditions (`1=0`, `false`) in \(O(1)\).
  - Added unit test suite in `pkg/proc/fold_test.go` and end-to-end integration test suite in `pkg/api/constant_folding_test.go`.

## [0.7.0]

### Added
- **Prometheus Metrics (`/metrics`) & Slow Query Logging (`pkg/api`)**:
  - Implemented lock-free atomic `MetricsCollector` (`pkg/api/metrics.go`) tracking high-frequency server diagnostics, including uptime, active connections, query counts by command type (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE TABLE`, etc.), query error counts, cumulative query duration, slow query counts, transaction commit/rollback counts, and total table counts.
  - Implemented standard Prometheus text exposition format (version 0.0.4) on `GET /metrics`.
  - Implemented bounded circular in-memory `SlowQueryLogger` (`pkg/api/slow_query.go`) with configurable threshold (`DefaultSlowQueryThreshold = 50ms`, capped at 200 entries) capturing query text, execution duration (microsecond & millisecond precision), timestamp, rows affected, session ID, and error messages.
  - Added REST management endpoints: `GET /api/slow-queries` (retrieving recent slow queries with limit support), `POST /api/slow-queries/threshold` (dynamically adjusting the slow query threshold), and `DELETE /api/slow-queries` (clearing recorded entries).
  - Added comprehensive unit, integration, and race-free concurrency test suite (`pkg/api/metrics_test.go`).
- **Correlated Subquery Decorrelation & Semi/Anti-Join Optimization (`pkg/planner`, `pkg/execution`, `pkg/proc`, `pkg/api`)**:
  - Implemented automatic query decorrelation in `pkg/planner/decorrelate.go` converting correlated `WHERE EXISTS (SELECT ...)`, `WHERE NOT EXISTS (SELECT ...)`, `WHERE col IN (SELECT ...)`, and `WHERE col NOT IN (SELECT ...)` subqueries into relational `SEMI` and `ANTI` joins.
  - Implemented paren-aware, string-literal-safe SQL clause splitter (`splitSelectClauses`), preventing subquery `FROM` and `WHERE` clauses from polluting outer query planning.
  - Extended Volcano `HashJoinIterator` (`pkg/execution/join.go`) to support `SEMI` join (emitting outer tuple on first inner hash match and skipping duplicates) and `ANTI` join (emitting outer tuple only when no inner match exists), projecting only the outer schema.
  - Extended Volcano `IndexNestedLoopJoinIterator` (`pkg/execution/join.go`) to support `SEMI` and `ANTI` joins with inner index scans and attached filter predicates (`FilterCond`).
  - Extended Cost-Based Optimizer (`BuildMultiJoinPlanWithStats` in `pkg/planner/planner.go`) with cost modeling and row estimation for `SEMI` and `ANTI` joins.
  - Extended Pratt parser (`pkg/proc/parser.go`) and AST (`pkg/proc/ast.go`) to parse full `GeneralSelectStmt` for scalar subqueries and `IN (SELECT ...)`.
  - Added type-aware scalar and IN-list value formatting (`formatSubqueryVal` in `pkg/api/sqlexec.go`), preventing quoted numeric literals from breaking type comparisons.
  - Added comprehensive test suites across `pkg/planner/decorrelate_test.go` and `pkg/api/subquery_test.go` verifying correlated `EXISTS`, `NOT EXISTS`, `IN`, `NOT IN`, scalar subqueries, and `EXPLAIN` verification of `SEMI` join plans.
- **Cooperative Query Cancellation & Execution Interruption (`pkg/proc/runtime`, `pkg/api`, `pkg/wire`)**:
  - Implemented cooperative cancellation via `context.Context` (`QueryCtx` and `CancelFunc`) tracked on `ClientSession`, returning standard `"canceling statement due to user request"` error on cancel.
  - Added non-blocking cancellation check (`f.ctx.Done()`) in VDBE virtual machine instruction loop (`VMFrame.Execute`), instantly aborting long-running procedural loops, and added `ExecuteInTxContext` and `RunContext` on `proc.Executor`.
  - Integrated `QueryCtx` into Volcano execution engine (`execVolcanoSelect`, `execSetOpSelect`, and `execVDBESelect`), terminating active iterators without data corruption.
  - Implemented T-SQL `KILL <spid> [WITH STATUSONLY]` statement execution, terminating target session queries by SPID.
  - Implemented PostgreSQL cancellation functions `SELECT pg_cancel_backend(<pid>)` and `SELECT pg_terminate_backend(<pid>)`, with dynamic PID resolution via `pg_backend_pid()` and `@@SPID`.
  - Extended PostgreSQL wire protocol server to handle `CancelRequest` (code 80877102) gracefully, canceling in-flight statement execution while keeping the client TCP connection alive.
  - Added REST HTTP management endpoints: `POST /api/query/cancel` (canceling by `session_id` or `spid`) and `GET /api/queries/active` (listing running queries with SPID, start time, and cancel status).
  - Added hermetic, race-free integration test suite (`pkg/api/query_cancel_test.go`) verifying query cancellation across context, KILL, PostgreSQL functions, and HTTP endpoints deterministically.
- **Relational Set Operations Planning & Execution: `UNION`, `UNION ALL`, `INTERSECT`, `EXCEPT` / `MINUS` (`pkg/execution`, `pkg/planner`, `pkg/api`)**:
  - Implemented Volcano-style `SetOpIterator` (`pkg/execution/setop.go`) supporting ANSI SQL set operations: `UNION`, `UNION ALL`, `INTERSECT`, and `EXCEPT` (including Oracle/PostgreSQL `MINUS` alias).
  - Implemented zero-allocation streaming pipeline for `UNION ALL`, pulling tuples sequentially from left and right iterators without buffering.
  - Implemented type-safe row encoding and deduplication hash set for `UNION`, `INTERSECT`, and `EXCEPT`, ensuring distinct set semantics.
  - Added `SliceIterator` in `pkg/execution/setop.go` for seamless integration of subqueries, scalar selects, and materialized results into Volcano execution pipelines.
  - Added `NodeSetOp` (`"SetOp"`) to `planner.NodeType` and `SetOp` field to `planner.PlanNode`.
  - Implemented robust top-level set operation scanner `FindTopLevelSetOps` (`pkg/planner/setop.go`), ignoring keywords inside parentheses or string literals, and isolating trailing `ORDER BY` and `LIMIT` clauses.
  - Implemented `BuildSetOpPlan` in `pkg/planner/setop.go`, creating left-deep SetOp plan trees with cost estimation and outer sort/limit wrapping.
  - Integrated `NodeSetOp` into Volcano physical plan compiler `BuildExecutorWithOptions` (`pkg/execution/builder.go`).
  - Extended `execSelect` and `execExplain` (`pkg/api/sqlexec.go`) to execute set queries via Volcano iterators and format set operation EXPLAIN plan trees.
  - Added comprehensive test suites across `pkg/execution/setop_test.go`, `pkg/planner/setop_test.go`, and `pkg/api/sqlexec_test.go`.
- **Exact Fixed-Point `DECIMAL`, `NUMERIC`, `MONEY` Data Types (`pkg/storage`, `pkg/api`, `pkg/execution`, `pkg/wire`)**:
  - Implemented exact fixed-point `storage.Decimal` struct backed by arbitrary-precision `math/big.Int` with integer scale, providing zero-drift arithmetic (`Add`, `Sub`, `Mul`, `Div` with half-up rounding), scale alignment, and exact comparisons (`Cmp`, `Equal`).
  - Added `storage.TypeDecimal` (`DataType = 7`) to storage engine and value system, with `Precision` and `Scale` metadata in `storage.Column`.
  - Added `DecimalValue`, `DecimalValueFromDecimal`, and `(v Value) Decimal()` to `storage.Value`, updating value comparisons and equality to operate directly on `storage.Decimal`.
  - Added `DECIMAL(precision, scale)`, `NUMERIC(precision, scale)`, and `MONEY` (default precision 19, scale 4) data type parsing to `execCreateTable` in `pkg/api/sqlexec.go`.
  - Extended in-memory OCC storage engine (`memengine`) insert and update record validation to convert and rescale fixed-point values defensively, and updated primary key parsing for decimal PK columns.
  - Extended Volcano execution engine (`AggregateIterator`, `formatStorageVal`, `CompareValues`, `tryDecimal`) to accumulate exact decimal sums and averages (`SUM(decimal_col)`, `AVG(decimal_col)`), eliminating IEEE-754 binary floating-point round-off errors (`0.10 + 0.20 + 100.55 = 100.85`).
  - Extended fallback query aggregation in `pkg/api/sqlexec.go:execAggregate` to preserve exact decimal precision.
  - Mapped `storage.TypeDecimal` to PostgreSQL wire protocol OID 1700 (`NUMERIC`).
  - Added comprehensive unit and end-to-end tests in `pkg/storage/decimal_test.go` and `pkg/api/sqlexec_test.go` (`TestSQLExecutor_DecimalAndFixedPoint`).
- **Multi-Table Chained Joins Planning & Execution (`pkg/planner`, `pkg/execution`, `pkg/api`, `pkg/proc/runtime`)**:
  - Extended `planner.QueryInfo` with `Joins []*JoinInfo` and implemented chained join extraction in AST walker (`FromAST`) and SQL parser fallback (`parseFromAndJoins`), parsing arbitrary sequences of chained `JOIN ... ON ...` clauses with aliases.
  - Implemented left-deep join tree generator `BuildMultiJoinPlanWithStats` in `pkg/planner/planner.go`, evaluating Cost-Based Optimization (CBO) decisions (Hash Join vs Index Nested Loop Join) at each join stage based on table cardinality and secondary index availability.
  - Maintained 100% backward compatibility with `BuildJoinPlanWithStats` and `BuildJoinPlan` delegating to `BuildMultiJoinPlanWithStats`.
  - Added key orientation defensive normalization in Volcano `HashJoinIterator.Open()` (`pkg/execution/join.go`), automatically swapping join keys if the right child schema contains `leftKey` and not `rightKey`.
  - Extended `execVolcanoSelect` and `buildPlanForQuery` (`pkg/api/sqlexec.go`) to resolve schemas and catalog statistics for all joined relations in multi-join queries.
  - Extended VDBE execution VM `execRelQuery` (`pkg/proc/runtime/vm.go`) to resolve all joined table schemas and execute multi-join queries via Volcano execution iterators.
  - Added comprehensive unit and end-to-end tests across `planner_test.go`, `execution_test.go`, `sqlexec_test.go`, and `runtime_test.go` verifying 3-table and 4-table chained joins (INNER, LEFT, and mixed).
- **Microsoft SQL Server TDS 7.x/8.0 Wire Protocol SSL/TLS Encryption (`pkg/wire/tds`, `cmd/server`)**:
  - Implemented PRELOGIN encryption negotiation supporting `EncryptOff (0x00)`, `EncryptOn (0x01)`, `EncryptNotSup (0x02)`, and `EncryptReq (0x03)`.
  - Added `ParsePrelogin` parsing option headers and payloads defensively to extract client encryption requirements.
  - Implemented `TDSHandshakeConn` adapter encapsulating and decapsulating TLS handshake records within TDS `PacketPrelogin` (`0x12`) frames during TDS 7.x negotiation.
  - Implemented TDS 8.0 direct TLS handshake detection by inspecting the initial TLS record header byte (`0x16`).
  - Added `SetTLSConfig`, `SetRequireSSL`, `TLSConfig`, and `RequireSSL` methods to TDS `Server`.
  - Extended `cmd/server/main.go` to configure TLS encryption and `-require-ssl` enforcement symmetrically across both PostgreSQL and TDS wire protocols.
  - Added hermetic in-memory integration test suite (`pkg/wire/tds/tls_test.go`) covering TDS 7.x TLS handshake, TDS 8.0 direct TLS, `requireSSL` plaintext rejection, and concurrent encrypted connections under `-race`.
- **Covering Index Scan (Index-Only Scan) Optimization (`pkg/planner`, `pkg/execution`, `pkg/storage`, `pkg/proc/runtime`)**:
  - Implemented `NodeIndexOnlyScan` in Cost-Based Optimizer (`pkg/planner/planner.go`), detecting when all projected and filtered columns are covered by secondary index keys and table primary keys.
  - Adjusted CBO cost model for index-only scans, eliminating table heap page I/O (`RandomPageCost`) and tuple CPU evaluation overhead.
  - Added `IndexOnlyScan` to `storage.Transaction` interface, `memengine`, and `wiredtiger`.
  - Implemented zero-heap-read `IndexOnlyScan` in `memengine` using `SkipList.ScanRangeEntries()`, building projection records directly from index keys and primary keys without touching main table rows or acquiring table read locks.
  - Updated Volcano `IndexScanIterator` and `BuildExecutor` to execute index-only scans.
  - Dispatched `IndexOnlyScan` in VDBE `OpIndexScan` when `IndexScanData.IndexOnly` is set.
  - Added comprehensive unit and end-to-end tests across `planner_test.go`, `index_test.go`, and `execution_test.go`.
- **Dynamic Storage Engine Detection & Display in Web Studio (`pkg/storage`, `pkg/api`, `pkg/ui`)**:
  - Added `Type() string` contract to `storage.Engine` interface, returning the active storage engine backend name (`WiredTiger` or `In-Memory OCC`).
  - Implemented `Type()` across `memengine` and `wiredtiger` (including build stubs).
  - Extended `/api/health` HTTP endpoint to report the active storage engine backend type in JSON.
  - Updated Web Management Studio (`pkg/ui/dashboard.html`) to dynamically fetch and display the active storage engine in the sidebar footer.
- **Single-Instance Server Guard & Duplicate Process Prevention (`cmd/server`)**:
  - Implemented pre-flight PID verification preventing duplicate server instances from launching on the same data directory.
  - Automatically registered and cleaned up PID file for foreground processes, preventing lock contention with background daemons.
  - Intercepted storage lock collisions (`Resource busy`, `code 16`) to display a clear, user-friendly diagnostic message rather than dumping raw Cgo fcntl lock errors.
- **VDBE Secondary Index Opcode `OpIndexScan` & Search Optimization (`pkg/proc`, `pkg/proc/runtime`)**:
  - Introduced `OpIndexScan` opcode in `pkg/proc/bytecode.go` with `IndexScanData` metadata struct for secondary index point and range scans.
  - Implemented `OpIndexScan` in `pkg/proc/runtime/vm.go` leveraging `storage.Transaction.IndexScan` to scan records within bounds.
  - Implemented dynamic secondary index matching (`findMatchingIndex`) optimizing `scanCursorRows`, `execUpdateWhere`, and `execDeleteWhere` to automatically scan via secondary index when filter conditions match, avoiding full table scans.
  - Added unit test coverage in `pkg/proc/runtime/runtime_test.go` (`TestVM_OpIndexScan`).
- **Web Management Studio: Visual VDBE Bytecode Disassembler Viewer (`pkg/ui`)**:
  - Added dedicated "Explain Bytecode" toolbar action and quick template in SQL Query Studio (:8090).
  - Implemented rich visual VDBE assembly viewer with colorized syntax highlighting for opcodes, control flow jumps, registers, and operands.
  - Added program metadata badge and one-click clipboard copying for bytecode disassembly listings.
- **VDBE Engine Unification for Standalone DML (`pkg/proc`, `pkg/proc/runtime`, `pkg/api`, `pkg/wire`)**:
  - Unified standalone interactive `INSERT`, `UPDATE`, and `DELETE` queries to execute directly on the VDBE Virtual Machine (`proc.CompileStatement` ➔ `s.executor.ExecuteInTx`), achieving 100% single-engine execution parity across all DQL, DML, stored procedures, and triggers.
  - Added `CompileStatement` in `pkg/proc/compiler.go` and `GeneralInsertStmt` compiler support for single and multi-tuple `VALUES` clauses.
  - Enhanced `OpInsert`, `OpUpdate`, and `OpDelete` execution in `pkg/proc/runtime/vm.go` with schema-aware column resolution, dynamic primary key discovery, `IDENTITY_INSERT` validation, and accurate `RowsAffected` / `LastIdentity` tracking.
  - Updated `ExecutionResult` to report `RowsAffected` and `LastIdentity`, accurately synchronizing session `@@ROWCOUNT` and `@@IDENTITY` variables.
  - Extended PGWire and TDS wire protocol command completions to report true DML affected row counts from bytecode execution.
  - Added full `EXPLAIN BYTECODE` support for standalone `INSERT`, `UPDATE`, and `DELETE` statements.
  - Added comprehensive test coverage in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_VBDEDML`).
- **VDBE Engine Unification for Interactive Queries (`pkg/api`, `pkg/proc/runtime`)**:
  - Unified the interactive SELECT query execution path (`ExecuteSQL`) to compile and dispatch directly through the VDBE Virtual Machine (`proc.CompileQuery` ➔ `s.executor.ExecuteInTx`), achieving 100% single-engine execution parity across stored procedures and relational queries.
  - Added `ExecuteInTx` method to `runtime.Executor` to execute precompiled bytecode programs within existing session transactions while returning relational `Rows` and `Columns`.
  - Retained multi-tier fallback ensuring zero regressions across all edge cases.
- **Wire Protocols: Prepared Statement VDBE Bytecode Caching across PGWire & TDS (`pkg/wire`, `pkg/wire/tds`)**:
  - Implemented zero-parse query execution fast-path for prepared statements across PostgreSQL Wire Protocol v3 (`pkg/wire`) and Microsoft SQL Server TDS 7.x/8.0 (`pkg/wire/tds`).
  - Precompiled parameterized queries (`$1`, `$2` and `@p1`, `@p2`) into VDBE `*proc.Program` bytecode during `Parse` (PGWire) and `sp_prepare` / `sp_prepexec` (TDS).
  - Executed cached bytecode programs directly via `runtime.Executor.Run` during `Execute` (PGWire) and `sp_execute` (TDS), completely bypassing runtime SQL re-parsing and string substitutions.
  - Added unit and protocol test coverage in `pkg/wire/server_test.go` (`TestWire_CompileQueryToProgram`) and `pkg/wire/tds/server_test.go` (`TestTDS_PreparedStatement_BytecodeProgram`).
- **VDBE Relational Query Opcode `OpRelQuery` & Volcano Engine Integration (`pkg/proc`, `pkg/proc/runtime`, `pkg/api`, `pkg/wire`)**:
  - Introduced `OpRelQuery` opcode in `pkg/proc/bytecode.go` with `RelQueryData` metadata struct and `Program.QuerySQL` field.
  - Extended bytecode compiler (`pkg/proc/compiler.go`) to compile interactive SELECT statements into VDBE bytecode programs via `CompileQuery` and `compileStatement`.
  - Implemented `OpRelQuery` execution in `pkg/proc/runtime/vm.go` connecting VDBE with the Volcano pipelined iterator engine (`pkg/execution`) for relational queries and joins.
  - Added parameter and variable binding (`bindWhereVariables`) for relational query execution inside stored procedures and ambient transactions.
  - Wired `TableGetter` schema resolution across `api.Server`, PostgreSQL Wire Protocol v3 (`pkg/wire`), and Microsoft TDS (`pkg/wire/tds`) executors.
  - Implemented `EXPLAIN BYTECODE <sql>` syntax in `pkg/api/sqlexec.go` returning disassembled VDBE instruction listings.
  - Added comprehensive unit and integration test coverage across `compiler_test.go`, `runtime_test.go`, and `sqlexec_test.go`.
- **T-SQL Built-in Functions Expansion (`pkg/proc`, `pkg/api`)**:
  - Implemented `STUFF(string, start, length, new_string)` string manipulation function adhering to Microsoft T-SQL 1-based indexing and boundary rules.
  - Expanded `CHARINDEX(sub, target [, start])`, `REPLACE(str, old, new)`, and `REVERSE(str)` with multi-byte rune awareness.
  - Enhanced `DATEADD(part, n, date)` and `DATEDIFF(part, start, end)` to support unquoted datepart identifiers (`day`, `month`, `year`, `hour`, etc.) matching native SQL Server syntax.
  - Added unit and SQL integration test coverage in `pkg/proc/builtins_test.go` and `pkg/api/sqlexec_test.go` (`TestSQLExecutor_BuiltinFunctions`).
- **Storage & DDL: `TRUNCATE TABLE <table>` Engine (`pkg/storage`, `pkg/storage/memengine`, `pkg/storage/wiredtiger`, `pkg/api`)**:
  - Added `TruncateTable(tableName string) error` method across `storage.Engine` interface, `memengine`, and `wiredtiger`.
  - Implemented instant O(1) in-memory record, version, and index resets in `memengine` with foreign key dependency safeguards.
  - Added WAL logging and recovery replay for `OpTruncateTable` guaranteeing durability across server restarts.
  - Implemented native `WT_SESSION::truncate` in WiredTiger with automatic drop & recreate fallback for tables and secondary indexes.
  - Added comprehensive test coverage in `pkg/storage/memengine/memengine_test.go` and `pkg/api/sqlexec_test.go` (`TestSQLExecutor_TruncateTable`).
- **T-SQL `SELECT ... INTO #temp_table / table` Engine (`pkg/api`)**:
  - Implemented dynamic table creation and population via `SELECT <projections> INTO <target_table> FROM <source> [WHERE ...]`.
  - Added intelligent column type and primary key inference from source schemas and evaluated expression result sets.
  - Supported local temporary tables (`#table`), global temporary tables (`##table`), and permanent tables.
  - Integrated session-scoped lifecycle tracking for `#temp` tables with automatic teardown on `CloseSession`.
  - Added comprehensive test coverage in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_SelectInto`).
- **T-SQL Table Variables `DECLARE @table TABLE (...)` Engine (`pkg/api`)**:
  - Implemented session-scoped table variable declaration syntax `DECLARE @var TABLE (colDef, ...)`.
  - Added physical storage binding via isolated session-namespaced tables (`__tv_<sessionID>_<var>`).
  - Integrated dynamic query rewriter substituting `@var` references across `INSERT`, `SELECT`, `UPDATE`, `DELETE`, and `JOIN` clauses.
  - Implemented automatic lifecycle cleanup on `CloseSession`, dropping all associated physical tables from the storage engine.
  - Added comprehensive multi-session isolation unit tests in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_TableVariables`).
- **Wire Protocols: `TCP_NODELAY` & Socket Keep-Alive Optimization (`pkg/wire`, `pkg/wire/tds`)**:
  - Explicitly enabled `SetNoDelay(true)` across PostgreSQL Wire Protocol v3 and Microsoft SQL Server TDS 7.x/8.0 listeners, disabling Nagle's algorithm and eliminating up to 40ms delayed-ACK packet latency.
  - Enabled active TCP keep-alive with 30-second interval probe lifecycle preventing stale socket accumulation.
- **In-Memory OCC Engine: Fine-Grained Table-Level Striped Locking (`pkg/storage/memengine`)**:
  - Replaced single coarse-grained engine mutex in `Commit()` with per-table read/write mutexes (`TableData.mu`).
  - Implemented deadlock-free canonical lock acquisition (sorted table names) allowing concurrent transactions modifying disjoint tables to commit in parallel.
  - Slashed read blocking during commit phases across `getRecordAndVerUnderLock`, `Scan`, and `IndexScan`.
  - Skyrocketed In-Memory TPC-C performance from 892k to **1,251,489 tpmC (+40.1%)** and **43,832 TPS (+30.9%)**, with New-Order commit success rate reaching **99.8%** and average latency plummeting to **0.20ms**.
- **In-Memory OCC Engine: `cloneRecord` & Heap Allocation Optimization (`pkg/storage/memengine`)**:
  - Eliminated redundant double map cloning on row updates by retrieving unallocated snapshots in `getRecordAndVerUnderLock(tableName, pk, false)` and sizing updated maps in a single allocation.
  - Eliminated map cloning on `deleteLocked` for foreign key cascade checks.
  - Reused transaction-local staged records directly without defensive copies when re-read in the same transaction.
  - Elevated In-Memory TPC-C performance from 802k to **892,975 tpmC (+11.3%)** and **33,480 TPS (+11.0%)** while slashing sub-millisecond row latency (0.93ms New-Order, 0.36ms Payment).
- **WiredTiger Engine: High-Performance Binary Record Codec (`pkg/storage/wiredtiger`)**:
  - Replaced reflection-based `json.Marshal` and `json.Unmarshal` with MessagePack binary serialization and direct CGO raw pointer passing (`WT_ITEM`, `value_format=u`).
  - Implemented zero-allocation `C.go_wt_cursor_set_val_raw` and `C.go_wt_cursor_get_val_raw`, eliminating string conversion and libc `malloc`/`free` on record writes.
  - Added seamless JSON fallback in `decodeRecord` ensuring backwards compatibility with existing table data.
  - Boosted TPC-C tpmC from 386k to **507,456 tpmC (+31.2%)** and overall TPS to **17,294 TPS (+33.0%)** while dropping p95 latency.
- **WiredTiger Engine: Cursor Caching & Reuse Optimization (`pkg/storage/wiredtiger`)**:
  - Implemented per-session table cursor caching (`s.cursors map[string]*C.WT_CURSOR`) in `WTSession`.
  - Replaced repetitive per-operation `open_cursor` and `cursor_close` allocations with `cursor->reset` recycling.
  - Added deterministic cursor resetting on transaction `Commit` and `Rollback`, and batch teardown on `WTSession.Close`.
  - Slashed CGO allocation overhead, improving TPC-C tpmC from 370k to 386k+ and reducing average row mutation latency.
- **Database Consistency & Diagnostics Engine: `DBCC` (`pkg/api`)**:
  - Implemented `DBCC USEROPTIONS`: inspects and returns active session settings (isolation level, textsize, lock_timeout, quoted_identifier, ansi_nulls).
  - Implemented `DBCC CHECKTABLE ('table_name')`: scans table records, validates primary key uniqueness, secondary index point-lookup consistency, and reports allocation/data integrity findings.
  - Implemented `DBCC CHECKDB`: runs consistency verification across all user tables and indexes in the catalog.
  - Implemented `DBCC SHRINKDATABASE ('db_name')`: reports database file allocation, current page counts, and estimated pages.
  - Added comprehensive test coverage in `pkg/api/sqlexec_test.go` (`TestSQLExecutor_DBCCCommands`).
- **T-SQL Control Flow: `GOTO` & Labels Engine (`pkg/proc`, `pkg/proc/runtime`)**:
  - Added lexical tokens `TokenGoto` and `TokenColon` (`:`) with AST node definitions `GotoStmt` and `LabelStmt`.
  - Implemented 2-pass jump resolution in `Compiler` supporting forward and backward jumps with `OpJump`.
  - Added compile-time validation detecting undefined target labels and rejecting duplicate label definitions.
  - Added comprehensive bytecode compilation and VM execution regression tests in `pkg/proc/compiler_test.go` and `pkg/proc/runtime/regression_test.go`.
- **T-SQL Global Temporary Tables `##` Engine (`pkg/api`)**:
  - Implemented cross-session global temporary tables prefixed with `##` (`CREATE TABLE ##table (...)`).
  - Added shared table access, mutations, and query planning across multiple concurrent client sessions with case-insensitive naming parity.
  - Implemented session lifecycle garbage collection in `CloseSession`, automatically dropping global temporary tables when their creator session disconnects.
  - Supported explicit `DROP TABLE ##table` and integration with `sp_help` and catalog introspection.
  - Added comprehensive multi-session concurrency tests in `pkg/api/sqlexec_test.go`.
- **T-SQL `@@ERROR` Status Tracking & `SET IDENTITY_INSERT` Engine (`pkg/storage`, `pkg/api`, `pkg/proc/runtime`)**:
  - Added session-level `@@ERROR` tracking that resets to 0 on successful statements and sets to 50000 on execution or DML errors.
  - Implemented `Identity` column metadata in `storage.Column` and support for `IDENTITY(seed, incr)` and `AUTO_INCREMENT` in `CREATE TABLE`.
  - Implemented `SET IDENTITY_INSERT [schema.]table ON|OFF` session controls with validation against non-identity tables and single-table session mutual exclusion.
  - Added identity insertion enforcement in `INSERT INTO`, auto-generating monotonic IDs and updating watermark on explicit inserts.
  - Added evaluation support for `@@IDENTITY`, `SCOPE_IDENTITY()`, and `ERROR_NUMBER()`.
  - Added comprehensive unit and regression tests in `pkg/api/sqlexec_test.go`.
- **Built-in System Stored Procedures (`pkg/api`)**:
  - Implemented core administrative stored procedures: `sp_help`, `sp_spaceused`, `sp_who`, `sp_who2`, `sp_lock`, and `sp_rename`.
  - Added object and column detail introspection in `sp_help [object_name]`.
  - Added database and table row count/storage space usage reporting in `sp_spaceused [object_name]`.
  - Added active connection and transaction monitor in `sp_who` / `sp_who2`, and lock status inspection in `sp_lock`.
  - Added atomic table and column renaming utility with schema and data migration in `sp_rename 'old_name', 'new_name' [, 'COLUMN']`.
  - Direct execution support with or without `EXEC` prefix (`EXEC sp_help` and `sp_help`).
- **DBeaver & Beekeeper Studio GUI Client Introspection Compatibility (`pkg/api`)**:
  - Implemented table-less scalar `SELECT` evaluation for session functions and literal expressions (`SELECT 1`, `current_database()`, `current_schema()`, `version()`, `now()`, `pg_backend_pid()`, `@@VERSION`, `@@SPID`, `@@TRANCOUNT`, `@@ROWCOUNT`).
  - Added support for comprehensive T-SQL and PostgreSQL session configuration options in `SET` (`SET NOCOUNT ON`, `SET ANSI_NULLS ON`, `SET QUOTED_IDENTIFIER ON`, `SET TRANSACTION ISOLATION LEVEL ...`, `SET TIME ZONE ...`, `SET NAMES ...`, `SET extra_float_digits ...`).
  - Added multi-word and system parameters to `SHOW` (`SHOW search_path`, `SHOW transaction_isolation`, `SHOW timezone`, `SHOW server_version`, `SHOW max_connections`).
  - Added connection pool lifecycle commands (`DISCARD ALL`, `RESET ALL`, `DEALLOCATE ALL`, `UNLISTEN *`).
  - Added virtual catalog views for PostgreSQL inspection (`pg_catalog.pg_database`, `pg_catalog.pg_namespace`, `pg_catalog.pg_tables`, `pg_catalog.pg_class`, `pg_catalog.pg_type`, `pg_catalog.pg_attribute`, `pg_catalog.pg_roles`, `pg_catalog.pg_am`, `pg_catalog.pg_settings`).
  - Added virtual catalog views for SQL Server inspection (`sys.schemas`, `sys.objects`, `sys.views`, `sys.types`), with enhanced column aliases and bracketed/quoted schema normalization (`[dbo].[accounts]`, `[sys].[tables]`, `"pg_catalog"."pg_tables"`).
---

## [0.6.0]

### Added
- **TPC-C Standard OLTP Benchmark Driver (`cmd/tpcc`, `pkg/storage/memengine`)**:
  - Implemented in-tree TPC-C benchmark suite modeling the full 9 standard relational tables (`warehouse`, `district`, `customer`, `history`, `new_order`, `orders`, `order_line`, `item`, `stock`).
  - Implemented standard TPC-C workload mix (45% New-Order, 43% Payment, 4% Order-Status, 4% Delivery, 4% Stock-Level).
  - Added configurable retry policy with exponential backoff and jitter (`-max-retries=3`) recovering from serialization and MVCC write-write lock contention.
  - High-precision metric measurement: official **tpmC** (New-Order Transactions Per Minute), overall **TPS** (Transactions Per Second), retry recovery statistics, and latency distribution percentiles (Min, Avg, p50, p90, p95, p99, Max).
  - Multi-engine verification with retries: achieves **802,542 tpmC (30,168 TPS)** on In-Memory OCC and **370,641 tpmC (12,460 TPS)** on WiredTiger B+Tree MVCC with 91.5% New-Order commit rate.
  - Hardened OCC record isolation in `memengine` to return cloned records on `tx.Get()` preventing concurrent map iteration/mutation data races.
- **PostgreSQL Wire Protocol SSL/TLS Encryption & Handshake (`pkg/wire`, `cmd/server`)**:
  - Implemented full PostgreSQL Wire v3 SSL/TLS encryption handshake (`SSLRequest` code `80877103` and `GSSENCRequest` code `80877104`).
  - Added support for both loaded X.509 certificate pairs (`--tls-cert`, `--tls-key`) and zero-config self-signed certificate generation (`pkg/wire/tls.go`).
  - Configurable strict encryption enforcement (`--require-ssl`) returning standard PostgreSQL FATAL ErrorResponse (`28000`) for unencrypted connections.
  - Seamless socket upgrade from plaintext to TLS stream with full protocol support (AuthenticationOk, ParameterStatuses, simple queries, extended queries, COPY).
  - Comprehensive unit and integration test suite (`pkg/wire/tls_test.go`).
- **JSON & JSONB Data Types with Extraction Operators (`pkg/storage`, `pkg/proc`, `pkg/execution`, `pkg/planner`, `pkg/api`)**:
  - Full ANSI/PostgreSQL/MSSQL compatible `JSON` and `JSONB` data type support (`storage.TypeJSON = 6`) with schema validation and syntax enforcement.
  - PostgreSQL extraction operators `->` (extract element as JSON string) and `->>` (extract element as unquoted text) with multi-level path chaining (e.g. `data -> 'user' ->> 'name'`).
  - Standard scalar and document extraction functions: `JSON_VALUE(doc, path)` (scalar extraction) and `JSON_QUERY(doc, path)` (object/array extraction) with support for standard dot-notation and array index paths (`$.user.scores[0]`).
  - Native Pratt parser and AST integration (`TokenJSONExtract`, `TokenJSONExtractText`) with tight operator precedence, enabling expressions in `SELECT` projections, `WHERE` / `HAVING` filters, `ORDER BY`, and stored procedures.
  - Pre-parsed AST caching in Volcano `ProjectionIterator` and `FilterIterator` for high-throughput zero-reparsing query evaluation.
  - Dedicated storage and API integration test suites (`pkg/storage/json_test.go`, `pkg/api/json_test.go`).
- **ANSI/ISO SQL & T-SQL Window Functions Engine (`pkg/planner`, `pkg/execution`, `pkg/api`)**:
  - Full relational window functions support integrated into Volcano execution pipeline: `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`, and partition-level window aggregates (`SUM`, `AVG`, `MIN`, `MAX`, `COUNT`).
  - Full syntax compatibility with `OVER ([PARTITION BY col1, col2, ...] [ORDER BY col [ASC|DESC], ...])` and projection aliases (`AS rn`).
  - Physical `WindowIterator` operator with partition grouping, multi-key in-partition sorting, tie-aware ranking, forward/backward offset peeking, and zero-allocation $O(1)$ `ColumnIndexMap` expansion.
  - Seamless execution inside Common Table Expressions (CTEs) and subqueries.
  - Comprehensive unit and integration test suite (`pkg/api/window_test.go`).
- **Foreign Key Constraints & Referential Integrity Cascading Engine (`pkg/storage`, `pkg/storage/memengine`, `pkg/api`)**:
  - Full relational referential integrity support with table-level, named, and column-inline `FOREIGN KEY ... REFERENCES ... [ON DELETE <action>] [ON UPDATE <action>]` syntax in `CREATE TABLE`.
  - Referential actions supported: `CASCADE`, `SET NULL`, `RESTRICT`, and `NO ACTION` across parent deletions and updates.
  - ANSI SQL MATCH SIMPLE compliant NULL-handling for composite and single foreign keys.
  - Dependency protection in `DROP TABLE` preventing accidental dropping of tables referenced by active foreign key constraints with `ErrDependentObjectsExist`.
  - Re-keying OCC mutations on parent primary key updates with bounded recursion depth (<= 16) preventing cyclic deadlock or stack overflow.
  - Comprehensive unit and integration test coverage (`pkg/api/foreign_key_test.go`).
- **Zero-Allocation Volcano Relational Execution Engine & Slice-Backed Tuple Pipeline (`pkg/execution`)**:
  - Refactored `execution.Tuple` from dynamic `map[string]any` to contiguous slice-backed struct (`Values []any`, `Index *ColumnIndexMap`), eliminating map bucket allocation on query execution hot paths.
  - Implemented precomputed `ColumnIndexMap` with $O(1)$ column offset lookups supporting case-insensitive table/alias prefixes and raw aggregate expressions (`COUNT(*)`, `AVG(col)`).
  - Extended `Iterator` interface with `ColIndexMap() *ColumnIndexMap` and introduced `MergeColumnIndexMaps` for seamless, zero-allocation schema and prefix propagation across join trees (`HashJoin`, `IndexNestedLoopJoin`) and upstream projection/sort/filter/aggregate operators.
  - Integrated `sync.Pool` buffer pooling (`AcquireValuesSlice` / `ReleaseValuesSlice`) to mitigate GC pressure during high-throughput query iteration.
  - Added `ToEvalMap()` for AST boolean expression evaluation and lossless JSON serialization for external merge sort spill runs.
- **Pure-Go Concurrent SkipList Secondary Index (`pkg/storage/memengine`)**:
  - High-concurrency, lock-aware SkipList indexing replacing $O(N)$ slice copy element shifting with $O(\log N)$ probabilistic skip links.
  - Sub-microsecond point lookup (~177ns/op) and bulk insertion (~584ns/op, 2.8M ops/sec).
  - Unbounded and bounded range scans (`ScanRange`) via Level-0 forward pointer iteration.
  - Seamless snapshot checkpoint serialization (`ToEntries` / `FromEntries`) preserving MessagePack and JSON backward compatibility.
- **Zero-Copy Cursor Tuple Iterator (`pkg/storage/memengine/memengine.go`)**:
  - Eliminated eager full-table clone allocations (`cloneRecord`) during `Scan()` and `IndexScan()`, switching to direct immutable record memory views.
  - Reduced per-row memory allocations to zero on table scan hot paths, cutting GC pressure and query latency.
- **T-SQL Built-in Function Library & VDBE System Variables (`pkg/proc/builtins.go`)**:
  - Centralized single-source-of-truth scalar function evaluator shared identically across VDBE Bytecode VM and Volcano relational expressions.
  - Date & Time functions: `DATEDIFF`, `DATEADD`, `DATEPART`, `YEAR`, `MONTH`, `DAY`, `GETDATE`, `NOW`, `GETUTCDATE`, `SYSDATETIME`.
  - String manipulation functions: `CONCAT`, `LEFT`, `RIGHT`, `CHARINDEX`, `LTRIM`, `RTRIM`, `REVERSE`, `SPACE`, `REPLICATE`.
  - Mathematical & logical functions: `POWER`, `SQRT`, `SIGN`, `PI`, `IIF`, `CHOOSE`.
  - System functions & global metadata: `NEWID()` (RFC 4122 UUID v4), `SCOPE_IDENTITY()`, `@@TRANCOUNT`, `@@SPID`, `@@ERROR`, `@@SERVERNAME`, `@@VERSION`.
  - Native parser support for keyword-colliding function calls (`LEFT`, `RIGHT`) and bare datepart identifiers (`day`, `month`, `year`).
- **Microsoft SQL Server TDS RPC Prepared Statement Pipeline (`pkg/wire/tds`)**:
  - Full RPC routing and lifecycle for `sp_prepare` (ProcID 11), `sp_execute` (ProcID 12), `sp_prepexec` (ProcID 13), `sp_unprepare` (ProcID 15), and parameterized `sp_executesql` (ProcID 10).
  - Accurate token framing: `TokenReturnValue` (0xAC) for statement `@handle`, `TokenReturnStatus` (0x79), and `TokenDoneProc` (0xFE).
  - Robust parameter deserializer supporting `INT`, `BIGINT`, `SMALLINT`, `TINYINT`, `BIT`, `FLOAT`, `REAL`, `VARCHAR`, `NVARCHAR`, `VARBINARY`, and Partially Length-Prefixed (`VARCHAR(MAX)` / `NVARCHAR(MAX)` PLP).
  - Out-of-quotes lexical parameter binding and `?` ODBC/JDBC placeholder replacement.
- **PostgreSQL Driver Protocol Hardening & Cancellation (`pkg/wire`)**:
  - `BackendKeyData` ('K') handshake message emitting backend process ID and secret key.
  - Complete `ParameterStatus` response suite (`server_version`, `server_encoding`, `client_encoding`, `application_name`, `is_superuser`, `session_authorization`, `integer_datetimes`, `DateStyle`, `IntervalStyle`, `standard_conforming_strings`, `TimeZone`).
  - Safe, non-blocking `CancelRequest` (code `80877102`) handling to abort executing client sessions.
- **T-SQL Variable Declaration Extension (`pkg/api/sqlexec.go`)**:
  - Multi-variable `DECLARE @var [type] [= expr]` statement support in script and session execution.

---

## [0.5.0]

### Added
- **Comprehensive Technical Documentation & Architecture Specification Suite**:
  - `doc/DATA_DICTIONARY.md`: Virtual system catalog views (`sys.tables`, `sys.columns`, `sys.indexes`, `sys.databases`, `sys.procedures`, `information_schema.*`) and CBO optimizer statistics views (`sys.table_stats`, `sys.column_stats`).
  - `doc/HIGH_AVAILABILITY.md`: Multi-node replication topology, Raft consensus clustering, automatic leader election, and disaster recovery SLAs.
  - `doc/EXTENSIONS.md`: Pluggable storage engine SPI (`storage.Engine`), secondary procedure fallback parser registration, and wire protocol drivers.
  - `doc/METRICS.md`: Prometheus OpenMetrics time-series telemetry dictionary, `/api/pool/stats`, `/api/health` probes, and Grafana alerting templates.
  - `CODE_OF_CONDUCT.md`: Contributor Covenant v2.1 code of conduct.
  - `LICENSE`: Official GNU General Public License v2 / v3 dual licensing terms.

### Changed
- Standardized project workspace paths and module references to `LionSQL` across all architectural documentation.

---

## [0.4.0]

### Added
- **Microsoft SQL Server TDS 7.x/8.0 Wire Protocol**:
  - Native TDS listener on port `1433` handling PRELOGIN, LOGIN7, SQLBatch UTF-16LE, and RPC token stream decoding.
  - Native compatibility with SQL Server Management Studio (SSMS), Azure Data Studio, `sqlcmd`, and Microsoft ODBC/JDBC drivers.
- **PostgreSQL High-Speed Bulk Copy Protocol (`COPY`)**:
  - Wire-level framing for `COPY <table> FROM STDIN` and `COPY <table> TO STDOUT` (`MsgCopyInResponse`, `MsgCopyOutResponse`, `MsgCopyData`, `MsgCopyDone`, `MsgCopyFail`).
  - Full compatibility with `psql \copy` and `pg_dump` for high-throughput batch ingestion.
- **Volcano Iterator Relational Execution Engine**:
  - Composable iterator pipeline (`Init`, `Next`, `Close`): `SeqScanIterator`, `IndexScanIterator`, `FilterIterator`, `ProjectIterator`, `HashJoinIterator`, `IndexNestedLoopJoinIterator`, `GroupByIterator`, `LimitIterator`.
- **Cost-Based Optimizer (CBO) & Statistics Catalog**:
  - `ANALYZE TABLE <name>` statistics engine computing row counts, page counts, distinct values (`NDV`), null fractions, and 64-bucket equi-depth histograms.
  - Cost model evaluating sequential disk I/O vs. random B+Tree traversal and CPU tuple processing.
  - Plan visualizer via `EXPLAIN` and `EXPLAIN ANALYZE`.
- **Query Memory Limit Governor & Spill-to-Disk External Sort**:
  - Operator-level memory budget tracking (`EstimateTupleSize`) governed by `work_mem` (default `64MB`).
  - Automated spilling of sorted chunks to temporary run files (`lionsql-sort-run-*.tmp`).
  - Streaming N-way Min-Heap merge sort (`tupleMergeHeap`) inside `SortIterator.Next()`.
- **Online Non-Blocking Snapshot Hot Backup & Restore**:
  - Live point-in-time snapshot isolation captured under read-only transactions without locking tables or pausing concurrent OLTP writes.
  - Self-contained `.tar.gz` archive containing `manifest.json`, `schemas.json`, catalog routines, and compressed data rows.
  - CLI utilities: `lionsql -dir data -backup <path>` and `-restore <path>`.
  - HTTP REST endpoints: `GET/POST /api/backup` and `POST /api/restore`.
- **Built-in Goroutine Connection Pooler (PgBouncer Replacement)**:
  - Go goroutine multiplexing supporting 10,000+ client connections with minimal RAM overhead.
  - Bounded worker execution pool (`MaxWorkers = NumCPU * 4`) and cached storage session pool (`MaxIdleSessions = 64`).
  - Monitoring via `GET /api/pool/stats`.
- **Virtual System Catalog Views (`sys.*` and `information_schema.*`)**:
  - Dynamic virtual catalog projection enabling schema inspection by external database tools (DBeaver, DataGrip, Navicat).
- **Embedded Web Management Studio**:
  - PocketBase-style single-file dashboard on `/_/` with spreadsheet inline editing, table builder, and SQL Query Studio.
  - Realtime Server-Sent Events (SSE) data change stream (`/api/realtime`) with live web UI synchronization.
  - Superuser authentication, JWT revocation, and granular API key management.
- **Core Architecture & Operational Documentation Suite**:
  - `doc/ARCHITECTURE.md`, `doc/ADR.md` (ADR-001 ~ ADR-014), `doc/TSQL_REFERENCE.md`, `doc/API_REFERENCE.md`, `doc/OPERATIONS.md`, `doc/BENCHMARK.md`, `doc/INTERNALS_*.md`, `doc/SECURITY.md`, `doc/MIGRATION.md`, `doc/ROADMAP.md`, `doc/TROUBLESHOOTING.md`.

---

## [0.3.0]

### Added
- **Native VDBE Bytecode Virtual Machine**:
  - SQLite-style register-based execution engine with `sync.Pool` register slice reuse, replacing the prototype JavaScript runtime.
  - Procedural control flow: `IF...ELSE`, `WHILE`, `TRY...CATCH`, `PRINT`, `RAISERROR`, `RETURN`, and cursors.
- **Secondary Index Engine & Query Optimizations**:
  - Unique and non-unique secondary indexes with range scan support.
  - In-memory Hash Join and Index Nested Loop Join execution engines.
  - `GROUP BY` aggregation engine and `HAVING` clause filtering.
  - Common Table Expressions (`WITH` CTE) and nested subqueries.
  - Temporary tables (`#temp`) and Table-Valued Functions (TVFs).
  - T-SQL User-Defined Functions (UDFs) and trigger engine with virtual delta tables (`INSERTED` / `DELETED`).
- **PostgreSQL Extended Query Protocol**:
  - Prepared statements and binary parameter binding (`Parse`, `Bind`, `Describe`, `Execute`, `Sync`) for ORM and GUI client compatibility.
- **In-Memory OCC Persistence & Durability**:
  - Write-Ahead Logging (WAL) and Append-Only File (AOF) crash recovery.
  - MessagePack binary serialization for fast snapshot checkpoints.
  - Interactive session transactions and session variables.

---

## [0.2.0]

### Added
- **Official ANTLR4 T-SQL Parser Integration**:
  - Microsoft T-SQL ANTLR4 grammar (`TSqlParser.g4`) integration for full-spec syntax parsing.
- **Language Extensions & Runtime Robustness**:
  - Expanded T-SQL grammar: `TRY...CATCH`, `CASE...WHEN`, built-in scalar functions, and multi-row DML.
  - Cursor execution loops and ambient transaction context.
- **Storage & Engine Enhancements**:
  - WiredTiger set as the default production storage engine backend.
  - Atomic file-backed disk persistence and crash recovery across restarts.
  - Background daemon lifecycle controls (`-d`, `-status`, `-restart`, `-stop`).
- **High-Concurrency Benchmarking**:
  - OLTP banking benchmark harness measuring transactions per second (TPS) and verifying serializability under contention.
  - WiredTiger session tuning and disk I/O optimization.

### Changed
- Project, Go module, and repository rebranded and standardized to `LionSQL`.

---

## [0.1.0]

### Added
- **Initial OLTP Database Engine**:
  - Standalone Go server executable delivering high-concurrency embedded transaction processing.
  - PostgreSQL Wire Protocol v3 listener on port `5433`.
  - Prototype T-SQL stored procedure execution engine via Goja JavaScript runtime transpilation.
  - Initial WiredTiger Cgo storage engine implementation.
  - Basic HTTP REST API for table schemas, records, and stored procedure execution.
