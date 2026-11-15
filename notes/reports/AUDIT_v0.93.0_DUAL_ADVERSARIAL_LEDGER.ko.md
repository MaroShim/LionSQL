# LionSQL v0.93.0 릴리즈 사전 듀얼 AI (Antigravity × Claude) 적대적 코드 감사 기록부

> - **문서 식별자**: `AUDIT-093-20261115`
> - **작성일**: 2026-11-15
> - **감사 범위**: `v0.92.0..HEAD` 변경 및 신규 추가된 전체 프로덕션 Go 소스코드 (총 111개 프로덕션 파일, 6개 도메인 전수 라인 단위 감사)
> - **감사 모드**: Line-by-Line Dual-AI Adversarial Audit (Antigravity 1차 아키텍처/로직 전수 분석 × Claude 2차 적대적 공격 및 엣지 케이스 교차 검증)
> - **준수 규약**: `doc/RELEASE_AUDIT_PROTOCOL.ko.md` 및 `notes/FULL_CODEBASE_ADVERSARIAL_AUDIT.ko.md`

---

## 1. 감사 진행 현황 대시보드 (Audit Summary Dashboard)

* **감사 완료 도메인 (`v0.92.0..HEAD` 111개 프로덕션 파일 100% 전수 조사)**:
  - **Domain 1 (`pkg/storage`, `pkg/storage/memengine`, `pkg/storage/wiredtiger`, `pkg/storage/dirlock`)**: 25개 프로덕션 파일 전수 조사 완료 (`TupleCodecV2`, WAL 리플레이, MVCC/OCC 트랜잭션, Savepoint, 파티션, 디렉토리 락)
  - **Domain 2 (`pkg/proc`, `pkg/proc/runtime`, `pkg/proc/parser/antlr`)**: 15개 프로덕션 파일 전수 조사 완료 (VDBE VM 디스패처, 서브프레임 트랜잭션 전파, ANTLR4 폴백 파서, 바이트코드 컴파일러 레지스터 할당)
  - **Domain 3 (`pkg/engine`, `pkg/catalog`, `pkg/sqlutil`)**: 33개 프로덕션 파일 전수 조사 완료 (`ALTER TABLE` DDL, 가상 카탈로그 뷰, 세션 동시성, MERGE, 저장 프로시저 실행)
  - **Domain 4 (`pkg/execution`, `pkg/planner`, `pkg/udf`)**: 12개 프로덕션 파일 전수 조사 완료 (Volcano 실행 연산자, 집합 연산 플래너, UDF SPI)
  - **Domain 5 (`pkg/wire`, `pkg/wire/tds`)**: 9개 프로덕션 파일 전수 조사 완료 (PGWire `COPY IN`, TDS 7.x/8.0 `ROW`/`NBCROW`/`RETURNVALUE` 직렬화)
  - **Domain 6 (`pkg/api`, `pkg/dbmail`, `pkg/webhook`, `pkg/cdc`, `pkg/config`, `cmd/lionsql`)**: 17개 프로덕션 파일 전수 조사 완료 (독립형 워커 데몬, DB Mail SMTP 헤더 보안, API 서버 라이프사이클, CLI 매니페스트 가드)
* **발굴 및 완치된 결함 총계**: **총 22건 (전건 소스코드 패치 및 TDD 회귀 테스트 검증 완료 — 100% Resolved)**
  - **Critical (P0)**: 2건 (`AUDIT-093-001`, `AUDIT-093-002`)
  - **High (P0/P1)**: 14건 (`AUDIT-093-003`, `AUDIT-093-004`, `AUDIT-093-005`, `AUDIT-093-006`, `AUDIT-093-007`, `AUDIT-093-008`, `AUDIT-093-012`, `AUDIT-093-013`, `AUDIT-093-014`, `AUDIT-093-018`, `AUDIT-093-019`, `AUDIT-093-020`, `AUDIT-093-022`)
  - **Medium / Low (P1/P2)**: 6건 (`AUDIT-093-009`, `AUDIT-093-010`, `AUDIT-093-011`, `AUDIT-093-015`, `AUDIT-093-016`, `AUDIT-093-017`, `AUDIT-093-021`)

---

## 2. 도메인별 적대적 감사 상세 기록 (Detailed Adversarial Findings)

### Domain 1: Storage Engine (`pkg/storage`, `memengine`, `wiredtiger`)

---

#### [AUDIT-093-001] `pkg/storage/memengine/tx.go:2516-2633` & `pkg/storage/wiredtiger/wt_engine.go:3405-3409`
* **대상 영역**: T-SQL 테이블 변수(`@tv`) 트랜잭션 커밋/롤백 경계 보존(`commitTableVariablesLocked`) 및 `WTTx.Commit()` 임시 트랜잭션 핸들 반환
* **Antigravity 1차 분석**:
  - T-SQL 표준에서 테이블 변수(`@tv`)는 일반 사용자 트랜잭션의 `ROLLBACK`에 영향을 받지 않아야 하므로, `memengine`의 `Commit()`과 `Rollback()` 양쪽 모두에 `tx.commitTableVariablesLocked()` 호출이 추가되었음.
  - `wiredtiger`의 `WTTx.Commit()`에서도 `tempTx`에 기록된 테이블 변수 변경분을 `tempTx.Commit()`으로 커밋함.
