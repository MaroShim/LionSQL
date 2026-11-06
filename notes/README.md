# LionSQL Engineering Notes & Context Handover (`notes/`)

> - **Created**: 2026-10-03
> - **Last Updated**: 2026-11-06
> [한국어 문서 (Korean Version)](README.ko.md)

This directory serves as a persistent knowledge base (Shared Brain & Handover Notes) designed to help new AI agents or collaborating engineers quickly synchronize on **underlying architectural context, rejected alternatives (Negative Knowledge), production pitfalls, and user engineering philosophies** that cannot be fully captured from source code or official end-user documentation (`doc/`) alone when switching across machines or sessions.

---

## 1. Core Philosophy & Invariants (SSOT)

Foundational engineering principles, architectural invariants, code landmines, and central backlog systems.

* **[`ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)** (Korean)
  * **Core Invariants (8 Rules)**: Dual-Parser, single catalog per process, non-blocking MVCC/OCC, single static binary, zero-regex SQL parsing.
  * **Explicit Spec-Out Decisions**: Theoretical and architectural rationales behind 14 rejected features to preserve extreme KISS/YAGNI.
* **[`PITFALLS_AND_TRICKS.md`](PITFALLS_AND_TRICKS.md)**
  * **Codebase Landmines & Fragile Spots**: SPI decoupling against circular imports, sandbox network isolation, atomic file I/O, and Decimal JSON safety.
* **[`TAXONOMY.ko.md`](TAXONOMY.ko.md)** (Korean)
  * **Task Identifier Taxonomy**: Consolidated reference for `P1~P3` (priority tasks), `VPERF` (complex queries), `GA-REGEX` (zero-regex gates), and `S-1~S-7` (syntax completeness).
* **[`PROTOCOL_EMULATION_BOUNDARIES.md`](PROTOCOL_EMULATION_BOUNDARIES.md)**
  * **Multi-Protocol Emulation Boundaries**: TDS (1433), PGWire (5433), and MySQL wire compatibility scopes and cutoff boundaries.
* **Master Roadmap & SSOT Backlogs**:
  * **[`TODO.md`](../TODO.md)**: 3-tier active engineering dashboard (100% synchronized with SQLite sprint DB).
  * **[`ROADMAP.md`](../ROADMAP.md)**: Macro-level release milestones (Phases 1-6) and architectural evolution.

---

## 2. Active 1.0 Release Sprints & Quality Gates

Active verification audits and hardening projects driving LionSQL toward the 1.0 General Availability release.

* **[`PRE_RELEASE_100_STEP_DUAL_CODE_REVIEW_PLAN.ko.md`](PRE_RELEASE_100_STEP_DUAL_CODE_REVIEW_PLAN.ko.md)** (Korean)
  * 100-step dual AI (Gemini × Claude) cross-audit master plan across all 10 engine domains (`AUDIT-100-DUAL-REVIEW`).
* **[`PRE_RELEASE_AUDIT_FINDINGS.ko.md`](PRE_RELEASE_AUDIT_FINDINGS.ko.md)** (Korean)
  * Consolidated matrix of defect findings discovered during the 100-step pre-release audit (Scopes 001-020 completed, 18 defects logged).
* **[`REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md)** (Korean)
  * Unified SSOT plan for total elimination of regular expressions in favor of deterministic tokenizers (`GA-REGEX-DDL-1` - `GA-REGEX-FINAL-3`, `SEC-PGWIRE-PARAM-1`).
* **[`DURABILITY_VERIFICATION_PLAN.ko.md`](DURABILITY_VERIFICATION_PLAN.ko.md)** (Korean)
  * Master verification plan for crash resilience (kill tests), soak stress tests, and dogfooding reliability.

---

## 3. Subsystem Architecture & Execution Plans

Technical design specifications and refactoring blueprints organized by engine domain.

