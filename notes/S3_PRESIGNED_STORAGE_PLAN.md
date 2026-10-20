# LionSQL Pattern 1: S3 Pre-signed URL Direct Upload Architecture & Implementation Plan

This document outlines the technical design and phased implementation plan for **Pattern 1 (Client Direct Pre-signed URL Upload)**. Under this architecture, high-volume binary payload streaming and CPU-intensive image thumbnail processing are explicitly excluded from the database kernel (Spec-Out, `GEMINI.md` Section 8.7). Instead, clients upload directly to external object storage (AWS S3, MinIO, Cloudflare R2, etc.), while LionSQL securely stores relational references and metadata in structured columns.

---

## 1. Architectural Background & Design Principles

### 1.1 Rationale for Excluding In-Kernel Binary Streaming (GEMINI.md Section 8.7)
- **OLTP Performance Isolation (Golden Rule 15: Radical KISS & YAGNI)**: Streaming multi-megabyte/gigabyte binary chunks and computing raster image transforms inside the database process causes heap allocation spikes, GC pauses, and CPU core saturation in the Go runtime. This directly threatens LionSQL's sub-millisecond p99 latencies and 18,500+ TPS transaction throughput.
- **Platform Primitives First (Golden Rule 14)**: Distributed object persistence and edge media processing are domain-native capabilities of specialized object stores (AWS S3, MinIO, Cloudflare R2) and edge CDNs (Cloudflare Images, CloudFront).
- **Core Database Responsibilities**: LionSQL manages file metadata (filename, MIME type, payload size, upload timestamp) and access links (`VARCHAR(512)` S3 URI) with strict ACID guarantees, while zero bytes of raw binary payload traverse the database process.

### 1.2 Core Value Proposition of Pattern 1 (Direct Pre-signed Upload)
1. **Zero Database Host Overhead**: Absorbs thousands of concurrent uploads without consuming database network bandwidth or Go heap memory.
2. **Universal Cloud DX**: 100% interoperable out of the box with any S3-compatible object store (AWS S3, Cloudflare R2, MinIO, Google Cloud Storage, Wasabi).
3. **Pure Self-Contained Static Binary (Golden Rule 36)**: Implements an autonomous AWS SigV4 signing engine using only the Go standard library (`crypto/hmac`, `crypto/sha256`), eliminating heavy external AWS SDK dependencies and maintaining zero CGo overhead.

---

## 2. End-to-End Architecture & Data Flow

```
+----------------------------------------------------------------------------------------------------+
|                                    Pattern 1 Data Flow Architecture                                 |
+----------------------------------------------------------------------------------------------------+

     [ 1. Presign Request ]             [ 2. Issue Pre-signed PUT URL ]
 Client ─────────────────────────────► LionSQL (HTTP :8090 or T-SQL)
    │                                              │ (Pure Go SigV4 Engine, 0 External SDKs)
    │                                              ▼
    │                                [ Return upload_url & public_url ]
    │ ◄────────────────────────────────────────────┘
    │
    │ [ 3. Direct Binary Upload (HTTP PUT) ]
    └──────────────────────────────────────────────────────────────┐
                                                                   ▼
                                                       [ External S3 / MinIO / R2 ]
                                                                   │
                                                                   │ (Upload Succeeded: 200 OK)
    ┌──────────────────────────────────────────────────────────────┘
    │
    ▼ [ 4. Insert Record / Update Column (SQL INSERT / UPDATE) ]
 LionSQL Database Kernel
    │  - INSERT INTO products (id, name, image_url) VALUES (1, '...', 'https://s3.../item.png');
    │
    ▼ [ 5. (Optional) Emit CDC Stream Event ]
 LionSQL CDC / Outbox Streamer
    │
    ▼
 Async Image Worker (Lambda / Sidecar) ──► Generate Thumbnails ──► Upload S3 ──► UPDATE products.thumb_url
```

---

## 3. Core Architectural Components

