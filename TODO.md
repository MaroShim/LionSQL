# LionSQL Task Backlog & Execution Roadmap (`TODO.md`)

This dashboard tracks active engineering sprint tasks, domain backlog matrices, and milestone completion histories for LionSQL.

> **Core Architecture & Knowledge Base**:
> - Strategic Roadmap: [`ROADMAP.md`](ROADMAP.md)
> - Defect Resolution Archive (107 cases): [`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md)
> - Core Invariants & Engineering Context: [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md)
> - App Integration Expansion Plan: [`notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md`](notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md)
> - Durability & Reliability Verification Plan: [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](notes/DURABILITY_VERIFICATION_PLAN.ko.md)
> - Taxonomy & Task Identifier Definitions: [`notes/TAXONOMY.ko.md`](notes/TAXONOMY.ko.md)
>
> **Engineering Principles**: 3-Stage Verification Pipeline (Build ➔ Test `-race` ➔ Commit), Zero Decorative Emojis, Radical KISS & Deletion-First.

---

## Priority 1. 신뢰성 및 내구성 자동화 검증 (Active Sprint: Durability & Reliability)

> 설계 및 실측 문서: [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](notes/DURABILITY_VERIFICATION_PLAN.ko.md)  
> 목표: 실제 프로세스 강제 종료(`kill -9`)와 장시간 부하(`soak`) 환경에서 커밋 데이터 손실 0건 및 자원 유계를 입증합니다.

### [-] 1.1 `DURA-KILL-1` kill -9 크래시 복구 장기 라운드 검증 (진행 중)
- [x] **도구 구현 (`cmd/crashtest`)**: 다중 워커 계좌 이체 + SHA-256 커밋 장부(`crash_ledger`) + 4단계 정합성 오라클(D1 커밋 내구성, D2 원자성 `SUM(balance)=200,000`, D3 체크섬 일관성, D4 복구 가능성) 구현
- [x] **변형 시나리오 구현**: `SIGKILL`, `SIGKILL+DOUBLE_KILL`(복구 중 재크래시 멱등성), `SIGKILL+TORN_WAL_TAIL`(로그 꼬리 절단 손상 복구), `SIGTERM` 대조군
- [x] **크래시 결함 수정 4건**:
  1. WiredTiger 유저 버퍼 유실 방지(`transaction_sync`)
  2. `CREATE TABLE` 제약 조건 접두사 충돌(`checksum` 누락 방지)
  3. PGWire 텍스트 파라미터 숫자 오인 방지
  4. WiredTiger 세션 간 커서 비동기 해제 동시성 충돌(`go_wt_cursor_close` SIGSEGV) 완치 및 `force=true` 안전 드롭
- [x] **100라운드 누적 완주**: WiredTiger 50/50 PASS (`170,477` tx 복구) & In-Memory OCC 50/50 PASS (`170,457` tx 복구) — 총 `340,934`건 트랜잭션 100% 무손실 복구 및 위반 0건 입증
- [ ] **초장기 라운드 누적 검증**: 500 ~ 5,000 라운드 장기 크래시 복구 무결성 검증

### [-] 1.2 `DURA-SOAK-1` 24h ~ 72h 장기 소크 테스트 (진행 중)
- [x] **소크 테스트 도구 구현 (`cmd/soaktest`)**: 8 워커 계좌 이체 + Prometheus 지표 수집 + RSS/FD/디스크/p99 추이 자동 판정 엔진
- [x] **WiredTiger 스모크 완주**:
  - `719,970` ops 완주, 커밋 장부 일치(`393,523` 행), 잔액 불변(`SUM(balance)=1,000,000`, 위반 0건)
  - `open_fds = 28` 완전 고정 (핸들 누수 0), `p99 = 6.23ms` 성능 열화 0
  - WiredTiger 500MB 캐시 워밍업 분석 완료 및 `/metrics` Go 힙/고루틴 수집 반영
- [x] **In-Memory OCC 스모크 완주 및 스캔 스냅샷 정합성 완치**:
  - `Scan()` 실행 중 개별 샤드 락 해제에 따른 찢긴 읽기(torn read) 결함 발굴 및 `snapshotRowsAndKeys` 카노니컬 오름차순 샤드 RLock 스냅샷 도입으로 완치
  - 소크 테스트 완주: `16,452` commits 일치, 잔액 불변(`SUM(balance)=1,000,000`, 위반 0건), RSS 비율 1.00, FD 비율 1.04, 고루틴 비율 1.00 (자원 누수 0)
- [ ] **24시간 ~ 72시간 장기 소크 완주**: 장기 무중단 운영을 통한 극한의 메모리/고루틴/디스크 누수 0 검증

### [ ] 1.3 `DURA-DOGFOOD-1` 실서비스 섀도 운영 (Dogfooding)
- [ ] 1단계: 개인용 서비스(Vaultwarden / Umami / Answer)를 LionSQL에 연결해 섀도 모드 운영
- [ ] 2단계: 프로젝트 자체 서비스(Gitea 등)로 확대 및 일 단위 백업/복원 리허설

### [x] 1.4 `DURA-SLT-1` SQLLogicTest (SLT) 블랙박스 정합성 오라클 검증 — **[완료 (Pass)]**
- 설계 계획서: [`notes/SQLLOGICTEST_INTEGRATION_PLAN.ko.md`](notes/SQLLOGICTEST_INTEGRATION_PLAN.ko.md)
- [x] **SLT 러너 구현 (`cmd/sqllogictest`, `pkg/slt`)**: 순수 Go 기반 표준 SLT 파일 파서, In-Process 및 PGWire 듀얼 모드, rowsort/valuesort/nosort 정렬 및 MD5 해시 대조 엔진
- [x] **Phase 1 (코어 로직 & 구문 스위트)**: `select1.slt`, `where.slt`, `join.slt`, `null.slt`, `aggregate.slt` (100% PASS)
- [x] **Phase 2 (화이트스페이스 & 엣지 케이스 스위트)**: 탭/개행/다중 공백(`whitespace.slt`), 키워드 식별자 충돌 및 리터럴 포함(`boundary.slt`), 복합 키워드 변형(`IF EXISTS`, `WITHIN GROUP`) (100% PASS)
- [x] **Phase 2.5 (핵심 영역 고난도 스위트 1차~6차 확장)**: 조건 분기 및 단락 평가(`case_expr.slt`), 다중 컬럼/NULL/집계 고립(`distinct.slt`), IN/EXISTS/스칼라 서브쿼리(`subquery.slt`), 윈도우 함수(`window.slt`), 집합 연산(`setops.slt`), 내장 스칼라 함수(`functions.slt`), 복합 DML 변이(`dml_mutate.slt`), 범위 조건(`between.slt`), 리스트 조건(`in_list.slt`), 패턴 매칭(`like.slt`), 그룹 필터링(`group_having.slt`), 페이지네이션(`limit_offset.slt`), 공통 테이블 식(`cte.slt`), 사칙연산 우선순위/모듈로(`arithmetic.slt`), 타입 캐스팅(`cast_type.slt`), 3치 논리 및 드모르간 법칙(`boolean_3vl.slt`), 다중/셀프/안티/크로스 조인(`join_advanced.slt`), JSON 연산자(`json_ops.slt`), 다중 정렬 및 Upsert(`order_upsert.slt`), 중첩 대수식 스트레스(`expr_stress.slt`), 복합 집계 및 `COALESCE(SUM(...), 0)` 스트레스(`agg_stress.slt`), 파생 테이블 다중 조인(`derived_join.slt`), 트랜잭션 롤백 격리(`tx_isolation.slt`), 복합 3VL 불리언 논리(`boolean_logic_stress.slt`), 대규모 IN 및 NOT IN NULL 함정(`in_subquery_stress.slt`), 문자열 연결 및 이스케이프(`datetime_string_stress.slt`) (총 33개 파일, 240문장, 181쿼리 — `memory`/`wiredtiger` × `inproc`/`pgwire` 4대 매트릭스 전수 100% PASS)
- [x] **엔진 버그 16건 발굴 및 완치**:
  1. `HasPrefixKeyword` 다중 공백/탭/개행 DDL 라우팅 버그 완치
  2. SQL 3-valued logic `NULL = NULL` 및 `val = NULL` UNKNOWN(falsy) 표준 정합성 완치
  3. `BinaryExpr.String()` 연산자 우선순위 부모 괄호 탈락 (`(A OR B) AND C`) 및 우측 결합성(`a - (b + c)`) deparser 완치
  4. SLT 러너 멀티컬럼 공백 포함 셀 파서 탭/길이 정합성 완치
  5. `FunctionCallExpr` 내 `DISTINCT` 파싱 누락으로 인한 `COUNT(DISTINCT ...)` 평가 결함 완치
  6. `BetweenExpr` (`NOT BETWEEN`) 평가 시 `NULL` 피연산자가 `true`로 오판되던 SQL 3VL 결함 완치
  7. `InExpr` (`NOT IN`) 평가 시 `NULL` 대상 컬럼 및 리스트 내 `NULL` 원소(`x NOT IN (100, NULL)`)의 SQL 3VL UNKNOWN 정합성 완치
  8. `%` 모듈로 연산자(`TokenPercent`) 렉서 토큰화, Pratt 파서 우선순위(`precProduct`), AST deparser(`%`) 및 `EvalRowExpr` 평가 지원 누락 완치
  9. `UnaryExpr.String()` 내 `BinaryExpr` 괄호 보존(`NOT (x = 10)`, `-(a + b)`), `TokenNot` 파서 우선순위(`precNot`), 단항/논리 연산자(`NOT NULL`, `-NULL`, `FALSE AND NULL`, `TRUE OR NULL`) SQL 3VL 정합성 완치
  10. Self-Join(`slt_emp2 e JOIN slt_emp2 m`) 시 동일 컬럼 별칭(`e.emp_name`, `m.emp_name`)이 `Tuple.ToMap()` 변환 중 덮어써지던 스키마 별칭 충돌 결함 완치
  11. `CROSS JOIN` (`ON` 절 없는 카티션 곱) 파서 허용 및 `HashJoinIterator` 빈 키 매칭 기반 카티션 곱 실행 완치
  12. `GROUP BY` 프로젝션 내 복합 집계 표현식(`COALESCE(SUM(o.amount), 0)`, `SUM(a) + SUM(b)`) 실행 시 내부 중첩 집계 함수를 누적하지 않고 첫 행만 참조하던 `AggregateIterator` 2-Pass 평가 결함 완치
  13. `||` 문자열 연결 연산자(`TokenConcat`) 렉서 토큰화 및 Pratt 파서, Volcano/Row 수식 평가자 지원 완치
  14. `NOT IN (SELECT ...)` 서브쿼리 내 `NULL` 키가 1개라도 포함된 경우 SQL 3VL UNKNOWN 규칙에 의해 모든 행이 suppress되어야 함에도 행이 반환되던 `HashJoinIterator` ANTI 조인 결함 완치
  15. 프로젝션 내 스칼라 서브쿼리의 집계 함수(`(SELECT avg(c) FROM t1)`)가 외부 쿼리를 전역 집계 쿼리로 오인시켜 1행만 반환되던 CBO 플래너 `hasTopLevelAggregateExpr` 결함 완치
  16. `ORDER BY 1, 2` 등 서수(Ordinal Position) 정렬 지정 시 프로젝션 이전에 실행되어 테이블 원본 컬럼으로 정렬되거나 무시되던 CBO 플래너 및 `SortIterator` 서수 인덱스 평가 결함 완치

### [-] 1.5 `DURA-SLT-2` SQLLogicTest 1.0 릴리즈 풀 스위트 (60,000+ 쿼리) 전수 인증 (진행 중)
- 설계 계획서: [`notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md`](notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md)
- 확장 로드맵: [`notes/SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md`](notes/SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md)
- 업계 적용 및 권장 범위: [`notes/SQLLOGICTEST_INDUSTRY_PRACTICES_AND_SCOPE.ko.md`](notes/SQLLOGICTEST_INDUSTRY_PRACTICES_AND_SCOPE.ko.md)
- [x] **병렬 워커 풀(`-parallel N`) 및 4대 매트릭스 러너 완성**: `cmd/sqllogictest`에 `-engine wiredtiger` 디스크 격리 엔진, `-mode pgwire` 실제 PostgreSQL v3 TCP 소켓 실행기(`PGWireExecutor`), `-parallel N` 고루틴 워커 풀 구현 완료
- [x] **Phase 1 (고위험 핵심 도메인 33개 스위트, 181개 쿼리 전수 인증)**: 다중 중첩 사칙연산, `COALESCE`+`NULLIF` 복합 대수식, 3VL 드모르간 항등식, `LEFT JOIN`+`GROUP BY`+`COALESCE(SUM(...), 0)`, 트랜잭션 격리, 복합 불리언 논리, 대규모 IN 및 NOT IN NULL 함정 검증 100% 완료
- [x] **Phase 2 (SQLite 공식 `select1.test` ~ `select5.test` 10,706 쿼리 전수 100% PASS)**:
  - `select1.test`: 1,031 쿼리 (2.29s)
  - `select2.test`: 1,031 쿼리 (2.18s)
  - `select3.test`: 3,351 쿼리 (5.88s)
  - `select4.test`: 3,857 쿼리 (6.36s)
  - `select5.test`: 1,436 쿼리 (5.67s, 최대 64개 테이블 콤마 조인)
  - 총 10,706개 쿼리 전수 100% 무오류 인증 통과 (소요 시간 22.69s, 0 data races).
  - BFS 기반 Spanning Tree 조인 키 추론(`pushdown.go`) 및 Greedy Join Ordering 필터 우선순위화(`planner.go`)로 최대 64개 테이블 조인 카테시안 폭발 완치.
- [ ] **Phase 3 (SQLite 공식 `index` / `random` / `evidence` 스위트 확장)**: 인덱스 복합 스캔, 난수 대수식, 통계적 카디널리티 스트레스 테스트 연동
- [ ] **Phase 6 (1.0 릴리즈 게이트 통합)**: 1.0 릴리즈 직전 풀 스위트 듀얼 엔진 100% 무오류 인증

---

## Priority 2. 실전 오픈소스 앱 연동 확장 (Application Integration Expansion)

> 마스터 플랜: [`notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md`](notes/APP_INTEGRATION_EXPANSION_PLAN.ko.md)  
> 방식: 각 앱의 실제 마이그레이션 및 실운영 쿼리를 통과시키며 커널 호환성을 검증합니다. (계획 순서 준수)

### [x] 2.1 `INTEG-ANSWER-1` Apache Answer (Q&A 지식베이스) — **[완료 (Pass)]**
- 연동 보고서: [`notes/ANSWER_INTEGRATION_REPORT.ko.md`](notes/ANSWER_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`doc/ANSWER_INTEGRATION_GUIDE.ko.md`](doc/ANSWER_INTEGRATION_GUIDE.ko.md) / [`doc/ANSWER_INTEGRATION_GUIDE.md`](doc/ANSWER_INTEGRATION_GUIDE.md)
- 스택: Go + XORM / PGWire (`TCP :5433`)
- 35개 테이블 스키마 자동 생성, VDBE 다중 행 배치 Identity 키 자동 발급, 관리자 로그인, 질문/답변 E2E 100% 무오류 완료 (Answer 소스 수정 0줄)

### [x] 2.2 `INTEG-VAULTWARDEN-1` Vaultwarden (비밀번호 관리) — **[완료 (Pass)]**
- 연동 계획서: [`notes/VAULTWARDEN_INTEGRATION_PLAN.ko.md`](notes/VAULTWARDEN_INTEGRATION_PLAN.ko.md)
- 연동 보고서: [`notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md`](notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`doc/VAULTWARDEN_INTEGRATION_GUIDE.ko.md`](doc/VAULTWARDEN_INTEGRATION_GUIDE.ko.md) / [`doc/VAULTWARDEN_INTEGRATION_GUIDE.md`](doc/VAULTWARDEN_INTEGRATION_GUIDE.md)
- 회귀 테스트: `pkg/engine/vaultwarden_compat_test.go`
- 스택: Rust + Diesel ORM / PGWire (`TCP :5433`)
- 47개 Diesel ORM 마이그레이션 100% 자동 완주, 29개 테이블 생성, 복합 기본 키 `ON CONFLICT DO UPDATE`, 8바이트 Timestamp OID 1114 인코딩, KDF/JWT/비밀번호 사이퍼 CRUD/동기화 E2E 무오류 통과 (Vaultwarden 소스 수정 0줄)

### [x] 2.3 `ROUTING-AST-CATALOG-1` 심볼 테이블 & AST 기반 쿼리 라우팅 리팩토링 — **[완료 (Pass)]**
- 계획서: [`notes/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](notes/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md)
- 회귀 테스트: `pkg/wire/routing_ast_test.go`, `pkg/proc/characteristics_test.go`
- 45개의 `strings.Contains`/`regexp` 문자열 휴리스틱 완전 삭제 완료
- C 언어 typedef 심볼 테이블 방식의 AST 특성 추출기(`proc.ExtractQueryCharacteristics`) 및 카탈로그 심볼 조회(`virtual.IsVirtualTable`) 도입
- `canCompileToVDBE`, `isVirtualCatalogQuery`, `virtual.IsVirtualCatalogJoinQuery`, `resolveColumnOIDs` 구조적 정상화
- False Positive (유사 테이블명 오인) 및 False Negative (리터럴/주석 키워드로 인한 VDBE 차단) 100% 원천 해결

