# SQLion Production Deployment & Operations Runbook

This guide details deployment patterns, operational runbooks, process supervision, disaster recovery, and security hardening for SQLion in production environments.

---

## 1. System Requirements & Capacity Planning

### 1.1. Hardware Guidelines

| Workload Tier | Concurrent Connections | CPU Cores | Memory (RAM) | Storage Architecture |
|---|---|---|---|---|
| **Edge / Dev** | 1 - 100 | 2 cores | 2 GB | Standard SSD |
| **Standard OLTP** | 100 - 2,000 | 4 - 8 cores | 8 - 16 GB | NVMe SSD (Ext4 / XFS) |
| **High-Throughput Enterprise** | 2,000 - 20,000+ | 16 - 64 cores | 32 - 128 GB | Dedicated NVMe Array (RAID 10) |

### 1.2. Memory Allocation Hierarchy
Total process memory consumption is governed by:
$$\text{Total Memory} \approx \text{WiredTiger Cache} + (\text{Active Workers} \times \text{work\_mem}) + (\text{Client Connections} \times 4\text{KB})$$
- **WiredTiger Cache**: Defaults to 500MB (dynamically adjustable in code or config).
- **Worker Execution Memory**: `MaxWorkers * work_mem`. For 32 workers at 64MB `work_mem`, maximum execution memory is capped at 2GB.
- **Client Connections**: Handled via lightweight Go goroutines (~4KB stack overhead per connection).

---

## 2. Deployment Models

### 2.1. Linux Systemd Service (Recommended for Bare-Metal & VMs)

Create `/etc/systemd/system/sqlion.service`:

```ini
[Unit]
Description=SQLion High-Concurrency OLTP Database Server
After=network.target local-fs.target
Documentation=https://github.com/sqlion/sqlion

[Service]
Type=simple
User=sqlion
Group=sqlion
WorkingDirectory=/var/lib/sqlion
ExecStart=/usr/local/bin/sqlion \
    -dir=/var/lib/sqlion/data \
    -engine=wiredtiger \
    -http=127.0.0.1:8090 \
    -pg=0.0.0.0:5433 \
    -tds=0.0.0.0:1433 \
    -work-mem=64MB \
    -max-workers=64 \
    -max-idle-sessions=128 \
    -auth=true \
    -admin-email=admin@company.com \
    -admin-pass=EnvSecurePassString

Restart=always
RestartSec=5s
LimitNOFILE=65536
LimitNPROC=32768
TimeoutStopSec=30s
KillMode=mixed
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

Enable and start the service:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now sqlion
sudo systemctl status sqlion
```

---

### 2.2. Docker Container Deployment

Create a `Dockerfile`:

```dockerfile
FROM golang:1.22-bookworm AS builder
WORKDIR /app
COPY . .
RUN apt-get update && apt-get install -y libwiredtiger-dev
RUN go build -tags wiredtiger -buildvcs=false -o /bin/sqlion ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y libwiredtiger0 ca-certificates curl && rm -rf /var/lib/apt/lists/*
COPY --from=builder /bin/sqlion /usr/local/bin/sqlion

EXPOSE 8090 5433 1433
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/sqlion"]
CMD ["-dir=/data", "-http=0.0.0.0:8090", "-pg=0.0.0.0:5433", "-tds=0.0.0.0:1433"]
```

Run via Docker Compose:

```yaml
version: '3.8'

services:
  sqlion:
    image: sqlion:latest
    container_name: sqlion-db
    restart: unless-stopped
    ports:
      - "8090:8090"
      - "5433:5433"
      - "1433:1433"
    volumes:
      - sqlion-data:/data
    environment:
      - SQLION_AUTH=true
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8090/api/health"]
      interval: 10s
      timeout: 3s
      retries: 3

volumes:
  sqlion-data:
```

---

## 3. Background Daemon Lifecycle Controls

SQLion includes native daemon lifecycle commands without external process wrappers:

```bash
# 1. Start as background daemon
./bin/server -d -dir=/var/lib/sqlion/data

# 2. Check running daemon status and PID
./bin/server -status -dir=/var/lib/sqlion/data

# 3. Graceful restart (re-reads configuration, preserves active data)
./bin/server -restart -dir=/var/lib/sqlion/data

# 4. Stop running daemon cleanly
./bin/server -stop -dir=/var/lib/sqlion/data
```

### Clean Teardown & Signal Handling
When receiving `SIGTERM` or `SIGINT`:
1. Closes TDS and PostgreSQL wire protocol listeners immediately, stopping incoming handshakes.
2. Reaps active client transactions and issues HTTP server graceful shutdown with a 15-second drain window.
3. Flushes in-flight write buffers, checkpoints B+Tree indexes, and invokes `engine.Close()`.
4. Exits with return code `0`.

---

## 4. Disaster Recovery & Backup Automation

### 4.1. Automated Daily Snapshot Backup via Cron

Create `/etc/cron.daily/sqlion-backup`:

```bash
#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR="/var/backups/sqlion"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
TARGET_FILE="${BACKUP_DIR}/sqlion_backup_${TIMESTAMP}.tar.gz"

mkdir -p "${BACKUP_DIR}"

# Trigger live snapshot via local HTTP API without taking write locks
curl -fsS -OJ http://127.0.0.1:8090/api/backup > "${TARGET_FILE}"

# Retention: Delete backups older than 14 days
find "${BACKUP_DIR}" -name "sqlion_backup_*.tar.gz" -type f -mtime +14 -delete
```

Ensure permissions:
```bash
chmod +x /etc/cron.daily/sqlion-backup
```

---

