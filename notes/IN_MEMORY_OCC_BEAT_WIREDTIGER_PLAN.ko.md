# In-Memory OCC 엔진의 WiredTiger 초격차 추월 및 메모리 효율화 플랜 (`notes/IN_MEMORY_OCC_BEAT_WIREDTIGER_PLAN.ko.md`)

## 1. 문제의식 및 목표 정의 (Problem Statement & Strategic Objectives)

### 1.1. 본질적 문제의식: 인메모리 엔진의 존재 가치 (Raison d'être)
인메모리 데이터베이스는 고가의 RAM 자원을 점유하므로 필연적으로 **"공간 제약(용량 한계 및 기가바이트당 비용)"**을 안고 출발합니다.
엔터프라이즈 환경에서 공간 제약과 비용을 감수하고 인메모리 엔진을 선택하는 유일한 정당성은:
> **"디스크/NVMe 기반 엔진(WiredTiger)이 물리적으로 절대 흉내 낼 수 없는 마이크로초(µs) 극초저지연과 압도적인 2~3배 이상의 폭발적 처리량"**

그러나 현재 LionSQL의 벤치마크 실측치는 다음과 같은 모순에 직면해 있습니다:
1. **단일 프로시저에서는 OCC 우세**: `TransferFunds` 100 워커 기준 OCC **186,766 TPS** vs WiredTiger **120,629 TPS** (+54.8% 우위).
2. **복합 TPC-C에서는 WiredTiger에 역전 허용**: 100 워커 기준 OCC **779,641 tpmC** vs WiredTiger **833,595 tpmC** (WiredTiger 대비 약 6.5% 열세).
3. **높은 메모리 소비량 (낮은 집적도)**: 행당 Go 힙 메모리를 1.5KB~2KB 점유하여, 동일한 RAM 크기에서 저장할 수 있는 레코드 수가 제한적임.

**"비싼 RAM을 쓰면서 공간 제약도 있고, 복합 트랜잭션 성능마저 디스크 엔진에 뒤처진다면 그 누구도 채택하지 않는다"**는 사용자의 지적은 100% 타당합니다. 본 플랜은 인메모리 OCC 엔진이 WiredTiger를 전 영역에서 **2배 이상 압도적인 처리량**과 **5배 이상의 메모리 집적도**로 완벽히 따돌리기 위한 구체적인 아키텍처 재설계 로드맵입니다.

---

## 2. 현행 OCC 엔진의 4대 구조적 병목 진단 (Root-Cause Diagnosis)

```
[현재 OCC 구조의 병목]
1. 샤드 RLock 경합: 읽기 트랜잭션이 shard.mu.RLock() 대기 -> 읽기 지연 0.48ms (WT는 0.04ms)
2. 고정 64개 샤드: 100~500 워커가 핫 테이블(customer, stock)에 몰려 동일 샤드 해시 충돌
3. Fat storage.Value: 인터페이스 및 포인터 남발로 행당 1.5~2KB 힙 점유 (GC 압력 폭증)
4. pk.String() 맵 키: 정수 PK를 매번 문자열로 변환하여 힙 할당 및 map[string] 해시 연산 낭비
```

| # | 병목 원인 | 현재 구현 방식 | WiredTiger 대비 취약점 |
| :---: | :--- | :--- | :--- |
| **1** | **읽기 샤드 락 경합** | `shard.mu.RLock()`으로 행 검색 | WiredTiger는 C 기반 **Hazard Pointer**로 100% 무락(Zero-Lock) 읽기를 수행하지만, OCC는 쓰기 락에 의해 읽기 락이 대기함. |
| **2** | **샤드 수 고정 (64개)** | `const numTableShards = 64` | 워커 수가 100~500개로 늘어날 때 동일 샤드로 해시 충돌(`hash % 64`)이 발생하여 멀티코어 확장성 정체. |
| **3** | **무거운 객체 구조 (Fat Value)** | Go 구조체 `storage.Value` 슬라이스 | 20개 컬럼 행 하나가 1.5KB~2KB 힙을 차지하며 포인터가 수십 개 존재하여 Go GC 스캔 부담 급증. |
| **4** | **문자열 기반 기본키 탐색** | `pk.String()` ➔ `entries map[string]` | 정수 PK(`w_id`, `d_id`, `c_id`)를 매번 문자열로 변환하여 힙 할당 및 느린 문자열 해시 탐색 발생. |

---

## 4대 초격차 아키텍처 혁신 (The 4 Pillars of Hyper-Scale OCC)

