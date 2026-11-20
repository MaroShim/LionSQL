# Sort Merge Join High-Performance Optimization Architecture & Plan (Post-1.0)

- **작성일**: 2026-11-19
- **작성자**: LionSQL Core Engine Team
- **상태**: PENDING (Post-1.0 Milestone)
- **태스크 ID**: `PERF-OPT-MERGE-JOIN-ARENA-AND-CHUNK-1` (Priority: P2)

> **Document Primacy & Rule References**:
> - `[RULE:PlanPersistence]`: Unconditional plan and architecture persistence to physical disk (`notes/*_PLAN.ko.md`).
> - `[RULE:ContinuousBacklog]`: Dual-sync registration in sprint SQLite DB (`PERF-OPT-MERGE-JOIN-ARENA-AND-CHUNK-1`) and `TASKS.md`.
> - `[RULE:ZeroAllocation]`: Zero-allocation hot paths using `storage.Arena` and chunk buffer reuse.
> - `[RULE:RadicalKISS]`: Keep Sort-Merge Join stream algorithm clean and deterministic; vectorize output emission without bloated state machines.
> - `[RULE:BoundedBuffers]`: Strict memory budget enforcement (`work_mem`) on duplicate-key buffer windows.

---

## 1. Background & Problem Statement

현재 LionSQL의 `MergeJoinIterator`(`pkg/execution/merge_join.go`)는 정렬된 두 관계를 $O(M+N)$ 1-Pass 스트리밍으로 조인하는 훌륭한 기반을 갖추고 있습니다.
그러나 최신 `HashJoinIterator`나 다른 연산자들에 적용된 핵심 고속화 기법들이 누락되어 있어 대용량 정렬 조인 시 다음과 같은 병목이 발생합니다:

1. **`NextChunk(ctx, dest)` 부재 (단일 튜플 `Next()` 오버헤드)**:
   - 모든 상위 연산자(`ProjectionIterator`, `FilterIterator`)는 청크 단위로 소비할 수 있으나, Merge Join에 `NextChunk`가 없어 매 행마다 Go 인터페이스 가상 함수 호출(`Next()`)이 발생하여 CPU 분기 예측 실패 및 호출 오버헤드가 누적됩니다.
2. **`Arena` 제로-GC 할당기 미연동 (매칭 결합 시 힙 메모리 할당)**:
   - `mergeTuples`가 매칭된 결과 튜플을 생성할 때마다 `make([]storage.Value, count)`를 호출하여 Go 런타임 힙(Heap)에 슬라이스를 할당합니다.
   - 10만~100만 행 조인 시 수십~수백 MB의 불필요한 단명 객체가 생성되어 가비지 컬렉션(GC STW) 지연을 유발합니다.
3. **`CompareValues` 타입 분기 오버헤드**:
   - `compareJoinKeys`가 매 스텝마다 양쪽 튜플의 타입을 동적으로 판별하고 비교합니다. 정수(INT/BIGINT) 단일 키 조인이 대다수인 OLTP 환경에서 불필요한 리플렉션성 분기 비용이 발생합니다.

---

## 2. Optimization Design & Key Implementations

### 2.1 Vectorized Batch Output (`NextChunk` Implementation)

단일 튜플 단위 `Next()` 호출을 4,096행 배치 단위 `NextChunk`로 전환합니다:
- `dest *Chunk`를 상위로부터 전달받아, 루프 내부에서 연속 매칭 튜플들을 `dest.Append(...)`로 직접 채웁니다.
- 4,096행이 채워지거나 스트림 끝(`io.EOF`)에 도달했을 때 단 한 번 반환하여 Volcano 함수 호출 횟수를 1/4096로 축소합니다.

```go
func (m *MergeJoinIterator) NextChunk(ctx context.Context, dest *Chunk) error {
    dest.Reset()
    for dest.Len() < DefaultChunkSize {
        tuple, err := m.Next(ctx)
        if err != nil {
            if err == io.EOF && dest.Len() > 0 {
                return nil
            }
            return err
        }
        dest.Append(tuple)
    }
    return nil
}
```

### 2.2 Zero-Allocation via `storage.Arena` (`SetArena`)

`HashJoin`과 동일하게 `storage.Arena`를 연동하여 결과 튜플의 `Values` 슬라이스를 아레나 블록에서 연속 할당합니다:

```go
func (m *MergeJoinIterator) SetArena(a *storage.Arena) {
    m.arena = a
}
```
- 기존 `mergeTuples` 호출을 `mergeTuplesWithArena(m.curLeft, m.curRight, m.colIndexMap, m.arena)`로 전면 교체.
- 외부 조인(`LEFT`, `RIGHT`, `FULL`)의 NULL 패딩 시에도 `mergeNullsWithTupleAndArena` 적용.

### 2.3 Integer Key Fast-Path Comparison

단일 정수 키(`TypeInt`) 조인에 대해 전용 비교 패스트패스 도입:
- `leftKey` 및 `rightKey`가 단일 컬럼이고 두 컬럼의 인덱스가 사전 바인딩(`leftKeyIdx`, `rightKeyIdx`)되어 있을 때:
  ```go
  if m.isIntFastPath {
      vL := m.curLeft.Values[m.leftKeyIdx]
      vR := m.curRight.Values[m.rightKeyIdx]
      if vL.IsNull || vR.IsNull {
          // Null handling
      } else if vL.IVal < vR.IVal {
          cmp = -1
      } else if vL.IVal > vR.IVal {
          cmp = 1
      } else {
          cmp = 0
      }
  }
  ```

---

## 3. Plan & Verification Checklist

- [ ] **Phase 1 (`NextChunk` 벡터화 구현)**:
  - `pkg/execution/merge_join.go`에 `NextChunk(ctx context.Context, dest *Chunk) error` 구현.
  - 상위 노드가 청크 배치로 인출할 수 있도록 지원.
- [ ] **Phase 2 (`Arena` 연동 및 힙 할당 제거)**:
  - `MergeJoinIterator` 구조체에 `arena *storage.Arena` 필드 및 `SetArena` 메서드 추가.
  - 튜플 결합 시 `mergeTuplesWithArena` 및 `mergeNullsWithTupleAndArena` 적용.
- [ ] **Phase 3 (정수 키 인덱스 사전 바인딩 및 Fast-Path)**:
  - `Open()` 시점에 컬럼 인덱스 맵에서 `leftKeyIdx`, `rightKeyIdx` 사전 확인 및 `isIntFastPath` 플래그 설정.
- [ ] **Phase 4 (성능 벤치마크 및 회귀 테스트)**:
  - `BenchmarkMergeJoin_100k` 벤치마크 작성 (B/op 및 allocs/op 80%+ 절감 검증).
  - 기존 회귀 테스트(`merge_join_test.go`, `merge_join_regression_test.go`) 100% PASS 검증.

---

## 4. Sprint Task Metadata

- **Task ID**: `PERF-OPT-MERGE-JOIN-ARENA-AND-CHUNK-1`
- **Domain**: `Execution`
- **Priority**: `P2`
- **Milestone**: `Post-1.0`
- **Status**: `PENDING`
