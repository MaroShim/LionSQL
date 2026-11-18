# LionSQL 공식 오픈소스 앱 연동 가이드 (`guide/`)

> - **문서 버전**: v1.2.0
> - **최초 작성일**: 2026-11-02
> - **최종 업데이트**: 2026-11-16
> - **대상 릴리즈**: LionSQL v0.93.0+
> - **검증 서비스**: 15대 엔터프라이즈 오픈소스 애플리케이션
> - **English Guide**: [`guide/README.md`](README.md)

이 디렉토리는 주요 오픈소스 애플리케이션 및 엔터프라이즈 서비스를 소스 코드 수정 없이(Zero-Modification) **LionSQL** 위에서 구동하기 위한 공식 구축·마이그레이션·운영 가이드를 모아둔 곳입니다.

> [!TIP]
> **가이드 작성 및 구동 원칙 (Strict Default & Tolerant Bootstrap)**:
> 1. **표준 앱 (기본)**: ANSI/PostgreSQL 표준을 준수하는 대다수 앱은 어떠한 추가 플래그 없이 기본 명령(`lionsql start`)으로 설치 및 구동합니다.
> 2. **비정규 DDL 앱**: NocoDB 등 비존재 객체 삭제나 비정규 마이그레이션을 던지는 특수 앱에 한하여, 최초 부트스트랩 시 `./bin/lionsql set tolerant_ddl on` 동적 활성화 및 설치 완료 후 `off` 복원 절차를 명시합니다 (서버는 재시작 시 안전한 기본값인 `strict` 모드로 자동 복원됩니다).

---

## 검증 완료 애플리케이션 일람표 (13대 서비스)

> [!NOTE]
> **듀얼 스토리지 엔진 100% 검증 완료**:
> 아래의 모든 엔터프라이즈 오픈소스 애플리케이션은 LionSQL의 **In-Memory OCC (`compact`)** 및 **WiredTiger B+Tree (`wiredtiger`)** 두 가지 핵심 스토리지 엔진 환경 모두에서 DDL 마이그레이션, 트랜잭션, 복합 CRUD 쿼리 전 계층 100% 무결성 검증을 통과했습니다.

