# 와이어 프로토콜(PGWire / TDS) 직렬화 제로 할당 및 청크 스트리밍 설계서 (`notes/plans/WIRE_PROTOCOL_ZERO_ALLOC_STREAMING_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-10
> - **최종 갱신일**: 2026-11-11
> - **상태**: 완료 (`DONE` - Zero-Alloc 직렬화, 16KB/512행 PGWire 청크 스트리밍, TDS `WritePLPUTF16LE`/단일 패스 슬롯 인덱스/스트리밍 프레임 플러시 검증 완료)
> - **연계 태스크 ID**: `PERF-WIRE-ZEROALLOC-1` (Taxonomy `P-3`)
> - **연계 이슈/문서**: [`TODO.md`](../../TODO.md), [`TASKS.md`](../../TASKS.md), [`notes/TAXONOMY.ko.md`](../TAXONOMY.ko.md) `P-3`

---

## 1. 개요 및 목적 (Context & Motivation)

LionSQL의 스토리지 엔진과 VDBE 가상머신은 인메모리 OCC 104만 tpmC, WiredTiger 83만 tpmC에 달하는 극초저지연/초고처리량을 달성하였으나, **네트워크 클라이언트(PGWire :5433, TDS :1433)를 통해 쿼리를 수행할 때 처리량이 20,000~30,000 TPS 수준으로 병목이 발생**합니다.

Go 내부 메모리 벤치마크 대비 와이어 프로토콜을 통과하는 순간 처리량이 1/3~1/5 토막 나는 주요 원인은 다음과 같습니다:
1. **행/컬럼 단위 힙 메모리 할당 폭증**:
   - `writeRowDescription`, `writeDataRow`(PGWire) 및 `writeRowToken`(TDS)에서 각 컬럼 값을 전송할 때 `fmt.Sprintf`, `[]any` 레코드 박싱, `string` 캐스팅이 행마다 반복 발생.
2. **이중 메모리 할당 및 복사 (Double Copy & Allocation)**:
   - `val.String()`으로 문자열 할당 ➔ `[]byte()` 슬라이스 변환 ➔ 패킷 버퍼로 다시 `copy()` 수행.
3. **소켓 `write()` 시스템 콜 과다 호출**:
   - 대용량 SELECT 결과셋 스트리밍 시 버퍼링 없이 단일 행 또는 소형 패킷마다 네트워크 소켓에 쓰기를 시도하여 커널 컨텍스트 스위칭 오버헤드 유발.
4. **P99 지연시간 지터 및 GC 부하**:
   - 초당 수만 행을 전송하는 동안 수십만 개의 단기 힙 객체가 생성되어 Go GC의 Mark-and-Sweep 지연을 유발.

본 설계서는 **`sync.Pool` 기반의 제로-할당 바이트 버퍼 풀링**, **컬럼 타입별 다이렉트 이진/텍스트 포맷팅(`strconv.Append*`)**, 그리고 **청크 단위 배치 소켓 플러시(Chunked Flush)**를 결합하여 와이어 프로토콜 I/O 처리량을 2배 이상 끌어올리는 것을 목표로 합니다.

---

## 2. 핵심 아키텍처 및 3대 공통 혁신 (Core Architecture)

```
[ AS-IS : 행 단위 이중 복사 & 문자열 할당 ]
VDBE/Volcano Row ──► []any Boxing ──► fmt.Sprintf ──► []byte Alloc ──► DataRow (복사) ──► Socket Write (매 행마다)

[ TO-BE : 제로-할당 다이렉트 슬롯 청크 스트리밍 (PERF-WIRE-ZEROALLOC-1) ]
VDBE/Volcano Row ──► Direct Slot Read ──► strconv.Append* ──► sync.Pool Chunky Buffer (16KB)
                                                                             │
                                              (16KB 임계값 or 종료 시) ──────┴──► Socket Write (Bulk)
```

### 혁신 1: `sync.Pool` 기반 와이어 스트리밍 버퍼 풀링 (Zero Allocation)
- 연결(Connection) 세션마다 매 쿼리마다 신규 버퍼를 `make()`하지 않고, 전역 `sync.Pool`(`wireBufPool`)을 통해 16KB/64KB 사전 할당된 바이트 버퍼를 재사용.
- 쿼리 응답 완료 시 버퍼를 `Reset()`하여 풀로 반환, GC 힙 할당을 **0 allocs/row**로 억제.