* **Claude 2차 적대적 교차 분석**:
  - **치명적 결함 1 (테이블 변수 이중 반영 및 커밋 메타데이터 소실)**:
    - LionSQL 전역 코드베이스의 표준 관용구는 `tx, _ := sess.BeginTx(...); defer tx.Rollback(); ...; tx.Commit()` 패턴임.
    - `tx.Commit()`이 성공한 후 `tx.closed = true`로 설정되지만 `tx.writes`, `tx.deletes`, `tx.insertOrder` 맵/슬라이스는 초기화되지 않은 채 유지됨.
    - 함수 종료 시 `defer tx.Rollback()`이 실행되면 `if tx.closed { tx.commitTableVariablesLocked(); tx.releaseToPool(); return nil }` 분기를 타게 됨!
    - 그 결과:
      1. 이미 `Commit()`에서 반영된 `@tv` 변경분이 `defer tx.Rollback()`에서 **두 번째로 중복 적용**되어 `rowCount`가 2배로 뻥튀기됨.
      2. `tx.releaseToPool()`이 호출되면서 `tx.commitLSN = 0`, `tx.commitTimestamp = 0`으로 덮어써져 호출자가 `tx.CommitLSN()`을 조회할 때 0을 반환받음.
      3. 반대로 `Commit()` 도중 OCC 충돌(`ErrSerializationFailure`)이나 Unique 제약조건 위반으로 커밋이 실패하여 조기 반환될 때는 `tx.closed = true`만 설정되고 `commitTableVariablesLocked()`가 호출되지 않은 채 반환되어, 이후 `defer tx.Rollback()`이 호출되지 않는 경로에서는 `@tv` 변경분이 유실됨.
  - **치명적 결함 2 (`WTTx.Commit()`의 `tempTx` 풀 누수)**:
    - `WTTx.Rollback()`은 `_ = tempTx.Commit(); _ = tempTx.Rollback()`을 모두 호출하여 `tempTx`를 풀에 반납(`releaseToPool`)하지만, `WTTx.Commit()`(`wt_engine.go:3406`)은 `_ = tempTx.Commit()`만 호출하고 `_ = tempTx.Rollback()`을 누락하여 `tempTx` 객체가 `txPool`로 반환되지 않고 영구 누수됨.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Critical (P0)`
  - **패치 내용**:
    1. `memengine.Transaction`에 `committed bool` 필드를 추가하여, `Commit()`이 성공하기 직전 모든 검증을 통과한 시점과 실패하여 중단(`abortAndPreserveTableVars`)되는 시점에 각각 정확히 1회만 `commitTableVariablesLocked()`를 수행하도록 수정.
    2. 이미 닫힌 트랜잭션(`if tx.closed`)에 대한 후속 `Rollback()` 호출에서는 `if !tx.committed`인 경우에만 `releaseToPool()`을 호출하고 즉시 `nil`을 반환하도록 수정하여 이중 반영과 `commitLSN` 초기화를 원천 차단.
    3. `WTTx.Commit()`에서 `_ = tempTx.Commit()` 직후 `_ = tempTx.Rollback()`을 호출하여 풀 반납 보장.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-002] `pkg/storage/wiredtiger/wt_engine.go:3559-3628, 3727-3741`
* **대상 영역**: WiredTiger `RollbackTo(savepoint)` 시 `wtUndoUpdate` 세컨더리 인덱스 복원 및 `rowDeltas` 테이블 키 정규화
* **Antigravity 1차 분석**:
  - `RollbackTo`는 `tx.undoLog`를 역순 순회하며 `wtUndoInsert`, `wtUndoUpdate`, `wtUndoDelete`를 복원함.
  - `wtUndoUpdate`는 `tx.putRawBytesLocked(tbl, op.pk, op.oldRaw)`를 호출하여 이전 튜플 바이트를 복원함.
* **Claude 2차 적대적 교차 분석**:
  - **치명적 결함 1 (세이브포인트 롤백 시 변경되지 않은 Unique 인덱스 키 증발 및 고아 인덱스 엔트리 잔류)**:
    - `Update` 실행 시 `updateSecondaryIndexesLocked(tbl, pk, oldRow, newRow)`가 호출되어 `oldRow`의 인덱스 엔트리를 지우고 `newRow`의 인덱스 엔트리를 삽입함.
    - 이후 `RollbackTo`에서 `wtUndoUpdate`가 실행될 때, `putRawBytesLocked`는 **새로 삽입된 `newRow`의 세컨더리 인덱스 엔트리를 삭제하지 않은 채** `oldRow`의 모든 인덱스에 대해 무조건 `insertIndexEntryLocked`를 덮어씀!
    - 이로 인해:
      1. 업데이트된 컬럼의 새 인덱스 키(`newRow`)가 B-Tree에서 지워지지 않고 **고아(Orphan) 인덱스 엔트리**로 남아, 존재하지 않는 값으로 인덱스 스캔 시 행이 잘못 조회됨.
      2. 만약 테이블에 변경되지 않은 다른 `UNIQUE` 인덱스 컬럼(예: `email`)이 존재할 경우, `Update` 시 최적화(`oldIdxKey != newIdxKey`)로 인해 지우지 않았던 Unique 인덱스 키에 대해 `wtUndoUpdate`가 `insertIndexEntryLocked`를 덮어쓰고, 뒤이어 `Update` 때 기록된 `wtUndoIndexInsert`가 실행되면서 해당 Unique 인덱스 키를 **`remove()`로 삭제해 버림**! 결과적으로 세이브포인트 롤백 후 원래 행의 Unique 인덱스 키가 완전히 증발함.
  - **결함 2 (`tx.rowDeltas` 대소문자 불일치)**:
    - `Insert`/`Delete`는 정규화된 `tbl.Name`을 키로 `tx.rowDeltas[tbl.Name]`을 증감하지만, `RollbackTo`는 비정규화된 `op.table`을 키로 `tx.rowDeltas[op.table]`을 차감하여 대소문자가 다를 경우 테이블 행 수(`rowCounts`)가 어긋남.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Critical (P0)`
  - **패치 내용**:
    1. `putRawBytesLocked(tbl, pk, rawBytes, updateIndexes bool)`로 시그니처를 분리하고, `wtUndoUpdate`에서는 `updateIndexes = false`로 호출한 뒤 `oldRow`와 `newRow`가 모두 존재할 때 `tx.updateSecondaryIndexesLocked(tbl, op.pk, newRow, oldRow)`를 호출하여 변경된 인덱스만 정확히 역방향 복원하도록 수정 (`wtUndoDelete`는 `updateIndexes = true` 유지).
    2. `RollbackTo`의 `wtUndoInsert`/`wtUndoDelete`에서 정규화된 `tbl.Name`을 사용해 `tx.rowDeltas[tbl.Name]`을 갱신하도록 수정.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-003] `pkg/storage/memengine/persistence.go:205-208, 238-247`
