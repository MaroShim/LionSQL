# LionSQL Declarative Schema Migrations & CLI Tooling Guide

This guide details the standalone `lionsql` command-line interface (CLI) and its declarative schema migration engine (`pkg/migrate`).

---

## 1. Overview & Architecture

LionSQL provides an in-tree declarative schema migration toolchain packaged directly inside the standalone `lionsql` executable. It replaces external migration utilities (such as Flyway, Liquibase, or Goose) with a native, zero-dependency solution.

```
      [ Developer / CI/CD Pipeline ]
                     │
                     ▼
      [ lionsql migrate up -data=./data ]
                     │
                     ▼
      ┌─────────────────────────────────┐
      │     Migration Engine Loader     │
      │  - Scans ./migrations/*.sql     │
      │  - Queries `_migrations` table  │
      │  - Computes pending versions    │
      └────────────────┬────────────────┘
                       │
                       ▼
      ┌─────────────────────────────────┐
      │   Atomic Transaction Boundary   │
      │   tx.Begin()                    │
      │     ├── Execute <step>.up.sql   │
      │     └── INSERT INTO _migrations │
      │   tx.Commit()                   │
      └─────────────────────────────────┘
```

### Key Architectural Invariants:
1. **Atomic Transaction Durability**: Each migration step executes within an explicit ACID transaction boundary (`sess.BeginTx()` $\rightarrow$ `tx.Commit()`). If any DDL or DML statement fails, the transaction is rolled back immediately, leaving zero partial tables or corrupt schema states.
2. **Metadata Ledger Invariance**: Migration history is recorded in an internal tracking table (`_migrations`). The table is initialized automatically upon the first migration execution.
3. **Dual-Backend Support**: Migrations execute natively against both `wiredtiger` (default disk storage) and `memory` (pure-Go in-memory OCC) engines.

---

## 2. CLI Command Reference

The `lionsql` CLI provides commands for schema lifecycle management and direct local query execution:

```bash
lionsql <command> [arguments...]
```

### 2.1. `lionsql migrate new <name>`
Generates a new timestamped migration pair in the target migrations directory.

* **Usage**:
  ```bash
  lionsql migrate new <migration_name> [-dir=./migrations]
  ```
* **Flags**:
  - `-dir`: Directory to store migration files (default: `./migrations`).
* **Example**:
  ```bash
  $ lionsql migrate new create_users_table -dir=./migrations
  Created migration files:
    ./migrations/20241017120000_create_users_table.up.sql
    ./migrations/20241017120000_create_users_table.down.sql
  ```

### 2.2. `lionsql migrate up`
Applies all pending migrations in ascending chronological order.

* **Usage**:
  ```bash
  lionsql migrate up [-dir=./migrations] [-data=./data] [-engine=wiredtiger|memory] [-steps=N]
  ```
* **Flags**:
  - `-dir`: Migrations directory path (default: `./migrations`).
  - `-data`: Target database data directory (default: `./data`).
  - `-engine`: Storage engine backend: `wiredtiger` (default) or `memory`.
  - `-steps`: Number of pending migrations to apply ($0$ or omitted = apply all).
* **Example**:
  ```bash
  $ lionsql migrate up -data=/var/lib/lionsql/data -dir=./migrations
  Applying 2 pending migration(s)...
  Applying 20241017120000_create_users_table... OK (14ms)
  Applying 20241017120500_add_user_roles... OK (8ms)
  Successfully applied 2 migration(s).
  ```

### 2.3. `lionsql migrate down`
Rolls back previously applied migrations in reverse chronological order.

* **Usage**:
  ```bash
  lionsql migrate down [-dir=./migrations] [-data=./data] [-engine=wiredtiger|memory] [-steps=N]
  ```
* **Flags**:
  - `-steps`: Number of migrations to roll back (default: `1`).
* **Example**:
  ```bash
  $ lionsql migrate down -steps=1 -data=/var/lib/lionsql/data -dir=./migrations
  Rolling back 1 migration(s)...
  Rolling back 20241017120500_add_user_roles... OK (11ms)
  Successfully rolled back 1 migration(s).
  ```

