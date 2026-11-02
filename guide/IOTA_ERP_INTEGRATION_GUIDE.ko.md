# LionSQL 기반 IOTA-SDK Go ERP 구축 및 운영 공식 연동 가이드 (`guide/IOTA_ERP_INTEGRATION_GUIDE.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-10-31
> - **대상 릴리즈**: LionSQL v0.90.0-beta4+
> - **연동 대상**: IOTA SDK Go ERP (github.com/iota-uz/iota-sdk, 공식 소스 무수정 연동)
> - **데이터베이스 드라이버**: `jackc/pgx/v5` (`pgxpool`)
> - **지원 유선 프로토콜**: **PostgreSQL Wire Protocol v3** (기본 TCP `:5433`)
> - **참조 문서**:
>   - 실전 연동 검증 보고서: [`notes/IOTA_ERP_INTEGRATION_REPORT.ko.md`](../notes/IOTA_ERP_INTEGRATION_REPORT.ko.md)
>   - Gitea 연동 가이드: [`guide/GITEA_INTEGRATION_GUIDE.ko.md`](GITEA_INTEGRATION_GUIDE.ko.md)

---

## 1. 개요 및 아키텍처 장점

**IOTA SDK**는 현대적인 전사적 자원 관리(ERP) 시스템 구축을 위해 Go 언어로 설계된 모듈형 엔터프라이즈 프레임워크입니다. 재무(Finance), 인사/인증(IAM/OIDC), 제조(Manufacturing), 고객관계관리(CRM), 창고/재고(Warehouse/Inventory) 등의 비즈니스 모듈을 제공합니다.

LionSQL은 외부 데몬(PgBouncer, Redis, Nginx, etcd) 의존성이 전혀 없는 **단일 정적 독립 실행형(Zero-Sidecar) 고성능 OLTP 데이터베이스**로서, 공식 PostgreSQL 와이어 프로토콜(TCP `:5433`)을 통해 `iota-sdk` 백엔드를 완벽하게 지원합니다.

### 핵심 아키텍처 이점
1. **코드 제로 수정 연동 (Zero-Code Modification)**:
   - ERP 소스 코드나 47개 마이그레이션 SQL을 단 한 줄도 수정하지 않고 그대로 구동합니다.
   - 복합 `ALTER TABLE` 구문, 인덱스 `USING GIN`, 스키마 별칭, 권한 DDL을 LionSQL 커널이 100% 투명하게 수용합니다.
2. **엄격한 `pgx/v5` 바이너리 프로토콜 호환**:
   - Go 생태계 최고 성능 드라이버인 `jackc/pgx/v5`의 확장 쿼리 프로토콜(Parse/Bind/Describe/Execute/Sync)과 원시 바이너리 포맷(16바이트 UUID, 마이크로초 단위 `TIMESTAMPTZ`)을 네이티브로 처리합니다.
3. **독립 단일 바이너리 배포 (Zero-Sidecar)**:
   - 복잡한 데이터베이스 서버 인스톨러나 Docker 오케스트레이션 없이, 단일 실행 파일(`lionsql`) 하나로 고성능 인메모리 OCC 또는 WiredTiger 영속 B+Tree 기반의 ERP 백엔드를 즉시 완성합니다.
4. **극단적인 하드웨어 효율성**:
   - 기가바이트 단위의 무거운 메모리를 요구하는 외부 RDBMS 대비, 수십 MB 수준의 컴팩트한 메모리 풋프린트로 구동됩니다.

---

## 2. 사전 요구사항 및 LionSQL 기동

### 2.1. LionSQL 바이너리 빌드
LionSQL은 CGo 기반의 고성능 WiredTiger 디스크 영속 엔진 태그 또는 Pure-Go 인메모리 모드로 빌드할 수 있습니다:

```bash
# 프로덕션 Unix 바이너리 (WiredTiger MVCC 디스크 영속 엔진 포함)
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql

# Pure-Go 인메모리 독립 바이너리 (Zero CGo)
go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
```

### 2.2. LionSQL 데몬 시작
데이터 디렉토리를 지정하여 프로덕션 데몬으로 구동합니다:

```bash
# 디스크 영속 모드 (기본 데이터 디렉토리: data/)
./bin/lionsql -data-dir=data -admin-pass=1234567890

# 또는 개발 및 테스트용 초고속 인메모리 모드 (-ephemeral)
./bin/lionsql -ephemeral -engine=memory -admin-pass=1234567890
```

