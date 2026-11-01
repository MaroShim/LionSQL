# Woodpecker CI Integration Guide (LionSQL PGWire)

This document provides instructions for integrating **Woodpecker CI**, a lightweight container-native CI/CD platform (alternative to GitHub Actions and Drone CI), with LionSQL using the PostgreSQL Wire Protocol (PGWire v3, default port `:5433`, WiredTiger storage engine).

---

## 1. Overview and Compatibility Architecture

Woodpecker CI communicates with PostgreSQL databases via the Go **XORM** library (`github.com/go-xorm/xorm` / `xorm.io/xorm`) and `lib/pq` / `pgx` drivers.

- **Supported Protocol**: PostgreSQL Wire Protocol v3 (`pgx`, `lib/pq`)
- **LionSQL Recommended Engine**: WiredTiger (MVCC ACID transactions and high-concurrency B+Tree support)
- **Applied Migrations**: 19 tables, 32 migration steps (completes in under 0.5s)
- **Primary Tables**:
  - `repos`, `builds`, `steps`, `logs`, `users`, `secrets`, `registries`, `tasks`, `orgs`, `agents`, etc.

---

## 2. Prerequisites

- **Build LionSQL Binary** (with WiredTiger engine):
  ```bash
  go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
- **Official Woodpecker Docker Image**:
  ```bash
  docker pull woodpeckerci/woodpecker-server:v3
  ```
  *(Note: The official 3.x release line uses the rolling tag `woodpeckerci/woodpecker-server:v3`)*

---

## 3. Running and Integration Steps

### Step 1: Start LionSQL Server
```bash
./bin/lionsql -dir data_woodpecker_pg -engine wiredtiger -pg 0.0.0.0:5433 -http 127.0.0.1:8095 -admin-pass="WoodpeckerPass2026!"
```

### Step 2: Start Woodpecker CI Server Container
```bash
docker run -d \
  --name woodpecker_server \
  --network host \
  -e WOODPECKER_DATABASE_DRIVER=postgres \
  -e WOODPECKER_DATABASE_DATASOURCE="postgres://postgres:WoodpeckerPass2026!@127.0.0.1:5433/woodpecker?sslmode=disable" \
  -e WOODPECKER_SERVER_ADDR=":8000" \
  -e WOODPECKER_HOST="http://localhost:8000" \
  -e WOODPECKER_DEV_ALWAYS_AUTHENTICATE=true \
  woodpeckerci/woodpecker-server:v3
```

---

## 4. Verification

### Healthcheck and Version Inspection
```bash
# Healthcheck (returns HTTP 204 No Content)
curl -I http://127.0.0.1:8000/healthz

# Version query (v3.20.1)
curl http://127.0.0.1:8000/version

# Web UI dashboard access (HTTP 200 OK)
curl -I http://127.0.0.1:8000/
```

---

## 5. Architectural Highlights & Compatibility Details

1. **Parameterized XORM Migrations (`pg_tables`, `pg_attribute`)**:
   - `SELECT tablename FROM pg_tables WHERE schemaname = $1` and `SELECT attname FROM pg_attribute ...` system catalog queries are routed with zero overhead via LionSQL's virtual catalog routing layer.
2. **Transactional Session Isolation**:
   - Sequential execution of DDL and index creation (`CREATE INDEX IF NOT EXISTS`) during migrations maintains complete transactional consistency.
