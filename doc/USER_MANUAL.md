# LionSQL User Manual

> - **Created**: 2026-10-08
> - **Last Updated**: 2026-11-03


This document is the **official User Manual** for the LionSQL database engine, providing step-by-step guidance from initial installation and startup, through client connections and application development, to production operations and administration.

---

## Table of Contents

### Chapter 1. Introduction
* **1.1. Overview and Core Features**
  * Single static binary (Zero-External-Dependency) self-contained architecture
  * High-concurrency embedded OLTP engine with built-in Backend-as-a-Service (BaaS)
  * Simultaneous multi-wire protocol support (PostgreSQL v3, MS SQL Server TDS 7.x/8.0, HTTP/REST)
* **1.2. System Architecture Overview**
  * Dual storage engines: Pure Go OCC In-Memory Engine vs. WiredTiger B+Tree MVCC Disk Engine
  * SQLite-style T-SQL VDBE bytecode virtual machine and dual-parser strategy
  * Volcano relational query execution engine and Cost-Based Optimizer (CBO)
* **1.3. System Requirements and Platform Support**
  * Supported operating systems: macOS (Apple Silicon / Intel), Linux (x86_64 / arm64), Windows
  * Build and runtime prerequisites (Go 1.22+, optional WiredTiger Cgo library)

---

### Chapter 2. Installation & Getting Started
* **2.1. Pre-built Binary Installation**
  * Platform release archive download and extraction
  * Granting execute permissions and system path (`$PATH`) configuration
* **2.2. Building from Source**
  * Building pure Go in-memory binary (`go build ./cmd/lionsql`)
  * Building with WiredTiger MVCC disk engine (`go build -tags wiredtiger ./cmd/lionsql`)
* **2.3. First Server Launch and Status Verification**
  * Foreground interactive launch (`./bin/lionsql` or `./bin/lionsql serve`)
  * Startup banner verification and health check endpoint validation (`http://127.0.0.1:8090/api/health`)
* **2.4. Quick Tour Step-by-Step**
  * Default administrator login and Web Studio access (`http://127.0.0.1:8090/_/`)
  * Creating the first table, inserting data, and executing SQL queries
  * Defining T-SQL stored procedures and immediate execution testing

---

### Chapter 3. Configuration & Operation
* **3.1. Configuration Precedence Hierarchy**
  * CLI Flags > Environment Variables (`LION_*`) > JSON Config File (`lionsql.json`) > Built-in Defaults
* **3.2. Configuration File (`lionsql.json`) Setup and Template Generation**
  * Generating default configuration template (`./bin/lionsql init-config`)
  * Guide to `server`, `network`, `auth`, `resources`, `logging`, and `replication` sections
* **3.3. Network Listeners and Port Management**
  * Web Management Studio & REST API (default port `:8090`)
  * PostgreSQL Wire Protocol (default port `:5433`, default DB `postgres`)
  * MS SQL Server TDS Protocol (default port `:1433`, default DB `master`)
  * Port collision avoidance and protocol opt-out (`-pg=""`, `-tds=""`)
* **3.4. Single Binary Lifecycle Management**
  * Background daemon startup (`./bin/lionsql start [-dev]`)
  * Daemon status inspection (`./bin/lionsql status`)
  * Zero-downtime graceful restart (`./bin/lionsql restart [-dev]`)
  * Clean process termination (`./bin/lionsql stop`)
  * Data integrity and system diagnostics (`./bin/lionsql doctor`)
* **3.5. Storage Engine Selection and Data Directory**
  * Persistent disk MVCC storage (`--engine=wiredtiger`, `--dir=data`)
  * High-speed in-memory OCC storage (`--engine=memory`, `--ephemeral`)
  * WAL/AOF durability and automatic snapshot compaction intervals
* **3.6. Global Memory Governor and Resource Tuning**
  * Dynamic sizing at 65% of host physical RAM and runtime upper bound (`--max-memory`)
  * Large hash join Fail-Fast circuit breaker mechanics
  * Operator work memory allocation (`--work-mem=64MB`)
  * Goroutine connection pooler: max workers and idle session limits
* **3.7. Logging and Diagnostic Modes**
  * Developer real-time tracing mode (`--dev`)
  * Slow query log threshold configuration (default: `100.0ms`)
  * General query audit logging and file rotation

---

### Chapter 4. Client Tool Integration
* **4.1. PostgreSQL Standard Client Connection**
  * CLI tool: `psql -h 127.0.0.1 -p 5433 -U admin dbname=postgres`
  * Connection parameters and SCRAM-SHA-256 authentication profile
* **4.2. MS SQL Server Standard Client Connection**
  * CLI tool: `sqlcmd -S 127.0.0.1,1433 -U sa -P 1234567890 -d master`
  * TDS 7.x/8.0 packet negotiation and virtual catalog (`dbo`, `sys.*`) integration rules
* **4.3. Verified Graphical (GUI) Database Tools**
  * Beekeeper Studio (PostgreSQL / SQL Server driver compatibility)
  * DBeaver Universal Database Tool (Metadata tree browsing guide)
  * Navicat Premium / Navicat for PostgreSQL
  * Azure Data Studio & SQL Server Management Studio (SSMS)
* **4.4. Built-in Web Studio Full Utilization**
  * Browser-based GUI dashboard (`http://127.0.0.1:8090/_/`)
  * Real-time SSE system monitoring stream (`/api/realtime`)
  * Real-time HTTP and SQL request audit log viewer (`/api/logs/requests`)
  * Non-destructive live column addition DDL modal (`+ Add Column`)
  * Table data viewer, cell inline editing, multi-row bulk export/import (CSV/JSON), FK jump links (`↗`)
  * SQL Query Studio: query execution, `EXPLAIN` execution plan tree visualization
  * Developer API Preview modal (dynamic schema payload and official SDK code tabs)
  * Light/dark theme toggle and high-contrast WCAG visibility mode
* **4.5. Official Driver Integration Recipes by Programming Language**
  * Go: `database/sql` (`lib/pq`, `pgx/v5`, `go-mssqldb`)
  * Python: `psycopg2`, `psycopg3`, `pymssql`, SQLAlchemy
  * Node.js / TypeScript: `pg`, `tedious`, TypeORM, Prisma
  * Java / Kotlin: PostgreSQL JDBC, Microsoft JDBC Driver for SQL Server
  * .NET / C#: `Npgsql`, `Microsoft.Data.SqlClient`

---

### Chapter 5. Database Development Guide
* **5.1. Schema Design and Data Types**
  * Table creation (`CREATE TABLE`) and column constraints (`NOT NULL`, `DEFAULT`)
  * Primary keys and auto-increment identity columns (`INT IDENTITY(1,1)`)
  * Foreign keys and referential integrity constraints (`ON DELETE CASCADE / RESTRICT`)
  * Time-series auto-rolling range partitioning (`PARTITION BY`, ClickHouse-style)
* **5.2. Index Design and Query Optimization Strategies**
  * Secondary indexes (`CREATE INDEX`) and composite indexes
  * Unique indexes (`CREATE UNIQUE INDEX`) for data integrity enforcement
  * Covering indexes (`INCLUDE (...)`) eliminating main table heap lookups
  * Distinguishing IndexScan vs. SeqScan in execution plans
* **5.3. Transactions and Concurrency Control (ACID & Concurrency)**
  * Basic transaction control: `BEGIN TRANSACTION`, `COMMIT`, `ROLLBACK`
  * Snapshot Isolation and lock-free reads
  * Optimistic Concurrency Control (OCC) and serialization conflict (`40001`) retry patterns
* **5.4. Relational Querying and Analytical Window Functions**
  * Data filtering, sorting, and grouping (`WHERE`, `ORDER BY`, `GROUP BY`, `HAVING`)
  * Multi-table joins (`INNER JOIN`, `LEFT JOIN`, `CROSS JOIN`, `CROSS APPLY`)
  * Standard pagination (`OFFSET ... FETCH NEXT ... ROWS ONLY` and `LIMIT / OFFSET`)
  * Common Table Expressions (`WITH CTE AS (...)`) and correlated subqueries
