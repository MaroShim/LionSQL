# LionSQL Engineering TODO & Backlog

이 문서는 LionSQL의 실무 엔지니어링 실행 과제, 도메인별 백로그 매트릭스, 최근 완료된 변경 이력을 관리하는 공식 엔지니어링 작업 대시보드입니다.

> [!NOTE]
> 중장기 아키텍처 로드맵 및 버전별 마일스톤 릴리스 계획은 [ROADMAP.md](ROADMAP.md)를 참조하세요.
> 코드 리뷰 및 결함 감사 트래커는 [CODE_REVIEW.ko.md](CODE_REVIEW.ko.md)에서 실시간으로 추적 및 관리됩니다.
> 구조적 아키텍처 추상화 및 리팩토링 명세는 [REFACTORING.ko.md](REFACTORING.ko.md)를 참조하세요.
> 과거 107대 데이터 무결성 결함 조치 아카이브는 [BUG_ARCHIVE.ko.md](BUG_ARCHIVE.ko.md)를 참조하세요.
> 분류 체계 및 식별 코드 규칙의 전체 명세는 [notes/TAXONOMY.ko.md](notes/TAXONOMY.ko.md)에 상세히 기술되어 있습니다.

---

## 1. 식별 코드 체계 범례 (Taxonomy Legend)

