# GoCD 연동 가이드 (`GOCD_INTEGRATION_GUIDE.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-11-16
> - **최종 업데이트**: 2026-11-16
> - **대상 릴리즈**: LionSQL v0.93.0+
> - **연동 대상**: GoCD (Thoughtworks 엔터프라이즈 CI/CD 서버, Java/Hibernate + Liquibase 기반)
> - **지원 유선 프로토콜**: **PostgreSQL Wire Protocol v3** (기본 TCP `:5433`) 및 **Microsoft SQL Server TDS 7.x/8.0** (기본 TCP `:1433`)
> - **English Guide**: [`guide/GOCD_INTEGRATION_GUIDE.md`](GOCD_INTEGRATION_GUIDE.md)

본 문서는 Thoughtworks의 오픈소스 엔터프라이즈 지속적 배포(CD) 및 파이프라인 오케스트레이션 플랫폼인 **GoCD**를 LionSQL의 PostgreSQL Wire Protocol (PGWire v3, 기본 포트 `:5433`) 또는 Microsoft SQL Server TDS Protocol (기본 포트 `:1433`) 백엔드로 연동하는 방법을 설명합니다.

---

## 1. 개요 및 호환성 아키텍처

GoCD는 Java 런타임 환경에서 공식 PostgreSQL JDBC 드라이버(`org.postgresql.Driver`) 또는 Microsoft SQL Server JDBC 드라이버(`com.microsoft.sqlserver.jdbc.SQLServerDriver`), Hibernate ORM, 그리고 스키마 버전 관리 도구인 **Liquibase**를 통해 데이터베이스와 통신합니다.

- **지원 프로토콜**: 
  - **PostgreSQL Wire Protocol v3** (JDBC `:5433`): `jdbc:postgresql://<host>:5433/gocd`
  - **Microsoft SQL Server TDS 7.x/8.0** (JDBC `:1433`): `jdbc:sqlserver://<host>:1433;databaseName=gocd;encrypt=false`
- **LionSQL 권장 스토리지 엔진**: WiredTiger (고동시성 빌드/스테이지 상태 전이 및 ACID 트랜잭션 보장)
- **적용 마이그레이션**: Liquibase 기반 `DATABASECHANGELOGLOCK` 락 제어 및 `DATABASECHANGELOG` 자동 스키마 마이그레이션
- **주요 테이블**:
  - `DATABASECHANGELOG`, `DATABASECHANGELOGLOCK`: Liquibase 마이그레이션 및 동시성 락
  - `ACCESSTOKEN`: 사용자 개인 액세스 토큰 및 인증
  - `AGENTS`: 분산 빌드 에이전트 UUID, IP 및 상태 레지스트리
  - `PIPELINES`, `STAGES`: 파이프라인 트리거 카운터, 스테이지 실행 이력

---

## 2. 사전 준비 사항

- **LionSQL 바이너리 빌드**:
  ```bash
  go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
- **공식 GoCD Docker 이미지 또는 서버 아티팩트**:
  ```bash
  docker pull gocd/gocd-server:v24.2.0
  ```

---

## 3. 실행 및 연동 단계

### 1단계: LionSQL 서버 구동
신규 데이터 디렉터리(`data_gocd`)를 지정하여 PostgreSQL 와이어 프로토콜 리스너를 구동합니다:
```bash
./bin/lionsql -dir data_gocd -engine wiredtiger -pg 0.0.0.0:5433 -tds 0.0.0.0:1433 -http 127.0.0.1:8096 -admin-pass="GoCDPass2026!"
```

### 2단계: GoCD 데이터베이스 환경변수 설정 및 컨테이너 구동

#### 옵션 A: PostgreSQL Wire Protocol (`:5433`) 연동
```bash
docker run -d \
  --name gocd-server \
  -p 8153:8153 \
  -e GO_CONFIG_REPO_GC_DISABLED=true \
  -e DB_TYPE=postgresql \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=5433 \
  -e DB_NAME=gocd \
  -e DB_USER=postgres \
  -e DB_PASSWORD="GoCDPass2026!" \
  gocd/gocd-server:v24.2.0
```

#### 옵션 B: Microsoft SQL Server TDS Protocol (`:1433`) 연동
```bash
docker run -d \
  --name gocd-server-mssql \
  -p 8153:8153 \
  -e GO_CONFIG_REPO_GC_DISABLED=true \
  -e DB_TYPE=sqlserver \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=1433 \
  -e DB_NAME=gocd \
  -e DB_USER=sa \
  -e DB_PASSWORD="GoCDPass2026!" \
  gocd/gocd-server:v24.2.0
```

*로컬 환경 구동 시 `db.properties` 파일 설정 (PostgreSQL 또는 SQL Server):*
```properties
# PostgreSQL PGWire (:5433)
db.type=postgresql
db.host=127.0.0.1
db.port=5433
db.name=gocd
db.user=postgres
db.password=GoCDPass2026!

# 또는 MS SQL Server TDS (:1433)
# db.type=sqlserver
# db.host=127.0.0.1
# db.port=1433
# db.name=gocd
# db.user=sa
# db.password=GoCDPass2026!
```

---

## 4. 연동 검증

### 헬스체크 및 웹 UI 접속
```bash
# 헬스체크 확인
curl -I http://127.0.0.1:8153/go/api/v1/health

# 웹 브라우저 접속
open http://127.0.0.1:8153
```

초기 접속 후 파이프라인 생성 및 분산 에이전트(`gocd-agent`) 등록 시, LionSQL의 `AGENTS` 및 `PIPELINES` 테이블에 실시간으로 레코드가 영속화됩니다.

---

## 5. 엔지니어링 주의 사항

1. **Liquibase 분산 락 해제**:
   - 서버 비정상 종료 후 재시작 시 `DATABASECHANGELOGLOCK.LOCKED = 1` 상태로 잔존할 경우, `./bin/lionsql sql "UPDATE DATABASECHANGELOGLOCK SET LOCKED = 0 WHERE ID = 1;"`로 즉시 복구할 수 있습니다.
2. **트랜잭션 격리 수준**:
   - GoCD Hibernate는 `READ COMMITTED`를 표준으로 요구하며, LionSQL은 비차단(Non-Blocking) MVCC Read Committed를 완벽하게 지원합니다.
