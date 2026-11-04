# [설계 계획서] 빈 괄호 IN () 문법 허용에 대한 아키텍처 분석 및 결정 계획서 (`notes/EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md`)

> - **최종 갱신일**: 2026-11-03
> - **최초 작성일**: 2026-11-03
> - **상태**: 분석 완료 / 장기 백로그 검토 (Post-1.0 대기)
> - **엔지니어링 철학**: [RULE:RadicalKISS] (불필요한 설정 플래그 배제 원칙), [RULE:PlanPersistence]
> - **주요 주제**: `WHERE col IN ()` 빈 괄호 구문 허용 여부, 표준 엄격주의 vs 개발자 편의성(Ergonomics), 옵션 플래그 배제 원칙

---

## 1. 개요 및 배경

`SELECT * FROM table WHERE col IN ()`과 같이 `IN` 또는 `NOT IN` 연산자 우변의 괄호 안에 원소가 0개인 **빈 괄호(Empty RHS List)** 구문의 평가는 데이터베이스 업계에서 15년 이상 지속된 대표적인 문법 철학 논쟁거리입니다.

- **표준 RDBMS (PostgreSQL, MS SQL Server, Oracle)**:
  - ANSI/ISO SQL 표준(SQL92, SQL:2016) 엄수를 이유로 문법 에러(Syntax Error)를 반환.
- **모던/편의성 RDBMS (MySQL 8.0.19+, SQLite)**:
  - 수학적 공집합 시맨틱스를 채택하여 `IN ()`은 항상 `FALSE`, `NOT IN ()`은 항상 `TRUE`로 관용 처리.

LionSQL의 1.0 LTS 안정성 및 Post-1.0 확장 관점에서 본 기능의 기술적 득실, 오픈소스 커뮤니티의 실제 요구 역사, 그리고 LionSQL의 핵심 설계 원칙을 정리합니다.

---

## 2. PostgreSQL 및 MS SQL Server 커뮤니티의 실제 요구 역사

### 2.1 PostgreSQL 진영 (`pgsql-hackers` 및 ORM 생태계)
- **개발자들의 고통**:
  - Hibernate, JPA, Prisma, Sequelize, Go `sqlx` 등에서 `ids = []` 빈 배열을 매핑할 때마다 `WHERE id IN ()`이 생성되어 백엔드 서버가 500 문법 오류로 중단되는 문제가 지속적으로 제기됨.
  - 이를 회피하기 위해 개발자들은 `if (ids.isEmpty())` 애플리케이션 방어 코드를 강제 작성하거나 `WHERE id IN (NULL)` 같은 비표준 트릭을 써야 했음.
- **코어 팀의 입장**:
  - `pgsql-hackers`에서 수차례 논의되었으나, "SQL 표준 BNF에 `<in_value_list>`는 1개 이상의 식이어야 한다"는 이유로 전면 기각.
  - 대신 Postgres 전용 문법인 `= ANY(ARRAY[]::int[])` 사용을 권장(단, 타 DB와의 호환성 상실).

### 2.2 MS SQL Server (T-SQL) 진영 (Microsoft Feedback)
- **.NET LINQ 및 EF Core의 고통**:
  - C# LINQ `db.Users.Where(u => ids.Contains(u.Id))` 실행 시 `ids`가 비어있으면 Entity Framework Core 팀이 SQL 생성을 포기하고 억지로 `WHERE 1 = 0`으로 SQL을 재작성하는 복잡한 트릭을 구현해야 했음.
  - Microsoft Feedback 포털에서 "Compatibility Level 옵션으로라도 빈 괄호를 허용해 달라"는 요청이 수년 동안 다수의 추천(Upvote)을 받았으나, 2022 최신 버전까지도 하위 호환성을 이유로 Error 102(문법 오류)를 유지 중.

