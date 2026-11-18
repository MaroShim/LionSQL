# WiredTiger Fail-Fast Write-Write Conflict 방어 및 VDBE 에러 마스킹 수정 플랜

## 1. 개요 및 배경
- **현상**: 인메모리 OCC 엔진은 트랜잭션 1이 행을 수정하고 커밋하지 않은 상태에서 트랜잭션 2(또는 autocommit 세션)가 동일 행 수정을 시도할 때 **방법 B(즉시 직렬화 충돌 에러 `ErrSerializationFailure`를 내고 멈춤)**로 차단함. 그러나 타 컴퓨터 환경에서 WiredTiger 엔진은 충돌 에러가 감지되지 않고 통과(Success)되는 문제가 보고됨.
- **근본 원인**:
  1. **VDBE 에러 마스킹 버그 (`pkg/proc/runtime/vm.go:2038`)**: `execUpdateWhere`에서 `oldRec, err := f.tx.Get(targetTable, pkVal)` 조회 시 충돌 에러(`ErrSerializationFailure`) 등이 발생해도 이를 검사하지 않고 삼켜버리며 `return 0, nil` (0건 수정 성공)로 클라이언트에 통과시킴.
  2. **WiredTiger `getRawBytesLocked` 에러 뭉개기 (`pkg/storage/wiredtiger/wt_engine.go:3232`)**: C 커서의 반환 코드가 0이 아니면 그것이 충돌(`WT_ROLLBACK`)이어도 무조건 `storage.ErrKeyNotFound`로 변환함.
  3. **WiredTiger 엔진 내 Go 레벨 `activeWriters` 부재**: 인메모리 엔진과 달리 WiredTiger는 Go 레벨의 미커밋 행 잠금 추적 테이블이 없어 하부 C 라이브러리의 버전/OS/트랜잭션 격리 편차에 취약함.

## 2. 세부 구현 계획
1. **상세 회귀 테스트 스위트 작성 (Red)**:
   - `pkg/proc/runtime/vm_update_conflict_test.go`: `execUpdateWhere`에서 `Get` 시 에러 발생 시 0건 성공으로 은폐되지 않고 에러를 상위로 전파하는지 단위 검증.
   - `pkg/storage/wiredtiger/wt_active_writers_test.go`: WiredTiger 스토리지 레이어에서 미커밋 행에 대한 타 세션 쓰기가 즉시 `ErrSerializationFailure`로 Fail-Fast 차단되고, 커밋/롤백 시 잠금이 해제되는지 직접 검증.
   - `pkg/engine/coordinator_concurrency_wt_write_conflict_test.go`: autocommit 세션 2가 절대 성공(RowsAffected: 0 포함)하지 않고 직렬화 실패 에러를 반환하는지 종합 검증.
2. **코드 수정 (Green)**:
   - `pkg/proc/runtime/vm.go`: `execUpdateWhere`에서 `f.tx.Get` 실패 시 `ErrKeyNotFound`인 경우에만 0건 처리하고, 그 외 에러는 즉시 반환하도록 수정.
   - `pkg/storage/wiredtiger/wt_engine.go`:
     - `getRawBytesLocked`에서 `ret == wtRollback`인 경우 `storage.ErrSerializationFailure` 반환.
     - `activeWriters` (테이블/PK 단위 미커밋 쓰기 소유권 추적) 메커니즘을 `WTTx` 및 `Engine`에 적용하여 세션 1의 미커밋 변경에 대한 세션 2의 쓰기를 Go 레벨에서 즉각 차단.
3. **파이프라인 검증 및 커밋**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - `go test -v -tags wiredtiger -race ./pkg/storage/wiredtiger/... ./pkg/proc/... ./pkg/engine/...` 전수 PASS
   - Conventional Commit 및 GitHub Push
