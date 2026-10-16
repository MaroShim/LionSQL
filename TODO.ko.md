# SQLion Engineering TODO & Backlog

이 문서는 SQLion의 단기 엔지니어링 우선순위 과제, 도메인별 기능 백로그, 최근 완료된 마일스톤 이력을 관리하는 공식 엔지니어링 백로그입니다. 중장기 아키텍처 로드맵은 [ROADMAP.md](ROADMAP.md)를 참조하세요.

---

## 식별 코드 체계 범례 (Taxonomy Legend)

| 접두사 / 코드 | 트랙 명칭 (도메인) | 번호 부여 규칙 및 범위 | 설명 |
| :---: | :--- | :---: | :--- |
| **`P1` ~ `P3`** | **우선순위 (Priority)** | `P1` ~ `P3` | 개발 착수 긴급도 (P1: 즉시 착수/완료, P2: 차기 착수, P3: 생태계 검증) |
| **`P-1` ~ `P-10`** | **엔진 성능 (Performance)** | `P-1` ~ `P-10` | 스토리지, 디코딩, 와이어, 캐시, 정규식, JSON, 병렬 쿼리 전반의 10대 핵심 성능 극대화 트랙 |
| **`R-1` ~ `R-5`** | **코드 리팩토링 (Refactoring)** | `R-1` ~ `R-5` | 거대 파일 모듈 분해, 단일 책임 원칙(SRP), DRY 중복 해소 및 가상 뷰 레지스트리화 |
| **`A` ~ `D`** | **CBO 최적화 (Optimizer)** | `A-1` ~ `D-9` | 비용 기반 옵티마이저 4대 기능 트랙 (A: 통계 수집, B: 조인 알고리즘, C: 선택도, D: 운영 제어) |
| **`S-1` ~ `S-7`** | **SQL 문법 (Syntax)** | `S-1` ~ `S-7` | T-SQL 및 ANSI SQL 7대 문법 호환성 완비 트랙 (전 항목 완료) |

> [!NOTE]
> 전체 식별 코드 일람 및 상세 명세는 [notes/TAXONOMY.md](notes/TAXONOMY.md)를 참조하세요.

---

## 1. 차기 착수 우선순위 과제 (Next High Priorities)

| 우선순위 | 영역 | 핵심 과제 | 대상 위치 | 기대 효과 및 구현 요약 |
| :---: | :---: | :--- | :--- | :--- |
| **P1** | **엔진 성능 (P-6)** | **SQL 파서 & 엔진 핫패스 정규식 사전 컴파일 리팩토링** | `pkg/engine` | 쿼리당 동적 `regexp.MustCompile` 힙 할당 및 5~15µs 지연 완전 제거 |
| **P1** | **스토리지/보안** | **투명한 데이터 암호화 (TDE: AES-256-GCM) & 감사 로그** | `pkg/storage` | 데이터 파일 암호화 및 불변 감사 추적 로그 완비 |
| **P2** | **엔진 성능 (P-7)** | **반정형 JSON 동일 튜플 DOM 캐시 & 제로-할당 경량 키 스캐너** | `pkg/storage`, `pkg/engine` | 다중 JSON 필드 추출 쿼리 CPU/GC 부하 50~70% 경감 |
| **P2** | **엔진 성능 (P-8)** | **WiredTiger 커서 풀링 & CGo 왕복 비용 절감 (`cur->reset`)** | `pkg/storage/wiredtiger` | CGo 경유 WiredTiger 단건 조회/인덱스 스캔 TPS 30%+ 향상 |
| **P2** | **엔진 성능 (P-9)** | **정형 SQL Parameterized 쿼리 실행 계획 캐시 (SQL Plan Cache)** | `pkg/engine`, `pkg/planner` | 반복 실행 OLTP 쿼리의 파서/CBO 플래너 오버헤드 100% 바이패스 |
| **P3** | **엔진 성능 (P-10)** | **Volcano 대규모 분석 쿼리 멀티코어 병렬 실행기 (`ParallelSeqScan`)** | `pkg/execution` | 대용량 OLAP 분석 쿼리 멀티코어 선형 가속 (4~8배) |
| **P3** | **생태계/드라이버** | **ODBC / JDBC 엔터프라이즈 드라이버 적합성 인증 확대** | `pkg/wire` | 엔터프라이즈 BI 도구 및 레거시 드라이버 연결 안정화 |
| **P3** | **플러그인/확장** | **Wasm 런타임 샌드박스 UDF (wazero)** | `pkg/extension` | 순수 Go wazero 기반 안전한 격리형 서드파티 확장 함수 |

### P1/P2 최근 완료 과제: 고가용성, 성능 & 리팩토링
- [x] **가상 시스템 카탈로그 모듈 분리 및 가상 뷰 레지스트리화 (`R-1`) (`pkg/engine`)**
  - **배경**: 단일 파일(3,283라인)에 PostgreSQL `pg_catalog`, ANSI `information_schema`, MSSQL `sys.tables`/DMV, DBeaver/Beekeeper/DbGate/HeidiSQL 도구별 쿼리 인터셉터가 혼재되어 거대한 switch 분기문과 높은 유지보수 비용을 초래함.
  - **구현 내용**:
    - 도메인별 4개 모듈 분리 완료: `sqlexec_pgcatalog.go`(634줄), `sqlexec_info_schema.go`(420줄), `sqlexec_mssql_sys.go`(554줄), `sqlexec_gui_interop.go`(477줄).
    - `VirtualCatalogView` 함수 시그니처 및 `registerCatalogView` 레지스트리 패턴 도입: 거대한 switch 분기를 15줄 수준의 $O(1)$ 맵 디스패치(`execPgCatalogSelect`) 및 $O(1)$ 테이블 식별(`isPgCatalogTable`)로 슬림화.
    - 전체 25개 패키지 레이스 감지 테스트 100% PASS 검증 완료.
- [x] **반정형 JSON 표현식 B-Tree 인덱스 & 내장함수 완비 (`ADR-022`) (`pkg/proc`, `pkg/storage`, `pkg/planner`, `pkg/execution`, `pkg/engine`)**
  - **배경**: PostgreSQL JSON 이항 연산자(`->`, `->>`) 및 T-SQL/ANSI JSON 표준 함수(`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`) 지원, 기능 표현식 인덱스(`((data->>'key'))`)를 통한 반정형 도큐먼트 쿼리의 초고속 B-Tree 인덱스 스캔 가속.
  - **구현 내용**:
    - JSON 내장 함수 및 Pratt 파서 연산자(`->`, `->>`) 완비.
    - 기능 표현식 인덱스 DDL(`CREATE [UNIQUE] INDEX ON tbl ((expr))`) 파싱 및 AST 정규화.
    - 스토리지 튜플 삽입/갱신/삭제 시 sub-microsecond JSON 키 추출(`storage.ExtractJSONIndexKey`), SkipList 및 WiredTiger B+Tree 실시간 동기화.
    - ANSI SQL UNIQUE 제약 조건 준수: NULL 다중 허용(`!key.IsNull`) 및 중복 비-NULL 키 삽입 시 원자적 롤백(`ErrUniqueViolation`).
    - CBO 옵티마이저 매칭(`pkg/planner`) 및 실행기 바인딩(`pkg/execution`): `WHERE data->>'city' = 'Seoul'` 또는 `JSON_VALUE(data, '$.city') = 'Seoul'` 평가 시 CBO가 `Index Scan` 자동 선택.
    - 1,000건 JSON 레코드 대상 SeqScan(2.17ms) 대비 IndexScan(334µs)으로 7배+ 성능 가속 검증 완료.
- [x] **T-SQL `STRING_SPLIT` 테이블 반환 함수 & 서브쿼리/CROSS APPLY 완비 (`pkg/engine`)**
  - **배경**: 모던 SQL Server 및 T-SQL 저장 프로시저의 필수 TVF인 `STRING_SPLIT(string, separator [, enable_ordinal])` 지원을 통해 `WHERE id IN (SELECT value FROM STRING_SPLIT(@ids, ','))` 및 동적 태그 분해 `CROSS APPLY STRING_SPLIT(col, ',')`의 100% 호환성 확보.
  - **구현 내용**:
    - `pkg/engine/sqlexec_string_split.go`: `STRING_SPLIT(string, separator [, enable_ordinal])` 파싱 및 레코드셋 생성기 구현. 단일 쿼리(`SELECT value, ordinal FROM STRING_SPLIT(...)`), 테이블 별칭(`AS s`), 컬럼 별칭, `WHERE` 필터링, `ORDER BY`, `DISTINCT`, 집계 `COUNT(*)`, 페이징(`LIMIT`/`OFFSET` 및 T-SQL `OFFSET ... ROWS FETCH NEXT ... ROWS ONLY`) 완비.
    - 세션 변수 연동: `@tags`, `@ids` 등 세션 변수를 즉각 해결하여 프로시저 및 매개변수화 쿼리와 매끄럽게 결합.
    - 서브쿼리 해결 (`pkg/engine/sqlexec_explain.go`): `resolveSubqueriesWithSession`을 통해 `WHERE id IN (SELECT value FROM STRING_SPLIT(...))` 구문을 세션 컨텍스트 하에서 리터럴 리스트로 원자적 치환 후 초고속 엔진 경로 실행.
    - `CROSS APPLY` 및 `OUTER APPLY` 연동: 행별 컬럼 데이터를 매개변수화하여 다중 행 조인 스트림 자동 생성.
    - 단위 및 E2E 테스트 완비 (`pkg/engine/sqlexec_string_split_test.go`).