### [x] 2.4 `INTEG-METABASE-1` Metabase (BI 대시보드) — **[완료 (Pass)]**
- 연동 계획서: [`notes/METABASE_INTEGRATION_PLAN.ko.md`](notes/METABASE_INTEGRATION_PLAN.ko.md)
- 연동 보고서: [`notes/METABASE_INTEGRATION_REPORT.ko.md`](notes/METABASE_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`doc/METABASE_INTEGRATION_GUIDE.ko.md`](doc/METABASE_INTEGRATION_GUIDE.ko.md) / [`doc/METABASE_INTEGRATION_GUIDE.md`](doc/METABASE_INTEGRATION_GUIDE.md)
- 회귀 테스트: `pkg/engine/metabase_compat_test.go`
- 스택: Clojure/JVM + JDBC (`pgjdbc`, `mssql-jdbc`) / 듀얼 프로토콜 (PGWire + TDS)
- 데이터 소스 등록(PGWire `:5433` & TDS `:1433`) 100% 무오류 통과, 스키마 인트로스펙션(테이블/컬럼/외래키/인덱스 350ms 내 완료), 비주얼 쿼리 빌더(MBQL 다중 테이블 파생 조인, COUNT/SUM 집계, `date_trunc` 시계열) 및 Native SQL 완벽 동작 검증 (Metabase 순정 컨테이너 소스 수정 0줄)

