# LionSQL Engineering Notes & Context Handover (`notes/`)

> [English Version](README.md)

이 디렉토리는 다른 개발 환경(다른 머신, 새 세션)으로 이동했을 때, 새로운 AI 에이전트나 동료 엔지니어가 **코드와 공식 문서(`doc/`)만으로는 파악하기 힘든 개발 이면의 맥락, 기각된 대안(Negative Knowledge), 실전 주의사항(Pitfalls), 사용자의 엔지니어링 철학**을 즉시 동기화받을 수 있도록 정리한 영속적 지식 베이스(Shared Brain & Handover Notes)입니다.

---

## 문서 색인 (Index)

1. **[`ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)** (한국어)
   - **핵심 아키텍처 불변식(Core Invariants)**: 왜 이 방식을 택했고 무엇을 의도적으로 배제(Spec-Out)했는가
   - **개발 철학 및 협업 규약**: 무조건 준수해야 하는 엔지니어링 원칙 (3단계 검증 파이프라인, 이모지 배제, KISS/YAGNI, 파일 분할 기준)
   - **기술 스택 및 주요 컴포넌트**: VDBE 바이트코드 VM, 듀얼 파서, 듀얼 트랙 UDF, 멀티 프로토콜

2. **[`PITFALLS_AND_TRICKS.ko.md`](PITFALLS_AND_TRICKS.ko.md)** (한국어)
   - **코드베이스 내 암초 지도(Landmines & Fragile Spots)**: 섣불리 건드리면 깨지기 쉬운 미묘한 포인트들
   - **순환 참조 방지 SPI 패턴**: `pkg/proc`과 ANTLR4 폴백 파서 간 디커플링
   - **샌드박스 및 네트워크 격리 이슈**: 테스트 환경에서의 로컬 루프백 소켓/HTTP 주의사항
   - **원자적 파일 I/O 및 데이터 타입 직렬화**: 크래시 방지 파일 저장 규칙과 Decimal JSON 무결성

3. **[`TAXONOMY.ko.md`](TAXONOMY.ko.md)** (한국어)
   - **과제 식별 코드 체계 일람표**: `P1~P3`(우선순위), `P-1~P-5`(성능), `A~D`(CBO 9대 과제), `S-1~S-7`(문법 완비) 종합 정리

4. **[`PROTOCOL_EMULATION_BOUNDARIES.md`](PROTOCOL_EMULATION_BOUNDARIES.md)**
   - **다중 프로토콜 에뮬레이션 경계 및 스코프**: TDS, PGWire, MySQL 프로토콜 에뮬레이션 범위 및 중단 기준, 패킷/토큰 처리 및 가상 No-Op 호환성 정책

5. **프로젝트 전역 로드맵 및 백로그**:
   - **[`TODO.ko.md`](../TODO.ko.md)**: 액티브 엔지니어링 백로그, 차기 착수 우선순위 과제(P1, P2, P3), CBO 및 성능 매트릭스, 최근 완료 마일스톤
   - **[`ROADMAP.md`](../ROADMAP.md)**: 거시적 릴리즈 마일스톤(Phase 1~6), 아키텍처 진화 방향, 단계별 구현 완료 상태

6. **도메인별 설계 및 작업 계획서 (Architecture & Execution Plans)**:
   - **[`UNIFIED_CLI_PLAN.md`](UNIFIED_CLI_PLAN.md)**: 단일 바이너리(`bin/lionsql`) 완전 통합 및 CLI 라이프사이클 재정립 마스터 플랜 (완료)
   - **[`DOCUMENTATION_CLEANUP_PLAN.md`](DOCUMENTATION_CLEANUP_PLAN.md)**: v0.90.0 기능 동결 4단계 기술 문서 전수 정돈 마스터 플랜 (완료)
   - **[`IMMUTABLE_AUDIT_LOG_PLAN.md`](IMMUTABLE_AUDIT_LOG_PLAN.md)**: PCI-DSS v4.0 / SOC 2 규제 대응 SHA-256 해시 체이닝 불변 감사 로그 서브시스템 설계
   - **[`S3_PRESIGNED_STORAGE_PLAN.md`](S3_PRESIGNED_STORAGE_PLAN.md)**: 클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 연동 설계
   - **[`COLLATION_AND_LOCALE_PLAN.md`](COLLATION_AND_LOCALE_PLAN.md)**: Microsoft SQL Server Collation 및 로케일 지원 설계
   - **[`SQL_ABSTRACTION_LAYER_PLAN.md`](SQL_ABSTRACTION_LAYER_PLAN.md)**: 다중 스토리지 백엔드 통합을 위한 SQL 추상화 계층 설계
   - **[`ANTLR_PARSER_COMPACTION_PLAN.md`](ANTLR_PARSER_COMPACTION_PLAN.md)**: ANTLR 파서 바이너리 및 런타임 메모리 경량화 플랜
   - **[`OVER_SPECIFICATION_REMEDIATION_PLAN.md`](OVER_SPECIFICATION_REMEDIATION_PLAN.md)**: 서드파티 도구 종속적 오버스펙 및 하드코딩 전수 정돈 마스터 플랜 (진행 중)

7. **완료된 보고서 및 역사적 아카이브 (Completed Reports & Archives)**:
   - **[`TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md`](TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md)**: TDS RPC `sp_executesql` 파라미터 해석 리그레션 분석, Beekeeper Studio 접속 크래시 원인 규명 및 순수 위치 기반(Ordinal Positional) 폴백 복원 포스트모텀.
   - **[`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md)**: 데이터 무결성 107대 결함 조치 아카이브 및 중간 방법론 이력 (`BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1`).
   - **[`TEST_VERIFICATION_REPORT.ko.md`](TEST_VERIFICATION_REPORT.ko.md)**: 단위 및 통합 테스트 스위트 회귀 검증 및 무경쟁(Race-Free) 진단 보고서.
   - **[`RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md)**: 1.0 릴리스 대비 아키텍처 감사 및 사전 피드백 이력.
