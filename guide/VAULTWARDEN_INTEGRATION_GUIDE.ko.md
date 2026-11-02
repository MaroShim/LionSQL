# LionSQL 기반 Vaultwarden 구축 및 운영 공식 연동 가이드 (`guide/VAULTWARDEN_INTEGRATION_GUIDE.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-11-01
> - **대상 릴리즈**: LionSQL v0.90.0-beta4+
> - **연동 대상**: Vaultwarden (공식 Docker 이미지 `vaultwarden/server:latest` 무수정 연동)
> - **지원 유선 프로토콜**:
>   - **PostgreSQL Wire Protocol v3** (기본 TCP `:5433`)
> - **참조 문서**:
>   - 실전 연동 검증 보고서: [`notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md`](../notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md)
>   - 모듈러 아키텍처 계획: [`notes/MODULAR_ARCHITECTURE_PLAN.ko.md`](../notes/MODULAR_ARCHITECTURE_PLAN.ko.md)

---

## 1. 개요 및 아키텍처 장점

**Vaultwarden**은 공식 Bitwarden 클라이언트 생태계(데스크톱, 모바일, 웹 브라우저 확장 프로그램, CLI)와 완전 호환되는 경량 패스워드 및 보안 저장소 서버입니다. 백엔드로 Rust 언어, Rocket 웹 프레임워크, Diesel ORM(PostgreSQL 지원)을 사용합니다.

LionSQL은 외부 미들웨어(Redis, PgBouncer, etcd 등) 의존성 없이 단일 정적 바이너리로 구동되는 **Zero-Sidecar OLTP 데이터베이스**로서, **PostgreSQL Wire Protocol(`:5433`)**을 완전하게 제공합니다.

Vaultwarden의 RDBMS로 LionSQL을 사용할 경우 다음과 같은 장점이 있습니다:

1. **Vaultwarden 소스 무수정 (Zero Code Modification)**:
   - 공식 Docker 이미지(`vaultwarden/server:latest`)를 단 한 줄의 소스나 설정 해킹 없이 그대로 구동합니다.
   - Diesel ORM의 엄격한 바이너리 직렬화 규격(8바이트 Timestamp OID 1114)과 시스템 인트로스펙션을 LionSQL 커널이 완벽하게 지원합니다.
2. **47개 전체 Diesel ORM 마이그레이션 자동 완주**:
   - Vaultwarden의 47개 PostgreSQL 마이그레이션 스크립트가 첫 기동 시 단 한 번의 오류 없이 자동으로 실행되어 29개 테이블 스키마를 구성합니다.
3. **복합 기본 키(Composite PK) 및 충돌 해결 지원**:
   - `devices (uuid, user_uuid)` 등 복합 기본 키 대상 `ON CONFLICT DO UPDATE` 처리가 완벽하게 동작하여 다중 디바이스 동기화가 매끄럽게 처리됩니다.
4. **극단적인 경량화와 단일 바이너리 배포**:
   - 무거운 기존 RDBMS 컨테이너 대신 단일 바이너리(`lionsql`)와 WiredTiger B+Tree 엔진으로 최소한의 리소스를 소모하며 안전하게 암호화된 자격 증명을 저장합니다.

---

## 2. 빠른 시작: Docker Compose 환경 구성

가장 간편한 배포 방법은 Docker Compose를 사용하는 것입니다.

### `docker-compose.yml` 예시