| 접두사 / 코드 | 트랙 명칭 (도메인) | 번호 범위 | 설명 |
| :---: | :--- | :---: | :--- |
| **`P1` ~ `P3`** | **우선순위 (Priority)** | `P1` ~ `P3` | 개발 착수 긴급도 (P1: 활성 스프린트/즉시 착수, P2: 차기 착수, P3: 생태계 검증) |
| **`P-1` ~ `P-17`** | **엔진 성능 (Performance)** | `P-1` ~ `P-17` | 스토리지, 디코딩, 와이어, 캐시, 정규식, JSON, 메모리 풀링 17대 성능 트랙 |
| **`R-1` ~ `R-5`** | **코드 리팩토링 (Refactoring)** | `R-1` ~ `R-5` | 거대 파일 모듈 분해, 단일 책임 원칙(SRP), DRY 중복 해소 및 가상 뷰 레지스트리화 |
| **`A` ~ `D`** | **CBO 최적화 (Optimizer)** | `A-1` ~ `D-9` | 비용 기반 옵티마이저 4대 기능 트랙 (A: 통계 수집, B: 조인 알고리즘, C: 선택도, D: 운영 제어) |
| **`S-1` ~ `S-7`** | **SQL 문법 (Syntax)** | `S-1` ~ `S-7` | T-SQL 및 ANSI SQL 7대 문법 호환성 완비 트랙 (전 항목 완료) |
| **`S3-1` ~ `S3-4`** | **오브젝트 스토리지 (Storage)** | `S3-1` ~ `S3-4` | 클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 연동 트랙 (Post-1.0) |
| **`OBS-1` ~ `OBS-3`** | **관제 및 프로파일링 (Observability)** | `OBS-1` ~ `OBS-3` | Prometheus 메트릭, EXPLAIN ANALYZE, 슬로우 쿼리 관제 트랙 |
| **`SEC-1` ~ `SEC-2`** | **엔터프라이즈 보안 (Security)** | `SEC-1` ~ `SEC-2` | TDS Pre-Login TLS 암호화 협상 및 프로토콜 보안 강화 트랙 |
| **`LOC-1` ~ `LOC-4`** | **다국어/정렬 (Collation)** | `LOC-1` ~ `LOC-4` | MSSQL Collation/로케일 지원, COLLATE 관용 파싱, 내장 함수 트랙 (Post-1.0) |
| **`PREP-1`** | **준비된 문장 (Prepared)** | `PREP-1` | 세션별 Prepared Statement Bounded LRU 캐시 및 메모리 상한 가드 |
| **`AUTH-1`** | **인증/토큰교환 (Auth)** | `AUTH-1` | 클라이언트 사이드 OIDC 토큰 교환 (Client-Side Token Exchange) 트랙 (Post-1.0) |
| **`HTTP-1` ~ `HTTP-4`** | **HTTP API 하드닝 (API)** | `HTTP-1` ~ `HTTP-4` | 글로벌 CORS, Gzip 압축, 본문 크기 제한, 보안 헤더 강화 트랙 |
| **`UI-1` ~ `UI-16`** | **Web Studio UI/UX (UI)** | `UI-1` ~ `UI-16` | Toast 알림, 반응형 메뉴, 브랜드 정돈, 실시간 검색, 비밀번호 토글, Drop Table 가드, Sample Seed 가드, SDK 탭, Light/Dark 테마, Overview 대시보드 (`UI-OVERVIEW-1`), 사이드바 4대 카테고리화(`UI-14`), 글로벌 홈 독립(`UI-15`), TPS/QPS 분리 및 Instant Delta(`UI-16`) |
| **`SHUTDOWN-1`** | **종료/안정성 (Shutdown)** | `SHUTDOWN-1` | Graceful Shutdown 시 WiredTiger CGO SIGSEGV 방지 및 PGWire 드레인 동기화 |
| **`BUG-*`** | **데이터 무결성 결함 아카이브 (107건)** | `BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1` | 107대 결함 전수 조치 완료 내역은 [`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md)로 이관하여 아카이브로 관리 |
| **`VPERF-1` ~ `VPERF-18`** | **수직 핫패스 최적화 (Vertical Perf)** | `VPERF-1` ~ `VPERF-18` | TDS 와이어 프로토콜 ➔ 코디네이터 ➔ VDBE VM ➔ 스토리지 엔진(OCC/WiredTiger) 전 계층 수직 성능 최적화 트랙 |

---

## 2. v0.9.0 릴리스 스프린트 마감 현황 (100% Completed)

LionSQL의 `v0.9.0` 릴리스 스프린트(`v0.9.0-feature-freeze`부터 `v0.9.0-beta1`, `v0.9.0-beta2`, `v0.9.0-beta3`)에서 진행된 핵심 안정화, 도구 호환성, 성능 최적화, 무결성 과제가 **100% 완료([x])**되어 스프린트가 공식 마감되었습니다.

5대 프로덕션 릴리스 품질 관문(Gate 1: 다중 와이어 드라이버 100% 인증, Gate 2: ACID 및 Zero-Race 20k TPS, Gate 3: Always On HA 및 CDC, Gate 4: 엔터프라이즈 보안 TDE/RBAC, Gate 5: 운영 도구 및 단일 CLI)이 모두 충족되었습니다.

### 2.1. 와이어 프로토콜 & 외부 도구 호환성 트랙

| 과제 코드 | 핵심 구현 과제 | 대상 패키지 | 상태 | 증적 및 기술 문서 |
| :---: | :--- | :--- | :---: | :--- |
| **`TDS-RPC-AUDIT-1`** | TDS RPC 핸들러(`sp_prepare`, `sp_prepexec`) 파라미터 이름 의존성 전면 제거 및 100% 위치 매핑 일원화 | `pkg/wire/tds` | **완료 (`[x]`)** | [`notes/TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md`](notes/TDS_RPC_POSITIONAL_MAPPING_POSTMORTEM.ko.md) |
| **`TOOL-AUDIT-1`** | 외부 GUI 클라이언트(DBeaver, DbGate, SSMS, Beekeeper Studio) 9단계 E2E 검증, `FormatResultWithSQL` alias/case 정규화, 고동시성 300회 스트레스 통과 | `pkg/catalog/virtual`, `pkg/wire/tds`, `pkg/wire` | **완료 (`[x]`)** | [`notes/TOOL_AUDIT_PLAN.ko.md`](notes/TOOL_AUDIT_PLAN.ko.md), [`doc/CLIENT_TOOL_INTEGRATION.md`](doc/CLIENT_TOOL_INTEGRATION.md) |
| **`TDS-COMPAT-1`** | WiredTiger TRUNCATE TABLE 트랜잭션 격리 및 assertion 크래시 방지 | `pkg/storage/wiredtiger`, `pkg/engine` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`TDS-COMPAT-2`** | 유니코드 N-prefix (`N'...'`) TDS UTF-16LE 직렬화 EOF 방지 | `pkg/proc`, `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`TDS-COMPAT-3`** | BIT 컬럼 수치 리터럴(`1`/`0`) 불리언 강제 변환(`CastValueToType`) 보강 | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`TDS-COMPAT-4`** | Ad-hoc 쿼리 배치 최상위 `IF ... ELSE` 및 `WHILE` 익명 VDBE 블록 실행 지원 | `pkg/engine`, `pkg/proc` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`TDS-COMPAT-5`** | 레거시 메타데이터 저장 프로시저 (`sp_tables`, `sp_columns`) 빌트인 구현 | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`TDS-COMPAT-7`** | T-SQL 프로시저 파서의 `DECLARE @var INT = <expr>` 인라인 초기화 지원 | `pkg/proc` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`TDS-COMPAT-8`** | 문자열 `+` 연산 시 `CONCAT_NULL_YIELDS_NULL ON` 규격에 따른 NULL 전파 보강 | `pkg/engine`, `pkg/proc` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`TDS-COMPAT-9`** | 사용자 예외 발생 구문 (`RAISERROR`, `THROW`) 파싱 및 TDS Error Token(0xAA) 방출 | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`TDS-TYPE-1`** | `GETDATE()` 및 타임스탬프 함수의 네이티브 DATETIME 바이너리 직렬화 | `pkg/engine`, `pkg/proc`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`PG-COMPAT-1`** | PGWire 미지정 타입 파라미터($1) 바이너리 길이 정합성 | `pkg/wire` | **완료 (`[x]`)** | [`doc/PGWIRE_COMPATIBILITY_REPORT.md`](doc/PGWIRE_COMPATIBILITY_REPORT.md) |
| **`PG-COMPAT-2`** | 기본키(PK) 중복 삽입 시 Unique Violation 에러 방출 | `pkg/engine`, `pkg/storage` | **완료 (`[x]`)** | [`doc/PGWIRE_COMPATIBILITY_REPORT.md`](doc/PGWIRE_COMPATIBILITY_REPORT.md) |
| **`PG-COMPAT-3`** | 관계형 조인 시 우측 자식 컬럼 프로젝션 누락 보강 | `pkg/execution` | **완료 (`[x]`)** | [`doc/PGWIRE_COMPATIBILITY_REPORT.md`](doc/PGWIRE_COMPATIBILITY_REPORT.md) |
| **`PREP-1`** | Prepared Statement 메모리 방어용 세션별 Bounded LRU 캐시 | `pkg/wire`, `pkg/wire/tds` | **완료 (`[x]`)** | 256개 상한 LRU, 장기 커넥션 OOM 원천 차단 |
| **`UNIFY-CLI`** | 단일 바이너리(`lionsql`) 완전 통합 및 CLI 라이프사이클 재정립 | `cmd/lionsql`, `cmd/server` | **완료 (`[x]`)** | [`notes/UNIFIED_CLI_PLAN.md`](notes/UNIFIED_CLI_PLAN.md) |
| **`CLI-VERSION-1`** | 단일 바이너리 CLI 버전/빌드 정보 듀얼 출력 (상세 Option A & 디폴트 기동 Option B) | `pkg/config`, `cmd/lionsql`, `pkg/api` | **완료 (`[x]`)** | `debug.ReadBuildInfo()` VCS 자동 추출, `version`/`-v`/`--version` 리포트 |

### 2.2. 스토리지 엔진 & OCC 수직 성능 최적화 트랙

| 과제 코드 | 핵심 구현 과제 | 대상 패키지 | 상태 | 증적 및 기술 문서 |
| :---: | :--- | :--- | :---: | :--- |
| **`VPERF-15`** | In-Memory OCC 5대 최적화 (비트마스크 샤드 추적, Table/Shard 캐싱, 지연 유니크셋, 비차단 AOF 압축, 힙 오염 제거) | `pkg/storage/memengine` | **완료 (`[x]`)** | [`notes/IN_MEMORY_OCC_PERFORMANCE_TUNING_PLAN.md`](notes/IN_MEMORY_OCC_PERFORMANCE_TUNING_PLAN.md) Stage 1, 3, 5, 6 |
| **`VPERF-16`** | Go 런타임 메모리 거버너 연동 GOGC 디폴트 최적화(GOGC=200 + GOMEMLIMIT) 전면 적용 | `pkg/execution`, `pkg/config`, `cmd/` | **완료 (`[x]`)** | [`notes/IN_MEMORY_OCC_PERFORMANCE_TUNING_PLAN.md`](notes/IN_MEMORY_OCC_PERFORMANCE_TUNING_PLAN.md) Stage 2 (+22.9% 향상) |
| **`VPERF-17`** | In-Memory OCC 3대 핫패스 고속화 (IntValue/FloatValue 0-Alloc 지연생성, 락 외부 사전 WAL 인코딩, 샤드 엔트리 단일 맵화) | `pkg/storage`, `pkg/storage/memengine` | **완료 (`[x]`)** | [`notes/IN_MEMORY_OCC_PERFORMANCE_TUNING_PLAN.md`](notes/IN_MEMORY_OCC_PERFORMANCE_TUNING_PLAN.md) Stage 4 & 7 (18,037 TPS) |
| **`VPERF-18`** | In-Memory OCC 읽기 핫패스 무락 카탈로그 스냅샷(`tablesAtomic`), GetRow 무할당 슬라이스 조회, txPool 재사용 안전성 확보 | `pkg/storage/memengine`, `cmd/tpcc_kernel` | **완료 (`[x]`)** | 카탈로그 RWMutex 경합 제거, ReadOnly 베이스버전 우회, 실측 779k tpmC 달성 |
| **`VPERF-1`** | In-Memory OCC 커밋 시 런타임 메트릭스 STW 및 슬라이스 힙 할당 제거 | `pkg/storage/memengine` | **완료 (`[x]`)** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **`VPERF-2`** | Autocommit 쿼리 `ClientSession.Session` 재사용으로 WT 커서 풀 보존 | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | 세션별 커서 풀 파괴 방지 및 CGo 세션 재사용 |
| **`VPERF-3`** | `IDENTITY` (`AUTO_INCREMENT`) 삽입 시 전체 테이블 스캔($O(N)$) 제거 | `pkg/engine` | **완료 (`[x]`)** | $O(1)$ 시퀀스 카운터 채번으로 가속 |
| **`VPERF-4`** | In-Memory OCC `Update`/`Delete` 역방향 외래키 참조 사전 캐싱 | `pkg/storage/memengine` | **완료 (`[x]`)** | 전체 테이블 순회 100% 생략 ($O(1)$ 단락) |
| **`VPERF-7`** | 협상된 `PacketSize` 동적 적용 및 `WriteUTF16LEDirect` 직접 버퍼 기록 | `pkg/wire/tds` | **완료 (`[x]`)** | Write 시스콜 8배 절감 및 셀당 슬라이스 할당 제거 |
| **`VPERF-8`** | `PlanCache.GetOrCompile` SHA-256 해싱 제거 및 `RLock` 패스트패스 | `pkg/proc/runtime` | **완료 (`[x]`)** | 원문 문자열 키 및 RLock 조회로 플랜 캐시 경합 해소 |
| **`VPERF-9`** | VDBE 무트리거 테이블 맵 할당 생략 및 `buildRowEvalMap` 루프 호이스팅 | `pkg/proc/runtime` | **완료 (`[x]`)** | 트리거 미보유 테이블 DML 힙 할당 생략 |
| **`VPERF-13`** | `INSERT ... ON CONFLICT (col)` 보조 인덱스 스캔(`IndexScan`) 연동 | `pkg/engine` | **완료 (`[x]`)** | Upsert 시 $O(N)$ 전체 스캔 방지 및 즉시 단건 인덱스 룩업 |
| **`VPERF-14`** | VDBE `execUpdateWhere`/`execDeleteWhere` 카탈로그 스키마 조회 호이스팅 | `pkg/proc/runtime` | **완료 (`[x]`)** | 루프 내 카탈로그 뮤텍스 락 경합 100% 제거 |
| **`MEM-3`** | In-Memory OCC 엔진 데이터 용량 한도 가드 (`storage.ErrTableFull`) | `pkg/storage/memengine`, `cmd/server` | **완료 (`[x]`)** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **`KISS-4`** | In-Memory SkipList 내부 뮤텍스 락 제거 및 Go 표준 PRNG 간소화 | `pkg/storage/memengine` | **완료 (`[x]`)** | Go 1.22+ `math/rand/v2` 전환으로 락-프리/무할당 난수 레벨 결정 |
| **`PERF-1`** | 크리티컬 패스 분할기, TDS 파라미터 바인딩, Row 스트리밍 및 UTF-16LE 변환 가속 | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | 단일 쿼리 분할 16ns/op, 바인딩 923ns, Row 스트리밍 53ns 0-Alloc 완비 |

### 2.3. 쿼리 실행기 & KISS 다이어트 트랙

| 과제 코드 | 핵심 구현 과제 | 대상 패키지 | 상태 | 증적 및 기술 문서 |
| :---: | :--- | :--- | :---: | :--- |
| **`KISS-5`** | 코어 엔진 3갈래 쿼리 실행 경로(`sqlexec_select` vs `execution` Iterator vs VDBE) 단일화 및 2-Tier 영구 아키텍처 확정 (`ARCH-2TIER-1`) | `pkg/engine`, `pkg/execution`, `pkg/proc/runtime` | **완료 (`[x]`)** | [`notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md) |
| **`ARCH-LEX-2`** | MERGE, STRING_SPLIT, PROCS 잔여 수동 분할 루프 `sqlutil.Tokenizer` 전면 일원화 | `pkg/sqlutil`, `pkg/engine` | **완료 (`[x]`)** | [`REFACTORING.ko.md`](REFACTORING.ko.md) |
| **`REFACTOR-TYPE-GUESS-1`** | 휴리스틱 타입 추론 및 임의 문자열 수치 강제변환 안티패턴 전면 제거 | `pkg/sqlutil`, `pkg/engine`, `pkg/execution`, `pkg/proc`, `pkg/wire`, `pkg/api` | **완료 (`[x]`)** | [`notes/TYPE_GUESSING_ANTI_PATTERN_ELIMINATION_PLAN.ko.md`](notes/TYPE_GUESSING_ANTI_PATTERN_ELIMINATION_PLAN.ko.md) |
| **`REFACTOR-OVER-SPECIFICATION-1`** | T-SQL 디스패처 100% 위치 매핑, SERVERPROPERTY 하이재킹 제거 및 가상 카탈로그 도구 종속성 정돈 | `pkg/engine`, `pkg/wire/tds`, `pkg/catalog/virtual` | **완료 (`[x]`)** | [`notes/OVER_SPECIFICATION_REMEDIATION_PLAN.md`](notes/OVER_SPECIFICATION_REMEDIATION_PLAN.md) |
| **`KISS-1`** | 미사용 구형 JS 트랜스파일러 및 `JSCode` 잔재 완전 제거 (1,169줄 순수 삭제) | `pkg/proc`, `pkg/catalog`, `cmd/` | **완료 (`[x]`)** | [`doc/FOUNDATIONAL_DESIGN_DECISIONS.md`](doc/FOUNDATIONAL_DESIGN_DECISIONS.md) |
| **`KISS-2`** | 가상 PG 카탈로그 및 information_schema 생성기 중복 단일화 | `pkg/engine`, `pkg/catalog/virtual` | **완료 (`[x]`)** | 1,690줄 코드 중복 제거 및 `virtual.Source` 단일화 |

### 2.4. 데이터 무결성 결함 Remediation & 보안 하드닝 트랙

| 과제 코드 | 핵심 구현 과제 | 대상 패키지 | 상태 | 증적 및 기술 문서 |
| :---: | :--- | :--- | :---: | :--- |
| **`BUG-107`** | 데이터 무결성, 대소문자 정합성, 임의 추측 및 맵 순회 107대 결함 전수 조치 완료 및 아카이빙 | 전 계층 (`pkg/`) | **완료 (`[x]`)** | [`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md) |
| **`BUG-PROC-VARCHAR-NUM-COERCE-1`** | T-SQL 프로시저 호출 시 Quoted 문자열 및 `VARCHAR` 파라미터 숫자 오변환 방지 | `pkg/engine` | **완료 (`[x]`)** | `execProcedureCall` 따옴표 리터럴 보존으로 New-Order PK 오변환 방지 |
| **`SEC-ESC-1`** | 세션 변수(`@var`) 치환 시 작은따옴표 이스케이프 및 인젝션 방어 | `pkg/engine` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **`SESS-CLEAN-1`** | 세션 종료 및 연결 리셋 시 테이블 변수(`TableVars`) 및 임시 테이블 누수 완전 해제 | `pkg/engine` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **`PROD-SEC-1`** | 외부 인터페이스 바인딩(`0.0.0.0`) 시 기본 패스워드 사용 차단 및 프로덕션 기동 가드레일 | `cmd/lionsql`, `pkg/config` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **`BRAND-UNIFY-1`** | 웹훅 헤더(`X-LionSQL-*`), SDK 엔드포인트/클래스명, 시스템 변수 잔여 브랜드 완전 정돈 | `pkg/api`, `pkg/proc`, `pkg/engine`, `pkg/wire` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **`SHUTDOWN-1`** | Graceful Shutdown 시 WiredTiger CGO SIGSEGV 방지 및 PGWire 드레인 동기화 | `pkg/wire`, `pkg/storage/wiredtiger` | **완료 (`[x]`)** | 커넥션 1초 Bounded Drain 및 atomic `connClosed` 가드레일 완비 |

### 2.5. 벤치마크 & 통합 스모크 검증 트랙

| 과제 코드 | 핵심 구현 과제 | 대상 패키지 | 상태 | 증적 및 기술 문서 |
| :---: | :--- | :--- | :---: | :--- |
| **`SMOKE-EXT-1`** | 13대 핵심 아키텍처 도메인 확장 통합 스모크 테스트 스위트 및 가이드 구현 | `scripts/verify_report.sh`, `scripts/README.md` | **완료 (`[x]`)** | [`scripts/README.md`](scripts/README.md), [`notes/TEST_VERIFICATION_REPORT.ko.md`](notes/TEST_VERIFICATION_REPORT.ko.md) (13대 도메인 100% PASS) |
| **`BENCH-TPCC-CLIENT-1`** | 실행 중인 서버 대상 네트워크 TPC-C 벤치마크 클라이언트 구현 | `cmd/tpcc_client` | **완료 (`[x]`)** | TDS(:1433)/PGWire(:5433) 소켓 접속, 5대 트랜잭션 동시 부하 주입 및 실시간 TPS 연동 |
| **`BENCH-TPCC-CLIENT-2`** | 네트워크 TPC-C 클라이언트 1-RTT 단일 배치 전송, 기본 스케일팩터 최적화(16wh/32wk), 50건 청크 고속 시딩 | `cmd/tpcc_client` | **완료 (`[x]`)** | 12회 소켓 핑퐁 1-RTT 단일화 및 7,400+ TPS 달성 |
| **`BENCH-VDBE-TPCC-1`** | VDBE 런타임 TPC-C 3단계 실무 스케일팩터 개편(16wh/32wk, 20wh/200wk, 50wh/500wk) 및 3.5절 공식 문서화 | `doc/BENCHMARK.md`, `cmd/tpcc_runtime` | **완료 (`[x]`)** | 16/32(497k vs 668k tpmC), 20/200(413k vs 553k tpmC), 50/500(381k vs 434k tpmC) 실측 |
| **`DOC-REORDER-1`** | 벤치마크 3.3절 In-Memory 우선 배치 및 최상위 README Key Features 5대 축(3-4-1-2-5) 재배치 | `doc/BENCHMARK.md`, `README.md`, `README.ko.md` | **완료 (`[x]`)** | 3.3절 OCC 좌측/WT 우측 일원화, README 핵심 기능 재정돈 |
| **`DOC-ARCHIVE-1`** | 결함 조치 아카이브 루트 디렉토리 이동(`BUG_ARCHIVE.ko.md`) 및 아라비아 숫자 넘버링 정돈 | `BUG_ARCHIVE.ko.md` | **완료 (`[x]`)** | 상대 링크 갱신 및 아라비아 숫자 체계 일원화 |

### 2.6. Web Studio & 관제/API 트랙

| 과제 코드 | 핵심 구현 과제 | 대상 패키지 | 상태 | 증적 및 기술 문서 |
| :---: | :--- | :--- | :---: | :--- |
| **`UI-1` ~ `UI-11`** | Toast 알림(`UI-1`), 반응형 레이아웃(`UI-2`), 브랜드 정돈(`UI-3`), 비밀번호 토글(`UI-5`), Drop Table 가드(`UI-6`), Sample Seed 가드(`UI-7`), 동적 더미 페이로드(`UI-8`), LionSQL SDK 탭(`UI-9`), Light/Dark 테마(`UI-10`), Light 모드 고대비 시인성 개편(`UI-11`) | `pkg/ui`, `pkg/api` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) |
| **`UI-13`** | 헤더 브랜딩(`LionSQL Web Studio`) 단일화 및 서브 뷰/내비게이션 `Studio` 접미사 삭제 | `pkg/ui` | **완료 (`[x]`)** | 로고 및 메뉴 명칭 정돈 |
| **`UI-OVERVIEW-1`** | System Overview 대시보드 신설 (Active Sessions, Uptime, Tables, TPS/QPS, 연결 가이드) | `pkg/ui`, `pkg/api` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) 2절 및 `/api/overview` |
| **`UI-14`** | 좌측 사이드 패널 4개 카테고리화(Database, Automation, Developer, Monitoring) 및 디바이더 스타일링 | `pkg/ui` | **완료 (`[x]`)** | 12개 도구 동선 최적화 |
| **`UI-15`** | 최상단 글로벌 홈(`Overview`) 독립 및 `Database & Query` 카테고리 순수화 | `pkg/ui` | **완료 (`[x]`)** | 시스템 홈 독립 및 DB 3종 도구 정돈 |
| **`UI-16`** | System Overview TPS/QPS 2대 독립 카드 분리 및 Instant Delta 실시간 반영 | `pkg/ui` | **완료 (`[x]`)** | 6대 지표 3x2 대칭 그리드 및 3초 Instant Delta 연동 |
| **`HTTP-1` ~ `HTTP-4`** | 글로벌 CORS 지원(`HTTP-1`), Gzip 응답 압축(`HTTP-2`), 본문 32MB 상한 가드(`HTTP-3`), 보안 응답 헤더 일괄 주입(`HTTP-4`) | `pkg/api` | **완료 (`[x]`)** | 엔터프라이즈 HTTP API 하드닝 완비 |
| **`HTTP-TEST-1`** | HTTP REST & Web Management 8대 기능 전수 검증 및 실측 E2E 테스트 스위트 | `pkg/api` | **완료 (`[x]`)** | [`doc/HTTP_API_FUNCTIONALITY_REPORT.md`](doc/HTTP_API_FUNCTIONALITY_REPORT.md) |
| **`OBS-1`** | Prometheus / OpenTelemetry 표준 관제 메트릭 엔드포인트 (`/metrics`, `/v1/metrics`) | `pkg/api`, `pkg/engine` | **완료 (`[x]`)** | Prometheus 텍스트 규격 및 OTLP JSON 완비 |
| **`OBS-2`** | `EXPLAIN ANALYZE` 런타임 실행 프로파일러 (Actual Startup, Duration, Rows, Loops 계측) | `pkg/planner`, `pkg/execution`, `pkg/engine` | **완료 (`[x]`)** | [`doc/INTERNALS_CBO_AND_STATISTICS.md`](doc/INTERNALS_CBO_AND_STATISTICS.md) |

