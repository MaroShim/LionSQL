# SQLion Engineering Roadmap & Milestones

This document charts the development milestones, architectural priorities, and future technical roadmap for SQLion.

---

## 1. Milestones Overview

```
[ Phase 1: v0.5.0 ] ───> [ Phase 2: v0.6.0 - v0.8.0 ] ───> [ Phase 3: v1.0.0+ Distributed & AI ]
- Core OLTP Engine       - Concurrent SkipList Index              - AI Vector Embeddings (HNSW)
- Dual Wire Protocols    - Zero-Copy Cursor Iteration             - Distributed Query Sharding
- Dual Storage Backends  - Raft 3-Node Consensus                  - Pluggable Pebble / Columnar
- VDBE Bytecode VM       - Automated TLS / ACME                   - Change Data Capture (CDC)
- CBO & Volcano Engine                                            - Full-Text Search (BM25)
- Online Hot Backup
- COPY Protocol
(COMPLETED)              (NEXT)                                   (PLANNED)
```

---

## 2. Phase 1: Core Foundation & Feature Complete (v0.5.0 - Completed)

- [x] **Single-Binary Standalone Executable**: Embedded Web Management Studio (`/_/`), REST API, wire protocol servers.
- [x] **Dual Wire Protocol Listeners**: PostgreSQL v3 on port `5433` and Microsoft SQL Server TDS 7.x/8.0 on port `1433`.
- [x] **Dual Storage Backends**: Production WiredTiger (Cgo, lock-free B+Tree, MVCC) and pure-Go In-Memory OCC engine with MessagePack and WAL/AOF persistence.
- [x] **Dual-Parser Architecture**: Sub-microsecond Native Pratt parser with automatic official Microsoft T-SQL ANTLR4 fallback.
- [x] **VDBE Bytecode Virtual Machine**: SQLite-style register VM executing procedures, triggers, UDFs, TVFs, and cursors.
- [x] **Cost-Based Optimizer (CBO)**: `ANALYZE TABLE`, 64-bucket equi-depth histograms, selectivity estimation, and `EXPLAIN ANALYZE`.
- [x] **Volcano Iterator Relational Engine**: Streaming query pipelines (`SeqScan`, `IndexScan`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`, `CTE`).
- [x] **Query Memory Governor**: `work_mem` tracking with automated Spill-to-Disk External Merge Sort.
- [x] **Built-in Connection Pooler**: Goroutine multiplexing supporting 10,000+ client connections without PgBouncer.
- [x] **Online Non-Blocking Hot Backup**: Live `.tar.gz` point-in-time snapshots without write locks.
- [x] **PostgreSQL Bulk Copy Streaming**: High-throughput `COPY FROM STDIN` and `COPY TO STDOUT` protocol support.

---

## 3. Phase 2: High Availability & Storage Performance (v0.6.0 - v0.8.0)

### 3.1. Distributed Raft Consensus Replication
- Implement a 3-node Raft consensus cluster using HashiCorp Serf and Raft.
- Synchronous log replication with automated leader election and transparent client failover.
- Read-scaling across follower nodes.

### 3.2. Pure-Go In-Memory Concurrent SkipList Secondary Index
- Replaced sorted `IndexEntry` slices in `memengine` with a lock-aware concurrent in-memory SkipList.
- Reduced index mutation overhead from $O(N)$ memory copies to $O(\log N)$ probabilistic skip links (~186ns lookups, 2.8M inserts/sec).

### 3.3. Zero-Copy Cursor Iteration
- Stream direct immutable memory record views in `storage.Cursor` during `Scan()` and `IndexScan()`, bypassing intermediate `storage.Record` map allocations (reducing scan allocations by 99%).

### 3.4. Automated TLS / ACME Integration
- Built-in Let's Encrypt TLS certificate provisioning directly inside the binary, eliminating reverse proxies for small deployments.

---

## 4. Phase 3: AI Workloads & Distributed Horizons (v2.0.0)

### 4.1. AI Vector Embeddings & Similarity Search
- Native `VECTOR(dim)` column type (e.g. 1536-dimension float vectors).
- Hierarchical Navigable Small World (HNSW) and Inverted File Flat (IVF-Flat) vector indexing.
- Distance operators: Cosine distance (`<=>`), L2 Euclidean distance (`<->`), Inner Product (`<#>`).
- Seamless hybrid queries combining relational filters with semantic similarity search:
  ```sql
  SELECT id, title, text
  FROM documents
  WHERE category = 'finance'
  ORDER BY embedding <=> '[0.012, -0.045, ...]'
  LIMIT 5;
  ```

### 4.2. Pluggable Storage Backend Ecosystem
- Modular support for alternative KV storage engines:
  - Pebble (CockroachDB's high-performance LSM-tree).
  - bbolt (Pure-Go B+Tree).
  - DuckDB Columnar storage for analytical OLAP queries.

### 4.3. Change Data Capture (CDC) Streaming
- Real-time streaming of committed transaction mutations to external message brokers (Apache Kafka, RabbitMQ, NATS).

### 4.4. Full-Text Search Engine
- Built-in inverted index with BM25 ranking, tokenization, stemming, and stop-word filtering.