### [x] 2.5 `INTEG-UMAMI-1` Umami (웹 애널리틱스) — **[완료]**
- 상세 보고서: [`notes/UMAMI_INTEGRATION_REPORT.ko.md`](notes/UMAMI_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`doc/UMAMI_INTEGRATION_GUIDE.ko.md`](doc/UMAMI_INTEGRATION_GUIDE.ko.md) / [`doc/UMAMI_INTEGRATION_GUIDE.md`](doc/UMAMI_INTEGRATION_GUIDE.md)
- 단위 및 회귀 테스트: `pkg/engine/umami_compat_test.go` (`TestUmami_PrismaCanConnectQuery`, `TestUmami_PrismaTableNamespaceQuery`, `TestUmami_CountDerivedTableQuery`)
- 스택: Next.js 16.4.2 + Prisma ORM / PGWire (`TCP :5433`)
- Prisma 초기화 26개 마이그레이션(01~26) 100% 무오류 적용, 관리자 인증(`POST /api/auth/login`), 웹사이트 등록(`POST /api/websites`), 웹사이트 목록 조회(`GET /api/websites` 파생 테이블 집계), 웹 분석 이벤트 수집(`POST /api/send`), 통계 분석 쿼리(`GET /api/websites/{id}/stats` 복합 서브쿼리, GROUP BY 1, 2, HAVING, CASE WHEN) 완벽 검증 통과 (Umami 순정 컨테이너 소스 수정 0줄)