### 3.1 Zero-Dependency Lightweight AWS SigV4 Signer (`pkg/storage/s3sign`)
Eliminates heavy official vendor SDKs by implementing a compact, self-contained package adhering strictly to the AWS Signature Version 4 specification using Go standard library cryptographic primitives.

- **Package Path**: `pkg/storage/s3sign`
- **Core API**:
  - `SignPresignedPUT(bucket, key string, contentType string, expiresIn time.Duration) (string, error)`
  - `SignPresignedGET(bucket, key string, expiresIn time.Duration) (string, error)`
- **Algorithm Pipeline**:
  1. Generate ISO 8601 timestamp (`YYYYMMDD'T'HHMMSS'Z'`) and date stamp (`YYYYMMDD`).
  2. Construct Canonical Request: HTTP Verb (PUT/GET), URI, Canonical Query String (`X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=...&X-Amz-Date=...&X-Amz-Expires=...&X-Amz-SignedHeaders=host`).
  3. Derive String to Sign: `AWS4-HMAC-SHA256` + Timestamp + Credential Scope + SHA-256(Canonical Request).
  4. Derive Signing Key via multi-stage HMAC-SHA256 chaining:
     $$K_{date} = \text{HMAC}(AWS4 + Secret, Date)$$
     $$K_{region} = \text{HMAC}(K_{date}, Region)$$
     $$K_{service} = \text{HMAC}(K_{region}, "s3")$$
     $$K_{signing} = \text{HMAC}(K_{service}, "aws4\_request")$$
  5. Compute Final Signature: $\text{Hex}(\text{HMAC}(K_{signing}, \text{StringToSign}))$.
  6. Return complete pre-signed URL with query parameters.

### 3.2 Storage Configuration Subsystem (`pkg/config`)
Unified configuration schema supporting standard object storage providers (AWS S3, Cloudflare R2, local MinIO).

```toml
# config.toml
[storage.s3]
enabled           = true
endpoint          = "https://s3.ap-northeast-2.amazonaws.com" # MinIO: "http://minio:9000", R2: "https://<account>.r2.cloudflarestorage.com"
region            = "ap-northeast-2"
access_key_id     = "${AWS_ACCESS_KEY_ID}"
secret_access_key = "${AWS_SECRET_ACCESS_KEY}"
default_bucket    = "lionsql-assets"
force_path_style  = false                                     # Set to true for MinIO / local dev
cdn_base_url      = "https://cdn.example.com"                 # (Optional) Cloudflare / CloudFront CDN public base URL
```

### 3.3 HTTP REST API Endpoint (`pkg/api/handlers_storage.go`)
Pre-signed URL issuance endpoint accessible by web frontends and the embedded Web Studio.

- **Endpoint**: `POST /api/storage/presign`
- **Authentication**: JWT Bearer token or API-Key (`Authorization: Bearer <token>`)
- **Request Body (JSON)**:
  ```json
  {
    "bucket": "lionsql-assets",
    "key": "avatars/user_1001.png",
    "method": "PUT",
    "expires_in": 600,
    "content_type": "image/png"
  }
  ```
- **Response Body (JSON)**:
  ```json
  {
    "upload_url": "https://lionsql-assets.s3.ap-northeast-2.amazonaws.com/avatars/user_1001.png?X-Amz-Algorithm=AWS4-HMAC-SHA256&...",
    "public_url": "https://cdn.example.com/avatars/user_1001.png",
    "key": "avatars/user_1001.png",
    "expires_at": "2024-10-17T01:00:00Z"
  }
  ```

### 3.4 T-SQL Built-in Scalar Functions (`pkg/proc/builtins_s3.go`)
Native scalar functions enabling pre-signed URL generation directly within Stored Procedures and interactive queries.