* **5.5. T-SQL Stored Procedures and Business Logic**
  * Procedure definition (`CREATE PROCEDURE`) and input/output parameters (`@param INT OUTPUT`)
  * Control flow structures (`IF...ELSE`, `WHILE`, `RETURN`)
  * Exception handling blocks (`BEGIN TRY...BEGIN CATCH`), `RAISERROR`, `THROW`
  * Cursor lifecycle (`DECLARE`, `OPEN`, `FETCH`, `CLOSE`, `DEALLOCATE`)
* **5.6. Declarative Schema Migration Tool (`lionsql migrate`)**
  * Migration file template generation (`./bin/lionsql migrate new <name>`)
  * Applying and rolling back versions (`./bin/lionsql migrate up`, `down`, `status`)
  * Atomic transactional DDL safety and migration locking mechanism
* **5.7. Utilizing Web Backend-as-a-Service (BaaS) Capabilities**
  * Zero-config automatic RESTful CRUD endpoints (`/api/tables`, `/api/records/:table`)
  * Admin authentication, JWT token issuance, and Row-Level Security (RLS) rules
  * Real-time Server-Sent Events (SSE) change detection pipeline

---

### Chapter 6. Operations & Administration
* **6.1. Query Performance Analysis and Cost-Based Optimizer (CBO)**
  * Execution plan analysis: interpreting `EXPLAIN` and profiling `EXPLAIN ANALYZE` results
  * Cost-based optimizer statistics collection: `ANALYZE TABLE <name>`
  * Index hints (`WITH (INDEX(...))`) and query planner steering
* **6.2. Online Hot Backup and Disaster Recovery (Backup & Restore)**
  * Single binary zero-downtime online backup (`./bin/lionsql backup -out=backup.tar.gz`)
  * REST API-based remote backup download (`POST /api/backup`)
  * Full database restoration (`./bin/lionsql restore -in=backup.tar.gz`)
* **6.3. High Availability (HA) and Read-Replica Scale-Out (Replication)**
  * 1-Writer Multi-Reader async/semi-sync WAL replication topology configuration
  * Primary node setup and Replica node LSN streaming connection (`-replica-of=...`)
  * Leader heartbeat timeout detection and automatic failover
  * Manual promotion command (`POST /api/replication/promote`)
* **6.4. Enterprise Security and Network Encryption**
  * TLS/SSL certificate and private key configuration (`--tls`, `--tls-cert`, `--tls-key`)
  * Blocking unencrypted connections and enforcing SSL (`--require-ssl`)
  * Process isolation principles and single-catalog security boundary
* **6.5. Fault Diagnostics and Troubleshooting**
  * Port conflicts (`bind: address already in use`) and zombie process cleanup
  * Running system diagnostic tools (`./bin/lionsql doctor`)
  * Diagnosing transaction lock contention and memory governor circuit breaker errors

---

## Chapter 1. Introduction

### 1.1. Overview and Core Features

LionSQL is an **enterprise-grade high-concurrency embedded OLTP database engine** running as a single standalone executable without external dependencies.

Traditional relational database systems (PostgreSQL, MS SQL Server, Oracle) are powerful, but they require heavy daemon processes, intricate installation scripts, large resident memory footprints, and mandatory external sidecars such as connection poolers (PgBouncer), caching tiers (Redis), or reverse proxies (Nginx). Conversely, lightweight embedded engines (SQLite, DuckDB) offer extreme simplicity in installation and deployment, but suffer from fundamental constraints: concurrent write locks, absence of multi-client network listeners, and lack of enterprise stored procedures (T-SQL).

LionSQL unites the best of both paradigms within a single binary, delivering **the extreme deployment simplicity of SQLite**, **the rich business logic expressiveness of MS SQL Server (T-SQL)**, and **the expansive ecosystem compatibility of PostgreSQL (PGWire v3)** simultaneously:

1. **Single Static Binary (Zero-External-Dependency Architecture)**:
   - Even when built with CGO, static library linking ensures immediate startup without external shared libraries (`*.so`, `*.dylib`) or runtime interpreters (Node, Python, Java).
   - A single executable (`lionsql`) encapsulates the DB kernel, three network listeners (PGWire, TDS, HTTP), an embedded web administration console (Web Studio), backup/restore engines, and a cron scheduler.
2. **Simultaneous Multi-Protocol Tri-Bridge**:
   - **PostgreSQL Wire Protocol v3 (`TCP :5433`)**: 100% compatible with standard `psql`, DBeaver, GORM, Prisma, `lib/pq`, `pgx`, and the extensive PostgreSQL client tool and driver ecosystem (default catalog: `postgres`).
   - **Microsoft SQL Server TDS 7.x/8.0 (`TCP :1433`)**: Direct integration with standard MSSQL tools such as `sqlcmd`, Azure Data Studio, SSMS, and Beekeeper Studio (default catalog: `master`).
   - **HTTP REST & Server-Sent Events (`TCP :8090`)**: Provides zero-config automated CRUD REST APIs, real-time SSE streams, and a browser-based GUI management studio concurrently.
   > [!TIP]
   > **Recommended Application Protocol: PGWire (`:5433`)**
   > Even in environments that extensively utilize T-SQL stored procedures (`EXEC ...`), **using the PGWire (:5433) protocol is strongly recommended** for backend applications and microservices integration. Compared to TDS, it features lower packet overhead and supports pipelining, yielding higher measured network throughput (141k tpmC / 5,177 TPS), alongside the most mature driver support across every language ecosystem (Go, Python, Node, Java). TDS (:1433) is optimally reserved for legacy Microsoft management tools such as SSMS and Azure Data Studio.
3. **High-Concurrency Dual Storage Engines**:
   - Seamlessly switch between a persistence-oriented disk B+Tree MVCC storage engine (WiredTiger) and an in-memory OCC storage engine (Pure-Go SkipList) delivering microsecond responses via a single configuration flag.
4. **Sub-Microsecond T-SQL VDBE Bytecode Virtual Machine**:
   - Pre-compiles T-SQL stored procedures and complex queries to execute on a register-based VDBE virtual machine in sub-microsecond intervals.
5. **Global Memory Governor and Circuit Breaker**:
   - Automatically sizes the memory upper bound to 65% of host physical RAM (with a 1GB OS reserve and a 50% lower-bound guard) and triggers a fail-fast circuit breaker within 75µs upon encountering massive hash joins or cartesian products, permanently protecting the process from the OS OOM Killer.

---

### 1.2. System Architecture Overview

LionSQL implements a strictly decoupled multi-tiered pipeline architecture:

```
               [ Client Applications & Tools ]
    (DBeaver, psql, sqlcmd, SSMS, Web Browsers, REST API)
                             │
     ┌───────────────────────┼───────────────────────┐
     ▼                       ▼                       ▼
[ PGWire v3 ]           [ TDS 7.x/8.0 ]        [ HTTP / REST ]
 (Port 5433)             (Port 1433)            (Port 8090)
     │                       │                       │
     └───────────────────────┼───────────────────────┘
                             │
                             ▼
              [ Core Session Coordinator ]
            - Protocol Handshake & Auth
            - Virtual Single Catalog Interceptor
            - Goroutine Connection Pooler
                             │
              ┌──────────────┴──────────────┐
              ▼                             ▼
    [ Primary Pratt Parser ]       [ ANTLR4 Fallback Parser ]
    (Sub-microsecond Fast-Path)    (Full-Spec Legacy T-SQL)
              │                             │
              └──────────────┬──────────────┘
                             ▼
             [ Cost-Based Optimizer (CBO) ]
           - 64-Bucket Equi-Height Histograms
           - Selinger DP Multi-Join Reordering
           - Covering Index & Predicate Pushdown
                             │
                             ▼
              [ Execution Pipeline Engine ]
    ┌────────────────────────┴────────────────────────┐
    ▼                                                 ▼
[ T-SQL VDBE VM ]                           [ Volcano Iterator ]
- Stored Procedures                         - Hash / Merge Join
- Variables, Loops, Flow                    - Vectorized Chunks
- Register sync.Pool                        - Memory Circuit Breaker
    │                                                 │
    └────────────────────────┬────────────────────────┘
                             ▼
               [ Pluggable Storage SPI ]
              (storage.Engine / Tx / Cursor)
              ┌──────────────┴──────────────┐
              ▼                             ▼
    [ In-Memory OCC Engine ]      [ WiredTiger MVCC Engine ]
    - Concurrent SkipList         - Lock-Free Hazard Pointers
    - Striped Table Locks         - Durable Write-Ahead Log
    - Redis-Style AOF/RDB         - Zero-Vacuum B+Tree
```

