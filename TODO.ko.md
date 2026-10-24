# LionSQL Engineering TODO & Backlog

이 문서는 LionSQL의 실무 엔지니어링 실행 과제, 도메인별 백로그 매트릭스, 최근 완료된 변경 이력을 관리하는 공식 엔지니어링 작업 대시보드입니다.

> [!NOTE]
> 중장기 아키텍처 로드맵 및 버전별 마일스톤 릴리스 계획은 [ROADMAP.md](ROADMAP.md)를 참조하세요.
> 코드 리뷰 및 결함 감사 트래커는 [CODE_REVIEW.ko.md](CODE_REVIEW.ko.md)에서 실시간으로 추적 및 관리됩니다.
> 구조적 아키텍처 추상화 및 리팩토링 명세는 [REFACTORING.ko.md](REFACTORING.ko.md)를 참조하세요.
> 과거 107대 데이터 무결성 결함 조치 아카이브는 [notes/BUG_ARCHIVE.ko.md](notes/BUG_ARCHIVE.ko.md)를 참조하세요.
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
| **`S3-1` ~ `S3-4`** | **오브젝트 스토리지 (Storage)** | `S3-1` ~ `S3-4` | 클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 연동 트랙 |
| **`OBS-1` ~ `OBS-3`** | **관제 및 프로파일링 (Observability)** | `OBS-1` ~ `OBS-3` | Prometheus 메트릭, EXPLAIN ANALYZE, 슬로우 쿼리 웹훅 연동 트랙 |
| **`SEC-1` ~ `SEC-2`** | **엔터프라이즈 보안 (Security)** | `SEC-1` ~ `SEC-2` | TDS Pre-Login TLS 암호화 협상 및 프로토콜 보안 강화 트랙 |
| **`LOC-1` ~ `LOC-4`** | **다국어/정렬 (Collation)** | `LOC-1` ~ `LOC-4` | MSSQL Collation/로케일 지원, COLLATE 관용 파싱, 내장 함수 트랙 |
| **`PREP-1`** | **준비된 문장 (Prepared)** | `PREP-1` | 세션별 Prepared Statement Bounded LRU 캐시 및 메모리 상한 가드 |
| **`AUTH-1`** | **인증/토큰교환 (Auth)** | `AUTH-1` | 클라이언트 사이드 OIDC 토큰 교환 (Client-Side Token Exchange) 트랙 |
| **`HTTP-1` ~ `HTTP-4`** | **HTTP API 하드닝 (API)** | `HTTP-1` ~ `HTTP-4` | 글로벌 CORS, Gzip 압축, 본문 크기 제한, 보안 헤더 강화 트랙 |
| **`UI-1` ~ `UI-14`** | **Web Studio UI/UX (UI)** | `UI-1` ~ `UI-14` | 모던 Toast, 반응형 메뉴, 브랜드 정돈(LionSQL Web Studio), 실시간 검색, 비밀번호 토글, Drop Table 가드, Sample Seed 가드, API Preview 동적 스키마 페이로드 및 공식 SDK 탭, Light/Dark 테마, Light 모드 고대비 시인성 개편, System Overview 대시보드 (`UI-OVERVIEW-1`) |
| **`SHUTDOWN-1`** | **종료/안정성 (Shutdown)** | `SHUTDOWN-1` | Graceful Shutdown 시 WiredTiger CGO SIGSEGV 방지 및 PGWire 드레인 동기화 |
| **`BUG-*`** | **데이터 무결성 결함 아카이브 (107건)** | `BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1` | 107대 결함 전수 조치 완료 내역은 [`notes/BUG_ARCHIVE.ko.md`](notes/BUG_ARCHIVE.ko.md)로 이관하여 아카이브로 관리 |
| **`VPERF-1` ~ `VPERF-14`** | **수직 핫패스 최적화 (Vertical Perf)** | `VPERF-1` ~ `VPERF-14` | TDS 와이어 프로토콜 ➔ 코디네이터 ➔ VDBE VM ➔ 스토리지 엔진(OCC/WiredTiger) 전 계층 수직 성능 최적화 트랙 |

---

## 2. 현재 활성 스프린트 과제 (Active Sprint Queue)

| 우선순위 | 영역 | 핵심 과제 | 대상 위치 | 상태 | 관련 기술 문서 |
| :---: | :---: | :--- | :--- | :---: | :--- |
| **P1** | **리소스/안정성** | **글로벌 메모리 예산 상한제 및 회로 차단기 (Global Memory Governor & Circuit Breaker)** | `pkg/config`, `pkg/execution`, `cmd/server` | **완료 (`[x]`)** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **P1** | **KISS/다이어트** | **미사용 구형 JS 트랜스파일러 및 `JSCode` 잔재 완전 제거 (`KISS-1`)** | `pkg/proc`, `pkg/catalog`, `cmd/` | **완료 (`[x]`)** | [`doc/FOUNDATIONAL_DESIGN_DECISIONS.md`](doc/FOUNDATIONAL_DESIGN_DECISIONS.md) |
| **P3** | **보안/감사** | **불변 엔터프라이즈 감사 로그 (Enterprise Immutable Audit Log)** | `pkg/audit`, `pkg/api`, `cmd/lionsql` | **Post-1.0 연기 (Postponed)** | [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md) |
| **P1** | **성능/크리티컬패스** | **크리티컬 패스 분할기, TDS 파라미터 바인딩, Row 스트리밍 및 UTF-16LE 변환 가속 (`PERF-1`)** | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`notes/PITFALLS_AND_TRICKS.ko.md`](notes/PITFALLS_AND_TRICKS.ko.md) |
| **P2** | **스토리지/가드** | **In-Memory OCC 엔진 데이터 용량 한도 가드 (`MEM-3`)** | `pkg/storage/memengine`, `cmd/server` | **완료 (`[x]`)** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **P2** | **KISS/중복제거** | **가상 PG 카탈로그 생성기 중복 단일화 (`KISS-2`)** | `pkg/engine`, `pkg/catalog/virtual` | **완료 (`[x]`)** | - |
| **P2** | **KISS/파서통합** | **`sqlexec` 하위 정규식 파싱 계층 제거 및 AST 파서 일원화 (`KISS-3`)** | `pkg/engine`, `pkg/proc` | **Post-1.0 연기 (Postponed)** | - |
| **P2** | **KISS/자료구조** | **In-Memory SkipList 내부 뮤텍스 락 제거 및 Go 표준 PRNG 간소화 (`KISS-4`)** | `pkg/storage/memengine` | **완료 (`[x]`)** | - |
| **P3** | **스토리지/클라우드** | **클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 (`S3-1` ~ `S3-4`)** | `pkg/storage/s3sign`, `pkg/config`, `pkg/api`, `pkg/proc` | **Post-1.0 연기 (Postponed)** | [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md) |
| **P2** | **운영/관제** | **Prometheus / OpenTelemetry 표준 관제 메트릭 엔드포인트 (`OBS-1`)** | `pkg/api`, `pkg/engine` | **완료 (`[x]`)** | - |
| **P2** | **최적화/DX** | **`EXPLAIN ANALYZE` 런타임 실행 프로파일러 (`OBS-2`)** | `pkg/planner`, `pkg/execution`, `pkg/engine` | **완료 (`[x]`)** | [`doc/INTERNALS_CBO_AND_STATISTICS.md`](doc/INTERNALS_CBO_AND_STATISTICS.md) |
| **P1** | **스토리지/안정성** | **WiredTiger TRUNCATE TABLE 트랜잭션 격리 및 assertion 크래시 방지 (`TDS-COMPAT-1`)** | `pkg/storage/wiredtiger`, `pkg/engine` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P1** | **와이어/유니코드** | **유니코드 N-prefix (`N'...'`) TDS UTF-16LE 직렬화 EOF 방지 (`TDS-COMPAT-2`)** | `pkg/proc`, `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P2** | **ORM/와이어** | **다중 결과셋 (Multiple Result Sets) 스트리밍 지원 (Dapper `QueryMultiple` 필수) (`TDS-COMPAT-6`)** | `pkg/engine`, `pkg/wire/tds` | **Post-1.0 연기 (Postponed)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P1** | **프로시저/파서** | **T-SQL 프로시저 파서의 `DECLARE @var INT = <expr>` 인라인 초기화 지원 (`TDS-COMPAT-7`)** | `pkg/proc` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P2** | **타입/파서** | **BIT 컬럼 수치 리터럴(`1`/`0`) 불리언 강제 변환(`CastValueToType`) 보강 (`TDS-COMPAT-3`)** | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P2** | **엔진/제어문** | **Ad-hoc 쿼리 배치 최상위 `IF ... ELSE` 및 `WHILE` 익명 VDBE 블록 실행 지원 (`TDS-COMPAT-4`)** | `pkg/engine`, `pkg/proc` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P2** | **수식/NULL** | **문자열 `+` 연산 시 `CONCAT_NULL_YIELDS_NULL ON` 규격에 따른 NULL 전파 보강 (`TDS-COMPAT-8`)** | `pkg/engine`, `pkg/proc` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P2** | **예외/TDS** | **사용자 예외 발생 구문 (`RAISERROR`, `THROW`) 파싱 및 TDS Error Token(0xAA) 방출 (`TDS-COMPAT-9`)** | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P3** | **도구호환/카탈로그** | **레거시 메타데이터 저장 프로시저 (`sp_tables`, `sp_columns`) 빌트인 구현 (`TDS-COMPAT-5`)** | `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P3** | **타입/와이어** | **`GETDATE()` 및 타임스탬프 함수의 네이티브 DATETIME 바이너리 직렬화 (`TDS-TYPE-1`)** | `pkg/engine`, `pkg/proc`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/TDS_COMPATIBILITY_REPORT.md`](doc/TDS_COMPATIBILITY_REPORT.md) |
| **P3** | **KISS/실행통합** | **3갈래 쿼리 실행 경로 단일화 (`sqlexec_select` ➔ `execution` Iterator) (`KISS-5`)** | `pkg/engine`, `pkg/execution` | **Post-1.0 연기 (Postponed)** | - |
| **P1** | **KISS/단일바이너리** | **단일 바이너리(`lionsql`) 완전 통합 및 CLI 라이프사이클 재정립 (`UNIFY-CLI`)** | `cmd/lionsql`, `cmd/server` | **완료 (`[x]`)** | [`notes/UNIFIED_CLI_PLAN.md`](notes/UNIFIED_CLI_PLAN.md) |
| **P1** | **와이어/디코딩** | **PGWire 미지정 타입 파라미터($1) 바이너리 길이 정합성 (`PG-COMPAT-1`)** | `pkg/wire` | **완료 (`[x]`)** | [`doc/PGWIRE_COMPATIBILITY_REPORT.md`](doc/PGWIRE_COMPATIBILITY_REPORT.md) |
| **P1** | **엔진/무결성** | **기본키(PK) 중복 삽입 시 Unique Violation 에러 방출 (`PG-COMPAT-2`)** | `pkg/engine`, `pkg/storage` | **완료 (`[x]`)** | [`doc/PGWIRE_COMPATIBILITY_REPORT.md`](doc/PGWIRE_COMPATIBILITY_REPORT.md) |
| **P1** | **와이어/안정성** | **Prepared Statement 메모리 방어용 Bounded LRU 캐시 (`PREP-1`)** | `pkg/wire`, `pkg/wire/tds` | **완료 (`[x]`)** | - |
| **P2** | **실행/조인** | **관계형 조인 시 우측 자식 컬럼 프로젝션 누락 보강 (`PG-COMPAT-3`)** | `pkg/execution` | **완료 (`[x]`)** | [`doc/PGWIRE_COMPATIBILITY_REPORT.md`](doc/PGWIRE_COMPATIBILITY_REPORT.md) |
| **P3** | **다국어/정렬** | **MSSQL Collation/로케일 지원 및 `COLLATE` 관용 파싱 (`LOC-1` ~ `LOC-4`)** | `pkg/proc`, `pkg/engine`, `pkg/wire/tds` | **Post-1.0 연기 (Postponed)** | [`notes/COLLATION_AND_LOCALE_PLAN.md`](notes/COLLATION_AND_LOCALE_PLAN.md) |
| **P1** | **와이어/HTTP** | **HTTP REST & Web Management (포트 8090) 8대 기능 전수 검증 및 실측 E2E 테스트 스위트 (`HTTP-TEST-1`)** | `pkg/api`, `doc/HTTP_API_FUNCTIONALITY_REPORT.md` | **완료 (`[x]`)** | [`doc/HTTP_API_FUNCTIONALITY_REPORT.md`](doc/HTTP_API_FUNCTIONALITY_REPORT.md) |
| **P1** | **와이어/보안** | **글로벌 CORS 미들웨어 & OPTIONS Preflight 지원 (`HTTP-1`)** | `pkg/api` | **완료 (`[x]`)** | - |
| **P1** | **와이어/성능** | **HTTP Gzip 응답 압축 핸들러 (`HTTP-2`)** | `pkg/api` | **완료 (`[x]`)** | - |
| **P1** | **보안/리소스** | **요청 본문 상한 가드(32MB) 및 보안 응답 헤더 하드닝 (`HTTP-3`, `HTTP-4`)** | `pkg/api` | **완료 (`[x]`)** | - |
| **P1** | **UI/UX** | **Web Studio 네이티브 `alert()` ➔ 모던 Toast 알림 전면 교체 (`UI-1`)** | `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/UX** | **Web Studio 반응형 레이아웃 및 사이드바 햄버거 토글 (`UI-2`)** | `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/브랜드** | **Web Studio 브랜드 네이밍(`LionSQL`) 정돈 및 검색 필터 (`UI-3`, `UI-4`)** | `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/UX** | **Web Studio 비밀번호 표시/숨기기(Eye Toggle) 버튼 지원 (`UI-5`)** | `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/안전성** | **Drop Table 실수 방지용 테이블명 일치 확인 모달 가드레일 (`UI-6`)** | `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/데이터** | **Sample Data Seed 빈 DB 사전 검증 가드 및 원자적 피드백 (`UI-7`)** | `pkg/api`, `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/DX** | **API Preview 모달 테이블 스키마 기반 동적 더미 페이로드 생성 (`UI-8`)** | `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/DX** | **API Preview 모달 LionSQL 공식 SDK (`lionsql.js`) 전용 코드 탭 신설 (`UI-9`)** | `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/테마** | **Web Studio Light / Dark 모드 테마 전환 및 Cache-Control 캐시 방지 (`UI-10`)** | `pkg/ui`, `pkg/api` | **완료 (`[x]`)** | - |
| **P1** | **UI/가시성** | **Web Studio Light 모드 고대비 시인성 개편 (경고창/배지/모달/ID 컬럼 WCAG AAA 정합) (`UI-11`)** | `pkg/ui` | **완료 (`[x]`)** | - |
| **P1** | **UI/브랜딩** | **Web Studio 헤더 브랜딩(`LionSQL Web Studio`, `OLTP+SP` 배지 제거) 및 서브 뷰/내비게이션 `Studio` 접미사 전면 삭제 (`UI-13`)** | `pkg/ui` | **완료 (`[x]`)** | 상단 로고 `LionSQL Web Studio` 단일화 및 좌측/우측 서브 기능 명칭 정돈 |
| **P1** | **UI/개요** | **Web Studio System Overview 대시보드 신설 (Active Sessions, Uptime, Tables, TPS, 와이어 연결 가이드) (`UI-OVERVIEW-1`)** | `pkg/ui`, `pkg/api` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) 2절 실구현 및 `/api/overview` 연동 |
| **P1** | **UI/네비게이션** | **Web Studio 좌측 사이드 패널 4개 카테고리화(Database, Automation, Developer, Monitoring) 및 우측 구분선(`::after`) 스타일링 (`UI-14`)** | `pkg/ui` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) 1.4절 반영, 12개 도구 동선 최적화 및 카테고리 우측 수평 구분선 디바이더 정돈 |
| **P1** | **CLI/버전** | **단일 바이너리 CLI 버전/빌드 정보 듀얼 출력 (상세 Option A & 디폴트 기동 Option B) (`CLI-VERSION-1`)** | `pkg/config`, `cmd/lionsql`, `pkg/api` | **완료 (`[x]`)** | `debug.ReadBuildInfo()` VCS 자동 추출, `version`/`-v`/`--version` 상세 리포트 및 디폴트 기동 배너 한 줄 표시 |
| **P1** | **종료/안정성** | **Graceful Shutdown 시 WiredTiger CGO SIGSEGV 방지 및 PGWire 드레인 동기화 (`SHUTDOWN-1`)** | `pkg/wire`, `pkg/storage/wiredtiger` | **완료 (`[x]`)** | - |
| **P2** | **UI/실시간** | **Web Studio 실시간 자동 갱신(Auto-Refresh) 토글 및 주기 선택기 (`UI-REALTIME-1`)** | `pkg/ui` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) |
| **P2** | **UI/DDL** | **Web Studio 스키마 DDL 뷰어 (SQL `CREATE TABLE` 및 JSON 내보내기/복사) (`UI-DDL-1`)** | `pkg/ui` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) |
| **P2** | **UI/데이터** | **CSV / JSON 데이터 가져오기(Import) 및 내보내기(Export) 모달 (`UI-DATA-1`)** | `pkg/ui` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) |
| **P3** | **UI/SQL** | **SQL Query Studio 에디터 강화 (CodeMirror/Monaco 문법 강조, 자동완성) (`UI-SQL-1`)** | `pkg/ui` | **Post-1.0 백로그 (Postponed)** | - |
| **P3** | **UI/관제** | **실시간 쿼리 및 HTTP API 요청 감사 로그 뷰어 (`UI-LOGS-1`)** | `pkg/ui`, `pkg/api` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) |
| **P3** | **문서/가이드** | **Web Studio 전체 기능 상세 사용자 설명서 신설 (`DOC-UI-1`)** | `doc/WEB_STUDIO_GUIDE.ko.md` | **완료 (`[x]`)** | [`doc/WEB_STUDIO_GUIDE.ko.md`](doc/WEB_STUDIO_GUIDE.ko.md) |
| **P3** | **프로토콜/보안** | **TDS 8.0 / Pre-Login SSL/TLS 암호화 핸드셰이크 보강 (`SEC-1`)** | `pkg/wire/tds` | **Post-1.0 연기 (Postponed)** | - |
| **P3** | **인증/토큰교환** | **클라이언트 사이드 OIDC 토큰 교환 (Client-Side Token Exchange) (`AUTH-1`)** | `pkg/auth`, `pkg/api` | **Post-1.0 백로그 (Postponed)** | `GEMINI.md` 8.6절 |
| **P1** | **문서/릴리스** | **v0.90.0 기능 동결 4단계 문서 전수 정돈 마스터 플랜 (`DOC-CLEANUP`)** | `CHANGELOG.md`, `README.md`, `doc/`, `ROADMAP.md` | **완료 (`[x]`)** | [`notes/DOCUMENTATION_CLEANUP_PLAN.md`](notes/DOCUMENTATION_CLEANUP_PLAN.md) |
| **P3** | **문서/다국어** | **`doc/` 내 잔여 한글 문서(`USER_MANUAL`, `REFERENCE_MANUAL`, `PERFORMANCE_GUIDE`, `WEB_STUDIO_GUIDE`) 공식 영문판 작성 및 상호 링크 (`DOC-EN-1`)** | `doc/` | **Post-1.0 백로그 (Postponed)** | `USER_MANUAL.ko.md`, `REFERENCE_MANUAL.ko.md`, `TRANSACTION_PERFORMANCE_GUIDE.ko.md`, `WEB_STUDIO_GUIDE.ko.md` |
| **P1** | **보안/변수치환** | **세션 변수(`@var`) 치환 시 작은따옴표 이스케이프 및 인젝션 방어 (`SEC-ESC-1`)** | `pkg/engine/sqlexec.go`, `pkg/engine/query_utils.go` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **P1** | **스토리지/생명주기** | **세션 종료 및 연결 리셋 시 테이블 변수(`TableVars`) 및 임시 테이블 누수 완전 해제 (`SESS-CLEAN-1`)** | `pkg/engine/sqlexec.go`, `pkg/engine/coordinator_impl.go` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **P1** | **브랜드/정합성** | **웹훅 헤더(`X-LionSQL-*`), SDK 엔드포인트/클래스명, 시스템 변수 잔여 브랜드 완전 정돈 (`BRAND-UNIFY-1`)** | `pkg/api`, `pkg/proc`, `pkg/engine`, `pkg/wire` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **P1** | **문서/출시정합성** | **`ROADMAP.md`와 `TODO.ko.md` 1.0 Gate 4 출시 기준 일치화 및 문서 권한(`0644`) 정규화 (`REL-ALIGN-1`)** | `ROADMAP.md`, `TODO.ko.md`, `doc/` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **P2** | **보안/운영가드** | **외부 인터페이스 바인딩(`0.0.0.0`) 시 기본 패스워드 사용 차단 및 프로덕션 기동 가드레일 (`PROD-SEC-1`)** | `cmd/lionsql`, `pkg/config` | **완료 (`[x]`)** | [`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`](notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md) |
| **P3** | **보안/감사** | **엔터프라이즈 암호학적 불변 감사 로그 서브시스템 구축 (`AUDIT-LOG-1`)** | `pkg/audit` | **Post-1.0 연기 (`v1.1.0`)** | [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md) |
| **P1** | **무결성/결함아카이브** | **데이터 무결성, 대소문자 정합성, 임의 추측 및 맵 순회 107대 결함 전수 조치 완료 (`BUG-107`)** | 전 계층 (`pkg/`) | **완료 (`[x]`)** | 107대 세부 내역 및 증적은 [`notes/BUG_ARCHIVE.ko.md`](notes/BUG_ARCHIVE.ko.md) 참조 |
| **P1** | **수직성능/Track A** | **스토리지 & 코디네이터 치명적 병목 해소 (OCC STW 제거 [x], Autocommit 세션 재사용 [x], IDENTITY $O(1)$ [x], FK 사전 캐싱 [x]) (`VPERF-1` ~ `VPERF-4`)** | `pkg/storage/memengine`, `pkg/engine`, `pkg/wire/tds` | **완료 (`[x]`)** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **P2** | **수직성능/Track B** | **TDS & VDBE 저위험 무할당 튜닝 (`PacketSize` 연동 [x], `PlanCache` RLock [x], VDBE 맵 제거 [x], ON CONFLICT 인덱스 스캔 [x], VDBE 루프 카탈로그 호이스팅 [x]) (`VPERF-7` ~ `VPERF-9`, `VPERF-13`, `VPERF-14`)** | `pkg/wire/tds`, `pkg/proc/runtime`, `pkg/engine` | **완료 (`[x]`)** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **P3** | **수직성능/Track C** | **고위험 구조 개편 및 스트리밍 (Attention 동기화, `sp_executesql` 직결, `RawRows` 파이프라인, OCC 스트리밍, WT Zero-Copy) (`VPERF-5`, `VPERF-6`, `VPERF-10` ~ `VPERF-12`)** | `pkg/engine`, `pkg/wire/tds`, `pkg/storage/memengine`, `pkg/storage/wiredtiger` | **Post-1.0 연기 (`v1.1.0`)** | [`doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md) |

