# LionSQL Gitea 연동 및 엔터프라이즈 ORM 호환성 마일스톤 보고서 (`notes/GITEA_INTEGRATION_MILESTONE.ko.md`)

> - **작성일**: 2026-10-29 (최종 개정일: 2026-10-30)
> - **대상 릴리즈**: v0.90.0-beta4
> - **핵심 검증 대상**: Gitea v1.23+ (Go 풀스택 Git 호스팅 플랫폼) & XORM (Microsoft SQL Server TDS :1433 및 PostgreSQL Wire Protocol :5433)
> - **연계 핵심 문서**:
>   - **기술 포스트모템**: [`notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md) (엔터프라이즈 ORM SQL 호환성 분석 및 테스트 사각지대 심층 회고)
>   - **공식 실전 가이드**: [`doc/GITEA_INTEGRATION_GUIDE.ko.md`](../doc/GITEA_INTEGRATION_GUIDE.ko.md) (LionSQL 기반 Gitea 듀얼 프로토콜 구축 및 프로덕션 운영 매뉴얼)

---

## 1. 개요 및 마일스톤 의의

LionSQL은 단일 정적 바이너리로 구동되는 고동시성 임베디드 OLTP 데이터베이스 엔진으로서, Microsoft SQL Server T-SQL 방언/TDS 유선 프로토콜(:1433)과 PostgreSQL 표준 PGWire v3 프로토콜(:5433)을 네이티브로 듀얼 지원합니다.

그동안 LionSQL은 SSMS(SQL Server Management Studio), DBeaver, DbGate, Beekeeper Studio 등 주요 GUI 데이터베이스 관리 도구들과의 상호운용성을 집중적으로 검증해 왔습니다. 그러나 이러한 GUI 도구들은 주로 메타데이터 탐색, 단일 SQL 쿼리 실행, 스키마 리버스 엔지니어링 위주의 인터랙티브 세션에 집중되어 있습니다.

**Gitea의 완전한 초기화, 듀얼 프로토콜 지원, 그리고 실전 풀 라이프사이클 E2E 검증 통과**는 단순 도구 연동을 넘어서는 다음과 같은 결정적 엔지니어링 의의를 갖습니다:

1. **실제 엔터프라이즈 웹 애플리케이션 백엔드로서의 실증**:
   Gitea는 전 세계 수많은 조직에서 소스 코드 관리 및 CI/CD 워크플로의 코어로 사용하는 고부하 프로덕션 웹 서비스입니다. 이번 연동 성공은 LionSQL이 실제 복잡한 비즈니스 로직과 트랜잭션을 처리하는 메인 RDBMS로 완벽히 동작할 수 있음을 입증한 최초의 실증 사례입니다.
2. **복합 ORM 프레임워크(XORM)의 극단적 검증 통과**:
   ORM은 수동 SQL 작성과 달리 엔진의 SQL 파서, 가상 카탈로그, 메타데이터 리버스 엔지니어링, 트랜잭션 수명주기, 데이터 타입 엄격성을 기계적으로 극한까지 검증합니다. (상세 분석: [`notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md) 참조)
3. **116개 프로덕션 테이블 DDL/DML 생애주기 무결성 입증**:
   Gitea가 요구하는 사용자 계정, 리포지토리, 이슈, 커밋 상태, OAuth2, 웹훅, LFS 등 116개 테이블의 스키마 생성 및 초기 시드 트랜잭션이 단 한 건의 데이터 레이스나 패닉 없이 무결하게 실행되었습니다.
4. **TDS(:1433) 및 PGWire(:5433) 듀얼 프로토콜 전방위 검증**:
   동일한 비즈니스 로직에 대해 MSSQL 드라이버와 PostgreSQL 드라이버 양쪽 모두에서 단 한 줄의 애플리케이션 코드 수정 없이 100% 동작함을 실증했습니다.

---

## 2. 해결된 핵심 기술 장벽 및 아키텍처 개선 (기동 및 스키마 단계)

