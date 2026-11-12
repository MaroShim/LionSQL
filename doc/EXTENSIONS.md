# LionSQL Extension & Driver Development Guide

> - **Created**: 2026-09-23
> - **Last Updated**: 2026-11-12

LionSQL is engineered around modular, decoupled interface contracts (Open-Closed Principle). Developers can extend LionSQL with custom storage backends, secondary procedure parsers, custom scalar functions, alternative wire protocol frontends, and pluggable collation providers without modifying the database core.

---

## 1. Extension Architecture Overview

```
                      [ Client Wire Protocol Request ]
                                     │
                                     ▼
                      [ pkg/wire (Protocols SPI) ]
                        (PostgreSQL v3 / TDS 7.x)
                                     │
                                     ▼
                      [ pkg/proc (Parser SPI) ]
                     Native Pratt ──► ANTLR4 Fallback
                                     │
                                     ▼
                    [ pkg/execution (Volcano VM) ]
                                     │
                                     ▼
                     [ pkg/storage (Storage SPI) ]
                 ┌───────────────────┴───────────────────┐
                 ▼                                       ▼
        [ In-Memory OCC Engine ]              [ WiredTiger Engine ]
```

---

## 2. Storage Engine SPI (`pkg/storage`)

Custom storage engines (such as LSM-Tree, RocksDB, or distributed key-value stores) implement the `storage.Engine` interface contract.

### 2.1 Interface Contracts

```go
package storage

// Engine is the top-level contract for the database storage layer.
type Engine interface {
    Open(dir string) error
    Close() error
    NewSession() (Session, error)
    CreateTable(schema *TableSchema) error
    DropTable(tableName string) error
    GetTable(tableName string) (*TableSchema, error)
    ListTables() ([]*TableSchema, error)
    CreateIndex(index *IndexSchema) error
    DropIndex(tableName, indexName string) error
}

// Session represents an active transaction context.
type Session interface {
    BeginTx(opts TxOptions) (Transaction, error)
    Close() error
}

// Transaction represents an ACID transaction unit.
type Transaction interface {
    Insert(tableName string, record Record) error
    Update(tableName string, pk Value, record Record) error
    Delete(tableName string, pk Value) error
    Get(tableName string, pk Value) (Record, error)
    Scan(tableName string) (Cursor, error)
    IndexScan(tableName string, indexName string, low, high *Value) (Cursor, error)
    Commit() error
    Rollback() error
}

// Cursor iterates over table records.
type Cursor interface {
    Next() bool
    Record() (Record, error)
    Close() error
}
```

### 2.2 Implementing a Custom Storage Engine
To implement a custom storage backend:
1. Define a struct satisfying `storage.Engine`.
2. Implement record serialization converting `storage.Record` to byte sequences and vice versa using deterministic encoding (e.g., binary tag-length-value).
3. Ensure transaction isolation guarantees snapshot isolation or serializability to prevent dirty reads.
4. Support defensive cleanups via `defer Close()` on cursors and sessions.

---

## 3. Secondary Parser Extension SPI (`pkg/proc`)

LionSQL enforces a dual-parser architecture where unknown or advanced vendor syntax falls back to secondary parsers without creating circular dependencies.

### 3.1 Fallback Parser Registration

```go
package proc

// ProcedureFallbackParser parses custom or vendor-specific T-SQL ASTs.
type ProcedureFallbackParser func(script string) (*CreateProcStmt, error)

// RegisterProcedureFallback registers the fallback handler.
func RegisterProcedureFallback(p ProcedureFallbackParser)

// GetProcedureFallback retrieves the active fallback handler.
func GetProcedureFallback() ProcedureFallbackParser
```

### 3.2 Example Fallback Implementation

```go
package myparser

import (
    "github.com/lionsql/lionsql/pkg/proc"
)

func init() {
    proc.RegisterProcedureFallback(parseCustomSyntax)
}

func parseCustomSyntax(script string) (*proc.CreateProcStmt, error) {
    // Custom AST extraction logic
    stmt := &proc.CreateProcStmt{
        Name: "custom_proc",
        // Populate parameters and statements
    }
    return stmt, nil
}
```

---

## 4. Wire Protocol Extension SPI (`pkg/wire`)

LionSQL natively supports PostgreSQL Wire Protocol v3 and Microsoft SQL Server TDS. Developers can implement alternative wire protocol handlers (such as MySQL Protocol or Redis Serialization Protocol).

### 4.1 Wire Handler Lifecycle
Every wire listener follows a unified connection lifecycle:
1. **TCP Listener**: Bind to dedicated port and accept raw client connections.
2. **Handshake & Authentication**: Exchange protocol capability flags and authenticate against `pkg/auth.Manager`.
3. **Session Allocation**: Obtain an isolated session via `engine.NewSession()`.
4. **Command Loop**: Parse client packets, dispatch SQL queries to `api.Server.ExecuteSQL()`, and serialize result sets into protocol-specific wire frames.
5. **Connection Teardown**: Release session resources deterministically upon EOF or network errors.

