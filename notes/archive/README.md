# Historical Archive (`notes/archive/`)

This directory preserves completed architectural plans, historical backlog records, and reference documents that are preserved for design rationale, post-implementation audit, and engineering context.

---

## 1. Archived Core Backlog & Status Records
1. **[`TODO.ko.md`](TODO.ko.md)**: Original 42KB Korean operational backlog and active sprint queue prior to compact English translation into root [`TODO.md`](../../TODO.md).
2. **[`PITFALLS_AND_TRICKS.ko.md`](PITFALLS_AND_TRICKS.ko.md)**: Original 22KB Korean narrative landmines document prior to compact English cheat-sheet consolidation into [`notes/PITFALLS_AND_TRICKS.md`](../PITFALLS_AND_TRICKS.md).
3. **[`PROJECT_STATUS_ASSESSMENT.ko.md`](PROJECT_STATUS_ASSESSMENT.ko.md)**: Historical architectural maturity and technical debt resolution assessment report (pre-0.90.0).

---

## 2. Completed Architecture & Execution Plans (100% Implemented)
*These plans detail the original problem context, technical specifications, and verification methods for features that are now fully implemented and active in the engine:*

1. **[`WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md`](WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md)**: Windows service decoupling, 1ms high-resolution timer (`timeBeginPeriod`), and cross-compilation pipeline.
2. **[`OVER_SPECIFICATION_REMEDIATION_PLAN.md`](OVER_SPECIFICATION_REMEDIATION_PLAN.md)**: Elimination of tool-specific over-specifications and ad-hoc hardcodings across SQL parser and wire protocols.
3. **[`WEB_STUDIO_CONCURRENCY_AND_SESSION_ISOLATION_PLAN.ko.md`](WEB_STUDIO_CONCURRENCY_AND_SESSION_ISOLATION_PLAN.ko.md)**: Embedded Web Studio browser session isolation, concurrency controls, and state management.
4. **[`FORWARD_ONLY_CURSOR_COMPATIBILITY_PLAN.ko.md`](FORWARD_ONLY_CURSOR_COMPATIBILITY_PLAN.ko.md)**: T-SQL forward-only cursor implementation and state machine execution.
5. **[`MYSQL_SYNTAX_ERGONOMICS_COMPATIBILITY_PLAN.ko.md`](MYSQL_SYNTAX_ERGONOMICS_COMPATIBILITY_PLAN.ko.md)**: Phase 1 MySQL syntax ergonomics (`DESC`, `SHOW TABLES`, backticks) and spec-out boundaries.
6. **[`SQL_ABSTRACTION_LAYER_PLAN.md`](SQL_ABSTRACTION_LAYER_PLAN.md)**: Storage-engine-agnostic SQL abstraction layer unifying In-Memory OCC and WiredTiger backends.
7. **[`UNIFIED_CLI_PLAN.md`](UNIFIED_CLI_PLAN.md)**: Single self-contained binary (`bin/lionsql`) lifecycle unification and subcommands restructuring.
8. **[`TYPE_GUESSING_ANTI_PATTERN_ELIMINATION_PLAN.ko.md`](TYPE_GUESSING_ANTI_PATTERN_ELIMINATION_PLAN.ko.md)**: Strict typing enforcement eliminating heuristic type guessing across wire gateways.
9. **[`COMPLEX_EXPRESSION_AUDIT_AND_DEEP_TEST_PLAN.ko.md`](COMPLEX_EXPRESSION_AUDIT_AND_DEEP_TEST_PLAN.ko.md)**: Comprehensive nested arithmetic audit and deep value assertion testing across Volcano operators.
10. **[`EXTENDED_SMOKE_TEST_PLAN.md`](EXTENDED_SMOKE_TEST_PLAN.md)**: 13-domain extended integration smoke test suite architecture and verification.
11. **[`TOOL_AUDIT_PLAN.ko.md`](TOOL_AUDIT_PLAN.ko.md)**: GUI client tools (SSMS, DbGate, DBeaver, Beekeeper Studio) query compatibility and virtual catalog audit.
12. **[`BENCHMARK_DIR_SAFETY_PLAN.md`](BENCHMARK_DIR_SAFETY_PLAN.md)**: Non-destructive benchmark directory lifecycle and data protection architecture.
13. **[`ANTLR_PARSER_COMPACTION_PLAN.md`](ANTLR_PARSER_COMPACTION_PLAN.md)**: ANTLR4 grammar consolidation, single-file compilation, and memory compaction plan.
