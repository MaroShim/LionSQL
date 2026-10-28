# LionSQL Web Studio User Guide

> [한국어 문서 (Korean Version)](WEB_STUDIO_GUIDE.ko.md)

LionSQL Web Studio is an all-in-one browser-based database management and development tool embedded directly into the single LionSQL static binary (`embed.FS`). It operates with zero external web server dependencies (such as Nginx or Apache) and zero runtime dependencies (such as Node.js or Python).

From local development setups to cloud production deployments, users can design schemas, execute data CRUD operations, run T-SQL queries, analyze cost-based execution plans (EXPLAIN), generate ready-to-run REST API client code, inspect real-time query audit logs, and trigger non-blocking hot backups directly from any modern web browser.

---

## Table of Contents

1. [Access & Default Interface](#1-access--default-interface)
2. [Overview Dashboard & Real-Time Monitoring](#2-overview-dashboard--real-time-monitoring)
3. [Table & Schema Management](#3-table--schema-management)
4. [Data Grid Viewing & Editing (CRUD)](#4-data-grid-viewing--editing-crud)
5. [Data Import & Export](#5-data-import--export)
6. [SQL Query Console & Execution Plan Analysis](#6-sql-query-console--execution-plan-analysis)
7. [Developer API Preview](#7-developer-api-preview)
8. [Real-Time Query & Request Logs](#8-real-time-query--request-logs)
9. [Database Hot Backup & Restore](#9-database-hot-backup--restore)
10. [Security & Performance Optimization](#10-security--performance-optimization)

---

## 1. Access & Default Interface

### 1.1. Access URL and Automatic Redirect
Once the LionSQL server process starts, the Web Studio is immediately accessible on the HTTP management port (default: TCP `8090`).

- **Default URL**: `http://127.0.0.1:8090/_/`
- **Root Redirect**: Accessing the root URL (`http://127.0.0.1:8090/`) in a web browser safely returns an HTTP 302 redirect directly to the studio interface at `/_/`.

### 1.2. Administrator Authentication & Password Visibility Toggle
When security authentication is enabled, administrative login credentials are required.
- **Default Admin Email**: `admin@lionsql.local`
- **Default Password**: `1234567890` (configurable via server startup flags or environment variables)
- **Password Eye-Toggle**: The login screen and configuration modals feature an eye-toggle button to the right of the password field, allowing instant switching between masked (`password`) and unmasked (`text`) inputs.

### 1.3. Dark Mode / Light Mode Theme Switching
Clicking the theme toggle button in the top navigation bar switches between Dark Mode and Light Mode.
- **Automatic System Preference Detection**: Detects OS or browser color scheme preferences on initial load.
- **High-Contrast Readability**: Light Mode features an engineered CSS variable palette ensuring primary key (PK) badges, warning dialogs, inactive tabs, and deletion modals remain high-contrast and legible.

### 1.4. Left Sidebar Navigation Structure
The left sidebar is organized into a global home dashboard (`Overview`) and 4 functional categories comprising 12 core tools:
- **Global Dashboard (Home)**:
  - `Overview`: Real-time system resource usage, server uptime, and transactions/queries per second (TPS/QPS).
- **DATABASE & QUERY**:
  - `Tables & Records`: Table schema design and spreadsheet data CRUD grid.
  - `SQL Query`: Multi-query execution, T-SQL batching, and bytecode EXPLAIN execution plan analyzer.
  - `Stored Procedures`: T-SQL stored procedures and user-defined function (UDF) source inspection and interactive execution.
- **AUTOMATION & STREAM**:
  - `Realtime Live Stream`: WebSocket/SSE change data capture (CDC) event stream monitor.
  - `Web Hooks`: Outbound webhook dispatcher configuration for INSERT, UPDATE, and DELETE mutations.
  - `Cron Jobs`: Embedded sub-minute cron scheduler for recurring SQL queries, data rollups, and automated tasks.
- **DEVELOPER & ACCESS**:
  - `Auth & API Keys`: User account provisioning, RBAC role assignment (Admin, Read-Only), and API key management.
  - `Client SDK`: JavaScript/TypeScript client library integration and polyglot REST API code snippets.
- **MONITORING & SYSTEM**:
  - `Request Logs`: Real-time HTTP request/response latency, HTTP status codes, and remote IP audit log viewer.
  - `Replication (HA)`: Primary/Replica topology, LSN synchronization lag, and failover health checks.
  - `Settings / Mail`: Embedded transactional SMTP mailer configuration, template testing, and runtime parameters.

---

## 2. Overview Dashboard & Real-Time Monitoring

The **Overview** tab provides an immediate snapshot of LionSQL's runtime health, performance throughput, and client connectivity details.

### 2.1. System Metrics
Six core metric cards are arranged in a 3x2 grid, utilizing rolling-window instant deltas to reflect live system workload without latency:
- **Database Engine**: Active storage engine backend (`In-Memory OCC` or `WiredTiger B+Tree MVCC`).
- **Active Sessions**: Number of concurrent client sessions managed by the goroutine connection pooler.
- **Tables & Views**: Total registered user tables and views in the database catalog.
- **Server Uptime**: Continuous uptime of the database process.
- **Transactions (TPS)**: Real-time transaction throughput (commits per second during the polling window) and cumulative commits (`Total Committed`).
- **Queries (QPS)**: Real-time SQL query throughput (executions per second during the polling window) and cumulative queries (`Total Executed`).

### 2.2. Real-Time Auto-Refresh (`UI-REALTIME-1`)
The top dashboard bar provides a dedicated real-time control panel:
- **Live Indicator**: Emits a green pulse signal when active background synchronization is engaged.
- **Auto-Refresh Toggle**: Toggle automatic polling on or off with a single click.
- **Interval Selector**: Choose polling intervals from `3s`, `5s`, `10s`, `30s`, `60s`, or `Off`.

### 2.3. Client Connection Guide Panel
Connection parameters for external client tools (e.g., DBeaver, Beekeeper Studio, DbGate, sqlcmd, psql) are presented in clean copyable cards:
- **PostgreSQL Wire Protocol**: `localhost:5433` (default user: `admin`, database: `postgres`)
- **Microsoft SQL Server TDS**: `localhost:1433` (default user: `sa`, database: `master`)
- **HTTP REST & BaaS**: `localhost:8090` (Bearer Token authentication)

### 2.4. Seed Sample Database
For rapid testing of new database instances, a one-click sample database generator is available:
- **Empty DB Guard**: To prevent accidental data overwrites on active systems, the seed button is only enabled when the database catalog contains zero user tables.
- **Generated Tables**:
  - `customers`: Customer profiles (ID, Name, Email, Balance).
  - `products`: Product catalog (ID, Title, Price, Inventory Stock).
  - `sales`: Order headers (Order Number, Customer ID, Timestamp, Total Amount).
  - `accounts`: Payment accounts ledger (Account Number, Owner, Currency, Balance).

---

## 3. Table & Schema Management

The **Tables** tab in the sidebar provides complete management over all table schemas and physical column definitions.

### 3.1. Table and Column Inspection
Selecting a table renders a detailed schema card display:
- **Column Name**: Unique column identifier.
- **Data Type**: Standardized SQL data type (`INT`, `BIGINT`, `VARCHAR`, `DECIMAL`, `DATETIME`, `BIT`, `UUID`, `JSON`, etc.).
- **Primary Key (PK)**: High-contrast primary key indicator badge.
- **Nullable**: Nullability constraint flag.
- **Default Value**: Explicit default expression or value.

### 3.2. DDL Viewer & Export (`UI-DDL-1`)
Clicking the **`DDL`** button in the upper-right corner opens the DDL export modal:
- **SQL DDL Tab**: Provides the complete `CREATE TABLE` DDL script to reproduce the table definition in LionSQL.
- **JSON Schema Tab**: Provides a structured JSON Schema specification for frontend validation and external service integration.
- **One-Click Copy**: Dedicated `Copy SQL` and `Copy JSON` buttons copy code directly to the clipboard.

### 3.3. Safe Drop Table Confirmation Modal (`UI-DROP-CONFIRM`)
A strict two-stage safety mechanism prevents accidental data loss:
- **Name Verification**: Clicking `Drop Table` invokes a confirmation dialog rather than executing immediately.
- **Activation Rule**: The red `Drop Table` confirmation button remains disabled until the user types the exact table name into the confirmation field without typos.

---

## 4. Data Grid Viewing & Editing (CRUD)

User table records are presented in a high-speed interactive spreadsheet grid with direct editing capabilities.

### 4.1. Record Browsing & Pagination
- High-volume tables are paged smoothly with 20 or 50 rows per page to prevent UI lag.
- A search input filters records in real time across columns.

### 4.2. Adding Records
- Clicking **`+ Add Row`** opens a schema-driven record creation modal.
- Form inputs validate entries according to target column data types, and `IDENTITY` primary keys automatically compute incremented values.

### 4.3. Inline Editing & Deletion
- Clicking **Edit** on any row enables direct field editing, persisting updates via `PUT /api/records/:table/:id`.
- Single-row **Delete** and multi-row checkbox bulk deletion are supported with explicit safety prompts.

---

## 5. Data Import & Export (`UI-DATA-1`)

The Web Studio provides a modal interface for bulk importing records from external files and exporting table contents.

### 5.1. Data Import Modal
Accessible via the **`Import`** button on any table view:
1. **Format Support**: Delimited CSV files and structured JSON arrays.
2. **Drag-and-Drop Upload**: Drag and drop files onto the upload zone or browse using the system file picker.
3. **Data Preview**: Parses the file and renders a preview table of the first 5 rows to confirm column mapping prior to execution.
4. **Atomic Batch Insert**: Batches thousands of records within transactional boundaries for high-speed, safe insertion.

### 5.2. Data Export Modal
Accessible via the **`Export`** button on any table view:
- **CSV Export**: Downloads a UTF-8 CSV file compatible with Excel, Google Sheets, and standard spreadsheet tools.
- **JSON Export**: Downloads a structured JSON array matching REST API schemas.

---

## 6. SQL Query Console & Execution Plan Analysis

The **SQL Console** tab enables writing and executing ad-hoc T-SQL queries against the database engine.

### 6.1. SQL Editor & Keyboard Shortcuts
- Multi-line SQL text editor with syntax highlighting and automatic indentation.
- **Execution Shortcut**: Press `Ctrl + Enter` (Windows/Linux) or `Cmd + Enter` (macOS) to run queries immediately without reaching for the mouse.
- Supports complete T-SQL statements: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE PROCEDURE`, `EXEC`, transactions, and control flow.

### 6.2. Execution Results & Metrics
- Query results are displayed in a clean tabular grid.
- Execution latency (in microseconds or milliseconds) and row count (`Rows Affected`) are reported in real time.

### 6.3. Execution Plan Visualization (EXPLAIN / EXPLAIN ANALYZE)
- Clicking **`Explain`** visualizes the physical query plan tree produced by the LionSQL Cost-Based Optimizer (CBO) and Volcano execution engine.
- **Plan Analysis Metrics**:
  - `SeqScan` (full table scan) versus `IndexScan` (B+Tree index lookup).
  - Join strategies: `HashJoin` versus `IndexNestedLoopJoin`.
  - Operator cost estimates and actual execution time breakdowns across `Sort`, `Aggregate`, and `Filter` nodes.

---

## 7. Developer API Preview

Whenever a table is created, LionSQL automatically exposes zero-configuration RESTful CRUD endpoints. The **Developer API Preview** modal gives developers immediate, copy-pasteable client code without needing external documentation.

### 7.1. Accessing the Modal
Click the **`</> API Preview`** button in the upper-right corner of any table view.

### 7.2. Three Development Environment Tabs
The modal displays executable code snippets across three environments:
1. **cURL Tab**:
   - Ready-to-run terminal commands for shell scripting and CLI verification.
   - Includes full header specifications and JSON request payloads for `GET /api/records/:table`, `POST`, `PUT`, and `DELETE`.
2. **Fetch (JavaScript) Tab**:
   - Asynchronous `fetch()` code for modern browsers, Node.js, Next.js, and serverless runtimes.
3. **LionSQL Web SDK Tab**:
   - Clean client SDK method invocations using the bundled `lionsql.js` library (`db.records('customers').find()`, `create()`, `update()`, `delete()`).

### 7.3. Schema-Aware Payload Auto-Mapping
Rather than outputting generic placeholder text, the modal inspects the active table's actual columns and types to generate syntactically valid sample payloads (integers as `1`, strings as `"Sample Text"`, booleans as `true`, and dates as ISO-8601 strings).

---

## 8. Real-Time Query & Request Logs (`UI-LOGS-1`)

The **Logs** tab provides a real-time audit trail of all API requests and SQL queries received by the LionSQL kernel.

### 8.1. Query History Tracking
Backed by an in-memory ring-buffer logger (`/api/logs/requests`), the viewer logs recent requests in real time:
- **Timestamp**: Execution timestamp (`YYYY-MM-DD HH:MM:SS`).
- **Method / Operation**: HTTP method (`GET`, `POST`, `PUT`, `DELETE`) or SQL operation type.
- **Query / Path**: Executed SQL query statement or targeted REST endpoint path.
- **Duration**: Request latency in milliseconds (slow queries highlighted).
- **Status**: Response status code (`200 OK`, `400 Bad Request`, `500 Internal Error`, etc.).
- **Remote IP**: Client IP address.

### 8.2. Log Filtering & Clearing
- **Search Filter**: Filter displayed log entries by table name, query keywords, or IP address in real time.
- **Clear Logs**: Click **`Clear`** to reset the local log viewer buffer.

---

## 9. Database Hot Backup & Restore

The **Backup** menu enables safe, zero-downtime database backups and restoration without service disruption or table locking.

### 9.1. One-Click Hot Backup
- Clicking **`Create Backup`** triggers an online snapshot that preserves transaction consistency.
- The resulting `.tar.gz` compressed archive downloads directly through the browser.
- Operates in a non-blocking fashion: concurrent read and write transactions continue without interruption.

### 9.2. Hot Restore
- Upload a previously captured backup archive to restore the database data directory to that exact state.
- A warning confirmation modal requires user acknowledgment before initiating the restore operation to prevent accidental overwrites.

---

## 10. Security & Performance Optimization

LionSQL Web Studio adheres to rigorous engineering standards for secure and fast operation in production environments.

### 10.1. Enterprise HTTP Security Headers
Every Web Studio HTTP response enforces enterprise security headers:
- **`X-Content-Type-Options: nosniff`**: Prevents MIME-type sniffing attacks.
- **`X-Frame-Options: DENY`**: Eliminates clickjacking risks by preventing iframe embedding.
- **`X-XSS-Protection: 1; mode=block`**: Enforces strict browser-level XSS filtering.
- **`Referrer-Policy: strict-origin-when-cross-origin`**: Protects sensitive referrer metadata from leaking across origins.

### 10.2. Cache Invalidation & Asset Freshness
- The main Web Studio document (`/_/`) is served with `Cache-Control: no-cache, no-store, must-revalidate` to ensure browsers always load the latest UI without stale client caches.
- Static JavaScript assets (`/sdk/lionsql.js`) utilize standard ETags and HTTP caching to optimize network performance.

### 10.3. High-Speed Gzip Compression
All HTML, CSS, and JavaScript assets are compressed with gzip on the fly for requests carrying `Accept-Encoding: gzip`, allowing the single-page application to load in tens of milliseconds over low-bandwidth networks.

---

## Related Documentation

- [LionSQL User Manual](USER_MANUAL.ko.md)
- [LionSQL Auto-REST Records Guide](REST_API_RECORDS_GUIDE.md)
- [LionSQL Web SDK Developer Guide](WEB_SDK.md)
- [LionSQL Client Tool Integration Guide](CLIENT_TOOL_INTEGRATION.md)
