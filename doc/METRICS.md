# SQLion Observability, Metrics & Telemetry Guide

SQLion provides comprehensive telemetry, performance metrics, and health probe interfaces to ensure production observability across containerized and bare-metal environments. This guide details the metrics dictionary, Prometheus scraping integration, health probe endpoints, and Grafana dashboard recommendations.

---

## 1. Observability Architecture Overview

```
  ┌──────────────────────────────────────────────────────────┐
  │                     SQLion Server                        │
  │                                                          │
  │   [ Connection Pool ]  [ Query Engine ]  [ Storage Engine]
  │           │                   │                 │        │
  │           ▼                   ▼                 ▼        │
  │     Active Workers      Execution Latency   Disk I/O     │
  │     Queued Requests     Spill Sorting       Compaction   │
  │           │                   │                 │        │
  │           └───────────────────┼─────────────────┘        │
  │                               ▼                          │
  │                   [ Internal Telemetry Hub ]             │
  └───────────────────────────────┬──────────────────────────┘
                                  │
                  ┌───────────────┴───────────────┐
                  ▼                               ▼
       GET /api/pool/stats               GET /api/health
       GET /metrics (Prometheus)         (Liveness / Readiness)
                  │                               │
                  ▼                               ▼
      [ Prometheus / Datadog ]             [ Kubernetes / LB ]
                  │
                  ▼
       [ Grafana Dashboards ]
```

---

## 2. Health & Status Probe Endpoints

### 2.1 Liveness & Readiness Probe (`GET /api/health`)

Used by Kubernetes probes, AWS ALB/ELB health checks, and HAProxy keepalive checks.

- **HTTP Method**: `GET /api/health`
- **Response Format**: `application/json`
- **Status Code**: `200 OK` when healthy, `503 Service Unavailable` during startup or unrecoverable storage failure.

#### Sample Response:
```json
{
  "status": "ok",
  "version": "0.7.0",
  "storage_engine": "wiredtiger",
  "uptime_seconds": 86420,
  "cluster_state": "leader"
}
```

---

## 3. Connection Pool & Worker Telemetry (`GET /api/pool/stats`)

Exposes real-time worker utilization, queue backpressure, and session allocation status.

- **HTTP Method**: `GET /api/pool/stats`
- **Response Format**: `application/json`

#### Sample Response:
```json
{
  "max_workers": 128,
  "active_workers": 14,
  "max_idle_sessions": 32,
  "idle_sessions": 12,
  "queued_requests": 0
}
```

---

## 4. Prometheus Metrics Dictionary

When running with Prometheus telemetry enabled, SQLion exports standard OpenMetrics/Prometheus formatted time-series:

### 4.1 Connection Pool & Concurrency Metrics

| Metric Name | Type | Description |
|---|---|---|
| `sqlion_pool_max_workers` | Gauge | Maximum worker goroutines configured in execution pool. |
| `sqlion_pool_active_workers` | Gauge | Current number of worker goroutines actively processing queries. |
| `sqlion_pool_queued_requests` | Gauge | Number of incoming requests waiting for an available worker slot. |
| `sqlion_pool_idle_sessions` | Gauge | Cached idle database sessions available for immediate reuse. |

### 4.2 Query Execution Metrics

| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `sqlion_queries_total` | Counter | `protocol`, `type`, `status` | Total SQL queries processed (`protocol`: `postgres`, `tds`, `http`; `status`: `success`, `error`). |
| `sqlion_query_duration_seconds` | Histogram | `protocol`, `type` | Query latency distribution across p50, p90, p99 percentiles. |
| `sqlion_spill_sort_runs_total` | Counter | - | Number of times sort operations exceeded memory quota and spilled to temporary scratch disk. |
| `sqlion_memory_governor_rejections_total` | Counter | - | Number of queries rejected due to system memory limit saturation. |

### 4.3 Storage & Persistence Metrics

| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `sqlion_storage_read_bytes_total` | Counter | `engine` | Total bytes read from underlying storage. |
| `sqlion_storage_write_bytes_total` | Counter | `engine` | Total bytes committed to storage and WAL. |
| `sqlion_wal_sync_duration_seconds` | Histogram | - | Latency of WAL `fsync` calls to durable disk. |
| `sqlion_backup_duration_seconds` | Gauge | `type` | Duration of the most recent online snapshot backup pass. |

---

## 5. Prometheus Configuration Example

Add SQLion as a scrape target in your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'sqlion'
    scrape_interval: 10s
    metrics_path: '/api/pool/stats'
    static_configs:
      - targets: ['127.0.0.1:8090']
        labels:
          cluster: 'production-primary'
          datacenter: 'us-east-1'
```

---

## 6. Recommended Alerting Rules

```yaml
groups:
  - name: sqlion_alerts
    rules:
      - alert: SQLionPoolSaturation
        expr: sqlion_pool_queued_requests > 10
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "SQLion connection pool backpressure detected"
          description: "Over 10 requests queued for more than 1 minute on {{ $labels.instance }}."

      - alert: SQLionHighQueryLatencyP99
        expr: histogram_quantile(0.99, rate(sqlion_query_duration_seconds_bucket[5m])) > 0.5
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "SQLion P99 latency exceeding 500ms"
          description: "99th percentile query latency is {{ $value }}s on {{ $labels.instance }}."

      - alert: SQLionInstanceDown
        expr: up{job="sqlion"} == 0
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "SQLion instance unreachable"
          description: "SQLion instance {{ $labels.instance }} failed liveness checks."
```