Gitea 기동 과정에서 도출된 초기 기술 난제를 해결하기 위해 LionSQL 커널과 가상 카탈로그에 다음과 같은 고도화가 적용되었습니다.

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
  - `pkg/engine/sqlexec_conditional_ddl.go`를 구현하여 DDL 본문을 포함하는 조건부 구문(`IF NOT EXISTS`, `IF EXISTS`, `IF OBJECT_ID(...) IS NULL`)을 감지.
  - 조건식 평가 결과를 바탕으로 하위 DDL(테이블 생성 등)을 트랜잭션 내에서 원자적으로 실행하거나 안전한 No-Op으로 처리.
  - 레거시 메타데이터 테이블 `sysobjects` 컬럼(`id`, `uid`, `xtype`)을 보강하고 ANSI SQL CHAR 공백 허용 문자열 비교 규칙 적용.

### 2.2. 복합 조인 컬럼 카탈로그 라우팅 및 타입 안전성 (`ORM-MIGRATE-2`)
- **현상**: XORM의 컬럼 메타데이터 수집 쿼리 실행 시 `max_length`가 NULL로 반환되어 Go의 `database/sql` 드라이버에서 `converting NULL to int64 is unsupported` 런타임 오류 발생.
- **아키텍처 개선**:
  - 가상 카탈로그 인터셉터(`interceptor.go`)에서 `FROM sys.columns`로 시작하는 쿼리가 조인된 인덱스 조건(`sys.indexes`) 때문에 인덱스 쿼리로 잘못 분기되던 우선순위 문제 해결.
  - `object_id('tablename')` 함수 호출 패턴을 추출하여 테이블 객체 ID(1000+)와 매핑하는 정규식 해석기 구축.
  - 메타데이터의 `max_length`, `precision`, `scale`, `column_id` 수치 필드를 non-null `int64`로 강제 캐스팅하여 Go 드라이버 스캔 완벽 호환.

### 2.3. T-SQL 고유 대입식 별칭 및 Collation 호환성 (`ORM-MIGRATE-3`)
- **현상**: XORM의 `GetColumns()`가 생성하는 `"default_is_null" = (CASE WHEN c.text is null THEN 1 ELSE 0 END)` 구문에서 컬럼명이 쪼개지며 `sql/driver: couldn't convert <nil> into type bool` 오류 발생.
- **아키텍처 개선**:
  - T-SQL 고유의 대입식 별칭 구문(`"alias" = <expr>`, `alias = <expr>`)을 최상위 레벨에서 안전하게 파싱하여 컬럼명과 수식을 완벽히 분리.
  - `default_is_null` 평가 시 non-null `bool(true)`를 전달하여 Go ORM의 `bool` 포인터 스캔 규격 충족.
  - Gitea가 지원 문자셋 목록을 조회하는 `SELECT * FROM sys.fn_helpcollations() WHERE name LIKE '%[_]CS[_]AS%'` 가상 테이블 함수 구현.
  - 마이그레이션 스크립트의 `ALTER DATABASE CURRENT COLLATE` 구문을 관용적 성공(No-Op)으로 처리.

---

## 3. 실전 DML 및 런타임 호환성 돌파 (`GITEA-MOCK-SEED-1`)

