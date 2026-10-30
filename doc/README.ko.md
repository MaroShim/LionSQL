# LionSQL 기술 문서 색인 디렉토리 (Technical Documentation Directory)

> [English Version](README.md)

단일 독립 실행 파일로 패키징된 고동시성 임베디드 OLTP 데이터베이스 엔진이자 모던 Backend-as-a-Service (BaaS)인 **LionSQL**의 공식 기술 문서 디렉토리에 오신 것을 환영합니다.

---

## 1. 주제 영역별 문서 맵 (Documentation Map)

```
                              [ LionSQL 기술 문서 루트 ]
                                          │
       ┌──────────────────┬───────────────┴───────────────┬──────────────────┐
       ▼                  ▼                               ▼                  ▼
[ 코어 및 스토리지 ] [ 쿼리 및 VDBE VM ]             [ 와이어 프로토콜 ]    [ BaaS 및 API ]
- ARCHITECTURE.md   - internal/VDBE.md        - internal/WIRE.md   - REST_API_RECORDS_GUIDE.md
- internal/STORAGE.md - internal/CBO_VOLC.md - TSQL_REFERENCE.md   - WEBHOOKS_AND_RLS.md
- internal/TX_CONC  - PERCENTILE_ANALYTICS.md  - CLIENT_TOOL_INT.md  - CRON_AND_MAILER.md
- SNAPSHOT_ISOLATION - LOG_PROCESSING_GUIDE.md - TDS_COMPAT_REPORT   - CDC_DUCKDB_INT.md
- internal/RECORD   - EXTENSIONS.md            - PGWIRE_COMPAT_REP   - REST_API_REFERENCE.md
- DATA_DICTIONARY   - HIGH_AVAIL.md                                  - WEB_SDK.md
```

---

## 2. 문서 상세 색인 (Directory of Documents)

### 2.0. 공식 매뉴얼 (Official Manuals)
* [`USER_MANUAL.ko.md`](USER_MANUAL.ko.md)
  * 설치, 기동, 클라이언트 도구 연동, 데이터베이스 개발 및 운영 유지보수 전 과정을 안내하는 공식 사용자 매뉴얼.
* [`REFERENCE_MANUAL.ko.md`](REFERENCE_MANUAL.ko.md)
  * CLI 플래그, 설정 파일, SQL 문법 및 구문, 데이터 타입, 내장 함수, 가상 카탈로그 뷰, REST API 및 와이어 프로토콜 기술 사양을 총망라하는 공식 레퍼런스 매뉴얼.

---

### 2.1. 아키텍처 및 코어 데이터베이스 엔진 (Architecture & Core Storage)
* [`WHY_TSQL_GO_WIREDTIGER.md`](WHY_TSQL_GO_WIREDTIGER.md)
  * T-SQL 절차적 VM, Go 런타임 동시성, WiredTiger 락프리 B+Tree 3대 핵심 기반 기술의 전략적 결합 분석.
  * Zero-Vacuum 영속성, 0-RTT 저장 프로시저의 근거, 기술적 반론 검토 및 100MB 이하 단일 바이너리 폼팩터의 설계 철학.
* [`FOUNDATIONAL_DESIGN_DECISIONS.md`](FOUNDATIONAL_DESIGN_DECISIONS.md)
  * LionSQL의 태동과 창립 전략적 아키텍처 결정 기록.
  * WiredTiger 락프리 B+Tree, Pure-Go In-Memory OCC 엔진, Go 런타임 동시성, VDBE 바이트코드 VM 기반 T-SQL 코어, PGWire 에코시스템 호환성, 임베디드 HTTP/REST BaaS 엔진 및 Antigravity AI 페어 프로그래밍 개발 방법론 심층 분석.