* **대상 영역**: `applyWALRecordLocked` WAL 리플레이 시 `DecodeTupleToRow` 디코딩 에러 처리
* **Antigravity 1차 분석**:
  - `OpInsert` 및 `OpUpdate` WAL 레코드 재생 중 `storage.DecodeTupleToRow`를 호출함.
* **Claude 2차 적대적 교차 분석**:
  - `OpInsert`: `DecodeTupleToRow`가 에러를 반환할 때 에러를 무시하고 `continue`로 넘어가 레코드가 조용히 유실(Silent Data Drop)됨 (`[RULE:ExplicitError]`, Invariant 9 위반).
  - `OpUpdate`: `DecodeTupleToRow`를 호출하기 전에(239라인) 먼저 `if !hadOld { sh.keys = append(sh.keys, k) }`를 실행한 뒤, 244라인에서 디코딩이 실패하면 `continue`로 빠져나가 `sh.keys`에는 키가 등록되었으나 `sh.entries`에는 엔트리가 없는 불일치 상태가 발생함.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P0)`
  - **패치 내용**: `OpInsert` 및 `OpUpdate`에서 `DecodeTupleToRow` 실패 시 즉시 명시적 에러(`fmt.Errorf("WAL replay OpInsert/OpUpdate decode failed on table %s key %s: %w", ...)`)를 반환(Fail-Closed)하고, `OpUpdate`의 `sh.keys` 추가를 디코딩 성공 이후로 이동.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-004] `pkg/storage/memengine/wal.go:459-462`
* **대상 영역**: `ReplayWAL` 실행 중 `w.file.Stat()` 실패 처리
* **Antigravity 1차 분석**:
  - `ReplayWAL`에서 Torn Frame 검사를 위해 `w.file.Stat()`을 호출함.
* **Claude 2차 적대적 교차 분석**:
  - `stat, statErr := w.file.Stat()`에서 `statErr != nil`일 때 에러를 반환하지 않고 `return 0, nil`을 반환하여, 디스크 I/O 장애 시 WAL 리플레이가 성공한 것처럼 위장하고 미재생 트랜잭션을 전부 유실시킴 (Invariant 9 Fail-Closed 위반).
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P0)`
  - **패치 내용**: `statErr != nil` 시 `return maxLSN, fmt.Errorf("failed to stat WAL file during replay: %w", statErr)`를 즉시 반환하도록 수정.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-005] `pkg/storage/memengine/ddl.go:187-245` & `persistence.go:80-196, 566-600`
* **대상 영역**: `memengine` 인덱스 DDL 및 스냅샷/WAL 로드 시 `td.snapSchema` 원자적 포인터 갱신 누락
* **Antigravity 1차 분석**:
  - `TableData`는 락-프리 스냅샷 읽기를 위해 `td.snapSchema`(`atomic.Pointer[storage.TableSchema]`)를 유지하며 `getSnapSchema()`로 조회함.
