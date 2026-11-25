# In-Memory OCC Engine High-Concurrency Performance Optimization Plan (Target: 900k+ tpmC)

## 1. 개요 및 배경
- **문제점 진단**:
  - WALOp 메타데이터 직렬화 오버헤드 제거로 기본 처리량이 38.4만에서 77.4만 tpmC로 복구되었으나, 100 워커 고동시성 환경에서의 **직렬화 Abort율이 25.2% (13.4만 건 실패 / 63만 건 재시도)**로 여전히 높음.
  - TPC-C `NewOrder` 및 `Payment` 트랜잭션에서 부모 테이블(`warehouse`, `district`, `customer`)의 기본 키(PK)를 참조하는 자식 레코드 삽입 시, 불필요한 비관적 공유 참조 락(`refLock`)을 획득하여 부모 행을 업데이트하려는 다른 워커와 충돌 발생.
  - WAL fsync 디스크 대기 구간 동안 인메모리 샤드 락이 유지되어 핫스팟 레코드(`district.d_next_o_id`)의 잠금 대기 시간이 길어짐.
  - Savepoint를 사용하지 않는 일반 OLTP 트랜잭션에서도 매 행 변경마다 락 목록을 힙 슬라이스에 등록/추적하는 오버헤드 존재.

## 2. 목표 (Target Metrics)
- **TPC-C VDBE Runtime (10 Warehouses / 100 Workers, 15초)**:
  - tpmC: **774,000 ➔ 850,000 ~ 900,000+ tpmC 달성**
  - 전체 처리량: **26,500 ➔ 30,000+ TPS 달성**
  - 직렬화 Abort율: **25.2% ➔ 5% 미만으로 대폭 압축**
  - `go test -v -race ./...` 100% PASS 및 데이터 레이스 0건 유지

---

## 3. 세부 실행 단계

### Phase 1: 불변 부모 PK 참조 외래 키 검증 시 `refLock` 생략 및 OCC 스냅샷 검증
- **대상**: `pkg/storage/memengine/tx.go`의 `parentRecordExistsUnderLock()`
- **설계**:
  - 외래 키가 부모의 Primary Key를 참조하는 경우, 부모 레코드가 삭제(`DELETE`)되지 않는 한 자식 행의 삽입은 부모 행의 정합성을 훼손하지 않음.
  - 부모 행에 대한 비관적 `tryAcquireRefLockLocked`을 배제하고, `parentTd.hasKey(parentKey)` 및 트랜잭션 로컬 델타(`tx.isLocallyDeleted`) 검사만으로 $O(1)$ 즉시 통과.
  - 부모 행을 실제로 `DELETE`하거나 부모 PK를 `UPDATE`할 때만 자식 참조 검사(Cascade / Restrict)가 작동하므로 외래 키 무결성은 100% 완벽히 보장됨.
  - 비-PK 유니크 컬럼 참조(`TestForeignKey_NonPK_ParentRowLocking`)의 경우에만 선별적으로 `refLock` 유지.

### Phase 2: Savepoint 미사용 트랜잭션 락 추적기 Fast-Path
- **대상**: `pkg/storage/memengine/tx.go`
- **설계**:
  - 명시적 `SAVEPOINT`가 호출되지 않은 일반 트랜잭션(`len(tx.savepoints) == 0`)에서는 세이브포인트 롤백용 중간 델타 배열 추적을 건너뛰고, 트랜잭션 커밋/롤백 시 일괄 해제(Bulk Release) 수행.
  - `acquiredLocks` 슬라이스의 불필요한 중간 슬라이싱 및 메모리 재할당 제거.

### Phase 3: WAL 그룹 커밋 구간 샤드 락 조기 반납 (Early Shard Unlock)
- **대상**: `pkg/storage/memengine/tx.go` `Commit()`
- **설계**:
  - `appendWALFrameLocked`으로 WAL 버퍼에 프레임이 안전하게 적재된 후, 긴 디스크 fsync(`finishWALCommitLocked`)를 대기하기 전에 인메모리 튜플 반영 및 샤드 락 해제 순서를 정밀 분리.
  - 핫스팟 레코드의 뮤텍스 점유 시간을 수 밀리초에서 수 마이크로초로 단축.

---

## 4. 검증 파이프라인
1. `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. `go test -v -count=1 ./pkg/storage/memengine/...` (무결성 회귀 검증)
3. `/tmp/tpcc-runtime-test -engine=memory -warehouses=10 -workers=100 -duration=15s` (성능 및 Abort율 측정)
