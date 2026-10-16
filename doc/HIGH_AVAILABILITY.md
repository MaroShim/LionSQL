# LionSQL High Availability & Disaster Recovery Architecture

LionSQL is architected to provide enterprise-grade High Availability (HA), continuous data durability, and disaster recovery (DR) across distributed environments. This document details the clustering topology, replication protocols, automatic failover mechanisms, and recovery service level agreements (SLAs).

---

## 1. High Availability Architecture Overview

LionSQL supports two primary clustering models:
1. **Primary-Replica Asynchronous / Semi-Synchronous Log Shipping**: Optimized for read scaling and low-latency local write throughput.
2. **Quorum-Based Raft Consensus Clustering**: Provides zero-data-loss (RPO = 0) state machine replication and automatic failover (RTO < 5 seconds) without external orchestrators.

```
                         [ Client Applications / Load Balancer ]
                                     │
                 ┌───────────────────┴───────────────────┐
                 │ Write Traffic                         │ Read Queries
                 ▼                                       ▼
       ┌───────────────────┐                   ┌───────────────────┐
       │   LionSQL Leader   │                   │  LionSQL Follower  │
       │   (Read / Write)  │                   │    (Read Only)    │
       └─────────┬─────────┘                   └─────────▲─────────┘
                 │                                       │
                 │ ─── Consensus WAL Stream (Raft) ─────┤
                 │                                       │
                 ▼                                       ▼
       ┌───────────────────┐                   ┌───────────────────┐
       │  LionSQL Follower  │                   │  LionSQL Follower  │
       │    (Read Only)    │                   │    (Read Only)    │
       └───────────────────┘                   └───────────────────┘
```

---

## 2. Replication Mechanisms

### 2.1 Consensus Log Shipping
All mutating transactions (inserts, updates, deletes, DDL schema alterations) produce deterministic Write-Ahead Log (WAL) frame records.
- **Synchronous Mode**: The leader buffers mutations in its active write batch, broadcasts log entries to follower nodes, and waits for a quorum ($Q = \lfloor N/2 \rfloor + 1$) acknowledgment before committing the transaction to the local storage engine.
- **Asynchronous Mode**: The leader commits transactions locally immediately upon appending to disk, streaming WAL records in the background to connected replicas.

### 2.2 Wire Protocol Replication Stream
Followers maintain persistent TCP streaming connections to the primary node.
- Followers issue handshake messages specifying their latest committed Log Sequence Number (`LSN`).
- The primary streams compressed, CRC32-checksummed delta frames.
- In case of network disconnection, the follower reconnects and requests catch-up frames from its last known LSN. If the primary has compacted logs past that point, an automated online snapshot transfer is initiated.

---

## 3. Consensus & Failover Lifecycle

### 3.1 Leader Election
LionSQL utilizes a leader election protocol based on Raft:
- Each node maintains a randomized election timer ($150\text{ms} - 300\text{ms}$).
- If a follower misses leader heartbeats beyond the election timeout, it increments its term, transitions to `Candidate` state, and requests votes from peers.
- A candidate requires majority approval to become the new `Leader`.
- A node only grants a vote if the candidate's log is at least as up-to-date as its own (`LastLogTerm` and `LastLogIndex`), preventing stale nodes from ascending to leadership.

### 3.2 Split-Brain Prevention
Split-brain scenarios are structurally prevented through strict quorum enforcement:
- A partitioned leader unable to reach a majority of nodes fails all subsequent synchronous write transactions.
- Replicas in the minority partition cannot elect a candidate since they cannot achieve a quorum of votes.
- Once network connectivity is restored, partitioned nodes step down upon discovering a higher term and reconcile their state with the true leader.

```
  Normal State:
  Node A (Leader, Term 1)  <--->  Node B (Follower)  <--->  Node C (Follower)
  Quorum = 2 / 3

  Network Partition:
  [ Node A (Isolated) ]    ||||   [ Node B (Term 2 Leader) <---> Node C (Follower) ]
  Write Rejected (0/2)     ||||   Quorum Satisfied (2/2) -> Writes Accepted
```

---

## 4. Disaster Recovery & Backup Strategy

### 4.1 Online Non-Blocking Hot Snapshots
LionSQL provides built-in online non-blocking backup mechanisms (`POST /api/backup` or CLI `-backup <path>`):
- **Point-in-Time Consistency**: Utilizing MVCC and engine-level checkpointing (such as WiredTiger named checkpoints or In-Memory OCC snapshot isolation), backups are taken live without acquiring table-level locks or interrupting write transactions.
- **Zero Query Impact**: Background workers stream read-only data pages directly to target storage (local disk, NFS, or cloud object stores) with adaptive throttling to prevent I/O starvation of foreground queries.

