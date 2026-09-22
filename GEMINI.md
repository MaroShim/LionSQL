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

## 3. Engineering Guidelines & Development Pipeline

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
