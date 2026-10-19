# LionSQL Engineering Context & Architectural Invariants (`notes/ENGINEERING_CONTEXT.md`)

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
* **결정 사항**: 코어 커널, PGWire(:5433), TDS(:1433), HTTP API(:8090), 임베디드 Web Studio, 고루틴 커넥션 풀러, 크론 스케줄러, SMTP 메일러가 단일 정적 실행 파일(`bin/server`) 내에서 동작.
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

### ⑮ 쿼리 파서 및 실행 엔진의 실용적 설계 원칙: '미학적 단일화'보다 '초고속 성능과 회귀 무결성' 우선 (`KISS-3`, `KISS-5`의 영구 불변식)
* **핵심 불변식**:
  > **"문제가 있어서 못 만든 것"이 아니라, "현재 시스템의 초고속 성능과 무결성을 지키기 위해 의도적으로 남겨둔 최선의 실용적 설계"이다.**
* **세부 배경 및 트레이드오프**:
  1. **`sqlexec` 하위 정규식 파싱 계층 제거 및 AST 파서 일원화 (`KISS-3`)**:
     - **명분 vs 실질**: 이론적으로는 쿼리 실행 계층의 모든 정규식을 없애고 AST 파서(`pkg/proc`) 하나로만 파싱하는 것이 순수해 보이지만, 이미 패키지 싱글톤 정규식 및 1-Pass 인덱스 슬라이스 스캐너(`PERF-1`)를 통해 **16ns/op**의 사실상 제로 오버헤드를 달성함.
     - **회귀 위험 방어**: 전 패키지 및 67개 TDS + 57개 PGWire 드라이버 통합 테스트가 100% 통과 중인 상황에서, 단지 정규식을 없애겠다는 미학적 이유로 수십 개 DML/DDL 경로의 파싱 로직을 대규모로 뜯어고치는 것은 미세한 엣지케이스에서 회귀 버그(Regression)를 유발할 위험만 극도로 높임. 따라서 영구 보류(Indefinitely Deferred)함.
  2. **3갈래 쿼리 실행 경로 단일화 (`sqlexec_select` ➔ `execution` Volcano Iterator) (`KISS-5`)**:
     - **명분 vs 실질**: 모든 SELECT 쿼리를 단 하나의 Volcano 반복자 트리로 통합하는 것이 아키텍처 다이어그램상 깔끔해 보일 수 있음.
     - **OLTP 성능 보호**: 그러나 `SELECT 1`, `SELECT * FROM tbl WHERE id = 1` 같은 고빈도 단건 쿼리는 Volcano 반복자 트리를 조립하고 가상 함수(`Next()`)를 호출하는 비용보다 직결 패스트패스가 **2~3배 빠름**. 이를 무리하게 단일 트리로 강제 통합하면 LionSQL의 핵심 경쟁력인 **TPC-C 20,000+ TPS급의 서브마이크로초 OLTP 처리량을 희생**하게 됨.
     - **결론**: PostgreSQL, SQLite, MySQL 등 고성능 RDBMS가 공통으로 채택하고 있는 **"고빈도 단건 패스트패스 + 복합 쿼리 Volcano CBO" 이원화 구조**를 유지하는 것이 최선의 실용적 엔지니어링 선택임. 향후 Plan Cache 컴파일이나 JIT 코드 생성이 단건 직결보다 빨라지는 혁신이 전제되지 않는 한 현 체제를 유지함.

---

## 2. 사용자의 엔지니어링 철학 및 협업 규약

LionSQL 개발 시 아래의 원칙은 **절대적인 우선순위**를 가집니다:

1. **3단계 검증 파이프라인 (Build ➔ Test `-race` ➔ Commit)**:
   - 모든 기능 수정 및 리팩토링 후에는 빌드 컴파일 검증 ➔ 레이스 디텍터를 포함한 전체 테스트 통과 ➔ Conventional Commit 순서를 엄격히 지켜야 합니다.
   - **문서 수정 예외 규약**: 설정이나 런타임에 영향을 주지 않는 순수 마크다운(`.md`) 및 텍스트 문서 수정 시에는 불필요한 빌드/테스트 단계를 생략하고 바로 커밋합니다.
   - 명령어:
     ```bash
     go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
     go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
     go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
     go test -tags wiredtiger -count=1 -race <modified_packages>
     ```
2. **이모지 절대 배제 (Golden Rule 19)**:
   - 커밋 메시지, 소스코드 주석, 로그 출력, 문서 등에 장식용 이모지(🎉, 🚀, 💡 등)를 일체 사용하지 않으며, 단정한 엔지니어링 톤을 유지합니다.
3. **Radical KISS & Deletion-First (Golden Rule 15 & 21)**:
   - 복잡한 추상화나 불필요한 번역 계층을 새로 짜기 전에, **안 쓰는 레거시 코드를 삭제(Pruning)**하고 OS/Go 표준 라이브러리 프리미티브를 활용하는 해결책을 최우선합니다.
   - 복잡한 에뮬레이션이나 새로운 플래그를 도입하기 전에는 반드시 트레이드오프를 사용자에게 먼저 제안하고 합의를 거칩니다.
4. **비파괴적 원자적 파일 I/O (Golden Rule 1 & 5)**:
   - 데이터나 설정 파일을 쓸 때는 직접 덮어쓰지 않고, 반드시 `filepath.Clean` 후 임시 파일(`*.tmp`)에 쓰고 `os.Rename`으로 원자적 커밋을 수행합니다 (권한: `0644`, 디렉토리: `0755`).
5. **모놀리식 파일 리팩토링 기준**:
   - 단순히 기계적으로 라인 수만 보고 자르는 것이 아니라, **도메인 책임(Responsibility)별로 묶어서 분할**합니다.
   - 예: `sqlexec_dml.go`에서 SELECT/PIVOT/EXPLAIN/EVAL을 분리하고 순수 DML만 남김.
6. **문서 명명 및 디렉터리 배치 규약 (Documentation Naming & Directory Conventions)**:
   - 신규 기능 구현, 아키텍처 통합, 리팩토링을 위한 작업 계획/설계 제안 문서는 개발자/AI 핸드오버용 컨텍스트로 분류하여 **반드시 `notes/*_PLAN.md`**에 배치합니다 (예: `notes/UNIFIED_CLI_PLAN.md`, `notes/IMMUTABLE_AUDIT_LOG_PLAN.md`, `notes/S3_PRESIGNED_STORAGE_PLAN.md`, `notes/COLLATION_AND_LOCALE_PLAN.md`, `notes/DOCUMENTATION_CLEANUP_PLAN.md`).
   - 공식 기술 매뉴얼, 아키텍처 명세서, 가이드 및 호환성 검증 결과 보고서는 **`doc/`** 아래에 배치하며, 검증 문서는 **`doc/*_REPORT.md`** 형식을 유지합니다 (예: `doc/TDS_COMPATIBILITY_REPORT.md`, `doc/PGWIRE_COMPATIBILITY_REPORT.md`, `doc/TEST_VERIFICATION_REPORT.md`).
