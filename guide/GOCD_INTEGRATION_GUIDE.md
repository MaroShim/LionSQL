# GoCD Integration Guide (`GOCD_INTEGRATION_GUIDE.md`)

> - **Document Version**: v1.0.0
> - **Initial Created**: 2026-11-16
> - **Last Updated**: 2026-11-16
> - **Target Release**: LionSQL v0.93.0+
> - **Integration Target**: GoCD (Thoughtworks Enterprise CI/CD Server, Java/Hibernate + Liquibase based)
> - **Supported Wire Protocol**: **PostgreSQL Wire Protocol v3** (default TCP `:5433`) and **Microsoft SQL Server TDS 7.x/8.0** (default TCP `:1433`)
> - **한국어 문서**: [`guide/GOCD_INTEGRATION_GUIDE.ko.md`](GOCD_INTEGRATION_GUIDE.ko.md)

This document explains how to integrate Thoughtworks' open-source enterprise continuous delivery and pipeline orchestration platform, **GoCD**, with LionSQL using either the PostgreSQL Wire Protocol (PGWire v3, default port `:5433`) or Microsoft SQL Server TDS Protocol (default port `:1433`).

---

## 1. Overview and Compatibility Architecture

GoCD communicates with databases in a Java runtime environment using the official PostgreSQL JDBC driver (`org.postgresql.Driver`) or Microsoft SQL Server JDBC driver (`com.microsoft.sqlserver.jdbc.SQLServerDriver`), Hibernate ORM, and **Liquibase** for database schema migrations.

- **Supported Protocols**: 
  - **PostgreSQL Wire Protocol v3** (JDBC `:5433`): `jdbc:postgresql://<host>:5433/gocd`
  - **Microsoft SQL Server TDS 7.x/8.0** (JDBC `:1433`): `jdbc:sqlserver://<host>:1433;databaseName=gocd;encrypt=false`
- **LionSQL Recommended Engine**: WiredTiger (MVCC ACID transactions and high-concurrency pipeline state transitions)
- **Applied Migrations**: Liquibase-driven `DATABASECHANGELOGLOCK` lock management and `DATABASECHANGELOG` automated schema execution
- **Primary Tables**:
  - `DATABASECHANGELOG`, `DATABASECHANGELOGLOCK`: Liquibase migration history and concurrency locks
  - `ACCESSTOKEN`: User personal access tokens and authentication
  - `AGENTS`: Distributed build agent UUID, IP, and status registry
  - `PIPELINES`, `STAGES`: Pipeline trigger counters and stage execution histories

---

## 2. Prerequisites

- **Build LionSQL Binary**:
  ```bash
  go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
- **Official GoCD Docker Image or Server Artifact**:
  ```bash
  docker pull gocd/gocd-server:v24.2.0
  ```

---

## 3. Running and Integration Steps

### Step 1: Start LionSQL Server
Start the server with a fresh data directory (`data_gocd`) and the PostgreSQL wire listener enabled:
```bash
./bin/lionsql -dir data_gocd -engine wiredtiger -pg 0.0.0.0:5433 -tds 0.0.0.0:1433 -http 127.0.0.1:8096 -admin-pass="GoCDPass2026!"
```

### Step 2: Start GoCD Container with Database Environment Variables

#### Option A: PostgreSQL Wire Protocol (`:5433`)
```bash
docker run -d \
  --name gocd-server \
  -p 8153:8153 \
  -e GO_CONFIG_REPO_GC_DISABLED=true \
  -e DB_TYPE=postgresql \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=5433 \
  -e DB_NAME=gocd \
  -e DB_USER=postgres \
  -e DB_PASSWORD="GoCDPass2026!" \
  gocd/gocd-server:v24.2.0
```

#### Option B: Microsoft SQL Server TDS Protocol (`:1433`)
```bash
docker run -d \
  --name gocd-server-mssql \
  -p 8153:8153 \
  -e GO_CONFIG_REPO_GC_DISABLED=true \
  -e DB_TYPE=sqlserver \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=1433 \
  -e DB_NAME=gocd \
  -e DB_USER=sa \
  -e DB_PASSWORD="GoCDPass2026!" \
  gocd/gocd-server:v24.2.0
```

*For local standalone deployments, configure `db.properties` (PostgreSQL or SQL Server):*
```properties
# PostgreSQL PGWire (:5433)
db.type=postgresql
db.host=127.0.0.1
db.port=5433
db.name=gocd
db.user=postgres
db.password=GoCDPass2026!

# Or MS SQL Server TDS (:1433)
# db.type=sqlserver
# db.host=127.0.0.1
# db.port=1433
# db.name=gocd
# db.user=sa
# db.password=GoCDPass2026!
```

---

## 4. Verification

### Health Check and Web UI
```bash
# Verify health endpoint
curl -I http://127.0.0.1:8153/go/api/v1/health

# Open Web Studio / Dashboard
open http://127.0.0.1:8153
```

When creating pipelines or registering agents (`gocd-agent`), records are persistently stored in the `AGENTS` and `PIPELINES` tables on LionSQL.

---

## 5. Engineering Notes

1. **Liquibase Distributed Lock Recovery**:
   - If GoCD halts unexpectedly leaving `DATABASECHANGELOGLOCK.LOCKED = 1`, run `./bin/lionsql sql "UPDATE DATABASECHANGELOGLOCK SET LOCKED = 0 WHERE ID = 1;"` to reset the lock.
2. **Transaction Isolation**:
   - GoCD requires `READ COMMITTED` by default, which LionSQL fully guarantees with non-blocking MVCC.