### [x] 2.6 `INTEG-WOODPECKER-1` Woodpecker CI (CI/CD 엔진) — **[완료 (Pass)]**
- 설계 계획서: [`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`](notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md)
- 통합 보고서: [`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md`](notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`doc/WOODPECKER_INTEGRATION_GUIDE.ko.md`](doc/WOODPECKER_INTEGRATION_GUIDE.ko.md) / [`doc/WOODPECKER_INTEGRATION_GUIDE.md`](doc/WOODPECKER_INTEGRATION_GUIDE.md)
- 회귀 테스트: `pkg/engine/woodpecker_compat_test.go`
- 스택: Go + XORM / PGWire (`TCP :5433`)
- 19개 테이블, 32개 XORM 마이그레이션 100% 무오류 자동 적용, 서버 헬스체크(`/healthz` 204), 버전 조회(`/version` v3.20.1), 웹 UI 서빙 완벽 동작 검증 (Woodpecker CI 순정 컨테이너 소스 수정 0줄)

### [x] 2.7 `INTEG-VIKUNJA-1` Vikunja (프로젝트 칸반 & To-Do) — **[완료 (Pass)]**
- 설계 계획서: [`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`](notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md)
- 통합 보고서: [`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md`](notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`doc/VIKUNJA_INTEGRATION_GUIDE.ko.md`](doc/VIKUNJA_INTEGRATION_GUIDE.ko.md) / [`doc/VIKUNJA_INTEGRATION_GUIDE.md`](doc/VIKUNJA_INTEGRATION_GUIDE.md)
- 회귀 테스트: `pkg/engine/vikunja_compat_test.go`
- 스택: Go + XORM / PGWire (`TCP :5433`)
- 80개 이상의 XORM 마이그레이션 100% 무오류 자동 적용, 사용자 회원가입(`POST /api/v1/register`), JWT 로그인(`POST /api/v1/login`, 복합 `OR` 술어 처리), 프로젝트 및 4대 뷰(List/Gantt/Table/Kanban) 생성(`PUT /api/v1/projects`), 칸반 태스크 생성 및 상태 변경(`PUT /api/v1/projects/:id/tasks`, `POST /api/v1/tasks/:id`), 재귀 CTE(`WITH RECURSIVE`) 프로젝트 계층 탐색, 웹 UI 서빙 완벽 동작 검증 (Vikunja 순정 컨테이너 소스 수정 0줄)