### P1 과제 세부 명세 및 수락 기준 (Acceptance Criteria)

#### 1. 글로벌 메모리 예산 상한제 및 회로 차단기 (`pkg/config`, `pkg/execution`, `cmd/server`)
* **과제 개요**: 무제한 메모리 할당으로 인한 OS OOM Killer 사살 방지(Golden Rule 1, 23)를 위한 프로세스/세션 레벨 방어 체계 구축.
* **핵심 아키텍처 및 구현 요구사항**:
  1. **`--max-memory` 글로벌 CLI 플래그 및 환경 설정 도입**:
     - `--max-memory` (예: `2GB`, `4GB`) 플래그 및 `resources.max_memory` 설정 연동.
     - 미설정 시 기본값: **호스트 시스템 물리 메모리(RAM)의 65%** (1GB OS 예비 및 50% 하한 가드) 자동 계산 적용 (cgroups/sysctl/OS 메모리 자동 감지).
  2. **Go 런타임 플랫폼 프리미티브 연동 (`debug.SetMemoryLimit`)**:
     - Go 1.19+ `debug.SetMemoryLimit`을 호출하여 힙 메모리가 상한에 근접할 시 Go 런타임 GC가 사전에 능동적으로 메모리를 회수하도록 보장 (Golden Rule 14).
     - 서버 기동 배너에 실시간 적용 메모리 예산 로깅 (`[Memory Governor] max_memory: X.XGB (65% of RAM), work_mem: 64MB`).
  3. **대량 조인(`HashJoinIterator`) Fail-Fast 회로 차단기 (Circuit Breaker)**:
     - 복잡한 디스크 스필러 오버엔지니어링(YAGNI, Golden Rule 15)을 배제하고, Build 단계에서 `buildMemory > workMem` 도달 시 즉시 안전한 중단 및 에러 반환 (`hash join memory budget exceeded: ... > work_mem`).
     - 비정상적인 카테시안 조인 또는 초대용량 해시 조인으로부터 다른 정상 OLTP 쿼리들을 완벽히 격리 보호.

#### 2. [Post-1.0 (v1.1.0) 연기] 엔터프라이즈 암호학적 불변 감사 로그 서브시스템 구축 (`pkg/audit`, `AUDIT-LOG-1`)
* **마일스톤 분류**: 1.0 LTS 출시 기준 일원화(Gate 4는 기존 TDE, RBAC, HTTP 감사 추적으로 충족)에 따라 **Post-1.0 (`v1.1.0`)** 마일스톤으로 연기 확정.
* **표준 규격**: PCI-DSS v4.0 Requirement 10, SOC 2 Type II, HIPAA §164.312(b) ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md))
* **핵심 아키텍처 및 구현 요구사항**:
  1. **SHA-256 해시 체이닝 (Cryptographic Hash Chaining)**:
     - 각 감사 레코드는 이전 레코드의 해시값을 포함하여 연결 ($H_n = \text{SHA256}(H_{n-1} \parallel R_n)$).
     - 로그 파일 임의 변조, 행 삭제, 재정렬 시 오프라인 검증기에서 $100\%$ 즉각 탐지.
  2. **비차단 MPSC 링 버퍼 (Non-Blocking Ring Buffer, Golden Rule 29)**:
     - 트랜잭션 커밋 핫패스에서 디스크 I/O를 직접 수행하지 않고 인메모리 링 버퍼로 원자적 전달.
     - 전용 백그라운드 플러셔 goroutine이 배치 단위 fsync 기록하여 OLTP 트랜잭션 지연 영향 $0\text{ms}$ 보장.
  3. **감사 대상 이벤트 태깅**:
     - 인증 이벤트: 로그인 성공/실패, 토큰 발급/폐기, 패스워드 변경.
     - 권한 제어: `CREATE LOGIN/USER`, `GRANT`, `REVOKE`, `DENY`.
     - 스키마 변경: `CREATE/ALTER/DROP TABLE`, `INDEX`, `PROCEDURE`, `VIEW`.
     - 데이터 조작(선택적): 슈퍼유저 DML, 테이블 벌크 반출입.
  4. **독립 CLI 무결성 검증기**:
     - `lionsql audit verify -file=<audit.log>` 명령어를 통해 외부 감사인이 오프라인에서 암호학적 무결성 검증 가능.

#### 3. 클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 (`pkg/storage/s3sign`, `pkg/config`, `pkg/api`, `pkg/proc`)
* **과제 개요**: 대용량 바이너리 스트리밍 및 이미지 썸네일 처리를 DB 커널에서 배제(Spec-Out, `GEMINI.md` 8.7절)하고, 클라이언트가 외부 S3/R2/MinIO로 직접 업로드하도록 지원하는 제로-의존성 순수 Go SigV4 서명 서브시스템 구축.
* **사전 구현 세부 요구사항 (Phase 1 ~ Phase 4)**:
  1. **제로-의존성 경량 AWS SigV4 서명기 (`pkg/storage/s3sign`)**:
     - 외부 AWS SDK(수십 MB) 없이 Go 표준 `crypto/hmac`, `crypto/sha256`만으로 Canonical Request, StringToSign, 다단계 파생 서명 키 계산.
     - `SignPresignedPUT(bucket, key, contentType, expiresIn)` 및 `SignPresignedGET(bucket, key, expiresIn)` 구현.
     - AWS 공식 Canonical Test Suite 기반 100% 호환성 단위 테스트 검증.
  2. **통합 스토리지 설정 서브시스템 (`pkg/config`)**:
     - `S3Config` (endpoint, region, access_key_id, secret_access_key, default_bucket, force_path_style, cdn_base_url) 구조체 및 환경 변수 바인딩 (`LIONSQL_S3_*`).
  3. **HTTP REST Pre-signed 발급 API 엔드포인트 (`pkg/api/handlers_storage.go`)**:
     - `POST /api/storage/presign` 엔드포인트 (JWT/API-Key RBAC 인증).
     - 업로드용 `upload_url`, 조회용 `public_url`, 유효기간 반환 및 Content-Length/MIME 타입 화이트리스트 가드.
  4. **T-SQL 내장 스칼라 함수 확장 (`pkg/proc/builtins_s3.go`, `pkg/engine`)**:
     - `S3_PRESIGNED_PUT_URL('avatars/user.png')` 및 `S3_PRESIGNED_GET_URL(...)` 스칼라 함수 등록.
     - Stored Procedure 및 대화형 쿼리에서 직접 업로드 링크 생성 지원.

#### 4. 크리티컬 패스 5대 무할당 최적화 완결 (A~E) (`PERF-1`) (`pkg/engine`, `pkg/wire/tds`)
* **과제 개요**: TDS 네트워크 수신부터 쿼리 분할, 파라미터 바인딩, 엔진 실행, 결과 행 직렬화에 이르는 핫패스 전수 진단 및 5대 병목(A~E) 전면 제거 (완료 `[x]`).
* **5대 최적화 상세 내역 및 벤치마크 성과**:
  1. **[A] 정규식 패키지 싱글톤 승격 (`pkg/engine`)**:
     - `execSelect`, `execCreateTable`, `execCreateView`, `execDropView`, `execAlterTable`, `execCreateStatistics` 내부 동적 `regexp.MustCompile`을 전면 패키지 레벨 `var`로 승격.
     - 쿼리당 15~30µs 컴파일 지연 및 15~25개 힙 객체 할당 100% 제거.
  2. **[B] 문장 분할기 인덱스 기반 제로-카피 슬라이스 스캐너 전면 재작성 (`pkg/engine/query_utils.go`)**:
     - 바이트 단위 `cur.String()` O(N^2) 검사 루프와 `strings.Builder`를 전면 폐기하고, 단일 쿼리 세미콜론 검사 Fast-Path 및 인덱스 슬라이싱(`query[start:i]`) 도입.
     - **단일 쿼리 분할 성능: 16.46 ns/op, 16 B/op, 1 allocs/op** (40KB 메모리 폭증 및 마이크로초 지연 완전 박멸).
  3. **[C] TDS 파라미터 바인딩 O(N) 단일 패스 치환기 (`pkg/wire/tds/server.go`)**:
     - `sp_executesql`, `sp_execute`에서 파라미터별 `regexp.MustCompile` 및 `[]rune` 변환을 제거하고 바이트 스캐너 기반 1-Pass 치환(`substituteParamsSinglePass`) 적용.
     - **3개 파라미터 바인딩 성능: 923.1 ns/op (50배 가속, 서브마이크로초 진입)**.
  4. **[D] TDS Row 스트리밍 직렬화 및 리플렉션 제거 (`pkg/wire/tds/token.go`, `server.go`)**:
     - `WriteRowDirect` 구현으로 행당 신규 슬라이스 할당 및 이중 복사 완전 제거 (**53.49 ns/op, 0 B/op, 0 allocs/op 달성**).
     - `writeColumnValue` 내 `binary.Write` 리플렉션을 스택 바이트 배열 기반 `binary.LittleEndian.PutUint16/32/64` 및 `math.Float64bits`로 교체하여 런타임 리플렉션 100% 제거.
     - 문자열 컬럼 `val.(string)` 타입 단언 선행으로 불필요한 `fmt.Sprintf` 우회.
  5. **[E] UTF-16LE / UTF-8 제로-할당 변환 (`pkg/wire/tds/packet.go`)**:
     - ASCII Fast-Path 도입으로 쿼리/메타데이터 송수신 시 중간 `[]rune`, `[]uint16` 슬라이스 제거.
     - **`EncodeUTF16LE_ASCII`: 121.9 ns/op, 192 B/op, 1 allocs/op** (3회 ➔ 1회 할당).
     - **`DecodeUTF16LE_ASCII`: 266.3 ns/op, 96 B/op, 1 allocs/op** (3회 ➔ 1회 할당).
* **재발 방지 3대 불변 원칙 수립**: [`notes/PITFALLS_AND_TRICKS.ko.md`](notes/PITFALLS_AND_TRICKS.ko.md) 13절에 유틸리티 침투 경계, 크리티컬 패스 정규식/바이트 빌더 금지, 벤치마크 미러링 불변 원칙 영구 명문화.

#### 5. EXPLAIN ANALYZE Volcano 런타임 실행 프로파일러 완결 (`OBS-2`) (`pkg/planner`, `pkg/execution`, `pkg/engine`)
* **과제 개요**: 기존 평면적 전체 쿼리 소요 시간 복사 방식을 탈피하고, Volcano 반복자(Iterator) 계층 구조에 결합된 실시간 연산자 단위 프로파일러 구축 (완료 `[x]`).
* **핵심 구현 내역**:
  1. **`InstrumentedIterator` 및 서브마이크로초 나노초 정밀 계측 (`pkg/execution/instrument.go`)**:
     - 개별 연산자의 첫 튜플 반환 시점(`ActualStartupUS`)과 전체 실행 시간(`ActualDurationUS`), 처리 행수(`ActualRows`), 반복 횟수(`ActualLoops`)를 실시간 집계.
     - 나노초(`totalNanos`) 누적으로 1µs 미만 초고속 연산자의 양자화 오차(0µs 절삭) 방지.
     - `Next()` 단일 튜플 및 `NextChunk()` 벡터화 배치 스트리밍 동시 지원.
  2. **재귀적 실행기 계측 빌더 (`pkg/execution/builder.go`)**:
     - `BuildInstrumentedExecutor`를 통해 Scan, Filter, Sort, Limit, HashJoin, MergeJoin, IndexScan 등 모든 Volcano 반복자를 `InstrumentedIterator`로 래핑.
     - `IndexNestedLoopJoinIterator` 내부 인덱스 시크 시 `innerPlan` 루프 횟수(`ActualLoops`) 및 누적 행수 연동.
  3. **쿼리 코디네이터 연동 및 안전한 폴백 (`pkg/engine/sqlexec_explain.go`)**:
     - `EXPLAIN ANALYZE` 실행 시 읽기 전용 트랜잭션에서 계측 실행기를 구동하여 청크를 소진(drain)하고 실행 트리 통계 수집.
     - Volcano 실행 불가 쿼리 시 안전하게 기존 `execSelect`로 폴백하는 무회귀 안전망 유지.
  4. **PostgreSQL 표준 포맷 호환성 (`pkg/planner/planner.go`, `explain.go`)**:
     - Text 형식: `(cost=0.00..0.00 rows=10 width=0) (actual time=0.000..0.250 rows=10 loops=1)`
     - JSON 형식: `"Actual Startup Time"`, `"Actual Total Time"`, `"Actual Rows"`, `"Actual Loops"` 완전 직렬화.