스키마가 생성된 이후, 실제 사용자가 로그인하고 Git 저장소 및 이슈를 조작하는 런타임 환경에서 발생하는 복합 DML 구문 장벽을 전면 격파했습니다. (상세 내용: [`notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md) Section 3 참조)

1. **`UPDATE ... FROM <source>` 및 `DELETE FROM` 테이블 한정 구문**:
   - MSSQL 고유의 `UPDATE [user] SET [num_repos] = [num_repos] + 1 FROM [user] INNER JOIN ...` 구문 지원.
   - 단일 테이블 타깃 업데이트와 조인 소스 테이블 분리 실행기 보강.
2. **`AS` 뒤 키워드 및 집계함수 별칭 허용**:
   - `SELECT count(*) AS count`와 같이 집계 함수명 또는 SQL 키워드가 별칭으로 사용될 때 파서가 키워드로 오인하지 않고 식별자(Identifier)로 처리하도록 Lexer/Parser 문법 확장.
3. **테이블 한정 와일드카드(`table.*`) 프로젝션**:
   - 다중 테이블 조인 시 컬럼 충돌을 방지하기 위해 `SELECT u.*, r.name FROM [user] u JOIN repository r ...` 형태로 특정 테이블의 전체 컬럼만 한정하여 프로젝션하는 기능 완성.
4. **복합 인덱스 범위 검색 괄호 불일치 함정 해결**:
   - 다중 컬럼 복합 인덱스 검색 시 정규화기(`bounds normalizer`)가 괄호 쌍을 잘못 닫아 인덱스 스캔이 유실되던 결함을 수정하여 초고속 인덱스 룩업 보장.

---

## 4. 듀얼 프로토콜 완성: PostgreSQL Wire Protocol (:5433) 연동 (`GITEA-PGWIRE-1`)

Gitea를 `db_type = postgres`로 구동하는 엔터프라이즈 환경을 위해, PostgreSQL 유선 프로토콜 계층에 필요한 필수 RDBMS 기능을 완벽히 구현했습니다. (상세 분석: [`notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md) Section 5 참조)

```mermaid
sequenceDiagram
    participant Gitea as Gitea (PGWire Client)
    participant PGServer as LionSQL PGWire (:5433)
    participant Scanner as Single-Pass Param Scanner
    participant Engine as LionSQL Kernel

    Gitea->>PGServer: INSERT INTO user (passwd, ...) VALUES ($1, ...) RETURNING id
    PGServer->>Scanner: Quote-Aware Param Substitution ($1 -> 'pbkdf2$50000$50...')
    Note over Scanner: 문자열 내부의 $50000은 치환하지 않고 보호!
    Scanner-->>PGServer: Safe SQL Statement
    PGServer->>Engine: Execute Insert & Return Projected ID
    Engine-->>PGServer: Output Tuple (id = 1)
    PGServer-->>Gitea: RowDescription + DataRow + CommandComplete
```

1. **`BIGSERIAL`, `SERIAL`, `SMALLSERIAL` DDL 파싱 지원**:
   - XORM의 PostgreSQL 마이그레이션이 생성하는 시퀀스 컬럼을 Pratt 파서, DDL 엔진, ANTLR4에서 네이티브 정수형 및 자동 증가(Identity) 속성으로 자동 매핑.
2. **`INSERT ... RETURNING` 구문 파서 및 실행기 완전 지원**:
   - 신규 행 삽입 후 자동 생성된 PK를 즉시 반환받는 PostgreSQL 표준 DML 파이프라인 구축 (`RowDescription` + `DataRow` 스트리밍).
3. **`pg_catalog.pg_indexes` 가상 카탈로그 뷰 구현**:
   - PostgreSQL 클라이언트가 기존 인덱스 중복 여부를 확인할 수 있는 카탈로그 뷰 추가.
4. **Single-Pass 파라미터 안전 치환 스캐너 (`Quote-Aware Scanner`)**:
   - 단순 치환 시 비밀번호 해시 문자열(`pbkdf2$50000$50...`) 내의 `$5`가 5번째 파라미터로 오인 치환되어 계정 인증이 파괴되던 치명적 결함을 단일 패스 스캐너로 완벽 해결.
5. **OID 0 비타입 파라미터 및 수치형 타입 안전 역직렬화**:
   - OID 0(텍스트 추론 파라미터)으로 전송된 데이터가 기본값(`0`)으로 유실되지 않도록 파싱 및 `ParseLiteralDefault` 안전 캐스팅 구현 (예: 조직 생성 시 `type = 1` 보존).

---

## 5. Gitea 전체 비즈니스 라이프사이클 E2E 실전 검증

