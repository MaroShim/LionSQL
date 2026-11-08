# LionSQL & SSMS (SQL Server Management Studio) Integration Guide (`SSMS_INTEGRATION_GUIDE.md`)

This document is the official operational guide for connecting Microsoft's flagship database tool, **[SQL Server Management Studio (SSMS)](https://learn.microsoft.com/en-us/sql/ssms/download-sql-server-management-studio-ssms)**, to **LionSQL**'s native Microsoft SQL Server TDS (Tabular Data Stream) listener (`TCP :1433`).

> [Korean Version (한국어)](SSMS_INTEGRATION_GUIDE.ko.md)

---

## 1. Overview

LionSQL natively listens on Microsoft's official **TDS 7.x and 8.0 protocol (`TCP :1433`)** without requiring external emulators, proxy drivers, or translation layers. Standard Windows enterprise installations of SSMS 18.x, 19.x, and 20.x connect to LionSQL exactly like a vanilla Microsoft SQL Server instance, supporting Object Explorer navigation, T-SQL query execution, stored procedures, and transaction management.

```
[ Client Environment: Windows ]
   ┌────────────────────────────────────────────────────────┐
   │ Microsoft SQL Server Management Studio (SSMS 18/19/20) │
   │  - Object Explorer                                     │
   │  - T-SQL Query Editor                                  │
   └──────────────────────────┬─────────────────────────────┘
                              │ Microsoft TDS Protocol (:1433)
                              ▼
[ Server Environment: LionSQL Standalone Binary (Windows / macOS / Linux) ]
   ┌────────────────────────────────────────────────────────┐
   │ TDS 7.x / 8.0 Wire Listener (:1433)                    │
   │  ├── PreLogin & SSL/TLS Handshake Negotiation          │
   │  ├── Token Stream Parser & Virtual Catalog DMV Router   │
   │  └── T-SQL VDBE Bytecode Engine & In-Memory/WT Core    │
   └────────────────────────────────────────────────────────┘
```

---

## 2. Prerequisites

1. **Start LionSQL Server**:
   Ensure the LionSQL binary is running with the TDS listener enabled on port 1433:
   ```bash
   # Windows PowerShell / CMD
   .\bin\lionsql.exe server --data-dir .\data --tds-port 1433

   # Linux / macOS
   ./bin/lionsql server --data-dir ./data --tds-port 1433
   ```
2. **SSMS Installation**:
   - Download and install SSMS 19.x or 20.x from the official Microsoft download page.
3. **Network & Firewall**:
   - Verify `TCP 1433` is open and accessible from your client machine.

---

## 3. Step-by-Step Connection Guide

Launch SSMS and configure the **Connect to Server** dialog as follows:

### 3.1. Login Tab

| Option | Value / Setting | Notes |
| :--- | :--- | :--- |
| **Server type** | `Database Engine` | Default |
| **Server name** | `127.0.0.1,1433` or `localhost,1433` | **Important**: Use a **comma (`,`)**, not a colon (`:`), for the port delimiter. |
| **Authentication** | `SQL Server Authentication` | Native TDS login |
| **Login** | `sa` (or configured database user) | Default administrative account |
| **Password** | `1234567890` (or configured password) | Default initial administrator password |

### 3.2. Connection Properties Tab (Only 1 Required Change)

Leave all other network, timeout, and database settings at their **default values**. For SSMS 19.x and 20.x, modify **only one single setting**:

1. Click **Options >>** at the bottom-right of the connection window.
2. Switch to the **Connection Properties** tab.
3. Check the box for **`Trust server certificate` (`True`)**.
   > **Reason**: Prevents `pre-login handshake` certificate validation rejections in local/self-signed TLS environments.
4. Click **Connect** to open the session immediately.

---

## 4. SSMS Object Explorer Support

LionSQL includes built-in metadata catalog and Dynamic Management Views (DMVs) required by the SSMS Object Explorer:

### 4.1. Supported System DMVs & Views
- **Host & System Environment**:
  - `sys.dm_os_host_info`: Operating system platform (`Windows`, `Linux`, `macOS`), architecture (`64-bit`), and distribution release strings.
  - `sys.dm_os_sys_info`: Logical CPU count, physical/virtual memory capacity, and server start timestamp.
- **Sessions & Connections**:
  - `sys.dm_exec_sessions`: Active session SPID, login name, client host, and status (`running`, `sleeping`).
  - `sys.dm_exec_connections`: Client IP address, transport protocol (`TCP`), and connection timestamp.
