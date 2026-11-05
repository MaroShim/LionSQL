# LionSQL Materialized View Virtual Alias & Tolerant Refresh Architecture Plan (`MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-05
> - **상태**: 승인 완료 (Post-1.0 및 엔터프라이즈 앱 호환성 로드맵 반영: `v1.1.0`)
> - **관련 과제**: `FEAT-MATVIEW-ALIAS-1` (Materialized View Virtual Alias & Tolerant Refresh)
> - **연계 태스크**: `INTEG-MATTERMOST-1` (Mattermost), `INTEG-NOCODB-1` (NocoDB)
> - **핵심 불변식**: `[RULE:RadicalKISS]` (극단적 단순성 및 실용주의), `[RULE:SafeDefaults]` (안전한 기본값), `[RULE:ModularInterface]` (인터페이스 분리)

---

## 1. 개요 및 배경 (Context & Problem)

Mattermost, Discourse, GitLab, Redmine 등 다수의 엔터프라이즈 오픈소스 애플리케이션 및 마이그레이터(Knex, Flyway, Liquibase, Prisma)는 집계 및 통계 쿼리의 성능 최적화를 위해 **`MATERIALIZED VIEW`** 생성을 요구한다.

- **문제점**:
  - 전통적인 RDBMS 방식의 "물리 디스크 스토리지 기반 Materialized View"는 물리 테이블 생성, 배타적 락/`CONCURRENTLY` 원자적 스왑, 쓰기 증폭(Write Amplification), 크래시 복구(WAL) 등 수천 줄의 극도로 복잡한 동기화 서브시스템을 요구하며 버그 발생 위험이 매우 높다.
  - 마테리얼라이즈드 뷰 미지원 시 Mattermost 등의 엔터프라이즈 애플리케이션 부트스트랩이 즉시 실패한다.
- **해결책**:
  - 현대적 인메모리/분석 DB(DuckDB, CockroachDB, Redshift Smart Rewrite)가 채택하고 있는 **"가상 뷰 인라인 치환(Virtual Alias) + No-Op 관용 리프레시(Tolerant Refresh)"** 패턴을 공식 도입한다.
  - 애플리케이션과의 계약(Contract: DDL 성공, 정확한 조회 결과, `REFRESH` 성공)을 100% 만족시키면서, 엔진 내부 구현 복잡도를 99% 절감한다.

---

## 2. 핵심 아키텍처 원리 (Architectural Invariants)

```
[ Application / ORM ]
         │
         ├── 1. CREATE MATERIALIZED VIEW poststats AS SELECT ...
         │      │
         │      ▼
         │   [ LionSQL DDL Router ]
         │      └── 내부 일반 가상 뷰(Virtual View)로 등록 (`relkind = 'm'`)
         │
         ├── 2. SELECT * FROM poststats WHERE userid = '...'
         │      │
         │      ▼
         │   [ Volcano CBO / In-Memory OCC Execution Engine ]
         │      └── CTE / 인라인 서브쿼리 치환으로 실시간 평가 (항상 최신 데이터, Stale 캐시 0)
         │
         └── 3. REFRESH MATERIALIZED VIEW poststats
                │
                ▼
             [ Tolerant Refresh Handler ]
                └── 뷰 존재 여부 검증 후 즉시 OK (0.00ms) 반환 (No-Op)
```

### 2.1 3대 핵심 메커니즘
1. **생성 라우팅 (Virtual Alias DDL)**:
   - `CREATE MATERIALIZED VIEW [IF NOT EXISTS] <name> AS <select_query>` 구문 수신 시:
   - 문법 파서에서 일반 뷰 DDL(`CREATE VIEW <name> AS <select_query>`)과 동일한 뷰 정의 객체(`storage.View`)로 생성하여 카탈로그에 등록.
   - 단, PostgreSQL 시스템 카탈로그 메타데이터 호환성을 위해 `pg_class.relkind` 값을 일반 뷰 `'v'` 대신 마테리얼라이즈드 뷰 식별자인 **`'m'`**으로 마킹.
2. **실시간 가상 평가 (Always Freshness)**:
   - 클라이언트가 `SELECT ... FROM <matview>`를 질의하면, LionSQL의 기존 뷰 치환 엔진(`rewriteViewsAsCTE` 또는 서브쿼리 플래너)이 작동하여 실시간으로 실행.
   - **효과**: 물리 캐시의 최대 약점인 "리프레시 전까지 낡은 데이터(Stale Data)를 보는 현상"이 원천 제거되고 언제나 무지연 실시간 최신성 보장.
   - LionSQL의 In-Memory OCC 및 2-Tier CBO 엔진은 서브-밀리초 단위로 동작하므로 중소/중견 규모 집계에서는 캐시보다 빠른 체감 성능 제공.
