# LionSQL PostgreSQL 호환성 강화 아키텍처 및 구현 계획서 (`POSTGRES_COMPATIBILITY_ENHANCEMENT_PLAN.ko.md`)

> **문서 상태**: Approved for Implementation (Radical KISS Downsizing 반영)  
> **최초 작성일**: 2026-11-18  
> **최종 수정일**: 2026-11-18  
> **대상 마일스톤**: `v1.1.0` ~ `v1.2.0`  
> **아키텍처 대원칙**:  
> 1. `[Invariant 1]`: 2-Tier Parser (Pratt Fast-Path + ANTLR4 Fallback) 유지  
> 2. `[Invariant 4]`: 단일 정적 바이너리 (외부 사이드카 데몬 일체 불허)  
> 3. `[Invariant 7]`: 프로덕션 SQL 파싱 경로 Zero-Regex 원칙 준수  
> 4. `[Invariant 12]`: 맥락 무시 `strings.*` 금지 및 `sqlutil` 문맥 인식 토크나이저 우선  
> 5. `[Invariant 17]`: **단일 통합 SQL 문법 & 프로토콜 무분기 불변식 (`[RULE:ZeroProtocolBranch]`)**:
>    - 파서, 렉서, 컴파일러, VDBE 실행기 전반에서 `if cs.IsPostgres()` 또는 `if cs.IsTDS()` 분기 코드를 작성하는 것을 영구 금지함.
>    - 모든 SQL 문법은 TDS(:1433), PGWire(:5433), Web Studio(:8090) 어디서나 100% 동일하게 해석되고 실행되는 **단일 통합 슈퍼셋(Unified Superset)**으로 동작해야 함.
>    - 방언 간 문법적 차이는 wire 분기가 아닌 결정론적 문맥 구문 분석(Deterministic Syntactic Disambiguation)으로 해결함.
> 6. **Radical KISS & 과도한 비표준 기능 Spec-Out (`[RULE:RadicalKISS]`)**:
>    - 문법 파싱 자체보다 무거운 엔진 내부 구현을 요구하는 비표준 기능(다차원 배열 타입 시스템, DISTINCT ON 등)은 과감히 Spec-Out함.
>    - 절차형 저장 프로시저 엔진은 초고속 T-SQL VDBE 바이트코드 VM 단일 체제를 유지함.

---

## 1. 개요 및 배경

LionSQL은 Microsoft SQL Server TDS(:1433)와 PostgreSQL Wire Protocol v3(:5433)를 기본 내장하여 단일 바이너리로 서비스하는 고성능 임베디드 OLTP 엔진이다.  
PostgreSQL 생태계의 다양한 도구와 마이그레이션 수요를 수용하되, 복잡한 엔진 내부 구현을 유발하는 비표준 기능들은 철저히 배제(Spec-Out)하고, 현대적인 애플리케이션 개발에서 필수적인 **와이어 프로토콜, 세션 제어, 시퀀스, 핵심 드라이버 슈가(Sugar), 카탈로그 뷰** 중심으로 호환성을 고도화한다.

---

## 2. 아키텍처 범위 및 Spec-In / Spec-Out 명세

### 2.1. Spec-Out (영구 제외 항목)
1. **PL/pgSQL 절차형 언어 해석기**:
   - `CREATE OR REPLACE FUNCTION ... RETURNS ... AS $$ DECLARE ... BEGIN ... END; $$ LANGUAGE plpgsql;`
   - 절차형 비즈니스 로직은 이미 세계 최고 수준의 서브마이크로초 성능을 내는 T-SQL VDBE 가상 머신(`pkg/proc/runtime`)이 전담함.
2. **분산 확장 프로토콜 및 복제 슬롯 데몬**:
   - 물리적 스트리밍 복제 슬롯, 논리적 디코딩 플러그인(`test_decoding`, `wal2json`) 등은 LionSQL의 경량 1-Writer Multi-Reader WAL 복제 체제(`Invariant 5`)로 충분하므로 제외.
