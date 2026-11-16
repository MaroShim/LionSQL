# LionSQL 단독 아웃박스 워커 및 CDC 스트리밍 아키텍처 설계서 (`STANDALONE_OUTBOX_WORKER_CDC_STREAMING_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-16
> - **문서 버전**: v1.0
> - **상태**: 승인 대기 (Draft / Plan Proposed)
> - **대상 릴리즈**: LionSQL `v0.94.0` (또는 차기 마이너 릴리즈)
> - **관련 표준 규약**: `[RULE:PlanPersistence]`, `[RULE:RadicalKISS]`, `[RULE:ZeroOrphan]`, `[RULE:PlatformPrimitives]`, `doc/ADR.md` (ADR-024, ADR-026), `GEMINI.md` (Invariant 4, Invariant 21)

---

## 1. 배경 및 아키텍처 비전

LionSQL은 단일 정적 실행 파일(`bin/lionsql`) 하나로 동작하는 초고동시성 OLTP 데이터베이스 엔진입니다.

현재 엔진 내부(`cmd/lionsql/server.go`, `pkg/cdc/outbox.go`)에는 DB 변경 사항을 외부 브로커(Kafka REST, RabbitMQ, Webhook)로 직접 HTTP POST 전송하는 **인커널 트랜잭셔널 아웃박스 디스패처**가 탑재되어 있습니다. 

그러나 외부 분산 브로커의 네트워크 지연, 재시도 루프, 장애 대기 로직이 데이터베이스 커널과 동일한 메모리 공간(In-Process)에 상주함에 따라, **서브 마이크로초 OLTP 엔진의 본질적 단순성과 무결점 셧다운(`[RULE:CleanTeardown]`, `[RULE:ZeroOrphan]`)을 위협하는 구조적 부채**가 발생하고 있습니다.

본 계획서는 **"단일 바이너리 자족성(Invariant 4)"**을 100% 계승하면서도, 외부 브로커 전송 로직을 커널에서 완전히 덜어내어 **독립 프로세스 서브커맨드(`lionsql outbox-worker`)로 분리**하고, 프로세스 간 통신은 **OS 커널 메모리 버퍼를 통과하는 고성능 비차단 CDC 스트리밍 소켓(방식 A)**으로 결합하는 차세대 하이브리드 서브시스템 아키텍처를 정의합니다.

---

## 2. 왜 DatabaseMail(방식 B)과 Outbox(방식 A)는 서로 다른 통신 방식을 취하는가?

LionSQL의 서브시스템 독립 프로세스화 전략에서 `DatabaseMail`과 `Outbox`는 서로 다른 통신 채널을 채택하며, 이는 각 도메인의 본질적 특성 차이에 기인합니다.

```
┌───────────────────────────────────────────────────────────────────────────────────────┐
│                        하이브리드 서브시스템 통신 방식 대조                           │
├───────────────────────────────┬───────────────────────────────────────────────────────┤
│ 구분                          │ DatabaseMail (방식 B: SQL 드라이버 폴링)              │ Outbox / CDC (방식 A: CDC 스트리밍 소켓)              │
├───────────────────────────────┼───────────────────────────────────────────────────────┤
│ 통신 채널                     │ 표준 PGWire SQL 연결 (:5433)                          │ 전용 비차단 CDC 스트림 소켓 (UDS or HTTP SSE/Raw)      │
│ 상호작용 방식                 │ 양방향 상태 머신 (Unsent ➔ Sending ➔ Sent/Failed)     │ 단방향 고속 순차 스트림 (Push Stream, Tail-Log)       │
│ 트래픽 규모 (Throughput)      │ 초당 수 건 ~ 수십 건 이하 (Low-Frequency)             │ 초당 수천 ~ 수만 건 이상 (High-Frequency OLTP)         │
│ 상태 갱신 필요성              │ 필수 (UPDATE sysmail_mailitems SET sent_status=...)    │ 불필요 (소켓 ACK 기반 LSN 체크포인트 전진)            │
│ 외부 시스템 I/O 지연시간      │ 100ms ~ 수초 (SMTP 프로토콜 핸드셰이크)               │ 수 밀리초 ~ 수십 밀리초 (Kafka/RabbitMQ 큐잉)          │
│ 적합성 이유                   │ 어차피 결과 기록을 위해 SQL DML이 필수이므로 단순함    │ 매 쿼리 폴링/삭제는 DB 부하 유발, 스트리밍이 필수적   │
└───────────────────────────────┴───────────────────────────────────────────────────────┘
```

