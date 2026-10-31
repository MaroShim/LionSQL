# Grafana 듀얼 프로토콜(PGWire & TDS) 통합 및 검증 계획서 (`notes/GRAFANA_INTEGRATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-10-31
> - **과제 식별 코드**: `GRAFANA-INTEGRATION-1`
> - **목표**: 오픈소스 관측성/시각화 표준 플랫폼인 [Grafana](https://github.com/grafana/grafana)를 LionSQL에 연동하여, **내부 메타데이터 DB(PGWire :5433)**와 **듀얼 쿼리 데이터 소스(MSSQL TDS :1433 및 PGWire :5433)**를 동시 검증
> - **상태**: 완료 (`Completed [x]`)
> - **참조 문서**:
>   - Grafana 마일스톤 보고서: [`notes/GRAFANA_INTEGRATION_MILESTONE.ko.md`](GRAFANA_INTEGRATION_MILESTONE.ko.md) ([English](GRAFANA_INTEGRATION_MILESTONE.md))
>   - Grafana 공식 가이드: [`doc/GRAFANA_INTEGRATION_GUIDE.ko.md`](../doc/GRAFANA_INTEGRATION_GUIDE.ko.md) ([English](../doc/GRAFANA_INTEGRATION_GUIDE.md))
>   - Gitea 마일스톤: [`notes/GITEA_INTEGRATION_MILESTONE.ko.md`](GITEA_INTEGRATION_MILESTONE.ko.md)
>   - IOTA ERP 마일스톤: [`notes/IOTA_ERP_INTEGRATION_MILESTONE.ko.md`](IOTA_ERP_INTEGRATION_MILESTONE.ko.md)

---

## 1. 개요 및 연동 아키텍처

Grafana는 전 세계 인프라 및 애플리케이션 모니터링의 표준 대시보드 플랫폼입니다.
LionSQL은 두 가지 상이한 채널을 통해 Grafana와 상호작용합니다:

```
+─────────────────────────────────────────────────────────────────────────────────+
|                                Grafana Server                                   |
|                                                                                 |
|  [Channel 1: 내부 메타데이터 DB]                [Channel 2: 대시보드 쿼리 데이터 소스] |
|  - 계정, 권한, 대시보드 JSON, 폴더, 알림 저장    - 차트 패널 시계열 집계 질의          |
|  - 프로토콜: PostgreSQL Wire (:5433)             - 프로토콜: MSSQL (:1433) & PG (:5433)|
+─────────────────────────────────────────────────────────────────────────────────+
                 │                                        │             │
                 │ TCP :5433 (PGWire)                     │ TCP :1433   │ TCP :5433
                 ▼                                        ▼ (TDS)       ▼ (PGWire)
+─────────────────────────────────────────────────────────────────────────────────+
|                        LionSQL Database Kernel (Single Binary)                  |
|  - PostgreSQL Wire Listener: pkg/wire/server.go (:5433)                         |
|  - Microsoft SQL Server TDS Listener: pkg/wire/tds/server.go (:1433)            |
|  - Execution Engines: VDBE Bytecode VM & Volcano CBO Engine                     |
|  - Storage Engines: In-Memory OCC (memengine) / WiredTiger MVCC Disk Engine     |
+─────────────────────────────────────────────────────────────────────────────────+
```

---

## 2. 세부 단계별 구현 및 검증 계획

### 1단계: 저장소 준비 및 환경 구성
- `/Users/maro/Projects/grafana` 위치에 저장소 클론 (shallow clone으로 빠른 확보).
- Go 백엔드 의존성 다운로드 및 빌드 검증 (`cmd/grafana-server`).
- `conf/custom.ini` 생성:
  - `[database] type = postgres`, `host = 127.0.0.1:5433`, `user = admin`, `password = 1234567890`, `name = grafana`, `ssl_mode = disable`.
  - `[server] http_port = 3100` (Gitea 3000번 포트와의 충돌 방지).

### 2단계: 내부 메타데이터 DB 마이그레이션 (PGWire :5433)
- LionSQL 서버 데몬 기동 (`:5433`).
- Grafana 서버 실행으로 내부 50여 개 스키마 마이그레이션(사용자, 권한, 대시보드, 팀 등) 트리거.
- 미지원 DDL/타입/카탈로그 쿼리 발견 시 LionSQL PGWire 계층 즉각 보완.
- `http://localhost:3100` 관리자 로그인 화면 정상 응답 확인 (`HTTP 200 OK`).

### 3단계: Microsoft SQL Server 데이터 소스 연동 (TDS :1433)
- Grafana 내부에서 **공식 Microsoft SQL Server Data Source** 플러그인 설정 (`host = 127.0.0.1:1433`, `user = sa`, `password = 1234567890`).
- 연결 테스트(Test Connection) 수행 및 시스템 카탈로그 질의 호환성 확인.
- 실제 T-SQL 대시보드 패널 생성:
  - 시스템 테이블, 세션 풀러 상태, TPC-C 벤치마크 결과 테이블 대상 시계열 집계 질의.
  - `$__timeFilter()`, `DATEADD()`, `DATEDIFF()`, `GROUP BY` 시계열 쿼리 실행 검증.

### 4단계: PostgreSQL 데이터 소스 연동 (PGWire :5433)
- Grafana 내부에서 **공식 PostgreSQL Data Source** 플러그인 설정 (`host = 127.0.0.1:5433`).
- 동일한 지표를 PostgreSQL 방언 쿼리로 조회하는 대시보드 패널 생성 및 듀얼 검증.

### 5단계: 문서화 및 3단계 파이프라인 커밋
- 실전 가이드: `doc/GRAFANA_INTEGRATION_GUIDE.ko.md` 및 `.md` 작성.
- 마일스톤 보고서: `notes/GRAFANA_INTEGRATION_MILESTONE.ko.md` 및 `.md` 작성.
- 3-Stage Pipeline (Build ➔ Test ➔ Commit) 완수.

---

## 3. 리스크 분석 및 방어 전략

1. **Grafana 빌드 볼륨**:
   - 프론트엔드 전체 번들링(Webpack/NodeJS)은 빌드 시간이 오래 걸릴 수 있으므로, 이미 빌드된 정적 에셋(public)을 활용하거나 Go 백엔드 API/헤드리스 모드로 신속히 검증.
2. **시계열 매크로 확장**:
   - Grafana SQL 데이터 소스는 `$__timeGroup()`, `$__timeFilter()` 등의 고유 매크로를 실제 SQL로 치환하여 전송하므로, 치환된 날짜/시간 연산자(`DATEADD`, `DATE_TRUNC`)의 지원 여부 집중 점검.
