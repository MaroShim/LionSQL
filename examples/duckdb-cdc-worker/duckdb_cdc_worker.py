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
