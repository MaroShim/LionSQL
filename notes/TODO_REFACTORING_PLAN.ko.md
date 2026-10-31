# LionSQL TODO 대시보드 구조 개선 계획서 (`TODO_REFACTORING_PLAN.ko.md`)

> - **최초 작성일**: 2026-10-31
> - **참조 모델**: `Grove/TODO.md` (Active Priority ➔ Future Roadmap ➔ Completed Archive 3단 구조)
> - **목적**: 현재 `TODO.md`에 완료된 항목(`[x]`)과 미완료 항목(`[ ]`, `[-]`)이 뒤섞여 시인성이 떨어지는 문제를 해결하고, "지금 당장 해야 할 일(Active)"과 "향후 로드맵(Future)", "완료된 마일스톤(Archive)"을 명확히 분리하여 운영 생산성을 극대화함.

---

## 1. 현재 LionSQL `TODO.md`의 문제점 분석

1. **완료 항목(`[x]`)과 활성 작업의 혼재**:
   - Section 3.1(`v0.90.0 Active Implementation Queue`)에 적힌 18개 항목 중 **18개 전량(100%)이 이미 완료(`[x]`)** 상태인데, 여전히 상단 "Active Queue" 테이블에 거대한 분량으로 남아 있어 실제 남아 있는 할 일을 파악하기 어렵습니다.
2. **다양한 영역의 분산**:
   - 실제 지금 해야 할 일(예: `DURA-KILL-1`, `DURA-SOAK-1`, `INTEG-ANSWER-1` 등)은 Section 3.3, 3.4 등 문서 하단으로 밀려나 있어 스크롤을 한참 내려야 보입니다.
3. **Grove 모델과의 차이점**:
   - `Grove/TODO.md`는 **상단에 현재 스프린트 및 핵심 우선순위(`Priority 1~3`)의 미완료 작업(`[ ]`)들만 직관적인 체크리스트로 집중 배치**하고,
   - 이미 끝난 거대한 작업 내역은 문서 하단의 **`Completed Milestones Archive`**로 깔끔하게 묶어 관리함으로써 첫 화면에서 "지금 당장 해야 할 작업"이 1초 만에 눈에 들어옵니다.

---

## 2. 개편 후 새로운 LionSQL `TODO.md` 구조 (Grove 스타일 3단 구성)