* **Claude 2차 적대적 교차 분석**:
  - `CreateIndex`(`ddl.go:196`)와 `DropIndex`(`ddl.go:235`)에서 `td.Schema.Indexes`를 변경하면서:
    1. `td.mu.Lock()`을 획득하지 않아 동시 `td.Schema` 읽기 경로와 Data Race가 발생함.
    2. `td.refreshSnapSchemaLocked()`를 호출하지 않아 `td.snapSchema`가 구버전 스키마(인덱스 추가/삭제 전 상태)에 영구 고착됨!
  - 또한 `persistence.go`의 `applyWALRecordLocked`(`OpCreateIndex`, `OpDropIndex`, `OpAlterTable`)와 `loadDiskLocked`에서도 `td.Schema` 변경 후 `td.refreshSnapSchemaLocked()`를 호출하지 않아 재시작 후 `getSnapSchema()`가 구버전 스키마를 반환함.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `CreateIndex` 및 `DropIndex`에 `td.mu.Lock()`과 `td.refreshSnapSchemaLocked()`를 추가하고, `applyWALRecordLocked` 및 `loadDiskLocked`의 모든 스키마 변경 지점에 `td.refreshSnapSchemaLocked()` 호출 추가.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-006] `pkg/storage/wiredtiger/wt_engine.go:1414-1466` & `pkg/storage/memengine/ddl.go:600-634`
* **대상 영역**: `GetTable` 및 `ListTables` 반환 `TableSchema`의 슬라이스 백킹 배열 공유 및 Data Race
* **Antigravity 1차 분석**:
  - `wiredtiger.Engine.GetTable`/`ListTables` 및 `memengine.Engine.GetTable`/`ListTables`가 `*t` 또는 `td.Schema`를 얕은 복사(Shallow Copy)로 반환함.
* **Claude 2차 적대적 교차 분석**:
  - `TableSchema` 내부의 `Columns`, `Indexes`, `ForeignKeys`, `CheckConstraints` 슬라이스가 엔진 내부 카탈로그 맵의 슬라이스 백킹 배열과 메모리를 공유함.
  - 호출자(예: `sqlexec_ddl.go`의 `ALTER TABLE` 핸들러나 플래너)가 반환받은 `schema.Columns[i]`를 수정하거나 동시 DDL이 발생할 때 Go `-race` 감지기에서 Data Race가 발생하고 검증 실패 시에도 인메모리 스키마가 오염됨.
  - 또한 `memengine`의 `GetTable`/`ListTables`는 `e.mu.RLock()`만 잡고 `td.mu.RLock()`을 잡지 않은 채 `td.Schema`를 읽고 있었음.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `wiredtiger`와 `memengine`의 `GetTable` 및 `ListTables`에서 `Clone()` 깊은 복사본을 반환하도록 수정하고, `memengine`에서 `td.mu.RLock()`을 획득하도록 수정.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-007] `pkg/storage/wiredtiger/wt_engine.go:1052-1110`
* **대상 영역**: `wiredtiger.Engine.DropPartition` 실행 시 세컨더리 인덱스 엔트리, 캐시 커서, 행 카운터 정리 누락
* **Antigravity 1차 분석**:
  - `DropPartition`은 메인 테이블 커서를 순회하며 파티션 조건에 부합하는 행을 `cursor.remove()`로 삭제함.
* **Claude 2차 적대적 교차 분석**:
  - `DropPartition`이 메인 테이블의 행만 삭제하고:
    1. 해당 행들의 **세컨더리 인덱스(`table:_idx_...`) 엔트리를 전혀 삭제하지 않아** 인덱스 스캔 시 이미 삭제된 파티션의 키가 계속 조회됨(Dangling Index Entry).
    2. `s.evictAllCachedCursors()`를 호출하지 않아 열려 있는 캐시된 커서가 충돌할 수 있음.
    3. 삭제된 행 수만큼 `e.rowCounts[tbl.Name]`을 차감하지 않아 `TableRowCount()`가 실제보다 큰 값을 반환함.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `DropPartition`에서 `s.evictAllCachedCursors()` 호출, 각 삭제 대상 행을 디코딩하여 모든 활성 세컨더리 인덱스 엔트리를 함께 제거, 그리고 삭제된 행 수(`droppedCount`)만큼 `e.rowCounts[tbl.Name]`을 원자적으로 차감하도록 수정.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-010] `pkg/storage/memengine/persistence.go:560-570`
* **대상 영역**: `loadDiskLocked` 스냅샷 로드 시 `td == nil` 방어 가드
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - 손상되거나 구버전인 RDB 스냅샷 파일에서 `Tables` 맵에 `nil` 엔트리가 역직렬화될 경우 `td.Name` 역참조 시 nil pointer panic 발생 가능.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **패치 내용**: `if td == nil { continue }` 방어 가드 추가.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-011] `pkg/storage/tuple.go:160-175`
* **대상 영역**: `EncodeRowToBuffer` (`TupleCodecV2`) 컬럼 수 `uint16` 오버플로우 방어
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `TupleCodecV2` 헤더의 `Bytes 2..3`은 `uint16(numCols)`를 기록함. 만약 `numCols > math.MaxUint16`(65,535)인 행이 전달되면 `uint16(numCols)`가 조용히 하위 16비트로 잘려(Wrap-around) 튜플이 손상됨 (`[RULE:DefensiveIndexing]`).
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **패치 내용**: `if numCols > math.MaxUint16 { return nil, fmt.Errorf("%w: column count %d exceeds uint16 limit", ErrInvalidTupleHeader, numCols) }` 가드 추가.
  - **판정**: `RESOLVED`

---