#### 6. LionSQL 1.0 릴리즈 사전 점검 및 정합성 액션 플랜 (`notes/RELEASE_1.0_REVIEW_AND_FEEDBACK.ko.md`)
* **과제 개요**: 1.0 LTS 정식 출시를 앞두고 실시된 전수 아키텍처 리뷰에서 도출된 핵심 개선 과제 (보안 이스케이프, 임시 객체 누수, 브랜드 단일화, 릴리즈 게이트 일치화).
* **세부 과제 및 진행 현황**:
  1. **[A] 세션 변수(`@var`) 치환 시 작은따옴표 이스케이프 (`SEC-ESC-1`, 완료 `[x]`)**:
     - `ParseSessionVarValue`의 SQL 리터럴(`'O''Reilly'`) 언이스케이프(`strings.ReplaceAll(inner, "''", "'")`) 보강.
     - 쿼리 내 `@var` 치환 시 문자열 리터럴에 대해 작은따옴표 이중화(`''`) 강제로 2차 SQL 인젝션 및 구문 파괴 방지.
  2. **[B] 테이블 변수(`TableVars`) 및 임시 테이블 생명주기 완전 정리 (`SESS-CLEAN-1`, 완료 `[x]`)**:
     - 세션 종료(`CloseSession`) 및 코디네이터 종료(`Close`) 시 `default` 세션을 포함하여 모든 `__tv_*` 물리 테이블 즉시 드롭.
     - 커넥션 풀 리셋(`DISCARD ALL`, `RESET ALL`) 실행 시 할당된 세션 임시 테이블과 테이블 변수를 물리 드롭하여 스토리지 누수 원천 차단.
  3. **[C] 웹훅 헤더, SDK 엔드포인트 및 잔여 브랜드 브랜드 완전 정돈 (`BRAND-UNIFY-1`, 완료 `[x]`)**:
     - `pkg/api/webhook.go`, `pkg/cdc/outbox_sinks.go`: 웹훅 헤더를 `X-LionSQL-Signature`, `X-LionSQL-Event`, `X-LionSQL-Delivery` 및 `LionSQL-Webhook/1.0`으로 완전 전환.
     - `pkg/api/sdk/`: `lionsql.js` 및 `lionsql.d.ts`로 정식 전환, 엔드포인트 `/sdk/lionsql.js` 신설 및 `class LionSQL`로 완전 일원화 (하위 호환 별칭 없이 단일화).
     - `DB_NAME()`, `APP_NAME()`, `SHOW server_version`, `SELECT @@VERSION`, TDS `BuildLoginAck`, `BuildError`, PG Wire ParameterStatus, TLS 인증서 Organization의 잔여 명칭을 `LionSQL`로 완전 일원화.
  4. **[D] 1.0 릴리즈 게이트 4 기준 일치화 및 문서 권한 정규화 (`REL-ALIGN-1`, 완료 `[x]`)**:
     - `ROADMAP.md` Gate 4를 엔터프라이즈 보안 및 거버넌스(TDE, RBAC, 감사 추적)로 1.0 충족 확정하고 암호학적 불변 감사 로그(`pkg/audit`)를 Post-1.0(`v1.1.0`)으로 연기 일원화.
     - `doc/README.md`, `doc/REFERENCE_MANUAL.ko.md` 파일 퍼미션 `0644` 정규화 완료 (Golden Rule 5).
  5. **[E] 외부 인터페이스 바인딩 시 프로덕션 보안 가드레일 (`PROD-SEC-1`, 완료 `[x]`)**:
     - `pkg/config`: `IsLoopbackHost`, `NonLoopbackServiceAddresses`, `ValidateSecurityGuard` 구현.
     - `127.0.0.1` / `localhost` 바인딩일 때만 기본 비밀번호(`1234567890`) 허용 (Zero-config 편의성 유지).
     - `0.0.0.0`, `:port`, 공용 IP 등 외부 노출 인터페이스 바인딩 시 기본 비밀번호 기동 즉각 차단 및 조치 가이드라인 출력 (`os.Exit(1)`).
     - 환경 변수 `LIONSQL_ADMIN_PASS` 지원 추가.
     - `lionsql doctor`에 `Production Security Guard` 진단 항목 신설 (외부 노출 시 FAIL, 로컬호스트 또는 비밀번호 변경 시 OK 판정).
     - `pkg/config/config_test.go` 단위 테스트 100% 검증 통과.

#### 7. TDS 프로토콜 ➔ 코디네이터 ➔ VDBE ➔ 스토리지 전 계층 수직 성능 최적화 (`VPERF-1` ~ `VPERF-14`)
* **과제 개요**: TDS 네트워크 패킷 수신부(`pkg/wire/tds`)부터 SQL 코디네이터(`pkg/engine`), VDBE 바이트코드 VM(`pkg/proc/runtime`), 스토리지 엔진(`memengine`, `wiredtiger`)까지 수직 핫패스(Vertical Hot-Path)의 병목을 전면 제거.
* **Track A — 스토리지 & 코디네이터 치명적 병목 즉시 해소 (`VPERF-1` ~ `VPERF-4`)**:
  1. **`VPERF-1` (`pkg/storage/memengine/tx.go`, `memengine.go`, 완료 `[x]`)**: `MEM-3` 메모리 가버너 활성 시 매 쓰기 트랜잭션 `Commit()`마다 런타임 메트릭스 및 슬라이스 힙 할당을 수행하던 병목을 원자적 캐시(`cachedHeapBytes`) 및 128회 주기 샘플링(`commitCounter`) 방식으로 교체하여 커밋 핫패스 $O(1)$(1~2ns) 무할당 전환 완료.
  2. **`VPERF-2` (`pkg/engine`, `pkg/wire/tds`, 완료 `[x]`)**: Autocommit 쿼리(`tryPKLookupBypass`, `execVolcanoSelect`, `execInsert`, `execUpdate`, TDS `handleRPCExecute`)마다 `engine.NewSession()` 생성 및 `Close()`로 인해 WiredTiger 세션별 `WT_CURSOR` 풀이 매번 파괴되는 문제를 `ClientSession.Session`(또는 커넥션별 영속 세션) 재사용으로 해결.
  3. **`VPERF-3` (`pkg/engine/sqlexec_dml.go`, `sqlexec_ddl.go`, 완료 `[x]`)**: `AUTO_INCREMENT`(`IDENTITY`) 컬럼 보유 테이블에 대한 `execInsert` / `execInsertSelect` 시 매번 전체 테이블을 풀 스캔(`tx.Scan`)하던 $O(N)$ 병목을 코디네이터 메모리 시퀀스 캐시(`GetTableLastIdentity`) 기반 $O(1)$ 즉시 채번으로 교체 및 `TRUNCATE`/`DROP` 시 0 리셋 연동 완료.
  4. **`VPERF-4` (`pkg/storage/memengine/tx.go`, `memengine.go`, 완료 `[x]`)**: `memengine`의 매 `Update` / `Delete`마다 `engine.mu.RLock()`을 획득하고 전체 테이블 맵을 순회하며 역방향 외래키(FK)를 검사하는 오버헤드를 DDL 시점 사전 계산된 역방향 외래키 인덱스 캐시(`fkCache`)로 교체하여 $O(1)$ 단락 처리.
* **Track B — TDS & VDBE 저위험 무할당 튜닝 (`VPERF-7` ~ `VPERF-9`, `VPERF-13`, `VPERF-14`)**:
  5. **`VPERF-7` (`pkg/wire/tds/packet.go`, `token.go`, `server.go`, 완료 `[x]`)**: `WritePacket`의 `4096` 하드코딩을 세션 협상 `PacketSize`(최대 32KB)로 연동하고, 응답 `bytes.Buffer` 재사용 및 `WriteUTF16LEDirect` 직접 버퍼 쓰기로 셀당 슬라이스 할당 제거.
  6. **`VPERF-8` (`pkg/proc/runtime/vm.go`, 완료 `[x]`)**: `PlanCache.GetOrCompile` 조회 시 매번 수행되는 `sha256.Sum256` + `hex.EncodeToString` 해싱과 배타적 쓰기 락(`mu.Lock()`) 경합을 원문 문자열 키 및 `RLock` 패스트패스로 개선.
  7. **`VPERF-9` (`pkg/proc/runtime/vm_dispatcher.go`, `vm.go`, 완료 `[x]`)**: VDBE `OpInsert`/`OpUpdate` 시 트리거가 없는 테이블에 대한 무조건적인 `rowMap`/`oldRow`/`newRow` 할당을 생략하고, `execUpdateWhere` 내부 할당 루프의 `buildRowEvalMap` 중복 호출을 루프 외부로 호이스팅.
  8. **`VPERF-13` (`pkg/engine/sqlexec_dml.go`, 완료 `[x]`)**: `INSERT ... ON CONFLICT (col) DO UPDATE/NOTHING` 처리 시 충돌 대상 컬럼에 보조 유니크/B-Tree 인덱스가 존재함에도 불구하고 무조건 $O(N)$ 전체 테이블 스캔(`tx.Scan`)으로 폴백하던 성능 결함을 `schema.FindIndexByColumn` 검사 후 `tx.IndexScan` 단건 룩업으로 즉시 최적화.
  9. **`VPERF-14` (`pkg/proc/runtime/vm.go`, 완료 `[x]`)**: VDBE `execUpdateWhere` 및 `execDeleteWhere`에서 `for cursor.Next()` 행 스캔 루프 내부에서 매 행마다 `tg := f.executor.getTableGetter()` 및 `schema, err := tg(data.Table)` 카탈로그 조회를 반복 수행하여 카탈로그 뮤텍스 락 경합을 유발하던 루프 불변식(Loop Invariant)을 루프 외측으로 1회 호이스팅.
* **Track C — [Post-1.0 (`v1.1.0`) 연기] 고위험 구조 개편 및 스트리밍 (`VPERF-5`, `VPERF-6`, `VPERF-10` ~ `VPERF-12`)**:
  10. **`VPERF-5` (`pkg/wire/tds/server.go`)**: `runQueryWithAttention` 취소 감지 고루틴/채널 제거 (TDS 취소 프로토콜 상태머신 회귀 방지를 위해 v1.1.0 이관).
  11. **`VPERF-6` (`pkg/wire/tds/server.go`)**: `sp_executesql`(ProcID 10) VDBE 직결 (클라이언트 드라이버 RPC 호환성 보호를 위해 v1.1.0 이관).
  12. **`VPERF-10` (`pkg/engine`, `pkg/wire/tds`)**: `Row -> Tuple -> map[string]any -> TDS Wire` 맵 제거 및 `RawRows` 슬라이스 직결 (전체 프로토콜/엔진 공용 구조체 영향 최소화를 위해 v1.1.0 이관).
  13. **`VPERF-11` (`pkg/storage/memengine/tx.go`)**: In-Memory OCC `Scan()` 샤드 스트리밍 커서 전환 (검증된 TPC-C 20k TPS 커서 무결성 보호를 위해 v1.1.0 이관).
  14. **`VPERF-12` (`pkg/storage/wiredtiger/wt_engine.go`)**: WiredTiger `updateLocked` 단일 커서화 및 Zero-Copy `unsafe.Slice` 디코딩 (CGo 포인터 생명주기 세그폴트 위험 차단을 위해 v1.1.0 이관).

#### 8. 데이터 무결성, 대소문자 정합성, 임의 추측 및 무작위 맵 순회 107대 결함 전수 조치 아카이브 (`BUG-107`)
* **과제 개요**: SQL 표준의 식별자 대소문자 무관성(Case-insensitivity)과 Go 맵/스칼라의 대소문자 엄격 비교 불일치, 그리고 식별자/인덱스 조회 실패 시의 취약한 무작위 순회 폴백(`for _, v := range rec/row/map { break }`) 및 첫 번째 요소 임의 대입(`[0]`), 외래키 무검증/오판, NULL 전파 누락, WAL 복구 절단 처리 실패, Int-Float 및 DateTime-VarChar 비교 역전, 파생테이블 조인 프로젝션 유실, 혼합타입 집계합 누락, 기본값(DEFAULT) 누락, 관리자 인증 우회, MERGE IDENTITY 누락, 복합 PK 파싱 절단, COPY 프로토콜 세션 우회 및 데이터 변조, 쉼표 오절단, RBAC 커스텀 롤 누락, 따옴표 이스케이프 분해 파괴, 샤드 초기화 경쟁 크래시, VDBE 미존재 PK 크래시, EXEC 프로시저 및 TDS RPC 트랜잭션 우회, Simple CASE 누락, 산술식 0값 변조, 윈도우 함수 RANGE 피어 누락, OCC 동일 트랜잭션 유니크 충돌 사일런트 커밋, HTTP API DATETIME 빈 문자열 방출, PITR DDL 에러 침묵 등으로 인해 발생하는 107대 치명적 무결성 결함을 원천 해결 완료.
* **조치 이력 및 세부 내역 아카이브**:
  - 본 107대 결함 트랙은 LionSQL의 데이터 무결성 조기 안정화를 위해 추진된 중간 단계 방법론(Interim Methodology)으로 100% 조치 완료(`[x]`)되었습니다.
  - 107개 결함의 상세 파일 경로, 버그 발생 원인, 수정 코드 및 증적 일람은 별도의 독립 아카이브 문서인 [**`notes/BUG_ARCHIVE.ko.md`**](notes/BUG_ARCHIVE.ko.md)로 분리 이관되었습니다.
  - 향후 결함 감사, 정적 분석 및 구조적 리팩토링은 [`CODE_REVIEW.ko.md`](CODE_REVIEW.ko.md) 및 [`REFACTORING.ko.md`](REFACTORING.ko.md)에서 지속 관리됩니다.

---

## 3. 도메인별 통합 백로그 매트릭스 (Domain Backlog Matrices)

### 3.1. CBO & 쿼리 최적화 트랙 (`A` ~ `D`)

| 번호 | 과제명 | 구분 | 상태 | 구현 내용 요약 및 전략적 가치 |
| :---: | :--- | :---: | :---: | :--- |
| **A-1** | `ANALYZE TABLE` 멀티코어 병렬 통계 수집 | 통계 수집 | **완료 (`[x]`)** | `sync.WaitGroup` 기반 멀티코어 병렬화로 통계 수집 4~8배 가속 |
| **A-2** | CBO 통계 메타데이터 디스크 영속화 | 통계 수집 | **완료 (`[x]`)** | `cbo_stats.json` 원자적 파일 쓰기 및 기동 시 자동 복원 연동 |
| **B-3** | 소트-머지 조인 (Sort-Merge Join) 연산자 | 조인 엔진 | **완료 (`[x]`)** | 사전 정렬된 관계에 대해 $O(M+N)$ 단일 패스 해시리스 스트리밍 조인 |
| **B-4** | $N > 4$ 체인 조인 동적 계획법 (Selinger DP) | 조인 최적화 | **완료 (`[x]`)** | Left-Deep DP 비트마스크 메모이제이션으로 $O(N!)$ 순열 폭증 방지 |
| **C-5** | 다중 컬럼 확장 통계 (Extended Stats) | 선택도 모델 | **완료 (`[x]`)** | 다중 컬럼 결합 NDV 및 함수적 종속성($dep$) 모델링으로 과소평가 제거 |
| **C-6** | OR 조건 및 불리언 트리 선택도 계산 | 선택도 모델 | **완료 (`[x]`)** | AST 재귀 평가, 배타합/독립합집합, NOT, IN, BETWEEN 정밀 산출 |
| **C-7** | MCV (Most Common Values) 최빈값 목록 결합 | 선택도 모델 | **완료 (`[x]`)** | Top-20 최빈값 분리 보관 및 비-MCV 롱테일 히스토그램 이원화 |
| **D-8** | 백그라운드 자동 통계 갱신 데몬 | 운영 제어 | **완료 (`[x]`)** | DML 변동량(Dirty Tuples) 임계치 도달 시 비동기 `ANALYZE` 자동 트리거 |
| **D-9** | CBO 옵티마이저 힌트 오버라이드 제어 | 운영 제어 | **완료 (`[x]`)** | `OPTION (HASH/LOOP/MERGE JOIN)`, `WITH (INDEX(...))` 계획 강제 반영 |

---

### 3.2. 엔진 성능 및 무할당 풀링 트랙 (`P-1` ~ `P-17`, `VPERF-1` ~ `VPERF-14`)

> [!NOTE]
> 과거 데이터 무결성 및 대소문자 정합성 107대 결함 조치 내역(`BUG-PK-1` ~ `BUG-PITR-DDL-SILENT-SWALLOW-1`)은 전수 완료되어 별도 아카이브인 [**`notes/BUG_ARCHIVE.ko.md`**](notes/BUG_ARCHIVE.ko.md)로 이관되었습니다.

