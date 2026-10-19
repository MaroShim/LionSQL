# LionSQL v0.90.0 Feature Freeze Documentation Cleanup Master Plan

This document establishes the official 4-stage execution plan for comprehensive documentation alignment following the **LionSQL v0.90.0 Feature Freeze**.

---

## High-Level Execution Matrix

| Stage | Target Documents | Core Objectives | Status |
| :---: | :--- | :--- | :---: |
| **Stage 1** | `CHANGELOG.md` | **0.90.0 Release Block Polish**: Summarize major Feature Freeze deliverables (Single-Binary CLI, Web Studio Realtime Stream/Logs/DDL, Global Memory Governor & Circuit Breaker, CBO & JOIN materialization, Virtual Catalog Single-DB standardization) in Keep a Changelog standard. | **Completed (`[x]`)** |
| **Stage 2** | `README.md`<br>`README.ko.md` | **Root README Modernization**: Update version headers to `0.90.0 Feature Freeze`, unify CLI commands on `./bin/lionsql` (foreground, daemon, subcommands), feature modern Web Studio views (Realtime Live Stream, Request Logs, Schema DDL Column Addition), align database names (`postgres`, `master`) and verified GUI tools. | **Completed (`[x]`)** |
| **Stage 3** | `doc/` Directory | **Legacy Term & Configuration Sweep**: Sweep `doc/` directory to replace legacy `dbname=sqlion` with canonical `postgres`/`master`, purge unsupported TablePlus references, convert deprecated `bin/server` / `server -dir` commands to `lionsql`, and sync `v0.90` to `v0.90.0`. | **Completed (`[x]`)** |
| **Stage 4** | `ROADMAP.md` | **Roadmap Status Synchronization**: Reflect Phase 1~5 completion, Phase 6 v0.90.0 Feature Freeze milestone attainment, and update exit criteria gates towards v1.0.0 LTS production release. | **In Progress (`[/]`)** |

---

## Detailed Action Plan by Stage

### Stage 1: CHANGELOG.md 0.90.0 Release Block Polish (Completed)
- [x] Promoted `[Unreleased]` changes to `## [0.90.0] - 2026-10-19`.
- [x] Added **Global Memory Governor & Query Circuit Breaker (`MEM-1`)** milestone.
- [x] Aligned version references to `const Version = "0.90.0"` SSOT.
- [x] Documented virtual catalog standardization (`postgres` on OID 1, `master` on DB ID 1) and long-lived SSE streaming latency isolation.
- [x] Tagged git commit as annotated tag `v0.90.0`.

---

### Stage 2: Root README.md & README.ko.md Modernization (Completed)
- [x] **Version Alignment**: Added `v0.90.0 Feature Freeze` badge and version header.
- [x] **Unified CLI Instructions (`bin/lionsql`)**:
  - Replaced legacy `bin/server` build and execution commands with `bin/lionsql`.
  - Documented foreground execution (`./bin/lionsql`, `./bin/lionsql -dir=./data`).
  - Documented daemon commands (`./bin/lionsql start -dev`, `status`, `restart -dev`, `stop`).
  - Documented operational subcommands (`serve`, `migrate`, `backup`, `restore`, `version`, `doctor`).
- [x] **Web Studio Feature Suite Overview**:
  - Highlighted the 4 new production management interfaces:
    1. Realtime Live Stream (`/api/realtime`, 200-event circular ring buffer).
    2. Request Activity Logs (`/api/logs/requests`, latency profiler with 3s auto-refresh).
    3. Visual Column Addition GUI modal (`+ Add Column` dynamic DDL without SQL typing).
    4. Multi-Row Bulk Actions & Foreign Key Jump links (`↗`).
- [x] **Client Connection & Database Name Alignment**:
  - Updated DSN examples to `dbname=postgres` (PGWire) and `database=master` (TDS).
  - Standardized recommended GUI clients: Beekeeper Studio, DBeaver, Navicat, Azure Data Studio, SSMS.
- [x] **Verification Pipeline Alignment**:
  - Updated 3-stage pipeline build commands in Section 8/9 to reference all 4 build targets.

---

### Stage 3: doc/ Directory Legacy Term & Configuration Sweep (Completed)
- [x] **Database Name Standardization (`sqlion` ➔ `postgres` / `master`)**:
  - [`doc/CLIENT_TOOL_INTEGRATION.md`](doc/CLIENT_TOOL_INTEGRATION.md): Updated connection tables and DSN examples across Go, Python, JDBC, and GUI setup cards.
  - [`doc/TROUBLESHOOTING.md`](doc/TROUBLESHOOTING.md): Updated CLI connection troubleshooting examples.
  - [`doc/DATA_DICTIONARY.md`](doc/DATA_DICTIONARY.md): Updated database name description.
- [x] **Purge Unsupported TablePlus References**:
  - [`doc/WHY_TSQL_GO_WIREDTIGER.md`](doc/WHY_TSQL_GO_WIREDTIGER.md)
  - [`doc/PGWIRE_COMPATIBILITY_REPORT.md`](doc/PGWIRE_COMPATIBILITY_REPORT.md)
  - [`doc/PROTOCOL_EMULATION_GUIDELINES.md`](doc/PROTOCOL_EMULATION_GUIDELINES.md)
  - Standardized on Beekeeper Studio, DBeaver, Navicat.
- [x] **Executable Command Modernization**:
  - Replaced `./bin/server` with `./bin/lionsql` across operational guides (`doc/OPERATIONS.md`, `doc/SCHEMA_MIGRATIONS_CLI.md`, `doc/ARCHITECTURE.md`, `doc/USER_MANUAL.ko.md`, `doc/REFERENCE_MANUAL.ko.md`, `doc/HIGH_AVAILABILITY.md`, `doc/ADR.md`).
- [x] **Version String Alignment**:
  - [`doc/HTTP_API_FUNCTIONALITY_REPORT.md`](doc/HTTP_API_FUNCTIONALITY_REPORT.md): Updated `v0.90` / `0.90` to `v0.90.0` / `0.90.0`.

---

### Stage 4: ROADMAP.md Status Synchronization (Active)
- [ ] **Evolutionary Milestones**:
  - Mark Phase 1~5 as 100% completed.
  - Formally record `v0.90.0 Feature Freeze` under Phase 6.
  - Define remaining path toward official `v1.0.0 LTS`.
- [ ] **Exit Criteria Verification Status**:
  - Update Gate 1~5 statuses reflecting multi-protocol pass rates, zero-allocation memory governor, and single-binary CLI maturity.
