# SQLion

High-Concurrency Embedded OLTP Database Engine & Multi-Protocol Stored Procedure Server

---

## Overview

SQLion is an enterprise-grade, high-concurrency embedded relational database engine written in Go and Cgo (WiredTiger). Designed with the simplicity of PocketBase and the operational power of enterprise relational engines, SQLion is delivered as a **single standalone executable** with zero external dependencies.

SQLion seamlessly integrates multi-protocol wire access (PostgreSQL v3 and Microsoft SQL Server TDS), an embedded web administration dashboard, a native T-SQL stored procedure bytecode virtual machine, a cost-based query optimizer (CBO), and a built-in goroutine connection pooler that eliminates the need for external tools like PgBouncer.

```
                    +------------------------------------------+
                    |                  SQLion                  |
                    |  (Single Standalone Static Go Executable)|
                    +------------------------------------------+
                                         │
        ┌────────────────────────────────┼────────────────────────────────┐
        │                                │                                │
        ▼                                ▼                                ▼
[Postgres Wire v3]             [MSSQL TDS 7.x/8.0]             [Web Studio & BaaS API]
  Port :5433                      Port :1433                      Port :8090 /_/
 (psql, DBeaver,                  (sqlcmd, SSMS,                  (Browser Dashboard,
  GORM, Prisma)                    Azure Data Studio)              REST API, Realtime SSE,
        │                                │                         Webhooks, RLS, Cron,
        │                                │                         Mailer, JS/TS SDK)
        └────────────────────────────────┼────────────────────────────────┘
                                         │
                                         ▼
                    +------------------------------------------+
                    |      Goroutine Connection Pooler         |
                    |    (Replaces PgBouncer; 10,000+ conns)   |
                    +------------------------------------------+
                                         │
                                         ▼
                    +------------------------------------------+
                    |    Dual-Parser & Cost-Based Optimizer    |
                    | (Native Pratt + ANTLR4 Fallback + CBO)   |
                    +------------------------------------------+
                                         │
                                         ▼
                    +------------------------------------------+
                    |     Unified Native VDBE Bytecode VM      |
                    |  - Interactive DQL & DML (SELECT, DML)   |
                    |  - T-SQL Procedures, Triggers, UDFs, TVFs|
                    |  - sync.Pool Registers & Ambient Tx      |
                    +--------------------+---------------------+
                                         │
                    ┌────────────────────┴────────────────────┐
                    ▼                                         ▼
     [Volcano Relational Pipeline]             [Index & Direct Storage Ops]
     (Hash Join, work_mem External Sort,        (OpIndexScan SkipList/B+Tree,
      Aggregates, OpRelQuery Iteration)          OpInsert, OpUpdate, OpDelete)
                    │                                         │
                    └────────────────────┬────────────────────┘
                                         │
                                         ▼
                    +------------------------------------------+
                    |         Unified Storage Engine           |
                    |  (WiredTiger MVCC & Pure-Go Memory OCC)  |
                    +------------------------------------------+
```

---

## Key Features

- **Single Standalone Executable**: Ships as a single static binary embedding the database kernel, wire protocol servers, HTTP API, and web management studio via Go `embed.FS`. No runtime dependencies, JVM, or Node.js required.
- **Auto-Generated RESTful Records CRUD API**:
  - Instant zero-code REST endpoints (`/api/records/:table`) for all database tables upon creation.
  - Advanced URL query filter engine (`filter=(balance>500 && status='active') || country='US'`), multi-column sorting (`sort=-id,balance`), pagination, and schema-aware JSON typing.
- **Official Universal JavaScript & TypeScript Client SDK**:
  - PocketBase-compatible client (`sqlion.js`, `sqlion.d.ts`) served directly by the standalone executable (`GET /sdk/sqlion.js`).
  - Supports Web Browsers, Node.js (v18+), Bun, Deno, and Edge runtimes with collection CRUD, query builders, and typed models.
