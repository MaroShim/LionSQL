# SQLion Project Architecture & Engineering Guidelines

## 1. Project Overview
SQLion is a high-concurrency embedded OLTP database engine written in Go, packaged as a single standalone executable. It delivers enterprise-grade T-SQL stored procedure execution, ACID transaction durability, multiple wire protocols, and an embedded web management studio.

- **Storage Backends**: In-Memory OCC (Optimistic Concurrency Control with WAL & AOF compaction) and WiredTiger (Lock-free B+Tree with MVCC & hazard pointers).
- **Wire Protocols**:
  - PostgreSQL Wire Protocol v3 (TCP `:5433` by default)
  - Microsoft SQL Server TDS 7.x/8.0 Protocol (TCP `:1433` by default)
  - REST & JSON HTTP API / Web Studio (TCP `:8090` by default)
- **Execution Engine**: Native SQLite VDBE-style T-SQL Bytecode Virtual Machine with `sync.Pool` register reuse.

---

## 2. Core Architectural Invariant: Dual-Parser & Automatic Fallback Strategy

SQLion enforces a **Dual-Parser Architecture** to simultaneously achieve sub-microsecond parsing latency and 100% full-spec Microsoft T-SQL syntax compatibility:

```
                  [ T-SQL Client Script / Query ]
                                │
                                ▼
                 [ Primary: Native Pratt Parser ]
                 (pkg/proc/lexer.go, parser.go)
                                │
                    ┌───────────┴───────────┐
                 (Success)               (Failure)
                    │                       │
                    │                       ▼
                    │           [ Secondary: ANTLR4 Fallback ]
                    │           (pkg/proc/parser/antlr/)
                    │           - Official TSqlParser.g4
                    │           - ASTBuilder substring slice
                    │                       │
                    └───────────┬───────────┘
                                │
                                ▼
                 [ Unified AST: *proc.CreateProcStmt ]
                                │
                                ▼
                 [ Native VDBE Bytecode Compiler ]
                                │
                                ▼
                 [ Bytecode VM & Storage Execution ]
```

### Key Requirements & Principles:
1. **Primary Fast-Path (Native Pratt / Recursive Descent Parser)**:
   - Location: `pkg/proc/lexer.go`, `pkg/proc/parser.go`
   - Purpose: Zero-allocation, sub-microsecond parsing for high-frequency queries, standard DDL/DML, and core procedure control flow statements (`IF`, `WHILE`, `TRY...CATCH`, `SELECT...INTO`, `UPDATE`, `INSERT`, `DELETE`).
2. **Secondary Full-Spec Fallback (Official Microsoft T-SQL ANTLR4 Parser)**:
   - Location: `pkg/proc/parser/antlr/`
   - Purpose: Invoked automatically as a runtime fallback whenever the primary parser encounters advanced, vendor-specific, or legacy MSSQL syntax (e.g. `WITH RECOMPILE`, `WITH ENCRYPTION`, schema prefixes `dbo.ProcName`, bracket escapes `[dbo].[Proc]`, vendor options).
3. **Pluggable Decoupling (Circular Dependency Immunity)**:
   - `pkg/proc` defines the `ProcedureFallbackParser` hook and `RegisterProcedureFallback()` / `GetProcedureFallback()` API.
   - `pkg/proc/parser/antlr/ast_builder.go` registers itself via `init()`.
   - `pkg/proc` never directly imports `pkg/proc/parser/antlr`, preserving strict architectural hierarchy and zero circular imports.
4. **Permanent Invariance**:
   - Both parsers and the fallback mechanism **must be permanently maintained and tested**.
   - Any new syntax extension should first be verified against ANTLR, and dual parity must be confirmed in `pkg/proc/runtime/regression_test.go`.

---

## 3. Core Architectural Invariant: Database Isolation Strategy (Instance-Based Isolation)

SQLion adopts **Instance-Level Isolation (Process / Container per Database)** as a core architectural invariant, rather than multi-database catalog multiplexing (`USE <dbname>`) inside a single process:

1. **Architectural Rationale**:
   - **Fault & Resource Isolation**: Prevents runaway queries, memory leaks, or file descriptor saturation in one database from impacting others.
   - **Security & Blast Radius**: Eliminates cross-database data leaks and ensures strict physical isolation of data files, WAL/AOF logs, TDE encryption keys, and network listening ports.
   - **Independent Lifecycle**: Enables independent scaling, upgrading, point-in-time recovery (PITR), and restarts per database.
   - **KISS & Zero Redundant Complexity**: Eliminates cross-database catalog locking, session database context switching overhead, and distributed lock contention in the database kernel.
2. **Permanent Specification Invariant**:
   - **`USE <dbname>` Syntax**: Explicitly **Spec Out**. Each SQLion process serves exactly one isolated database catalog per data directory (`--data-dir`).
   - **Multi-Tenant Deployment Standard**: Multi-tenancy and multiple databases are deployed via lightweight SQLion standalone processes/containers with dedicated ports and directories (`--port`, `--data-dir`).

---

## 4. Core Architectural Invariant: Transaction Isolation & Concurrency Model

SQLion defines **Non-Blocking Snapshot Isolation / Read Committed with Optimistic/Hazard Concurrency Control** as its canonical transaction processing invariant across both storage engines:

1. **Architectural Principles**:
   - **Non-Blocking Reads (Readers Never Block Writers, Writers Never Block Readers)**: Both backends completely eliminate traditional 2PL (Two-Phase Locking) read locks. Queries always read from consistent snapshots without acquiring shared locks.
   - **Private Workspace Isolation**: All mutation operations (INSERT, UPDATE, DELETE) are buffered inside transaction-private workspaces until commit, guaranteeing zero dirty reads and zero uncommitted leakage.
   - **Storage Engine Concurrency Profiles**:
     - **In-Memory OCC (`pkg/storage/memengine`)**: Pure OCC (Optimistic Concurrency Control) using per-record monotonic integer versions (`Version uint64`). Concurrency conflicts are validated atomically at the commit phase (`Commit()`), triggering immediate fail-fast rollback on write-write skew (`ErrWriteConflict`).
     - **WiredTiger MVCC (`pkg/storage/wiredtiger`)**: Disk-based MVCC B+Tree with hazard pointers and transaction timestamps. Concurrent conflicting writes on identical keys are detected during write/commit operations (`WT_ROLLBACK`).
2. **Permanent Specification Invariant**:
   - **Standard Isolation Level**: Canonical Snapshot / Read Committed semantics are enforced across all wire protocols (PGWire, TDS, HTTP).
   - **Pessimistic Read Locks (`READ UNCOMMITTED`, Heavy Distributed 2PL, SSI Graph Tracking)**: Explicitly **Spec Out / Deferred**. Advanced isolation levels that introduce false abort penalties, serializable dependency graphs, or dirty read compromises are excluded in favor of high-throughput non-blocking OLTP execution.

---

## 5. Engineering Guidelines & Development Pipeline

### 3-Stage Verification Pipeline (Build ➔ Test ➔ Commit)
All modifications, refactoring, and feature additions must strictly pass the following 3 stages before committing:

1. **Build**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench
   ```
2. **Test (Race-Free Verification)**:
   ```bash
   go test -tags wiredtiger -count=1 -v -race ./...
   ```
   Must achieve 100% PASS across all packages with 0 data races.
3. **Commit**:
   - Conventional Commits format (`feat(...)`, `fix(...)`, `refactor(...)`, etc.).
   - Strictly avoid decorative emojis in commit messages, comments, and code.
