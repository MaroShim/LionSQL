# LionSQL 마이크로 아키텍처 제로 할당 6대 핫패스 튜닝 계획서 (`MICRO_HOTPATH_ZERO_ALLOC_TUNING_PLAN.ko.md`)

> - **작성일**: 2026-11-12
> - **상태**: **Post-1.0 Candidate Specification (`v1.10.0` 온디맨드 후보 과제로 전수 이관 — 1.0-GA 코드 동결 및 안정성 최우선 원칙 준수)**
> - **관련 태스크 ID (`v1.10.0` `PENDING`)**:
>   - **1순위**: `PERF-VDBE-FRAME-SLIM-1` (VDBE 프레임/레지스터 슬림 초기화 및 포지셔널 파라미터 슬라이스 직결)
>   - **2순위**: `PERF-WAL-ZERO-ALLOC-1` (In-Memory OCC WAL 단일 풀 버퍼 직렬화 및 `writeFrame` Zero-Alloc)
>   - **3순위**: `PERF-WT-ROW-DIRECT-1` (WiredTiger `InsertRow`/`Update` 중간 `Record` 맵 2중 할당 우회 및 `EncodeRowToBuffer` 직결)
>   - **4순위**: `PERF-RESULT-ROW-STREAM-1` (쿼리 결과셋 `Rows []map[string]any` 이중 구체화 제거 및 `[]storage.Row` Zero-Boxing 스트리밍)
>   - **5순위**: `PERF-TDS-ZEROCOPY-FRAME-1` (TDS `tdsStreamWriter` 8바이트 헤더 예약 인플레이스 직렬화 및 슬라이딩 `copy` 제거)
>   - **6순위**: `PERF-CACHE-SHARD-1` (`PlanCache` 및 세션 레지스트리 32-Shard 분할을 통한 멀티코어 `RLock` 캐시라인 바운싱 제거)

---

## 1. 배경 및 목적 (Context & Motivation)

LionSQL은 v0.92.0까지 튜플 바이너리 코덱(V2), Pointer-Free FlatRecord(`MemoryCompact`), 256 캐시라인 패딩 샤드, 16-Byte VDBE 바이트코드 4-Way 언롤링, WiredTiger `transaction_sync` 해체, PGWire/TDS 16KB 청크 스트리밍 등 거시적 아키텍처 병목(Macro-Bottlenecks)을 모두 해결했습니다.

그러나 코드베이스의 실행 핫패스(Hot-Path)를 정밀 프로파일링한 결과, **기존 아키텍처 불변식을 전혀 변경하지 않고도 힙 할당(Heap Allocation), 메모리 복사(`copy`), 인터페이스 박싱(Boxing), 멀티코어 캐시라인 바운싱을 추가로 15~40% 절감할 수 있는 6대 미시적(Micro-Architectural) 튜닝 포인트**가 발굴되었습니다.

본 계획서는 6대 과제를 정식 엔지니어링 후보 과제로 등재하고, 그중 **난이도가 낮고 회귀 위험 없이 즉각적인 TPS/GC 개선 효과를 내는 1~3순위 과제의 상세 구현 명세**와 **4~6순위 후보 과제의 설계 지침**을 정의합니다.

---

## 2. 6대 마이크로 핫패스 튜닝 매트릭스 (Priority & ROI Matrix)