- **Declarative Row-Level Security (RLS)**:
  - Table-level access control rules (`list`, `view`, `create`, `update`, `delete`) with dynamic `@auth.id`, `@auth.role`, `@auth.email`, `@auth.tenant_id` context token substitution.
  - Automatically injects `list_rule` conditions into queries (`(list_rule) && (client_filter)`), preventing unauthorized data leakage.
- **Asynchronous Webhook Dispatch Engine**:
  - Dispatches committed database mutations (`create`, `update`, `delete`, `import`) to external URLs via non-blocking worker pools and buffered channels.
  - Includes HMAC-SHA256 payload integrity signatures, exponential backoff retries (up to 3 attempts), and a 200-entry delivery audit log.
- **Embedded Cron Jobs Scheduler**:
  - Non-blocking scheduler automating recurring SQL queries and T-SQL stored procedures using standard 5-field cron syntax (`min hour dom month dow`).
  - Live execution tracking with ms-precision duration and error logging.
- **Embedded SMTP Mailer & Transactional Email Templates**:
  - Pure-Go SMTP client with TLS/STARTTLS support and password masking.
  - Built-in HTML templates for `welcome`, `password_reset`, and `verification` workflows.
- **Concurrent Dual Wire Protocols**:
  - **PostgreSQL v3 Protocol (Port 5433)**: Compatible with `psql`, DBeaver, GORM, Prisma, asyncpg, and pgx. Supports SSL/TLS encryption and Extended Query Protocol (`Parse`, `Bind`, `Execute`, `Sync`).
  - **Microsoft SQL Server TDS 7.x/8.0 Protocol (Port 1433)**: Compatible with `sqlcmd`, SQL Server Management Studio (SSMS), Azure Data Studio, and `pymssql`. Supports RPC prepared statement pipeline (`sp_prepare`, `sp_execute`, `sp_prepexec`).
- **Single-Engine VDBE Virtual Machine Unification**:
  - Standalone interactive queries (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) and stored procedures execute on the exact same register-based VDBE Virtual Machine (`proc.CompileQuery`, `proc.CompileStatement` ➔ `s.executor.ExecuteInTx`).
  - Eliminates separate execution engines and unifies relational execution (`OpRelQuery`), secondary index scans (`OpIndexScan`), and DML mutations (`OpInsert`, `OpUpdate`, `OpDelete`) on a single VM.
  - Complete `EXPLAIN BYTECODE <sql>` disassembly inspection for all queries and procedures.
- **Prepared Statement VDBE Bytecode Caching**:
  - Both PGWire and TDS listeners compile parameterized queries (`$1`, `$2` and `@p1`, `@p2`) directly into VDBE `*proc.Program` bytecode during preparation.
  - Subsequent executions run precompiled bytecode directly, completely bypassing query re-parsing and string manipulation.
- **Embedded Web Management Studio (Port 8090)**: Instant, zero-install web dashboard served at `http://localhost:8090/_/`:
  - Spreadsheet-style table viewer with inline cell editing, pagination, and multi-column sorting.
  - Interactive SQL Query Studio with execution history, query bookmarks, `EXPLAIN ANALYZE` visualizer, and **Visual VDBE Bytecode Disassembler**.
  - Realtime Server-Sent Events (SSE) live synchronizing browser tables with concurrent background mutations.
  - Granular API Key and Superuser JWT authentication management.
  - Dedicated Webhooks, Cron Jobs, Mailer Settings, and Client SDK tabs.
- **Enterprise T-SQL Procedural Engine**:
  - Full support for `CREATE PROCEDURE`, `CREATE FUNCTION` (scalar and table-valued), and `CREATE TRIGGER` (`AFTER`, `INSTEAD OF`).
  - Virtual delta tables (`INSERTED` and `DELETED`) for trigger execution.
  - Control flow: `IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO` & labels, `PRINT`, `RAISERROR`, local variables (`@var`), and cursors.
  - Rich T-SQL extensions: Window functions (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), JSON/JSONB with extraction operators (`->`, `->>`, `JSON_VALUE`), Foreign Keys with cascading actions, table variables (`DECLARE @table TABLE`), `TRUNCATE TABLE`, `SELECT ... INTO`, and `DBCC` diagnostics.
