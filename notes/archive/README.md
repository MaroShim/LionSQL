# Historical Archive (`notes/archive/`)

> - **Created**: 2026-10-31
> - **Last Updated**: 2026-11-06
> [한국어 문서 (Korean Version)](README.ko.md)

This directory preserves completed architectural plans, historical backlog records, defect archives, and reference documents that are preserved for design rationale, post-implementation audit, and engineering context.

---

## 1. Archived Core Backlog & Defect Records

1. **[`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md)** (127KB)
   - Comprehensive resolution archive of 107 critical data integrity defects (`BUG-PK-1` - `BUG-PITR-DDL-SILENT-SWALLOW-1`).
2. **[`TODO_SPEC_ARCHIVE.ko.md`](TODO_SPEC_ARCHIVE.ko.md)**
   - Initial engineering specifications and completed subsystem feature archives.
3. **[`TODO.ko.md`](TODO.ko.md)** (42KB)
   - Original Korean comprehensive operational backlog prior to compact English dashboard translation in root [`TODO.md`](../../TODO.md).
4. **[`PITFALLS_AND_TRICKS.ko.md`](PITFALLS_AND_TRICKS.ko.md)** (22KB)
   - Original Korean narrative landmines document prior to compact cheat-sheet consolidation into [`notes/PITFALLS_AND_TRICKS.md`](../PITFALLS_AND_TRICKS.md).
5. **[`PROJECT_STATUS_ASSESSMENT.ko.md`](PROJECT_STATUS_ASSESSMENT.ko.md)**
   - Pre-0.90 architectural maturity and technical debt resolution assessment report.

---

## 2. Completed Architecture & Execution Plans (100% Implemented)

*These plans detail the original problem context, technical specifications, and verification methods for features that are now fully implemented and active in the engine:*

### 2.1. Core Engine & CLI Architecture
1. **[`UNIFIED_CLI_PLAN.md`](../plans/UNIFIED_CLI_PLAN.md)**: Single self-contained binary (`bin/lionsql`) lifecycle unification and subcommands restructuring.
2. **[`WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md`](../plans/WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md)**: Windows service decoupling, 1ms high-resolution timer (`timeBeginPeriod`), and cross-compilation pipeline.
3. **[`OVER_SPECIFICATION_REMEDIATION_PLAN.md`](../plans/OVER_SPECIFICATION_REMEDIATION_PLAN.md)**: Elimination of tool-specific over-specifications and ad-hoc hardcodings across SQL parser and wire protocols.
4. **[`TYPE_GUESSING_ANTI_PATTERN_ELIMINATION_PLAN.ko.md`](../plans/TYPE_GUESSING_ANTI_PATTERN_ELIMINATION_PLAN.ko.md)**: Strict typing enforcement eliminating heuristic type guessing across wire gateways.
5. **[`SQL_ABSTRACTION_LAYER_PLAN.md`](../plans/SQL_ABSTRACTION_LAYER_PLAN.md)**: Storage-engine-agnostic SQL abstraction layer unifying In-Memory OCC and WiredTiger backends.
6. **[`ANTLR_PARSER_COMPACTION_PLAN.md`](../plans/ANTLR_PARSER_COMPACTION_PLAN.md)**: ANTLR4 grammar consolidation, single-file compilation, and memory compaction plan.

### 2.2. SQL Syntax & Query Execution
7. **[`FORWARD_ONLY_CURSOR_COMPATIBILITY_PLAN.ko.md`](../plans/FORWARD_ONLY_CURSOR_COMPATIBILITY_PLAN.ko.md)**: T-SQL forward-only cursor implementation and state machine execution.
8. **[`MYSQL_SYNTAX_ERGONOMICS_COMPATIBILITY_PLAN.ko.md`](../plans/MYSQL_SYNTAX_ERGONOMICS_COMPATIBILITY_PLAN.ko.md)**: Phase 1 MySQL syntax ergonomics (`DESC`, `SHOW TABLES`, backticks) and spec-out boundaries.
9. **[`WEB_STUDIO_CONCURRENCY_AND_SESSION_ISOLATION_PLAN.ko.md`](../plans/WEB_STUDIO_CONCURRENCY_AND_SESSION_ISOLATION_PLAN.ko.md)**: Embedded Web Studio browser session isolation, concurrency controls, and state management.
10. **[`COMPLEX_EXPRESSION_AUDIT_AND_DEEP_TEST_PLAN.ko.md`](../plans/COMPLEX_EXPRESSION_AUDIT_AND_DEEP_TEST_PLAN.ko.md)**: Comprehensive nested arithmetic audit and deep value assertion testing across Volcano operators.

### 2.3. SQLLogicTest (SLT) 550k Query Roadmap
11. **[`SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md`](../plans/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md)**: SLT 107 full suite dual-engine certification plan.
12. **[`SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md`](../plans/SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md)**: SQLLogicTest 3-phase expansion roadmap.
13. **[`SLT_EXTENDED_SUITE_FIXES_PLAN.ko.md`](../plans/SLT_EXTENDED_SUITE_FIXES_PLAN.ko.md)**: Extended suite defect fixes plan.
14. **[`SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md`](../plans/SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md)**: SLT user guide and full suite establishment plan.

### 2.4. Tool Audits & Testbed Plans
15. **[`EXTENDED_SMOKE_TEST_PLAN.md`](../plans/EXTENDED_SMOKE_TEST_PLAN.md)**: 13-domain extended integration smoke test suite architecture and verification.
16. **[`TOOL_AUDIT_PLAN.ko.md`](../plans/TOOL_AUDIT_PLAN.ko.md)**: GUI client tools (SSMS, DbGate, DBeaver, Beekeeper Studio) query compatibility and virtual catalog audit.
17. **[`BENCHMARK_DIR_SAFETY_PLAN.md`](../plans/BENCHMARK_DIR_SAFETY_PLAN.md)**: Non-destructive benchmark directory lifecycle and data protection architecture.
18. **[`GA_VALIDATION_HANDOFF_PLAN.ko.md`](../plans/GA_VALIDATION_HANDOFF_PLAN.ko.md)**: 0.90 GA validation and engineering handover plan.

---

## 3. Deep Audits & Historical Release Notes

1. **[`LIONSQL_DEEP_CODE_REVIEW.ko.md`](LIONSQL_DEEP_CODE_REVIEW.ko.md)**
   - In-depth architectural analysis and engineering code review across all 390 source files (~350k lines).
2. **[`TEST_VERIFICATION_REPORT.ko.md`](../reports/TEST_VERIFICATION_REPORT.ko.md)**
   - Unit and integration test suite regression verification and race-free audit report.
3. **[`RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md)**
   - Architectural audit and preliminary feedback history for 1.0 release.
4. **Historical Beta Release Notes**:
   - Original release notes preserved for `v0.90.0-beta0` through `beta4` (`.ko.md` and `.md`).
