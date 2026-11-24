# LionSQL Data Import, Bulk Ingestion & Migration Guide

> - **Created**: 2026-09-22
> - **Last Updated**: 2026-11-24
> - **Engine Release**: v0.93.0 (Bulk Ingestion Pipeline & 1.0-GA Ready)
> - **한국어 문서**: [doc/DATA_IMPORT_AND_MIGRATION.ko.md](DATA_IMPORT_AND_MIGRATION.ko.md)

This comprehensive guide outlines how to migrate schemas, stream high-throughput bulk data, and port stored procedures to LionSQL from PostgreSQL, Microsoft SQL Server, SQLite, and MySQL.

---

## 1. Unified Bulk Ingestion Architecture

LionSQL provides native, high-throughput bulk ingestion across standard SQL, PostgreSQL Wire Protocol v3, and Microsoft SQL Server TDS 7.x/8.0 without requiring external ETL tools, proxy daemons, or intermediate disk storage.

```
 [PostgreSQL Ecosystem]                [Standard SQL / T-SQL]                [SQL Server Ecosystem]
  psql \copy / pg_dump               INSERT INTO t VALUES (..)              bcp.exe / SqlBulkCopy
         │                                       │                                     │
         ▼                                       ▼                                     ▼
   [PGWire :5433]                         [SQL Exec DML]                         [TDS :1433]
  COPY FROM STDIN                         sqlexec_dml.go                       0x07 BCP Packet
         │                                       │                                     │
         └─────────────────┬─────────────────────┴─────────────────────────────────────┘
                           │
                           ▼ Chunked Batch Slice ([]storage.Row, ChunkSize = 1,000)
             ┌───────────────────────────────────────────┐
             │   tx.BatchInsertRows(tableName, rows)     │
             │   (pkg/storage: memengine & wiredtiger)   │
             └─────────────────────┬─────────────────────┘
                                   │
         ┌─────────────────────────┴─────────────────────────┐
         ▼                                                   ▼
[Primary B-Tree Sequential Write]               [Secondary Indexes Pre-Sorted Batch Insertion]
- Single cursor acquisition per batch           - Collect (IndexKey, PK) pairs per index in memory
- IDENTITY / PK sequential leaf packing         - In-memory sorting (sort.Slice) in O(N log N)
- Zero cursor reopen & root contention          - Sequential cursor traversal in O(N) leaf append
```

### Key Architectural Invariants of the Bulk Pipeline:
1. **Pre-Sorted Secondary Index Maintenance**:
   - In traditional naive databases, inserting $N$ rows into a table with $K$ secondary indexes requires $N \times K$ random B-Tree lookups, causing severe page splits and root lock thrashing ($O(N \cdot K \log M)$).
   - LionSQL collects index key pairs in an arena buffer, sorts them in memory via `storage.CompareIndexKeys`, and performs a single forward-progress cursor pass over the index B-Tree. This transforms random disk writes into linear $O(N)$ sequential leaf page appends, eliminating page re-balancing.
2. **Primary Cursor Lifecycle Coalescing**:
   - Rather than opening and closing a B-Tree cursor for every individual record, the batch pipeline acquires a single write cursor for the entire 1,000-row chunk, setting keys and values in dense sequential order.
3. **Bounded Memory Streaming (`[RULE:BoundedBuffers]`)**:
   - Streaming wire protocols (PGWire `COPY` and TDS `BCP`) do not buffer unbounded tables in memory. Rows are streamed into 1,000-row chunks that are immediately committed to storage, guaranteeing predictable memory consumption (< 32 MB) even when loading multi-gigabyte or multi-billion row datasets.

---

## 2. Migrating from PostgreSQL

Because LionSQL natively speaks PostgreSQL Wire Protocol v3 on port `5433` and implements high-throughput `COPY` streaming, migrating from PostgreSQL is seamless and fast.

### 2.1. Data Type Mapping

| PostgreSQL Type | LionSQL Type | Storage Representation & Constraints |
|---|---|---|
| `INT2`, `SMALLINT` | `INT` | 64-bit integer (normalized) |
| `INT4`, `INTEGER` | `INT` | 64-bit integer (normalized) |
| `INT8`, `BIGINT` | `INT` | 64-bit signed integer |
| `SERIAL`, `BIGSERIAL` | `INT IDENTITY(1, 1)` | Auto-incrementing 64-bit integer |
| `VARCHAR(n)`, `CHAR(n)` | `VARCHAR` | UTF-8 variable-length string |
| `TEXT` | `VARCHAR` | Arbitrary length UTF-8 string |
| `FLOAT4`, `REAL` | `FLOAT` | IEEE 754 64-bit float |
| `FLOAT8`, `DOUBLE PRECISION` | `FLOAT` | IEEE 754 64-bit float |
| `NUMERIC`, `DECIMAL` | `FLOAT` | 64-bit float |
| `BOOLEAN`, `BOOL` | `BOOL` | 1-byte boolean flag (`TRUE` / `FALSE`) |
| `TIMESTAMP`, `TIMESTAMPTZ` | `DATETIME` | RFC3339 UTC timestamp |
| `DATE` | `DATETIME` | RFC3339 UTC timestamp (00:00:00) |
| `BYTEA` | `VARBINARY` | Binary blob (hex encoded `\x...`) |
| `JSON`, `JSONB` | `VARCHAR` | Native JSON with indexing & path queries |

