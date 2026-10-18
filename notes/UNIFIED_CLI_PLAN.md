# LionSQL 1.0 Unified CLI Architecture & Integration Plan

## 1. Executive Summary

LionSQL is fundamentally architected as an **autonomous, zero-external-dependency standalone static executable** (GEMINI.md Section 5). 

Currently, the executable entry points are divided into two separate binaries:
- `bin/server`: Database engine server, wire protocol listeners, daemon control, backup/restore, and doctor diagnostics (`cmd/server/main.go`).
- `bin/lionsql`: CLI schema migration manager and one-off SQL executor (`cmd/lionsql/main.go`).

This duality compromises the single-binary value proposition, introduces a generic and colliding binary name (`server`), duplicates build targets, and fragments developer experience.

This plan details the comprehensive consolidation of all server and CLI capabilities into a single, canonical executable: **`lionsql`**.

---

## 2. Design Principles & Ergonomics

### 2.1. PocketBase / CockroachDB Model (Single Canonical Binary)
The unified CLI follows the battle-tested single-binary paradigm established by **PocketBase**, **CockroachDB**, **HashiCorp Vault**, and **SurrealDB**:
1. **Zero-Argument Default (Docker & Local Dev First)**:
   Running `lionsql` with no subcommands immediately launches the full database engine in the **foreground**, streaming logs to `stdout/stderr`. This provides zero-configuration instant startup and perfect Docker container compatibility (`ENTRYPOINT ["lionsql"]`).
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

## 3. Unified Command Specification & CLI Syntax Map

```text
lionsql [command] [options...]
```

### 3.1. Server Lifecycle Commands

| Command | Execution Mode | Description | Primary Use Case |
| :--- | :--- | :--- | :--- |
| **`lionsql`** *(no args)* | **Foreground** | Launches HTTP (:8090), PGWire (:5433), and TDS (:1433) in the active terminal. Terminate via `Ctrl+C`. | Local development, debugging, Docker containers, Kubernetes Pods. |
| **`lionsql serve`** | **Foreground** | Explicit subcommand alias for foreground launch (PocketBase compatibility). | Explicit deployment scripts, systemd service units. |
| **`lionsql start`** | **Background Daemon** | Spawns a detached background daemon, writes `<dir>/lionsql.pid`, and returns to the shell immediately. | Bare-metal Linux servers, macOS background service, developer workstations. |
| **`lionsql stop`** | **Control** | Reads PID from `<dir>/lionsql.pid`, dispatches graceful interrupt, and escalates to SIGKILL within 1.5s (Rule 4). | Host maintenance, service shutdown. |
| **`lionsql restart`** | **Control** | Executes `stop` followed by `start`, preserving all active CLI flags and configurations. | Configuration reload, service recycling. |
| **`lionsql status`** | **Diagnostic** | Reports daemon PID, uptime, active storage engine, and port listening status. | Operations health checks, automated watchdog scripts. |

### 3.2. Database Management & Developer Tools

| Command | Subcommands / Syntax | Description |
| :--- | :--- | :--- |
| **`lionsql migrate`** | `new <name>`<br>`up [-steps=N]`<br>`down [-steps=N]`<br>`status` | Declarative database schema migrations tracking `_migrations` catalog table. |
| **`lionsql sql`** | `lionsql sql "<query>"` | Executes ad-hoc SQL/T-SQL queries against local storage without requiring external client tools. |
| **`lionsql backup`** | `lionsql backup [target.tar.gz]` | Triggers live, point-in-time snapshot backup without blocking concurrent OLTP writes. |
| **`lionsql restore`** | `lionsql restore <source.tar.gz>` | Restores database files, catalog schemas, and WAL state from a snapshot archive. |
| **`lionsql doctor`** | `lionsql doctor` | Performs environment diagnostics: CPU/RAM budgets, port availability, WiredTiger C library check. |
| **`lionsql version`** | `lionsql version` *(or `-v`, `--version`)* | Displays release banner: `LionSQL 1.0 (2026 Release) / v1.0.0 LTS`. |

---

## 4. Configuration & Flag Preservation Matrix

All existing server configurations and engine flags are **100% preserved** without modification:

| Flag | Default | Description | Smart Aliases |
| :--- | :--- | :--- | :--- |
| `-dev` | `false` | Enable developer mode (verbose wire query & parameter logging) | `--debug`, `-v`, `--verbose` |
| `-engine` | `wiredtiger` | Storage engine backend (`wiredtiger` or `memory`) | - |
| `-dir` | `data` | Database directory path (holds B+Trees, WAL, PID file) | `-data`, `--dir` |
| `-http` | `127.0.0.1:8090` | HTTP API & Web Studio listen address | `--http` |
| `-pg` | `127.0.0.1:5433` | PostgreSQL wire protocol listen address | `--pg` |
| `-tds` | `127.0.0.1:1433` | Microsoft SQL Server TDS protocol listen address | `--tds` |
| `-work-mem` | `64MB` | Memory budget per query operator before disk spill | `--work-mem` |
| `-max-memory`| `70% RAM` | Global memory governor circuit breaker threshold | `--max-memory` |
| `-seed` | `false` | Seed sample database (`scripts/demo_seed.sql`) on startup | `--seed` |
| `-auth` | `false` | Enforce JWT token authentication on REST endpoints | `--auth` |
| `-tls` | `false` | Enable TLS encryption on wire protocol listeners | `--tls` |
| `-replica-of`| `""` | Primary server URL for 1-writer multi-reader WAL streaming | `--replica-of` |

---

## 5. Technical Implementation Blueprint

### 5.1. Entry Point Refactoring (`cmd/lionsql/main.go`)

```
                        [ ./bin/lionsql <args...> ]
                                     │
                    ┌────────────────┴────────────────┐
             (Has Subcommand)                 (No Subcommand or Flags)
                    │                                 │
         ┌──────────┼──────────┐                      ▼
         ▼          ▼          ▼             [ RunServerForeground ]
     [migrate]    [sql]     [doctor]         (Start listeners on
     [backup]    [version]  [start/stop]      :8090, :5433, :1433)
```

1. **Subcommand Dispatcher**:
   - Inspects `os.Args[1]`.
   - If `os.Args[1]` matches `"start"`, `"stop"`, `"restart"`, `"status"`, `"migrate"`, `"sql"`, `"backup"`, `"restore"`, `"doctor"`, `"version"`, or `"help"`, dispatches directly to the corresponding handler.
   - If `os.Args[1]` begins with `-` (flag) or no arguments are provided, defaults to executing the server in the foreground.
2. **Daemon Engine Consolidation**:
   - Incorporates the daemon fork logic (`syscall.ForkExec` / detached background process) into `cmd/lionsql/daemon.go`.
   - PID file location normalized to `<dir>/lionsql.pid` (with backward-compatible check for `server.pid`).
3. **Deprecation & Transition Strategy for `cmd/server`**:
   - `cmd/server/main.go` will be refactored into a lightweight forwarder:
     Prints a gentle notice (`Notice: 'server' is deprecated and will be removed in 1.1; please use 'lionsql'`) and delegates execution directly to `lionsql`.
   - This guarantees 0% breakage for any existing background scripts or container definitions.

---

## 6. Build Pipeline & Packaging Impact

### 6.1. Build Command Unification
- **Previous Build Command**:
  ```bash
  go build -tags wiredtiger -o bin/server ./cmd/server && go build -tags wiredtiger -o bin/lionsql ./cmd/lionsql
  ```
- **Unified 1.0 Build Command**:
  ```bash
  go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
  *(Reduces build time, disk footprints, and build artifact complexity by 50%.)*

### 6.2. Packaging Simplification (Homebrew / Docker / Debian)
- **Homebrew Formula**:
  Installs a single binary: `bin.install "bin/lionsql"`.
- **Dockerfile**:
  ```dockerfile
  ENTRYPOINT ["/usr/local/bin/lionsql"]
  CMD []
  ```
- **Systemd Service Unit**:
  ```ini
  [Service]
  ExecStart=/usr/local/bin/lionsql
  Restart=always
  ```

---

## 7. Step-by-Step Execution Plan

| Step | Scope | Target Files | Acceptance Criteria |
| :---: | :--- | :--- | :--- |
| **Step 1** | **Dispatcher Architecture** | `cmd/lionsql/main.go`, `cmd/lionsql/cli_dispatch.go` | Subcommand router handling `serve`, `start`, `stop`, `restart`, `status`, `migrate`, `sql`, `doctor`. |
| **Step 2** | **Server Logic Migration** | `cmd/lionsql/server_runner.go`, `daemon.go` | Migrate listeners, engine instantiation, and daemon control from `cmd/server/main.go`. |
| **Step 3** | **Flag Aliasing & Tolerant Parser** | `cmd/lionsql/flags.go` | `-dev`, `--debug`, `-v`, and dual tolerance (`lionsql stop` and `lionsql -stop`) passing 100%. |
| **Step 4** | **Legacy Server Forwarder** | `cmd/server/main.go` | `bin/server` invokes `bin/lionsql` with deprecation notice. |
| **Step 5** | **Documentation & Test Verification** | `README.md`, `README.ko.md`, `ROADMAP.md`, `TODO.ko.md` | Verification passing 100% across foreground, daemon, and tool commands with 0 data races. |
