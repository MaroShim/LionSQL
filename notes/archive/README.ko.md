# 역사적 아카이브 디렉토리 (`notes/archive/`)

> - **최초 작성일**: 2026-10-31
> - **최종 갱신일**: 2026-11-06
> [English Version](README.md)

이 디렉토리는 100% 구현 완료되어 설계 의도, 엔지니어링 맥락, 결함 치료 이력 및 사후 감사를 위해 보존된 **과거의 아키텍처 계획서, 완료된 백로그, 릴리스 노트 및 심층 보고서**를 영구 보관하는 역사적 아카이브입니다.

---

## 1. 완료된 핵심 결함 및 백로그 아카이브 (Historical Records)

1. **[`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md)** (127KB)
   - 데이터 무결성 107대 결함 조치 완료 아카이브 (`BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1`).
2. **[`TODO_SPEC_ARCHIVE.ko.md`](TODO_SPEC_ARCHIVE.ko.md)**
   - 초기 엔지니어링 과제 및 서브시스템별 구현 완료 스펙 아카이브.
3. **[`TODO.ko.md`](TODO.ko.md)** (42KB)
   - 최상위 경량 대시보드(`TODO.md`)로 이전되기 전의 초기 한글 종합 운영 백로그.
4. **[`PITFALLS_AND_TRICKS.ko.md`](PITFALLS_AND_TRICKS.ko.md)** (22KB)
   - 영문 치트시트(`notes/PITFALLS_AND_TRICKS.md`)로 정제되기 전의 상세 서술형 지뢰밭 지도.
5. **[`PROJECT_STATUS_ASSESSMENT.ko.md`](PROJECT_STATUS_ASSESSMENT.ko.md)**
   - 0.90.0 이전의 초기 아키텍처 성숙도 및 기술 부채 청산 진단 보고서.

---

## 2. 100% 구현 완료된 아키텍처 및 실행 계획서 (Completed Plans)

*실제 구현 및 테스트가 100% 완료되어 코어 엔진에 흡수된 설계 명세서들입니다:*

### 2.1. 코어 엔진 및 CLI 아키텍처
1. **[`UNIFIED_CLI_PLAN.md`](UNIFIED_CLI_PLAN.md)**: 단일 정적 바이너리(`bin/lionsql`) 라이프사이클 통합 및 서브커맨드 체계화.
2. **[`WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md`](WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md)**: Windows 서비스 데몬 분리, 1ms 고해상도 타이머(`timeBeginPeriod`) 및 크로스 컴파일 파이프라인.
3. **[`OVER_SPECIFICATION_REMEDIATION_PLAN.md`](OVER_SPECIFICATION_REMEDIATION_PLAN.md)**: 도구별 오버스펙 및 애드혹 하드코딩 전면 제거.
4. **[`TYPE_GUESSING_ANTI_PATTERN_ELIMINATION_PLAN.ko.md`](TYPE_GUESSING_ANTI_PATTERN_ELIMINATION_PLAN.ko.md)**: 휴리스틱 타입 추측 안티패턴 완전 퇴출 및 엄격한 정적 타이핑 확립.
5. **[`SQL_ABSTRACTION_LAYER_PLAN.md`](SQL_ABSTRACTION_LAYER_PLAN.md)**: In-Memory OCC와 WiredTiger를 일원화하는 스토리지 추상화 레이어 구축.
6. **[`ANTLR_PARSER_COMPACTION_PLAN.md`](ANTLR_PARSER_COMPACTION_PLAN.md)**: ANTLR4 T-SQL 문법 통합, 단일 파일 컴파일 및 메모리 압축.

### 2.2. SQL 문법 및 쿼리 실행
7. **[`FORWARD_ONLY_CURSOR_COMPATIBILITY_PLAN.ko.md`](FORWARD_ONLY_CURSOR_COMPATIBILITY_PLAN.ko.md)**: T-SQL Forward-only 커서 구현 및 상태머신 바이트코드 실행.
8. **[`MYSQL_SYNTAX_ERGONOMICS_COMPATIBILITY_PLAN.ko.md`](MYSQL_SYNTAX_ERGONOMICS_COMPATIBILITY_PLAN.ko.md)**: Phase 1 MySQL 편의 문법(`DESC`, `SHOW TABLES`, 백틱) 지원 및 Spec-Out 경계 확립.
9. **[`WEB_STUDIO_CONCURRENCY_AND_SESSION_ISOLATION_PLAN.ko.md`](WEB_STUDIO_CONCURRENCY_AND_SESSION_ISOLATION_PLAN.ko.md)**: 내장 Web Studio 브라우저 세션 격리 및 동시성 제어.
10. **[`COMPLEX_EXPRESSION_AUDIT_AND_DEEP_TEST_PLAN.ko.md`](COMPLEX_EXPRESSION_AUDIT_AND_DEEP_TEST_PLAN.ko.md)**: 중첩 수식 감사 및 Volcano 연산자 값 단언 검증.

### 2.3. SQLLogicTest (SLT) 55만 쿼리 완주 로드맵 계획서
11. **[`SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md`](SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md)**: SLT 107개 풀세트 듀얼 엔진 인증 계획서.
12. **[`SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md`](SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md)**: SQLLogicTest 3단계 점진 확장 로드맵.
13. **[`SLT_EXTENDED_SUITE_FIXES_PLAN.ko.md`](SLT_EXTENDED_SUITE_FIXES_PLAN.ko.md)**: 확장 스위트 결함 치료 계획서.
14. **[`SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md`](SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md)**: SLT 사용자 가이드 및 풀세트 구축 계획서.

### 2.4. 테스트베드 및 툴 감사
15. **[`EXTENDED_SMOKE_TEST_PLAN.md`](EXTENDED_SMOKE_TEST_PLAN.md)**: 13개 도메인 확장 통합 스모크 테스트 스위트.
16. **[`TOOL_AUDIT_PLAN.ko.md`](TOOL_AUDIT_PLAN.ko.md)**: SSMS, DBeaver, DbGate, Beekeeper 쿼리 호환성 및 카탈로그 감사.
17. **[`BENCHMARK_DIR_SAFETY_PLAN.md`](BENCHMARK_DIR_SAFETY_PLAN.md)**: 비파괴적 벤치마크 디렉토리 격리 및 데이터 보호 아키텍처.
18. **[`GA_VALIDATION_HANDOFF_PLAN.ko.md`](GA_VALIDATION_HANDOFF_PLAN.ko.md)**: 0.90 GA 검증 및 핸드오버 계획서.

---

## 3. 심층 감사 및 과거 릴리스 노트 (Reviews & Release Notes)

1. **[`LIONSQL_DEEP_CODE_REVIEW.ko.md`](LIONSQL_DEEP_CODE_REVIEW.ko.md)**
   - 전체 390개 소스 파일(약 35만 라인)에 대한 서브시스템별 심층 코드 리뷰 보고서.
2. **[`TEST_VERIFICATION_REPORT.ko.md`](TEST_VERIFICATION_REPORT.ko.md)**
   - 단위 및 통합 테스트 스위트 회귀 검증 및 무경쟁(Race-Free) 진단 보고서.
3. **[`RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md)**
   - 1.0 릴리스 대비 아키텍처 감사 및 사전 피드백 이력.
4. **버전별 베타 릴리스 노트**:
   - `v0.90.0-beta0` ~ `beta4` 릴리스 노트 원본 보존 (`.ko.md` 및 `.md`).