- [x] **Transactional Outbox 브로커 디스패처 (Kafka/RabbitMQ/Webhook) (`pkg/cdc`, `pkg/config`, `pkg/api`, `cmd/server`)**
  - **배경**: 분산 마이크로서비스 환경에서 듀얼 라이트(Dual-Write) 문제 해결 및 데이터베이스 커밋과 100% 원자적으로 연동되는 무손실 메시지 브로커 발행(At-Least-Once Delivery).
  - **구현 내용**:
    - `pkg/cdc/outbox.go`: 트랜잭션 커밋 CDC 스트림 구독 및 `OutboxMessage` 정규화 디스패처 구현. 일반 테이블 변경 CDC 스트리밍(`sqlion.<table_name>`)과 전용 아웃박스 테이블(`outbox` / `_outbox`) 스키마(`topic`, `payload`, `key`, `headers`) 자동 해석 지원.
    - 무손실 체크포인트 영속화: 원자적 스왑 파일(`outbox_checkpoint.json`, Golden Rules 1 & 5) 기반으로 마지막 처리 LSN(`last_ack_lsn`)을 기록하여 재기동 시 유실 없이 자동 재개.
    - 지수 백오프 재시도: 브로커 일시 장애 시 최대 재시도 횟수(`MaxRetries`) 및 점진적 백오프 루프 관통.
    - 플러그형 브로커 싱크(`pkg/cdc/outbox_sinks.go`): `MemorySink`(테스트/임베디드), `WebhookSink`(HTTP POST), `KafkaRESTSink`(Confluent/Strimzi/Upstash), `RabbitMQHTTPSink`(Management REST API).
    - 운영 관측성 및 CLI 연동: `GET /api/cdc/outbox/status` 실시간 모니터링 엔드포인트 완비 및 CLI 플래그(`-outbox`, `-outbox-table`, `-outbox-webhook`, `-outbox-kafka`, `-outbox-rabbitmq`), JSON 설정 바인딩.
    - 단위 및 E2E 테스트 완비 (`pkg/cdc/outbox_test.go`, `pkg/api/outbox_api_test.go`).
- [x] **PostgreSQL 서버 사이드 커서 및 Extended Query 포털 배치 스트리밍 (`pkg/engine`, `pkg/wire`)**
  - **배경**: 대규모 데이터셋 조회 시 클라이언트 메모리 고갈 방지 및 PostgreSQL 표준 SQL 커서(`DECLARE ... CURSOR`, `FETCH`, `MOVE`, `CLOSE`) 및 드라이버 포털 배치(`Execute` with `maxRows`, `PortalSuspended`) 완비.
  - **구현 내용**:
    - `pkg/engine/sqlexec_cursor.go`: `DECLARE <name> [SCROLL] [BINARY] CURSOR [{WITH|WITHOUT} HOLD] FOR <query>` 스냅샷 커서 등록기 구현.
    - 커서 제어 명령 구현: `FETCH` 및 `MOVE` 지원 (`NEXT`, `PRIOR`, `FIRST`, `LAST`, `ALL`, `FORWARD`, `BACKWARD`, `ABSOLUTE <n>`, `RELATIVE <n>`, 정수 카운트).
    - 커서 해제: 단일 커서 `CLOSE <name>` 및 전체 커서 일괄 정리 `CLOSE ALL`.
    - 트랜잭션 수명주기 불변성: 일반 커서는 `COMMIT` 시 파기, `WITH HOLD` 커서는 `COMMIT` 후에도 유지, `ROLLBACK` 시 모든 커서 즉각 파기 (PostgreSQL 공식 표준).
    - Extended Query 프로토콜 연동 (`pkg/wire/server.go`): `Execute ('E')` 메시지의 `maxRows`를 파싱하여 배치 단위(`maxRows`)로 데이터 튜플을 스트리밍하고 미완료 시 `MsgPortalSuspended ('s')` 송출, 완료 시 `MsgCommandComplete ('C')` 송출.
    - 단위 및 통합 테스트 완비: `pkg/engine/sqlexec_cursor_test.go` 및 `pkg/wire/server_test.go`.
- [x] **관용적 No-Op `USE <database>` 및 세션 가상 DB 컨텍스트 동기화 (`pkg/engine`, `pkg/wire/tds`, `pkg/proc/runtime`)**
  - **배경**: 인스턴스 단위 격리(Instance-Based Isolation) 아키텍처 불변성을 엄격히 유지하면서도, 레거시 SQL Server 덤프 스크립트, 마이그레이션 도구, DBeaver/SSMS GUI 클라이언트와의 100% 호환성 확보.
  - **구현 내용**:
    - `pkg/engine/sqlexec.go`: `USE <database>` (`USE master`, `USE [SalesDB]`, `USE "Analytics_2026"`) 정규식 인터셉트를 통한 무오류 즉시 수용 및 `Command: "USE"` 응답.
    - 세션 격리형 가상 DB 추적: `ClientSession.Database` 및 `ClientSession.Vars["current_database"]` / `["db_name"]` 동기화 (기본 세션 `sqlion`, TDS 세션 `master`).
    - 스칼라 함수 일원화: `DB_NAME()`, `CURRENT_DATABASE()`, `DB_ID()`, `ORIGINAL_DB_NAME()` 호출 시 세션 활성 데이터베이스를 완벽 동기화 반환 (테이블 미참조 스칼라 SELECT 및 VDBE `OpCallBuiltin` 바인딩).
    - TDS 와이어 프로토콜 연동: `pkg/wire/tds/server.go`에서 `USE` 명령 실행 시 공식 MS-TDS `TokenEnvChange (0xE3)` (`EnvChangeDatabase`) 토큰 스트림을 클라이언트로 즉시 송출.
    - 회귀 테스트 완비: `pkg/engine/coordinator_test.go` (`TestTolerantUseDatabase`), `pkg/wire/tds/server_test.go` (`TestTDS_TolerantUseDatabase`).
- [x] **Always On 스타일 자동 장애 조치(Auto-Failover) 및 리더 선출 (`pkg/replication`, `cmd/server`, `pkg/config`, `pkg/ui`)**
  - **배경**: 엔터프라이즈 99.999% 무중단 운영을 위한 Primary 장애 자동 감지, 최저 지연 LSN 레플리카 무손실 승격 및 스플릿 브레인 방지.
  - **구현 내용**:
    - `pkg/replication/failover.go`: 단조 증가 리더 텀(`term int64`) 및 Always On 최신 LSN 우선 규칙(`req.LastLSN >= localLSN`) 기반 리더 선출 코디네이터 구현.
    - 스플릿 브레인 방지(펜싱): 기존 Primary가 상위 텀 발표(`POST /api/replication/leader`)나 투표 요청 감지 시 즉시 읽기 전용 레플리카로 자동 강등(`role = RoleReplica`, 쓰기 트랜잭션 차단).
    - Primary 하트비트 타임아웃 자동 감지(`ReplicationClient.LastHeartbeatTime()`) 및 정족수(Quorum) 기반 백그라운드 선출 루프 완비.
    - CLI 플래그 및 JSON 설정 연동: `-auto-failover`, `-peers`, `-self-url`, `-heartbeat-timeout-ms`, `-election-timeout-ms`.
    - 임베디드 웹 스튜디오(`pkg/ui/dashboard.html`) 실시간 리더 텀 및 고가용성 상태 시각화 연동.
    - 단위 및 E2E 통합 테스트 완비 (`pkg/replication/failover_test.go`): LSN 우선권 검증, 스플릿 브레인 방지, 정족수 투표, Primary 강제 다운 시 자동 승격 및 신규 Primary 쓰기 검증.
- [x] **선언적 스키마 마이그레이션 CLI (`lionsql migrate`) 및 `_migrations` 이력 추적 (`cmd/lionsql`, `pkg/migrate`, `cmd/server`)**
  - **배경**: PocketBase 스타일의 개발자 생산성 극대화 및 독립형 데이터베이스 스키마 버전 관리 완비.
  - **구현 내용**:
    - `_migrations` 내부 테이블 자동 생성 및 마이그레이션 메타데이터(`version`, `name`, `applied_at`, `duration_ms`) 이력 추적.
    - `pkg/migrate` 엔진 신설: `Create`, `Load`, `Up`, `Down`, `Status` 라이프사이클 및 단조 증가 타임스탬프 충돌 방지 완비.
    - `cmd/lionsql` 통합 CLI 바이너리 구축 (`bin/lionsql`): `lionsql migrate new <name>`, `lionsql migrate up/down/status`, 임베디드 직접 쿼리 실행기 `lionsql sql "<query>"`.
    - `cmd/server/main.go` 양방향 패리티 완비 (`./bin/server migrate ...`).
    - `pkg/migrate/migrate_test.go` 및 `cmd/lionsql/main_test.go` 단위/통합 테스트 완비.
