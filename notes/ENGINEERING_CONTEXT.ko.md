# LionSQL Engineering Context & Architectural Invariants (`notes/ENGINEERING_CONTEXT.ko.md`)

> - **최초 작성일**: 2026-10-03
> - **최종 갱신일**: 2026-11-04


이 문서는 LionSQL의 설계 원칙, 확립된 핵심 불변식(Invariants), 의도적으로 배제(Spec-Out)한 대안들의 이유, 그리고 사용자가 요구하는 엄격한 엔지니어링 및 협업 규약을 설명합니다.

---

## 1. 핵심 아키텍처 불변식 (Core Architectural Invariants)

새로운 환경의 AI나 개발자가 **가장 자주 범하는 실수는 다른 대형 DBMS(MySQL, Oracle, CockroachDB 등)의 복잡한 메커니즘을 섣불리 LionSQL에 도입하려 하는 것**입니다. 아래 8가지 불변식은 심도 깊은 검토 끝에 확립된 핵심 기둥입니다.

### ① 데이터베이스 격리 전략: 인스턴스 단위 물리 격리 (Process-per-Database)
* **결정 사항**: 데이터베이스 단위 물리 프로세스/컨테이너 격리.
* **배제(Spec-Out)된 것**: 단일 프로세스 내 진짜 다중 데이터베이스 카탈로그 동시 마운트 및 런타임 카탈로그 멀티플렉싱.
* **관용적 가상 No-Op 허용 정책**:
  * 단, MSSQL 덤프 스크립트, 마이그레이션 파일, DBeaver/SSMS/sqlcmd 등 외부 도구와의 100% 호환성을 위해 `USE <dbname>` (`USE master`, `USE [SalesDB]`) 구문은 **관용적 가상 No-Op(Tolerant Virtual No-Op)**으로 즉시 수용하고 TDS 환경 변경 토큰(`0xE3 ENVCHANGE`)과 세션 가상 DB 컨텍스트(`ClientSession.Database`, `DB_NAME()`, `CURRENT_DATABASE()`)를 동기화하여 구문 에러를 원천 차단함.
* **이유 & 맥락**:
  * 단일 프로세스 내 물리적 카탈로그 멀티플렉싱은 세션별 DB 컨텍스트 스위칭, 교차 DB 메타데이터 잠금 경합, 한 테넌트의 메모리 누수가 전체를 죽이는 장애 반경(Blast Radius) 문제를 낳습니다.
  * LionSQL은 단일 정적 바이너리로 매우 가볍게 구동되므로, 멀티 테넌시는 포트(`--port`, `--http`)와 데이터 디렉토리(`--data-dir`)를 분리한 독립 프로세스/컨테이너로 배포하는 것이 Radically Simple(KISS)하며 보안 및 자원 격리에 절대적으로 유리합니다.

### ② 트랜잭션 동시성 모델: Non-Blocking 스냅샷 격리 / 커밋 읽기
* **결정 사항**: 독자(Reader)는 필자(Writer)를 막지 않고, 필자도 독자를 막지 않는 비차단 스냅샷 격리.
  * In-Memory OCC: 레코드 버전(`Version uint64`) 기반 낙관적 검증. 충돌 시 `ErrWriteConflict` 즉시 롤백.
  * WiredTiger MVCC: 타임스탬프 및 Hazard Pointer 기반 락프리 B+Tree. 충돌 시 `WT_ROLLBACK`.
* **배제(Spec-Out)된 것**: 2PL(Two-Phase Locking) 공유 읽기 잠금, `READ UNCOMMITTED`, 복잡한 SSI(Serializable Snapshot Isolation) 직렬화 의존성 그래프 추적.
* **이유 & 맥락**: 고빈도 OLTP 환경에서 가짜 어보트(False Abort) 패널티와 분산 락 오버헤드를 배제하고 초당 수만 건의 비차단 읽기/쓰기 처리량을 보장하기 위함.

### ③ 단일 정적 바이너리 자립성 (Zero External Dependencies)
* **결정 사항**: 코어 커널, PGWire(:5433), TDS(:1433), HTTP API(:8090), 임베디드 Web Studio, 고루틴 커넥션 풀러, 크론 스케줄러, SMTP 메일러가 단일 정적 실행 파일(`bin/lionsql`) 내에서 동작.
* **배제(Spec-Out)된 것**: 외부 사이드카(PgBouncer, Redis 캐시 계층, Nginx, 외부 cron 데몬) 및 외부 코디네이터(ZooKeeper, etcd, Consul, Kafka).
* **이유 & 맥락**: LionSQL의 최대 강점은 바이너리 하나 복사해서 `--dir`만 주면 수 초 만에 엔터프라이즈급 DB가 기동되는 간결함입니다. 외부 의존성 추가는 절대 금지됩니다.

### ④ 수직적 확장 우선(Scale-Up Primacy) & 1-Writer Multi-Reader 복제
* **결정 사항**: 단일 노드 하드웨어 포화(Multi-core, NVMe, VDBE 바이트코드 최적화) + 1-Writer Primary와 N-Reader Replica 간 비동기/반동기 WAL 스트리밍 복제.
* **배제(Spec-Out)된 것**: 분산 2PC(Two-Phase Commit) 트랜잭션 샤딩 및 멀티 마스터 클러스터링.
* **이유 & 맥락**: 분산 2PC는 네트워크 RTT로 인해 쓰기 지연시간을 급증시킵니다. LionSQL은 단일 노드에서 16,000+ TPS 이상의 raw 성능을 뽑아내고, 읽기 확장은 LSN 기반 복제본으로 처리하며, 고가용성은 페일오버 승격(`Promote`)으로 해결합니다.

### ⑤ 듀얼 파서 전략 (Dual-Parser Architecture)
* **결정 사항**:
  * **Primary (Native Pratt Parser)**: `pkg/proc/parser.go`, 고빈도 쿼리 및 표준 절차 흐름용 초고속 제로할당 파서.
  * **Secondary Fallback (ANTLR4 TSqlParser)**: `pkg/proc/parser/antlr/`, MSSQL 전용 특화 문법(`[dbo].[Name]`, `WITH RECOMPILE` 등) 자동 폴백.
* **설계 원칙**: 두 파서 간 순환 참조(Circular Dependency)를 방지하기 위해 `proc.RegisterProcedureFallback` SPI로 분리되어 있으며, 두 파서 모두 영구적으로 유지/검증되어야 합니다.

