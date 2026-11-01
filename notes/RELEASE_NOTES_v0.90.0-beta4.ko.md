# LionSQL v0.90.0-beta4 릴리즈 노트 (`notes/RELEASE_NOTES_v0.90.0-beta4.ko.md`)

> - **릴리즈 일자**: 2026-10-30
> - **버전**: `v0.90.0-beta4`
> - **마일스톤 주제**: **Gitea 듀얼 프로토콜 연동 완주, 서버 파이프라인 135k tpmC 달성, SSB 벤치마크 스위트, 그리고 Windows/macOS/Linux 정적 빌드 자동화**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.90.0-beta4`는 대표적인 오픈소스 Git 협업 플랫폼인 **Gitea와의 프로덕션 듀얼 프로토콜(TDS/PGWire) 연동**을 완료하고, 서버 네트워크 파이프라인 최적화를 통해 **TPC-C 135,000 tpmC 돌파**, 그리고 **Windows/Linux/macOS 3대 플랫폼 정적 크로스 컴파일 배포 파이프라인**을 완성한 대규모 엔지니어링 릴리즈입니다.

- **Gitea 무변경 프로덕션 연동 완성**: 복잡한 XORM ORM 마이그레이션(40+ 테이블)과 DML/DDL을 MSSQL TDS 및 PostgreSQL Wire 듀얼 프로토콜 환경에서 100% 무변경 연동.
- **서버 파이프라인 고도화 및 135,000 tpmC 돌파**: 소켓 읽기 병합(Read Coalescing), 32KB 패킷 풀링, 제로 할당 RPC 바인딩을 통해 네트워크 TPC-C 135,000 tpmC / 5,000 TPS 기록.
- **Star Schema Benchmark (SSB) 스위트 내장**: 복합 분석계 쿼리 처리를 위한 Star Schema Benchmark 전용 CLI(`cmd/ssb`) 및 튜플 AST 제로 할당 평가기 도입.
- **T-SQL Forward-Only 커서 및 멀티 방언 내장 함수 지원**: 엔터프라이즈 프로시저용 고속 전방향 커서(`PROC-CURSOR-1`) 및 고수익 멀티 방언 함수 Best 7 탑재.
- **3대 OS 크로스 컴파일 릴리즈 자동화**: 단일 릴리즈 스크립트(`scripts/release.sh`)로 macOS arm64, Linux amd64, Windows amd64(`lionsql.exe`) 자립형 정적 바이너리 자동 패키징 구축.

---

## 2. 세부 변경 사항

### 2.1. Gitea 듀얼 프로토콜 및 ORM 마이그레이션 호환
- **PostgreSQL Wire 호환 DDL/DML 확장 (`GITEA-PGWIRE-1`)**:
  - `BIGSERIAL`, `SERIAL`, `SMALLSERIAL` 자동 증가 기본키 DDL 지원.
  - PostgreSQL 전용 `INSERT ... RETURNING` 절 런타임 평가 및 `RowDescription` 메타데이터 전파 구현.
  - XORM 인덱스 리버스 엔지니어링을 위한 `pg_catalog.pg_indexes` 가상 카탈로그 뷰 구축.
- **TDS MSSQL 프로토콜 호환 DML 확장**:
  - `UPDATE <target> FROM <source> JOIN ...` 및 `DELETE FROM <target> FROM ...` 조인 기반 DML 구문 지원.
  - `cleanSymbol` 변수 접두사 파싱 정합성 및 복합 인덱스 경계 괄호 언매칭 트랩 제거.
- **Gitea 대규모 목업 시더 도구 (`scripts/seed_gitea_mock_data.go`)**:
  - 사용자, 조직, 저장소, 이슈, 라벨, 마일스톤, 브랜치, 커밋, PR, 리뷰, 릴리즈 등 Gitea의 전 도메인을 시뮬레이션하는 대규모 목업 시더 내장.

---

### 2.2. 고성능 서버 네트워크 파이프라인 최적화 (`PERF-SERVER-PIPELINE`)
- **네트워크 소켓 I/O 병합 및 버퍼 풀링 (`Phase 1`)**:
  - TDS 소켓 읽기 병합(Read Coalescing) 및 32KB 패킷 버퍼 풀링(`sync.Pool`) 적용.
  - OS 컨텍스트 스위칭 오버헤드를 줄이기 위한 세션 피닝(Session Pinning) 도입.
- **제로 할당 RPC 매개변수 바인딩 (`Phase 2`)**:
  - TDS RPC 파라미터 역직렬화 시 메모리 할당을 제거하고 컴파일된 VM 실행 계획(`PlanCache`)으로 직결 디스패치.
  - 네트워크 TPC-C 벤치마크 실측: **135,000 tpmC / 5,000 TPS** (단일 노드 극한 하드웨어 포화).

---

### 2.3. 분석계 쿼리 엔진 및 SSB 벤치마크 (`BENCH-SSB-1`, `PERF-COMPLEX-QUERY-1`)
- **Star Schema Benchmark (SSB) CLI 구축**:
  - 분석계 표준 Star Schema Benchmark 13대 복합 분석 쿼리를 측정하는 독립 벤치마크 도구(`cmd/ssb`) 구현.
- **튜플 AST 제로 할당 평가기**:
  - 프로젝션 및 필터 연산 시 메모리 힙 할당을 최소화하는 제로 카피 튜플 평가기 및 산술 집계 최적화 도입.
- **운영자 중첩 표현식 감사 스위트 (`AUDIT-EXPR-1`)**:
  - 복합 중첩 괄호, 단항/이항 연산자 조합의 수식 무결성을 검증하는 심층 단언 테스트 스위트 구축.

---

### 2.4. T-SQL 프로시저 및 멀티 방언 사용성 혁신
- **T-SQL 고속 전방향 커서 (`PROC-CURSOR-1`)**:
  - 읽기 전용 대용량 행 조회를 위한 `DECLARE ... CURSOR FOR`, `OPEN`, `FETCH NEXT FROM ... INTO`, `CLOSE`, `DEALLOCATE` 및 `@@FETCH_STATUS` 지원.
- **고수익 멀티 방언 내장 함수 Best 7 (`FUNC-DIALECT-7`)**:
  - `IFNULL`, `NVL`, `IIF`, `INSTR`, `SUBSTR`, `SPLIT_PART`, `GENERATE_SERIES` 등 주요 DBMS 호환 함수 내장.
- **MySQL 스타일 구문 편의성 1단계 (`S-MYSQL-1-P1`)**:
  - 백틱(` ` `) 식별자 이스케이프 및 `DESC <table_name>`, `SHOW TABLES` 편의 구문 네이티브 지원.

