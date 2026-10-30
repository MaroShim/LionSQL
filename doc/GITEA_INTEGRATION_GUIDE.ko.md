# LionSQL 기반 Gitea 구축 및 운영 공식 연동 가이드 (`doc/GITEA_INTEGRATION_GUIDE.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-10-30
> - **대상 릴리즈**: LionSQL v0.90.0-beta4+
> - **연동 대상**: Gitea v1.23 ~ v1.27+ (공식 소스/바이너리 무수정 연동)
> - **지원 유선 프로토콜**:
>   - **PostgreSQL Wire Protocol v3** (기본 TCP `:5433`)
>   - **Microsoft SQL Server TDS 7.x/8.0** (기본 TCP `:1433`)
> - **참조 문서**:
>   - 아키텍처 포스트모템: [`notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](../notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md)
>   - 마일스톤 보고서: [`notes/GITEA_INTEGRATION_MILESTONE.ko.md`](../notes/GITEA_INTEGRATION_MILESTONE.ko.md)

---

## 1. 개요 및 아키텍처 장점

**Gitea**는 전 세계 수많은 엔지니어링 조직과 기업에서 소스 코드 호스팅, 협업, CI/CD 워크플로의 핵심으로 사용하는 고부하 Go 풀스택 웹 플랫폼입니다.

LionSQL은 외부 데몬(PgBouncer, Redis, Nginx, etcd) 의존성이 전혀 없는 **단일 정적 독립 실행형(Zero-Sidecar) OLTP 데이터베이스 엔진**으로서, 단 하나의 바이너리(`lionsql`)가 뜨면서 **PostgreSQL Wire Protocol(`:5433`)**과 **Microsoft SQL Server TDS(`:1433`)**를 동시에 리슨합니다.

Gitea의 백엔드 RDBMS로 LionSQL을 채택할 경우 다음과 같은 엔터프라이즈급 아키텍처 이점을 얻을 수 있습니다:

1. **클라이언트 코드 제로 수정 (Zero-Code Change)**:
   - Gitea의 코드를 단 한 줄도 수정하지 않고, 공식 바이너리(v1.27.3)를 그대로 구동합니다.
   - XORM의 기계적인 SQL 생성 패턴, 방언 고유 DDL/DML, 시스템 카탈로그 질의를 LionSQL 커널이 100% 투명하게 수용합니다.
2. **듀얼 와이어 프로토콜 자유 선택**:
   - 오픈소스 및 클라우드 네이티브 환경에 표준적인 **PostgreSQL 모드**(`lib/pq`, `:5433`)
   - 엔터프라이즈 및 Microsoft 생태계(SSMS, .NET, Azure Data Studio)에 최적화된 **SQL Server 모드**(`go-mssqldb`, `:1433`)
3. **극단적인 하드웨어 효율성 및 경량화**:
   - 수 기가바이트의 메모리를 요구하는 거대 엔터프라이즈 RDBMS 대비, 서브 마이크로초 VDBE 바이트코드 엔진과 인메모리 OCC/WiredTiger B+Tree 엔진을 통해 수십 MB 수준의 컴팩트한 풋프린트로 구동됩니다.
4. **무인 완전 자동 배포 (Headless GitOps)**:
   - 웹 기반 설치 마법사를 거치지 않고, 설정 파일(`app.ini`) 하나로 100개 이상의 스키마를 초기화하고 즉시 프로덕션 서비스를 시작할 수 있습니다.

---

## 2. 프로토콜 모드 비교 매트릭스

LionSQL은 두 유선 프로토콜을 동시에 서비스하므로, 운영 환경의 도구 체인과 보안 정책에 맞춰 최적의 프로토콜을 선택할 수 있습니다.

| 비교 항목 | 모드 A: PostgreSQL Wire (권장) | 모드 B: Microsoft SQL Server (TDS) |
| :--- | :--- | :--- |
| **Gitea `DB_TYPE`** | `postgres` | `mssql` |
| **기본 TCP 포트** | `:5433` (Postgres 표준 5432와의 충돌 방지) | `:1433` (SQL Server 표준 포트) |
| **Gitea DB 드라이버** | `github.com/lib/pq` (PGWire v3) | `github.com/microsoft/go-mssqldb` (TDS 7.x/8.0) |
| **기본 키 생성 방식** | `BIGSERIAL PRIMARY KEY NOT NULL` | `BIGINT IDENTITY(1,1) PRIMARY KEY NOT NULL` |
| **생성된 ID 획득** | `INSERT ... RETURNING "id"` | `SCOPE_IDENTITY()`, `OUTPUT INSERTED.id` |
| **메타데이터 인트로스펙션** | `pg_catalog.pg_indexes`, `pg_attribute` | `sysobjects`, `sys.columns`, `sys.fn_helpcollations` |
| **파라미터 전달 체계** | `$1, $2, ...` Extended Query 바인딩 | `@p1, @p2, ...` RPC 포지셔널 매핑 |
| **권장 사용 환경** | 컨테이너(Docker/K8s), Linux/macOS 네이티브 환경 | Windows Server, Active Directory, SSMS 모니터링 환경 |

---

## 3. 사전 요구사항 및 LionSQL 구동

### 3.1. 바이너리 준비
LionSQL은 CGo 기반 고성능 WiredTiger 엔진 태그 또는 Pure-Go 인메모리 모드로 빌드할 수 있습니다.

```bash
# 1. 프로덕션 Unix 바이너리 (WiredTiger MVCC 디스크 영속 엔진 포함)
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql

# 2. Windows 독립 바이너리 (Zero-CGo, 고해상도 1ms 타이머 포함)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql
```

### 3.2. LionSQL 데몬 시작
Gitea 전용 격리 데이터 디렉토리(예: `data/` 또는 `data_pg/`)를 지정하여 구동합니다.

```bash
# 백그라운드 프로덕션 모드 구동
./bin/lionsql -data-dir=data -admin-pass=1234567890

# 개발 및 실시간 쿼리 디버깅 모드 (-dev 플래그 활성화)
./bin/lionsql -data-dir=data -dev
```

서버가 정상 기동되면 콘솔에 다음과 같은 리스너 바인딩 상태가 출력됩니다:
- `[Web Dashboard]  http://127.0.0.1:8090/_/`
- `[Postgres Wire]  psql -h 127.0.0.1 -p 5433`
- `[TDS / MSSQL]    sqlcmd -S 127.0.0.1,1433`

---

## 4. Gitea 설정 가이드 (`custom/conf/app.ini`)

Gitea 작업 디렉토리의 `custom/conf/app.ini`에 아래 설정을 적용합니다.

### 4.1. 모드 A: PostgreSQL Wire 연동 설정 (`DB_TYPE = postgres`)
```ini
APP_NAME = Gitea on LionSQL (PostgreSQL Wire)
RUN_USER = git
WORK_PATH = /var/lib/gitea
RUN_MODE = prod

[database]
DB_TYPE  = postgres
HOST     = 127.0.0.1:5433
NAME     = gitea
USER     = admin
PASSWD   = 1234567890
SCHEMA   = public
SSL_MODE = disable
LOG_SQL  = false

[repository]
ROOT = /var/lib/gitea/data/gitea-repositories

[server]
SSH_DOMAIN       = localhost
DOMAIN           = localhost
HTTP_PORT        = 3000
ROOT_URL         = http://localhost:3000/
APP_DATA_PATH    = /var/lib/gitea/data
DISABLE_SSH      = false
SSH_PORT         = 2222
LFS_START_SERVER = true

[security]
INSTALL_LOCK       = true
INTERNAL_TOKEN     = eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
PASSWORD_HASH_ALGO = pbkdf2
```

### 4.2. 모드 B: Microsoft SQL Server 연동 설정 (`DB_TYPE = mssql`)
```ini
APP_NAME = Gitea on LionSQL (SQL Server TDS)
RUN_USER = git
WORK_PATH = /var/lib/gitea
RUN_MODE = prod

[database]
DB_TYPE  = mssql
HOST     = 127.0.0.1:1433
NAME     = gitea
USER     = sa
PASSWD   = 1234567890
SSL_MODE = disable
LOG_SQL  = false

[repository]
ROOT = /var/lib/gitea/data/gitea-repositories

[server]
SSH_DOMAIN       = localhost
DOMAIN           = localhost
HTTP_PORT        = 3000
ROOT_URL         = http://localhost:3000/
APP_DATA_PATH    = /var/lib/gitea/data
DISABLE_SSH      = false
SSH_PORT         = 2222
LFS_START_SERVER = true

[security]
INSTALL_LOCK       = true
INTERNAL_TOKEN     = eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
PASSWORD_HASH_ALGO = pbkdf2
```

> [!IMPORTANT]
> `INSTALL_LOCK = true`로 설정하면 웹 설치 마법사가 완전히 생략(Headless)되며, Gitea는 기동 즉시 LionSQL과 연결하여 100개 이상의 테이블과 인덱스를 자동으로 마이그레이션합니다.

---

## 5. 초기화 및 무인 자동 마이그레이션

### 5.1. Gitea 웹 서비스 기동
```bash
./gitea web
```
서버 기동 시 Gitea 내부의 XORM 엔진이 LionSQL로 연결되어 DDL을 원자적으로 실행합니다:
- 사용자 및 권한 (`user`, `org_user`, `team`)
- 저장소 및 브랜치 (`repository`, `branch`, `repo_unit`)
- 이슈 및 협업 (`issue`, `comment`, `pull_request`, `milestone`, `label`)
- 보안 및 토큰 (`access_token`, `auth_token`, `two_factor`)

정상 완료 시 콘솔에 아래 메시지가 출력됩니다:
```text
[I] ORM engine initialization successful!
[I] Listen: http://0.0.0.0:3000
[I] AppURL(ROOT_URL): http://localhost:3000/
```

### 5.2. 최초 관리자 계정 생성 (CLI)
웹 화면을 거치지 않고 CLI를 통해 안전하게 초기 관리자 계정을 생성합니다:
```bash
./gitea admin user create \
  --username admin \
  --password "StrongAdminPassword123!" \
  --email "admin@lionsql.local" \
  --admin
```
생성 즉시 `http://localhost:3000`에서 해당 계정으로 로그인할 수 있습니다.

---

## 6. 엔드투엔드 기능 검증 (E2E Verification)

LionSQL 저장소에 내장된 종합 모의 데이터 시딩 도구([`scripts/seed_gitea_mock_data.go`](../scripts/seed_gitea_mock_data.go))를 실행하여 Gitea의 전 기능을 자동으로 검증합니다:

```bash
go run scripts/seed_gitea_mock_data.go
```

### 자동 검증 항목:
1. **사용자 4인 생성**: 개발자, 코드 리뷰어, QA 리드, 성능 엔지니어
2. **조직 생성**: `lionsql-core` (조직 타입 `type = 1` 무결성 검증)
3. **저장소 생성 및 Git 커밋**:
   - `lionsql-core/lion-engine`
   - `lionsql-core/lion-bench`
   - `lionsql-core/lion-docs`
   - `admin/personal-scripts`
4. **마일스톤 및 5개 레이블 등록**: `bug`, `enhancement`, `performance`, `protocol`, `benchmark`
5. **이슈 및 스레드 댓글**: 이슈 생성 및 다중 사용자 간 토론 댓글 등록
6. **기능 브랜치 생성 및 파일 커밋**: `feature/flat-hash-join` 브랜치 및 `docs/HASH_JOIN.md` 커밋
7. **풀 리퀘스트(PR) & 머지**: PR #5 생성, 리뷰어 승인, `main` 브랜치 자동 머지
8. **정식 릴리스 태그 발행**: `v1.0.0-rc1` 배포

검증 완료 후 브라우저에서 `http://localhost:3000/lionsql-core/lion-docs`에 접속하여 커밋 히스토리와 머지된 PR을 직접 확인할 수 있습니다.

---

## 7. 프로덕션 운영 및 튜닝 모범 사례 (Best Practices)

### 7.1. 스토리지 엔진 선택 기준
LionSQL은 기동 옵션에 따라 최적의 스토리지 엔진을 제공합니다:
- **WiredTiger MVCC B+Tree (`-tags wiredtiger`)** (권장):
  - 파일 시스템 기반의 완벽한 ACID 내구성 보장
  - 대용량 Git LFS 메타데이터 및 다수의 리포지토리 메타데이터 보관에 적합
- **In-Memory OCC (`pkg/storage/memengine`)**:
  - 초고속 CI/CD 임시 테스트 환경, 단기 에페머럴 테스트러너 백엔드용
  - 메모리 내 낙관적 동시성 제어로 100만 tpmC 이상의 초고속 트랜잭션 제공

### 7.2. 세션 풀러(Session Pooler) 튜닝
LionSQL은 내장 세션 풀러를 탑재하여 PgBouncer 없이도 수천 개의 동시 연결을 안전하게 관리합니다:
```bash
./bin/lionsql -data-dir=data \
  -max-workers=64 \
  -max-idle=128 \
  -max-memory=4096MB
```
- `-max-workers`: 액티브 쿼리 동시 처리 고루틴 수 제한
- `-max-memory`: Go 런타임 소프트 GC 제한을 통한 메모리 보호

### 7.3. 백업 및 재해 복구 (Backup & Disaster Recovery)
LionSQL은 온라인 무중단 백업 REST API 및 TDS 백업 구문을 지원합니다:
```bash
# REST API를 통한 즉시 백업 트리거
curl -X POST http://127.0.0.1:8090/api/backup \
  -H "Authorization: Bearer <ADMIN_JWT_TOKEN>" \
  -d '{"target_dir": "/backup/lionsql_gitea"}'
```
또는 정기 cron 스케줄러(`pkg/cron`)를 활용하여 일일 전체 백업 및 WAL 아카이빙을 자동화할 수 있습니다.

---

## 8. 문제 해결 가이드 (Troubleshooting)

### Q1. 비밀번호 인증 시 패닉 또는 로그인 실패
- **원인**: 이전 버전의 단순 파라미터 치환기로 인해 비밀번호 해시(`pbkdf2$50000$50`) 속 `$5`가 손상되었을 수 있습니다.
- **해결**: 최신 LionSQL(단일 패스 인용구 인식 스캐너 적용)로 업데이트 후 관리자 계정을 재설정하십시오.

### Q2. 조직(Organization) 조회 시 404 Not Found 반환
- **원인**: XORM이 전달한 문자열 파라미터가 스토리지에 정수(`type: 1`) 대신 문자열로 저장되어 0으로 인식된 경우입니다.
- **해결**: 최신 LionSQL의 `ParseLiteralDefault` 숫자 묵시적 강제 변환 패치로 해결되었습니다.

### Q3. 포트 충돌 오류 (`bind: address already in use`)
- 기존에 로컬 PostgreSQL(`:5432`)이나 MSSQL(`:1433`)이 구동 중인지 확인하십시오.
- LionSQL의 PGWire 기본 포트는 충돌 방지를 위해 `:5433`으로 지정되어 있으며, TDS 포트는 필요 시 `-tds-port=14333` 등으로 변경할 수 있습니다.
