# LionSQL 전역 로드맵(ROADMAP.md) 재정비 실행 계획서 (`notes/ROADMAP_REORGANIZATION_PLAN.ko.md`)

> - **최종 갱신일**: 2026-11-01
> - **최초 작성일**: 2026-11-01
> - **과제 식별 코드**: `DOC-ROADMAP-REORG-1`
> - **상태**: 구현 및 반영 완료 (Completed) — `ROADMAP.md` 및 `TODO.md` 동기화 완료
> - **목표**: `v0.90.0` Feature Freeze 이후 축적된 대규모 성과(10대 실전 프로덕션 앱 생태계 연동, kill -9 크래시 복구 오라클, 장기 소크 부하 테스트, AST 심볼 라우팅, Table-Driven 호환성 정식화)를 전략적 로드맵에 완전 동기화하고, `v1.0.0 LTS` 정식 출시를 위한 품질 게이트 및 포스트 1.0 방향성을 일관되게 재정립합니다.

---

## 1. 개편 추진 배경 및 현황 진단

### 1.1 현재 `ROADMAP.md`의 한계와 괴리
1. **최근 스프린트 대규모 성과 누락**:
   - `ROADMAP.md`의 최신 마일스톤은 `v0.90.0` Betas(0~3)에 머물러 있습니다.
   - 현재 코드베이스는 `0.90.0-beta4`이며, 그 사이에 **10대 실전 프로덕션 오픈소스 앱 무수정 연동(Gitea ~ Vikunja)**, **`crashtest`(`kill -9` 4대 무결성 오라클 D1~D4)**, **`soaktest`(장기 안정성 및 누수 0 검증)**, **AST 심볼 기반 쿼리 라우팅 리팩토링** 등 제품의 신뢰성을 결정짓는 핵심 엔지니어링이 완수되었으나 로드맵에 공식 반영되지 않았습니다.
2. **`v1.0.0 LTS` 출시 품질 게이트의 모호성**:
   - 기존의 5대 게이트(Gate 1~5)는 드라이버 호환성과 TPC-C 중심이어서, 실제 소프트웨어가 구동되는 "실전 앱 연동성"과 "하드 크래시 무결성"이 공식 게이트 지표로 명시되지 않았습니다.
3. **포스트 1.0 과제와 Spec-Out 경계 최신화 필요**:
   - `COMPAT-TABLE-DRIVEN-1`과 향후 과제(NocoDB, Mattermost 등)의 거시적 위치 정렬이 필요합니다.

---

## 2. 4대 핵심 재정비 축 (Reorganization Pillars)

### Pillar 1: 마일스톤 단계(Phase Progression)의 현실적 재정렬
로드맵 1장의 진화 다이어그램 및 마일스톤 테이블을 다음과 같이 현실화합니다:

- **기존 단계**: Phase 1 ~ 5 (v0.1.0 ~ v0.24.0, 완료) ➔ Phase 6 (v0.90.0 Feature Freeze, 완료)
- **신규 분기 (Phase 7 - Active Hardening)**:
  - **`Phase 7: Real-World Ecosystem & Autonomous Durability Hardening` (`v0.90.0-beta4` ~ `v0.95.0`)** [현재 진행/완료 중]:
    1. **10대 실전 프로덕션 앱 무수정 연동 (Ecosystem Interop)**:
       - 개발/인프라: Gitea (Git), Woodpecker CI (CI/CD)
       - 보안/인증: Vaultwarden (비밀번호), Casdoor (SSO/IAM)
       - 협업/업무: Vikunja (칸반), AFFiNE (위키/노션), Apache Answer (Q&A)
       - 데이터/콘텐츠: Ghost (CMS), Metabase (BI, 듀얼 프로토콜), Umami (웹 분석)
       - Go XORM, Rust Diesel, Node.js Prisma/Knex, JVM JDBC 등 전 언어 ORM/드라이버 실전 제압.
    2. **내구성 및 운영 신뢰성 오라클 하네스 (Durability Verification)**:
       - `cmd/crashtest` (`DURA-KILL-1`): `kill -9` 강제 크래시 복구 D1~D4 오라클, DOUBLE_KILL, TORN_WAL_TAIL 변형 무결성 실증.
       - `cmd/soaktest` (`DURA-SOAK-1`): 연속 장기 부하 환경에서 FD, RSS, p99, 디스크 불변성 및 메모리 누수 0 검증.
    3. **구조적 쿼리 라우팅 정규화 (Structural AST Routing)**:
       - 45개 정규식/문자열 휴리스틱 완전 삭제 및 C typedef 심볼 테이블 기반 527ns 고속 디스패치 (`ROUTING-AST-CATALOG-1`).
    4. **호환성 테이블 정식화 (Table-Driven Formalization)**:
       - 엔진/와이어 case-by-case 분기문의 Go 정적 테이블 디스패치화 (`COMPAT-TABLE-DRIVEN-1`).
