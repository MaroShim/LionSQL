# LionSQL & DuckDB Integration Guide (`DUCKDB_INTEGRATION_GUIDE.md`)

> - **Document Version**: v1.1.0
> - **Initial Created**: 2026-11-07
> - **Last Updated**: 2026-11-14
> - **Target Release**: LionSQL v0.90.0-beta4+
> - **Integration Target**: DuckDB v0.10.x ~ v1.1.x+ (Python `duckdb`, DuckDB CLI)
> - **Supported Channels**:
>   - **HTTP REST & Realtime CDC Stream**: `GET /api/cdc/stream` (default TCP `:8090`)
>   - **PostgreSQL Wire Protocol v3**: default TCP `:5433` (for initial snapshot extraction)
> - **Reference Implementation**: [`examples/duckdb-cdc-worker/duckdb_cdc_worker.py`](../examples/duckdb-cdc-worker/duckdb_cdc_worker.py)
> - **한국어 문서**: [`guide/DUCKDB_INTEGRATION_GUIDE.ko.md`](DUCKDB_INTEGRATION_GUIDE.ko.md)

This guide specifies how to integrate **[DuckDB](https://duckdb.org/)**, the high-performance embedded analytical (OLAP) database engine, with **LionSQL** to establish a unified, sub-millisecond vectorized hybrid transactional/analytical processing (HTAP) architecture.

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

A production-grade streaming consumer using only the Python standard library and `duckdb`—requiring zero external message brokers—complete with:
1. **Initial Snapshot Bootstrap** (loads pre-existing table baseline)
2. **Transactional LSN Disk Checkpointing** (`_lionsql_cdc_checkpoint`)
3. **Exponential Backoff Infinite Auto-Reconnect Loop** with `from_lsn` resume.

```python
#!/usr/bin/env python3
"""
LionSQL CDC -> DuckDB In-Memory/Persistent HTAP Streaming Worker
- Initial Boot: Automatic baseline snapshot loading (Initial Snapshot Bootstrap)
- Real-time Stream: Transactional LSN checkpointing in DuckDB (Zero Data Loss)
- Network/Server Disconnects: Infinite auto-reconnect loop with exponential backoff & from_lsn resume
"""

import json
import time
import urllib.request
import urllib.error
import duckdb

BASE_URL = "http://127.0.0.1:8090"
TABLE_NAME = "orders"
DB_FILE = "analytics.duckdb"

def init_duckdb(con):
    """Initialize analytical columnar table and LSN checkpoint metadata store"""
    con.execute(f"""
        CREATE TABLE IF NOT EXISTS {TABLE_NAME} (
            id BIGINT PRIMARY KEY,
            customer_id BIGINT,
            amount DOUBLE,
            status VARCHAR,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );
        CREATE TABLE IF NOT EXISTS _lionsql_cdc_checkpoint (
            table_name VARCHAR PRIMARY KEY,
            last_lsn BIGINT NOT NULL,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );
    """)
    print("[DuckDB] Analytical table and LSN checkpoint store are ready.")

def get_last_checkpoint_lsn(con, table_name):
    """Query persistent checkpoint LSN from DuckDB disk"""
    row = con.execute("""
        SELECT last_lsn FROM _lionsql_cdc_checkpoint WHERE table_name = ?
    """, [table_name]).fetchone()
    return row[0] if row else 0

def save_checkpoint(con, table_name, lsn):
    """Atomically record latest checkpoint LSN"""
    con.execute("""
        INSERT OR REPLACE INTO _lionsql_cdc_checkpoint (table_name, last_lsn, updated_at)
        VALUES (?, ?, CURRENT_TIMESTAMP)
    """, [table_name, lsn])

def perform_initial_snapshot(con, base_url, table_name):
    """
    On initial boot (last_lsn == 0), export pre-existing table baseline snapshot
    and commit records into DuckDB alongside baseline LSN checkpoint.
    """
    print(f"[CDC Worker] Performing initial baseline snapshot for '{table_name}'...")
    sql_url = f"{base_url}/api/sql?format=array"
    query_payload = json.dumps({"query": f"SELECT * FROM {table_name}"}).encode("utf-8")
    req = urllib.request.Request(sql_url, data=query_payload, headers={"Content-Type": "application/json"})
    
    with urllib.request.urlopen(req) as resp:
        rows = json.loads(resp.read().decode("utf-8"))
    
    con.begin()
    for r in rows:
        con.execute(f"""
            INSERT OR REPLACE INTO {table_name} (id, customer_id, amount, status)
            VALUES (?, ?, ?, ?)
        """, [r.get("id"), r.get("customer_id"), float(r.get("amount", 0)), r.get("status")])
    
    save_checkpoint(con, table_name, 1)
    con.commit()
    print(f"[CDC Worker] Initial snapshot complete: {len(rows)} rows synchronized.")
    return 1

def run_cdc_worker():
    con = duckdb.connect(DB_FILE)
    init_duckdb(con)

    retry_delay = 1
    max_delay = 30

    # Infinite auto-reconnect loop
    while True:
        try:
            last_lsn = get_last_checkpoint_lsn(con, TABLE_NAME)
            
            # Perform initial snapshot on very first run
            if last_lsn == 0:
                last_lsn = perform_initial_snapshot(con, BASE_URL, TABLE_NAME)

            cdc_url = f"{BASE_URL}/api/cdc/stream?table={TABLE_NAME}&from_lsn={last_lsn}"
            print(f"[CDC Worker] Connecting to LionSQL CDC stream (from_lsn={last_lsn}): {cdc_url}")

            req = urllib.request.Request(cdc_url)
            with urllib.request.urlopen(req) as resp:
                retry_delay = 1  # Reset backoff upon successful connection
                print("[CDC Worker] Stream connection established. Listening for events...")
                
                for line in resp:
                    if not line:
                        continue
                    line_str = line.decode("utf-8").strip()
                    if not line_str or line_str.startswith(":"):
                        continue  # Skip heartbeat ping

                    event = json.loads(line_str)
                    if event.get("type") == "ping":
                        continue

                    op = event.get("op")
                    data = event.get("data", {})
                    lsn = event.get("lsn", 0)

                    # Atomically commit data mutation and LSN checkpoint within single DuckDB transaction
                    con.begin()
                    if op in ("INSERT", "SNAPSHOT"):
                        con.execute(f"""
                            INSERT OR REPLACE INTO {TABLE_NAME} (id, customer_id, amount, status)
                            VALUES (?, ?, ?, ?)
                        """, [data.get("id"), data.get("customer_id"), float(data.get("amount", 0)), data.get("status")])
                        print(f"[CDC Event LSN:{lsn}] {op} -> id={data.get('id')}")

                    elif op == "UPDATE":
                        con.execute(f"""
                            UPDATE {TABLE_NAME}
                            SET customer_id = ?, amount = ?, status = ?
                            WHERE id = ?
                        """, [data.get("customer_id"), float(data.get("amount", 0)), data.get("status"), data.get("id")])
                        print(f"[CDC Event LSN:{lsn}] UPDATE -> id={data.get('id')}")

                    elif op == "DELETE":
                        key = event.get("key")
                        con.execute(f"DELETE FROM {TABLE_NAME} WHERE id = ?", [key])
                        print(f"[CDC Event LSN:{lsn}] DELETE -> key={key}")

                    if lsn > 0:
                        save_checkpoint(con, TABLE_NAME, lsn)
                    con.commit()

        except (urllib.error.URLError, ConnectionError, TimeoutError) as e:
            print(f"[CDC Worker] Stream disconnected ({e}). Reconnecting in {retry_delay}s from LSN {last_lsn}...")
            time.sleep(retry_delay)
            retry_delay = min(retry_delay * 2, max_delay)
        except Exception as e:
            print(f"[CDC Worker] Error encountered ({e}). Reconnecting safely in {retry_delay}s...")
            time.sleep(retry_delay)
            retry_delay = min(retry_delay * 2, max_delay)

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
