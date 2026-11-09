# LionSQL 기반 Apache Answer 구축 및 운영 공식 연동 가이드 (`guide/ANSWER_INTEGRATION_GUIDE.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-11-01
> - **대상 릴리즈**: LionSQL v0.90.0-beta4+
> - **연동 대상**: Apache Answer v1.4+ (공식 소스/바이너리 무수정 연동)
> - **지원 유선 프로토콜**:
>   - **PostgreSQL Wire Protocol v3** (기본 TCP `:5433`)
> - **참조 문서**:
>   - 실전 연동 검증 보고서: [`notes/ANSWER_INTEGRATION_REPORT.ko.md`](../notes/reports/ANSWER_INTEGRATION_REPORT.ko.md)
>   - 모듈러 아키텍처 계획: [`notes/MODULAR_ARCHITECTURE_PLAN.ko.md`](../notes/plans/MODULAR_ARCHITECTURE_PLAN.ko.md)

---

## 1. 개요 및 아키텍처 장점

**Apache Answer**는 전 세계 개발자 및 기업 커뮤니티에서 Q&A 지식 기반 포털로 널리 쓰이는 오픈소스 플랫폼입니다. Go 언어와 XORM ORM, PostgreSQL 드라이버(`github.com/lib/pq`)를 백엔드로 사용합니다.

LionSQL은 외부 데몬(PgBouncer, Redis, Nginx, etcd) 의존성이 전혀 없는 **단일 정적 독립 실행형(Zero-Sidecar) OLTP 데이터베이스 엔진**으로서, 단 하나의 바이너리(`lionsql`)가 뜨면서 **PostgreSQL Wire Protocol(`:5433`)**을 완전하게 서비스합니다.

Apache Answer의 백엔드 RDBMS로 LionSQL을 채택할 경우 다음과 같은 엔터프라이즈급 아키텍처 이점을 얻을 수 있습니다:

1. **클라이언트 소스 제로 수정 (Zero-Code Change)**:
   - Apache Answer의 코드를 단 한 줄도 수정하지 않고, 공식 바이너리 및 컨테이너를 그대로 구동합니다.
   - XORM의 기계적인 SQL 생성 패턴, 방언 고유 DDL/DML, 시스템 카탈로그 질의를 LionSQL 커널이 100% 투명하게 수용합니다.
2. **다중 행 배치 INSERT 및 Identity 완벽 지원**:
   - `role_power_rel` 등 대량의 초기 시드 데이터를 일괄 삽입하는 다중 행 배치 INSERT 문에서 `SERIAL PRIMARY KEY`를 VDBE 바이트코드 가상 머신이 자동 발급합니다.
3. **극단적인 하드웨어 효율성 및 경량화**:
   - 수 기가바이트의 메모리를 요구하는 거대 엔터프라이즈 RDBMS 대비, 서브 마이크로초 VDBE 바이트코드 엔진과 WiredTiger B+Tree 엔진을 통해 수십 MB 수준의 컴팩트한 풋프린트로 구동됩니다.
4. **헤드리스 완전 자동 배포 (Headless GitOps)**:
   - 웹 기반 설치 마법사를 거치지 않고, 환경 변수(`AUTO_INSTALL=true`) 하나로 35개 테이블 스키마를 초기화하고 즉시 프로덕션 서비스를 시작할 수 있습니다.

---

## 2. 연동 사전 요구사항 및 설정

### 2.1 LionSQL 서버 구동
LionSQL 데몬을 WiredTiger 스토리지 엔진과 PGWire 리스너(`:5433`)를 활성화하여 실행합니다:
```bash
./bin/lionsql -dir data_answer -engine wiredtiger -pg 127.0.0.1:5433 -http 127.0.0.1:8090
```

### 2.2 Apache Answer 헤드리스 자동 설치 (`answer init`)
Apache Answer는 환경 변수를 통한 자동 설치(`AUTO_INSTALL=true`)를 기본 지원합니다. 다음 명령어를 실행하면 데이터베이스 연결 검증, 35개 테이블 생성, 기본 관리자 계정 생성, 시드 데이터 적재가 무인으로 완료됩니다:

```bash
mkdir -p /path/to/answer_data

AUTO_INSTALL=true \
DB_TYPE=postgres \
DB_HOST=127.0.0.1:5433 \
DB_USERNAME=admin \
DB_PASSWORD=1234567890 \
DB_NAME=answer \
LANGUAGE=en_US \
SITE_NAME="LionSQL Q&A" \
SITE_URL="http://127.0.0.1:9080" \
CONTACT_EMAIL=admin@lionsql.local \
ADMIN_NAME=admin \
ADMIN_PASSWORD=adminadmin \
ADMIN_EMAIL=admin@lionsql.local \
EXTERNAL_CONTENT_DISPLAY=always_display \
answer init -C /path/to/answer_data
```

### 2.3 Answer 설정 파일 확인 (`config.yaml`)
설치가 완료되면 `/path/to/answer_data/conf/config.yaml` 파일이 자동 생성됩니다. 웹 서버 포트를 필요에 따라 수정합니다 (예: 9080):
```yaml
debug: false
server:
  http:
    addr: 0.0.0.0:9080
data:
  database:
    driver: postgres
    connection: host=127.0.0.1 port=5433 user=admin password=1234567890 dbname=answer sslmode=disable
  cache:
    file_path: /path/to/answer_data/cache/cache.db
i18n:
  bundle_dir: /path/to/answer_data/i18n
service_config:
  upload_path: /path/to/answer_data/uploads
```

---

## 3. 서비스 구동 및 운영

### 3.1 Answer 백그라운드 서비스 시작
```bash
answer run -C /path/to/answer_data
```

### 3.2 헬스체크 및 API 정상 작동 확인
```bash
# 사이트 메타데이터 조회
curl -s http://127.0.0.1:9080/answer/api/v1/siteinfo

# 관리자 로그인 (JWT 토큰 발급)
curl -s -X POST http://127.0.0.1:9080/answer/api/v1/user/login/email \
  -H "Content-Type: application/json" \
  -d '{"e_mail":"admin@lionsql.local","pass":"adminadmin"}'
```

---

## 4. 장애 대응 및 문제 해결 (Troubleshooting)

### Q1: `table "pg_tables" not found` 오류가 발생하는 경우
- **원인**: 이전 버전 LionSQL에서 시스템 카탈로그 쿼리가 물리 스토리지로 직접 라우팅되어 발생합니다.
- **해결**: LionSQL v0.90.0-beta4 이상으로 업그레이드하십시오. `isVirtualCatalogQuery` 라우터가 활성화되어 가상 카탈로그 뷰로 자동 처리됩니다.

### Q2: 다중 행 INSERT 시 `missing primary key id` 오류가 발생하는 경우
- **원인**: VDBE `OpInsert` 실행기에서 identity 컬럼 자동 증가 번호 발급이 누락된 구버전 엔진 문제입니다.
- **해결**: `IdentityProvider` SPI가 통합된 최신 LionSQL 바이너리를 사용하십시오.
