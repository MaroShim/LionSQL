# LionSQL 1.0 Unified CLI Architecture & Implementation Plan

## 1. Executive Summary

LionSQL is fundamentally architected as an **autonomous, zero-external-dependency standalone static executable** (GEMINI.md Section 5).

Previously, the executable entry points were divided into two separate binaries:
- `bin/server`: Database engine server, wire protocol listeners, daemon control, backup/restore, and doctor diagnostics (`cmd/server/main.go`).
- `bin/lionsql`: CLI schema migration manager and one-off SQL executor (`cmd/lionsql/main.go`).

This duality compromises the single-binary value proposition, introduces a generic and colliding binary name (`server`), duplicates build targets, and fragments developer experience.

This plan establishes the comprehensive consolidation of all server and CLI capabilities into a single, canonical executable: **`bin/lionsql`**.

---

## 2. Design Principles & Ergonomics

### 2.1. PocketBase / CockroachDB Model (Single Canonical Binary)
The unified CLI follows the battle-tested single-binary paradigm established by **PocketBase**, **CockroachDB**, **HashiCorp Vault**, and **SurrealDB**:

1. **Zero-Argument Default (Docker & Local Dev First)**:
   Running `lionsql` with no subcommands (or with flags only) immediately launches the full database engine in the **foreground**, streaming logs to `stdout/stderr`. This provides zero-configuration instant startup and perfect Docker container compatibility (`ENTRYPOINT ["lionsql"]`).
2. **First-Class Service Lifecycle Commands**:
   Eliminates awkward flag combinations (`-d`, `-daemon`). A service that can be `stop`ped is started via `start`:
   - `lionsql start`: Spawns and detaches the background daemon process.
   - `lionsql stop`: Sends a graceful shutdown signal to the running daemon.
   - `lionsql restart`: Safely stops the running daemon and restarts a fresh instance.
   - `lionsql status`: Inspects PID file, process liveness, and active port listeners.
3. **Dual Tolerance (Backward Compatibility)**:
   Accepts both modern subcommands (`lionsql stop`) and legacy flag invocations (`lionsql -stop`, `lionsql -status`) seamlessly.
4. **Developer-Centric Flag Aliases**:
   Retains `-dev` as the canonical flag for verbose query/parameter logging, while supporting intuitive aliases (`--debug`, `-v`, `--verbose`).

---

## 3. Strict Security & Branding Invariants (1.0 Non-Negotiables)

1. **Secure by Default Authentication (`-auth=true`)**:
   - `Auth.Enabled` defaults to `true` across CLI flags, configuration structs, and runtime listeners.
   - External REST and administrative endpoints are protected by default.
2. **Default Admin Password Change Reminder**:
   - When the administrator password remains the factory default (`"1234567890"`), the server startup banner displays a conspicuous **`[SECURITY WARNING]`** prompting the user to update credentials via `-admin-pass` or `lionsql.json`.
3. **Standard Administrative Email**:
   - Default superuser email is strictly standardized to `admin@lionsql.local`.
4. **Strict `lionsql.*` File Naming & Zero Silent Fallback**:
   - **Config File**: Exclusively searches `--config <path>`, `./lionsql.json`, and `<dir>/lionsql.json`. Legacy `sqlion.json` is **never silently loaded** to eliminate configuration hijacking risks; if detected, a loud warning is emitted while the legacy file is ignored.
   - **PID File**: Exclusively `<dir>/lionsql.pid` to avoid killing unintended processes across PID re-allocations.
   - **Log File**: Exclusively `<dir>/lionsql.log`.
   - **Outbox Topic Prefix**: Default `'lionsql.'`.
5. **Slow Query Threshold**:
   - Default threshold raised from `50ms` to **`100ms`** (`-slow-threshold-ms=100.0`) for production-grade operational signal-to-noise ratio.

---

## 4. Unified Command Specification & CLI Syntax Map

```text
lionsql [command] [options...]
```

### 4.1. Server Lifecycle Commands

| Command | Execution Mode | Description | Primary Use Case |
| :--- | :--- | :--- | :--- |
| **`lionsql`** *(no args or flags only)* | **Foreground** | Launches HTTP (:8090), PGWire (:5433), and TDS (:1433) in the active terminal. Terminate via `Ctrl+C`. | Local dev, debugging, Docker, Kubernetes. |
| **`lionsql serve`** | **Foreground** | Explicit subcommand alias for foreground launch (PocketBase parity). | Explicit deployment scripts, systemd service units. |
| **`lionsql start`** | **Background Daemon** | Spawns a detached background daemon, writes `<dir>/lionsql.pid`, and returns to shell. | Bare-metal Linux, macOS background service. |
| **`lionsql stop`** | **Control** | Reads PID from `<dir>/lionsql.pid`, dispatches graceful interrupt, and escalates to SIGKILL within 1.5s (Rule 4). | Host maintenance, automated service shutdown. |
| **`lionsql restart`** | **Control** | Executes `stop` followed by `start`, preserving all active CLI flags and configurations. | Configuration reload, service recycling. |
| **`lionsql status`** | **Diagnostic** | Reports daemon PID, uptime, active storage engine, and port listening status. | Operations health checks, watchdog scripts. |

