# 실전 앱 연동 확장 마스터 플랜 (Answer / Vaultwarden / Metabase / Umami / NocoDB / Mattermost) (`notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md`)

> - **최초 작성일**: 2026-10-31
> - **과제 식별 코드**: `INTEG-ANSWER-1`, `INTEG-VAULTWARDEN-1`, `INTEG-METABASE-1`, `INTEG-UMAMI-1`, `INTEG-NOCODB-1`, `INTEG-MATTERMOST-1`
> - **목표**: 지금까지의 연동(Gitea, IOTA ERP, Grafana, Casdoor)으로 다져진 호환성 위에서, 실제 사용자가 많이 쓰는 오픈소스 앱 6종을 소스 무수정으로 LionSQL에 붙여 "실사용 가능성"을 입증하고, 그 과정에서 남은 호환성 결함을 제거
> - **상태**: 계획 수립 (`Pending`)
> - **연동 보고서**: [`ANSWER_INTEGRATION_REPORT.ko.md`](ANSWER_INTEGRATION_REPORT.ko.md) (1순위 상세)
> - **제외 결정**: Keycloak (후보에서 제외)

---

## 1. 현재 연동 현황 (출발점)

| 완료 과제 | 대상 | 스택 / 드라이버 | 프로토콜 | 이번 계획에 주는 자산 |
| :--- | :--- | :--- | :---: | :--- |
| `GITEA-MILESTONE-1`, `GITEA-PGWIRE-1` | Gitea (116 테이블) | Go + XORM | TDS + PGWire | XORM 인트로스펙션, 조건부 DDL, `BIGSERIAL`, `RETURNING`, `pg_indexes` |
| `IOTA-ERP-1` | IOTA-SDK ERP (47 마이그레이션) | Go + `pgx/v5`, sql-migrate | PGWire | `pgx` 엄격한 바이너리 프로토콜, UUID/TIMESTAMPTZ 인코딩, 마이그레이션 도구 |
| `GRAFANA-INTEGRATION-1` | Grafana (785 마이그레이션, 데이터 소스 2종) | Go + XORM, 데이터 소스 플러그인 | PGWire + TDS | `BYTEA` 왕복, advisory lock, `EXTRACT`, CTE 파생 테이블, 데이터 소스 쿼리 경로 |
| `CASDOOR-INTEGRATION-1` | Casdoor (44 테이블) | Go + XORM + Casbin | PGWire + TDS | 복합 PK, TDS `VARBINARY`, 16진 리터럴 |

### 1.1. 이미 확보된 호환성 (재사용 가능)
- PGWire v3 확장 쿼리 프로토콜, 바이너리 타입 인코딩(`pgx/v5` 통과).
- `information_schema` / `pg_catalog` 주요 인트로스펙션 뷰, MSSQL `sys.*` 가상 카탈로그.
- `RETURNING`, `BIGSERIAL`/`SERIAL`, 복합 PK, `BYTEA` hex 왕복, advisory lock 내장 함수.
- 조건부 DDL(`IF NOT EXISTS`), CTE 파생 테이블, `EXTRACT`/`statement_timestamp()`.

### 1.2. 아직 검증되지 않은 영역 (이번 계획의 신규 위험)
| 영역 | 해당 앱 | 비고 |
| :--- | :--- | :--- |
| libpq(C) 확장 프로토콜 + Diesel | Vaultwarden | 지금까지 Go 드라이버(`lib/pq`, `pgx`)만 검증 |
| JDBC (`pgjdbc`, `mssql-jdbc`) 메타데이터 쿼리 | Metabase | JVM 드라이버는 미검증 |
| Prisma (Rust 쿼리 엔진) | Umami | 마이그레이션 SQL과 확장 프로토콜 사용 패턴 미검증 |
| Knex (`pg`, `tedious`) + 임의 스키마 인트로스펙션 | NocoDB | Node.js 드라이버 미검증 |
| `jsonb`, 전문 검색(`tsvector`/GIN), `pg_trgm`, `DO $$` 블록 | Mattermost | 엔진 신규 기능 필요 가능성 높음 |