### 3.1. Storage Engine & OCC Concurrency
* **[`IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)** (Korean): In-Memory OCC evolution, beating WiredTiger, 104k tpmC architecture, and Option A flat record hardening (`OCC-HYPER`).
* **[`FLAT_RECORD_RELIABILITY_PLAN.ko.md`](FLAT_RECORD_RELIABILITY_PLAN.ko.md)** (Korean): Pointer-free flat record encoding reliability and decoding verification plan.
* **[`LAZY_DECODING_P2_PLAN.ko.md`](LAZY_DECODING_P2_PLAN.ko.md)** (Korean): Columnar lazy decoding design eliminating unnecessary row unpacking costs.
* **[`PERF_REGRESSION_RECOVERY_AND_GATE_PLAN.ko.md`](PERF_REGRESSION_RECOVERY_AND_GATE_PLAN.ko.md)** (Korean): Eliminating key latches to restore 160k TPS and establishing regression prevention gates.

### 3.2. Query Engine, Optimizer & Parser
* **[`QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md)** (Korean): Permanent 2-tier query execution architecture decoupling simple OLTP (VDBE) from complex relational algebra (CBO).
* **[`ARCH_VDBE_ROUTING_1_PLAN.ko.md`](ARCH_VDBE_ROUTING_1_PLAN.ko.md)** (Korean): AST and dialect-based 2-tier query routing normalization (`ARCH-VDBE-ROUTING-1`).
* **[`AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md)** (Korean): Structural catalog routing via symbol tables and AST (measured 527ns).
* **[`LARGE_JOIN_OPTIMIZATION_PLAN.ko.md`](LARGE_JOIN_OPTIMIZATION_PLAN.ko.md)** (Korean): Large join (10+ tables) query optimization and dynamic root selection (`OPT-LARGE-JOIN-1`).
* **[`PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md`](PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md)** (Korean): Planner slimming and pure AST transition plan (`PLANNER-SLIM-1`, `PLANNER-AST-1`).
* **[`VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md`](VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md)** (Korean): Opcode loop unrolling, register pool recycling, and branch prediction optimizations.
* **[`WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md`](WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md)** (Korean): Bytecode compiler lowering for writable Common Table Expressions.
* **[`SQL_PARSER_HARDENING_PLAN.ko.md`](SQL_PARSER_HARDENING_PLAN.ko.md)** (Korean): Hardening SQL parser against literal contamination and semicolon truncation.

### 3.3. Wire Protocols & Networking
* **[`SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md)** (Korean): Server vertical pipeline optimization from socket reception down to adaptive group commits.
* **[`SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)** (Korean): Embedded single-binary Web Studio and SPA hosting architecture (`WEB-SPA-1`, official guide: [`doc/WEB_APP_HOSTING_GUIDE.md`](../doc/WEB_APP_HOSTING_GUIDE.md)).
* **[`MODULAR_ARCHITECTURE_PLAN.ko.md`](MODULAR_ARCHITECTURE_PLAN.ko.md)** (Korean): TDS/Wire layer internal file decomposition without interface disruption (`TDS-MOD-1`).

---

## 4. Verification Reports & Integration Milestones

Empirical verification benchmarks and production application test reports.

### 4.1. Real-World Open-Source Application Integrations (E2E)
* **[`METABASE_INTEGRATION_REPORT.ko.md`](METABASE_INTEGRATION_REPORT.ko.md)** (Korean): Dual-wire Metabase BI integration, MBQL visual query builder, and nested joins verification.
* **[`VAULTWARDEN_INTEGRATION_REPORT.ko.md`](VAULTWARDEN_INTEGRATION_REPORT.ko.md)** (Korean): Vaultwarden (Rust Diesel ORM) 47 migrations and Bitwarden REST API pass.
* **[`ANSWER_INTEGRATION_REPORT.ko.md`](ANSWER_INTEGRATION_REPORT.ko.md)** (Korean): Apache Answer 35 tables, batch identity generation, and REST API pass.
* **[`GITEA_INTEGRATION_REPORT.ko.md`](GITEA_INTEGRATION_REPORT.ko.md)** (Korean): Gitea dual-wire protocol verification across 116 tables.
* **[`WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md`](WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md)** (Korean): Woodpecker CI and Vikunja recursive CTE and compound OR index branching verification.
* **[`GRAFANA_INTEGRATION_REPORT.ko.md`](GRAFANA_INTEGRATION_REPORT.ko.md)** (Korean): Grafana 785 migrations, bytea envelope encryption, and dashboard queries.
* **[`CASDOOR_INTEGRATION_REPORT.ko.md`](CASDOOR_INTEGRATION_REPORT.ko.md)** (Korean): Casdoor 44 tables, composite primary keys, and OIDC/RBAC verification.
* **[`IOTA_ERP_INTEGRATION_REPORT.ko.md`](IOTA_ERP_INTEGRATION_REPORT.ko.md)** (Korean): IOTA SDK ERP integration with `pgx/v5` binary protocols and 47 migrations.
* **[`UMAMI_INTEGRATION_REPORT.ko.md`](UMAMI_INTEGRATION_REPORT.ko.md)** (Korean): Umami web analytics platform integration report.
* **[`BABELFISH_INTEGRATION_REPORT.ko.md`](BABELFISH_INTEGRATION_REPORT.ko.md)** (Korean): AWS Babelfish T-SQL stored procedure compatibility report.

### 4.2. Standard Benchmarks & Soak Load Testing
* **[`SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md`](SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md)** ([English](SLT_FULL_SUITE_100_PERCENT_REPORT.md)): SQLite official SQLLogicTest (107 suites, 551,217 queries) 100% flawless pass report and cures for 8 deep engine defects.
* **[`SOAK_TEST_1H_VERIFICATION_REPORT.ko.md`](SOAK_TEST_1H_VERIFICATION_REPORT.ko.md)** (Korean): 1-hour soak stress test (720k ops / 393k commits), `SQLSTATE 40001` conflict handling, and WiredTiger 500MB cache convergence.
* **[`COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)** (Korean): SSB Star Schema & Mini TPC-H representative queries, DuckDB/ClickHouse comparison, and 1.0 freeze status.

