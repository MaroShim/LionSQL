# LionSQL Troubleshooting, Error Diagnostics & FAQ

This guide provides actionable solutions for common deployment, connectivity, query optimization, and storage engine issues encountered when operating LionSQL.

---

## 1. Startup & Port Binding Issues

### 1.1. `bind: address already in use`

#### Symptom
```
Failed to start PostgreSQL wire protocol listener: listen tcp 127.0.0.1:5433: bind: address already in use
```

#### Cause
Another process (or an existing instance of PostgreSQL or LionSQL) is already bound to port `5433`, `1433`, or `8090`.

#### Resolution
1. Identify the occupying process:
   ```bash
   lsof -i :5433
   lsof -i :1433
   lsof -i :8090
   ```
2. Terminate the stale process, or assign alternative ports using CLI flags:
   ```bash
   ./bin/server -pg=127.0.0.1:5434 -tds=127.0.0.1:1434 -http=127.0.0.1:8091
   ```

---

### 1.2. `wiredtiger_open failed`

#### Symptom
```
[LionSQL] Failed to open WiredTiger storage engine: wiredtiger_open failed with code 2: No such file or directory
```

#### Cause
1. Target data directory does not exist or lacks write permissions.
2. Missing WiredTiger native shared libraries.
3. Stale WiredTiger lock file from an ungraceful system halt (`WiredTiger.lock`).

#### Resolution
1. Ensure write permissions on the data directory:
   ```bash
   mkdir -p data
   chmod 755 data
   ```
2. Install WiredTiger development headers:
   - macOS: `brew install wiredtiger`
   - Linux: `sudo apt-get install -y libwiredtiger-dev`
3. If native libraries are unavailable in your environment, switch to the pure-Go engine:
   ```bash
   ./bin/server -engine=memory
   ```

---

## 2. Concurrency & Storage Engine Errors

### 2.1. `serialization failure: concurrent update conflict`

#### Symptom
```
Error: serialization failure: concurrent update conflict
```

#### Cause
Occurs when using the in-memory engine (`-engine=memory`). Two concurrent transactions read the same row version and both attempted to update or delete it. Under Optimistic Concurrency Control (OCC), the first commit succeeds and the second transaction fails validation to preserve ACID serializability.

#### Resolution
Implement an application-level optimistic retry loop with exponential backoff:

```go
for attempt := 0; attempt < 3; attempt++ {
    err := runTransaction(db)
    if err == nil {
        break
    }
    if strings.Contains(err.Error(), "serialization failure") {
        time.Sleep(time.Duration(10*(1<<attempt)) * time.Millisecond)
        continue
    }
    return err
}
```

*Note: For lock-free MVCC without optimistic commit conflicts, use the default `wiredtiger` engine.*

---

## 3. Query Performance & Memory Governor Diagnostics

### 3.1. Suboptimal Query Execution Plans (Slow Queries)

#### Symptom
A query with a selective filter is taking longer than expected.

#### Cause
The Cost-Based Optimizer (CBO) is missing up-to-date column statistics and histograms, causing it to fall back to a sequential scan (`SeqScanIterator`) instead of using a secondary index (`IndexScanIterator`).

#### Resolution
1. Run `ANALYZE TABLE` to refresh statistics and equi-depth histograms:
   ```sql
   ANALYZE TABLE accounts;
   ```
2. Inspect the physical plan using `EXPLAIN ANALYZE`:
   ```sql
   EXPLAIN ANALYZE SELECT * FROM accounts WHERE balance > 10000;
   ```
3. Verify that the estimated costs match actual row counts.

---

### 3.2. Temporary Spill Files Consuming Disk Space

#### Symptom
Large queries with `ORDER BY` produce temporary files in `/tmp` matching `sqlion-sort-run-*.tmp`.

#### Cause
The query's accumulated sorting buffer exceeded the configured `work_mem` limit, triggering an **External Merge Sort** to prevent out-of-memory crashes.

#### Resolution
1. Temporary files are automatically closed and unlinked (`os.Remove`) when the query finishes.
2. If your server has ample RAM and you wish to keep sorting entirely in memory, increase `work_mem`:
   ```sql
   -- Increase for the current session
   SET work_mem = '256MB';
   ```
   Or set server-wide via CLI flag:
   ```bash
   ./bin/server -work-mem=128MB
   ```

---

## 4. Client Driver & Wire Protocol FAQ

### 4.1. `psql: error: connection to server failed: SSL not supported`

#### Symptom
```
psql: error: connection to server at "127.0.0.1", port 5433 failed: server does not support SSL
```

#### Resolution
LionSQL serves unencrypted wire protocol internally, intended to run behind a TLS-terminating reverse proxy (Nginx/Caddy) or private VPC. Specify `sslmode=disable`:
```bash
psql "host=127.0.0.1 port=5433 user=admin dbname=sqlion sslmode=disable"
```

---

### 4.2. DBeaver / DataGrip Connection Setup

#### PostgreSQL Protocol:
1. Create a new connection with driver **PostgreSQL**.
2. Host: `127.0.0.1`, Port: `5433`, Database: `sqlion`, User: `admin`.
3. In **Driver Properties** or **SSL**, set `ssl=false` (`sslmode=disable`).

#### Microsoft SQL Server (TDS) Protocol:
1. Create a new connection with driver **Microsoft SQL Server**.
2. Host: `127.0.0.1`, Port: `1433`, Database: `sqlion`, User: `admin`.
3. In **Driver Properties**, set `encrypt=false` and `trustServerCertificate=true`.

---

## 5. Backup & Restore Diagnostics

### 5.1. Restore Fails with `table already exists`

#### Symptom
```
POST /api/restore failed: failed to recreate table accounts: table already exists: accounts
```

#### Cause
The target database already contains a table with the same name, and `drop_existing` was set to false.

#### Resolution
Enable `drop_existing: true` in your JSON request:
```bash
curl -X POST http://127.0.0.1:8090/api/restore \
     -H "Content-Type: application/json" \
     -d '{"source": "backups/prod.tar.gz", "drop_existing": true}'
```
Or via CLI:
```bash
./bin/server -dir=data -restore=backups/prod.tar.gz
```
*(CLI restore defaults to `DropExisting: true`)*.
