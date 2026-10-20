# LionSQL Enterprise Authentication SPI & External Directory Integration

This specification details LionSQL's authentication architecture, the Service Provider Interface (SPI) for enterprise directory integration (LDAP, Active Directory, PAM), and the security perimeter design that decouples core database authentication from third-party social identity providers.

---

## 1. Authentication Architecture Overview

LionSQL serves enterprise client workloads simultaneously across three distinct wire interfaces:
1. **PostgreSQL Wire Protocol v3** (TCP Port `5433`): Native drivers, ORMs, and BI tools.
2. **Microsoft SQL Server TDS 7.x/8.0** (TCP Port `1433`): Enterprise .NET, SSMS, Azure Data Studio, and legacy T-SQL clients.
3. **HTTP/REST Management Engine** (TCP Port `8090`): Web Studio dashboard, auto-generated CRUD APIs, and Server-Sent Events.

Rather than maintaining separate authentication silos, LionSQL unifies credential verification and role-based access control (RBAC) under a centralized authentication manager (`pkg/auth`):

```
                   [ Incoming Client Connection Request ]
                                      │
         ┌────────────────────────────┼────────────────────────────┐
         ▼                            ▼                            ▼
  [ PGWire Listener ]          [ TDS Listener ]            [ HTTP / REST API ]
    (Port :5433)                 (Port :1433)                 (Port :8090)
         │                            │                            │
   (SCRAM / Cleartext)         (Login7 / NTLM)           (Bearer JWT / API-Key)
         │                            │                            │
         └────────────────────────────┼────────────────────────────┘
                                      │
                                      ▼
                        [ Central AuthService Engine ]
                                (pkg/auth)
                                      │
               ┌──────────────────────┴──────────────────────┐
               ▼                                             ▼
     [ Local Identity Store ]                     [ AuthProvider SPI Chain ]
     - PocketBase Superusers                      - Pluggable Enterprise SPI
     - Argon2id / SHA256 Hashes                   - LDAP / Active Directory
     - Granular API Keys                          - Corporate Directory / PAM
```

---

## 2. Pluggable AuthProvider SPI Interface

The database kernel defines an extensible, network-agnostic interface (`pkg/auth/provider.go`) allowing organizations to bind corporate directory services without modifying core database engine code:

```go
package auth

// AuthProvider abstracts identity verification for local and external directory services.
// It enables LionSQL to plug in enterprise directory authentication without coupling
// the database kernel to external network dependencies.
type AuthProvider interface {
    // Name returns the unique provider identifier (e.g. "local", "ldap", "activedirectory").
    Name() string

    // Authenticate validates the provided credentials and returns an authenticated Login principal if valid.
    Authenticate(username, password string) (*Login, error)
}
```

### 2.1. Provider Registration & Evaluation Chain
Multiple providers can be registered in priority order during server initialization:

```go
authService := auth.NewAuthService()

// Register corporate LDAP provider before local fallback
authService.RegisterProvider(ldap.NewProvider(ldapConfig))
authService.RegisterProvider(local.NewProvider(storageEngine))
```

During credential validation, `AuthService` iterates through registered providers:
1. The first provider to return a valid `*Login` principal completes the authentication handshake.
2. If a provider returns `ErrInvalidCredentials`, evaluation advances to subsequent providers.
3. If all providers fail, connection establishment is rejected with standard wire protocol error tokens (`0xAA ERROR` in TDS, `ErrorResponse` in PGWire, `401 Unauthorized` in HTTP).

---

## 3. Multi-Protocol Identity Convergence

LionSQL maps diverse protocol-specific authentication challenges to the unified `AuthProvider` verification pipeline:

| Wire Protocol | Inbound Challenge | Handshake Protocol | Engine Validation Path |
|---|---|---|---|
| **PostgreSQL Wire** | Cleartext / MD5 / SCRAM | `AuthenticationOk` (0) or `ErrorResponse` (R) | `authService.ValidateUser(user, pass)` |
| **Microsoft TDS** | Login7 Record | `0xAD LOGINACK` or `0xAA ERROR` | `authService.ValidateUser(user, pass)` |
| **HTTP / REST** | HTTP Basic Auth | `401 Unauthorized` or `200 OK` | `authService.AuthenticateSuperuser(email, pass)` |
| **HTTP / Web Studio** | Bearer JWT Token | `Authorization: Bearer <jwt>` | `authService.ValidateToken(token)` |
| **Machine-to-Machine** | API Key Secret | `X-Api-Key: lionsql_<token>` | `authService.ValidateAPIKey(key)` |