* **Dual-Parser Strategy**: High-frequency standard queries are processed in nanoseconds by the native Pratt parser, while complex vendor-specific syntaxes (`WITH RECOMPILE`, compound schema expressions) transparently fall back to the official T-SQL ANTLR4 parser.
* **Cost-Based Optimizer (CBO)**: Combines 64-bucket equi-height histograms and Most Common Values (MCV) statistics to optimize join orders and determine index scans.
* **Volcano Vectorized Executor**: Maximizes CPU cache locality via pooled 1,024-row chunk buffers.

---

### 1.3. System Requirements and Platform Support

Due to its lightweight embedded design, LionSQL operates smoothly even on minimal hardware specifications:

| Category | Minimum Requirements | Recommended Production Specifications |
| :--- | :--- | :--- |
| **CPU** | 1 Core (x86_64 or ARM64) | 4 Cores or higher (Apple Silicon M-series, modern Intel/AMD multi-core) |
| **RAM** | 512 MB (default work_mem 64MB) | 4 GB or higher (65% of physical memory automatically budgeted for global governor) |
| **Disk** | 100 MB free space (binary approx. 50MB) | NVMe SSD (ensures tens of thousands of WAL Group Commit I/O operations per second) |
| **OS** | macOS 12+ (Darwin ARM64 / x86_64)<br>Linux (Ubuntu 20.04+, RHEL 8+, Alpine/Debian)<br>Windows 10 / Server 2019+ | Linux LTS (Ubuntu 22.04 LTS / 24.04 LTS x86_64 or ARM64) |
| **Go Compiler** | Go 1.22 or higher (when building from source) | Go 1.24 or higher |

---

## Chapter 2. Installation & Getting Started

### 2.1. Pre-built Binary Installation

LionSQL is distributed as a self-contained single static binary requiring no external shared libraries or runtime installations. Download the release archive corresponding to your operating system and architecture for immediate use.

#### 1. Downloading and Installing Platform Archives
* **macOS (Apple Silicon M1/M2/M3/M4, ARM64)**:
  ```bash
  curl -LO https://github.com/your-org/lionsql/releases/download/v0.90.0-feature-freeze/lionsql-v0.90.0-darwin-arm64.tar.gz
  tar -xzf lionsql-v0.90.0-darwin-arm64.tar.gz
  chmod +x lionsql
  sudo mv lionsql /usr/local/bin/
  ```
* **Linux (Ubuntu / Debian / RHEL / Rocky, x86_64)**:
  ```bash
  curl -LO https://github.com/your-org/lionsql/releases/download/v0.90.0-feature-freeze/lionsql-v0.90.0-linux-amd64.tar.gz
  tar -xzf lionsql-v0.90.0-linux-amd64.tar.gz
  chmod +x lionsql
  sudo mv lionsql /usr/local/bin/
  ```
* **Linux (ARM64 / AWS Graviton)**:
  ```bash
  curl -LO https://github.com/your-org/lionsql/releases/download/v0.90.0-feature-freeze/lionsql-v0.90.0-linux-arm64.tar.gz
  tar -xzf lionsql-v0.90.0-linux-arm64.tar.gz
  chmod +x lionsql
  sudo mv lionsql /usr/local/bin/
  ```

#### 2. Binary Integrity and Version Check
After installation, verify proper operation by checking the version in your terminal:
```bash
$ lionsql version
LionSQL v0.90.0 (darwin/arm64) [tags: wiredtiger]
Git Commit: 477a4cb6
Go Version: go1.24.0
Build Date: 2024-10-19T08:30:00Z
Storage Backends: WiredTiger MVCC, In-Memory OCC
```

---

### 2.2. Building from Source

After cloning the LionSQL repository, compile it using standard Go toolchains:

```bash
# 1. Clone repository
git clone https://github.com/your-org/lionsql.git
cd lionsql

# 2. Compile static binary with persistent disk MVCC (WiredTiger) support (Recommended for Production)
go build -tags wiredtiger -buildvcs=false -ldflags="-s -w" -o bin/lionsql ./cmd/lionsql

# 3. Compile pure Go in-memory binary without CGO (Ultra-lightweight deployment)
go build -buildvcs=false -o bin/lionsql ./cmd/lionsql

# 4. Build benchmark and conformance verification harnesses
go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-kernel ./cmd/tpcc_kernel
```

---

### 2.3. First Server Launch and Status Verification

Start the server immediately using default flags without requiring a configuration file:

```bash
# Interactive foreground launch with default data directory (./data)
./bin/lionsql
```

The terminal outputs the startup banner:

```
================================================================================
  LionSQL High-Concurrency Embedded OLTP Database Server (v0.90.0)
================================================================================
 [Engine Core]    WiredTiger Lock-Free B+Tree MVCC Storage
 [Data Directory] ./data
 [Memory Budget]  2662MB (65% of 4096MB Physical RAM), work_mem: 64MB
 [Web Studio]     http://127.0.0.1:8090/_/ (HTTP REST Engine ready)
 [PostgreSQL]     postgres://admin@127.0.0.1:5433/postgres (PGWire v3)
 [SQL Server]     sqlcmd -S 127.0.0.1,1433 -U sa -d master (TDS 7.x/8.0)
--------------------------------------------------------------------------------
 [Notice] Default admin credentials initialized:
          Email:    admin@lionsql.local
          Password: (configured via -admin-pass or lionsql.json)
================================================================================
```

In a separate terminal window, immediately verify startup health via the health check endpoint:
```bash
$ curl -s http://127.0.0.1:8090/api/health
{"status":"ok","version":"0.90.0","engine":"wiredtiger","uptime_seconds":5}
```

---

### 2.4. Quick Tour Step-by-Step

#### Step 1: Accessing Built-in Web Studio and Dashboard
1. Open a web browser and navigate to `http://127.0.0.1:8090/_/`.
2. Sign in with the default administrator credentials:
   - **Email**: `admin@lionsql.local`
   - **Password**: `1234567890` (Recommended to change after initial launch)
3. Check the real-time SSE stream on the dashboard showing the QPS gauge, active worker count, and heap memory usage.

#### Step 2: Creating the First Relational Table (SQL Studio)
Navigate to **SQL Studio** in the left menu of Web Studio, or execute the following DDL statement from `psql` or `sqlcmd`:

```sql
-- Create customer accounts table
CREATE TABLE accounts (
    id INT IDENTITY(1,1) PRIMARY KEY,
    holder_name VARCHAR(100) NOT NULL,
    email VARCHAR(150) UNIQUE,
    balance DECIMAL(18,2) DEFAULT 0.00,
    created_at DATETIME DEFAULT GETDATE()
);

-- Create composite index for query optimization
CREATE INDEX idx_accounts_name_bal ON accounts (holder_name, balance);
```

#### Step 3: Inserting Initial Records and Querying
```sql
-- Insert 3 sample records
INSERT INTO accounts (holder_name, email, balance) VALUES 
('Alice Smith', 'alice@example.com', 150000.00),
('Bob Jones', 'bob@example.com', 250000.00),
('Charlie Brown', 'charlie@example.com', 50000.00);

-- Query high-balance accounts
SELECT id, holder_name, email, balance 
FROM accounts 
WHERE balance >= 100000.00
ORDER BY balance DESC;
```

