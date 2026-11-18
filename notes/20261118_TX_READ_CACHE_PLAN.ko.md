# Transaction-Local Zero-Copy Read Cache & Decode Bypass Architecture Plan

> **Author**: Antigravity  
> **Date**: 2026-11-18  
> **Status**: In Progress  
> **Milestone**: v0.94.0 Performance Restoration  
> **Rules Followed**: `[RULE:PlanPersistence]`, `[RULE:ZeroAllocation]`, `[RULE:RadicalKISS]`, `[RULE:PipelineVerification]`

---

## 1. Problem Statement & Motivation
Following the transition to `MemoryCompact` as the single unified in-memory engine (commit `0ed35ad9`), all table records are stored as flat byte buffers (`flatBuf []byte`).
In multi-step OLTP transactions (such as TPC-C New-Order, Payment, and banking transfers), `tx.Get(tableName, pk)` and subsequent `WHERE` clause evaluations repeatedly decode the exact same flat byte buffer into heap-allocated `storage.Row` / `storage.Record` objects multiple times within a single transaction lifecycle.
This redundant decoding induces CPU cycles and memory allocations, degrading VDBE runtime TPC-C throughput from ~759k tpmC to ~532k tpmC.

## 2. Architectural Design: Transaction-Local Read Cache (`tx.readCache`)

### 2.1. Transaction Read Cache Lifecycle
- Add a transaction-private cache `readCache map[string]cachedRow` (keyed by `<tableName>\x00<pkKey>`).
- On `tx.Get(tableName, pk)`:
  1. Check if `tx.local[tableName][pkKey]` exists (uncommitted mutations take highest precedence).
  2. Check `tx.readCache[cacheKey]`:
     - If hit and version matches, return cached record/row directly (0ns decode, zero allocation).
  3. If miss:
     - Decode `flatBuf` once.
     - Store into `tx.readCache[cacheKey]`.
- On `tx.Update` / `tx.Delete`:
  - Invalidate or update `tx.readCache[cacheKey]`.
- On `tx.resetForPoolLocked()` / `releaseToPoolLocked()`:
  - If `len(tx.readCache) > 0`, clear the map; if cap > 128, shrink/nil it to eliminate long-term memory leaks (`[RULE:BoundedBuffers]`).

### 2.2. VDBE UPDATE / DELETE Fast-Path Bypass
- In `execUpdateWhere` and `execDeleteWhere`:
  - Avoid redundant `recordToMap` or re-fetching when `oldRec` is already available from the initial point-lookup.

---

## 3. Verification Pipeline (3-Stage)
1. **Stage 1 (Build)**: `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Stage 2 (Test & Benchmark)**:
   - `go test -v -race ./pkg/storage/memengine/...`
   - `./bin/tpcc-runtime -engine=memory -warehouses=16 -workers=32 -duration=15s`
3. **Stage 3 (Commit)**: Adhere to Conventional Commits without emojis.
