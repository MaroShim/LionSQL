# LionSQL

High-Concurrency Embedded OLTP Database Engine & Multi-Protocol Stored Procedure Server

[![Version](https://img.shields.io/badge/version-v0.90.0--feature--freeze-blue.svg)](CHANGELOG.md)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-green.svg)](LICENSE)
[![Go Report](https://img.shields.io/badge/go-1.22+-00ADD8.svg)](https://go.dev)

(한국어 문서는 [README.ko.md](README.ko.md)를 참조하세요.)

---

## Overview

LionSQL is a high-performance, embedded OLTP database engine that natively compiles and executes Microsoft SQL Server T-SQL stored procedures and complex business logic on a sub-microsecond VDBE bytecode virtual machine, while simultaneously offering broad compatibility with PostgreSQL SQL dialects and the standard PGWire v3 protocol. It comes fully equipped with a built-in web management studio for real-time telemetry and schema administration, as well as an integrated goroutine connection pooler capable of handling tens of thousands of concurrent connections—eliminating the need for additional software to achieve a complete operational environment. Packaging this complete enterprise feature set into a single, ultra-compact static binary under 100MB (currently ~50MB) with zero external dependencies, LionSQL delivers blazing-fast transaction throughput of tens of thousands of transactions per second with predictable sub-millisecond latency and non-blocking snapshot isolation.


```
                    +------------------------------------------+
                    |                 LionSQL                  |
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

LionSQL's core capabilities are organized across five foundational architectural pillars:

### 1. Core Engine & Virtual Machine Architecture
- **Unified VDBE Virtual Machine**:
  Both interactive queries (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) and stored procedures execute on the exact same register-based VDBE virtual machine, complete with bytecode disassembly support (`EXPLAIN BYTECODE`).
- **Prepared Statement Bytecode Caching**:
  Parameterized queries are compiled directly into bytecode during preparation and cached, completely eliminating query re-parsing overhead on subsequent executions.
- **Dual-Parser Architecture with Automatic Fallback**:
  - **Ultra Fast-Path**: In-house zero-allocation Pratt parser delivering sub-microsecond parsing for high-frequency queries.
  - **Full-Spec Fallback**: Official Microsoft T-SQL parser invoked automatically on advanced syntax (bracket escapes, vendor options) to ensure 100% grammar compatibility.
- **Volcano Relational Pipeline & Memory Governor**:
  Composable iterator execution (Hash Join, Index Scan, GroupBy) with streaming batches, featuring an automatic disk spill to external merge sort when query memory limits are exceeded.
- **Cost-Based Optimizer (CBO)**:
  Leverages table statistics (cardinality, distinct counts, histograms) and multi-column extended statistics to optimize join orders and generate efficient execution plans.

### 2. High-Performance Storage & ACID Concurrency
- **Dual Storage Backends**:
  - **In-Memory OCC Engine**: Delivers 20,000+ TPS based on concurrent SkipList indexes and Optimistic Concurrency Control.
  - **WiredTiger MVCC Disk Engine**: Delivers 16,000+ TPS with full ACID durability backed by lock-free B+Trees.
- **Bloat-Free MVCC without Vacuum**:
  Multi-version update chains are maintained in memory and only latest committed versions are written to disk, completely eliminating table bloat and Autovacuum I/O storms.
- **Non-Blocking Snapshot Isolation**:
  Readers never block writers and writers never block readers, ensuring long-running analytical queries or backups never stall online transaction processing.
- **Non-Blocking Online Snapshot Hot Backup**:
  Captures live, compressed `.tar.gz` database backups without interrupting active transaction traffic. Supported seamlessly via CLI, REST API, and Web Studio.
- **High-Availability Replication**:
  Supports real-time WAL stream replication from Primary to Replicas with zero-downtime failover.

### 3. Dual Wire Protocols & Connection Pooling
- **Concurrent Dual Wire Protocols**:
  - **PostgreSQL v3 Protocol (Port 5433)**: Fully compatible with `psql`, DBeaver, GORM, Prisma, and pgx, supporting SSL/TLS encryption and binary parameter binding.
  - **Microsoft SQL Server TDS Protocol (Port 1433)**: Instantly compatible with SSMS, Azure Data Studio, `sqlcmd`, and MSSQL drivers, supporting RPC prepared execution.
- **Built-in Goroutine Connection Pooler**:
  Handles 10,000+ simultaneous connections over lightweight Go goroutines, eliminating connection bottlenecks without external poolers.

### 4. Enterprise T-SQL Procedural Runtime
- **Stored Procedures, Functions & Triggers**:
  Full support for `CREATE PROCEDURE`, scalar and Table-Valued Functions (TVF), and `AFTER` / `INSTEAD OF` triggers.
- **Control Flow & Variables**:
  Supports conditional branching (`IF...ELSE`), loops (`WHILE`), exception handling (`TRY...CATCH`), local variables (`@var`), table variables, session variables, and cursors.
- **Rich SQL Extensions**:
  Window functions (`ROW_NUMBER`, `RANK`), JSON extraction functions, foreign keys with cascading actions, `TRUNCATE TABLE`, and `SELECT ... INTO`.

### 5. Web Management Studio & Developer Experience
- **Single Standalone Executable**:
  Database kernel, wire protocol listeners, REST API, and web studio packaged into a single static executable with zero external runtime dependencies.
- **Embedded Web Management Studio (Port 8090)**:
  Zero-install browser dashboard (`/_/`) featuring visual schema explorer, inline data grid editor, SQL query console, real-time event stream viewer, and request activity logs.
- **Auto-Generated RESTful CRUD API**:
  Instant REST endpoints generated automatically for all tables upon creation, with boolean filtering, sorting, and pagination.
- **JavaScript & TypeScript Client SDK**:
  Lightweight client SDK served directly by the standalone executable, supporting Browsers, Node.js, Bun, and Deno.
- **Row-Level Security (RLS) & Automation**:
  Authentication-based data access control (RLS), asynchronous webhooks, 5-field cron automation scheduler, and built-in SMTP mailer.

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
# Build production single binary with WiredTiger support
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql

# Build pure-Go single binary (Zero Cgo dependency, runs everywhere)
go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
```

### 2. Starting the Server

```bash
# Start LionSQL in foreground (default, creates 'data' directory)
./bin/lionsql

# Or start with pure-Go in-memory engine (zero Cgo required)
./bin/lionsql -engine=memory

# Or start as a background daemon with live-logging
./bin/lionsql start -dev

# Inspect status, restart, or stop daemon
./bin/lionsql status
./bin/lionsql restart -dev
./bin/lionsql stop
```

Upon startup, the console displays the connection endpoints:

```
------------------------------------------------------------
 LionSQL - High-Concurrency OLTP & Stored Procedure Server
------------------------------------------------------------
 [Web Dashboard]  http://127.0.0.1:8090/_/
 [HTTP API]       http://127.0.0.1:8090/api/health
 [Superuser Auth] admin@lionsql.local (JWT Auth: true)
 [Postgres Wire]  psql -h 127.0.0.1 -p 5433 -d postgres (Beekeeper / DBeaver / Navicat / psql)
 [TDS / MSSQL]    sqlcmd -S 127.0.0.1,1433 -d master (Beekeeper / DBeaver / Navicat / SSMS / Azure Data Studio)
 [Data Directory] data
 [Storage Engine] wiredtiger
 [Session Pooler] Max Workers: 32, Max Idle Sessions: 64 (No PgBouncer required)
 [Memory Governor] max_memory: Auto (70% of RAM), work_mem: 64MB (Spill-to-disk enabled)
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
psql -h 127.0.0.1 -p 5433 -U admin -d postgres
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
sqlcmd -S 127.0.0.1,1433 -U admin -d master
```

```sql
-- Execute stored procedure
EXEC TransferFunds @from = 1, @to = 2, @amount = 100;
GO
```

### Bulk Copy Streaming with `psql \copy`

```bash
-- High-speed data export to CSV
psql -h 127.0.0.1 -p 5433 -d postgres -c "\copy accounts to 'accounts.csv' with (format csv, header)"

-- High-speed data import from CSV
psql -h 127.0.0.1 -p 5433 -d postgres -c "\copy accounts from 'accounts.csv' with (format csv, header)"
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
	dsn := "postgres://admin:1234567890@127.0.0.1:5433/postgres?sslmode=disable"
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
import LionSQL from './sdk/sqlion.js';

const client = new LionSQL('http://127.0.0.1:8090', { auth: 'YOUR_API_KEY' });

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
    dbname="lionsql"
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
  database: 'lionsql',
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
| `-ephemeral` | `false` | Disable WAL/AOF persistence for in-memory engine (pure RAM) |
| `-replica-of` | `""` | Primary server URL to stream WAL from (runs as read-only replica) |
| `-node-id` | `"node-1"` | Unique cluster node identifier for replication tracking |
| `-work-mem` | `64MB` | Default memory limit per query operator before spill-to-disk |
| `-max-workers` | `NumCPU * 4` | Maximum concurrent query execution workers |
| `-max-idle-sessions` | `64` | Maximum idle storage sessions to retain in pool |
| `-auth` | `false` | Enforce JWT authentication on protected REST endpoints |
| `-seed` | `true` | Seed sample accounts and `TransferFunds` procedure on startup |

### Unified CLI & Daemon Controls

LionSQL provides a modern, unified CLI with first-class lifecycle commands:

```bash
# Foreground execution (default)
./bin/lionsql                   # Start immediately using ./data
./bin/lionsql serve -dir=./data # Explicit serve subcommand

# Background daemon management
./bin/lionsql start -dev        # Start daemon in background (creates lionsql.pid & lionsql.log)
./bin/lionsql status            # Inspect daemon status and PID
./bin/lionsql restart -dev      # Graceful restart daemon
./bin/lionsql stop              # Graceful stop daemon

# Operational subcommands
./bin/lionsql version           # Display engine version and build tags
./bin/lionsql doctor            # Verify environment, ports, and permissions
./bin/lionsql init-config       # Generate default lionsql.json configuration file
./bin/lionsql sql "SELECT 1"    # Quick interactive query execution
```

### Declarative Schema Migrations CLI (`lionsql migrate`)

```bash
# Create a new timestamped migration pair (.up.sql / .down.sql)
./bin/lionsql migrate new create_users_table

# Apply all pending migrations atomically
./bin/lionsql migrate up -data=./data

# Roll back the most recent migration step
./bin/lionsql migrate down -steps=1 -data=./data

# View migration deployment status
./bin/lionsql migrate status -data=./data
```

### Backup & Restore Operations

```bash
# Offline or CLI-driven snapshot hot backup to target archive
./bin/lionsql backup -dir=data -target=/backups/lionsql-snapshot.tar.gz

# Restore database from archive
./bin/lionsql restore -dir=data -source=/backups/lionsql-snapshot.tar.gz

# Online live backup download via curl while server is running
curl -OJ http://127.0.0.1:8090/api/backup

# Online restore via HTTP POST multipart upload
curl -X POST -F "backup=@/backups/lionsql-snapshot.tar.gz" http://127.0.0.1:8090/api/restore
```

---

## HTTP REST API Reference

LionSQL exposes a complete PocketBase-style REST API on `:8090`:

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
| `GET` | `/api/replication/status` | Get node replication role, LSN, lag, and replica list |
| `GET` | `/api/replication/stream` | Stream chunked WAL frames to connected replicas |
| `POST` | `/api/replication/ack` | Report follower heartbeat and replayed LSN |
| `POST` | `/api/replication/promote` | Promote replica to active primary node |
| `POST` | `/api/replication/demote` | Demote primary node to follower under a new primary |

---

## Documentation Suite

LionSQL provides comprehensive documentation covering system architecture, operational runbooks, internal engine specifications, and API guides:

### [Master Technical Documentation Directory (doc/README.md)](doc/README.md)
Start here for a complete subject-based directory of all architectural blueprints, internals, and guides.

### Backend-as-a-Service (BaaS) & Developer APIs
- **[Official Web SDK & HTML Embedding Guide](doc/WEB_SDK.md)**: Universal zero-dependency client library and HTML embedding patterns for Web, Node.js, Bun, and Deno.
- **[Auto-Generated RESTful CRUD API](doc/AUTO_REST_API.md)**: Zero-code `/api/records/:table` endpoints with rich boolean filter expressions.
- **[Webhooks & Row-Level Security (RLS)](doc/WEBHOOKS_AND_RLS.md)**: Non-blocking mutation webhooks with HMAC signatures and declarative `@auth` access rules.
- **[Cron Jobs Scheduler & SMTP Mailer](doc/CRON_AND_MAILER.md)**: Embedded 5-field cron automation and transactional HTML email dispatching.
- **[HTTP REST API & Realtime SSE Specification](doc/API_REFERENCE.md)**: OpenAPI-style endpoint documentation and JavaScript EventSource examples.

### Core Architecture & Internals Specifications
- **[System Architecture Specification](doc/ARCHITECTURE.md)**: Exhaustive technical specification covering Volcano iterators, dual-parser decoupling, and storage backends.
- **[Architecture Decision Records (ADRs)](doc/ADR.md)**: Historical log of 18 foundational architectural decisions (ADR-001 through ADR-018).
- **[VDBE Bytecode Virtual Machine Internals](doc/INTERNALS_VDBE.md)**: Register allocation, opcode instruction set architecture (ISA), and procedural execution.
- **[Storage Engine Mechanics (WiredTiger & Memory OCC)](doc/INTERNALS_STORAGE.md)**: Lock-free B+Trees, hazard pointers, OCC validation, and AOF log compaction.
- **[Cost-Based Optimizer & Volcano Pipeline Internals](doc/INTERNALS_CBO_VOLCANO.md)**: Equi-depth histograms, physical cost formulas, and external merge sort.
- **[Multi-Wire Protocol Internals](doc/INTERNALS_WIRE_PROTOCOLS.md)**: Binary framing for PostgreSQL v3, TDS 7.x/8.0, and goroutine connection pooler.
- **[Virtual System Catalog & Data Dictionary](doc/DATA_DICTIONARY.md)**: System views (`sys.*`, `information_schema.*`), data types, and tool compatibility.
- **[High Availability & Disaster Recovery](doc/HIGH_AVAILABILITY.md)**: Multi-node replication topology, 1-writer multi-reader WAL streaming, failover, and recovery SLAs.
- **[Extension & Driver Development Guide](doc/EXTENSIONS.md)**: Dual-track UDFs (Go native SPI + Wasm sandbox), pluggable storage engines, and fallback parsers.

### User Guides & Operations
- **[T-SQL & SQL Syntax Reference Guide](doc/TSQL_REFERENCE.md)**: Complete language reference for procedures, triggers, UDFs, TVFs, and queries.
- **[Percentile & Quantile Analytics](doc/PERCENTILE_AND_QUANTILE_ANALYTICS.md)**: Continuous/discrete quantiles (`PERCENTILE_CONT`, `PERCENTILE_DISC`), `MEDIAN`, and streaming `APPROX_PERCENTILE` (T-Digest).
- **[In-Engine Text & Log Analytics](doc/LOG_PROCESSING_GREP_AWK.md)**: Zero-copy `GREP`, `REGEXP_LIKE`, and `AWK` / `SPLIT_PART` filter and extraction operators.
- **[Observability, Metrics & Telemetry Guide](doc/METRICS.md)**: Prometheus time-series metrics, health probes, alerting rules, and Grafana integration.
- **[Production Deployment & Operations Runbook](doc/OPERATIONS.md)**: Systemd services, Docker deployment, 1-writer replication failover, and security hardening.
- **[Performance Tuning & Benchmark Guide](doc/BENCHMARK.md)**: High-concurrency banking benchmark harness, TPS measurement, and memory tuning.
- **[Database Migration Guide](doc/MIGRATION.md)**: Procedures for migrating from PostgreSQL, MSSQL, SQLite, and MySQL.
- **[Security Policy & Threat Model](doc/SECURITY.md)**: RBAC access control, SQL injection immunity, and vulnerability disclosure.
- **[Engineering Roadmap & Milestones](ROADMAP.md)**: Versioned engineering milestones from single-node OLTP to Always On HA and decoupled HTAP.
- **[Actionable Engineering Backlog (TODO.ko.md)](TODO.ko.md)**: Immediate, daily task backlog and prioritized operational execution tracker.
- **[Developer & AI Handover Context Notes](notes/README.md)**: Shared brain, engineering invariants, why alternatives were spec'd out, fragile landmines, and upcoming refactoring context.
- **[Troubleshooting, Error Diagnostics & FAQ](doc/TROUBLESHOOTING.md)**: Port conflicts, serialization conflicts, slow query tuning, and client driver setup.
- **[Contributing & Development Guide](CONTRIBUTING.md)**: Engineering standards, 3-stage verification pipeline, and pull request checklist.
- **[Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md)**: Standards, pledge, enforcement responsibilities, and guidelines.
- **[Changelog & Release Notes](CHANGELOG.md)**: Semantic versioning history and release notes.

---

## Engineering Quality & Verification Pipeline

LionSQL strictly adheres to a **3-Stage Verification Pipeline** for every change:

1. **Compilation (Build)**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
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

### Open Source License
LionSQL is free open-source software licensed under the **GNU General Public License v3 (GPL-3.0-or-later)**. See [LICENSE](LICENSE) for the full canonical license text.

### Third-Party Software & Copyright Attribution
LionSQL incorporates or dynamically links with third-party software components under compatible open-source licenses:
- **WiredTiger Storage Engine**:
  - Copyright (c) MongoDB, Inc.
  - Copyright (c) Keith Bostic and Michael Cahill.
  - Licensed under the **GNU General Public License (GPLv2 or GPLv3)**.
  - When LionSQL is built with `-tags wiredtiger`, it links with `libwiredtiger` governed by its respective GPL terms.

### Commercial Dual Licensing & Enterprise Embeddings
For commercial software development, proprietary in-engine embedding, or enterprise deployments that require custom licensing terms without GPL copyleft obligations:
- Commercial dual-licensing agreements covering LionSQL's proprietary kernel code are available upon request.
- Deployments using LionSQL's pure-Go in-memory engine (`-engine=memory`) operate free of Cgo and third-party C library copyleft requirements. Commercial embedding with the WiredTiger storage backend requires a separate commercial WiredTiger license from MongoDB, Inc. or adherence to GPL.

