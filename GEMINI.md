# LionSQL 프로젝트 아키텍처 & 엔지니어링 가이드라인 (GEMINI.md)

## 1. 프로젝트 개요 (Project Overview)
LionSQL은 단일 독립 실행 파일로 패키징된 고동시성 임베디드 OLTP 데이터베이스 엔진입니다 (Go 언어로 구현). 엔터프라이즈급 T-SQL 저장 프로시저 실행, ACID 트랜잭션 지속성, 다중 와이어 프로토콜 및 내장 웹 관리 스튜디오를 제공합니다.

- **스토리지 백엔드**: In-Memory OCC (낙관적 동시성 제어 및 WAL/AOF 압축) 및 WiredTiger (Lock-Free B+Tree, MVCC 및 Hazard Pointer).
- **와이어 프로토콜**:
  - PostgreSQL Wire Protocol v3 (기본 TCP `:5433`)
  - Microsoft SQL Server TDS 7.x/8.0 프로토콜 (기본 TCP `:1433`)
  - REST & JSON HTTP API / 웹 스튜디오 (기본 TCP `:8090`)
- **실행 엔진**: `sync.Pool` 레지스터 재사용 기반의 SQLite VDBE 스타일 T-SQL 바이트코드 가상 머신.

---

## 2. 핵심 아키텍처 불변 규칙: 듀얼 파서 및 자동 폴백 전략

LionSQL은 서브마이크로초 파싱 지연시간과 Microsoft T-SQL 전체 문법 호환성을 동시에 달성하기 위해 **듀얼 파서 아키텍처(Dual-Parser Architecture)**를 영구 불변 규칙으로 강제합니다:

```
                  [ T-SQL 클라이언트 스크립트 / 쿼리 ]
                                │
                                ▼
                 [ 1차: 고속 Native Pratt 파서 ]
                 (pkg/proc/lexer.go, parser.go)
                                │
                    ┌───────────┴───────────┐
                 (성공)                  (실패)
                    │                       │
                    │                       ▼
                    │           [ 2차: ANTLR4 폴백 파서 ]
                    │           (pkg/proc/parser/antlr/)
                    │           - 공식 TSqlParser.g4
                    │           - ASTBuilder 부분문자열 슬라이스
                    │                       │
                    └───────────┬───────────┘
                                │
                                ▼
                 [ 통합 AST: *proc.CreateProcStmt ]
                                │
                                ▼
                 [ Native VDBE 바이트코드 컴파일러 ]
                                │
                                ▼
                 [ 바이트코드 VM & 스토리지 실행 ]
```

### 핵심 원칙 및 요구사항:
1. **1차 고속 패스 (Native Pratt / 재귀 하강 파서)**:
   - 위치: `pkg/proc/lexer.go`, `pkg/proc/parser.go`
   - 목적: 고빈도 쿼리, 표준 DDL/DML, 핵심 프로시저 제어 흐름문(`IF`, `WHILE`, `TRY...CATCH`, `SELECT...INTO`, `UPDATE`, `INSERT`, `DELETE`)을 위한 제로 할당, 서브마이크로초 초고속 파싱.
2. **2차 전체 문법 폴백 (공식 Microsoft T-SQL ANTLR4 파서)**:
   - 위치: `pkg/proc/parser/antlr/`
   - 목적: 1차 파서가 고급 벤더 특화 구문이나 레거시 MSSQL 문법(`WITH RECOMPILE`, `WITH ENCRYPTION`, 스키마 접두사 `dbo.ProcName`, 대괄호 이스케이프 `[dbo].[Proc]`, 벤더 옵션 등)을 만났을 때 런타임 폴백으로 자동 호출.
