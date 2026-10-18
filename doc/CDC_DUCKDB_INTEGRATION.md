# LionSQL Change Data Capture (CDC) & DuckDB Integration Guide

LionSQL is an enterprise-grade, high-concurrency embedded OLTP database engine designed to guarantee ACID durability and 16,000+ TPS write throughput. 

This document specifies the **decoupled Change Data Capture (CDC) integration architecture**, enabling external analytics applications (Python, BI dashboards, backend microservices) to embed DuckDB for real-time, sub-millisecond OLAP aggregations while keeping LionSQL's core OLTP engine lightweight, safe, and unaffected.

---

## 1. Architecture Overview (Decoupled Analytics Architecture)

```
[ OLTP Domain: LionSQL Server ]
   ├── Port :5433 (PostgreSQL Wire Protocol v3)
   ├── Port :1433 (Microsoft SQL Server TDS 7.x/8.0)
   └── Port :8090 (HTTP API & Realtime CDC Stream)
          │
          │ (Captures committed transactions across all wire protocols)
          ▼
   [ WAL / CDC Streamer ] ──(Ultra-low latency async stream)──┐
                                                               │
                                                               ▼
                           [ Analytics Domain: External Application ]
                              ├── Python (FastAPI / Streamlit / Pandas)
                              └── Go / Node.js Analytics Microservices
                                     │ (Subscribes to HTTP Stream: NDJSON / SSE)
                                     ▼
                               [ DuckDB (Embedded Engine) ]
                                     │ (In-memory or single-file columnar store)
                                     ▼
                               [ Sub-millisecond Vectorized Aggregations / OLAP ]
```

### Why a Decoupled Architecture?

1. **Failure & Resource Isolation (Zero Blast Radius)**:
   - Analytical queries performing multi-million row joins or full table scans that cause Out-of-Memory (OOM) conditions never crash or impact the LionSQL OLTP server.
2. **Lightweight Single Binary (KISS & Golden Rules 15, 36)**:
   - Avoids embedding heavy C++17 DuckDB dependencies inside the LionSQL static binary, preserving sub-second build times and binary minimalism (<50MB).
3. **Independent Analytics Scale-Out**:
   - Multiple independent analytics workers, data science notebooks, or ETL consumers can concurrently subscribe to the same LionSQL CDC endpoint without thread or lock contention in the primary database kernel.

---

## 2. LionSQL CDC Streaming API Specification

When the LionSQL server runs with the HTTP service enabled (`:8090` by default), a real-time CDC streaming endpoint is exposed:

- **Endpoint**: `GET /api/cdc/stream`
- **Query Parameters**:
  - `table`: Filter events for a specific table (e.g. `?table=orders`; omitted to subscribe to all tables)
  - `from_lsn`: Replay committed changes starting strictly after this Log Sequence Number (LSN) (defaults to `0` or current watermark)
  - `format`: Payload encoding format (`ndjson` by default, or `sse` for Server-Sent Events)

### Response Payload Format (NDJSON Example)

```json
{"lsn":101,"timestamp":1773574800000000000,"table":"orders","op":"INSERT","key":"ORD_001","data":{"id":1,"customer_id":42,"amount":"150.50","status":"COMPLETED"}}
{"lsn":102,"timestamp":1773574805000000000,"table":"orders","op":"UPDATE","key":"ORD_001","data":{"id":1,"customer_id":42,"amount":"150.50","status":"SHIPPED"},"base_ver":1}
{"lsn":103,"timestamp":1773574810000000000,"table":"orders","op":"DELETE","key":"ORD_001"}
```

---

## 3. Python + DuckDB Ingestion Worker Example (`duckdb_cdc_worker.py`)

The following complete Python worker consumes the LionSQL CDC stream using only the standard library and `duckdb`, requiring zero external sidecars (no Kafka, no Debezium, no Zookeeper):

```python
#!/usr/bin/env python3
import json
import urllib.request
import duckdb

def run_cdc_ingestion():
    # 1. Connect to local DuckDB instance (in-memory or persistent file)
    con = duckdb.connect("analytics.duckdb")

    # Initialize analytical destination table with primary key
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

    # 2. Connect to LionSQL CDC stream (subscribing to 'orders' table)
    url = "http://127.0.0.1:8090/api/cdc/stream?table=orders"
    print(f"[CDC Worker] Subscribing to LionSQL CDC stream: {url}")

    req = urllib.request.Request(url)
    with urllib.request.urlopen(req) as resp:
        for line in resp:
            if not line:
                continue
            line_str = line.decode("utf-8").strip()
            if not line_str or line_str.startswith(":"):
                continue  # Skip heartbeat ping frames

            event = json.loads(line_str)
            if event.get("type") == "ping":
                continue

            op = event.get("op")
            data = event.get("data", {})
            lsn = event.get("lsn")

            if op == "INSERT":
                con.execute("""
                    INSERT OR REPLACE INTO orders (id, customer_id, amount, status)
                    VALUES (?, ?, ?, ?)
                """, [data.get("id"), data.get("customer_id"), float(data.get("amount", 0)), data.get("status")])
                print(f"[CDC Event LSN:{lsn}] Inserted order id={data.get('id')}")

            elif op == "UPDATE":
                con.execute("""
                    UPDATE orders
                    SET customer_id = ?, amount = ?, status = ?
                    WHERE id = ?
                """, [data.get("customer_id"), float(data.get("amount", 0)), data.get("status"), data.get("id")])
                print(f"[CDC Event LSN:{lsn}] Updated order id={data.get('id')}")

            elif op == "DELETE":
                key = event.get("key")
                con.execute("DELETE FROM orders WHERE id = ?", [key])
                print(f"[CDC Event LSN:{lsn}] Deleted order key={key}")

if __name__ == "__main__":
    run_cdc_ingestion()
```

---

## 4. Terminal Verification with cURL

Once the LionSQL server is running, subscribe directly to live mutation events via command-line HTTP streaming:

```bash
# Stream all committed table mutation events
curl -N http://127.0.0.1:8090/api/cdc/stream

# Stream changes for a specific table (e.g. accounts) starting from LSN 100
curl -N "http://127.0.0.1:8090/api/cdc/stream?table=accounts&from_lsn=100"
```