3. **다차원 배열 타입 시스템 전체 (`FEAT-PG-ARRAY-OPERATORS-AND-UNNEST-1` Spec-Out)**:
   - PostgreSQL의 `int[]`, `text[]`, 다차원 차원 메타데이터, 1-based 인덱싱/슬라이싱(`arr[1:3]`), 배열 포함 연산자(`@>`, `<@`, `&&`), `UNNEST()` 테이블 함수 등 비관계형 배열 전용 타입 시스템 전체.
   - **아키텍처 사유**: OLTP RDBMS 표준을 벗어난 독자적 타입 시스템으로 카탈로그, V2 튜플 바이너리 코덱, VDBE 레지스터 전체의 비대화 및 복잡도를 초래함 (`[RULE:RadicalKISS]`).
   - **경량 대안**: ORM/드라이버가 IN 절 파라미터 바인딩으로 넘기는 `WHERE id = ANY($1)` 형태만 단일 통합 SQL 수준에서 `WHERE id IN (...)`으로 전처리(Desugaring)하여 실용적 호환성만 보장함.
4. **`SELECT DISTINCT ON (...)` 전용 구문 (`FEAT-PG-DISTINCT-ON-1` Spec-Out)**:
   - **아키텍처 사유**: 비표준 고유 문법이며, 주요 ORM(Prisma, GORM, TypeORM, Hibernate)이 자동 생성하지 않음.
   - **표준 대안**: 표준 SQL인 `ROW_NUMBER() OVER (PARTITION BY ... ORDER BY ...) = 1` 윈도우 함수를 통해 100% 동일하게 해결 가능하므로 전용 플래너/실행기 파이프라인 신설을 영구 배제함.
5. **복잡한 JSONB 연산자 및 POSIX 기호 연산자/FILTER 절 축소 (`Spec-Out / Downsizing`)**:
   - POSIX 정규식 기호 연산자(`~`, `!~`, `~*`, `!~*`): 제외. 표준 함수 `REGEXP_LIKE()` 권장.
   - JSONB 복잡 경로/존재 연산자(`#>`, `#>>`, `?`, `?|`, `?&`): 제외. 가벼운 인체공학적 구문인 `->` (JSON 객체) 및 `->>` (텍스트)만 기존 `JSON_QUERY` / `JSON_VALUE`로 1:1 매핑 지원.
   - `AGG(...) FILTER (WHERE ...)`: 제외. 표준 `SUM(CASE WHEN ... THEN ... END)` 권장.

### 2.2. Spec-In (집중 구현 대상 4대 카테고리)
```
+-----------------------------------------------------------------------------------------+
|                              LionSQL PostgreSQL Parity Matrix                            |
+-----------------------------------------------------------------------------------------+
| 1. SQL 문법/연산자  | :: 캐스트 전역화, ILIKE / NOT ILIKE, ANY($1) IN-절 전처리, ->, ->>      |
| 2. 시퀀스 & Identity | CREATE/ALTER/DROP SEQUENCE, nextval(), currval(), GENERATED AS IDENTITY  |
| 3. 세션 & 풀러 제어  | DISCARD ALL, SET/SHOW search_path/timezone, 격리수준/READ ONLY 구문      |
| 4. 가상 카탈로그     | pg_stat_activity, pg_settings, format_type, pg_get_expr, 툴킷 E2E 검증  |
+-----------------------------------------------------------------------------------------+
```

---

## 3. 단계별 상세 구현 계획 (4개 Phase)

### Phase 1: Core SQL Grammar & Dialect Ergonomics (문법 및 연산자 경량 슈가)
- **목표**: ORM 및 드라이버에서 가장 빈번하게 발생하는 구문을 파서/실행기에서 무분기 단일 문법으로 완전 지원.
- **태스크 목록**:
  1. `FEAT-PG-DOUBLE-COLON-CAST-ALL-1`:
     - **설명**: `expr::type` 캐스팅 연산자를 SELECT 절뿐만 아니라 WHERE 조건식, INSERT VALUES, UPDATE SET, JOIN ON, 함수 인자 등 전 영역에서 AST 레벨로 일관되게 해석 (`sqlutil` 및 `proc.Lexer` 연동).
     - **적용 예시**: `SELECT '123'::int4, now()::date WHERE status = $1::text;`
  2. `FEAT-PG-ILIKE-AND-ANY-DESUGAR-1`:
     - **설명**: 대소문자 무시 패턴 매칭 `ILIKE`, `NOT ILIKE` 지원 및 드라이버 파라미터 바인딩용 `ANY($1)` ➔ `IN (...)` 전처리 지원.
     - **적용 예시**: `SELECT * FROM users WHERE email ILIKE '%@GMAIL.COM' AND id = ANY($1);`
  3. `FEAT-PG-JSON-ARROW-OPS-1`:
     - **설명**: 가벼운 JSON 접근 연산자:
       - `->`: `JSON_QUERY(col, '$.key')`로 1:1 매핑 (JSON 객체/배열 반환)
       - `->>`: `JSON_VALUE(col, '$.key')`로 1:1 매핑 (텍스트 반환)
     - **함수**: `json_build_object()`, `to_json()`.

