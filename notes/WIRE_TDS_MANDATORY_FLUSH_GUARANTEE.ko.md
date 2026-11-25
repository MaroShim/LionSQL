# Wire 프로토콜 (PGWire & TDS) 버퍼링 I/O Flush 보장 및 스트림 생명주기 규격

## 1. 개요 및 배경
LionSQL은 높은 네트워크 처리량과 GC 압력 최소화를 위해 PostgreSQL Wire v3(`:5433`) 및 Microsoft SQL Server TDS 7.x/8.0(`:1433`) 프로토콜 계층에서 `sync.Pool` 기반 버퍼링 I/O(`bufio.Writer`, `tdsStreamWriter`)를 활용합니다.

그러나 버퍼링 I/O 환경에서 쿼리 완료 신호(`CommandComplete`, `ReadyForQuery`, `DONE` 토큰)가 소켓 버퍼에 잔류하고 즉시 전송되지 않으면, 클라이언트(드라이버, ORM, 커넥션 풀러)는 응답 수신 대기 상태로 블로킹되어 타임아웃 또는 커넥션 고갈 장애를 겪게 됩니다.

본 문서는 이러한 버퍼 고립 문제를 원천 차단하기 위해 수립된 **PGWire 및 TDS의 무조건적 Flush 보장(Mandatory Flush Guarantee)**과 **결정론적 스트림 생명주기(Deterministic Stream Lifecycle)** 규격을 정의합니다.

---

## 2. PGWire 무조건적 Flush 인베리언트 (`pkg/wire`)

### 2.1 결함 원인 분석
- `writePgDataRows`는 대용량 결과셋 전송 시 32KB 청크 단위로 버퍼를 채워 소켓에 Flush합니다.
- 그러나 결과 레코드 수가 적거나(예: 1~10건) 32KB 미만의 잔여 레코드가 남아 있는 경우, 버퍼가 가득 차지 않아 Flush가 호출되지 않은 채 리턴될 수 있었습니다.
- 상위 호출자(`runAndWriteQueryWithFormats`, `handleExecute`, `writeExecutionResult`)에서 커넥션 레벨의 버퍼 플러시가 지연되거나 조기 반환(`early return`) 경로에서 누락될 경우 클라이언트에 행(Hang)이 발생하는 취약점이 존재했습니다.

### 2.2 해결 및 설계 원칙

#### 원칙 1: `writePgDataRows` 잔여 청크 즉시 플러시
`pkg/wire/row_encode.go`의 `writePgDataRows` 함수는 청크 단위 플러시 루프 종료 후, 32KB 미만의 잔여 데이터가 존재하더라도 라이터가 `flusher` 인터페이스를 구현하고 있다면 즉시 Flush를 실행합니다.

```go
// pkg/wire/row_encode.go
if len(buf) >= pgChunkFlushSize {
    // 32KB 도달 시 중간 플러시
    ...
} else {
    // 잔여 데이터 즉시 플러시 보장
    if f, ok := w.(flusher); ok {
        writeErr = f.Flush()
    }
}
```

#### 원칙 2: 모든 버퍼링 라이터 스코프에 `defer Flush()` 의무화 (`[RULE:HandleHygiene]`)
`getBufioWriter(conn)`를 획득하는 모든 실행 경로(`handleExecute`, `writeExecutionResult`, `runAndWriteQueryWithFormats`, `writeQueryResult`)는 버퍼 반환(`defer putBufioWriter(bw)`)과 함께 **반드시** `defer func() { _ = bw.Flush() }()`를 선언합니다.

```go
bw := getBufioWriter(conn)
defer putBufioWriter(bw)
defer func() { _ = bw.Flush() }() // 무조건적 Flush 보장
```

이를 통해 중간 에러 반환, 조기 탈출, 다중 결과셋 처리 도중에도 소켓에 기록된 모든 메시지가 손실 없이 즉시 클라이언트로 방출됩니다.

