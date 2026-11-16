# Wiki.js 연동 가이드 (`WIKIJS_INTEGRATION_GUIDE.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-11-16
> - **최종 업데이트**: 2026-11-16
> - **대상 릴리즈**: LionSQL v0.93.0+
> - **연동 대상**: Wiki.js (Node.js/TypeScript 기반 모던 오픈소스 위키, Knex / Drizzle ORM 기반)
> - **지원 유선 프로토콜**: **PostgreSQL Wire Protocol v3** (기본 TCP `:5433`) 및 **Microsoft SQL Server TDS 7.x/8.0** (기본 TCP `:1433`)
> - **English Guide**: [`guide/WIKIJS_INTEGRATION_GUIDE.md`](WIKIJS_INTEGRATION_GUIDE.md)

본 문서는 전 세계 5만 개 이상의 조직에서 사용하는 현대적인 엔터프라이즈 오픈소스 위키 플랫폼인 **Wiki.js**를 LionSQL의 PostgreSQL Wire Protocol (PGWire v3, 기본 포트 `:5433`) 또는 Microsoft SQL Server TDS Protocol (기본 포트 `:1433`) 백엔드로 무수정 연동(Zero-Modification)하는 방법을 설명합니다.

---

## 1. 개요 및 호환성 아키텍처

Wiki.js는 Node.js 런타임에서 `node-postgres`(`pg`), **Knex.js**, 그리고 **Drizzle ORM**을 통해 관계형 데이터베이스와 통신합니다. Knex.js의 `mssql` 및 `postgres` 방언(dialect)을 모두 공식 지원하므로, 사용 환경에 따라 원하는 유선 프로토콜을 선택할 수 있습니다.

- **지원 프로토콜 및 포트 권장**: 
  - **PostgreSQL Wire Protocol v3** (`pg`, `:5433`, 기본 권장): `postgres://<user>:<pass>@<host>:5433/wikijs` — Knex.js의 네이티브 전문 검색(Full-Text Search) 및 JSON 태그 인덱싱 처리가 가장 빠르고 가볍습니다.
  - **Microsoft SQL Server TDS 7.x/8.0** (`tedious`, `:1433`): Knex MSSQL Dialect (대괄호 식별자, `OUTPUT INSERTED` 지원) — 사내 Windows / Active Directory 환경에서 SQL Server 정책을 준수해야 할 때 권장합니다.
- **LionSQL 권장 스토리지 엔진**: WiredTiger (대용량 마크다운 문서 버전 관리 및 고속 인덱싱 지원)
- **적용 마이그레이션**: Knex / Drizzle 자동 마이그레이션 (`knex_migrations`, `knex_migrations_lock`)
- **주요 테이블**:
  - `knex_migrations`, `knex_migrations_lock`: DDL 마이그레이션 버전 및 분산 락 관리
  - `pages`, `pageHistory`: 문서 본문, 렌더링된 HTML, 버전 히스토리
  - `users`, `groups`: 사용자 계정 및 권한 역할
  - `navigation`, `site`: 위키 글로벌 네비게이션 트리 및 사이트 설정

---

## 2. 사전 준비 사항

- **LionSQL 바이너리 빌드**:
  ```bash
  go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
- **공식 Wiki.js Docker 이미지**:
  ```bash
  docker pull ghcr.io/requarks/wiki:2
  ```

---

## 3. 실행 및 연동 단계

### 1단계: LionSQL 서버 구동
신규 데이터 디렉터리(`data_wikijs`)를 지정하고 PostgreSQL 와이어 포트를 오픈합니다:
```bash
./bin/lionsql -dir data_wikijs -engine wiredtiger -pg 0.0.0.0:5433 -tds 0.0.0.0:1433 -http 127.0.0.1:8097 -admin-pass="WikiPass2026!"
```

### 2단계: Wiki.js 컨테이너 구동

#### 옵션 A: PostgreSQL Wire Protocol (`:5433`) 연동
```bash
docker run -d \
  --name wikijs \
  -p 3000:3000 \
  -e DB_TYPE=postgres \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=5433 \
  -e DB_NAME=wikijs \
  -e DB_USER=postgres \
  -e DB_PASS="WikiPass2026!" \
  ghcr.io/requarks/wiki:2
```

#### 옵션 B: Microsoft SQL Server TDS Protocol (`:1433`) 연동
```bash
docker run -d \
  --name wikijs-mssql \
  -p 3000:3000 \
  -e DB_TYPE=mssql \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=1433 \
  -e DB_NAME=wikijs \
  -e DB_USER=sa \
  -e DB_PASS="WikiPass2026!" \
  ghcr.io/requarks/wiki:2
```

*로컬 소스 구동 시 `config.yml` 설정 (PostgreSQL 또는 MS SQL Server):*
```yaml
# PostgreSQL PGWire (:5433)
port: 3000
db:
  type: postgres
  host: 127.0.0.1
  port: 5433
  user: postgres
  pass: WikiPass2026!
  db: wikijs
  schema: public
  ssl: false

# 또는 MS SQL Server TDS (:1433)
# port: 3000
# db:
#   type: mssql
#   host: 127.0.0.1
#   port: 1433
#   user: sa
#   pass: WikiPass2026!
#   db: wikijs
#   ssl: false
  db: wikijs
  schema: public
  ssl: false
```

---

## 4. 연동 검증

### 브라우저 초기 온보딩 및 첫 페이지 작성
1. 브라우저에서 `http://localhost:3000` 접속
2. 최초 관리자 계정 생성 (관리자 이메일, 비밀번호, 사이트 URL 입력)
3. 첫 페이지(Home Page) 생성 및 마크다운 작성 후 [Save] 클릭
4. LionSQL에 접속하여 데이터 영속화 확인:
   ```bash
   ./bin/lionsql sql "SELECT path, title FROM wiki_pages;"
   ```

---

## 5. 엔지니어링 주의 사항

1. **Knex 분산 락 해제**:
   - 컨테이너 비정상 종료 시 `knex_migrations_lock`이 잠금 상태로 남으면, `./bin/lionsql sql "UPDATE knex_migrations_lock SET is_locked = 0 WHERE index = 1;"`로 즉시 초기화할 수 있습니다.
2. **PostgreSQL 드라이버 호환성**:
   - Wiki.js의 `node-postgres`(`pg`) 드라이버는 SSL 핸드셰이크 요청(`SSLRequest` 0x04D2162F)을 초기에 전송하며, LionSQL은 비SSL 및 자체 서명 SSL 환경을 투명하게 지원합니다.