### [x] 2.8 `COMPAT-TABLE-DRIVEN-1` 10개 실전 서비스 호환 규칙 Table-Driven 구조 및 안티패턴 키워드 매칭 전면 정식화 — **[완료 (Pass)]**
- 설계 계획서: [`notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`](notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md)
- 회귀 테스트: `pkg/wire/param_rules_test.go`, `pkg/wire/command_tags_test.go`, `pkg/engine/*_compat_test.go`, `pkg/engine/pk_bypass_test.go`, `pkg/planner/planner_test.go`
- 대상 서비스: 10개 실전 서비스 (Gitea, Vaultwarden, Casdoor, Ghost, Answer, Affine, Metabase, Umami, Woodpecker, Vikunja)
- 엔진(`pkg/engine`), 와이어(`pkg/wire`), 가상 카탈로그(`pkg/catalog/virtual`), 플래너(`pkg/planner`)에 흩어진 case-by-case 분기문 및 ad-hoc 원시 문자열 매칭을 Go 정적 구조체 테이블 및 `sqlutil` SSOT 토크나이저 원형으로 전면 정식화 완료:
  1. `pkg/wire/param_rules.go`: 파라미터 OID 추론 규칙 테이블 (`defaultParamOIDRules`, `LookupParamOID`)
  2. `pkg/wire/command_tags.go`: PGWire VDBE DML 커맨드 완료 태그 테이블 (`defaultCommandTagRules`, `formatPgCommandComplete`)
  3. `pkg/catalog/virtual/sys_tables.go`: 가상 카탈로그 뷰 매핑 테이블 (`sysViewAliases`, `IsSysCatalogTable`), 기존 40줄 `switch norm` 제거
  4. `pkg/planner/bounds.go`: `HasDisjunction` 공통 술어를 통한 복합 `OR` 단일 인덱스 색인 방어 일원화
  5. 엔진/플래너/와이어 전반의 나이브 `strings.Contains`/`strings.Index` 키워드 매칭 안티패턴 전수 감사 및 `sqlutil.ContainsKeyword`/`FindTopLevelKeyword` 대체 완료 (PK 바이패스, DDL `IF [NOT] EXISTS`, DML `OUTPUT`/`RETURNING`, 윈도우 `OVER`/`BETWEEN...AND`, 서브쿼리 `NOT EXISTS`/`NOT IN`, `IS [NOT] NULL`)
