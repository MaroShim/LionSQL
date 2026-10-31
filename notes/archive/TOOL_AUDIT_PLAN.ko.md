# LionSQL 외부 GUI 클라이언트 실전 쿼리 감사 및 가상 카탈로그 하드닝 실행 계획 (TOOL_AUDIT_PLAN.ko.md)

- **과제 식별자**: `TOOL-AUDIT-1`
- **문서 목적**: 상용 및 오픈소스 GUI 데이터베이스 관리 도구(SSMS, DBeaver, DbGate, Beekeeper Studio)의 실제 SQL 쿼리 트레이스를 감사하고, 가상 카탈로그 인터셉터 및 TDS/PGWire 호환성을 하드닝하기 위한 아키텍처 프레임워크 및 실행 명세.
- **상태**: **완료 (`[x]`) - 100% PASS across Unit & Live Socket Integration Suites**
- **적용 대상**: Microsoft SQL Server TDS 7.3 (`:1433`), PostgreSQL Wire v3 (`:5433`), 가상 시스템 카탈로그 (`pkg/catalog/virtual`)

---

## 1. 개요 및 배경 (Background & Objectives)

LionSQL은 단일 경량 정적 바이너리로 구동되면서도 엔터프라이즈 환경과의 즉각적인 상호 운용성을 보장하기 위해 Microsoft SQL Server TDS 7.x/8.0 프로토콜과 PostgreSQL v3 프로토콜을 자체 내장하고 있습니다.

그러나 실제 데이터베이스 관리 도구(GUI Clients)는 사용자가 입력한 쿼리 외에도 연결 즉시 다음과 같은 수십 개의 시스템 카탈로그 쿼리를 백그라운드에서 동시다발적으로 실행합니다:
1. **SSMS (SQL Server Management Studio)**: `SERVERPROPERTY`, `DB_NAME()`, `sys.tables JOIN sys.schemas` 스키마 조인 트리 탐색.
2. **DBeaver Universal Database Tool**: `sys.tables` 와일드카드 조회, `OFFSET ... FETCH` 그리드 페이징, PostgreSQL `pg_class`/`pg_namespace` 메타데이터 탐색.
3. **DbGate**: 접속 즉시 9단계 엄격한 스키마 디스커버리 파이프라인(tables, columns, PK, FK, indexes, indexColumns, views, programmables, sqlCode) 실행 및 camelCase 필드 요구.
4. **Beekeeper Studio**: `information_schema.columns`와 계산 컬럼(Computed Columns) 조인, 대괄호(`[dbo].[table]`) 식별자 처리.

본 과제의 목표는 4대 관리 도구의 실전 쿼리를 전수 수집·분석하여, 불필요한 도구별 전용 하이재킹을 제거하고 표준에 기반한 **이중 명명 카탈로그(Dual-Naming Catalog)**와 **정규화된 쿼리 인터셉터**를 구축함으로써 어떠한 도구에서도 네이티브 데이터베이스처럼 매끄럽게 동작하도록 완벽히 하드닝하는 것입니다.

---

## 2. 도구별 쿼리 특성 및 요구 시퀀스 명세

```
 [ Client GUI Tools ]               [ Dual Wire Listeners ]              [ Virtual Catalog Hardening ]
 ┌──────────────────────┐           ┌──────────────────────┐            ┌────────────────────────────┐
 │ SSMS (DMV / Schemas) │ ──TDS───> │ :1433 TDS Listener   │ ──Route───>│ Column Alias Priority      │
 ├──────────────────────┤           ├──────────────────────┤            │ (s.name AS schema_name)    │
 │ DbGate (9-Step Pipe) │ ──TDS───> │ ClientSession        │            ├────────────────────────────┤
 ├──────────────────────┤           │ Database Context     │            │ Dual-Naming Schema         │
 │ Beekeeper (Brackets) │ ──TDS───> └──────────────────────┘            │ (snake_case + camelCase)   │
 ├──────────────────────┤           ┌──────────────────────┐            ├────────────────────────────┤
 │ DBeaver (PG / TDS)   │ ──PGW───> │ :5433 PGWire Listener│ ──Route───>│ Tolerant Virtual No-Op USE │
 └──────────────────────┘           └──────────────────────┘            │ (0xE3 ENVCHANGE / Synced)  │
                                                                        └────────────────────────────┘
```

