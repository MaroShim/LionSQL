# LionSQL 성능 비교 벤치마크 리포트 (2026-11-11)

> - **최초 작성일**: 2026-11-11
> - **측정 커밋**: `main@1cc16aa` (`PERF-HYBRID-TEMPMEM-1` 반영 직후)
> - **측정 환경**: Apple M1 (`8 cores`), 16 GB RAM, `darwin/arm64`, Go `1.27.1`, `-tags wiredtiger`
> - **측정 목적**:
>   1. 와이어 프로토콜 제로-할당 & 청크 스트리밍(`PERF-WIRE-ZEROALLOC-1`) 효과 검증
>   2. WiredTiger 임시 테이블 인메모리 하이브리드 바이패스(`PERF-HYBRID-TEMPMEM-1`) 효과 검증
>   3. In-Memory OCC `compact` 디폴트 전환(`DefaultMemory()` = `"compact"`) 실측 평가 및 `performance` 모드 비교
>   4. 인프로세스 OLTP(`cmd/bench`, `cmd/tpcc_runtime`) 및 실서버 TCP 루프백(`cmd/tpcc_client`) 수치 기록

---

## 1. 와이어 프로토콜 제로-할당 스트리밍 (`PERF-WIRE-ZEROALLOC-1`)

### 1.1 PGWire & TDS 100,000행 스트리밍 마이크로 벤치마크
- **대상 패키지**: `pkg/wire`, `pkg/wire/tds`
- **실행 명령**:
  ```bash
  go test -run='^$' -bench='Benchmark(PGWire|TDS)_(Legacy|ZeroAlloc).*100k' -benchmem ./pkg/wire ./pkg/wire/tds
  ```

| 벤치마크 (`100,000 rows`) | 지연시간 (`ns/op`) | 힙 할당량 (`B/op`) | 할당 횟수 (`allocs/op`) | 개선 폭 |
|---|---:|---:|---:|---|
| `BenchmarkPGWire_LegacyDataRows_100k` | 19,841,625 ns (19.84 ms) | 8,001,302 B (~7.63 MB) | 600,000 | 기준 (Before) |
| `BenchmarkPGWire_ZeroAllocStream_100k` (Text) | **9,203,629 ns (9.20 ms)** | **307 B** | **0** | **2.16x 빠름 / 할당 -100%** |
| `BenchmarkPGWire_ZeroAllocStream_Binary_100k` | **6,807,176 ns (6.81 ms)** | **201 B** | **0** | **2.91x 빠름 / 할당 -100%** |
| `BenchmarkTDS_LegacyResultSet_100k` | 22,692,390 ns (22.69 ms) | 16,779,525 B (~16.0 MB) | 20 | 기준 (Before) |
| `BenchmarkTDS_ZeroAllocStream_100k` | **18,332,033 ns (18.33 ms)** | **535 B** | **1** | **1.24x 빠름 / 메모리 -99.997%** |

---

## 2. WiredTiger 임시 테이블 인메모리 바이패스 (`PERF-HYBRID-TEMPMEM-1`)

### 2.1 테이블 생명주기 비교 (`CREATE TABLE` ➔ 50행 `INSERT` ➔ `SCAN` ➔ `DROP TABLE`)
- **대상 패키지**: `pkg/storage/wiredtiger`
- **비교 내용**: 기존 WiredTiger CGo B+Tree 디스크 경로(`open/create/unlink`) vs `__temp_*` 내장 `memengine` 바이패스 경로

| 경로 | 실행 횟수 (`b.N`) | 지연시간 (`ns/op`) | 디스크 `.wt` 파일 I/O | 개선 폭 |
|---|---:|---:|---:|---|
| **Before** (`WiredTigerDiskPath`) | 76 | 13,893,366 ns (13.89 ms) | 매 사이클 `.wt` 생성/삭제 + `catalog.json` | 기준 (Before) |
| **After** (`HybridInMemoryBypass`) | **8,646** | **148,171 ns (0.148 ms)** | **0개 (파일 I/O 없음)** | **93.8x 빠름 (지연 98.9% 감소)** |

---

## 3. OCC `compact` 디폴트 vs `performance` 모드 비교

### 3.1 스토리지 마이크로 벤치마크 (`pkg/storage/memengine`)
- **실행 명령**:
  ```bash
  go test -run='^$' -bench='BenchmarkMemEngine_' -benchmem ./pkg/storage/memengine
  ```

| 벤치마크 | `performance` (`ModeRaw`) | `compact` 디폴트 (`ModeFlat`) | 변화 |
|---|---:|---:|---|
| **10,000행 힙 메모리** (`MemoryFootprint`) | 7.657 MB (`54,149` objects) | **3.001 MB** (`44,131` objects) | **힙 메모리 -60.8% / 오브젝트 -18.5%** |
| **단건 Insert** (`AB_Insert`) | 6,637 ns/op (`11,604 B/op`, `31 allocs`) | **6,214 ns/op** (`11,757 B/op`, `33 allocs`) | **+6.8% 빠름** |
| **1,000행 Scan** (`AB_Scan`) | **167,475 ns/op** (`82,184 B/op`, `7 allocs`) | 311,051 ns/op (`560,592 B/op`, `4,007 allocs`) | 로우 디코딩 비용으로 Scan 약 1.86x 느림 |

