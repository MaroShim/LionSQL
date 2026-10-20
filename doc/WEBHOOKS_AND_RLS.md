# LionSQL Webhooks & Declarative Row-Level Security (RLS) Guide

LionSQL combines the performance and ACID transaction guarantees of an enterprise relational database with the developer velocity of a Backend-as-a-Service (BaaS).

This document outlines the architecture, configuration, and practical usage of two core BaaS features:
1. **Asynchronous Webhook Dispatch Engine**: Real-time HTTP push notifications for database mutations.
2. **Declarative Row-Level Security (RLS)**: PocketBase-style granular access control rules per table with `@auth` variable substitution.

---

## Part 1: Asynchronous Webhook Dispatch Engine

### 1.1. Architecture Overview

Whenever rows are inserted, updated, deleted, or bulk-imported into LionSQL (via REST API, Web Studio, or T-SQL statements), the internal `RealtimeBroker` broadcasts mutation events. The `WebhookDispatcher` listens for these events and dispatches HTTP POST payloads asynchronously to registered endpoints without blocking the database writer transaction.

```
       [ Client / API / T-SQL Query ]
                     │
                     ▼
          [ Database Engine (OCC/WT) ]
                     │
                     ▼
             [ RealtimeBroker ]
                     │
          ┌──────────┴──────────┐
          │                     │
          ▼                     ▼
  [ Realtime SSE ]     [ WebhookDispatcher ]
  (Browser Clients)    ├─ Event Queue (cap 1024)
                       ├─ Worker Pool (4 goroutines)
                       ├─ HMAC-SHA256 Signer
                       ├─ Exponential Backoff (3 retries)
                       └─ Delivery Log (Ring Buffer 200)
                                │
                                ▼
                       [ External Webhook URLs ]
                       (Discord, Slack, Zapier, Microservices)
```

### 1.2. Key Features

- **Non-Blocking In-Memory Queue**: Dispatches occur off the hot path via buffered channels (capacity: 1024).
- **Worker Pool**: 4 dedicated goroutine workers process HTTP deliveries concurrently.
- **Exponential Backoff Retries**: Transient failures (e.g. HTTP 500, network timeout) are retried up to 3 times with exponential backoff (1s, 2s, 4s).
- **HMAC-SHA256 Payload Signature**: Outgoing requests include an `X-LionSQL-Signature: sha256=<hex>` header computed using the webhook's pre-shared secret.
- **Delivery Log Ring Buffer**: The latest 200 delivery attempts (status code, latency in ms, attempt count, timestamp) are kept in memory and inspectable via REST or Web Studio.
- **Web Studio Integration**: Full GUI under the "Webhooks" navigation tab for registering webhooks, triggering instant test pings, and viewing delivery logs.

### 1.3. Webhook REST API Reference

#### 1. List Webhooks
```http
GET /api/webhooks
Authorization: Bearer <TOKEN>
```
Response:
```json
{
  "items": [
    {
      "id": "wh-0192384a-9b12-70b1-8209-1a2b3c4d5e6f",
      "name": "Audit Service",
      "table": "customers",
      "events": ["create", "update", "delete"],
      "url": "https://api.example.com/webhooks/audit",
      "secret": "my-secret-key",
      "enabled": true,
      "created_at": "2024-09-29T10:00:00Z"
    }
  ]
}
```

#### 2. Register Webhook
```http
POST /api/webhooks
Authorization: Bearer <TOKEN>
Content-Type: application/json

{
  "name": "Discord Alert",
  "table": "orders",
  "events": ["create"],
  "url": "https://discord.com/api/webhooks/...",
  "secret": "optional-secret",
  "enabled": true
}
```

#### 3. Send Test Ping
```http
POST /api/webhooks/:id/test
Authorization: Bearer <TOKEN>
```
Response:
```json
{
  "success": true,
  "status_code": 200,
  "duration_ms": 18,
  "attempt_count": 1
}
```

#### 4. Delete Webhook
```http
DELETE /api/webhooks/:id
Authorization: Bearer <TOKEN>
```

#### 5. View Delivery Logs
```http
GET /api/webhooks/deliveries?limit=50
Authorization: Bearer <TOKEN>
```
Response:
```json
{
  "items": [
    {
      "id": "del-0192384c-...",
      "webhook_id": "wh-0192384a-...",
      "webhook_name": "Audit Service",
      "event": "create",
      "table": "customers",
      "status_code": 200,
      "duration_ms": 12,
      "success": true,
      "attempt_count": 1,
      "timestamp": "2024-09-29T10:05:00Z"
    }
  ]
}
```

---

## Part 2: Declarative Row-Level Security (RLS)

### 2.1. Concepts & Threat Model

Unlike traditional RDBMS where row security requires complex triggers or vendor-specific SQL policies, LionSQL provides simple, declarative **PocketBase-style rules** per table.

Rules are defined as boolean expressions evaluated at runtime for each REST API operation:

| Rule Name | Target Endpoint | Evaluation Moment |
| :--- | :--- | :--- |
| **`list_rule`** | `GET /api/records/:table` | Injected into the query filter: `(list_rule) && (client_filter)` |
| **`view_rule`** | `GET /api/records/:table/:id` | Evaluated against the fetched row; blocks unauthorized read (`403`) |
| **`create_rule`** | `POST /api/records/:table` | Evaluated against the incoming payload before insertion |
| **`update_rule`** | `PATCH/PUT /api/records/:table/:id` | Evaluated against the existing record before updating |
| **`delete_rule`** | `DELETE /api/records/:table/:id` | Evaluated against the existing record before deletion |

> **Public Access vs Strict Denial**:
> - **Empty Rule (`""`)**: Unrestricted public access (anyone can perform the action).
> - **Condition (`owner_id = @auth.id`)**: Allowed only if the condition evaluates to `true`.
> - **Impossible Condition (`false` or `@auth.role = 'admin'`)**: Blocks all unauthenticated or non-admin requests.

### 2.2. Dynamic `@auth.*` Variables

LionSQL automatically resolves authentication tokens from the request context (JWT token, API Key, or `X-User-ID` / `X-Tenant-ID` headers):

- `@auth.id`: The unique identifier of the authenticated user.
- `@auth.role`: The user's role (e.g. `'admin'`, `'editor'`, `'member'`).
- `@auth.email`: The authenticated user's email address.
- `@auth.tenant_id`: The organization or tenant ID for multi-tenant SaaS applications.

### 2.3. Common Rule Presets

#### 1. Owner-Only Access (Personal Data / Notes)
Only the user who created the record can read, update, or delete it:
- `list_rule`: `owner_id = @auth.id || @auth.role = 'admin'`
- `view_rule`: `owner_id = @auth.id || @auth.role = 'admin'`
- `create_rule`: `@auth.id != '' && owner_id = @auth.id`
- `update_rule`: `owner_id = @auth.id || @auth.role = 'admin'`
- `delete_rule`: `owner_id = @auth.id || @auth.role = 'admin'`

#### 2. Public Read / Authenticated Write (Blog / Community)
Anyone can read published posts; only logged-in authors can create or edit:
- `list_rule`: `status = 'published' || owner_id = @auth.id || @auth.role = 'admin'`
- `view_rule`: `status = 'published' || owner_id = @auth.id || @auth.role = 'admin'`
- `create_rule`: `@auth.id != ''`
- `update_rule`: `owner_id = @auth.id || @auth.role = 'admin'`
- `delete_rule`: `owner_id = @auth.id || @auth.role = 'admin'`

#### 3. Multi-Tenant SaaS Isolation
Users can only ever see or modify records belonging to their tenant organization:
- `list_rule`: `tenant_id = @auth.tenant_id`
- `view_rule`: `tenant_id = @auth.tenant_id`
- `create_rule`: `tenant_id = @auth.tenant_id`
- `update_rule`: `tenant_id = @auth.tenant_id`
- `delete_rule`: `tenant_id = @auth.tenant_id`

### 2.4. RLS REST API

#### 1. Fetch Table Rules
```http
GET /api/tables/:name/rules
Authorization: Bearer <TOKEN>
```
Response:
```json
{
  "table": "documents",
  "list_rule": "owner_id = @auth.id",
  "view_rule": "owner_id = @auth.id",
  "create_rule": "@auth.id != ''",
  "update_rule": "owner_id = @auth.id",
  "delete_rule": "owner_id = @auth.id"
}
```

#### 2. Update Table Rules
```http
PUT /api/tables/:name/rules
Authorization: Bearer <TOKEN>
Content-Type: application/json

{
  "list_rule": "owner_id = @auth.id || @auth.role = 'admin'",
  "view_rule": "owner_id = @auth.id || @auth.role = 'admin'",
  "create_rule": "@auth.id != ''",
  "update_rule": "owner_id = @auth.id || @auth.role = 'admin'",
  "delete_rule": "owner_id = @auth.id || @auth.role = 'admin'"
}
```

#### 3. Clear Table Rules (Reset to Public)
```http
DELETE /api/tables/:name/rules
Authorization: Bearer <TOKEN>
```

---

## Part 3: Web Studio GUI Workflow

1. Navigate to the LionSQL Web Studio at `http://127.0.0.1:8090/`.
2. Under **Tables & Records Studio**:
   - Click the **Security Rules (RLS)** subview button.
   - Choose a preset (e.g. *Owner Only* or *Multi-Tenant*) or define custom expressions.
   - Click **Save Rules**.
3. Under **Webhooks**:
   - Click **+ Register Webhook**.
   - Select the target table (or `*` for all tables), trigger events (`create`, `update`, `delete`), and URL.
   - Click **⚡ Test** to verify delivery status, latency, and response code in real time.
