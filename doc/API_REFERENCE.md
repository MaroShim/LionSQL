# SQLion HTTP REST API & Realtime SSE Specification

SQLion provides an embedded, zero-dependency PocketBase-style HTTP REST API and Server-Sent Events (SSE) streaming engine on port `8090` (configurable via `-http`).

---

## 1. Authentication & Security Model

When authentication is enabled (via `-auth` flag), endpoints enforce Bearer Token verification:
```http
Authorization: Bearer <JWT_TOKEN_OR_API_KEY>
```

### 1.1. Superuser Authentication

#### `POST /api/auth/login`
Authenticates a superuser and issues a signed JWT token.

- **Request Body**:
  ```json
  {
    "email": "admin@sqlion.local",
    "password": "your-password"
  }
  ```
- **Response `200 OK`**:
  ```json
  {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "user": {
      "id": "usr-01",
      "email": "admin@sqlion.local",
      "role": "superuser"
    }
  }
  ```

#### `GET /api/auth/me`
Returns current authenticated user identity and claims.

#### `POST /api/auth/refresh`
Refreshes an active token before expiration.

#### `POST /api/auth/logout`
Revokes the current JWT token, invalidating it immediately in the plan and session cache.

---

### 1.2. API Key Management (Granular Scopes)

Administrators can generate scoped API keys for frontend web clients or microservices.

#### `GET /api/keys`
Lists all active API keys (requires superuser authorization).

#### `POST /api/keys`
Creates a new scoped API key token.

- **Request Body**:
  ```json
  {
    "name": "Frontend Public Key",
    "role": "readonly",
    "allowed_tables": ["products", "categories"]
  }
  ```
- **Response `201 Created`**:
  ```json
  {
    "id": "key-019283",
    "name": "Frontend Public Key",
    "token": "sqlion_key_abc123xyz...",
    "role": "readonly",
    "allowed_tables": ["products", "categories"],
    "created_at": "2026-09-22T10:00:00Z"
  }
  ```

#### `DELETE /api/keys/{id}`
Revokes an API key immediately.

---

## 2. Table & Record Operations (CRUD)

### 2.1. Table Management

#### `GET /api/tables`
Returns metadata and DDL definitions for all database tables.

- **Response `200 OK`**:
  ```json
  [
    {
      "name": "accounts",
      "columns": [
        {"name": "id", "type": "INT", "primary_key": true, "nullable": false},
        {"name": "holder", "type": "VARCHAR", "primary_key": false, "nullable": false},
        {"name": "balance", "type": "INT", "primary_key": false, "nullable": false}
      ],
      "ddl": "CREATE TABLE accounts (\n    id INT PRIMARY KEY,\n    holder VARCHAR NOT NULL,\n    balance INT NOT NULL\n);"
    }
  ]
  ```

#### `POST /api/tables`
Creates a new table via JSON schema.

- **Request Body**:
  ```json
  {
    "name": "orders",
    "columns": [
      {"name": "order_id", "type": "INT", "primary_key": true},
      {"name": "user_id", "type": "INT", "nullable": false},
      {"name": "amount", "type": "FLOAT", "nullable": false}
    ]
  }
  ```

#### `DELETE /api/tables/{name}`
Drops the specified table and its secondary indexes.

---

### 2.2. Records CRUD

#### `GET /api/tables/{name}/records`
Fetches records with built-in pagination, column sorting, and multi-field search.

- **Query Parameters**:
  - `page`: Page number (1-indexed, default `1`).
  - `perPage`: Items per page (default `30`, max `500`).
  - `sort`: Column name to sort by (e.g. `balance` or `-balance` for descending).
  - `search`: Substring search across all text/numeric columns.
- **Response `200 OK`**:
  ```json
  {
    "table": "accounts",
    "page": 1,
    "perPage": 30,
    "totalItems": 100,
    "totalPages": 4,
    "records": [
      {
        "id": "1",
        "holder": "Alice",
        "balance": "1000"
      },
      {
        "id": "2",
        "holder": "Bob",
        "balance": "500"
      }
    ]
  }
  ```

#### `POST /api/tables/{name}/records`
Inserts a single record. Broadcasts `ActionCreate` event via Realtime SSE.

- **Request Body**:
  ```json
  {
    "id": 101,
    "holder": "David",
    "balance": 1500
  }
  ```

#### `PATCH /api/tables/{name}/records/{pk}`
Updates an existing record by primary key value. Broadcasts `ActionUpdate` event.

- **Request Body**:
  ```json
  {
    "balance": 1750
  }
  ```

#### `DELETE /api/tables/{name}/records/{pk}`
Deletes an existing record by primary key value. Broadcasts `ActionDelete` event.

---

