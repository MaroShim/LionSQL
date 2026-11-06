# LionSQL Engineering Notes & Context Handover (`notes/`)

> - **최초 작성일**: 2026-10-03
> - **최종 갱신일**: 2026-11-06
> [English Version](README.md)

이 디렉토리는 다른 개발 환경(다른 머신, 새 세션)으로 이동했을 때, 새로운 AI 에이전트나 동료 엔지니어가 **코드와 공식 문서(`doc/`)만으로는 파악하기 힘든 개발 이면의 맥락, 기각된 대안(Negative Knowledge), 실전 주의사항(Pitfalls), 사용자의 엔지니어링 철학**을 즉시 동기화받을 수 있도록 정리한 영속적 지식 베이스(Shared Brain & Handover Notes)입니다.

---

## 1. 핵심 인지 체계 & 불변 원칙 (Core Philosophy & SSOT)

엔진 전반에 걸쳐 타협 없이 지켜져야 하는 핵심 불변식, 개발 철학, 위험 요소 및 전역 백로그 체계입니다.

* **[`ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)** (한국어)
  * **핵심 아키텍처 불변식(Core Invariants 8선)**: Dual-Parser, 인스턴스 격리, Non-Blocking MVCC/OCC, 단일 정적 바이너리, 제로 정규식 파싱 원칙.
  * **명시적 Spec-Out 철학**: 불필요한 레이어를 제거하고 YAGNI/KISS를 수호하기 위해 기각된 14대 기능과 그 이론적 배경.
* **[`PITFALLS_AND_TRICKS.md`](PITFALLS_AND_TRICKS.md)**
  * **코드베이스 내 암초 지도(Landmines & Fragile Spots)**: 순환 참조 방지 SPI 패턴, 샌드박스 소켓 격리, 원자적 파일 I/O 및 Decimal JSON 무결성.
* **[`TAXONOMY.ko.md`](TAXONOMY.ko.md)** (한국어)
  * **과제 식별 코드 체계 일람표**: 우선순위(`P1~P3`), 복합 쿼리 최적화(`VPERF`), 릴리스 게이트(`GA-REGEX`), 문법 완비(`S-1~S-7`) 마스터 인덱스.
* **[`PROTOCOL_EMULATION_BOUNDARIES.md`](PROTOCOL_EMULATION_BOUNDARIES.md)**
  * **다중 프로토콜 에뮬레이션 경계**: TDS(1433), PGWire(5433), MySQL 호환 구문의 수용 범위와 중단 기준.
* **전역 로드맵 및 단일 진실 공급원 (SSOT Backlogs)**:
  * **[`TODO.md`](../TODO.md)**: 3단계 우선순위 실시간 엔지니어링 대시보드 (Sprint DB와 100% 동기화).
  * **[`ROADMAP.md`](../ROADMAP.md)**: 거시적 릴리즈 마일스톤(Phase 1~6) 및 아키텍처 진화 방향.

---

## 2. 1.0 릴리스 액티브 스프린트 (Release Gates & Active Audits)

LionSQL 1.0 정식 릴리스 품질 완성을 위해 현재 집중 진행 중인 전수 감사 및 무결성 강화 프로젝트입니다.

* **[`PRE_RELEASE_100_STEP_DUAL_CODE_REVIEW_PLAN.ko.md`](PRE_RELEASE_100_STEP_DUAL_CODE_REVIEW_PLAN.ko.md)**
  * 전 소스코드 100단계 듀얼 AI(Gemini × Claude) 크로스 감사 마스터 플랜 및 10대 도메인 실시간 추적 매트릭스 (`AUDIT-100-DUAL-REVIEW`).
* **[`PRE_RELEASE_AUDIT_FINDINGS.ko.md`](PRE_RELEASE_AUDIT_FINDINGS.ko.md)**
  * 100단계 전수 감사 과정에서 발굴된 실측 결함 누적 매트릭스 (Scope 001~020 스캔 완료, 18건 기록).
* **[`REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md)**
  * LionSQL 전역 정규식 완전 박멸 및 결정론적 토크나이저 전환 단일 SSOT 종합 계획서 (`GA-REGEX-DDL-1` ~ `GA-REGEX-FINAL-3`, `SEC-PGWIRE-PARAM-1`).