- 10개 실전 앱 회귀 테스트 100% 통과, 벤치마크 18.7만 TPS 유지 확인, 전체 25개 패키지 race detector 0 data race 검증 완료

### [ ] 2.9 `COMPAT-DECLARATIVE-1` 외부 앱 호환성 규칙 선언적 외재화 (Declarative Compatibility Engine) — **[백로그 (2단계)]**
- 대상: `COMPAT-TABLE-DRIVEN-1`에서 정식화된 Go 구조체 테이블
- 목표: 정식화된 구조체 테이블을 기반으로 필요 시 외부 텍스트 설정 파일(YAML/JSON 또는 embed.FS)로 분리하여 런타임 동적 규칙 주입 가능성 검토 (YAGNI 평가 후 결정)

### [ ] 2.10 `INTEG-NOCODB-1` NocoDB (노코드 DB 스튜디오) — **[다음 순위]**
- 스택: Node.js + Knex / 듀얼 프로토콜 (PGWire + TDS)
- 메타데이터 자동 감지 및 스프레드시트 뷰 CRUD 검증

### [ ] 2.11 `INTEG-MATTERMOST-1` Mattermost (팀 협업 채팅) — **[5순위]**
- 스택: Go + `sqlx`/`squirrel` / PGWire (`TCP :5433`)
- 대규모 팀 채널 및 메시지 인덱싱 검증

---

## Priority 3. Post-0.90.0 (`v1.1.0`) 아키텍처 로드맵 (Future Engine Roadmap)

> 대상 릴리즈: `v1.1.0` (엔진 최적화 및 프로토콜 리팩토링)

### [ ] 3.1 `PERF-COMPLEX-QUERY-2` Complex Query Flat Hash Join & Direct Slot Projection
- 오픈 어드레싱 정수 해시 테이블 기반 복합 쿼리 서브 50ms 목표 최적화 ([`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md))

### [ ] 3.2 `BENCH-TPCH-1` Mini TPC-H Benchmark Suite & CBO 서브쿼리 Decorrelation
- TPC-H Q1, Q3, Q4, Q6, Q9 스위트 및 `EXISTS`/`IN` -> Semi-Join 자동 변환 ([`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md))

### [ ] 3.3 `OCC-HYPER-2` In-Memory OCC Compact Arena Memory Compaction
- 인메모리 컴팩트 아레나 메모리 압축을 통한 5배 고밀도화 ([`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md))

### [ ] 3.4 `PERF-SERVER-PIPELINE-P3-P4` 서버 파이프라인 Phase 3/4
- Generic LRU 커서 및 ReadOnly Auto-Bypass, 적응형 그룹 커밋 ([`notes/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](notes/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md))

### [ ] 3.5 `FRIENDLY-SQL` DuckDB-Style Friendly SQL 문법 지원
- `SELECT * EXCLUDE`, `FROM <table>;`, `GROUP BY ALL`, CSV 파일 직접 조회 ([`notes/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md`](notes/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md))

