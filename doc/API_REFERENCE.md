# LionSQL HTTP REST API & Realtime SSE Specification

LionSQL provides an embedded, zero-dependency PocketBase-style HTTP REST API and Server-Sent Events (SSE) streaming engine on port `8090` (configurable via `-http`).

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
    "email": "admin@lionsql.local",
    "password": "your-password"
  }
  ```
- **Response `200 OK`**:
  ```json
  {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "user": {
      "id": "usr-01",
      "email": "admin@lionsql.local",
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
    "token": "lionsql_key_abc123xyz...",
    "role": "readonly",
    "allowed_tables": ["products", "categories"],
    "created_at": "2024-09-22T10:00:00Z"
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

### 2.2. Records CRUD (Auto-Generated RESTful Endpoints)

LionSQL automatically exposes standard RESTful CRUD endpoints for every table. For detailed documentation on filtering operators, nested expressions, and client SDK integration, see [Auto-Generated RESTful API Guide](AUTO_REST_API.md).

#### `GET /api/records/:table` (or `/api/tables/:name/records`)
Fetches records with built-in query filtering, multi-column sorting, pagination, and full-text search.

- **Query Parameters**:
  - `filter`: Rich boolean filter expression (e.g. `balance>500 && status='active'`, `status=active,balance<=800`, `name~'smith'`).
  - `page`: Page number (1-indexed, default `1`).
  - `limit` (or `perPage`): Items per page (default `30`, max `500`).
  - `sort`: Column(s) to sort by (e.g. `-id`, `balance`, `-created_at,balance`).
  - `search`: Case-insensitive substring search across all columns.
  - Column parameter shorthands (e.g. `?status=active&balance>500`).
- **Response `200 OK`**:
  ```json
  {
    "table": "customers",
    "page": 1,
    "limit": 20,
    "perPage": 20,
    "totalItems": 3,
    "totalPages": 1,
    "records": [
      { "id": 5, "name": "Evan", "balance": 600, "status": "active" },
      { "id": 3, "name": "Charlie", "balance": 800, "status": "active" },
      { "id": 1, "name": "Alice", "balance": 1000.5, "status": "active" }
    ],
    "items": [ ... ]
  }
  ```

#### `GET /api/records/:table/:id` (or `/api/tables/:name/records/:pk`)
Retrieves a single record by primary key value.
- **Response `200 OK`**:
  ```json
  {
    "id": 1,
    "name": "Alice",
    "balance": 1000.5,
    "status": "active"
  }
  ```

#### `POST /api/records/:table` (or `/api/tables/:name/records`)
Inserts a single record. Auto-generates snowflake/timestamp ID if PK is omitted. Broadcasts `ActionCreate` event via Realtime SSE.

- **Request Body**:
  ```json
  {
    "name": "Alice",
    "balance": 1000.5,
    "status": "active"
  }
  ```
- **Response `201 Created`**: The created record object.

#### `PATCH /api/records/:table/:id` (or `/api/tables/:name/records/:pk`)
Partially updates an existing record by primary key value. Broadcasts `ActionUpdate` event.

- **Request Body**:
  ```json
  {
    "balance": 1250.0
  }
  ```
- **Response `200 OK`**: The updated record object.

#### `PUT /api/records/:table/:id` (or `/api/tables/:name/records/:pk`)
Updates an existing record by primary key value. Broadcasts `ActionUpdate` event.

#### `DELETE /api/records/:table/:id` (or `/api/tables/:name/records/:pk`)
Deletes an existing record by primary key value. Broadcasts `ActionDelete` event.

- **Response `200 OK`**:
  ```json
  {
    "success": true,
    "message": "record deleted",
    "id": "1"
  }
  ```

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
- **Query Parameters / Body Options**:
  - `format`: Response representation format (`envelope` (default), `array`, `raw`, `records`).
    - `POST /api/sql`: Standard envelope including `columns`, `rows`, `rows_affected`, and duration.
    - `POST /api/sql?format=array` (or body `{"format": "records"}`): Returns a pure JSON record array (`[ {"holder": "David", ...}, ... ]`) directly without outer metadata wrapper for effortless frontend/microservice consumption.
- **Default Response `200 OK` (Envelope)**:
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
- **Array Response `200 OK` (`?format=array`)**:
  ```json
  [
    {"holder": "David", "balance": 1750},
    {"holder": "Alice", "balance": 1000}
  ]
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
  Returns `Content-Type: application/gzip` with header `Content-Disposition: attachment; filename="lionsql-backup-YYYYMMDD-HHMMSS.tar.gz"`.

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
        "version": "0.7.0",
        "created_at": "2024-09-22T04:30:00Z",
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
       -F "backup=@/path/to/lionsql-backup.tar.gz"
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
  "version": "0.7.0"
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

---

## 7. Webhook Dispatch Engine

LionSQL features an embedded, asynchronous Webhook Dispatch Engine. Whenever records are created, updated, deleted, or imported via REST or T-SQL, configured webhooks receive asynchronous HTTP `POST` notifications with exponential backoff retries and HMAC-SHA256 signature verification.

### 7.1. `GET /api/webhooks`
Lists all registered webhooks.

### 7.2. `POST /api/webhooks`
Registers a new webhook endpoint.
- **Request Body**:
  ```json
  {
    "name": "Audit Service",
    "table": "customers",
    "events": ["create", "update", "delete"],
    "url": "https://api.example.com/webhooks/customers",
    "secret": "my-hmac-secret-key",
    "enabled": true
  }
  ```
- **Response `201 Created`**: Returns registered `Webhook` object with generated UUID.

### 7.3. `DELETE /api/webhooks/:id`
Deletes a registered webhook by ID.

### 7.4. `POST /api/webhooks/:id/test`
Triggers an immediate test ping delivery to the webhook URL.
- **Response `200 OK`**:
  ```json
  {
    "success": true,
    "status_code": 200,
    "duration_ms": 14,
    "attempt_count": 1
  }
  ```

### 7.5. `GET /api/webhooks/deliveries?limit=50`
Fetches recent webhook delivery logs from the in-memory ring buffer (up to 200 entries).

### 7.6. `DELETE /api/webhooks/deliveries`
Clears the in-memory delivery history ring buffer.

### 7.7. HMAC Signature Verification
If a `secret` is configured, outgoing webhooks include a signature header:
```http
X-LionSQL-Signature: sha256=<hex_hmac_sha256(secret, payload)>
X-LionSQL-Event: create
X-LionSQL-Delivery: <delivery_uuid>
```

---

## 8. Declarative Row-Level Security (RLS)

LionSQL provides declarative, PocketBase-style Row-Level Security rules configured per table. Rules control `list`, `view`, `create`, `update`, and `delete` operations on REST endpoints (`/api/records/:table` and `/api/tables/:table/records`).

### 8.1. Rule Types & Execution
- **`list_rule`**: Conjoined as an automatic WHERE filter into all list queries. If empty, public access is allowed. If set to an impossible condition (e.g. `false`), list queries return empty results.
- **`view_rule`**: Evaluated against the fetched record. Returns `403 Forbidden` if evaluation fails.
- **`create_rule`**: Evaluated against the incoming record fields.
- **`update_rule`**: Evaluated against the existing record before applying updates.
- **`delete_rule`**: Evaluated against the existing record before deletion.

### 8.2. Dynamic Auth Variable Substitution
Expressions can reference client authentication claims:
- `@auth.id`: Authenticated user ID (from JWT claim `id` / `sub`, or API key user ID).
- `@auth.role`: Authenticated user role (`admin`, `user`, etc.).
- `@auth.email`: Authenticated user email.
- `@auth.tenant_id`: Authenticated tenant identifier (from JWT claim or `X-Tenant-ID` header).

### 8.3. `GET /api/tables/:name/rules`
Fetches the current RLS rule set for a table.
- **Response `200 OK`**:
  ```json
  {
    "table": "customers",
    "list_rule": "owner_id = @auth.id || @auth.role = 'admin'",
    "view_rule": "owner_id = @auth.id || @auth.role = 'admin'",
    "create_rule": "@auth.id != '' && owner_id = @auth.id",
    "update_rule": "owner_id = @auth.id || @auth.role = 'admin'",
    "delete_rule": "owner_id = @auth.id || @auth.role = 'admin'"
  }
  ```

### 8.4. `PUT /api/tables/:name/rules`
Saves or updates RLS rules for a table.

### 8.5. `DELETE /api/tables/:name/rules`
Clears all RLS rules for a table, restoring unrestricted public access.

---

## 9. Cron Jobs & Recurring Scheduler

LionSQL features an embedded, non-blocking Cron Scheduler supporting standard 5-field cron syntax (`min hour dom month dow`).

### 9.1. `GET /api/crons`
Lists all registered cron jobs.

### 9.2. `POST /api/crons`
Registers a new cron job.
- **Request Body**:
  ```json
  {
    "name": "Nightly Log Purge",
    "schedule": "0 2 * * *",
    "command_type": "sql",
    "command": "DELETE FROM logs WHERE created_at < NOW() - 86400",
    "enabled": true
  }
  ```

### 9.3. `PUT /api/crons/:id`
Updates an existing cron job schedule or command.

### 9.4. `DELETE /api/crons/:id`
Removes a cron job.

### 9.5. `POST /api/crons/:id/run`
Triggers an immediate one-off execution of a job.
- **Response `200 OK`**:
  ```json
  {
    "success": true,
    "last_status": "success",
    "duration_ms": 12
  }
  ```

---

## 10. Mailer & SMTP Settings

Transactional outbound email delivery with TLS and built-in HTML templates.

### 10.1. `GET /api/settings/mail`
Fetches current SMTP server settings with passwords masked.

### 10.2. `PUT /api/settings/mail`
Configures SMTP server credentials and sender profiles.
- **Request Body**:
  ```json
  {
    "host": "smtp.mailgun.org",
    "port": 587,
    "username": "postmaster@domain.com",
    "password": "secret-password",
    "from_email": "alerts@domain.com",
    "from_name": "Domain Alerts",
    "tls": true,
    "enabled": true
  }
  ```

### 10.3. `POST /api/settings/mail/test`
Dispatches an immediate test email to verify SMTP configuration.

### 10.4. `GET /api/mail/logs`
Returns the recent email delivery ring buffer log.

### 10.5. `DELETE /api/mail/logs`
Clears delivery log records.

### 10.6. `POST /api/auth/request-password-reset`
Dispatches a password reset link to the specified email address using the `password_reset` template.

---

## 11. Client SDK Static Distribution

LionSQL hosts the official client library directly for zero-dependency browser integration.

- `GET /sdk/lionsql.js`: Universal JavaScript Client SDK bundle (`Content-Type: application/javascript`)
- `GET /sdk/lionsql.d.ts`: TypeScript Declaration definitions (`Content-Type: text/typescript`)

---

## 12. High-Availability & WAL Replication Endpoints

LionSQL supports 1-Writer Multi-Reader High-Availability clustering with linear WAL streaming and zero-downtime failover promotion.

### 12.1. `GET /api/replication/status`
Returns the replication state, role, committed LSN, and follower statuses.

- **Response `200 OK` (Primary Node)**:
  ```json
  {
    "role": "primary",
    "node_id": "primary-node-1",
    "current_lsn": 105400,
    "replayed_lsn": 105400,
    "is_read_only": false,
    "connected_replicas": 2,
    "replicas": [
      {
        "id": "replica-node-1",
        "remote_addr": "10.0.0.2:48392",
        "last_acked_lsn": 105398,
        "replication_lag": 2,
        "connected_at": "2024-10-02T10:00:00Z",
        "last_ack_at": "2024-10-02T10:14:32Z"
      }
    ]
  }
  ```

- **Response `200 OK` (Replica Node)**:
  ```json
  {
    "role": "replica",
    "node_id": "replica-node-1",
    "current_lsn": 105398,
    "replayed_lsn": 105398,
    "is_read_only": true,
    "primary_url": "http://10.0.0.1:8090"
  }
  ```

### 12.2. `GET /api/replication/stream`
Streams chunked, line-delimited binary-framed WAL records from the primary node to connected followers.

- **Query Parameters**:
  - `from_lsn`: Starting LSN sequence number (defaults to `0` or replica's current position).
  - `replica_id`: Unique identifier of the requesting follower node.
- **Headers**:
  - `Content-Type: text/plain; charset=utf-8`
  - `Cache-Control: no-cache, no-transform`
- **Stream Format**:
  - Heartbeat lines: `:heartbeat\n` (sent every 5 seconds to keep the HTTP connection alive).
  - Serialized WAL frame records: `{"lsn": 105401, "tx_id": 9928, "type": "INSERT", "table": "accounts", ...}\n`

### 12.3. `POST /api/replication/ack`
Replicas report heartbeat acknowledgments and their latest replayed LSN to the primary node.

- **Request Body**:
  ```json
  {
    "node_id": "replica-node-1",
    "acked_lsn": 105401
  }
  ```
- **Response `200 OK`**:
  ```json
  {
    "ok": true
  }
  ```

### 12.4. `POST /api/replication/promote`
Instantly promotes a secondary replica into an active primary node capable of accepting writes. Lifts read-only locks without restarting the process.

- **Response `200 OK`**:
  ```json
  {
    "status": "promoted",
    "role": "primary"
  }
  ```

### 12.5. `POST /api/replication/demote`
Demotes an active primary node into a secondary replica and attaches it to a new primary URL.

- **Request Body**:
  ```json
  {
    "primary_url": "http://10.0.0.2:8090"
  }
  ```
- **Response `200 OK`**:
  ```json
  {
    "status": "demoted",
    "role": "replica"
  }
  ```



