# Architecture Design & Implementation Plan: Parallel Hash Join (1-Build N-Probe) for Post-1.0

- **작성일**: 2026-11-19
- **작성자**: LionSQL Core Engine Team
- **상태**: PENDING (Post-1.0 Milestone)
- **태스크 ID**: `PERF-PARALLEL-HASH-JOIN-1` (Priority: P2)

> **Document Primacy & Rule References**:
> - `[RULE:PlanPersistence]`: Unconditional plan and architecture persistence to physical disk (`notes/*_PLAN.ko.md`).
> - `[RULE:ContinuousBacklog]`: Dual-sync registration in sprint SQLite DB (`PERF-PARALLEL-HASH-JOIN-1`) and `TASKS.md`.
> - `[RULE:RadicalKISS]`: Radical simplicity; operator-wide morsel parallelism is banned in OLTP; isolate parallelism strictly to Hash Join Probe Phase.
> - `[RULE:ZeroAllocation]`: Zero-allocation hot paths using `sync.Pool` and `Arena` buffer reuse.
> - `[RULE:ConcurrencySafety]`: Lock-free immutable build table reads across probe workers.

---

## 1. Current Hash Join Architecture Analysis (`pkg/execution/join.go`)

### 1.1 Implementation Characteristics
LionSQL의 기존 `HashJoinIterator`는 단일 스레드 Volcano Iterator 모델 기반입니다:
```go
type HashJoinIterator struct {
    left            Iterator                // Outer / Probe relation
    right           Iterator                // Inner / Build relation
    joinType        string                  // "INNER", "LEFT", "RIGHT", "FULL", "SEMI", "ANTI"
    leftKey         string                  // Outer join key column(s)
    rightKey        string                  // Inner join key column(s)
    hashTable       map[string][]hashMatch  // In-memory hash table of build tuples
    allRightTuples  []Tuple                 // Preserved for RIGHT / FULL outer joins
    matchedRight    []bool                  // Match flags for RIGHT / FULL outer joins
    workMem         int64                   // Memory governor threshold (default 64MB)
    buildMemory     int64                   // Accumulated memory usage
    // ...
}
```

### 1.2 Execution Lifecycle
1. **`Open(ctx)` (Build Phase)**:
   - `right` (Inner) Iterator를 열어 `PullChunk`로 모든 튜플을 소비.
   - 키 추출(`extractTupleKey`) 후 Go 맵 `hashTable[kStr]`에 적재.
   - `work_mem` 초과 시 Fail-Fast 에러 방출 (`governor_test.go`).
   - 적재 완료 후 `right.Close()` 호출.
   - `left` (Outer) Iterator를 열고 프로브 준비.
2. **`Next(ctx)` / `NextChunk(ctx, dest)` (Probe Phase)**:
   - `pullLeft(ctx)`를 통해 `left` (Outer) 관계로부터 튜플을 하나씩 또는 청크 단위(`inChunk`)로 인출.
   - `hashTable`에서 일치하는 `[]hashMatch`를 조회하여 `mergeTuplesWithArena`로 결합 튜플 생성.
   - `SEMI`, `ANTI`, `LEFT`, `RIGHT`, `FULL` 조인 시멘틱 처리.

### 1.3 Key Architectural Bottlenecks on Large Datasets
- **단일 코어 Probe 병목**: Outer 테이블이 수만~수백만 행일 때, 1개 CPU 코어만 100% 사용되고 나머지 30+개 코어가 유휴 상태(Idle)가 됨.
- **순차적 튜플 디코딩/매칭**: 대규모 테이블 조인 시 쿼리 레이턴시의 85% 이상이 Probe 단계의 키 추출 및 맵 조회에서 소비됨.

---

## 2. Parallel Hash Join Architecture (1-Build N-Probe)

### 2.1 Core Invariant & Safety Policy
1. **Single-Threaded Build (Zero 락 경합)**:
   - Inner 테이블은 CBO에 의해 더 작은 카디널리티로 선택되므로, 단일 스레드로 빠르게 Build하여 불변(Immutable) 해시 맵을 생성합니다.
   - 복잡한 Concurrent Hash Map이나 락이 필요 없습니다.
2. **Lock-Free Concurrent Probe**:
   - Build가 완료된 `hashTable`은 **읽기 전용(Read-Only)** 상태가 됩니다.
   - Go의 내장 `map[string][]hashMatch`는 읽기 전용 상태일 때 여러 고루틴이 락 없이 동시에 읽어도 데이터 레이스가 발생하지 않으며(`go test -race` 100% 통과), 가장 빠른 인라인 메모리 조회를 보장합니다.
3. **Morsel-Free Static/Dynamic Chunk Slicing**:
   - Outer 테이블을 `ChunkSize` (기본 4,096행) 단위의 청크로 워커 고루틴 풀에 분배합니다.
   - 각 워커는 독립적인 출력 버퍼에 결합 튜플을 생성하므로 워커 간 공유 상태가 0개입니다.

```
                  ┌────────────────────────┐
                  │   Right (Inner Table)  │
                  └───────────┬────────────┘
                              │ (Single Thread Build)
                              ▼
                  ┌────────────────────────┐
                  │ Immutable Hash Table   │ ◄─── Read-Only (Lock-Free)
                  └────────────────────────┘
                    ▲         ▲          ▲
                 Probe     Probe      Probe
                    │         │          │
              ┌─────┴───┐ ┌───┴────┐ ┌───┴────┐
              │Worker 1 │ │Worker 2│ │Worker N│  (max 8 workers)
              └─────▲───┘ └───▲────┘ └───▲────┘
                    │         │          │
                 Chunk 1   Chunk 2    Chunk K
              ┌───────────────────────────────┐
              │      Chunk Dispatcher         │
              └───────────────┬───────────────┘
                              │
                  ┌───────────┴────────────┐
                  │    Left (Outer Table)  │
                  └────────────────────────┘
```