#### 원칙 3: 메타데이터 및 완료 태그 기록 직후 명시적 플러시
- `WriteRowDescription` 전송 직후 즉시 `bw.Flush()` 호출.
- `WriteCommandComplete` 전송 직후 및 다중 결과셋 루프 마감 직후 최종 `bw.Flush()` 호출.

---

## 3. TDS 패킷 스트림 생명주기 및 인크리멘탈 Flush 규격 (`pkg/wire/tds`)

### 3.1 결함 원인 분석
- TDS 프로토콜은 4KB 고정 크기 패킷 헤더(`0x04` 등)와 함께 데이터를 청크 단위로 전송합니다.
- `tdsStreamWriter`가 패킷 페이로드를 채우고 `FlushIfReady()`를 호출할 때, 내부 `buf.Truncate`만 수행하고 소켓 레벨의 버퍼링 라이터(`s.bw`)에 Flush를 전달하지 않으면 패킷이 OS 소켓 버퍼로 즉시 넘어가지 않는 문제가 있었습니다.
- RPC 실행(`executeRPCAndRespond`), T-SQL 배치(`executeQueryAndRespond`), 바이트코드 실행(`respondExecutionResult`)에서 스트림 생성 후 에러 발생 시 스트림이 닫히지 않아 패킷 헤더의 `Status = 0x01 (EOM - End of Message)` 비트가 누락될 위험이 있었습니다.

### 3.2 해결 및 설계 원칙

#### 원칙 1: `FlushIfReady()` 내 버퍼 라이터 즉시 동기화
`pkg/wire/tds/row_stream.go`의 `FlushIfReady()`는 4KB TDS 패킷 청크가 채워져 소켓에 기록될 때마다 즉시 하부 `bufio.Writer`의 `Flush()`를 호출하여 패킷이 네트워크로 직행하도록 보장합니다.

```go
// pkg/wire/tds/row_stream.go
func (s *tdsStreamWriter) FlushIfReady() error {
    for s.buf.Len() >= s.maxPayload {
        raw := s.buf.Bytes()
        if err := writePacket(s.w, s.pktType, 0, raw[:s.maxPayload]); err != nil {
            return err
        }
        rem := copy(raw, raw[s.maxPayload:])
        s.buf.Truncate(rem)
        if s.bw != nil {
            if err := s.bw.Flush(); err != nil {
                return err
            }
        }
    }
    return nil
}
```

#### 원칙 2: 스트림 할당 즉시 `defer stream.Close()` 바인딩
TDS 응답을 생성하는 모든 핸들러는 `streamWriter` 생성 직후 `defer func() { _ = stream.Close() }()`를 바인딩하여 함수 종료 시 자동으로 최종 패킷(`EOM 0x01`) 마감 및 소켓 플러시가 수행되도록 격리합니다.

```go
// pkg/wire/tds/server.go
stream := sess.newStreamWriter(conn, PacketTabular)
defer func() { _ = stream.Close() }()
```

이를 통해 패닉(`recover`), SQL 런타임 에러, 클라이언트 조기 단절 상황에서도 불완전한 상태로 스트림이 방치되는 현상을 방지합니다.

---

## 4. 아키텍처 규칙 및 검증 기준

1. **규칙 번호**: `[RULE:HandleHygiene]`, `[RULE:CleanTeardown]`
2. **검증 방법**:
   - `pkg/wire` 및 `pkg/wire/tds` 단위/통합 테스트에서 단일 행 조회, 대용량 스트리밍 조회, 중간 오류 발생 시 클라이언트 소켓의 Read 타임아웃 발생 여부 검증.
   - `TestPGWire_10k_StreamingChunkFlush`: 10,000행 대용량 청크 플러시 검증 통과.
   - `TestTDS_SingleRowAndMultiRowStreaming`: SSMS 및 Go mssql 드라이버의 세션 행 없이 즉시 응답 완료 확인.
