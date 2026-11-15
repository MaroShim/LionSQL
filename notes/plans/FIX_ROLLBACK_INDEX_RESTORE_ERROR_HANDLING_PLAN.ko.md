# FIX-ROLLBACK-INDEX-RESTORE-ERROR-HANDLING-1: 롤백 과정 보조 인덱스 복원 오류 전파 및 복원 누락 방어 계획

## 1. 개요 및 배경
트랜잭션 롤백 과정에서 보조 인덱스(Secondary Index) 정합성 복원이 실패했을 때 오류를 은폐(`_ = err`)하거나, 세이브포인트 롤백(`RollbackTo`) 시 보조 인덱스의 복원/제거 처리가 누락되는 중대한 정합성 결함을 해결한다.

### 핵심 결함 위치
1. **`memengine/tx.go` (`rollbackApplied`)**:
   - `undoInsertIndexEntry`에서 기존 인덱스 키 복원 시 `_ = u.idxData.Insert(...)`로 오류가 무시됨.
   - 인덱스 복원이 실패하면 테이블 로우는 복구되었으나 인덱스는 누락되어 인덱스 손상이 발생하며, 상위 호출자에게는 롤백 실패가 보고되지 않음 (`[RULE:ExplicitError]` 위반).
2. **`wiredtiger/wt_engine.go` (`RollbackTo`)**:
   - `wtUndoUpdate`: `removeIndexEntry(idxCur, newKey)`로 신규 키는 지우나 에러를 무시하고, 원래 인덱스 키(`oldKey`)를 다시 `insertIndexEntry`로 복원하는 로직이 완전히 누락됨.
   - `wtUndoPut`: 삭제된 로우를 테이블에 복원할 때, 해당 로우의 보조 인덱스 엔트리를 복원하는 처리가 누락됨.
   - `wtUndoDelete`: 삽입된 로우를 테이블에서 제거할 때, 삽입되었던 보조 인덱스 엔트리를 제거하는 처리가 누락되어 유령 키(Ghost Key)가 잔류함.

---

## 2. 세부 구현 계획

### Phase 1: `memengine` 롤백 에러 전파 (`pkg/storage/memengine/tx.go`)
1. `rollbackApplied()` 시그니처를 `rollbackApplied() error`로 변경.
2. `undoInsertIndexEntry` 실행 중 에러 발생 시 `rbErrs` 슬라이스에 누적:
   ```go
   if err := u.idxData.Insert(u.idxKey, u.key, u.incVals); err != nil {
       rbErrs = append(rbErrs, fmt.Errorf("memengine: failed to restore index %s for key %s: %w", u.idxData.Schema.Name, u.key, err))
   }
   ```
3. `rollbackApplied()`가 반환하는 에러를 원래 커밋 실패 에러(`origErr`)와 `errors.Join`하여 반환:
   ```go
   if rbErr := rollbackApplied(); rbErr != nil {
       return fmt.Errorf("%w (critical rollback failure: %v)", err, rbErr)
   }
   return err
   ```
4. 단위 테스트: 인덱스 복원 중 강제 에러를 주입하여 원래 에러와 함께 롤백 실패 에러가 명시적으로 반환되는지 검증.

### Phase 2: `wiredtiger` 세이브포인트 인덱스 롤백 완결 (`pkg/storage/wiredtiger/wt_engine.go`)
1. **`wtUndoUpdate` 보조 인덱스 복원**:
   - `removeIndexEntry(idxCur, newKey)`의 반환 에러 확인.
   - `oldRec`로부터 `oldKey, oldVal`을 계산하여 `insertIndexEntry(idxCur, oldKey, oldVal)`를 호출하여 원본 인덱스 엔트리 복원.
2. **`wtUndoPut` (Delete 롤백 시 인덱스 복원)**:
   - `op.oldData`로부터 `oldRec`를 디코딩.
   - 테이블의 모든 `tbl.Indexes`에 대해 `formatIndexKeyValue(oldRec, &idx, pkStr)`를 구하고, 각 인덱스 커서에 `insertIndexEntry(idxCur, oldKey, oldVal)`를 호출하여 삭제되었던 인덱스 복원.
3. **`wtUndoDelete` (Insert 롤백 시 인덱스 제거)**:
   - `op.table`의 `op.pk` 로우를 삭제하기 전에 현재 데이터를 읽거나(또는 undoOp에 기록된 데이터로부터) `removeIndexEntry(idxCur, key)`를 호출하여 보조 인덱스 엔트리를 명시적으로 삭제.
   - 이를 위해 `wtUndoOp`에 `insertedRec []byte` 또는 레코드 정보를 저장하거나, `removeRawLocked` 직전에 로우를 읽어 인덱스 엔트리를 먼저 제거.
4. 단위 테스트:
   - `TestWiredTiger_Savepoint_Rollback_RestoresOriginalIndexKey`: Update 롤백 후 원본 인덱스 키로 정상 조회되는지 검증.
   - `TestWiredTiger_Savepoint_Rollback_RestoresDeletedIndexKey`: Delete 롤백 후 보조 인덱스로 정상 조회되는지 검증.
   - `TestWiredTiger_Savepoint_Rollback_RemovesInsertedIndexKey`: Insert 롤백 후 보조 인덱스에서 유령 키가 완전 제거되는지 검증.

---

## 3. 검증 전략 (`[RULE:PipelineVerification]`)
1. **Build**:
   `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Test**:
   - `go test -v -race ./pkg/storage/memengine/...`
   - `go test -v -race -tags wiredtiger ./pkg/storage/wiredtiger/...`
3. **Commit**:
   - `fix(storage): propagate index restore errors on rollback and complete wt savepoint index undo (FIX-ROLLBACK-INDEX-RESTORE-ERROR-HANDLING-1)`