---

### 2.5. 릴리즈 자동화 및 크로스 컴파일 배포
- **3대 플랫폼 정적 단일 바이너리 패키징 (`scripts/release.sh`)**:
  - macOS arm64 (`lionsql`)
  - Linux amd64 (`lionsql`)
  - Windows amd64 (`lionsql.exe`, 순수 Go zero-CGo 고성능 타이머 탑재)
- **웹 스튜디오 탭 세션 격리 (`UI-CONCURRENCY-1`)**:
  - 브라우저 탭 간 트랜잭션 충돌을 방지하는 독립 세션 ID 바인딩 및 오버뷰 메모리 캐싱 적용.

---

## 3. 검증 지표 및 벤치마크 스코어카드

| 검증 영역 | 벤치마크 / 대상 | 주요 실측 지표 | 비고 |
| :--- | :--- | :--- | :--- |
| **OLTP 트랜잭션** | TPC-C (TDS 네트워크) | **135,000 tpmC / 5,000 TPS** | 서버 파이프라인 패킷 풀링 적용 후 |
| **OLAP 복합 분석** | SSB (Star Schema) | 13대 쿼리 베이스라인 측정 완료 | 제로 할당 튜플 평가기 탑재 |
| **실서비스 연동** | Gitea (v1.22+) | XORM 마이그레이션 & DML 100% PASS | TDS & PGWire 듀얼 프로토콜 지원 |
| **플랫폼 호환성** | macOS / Linux / Windows | 3대 OS 바이너리 패키징 100% 성공 | 단일 스크립트 크로스 컴파일 완주 |