```yaml
version: '3.8'

services:
  lionsql:
    image: lionsql:latest  # 또는 로컬 빌드 바이너리 호스트 구동
    container_name: lionsql
    restart: unless-stopped
    ports:
      - "5433:5433"   # PostgreSQL Wire Protocol
      - "8090:8090"   # LionSQL Web Studio & REST API
    volumes:
      - ./data/lionsql:/data
    command: ["lionsql", "-dir", "/data", "-engine", "wiredtiger", "-pg", "0.0.0.0:5433", "-admin-pass", "VaultwardenDBPass2026!"]

  vaultwarden:
    image: vaultwarden/server:latest
    container_name: vaultwarden
    restart: unless-stopped
    depends_on:
      - lionsql
    ports:
      - "8080:80"     # Vaultwarden Web Vault & API
    environment:
      - DATABASE_URL=postgresql://admin:VaultwardenDBPass2026!@lionsql:5433/vaultwarden
      - SIGNUPS_ALLOWED=true
      - WEBSOCKET_ENABLED=true
    volumes:
      - ./data/vaultwarden:/data
```

---

## 3. 수동 실행 및 프로덕션 배포 단계

### 3.1 LionSQL 서버 기동
LionSQL 바이너리를 WiredTiger 스토리지 엔진과 PostgreSQL 리스너로 기동합니다:

```bash
mkdir -p data_vaultwarden
./bin/lionsql -dir data_vaultwarden -engine wiredtiger -pg 0.0.0.0:5433 -admin-pass="VaultwardenDBPass2026!"
```

### 3.2 Vaultwarden 컨테이너 구동
호스트의 LionSQL 인스턴스에 연결하여 Vaultwarden을 기동합니다:

```bash
docker run -d --name vaultwarden \
  -e DATABASE_URL="postgresql://admin:VaultwardenDBPass2026!@host.docker.internal:5433/vaultwarden" \
  -e SIGNUPS_ALLOWED="true" \
  -p 8080:80 \
  -v $(pwd)/vw-data:/data \
  vaultwarden/server:latest
```

기동 즉시 Vaultwarden 로그에서 Diesel ORM 마이그레이션이 실행되는 것을 확인할 수 있습니다:
```
[INFO] Running all pending migrations...
[INFO] Executing migration: 2019-09-12-100000_create_tables
[INFO] Executing migration: ...
[INFO] Executing migration: 2026-10-07-120000_add_org_policy_revision_date
[INFO] Rocket has launched from http://0.0.0.0:80
```

---

## 4. 백업 및 복구 가이드

LionSQL은 단일 데이터 디렉토리 구조를 따르므로 백업과 복구가 매우 직관적입니다.

### 4.1 오프라인 스냅샷 백업
LionSQL 서비스를 중지하거나 일시 정지한 상태에서 데이터 디렉토리를 보관합니다:
```bash
tar -czvf vaultwarden_backup_$(date +%Y%m%d).tar.gz data_vaultwarden/
```

### 4.2 Web Studio & SQL 인터페이스를 통한 상태 점검
LionSQL 웹 관리 콘솔(`http://localhost:8090/_/`) 또는 PostgreSQL CLI(`psql`)를 통해 스키마 상태와 마이그레이션 내역을 즉시 검사할 수 있습니다:

```bash
psql -h 127.0.0.1 -p 5433 -U admin -d vaultwarden -c "SELECT count(*) FROM __diesel_schema_migrations;"
# 결과: 47
```

---

## 5. 트러블슈팅 및 FAQ

### Q. Diesel 마이그레이션 중 컬럼 타입 변경 오류가 발생하면 어떻게 하나요?
LionSQL 커널은 다중 액션 복합 `ALTER TABLE` 구문(`ALTER COLUMN ... TYPE ..., ALTER COLUMN ... SET NOT NULL`)을 기본 지원하므로, 마이그레이션 과정에서 스키마 변경 시 별도의 수동 조작이 불필요합니다.

### Q. Bitwarden 공식 확장 프로그램에서 동기화 오류가 날 경우?
공식 Bitwarden 확장 프로그램 및 모바일 앱은 서버 URL을 `http://<호스트>:8080`으로 지정하여 직접 연결할 수 있습니다. LionSQL은 PGWire v3의 8바이트 정수형 타임스탬프(`OID 1114`) 인코딩을 지원하므로 Rust Diesel의 시간 역직렬화(`NaiveDateTime`) 에러가 발생하지 않습니다.