- [x] **내장형 Google 및 GitHub OAuth2 소셜 로그인 & 자동 사용자 프로비저닝 (`pkg/api`, `pkg/auth`, `pkg/config`, `pkg/ui`)**
  - **배경**: 서드파티 OAuth 라이브러리 없는 순수 Go 표준 라이브러리 기반 턴키 소셜 인증 및 웹 스튜디오 연동.
  - **구현 내용**:
    - `pkg/api/oauth.go`: Google 및 GitHub 인가 코드 교환, GitHub 비공개 이메일(`/user/emails`) 자동 해석 및 Mock 서버 주입 인터페이스.
    - `pkg/auth`: `ProvisionOAuthUser` 신설을 통한 신규 Superuser 자동 프로비저닝, 계정 연동 및 서명된 HS256 JWT 즉시 발급.
    - REST 엔드포인트 완비: `GET /api/auth/oauth2/providers`, `GET /api/auth/oauth2/url`, `GET/POST /api/auth/oauth2/callback`, `GET/PUT /api/settings/oauth2` (시크릿 마스킹 `••••••••`).
    - 임베디드 웹 스튜디오(`pkg/ui/dashboard.html`) 로그인 모달 소셜 로그인 버튼 및 팝업 콜백(`postMessage`) 연동.
    - `pkg/api/oauth_test.go` 완전 격리형 Mock HTTP 서버 기반 단위/통합 테스트 완비.
- [x] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`C-7`) (`pkg/planner`)**
  - **배경**: 범주형 상태 컬럼이나 특정 핫스팟 값에 데이터가 집중되는 왜곡(Skew) 데이터 환경에서 균등 분포 가정(1/NDV)으로 인해 발생하는 선택도 최대 10~100배 오차 제거.
  - **구현 내용**:
    - `MCVItem` 모델 및 컬럼 통계 내 `MCVList` 추출 엔진 구현 (`pkg/planner/stats.go`).
    - `BuildColumnStats`에서 상위 최빈값(Top-20, 발생 빈도 >= 1% 및 평균 빈도 1.2배 초과) 자동 탐지 및 MCV 목록 분리.
    - 동치(`=`), 부등치(`!=`), `IN`, `BETWEEN`, 범위(`<`, `<=`, `>`, `>=`) 평가 시 MCV 일치 여부를 1차 검사하고, 비-MCV 롱테일에 대해서만 히스토그램 보간을 적용하는 이원화 모델 완비.
    - `cbo_stats.json` 영속화 및 역직렬화 무결성 보장.
    - 범주형/수치형 왜곡 데이터, 균등 데이터, 단일값 데이터 및 JSON 라운드트립 단위 테스트 완비 (`pkg/planner/stats_test.go`).
- [x] **다중 컬럼 확장 통계 및 함수적 종속성 모델링 (`C-5`) (`pkg/planner`, `pkg/catalog`, `pkg/engine`)**
  - **배경**: 속성 값 독립성(AVI: Attribute Value Independence) 가정으로 인해 상관관계가 높은 다중 컬럼(예: 국가-도시) 조건 평가 시 카디널리티가 최대 10~100배 과소평가(Underestimation)되는 왜곡 해소.
  - **구현 내용**:
    - `MultiColumnStats` 모델 및 다중 컬럼 결합 NDV(DistinctCount) 수집 엔진 구현 (`pkg/planner/stats.go`).
    - 단일 컬럼 NDV 대비 결합 NDV 비율을 기반으로 함수적 종속성 계수($dep \in [0.0, 1.0]$) 자동 산출 (`ComputeFunctionalDependency`).
    - `EstimateCompoundSelectivity` 및 `EstimateExprSelectivity`에서 상관관계 컬럼 감지 시 $dep \cdot \min(P_i) + (1-dep) \cdot \prod P_i$ 결합 선택도 산출 적용.
    - T-SQL DDL 문법 지원: `CREATE STATISTICS stat_name ON table_name (col1, col2, ...)` 및 `DROP STATISTICS` 파싱 및 카탈로그 영속화 연동.
    - `ANALYZE TABLE` 실행 시 복합 보조 인덱스 및 등록된 확장 통계에 대한 다중 컬럼 결합 통계 자동 수집 완비.
- [x] **$N > 4$ 체인 조인 동적 계획법 (DP / Selinger) 조인 순서 최적화 (`B-4`) (`pkg/planner`)**
  - **배경**: 5개 이상 다중 테이블 조인 시 기존 $O(N!)$ 전수 순열 탐색으로 인한 지수적 계산 폭증 방지 및 서브밀리초 최적 조인 트리 탐색.
  - **구현 내용**:
    - 부분집합 비트마스크(`1 << n`) 기반 Selinger 동적 계획법 `optimizeJoinOrderDP` 및 $O(1)$ 플랫 슬라이스 메모이제이션 구현.
    - 조인 동등 조건 그래프 연결 검증을 통한 데카르트 곱(Cartesian Product) 우선 가지치기(Pruning) 및 비연결 쿼리용 2차 폴백 패스 완비.
    - DP 상태 공간 $\le 12$ 테이블 바운딩 가드(Golden Rules 21 & 23)로 복잡한 엔터프라이즈 ERP 다중 조인에서도 쿼리 플래닝 지연 서브밀리초 보장.
    - 6개 및 8개 테이블 체인 조인에 대한 서브밀리초 플래닝 단위 테스트 완비 (`pkg/planner/cbo_join_test.go`).
- [x] **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`P-3`) (`pkg/wire`, `pkg/wire/tds`)**
  - **배경**: 쿼리 결과 스트리밍 시 행마다 `[]any` 및 `[][]byte` 슬라이스를 생성하는 힙 할당 오버헤드와 복사 제거.
  - **구현 내용**:
    - TDS 프로토콜 `WriteNBCRowDirect` 신설: `TokenNBCRow` (0xD2) 및 NULL 비트맵을 네트워크 직렬화 버퍼에 직접 기록.
    - `executeRPCAndRespond` 및 `executeQueryAndRespond`에서 행 반복문 외부 `scratchVals []any` 1회 할당 재사용.
    - PGWire 프로토콜 `writeExecutionResult` 및 `runAndWriteQueryWithFormats`에서 행 반복문 외부 `scratchRowBytes [][]byte` 1회 할당 재사용.
- [x] **T-SQL 핵심 시스템 내장 함수 및 IDENTITY 추적 완비 (`pkg/proc`, `pkg/engine`)**
  - **배경**: SQL Server 애플리케이션 및 ORM(Dapper, EF Core) 호환성을 위한 핵심 시스템 스칼라 함수 및 ID 생성 추적 기능 제공.
  - **구현 내용**:
    - `ISNUMERIC`, `CHARINDEX`, `PATINDEX`, `STUFF`, `IDENT_CURRENT`, `COLUMNPROPERTY`(IsIdentity, AllowsNull, Precision 등), `SCOPE_IDENTITY` 함수 구현.
    - `Coordinator` 테이블별 마지막 발번 ID 추적(`SetTableLastIdentity`/`GetTableLastIdentity`).
    - 세션 변수 치환 시 단일 `@` 사용자 변수가 시스템 `@@IDENTITY`와 충돌하는 토큰 결함 수정.
- [x] **TDS PLP (Partially Length-Prefixed) LOB 청크 스트리밍 (`VARCHAR(MAX)`, `VARBINARY(MAX)`) (`pkg/wire/tds`)**
  - **배경**: TDS 프로토콜에서 8,000 바이트를 초과하는 대용량 LOB 데이터 전송 시 단일 패킷 길이를 초과하는 페이로드 전송 표준 규격 준수.
  - **구현 내용**:
    - [MS-TDS] 2.2.5.2 규격 기반 PLP(Partially Length-Prefixed) 청크 인코딩 엔진 구현 (`writePLPValue`).
    - `ColMetaData`에서 비제한 LOB 타입(`VARCHAR(MAX)`, `NVARCHAR(MAX)`, `VARBINARY(MAX)`)에 대해 `0xFFFF` (65535) 길이 지시자 지정.
    - 8바이트 전체 길이 헤더, 최대 8,000바이트 단위 청크 분할 전송, 4바이트 제로 종료 청크(`0x00000000`) 스트리밍.
    - PLP 8바이트 `0xFFFFFFFFFFFFFFFF` NULL 표현식 완비.
    - Go 공식 `sqlserver` 드라이버(12,000바이트) 및 Python `pymssql` 드라이버(10,000바이트) E2E 검증 완료.
- [x] **외부 언어 드라이버 E2E 통합 검증 하네스 (`pkg/wire`, `pkg/wire/tds`)**
  - **배경**: Go 공식 드라이버(`go-mssqldb`, `lib/pq`, `pgx/v5`)에 이어 엔터프라이즈 멀티 언어 클라이언트 검증.
  - **구현 내용**:
    - `pkg/wire/driver_external_test.go`: Python `psycopg3` (PGWire :5433) 연결, DDL/DML, NULL 값 처리, 명시적 트랜잭션(COMMIT/ROLLBACK) E2E 검증.
    - `pkg/wire/tds/driver_external_test.go`: Python `pymssql` (TDS :1433 / FreeTDS dblib) 연결, DDL/DML, DATE/TIME/GUID 바이너리 파싱, 트랜잭션 원자성 E2E 검증.
    - FreeTDS/pymssql 클라이언트 호환성: 일반 DDL 실행 시 unsolicited INFO 토큰 예외 방지 가드(`server.go` PRINT 전용 제한) 적용.
    - `uv run` 기반 격리형 실행 환경 및 미설치 환경 자동 스킵(`exec.LookPath`) 연동으로 100% 무결점 밀폐형 테스트(Golden Rule 17) 달성.
