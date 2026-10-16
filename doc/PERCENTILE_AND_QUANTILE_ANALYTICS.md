# LionSQL Quantile & Percentile Analytics Specification

## 1. Architectural Overview

In high-concurrency OLAP and telemetry pipelines, computing percentiles (P50, P90, P95, P99) in client applications requires transferring uncompressed raw datasets across the network, leading to high network I/O, CPU overhead, and memory pressure.

LionSQL implements **in-engine exact and streaming approximate quantile operators** across both its Volcano execution engine and window function subsystem:
- **`PERCENTILE_CONT`**: ANSI SQL standard continuous percentile with linear interpolation.
- **`PERCENTILE_DISC`**: ANSI SQL standard discrete percentile (nearest rank).
- **`APPROX_PERCENTILE`**: High-performance streaming approximate percentile powered by the **T-Digest** algorithm with bounded memory footprint ($O(1)$ centroids).
- **`MEDIAN`**: Dedicated 50th-percentile aggregate and window operator.

---

## 2. Syntax & Specifications

### 2.1. `PERCENTILE_CONT` (Continuous Percentile)

Computes a percentile based on a continuous distribution of column values using linear interpolation between adjacent ranks.

#### Supported Syntaxes
```sql
-- 1. ANSI / Oracle / PostgreSQL / MSSQL WITHIN GROUP Syntax
SELECT dept, PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY salary) AS median_salary
FROM employees
GROUP BY dept;

-- 2. Modern 2-Argument Syntax (Snowflake / DuckDB / BigQuery)
SELECT dept, PERCENTILE_CONT(salary, 0.95) AS p95_salary
FROM employees
GROUP BY dept;

-- 3. Analytic Window Function Syntax
SELECT id, dept, salary,
       PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY salary) OVER (PARTITION BY dept) AS dept_median
FROM employees;
```

#### Mathematical Definition
For an ordered sequence $x_0, x_1, \dots, x_{n-1}$ of non-null values ($n = \text{len}(x)$) and target quantile $p \in [0.0, 1.0]$:
1. Target rank: $rn = p \times (n - 1)$
2. Floor index: $k = \lfloor rn \rfloor$
3. Fractional part: $d = rn - k$
4. Computed value:
   $$\text{PercentileCont}(p) = x_k + d \times (x_{k+1} - x_k)$$

---

### 2.2. `PERCENTILE_DISC` (Discrete Percentile)

Computes the discrete percentile by returning the first value whose cumulative distribution is greater than or equal to $p$.

#### Supported Syntaxes
```sql
-- ANSI WITHIN GROUP Syntax
SELECT dept, PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY salary) AS discrete_median
FROM employees
GROUP BY dept;

-- 2-Argument Syntax
SELECT dept, PERCENTILE_DISC(salary, 0.75) AS p75_salary
FROM employees
GROUP BY dept;
```

#### Mathematical Definition
1. Target rank index: $idx = \lceil p \times n \rceil - 1$ (0-indexed, clamped to $[0, n-1]$)
2. Computed value:
   $$\text{PercentileDisc}(p) = x_{idx}$$

---

### 2.3. `APPROX_PERCENTILE` (T-Digest Streaming Percentile)

Calculates approximate percentiles for massive datasets and streaming telemetry logs without memory exhaustion.

#### Supported Syntaxes
```sql
-- Global Aggregation
SELECT APPROX_PERCENTILE(latency_ms, 0.99) AS p99_latency
FROM api_metrics;

-- Grouped Aggregation
SELECT endpoint,
       APPROX_PERCENTILE(latency_ms, 0.50) AS p50,
       APPROX_PERCENTILE(latency_ms, 0.90) AS p90,
       APPROX_PERCENTILE(latency_ms, 0.99) AS p99
FROM api_metrics
GROUP BY endpoint;

-- Window Function
SELECT request_id, endpoint, latency_ms,
       APPROX_PERCENTILE(latency_ms, 0.95) OVER (PARTITION BY endpoint) AS endpoint_p95
FROM api_metrics;
```

#### T-Digest Algorithm Details
- **Centroid Clustering**: Maintains centroids with mean $\mu_i$ and weight $w_i$.
- **Scale Function $k_1$**: Constrains centroid size using $q(1 - q)$, ensuring maximum precision at extreme tails (P0.1, P1, P99, P99.9).
- **Bounded Resource Footprint**: With default compression $\delta = 100$, the memory consumed per group is bounded to $< 4\text{ KB}$, regardless of whether $10^3$ or $10^8$ rows are ingested.
- **Fast Path**: For partitions with $\le 10,000$ points, exact linear interpolation is computed directly with 0% error.

---

### 2.4. `MEDIAN`

Shorthand aggregate and window operator equivalent to `PERCENTILE_CONT(col, 0.5)`.

#### Supported Syntaxes
```sql
SELECT dept, MEDIAN(salary) AS med_sal
FROM employees
GROUP BY dept;

SELECT id, dept, salary,
       MEDIAN(salary) OVER (PARTITION BY dept) AS dept_med
FROM employees;
```

---

## 3. Engine Integration & Performance

- **Dual-Parser & AST Compatibility**: Handled directly in Pratt parser AST (`*proc.WithinGroupExpr`) and Volcano `AggregateIterator` / `WindowIterator`.
- **Zero Heap Thrashing**: Numeric conversions leverage contiguous float buffers and lightweight centroid merges.
- **Concurrency Safety**: Evaluators and accumulators are fully isolated per Volcano iterator and verified under `go test -race`.