### 2.2. Exporting Schema & Applying DDL

1. **Export DDL Schema from PostgreSQL**:
   ```bash
   pg_dump -h pg.example.com -U postgres -d production_db --schema-only --no-owner --no-privileges > schema.sql
   ```
2. **Apply DDL to LionSQL**:
   ```bash
   psql -h 127.0.0.1 -p 5433 -U admin -d master -f schema.sql
   ```

### 2.3. Direct Stream Ingestion via `psql \copy`

LionSQL's `pgCopyReader` processes incoming `CopyData` (`'d'`) wire packets, parses rows using zero-allocation token splitting, chunks them into 1,000-row slices, and commits them via `tx.BatchInsertRows`.

#### Direct Pipe Migration (Zero Intermediate Files):
Stream tables directly from an existing PostgreSQL database into LionSQL across a Unix pipeline without touching the local disk:
```bash
psql -h pg.example.com -U postgres -d production_db -c "\copy accounts to stdout with (format csv, header)" | \
psql -h 127.0.0.1 -p 5433 -U admin -d master -c "\copy accounts from stdin with (format csv, header)"
```

#### Importing from Local CSV Files:
```bash
psql -h 127.0.0.1 -p 5433 -U admin -d master -c "\copy users from 'users.csv' with (format csv, header)"
```

#### Importing from Tab-Delimited Text:
```bash
psql -h 127.0.0.1 -p 5433 -U admin -d master -c "\copy logs from 'access.log' with (format text, delimiter E'\t')"
```

---

## 3. Migrating from Microsoft SQL Server (MSSQL)

LionSQL provides native Microsoft SQL Server TDS 7.x/8.0 wire compatibility on port `1433`, including official T-SQL stored procedures, system catalogs, and the **TDS 0x07 BCP (Bulk Copy Protocol)**.

### 3.1. Stored Procedures & Control Flow Compatibility

T-SQL stored procedures execute natively on LionSQL's VDBE bytecode virtual machine without rewriting:
- `DECLARE @var TYPE`, `SET @var = ...`, `SELECT @var = ...`
- Control flow: `IF...ELSE`, `WHILE`, `BREAK`, `CONTINUE`
- Error handling: `BEGIN TRY ... END TRY`, `BEGIN CATCH ... END CATCH`, `ERROR_MESSAGE()`, `RAISERROR`
- System variables: `@@ROWCOUNT`, `@@ERROR`, `@@TRANCOUNT`, `@@IDENTITY`, `@@FETCH_STATUS`
- Forward-only cursors: `DECLARE cur CURSOR FOR SELECT ...`, `OPEN cur`, `FETCH NEXT FROM cur INTO ...`, `CLOSE cur`, `DEALLOCATE cur`

Deploy procedures via `sqlcmd`, SSMS, or the HTTP API:
```bash
sqlcmd -S 127.0.0.1,1433 -U admin -P password -i procedures.sql
```

### 3.2. Native TDS BCP Streaming (`bcp.exe`)

LionSQL natively implements the TDS `0x07` BCP stream protocol and handles `INSERT BULK` queries directly. You can use standard Microsoft `bcp.exe` CLI tools without any proprietary drivers.

#### Character Mode Ingestion (CSV / Delimited Text):
Export from MSSQL:
```bash
bcp "SELECT * FROM SalesDB.dbo.Orders" queryout orders.csv -c -t, -S mssql.prod -U sa -P secret
```

Import directly into LionSQL:
```bash
bcp [SalesDB].[dbo].[Orders] in orders.csv -S 127.0.0.1,1433 -U admin -P password -c -t, -b 1000
```

#### Native Binary Mode Ingestion (High-Speed Compressed):
For maximum throughput, export and import using SQL Server native binary format (`-n`):
```bash
# Export from MSSQL in native binary format
bcp SalesDB.dbo.Customers out customers.dat -n -S mssql.prod -U sa -P secret

# Ingest into LionSQL in native binary format (5,000-row batching)
bcp [SalesDB].[dbo].[Customers] in customers.dat -n -b 5000 -S 127.0.0.1,1433 -U admin -P password
```

### 3.3. C# / .NET `SqlBulkCopy` Direct Streaming

LionSQL's TDS BCP implementation is 100% compatible with .NET `Microsoft.Data.SqlClient.SqlBulkCopy`:

```csharp
using System;
using System.Data;
using Microsoft.Data.SqlClient;

class Program
{
    static void Main()
    {
        string connStr = "Server=127.0.0.1,1433;Database=SalesDB;User Id=admin;Password=password;TrustServerCertificate=True;";
        
        using (var connection = new SqlConnection(connStr))
        {
            connection.Open();
            
            using (var bulkCopy = new SqlBulkCopy(connection))
            {
                bulkCopy.DestinationTableName = "dbo.Transactions";
                bulkCopy.BatchSize = 1000;
                bulkCopy.BulkCopyTimeout = 60;
                
                // Stream DataTable, IDataReader, or DataRow array
                DataTable dataTable = FetchSourceData();
                bulkCopy.WriteToServer(dataTable);
            }
        }
        Console.WriteLine("Bulk copy completed successfully.");
    }
}
```

---

## 4. High-Performance Multi-Row Batch Inserts (`INSERT INTO ... VALUES`)

LionSQL automatically optimizes standard SQL multi-row `INSERT` statements by detecting value lists and routing them directly into the vectorized `tx.BatchInsertRows` pipeline:

```sql
INSERT INTO bulk_items (sku, price, category)
VALUES 
    ('SKU-0001', 100, 'Electronics'),
    ('SKU-0002', 250, 'Hardware'),
    ('SKU-0003', 499, 'Audio'),
    -- ... up to 1,000+ rows in a single statement
    ('SKU-1000', 890, 'Accessories');
```

- **Execution Path**: `sqlexec_dml.go` parses all value tuples, checks schema constraints, and invokes `tx.BatchInsertRows` within a single storage transaction boundary.
- **Throughput**: Achieves **over 195,000 rows/sec** on the WiredTiger disk engine, including full SQL parsing and AST tokenization.

---

## 5. Migrating from SQLite

SQLite databases can be converted using SQL dumps or CSV exports.

### 5.1. Syntax Adjustments
1. **Auto-Incrementing Keys**:
   - In SQLite: `id INTEGER PRIMARY KEY AUTOINCREMENT`.
   - In LionSQL: `id INT IDENTITY(1, 1) PRIMARY KEY` or `id INT PRIMARY KEY` (LionSQL auto-generates 64-bit integer IDs if omitted).
2. **Type Affinities**:
   - SQLite dynamically accepts arbitrary types in any column. Ensure column definitions in LionSQL reflect proper strong types (`INT`, `VARCHAR`, `FLOAT`, `BOOL`, `DATETIME`).

### 5.2. Migration Steps
1. **Export SQLite Table to CSV**:
   ```bash
   sqlite3 mydatabase.db -header -csv "SELECT * FROM users;" > users.csv
   ```
2. **Create Matching Table in LionSQL**:
   ```sql
   CREATE TABLE users (
       id INT IDENTITY(1, 1) PRIMARY KEY,
       name VARCHAR NOT NULL,
       score FLOAT,
       created_at DATETIME NOT NULL
   );
   ```
3. **Import CSV via `psql` or `bcp`**:
   ```bash
   psql -h 127.0.0.1 -p 5433 -U admin -c "\copy users from 'users.csv' with (format csv, header)"
   ```

---

## 6. Migrating from MySQL / MariaDB

1. **Identifier Quoting**:
   Replace MySQL backticks (`` `col` ``) with standard double quotes (`"col"`) or T-SQL square brackets (`[col]`).
2. **Data Types**:
   - `TINYINT(1)` ➔ `BOOL`
   - `DATETIME`, `TIMESTAMP` ➔ `DATETIME`
   - `VARCHAR(...)`, `LONGTEXT` ➔ `VARCHAR`
   - `BIGINT UNSIGNED` ➔ `INT` (64-bit signed)
3. **Stored Procedures**:
   Convert MySQL procedural syntax (`DELIMITER //`, `DECLARE CONTINUE HANDLER`) to LionSQL's standard T-SQL dialect (`BEGIN TRY...END TRY`, `@@FETCH_STATUS`).
4. **Data Loading**:
   Export using `mysqldump` or `SELECT INTO OUTFILE`, then ingest via `psql \copy` or multi-row `INSERT`.

---

## 7. Bulk Ingestion Performance Tuning & Best Practices

| Recommendation | Optimal Value | Architectural Reason |
|---|---|---|
| **Batch Size (`-b` / Chunk)** | `1,000` to `5,000` rows | Maximizes CPU cache and arena reuse while maintaining bounded memory (< 32 MB) |
| **Secondary Indexes** | Keep active | Pre-sorted batch pipeline eliminates leaf splits; no need to drop and rebuild indexes |
| **Transaction Boundary** | Per batch (1,000 - 5,000) | Avoids holding exclusive locks on billions of records in a single monolithic transaction |
| **Network Socket Cooldown** | 5 seconds between runs | Prevents host OS ephemeral port starvation (`TIME_WAIT` socket drain) |
| **Storage Engine** | `wiredtiger` (Disk) / `memory` (RAM) | WiredTiger achieves >244k rows/sec on NVMe; In-Memory OCC achieves >210k rows/sec |
