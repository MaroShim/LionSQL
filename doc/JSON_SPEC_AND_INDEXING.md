# JSON Functions & Expression Indexing Specification

## 1. Overview & Architectural Motivation
Modern web applications, microservices, and Backend-as-a-Service (BaaS) frameworks (e.g. Supabase, PocketBase, Prisma, Next.js) increasingly adopt semi-structured document patterns where relational tables store JSON payloads alongside primary relational keys.

To provide first-class support for semi-structured data across both PostgreSQL Wire Protocol (`:5433`) and Microsoft SQL Server TDS Protocol (`:1433`), SQLion unifies JSON querying and indexing into a high-performance, dual-protocol standard backed by its native B+Tree storage backends (WiredTiger MVCC & In-Memory OCC SkipList).

---

## 2. Syntax Comparison: PostgreSQL vs MySQL vs SQL Server

| Capability / DBMS | **PostgreSQL** | **MySQL (8.0+)** | **Microsoft SQL Server (T-SQL)** | **SQLion (Unified Architecture)** |
| :--- | :--- | :--- | :--- | :--- |
| **Scalar Extraction** | `data->>'key'` | `data->>'$.key'` | `JSON_VALUE(data, '$.key')` | **Both supported**: `data->>'key'` and `JSON_VALUE(data, '$.key')` |
| **Object / Array Extraction** | `data->'key'` | `data->'$.key'` | `JSON_QUERY(data, '$.key')` | **Both supported**: `data->'key'` and `JSON_QUERY(data, '$.key')` |
| **Validation** | `data IS JSON` | `JSON_VALID(data)` | `ISJSON(data)` | `ISJSON(data)` & `JSON_VALID(data)` |
| **JSON Index Syntax** | **Expression B-Tree Index**:<br>`CREATE INDEX idx ON tbl ((data->>'key'));` | **Functional B-Tree Index**:<br>`CREATE INDEX idx ON tbl ((CAST(data->>'$.key' AS CHAR(50))));` | **Computed Column + B-Tree**:<br>`ALTER TABLE tbl ADD k AS JSON_VALUE(data, '$.key');`<br>`CREATE INDEX idx ON tbl(k);` | **Expression B-Tree Index**:<br>`CREATE INDEX idx ON tbl ((data->>'key'));`<br>`CREATE INDEX idx ON tbl ((JSON_VALUE(data, '$.key')));` |
| **Full Inverted Indexing** | `USING gin (data)` | Multi-Valued Index (`ARRAY`) | Full-Text / Columnstore bypass | Evaluated / Deferred (Spec-Out for Stage 1) |
| **DDL Complexity** | 1 Query (`CREATE INDEX`) | 1 Query (`CREATE INDEX`) | 2 Queries (`ALTER TABLE` + `CREATE INDEX`) | **1 Query (`CREATE INDEX`)** |

---

## 3. SQLion Architectural Decision: Expression B-Tree Indexing

### 3.1. Rationale for Expression B-Tree Indexing
1. **Zero-Overhead Storage Alignment (KISS & DRY)**:
   - SQLion's storage engines are optimized for sorted scalar keys: WiredTiger Lock-Free B+Trees (`pkg/storage/wiredtiger`) and Concurrent SkipLists (`pkg/storage/memengine`).
   - Generalized Inverted Indexes (GIN) require dedicated posting lists, inverted tree structures, and high write amplification on ingestion.
   - An **Expression B-Tree Index** extracts a scalar string/number from the JSON column on write (`INSERT` / `UPDATE`) and stores it directly into standard B+Tree index pages, achieving sub-microsecond point lookups and range scans.
2. **Frictionless Developer Experience (DX)**:
   - Eliminates two-step DDL migrations required by legacy SQL Server (`ALTER TABLE ADD ... AS JSON_VALUE(...) PERSISTED` followed by `CREATE INDEX`).
   - Allows application developers and ORMs (Prisma, Drizzle, GORM, Hibernate) to declare functional indexes in a single standard statement.
3. **Dual-Protocol AST Normalization**:
   - Both PostgreSQL operator syntax (`((data->>'attr'))`) and T-SQL function syntax (`((JSON_VALUE(data, '$.attr')))` ) compile to an identical internal AST node: `*proc.JsonExtractExpr`.
   - Queries arriving from PostgreSQL clients (`WHERE data->>'attr' = 'val'`) and TDS clients (`WHERE JSON_VALUE(data, '$.attr') = 'val'`) probe the exact same underlying B+Tree index.

---

## 4. Query & Index Pipeline Architecture

