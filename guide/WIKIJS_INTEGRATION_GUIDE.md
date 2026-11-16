# Wiki.js Integration Guide (`WIKIJS_INTEGRATION_GUIDE.md`)

> - **Document Version**: v1.0.0
> - **Initial Created**: 2026-11-16
> - **Last Updated**: 2026-11-16
> - **Target Release**: LionSQL v0.93.0+
> - **Integration Target**: Wiki.js (Modern open-source wiki, Node.js/TypeScript + Knex/Drizzle ORM based)
> - **Supported Wire Protocol**: **PostgreSQL Wire Protocol v3** (default TCP `:5433`) and **Microsoft SQL Server TDS 7.x/8.0** (default TCP `:1433`)
> - **한국어 문서**: [`guide/WIKIJS_INTEGRATION_GUIDE.ko.md`](WIKIJS_INTEGRATION_GUIDE.ko.md)

This document provides instructions for integrating **Wiki.js**, a modern open-source enterprise wiki platform used by over 50,000 organizations worldwide, with LionSQL using either the PostgreSQL Wire Protocol (PGWire v3, default port `:5433`) or Microsoft SQL Server TDS Protocol (default port `:1433`).

---

## 1. Overview and Compatibility Architecture

Wiki.js communicates with relational databases in a Node.js runtime using `node-postgres` (`pg`), **Knex.js**, and **Drizzle ORM**. Knex officially supports both `postgres` and `mssql` dialects, allowing operators to choose either wire protocol.

- **Supported Protocols**: 
  - **PostgreSQL Wire Protocol v3** (`pg`, `:5433`): `postgres://<user>:<pass>@<host>:5433/wikijs`
  - **Microsoft SQL Server TDS 7.x/8.0** (`tedious`, `:1433`): Knex MSSQL Dialect (bracket identifiers, `OUTPUT INSERTED` support)
- **LionSQL Recommended Engine**: WiredTiger (Large markdown document revision control and high-speed B+Tree indexing)
- **Applied Migrations**: Knex / Drizzle automated migrations (`knex_migrations`, `knex_migrations_lock`)
- **Primary Tables**:
  - `knex_migrations`, `knex_migrations_lock`: DDL schema migration versions and concurrency locks
  - `pages`, `pageHistory`: Markdown contents, rendered HTML, and edit revisions
  - `users`, `groups`: User accounts, authentication roles, and group memberships
  - `navigation`, `site`: Global wiki navigation hierarchy and site configurations

---

## 2. Prerequisites

- **Build LionSQL Binary**:
  ```bash
  go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
- **Official Wiki.js Docker Image**:
  ```bash
  docker pull ghcr.io/requarks/wiki:2
  ```

---

## 3. Running and Integration Steps

### Step 1: Start LionSQL Server
Start the server with a fresh data directory (`data_wikijs`) and the PostgreSQL listener enabled:
```bash
./bin/lionsql -dir data_wikijs -engine wiredtiger -pg 0.0.0.0:5433 -tds 0.0.0.0:1433 -http 127.0.0.1:8097 -admin-pass="WikiPass2026!"
```

### Step 2: Start Wiki.js Container

#### Option A: PostgreSQL Wire Protocol (`:5433`)
```bash
docker run -d \
  --name wikijs \
  -p 3000:3000 \
  -e DB_TYPE=postgres \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=5433 \
  -e DB_NAME=wikijs \
  -e DB_USER=postgres \
  -e DB_PASS="WikiPass2026!" \
  ghcr.io/requarks/wiki:2
```

#### Option B: Microsoft SQL Server TDS Protocol (`:1433`)
```bash
docker run -d \
  --name wikijs-mssql \
  -p 3000:3000 \
  -e DB_TYPE=mssql \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=1433 \
  -e DB_NAME=wikijs \
  -e DB_USER=sa \
  -e DB_PASS="WikiPass2026!" \
  ghcr.io/requarks/wiki:2
```

*For local source deployments, configure `config.yml` (PostgreSQL or SQL Server):*
```yaml
# PostgreSQL PGWire (:5433)
port: 3000
db:
  type: postgres
  host: 127.0.0.1
  port: 5433
  user: postgres
  pass: WikiPass2026!
  db: wikijs
  schema: public
  ssl: false

# Or MS SQL Server TDS (:1433)
# port: 3000
# db:
#   type: mssql
#   host: 127.0.0.1
#   port: 1433
#   user: sa
#   pass: WikiPass2026!
#   db: wikijs
#   ssl: false
  db: wikijs
  schema: public
  ssl: false
```

---

## 4. Verification

### Initial Onboarding and Page Creation
1. Open `http://localhost:3000` in your web browser.
2. Complete the initial administrator account setup (email, password, site URL).
3. Create your first page (Home Page), write Markdown, and click [Save].
4. Verify data persistence on LionSQL:
   ```bash
   ./bin/lionsql sql "SELECT path, title FROM wiki_pages;"
   ```

---

## 5. Engineering Notes

1. **Knex Distributed Lock Reset**:
   - If a container restarts unexpectedly leaving `knex_migrations_lock` locked, reset it via:
     `./bin/lionsql sql "UPDATE knex_migrations_lock SET is_locked = 0 WHERE index = 1;"`
2. **PostgreSQL Driver Compatibility**:
   - Wiki.js's `node-postgres` (`pg`) driver sends an initial `SSLRequest` packet (`0x04D2162F`), which LionSQL transparently negotiates for both non-SSL and TLS environments.
