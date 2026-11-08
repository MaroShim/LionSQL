# LionSQL Active Engineering Sprint & Task Dashboard (`TODO.md`)

> **SSOT (Single Source of Truth) 원칙 공지**:
> - LionSQL의 전체 180+건 정형 태스크 및 완료(`DONE`)된 버그 해결/리팩토링 이력은 **LionSQL 자체 DB(`sprint_tasks`)와 [`TASKS.md`](TASKS.md)**에 영구 보존됩니다.
> - 본 `TODO.md`는 **1.0 릴리스 활성 스프린트(Active Sprint)와 검증 게이트에만 집중하는 경량 실시간 대시보드**입니다.
> - CLI 관리: `./bin/sprint list`, `./bin/sprint set <ID> <STATUS>`, `./bin/sprint backup`
>
> **Core Architecture & Knowledge Base**:
> - Full Task & Bug Archive Mirror: [`TASKS.md`](TASKS.md)
> - Defect Resolution Archive (107 cases): [`notes/archive/BUG_ARCHIVE.ko.md`](notes/archive/BUG_ARCHIVE.ko.md)
> - Core Invariants & Engineering Context: [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md)
> - Strategic Roadmap: [`ROADMAP.md`](ROADMAP.md)
> - Post-1.0 Candidates Matrix: [`notes/POST_1.0_CANDIDATES.ko.md`](notes/POST_1.0_CANDIDATES.ko.md)

---

## 1. Active Sprint: 1.0 Release Implementation & Gates (1.0 구현 및 검증 게이트)

LionSQL 1.0 릴리스를 완성하기 위한 **3대 핵심 구현 과제**와 **신뢰성 검증 게이트**입니다.

### 1.1 1.0 핵심 구현 및 호환성 리팩토링 (Core 1.0 Features & Clean-up)
1. **`SEC-BOOT-DEFAULT-PASS-WARN`: 초기 패스워드 미변경 시 부팅 출력 최후행 대문자 보안 경고 불변식** [x]
   - **완료 내용**: 어떤 실행 모드(CLI, 데몬, 포트 옵션)든 슈퍼유저 패스워드가 기본값(`1234567890`) 또는 비어있을 때 부팅 화면 콘솔 및 로그의 **가장 마지막 줄(The Very Last Line)**에 비밀번호 리터럴을 노출하지 않는 **100% 대문자(ALL CAPS)** 보안 경고를 반드시 출력하도록 보장 (`cmd/lionsql/server.go`, `daemon.go`, `pkg/config/config.go`). 단위 테스트 5종 전수 PASS.
   - **설계 문서**: [`notes/BOOT_DEFAULT_PASSWORD_WARNING_PLAN.ko.md`](notes/BOOT_DEFAULT_PASSWORD_WARNING_PLAN.ko.md)