| 번호 | 전략 분야 | 핵심 구현 과제 | 상태 | 구현 성과 및 최적화 효과 |
| :---: | :--- | :--- | :---: | :--- |
| **P-1** | 스토리지 | 배치형 그룹 커밋 & 무경합 Fast-Path | **완료 (`[x]`)** | `FsyncAlways` 지연 100배 단축(0.038ms/op), TPC-C 2.8k TPS 기여 |
| **P-2** | 디코딩 | 바이너리 튜플 지연 역직렬화 & 커버링 인덱스 | **완료 (`[x]`)** | `SkipTupleColumn`, $O(1)$ `LazyTuple`, Index-Only Scan 100% 힙 바이패스 |
| **P-3** | 와이어 | 제로카피 와이어 인코딩 & I/O 버퍼 결합 (`PERF-1`) | **완료 (`[x]`)** | `WriteNBCRowDirect` & `WriteRowDirect` 직결, 리플렉션 제거로 행당 **0 B/op, 0 allocs/op (53ns)** 달성 |
| **P-4** | 실행 엔진 | 벡터화 청크 반복자 (Chunked Batch Next) | **완료 (`[x]`)** | 1,024건 튜플 배치 스트리밍으로 가상 함수 디스패치 오버헤드 99% 제거 |
| **P-5** | 캐시 | 고속 SQL 지문 해싱 & 단순 PK 조회 바이패스 | **완료 (`[x]`)** | FNV-1a 지문 해싱으로 CBO/VDBE 우회, 단순 PK 룩업 TPS 50%+ 향상 |
| **P-6** | 파서/엔진 | 핫패스 정규식 사전 컴파일 및 캐싱 (`PERF-1`) | **완료 (`[x]`)** | sqlexec/select/ddl 정규식 패키지 싱글톤 승격으로 쿼리당 15~30µs 힙 할당 제거 |
| **P-7** | JSON | 반정형 JSON 동일 튜플 DOM 캐시 & 키 스캐너 | **완료 (`[x]`)** | `ScanJSONTopLevelKey` 무할당 스캐너 및 1,024건 DOM FIFO 캐시 |
| **P-8** | 스토리지 | WiredTiger 커서 풀링 & `cur->reset` 재사용 | **완료 (`[x]`)** | 세션별 커서 풀링 및 C 헬퍼 단일화로 894ns 룩업(1.13M+ OPS) 달성 |
| **P-9** | 옵티마이저 | 정형 SQL Parameterized 실행 계획 캐시 | **보류 (`[-]`)** | SP/Prepared/PK 바이패스로 고주파 경로가 기충족되어 보류 |
| **P-10** | 실행기 | Volcano 대규모 분석 쿼리 멀티코어 병렬 실행기 | **보류 (`[-]`)** | OLTP 동시성 간섭 방지를 위해 1.0 보류 (v1.2+ 이관) |
| **P-11** | 스토리지 | In-Memory OCC 트랜잭션 객체 & 맵 풀링 | **완료 (`[x]`)** | `tx.workspaces` 맵 `clear()` 재사용으로 트랜잭션 시작 할당 0건화 (20k TPS) |
| **P-12** | 실행기 | Volcano 벡터화 배치 청크(`Chunk`) 풀링 | **완료 (`[x]`)** | `sync.Pool` 기반 1,024건 청크 재활용으로 대량 스캔 GC 부하 극소화 |
| **P-13** | 실행기 | DML Record(Map) 제거 ➔ Row(Slice) 직결 | **보류 (`[-]`)** | 외래키 연쇄 및 제약조건 엔진 회귀 위험으로 공식 보류 |
| **P-14** | 와이어 | Prepared Statement 바이너리 바인드 직접 디코딩 | **완료 (`[x]`)** | 중간 문자열 변환 제거 및 64개 정적 파라미터명 슬라이스 풀링 |
| **P-15** | 스토리지 | WiredTiger 다중 행 배치 CGo 삽입 | **보류 (`[-]`)** | CGo 포인터 세그폴트 및 트랜잭션 롤백 복잡성으로 보류 |
| **P-16** | 스토리지 | In-Memory OCC Row-Level Atomic CAS 커밋 | **보류 (`[-]`)** | 동시성 엣지 케이스 및 데이터 사일런트 손상 위험으로 보류 |
| **P-17** | VM | VDBE 컬럼 산술 융합 슈퍼 명령어 (`OpIncrColInt`) | **보류 (`[-]`)** | 바이트코드 점프 타깃(PC) 오프셋 불일치 및 VM 복잡성으로 보류 |
| **VPERF-1** | 스토리지 | In-Memory OCC 커밋 시 `runtime.ReadMemStats` 전역 STW 제거 | **완료 (`[x]`)** | 원자적 힙 메모리 캐시/샘플링(128회 주기)으로 쓰기 트랜잭션 커밋 STW 및 슬라이스 힙 할당 100% 제거 |
| **VPERF-2** | 코디네이터/WT | Autocommit 쿼리 `ClientSession.Session` 재사용으로 WT 커서 풀 보존 | **완료 (`[x]`)** | 매 쿼리 `NewSession()`/`Close()` 호출 제거로 CGo 세션 생성 및 `WT_CURSOR` 파괴 방지 |
| **VPERF-3** | 엔진/DML | `IDENTITY` (`AUTO_INCREMENT`) 삽입 시 전체 테이블 스캔($O(N)$) 제거 | **완료 (`[x]`)** | `execInsert` 시 `tx.Scan` 풀 스캔을 $O(1)$ 원자적 시퀀스 카운터로 교체 |
| **VPERF-4** | 스토리지 | In-Memory OCC `Update`/`Delete` 역방향 외래키(FK) 참조 사전 캐싱 | **완료 (`[x]`)** | 매 수정/삭제마다 `engine.mu.RLock()` 및 전체 테이블 순회 오버헤드 100% 생략 |
| **VPERF-5** | 와이어/TDS | `runQueryWithAttention` 쿼리당 고루틴 및 채널 생성 제거 | **Post-1.0 (`v1.1.0`)** | TDS 취소 프로토콜 상태머신 회귀 방지 위해 v1.1.0 이관 |
| **VPERF-6** | 와이어/TDS | `sp_executesql` (ProcID 10) VDBE `BytecodePlanCache` 직결 | **Post-1.0 (`v1.1.0`)** | 클라이언트 드라이버 RPC 호환성 보호 위해 v1.1.0 이관 |
| **VPERF-7** | 와이어/TDS | 협상된 `PacketSize` 동적 적용 및 `WriteUTF16LEDirect` 직접 버퍼 기록 | **완료 (`[x]`)** | 최대 32KB 패킷 활용으로 `Write` 시스콜 8배 절감 및 셀당 UTF-16LE 슬라이스 할당 제거 |
| **VPERF-8** | VM/캐시 | `PlanCache.GetOrCompile` SHA-256 해싱 제거 및 `RLock` 패스트패스 | **완료 (`[x]`)** | 원문 문자열 키 및 읽기 락 조회로 멀티코어 바이트코드 플랜 캐시 락 경합 해소 |
| **VPERF-9** | VM/DML | VDBE 무트리거 테이블 맵 할당 생략 및 `buildRowEvalMap` 루프 호이스팅 | **완료 (`[x]`)** | 트리거 미보유 테이블 DML 시 `rowMap`/`oldRow`/`newRow` 할당 생략 및 중복 맵 생성 제거 |
| **VPERF-10** | 엔진/와이어 | `RawRows` 슬라이스 직결 및 스키마 `ColumnTypes` 전달로 맵/타입 재추론 제거 | **Post-1.0 (`v1.1.0`)** | 전체 프로토콜/엔진 공용 구조체 영향 최소화 위해 v1.1.0 이관 |
| **VPERF-11** | 스토리지 | In-Memory OCC `Scan()` 샤드 스트리밍 커서 전환 | **Post-1.0 (`v1.1.0`)** | 검증된 TPC-C 20k TPS 커서 무결성 보호 위해 v1.1.0 이관 |
| **VPERF-12** | 스토리지/WT | WiredTiger `Update` 단일 커서 통합 및 Zero-Copy 슬라이스 뷰 디코딩 | **Post-1.0 (`v1.1.0`)** | CGo 포인터 생명주기 세그폴트 위험 차단 위해 v1.1.0 이관 |
| **VPERF-13** | 엔진/DML | `INSERT ... ON CONFLICT (col)` 보조 인덱스 스캔(`IndexScan`) 연동 | **완료 (`[x]`)** | 인덱스 보유 컬럼에 대한 Upsert 시 $O(N)$ 전체 테이블 풀 스캔 방지 및 `IndexScan` 즉시 룩업 |
| **VPERF-14** | VM/DML | VDBE `execUpdateWhere`/`execDeleteWhere` 카탈로그 스키마 조회 호이스팅 | **완료 (`[x]`)** | `cursor.Next()` 루프 내 매 행마다 발생하던 `getTableGetter()` 및 카탈로그 뮤텍스 락 경합 100% 제거 |

---

### 3.3. 코드베이스 아키텍처 리팩토링 트랙 (`R-1` ~ `R-5`)

| 번호 | 영역 | 핵심 리팩토링 과제 | 상태 | 목표 및 엔지니어링 성과 |
| :---: | :--- | :--- | :---: | :--- |
| **R-1** | 카탈로그 | 가상 시스템 카탈로그 모듈 분리 및 레지스트리화 | **완료 (`[x]`)** | 3,284줄 모놀리식 파일을 PG/MSSQL/ANSI 4개 도메인으로 분해, $O(1)$ 라우팅 |
| **R-2** | 파서 | Pratt 파서 및 내장 함수 도메인별 관심사 분리 | **완료 (`[x]`)** | `parser.go`(3.2k줄 ➔ 5개 파일), `builtins.go`(2.8k줄 ➔ 6개 파일) 모듈화 |
| **R-3** | 플래너 | 단일 테이블 패스트패스 vs Volcano 바운드 일원화 | **완료 (`[x]`)** | `pkg/planner/bounds.go` 공통 모듈 신설로 260줄 중복 제거 (DRY) |
| **R-4** | 스토리지 | In-Memory OCC 트랜잭션 라이프사이클 모듈 분해 | **Post-1.0 (`[-]`)** | 1.0 안정성 최우선. 동시성 검증 완료 상태 보존을 위해 v1.1+ 이관 |
| **R-5** | 스토리지 | WiredTiger CGo 바인딩 격리 및 트랜잭션 분해 | **Post-1.0 (`[-]`)** | 1.0 안정성 최우선. CGo 빌드 태그 복잡도 방지를 위해 v1.1+ 이관 |

---

### 3.4. 클라우드 오브젝트 스토리지 & Pre-signed URL 연동 트랙 (`S3-1` ~ `S3-4`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **S3-1** | 스토리지 | 제로-의존성 경량 AWS SigV4 서명기 구축 | **Post-1.0 연기 (Postponed)** | 순수 Go 표준 라이브러리(`crypto/hmac`, `crypto/sha256`) 기반 Canonical Request & 다단계 파생 키 서명 (`pkg/storage/s3sign`) |
| **S3-2** | 설정/API | S3 스토리지 설정 및 HTTP REST Pre-signed API | **Post-1.0 연기 (Postponed)** | `POST /api/storage/presign` 엔드포인트, MIME/크기 가드, `S3Config` 환경 변수 연동 (`pkg/config`, `pkg/api`) |
| **S3-3** | 파서/엔진 | T-SQL Pre-signed URL 내장 스칼라 함수 등록 | **Post-1.0 연기 (Postponed)** | `S3_PRESIGNED_PUT_URL`, `S3_PRESIGNED_GET_URL` 내장 함수 및 SP 연동 (`pkg/proc`, `pkg/engine`) |
| **S3-4** | UI/스튜디오 | 웹 관리자 스튜디오(Web Studio) 파일 업로드 위젯 | **Post-1.0 연기 (Postponed)** | 레코드 편집 모달 내 브라우저 ➔ S3 직접 업로드 및 URL 자동 입력 위젯 연동 (`pkg/ui`) |

---

### 3.5. 엔터프라이즈 관제 및 쿼리 프로파일링 트랙 (`OBS-1` ~ `OBS-3`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **OBS-1** | 관제/메트릭 | Prometheus / OpenTelemetry 표준 관제 메트릭 엔드포인트 | **완료 (`[x]`)** | `/metrics` Prometheus 텍스트 규격 확장 (스토리지 엔진, 복제 상태/LSN/랙, 충돌 트랜잭션 수, 메모리 상한) 및 `/v1/metrics`, `/api/v1/metrics` OpenTelemetry OTLP JSON 엔드포인트 완비 (`pkg/engine`, `pkg/api`) |
| **OBS-2** | 최적화/DX | `EXPLAIN ANALYZE` 런타임 실행 프로파일러 | **완료 (`[x]`)** | Volcano Iterator 계층별 `InstrumentedIterator` 래핑, 노드별 독립 실행 시간(Actual Startup Time / Actual Duration), 처리 행수(Actual Rows), 실행 횟수(Actual Loops) 프로파일링 및 PostgreSQL 표준 Text/JSON 포맷 출력 완비 (`pkg/planner`, `pkg/execution`, `pkg/engine`) |
| **OBS-3** | 운영/알림 | 슬로우 쿼리 실시간 웹훅 알림 (Slack / Discord / Webhook) | **스펙-아웃 (Spec-Out)** | DB 커널 직접 발송 배제. 외부 Prometheus/Alertmanager 위임 표준화 및 Post-1.0 외부 툴링 아이디어로 검토 (`GEMINI.md` 8.9절) |

---

### 3.6. 프로토콜 보안 및 스토리지 가드 트랙 (`SEC-1`, `MEM-3`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **SEC-1** | 와이어/보안 | TDS 8.0 / Pre-Login SSL/TLS 암호화 핸드셰이크 보강 | **Post-1.0 연기 (Postponed)** | MS-TDS 2.2.6.4 Pre-Login 토큰 내 암호화 협상 플래그 교환 및 엄격 암호화 핸드셰이크 완비 (`pkg/wire/tds`) |
| **MEM-3** | 스토리지/가드 | In-Memory OCC 엔진 데이터 용량 한도 가드 (`memengine`) | **완료 (`[x]`)** | `runtime/metrics` 기반 STW-free 실시간 힙 측정, 트랜잭션 커밋 시 상한 초과 방어 및 `storage.ErrTableFull` 페일세이프 반환 완비 (`pkg/storage/memengine`, `cmd/server`) |

---

### 3.7. 아키텍처 단순화 및 다이어트 트랙 (KISS & Deletion-First, `KISS-1` ~ `KISS-5`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **KISS-1** | KISS/다이어트 | 미사용 구형 JS 트랜스파일러 및 `JSCode` 잔재 완전 제거 | **완료 (`[x]`)** | VDBE VM 전환 이후 미사용 상태인 `pkg/proc/transpiler.go`(782줄) 및 테스트(387줄) 완전 삭제(총 1,169줄 제거), `catalog.Procedure.JSCode` 등 잔재 필드를 직관적인 `BytecodeDisasm` 메타데이터로 정리 완비 |
| **KISS-2** | KISS/중복제거 | 가상 PG 카탈로그 및 information_schema 생성기 중복 단일화 | **완료 (`[x]`)** | `pkg/engine/sqlexec_pgcatalog.go`(1,102줄) 및 `sqlexec_info_schema.go`(588줄)를 완전 삭제하고 `pkg/catalog/virtual/`(`pg.go`, `info_schema.go`)로 일원화. `sqlexec_dbcc.go`는 `virtual.IsPgCatalogTable` 및 `virtual.ExecPgCatalogSelect`를 호출하도록 위임하여 순수 중복 제거 및 단일 책임 원칙(SRP) 완비 |
| **KISS-3** | KISS/파서통합 | `sqlexec` 하위 정규식 파싱 계층 제거 및 AST 파서 일원화 | **연기 (Postponed)** | `sqlexec_eval.go`, `sqlexec_pivot.go` 등의 정규식 분할 로직을 `pkg/proc` Pratt AST 소비로 통합하는 과제로, 25개 패키지 및 E2E 드라이버 호환성 회귀 방지를 위해 Post-1.0으로 연기 |
| **KISS-4** | KISS/자료구조 | In-Memory SkipList 내부 뮤텍스 락 제거 및 Go 표준 PRNG 간소화 | **완료 (`[x]`)** | `pkg/storage/memengine/skiplist.go`의 `rndMu sync.Mutex` 및 `rnd *rand.Rand`를 완전 삭제하고 Go 표준 `math/rand/v2`로 전환하여 이중 락 중첩 제거 및 락-프리/제로-할당 난수 생성 구현 완비 |
| **KISS-5** | KISS/실행통합 | 3갈래 쿼리 실행 경로 단일화 (`sqlexec_select` ➔ `execution` Iterator) | **연기 (Postponed)** | `sqlexec_select.go`(2,308줄)의 인메모리 조인/소팅 인터프리터를 `pkg/execution` Iterator로 통합하는 과제로, 단일 테이블 패스트패스 및 TPC-C 20k TPS 성능 안정성 유지를 위해 Post-1.0으로 연기 |

---

### 3.8. 크리티컬 패스 가속 및 무할당 스캐너 트랙 (`PERF-1`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **PERF-1** | 성능/크리티컬패스 | 문장 분할기 및 TDS 파라미터 바인딩 가속 | **완료 (`[x]`)** | `SplitStatements`의 `strings.Builder` 및 매 바이트 O(N^2) 검사를 제거하고 인덱스 슬라이싱(`query[start:i]`) 기반 제로-카피 스캐너로 전면 재작성(16ns/op, 1 alloc). 함수 내부 동적 `regexp.MustCompile` 전역 `var` 승격. `sp_executesql` 1-Pass 단일 스캔 파라미터 바인딩으로 50배 가속(<1µs) 및 재발 방지 3원칙 문서화 완비 (`pkg/engine`, `pkg/wire/tds`, `notes/PITFALLS_AND_TRICKS.ko.md`) |

---

### 3.9. 다국어 로케일 및 정렬 호환 트랙 (Collation & Locale, `LOC-1` ~ `LOC-4`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **LOC-1** | 파서/DDL | DDL 및 쿼리 표현식 내 `COLLATE` 구문 관용 파싱 | **Post-1.0 연기 (Postponed)** | `CREATE TABLE` 컬럼 정의 및 `WHERE`/`ORDER BY` 내 `COLLATE <name>` 관용 수용 (마이그레이션 DDL 에러 원천 방지, `_BIN`/`_BIN2` 엄격 비교 분기) (`pkg/proc/lexer.go`, `pkg/proc/parser.go`) |
| **LOC-2** | 설정/와이어 | 동적 Collation 설정 (`--collation`, `config.toml`) | **Post-1.0 연기 (Postponed)** | 서버 로케일 설정에 따라 TDS `LOGINACK`/`ENVCHANGE` 5바이트 바이너리 토큰(한국어 `0x12, 0x04...` 등) 동적 생성 및 매핑 (`pkg/config`, `pkg/wire/tds`) |
| **LOC-3** | 함수/시스템 | T-SQL 내장 시스템 함수 `COLLATIONPROPERTY` 신설 | **Post-1.0 연기 (Postponed)** | `COLLATIONPROPERTY(name, 'CodePage'\|'LCID'\|'ComparisonStyle')` 빌트인 구현 (SSMS, DBeaver, EF Core 스키마 리버스 엔지니어링 호환) (`pkg/proc/builtins_sys.go`) |
| **LOC-4** | 카탈로그 | 가상 테이블 함수 `::fn_helpcollations()` 구현 | **Post-1.0 연기 (Postponed)** | 주요 40대 국제/SQL Collation 목록(`name`, `description`)을 반환하는 가상 카탈로그 시스템 함수 연동 (`pkg/catalog/virtual/`) |

