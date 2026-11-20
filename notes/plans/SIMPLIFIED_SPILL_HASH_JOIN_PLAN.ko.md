# Simplified Disk-Spill Grace Hash Join Architecture & Plan (Post-1.0)

- **작성일**: 2026-11-19
- **작성자**: LionSQL Core Engine Team
- **상태**: PENDING (Post-1.0 Milestone)
- **태스크 ID**: `EXEC-SIMPLIFIED-SPILL-HASH-JOIN-1` (Priority: P2)

> **Document Primacy & Rule References**:
> - `[RULE:PlanPersistence]`: Unconditional plan and architecture persistence to physical disk (`notes/*_PLAN.ko.md`).
> - `[RULE:ContinuousBacklog]`: Dual-sync registration in sprint SQLite DB (`EXEC-SIMPLIFIED-SPILL-HASH-JOIN-1`) and `TASKS.md` (superseding `OLAP-OUT-OF-CORE-GRACE-HASH-JOIN-1` which is marked `SPEC_OUT`).
> - `[RULE:RadicalKISS]`: Radical simplicity; eliminate textbook hybrid dynamic repartitioning; adopt **Simplified Fixed-Partition Spill Hash Join**.
> - Execution Order: **Preceded by `PERF-PARALLEL-HASH-JOIN-1` (1-Build N-Probe)** -> Followed by `EXEC-SIMPLIFIED-SPILL-HASH-JOIN-1`.
> - `[RULE:AtomicSaves]`: Safe temporary swap files, isolated spill directory (`<datadir>/tmp_spill/`), and automatic cleanup on completion or crash.
> - `[RULE:CleanTeardown]`: Guarantee 100% deterministic temporary file removal via `defer` and `sync.Once`.
> - `[RULE:BoundedBuffers]`: Strict memory enforcement under `work_mem` budget.

---

## 1. Background & The Problem with Traditional Hybrid Grace Hash Join

교과서적인 **Hybrid Grace Hash Join**은 구현 복잡도가 매우 높습니다:
1. **Dynamic Partition Tuning**: 메모리 잔여량에 따라 0번 파티션을 메모리에 유지하고 $1 \sim K$번 파티션을 디스크로 보냄.
2. **Recursive Partitioning (Hash Skew)**: 특정 파티션에 데이터가 몰려 다시 `work_mem`을 초과하면 2차, 3차 재귀 파티셔닝 수행.
3. **Complex Buffer Management**: 수십 개의 파티션 파일 디스크립터(FD)를 동시에 열어두고 버퍼 풀을 분할 관리.

이러한 복잡성은 코드베이스를 수천 줄로 부풀리고 버그 발생률과 테스트 난이도를 급상승시킵니다 (`[RULE:RadicalKISS]` 위배).

---

## 2. LionSQL을 위한 "Simplified Spill Hash Join" (간소화 방안)

LionSQL의 철학에 맞춰 복잡한 Hybrid/Recursive 메커니즘을 걷어내고, **단 3단계로 동작하는 `Simplified Spill Hash Join`**을 설계합니다.

### 2.1 3대 간소화 핵심 아이디어

```
              [ Phase 1: In-Memory Probe (Normal Path) ]
                                   │
              Memory <= work_mem? ─┴─► Yes: 기존 0ms 인메모리 해시 조인 완료!
                                   │
                                   ▼ No (work_mem 초과 시만 트리거)
                                   
              [ Phase 2: Fixed 16-Bucket Spill (Split) ]
                 Right (Build)  ──► Hash(Key) % 16 ──► 16 Spill Files
                 Left (Probe)   ──► Hash(Key) % 16 ──► 16 Spill Files
                                   │
                                   ▼
              [ Phase 3: Partition-by-Partition Join (Loop) ]
                 For p = 0 to 15:
                   - Build InMemory Map from Right_File[p]
                   - Probe Left_File[p] and Emit Matches
                   - Delete Right_File[p], Left_File[p]
```

1. **고정 16/32 파티션 (Fixed Bucket Number)**:
   - 동적 파티션 계산 없이 고정 $K=16$ (또는 32)개의 고정 버킷을 사용합니다.
   - 예: `work_mem = 64MB`일 때 16개 버킷으로 분할하면 최대 **1GB** 데이터까지 단 1회의 디스크 분할로 완전히 수용 가능합니다.
2. **Hybrid(파티션 0 메모리 유지) 배제 -> 완전 스필 후 순차 처리**:
   - 파티션 0을 메모리에 남겨두고 나머지를 디스크로 보내는 복잡한 하이브리드 상태 머신을 제거합니다.
   - 메모리가 넘치면 일단 양쪽을 $K$개의 임시 파일로 분할 적재한 뒤, **파티션 $0$부터 $K-1$까지 기존의 단순한 `HashJoinIterator`를 루프로 재사용**하여 하나씩 처리합니다.
3. **기존 `HashJoinIterator` 100% 재활용 (Zero Duplicate Code)**:
   - 디스크에서 파티션 쌍(`Right_p`, `Left_p`)을 읽어올 때는 이미 만들어진 기존의 Zero-Alloc 인메모리 `HashJoinIterator`를 그대로 호출합니다. 새로운 조인 코드를 작성할 필요가 없습니다.