#### Step 4: Non-Destructive Live Column Addition (`+ Add Column`)
LionSQL allows adding columns to active tables in real time without lock contention:
1. In the **Tables** tab in the left panel of Web Studio, click the `accounts` table.
2. Click the `+ Add Column` button in the top right of the table header.
3. Enter the column properties in the modal:
   - **Column Name**: `phone_number`
   - **Data Type**: `VARCHAR(30)`
   - **Default Value**: `'N/A'`
   - **Nullable**: `Yes`
4. Click **Save** to expand the schema in sub-milliseconds without server restarts or table rewrites.

#### Step 5: Defining a T-SQL Stored Procedure and Executing Atomic Transfer
Encapsulate business logic safely within a database kernel transaction:

```sql
CREATE PROCEDURE TransferMoney
    @from_id INT,
    @to_id INT,
    @amount DECIMAL(18,2)
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @current_bal DECIMAL(18,2);
    
    SELECT balance INTO @current_bal FROM accounts WHERE id = @from_id;
    
    IF @current_bal < @amount
    BEGIN
        RAISERROR('Insufficient funds to complete transfer.', 16, 1);
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from_id;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to_id;

    RETURN 0;
END;
```

Execute procedure and verify balance changes:
```sql
EXEC TransferMoney @from_id = 1, @to_id = 2, @amount = 50000.00;

SELECT id, holder_name, balance FROM accounts WHERE id IN (1, 2);
```

#### Step 6: Utilizing SDK Code from Developer API Preview Modal
1. Click the `API` button next to the `accounts` table in the Web Studio table list.
2. Dynamically generated code snippets in cURL, Vanilla JavaScript `fetch()`, and TypeScript Web SDK are displayed.
3. Click copy to paste directly into your frontend or backend applications.

---

## Chapter 3. Configuration & Operation

### 3.1. Configuration Precedence Hierarchy

LionSQL adheres to 12-Factor App principles and engineering guidelines, enforcing a strict, deterministic four-tier configuration hierarchy:

$$\text{CLI Flags} \;>\; \text{Environment Variables} \;>\; \text{Config File (lionsql.json)} \;>\; \text{Built-in Defaults}$$

* **CLI Flags**: Options explicitly supplied at execution take highest precedence (e.g. `-http=:9000`).
* **Environment Variables**: Environment variables prefixed with `LION_` take secondary precedence (e.g. `LION_HTTP_ADDR=:9000`).
* **Config File (`lionsql.json`)**: JSON configuration file specified in the data directory or via `-config`.
* **Built-in Defaults**: Safe hardcoded fallback defaults applied when unspecified (ports 8090/5433/1433, RAM 65% dynamic memory ceiling).

---

### 3.2. Configuration File (`lionsql.json`) Setup and Template Generation

Generate a default configuration file template with the following command:

```bash
lionsql init-config -out=lionsql.json
```

Complete structure of generated `lionsql.json`:

```json
{
  "server": {
    "engine": "wiredtiger",
    "data_dir": "data",
    "ephemeral": false,
    "auto_seed": false,
    "pid_file": "data/lionsql.pid",
    "graceful_timeout_seconds": 1.5
  },
  "network": {
    "http_addr": ":8090",
    "pg_addr": ":5433",
    "tds_addr": ":1433",
    "tls": {
      "enabled": false,
      "cert_file": "",
      "key_file": "",
      "require_ssl": false
    }
  },
  "auth": {
    "enabled": true,
    "admin_email": "admin@lionsql.local",
    "admin_pass": "",
    "jwt_secret": "",
    "jwt_expiry_hours": 72
  },
  "resources": {
    "max_memory": "",
    "work_mem": "64MB",
    "max_workers": 256,
    "max_idle_sessions": 64,
    "memory_pressure_threshold_pct": 85
  },
  "logging": {
    "dev_mode": false,
    "slow_query": {
      "enabled": true,
      "threshold_ms": 100.0,
      "file": "data/slow_queries.log"
    },
    "general_query": {
      "enabled": false,
      "file": "data/general_query.log"
    }
  },
  "replication": {
    "enabled": false,
    "role": "PRIMARY",
    "node_id": "node-1",
    "primary_url": "",
    "auto_failover": false,
    "peers": [],
    "heartbeat_interval_ms": 1000,
    "heartbeat_timeout_ms": 3000
  }
}
```

---

### 3.3. Network Listeners and Port Management

LionSQL manages three network listeners concurrently within a single process:
* **`:8090` (Web Studio & REST API)**: Admin web UI and auto-generated REST API serving.
* **`:5433` (PostgreSQL Wire Protocol)**: Defaults to `5433` to prevent collision with existing local PostgreSQL port 5432.
* **`:1433` (MS SQL Server TDS)**: Uses standard SQL Server port `1433`.

#### Protocol Opt-Out:
To expose only specific protocols per security policies, easily disable listeners by passing an empty string (`""`):

```bash
# Disable TDS (1433) and HTTP (8090), opening only PostgreSQL (5433) port
lionsql serve -tds="" -http="" -pg=":5433"
```

---

### 3.4. Single Binary Lifecycle Management (Lifecycle Commands)

`lionsql` provides built-in lifecycle management commands without requiring external daemonization tools (`systemd`, `supervisord`, `nohup`):

```bash
# 1. Start as background daemon (Automatic PID and log management)
$ lionsql start -dir data
LionSQL daemon started successfully. (PID: 99777)
Logs redirected to: data/lionsql.log
Web Studio URL: http://localhost:8090
PostgreSQL v3: localhost:5433
MS SQL Server TDS: localhost:1433

# 2. Inspect active daemon status
$ lionsql status -dir data
LionSQL Server Status: RUNNING
----------------------------------------
PID               : 99777
Data Directory    : ./data
Storage Backend   : wiredtiger (Disk B+Tree MVCC)
HTTP / Studio     : 127.0.0.1:8090 (HEALTHY)
PostgreSQL v3     : 127.0.0.1:5433 (LISTENING)
MS SQL Server TDS : 127.0.0.1:1433 (LISTENING)
Uptime            : 1h 24m 12s
Heap Allocated    : 142.5 MB / Max: 2867.0 MB (5.0%)
Active Workers    : 6 / 256

# 3. Graceful restart (Safely drains active sessions, then promotes new instance)
$ lionsql restart -dir data
Stopping LionSQL daemon (PID: 99777)... Done.
LionSQL daemon started successfully. (PID: 101234)

# 4. Clean daemon stop (Flushes pending transactions and exits cleanly within 1.5s)
$ lionsql stop -dir data
Stopping LionSQL daemon (PID: 101234)...
LionSQL daemon (PID: 101234) stopped cleanly.

# 5. Data integrity and system diagnostics
$ lionsql doctor -dir data
Checking LionSQL Environment & Storage Integrity...
[OK] Data directory permissions: rwxr-xr-x (data)
[OK] Free disk space: 42.8 GB available
[OK] TCP Port 8090 (HTTP/Studio): AVAILABLE
[OK] TCP Port 5433 (PGWire): AVAILABLE
[OK] TCP Port 1433 (TDS): AVAILABLE
[OK] Catalog schema integrity: PASSED
[OK] WAL LSN continuity check: PASSED
Overall Result: HEALTHY (0 errors, 0 warnings)

# 6. Runtime Configuration & Emergency Hatch Control (get & set)
$ lionsql get
KEY            VALUE   TYPE   DESCRIPTION
tolerant_ddl   false   bool   EMERGENCY USE ONLY: Volatile bootstrap hatch for irregular third-party DDLs (auto-resets to strict on reboot)

$ lionsql set tolerant_ddl on
[sudo] Password for admin@lionsql.local: ********
OK: tolerant_ddl set to true (in-memory volatile)
[EMERGENCY WARNING] Tolerant DDL bootstrap hatch is now ACTIVE!
                    Non-existent drops and duplicate creates will be silently bypassed.
                    This mode is for emergency third-party bootstrap ONLY.
                    Disable it immediately after migration via: lionsql set tolerant_ddl off
```