### 4.2. Database Management & Developer Tools

| Command | Subcommands / Syntax | Description |
| :--- | :--- | :--- |
| **`lionsql migrate`** | `new <name>`<br>`up [-steps=N]`<br>`down [-steps=N]`<br>`status` | Declarative database schema migrations tracking `_migrations` catalog table. |
| **`lionsql sql`** | `lionsql sql "<query>"` | Executes ad-hoc SQL/T-SQL queries against local storage without external client tools. |
| **`lionsql backup`** | `lionsql backup [target.tar.gz]` | Triggers live, point-in-time snapshot backup without blocking concurrent writes. |
| **`lionsql restore`** | `lionsql restore <source.tar.gz>` | Restores database files, catalog schemas, and WAL state from a snapshot archive. |
| **`lionsql doctor`** | `lionsql doctor` | Performs environment diagnostics: CPU/RAM budgets, port availability, WiredTiger C library check. |
| **`lionsql init-config`**| `lionsql init-config [target.json]` | Generates a clean `lionsql.json` configuration template with recommended defaults. |
| **`lionsql version`** | `lionsql version` *(or `-v`, `--version`)* | Displays release banner: `LionSQL 1.0 / v1.0.0 LTS`. |

---

## 5. Configuration & Flag Preservation Matrix

All existing server configurations and engine flags are **100% preserved and supported**:

| Flag | Default | Description | Smart Aliases |
| :--- | :--- | :--- | :--- |
| `-dev` | `false` | Enable developer mode (verbose wire query & parameter logging) | `--debug`, `-v`, `--verbose`, `--dev` |
| `-engine` | `wiredtiger` | Storage engine backend (`wiredtiger` or `memory`) | `--engine` |
| `-dir` | `data` | Database directory path (holds B+Trees, WAL, PID file) | `-data`, `--dir`, `--data` |
| `-http` | `127.0.0.1:8090` | HTTP API & Web Studio listen address | `--http`, `-port` |
| `-pg` | `127.0.0.1:5433` | PostgreSQL wire protocol listen address | `--pg` |
| `-tds` | `127.0.0.1:1433` | Microsoft SQL Server TDS protocol listen address | `--tds` |
| `-work-mem` | `64MB` | Memory budget per query operator before disk spill | `--work-mem` |
| `-max-memory` | `70% RAM` | Global memory governor circuit breaker threshold | `--max-memory` |
| `-max-workers` | `NumCPU * 4` | Max concurrent query execution workers | `--max-workers` |
| `-max-idle-sessions` | `64` | Max idle storage sessions in pool | `--max-idle-sessions` |
| `-seed` | `false` | Seed sample database (`scripts/demo_seed.sql`) on startup | `--seed` |
| `-auth` | **`true`** | Enforce JWT token authentication on REST endpoints | `--auth` |
| `-admin-email` | **`admin@lionsql.local`** | Default superuser email | `--admin-email` |
| `-admin-pass` | `1234567890` | Default superuser password (triggers reminder if unchanged) | `--admin-pass` |
| `-tls` | `false` | Enable TLS encryption on wire protocol listeners | `--tls` |
| `-tls-cert` | `""` | Path to TLS certificate file | `--tls-cert` |
| `-tls-key` | `""` | Path to TLS private key file | `--tls-key` |
| `-require-ssl` | `false` | Disallow plaintext connections; require SSL/TLS | `--require-ssl` |
| `-slow-log` | `""` | Path to slow query log file | `--slow-log` |
| `-slow-threshold-ms`| **`100.0`** | Threshold in milliseconds to record slow queries | `--slow-threshold-ms` |
| `-general-log` | `""` | Enable general query logging | `--general-log` |
| `-replica-of` | `""` | Primary server URL for 1-writer multi-reader WAL streaming | `--replica-of` |
| `-node-id` | Auto-UUID | Replication node identifier | `--node-id` |
| `-auto-failover` | `false` | Enable automatic failover on heartbeat timeout | `--auto-failover` |
| `-peers` | `""` | Comma-separated peer HTTP URLs | `--peers` |
| `-self-url` | `""` | Self HTTP URL for peer routing | `--self-url` |
| `-heartbeat-timeout-ms` | `3000` | Heartbeat failure detection timeout (ms) | `--heartbeat-timeout-ms` |
| `-election-timeout-ms` | `1500` | Leader election vote timeout (ms) | `--election-timeout-ms` |
| `-outbox` | `false` | Enable Transactional Outbox broker dispatcher | `--outbox` |
| `-outbox-table` | `""` | Filter outbox to specific table | `--outbox-table` |
| `-outbox-topic-prefix` | **`lionsql.`** | Prefix for outbox broker topics | `--outbox-topic-prefix` |
| `-outbox-webhook` | `""` | Webhook URL for outbox forwarding | `--outbox-webhook` |
| `-outbox-kafka` | `""` | Kafka REST Proxy endpoint | `--outbox-kafka` |
| `-outbox-rabbitmq`| `""` | RabbitMQ Management API endpoint | `--outbox-rabbitmq` |
| `-config` | `""` | Path to JSON config file | `--config` |
| `-init-config` | `""` | Generate default `lionsql.json` | `--init-config` |
| `-backup` | `""` | Snapshot backup target archive | `--backup` |
| `-restore` | `""` | Snapshot restore source archive | `--restore` |
| `-wal-archive` | `""` | WAL archive directory for PITR | `--wal-archive` |
| `-restore-time` | `""` | Target timestamp for PITR | `--restore-time` |
| `-restore-lsn` | `0` | Target LSN for PITR | `--restore-lsn` |
| `-archive-wal` | `""` | Archive specific WAL file and exit | `--archive-wal` |
| `-doctor` | `false` | Run self-diagnostic health check | `--doctor` |
| `-pidfile` | `<dir>/lionsql.pid` | Custom path to PID file | `--pidfile` |
| `-logfile` | `<dir>/lionsql.log` | Custom path to log file | `--logfile` |
| `-is-child-worker` | `false` | Internal daemon child process flag | `--is-child-worker` |