3. **플러그형 디커플링 (순환 참조 원천 차단)**:
   - `pkg/proc`은 `ProcedureFallbackParser` 훅과 `RegisterProcedureFallback()` / `GetProcedureFallback()` API를 정의합니다.
   - `pkg/proc/parser/antlr/ast_builder.go`는 `init()`을 통해 자신을 등록합니다.
   - `pkg/proc`은 `pkg/proc/parser/antlr`을 직접 임포트하지 않으며, 엄격한 계층 구조와 순환 임포트 제로를 유지합니다.
4. **영구 불변성 (Permanent Invariance)**:
   - 두 파서와 자동 폴백 메커니즘은 **영구적으로 유지 관리 및 테스트**되어야 합니다.
   - 새로운 문법 확장은 먼저 ANTLR에 대해 검증되어야 하며, `pkg/proc/runtime/regression_test.go`에서 듀얼 파서 패리티가 확인되어야 합니다.

---

## 3. 핵심 아키텍처 불변 규칙: 데이터베이스 격리 전략 (인스턴스 기반 격리)

LionSQL은 단일 프로세스 내에서 다중 데이터베이스 카탈로그를 멀티플렉싱(`USE <dbname>`)하는 대신, **인스턴스 수준 격리 (데이터베이스당 프로세스 / 컨테이너 격리)**를 핵심 아키텍처 불변 규칙으로 채택합니다:

1. **아키텍처 근거**:
   - **결함 및 자원 격리**: 한 데이터베이스의 폭주 쿼리, 메모리 누수, 파일 디스크립터 고갈이 다른 데이터베이스에 영향을 주지 않도록 원천 차단.
   - **보안 및 영향 범위 (Blast Radius)**: 교차 데이터베이스 데이터 유출을 방지하고 데이터 파일, WAL/AOF 로그, TDE 암호화 키, 네트워크 포트의 엄격한 물리적 격리 보장.
   - **독립적 수명 주기**: 데이터베이스별 독립적인 스케일링, 업그레이드, 시점 복구(PITR) 및 재시작 가능.
   - **KISS 원칙 및 불필요한 복잡성 제거**: 데이터베이스 커널 내 교차 데이터베이스 카탈로그 잠금, 세션 데이터베이스 컨텍스트 스위칭 오버헤드, 분산 락 경합 제거.
2. **영구 명세 불변 규칙**:
   - **진짜 다중 데이터베이스 카탈로그 동시 마운트 및 런타임 카탈로그 멀티플렉싱**: 명시적으로 **Spec Out (제외)**. 각 LionSQL 프로세스는 데이터 디렉터리(`--data-dir`)당 정확히 하나의 격리된 데이터베이스 카탈로그만 서비스합니다.
   - **`USE <dbname>` 관용적 가상 No-Op 허용 정책**: 단, MSSQL 덤프 스크립트, 마이그레이션 파일, DBeaver/SSMS/sqlcmd 등 외부 도구와의 100% 호환성을 위해 `USE <dbname>` (`USE master`, `USE [SalesDB]`) 구문은 **관용적 가상 No-Op(Tolerant Virtual No-Op)**으로 즉시 수용하고 TDS 환경 변경 토큰(`0xE3 ENVCHANGE`)과 세션 가상 DB 컨텍스트(`ClientSession.Database`, `DB_NAME()`, `CURRENT_DATABASE()`)를 동기화하여 구문 에러를 원천 차단함.
   - **멀티 테넌트 배포 표준**: 멀티 테넌시 및 다중 데이터베이스는 전용 포트와 디렉터리를 갖춘 가벼운 LionSQL 독립 프로세스/컨테이너(`--port`, `--data-dir`)를 통해 배포됩니다.

---

## 4. 핵심 아키텍처 불변 규칙: 트랜잭션 격리 및 동시성 모델

LionSQL은 두 스토리지 엔진 전반에 걸쳐 **Non-Blocking 스냅샷 격리 / 낙관적·Hazard 동시성 제어를 갖춘 Read Committed**를 정규 트랜잭션 처리 불변 규칙으로 정의합니다:

1. **아키텍처 원칙**:
   - **Non-Blocking 읽기 (읽기는 쓰기를 블로킹하지 않고, 쓰기는 읽기를 블로킹하지 않음)**: 두 백엔드 모두 전통적인 2PL(Two-Phase Locking) 공유 읽기 락을 완전히 제거했습니다. 쿼리는 항상 공유 락 획득 없이 일관된 스냅샷에서 읽습니다.
   - **프라이빗 워크스페이스 격리**: 모든 변이 작업(INSERT, UPDATE, DELETE)은 커밋될 때까지 트랜잭션 전용 프라이빗 워크스페이스에 버퍼링되어, Dirty Read와 미커밋 데이터 유출이 전혀 발생하지 않습니다.
   - **스토리지 엔진별 동시성 프로필**:
     - **In-Memory OCC (`pkg/storage/memengine`)**: 레코드별 단조 증가 정수 버전(`Version uint64`)을 사용하는 순수 OCC. 동시성 충돌은 커밋 단계(`Commit()`)에서 원자적으로 검증되며, 쓰기-쓰기 충돌 시 즉시 실패-빠른 롤백(`ErrWriteConflict`)을 트리거합니다.
     - **WiredTiger MVCC (`pkg/storage/wiredtiger`)**: Hazard Pointer와 트랜잭션 타임스탬프를 갖춘 디스크 기반 MVCC B+Tree. 동일 키에 대한 동시 충돌 쓰기는 쓰기/커밋 작업 중에 감지됩니다(`WT_ROLLBACK`).
2. **영구 명세 불변 규칙**:
   - **표준 격리 수준**: 모든 와이어 프로토콜(PGWire, TDS, HTTP) 전반에 걸쳐 정규 스냅샷 / Read Committed 시맨틱이 강제됩니다.
   - **비관적 읽기 락 (`READ UNCOMMITTED`, 무거운 분산 2PL, SSI 그래프 추적)**: 명시적으로 **Spec Out / Deferred (제외 및 보류)**. 오탐단 롤백 페널티나 직렬화 의존성 그래프, 더티 리드 타협을 초래하는 복잡한 격리 수준은 초고성능 Non-Blocking OLTP 실행을 위해 배제됩니다.

---

## 5. 핵심 아키텍처 불변 규칙: 자립형 단일 바이너리 (외장 의존성 제로)

LionSQL은 **자율적이고 외부 의존성이 전혀 없는 독립 실행형 정적 바이너리**로 설계되었습니다:

1. **아키텍처 원칙**:
   - **풀스택 자체 내장**: 데이터베이스 커널, 다중 프로토콜 와이어 리스너(PGWire :5433, TDS :1433), HTTP/REST 엔진(:8090), 임베디드 웹 스튜디오(`embed.FS`), 고루틴 커넥션 풀러, 크론 작업 스케줄러, SMTP 메일러가 단 하나의 정적 바이너리에서 구동됩니다.
   - **운영 사이드카 제로**: 외부 보조 프로세스나 데몬을 완전히 배제합니다 (PgBouncer 없음, Redis 캐시 계층 없음, Nginx 같은 외부 웹서버 없음, OS crontab 의존성 없음).
2. **영구 명세 불변 규칙**:
   - **외부 분산 코디네이터**: ZooKeeper, etcd, Consul 또는 외부 메시지 브로커(Kafka, RabbitMQ)는 데이터베이스 코어 커널에서 명시적으로 **Spec Out (제외)**됩니다.
   - **배포의 단순성**: 완전히 동작하는 데이터베이스 노드를 배포하는 데에는 단일 실행 파일을 대상 디렉터리(`--data-dir`)에서 실행하는 것 외에 어떠한 설정도 필요하지 않습니다.

---

## 6. 핵심 아키텍처 불변 규칙: 스케일업 우선 & WAL 복제 토폴로지