---

## 2. 앱별 개요

> 아래 프로토콜 지원 및 난이도는 각 앱의 스택에서 추정한 것이다. 각 앱의 1단계(사전 조사)에서 소스로 직접 확정하고 본 표를 갱신한다.

| 순서 | 과제 | 앱 | 용도 | 스택 | 대상 프로토콜 | 난이도(추정) |
| :---: | :--- | :--- | :--- | :--- | :--- | :---: |
| 1 | `INTEG-ANSWER-1` | Apache Answer | 사내 Q&A / 지식베이스 | Go + XORM(확인 필요) | PGWire (SQL Server 미지원) | 낮음 |
| 2 | `INTEG-VAULTWARDEN-1` | Vaultwarden | 비밀번호 관리자 (Bitwarden 호환) | Rust + Diesel | PGWire (MSSQL 백엔드 없음) | 낮음~보통 |
| 3 | `INTEG-METABASE-1` | Metabase | BI 대시보드 | JVM + JDBC | PGWire + TDS (데이터 소스 방식 우선) | 보통 |
| 4 | `INTEG-UMAMI-1` | Umami | 웹 애널리틱스 | Next.js + Prisma | PGWire (SQL Server 지원 미확인) | 보통 |
| 5 | `INTEG-NOCODB-1` | NocoDB | 노코드 DB 스튜디오 | Node.js + Knex | PGWire + TDS | 보통 |
| 6 | `INTEG-MATTERMOST-1` | Mattermost | 팀 채팅 | Go + sqlx | PGWire (MSSQL 백엔드 없음) | 높음 |

### 2.1. 순서 근거
1. **Answer**: Gitea/Casdoor와 동일 계열(XORM)이라 기존 경로를 재사용, 최소 비용으로 성공 사례 확보.
2. **Vaultwarden**: 규모가 작고(약 20 테이블) 스키마가 단순. 새로운 위험인 libpq 바이너리 프로토콜을 좁은 범위에서 검증.
3. **Metabase**: 외부 데이터 소스 모드로 JDBC 메타데이터/날짜 함수 호환을 먼저 검증(내부 앱 DB 모드는 선택).
4. **Umami**: Prisma 마이그레이션과 분석 쿼리(`date_trunc`, 타임존, 백분위)로 함수 호환성 확대.
5. **NocoDB**: 임의 스키마 대상 자동 인트로스펙션이므로 앞선 카탈로그 보강이 누적된 뒤에 수행하는 편이 효율적.
6. **Mattermost**: 100개 이상 테이블과 PG 전용 기능으로 작업량이 가장 크므로 마지막. 앞선 5개로 확보한 호환성이 최대일 때 도전.

---

## 3. 공통 수행 절차 (앱마다 동일)

Casdoor 연동에서 검증된 절차를 표준화한다.

1. **사전 조사**: 저장소 shallow clone, 지원 DB 드라이버/ORM/마이그레이션/특수 SQL 기능 목록화, 프로토콜 범위 확정.
2. **기동 및 초기화**: 전용 데이터 디렉토리(`data_<app>_<proto>`)로 LionSQL 기동, 앱 설치/마이그레이션 100% 통과 확인.
3. **E2E 검증**: 핵심 사용자 시나리오(로그인, 생성/조회/수정/삭제, 검색, 재기동 후 영속성) 수행.
4. **결함 수정**: 발견된 이슈는 LionSQL 측에서 수정하고, 이슈마다 `pkg/engine/<app>_compat_test.go` 회귀 테스트 추가. 앱 소스는 수정하지 않는다.
5. **3-Stage Verification Pipeline**: Build ➔ Test(`go test -tags wiredtiger -count=1 -v -race ./...`, 0 data race) ➔ Commit(Conventional Commits, 이모지 금지).
6. **문서화**: `doc/<APP>_INTEGRATION_GUIDE.ko.md` / `.md`, `notes/<APP>_INTEGRATION_REPORT.ko.md`, `TODO.md`, `CHANGELOG.md`, `notes/README.ko.md` / `README.md` 갱신.
7. **정리**: 임시 디렉토리와 백그라운드 프로세스 제거.

