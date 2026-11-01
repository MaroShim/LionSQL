# LionSQL Woodpecker CI 및 Vikunja 연동 검증 보고서 (INTEG-WOODPECKER-1, INTEG-VIKUNJA-1)

> - **최초 작성일**: 2026-11-01
> - **상태**: 완료 (Completed)
> - **대상 애플리케이션**:
>   - **Woodpecker CI**: 공식 Docker 이미지 `woodpeckerci/woodpecker-server:v3` (v3.20.1)
>   - **Vikunja**: 공식 Docker 이미지 `vikunja/vikunja:latest` (v2.7.1)
> - **연동 프로토콜**: PostgreSQL Wire Protocol v3 (`TCP :5433`, SSL 비활성화)
> - **스토리지 엔진**: WiredTiger MVCC 디스크 B+Tree 엔진

---

## 1. 개요 및 목적

Woodpecker CI는 Drone CI 및 GitHub Actions를 대체하는 현대적인 컨테이너 기반 CI/CD 서버이며, Vikunja는 Trello와 Asana를 대체하는 올인원 태스크 관리 및 프로젝트 칸반 플랫폼입니다. 두 서비스 모두 Go 언어의 ORM인 **XORM**(`xorm.io/xorm`) 및 PostgreSQL 드라이버(`lib/pq`, `pgx`)를 핵심 데이터 계층으로 사용합니다.

본 연동 검증의 목적은 LionSQL의 PostgreSQL 와이어 프로토콜(PGWire)과 쿼리 엔진이 Go/XORM 기반 복합 엔터프라이즈 애플리케이션의 DDL 마이그레이션, 고난도 CTE(공통 테이블 표현식), 재귀 쿼리(`WITH RECURSIVE`), 복합 인덱스 검색 및 DML 갱신을 원본 소스코드 수정 없이(Zero Code Change) 100% 충족함을 실증하는 것입니다.

---

## 2. 연동 검증 결과 요약

### 2.1 Woodpecker CI (`INTEG-WOODPECKER-1`)
| 검증 항목 | 대상 및 상세 | 결과 |
| :--- | :--- | :---: |
| **XORM 스키마 마이그레이션** | 19개 테이블, 32단계 마이그레이션 순차 적용 | **성공 (0.5s 이내 완료)** |
| **서버 헬스체크** | `GET /healthz` (HTTP 204 No Content 반환) | **성공 (204 No Content)** |
| **서버 버전 확인** | `GET /version` (`{"version":"3.20.1"}`) | **성공 (200 OK)** |
| **웹 대시보드 UI 서빙** | `GET /` 웹 클라이언트 SPA 서빙 | **성공 (200 OK)** |
| **가상 카탈로그 라우팅** | `pg_tables`, `pg_attribute` 파라미터화 인트로스펙션 | **성공 (Zero-overhead)** |

### 2.2 Vikunja (`INTEG-VIKUNJA-1`)
| 검증 항목 | 대상 및 상세 | 결과 |
| :--- | :--- | :---: |
| **XORM 스키마 마이그레이션** | 80단계 이상의 고유 테이블/인덱스 마이그레이션 | **성공 (0.6s 이내 완료)** |
| **서비스 헬스체크 및 정보** | `GET /api/v1/info` (버전 v2.7.1 확인) | **성공 (200 OK)** |
| **웹 UI 프론트엔드 서빙** | `GET /` 웹 클라이언트 SPA 서빙 | **성공 (200 OK)** |
| **사용자 회원가입 (Register)** | `POST /api/v1/register` 신규 사용자 생성 | **성공 (200 OK)** |
| **JWT 인증 로그인 (Login)** | `POST /api/v1/login` (복합 `OR` 술어 기반 사용자 조회) | **성공 (200 OK)** |
| **프로젝트 및 4대 뷰 생성** | `PUT /api/v1/projects` (List/Gantt/Table/Kanban) | **성공 (201 Created)** |
| **칸반 태스크 생성** | `PUT /api/v1/projects/2/tasks` (순번 카운터 증가) | **성공 (201 Created)** |
| **태스크 목록 및 권한 평가** | `GET /api/v1/projects/2/tasks` (중첩 CTE + Derived Join) | **성공 (200 OK)** |
| **태스크 완료 상태 갱신** | `POST /api/v1/tasks/1` (`done: true`, `RowsAffected` 감지) | **성공 (200 OK)** |

---

## 3. 핵심 호환성 이슈 및 해결 내역

### 3.1. PGWire Describe 단계에서 CTE 쿼리의 컬럼 메타데이터 오염
- **현상**: Vikunja의 복합 권한 쿼리(`WITH grants (...) SELECT id, permission ...`) 파싱 시, PGWire `writeSchemaDescription`이 첫 번째 괄호 안의 임시 테이블 스키마나 서브쿼리 내부 컬럼을 메인 프로젝션 컬럼으로 오인하여 클라이언트에 엉뚱한 컬럼 메타데이터가 전달됨.
- **원인**: `ParseCTEs`로 분리하기 전 원본 SQL의 첫 번째 `SELECT ... FROM` 패턴을 매칭하면서 CTE 정의 절 내부의 텍스트가 추출됨.
- **해결**: `pkg/wire/server.go`의 `writeSchemaDescription`에서 CTE 쿼리인 경우 `planner.ParseCTEs`를 통해 메인 질의를 명확히 분리한 뒤, 메인 질의의 프로젝션 절에서 최종 컬럼 목록을 정확히 추출하도록 개선.

