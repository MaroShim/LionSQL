# LionSQL Frequently Asked Questions (FAQ) (`doc/FAQ.md`)

> **Document Version**: v0.91.0  
> **Korean Version**: [`doc/FAQ.ko.md`](FAQ.ko.md)  
> **Core Architecture & Knowledge Base**: [`notes/ENGINEERING_CONTEXT.ko.md`](../notes/ENGINEERING_CONTEXT.ko.md), [`doc/ARCHITECTURE.md`](ARCHITECTURE.md)

Frequently asked questions regarding LionSQL's design philosophy, internal architecture, performance optimizations, and protocol compatibility.

---

## I. Engine & Runtime Architecture

### Q1. In a nutshell, what is LionSQL?
> **A.** LionSQL is an autonomous, high-concurrency OLTP database engine implemented in Go and packaged into a **single standalone static binary**. It strictly excludes external sidecars (such as PgBouncer, Redis, or Nginx), consolidating connection pooling, core storage, wire listeners, embedded Web Studio, and zero-dependency SPA web hosting (`--app`) into a single executable (`lionsql`).

### Q2. How does LionSQL execute T-SQL stored procedures in sub-microseconds?
> **A.** It utilizes an SQLite-style **VDBE (Virtual Database Engine) bytecode VM**. SQL/T-SQL statements are compiled into sequential low-level bytecode instructions. Through `sync.Pool` register recycling and direct array slot indexing (0.2ns), AST traversal overhead and heap allocations are virtually eliminated.

### Q3. Why does LionSQL adopt a 2-Tier parser architecture (Native Pratt + ANTLR4)?
> **A.** Over 95% of high-frequency DDL/DML and transaction statements are parsed instantaneously by the zero-allocation **Tier 1 Native Pratt Parser**. For vendor-specific syntax and complex clauses (such as `WITH RECOMPILE` or deep query hints), execution automatically falls back to the official Microsoft T-SQL **Tier 2 ANTLR4 Parser**. This architecture satisfies both sub-microsecond latency and 100% dialect compatibility.

### Q4. Why is the query execution engine split into 2 Tiers (VDBE vs. Volcano CBO)?
> **A.** Because we abandoned the unrealistic ambition of solving all queries with a single execution model. Simple OLTP point lookups and mutations route directly to the VDBE bytecode VM (Tier 1) for nano-to-microsecond latency. Complex analytical queries involving multi-table joins, CTEs, and window functions are routed to the Volcano iterator model guided by a Cost-Based Optimizer (CBO, Tier 2).

### Q5. What is the difference between In-Memory OCC and WiredTiger?
> **A.** 
> - **In-Memory OCC**: A pure in-memory engine utilizing monotonic version counters for Optimistic Concurrency Control (OCC) and background WAL/AOF persistence for extreme in-memory throughput.
> - **WiredTiger**: A disk-backed B+Tree engine protected by hazard pointers and MVCC timestamps, providing battle-tested durability and configurable page cache management for large datasets.

---

## II. Concurrency & ACID Durability

### Q6. How is "Reads never block writes, and writes never block reads" achieved?
> **A.** Traditional 2-Phase Locking (2PL) shared read locks (S-Locks) are strictly eliminated. Read transactions non-blockingly access committed snapshot versions. Write transactions buffer mutations within transaction-private workspaces until commit time, when conflicts are verified (OCC) or applied to MVCC chains.

### Q7. How was zero data loss proven under sudden power loss or `kill -9` crashes?
> **A.** Proven through an automated black-box oracle harness (`cmd/crashtest`). Across **100 consecutive rounds of sudden SIGKILL, in-recovery DOUBLE_KILL, and torn WAL tail corruptions**, LionSQL verified 100% lossless recovery of all 344,000+ committed transactions and zero balance invariant violations (`SUM(balance)=C`).

### Q8. How are deadlocks prevented across concurrent client connections?
> **A.** Cross-lock wait loops are eliminated. Under In-Memory OCC, write-write conflicts trigger immediate fast-fail (`ErrWriteConflict`) or transparent backoff retries rather than blocking. Both engines enforce a canonical lock acquisition hierarchy (`[RULE:LockHierarchy]`), eliminating circular wait conditions.