### [ ] 3.6 `WEB-SPA-1` 단일 바이너리 내장 SPA 웹 서버 호스팅
- `--app`, `--public` 플래그를 통한 SPA 정적 웹 호스팅 및 네이티브 DB 서빙 ([`notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md))

### [-] 3.7 `R-6` 세션 캡슐화 및 모듈 디커플링 (Lean 3단계 리팩토링)
- 상세 계획: [`notes/MODULAR_ARCHITECTURE_PLAN.ko.md`](notes/MODULAR_ARCHITECTURE_PLAN.ko.md)
- [x] **Phase 1 (`R-6-P1`)**: `ClientSession` 내부 락 및 상태 은닉 (와이어의 `cs.Mu.Lock()` 직접 침범 제거, `ExecuteProgram` 세션 일원화)
- [ ] **Phase 2 (`TDS-MOD-1`)**: TDS 모놀리스(`server.go` 2.9k 라인) 4개 파일 분할 및 와이어 구조체 내 `storage`/`catalog` 직접 참조 제거 (Post-0.90.0 대기)
- [x] **Phase 3 (`R-6-P3`)**: 서버 기동 시 `Coordinator` 최상위 인스턴스화 및 3대 리스너(PG, TDS, HTTP) 대등 주입 (역할 전도 해소)

### [ ] 3.8 `ARCH-VDBE-ROUTING-1` AST 및 카탈로그 기반 2-Tier 쿼리 라우팅 정규화 (Anti-Heuristic Refactoring)
- **목표**: `pkg/wire`의 문자열 매칭(`strings.Contains`) 휴리스틱을 완전 제거하고, AST 노드와 카탈로그 SSOT 기반의 견고한 역량 검증으로 전환
- **세부 작업**:
  - `proc.CanCompileAST(stmt)` 도입: AST 레벨에서 `RETURNING`, `ON CONFLICT` 등 VDBE 미지원 구문 O(1) 구조체 필드로 판별
  - 가상 카탈로그 뷰 판별을 `catalog.IsVirtualTable(name)` SSOT로 일원화 (원시 문자열 키워드 목록 하드코딩 제거)
  - `ClientSession.Dialect` enum (`DialectTSQL`, `DialectPostgres`) 도입하여 `strings.HasPrefix(cs.ID, "pg-")` 문자열 검사 및 암묵적 플래그 제거
  - `pkg/proc`의 순수성 유지 (카탈로그와의 순환 참조 방지를 위해 함수/인터페이스 주입 방식 준수)

### [ ] 3.9 기타 엔터프라이즈 기능 확장
- [ ] `AUDIT-LOG-1`: 암호화 SHA-256 불변 감사 로그 서브시스템 ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md))
- [ ] `TDS-COMPAT-6`: Multiple Result Sets (MARS) 스트리밍 (Dapper `QueryMultiple`)
- [ ] `S3-1` ~ `S3-4`: 클라이언트 사이드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 ([`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md))
- [ ] `LOC-1` ~ `LOC-4`: MSSQL Collation 및 로케일 정렬 지원 ([`notes/COLLATION_AND_LOCALE_PLAN.md`](notes/COLLATION_AND_LOCALE_PLAN.md))
- [ ] `SEC-1` / `AUTH-1`: TDS 8.0 Pre-Login SSL/TLS 핸드셰이크 강화 및 클라이언트 사이드 OIDC 토큰 교환

---

## 4. Architectural Invariants & Spec-Out Invariants (13대 불변 원칙)

To preserve sub-microsecond latency and single-binary simplicity, the following features are permanently **Spec-Out** (detailed rationales preserved in [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md) and [`GEMINI.md`](GEMINI.md)):

1. **Duplicate PL/pgSQL Interpreter**: Stored procedures unify on native T-SQL VDBE VM (`pkg/proc`).
2. **Legacy XML DML / XQuery**: Standardized on modern JSON functions (`ISJSON`, `JSON_VALUE`) and native Go parsers.
3. **In-Memory OCC Background Vacuum**: Workspace atomic swap/discard leaves zero dead tuples; vacuum workers rejected.
4. **POSIX Shared Memory (Arrow IPC) Snapshots**: Standardized on CDC/WAL event streams.
5. **Distributed 2PC Coordinators (XA / MS DTC)**: Standardized on 1-Writer Multi-Reader WAL replication.
6. **In-Kernel OAuth2 Web Flows**: Standardized on Client-Side OIDC Token Exchange.
7. **In-Kernel Blob File Storage**: Standardized on S3 pre-signed direct client uploads.
8. **Immature Pure-Go Storage Engines**: Focused exclusively on WiredTiger MVCC and In-Memory OCC.
9. **In-Kernel Slow-Query Webhooks**: Standardized on Prometheus `/metrics` and OpenTelemetry OTLP.
10. **In-Kernel Wasm Sandbox**: Standardized on zero-allocation Go Native UDF SPI (`RegisterScalarFunction`).
11. **Total Bytecode Translation for Complex Queries**: 2-Tier query engine (Tier 1 VDBE, Tier 2 Volcano CBO) invariant.
12. **Pure Columnar Vectorization Engine Full Rewrite**: Rejected due to Row-storage I/O scan physical limits and single-binary KISS code complexity; standardized on 1,024-row chunk streaming (`ChunkedIterator`, `PullChunk`) and zero-unboxing slot optimizations.
13. **MySQL-Specific DML & Legacy Tabular EXPLAIN Extensions (`ON DUPLICATE KEY UPDATE`, Comma `LIMIT offset, count`, 8-Column Tabular `EXPLAIN`)**: Permanently Spec-Out. Standard ANSI/MSSQL `MERGE` and PostgreSQL `ON CONFLICT DO UPDATE` already provide full functional coverage without 3-way transactional branching. Flat 8-column tabular `EXPLAIN` flattens and destroys modern hierarchical Volcano CBO plan trees (which even MySQL 8.0 deprecated in favor of `FORMAT=TREE`) and breaks PostgreSQL GUI visualizers. LionSQL standardizes exclusively on modern indented tree text and JSON plan representations, keeping MySQL syntax support strictly at Phase 1 ergonomics (`DESC`, `SHOW TABLES`, backticks).