| 애플리케이션 | 분야 | 언어 / ORM 스택 | 지원 와이어 프로토콜 | 검증 스토리지 엔진 | 한국어 가이드 | 영문 가이드 |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Gitea** | DevOps / Git 호스팅 | Go + XORM | PGWire (`:5433`, 권장) & TDS (`:1433`) | Compact & WiredTiger | [`GITEA_INTEGRATION_GUIDE.ko.md`](GITEA_INTEGRATION_GUIDE.ko.md) | [`GITEA_INTEGRATION_GUIDE.md`](GITEA_INTEGRATION_GUIDE.md) |
| **Grafana** | 관측성 및 대시보드 | Go + XORM / SQL 플러그인 | PGWire (`:5433`, 메타/쿼리 권장) & TDS (`:1433`) | Compact & WiredTiger | [`GRAFANA_INTEGRATION_GUIDE.ko.md`](GRAFANA_INTEGRATION_GUIDE.ko.md) | [`GRAFANA_INTEGRATION_GUIDE.md`](GRAFANA_INTEGRATION_GUIDE.md) |
| **GoCD** | 엔터프라이즈 CI/CD 서버 | Java + Hibernate + Liquibase | PGWire (`:5433`, 권장) & TDS (`:1433`) | Compact & WiredTiger | [`GOCD_INTEGRATION_GUIDE.ko.md`](GOCD_INTEGRATION_GUIDE.ko.md) | [`GOCD_INTEGRATION_GUIDE.md`](GOCD_INTEGRATION_GUIDE.md) |
| **Wiki.js** | 엔터프라이즈 위키 및 지식관리 | Node.js + Knex / Drizzle | PGWire (`:5433`, 권장) & TDS (`:1433`) | Compact & WiredTiger | [`WIKIJS_INTEGRATION_GUIDE.ko.md`](WIKIJS_INTEGRATION_GUIDE.ko.md) | [`WIKIJS_INTEGRATION_GUIDE.md`](WIKIJS_INTEGRATION_GUIDE.md) |
| **Metabase** | BI 및 데이터 분석 | Clojure/JVM + JDBC | PGWire (`:5433`, 권장) & TDS (`:1433`) | Compact & WiredTiger | [`METABASE_INTEGRATION_GUIDE.ko.md`](METABASE_INTEGRATION_GUIDE.ko.md) | [`METABASE_INTEGRATION_GUIDE.md`](METABASE_INTEGRATION_GUIDE.md) |
| **DuckDB** | 고성능 임베디드 OLAP / HTAP | C++ / Python / CLI | PGWire (`:5433`, 권장) & REST CDC (`:8090`) | Compact & WiredTiger | [`DUCKDB_INTEGRATION_GUIDE.ko.md`](DUCKDB_INTEGRATION_GUIDE.ko.md) | [`DUCKDB_INTEGRATION_GUIDE.md`](DUCKDB_INTEGRATION_GUIDE.md) |
| **Vaultwarden** | 비밀번호 관리 (Bitwarden) | Rust + Diesel ORM | PGWire (`:5433`) | Compact & WiredTiger | [`VAULTWARDEN_INTEGRATION_GUIDE.ko.md`](VAULTWARDEN_INTEGRATION_GUIDE.ko.md) | [`VAULTWARDEN_INTEGRATION_GUIDE.md`](VAULTWARDEN_INTEGRATION_GUIDE.md) |
| **Casdoor** | 통합 인증 IAM / OIDC SSO | Go + XORM + Casbin | PGWire (`:5433`, 권장) & TDS (`:1433`) | Compact & WiredTiger | [`CASDOOR_INTEGRATION_GUIDE.ko.md`](CASDOOR_INTEGRATION_GUIDE.ko.md) | [`CASDOOR_INTEGRATION_GUIDE.md`](CASDOOR_INTEGRATION_GUIDE.md) |
| **Umami** | 웹 애널리틱스 | Node.js / Next.js + Prisma | PGWire (`:5433`) | Compact & WiredTiger | [`UMAMI_INTEGRATION_GUIDE.ko.md`](UMAMI_INTEGRATION_GUIDE.ko.md) | [`UMAMI_INTEGRATION_GUIDE.md`](UMAMI_INTEGRATION_GUIDE.md) |
| **Apache Answer** | Q&A 지식베이스 | Go + XORM | PGWire (`:5433`) | Compact & WiredTiger | [`ANSWER_INTEGRATION_GUIDE.ko.md`](ANSWER_INTEGRATION_GUIDE.ko.md) | [`ANSWER_INTEGRATION_GUIDE.md`](ANSWER_INTEGRATION_GUIDE.md) |
| **Woodpecker CI** | CI/CD 파이프라인 엔진 | Go + XORM | PGWire (`:5433`) | Compact & WiredTiger | [`WOODPECKER_INTEGRATION_GUIDE.ko.md`](WOODPECKER_INTEGRATION_GUIDE.ko.md) | [`WOODPECKER_INTEGRATION_GUIDE.md`](WOODPECKER_INTEGRATION_GUIDE.md) |
| **Vikunja** | 프로젝트 및 칸반 관리 | Go + XORM | PGWire (`:5433`) | Compact & WiredTiger | [`VIKUNJA_INTEGRATION_GUIDE.ko.md`](VIKUNJA_INTEGRATION_GUIDE.ko.md) | [`VIKUNJA_INTEGRATION_GUIDE.md`](VIKUNJA_INTEGRATION_GUIDE.md) |
| **IOTA-SDK ERP** | 엔터프라이즈 ERP 프레임워크 | Go + `pgx/v5` + `goose` | PGWire (`:5433`) | Compact & WiredTiger | [`IOTA_ERP_INTEGRATION_GUIDE.ko.md`](IOTA_ERP_INTEGRATION_GUIDE.ko.md) | [`IOTA_ERP_INTEGRATION_GUIDE.md`](IOTA_ERP_INTEGRATION_GUIDE.md) |
| **SSMS** | 공식 데이터베이스 관리 도구 | Microsoft Windows GUI | TDS (`:1433`) | Compact & WiredTiger | [`SSMS_INTEGRATION_GUIDE.ko.md`](SSMS_INTEGRATION_GUIDE.ko.md) | [`SSMS_INTEGRATION_GUIDE.md`](SSMS_INTEGRATION_GUIDE.md) |

---

## 관련 문서
- **코어 아키텍처 및 기능 매뉴얼**: [`doc/README.ko.md`](../doc/README.ko.md)
- **GUI 및 CLI 클라이언트 도구 연동 가이드 (DBeaver, SSMS, Beekeeper, psql, sqlcmd)**: [`doc/CLIENT_TOOL_INTEGRATION.md`](../doc/CLIENT_TOOL_INTEGRATION.md)
- **상세 연동 검증 보고서 및 엔지니어링 노트**: [`notes/README.ko.md`](../notes/README.ko.md)
