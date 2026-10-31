# Casdoor 통합 인증(IAM/SSO) 듀얼 프로토콜(PGWire & TDS) 연동 및 검증 계획서 (`notes/CASDOOR_INTEGRATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-10-31
> - **과제 식별 코드**: `CASDOOR-INTEGRATION-1`
> - **목표**: 오픈소스 통합 인증 및 싱글 사인온(IAM / SSO / OIDC / OAuth 2.0 / SAML) 플랫폼인 [Casdoor](https://github.com/casdoor/casdoor)를 LionSQL에 연동하여, **PostgreSQL Wire Protocol (`:5433`)**과 **Microsoft SQL Server TDS (`:1433`)** 양쪽 모두에서 백엔드 RDBMS로 완벽히 동작함을 검증
> - **상태**: 진행 중 (`In Progress`)
> - **참조 문서**:
>   - Gitea 마일스톤: [`notes/GITEA_INTEGRATION_MILESTONE.ko.md`](GITEA_INTEGRATION_MILESTONE.ko.md)
>   - IOTA ERP 마일스톤: [`notes/IOTA_ERP_INTEGRATION_MILESTONE.ko.md`](IOTA_ERP_INTEGRATION_MILESTONE.ko.md)
>   - Grafana 마일스톤: [`notes/GRAFANA_INTEGRATION_MILESTONE.ko.md`](GRAFANA_INTEGRATION_MILESTONE.ko.md)

---

## 1. 개요 및 연동 아키텍처

**Casdoor**는 Go 언어(Beego + XORM + Casbin)로 작성된 엔터프라이즈급 IAM/SSO 플랫폼입니다. 사내에 구축된 Gitea, Grafana, IOTA ERP 등의 인증을 단일 OIDC/OAuth2 중앙 서버로 통합하는 핵심 인프라입니다.

LionSQL은 단일 바이너리로 두 유선 프로토콜을 동시에 제공하므로, Casdoor의 두 가지 주요 데이터베이스 드라이버 모드를 모두 검증합니다:

```
+---------------------------------------------------------------------------------+
|                         Casdoor IAM / SSO Server (:8000)                        |
|  - Engine: XORM (xorm.io/xorm) + Casbin (xorm-adapter)                          |
|  - Core Entities: organization, user, application, provider, token,             |
|    certificate, permission, role, model, adapter, casbin_rule, etc. (~35 tables)|
+---------------------------------------------------------------------------------+
                 |                                        |
                 | Mode A: driverName = postgres          | Mode B: driverName = mssql
                 | (github.com/lib/pq, TCP :5433)         | (go-mssqldb, TCP :1433)
                 v                                        v
+---------------------------------------------------------------------------------+
|                        LionSQL Database Kernel (Single Binary)                  |
|  - PostgreSQL Wire Listener: pkg/wire/server.go (:5433)                         |
|  - Microsoft SQL Server TDS Listener: pkg/wire/tds/server.go (:1433)            |
|  - Execution Engines: VDBE Bytecode VM & Volcano CBO Engine                     |
|  - Storage Engines: WiredTiger MVCC B+Tree Engine / In-Memory OCC Engine        |
+---------------------------------------------------------------------------------+
```

---

## 2. 세부 단계별 구현 및 검증 계획

### 1단계: 저장소 준비 및 빌드 검증
- `/Users/maro/Projects/casdoor` 위치에 공식 저장소 shallow clone(`--depth 1`).
- Go 백엔드 바이너리(`casdoor`) 빌드 검증.

### 2단계: 모드 A — PostgreSQL Wire (`:5433`) 백엔드 연동 및 검증
- `conf/app.conf` 설정:
  - `driverName = postgres`
  - `dataSourceName = user=admin password=1234567890 host=127.0.0.1 port=5433 sslmode=disable dbname=casdoor`
  - `httpport = 8000`
- LionSQL 서버 기동(`data_casdoor_pg`) 후 Casdoor 실행:
  - XORM `Sync2`를 통한 ~35개 IAM 테이블 및 인덱스 자동 생성 확인.
  - 초기 시드 데이터(`built-in` 조직, `admin` 계정, `app-built-in` 애플리케이션, RSA 인증서, `casbin_rule` 정책) 초기화 확인.
  - REST API(`/api/get-organizations`, `/api/get-users`, `/api/login`, `/.well-known/openid-configuration`) 정상 응답 검증.

### 3단계: 모드 B — Microsoft SQL Server TDS (`:1433`) 백엔드 연동 및 검증
- `conf/app.conf` 설정:
  - `driverName = mssql`
  - `dataSourceName = sqlserver://admin:1234567890@127.0.0.1:1433?database=casdoor&encrypt=disable`
- LionSQL 서버 기동(`data_casdoor_mssql`) 후 Casdoor 실행:
  - MSSQL 방언 기반 XORM `Sync2` 테이블 생성 및 Casbin 어댑터 초기화 확인.
  - 동일하게 REST API 및 OIDC Discovery 엔드포인트 정상 응답 검증.

### 4단계: 커널 보완 및 3-Stage Verification Pipeline
- 연동 과정에서 발견된 호환성 이슈 수정.
- 임시 데이터 디렉토리 정리 후 Build ➔ Test (`-race`) ➔ Commit 수행.

### 5단계: 한/영 공식 연동 가이드 및 마일스톤 보고서 작성
- 실전 가이드: `doc/CASDOOR_INTEGRATION_GUIDE.ko.md` 및 `doc/CASDOOR_INTEGRATION_GUIDE.md`
- 마일스톤 보고서: `notes/CASDOOR_INTEGRATION_MILESTONE.ko.md` 및 `notes/CASDOOR_INTEGRATION_MILESTONE.md`
- `TODO.md` 및 `CHANGELOG.md` 업데이트 후 문서 커밋.