---

## Completed Milestones Archive (완료된 작업 이력)

### [x] M1. v0.90.0 Feature Freeze & Production Hardening (100% PASS)
- **Gate 1 (Multi-Wire)**: TDS (SSMS, DBeaver, Beekeeper, sqlcmd) & PGWire v3 (psql) live query parity 100% PASS
- **Gate 2 (ACID & Concurrency)**: In-Memory OCC 1,046k tpmC / WiredTiger 833k tpmC, 0 data races under `-race`
- **Gate 3 (HA & CDC)**: 1-Writer Multi-Reader WAL replication streaming, real-time CDC, live failover 100% PASS
- **Gate 4 (Security & Governance)**: SQL RBAC Default Deny, TLS dynamic certificates, heap memory governor
- **Gate 5 (Unified Binary)**: Single executable (`lionsql`), Web Studio suite, Windows standalone (`lionsql.exe`, 1ms timer)

### [x] M2. v0.90.0 액티브 스프린트 완료 과제 (18개 전량 완료 아카이브)
- [x] **`PERF-PGWIRE-PREP-TX`**: PGWire PreparedStatement 트랜잭션 내 VDBE 바이트코드 직접 바인딩 (`ExecuteInTxWithSessionContext`, `substituteParams` 제거, PGWire TPC-C 24.3만 tpmC 달성)
- [x] **`CASDOOR-INTEGRATION-1`**: Casdoor IAM/SSO 듀얼 프로토콜 연동 (44 XORM 테이블, 복합 PK `\x1f`, `0x` Hex 바이너리)
- [x] **`GRAFANA-INTEGRATION-1`**: Grafana 듀얼 프로토콜 연동 (내부 메타데이터 PGWire 785개 마이그레이션 완주 + PG/TDS 듀얼 쿼리 데이터소스)
- [x] **`IOTA-ERP-1`**: iota-sdk Go ERP PGWire 연동 (47개 마이그레이션, binary UUID, TIMESTAMPTZ)
- [x] **`GITEA-PGWIRE-1` & `GITEA-MOCK-SEED-1`**: Gitea 프로덕션 연동 (BIGSERIAL, RETURNING, pg_indexes, 모의 데이터 시더)
- [x] **`BENCH-TPCC-RPC` & `PERF-TPCC-CLIENT`**: TPC-C 단일 왕복 RPC 벤치마크 및 클라이언트 최적화 (TDS 236k / PGWire 243k tpmC)
- [x] **`PERF-SERVER-PIPELINE-P1-P2`**: TDS 파이프라인 최적화 (Syscall Coalescing, 32KB sync.Pool, Zero-Alloc RPC)
- [x] **`PERF-WIRE-SOCKET-POOL` & `PERF-FASTPATH-PING`**: PGWire 32KB 버퍼 풀링 및 `SELECT 1` 무파싱 핑 패스트패스
- [x] **`ORM-MIGRATE-1~3`**: 조건부 DDL 지원, XORM 컬럼 인트로스펙션, 할당 에일리어싱(`alias = expr`), `sys.fn_helpcollations()`
- [x] **`S-MYSQL-1-P1` & `FUNC-DIALECT-7`**: MySQL 편의 문법(`DESC`, `SHOW TABLES`, 백틱) 및 고ROI 빌트인 함수 Best 7
- [x] **`PROC-CURSOR-1`**: T-SQL Fast Forward-Only 읽기 전용 커서 및 `@@FETCH_STATUS`
- [x] **`AUDIT-EXPR-1` & `PERF-COMPLEX-QUERY-1`**: 복합 쿼리 중첩 표현식 감사 및 AST 제로할당 평가기 튜닝
- [x] **`BENCH-SSB-1`**: Star Schema Benchmark (SSB) 스위트 CLI 도구 완성

### [x] M3. 내구성 및 신뢰성 1차 검증 완료 이력
- [x] **`DURA-SOAK-F1`**: WiredTiger `WT_ROLLBACK`(-31800) 쓰기 충돌을 `storage.ErrSerializationFailure`로 래핑하여 PGWire `SQLSTATE 40001` 매핑
- [x] **`DURA-SOAK-F2`**: 1시간 소크 실측을 통한 500MB 페이지 캐시 워밍업 분석 및 `/metrics` Go 힙/고루틴 수집과 캐시 상한 기준선 확립
- [x] **`DURA-KILL-1 (Phase 1)`**: `cmd/crashtest` 하네스 완성, 3대 결함 수정 및 20라운드/엔진 (총 12.8만 건 tx) 강제 종료 복구 100% 무결성 입증