* **[`DURABILITY_VERIFICATION_PLAN.ko.md`](DURABILITY_VERIFICATION_PLAN.ko.md)**
  * 킬 테스트(SIGKILL 즉시 복구), 소크 테스트, 개밥 먹기(Dogfooding) 기반 실전 운영 내구성 검증 계획서.

---

## 3. 도메인별 심층 설계 계획서 (Architecture & Execution Plans)

엔진의 각 핵심 서브시스템별 설계 및 리팩토링 명세서입니다.

### 3.1. 스토리지 엔진 & 고동시성 트랜잭션 (Storage & OCC)
* **[`IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)**: In-Memory OCC 최적화 역사, WiredTiger 초격차 추월, 104만 tpmC 아키텍처 및 Option A 플랫 레코드 하드닝 (`OCC-HYPER`).
* **[`FLAT_RECORD_RELIABILITY_PLAN.ko.md`](FLAT_RECORD_RELIABILITY_PLAN.ko.md)**: 포인터 없는 플랫 레코드 인코딩의 안정성 및 디코딩 신뢰성 검증 계획서.
* **[`LAZY_DECODING_P2_PLAN.ko.md`](LAZY_DECODING_P2_PLAN.ko.md)**: 레코드 전체 언패킹 비용을 없애는 칼럼 단위 지연 디코딩(Lazy Decoding) 설계 계획서.
* **[`PERF_REGRESSION_RECOVERY_AND_GATE_PLAN.ko.md`](PERF_REGRESSION_RECOVERY_AND_GATE_PLAN.ko.md)**: 키 래치 완전 제거를 통한 160k TPS 회귀 복구 및 CI 성능 게이트 구축 계획서.

### 3.2. 쿼리 엔진, 옵티마이저 & 파서 (Engine, Planner & Parser)
* **[`QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md)**: 단순 질의(VDBE)와 복합 관계대수(CBO)를 분리하는 영구 불변 2-Tier 쿼리 실행 엔진 아키텍처 명세서.
* **[`ARCH_VDBE_ROUTING_1_PLAN.ko.md`](ARCH_VDBE_ROUTING_1_PLAN.ko.md)**: AST 및 Dialect 타입 기반 2-Tier 쿼리 라우팅 정규화 계획서 (`ARCH-VDBE-ROUTING-1`).
* **[`AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md)**: 심볼 테이블 및 AST 기반 구조적 카탈로그 라우팅 리팩토링 검증 보고서 (527ns 실측).
* **[`LARGE_JOIN_OPTIMIZATION_PLAN.ko.md`](LARGE_JOIN_OPTIMIZATION_PLAN.ko.md)**: 10개 이상 테이블 조인 최적화 및 공집합 조기 탈락 설계 계획서 (`OPT-LARGE-JOIN-1`).
* **[`PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md`](PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md)**: 플래너 슬림화 및 순수 AST 전환 설계 계획서 (`PLANNER-SLIM-1`, `PLANNER-AST-1`).
* **[`VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md`](VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md)**: 바이트코드 루프 언롤링, 레지스터 풀링, 분기 예측 최적화 극단 가속 계획서.
* **[`WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md`](WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md)**: 쓰기 가능한 CTE(Writable CTE)를 VDBE 바이트코드로 직접 컴파일하는 Lowering 설계.
* **[`SQL_PARSER_HARDENING_PLAN.ko.md`](SQL_PARSER_HARDENING_PLAN.ko.md)**: 리터럴 오염 방지 및 세미콜론 절단 방지를 위한 SQL 파서 하드닝 계획서.

### 3.3. 와이어 프로토콜 & 네트워킹 (Wire & Protocols)
* **[`SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md)**: 소켓 수신부터 VDBE 및 WAL 적응형 그룹 커밋에 이르는 서버 수직 파이프라인 최적화 마스터 플랜.
* **[`SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)**: 단일 정적 바이너리 내장 웹 스튜디오 및 SPA 서빙 아키텍처 계획서 (`WEB-SPA-1`, 공식 가이드: [`doc/WEB_APP_HOSTING_GUIDE.ko.md`](../doc/WEB_APP_HOSTING_GUIDE.ko.md)).
* **[`MODULAR_ARCHITECTURE_PLAN.ko.md`](MODULAR_ARCHITECTURE_PLAN.ko.md)**: TDS/와이어 레이어 인터페이스 불변 파일 분리 및 모듈러 아키텍처 평가 계획서 (`TDS-MOD-1`).

---

## 4. 실전 앱 연동 & 무결성 실측 검증 보고서 (Verification Reports)

실제 오픈소스 서비스 및 표준 벤치마크 스위트를 통해 검증된 실측 엔지니어링 보고서입니다.

### 4.1. 10대 프로덕션 오픈소스 애플리케이션 E2E 검증
* **[`METABASE_INTEGRATION_REPORT.ko.md`](METABASE_INTEGRATION_REPORT.ko.md)**: Metabase BI 듀얼 프로토콜 연동, MBQL 비주얼 쿼리 빌더 및 집계/시계열 100% 무오류 검증.
* **[`VAULTWARDEN_INTEGRATION_REPORT.ko.md`](VAULTWARDEN_INTEGRATION_REPORT.ko.md)**: Vaultwarden (Rust Diesel ORM) 47개 마이그레이션 및 Bitwarden REST API 연동 완료.
* **[`ANSWER_INTEGRATION_REPORT.ko.md`](ANSWER_INTEGRATION_REPORT.ko.md)**: Apache Answer Q&A 35개 테이블 생성 및 Identity 자동 발급 E2E 검증.
* **[`GITEA_INTEGRATION_REPORT.ko.md`](GITEA_INTEGRATION_REPORT.ko.md)**: Gitea 듀얼 프로토콜 연동 및 116개 테이블 실전 E2E 검증 완료.
* **[`WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md`](WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md)**: Woodpecker CI 및 Vikunja 재귀 CTE, 복합 OR 인덱스 분기 검증.
* **[`GRAFANA_INTEGRATION_REPORT.ko.md`](GRAFANA_INTEGRATION_REPORT.ko.md)**: Grafana 785개 마이그레이션 완주, 봉투 암호화(`BYTEA`) 및 대시보드 연동 검증.
* **[`CASDOOR_INTEGRATION_REPORT.ko.md`](CASDOOR_INTEGRATION_REPORT.ko.md)**: Casdoor 44개 테이블 자동 생성, 복합 PK 및 OIDC/RBAC E2E 검증.
* **[`IOTA_ERP_INTEGRATION_REPORT.ko.md`](IOTA_ERP_INTEGRATION_REPORT.ko.md)**: IOTA SDK ERP 연동, `pgx/v5` 바이너리 프로토콜 완비 및 47개 마이그레이션 관통.
* **[`UMAMI_INTEGRATION_REPORT.ko.md`](UMAMI_INTEGRATION_REPORT.ko.md)**: Umami 웹 분석 플랫폼 연동 검증 보고서.
* **[`BABELFISH_INTEGRATION_REPORT.ko.md`](BABELFISH_INTEGRATION_REPORT.ko.md)**: AWS Babelfish T-SQL 저장 프로시저 호환성 검증 보고서.

### 4.2. 표준 벤치마크 및 장기 부하 실측
* **[`SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md`](SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md)** ([English](SLT_FULL_SUITE_100_PERCENT_REPORT.md))
  * SQLite 공식 SQLLogicTest (SLT) 107개 풀세트(551,217 쿼리) 100% 무결점 통과 실측 보고서 및 8대 심층 엔진 결함 완치 기록.
* **[`SOAK_TEST_1H_VERIFICATION_REPORT.ko.md`](SOAK_TEST_1H_VERIFICATION_REPORT.ko.md)**
  * 1시간 장시간 고부하(72만 ops / 39.3만 커밋) 실측 검증, `SQLSTATE 40001` 충돌 처리 및 WiredTiger 500MB 캐시 수렴 분석 보고서.
* **[`COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)**
  * SSB (Star Schema Benchmark) 및 Mini TPC-H 5대 대표 쿼리 실측, DuckDB/ClickHouse 비교 분석 및 1.0 완료 동결 보고.