- **Dual-Parser Architecture with Automatic Fallback**:
  - **Primary Fast-Path**: Zero-allocation Native Pratt parser achieving sub-microsecond parsing for high-frequency queries.
  - **Secondary Full-Spec Fallback**: Official Microsoft T-SQL ANTLR4 grammar parser invoked automatically on advanced syntax (bracket escapes, `WITH RECOMPILE`, vendor options).
- **Volcano Relational Engine & Memory Governor**:
  - Composable iterator execution: `SeqScan`, `IndexScan`, `Filter`, `Project`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`, `WindowIterator`, `CTE WITH`.
  - **`work_mem` Memory Governor**: Prevents out-of-memory (OOM) crashes by automatically spilling sorted chunks to disk and performing an N-way Min-Heap streaming external merge sort.
- **Cost-Based Optimizer (CBO)**:
  - `ANALYZE TABLE <name>` statistics collection: row counts, distinct values (`NDV`), null counts, and 64-bucket equi-depth histograms.
  - Cost formulas evaluating physical operator costs (disk page I/O + tuple CPU) to pick optimal join algorithms and index scan access paths.
- **Built-in Goroutine Connection Pooler**:
  - Handles 10,000+ simultaneous client connections over lightweight Go goroutines.
  - Internal worker pool and session reuse eliminate the need for PgBouncer or external poolers.
- **Online Non-Blocking Snapshot Hot Backup**:
  - Live, point-in-time `.tar.gz` database backups captured via read-only snapshot transactions without blocking writers or interrupting active OLTP traffic.
  - Accessible via CLI (`-backup`, `-restore`), REST API (`/api/backup`, `/api/restore`), and Web Studio UI.
- **High-Performance Storage Backends & TPC-C Benchmark**:
  - **In-Memory OCC Engine**: Fine-grained table-level striped locking, pure-Go concurrent SkipList secondary indexes, and zero-copy cursor iteration delivering **1,251,489 tpmC** and **43,832 TPS** on the standard TPC-C benchmark.
  - **WiredTiger (Default)**: Cgo-backed lock-free B+Tree with hazard pointers, MessagePack binary record codec, and cursor caching delivering **507,456 tpmC**.

---

## Quick Start Guide

### 1. Building from Source

#### Prerequisites
- Go 1.22 or higher
- (Optional, for WiredTiger backend) `wiredtiger` development headers and libraries:
  - macOS: `brew install wiredtiger`
  - Linux (Ubuntu/Debian): `sudo apt-get install libwiredtiger-dev`

#### Build Command

```bash
# Build production server with WiredTiger support
go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server

# Build pure-Go server (Zero Cgo dependency, runs everywhere)
go build -buildvcs=false -o bin/server ./cmd/server
```

### 2. Starting the Server

```bash
# Start SQLion with default ports and WiredTiger storage (creates 'data' directory)
./bin/server

# Or start with the pure-Go in-memory engine (zero Cgo required)
./bin/server -engine=memory
```

Upon startup, the console displays the connection endpoints:

```
------------------------------------------------------------
 SQLion - High-Concurrency OLTP & Stored Procedure Server
------------------------------------------------------------
 [Web Dashboard]  http://127.0.0.1:8090/_/
 [HTTP API]       http://127.0.0.1:8090/api/health
 [Superuser Auth] admin@sqlion.local (JWT Auth: false)
 [Postgres Wire]  psql -h 127.0.0.1 -p 5433 (DBeaver / psql)
 [TDS / MSSQL]    sqlcmd -S 127.0.0.1,1433 (DBeaver / Azure Data Studio / pymssql)
 [Data Directory] data
 [Storage Engine] wiredtiger
 [Session Pooler] Max Workers: 32, Max Idle Sessions: 64 (No PgBouncer required)
 [Memory Governor] work_mem: 64MB (External Merge Sort spill-to-disk enabled)