---

## 5. User-Defined Functions (UDF) & Collation Extension SPI

LionSQL defines a modular extension model for custom user functions and multilingual collations, catering to both in-process application developers and standalone database deployments:

```
                            [ Extensibility Framework ]
                                           │
                   ┌───────────────────────┼───────────────────────┐
                   ▼                       ▼                       ▼
      [ Track 1: Go Native UDF ]   [ Track 2: Wasm UDF ]  [ Pluggable Collation SPI ]
      - proc.RegisterScalarFunction  - Standalone Wasm      - proc.RegisterCollation
      - Zero FFI (< 5ns)            - wazero sandbox       - Fast-Path No-Collation (0.5ns)
      - Typed storage.Value         - Memory isolation     - Pay-as-you-go multilingual
```

### 5.1. Track 1: Go Native Function Registration SPI (`pkg/proc`)
For developers embedding LionSQL directly inside Go applications or microservices:
```go
// Register a high-performance Go function directly into the SQL engine
proc.RegisterScalarFunction("CUSTOM_HASH", func(args []storage.Value) storage.Value {
    if len(args) == 0 || args[0].IsNull {
        return storage.NullValue(storage.TypeVarChar)
    }
    return storage.StringValue(myFastHash(args[0].SVal))
})
```
- **Performance**: Zero serialization penalty; executes with native Go call speed (< 5ns per invocation).
- **Type Safety**: Operates directly on typed `storage.Value` primitives.
- **Portability**: Pure Go; compiles cleanly on Windows, macOS, and Linux without Cgo dependencies.

### 5.2. Track 2: WebAssembly (Wasm) Dynamic Sandboxed UDFs (`pkg/extension`)
For standalone database deployments where operators need to dynamically load custom functions without recompiling or restarting the LionSQL server binary:
```sql
CREATE FUNCTION compute_risk_score AS WASM 'plugins/risk_model.wasm';
```
- **Zero-CGO Pure-Go Runtime**: Powered by [wazero](https://wazero.io/), maintaining LionSQL's single static binary invariant.
- **Multi-Language Authoring**: Users can author plugins in Go (TinyGo), Rust, C, or Zig, compiling to standard WebAssembly.
- **Strict Fault Isolation**: Executes within an isolated virtual memory sandbox. Unhandled panics or memory violations are trapped cleanly, ensuring zero crash impact on the core database kernel.

### 5.3. Pluggable Collation Provider SPI (`pkg/proc`, `pkg/storage`)
To support complex multilingual sorting without bloating the core engine binary with multi-megabyte Unicode ICU tables and without incurring map lookup overhead on hot paths, LionSQL adopts a **Two-Tier Pre-Resolved (Fast-Path Native vs. Pre-Bound Go UDF)** collation architecture:
1. **Cold-Path Pre-Binding & Normalization**: At query plan compilation and schema resolution time, collation names are pre-resolved directly into function pointers (`CollationCompareFunc`) or pre-allocated integer slots (`[RULE:BoundedLookup]`). Default collations (`SQL_Latin1_General_CP1_CI_AS`, `DATABASE_DEFAULT`, `SERVER_DEFAULT`) resolve directly to `compareFn = nil`.
2. **Hot-Path Zero-Overhead Fast-Path (Zero Map Lookups)**: Inside high-frequency B+Tree index searches and table scans, the engine executes a single nil pointer check (`if compareFn == nil`) to dispatch inlined `strings.EqualFold` or `bytes.Compare` (0.5ns, zero allocations).
3. **Pre-Bound Pluggable UDF Execution**: When specialized collations (e.g. `Korean_Wansung_CI_AS`, accent sensitivity, custom business sort orders) are requested, the engine invokes the pre-bound function pointer directly (`compareFn(a, b)`), paying zero map lookup or locking penalties on hot execution paths:
```go
// Register a custom collation provider into the engine at startup
proc.RegisterCollation("KOREAN_WANSUNG_CI_AS", func(a, b string) int {
    // Custom language-specific sort comparison logic
    return myKoreanCompare(a, b)
})
```

---

## 6. Extension Sandboxing & Safety Guidelines

In accordance with LionSQL Engineering Guidelines:
- **Panic Recovery (`[RULE:PanicRecovery]`)**: All extension entrypoints and callbacks must be wrapped in `defer func() { if r := recover(); r != nil { ... } }()` panic interceptors to prevent third-party crashes from terminating the database engine.
- **Defensive Boundary Checks (`[RULE:DefensiveIndexing]`)**: Validate all slice and buffer accesses against bounds before indexing.
- **Resource Hygiene (`[RULE:HandleHygiene]`)**: Ensure every open file descriptor, network socket, and cursor is closed cleanly via `defer Close()`.
- **Thread Safety (`[RULE:ConcurrencySafety]`)**: Custom extensions must acquire mutexes in canonical hierarchical order to prevent deadlocks and pass `go test -race` with 0 data races.