### Domain 2: VDBE Bytecode VM & T-SQL Parsers (`pkg/proc`, `pkg/proc/runtime`, `pkg/proc/parser/antlr`)

---

#### [AUDIT-093-008] `pkg/proc/runtime/vm.go:767-770, 1448-1464, 1707-1726, 3033-3073`
* **대상 영역**: 중첩 프로시저(`callProc`), `INSERT...EXEC`(`execInsertExec`), 트리거(`fireTriggers`) 실행 시 `subFrame`과 부모 `VMFrame` 간 트랜잭션 상태(`tx`, `tranCount`, `txFailed`) 동기화
* **Antigravity 1차 분석**:
  - `callProc`, `execInsertExec`, `fireTriggers`는 새로운 `subFrame`을 할당하여 `subFrame.tx = f.tx`를 전달함.
* **Claude 2차 적대적 교차 분석**:
  - **결함 1 (트리거/서브프레임 내 트랜잭션 제어 시 부모 프레임 Dangling Transaction 참조)**:
    - `fireTriggers`와 `execInsertExec`에서 `subFrame`을 생성할 때 `subFrame.session = f.session`, `subFrame.ctx = f.ctx`, `subFrame.tranCount = f.tranCount`, `subFrame.txFailed = f.txFailed`를 전달하지 않았음!
    - 이로 인해 트리거 내부에서 `ROLLBACK TRANSACTION`(T-SQL 트리거 내 표준 에러 취소 패턴)을 실행하거나 중첩 프로시저 내부에서 `BEGIN TRAN` / `COMMIT TRAN` / `ROLLBACK TRAN`을 실행할 때:
      1. `subFrame.tx`가 `nil`로 바뀌거나 새 트랜잭션으로 교체되어도 부모 프레임 `f.tx`, `f.tranCount`, `f.txFailed`에 전혀 역전파되지 않음.
      2. 부모 프레임은 이미 닫힌(`ErrTxClosed`) 구 `f.tx` 포인터를 계속 사용하여 후속 문장에서 `transaction is already closed` 에러가 발생하거나 세션의 `ActiveTx`가 불일치 상태에 빠짐.
  - **결함 2 (`ExecuteInTxWithSessionContext` 에러 반환 시 `ActiveTx` 누락)**:
    - 프로시저가 `BEGIN TRAN`을 실행해 `frame.tx`를 생성한 뒤 후속 문장에서 에러로 중단될 때 `return nil, err`를 반환하여 호출자(`ClientSession`)가 열린 `frame.tx`를 전달받지 못하고 트랜잭션이 영구 고아(Orphan Transaction)로 남음.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**:
    1. `fireTriggers`, `execInsertExec`, `callProc` 모두에서 `subFrame` 진입 전 `session`, `ctx`, `tranCount`, `txFailed`를 복사하고, `subFrame.run()` 종료 후 `f.tx = subFrame.tx`, `f.tranCount = subFrame.tranCount`, `f.txFailed = subFrame.txFailed`를 부모 프레임으로 역전파하도록 수정.
    2. `ExecuteInTxWithSessionContext`에서 에러 발생 시에도 `&ExecutionResult{ActiveTx: frame.tx}, err`를 반환하여 호출자가 활성 트랜잭션을 추적/롤백할 수 있도록 수정.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-009] `pkg/proc/runtime/vm_dispatcher.go:994-1050`
* **대상 영역**: `VMFrame` `OpInsert` 고속 경로(`canDirectInsert`)의 `NOT NULL` 검증 및 `OUTPUT` 절 결과셋 누락
* **Antigravity 1차 분석**:
  - `OpInsert`는 트리거/FK/파티션/체크 제약이 없을 때 `RowInsertProvider.InsertRow`를 직접 호출하는 `canDirectInsert` 고속 경로를 사용함.
* **Claude 2차 적대적 교차 분석**:
  - **결함 1 (`OUTPUT` 결과셋 증발)**:
    - `canDirectInsert` 조건이 `len(data.OutputCols) == 0`을 확인하지 않고, 아래에서 `if len(data.OutputCols) > 0 && len(data.IntoVars) > 0`인 경우만 처리함.
    - 따라서 `INSERT INTO tbl OUTPUT inserted.id VALUES (1)`처럼 `INTO @var` 없이 클라이언트로 결과셋을 반환해야 하는 `OUTPUT` 절이 `canDirectInsert`를 타면 결과셋이 생성되지 않고 조용히 증발함!
  - **결함 2 (`NOT NULL` 검증 우회)**:
    - `canDirectInsert`에서 `colSchema.Default == ""`이고 삽입된 값 `stVal.IsNull == true`일 때 `!colSchema.Nullable` 여부를 검사하지 않고 그대로 `InsertRow`에 넘겨 `NOT NULL` 컬럼에 `NULL`이 삽입될 수 있음.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **패치 내용**: `canDirectInsert` 조건에 `(len(data.OutputCols) == 0 || len(data.IntoVars) > 0)`을 추가하고, 컬럼 루프에서 `if stVal.IsNull && !colSchema.Nullable { return fmt.Errorf("%w: column %s", storage.ErrNotNullViolation, colSchema.Name) }` 검증을 추가.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-017] `pkg/proc/compiler.go:674, 1085, 1170-1182`