---

## 3. 차기 스프린트 대기열 (v1.0.0 LTS 출시 준비 및 Post-1.0 Queue)

### 3.1. v1.0.0 LTS 정식 출시 및 패키징 태스크 (Active Queue)

| 작업 코드 | 영역 | 핵심 출시 과제 | 대상 산출물 | 상태 | 비고 |
| :---: | :---: | :--- | :--- | :---: | :--- |
| **`REL-1.0-TAG`** | 릴리스/태그 | **`v1.0.0` 정식 릴리스 태그 생성 및 GitHub Release 배포** | Git Tag `v1.0.0`, GitHub Release | **준비 완료 (Ready)** | 5대 관문 전수 통과 상태에서 공식 1.0 LTS 태그 선언 |
| **`REL-1.0-BIN`** | 빌드/패키징 | **멀티플랫폼(Linux amd64/arm64, macOS, Windows) 정적 단일 바이너리 릴리스 빌드** | `bin/lionsql-*`, `Dockerfile` | **준비 완료 (Ready)** | CGo static linking / Darwin arm64 / Linux x86_64 배포 아티팩트 |
| **`REL-1.0-DOC`** | 문서/안내 | **공식 LionSQL 1.0 릴리스 노트 발행 및 사용자 마이그레이션 가이드 배포** | `CHANGELOG.md`, `README.md` | **준비 완료 (Ready)** | 0.9.0 베타 결실 및 1.0 프로덕션 사양 공표 |

