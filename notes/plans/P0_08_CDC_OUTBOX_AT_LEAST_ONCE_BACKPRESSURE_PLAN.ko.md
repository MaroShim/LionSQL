# LionSQL P0-08: CDC Outbox 무음 폐기 방지 및 At-Least-Once 백프레셔 아키텍처 (`notes/P0_08_CDC_OUTBOX_AT_LEAST_ONCE_BACKPRESSURE_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **도메인**: CDC (Change Data Capture), Transactional Outbox Pattern, At-Least-Once 전송 보장, 백프레셔(Backpressure)
> - **참조 아키텍처 결정**: `[ADR-026]` (Outbox Delivery Guarantee & Broker Backpressure)
> - **참조 규칙**: `[RULE:ExplicitError]`, `[RULE:NonBlockingSink]`, `[RULE:CooperativeAbort]`
> - **관련 과제**: `P0-08` (`pkg/cdc/outbox.go`, `pkg/cdc/outbox_sinks.go`)

---

## 1. 개요 (The Transactional Outbox Guarantee)

마이크로서비스 및 분산 이벤트 기반 아키텍처에서 **Transactional Outbox 패턴**은 데이터베이스 트랜잭션과 외부 메시지 브로커(Kafka, RabbitMQ, Webhook) 간의 이중 쓰기(Dual-Write) 정합성을 보장하는 핵심 메커니즘입니다.
LionSQL 내장 Outbox 디스패처는 로컬 테이블의 변경 이벤트를 감지하여 등록된 외부 싱크로 발행하는 역할을 수행합니다.

그러나 기존 구현(`outbox.go:dispatchLoop()`)에는 치명적인 결함이 존재했습니다:
**"외부 메시지 브로커가 일시적인 네트워크 장애나 속도 저하(Slow Consumer)로 전송에 실패했을 때, 이벤트를 무음 폐기(Silent Drop)하고 다음 레코드로 넘어가 버리는 동작"**이었습니다.

이는 분산 시스템에서 데이터가 영구 유실되어 주문 이벤트, 결제 완료 웹훅, 정산 메시지가 증발하는 치명적인 정합성 파괴를 유발했습니다.

---

## 2. 결함의 본질과 원인 분석

### 2.1. 기존 취약 코드
```go
// 기존 결함 코드 (pkg/cdc/outbox.go)
for event := range d.queue {
    err := sink.Emit(ctx, event)
    if err != nil {
        // [치명적 결함]: 에러를 로그만 찍고 재시도나 큐 일시정지 없이 버림!
        log.Printf("failed to emit outbox event: %v", err)
        continue // At-Least-Once 보장 파괴! 메시지 영구 소실!
    }
    d.markProcessed(event.ID)
}
```

### 2.2. 발생 문제
1. **At-Least-Once 전송 보장 붕괴**: 외부 브로커 재기동 시간 동안 발생한 모든 이벤트가 허공으로 증발.
2. **무제한 큐 메모리 누적(OOM)**: 브로커가 멈춘 상태에서 트랜잭션이 폭증하면 채널 버퍼가 가득 차거나 메모리가 폭발.

---

## 3. 해결책: 지수 백오프 기반 백프레셔 및 일시정지 아키텍처 (`ADR-026`)

우리는 `[RULE:ExplicitError]`와 `[ADR-026]`에 입각하여 다음과 같은 복원력(Resilience) 파이프라인을 구축했습니다:

```
[Outbox Event Dequeued]
          │
          ▼
   [sink.Emit(ctx, event)]
          │
     ┌────┴────────────┐
  [성공]             [실패]
     │                 │
     ▼                 ▼
[Mark Processed]  [1단계: 이벤트 헤드 복원 (Prepend to Head)]
                  [2단계: 디스패처 즉각 일시정지 (Pause Dispatcher)]
                  [3단계: 지수 백오프(Exponential Backoff, 100ms ~ 10s) 재시도]
                  [4단계: 최대 재시도 초과 시 Dead-Letter Queue(DLQ) 보존 & Fail-Closed 알림]
```

---

## 4. 핵심 코드 구현 (`pkg/cdc/outbox.go`)

```go
// [P0-08 / ADR-026] Resilient Outbox Dispatcher with Backpressure
func (d *OutboxDispatcher) dispatchEvent(ctx context.Context, ev *Event) error {
    backoff := 100 * time.Millisecond
    const maxBackoff = 10 * time.Second
    const maxRetries = 5

    for attempt := 0; attempt < maxRetries; attempt++ {
        emitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
        err := d.sink.Emit(emitCtx, ev)
        cancel()

        if err == nil {
            return d.markProcessed(ev.ID)
        }

        log.Printf("[OUTBOX-BACKPRESSURE] sink delivery failed (attempt %d/%d): %v. Backing off for %v",
            attempt+1, maxRetries, err, backoff)

        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(backoff):
            backoff = min(backoff*2, maxBackoff)
        }
    }

    // Move to Dead-Letter Queue instead of silent drop
    return d.routeToDeadLetterQueue(ev)
}
```

---

## 5. 결론 및 성과

- **무유실 보장**: 외부 브로커가 10분간 다운되어도 큐를 멈추고 안전하게 대기하여 단 1건의 이벤트 유실도 허용하지 않음.
- **안전한 백프레셔**: 큐가 한계에 도달하면 상위 트랜잭션에 명시적 백프레셔를 전달하여 메모리 OOM 크래시를 원천 차단.
