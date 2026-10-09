# SQLion 프로젝트 아키텍처 & 엔지니어링 가이드라인 (GEMINI.ko.md)

(For the official English architectural specification, see [GEMINI.md](GEMINI.md).)

## 1. 프로젝트 개요 (Project Overview)
SQLion은 단일 독립 실행 파일로 패키징된 고동시성 임베디드 OLTP 데이터베이스 엔진입니다 (Go 언어로 구현). 엔터프라이즈급 T-SQL 저장 프로시저 실행, ACID 트랜잭션 지속성, 다중 와이어 프로토콜 및 내장 웹 관리 스튜디오를 제공합니다.

- **스토리지 백엔드**: In-Memory OCC (낙관적 동시성 제어 및 WAL/AOF 압축) 및 WiredTiger (Lock-Free B+Tree, MVCC 및 Hazard Pointer).
- **와이어 프로토콜**:
  - PostgreSQL Wire Protocol v3 (기본 TCP `:5433`)
  - Microsoft SQL Server TDS 7.x/8.0 프로토콜 (기본 TCP `:1433`)
  - REST & JSON HTTP API / 웹 스튜디오 (기본 TCP `:8090`)
- **실행 엔진**: `sync.Pool` 레지스터 재사용 기반의 SQLite VDBE 스타일 T-SQL 바이트코드 가상 머신.

---

## 2. 핵심 아키텍처 불변 규칙: 듀얼 파서 및 자동 폴백 전략

SQLion은 서브마이크로초 파싱 지연시간과 Microsoft T-SQL 전체 문법 호환성을 동시에 달성하기 위해 **듀얼 파서 아키텍처(Dual-Parser Architecture)**를 영구 불변 규칙으로 강제합니다:

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

SQLion은 단일 프로세스 내에서 다중 데이터베이스 카탈로그를 멀티플렉싱(`USE <dbname>`)하는 대신, **인스턴스 수준 격리 (데이터베이스당 프로세스 / 컨테이너 격리)**를 핵심 아키텍처 불변 규칙으로 채택합니다:

1. **아키텍처 근거**:
   - **결함 및 자원 격리**: 한 데이터베이스의 폭주 쿼리, 메모리 누수, 파일 디스크립터 고갈이 다른 데이터베이스에 영향을 주지 않도록 원천 차단.
   - **보안 및 영향 범위 (Blast Radius)**: 교차 데이터베이스 데이터 유출을 방지하고 데이터 파일, WAL/AOF 로그, TDE 암호화 키, 네트워크 포트의 엄격한 물리적 격리 보장.
   - **독립적 수명 주기**: 데이터베이스별 독립적인 스케일링, 업그레이드, 시점 복구(PITR) 및 재시작 가능.
   - **KISS 원칙 및 불필요한 복잡성 제거**: 데이터베이스 커널 내 교차 데이터베이스 카탈로그 잠금, 세션 데이터베이스 컨텍스트 스위칭 오버헤드, 분산 락 경합 제거.
2. **영구 명세 불변 규칙**:
   - **`USE <dbname>` 구문**: 명시적으로 **Spec Out (제외)**. 각 SQLion 프로세스는 데이터 디렉터리(`--data-dir`)당 정확히 하나의 격리된 데이터베이스 카탈로그만 서비스합니다.
   - **멀티 테넌트 배포 표준**: 멀티 테넌시 및 다중 데이터베이스는 전용 포트와 디렉터리를 갖춘 가벼운 SQLion 독립 프로세스/컨테이너(`--port`, `--data-dir`)를 통해 배포됩니다.

---

## 4. 핵심 아키텍처 불변 규칙: 트랜잭션 격리 및 동시성 모델

SQLion은 두 스토리지 엔진 전반에 걸쳐 **Non-Blocking 스냅샷 격리 / 낙관적·Hazard 동시성 제어를 갖춘 Read Committed**를 정규 트랜잭션 처리 불변 규칙으로 정의합니다:

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

SQLion은 **자율적이고 외부 의존성이 전혀 없는 독립 실행형 정적 바이너리**로 설계되었습니다:

1. **아키텍처 원칙**:
   - **풀스택 자체 내장**: 데이터베이스 커널, 다중 프로토콜 와이어 리스너(PGWire :5433, TDS :1433), HTTP/REST 엔진(:8090), 임베디드 웹 스튜디오(`embed.FS`), 고루틴 커넥션 풀러, 크론 작업 스케줄러, SMTP 메일러가 단 하나의 정적 바이너리에서 구동됩니다.
   - **운영 사이드카 제로**: 외부 보조 프로세스나 데몬을 완전히 배제합니다 (PgBouncer 없음, Redis 캐시 계층 없음, Nginx 같은 외부 웹서버 없음, OS crontab 의존성 없음).
