# Casdoor Dual-Protocol Integration Milestone Report (`CASDOOR-INTEGRATION-1`)

> - **Created**: 2026-10-31
> - **Task ID**: `CASDOOR-INTEGRATION-1`
> - **Status**: Completed (`[x]`)

---

## 1. Overview & Objectives

We integrated **Casdoor** (`github.com/casdoor/casdoor`, built on Beego + XORM + Casbin) with **LionSQL** and verified 100% compatibility across both wire protocols without modifying Casdoor's source code:
1. **PostgreSQL Wire Protocol v3 (`:5433`, `driverName = postgres`)**
2. **Microsoft SQL Server TDS 7.x/8.0 (`:1433`, `driverName = mssql`)**

All 44 XORM tables, composite primary keys, secondary indexes, initial seed records (`built-in` organization, `admin` user, 4096-bit RSA X.509 certificate, Casbin RBAC rules), OIDC/JWKS endpoints, and authenticated REST CRUD APIs operate cleanly on both protocols.

---

## 2. Key Engine Enhancements

### 2.1 Composite Primary Key Storage Resolution
- **Root Cause**: Casdoor defines ~40 of its 44 tables with multi-column primary keys such as `PRIMARY KEY (owner, name)` or `PRIMARY KEY (owner, name, application)`. Previously, `TableSchema.ResolveRecordPK` and the storage engines (`wiredtiger` and `memengine`) only extracted the first PK column (`owner`), causing collisions when inserting multiple rows sharing the same `owner` (e.g., `admin/provider_captcha_default` and `admin/provider_balance`).
- **Resolution**:
  - Added `TableSchema.IsCompositePK() bool` and updated `TableSchema.ResolveRecordPK(rec)` in `pkg/storage/engine.go` to join all primary key column values using the ASCII Unit Separator (`"\x1f"`, safe for WiredTiger `key_format=S` C-strings) when a table has a composite primary key.
  - Updated `pkg/storage/wiredtiger/wt_engine.go` and `pkg/storage/memengine/tx.go` to resolve primary keys uniformly via `ResolveRecordPK(record)`.
  - Guarded single-column primary key point-lookup shortcuts (`isPKCol`, `tryPointLookup`, `tryPKLookupBypass`, and `execTruncateTable`) when `schema.IsCompositePK()` is true so single-column `WHERE` filters on one part of a composite PK properly scan/filter rows.

### 2.2 MSSQL TDS `0x` Binary Literal Lexing & `TypeBigVarBin` Round-Trip
- **Root Cause**: Under XORM's MSSQL dialect, struct slice fields tagged with `xorm:"blob"` (`Addresses`, `WebauthnCredentials`, `ManagedAccounts`, `MfaAccounts`) map to `VARBINARY(50)` and are serialized as JSON bytes (`[]byte("null")` -> `0x6e756c6c`).
  1. The Tier-1 lexer (`pkg/proc/lexer.go`) did not tokenize unquoted `0x...` hex literals as string/binary tokens for VDBE DML coercion.
  2. The TDS result encoder (`pkg/wire/tds/server.go`, `token.go`) sent `"0x6e756c6c"` as `TypeNVarChar` instead of decoding it into raw `TypeBigVarBin` (`0xA5`) bytes, and heuristically coerced string `id` / `*_time` columns into binary GUID / time-only tokens.
- **Resolution**:
  - Updated `pkg/proc/lexer.go`, `pkg/proc/runtime/vm_dispatcher.go`, and `pkg/proc/runtime/vm.go` to tokenize `0x[0-9a-fA-F]+` literals and coerce them via `storage.BinaryValueFromString` for `TypeBinary` columns.
  - Updated `pkg/wire/tds/server.go` and `pkg/wire/tds/token.go` to decode `"0x..."` binary values into raw `TypeBigVarBin` (`0xA5`) payloads and preserve `TypeNVarChar` for string `id` and ISO-8601 `*_time` columns.

---

## 3. Verification Summary

| Verification Item | Mode A: PostgreSQL (`:5433`) | Mode B: MSSQL TDS (`:1433`) |
| :--- | :--- | :--- |
| **XORM `Sync2` (44 Tables & Indexes)** | PASS (100%) | PASS (100%) |
| **Initial Seed Data (`built-in`, `admin`, `cert-built-in`, Casbin Rules)** | PASS (100%) | PASS (100%) |
| **OIDC Discovery (`/.well-known/openid-configuration`) & JWKS** | PASS (`HTTP 200 OK`) | PASS (`HTTP 200 OK`) |
| **Admin Login (`POST /api/login`) & Session (`/api/get-account`)** | PASS (`status: "ok"`) | PASS (`status: "ok"`) |
| **Organization / Application / User CRUD APIs** | PASS (`status: "ok"`) | PASS (`status: "ok"`) |
| **3-Stage Verification (`go test -tags wiredtiger -race ./...`)** | PASS (0 Race) | PASS (0 Race) |
