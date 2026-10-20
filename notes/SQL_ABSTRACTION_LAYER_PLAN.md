# LionSQL Unified SQL Abstraction Layer Implementation Plan (`SQL-ABSTRACT-1`)

## 1. Executive Summary & Architectural Motivation

### 1.1. Context
During the feature freeze audit of LionSQL, 121 edge-case integrity and consistency defects were identified. An analysis of these defects revealed that **over 35% of the total defects stemmed directly from engine layers improperly relying on Go native data types and Go runtime semantics** rather than formal SQL relational abstractions:
- **Go `map[string]T` Strict Case-Sensitivity**: Go maps distinguish `"id"` from `"ID"`. When AST column references or client query casings did not match schema column definitions, lookups silently failed, returning `nil` or defaulting columns to `0`/`NULL` (`BUG-RECORD-1`, `BUG-ARITH-1`, `BUG-UPSERT-1`, `BUG-WHERE-1`).
- **Go `map` Non-Deterministic Iteration**: Go runtime intentionally randomizes map iteration order with a per-process seed. Queries extracting "the first column" via `for _, v := range row { return v; break }` randomly selected arbitrary columns, causing data corruption and flaky test behaviors (`BUG-PK-1`, `BUG-VM-INTO-1`, `BUG-SUBQ-1`, `BUG-BOUNDS-1`, `BUG-EXP-1`).
- **Go 2-Valued Logic vs SQL 3-Valued Logic (3VL)**: Treating SQL `NULL` as Go `nil` and using native Go boolean checks (`val == nil || other == nil`) violated ANSI SQL 3VL, causing `WHERE status <> 'completed'` to leak NULL rows and date functions to misbehave (`BUG-65`, `BUG-66`).

### 1.2. Strategic Decision
Rather than performing ad-hoc "whack-a-mole" patches (`for k, v := range m { if strings.EqualFold(k, col) { ... } }`) scattered across 40+ files, LionSQL adopts **Option 3: The Unified SQL Abstraction Layer (`SQL-ABSTRACT-1`)**.

This plan specifies the architecture, interfaces, transition milestones, and verification criteria to permanently eliminate raw Go map vulnerabilities from the database kernel.

---

## 2. Target Architecture

```
                       [ Client Query / TDS / PGWire / REST ]
                                         │
                                         ▼
                 [ Phase 1: Boundary Identifier Canonicalizer ]
                 (pkg/engine/boundary_normalizer.go)
                 - Matches unquoted identifiers against TableSchema.FindColumn
                 - Normalizes all DML/DQL keys to Canonical Schema Casing
                                         │
                                         ▼
                 [ Phase 2: First-Class storage.Record ]
                 (pkg/storage/record.go, engine.go)
                 - rec.Get(col): O(1) Case-Insensitive & Prefix-Tolerant Lookup
                 - rec.Set(col, val): Canonical Key Preservation
                 - rec.FirstValue(cols...): Deterministic Column Extraction
                 - rec.SortedKeys(): Deterministic Key Ordering
                 - rec.ToRow(schema): Direct Conversion to Slice-Backed storage.Row
                                         │
                                         ▼
                 [ Phase 3: Coordinator SQLRow & Tuple Unification ]
                 (pkg/engine/sql_row.go, pkg/execution/iterator.go)
                 - Wraps raw map[string]any into structured SQLRow
                 - Integrates pkg/execution.ColumnIndexMap for O(1) slot resolution
                 - Unifies Volcano Tuple, VDBE Register Slots, and Coordinator DML
                                         │
                                         ▼
                 [ Phase 4: Standardized SQL 3VL & NULL Coercion ]
                 (pkg/storage/engine.go, pkg/engine/sqlexec_eval.go)
                 - Value.Compare / Value.Equal handles 3VL UNKNOWN semantics
                 - Zero raw Go 'nil == nil' equality leakages
```

---