| 순위 | 태스크 ID | 대상 서브시스템 | 핵심 병목 진단 | 기대 효과 (ROI) | 난이도 | 추진 구분 |
|:---:|---|---|---|---|:---:|:---:|
| **1순위** | **`PERF-VDBE-FRAME-SLIM-1`** | `pkg/proc/runtime`, `pkg/wire` | `newFrame()`에서 매번 최소 64개 레지스터(3KB) 루프 초기화 + PGWire/TDS Prepared 실행 시 `paramMap` 3~5중 해시 삽입 | 프로시저/Prepared 호출당 맵 해싱 0회, 프레임 초기화 비용 75% 절감 | ★☆☆ | **즉시 착수 (Active)** |
| **2순위** | **`PERF-WAL-ZERO-ALLOC-1`** | `pkg/storage/memengine` | `WALWriter.writeFrame`에서 `MarshalMsgPack` 슬라이스 할당 후 `make([]byte, 4+len)` 2차 할당 및 전체 `copy` | 쓰기 트랜잭션 커밋당 힙 할당 2회 ➔ 0회 (Zero-Alloc WAL) | ★☆☆ | **즉시 착수 (Active)** |
| **3순위** | **`PERF-WT-ROW-DIRECT-1`** | `pkg/storage/wiredtiger`, `pkg/storage` | `WTTx.InsertRow`가 포지셔널 `Row`를 받아 `RowToRecord` 맵 생성 후 `insertLocked`에서 또 `normalized` 맵을 생성 | WiredTiger `INSERT` 시 행당 `map[string]Value` 할당 2회 완전 제거 | ★★☆ | **즉시 착수 (Active)** |
| **4순위** | **`PERF-RESULT-ROW-STREAM-1`** | `pkg/proc/runtime`, `pkg/wire`, `pkg/wire/tds` | `ExecutionResult`가 와이어에서 쓰이지도 않는 `Rows []map[string]any`와 `Values [][]any`를 동시 생성 및 `any` 박싱 | `SELECT` 다건 조회 처리량 30~50% 향상 및 GC 압력 대폭 감소 | ★★☆ | **후보 과제 (Candidate)** |
| **5순위** | **`PERF-TDS-ZEROCOPY-FRAME-1`** | `pkg/wire/tds` | `tdsStreamWriter`가 `bytes.Buffer`에 쓴 뒤 `frame` 버퍼로 재복사하고 `FlushIfReady`에서 잔여 바이트를 슬라이딩 복사 | TDS 대량 결과셋 패킷 프레이밍 시 메모리 `copy` 0회 달성 | ★★☆ | **후보 과제 (Candidate)** |
| **6순위** | **`PERF-CACHE-SHARD-1`** | `pkg/proc/runtime`, `pkg/api` | 고동시성(64코어+) 환경에서 단일 `PlanCache.mu.RLock()`의 `readerCount` 원자적 카운터 캐시라인 바운싱 | 다중 코어 동시 쿼리 디스패치 시 L1 캐시 무효화 제거 | ★★☆ | **후보 과제 (Candidate)** |

---

## 3. 즉시 착수 과제 (1~3순위) 상세 설계 명세

### 3.1. [1순위] `PERF-VDBE-FRAME-SLIM-1`: VDBE 프레임 슬림 초기화 및 파라미터 슬라이스 직결