### 3.1. Superuser Parity
When a cluster is initialized with an administrative identity (e.g. `admin / secret`), that single administrative identity is recognized identically across all wire protocols:
* Connecting via `psql -p 5433 -U admin`
* Connecting via `sqlcmd -S 127.0.0.1,1433 -U admin`
* Logging into Web Studio at `http://127.0.0.1:8090/_/` with `admin`

---

## 4. Custom Enterprise Provider Implementation Guide

To implement an external corporate authentication provider (e.g. corporate OpenLDAP or Active Directory), implement the `auth.AuthProvider` interface:

```go
package ldapprovider

import (
    "crypto/tls"
    "fmt"
    "lionsql/pkg/auth"
    "github.com/go-ldap/ldap/v3"
)

type LDAPProvider struct {
    serverURL string
    bindDN    string
    baseDN    string
}

func NewLDAPProvider(url, bindDN, baseDN string) *LDAPProvider {
    return &LDAPProvider{
        serverURL: url,
        bindDN:    bindDN,
        baseDN:    baseDN,
    }
}

func (p *LDAPProvider) Name() string {
    return "enterprise_ldap"
}

func (p *LDAPProvider) Authenticate(username, password string) (*auth.Login, error) {
    conn, err := ldap.DialURL(p.serverURL, ldap.DialWithTLSConfig(&tls.Config{InsecureSkipVerify: false}))
    if err != nil {
        return nil, fmt.Errorf("ldap dial failure: %w", err)
    }
    defer conn.Close()

    userDN := fmt.Sprintf("uid=%s,%s", username, p.baseDN)
    if err := conn.Bind(userDN, password); err != nil {
        return nil, auth.ErrInvalidCredentials
    }

    return &auth.Login{
        Username: username,
        Role:     "engineer",
    }, nil
}
```

---

## 5. Architectural Boundary: In-Kernel OAuth2 Exclusion & Perimeter Security

LionSQL explicitly designates **In-Kernel OAuth2 Social Login as Spec-Out (ADR-021 & Guidelines Section 8.6)**:

### 5.1. Rationale for Excluding In-Kernel OAuth2
1. **Kernel Purity & Zero External Outbound Dependencies**:
   A database kernel should never initiate outbound internet HTTP requests to third-party endpoints (Google, GitHub, Microsoft Graph) during query execution or connection handling. Outbound calls introduce external latency, DNS failures, and firewall egress complexities.
2. **Elimination of Secret Exposure**:
   Storing consumer OAuth2 client secrets (`client_id`, `client_secret`) inside database configuration files expands the attack surface and complicates secret rotation.
3. **Database Protocols Incompatibility**:
   Traditional binary wire protocols (PostgreSQL wire and TDS) do not support interactive HTTP browser redirect handshakes (`HTTP 302` redirect to authorization URL). Attempting to embed social login inside the database creates asymmetric capabilities between HTTP and database client tools.

### 5.2. Recommended Enterprise Perimeter Topology
Enterprise Single Sign-On (SSO), SAML, OIDC, and multi-factor authentication (MFA) must be handled by dedicated perimeter reverse proxies:

```
    [ Corporate Browser / Client ]
                   │
                   ▼
    ┌─────────────────────────────┐
    │  Enterprise Perimeter Proxy │  <───> [ Enterprise IdP (Okta / Keycloak / Azure AD) ]
    │  (OAuth2-Proxy / Nginx)     │
    └──────────────┬──────────────┘
                   │ (Injects Validated Claims / mTLS)
                   ▼
    ┌─────────────────────────────┐
    │    LionSQL Kernel Node      │
    │  - Port 8090 (HTTP API)     │
    │  - Port 5433 (PGWire)       │
    │  - Port 1433 (TDS)          │
    └─────────────────────────────┘
```

By decoupling corporate perimeter authentication from the storage engine, LionSQL remains an autonomous, zero-dependency standalone binary capable of operating in air-gapped secure production enclaves.
