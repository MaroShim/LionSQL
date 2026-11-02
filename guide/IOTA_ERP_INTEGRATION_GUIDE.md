# Official LionSQL Integration & Operation Guide for IOTA-SDK Go ERP (`guide/IOTA_ERP_INTEGRATION_GUIDE.md`)

> - **Document Version**: v1.0.0
> - **Initial Date**: 2026-10-31
> - **Target Release**: LionSQL v0.90.0-beta4+
> - **Integration Target**: IOTA SDK Go ERP (github.com/iota-uz/iota-sdk, unmodified official source)
> - **Database Driver**: `jackc/pgx/v5` (`pgxpool`)
> - **Wire Protocol**: **PostgreSQL Wire Protocol v3** (default TCP `:5433`)
> - **Related Documents**:
>   - Verification Report: [`notes/IOTA_ERP_INTEGRATION_REPORT.ko.md`](../notes/IOTA_ERP_INTEGRATION_REPORT.ko.md)
>   - Gitea Integration Guide: [`guide/GITEA_INTEGRATION_GUIDE.md`](GITEA_INTEGRATION_GUIDE.md)

---

## 1. Overview & Architectural Benefits

**IOTA SDK** is a modular enterprise framework designed in Go for building enterprise resource planning (ERP) solutions. It features modules for Finance, Identity & Access Management (IAM/OIDC), Manufacturing, CRM, and Warehouse/Inventory management.

LionSQL is a **standalone, zero-sidecar, high-concurrency OLTP database engine** with zero external dependencies (no PgBouncer, Redis, or Nginx required). It natively powers `iota-sdk` over its PostgreSQL Wire Protocol (TCP `:5433`).

### Key Architectural Benefits
1. **Zero-Code Modification**:
   - Runs `iota-sdk` and its 47 SQL migrations out of the box without changing a single line of application or migration code.
   - LionSQL transparently executes complex `ALTER TABLE` operations, `USING GIN` index clauses, schema aliases, and privilege DDLs.
2. **Strict `pgx/v5` Binary Protocol Fidelity**:
   - Full support for the extended query protocol (Parse/Bind/Describe/Execute/Sync) and raw binary formatting (16-byte UUIDs, microsecond `TIMESTAMPTZ` offsets) as required by `jackc/pgx/v5`.
3. **Standalone Single-Binary Deployment (Zero-Sidecar)**:
   - Eliminates database installation wizards and Docker daemon requirements. A single static binary (`lionsql`) delivers an enterprise-grade In-Memory OCC or WiredTiger disk MVCC database.
4. **Extreme Hardware Efficiency**:
   - Operates with a compact memory footprint of tens of megabytes, in sharp contrast to conventional multi-gigabyte RDBMS setups.

---

## 2. Prerequisites & Starting LionSQL

### 2.1. Building LionSQL
LionSQL can be built with the high-performance WiredTiger disk persistence engine or in pure-Go in-memory mode:

```bash
# Production Unix binary (includes WiredTiger MVCC disk engine)
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql

# Pure-Go in-memory standalone binary (zero CGo)
go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
```

### 2.2. Starting the LionSQL Daemon
Launch LionSQL specifying the data directory:

```bash
# Disk persistence mode (default data directory: data/)
./bin/lionsql -data-dir=data -admin-pass=1234567890

# Or ultra-fast in-memory testing mode (-ephemeral)
./bin/lionsql -ephemeral -engine=memory -admin-pass=1234567890
```

Upon startup, the console displays active listeners:
- `[Web Dashboard]  http://127.0.0.1:8090/_/`
- `[Postgres Wire]  psql -h 127.0.0.1 -p 5433`
- `[TDS / MSSQL]    sqlcmd -S 127.0.0.1,1433`

---

## 3. Configuring & Building IOTA ERP

### 3.1. Clone Repository & Download Dependencies
```bash
cd /Users/maro/Projects/iota-sdk
go mod download
```

### 3.2. Configuration Setup (`.env` or `.env.local`)
Set the database parameters pointing to LionSQL's PostgreSQL port (`5433`):

```env
# Server
PORT=3200
ENV=production

# Database (LionSQL PGWire v3)
DB_HOST=127.0.0.1
DB_PORT=5433
DB_USER=admin
DB_PASSWORD=1234567890
DB_NAME=iota
DB_SSL_MODE=disable

# UI Lens Assets Directory
LENS_ASSETS_DIR=/tmp/lens-dist
```

> [!NOTE]
> If Vite frontend assets are not locally built, generate a minimal bundle manifest to bootstrap the server:
> ```bash
> mkdir -p /tmp/lens-dist/.vite
> echo '{"index.html":{"file":"index.js"}}' > /tmp/lens-dist/.vite/manifest.json
> touch /tmp/lens-dist/index.js
> ```

---

## 4. Running Schema Migrations

IOTA SDK uses `rubenv/sql-migrate` for versioned database migrations.

### 4.1. Building the Migration CLI
```bash
go build -o bin/iota ./cmd/iota
```

### 4.2. Applying Migrations (`migrate up`)
```bash
./bin/iota migrate up
```

Console confirmation:
```text
INFO[0000] Applied 47 migrations
```

### 4.3. Verifying Migration Status (`migrate status`)
```bash
./bin/iota migrate status
```
Verify that all 47 schema migration files are listed as `Applied` with valid timestamps.

---

## 5. Starting ERP Server & Verifying Web UI

### 5.1. Building & Starting the Server
```bash
go build -o bin/server ./cmd/server
LENS_ASSETS_DIR=/tmp/lens-dist ./bin/server
```

Upon successful startup, the server logs confirm listener readiness:
```text
INFO config loaded provenance="..."
Listening on: http://localhost:3200
```

### 5.2. Web Access & Login Page
- Access `http://localhost:3200` in your browser. The request redirects `HTTP 302 Found -> /login` and serves the official ERP login interface.
- CLI verification:
  ```bash
  curl -i -L http://localhost:3200
  ```
  Returns `HTTP/1.1 200 OK` with `<title>Log in</title>`.

---

## 6. Troubleshooting & Technical Reference

| Issue | Cause | Resolution |
| :--- | :--- | :--- |
| `cannot scan varchar (OID 1043) into *time.Time` | Older engine parser defaulted unrecognized `TIMESTAMPTZ` to VARCHAR | Upgrade to latest LionSQL v0.90.0-beta4+ and recreate schema |
| `invalid length for UUID: 36` | Driver expects 16-byte raw binary for OID 2950 but received hex string | Verify `Format = 1` binary decoder is active in PGWire |
| `USING GIN syntax error` | Migration script includes GIN index clause | LionSQL parses `USING GIN` and automatically degrades it to a fast B+Tree / expression index |
| `column "CONSTRAINT" already exists` | Inline table constraint mistakenly parsed as a column definition | Resolved by the inline constraint separator in LionSQL DDL engine |