------------------------------------------------------------
```

### 3. Accessing the Web Studio

Open your browser and navigate to:
```
http://127.0.0.1:8090/_/
```
The Web Studio provides immediate visual table inspection, real-time query execution, stored procedure inspection, and one-click database backup/restore.

---

## Client Connectivity

### Connecting via `psql` (PostgreSQL Client)

```bash
psql -h 127.0.0.1 -p 5433 -U admin
```

```sql
-- Query sample seeded data
SELECT * FROM accounts;

-- Check execution plan and cost estimates
EXPLAIN ANALYZE SELECT * FROM accounts WHERE balance > 600 ORDER BY balance DESC;

-- Configure session query memory budget
SET work_mem = '128MB';
SHOW work_mem;
```

### Connecting via `sqlcmd` (MSSQL Client)

```bash
sqlcmd -S 127.0.0.1,1433 -U admin
```

```sql
-- Execute stored procedure
EXEC TransferFunds @from = 1, @to = 2, @amount = 100;
GO
```

### Bulk Copy Streaming with `psql \copy`

```bash
-- High-speed data export to CSV
psql -h 127.0.0.1 -p 5433 -c "\copy accounts to 'accounts.csv' with (format csv, header)"

-- High-speed data import from CSV
psql -h 127.0.0.1 -p 5433 -c "\copy accounts from 'accounts.csv' with (format csv, header)"
```

---

## Application Integration & ORMs

### Go (`pgx` / `database/sql`)

```go
package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dsn := "postgres://admin:1234567890@127.0.0.1:5433/sqlion?sslmode=disable"
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, holder, balance FROM accounts")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var holder string
		var balance int
		if err := rows.Scan(&id, &holder, &balance); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Account: %d | %s | %d\n", id, holder, balance)
	}
}
```

### JavaScript / TypeScript (Official Client SDK)

```javascript
import SQLion from './sdk/sqlion.js';

const client = new SQLion('http://127.0.0.1:8090', { auth: 'YOUR_API_KEY' });

// 1. Query records with declarative filtering & sorting
const res = await client.collection('accounts').getList(1, 20, {
  filter: 'balance > 500',
  sort: '-balance'
});
console.log(res.items);

// 2. Real-time Live SSE change stream
const unsubscribe = client.collection('accounts').subscribe((event) => {
  console.log('Account mutated:', event.action, event.record);
});

// 3. Execute T-SQL Stored Procedure
await client.exec('TransferFunds', { from: 1, to: 2, amount: 50 });
```

### Python (`psycopg2` / `SQLAlchemy`)

```python
import psycopg2

conn = psycopg2.connect(
    host="127.0.0.1",
    port=5433,
    user="admin",
    password="",
    dbname="sqlion"
)

with conn.cursor() as cur:
    cur.execute("SELECT holder, balance FROM accounts WHERE balance > %s", (500,))
    for row in cur.fetchall():
        print(f"Holder: {row[0]}, Balance: {row[1]}")

conn.close()
```

### Node.js (`pg` / `Prisma`)

```javascript
const { Client } = require('pg');

const client = new Client({
  host: '127.0.0.1',
  port: 5433,
  user: 'admin',
  database: 'sqlion',
});

async function run() {
  await client.connect();
  const res = await client.query('SELECT * FROM accounts');
  console.log(res.rows);
  await client.end();
}

run();
```

---

## Command-Line Interface (CLI) Reference

### Configuration Flags

| Flag | Default | Description |
|---|---|---|
| `-http` | `127.0.0.1:8090` | HTTP service and embedded Web Studio address |
| `-pg` | `127.0.0.1:5433` | PostgreSQL wire protocol listen address |
| `-tds` | `127.0.0.1:1433` | Microsoft SQL Server TDS protocol listen address |
| `-dir` | `data` | Database storage directory |
| `-engine` | `wiredtiger` | Storage engine backend (`wiredtiger` or `memory`) |
| `-work-mem` | `64MB` | Default memory limit per query operator before spill-to-disk |
| `-max-workers` | `NumCPU * 4` | Maximum concurrent query execution workers |
| `-max-idle-sessions` | `64` | Maximum idle storage sessions to retain in pool |
| `-auth` | `false` | Enforce JWT authentication on protected REST endpoints |
| `-seed` | `true` | Seed sample accounts and `TransferFunds` procedure on startup |

### Background Daemon Controls

SQLion can run as a background service without third-party daemon wrappers:

```bash
# Start SQLion in the background
./bin/server -d