> [!IMPORTANT]
> **LionSQL Compatibility Philosophy & Emergency Hatch Policy**:
> 1. **Primary Standard Compatibility**: LionSQL prioritizes native PostgreSQL SQLSTATE error compliance (`42704`, `42P01`, etc.) so external ORMs and frameworks (Knex, GORM, Hibernate) handle schema idempotency cleanly via standard `TRY...CATCH` logic.
> 2. **Emergency Escape Hatch (Last Resort)**: For third-party open-source applications hardcoding irregular migrations (e.g. dropping non-existent indexes without checks) where source modification is impractical, `lionsql set tolerant_ddl on` serves strictly as a **temporary, volatile emergency escape hatch**. It must be disabled immediately post-bootstrap, and automatically resets to strict mode on server restart.

---

### 3.5. Storage Engine Selection and Practical Workload Recipes

For an in-depth analysis, refer to [`TRANSACTION_PERFORMANCE_GUIDE.md`](TRANSACTION_PERFORMANCE_GUIDE.md) (or the Korean counterpart [`TRANSACTION_PERFORMANCE_GUIDE.ko.md`](TRANSACTION_PERFORMANCE_GUIDE.ko.md)). The four most frequent production recipes are:

#### Recipe 1: Ultra-Fast Local Testing and CI Pipelines (In-Memory Ephemeral)
```bash
lionsql serve -engine memory -ephemeral -fsync off -max-memory 2GB
```
* Completely eliminates disk file I/O, executing unit tests in pure RAM at 25,000+ TPS.

#### Recipe 2: Standard Enterprise Production OLTP (Recommended Default)
```bash
lionsql start -dir /var/lib/lionsql/data -engine wiredtiger -fsync everysec -max-memory 4GB
```
* Leverages 1-second interval WAL flushes and WiredTiger MVCC B+Tree to balance 12,000~16,800+ TPS with 1-second RPO safety.

#### Recipe 3: Strict Zero-Loss Financial Core Ledger
```bash
lionsql start -dir /var/lib/lionsql/data -engine wiredtiger -fsync always -max-workers 128
```
* Enforces physical `F_FULLFSYNC` barriers and Leader/Follower Group Commit on every transaction commit, guaranteeing RPO=0 zero data loss.

#### Recipe 4: Bulk Data Migration and Nightly Settlement Batch
```bash
lionsql serve -dir /var/lib/lionsql/data -engine wiredtiger -work-mem 256MB -max-memory 16GB
```
* Expands operator sort memory to 256MB, preventing external disk sort file partitioning and executing 100% in-memory.

---

### 3.6. Global Memory Governor Practical Tuning

LionSQL implements a two-tier defense mechanism to prevent process termination by the OS OOM Killer:

1. **Global Heap Memory Budget (`--max-memory`)**:
   - When unspecified, automatically computes **65% of host physical RAM** (applying a 1GB OS reserve and 50% lower-bound guard) as the ceiling (e.g. 4GB RAM ➔ 2,662MB).
   - Integrates directly with Go 1.19+ `debug.SetMemoryLimit` primitives, triggering proactive runtime GC reclamation before hitting memory limits.
2. **Hash Join Fail-Fast Circuit Breaker (`--work-mem`)**:
   - If an anomalous query or runaway cartesian product exceeds the per-operator budget (default: `64MB`), it terminates immediately within 75µs, returning `hash join memory budget exceeded`.

---

### 3.7. Practical Logging and Diagnostics

* **Developer Tracing Mode (`-dev`)**:
   - Streams inbound queries, execution plan costs, parser fallback events, and connection handshakes in colorized real-time console output.
* **Slow Query Logging (`-slow-log=...`, `-slow-threshold-ms=100.0`)**:
   - Records queries exceeding 100ms into a dedicated log file, viewable visually in the Web Studio `/api/slow-queries` dashboard.
* **General Query Audit Logging (`-general-log=...`)**:
   - Sequentially records all inbound SQL statements alongside timestamps and client IPs for compliance and auditing.

---

## Chapter 4. Client Tool Integration

Because LionSQL serves three interfaces concurrently (PostgreSQL Wire Protocol v3, Microsoft SQL Server TDS 7.x/8.0, HTTP REST & Web Studio), existing database management tools and application libraries integrate seamlessly without code modifications.

---

### 4.1. PostgreSQL Standard Client Connection (`psql`)

LionSQL fully implements the PGWire v3 frame protocol, providing an interactive terminal experience via standard `psql`.

```bash
# Default connection command (default port: 5433, database: postgres)
psql -h 127.0.0.1 -p 5433 -U admin dbname=postgres
```
* **Password**: Administrator password (default: `1234567890`)
* **Single Catalog Standard**: The database name must strictly be `postgres` (legacy `lionsql` catalog has been completely removed).

#### Practical `psql` Meta-Commands:
```sql
-- 1. List tables in current session
\dt

-- 2. Inspect detailed column and index schema for a table
\d+ accounts

-- 3. List secondary indexes
\di

-- 4. Enable real-time millisecond query execution timing
\timing on

-- 5. Expanded table display mode (prevents horizontal scrolling on wide schemas)
\x auto

-- 6. Check server version
SELECT version();
```

---

### 4.2. MS SQL Server Standard Client Connection (`sqlcmd`)

Use Microsoft's official `sqlcmd` CLI tool to execute T-SQL batch scripts and manage stored procedures.

```bash
# Default connection command (default port: 1433, database: master)
sqlcmd -S 127.0.0.1,1433 -U sa -P 1234567890 -d master
```

#### Executing T-SQL Batch Queries:
```sql
1> SELECT name, database_id, compatibility_level FROM sys.databases;
2> GO
name       database_id compatibility_level
---------- ----------- -------------------
master               1                 150

(1 rows affected)
1> EXEC sp_tables;
2> GO
TABLE_QUALIFIER TABLE_OWNER TABLE_NAME TABLE_TYPE
--------------- ----------- ---------- ----------
master          dbo         accounts   TABLE     

(1 rows affected)
1> QUIT
```

---

### 4.3. Verified Graphical (GUI) Database Tools

Connection guides for six major tools validated with 100% compatibility by the LionSQL engineering team:

#### 1. Beekeeper Studio (Highly Recommended)
* **PostgreSQL Mode**:
  - **Connection Type**: `PostgreSQL`
  - **Host**: `127.0.0.1`, **Port**: `5433`, **User**: `admin`, **Password**: `1234567890`, **Database**: `postgres`
* **SQL Server Mode**:
  - **Connection Type**: `SQL Server`
  - **Host**: `127.0.0.1`, **Port**: `1433`, **User**: `sa`, **Password**: `1234567890`, **Database**: `master`
  - **SSL**: `Disabled` (or enable Trust Server Certificate)

#### 2. DBeaver Universal Database Tool
* **Driver Selection**: `PostgreSQL` or `Microsoft SQL Server`
* **Connection Details**: `localhost:5433` (PG) or `localhost:1433` (TDS)
* **Catalog Compatibility**:
  - DBeaver connection initialization queries (`USE <dbname>`, `SET extra_float_digits`) are handled by LionSQL's virtual catalog layer as tolerant virtual no-ops, ensuring flawless schema tree rendering without syntax errors.

#### 3. Navicat Premium
* **Connection Type**: `PostgreSQL`
* **Host**: `localhost`, **Port**: `5433`, **Database**: `postgres`, **User**: `admin`

#### 4. Azure Data Studio & SQL Server Management Studio (SSMS)
* **Server**: `127.0.0.1,1433` (Port separated by comma)
* **Authentication**: `SQL Server Authentication`
* **Login**: `sa`, **Password**: `1234567890`
* **Database**: `master`
* **Options**: `Trust server certificate = True`

---

### 4.4. Built-in Web Studio Full Utilization

Manage all aspects of the database visually through any web browser (`http://localhost:8090/_/`) without installing client software.

1. **Real-time SSE Live Stream Dashboard (`/api/realtime`)**:
   - The `Live Sync` badge in the header streams QPS, heap memory usage, and active goroutine worker counts in real-time charts updated every second.