---

## 5. 엔지니어링 포스트모텀 & 네거티브 지식 (Postmortems & Negative Knowledge)

실전 장애 극복 과정에서 얻은 교훈, 사각지대 분석 및 아키텍처 불변성을 지키기 위해 의도적으로 기각된 평가서입니다.

* **[`DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md`](DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md)**
  * DuckDB 스타일 편의 문법(`SELECT * EXCLUDE`, `FROM <table>;`) 및 MySQL 추가 호환 구문의 기각 사유와 불변 경계 평가 (오타 침묵 방지 및 RDBMS 엄격성 수호).
* **[`EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md`](EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md)**
  * `WHERE col IN ()` 빈 괄호 평가 허용 여부에 대한 분석, 1.0 엄격 에러 유지 및 무분별한 토글 플래그 배제 원칙.
* **[`ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md)**
  * Gitea XORM 실전 연동 중 노출된 결함 원인 분석 및 "인간 작성 SQL vs ORM 기계 생성 SQL" 테스트 사각지대 영구 분석.
* **[`TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md`](TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md)**
  * TDS RPC `sp_executesql` 파라미터 해석 리그레션 분석, Beekeeper Studio 접속 오류 규명 및 위치 기반(Ordinal Positional) 폴백 복원 포스트모텀.
* **[`GROVE_HOTSPOT_POSTMORTEM_AND_ASSETS.ko.md`](GROVE_HOTSPOT_POSTMORTEM_AND_ASSETS.ko.md)**
  * Grove 핫스팟 최적화 과정에서 도출된 핵심 자산(VDBE 슬롯 인덱싱, OCC vs WT 역할 분담, 락 컨보이/OS 타이머 퀀텀 실체) 심층 포스트모텀.

---

## 6. Post-1.0 후보 과제 & 역사적 아카이브 (Post-1.0 & Archives)

* **[`POST_1.0_CANDIDATES.ko.md`](POST_1.0_CANDIDATES.ko.md)**
  * LionSQL Post-1.0 기술 과제 풀 (사전 기술 분석 완료 및 온디맨드 채택 대기 목록).
* **[`MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md`](MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md)**
  * Materialized View 가상 뷰 알리아스 방식 도입 설계 (Post-1.0 이관 확정, `FEAT-MATVIEW-ALIAS-1`).
* **[`POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md)**
  * HNSW 벡터 인덱스 및 시계열 압축 청크 아키텍처 설계 계획서.
* **[`OFFLINE_DOCS_PACKAGING_PLAN.ko.md`](OFFLINE_DOCS_PACKAGING_PLAN.ko.md)**
  * 배포 패키지 오프라인 문서 번들링 및 내장 뷰어 설계 계획서 (`PKG-OFFLINE-DOCS-1`).
* **[`BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md`](BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md)**
  * AWS Babelfish T-SQL Stored Procedure 전용 SLT 테스트 스위트 구축 계획서.
* **역사적 아카이브 디렉토리 (`notes/archive/`)**:
  * 100% 구현 완료되어 설계 의도 및 검증 이력을 보존하기 위해 보관된 과거 13개 계획서(Windows 데몬 분리, Forward-only 커서, 단일 CLI 통합 등) 및 원본 백로그는 **[`notes/archive/README.md`](archive/README.md)**를 참조하십시오.