### 1) DatabaseMail이 방식 B(SQL 연결/폴링)를 채택한 이유
- **Microsoft SQL Server 공식 카탈로그 표준 준수**: `sp_send_dbmail`은 `msdb.dbo.sysmail_mailitems` 테이블에 의존하며, 발송 결과(`sent_status`, `send_request_date`) 및 에러 메시지를 `sysmail_log` 테이블에 직접 SQL DML로 기록해야 합니다.
- **저빈도 I/O**: 메일 발송은 사용자 알림, 일일 리포트 등 발생 빈도가 낮아 1초 주기의 폴링이 DB 커널에 미치는 부하가 0%에 수렴합니다.

### 2) Outbox가 방식 A(CDC 스트리밍 소켓)를 채택해야 하는 이유
- **초고빈도 이벤트 스트리밍**: 실시간 주문, 결제, 클릭 스트림은 초당 수만 건에 달합니다. 이를 테이블에 쌓고 워커가 매번 `SELECT ... FOR UPDATE`로 폴링하고 `DELETE`하면 DB의 I/O와 락 경합이 폭발합니다.
- **단방향 Append-Only 특성**: 아웃박스 이벤트는 소비된 후 DB 테이블을 다시 `UPDATE`할 필요 없이, LSN 체크포인트만 전진시키면 되므로 **소켓 기반 Push 파이프라인**이 가장 가볍고 빠릅니다.

---

## 3. 시스템 아키텍처 및 통신 프로토콜 설계 (방식 A)

```
 [Application] ─── SQL (INSERT/UPDATE/DELETE) ───┐
                                                 ▼
┌────────────────────────────────────────────────────────────────────────┐
│  LionSQL DB 커널 프로세스 (lionsql server)                             │
│  - 초고속 ACID 트랜잭션 커밋 및 WAL 기록                              │
│  - 인메모리 OCC / WiredTiger B+Tree 핫패스 유지                        │
│  - 외부 HTTP / Kafka 클라이언트 코드 0줄 (완전 제거)                  │
│  - CDC Streamer 엔드포인트 노출:                                       │
│    * Unix Domain Socket: unix:///tmp/lionsql_cdc.sock                  │
│    * Loopback TCP      : http://127.0.0.1:8090/api/cdc/stream          │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ 
                                    │ (Non-blocking Binary CDC Event Stream)
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│  LionSQL 아웃박스 워커 프로세스 (lionsql outbox-worker)                 │
│  - 단일 정적 바이너리 내부 서브커맨드로 구동 (추가 도구 설치 0)        │
│  - 소켓으로부터 ChangeEvent 바이너리 프레임 수신                       │
│  - ADR-026 송신자 무유실 계약(At-Least-Once & FIFO Backpressure) 전담  │
│  - 외부 브로커 재시도, 지수 백오프, HTTP 통신 격리 관리                │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
               ┌────────────────────┼────────────────────┐
               ▼                    ▼                    ▼
     [ Apache Kafka ]      [ RabbitMQ REST ]      [ Webhook URL ]
```

### 1) IPC 통신 채널: UDS 및 Loopback TCP
- **로컬 기본 통신**: `unix:///tmp/lionsql_cdc.sock` (Unix 도메인 소켓)
  - 네트워크 드라이버 및 패킷 캡슐화를 우회하고 **OS 커널 메모리 버퍼(`sk_buff`)** 복사로 동작하여 지연시간 5마이크로초 이하 달성.
- **원격/컨테이너 분리 통신**: `http://<db_host>:8090/api/cdc/stream` (Loopback/Remote TCP)
  - 동일 호스트뿐 아니라 워커를 별도 노드로 분리 배포 시에도 즉시 활용 가능.

### 2) 스트리밍 프로토콜 프레임 명세
CDC 소켓 파이프를 통해 전달되는 프레임은 고속 바이너리 인코딩을 적용합니다:
```
[4-Byte FrameLen (BigEndian)] + [8-Byte CommitLSN] + [1-Byte OpCode] + [Payload (JSON/MsgPack)]
```
- 워커는 프레임을 읽어 Kafka/RabbitMQ/Webhook으로 전송 성공 시, 소켓을 통해 `ACK <CommitLSN>`을 반환하여 LSN 체크포인트를 상호 동기화합니다.

---

## 4. 워커 CLI 인터페이스 및 서브커맨드 명세

기존 `server.go`에 흩어져 있던 8개의 아웃박스 플래그를 정돈하여 전용 서브커맨드로 일원화합니다.

