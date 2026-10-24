# LionSQL Engineering Notes & Context Handover (`notes/`)

> [한국어 문서 (Korean Version)](README.ko.md)

This directory serves as a persistent knowledge base (Shared Brain & Handover Notes) designed to help new AI agents or collaborating engineers quickly synchronize on **underlying architectural context, rejected alternatives (Negative Knowledge), production pitfalls, and user engineering philosophies** that cannot be fully captured from source code or official end-user documentation (`doc/`) alone when switching across machines or sessions.

---

## Document Index

1. **[`ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)** (Korean)
   - **Core Invariants**: Architectural rationale and explicit spec-out decisions.
   - **Engineering Philosophies & Collaboration Protocols**: Absolute rules including the 3-stage verification pipeline (`Build` -> `Test -race` -> `Commit`), no decorative emojis, radical KISS/YAGNI, and file splitting guidelines.
   - **Core Technology Stack**: VDBE bytecode virtual machine, dual-parser architecture, dual-track UDF execution, and multi-wire protocols.

2. **[`PITFALLS_AND_TRICKS.ko.md`](PITFALLS_AND_TRICKS.ko.md)** (Korean)
   - **Landmines & Fragile Spots**: Sensitive implementation details across the codebase.
   - **Circular Import Immunity**: SPI pluggable registration decoupling `pkg/proc` from ANTLR4 fallback parsers.
   - **Sandbox & Network Isolation**: Guidelines for local loopback sockets and HTTP mock testing under sandbox constraints.
   - **Atomic File I/O & Serialization Integrity**: Crash-proof atomic write conventions and Decimal JSON type safety.

3. **[`TAXONOMY.ko.md`](TAXONOMY.ko.md)** (Korean)
   - **Task Identifier Taxonomy**: Consolidated reference for `P1~P3` (priority tasks), `P-1~P-5` (performance goals), `A~D` (CBO nine core tasks), and `S-1~S-7` (syntax completeness).

4. **[`PROTOCOL_EMULATION_BOUNDARIES.md`](PROTOCOL_EMULATION_BOUNDARIES.md)**
   - **Multi-Protocol Emulation Boundaries & Scope**: TDS, PGWire, and MySQL wire protocol emulation boundaries, packet and token handling, and tolerant virtual no-op policies.

5. **Project Roadmap & Backlog**:
   - **[`TODO.ko.md`](../TODO.ko.md)** (Korean): Active engineering backlog, next priority targets (P1, P2, P3), CBO and performance matrices, and recent milestone completions.
   - **[`ROADMAP.md`](../ROADMAP.md)**: Macro-level release milestones (Phases 1-6), architectural direction, and phase completion status.

6. **Domain Architecture & Execution Plans**:
   - **[`UNIFIED_CLI_PLAN.md`](UNIFIED_CLI_PLAN.md)**: Master plan for unifying the single binary (`bin/lionsql`) and restructuring CLI lifecycle (Completed).
   - **[`DOCUMENTATION_CLEANUP_PLAN.md`](DOCUMENTATION_CLEANUP_PLAN.md)**: Four-stage documentation restructuring master plan for v0.90.0 feature freeze (Completed).
   - **[`IMMUTABLE_AUDIT_LOG_PLAN.md`](IMMUTABLE_AUDIT_LOG_PLAN.md)**: Architecture for SHA-256 hash-chained immutable audit logging for PCI-DSS v4.0 / SOC 2 compliance.
   - **[`S3_PRESIGNED_STORAGE_PLAN.md`](S3_PRESIGNED_STORAGE_PLAN.md)**: Design for direct S3 pre-signed URL uploads and SigV4 client signer integration.
   - **[`COLLATION_AND_LOCALE_PLAN.md`](COLLATION_AND_LOCALE_PLAN.md)**: Design for Microsoft SQL Server collation and locale support.
   - **[`SQL_ABSTRACTION_LAYER_PLAN.md`](SQL_ABSTRACTION_LAYER_PLAN.md)**: Design for SQL abstraction layer unifying multiple storage backends.
   - **[`ANTLR_PARSER_COMPACTION_PLAN.md`](ANTLR_PARSER_COMPACTION_PLAN.md)**: Memory and binary compaction plan for ANTLR grammar parser.
   - **[`TOOL_AUDIT_PLAN.ko.md`](TOOL_AUDIT_PLAN.ko.md)** (Korean): GUI client tools (SSMS, DbGate, DBeaver, Beekeeper Studio) query compatibility and virtual catalog audit plan (Completed).
   - **[`OVER_SPECIFICATION_REMEDIATION_PLAN.md`](OVER_SPECIFICATION_REMEDIATION_PLAN.md)** (Korean): Master remediation plan eliminating tool-specific over-specifications and ad-hoc hardcodings (In Progress).

7. **Completed Reports & Historical Archives**:
   - **[`TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md`](TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md)** (Korean): TDS RPC `sp_executesql` parameter resolution regression post-mortem, Beekeeper Studio connection crash root-cause analysis, and pure ordinal positional fallback restoration.
   - **[`BUG_ARCHIVE.ko.md`](../BUG_ARCHIVE.ko.md)** (Korean): 107 data integrity defect resolutions archive and interim methodology records (`BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1`).
   - **[`TEST_VERIFICATION_REPORT.ko.md`](TEST_VERIFICATION_REPORT.ko.md)** (Korean): Automated test suite verification report and race detection diagnostics.
   - **[`RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md)** (Korean): Release 1.0 architecture review, code audit, and feedback records.