```markdown
# LionSQL Task Backlog & Execution Roadmap (`TODO.md`)

[헤더: 핵심 아키텍처 문서 링크 & 엔지니어링 원칙]

---

## Priority 1. 신뢰성 및 내구성 자동화 검증 (Active Sprint: Durability & Soak)
> 집중 과제: DURA-KILL-1, DURA-SOAK-1, DURA-DOGFOOD-1
- [ ] 1.1 `DURA-KILL-1` kill -9 크래시 복구 장기 라운드 검증 (500 ~ 5,000 라운드)
- [ ] 1.2 `DURA-SOAK-1` 24h ~ 72h 장기 소크 테스트 (In-Memory OCC 및 WiredTiger)
- [ ] 1.3 `DURA-DOGFOOD-1` 실서비스 섀도 운영 (Vaultwarden, Umami 등)

---

## Priority 2. 실전 오픈소스 앱 연동 확장 (Application Integration Expansion)
> 마스터 플랜: notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md
- [ ] 2.1 `INTEG-ANSWER-1` Apache Answer (Q&A) PGWire 연동 (1순위, 상세 계획 완료)
- [ ] 2.2 `INTEG-VAULTWARDEN-1` Vaultwarden (비밀번호 관리) Rust+Diesel PGWire 연동 (2순위)
- [ ] 2.3 `INTEG-METABASE-1` Metabase (BI) JVM+JDBC 듀얼 연동 (3순위)
- [ ] 2.4 `INTEG-UMAMI-1` Umami (웹 애널리틱스) Next.js+Prisma PGWire 연동 (4순위)
- [ ] 2.5 `INTEG-NOCODB-1` NocoDB (노코드 DB) Node.js+Knex 듀얼 연동 (5순위)
- [ ] 2.6 `INTEG-MATTERMOST-1` Mattermost (협업 채팅) Go+sqlx 연동 (6순위)

---

## Priority 3. Post-0.90.0 (`v1.1.0`) 아키텍처 로드맵 (Future Engine Roadmap)
> 대상 릴리즈: v1.1.0 (엔진 고도화 및 쿼리 최적화)
- [ ] 3.1 `PERF-COMPLEX-QUERY-2` Complex Query Flat Hash Join & Direct Slot Projection
- [ ] 3.2 `BENCH-TPCH-1` Mini TPC-H Benchmark Suite & CBO 서브쿼리 Decorrelation
- [ ] 3.3 `OCC-HYPER-2` In-Memory OCC Compact Arena Memory Compaction (5배 고밀도)
- [ ] 3.4 `PERF-SERVER-PIPELINE-P3-P4` 서버 파이프라인 Phase 3/4
- [ ] 3.5 `FRIENDLY-SQL` DuckDB-Style Friendly SQL 문법 지원
- [ ] 3.6 `WEB-SPA-1` 단일 바이너리 내장 SPA 웹 서버 호스팅
- [ ] 3.7 `R-6` 모듈 인터페이스 추상화 및 경계 디커플링
- [ ] 3.8 `TDS-MOD-1` TDS 모놀리스 분할 리팩토링

---

## 4. Architectural Invariants & Spec-Out Invariants (13대 불변 원칙)
(기존 13대 영구 배제 항목 보존)

---

## Completed Milestones Archive (완료된 작업 이력)

### [x] M1. v0.90.0 Feature Freeze & Production Hardening (100% PASS)
- Gate 1~5 전체 통과 요약
- Core Tracks 요약 (Wire, OCC Vertical, 2-Tier CBO, Daemon/Lifecycle, Web Studio)

### [x] M2. v0.90.0 액티브 스프린트 완료 과제 (18개 전량 완료)
- [x] `PERF-PGWIRE-PREP-TX`: PGWire PreparedStatement 트랜잭션 내 VDBE 바이트코드 직접 바인딩
- [x] `CASDOOR-INTEGRATION-1`: Casdoor IAM/SSO 듀얼 프로토콜 연동
- [x] `GRAFANA-INTEGRATION-1`: Grafana 듀얼 프로토콜 연동
- [x] `IOTA-ERP-1`: iota-sdk Go ERP PGWire 연동
- [x] `GITEA-PGWIRE-1` & `GITEA-MOCK-SEED-1`: Gitea 프로덕션 연동
- [x] `BENCH-TPCC-RPC` & `PERF-TPCC-CLIENT`: TPC-C 네트워크 벤치마크 및 클라이언트 최적화
- [x] `PERF-SERVER-PIPELINE-P1-P2`: TDS 파이프라인 최적화
- [x] `PERF-WIRE-SOCKET-POOL` & `PERF-FASTPATH-PING`: 와이어 소켓 풀링 및 핑 패스트패스
- [x] `ORM-MIGRATE-1~3`: ORM 조건부 DDL 및 가상 카탈로그 호환성
- [x] `S-MYSQL-1-P1` & `FUNC-DIALECT-7`: MySQL 문법 및 베스트 7 빌트인 함수
- [x] `PROC-CURSOR-1`: T-SQL Fast Forward-Only 커서
- [x] `AUDIT-EXPR-1` & `PERF-COMPLEX-QUERY-1`: 복합 쿼리 연산자 튜닝
- [x] `BENCH-SSB-1`: Star Schema Benchmark (SSB) 스위트

### [x] M3. 내구성 및 신뢰성 1차 검증 완료 이력
- [x] `DURA-SOAK-F1`: WiredTiger write conflict -> SQLSTATE 40001 매핑
- [x] `DURA-SOAK-F2`: 1시간 소크 실측 및 캐시 상한 기준선 확립
- [x] `DURA-KILL-1 (Phase 1)`: kill -9 크래시 복구 도구 구현 및 20라운드/엔진 검증 통과
```

---

## 3. 기대 효과

1. **1초 시인성 확보**:
   - `TODO.md`를 열었을 때 거대한 `[x]` 테이블에 묻히지 않고, **"지금 해야 할 일(신뢰성 장기 검증 + Answer 연동)"**이 상단에 바로 보입니다.
2. **Grove와의 일관된 프로젝트 관리 경험**:
   - `Grove`와 `SQLion` 양쪽 리포지토리 모두 동일한 `Priority 1 ~ 3 ➔ Invariants ➔ Completed Archive` 구조를 갖추어 컨텍스트 스위칭 비용이 제로가 됩니다.
3. **이력 보존(No Data Loss)**:
   - 지금까지 이룩한 18개 스프린트 완료 과제와 커밋 태그, 벤치마크 성과가 사라지지 않고 `Completed Milestones Archive`에 체계적으로 보존됩니다.
