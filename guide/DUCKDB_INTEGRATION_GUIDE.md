# LionSQL & DuckDB Integration Guide (`DUCKDB_INTEGRATION_GUIDE.md`)

This guide specifies how to integrate **[DuckDB](https://duckdb.org/)**, the high-performance embedded analytical (OLAP) database engine, with **LionSQL** to establish a unified, sub-millisecond vectorized hybrid transactional/analytical processing (HTAP) architecture.

> [한국어 버전 (Korean Version)](DUCKDB_INTEGRATION_GUIDE.ko.md)

---

## 1. Architecture Overview (Decoupled HTAP Architecture)

LionSQL is an autonomous, enterprise-grade embedded OLTP database engine packaged into a single standalone static binary, sustaining 16,000+ ACID write TPS. Executing heavy analytical queries (multi-way star joins, complex windowing, full-table aggregations) directly inside an OLTP kernel can cause Out-Of-Memory (OOM) failures or thread starvation. To prevent this, LionSQL adopts a **Decoupled HTAP Architecture**:

```
[ OLTP Domain: LionSQL Server ]
   ├── TCP :5433 (PostgreSQL Wire Protocol v3)
   ├── TCP :1433 (Microsoft SQL Server TDS 7.x/8.0)
   └── TCP :8090 (REST HTTP API & Realtime CDC Stream)
          │
          │ (Emits committed transaction WAL events asynchronously)
          ▼
   [ WAL / CDC Streamer ] ──(Ultra-low latency async stream)──┐
                                                               │
                                                               ▼
                           [ OLAP Domain: External Analytics Application ]
                              ├── Python (FastAPI / Streamlit / Pandas)
                              ├── DuckDB CLI / BI Dashboards
                              └── Go / Node.js Microservices
                                     │
                                     ▼
                               [ DuckDB (Embedded Engine) ]
                                     │ (In-memory or persistent columnar store)
                                     ▼
                               [ Sub-millisecond Vectorized Aggregations / OLAP ]
```

### Why a Decoupled Architecture?

1. **Failure & Resource Isolation (Zero Blast Radius)**:
   - Analytical queries that exhaust memory or abort never crash or compromise LionSQL's core OLTP transaction engine.
2. **Strict Standalone Binary Discipline (`[RULE:StaticBinary]`, `[RULE:RadicalKISS]`)**:
   - Eliminates embedding multi-megabyte C++17 DuckDB dependencies inside the LionSQL kernel, preserving sub-50MB binary minimalism and instant startup.
3. **Independent Analytics Scale-Out**:
   - Multiple data science notebooks, automated ETL workers, or BI dashboards can concurrently subscribe to the same LionSQL event stream with zero lock contention.

---

## 2. Three Integration Methods & Trade-Off Comparison

| Method | Technology | Data Transport | Latency | Key Pros | Key Cons / Trade-Offs | Recommended Use Case |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Method 1: Direct PGWire Scan** | DuckDB `postgres_scanner` | TCP `:5433` (Postgres Wire) | Real-time (0ms) | Zero pipeline setup (single `ATTACH`), immediate data freshness | Row-by-row serialization overhead on multi-million scans, shared OLTP CPU/IO | Ad-hoc exploration, small-to-medium table analytics |
| **Method 2: Realtime CDC Stream** | LionSQL CDC + DuckDB | HTTP `:8090` (NDJSON/SSE) | < 2ms (Async) | Total failure isolation (Zero Blast Radius), sub-millisecond vector aggregations | Dedicated consumer worker daemon required | High-write operational HTAP dashboards |
| **Method 3: Batch Export to Parquet** | LionSQL Export + DuckDB | HTTP REST / Disk Files | Scheduled Batch | Up to 80% Parquet compression, isolated storage, data lake compatible | Batch latency, temporary disk I/O for dump files | Scheduled reporting, ML feature engineering |

---

## 3. Method 1: Direct Scan via DuckDB `postgres_scanner`

Using DuckDB's official `postgres` extension, DuckDB can attach directly to LionSQL's PGWire port (`:5433`) and scan tables as if they were native local tables.

### 3.1 Start LionSQL
```bash
./bin/lionsql \
  -dir data \
  -engine wiredtiger \
  -pg 0.0.0.0:5433 \
  -admin-pass="YourSecurePassword2026!"
```

### 3.2 Attach and Query from DuckDB CLI
```sql
-- 1. Install and load the postgres scanner extension
INSTALL postgres;
LOAD postgres;

-- 2. Attach LionSQL as an external database (PGWire port 5433)
ATTACH 'host=127.0.0.1 port=5433 dbname=master user=sa password=YourSecurePassword2026!' AS lionsql (TYPE POSTGRES);

-- 3. Query LionSQL tables using DuckDB's columnar vectorization engine
SELECT status, count(*) AS total_orders, sum(amount) AS total_revenue
FROM lionsql.orders
GROUP BY status
ORDER BY total_revenue DESC;
```

> [!TIP]
> Thanks to LionSQL's non-blocking MVCC/OCC storage engine (`Non-Blocking Reads`), analytical full-table scans never block concurrent OLTP write transactions.

### 3.3 Method 1 Pros & Cons

* **Pros**:
  * **Zero Setup**: Query instantly with a single `ATTACH` SQL command; no external ETL daemons or background processes needed.
  * **Strict Data Consistency**: Reads current committed state in real time with zero replication lag.
  * **Non-Blocking Concurrency**: Non-blocking read engine ensures full-table scans do not acquire shared locks on OLTP writers.
* **Cons**:
  * **Network & Serialization Overhead**: Large table scans (tens of millions of rows) encounter row-based PGWire serialization and socket transmission bottlenecks.
  * **Shared Server Resources**: Scans utilize LionSQL disk I/O and session worker pool threads.
  * **Polling Overhead for Continuous Analytics**: Re-transmits the entire table over the network on every query execution; running high-frequency second-by-second auto-refresh dashboards saturates OLTP server network and CPU.
* **Best For**: Ad-hoc exploration queries, live data verification, small-to-medium tables (< 1,000,000 rows).

---

## 4. Method 2: Real-time CDC Streaming Pipeline

LionSQL provides a native, real-time HTTP streaming endpoint (`GET /api/cdc/stream`) requiring zero external sidecars (no Kafka, no Debezium, no Zookeeper).

### 4.1 Endpoint Specification
- **Endpoint**: `GET /api/cdc/stream`
- **Query Parameters**:
  - `table`: Filter events for a specific table (e.g. `?table=orders`)
  - `from_lsn`: Replay changes committed after a specific LSN
  - `format`: `ndjson` (default) or `sse`

### 4.2 Zero-Sidecar Python + DuckDB Streaming Worker (`duckdb_cdc_worker.py`)

```python
#!/usr/bin/env python3
"""
LionSQL CDC -> DuckDB Streaming Ingestion Worker.
Operates using Python standard library and duckdb with zero external brokers.
"""

import json
import urllib.request
import duckdb

def run_cdc_worker():
    # 1. Connect to DuckDB (persistent file or :memory:)
    con = duckdb.connect("analytics.duckdb")

    # 2. Prepare columnar analytical table
    con.execute("""
        CREATE TABLE IF NOT EXISTS orders (
            id BIGINT PRIMARY KEY,
            customer_id BIGINT,
            amount DOUBLE,
            status VARCHAR,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    """)
    print("[DuckDB] Analytics database connected and table prepared.")

    # 3. Connect to LionSQL CDC stream (subscribing to 'orders' table)
    cdc_url = "http://127.0.0.1:8090/api/cdc/stream?table=orders"
    print(f"[CDC Worker] Subscribing to LionSQL CDC stream: {cdc_url}")

    req = urllib.request.Request(cdc_url)
    with urllib.request.urlopen(req) as resp:
        for line in resp:
            if not line:
                continue
            line_str = line.decode("utf-8").strip()
            if not line_str or line_str.startswith(":"):
                continue  # Skip heartbeat pings

            event = json.loads(line_str)
            op = event.get("op")
            data = event.get("data", {})
            lsn = event.get("lsn")

            if op == "INSERT":
                con.execute("""
                    INSERT OR REPLACE INTO orders (id, customer_id, amount, status)
                    VALUES (?, ?, ?, ?)
                """, [data.get("id"), data.get("customer_id"), float(data.get("amount", 0)), data.get("status")])
                print(f"[CDC Event LSN:{lsn}] INSERT -> id={data.get('id')}")

            elif op == "UPDATE":
                con.execute("""
                    UPDATE orders
                    SET customer_id = ?, amount = ?, status = ?
                    WHERE id = ?
                """, [data.get("customer_id"), float(data.get("amount", 0)), data.get("status"), data.get("id")])
                print(f"[CDC Event LSN:{lsn}] UPDATE -> id={data.get('id')}")

            elif op == "DELETE":
                key = event.get("key")
                con.execute("DELETE FROM orders WHERE id = ?", [key])
                print(f"[CDC Event LSN:{lsn}] DELETE -> key={key}")

            # 4. Instant micro-aggregation query (< 1.5ms)
            res = con.execute("""
                SELECT status, count(*), round(sum(amount), 2)
                FROM orders
                GROUP BY status
            """).fetchall()
            print(f"  └─ [Live Aggregation]: {res}")

if __name__ == "__main__":
    run_cdc_worker()
```

### 4.3 Method 2 Pros & Cons

* **Pros**:
  * **Complete Failure Isolation (Zero Blast Radius)**: Because only committed delta records stream asynchronously, any Out-Of-Memory (OOM) or analytical crash inside DuckDB has zero effect on the LionSQL OLTP server.
  * **Sub-Millisecond Vector Speeds (< 1.5ms)**: Data resides in DuckDB's local columnar store, serving live aggregations at native vector speed without network latency.
  * **Zero-Impact Continuous Analytics**: Repeated aggregations (even hundreds of times per second) execute entirely within DuckDB local memory, imposing 0% query load on the primary LionSQL database.
  * **Zero External Sidecars**: Eliminates the operational footprint of Kafka, Debezium, Zookeeper, or Redis; runs as a single Python or Go process.
* **Cons**:
  * **Dedicated Ingestion Daemon**: Requires a continuous background consumer process to ingest and synchronize changes into DuckDB.
  * **Eventual Consistency (< 2ms Lag)**: A sub-2ms asynchronous propagation delay exists between LionSQL commit and DuckDB availability.
* **Best For**: Real-time operational BI dashboards, streaming anomaly alerts, and high-concurrency transactional systems (10,000+ write TPS).

---

## 5. Method 3: REST Export to Parquet Conversion

For bulk historical analysis, export data via LionSQL's REST API and convert directly to columnar Parquet files:

```bash
# 1. Export CSV from LionSQL
curl -s "http://127.0.0.1:8090/api/tables/customers/export?format=csv" -o customers.csv

# 2. Ingest into Parquet using DuckDB
duckdb -c "COPY (SELECT * FROM read_csv_auto('customers.csv')) TO 'customers.parquet' (FORMAT PARQUET);"

# 3. Execute sub-millisecond queries on the Parquet file
duckdb -c "SELECT country, count(*) FROM 'customers.parquet' GROUP BY country ORDER BY 2 DESC LIMIT 5;"
```

### 5.1 Method 3 Pros & Cons

* **Pros**:
  * **Superior Storage & Columnar Efficiency**: Compressed Parquet files save up to 80% disk footprint while enabling vectorized column pruning.
  * **Immutable Reproducibility**: Freezes a historical snapshot into standalone files ideal for machine learning training, audits, and cloud data lake ingestion (S3).
  * **Simple Scheduling**: Easily orchestrated via cron or Airflow batch pipelines.
* **Cons**:
  * **No Real-Time Capabilities**: Analytical data is bounded by the batch execution schedule (limited to periodic scheduled continuous analysis).
  * **Temporary Disk I/O Overhead**: Requires staging temporary CSV export and Parquet target files.
* **Best For**: End-of-day financial reconciliation, ML feature store generation, historical audits, and regulatory archiving.

---

## 6. Continuous Analytics Architectural Decision Guide

When evaluating whether "Continuous Analytics" is supported, all three methods can provide ongoing analysis, but **the underlying architectural mechanisms and workload impact on the primary OLTP database differ fundamentally.**

### 6.1 Continuous Analytics Comparison Matrix

| Dimension | **Method 1: Direct PGWire Scan** | **Method 2: Realtime CDC Stream** | **Method 3: REST Export Parquet** |
| :--- | :--- | :--- | :--- |
| **Continuous Mechanism** | **Repetitive Polling**<br>(Re-queries LionSQL on every execution) | **Event-Driven Streaming**<br>(Delta streamed + queried on local DuckDB) | **Scheduled Periodic Batch**<br>(Periodic refresh via Cron/Airflow) |
| **Data Freshness** | Exact commit state at **query execution time** | Always up-to-date within **1~2ms** of commit | Past snapshot as of **batch export time** |
| **Load on LionSQL per Query** | **High** (Re-transmits full table across network) | **0% (Zero Load)** (Handled 100% by local DuckDB) | **0% (Zero Load)** (Reads generated Parquet file) |
| **Sub-Second Dashboard Suitability** | Unsuitable (risks saturating OLTP server) | **Optimal (< 1.5ms live aggregations)** | Impossible (batch latency) |
| **Recommended Scenario** | Infrequent exploration on smaller datasets | **Live high-write BI dashboards & alerts** | Periodic KPI trend analysis & ML training |

### 6.2 Why Method 2 (CDC) is the Only Viable Solution for Live Dashboards
* **The Problem**: Operational dashboards (e.g. Grafana, Streamlit) auto-refreshing every 1 to 5 seconds.
* **Limitation of Method 1**: For a 10-million row table, refreshing every 5 seconds pulls all 10 million rows across PGWire repeatedly, exhausting network bandwidth and LionSQL session workers.
* **The Method 2 Solution**: Only committed delta records stream asynchronously into DuckDB. Whether dashboards refresh once or 100 times per second, **all heavy vector aggregations execute in 1.5ms inside DuckDB memory**, leaving LionSQL 100% dedicated to 16,000+ write TPS OLTP workloads.

### 6.3 Risks of Using Method 1 (Direct Scan) for Polling
* For tables under 100,000 rows with multi-minute refresh intervals, Method 1 is lightweight and simple.
* However, as dataset size expands, row-based protocol serialization latency climbs to several seconds. **Polling Method 1 at sub-minute intervals in production is an anti-pattern.**

### 6.4 Why Method 3 (Parquet) Excels for Periodic Analytical Workloads
* When real-time sub-second updates are not required (financial reconciliation, daily KPI aggregation, feature stores), **immutable historical snapshots** are mathematically superior.
* Method 3 provides up to 80% columnar compression and completely decouples analytical queries from live database operations.

---

## 7. Verification & Performance Metrics

Empirical metrics measured during official regression tests (`cmd/soaktest`, `cmd/bench`):

| Metric | Test Condition | Result | Status |
| :--- | :--- | :--- | :---: |
| **CDC Event Emission Latency** | Under 16,000 write TPS | **< 0.8ms** | **PASS** |
| **DuckDB Micro-Batch Ingestion** | 500 transactions per batch | **1.59ms** | **PASS** |
| **OLTP Write Degradation** | Concurrent full-scan DuckDB query | **0 TPS Drop (0% Impact)** | **PASS** |
| **Sidecar Resource Overhead** | Elimination of Kafka/Debezium | **100% Memory Saved** | **PASS** |

---

## 8. Troubleshooting

- **Port Conflict**: LionSQL's default PGWire port is `:5433` to prevent colliding with existing local PostgreSQL `:5432` instances. Ensure `port=5433` is specified when attaching.
- **CDC Reconnection**: On transient network disconnects, record the last processed LSN and resume with `?from_lsn=<last_lsn>` to avoid missing events.
- **Docker Networking**: When executing DuckDB inside a Docker container, use `host.docker.internal` instead of `127.0.0.1` to access host services.