```sql
-- 1. Issue default PUT pre-signed URL (default 600 seconds validity)
SELECT S3_PRESIGNED_PUT_URL('avatars/user_1001.png') AS upload_url;

-- 2. Explicit bucket and TTL configuration
SELECT S3_PRESIGNED_PUT_URL('my-bucket', 'docs/contract_99.pdf', 300) AS upload_url;

-- 3. Issue GET pre-signed URL for private bucket asset retrieval
SELECT S3_PRESIGNED_GET_URL('invoices/inv_202410.pdf', 180) AS download_url;
```

### 3.5 Orphan Object Lifecycle & Cleanup Pipeline (CDC Event-Driven Lifecycle)
Safely purges unreferenced S3 objects when database records are deleted using LionSQL's native CDC pipeline.

1. **Record Deletion**:
   ```sql
   DELETE FROM users WHERE id = 1001;
   ```
2. **LionSQL CDC Event Emission**:
   A `ChangeEvent (kind: DELETE)` containing the deleted row's `avatar_url` is emitted via the internal CDC engine (`pkg/cdc`).
3. **Asynchronous Cleanup Worker**:
   A lightweight outbox relay worker parses the target S3 key and invokes S3 `DeleteObject` in the background, keeping the OLTP path free of external HTTP calls.

---

## 4. Phased Implementation Plan

### Phase 1: Lightweight SigV4 Signer & Unit Test Suite (`pkg/storage/s3sign`)
1. Define `Signer` struct and implement SHA-256 / HMAC cryptographic utilities.
2. Implement Canonical Request and StringToSign formatting pipelines.
3. Validate against the official AWS Canonical Test Suite:
   - AWS S3, Cloudflare R2, and MinIO compatibility verification.
   - Comprehensive edge-case handling for special characters, slashes (`/`), and URL query encoding.

### Phase 2: Configuration & HTTP REST API Layer (`pkg/config`, `pkg/api`)
1. Extend `pkg/config/config.go` with `S3Config` struct and environment variable resolution.
2. Implement `pkg/api/handlers_storage.go`:
   - `POST /api/storage/presign` request handler.
   - Content-Length ceiling and allowed MIME type validation (images, PDFs, documents).
3. Register route in `pkg/api/server.go` with RBAC authorization enforcement.
4. Add HTTP integration tests (`handlers_storage_test.go`).

### Phase 3: T-SQL Built-in Scalar Function Registration (`pkg/proc`, `pkg/engine`)
1. Implement `pkg/proc/builtins_s3.go`:
   - Register `S3_PRESIGNED_PUT_URL` and `S3_PRESIGNED_GET_URL`.
2. Connect scalar functions into VDBE bytecode VM and SQL interpreter.
3. Add end-to-end procedural execution tests with parameter passing.

### Phase 4: Embedded Web Studio Integration (`pkg/ui`)
1. Add "File Upload" field renderer (`file / image_url`) to the Web Studio record detail modal.
2. Browser-side upload flow:
   - Request temporary upload URL via `/api/storage/presign`.
   - Stream file directly to S3 via `fetch(upload_url, { method: 'PUT', body: file })` with progress feedback.
   - Automatically populate the returned `public_url` into the target column input.

---

## 5. Engineering Guidelines Compliance Matrix

| Guideline / Invariant | Application & Enforcement |
| :--- | :--- |
| **Golden Rule 14 (Platform Primitives First)** | Delegates binary object storage to native cloud/OS primitives instead of reinventing storage daemons inside the database kernel. |
| **Golden Rule 15 (Radical KISS & DRY)** | Replaces multi-megabyte vendor SDKs with a ~150-line pure-Go standard library implementation of the AWS SigV4 specification. |
| **Golden Rule 23 (Bounded Buffers)** | Enforces strict upload limits via pre-signed payload headers (`x-amz-content-sha256`) and size ceilings. |
| **Golden Rule 29 (Non-blocking)** | Decouples orphan object cleanup from synchronous DML write transactions via asynchronous CDC streaming. |
| **Golden Rule 36 (Pure Static Binaries)** | Zero CGo bindings and zero external network SDK dependencies, preserving the standalone single-binary release model. |