---

## III. Protocols & Ecosystem Compatibility

### Q9. Can psql and SSMS connect to the same LionSQL instance simultaneously?
> **A.** Yes. LionSQL natively listens on TCP port `:5433` for PostgreSQL v3 (PGWire) and TCP port `:1433` for Microsoft SQL Server (TDS 7.x/8.0) as equal first-class citizens. You can query a table using `psql` while concurrently updating rows via `sqlcmd`, SSMS, or DBeaver.

### Q10. Does LionSQL support `USE <dbname>` under its single-catalog policy?
> **A.** LionSQL enforces one isolated database catalog per process (`--data-dir`) to prevent noisy-neighbor memory leaks. However, for 100% compatibility with SSMS, DBeaver, and migration scripts, statements like `USE master` or `USE [SalesDB]` are accepted as **tolerant virtual no-ops**, synchronizing TDS environment tokens (`ENVCHANGE`) without error.

### Q11. Which production open-source applications are validated with LionSQL?
> **A.** 10 major applications run on LionSQL with zero container source modifications: **Gitea, Grafana, Metabase, Vaultwarden, Casdoor, Umami, Apache Answer, Woodpecker CI, Vikunja, and IOTA-SDK ERP**. In particular, **Grafana** serves as a core observability partner, visualizing LionSQL's embedded Prometheus time-series metrics (`:8090/metrics`) while concurrently running live analytical SQL dashboards.

---

## IV. Performance & Engineering Invariants

### Q12. What throughput (TPS / tpmC) does LionSQL achieve on a single node?
> **A.** 
> - **In-Memory OCC**: **218,000+ TPS** (TransferFunds SP), and on TPC-C sustains **1,066,000+ tpmC** / 39,700+ TPS (Storage Kernel), **759,000+ tpmC** / 28,100+ TPS (VDBE Runtime), and **376,000+ tpmC** / 13,900+ TPS over network wire (PGWire).
> - **WiredTiger**: **141,000+ TPS** (TransferFunds SP), and on TPC-C sustains **1,184,000+ tpmC** / 41,900+ TPS (Storage Kernel), **863,000+ tpmC** / 31,700+ TPS (VDBE Runtime), and **352,000+ tpmC** / 13,000+ TPS over network wire (PGWire).  
> It maximizes multi-core hardware saturation without distributed network overhead.

### Q13. Why were regular expressions completely eliminated from query execution?
> **A.** Regular expressions (Chomsky Type 3 regular languages) lack stack memory and cannot reliably parse context-free SQL grammars with balanced parentheses (Dyck languages). Regexes cause greedy truncation and literal/comment contamination. In LionSQL v0.91.0, regular expressions have been 100% eliminated across all production query paths and replaced with deterministic zero-allocation state machine tokenizers (`proc.Lexer`, `sqlutil`) (`Invariant 7`).

---

## V. Operations & Product Philosophy

### Q14. Why are distributed 2PC (XA) and multi-master sharding excluded?
> **A.** Under the **Scale-Up First & 1-Writer Multi-Reader philosophy**, 99% of business workloads benefit far more from vertical hardware saturation and operational simplicity than from distributed 2PC network latency and distributed deadlocks. Read scaling is handled via asynchronous/semi-synchronous WAL replication replicas.

### Q15. What is LionSQL's ultimate goal for version 1.0 and beyond?
> **A.** To be **"the leanest yet most uncompromising OLTP database engine."** Simple and self-contained to deploy like SQLite, while delivering the enterprise capabilities (T-SQL stored procedures, ACID durability, multi-wire protocols) and raw throughput of tier-one enterprise databases.

### Q16. Why embed a static web server and JavaScript SDK inside the database process?
> **A.** To eliminate the infrastructure complexity, network hop latency, and CORS preflight overhead inherent to traditional 3-tier stacks (Nginx ➔ API ➔ DB). Serving frontend assets and BaaS REST/SQL APIs from a single port (`:8090`) enables zero-hop in-memory dispatch, while OS zero-copy file transmission guarantees zero interference with core high-concurrency OLTP transactions.