# Check daemon status and PID
./bin/server -status

# Restart the running daemon
./bin/server -restart

# Stop the daemon gracefully
./bin/server -stop
```

### Backup & Restore Operations

```bash
# Offline or CLI-driven snapshot hot backup to target archive
./bin/server -dir=data -backup=/backups/sqlion-snapshot.tar.gz

# Restore database from archive
./bin/server -dir=data -restore=/backups/sqlion-snapshot.tar.gz

# Online live backup download via curl while server is running
curl -OJ http://127.0.0.1:8090/api/backup

# Online restore via HTTP POST multipart upload
curl -X POST -F "backup=@/backups/sqlion-snapshot.tar.gz" http://127.0.0.1:8090/api/restore
```

---

## HTTP REST API Reference

SQLion exposes a complete PocketBase-style REST API on `:8090`:

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/health` | Service health and version check |
| `GET` | `/api/pool/stats` | Realtime worker pool and connection pooler metrics |
| `GET` | `/api/realtime` | Server-Sent Events (SSE) live data mutation stream |
| `POST` | `/api/sql` | Execute SQL/T-SQL statement directly (`{"sql": "..."}`) |
| `GET` | `/api/tables` | List all tables and schemas |
| `POST` | `/api/tables` | Create a new table |
| `GET` | `/api/tables/{name}/records` | Get records with pagination, sorting, and search |
| `POST` | `/api/tables/{name}/records` | Insert a new record |
| `PATCH` | `/api/tables/{name}/records/{pk}` | Update a record by primary key |
| `DELETE` | `/api/tables/{name}/records/{pk}` | Delete a record by primary key |
| `GET` | `/api/procs` | List all registered stored procedures |
| `POST` | `/api/procs` | Register a stored procedure script |
| `POST` | `/api/procs/{name}/exec` | Execute stored procedure via JSON parameters |
| `GET` | `/api/backup` | Stream live `.tar.gz` snapshot hot backup |
| `POST` | `/api/backup` | Create backup file on server (`{"target": "path"}`) |
| `POST` | `/api/restore` | Restore database from archive file or multipart upload |

---

## Documentation Suite

SQLion provides comprehensive documentation covering system architecture, operational runbooks, internal engine specifications, and API guides:

### [Master Technical Documentation Directory (doc/README.md)](doc/README.md)
Start here for a complete subject-based directory of all architectural blueprints, internals, and guides.

### Backend-as-a-Service (BaaS) & Developer APIs
- **[Official JavaScript & TypeScript SDK Guide](doc/SDK.md)**: Universal zero-dependency client library for Web, Node.js, Bun, and Deno.
- **[Auto-Generated RESTful CRUD API](doc/AUTO_REST_API.md)**: Zero-code `/api/records/:table` endpoints with rich boolean filter expressions.
- **[Webhooks & Row-Level Security (RLS)](doc/WEBHOOKS_AND_RLS.md)**: Non-blocking mutation webhooks with HMAC signatures and declarative `@auth` access rules.
- **[Cron Jobs Scheduler & SMTP Mailer](doc/CRON_AND_MAILER.md)**: Embedded 5-field cron automation and transactional HTML email dispatching.
- **[HTTP REST API & Realtime SSE Specification](doc/API_REFERENCE.md)**: OpenAPI-style endpoint documentation and JavaScript EventSource examples.