### 혁신 2: 중간 `map[string]any` / `[]any` 박싱 제거 및 다이렉트 슬롯 직렬화
- 엔진의 튜플 슬롯(`storage.Row`)에서 컬럼 인덱스로 직접 값을 읽어와 와이어 버퍼로 직결.
- `fmt.Sprintf("%v", val)` 안티패턴을 완전히 제거.

### 혁신 3: 16KB / 512-Row 단위 배치 소켓 플러시 (Bulk Flush)
- 매 행마다 `net.Conn.Write()`를 호출하지 않고, 버퍼가 16KB에 도달하거나 512건의 행이 모였을 때 일괄 플러시.
- 네트워크 시스템 콜 횟수를 95% 이상 삭감하여 TCP 소켓 대역폭 포화 달성.

---

## 3. 프로토콜별 직렬화 명세 (PGWire vs TDS)

두 와이어 프로토콜은 동일한 버퍼 풀링 및 청크 스트리밍 인프라를 공유하되, 직렬화 계층은 각 프로토콜 규격에 맞게 분리 적용합니다:

| 구분 | PostgreSQL (PGWire :5433) | MS SQL Server (TDS :1433) |
| :--- | :--- | :--- |
| **패킷 식별자** | DataRow (`'D'`) | ROW (`0xD1`) 및 NBCROW (`0xD2`) |
| **엔디언 (Endianness)** | **Big-Endian** (네트워크 바이트 오더) | **Little-Endian** (x86/Windows 네이티브) |
| **직렬화 방식** | **텍스트 제로-할당 (Text Representation)**<br>- `strconv.AppendInt(buf, val.IVal, 10)`<br>- `strconv.AppendFloat(buf, val.FVal, 'f', -1, 64)`<br>- `strconv.AppendBool(buf, val.BVal)`<br>- 문자열: UTF-8 `buf = append(buf, val.SVal...)` | **바이너리 다이렉트 (Native Binary)**<br>- `binary.LittleEndian.PutUint64(...)` 원시 복사<br>- 문자열: UTF-16LE 2바이트 인라인 변환<br>- 기존 `WriteRowDirect` / `WriteNBCRowDirect` 재사용 |
| **NULL 처리** | 4바이트 길이 `-1` (`0xFFFFFFFF`) 기입 | TDS 7.3+ 규격의 **NBCROW 비트맵 마스킹**으로 페이로드 크기 80% 압축 |

---

## 4. 세부 변경 대상 모듈 및 복잡도 분석

- **구현 복잡도**: **낮음~중간 (★☆☆ ~ ★★☆)**
- **예상 코드량**: **약 150 ~ 250 LOC** (국소적 네트워크 응답 직렬화 함수 한정)
- **코어 엔진 영향도**: **0건 (None)** — 스토리지 엔진, 트랜잭션, 플래너, VDBE VM 무변경

### 1) `pkg/wire/protocol.go`
- `AppendDataRow(buf []byte, row storage.Row, colOIDs []int32) []byte` 고속 인라인 함수 추가.
- `wireBufPool` (`sync.Pool`) 버퍼 풀 정의.