LionSQL은 복잡한 분산 멀티 마스터 샤딩보다 **수직적 스케일업 하드웨어 포화**와 **1-Writer Multi-Reader 비동기/반동기 WAL 복제**를 우선합니다:

1. **아키텍처 원칙**:
   - **단일 노드 하드웨어 포화**: 단일 호스트에서 멀티코어 CPU 동시성, 락프리 데이터 구조(Concurrent SkipList, Hazard Pointers), NVMe I/O 효율성 및 서브마이크로초 VDBE 바이트코드 실행을 극대화하는 데 엔지니어링 역량을 집중합니다.
   - **쓰기 경로 분산 합의 페널티 제로**: 쓰기 경로에서 분산 2PC(Two-Phase Commit) 네트워크 왕복 지연시간을 배제하여 단일 노드의 원시 트랜잭션 처리량(16,000+ TPS)을 온전히 보존합니다.
2. **표준 고가용성 & 읽기 확장 토폴로지 (1-Writer Multi-Reader)**:
   - **Primary 노드 (단일 라이터)**: 모든 쓰기 변이(INSERT, UPDATE, DELETE)를 처리하고, OCC/MVCC 검증을 강제하며, 정규 WAL/AOF 로그 스트림을 생성하고 읽기 쿼리를 처리합니다.
   - **Replica 노드 (다중 리더)**: LSN 추적을 통해 Primary로부터 WAL 레코드를 지속적으로 스트리밍 및 재생(Replay)하여 락 경합 없이 읽기 용량(SELECT)을 스케일아웃합니다.
   - **장애 조치 승격**: 복잡한 런타임 분산 합의 대신, Primary 장애 시 LSN이 가장 최신인 Replica를 Primary로 승격하여 고가용성을 달성합니다.
3. **영구 명세 불변 규칙**:
   - **멀티 마스터 분산 샤딩 & 2PC 그래프**: 명시적으로 **Spec Out / Deferred (제외 및 보류)**. 코어 엔진의 복잡성은 단일 노드 ACID 실행과 선형적 WAL 로그 스트림 복제로 한정됩니다.

---

## 7. 핵심 아키텍처 불변 규칙: 벤치마크 및 트랜잭션 모델링 무결성 (ACID 단일 트랜잭션 원자성)

LionSQL은 모든 벤치마크(TPC-C, TPC-H, 마이크로 벤치마크), 테스트 하네스 및 저장 프로시저 워크로드 전반에 걸쳐 **엄격한 원자적 ACID 트랜잭션 모델링**을 강제합니다:

1. **전부 아니면 전무(All-or-Nothing) 트랜잭션 무결성**:
   - 다중 문장 비즈니스 트랜잭션(예: 웨어하우스 조회, 디스트릭트 카운터 증가, 고객 조회, 주문/신규주문 삽입 및 5~10개 주문 항목/재고 갱신으로 구성된 TPC-C New-Order)은 **반드시 단일 원자적 ACID 트랜잭션 경계(`sess.BeginTx()` -> `tx.Commit()`) 내에서 엄격하게 실행**되어야 합니다.
   - 단일 논리 비즈니스 트랜잭션을 여러 자동 커밋(Autocommit) 문장이나 별도의 앰비언트 트랜잭션(예: `exec.Run()` 루프)으로 분할하는 것은 **엄격히 금지**됩니다.
2. **VDBE 바이트코드 VM 트랜잭션 결합**:
   - 벤치마크 하네스에서 실행되는 다단계 저장 프로시저 시퀀스 또는 파티셔닝된 VDBE 바이트코드 프로그램은 명시적 트랜잭션 핸들 `tx`를 공유하는 `exec.RunInTx(prog, params, tx, nil)`를 통해 실행되어야 합니다.
   - 단계 내에서의 트랜잭션 실패, 충돌 중단 또는 롤백은 즉시 `tx.Rollback()`을 트리거하여 부분 쓰기나 불완전한 레코드를 일절 남기지 않아야 합니다.