### 2.3 실용주의를 택한 진영
- **SQLite**: 오래전부터 `WHERE col IN ()`을 공집합(`0/FALSE`)으로 지원 (SLT `evidence/in1.test`의 핵심 테스트 케이스).
- **MySQL 8.0.19 (2020년 전격 도입)**:
  - 과거 문법 에러를 내던 동작을 버리고, 현대 웹 애플리케이션 프레임워크와의 개발 편의성을 높이기 위해 `WHERE col IN ()`을 정식 지원(`FALSE` 평가)하도록 전격 개정.

---

## 3. 핵심 아키텍처 철학: "옵션(플래그)으로 제공하는 것은 피한다"

일각에서는 타협안으로 `--enable-empty-in-predicate=true` 같은 설정 플래그를 제안하기도 하지만, LionSQL의 핵심 엔지니어링 원칙 관점에서 이는 가장 지양해야 할 안티패턴입니다.

> **[RULE:RadicalKISS] 철학**:
> "어떤 기능을 제공할지 말지(All-or-Nothing)를 결단해야지, 어설프게 옵션 플래그로 책임을 사용자에게 떠넘기면 코드베이스에 if/else 분기만 증식하고 테스트 매트릭스가 2배로 폭발한다."

- 옵션으로 제공할 경우의 문제점:
  1. **설정 복잡도 증가**: 사용자가 환경 설정(`lionsql.json`, CLI 플래그)을 신경 써야 함.
  2. **테스트 매트릭스 2배 폭발**: 플래그가 `true`일 때와 `false`일 때 모든 쿼리 파서/플래너 테스트를 이중으로 검증해야 함.
  3. **드롭인 호환성 혼선**: 어떤 인스턴스에서는 되고 어떤 인스턴스에서는 안 되는 비결정적 동작 초래.

따라서 LionSQL은 **"엔진 자체에서 기본적으로 지원할 것인가, 아니면 엄격한 문법 에러로 유지할 것인가"** 둘 중 하나만을 명확히 선택해야 합니다.

---

## 4. 장단점 비교 매트릭스

| 비교 기준 | **방안 1. 전격 수용 (SQLite/MySQL 8 방식)** | **방안 2. 엄격 거부 (현행 유지, PG/MSSQL 방식)** |
| :--- | :--- | :--- |
| **핵심 가치** | **개발자 편의성(Developer Ergonomics)** 극대화 | **엄격한 SQL 표준 준수 및 드롭인 정합성** |
| **빈 배열 필터 시** | 에러 없이 **0건(빈 결과)** 안전 반환 | **Syntax Error** 반환 (클라이언트 방어 코드 필수) |
| **PostgreSQL 호환성** | 불일치 (Postgres는 에러 냄) | **100% 일치** |
| **T-SQL (MSSQL) 호환성** | 불일치 (MSSQL은 에러 냄) | **100% 일치** |
| **SQLLogicTest 영향** | `evidence/in1.test` 즉시 통과 가능 | `evidence/in1.test` 실패 (격리 유지 필요) |
| **구현 비용** | 10~20줄 내외 (`pkg/sqlutil`, `pkg/proc`) | 0 (현행 유지) |

---

## 5. 결론 및 향후 검토 방향

1. **1.0 LTS 출시 전 (현재)**:
   - **현행 유지(방안 2)**: 1.0의 최우선 목표는 PostgreSQL Wire Protocol과 Microsoft SQL Server T-SQL의 완벽한 1:1 드롭인 호환성입니다. 레퍼런스 DBMS와 완전히 동일하게 Syntax Error를 유지합니다.
2. **Post-1.0 이후 재검토 기준**:
   - DuckDB 스타일 편의 문법(`FRIENDLY-SQL`, `SELECT * EXCLUDE` 등)을 검토하는 v1.1.0 시점에, 개발자 손끝 편의 문법의 일환으로서 "방안 1(전격 수용)"을 단일 정책으로 전면 채택할지 최종 판단합니다.
   - 단, 타협안 형태의 **ad-hoc 설정 플래그는 절대 도입하지 않는다**는 원칙을 확고히 고수합니다.