### 2.4. `lionsql migrate status`
Inspects and prints the status table of all discovered migrations.

* **Usage**:
  ```bash
  lionsql migrate status [-dir=./migrations] [-data=./data] [-engine=wiredtiger|memory]
  ```
* **Output Sample**:
  ```text
  VERSION         NAME                STATUS   APPLIED AT (UTC)      DURATION
  20241017120000  create_users_table  APPLIED  2024-10-17 12:01:14   14ms
  20241017120500  add_user_roles      APPLIED  2024-10-17 12:06:22   8ms
  20241017121000  create_orders_table PENDING  -                     -
  ```

### 2.5. `lionsql sql`
Executes one-off SQL statements directly against a local database directory without launching network daemons:

* **Usage**:
  ```bash
  lionsql sql "<query>" [-data=./data] [-engine=wiredtiger|memory]
  ```
* **Example**:
  ```bash
  $ lionsql sql "SELECT table_name, row_count FROM sys.tables;" -data=/var/lib/lionsql/data
  ```

---

## 3. Migration File Format & Syntax

Migration files strictly follow the 14-digit UTC timestamp convention:
`YYYYMMDDHHMMSS_<name>.<up|down>.sql`

### 3.1. Standard Migration File Example

**`20241017120000_create_accounts.up.sql`**:
```sql
-- Migration: create_accounts (Up)
CREATE TABLE accounts (
    id INT PRIMARY KEY,
    email VARCHAR NOT NULL,
    balance FLOAT NOT NULL DEFAULT 0.0,
    created_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX idx_accounts_email ON accounts (email);
```

**`20241017120000_create_accounts.down.sql`**:
```sql
-- Migration: create_accounts (Down)
DROP TABLE IF EXISTS accounts;
```

---

## 4. Metadata Schema & Tracking Invariant

The migration engine maintains tracking state in the system metadata table `_migrations`:

```sql
CREATE TABLE _migrations (
    version     VARCHAR PRIMARY KEY,
    name        VARCHAR NOT NULL,
    applied_at  DATETIME NOT NULL,
    duration_ms INT NOT NULL
);
```

### Safety Guarantees:
1. **No Duplicate Execution**: The engine checks `SELECT version FROM _migrations` prior to processing. If a version exists, it is skipped during `migrate up`.
2. **Missing Down-File Protection**: Executing `migrate down` requires a corresponding `.down.sql` file. If the file is missing or unreadable, the command halts immediately before mutating the database.
3. **Lexicographical Timestamp Ordering**: Migrations are strictly sorted by version string. Manual insertions with out-of-order timestamps trigger validation errors.

---

## 5. Production CI/CD & Kubernetes Deployment

### 5.1. Docker Entrypoint Script
Run migrations automatically before starting the LionSQL server daemon:

```bash
#!/bin/sh
set -e

# Apply pending schema migrations
/usr/local/bin/lionsql migrate up -data=/data -dir=/migrations -engine=wiredtiger

# Launch database server daemon
exec /usr/local/bin/lionsql serve --data-dir=/data --port=5433 --tds-port=1433 --http-port=8090
```

### 5.2. Kubernetes InitContainer Pattern
Ensure database schema is up-to-date prior to application pod startup:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: lionsql-deployment
spec:
  template:
    spec:
      initContainers:
        - name: schema-migration
          image: lionsql:latest
          command: ["lionsql", "migrate", "up", "-data=/var/lib/lionsql/data", "-dir=/etc/lionsql/migrations"]
          volumeMounts:
            - name: db-data
              mountPath: /var/lib/lionsql/data
            - name: db-migrations
              mountPath: /etc/lionsql/migrations
      containers:
        - name: lionsql-server
          image: lionsql:latest
          command: ["server", "--data-dir=/var/lib/lionsql/data"]
          ports:
            - containerPort: 5433
            - containerPort: 1433
            - containerPort: 8090
          volumeMounts:
            - name: db-data
              mountPath: /var/lib/lionsql/data
      volumes:
        - name: db-data
          persistentVolumeClaim:
            claimName: lionsql-pvc
        - name: db-migrations
          configMap:
            name: lionsql-migrations-cm
```
