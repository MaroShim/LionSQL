# LionSQL 기반 Grafana 구축 및 듀얼 데이터 소스 공식 연동 가이드 (`doc/GRAFANA_INTEGRATION_GUIDE.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-10-31
> - **대상 릴리즈**: LionSQL v0.90.0-beta4+
> - **연동 대상**: Grafana v10.x ~ v13.x+ (github.com/grafana/grafana, 공식 소스 무수정 연동)
> - **데이터베이스 드라이버**:
>   - 내부 메타데이터 백엔드: `github.com/lib/pq` (XORM 기반 PostgreSQL Wire Protocol v3, TCP `:5433`)
>   - 쿼리 데이터 소스 1: `grafana-postgresql-datasource` (`lib/pq`, TCP `:5433`)
>   - 쿼리 데이터 소스 2: `mssql` (`github.com/microsoft/go-mssqldb`, TDS 7.x/8.0, TCP `:1433`)
> - **참조 문서**:
>   - 실전 연동 검증 보고서: [`notes/GRAFANA_INTEGRATION_REPORT.ko.md`](../notes/GRAFANA_INTEGRATION_REPORT.ko.md)
>   - 아키텍처 계획서: [`notes/GRAFANA_INTEGRATION_PLAN.ko.md`](../notes/GRAFANA_INTEGRATION_PLAN.ko.md)
>   - 영문 가이드: [`doc/GRAFANA_INTEGRATION_GUIDE.md`](GRAFANA_INTEGRATION_GUIDE.md)

---

## 1. 개요 및 듀얼 채널 아키텍처

**Grafana**는 전 세계 클라우드 네이티브 인프라 및 엔터프라이즈 환경에서 가장 널리 사용되는 표준 관측성(Observability) 및 대시보드 시각화 플랫폼입니다.

LionSQL은 외부 데몬(PgBouncer, Redis, Nginx, etcd) 의존성이 전혀 없는 **단일 정적 독립 실행형(Zero-Sidecar) 고성능 OLTP 데이터베이스 엔진**으로서, 단 하나의 프로세스에서 **PostgreSQL Wire Protocol v3(`:5433`)**와 **Microsoft SQL Server TDS(`:1433`)**를 동시에 리슨합니다.

이를 통해 LionSQL은 Grafana와 **두 가지 독립적인 채널**로 동시에 상호작용합니다:

```
+---------------------------------------------------------------------------------+
|                                Grafana Server                                   |
|                                                                                 |
|  [Channel 1: 내부 메타데이터 백엔드 DB]         [Channel 2: 대시보드 쿼리 데이터 소스] |
|  - 785개 마이그레이션 (사용자, RBAC, 대시보드,   - 실시간 SQL 질의 및 시계열/테이블 시각화 |
|    폴더, 알림, 데이터 소스, 암호화 키 저장)      - 플러그인 1: PostgreSQL (:5433)      |
|  - 프로토콜: PostgreSQL Wire (:5433)             - 플러그인 2: Microsoft SQL Server (:1433)|
+---------------------------------------------------------------------------------+
                 |                                        |             |
                 | TCP :5433 (PGWire)                     | TCP :1433   | TCP :5433
                 v                                        v (TDS)       v (PGWire)
+---------------------------------------------------------------------------------+
|                        LionSQL Database Kernel (Single Binary)                  |
|  - PostgreSQL Wire Listener: pkg/wire/server.go (:5433)                         |
|  - Microsoft SQL Server TDS Listener: pkg/wire/tds/server.go (:1433)            |
|  - Execution Engines: VDBE Bytecode VM & Volcano CBO Engine                     |
|  - Storage Engines: WiredTiger MVCC B+Tree Engine / In-Memory OCC Engine        |
+---------------------------------------------------------------------------------+
```

### 핵심 아키텍처 이점
1. **클라이언트 코드 제로 수정 (Zero-Code Modification)**:
   - Grafana 코어 소스 코드나 785개의 데이터베이스 마이그레이션(728개 코어 스키마 + 57개 유니파이드 리소스 스키마)을 단 한 줄도 수정하지 않고 100% 그대로 실행합니다.
2. **내부 백엔드 + 듀얼 데이터 소스 올인원(All-in-One) 통합**:
   - 별도의 PostgreSQL 서버와 SQL Server 인스턴스를 각각 구축할 필요 없이, 단일 `lionsql` 바이너리 하나로 Grafana 메타데이터 저장소와 PG/MSSQL 대시보드 쿼리 타깃을 동시에 제공합니다.