---

## 3. Detailed Component Design

### 3.1 Data Structures (`pkg/execution/parallel_hash_join.go`)

```go
type ParallelHashJoinIterator struct {
    left         Iterator
    right        Iterator
    joinType     string // INNER, LEFT, SEMI, ANTI
    leftKey      string
    rightKey     string

    // Shared read-only build structure (Built in Open)
    hashTable    map[string][]hashMatch
    colIndexMap  *ColumnIndexMap
    workMem      int64

    // Worker configuration
    numWorkers   int             // min(runtime.GOMAXPROCS(0), 8)
    chunkSize    int             // 4096
    
    // Concurrency pipeline
    chunkInCh    chan *Chunk     // Outer chunks to probe
    chunkOutCh   chan *Chunk     // Joined result chunks
    errCh        chan error      // Fast-fail error channel
    stopCh       chan struct{}   // Cancellation broadcast
    wg           sync.WaitGroup
    
    // Iterator state
    curOutChunk  *Chunk
    curOutIdx    int
    closed       bool
}
```

### 3.2 Execution Lifecycle

1. **`Open(ctx)`**:
   - CBO가 전달한 `ParallelJoin` 플래그 및 임계치 검증 (Outer 행 추정치 >= 10,000 && CPU 코어 >= 4).
   - `right.Open(ctx)` 및 `right` 전체 드레인 -> `hashTable` 구축 -> `right.Close()`.
   - `left.Open(ctx)` 실행.
   - `numWorkers`개의 프로브 워커 고루틴 기동 (`wg.Add(numWorkers)`).
   - Outer 스캐너 고루틴 1개 기동: `left`로부터 `ChunkSize`씩 읽어 `chunkInCh`로 송신, 완료 시 `close(chunkInCh)`.

2. **Probe Worker Logic**:
   ```go
   func (p *ParallelHashJoinIterator) probeWorker(ctx context.Context) {
       defer p.wg.Done()
       for {
           select {
           case <-ctx.Done():
               return
           case <-p.stopCh:
               return
           case inChunk, ok := <-p.chunkInCh:
               if !ok {
                   return
               }
               outChunk := GetChunk()
               for _, probeTuple := range inChunk.Tuples {
                   kStr, found := extractTupleKey(probeTuple, p.leftKey, nil)
                   if found {
                       if matches, exists := p.hashTable[kStr]; exists {
                           for _, m := range matches {
                               joined := mergeTuples(probeTuple, m.tuple, p.colIndexMap)
                               outChunk.Append(joined)
                           }
                       } else if p.joinType == "LEFT" {
                           outChunk.Append(mergeNulls(probeTuple, p.colIndexMap))
                       }
                   }
               }
               PutChunk(inChunk) // Recycle input chunk
               
               // Non-blocking result push with context guard
               select {
               case p.chunkOutCh <- outChunk:
               case <-p.stopCh:
                   PutChunk(outChunk)
                   return
               }
           }
       }
   }
   ```

3. **`NextChunk(ctx, dest)`**:
   - `chunkOutCh`에서 완성된 `outChunk`를 꺼내 `dest`로 넘기거나 스왑(Zero-Copy).
   - 모든 워커가 종료되고 채널이 빌 때까지 반복, 고갈 시 `io.EOF`.

4. **`Close()`**:
   - `close(p.stopCh)`로 모든 고루틴 즉시 종료.
   - `p.wg.Wait()`로 고루틴 릭 원천 방지 (`[RULE:CleanTeardown]`, `[RULE:ZeroOrphan]`).
   - `left.Close()` 호출 및 잔여 청크 풀 반납.

---

## 4. Cost-Based Optimizer (CBO) Integration

`pkg/planner/cbo.go`에서 물리 조인 연산자 선택 시 조건부 분기:

```go
func (p *Planner) chooseJoinAlgorithm(left, right *PlanNode, joinCond Expr) NodeType {
    // 1. Unique index lookup on Inner -> Index Nested Loop Join (Fastest, O(1))
    if p.canUseIndexNestedLoop(right, joinCond) {
        return NodeIndexNestedLoopJoin
    }

    // 2. High-cardinality join -> Parallel Hash Join check
    if p.enableParallelJoin && 
       left.EstimatedRows >= 10000 && 
       runtime.GOMAXPROCS(0) >= 4 && 
       isEquiJoin(joinCond) {
        return NodeParallelHashJoin // Post-1.0 New Operator
    }

    // 3. Standard in-memory Hash Join
    return NodeHashJoin
}
```

---

## 5. Verification & Performance Validation Criteria

1. **Race-Free Verification (`[RULE:ConcurrencySafety]`)**:
   - `go test -v -race -run TestParallelHashJoin ./pkg/execution/...`
   - 동시 프로브 중 데이터 레이스 0건 검증.
2. **Throughput Scaling Metric**:
   - 100,000행 x 10,000행 조인 벤치마크:
     - 기존 단일 스레드 `HashJoinIterator`: ~45ms
     - `ParallelHashJoinIterator` (8 코어): <= 8ms (**5.5배 이상 가속**)
3. **Fail-Closed Memory Governor**:
   - Build 단계 `work_mem` 초과 시 기존 단일 스레드와 동일하게 `work_mem exceeded` 에러를 즉시 방출하는지 검증.

---

## 6. Sprint Milestone & Schedule

- **Task ID**: `PERF-PARALLEL-HASH-JOIN-1`
- **Milestone**: `Post-1.0`
- **Priority**: P2
- **Pre-requisite**: 1.0-GA 안정화 및 Zero-Alloc 아레나 연동 완료.
