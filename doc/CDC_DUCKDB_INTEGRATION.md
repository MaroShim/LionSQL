# LionSQL Change Data Capture (CDC) & DuckDB Integration Guide

> - **Created**: 2026-10-14
> - **Last Updated**: 2026-11-21
> - [한국어](CDC_DUCKDB_INTEGRATION.ko.md)


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
  - `table`: Target table name to filter events (e.g. `?table=orders`). Required for snapshot generation.
  - `consumer_id`: Persistent consumer group or worker identifier (e.g. `?consumer_id=analytics_duckdb_1`). Enables server-side checkpointing. When omitted with `from_lsn`, starts from 0 or latest.
  - `from_lsn`: Replay committed changes starting strictly after this Log Sequence Number (LSN) (e.g. `?from_lsn=1000`). If omitted and `consumer_id` is supplied, resumes automatically from the last saved checkpoint.
  - `snapshot`: Baseline snapshot mode for existing table data:
    - `auto` (default): Emits an initial table baseline snapshot (`op="SNAPSHOT"`) if starting from `from_lsn <= 0` or fresh consumer, then seamlessly transitions to live CDC events.
    - `never`: Disables initial snapshot generation, streaming only new mutation events after `from_lsn`.
    - `always`: Forcefully dumps full table records as `SNAPSHOT` events before transitioning to live events.
  - `format`: Payload encoding format (`ndjson` by default, or `sse` for Server-Sent Events).

### Event Operations & Payload Format

The stream produces line-delimited JSON (`ndjson`) or Server-Sent Events (`sse`) with monotonic LSN sequencing:

| Operation (`op`) | Description | Included Fields |
|---|---|---|
| `SNAPSHOT` | Pre-existing baseline record emitted during initial table bootstrap | `lsn`, `timestamp`, `table`, `key`, `data` |
| `INSERT` | Newly committed row insertion | `lsn`, `timestamp`, `table`, `key`, `data` |
| `UPDATE` | Committed row update with modified column values | `lsn`, `timestamp`, `table`, `key`, `data`, `base_ver` |
| `DELETE` | Committed row deletion | `lsn`, `timestamp`, `table`, `key` |

#### Payload Examples (NDJSON)

```json
{"lsn":100,"timestamp":1773574700000000000,"table":"orders","op":"SNAPSHOT","key":"1","data":{"id":1,"customer_id":101,"amount":45.0,"status":"SHIPPED"}}
{"lsn":101,"timestamp":1773574800000000000,"table":"orders","op":"INSERT","key":"2","data":{"id":2,"customer_id":102,"amount":150.5,"status":"COMPLETED"}}
{"lsn":102,"timestamp":1773574805000000000,"table":"orders","op":"UPDATE","key":"2","data":{"id":2,"customer_id":102,"amount":150.5,"status":"REFUNDED"},"base_ver":1}
{"lsn":103,"timestamp":1773574810000000000,"table":"orders","op":"DELETE","key":"2"}
```

---

## 3. Production Python + DuckDB Ingestion Worker (`duckdb_cdc_worker.py`)

The following production-ready worker features:
1. **Initial Baseline Synchronization**: Safely ingests existing rows via `SNAPSHOT` events without duplicating subsequent live `INSERT` events.
2. **Persistent Checkpointing**: Tracks processed LSNs both locally and via server-side `consumer_id`, ensuring zero event loss across restarts.
3. **Automatic Reconnection with Exponential Backoff**: Resilient to temporary network interruptions or server reboots.
4. **Idempotent Upsert in DuckDB**: Uses `INSERT OR REPLACE` to guarantee deterministic state even on network replays.

