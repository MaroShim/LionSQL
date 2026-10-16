# SQLion Security Policy & Threat Modeling

This document outlines SQLion's security architecture, threat mitigations, authentication mechanisms, hardening guidelines, and responsible vulnerability disclosure policy.

---

## 1. Security Architecture & Threat Mitigations

SQLion enforces defense-in-depth principles across its compiler, network protocols, execution VM, and storage engines.

### 1.1. SQL Injection Immunity
- **Parameter Substitution**: The PostgreSQL Extended Query Protocol (`Parse`, `Bind`, `Execute`) and TDS RPC protocols separate SQL AST parsing from parameter evaluation. Arguments are passed as typed parameters (`$1`, `$2` or `@from`, `@to`) rather than string concatenations.
- **AST Quoting & Escaping**: When literals must be interpolated internally (`substituteParams`), single quotes are escaped (`''`) and numeric types are validated via `strconv.ParseFloat` before substitution.

### 1.2. Shell Injection Immunity (Golden Rule 3)
SQLion **never** invokes external operating system shells (`sh -c`, `cmd.exe`, `bash`). When external processes are spawned (e.g. daemon fork management), parsed argument vectors are passed directly to `exec.Command` without shell interpretation.

### 1.3. Defensive Path Security & Traversal Guards (Golden Rule 5)
- All user-supplied file paths (backup files, restore archives, data directories) are normalized using `filepath.Clean`.
- Path traversal sequences (`..`) and unexpected absolute paths in tar archives are rejected.
- All created files enforce non-executable `0644` permissions, and directories enforce `0755` permissions.

### 1.4. Non-Destructive Atomic File Safety (Golden Rule 1)
Database state snapshots (`tables.mpk`), backup archives, and configuration files are written to temporary swap files (`*.tmp`) and flushed via `file.Sync()` before executing an atomic filesystem rename (`os.Rename`). A sudden power interruption or system crash cannot produce zero-byte truncated database files.

### 1.5. Data-at-Rest Encryption (Storage Subsystem Standard)
SQLion delegates data-at-rest encryption to the underlying OS and cloud block storage subsystems (Linux LUKS2 / dm-crypt, AWS EBS Encryption, GCP CMEK, Azure Managed Disk Encryption) rather than in-process software TDE. This eliminates double-encryption CPU overhead, harnesses dedicated AES-NI hardware instructions, and isolates key custody from the database runtime. See the comprehensive [Storage Encryption Guide](STORAGE_ENCRYPTION_GUIDE.md) for full deployment procedures.

---

## 2. Authentication & Access Control

### 2.1. Superuser Authentication
- **Password Hashing**: Passwords are encrypted using `bcrypt` with cost factor 12. Plaintext passwords are never persisted.
- **JWT Tokens**: Authentication produces signed JWT tokens using HMAC-SHA256 with configurable validity periods.
- **Token Invalidation**: Calling `POST /api/auth/logout` adds the active token ID to an in-memory revocation blocklist.

### 2.2. Granular API Key Scoping
For client-side applications and microservices:
- **Roles**:
  - `superuser`: Unrestricted access across all REST APIs and tables.
  - `readonly`: Permitted only to execute `GET` queries. Mutations are rejected with `403 Forbidden`.
- **Table Whitelists**: API keys can be locked to a specific slice of tables (`allowed_tables: ["products", "categories"]`). Requests targeting unlisted tables are rejected.

---

## 3. Network Security & Production Hardening

### 3.1. TLS/SSL Termination Architecture
SQLion serves unencrypted wire protocols (`:5433`, `:1433`) and HTTP (`:8090`) designed for high-throughput private VPC deployment. For external traffic:
- Terminate TLS/HTTPS at a reverse proxy (Caddy, Nginx, Envoy, or AWS ALB).
- Never expose ports `8090`, `5433`, or `1433` directly to the public internet without a firewall or VPN.

### 3.2. Recommended Production Firewall Rules

```bash
# Allow internal VPC application servers to reach Postgres Wire
sudo ufw allow from 10.0.0.0/16 to any port 5433 proto tcp

# Allow internal VPC application servers to reach TDS Wire
sudo ufw allow from 10.0.0.0/16 to any port 1433 proto tcp

# Restrict HTTP API / Web Studio to admin subnet or localhost
sudo ufw allow from 10.0.1.0/24 to any port 8090 proto tcp
```

---

## 4. Vulnerability Disclosure Policy

If you discover a security vulnerability in SQLion, please report it responsibly rather than opening a public issue.

### Reporting Procedure
- **Email**: Send detailed reproduction steps, proof-of-concept scripts, and affected versions to `security@sqlion.local` (or create a private GitHub Security Advisory).
- **Response Timeline**:
  - Initial acknowledgement: Within 24 hours.
  - Triage and impact assessment: Within 48 hours.
  - Coordinated patch release: Within 7 calendar days.
- **Credit**: We will credit security researchers in release notes and commit logs (unless anonymity is requested).
