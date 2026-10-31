# Casdoor + LionSQL Dual-Protocol Integration Guide (`PostgreSQL` & `MSSQL`)

> - **Created**: 2026-10-31
> - **Target Version**: LionSQL v1.0.x / Casdoor (Beego + XORM + Casbin)
> - **Supported Protocols**: PostgreSQL Wire Protocol v3 (`:5433`), Microsoft SQL Server TDS 7.x/8.0 (`:1433`)

---

## 1. Overview

**Casdoor** (`github.com/casdoor/casdoor`) is an open-source Identity and Access Management (IAM) / Single Sign-On (SSO) platform supporting OAuth 2.0, OIDC, SAML, CAS, LDAP, and RADIUS.
LionSQL supports Casdoor's **XORM + Casbin** persistence layer out of the box with zero modifications to Casdoor across both **PostgreSQL Wire Protocol (`:5433`)** and **Microsoft SQL Server TDS (`:1433`)**.

---

## 2. Starting LionSQL

```bash
./bin/lionsql -data-dir=data_casdoor -admin-pass=1234567890
```

- **PostgreSQL Wire Listener**: `127.0.0.1:5433`
- **MSSQL TDS Listener**: `127.0.0.1:1433`
- **Web Studio**: `http://127.0.0.1:8090/_/`

---

## 3. Configuring Casdoor (`conf/app.conf`)

### Mode A: PostgreSQL Wire Protocol (`:5433`)

Configure `driverName` and `dataSourceName` in `conf/app.conf`:

```ini
appname = casdoor
httpport = 8000
runmode = dev
copyrequestbody = true
driverName = postgres
dataSourceName = "user=admin password=1234567890 host=127.0.0.1 port=5433 sslmode=disable dbname=casdoor"
dbName =
tableNamePrefix =
showSql = false
initAdminPassword = "123"
```

### Mode B: Microsoft SQL Server TDS (`:1433`)

Configure `driverName = mssql` in `conf/app.conf`:

```ini
appname = casdoor
httpport = 8000
runmode = dev
copyrequestbody = true
driverName = mssql
dataSourceName = "sqlserver://admin:1234567890@127.0.0.1:1433?database=casdoor&encrypt=disable"
dbName = casdoor
tableNamePrefix =
showSql = false
initAdminPassword = "123"
```

---

## 4. Running and Verifying Casdoor

```bash
# Start Casdoor backend (automatically syncs 44 tables and seeds built-in org, admin user, RSA-4096 cert, and Casbin policies)
./bin/casdoor
```

### 4.1 Verify OIDC Discovery and JWKS

```bash
curl -s http://127.0.0.1:8000/.well-known/openid-configuration | jq .
curl -s http://127.0.0.1:8000/.well-known/jwks | jq .
```

### 4.2 Verify Admin Login and Organization/Application/User CRUD APIs

```bash
# 1. Sign in as built-in/admin
curl -s -c /tmp/casdoor_cookie.txt -X POST http://127.0.0.1:8000/api/login \
  -H "Content-Type: application/json" \
  -d '{"application":"app-built-in","organization":"built-in","username":"admin","password":"123","autoSignin":true,"type":"login"}'

# 2. Fetch current account profile
curl -s -b /tmp/casdoor_cookie.txt "http://127.0.0.1:8000/api/get-account" | jq .

# 3. Create a new Organization
curl -s -b /tmp/casdoor_cookie.txt -X POST http://127.0.0.1:8000/api/add-organization \
  -H "Content-Type: application/json" \
  -d '{"owner":"admin","name":"lionsql-org","displayName":"LionSQL Organization","websiteUrl":"https://lionsql.io","passwordType":"plain"}'

# 4. Create a new Application
curl -s -b /tmp/casdoor_cookie.txt -X POST http://127.0.0.1:8000/api/add-application \
  -H "Content-Type: application/json" \
  -d '{"owner":"admin","name":"lionsql-app","displayName":"LionSQL App","organization":"lionsql-org","cert":"cert-built-in","enablePassword":true}'

# 5. Create and retrieve a new User
curl -s -b /tmp/casdoor_cookie.txt -X POST http://127.0.0.1:8000/api/add-user \
  -H "Content-Type: application/json" \
  -d '{"owner":"lionsql-org","name":"lionsql-user","displayName":"LionSQL User","email":"user@lionsql.io","password":"123","type":"normal-user","signupApplication":"lionsql-app"}'

curl -s -b /tmp/casdoor_cookie.txt "http://127.0.0.1:8000/api/get-user?id=lionsql-org/lionsql-user" | jq .
```
