# LionSQL Official Application Integration Guides (`guide/`)

> [한국어 문서 (Korean Version)](README.ko.md)

This directory contains official step-by-step deployment, migration, and operational guides for running popular open-source applications and enterprise services directly on **LionSQL** with zero source code modifications.

> [!TIP]
> **Integration Principles (Strict Default & Tolerant Bootstrap)**:
> 1. **Standard Applications (Default)**: Applications adhering to ANSI/PostgreSQL standards deploy seamlessly using pure default commands (`lionsql start`) without extra flags.
> 2. **Irregular DDL Applications**: For applications with non-standard migrations (e.g., NocoDB dropping non-existent objects), document enabling tolerant mode via `./bin/lionsql set tolerant_ddl on` (or `--tolerant-ddl`) during initial bootstrap and restoring it to `off` post-migration.

---

## Verified Application Matrix (10 Services)

| Application | Category | Language / ORM | Wire Protocol(s) | English Guide | Korean Guide |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Gitea** | DevOps / Git Hosting | Go + XORM | PGWire (`:5433`) & TDS (`:1433`) | [`GITEA_INTEGRATION_GUIDE.md`](GITEA_INTEGRATION_GUIDE.md) | [`GITEA_INTEGRATION_GUIDE.ko.md`](GITEA_INTEGRATION_GUIDE.ko.md) |
| **Grafana** | Observability & Dashboards | Go + XORM / SQL Plugins | PGWire (`:5433`) & TDS (`:1433`) | [`GRAFANA_INTEGRATION_GUIDE.md`](GRAFANA_INTEGRATION_GUIDE.md) | [`GRAFANA_INTEGRATION_GUIDE.ko.md`](GRAFANA_INTEGRATION_GUIDE.ko.md) |
| **Metabase** | BI & Analytics | Clojure/JVM + JDBC | PGWire (`:5433`) & TDS (`:1433`) | [`METABASE_INTEGRATION_GUIDE.md`](METABASE_INTEGRATION_GUIDE.md) | [`METABASE_INTEGRATION_GUIDE.ko.md`](METABASE_INTEGRATION_GUIDE.ko.md) |
| **Vaultwarden** | Password Manager (Bitwarden) | Rust + Diesel ORM | PGWire (`:5433`) | [`VAULTWARDEN_INTEGRATION_GUIDE.md`](VAULTWARDEN_INTEGRATION_GUIDE.md) | [`VAULTWARDEN_INTEGRATION_GUIDE.ko.md`](VAULTWARDEN_INTEGRATION_GUIDE.ko.md) |
| **Casdoor** | IAM / OIDC SSO | Go + XORM + Casbin | PGWire (`:5433`) & TDS (`:1433`) | [`CASDOOR_INTEGRATION_GUIDE.md`](CASDOOR_INTEGRATION_GUIDE.md) | [`CASDOOR_INTEGRATION_GUIDE.ko.md`](CASDOOR_INTEGRATION_GUIDE.ko.md) |
| **Umami** | Web Analytics | Node.js / Next.js + Prisma | PGWire (`:5433`) | [`UMAMI_INTEGRATION_GUIDE.md`](UMAMI_INTEGRATION_GUIDE.md) | [`UMAMI_INTEGRATION_GUIDE.ko.md`](UMAMI_INTEGRATION_GUIDE.ko.md) |
| **Apache Answer** | Q&A Knowledge Base | Go + XORM | PGWire (`:5433`) | [`ANSWER_INTEGRATION_GUIDE.md`](ANSWER_INTEGRATION_GUIDE.md) | [`ANSWER_INTEGRATION_GUIDE.ko.md`](ANSWER_INTEGRATION_GUIDE.ko.md) |
| **Woodpecker CI** | CI/CD Pipeline Engine | Go + XORM | PGWire (`:5433`) | [`WOODPECKER_INTEGRATION_GUIDE.md`](WOODPECKER_INTEGRATION_GUIDE.md) | [`WOODPECKER_INTEGRATION_GUIDE.ko.md`](WOODPECKER_INTEGRATION_GUIDE.ko.md) |
| **Vikunja** | Project & Kanban Management | Go + XORM | PGWire (`:5433`) | [`VIKUNJA_INTEGRATION_GUIDE.md`](VIKUNJA_INTEGRATION_GUIDE.md) | [`VIKUNJA_INTEGRATION_GUIDE.ko.md`](VIKUNJA_INTEGRATION_GUIDE.ko.md) |
| **IOTA-SDK ERP** | Enterprise ERP Framework | Go + `pgx/v5` + `goose` | PGWire (`:5433`) | [`IOTA_ERP_INTEGRATION_GUIDE.md`](IOTA_ERP_INTEGRATION_GUIDE.md) | [`IOTA_ERP_INTEGRATION_GUIDE.ko.md`](IOTA_ERP_INTEGRATION_GUIDE.ko.md) |

---

## Related Documentation
- **Core Documentation & Architecture**: [`doc/README.md`](../doc/README.md)
- **GUI & CLI Client Tool Connections (DBeaver, SSMS, Beekeeper, psql, sqlcmd)**: [`doc/CLIENT_TOOL_INTEGRATION.md`](../doc/CLIENT_TOOL_INTEGRATION.md)
- **Engineering Verification Reports**: [`notes/README.md`](../notes/README.md)
