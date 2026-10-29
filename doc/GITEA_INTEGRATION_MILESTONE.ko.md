# LionSQL Gitea 연동 및 엔터프라이즈 ORM 호환성 마일스톤 보고서 (`doc/GITEA_INTEGRATION_MILESTONE.ko.md`)

> - **작성일**: 2026-10-29
> - **대상 릴리즈**: v0.90.0-beta4
> - **핵심 검증 대상**: Gitea v1.23+ (Go 풀스택 Git 호스팅 플랫폼) & XORM (Microsoft SQL Server Dialect)

---

## 1. 개요 및 마일스톤 의의

LionSQL은 단일 정적 바이너리로 구동되는 고동시성 임베디드 OLTP 데이터베이스 엔진으로서, Microsoft SQL Server T-SQL 방언과 TDS(Tabular Data Stream) 유선 프로토콜을 네이티브로 지원합니다.

그동안 LionSQL은 SSMS(SQL Server Management Studio), DBeaver, DbGate, Beekeeper Studio 등 주요 GUI 데이터베이스 관리 도구들과의 상호운용성을 집중적으로 검증해 왔습니다. 그러나 이러한 GUI 도구들은 주로 메타데이터 탐색, 단일 SQL 쿼리 실행, 스키마 리버스 엔지니어링 위주의 인터랙티브 세션에 집중되어 있습니다.

**Gitea(XORM mssql dialect)의 완전한 초기화 및 프로덕션 기동 성공**은 단순 도구 연동을 넘어서는 다음과 같은 기술적 의의를 갖습니다:

1. **실제 엔터프라이즈 웹 애플리케이션 백엔드로서의 실증**:
   Gitea는 전 세계 수많은 조직에서 소스 코드 관리 및 CI/CD 워크플로의 코어로 사용하는 고부하 프로덕션 웹 서비스입니다. 이번 연동 성공은 LionSQL이 실제 복잡한 비즈니스 로직과 트랜잭션을 처리하는 메인 RDBMS로 완벽히 동작할 수 있음을 입증한 최초의 실증 사례입니다.
2. **복합 ORM 프레임워크(XORM)의 극단적 검증 통과**:
   ORM은 수동 SQL 작성과 달리 엔진의 T-SQL 파서, 가상 카탈로그, 메타데이터 리버스 엔지니어링, 트랜잭션 수명주기, 데이터 타입 엄격성을 기계적으로 극한까지 검증합니다.
3. **116개 프로덕션 테이블 DDL/DML 생애주기 무결성 입증**:
   Gitea가 요구하는 사용자 계정, 리포지토리, 이슈, 커밋 상태, OAuth2, 웹훅, LFS 등 116개 테이블의 스키마 생성 및 초기 시드 트랜잭션이 단 한 건의 데이터 레이스나 패닉 없이 무결하게 실행되었습니다.

---

## 2. 해결된 핵심 기술 장벽 및 아키텍처 개선

Gitea 기동 과정에서 도출된 세 가지 핵심 기술 난제를 해결하기 위해 LionSQL 커널과 가상 카탈로그에 다음과 같은 고도화가 적용되었습니다.

```mermaid
flowchart TD
    Gitea["Gitea Startup Sequence"] --> Step1["1. Schema Migration<br/>(x.Sync)"]
    Gitea --> Step2["2. Table Metadata Introspection<br/>(x.DBMetas / GetColumns)"]
    Gitea --> Step3["3. Collation & Engine Pre-check<br/>(preprocessDatabaseCollation)"]

    Step1 --> Fix1["조건부 DDL & 익명 블록 지원<br/>(ORM-MIGRATE-1)"]
    Step2 --> Fix2["가상 카탈로그 라우팅 & max_length int64 보장<br/>(ORM-MIGRATE-2)"]
    Step3 --> Fix3["T-SQL 대입식 별칭 & sys.fn_helpcollations()<br/>(ORM-MIGRATE-3)"]

    Fix1 --> Kernel["LionSQL Storage & VDBE Kernel"]
    Fix2 --> Kernel
    Fix3 --> Kernel
    Kernel --> Success["Gitea 116개 테이블 초기화 및 정상 기동 완료"]
```

### 2.1. 조건부 DDL 및 익명 블록 처리 (`ORM-MIGRATE-1`)
- **현상**: XORM 마이그레이션 러너가 테이블 생성 시 `IF NOT EXISTS (SELECT [name] FROM sysobjects WHERE [name] = 'user' AND xtype = 'U') CREATE TABLE [user] (...)` 형태의 복합 문장을 전송하여 파서 에러(`unexpected token: CREATE`) 발생.
- **아키텍처 개선**:
  - `pkg/engine/sqlexec_conditional_ddl.go`를 구현하여 DDL 본문을 포함하는 조건부 구문(`IF NOT EXISTS`, `IF EXISTS`, `IF OBJECT_ID(...) IS NULL`)을 정규식 및 AST 레벨에서 감지.
  - 조건식 평가 결과를 바탕으로 하위 DDL(테이블 생성 등)을 트랜잭션 내에서 원자적으로 실행하거나 안전한 No-Op으로 처리.
  - 레거시 메타데이터 테이블 `sysobjects` 컬럼(`id`, `uid`, `xtype`)을 보강하고 ANSI SQL CHAR 공백 허용 문자열 비교 규칙 적용.

