# LionSQL Engineering Notes & Context Handover (`notes/`)

> [한국어 문서 (Korean Version)](README.ko.md)

This directory serves as a persistent knowledge base (Shared Brain & Handover Notes) designed to help new AI agents or collaborating engineers quickly synchronize on **underlying architectural context, rejected alternatives (Negative Knowledge), production pitfalls, and user engineering philosophies** that cannot be fully captured from source code or official end-user documentation (`doc/`) alone when switching across machines or sessions.

---

## Document Index

1. **[`ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)** (Korean)
   - **Core Invariants**: Architectural rationale and explicit spec-out decisions.
   - **Engineering Philosophies & Collaboration Protocols**: Absolute rules including the 3-stage verification pipeline (`Build` -> `Test -race` -> `Commit`), no decorative emojis, radical KISS/YAGNI, and file splitting guidelines.
   - **Core Technology Stack**: VDBE bytecode virtual machine, dual-parser architecture, dual-track UDF execution, and multi-wire protocols.

2. **[`PITFALLS_AND_TRICKS.md`](PITFALLS_AND_TRICKS.md)**
   - **Landmines & Fragile Spots**: Sensitive implementation details across the codebase.
   - **Circular Import Immunity**: SPI pluggable registration decoupling `pkg/proc` from ANTLR4 fallback parsers.
   - **Sandbox & Network Isolation**: Guidelines for local loopback sockets and HTTP mock testing under sandbox constraints.
   - **Atomic File I/O & Serialization Integrity**: Crash-proof atomic write conventions and Decimal JSON type safety.

3. **[`TAXONOMY.ko.md`](TAXONOMY.ko.md)** (Korean)
   - **Task Identifier Taxonomy**: Consolidated reference for `P1~P3` (priority tasks), `P-1~P-5` (performance goals), `A~D` (CBO nine core tasks), and `S-1~S-7` (syntax completeness).

4. **[`PROTOCOL_EMULATION_BOUNDARIES.md`](PROTOCOL_EMULATION_BOUNDARIES.md)**
   - **Multi-Protocol Emulation Boundaries & Scope**: TDS, PGWire, and MySQL wire protocol emulation boundaries, packet and token handling, and tolerant virtual no-op policies.

5. **Project Roadmap & Backlog**:
   - **[`TODO.md`](../TODO.md)**: Active engineering backlog, next priority targets (Priority 1 Durability Sprint, Priority 2 App Expansion, Priority 3 Post-1.0 Roadmap), and archived milestone completions (M1~M3).
   - **[`ROADMAP.md`](../ROADMAP.md)**: Macro-level release milestones (Phases 1-6), architectural direction, and phase completion status.

6. **Active & Post-1.0 Architecture Plans**:
   - **Active Sprints (Priority 1 & 2)**:
     - **[`DURABILITY_VERIFICATION_PLAN.ko.md`](DURABILITY_VERIFICATION_PLAN.ko.md)** (Korean): Master plan for crash resilience, kill tests, soak stress tests, and dogfooding.
     - **[`APP_INTEGRATION_EXPANSION_PLAN.ko.md`](APP_INTEGRATION_EXPANSION_PLAN.ko.md)** (Korean): Master plan for 6 real-world app integrations (Answer, Vaultwarden, Metabase, Umami, NocoDB, Mattermost).
     - **[`ANSWER_INTEGRATION_PLAN.ko.md`](ANSWER_INTEGRATION_PLAN.ko.md)** (Korean): Apache Answer PGWire integration detail plan (first in order).
     - **[`VAULTWARDEN_INTEGRATION_PLAN.ko.md`](VAULTWARDEN_INTEGRATION_PLAN.ko.md)** (Korean): Vaultwarden (Rust + Diesel ORM) PGWire integration detail plan (second in order).
     - **[`AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md)** (Korean): Structural query routing refactoring architecture and verification report using symbol table and AST (eliminated 45 heuristics, 527ns verified, completed).
   - **Post-1.0 Architecture & Roadmap Plans**:
     - **[`IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)**: In-Memory OCC performance optimization history, WiredTiger beat roadmap, 104k tpmC architecture, and post-1.0 memory compaction (OCC-HYPER-2).
     - **[`SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md)** (Korean): Vertical server pipeline optimization from TDS network socket read coalescing to WAL file system fallocate and adaptive group commit (Phase 3/4).
     - **[`COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)** (Korean): 2-stage hybrid benchmark plan for complex query performance tuning (SSB Star Schema & Mini TPC-H Top 5 queries, DuckDB/ClickHouse comparison matrix).
     - **[`DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md`](DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md)** (Korean): DuckDB-style friendly SQL ergonomics master plan (`SELECT * EXCLUDE`, `FROM <table>;`, `GROUP BY ALL`) (Post-1.0 / v1.1.0).
     - **[`MODULAR_ARCHITECTURE_PLAN.ko.md`](MODULAR_ARCHITECTURE_PLAN.ko.md)** (Korean): Modular architecture, session encapsulation, and protocol decoupling plan.
     - **[`SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)** (Korean): Embedded Single Page Application (SPA) static web server architecture.
     - **[`IMMUTABLE_AUDIT_LOG_PLAN.md`](IMMUTABLE_AUDIT_LOG_PLAN.md)**: Architecture for SHA-256 hash-chained immutable audit logging for PCI-DSS v4.0 / SOC 2 compliance (Post-1.0 / v1.1.0).
     - **[`COLLATION_AND_LOCALE_PLAN.md`](COLLATION_AND_LOCALE_PLAN.md)**: Design for Microsoft SQL Server collation and locale support (Post-1.0 / v1.1.0).
     - **[`S3_PRESIGNED_STORAGE_PLAN.md`](S3_PRESIGNED_STORAGE_PLAN.md)**: Design for direct S3 pre-signed URL uploads and SigV4 client signer integration (Spec-Out Architecture / Reference).

