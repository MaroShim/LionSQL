# Official LionSQL Integration & Operation Guide for Gitea (`doc/GITEA_INTEGRATION_GUIDE.md`)

> - **Document Version**: v1.0.0
> - **Initial Date**: 2026-10-30
> - **Target Release**: LionSQL v0.90.0-beta4+
> - **Integration Target**: Gitea v1.23 ~ v1.27+ (unmodified official binary/source)
> - **Supported Wire Protocols**:
>   - **PostgreSQL Wire Protocol v3** (default TCP `:5433`)
>   - **Microsoft SQL Server TDS 7.x/8.0** (default TCP `:1433`)
> - **Related Documents**:
>   - Architecture Postmortem: [`notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](../notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md)
>   - Milestone Report: [`notes/GITEA_INTEGRATION_MILESTONE.md`](../notes/GITEA_INTEGRATION_MILESTONE.md)

---

## 1. Overview & Architectural Benefits

**Gitea** is a high-load, full-stack Go web platform used by engineering organizations worldwide for source code hosting, team collaboration, and CI/CD pipelines.

LionSQL is a **standalone, zero-sidecar OLTP database engine** with zero external daemon dependencies (no PgBouncer, Redis, Nginx, or etcd needed). A single binary (`lionsql`) simultaneously listens on **PostgreSQL Wire Protocol (`:5433`)** and **Microsoft SQL Server TDS (`:1433`)**.

Adopting LionSQL as the backend RDBMS for Gitea yields enterprise-grade advantages:

1. **Zero-Code Change**:
   - Runs official Gitea binaries (v1.27+) out of the box without changing a single line of application code.
   - LionSQL transparently handles XORM SQL generation patterns, dialect-specific DDL/DML, and system catalog queries.
2. **Dual-Wire Protocol Freedom**:
   - **PostgreSQL Mode** (`lib/pq`, `:5433`): Standard for cloud-native and open-source environments.
   - **SQL Server Mode** (`go-mssqldb`, `:1433`): Optimized for Microsoft enterprise environments (SSMS, .NET, Azure Data Studio).
3. **Extreme Hardware Efficiency**:
   - Replaces multi-gigabyte enterprise RDBMS footprints with a compact memory footprint of tens of megabytes, powered by sub-microsecond VDBE bytecode execution and In-Memory OCC / WiredTiger B+Tree engines.
4. **Headless GitOps Automation**:
   - Bypasses web-based setup wizards completely; a single configuration file (`app.ini`) initializes 100+ tables and launches production services immediately.

---

## 2. Protocol Comparison Matrix

| Dimension | Mode A: PostgreSQL Wire (Recommended) | Mode B: Microsoft SQL Server (TDS) |
| :--- | :--- | :--- |
| **Gitea `DB_TYPE`** | `postgres` | `mssql` |
| **Default Port** | `:5433` (Avoids conflict with standard 5432) | `:1433` (Standard SQL Server port) |
| **Driver** | `github.com/lib/pq` (PGWire v3) | `github.com/microsoft/go-mssqldb` (TDS 7.x/8.0) |
| **PK Generation** | `BIGSERIAL PRIMARY KEY NOT NULL` | `BIGINT IDENTITY(1,1) PRIMARY KEY NOT NULL` |
| **Inserted ID Retrieval** | `INSERT ... RETURNING "id"` | `SCOPE_IDENTITY()`, `OUTPUT INSERTED.id` |
| **Catalog Introspection** | `pg_catalog.pg_indexes`, `pg_attribute` | `sysobjects`, `sys.columns`, `sys.fn_helpcollations` |
| **Parameter Protocol** | `$1, $2, ...` Extended Query binding | `@p1, @p2, ...` RPC positional mapping |
| **Recommended Deployment** | Containers (Docker/K8s), Linux/macOS Native | Windows Server, Active Directory, SSMS Environments |

---

## 3. Prerequisites & Starting LionSQL

### 3.1. Building the Binary
```bash
# Production Unix binary (includes WiredTiger MVCC disk persistence engine)
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql

# Windows standalone binary (zero CGo, 1ms high-res timer)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql
```

### 3.2. Starting LionSQL Daemon
```bash
# Production background daemon mode
./bin/lionsql -data-dir=data -admin-pass=1234567890

# Development mode with real-time query logging (-dev)
./bin/lionsql -data-dir=data -dev
```

Console confirmations upon startup:
- `[Web Dashboard]  http://127.0.0.1:8090/_/`
- `[Postgres Wire]  psql -h 127.0.0.1 -p 5433`
- `[TDS / MSSQL]    sqlcmd -S 127.0.0.1,1433`

---

## 4. Gitea Configuration (`custom/conf/app.ini`)

Place the following configuration in `custom/conf/app.ini` in your Gitea working directory.

### 4.1. Mode A: PostgreSQL Wire (`DB_TYPE = postgres`)
```ini
APP_NAME = Gitea on LionSQL (PostgreSQL Wire)
RUN_USER = git
WORK_PATH = /var/lib/gitea
RUN_MODE = prod

[database]
DB_TYPE  = postgres
HOST     = 127.0.0.1:5433
NAME     = gitea
USER     = admin
PASSWD   = 1234567890
SCHEMA   = public
SSL_MODE = disable
LOG_SQL  = false

[repository]
ROOT = /var/lib/gitea/data/gitea-repositories

[server]
SSH_DOMAIN       = localhost
DOMAIN           = localhost
HTTP_PORT        = 3000
ROOT_URL         = http://localhost:3000/
APP_DATA_PATH    = /var/lib/gitea/data
DISABLE_SSH      = false
SSH_PORT         = 2222
LFS_START_SERVER = true

[security]
INSTALL_LOCK       = true
INTERNAL_TOKEN     = eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
PASSWORD_HASH_ALGO = pbkdf2
```

### 4.2. Mode B: Microsoft SQL Server (`DB_TYPE = mssql`)
```ini
APP_NAME = Gitea on LionSQL (SQL Server TDS)
RUN_USER = git
WORK_PATH = /var/lib/gitea
RUN_MODE = prod

[database]
DB_TYPE  = mssql
HOST     = 127.0.0.1:1433
NAME     = gitea
USER     = sa
PASSWD   = 1234567890
SSL_MODE = disable
LOG_SQL  = false

[repository]
ROOT = /var/lib/gitea/data/gitea-repositories

[server]
SSH_DOMAIN       = localhost
DOMAIN           = localhost
HTTP_PORT        = 3000
ROOT_URL         = http://localhost:3000/
APP_DATA_PATH    = /var/lib/gitea/data
DISABLE_SSH      = false
SSH_PORT         = 2222
LFS_START_SERVER = true

[security]
INSTALL_LOCK       = true
INTERNAL_TOKEN     = eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
PASSWORD_HASH_ALGO = pbkdf2
```

> [!IMPORTANT]
> Setting `INSTALL_LOCK = true` completely bypasses the browser setup wizard. Gitea connects to LionSQL at startup and automatically migrates all 100+ tables.

---

## 5. Startup & Automated Migration

### 5.1. Launching Gitea
```bash
./gitea web
```

XORM executes atomic DDLs across:
- Users & permissions (`user`, `org_user`, `team`)
- Repositories & branches (`repository`, `branch`, `repo_unit`)
- Issues & collaboration (`issue`, `comment`, `pull_request`, `milestone`, `label`)
- Security & credentials (`access_token`, `auth_token`, `two_factor`)

Startup confirmation:
```text
[I] ORM engine initialization successful!
[I] Listen: http://0.0.0.0:3000
[I] AppURL(ROOT_URL): http://localhost:3000/
```

### 5.2. Creating Initial Administrator (CLI)
```bash
./gitea admin user create \
  --username admin \
  --password "StrongAdminPassword123!" \
  --email "admin@lionsql.local" \
  --admin
```
Log in at `http://localhost:3000` with the created credentials.

---

## 6. End-to-End Verification (E2E)

Run the automated mock data seeder ([`scripts/seed_gitea_mock_data.go`](../scripts/seed_gitea_mock_data.go)) to verify full Gitea functionality:

```bash
go run scripts/seed_gitea_mock_data.go
```

### Verified Scenarios:
1. **User Creation**: 4 users (Developer, Code Reviewer, QA Lead, Performance Engineer)
2. **Organization Creation**: `lionsql-core` (`type = 1` integrity verified)
3. **Repository Creation & Git Commits**:
   - `lionsql-core/lion-engine`
   - `lionsql-core/lion-bench`
   - `lionsql-core/lion-docs`
   - `admin/personal-scripts`
4. **Milestones & Labels**: `bug`, `enhancement`, `performance`, `protocol`, `benchmark`
5. **Issues & Comments**: Threaded discussion across multiple users
6. **Feature Branches & Commits**: `feature/flat-hash-join` with `docs/HASH_JOIN.md`
7. **Pull Requests & Merges**: PR #5 creation, reviewer approval, merge to `main`
8. **Release Tags**: `v1.0.0-rc1` publishing

---

## 7. Production Best Practices

### 7.1. Storage Engine Selection
- **WiredTiger MVCC B+Tree (`-tags wiredtiger`)** (Recommended): Full file-based ACID durability for Git LFS and enterprise repository data.
- **In-Memory OCC (`pkg/storage/memengine`)**: Ultra-fast ephemeral testing, CI/CD runners, and 100k+ tpmC benchmarking.

### 7.2. Session Pooler Tuning
```bash
./bin/lionsql -data-dir=data \
  -max-workers=64 \
  -max-idle=128 \
  -max-memory=4096MB
```

### 7.3. Online Backup & Disaster Recovery
Trigger live backups via REST API or TDS:
```bash
curl -X POST http://127.0.0.1:8090/api/backup \
  -H "Authorization: Bearer <ADMIN_JWT_TOKEN>" \
  -d '{"target_dir": "/backup/lionsql_gitea"}'
```

---

## 8. Troubleshooting

| Issue | Cause | Solution |
| :--- | :--- | :--- |
| Login failure or panic during auth | Parameter substituter corrupted `$5` inside PBKDF2 hash | Upgrade to latest LionSQL with quote-aware scanner |
| Organization returns 404 Not Found | String parameter stored as string instead of int (`type: 1`) | Resolved by `ParseLiteralDefault` implicit type coercion |
| Port conflict (`bind: address already in use`) | Port `:5432` or `:1433` occupied by existing service | Use LionSQL default `:5433` for PGWire or customize via `-tds-port=14333` |