### 3.1. 앱별 신규 검증 포인트

| 앱 | 핵심 검증 항목 |
| :--- | :--- |
| Answer | 설치 마법사/`answer init`, 마이그레이션 SQL, 질문/답변/댓글/태그/투표/검색 |
| Vaultwarden | Diesel 마이그레이션, libpq 바이너리 타입(`bytea`, `uuid`, `timestamp`), 로그인/볼트 CRUD (Bitwarden 클라이언트 또는 API) |
| Metabase | 데이터 소스 연결, 스키마 동기화(Sync/Fingerprint), GUI 질문(`GROUP BY`/JOIN/날짜 함수), 대시보드. 선택: 내부 앱 DB(Liquibase) |
| Umami | Prisma `migrate deploy`, 이벤트 수집 API, 통계 쿼리(`date_trunc`, 타임존, 백분위) |
| NocoDB | Knex 마이그레이션, 외부 데이터 소스로 기존 LionSQL 테이블 연결, 스프레드시트 CRUD, 컬럼 추가/수정(DDL) |
| Mattermost | 전체 마이그레이션(`jsonb`, GIN, `pg_trgm`, `DO $$`), 채널/메시지/파일/검색 |

---

## 4. 위험 관리 및 중단 기준

| 위험 | 대응 |
| :--- | :--- |
| 앱이 요구하는 PG 전용 기능이 엔진 아키텍처(Spec-Out)와 충돌 | `GEMINI.md`의 Spec-Out 목록 대조. 충돌 시 구현하지 않고 "미지원"으로 문서화하고 해당 앱 중단 |
| 한 앱의 결함 수정이 타 앱 회귀 유발 | 매 단계 전체 `-race` 테스트 + 선행 연동 회귀 테스트(`casdoor_compat_test.go` 등) 유지 |
| 작업량 폭증(특히 Mattermost) | 앱별 결함이 20건을 넘거나 신규 서브시스템이 필요하면 중단하고 별도 계획서로 분리 |
| 프로토콜 지원 추정 오류(Answer의 MSSQL 등) | 1단계 사전 조사에서 소스 기준으로 확정 후 범위 조정 |

> 엔진 단순성 원칙(KISS, YAGNI): 앱 하나를 위해 엔진에 특수 분기를 넣지 않고, 표준 SQL/PG 동작으로 일반화해 구현한다.

## 5. 마일스톤

| 단계 | 내용 | 완료 기준 |
| :---: | :--- | :--- |
| M1 | Answer 연동 완료 | PGWire 설치/마이그레이션/E2E 통과, 문서 및 커밋 |
| M2 | Vaultwarden 연동 완료 | libpq/Diesel 경로 통과 |
| M3 | Metabase 연동 완료 | 데이터 소스 모드 PGWire + TDS 통과 |
| M4 | Umami 연동 완료 | Prisma 마이그레이션 및 통계 쿼리 통과 |
| M5 | NocoDB 연동 완료 | PGWire + TDS 외부 데이터 소스 CRUD/DDL 통과 |
| M6 | Mattermost 연동 완료 또는 중단 판정 | 전체 마이그레이션 통과 또는 중단 사유 문서화 |
| 종합 | 통합 호환성 보고서 | 앱 10종(기존 4 + 신규 6)의 지원 매트릭스를 `doc/`에 정리 |

## 6. 백로그 동기화
- 본 문서와 `TODO.md` 섹션 3.3을 항상 일치시킨다. 각 앱 완료 시 해당 행을 `[x]`로 갱신한다.
- 새 결함 유형이 발견되면 `notes/PITFALLS_AND_TRICKS.md`에 교훈을 추가한다.