- [x] **볼케이노 벡터화 청크 반복자 (Chunked Batch Next: 64~1024건) (`P-4`) (`pkg/execution`, `pkg/engine`)**
  - **배경**: 행 단위 `Next()` 가상 함수 호출 및 `ctx.Done()` 검사 오버헤드로 인한 CPU 캐시 미스 해소.
  - **구현 내용**:
    - `pkg/execution/chunk.go`: `Chunk` 구조체(1,024건 프리알로케이션), `ChunkedIterator` 인터페이스, `PullChunk` 자동 폴백 엔진 신설.
    - `SeqScan`, `IndexScan`, `Filter`, `Projection`, `Limit`, `Aggregate`, `Sort`, `HashJoin`, `IndexNestedLoopJoin`, `sqlexec_select` 전반에 배치 파이프라인 관통.
    - 기존 단건 `Next()` 호출에 대해 내부 청크 버퍼링을 통한 100% 투명한 하위 호환성 보장.
- [x] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 커버링 인덱스 (`P-2`) (`pkg/storage`, `pkg/execution`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - **배경**: 풀 스캔 및 인덱스 스캔 시 불필요한 모든 컬럼의 Varint/문자열 디코딩 힙 할당을 방지하고, 메인 테이블 조회를 생략.
  - **구현 내용**:
    - `pkg/storage/tuple.go`: 제로 할당 `SkipTupleColumn`, $O(1)$ 오프셋 캐싱 `LazyTuple`, 선별 디코딩 `DecodeTupleSelected` 구현.
    - `CREATE INDEX ... INCLUDE(...)` 문법 파싱, 카탈로그 및 `SkipList` / `IndexData` 비키 페이로드 적재 연동.
    - CBO 플래너 `isCovering` 및 `NodeIndexOnlyScan` 연동으로 테이블 힙 조회 100% 생략.
- [x] **CBO 옵티마이저 힌트 오버라이드 제어 (`D-9`) (`pkg/planner`, `pkg/proc`)**
  - 테이블 힌트(`WITH (INDEX(...))`) 및 쿼리 힌트(`OPTION (HASH/MERGE/LOOP JOIN, FORCE ORDER)`) 파싱 및 CBO 강제 오버라이드.
- [x] **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델 (`B-3`) (`pkg/execution`, `pkg/planner`)**
  - 정렬 입력에 대한 $O(M+N)$ 해시리스 스트리밍 반복자 `MergeJoinIterator` 신설 및 CBO 비용 경쟁 모델 연동.
- [x] **단순 PK 조회 고속 지문 해싱 & 바이패스 캐시 (`P-5`) (`pkg/engine`, `pkg/storage`)**
  - FNV-1a 64비트 지문 해싱 기반 Fast-Path 템플릿 탐지, 정규 파서/CBO/VDBE 스택 우회 및 스토리지(`tx.Get`) 직결.
- [x] **TDS 프로토콜 고도화: NBCROW 행 압축 (`0xD2`) (`pkg/wire/tds`)**
  - TDS 7.3+ 표준 `BuildNBCRow` 신설, NULL 비트맵 패킹 및 0바이트 전송을 통한 네트워크 오버헤드 30%+ 절감.
- [x] **SQL Server 2008 / TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET 및 UNIQUEIDENTIFIER 바이너리 직렬화 (`pkg/wire/tds`)**
  - `DATE`(0x28, 0001-01-01 기준 일수 3바이트 LE), `TIME`(0x29, Scale 0..7 100ns 틱 3~5바이트), `DATETIME2`(0x2A, Scale 0..7 틱 + 그레고리력 3바이트), `DATETIMEOFFSET`(0x2B, UTC DATETIME2 + 2바이트 분 오프셋), `UNIQUEIDENTIFIER`(0x24, Windows Mixed-Endian 바이트 스왑) 전 항목 완비.
  - 결과셋 컬럼 타입 자동 추론(`inferColumnInfos`) 고도화 및 공식 `github.com/microsoft/go-mssqldb`(`mssql.UniqueIdentifier`, `time.Time`) 통합 검증 완료.

---

## 2. 도메인별 대기 백로그 (Domain-Specific Backlogs)

### 2.1. CBO & 쿼리 최적화 (CBO & Query Engine)

| 번호 | 과제명 | 상태 | 설명 |
| :---: | :--- | :---: | :--- |
| **A-1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | **완료 (`[x]`)** | 컬럼별 정렬 및 히스토그램 수집을 `sync.WaitGroup` 병렬화 완료 |
| **A-2** | **CBO 통계 메타데이터 디스크 영속화** | **완료 (`[x]`)** | `cbo_stats.json` 원자적 파일 쓰기 및 기동 시 자동 복원 연동 완료 |
| **C-6** | **OR 조건 및 불리언 트리 선택도 계산** | **완료 (`[x]`)** | AST 재귀 평가, 배타합/독립합집합, NOT, IN, BETWEEN 정밀 산출 완료 |
| **D-9** | **CBO 옵티마이저 힌트 오버라이드 제어** | **완료 (`[x]`)** | T-SQL 조인 힌트(`HASH/LOOP/MERGE`) 및 인덱스 힌트(`WITH (INDEX(...))`) 강제 반영 |
| **B-3** | **소트-머지 조인 (Sort-Merge Join) 연산자** | **완료 (`[x]`)** | 정렬된 입력에 대해 $O(M+N)$ 해시리스 스트리밍 조인(`MergeJoinIterator`) |
| **B-4** | **$N > 4$ 체인 조인 동적 계획법 (DP)** | **완료 (`[x]`)** | 5개 이상 테이블 조인 시 Left-Deep DP 부분집합 비트마스크 동적 탐색 완료 |
| **C-5** | **다중 컬럼 확장 통계 (Extended Stats)** | **완료 (`[x]`)** | 컬럼 상관관계로 인한 카디널리티 과소평가 방지 및 결합 선택도 산출 완료 |
| **C-7** | **MCV (Most Common Values) 최빈값 목록 결합** | **완료 (`[x]`)** | 왜곡 데이터 최빈값 분리 보관 및 비-MCV 히스토그램 이원화 완비 |
| **D-8** | **백그라운드 자동 통계 갱신 데몬** | **완료 (`[x]`)** | DML 변동량(Dirty Tuples) 기반 자동 `ANALYZE` 트리거 |

- [x] **동적 계획법(DP / Selinger) 기반 대규모 조인 순서 최적화 ($N > 4$) (`B-4`) (`pkg/planner`)**
  - 4개 이하의 체인 조인 전수 순열 탐색을 넘어, $N > 4$ 테이블 조인에 대한 부분집합 비트마스크(`1 << n`) 기반 Selinger DP 및 연결성 기반 가지치기(Pruning) 완비.
- [x] **다중 컬럼 확장 통계 (Extended / Multi-Column Statistics) (`C-5`) (`pkg/planner`, `pkg/catalog`, `pkg/engine`)**
  - 컬럼 간 상관관계(예: 국가-도시)로 인한 복합 조건 카디널리티 과소평가(Underestimation)를 방지하는 다중 컬럼 NDV 수집, 함수적 종속성 모델링($dep$), CREATE/DROP STATISTICS DDL 및 결합 선택도 산출 완비.
- [x] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`C-7`) (`pkg/planner`)**
  - 왜곡(Skew)이 심한 상위 N개 최빈값(Top-20)은 MCV 목록으로 보관하고 나머지 균등 분포는 히스토그램으로 이원화 처리하여 선택도 오차 최소화.
- [x] **백그라운드 자동 통계 갱신 데몬 (Auto-Analyze Daemon) (`D-8`) (`pkg/engine`, `pkg/catalog`, `pkg/storage`)**
  - 테이블 DML 변경 비율(동적 임계치: `min_threshold + base_rows * scale_factor`)을 추적하여 백그라운드 비동기로 `coordinator.AnalyzeTable` 자동 트리거.
  - 트랜잭션 커밋 핫패스에 락 경합 없이 원자적 카운팅(`AddWALHook`) 및 디바운싱 완비.
