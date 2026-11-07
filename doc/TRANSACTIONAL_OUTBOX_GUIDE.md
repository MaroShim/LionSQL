# LionSQL Transactional Outbox & Broker Relay Guide

> - **Created**: 2026-11-07
> - **Last Updated**: 2026-11-07

LionSQL embeds a native **Transactional Outbox Broker Relay** that guarantees reliable, zero-data-loss event streaming from committed OLTP transactions directly to external message brokers and webhooks without external daemon sidecars (such as Debezium, Kafka Connect, or Zookeeper).

---

## 1. Unified Architecture & Sender Contract

All outbox forwarding mechanisms share a **single, unified event pipeline** driven by the `OutboxDispatcher` background worker:

```
[ Committed WAL Transaction ]
              │ (LSN-ordered mutation events)
              ▼
    [ CDC Streamer Channel ]
              │
              ▼
   [ OutboxDispatcher Worker ]  <──(Head-of-Line Retention & Backpressure)
              │
              ├── [ WebhookSink ]     ──> POST http://service/webhook
              ├── [ KafkaRESTSink ]   ──> POST http://kafka-proxy:8082/topics/{table}
              └── [ RabbitMQHTTPSink] ──> POST http://rabbitmq:15672/api/exchanges/...
```

### The Strict Sender Contract (Zero Data Loss & Order Preservation)
The Outbox Dispatcher adheres strictly to the **Sender Contract** ([ADR-026](ADR.md#adr-026-transactional-outbox-sender-contract---backpressure-pause-and-zero-data-loss-guarantee) and [`notes/ENGINEERING_CONTEXT.ko.md`](../notes/ENGINEERING_CONTEXT.ko.md) Invariant 21):

1. **Faithful Delivery (Zero Silent Drops)**: The sender's sole responsibility is the faithful delivery of committed database mutations to registered receivers. Under no circumstance will a message be discarded or skipped due to broker unavailability.
2. **Backpressure on Failure**: If a receiver (broker or webhook) is unreachable or returns transient errors, the dispatcher **pauses in-place**, retrying with exponential backoff (`50ms` up to `2s` cap). It does NOT advance to subsequent events in the channel.
3. **Causal FIFO Ordering**: Events are delivered strictly in the order they were committed. An `UPDATE` or `DELETE` event will never precede its corresponding `INSERT`.
4. **Persistent LSN Checkpoint**: Successful deliveries atomically update the disk checkpoint (`outbox_checkpoint.json`). If the database process is restarted during a broker outage, streaming resumes deterministically from the exact unacknowledged LSN (`last_ack_lsn`).

---

## 2. Command-Line Flags & Configuration

While each flag configures a specific destination format, all flags feed the **same single Outbox Dispatcher engine**:

### CLI Flags

| Flag | Argument | Description |
|---|---|---|
| `-outbox` | (boolean) | Activates outbox dispatching (reads targets from `lionsql.json` if specified) |
| `-outbox-table` | `<table>` | Filters dispatching to a specific table (default: all tables) |
| `-outbox-topic-prefix` | `<prefix>` | Prefix for destination topic/routing keys (default: `lionsql.`) |
| `-outbox-webhook` | `<url>` | HTTP endpoint URL for generic Webhook JSON forwarding |
| `-outbox-kafka` | `<url>` | Endpoint URL for Confluent-compatible Kafka REST Proxy (e.g. `http://localhost:8082`) |
| `-outbox-rabbitmq` | `<url>` | Endpoint URL for RabbitMQ Management API (e.g. `http://guest:guest@localhost:15672`) |

### Example Invocations

```bash
# 1. Forward all database mutations to a microservice webhook
lionsql -outbox-webhook="http://127.0.0.1:8080/events"

# 2. Forward only the 'orders' table to Kafka REST Proxy with custom prefix
lionsql -outbox-kafka="http://localhost:8082" -outbox-table="orders" -outbox-topic-prefix="corp.sales."

# 3. Multi-sink forwarding: stream to both RabbitMQ and Webhook simultaneously
lionsql -outbox-rabbitmq="http://guest:guest@localhost:15672" -outbox-webhook="http://audit.internal/log"
```

---

## 3. Dedicated Outbox Table Pattern vs General CDC

LionSQL supports two dispatch modes automatically:

1. **General CDC Mode (Default)**:
   Any table mutation (`INSERT`, `UPDATE`, `DELETE`) across the database is captured from the WAL, wrapped as a JSON event, and published to topic `<prefix><table_name>`.
2. **Dedicated Outbox Table Mode**:
   When mutations occur on a table named `outbox` or `_outbox` (or configured via `dedicated_table: true`), the dispatcher extracts structured routing columns directly from the record:
   - `topic`: Overrides destination topic
   - `key`: Partition routing key
   - `payload`: Exact payload forwarded as-is (bypassing generic CDC wrappers)
   - `headers`: Optional JSON map forwarded as HTTP/broker headers

This enables classic Transactional Outbox patterns executed via PostgreSQL Writable CTEs:

```sql
WITH ins_order AS (
    INSERT INTO orders (customer_id, amount) VALUES (42, 150.00) RETURNING id
),
ins_outbox AS (
    INSERT INTO outbox (topic, key, payload)
    SELECT 'orders.created', CAST(id AS VARCHAR), '{"order_id":' + CAST(id AS VARCHAR) + '}'
    FROM ins_order
)
SELECT id FROM ins_order;
```

---

## 4. Observability & Health Monitoring

The outbox dispatcher exports operational metrics via the REST API and Prometheus:

- **HTTP Status API**: `GET /api/system/stats`
  ```json
  {
    "outbox": {
      "active": true,
      "sink_count": 1,
      "dispatched_count": 14205,
      "failed_count": 0,
      "retried_count": 12,
      "last_ack_lsn": 10452,
      "last_dispatch_at": "2026-11-07T22:20:00Z"
    }
  }
  ```
- **Boot Banner**:
  Upon startup, LionSQL prints the active sink status:
  ```
   [Outbox Broker]  Active (Sinks: 1, Table Filter: 'orders')
  ```