---

### 3.10. 세션별 Prepared Statement 메모리 보호 및 Bounded LRU 트랙 (`PREP-1`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **PREP-1** | 와이어/안정성 | 세션별 Prepared Statement Bounded LRU 캐시 | **완료 (`[x]`)** | PGWire(`PreparedStatementCache`) 및 TDS(`TdsPreparedStatementCache`)에 세션당 최대 256개 상한 Bounded LRU 캐시 메커니즘을 적용하여, DBeaver/GORM/HikariCP 등 장기 유지 커넥션에서의 메모리 누수 및 OOM 원천 차단 (Golden Rule 23 준수, 25개 패키지 레이스 검증 100% 통과) (`pkg/wire/server.go`, `pkg/wire/tds/server.go`) |

---

### 3.11. HTTP API 하드닝 및 Web Studio 고도화 트랙 (`HTTP-1` ~ `HTTP-4`, `UI-1` ~ `UI-11`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **HTTP-1** | 와이어/보안 | 글로벌 CORS 미들웨어 & OPTIONS Preflight 지원 | **완료 (`[x]`)** | 웹 프론트엔드 및 서드파티 통합용 글로벌 CORS 미들웨어 완비 (`Access-Control-Allow-Origin`, `Methods`, `Headers`), OPTIONS 요청 시 204 No Content 즉시 응답 (`pkg/api/middleware.go`) |
| **HTTP-2** | 와이어/성능 | HTTP Gzip 응답 압축 핸들러 | **완료 (`[x]`)** | `Accept-Encoding: gzip` 지원으로 대용량 JSON 쿼리 결과 및 스키마 메타데이터 전송량 70~80% 절감 (`pkg/api/middleware.go`) |
| **HTTP-3** | 보안/리소스 | 요청 본문 크기 상한 가드 (Max Body Limit 32MB) | **완료 (`[x]`)** | 악의적인 초대용량 페이로드 전송으로 인한 OOM 사살 방지를 위해 `http.MaxBytesReader` 32MB 상한 가드 적용 (Golden Rule 23 준수, `pkg/api/middleware.go`) |
| **HTTP-4** | 와이어/보안 | 보안 강화 HTTP 응답 헤더 일괄 탑재 | **완료 (`[x]`)** | `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN` 표준 보안 헤더 전역 주입 (`pkg/api/middleware.go`) |
| **UI-1** | UI/UX | 네이티브 `alert()` ➔ 모던 Toast 알림 전면 전환 | **완료 (`[x]`)** | 동기식 브라우저 UI 블로킹을 유발하는 구형 `alert()` 13개를 세련된 비동기 플로팅 Toast 알림(`showToast`)으로 전면 전환 (`pkg/ui/dashboard.html`) |
| **UI-2** | UI/UX | 모바일/태블릿 반응형 레이아웃 및 햄버거 메뉴 | **완료 (`[x]`)** | 모바일/태블릿(900px 이하) 뷰포트에서 사이드바 자동 축소 및 햄버거 토글 메뉴 지원 (`pkg/ui/dashboard.html`) |
| **UI-3** | UI/브랜드 | Web Studio 브랜드 네이밍(`LionSQL`) 정돈 | **완료 (`[x]`)** | 웹 콘솔 내 잔재 브랜드 표기를 `LionSQL`로 정돈 및 기본 관리자 계정 `admin@lionsql.local` 일원화 (`pkg/ui/dashboard.html`) |
| **UI-4** | UI/생산성 | 사이드바 실시간 테이블/프로시저 빠른 검색 필터 | **완료 (`[x]`)** | 수십~수백 개의 테이블 및 저장 프로시저를 즉시 타이핑하여 인라인 필터링하는 빠른 검색창(`onSidebarQuickSearch`) 완비 (`pkg/ui/dashboard.html`) |
| **UI-5** | UI/UX | 패스워드 표시/숨기기(Eye Toggle) 버튼 지원 | **완료 (`[x]`)** | 로그인 모달 및 SMTP 설정의 패스워드 입력창에 마이크로 인터랙션 Eye 토글 버튼을 추가하여 입력 편의성 및 오타 방지 지원 (`pkg/ui/dashboard.html`) |
| **UI-6** | UI/안전성 | Drop Table 실수 방지용 테이블명 일치 확인 모달 가드레일 | **완료 (`[x]`)** | 단순 브라우저 confirm 대신 대상 테이블명을 직접 타이핑해야만 삭제 버튼이 활성화되는 안전 가드레일 모달(`#drop-table-modal`) 구현 (GitHub/Supabase 스타일 오타/실수 원천 방지) (`pkg/ui/dashboard.html`) |
| **UI-7** | UI/데이터 | Sample Data Seed 빈 DB 사전 검증 가드 및 원자적 피드백 | **완료 (`[x]`)** | 기존 테이블 보유 시 409 Conflict 차단 및 명확한 에러 안내, 빈 DB 시 원자적 트랜잭션 씨딩 및 첫 테이블(`accounts`) 자동 선택 로드 완비 (`pkg/api/handlers_records.go`, `pkg/ui/dashboard.html`) |
| **UI-8** | UI/DX | API Preview 모달 테이블 스키마 기반 동적 더미 페이로드 생성 | **완료 (`[x]`)** | 테이블 컬럼 타입(INT, VARCHAR, FLOAT, BOOL, DATETIME)별 최적화된 동적 샘플 페이로드 생성 및 단건/배치 탭 지원 (`pkg/ui/dashboard.html`) |
| **UI-9** | UI/DX | API Preview 모달 LionSQL 공식 SDK (`lionsql.js`) 전용 코드 탭 신설 | **완료 (`[x]`)** | curl, Fetch, Python에 이어 공식 TypeScript/JavaScript SDK 탭(`db.collection(...)`) 추가 완비 (`pkg/ui/dashboard.html`) |
| **UI-10** | UI/테마 | Web Studio Light / Dark 모드 테마 전환 및 Cache-Control 캐시 방지 | **완료 (`[x]`)** | CSS 변수 기반 클린 라이트 모드 지원, 사이드바/모바일 테마 토글 버튼, 로컬 스토리지 테마 영속화 및 `Cache-Control: no-cache` 헤더 완비 (`pkg/ui`, `pkg/api/server.go`) |
| **UI-11** | UI/가시성 | Web Studio Light 모드 고대비 시인성 개편 (경고창/배지/모달/ID 컬럼 WCAG AAA 정합) | **완료 (`[x]`)** | Light 모드 전반의 파스텔톤 저시인성 색상 전면 퇴출. Drop Table Confirmation 모달의 연분홍 텍스트를 딥 버건디(`--alert-danger-text: #991b1b`) 및 볼드 레드(`--alert-danger-strong: #b91c1c`)로 전환(7:1 대비비 확보), ID 컬럼 노란색 텍스트를 선명한 딥 블루(`--pk-val-color: #1d4ed8`)로 개편, Developer API Preview 모달 미선택 탭 버튼의 검은색 텍스트/어두운 배경 겹침 현상을 클린 화이트 배경(`--tab-inactive-bg: #ffffff`) 및 고대비 텍스트(`--tab-inactive-color: #334155`)로 개선, 모든 에러 모달(`create-table-error`, `create-cron-error`, `create-webhook-error` 등)을 `.alert-danger-box`와 `.btn-danger-subtle`로 단일화, 동적 JS 테이블 렌더러의 인라인 하드코딩 스타일을 시맨틱 배지 클래스로 완전 교체 (`pkg/ui/dashboard.html`, `pkg/ui/embed_test.go`) |
| **UI-DDL-1** | UI/DDL | 스키마 뷰 비주얼 컬럼 추가 GUI (`+ Add Column` 모달) 및 ALTER TABLE 연동 | **완료 (`[x]`)** | `Schema & DDL` 서브뷰에 `+ Add Column` 모달 연동. 스토리지 엔진 인터페이스(`storage.Engine`)에 `AddColumn` 정의 및 In-Memory OCC / WiredTiger B+Tree 튜플 리라이트 구현, `ALTER TABLE <table> ADD [COLUMN]` SQL 파서/실행기 완비 (`pkg/storage`, `pkg/engine`, `pkg/ui`) |
| **UI-REALTIME-1** | UI/CDC | 실시간 CDC / SSE 스트림 인스펙터 뷰어 및 라이브 피드 | **완료 (`[x]`)** | 사이드바 `Realtime Live Stream` 전용 뷰 신설. `/api/realtime` SSE와 직결하여 200건 Bounded 링버퍼 기반 실시간 DML 변경 이벤트 카드 스트림, 액션 배지(CREATE, UPDATE, DELETE, DDL), 테이블/액션 필터, 실시간 검색, 스트림 일시정지/재개 및 JSON 복사 지원 (`pkg/ui/dashboard.html`) |
| **UI-DATA-1** | UI/데이터 | 레코드 테이블 다중 행 선택 대량 작업 (일괄 삭제, CSV/JSON 내보내기) 및 외래키(FK) 릴레이션 팝업 점프 | **완료 (`[x]`)** | 레코드 체크박스 다중 선택 기반 일괄 삭제(`DELETE`) 및 선택 레코드 CSV/JSON 내보내기 완비. 스키마 외래키 메타데이터(`TableSchemaDTO.ForeignKeys`) 및 네이밍 휴리스틱 연동으로 FK 컬럼 링크(`↗`) 클릭 시 부모 레코드 정보 팝업 모달(`#fk-relation-modal`) 표시 및 원클릭 부모 테이블 이동 지원 (`pkg/api`, `pkg/ui`) |
| **UI-LOGS-1** | UI/관측성 | 실시간 HTTP API 요청 활동 로그 뷰어 (Activity Logs) | **완료 (`[x]`)** | 인입되는 HTTP API 요청(Method, Path, Status, Latency, IP)을 200건 Bounded 링버퍼(Golden Rule 23)에 무할당 추적하는 `RequestActivityLogger` 및 미들웨어 신설. `GET /api/logs/requests` 엔드포인트와 사이드바 `Request Logs` 화면, 자동 갱신(3s)/일시정지, 메서드/상태코드/검색 필터 및 원클릭 로그 초기화 지원 (`pkg/api`, `pkg/ui`) |

---

### 3.12. Graceful Shutdown 원자적 Teardown 및 CGO SIGSEGV 방지 트랙 (`SHUTDOWN-1`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **SHUTDOWN-1** | 종료/안정성 | Graceful Shutdown 시 WiredTiger CGO SIGSEGV 방지 및 PGWire 드레인 동기화 | **완료 (`[x]`)** | PGWire 클라이언트 핸들러 고루틴 종료 시점과 스토리지 엔진 커넥션 해제(`C.go_wt_close`) 간 Use-After-Free 경쟁 상태(SIGSEGV addr=0xad0) 완전 해소. `pkg/wire/server.go`에 `sync.WaitGroup` 수명주기 추적 및 1초 상한 Bounded Drain 동기화 도입, `pkg/storage/wiredtiger/wt_engine.go`의 `Engine.Close()`에서 활성 세션 사전 일괄 소멸(Drain) 및 `WTSession.Close()`에 `connClosed` atomic 가드레일을 적용하여 셧다운 시 100% 무결점 종료 보장 (`pkg/wire/server.go`, `pkg/storage/wiredtiger/wt_engine.go`) |

---

## 4. 공식 보류 및 스펙-아웃 사유 명세 (Deferred & Spec-Out Rationale)

1.0 릴리스 안정성 확보 및 극단적 KISS/YAGNI 원칙(Golden Rule 14, 15)에 따라 보류 및 배제된 항목들의 기술적 결정 사유입니다:

### 4.1. 성능 및 실행 엔진 보류 과제
* **DML `storage.Record` 제거 ➔ `storage.Row` 직결 (`P-13`)**:
  `memengine` 및 WiredTiger 스토리지 계층의 복잡한 외래키 연쇄(CASCADE), 유니크 인덱스 충돌 검사, 복합 제약조건 검증 로직이 `storage.Record(map[string]Value)` 구조와 강하게 결합되어 있음. 이를 슬라이스 직결로 무리하게 전면 개편할 경우 엣지 케이스에서의 ACID 무결성 훼손 및 롤백 불안정 위험이 있어 보류.
* **WiredTiger 다중 행 배치 CGo 삽입 (`P-15`)**:
  CGo 프리앰블 수준의 일괄 삽입은 C 포인터 메모리 관리 오류, 세그폴트(SIGSEGV), 해저드 포인터 레이스 등 시스템 크래시 위험을 수반함. 이미 단일 노드 16,814 TPS의 높은 처리량을 안정적으로 달성 중이므로 크래시 방지(Rule 1, 36)를 위해 보류.
* **In-Memory OCC Row-Level Atomic CAS 커밋 검증 (`P-16`)**:
  롤백 시 버전 복원 경쟁, 툼스톤 동시성 처리 등 동시성 엣지 케이스와 데이터 사일런트 손상(Silent Corruption) 위험이 큼. 이미 TPC-C 20,087 TPS 환경에서 직렬화 어보트율이 0.10%로 극히 안정적이므로 시스템 무결성을 최우선으로 보류.
* **VDBE 컬럼 산술 융합 슈퍼 명령어 (`P-17`)**:
  바이트코드 점프 타깃(PC) 오프셋 불일치, 타입 불일치 등 가상 머신 실행 무결성 위협 위험이 큼. `sync.Pool` 레지스터 재사용과 서브마이크로초 디스패치로 20k TPS를 안정적으로 달성 중이므로 KISS 원칙에 따라 보류.
* **Volcano ➔ VDBE 결과 수집 시 Compact RowSet 구조 전환 (Post-1.0 검토)**:
  현재 `ExecutionResult.Rows`가 `[]map[string]any`로 정의되어 있어 대량 조회(수만~수십만 건) 시 매 행마다 새로운 해시맵을 힙에 할당함(`tuple.ToMap()`). 1.0 프로덕션에서는 안정성을 위해 현행 인터페이스를 유지하고, 1.0 이후 컬럼 메타데이터 분리 및 `[]storage.Value` 1차원 슬라이스 기반 Compact RowSet으로 전환하여 GC 압박을 70% 이상 절감하는 과제로 검토.
* **Hash Join 대규모 데이터 디스크 스필 (Grace Hash Join) (Post-1.0 검토)**:
  현재 `HashJoinIterator`는 `work_mem` 초과 시 에러를 반환하여 프로세스 OOM 크래시를 안전하게 차단하고 있음(Golden Rule 23 안전 가드 완비). 1.0 프로덕션에서는 고성능 인메모리 OLTP 핫패스 안정성에 집중하고, `SortIterator`의 외장 머지 소트처럼 `work_mem` 초과 시 임시 디렉토리 파일로 파티셔닝하는 하이브리드 Grace Hash Join 구현은 1.0 이후 대규모 배치/분석 고도화 과제로 검토.

### 4.2. 구조 리팩토링 및 플랫폼 보류 과제
* **In-Memory OCC 트랜잭션 라이프사이클 모듈 분해 (`R-4`) & WiredTiger CGo 분해 (`R-5`)**:
  현재 25개 전 패키지 레이스 검사 100% PASS(0 Data Race)로 동시성 안정성이 완벽히 입증됨. 1.0 출시 전 단순 파일 분리는 런타임 성능 이득이 없고 오히려 회귀 위험을 유발하므로 v1.1+로 공식 이관.
* **투명 데이터 암호화 (In-Kernel TDE)**:
  인-커널 소프트웨어 암호화는 클라우드 볼륨(AWS EBS, GCP PD) 및 OS 블록 디바이스(LUKS2)와 중복 암호화되어 NVMe 처리량을 최대 35% 저하시킴. 하드웨어 가속(AES-NI) 기반 스토리지 파일시스템 암호화 표준 가이드([`doc/STORAGE_ENCRYPTION_GUIDE.md`](doc/STORAGE_ENCRYPTION_GUIDE.md)) 수립으로 대체 충족.
* **Pure-Go 영속 스토리지 엔진 (Pebble / bbolt)**:
  1.0 프로덕션에서는 검증된 WiredTiger B+Tree 엔진과 순수 Go 초고속 In-Memory OCC 엔진 2대 백엔드 체제에 집중(v1.1+ 이관).

### 4.3. 아키텍처 다이어트 보류 과제 (Post-1.0 연기)
> **핵심 아키텍처 불변식**: `KISS-3`과 `KISS-5`는 **"문제가 있어서 못 만든 것"이 아니라, "현재 시스템의 초고속 성능과 무결성을 지키기 위해 의도적으로 남겨둔 최선의 실용적 설계"**입니다. 이론적 코드 순수성을 위해 검증된 런타임 안정성과 서브마이크로초 OLTP 처리량을 희생하지 않습니다.

* **`sqlexec` 하위 정규식 파싱 계층 제거 및 AST 파서 일원화 (`KISS-3`)**:
  - 이미 패키지 싱글톤 정규식 및 1-Pass 인덱스 슬라이스 스캐너(`PERF-1`)로 **16ns/op**의 제로-오버헤드가 달성됨.
  - 25개 전 패키지와 120개 이상의 E2E 드라이버 통합 테스트가 100% 통과 중인 상태에서, 단지 정규식을 없애겠다는 미학적 이유로 수십 개 DML/DDL 경로의 파싱 로직을 대규모로 개편할 경우 엣지케이스 문법 파싱 리그레션 위험만 큼. 런타임 무결성 보호를 위해 영구 보류(Indefinitely Deferred).
