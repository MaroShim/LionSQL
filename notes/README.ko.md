# LionSQL Engineering Notes & Context Handover (`notes/`)

> [English Version](README.md)

이 디렉토리는 다른 개발 환경(다른 머신, 새 세션)으로 이동했을 때, 새로운 AI 에이전트나 동료 엔지니어가 **코드와 공식 문서(`doc/`)만으로는 파악하기 힘든 개발 이면의 맥락, 기각된 대안(Negative Knowledge), 실전 주의사항(Pitfalls), 사용자의 엔지니어링 철학**을 즉시 동기화받을 수 있도록 정리한 영속적 지식 베이스(Shared Brain & Handover Notes)입니다.

---

## 문서 색인 (Index)

1. **[`ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)** (한국어)
   - **핵심 아키텍처 불변식(Core Invariants)**: 왜 이 방식을 택했고 무엇을 의도적으로 배제(Spec-Out)했는가
   - **개발 철학 및 협업 규약**: 무조건 준수해야 하는 엔지니어링 원칙 (3단계 검증 파이프라인, 이모지 배제, KISS/YAGNI, 파일 분할 기준)
   - **기술 스택 및 주요 컴포넌트**: VDBE 바이트코드 VM, 듀얼 파서, 듀얼 트랙 UDF, 멀티 프로토콜

2. **[`PITFALLS_AND_TRICKS.md`](PITFALLS_AND_TRICKS.md)**
   - **코드베이스 내 암초 지도(Landmines & Fragile Spots)**: 섣불리 건드리면 깨지기 쉬운 미묘한 포인트들
   - **순환 참조 방지 SPI 패턴**: `pkg/proc`과 ANTLR4 폴백 파서 간 디커플링
   - **샌드박스 및 네트워크 격리 이슈**: 테스트 환경에서의 로컬 루프백 소켓/HTTP 주의사항
   - **원자적 파일 I/O 및 데이터 타입 직렬화**: 크래시 방지 파일 저장 규칙과 Decimal JSON 무결성

3. **[`TAXONOMY.ko.md`](TAXONOMY.ko.md)** (한국어)
   - **과제 식별 코드 체계 일람표**: `P1~P3`(우선순위), `P-1~P-5`(성능), `A~D`(CBO 9대 과제), `S-1~S-7`(문법 완비) 종합 정리

4. **[`PROTOCOL_EMULATION_BOUNDARIES.md`](PROTOCOL_EMULATION_BOUNDARIES.md)**
   - **다중 프로토콜 에뮬레이션 경계 및 스코프**: TDS, PGWire, MySQL 프로토콜 에뮬레이션 범위 및 중단 기준, 패킷/토큰 처리 및 가상 No-Op 호환성 정책

5. **프로젝트 전역 로드맵 및 백로그**:
   - **[`TODO.md`](../TODO.md)**: 액티브 엔지니어링 백로그, 3계층 우선순위(Priority 1 내구성 스프린트, Priority 2 앱 연동, Priority 3 장기 로드맵) 및 완료 마일스톤(M1~M3)
   - **[`ROADMAP.md`](../ROADMAP.md)**: 거시적 릴리즈 마일스톤(Phase 1~6), 아키텍처 진화 방향, 단계별 구현 완료 상태

6. **도메인별 설계 및 작업 계획서 (Architecture & Execution Plans)**:
   - **액티브 스프린트 (Priority 1 & 2)**:
     - **[`LARGE_JOIN_OPTIMIZATION_PLAN.ko.md`](LARGE_JOIN_OPTIMIZATION_PLAN.ko.md)**: 대규모 조인(10+ Tables) 최적화 설계 및 구현 계획서 (베이스 테이블 동적 루트 선정 및 공집합 조기 탈락, OPT-LARGE-JOIN-1).
     - **[`PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md`](PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md)**: 플래너 슬림화 및 순수 AST 전환 설계 계획서 (Sort-Merge Join 지연 평가 및 정규식 폴백 옵션 격리, PLANNER-SLIM-1 & PLANNER-AST-1).
     - **[`SQLLOGICTEST_INDUSTRY_PRACTICES_AND_SCOPE.ko.md`](SQLLOGICTEST_INDUSTRY_PRACTICES_AND_SCOPE.ko.md)**: 데이터베이스 엔진 업계(DuckDB, CockroachDB, TiDB, DataFusion 등) SQLLogicTest 실전 적용 기준, 3단계 계층화(Tier 1~3) 및 권장 범위 표준 가이드.
     - **[`DURABILITY_VERIFICATION_PLAN.ko.md`](DURABILITY_VERIFICATION_PLAN.ko.md)**: 킬 테스트(크래시 복구), 소크 테스트(장시간 부하), 개밥 먹기 기반 내구성/운영 신뢰성 검증 계획서.
     - **[`APP_INTEGRATION_EXPANSION_PLAN.ko.md`](APP_INTEGRATION_EXPANSION_PLAN.ko.md)**: 실전 앱 연동 확장 마스터 플랜 (Answer, Vaultwarden, Metabase, Umami, NocoDB, Mattermost 6종).
     - **[`WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md`](WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md)**: Woodpecker CI 및 Vikunja 연동 검증 보고서 (재귀 CTE, 복합 OR 인덱스 분기, DML 커맨드 태그 보정 완료).
     - **[`ROADMAP_REORGANIZATION_PLAN.ko.md`](ROADMAP_REORGANIZATION_PLAN.ko.md)**: LionSQL 전역 로드맵(ROADMAP.md) 재정비 및 Phase 7/품질 게이트 확장 설계 계획서 (DOC-ROADMAP-REORG-1).
     - **[`TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`](TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md)**: 10개 실전 서비스 호환 규칙 Table-Driven 정식화 설계 계획서 (COMPAT-TABLE-DRIVEN-1).
     - **[`AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md)**: 심볼 테이블 및 AST 기반 구조적 쿼리 라우팅 리팩토링 설계 및 검증 보고서 (문자열 휴리스틱 45개 전면 제거, 527ns 실측, 완료).
   - **Post-1.0 아키텍처 및 장기 로드맵 계획서**:
     - **[`IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)**: In-Memory OCC 성능 최적화 역사, WiredTiger 초격차 추월, 104만 tpmC 아키텍처 및 Post-1.0 메모리 5배 압축 로드맵 (OCC-HYPER-2).
     - **[`SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md)**: TDS 네트워크 소켓 수신부터 VDBE 및 WAL 파일 시스템 사전 할당/적응형 그룹 커밋에 이르는 서버 수직 파이프라인 종단 최적화 마스터 플랜 (Phase 3/4 대기).
     - **[`COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)**: LionSQL 복합 쿼리 성능 튜닝을 위한 2단계 하이브리드 표준 벤치마크 플랜 (SSB Star Schema & Mini TPC-H 5대 대표 쿼리, DuckDB/ClickHouse 비교 매트릭스).
     - **[`DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md`](DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md)**: DuckDB 스타일의 친화적 SQL 손끝 편의 문법(`SELECT * EXCLUDE`, `FROM <table>;`, `GROUP BY ALL`) 도입 마스터 플랜 (Post-1.0 / v1.1.0).
     - **[`EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md`](EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md)**: `WHERE col IN ()` 빈 괄호 평가 허용 여부에 대한 역사적 배경, 아키텍처 분석 및 옵션 플래그 배제 원칙 계획서 (Post-1.0 대기).
    - **[`BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md`](BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md)**: AWS Babelfish 기반 T-SQL Stored Procedure 전용 SLT 로직 테스트 스위트 구축 계획서 (1.0 릴리즈 게이트).
    - **[`BABELFISH_INTEGRATION_REPORT.ko.md`](BABELFISH_INTEGRATION_REPORT.ko.md)**: Babelfish T-SQL 2,900-Suite 도입, 엣지 케이스 장애 추적(우여곡절), 코어 버그 수정 및 종합 호환성 검증 보고서.
    - **[`MODULAR_ARCHITECTURE_PLAN.ko.md`](MODULAR_ARCHITECTURE_PLAN.ko.md)**: 모듈러 아키텍처 및 세션 캡슐화 리팩토링 계획서.
     - **[`SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)**: 내장 SPA 정적 웹 파일 서빙 엔진 설계 계획서.
     - **[`IMMUTABLE_AUDIT_LOG_PLAN.md`](IMMUTABLE_AUDIT_LOG_PLAN.md)**: PCI-DSS v4.0 / SOC 2 규제 대응 SHA-256 해시 체이닝 불변 감사 로그 서브시스템 설계 (Post-1.0 / v1.1.0).
     - **[`COLLATION_AND_LOCALE_PLAN.md`](COLLATION_AND_LOCALE_PLAN.md)**: Microsoft SQL Server Collation 및 로케일 지원 설계 (Post-1.0 / v1.1.0).
     - **[`S3_PRESIGNED_STORAGE_PLAN.md`](S3_PRESIGNED_STORAGE_PLAN.md)**: 클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 연동 설계 (Spec-Out 아키텍처 / 참고).

