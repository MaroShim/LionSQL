# LionSQL CDC to DuckDB Streaming Worker

A zero-sidecar real-time streaming ingestion pipeline from LionSQL CDC to DuckDB analytical storage.

## Features
- **Initial Snapshot Bootstrap**: Automatically extracts pre-existing table baseline on first boot (`last_lsn == 0`).
- **Transactional LSN Checkpointing**: Atomically records `last_lsn` in `_lionsql_cdc_checkpoint` within each DuckDB transaction.
- **Resilient Auto-Reconnect**: Infinite reconnect loop with exponential backoff ($1\text{s} \to 2\text{s} \to \dots \max 30\text{s}$) resuming from `from_lsn={last_lsn}`.

## Requirements
```bash
pip install duckdb
```

## Running the Worker
```bash
python3 duckdb_cdc_worker.py
```