```python
#!/usr/bin/env python3
"""
LionSQL to DuckDB Real-Time CDC Ingestion Worker
Requires: pip install duckdb
"""
import json
import logging
import os
import time
import urllib.request
import urllib.error
import duckdb

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s"
)
logger = logging.getLogger("CDC-DuckDB")

LIONSQL_BASE_URL = os.environ.get("LIONSQL_URL", "http://127.0.0.1:8090")
TARGET_TABLE = os.environ.get("CDC_TABLE", "orders")
CONSUMER_ID = os.environ.get("CDC_CONSUMER_ID", "duckdb_analytics_node1")
DUCKDB_PATH = os.environ.get("DUCKDB_PATH", "analytics.duckdb")
CHECKPOINT_FILE = os.environ.get("CHECKPOINT_FILE", f".checkpoint_{TARGET_TABLE}.lsn")

def init_duckdb():
    con = duckdb.connect(DUCKDB_PATH)
    con.execute(f"""
        CREATE TABLE IF NOT EXISTS {TARGET_TABLE} (
            id BIGINT PRIMARY KEY,
            customer_id BIGINT,
            amount DOUBLE,
            status VARCHAR,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    """)
    logger.info(f"Connected to DuckDB at '{DUCKDB_PATH}', table '{TARGET_TABLE}' ready.")
    return con

def load_local_checkpoint() -> int:
    if os.path.exists(CHECKPOINT_FILE):
        try:
            with open(CHECKPOINT_FILE, "r") as f:
                val = f.read().strip()
                if val.isdigit():
                    return int(val)
        except Exception as e:
            logger.warning(f"Could not read local checkpoint file: {e}")
    return 0

def save_local_checkpoint(lsn: int):
    tmp_path = f"{CHECKPOINT_FILE}.tmp"
    with open(tmp_path, "w") as f:
        f.write(str(lsn))
    os.replace(tmp_path, CHECKPOINT_FILE)

def process_stream():
    con = init_duckdb()
    last_lsn = load_local_checkpoint()
    logger.info(f"Starting CDC worker for '{TARGET_TABLE}'. Initial local LSN: {last_lsn}")

    backoff = 1.0
    max_backoff = 30.0

    while True:
        query_params = [
            f"table={TARGET_TABLE}",
            f"consumer_id={CONSUMER_ID}",
            "format=ndjson"
        ]
        # Pass explicit from_lsn if we have a locally saved checkpoint
        if last_lsn > 0:
            query_params.append(f"from_lsn={last_lsn}")
            query_params.append("snapshot=never")
        else:
            query_params.append("snapshot=auto")

        url = f"{LIONSQL_BASE_URL}/api/cdc/stream?{'&'.join(query_params)}"
        logger.info(f"Connecting to LionSQL CDC endpoint: {url}")

        try:
            req = urllib.request.Request(url, headers={"User-Agent": "LionSQL-DuckDB-Worker/1.0"})
            with urllib.request.urlopen(req, timeout=3600) as resp:
                backoff = 1.0  # Reset backoff on successful connection
                logger.info("CDC Stream connection established. Ingesting events...")

                for raw_line in resp:
                    if not raw_line:
                        continue
                    line = raw_line.decode("utf-8").strip()
                    if not line or line.startswith(":"):
                        continue  # Skip heartbeat ping lines

                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError:
                        logger.error(f"Malformed JSON payload: {line}")
                        continue

                    if event.get("type") == "ping":
                        continue

                    op = event.get("op")
                    lsn = event.get("lsn", 0)
                    data = event.get("data", {})

                    if op in ("SNAPSHOT", "INSERT"):
                        con.execute(f"""
                            INSERT OR REPLACE INTO {TARGET_TABLE} (id, customer_id, amount, status)
                            VALUES (?, ?, ?, ?)
                        """, [data.get("id"), data.get("customer_id"), float(data.get("amount", 0.0)), data.get("status")])

                    elif op == "UPDATE":
                        con.execute(f"""
                            UPDATE {TARGET_TABLE}
                            SET customer_id = ?, amount = ?, status = ?, updated_at = CURRENT_TIMESTAMP
                            WHERE id = ?
                        """, [data.get("customer_id"), float(data.get("amount", 0.0)), data.get("status"), data.get("id")])

                    elif op == "DELETE":
                        key = event.get("key")
                        con.execute(f"DELETE FROM {TARGET_TABLE} WHERE id = ?", [key])

                    if lsn > last_lsn:
                        last_lsn = lsn
                        save_local_checkpoint(last_lsn)

        except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, ConnectionResetError) as err:
            logger.warning(f"Connection lost ({err}). Reconnecting in {backoff:.1f}s...")
            time.sleep(backoff)
            backoff = min(backoff * 2.0, max_backoff)
        except Exception as e:
            logger.error(f"Unexpected error in ingestion loop: {e}", exc_info=True)
            time.sleep(backoff)
            backoff = min(backoff * 2.0, max_backoff)

if __name__ == "__main__":
    process_stream()
```

---

## 4. Checkpointing & Fault Tolerance Guarantee

LionSQL implements dual-layer checkpointing for CDC consumers:

1. **Server-Side Checkpoint Store (`FileCheckpointStore`)**:
   - When `consumer_id` is passed, LionSQL persists the highest delivered LSN in `checkpoints.json` inside the instance data directory.
   - On disconnect and reconnect with the same `consumer_id`, LionSQL automatically resumes streaming from that LSN, preventing re-transmission of the initial snapshot.
2. **Client-Side Atomic Checkpoint File (`.checkpoint_<table_name>.lsn`)**:
   - The worker persists `last_lsn` atomically via temporary file writes and renames (`[RULE:AtomicSaves]`).
   - Even if the server state is cleared or migrated, the worker can pass `from_lsn=<last_lsn>` to guarantee contiguous event consumption.
3. **Idempotent Destination State**:
   - Using `INSERT OR REPLACE` (or `UPSERT`) in DuckDB guarantees that even in extreme scenarios where an event is redelivered (at-least-once delivery), the analytical dataset remains 100% deterministic and free of duplicated records.

---

## 5. Terminal Verification with cURL

Once the LionSQL server is running, subscribe directly to live mutation events via command-line HTTP streaming:

```bash
# 1. Connect with consumer_id to receive snapshot + live stream
curl -N "http://127.0.0.1:8090/api/cdc/stream?table=orders&consumer_id=test_worker_1"

# 2. Replay changes starting strictly after LSN 500 without snapshot
curl -N "http://127.0.0.1:8090/api/cdc/stream?table=orders&from_lsn=500&snapshot=never"

# 3. Stream in standard Server-Sent Events (SSE) format
curl -N "http://127.0.0.1:8090/api/cdc/stream?table=orders&format=sse"
```

---

## Related Documentation

- [LionSQL User Manual](USER_MANUAL.ko.md)
- [LionSQL Auto-REST Records Guide](REST_API_RECORDS_GUIDE.md)
- [LionSQL Web Studio Guide](WEB_STUDIO_GUIDE.md)

