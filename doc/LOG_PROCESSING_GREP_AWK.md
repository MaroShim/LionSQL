# SQLion In-Engine Log Processing: GREP and AWK Reference Guide

This document specifies the design, syntax, and operational usage of SQLion's native in-engine log processing operators: `GREP` and `AWK` (also aliased as `SPLIT_PART`).

---

## 1. Architectural Motivation & Zero-Copy Pushdown

Traditional database architectures force engineers to export raw logs or large text columns to external operating system pipelines:

```
[ Traditional Pipeline ]
Database Disk ──► Row Deserialization ──► String Encoding ──► Socket/Pipe ──► OS Context Switch ──► grep / awk
```

This traditional workflow introduces massive I/O serialization penalties, high network bandwidth usage, and memory fragmentation (GC pressure).

SQLion brings Unix-style text and log processing directly into the database engine:

```
[ SQLion In-Engine Pipeline ]
Storage Pages ──► Zero-Copy Byte Scan ──► In-Engine GREP/AWK Filter ──► Materialized Result Set
                                            (Discard unneeded rows)
```

### Key Architectural Advantages
1. **Zero-Copy Row Discard**: Records that do not match the `GREP` pattern are rejected at the volcano scan/filter iterator level before constructing full result tuples or row dictionaries.
2. **Schema-Free Log Analytics**: Raw unstructured lines (e.g., Nginx, Envoy, Syslog, application panic traces) can be stored in a single `raw_log VARCHAR` column without upfront normalization.
3. **Pipelined Aggregation**: Columns extracted on the fly via `AWK` can be directly passed into `GROUP BY`, `ORDER BY`, and aggregate functions (`COUNT`, `SUM`, `AVG`).

---

## 2. Operator Specifications

### 2.1. `GREP(text, pattern)`

Evaluates whether `text` matches the regular expression or substring `pattern`. Returns a boolean value (`true` / `false`).

#### Syntax
```sql
GREP(text_expression, pattern_string)
```

#### Arguments
- `text_expression`: The input string or table column to search against. If `NULL`, returns `false`.
- `pattern_string`: A standard regular expression pattern (Go RE2 standard). If empty (`''`), returns `true`. If the pattern is an invalid regular expression, SQLion falls back to a safe case-insensitive literal substring search.

#### Return Type
- `BOOLEAN` (`true` or `false`).

#### Examples
```sql
-- Regular expression matching HTTP 5xx server errors
SELECT * FROM access_logs WHERE GREP(raw_log, 'HTTP/1.1 5[0-9]{2}');

-- Disjunction matching for panic or fatal logs
SELECT timestamp, message FROM app_logs WHERE GREP(message, 'panic|fatal|OOM');

-- Combining with standard boolean logic
SELECT * FROM error_logs WHERE GREP(payload, 'timeout') AND NOT GREP(payload, 'test_mode');
```

---

### 2.2. `AWK(text, [delimiter], field_index)` / `SPLIT_PART`

Extracts the N-th field from a delimited string using 1-based indexing ($1, $2, \dots).

#### Syntax
```sql
-- 1. Default whitespace delimiter (similar to standard awk '{print $N}')
AWK(text_expression, field_index)

-- 2. Custom delimiter (character or substring)
AWK(text_expression, delimiter_string, field_index)

-- 3. PostgreSQL compatibility alias
SPLIT_PART(text_expression, delimiter_string, field_index)
```

#### Arguments
- `text_expression`: The input text line or column. If `NULL`, returns an empty string `''`.
- `delimiter_string` *(optional)*: The separator string. If omitted, whitespace (spaces and tabs) is used, and multiple consecutive whitespace characters are collapsed into a single delimiter (standard `awk` behavior).
- `field_index`: 1-based field position ($1 is the first field). If `field_index <= 0` or exceeds the total number of fields, an empty string `''` is returned safely without raising runtime errors.

#### Return Type
- `VARCHAR`

#### Examples
```sql
-- Extracting client IP ($1) and HTTP status code ($4) with default whitespace delimiter
-- Example row: "192.168.1.100 GET /api/v1/checkout 200 1024"
SELECT AWK(raw_log, 1) AS client_ip,
       AWK(raw_log, 4) AS status_code
FROM access_logs;

-- Extracting fields with custom delimiter (colon, comma, pipe)
-- Example row: "root:x:0:0:root:/root:/bin/bash"
SELECT AWK(entry, ':', 1) AS username,
       SPLIT_PART(entry, ':', 7) AS login_shell
FROM system_users;
```

---

## 3. End-to-End Real World Use Cases

### 3.1. Web Server Access Log Aggregation

Analyze top client IP addresses generating 5xx server errors directly from raw log entries:

```sql
SELECT AWK(raw_log, 1) AS client_ip,
       COUNT(*) AS error_count
FROM web_access_logs
WHERE GREP(raw_log, ' 50[0-9] ')
GROUP BY AWK(raw_log, 1)
ORDER BY error_count DESC
LIMIT 10;
```

### 3.2. Microservice Error Monitoring with Window Functions

Identify error frequency per service component parsed directly from structured log prefixes:

```sql
SELECT AWK(raw_log, 3) AS service_name,
       COUNT(*) AS incident_count,
       ROW_NUMBER() OVER (ORDER BY COUNT(*) DESC) AS severity_rank
FROM cluster_logs
WHERE GREP(raw_log, 'level=(error|fatal)')
GROUP BY AWK(raw_log, 3);
```

### 3.3. CSV Payload Decomposition in Stored Procedures

Extract transactional metadata without declaring multiple auxiliary normalization tables:

```sql
CREATE PROCEDURE ProcessBatchPayload
    @payload VARCHAR
AS
BEGIN
    DECLARE @batch_id VARCHAR;
    DECLARE @sender_id VARCHAR;
    
    SET @batch_id = AWK(@payload, ',', 1);
    SET @sender_id = AWK(@payload, ',', 2);
    
    IF GREP(@payload, 'STATUS=ACTIVE')
    BEGIN
        INSERT INTO processed_batches (batch_id, sender_id, status)
        VALUES (@batch_id, @sender_id, 'ACTIVE');
    END;
END;
```

---

## 4. Performance & Engineering Best Practices

1. **Predicate Pushdown**: Place `GREP` directly inside `WHERE` clauses so the volcano `FilterIterator` can reject non-matching rows early before projection allocations.
2. **Deterministic Regex Compilation**: SQLion caches parsed regular expressions in evaluation loops, avoiding repeated pattern re-compilations over high-volume scans.
3. **Safe Fallback**: If an invalid regex pattern is provided (such as an unclosed bracket), SQLion automatically falls back to an exact case-insensitive substring search rather than panicking or terminating active client transactions.