### Phase 2: Standalone Sequence Engine & Identity Parity (시퀀스 및 키 채번)
- **목표**: 일련번호 채번 및 분산 키 생성을 위한 독립 시퀀스 객체 완벽 지원.
- **태스크 목록**:
  1. `FEAT-PG-SEQUENCE-OBJECTS-1`:
     - **설명**: `CREATE SEQUENCE [schema.]name [START WITH n] [INCREMENT BY n] [MINVALUE n] [MAXVALUE n] [CYCLE]`, `ALTER SEQUENCE`, `DROP SEQUENCE` DDL 파싱 및 카탈로그 영속화.
     - **인메모리 원자적 카운터**: `sync/atomic` 기반으로 초당 500,000+ 번호 발급 보장.
  2. `FEAT-PG-SEQUENCE-FUNCS-1`:
     - **설명**: `nextval('seq')`, `currval('seq')`, `setval('seq', val, is_called)` 내장 함수 구현.
     - **연동**: `SERIAL` 및 `BIGSERIAL` 정의 시 기본값으로 `nextval('table_col_seq')`가 자동 부여되도록 일원화.
  3. `FEAT-PG-IDENTITY-COLUMN-PARITY-1`:
     - **설명**: SQL 표준 및 PostgreSQL 10+ 호환 `id INT GENERATED ALWAYS AS IDENTITY` 및 `GENERATED BY DEFAULT AS IDENTITY` DDL 파싱 및 시퀀스 바인딩.

### Phase 3: Session Control, Connection Pooler & Wire Protocol Parity (세션 및 풀러 제어)
- **목표**: HikariCP, PgBouncer, SQLAlchemy, Go `pgx` 연결 풀러의 반납/재사용 시그널 완벽 수용.
- **태스크 목록**:
  1. `FEAT-PG-DISCARD-COMMANDS-1`:
     - **설명**: `DISCARD ALL` (임시 테이블 정리, 세션 변수 초기화, 준비된 문장 캐시 해제, 커서 닫기, search_path 리셋), `DISCARD PLANS`, `DISCARD SEQUENCES`, `DISCARD TEMP` 구현.
  2. `FEAT-PG-SESSION-SETTINGS-PARITY-1`:
     - **설명**: 세션 설정 명령 및 함수:
       - `SET search_path = public, sales;` / `SHOW search_path;`
       - `SET client_encoding = 'UTF8';` / `SHOW client_encoding;`
       - `SET timezone = 'UTC';` / `SHOW timezone;`
       - `current_setting('setting_name')`, `set_config('setting_name', 'new_val', is_local)`.
  3. `FEAT-PG-TRANSACTION-ISOLATION-SYNTAX-1`:
     - **설명**: `SET TRANSACTION ISOLATION LEVEL READ COMMITTED / REPEATABLE READ / SERIALIZABLE`, `SET TRANSACTION READ ONLY / READ WRITE` 지원.

### Phase 4: Virtual Catalog & Client Tools Introspection (시스템 뷰 및 툴킷 검증)
- **목표**: DBeaver, DataGrip, Metabase, Grafana, psql 표준 툴킷이 무오류로 메타데이터를 검색하고 상호작용하도록 보장.
- **태스크 목록**:
  1. `FEAT-PG-STAT-ACTIVITY-AND-SETTINGS-1`:
     - **설명**: `pg_stat_activity` 뷰(현재 연결된 세션, 실행 중인 쿼리, 상태 `idle/active`, 트랜잭션 시작 시각) 및 `pg_settings` 뷰 제공.
  2. `FEAT-PG-CATALOG-FUNCTIONS-1`:
     - **설명**: 카탈로그 메타데이터 도우미 함수:
       - `format_type(type_oid, typemod)`
       - `pg_get_expr(adbin, adrelid)`
       - `pg_get_constraintdef(conoid)`
       - `pg_get_indexdef(indexoid)`
       - `pg_table_is_visible(oid)`
       - `has_table_privilege(user, table, priv)`
  3. `FEAT-PG-E2E-TOOLING-VALIDATION-1`:
     - **설명**: psql 대화형 메타 커맨드(`\dt`, `\d`, `\l`, `\dn`, `\df`), Prisma 마이그레이션 엔진, GORM PostgreSQL 드라이버, DBeaver PGWire 연결을 포괄하는 E2E 통합 테스트 스위트 구축.