* **대상 영역**: VDBE 바이트코드 컴파일러(`Compiler`)의 임시 레지스터 반환(`freeReg`) 누수
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - 긴 루프나 대규모 배치 스크립트 컴파일 시:
    1. `DeclareStmt`(`compiler.go:674`): 초기값 컴파일에 사용한 `valReg`를 `emit(OpCopy, valReg, reg, ...)` 이후 `c.freeReg(1)`로 해제하지 않음.
    2. `WaitforStmt`(`compiler.go:1085`): 대기 시간 표현식에 사용한 `valReg`를 `emit(OpWaitfor, ...)` 이후 `c.freeReg(1)`로 해제하지 않음.
    3. `GeneralSelectStmt`(`compiler.go:1170-1182`): 무테이블 스칼라 `SELECT` 프로젝션에 할당한 연속 레지스터 블록(`count := int32(len(projRegs))`)을 `OpResultRow` 방출 후 해제하지 않음.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **패치 내용**: 위 3개 지점 모두 명령어 방출 직후 `c.freeReg(...)`를 호출하여 레지스터 워터마크 팽창 방지.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-018] `pkg/proc/parser/antlr/ast_builder.go:129-133`
* **대상 영역**: ANTLR4 폴백 파서 `VisitCreate_or_alter_procedure` 내 개별 문장 파싱 실패 시 침묵 누락
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `VisitCreate_or_alter_procedure` 내부에서 빠른 경로로 `cParser.ParseStatement()`를 시도할 때, `stmt, err := cParser.ParseStatement()`가 에러를 반환하면 `break`나 `return nil` 없이 다음 문장으로 넘어가면서 해당 문장을 프로시저 본문에서 **조용히 삭제(Silent Statement Drop)**한 채 불완전한 AST를 반환함! (`[RULE:ExplicitError]`)
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `if err != nil || stmt == nil { return nil }`로 즉시 빠른 경로를 포기하고 ANTLR4 전체 문법 트리 방문자로 폴백하도록 수정.
  - **판정**: `RESOLVED`

---

### Domain 3 & 4: SQL Coordinator, Catalog & Planner (`pkg/engine`, `pkg/planner`)

---

#### [AUDIT-093-012] `pkg/engine/sqlexec_ddl.go:1801-2173`
* **대상 영역**: `execAlterTable`의 에러 무시, 존재하지 않는 컬럼 조작 시 침묵 성공, `InvalidatePKBypassCache()` 누락
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `execAlterTable` 감사 결과 다음 3가지 결함이 확인됨:
    1. `ADD CHECK` (`sqlexec_ddl.go:1805`): `s.engine.AlterTable(tableName, *schema)`의 반환 에러를 확인하지 않고 무시함 (`[RULE:ExplicitError]`).
    2. 존재하지 않는 컬럼에 대해 `ALTER COLUMN ... SET/DROP DEFAULT`, `ALTER COLUMN ... TYPE`, `RENAME COLUMN`, `ALTER COLUMN ... SET/DROP NOT NULL`, `DROP COLUMN` (without `IF EXISTS`) 실행 시 루프에서 컬럼을 찾지 못해도 에러(`storage.ErrColumnNotFound`)를 반환하지 않고 `COMMAND_OK`를 반환함.
    3. `ALTER TABLE`로 컬럼/제약조건이 변경된 후 `s.InvalidatePKBypassCache()`를 호출하지 않아 PK Bypass 캐시가 구버전 스키마를 계속 참조함.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**:
    1. `ADD CHECK`에서 `AlterTable` 에러를 즉시 반환하도록 수정.
    2. `DROP/SET DEFAULT`, `ALTER TYPE`, `RENAME COLUMN`, `SET/DROP NOT NULL`, `DROP COLUMN`(비 `IF EXISTS`)에서 대상 컬럼 미존재 시 `fmt.Errorf("%w: %s", storage.ErrColumnNotFound, colName)` 반환.
    3. 모든 `AlterTable` 성공 지점 직후 `s.InvalidatePKBypassCache()` 호출 추가.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-013] `pkg/engine/sqlexec_virtual.go:111-140`
* **대상 영역**: `Coordinator.GetTableRowCount`의 트랜잭션 누수 및 불필요한 Full Table Scan
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `GetTableRowCount`가 `sess.BeginTx(storage.TxOptions{ReadOnly: true})`를 호출한 뒤 **`tx.Rollback()`을 호출하지 않고 반환**하여 트랜잭션 객체와 스냅샷 리소스를 누수시킴 (`[RULE:HandleHygiene]`).
  - 또한 `memengine`과 `wiredtiger` 모두 $O(1)$ `TableRowCount(name)` 메서드를 제공하고 있음에도 이를 호출하지 않고 매번 전체 테이블 커서 스캔(`for cursor.Next() { cnt++ }`)을 수행함.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `s.engine.(interface{ TableRowCount(string) (int64, error) })`를 통한 $O(1)$ 고속 경로를 최우선 호출하고, 폴백 스캔 경로에 `defer tx.Rollback()`을 추가.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-014] `pkg/engine/session.go:522-535` & `pkg/engine/sqlexec_procs.go:1122-1131`