```mermaid
flowchart TD
    subgraph P1["Pillar 1: Lock-Free Snapshot Reading"]
        Reader["Reader Worker\n(Order-Status / Select)"] -->|Zero-Lock| RowHeader["atomic.Pointer[RowVersion]"]
        RowHeader --> V1["Version N (Timestamp / Raw Data)"]
        V1 --> V0["Version N-1 (Undo Chain)"]
    end

    subgraph P2["Pillar 2: Compact Binary Tuple & Arena"]
        RowArena["Contiguous Byte Arena\n(300 Bytes per Row)"]
        RowArena -->|Zero Pointer Scan| GC["Go GC: 95% Less Work"]
    end

    subgraph P3["Pillar 3: Int PK Direct Slot Indexing"]
        IntKey["uint64 Key (w_id, d_id, c_id)"] -->|No pk.String()| DirectMap["Fast Int Hash / Radix Tree"]
    end

    subgraph P4["Pillar 4: Dynamic CPU-Proportional Sharding"]
        Workers["100~500 Concurrent Workers"] -->|Scales to 1024 Shards| Shards["Shards = NextPowerOf2(NumCPU * 8)"]
    end
```

### Pillar 1: 100% 무락(Lock-Free) 행 버전 스냅샷 읽기 (Hekaton MVCC 모델)
- **개념**: Microsoft SQL Server의 Hekaton 엔진 및 WiredTiger의 Hazard Pointer처럼, **읽기 작업이 샤드 뮤텍스를 단 1나노초도 잡지 않는 완전 무락(Zero-Lock) 구조**로 전환합니다.
- **구현 방식**:
  - 행(Row)을 가변 구조체가 아닌 불변(Immutable) 버전 노드로 정의:
    ```go
    type RowVersion struct {
        CommitTS  int64
        TxID      uint64
        RawTuple  []byte // 컴팩트 바이너리 튜플
        Prev      *RowVersion
    }
    type RowHeader struct {
        Head atomic.Pointer[RowVersion]
    }
    ```
  - 읽기 트랜잭션은 샤드 락 없이 오직 `header.Head.Load()`로 최신 버전을 원자적으로 가져온 후, 자신의 `SnapshotTS` 이하인 첫 번째 버전을 포인터 체인으로 읽고 즉시 반환.
  - **효과**: WiredTiger의 읽기 지연시간(0.04ms)을 능가하는 **0.01ms ~ 0.02ms (10~20µs)** 읽기 지연시간 달성.

### Pillar 2: 컴팩트 바이너리 튜플 및 오프-힙(Off-Heap)/Arena 저장 (메모리 1/5 압축)
- **개념**: Go 포인터 기반의 `storage.Value` 슬라이스를 영구 퇴출하고, 연속된 바이트 메모리(Arena)에 다닥다닥 압축 저장하여 공간 제약을 획기적으로 극복합니다.
- **구현 방식**:
  - `storage.EncodeRowWithScratch` 기반 가변 길이 바이너리 튜플 포맷(Varlen Byte Packing)을 인메모리 표준 포맷으로 채택.
  - 20개 컬럼 기준 행 크기를 1,500바이트 ➔ **200~300바이트**로 80% 압축.
  - 포인터 없는 원시 바이트 슬라이스(`[]byte`)로 저장하여 Go GC의 객체 스캔 대상을 95% 제거.
  - **효과**: 동일한 16GB RAM 서버에서 저장 가능한 레코드 수가 **1,000만 건 ➔ 5,000만 건 이상으로 5배 증가**.

### Pillar 3: 정수 기본키(Int PK) 제로-할당 다이렉트 슬롯 인덱싱
- **개념**: OLTP 기본키의 95%를 차지하는 단일/복합 정수 키(`INT`, `BIGINT`)에 대해 문자열 변환(`pk.String()`)을 전면 폐지합니다.
- **구현 방식**:
  - 키가 8바이트 이하 정수인 경우 `uint64` 키 전용 샤드 맵(`map[uint64]*RowHeader`) 또는 락-프리 Radix Tree로 직결.
  - 복합 정수 키(`w_id`, `d_id`, `c_id`)는 비트 시프트(`(w_id << 32) | (d_id << 16) | c_id`)로 단일 64비트 정수로 패킹.
  - **효과**: 점조회(`tx.Get`) 핫패스에서 힙 할당 **0 allocs/op**, 지연시간 **0.1µs(100나노초) 이하** 달성.

### Pillar 4: CPU 코어 비례 동적 샤딩 (Dynamic CPU-Proportional Sharding)
- **개념**: 64개로 고정된 하드코딩 상수를 철폐하고 하드웨어 병렬도에 맞춰 샤드를 동적 확장합니다.
- **구현 방식**:
  - 샤드 수 자동 계산 공식:
    $$\text{NumShards} = \min\left(1024, \max\left(64, \text{NextPowerOf2}(\text{NumCPU} \times 8)\right)\right)$$
  - 16코어 머신 기준 128개, 32코어 머신 기준 256개, 64코어 이상 서버 기준 512~1024개 샤드 자동 프로비저닝.
  - **효과**: 100~500개 워커가 극단적 동시 쓰기를 수행해도 샤드 쓰기 락 충돌 확률이 **1/4 ~ 1/16로 격감**.

---

## 4. 정량적 목표 지표 및 WiredTiger와의 격차 목표