- **Phase 8: Official 1.0 LTS Production Release (`v1.0.0 LTS`)** [Ready for Tagging]:
  - 6대 프로덕션 품질 게이트 최종 검증 통과 및 LTS 릴리즈 선언.
- **Phase 9: Post-1.0 Horizons (`v1.1.0+` - `v1.2.0+`)** [Planned]:
  - Pure-Go 네이티브 영속 스토리지 엔진(`CGO_ENABLED=0`), Wasm UDF 샌드박스, 불변 감사 로그(`pkg/audit`), 복합 쿼리 해시 조인 및 미니 TPC-H.

---

### Pillar 2: 1.0 LTS 품질 게이트(Quality Gates) 6대 축 확장

기존 5대 게이트를 6대 게이트로 정밀화하여 신뢰성 기준을 완성합니다:

```
                      [ LionSQL v1.0.0 LTS 6대 품질 게이트 ]
                                         │
    ┌─────────────┬─────────────┬────────┴────────┬─────────────┬─────────────┐
    ▼             ▼             ▼                 ▼             ▼             ▼
[ Gate 1 ]    [ Gate 2 ]    [ Gate 3 ]        [ Gate 4 ]    [ Gate 5 ]    [ Gate 6 ]
Multi-Wire    ACID Concur   Durability &      Ecosystem     Enterprise    Autonomous
Driver Cert   & Zero-Race   Crash Recovery    Conformance   Security      Ops & Soak
(100% PASS)   (20k+ TPS)    (kill -9 D1~D4)   (10 Apps OK)  (TDE & RBAC)  (24h Soak OK)
```

1. **Gate 1: Multi-Protocol & Driver Conformance**: Go, Python, Java JDBC, C ODBC, SSMS, DBeaver 100% PASS.
2. **Gate 2: ACID Concurrency & Zero Data Race**: `go test -race ./...` 25개 패키지 100% PASS, TPC-C 20k+ TPS.
3. **Gate 3: Durability & Crash Recovery Oracle (D1~D4)**: `cmd/crashtest` 기반 `kill -9`, Torn WAL, 재크래시 환경에서 커밋 데이터 손실 0건 보장.
4. **Gate 4: Production Ecosystem Conformance (10 Apps)**: 10대 실전 프로덕션 오픈소스 앱 무수정(Zero Code Change) 완벽 구동.
5. **Gate 5: Enterprise Security & Object Governance**: TDE 데이터 암호화, SQL RBAC, HTTP JWT/API-Key 토큰 격리.
6. **Gate 6: Autonomous Tooling & Long-Term Soak Stability**: 단일 바이너리 마이그레이션/백업 CLI 및 장시간 연속 부하(`soaktest`) 시 FD/메모리 누수 0 입증.

---

### Pillar 3: 거시 전략 산출물 매트릭스(Master Deliverable Matrix) 전면 동기화

로드맵 6장의 총괄 표에 누락된 최근 핵심 마일스톤들을 일괄 등재합니다:

| 마일스톤 | 산출물 / 과제 | 도메인 | 대상 릴리즈 | 상태 | 기술 문서 링크 |
| :---: | :--- | :---: | :---: | :---: | :--- |
| **Phase 7** | 10대 실전 프로덕션 앱 생태계 연동 (Gitea ~ Vikunja) | Ecosystem | `v0.95.0` | **Completed** | [`notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md`](APP_INTEGRATION_EXPANSION_PLAN.ko.md) |
| **Phase 7** | `kill -9` 크래시 복구 내구성 오라클 하네스 (`DURA-KILL-1`) | Storage & WAL | `v0.95.0` | **Completed** | [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](DURABILITY_VERIFICATION_PLAN.ko.md) |
| **Phase 7** | 24h 장기 소크 부하 안정성 및 자원 유계 검증 (`DURA-SOAK-1`) | Quality & Soak | `v0.95.0` | **Completed** | [`notes/SOAK_TEST_1H_VERIFICATION_REPORT.ko.md`](../reports/SOAK_TEST_1H_VERIFICATION_REPORT.ko.md) |
| **Phase 7** | AST 심볼 테이블 기반 쿼리 라우팅 리팩토링 (`ROUTING-AST-CATALOG-1`) | Query Routing | `v0.95.0` | **Completed** | [`notes/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md) |
| **Phase 7** | 호환성 규칙 Table-Driven 정식화 (`COMPAT-TABLE-DRIVEN-1`) | Compatibility | `v0.95.0` | **In Progress** | [`notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`](TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md) |
| **Phase 7** | In-Memory OCC 104만 tpmC 초격차 성능 최적화 | Storage Engine | `v0.95.0` | **Completed** | [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md) |
| **Phase 8** | 공식 LionSQL v1.0.0 LTS 출시 및 6대 게이트 최종 사인오프 | Release | `v1.0.0 LTS` | **Ready for Sign-off** | [`CHANGELOG.md`](../../CHANGELOG.md) |
| **Post-1.0** | Mini TPC-H 벤치마크 및 CBO 서브쿼리 Decorrelation | Query Planner | `v1.1.0` | **Planned** | [`notes/COMPLEX_QUERY_OPTIMIZATION_PLAN.ko.md`](COMPLEX_QUERY_OPTIMIZATION_PLAN.ko.md) |
| **Post-1.0** | In-Memory OCC 컴팩트 아레나 메모리 압축 (`OCC-HYPER-2`) | Storage Engine | `v1.1.0` | **Planned** | [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md) |

---

### Pillar 4: 명시적 Spec-Out(영구 배제) 영역 최신화

엔진 순수성과 초경량성을 지키기 위한 아키텍처 불변식 결정사항을 최신 버전으로 갱신:
- 기존 항목: In-Kernel PL/pgSQL 인터프리터, In-Kernel OAuth2 소셜 로그인, S3 Blob/이미지 리사이징, In-Memory Dead-Tuple 백큠 스레드, 외부 2PC 코디네이터.
- 추가 항목:
  - **MySQL 전용 DML(`ON DUPLICATE KEY UPDATE`) 및 레거시 8컬럼 Tabular EXPLAIN (`S-MYSQL-1-P2-P3`)**: 
    - PostgreSQL의 `ON CONFLICT DO UPDATE` 및 표준 `MERGE`가 이미 완비되어 있어 MySQL DML은 불필요한 3중 충돌 경로를 양산함.
    - 레거시 8컬럼 테이블 형식은 현대적 계층 트리 CBO 플랜을 파괴하므로 배제하고, 표준 인덴트 텍스트 및 JSON 플랜으로 단일화.

---

## 3. 실행 단계 및 작업 분할

1. **Step 1: 계획서 검토 및 정렬 승인**: 본 계획서(`ROADMAP_REORGANIZATION_PLAN.ko.md`)에 대한 사용자 의견 수렴 및 확정.
2. **Step 2: `ROADMAP.md` 본문 전면 개편**:
   - 1장 진화 다이어그램 및 마일스톤 테이블에 Phase 7 반영.
   - 2장 1.0 LTS 품질 게이트를 6대 게이트(실전 10개 앱 + 내구성 크래시/소크 오라클)로 보강.
   - 3장에 Phase 7 세부 완료 내역 신설.
   - 4장 Spec-Out 테이블에 MySQL DML/Tabular EXPLAIN 추가.
   - 5장 Post-1.0 로드맵 정리.
   - 6장 전략적 산출물 매트릭스 최신화.
3. **Step 3: 문서 간 정합성 검증**:
   - `TODO.md`, `README.md`, `notes/README.ko.md`, `ROADMAP.md` 간 버전 및 링크 정합성 최종 점검.
4. **Step 4: 완료 커밋**:
   - 순수 문서 커밋이므로 불필요한 빌드/테스트를 건너뛰고 Conventional Commits 규격으로 즉시 커밋.