### 3.2. Post-1.0 (`v1.1.0+`) 로드맵 과제 대기열 (Postponed Queue)

1.0 릴리스의 무결성과 20k TPS 초고속 OLTP 안정성을 지키기 위해 공식 이관된 차기 엔지니어링 백로그 목록입니다:

| 과제 코드 | 영역 | 핵심 구현 과제 | 대상 위치 | 목표 릴리스 | 관련 기술 문서 |
| :---: | :---: | :--- | :--- | :---: | :--- |
| **`AUDIT-LOG-1`** | 보안/감사 | **엔터프라이즈 암호학적 SHA-256 불변 감사 로그 서브시스템 (MPSC 링버퍼, CLI 검증기)** | `pkg/audit`, `cmd/lionsql` | `v1.1.0` | [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md) |
| **`TDS-MOD-1`** | 와이어/모듈 | **TDS 거대 단일 파일(`server.go` 2.6k줄) 관심사별 모듈 분해 (Handshake/RPC/Batch/Server)** | `pkg/wire/tds` | `v1.1.0` | 1.0 안정성 최우선에 따라 이관 |
| **`TDS-COMPAT-6`** | ORM/와이어 | **다중 결과셋 (Multiple Result Sets) 스트리밍 지원 (Dapper `QueryMultiple` 필수)** | `pkg/engine`, `pkg/wire/tds` | `v1.1.0` | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **`S3-1` ~ `S3-4`** | 스토리지/클라우드 | **클라우드 S3 Pre-signed URL 직접 업로드 및 순수 Go SigV4 서명기 연동** | `pkg/storage/s3sign`, `pkg/config`, `pkg/api`, `pkg/proc` | `v1.1.0` | [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md) |
| **`LOC-1` ~ `LOC-4`** | 다국어/정렬 | **MSSQL Collation/로케일 지원, `COLLATE` 관용 파싱 및 `::fn_helpcollations()`** | `pkg/proc`, `pkg/engine`, `pkg/wire/tds` | `v1.1.0` | [`notes/COLLATION_AND_LOCALE_PLAN.md`](notes/COLLATION_AND_LOCALE_PLAN.md) |
| **`SEC-1`** | 와이어/보안 | **TDS 8.0 / Pre-Login SSL/TLS 암호화 핸드셰이크 보강** | `pkg/wire/tds` | `v1.1.0` | MS-TDS 2.2.6.4 규격 보강 |
| **`AUTH-1`** | 인증/토큰 | **클라이언트 사이드 OIDC 토큰 교환 (Client-Side Token Exchange) 엔드포인트** | `pkg/auth`, `pkg/api` | `v1.1.0` | `GEMINI.md` 8.6절 |
| **`DOC-EN-1`** | 문서/다국어 | **`doc/` 내 4대 핵심 매뉴얼(`USER_MANUAL`, `REFERENCE_MANUAL`, `PERFORMANCE_GUIDE`, `WEB_STUDIO_GUIDE`) 공식 영문판 작성** | `doc/` | `v1.1.0` | 글로벌 사용자용 영문 문서 완비 |
| **`UI-SQL-1`** | UI/DX | **Web Studio SQL Query 에디터 문법 강조 및 자동완성 강화 (Monaco/CodeMirror)** | `pkg/ui` | `v1.1.0` | 대규모 쿼리 편집 경험 고도화 |
| **`VPERF-5, 6, 10~12`** | 핫패스/스트리밍 | **고위험 무복사 스트리밍 파이프라인 (Attention 동기화, `RawRows` 직결, WT Zero-Copy)** | `pkg/engine`, `pkg/wire/tds`, `pkg/storage` | `v1.1.0` | 무결성 검증 완료 상태 보존 위해 이관 |
| **`R-4, R-5`** | 스토리지/리팩토링 | **In-Memory OCC 트랜잭션 수명주기 모듈 분해 및 WiredTiger CGo 분해** | `pkg/storage/memengine`, `pkg/storage/wiredtiger` | `v1.1.0+` | CGo 빌드 태그 복잡도 및 회귀 방지 위해 이관 |