### ⑥ 듀얼 트랙 UDF 전략 (ADR-018)
* **결정 사항**:
  * **Track 1 (Go Native In-Process SPI)**: `proc.RegisterScalarFunction`. Go로 LionSQL을 임베딩하는 개발자를 위한 제로 FFI 오버헤드 직결 호출 (<5ns).
  * **Track 2 (WebAssembly 샌드박스)**: 독립 서버 환경에서 안전하게 외부 함수를 실행하기 위한 `wazero` 메모리 샌드박스.

### ⑦ 인메모리 OCC 스토리지: 백그라운드 Vacuum 스레드 배제 (Spec-Out)
* **결정 사항**: 인메모리 OCC 엔진(`memengine`)은 세션 프라이빗 워크스페이스에서 변경분을 버퍼링한 뒤, 커밋 시점에 버전을 원자적으로 교체하거나 롤백 시 워크스페이스를 버림.
* **배제(Spec-Out)된 것**: PostgreSQL 스타일의 백그라운드 멀티버전 데드 튜플 청소기(Background Vacuum Worker / GC 스레드).
* **이유 & 맥락**:
  * PostgreSQL이나 전통적 디스크 기반 MVCC는 테이블 블록 내에 구버전 튜플 체인을 남겨두기 때문에 주기적 진공 청소(Vacuum)가 필수적이지만, LionSQL의 인메모리 OCC는 데드 튜플 체인을 메모리에 누적하지 않습니다.
  * 주기적 백그라운드 Vacuum 루프는 불필요한 고루틴 생성, 주기적 전체 메모리 스캔, 락 경합 및 CPU 낭비만 초래하는 전형적인 오버엔지니어링(YAGNI, Golden Rule 15)이므로 의도적으로 배제했습니다.

### ⑧ 분석/OLAP 연동: CDC 스트리밍 채택 및 공유 메모리(Arrow IPC) 스냅샷 배제 (Spec-Out)
* **결정 사항**: 트랜잭션 커밋과 원자적으로 결합된 **CDC(Change Data Capture) / WAL 이벤트 스트림**을 단일 표준 파이프라인으로 채택. 외부 Kafka/RabbitMQ 브로커 전송은 물론, 임베디드 DuckDB OLAP 마이크로 배치 적재에도 동일 파이프라인 활용.
* **배제(Spec-Out)된 것**: POSIX 공유 메모리(`/dev/shm`, `mmap`) 기반 Apache Arrow IPC 테이블 스냅샷 (`SNAPSHOT TABLE ... TO ARROW_SHM`).
* **이유 & 맥락**:
  * Arrow IPC 공유 메모리 방식은 쿼리나 주기마다 수 GB에 달하는 테이블 전체를 풀스캔하여 Arrow 포맷으로 직렬화해야 하므로 심각한 OLTP CPU/메모리 부하를 유발하며, 스냅샷 시점의 데이터만 보는 정적 배치 방식에 불과합니다.
  * 또한 거대한 Apache Arrow Go SDK 의존성 추가(KISS 원칙 위배)와 OS별 비이식성(Windows 호환 불가, 도커 컨테이너 `/dev/shm` 64MB 제한) 문제를 발생시킵니다.
  * 반면 CDC 스트리밍은 커밋된 변경분(Delta)만 가볍게 비동기 전달하므로 OLTP 오버헤드가 사실상 0이고, 실시간 반영(ms 단위)이 가능하며, 임베디드 DuckDB Appender와 네이티브하게 결합되므로 공유 메모리 스냅샷은 중복이자 불필요한 기능입니다.

### ⑨ 절차 언어 런타임 단일화 & 중복 인터프리터(PL/pgSQL) 배제 (Spec-Out)
* **결정 사항**: 저장 프로시저 및 함수 실행 엔진은 Native SQLite VDBE 스타일의 고성능 T-SQL 바이트코드 가상머신(`pkg/proc`)으로 완전 단일화.
* **배제(Spec-Out)된 것**: PostgreSQL 전용 절차 언어(PL/pgSQL) 인터프리터 및 다중 프로시저 언어 런타임 탑재.
* **이유 & 맥락**:
  * PGWire는 PostgreSQL 생태계의 표준 드라이버, ORM 및 GUI 클라이언트 도구(DBeaver, Beekeeper Studio 등)와의 완벽한 접속 인터페이스(I/O 게이트웨이)로 기능합니다.
  * 프로시저 비즈니스 로직 런타임까지 중복으로 PL/pgSQL 인터프리터를 구현하여 탑재하는 것은 코어 엔진 비대화, 실행 경로 이중화, 잠재적 보안 버그 및 관리 비용을 2배로 증가시키는 안티패턴(KISS & DRY 위배)이므로 의도적으로 배제합니다.

### ⑩ 모던 JSON 표준화 & 레거시 XML DML/XQuery 엔진 배제 (Spec-Out)
* **결정 사항**: 반정형(Semi-Structured) 데이터 저장 및 추출은 T-SQL 및 ANSI 표준 JSON 함수군(`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`, `FOR JSON PATH / AUTO`) 및 Go 네이티브 고속 JSON 엔진으로 전면 표준화.
* **배제(Spec-Out)된 것**: 2000년대 레거시 Microsoft XML 데이터 타입 및 XML DML(`.modify()`, `.nodes()`, `.value()`, `.exist()`, XQuery/XPath 프로세서).
* **이유 & 맥락**:
  * 과거 엔터프라이즈 환경에서 사용되던 XML 스키마 검증 및 XQuery 엔진은 수만 라인의 파서 복잡성과 메모리 오버헤드를 유발합니다.
  * 모던 웹, 모바일, BaaS(PocketBase/Supabase 스타일) 및 마이크로서비스 생태계는 100% JSON으로 통일되어 있으므로, 무거운 레거시 XML DML 엔진은 의도적으로 배제하고 고성능 JSON 처리에 집중합니다.

### ⑪ 외부 분산 트랜잭션 코디네이터 (XA / MS DTC) 배제 (Spec-Out)
* **결정 사항**: 엄격한 단일 노드 원자적 ACID 트랜잭션 모델링과 선형적 LSN WAL 복제본 승격 토폴로지 채택.
* **배제(Spec-Out)된 것**: 네트워크 분산 트랜잭션 프로토콜인 XA Two-Phase Commit 및 Microsoft Distributed Transaction Coordinator (MS DTC).
* **이유 & 맥락**:
  * 분산 트랜잭션 코디네이터는 네트워크 단절 시 인다웃(In-Doubt) 트랜잭션 잠금 고착, 코디네이터 단일 장애점(SPOF), 네트워크 RTT에 의한 쓰기 지연시간 폭증(16,000+ TPS 붕괴)을 유발합니다.
  * 트랜잭션 경계는 항상 단일 노드 내에서 원자적으로 종결(`sess.BeginTx()` -> `tx.Commit()`)되며, 분산 환경의 정합성은 비동기/반동기 WAL 복제와 멱등적(Idempotent) 이벤트 컨슈밍으로 해결합니다.

