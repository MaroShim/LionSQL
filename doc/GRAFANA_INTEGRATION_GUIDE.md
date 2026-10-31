# Official Grafana Integration & Dual Data Source Guide on LionSQL (`doc/GRAFANA_INTEGRATION_GUIDE.md`)

> - **Document Version**: v1.0.0
> - **Created**: 2026-10-31
> - **Target Release**: LionSQL v0.90.0-beta4+
> - **Target Application**: Grafana v10.x ~ v13.x+ (`github.com/grafana/grafana`, zero-modification integration)
> - **Database Drivers**:
>   - Internal Metadata Backend: `github.com/lib/pq` (XORM over PostgreSQL Wire Protocol v3, TCP `:5433`)
>   - Query Data Source 1: `grafana-postgresql-datasource` (`lib/pq`, TCP `:5433`)
>   - Query Data Source 2: `mssql` (`github.com/microsoft/go-mssqldb`, TDS 7.x/8.0, TCP `:1433`)
> - **Related Documents**:
>   - Milestone Report: [`notes/GRAFANA_INTEGRATION_MILESTONE.md`](../notes/GRAFANA_INTEGRATION_MILESTONE.md)
>   - Korean Guide: [`doc/GRAFANA_INTEGRATION_GUIDE.ko.md`](GRAFANA_INTEGRATION_GUIDE.ko.md)

---

## 1. Overview & Dual-Channel Architecture

**Grafana** is the industry-standard open-source observability and data visualization platform used across cloud-native and enterprise environments.

LionSQL is a **self-contained, single-binary (Zero-Sidecar) OLTP database engine** with zero external daemon dependencies (no PgBouncer, Redis, Nginx, or etcd). A single `lionsql` process simultaneously exposes both **PostgreSQL Wire Protocol v3 (`:5433`)** and **Microsoft SQL Server TDS (`:1433`)**.

This allows LionSQL to serve Grafana across **two distinct channels simultaneously**:

```
+---------------------------------------------------------------------------------+
|                                Grafana Server                                   |
|                                                                                 |
|  [Channel 1: Internal Metadata Backend DB]      [Channel 2: Dashboard Data Sources]   |
|  - 785 migrations (users, RBAC, dashboards,     - Live SQL queries & visualizations   |
|    folders, alerts, datasources, data_keys)     - Plugin 1: PostgreSQL (:5433)        |
|  - Protocol: PostgreSQL Wire (:5433)            - Plugin 2: Microsoft SQL Server (:1433)|
+---------------------------------------------------------------------------------+
                 |                                        |             |
                 | TCP :5433 (PGWire)                     | TCP :1433   | TCP :5433
                 v                                        v (TDS)       v (PGWire)
+---------------------------------------------------------------------------------+
|                        LionSQL Database Kernel (Single Binary)                  |
|  - PostgreSQL Wire Listener: pkg/wire/server.go (:5433)                         |
|  - Microsoft SQL Server TDS Listener: pkg/wire/tds/server.go (:1433)            |
|  - Execution Engines: VDBE Bytecode VM & Volcano CBO Engine                     |
|  - Storage Engines: WiredTiger MVCC B+Tree Engine / In-Memory OCC Engine        |
+---------------------------------------------------------------------------------+
```

### Key Architectural Benefits
1. **Zero Client Code Modification**:
   - Runs unmodified Grafana source and binaries, executing all **785 database migrations** (728 core schema migrations + 57 unified resource migrations) cleanly.
2. **All-in-One Metadata Backend & Dual Data Source Target**:
   - Eliminates the need to deploy separate PostgreSQL and SQL Server instances; a single `lionsql` binary powers Grafana's internal metadata catalog while simultaneously serving both PostgreSQL and MSSQL dashboard queries.
3. **Full Envelope Encryption & Advisory Lock Compatibility**:
   - Natively supports PostgreSQL `BYTEA` (`\x<hex>`) round-trip binary serialization for Grafana's `data_keys` envelope encryption subsystem and PostgreSQL session/transaction advisory locks (`pg_try_advisory_lock`).

---

## 2. Prerequisites & Starting LionSQL

### 2.1. Building LionSQL
```bash
# Production Unix binary (with WiredTiger MVCC persistent B+Tree engine)
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
```

### 2.2. Starting the LionSQL Server
Start LionSQL with an isolated data directory (e.g., `data_grafana/`):
```bash
./bin/lionsql -data-dir=data_grafana -admin-pass=1234567890
```

Active listeners upon startup:
- **PostgreSQL Wire Protocol v3**: `127.0.0.1:5433`
- **Microsoft SQL Server TDS 7.x/8.0**: `127.0.0.1:1433`
- **LionSQL Web Studio**: `http://127.0.0.1:8090/_/`

---

## 3. Building & Configuring Grafana

### 3.1. Building Grafana & Installing Core SQL Data Source Plugins
In modern Grafana releases (v13.0+), the PostgreSQL and MSSQL data sources are distributed as decoupled backend plugins. Build the Grafana binary with `-ldflags "-X main.version=13.0.0"` so plugin version checks succeed, then install both plugins:

```bash
cd /Users/maro/Projects/grafana

# 1. Build Grafana server & CLI binary
go build -ldflags "-w -s -X main.version=13.0.0" -o ./bin/grafana ./pkg/cmd/grafana

# 2. Install official PostgreSQL and Microsoft SQL Server data source plugins
./bin/grafana cli --pluginsDir "/Users/maro/Projects/grafana/data/plugins" plugins install grafana-postgresql-datasource
./bin/grafana cli --pluginsDir "/Users/maro/Projects/grafana/data/plugins" plugins install mssql
```

### 3.2. Configuration (`conf/custom.ini`)
Configure Grafana to use LionSQL's PostgreSQL Wire listener (`:5433`) as its internal database:

```ini
[server]
http_port = 3100
domain = localhost

[database]
type = postgres
host = 127.0.0.1:5433
name = grafana
user = admin
password = 1234567890
ssl_mode = disable

[security]
admin_user = admin
admin_password = 1234567890

[paths]
data = /Users/maro/Projects/grafana/data
logs = /Users/maro/Projects/grafana/data/log
plugins = /Users/maro/Projects/grafana/data/plugins
provisioning = /Users/maro/Projects/grafana/conf/provisioning
```

### 3.3. Starting Grafana & Verifying 785 Migrations
```bash
cd /Users/maro/Projects/grafana
./bin/grafana server --config conf/custom.ini
```

Startup logs confirm that both migration engines complete with zero errors:
1. **Core Migrator**: `migrations performed=728` (728 entries recorded in `migration_log`)
2. **Resource Migrator**: `migrations performed=57` (57 entries recorded in `resource_migration_log`)
3. **RBAC & Envelope Encryption**: Admin user creation, role/permission seeding (`role`, `permission`, `builtin_role`), and `data_keys` encryption key initialization succeed before listening on `HTTP :3100`.

---

## 4. Registering & Querying Dual Data Sources (PGWire & MSSQL TDS)

With Grafana running on `http://localhost:3100`, register both LionSQL wire protocols as data sources and execute live queries.

### 4.1. Registering Data Source 1: LionSQL PostgreSQL (`:5433`)
```bash
curl -s -u admin:1234567890 -X POST http://localhost:3100/api/datasources \
  -H "Content-Type: application/json" \
  -d '{
    "name": "LionSQL-PG",
    "type": "postgres",
    "access": "proxy",
    "url": "127.0.0.1:5433",
    "user": "admin",
    "database": "grafana",
    "basicAuth": false,
    "jsonData": {
      "sslmode": "disable",
      "postgresVersion": 1500
    },
    "secureJsonData": {
      "password": "1234567890"
    }
  }'
```

### 4.2. Registering Data Source 2: LionSQL Microsoft SQL Server (`:1433`)
```bash
curl -s -u admin:1234567890 -X POST http://localhost:3100/api/datasources \
  -H "Content-Type: application/json" \
  -d '{
    "name": "LionSQL-MSSQL",
    "type": "mssql",
    "access": "proxy",
    "url": "127.0.0.1:1433",
    "user": "admin",
    "database": "grafana",
    "basicAuth": false,
    "jsonData": {
      "encrypt": "disable"
    },
    "secureJsonData": {
      "password": "1234567890"
    }
  }'
```

### 4.3. Data Source Health Checks (`/api/datasources/uid/<uid>/health`)
```bash
# PostgreSQL Data Source Health Check
curl -s -u admin:1234567890 http://localhost:3100/api/datasources/uid/<pg-datasource-uid>/health
# Response: {"message":"Database Connection OK","status":"OK"}

# MSSQL Data Source Health Check
curl -s -u admin:1234567890 http://localhost:3100/api/datasources/uid/<mssql-datasource-uid>/health
# Response: {"message":"Database Connection OK","status":"OK"}
```

### 4.4. Executing Dual-Protocol Queries (`POST /api/ds/query`)
Query both the PostgreSQL (`:5433`) and MSSQL (`:1433`) data sources simultaneously in a single request:

```bash
curl -s -u admin:1234567890 -X POST http://localhost:3100/api/ds/query \
  -H "Content-Type: application/json" \
  -d '{
    "queries": [
      {
        "refId": "PG",
        "datasource": { "type": "postgres", "uid": "<pg-datasource-uid>" },
        "rawSql": "SELECT id, name, type, url FROM data_source ORDER BY id",
        "format": "table"
      },
      {
        "refId": "MSSQL",
        "datasource": { "type": "mssql", "uid": "<mssql-datasource-uid>" },
        "rawSql": "SELECT id, name, type, url FROM data_source ORDER BY id",
        "format": "table"
      }
    ],
    "from": "now-1h",
    "to": "now"
  }'
```

Response (`HTTP 200 OK`):
```json
{
  "results": {
    "MSSQL": {
      "status": 200,
      "frames": [{
        "data": {
          "values": [
            [1, 2],
            ["LionSQL-PG", "LionSQL-MSSQL"],
            ["postgres", "mssql"],
            ["127.0.0.1:5433", "127.0.0.1:1433"]
          ]
        }
      }]
    },
    "PG": {
      "status": 200,
      "frames": [{
        "data": {
          "values": [
            [1, 2],
            ["LionSQL-PG", "LionSQL-MSSQL"],
            ["postgres", "mssql"],
            ["127.0.0.1:5433", "127.0.0.1:1433"]
          ]
        }
      }]
    }
  }
}
```

---

## 5. Troubleshooting & Operational Notes

1. **Plugin Not Registered (`404 plugin.notRegistered`)**:
   - Building Grafana from source without `-ldflags` defaults `main.version` to `10.2.0`, causing Grafana's plugin loader to skip v13.0+ plugins. Always build with `-ldflags "-X main.version=13.0.0"` and ensure `grafana-postgresql-datasource` and `mssql` are installed in `data/plugins`.
2. **Envelope Encryption (`data_keys`) Integrity**:
   - Grafana encrypts data source passwords (`secureJsonData.password`) using DEKs stored in `data_keys` (`BYTEA`). LionSQL preserves binary DEK payloads via standard PostgreSQL `\x<hex>` encoding and decoding.
3. **Single-Directory Snapshot Backups**:
   - Because all Grafana metadata tables, RBAC permissions, dashboards, and encrypted keys reside inside LionSQL's `--data-dir` directory, backing up the entire environment requires only a directory snapshot.
