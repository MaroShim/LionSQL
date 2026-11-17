# GoCD Enterprise CI/CD Docker E2E 실전 통합 검증 보고서 (`GOCD_INTEGRATION_REPORT.ko.md`)

> - **검증 일자**: 2026-11-17
> - **대상 애플리케이션**: GoCD Server v24.1.0 (`gocd/gocd-server:v24.1.0`, Java 17 + Spring + Hibernate + MyBatis + Liquibase 4.27)
> - **연동 프로토콜**: PostgreSQL Wire Protocol v3 (`:5435`, PostgreSQL JDBC Driver 42.x)
> - **스토리지 엔진**: LionSQL In-Memory OCC (`compact` / AOF + RDB 영속화)
> - **최종 판정**: **100% PASS (Liquibase 107개 Changeset 전수 완주, 25개 테이블 + 2개 핵심 뷰 생성, Spring/Hibernate 초기화 및 HTTP 8153 대시보드/REST API 정상 기동)**

---

## 1. 검증 개요 및 환경

Thoughtworks가 개발한 엔터프라이즈 배포 자동화 서버 **GoCD (`v24.1.0`)** 공식 Docker 컨테이너를 구동하여 LionSQL과 연결하고, 내장된 **Liquibase 4.27** 마이그레이션 엔진의 107개 스키마 변경 셋과 **Hibernate + MyBatis** 복합 영속성 계층의 전체 부트스트랩을 검증하였습니다.

- **컨테이너 이미지**: `gocd/gocd-server:v24.1.0`
- **데이터베이스 설정 (`db.properties`)**:
  - `db.driver=org.postgresql.Driver`
  - `db.url=jdbc:postgresql://host.docker.internal:5435/gocd`

---

## 2. E2E 마이그레이션 및 런타임 검증 결과

1. **Liquibase 107개 스키마 마이그레이션 전수 통과**:
   - `databasechangelog`, `databasechangeloglock` 생성 및 잠금 제어 성공.
   - `ACCESSTOKEN`, `AGENTS`, `ARTIFACTPLANS`, `ARTIFACTPROPERTIESGENERATOR`, `BUILDS`, `BUILDSTATETRANSITIONS`, `CLUSTERPROFILES`, `CONFIGS`, `ENVIRONMENTVARIABLES`, `MATERIALS`, `MODIFICATIONS`, `MODIFIEDFILES`, `NOTIFICATIONFILTERS`, `OAUTHCLIENTS`, `PIPELINELABELCOUNTS`, `PIPELINEMATERIALREVISIONS`, `PIPELINES`, `PIPELINESELECTIONS`, `PIPELINESTATES`, `PLUGINS`, `RESOURCES`, `SERVERBACKUPS`, `STAGEARTIFACTCLEANUPPROHIBITED`, `STAGES`, `USERS`, `VERSIONINFOS` 등 **25개 엔터프라이즈 테이블** 및 **`_BUILDS`, `_STAGES` 2개 핵심 뷰(View)** 생성 완료.
   - `DataMigrationRunner: Data migrations completed.` 정상 완료.
2. **웹 대시보드 및 REST API 기동 확인**:
   - `GET /go/home` ➔ `302 Found` (`Location: http://127.0.0.1:8153/go/pipelines` ➔ `/go/admin/pipelines/create?group=defaultGroup` HTTP 200 OK).
   - `GET /go/api/version` (`Accept: application/vnd.go.cd.v1+json`) ➔ HTTP 200 OK (`"full_version": "24.1.0 (18867-a60529cdf34a96de310bc05e2041f2ac23014707)"`).

---

## 3. 발굴 및 해결된 엔진 호환성 이슈

1. **PostgreSQL `pg_get_keywords()` 배열 비교(`<> ALL ('{...}')`) 및 `string_agg` 가상 집계 지원 (`pkg/proc/eval.go`, `pkg/catalog/virtual/result.go`)**:
   - JDBC `DatabaseMetaData.getSQLKeywords()`가 실행하는 `select string_agg(word, ',') from pg_catalog.pg_get_keywords() where word <> ALL ('{...}'::text[])` 쿼리를 지원하여 Liquibase 초기화 시 `NullPointerException`을 원천 해결.
2. **PostgreSQL 트리거 함수(`RETURNS TRIGGER AS $$ ... $$`) 및 `EXECUTE PROCEDURE` DDL 파싱 (`pkg/proc/lexer.go`, `pkg/proc/parser_ddl.go`, `pkg/engine/sqlexec.go`)**:
   - `CREATE OR REPLACE FUNCTION ... RETURNS TRIGGER AS $$ ... $$ LANGUAGE plpgsql;` 및 `CREATE TRIGGER ... EXECUTE PROCEDURE ...` 문법 수용.
   - Lexer에서 `$$ ... $$` 달러 인용 리터럴과 `$1`, `$2` 위치 매개변수(`TokenVariable`) 및 단독 `$` 토큰의 포인터 전진 불변성(`l.readChar()`) 보장.
3. **PostgreSQL JDBC `getColumns()` 메타데이터 컬럼 보강 (`pkg/catalog/virtual/pg.go`)**:
   - `pg_attribute` + `pg_class` + `pg_namespace` 조인 시 `nspname`, `atttypmod`, `attlen`, `attnotnull`, `relname`, `adsrc` 컬럼을 정식 반환하여 Liquibase 스냅샷 검증 통과.
4. **PGWire Extended Query Protocol 카탈로그 뷰(`_builds`, `_stages`) VDBE 우회 라우팅 (`pkg/wire/server.go`)**:
   - `referencesCatalogView(qc)` 가드를 추가하여 카탈로그 뷰를 조회하는 Prepared Statement가 VDBE 물리 테이블 스캔으로 빠지지 않고 Tier-2 CBO View Expansion 엔진으로 라우팅되도록 교정.

---

## 4. 회귀 테스트 등록 현황

- [`pkg/engine/gocd_compat_test.go`](../../pkg/engine/gocd_compat_test.go): Liquibase 락/체인지로그, `string_agg` + `<> ALL`, 트리거 함수(`RETURNS TRIGGER AS $$`), JDBC `getColumns()` 메타데이터, `_builds` 뷰 조회 통합 검증.
- [`pkg/proc/lexer_test.go`](../../pkg/proc/lexer_test.go): `TestLexer_DollarQuotedStringsAndPositionalParams` 달러 인용 및 `$1` 매개변수 전진 불변성 검증.
- [`pkg/proc/characteristics_test.go`](../../pkg/proc/characteristics_test.go): Hibernate `$1` 매개변수 쿼리 특성 추출 검증.
- [`pkg/wire/routing_ast_test.go`](../../pkg/wire/routing_ast_test.go): `TestRoutingAST_CatalogViewBypassesVDBE` 카탈로그 뷰 VDBE 우회 검증.