단순 부팅을 넘어, 실제 프로덕션 환경의 모든 Git 및 협업 라이프사이클을 모의하는 자동화 시더([`scripts/seed_gitea_mock_data.go`](../scripts/seed_gitea_mock_data.go))를 작성하여 TDS(:1433)와 PGWire(:5433) 양쪽 모두에서 **100% 무결점 통과(All Pass)**를 달성했습니다.

| 비즈니스 라이프사이클 단계 | 검증 항목 | 검증 결과 |
| :--- | :--- | :---: |
| **1. 관리자 및 사용자 인증** | 최초 관리자 계정 생성, 일반 사용자 등록, API 토큰 발급 | **PASS** |
| **2. 조직 및 팀 거버넌스** | 조직(Org) 생성, 개발팀(Team) 구성, 멤버십 권한 할당 | **PASS** |
| **3. 리포지토리 생성 및 설정**| 신규 저장소 생성, README 초기화, 공개/비공개 권한 설정 | **PASS** |
| **4. 이슈 및 프로젝트 협업** | 이슈 생성, 마일스톤 연결, 라벨 태깅, 코멘트 작성 | **PASS** |
| **5. 브랜치 및 소스 코드 관리**| 신규 기능 브랜치 생성, 다중 파일 커밋 및 푸시 | **PASS** |
| **6. 풀 리퀘스트 및 코드 리뷰**| PR 발의, 코드 리뷰 코멘트 작성, 승인(Approval), 머지(Merge) | **PASS** |
| **7. 릴리즈 및 아티팩트** | 시맨틱 버전 태그 릴리즈 생성 및 Web UI 렌더링 검증 | **PASS** |

---

## 6. 프로토콜별 특성 비교 및 운영 권장사항

| 비교 항목 | Microsoft SQL Server (TDS :1433) | PostgreSQL (PGWire :5433) |
| :--- | :--- | :--- |
| **Gitea 설정 (`db_type`)** | `mssql` | `postgres` |
| **연결 드라이버** | `com.microsoft.sqlserver`, `go-mssqldb` | `lib/pq`, `pgx` |
| **주요 장점** | T-SQL 고유 저장 프로시저 및 VDBE 바이트코드 직접 바인딩 최적화 | 범용 오픈소스 클라이언트 호환성 및 풍부한 커넥션 풀링 생태계 |
| **권장 스토리지 엔진** | In-Memory OCC (초고속 인스턴스) / WiredTiger (대용량 디스크) | 동일 지원 (In-Memory OCC / WiredTiger) |
| **상세 설정 매뉴얼** | [`doc/GITEA_INTEGRATION_GUIDE.ko.md`](../doc/GITEA_INTEGRATION_GUIDE.ko.md) 2.1절 참조 | [`doc/GITEA_INTEGRATION_GUIDE.ko.md`](../doc/GITEA_INTEGRATION_GUIDE.ko.md) 2.2절 참조 |

---

## 7. 향후 로드맵 영향 및 결론

Gitea의 완전한 실전 라이프사이클 검증 성공은 LionSQL이 실험실 단계를 완전히 벗어나 **"실제 고부하 프로덕션 웹 애플리케이션을 지탱하는 엔터프라이즈 RDBMS"** 단계에 도달했음을 선언하는 마일스톤입니다.

1. **테스트 사각지대 전면 해소**:
   - 단위 테스트 위주의 검증에서 벗어나, 대형 ORM의 실전 쿼리 패턴을 흡수함으로써 엔진 신뢰도를 근본적으로 격상했습니다. (상세 내용: [`notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md))
2. **타 엔터프라이즈 애플리케이션 확장 기반 확보**:
   - Gitea 외에도 Redmine, Nextcloud, Grafana 등 타 오픈소스 엔터프라이즈 소프트웨어 연동을 위한 탄탄한 기술적 표준 토대를 확립했습니다.
3. **v0.90.0-beta4 완전 탑재**:
   - 본 마일스톤에서 확보된 모든 커널 최적화와 가상 뷰, DDL/DML 개선 사항은 `v0.90.0-beta4`에 정식 탑재되어 공식 배포되었습니다.