---

## 8. 핵심 아키텍처 불변 규칙: 엔진 단순성 및 명시적 기능 배제 (Explicit Spec-Out Decisions)

LionSQL은 엔터프라이즈급 sub-microsecond 트랜잭션 성능과 자립형 단일 바이너리(KISS) 아키텍처를 영구히 유지하기 위해 다음 기능들을 명시적으로 **Spec Out (영구 배제 및 불변 규칙)**으로 확정합니다:

1. **절차 언어 런타임 단일화 & 중복 인터프리터(PL/pgSQL) 배제**:
   - 프로시저 및 UDF 실행 엔진은 Native SQLite VDBE 스타일의 고성능 T-SQL 바이트코드 가상머신(`pkg/proc`)으로 완전 단일화합니다.
   - PGWire는 PostgreSQL 표준 드라이버, ORM 및 GUI 도구(DBeaver, Beekeeper Studio 등)와의 클라이언트 연결 게이트웨이로만 기능하며, 엔진 내부에 PL/pgSQL 인터프리터를 중복 구현/탑재하는 것은 코어 비대화 및 버그 발생 지점 증가를 방지하기 위해 명시적으로 **Spec Out**합니다.
2. **모던 JSON 전면 표준화 & 레거시 XML DML/XQuery 엔진 배제**:
   - 반정형 데이터 처리는 T-SQL 및 ANSI 표준 JSON 함수군(`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`, `FOR JSON PATH / AUTO`) 및 Go 네이티브 고속 JSON 파서로 표준화합니다.
   - 2000년대 레거시 Microsoft XML 스키마 검증, XML 데이터 타입 및 XML DML(`.modify()`, `.nodes()`, `.value()`, XQuery 프로세서)은 메모리 오버헤드와 파서 복잡성 배제를 위해 명시적으로 **Spec Out**합니다.
3. **인메모리 OCC 스토리지 데드 튜플 백그라운드 Vacuum 스레드 배제**:
   - 인메모리 OCC 엔진(`memengine`)은 세션 프라이빗 워크스페이스에서 변경분을 버퍼링 후 커밋 시 원자적으로 버전을 교체하거나 롤백 시 워크스페이스를 버림으로써 데드 튜플 체인을 메모리에 누적하지 않습니다.
   - 불필요한 백그라운드 고루틴 생성, 주기적 전체 메모리 스캔 및 락 경합을 유발하는 PostgreSQL 스타일의 Vacuum 워커 스레드는 오버엔지니어링(YAGNI)으로 명시적으로 **Spec Out**합니다.
4. **CDC 이벤트 스트리밍 표준화 & POSIX 공유 메모리(Arrow IPC) 스냅샷 배제**:
   - 트랜잭션 커밋과 원자적으로 결합된 CDC/WAL 이벤트 스트림을 단일 표준 파이프라인으로 채택하여 임베디드 DuckDB OLAP 마이크로 배치 적재와 외부 브로커 포워딩을 일원화합니다.
   - 쿼리마다 수 GB의 테이블을 풀스캔하여 직렬화하는 높은 OLTP CPU 부하와 OS 비이식성(도커 컨테이너 `/dev/shm` 64MB 한계, Windows 미지원)을 초래하는 Apache Arrow IPC 공유 메모리 스냅샷은 명시적으로 **Spec Out**합니다.
5. **외부 분산 트랜잭션 코디네이터 (XA / MS DTC) 배제**:
   - 네트워크 분산 2PC 트랜잭션 프로토콜인 XA Two-Phase Commit 및 Microsoft Distributed Transaction Coordinator (MS DTC)는 단일 노드 16,000+ TPS 트랜잭션 처리량 보존을 위해 명시적으로 **Spec Out**합니다. 분산 환경의 정합성은 비동기/반동기 WAL 복제와 멱등적(Idempotent) 이벤트 컨슈밍으로 처리합니다.