- [x] **반정형 JSON 표현식 B-Tree 인덱스 및 내장 함수 완비 (`ADR-022`) (`pkg/proc`, `pkg/storage`, `pkg/planner`, `pkg/execution`, `pkg/engine`)**
  - [x] **[1단계] JSON 내장 함수 및 파서 연산자 구현 (완료) (`pkg/proc`, `pkg/engine`, `pkg/execution`)**:
    - PostgreSQL 이항 연산자 `->`(JSON 하위 객체/배열 반환) 및 `->>`(스칼라 텍스트 추출) Pratt 파서 등록.
    - T-SQL & ANSI 표준 내장 함수: `ISJSON(val)`, `JSON_VALUE(col, '$.path')`, `JSON_QUERY(col, '$.path')`, `JSON_MODIFY(col, '$.path', new_val)`.
    - RFC 6901 / 경량 JSONPath(키 접근 및 `$.key.nested[0]`) 고속 파서 및 프로젝션/WHERE 필터 평가 연동.
    - `Tuple.ToEvalMap()` 지연 튜플(LazyTuple) 역직렬화 누락 버그 해결 및 스칼라 문자열 리터럴(`isSingleQuotedLiteral`) 식별 정밀화.
    - In-Memory OCC 및 WiredTiger 양대 스토리지 백엔드 상에서 단일/체인 추출, 다중 테이블 조인 및 필터 100% 통합 검증 완료 (`pkg/engine/json_query_test.go`, `pkg/engine/json_wt_test.go`).
  - [x] **[2단계] 기능 표현식 인덱스 DDL 및 스토리지 키 추출 (완료) (`pkg/proc`, `pkg/storage`, `pkg/engine`)**:
    - `CREATE [UNIQUE] INDEX idx ON tbl ((data->>'key'))` 및 `CREATE INDEX idx ON tbl ((JSON_VALUE(data, '$.path')))` 이중 괄호 기능 인덱스 DDL 파싱 및 AST 정규화 (`ExtractJSONExprInfo`, `ParseCreateIndex`).
    - `storage.IndexSchema`에 `Expression`, `ExprColumn`, `ExprPath` 및 `IsExpression()`, `TableSchema.FindExpressionIndex` 완비.
    - 스토리지 튜플 삽입/갱신/삭제 시 `ExtractJSONIndexKey`를 통해 실시간 SkipList 및 WAL 동기화 (`extractIndexKeyFromRowWithSchema`).
    - ANSI SQL 표준 준수: UNIQUE 표현식 인덱스에서 NULL 다중 허용(`!key.IsNull`), 중복 비-NULL 키 삽입 시 즉각 `ErrUniqueViolation` 롤백.
    - In-Memory OCC 및 WiredTiger 양대 스토리지 백엔드 상에서 DDL 생성, 직접 `IndexScan` 범위 스캔, UPDATE/DELETE 실시간 동기화 100% 통합 검증 완료 (`TestJSON_ExpressionIndex`, `TestJSON_ExpressionIndex_WiredTiger`).
  - [x] **[3단계] CBO 표현식 인덱스 매칭 및 E2E/성능 벤치마크 (완료) (`pkg/planner`, `pkg/execution`, `pkg/engine`)**:
    - CBO 옵티마이저 매칭 (`pkg/planner/planner.go`): `WHERE data->>'city' = 'Seoul'` 또는 `WHERE JSON_VALUE(data, '$.city') = 'Seoul'` 평가 시 순차 스캔 대신 `Index Scan using <idx_name>` 자동 선택.
    - 실행기 바인딩 (`pkg/execution/builder.go`): `parseIndexCond`에서 기능 표현식 조건 분석 및 인덱스 스캔 바운드(`lowVal`, `highVal`) 자동 추출.
    - 단일 테이블 고속 경로 (`pkg/engine/sqlexec_eval.go`): `findIndexAndBounds`에서 표현식 인덱스 즉각 감지 및 복합 투영 식별 시 Volcano 위임.
    - WiredTiger 인덱스 스캔 필터링 커서 (`pkg/storage/wiredtiger/wt_engine.go`): `wtFilteredIndexCursor`로 JSON 및 컬럼 키 범위 정확 필터링 완비.
    - 성능 벤치마크 검증: 1,000건 JSON 레코드 기준 SeqScan(2.17ms) 대비 IndexScan(334µs)으로 **6.5배~7.3배 성능 가속 달성** (`TestJSON_ExpressionIndex_CBO_And_Explain`, `TestJSON_ExpressionIndex_Benchmark`).



### 2.2. 스토리지 & 엔진 성능 (Storage & Engine Performance)

| 단계 | 전략 분야 | 핵심 구현 과제 | 상태 | 예상 효과 및 ROI |
| :---: | :--- | :--- | :---: | :--- |
| **P-1** | **스토리지/트랜잭션** | **배치형 그룹 커밋 & 무경합 Fast-Path** | **완료 (`[x]`)** | `FsyncAlways` 100배 지연 단축(0.038ms/op), TPC-C 2.8K TPS |
| **P-2** | **스토리지/디코딩** | **바이너리 튜플 지연 역직렬화 & 커버링 인덱스** | **완료 (`[x]`)** | 스캔 속도 2배 향상, 불필요한 Varint 디코딩 힙 할당 제거 |
| **P-3** | **와이어 프로토콜** | **제로카피 와이어 인코딩 & I/O 버퍼 결합** | **완료 (`[x]`)** | 중간 `[]any` 레코드 박싱 제거, TCP syscall 횟수 대폭 감소 |
| **P-4** | **실행 엔진** | **벡터화 청크 반복자 (Chunked Batch Next)** | **완료 (`[x]`)** | 볼케이노 가상 디스패치 및 컨텍스트 취소 오버헤드 99% 삭감, 1024 튜플 배치 스트리밍 |
| **P-5** | **캐시 및 메모리** | **고속 SQL 지문 해싱 & 단순 PK 조회 바이패스** | **완료 (`[x]`)** | 단순 PK 조회 파서 바이패스, TPS 50%+ 향상, GC 부하 90% 경감 |
| **P-6** | **파서 & 엔진** | **SQL 파서 & 엔진 핫패스 정규식 사전 컴파일 리팩토링** | **대기 (`[ ]`)** | 쿼리당 동적 `regexp.MustCompile` 힙 할당 및 5~15µs 지연 완전 제거 |
| **P-7** | **JSON & 내장함수** | **반정형 JSON 동일 튜플 DOM 캐시 & 제로-할당 키 스캐너** | **대기 (`[ ]`)** | 다중 JSON 필드 추출 쿼리 CPU/GC 부하 50~70% 경감 |
| **P-8** | **스토리지/CGo** | **WiredTiger 커서 풀링 & CGo 왕복 비용 절감 (`cur->reset`)** | **대기 (`[ ]`)** | CGo 경유 WiredTiger 단건 조회/인덱스 스캔 TPS 30%+ 향상 |
| **P-9** | **옵티마이저/캐시** | **정형 SQL Parameterized 쿼리 실행 계획 캐시 (SQL Plan Cache)** | **대기 (`[ ]`)** | 반복 실행 OLTP 쿼리의 파서/CBO 플래너 오버헤드 100% 바이패스 |
| **P-10** | **실행기/병렬화** | **Volcano 대규모 분석 쿼리 멀티코어 병렬 실행기 (`ParallelSeqScan`)** | **대기 (`[ ]`)** | 대용량 OLAP 분석 쿼리 멀티코어 선형 가속 (4~8배) |

