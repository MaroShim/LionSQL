# SQLion Extension & Driver Development Guide

SQLion is engineered around modular, decoupled interface contracts (Open-Closed Principle). Developers can extend SQLion with custom storage backends, secondary procedure parsers, custom scalar functions, and alternative wire protocol frontends without modifying the database core.

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

SQLion enforces a dual-parser architecture where unknown or advanced vendor syntax falls back to secondary parsers without creating circular dependencies.

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
    "github.com/sqlion/sqlion/pkg/proc"
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

SQLion natively supports PostgreSQL Wire Protocol v3 and Microsoft SQL Server TDS. Developers can implement alternative wire protocol handlers (such as MySQL Protocol or Redis Serialization Protocol).

### 4.1 Wire Handler Lifecycle
Every wire listener follows a unified connection lifecycle:
1. **TCP Listener**: Bind to dedicated port and accept raw client connections.
2. **Handshake & Authentication**: Exchange protocol capability flags and authenticate against `pkg/auth.Manager`.
3. **Session Allocation**: Obtain an isolated session via `engine.NewSession()`.
4. **Command Loop**: Parse client packets, dispatch SQL queries to `api.Server.ExecuteSQL()`, and serialize result sets into protocol-specific wire frames.
5. **Connection Teardown**: Release session resources deterministically upon EOF or network errors.

---

## 5. User-Defined Functions (UDF) Extension SPI: Dual-Track Architecture

SQLion defines a complementary dual-track extension model for custom user functions, catering to both in-process application developers and standalone database deployments:

```
                            [ User-Defined Functions (UDF) ]
                                          │
                  ┌───────────────────────┴───────────────────────┐
                  ▼                                               ▼
     [ Track 1: Go Native In-Process SPI ]           [ Track 2: WebAssembly (Wasm) Sandbox ]
     - Target: Go Embedded Library Users             - Target: Standalone Database Deployments
     - Binding: proc.RegisterScalarFunction(...)    - Binding: CREATE FUNCTION ... AS WASM
     - Overhead: Zero (Direct Function Call)         - Security: Pure-Go Memory Sandboxing (wazero)
     - Cross-Platform: Windows, Mac, Linux           - Isolation: Golden Rule 26 Panic Recovery
```

### 5.1. Track 1: Go Native Function Registration SPI (`pkg/proc`)
For developers embedding SQLion directly inside Go applications or microservices:
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
For standalone database deployments where operators need to dynamically load custom functions without recompiling or restarting the SQLion server binary:
```sql
CREATE FUNCTION compute_risk_score AS WASM 'plugins/risk_model.wasm';
```
- **Zero-CGO Pure-Go Runtime**: Powered by [wazero](https://wazero.io/), maintaining SQLion's single static binary invariant.
- **Multi-Language Authoring**: Users can author plugins in Go (TinyGo), Rust, C, or Zig, compiling to standard WebAssembly.
- **Strict Fault Isolation**: Executes within an isolated virtual memory sandbox. Unhandled panics or memory violations are trapped cleanly (Golden Rule 26), ensuring zero crash impact on the core database kernel.

---

## 6. Extension Sandboxing & Safety Guidelines

In accordance with SQLion Engineering Guidelines:
- **Panic Recovery (Rule 26)**: All extension entrypoints and callbacks must be wrapped in `defer func() { if r := recover(); r != nil { ... } }()` panic interceptors to prevent third-party crashes from terminating the database engine.
- **Defensive Boundary Checks (Rule 11)**: Validate all slice and buffer accesses against bounds before indexing.
- **Resource Hygiene (Rule 24)**: Ensure every open file descriptor, network socket, and cursor is closed cleanly via `defer Close()`.
- **Thread Safety (Rule 10)**: Custom extensions must acquire mutexes in canonical hierarchical order to prevent deadlocks and pass `go test -race` with 0 data races.