### 2.1 SSMS (Object Explorer 트리 빌더)
- 접속 직후 환경 프로브: `SELECT SERVERPROPERTY('ProductVersion'), SERVERPROPERTY('EngineEdition'), DB_NAME()`
- 테이블 및 스키마 탐색:
  ```sql
  SELECT t.object_id, t.name AS table_name, s.name AS schema_name
  FROM sys.tables t
  JOIN sys.schemas s ON t.schema_id = s.schema_id
  WHERE t.is_ms_shipped = 0
  ORDER BY s.name, t.name;
  ```
- **요구사항**: `s.name AS schema_name`과 `t.name AS table_name`이 동일 기본 컬럼명(`name`)을 갖더라도 명시적 별칭이 우선 매핑되어 테이블명과 스키마명이 바뀌지 않아야 함.

### 2.2 DbGate (9단계 엄격한 스키마 디스커버리 파이프라인)
- DbGate는 연결 직후 다음 9단계 쿼리를 연속 실행하여 내부 캐시를 빌드함:
  1. `tables`: `SELECT object_id AS objectId, name AS pureName, ... FROM sys.tables`
  2. `columns`: `SELECT column_id, name AS columnName, ... FROM sys.columns`
  3. `primaryKeys`: `SELECT name AS constraintName, ... FROM sys.key_constraints WHERE type = 'PK'`
  4. `foreignKeys`: `SELECT name AS constraintName, ... FROM sys.foreign_keys`
  5. `indexes`: `SELECT name AS indexName, is_unique, ... FROM sys.indexes`
  6. `indexColumns`: `SELECT index_id, column_id, ... FROM sys.index_columns`
  7. `views`: `SELECT object_id AS objectId, name AS pureName FROM sys.views`
  8. `programmables`: `SELECT object_id AS objectId, name AS pureName, type FROM sys.objects WHERE type IN ('P', 'FN', 'IF')`
  9. `sqlCode`: `SELECT object_id AS objectId, definition AS sqlCode FROM sys.sql_modules`
- **요구사항**: 결과 행 맵에 snake_case(`object_id`)와 camelCase(`objectId`, `pureName`)가 공존해야 하며, `sys.sql_modules`가 정상 인터셉트되어야 함.

### 2.3 DBeaver (Universal Data Grid)
- SQL Server TDS 연결: `SELECT * FROM [dbo].[accounts] OFFSET 0 ROWS FETCH NEXT 50 ROWS ONLY;`
- PostgreSQL PGWire 연결:
  ```sql
  SELECT c.oid, c.relname, c.relkind, n.nspname
  FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  WHERE c.relkind IN ('r', 'v', 'm');
  ```
- **요구사항**: 와일드카드 조회, 슬라이딩 윈도우 페이징, PGWire 카탈로그 OID 매핑 완비.

### 2.4 Beekeeper Studio
- Tedious 드라이버 기반 연결 및 계산 컬럼 조인:
  ```sql
  SELECT c.column_name, c.data_type, cc.is_computed
  FROM information_schema.columns c
  LEFT JOIN sys.computed_columns cc ON cc.object_id = OBJECT_ID(c.table_name)
  WHERE c.table_name = 'customers';
  ```
- **요구사항**: 대괄호 식별자(`[dbo].[customers]`) 및 계산 컬럼 메타데이터 조인 무결성.

---

## 3. 핵심 아키텍처 결함 및 해결 설계 (Architecture Solutions)

### 3.1 컬럼 별칭 우선권 보장 (Column Alias Precedence Guard)
- **문제점**: `FormatResult`의 투영 맵(`colSourceMap`)이 `s.name AS schema_name`을 `name`으로 축약하여, 행 맵에서 명시적 별칭(`schema_name`)보다 기본 컬럼명(`name`)을 먼저 조회함으로써 스키마명에 테이블명이 덮어써지는 역전 결함 발생.
- **해결 설계**:
  - `pkg/catalog/virtual/result.go`에 `ExtractSQLParts(sql string)` 및 `FormatResultWithSQL` 신설.
  - 별칭 룩업 시 행 맵(`fr`)에 존재하는 명시적 별칭(`fr[col]` 또는 `fr[strings.ToLower(col)]`)을 기본 컬럼 소스(`fr[src]`)보다 무조건 우선 매칭하도록 엄격히 수정.

