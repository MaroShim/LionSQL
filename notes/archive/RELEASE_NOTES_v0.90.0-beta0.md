# LionSQL v0.90.0-beta0 Release Notes (`notes/RELEASE_NOTES_v0.90.0-beta0.md`)

> - **Release Date**: 2026-10-19
> - **Version**: `v0.90.0-beta0`
> - **Theme**: **Feature Freeze Baseline, Embedded Web Studio Suite, Global Memory Governor, and Unified Single-Binary CLI**

---

## 1. Release Highlights

LionSQL `v0.90.0-beta0` marks the official **Feature Freeze Baseline** for the 1.0 production roadmap. This milestone consolidates LionSQL into an autonomous single standalone static executable (< 100MB), embeds the production-ready Web Management Studio, introduces the Global Memory Governor query circuit breaker, and hardens the zero-dependency REST BaaS API.

- **Unified Single-Binary CLI (`cmd/lionsql`)**: Consolidated server daemon and operational tooling into a single executable with modern subcommands (`serve`, `start`, `stop`, `restart`, `status`, `doctor`, `backup`, `restore`, `migrate`, `sql`).
- **Production Web Studio Suite (`UI-DDL-1`, `UI-REALTIME-1`, `UI-DATA-1`, `UI-LOGS-1`)**:
  - Visual column addition modal with dynamic type/constraint selection.
  - Real-time CDC / SSE live stream inspector with bounded circular buffer and pause/resume controls.
  - Multi-row bulk actions (delete, CSV/JSON export) and interactive foreign key cell navigation.
  - Thread-safe HTTP request activity logs viewer with latency profiling.
- **Global Memory Governor & Circuit Breaker (`MEM-1`)**: Dynamic global RAM allocation (up to 70% physical memory) and per-query bounds (`work_mem: 64MB`) halting runaway queries with descriptive SQLSTATE errors instead of triggering OS OOM panics.
- **Hardened HTTP REST & Web Management Port 8090 (`HTTP-1` ~ `HTTP-4`)**: Zero-dependency CORS, transparent Gzip compression (77% payload reduction), request body size limits (32MB), and 37 live socket scenario tests verified.
- **Enterprise Documentation Overhaul**: Published comprehensive User Manual (1,307 lines), Reference Manual (1,770 lines), Transaction Performance Guide, and Web Studio Guide.

---

## 2. Detailed Technical Improvements

### 2.1. Unified CLI & Autonomous Process Lifecycle
- Standardized foreground server execution by default when invoking `lionsql` without arguments.
- Added daemon process control, automated health inspection (`doctor`), and security warning banners when default administrative credentials remain in use.
- Established authoritative release version single source of truth in `pkg/config/version.go`.

### 2.2. Web Studio Management UI/UX Modernization
- Replaced 13 blocking browser `alert()` dialogs with non-intrusive animated Toast notifications.
- Added responsive layout support (`@media (max-width: 900px)`) with mobile drawer navigation.
- Implemented destructive Drop Table confirmation modal requiring exact table name confirmation.
- Built PocketBase-grade schema-aware Developer API Preview generating realistic typed dummy payloads for cURL, Fetch, Python, Go, and the official `lionsql.js` SDK.
- Added Light / Dark theme switcher with high-contrast WCAG AAA compliance and FOUC prevention.

### 2.3. HTTP Server Hardening & Security
- Zero-dependency global CORS middleware supporting preflight caching (`204 No Content`).
- Streaming-aware Gzip compression pooling writers via `sync.Pool`.
- Security headers enforced: `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`, and `Referrer-Policy: strict-origin-when-cross-origin`.

### 2.4. Stability & Shutdown Race Prevention
- Eliminated WiredTiger session close segmentation fault (`SIGSEGV: addr=0xad0`) during shutdown via `sync.WaitGroup` connection draining and atomic connection guards.
- Corrected long-lived SSE stream deadlines (`/api/realtime`, `/api/cdc/stream`) to prevent premature disconnection.
- Standardized virtual catalog listings on canonical protocol databases (`postgres` for PGWire, `master` for TDS).

---

## 3. Verification Scorecard

| Area | Scope | Verified Metrics | Status |
| :--- | :--- | :--- | :---: |
| **REST API Parity** | Port 8090 (37 scenarios) | 100% pass across health, auth, DDL, CRUD, SQL, CDC, metrics | **PASS** |
| **HTTP Concurrency** | Multiplexed traffic | 20 parallel workers, 200 concurrent requests, 0 data races | **PASS** |
| **Graceful Teardown** | Engine shutdown | 100% crash-free session pre-drain and CGO guard verification | **PASS** |
| **Asset Compression** | Web Studio payload | Transfer size reduced from 198KB to 45KB (~77% reduction) | **PASS** |

---

<details>
<summary><b>Detailed Changelog Delta</b></summary>

### Added
- Refined executive overview foregrounding LionSQL's five core architectural pillars in README
- Published independent Transactional Performance Optimization Guide, User Manual, and Reference Manual
- Published Web Studio User Manual covering access, dashboard, DDL, data grid, SQL console, and backups
- Visual Column Addition GUI in Schema Structure view (UI-DDL-1)
- Realtime CDC / SSE Stream Inspector Viewer with 200-event circular ring buffer (UI-REALTIME-1)
- Record Table Multi-Row Bulk Actions and Foreign Key Jump links (UI-DATA-1)
- Realtime HTTP API Request Activity Logs Viewer with latency metrics (UI-LOGS-1)
- Zero-dependency global CORS middleware with preflight fast-path (HTTP-1)
- Transparent HTTP Gzip response compression with sync.Pool writer pooling (HTTP-2)
- Request body size limit guard (32MB) to prevent memory starvation DOS (HTTP-3)
- Hardened default security response headers (nosniff, SAMEORIGIN) (HTTP-4)
- Modern non-intrusive Toast notification component replacing 13 alert() calls (UI-1)
- Responsive layout support with mobile drawer navigation (UI-2)
- Standardized administrative credentials on admin@lionsql.local (UI-3)
- Real-time instant search filter in sidebar for table and procedure catalogs (UI-4)
- Password visibility eye toggle button on login modal and SMTP settings (UI-5)
- Interactive Drop Table safety confirmation modal requiring exact name match (UI-6)
- Empty database validation on sample data seeding with 409 Conflict guard (UI-7)
- Dynamic schema-aware Developer API Preview modal generating typed dummy payloads (UI-8)
- Official LionSQL Web SDK tab with ready-to-use JavaScript/TypeScript snippets (UI-9)
- Light/Dark mode theme switcher with WCAG AAA contrast compliance (UI-10, UI-11)
- End-to-end HTTP integration test suite covering 37 scenarios across 8 domains (HTTP-TEST-1)
- Global Memory Governor allocating up to 70% RAM and query circuit breaker (MEM-1)
- Unified single-binary CLI with subcommands: serve, start, stop, restart, doctor, etc. (UNIFY-CLI)

### Fixed
- Standardized virtual catalog on canonical protocol databases (postgres, master) (CATALOG-CLEANUP)
- Configured infinite keep-alive deadlines for long-lived SSE streaming endpoints (SSE-STREAM-1)
- Prevented WiredTiger SIGSEGV crash on graceful shutdown via session pre-draining (SHUTDOWN-1)
- Overhauled Web Studio Light Mode contrast and alert boxes to WCAG AAA standards (UI-11)
- Fixed Web Studio view isolation and default active status indicators (UI-12)
</details>
