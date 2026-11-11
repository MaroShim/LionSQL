# MemoryCompact Mode (`pkg/storage/memengine`) 2단계 Adversarial Audit 보고서

> - **최초 작성일**: 2026-11-11
> - **감사 대상**: `MemoryCompact` (FlatRecord) 모드 및 `ArenaRowCursor` 실행·DDL·영속성 전 경로
> - **기준 커밋**: `af21018` (`main`, `-memory=compact` 기본값 전환 직후)
> - **판정 요약**: **Critical 2건, High 2건, Medium/Low 2건** (즉시 수정 필수 Urgent P1 결함 포함)

---

## 1. Executive Summary

`MemoryCompact` 모드는 기본 설정 전환(`af21018`) 및 RDB/복합 PK/FK/TIME 코덱 보강으로 단일 스레드 기본 CRUD 및 재시작 복구 테스트를 통과했으나, 2단계 코드 감사 결과 **메모리 수명 경계(`Arena` 문자열 탈출), 범용 `ALTER TABLE` 스키마-레이아웃 불일치, 커서-DDL 동시성 레이스, 24비트 길이 무음 절삭** 등 프로덕션 데이터 무결성과 안전성을 위협하는 결함이 식별되었습니다.

특히 **Finding 1(`Arena` Use-After-Free)** 은 `PERF-OCC-FLAT-DECODE-1`(`a7d87b1`)에서 도입된 `ArenaRowCursor`와 `sqlexec_select.go` 사이의 수명 불일치로, `compact` 모드가 기본값으로 활성화된 상태에서 다중/연속 `SELECT` 수행 시 쿼리 간 문자열 메모리 오염을 일으킬 수 있는 최우선 수정 대상입니다.

---

## 2. Stage 1 Audit: 메모리 안전성 · 동시성 · 스키마 불변식 (Critical / High)

### Finding 1 [CRITICAL] `Arena.AllocStringBytes` 수명 탈출로 인한 Use-After-Free 및 쿼리 간 문자열 오염

