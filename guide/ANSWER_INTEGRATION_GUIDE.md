# Official Deployment and Operation Guide for Apache Answer on LionSQL (`guide/ANSWER_INTEGRATION_GUIDE.md`)

> - **Document Version**: v1.0.0
> - **Initial Date**: 2026-11-01
> - **Target Release**: LionSQL v0.90.0-beta4+
> - **Target Application**: Apache Answer v1.4+ (Unmodified official source/binary)
> - **Supported Protocol**:
>   - **PostgreSQL Wire Protocol v3** (Default TCP `:5433`)
> - **Reference Documents**:
>   - Live Integration Report: [`notes/ANSWER_INTEGRATION_REPORT.ko.md`](../notes/ANSWER_INTEGRATION_REPORT.ko.md)
>   - Modular Architecture Plan: [`notes/MODULAR_ARCHITECTURE_PLAN.ko.md`](../notes/MODULAR_ARCHITECTURE_PLAN.ko.md)

---

## 1. Overview & Architectural Benefits

**Apache Answer** is an open-source Q&A community knowledge-base platform widely used by engineering teams. Its backend is built with Go, XORM, and the standard PostgreSQL driver (`github.com/lib/pq`).

LionSQL is a **standalone, zero-sidecar OLTP database engine** with zero external dependencies (no PgBouncer, Redis, Nginx, or etcd). A single static binary (`lionsql`) natively provides the **PostgreSQL Wire Protocol (`:5433`)**.

Using LionSQL as the backend RDBMS for Apache Answer provides several key architectural advantages:

1. **Zero Source Code Changes**:
   - Run official Apache Answer binaries or containers without changing a single line of code.
   - XORM query patterns, PostgreSQL-specific DDL/DML, and system catalog queries are seamlessly handled by LionSQL.
2. **Multi-Row Batch INSERT & Auto-Increment Identity**:
   - High-throughput multi-row batch inserts for seed data (`role_power_rel`) automatically allocate sequential identity keys via the VDBE bytecode virtual machine.
3. **Hardware Efficiency & Compact Footprint**:
   - Sub-microsecond execution with VDBE and WiredTiger MVCC engine runs in tens of megabytes, drastically reducing infrastructure costs compared to heavyweight traditional databases.
4. **Headless Automated Deployment (GitOps-Friendly)**:
   - Automated installation via environment variables (`AUTO_INSTALL=true`) initializes all 35 tables without manual browser interaction.

---

## 2. Requirements & Setup

### 2.1 Start LionSQL Server
Start LionSQL with the WiredTiger storage engine and PostgreSQL wire protocol enabled:
```bash
./bin/lionsql -dir data_answer -engine wiredtiger -pg 127.0.0.1:5433 -http 127.0.0.1:8090
```

### 2.2 Headless Automated Installation (`answer init`)
Apache Answer natively supports headless setup using environment variables:
```bash
mkdir -p /path/to/answer_data

AUTO_INSTALL=true \
DB_TYPE=postgres \
DB_HOST=127.0.0.1:5433 \
DB_USERNAME=admin \
DB_PASSWORD=1234567890 \
DB_NAME=answer \
LANGUAGE=en_US \
SITE_NAME="LionSQL Q&A" \
SITE_URL="http://127.0.0.1:9080" \
CONTACT_EMAIL=admin@lionsql.local \
ADMIN_NAME=admin \
ADMIN_PASSWORD=adminadmin \
ADMIN_EMAIL=admin@lionsql.local \
EXTERNAL_CONTENT_DISPLAY=always_display \
answer init -C /path/to/answer_data
```

### 2.3 Verify Configuration (`config.yaml`)
Answer automatically creates `/path/to/answer_data/conf/config.yaml`. Adjust the HTTP port as desired (e.g., 9080):
```yaml
debug: false
server:
  http:
    addr: 0.0.0.0:9080
data:
  database:
    driver: postgres
    connection: host=127.0.0.1 port=5433 user=admin password=1234567890 dbname=answer sslmode=disable
  cache:
    file_path: /path/to/answer_data/cache/cache.db
i18n:
  bundle_dir: /path/to/answer_data/i18n
service_config:
  upload_path: /path/to/answer_data/uploads
```

---

## 3. Running Answer Service

### 3.1 Start Answer Daemon
```bash
answer run -C /path/to/answer_data
```

### 3.2 Verify Health & Core APIs
```bash
# Fetch site metadata
curl -s http://127.0.0.1:9080/answer/api/v1/siteinfo

# Authenticate admin (receive JWT token)
curl -s -X POST http://127.0.0.1:9080/answer/api/v1/user/login/email \
  -H "Content-Type: application/json" \
  -d '{"e_mail":"admin@lionsql.local","pass":"adminadmin"}'
```

---

## 4. Troubleshooting

### Q1: `table "pg_tables" not found`
- **Cause**: In older LionSQL versions, system catalog queries were routed to physical storage.
- **Fix**: Upgrade to LionSQL v0.90.0-beta4 or later, which routes virtual catalog queries to the virtual view engine.

### Q2: `missing primary key id` on batch INSERT
- **Cause**: Multi-row batch inserts omitting identity columns failed to auto-generate IDs in previous VDBE releases.
- **Fix**: Use the latest LionSQL binary with the `IdentityProvider` SPI integration.