---

## 5. Postmortems & Negative Knowledge

Hard-learned operational lessons, test blind-spot analyses, and explicit evaluation notes preserving architectural boundaries.

* **[`DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md`](DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md)** (Korean): Rationale for rejecting DuckDB friendly syntax (`SELECT * EXCLUDE`, `FROM <table>;`) and MySQL DML extensions to prevent silent typo bugs.
* **[`EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md`](EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md)** (Korean): Analysis of empty `IN ()` predicates, retaining 1.0 strict syntax errors, and rejection of ad-hoc toggle flags.
* **[`ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md)** (Korean): Postmortem on Gitea XORM integration bugs and resolving the blind spot between human-crafted vs ORM-generated SQL.
* **[`TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md`](TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md)** (Korean): Postmortem on TDS RPC `sp_executesql` parameter resolution regression and restoration of ordinal positional fallback.
* **[`GROVE_HOTSPOT_POSTMORTEM_AND_ASSETS.ko.md`](GROVE_HOTSPOT_POSTMORTEM_AND_ASSETS.ko.md)** (Korean): Postmortem on Grove benchmark hotspot optimizations and four permanent assets.

---

## 6. Post-1.0 Candidates & Historical Archive

* **[`POST_1.0_CANDIDATES.ko.md`](POST_1.0_CANDIDATES.ko.md)** (Korean)
  * Pool of candidates for Post-1.0 technical exploration (vetted and queued for on-demand review).
* **[`MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md`](MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md)** (Korean)
  * Virtual view alias approach for Materialized Views (transferred to Post-1.0, `FEAT-MATVIEW-ALIAS-1`).
* **[`POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md)** (Korean)
  * Architectural design for HNSW vector indexing and time-series compressed chunking.
* **[`OFFLINE_DOCS_PACKAGING_PLAN.ko.md`](OFFLINE_DOCS_PACKAGING_PLAN.ko.md)** (Korean)
  * Offline documentation bundling and embedded web viewer design (`PKG-OFFLINE-DOCS-1`).
* **[`BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md`](BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md)** (Korean)
  * Babelfish T-SQL Stored Procedure dedicated SLT test suite creation plan.
* **Historical Archive (`notes/archive/`)**:
  * 13 historical execution plans (Windows daemon decoupling, forward-only cursors, unified CLI, etc.) permanently archived upon 100% completion are indexed in **[`notes/archive/README.md`](archive/README.md)**.