### 3.2 실서버 TCP 루프백 TPC-C (`bin/lionsql` + `bin/tpcc-client`, `everysec` WAL 활성)
- **조건**: `16 Warehouses`, `32 Concurrent Workers`, `8s`, 동일 호스트(M1 8코어) 내 서버·클라이언트 동시 구동, `-ephemeral` 미사용(기본 `everysec` WAL 활성)

| 서버 설정 / 프로토콜 | `tpmC` (New-Order/분) | 전체 `TPS` | 커밋 / 시도 | Abort 비율 | New-Order P50 / P95 / P99 |
|---|---:|---:|---:|---:|---|
| **`-disk=none -memory=performance` / TDS** | 69,764.85 | 2,541.97 | 20,338 / 20,370 | 0.16% | 8.73 ms / 52.93 ms / 109.43 ms |
| **`-disk=none -memory=compact` (디폴트) / TDS** | **100,134.22** | **3,722.76** | 29,728 / 29,761 | **0.11%** | **6.21 ms / 41.92 ms / 68.43 ms** |
| **`-disk=none -memory=compact` (디폴트) / PGWire** | **117,449.88** | **4,323.73** | 34,581 / 34,589 | **0.02%** | **3.64 ms / 49.88 ms / 96.94 ms** |
| **`-disk=wiredtiger` / TDS** | 95,633.96 | 3,531.45 | 28,260 / 28,294 | 0.12% | 6.79 ms / 25.83 ms / 50.75 ms |
| **`-disk=wiredtiger` / PGWire** | **110,831.13** | **4,110.60** | 33,311 / 33,480 | 0.50% | 4.25 ms / 29.90 ms / 65.36 ms |

### 3.3 `MemoryPerformance` (`performance` 모드) 1.0 이전 Deprecate 검토 메모
- 단일 코어 풀스캔 마이크로 벤치마크(`ModeRaw_Scan1000`)에서는 `performance`가 앞서지만, 실제 동시 네트워크 부하(`tpcc-client` ➔ `lionsql`)에서는 **`compact` 디폴트가 `performance` 대비 TDS 기준 +46.4% 높은 TPS (`3,722.76` vs `2,541.97 TPS`), +43.5% 높은 `tpmC` (`100,134` vs `69,765`)** 를 기록했습니다.
- 원인: 포인터-프리 플랫 버퍼 구조가 고동시성 환경에서 Go GC 마킹/스캔 지연(`STW` 및 어시스트 오버헤드)을 대폭 억제하고 L1/L2 캐시 지역성을 높이기 때문입니다.
- 이에 따라 1.0 릴리스 전 `-memory=performance` (`MemoryPerformance`) 모드의 단계적 Deprecate(또는 `compact` 단일화)를 아키텍처 정리 후보로 검토할 수 있습니다.

---

## 4. 인프로세스 OLTP & VDBE 참고 수치 (`everysec` WAL 활성)

### 4.1 TransferFunds 저장 프로시저 (`./bin/bench`)
- **조건**: `-accounts=10000 -workers=100 -ops=50000`

| 지표 | `memory` (In-Memory OCC) | `wiredtiger` (Disk MVCC) |
|---|---:|---:|
| **총 실행 시간** | **328.26 ms** | 342.39 ms |
| **처리량 (TPS)** | **152,319.66 TPS** | **146,030.74 TPS** |
| **성공 / 롤백** | 50,000 / **0** (100.0%) | 49,891 / 109 (99.78%) |
| **P50 / P95 / P99 지연** | 26.29 µs / 2.74 ms / 13.49 ms | **23.67 µs / 153.13 µs / 3.61 ms** |
| **ACID 잔액 보존** | 10,000,000,000 (100% VERIFIED) | 10,000,000,000 (100% VERIFIED) |

### 4.2 VDBE TPC-C 런타임 (`./bin/tpcc-runtime`)
- **조건**: `-warehouses=16 -workers=32 -duration=10s`, `-fsync=everysec`

| 지표 | `memory` (`everysec`) | `wiredtiger` (`everysec`) |
|---|---:|---:|
| **tpmC (New-Order/분)** | 622,073.01 tpmC | **819,808.14 tpmC** |
| **전체 TPS** | 23,083.18 TPS | **30,212.30 TPS** |
| **커밋 / 시도** | 238,727 / 238,882 | 302,274 / 309,921 |
| **직렬화 Abort 비율** | **0.06%** (155건) | 2.47% (7,647건) |
| **New-Order P50 / P95 / P99** | 0.98 ms / **5.05 ms** / **11.13 ms** | **0.31 ms** / 6.16 ms / 18.59 ms |
| **Payment P50 / P95 / P99** | 0.16 ms / **1.29 ms** / **3.03 ms** | **0.05 ms** / 5.78 ms / 15.43 ms |
| **Order-Status P50 / P95** | **0.00 ms / 0.02 ms** | 0.01 ms / 0.03 ms |

---

*(주의: [doc/BENCHMARK.md](../../doc/BENCHMARK.md) Section 3.6 수치는 `-ephemeral` WAL 비활성 기준이며, 본 리포트의 네트워크 TPC-C 수치는 기본 `everysec` WAL이 활성화된 상태에서 동일 조건 비교를 위해 별도 실측한 기록입니다.)*