* **대상 영역**: `ClientSession.ExecuteProgram` 및 `execProcedureCallWithRaw` 실행 시 `cs.Mu` 락 점유 및 `cs.Tx` 동기화 누락
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `ClientSession.ExecuteProgram`: 세션에 열린 트랜잭션(`cs.Tx != nil`)이 있을 때 `cs.Mu.Lock()`을 잡은 채로 블로킹 VM 실행(`ExecuteInTxWithSessionContext`)을 끝까지 수행하여, 다른 고루틴에서 해당 세션의 상태 조회나 취소 시 심각한 락 경합을 유발함.
  - 또한 프로시저 내부에서 `COMMIT` 또는 `ROLLBACK`이 실행되어 `res.ActiveTx`가 변경(`nil` 등)되었을 때 `cs.Tx = res.ActiveTx`로 갱신하지 않아(`session.go:530` 및 `sqlexec_procs.go:1126`), 프로시저 종료 후 세션이 이미 닫힌 `cs.Tx`를 계속 보유하게 됨!
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `ClientSession.ExecuteProgram`에서 `cs.sessInUse = true`를 설정하고 `cs.Mu.Unlock()`을 해제한 뒤 VM을 실행하고, 실행 완료 후 `cs.Mu.Lock()`을 다시 획득하여 `cs.Tx = res.ActiveTx`를 동기화하도록 수정 (`sqlexec_procs.go`의 `execProcedureCallWithRaw`에도 동일하게 `cs.Tx = res.ActiveTx` 동기화 반영).
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-015] `pkg/engine/sqlexec_merge.go:82-88`, `pkg/engine/sqlexec_procs.go:1547`, `pkg/wire/copy.go:404`
* **대상 영역**: `MERGE`, 익명 블록(`BEGIN...END`), PGWire `COPY IN` 실행 시 세션 `TxOptions()`(ReadOnly 등) 무시
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `SET TRANSACTION READ ONLY`가 활성화된 세션(`cs.ReadOnly == true`)에서도 `execMerge`, `execAnonymousBlock`, `handleCopyIn`이 `storage.TxOptions{}`(빈 옵션)로 트랜잭션을 시작하여 읽기 전용 세션에서 데이터 변경이 허용됨.
  - 또한 `execMerge`는 `s.acquireSessionForQuery(cs)`를 사용하지 않고 매번 `s.engine.NewSession()`을 직접 호출하고 있었음.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **패치 내용**: 세 경로 모두 `cs.TxOptions()`를 전달하여 읽기 전용 트랜잭션 제약을 준수하도록 수정하고, `execMerge`는 `s.acquireSessionForQuery(cs)`를 사용하도록 일원화.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-016] `pkg/planner/setop.go:222-237`