### 3.2 이중 명명 체계 (Dual-Naming Convention) 표준화
- **문제점**: DbGate는 `pureName`, `schemaName`, `objectId`, `tableName` 등의 camelCase 필드를 필수로 기대하나, 표준 MSSQL DMV는 `object_id`, `schema_id`, `name` 등의 snake_case 필드를 방출하여 불일치 발생.
- **해결 설계**:
  - `pkg/catalog/virtual/mssql.go`의 전체 가상 카탈로그 생성기(`execSysTablesMetadata`, `execSysObjectsMetadata`, `execSysObjectsViews`, `execSysObjectsProgrammables`, `execSysColumnsJoinedWithTypes`, `execSysIndexesPrimaryKey`, `execSysIndexesSecondary`, `execSysIndexColumns`, `execReferentialConstraintsForeignKey`, `execSysCommentsSqlCode`)에서 두 명명 규약을 동시에 공급:
    - `object_id` & `objectId`: 1000 단위 결정론적 정수 매핑
    - `name` & `pureName`: 객체/테이블명
    - `schema_name` & `schemaName`: `"dbo"`
    - `type`: 공백이 제거된 `"V"`, `"U"`, `"P"`
    - `syscomments` / `sys.sql_modules`: `object_id` & `id`, `definition` & `sqlCode` 동시 지원.

### 3.3 인터셉터 규칙 일반화 (Tolerant Interceptor Rules)
- **문제점**: `sys.tables`와 `sys.schemas` 조인 패턴이 특정 도구의 토큰(`pureName`)이 포함된 경우에만 인터셉트되어 SSMS의 정규 조인 쿼리가 코어 SQL 엔진으로 빠져 에러 발생.
- **해결 설계**:
  - `pkg/catalog/virtual/interceptor.go`에서 도구 특화 토큰 의존성을 완전히 제거하고, 표준 `JOIN sys.schemas` 구문을 보편적으로 수용하도록 정규식 일반화.
  - 레지스트리에 `SYS.SQL_MODULES` 추가 등록.

### 3.4 관용적 가상 No-Op `USE <dbname>` 동기화
- **아키텍처 불변 규칙**: LionSQL은 인스턴스 수준 격리(단일 프로세스당 단일 데이터베이스)를 고수하며, 런타임 멀티 데이터베이스 동시 마운트는 영구 배제(Spec Out)함.
- **관용 정책**: 도구 연결 초기화 시 전송되는 `USE master`, `USE [mydb]`는 에러를 내지 않고 즉시 TDS `0xE3 ENVCHANGE` 토큰을 응답하고 세션 가상 DB 컨텍스트(`ClientSession.Database`, `DB_NAME()`, `CURRENT_DATABASE()`)를 동기화하여 연결 실패를 원천 차단함.

---

## 4. 단계별 실행 및 검증 체크리스트 (Execution Checklist)

- [x] **Phase 1: 가상 카탈로그 결과 포맷터 별칭 우선순위 개편 (`pkg/catalog/virtual/result.go`)**
  - [x] `ExtractSQLParts(sql string)` 구현 (프로젝션, WHERE, ORDER BY 1-Pass 추출).
  - [x] `FormatResultWithSQL` 신설 및 클라이언트 SQL 별칭 최우선 반영.
  - [x] `FormatResult` 내 기본 컬럼보다 명시적 별칭 우선 매칭 로직 적용.

- [x] **Phase 2: 가상 카탈로그 이중 명명 및 메타데이터 정규화 (`pkg/catalog/virtual/mssql.go`)**
  - [x] `sys.tables`, `sys.views`, `sys.columns`, `sys.indexes`에 snake_case + camelCase 이중 키 공급.
  - [x] `sys.objects` 뷰 `type` 공백 트림 (`"V "` ➔ `"V"`).
  - [x] `sys.sql_modules` 및 `syscomments` 객체 ID(`object_id`, `id`) 및 정의문(`sqlCode`, `definition`) 연동.

- [x] **Phase 3: 가상 카탈로그 인터셉터 일반화 (`pkg/catalog/virtual/interceptor.go`)**
  - [x] 도구 특화 토큰 의존성 제거 및 표준 `JOIN sys.schemas` 인터셉트 보장.
  - [x] `SYS.SQL_MODULES` 인터셉터 등록.