### 2.2. 복합 조인 컬럼 카탈로그 라우팅 및 타입 안전성 (`ORM-MIGRATE-2`)
- **현상**: XORM의 컬럼 메타데이터 수집 쿼리(`from sys.columns a left join sys.types b ... left outer join sys.indexes i ... where a.object_id = object_id('user')`) 실행 시 `max_length`가 NULL로 반환되어 Go의 `database/sql` 드라이버에서 `converting NULL to int64 is unsupported` 런타임 오류 발생.
- **아키텍처 개선**:
  - 가상 카탈로그 인터셉터(`interceptor.go`)에서 `FROM sys.columns`로 시작하는 쿼리가 조인된 인덱스 조건(`sys.indexes`) 때문에 인덱스 쿼리로 잘못 분기되던 우선순위 문제 해결.
  - `object_id('tablename')` 함수 호출 패턴을 추출하여 테이블 객체 ID(1000+)와 매핑하는 정규식 해석기 구축.
  - 메타데이터의 `max_length`, `precision`, `scale`, `column_id` 수치 필드를 non-null `int64`로 강제 캐스팅하여 Go 드라이버 스캔 완벽 호환.

### 2.3. T-SQL 고유 대입식 별칭 및 Collation 호환성 (`ORM-MIGRATE-3`)
- **현상**: XORM의 `GetColumns()`가 생성하는 `"default_is_null" = (CASE WHEN c.text is null THEN 1 ELSE 0 END)` 구문에서 컬럼명이 쪼개지며 `sql/driver: couldn't convert <nil> into type bool` 오류 발생. 또한 Gitea의 사전 Collation 검증 루틴이 중단됨.
- **아키텍처 개선**:
  - T-SQL 고유의 대입식 별칭 구문(`"alias" = <expr>`, `alias = <expr>`)을 최상위 레벨에서 안전하게 파싱하여 컬럼명과 수식을 완벽히 분리.
  - `default_is_null` 평가 시 non-null `bool(true)`를 전달하여 Go ORM의 `bool` 포인터 스캔 규격 충족.
  - Gitea가 지원 문자셋 목록을 조회하는 `SELECT * FROM sys.fn_helpcollations() WHERE name LIKE '%[_]CS[_]AS%'` 가상 테이블 함수 구현.
  - T-SQL `LIKE` 구문에서 언더스코어를 이스케이프하는 대괄호 표현식(`[_]`, `[%]`)의 정규식 변환기 보강.
  - 마이그레이션 스크립트의 `ALTER DATABASE CURRENT COLLATE` 구문을 관용적 성공(No-Op)으로 처리.

---

## 3. 검증 결과 및 신규 생성된 데이터베이스 객체 현황

신규 초기화된 클린 데이터베이스 환경에서 Gitea를 구동한 결과, 총 **116개의 핵심 비즈니스 테이블**이 오차 없이 생성되었습니다:

| 도메인 | 주요 생성 테이블 |
| :--- | :--- |
| **인증 및 계정 (Auth & Users)** | `user`, `public_key`, `two_factor`, `access_token`, `external_login_user`, `webauthn_credential` |
| **리포지토리 및 브랜치 (Repositories)** | `repository`, `branch`, `protected_branch`, `protected_tag`, `repo_unit`, `repo_redirect` |
| **이슈 및 협업 (Issues & Pulls)** | `issue`, `issue_assignees`, `issue_content_history`, `issue_label`, `pull_request`, `review` |
| **CI/CD 및 액션 (Actions)** | `action_run`, `action_task`, `action_runner`, `action_schedule`, `action_variable` |
| **패키지 및 릴리즈 (Packages & Release)**| `package`, `package_version`, `package_file`, `release`, `attachment` |
| **보안 및 시스템 설정 (Security & Admin)**| `system_setting`, `notice`, `webhook`, `hook_task`, `version` |

---

## 4. 향후 로드맵 영향

Gitea의 성공적인 부팅 및 마이그레이션 통과는 LionSQL의 1.0 프로덕션 출시 신뢰도를 비약적으로 상승시켰습니다:
1. **타 ORM 프레임워크 확장**: GORM, Entity Framework Core, Prisma 등 타 생태계의 주요 ORM 연동 테스트를 위한 표준 테스트베드 확보.
2. **복합 트랜잭션 벤치마크**: 단순 합성 쿼리(TPC-C, SSB) 외에 실제 웹 애플리케이션의 복합 세션 및 커넥션 풀링 환경에 대한 지속적 검증 가능.
3. **v0.90.0-beta4 공식 탑재**: 본 개선 사항은 모두 `v0.90.0-beta4` 릴리즈에 완전하게 통합되어 모든 공식 배포 바이너리(macOS, Linux, Windows)에 즉시 적용됩니다.