## 3. Detailed Component Specifications

### 3.1. First-Class `storage.Record` Objectification (`pkg/storage/record.go`)

`storage.Record` is currently defined as `type Record map[string]Value`. Rather than breaking binary storage engine interfaces, we elevate `Record` into a first-class citizen by equipping it with canonical SQL manipulation methods:

```go
package storage

// Record represents a column-to-value mapping for a database tuple.
type Record map[string]Value

// Get retrieves a value by column name, supporting exact match first,
// followed by case-insensitive matching and table-prefix stripping (e.g. "users.id" -> "id").
func (r Record) Get(colName string) (Value, bool)

// Set stores a value for the column. If an existing key matches case-insensitively,
// the existing canonical key is updated in place, preventing duplicate casing entries.
func (r Record) Set(colName string, val Value)

// Delete removes a column from the record case-insensitively.
func (r Record) Delete(colName string) bool

// FirstValue returns the value of the first specified column in 'cols', or if 'cols'
// is empty, returns the value of the first key in lexicographically sorted order.
// This guarantees 100% deterministic execution and eliminates random Go map iteration.
func (r Record) FirstValue(cols ...string) (Value, bool)

// SortedKeys returns all column keys sorted in lexicographical order.
func (r Record) SortedKeys() []string

// Clone creates a shallow copy of the Record.
func (r Record) Clone() Record

// ToRow converts the Record into a slice-backed Row aligned with the provided schema.
func (r Record) ToRow(schema *TableSchema) Row
```

### 3.2. Coordinator `SQLRow` Abstraction (`pkg/engine/sql_row.go`)

In the coordinator DML and evaluation layer (`sqlexec_dml.go`, `sqlexec_eval.go`, `sqlexec_explain.go`), raw `map[string]any` is replaced with `SQLRow`:

```go
package engine

import "lionsql/pkg/storage"

// SQLRow encapsulates a query or execution row, providing case-insensitive,
// table-alias-aware column access without raw Go map iteration hazards.
type SQLRow struct {
    values  map[string]any
    columns []string
}

// NewSQLRow creates an SQLRow from raw values and an optional column ordering.
func NewSQLRow(values map[string]any, cols ...[]string) *SQLRow

// Get returns the value of a column by checking:
// 1. Exact match
// 2. Table-prefix stripped match ("t.col" -> "col")
// 3. Case-insensitive match (strings.EqualFold)
func (r *SQLRow) Get(colName string) (any, bool)

// GetFirst returns the value of the first matching column in cols,
// or the first column in the defined column sequence.
func (r *SQLRow) GetFirst(cols ...string) (any, bool)

// ToStorageRecord converts the SQLRow into a storage.Record mapped to schema types.
func (r *SQLRow) ToStorageRecord(schema *storage.TableSchema) storage.Record

// ToMap returns the underlying map[string]any for final serialization in QueryResult.
func (r *SQLRow) ToMap() map[string]any
```

### 3.3. Boundary Identifier Normalization (`pkg/engine/boundary_normalizer.go`)

At the entry point of the Coordinator (`execInsert`, `execUpdate`, `execDelete`, `execMerge`, `execSelect`):
- All unquoted AST column identifiers are checked against `schema.FindColumn(ident)`.
- If a match is found, the column name is replaced with `colDef.Name` (the canonical casing from the catalog).
- This guarantees that within the execution boundary, all column names match the schema casing exactly, allowing $O(1)$ fast-path lookups without repeatedly looping with `strings.EqualFold`.

### 3.4. SQL Three-Valued Logic (3VL) Standard (`pkg/storage/engine.go`)

- `Value.Compare`:
  - If either operand is `IsNull`, the comparison result is `TypeUnknown` / SQL UNKNOWN.
  - Explicit helper: `Value.IsUnknown() bool`.