- [x] **Phase 4: 단위 감사 테스트 스위트 구축 (`pkg/catalog/virtual/tool_audit_test.go`)**
  - [x] SSMS Object Explorer `sys.tables JOIN sys.schemas` 검증 (`table_name="customers"`, `schema_name="dbo"`).
  - [x] DBeaver 와일드카드 및 컬럼 메타데이터 검증.
  - [x] DbGate 9단계 스키마 디스커버리 파이프라인 전 항목 검증.
  - [x] Beekeeper `information_schema.columns` 조인 검증.
  - [x] 20개 고루틴 200회 연속 동시성 카탈로그 스트레스 검증 (0 Failures, 0 Data Races).

- [x] **Phase 5: TDS 실소켓 통합 테스트 구축 (`pkg/wire/tds/tool_audit_test.go`)**
  - [x] `go-mssqldb` 드라이버 기반 TCP `:1433` 라이브 소켓 연결.
  - [x] SSMS DMV 프로브 및 DbGate 9단계 파이프라인 실제 와이어 패킷 검증.
  - [x] DBeaver 그리드 페이징(`OFFSET ... FETCH`) 및 Beekeeper 브래킷 식별자 쿼리 검증.
  - [x] 8개 독립 세션 동시 DML + 카탈로그 조회 폭주 스트레스 검증 (교착 상태 0건).

- [x] **Phase 6: PGWire 실소켓 통합 테스트 구축 (`pkg/wire/tool_audit_test.go`)**
  - [x] `lib/pq` 드라이버 기반 TCP `:5433` 라이브 소켓 연결.
  - [x] DBeaver `pg_class`, `pg_attribute`, `pg_constraint` 카탈로그 정합성 검증.
  - [x] 다중 PGWire 세션 동시 접속 무결성 검증.

- [x] **Phase 7: 문서 동기화 및 가이드 반영**
  - [x] `doc/CLIENT_TOOL_INTEGRATION.md` Section 5.5 및 문제 해결 매트릭스 업데이트.
  - [x] `TODO.ko.md` 및 `CHANGELOG.md` 완료 마킹.

---

## 5. 검증 결과 및 품질 지표 (Quality Evidence)

```
=== RUN   TestToolAudit_SSMS_ObjectExplorer_Query
--- PASS: TestToolAudit_SSMS_ObjectExplorer_Query (0.00s)
=== RUN   TestToolAudit_DBeaver_CatalogQueries
--- PASS: TestToolAudit_DBeaver_CatalogQueries (0.00s)
=== RUN   TestToolAudit_DbGate_FullChoreography
--- PASS: TestToolAudit_DbGate_FullChoreography (0.00s)
=== RUN   TestToolAudit_Beekeeper_ComputedColumns_Query
--- PASS: TestToolAudit_Beekeeper_ComputedColumns_Query (0.00s)
=== RUN   TestToolAudit_Concurrent_Stress
--- PASS: TestToolAudit_Concurrent_Stress (0.02s)
PASS
ok      lionsql/pkg/catalog/virtual     2.947s  (with -race)

=== RUN   TestTDS_ToolAudit_SSMS_DMVs
--- PASS: TestTDS_ToolAudit_SSMS_DMVs (0.01s)
=== RUN   TestTDS_ToolAudit_DbGate_Choreography
--- PASS: TestTDS_ToolAudit_DbGate_Choreography (0.02s)
=== RUN   TestTDS_ToolAudit_DBeaver_Grid_Queries
--- PASS: TestTDS_ToolAudit_DBeaver_Grid_Queries (0.01s)
=== RUN   TestTDS_ToolAudit_Beekeeper_Brackets
--- PASS: TestTDS_ToolAudit_Beekeeper_Brackets (0.01s)
=== RUN   TestTDS_ToolAudit_Concurrent_GUI_Sessions
--- PASS: TestTDS_ToolAudit_Concurrent_GUI_Sessions (0.06s)
PASS
ok      lionsql/pkg/wire/tds            3.812s  (with -race)

=== RUN   TestPGWire_ToolAudit_DBeaver_Catalogs
--- PASS: TestPGWire_ToolAudit_DBeaver_Catalogs (0.01s)
=== RUN   TestPGWire_ToolAudit_Concurrent_Sessions
--- PASS: TestPGWire_ToolAudit_Concurrent_Sessions (0.03s)
PASS
ok      lionsql/pkg/wire                3.115s  (with -race)
```

- **품질 판정**: 3개 패키지 전 항목 **100% PASS**, **데이터 레이스 0건**, **교착 상태 0건**.
- **도구 호환성**: SSMS, DBeaver, DbGate, Beekeeper Studio 4대 도구 전 항목 프로덕션 준비 완료.