## 3. Direct SQL Execution & Procedure RPC

### 3.1. `POST /api/sql`
Executes raw SQL or T-SQL statements directly.

- **Request Body**:
  ```json
  {
    "sql": "SELECT holder, balance FROM accounts WHERE balance > 600 ORDER BY balance DESC;"
  }
  ```
- **Response `200 OK`**:
  ```json
  {
    "command": "SELECT",
    "columns": ["holder", "balance"],
    "rows": [
      {"holder": "David", "balance": 1750},
      {"holder": "Alice", "balance": 1000}
    ],
    "rows_affected": 2,
    "duration_ms": 0.42
  }
  ```

---

### 3.2. Stored Procedure Remote Procedure Call (RPC)

#### `POST /api/procs/{name}/exec`
Invokes a registered stored procedure with typed JSON argument mapping.

- **Request Body**:
  ```json
  {
    "params": {
      "@from": 1,
      "@to": 2,
      "@amount": 200
    }
  }
  ```
- **Response `200 OK`**:
  ```json
  {
    "status": "success",
    "procedure": "TransferFunds",
    "return_code": 0,
    "output_messages": ["Funds successfully transferred!"],
    "duration_ms": 1.15
  }
  ```

---

## 4. Realtime Synchronization via Server-Sent Events (SSE)

### `GET /api/realtime`
Opens an active HTTP SSE stream delivering live table mutation events to connected web frontends.

- **Headers**:
  - `Accept: text/event-stream`
- **Stream Event Format**:
  ```http
  data: {"action":"CREATE","table":"accounts","record":{"id":"101","holder":"David","balance":"1500"},"timestamp":1727000000000}

  data: {"action":"UPDATE","table":"accounts","record":{"id":"101","holder":"David","balance":"1750"},"timestamp":1727000005000}

  data: {"action":"DELETE","table":"accounts","record":{"id":"101"},"timestamp":1727000010000}
  ```

### JavaScript Frontend Integration Example:

```javascript
const eventSource = new EventSource('http://localhost:8090/api/realtime');

eventSource.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log(`Live mutation detected: ${data.action} on table ${data.table}`, data.record);
  
  if (data.table === 'accounts') {
    refreshAccountsGrid();
  }
};

eventSource.onerror = (err) => {
  console.error('SSE connection error:', err);
};
```

---

## 5. Live Snapshot Hot Backup & Restore

### 5.1. `GET /api/backup` & `POST /api/backup`
Captures a consistent, live point-in-time snapshot of the database without acquiring write locks or interrupting active OLTP traffic.

- **Direct Stream Download**:
  ```bash
  curl -OJ http://127.0.0.1:8090/api/backup
  ```
  Returns `Content-Type: application/gzip` with header `Content-Disposition: attachment; filename="sqlion-backup-YYYYMMDD-HHMMSS.tar.gz"`.

- **Server-Side Target Path**:
  ```bash
  curl -X POST http://127.0.0.1:8090/api/backup \
       -H "Content-Type: application/json" \
       -d '{"target": "backups/prod-snapshot.tar.gz"}'
  ```
  - **Response `200 OK`**:
    ```json
    {
      "status": "success",
      "target": "backups/prod-snapshot.tar.gz",
      "manifest": {
        "version": "1.0.0",
        "created_at": "2026-09-22T04:30:00Z",
        "tables_count": 3,
        "total_records": 15000,
        "procedures_count": 2
      }
    }
    ```

---

### 5.2. `POST /api/restore`
Restores database schemas, indexes, data rows, and catalog stored procedures from a `.tar.gz` backup archive.

- **Multipart Archive File Upload**:
  ```bash
  curl -X POST http://127.0.0.1:8090/api/restore \
       -F "backup=@/path/to/sqlion-backup.tar.gz"
  ```
- **Server File Path Reference**:
  ```bash
  curl -X POST http://127.0.0.1:8090/api/restore \
       -H "Content-Type: application/json" \
       -d '{"source": "backups/prod-snapshot.tar.gz", "drop_existing": true}'
  ```
- **Response `200 OK`**:
  ```json
  {
    "status": "success",
    "result": {
      "tables_restored": 3,
      "records_restored": 15000,
      "procedures_restored": 2,
      "duration_ms": 28.5
    }
  }
  ```

---

## 6. System Health & Connection Pool Metrics

### 6.1. `GET /api/health`
Verifies server health and version.
```json
{
  "status": "ok",
  "version": "1.0.0"
}
```

### 6.2. `GET /api/pool/stats`
Monitors internal goroutine connection pooler and worker status.
```json
{
  "max_workers": 32,
  "active_workers": 4,
  "max_idle_sessions": 64,
  "idle_sessions": 12,
  "queued_requests": 0
}
```