---

## 4. 공식 보류 및 영구 스펙-아웃 사유 명세 (Deferred & Spec-Out Invariants)

엔터프라이즈급 sub-microsecond 트랜잭션 성능(20,000+ TPS)과 자립형 단일 바이너리(KISS) 아키텍처를 영구히 유지하기 위해, [GEMINI.md](GEMINI.md) 제8절에 의거하여 공식 확정된 영구 스펙-아웃 및 보류 사유입니다:

### 4.1. 영구 스펙-아웃 아키텍처 불변 규칙 (Spec-Out Invariants)

1. **절차 언어 런타임 단일화 & 중복 인터프리터(PL/pgSQL) 배제 (`GEMINI §8.1`)**:
   - 프로시저 및 UDF 실행 엔진은 SQLite VDBE 스타일의 고성능 T-SQL 바이트코드 가상머신(`pkg/proc`)으로 완전 단일화합니다. 커널 내부에 PL/pgSQL 인터프리터를 중복 탑재하는 것은 코어 비대화 및 보안 공격 표면 증가를 방지하기 위해 영구 **Spec Out**합니다.
2. **모던 JSON 전면 표준화 & 레거시 XML DML/XQuery 엔진 배제 (`GEMINI §8.2`)**:
   - 반정형 데이터 처리는 T-SQL/ANSI 표준 JSON 함수군 및 Go 네이티브 고속 JSON 파서로 표준화합니다. 레거시 XML 스키마 검증 및 XML DML(`.modify()`, `.nodes()`)은 메모리 오버헤드 배제를 위해 영구 **Spec Out**합니다.