- Predicate evaluation (`evaluateWhereConditionMap`):
  - In SQL 3VL, a row passes a `WHERE` filter **if and only if** the predicate evaluates to `TRUE`. Predicates evaluating to `FALSE` or `UNKNOWN` (NULL) are rejected.
  - Fixes all `WHERE col <> 'val'` and `WHERE NOT IN (...)` leakages.

---

## 4. Implementation Phases & Milestones

| Phase | Component | Key Tasks | Verification Gate |
| :---: | :--- | :--- | :--- |
| **Phase 1** | **`pkg/storage`** | Implement `storage/record.go` (`Get`, `Set`, `FirstValue`, `SortedKeys`, `ToRow`). | Unit tests in `pkg/storage/record_test.go` covering mixed case, sorted order, and deterministic lookups. |
| **Phase 2** | **`pkg/engine`** | Implement `pkg/engine/sql_row.go` and `boundary_normalizer.go`. | Unit tests in `pkg/engine/sql_row_test.go` verifying case-insensitivity, alias resolution, and canonicalization. |
| **Phase 3** | **Coordinator DML Refactoring** | Migrate `sqlexec_dml.go` (`execUpdate`, `execDelete`, `execInsert`, `execMerge`, `execUpsert`) to use `Record.Get/Set` and boundary normalization. | Integration tests across DML suite; verify zero random map iteration. |
| **Phase 4** | **Coordinator Eval & Subqueries** | Migrate `sqlexec_eval.go` and `sqlexec_explain.go` to use `SQLRow` and deterministic `GetFirst`. | Regression suite verification; verify `evalUpdateExpr` and subquery flattening parity. |
| **Phase 5** | **Full Battery Verification** | Run full 3-Stage Pipeline across all packages with `-race`. | `go test -tags wiredtiger -race ./...` 100% PASS with 0 data races. |

---

## 5. Impact Analysis on Active Bug Backlog

By introducing this unified layer, the following pending bugs in `TODO.ko.md` are directly resolved or structurally simplified:

| Bug ID | Domain | Root Cause Solved by SQL Abstraction Layer |
| :--- | :--- | :--- |
| **`BUG-DDL-PK-1`** | DDL/PK | Eliminates arbitrary `Columns[0]` guessing via formal PK resolution. |
| **`BUG-SELECT-INTO-PK-1`** | Select Into | Eliminates arbitrary PK promotion on derived/temporary tables. |
| **`BUG-JOIN-IDX-1`** | Join/Execution | Replaces random index selection with `ColumnIndexMap` index resolution. |
| **`BUG-JOIN-PARSE-1`** | Execution/Join | Eliminates fragile `strings.Split(cond, "=")` with AST-based tuple matching. |
| **`BUG-TABLE-CASE-1`** | Storage/Catalog | Normalizes table lookup boundaries via case-insensitive engine wrappers. |
| **`BUG-ORDER-BY-SPLIT-1`** | Engine/Sort | Replaces raw `strings.Fields` split with canonical column index ordering. |
| **`BUG-65` ~ `BUG-75`** | Eval/Comparison | Eliminates 3VL NULL comparison leakage and float string formatting drift. |

---

## 6. Engineering Golden Rules Compliance

- **Rule 11 (Defensive Indexing & Overflow Guard)**: All slice accesses in `SQLRow` and `Record.ToRow` are boundary-checked.
- **Rule 15 (Radical KISS, DRY & Deletion-First)**: Eliminates dozens of duplicated `for k, v := range m` loops across 40 files, replacing them with standardized `rec.Get()` and `row.Get()`.
- **Rule 16 (3-Stage Verification Pipeline)**: Each phase strictly passes Build -> Test with `-race` -> Commit.
- **Rule 18 (Documentation & Style Integrity)**: Zero decorative emojis across all code, tests, and commit messages.
- **Rule 24 (Zero-Allocation Hot Paths)**: `Record.Get` checks exact match first ($O(1)$ without allocating) before falling back to case-insensitive scan.