### ⑫ DB 커널 내장 OAuth2 소셜 로그인 및 서드파티 SaaS 인증 배제 (Spec-Out)
* **결정 사항**: 데이터베이스 엔진 보안 및 인증은 엔터프라이즈 데이터베이스 표준 규격(PGWire의 SCRAM-SHA-256, TDS의 SQL Server 표준/NTLM 인증, HTTP 관리용 안전한 토큰 기반 RBAC/API-Key)으로 완전 일원화.
* **배제(Spec-Out)된 것**: 외부 컨슈머용 OAuth2 소셜 로그인(구글, 깃허브 등 특정 SaaS의 웹 리다이렉트 핸들러, Client Secret 생명주기 관리, 아웃바운드 인터넷 토큰 교환 HTTP 호출).
* **이유 & 맥락**:
  * 데이터베이스 커널이 외부 상용 SaaS(Google/GitHub)의 웹 엔드포인트와 통신하는 것은 외부 인터넷 의존성을 강제하고 보안 공격 표면(Attack Surface)을 넓히는 안티패턴입니다.
  * DB 엔진 본연의 무결성과 경량성을 지키며, 엔터프라이즈 SSO 연동은 데이터베이스 커널 내부가 아닌 전면 게이트웨이/리버스 프록시(Keycloak, OAuth2-Proxy 등)에 위임하는 것이 표준 아키텍처입니다(Golden Rule #15 KISS 및 단일 책임 원칙).

### ⑬ DB 커널 내장 S3 Blob 파일 스토리지 및 이미지 썸네일 처리 배제 (Spec-Out)
* **결정 사항**: 데이터베이스 프로세스 내부에 S3 호환 오브젝트 스토리지 서버/클라이언트 모듈 및 이미지 디코딩/리사이징(썸네일) 라이브러리를 직접 내장하는 것을 영구 배제(Spec-Out)함.
* **배제 이유 & 맥락**:
  * **KISS & YAGNI 및 OLTP 코어 보호 (Golden Rule 15)**: 대용량 바이너리 청크 스트리밍과 무거운 래스터 이미지 변환 연산은 Go 런타임 힙 메모리 할당과 CPU를 급격히 점유하여 고동시성 OLTP 트랜잭션의 서브밀리초 지연 시간(p99)을 훼손합니다.
### ⑭ 1.0 기능 스코프 및 단일 바이너리 경계 원칙 (Single Binary & Scope Consolidation)
* **결정 사항**:
  1. **단일 바이너리(`lionsql`) 완전 통합**: `bin/server`를 제거하고 단일 실행 파일 `bin/lionsql`이 포그라운드 서버 기동 및 데몬 라이프사이클(`start`, `stop`, `restart`, `status`), 마이그레이션(`migrate`), CLI 도구(`sql`, `doctor`)를 모두 일원화 처리 ([`notes/UNIFIED_CLI_PLAN.md`](notes/UNIFIED_CLI_PLAN.md)).
  2. **내장 5필드 크론 잡 스케줄러 유지**: SQL Server Agent 대체 및 OS 독립적인 배치 실행(`sys.cron_jobs`)을 위해 1.0 코어에 유지하되 유휴 시 무오버헤드 보장.
  3. **내장 SMTP 메일러 유지 (기본 비활성화)**: PocketBase 스타일의 BaaS 완결성을 위해 유지하되, 보안 격리를 위해 기본값은 비활성화(Opt-In).
  4. **Kafka / RabbitMQ Outbox 직접 릴레이 절충안 유지**: 코어는 순수 CDC 스트림(`GET /api/cdc/stream`) PULL 파이프라인을 표준으로 하되, 외부 브로커 푸시는 명시적 `-outbox` 플래그로만 기동되는 옵트인 격리 구조를 유지하여 코어 OLTP의 무결점 안정성을 완벽히 보호함.

### ⑮ 정규식 기반 SQL 파싱 금지 및 괄호·중첩 구조 절대 불가 원칙 (Chomsky Type 3 vs Type 2)
* **결정 사항**:
  * 괄호, 식별자, 절 분기, 수식 트리가 개입되는 모든 SQL 파싱, DDL/DML 컴파일, 쿼리 라우팅에서 정규식(`regexp`) 사용을 전면 배제하고 결정론적 렉서/토크나이저(`proc.Lexer`, `sqlutil.FindMatchingParen`) 및 2-Tier AST 파서로 완전 단일화.
* **배제 이유 & 형식 언어론적 맥락 (Chomsky Hierarchy)**:
  * **수학적 한계**: 정규 표현식(Type 3 정규 언어)은 내부 상태 기억을 위한 스택(Stack) 메모리가 결여된 유한 상태 오토마톤(FSA)입니다. 반면 괄호의 짝 맞춤(Dyck Language), 중첩 서브쿼리(`FROM (SELECT ...)`), 수식 트리, `CASE...WHEN` 등은 스택이 필수적인 문맥 자유 문법(Type 2 CFG)입니다.
  * **중첩 괄호 파괴 및 탐욕적 절단**: `\((.*?)\)` 정규식은 내부 서브쿼리나 복합 조건식의 첫 번째 닫는 괄호 `)`에서 조기 절단되거나, 탐욕적 매칭 시 쿼리 끝까지 과도하게 삼켜버립니다.
  * **문자열/주석 리터럴 오염**: 따옴표 안의 문자열(예: `WHERE note = 'Data (Confidential)'`)이나 주석 내 괄호를 실제 SQL 연산 괄호로 오인하여 쿼리가 파괴됩니다.
  * **소리 없는 데이터 왜곡 (Silent Corruption)**: 구문 오류로 중단되지 않고 정규식이 잘못 자른 문자열이 엔진 하위로 흘러 들어가 잘못된 WHERE 조건이 실행됨으로써 엉뚱한 행이 갱신/삭제되는 치명적 사고를 유발합니다.
* **허용 영역과 절대 금지 영역의 엄격한 경계선**:
  * **사용 허용 (선형/단순 포맷 검증)**: 문맥(Context)이나 중첩 깊이(Depth)가 전혀 없는 단순 선형 패턴 검증에 한하여 사용 허용.
    - 마이그레이션 파일명 포맷 검증 (`^\d{14}_.+\.(up|down)\.sql$`)
    - SQL 내장 함수 자체의 정규식 연산 (`REGEXP_LIKE`, `~`, `!~`)
    - 단순 파라미터 숫자 치환 (`$1`, `$2`) 또는 정적 이메일/UUID 유효성 검사
  * **절대 금지 (괄호·중첩·재귀·SQL 구문 파싱 전반)**:
    - 서브쿼리 및 유도 테이블 (`FROM (SELECT ...)`, `JOIN (SELECT ...)`)
    - 괄호 인자를 취하는 함수 및 내장 프로퍼티 호출 (`SERVERPROPERTY(...)`, `DBCC CHECKTABLE(...)`)
    - 중첩 연산자 및 IN 절 분해 (`WHERE x IN (...)`, `WHERE x = ANY(...)`)
    - 윈도우/OVER 절, CTE 컬럼 리스트, CBO 플래너의 조인/조건식 트리 분해

### ⑮ 쿼리 파서 및 실행 엔진의 실용적 설계 원칙: '미학적 단일화'보다 '초고속 성능과 회귀 무결성' 우선 (`KISS-3`, `KISS-5`의 영구 불변식)
* **핵심 불변식**:
  > **"문제가 있어서 못 만든 것"이 아니라, "현재 시스템의 초고속 성능과 무결성을 지키기 위해 의도적으로 남겨둔 최선의 실용적 설계"이다.**
* **세부 배경 및 트레이드오프**:
  1. **`sqlexec` 하위 정규식 파싱 완전 제거 및 Zero-Regex 불변 원칙 확립 (`GA-REGEX-DDL-1`)**:
     - **경과 및 교훈**: 초기 프로토타입 단계에서는 정규식을 활용한 빠른 가설 검증이 유용했으나, SQL과 같이 중첩 괄호, 대괄호 식별자, 따옴표 내 특수문자, 주석 등이 복합된 비정규(Context-Free) 언어에서 정규식 파싱은 지속적인 런타임 오류와 엣지케이스 오작동을 유발함.
     - **최종 원칙 (Zero-Regex Rule)**: "정규식은 프로토타입 단계에서는 허용될 수 있으나, 라이브(GA)를 앞두고는 전량 걷어내는 것이 맞다." `sqlexec_ddl.go`, `sqlexec_eval.go`, `sqlexec_explain.go`의 35+종 잔여 정규식을 100% 제거하고 `sqlutil` 결정론적 토크나이저 및 `proc.Lexer`로 전환을 완료하였으며, 향후 프로덕션 SQL 실행 경로에 정규식 도입을 영구 금지함.
  2. **3갈래 쿼리 실행 경로 단일화 (`sqlexec_select` ➔ `execution` Volcano Iterator) (`KISS-5` / `REV-ENG-008`)**:
     - **명분 vs 실질**: 모든 SELECT 쿼리를 단 하나의 Volcano 반복자 트리로 통합하는 것이 아키텍처 다이어그램상 깔끔해 보일 수 있음.
     - **OLTP 성능 보호**: 그러나 `SELECT 1`, `SELECT * FROM tbl WHERE id = 1` 같은 고빈도 단건 쿼리는 Volcano 반복자 트리를 조립하고 가상 함수(`Next()`)를 호출하는 비용보다 직결 패스트패스가 **2~3배 빠름**. 이를 무리하게 단일 트리로 강제 통합하면 LionSQL의 핵심 경쟁력인 **TPC-C 20,000+ TPS급의 서브마이크로초 OLTP 처리량을 희생**하게 됨.
     - **원래 결론**: PostgreSQL, MySQL 등 고성능 RDBMS가 공통으로 채택하고 있는 **"고빈도 단건 패스트패스 + 복합 쿼리 Volcano CBO" 이원화 구조**를 유지하는 것이 최선의 실용적 엔지니어링 선택임. 향후 Plan Cache 컴파일이나 JIT 코드 생성이 단건 직결보다 빨라지는 혁신이 전제되지 않는 한 현 체제를 유지함.
     - **1세대 800라인 레거시 삭제 및 2-Tier 영구 확정 (해결 완료)**: 프로토타입 시절 남아있던 ~800라인의 1세대 인메모리 정규식 테이블 스캔 코드를 영구 삭제하고, **"단순 쿼리 VDBE Fast-Path (<1µs) + 복합 쿼리 Volcano CBO 파이프라인 (15~50µs)" 2-Tier 체제**로 최종 확정함.
     - **대안 2 (SQLite 스타일 VDBE 100% 단일화) 영구 배제 원칙**:
       - **"복잡한 쿼리에 대해서는 빠르게 하는 것보다 옳게(Correctly & Safely) 하는 것이 최우선이다."**
       - 다중 조인, 윈도우 함수, 재귀 CTE를 무리하게 바이트코드로 컴파일하려는 시도는 수십만 라인의 컴파일러 비대화(Bloat), 디버깅 난도 폭증, 시스템 취약성만 초래하며, I/O 집약적 쿼리에서 실질적 성능 이득도 없음.
       - 따라서 2-Tier 아키텍처는 완전 통합을 위한 과도기가 아니며, **단순 쿼리의 '속도'와 복합 쿼리의 '정확성/모듈화'를 각각 최우선한 최종 불변 설계**로 영구 유지함 ([`notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md)).

---

### ⑯ 순수 컬럼 지향 벡터화 엔진 전면 도입 배제 (Spec-Out)
* **결정 사항**: DuckDB, ClickHouse와 같은 순수 컬럼 지향 벡터화 엔진(Pure Columnar Engine)으로의 전면 재작성을 영구 배제(Spec-Out)함.
* **배제 이유 & 맥락**:
  1. **Row 저장소의 물리적 스캔 I/O 한계**: LionSQL은 In-Memory OCC 및 WiredTiger 기반의 Row 지향 OLTP 엔진임. 메모리에서 1,024개 행을 컬럼으로 전치(Row-to-Column Pivot)하더라도, 스토리지 스캔 단계에서 모든 컬럼을 읽어야 하므로 컬럼형 DBMS의 핵심인 디스크/메모리 I/O 대역폭 절감 효과가 발생하지 않음.
  2. **KISS 원칙 및 코드 복잡도 급증 방지**: 데이터 타입별 `ColumnVector` 슬라이스, NULL 비트맵, 벡터화 표현식 평가기 등 수만 라인의 중복 계층 추가는 임베디드 OLTP 엔진으로서 과도한 오버엔지니어링(YAGNI)임.
  3. **실용적 대안**: 이미 구축된 1,024개 행 단위 배치 스트리밍(`ChunkedIterator`, `PullChunk`) 파이프라인을 유지하며, 핫패스 `any` 인터페이스 언박싱 제거 및 연산자 해시 테이블 슬라이스 최적화로 복잡 쿼리 목표 지연시간을 달성함 ([`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)).

---

### ⑰ MySQL 독자적 DML 및 레거시 8열 그리드 EXPLAIN 영구 배제 (Spec-Out)
* **결정 사항**: MySQL의 독자적 DML 확장(`INSERT ... ON DUPLICATE KEY UPDATE`), 콤마 기반 역순 페이징 문법(`LIMIT offset, count`), 그리고 구시대적 평면 표 형식인 8열 그리드 `EXPLAIN`의 엔진 도입을 영구 배제(Spec-Out)함.
* **배제 이유 & 맥락**:
  1. **트랜잭션 충돌 검증 로직의 3중 분기 방지 (엔진 복잡도 폭증 차단)**: LionSQL은 이미 PostgreSQL의 `ON CONFLICT DO UPDATE`와 ANSI/MSSQL 표준 `MERGE` 체계를 완전히 갖추고 있음. 여기에 MySQL 독자의 `ON DUPLICATE KEY UPDATE`까지 추가하면 스토리지 엔진의 OCC/MVCC 충돌 검증 및 락 제어 경로가 3개로 분기되어 유지보수 불가능한 블로트웨어(Bloatware)가 됨.
  2. **파서 모호성(Ambiguity) 및 쿼리 충돌 방지**: 표준 ANSI 및 PostgreSQL은 `LIMIT count OFFSET offset` 형식을 취함. MySQL의 콤마 기반 문법은 파서의 모호성과 심각한 문법 충돌 위험을 유발함.
  3. **현대 Volcano CBO 트리 계층 구조 보존 및 호환성 (8열 그리드 EXPLAIN 배제)**:
     - MySQL의 전통적 8열 평면 표는 해시 조인의 Build/Probe 관계, 서브쿼리, CTE, 윈도우 함수의 부모-자식 계층 실행 순서를 납작하게 뭉개버리는 한계가 있음. (MySQL 자신도 8.0부터 8열 표를 버리고 들여쓰기 텍스트 트리 `FORMAT=TREE`를 기본 권장으로 채택함)
     - 또한 임의로 8열 그리드로 변경할 경우 PostgreSQL GUI 도구(DBeaver, pgAdmin 등)의 그래픽 실행 계획 시각화 기능과 충돌함. LionSQL은 현재의 정보량이 풍부한 들여쓰기 텍스트 트리 및 JSON 포맷을 영구 표준으로 유지함.
  4. **MySQL 편의 문법 지원의 최종 경계 (Where to Stop)**:
     - MySQL 문법 지원은 이미 완료된 Phase 1(`DESC`, `SHOW TABLES`, 백틱 `` ` `` 식별자, `IFNULL`/`NOW()` 스칼라 함수)에서 완벽히 종결하며, 비표준 DML 및 레거시 평면 EXPLAIN은 일체 추가하지 않고 코어 단순성을 수호함.

---

### ⑱ 고동시성 핫스팟 제어: 하이브리드 키 래칭(Key-Striped Latch) & 인-커널 투명 재시도 안전망 (Grove 7,100 TPS 실증 완료)
* **결정 사항**: 고동시성 핫스팟 갱신 환경에서 순수 낙관적 동시성 제어(OCC)의 충돌 폭풍을 방지하기 위해 **2,048-스트라이프 인-메모리 키 래치(Key-Striped Latch)**와 **인-커널 투명 재시도 안전망(In-Kernel WriteConflict Retry Loop)**, 그리고 **선형 Writable CTE 바이트코드 Lowering**을 코어 엔진 표준 아키텍처로 채택.
* **배제(Spec-Out)된 것**: 전통적 무거운 2PL(Two-Phase Locking) 전역 행 잠금 큐, 클라이언트 노출형 빠른 실패(Fast-Fail Abort Dump) 및 분산 직렬화 그래프 추적.
* **핵심 아키텍처 및 튜닝 기제**:
  1. **하이브리드 마이크로 래칭 (2,048-Stripe Key Latch)**:
     - 읽기(SELECT)는 **100% Lock-Free** 스냅샷 격리를 유지하여 조회 트래픽의 제로 오버헤드를 보장함.
     - 99%의 서로 다른 행 쓰기는 2,048개 버킷으로 분산되어 **100% 완전 병렬**로 실행됨.
     - 오직 **동일 테이블의 동일 Primary Key를 동시에 수정하는 쓰기 요청들만** 엔진 진입 직전에 인-메모리에서 0.1~0.2ms 동안 짧게 대기(Queueing)하도록 조율하여 MVCC/OCC 충돌을 1차로 원천 차단함.
  2. **MongoDB 표준 아키텍처 기반 인-커널 투명 재시도 안전망**:
     - WiredTiger `WT_ROLLBACK`(-31800) 및 OCC 충돌 발생 시 클라이언트로 즉시 에러를 던지지 않고, VDBE VM 및 Coordinator 내부에서 지수 백오프와 랜덤 마이크로 지터(50µs~250µs)로 새 스냅샷을 획득하여 즉시 재시도함.
     - 이로써 50개 고루틴이 100개 SKU에 초당 수천 번씩 달려드는 극한의 핫스팟 환경에서도 **런타임 에러 0건(에러율 0.00%) 완주**를 달성함.
  3. **선형 Writable CTE ➔ VDBE Bytecode Lowering**:
     - 세션 임시 테이블(`#temp_...`) 스키마 등록/해제와 카탈로그 전역 락을 완전히 제거하고, DML `RETURNING` 컬럼을 VDBE 레지스터 슬롯에 직결 바인딩함.
     - 실제 락 보유 시간(Lock Hold Duration)을 15ms에서 **~60µs(250배 단축)**로 압축하여 래치 회전율을 극대화함.
* **실측 성과 (Apple Silicon, Concurrency: 50, Grove 벤치마크)**:
  - **핫스팟 재고 선점 (100 SKUs)**: PG 16 (2,890 TPS) ➔ **LionSQL TDS SP (7,100 TPS, +145.7% / 2.45배)**
  - **주문 생성 (3-Way 쓰기)**: PG 16 (3,419 TPS) ➔ **LionSQL TDS SP (5,913 TPS, +73.0%)**
  - **복식부기 원장 기표**: PG 16 (3,105 TPS) ➔ **LionSQL TDS SP (5,376 TPS, +73.1%)**
  - **지연 시간 (P50)**: 5ms 대 (Min: 0.20ms) 초저지연 달성.

### ⑲ 쿼리 라우팅 및 세션 방언 분기: 문자열 휴리스틱 완전 배제 및 순수 AST/Enum 불변 원칙 (`ARCH-VDBE-ROUTING-1`)
* **결정 사항**:
  1. **타입 안전한 세션 방언(`session.Dialect`)**: 세션이 어떤 프로토콜(PostgreSQL, T-SQL, MySQL)로 통신하는지를 세션 ID 문자열(`strings.HasPrefix(cs.ID, "pg-")`)로 역추적하던 휴리스틱을 전면 삭제하고, 정식 `Dialect` enum (`DialectTSQL`, `DialectPostgres`, `DialectMySQL`)과 `cs.IsPostgres()`, `cs.IsTSQL()`로 일원화함.
  2. **순수 AST 기반 VDBE 역량 검증 (`proc.QueryCharacteristics`)**: `canCompileToVDBE()` 내에 남아 있던 `sqlutil.FindAllTopLevelKeyword("FROM")` 및 `strings.HasPrefix(rest, "(")` 식의 원시 문자열 슬라이싱 코드를 전면 삭제하고, 토크나이저 AST 분석기의 `HasDerivedTable` 구조체 필드로 O(1) 검사 일원화.
* **배제(Spec-Out)된 것**:
  - 임시방편(Ad-hoc) 문자열 접두사 검사 및 원시 SQL 문자열 인덱스 슬라이싱을 통한 쿼리 능력 판별.
* **이유 & 맥락**:
  - 문자열 기반 꼼수는 주석, 문자열 리터럴(`'FROM ('`), 포트/ID 명명 규칙 변경 시 치명적인 False Positive/Negative를 유발함.
  - 정규 표현식 배제 원칙(`Invariant 7`)과 마찬가지로, **"문맥과 중첩 구조를 모르는 문자열 검사를 프로덕션 SQL 라우팅 경로에서 100% 영구 배제"**하고 순수 AST 및 타입 시스템에 의존해야만 1.0 프로덕션의 안정성을 담보할 수 있음 ([`notes/ARCH_VDBE_ROUTING_1_PLAN.ko.md`](ARCH_VDBE_ROUTING_1_PLAN.ko.md)).

---

### ⑳ 인메모리 OCC 가변 비트/바린트 압축 영구 배제 (Spec-Out)
* **결정 사항**: 인메모리 OCC 스토리지의 레코드 포맷 최적화(`OCC-HYPER-2`) 시, 비트 단위 가변 압축(Varint / Bit-Packing, 옵션 B)을 명시적으로 영구 배제(Spec-Out)하고 무압축 고정 오프셋 기반의 **포인터리스 플랫 레코드(Pointer-Free Flat Record, 옵션 A)**를 단일 아키텍처로 확정함.
* **배제 이유 & 맥락**:
  1. **단순 Zip 압축의 한계**: 옵션 B는 비트 단위 가변 인코딩으로 데이터 크기를 줄이는 단순 "Zip 압축"에 가깝습니다.
  2. **컬럼 읽기 시 CPU 디코딩 오버헤드**: 컬럼을 읽을 때마다 앞선 컬럼들을 순차 디코딩(25~50ns)해야 하므로, 인메모리 엔진의 본질인 0.5ns급 초저지연 $O(1)$ 직접 읽기 성능이 심각하게 훼손됩니다.
  3. **본질적 목적 수호**: 인메모리 DB를 채택하는 본질은 극초저지연과 수십만 TPS입니다. 단순 RAM 몇십 MB를 추가 절감하기 위해 매 쿼리마다 CPU 압축 해제 부하를 감수하는 것은 주객전도이며, Go GC 포인터 0개화와 4~5배 메모리 절감(300~400B)은 옵션 A(고정 오프셋 플랫 레코드)만으로도 완벽히 달성됩니다 ([`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)).

---

## 2. 사용자의 엔지니어링 철학 및 협업 규약

LionSQL 개발 시 아래의 원칙은 **절대적인 우선순위**를 가집니다:

1. **3단계 검증 파이프라인 (Build ➔ Test `-race` ➔ Commit)**:
   - 모든 기능 수정 및 리팩토링 후에는 빌드 컴파일 검증 ➔ 레이스 디텍터를 포함한 전체 테스트 통과 ➔ Conventional Commit 순서를 엄격히 지켜야 합니다.
   - **문서 수정 예외 규약**: 설정이나 런타임에 영향을 주지 않는 순수 마크다운(`.md`) 및 텍스트 문서 수정 시에는 불필요한 빌드/테스트 단계를 생략하고 바로 커밋합니다.
   - 명령어:
     ```bash
     go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
     go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
     go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
     go test -tags wiredtiger -count=1 -race <modified_packages>
     ```
2. **이모지 절대 배제 [RULE:StyleIntegrity]**:
   - 커밋 메시지, 소스코드 주석, 로그 출력, 문서 등에 장식용 이모지(🎉, 🚀, 💡 등)를 일체 사용하지 않으며, 단정한 엔지니어링 톤을 유지합니다.
3. **Radical KISS & Deletion-First [RULE:RadicalKISS]**:
   - 복잡한 추상화나 불필요한 번역 계층을 새로 짜기 전에, **안 쓰는 레거시 코드를 삭제(Pruning)**하고 OS/Go 표준 라이브러리 프리미티브를 활용하는 해결책을 최우선합니다.
   - 복잡한 에뮬레이션이나 새로운 플래그를 도입하기 전에는 반드시 트레이드오프를 사용자에게 먼저 제안하고 합의를 거칩니다.
4. **비파괴적 원자적 파일 I/O [RULE:AtomicSaves] & [RULE:PathSecurity]**:
   - 데이터나 설정 파일을 쓸 때는 직접 덮어쓰지 않고, 반드시 `filepath.Clean` 후 임시 파일(`*.tmp`)에 쓰고 `os.Rename`으로 원자적 커밋을 수행합니다 (권한: `0644`, 디렉토리: `0755`).
5. **모놀리식 파일 리팩토링 기준**:
   - 단순히 기계적으로 라인 수만 보고 자르는 것이 아니라, **도메인 책임(Responsibility)별로 묶어서 분할**합니다.
   - 예: `sqlexec_dml.go`에서 SELECT/PIVOT/EXPLAIN/EVAL을 분리하고 순수 DML만 남김.
6. **문서 명명 및 디렉터리 배치 규약 (Documentation Naming & Directory Conventions)**:
   - **한글 본문 마크다운 확장자 규칙 (`*.ko.md`)**: 이후 새로 생성하거나 작성하는 모든 마크다운 문서 중 본문 내용이 한국어인 문서는 반드시 `*.ko.md` 확장자(예: `notes/*_PLAN.ko.md`, `doc/*_GUIDE.ko.md`)를 사용합니다. 순수 영문으로 작성된 문서에만 `*.md` 확장자를 부여하여 다국어 문서 체계를 명확히 분리합니다.
   - 신규 기능 구현, 아키텍처 통합, 리팩토링을 위한 작업 계획/설계 제안 문서는 개발자/AI 핸드오버용 컨텍스트로 분류하여 **반드시 `notes/*_PLAN.md` (한글 본문인 경우 `notes/*_PLAN.ko.md`)**에 배치합니다 (예: `notes/UNIFIED_CLI_PLAN.md`, `notes/IMMUTABLE_AUDIT_LOG_PLAN.md`, `notes/S3_PRESIGNED_STORAGE_PLAN.md`, `notes/COLLATION_AND_LOCALE_PLAN.md`, `notes/EXTENDED_SMOKE_TEST_PLAN.md`).
   - 공식 기술 매뉴얼, 아키텍처 명세서, 가이드 및 호환성 검증 결과 보고서는 **`doc/`** 아래에 배치하며, 검증 문서는 **`doc/*_REPORT.md`** (한글 문서는 `*.ko.md`) 형식을 유지합니다 (예: `doc/TDS_COMPATIBILITY_REPORT.md`, `doc/PGWIRE_COMPATIBILITY_REPORT.md`, `doc/TEST_VERIFICATION_REPORT.ko.md`).
7. **문서 무결성 및 역사적 컨텍스트 보존 규율 (Documentation Integrity & No Careless Deletion)**:
   - 문서에 기록된 논의 배경, 설계 트레이드오프 분석, 과거의 아키텍처 결정 근거는 프로젝트의 핵심 엔지니어링 자산입니다. 문서를 새로 정리하거나 최신화할 때 기존 맥락이나 과거 결론을 임의로 삭제하거나 덮어써서는 안 됩니다.
   - 새로운 합의, 구현 완료 내역, 또는 진화된 원칙이 발생하면 기존 기록을 지우지 않고 그 아래에 **"해결 완료", "추가 합의 사항", "후속 원칙"** 형태로 덧붙여 의사결정의 역사와 발전 과정을 명확하게 추적할 수 있도록 관리합니다.
   - 내용 중복 정리가 불가피한 경우에도 과거의 핵심 배경과 기술적 맥락이 유실되지 않도록 엄격히 대조하고 보존해야 합니다.
8. **핵심 분석 및 작업 계획의 즉시 파일 영속화 규율 (Immediate Plan & Architecture Persistence)**:
   - **`/plan` 명령 영속화 불변 규칙**: 사용자가 `/plan` 명령으로 프롬프트를 입력하면, 그 결과로 수립된 계획(PLAN) 문서는 **무조건 디스크의 `notes/` 디렉터리에 즉시 파일로 저장**합니다 (한글 본문인 경우 `notes/*_PLAN.ko.md`, 영문 본문인 경우 `notes/*_PLAN.md`). 문서의 삭제나 정리는 AI가 임의로 판단하지 않고 차후 수작업으로 검토합니다.
   - 성능 분석, 구조적 병목 진단, 최적화 제안, 리팩토링 설계 등 세션 대화 중에 도출된 중요한 엔지니어링 분석이나 작업 계획(PLAN)을 **대화창 메모리(트랜스크립트)에만 남겨두고 다음 작업으로 넘어가는 것을 엄격히 금지**합니다.
   - 대화 세션 컨텍스트가 압축되거나 모델이 교체되면 메모리 내 아이디어는 완전히 유실되기 때문입니다.
   - 따라서 작업 계획 및 분석이 도출되는 즉시, 최소한 `scratch/` 임시 디렉터리나 정식 `notes/*_PLAN.ko.md` 파일로 즉각 디스크에 기록하고, `TODO.ko.md`에 링크를 걸어 세션이 바뀌어도 지속적이고 완전한 핸드오버가 가능하도록 해야 합니다.
9. **능동적 모듈화 및 도메인 유틸리티 승격 철학 (Rule of Two & Proactive Domain Factoring)**:
   - **"KISS는 모듈화의 생략이 아니다"**: 단순함(Simplicity)이란 각 호출부에 임시방편(Ad-hoc) 문자열 조작이나 조건 분기를 인라인으로 복사-붙여넣기하는 것이 아닙니다. 복잡한 세부 구현은 캡슐화된 모듈 뒤로 숨기고, 호출부(Call site)를 1~2줄로 극도로 단순하고 명확하게 유지하는 것이 진정한 KISS입니다.
   - **"Rule of Two 원칙"**: 동일하거나 유사한 파싱, 조건 검증, 데이터 변환 로직이 코드베이스 내 2곳 이상에서 발견되거나 재사용 가능성이 명백한 경우, 각 파일마다 인라인 코드를 작성하는 것을 엄격히 금지합니다.
   - **도메인 코어 패키지 우선 승격**: 재사용 가능한 로직은 즉시 도메인 유틸리티 패키지(예: `pkg/sqlutil`, `pkg/proc`)에 단독 단위 테스트(`*_test.go`)를 갖춘 모듈식 API(예: `sqlutil.ParseInPredicate`, `sqlutil.StripParentheses`)로 승격(Externalize)시켜 시스템 전반의 DRY를 사수합니다.
   - **사전 자문(Self-Audit) 습관**: 새로운 헬퍼 로직을 특정 파일 안에 작성하기 전에 항상 먼저 자문합니다: *"이 로직이 특정 파일의 전유물인가, 아니면 도메인 공통 유틸리티인가?"* 공통 유틸리티라면 먼저 모듈을 만들고 호출부는 얇게 유지합니다.

---

## 부록 B: Zero-Regex 전면 퇴출 및 결정론적 파서 전환 연대기 (GA-REGEX-DDL-1 ~ GA-REGEX-FINAL-3)

### 1. 전면 퇴출의 근본적 배경 (Why Zero-Regex?)
SQL은 괄호의 짝이 무한히 중첩될 수 있는 비정규(Context-Free, Type 2 Dyck Language) 언어입니다. 유한 오토마타(FSM, Type 3)에 기반한 정규식(Regular Expression)으로 SQL 문법을 파싱하거나 절을 추출하려는 시도는 다음과 같은 구조적 결함을 필연적으로 야기합니다:
1. **괄호 및 중첩 구조 파괴**: `WHERE x IN (SELECT y FROM t WHERE (a = 1))` 또는 `TOP (10)`과 같은 구조에서 정규식의 탐욕적(Greedy)/비탐욕적(Lazy) 매칭은 의도치 않은 지점에서 문자열을 잘라버려 쿼리를 오염시킵니다.
2. **리터럴 및 주석 오염**: 문자열 리터럴(`'Price is $10'`)이나 주석(`/* ... */`) 내부에 포함된 키워드(`SELECT`, `JOIN`, `NOLOCK`)를 SQL 절로 오인식하여 쿼리 본문을 손상시킵니다.
3. **런타임 힙 할당 및 GC 부하**: 쿼리 실행 런타임의 행 순회(Row iteration)에서 `regexp.Compile`이나 `regexp.MatchString`을 반복 호출하면 심각한 CPU 캐시 미스와 GC 압박이 발생합니다.

이에 따라 LionSQL은 프로덕션 쿼리 파싱 및 런타임 핫패스에서 정규식을 100% 영구 퇴출하고 결정론적 토크나이저(`proc.Lexer`, `sqlutil`) 및 2-Tier AST 파서로 전면 전환을 단행하였습니다.

### 2. 단계별 정규식 퇴출 연대기
* **Phase 1 (`GA-REGEX-DDL-1`) - DDL 및 엔진 진입로 35+종 정규식 전면 제거**:
  - 대상: `sqlexec_ddl.go`, `sqlexec_eval.go`, `sqlexec_explain.go`
  - 내용: `reCreateTablePrefix`, `reAddColumn`, `reAlterColType`, `reColDefault`, `reInlineFK`, `reDropTable` 등 35개 이상의 레거시 정규식을 전량 제거하고 `proc.Lexer` 및 `sqlutil` 결정론적 상태머신으로 전환.
* **Phase 2 (`FIX-SQLUTIL-PARSER-1`) - 결정론적 파서 14대 결함 치유 및 품질 강화**:
  - 대상: `pkg/sqlutil` (`pagination.go`, `predicate.go`)
  - 내용: 정규식 퇴출 직후 ad-hoc 파서에서 발견된 리터럴 파괴, 세미콜론 누락, `TOP PERCENT/WITH TIES/UNION` 오변환, `ANY/IN/IS NULL` 다중바이트 연산자 누락 등 14대 probe 결함을 완전 치유하고 단위 테스트로 고정.
* **Phase 3 (`GA-REGEX-FINAL-1`) - 엔진 진입로 및 가상 카탈로그 잔여 정규식 제거**:
  - 대상: `sqlexec_select.go` (T-SQL 페이징 치환기 `mssqlTopRe`, `mssqlOffsetFetchRe` 및 SQLite 호환 `reBareTableIn`), `sqlexec_jdbc_metadata.go`
  - 내용: 쿼리 재작성기를 `sqlutil.NormalizeMSSQLPagination` 단일 결정론적 함수로 일원화.
* **Phase 4 (`GA-REGEX-FINAL-2`) - CBO 플래너 및 VDBE 가상머신 잔여 정규식 제거**:
  - 대상: `pkg/planner/planner.go` (`selectRe`, `joinPattern`), `pkg/proc/runtime/vm.go` (파라미터 바인딩 치환 및 LIKE)
  - 내용: `splitSelectClauses` 단일화 및 `sqlutil.FindAllTopLevelKeyword("JOIN")` 기반 순수 토큰 분해로 대체, `vm.go`의 `"regexp"` import 완전 제거.
* **Phase 5 (`GA-REGEX-FINAL-3`) - 쿼리 실행 런타임 LIKE 정규식 제거 및 일원화**:
  - 대상: `pkg/execution/filter.go` (Volcano Seq Scan 필터 노드), `pkg/catalog/virtual/result.go`
  - 내용: 행 순회 핫패스에서 `strings.ReplaceAll(regexp.QuoteMeta...)` 및 `regexp.MatchString`을 완전 퇴출하고 `sqlutil.MatchLike`로 전면 교체하여 0-할당 달성. 가상 카탈로그의 T-SQL 대괄호 이스케이프(`[_]`, `[%]`) 누락 결함 보정.

### 3. 프로젝트 전역 정규식 허용/금지 최종 경계 매트릭스
LionSQL은 무조건적인 문자열 정규식 혐오가 아닌, **언어학적 문맥(CFG vs Linear)**에 기반한 명확한 기술적 경계를 수호합니다:
| 분류 | 영역 / 파일 | 허용 여부 | 사유 및 동작 원칙 |
| :--- | :--- | :--- | :--- |
| **엄격 금지 (Zero-Regex)** | SQL DDL/DML 파싱 및 쿼리 라우팅 | **금지 (100% 제거 완료)** | 괄호 중첩, 주석, 문자열 리터럴 오염 및 문법 파괴 원천 차단 |
| **엄격 금지 (Zero-Regex)** | CBO 플래너 및 절(Clause) 분해 | **금지 (100% 제거 완료)** | 서브쿼리, 복합 조인 탐욕 매칭 방지 |
| **엄격 금지 (Zero-Regex)** | 쿼리 실행기 런타임 행 순회 `LIKE` | **금지 (100% 제거 완료)** | 핫패스 행 순회 시 GC 부하 및 CPU 캐시 미스 방지 (`sqlutil.MatchLike`) |
| **영구 허용 (불변식 7)** | SQL 내장 정규식 함수 (`REGEXP_LIKE`, `~`, `!~`) | **허용 (표준 기능)** | 사용자가 명시적으로 정규식 평가를 요청한 표준 기능 (`proc/builtins_string.go`) |
| **영구 허용 (불변식 7)** | 마이그레이션 파일명 검증 (`migrate.go`) | **허용 (선형 포맷)** | `^\d{14}_.+\.(up\|down)\.sql$` 평면 정규 언어 검증 |
| **영구 허용 (불변식 7)** | REST URL 파라미터 / 인증 플레이스홀더 (`api/`) | **허용 (선형 포맷)** | 선형 텍스트 치환 |
| **영구 허용 (불변식 7)** | 테스트 러너 및 벤치마크 도구 (`babelfish_runner`, `tools/`) | **허용 (도구 전용)** | 런타임 엔진 외부의 비-프로덕션 테스트 하네스 |