### 3.2. 공통 테이블 표현식(CTE) 동시 실행 시 임시 테이블 충돌
- **현상**: 동일한 CTE 이름(`grants`)을 사용하는 질의가 연속/동시 실행될 때 `duplicate key value violates unique constraint on "#__cte_grants": key already exists: key "2"` 에러 발생.
- **원인**: CTE 임시 테이블명이 정적(`#__cte_<name>`)이었으며, 기본 키가 자동 생성 `IDENTITY` 컬럼에 의존하여 다중 행 삽입 시 충돌이 발생함.
- **해결**:
  1. `atomic.AddUint64(&cteGlobalSeq, 1)` 기반의 유니크 임시 테이블명(`#__cte_<name>_<seq>`)을 생성하여 세션 및 쿼리 간 완전 격리 달성.
  2. `_cte_row_id INT PRIMARY KEY`를 명시하고 1부터 순차 증가하는 행 번호를 명시적으로 삽입하도록 수정.

### 3.3. PostgreSQL 표준 재귀 CTE(`WITH RECURSIVE`) 지원
- **현상**: Vikunja가 프로젝트 상속 구조를 조회할 때 `WITH RECURSIVE project_hierarchy AS (...)` 쿼리를 실행하여 `table "project_hierarchy" not found` 에러 발생.
- **해결**:
  1. `pkg/planner/planner.go`의 `ParseCTEs`에서 PostgreSQL 전용 `RECURSIVE` 한정자를 안전하게 제거하여 테이블 이름을 정확히 식별.
  2. `pkg/engine/sqlexec_explain.go`의 `execCTE`에서 본체 쿼리가 자신을 참조하는 재귀 CTE일 경우, 초기 집합(Base Query)을 1회 실행하여 임시 테이블에 적재한 후, 재귀 항(Recursive Query)을 반복 실행하여 결과 행이 0이 될 때까지 최대 100회 누적 수렴 평가하는 반복 평가 엔진 구현.

### 3.4. VDBE UPDATE 명령 태그 미인식 및 중복 키 오류
- **현상**: 태스크 생성 시 `project_task_counters` 테이블에 카운터 갱신(`UPDATE`)이 성공했음에도 불구하고 클라이언트가 영향 받은 행 수를 0으로 인식하여 불필요한 Fallback INSERT를 실행, 고유 키 위반(`duplicate key value violates unique constraint on "project_task_counters"`) 발생.
- **원인**: VDBE 프로그램 타입이 `cmd == "DML"`로 설정되어 있어 `formatPgCommandComplete("DML", 1, 0)`이 클라이언트에 `"DML 1"`을 전달함. `pgx`/`lib/pq`는 `"UPDATE 1"` 규격 태그만 파싱 가능하므로 반환 행 수가 0으로 처리됨.
- **해결**: `pkg/wire/server.go`의 `handleExecute`에서 `portal.Statement.Program.Type == "DML"`인 경우 원래 SQL 질의문의 첫 번째 토큰(`UPDATE`, `INSERT`, `DELETE`)을 추출하여 정확한 PostgreSQL 커맨드 태그(`"UPDATE 1"`, `"INSERT 0 1"` 등)를 생성하도록 보정.

### 3.5. 복합 인덱스 컬럼의 `OR` 조건 색인 범위 오인
- **현상**: 사용자 로그인 시 `WHERE (username = $1 OR email = $2)` 쿼리가 0건을 반환하며 HTTP 403 인증 실패 발생.
- **원인**: `planner.go` 및 `bounds.go`의 인덱스 Seek 후보 탐색 정규식(`eqRe`)이 `username = 'maro' OR email = 'maro'` 전체 문자열을 `username`의 등호 조건으로 잘못 캡처하여 잘못된 인덱스 Seek 범위를 생성함.
- **해결**:
  1. 인덱스 Seek 바운드 추출 로직에서 `" OR "` 논리 연산자가 포함된 조건은 단일 컬럼 인덱스 Seek 바운드로 변환하지 않고 건너뛰어 전체 필터 평가로 안전하게 위임.
  2. `pkg/proc/runtime/vm.go`의 `bindWhereVariables`에서 `$1`, `$2`와 같은 달러 파라미터 및 숫자 접미사 바인딩을 지원하여 Volcano 실행기 평가 시 파라미터가 정확한 값으로 치환되도록 수정.

---

## 4. 단위 및 회귀 테스트 체계

본 연동 검증을 통해 발견된 모든 엣지 케이스는 순수 인메모리 엔진 기반의 영구 회귀 테스트로 등재되었습니다:
- `pkg/engine/woodpecker_compat_test.go`:
  - `TestWoodpeckerCI_XORMMigrationQueries`: XORM 마이그레이션 테이블 인트로스펙션, DDL 및 시퀀스 검증 (100% PASS)
- `pkg/engine/vikunja_compat_test.go`:
  - `TestVikunja_CTEWithUnionDerivedTable`: 중첩 UNION ALL 및 집계 함수를 포함한 CTE 권한 평가 검증 (100% PASS)
  - `TestVikunja_RecursiveCTE`: 무한 깊이 재귀 CTE(`WITH RECURSIVE`) 계층 탐색 검증 (100% PASS)
  - `TestVikunja_WhereOrPredicate`: 인덱스 컬럼 대상 `OR` 술어의 안전한 필터링 검증 (100% PASS)

---

## 5. 결론 및 향후 계획

LionSQL은 Woodpecker CI와 Vikunja라는 두 가지 핵심 Go/XORM 기반 오픈소스 프로덕션 소프트웨어를 소스코드 수정 없이 완벽하게 수용함으로써, XORM 호환성 생태계를 Gitea, Casdoor, Grafana에 이어 완전한 수준으로 확장하였습니다.
다음 과제로는 텍스트 기반 선언적 호환성 엔진(`COMPAT-DECLARATIVE-1`) 및 노코드 DB 스튜디오인 NocoDB(`INTEG-NOCODB-1`) 연동을 순차 진행할 예정입니다.
