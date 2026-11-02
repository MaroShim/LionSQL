# Vikunja Integration Guide (LionSQL PGWire)

This document provides instructions for integrating **Vikunja**, an open-source task management and Kanban platform (alternative to Trello, Asana, and Todoist), with LionSQL using the PostgreSQL Wire Protocol (PGWire v3, default port `:5433`, WiredTiger storage engine).

---

## 1. Overview and Compatibility Architecture

Vikunja communicates with PostgreSQL databases via the Go **XORM** library (`xorm.io/xorm`) and `lib/pq` / `pgx` drivers.

- **Supported Protocol**: PostgreSQL Wire Protocol v3 (`lib/pq`, `pgx`)
- **LionSQL Recommended Engine**: WiredTiger (MVCC row versioning and hierarchical query transaction durability)
- **Applied Migrations**: 80+ migrations applied seamlessly in under 0.6s
- **Primary Data Models**:
  - `users`, `projects`, `tasks`, `project_views` (List, Gantt, Table, Kanban), `project_task_counters`, `teams`, `users_projects`, `project_ancestors`, etc.
- **Key Query Patterns**:
  - Multi-derived table unions (`UNION ALL`) within Common Table Expressions (`WITH ... AS`) for user permission resolution
  - Recursive CTEs (`WITH RECURSIVE project_hierarchy AS ...`) for infinite-depth project tree traversal
  - Composite indexed column `OR` predicates (`WHERE username = $1 OR email = $2`) for login authentication
  - Atomic counter updates (`UPDATE project_task_counters SET ...`) for task sequencing

---

## 2. Prerequisites

- **Build LionSQL Binary** (with WiredTiger engine):
  ```bash
  go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
- **Official Vikunja Docker Image**:
  ```bash
  docker pull vikunja/vikunja:latest
  ```

---

## 3. Running and Integration Steps

### Step 1: Start LionSQL Server
```bash
./bin/lionsql -dir data_vikunja_pg -engine wiredtiger -pg 0.0.0.0:5433 -http 127.0.0.1:8096 -admin-pass="VikunjaPass2026!"
```

### Step 2: Start Vikunja Server Container
```bash
docker run -d \
  --name vikunja_live \
  --network host \
  -e VIKUNJA_DATABASE_TYPE=postgres \
  -e VIKUNJA_DATABASE_HOST=127.0.0.1 \
  -e VIKUNJA_DATABASE_PORT=5433 \
  -e VIKUNJA_DATABASE_USER=postgres \
  -e VIKUNJA_DATABASE_PASSWORD="VikunjaPass2026!" \
  -e VIKUNJA_DATABASE_DATABASE=vikunja \
  -e VIKUNJA_DATABASE_SSLMODE=disable \
  -e VIKUNJA_SERVICE_JWTSECRET="lion-sql-vikunja-secret-jwt-key-32bytes" \
  -e VIKUNJA_SERVICE_FRONTENDURL="http://localhost:3456" \
  vikunja/vikunja:latest
```

---

## 4. E2E Verification & API Operations

### 4.1 Service Healthcheck and Version
```bash
# Server & API version check (HTTP 200, v2.7.1)
curl -s http://127.0.0.1:3456/api/v1/info

# Web frontend UI access (HTTP 200 OK)
curl -I http://127.0.0.1:3456/
```

### 4.2 User Registration
```bash
curl -X POST http://127.0.0.1:3456/api/v1/register \
  -H "Content-Type: application/json" \
  -d '{"username":"maro","email":"maro@example.com","password":"Password123!"}'
```
- **Response**: `{"id":1,"username":"maro","email":"maro@example.com", ...}` (HTTP 200 OK)

### 4.3 JWT Authentication (Login)
```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:3456/api/v1/login \
  -H "Content-Type: application/json" \
  -d '{"username":"maro","password":"Password123!"}' | jq -r '.token')
```
- **Result**: Authenticated via `WHERE (username = $1 OR email = $2)` query and issued JWT token.

### 4.4 Project and View Creation
```bash
curl -X PUT http://127.0.0.1:3456/api/v1/projects \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"LionSQL Roadmap","description":"WiredTiger & PGWire Integration"}'
```
- **Response**: HTTP 201 Created (generates default List, Gantt, Table, and Kanban views in a transactional hierarchy).

### 4.5 Kanban Task Creation and State Mutation
```bash
# Create task
curl -X PUT http://127.0.0.1:3456/api/v1/projects/2/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Verify Woodpecker & Vikunja Integration"}'

# Update task status (done: true)
curl -X POST http://127.0.0.1:3456/api/v1/tasks/1 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"done":true}'
```
- **Response**: HTTP 200 OK (atomic counter increment in `project_task_counters` and task completion persisted).

---

## 5. Key Engine Enhancements

1. **Recursive CTE (`WITH RECURSIVE`) Support**:
   - `ParseCTEs` strips PostgreSQL's `RECURSIVE` qualifier and evaluates base and recursive terms over iterative `UNION ALL` cycles up to depth 100.
2. **Derived Table Scope Isolation in CTEs**:
   - Transmits CTE prefixes into nested subqueries, materializing intermediate results into isolated temporary tables (`#__cte_<name>_<seq>`).
3. **Composite `OR` Predicate Guard**:
   - Prevents disjunctive `OR` clauses from being incorrectly parsed into single-column index seek bounds, falling back cleanly to table scan filtering with bound parameter support.
4. **Normalized VDBE Command Tags**:
   - Returns standard `"UPDATE <n>"` tags rather than `"DML <n>"` across PGWire prepared statement execution, preserving correct `RowsAffected` detection in XORM and `pgx`.
