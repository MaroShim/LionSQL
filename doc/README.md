# LionSQL Technical Documentation Directory (`doc/`)

> [Korean Version](README.ko.md)

Welcome to the official technical documentation directory for **LionSQL**, an autonomous, high-concurrency embedded OLTP database engine and modern Backend-as-a-Service (BaaS) packaged into a single standalone static binary.

---

## 1. Core Manuals (Essential Reading)

Primary authoritative references covering fundamentals, SQL/T-SQL specifications, engine architecture, and verified benchmarks.

* **[`USER_MANUAL.md`](USER_MANUAL.md)** ([Korean](USER_MANUAL.ko.md))
  * Comprehensive guide covering installation, startup, client tool connections, database development, and operations.
* **[`REFERENCE_MANUAL.md`](REFERENCE_MANUAL.md)** ([Korean](REFERENCE_MANUAL.ko.md))
  * Exhaustive reference for CLI flags, configurations, SQL grammar, data types, built-in functions, virtual catalog views, REST APIs, and wire protocol specifications.
* **[`ARCHITECTURE.md`](ARCHITECTURE.md)**
  * End-to-end architectural blueprint (dual storage engines, 2-Tier CBO/VDBE query runtime, goroutine connection pooler).
* **[`BENCHMARK.md`](BENCHMARK.md)**
  * Official verified benchmarks: TPC-C (1.04M tpmC), Banking SP (160k TPS), SQLLogicTest 107 full suite, and WiredTiger vs OCC comparisons.

---

## 2. Developer & Tooling Guides

Guides for client tools, query development, SQL ergonomics, and verification suites.

* **[`CLIENT_TOOL_INTEGRATION.md`](CLIENT_TOOL_INTEGRATION.md)**
  * Setup guide for SSMS, DBeaver, psql, Beekeeper Studio, Grafana, and other client tools.
* **[`SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.md`](SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.md)** ([Korean](SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md))
  * Multi-dialect SQL compatibility and developer ergonomics (MySQL backticks, `DESC`, `SHOW TABLES`, high-ROI functions).
* **[`SQLLOGICTEST_GUIDE.md`](SQLLOGICTEST_GUIDE.md)** ([Korean](SQLLOGICTEST_GUIDE.ko.md))
  * Execution and verification guide for SQLite official SQLLogicTest (550k+ queries) and Babelfish T-SQL procedural suite.
* **[`COMPLEX_QUERY_BENCHMARK_GUIDE.md`](COMPLEX_QUERY_BENCHMARK_GUIDE.md)** ([Korean](COMPLEX_QUERY_BENCHMARK_GUIDE.ko.md))
  * Benchmarking and tuning guide for Star Schema Benchmark (SSB) and Mini TPC-H complex queries.
* **[`WEB_STUDIO_GUIDE.md`](WEB_STUDIO_GUIDE.md)** ([Korean](WEB_STUDIO_GUIDE.ko.md))
  * Embedded Web Studio (`TCP :8090 /_/`) dashboard and Excel-style inline row creation guide.
* **Production Application Integration Guides (`guide/`)**:
  * Real-world guides for 10 open-source apps (Answer, Vaultwarden, Metabase, Umami, Woodpecker, Vikunja, Gitea, Grafana, Casdoor, IOTA ERP) are located in **[`guide/README.md`](../guide/README.md)**.

---

## 3. Operations, Security & Subsystems

Operational procedures, high availability, security hardening, APIs, and background services.

* **Operations & Monitoring**:
  * **[`OPERATIONS.md`](OPERATIONS.md)**: Production deployment, kernel tuning, and backup/restore workflows.
  * **[`HIGH_AVAILABILITY.md`](HIGH_AVAILABILITY.md)**: 1-Writer Multi-Reader live replication streaming and failover.
  * **[`METRICS.md`](METRICS.md)**: Prometheus (`/metrics`) telemetry metrics and internal diagnostics.
  * **[`TRANSACTION_PERFORMANCE_GUIDE.md`](TRANSACTION_PERFORMANCE_GUIDE.md)** ([Korean](TRANSACTION_PERFORMANCE_GUIDE.ko.md)): Concurrency tuning guide.
  * **[`RELEASE_AUTOMATION_GUIDE.md`](RELEASE_AUTOMATION_GUIDE.md)** ([Korean](RELEASE_AUTOMATION_GUIDE.ko.md)): 3-stage verification pipeline and release scripts.
* **Security & Governance**:
  * **[`SECURITY.md`](SECURITY.md)**: SQL RBAC Default Deny, TLS dynamic certificates, and isolation model.
  * **[`STORAGE_ENCRYPTION_GUIDE.md`](STORAGE_ENCRYPTION_GUIDE.md)**: Transparent Data Encryption (AES-256-CBC TDE).
  * **[`ENTERPRISE_AUTHENTICATION_SPI.md`](ENTERPRISE_AUTHENTICATION_SPI.md)**: Enterprise authentication SPI specification.
* **BaaS & Embedded Web Engine**:
  * **[`REST_API_REFERENCE.md`](REST_API_REFERENCE.md)** / **[`REST_API_RECORDS_GUIDE.md`](REST_API_RECORDS_GUIDE.md)**: REST JSON BaaS APIs.
  * **[`WEBHOOKS_AND_RLS.md`](WEBHOOKS_AND_RLS.md)**: Real-time webhooks and Row-Level Security (RLS).
  * **[`CRON_AND_MAILER.md`](CRON_AND_MAILER.md)**: Embedded cron scheduler and SMTP mailer.
  * **[`CDC_DUCKDB_INTEGRATION.md`](CDC_DUCKDB_INTEGRATION.md)**: Real-time CDC streaming.
* **Storage & Features**:
  * **[`RANGE_PARTITIONING.md`](RANGE_PARTITIONING.md)**: Automated rolling time-series range partitioning.
  * **[`JSON_INDEXING_GUIDE.md`](JSON_INDEXING_GUIDE.md)**: Semi-structured JSON B-Tree indexing.
  * **[`SCHEMA_MIGRATION_GUIDE.md`](SCHEMA_MIGRATION_GUIDE.md)** / **[`DATA_IMPORT_AND_MIGRATION.md`](DATA_IMPORT_AND_MIGRATION.md)**: Schema migrations.
  * **[`DATA_DICTIONARY.md`](DATA_DICTIONARY.md)**: System catalogs and virtual views.

---

## 4. Architectural History & Design Records (ADR & Knowledge Base)

* **[`ADR.md`](ADR.md)**: Architecture Decision Records (ADR-001 ~ ADR-022).
* **[`FOUNDATIONAL_DESIGN_DECISIONS.md`](FOUNDATIONAL_DESIGN_DECISIONS.md)** / **[`WHY_TSQL_GO_WIREDTIGER.md`](WHY_TSQL_GO_WIREDTIGER.md)**: Core technology selection rationales.
* **Developer Handover & Knowledge Base**:
  * Deep postmortems, negative knowledge, release notes, and architecture parking lot are located in **[`notes/README.md`](../notes/README.md)**.