3. **인메모리 OCC 스토리지 데드 튜플 백그라운드 Vacuum 스레드 배제 (`GEMINI §8.3`)**:
   - `memengine`은 프라이빗 워크스페이스에서 변경분을 버퍼링 후 커밋 시 원자적으로 버전을 교체하므로 데드 튜플 체인을 누적하지 않습니다. 불필요한 백그라운드 고루틴 생성 및 락 경합을 유발하는 Vacuum 워커 스레드는 영구 **Spec Out**합니다.
4. **CDC 이벤트 스트리밍 표준화 & POSIX 공유 메모리(Arrow IPC) 스냅샷 배제 (`GEMINI §8.4`)**:
   - 트랜잭션 커밋과 원자적으로 결합된 CDC/WAL 이벤트 스트림을 단일 표준 파이프라인으로 채택합니다. 쿼리마다 수 GB를 풀스캔하여 직렬화하는 높은 OLTP CPU 부하와 OS 비이식성을 초래하는 Apache Arrow IPC 공유 메모리 스냅샷은 영구 **Spec Out**합니다.
5. **외부 분산 트랜잭션 코디네이터 (XA / MS DTC) 배제 (`GEMINI §8.5`)**:
   - 단일 노드 20,000+ TPS 트랜잭션 처리량 보존을 위해 분산 2PC 네트워크 왕복 지연을 수반하는 XA/MS DTC는 영구 **Spec Out**합니다. 분산 정합성은 비동기/반동기 WAL 복제로 처리합니다.
6. **DB 커널 내장 OAuth2 웹 플로우 배제 & 클라이언트 사이드 토큰 교환 표준화 (`GEMINI §8.6`)**:
   - 외부 소셜 로그인의 웹 리다이렉트 핸들러, Client Secret 보관, 아웃바운드 인터넷 HTTP 통신을 DB 커널에 내장하는 것은 영구 **Spec Out**합니다. 인증은 엔터프라이즈 와이어 표준(SCRAM-SHA-256, TDS 표준) 및 Client-Side OIDC ID Token 검증(`POST /api/auth/token-exchange`)으로 표준화합니다.
7. **DB 커널 내장 S3 Blob 파일 스토리지 및 이미지 썸네일 리사이징 배제 (`GEMINI §8.7`)**:
   - 대용량 바이너리 스트리밍 및 CPU 집약적 이미지 리사이징으로 인한 OLTP 코어의 자원 기아를 방지하기 위해 파일 서버 기능은 영구 **Spec Out**하며, 전용 오브젝트 스토리지(S3/R2/MinIO) Pre-signed URL 직접 업로드 방식을 채택합니다.
8. **미성숙 Pure-Go 영속 스토리지 엔진 (Pebble/bbolt) 단기 도입 배제 (`GEMINI §8.8`)**:
   - 현재 오픈소스 Pure-Go 임베디드 스토리지 생태계는 WiredTiger 수준의 멀티코어 MVCC B+Tree, Hazard Pointer 동시성, 세밀한 트랜잭션 타임스탬프를 온전히 대체하기 어렵습니다. 1.0 프로덕션에서는 WiredTiger(디스크)와 In-Memory OCC(메모리) 2대 백엔드 체제에 집중합니다.
9. **슬로우 쿼리 실시간 웹훅 직발송 커널 내장 배제 (`GEMINI §8.9`)**:
   - DB 커널 내부에서 Slack, Discord 등의 외부 HTTP 웹훅을 직접 호출하는 것은 단일 책임 원칙과 네트워크 장애 격리를 위반하므로 영구 **Spec Out**합니다. 관제는 표준 Prometheus 메트릭(`OBS-1`)과 런타임 실행 분석기(`OBS-2`)를 통해 외부 전용 도구가 수집합니다.
10. **Wasm UDF 격리 샌드박스 (`wazero`) 커널 내장 배제 (`GEMINI §8.10`)**:
    - 불필요한 제3의 런타임 내장으로 인한 바이너리 비대화와 메모리 오버헤드를 방지하기 위해 영구 **Spec Out**하며, 서브마이크로초 Native UDF SPI(`proc.RegisterScalarFunction`)로 일원화합니다.
11. **복잡 쿼리 전면 VDBE 바이트코드화 배제 & 2-Tier 쿼리 실행 아키텍처 영구 불변 (`GEMINI §8.11`, `ARCH-2TIER-1`)**:
    - 모든 복합 관계대수를 바이트코드로 무리하게 번역하려는 시도는 컴파일러 비대화와 엣지 케이스 디버깅 난도를 폭증시키므로 영구 **Spec Out**합니다. 단순 OLTP는 VDBE 바이트코드(Tier 1, <1µs)로 초고속 처리하고, 복합 관계대수는 Volcano CBO 반복자 엔진(Tier 2)으로 처리하는 2-Tier 구조를 영구 불변 규칙으로 확정합니다.

### 4.2. 고위험 성능 개편 보류 사유 (Post-1.0 로드맵 백로그)