```
[ PGWire Client ]                               [ TDS / SSMS Client ]
  WHERE data->>'user' = 'alice'                   WHERE JSON_VALUE(data, '$.user') = 'alice'
        │                                               │
        └───────────────────────┬───────────────────────┘
                                ▼
                   [ Unified SQL / Pratt Parser ]
                   (Normalizes to *proc.JsonExtractExpr)
                                │
                                ▼
                 [ Cost-Based Optimizer (CBO) ]
                 - Detects matching Expression Index:
                   IndexDef{ Column: "((data->>'user'))", Type: BTree }
                 - Replaces TableScan with IndexScan
                                │
                                ▼
                 [ Volcano IndexScanIterator ]
                 - Probes WiredTiger B+Tree / SkipList cursor
                 - Returns matching tuple in O(log N) time
```

---

## 5. Specification: Built-in JSON Functions

### 5.1. `JSON_VALUE(json_doc, path)`
* **Description**: Extracts a scalar primitive (string, number, boolean, null) from a JSON document at the specified path.
* **Return Type**: `VARCHAR` (or typed integer/float if cast).
* **Path Syntax**:
  - `$.key`: Top-level field access.
  - `$.parent.child`: Nested object property access.
  - `$.items[0]`: Array index access.
* **Behavior**:
  - Returns `NULL` if path does not exist.
  - Returns `NULL` if target value is a JSON object or array.
* **Example**:
  ```sql
  SELECT JSON_VALUE(payload, '$.customer.email') AS email FROM orders;
  ```

### 5.2. `JSON_QUERY(json_doc, path)`
* **Description**: Extracts a JSON fragment (object `{...}` or array `[...]`) from a JSON document at the specified path.
* **Return Type**: `VARCHAR` (raw valid JSON string).
* **Behavior**:
  - Returns `NULL` if target value is a scalar primitive.
* **Example**:
  ```sql
  SELECT JSON_QUERY(payload, '$.customer.addresses') AS addresses FROM orders;
  ```

### 5.3. `ISJSON(json_doc)`
* **Description**: Tests whether a string contains valid JSON.
* **Return Type**: `INT` (`1` if valid JSON, `0` otherwise).
* **Example**:
  ```sql
  SELECT id FROM documents WHERE ISJSON(raw_text) = 1;
  ```

### 5.4. PostgreSQL Operators: `->` and `->>`
* **`json -> key_or_index`**:
  - Extracts JSON component as a raw JSON string (equivalent to `JSON_QUERY` or scalar string).
  - Example: `data->'customer'` returns `'{"id": 1, "name": "Alice"}'`.
* **`json ->> key_or_index`**:
  - Extracts JSON component as unquoted text (equivalent to `JSON_VALUE`).
  - Example: `data->>'name'` returns `'Alice'`.

---

## 6. Specification: Expression B-Tree Index DDL & Mutation

### 6.1. DDL Syntax
```sql
-- PostgreSQL double-parenthesis expression syntax
CREATE INDEX idx_users_email ON users ((profile->>'email'));

-- T-SQL function expression syntax
CREATE INDEX idx_users_email ON users ((JSON_VALUE(profile, '$.email')));

-- Composite expression index
CREATE INDEX idx_tenant_user ON orders (tenant_id, (data->>'status'));
```

### 6.2. Table Mutation Invariant (Index Maintenance)
When a table contains expression indexes:
1. **On `INSERT` / `UPDATE`**:
   - The storage layer evaluates the compiled expression against the row's values.
   - The evaluated scalar value is serialized as the secondary index key.
   - The primary key is appended as the index payload.
   - Updated into the index B+Tree atomically within the same transaction.
2. **On `DELETE`**:
   - The old row expression value is deleted from the index B+Tree.

### 6.3. CBO Query Matching Rule
The Cost-Based Optimizer matches a predicate `WHERE <expr> = <val>` against secondary indexes:
1. Strip outer parentheses and normalize column/path casing.
2. Match AST representation of `data->>'key'` with index definition `((data->>'key'))` or `((JSON_VALUE(data, '$.key')))`.
3. If matched, generate `NodeIndexScan` or `NodeIndexOnlyScan` instead of `NodeTableScan`.

---

## 7. Implementation Roadmap
1. **Stage 1 (Scalar Evaluation)**: Implement `JSON_VALUE`, `JSON_QUERY`, `ISJSON`, `->`, `->>` in `pkg/proc/compiler.go` and `pkg/execution` expression evaluator using `pkg/storage/json.go`.
2. **Stage 2 (Parser DDL Extension)**: Update `pkg/proc/parser.go` to accept expression indexes with double parentheses `CREATE INDEX ... ON tbl ((expr))`.
3. **Stage 3 (Storage Engine Indexing)**: Update `TableSchema.Indexes` to store `Expression string` and evaluate index keys during `Insert` / `Update` in `memengine` and `wiredtiger`.
4. **Stage 4 (CBO Integration)**: Add expression index matching in `pkg/planner/cbo.go`.
5. **Stage 5 (Verification)**: Add comprehensive unit tests in `pkg/engine` and driver integration tests in `pkg/wire`.