1. **현상 및 문제점**:
   - [`pkg/proc/runtime/vm.go`](file:///Users/maro/Projects/SQLion/pkg/proc/runtime/vm.go#L741-L820): `resetForPool()`에서 이미 `clear(f.regs[:cap(f.regs)])`로 전체 레지스터 배열을 제로화하고 있음에도, `newFrame()`에서 `numRegs < 64` 강제 하한선과 함께 `for i := range f.regs { f.regs[i] = storage.NullValue(storage.TypeUnknown) }` 루프를 매번 실행함.
   - 단 4개의 레지스터만 쓰는 포인트 쿼리도 매 실행마다 64개(`64 * 48B = 3,072B`)의 구조체를 순회하며 덮어씀.
   - 또한 [`pkg/wire/server.go`](file:///Users/maro/Projects/SQLion/pkg/wire/server.go#L1658-L1682) (`handleExecute`)에서 PGWire Extended Query 실행 시 파라미터마다 `paramMap["p1"]`, `paramMap["@p1"]`, `paramMap["$1"]`, `paramMap[targetProcName]`, `paramMap[clean]` 등 최대 6번의 `map[string]any` 해시 테이블 삽입을 수행함.
2. **해결 설계**:
   - **Exact-Size Register Slicing**: `newFrame(prog, tx)`에서 `numRegs := int(prog.NumRegisters) + 4` (최소 8)로 실제 프로그램이 요구하는 레지스터 개수만큼만 슬라이싱하고, 필요한 범위(`f.regs[:numRegs]`, `f.slots[:numSlots]`)만 초기화.
   - **Prepared Statement Pre-Resolved Slot Mapping**: `Statement` 컴파일 시점에 각 포지셔널 파라미터(`$1`, `$2`, `@p1`)가 매핑되는 `prog.VarSlots` 인덱스를 `stmt.ParamSlotIndices []int32`로 사전 계산(Pre-resolve)하거나, `bindParams`에서 불필요한 중복 키(`p1`, `@p1`, `$1` 3중 삽입) 대신 단일 정규화 키 또는 포지셔널 바인딩으로 단축.

---

### 3.2. [2순위] `PERF-WAL-ZERO-ALLOC-1`: In-Memory OCC WAL 단일 풀 버퍼 직렬화

1. **현상 및 문제점**:
   - [`pkg/storage/memengine/wal.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/wal.go#L163-L192) (`WALWriter.writeFrame`):
     ```go
     payload, err := MarshalMsgPack(rec)          // 1차 힙 할당
     frame := make([]byte, 4+len(payload))        // 2차 힙 할당
     binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
     copy(frame[4:], payload)                     // 전체 바이트 복사
     ```
   - 매 트랜잭션 커밋마다 2번의 `[]byte` 힙 할당과 1번의 전체 메모리 복사가 발생하여 고동시성 쓰기 워크로드에서 불필요한 GC 할당률을 유발함.
2. **해결 설계**:
   - `pkg/storage/memengine/msgpack.go`에 기존 버퍼 뒤에 직접 직렬화하는 `AppendMsgPackFrame(dst []byte, rec WALRecord) ([]byte, error)` 및 `walFrameBufPool` (`sync.Pool`, 기본 4KB, 최대 반환 상한 256KB `[RULE:BoundedBuffers]`)을 도입.
   - 풀에서 꺼낸 슬라이스의 첫 4바이트(`[0:4]`)를 길이 헤더 자리로 비워두고 `[4:]` 위치에 곧바로 MessagePack 인코더가 쓰도록 한 뒤, 완료 시 `binary.BigEndian.PutUint32(buf[:4], uint32(len(buf)-4))`를 채워 `w.file.Write(buf)`에 직결.
   - `Write` 직후 버퍼를 즉시 풀에 반환하여 **트랜잭션 커밋당 WAL 직렬화 힙 할당을 0 B/op, 0 allocs/op으로 제거**.

---

### 3.3. [3순위] `PERF-WT-ROW-DIRECT-1`: WiredTiger `InsertRow` 맵 할당 우회 및 직접 인코딩

1. **현상 및 문제점**:
   - [`pkg/storage/wiredtiger/wt_engine.go`](file:///Users/maro/Projects/SQLion/pkg/storage/wiredtiger/wt_engine.go#L1708-L1744) (`WTTx.InsertRow` ➔ `insertLocked`):
   - VDBE 바이트코드 엔진(`OpInsertRow`)이 이미 스키마 컬럼 순서에 맞춘 `storage.Row` (`[]storage.Value`) 슬라이스를 전달했음에도, `InsertRow`가 `rec := tbl.RowToRecord(row)`를 호출해 1차 `map[string]Value`를 할당하고, `insertLocked` 진입 직후 `normalized := make(storage.Record, len(record))`로 **2차 `map[string]Value`를 또 할당**한 뒤, 정작 직렬화할 때는 다시 컬럼 배열을 순회하며 `EncodeTupleToBuffer`를 호출함.
2. **해결 설계**:
   - `storage.EncodeRowToBuffer(schema *TableSchema, row Row, buf []byte)`를 사용해 포지셔널 `storage.Row`에서 V2 바이너리 튜플을 직접 직렬화.
   - `WTTx.insertRowLocked(tableName string, tbl *storage.TableSchema, row storage.Row)` 전용 패스트패스를 구현하여 PK 추출(`row[tbl.PKIndex()]`), FK/Unique 인덱스 검사, V2 바이너리 튜플 직렬화 전 과정에서 **중간 `storage.Record` 맵 생성 2회를 완전히 제거**.

---

## 4. 온디맨드 후보 과제 (4~6순위) 설계 지침

### 4.1. [4순위] `PERF-RESULT-ROW-STREAM-1`: 결과셋 `[]storage.Row` Zero-Boxing 직결 스트리밍
- **목표**: `SELECT` 실행 시 `Rows []map[string]any`와 `Values [][]any`를 중복 생성하는 대신, 와이어 프로토콜(PGWire/TDS)이 `RawRows []storage.Row` (`[][]storage.Value`)를 직접 소비하도록 파이프라인을 일원화하여 셀당 `any` 인터페이스 박싱과 행당 `map` 할당을 제거.
- **주의사항**: REST JSON API 및 내부 테스트 헬퍼 중 `res.Rows` 맵을 직접 참조하는 호출부와의 하위 호환성을 위해 지연 변환(`LazyRows()`) 또는 호출자 힌트 플래그(`WantTypedRows`)를 함께 설계해야 함.

### 4.2. [5순위] `PERF-TDS-ZEROCOPY-FRAME-1`: TDS `tdsStreamWriter` 인플레이스 패킷 프레이밍
- **목표**: `tdsStreamWriter`가 내부 `bytes.Buffer` 대신 `tdsFramePool32K` 슬라이스를 직접 보유하며 오프셋 `HeaderSize(8)`부터 토큰을 기록하고, 패킷 경계(`maxPayload`) 도달 시 앞단 8바이트 헤더만 채워 즉시 `w.Write()`를 호출함으로써 중간 복사(`copy`)를 완전 제거.

### 4.3. [6순위] `PERF-CACHE-SHARD-1`: `PlanCache` 및 세션 레지스트리 32-Shard 분할
- **목표**: `PlanCache`의 단일 `sync.RWMutex`를 프로시저/쿼리 해시 기반 32개 샤드(`cacheShard [32]struct{ mu sync.RWMutex; ... padding [40]byte }`)로 분리하여 64코어 이상 하이엔드 서버에서의 L1 캐시라인 바운싱을 원천 차단.