3. **관용 리프레시 (Tolerant No-Op Refresh)**:
   - `REFRESH MATERIALIZED VIEW [CONCURRENTLY] <name> [WITH [NO] DATA]` 구문 수신 시:
   - 대상 뷰가 카탈로그에 존재하는지 검증 (`relkind = 'm'`).
   - 존재할 경우 실제 디스크 물리 복제를 수행하지 않고 즉시 성공 응답(`Command: REFRESH MATERIALIZED VIEW`, `DurationUS: < 100us`) 반환.
   - 존재하지 않을 경우 표준 PostgreSQL 규격에 맞춰 `relation "..." does not exist` 에러 반환.
4. **삭제 (DROP MATERIALIZED VIEW)**:
   - `DROP MATERIALIZED VIEW [IF EXISTS] <name> [CASCADE | RESTRICT]` 구문 수신 시:
   - 기존 뷰 삭제 핸들러(`DropView`)로 라우팅하여 카탈로그에서 정상 제거.

---

## 3. 상세 비교 및 트레이드오프 (Pros & Cons)

| 비교 항목 | 전통적 물리 Materialized View | LionSQL Virtual Alias 방식 |
| :--- | :--- | :--- |
| **코드베이스 복잡도** | 수천 줄 (물리 스토리지, WAL, 락 스왑) | **약 30~50줄 (기존 View 핸들러 재사용)** |
| **회귀 버그 및 락 위험** | 매우 높음 (`CONCURRENTLY` 락 경합, 크래시) | **0% (새로운 상태 스토리지 없음, 락 0)** |
| **데이터 일관성** | `REFRESH` 전까지 지연된 과거 데이터 (Stale) | **언제나 100% 실시간 최신 데이터 (Fresh)** |
| **디스크 I/O 및 용량** | 대량 쓰기 증폭 및 디스크 낭비 발생 | **디스크 I/O 0, 추가 용량 0, VACUUM 0** |
| **조회 성능 (Read)** | $O(1)$ 정적 스냅샷 읽기 | 실시간 쿼리 실행 (대규모 집계 시 CPU 부하) |
| **보조 인덱스 지원** | `CREATE INDEX ON matview` 가능 | 원본 기본 테이블 인덱스에 의존 |

---

## 4. 구현 명세 (Implementation Specification)

### 4.1 수정 대상 파일
1. **`pkg/engine/sqlexec.go`**:
   - `CREATE MATERIALIZED VIEW`, `DROP MATERIALIZED VIEW`, `REFRESH MATERIALIZED VIEW` 최상위 DDL 라우팅 분기 추가.
2. **`pkg/engine/sqlexec_ddl.go`**:
   - `execCreateMaterializedView(cleaned, start)`: `MATERIALIZED` 키워드 제거 후 `execCreateView` 호출, 카탈로그에 `IsMaterialized = true` 마킹.
   - `execRefreshMaterializedView(cleaned, start)`: 대상 뷰 조회 후 `Success: true` 즉각 반환.
   - `execDropMaterializedView(cleaned, start)`: `execDropView`로 위임.
3. **`pkg/catalog/virtual/pg.go`**:
   - `generatePgClass`: 해당 뷰의 `relkind`를 `'m'`으로 반환하여 Metabase, DBeaver, Mattermost의 스키마 인스펙터 호환성 보장.

---

## 5. 단계별 실행 계획 및 완료 기준

1. **Phase 1: DDL 파싱 및 라우팅 (`v1.1.0`)**:
   - `CREATE MATERIALIZED VIEW [IF NOT EXISTS]`, `REFRESH MATERIALIZED VIEW`, `DROP MATERIALIZED VIEW` 기본 문법 지원.
2. **Phase 2: 회귀 테스트 스위트 작성**:
   - Mattermost의 실제 마이그레이션 쿼리(`CREATE MATERIALIZED VIEW IF NOT EXISTS poststats AS SELECT ...`) 실행 및 `REFRESH` 동작 검증 단위 테스트 추가.
3. **Phase 3: 엔터프라이즈 앱 연동 인증**:
   - Mattermost 부트스트랩을 재실행하여 통계 뷰 생성 및 초기 기동 100% 통과 확인.