2. **`OCC-HYPER-2`: In-Memory OCC Pointer-Free Flat Record A/B 모드 및 신뢰성 검증 게이트** [진행 중]
   - **구현 현황**: 기본 모드(`OCCModeRaw`, 기존 104만 tpmC 오리지널) 100% 보존 + 신규 플랫 모드(`OCCModeFlat`) A/B 분기 구축 완료. 단일 행 기준 5.95배(1,017B ➔ 171B, 83.2% 절감), 10만 건 힙 메모리 67.5% 절감(68.9MB ➔ 22.3MB), 처리 속도 대등 실측 확인.
   - **기본값 정책**: **`OCCModeRaw` (기존 안정 버전) 디폴트 영구 유지 (`[RULE:SafeDefaults]`)**. 아래 4대 검증 게이트를 100% 완주하기 전까지 디폴트 변경 절대 금지.
   - **신규 플랫 모드(`OCCModeFlat`) 안정성 강화 및 필수 검증 게이트**:
     - [x] **`REFACTOR-OCC-FLAT-1` Flat 코덱 신뢰성 및 동시성 결함 보강**: `td.layout` 불변 사전초기화(Data Race 방지), `materialize` 중복 분기 일원화, 디코딩 실패 명시적 에러 전파 (`notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md`)
     - [ ] **`TEST-OCC-FLAT-1` 크래시 복구 무결성**: Flat 모드에서 AOF/RDB 로그 기록 및 `kill -9` 비정상 종료 후 복구/리플레이 100% 정합성 검증
     - [ ] **`TEST-OCC-FLAT-2` 복합 DDL/DML 회귀**: 복합 보조 인덱스(Secondary/Unique Index), SkipList 범위 검색, 외래키(FK) 연동 시 Flat 디코딩 무결성 검증
     - [ ] **`TEST-OCC-FLAT-3` 쿼리 엔진 풀 스위트 회귀**: SQLLogicTest 및 T-SQL 프로시저(Babelfish 스위트)를 Flat 모드 환경에서 실행하여 0 결함 검증
     - [ ] **`TEST-OCC-FLAT-4` 24h 장기 소크 및 가변 길이 UPDATE 단편화 검증**: VARCHAR 갱신이 반복되는 고부하 환경에서 메모리 단편화 및 P99 지연시간 지터 0ms 수호 검증
   - **설계 문서**: [`notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md) & [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)
2. **`WEB-SPA-1`: 단일 바이너리 내장 SPA 웹 서버 호스팅** [x]
   - **완료 내용**: `--app=<path>` 단일 스마트 플래그 및 `LIONSQL_APP` 환경변수 지원. 단일 HTML 파일 및 Flat 정적 디렉토리 자동 감지, HTML5 History API 브라우저 라우트 Fallback 서빙. **Flat Directory Invariant** 엄격 적용으로 하위 계층 에셋 요청 및 도트(`.`) 숨김 파일 차단으로 Path Traversal 100% 원천 봉쇄. E2E 및 단위 테스트 100% 통과.
   - **설계 문서**: [`notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)
3. **`PKG-OFFLINE-DOCS`: 배포 패키지 오프라인 브라우저 문서 번들링**
   - **목표**: 배포 아카이브(`tar.gz`/`zip`) 내 `docs/index.html` 번들링으로 폐쇄망/에어갭 100% 자립 지원
   - **설계 문서**: [`notes/OFFLINE_DOCS_PACKAGING_PLAN.ko.md`](notes/OFFLINE_DOCS_PACKAGING_PLAN.ko.md)
4. **`FIX-SQLUTIL-PARSER-1`: `pkg/sqlutil` SQL 파서/정규화기 결함 보강 및 렉서 공통화** [x]
   - **완료 내용**: `NOLOCK` 리터럴 파괴 방지, 세미콜론(`;`) 보존, `TOP PERCENT/WITH TIES/UNION` 오변환 차단, `ANY/IN/IS NULL` 다중바이트/배열 파싱 결함 수정 및 UTF-8 안전 상태머신 모듈화 완료. 14대 probe 테스트 전수 PASS.
   - **설계 문서**: [`notes/SQL_PARSER_HARDENING_PLAN.ko.md`](notes/SQL_PARSER_HARDENING_PLAN.ko.md)
5. **`GA-REGEX-FINAL-2`: CBO Planner 및 VDBE 잔여 정규식 100% 완전 제거** [x]
   - **완료 내용**: `planner.go` 거대 SELECT/JOIN 정규식 제거(결정론적 토크나이저 대체) 및 `vm.go` 쿼리 바인딩/LIKE 정규식 제거(`sqlutil.MatchLike` 통합)로 엔진 코어 Zero-Regex 달성 완료.
   - **설계 문서**: [`notes/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md)
6. **`GA-REGEX-FINAL-3`: 쿼리 실행 런타임 LIKE 정규식 100% 완전 제거** [x]
   - **완료 내용**: `execution/filter.go`의 런타임 행 순회 `LIKE` 평가 시 정규식 컴파일을 `sqlutil.MatchLike`로 전면 교체하여 핫패스 GC 부하를 0으로 제거하고, `catalog/virtual/result.go`의 가상 카탈로그 `LIKE` 평가는 T-SQL 대괄호 이스케이프(`[_]`, `[%]`)를 보정하여 XORM 및 Collation 회귀 방어 완료.
   - **설계 문서**: [`notes/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md)