---

## 3. Simplified Spill Hash Join 상세 아키텍처

### 3.1 Data Structures (`pkg/execution/spill_hash_join.go`)

```go
type SpillHashJoinIterator struct {
    left         Iterator
    right        Iterator
    joinType     string
    leftKey      string
    rightKey     string
    workMem      int64
    
    // Spill management
    spillDir     string               // <datadir>/tmp_spill/<query_id>/
    numBuckets   int                  // Fixed 16
    rightFiles   []*os.File           // 16 temporary files for Inner
    leftFiles    []*os.File           // 16 temporary files for Outer
    
    // Active partition iterator
    curBucket    int
    curIter      *HashJoinIterator    // Reused standard in-memory HashJoin
    closed       bool
}
```

### 3.2 Detailed Execution Flow

#### Step 1: Normal In-Memory Attempt
- `HashJoinIterator`로 Inner 테이블 빌드를 시도합니다.
- 데이터가 `work_mem` 이내에 모두 들어가면 **디스크 I/O 0회(기존 고속 경로)**로 정상 완료됩니다.

#### Step 2: Trigger Spill on Overflow
- 빌드 도중 누적 크기가 `work_mem`을 초과하는 순간:
  1. `<datadir>/tmp_spill/` 하위에 고유 임시 디렉토리 생성.
  2. 이미 메모리에 적재된 Inner 튜플들과 나머지 Inner 스트림을 `Hash(Key) % 16`에 따라 16개 임시 파일(`inner_0.tmp` ~ `inner_15.tmp`)로 직렬화(Length-Prefixed Binary Frame)하여 저장.
  3. Outer 스트림을 순회하며 동일하게 `Hash(Key) % 16`으로 16개 임시 파일(`outer_0.tmp` ~ `outer_15.tmp`)로 분할 저장.

#### Step 3: Sequential Partition Join
- 파티션 번호 $p = 0 \dots 15$를 순회:
  1. `inner_p.tmp`를 읽어 인메모리 해시 맵 생성 (크기가 1/16로 축소되어 `work_mem` 안에 안전하게 들어감).
  2. `outer_p.tmp`를 읽으며 프로브하여 일치하는 조인 튜플을 상위로 방출 (`Next()`).
  3. 파티션 $p$ 조인이 끝나면 임시 파일 즉시 삭제하여 디스크 공간 회수.

#### Step 4: Cleanup (`[RULE:CleanTeardown]`, `[RULE:DoubleCloseSafety]`)
- 모든 파티션 순회가 끝나거나, 쿼리 도중 취소(Context Cancel) 또는 에러 발생 시:
  - `defer os.RemoveAll(spillDir)`로 임시 디렉토리 및 잔여 파일을 100% 원자적으로 영구 삭제.

---

## 4. Simplified vs Traditional Hybrid 비교

| 항목 | Traditional Hybrid Grace | **LionSQL Simplified Spill** |
| :--- | :--- | :--- |
| **코드 라인 수** | 약 3,500 ~ 5,000 라인 | **약 600 ~ 800 라인** |
| **파티션 수** | 카디널리티 기반 동적 계산 | **고정 16 버킷 (최대 1GB~2GB 커버)** |
| **인메모리 파티션 0** | 유지 (복잡한 상태 전이 필요) | **미유지 (일단 분할 후 순차 실행)** |
| **재귀 분할 (Skew)** | 재귀 분할 지원 (구현 극히 난해) | **16개로 부족할 시 32개 설정 가능 / OOM 방어** |
| **기존 코드 재사용** | 별도 전용 연산자 전체 재작성 | **기존 `HashJoinIterator` 100% 그대로 재사용** |
| **안정성 / 유지보수** | 회귀 버그 발생률 높음 | **버그 가능성 극도로 낮음 (`[RULE:RadicalKISS]`)** |

---

## 5. Integration with CBO & Backlog

- **스프린트 백로그 연동**:
  - `OLAP-OUT-OF-CORE-GRACE-HASH-JOIN-1`: `SPEC_OUT` (과도한 복잡도로 인한 전통적 Grace Hash Join 폐기)
  - `EXEC-SIMPLIFIED-SPILL-HASH-JOIN-1`: `PENDING` (`Post-1.0`, Prio 2, 신규 간소화 Spill Hash Join)
- **실행 선후 관계**:
  1. **1단계**: `PERF-PARALLEL-HASH-JOIN-1` (1-Build N-Probe 인메모리 병렬 조인) 우선 구현.
  2. **2단계**: `EXEC-SIMPLIFIED-SPILL-HASH-JOIN-1` (고정 16 버킷 디스크 스필 조인)을 `work_mem` 초과 시 폴백 연산자로 후속 구현.
- **CBO 플래너 연계**:
  - 기본 실행은 `1-Build N-Probe` (또는 단일 스레드 `HashJoin`)로 시작.
  - 런타임에 메모리 초과 시 에러로 중단되지 않고 `SpillHashJoin`으로 매끄럽게 폴백(Fallback)하도록 설계.
