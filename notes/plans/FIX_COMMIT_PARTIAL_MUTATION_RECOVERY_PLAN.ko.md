# Commit 중도 실패 시 부분 변경 복구 누락 결함 수정 계획 (`FIX-COMMIT-PARTIAL-MUTATION-RECOVERY-1`)

## 1. 개요 및 배경
- **문제점**: LionSQL의 인메모리 OCC 스토리지 엔진(`pkg/storage/memengine/tx.go`)에서 `tx.Commit()` 수행 도중 보조 인덱스 삽입(`idxData.Insert`), 파티션 키 계산, 또는 WAL 기록 도중에 에러가 발생할 경우, 해당 변경 항목(`changeItem`)에 대해 이미 메모리에 반영된 샤드 엔트리(`sh.entries[key]`), 선행 보조 인덱스 키, 파티션 키 변경 사항이 정상 롤백되지 않고 누출(leak)되는 결함이 존재함.
- **원인 분석**:
  - `tx.Commit()`의 Phase B 루프(lines 2182~2289)에서 레코드 변경 반영(`sh.entries[key] = ...`), 파티션 키 갱신, 보조 인덱스 삽입(`idxData.Insert`)이 순차적으로 진행되는데, `appliedUndos` 등록은 해당 변경 항목의 **모든 처리가 끝난 뒤 루프 맨 마지막**(line 2280)에서 한 번만 수행됨.
  - 따라서 테이블에 여러 보조 인덱스가 존재할 때, 첫 번째 인덱스 삽입 후 두 번째 인덱스에서 에러가 발생하거나 파티션 키 계산에서 에러가 발생하면 `rollbackApplied()`가 호출되더라도 현재 항목은 `appliedUndos`에 포함되어 있지 않으므로:
    1. 샤드 엔트리(`sh.entries[key]`)가 갱신된 채로 방치됨.
    2. 선행 인덱스에 삽입된 신규 인덱스 키가 삭제되지 않고 방치됨.
    3. 파티션 키 변경이 복구되지 않음.
- **해결 방안**:
  1. **사전 계산(Pre-computation) 완료**: Flat Record 인코딩 및 파티션 키 계산(`ComputePartitionKeyFromRow`)을 Phase A/B 진입 전 사전 준비 루프에서 100% 완료하여, 계산 실패 시 상태 변경 전에 즉시 fail-fast 에러 반환.
  2. **미세 단위 LIFO Undo 로그 도입 (`commitUndoOp`)**:
     - 샤드 엔트리 갱신/삭제, 파티션 키 추가/삭제, 인덱스 키 삭제/삽입 등 **실제 인메모리 상태를 1회 변경할 때마다 즉시 대칭적인 Undo 연산(`commitUndoOp`)을 LIFO 스택(`undos`)에 등록**.
     - Phase A, Phase B, WAL 기록(`appendWALFrameLocked`), 동기화(`finishWALCommitLocked`) 중 어느 지점에서 에러가 발생하더라도 `rollbackApplied()`가 `undos` 스택을 역순 순회하여 부분 변경을 100% 원자적으로 완전 복구.
     - 슬라이스 기반 구조체 값 저장으로 GC 압력 최소화 (`[RULE:ZeroAllocation]`).

---

## 2. 세부 구현 계획

### 2.1 Undo 연산 구조체 정의
```go
type undoKind uint8
const (
    undoRestoreShardEntry undoKind = iota
    undoDeleteShardEntry
    undoAddPartitionKey
    undoRemovePartitionKey
    undoInsertIndexEntry
    undoDeleteIndexEntry
)

type commitUndoOp struct {
    kind     undoKind
    sh       *tableShard
    td       *TableData
    key      string
    partID   string
    idxData  *IndexData
    idxKey   storage.Value
    incVals  []storage.Value
    oldEntry shardEntry
}
```

### 2.2 준비 루프 (Preparation)
- 모든 `changeItem`에 대해 사전 계산:
  - `EncodeRowToFlat`
  - `oldPartID`, `newPartID` (`ComputePartitionKeyFromRow`)
  - 실패 시 즉시 에러 반환 (아직 아무 상태도 변경되지 않음)

### 2.3 Phase A (구 인덱스 키 삭제)
- 구 키 삭제 시 즉시 `undoInsertIndexEntry` 등록.

### 2.4 Phase B (샤드 엔트리 및 신규 인덱스 반영)
- `sh.entries` 변경 즉시 `undoRestoreShardEntry` 또는 `undoDeleteShardEntry` 등록.
- 파티션 키 변경 즉시 대칭 Undo 등록.
- 각 보조 인덱스 `idxData.Insert` 성공 즉시 `undoDeleteIndexEntry` 등록.
- `idxData.Insert` 실패 시 `rollbackApplied()` 호출 후 즉시 에러 반환.

---

## 3. 검증 계획
1. `TestMemEngine_CommitFailure_NoPartialMutationLeak`: PASS (Green 전환)
2. `go test -v -race ./pkg/storage/memengine/...` 100% PASS
3. `go test -v -race ./pkg/engine/...` 100% PASS
4. `./scripts/verify_no_slt_mutation.sh` 무결성 검증
5. `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql` 빌드 검증
