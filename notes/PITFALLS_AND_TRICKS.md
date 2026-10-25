# LionSQL Architectural Pitfalls & Engineering Landmines (`notes/PITFALLS_AND_TRICKS.md`)

This document records subtle architectural landmines, runtime hazards, and critical anti-patterns discovered across the LionSQL codebase.

---

## 1. Circular Import Immunity (Dual-Parser Decoupling)
- **Problem**: `pkg/proc` (Pratt parser, AST definitions) and `pkg/proc/parser/antlr` (ANTLR4 T-SQL fallback) require mutual awareness. Direct imports cause compile-time circular dependency cycles (`import cycle not allowed`).
- **Forbidden Anti-Pattern**: Never import `lionsql/pkg/proc/parser/antlr` inside `pkg/proc`.
- **Required Idiom**: Decouple via SPI in `pkg/proc`:
  ```go
  // pkg/proc defines SPI
  type ProcedureFallbackParser interface { ... }
  func RegisterProcedureFallback(p ProcedureFallbackParser) { ... }
  ```
  `pkg/proc/parser/antlr` registers itself in `init()`. Binaries or tests needing ANTLR include blank imports:
  ```go
  import _ "lionsql/pkg/proc/parser/antlr"
  ```

---

## 2. Build Tag Invariant: WiredTiger vs. Pure-Go
- **Problem**: WiredTiger relies on native CGo. Omitting `-tags wiredtiger` links no-op stub implementations (`wt_stub.go`), silently failing disk B+Tree tests and benchmarks.
- **Required Build Commands**:
  - Production Unix (macOS / Linux):
    ```bash
    go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
    go test -tags wiredtiger -count=1 -v -race ./...
    ```
  - Pure-Go / Windows Standalone (Zero CGo, OCC memory engine):
    ```bash
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql
    ```

---

## 3. Sandbox Network Isolation (Loopback Sockets)
- **Problem**: In sandboxed environments (`BypassSandbox: false`), local loopback (`127.0.0.1`) network sockets may be blocked, causing replication streaming or HTTP tests to hang with deadline timeouts.
- **Required Idiom**: Run live socket integration tests (e.g., `pkg/replication`, `pkg/api/replication_integration_test.go`, external client tests) with `BypassSandbox: true`.

---

## 4. `storage.Decimal` & JSON Serialization Integrity
- **Problem**: `storage.Decimal` holds unexported fields (`value *big.Int`, `scale int32`). Standard `json.Marshal` serializes unexported fields as empty objects (`{}`), causing `nil` pointer panics on deserialization.
- **Forbidden Anti-Pattern**: Never serialize `storage.Decimal` without string-based custom marshalling.
- **Required Idiom**: Ensure `MarshalJSON` and `UnmarshalJSON` encode/decode strings (`"123.45"`). Verify round-trip fidelity in CBO stats (`cbo_stats.json`) and catalog serialization.

---

## 5. Non-Destructive Atomic File I/O (Golden Rules 1 & 5)
- **Problem**: Direct writes (`os.WriteFile`, `os.OpenFile` with `O_TRUNC`) corrupt config files and WAL logs on power halts or `SIGKILL`.
- **Required Idiom**:
  1. Normalize path: `cleanPath := filepath.Clean(targetPath)`
  2. Write temporary file: `tmpFile := cleanPath + fmt.Sprintf(".tmp.%d", time.Now().UnixNano())`
  3. Flush and commit atomically: `os.Rename(tmpFile, cleanPath)`
  4. Cleanup on error: `defer os.Remove(tmpFile)` with `0644` permissions.

---

## 6. Test Fixture Isolation: `setupTestServer()`
- **Problem**: `setupTestServer()` in `pkg/api` returns a clean, unseeded in-memory catalog.
- **Required Idiom**: Queries against demo tables (`accounts`, `customers`) must explicitly execute `CREATE TABLE` and `INSERT` statements inside the test body before querying.

---

## 7. VDBE Transaction Coupling (ACID Atomicity)
- **Problem**: Executing multi-step stored procedures (e.g., TPC-C New-Order) across separate autocommit calls breaks ACID atomicity.
- **Forbidden Anti-Pattern**: Never execute procedure statements in uncoordinated `exec.Run()` loops.
- **Required Idiom**: Bind multi-step execution to a single ambient transaction handle:
  ```go
  exec.RunInTx(prog, params, tx, nil)
  ```
  Any failure or conflict (`ErrWriteConflict`, `WT_ROLLBACK`) must trigger `tx.Rollback()`.

---

