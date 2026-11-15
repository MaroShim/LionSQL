#同一 트랜잭션 내 Unique 키 해제 후 재사용 충돌 방지 계획 (`FIX-TX-UNIQUE-KEY-RELEASE-COLLISION-1`)

## 1. 개요 및 배경
- **문제점**: LionSQL의 인메모리 OCC 스토리지 엔진(`pkg/storage/memengine`)에서 동일 트랜잭션 내에 기존 행의 unique key를 해제(DELETE 또는 UPDATE로 다른 값으로 변경)한 후, 동일 트랜잭션에서 다른 행이 그 해제된 unique key로 INSERT 또는 UPDATE를 수행하면 허위 `ErrUniqueViolation`("duplicate key in unique index") 에러가 발생하거나 커밋 단계에서 검증에 실패하는 결함이 발생함.
- **원인 분석**:
  1. **DML 검증 단계 (`tx.Insert`, `tx.Update`)**:
     `idxData.ScanRange(&keyVal, &keyVal)`로 기존 커밋된 인덱스를 조회할 때, `!tx.isLocallyDeleted(tableName, epk)`만 확인하고 있음. 만약 `epk` 행이 동일 트랜잭션 내에서 `tx.local[tableName]`을 통해 다른 unique key 값으로 UPDATE된 경우(`locKeyVal != keyVal`), `isLocallyDeleted`는 `false`이므로 해당 행이 아직 이전 키를 점유하고 있는 것으로 오판하여 `ErrUniqueViolation`을 반환함.
  2. **커밋 검증 단계 (`tx.Commit`)**:
     트랜잭션 커밋 시 `changeInsert` 및 `changeUpdate` 체인지셋을 실제 인덱스에 반영하기 전 unique 제약조건을 재검증(`idxData.ScanRange`)함. 그러나 이때 `isLocallyDeleted` 및 `tx.local` 업데이트 상태를 전혀 반영하지 않고 커밋된 인덱스 트리만 조회하므로, 동일 트랜잭션 내에서 삭제되었거나 키가 변경된 행 `epk`를 충돌 대상으로 오인하여 커밋이 거부됨.
- **해결 방안**:
  - DML 검증 및 Commit 검증 로직 모두에서, `existing` 행 `epk`가 동일 트랜잭션 내에서 삭제(`tx.isLocallyDeleted`)되었거나, `tx.local`에서 해당 unique 인덱스 컬럼의 값이 다른 값으로 업데이트되어 현재 검사 중인 `keyVal`과 일치하지 않는 경우(`!locKeyVal.Equal(keyVal)`), 이미 해제된 키로 판단하여 충돌 검사에서 정상 제외(pass) 처리.

---

## 2. 세부 구현 계획

### 2.1 TDD 검증 테스트 케이스 (`pkg/storage/memengine/tx_unique_workspace_test.go`)
1. **Delete 후 동일 unique key Insert**: 한 행을 삭제하고 새 행에 해당 unique key 할당 후 커밋 성공 검증.
2. **Update away 후 동일 unique key Insert**: 기존 행의 unique key를 다른 값으로 변경한 후 새 행에 원본 unique key 할당 후 커밋 성공 검증.
3. **Update away 후 다른 행에 원본 unique key Update**: 행 A의 unique key를 임시 값으로 변경하고, 행 B의 unique key를 행 A의 원본 키로 변경 후 커밋 성공 검증.
4. **실제 미해제 중복 검사 유지**: 해제되지 않은 기존 unique key에 대한 충돌은 여전히 엄격하게 `ErrUniqueViolation`을 발생시키는지 검증.

### 2.2 `pkg/storage/memengine/tx.go` 수정 사항
1. **헬퍼 함수 추가/정리**:
   동일 트랜잭션 내에서 행 `epk`가 해당 unique 인덱스의 `keyVal`을 여전히 점유하고 있는지 확인하는 헬퍼:
   `tx.isIndexKeyLocallyReleased(td *tableData, idx *indexData, epk string, keyVal storage.Value) bool`
   - 행이 `isLocallyDeleted`인 경우 -> true (해제됨)
   - 행이 `tx.local`에 존재하고, 업데이트된 행의 인덱스 키 값이 `keyVal`과 일치하지 않는 경우 -> true (해제됨)
   - 그 외 -> false (여전히 점유 중)
2. **`tx.Insert` 적용**:
   unique 인덱스 검사 루프에서 `isIndexKeyLocallyReleased`가 true이면 continue.
3. **`tx.Update` 적용**:
   unique 인덱스 검사 루프에서 `isIndexKeyLocallyReleased`가 true이면 continue.
4. **`tx.Commit` 적용**:
   `changeInsert` 및 `changeUpdate` 유효성 검사 루프에서 `isIndexKeyLocallyReleased`가 true이면 continue.

---

## 3. 검증 파이프라인
1. `go test -v -race -run 'TestMemEngine_UniqueKeyReleaseInSameTx' ./pkg/storage/memengine` ➔ PASS (Green)
2. `go test -v -race ./pkg/storage/memengine/...` ➔ PASS
3. `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql` ➔ 빌드 검증
4. `./scripts/verify_no_slt_mutation.sh` ➔ 오라클 무결성 검증