### Core Architecture & Internals Specifications
- **[System Architecture Specification](doc/ARCHITECTURE.md)**: Exhaustive technical specification covering Volcano iterators, dual-parser decoupling, and storage backends.
- **[Architecture Decision Records (ADRs)](doc/ADR.md)**: Historical log of 14 foundational architectural decisions (ADR-001 through ADR-014).
- **[VDBE Bytecode Virtual Machine Internals](doc/INTERNALS_VDBE.md)**: Register allocation, opcode instruction set architecture (ISA), and procedural execution.
- **[Storage Engine Mechanics (WiredTiger & Memory OCC)](doc/INTERNALS_STORAGE.md)**: Lock-free B+Trees, hazard pointers, OCC validation, and AOF log compaction.
- **[Cost-Based Optimizer & Volcano Pipeline Internals](doc/INTERNALS_CBO_VOLCANO.md)**: Equi-depth histograms, physical cost formulas, and external merge sort.
- **[Multi-Wire Protocol Internals](doc/INTERNALS_WIRE_PROTOCOLS.md)**: Binary framing for PostgreSQL v3, TDS 7.x/8.0, and goroutine connection pooler.
- **[Virtual System Catalog & Data Dictionary](doc/DATA_DICTIONARY.md)**: System views (`sys.*`, `information_schema.*`), data types, and tool compatibility.
- **[High Availability & Disaster Recovery](doc/HIGH_AVAILABILITY.md)**: Multi-node replication topology, Raft consensus clustering, failover, and recovery SLAs.
- **[Extension & Driver Development Guide](doc/EXTENSIONS.md)**: Pluggable storage engines, fallback parsers, wire protocol drivers, and sandbox safety.

### User Guides & Operations
- **[T-SQL & SQL Syntax Reference Guide](doc/TSQL_REFERENCE.md)**: Complete language reference for procedures, triggers, UDFs, TVFs, and queries.
- **[HTTP REST API & Realtime SSE Specification](doc/API_REFERENCE.md)**: OpenAPI-style endpoint documentation and JavaScript EventSource examples.
- **[Observability, Metrics & Telemetry Guide](doc/METRICS.md)**: Prometheus time-series metrics, health probes, alerting rules, and Grafana integration.
- **[Production Deployment & Operations Runbook](doc/OPERATIONS.md)**: Systemd services, Docker deployment, disaster recovery, and security hardening.
- **[Performance Tuning & Benchmark Guide](doc/BENCHMARK.md)**: High-concurrency banking benchmark harness, TPS measurement, and memory tuning.
- **[Database Migration Guide](doc/MIGRATION.md)**: Procedures for migrating from PostgreSQL, MSSQL, SQLite, and MySQL.
- **[Security Policy & Threat Model](doc/SECURITY.md)**: RBAC access control, SQL injection immunity, and vulnerability disclosure.
- **[Engineering Roadmap & Milestones](doc/ROADMAP.md)**: Near-term and long-term milestones (Raft replication, vector search).
- **[Troubleshooting, Error Diagnostics & FAQ](doc/TROUBLESHOOTING.md)**: Port conflicts, serialization conflicts, slow query tuning, and client driver setup.
- **[Contributing & Development Guide](CONTRIBUTING.md)**: Engineering standards, 3-stage verification pipeline, and pull request checklist.
- **[Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md)**: Standards, pledge, enforcement responsibilities, and guidelines.
- **[Changelog & Release Notes](CHANGELOG.md)**: Semantic versioning history and release notes.

---

## Engineering Quality & Verification Pipeline

SQLion strictly adheres to a **3-Stage Verification Pipeline** for every change:

1. **Compilation (Build)**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench
   ```
2. **Deterministic Race-Free Verification (Test)**:
   ```bash
   go test -tags wiredtiger -count=1 -v -race ./...
   ```
   Requires 100% PASS across all packages with **0 data races**.
3. **Commit Integrity**:
   - Conventional Commits format (`feat`, `fix`, `docs`, `refactor`, `test`).
   - Zero decorative emojis in code, logs, comments, and commit messages.

---

## License

SQLion is free open-source software licensed under the **GNU General Public License v2 or v3** (GPL-2.0 / GPL-3.0). See [LICENSE](LICENSE) for details.