* **3갈래 쿼리 실행 경로 단일화 (`sqlexec_select` ➔ `execution` Volcano Iterator) (`KISS-5`)**:
  - `sqlexec_select.go`의 단일 행/기본키 및 기본 필터 패스트패스는 Volcano 반복자 트리를 생성하고 가상 함수(`Next()`)를 호출하는 비용보다 **2~3배 빠름**.
  - 이를 무리하게 단일 트리로 강제 통합하면 TPC-C 20,000+ TPS급의 서브마이크로초 OLTP 처리량을 희생하게 됨. 고성능 RDBMS의 표준인 **"고빈도 단건 패스트패스 + 복합 쿼리 Volcano CBO" 이원화 구조**를 유지하는 것이 최선의 실용적 설계임. Plan Cache JIT 컴파일 등으로 단건 직결보다 빨라지는 혁신이 전제되지 않는 한 영구 보류.
* **다중 결과셋 (Multiple Result Sets) 스트리밍 지원 (`TDS-COMPAT-6`)**:
  현대 웹/OLTP 백엔드(Spring, Prisma, Gorm 등)의 95% 이상은 단일 요청당 1개의 쿼리 결과셋을 소비하며, C# Dapper `QueryMultiple` 등 제한된 영역에서만 사용됨. 엔진 코어 인터페이스(`ExecuteSessionSQL`)를 전면 슬라이스로 개편하고 TDS 토큰 상태 머신을 건드리는 큰 변경을 1.0 직전에 넣으면 전체 패키지 회귀 위험이 큼. 클라이언트에서 쿼리를 2회 분리 호출하는 명확한 대안이 존재하므로 1.0 이후(Post-1.0)로 연기.
* **TDS 8.0 / Pre-Login SSL/TLS 암호화 핸드셰이크 보강 (`SEC-1`)**:
  현재 사설망 및 로컬/컨테이너 환경에서 TLS 비활성화(`encrypt=disable`) 모드로 100% 정상 통신 중임. 마이크로소프트 TDS 8.0의 복잡한 Pre-Login TLS 래핑 및 ALPN 핸드셰이크 협상은 공용 인터넷 노출 시 필요한 고급 보안 기능이며, 네트워크 암호화는 mTLS 리버스 프록시(Envoy, Traefik, stunnel) 또는 WireGuard/VPN으로 충분히 대체 가능함. 1.0 코어 안정성 확보를 위해 Post-1.0으로 공식 연기.
* **다국어 Collation 및 로케일 전면 지원 트랙 (`LOC-1` ~ `LOC-4`)**:
  LionSQL은 내부 스토리지를 완전한 표준 유니코드 UTF-8로 단일화하고 대소문자 무관(`CI_AS`) 비교를 기본 제공하여, 한글/영문/다국어 처리 및 드라이버 연동에 실무적 장애가 전혀 없음. 레거시 CodePage/Collation 에뮬레이션(`COLLATE` 관용 구문, 동적 TDS 바이너리 토큰 매핑, `COLLATIONPROPERTY`, `fn_helpcollations`)은 1.0 코어 엔진의 극단적 단순성(KISS)과 안정성 확보를 위해 전 항목을 Post-1.0으로 일괄 연기 (상세 설계: [`notes/COLLATION_AND_LOCALE_PLAN.md`](notes/COLLATION_AND_LOCALE_PLAN.md)).
* **불변 엔터프라이즈 감사 로그 트랙 (`AUDIT-1` ~ `AUDIT-4`)**:
  1.0의 핵심 타겟 유저(독립 경량 OLTP, 사설망 컨테이너, T-SQL 마이그레이션) 환경에서는 외부 침입 감사나 금융권 규제 감사보다 엔진 안정성과 서브마이크로초 처리량(TPS)이 최우선임. 디스크/네트워크 레벨 암호화와 이미 완비된 CDC 스트리밍/Prometheus 관제/기본 인증(SCRAM-SHA-256, JWT)으로 기초 보안과 모니터링이 충분히 충족되므로, 규제 컴플라이언스 대응 전용 불변 로그 체계는 1.0 안정 릴리스 이후 엔터프라이즈 확장 단계(Post-1.0)로 연기 (상세 설계: [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md)).
* **클라우드 S3 Pre-signed URL 및 SigV4 서명기 트랙 (`S3-1` ~ `S3-4`)**:
  대용량 미디어/Blob 파일 처리는 DB 커널의 핵심 책임(ACID 트랜잭션)이 아니며, 이미 인-커널 바이너리 저장은 영구 스펙-아웃(`GEMINI.md` 8.7절)되어 있음. 애플리케이션 레벨(AWS SDK)에서 S3 직접 업로드를 처리하는 표준 패턴이 널리 쓰이므로, DB 커널 내장 서명기 및 UI 위젯은 1.0 릴리스 이후로 연기 (상세 설계: [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md)).

### 4.4. 영구 스펙-아웃 (Spec-Out Invariants)
* **내장형 OAuth2 웹 플로우 배제 & 클라이언트 사이드 토큰 교환 (Client-Side Token Exchange) 표준화**:
  데이터베이스 커널 내부에서 구글/깃허브 웹 브라우저 리다이렉트, 콜백 엔드포인트 수신, Client Secret 보관 및 아웃바운드 인터넷 토큰 교환을 처리하는 것은 단일 책임 원칙 및 KISS(Rule 15)를 위반하므로 명시적으로 배제함. 소셜 로그인은 Supabase/Firebase/Cognito 표준인 **Client-Side OIDC Token Exchange (RFC 8693)** 패턴(`AUTH-1`)으로 표준화: 클라이언트가 IdP SDK로 직접 로그인 후 획득한 서명된 ID Token(JWT)을 `POST /api/auth/token-exchange`로 전달하고, LionSQL 커널(`pkg/auth`)은 IdP의 공개키(JWKS)로 서명 무결성만 검증하여 내부 세션 토큰을 발급함 (`GEMINI.md` 8.6절).
* **내장형 S3 Blob 파일 스토리지 및 썸네일 리사이징 배제**:
  대용량 바이너리 스트리밍 및 이미지 리사이징으로 인한 OLTP CPU/메모리 기아를 방지하고, S3/MinIO 직접 업로드(Pre-signed URL) 위임으로 영구 표준화 (`GEMINI.md` 8.7절, `notes/S3_PRESIGNED_STORAGE_PLAN.md`).
* **미성숙 Pure-Go 영속 스토리지 엔진 (Pebble/bbolt) 단기 도입 배제 (Spec-Out & 지속적 대안 탐색)**:
  현재 Pure-Go 임베디드 스토리지 생태계(Pebble, bbolt, Badger 등)는 WiredTiger 수준의 고성능 멀티코어 MVCC B+Tree, Hazard Pointer 동시성, 세밀한 트랜잭션 타임스탬프와 ACID 격리를 온전히 대체하기에 아직 성숙도와 트랜잭션 기능 측면에서 프로덕션 OLTP 대안으로 쓸만한 상태가 아님. 따라서 1.0 프로덕션 체제에서는 공식 **스펙-아웃(Spec-Out)**으로 확정하며, WiredTiger MVCC(디스크)와 In-Memory OCC(메모리) 2대 백엔드 체제에 집중함. 단, 향후 CGo 완전 배제(Pure-Go 정적 단일 바이너리)를 위한 잠재적 스토리지 대안 후보 탐색은 지속적으로 모니터링함 (`GEMINI.md` 8.8절).
* **슬로우 쿼리 실시간 웹훅 직발송 커널 내장 배제 (`OBS-3`) (Spec-Out & Post-1.0 툴링 아이디어 검토)**:
  DB 커널 내부에서 Slack, Discord 등의 외부 HTTP 웹훅을 직접 호출하는 것은 단일 책임 원칙 및 KISS(Rule 15)를 위반함. 커널 내부의 직접적인 아웃바운드 HTTP 호출은 네트워크 지연(RTT), 타임아웃, 재시도 큐 관리, 고루틴 기아 등의 런타임 리스크를 초래함. 표준 Prometheus 메트릭(`OBS-1`), 런타임 프로파일러(`OBS-2`), 감사 로그를 통해 외부 관제 도구(Alertmanager, Grafana)로 위임하는 표준 아키텍처를 따르며, 공식 **스펙-아웃(Spec-Out)**으로 확정(향후 외부 사이드카/CLI 아이디어로만 검토, `GEMINI.md` 8.9절).
* **Wasm UDF 격리 샌드박스 (`wazero`) 커널 내장 배제 (Spec-Out & Post-1.0 아이디어 탐색)**:
  DB 커널 내부에 별도의 WebAssembly 바이트코드 가상머신(`wazero`)을 내장하는 것은 바이너리 비대화 및 런타임 메모리 관리 복잡도를 키우며 실무적 수요가 극히 희박함. 이미 Go Native UDF SPI(`proc.RegisterScalarFunction`, <5ns)와 T-SQL VDBE 바이트코드 가상머신으로 완벽히 통합되어 있으므로, 공식 **스펙-아웃(Spec-Out)**으로 확정(1.0 이후 장기적 아이디어 수준으로만 후보 탐색, `GEMINI.md` 8.10절).

### 4.5. Post-1.0 6대 과제 실질 구현 가치 평가 및 스펙-아웃/보류 분석 매트릭스

1.0 이후(Post-1.0 및 Horizons)로 분류되었던 6대 과제에 대해 실무적 효용성, 아키텍처 원칙(KISS/YAGNI), 런타임 리스크를 종합 평가한 결과, 대부분이 **이론적 순수성에 치중되었거나 DB 커널의 책임을 벗어난 과제**로 판정되어 사실상 미구현 또는 스펙-아웃(Spec-Out)될 가능성이 높습니다:

| 분류 | 과제명 (코드) | 기존 명분 | 실질 엔지니어링 리스크 및 스펙-아웃/미구현 사유 | 최종 판정 |
| :--- | :--- | :--- | :--- | :---: |
| **운영/경계 침범** | **슬로우 쿼리 실시간 웹훅 알림 (`OBS-3`)** | DB가 슬랙/디스코드 웹훅 직접 발송 | **KISS & 단일 책임 원칙 위배 (안티패턴)**: DB 커널이 외부 HTTP 웹훅을 직접 호출하면 네트워크 RTT 지연, 타임아웃, 재시도 큐 관리 등 커널답지 않은 부가 책임을 떠안게 됨. 이미 완비된 **Prometheus 메트릭(`OBS-1`), `EXPLAIN ANALYZE`(`OBS-2`), 감사 로그**를 통해 Alertmanager/Grafana 등 외부 관제 도구가 수집하는 것이 표준 아키텍처임. | **공식 스펙-아웃 확정 (Spec-Out, 향후 아이디어 검토)** |
| **이론적 순수성 vs 회귀 위험** | **`sqlexec` 하위 정규식 제거 & AST 일원화 (`KISS-3`)** | 정규식 없는 순수 AST 단일 파싱 파이프라인 | **"문제가 있어서 못 만든 것이 아니라, 초고속 성능과 무결성을 지키기 위해 의도적으로 남겨둔 최선의 실용적 설계"**: 이미 패키지 싱글톤 정규식과 1-Pass 인덱스 슬라이스 스캐너(`PERF-1`)로 **16ns/op**의 서브마이크로초 핫패스를 달성함. 수천 개 회귀 테스트가 검증된 상태에서 단지 정규식을 없애겠다는 미학적 이유로 멀쩡한 코드를 뜯어고치는 것은 버그 유발 위험만 큼. | **영구 보류 (Indefinitely Deferred)** |
| **단일화 vs OLTP 지연시간** | **3갈래 실행 경로 단일화 (`KISS-5`)** | 모든 쿼리를 Volcano Iterator로 통합 | **"문제가 있어서 못 만든 것이 아니라, 초고속 성능과 무결성을 지키기 위해 의도적으로 남겨둔 최선의 실용적 설계"**: 단순 `SELECT 1`, `SELECT * FROM tbl WHERE id = 1` 같은 고빈도 단건 쿼리는 Volcano 반복자 트리의 가상 함수 디스패치보다 직결 `sqlexec_select`가 2~3배 빠름. 굳이 단일 트리로 강제 통합하면 초당 2만 TPS급 OLTP 성능을 희생하게 됨. | **영구 보류 (Indefinitely Deferred)** |
| **스토리지 중복/YAGNI** | **Pure-Go Pebble/bbolt 스토리지 엔진** | CGo 없는 100% 순수 Go 영속 스토리지 | **YAGNI & 스토리지 삼중화 오버엔지니어링**: 이미 **초고속 In-Memory OCC(`memengine`)**와 프로덕션 검증된 **WiredTiger MVCC**로 완벽한 이원화 체계가 확립됨. 3번째 스토리지 엔진을 추가하는 것은 유지보수 및 회귀 테스트 비용만 2배로 폭증시킴. | **공식 스펙-아웃 확정 (Spec-Out, 지속 대안 탐색)** |
| **런타임 비대화/복잡도** | **Wasm UDF 샌드박스 (`wazero`)** | 다국어 UDF 격리 실행 가상머신 | **과도한 런타임 복잡도 & 불필요성**: 이미 Go Native SPI(`proc.RegisterScalarFunction`, <5ns)로 UDF가 완벽히 동작함. DB 커널 내부에 Wasm 인터프리터를 내장하는 것은 바이너리 크기 및 메모리 관리 복잡도만 키우며 실무적 수요가 극히 희박함. | **공식 스펙-아웃 확정 (Spec-Out, 향후 아이디어 검토)** |
| **아키텍처 불변식 충돌** | **멀티코어 병렬 OLAP 스캔 (`P-10`)** | 대규모 분석 쿼리 멀티코어 풀스캔 | **HTAP 아키텍처 불변식 충돌**: LionSQL은 분석 쿼리를 OLTP 커널에서 무겁게 돌리는 대신, **트랜잭션 커밋 CDC 스트리밍으로 임베디드 DuckDB에 마이크로 배치 적재**하는 아키텍처(`GEMINI.md` 8.4절)를 확립함. OLTP 엔진에서 고루틴 대량 생성 병렬 스캔을 돌리는 것은 코어 엔진 격리 원칙에 위배됨. | **스펙-아웃 권고 (Spec-Out)** |


### 4.6. Web Studio (Port 8090) Post-1.0 고도화 백로그 (Supabase & PocketBase 벤치마킹)
웹 관리자 경험(DX)을 글로벌 BaaS 수준으로 극대화하기 위한 백로그 중 4대 과제(`UI-DDL-1`, `UI-REALTIME-1`, `UI-DATA-1`, `UI-LOGS-1`)는 1.0 프로덕션 버전에 완결(`[x]`) 반영되었으며, Monaco/CodeMirror 에디터 고도화 과제(`UI-SQL-1`)는 1.0 이후 단계로 연기되었습니다:

* [x] **실시간 CDC / SSE 스트림 인스펙터 뷰어 (`UI-REALTIME-1`)**: 완료 (`[x]`). 사이드바 `Realtime Live Stream` 전용 화면, 200건 링버퍼 카드 스트림, 실시간 테이블/액션 필터 및 JSON 페이로드 복사 지원.
* [x] **스키마 뷰 내 비주얼 컬럼 추가 GUI (`UI-DDL-1`)**: 완료 (`[x]`). `+ Add Column` 모달 연동, In-Memory OCC 및 WiredTiger B+Tree 튜플 리라이트, `ALTER TABLE ... ADD ...` DDL 엔진 연동.
* [x] **레코드 테이블 다중 행 선택 대량 작업 및 외래키 릴레이션 팝업 점프 (`UI-DATA-1`)**: 완료 (`[x]`). 일괄 삭제, 선택 레코드 CSV/JSON 내보내기, FK 릴레이션 팝업 모달(`#fk-relation-modal`) 및 부모 테이블 원클릭 이동.
* [x] **실시간 HTTP API 요청 로그 뷰어 (`UI-LOGS-1`)**: 완료 (`[x]`). 200건 Bounded 링버퍼 기반 `RequestActivityLogger`, `GET /api/logs/requests`, 사이드바 `Request Logs` 화면, 자동 갱신(3s), 메서드/상태/경로 필터 및 초기화.
* [ ] **SQL Query Studio 에디터 강화 (`UI-SQL-1`) (Post-1.0 연기)**:
  - CodeMirror/Monaco 결합 구문 컬러링, T-SQL 키워드 자동완성, 실행 쿼리 히스토리(History) 패널 및 Volcano 실행 계획 계층형 트리 뷰 시각화.

### 4.7. 수직 핫패스 고위험 구조 개편 과제 Post-1.0 (`v1.1.0`) 이관 사유 (`VPERF-5`, `VPERF-6`, `VPERF-10` ~ `VPERF-12`)
v0.90.0 Feature Freeze(기능 동결) 원칙에 따라, 25개 패키지 및 120개 이상의 E2E 드라이버 통합 테스트 안정성을 지키고 회귀 버그(Regression)를 방지하기 위해 고위험 구조 개편 5개 항목을 `v1.1.0`으로 공식 이관합니다:

1. **`VPERF-10` (엔드투엔드 슬라이스 파이프라인 전환)**:
   - `ExecutionResult.Rows`(`[]map[string]any`) 구조체를 슬라이스 직결로 변경하는 작업은 TDS, PGWire, HTTP REST API, VDBE VM 등 프로젝트 전반의 공용 인터페이스에 영향을 미치므로 1.0 프로덕션에서는 현행 안정 상태를 유지하고 v1.1.0에서 점진 전환.
2. **`VPERF-11` (In-Memory OCC `Scan()` 샤드 스트리밍 커서)**:
   - TPC-C 20k TPS를 공식 검증한 인메모리 스토리지 커서의 정렬/순회 알고리즘 변경 시 동시성 트랜잭션 충돌이나 툼스톤 스캔 누락 회귀 위험이 있으므로 v1.1.0으로 이관.
