# Official Vaultwarden Integration & Deployment Guide with LionSQL (`guide/VAULTWARDEN_INTEGRATION_GUIDE.md`)

> - **Document Version**: v1.0.0
> - **Initial Date**: 2026-11-01
> - **Target Release**: LionSQL v0.90.0-beta4+
> - **Integration Target**: Vaultwarden (Stock official Docker image `vaultwarden/server:latest` without modifications)
> - **Supported Wire Protocol**:
>   - **PostgreSQL Wire Protocol v3** (Default TCP `:5433`)
> - **Reference Documents**:
>   - Integration Report: [`notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md`](../notes/reports/VAULTWARDEN_INTEGRATION_REPORT.ko.md)
>   - Modular Architecture Plan: [`notes/MODULAR_ARCHITECTURE_PLAN.ko.md`](../notes/plans/MODULAR_ARCHITECTURE_PLAN.ko.md)

---

## 1. Overview and Architectural Advantages

**Vaultwarden** is a lightweight, alternative Bitwarden-compatible server written in Rust using the Rocket web framework and Diesel ORM (with PostgreSQL backend support). It is fully compatible with official Bitwarden desktop, browser extension, CLI, and mobile clients.

LionSQL operates as an autonomous, single-binary, **Zero-Sidecar OLTP database engine** with no external daemon dependencies (no PgBouncer, Redis, or etcd), fully serving the **PostgreSQL Wire Protocol (`:5433`)**.

Using LionSQL as the backend RDBMS for Vaultwarden delivers distinct enterprise advantages:

1. **Zero Source Code Changes**:
   - Runs stock official Vaultwarden Docker containers (`vaultwarden/server:latest`) or binaries without modifying a single line of application source code or migration SQL.
   - Fully satisfies Diesel ORM's strict binary serialization requirements (such as 8-byte integer timestamp OID 1114) and introspective system catalog queries.
2. **100% Automatic Execution of 47 Diesel Migrations**:
   - All 47 PostgreSQL migrations run cleanly on first startup without errors, automatically creating all 29 relational tables.
3. **Composite Primary Key & Conflict Resolution Support**:
   - Seamlessly handles `ON CONFLICT DO UPDATE` over composite primary keys such as `devices (uuid, user_uuid)`, ensuring smooth multi-device token synchronization.
4. **Minimal Footprint & Single Standalone Binary**:
   - Replaces heavy database containers with LionSQL's compact footprint, powered by sub-microsecond VDBE execution and WiredTiger lock-free B+Tree storage.

---

## 2. Quick Start: Docker Compose Setup

The most streamlined deployment method uses Docker Compose.

### Sample `docker-compose.yml`

```yaml
version: '3.8'

services:
  lionsql:
    image: lionsql:latest  # or local host-built binary
    container_name: lionsql
    restart: unless-stopped
    ports:
      - "5433:5433"   # PostgreSQL Wire Protocol
      - "8090:8090"   # LionSQL Web Studio & REST API
    volumes:
      - ./data/lionsql:/data
    command: ["lionsql", "-dir", "/data", "-engine", "wiredtiger", "-pg", "0.0.0.0:5433", "-admin-pass", "VaultwardenDBPass2026!"]

  vaultwarden:
    image: vaultwarden/server:latest
    container_name: vaultwarden
    restart: unless-stopped
    depends_on:
      - lionsql
    ports:
      - "8080:80"     # Vaultwarden Web Vault & API
    environment:
      - DATABASE_URL=postgresql://admin:VaultwardenDBPass2026!@lionsql:5433/vaultwarden
      - SIGNUPS_ALLOWED=true
      - WEBSOCKET_ENABLED=true
    volumes:
      - ./data/vaultwarden:/data
```

---

## 3. Manual Deployment Instructions

### 3.1 Start LionSQL Server
Launch LionSQL with the WiredTiger storage engine and PostgreSQL wire listener:

```bash
mkdir -p data_vaultwarden
./bin/lionsql -dir data_vaultwarden -engine wiredtiger -pg 0.0.0.0:5433 -admin-pass="VaultwardenDBPass2026!"
```

### 3.2 Launch Vaultwarden Container
Connect Vaultwarden to the host LionSQL instance:

```bash
docker run -d --name vaultwarden \
  -e DATABASE_URL="postgresql://admin:VaultwardenDBPass2026!@host.docker.internal:5433/vaultwarden" \
  -e SIGNUPS_ALLOWED="true" \
  -p 8080:80 \
  -v $(pwd)/vw-data:/data \
  vaultwarden/server:latest
```

Upon launch, Vaultwarden's output confirms pending Diesel migrations:
```
[INFO] Running all pending migrations...
[INFO] Executing migration: 2019-09-12-100000_create_tables
[INFO] Executing migration: ...
[INFO] Executing migration: 2026-10-07-120000_add_org_policy_revision_date
[INFO] Rocket has launched from http://0.0.0.0:80
```

---

## 4. Maintenance and Backup

Because LionSQL adheres to a unified single-directory architecture, maintenance is simple and reliable.

### 4.1 Offline Snapshot Backup
With LionSQL cleanly stopped or during a maintenance window, archive the data directory:
```bash
tar -czvf vaultwarden_backup_$(date +%Y%m%d).tar.gz data_vaultwarden/
```

### 4.2 Health Inspection via CLI or Web Studio
Inspect schema migration status using standard PostgreSQL CLI (`psql`) or LionSQL Web Studio (`http://localhost:8090/_/`):

```bash
psql -h 127.0.0.1 -p 5433 -U admin -d vaultwarden -c "SELECT count(*) FROM __diesel_schema_migrations;"
# Output: 47
```

---

## 5. Troubleshooting & FAQ

### Q: Does LionSQL handle multi-action `ALTER TABLE` statements?
Yes. LionSQL natively splits and executes compound `ALTER TABLE` statements (e.g. `ALTER COLUMN ... TYPE ..., ALTER COLUMN ... SET NOT NULL`) sequentially, guaranteeing complete compatibility with Diesel ORM migrations.

### Q: Are timestamps properly synchronized across official clients?
Yes. LionSQL maps PostgreSQL datetime types to OID 1114 (`TIMESTAMP`) with 8-byte binary integer encoding (microseconds since 2000-01-01), preventing deserialization panics in Rust client runtimes.