* **DML `storage.Record` 제거 ➔ `storage.Row` 직결 (`P-13`)**:
  `memengine` 및 WiredTiger 스토리지 계층의 복잡한 외래키 연쇄(CASCADE), 유니크 인덱스 충돌 검사, 복합 제약조건 검증 로직이 `storage.Record`와 결합되어 있어 무리한 슬라이스 직결 시 ACID 무결성 훼손 위험이 있어 보류.
* **WiredTiger 다중 행 배치 CGo 삽입 (`P-15`)**:
  CGo 프리앰블 수준의 일괄 삽입은 C 포인터 메모리 관리 오류 및 세그폴트(SIGSEGV) 크래시 위험이 있어 시스템 무결성을 위해 보류.
* **In-Memory OCC Row-Level Atomic CAS 커밋 (`P-16`)**:
  롤백 시 버전 복원 경쟁, 툼스톤 동시성 처리 등 동시성 엣지 케이스와 데이터 사일런트 손상(Silent Corruption) 위험이 큼. 이미 TPC-C 20k TPS 환경에서 직렬화 어보트율이 0.10%로 극히 안정적이므로 보류.
* **VDBE 컬럼 산술 융합 슈퍼 명령어 (`P-17`)**:
  바이트코드 점프 타깃(PC) 오프셋 불일치 등 가상 머신 실행 무결성 위협 위험이 크며, 이미 서브마이크로초 디스패치로 20k TPS를 달성 중이므로 보류.
* **`sqlexec` 하위 정규식 제거 & AST 파서 일원화 (`KISS-3`)**:
  25개 전 패키지와 120개 이상의 E2E 드라이버 통합 테스트가 100% 통과 중인 상태에서, 단지 정규식을 없애겠다는 미학적 이유로 멀쩡한 코드를 뜯어고치는 것은 버그 유발 위험만 큼. 1-Pass 인덱스 슬라이스 스캐너(`PERF-1`, 16ns/op)로 이미 초고속 핫패스를 달성하였으므로 영구 보류.

---

## 5. 최근 완료 마일스톤 상세 이력 (Recent Completed History)

### 5.1. v0.9.0 Betas (Beta 1 ~ Beta 3) 주요 완성 내역 (2026-10-19 ~ 2026-10-25)

- [x] **13대 핵심 아키텍처 도메인 확장 통합 스모크 테스트 스위트 및 자동화 구현 (`SMOKE-EXT-1`) (2026-10-25)**:
  - `scripts/verify_report.sh` 및 `scripts/README.md` 구축: TDS, T-SQL, TPC-C, 경계조건, 트라이브릿지, 크래시복구, HA, HTAP, CBO, JSON, RBAC, 마이그레이션, 파티셔닝 13개 핵심 도메인 전수 검증.
  - In-Memory OCC 및 WiredTiger 듀얼 백엔드 전반에 걸쳐 ~3초 이내 100% PASS 달성.
- [x] **외부 GUI 클라이언트(SSMS, DbGate, DBeaver, Beekeeper) 9단계 E2E 심층 감사 완결 (`TOOL-AUDIT-1`) (2026-10-25)**:
  - 4대 대표 GUI 도구에 대한 실전 세션 초기화, 메타데이터 인트로스펙션, 결과셋 정규화(`FormatResultWithSQL`), 고동시성 300회 연결 스트레스 검증 100% 통과.
  - [`notes/TOOL_AUDIT_PLAN.ko.md`](notes/TOOL_AUDIT_PLAN.ko.md) 및 [`doc/CLIENT_TOOL_INTEGRATION.md`](doc/CLIENT_TOOL_INTEGRATION.md) 반영 완료.
- [x] **In-Memory OCC 읽기 핫패스 무락 카탈로그 스냅샷(`tablesAtomic`) 및 무할당 슬라이스 조회 (`VPERF-18`) (2026-10-25)**:
  - 카탈로그 전역 RWMutex 경합 완전 제거, ReadOnly 베이스버전/복제 우회, 21컬럼 Record map 할당 방지.
  - 3.4 벤치마크 실측: In-Memory OCC **779k tpmC** / WiredTiger **833k tpmC** 달성.
- [x] **VDBE 런타임 TPC-C 3단계 실무 스케일팩터 개편 및 공식 문서화 (`BENCH-VDBE-TPCC-1`) (2026-10-24)**:
  - 16wh/32wk(497k vs 668k tpmC), 20wh/200wk(413k vs 553k tpmC), 50wh/500wk(381k vs 434k tpmC) 실측 및 문서화 완료.
- [x] **네트워크 TPC-C 클라이언트 1-RTT 단일 배치 전송 및 최적화 (`BENCH-TPCC-CLIENT-1/2`) (2026-10-24)**:
  - New-Order/Delivery/Stock-Level 12회 소켓 핑퐁을 1-RTT 단일 배치로 통합하고 16wh/32wk 최적화로 7,400+ TPS 달성.