2. **영구 명세 불변 규칙**:
   - **외부 분산 코디네이터**: ZooKeeper, etcd, Consul 또는 외부 메시지 브로커(Kafka, RabbitMQ)는 데이터베이스 코어 커널에서 명시적으로 **Spec Out (제외)**됩니다.
   - **배포의 단순성**: 완전히 동작하는 데이터베이스 노드를 배포하는 데에는 단일 실행 파일을 대상 디렉터리(`--data-dir`)에서 실행하는 것 외에 어떠한 설정도 필요하지 않습니다.

---

## 6. 핵심 아키텍처 불변 규칙: 스케일업 우선 & WAL 복제 토폴로지

SQLion은 복잡한 분산 멀티 마스터 샤딩보다 **수직적 스케일업 하드웨어 포화**와 **1-Writer Multi-Reader 비동기/반동기 WAL 복제**를 우선합니다:

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

SQLion은 모든 벤치마크(TPC-C, TPC-H, 마이크로 벤치마크), 테스트 하네스 및 저장 프로시저 워크로드 전반에 걸쳐 **엄격한 원자적 ACID 트랜잭션 모델링**을 강제합니다:

1. **전부 아니면 전무(All-or-Nothing) 트랜잭션 무결성**:
   - 다중 문장 비즈니스 트랜잭션(예: 웨어하우스 조회, 디스트릭트 카운터 증가, 고객 조회, 주문/신규주문 삽입 및 5~10개 주문 항목/재고 갱신으로 구성된 TPC-C New-Order)은 **반드시 단일 원자적 ACID 트랜잭션 경계(`sess.BeginTx()` -> `tx.Commit()`) 내에서 엄격하게 실행**되어야 합니다.
   - 단일 논리 비즈니스 트랜잭션을 여러 자동 커밋(Autocommit) 문장이나 별도의 앰비언트 트랜잭션(예: `exec.Run()` 루프)으로 분할하는 것은 **엄격히 금지**됩니다.
2. **VDBE 바이트코드 VM 트랜잭션 결합**:
   - 벤치마크 하네스에서 실행되는 다단계 저장 프로시저 시퀀스 또는 파티셔닝된 VDBE 바이트코드 프로그램은 명시적 트랜잭션 핸들 `tx`를 공유하는 `exec.RunInTx(prog, params, tx, nil)`를 통해 실행되어야 합니다.
   - 단계 내에서의 트랜잭션 실패, 충돌 중단 또는 롤백은 즉시 `tx.Rollback()`을 트리거하여 부분 쓰기나 불완전한 레코드를 일절 남기지 않아야 합니다.

---

## 8. 엔지니어링 가이드라인 & 개발 파이프라인

### 3단계 검증 파이프라인 (Build ➔ Test ➔ Commit)
모든 수정, 리팩토링 및 기능 추가는 커밋하기 전에 반드시 다음 3단계를 순서대로 통과해야 합니다:

1. **빌드 (Build)**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
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

---

## 9. 개발자 & AI 인수인계 컨텍스트 노트 (`notes/`)
다양한 개발 환경에서 작업하거나 새로운 AI 에이전트 세션을 시작할 때는 과거 설계 논의, 음성적 지식(Spec-Out 사유), 엔지니어링 주의점 및 차기 리팩토링 계획에 대해 `notes/` 디렉터리를 참조하세요:
- [`notes/ENGINEERING_CONTEXT.md`](notes/ENGINEERING_CONTEXT.md): 아키텍처 불변 규칙, 대안 기술 Spec-Out 사유, 협업 엔지니어링 원칙.
- [`notes/PITFALLS_AND_TRICKS.md`](notes/PITFALLS_AND_TRICKS.md): 순환 참조 방지, wiredtiger 빌드 태그, 샌드박스 네트워크 격리, 소수점 JSON 직렬화, VDBE 트랜잭션 결합.
- [`notes/REFACTORING_AND_ROADMAP.md`](notes/REFACTORING_AND_ROADMAP.md): 단일 거대 파일 모듈화 분해 이력, 차주 핵심 과제(VDBE 디스패처 모듈화, SQL 코디네이터 분리), CBO 최적화 백로그.