- **발생 위치**:
  - [`pkg/storage/arena.go`](file:///Users/maro/Projects/SQLion/pkg/storage/arena.go#L165-L175) (`AllocStringBytes`), [`PutArena`](file:///Users/maro/Projects/SQLion/pkg/storage/arena.go#L86) / `Reset` ([L260-L262](file:///Users/maro/Projects/SQLion/pkg/storage/arena.go#L260-L262))
  - [`pkg/storage/memengine/flat_record.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/flat_record.go#L235) (`DecodeFlatToRowWithArena`, [L289](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/flat_record.go#L289))
  - [`pkg/execution/scan.go`](file:///Users/maro/Projects/SQLion/pkg/execution/scan.go#L116-L117) (`fillScanChunk`)
  - [`pkg/execution/iterator.go`](file:///Users/maro/Projects/SQLion/pkg/execution/iterator.go#L531) (`FormatRowWithArena`, `formatStorageVal` [L561-L565](file:///Users/maro/Projects/SQLion/pkg/execution/iterator.go#L561-L565), [`Tuple.ToMap`](file:///Users/maro/Projects/SQLion/pkg/execution/iterator.go#L227-L244))
  - [`pkg/engine/sqlexec_select.go`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_select.go#L112-L113), [L178-L189](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_select.go#L178-L189)
- **결함 메커니즘**:
  1. `SELECT` 실행 함수([`sqlexec_select.go`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_select.go#L112-L113))가 `arena := storage.GetArena()` 및 `defer storage.PutArena(arena)`를 설정합니다.
  2. [`fillScanChunk`](file:///Users/maro/Projects/SQLion/pkg/execution/scan.go#L116-L117)가 `arenaCur.RowWithArena(arena)`를 호출하면, [`DecodeFlatToRowWithArena`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/flat_record.go#L289)가 `TypeVarChar`/`TypeJSON`/`TypeUUID` 컬럼에 대해 `arena.AllocStringBytes(raw)`를 호출합니다.
  3. [`AllocStringBytes`](file:///Users/maro/Projects/SQLion/pkg/storage/arena.go#L172-L175)는 `arena` 내부 `byteSlabs` 버퍼 위에 `unsafe.String(unsafe.SliceData(buf), len(buf))`를 생성합니다.
  4. `formatStorageVal`([`iterator.go`](file:///Users/maro/Projects/SQLion/pkg/execution/iterator.go#L564-L565))은 `TypeVarChar`의 `v.SVal`을 복사 없이 그대로 반환하고, `sqlexec_select.go`는 `copy(vals, tuple.Values)`([L180-L181](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_select.go#L180-L181))와 [`Tuple.ToMap`](file:///Users/maro/Projects/SQLion/pkg/execution/iterator.go#L236-L240)으로 **Go string 헤더(포인터+길이)만 얕은 복사**하여 `QueryResult.Rows`와 `QueryResult.Values`에 담아 반환합니다.
  5. 함수 반환 직후 `defer storage.PutArena(arena)`가 `byteSlabPool.Put(slabPtr)`로 해당 슬랩 메모리를 풀에 반납합니다. 후속 또는 동시 쿼리가 같은 슬랩을 재할당해 덮어쓰는 순간, 반환된 `QueryResult`의 문자열 데이터가 즉시 손상됩니다.
- **권장 수정안**:
  - `QueryResult` 등 `Arena` 스코프 밖으로 탈출하는 모든 string 경계(`sqlexec_select.go` 결과 적재 시점 및 `Tuple.ToMap()`)에서 `strings.Clone`으로 힙 독립 복사본을 생성하거나, `DecodeFlatToRowWithArena`의 문자열 할당을 표준 Go 힙 할당(`string(raw)`)으로 전환하고 `Arena`는 슬라이스 백킹(`AllocValues`, `AllocAnySlice`)에만 제한해야 합니다.

---

### Finding 2 [CRITICAL] `ALTER TABLE DROP COLUMN` / `ALTER COLUMN TYPE` 시 `TableData.layout`·`flatBuf`·WAL 미갱신

- **연관 결함**: [`AUDIT-FULL-009`](file:///Users/maro/Projects/SQLion/notes/FULL_CODEBASE_ADVERSARIAL_AUDIT.ko.md) 확장
- **발생 위치**:
  - [`pkg/storage/memengine/ddl.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/ddl.go#L681-L695) (`Engine.AlterTable`)
  - [`pkg/engine/sqlexec_ddl.go`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_ddl.go#L1815) (`ALTER COLUMN ... TYPE`), [L1932](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_ddl.go#L1932) (`DROP COLUMN`)
- **결함 메커니즘**:
  1. `AddColumn`과 달리 범용 [`AlterTable`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/ddl.go#L681-L695)은 `td.mu.Lock()`을 잡지 않은 채 `mutator(&td.Schema)`와 `e.rebuildFKCacheLocked()`만 수행하고, `td.layout` 재계산·기존 샤드 `flatBuf` 재인코딩·WAL(`OpAlterTable`) 기록을 전혀 수행하지 않습니다.
  2. `DROP COLUMN` 실행 시 `td.Schema.Columns`는 줄어들지만 `td.layout.NumColumns`와 `FixedOffsets`는 구 스키마 상태 그대로 남아, 이후 모든 `DecodeFlatToRowWithArena` 및 `EncodeRowToFlat`에서 컬럼 인덱스가 어긋나 잘못된 슬롯을 읽거나 `ErrFlatRecordTruncated`/`ErrFlatRecordInvalid` 오류가 발생합니다.
  3. `ALTER COLUMN TYPE` 실행 시(예: `INT` <-> `VARCHAR`), 기존 8바이트 고정 정수 슬롯을 `decodeVarSlot`으로 해석하거나 반대로 해석해 데이터 손상 및 디코드 실패를 유발합니다.
  4. WAL 영속화가 누락되어 프로세스 재시작 시 스키마 변경 사항이 유실됩니다.
- **권장 수정안**:
  - `AlterTable` 내부에서 `td.mu.Lock()`을 획득하고 `mutator` 호출 전 `oldSchema := td.Schema.Clone()`을 보존한 뒤, 컬럼 수/타입 변경 시 `oldSchema`로 기존 `flatBuf`를 디코딩하여 새 `FlatRecordLayout`으로 재인코딩하고, 변경된 스키마 메타데이터를 `OpAlterTable` WAL에 기록해야 합니다.

---

### Finding 3 [HIGH] `memCursor` 지연 디코딩과 DDL(`AddColumn`/`AlterTable`) 간 데이터 레이스 및 디코드 실패 시 빈 행 덮어쓰기

- **연관 결함**: [`AUDIT-FULL-001`](file:///Users/maro/Projects/SQLion/notes/FULL_CODEBASE_ADVERSARIAL_AUDIT.ko.md#L44-L69) 확장
- **발생 위치**:
  - [`pkg/storage/memengine/tx.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L1347-L1348) (`Scan`), `memCursor.Row` / `RowWithArena` ([L2577](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L2577), [L2603](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L2603))
  - [`pkg/storage/memengine/ddl.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/ddl.go#L615-L629) (`AddColumn`)
  - [`pkg/storage/memengine/table_data.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/table_data.go#L378-L432) (`addColumnValues`)
- **결함 메커니즘**:
  1. `Scan`([L1347-L1348](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L1347-L1348))은 `tx.engine.mu.RLock()` 아래 `snapshotEntriesAndKeys`로 `shardEntry` 포인터만 수집하고 즉시 `RUnlock()`한 뒤 `memCursor`를 반환합니다.
  2. 실제 디코딩은 `c.td.layout`과 `&c.td.Schema`를 락 없이 읽어 수행하므로, 동시 `AddColumn`/`AlterTable`과 `-race` 데이터 레이스 및 Torn Decode(구 `flatBuf` + 신 `td.layout`)가 발생합니다.
  3. [`table_data.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/table_data.go#L398-L405)는 `addColumnValues` 중 `DecodeFlatToRow` 실패 시 에러를 반환하지 않고 `origRow = entry.row`(compact 모드에서는 빈 `Row{}`)로 폴백해 기존 행 데이터를 전부 `NULL`로 덮어씁니다(`[RULE:ExplicitError]` 위반).
  4. [`ddl.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/ddl.go#L623-L629)는 `td.Schema.Columns` 및 `addColumnValues` 변경 후 `recordWALLocked`가 실패해도 롤백하지 않습니다.
- **권장 수정안**:
  - `memCursor` 생성 시점에 불변 스키마 스냅샷과 `*FlatRecordLayout`을 함께 캡처해 디코딩이 live `td.Schema`/`td.layout`을 락 없이 참조하지 않도록 분리하고, `addColumnValues`는 `td.mu.Lock()` + 디코드 오류 즉시 반환(fail-closed)으로 전환해야 합니다.

---

### Finding 4 [HIGH] `encodeVarSlot`의 24비트 길이 무음 절삭 및 32비트 varHeap 오버플로우

- **발생 위치**: [`pkg/storage/memengine/flat_record.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/flat_record.go#L36-L42) (`encodeVarSlot`)
- **결함 메커니즘**:
  - 가변 컬럼 길이를 `uint32(len(raw))`로 변환한 뒤 3바이트(`slot[4:7]`, 최대 `16 MiB - 1`)만 기록합니다. `16 MiB` 이상의 `VARCHAR`/`JSON`/`VARBINARY` 값이 입력되면 에러 없이 `len & 0xFFFFFF`로 무음 절삭됩니다.
  - 행 내 `varHeap` 누적 크기가 `4 GiB`를 초과하면 `uint32(len(*varHeap))`가 오버플로우합니다.
- **권장 수정안**:
  - `len(raw) >= 1<<24` 또는 `len(*varHeap)+len(raw) > math.MaxUint32`일 때 명시적 오류(`ErrValueTooLarge` 등)를 반환하도록 가드를 추가해야 합니다.

---

## 3. Stage 2 Audit: 영속성 · 코덱 경계 · 검증 요약 (Medium / Low & Clean)

### Finding 5 [MEDIUM] `syncShardsToMapsLocked` 디코드 실패 시 빈 행 스냅샷 저장 및 스냅샷 락 경계

- **연관 결함**: [`AUDIT-FULL-004`](file:///Users/maro/Projects/SQLion/notes/FULL_CODEBASE_ADVERSARIAL_AUDIT.ko.md) 연계
- **발생 위치**:
  - [`pkg/storage/memengine/table_data.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/table_data.go#L301-L320) (`syncShardsToMapsLocked`)
  - [`pkg/storage/memengine/persistence.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/persistence.go#L340-L368) (`saveDiskSnapshotLocked`)
- **세부 사항**:
  - `syncShardsToMapsLocked`([L313](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/table_data.go#L313))가 `entry.materialize(td)` 실패를 무시하고 빈 `entry.row`를 `td.Rows`에 넣어 `tables.mpk` RDB 스냅샷을 오염시킬 수 있습니다. 또한 `saveDiskSnapshotLocked`에서 샤드 동기화와 테이블 직렬화 락 경계를 통합해 일관된 스냅샷을 보장해야 합니다.

### Finding 6 [MEDIUM/LOW] `EncodeRowToFlat` 방어적 타입 검증 누락 및 `TypeBinary` 디코드 할당 불일치

- **발생 위치**: [`pkg/storage/memengine/flat_record.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/flat_record.go#L196), [L296](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/flat_record.go#L296)
- **세부 사항**:
  - SQL DML 경로([`Insert`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L480-L530), [`Update`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L678-L731))는 강제 형변환을 보장하지만, `EncodeRowToFlat` 코덱 자체는 `TypeBool` 외 컬럼의 `val.Type == col.Type` 검증을 생략하므로 Go API 직접 호출 시 zero-value 오인코딩 위험이 있습니다.
  - `TypeBinary` 디코드([L296](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/flat_record.go#L296))는 `storage.BinaryValue(raw)`를 호출해 매번 힙 복사와 hex 문자열 변환을 수행합니다.

### 검증 통과 (Clean) 확인 영역
- `Insert`/`Update` DML 타입 강제 변환 및 `DECIMAL` scale 보정 일관성 ([`tx.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L480-L530), [L678-L731](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L678-L731))
- `DropTable`/`TruncateTables`/`DropPartition` 카노니컬 락 순서(`e.mu` -> `td.mu` -> `partMu`) ([`ddl.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/ddl.go#L360-L526))
- WAL Replay(`OpUpdate`/`OpDelete`) 시 `MemoryCompact` 인코딩 및 인덱스/파티션 키 동기화 ([`persistence.go`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/persistence.go#L231-L279))

---

## 4. 즉시 조치 체크리스트 (Urgent Pre-Commit)

1. [x] **`Arena` 문자열 수명 격리 (Finding 1)**: Option B (`DecodeFlatToRowWithArena` 표준 `string(raw)` + `TypeDecimal` 무복사 파싱) 적용 및 회귀 테스트 통과
2. [x] **`AlterTable` 스키마·레이아웃·인덱스·WAL 원자적 통합 (Finding 2)**: `trialSchema` 검증, Fail-Closed `ConvertValueToColumn`, PK/FK 차단 + 보조 인덱스 자동 동반 정리, `flatBuf`/`SkipList` 재구축 및 `OpAlterTable{Schema}` WAL 재생 통합 완료
3. [x] **`memCursor` 스키마/레이아웃 불변 캡처 & `addColumnValues` Fail-Closed (Finding 3)**: `memCursor` 생성 시 `schema`/`layout` 스냅샷 캡처 + `AddColumn` `applySchemaMigrationLocked` 통합·WAL 롤백·디코드 오류 전파 완료
4. [x] **`encodeVarSlot` 24-bit/32-bit 오버플로우 가드 (Finding 4)**: Option A (`16 MiB - 1` / `4 GiB - 1` 초과 시 `ErrFlatRecordTooLarge` 반환 + `ENGINEERING_CONTEXT` ㉗ 명문화) 완료
5. [x] **`syncShardsToMapsLocked` / `saveDiskSnapshotLocked` 무결성 가드 (Finding 5)**: `syncShardsToMapsAlreadyLocked` 디코드 실패 시 Fail-Closed 에러 전파, 단일 락 경계 통합 완료
6. [x] **`EncodeRowToFlat` 방어적 타입 검증 및 `TypeBinary` 디코드 정리 (Finding 6)**: `val.Type != col.Type` 비-NULL 값 `ConvertValueToColumn` 방어 가드 적용 및 `TypeBinary` 포맷 일관성 확정 완료

---

## 5. 합의된 아키텍처 결정 기록 (ADR / Design Decisions)

- **Finding 1 — Option B 확정 (`DecodeFlatToRowWithArena` Go 힙 문자열 + `TypeDecimal` 무복사 파싱)**:
  - `QueryResult` 탈출 경계에만 `strings.Clone`을 거는 Option A는 OLTP/일반 `SELECT`에서 오히려 2중 복사(`raw -> Arena -> Go 힙`)가 발생하고, 향후 다른 `GetArena()` 호출처 유입 시 무음 UAF 시한폭탄을 남기므로 **기각**.
  - Option B는 `[]Value`/`[]any` 슬라이스 풀링만 Arena를 쓰고 문자열 페이로드는 Go 힙(`string(raw)`)으로 할당해 구조적으로 UAF를 차단하며 WiredTiger(`DecodeTupleToRowWithArena`)와 수명 모델을 통일함. `TypeDecimal`은 즉시 파싱 후 버려지므로 슬랩 복사 없이 무복사 `unsafe.String` 뷰로 파싱함. (`join.go` 내부 해시 조인 키 `arena.AllocString`은 외부 탈출이 없어 유지.)
- **Finding 2 — `Engine.AlterTable` 원자적 마이그레이션 + Fail-Closed + 보조 인덱스 Cascade GC 확정**:
  - `AlterTable`은 live `td.Schema`를 직접 변조하지 않고 `trialSchema` 복제본에 `mutator`를 선적용해 실패 시 원본 무손상 보장.
  - `ALTER COLUMN TYPE` 시 기존 행 값을 대상 타입으로 변환할 때 단 1건이라도 변환 불가하면 **즉시 에러 반환 + 스키마/데이터/WAL 롤백 (Fail-Closed)** 확정.
  - `DROP COLUMN` 시 `PK`·`FK`(자신/타 테이블 참조)에 걸린 컬럼은 `ErrDependentObjectsExist`로 **엄격 차단**(k8s `Finalizer` 역할)하되, 단일 테이블 내 일반 보조 인덱스(`Schema.Indexes` / `td.Indexes`)는 파생 객체(k8s `OwnerReference` 기반 `Cascading Deletion`)로 간주해 **PostgreSQL 방식으로 자동 동반 정리**(유령 인덱스 잔존 0건 + PGWire ORM·T-SQL 모두 호환).
  - 이름/메타데이터만 변경(`RENAME COLUMN`, `SET/DROP NOT NULL`, `ADD/DROP CONSTRAINT`)되는 경우 바이너리 재인코딩 생략, 컬럼 삭제/타입 변경 시 `td.mu.Lock()` 아래 `FlatRecordLayout` 재계산 및 전 샤드 재인코딩, 비임시 테이블 `OpAlterTable{Schema}` WAL 영속화 및 `persistence.go` replay 통합.
- **Finding 3 — A + B 통합 확정 (`AddColumn` 원자화/Fail-Closed + `memCursor` 세대 일치 스냅샷)**:
  - **쓰기 측 (A)**: `AddColumn`을 Finding 2의 `applySchemaMigrationLocked` 2단계 원자 경로로 통합해 `td.mu.Lock()` 보호, 기존 행 디코드/인코드 실패 시 빈 행 덮어쓰기 대신 즉시 에러 반환(Fail-Closed), WAL 기록 성공 후에만 스키마·레이아웃·샤드를 커밋. 구형 WAL(`OpAlterTable{Column}`) 호환용 `addColumnValues`도 `(error)`를 반환하도록 Fail-Closed 전환.
  - **읽기 측 (B)**: `Scan`·`ScanPartitions`·`IndexScan`·`IndexScanCovering`의 `memCursor`가 엔트리를 조회한 시점과 동일 세대의 불변 `(schema, layout)`을 캡처/바인딩해, 동시 DDL 수행 중에도 data race 및 구/신 `flatBuf` 레이아웃 불일치를 원천 차단.
- **Finding 4 — Option A 확정 (`16 MiB - 1` / `4 GiB - 1` Fail-Closed 가드 + `ENGINEERING_CONTEXT` ㉗ 명문화)**:
  - Compact OCC(`FlatRecord`)는 `O(1)` 고정 슬롯 접근을 위해 `slot[7]`을 태그 바이트로 예약하고 `slot[4..6]`에 24비트 `vLen`을 기록하는 구조이며(순차 `PutUvarint`를 쓰는 WiredTiger `tuple.go`와 구분되는 Compact OCC 고유 특성), 이를 확장 헤더로 복잡화하지 않고 단일 가변 컬럼 `16 MiB - 1`(`0xFFFFFF`) 및 행 `varHeap` `4 GiB - 1` 초과 시 즉시 `ErrFlatRecordTooLarge`를 반환하는 Option A 확정. 상세 제약 계약을 [`ENGINEERING_CONTEXT.ko.md`](file:///Users/maro/Projects/SQLion/notes/ENGINEERING_CONTEXT.ko.md) §1 ㉗에 명문화.
- **Finding 5 — `syncShardsToMapsLocked` Fail-Closed & `saveDiskSnapshotLocked` 단일 락 경계 통합 확정**:
  - `syncShardsToMapsAlreadyLocked`는 디코드(`materialize`) 실패 시 빈 행 shells를 `td.Rows`에 넣고 은닉하는 대신 즉시 `(error)`를 반환해 손상된 스냅샷 덤프를 원천 차단 (Fail-Closed).
  - `saveDiskSnapshotLocked`는 테이블별 분할 lock/unlock 및 2차 RLock 재진입 사이의 갭을 제거하고, 정렬된 `tNames` 순서로 `td.mu.Lock() -> td.partMu.RLock()`을 잡은 단일 잠금 구간 안에서 [샤드 복사 ➔ 인덱스 동기화 ➔ 파일 직렬화]를 수행하며, 실패 시에도 `defer`로 맵 정리(`clearShardsSyncMapsAlreadyLocked`)와 unlock을 보장함.
- **Finding 6 — `EncodeRowToFlat` 방어적 형변환 가드 & `TypeBinary` 디코드 일치 확정**:
  - `EncodeRowToFlat`은 정상 핫 패스(`val.Type == col.Type`)에서 0 allocation을 유지하되, `!val.IsNull && val.Type != col.Type`인 경우 `storage.ConvertValueToColumn`으로 정규 형변환을 시도하고 변환 불가(`"not_a_num"` ➔ `INT`, `"invalid_date"` ➔ `DATE`) 시 즉시 명시적 에러를 반환.
  - `TypeBinary` 디코드([L324](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/flat_record.go#L324))의 `storage.BinaryValue(raw)`는 WiredTiger `tuple.go`의 `DecodeTupleValue`와 100% 동일한 `SVal: "0x"+hex` 표준 계약이므로 포맷을 현행 유지함.