* [`internal/ZERO_ALLOCATION_ARCHITECTURE.md`](internal/ZERO_ALLOCATION_ARCHITECTURE.md)
  * In-Memory OCC 20,000+ TPS 및 WiredTiger 16,800+ TPS를 달성하기 위한 제로 할당 메모리 아키텍처, 버퍼 재사용 전략 및 GC 완화 기법.
  * 계층별 분석: TDS 단일 프레임 풀링, PGWire 바이너리 바인드 직접 디코딩, 카탈로그 제로-할당 ASCII 사전 필터링(`containsFold`), VDBE 레지스터 재활용, Volcano 청크 스트리밍(`TupleChunk` sync.Pool), WiredTiger `WT_CURSOR` 풀링/리셋.
* [`ARCHITECTURE.md`](ARCHITECTURE.md)
  * LionSQL 전 계층 엔드투엔드 아키텍처 청사진.
  * 듀얼 스토리지 백엔드(Pure-Go OCC 메모리 엔진 vs WiredTiger 락프리 B+Tree MVCC).
  * 앰비언트 ACID 트랜잭션, 격리 수준, 고루틴 커넥션 풀러.
* [`internal/STORAGE.md`](internal/STORAGE.md)
  * 스토리지 엔진 인터페이스 규약 딥다이브.
  * SkipList 인메모리 인덱싱 vs WiredTiger 페이지 구조, 체크포인트 및 WAL 지속성 메커니즘.
  * 물리적 스토리지 디렉터리 레이아웃, `catalog.json` 메타데이터 영속화, `cbo_stats.json` 및 보조 인덱스 저장 구조.
* [`internal/RECORD_BINARY_FORMAT.md`](internal/RECORD_BINARY_FORMAT.md)
  * 스키마 인식형 콤팩트 바이너리 튜플 직렬화 명세 (`pkg/storage/tuple.go`).
  * 비트 단위 레이아웃: `0x01` 버전 헤더, $\lceil N/8 \rceil$ NULL 비트맵, 고밀도 ZigZag varint 페이로드.
  * PostgreSQL 힙 튜플 `t_bits`, CockroachDB/TiDB KV 인코딩, Protobuf varint 계보 비교.
  * WiredTiger B+Tree 디스크 레코드 저장 vs In-Memory OCC WAL 지속성 비교.
* [`internal/TRANSACTION_CONCURRENCY.md`](internal/TRANSACTION_CONCURRENCY.md)
  * 트랜잭션 격리 수준 및 동시성 제어 모델 명세서.
  * 논블로킹 스냅샷 읽기 및 프라이빗 워크스페이스 격리.
  * 단조 증가 정수 버전을 사용하는 In-Memory OCC vs 해저드 포인터와 타임스탬프를 갖춘 WiredTiger MVCC.
  * VDBE 트랜잭션 결합(`exec.RunInTx`)과 벤치마크 단일 트랜잭션 원자성 원칙.
* [`DATA_DICTIONARY.md`](DATA_DICTIONARY.md)
  * 시스템 카탈로그, 시스템 테이블, 스키마 메타데이터 영속화 및 가상 카탈로그 뷰(`sys.tables`, `INFORMATION_SCHEMA`).
* [`RANGE_PARTITIONING.md`](RANGE_PARTITIONING.md)
  * ClickHouse 스타일 시계열 자동 롤링 레인지 파티셔닝 및 $O(1)$ 초고속 파티션 파기.
  * 수동 생성 없는 동적 파티션 수명주기(`toYYYYMM`, `MONTH`, `toYYYYMMDD`, `DAY`, `toYYYY`, `YEAR`), CBO 파티션 프루닝을 통한 디스크 I/O 70%+ 절감, `EXPLAIN` 시각화 및 시스템 뷰(`system.partitions`).
* [`HIGH_AVAILABILITY.md`](HIGH_AVAILABILITY.md)
  * 크래시 안전성, 체크포인팅, AOF 압축 및 무중단 온라인 핫 백업/복원 파이프라인.
* [`STORAGE_ENCRYPTION_GUIDE.md`](STORAGE_ENCRYPTION_GUIDE.md)
  * WiredTiger 스토리지 엔진을 위한 저장 데이터 암호화(TDE) 아키텍처 (AES-256-CBC, PBKDF2 키 파생, 무손실 데이터 무결성).