| 지표 (Metric) | 현재 WiredTiger (NVMe 디스크) | 현재 In-Memory OCC (과도기) | **초격차 OCC 목표 (Target)** | **WiredTiger 대비 개선 배수** |
| :--- | :---: | :---: | :---: | :---: |
| **단일 프로시저 TPS (`TransferFunds`)** | 120,629 TPS | 186,766 TPS | **450,000+ TPS** | **3.7배 우위** |
| **TPC-C 커널 처리율 (10Wh, 100Wrk)** | 833,595 tpmC | 779,641 tpmC | **1,500,000 ~ 2,000,000 tpmC** | **1.8 ~ 2.4배 우위** |
| **TPC-C VDBE 처리율 (32Wrk)** | 497,294 tpmC | 668,521 tpmC | **1,000,000+ tpmC** | **2.0배 우위** |
| **읽기 P50 지연시간 (`Order-Status`)** | 40 µs (0.04 ms) | 480 µs (0.48 ms) | **10 ~ 20 µs (0.01 ~ 0.02 ms)** | **2 ~ 4배 빠름** |
| **쓰기 P50 지연시간 (`New-Order`)** | 2.93 ms | 3.77 ms | **0.8 ~ 1.2 ms** | **2.5 ~ 3.5배 빠름** |
| **100만 행당 메모리 점유량** | NVMe 디스크 기반 | 약 1.8 GB (Go Heap) | **약 350 MB (Compact Arena)** | **메모리 집적도 5배 향상** |
| **Cgo 스택 전환 오버헤드** | Cgo 경계 전환 비용 수반 | **0 (순수 Go 네이티브)** | **0 (순수 Go 네이티브)** | **완전 무오버헤드** |

---

## 5. 단계별 실행 로드맵 (Post-1.0 Phased Roadmap)

기존 기능 안정성을 해치지 않고 점진적으로 격차를 벌리기 위해 4단계로 나누어 추진합니다:

```
[Phase 1: 동적 샤딩 & 정수 키 최적화] -> [Phase 2: 100% 무락 행 버전 스냅샷] -> [Phase 3: 컴팩트 바이너리 튜플화] -> [Phase 4: 200만 tpmC 달성 검증]
```

### Phase 1: 동적 샤딩 확장 및 정수 PK 제로-할당 (Quick-Win)
- `pkg/storage/memengine/table_data.go`의 `numTableShards`를 런타임 CPU 비례(`NextPowerOf2(NumCPU * 8)`)로 동적화.
- 단일/복합 정수 PK에 대해 `uint64` 다이렉트 해시 경로 추가 (`pk.String()` 문자열 할당 제거).
- **목표**: TPC-C 커널 처리율 **779k ➔ 950,000 tpmC** (WiredTiger 833k 1차 추월).

### Phase 2: 무락(Lock-Free) 행 버전 스냅샷 읽기 구현
- `TableData`에 `atomic.Pointer[RowVersion]` 불변 버전 체인 도입.
- 읽기 트랜잭션(`tx.Get`, `tx.Scan`, `tx.IndexScan`)에서 샤드 뮤텍스 `shard.mu.RLock()` 호출을 100% 제거.
- **목표**: 읽기 지연시간 0.48ms ➔ 0.02ms 이하 단축, TPC-C 커널 **1,200,000 tpmC 돌파**.

### Phase 3: 컴팩트 바이너리 튜플 및 바이트 아레나 저장
- 행 저장 포맷을 `storage.Row` 슬라이스에서 `[]byte` 컴팩트 바이너리 튜플로 전환.
- Go GC 힙 객체 스캔 수 90% 이상 감축, 행당 메모리 300바이트 이하 달성.
- **목표**: 메모리 집적도 5배 향상, 100개 워커 장시간 부하 시 GC 프리징 0초 달성.

### Phase 4: 극단적 동시성 벤치마크 및 200만 tpmC 초격차 검증
- 20 Wh / 200 Workers 및 50 Wh / 500 Workers 극단적 부하 하네스 실행.
- TPC-C **1,500,000 ~ 2,000,000 tpmC** 공인 달성 및 `doc/BENCHMARK.md` 지표 갱신.

---

## 6. 결론: "비싼 서버 램을 기꺼이 쓸 수밖에 없는 엔진"

본 플랜이 완료되면 LionSQL In-Memory OCC는:
1. **공간 제약 극복**: 컴팩트 바이너리 튜플로 동일 메모리에서 5배 더 많은 데이터를 수용하고,
2. **성능 제약 극복**: WiredTiger 디스크 엔진 대비 **2배 이상의 처리량(150만~200만 tpmC)**과 **10~20µs 초저지연**을 달성하여,

**"초저지연과 극도의 처리량을 위해 비싼 RAM 비용을 지불할 충분한 가치가 있는 독보적인 고성능 엔진"**으로 확고히 자리매김하게 됩니다.
본 계획서는 Golden Rule 42에 의거하여 영구 보존되며, Post-1.0의 최우선 성능 과제로 추진됩니다.