---

<details>
<summary><b>상세 변경 내역 (CHANGELOG 보기)</b></summary>

### Added
- support BIGSERIAL, SERIAL, and SMALLSERIAL autoincrement primary key DDL in Pratt parser and DDL executor (GITEA-PGWIRE-1)
- support PostgreSQL DML RETURNING clause evaluation and RowDescription schema propagation in PGWire server (GITEA-PGWIRE-1)
- implement pg_catalog.pg_indexes virtual catalog view for ORM index introspection (GITEA-PGWIRE-1)
- add comprehensive Gitea mock data seeder covering users, orgs, repos, issues, labels, milestones, branches, commits, PRs, reviews, merges, and releases (scripts/seed_gitea_mock_data.go)
- support MSSQL UPDATE and DELETE FROM source table syntax in Pratt parser and DML parser
- support aggregate function and keyword aliases after AS in SELECT projections
- support table-qualified wildcard projections (table.*) in multi-table joins to eliminate column collisions
- implement web studio tab session isolation and overview memory caching (UI-CONCURRENCY-1)
- add automated release and changelog grouping pipeline (scripts/release.sh)
- add Windows .exe cross-compilation targets to release pipeline
- integrate GitHub Release creation with downloadable assets in release pipeline
- standardize 3 official release targets (macOS arm64, Linux amd64, Windows amd64)
- add high-ROI multi-dialect built-in functions Best 7 (FUNC-DIALECT-7)
- add MySQL-style concise syntax ergonomics Phase 1 (S-MYSQL-1-P1)
- add T-SQL fast forward-only read-only cursor and FETCH_STATUS (PROC-CURSOR-1)
- add Star Schema Benchmark (SSB) suite and CLI tool (BENCH-SSB-1)
- record SSB complex query performance baseline metrics
- implement zero-alloc tuple AST evaluator and arithmetic aggregate optimization (PERF-COMPLEX-QUERY-1)
- implement operator nested expression audit and deep assertion test suite (AUDIT-EXPR-1)
- implement socket read coalescing, 32KB packet sync.Pool, and session pinning (PERF-SERVER-PIPELINE-P1)
- implement zero-alloc RPC parameter binding and direct PlanCache dispatch (PERF-SERVER-PIPELINE-P2)
- support conditional DDL for ORM migrations (ORM-MIGRATE-1)
- resolve XORM column introspection routing and max_length scan error (ORM-MIGRATE-2)
- support assignment aliasing, default_is_null bool scan, and fn_helpcollations (ORM-MIGRATE-3)

### Fixed
- prevent string literal parameter corruption by implementing single-pass quote-aware scanner in PGWire parameter substitution
- fix virtual catalog join interception ordering to prioritize pg_attribute column introspection over foreign keys
- decode untyped PGWire parameters (OID 0) to numeric types and coerce quoted string literals to integer/float/decimal in ParseLiteralDefault
- auto-resolve default wire port by protocol in tpcc-client
- resolve XORM GetIndexes interception collision on sys.columns queries with collation_name
- resolve composite index boundary parsing unmatched parentheses trap in bounds normalizer
- resolve variable assignment cleanSymbol prefix stripping in VDBE storage dispatch
- treat WT_NOTFOUND as no-op in cursor_remove rather than error
- make changelog insertion compatible with BSD awk on macOS
</details>