3. **`VPERF-12` (WiredTiger `Update` 단일 커서 및 Zero-Copy `unsafe.Slice` 디코딩)**:
   - CGo 포인터 생명주기와 GC 수명 불일치로 인한 세그폴트(SIGSEGV Crash) 위험을 원천 배제(Golden Rule 1, 36)하기 위해 v1.1.0으로 이관.
4. **`VPERF-6` (`sp_executesql` VDBE 플랜캐시 직결)**:
   - 복합 SQL 배치, 세션 변수, 제어문이 혼합 유입되는 RPC 특성상 C# Dapper / Java JDBC 드라이버와의 호환성 회귀를 방지하기 위해 v1.1.0으로 이관.
5. **`VPERF-5` (TDS `runQueryWithAttention` 취소 동기화)**:
   - 클라이언트 쿼리 취소(0x06 Attention) 패킷 레이스 컨디션 및 네트워크 계층 동시성 상태머신 안정을 위해 v1.1.0으로 이관.

---

## 5. 최근 완료 마일스톤 상세 이력 (Recent Completed History)

### 5.1. 최근 고성능 최적화 및 안정화 과제
- [x] **통합 SQL 추상화 레이어 기반 2번 결함군 14건 일괄 해결 (`SQL-ABSTRACT-1`) (`pkg/storage`, `pkg/engine`, `pkg/proc`, `pkg/execution`, `pkg/planner`, `pkg/wire`)**:
  - `storage.Record` 1급 객체화 및 `Record.Get` 대소문자 무관 탐색을 바탕으로 전 계층 경계 정합성 전면 보강 (세부 14건 결함 조치 내역은 [`notes/BUG_ARCHIVE.ko.md`](notes/BUG_ARCHIVE.ko.md) 참조).
  - 전용 리그레션 테스트 6개 패키지 신설 및 23개 전 패키지 100% PASS, 0 데이터 레이스 검증 완료.
- [x] **Web Studio 4대 생산성/관측성 기능 완결 (`UI-DDL-1`, `UI-REALTIME-1`, `UI-DATA-1`, `UI-LOGS-1`) (`pkg/storage`, `pkg/engine`, `pkg/api`, `pkg/ui`)**:
  - `UI-DDL-1`: 스키마 서브뷰에 `+ Add Column` 비주얼 모달 연동. `storage.Engine` 인터페이스에 `AddColumn` 정의, In-Memory OCC의 테이블 컬럼 추가 및 WAL(`OpAlterTable`) 로깅/재생, WiredTiger의 커서 스캔 및 튜플 리라이트(`C.go_wt_cursor_update_raw`), DDL 파서(`ALTER TABLE <table> ADD [COLUMN] ...`) 완비.
  - `UI-REALTIME-1`: 사이드바 `Realtime Live Stream` 전용 화면 신설. `/api/realtime` SSE와 직결하여 200건 Bounded 링버퍼 기반 실시간 DML 변경 이벤트 카드 스트림, 액션 배지(CREATE, UPDATE, DELETE, DDL), 테이블/액션 필터, 실시간 검색, 스트림 일시정지/재개 및 JSON 복사 지원.
  - `UI-DATA-1`: 레코드 그리드 다중 행 선택 기반 일괄 삭제(`deleteSelectedRecords`) 및 선택 레코드 CSV/JSON 다운로드(`exportSelectedRecords`). 스키마 외래키 메타데이터(`TableSchemaDTO.ForeignKeys`) 및 네이밍 휴리스틱 연동으로 FK 컬럼 링크(`↗`) 클릭 시 부모 레코드 정보 팝업 모달(`#fk-relation-modal`) 표시 및 부모 테이블 점프 지원.
  - `UI-LOGS-1`: 인입되는 HTTP API 요청(Method, Path, Status, Latency, IP)을 200건 Bounded 링버퍼(Golden Rule 23)에 추적하는 `RequestActivityLogger` 및 미들웨어 신설. `GET /api/logs/requests` 엔드포인트와 사이드바 `Request Logs` 화면, 3초 주기 자동 갱신 및 일시정지, 메서드/상태코드/검색 필터 및 로그 초기화 완비.
- [x] **Web Studio Developer API Preview 모달 고도화 (동적 스키마 페이로드 & LionSQL SDK 탭) (`UI-8`, `UI-9`) (`pkg/ui`)**:
  - 테이블별 스키마 컬럼 메타데이터(`tablesList`)를 분석하여 자동 증분 PK는 제외하고, 데이터 타입(INT, VARCHAR, DECIMAL, DATETIME, JSON, BOOL)과 컬럼 의미에 맞춘 현실적인 더미 JSON 페이로드(`getDummyRecordForTable`) 및 PK/필터 샘플(`getPrimaryKeySample`, `getFilterSample`)을 동적으로 생성.
  - cURL, Fetch, Python, Go 탭과 함께 LionSQL 공식 SDK (`lionsql.js`) 전용 `import LionSQL` 및 `db.collection('table').getList/getOne/create/query` 템플릿 탭(`api-lang-sdk`) 신설.
  - 단위 테스트 `TestAPIPreviewEnhancements` 추가 및 100% PASS 검증 완료.
- [x] **PGWire 미지정 타입 파라미터($1) 바이너리 길이 정합성 및 스키마 OID 캐싱 (`PG-COMPAT-1`) (`pkg/wire`)**:
  - `PreparedStatement` 구조체에 분석된 `ColumnNames []string`, `ColumnOIDs []int32` 필드를 신설하고 `writeSchemaDescription`에서 분석된 메타데이터를 캐싱하여 Describe와 Execute 단계의 OID 규약을 100% 동기화.
  - `buildPreviewSQL`에서 산술 연산자가 없는 단독 파라미터(`SELECT $1`, `$1, $2`)의 프리뷰 치환 기본값을 PostgreSQL 표준 규격에 따라 TEXT(`'sample'`)로 정의하여, 미지정 파라미터가 OID 23(4바이트 정수)으로 오인된 뒤 가변길이 문자열(3바이트 `"100"`)로 전송되어 `lib/pq` 클라이언트 패닉을 유발하던 결함 원천 해결.
  - `writeExecutionResult` 및 `executePortal`에서 캐시된 Statement Column OID를 우선 적용하여 재추론 왜곡 차단.
  - `pkg/wire/driver_integration_test.go`에 정수, 문자열, 다중 파라미터 동시 바인딩 E2E 통합 테스트(`TestDriverIntegration_LibPQ_UntypedParameter`) 100% PASS 검증 완료.
- [x] **기본키(PK) 중복 삽입 시 Unique Violation (SQLSTATE 23505) 에러 방출 (`PG-COMPAT-2`) (`pkg/storage/wiredtiger`, `pkg/engine`, `pkg/proc`, `pkg/wire`)**:
  - WiredTiger `insertLocked`에서 C 커서 삽입 전 `go_wt_cursor_get` 선행 검사를 추가하여 기존 기본키가 이미 존재할 경우 덮어쓰기 대신 `storage.ErrKeyExists` 즉시 반환.
  - DML 실행기(`pkg/engine/sqlexec_dml.go`) 및 VDBE `OpInsert`(`pkg/proc/runtime/vm_dispatcher.go`)에서 `storage.ErrKeyExists`/`storage.ErrUniqueViolation` 발생 시 `duplicate key value violates unique constraint on ...`으로 표준 에러 포맷팅.
  - PGWire `WriteError`에서 `DUPLICATE`, `UNIQUE`, `ALREADY EXISTS` 문자열에 대해 PostgreSQL 표준 에러 코드 `23505 (unique_violation)` 매핑.
  - `pkg/storage/wiredtiger/wt_engine_test.go` 단위 테스트 및 `pkg/wire/driver_integration_test.go` 공식 lib/pq 드라이버 E2E 통합 테스트 통과.
- [x] **세션별 Prepared Statement Bounded LRU 캐시 적용 (`PREP-1`) (`pkg/wire`, `pkg/wire/tds`)**:
  - TDS(`TdsPreparedStatementCache`) 및 PGWire(`PreparedStatementCache`)에 스레드 세이프 Bounded LRU 캐시(`container/list` + `map[*list.Element]`) 구현.
  - 세션당 기본 최대 256개(`DefaultMaxPreparedPerSession`) 상한 설정 및 `SetMaxPreparedPerSession` 동적 제어 지원.
  - 용량 초과 시 가장 오래 참조되지 않은(LRU) 준비된 문장을 자동 축출하고, 호출(`Get`/`Put`) 시 MRU로 승격하여 장기 커넥션 풀(DBeaver, GORM, HikariCP)에서의 메모리 누수 원천 차단 (Golden Rule 23 준수).
  - 단위 및 E2E 축출 검증(`lru_test.go`) 및 25개 전 패키지 무결성/데이터 레이스 0건 통과.
- [x] **크리티컬 패스 5대 무할당 최적화 (A~E) 완결 및 TDS 제로-할당 스트리밍 (`PERF-1`) (`pkg/engine`, `pkg/wire/tds`)**:
  - **[A] 정규식 패키지 싱글톤 승격**: DDL/DML 내부 15~30µs 정규식 컴파일 지연 및 15~25개 힙 객체 할당 100% 제거.
  - **[B] 문장 분할기 인덱스 기반 제로-카피 슬라이스 스캐너 재작성**: 바이트 단위 O(N^2) 루프 및 40KB 메모리 폭증 제거, 단일 쿼리 분할 **16.46 ns/op, 16 B/op, 1 allocs/op** 달성.
  - **[C] TDS 파라미터 바인딩 O(N) 단일 패스 치환기**: 바이트 스캐너 기반 1-Pass 치환으로 **923.1 ns/op (50배 가속, 서브마이크로초)** 달성.
  - **[D] TDS Row 스트리밍 직렬화 및 리플렉션 제거**: `WriteRowDirect` 도입으로 행 직렬화 시 **53.49 ns/op, 0 B/op, 0 allocs/op (힙 할당 0건)** 달성 및 `binary.Write` 리플렉션 100% 박멸.
  - **[E] UTF-16LE / UTF-8 제로-할당 변환**: ASCII Fast-Path 및 `strings.Builder` 스트리밍으로 중간 `[]rune`/`[]uint16` 제거 (**인코딩 121.9ns, 디코딩 266.3ns, 각각 1 allocs/op**).
  - **재발 방지 3대 불변 원칙 수립**: [`notes/PITFALLS_AND_TRICKS.ko.md`](notes/PITFALLS_AND_TRICKS.ko.md) 13절에 명문화.
- [x] **In-Memory SkipList 내부 뮤텍스 락 제거 및 Go 표준 PRNG 간소화 (`KISS-4`) (`pkg/storage/memengine`)**:
  - `SkipList` 구조체에서 `rnd *rand.Rand` 포인터 및 `rndMu sync.Mutex` 락을 완전히 제거하고 Go 1.22+ `math/rand/v2`로 전환.
  - `Insert()` 수행 중 발생하던 불필요한 이중 락킹(`sl.mu` + `sl.rndMu`) 및 시드 초기화 비용을 제거하여 락-프리/제로-할당 난수 레벨 결정 구현.
- [x] **가상 PG 카탈로그 및 information_schema 생성기 단일화 (`KISS-2`) (`pkg/catalog/virtual`, `pkg/engine`)**:
  - `pkg/engine/sqlexec_pgcatalog.go`(1,102줄) 및 `sqlexec_info_schema.go`(588줄)를 `pkg/engine`에서 완전 삭제하고 `pkg/catalog/virtual/` 하위(`pg.go`, `info_schema.go`)로 일원화.
  - `pg_catalog.*`(35개 뷰)와 `information_schema.*`(17개 뷰) 가상 뷰 생성을 `virtual.Source` 추상화 기반으로 통합하고, 엔진 쿼리 라우팅을 `virtual.IsPgCatalogTable` 및 `virtual.ExecPgCatalogSelect`로 슬림화 (25개 패키지 100% PASS, 데이터 레이스 0).
- [x] **미사용 구형 JS 트랜스파일러 및 `JSCode` 잔재 완전 제거 (`KISS-1`) (`pkg/proc`, `pkg/catalog`, `cmd/`)**:
  - VDBE VM 단일화에 따라 구형 JS 트랜스파일러(1,169줄) 완전 삭제, 잔재 필드를 `BytecodeDisasm` 메타데이터로 직관적 정리.
- [x] **In-Memory OCC 엔진 데이터 용량 한도 가드 (`MEM-3`) (`pkg/storage/memengine`, `cmd/server`)**:
  - `runtime/metrics` 기반 Zero-STW 힙 모니터링 및 `--max-memory` 연동, 용량 초과 시 `storage.ErrTableFull (SQLSTATE 53200)` 조기 반환으로 OOM 방지.
- [x] **글로벌 메모리 예산 상한제 및 대량 조인 회로 차단기 (`pkg/config`, `pkg/execution`, `cmd/server`)**:
  - 호스트/컨테이너 물리 메모리 자동 감지(Darwin `sysctl`, Linux cgroups v1/v2 및 `/proc/meminfo`) 기반 시스템 RAM의 65% 상한선(1GB OS 예비 및 50% 하한 가드)을 기본 글로벌 상한으로 자동 계산 및 적용.
  - Go 런타임 플랫폼 프리미티브 연동 (`debug.SetMemoryLimit` / GOMEMLIMIT)으로 힙 메모리 상한 근접 시 능동적 사전 GC 트리거 및 OS OOM Killer 사살 방지.
  - 대량 해시 조인(`HashJoinIterator`) 빌드 단계에서 `buildMemory > workMem` 초과 시 Fail-Fast 회로 차단기(`hash join memory budget exceeded`) 가동으로 비정상 쿼리 즉시 차단 및 타 세션 OLTP 격리 보호.
- [x] **테스트 스위트 중복 보일러플레이트 대규모 슬림화 (Net -291줄) (`pkg/wire`, `pkg/api`)**:
  - `pkg/wire/server_test.go`: `authenticatePGClient` 헬퍼 도입 및 동시성/커서 테스트 슬림화 (-176줄).
  - `pkg/api/sqlexec_test.go`: 31개 단위 테스트에 걸친 중복 인메모리 서버 초기화를 `setupMemServer(t)`로 단일화 (-115줄).
  - 전체 패키지 무결성 유지 및 291줄 순수 코드 삭제 달성.
- [x] **가상 카탈로그 쿼리 인터셉터 0-Alloc 프리필터 & 공백 무관 등호 매칭 리팩토링 (`pkg/catalog/virtual`)**:
  - `containsFold` (ASCII 대소문자 무관 무할당 탐색) 및 `hasCatalogCandidate` 도입: 일반 OLTP 쿼리 103.5ns, 0 B/op 탈출.
  - `containsEq`: 키와 값 사이 등호(`=`) 주변의 임의 공백 순열(`= `, ` =`, ` = `, `   =   `, 탭)을 완벽 수용하는 강건한 매처 구현.
- [x] **ODBC / JDBC 엔터프라이즈 드라이버 적합성 인증 (`pkg/wire`)**:
  - PostgreSQL 공식 JDBC(`pgjdbc`) 및 `psqlODBC` 드라이버 초기화 파라미터(`extra_float_digits`, `application_name`) 및 카탈로그 뷰 E2E 적합성 검증 완비.
- [x] **Prepared Statement 바이너리 바인드 파라미터 직접 디코딩 (`P-14`) (`pkg/wire`, `pkg/wire/tds`)**:
  - OID 기반 `storage.Value` 직접 디코딩으로 중간 문자열 변환 제거, 64개 정적 파라미터명 슬라이스 풀링으로 `fmt.Sprintf` 힙 할당 100% 제거.
- [x] **TDS 단일 프레임 버퍼 풀링 & 네트워크 시스템 콜 50% 삭감 (`pkg/wire/tds`)**:
  - `sync.Pool` 기반 4KB 고정 프레임 풀 구축, 8바이트 헤더와 페이로드를 단일 연속 프레임에 패킹하여 패킷당 1회의 `w.Write()` 시스템 콜 원자적 전송.
- [x] **Volcano 벡터화 배치 청크(`Chunk`) 풀링 (`P-12`) (`pkg/execution`)**:
  - 1,024건 단위 튜플 배치 스트리밍 버퍼를 `sync.Pool`로 재활용하여 대규모 테이블 스캔 및 조인 시 메모리 누수 방지 및 GC 부하 극소화.
- [x] **In-Memory OCC 트랜잭션 객체 & 워크스페이스 맵 풀링 (`P-11`) (`pkg/storage/memengine`)**:
  - 트랜잭션 종료 시 Go 1.21+ `clear(m)`으로 해시 버킷 용량을 유지한 채 $O(1)$ 초기화 및 풀링, TPC-C Scale 10에서 20,087 TPS 공식 돌파.
- [x] **반정형 JSON 동일 튜플 DOM 캐시 & 제로-할당 키 스캐너 (`P-7`) (`pkg/storage`, `pkg/engine`)**:
  - `ScanJSONTopLevelKey` 제로-할당 스캐너 및 1,024건 FIFO DOM 캐시 결합으로 다중 JSON 필드 추출 쿼리 CPU/GC 부하 50~70% 경감.
- [x] **가상 카탈로그 멀티테이블 조인 인터셉터 격리 리팩토링 (`pkg/catalog/virtual`, `pkg/engine`)**:
  - DBeaver/Beekeeper/DbGate 메타데이터 크로스 조인 쿼리 가로채기 레이어를 독립 패키지 `pkg/catalog/virtual`로 분리, 1,300여 라인 중복 코드 제거.
- [x] **하드코딩 Go 시딩 제거 및 5개 테이블 기반 독립 데모 스크립트(`scripts/demo_seed.sql`) & `--seed=false` 기본값 전환**:
  - `customers`, `accounts`, `products`, `sales`, `sales_details` 5개 표준 테이블과 `TransferFunds`, `ProcessOrder` 프로시저를 독립 SQL 파일로 분리.