* [`ADR.md`](ADR.md)
  * 핵심 설계 결정 기록 (VDBE 단일화, 듀얼 파서 폴백, 단일 바이너리 배포, HA 복제, 듀얼 트랙 UDF, 콤팩트 바이너리 튜플 코덱, 멀티 프로토콜 카탈로그 인터셉션, 하이브리드 JSON 표현식 B-Tree 인덱싱 등 ADR-001 ~ ADR-022).

---

### 2.2. 쿼리 컴파일, 최적화 및 가상 머신 (Query Engine & VM)
* [`internal/VDBE.md`](internal/VDBE.md)
  * SQLite 스타일 레지스터 기반 가상 데이터베이스 엔진(VDBE) 아키텍처.
  * 바이트코드 명령어 셋 명세 (`OpInit`, `OpOpenRead`, `OpIndexScan`, `OpRelQuery`, `OpYield`).
  * `sync.Pool` 레지스터 재사용 및 컴파일된 실행 계획 캐싱 (`*runtime.PlanCache`).
* [`internal/CBO_VOLCANO.md`](internal/CBO_VOLCANO.md)
  * Volcano 반복자 실행 파이프라인 (`SeqScan`, `IndexScan`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`).
  * 비용 기반 옵티마이저(CBO), 선택도 모델, 64-버킷 균등 깊이 히스토그램.
  * 상수 폴딩, 조건 푸시다운, 상관 서브쿼리 비상관화 (`SEMI`/`ANTI` 조인 변환).
  * `work_mem` 메모리 거버너 및 디스크 외부 머지 소트.
* [`JSON_INDEXING_GUIDE.md`](JSON_INDEXING_GUIDE.md)
  * 반정형 JSON 문서 쿼리 및 표현식 B-Tree 인덱싱 가이드 및 구현 완료 매트릭스.
  * PostgreSQL, MySQL 8.0, Microsoft SQL Server 문법 비교 분석.
  * 듀얼 프로토콜 AST 정규화(`*proc.JsonExtractExpr`), `JSON_VALUE`, `JSON_QUERY`, `ISJSON`, PostgreSQL `->` / `->>` 연산자.
  * 단일 문장 함수형 B-Tree 인덱스 DDL(`CREATE INDEX ... ON tbl ((data->>'key'))`) 및 CBO 인덱스 매칭.
* [`TSQL_REFERENCE.md`](TSQL_REFERENCE.md)
  * 완전한 Microsoft T-SQL 문법 호환성 가이드.
  * 저장 프로시저, 사용자 정의 함수(스칼라 및 TVF), 트리거(`INSERTED`/`DELETED` 델타 테이블).
  * 제어 흐름문 (`IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO`, 커서).
* [`SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md`](SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md)
  * LionSQL 다중 방언 SQL 호환성 및 개발자 편의 문법 가이드.
  * MySQL 스타일 손끝 편의 문법(백틱 ` ` `, `DESC`, `SHOW TABLES`), 다중 방언 내장 함수 Best 7(`IFNULL`, `CURDATE`, `MOD`, `TRUNC`, `INSTR` 등), T-SQL 단방향 읽기 전용 커서(`PROC-CURSOR-1`).