6. **DB 커널 내장 OAuth2 웹 플로우 배제 & 클라이언트 사이드 토큰 교환(Client-Side Token Exchange) 표준화**:
   - 데이터베이스 엔진의 보안 및 사용자 인증은 엔터프라이즈 데이터베이스 표준 규격(PGWire의 SCRAM-SHA-256, TDS의 SQL Server 표준/NTLM 인증, HTTP 관리용 안전한 토큰 기반 RBAC/API-Key)으로 완전 일원화합니다.
   - 외부 컨슈머용 OAuth2 소셜 로그인(구글, 깃허브 등 특정 SaaS의 웹 리다이렉트 핸들러, Client Secret 생명주기 관리, 아웃바운드 인터넷 토큰 교환 HTTP 호출)을 데이터베이스 커널 정적 바이너리에 직접 내장하는 것은 명시적으로 **Spec Out**합니다. 이를 통해 외부 인터넷 아웃바운드 통신 의존성을 완전히 제거하고, Client Secret 유출 리스크 및 보안 공격 표면(Attack Surface)을 원천 차단하며, 데이터베이스 커널 본연의 경량성과 무결성을 보호합니다(Golden Rule #15 KISS 및 단일 책임 원칙).
   - **클라이언트 사이드 토큰 교환 아키텍처 (권장 / 표준 패턴)**: 소셜 로그인은 Supabase/Firebase/AWS Cognito와 동일한 **Client-Side OIDC Token Exchange** 패턴(RFC 8693 / OpenID Connect ID Token 검증)으로 표준화합니다:
     1. 프론트엔드/클라이언트 앱이 네이티브 SDK를 통해 Google/Apple/GitHub 등 IdP에 직접 로그인하여 서명된 OIDC ID Token(JWT)을 발급받습니다.
     2. 클라이언트는 발급받은 ID Token을 LionSQL API(`POST /api/auth/token-exchange`)로 전달합니다.
     3. LionSQL 커널(`pkg/auth`)은 IdP의 공개키(JWKS)로 서명 무결성만 암호학적으로 검증하고 사용자 클레임(`sub`, `email`)을 추출하여 내부 DB 세션 토큰을 발급합니다.
     4. 이로써 DB 커널 내부에는 `client_secret` 보관, 브라우저 리다이렉트, 세션 쿠키 관리 등의 웹 서버 부가 책임이 일체 발생하지 않습니다.
7. **DB 커널 내장 S3 Blob 파일 스토리지 및 이미지 썸네일 리사이징 배제 (Spec-Out)**:
   - 대용량 바이너리 스트리밍 및 CPU 집약적 이미지 리사이징으로 인한 OLTP 고성능 코어의 CPU/메모리 기아를 방지하기 위해 데이터베이스 커널 내장 Blob 파일 서버는 명시적으로 **Spec Out**합니다 (Golden Rules 14 & 15).
   - 오브젝트 스토리지 파이프라인은 전용 클라우드 스토리지(AWS S3, MinIO, Cloudflare R2) 및 엣지 CDN에 위임하며, LionSQL은 테이블에 정규 URI 및 메타데이터 포인터만 저장합니다.
8. **미성숙 Pure-Go 영속 스토리지 엔진 (Pebble/bbolt) 단기 도입 배제 (Spec-Out & 지속적 대안 탐색)**:
   - 현재 오픈소스 Pure-Go 임베디드 스토리지 생태계(Pebble, bbolt, Badger 등)는 WiredTiger 수준의 고성능 멀티코어 MVCC B+Tree, Hazard Pointer 동시성, 세밀한 트랜잭션 타임스탬프와 ACID 격리를 온전히 대체하기에는 성숙도, 레이턴시, 트랜잭션 기능 측면에서 아직 프로덕션 OLTP 대안으로 쓸만한 상태가 아닙니다.
   - 따라서 1.0 프로덕션 체제에서는 미성숙한 제3의 영속 스토리지 엔진 도입을 공식 **스펙-아웃(Spec-Out)**으로 확정하며, WiredTiger MVCC(디스크)와 In-Memory OCC(메모리) 2대 백엔드 체제에 집중합니다.
   - 단, 향후 CGo 완전 배제(Pure-Go 정적 단일 바이너리)를 위한 잠재적 스토리지 대안 후보 탐색은 지속적으로 모니터링하고 평가합니다.
9. **슬로우 쿼리 실시간 웹훅 직발송 커널 내장 배제 (`OBS-3`) (Spec-Out & Post-1.0 툴링 아이디어 검토)**:
   - 데이터베이스 커널 내부에서 Slack, Discord 등의 외부 HTTP 웹훅을 직접 호출하는 것은 단일 책임 원칙, 극단적 KISS, 엔진 순수성을 심각하게 위반합니다 (Golden Rules 14 & 15).
   - 커널 내부의 직접적인 아웃바운드 HTTP 호출은 네트워크 지연(RTT), 타임아웃 처리, 비동기 재시도 큐 관리, 고루틴 기아 등 심각한 런타임 리스크를 유발합니다. 관제 및 알림은 이미 구축된 표준 **Prometheus 메트릭(`OBS-1`), 런타임 실행 분석기(`OBS-2`, `EXPLAIN ANALYZE`), 암호학적 불변 감사 로그**를 통해 외부 전용 관제 도구(Alertmanager, Grafana, Vector 등)가 수집하여 처리하는 것이 표준 아키텍처입니다. 따라서 커널 내장 웹훅은 공식 **스펙-아웃(Spec-Out)**으로 확정하며, 필요 시 Post-1.0 외부 사이드카/CLI 도구 아이디어로만 검토합니다.
10. **Wasm UDF 격리 샌드박스 (`wazero`) 커널 내장 배제 (Spec-Out & Post-1.0 아이디어 탐색)**:
   - 데이터베이스 커널 내부에 별도의 WebAssembly 바이트코드 가상머신(`wazero`)을 내장하는 것은 정적 바이너리 크기 비대화, 런타임 메모리 오버헤드, JIT/Wasm 수명주기 관리 복잡도를 크게 증가시키며, 실제 OLTP 환경에서의 실무 수요도 극히 희박합니다.
   - LionSQL은 이미 초고속 SQLite VDBE 스타일 T-SQL 가상머신(`pkg/proc`)과 제로-할당 Go Native UDF SPI(`proc.RegisterScalarFunction`, <5ns)로 프로시저 및 함수 실행 경로가 일원화되어 있습니다. 불필요한 제3의 런타임 내장은 공식 **스펙-아웃(Spec-Out)**으로 확정하며, 1.0 이후 장기적 아이디어 수준으로만 후보를 탐색합니다.
11. **복잡 쿼리 전면 VDBE 바이트코드화 배제 & 2-Tier 쿼리 실행 아키텍처 영구 불변 (`ARCH-2TIER-1`)**:
   - 복잡한 쿼리(다중 테이블 조인, 윈도우 함수, 재귀 CTE, 복합 서브쿼리 등)는 '빠르게 하는 것보다 옳게(Correctness & Modularity) 하는 것'이 절대적으로 우선합니다. SQLite 스타일로 모든 복합 관계대수를 바이트코드 VM으로 번역하려는 시도는 수십만 라인의 컴파일러 비대화, 엣지 케이스 디버깅 난도 폭증, 불필요한 시스템 복잡도를 초래하므로 명시적으로 **Spec Out**합니다.
   - 단순 OLTP 쿼리는 VDBE 바이트코드(Tier 1, <1µs)로 초고속 처리하고, 복합 관계대수 쿼리는 표준 관계대수 연산자 격리와 디버깅/실행계획 검증(`EXPLAIN ANALYZE`)이 명확한 Volcano CBO 반복자 엔진(Tier 2)으로 처리하는 2-Tier 구조를 영구 불변 규칙으로 확정합니다. 상세 결정 내역은 [`notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md)를 참조합니다.

---

## 9. 엔지니어링 가이드라인 & 개발 파이프라인

### 3단계 검증 파이프라인 (Build ➔ Test ➔ Commit)
모든 수정, 리팩토링 및 기능 추가는 커밋하기 전에 반드시 다음 3단계를 순서대로 통과해야 합니다:

1. **빌드 (Build)**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
   ```
2. **테스트 (Race-Free 무결성 검증)**:
   ```bash
   go test -tags wiredtiger -count=1 -v -race ./...
   ```
   전체 패키지 100% PASS 및 데이터 레이스 0건을 반드시 달성해야 합니다.
3. **커밋 (Commit)**:
   - Conventional Commits 형식 (`feat(...)`, `fix(...)`, `refactor(...)`, `docs(...)` 등).
   - 커밋 메시지, 주석, 코드 내 장식용 이모지 일절 배제.

> **예외 (문서 전용 커밋)**: 설정이나 런타임 동작에 영향을 주지 않는 순수 마크다운(`.md`) 및 텍스트 문서 수정(`docs: ...`)의 경우 불필요한 빌드 및 테스트 단계를 생략하고 즉시 커밋할 수 있습니다.

### 지속적 백로그 & TODO 동기화 규율 (`TODO.ko.md` Auto-Sync 불변 규칙)
대화 중 새로운 기능 요구, 아키텍처 결정(ADR), 또는 구체적인 엔지니어링 구현 계획이 논의되고 합의되면, AI 에이전트는 사용자의 명시적인 요청 명령을 기다리지 않고 **해당 세션 내에서 선제적으로 `TODO.ko.md`에 자동 등록(Section 1 우선순위 테이블 및 Section 2 도메인 백로그)**해야 합니다. 마찬가지로 작업이나 리팩토링이 완료되어 검증을 통과한 경우, 에이전트는 즉시 해당 항목을 완료(`[x]`) 처리하고 구현 요약을 마일스톤에 기록해야 합니다.

---

## 10. 개발자 & AI 인수인계 컨텍스트 노트 (`notes/`) & 로드맵
다양한 개발 환경에서 작업하거나 새로운 AI 에이전트 세션을 시작할 때는 과거 설계 논의, 음성적 지식(Spec-Out 사유), 엔지니어링 주의점 및 백로그에 대해 다음 문서를 참조하세요:
- [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md): 아키텍처 불변 규칙, 대안 기술 Spec-Out 사유, 협업 엔지니어링 원칙.
- [`notes/PITFALLS_AND_TRICKS.ko.md`](notes/PITFALLS_AND_TRICKS.ko.md): 순환 참조 방지, wiredtiger 빌드 태그, 샌드박스 네트워크 격리, 소수점 JSON 직렬화, VDBE 트랜잭션 결합.
- [`TODO.ko.md`](TODO.ko.md): 액티브 엔지니어링 백로그, 차기 착수 우선순위 과제(P1, P2, P3), CBO 및 성능 매트릭스, 최근 완료 마일스톤.
- [`CODE_REVIEW.ko.md`](CODE_REVIEW.ko.md): 코드 리뷰 및 품질 감사 로그, 결함 수정 및 3단계 파이프라인 검증 추적.
- [`REFACTORING.ko.md`](REFACTORING.ko.md): 아키텍처 추상화 계층 명세, 모듈 분리 및 Deletion-First 중복 제거 설계도.
- [`ROADMAP.md`](ROADMAP.md): 거시적 릴리즈 마일스톤(Phase 1~6), 아키텍처 진화 방향, 단계별 구현 완료 상태.