### 명령어 문법
```bash
lionsql outbox-worker [options]
```

### 주요 CLI 플래그
- `-stream-url`: DB 서버의 CDC 스트림 소켓 경로 (기본값: `unix:///tmp/lionsql_cdc.sock` 또는 `http://127.0.0.1:8090/api/cdc/stream`)
- `-token`: DB 서버 연결 인증 토큰 (Cluster Token or JWT)
- `-table`: 모니터링 대상 테이블 필터 (기본값: `""`, 전체)
- `-topic-prefix`: 발행 토픽 접두사 (기본값: `lionsql.`)
- `-kafka`: Kafka REST Proxy 엔드포인트 URL
- `-rabbitmq`: RabbitMQ Management API 엔드포인트 URL
- `-webhook`: 대상 Webhook HTTP URL
- `-batch-size`: 브로커 전송 마이크로배치 크기 (기본값: `100`)
- `-flush-interval-ms`: 버퍼 플러시 최대 주기 (기본값: `200ms`)

---

## 5. 무유실(Zero-Loss) 및 장애 복구 메커니즘 (ADR-026 계승)

독립 프로세스로 분리되더라도 **ADR-026**의 핵심 철학인 **"송신자 무유실 책무 및 백프레셔"**는 불변식으로 계승됩니다.

1. **외부 브로커 다운 시 자연스러운 백프레셔**:
   - Kafka나 Webhook이 응답하지 않으면, `outbox-worker`는 소켓의 다음 이벤트를 읽지 않고 대기하며 지수 백오프로 재시도합니다.
   - 워커가 소켓 버퍼를 읽지 않으므로, DB 커널의 스트리밍 채널 버퍼가 차오르고, 상류로 무손실 백프레셔가 전달됩니다.
2. **워커 프로세스 비정상 크래시 시 복구**:
   - 워커가 죽더라도 마지막으로 브로커에 전송 완료된 `lastAckLSN`은 DB 서버의 체크포인트 파일(`outbox.checkpoint`)에 영구 동기화되어 있습니다.
   - 워커가 재기동되면 소켓 연결 시 `START_STREAM LSN=<lastAckLSN>`을 요청하여, 누락 없이 정확히 중단된 지점부터 스트리밍을 재개합니다 (At-Least-Once 보장).

---

## 6. 단계별 마이그레이션 및 구현 로드맵

### Phase 1: CDC 스트리밍 소켓 SPI 표준화 (`pkg/cdc/`)
- Unix 도메인 소켓 및 HTTP 스트림 리스너 추상화 (`StreamListener`).
- LSN 양방향 핸드셰이크 및 커밋 ACK 프로토콜 구현.

### Phase 2: `outbox-worker` 서브커맨드 구현 (`cmd/lionsql/cli_outbox.go`)
- `pkg/cdc/outbox.go` 디스패처 엔진을 커널에서 워커 CLI 패키지로 이전.
- OS 시그널(`SIGTERM`, `SIGINT`) 기반의 우아한 셧다운 핸들러 구현 (`[RULE:CleanTeardown]`).

### Phase 3: DB 커널(`cmd/lionsql/server.go`) 아웃박스 레거시 제거
- `server.go` 내부의 `outboxDispatcher`, `outboxKafka`, `outboxWebhook` 등 인프로세스 실행 코드 완전 삭제.
- 서버 종료 시 워커 누수 문제(`AUDIT-093-022`) 근본적 종식.

### Phase 4: 엔드투엔드(E2E) 회귀 테스트 및 성능 검증
- 10만 건 이벤트 부하 시 DB 커널 지연시간 측정 (인프로세스 대비 p99 20% 이상 향상 실측).
- 워커 강제 종료(`kill -9`) 및 Kafka 다운 카오스 엔지니어링 무손실 복구 검증.

---

## 7. 기대 효과

1. **커널 순도 100% 회복**: 외부 네트워크 호출 및 HTTP 재시도 루프가 커널에서 영구 제거되어, DB는 오직 ACID와 트랜잭션 처리에만 모든 CPU/메모리를 집중.
2. **단일 바이너리(Invariant 4) 유지**: 새로운 서드파티 소프트웨어 도입 없이 `bin/lionsql` 바이너리 하나로 서버와 아웃박스 워커를 모두 배포.
3. **독립적인 수평 확장(Scale-Out)**: 대규모 트래픽 발생 시 아웃박스 워커만 별도 컨테이너/머신으로 분리하여 네트워크 대역폭 분리 가능.