* [`EXTENSIONS.md`](EXTENSIONS.md)
  * 고급 SQL 확장 기능: 윈도우 함수 (`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), JSON/JSONB 연산자, CTE 재귀 쿼리, 외래키 캐스케이드 액션, 듀얼 트랙 UDF 아키텍처.
* [`PERCENTILE_ANALYTICS.md`](PERCENTILE_ANALYTICS.md)
  * 연속/이산 분위수 함수 (`PERCENTILE_CONT`, `PERCENTILE_DISC`), ANSI `WITHIN GROUP (ORDER BY ...)`, `MEDIAN`, 스트리밍 근사 분위수 (`APPROX_PERCENTILE` via T-Digest).
* [`LOG_PROCESSING_GUIDE.md`](LOG_PROCESSING_GUIDE.md)
  * 엔진 내장 텍스트 및 로그 처리 연산자 (`GREP`, `AWK`, `SPLIT_PART`).
  * 제로 카피 푸시다운 필터링, 정형 토큰 추출, 스키마리스 로그 분석.

---

### 2.3. 와이어 프로토콜 및 클라이언트 호환성 (Wire Protocols & Clients)
* [`internal/WIRE_PROTOCOLS.md`](internal/WIRE_PROTOCOLS.md)
  * **PostgreSQL Wire Protocol v3** (포트 `5433`): Simple/Extended 쿼리 프로토콜 (`Parse`, `Bind`, `Execute`, `Sync`), 파라미터 바인딩, SSL/TLS.
  * **Microsoft SQL Server TDS 7.x/8.0** (포트 `1433`): Prelogin, Login7, RPC 패킷, `sp_prepexec` 바이트코드 플랜 캐시 연동.
  * 클라이언트 호환성 매트릭스 (`psql`, DBeaver, GORM, Prisma, `sqlcmd`, SSMS, Azure Data Studio).
* [`CLIENT_TOOL_INTEGRATION.md`](CLIENT_TOOL_INTEGRATION.md)
  * GUI 및 CLI 클라이언트 도구 연동 종합 가이드.
  * 검증된 클라이언트: Beekeeper Studio, DBeaver, Azure Data Studio, SSMS, `psql`, `sqlcmd`.
  * 멀티 프로토콜 카탈로그 인터셉터, 스키마 탐색 라우팅, 페이지네이션 정규화 (`OFFSET FETCH` / `TOP`).
  * 주요 프로그래밍 언어 드라이버 연결 문자열 및 커넥션 풀 가이드 (Go, Python, Node.js, C#, Rust, Java).
* [`GITEA_INTEGRATION_GUIDE.ko.md`](GITEA_INTEGRATION_GUIDE.ko.md)
  * LionSQL 기반 Gitea 구축 및 운영 공식 실전 연동 가이드.
  * PostgreSQL Wire Protocol(포트 `5433`) 및 Microsoft SQL Server TDS(포트 `1433`) 듀얼 모드 설정, 헤드리스 무인 자동 마이그레이션, 모의 데이터 종합 시딩, 스토리지 엔진 선택 및 프로덕션 운영 튜닝 모범 사례.
* [`../notes/GITEA_INTEGRATION_MILESTONE.ko.md`](../notes/GITEA_INTEGRATION_MILESTONE.ko.md)
  * Gitea(Go 풀스택 Git 플랫폼) 및 XORM(MSSQL 방언) 완전 초기화 및 116개 테이블 프로덕션 기동 마일스톤 보고서.
  * 조건부 DDL, 복합 조인 컬럼 메타데이터 라우팅, T-SQL 대입식 별칭 및 `sys.fn_helpcollations()` 연동 분석.
* [`TDS_COMPATIBILITY_REPORT.md`](TDS_COMPATIBILITY_REPORT.md)
  * Microsoft SQL Server TDS 7.x/8.0 (포트 `1433`) 123개 엔터프라이즈 T-SQL/ORM 시나리오 실측 호환성 검증 보고서 (**실무 호환율 99.2%**: 122/123 PASS).
* [`PGWIRE_COMPATIBILITY_REPORT.md`](PGWIRE_COMPATIBILITY_REPORT.md)
  * PostgreSQL Wire Protocol v3 (포트 `5433`) 주요 ORM 프레임워크 및 GUI 관리 도구 실측 연동 호환성 검증 보고서.

---

### 2.4. Backend-as-a-Service (BaaS), REST 및 클라이언트 SDK
* [`WEB_STUDIO_GUIDE.ko.md`](WEB_STUDIO_GUIDE.ko.md) ([English](WEB_STUDIO_GUIDE.md))
  * 브라우저 기반 내장 올인원 Web Studio 가이드: 스키마 모델링, 데이터 그리드 CRUD, SQL 쿼리 콘솔, 실시간 쿼리 감사 로그, 핫 백업/복원.
* [`REST_API_RECORDS_GUIDE.md`](REST_API_RECORDS_GUIDE.md)
  * PocketBase 스타일의 무설정 즉시 생성 RESTful CRUD 엔드포인트 (`/api/records/:table`).
  * 고급 URL 쿼리 엔진: 비교 연산자, 패턴 매칭(`LIKE`), 문자열 포함(`~`), 다중 컬럼 정렬, 페이징.
* [`WEBHOOKS_AND_RLS.md`](WEBHOOKS_AND_RLS.md)
  * **비동기 웹훅 디스패처**: 논블로킹 워커 풀, 지수 백오프 재시도, HMAC-SHA256 서명 검증.
  * **선언적 행 수준 보안 (RLS)**: `@auth.id`, `@auth.role`, `@auth.tenant_id` 변수 치환을 지원하는 테이블 접근 규칙(`list`, `view`, `create`, `update`, `delete`).
* [`CRON_AND_MAILER.md`](CRON_AND_MAILER.md)
  * **임베디드 크론 잡 스케줄러**: 표준 5필드 크론 구문 기반의 정기 SQL 쿼리 및 T-SQL 저장 프로시저 자동화.
  * **임베디드 SMTP 메일러**: 트랜잭션 HTML 이메일 템플릿(`welcome`, `password_reset`, `verification`) 및 발송 감사 링 버퍼.
* [`WEB_SDK.md`](WEB_SDK.md)
  * 공식 범용 Web SDK 및 HTML 임베딩 가이드 (`lionsql.js`, `lionsql.d.ts`).
  * 무의존성 단일 파일 템플릿, 바닐라 `fetch()`, 실시간 Server-Sent Events(SSE) 라이브 싱크, HTMX 연동 레시피.
* [`REST_API_REFERENCE.md`](REST_API_REFERENCE.md)
  * 시스템 헬스, 인증, 메트릭, 테이블 CRUD, 프로시저 실행 등 전 엔드포인트를 포괄하는 HTTP REST API 및 실시간 SSE 종합 명세서.
* [`REST_API_COMPATIBILITY_REPORT.md`](REST_API_COMPATIBILITY_REPORT.md)
  * HTTP REST 엔진, OpenAPI 엔드포인트, 실시간 SSE 스트리밍, 개발자 API 프리뷰 종합 기능 실측 검증 보고서.
* [`CDC_DUCKDB_INTEGRATION.md`](CDC_DUCKDB_INTEGRATION.md)
  * 실시간 CDC 스트리밍(`GET /api/cdc/stream`) 및 DuckDB와의 비결합형 HTAP 분석 연동 가이드 (Python 및 DuckDB 워커 구현 포함).
* [`SNAPSHOT_ISOLATION_ARCHITECTURE.md`](SNAPSHOT_ISOLATION_ARCHITECTURE.md)
  * 듀얼 스토리지 엔진 스냅샷 격리(WiredTiger MVCC vs In-Memory OCC), 고유 스냅샷 시점 차이, Zero-Vacuum 아키텍처 및 DuckDB 초기 동기화/CDC 정합성 컷오버 기술 백서.

---

### 2.5. 운영, 보안 및 벤치마크 (Operations, Security & Performance)
* [`TRANSACTION_PERFORMANCE_GUIDE.ko.md`](TRANSACTION_PERFORMANCE_GUIDE.ko.md)
  * 트랜잭션 성능, 지연시간-내구성 트레이드오프 및 워크로드별 옵션 튜닝 가이드 (초저지연 인메모리 캐시부터 무손실 금융 원장까지).
* [`OPERATIONS.md`](OPERATIONS.md)
  * 프로덕션 배포 가이드라인, CLI 플래그, 환경 설정 파라미터 및 안전한 정상 종료(Graceful Shutdown).
* [`SCHEMA_MIGRATION_GUIDE.md`](SCHEMA_MIGRATION_GUIDE.md)
  * 선언적 스키마 마이그레이션 툴체인 (`lionsql migrate new/up/down/status`), 타임스탬프 버전 관리, 원자적 트랜잭션 DDL 안전성, CI/CD 및 쿠버네티스 패턴.
* [`METRICS.md`](METRICS.md)
  * Prometheus 메트릭 (`/metrics`) 및 슬로우 쿼리 로깅 (`/api/slow-queries`).
* [`SECURITY.md`](SECURITY.md)
  * 인증 모델, JWT 수명 주기, API 키 역할 스코핑, 전송 계층 네트워크 암호화.
* [`ENTERPRISE_AUTHENTICATION_SPI.md`](ENTERPRISE_AUTHENTICATION_SPI.md)
  * 엔터프라이즈 디렉터리 서비스(LDAP/AD/PAM) 연동을 위한 플러그형 `AuthProvider` SPI, 멀티 프로토콜 계정 통합, 경계 프록시 보안 아키텍처.
* [`BENCHMARK.md`](BENCHMARK.md)
  * 성능 벤치마크 (TPC-C, pgbench, 읽기/쓰기 지연 시간, 동시성 스케일링 측정치).
* [`COMPLEX_QUERY_BENCHMARK_GUIDE.ko.md`](COMPLEX_QUERY_BENCHMARK_GUIDE.ko.md) ([English](COMPLEX_QUERY_BENCHMARK_GUIDE.md))
  * Star Schema Benchmark(SSB) 복합 분석 쿼리 및 Volcano CBO 엔진 성능 가이드, CLI 사용법(`bin/ssb`), 타 대표 DBMS(DuckDB, PostgreSQL, SQLite) 실측 성능 비교 및 아키텍처 분석.
* [`TESTING.md`](TESTING.md)
  * 로컬 빌드, 테스트 및 회귀 검증 가이드: 3단계 파이프라인, `-run` 필터링, 빌드 태그, 패키지별 회귀 테스트 맵, Go 마이크로 벤치마크, TPC-C 성능 회귀 측정, A/B 비교 절차.
* [`DATABASE_MIGRATION_GUIDE.md`](DATABASE_MIGRATION_GUIDE.md)
  * PostgreSQL, Microsoft SQL Server, SQLite, PocketBase로부터 LionSQL로의 데이터베이스 이전 절차.
* [`RELEASE_AUTOMATION_GUIDE.ko.md`](RELEASE_AUTOMATION_GUIDE.ko.md)
  * 원클릭 릴리스 자동화 파이프라인(`scripts/release.sh`), 4대 SSOT 버전 동기화, Keep a Changelog 분류, Windows .exe 크로스 컴파일 및 태깅 가이드.
* [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
  * 일반적인 운영 에러, 연결 진단 기법 및 장애 디버깅 가이드.

---

### 2.6. 개발자 인수인계, 엔지니어링 노트 및 백로그 (Handover & Notes)
* [`../notes/README.md`](../notes/README.md) ([한국어: `../notes/README.ko.md`](../notes/README.ko.md))
  * 개발 이면의 맥락, 핵심 아키텍처 불변 규칙, 실전 주의사항(Pitfalls) 및 도메인별 실행 계획서(`*_PLAN.md`).
* [`../ROADMAP.md`](../ROADMAP.md)
  * 단일 노드 OLTP부터 Always On HA 및 분리형 HTAP 분석까지의 버전별 마일스톤 로드맵.
* [`../TODO.ko.md`](../TODO.ko.md)
  * 일 단위 액티브 엔지니어링 백로그 및 작업 상태 트래커.
* [`../CHANGELOG.md`](../CHANGELOG.md)
  * Keep a Changelog 표준을 준수하는 버전별 릴리즈 변경 내역 및 상세 변경 로그.