2. **Real-time Request Activity Audit Log Viewer (`/api/logs/requests`)**:
   - The `Logs` tab tracks every inbound HTTP REST and SQL query with method, endpoint, client IP, HTTP status code, and latency measured in microseconds.
   - Slow queries exceeding 100ms are highlighted with a red badge (`#ef4444`).
3. **Non-Destructive Live Column Addition DDL Modal (`+ Add Column`)**:
   - Clicking `+ Add Column` in the table viewer appends columns to active table schemas in real time without downtime.
   - Enter column name, data type (`INT`, `VARCHAR`, `DATETIME`, `DECIMAL`), nullability, and default value via the form for instantaneous application.
4. **Table Data Viewer & Multi-Row Bulk Export/Import**:
   - Double-click cells for inline data editing with instant saving.
   - Select multiple rows via checkboxes and export via `Export JSON` or `Export CSV`.
   - Foreign key jump links (`↗`) instantly navigate directly to referenced target rows in parent tables.
5. **Developer API Preview Modal**:
   - Clicking `API` on any table row dynamically generates integration snippets for **cURL, Vanilla JavaScript `fetch()`, and the official LionSQL Web SDK (`lionsql.js`)**.
6. **SQL Query Studio & EXPLAIN Visualization**:
   - Execute SQL freely in a syntax-highlighted editor and view results in tabular/JSON representations.
   - Click `EXPLAIN` to visually inspect CBO index scan and join strategies.
7. **Light / Dark Theme High-Contrast (WCAG AAA) Support**:
   - Toggle instantly between themes via the sun/moon icon, featuring enhanced border contrast and badges in light mode.

8. **Single-Binary Static Web App Hosting (`--app`)**:
   - Serve frontend web applications alongside your database on `:8090` without external web servers using `--app=./index.html` or `--app=./dist`. (Details: [`doc/WEB_APP_HOSTING_GUIDE.md`](WEB_APP_HOSTING_GUIDE.md))

---

### 4.5. Single File / Flat Folder SPA Web App Hosting (`--app`)

LionSQL can host frontend web applications and high-concurrency database APIs simultaneously within a single static executable, completely eliminating the need for separate Nginx, Caddy, or Node.js daemons.

#### Key Scenarios
1. **Single HTML File Mode**:
   - `lionsql server --app=./dashboard.html`
   - Host self-contained dashboards (Alpine.js, Tailwind CDN) with instant live reloading.
2. **Flat Directory SPA Mode**:
   - `lionsql server --app=./dist`
   - Host production builds (React, Vue, Svelte) with automatic HTML5 History API fallback to `index.html`.

#### Same-Origin & Security Highlights
- **Zero CORS**: Frontend UIs and backend APIs share port `:8090`, completely eliminating cross-origin preflight requests.
- **Flat Directory Invariant**: Rejects path traversal (`..`) and hidden files (`.env`, `.git`) to ensure impenetrable static asset serving.

For full tutorials and architecture details, consult **[`WEB_APP_HOSTING_GUIDE.md`](WEB_APP_HOSTING_GUIDE.md)**.

---

### 4.6. Official Driver Integration Recipes by Programming Language

#### 1. Go (`database/sql` + `lib/pq` / `pgx`)
```go
package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

func main() {
	connStr := "host=127.0.0.1 port=5433 user=admin password=1234567890 dbname=postgres sslmode=disable"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var balance float64
	err = db.QueryRow("SELECT balance FROM accounts WHERE id = $1", 1).Scan(&balance)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Account Balance: $%.2f\n", balance)
}
```

#### 2. Python (`psycopg2` / `SQLAlchemy`)
```python
import psycopg2

conn = psycopg2.connect(
    host="127.0.0.1",
    port=5433,
    user="admin",
    password="1234567890",
    dbname="postgres"
)
cursor = conn.cursor()
cursor.execute("SELECT id, holder_name, balance FROM accounts WHERE balance > %s", (100000.0,))
for row in cursor.fetchall():
    print(f"ID: {row[0]}, Holder: {row[1]}, Balance: ${row[2]:,.2f}")

conn.close()
```

#### 3. Node.js / TypeScript (`pg`)
```typescript
import { Client } from 'pg';

const client = new Client({
  host: '127.0.0.1',
  port: 5433,
  user: 'admin',
  password: '1234567890',
  database: 'postgres',
});

async function main() {
  await client.connect();
  const res = await client.query('SELECT * FROM accounts WHERE id = $1', [1]);
  console.log('Result:', res.rows[0]);
  await client.end();
}

main().catch(console.error);
```

#### 4. Java / Spring Boot (`application.yml`)
```yaml
spring:
  datasource:
    url: jdbc:postgresql://127.0.0.1:5433/postgres
    username: admin
    password: "1234567890"
    driver-class-name: org.postgresql.Driver
  jpa:
    database-platform: org.hibernate.dialect.PostgreSQLDialect
    hibernate:
      ddl-auto: validate
    properties:
      hibernate.format_sql: true
```

#### 5. C# / .NET Core (`appsettings.json` + `Npgsql` / `SqlClient`)
```json
{
  "ConnectionStrings": {
    "LionSqlPostgres": "Host=127.0.0.1;Port=5433;Username=admin;Password=1234567890;Database=postgres",
    "LionSqlMssql": "Server=127.0.0.1,1433;Database=master;User Id=sa;Password=1234567890;TrustServerCertificate=True"
  }
}
```

---

## Chapter 5. Database Development Guide

### 5.1. Schema Design and Data Types

LionSQL provides native enforcement for data integrity constraints required in e-commerce, fintech, and enterprise SaaS systems:

```sql
-- 1. Customer orders table
CREATE TABLE orders (
    order_id INT IDENTITY(1001, 1) PRIMARY KEY,
    customer_id INT NOT NULL,
    order_date DATETIME DEFAULT GETDATE(),
    status VARCHAR(20) DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PAID', 'SHIPPED', 'CANCELLED')),
    total_amount DECIMAL(18, 2) NOT NULL CHECK (total_amount >= 0.00),
    notes TEXT,
    CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) 
        REFERENCES accounts(id) ON DELETE RESTRICT
);

-- 2. Order items table
CREATE TABLE order_items (
    item_id INT IDENTITY(1, 1) PRIMARY KEY,
    order_id INT NOT NULL,
    product_name VARCHAR(150) NOT NULL,
    unit_price DECIMAL(18, 2) NOT NULL,
    quantity INT NOT NULL CHECK (quantity > 0),
    subtotal DECIMAL(18, 2) NOT NULL,
    CONSTRAINT fk_items_order FOREIGN KEY (order_id) 
        REFERENCES orders(order_id) ON DELETE CASCADE
);
```
* **`IDENTITY(seed, increment)`**: Generates auto-incrementing monotonic numeric primary keys.
* **Cascade Actions (`ON DELETE CASCADE`)**: When a parent order row is deleted, child line items are deleted atomically in the same transaction.
* **Check Constraints (`CHECK`)**: Enforces data validation rules, preventing negative totals or invalid order statuses.

#### 5.1.4. Time-Series Auto-Rolling Range Partitioning (ClickHouse-Style Range Partitioning)

LionSQL supports **ClickHouse-style automatic rolling range partitioning** for managing high-volume time-series data and audit log lifecycles. Unlike traditional RDBMS approaches requiring manual pre-creation of child partition tables, partitions are automatically instantiated dynamically upon row insertion via a single DDL declaration:

```sql
-- 1. Monthly auto-rolling partitioning (toYYYYMM or MONTH function)
CREATE TABLE audit_logs (
    id INT PRIMARY KEY,
    created_at DATETIME,
    user_id VARCHAR(50),
    action VARCHAR(100)
) PARTITION BY toYYYYMM(created_at);

-- 2. Daily auto-rolling partitioning (toYYYYMMDD or DAY function)
CREATE TABLE daily_traffic (
    id INT PRIMARY KEY,
    logged_at DATETIME,
    ip_addr VARCHAR(50)
) PARTITION BY DAY(logged_at);
```

