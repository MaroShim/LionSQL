# Metabase BI Integration Guide (`METABASE_INTEGRATION_GUIDE.md`)

This document is the official administration guide for connecting **[Metabase](https://www.metabase.com/)** (open-source business intelligence and analytics dashboard) to LionSQL.

LionSQL natively supports dual client protocols:
- **PostgreSQL Wire Protocol v3** (`TCP :5433`, `org.postgresql.Driver`)
- **Microsoft SQL Server TDS 7.x/8.0** (`TCP :1433`, `com.microsoft.sqlserver.jdbc.SQLServerDriver`)

You can connect Metabase via either driver without modifying Metabase source code or installing external database proxies.

---

## 1. Prerequisites

- **LionSQL Standalone Binary** (v0.90.0+)
- **Docker** and Docker Compose (to run the official Metabase container)

---

## 2. Start the LionSQL Server

Start the LionSQL kernel daemon with both PGWire and TDS listeners enabled:

```bash
# Production mode with WiredTiger storage engine (:5433 for PG, :1433 for TDS)
./bin/lionsql \
  -dir data_metabase \
  -engine wiredtiger \
  -pg 0.0.0.0:5433 \
  -tds 0.0.0.0:1433 \
  -admin-pass="YourSecurePassword2026!"
```

---

## 3. Run the Official Metabase Container

Start stock official Metabase:

```bash
docker run -d --name metabase \
  -p 3000:3000 \
  metabase/metabase:latest
```

Open `http://localhost:3000` in your web browser and finish the initial setup wizard to create your admin account.

---

## 4. Add Database in Metabase

In Metabase, navigate to `Settings` ➔ `Admin Settings` ➔ `Databases` ➔ `Add database`. Choose either connection method:

### Method A: PostgreSQL Driver (Recommended)
- **Database type**: `PostgreSQL`
- **Display name**: `LionSQL-PG`
- **Host**: `host.docker.internal` (or your LionSQL host IP)
- **Port**: `5433`
- **Database name**: `master` (or any database name, virtual USE is supported)
- **Username**: `admin` (or `admin@lionsql.local`)
- **Password**: `YourSecurePassword2026!`
- **Use a secure connection (SSL)**: Disabled

### Method B: SQL Server Driver (TDS)
- **Database type**: `SQL Server`
- **Display name**: `LionSQL-MSSQL`
- **Host**: `host.docker.internal` (or your LionSQL host IP)
- **Port**: `1433`
- **Database name**: `master`
- **Username**: `admin`
- **Password**: `YourSecurePassword2026!`
- **Use a secure connection (SSL)**: Disabled

Click **Save**. Metabase will immediately verify the connection and initiate schema synchronization.

---

## 5. Visual Query Builder (MBQL) & Native SQL Queries

Once connected, all Metabase visual capabilities are immediately available:

1. **Automatic Schema & Field Introspection**: Tables, columns, and data types (`Integer`, `Text`, `Decimal`, `DateTime`) are automatically cataloged.
2. **Visual Aggregations**:
   - Select `orders` table ➔ `Summarize` ➔ `Count of rows` by `status`
   - Select `orders` table ➔ `Summarize` ➔ `Sum of total_amount` by `status`
3. **Multi-Table Joins**:
   - `orders` ➔ `Join data` ➔ `customers` on `customer_id = id` ➔ `Summarize` by `country`
4. **Time-Series Analysis**:
   - Visualize monthly, weekly, or daily order metrics grouped by `order_date` (`date_trunc`).
5. **Native SQL Queries**:
   - Use `+ New` ➔ `SQL query` to run complex subqueries, CTEs, and window functions directly against LionSQL.

---

## 6. Troubleshooting

- **Container to Host Networking**: On macOS and Windows Docker, use `host.docker.internal` instead of `localhost`. On Linux, add `--add-host=host.docker.internal:host-gateway` to your `docker run` command.
- **Force Immediate Schema Re-sync**: If you alter table schemas or add new tables in LionSQL, go to `Admin Settings` ➔ `Databases` ➔ Select your database ➔ Click `Sync database schema now`.
