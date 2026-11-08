# LionSQL 공식 오픈소스 앱 연동 가이드 (`guide/`)

> [English Version](README.md)

이 디렉토리는 주요 오픈소스 애플리케이션 및 엔터프라이즈 서비스를 소스 코드 수정 없이(Zero-Modification) **LionSQL** 위에서 구동하기 위한 공식 구축·마이그레이션·운영 가이드를 모아둔 곳입니다.

> [!TIP]
> **가이드 작성 및 구동 원칙 (Strict Default & Tolerant Bootstrap)**:
> 1. **표준 앱 (기본)**: ANSI/PostgreSQL 표준을 준수하는 대다수 앱은 어떠한 추가 플래그 없이 기본 명령(`lionsql start`)으로 설치 및 구동합니다.
> 2. **비정규 DDL 앱**: NocoDB 등 비존재 객체 삭제나 비정규 마이그레이션을 던지는 특수 앱에 한하여, 최초 부트스트랩 시 `./bin/lionsql set tolerant_ddl on` 동적 활성화 및 설치 완료 후 `off` 복원 절차를 명시합니다 (서버는 재시작 시 안전한 기본값인 `strict` 모드로 자동 복원됩니다).

---

## 검증 완료 애플리케이션 일람표 (11대 서비스)

| 애플리케이션 | 분야 | 언어 / ORM 스택 | 지원 와이어 프로토콜 | 한국어 가이드 | 영문 가이드 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Gitea** | DevOps / Git 호스팅 | Go + XORM | PGWire (`:5433`) & TDS (`:1433`) | [`GITEA_INTEGRATION_GUIDE.ko.md`](GITEA_INTEGRATION_GUIDE.ko.md) | [`GITEA_INTEGRATION_GUIDE.md`](GITEA_INTEGRATION_GUIDE.md) |
| **Grafana** | 관측성 및 대시보드 | Go + XORM / SQL 플러그인 | PGWire (`:5433`) & TDS (`:1433`) | [`GRAFANA_INTEGRATION_GUIDE.ko.md`](GRAFANA_INTEGRATION_GUIDE.ko.md) | [`GRAFANA_INTEGRATION_GUIDE.md`](GRAFANA_INTEGRATION_GUIDE.md) |
| **Metabase** | BI 및 데이터 분석 | Clojure/JVM + JDBC | PGWire (`:5433`) & TDS (`:1433`) | [`METABASE_INTEGRATION_GUIDE.ko.md`](METABASE_INTEGRATION_GUIDE.ko.md) | [`METABASE_INTEGRATION_GUIDE.md`](METABASE_INTEGRATION_GUIDE.md) |
| **DuckDB** | 고성능 임베디드 OLAP / HTAP | C++ / Python / CLI | PGWire (`:5433`) & REST CDC (`:8090`) | [`DUCKDB_INTEGRATION_GUIDE.ko.md`](DUCKDB_INTEGRATION_GUIDE.ko.md) | [`DUCKDB_INTEGRATION_GUIDE.md`](DUCKDB_INTEGRATION_GUIDE.md) |
| **Vaultwarden** | 비밀번호 관리 (Bitwarden) | Rust + Diesel ORM | PGWire (`:5433`) | [`VAULTWARDEN_INTEGRATION_GUIDE.ko.md`](VAULTWARDEN_INTEGRATION_GUIDE.ko.md) | [`VAULTWARDEN_INTEGRATION_GUIDE.md`](VAULTWARDEN_INTEGRATION_GUIDE.md) |
| **Casdoor** | 통합 인증 IAM / OIDC SSO | Go + XORM + Casbin | PGWire (`:5433`) & TDS (`:1433`) | [`CASDOOR_INTEGRATION_GUIDE.ko.md`](CASDOOR_INTEGRATION_GUIDE.ko.md) | [`CASDOOR_INTEGRATION_GUIDE.md`](CASDOOR_INTEGRATION_GUIDE.md) |
| **Umami** | 웹 애널리틱스 | Node.js / Next.js + Prisma | PGWire (`:5433`) | [`UMAMI_INTEGRATION_GUIDE.ko.md`](UMAMI_INTEGRATION_GUIDE.ko.md) | [`UMAMI_INTEGRATION_GUIDE.md`](UMAMI_INTEGRATION_GUIDE.md) |
| **Apache Answer** | Q&A 지식베이스 | Go + XORM | PGWire (`:5433`) | [`ANSWER_INTEGRATION_GUIDE.ko.md`](ANSWER_INTEGRATION_GUIDE.ko.md) | [`ANSWER_INTEGRATION_GUIDE.md`](ANSWER_INTEGRATION_GUIDE.md) |
| **Woodpecker CI** | CI/CD 파이프라인 엔진 | Go + XORM | PGWire (`:5433`) | [`WOODPECKER_INTEGRATION_GUIDE.ko.md`](WOODPECKER_INTEGRATION_GUIDE.ko.md) | [`WOODPECKER_INTEGRATION_GUIDE.md`](WOODPECKER_INTEGRATION_GUIDE.md) |
| **Vikunja** | 프로젝트 및 칸반 관리 | Go + XORM | PGWire (`:5433`) | [`VIKUNJA_INTEGRATION_GUIDE.ko.md`](VIKUNJA_INTEGRATION_GUIDE.ko.md) | [`VIKUNJA_INTEGRATION_GUIDE.md`](VIKUNJA_INTEGRATION_GUIDE.md) |
| **IOTA-SDK ERP** | 엔터프라이즈 ERP 프레임워크 | Go + `pgx/v5` + `goose` | PGWire (`:5433`) | [`IOTA_ERP_INTEGRATION_GUIDE.ko.md`](IOTA_ERP_INTEGRATION_GUIDE.ko.md) | [`IOTA_ERP_INTEGRATION_GUIDE.md`](IOTA_ERP_INTEGRATION_GUIDE.md) |
| **SSMS** | 공식 데이터베이스 관리 도구 | Microsoft Windows GUI | TDS (`:1433`) | [`SSMS_INTEGRATION_GUIDE.ko.md`](SSMS_INTEGRATION_GUIDE.ko.md) | [`SSMS_INTEGRATION_GUIDE.md`](SSMS_INTEGRATION_GUIDE.md) |

---

## 관련 문서
- **코어 아키텍처 및 기능 매뉴얼**: [`doc/README.ko.md`](../doc/README.ko.md)
- **GUI 및 CLI 클라이언트 도구 연동 가이드 (DBeaver, SSMS, Beekeeper, psql, sqlcmd)**: [`doc/CLIENT_TOOL_INTEGRATION.md`](../doc/CLIENT_TOOL_INTEGRATION.md)
- **상세 연동 검증 보고서 및 엔지니어링 노트**: [`notes/README.ko.md`](../notes/README.ko.md)