##### Key Capabilities and Practical Usage:
1. **Dynamic Auto-Rolling**: During `INSERT`, row timestamps (`created_at`) are evaluated automatically, creating and populating partitions like `202608`, `202609`, and `202610` on demand without external cron daemons.
2. **CBO Partition Pruning**: When executing queries such as `WHERE created_at >= '2026-10-01' AND created_at < '2026-11-01'`, the optimizer bypasses irrelevant partitions completely, reading exclusively from the targeted `[202610]` partition and reducing disk I/O by over 70% (`Partitions: [202610]` displayed in `EXPLAIN`).
3. **$O(1)$ Instant Partition Dropping**: Instead of executing slow bulk `DELETE` operations on hundreds of thousands of expired rows, entire partitions can be dropped physically with $O(1)$ complexity:
   ```sql
   -- Drop August 2026 partition immediately (Secondary indexes cleaned up synchronously)
   ALTER TABLE audit_logs DROP PARTITION '202608';
   ```
4. **Monitoring Partition Status**:
   ```sql
   SHOW PARTITIONS audit_logs;
   -- Or query virtual catalog views
   SELECT * FROM system.partitions WHERE table_name = 'audit_logs';
   ```
For comprehensive technical specifications, partitioning function reference, and detailed optimizer mechanics, refer to [RANGE_PARTITIONING.md](RANGE_PARTITIONING.md).

---

### 5.2. Index Design and Query Optimization Strategies

LionSQL supports secondary indexes backed by disk B+Trees and in-memory SkipLists, offering advanced indexing options that substantially reduce I/O costs:

```sql
-- 1. Single secondary index for customer lookups
CREATE INDEX idx_orders_customer ON orders(customer_id);

-- 2. Composite index for status filtering and date sorting
-- (Place columns with high cardinality or equality predicates on the left)
CREATE INDEX idx_orders_status_date ON orders(status, order_date DESC);

-- 3. Covering index with INCLUDE
-- Include total_amount and status as non-key columns in leaf nodes to eliminate main table bookmark lookups
CREATE INDEX idx_orders_covering ON orders(customer_id) INCLUDE (total_amount, status);
```

#### Covering Index Optimization Mechanics:
When executing the query below, the optimizer chooses an **Index-Only Scan**, scanning exclusively `idx_orders_covering` without fetching main table heap pages, improving throughput up to 5~10x:
```sql
SELECT customer_id, total_amount, status 
FROM orders 
WHERE customer_id = 101;
```

---

### 5.3. Transactions and Concurrency Control (ACID & Concurrency)

LionSQL eliminates traditional Two-Phase Locking (2PL) lock contention, providing **Non-blocking Snapshot Isolation / Read Committed** levels:
* **Non-blocking Reads**: `SELECT` queries acquire no locks and immediately return consistent snapshot data even while concurrent `UPDATE` or `INSERT` transactions run in the background.
* **Transaction Atomicity**:
  ```sql
  BEGIN TRANSACTION;
  
  UPDATE accounts SET balance = balance - 10000.00 WHERE id = 1;
  UPDATE accounts SET balance = balance + 10000.00 WHERE id = 2;
  
  COMMIT TRANSACTION; -- Automatic rollback upon conflict
  ```

#### OCC Serialization Conflicts (`40001`) and Retry Pattern:
Under the in-memory OCC storage engine, if two transactions concurrently modify the same row, the later commit triggers a `40001 (ErrWriteConflict)` error. Applications must implement an exponential backoff retry loop:

```go
// Standard exponential backoff retry pattern in Go applications
func executeWithRetry(db *sql.DB, query string, maxRetries int) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		_, err = db.Exec(query)
		if err == nil {
			return nil
		}
		// Check for 40001 conflict error, back off and retry
		if strings.Contains(err.Error(), "40001") || strings.Contains(err.Error(), "write conflict") {
			time.Sleep(time.Duration(10*(1<<i)) * time.Millisecond)
			continue
		}
		return err // Return other errors immediately
	}
	return fmt.Errorf("transaction aborted after %d retries: %w", maxRetries, err)
}
```

---

### 5.4. Relational Querying and Analytical Window Functions

```sql
-- 1. Multi-table join and aggregation query
SELECT 
    a.holder_name,
    COUNT(o.order_id) AS total_orders,
    COALESCE(SUM(o.total_amount), 0.00) AS gross_spend
FROM accounts a
LEFT JOIN orders o ON a.id = o.customer_id
WHERE a.balance > 0
GROUP BY a.holder_name
HAVING COUNT(o.order_id) >= 1
ORDER BY gross_spend DESC;

-- 2. Analytical window function (Customer purchase ranking)
SELECT 
    customer_id,
    order_id,
    total_amount,
    ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY total_amount DESC) AS spend_rank
FROM orders;

-- 3. Common Table Expression (CTE) with ANSI pagination
WITH VipOrders AS (
    SELECT customer_id, total_amount, order_date
    FROM orders
    WHERE total_amount >= 50000.00
)
SELECT a.holder_name, v.total_amount, v.order_date
FROM VipOrders v
INNER JOIN accounts a ON v.customer_id = a.id
ORDER BY v.order_date DESC
OFFSET 0 ROWS FETCH NEXT 10 ROWS ONLY;
```

---

### 5.5. T-SQL Stored Procedures and Business Logic

LionSQL compiles and runs T-SQL stored procedures at native speeds using an SQLite-style VDBE bytecode virtual machine.

```sql
CREATE PROCEDURE ProcessOrderPayment
    @order_id INT,
    @customer_id INT,
    @payment_amount DECIMAL(18,2),
    @new_balance DECIMAL(18,2) OUTPUT
AS
BEGIN
    SET NOCOUNT ON;
    
    DECLARE @user_bal DECIMAL(18,2);
    
    -- 1. Check account balance
    SELECT balance INTO @user_bal FROM accounts WHERE id = @customer_id;
    
    IF @user_bal < @payment_amount
    BEGIN
        RAISERROR('Customer balance is insufficient for payment.', 16, 1);
        RETURN -1;
    END
    
    BEGIN TRY
        -- 2. Atomically deduct balance and update order status
        UPDATE accounts SET balance = balance - @payment_amount WHERE id = @customer_id;
        UPDATE orders SET status = 'PAID' WHERE order_id = @order_id;
        
        -- 3. Return OUTPUT parameter
        SELECT balance INTO @new_balance FROM accounts WHERE id = @customer_id;
        
        RETURN 0;
    END TRY
    BEGIN CATCH
        PRINT 'Exception during payment processing: ' + ERROR_MESSAGE();
        RETURN -2;
    END CATCH
END;
```

#### Calling Stored Procedures and Receiving OUTPUT Parameters:
```sql
DECLARE @rem_bal DECIMAL(18,2);
EXEC ProcessOrderPayment 
    @order_id = 1001, 
    @customer_id = 1, 
    @payment_amount = 45000.00, 
    @new_balance = @rem_bal OUTPUT;

SELECT @rem_bal AS updated_balance;
```

---

### 5.6. Declarative Schema Migration Tool (`lionsql migrate`)

The LionSQL binary includes a built-in migration toolchain, eliminating the need for external migration tools:

```bash
# 1. Create new timestamped migration files
$ lionsql migrate new add_products_table
Created migration files:
  migrations/20241019_173000_add_products_table.up.sql
  migrations/20241019_173000_add_products_table.down.sql

# 2. Apply pending up-migrations
$ lionsql migrate up -dir=./data
Applying migration: 20241019_173000_add_products_table... Applied successfully.

# 3. Inspect migration status
$ lionsql migrate status -dir=./data
Applied Migrations:
  [X] 20241019_173000_add_products_table (Applied at: 2024-10-19 17:31:00)

# 4. Roll back latest migration step
$ lionsql migrate down -dir=./data -steps=1
Rolling back migration: 20241019_173000_add_products_table... Rolled back cleanly.
```

---

### 5.7. Utilizing Web Backend-as-a-Service (BaaS) Capabilities

LionSQL provides ready-to-use CRUD REST APIs automatically generated upon table creation without requiring backend application code:

```bash
# 1. Retrieve records (filtering, pagination, and sorting)
curl -s "http://127.0.0.1:8090/api/records/accounts?page=1&perPage=10&sort=-balance&filter=balance>50000"

# 2. Create new record (JSON)
curl -X POST "http://127.0.0.1:8090/api/records/accounts" \
  -H "Authorization: Bearer <ADMIN_JWT>" \
  -H "Content-Type: application/json" \
  -d '{"holder_name":"Diana Prince","email":"diana@amazon.com","balance":500000.00}'

# 3. Update record by ID
curl -X PUT "http://127.0.0.1:8090/api/records/accounts" \
  -H "Authorization: Bearer <ADMIN_JWT>" \
  -H "Content-Type: application/json" \
  -d '{"id":1,"balance":175000.00}'

# 4. Subscribe to real-time Server-Sent Events change stream
curl -N "http://127.0.0.1:8090/api/realtime"
```

---

## Chapter 6. Operations & Administration

### 6.1. Query Performance Analysis and Cost-Based Optimizer (CBO)

Following query parsing, LionSQL runs queries through a Cost-Based Optimizer (CBO) to generate execution plans that minimize I/O and CPU overhead.

#### 1. Inspecting Execution Plans: `EXPLAIN`
Inspect logical and physical execution plans with cost estimations without executing the query:
```sql
EXPLAIN SELECT * FROM orders WHERE customer_id = 101 AND status = 'PAID';
```
Output:
```text
-> IndexScan on orders using idx_orders_covering (cost=0.15..4.25 rows=3)
   Index Cond: (customer_id = 101)
   Filter: (status = 'PAID')
```

#### 2. Profiling Actual Execution: `EXPLAIN ANALYZE`
Executes the query to record actual execution times and returned row counts:
```sql
EXPLAIN ANALYZE SELECT * FROM orders WHERE customer_id = 101;
```
Output:
```text
-> IndexScan on orders using idx_orders_covering (cost=0.15..4.25 rows=3) (actual time=0.038ms rows=3 loops=1)
   Index Cond: (customer_id = 101)
Planning Time: 0.082 ms
Execution Time: 0.045 ms
```

#### 3. Collecting Statistical Data (`ANALYZE TABLE`)
After bulk loading or deleting data, update the optimizer's 64-bucket equi-height histograms to ensure optimal index selection:
```sql
ANALYZE TABLE orders;
ANALYZE TABLE order_items;
```

#### 4. Forcing Index Hints (`WITH (INDEX(...))`)
Explicitly steer index selection to bypass default optimizer decisions:
```sql
SELECT * FROM orders WITH (INDEX(idx_orders_customer)) WHERE customer_id = 101;
```

---

### 6.2. Online Hot Backup and Disaster Recovery (Backup & Restore)

LionSQL provides online hot backup capabilities, creating consistent snapshot archives (`.tar.gz`) without incurring downtime.

#### 1. CLI Local Online Backup
```bash
$ lionsql backup -dir=./data -out=/backups/lionsql_backup_20241019.tar.gz
Initiating online storage checkpoint... Done.
Archiving data directory to /backups/lionsql_backup_20241019.tar.gz...
Backup completed successfully. (Size: 45.2 MB, Checkpoint LSN: 1849300)
```

#### 2. Remote HTTP REST Backup Download
```bash
curl -s -X POST "http://127.0.0.1:8090/api/backup" \
  -H "Authorization: Bearer <ADMIN_JWT_TOKEN>" \
  --output remote_backup.tar.gz
```

#### 3. Restoring Backup Archives
Restore backups into a new server or target directory:
```bash
$ lionsql restore -dir=/var/lib/lionsql/data_restored -in=/backups/lionsql_backup_20241019.tar.gz
Validating backup archive integrity... Valid.
Extracting archive contents to /var/lib/lionsql/data_restored... Done.
Verifying storage headers and catalog checksums... OK.
Database restore completed successfully.
```
> [!CAUTION]
> To prevent accidental data overwrites, the restore target directory must be empty or newly created.

---

### 6.3. High Availability (HA) and Read-Replica Scale-Out (Replication)

LionSQL supports **1-Writer Multi-Reader asynchronous/semi-synchronous WAL replication** configured across a single Primary writer and multiple Replica readers.

#### Hands-On Local 2-Node Replication Cluster Setup:

```bash
# 1. Start Primary node (Ports: HTTP 8090, PG 5433, TDS 1433)
$ lionsql start -dir=./data_primary -node-id=primary-node

# 2. Start Replica node (Ports: HTTP 8091, PG 5434, TDS 1434)
# Streams WAL changes from Primary with auto-failover enabled
$ lionsql start -dir=./data_replica \
    -http=:8091 -pg=:5434 -tds=:1434 \
    -node-id=replica-node-1 \
    -replica-of=http://127.0.0.1:8090 \
    -auto-failover=true

# 3. Check Replica status
$ curl -s http://127.0.0.1:8091/api/replication/status
{
  "role": "REPLICA",
  "node_id": "replica-node-1",
  "primary_url": "http://127.0.0.1:8090",
  "synced_lsn": 1849300,
  "replication_lag_ms": 2.5
}
```

#### Zero-Downtime Failover and Promotion:
* **Auto-Failover**: If the Primary node is unreachable for over 3 seconds (`-heartbeat-timeout-ms`), the Replica holding the highest LSN automatically promotes to Primary and begins accepting write traffic.
* **Manual Promotion Command**:
  ```bash
  curl -X POST "http://127.0.0.1:8091/api/replication/promote" \
    -H "Authorization: Bearer <ADMIN_JWT_TOKEN>"
  ```

---

### 6.4. Enterprise Security and Network Encryption

In production environments, restrict public internet exposure and enable transport layer encryption:

#### 1. Generating Self-Signed TLS Certificate (Dev/Staging)
```bash
openssl req -x509 -newkey rsa:4096 -nodes \
  -keyout /etc/ssl/lionsql.key \
  -out /etc/ssl/lionsql.crt \
  -days 365 -subj "/CN=lionsql.local"
```

#### 2. Enforcing TLS Encryption on Startup
```bash
lionsql start -dir=/var/lib/lionsql/data \
  -tls=true \
  -tls-cert=/etc/ssl/lionsql.crt \
  -tls-key=/etc/ssl/lionsql.key \
  -require-ssl=true
```
* When `-require-ssl=true` is set, unencrypted plaintext connections are rejected at the handshake stage.

---

### 6.5. Fault Diagnostics and Troubleshooting

Key operational issues and remediation procedures:

| Symptom & Error Message | Root Cause Analysis | Remediation |
| :--- | :--- | :--- |
| `bind: address already in use` | A prior process did not terminate cleanly and holds the port | Check PID with `lionsql status -dir data` and run `lionsql stop`. If needed, `kill -9 <PID>` and restart |
| `hash join memory budget exceeded` | Massive cartesian product or unindexed join exceeded `work_mem` | Apply proper `WHERE` filters to the query, or raise session memory via `SET work_mem = '128MB'` |
| `serialization failure: write-write conflict (40001)` | Concurrent update contention on the same row under OCC or MVCC | Implement a 10~50ms exponential backoff retry loop in the application driver |
| `password authentication failed (28P01)` | Incorrect admin email or password | Verify `-admin-email` and `-admin-pass` set at launch or inspect `lionsql.json` |
| `Database connection failed` | Legacy database name (`lionsql`) supplied | Use the single-catalog standard: `postgres` for PGWire, `master` for TDS |
| `disk space low warning` | Data directory free disk space dropped below 1GB | Prune old backup files, rotate log files, or expand the host disk volume |
| `TLS handshake failed` | Certificate expired or domain mismatch | Verify certificate path and validity period; check client-side `TrustServerCertificate=True` |
| `table limit exceeded: memory limit exceeded` | In-memory heap reached limit (`--max-memory`) | Delete unnecessary table data or raise the `-max-memory` budget flag |

#### Comprehensive System Environment Diagnostics:
```bash
$ lionsql doctor -dir=./data
```
Outputs detailed verification across 5 domains: data file integrity, WAL checkpoint LSN, port availability, OS file descriptor limits, and available disk space.