3. **고급 보안 및 암호화 호환성 (Envelope Encryption)**:
   - Grafana의 시크릿 관리 서브시스템이 요구하는 `BYTEA`(`\x<hex>`) 바이너리 암호화 키(`data_keys`) 직렬화/역직렬화 및 어드바이저리 락(`pg_try_advisory_lock`)을 완벽히 지원합니다.

---

## 2. 사전 요구사항 및 LionSQL 기동

### 2.1. LionSQL 바이너리 빌드
```bash
# 프로덕션 Unix 바이너리 (WiredTiger MVCC 디스크 영속 엔진 포함)
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
```

### 2.2. LionSQL 데몬 시작
Grafana 전용 데이터 디렉토리(예: `data_grafana/`)를 지정하여 구동합니다:
```bash
./bin/lionsql -data-dir=data_grafana -admin-pass=1234567890
```

기동 즉시 다음 엔드포인트가 활성화됩니다:
- **PostgreSQL Wire Protocol v3**: `127.0.0.1:5433`
- **Microsoft SQL Server TDS 7.x/8.0**: `127.0.0.1:1433`
- **LionSQL Web Studio**: `http://127.0.0.1:8090/_/`

---

## 3. Grafana 빌드 및 환경 설정

### 3.1. Grafana 바이너리 빌드 및 코어 SQL 플러그인 설치
Grafana 최신 아키텍처(v13.0+)에서는 PostgreSQL 및 MSSQL 데이터 소스가 서명된 외부 코어 플러그인 형태로 분리되어 동작합니다. 버전 호환성 검사를 통과하도록 `-ldflags "-X main.version=13.0.0"` 옵션을 부여하여 빌드한 뒤 플러그인을 설치합니다:

```bash
cd /Users/maro/Projects/grafana

# 1. Grafana 서버 및 CLI 바이너리 빌드
go build -ldflags "-w -s -X main.version=13.0.0" -o ./bin/grafana ./pkg/cmd/grafana

# 2. 공식 PostgreSQL 및 Microsoft SQL Server 데이터 소스 플러그인 설치
./bin/grafana cli --pluginsDir "/Users/maro/Projects/grafana/data/plugins" plugins install grafana-postgresql-datasource
./bin/grafana cli --pluginsDir "/Users/maro/Projects/grafana/data/plugins" plugins install mssql
```

### 3.2. 설정 파일 구성 (`conf/custom.ini`)
Grafana의 내부 메타데이터 저장소로 LionSQL의 PostgreSQL 와이어 포트(`:5433`)를 지정합니다:

```ini
[server]
http_port = 3100
domain = localhost

[database]
type = postgres
host = 127.0.0.1:5433
name = grafana
user = admin
password = 1234567890
ssl_mode = disable

[security]
admin_user = admin
admin_password = 1234567890

[paths]
data = /Users/maro/Projects/grafana/data
logs = /Users/maro/Projects/grafana/data/log
plugins = /Users/maro/Projects/grafana/data/plugins
provisioning = /Users/maro/Projects/grafana/conf/provisioning
```

### 3.3. Grafana 서버 기동 및 785개 마이그레이션 검증
```bash
cd /Users/maro/Projects/grafana
./bin/grafana server --config conf/custom.ini
```

기동 로그에서 다음 두 마이그레이션 엔진이 에러 없이 완료됨을 확인할 수 있습니다:
1. **Core Migrator**: `migrations performed=728` (`migration_log` 테이블 728개 레코드 기록)
2. **Resource Migrator**: `migrations performed=57` (`resource_migration_log` 테이블 57개 레코드 기록)
3. **RBAC & Envelope Encryption**: 기본 `admin` 계정 생성, 역할/권한(`role`, `permission`, `builtin_role`) 시딩, `data_keys` 암호화 키 생성 완료 후 `HTTP :3100` 리스닝 시작.

---

## 4. 듀얼 데이터 소스(PGWire & MSSQL TDS) 등록 및 쿼리 검증

Grafana가 기동된 상태에서 REST API 또는 웹 UI를 통해 LionSQL의 두 프로토콜 포트를 데이터 소스로 등록하고 실시간 쿼리를 실행할 수 있습니다.

### 4.1. 데이터 소스 1: LionSQL PostgreSQL (`:5433`) 등록
```bash
curl -s -u admin:1234567890 -X POST http://localhost:3100/api/datasources \
  -H "Content-Type: application/json" \
  -d '{
    "name": "LionSQL-PG",
    "type": "postgres",
    "access": "proxy",
    "url": "127.0.0.1:5433",
    "user": "admin",
    "database": "grafana",
    "basicAuth": false,
    "jsonData": {
      "sslmode": "disable",
      "postgresVersion": 1500
    },
    "secureJsonData": {
      "password": "1234567890"
    }
  }'
```