- [x] **PGWire, TDS, HTTP 전반의 통합 관리자 인증 및 AuthProvider SPI 추상화 (`pkg/auth`)**:
  - 단일 슈퍼유저 비밀번호로 3개 와이어 프로토콜 통합 검증, `AuthProvider` SPI 인터페이스 신설로 외부 디렉터리 확장성 확보.
- [x] **SQL 파서 & 엔진 핫패스 정규식 사전 컴파일 리팩토링 (`P-6`) (`pkg/engine`)**:
  - 40여 개 동적 `regexp.MustCompile`을 패키지 싱글톤 및 `rePatternCache`로 승격하여 쿼리당 5~15µs 지연 및 힙 할당 100% 제거.
- [x] **WiredTiger 커서 풀링 & CGo 왕복 비용 절감 (`P-8`) (`pkg/storage/wiredtiger`)**:
  - 세션별 `cursorPool` 보관 및 `C.go_wt_cursor_reset` 재사용으로 894ns 룩업(1.13M+ OPS) 달성.
- [x] **단일 테이블 패스트패스 vs Volcano 인덱스 바운드 로직 일원화 (`R-3`) (`pkg/planner`)**:
  - `pkg/planner/bounds.go` 공통 모듈 신설로 260줄 이상의 중복 바운드 파싱 로직 제거.
- [x] **Pratt 파서 및 내장 함수 도메인별 관심사 분리 (`R-2`) (`pkg/proc`)**:
  - 비대했던 `parser.go`와 `builtins.go`를 11개 도메인 모듈 파일로 깔끔하게 분해.
- [x] **가상 시스템 카탈로그 모듈 분리 및 가상 뷰 레지스트리화 (`R-1`) (`pkg/engine`)**:
  - 거대한 switch 분기를 15줄 수준의 $O(1)$ 레지스트리 맵 디스패치(`VirtualCatalogView`)로 슬림화.

### 5.2. 코어 아키텍처 및 기능 마일스톤 이력
- [x] **Ad-hoc 최상위 익명 제어문 (`IF...ELSE`, `WHILE`) 지원 (`TDS-COMPAT-4` / 시나리오 64, 65) (`pkg/engine`, `pkg/proc`, `pkg/wire/tds`)**:
  - `pkg/engine/query_utils.go`: `SplitStatements`에서 일반 배치 쿼리의 `BEGIN...END` 블록 깊이를 추적하고 `hasNextTokenElse`로 `IF...ELSE` 경계 세미콜론 조기 분할 방지. 단독 `BEGIN;` 트랜잭션 시작과 복합 제어문 블록을 완벽히 분리하도록 `isBeginTransaction` 정합성 보강.
  - `pkg/proc/parser_stmt.go`: `parseIf` 및 `parseWhile`에서 `thenStmt`, `elseStmt`, `body`의 후행 세미콜론 소비를 지원.
  - `pkg/proc/compiler.go`: 최상위 제어문 및 블록을 VDBE 프로그램으로 컴파일하는 `CompileAnonymousBlock` API 추가.
  - `pkg/proc/runtime/vm.go`: `ExecutionResult`에 `Vars` 필드를 추가하고 `collectFrameVars`를 통해 VDBE 슬롯/레지스터 변수 갱신 상태 반환.
  - `pkg/engine/sqlexec.go` & `sqlexec_procs.go`: `execAnonymousBlock` 구현으로 세션 변수(`cs.Vars`) 바인딩, VDBE 트랜잭션 내 제어문 실행, 변수 상태 정규화 동기화 및 관계형 결과셋 반환 파이프라인 구축.
  - 단위 테스트(`TestSQLExecutor_AdHocIfElseControlFlow`, `TestSQLExecutor_AdHocWhileLoop`) 및 `go-mssqldb` 공식 드라이버 E2E 통합 테스트(`TestDriverIntegration_Scenario64_AdHocIfElse`, `TestDriverIntegration_Scenario65_AdHocWhile`) 100% PASS 검증 완료.
- [x] **`GETDATE()` 및 타임스탬프 함수 네이티브 DATETIME 바이너리 직렬화 (`TDS-TYPE-1` / 시나리오 29) (`pkg/engine`, `pkg/proc`, `pkg/wire/tds`)**:
  - `sqlexec_select.go` 및 `builtins_date.go`에서 `GETDATE()`, `CURRENT_TIMESTAMP`, `GETUTCDATE()`, `SYSDATETIME()`을 문자열 대신 Go 네이티브 `time.Time` 및 `storage.DateTimeValue`로 반환.
  - `parser_expr.go`에서 괄호 없는 SQL 표준 함수 `CURRENT_TIMESTAMP`, `CURRENT_DATE`, `CURRENT_TIME`을 `FunctionCallExpr`로 정확히 파싱.
  - TDS 와이어 계층에서 `TypeDateTimeN` (0x6F, 8바이트) 바이너리 메타데이터로 직렬화하여 클라이언트 드라이버(`go-mssqldb`)의 네이티브 `time.Time` 스캔 및 문자열 호환 스캔 동시 완결.
- [x] **T-SQL 유니코드 N-접두사 (`N'...'`) 리터럴 지원 (`TDS-COMPAT-2`) (`pkg/proc`, `pkg/engine`, `pkg/wire/tds`)**:
  - `pkg/proc/lexer.go`: 렉서 `default` 분기에서 `N'...'` 및 `n'...'` 패턴을 식별자가 아닌 `TokenString`으로 직행 처리하여 파싱 오류 및 토큰 누락 방지.
  - `pkg/engine/sqlexec_select.go` & `sqlexec_eval.go`: `isSingleQuotedLiteral`, `evalScalarExpr`, `parseLiteralValue`, `evaluateSingleConditionMap`에서 `N'...'` 언쿼트 및 WHERE 조건(`=`, `<>`, `IN`) 완벽 수용.
  - 단위/DML 테스트(`TestSQLExecutor_NPrefixUnicodeLiterals`) 및 공식 `go-mssqldb` E2E 통합 테스트(`TestDriverIntegration_GoMSSQLDB_NPrefixUnicodeLiterals`) 100% PASS 검증 완료.
- [x] **반정형 JSON 표현식 B-Tree 인덱스 & 내장함수 완비 (`ADR-022`)**:
  - `->`, `->>`, `ISJSON`, `JSON_VALUE`, `JSON_QUERY` 지원, `((data->>'key'))` 기능 인덱스 DDL 파싱, CBO 인덱스 스캔 6.5~7.3배 가속.
- [x] **T-SQL `STRING_SPLIT` 테이블 반환 함수 & 서브쿼리/CROSS APPLY 완비 (`pkg/engine`)**:
  - `STRING_SPLIT` 파싱, 세션 변수 연동, `WHERE id IN (SELECT value FROM STRING_SPLIT(...))` 서브쿼리 해결 및 `CROSS APPLY` 연동.
- [x] **Transactional Outbox 브로커 디스패처 (Kafka/RabbitMQ/Webhook) (`pkg/cdc`)**:
  - CDC 커밋 스트림 기반 아웃박스 릴레이, 무손실 체크포인트(`outbox_checkpoint.json`), 지수 백오프 재시도 및 플러그형 싱크 완비.
- [x] **PostgreSQL 서버 사이드 커서 및 Extended Query 포털 배치 스트리밍 (`pkg/wire`, `pkg/engine`)**:
  - `DECLARE CURSOR`, `FETCH`, `MOVE`, `CLOSE` 커서 라이프사이클 및 Extended Query `Execute` `maxRows` 분할 스트리밍 지원.
- [x] **관용적 No-Op `USE <database>` 및 세션 가상 DB 컨텍스트 동기화 (`pkg/engine`, `pkg/wire/tds`)**:
  - `USE` 명령 무오류 즉시 수용, `ClientSession.Database` 동기화, `DB_NAME()` 반환, TDS `TokenEnvChange (0xE3)` 송출.
- [x] **Always On 스타일 자동 장애 조치(Auto-Failover) 및 리더 선출 (`pkg/replication`)**:
  - 단조 증가 텀, LSN 우선권 리더 선출, 스플릿 브레인 방지 펜싱, 하트비트 타임아웃 감지 및 자동 승격 완비.
- [x] **선언적 스키마 마이그레이션 CLI (`lionsql migrate`) (`cmd/lionsql`, `pkg/migrate`)**:
  - `_migrations` 메타데이터 추적, `new`, `up`, `down`, `status` 라이프사이클, 임베디드 직접 쿼리 실행기 `lionsql sql`.
- [x] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`C-7`) (`pkg/planner`)**:
  - Top-20 최빈값 분리 보관 및 비-MCV 롱테일 히스토그램 이원화로 왜곡 데이터 선택도 오차 최소화.
- [x] **다중 컬럼 확장 통계 및 함수적 종속성 모델링 (`C-5`) (`pkg/planner`)**:
  - 결합 NDV 수집, 종속성 계수($dep$) 산출, `CREATE/DROP STATISTICS` DDL 지원.
- [x] **$N > 4$ 체인 조인 동적 계획법 (Selinger DP) (`B-4`) (`pkg/planner`)**:
  - 비트마스크 메모이제이션 기반 Selinger DP, 데카르트 곱 가지치기, 6개/8개 테이블 체인 서브밀리초 플래닝.
- [x] **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`P-3`) (`pkg/wire`, `pkg/wire/tds`)**:
  - `WriteNBCRowDirect` 직결, 네트워크 직렬화 버퍼 직접 기록으로 중간 힙 할당 제거.
- [x] **TDS PLP (Partially Length-Prefixed) LOB 청크 스트리밍 (`pkg/wire/tds`)**:
  - [MS-TDS] 2.2.5.2 PLP 규격 구현, 8KB 초과 `VARCHAR(MAX)` 청크 분할 전송 완비.
- [x] **볼케이노 벡터화 청크 반복자 (Chunked Batch Next: 1,024건) (`P-4`) (`pkg/execution`)**:
  - 1,024건 프리알로케이션 청크 기반 볼케이노 배치 파이프라인 관통 및 기존 단건 `Next()` 투명 버퍼링 100% 하위 호환성 보장.
- [x] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 커버링 인덱스 (`P-2`) (`pkg/storage`)**:
  - `SkipTupleColumn`, $O(1)$ `LazyTuple`, `CREATE INDEX ... INCLUDE(...)` 커버링 인덱스로 메인 테이블 힙 조회 100% 생략.
- [x] **관계형 조인 시 우측 자식 컬럼 프로젝션 누락 및 지연 디코딩(LazyTuple) 구체화 보강 (`PG-COMPAT-3` / `PG-ORM-9`) (`pkg/execution`, `pkg/wire`)**:
  - `pkg/execution/iterator.go`: `Tuple.Materialize()` 메서드를 구현하여 `storage.LazyTuple` 지연 역직렬화 상태의 튜플을 조인 결합 또는 프로젝션 직전에 안전하게 물리 슬라이스로 디코딩하도록 보장. `MergeColumnIndexMaps`의 `leftLen` 계산 시 슬라이스 누락 방어 로직 추가.
  - `pkg/execution/join.go` & `pkg/execution/merge_join.go`: `mergeTuplesWithArena`, `mergeTuplesWithNullsAndArena`, `mergeNullsWithTupleAndArena` 등 조인 튜플 병합 직전 좌/우측 튜플에 대해 `Materialize()`를 필수 호출하여 미조회 컬럼의 `NULL` 유실을 방지.
  - `pkg/execution/project.go`: `projectTuple`에서 자식 튜플을 사전 구체화하고, 컬럼 탐색 시 한정된 표현식(`f.expr`, 예: `o.amount`, `u.name`)을 비한정 별칭(`f.alias`)보다 먼저 조회하도록 우선순위를 바로잡아 동일 컬럼명 섀도잉 문제 완전 해소.
  - `pkg/wire/driver_integration_test.go` 및 `pkg/wire/driver_integration_wt_test.go` (`TestDriverIntegration_WiredTiger_RelationalJoin`)에 lib/pq 기반 E2E 조인 통합 테스트를 추가하여 PGWire 57개 전 시나리오 100.0% PASS 달성.
- [x] **단일 바이너리(`lionsql`) 완전 통합 및 CLI 라이프사이클 재정립 (`UNIFY-CLI`) (`cmd/lionsql`, `cmd/server`, `pkg/config`)**:
  - `pkg/config/version.go`: LionSQL 릴리스 버전의 단일 소스 오브 트루스(SSOT)로 `const Version = "0.90.0"` 정립.
  - `pkg/config/config.go`: 기본 관리자 계정을 `admin@lionsql.local`로 단일화, `-auth` 기본값을 `true`(Secure by Default)로 강화, 슬로우 쿼리 임계치(`ThresholdMS`) 기본값을 `100.0ms`로 상향.
  - 설정 파일 자동 탐색을 `lionsql.json`으로 단일화하고, 레거시 설정 파일 감지 시 보안 설정 오염을 방지하기 위해 자동 폴백 없이 경고를 출력하고 무시하는 방어 로직 적용.
  - `cmd/lionsql`: PocketBase/CockroachDB 스타일 단일 바이너리 모델 적용. 인자가 없거나 플래그로 시작할 경우 서버를 즉시 포그라운드로 기동(`lionsql`, `lionsql -dir=./data`)하고, `serve`, `start`, `stop`, `restart`, `status`, `doctor`, `init-config`, `migrate`, `sql`, `backup`, `restore`, `version` 등 현대적 서브커맨드 완비.
  - `cmd/server`: 하위 호환성을 위해 `[LionSQL Notice] 'bin/server' is deprecated...` 안내를 출력하고 `bin/lionsql`로 투명하게 실행을 위임하는 경량 래퍼로 전환.
  - 서버 배너 및 기본 관리자 비밀번호(`1234567890`) 사용 시 `[SECURITY WARNING]` 리마인드 경고 출력 로직 탑재.
- [x] **실시간 SSE 스트림(`/api/realtime`, `/api/cdc/stream`) 요청 로깅 제외 및 HTTP Server WriteTimeout 개선 (`pkg/api`, `cmd/lionsql`, `pkg/ui`)**:
  - `cmd/lionsql/server.go`: `http.Server`의 고정 `WriteTimeout: 15s` 설정을 `ReadHeaderTimeout` 및 `IdleTimeout`으로 교체하여 Go 표준 웹 서버가 15초마다 SSE 영구 연결을 강제 종료하던 문제 해소.
  - `pkg/api/realtime.go`: `http.NewResponseController`를 통해 SSE 스트림의 WriteDeadline을 해제하여 영구 Keep-Alive 스트리밍 보장.
  - `pkg/api/request_logger.go`: `/api/realtime` 및 `/api/cdc/stream`과 같은 영구 스트리밍 채널은 단발성 REST API 지연 시간 통계 왜곡을 방지하기 위해 요청 로거 기록 대상에서 제외.
  - `pkg/ui/dashboard.html`: Request Log UI에서 스트리밍 요청 수명이 유입될 경우 빨간색 에러 지연 시간(`#ef4444`) 대신 하늘색 스트림 뱃지(`(stream)`)로 렌더링하도록 안전 보강.
- [x] **레거시 가상 DB 제거 및 와이어 프로토콜별 표준 단일 DB 정립 (`pkg/catalog/virtual`, `pkg/engine`)**:
  - `pkg/catalog/virtual/pg.go`: `pg_database` 목록에서 레거시 DB를 완전 제거하고 PostgreSQL 표준 `postgres` 단일 DB만 노출하도록 정돈. `pg_stat_activity`의 기본 datname도 `postgres`로 갱신.
  - `pkg/engine/sqlexec_dbcc.go`: `sys.databases` 및 `sys.master_files`에서 레거시 항목을 완전 삭제하고 MSSQL 표준 `master` 단일 DB 및 master 파일만 노출하도록 정돈.
  - `pkg/catalog/virtual/info_schema.go`: `information_schema`의 `table_catalog`, `catalog_name` 등을 `postgres`로 일원화.
  - `pkg/engine/coordinator_impl.go`, `sqlexec_select.go`, `sqlexec_procs.go`: 세션 기본 데이터베이스를 PGWire/제네릭은 `postgres`, TDS는 `master`로 단일화하여 Beekeeper, DBeaver 등 외부 도구에서 단일 카탈로그 UX 완비.
- [x] **v0.90.0 기능 동결 4단계 문서 전수 정돈 마스터 플랜 (`DOC-CLEANUP`) (`CHANGELOG.md`, `README.md`, `doc/`, `ROADMAP.md`, `notes/`)**:
  - `CHANGELOG.md`: `[0.90.0]` 공식 릴리스 블록 확정 (글로벌 메모리 거버너 `MEM-1`, 단일 CLI `UNIFY-CLI`, Web Studio 실시간 스트림/로그/DDL 모달, 단일 카탈로그 표준화 등 Keep a Changelog 표준 정돈).
  - 루트 `README.md` & `README.ko.md`: `v0.90.0 Feature Freeze` 배지 도입, 단일 바이너리 `./bin/lionsql` 포그라운드/데몬/관리 CLI 전면 반영, 4대 Web Studio 신규 뷰 및 외부 클라이언트 DSN 일원화.
  - `doc/` 디렉터리 레거시 전수 정돈: `postgres`/`master` 단일 카탈로그 일원화, 미지원 TablePlus 언급 삭제 및 Beekeeper/DBeaver/Navicat 표준화, 구형 `./bin/server` ➔ `./bin/lionsql` 실행 명령 치환.
  - `ROADMAP.md`: Phase 1~5 전체 완료 및 Phase 6 `v0.90.0 Feature Freeze` 달성 상태 동기화, v1.0.0 LTS(Phase 7) 마일스톤 정립.
  - `notes/DOCUMENTATION_CLEANUP_PLAN.md`: 4단계 작업 현황 및 검증 결과 100% 완료(`[x]`) 상태 기록.