### 4.2 Recovery SLAs
- **Recovery Point Objective (RPO)**:
  - Synchronous Raft Mode: **$\text{RPO} = 0$** (Zero data loss).
  - Asynchronous Replication Mode: $\text{RPO} < 100\text{ms}$ under standard network conditions.
- **Recovery Time Objective (RTO)**:
  - Automatic Leader Failover: **$\text{RTO} < 5\text{s}$**.
  - Cold Restart Recovery from Snapshot + WAL Replay: Sub-second for datasets $< 10\text{GB}$; linearly scales with pending WAL log size.

---

## 5. Read Replica Scaling & Load Balancing

For read-intensive workloads, follower nodes serve read-only queries over PostgreSQL Wire Protocol v3 (`:5433`), TDS Protocol (`:1433`), and REST endpoints (`:8090`).

### 5.1 Read Consistency Modes
1. **Monotonic Read Consistency (Session Consistency)**: Clients track their observed LSN; read queries on replicas wait until the replica has replayed changes up to that LSN.
2. **Eventual Consistency**: Reads are serviced immediately from the follower's active memory snapshot without synchronization barriers, maximizing throughput.

### 5.2 Load Balancer Configuration
Recommended configurations for HAProxy, NGINX, or Envoy:
- **Write Target**: Route `TCP :5433`, `:1433`, and `HTTP :8090` to the active leader identified via the `/api/health` probe (`role: "leader"`).
- **Read Target**: Distribute read-only traffic round-robin across all healthy nodes (`status: "ok"`).

---

## 6. Zero-Downtime Rolling Upgrades

To upgrade LionSQL clusters without downtime:
1. **Upgrade Follower Nodes First**:
   - Drain traffic from target follower node.
   - Stop LionSQL process (`SIGTERM`), replace binary with new version, and restart.
   - Wait for node to catch up to the leader's LSN and verify health via `GET /api/health`.
   - Repeat for all follower nodes.
2. **Perform Controlled Leader Step-Down**:
   - Issue a controlled transfer leadership command (`POST /api/cluster/step-down`).
   - The leader transfers authority to an upgraded follower node.
3. **Upgrade Former Leader**:
   - Stop, upgrade, and restart the remaining node as a follower.

---

## 7. 1-Writer Multi-Reader High-Availability Replication (v0.11.0)

LionSQL integrates a native, standalone 1-Writer Multi-Reader High Availability Availability Group architecture into `cmd/server` (`pkg/replication`):

```
                        [ Primary Node (:5433 / :1433 / :8090) ]
                                    (Read / Write)
                                          │
                  ┌───────────────────────┴───────────────────────┐
                  │ WAL Streaming (/api/replication/stream)       │
                  ▼                                               ▼
     [ Replica 1 (:5434 / :1434) ]                   [ Replica 2 (:5435 / :1435) ]
      (Strict Read-Only Standby)                      (Strict Read-Only Standby)
```

### 7.1. Server CLI Configuration
- **Primary Node**:
  ```bash
  ./bin/server --port=5433 --tds-port=1433 --http-port=8090 --data-dir=data/primary --node-id=node-primary
  ```
- **Secondary Replica Node**:
  ```bash
  ./bin/server --port=5434 --tds-port=1434 --http-port=8091 --data-dir=data/replica1 --node-id=node-replica1 --replica-of=http://localhost:8090
  ```

### 7.2. Replication Management REST Endpoints
- **`GET /api/replication/stream?from_lsn=<n>`**: Streams compressed binary framed WAL records incrementally from primary to connected replicas via chunked transfer.
- **`GET /api/replication/status`**: Reports node role (`primary` or `replica`), current committed LSN, replication lag, and connected replicas.
- **`POST /api/replication/ack`**: Replicas send heartbeat acknowledgments reporting their latest replayed LSN.
- **`POST /api/replication/promote`**: Instantly promotes a replica into an active primary node capable of accepting writes.
- **`POST /api/replication/demote`**: Demotes a node to follower status upon graceful cluster reconfigurations.

### 7.3. Read-Only Invariant Enforcement
Secondary replicas enforce strict read-only execution across all wire protocols:
- Any mutating DDL (`CREATE/ALTER/DROP/TRUNCATE`) or DML (`INSERT/UPDATE/DELETE/MERGE`) query is immediately rejected with `ErrReadOnlyReplica` (SQLSTATE `25006`).
- Interactive transactions requesting write access (`BEGIN TRANSACTION READ WRITE`) fail fast without mutating local state.