---

## 4. 백로그 태스크 매핑 (Sprint DB & TASKS.md Dual-Sync)

| Task ID | Domain | Prio | Milestone | Status | Description |
|---|---|---|---|---|---|
| `FEAT-PG-DOUBLE-COLON-CAST-ALL-1` | Engine | P1 | `v1.1.0` | TODO | 범용 `expr::type` 캐스팅 전면 지원 (SELECT, WHERE, VALUES, JOIN, FUNC) |
| `FEAT-PG-ILIKE-AND-ANY-DESUGAR-1` | Engine | P1 | `v1.1.0` | TODO | `ILIKE` / `NOT ILIKE` 및 `ANY($1)` IN-절 전처리 지원 |
| `FEAT-PG-JSON-ARROW-OPS-1` | Engine | P2 | `v1.1.0` | TODO | 경량 `->` (JSON_QUERY), `->>` (JSON_VALUE) 화살표 연산자 슈가 지원 |
| `FEAT-PG-SEQUENCE-OBJECTS-1` | Catalog | P1 | `v1.1.0` | TODO | `CREATE/ALTER/DROP SEQUENCE` DDL 및 원자적 카운터 엔진 |
| `FEAT-PG-SEQUENCE-FUNCS-1` | Engine | P1 | `v1.1.0` | TODO | `nextval()`, `currval()`, `setval()` 시퀀스 함수 및 SERIAL 연동 |
| `FEAT-PG-IDENTITY-COLUMN-PARITY-1` | Catalog | P1 | `v1.1.0` | TODO | `GENERATED ALWAYS / BY DEFAULT AS IDENTITY` 지원 |
| `FEAT-PG-DISCARD-COMMANDS-1` | Wire | P1 | `v1.1.0` | TODO | 커넥션 풀러용 `DISCARD ALL / PLANS / SEQUENCES / TEMP` 지원 |
| `FEAT-PG-SESSION-SETTINGS-PARITY-1` | Wire | P1 | `v1.1.0` | TODO | `SET/SHOW search_path, timezone, current_setting(), set_config()` |
| `FEAT-PG-TRANSACTION-ISOLATION-SYNTAX-1` | Wire | P1 | `v1.1.0` | TODO | `SET TRANSACTION ISOLATION LEVEL ...` 지원 |
| `FEAT-PG-STAT-ACTIVITY-AND-SETTINGS-1` | Catalog | P2 | `v1.2.0` | TODO | `pg_stat_activity`, `pg_settings` 가상 뷰 확충 |
| `FEAT-PG-CATALOG-FUNCTIONS-1` | Catalog | P2 | `v1.2.0` | TODO | `format_type()`, `pg_get_expr()`, `pg_get_constraintdef()` 등 메타 함수 |
| `FEAT-PG-E2E-TOOLING-VALIDATION-1` | Test | P1 | `v1.2.0` | TODO | psql, Prisma, GORM, DBeaver PGWire E2E 통합 테스트 스위트 구축 |
| `FEAT-PG-ARRAY-OPERATORS-AND-UNNEST-1` | Engine | P3 | - | SPEC-OUT | 다차원 배열 타입 시스템 및 UNNEST (오버엔지니어링으로 영구 제외) |
| `FEAT-PG-DISTINCT-ON-1` | Engine | P3 | - | SPEC-OUT | `SELECT DISTINCT ON` (표준 ROW_NUMBER()로 대체, 영구 제외) |
| `FEAT-PG-AGG-FILTER-CLAUSE-1` | Engine | P3 | - | SPEC-OUT | `AGG(...) FILTER (WHERE ...)` (표준 CASE-WHEN 대체, 영구 제외) |

---

## 5. 검증 파이프라인 정책 (`[RULE:PipelineVerification]`)

모든 구현 태스크는 3단계 파이프라인을 엄격히 통과해야 함:
1. **Build**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Test**: `go test -v -race ./pkg/wire/... ./pkg/engine/... ./pkg/catalog/...` (데이터 레이스 0건, 100% 통과)
3. **Commit**: Conventional Commits 포맷 (`feat(engine,pg): ...`, `fix(wire,pg): ...`), 장식성 이모지 완전 금지.