- [x] **코어 엔진 3갈래 쿼리 실행 경로 단일화 및 2-Tier 아키텍처 영구 불변 확정 (`KISS-5`, `ARCH-2TIER-1`) (2026-10-24)**:
  - 단순 OLTP 직결(Tier 1, <1µs)과 복합 Volcano CBO 반복자(Tier 2)로 2-Tier 단일화 완료.
  - 상세 결정 내역: [`notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md).
- [x] **MERGE, STRING_SPLIT, PROCS 잔여 수동 분할 루프 `sqlutil.Tokenizer` 전면 일원화 (`ARCH-LEX-2`) (2026-10-24)**:
  - 파서 전반의 비표준 문자열 분할 루프를 공통 어휘 스캐너로 단일화 ([`REFACTORING.ko.md`](REFACTORING.ko.md)).
- [x] **In-Memory OCC 3대 핫패스 고속화 (`VPERF-17`) (2026-10-24)**:
  - `storage.IntValue`/`FloatValue` 온디맨드 지연 생성, 락 외부 사전 WAL 인코딩, 샤드 엔트리 단일 맵화로 18,037 TPS 달성.
- [x] **Go 런타임 메모리 거버너 연동 GOGC 디폴트 최적화 (`VPERF-16`) (2026-10-24)**:
  - `GOGC=200` 기본값 적용 및 GOMEMLIMIT 결합으로 처리량 +22.9% 향상 (17,150 TPS).
- [x] **In-Memory OCC 5대 수직 핫패스 최적화 (`VPERF-15`) (2026-10-24)**:
  - 비트마스크 샤드 추적, Table/Shard 캐싱, 지연 유니크셋, 비차단 AOF 압축 완비 (14.0k TPS, 375k tpmC).
- [x] **휴리스틱 타입 추론 및 임의 문자열 수치 강제변환 안티패턴 전면 제거 (`REFACTOR-TYPE-GUESS-1`) (2026-10-24)**:
  - `CompareAny` 신설, 따옴표 리터럴 컨텍스트 보존, 사전식 대소 비교 정립으로 파라미터 오변환 원천 차단.
- [x] **TDS RPC 핸들러 100% 위치 매핑 보강 (`BUG-TDS-BEEKEEPER-RPC-STATEMENT-1`) (2026-10-21)**:
  - `@stmt` 대신 `@statement`를 전달하는 클라이언트에서도 0번째 인자를 순서 기반으로 수용하여 Beekeeper Studio 접속 크래시 완전 해결.
- [x] **단일 바이너리 CLI 버전/빌드 정보 듀얼 출력 (`CLI-VERSION-1`) (2026-10-21)**:
  - `debug.ReadBuildInfo()` VCS 자동 추출, `version`/`-v`/`--version` 리포트 및 디폴트 기동 배너 한 줄 표시 완비.
- [x] **Web Studio 사이드바 4대 카테고리화(`UI-14`), 글로벌 홈 독립(`UI-15`), TPS/QPS 분리 및 Instant Delta(`UI-16`) (2026-10-21)**:
  - 12대 도구 동선 최적화, 최상단 `Overview` 독립, 3초 Instant Delta 롤링 윈도우 완비.
- [x] **브랜드 통일화(`BRAND-UNIFY-1`), 세션 변수 인젝션 방어(`SEC-ESC-1`), 임시 테이블 누수 해제(`SESS-CLEAN-1`) (2026-10-20)**:
  - 전 계층 `LionSQL` 네이밍 단일화, 작은따옴표 이중화 이스케이프, 커넥션 풀 리셋 시 임시 테이블 물리 드롭 완비.
- [x] **과거 107대 데이터 무결성 결함 전수 조치 및 아카이빙 (`DOC-ARCHIVE-1`) (2026-10-20)**:
  - 107대 결함 조치 내역을 루트 [`BUG_ARCHIVE.ko.md`](BUG_ARCHIVE.ko.md)로 이관 및 아라비아 숫자 넘버링 정돈 완료.

### 5.2. v0.9.0 Feature Freeze 주요 완성 내역 (2026-10-19 이전)

- [x] **통합 SQL 추상화 레이어 기반 결함군 일괄 해결 (`SQL-ABSTRACT-1`)**:
  - `storage.Record` 1급 객체화 및 `Record.Get` 대소문자 무관 탐색을 바탕으로 전 계층 경계 정합성 전면 보강.
- [x] **Web Studio 4대 생산성/관측성 기능 완결 (`UI-DDL-1`, `UI-REALTIME-1`, `UI-DATA-1`, `UI-LOGS-1`)**:
  - DDL 비주얼 컬럼 추가 모달, SSE 실시간 스트림 인스펙터, 레코드 다중 선택/외래키 점프, HTTP 요청 활동 로그 뷰어 완비.
- [x] **Developer API Preview 모달 고도화 (`UI-8`, `UI-9`)**:
  - 테이블 스키마 기반 동적 더미 JSON 페이로드 생성 및 공식 JavaScript/TypeScript SDK(`lionsql.js`) 탭 신설.
- [x] **PGWire 미지정 타입 파라미터($1) 바이너리 길이 정합성 (`PG-COMPAT-1`)**:
  - OID 캐싱 및 단독 파라미터 TEXT 기본값 정의로 `lib/pq` 클라이언트 패닉 원천 해결.
- [x] **기본키(PK) 중복 삽입 시 Unique Violation (SQLSTATE 23505) 에러 방출 (`PG-COMPAT-2`)**:
  - WiredTiger 커서 선행 검사 및 표준 에러 포맷팅 완비.
- [x] **세션별 Prepared Statement Bounded LRU 캐시 (`PREP-1`)**:
  - 세션당 256개 상한 Bounded LRU 캐시 적용으로 장기 커넥션 풀 메모리 누수 원천 차단.
- [x] **크리티컬 패스 5대 무할당 최적화 (A~E) 완결 (`PERF-1`)**:
  - 정규식 싱글톤 승격, 16ns/op 문장 분할기, 923ns TDS 파라미터 바인딩, 53ns Row 직접 스트리밍, 제로할당 UTF-16LE 인코딩 완비.
- [x] **In-Memory SkipList 내부 뮤텍스 락 제거 (`KISS-4`)**:
  - Go 1.22+ `math/rand/v2` 전환으로 락-프리/무할당 난수 레벨 결정.
- [x] **가상 PG 카탈로그 및 information_schema 생성기 단일화 (`KISS-2`)**:
  - 1,690줄 중복 제거 및 `virtual.Source` 추상화 기반 통합.
- [x] **미사용 구형 JS 트랜스파일러 및 `JSCode` 잔재 완전 제거 (`KISS-1`)**:
  - 1,169줄 구형 코드 완전 삭제 및 메타데이터 정돈.
- [x] **In-Memory OCC 엔진 데이터 용량 한도 가드 (`MEM-3`)**:
  - `runtime/metrics` 기반 Zero-STW 힙 모니터링 및 `storage.ErrTableFull` 페일세이프 반환 완비.
- [x] **글로벌 메모리 예산 상한제 및 대량 조인 회로 차단기 (`pkg/config`, `pkg/execution`)**:
  - 호스트 물리 RAM의 65% 상한선 자동 계산, `debug.SetMemoryLimit` 연동, 해시조인 `work_mem` 초과 시 Fail-Fast 회로 차단.
- [x] **단일 바이너리(`lionsql`) 완전 통합 및 CLI 라이프사이클 재정립 (`UNIFY-CLI`)**:
  - `bin/lionsql` 포그라운드/데몬/관리 서브커맨드 완비 및 `bin/server` 하위 호환 래퍼 전환.
- [x] **Always On 스타일 자동 장애 조치(Auto-Failover) 및 리더 선출 (`pkg/replication`)**:
  - 최고 LSN 우선권 리더 선출, 스플릿 브레인 방지 펜싱 및 자동 승격 완비.
- [x] **Transactional Outbox 브로커 디스패처 (Kafka/RabbitMQ/Webhook) (`pkg/cdc`)**:
  - CDC 커밋 스트림 기반 아웃박스 릴레이 및 무손실 체크포인트 완비.
- [x] **비용 기반 옵티마이저 (CBO) 4대 트랙 완결 (`A` ~ `D`)**:
  - 병렬 통계 수집(`A-1`), 통계 영속화(`A-2`), 소트-머지 조인(`B-3`), Selinger DP 조인 최적화(`B-4`), 다중 컬럼 확장 통계(`C-5`), MCV 최빈값 목록(`C-7`), 백그라운드 자동 통계 갱신(`D-8`), 플래너 힌트(`D-9`).
