# PGWire 직렬화 버퍼 Zero-Copy 및 Chunk Flush 파이프라인 구축 계획서 (`PGWIRE_ZERO_COPY_SERIALIZATION_AND_CHUNK_FLUSH_PLAN.ko.md`)

## 1. 개요 및 배경
- **목적**: 1만 건(10,000 rows) 이상의 대용량 쿼리 결과셋을 PostgreSQL Wire Protocol(`pgwire`)을 통해 클라이언트로 전송할 때, 직렬화 과정에서의 힙 메모리 할당(Allocations) 및 중간 메모리 복사(Memory Copy)를 원천 제거하고, 청크 단위 즉시 플러시(`Chunk Flush`)를 통해 클라이언트 스트리밍 수신 지연을 최소화한다.
- **관련 규칙**:
  - `[RULE:ZeroAllocation]`: Zero-Allocation Hot Paths (GC Pressure Mitigation)
  - `[RULE:BoundedBuffers]`: Bounded Buffers & Defensive Resource Limits
  - `[RULE:LatencyBudget]`: Responsive Latency & Performance Budget
  - `[RULE:PipelineVerification]`: 3-Stage Pipeline (Build ➔ Test `-race` ➔ Commit)

---

## 2. 현재 구현의 병목 분석

### 2.1 직렬화 폴백 시 과도한 힙 할당
- `appendColumnValueFast`가 `storage.TypeDecimal`, `storage.TypeDateTime`, `time.Time`, `storage.TypeUUID`를 처리하지 못하고 `formatColumnValue`로 폴백함.
- `formatColumnValue`는 매 행/컬럼마다 `v.String()`, `time.Format()`, `[]byte(...)`를 생성하여 1만 건 기준 수만~십만 회의 단기 힙 객체 할당 및 GC 압력을 유발함.

### 2.2 이중 버퍼링 및 메모리 복사
- `writePgDataRows`는 내부적으로 `pgChunkBufPool`에서 가져온 16KB 버퍼(`buf`)에 행들을 직렬화한 후 `w.Write(buf)`를 호출함.
- 하지만 `w`는 32KB 크기의 `*bufio.Writer`(`bw`)이므로, `buf` 크기(16KB)가 `bw.Available()`(32KB)보다 작아 `bufio.Writer`의 내부 버퍼로 `copy(bw.buf, buf)` 메모리 복사가 매 청크마다 발생함.

### 2.3 청크 플러시(Chunk Flush) 누락
- `writePgDataRows`에서 16KB/512행 단위로 `w.Write(buf)`를 호출하지만, `w`가 `flusher`(`interface{ Flush() error }`) 인터페이스를 구현하고 있어도 `Flush()`를 호출하지 않음.
- 이로 인해 1만 건 전체 쿼리가 끝날 때까지 데이터가 네트워크 소켓으로 즉시 방출되지 않고 버퍼에 고여 클라이언트에서 스트리밍 효과를 누리지 못함.

---

## 3. 해결 설계 및 구현 방안

### 3.1 완전한 Zero-Copy 컬럼 포맷터 확장 (`appendColumnValueFast`)
1. **`storage.TypeDateTime` & `time.Time`**:
   - `time.Time.AppendFormat(dst, "2006-01-02 15:04:05.999999")` (TIMESTAMPTZ의 경우 `-07` 추가)를 사용하여 `dst` 슬라이스에 직접 무할당 직렬화.
2. **`storage.TypeDecimal`**:
   - `v.SVal`이 비어있지 않은 경우 `append(dst, v.SVal...)`로 무할당 추가.
   - `v.DVal`의 경우 사전 포맷된 문자열 직결.
3. **`storage.TypeUUID`**:
   - `v.SVal`을 `dst`에 직접 바이트 복사.
4. **`storage.TypeBinary` 및 BYTEA (`oid == 17`)**:
   - `\x` 프리픽스 추가 후 `hex.Encode`를 `dst` 확장 영역에 직접 수행하여 중간 문자열/바이트 슬라이스 생성 제거.

### 3.2 청크 버퍼 크기 최적화 및 풀링
1. **버퍼 크기 상향**:
   - `pgStreamFlushBytes = 32 * 1024` (32KB) 및 `pgStreamFlushRows = 512`.
   - `pgChunkBufPool` 초기 용량을 64KB로 설정하여 1만 건 직렬화 시 슬라이스 재할당 방지.
2. **Zero-Copy 소켓 직결**:
   - `writePgDataRows` 진입 전 `bw.Flush()`로 이전 `RowDescription` 헤더 방출 및 `bw.Buffered() == 0` 확보.
   - 32KB 청크 단위 직렬화 완료 시 `w.Write(buf)` 호출 후 `if f, ok := w.(flusher); ok { _ = f.Flush() }` 호출로 즉시 TCP 소켓 방출.

### 3.3 검증 및 벤치마크
1. **패리티 및 무할당 단위 테스트 (`pkg/wire/row_encode_test.go`)**:
   - `storage.TypeDecimal`, `storage.TypeDateTime`, `time.Time`, `storage.TypeUUID`, `BYTEA` 포함 10,000행 직렬화 패리티 및 0-Alloc 검증.
2. **청크 플러시 검증 테스트**:
   - `mockFlusherWriter`를 통해 10,000행 전송 시 주기적인 `Flush()` 호출 횟수 및 청크 크기 정합성 검증.
3. **벤치마크**:
   - `BenchmarkPGWire_ZeroAllocStream_10k`: 10,000행 직렬화 시간 및 메모리 할당 측정.

---

## 4. 실행 계획
1. `sprint_tasks`에 `PERF-PGWIRE-ZERO-COPY-CHUNK-FLUSH-1` 등록 및 상태 `IN_PROGRESS` 설정.
2. `pkg/wire/row_encode.go` 리팩토링: 무할당 타입 확장 및 `flusher` 인터페이스 연동 청크 플러시 구현.
3. `pkg/wire/server.go` 연동: `RowDescription` 전송 후 스트리밍 전 `bw.Flush()` 선제 호출 확인.
4. `pkg/wire/row_encode_test.go`에 1만 건 스트리밍 검증 및 벤치마크 추가.
5. 3단계 파이프라인 검증 (Build ➔ Test `-race` ➔ Commit) 및 sprint task 완료 처리.