- **Catalog Navigation**:
  - `sys.databases` / `sysdatabases`: Database listing and online status.
  - `sys.tables` / `sysobjects`: User tables, object IDs, and schema IDs.
  - `sys.views` / `sysviews`: View definitions and creation timestamps.
  - `sys.columns`: Column names, type IDs, maximum lengths, and nullability flags.
  - `sys.indexes` / `sys.index_columns`: Primary keys, unique indexes, and secondary indexes.
  - `sys.procedures` / `sys.parameters`: Stored procedure names and parameter schemas.
- **Security & Endpoints**:
  - `sys.server_principals`: Server logins (`sa`, `public`, `sysadmin`).
  - `sys.endpoints`: TDS TCP listener status (`STARTED`).

---

## 5. T-SQL Query Editor Usage

Click **New Query** or press `Ctrl + N` in SSMS to execute T-SQL statements directly:

### 5.1. DDL & Schema Creation
```sql
CREATE TABLE dbo.Customers (
    CustomerID INT PRIMARY KEY,
    CustomerName NVARCHAR(100) NOT NULL,
    Email VARCHAR(100) UNIQUE,
    Balance DECIMAL(18, 2) DEFAULT 0.00,
    CreatedAt DATETIME DEFAULT GETDATE()
);
GO
```

### 5.2. ACID Transactions & DML
```sql
BEGIN TRANSACTION;

INSERT INTO dbo.Customers (CustomerID, CustomerName, Email, Balance)
VALUES (1, N'Alice Smith', 'alice@example.com', 150000.00);

UPDATE dbo.Customers
SET Balance = Balance + 50000.00
WHERE CustomerID = 1;

COMMIT TRANSACTION;
GO

SELECT * FROM dbo.Customers WHERE CustomerID = 1;
GO
```

### 5.3. Stored Procedures
```sql
CREATE PROCEDURE dbo.usp_AddCustomer
    @ID INT,
    @Name NVARCHAR(100),
    @Email VARCHAR(100),
    @Balance DECIMAL(18, 2)
AS
BEGIN
    SET NOCOUNT ON;
    INSERT INTO dbo.Customers (CustomerID, CustomerName, Email, Balance)
    VALUES (@ID, @Name, @Email, @Balance);
END;
GO

EXEC dbo.usp_AddCustomer @ID = 2, @Name = N'Bob Jones', @Email = 'bob@example.com', @Balance = 200000.00;
GO
```

---

## 6. Troubleshooting

### Q1. Error `sys.dm_os_host_info` 50000 causes the connection to abort.
- **Cause**: Older LionSQL builds lacked the `sys.dm_os_host_info` DMV queried by recent SSMS versions upon connecting.
- **Solution**: Upgrade to the latest LionSQL v1.0 binary (`bin/lionsql.exe`), which includes full built-in support for `sys.dm_os_host_info`.

### Q2. `A connection was successfully established with the server, but then an error occurred during the pre-login handshake`.
- **Cause**: SSMS 19/20 enforces certificate validation by default, which rejects self-signed certificates.
- **Solution**:
  1. Open **Options >>** on the SSMS connection window.
  2. Under the **Connection Properties** tab, check **Trust server certificate (`True`)**.
  3. Set **Encrypt** to `Optional` and reconnect.

### Q3. Port specification with a colon (`:1433`) fails to connect.
- **Cause**: Microsoft SQL Server address syntax requires a **comma (`,`)**, not a colon, for port numbers.
- **Solution**: Specify `127.0.0.1,1433` instead of `127.0.0.1:1433`.

### Q4. Behavior of `USE [DatabaseName]` in SSMS.
- **Invariant**: LionSQL enforces a single catalog per data directory process boundary ([`GEMINI.md`](../GEMINI.md)).
- **Behavior**: For 100% SSMS migration script compatibility, `USE master` and `USE [SalesDB]` operate as **tolerant virtual no-ops**, synchronizing TDS session environment tokens without raising syntax errors.

---

## 7. Related Documentation
- [Client Tool Integration Guide (`CLIENT_TOOL_INTEGRATION.md`)](../docs/CLIENT_TOOL_INTEGRATION.md)
- [T-SQL Language Reference (`TSQL_REFERENCE.md`)](../docs/TSQL_REFERENCE.md)
- [LionSQL Web Studio Guide (`WEB_STUDIO_GUIDE.md`)](../docs/WEB_STUDIO_GUIDE.md)