* **대상 영역**: `extractTrailingOrderByAndLimit` 내 도달 불가능한 데드 코드(Dead Code)
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - 218라인에서 `if orderIdx == -1 && limitIdx == -1 && offsetIdx == -1 { return sqlStr, "" }`로 조기 반환한 직후, 222라인에서 다시 `if orderIdx != -1 { ... } else if limitIdx != -1 { ... }`에 이어 절대 참이 될 수 없는 `else` 분기가 남아 있었음 (`[RULE:RadicalKISS]`).
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Low (P2)`
  - **패치 내용**: 도달 불가능한 분기를 정리하고 `cutIdx` 계산 로직을 간결화.
  - **판정**: `RESOLVED`

---

### Domain 5: Wire Protocols (`pkg/wire`, `pkg/wire/tds`)

---

#### [AUDIT-093-019] `pkg/wire/tds/token.go:657, 872, 913, 963, 1010`, `rpc_output.go:39-64`, `server.go:991`
* **대상 영역**: TDS 프로토콜 `ROW`(0xD1), `NBCROW`(0xD2), `RETURNVALUE`(0xAC) 토큰 직렬화 시 `storage.Value{IsNull: true}` NULL 판정 누락
* **Antigravity 1차 분석**:
  - TDS 직렬화기(`WriteRowDirect`, `BuildRow`, `BuildNBCRow`, `WriteNBCRowDirect`, `sess.buildRow`, `withOutputParams`)는 `val == nil`로만 SQL `NULL` 여부를 검사함.
* **Claude 2차 적대적 교차 분석**:
  - VDBE VM이나 엔진 내부 실행 경로에서 맵이나 변수에 `storage.Value{IsNull: true}`(또는 `*storage.Value`)가 담긴 채 TDS 계층으로 전달되면, Go 인터페이스 비교 `val == nil`이 **`false`**가 됨!
  - 그 결과:
    1. `NBCROW`의 Null Bitmap 비트가 0(Non-NULL)으로 설정되고, `writeColumnValue`가 호출되어 `NULL` 정수/실수/문자열이 `0`이나 빈 문자열 `""` 또는 `"{0 true 0  0 false ...}"`로 잘못 직렬화되어 클라이언트에 전송됨!
    2. `RPC` 출력 파라미터(`RETURNVALUE`)에서도 `storage.Value{IsNull: true}`가 `NULL` 대신 구조체 포맷 문자열로 전송됨.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `isNilTdsValue(v any) bool` 및 `unwrapTdsOutputValue(v any) any` 헬퍼를 도입하여 `nil`, `storage.Value{IsNull: true}`, `*storage.Value(nil 또는 IsNull: true)`를 모두 일관되게 SQL `NULL`로 판정하고, Non-NULL `storage.Value`도 네이티브 스칼라 값(`sv.Interface()`)으로 언래핑하도록 수정.
  - **판정**: `RESOLVED`

---

### Domain 6: API Server, Standalone Workers & CLI (`pkg/api`, `pkg/dbmail`, `cmd/lionsql`)

---

#### [AUDIT-093-020] `cmd/lionsql/sql.go:174-240`
* **대상 영역**: CLI 서브커맨드(`lionsql sql`, `lionsql cli`)의 `lionsql.engine.json` 매니페스트 검증 누락 및 `coord.Close()` 누락
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `cmd/lionsql/server.go`는 기동 시 `config.ResolveExistingEngineManifest`를 호출하여 기존 데이터 디렉토리의 엔진 종류(`memengine` vs `wiredtiger`)를 검증하고 불일치 시 엔진 오염을 차단함.
  - 반면 `cmd/lionsql/sql.go`의 `openCoordinatorWithOptions`는 `lionsql.engine.json`을 전혀 확인하지 않아, WiredTiger 데이터 디렉토리에 대해 `-engine` 플래그 없이 `lionsql sql "..."`을 실행하면 기본값인 `memengine`으로 디렉토리를 열어버리는 사고가 발생함!
  - 또한 `runSQL`과 `runCLI`에서 `defer coord.Close()`를 호출하지 않아 CLI 종료 시 WAL/스냅샷 정상 플러시 없이 프로세스가 종료됨 (`[RULE:HandleHygiene]`).
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `openCoordinatorWithOptions`에서 `config.ResolveExistingEngineManifest(dataDir, engineExplicit, engineType)`를 호출하여 서버와 동일한 엔진 보호 가드를 적용하고, `runSQL` 및 `runCLI`에 `defer coord.Close()`를 추가.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-021] `pkg/dbmail/worker.go:253-325, 428` & `cmd/lionsql/cli_dbmail.go:67`
* **대상 영역**: `dbmail` 외부 워커의 SMTP 헤더 CRLF 인젝션 방어, 상태 업데이트 에러 체크, 클라이언트 핸들 정리
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - **결함 1 (SMTP Header CRLF Injection)**: `dispatchItem`이 `item.Recipients`, `item.CopyRecipients`, `item.Subject`, `acct.EmailAddress`, `acct.DisplayName`을 `\r`/`\n` 필터링 없이 `fmt.Sprintf("Subject: %s\r\n", ...)`로 결합하여, 개행 문자가 포함된 제목/수신자 값으로 임의의 SMTP 헤더를 주입할 수 있음.
  - **결함 2 (`ProcessBatch` 에러 무시)**: `w.client.Exec(ctx, updateSQL)`의 반환 에러를 `_`로 무시하여 DB 연결 끊김 시에도 에러가 반환되지 않음 (`[RULE:ExplicitError]`).
  - **결함 3 (`cli_dbmail.go` 핸들 누수)**: `worker.Start(ctx)` 실패 시 이미 생성된 `client.Close()`를 호출하지 않고 반환함 (`[RULE:HandleHygiene]`).
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **패치 내용**: `sanitizeSMTPHeader(s string) string` 함수를 추가하여 모든 헤더 필드에서 `\r`, `\n`, `\x00`을 제거하고, `ProcessBatch`에서 `Exec` 에러를 확인하여 반환하며, `cli_dbmail.go`에서 `defer client.Close()`를 보장하도록 수정.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-022] `pkg/api/server.go:371-374` & `cmd/lionsql/server.go:956`
* **대상 영역**: `api.Server.Close()` 및 `cmd/lionsql/server.go` 종료 시 백그라운드 워커 고루틴 누수 (`[RULE:ZeroOrphan]`)
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `api.NewServer`는 생성 시 `s.crons.Start()`, `s.outboxDispatcher.Start()`, `s.webhooks.Start()`, `s.broker`(Realtime SSE 브로커) 등 총 6개의 백그라운드 고루틴을 기동함.
  - 그러나 `api.Server.Close()`는 오직 `s.rateLimiter.Stop()`만 호출하고 `s.crons.Stop()`, `s.outboxDispatcher.Stop()`, `s.webhooks.Stop()`, `s.broker.Close()`를 호출하지 않아 `api.Server` 인스턴스가 닫힐 때마다 백그라운드 고루틴들이 영구 고아(Orphan Goroutines)로 누수됨 (`[RULE:ZeroOrphan]`).
  - 또한 `cmd/lionsql/server.go:956`의 종료 훅에서도 `outboxDispatcher.Stop()` 호출이 누락되어 있었음.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **패치 내용**: `api.Server.Close()`에서 `s.crons`, `s.outboxDispatcher`, `s.webhooks`, `s.broker`, `s.rateLimiter`를 모두 정지하도록 수정하고, `cmd/lionsql/server.go` 종료 시퀀스에도 `outboxDispatcher.Stop()`을 추가.
  - **판정**: `RESOLVED`