## 8. TDS Transaction Descriptors & `PacketTransactionManager` (0x0E)
- **Problem**: Official Microsoft drivers (`go-mssqldb`, .NET SqlClient) initiate transactions via `PacketTransactionManager` (0x0E, `tmBeginXact = 5`) rather than plain text `BEGIN TRANSACTION`.
- **Required Idiom**:
  - Issue an 8-byte unique transaction descriptor and return `ENVCHANGE` token (Type 8: `EnvChangeBeginTransaction`).
  - Set `DoneInXact` (0x0004) status bit on all subsequent `DONE` tokens while transaction is active.
  - Return `ENVCHANGE` tokens on COMMIT (Type 9) and ROLLBACK (Type 10).

---

## 9. PGWire Extended Query Protocol & lib/pq Binary Decoding
- **Problem**: Official Go PostgreSQL driver (`github.com/lib/pq`) uses the Extended Query Protocol (`Parse` ➔ `Describe` ➔ `Bind` ➔ `Execute` ➔ `Sync`).
- **Required Idiom**:
  1. **Parameter Inference**: When `numParamTypes == 0`, scan query for `\$(\d+)` and allocate matching OID 0 (Unspecified) descriptors.
  2. **Binary Result Encoding**: When `Bind` specifies binary format (`format == 1`) for integer/bytea/uuid OIDs, encode values as big-endian binary (`binary.BigEndian.PutUint32`), not ASCII text.
  3. **CommandComplete Tags**: Return canonical tags without arguments (`BEGIN`, `COMMIT`, `ROLLBACK`, `SET`).

---

## 10. Map Iteration Non-Determinism & Catalog Object ID Drift
- **Problem**: Go `map` iteration order is randomized. If `ListTables()` returns unsorted slices, successive queries (`sys.objects`, `sys.tables`, `sys.columns`) assign different Object IDs (`1000 + idx`) to the same table, scrambling GUI column trees.
- **Required Idiom**:
  - `ListTables()` in `memengine` and `wiredtiger` must sort table schemas alphabetically by name before returning.
  - `pkg/api/sqlexec_pgcatalog.go` uses `getSortedTables()`, `getSortedViews()`, `getSortedProcedures()`.
  - `OBJECT_ID(name)` and `OBJECT_NAME(id)` share the same sorted cache.

---

## 11. WiredTiger Cursor EOF Wrap-Around & ChunkedIterator Infinite Loops
- **Problem**: WiredTiger cursors (`WT_CURSOR`) wrap around to the first record if `next()` is called again after reaching EOF (`WT_NOTFOUND`). Vectorized chunk iterators (`ChunkedIterator`) loop indefinitely on tables with fewer than 1024 rows.
- **Required Idiom**:
  - `WTCursor` latches `eof = true` upon the first non-zero return from `C.go_wt_cursor_next`, immediately rejecting subsequent `Next()`/`Record()` calls.
  - `SeqScanIterator` and `IndexScanIterator` maintain their own `eof bool` guards.

---

## 12. Graceful Shutdown: Idle TCP Sockets & sync.WaitGroup Deadlocks
- **Problem**: Idle keep-alive client connections block inside `net.Conn.Read()`. Closing only the listener (`net.Listener.Close()`) causes `s.wg.Wait()` to hang indefinitely.
- **Required Idiom**:
  - Track active connections in `activeConns map[net.Conn]struct{}`.
  - During `Close()`, invoke `conn.Close()` on all active sockets immediately after closing the listener.
  - Enforce a 1.5-second bounded shutdown deadline in signal handlers (Golden Rule 4).

---

## 13. Utility-to-Hotpath Creep & Package-Level Constant Regex
- **Problem**: Utilities with heavy allocations (dynamic `regexp.MustCompile`, `strings.Builder` char-by-char copies) promoted to query execution paths degrade OLTP throughput from 20k to 5k TPS.
- **Required Idiom**:
  - Zero dynamic regex compilation: compile once in package-level `var (...)` blocks.
  - Zero-allocation query splitting: use byte index slicing (`query[start:i]`) instead of string building.
  - Track allocations via `b.ReportAllocs()` benchmarks.

---

## 14. TDS RPC `sp_executesql` Ordinal Positional Mapping Invariant
- **Problem**: Node.js `tedious` (Beekeeper Studio) sends parameter `@statement` instead of official `@stmt`. Strict parameter name matching causes query execution to receive empty SQL, crashing the client.
- **Required Idiom**:
  - Ordinal positional fallback: ordinal slot 0 (`i == 0`) is unconditionally treated as the target SQL query, regardless of whether parameter names are omitted, `@stmt`, or driver-specific variants (`@statement`, `@sql`).
  - Eliminate tool-specific hardcoded branching (Golden Rule 15 KISS).