서버가 정상 기동되면 콘솔에 다음과 같은 리스너 바인딩 상태가 출력됩니다:
- `[Web Dashboard]  http://127.0.0.1:8090/_/`
- `[Postgres Wire]  psql -h 127.0.0.1 -p 5433`
- `[TDS / MSSQL]    sqlcmd -S 127.0.0.1,1433`

---

## 3. IOTA ERP 환경 설정 및 빌드

### 3.1. 소스 준비 및 의존성 다운로드
```bash
cd /Users/maro/Projects/iota-sdk
go mod download
```

### 3.2. 환경 설정 파일 구성 (`.env` 또는 `.env.local`)
LionSQL의 PostgreSQL 포트(`5433`)와 관리자 계정을 지정합니다:

```env
# Server
PORT=3200
ENV=production

# Database (LionSQL PGWire v3)
DB_HOST=127.0.0.1
DB_PORT=5433
DB_USER=admin
DB_PASSWORD=1234567890
DB_NAME=iota
DB_SSL_MODE=disable

# UI Lens Assets Directory
LENS_ASSETS_DIR=/tmp/lens-dist
```

> [!NOTE]
> Vite 프론트엔드 에셋 디렉토리가 로컬에 준비되지 않은 경우 다음 명령어로 최소 번들 매니페스트를 임시 생성하여 부팅을 진행할 수 있습니다:
> ```bash
> mkdir -p /tmp/lens-dist/.vite
> echo '{"index.html":{"file":"index.js"}}' > /tmp/lens-dist/.vite/manifest.json
> touch /tmp/lens-dist/index.js
> ```

---

## 4. 스키마 마이그레이션 실행

IOTA SDK는 `rubenv/sql-migrate` 기반의 CLI 마이그레이션 도구를 내장하고 있습니다.

### 4.1. CLI 도구 빌드
```bash
go build -o bin/iota ./cmd/iota
```

### 4.2. 마이그레이션 적용 (`migrate up`)
```bash
./bin/iota migrate up
```

정상 완료 시 콘솔에 아래 메시지가 출력됩니다:
```text
INFO[0000] Applied 47 migrations
```

### 4.3. 마이그레이션 상태 검증 (`migrate status`)
```bash
./bin/iota migrate status
```
47개의 모든 마이그레이션 스크립트가 `Applied` 상태와 적용 일시를 정상적으로 표시하는지 확인합니다.

---

## 5. ERP 서버 기동 및 서비스 접속

### 5.1. 서버 바이너리 빌드 및 기동
```bash
go build -o bin/server ./cmd/server
LENS_ASSETS_DIR=/tmp/lens-dist ./bin/server
```

정상 기동 시 다음과 같은 로그가 출력되며 백그라운드 워커가 동작합니다:
```text
INFO config loaded provenance="..."
Listening on: http://localhost:3200
```

### 5.2. 웹 브라우저 접속 및 로그인
- 브라우저에서 `http://localhost:3200` 접속 시 `HTTP 302 Found -> /login`을 거쳐 공식 ERP 로그인 웹 화면이 표시됩니다.
- CLI 확인:
  ```bash
  curl -i -L http://localhost:3200
  ```
  `HTTP/1.1 200 OK` 및 `<title>Log in</title>` HTML 문서가 반환됩니다.

---

## 6. 트러블슈팅 및 기술 참조

| 증상 | 원인 | 해결책 |
| :--- | :--- | :--- |
| `cannot scan varchar (OID 1043) into *time.Time` | 구버전 엔진에서 `TIMESTAMPTZ` 키워드가 누락되어 VARCHAR로 생성된 경우 | LionSQL v0.90.0-beta4+ 최신 빌드로 갱신 후 테이블 재생성 |
| `invalid length for UUID: 36` | 드라이버가 바이너리 포맷(16바이트)을 기대하는데 36바이트 문자열이 반환된 경우 | PGWire의 `Format = 1` OID 2950 바이너리 디코더 적용 확인 |
| `USING GIN syntax error` | 인덱스 생성 시 GIN 구문 거부 | LionSQL은 GIN 문법을 파싱하여 B+Tree/표현식 인덱스로 안전하게 자동 강등(Fallback) 처리함 |
| `column "CONSTRAINT" already exists` | `CREATE TABLE` 내의 인라인 제약조건 구문 오인식 | 최신 DDL 인터셉터에서 인라인 UNIQUE/CHECK 제약조건 자동 분리 적용 |