### 4.2. Disaster Recovery & Restore Procedure

In the event of hardware failure or catastrophic corruption:

1. Provision a clean host and install the SQLion binary.
2. Retrieve the latest backup archive (`sqlion_backup_YYYYMMDD_HHMMSS.tar.gz`).
3. Restore using the CLI utility:
   ```bash
   /usr/local/bin/sqlion \
       -dir=/var/lib/sqlion/data \
       -restore=/var/backups/sqlion/sqlion_backup_latest.tar.gz
   ```
4. Start the server service:
   ```bash
   sudo systemctl start sqlion
   ```
5. Verify health:
   ```bash
   curl -fsS http://127.0.0.1:8090/api/health
   ```

---

## 5. Security & Network Hardening

### 5.1. Network Binding & Reverse Proxy

For public production environments:
1. Bind SQLion HTTP API (`-http`) to `127.0.0.1:8090`.
2. Terminate TLS/HTTPS and WebSockets using Nginx or Caddy.

Example Nginx reverse proxy configuration:

```nginx
server {
    listen 443 ssl http2;
    server_name db.yourcompany.com;

    ssl_certificate /etc/letsencrypt/live/db.yourcompany.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/db.yourcompany.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket & Server-Sent Events (SSE) support
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 86400s;
    }
}
```

### 5.2. Firewall Rules

| Port | Protocol | Default Exposure | Purpose |
|---|---|---|---|
| `5433` | TCP | Private VPC / Internal | PostgreSQL Wire Protocol (psql, application ORMs) |
| `1433` | TCP | Private VPC / Internal | Microsoft TDS Protocol (SSMS, Azure Data Studio) |
| `8090` | TCP | Localhost / Reverse Proxy | Embedded Web Studio, REST API, Health & Metrics |

---

## 6. Health Monitoring & Observability

### 6.1. Kubernetes Probes

```yaml
livenessProbe:
  httpGet:
    path: /api/health
    port: 8090
  initialDelaySeconds: 5
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /api/pool/stats
    port: 8090
  initialDelaySeconds: 2
  periodSeconds: 5
```

### 6.2. Key Operational Metrics (`/api/pool/stats`)

| Metric | Target Threshold | Action if Exceeded |
|---|---|---|
| `queued_requests` | `0` to `5` | If persistently > 50, increase `-max-workers` or scale CPU |
| `active_workers` | `< max_workers * 0.8` | Normal working capacity |
| `idle_sessions` | `> 5` | If dropping to 0, increase `-max-idle-sessions` |

---

## 7. High-Availability & 1-Writer Multi-Reader Replication Runbook

SQLion enforces a **1-Writer Multi-Reader High-Availability Architecture** (GEMINI.md Section 6). The primary node accepts all read/write mutations, while secondary replicas continuously stream and replay WAL records, providing linear horizontal read capacity and rapid failover.

### 7.1. Launching the Primary Node

Start the primary node with a unique node identifier:

```bash
/usr/local/bin/sqlion \
    -node-id=prod-primary-01 \
    -dir=/var/lib/sqlion/data \
    -engine=wiredtiger \
    -http=0.0.0.0:8090 \
    -pg=0.0.0.0:5433 \
    -tds=0.0.0.0:1433
```

### 7.2. Launching Read Replicas

Launch one or more secondary replicas pointing to the primary's HTTP address via `-replica-of`:

```bash
/usr/local/bin/sqlion \
    -node-id=prod-replica-01 \
    -replica-of=http://primary-host.internal:8090 \
    -dir=/var/lib/sqlion/replica_data \
    -engine=wiredtiger \
    -http=0.0.0.0:8090 \
    -pg=0.0.0.0:5433 \
    -tds=0.0.0.0:1433
```

Upon boot, the replica:
1. Connects to `http://primary-host.internal:8090/api/replication/stream`.
2. Establishes a persistent chunked stream from its last replayed LSN.
3. Spawns a background thread dispatching LSN acknowledgments to `/api/replication/ack`.
4. Enforces strict read-only execution across PGWire, TDS, and HTTP REST (`ErrReadOnlyReplica`).

### 7.3. Monitoring Replication Lag

Query `/api/replication/status` on either primary or replica:

```bash
# Check primary status & connected followers
curl -fsS http://primary-host.internal:8090/api/replication/status | jq .

# Check replica status & current replayed LSN
curl -fsS http://replica-host.internal:8090/api/replication/status | jq .
```

Alerting threshold: If `replication_lag` (Primary `current_lsn` - Replica `replayed_lsn`) exceeds 1,000 for longer than 30 seconds, investigate network throughput or replica disk I/O saturation.

### 7.4. Zero-Downtime Failover & Promotion Runbook

If the primary node fails or undergoes maintenance:

1. **Promote the Most Up-to-Date Replica**:
   Select the replica with the highest `replayed_lsn` and issue the promotion command:
   ```bash
   curl -fsS -X POST http://replica-host.internal:8090/api/replication/promote
   ```
   The replica immediately lifts read-only constraints, transitions to `RolePrimary`, and begins accepting write traffic.

2. **Redirect Application Write Traffic**:
   Update DNS or your external layer-4 load balancer (AWS NLB, HAProxy) to point write traffic to `replica-host.internal`.

3. **Re-attaching the Old Primary as a Follower**:
   When the previous primary host is restored, demote it and attach it as a follower to the new primary:
   ```bash
   curl -fsS -X POST \
       -H "Content-Type: application/json" \
       -d '{"primary_url": "http://replica-host.internal:8090"}' \
       http://old-primary.internal:8090/api/replication/demote
   ```