### 4.2. 데이터 소스 2: LionSQL Microsoft SQL Server (`:1433`) 등록
```bash
curl -s -u admin:1234567890 -X POST http://localhost:3100/api/datasources \
  -H "Content-Type: application/json" \
  -d '{
    "name": "LionSQL-MSSQL",
    "type": "mssql",
    "access": "proxy",
    "url": "127.0.0.1:1433",
    "user": "admin",
    "database": "grafana",
    "basicAuth": false,
    "jsonData": {
      "encrypt": "disable"
    },
    "secureJsonData": {
      "password": "1234567890"
    }
  }'
```

### 4.3. 데이터 소스 헬스 체크 (`/api/datasources/uid/<uid>/health`)
```bash
# PostgreSQL Data Source Health Check
curl -s -u admin:1234567890 http://localhost:3100/api/datasources/uid/<pg-datasource-uid>/health
# 응답: {"message":"Database Connection OK","status":"OK"}

# MSSQL Data Source Health Check
curl -s -u admin:1234567890 http://localhost:3100/api/datasources/uid/<mssql-datasource-uid>/health
# 응답: {"message":"Database Connection OK","status":"OK"}
```

### 4.4. 듀얼 프로토콜 동시 데이터 쿼리 실행 (`POST /api/ds/query`)
하나의 대시보드 패널 요청에서 PGWire(`:5433`)와 MSSQL TDS(`:1433`) 데이터 소스를 동시에 쿼리하여 동일한 카탈로그 데이터를 조회합니다:

```bash
curl -s -u admin:1234567890 -X POST http://localhost:3100/api/ds/query \
  -H "Content-Type: application/json" \
  -d '{
    "queries": [
      {
        "refId": "PG",
        "datasource": { "type": "postgres", "uid": "<pg-datasource-uid>" },
        "rawSql": "SELECT id, name, type, url FROM data_source ORDER BY id",
        "format": "table"
      },
      {
        "refId": "MSSQL",
        "datasource": { "type": "mssql", "uid": "<mssql-datasource-uid>" },
        "rawSql": "SELECT id, name, type, url FROM data_source ORDER BY id",
        "format": "table"
      }
    ],
    "from": "now-1h",
    "to": "now"
  }'
```

응답 결과(`HTTP 200 OK`):
```json
{
  "results": {
    "MSSQL": {
      "status": 200,
      "frames": [{
        "data": {
          "values": [
            [1, 2],
            ["LionSQL-PG", "LionSQL-MSSQL"],
            ["postgres", "mssql"],
            ["127.0.0.1:5433", "127.0.0.1:1433"]
          ]
        }
      }]
    },
    "PG": {
      "status": 200,
      "frames": [{
        "data": {
          "values": [
            [1, 2],
            ["LionSQL-PG", "LionSQL-MSSQL"],
            ["postgres", "mssql"],
            ["127.0.0.1:5433", "127.0.0.1:1433"]
          ]
        }
      }]
    }
  }
}
```

---

## 5. 트러블슈팅 및 운영 팁

1. **플러그인 미등록 오류 (`Plugin not registered` / `404`)**:
   - 소스에서 직접 빌드한 Grafana 바이너리는 기본 버전이 `10.2.0`으로 설정되어 최신 코어 플러그인(`v13.0.x`) 로드를 거부할 수 있습니다. 반드시 `-ldflags "-X main.version=13.0.0"`을 포함하여 빌드하고 `data/plugins` 경로에 플러그인이 설치되었는지 확인하십시오.
2. **암호화 키(`data_keys`) 복호화 무결성**:
   - Grafana는 데이터 소스의 비밀번호(`secureJsonData.password`)를 저장할 때 `data_keys` 테이블의 `BYTEA` 암호화 키를 사용합니다. LionSQL은 PostgreSQL 표준 `\x<hex>` 포맷을 통해 바이너리 키를 무손실로 저장 및 반환합니다.
3. **고속 스토리지 백업**:
   - LionSQL의 모든 상태(Grafana 대시보드, 사용자, 권한, 데이터 소스)는 단일 `--data-dir` 디렉토리 내부에 영속화되므로, 해당 디렉토리 스냅샷만으로 완벽한 백업 및 복구가 가능합니다.