7. **Production Integration Reports & Postmortems**:
   - **[`VAULTWARDEN_INTEGRATION_REPORT.ko.md`](VAULTWARDEN_INTEGRATION_REPORT.ko.md)** (Korean): Vaultwarden (Rust + Diesel ORM) 47 migrations execution, composite PK upsert, 8-byte timestamp encoding, and Bitwarden REST API E2E verification report.
   - **[`ANSWER_INTEGRATION_REPORT.ko.md`](ANSWER_INTEGRATION_REPORT.ko.md)** (Korean): Apache Answer Q&A platform 35 tables creation, VDBE multi-row batch identity generation, and REST API E2E verification report.
   - **[`GITEA_INTEGRATION_REPORT.ko.md`](GITEA_INTEGRATION_REPORT.ko.md)** (Korean): Gitea dual-protocol (TDS :1433 & PGWire :5433) live integration and 116 production tables full E2E lifecycle verification report.
   - **[`IOTA_ERP_INTEGRATION_REPORT.ko.md`](IOTA_ERP_INTEGRATION_REPORT.ko.md)** (Korean): IOTA SDK Go ERP integration, strict `pgx/v5` binary wire protocol compliance, and 47 schema migrations execution verification report.
   - **[`GRAFANA_INTEGRATION_REPORT.ko.md`](GRAFANA_INTEGRATION_REPORT.ko.md)** (Korean): Grafana 785 migrations execution, envelope encryption (`BYTEA`), CTE derived table isolation, and dual data source query verification report.
   - **[`CASDOOR_INTEGRATION_REPORT.ko.md`](CASDOOR_INTEGRATION_REPORT.ko.md)** (Korean): Casdoor 44-table creation, composite primary key (`PRIMARY KEY (owner, name)`) resolution, TDS `VARBINARY` JSON round-trip, and dual-protocol OIDC/RBAC E2E verification report.
   - **[`SOAK_TEST_1H_VERIFICATION_REPORT.ko.md`](SOAK_TEST_1H_VERIFICATION_REPORT.ko.md)** (Korean): 1-hour soak test verification report (720k ops, 393.5k commits, SQLSTATE 40001 mapping, and WiredTiger 500MB cache convergence analysis).
   - **[`ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md)** (Korean): Root cause analysis of DML/wildcard/keyword alias defects exposed during Gitea XORM live integration and architectural postmortem on "Human SQL vs Machine-Generated ORM SQL" test blind spots.
   - **[`TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md`](TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md)** (Korean): TDS RPC `sp_executesql` parameter resolution regression post-mortem, Beekeeper Studio connection crash root-cause analysis, and pure ordinal positional fallback restoration.
   - **[`QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md)** (Korean): Permanent 2-tier query execution architecture specification.
   - **[`BUG_ARCHIVE.ko.md`](../BUG_ARCHIVE.ko.md)** (Korean): 107 data integrity defect resolutions archive and interim methodology records (`BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1`).
   - **[`TEST_VERIFICATION_REPORT.ko.md`](TEST_VERIFICATION_REPORT.ko.md)** (Korean): Automated test suite verification report and race detection diagnostics.
   - **[`RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md)** (Korean): Release 1.0 architecture review, code audit, and feedback records.
   - **[`LIONSQL_DEEP_CODE_REVIEW.ko.md`](LIONSQL_DEEP_CODE_REVIEW.ko.md)** (Korean): Deep code review and subsystem architecture audit across all 390 source files (~350k lines).

8. **Historical Archives (`notes/archive/`)**:
   - See **[`notes/archive/README.md`](archive/README.md)** for 13 completed historical architectural plans (Windows daemon, over-specification cleanup, forward-only cursors, CLI unification, etc.) and original raw backlogs.