---

## 6. Technical Implementation Blueprint

### 6.1. Entry Point Architecture (`cmd/lionsql/`)
- **`cmd/lionsql/main.go`**: Central entry point. Inspects `os.Args[1]`.
  - If argument matches subcommands (`serve`, `start`, `stop`, `restart`, `status`, `migrate`, `sql`, `backup`, `restore`, `doctor`, `init-config`, `version`, `help`), delegates to corresponding command handler.
  - If argument starts with `-` or no arguments are provided, defaults to executing the server in foreground mode (`runServer(os.Args[1:])`).
- **`cmd/lionsql/server.go`**: Core server lifecycle implementation migrated from `cmd/server/main.go`. Manages listeners, engines, replication, outbox, and banners.
- **`cmd/lionsql/daemon.go`**: Background daemon management (`start`, `stop`, `restart`, `status`). Implements Golden Rule 4 (graceful shutdown with 1.5s escalation to SIGKILL) using `lionsql.pid`.
- **`cmd/lionsql/doctor.go`**: Environment diagnostics migrated from `cmd/server/doctor.go`.
- **`cmd/lionsql/backup_cmd.go`**: Online backup/restore CLI commands.

### 6.2. Legacy `cmd/server/main.go` Forwarder
`cmd/server/main.go` is maintained as a backward-compatibility wrapper:
- Emits a deprecation notice: `[Notice] 'bin/server' is deprecated and will be removed in 1.1; please use 'bin/lionsql'`
- Re-executes `bin/lionsql` or delegates to `cmd/lionsql` internal handlers directly with identical arguments.
- Guarantees 0% breakage for existing scripts, Dockerfiles, and systemd units during the transition period.

---

## 7. Step-by-Step Implementation Roadmap

| Step | Phase | Key Actions | Target Files |
| :---: | :--- | :--- | :--- |
| **Step 1** | **Global Defaults & Security Updates** | - Update `admin@sqlion.local` ➔ `admin@lionsql.local`<br>- Update `sqlion.json` ➔ `lionsql.json` (strict without silent fallback)<br>- Update `sqlion.pid`/`log` ➔ `lionsql.pid`/`log`<br>- Update `SlowThresholdMS` to 100.0ms<br>- Update `Auth.Enabled` default to `true` | `pkg/config/config.go`<br>`pkg/config/config_test.go`<br>`pkg/api/`<br>`pkg/auth/` |
| **Step 2** | **CLI Subcommand Router & Dispatcher** | - Implement PocketBase-style dispatcher (no-args/flags ➔ foreground server)<br>- Implement `serve`, `start`, `stop`, `restart`, `status`, `backup`, `restore`, `doctor`, `init-config`, `version` | `cmd/lionsql/main.go` |
| **Step 3** | **Server & Daemon Logic Migration** | - Port full server runner from `cmd/server/main.go` into `cmd/lionsql/`<br>- Add security warning banner for default admin password<br>- Port daemon fork/stop/status logic to `cmd/lionsql/daemon.go`<br>- Port doctor diagnostics to `cmd/lionsql/doctor.go` | `cmd/lionsql/server.go`<br>`cmd/lionsql/daemon.go`<br>`cmd/lionsql/doctor.go` |
| **Step 4** | **Legacy Wrapper & Forwarder** | - Refactor `cmd/server/main.go` to lightweight forwarder with deprecation banner | `cmd/server/main.go` |
| **Step 5** | **Testing & 3-Stage Pipeline Verification** | - Verify CLI lifecycle tests (foreground, start/status/stop)<br>- Run full build & test: `go build` + `go test -tags wiredtiger -race ./...` | `cmd/lionsql/*_test.go` |
| **Step 6** | **Documentation & Backlog Sync** | - Update `TODO.ko.md`, `ROADMAP.md`, `README.md`, `CHANGELOG.md` | Root documentation |
