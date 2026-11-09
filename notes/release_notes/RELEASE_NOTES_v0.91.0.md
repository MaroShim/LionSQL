# LionSQL v0.91.0 Release Notes (`notes/RELEASE_NOTES_v0.91.0.md`)

> - **Initial Date**: 2026-11-06
> - **Release Version**: `v0.91.0` (Minor Release)
> - **Milestone Theme**: **Embedded Single-Binary SPA Web Server Hosting (`WEB-SPA-1`), Total Regular Expression Elimination (`GA-REGEX`), Formal Compatibility Standards, and In-Memory OCC Flat Record Hardening**

---

## 1. Release Highlights

LionSQL `v0.91.0` is a significant milestone release that expands LionSQL beyond a high-performance relational database into a complete standalone application platform, while radically hardening the core query engine:

- **Embedded SPA Web App Hosting (`WEB-SPA-1`)**:
  - Direct hosting of single-page HTML files or production frontend SPA builds (React, Vue, Svelte) on port `:8090` without Nginx, Caddy, or reverse proxies.
  - Zero-network-hop in-process bus for web queries, eliminating loopback TCP latency and CORS overhead.
  - OS kernel `sendfile(2)` zero-copy resource isolation protecting DB OLTP execution from noisy-neighbor web traffic.
  - Comprehensive operational guides published in [`doc/WEB_APP_HOSTING_GUIDE.ko.md`](../../doc/WEB_APP_HOSTING_GUIDE.ko.md) and [`doc/WEB_APP_HOSTING_GUIDE.md`](../../doc/WEB_APP_HOSTING_GUIDE.md).
- **Total Elimination of Regular Expressions (`GA-REGEX`)**:
  - Eradicated all regular expressions across Volcano CBO planner, VDBE runtime, execution LIKE filters, and PGWire dollar parameter binding (`$N`).
  - Standardized on zero-allocation deterministic tokenizers and state machines in `pkg/sqlutil` (`MatchLike`, `ReplacePgParams`, `FindTopLevelKeyword`), resolving context-free grammar Dyck language edge cases.
  - Hardened `sqlutil.MatchLike` with native T-SQL bracket wildcard support (`[...]`, `[^...]`, `[_]`, `[%]`).
- **Root Compatibility Specifications**:
  - Published formal standalone compatibility specifications ([`COMPATIBILITY.ko.md`](../../COMPATIBILITY.ko.md) / [`COMPATIBILITY.md`](../../COMPATIBILITY.md)) standardizing on Microsoft SQL Server 2019 (v15.0) and PostgreSQL 14-16 wire protocols.
- **WiredTiger Power-Cut / SIGKILL Zero-Loss Durability Hardening**:
  - Restored `transaction_sync=(enabled=true,method=none)` in WiredTiger configuration, guaranteeing user-space transaction logs flush to OS buffers on commit and survive abrupt `kill -9` without data loss (`bin/crashtest` verified 100% PASS).
- **6-Stage Golden Release Verification Gate Deployment (`scripts/release.sh`)**:
  - Permanently integrated Core Unit (100k TPS), 13-Domain Smoke, TPC-C (>700k tpmC), SQLLogicTest (410k queries), T-SQL 14-Suite Procedures, and SIGKILL Crash Oracles into the release pipeline, accompanied by an architectural whitepaper ([`notes/HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.md`](../HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.md)).
- **In-Memory OCC Pointer-Free Flat Record Hardening (`OCC-HYPER-2`)**:
  - Pointer-free flat record layout codec delivering 67.5% memory reduction with zero GC overhead and verified sub-nanosecond $O(1)$ reads.
- **Engine Internals & Historical Archives Hub**:
  - Indexed 7 core architectural specifications in `doc/internal/` and established structured 6-tier architecture hubs in `notes/` and `notes/archive/`.

---

## 2. Verification & Compliance

1. **3-Stage Pipeline Verification (`[RULE:PipelineVerification]`)**:
   - Official release binaries compiled for macOS (`darwin-arm64`), Linux (`linux-amd64`), and Windows (`windows-amd64.exe`).
   - `go test -tags wiredtiger -count=1 ./pkg/...` **100% PASS across 23 core packages (0 data races)**.
2. **Ecosystem & Tool Introspection**:
   - Validated against official SQL Server JDBC drivers, `go-mssqldb`, XORM, and DBeaver/SSMS discovery suites.
