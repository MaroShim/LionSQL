# WiredTiger 커서 Zero-Copy 버퍼 재사용 및 Scan 힙 메모리 할당 제거 아키텍처 플랜 (`notes/plans/CURSOR_ZERO_COPY_SCAN_PLAN.ko.md`)

## 1. 개요 및 배경

LionSQL의 테이블 스캔(`SeqScan`, `IndexScan`) 성능을 극대화하기 위해, 스토리지 커서 계층에서 발생하는 치명적인 힙 메모리 할당 병목을 진단하고 해결책을 제시한다.

### 현행 문제점
- `pkg/storage/wiredtiger/wt_engine.go`의 `WTCursor.RawTuple()`:
  ```go
  cSlice := unsafe.Slice((*byte)(pData), int(sz))
  res := make([]byte, len(cSlice)) // <-- 매 행마다 신규 힙 메모리 슬라이스 할당!
  copy(res, cSlice)
  return res, true
  ```
- 10만 건 테이블을 스캔하거나 조인/집계할 때, 매 행마다 `make([]byte)`가 호출되어 **10만 번 이상의 힙 메모리 할당과 GC 가비지(수십 MB)**가 발생한다.
- 1단계에서 구현된 `Storage Projection Pushdown`(`DecodeTupleSelectedWithArena`)은 원시 바이트에서 필요한 컬럼만 추출하여 디코딩을 건너뛰지만, 그 이전 단계인 커서에서 원시 바이트를 전달받을 때 이미 불필요한 힙 할당이 일어나고 있다.

---

## 2. 핵심 최적화 설계

### 2.1 `RawTupleBuffer(dest *[]byte) bool` 인터페이스 확장
- **위치**: `pkg/storage/engine.go` (`RawTupleCursor` 인터페이스)
- **설계**:
  ```go
  type RawTupleCursor interface {
      Cursor
      RawTuple() ([]byte, bool)
      RawTupleBuffer(dest *[]byte) bool
  }
  ```
- 호출자가 제공한 재사용 슬라이스 포인터(`dest *[]byte`)에 원시 바이트를 복사하여 힙 할당 0을 달성.

### 2.2 WiredTiger Cursor Zero-Copy 버퍼 구현
- **위치**: `pkg/storage/wiredtiger/wt_engine.go`
  - `WTCursor.RawTupleBuffer(dest *[]byte) bool`:
    - `pData`의 슬라이스를 `*dest = append((*dest)[:0], cSlice...)`로 복사.
    - `dest` 버퍼의 기존 capacity를 재사용하므로 Go 런타임 힙 할당 0 달성.
  - `wtIndexBTreeCursor.RawTupleBuffer(dest *[]byte) bool`:
    - 인덱스 커서 역시 동일하게 `dest` 버퍼에 PK 레코드 바이트를 복사하여 힙 할당 제거.

### 2.3 `fillScanChunk` 재사용 버퍼 연동
- **위치**: `pkg/execution/scan.go`
- **로직**:
  - `fillScanChunk` 시작 시 `bufPtr := storage.GetTupleBuffer()` / `defer storage.PutTupleBuffer(bufPtr)`로 풀에서 1개의 스크래치 버퍼를 대여.
  - `hasSelection` 경로에서 `rawCur.RawTupleBuffer(bufPtr)`를 호출하여 `*bufPtr`에 원시 바이트를 담고, `DecodeTupleSelectedWithArena(schema, *bufPtr, requiredCols, arena)`로 즉시 소비.
  - 다음 행으로 넘어갈 때 `*bufPtr`가 그대로 재사용되므로, **1,024개 행으로 구성된 청크를 채우는 동안 힙 할당 횟수가 0**이 됨.
- `mockRawCursor` 등 테스트 모의 객체에도 `RawTupleBuffer`를 추가 구현하여 단위 테스트 및 벤치마크 정합성 보장.

---

## 3. 검증 계획 (3-Stage Pipeline per [RULE:PipelineVerification])

1. **Stage 1 (Build)**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Stage 2 (Benchmark & Tests)**:
   - `pkg/execution/scan_bench_test.go`: 100,000건 스캔 벤치마크 메모리 할당 및 지연 시간 측정
   - `go test -v -race ./pkg/storage/wiredtiger/... ./pkg/execution/...`
   - SQLLogicTest 코어 스위트 검증 (`./bin/sqllogictest -dir test/slt/core -parallel 16`)
   - 오라클 불변성 검증 (`bash scripts/verify_no_slt_mutation.sh`)
3. **Stage 3 (Commit & Push)**:
   - Conventional Commits, 데코레이션 이모지 배제 per `[RULE:StyleIntegrity]`.
   - sprint DB 완료 처리 및 `TASKS.md` 동기화 per `[RULE:ContinuousBacklog]`.