7. **`FEAT-TOLERANT-DDL-1`: 동적 런타임 관용 DDL 제어 CLI (`get` & `set`) 및 PostgreSQL SQLSTATE 42704 정합성** [x]
   - **완료 내용**: 기본 엄격 모드(`strict`) 유지. 정적 옵션(Flag/Env/JSON) 전면 배제 및 오직 `./bin/lionsql set tolerant_ddl on/off` 단일 수단만 제공(재시작 시 자동 복귀). Wire 프로토콜에서 인덱스 부재 에러 시 PostgreSQL 표준 `42704` SQLSTATE 매핑 완료.
   - **설계 문서**: [`notes/TOLERANT_DDL_MODE_PLAN.ko.md`](notes/TOLERANT_DDL_MODE_PLAN.ko.md)
8. **`REFACTOR-CATALOG-INTERCEPTOR-1`: 가상 카탈로그 `interceptor.go` Table-Driven 라우터 전환** [x]
   - **완료 내용**: 330여 줄의 순서 의존적 ad-hoc IF 체인을 `RouteRule[]` 정적 디스패치 테이블로 전면 개편. 라우팅 순서 종속성 원천 차단 및 모든 외부 앱 회귀 테스트 통과.
   - **설계 문서**: [`notes/TOLERANT_DDL_MODE_PLAN.ko.md`](notes/TOLERANT_DDL_MODE_PLAN.ko.md)
