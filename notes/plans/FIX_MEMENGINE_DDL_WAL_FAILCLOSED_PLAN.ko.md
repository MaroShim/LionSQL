# DDL WAL 기록 실패 시 인메모리 카탈로그 복구 및 선기록 패턴 계획서 (`FIX-MEMENGINE-DDL-WAL-FAILCLOSED-1`)

## 1. 개요 및 배경
- **문제점**: LionSQL의 인메모리 스토리지 엔진(`pkg/storage/memengine/ddl.go`)에서 `CreateTable`, `DropTable`, `CreateIndex`, `DropIndex`, `TruncateTables`, `DropPartition`, `RenameTable` 수행 시, 메모리 맵(`e.tables`, `td.Indexes`)을 먼저 수정한 뒤 `recordWALLocked()`를 호출함. 만약 디스크 풀, I/O 에러 등으로 WAL 프레임 기록이 실패하면 에러를 반환하면서도 변경된 메모리 상태를 되돌리지 않아 카탈로그 불일치 및 고아 객체가 잔존하는 결함이 존재함.
- **해결 방안**:
  1. **Write-WAL-First 선기록 패턴 적용**: `TruncateTables`, `DropPartition`, `RenameTable`의 경우 메모리 샤드나 파티션을 파괴/이름변경하기 전에 WAL 기록을 먼저 수행하여, WAL 기록이 실패하면 원본 데이터와 테이블 맵이 전혀 손상되지 않도록 보장.
  2. **대칭적 롤백(Rollback on Error)**: `CreateTable`, `DropTable`, `CreateIndex`, `DropIndex`에서 WAL 기록 실패 시 즉시 변경된 `e.tables` 및 `td.Indexes`, `td.Schema.Indexes`, 외래키 캐시(`rebuildFKCacheLocked`)를 원래 상태로 완전 원복.

---

## 2. 세부 구현 내용 (`pkg/storage/memengine/ddl.go`)
1. **`CreateTable`**:
   `recordWALLocked` 실패 시 `delete(e.tables, schema.Name)` 호출 및 `e.rebuildFKCacheLocked()` 수행.
2. **`DropTable`**:
   `recordWALLocked` 실패 시 백업해 둔 `td`를 `e.tables[td.Schema.Name] = td`로 복원 및 `e.rebuildFKCacheLocked()` 수행.
3. **`CreateIndex`**:
   `recordWALLocked` 실패 시 `delete(td.Indexes, index.Name)` 및 `td.Schema.Indexes` 슬라이스에서 해당 인덱스 제거.
4. **`DropIndex`**:
   `recordWALLocked` 실패 시 삭제 전 백업해 둔 `backupIdxData`와 `backupSchemaIdx`를 `td.Indexes` 및 `td.Schema.Indexes`의 원래 인덱스 위치로 복원.
5. **`TruncateTables`**:
   샤드 엔트리 초기화 전에 `recordWALLocked(walOps)`를 선실행하여 실패 시 샤드 데이터를 보존.
6. **`DropPartition`**:
   파티션 행 삭제 및 인덱스 정제 전에 `recordWALLocked`를 선실행.
7. **`RenameTable`**:
   테이블 맵 및 외래키 참조 부모 테이블명 변경 전에 `recordWALLocked`를 선실행.

---

## 3. 검증 결과
- **단위 테스트**: `pkg/storage/memengine/ddl_wal_failclosed_test.go`
  - 6가지 DDL 연산(Create/Drop Table, Create/Drop Index, Truncate, Rename)에 대해 WAL 파일 강제 닫힘(I/O 실패) 상황을 시뮬레이션하고, 실패 후 메모리 카탈로그가 100% 무결하게 유지됨을 검증 (PASS).
- **회귀 테스트**: `go test -v -race ./pkg/storage/memengine` 100% PASS (0 data race).