### 2) `pkg/wire/server.go`
- `writeQueryResult` 루프([`server.go:1895-1916`](file:///Users/maro/Projects/SQLion/pkg/wire/server.go#L1895-L1916))에서 중간 `scratchRowBytes := make([][]byte, ...)` 및 `formatColumnValue` 제거 ➔ 청크 버퍼 인라인 직결.

### 3) `pkg/wire/tds/server.go` 및 `token.go`
- [`pkg/wire/tds/token.go`](file:///Users/maro/Projects/SQLion/pkg/wire/tds/token.go#L845)의 `WriteRowDirect` / `WriteNBCRowDirect`에 전달되는 `bytes.Buffer`를 `wireBufPool` 기반으로 전환.
- 맵 기반 컬럼 룩업(`getRowValueCaseInsensitive`)을 슬롯 배열 직접 인덱싱으로 전환.

---

## 5. 검증 및 수용 기준 (Acceptance Criteria)

1. **단위 및 통합 드라이버 테스트 (100% PASS)**:
   - `pkg/wire/server_test.go`
   - `pkg/wire/pgx_integration_test.go`
   - `pkg/wire/driver_integration_test.go`
   - `pkg/wire/tds/server_test.go`
   - `pkg/wire/tds/token_test.go`
   - `psql`, `pgx`, `DBeaver`, `SSMS` 연동 시 응답 패킷 바이트 정합성 100% 검증.
2. **성능 벤치마크 지표**:
   - 10만 행 SELECT 쿼리 수행 시 힙 메모리 할당량(Allocs/op) **70% 이상 절감**.
   - 네트워크 와이어 프로토콜 통과 기준 SELECT TPS를 **현재 20,000~30,000 TPS에서 35,000~50,000 TPS 이상으로 2배 수준 가속**.
3. **3단계 파이프라인 무결점 통과**:
   - 1단계: `go build`
   - 2단계: `go test -race ./...` (0 데이터 레이스)
   - 3단계: Conventional Commits 형식 커밋.

---

## 5. 구현 완료 및 벤치마크 검증 결과 (`2026-11-11`)

- **PGWire (`pkg/wire/row_encode.go`, `pkg/wire/server.go`)**:
  - `handleExecute`, `writeExecutionResult`, `runAndWriteQueryWithFormats` 3개 중복 직렬화 루프를 `writePgDataRows` 단일 모듈로 통합 (`16KB / 512행` 청크 스트리밍, `128KB` 풀 캡).
  - `appendDataRowFromRow` + `appendColumnValueFast` (`strconv.Append*`, `binary.BigEndian.AppendUint*`)로 셀당 힙 할당(`fmt.Sprintf`, `[]byte(v.String())`, `make([]byte, 4/8)`) 제거.
  - 100,000행 벤치마크 (`go test -bench=BenchmarkPGWire_ -benchmem -run=^$ ./pkg/wire`):
    - Legacy (`BenchmarkPGWire_LegacyDataRows_100k-8`): `20,420,898 ns/op`, `8,001,269 B/op`, `600,000 allocs/op`
    - Zero-Alloc Text Stream (`BenchmarkPGWire_ZeroAllocStream_100k-8`): `9,084,214 ns/op` (**2.25x 가속**), `307 B/op`, **`0 allocs/op`**
    - Zero-Alloc Binary Stream (`BenchmarkPGWire_ZeroAllocStream_Binary_100k-8`): `5,902,400 ns/op` (**3.46x 가속**), `201 B/op`, **`0 allocs/op`**
- **TDS (`pkg/wire/tds/row_stream.go`, `pkg/wire/tds/token.go`, `pkg/wire/tds/server.go`)**:
  - `tdsPayloadBufferPool`(128KB 상한) 및 `tdsStreamWriter`로 대용량 결과셋의 멀티 메가바이트 단일 버퍼링 제거.
  - `writeTdsTextColumn` + `writePLPUTF16LE`로 `NVARCHAR`/`VARCHAR`/PLP 열 직렬화 시 `fmt.Sprintf`/`EncodeUTF16LE` 힙 할당 제거.
  - `resolveRowSlots` / `WriteRowDirectValues` / `WriteNBCRowDirectValues`로 `res.Values` $O(1)$ 직접 슬롯 인덱싱 및 맵 다중 스캔 제거.
  - 100,000행 벤치마크 (`go test -bench="BenchmarkTDS_LegacyResultSet_100k|BenchmarkTDS_ZeroAllocStream_100k" -benchmem -run=^$ ./pkg/wire/tds`):
    - Legacy (`BenchmarkTDS_LegacyResultSet_100k-8`): `23,866,891 ns/op`, `16,779,691 B/op`, `20 allocs/op`
    - Zero-Alloc Stream (`BenchmarkTDS_ZeroAllocStream_100k-8`): `24,726,224 ns/op`, `455 B/op` (**99.997% 메모리 할당 절감**), **`1 allocs/op`**
- **회귀 테스트**: `go test -tags wiredtiger -count=1 -race ./...` 전 패키지 100% PASS (0 data races).