9. **`SEC-PGWIRE-PARAM-1`: PGWire 달러 파라미터($N) 상태머신 토크나이저 전환 및 보안 강화** [x]
   - **완료 내용**: `pkg/wire/server.go`에서 `pgParamRe` 정규식을 전면 제거하고 `"regexp"` import를 완전 삭제. 작은따옴표(`'...'`), 큰따옴표(`"..."`), 달러 인용(`$$...$$`, `$tag$...$tag$`) 및 주석(`--`, `/* */`)을 완벽히 보존하는 `sqlutil.ReplacePgParams`, `TranslatePgParamsToAtParams`, `MaxPgParamIndex`를 개발하여 쿼리 변조 및 DoS 원천 차단 완료.
   - **설계 문서**: [`notes/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md)

### 1.2 1.0 신뢰성 검증 게이트 (Release Gates)
1. **`AUDIT-135-DUAL-REVIEW`: [1단계] 전 소스코드 135단계 듀얼 AI (Gemini × Claude) 정밀 코드 리뷰 및 엣지 케이스 전수 감사** [x] 완주 완료 (135/135 완주, 총 134건 결함 식별 완료)
   - **목표**: 릴리스 게이트 장기 부하 테스트 이전, Lionsql 엔진 본체 216개 순수 Go 소스코드 전수를 13대 도메인, 총 135개 마이크로 스코프로 완전 분할하여 Gemini(심층 추론)와 Claude(공격적 엣지케이스)의 교차 검증으로 숨은 동시성 레이스, 데드락, 메모리 누수, 침묵하는 에러(`_ = err`), 경계값 결함을 0의 누락도 없이 선제 발굴 및 완치.
   - **설계 문서**: [`notes/PRE_RELEASE_AUDIT_PLAN.ko.md`](notes/PRE_RELEASE_AUDIT_PLAN.ko.md)
   - **감사 기록부 (SSOT)**: [`notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md`](notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md)
2. **`AUDIT-CORE-DEEP-RESCAN`: [2단계] 15대 고복잡도 핵심 코어 구역 다중 결함 심층 전수 감사 (Deep Core Precision Rescan)** [x] 완주 완료 (15/15 전 구역 완주, 총 72건 심층 결함 발굴 완료, 누적 총 206건)
   - **목표**: 1차 광역 스캔의 조기 종료 편향(스코프당 1건 제한)을 극복하기 위해, 스토리지 코어(`wt_engine.go`, `memengine.go`), 트랜잭션 코디네이터(`coordinator.go`), 바이트코드 컴파일러/VM(`compiler.go`, `vm.go`), CBO 플래너/실행기(`planner.go`, `hash_join.go`), 멀티 와이어/복제(`server.go`, `packet.go`), T-SQL 렉서/파서, 시스템 카탈로그, SQL 실행 엔진 본체, 백업/PITR, 고급 분석 오퍼레이터, 인메모리 잔여, CDC & 보안/RBAC, REST API & SPA 서버, SQL 잔여 고급 실행기, 복제 & CLI 데몬 등 15대 핵심 구역에 대해 1건 제한을 완전히 철폐하고 파일 끝까지 다중 결함($N$건)을 전수 발굴하여 SSOT에 증분 누적.
   - **설계 문서**: [`notes/PRE_RELEASE_AUDIT_PLAN.ko.md`](notes/PRE_RELEASE_AUDIT_PLAN.ko.md) 제6장
   - **감사 기록부 (SSOT)**: [`notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md`](notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md)
3. **`AUDIT-TRIAGE-POST`: [3단계] 통합 Triage 회의 및 결함 분류 로드맵 (Post-Audit Triage & Action Plan)** [x] 완주 완료 (197건 전수 분류 및 P0 Must Fix 30선 확정)
   - **목표**: 1차 광역 감사(135단계) 및 2차 코어 심층 재스캔(15대 구역)으로 발굴된 총 206건 결함을 4대 등급(P0: 31건, P1: 53건, P2: 97건, P3: 25건)으로 분류하여, `[RULE:RadicalKISS]` 원칙에 따라 복잡도를 통제하고 1.0 필수 패치 범위 선별 확정.
   - **Triage 보고서**: [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md)
3. **`AUDIT-PATCH-P0`: [4단계] 전수 감사 P0 (Must Fix 31선) 결함 TDD 순차 패치 및 회귀 검증** [진행 중] (27 / 31건 완치 완료, 진척도 87.1%)
   - **목표**: 데이터 손상/유실, 서버 크래시/OOM, 보안 백도어, RLS 테넌트 인젝션, 카탈로그 영속화 누락 등 31개 P0 핵심 결함에 대해 5개 집중 배치(Batch 1~5)로 분할하여 TDD Red ➔ Green 회귀 테스트 및 `go test -race` 0건 완치.
   - **최근 완료 내역**:
     - **[Batch 1 종결]**: P0-06(익명 관리자 백도어 제거), SEC-BOOT-DEFAULT-PASS-WARN(부팅 최후행 경고), P0-07(다중 롤 DENY 우선권 보장), P0-08(Outbox 수신자 장애 시 무음 폐기 방지 및 백프레셔 일시정지, ADR-026), P0-09(보안 계정/권한 메타데이터 auth.json 원자적 영속화), P0-10(CDC 히스토리 재생 무음 드롭 방지 및 무유실 브릿지 스트리밍)
     - **[Batch 2 종결]**: P0-01(FlatRecord 힙 오프셋 태그 비트 충돌 데이터 오염 완치), P0-02(스냅샷/체크포인트 동기화 시 FlatRecord 빈 로 대입 데이터 증발 방지 및 역직렬화 완치), P0-03(WAL I/O 오류 및 중간 손상 시 정상 후속 레코드 영구 절삭 방지 및 Fail-Closed 확립, Invariant ㉒, ADR-027), P0-04(ALTER TABLE ADD COLUMN 시 FlatRecord 기존 컬럼 데이터 전량 파괴 방지 및 구 스키마 역직렬화/새 레이아웃 재압축 완치), P0-05(파티션 키 갱신 및 GetPartitions 시 partMu 보호 및 동시 맵 쓰기 Data Race 완치)
     - **[Batch 3 종결]**: P0-15(WAL 아카이브 회전 중 신규 트랜잭션 Truncate 절삭 방지 및 동결 세그먼트 원자적 로테이션), P0-16(백업 복원 실패 시 사전 스냅샷 격리 및 원자적 롤백 복원, 반파 DB 방치 차단), P0-17(외래키 Kahn's 위상 정렬 순차 생성/삽입 및 역순 드롭), P0-20(AlterTrigger 디스크 persistObjectsLocked 영속화 보장), P0-21(CatalogObjects 내 Statistics 필드 직렬화/복원 및 Create/DropStatistics 영속화), P0-22(GetView/ListViews 방어적 복사본 반환 Data Race 원천 차단)
     - **[Batch 4 종결]**: P0-23(DECIMAL/NUMERIC/MONEY 파서의 TypeFloat 강등 오매핑 차단 및 Precision/Scale 보존 TypeDecimal 정규 매핑 완치), P0-24(UTF-8 멀티바이트 비-ASCII 식별자 byte 캐스팅 파편화 방지 및 utf8.DecodeRuneInString 기반 룬 렉서 완치), P0-25(이항/BETWEEN/LIKE 연산자 오른쪽 파싱 실패 시 Right nil 조립 차단 및 에러 전파 완치), P0-28(빈 테이블/Zero Cardinality CBO 유니크 인덱스 비용 산출 시 0 나누기 +Inf/NaN 전파 및 Full Scan 왜곡 방지, LEFT JOIN 보존 완치)
     - **[Batch 5 진행 중]**: P0-11(GROUP BY CUBE 컬럼 12개 상한 가드레일), P0-12(Window 함수 하위 스트림 workMem 64MB 가드레일), P0-13(Merge Join 중복 키 버퍼링 workMem 64MB 가드레일), P0-14(Aggregate PullChunk I/O 에러 시 자식 리소스 누수 방지), P0-18(MERGE 문 PK 직접 룩업 스트리밍 및 비인덱스 풀스캔 5만행 상한 가드레일 OOM DoS 완치), P0-19(UPDATE/DELETE 스캔 중 I/O 에러 시 무음 탈출 방지 및 Fail-Closed 즉각 롤백 완치), P0-26(네트워크 비정상 단절 시 트랜잭션 최우선 즉각 롤백 및 cs.Mu 언락 후 DDL 드롭 데드락 차단 완치) 완료. 잔여 4건 순차 진행 중.
   - **실행 계획**: [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md) 제4장
4. **`DOC-REVAMP-10`: 공식 문서 및 엔지니어링 지식 체계 10대 핵심 고도화 마스터 플랜** [계획 수립 / 대기]
   - **목표**: 오버스펙(별도 VitePress 웹사이트, Swagger 중복 번들링 등)을 배제하고 실질적 가치 중심의 10대 알짜 과제 집중 완수 (1단계: 핵심 영문 4종 국문화, 14대 Spec-Out 기각 결정 단일 카탈로그, 내장 SPA 웹 서버 1분 스타터 예제, 마크다운 링크 깨짐 자동 검사기 / 2단계: T-SQL 3초 치트시트, SQLSTATE 트러블슈팅 사전, 사용자 여정 네비게이션, 오픈소스 가이드 메타 카드, 벤치마크 Mermaid 차트화, MSSQL 이전 체크리스트).
   - **설계 문서**: [`notes/DOCUMENTATION_SYSTEM_UPGRADE_PLAN.ko.md`](notes/DOCUMENTATION_SYSTEM_UPGRADE_PLAN.ko.md)
5. **`DURA-KILL-1`: `kill -9` 크래시 복구 장기 라운드 검증**
   - 상태: 진행 중 (100라운드 100% 무손실 복구 통과 완료, 릴리스 직전 500 ~ 1,000 라운드 최종 확인)
   - 설계 문서: [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](notes/DURABILITY_VERIFICATION_PLAN.ko.md)
6. **`DURA-SOAK-1`: 24h ~ 72h 장기 무중단 소크 테스트**
   - 상태: 진행 중 (단기 연속 부하 통과, 24시간 ~ 72시간 연속 가동으로 자원 유계 입증)
   - 설계 문서: [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](notes/DURABILITY_VERIFICATION_PLAN.ko.md)
7. **`DURA-SLT-2`: SQLLogicTest 1.0 풀 스위트 듀얼 엔진 최종 인증**
   - 상태: 107개 스위트 55만 쿼리 100% PASS 입증 완료 (`scripts/verify_0.90.sh`)
   - 설계 문서: [`notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md`](notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md)
8. **`GA-VALID-3`: 연동 앱 섀도 모드 실운영 도그푸딩**
   - 이미 100% 연동 통과된 실전 서비스(Vaultwarden, Umami, Answer 중 1~2종) 1~2주간 실운영 백그라운드 구동
9. **`TUNE-DOC-SYNC-1`: 벤치마크 및 공식 문서 실측치 일괄 동기화**
   - 1.0 GA 릴리스 직전 모든 최적화 실측치를 `doc/BENCHMARK.md` 및 `README.md`에 일괄 반영

### 1.3 [테스트베드] 타임박스 외부 앱 호환성 프로브 (Time-Boxed Probe Protocol)
> **운영 원칙**: 완벽 연동을 위해 무한정 시간을 쓰지 않고, **엄격한 30분 타임박스**를 두고 가볍게 찔러보며 엔진의 숨은 엣지 케이스를 발굴하는 테스트베드로만 활용 (비침습적 패치 우선, 무한 루프나 구조적 대수술 감지 시 30분 내 즉시 중단).
- `INTEG-DIRECTUS-1`: Directus (30분 프로브 완료 ➔ PENDING 정리: 92개 마이그레이션 DDL 통과 확인, 이후 `@directus/schema` 인스펙터의 `information_schema.columns` LEFT JOIN `information_schema.tables` 및 `relnamespace IN ('public'::regnamespace)` 복합 쿼리 체인 식별 완료)
- `INTEG-NOCODB-1`: NocoDB (30분 프로브 완료 ➔ PENDING 정리: 100+개 시스템 테이블 DDL 및 다중 마이그레이션 통과 확인, 이후 `nc_202610121136_file_references_url_index` 마이그레이션의 `DROP INDEX "nc_fr_file_url"` 비정규 인덱스 삭제 시 존재하지 않는 인덱스 예외 식별. **공식 해결 아키텍처 확정**: 코어 무결성을 훼손하지 않고 외부 앱 부트스트랩을 무음 통과시키는 **"설치 호환 모드 플래그(`--tolerant-ddl`)"** 도입 결정)
- `INTEG-MATTERMOST-1`: Mattermost (30분 프로브 완료 ➔ PENDING 정리: `SHOW server_version_num` 및 `ALTER TABLE tbl SET (...)` 스토리지 옵션 관용 통과 확인, 이후 `CREATE MATERIALIZED VIEW IF NOT EXISTS poststats` 마테리얼라이즈드 뷰 DDL 및 후속 `REFRESH` 수명주기 요구 식별)
- `INTEG-GHOST-1`: Ghost (10분 프로브 완료 ➔ PENDING 정리: 공식 릴리스 v5.x는 내부 드라이버로 `mysql2`와 `sqlite3`만 패키징하고 `pg` 드라이버 번들링이 영구 제거됨 확인. LionSQL의 PGWire/TDS 리스너와 직접 호환 불가하여 클라우드 백로그로 보존)

---

## 2. Post-1.0 Candidates & Deep Freezer (1.0 이후 후보 및 냉동 보관)

### 2.0 1.0 전수 감사 결과 기반 P2 이관 백로그 및 P3 아키텍처 불변 SSOT
- [ ] **`P2-BACKLOG` Post-1.0 v1.1.0 P2 이관 백로그 전수 카탈로그 (총 92건)**: 128단 중첩 서브쿼리, 10만 건 IN-List, 극한 마진 엣지 케이스를 1.0 코드 복잡도 억제(`[RULE:RadicalKISS]`)를 위해 v1.1.0으로 안전하게 이관 완료. ([`notes/POST_1.0_CANDIDATES.ko.md#4-post-10-v110-p2-이관-백로그-전수-카탈로그-총-92건`](notes/POST_1.0_CANDIDATES.ko.md))
- [x] **`P3-INVARIANTS` By-Design & Spec-Out 영구 아키텍처 불변 목록 (총 25건)**: PL/pgSQL 단일 VM 통합, Non-blocking 2PL 배제, Vacuum 불필요 등 25건 아키텍처 철학 명문화 및 코드 수정 배제 종결. ([`notes/POST_1.0_CANDIDATES.ko.md#5-lionsql-by-design--spec-out-영구-아키텍처-불변-목록-p3-총-25건`](notes/POST_1.0_CANDIDATES.ko.md), [`GEMINI.md`](GEMINI.md))

### 2.1 클라우드 유틸리티 및 엔터프라이즈 백로그
1. **`FEAT-MATVIEW-ALIAS-1` Materialized View Virtual Alias & Tolerant Refresh (1.0 이후)**
   - **목표**: Mattermost, Discourse 등 엔터프라이즈 앱 부트스트랩을 위해 `CREATE MATERIALIZED VIEW`를 가상 뷰(Virtual View, `relkind='m'`)로 인라인 치환하고 `REFRESH MATERIALIZED VIEW`를 Tolerant No-Op(성공) 처리하여 수천 줄의 복잡도와 락 위험을 99% 절감.
   - **설계 문서**: [`notes/MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md`](notes/MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md)
2. **`S3-1~4` 클라이언트 사이드 S3 Pre-signed URL 및 SigV4 서명기** ([`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md))
3. **`AUDIT-LOG-1` 암호화 SHA-256 체이닝 불변 감사 로그 서브시스템** ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md))
4. **`S-SYNTAX-EMPTY-PARENS` 빈 괄호 `IN ()` 문법 평가 정책 (미결 검토)**
   - **내용**: `WHERE col IN ()` 빈 괄호 구문에 대해 ANSI/PG/MSSQL 표준 엄격 문법 에러(Syntax Error)를 유지할지, SQLite/MySQL 관용 상수 폴딩(`1=0`)을 수용할지에 대한 장단점 논의 미결 상태 유지 (1.0에서는 현행 엄격 에러 유지).
   - **설계 문서**: [`notes/EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md`](notes/EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md)

### 2.2 고도화 스토리지 & AI/빅데이터 백로그 (Storage, AI & Replication)
1. **`FEAT-PUREGO-WT-ENGINE` WiredTiger 호환 자체 Pure-Go MVCC 스토리지 엔진 구현 및 CGo 분리 (Post-1.0)**
   - **목표**: Pebble/bbolt는 LSM/COW 구조상 타임스탬프 기반 MVCC 및 미세 튜플 락을 지원하지 못하므로 부적합. WiredTiger의 엔진 계약 인터페이스(`pkg/storage/wiredtiger` SPI)와 100% 호환되는 **자체 순수 Go MVCC B+Tree 스토리지 엔진**을 개발하여:
     1. **성능 극대화**: CGo 호출 오버헤드(cgo call overhead 40~100ns)를 제거하여 Go 런타임 스케줄러와 완전 밀착.
     2. **Windows 네이티브 영속 스토리지 완벽 자립**: Windows 환경에서 CGo 제약으로 인해 인메모리 모드에 머물던 한계를 극복하고, `CGO_ENABLED=0` 순수 단일 정적 바이너리로 3대 OS(macOS/Linux/Windows) 모두에서 완전한 디스크 영속 MVCC 스토리지 지원 (`[RULE:HermeticParity]`, `[RULE:StaticBinary]`).
2. **`RESEARCH-INPROCESS-REBOOT-USE` In-Process Graceful Reboot 기반 물리 서브폴더 USE DB 전환 (Post-1.0)**
   - **목표**: 복잡한 멀티테넌시 대신, 프로세스는 상시 1개 활성 DB만 집중 처리하되 `USE <dbname>` 시 와이어 소켓을 유지한 채 내부에서 기존 엔진 Close ➔ 서브폴더 신규 엔진 Open으로 핫스왑. 다른 활성 세션 존재 시 즉시 `SQLSTATE 55006 (object_in_use)` 에러를 반환하여 안전성 극대화.
   - **설계 문서**: [`notes/IN_PROCESS_REBOOT_MULTI_DATABASE_PLAN.ko.md`](notes/IN_PROCESS_REBOOT_MULTI_DATABASE_PLAN.ko.md)
3. **`FEAT-VECTOR-1` 순수 Go 기반 Vector 임베딩 및 유사도 검색 (1.0 이후)**
   - **목표**: `VECTOR(dim)` 타입, L2/Cosine/Dot 거리 연산자(`<->`, `<=>`), 멀티코어 병렬 Flat KNN 스캔 (LangChain/pgvector 호환).
   - **설계 문서**: [`notes/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](notes/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md)
2. **`FEAT-TIMESERIES-LSM-1` LSM 기반 초고속 Time Series 파티셔닝 & 불변 청크 스토리지 (1.0 이후)**
   - **목표**: DDL `WITH (TYPE='TIMESERIES')`, WiredTiger 내장 `lsm:` 핸들러(경로 A) 및 순수 Go 일별 불변 청크 플러시(경로 B) 하이브리드 결합, `time_bucket()`, $O(1)$ 청크 드롭.
   - **설계 문서**: [`notes/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](notes/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md)
3. **`REPL-STREAM-BIN-1` Replication 네트워크 Length-Prefixed Binary Frame Zero-Copy 스트리밍**
   - **목표**: 복제 스트림 프로토콜을 기존 JSON-lines에서 AOF와 동일한 4바이트 길이 접두사 바이너리 프레임(`[4B length][MsgPack/Raw]`)으로 전환하여 Base64 인코딩 및 JSON 파싱 오버헤드를 100% 제거하고 네트워크 Zero-Copy 달성.
   - **난이도/영향도**: 낮음~중간(★☆☆~★★☆) / 국소 (네트워크 송수신 레이어 한정)
4. **`WAL-DELTA-1` Partial / Delta WAL 로깅 (UPDATE 변경분 컬럼 차분 기록)**
   - **목표**: 대규모 컬럼 테이블에서 `UPDATE` 발생 시 전체 튜플을 재직렬화하지 않고, 변경된 컬럼만 Dirty 비트마스킹(`[DirtyMask][ColIdx, Val]`)하여 AOF 디스크 I/O와 복제 전송량을 최소화.
   - **난이도/영향도**: 중간~높음(★★☆~★★★) / 엔진 내부 (트랜잭션 변경 감지, Delta 코덱, 리플레이 시 Read-Modify-Write 및 보조 인덱스 정합성 유지 필요)

### 2.3 초장기 냉동 보관 후보 (Deep Freezer Backlog)
> 구현 난이도(★★★~★★★★)와 시스템 파급 위험도가 극히 높고 실무 효익 대비 오버헤드가 크므로, 특수 목적의 극단적 대규모 요구 전까지 **초장기 냉동 보관(Deep Freeze)**합니다.
1. **`BENCH-TPCH-1` Mini TPC-H Suite & CBO 서브쿼리 Decorrelation**
2. **`PERF-COMPLEX-QUERY-2` Complex Query Flat Hash Join & Direct Slot Projection**
3. **`LOC-1~4` MSSQL Collation 및 유니코드 다국어 로케일 정렬/비교**

### 2.4 영구 기각 (Spec-Out Invariants)
- **`FRIENDLY-SQL` (`P-DUCKDB-1`)**: DuckDB 스타일 편의 문법 (`* EXCLUDE`, `FROM tbl;` 등) ➔ 오타 침묵 및 RDBMS 엄격성 훼손으로 영구 기각 ([`notes/DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md`](notes/DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md))
- **`TDS-MOD-1` (대규모 모놀리스 분할)**: TDS 2.9k 라인 대규모 파일 분할 ➔ Git Blame/히스토리 보존 가치 우선 및 1.0 회귀 위험 방지를 위해 기각 (공개 인터페이스 변경 없는 내부 경량 분리만 허용)
- **`OCC-HYPER-2 Option B` (Varint/Bit 압축)**: 비트 패킹 레코드 압축 ➔ 0.5ns $O(1)$ 읽기 파괴로 영구 기각 (Pointer-Free Flat Record 및 결함 치료로 하드닝 완결)
- **`PERF-COMPLEX-QUERY-COLUMNAR`**: 순수 Columnar Vectorization 전면 재작성 ➔ Row-store OLTP 엔진 한계 초과로 영구 기각 (1,024행 Chunked Iterator 수준에서 1.0 동결)
- **`PERF-SERVER-PIPELINE-P3-P4`**: ReadOnly 자동 바이패스 ➔ 겉형식과 실질 불일치 및 격리성 파괴 위험으로 기각

---

## 3. Completed Milestones & Historical SSOT Link

- **전체 완료 태스크 및 버그 아카이브 (180+건)**: [`TASKS.md`](TASKS.md) (LionSQL DB 자동 동기화 미러)
- **10대 실전 오픈소스 앱 연동 완료**: Answer, Vaultwarden, Metabase, Umami, Vikunja, Woodpecker CI, Gitea, Ghost, Casdoor, Affine
- **VDBE VM 5대 혁신 (`PERF-VDBE-EXTREME-OPT-1`)**: 슬롯 배열 직접 인덱싱, DML 풀링, 평탄화 루프 전환 완료 (상시 84,766 TPS 통과)
- **정규식 제로 불변식 (`Invariant 7`)**: 엔진 및 와이어 프로덕션 경로 정규식 100% 박멸 완료