- [ ] **SQL 파서 & 엔진 핫패스 정규식 사전 컴파일 및 바이트 토큰 스캐너 전환 (`P-6`) (`pkg/engine`)**
  - **배경**: `sqlexec.go`, `sqlexec_dml.go`, `sqlexec_select.go`, `sqlexec_eval.go`, `sqlexec_explain.go`의 핫패스 함수 내부에 위치한 동적 `regexp.MustCompile` 호출로 인한 매 쿼리당 NFA/DFA 재컴파일 및 힙 할당 오버헤드(5~15µs) 제거.
  - **구현 계획**:
    - 모든 동적 정규식을 패키지 레벨 전역 변수(`var selectRe = regexp.MustCompile(...)`)로 승격 및 사전 컴파일.
    - 고빈도 커맨드 분기(`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `KILL`, `USE`)에 대해 정규식 대신 슬라이스 프리픽스 및 바이트 토큰 스캐너 적용.
    - 쿼리당 힙 할당 0건 및 디스패치 지연 서브마이크로초화.
- [ ] **반정형 JSON 동일 튜플 DOM 캐시 & 제로-할당 경량 키 스캐너 (`P-7`) (`pkg/storage`, `pkg/engine`)**
  - **배경**: `ExtractJSON`, `ExtractJSONText`, `JSONValueByPath` 호출 시 매번 Go 표준 라이브러리의 `json.Unmarshal`을 호출하여 `map[string]any`를 힙에 생성하는 오버헤드 및 단일 튜플 내 다중 JSON 필드 참조 시 중복 파싱 제거.
  - **구현 계획**:
    - 동일 튜플 평가 컨텍스트 내 파싱된 JSON DOM 캐싱: 한 행에서 `doc->>'name'`, `doc->>'city'` 등 여러 필드를 추출할 때 1회만 언마샬링.
    - 최상위 키 추출 전용 제로-할당 경량 스트리밍 바이트 스캐너(`ScanJSONTopLevelKey`): 전체 트리 구축 없이 바이트 오프셋 슬라이싱으로 즉시 추출.
    - JSON 대량 스캔/필터 쿼리 시 GC 부하 70% 경감.
- [ ] **WiredTiger 커서 풀링 & CGo 왕복 비용 절감 (`P-8`) (`pkg/storage/wiredtiger`)**
  - **배경**: `tx.Get`, `tx.Scan`, `tx.IndexScan` 호출 시마다 CGo를 통해 `go_wt_open_cursor` 및 `cur->close`를 반복 수행하여 발생하는 CGo 전환 오버헤드 및 WiredTiger 내부 커서 메모리 재할당 병목 제거.
  - **구현 계획**:
    - 세션/트랜잭션 레벨 커서 캐시 도입: 테이블/인덱스 URI별로 열린 커서를 보관하고, 쿼리 종료 시 닫는 대신 `cur->reset()`으로 초기화 후 재사용.
    - WiredTiger 단건 포인트 룩업(`tx.Get`) 및 인덱스 스캔 처리량 30%~50% 향상.
- [ ] **정형 SQL Parameterized 쿼리 실행 계획 캐시 (`P-9`) (`pkg/engine`, `pkg/planner`)**
  - **배경**: VDBE 프로시저 외에 일반 PGWire/TDS를 통해 유입되는 반복 실행 Parameterized 쿼리(`SELECT ... WHERE id = $1`)의 매회 정규식 파싱, AST 구축, CBO 비용 계산 및 Volcano 실행 트리 재생성 비용 제거.
  - **구현 계획**:
    - FNV-1a 64비트 쿼리 템플릿 해시 기반 Bounded LRU `SQLPlanCache` 구축.
    - 캐시 히트 시 파서와 CBO 최적화 과정을 완전히 바이패스하고, 캐싱된 물리 실행 노드(`PlanNode`)에 런타임 파라미터만 리바인딩하여 실행.
    - 고빈도 OLTP 쿼리 TPS 대폭 향상.
- [ ] **Volcano 대규모 분석 쿼리 멀티코어 병렬 실행기 (`P-10`) (`pkg/execution`)**
  - **배경**: 현재 1개 쿼리가 단일 고루틴(1개 CPU 코어)에서만 실행되는 구조를 탈피하여, 100만 건 이상의 대규모 테이블 풀 스캔 및 집계 시 멀티코어 하드웨어 자원을 100% 활용.
  - **구현 계획**:
    - `ParallelSeqScanIterator`: 테이블 파티션 또는 레코드 키 범위를 N개 작업 단위로 분할하여 N개 워커 고루틴이 동시 스캔.
    - 병렬 집계 취합기(`ParallelAggregateIterator`): 각 워커가 로컬 집계(`SUM`, `COUNT`)를 수행한 뒤 마스터가 최종 병합.
    - 8코어/16코어 환경에서 대용량 집계 쿼리 4~8배 선형 가속.
- [x] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 투영 푸시다운 (`pkg/storage`, `pkg/execution`)**
  - 풀 스캔 시 WHERE 절 조건 컬럼만 선별 디코딩하고, 조건을 통과한 행에 한해서만 나머지 투영 컬럼을 언패킹하여 CPU 및 힙 할당 70% 삭감.
- [x] **커버링 인덱스 비키 컬럼 문법 지원: `CREATE INDEX ... INCLUDE (col...)` (`pkg/storage`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - B+Tree/SkipList 리프 노드에 비키 컬럼을 페이로드로 적재하여 메인 테이블 힙 조회를 100% 생략하는 Index-Only Scan 극대화.
- [x] **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`P-3`) (`pkg/wire`, `pkg/wire/tds`)**
  - TDS 프로토콜 `WriteNBCRowDirect` 신설: `TokenNBCRow` (0xD2) 및 NULL 비트맵을 네트워크 직렬화 버퍼에 직접 기록하여 중간 `[]any` 레코드 박싱 및 버퍼 복사 100% 제거.
  - TDS 및 PGWire 프로토콜 결과 스트리밍 핫패스에서 행 반복문 외부 `scratchVals` / `scratchRowBytes` 1회 할당 재사용.
- [x] **Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator: 64~1024건) (`pkg/execution`, `pkg/engine`)**
  - 행 단위 `Next()` 가상 함수 호출 및 `ctx.Done()` 검사 오버헤드를 줄이기 위해 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍(`ChunkedIterator`, `NextChunk`).
  - `SeqScan`, `IndexScan`, `Filter`, `Projection`, `Limit`, `Aggregate`, `Sort`, `HashJoin`, `IndexNestedLoopJoin`, `sqlexec_select` 전반에 배치 파이프라인 관통 및 기존 단건 `Next()` 투명 버퍼링 100% 하위 호환성 보장.
- [x] **세션/쿼리 전용 아레나 메모리 할당기 (Arena Allocator) (`pkg/execution`, `pkg/storage`)**
  - 해시 조인, 소트 버퍼, 스캔, 어그리게이션에 대해 쿼리 수명주기에 묶인 아레나 할당기를 적용하여 Go GC 압박 원천 제거 (`GetArena` / `PutArena`, `AllocAnySlice`, `AllocValues`, `AllocString`, `AllocBytes`).
  - 8바이트 정렬 슬랩 풀링(`sync.Pool`)과 단일 포인터 리셋($O(1)$)으로 슬랩 재활용 및 제로 GC 실행 경로 구현.

### 2.3. 와이어 프로토콜 & 생태계 (Wire Protocols & Ecosystem)
- [x] **T-SQL 특수 타입 직렬화 (TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET, UNIQUEIDENTIFIER) (`pkg/wire/tds`)**
  - `UNIQUEIDENTIFIER` (UUID 16바이트 Windows GUID 바이트 스왑), `DATE` (`0x28`, 3바이트 LE), `TIME` (`0x29`, 100ns 틱 3~5바이트), `DATETIME2` (`0x2A`, 틱 + 3바이트 날짜), `DATETIMEOFFSET` (`0x2B`, UTC DATETIME2 + 2바이트 분 오프셋) TDS 바이너리 인코딩 완비.
  - 결과셋 컬럼 타입 자동 추론(`inferColumnInfos`) 고도화 및 `go-mssqldb` 공식 드라이버 통합 검증 완료.
- [x] **PLP LOB 청크 스트리밍 (`pkg/wire/tds`)**
  - [MS-TDS] 2.2.5.2 Partially Length-Prefixed (PLP) 스트리밍 규격 구현 (`writePLPValue`), 8바이트 전체 길이 헤더, 최대 8,000바이트 단위 청크 분할 및 4바이트 제로 종료 청크(`0x00000000`) 전송, 8바이트 `0xFFFFFFFFFFFFFFFF` NULL 표현 완비.
- [x] **외부 언어 드라이버 E2E 통합 검증 하네스 (`pkg/wire`, `pkg/wire/tds`)**
  - Python `psycopg3` (PostgreSQL Wire Protocol :5433) 및 `pymssql` (TDS Protocol :1433) 종단간 통합 검증 완료.
  - `CREATE TABLE`, `INSERT`, `SELECT`, DATE/TIME/GUID 바이너리 파싱, 트랜잭션 `COMMIT`/`ROLLBACK` 원자성 및 NULL 값 처리 완비.
- [x] **SSMS (SQL Server Management Studio) Object Explorer 가상 카탈로그/DMV 뷰 (`pkg/engine`, `pkg/catalog`)**
  - SSMS 접속 및 탐색기 트리 조회에 필수적인 `sys.configurations`, `sys.dm_exec_sessions`, `sys.dm_exec_connections`, `sys.database_principals`, `sys.server_principals`, `sys.dm_os_sys_info`, `sys.endpoints`, `sys.database_files`, `sys.master_files`, `sys.all_objects`, `sys.all_columns`, `sys.all_views` 가상 뷰 완비.
- [x] **PostgreSQL 서버 사이드 커서 및 Extended Query 포털 배치 스트리밍 (`pkg/engine`, `pkg/wire`)**
  - 대규모 쿼리 결과 집합을 분할 수신하는 드라이버 호환성을 위한 `DECLARE ... CURSOR [WITH HOLD] FOR`, `FETCH`, `MOVE`, `CLOSE [ALL]` 및 트랜잭션 경계 수명주기 완비.
  - Extended Query 프로토콜 `Execute`의 `maxRows` 페이로드 분할 스트리밍 및 `MsgPortalSuspended ('s')` 토큰 송출 지원.
- [x] **임베디드 웹 스튜디오 HA 복제 모니터링 카드 (`pkg/ui`)**
  - 브라우저 웹 스튜디오(`:8090/_/`)에 고가용성(HA) 복제 상태(Primary vs Replica) 시각화 카드 추가.
  - 연결된 Replica 목록, LSN 랙(Lag) 지연 시간, 스트리밍 상태 표시 및 수동 승격(`Promote`) 버튼 제공.

### 2.4. 엔터프라이즈 보안 & 거버넌스 (Enterprise Security & Governance)
- [ ] **투명 데이터 암호화 (TDE: Transparent Data Encryption) (`pkg/storage`)**
  - 디스크에 기록되는 WiredTiger 데이터 파일, WAL/AOF 저널 및 백업 아카이브를 AES-256-GCM으로 투명 암호화(Data at Rest Encryption).
  - 마스터 암호화 키(MEK) 로테이션 및 환경 변수/KMS 키 주입 인터페이스.
- [ ] **불변 엔터프라이즈 감사 로그 (Enterprise Immutable Audit Log) (`pkg/logger`, `pkg/api`)**
  - 로그인 성공/실패, 권한 변경(`GRANT/REVOKE`), DDL 스키마 변경, 관리자 데이터 조작 이벤트를 태깅하여 위변조가 불가능한 불변 감사 파일로 안전 기록.
- [ ] **`INSTEAD OF` 트리거 런타임 인터셉터 (`pkg/proc/runtime`, `pkg/api`)**
  - 뷰(View) 및 복합 테이블에 대한 `INSERT/UPDATE/DELETE` 작업을 가로채서 사용자 정의 프로시저/배치로 치환하는 런타임 DML 인터셉터 완비.

### 2.5. 고가용성 & 분석 확장 (HA & Analytical Extensions)
- [x] **Always On 스타일 자동 장애 조치 및 리더 선출 (Automatic Failover & Leader Election) (`pkg/replication`)**
  - Primary 노드 다운 시 복제 랙(LSN Lag)이 가장 적은 최신 Replica 노드를 새로운 Primary로 자동 승격 (Leader Promotion).
  - 클라이언트 라우팅 갱신 및 Split-Brain 방지를 위한 리더 텀(Term) 검증.
- [x] **분리형 실시간 CDC (Change Data Capture) 스트리밍 프레임워크 (`pkg/cdc`, `pkg/api`, `cmd/server`)**
  - 트랜잭션 커밋과 100% 원자적으로 연동되는 Row-level 변경 델타(Inserted/Updated/Deleted) 이벤트 모델(`ChangeEvent`) 및 스트리머(`Streamer`) 구축.
  - 유계 링 버퍼(Bounded Ring Buffer, 10,000건) 기반 과거 LSN 재생, 비차단 팬아웃(Non-blocking Backpressure, Golden Rule 29), 테이블 필터링 지원.
  - `GET /api/cdc/stream` 실시간 NDJSON 및 SSE 스트리밍 HTTP 엔드포인트 완비.
  - 스토리지 엔진(`memengine`, `wiredtiger`) 커밋 훅 다중화(`AddWALHook`) 관통.
  - Python 표준 라이브러리 기반 DuckDB 마이크로 배치 수신 워커 레시피 작성 (`doc/CDC_DUCKDB_INTEGRATION.md`).
- [x] **트랜잭셔널 아웃박스 외부 브로커 디스패처 (Kafka, RabbitMQ, Webhook) (`pkg/cdc`, `pkg/config`, `pkg/api`, `cmd/server`)**
  - CDC 스트림을 외부 분산 메시지 브로커로 무손실 발행하는 Outbox 릴레이 워커, 지수 백오프 재시도 및 아웃박스 체크포인트 원자적 영속화 완비.
- [ ] **[보류 / Postponed] Pure Go 영속 스토리지 엔진 옵션 (Pebble / bbolt) (`pkg/storage`)**
  - CGO/WiredTiger 의존성 없이 완전한 정적 바이너리(`CGO_ENABLED=0`, Golden Rule 36) 크로스 컴파일 배포를 위한 `storage.Engine` 플러그인 백엔드 (현재는 네이티브 WiredTiger CGO 및 인메모리 OCC 고성능 엔진에 집중하며 보류).
- [x] **내장형 Google 및 GitHub OAuth2 소셜 로그인 & 자동 사용자 프로비저닝 (`pkg/api`, `pkg/auth`, `pkg/config`, `pkg/ui`)**
  - Google 및 GitHub 공식 OAuth2 엔드포인트 연동 (`GET /api/auth/oauth2/providers`, `GET /api/auth/oauth2/url`, `GET/POST /api/auth/oauth2/callback`).
  - 외부 ID 및 이메일 기반 자동 사용자 프로비저닝(`ProvisionOAuthUser`), 계정 연동 및 JWT 토큰 즉시 발급.
  - 관리자 전용 OAuth 설정 조회/수정 API (`GET/PUT /api/settings/oauth2`) 및 클라이언트 마스킹(`••••••••`).
  - 브라우저 웹 스튜디오(`:8090/_/`) 로그인 다이얼로그 내 Google / GitHub 로그인 버튼 및 팝업 콜백(`postMessage`) 연동.
  - 외부 네트워크 의존성 없는 완전 격리형 Mock HTTP 서버 기반 단위/통합 테스트 완비 (`pkg/api/oauth_test.go`).
- [x] **선언적 스키마 마이그레이션 CLI (`lionsql migrate`) (`cmd/lionsql`, `pkg/migrate`, `cmd/server`)**
  - 타임스탬프 기반 마이그레이션 파일 페어(`YYYYMMDDHHMMSS_<name>.up.sql` / `.down.sql`) 생성 (`lionsql migrate new <name>`).
  - 데이터베이스 스키마 순차 배포 및 실행 지연 시간 추적 (`lionsql migrate up [-steps=N]`).
  - `.down.sql` 기반 역순 롤백 및 `_migrations` 이력 갱신 (`lionsql migrate down [-steps=N]`).
  - 배포 상태 정렬 표 형식 출력 (`lionsql migrate status`).
  - 임베디드 데이터베이스 직접 쿼리 실행기 (`lionsql sql "<query>"`).
  - `cmd/server` 및 `cmd/lionsql` 양방향 CLI 문법 패리티 완비.

### 2.6. 코드베이스 구조 개선 & 아키텍처 리팩토링 (Architecture & Refactoring)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 목표 및 리팩토링 상세 |
| :---: | :--- | :--- | :---: | :--- |
| **R-1** | **엔진/카탈로그** | **가상 시스템 카탈로그 모듈 분리 및 가상 뷰 레지스트리화** | **완료 (`[x]`)** | 3.2K줄 `sqlexec_pgcatalog.go` 도메인 분해(PG/MSSQL/ANSI) 및 $O(1)$ 라우팅 |
| **R-2** | **파서/내장함수** | **Pratt 파서 및 내장 함수 도메인별 관심사 분리** | **대기 (`[ ]`)** | `parser.go`(3.2K줄), `builtins.go`(2.8K줄) 도메인 모듈화 |
| **R-3** | **엔진/실행기** | **단일 테이블 패스트패스 vs Volcano 인덱스 바운드 로직 일원화** | **대기 (`[ ]`)** | `sqlexec_eval.go`와 `execution/builder.go` 간 중복 제거 (DRY) |
| **R-4** | **스토리지/OCC** | **In-Memory OCC 트랜잭션 라이프사이클 모듈 분해** | **대기 (`[ ]`)** | `memengine/tx.go`(1.9K줄)의 `Commit()`, 스캔, 세이브포인트 분리 |
| **R-5** | **스토리지/CGo** | **WiredTiger CGo 바인딩 격리 및 트랜잭션/커서 모듈화** | **대기 (`[ ]`)** | `wt_engine.go`(1.7K줄)의 CGo 인터페이스, 커서, DDL 물리적 분리 |

- [x] **가상 시스템 카탈로그 모듈 분리 및 가상 뷰 레지스트리화 (`R-1`) (`pkg/engine`)**
  - **배경**: 단일 파일(3,284라인)에 PostgreSQL `pg_catalog`, ANSI `information_schema`, MSSQL `sys.tables`/DMV, DBeaver/Beekeeper/DbGate/HeidiSQL 도구별 쿼리 인터셉터가 혼재되어 유지보수성과 가독성이 저하됨.
  - **구현 내용**:
    - **도메인별 물리적 파일 분리**:
      - `sqlexec_pgcatalog.go` (634라인으로 80% 이상 슬림화): 순수 PostgreSQL `pg_catalog.pg_*` 가상 테이블 및 전역 레지스트리 디스패처.
      - `sqlexec_mssql_sys.go` (554라인): SQL Server / TDS 전용 가상 카탈로그 및 DMV (`sys.tables`, `sys.columns`, `sys.dm_*`, `sys.database_files` 등) 및 데이터타입 포매터.
      - `sqlexec_info_schema.go` (420라인): ANSI 표준 정보 스키마 (`information_schema.tables`, `columns`, `table_constraints` 등) 및 스키마 카탈로그 매핑.
      - `sqlexec_gui_interop.go` (477라인): HeidiSQL, Beekeeper Studio, DbGate, DBeaver 멀티 테이블 크로스 조인 쿼리 인터셉터.
    - **가상 뷰 레지스트리 패턴 확립 (`VirtualCatalogView`)**:
      - `VirtualCatalogView` 함수 시그니처 및 동시성 안전 읽기 전용 `catalogViewRegistry map[string]VirtualCatalogView` 도입.
      - 기존 60줄의 `isPgCatalogTable` 분기문을 $O(1)$ 맵 조회로 교체.
      - 기존 1,500줄의 `execPgCatalogSelect` 거대 switch-case를 15줄의 $O(1)$ 레지스트리 디스패처로 전면 교체(개방-폐쇄 원칙 OCP 달성).
    - 전체 25개 패키지 race-free 테스트(100% PASS) 및 GUI 클라이언트 호환성 보장.
- [ ] **Pratt 파서 및 내장 함수 도메인별 관심사 분리 (`R-2`) (`pkg/proc`)**
  - **배경**: `pkg/proc/parser.go`(3,241라인)와 `pkg/proc/builtins.go`(2,880라인)가 단일 파일에 비대하게 집중되어 신규 문법/함수 추가 시 인지 부하 및 머지 충돌 위험 증가.
  - **구현 계획**:
    - 파서 분리: `parser_expr.go`(Pratt 연산자 우선순위 식 파싱), `parser_stmt.go`(T-SQL 제어문 `IF/WHILE/TRY`), `parser_ddl.go`(`CREATE/ALTER/DROP`), `parser_dml.go`(`SELECT/DML`).
    - 내장 함수 분리: `builtins_string.go`, `builtins_date.go`, `builtins_math.go`, `builtins_json.go`, `builtins_sys.go`.
- [ ] **단일 테이블 패스트패스 vs Volcano 인덱스 바운드 및 조건 평가 로직 일원화 (`R-3`) (`pkg/engine`, `pkg/execution`, `pkg/planner`)**
  - **배경**: `sqlexec_eval.go`의 단일 테이블 고속 검색과 `execution/builder.go`의 Volcano 플랜 빌더 간에 인덱스 범위 검색 경계(`lowVal`, `highVal`) 추출 및 조건식 파싱 로직이 중복 존재(DRY 위반).
  - **구현 계획**:
    - `pkg/planner/bounds.go` 또는 `pkg/engine/bounds.go` 공통 헬퍼로 인덱스 바운드 추출 로직을 단일화.
    - 새로운 인덱스 타입(표현식 인덱스, 다중 컬럼 인덱스 등) 추가 시 양쪽 엔진이 100% 동일한 경계 분석 엔진을 공유하도록 보장.
- [ ] **In-Memory OCC 스토리지 트랜잭션 라이프사이클 모듈 분해 (`R-4`) (`pkg/storage/memengine`)**
  - **배경**: `pkg/storage/memengine/tx.go`(1,965라인)에 트랜잭션 라이프사이클 전체와 500줄 이상의 인라인 `Commit()` 함수가 집중되어 동시성 로직의 가독성 저하.
  - **구현 계획**:
    - `tx_commit.go`: 샤드 정렬 락 획득, OCC 버전 충돌 검증(`validateConflicts`), 유니크 제약 검증(`validateUniqueConstraints`), 인덱스 갱신(`applyIndexMutations`), WAL/AOF 디스패치(`flushWAL`) 단계별 함수 분해.
    - `tx_scan.go`: `Scan()`, `IndexScan()`, 샤드 범위 반복자 분리.
    - `tx_savepoint.go`: 세이브포인트 생성 및 롤백 로직 분리.
- [ ] **WiredTiger 스토리지 CGo 바인딩 격리 및 파일 분해 (`R-5`) (`pkg/storage/wiredtiger`)**
  - **배경**: `pkg/storage/wiredtiger/wt_engine.go`(1,753라인)에 저수준 C/CGo 인터페이스, 세션/트랜잭션 관리, 커서 래핑, DDL이 한 파일에 혼재.
  - **구현 계획**:
    - `wt_cgo.go`: 순수 CGo 인터페이스 및 C 래퍼 함수 격리.
    - `wt_cursor.go`: `wtCursor`, `wtFilteredIndexCursor` 커서 순회 및 바운드 검사 로직 전담.
    - `wt_tx.go`: `WTTx` 트랜잭션 커밋/롤백/스캔 라이프사이클 전담.
    - `wt_engine.go`: 스토리지 엔진 수명주기, 세션 풀링 및 DDL 전담.

---

## 3. 최근 완료된 핵심 마일스톤 아카이브 (Recently Completed Milestones)

- [x] **[아키텍처] VDBE Instruction 구조체 16바이트 패킹 (`pkg/proc/bytecode.go`)**
  - `Op(1B) + Flags(1B) + Aux(2B) + A(4B) + B(4B) + C(4B) = Exactly 16 Bytes` 고정 크기 레코드 압축.
  - 64B L1 Cache Line당 정확히 4개 명령어 적재(`unsafe.Sizeof == 16`), VM 디스패치 레이턴시 단축.
- [x] **[아키텍처] SQL 실행 코디네이터 패키지 분리 (`pkg/api` ➔ `pkg/engine`)**
  - 13개 `sqlexec_*.go` 파일(14,000+ 라인)을 `pkg/engine`으로 완전 이관.
  - PGWire 및 TDS 리스너가 `pkg/api` 의존성 없이 `engine.SQLCoordinator` 단방향 직결 호출 확립.
- [x] **[스토리지] 스토리지 파이프라인 배치형 그룹 커밋 (Group Commit & Fsync 가속)**
  - `memengine` WAL 및 `wiredtiger` 양쪽 엔진 모두에 리더/팔로워 그룹 커밋 및 무경합 패스트패스 구현.
  - `FsyncAlways` 마이크로벤치마크 커밋 지연 100배 단축(0.038ms/op), TPC-C VDBE 14,800+ TPS (422,000+ tpmC) 달성.
- [x] **[SQL 문법] T-SQL & ANSI SQL 7대 문법 및 실행 파이프라인 전면 완비 (`S-1` ~ `S-7`)**
  - **S-1**: `OFFSET ... FETCH NEXT ... ROWS ONLY` 및 `TOP (n)` 정규 페이징 완비.
  - **S-2**: 복합 한정자(`[db].[schema].[tbl].[col]`) 및 인라인 별칭(`FROM accounts basetbl`) 파싱 정밀화.
  - **S-3**: FROM 절 파생 테이블(`FROM (SELECT ...) AS t`) 및 인라인 서브쿼리 AST 노드화, 격리 구체화(`__dt_*`).
  - **S-4**: `CAST`, `TRY_CAST`, `CONVERT`, `TRY_CONVERT`, `::` 및 `COALESCE`/`ISNULL` 단락 평가 완비.
  - **S-5**: `CROSS APPLY`, `OUTER APPLY` 및 `FULL OUTER JOIN`, `RIGHT OUTER JOIN` 볼케이노 해시조인 완비.
  - **S-6**: 세션 제어문(`SET NOCOUNT ON`, `SET ANSI_NULLS ON` 등) 정식 AST 파싱 및 세션 플래그 바인딩.
  - **S-7**: `WHERE [NOT] EXISTS` 상관 서브쿼리 Semi/Anti-Join CBO 변환 및 비상관 조기 평가.
- [x] **[CBO] CBO 불리언 트리 재귀 평가 & OR 조건 정밀 선택도 산출 (`C-6`)**
  - `TableStats.EstimateExprSelectivity()` 신설, 동일 컬럼 배타 합($P(A)+P(B)$), 독립 합집합, NOT, IN, BETWEEN 정밀 산출 완비.
- [x] **[보안] 세분화된 SQL 객체 수준 RBAC (Role-Based Access Control) (`pkg/auth`, `pkg/catalog`, `pkg/api`)**
  - `CREATE LOGIN/USER`, `SETUSER`, `GRANT/REVOKE/DENY` (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `EXECUTE`, `CONTROL`, `REFERENCES`) 및 사전 카탈로그 인터셉터(`checkTableAccess`) 완비.
- [x] **[안정성] PlanCache Bounded LRU 캐시 및 메모리 상한 방어 (Golden Rule 29) (`pkg/proc/runtime`)**
  - 무제한 맵을 `container/list` 기반 상한 용량(`DefaultPlanCacheCapacity = 1024`) LRU 캐시로 전면 개편하여 OOM 원천 방지.
- [x] **[T-SQL 내장함수] T-SQL 공식 내장 스칼라 함수 전수 완비 (A~F 그룹)**
  - 에러(`ERROR_MESSAGE`), 문자열(`PATINDEX`, `STUFF`, `CONCAT_WS`), 수학/삼각(`LOG`, `EXP`, `RAND`), 메타데이터(`OBJECT_ID`, `DB_NAME`), 암호화(`HASHBYTES`), 날짜(`DATEFROMPARTS`) 등 완비.
- [x] **[드라이버] 공식 클라이언트 드라이버 E2E 통합 검증**
  - Go 공식 드라이버 `go-mssqldb` (TDS), `lib/pq` (PGWire), `pgx/v5` (Extended Query Pipelining) 100% PASS 검증 완료.
- [x] **[성능] 단순 PK 조회 고속 지문 해싱 & 스토리지 바이패스 캐시 (`P-5`) (`pkg/engine`, `pkg/storage`)**
  - 쿼리 문자열 FNV-1a 64비트 지문 해싱 기반 Fast-Path 템플릿 탐지 및 바운디드 캐시(`PKLookupPlan`).
  - 정규 파서/CBO/VDBE 스택을 전면 우회하여 스토리지 엔진(`tx.Get`)으로 직결 반환, DDL 발생 시 자동 무효화(`InvalidatePKBypassCache`).
  - Prometheus 메트릭스 `sqlion_pk_bypass_hits_total` 연동 및 멀티 테이블 한정자/별칭 완벽 지원.
- [x] **[스토리지/디코딩] 바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 커버링 인덱스 INCLUDE (`P-2`) (`pkg/storage`, `pkg/execution`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - 제로 할당 `SkipTupleColumn`, $O(1)$ 오프셋 캐싱 `LazyTuple`, 선별 디코딩 `DecodeTupleSelected` 구현.
  - `CREATE INDEX ... INCLUDE(...)` 문법 파싱, SkipList/IndexData 비키 페이로드 적재 및 CBO `isCovering` Index-Only Scan 극대화.
- [x] **[실행 엔진] 볼케이노 벡터화 청크 반복자 및 1,024건 배치 스트리밍 (`P-4`) (`pkg/execution`, `pkg/engine`)**
  - `ChunkedIterator` 및 `NextChunk` 인터페이스 신설, 1,024건 프리알로케이션 청크 기반 볼케이노 배치 파이프라인 관통.
  - SeqScan, IndexScan, Filter, Project, Limit, Aggregate, Sort, HashJoin, IndexNestedLoopJoin, sqlexec_select 전반 배치 가속 및 100% 하위 호환성 보장.
- [x] **[와이어 프로토콜] TDS NBCROW 행 압축 (`0xD2`) (`pkg/wire/tds`)**
  - TDS 7.3+ 표준 `BuildNBCRow` 신설, NULL 비트맵 패킹 및 0바이트 전송으로 네트워크 대역폭 30%+ 절감.
- [x] **[CBO/조인] 소트-머지 조인 (Sort-Merge Join) 연산자 및 옵티마이저 힌트 (`B-3`, `D-9`) (`pkg/execution`, `pkg/planner`, `pkg/proc`)**
  - $O(M+N)$ 해시리스 스트리밍 반복자 `MergeJoinIterator` 신설 및 `OPTION (HASH/MERGE/LOOP JOIN)`, `WITH (INDEX(...))` 강제 오버라이드.
- [x] **[생태계/GUI] 3대 메이저 GUI 클라이언트(DBeaver, DbGate, Beekeeper Studio) 완벽 호환 (`pkg/engine`, `pkg/wire`, `pkg/wire/tds`)**
  - TDS 및 PGWire 양대 프로토콜 상에서 테이블 브라우징, 인덱스/외래키/프로시저 인트로스펙션, 스토리지 크기 산출 및 메타데이터 조회 전면 완비.
  - DBeaver SQL Server 무한 쿼리 루프(`totalSize`, `usedSize` 컬럼 및 단일 `object_id` 필터링) 해결 및 PostgreSQL 멀티 네임스페이스(`public`, `dbo`) `pg_class` 인트로스펙션 지원.
  - Beekeeper Studio 및 DbGate 테이블 한정 와일드카드 프로젝션(`alias.*`, `schema.table.*`)과 WiredTiger EOF 래핑 방지 래치 적용 완료.