7. **마일스톤 실전 검증 보고서 및 포스트모텀 (Integration Reports & Postmortems)**:
   - **[`SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md`](SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md)** ([English](SLT_FULL_SUITE_100_PERCENT_REPORT.md)): SQLLogicTest (SLT) 107개 풀세트(19,211 문장, 551,217 쿼리) 100% 무결점 통과 실측 보고서 및 8대 심층 엔진 결함(3VL, 별칭/슬롯 보존, 중첩 집계 등) 완치 기록.
   - **[`METABASE_INTEGRATION_REPORT.ko.md`](METABASE_INTEGRATION_REPORT.ko.md)**: Metabase BI 듀얼 프로토콜(PGWire :5433 & TDS :1433) 연동, 스키마 인트로스펙션, MBQL 비주얼 쿼리 빌더 다중 테이블 파생 조인, COUNT/SUM 집계, 시계열 분석 및 Native SQL 100% 무오류 검증 보고서.
   - **[`VAULTWARDEN_INTEGRATION_REPORT.ko.md`](VAULTWARDEN_INTEGRATION_REPORT.ko.md)**: Vaultwarden (Rust + Diesel ORM) 47개 마이그레이션 완주, 복합 PK upsert, 8바이트 Timestamp 인코딩 및 Bitwarden REST API E2E 100% 검증 보고서.
   - **[`ANSWER_INTEGRATION_REPORT.ko.md`](ANSWER_INTEGRATION_REPORT.ko.md)**: Apache Answer Q&A 플랫폼 35개 테이블 생성, VDBE 다중 행 배치 Identity 키 자동 발급 및 REST API E2E 검증 보고서.
   - **[`GITEA_INTEGRATION_REPORT.ko.md`](GITEA_INTEGRATION_REPORT.ko.md)**: Gitea 풀스택 Git 플랫폼 듀얼 프로토콜(TDS :1433 & PGWire :5433) 연동 및 116개 테이블 실전 E2E 검증 완료 보고서.
   - **[`IOTA_ERP_INTEGRATION_REPORT.ko.md`](IOTA_ERP_INTEGRATION_REPORT.ko.md)**: IOTA SDK Go ERP 연동, `pgx/v5` 엄격한 바이너리 프로토콜 완비 및 47개 스키마 순차 마이그레이션 100% 관통 검증 보고서.
   - **[`GRAFANA_INTEGRATION_REPORT.ko.md`](GRAFANA_INTEGRATION_REPORT.ko.md)**: Grafana 785개 마이그레이션 완주, 봉투 암호화(`BYTEA`), CTE 파생 테이블 격리 및 듀얼 데이터 소스 쿼리 검증 보고서.
   - **[`CASDOOR_INTEGRATION_REPORT.ko.md`](CASDOOR_INTEGRATION_REPORT.ko.md)**: Casdoor 44개 테이블 자동 생성, 복합 PK(`PRIMARY KEY (owner, name)`) 지원, TDS `VARBINARY` JSON 직렬화 및 듀얼 프로토콜 OIDC/RBAC E2E 검증 보고서.
   - **[`SOAK_TEST_1H_VERIFICATION_REPORT.ko.md`](SOAK_TEST_1H_VERIFICATION_REPORT.ko.md)**: 1시간 장시간 부하(Soak Test, 72만 ops / 39.3만 커밋) 실측 검증, `SQLSTATE 40001` 충돌 매핑 및 WiredTiger 500MB 캐시 수렴 분석 보고서.
   - **[`ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md)**: Gitea XORM 실전 연동 중 노출된 DML/와일드카드/키워드 별칭 결함 원인 분석 및 "인간 작성 SQL vs ORM 기계 생성 SQL" 테스트 사각지대 영구 사후 분석 보고서 (Postmortem).
   - **[`PERF_REGRESSION_RECOVERY_AND_GATE_PLAN.ko.md`](PERF_REGRESSION_RECOVERY_AND_GATE_PLAN.ko.md)**: 키 래치 및 슬립 재시도 완전 제거를 통한 108k/160k TPS 성능 회귀 정상화 및 단위 테스트 내 경량 성능 회귀 방지 게이트 설계 계획서 (`PERF-REGRESSION-RECOVERY-1`).
   - **[`GROVE_HOTSPOT_POSTMORTEM_AND_ASSETS.ko.md`](GROVE_HOTSPOT_POSTMORTEM_AND_ASSETS.ko.md)**: Grove 핫스팟 최적화 과정에서 도출된 핵심 가치 및 4대 영구 자산(VDBE 슬롯 인덱싱, OCC vs WT 엔진별 역할 분담, 락 컨보이/OS 타이머 퀀텀 실체 규명, 회귀 방지 게이트) 심층 포스트모텀.
   - **[`TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md`](TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md)**: TDS RPC `sp_executesql` 파라미터 해석 리그레션 분석, Beekeeper Studio 접속 크래시 원인 규명 및 순수 위치 기반(Ordinal Positional) 폴백 복원 포스트모텀.
   - **[`QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md)**: 영구 불변 2-Tier 쿼리 실행 엔진 아키텍처 확정 명세서.
   - **[`BUG_ARCHIVE.ko.md`](../BUG_ARCHIVE.ko.md)**: 데이터 무결성 107대 결함 조치 아카이브 및 중간 방법론 이력 (`BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1`).
   - **[`TEST_VERIFICATION_REPORT.ko.md`](TEST_VERIFICATION_REPORT.ko.md)**: 단위 및 통합 테스트 스위트 회귀 검증 및 무경쟁(Race-Free) 진단 보고서.
   - **[`RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md)**: 1.0 릴리스 대비 아키텍처 감사 및 사전 피드백 이력.
   - **[`LIONSQL_DEEP_CODE_REVIEW.ko.md`](LIONSQL_DEEP_CODE_REVIEW.ko.md)**: 전체 390개 소스 파일(약 35만 라인)에 대한 아키텍처, 서브시스템별 심층 분석 및 엔지니어링 코드 리뷰 보고서.

8. **역사적 아카이브 (`notes/archive/`)**:
   - 100% 구현 완료되어 설계 의도 및 검증 이력을 영구 보존하기 위해 이동된 과거 13개 계획서(Windows 데몬, 오버스펙 제거, Forward-only 커서, 단일 CLI 통합 등) 및 원본 백로그는 **[`notes/archive/README.md`](archive/README.md)**를 참조하십시오.
