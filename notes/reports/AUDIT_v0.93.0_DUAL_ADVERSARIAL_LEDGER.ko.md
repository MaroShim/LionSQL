# LionSQL v0.93.0 릴리즈 사전 듀얼 AI (Antigravity × Claude) 적대적 코드 감사 기록부

> - **문서 식별자**: `AUDIT-093-20261115`
> - **작성일**: 2026-11-15
> - **감사 범위**: `v0.92.0..HEAD` 변경 및 신규 추가된 전체 프로덕션 Go 소스코드 (총 111개 프로덕션 파일, 6개 도메인 전수 라인 단위 감사)
> - **감사 모드**: Line-by-Line Dual-AI Adversarial Audit (Antigravity 1차 아키텍처/로직 전수 분석 × Claude 2차 적대적 공격 및 엣지 케이스 교차 검증)
> - **준수 규약**: `doc/RELEASE_AUDIT_PROTOCOL.ko.md` 및 `notes/reports/FULL_CODEBASE_ADVERSARIAL_AUDIT.ko.md`

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
  - **1차 패치 내용**:
    1. `memengine.Transaction`에 `committed bool` 필드를 추가하여, `Commit()`이 성공하기 직전 모든 검증을 통과한 시점과 실패하여 중단(`abortAndPreserveTableVars`)되는 시점에 각각 정확히 1회만 `commitTableVariablesLocked()`를 수행하도록 수정.
    2. 이미 닫힌 트랜잭션(`if tx.closed`)에 대한 후속 `Rollback()` 호출에서는 `if !tx.committed`인 경우에만 `releaseToPool()`을 호출하고 즉시 `nil`을 반환하도록 수정하여 이중 반영과 `commitLSN` 초기화를 원천 차단.
    3. `WTTx.Commit()`에서 `_ = tempTx.Commit()` 직후 `_ = tempTx.Rollback()`을 호출하여 풀 반납 보장.
  - **2차 심화 패치 (Re-Patch / 영구 멱등성 및 풀 누수 완전 차단)**:
    - `commitTableVariablesLocked()`에서 테이블 변수 변경사항을 전역 테이블에 반영한 직후, `delete(tx.changes, changeKey)`로 처리된 키를 활성 맵에서 완전히 제거하도록 개선.
    - 이를 통해 동일 트랜잭션에서 `commitTableVariablesLocked()`가 다중 호출되더라도 100% 멱등하게 No-Op으로 동작하며, `defer tx.Rollback()` 시 테이블 변수 변경사항이 전역에 중복 적용되는 문제를 원천 방지함.
    - 트랜잭션 정상 종료 시 `releaseToPoolLocked()`를 호출하여 `txPool` 자원 회수 보장 (`TestTableVariable_CommitThenDeferRollback` 100% PASS).
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
  - **1차 임시 패치 내용**:
    1. `putRawBytesLocked(tbl, pk, rawBytes, updateIndexes bool)`로 시그니처를 분리하고, `wtUndoUpdate`에서는 `updateIndexes = false`로 호출한 뒤 `oldRow`와 `newRow`가 모두 존재할 때 `tx.updateSecondaryIndexesLocked(tbl, op.pk, newRow, oldRow)`를 호출하여 변경된 인덱스만 역방향 복원하도록 수정.
    2. `RollbackTo`의 `wtUndoInsert`/`wtUndoDelete`에서 정규화된 `tbl.Name`을 사용해 `tx.rowDeltas[tbl.Name]`을 갱신하도록 수정.
  - **2차 근본 아키텍처 개편 (ARIES Physiological Logging 정식 도입)**:
    - 1차 패치의 "런타임 커서 재조회 및 튜플 역디코딩" 방식은 롤백 시점의 커서 에러나 스키마 변경 시 여전히 인덱스 복구가 누락될 위험이 존재하며, `Delete` 롤백 시 보조 인덱스를 복구하지 못하는 결함이 잔존함을 적대적 분석으로 추가 확인.
    - 이에 따라 SQLite/ARIES 표준에 기반한 **Before/After Image 물리-논리적 로깅(`pkg/storage/aries_log.go`)**을 정식 설계/도입 (`notes/plans/ARCH_ARIES_LOGGING_PLAN.ko.md`):
      1. `PhysiologicalRecord`: 원자적 상태 변경마다 `OldTuple`, `NewTuple`, `OldIdx([]IndexImage)`, `NewIdx([]IndexImage)` 스냅샷 보존.
      2. `Insert`: 신규 튜플 및 생성된 인덱스 키 스냅샷(`NewIdx`) 기록.
      3. `Delete`: 이전 튜플 및 삭제된 모든 보조 인덱스 키 스냅샷(`OldIdx`) 기록 (기존 누락 결함 완벽 해결).
      4. `Update`: 이전/이후 튜플 및 변경된 보조 인덱스 키 스냅샷(`OldIdx`, `NewIdx`) 기록.
      5. `RollbackTo`: 런타임 튜플 디코딩 및 커서 재조회 일체를 배제하고 $O(1)$ 직접 B-Tree 보상 연산(CLR) 수행.
      6. `sync.Pool`을 통한 객체 재사용으로 롤백 로그 할당 오버헤드 제로화 (`[RULE:ZeroAllocation]`).
    - 단위 테스트 `TestWiredTiger_ARIES_Savepoint_ComprehensiveUndo` 및 48개 전 테스트 100% PASS 검증 완료.
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-003] `pkg/storage/memengine/persistence.go:205-208, 238-247`
* **대상 영역**: `applyWALRecordLocked` WAL 리플레이 시 `DecodeTupleToRow` 디코딩 에러 처리 및 복제 재생 경로
* **Antigravity 1차 분석**:
  - `OpInsert` 및 `OpUpdate` WAL 레코드 재생 중 `storage.DecodeTupleToRow`를 호출함.
* **Claude 2차 적대적 교차 분석**:
  - `OpInsert`: `DecodeTupleToRow`가 에러를 반환할 때 에러를 무시하고 `continue`로 넘어가 레코드가 조용히 유실(Silent Data Drop)됨 (`[RULE:ExplicitError]`, Invariant 9 위반).
  - `OpUpdate`: `DecodeTupleToRow`를 호출하기 전에(239라인) 먼저 `if !hadOld { sh.keys = append(sh.keys, k) }`를 실행한 뒤, 244라인에서 디코딩이 실패하면 `continue`로 빠져나가 `sh.keys`에는 키가 등록되었으나 `sh.entries`에는 엔트리가 없는 불일치 상태가 발생함.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P0)`
  - **1차 패치 내용**:
    - `OpInsert` 및 `OpUpdate`에서 `DecodeTupleToRow` 실패 시 즉시 명시적 에러(`fmt.Errorf("WAL replay OpInsert/OpUpdate decode failed on table %s key %s: %w", ...)`)를 반환(Fail-Closed)하고, `OpUpdate`의 `sh.keys` 추가를 디코딩 성공 이후로 이동.
  - **2차 심화 패치 (Re-Patch / 전역 Fail-Closed 무결성 및 외부 복제 경로 보완)**:
    1. **`memengine/persistence.go` 카탈로그 부재 방어**:
       - `OpInsert`, `OpUpdate`, `OpDelete` 리플레이 중 대상 테이블이 카탈로그에 존재하지 않을 때(`!exists`) 조용히 `continue`하던 결함을 제거하고, 즉시 `fmt.Errorf("memengine: wal replay ... table %q not found")`로 기동을 거부(Fail-Closed)하도록 수정.
       - Data와 Record가 모두 없는 비정상 WAL 레코드에 대해서도 Fail-Closed 에러 반환.
    2. **`backup/pitr.go` 외부 엔진/WiredTiger 복제 재생 경로 결함 해소**:
       - Primary의 WAL을 받아 재생하는 `ApplyWALRecordWithOptions`의 `OpInsert` 및 `OpUpdate`에서 `storage.DecodeTuple(tblSchema, op.Data)`의 에러를 `_`로 무시하고 삼키던 취약점 적발.
       - 디코딩 에러 발생 시 즉시 명시적 에러를 반환하여 복제 세션 트랜잭션을 롤백하고 Fail-Closed를 강제하도록 패치.
       - `TestPITR_ApplyWALRecord_CorruptTuple_FailsClosed` 검증 추가 (100% PASS).
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
  - **1차 패치 내용**:
    - `statErr != nil` 시 `return maxLSN, fmt.Errorf("failed to stat WAL file during replay: %w", statErr)`를 즉시 반환하도록 수정하여 디스크 I/O 실패 시 엔진 기동 중단(Fail-Closed) 강제.
  - **2차 심화 패치 (Re-Patch / AOF 컴팩션 복구 경로 os.Stat 결함 보완)**:
    - `memengine.go:481`의 크래시 복구 시 `os.Stat(compactingPath)` 결과를 단순히 `err == nil`로만 판정하여, 디스크 I/O 장애(`EIO`, `EACCES`) 발생 시 컴팩팅 WAL을 조용히 건너뛰고 정상 기동으로 오판하던 결함 적발.
    - `ENOENT`(`os.IsNotExist`)인 경우에만 컴팩팅 파일이 부재한 정상 상태로 처리하고, 그 외의 모든 OS I/O 에러 발생 시 즉시 `fmt.Errorf("failed to stat compacting WAL %q: %w", ...)`로 기동을 거부하도록 Fail-Closed 보강.
    - PostgreSQL, SQLite, InnoDB, Redis 표준 복구 철학과 100% 동일한 엄격한 부재 판정 규칙 확립.
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
  - **1차 패치 내용**:
    - `CreateIndex` 및 `DropIndex`에 `td.mu.Lock()`과 `td.refreshSnapSchemaLocked()`를 추가하고, `applyWALRecordLocked` 및 `loadDiskLocked`의 모든 스키마 변경 지점에 `td.refreshSnapSchemaLocked()` 호출 추가.
  - **2차 심화 패치 (Re-Patch / 인덱스 비활성화 경로 스냅샷 동기화 사각지대 해소)**:
    - RDB 스냅샷 복구 시 손상된 세컨더리 인덱스의 재빌드가 실패하여 비활성화로 전환될 때, `updateSchemaIndexDisabled`([`table_data.go:105`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/table_data.go#L105))가 `td.Schema.Indexes[i].Disabled = disabled`만 수정하고 `td.refreshSnapSchemaLocked()`를 누락하여 캐시된 `snapSchema`가 활성 상태로 고착되던 사각지대 적발.
    - `updateSchemaIndexDisabled` 내부에 `td.refreshSnapSchemaLocked()` 호출을 추가하여 비활성화 플래그 변경 즉시 불변 스냅샷 캐시가 원자적으로 갱신되도록 보강.
    - `TestSecondaryIndex_DisableAndFallbackToTableScanOnRebuildFailure`에 `SnapshotSchema()` 동기화 검증 추가 완료 (100% PASS).
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
  - **1차 패치 내용**:
    - `wiredtiger`와 `memengine`의 `GetTable` 및 `ListTables`에서 `Clone()` 깊은 복사본을 반환하도록 수정하고, `memengine`에서 `td.mu.RLock()`을 획득하도록 수정.
  - **2차 심화 패치 (Re-Patch / 트랜잭션 ListTables 딥클론 및 락 격리 보강)**:
    - 세션 및 트랜잭션 컨텍스트에서 메타데이터 뷰(`sys.tables`, `INFORMATION_SCHEMA`)가 호출하는 `Transaction.ListTables()`([`tx.go:322`](file:///Users/maro/Projects/SQLion/pkg/storage/memengine/tx.go#L322))에서 `cp := td.Schema`로 슬라이스 기저 배열을 얕은 복사하던 사각지대 적발.
    - `cp := td.SnapshotSchema().Clone()`으로 변경하여 `td.mu.RLock()` 락 보호 및 100% 독립된 슬라이스 딥클론을 반환하도록 보강.
    - `TestRegression_AUDIT_093_006_GetTableAndListTablesReturnDeepClone`에 트랜잭션 레벨 딥클론 검증 추가 완료 (100% PASS).
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
  - **1차 패치 내용**: `DropPartition`에서 `s.evictAllCachedCursors()` 호출, 각 삭제 대상 행을 디코딩하여 모든 활성 세컨더리 인덱스 엔트리를 함께 제거, 그리고 삭제된 행 수(`droppedCount`)만큼 `e.rowCounts[tbl.Name]`을 원자적으로 차감하도록 수정.
  - **2차 심화 패치 (Re-Patch / ACID 트랜잭션 원자성 및 Fail-Closed 격리 보강)**:
    - WiredTiger 원자성 원칙(`ACID Invariant 3, 6`): 파티션 드롭 시 테이블 행 삭제와 세컨더리 인덱스 B-Tree 키 제거가 개별 오토커밋으로 흩어지면 중도 오류(`cursor_remove` 실패 등) 발생 시 불완전 삭제(Partial Dropped State) 및 인덱스 불일치가 유발될 수 있음을 규명.
    - `DropPartition` 전체 작업을 전용 `WT_SESSION` 트랜잭션(`go_wt_session_begin` $\rightarrow$ `go_wt_session_commit`, 에러 시 `defer go_wt_session_rollback`)으로 감싸 완전한 원자적 롤백 보장.
    - `decodeRecord`, `ComputePartitionKey`, `cursor_remove`, `removeIndexEntry` 등에서 에러 발생 시 조용히 넘기지 않고 즉시 트랜잭션을 롤백하고 구체적인 에러를 반환하는 Fail-Closed 메커니즘 확립 (`[RULE:ExplicitError]`).
    - `TestRegression_AUDIT_093_007_DropPartitionCleansUpSecondaryIndexes` 통과 (100% PASS, 0 data races).
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-010] `pkg/storage/memengine/persistence.go:560-570`
* **대상 영역**: `loadDiskLocked` 스냅샷 로드 시 `td == nil` 방어 가드
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - 손상되거나 구버전인 RDB 스냅샷 파일에서 `Tables` 맵에 `nil` 엔트리가 역직렬화될 경우 `td.Name` 역참조 시 nil pointer panic 발생 가능.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **1차 패치 내용**: `persistence.go:578`에 `if td == nil { return 0, fmt.Errorf(...) }` 가드를 추가하여 `nil` 테이블 엔트리가 감지되면 즉시 Fail-Closed 에러로 기동 중단.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & RDB 스냅샷 스키마 무결성 및 대소문자 충돌 방어)**:
    - 스냅샷 맵 내 `td.Schema.Name`이 빈 문자열(`""`)로 오염되었거나, `"users"`와 `"USERS"`처럼 대소문자만 다른 중복 테이블 엔트리가 공존할 경우 카탈로그 조회 및 쿼리 라우팅이 비정상 동작하는 무결성 사각지대 규명.
    - TDD Red 단계 확립: `TestRegression_AUDIT_093_010_LoadDiskLockedCaseCollisionAndSchemaIntegrity` (CaseCollisionRejection, EmptySchemaNameRejection 2개 서브테스트 실패 확인).
    - TDD Green 단계 적용: `loadDiskLocked` 루프에 `td.Schema.Name == ""` 방어 가드 및 `seenFold` 맵을 통한 대소문자 유일성 검증 추가로 비정상 스냅샷 로드 시 즉시 Fail-Closed 에러 반환.
    - `TestRegression_AUDIT_093_010_LoadDiskLockedCaseCollisionAndSchemaIntegrity` 통과 (100% PASS, 0 data races).
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-011] `pkg/storage/tuple.go:160-175`
* **대상 영역**: `EncodeRowToBuffer` (`TupleCodecV2`) 컬럼 수 `uint16` 오버플로우 방어
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `TupleCodecV2` 헤더의 `Bytes 2..3`은 `uint16(numCols)`를 기록함. 만약 `numCols > math.MaxUint16`(65,535)인 행이 전달되면 `uint16(numCols)`가 조용히 하위 16비트로 잘려(Wrap-around) 튜플이 손상됨 (`[RULE:DefensiveIndexing]`).
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **1차 패치 내용**: `if numCols > math.MaxUint16 { return nil, fmt.Errorf("%w: column count %d exceeds uint16 limit", ErrInvalidTupleHeader, numCols) }` 가드 추가.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & SQL Server 표준 상한 1,024 컬럼 제한 전면 확립 및 공식 문서화)**:
    - 상용 DB 표준 조사: SQL Server(1,024개), PostgreSQL(1,600개), MySQL(1,017개) 기준에 맞춰, 엔진의 비정상 메모리 팽창 및 DoS 방어를 위해 SQL Server 패리티 기준인 **1,024 컬럼 상한(`storage.MaxColumnsPerTable = 1024`, `storage.ErrTooManyColumns`)** 공식 채택.
    - TDD Red 단계 확립: 1,025개 컬럼 초과 시 인코딩 거절 및 1,025 컬럼 테이블 DDL 거절 테스트 실패 확인.
    - TDD Green 단계 적용:
      1. `pkg/storage/tuple.go`: `EncodeRow`, `EncodeRowToBuffer`에서 1,024개 초과 시 즉시 `ErrTooManyColumns` 반환. `readTupleHeader`에서 `encodedCols > 1024` 감지 시 즉시 `ErrInvalidTupleHeader` 반환.
      2. `memengine` 및 `wiredtiger`의 `CreateTable` 및 `AlterTable` DDL에서 1,024개 초과 시 즉시 `ErrTooManyColumns` 거절.
      3. 공식 규격 문서화: `doc/internal/PHYSICAL_TABLE_FORMAT.ko.md` 및 `GEMINI.md` [Invariant 13]에 1,024개 상한 및 불변식 공식 명문화 완료.
    - `TestRegression_AUDIT_093_011_EncodeRowToBufferUint16OverflowGuard` 및 `TestRegression_AUDIT_093_011_CreateTableMaxColumnLimit` 통과 (100% PASS, 0 data races).
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
  - **1차 패치 내용**:
    1. `fireTriggers`, `execInsertExec`, `callProc` 모두에서 `subFrame` 진입 전 `session`, `ctx`, `tranCount`, `txFailed`를 복사하고, `subFrame.run()` 종료 후 `f.tx = subFrame.tx`, `f.tranCount = subFrame.tranCount`, `f.txFailed = subFrame.txFailed`를 부모 프레임으로 역전파하도록 수정.
    2. `ExecuteInTxWithSessionContext`에서 에러 발생 시에도 `&ExecutionResult{ActiveTx: frame.tx}, err`를 반환하여 호출자가 활성 트랜잭션을 추적/롤백할 수 있도록 수정.
  - **2차 심화 패치 (Re-Patch / Radical KISS 세션 진입점 단일화 & 90줄 중복 제거)**:
    - `execProcedure` 및 `execAnonymousBlock`(`sqlexec_procs.go`)에 산재해 있던 중복된 수동 락/세션/트랜잭션 관리 코드(~90줄)를 전면 삭제 (`[RULE:RadicalKISS]`).
    - 표준 진입점인 `ClientSession.ExecuteProgram` 단일 메서드로 일원화하여 세션 생명주기, 트랜잭션 동기화(`cs.Tx = res.ActiveTx`), 실패 플래그 전파를 SSOT로 일체화.
    - `buildExecutionResult`에서 `frame.tranCount == 0`일 때 내부 연속성 트랜잭션을 자동 커밋하고 `ActiveTx: nil`을 반환하도록 정밀화하여 프로시저 커밋/롤백 후 클라이언트 세션이 정상적으로 Autocommit 상태로 복귀함을 보장.
    - `TestRegression_AUDIT_093_008_TriggerAndNestedProcTransactionPropagation` 통과 (100% PASS, 0 data races).
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
  - **1차 패치 내용**: `canDirectInsert` 조건에 `(len(data.OutputCols) == 0 || len(data.IntoVars) > 0)`을 추가하고, 컬럼 루프에서 `if stVal.IsNull && !colSchema.Nullable { return fmt.Errorf("%w: column %s", storage.ErrNotNullViolation, colSchema.Name) }` 검증을 추가.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & OUTPUT 컬럼 한정자 정규화)**:
    - `OUTPUT inserted.col` / `OUTPUT deleted.col` 실행 시 결과셋 컬럼 헤더가 가상 테이블 접두사를 포함한 채 반환되어 클라이언트 및 상위 레이어에서 순수 컬럼명(`col`)으로 조회하지 못하던 결함 발견.
    - TDD Red 단계 확립: `TestRegression_AUDIT_093_009_DirectInsertNotNullAndOutputClause` 실패 확인.
    - TDD Green 단계 적용: `cleanOutputColName` 도입으로 `OpInsert`, `OpUpdate`(고속/표준), `OpDelete` 전반에 걸쳐 결과셋 컬럼명 및 맵 키를 표준 컬럼명(`col`)으로 정규화(하위 호환성 키 `inserted.col` 병행 유지).
    - `TestRegression_AUDIT_093_009_DirectInsertNotNullAndOutputClause` 통과 (100% PASS, 0 data races).
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
  - **1차 패치 내용**: 위 3개 지점 모두 명령어 방출 직후 `c.freeReg(...)`를 호출하여 레지스터 워터마크 팽창 방지.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & 다중 문장 배치 연속 선언/스칼라 SELECT 레지스터 바운딩 검증)**:
    - 20회 이상의 연속 인라인 `DECLARE`, `WAITFOR`, `SELECT 10, 20, 30` 문장으로 구성된 배치 프로시저 컴파일 시 임시 레지스터 반환을 정밀 검증하는 `TestRegression_AUDIT_093_017_CompilerRegisterReclaim` 신설.
    - 레지스터 누수 발생 시 80개 이상으로 선형 팽창하던 `prog.NumRegisters`가 회수 로직에 의해 최대 동시 필요 레지스터 수인 4개 이하(`<= 4`)로 컴팩트하게 제한됨을 엄격히 검증 (`[RULE:BoundedBuffers]`, `[RULE:ZeroAllocation]`).
    - `pkg/proc` 전체 테스트 통과 (100% PASS, 0 data races).
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-018] `pkg/proc/parser/antlr/ast_builder.go:90-179`, `pkg/proc/parser.go:270`
* **대상 영역**: ANTLR4 폴백 파서 `VisitCreate_or_alter_procedure` 내 개별 문장 파싱 실패 시 침묵 누락, 파라미터 OUTPUT/DEFAULT 유실, 타입 파서 중복 코드
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `VisitCreate_or_alter_procedure` 내부에서 `cParser.ParseStatement()`가 에러를 반환할 때 침묵 누락(`continue`)으로 해당 문장이 프로시저 본문에서 **조용히 삭제(Silent Statement Drop)**되던 중대 결함 (`[RULE:ExplicitError]`).
  - 추가 사각지대: 파라미터 `OUTPUT` 플래그 및 `DEFAULT` 표현식 완전 증발, `parseDataTypeString` 30줄 코드 중복, 프로시저명 Invariant 12 위반, 문장 뒤 미파싱 가비지 토큰 방어 누락.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **1차 패치 내용**: `if err != nil || stmt == nil { return nil }`로 즉시 빠른 경로를 포기하고 에러를 전파하도록 수정.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & 표준 유틸 재사용, 중복 코드 30줄 삭제, OUTPUT/DEFAULT 파라미터 복원 및 미파싱 잔여 토큰 Fail-Closed)**:
    - 파라미터 속성 유실 복구: ANTLR 문법 노드(`p.OUT()`, `p.OUTPUT()`)를 검사하여 `proc.ParamDef.Output` 플래그 복원. `p.Procedure_param_default_value()` 표현식을 파싱하여 `proc.ParamDef.Default` 기본값 표현식 복원.
    - DRY & 표준화 (`[RULE:RadicalKISS]`, Invariant 8): `ast_builder.go:150-179`의 30줄 중복 `parseDataTypeString`을 완전히 삭제하고, `pkg/proc/parser.go`의 `proc.ParseDataType` 공용 유틸 함수를 재사용하도록 일원화.
    - Invariant 12 준수: `ast_builder.go:94-97`의 ad-hoc `strings.Trim(procName, "[]")`을 `sqlutil.CleanIdentifier` 표준 모듈 호출로 전면 교체.
    - 잔여 미파싱 가비지 토큰 감지: `cParser.ParseStatement()` 후 선택적 세미콜론 소비 및 `!cParser.CurTokenIs(proc.TokenEOF)` 검증을 추가하여 미파싱 토큰 잔존 시 침묵 성공하지 않고 즉시 `return nil`로 Fail-Closed 차단.
    - TDD Red 단계 확립: `TestRegression_AUDIT_093_018_AntlrCreateProcedureNeverDropsUnparsedStatements`에 `OUTPUT`, `DEFAULT`, 및 후행 가비지 토큰 거부 검증 추가 후 실패(RED) 확인.
    - TDD Green 단계 적용 후 100% PASS 확인 (`pkg/proc/parser/antlr` 전체 테스트 무결성 통과).
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
  - **1차 패치 내용**:
    1. `ADD CHECK`에서 `AlterTable` 에러를 즉시 반환하도록 수정.
    2. `DROP/SET DEFAULT`, `ALTER TYPE`, `RENAME COLUMN`, `SET/DROP NOT NULL`, `DROP COLUMN`(비 `IF EXISTS`)에서 대상 컬럼 미존재 시 `fmt.Errorf("%w: %s", storage.ErrColumnNotFound, colName)` 반환.
    3. 모든 `AlterTable` 성공 지점 직후 `s.InvalidatePKBypassCache()` 호출 추가.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & 빈 ADD CHECK 표현식 침묵 성공 방어 및 다중 컬럼 드롭 원자성)**:
    - `sqlexec_ddl.go:1803`에서 `ALTER TABLE tbl ADD CHECK ();`처럼 잘못되거나 빈 표현식이 주어졌을 때 `parseColumnCheckExpr`가 빈 문자열을 반환하여 스토리지 변경은 건너뛰면서 클라이언트에는 조용히 성공(침묵 성공)을 반환하던 사각지대 적발 (`[RULE:ExplicitError]`).
    - TDD Red 단계 확립: `TestRegression_AUDIT_093_012_AlterTableNonExistentColumnErrors`에 `ADD CHECK ()` 검증 추가 후 실패(RED) 확인.
    - TDD Green 단계 적용: `checkExpr == ""`일 때 즉시 `fmt.Errorf("invalid check constraint syntax: missing expression")` Fail-Closed 에러 반환.
    - `DROP COLUMN IF EXISTS` 무오류 통과 및 다중 컬럼 드롭(`c1, no_such_col`) 시 미존재 컬럼 감지 시 기존 컬럼 보존 원자성 확인.
    - `TestRegression_AUDIT_093_012_AlterTableNonExistentColumnErrors` 통과 (100% PASS, 0 data races).
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-013] `pkg/engine/sqlexec_virtual.go:111-140`
* **대상 영역**: `Coordinator.GetTableRowCount`의 트랜잭션 누수 및 불필요한 Full Table Scan
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `GetTableRowCount`가 `sess.BeginTx(storage.TxOptions{ReadOnly: true})`를 호출한 뒤 **`tx.Rollback()`을 호출하지 않고 반환**하여 트랜잭션 객체와 스냅샷 리소스를 누수시킴 (`[RULE:HandleHygiene]`).
  - 또한 `memengine`과 `wiredtiger` 모두 $O(1)$ `TableRowCount(name)` 메서드를 제공하고 있음에도 이를 호출하지 않고 매번 전체 테이블 커서 스캔(`for cursor.Next() { cnt++ }`)을 수행함.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **1차 패치 내용**: `s.engine.(interface{ TableRowCount(string) (int64, error) })`를 통한 $O(1)$ 고속 경로를 최우선 호출하고, 폴백 스캔 경로에 `defer tx.Rollback()`을 추가.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & Radical KISS 레거시 풀스캔 블록 완전 삭제)**:
    - 아키텍처 원칙 재확립: 메타데이터 카탈로그 뷰(`sys.tables`, `sys.partitions`) 조회 시 행 수를 세기 위해 테이블 전체를 `cursor.Next()`로 한 줄씩 세는 폴백은 메모리/IO를 고갈시키는 치명적 안티패턴 (`[RULE:RadicalKISS]`).
    - LionSQL의 양대 엔진(`memengine`, `wiredtiger`)은 모두 $O(1)$ 원자적 카운터를 보장하므로, 레거시 세션/트랜잭션/풀스캔 코드 블록(~20줄)을 완전히 영구 삭제.
    - TDD Red 단계 확립: `audit013NoRowCountEngine`으로 세션 할당 추적 시 기존 코드가 불필요한 세션을 열고 풀스캔을 시도함을 실패(RED)로 증명.
    - TDD Green 단계 적용: `getTableRowCountFast`에서 레거시 스캔 블록 삭제 후 세션 오픈 0건 및 $O(1)$ 직접 반환 검증 완료.
    - `TestRegression_AUDIT_093_013_GetTableRowCountFastPathAndNoTxLeak` 통과 (100% PASS, 0 data races).
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-014] `pkg/engine/session.go:522-535` & `pkg/engine/sqlexec_procs.go:1122-1131`
* **대상 영역**: `ClientSession.ExecuteProgram` 및 `execProcedureCallWithRaw` 실행 시 `cs.Mu` 락 점유 및 `cs.Tx` 동기화 누락
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `ClientSession.ExecuteProgram`: 세션에 열린 트랜잭션(`cs.Tx != nil`)이 있을 때 `cs.Mu.Lock()`을 잡은 채로 블로킹 VM 실행(`ExecuteInTxWithSessionContext`)을 끝까지 수행하여, 다른 고루틴에서 해당 세션의 상태 조회나 취소 시 심각한 락 경합을 유발함.
  - 또한 프로시저 내부에서 `COMMIT` 또는 `ROLLBACK`이 실행되어 `res.ActiveTx`가 변경(`nil` 등)되었을 때 `cs.Tx = res.ActiveTx`로 갱신하지 않아(`session.go:530` 및 `sqlexec_procs.go:1126`), 프로시저 종료 후 세션이 이미 닫힌 `cs.Tx`를 계속 보유하게 됨!
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **1차 패치 내용**: `ClientSession.ExecuteProgram`에서 `cs.sessInUse = true`를 설정하고 `cs.Mu.Unlock()`을 해제한 뒤 VM을 실행하고, 실행 완료 후 `cs.Mu.Lock()`을 다시 획득하여 `cs.Tx = res.ActiveTx`를 동기화하도록 수정 (`sqlexec_procs.go`의 `execProcedureCallWithRaw`에도 동일하게 `cs.Tx = res.ActiveTx` 동기화 반영).
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & 프로시저 종료 시 tranCount==0 연속성 트랜잭션 자동 종료 및 세션 Autocommit 복귀)**:
    - 프로시저 내부에서 `COMMIT` 또는 `ROLLBACK` 후 연속성 보장을 위해 열린 내부 트랜잭션(`newTx`)이 프로시저 종료 후에도 `cs.Tx`에 남아 클라이언트 세션이 Autocommit 상태로 복귀하지 못하던 결함 발견.
    - TDD Red 단계 확립: `TestRegression_AUDIT_093_014_ExecuteProgramSyncsSessionTxOnCommitOrRollback`에서 프로시저 커밋 후 세션 `cs.Tx == nil` 검증 실패(RED) 확인.
    - TDD Green 단계 적용:
      1. `pkg/proc/runtime/vm.go`: `buildExecutionResult` 및 에러 반환 시 `frame.tranCount == 0`이면 내부 연속성 트랜잭션을 자동 커밋하고 `ActiveTx: nil`을 명시적으로 반환.
      2. `pkg/engine/session.go`: `cs.ExecuteProgram`에서 `if res != nil { cs.Tx = res.ActiveTx }`로 갱신하여, 프로시저 완료 시 `cs.Tx`가 정상적으로 `nil`(Autocommit)로 복귀하도록 확립.
    - `TestRegression_AUDIT_093_014_ExecuteProgramSyncsSessionTxOnCommitOrRollback` 통과 (100% PASS, 0 data races).
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-015] `pkg/engine/sqlexec_merge.go:82-88`, `pkg/engine/sqlexec_procs.go:1547`, `pkg/wire/copy.go:404`
* **대상 영역**: `MERGE`, 익명 블록(`BEGIN...END`), PGWire `COPY IN` 실행 시 세션 `TxOptions()`(ReadOnly 등) 무시
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `SET TRANSACTION READ ONLY`가 활성화된 세션(`cs.ReadOnly == true`)에서도 `execMerge`, `execAnonymousBlock`, `handleCopyIn`이 `storage.TxOptions{}`(빈 옵션)로 트랜잭션을 시작하여 읽기 전용 세션에서 데이터 변경이 허용됨.
  - 또한 `execMerge`는 `s.acquireSessionForQuery(cs)`를 사용하지 않고 매번 `s.engine.NewSession()`을 직접 호출하고 있었음.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **1차 패치 내용**: 세 경로 모두 `cs.TxOptions()`를 전달하여 읽기 전용 트랜잭션 제약을 준수하도록 수정하고, `execMerge`는 `s.acquireSessionForQuery(cs)`를 사용하도록 일원화.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & 익명 블록/프로시저 VDBE 내부 트랜잭션 ReadOnly 제약 완벽 전파)**:
    - VDBE VM 실행 경로에서 `RunContext`가 빈 `storage.TxOptions{}`를 하드코딩하고 있어 세션의 `ReadOnly` 설정이 내부 트랜잭션에 전파되지 못하던 사각지대 적발.
    - `pkg/proc/runtime/vm.go`에 `RunContextWithOptions(..., opts storage.TxOptions)` 신설.
    - `pkg/engine/session.go`의 `ExecuteProgram`에서 `cs.TxOptions()`를 `RunContextWithOptions`로 직접 전달하여 프로시저 및 익명 블록 내부의 모든 데이터 변경 시도가 `storage.ErrReadOnly`로 Fail-Closed 차단되도록 확립.
    - `sqlexec.go:660`의 `BeginSessionTx`에서도 `cs.IsReadOnly() || s.IsReadOnly()`일 때 `opts.ReadOnly = true` 강제.
    - TDD Red 단계 확립: `TestRegression_AUDIT_093_015_ReadOnlySessionRejectsMergeAndAnonymousBlock` 실패 확인.
    - TDD Green 단계 적용: 익명 블록, MERGE, 명시적 `BEGIN TRAN` 모두에서 읽기 전용 쓰기 차단 검증 완료.
    - `TestRegression_AUDIT_093_015_ReadOnlySessionRejectsMergeAndAnonymousBlock` 통과 (100% PASS, 0 data races).
  - **판정**: `RESOLVED`

---

#### [AUDIT-093-016] `pkg/planner/setop.go:34-45, 186-221`, `pkg/engine/sqlexec_select.go:1616-1618`, `pkg/sqlutil/tokenizer.go:1030`
* **대상 영역**: `SetOpQuery`의 `LIMIT` / `OFFSET` 파싱 실패, 인라인 바이트 루프 중복, 실행기 오프셋 누락
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - 1차: `extractTrailingOrderByAndLimit` 내 도달 불가능한 데드 코드 정리.
  - 2차: `LIMIT 10 OFFSET 5` 구문에서 `limitStr`이 `"10 OFFSET 5"`로 슬라이싱되어 `strconv.ParseInt`가 실패(`invalid syntax`)하고 `Limit`이 `-1`로 리셋되어 페이징이 증발함. 또한 단독 `OFFSET` 절 미인식, `SetOpQuery` 내 `Offset` 필드 누락, 실행 계층(`sqlexec_select.go:1617`)의 오프셋 `0` 하드코딩 결함 확인.
  - `[RULE:ModularAbstraction]` 위반: `setop.go`가 자체적으로 90줄의 ad-hoc 인라인 바이트 루프를 돌며 주석(`--`, `/* */`) 처리를 누락하고 있었음.
* **합의된 결함 및 패치 결과**:
  - **심각도**: `Medium (P1)`
  - **1차 패치 내용**: 도달 불가능한 분기를 정리하고 `cutIdx` 계산 로직을 간결화.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & `sqlutil` 표준화, 90줄 인라인 루프 삭제, SetOp `OFFSET` 지원 및 오라클 정합성 복원)**:
    - Invariant 8 / Invariant 12 / `[RULE:ModularAbstraction]` 준수: `pkg/planner/setop.go`의 90줄 ad-hoc 인라인 바이트 루프를 전면 삭제하고, `pkg/sqlutil/tokenizer.go`에 다중 키워드 최상위 스캐너(`FindAllTopLevelAnyKeywords`)를 신설하여 주석/따옴표/괄호 스킵을 단일 책임으로 일원화.
    - `SetOpQuery`에 `Offset int64`를 추가하고, `sqlexec_select.go:1617`(`NewLimitIterator`)의 하드코딩 `0`을 `setQuery.Offset`으로 연결.
    - CBO 플래너 `BuildSetOpPlan`에서 `NodeLimit`의 `OffsetCount` 정상 바인딩.
    - TDD Red 단계 확립: `TestRegression_AUDIT_093_016_SetOpExtractTrailingOrderByAndLimit`에 `LIMIT+OFFSET`, 단독 `OFFSET`, `ORDER BY+OFFSET`, 주석 내 `UNION` 무시 검증 추가 후 실패(RED) 확인.
    - TDD Green 단계 적용 후 100% PASS 확인.
    - Invariant 16 (`[RULE:ZeroOracleTampering]`): 하베스팅 당시 버그로 미페이징 결과가 기록되었던 `test/slt_proc/sqlglot_oracle/3002_sqlglot_set_operation_pagination.slt`의 기대값을 표준 T-SQL 페이징 정답으로 사용자 승인 하에 정상 갱신 및 `CHECKSUMS.sha256` 동기화 완료 (`TestOracleIntegrity_ZeroTampering` 100% PASS).
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
  - **1차 패치 내용**: `isNilTdsValue(v any) bool` 및 `unwrapTdsOutputValue(v any) any` 헬퍼를 도입하여 `token.go` 및 `row_stream.go` 내 `ROW`, `NBCROW`, `WriteReturnValue`에서 `storage.Value{IsNull: true}` 및 `*storage.Value`를 일관되게 SQL `NULL`로 판정하도록 수정.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & `server.go` / `rpc_output.go` 전역 사각지대 해소)**:
    - 사각지대 발굴: `pkg/wire/tds/server.go:1912`(`formatSQLValue`) 및 `server.go:1937`(`inferSQLType`)에서 `storage.Value{IsNull: true}`가 `v == nil` 검사를 우회하여 문자열 `"{...}"`로 포맷되거나 타입 추론이 실패하는 결함 확인. 또한 `pkg/wire/tds/rpc_output.go:101`(`outputColumnInfo`)에 `storage.Value`가 직접 전달될 때의 타입 추론 누락 방어.
    - `formatSQLValue`, `inferSQLType`, `outputColumnInfo`에 `isNilTdsValue` 및 `unwrapTdsOutputValue`를 전면 적용하여 스칼라 언래핑 및 `NULL` 일관성 완비.
    - TDD Red 단계 확립: `pkg/wire/tds/rpc_output_test.go`에 `formatSQLValue`, `inferSQLType`, `outputColumnInfo`, `WriteReturnValue`에 대한 `storage.NullValue`, `*storage.Value(nil)`, 스칼라 `storage.IntValue(42)` 심화 단언 추가 후 실패(RED) 확인.
    - TDD Green 단계 적용 후 100% PASS 확인 (`pkg/wire/tds` 전체 패키지 0 data races).
  - **판정**: `RESOLVED`

---

### Domain 6: API Server, Standalone Workers & CLI (`pkg/api`, `pkg/dbmail`, `cmd/lionsql`)

---

#### [AUDIT-093-020] `cmd/lionsql/sql.go:174-240`
* **대상 영역**: CLI 서브커맨드(`lionsql sql`, `lionsql cli`, `lionsql migrate`)의 `lionsql.engine.json` 매니페스트 검증 및 생성 누락, 플래그 미지정 시 엔진 오탐, `coord.Close()` 누락
* **Antigravity 1차 분석 × Claude 2차 교차 분석**:
  - `cmd/lionsql/server.go`는 기동 시 `config.ResolveOrWriteEngineManifest`를 호출하여 기존 데이터 디렉토리의 엔진 종류(`memengine` vs `wiredtiger`)를 검증하고 불일치 시 엔진 오염을 차단함.
  - 반면 `cmd/lionsql/sql.go`의 `openCoordinatorWithOptions`는 `lionsql.engine.json`을 전혀 확인하지 않아, WiredTiger 데이터 디렉토리에 대해 `-engine` 플래그 없이 `lionsql sql "..."`을 실행하면 기본값인 `memengine`으로 디렉토리를 열어버리는 사고가 발생함.
  - 또한 `runSQL`과 `runCLI`에서 `defer coord.Close()`를 호출하지 않아 CLI 종료 시 WAL/스냅샷 정상 플러시 없이 프로세스가 종료됨 (`[RULE:HandleHygiene]`).
* **합의된 결함 및 패치 결과**:
  - **심각도**: `High (P1)`
  - **1차 패치 내용**: `openCoordinatorWithOptions`에서 `config.ResolveExistingEngineManifest`를 호출하여 기존 엔진 매니페스트 불일치를 차단하고, `runSQL` 및 `runCLI`에 `defer coord.Close()`를 추가.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & 신규 디렉토리 매니페스트 자동 생성, `migrate.go` 플래그 판별 정규화 및 리소스 해제 완비)**:
    - 사각지대 발굴:
      1. `openCoordinatorWithOptions`가 `ResolveExistingEngineManifest`만 호출하여 신규 디렉토리를 CLI로 초기화할 때 `lionsql.engine.json`을 기록하지 않는 결함 확인.
      2. `cmd/lionsql/migrate.go`(`runMigrateUp`, `runMigrateDown`, `runMigrateStatus`)에서 `defer coord.Close()`가 누락되어 세션 및 캐시 정리가 보장되지 않는 결함 (`[RULE:HandleHygiene]`).
      3. `migrate.go`가 `fs.Visit`을 거치지 않고 플래그 미입력 시 `config.DefaultDisk()`를 하드코딩하여 넘김으로써, 비기본 엔진(예: WiredTiger) 디렉토리에서 플래그 없이 `migrate` 실행 시 사용자가 명시적 엔진 플래그를 넘긴 것으로 오인되어 `ErrEngineMismatch` 오탐이 발생하는 결함 발견.
    - 해결 조치:
      - `openCoordinatorWithOptions`에서 `config.ResolveOrWriteEngineManifest`를 호출하여 신규 디렉토리 생성 시 `lionsql.engine.json` 원자적 기록(`[RULE:AtomicSaves]`) 및 기존 매니페스트 불일치 Fail-Closed 차단.
      - `cmd/lionsql/sql.go`에 단일화된 `resolveEngineOptions` 헬퍼를 도입하여 `runSQL`, `runCLI`, `runMigrateUp`, `runMigrateDown`, `runMigrateStatus` 전반의 5개 중복 플래그 추출 블록을 일원화 (`[RULE:RadicalKISS]`, `[RULE:ModularAbstraction]`).
      - `migrate.go`의 3개 진입점에 `defer coord.Close()` 추가.
    - TDD Red 단계 확립:
      - `cmd/lionsql/doctor_test.go`에 `TestRegression_AUDIT_093_020_OpenCoordinatorWritesManifestForFreshDirectory` 추가하여 신규 디렉토리 매니페스트 미생성 시 실패(RED) 확인.
      - `TestRegression_AUDIT_093_020_MigrateSubcommandAutoResolvesManifestWithoutEngineFlag` 추가하여 플래그 없는 마이그레이션의 매니페스트 자동 감지 검증.
    - TDD Green 단계 적용 후 100% PASS 확인 (`cmd/lionsql` 전체 테스트 통과).
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
  - **1차 패치 내용**: `sanitizeSMTPHeader(s string) string` 함수를 추가하여 모든 헤더 필드에서 `\r`, `\n`, `\x00`을 제거하고, `ProcessBatch`에서 `Exec` 에러를 확인하여 반환하며, `cli_dbmail.go`에서 `defer client.Close()`를 보장하도록 수정.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & `\x00` NUL 바이트 주입 방어, `sqlDBClient.Close` 멱등성 및 로그 인서트 Fail-Closed 전파)**:
    - 사각지대 발굴:
      1. `sanitizeSMTPHeader`가 `\x00`(NUL) 바이트를 남겨두어 C-문자열 절단 및 비정상 주입 위험 잔존.
      2. `sqlDBClient.Close()`에 `sync.Once` 방어가 없어 `worker.Stop()`과 `defer client.Close()`가 연속 호출될 때 `[RULE:DoubleCloseSafety]`(Resource Teardown Idempotency) 위반 위험.
      3. `ProcessBatch` 내 `sysmail_log` 인서트 실패 시 `_, _ = w.client.Exec(ctx, logSQL)`로 에러를 완전히 침묵(`_ = err`)하여 DB 장애를 은폐하는 결함 (`[RULE:ExplicitError]`).
      4. `sysmail_log` 인서트 시 `sendErr.Error()` 내 NUL 바이트(`\x00`) 미정제로 CGo/렉서 오염 위험 (`[RULE:CharSanitization]`).
    - 해결 조치:
      - `sanitizeSMTPHeader`에 `strings.ReplaceAll(s, "\x00", "")` 추가.
      - `sqlDBClient`에 `closeOnce sync.Once`를 적용하여 다중 호출 시에도 안전하게 1회만 `Close()` 실행.
      - `ProcessBatch`에서 `sysmail_log` 인서트 에러를 Fail-Closed로 상위 전파하고 `desc` 내 `\x00` 정제.
      - `nextLogID`에서 `MAX(log_id)` 조회 실패 시 경고 로그를 출력하여 진단성 확보.
      - `cmd/lionsql/cli_dbmail.go`에서 `client` 생성 직후 `defer client.Close()` 등록하여 에러 및 정상 SIGTERM 종료 양쪽 모두 결정론적 핸들 정리 보장 (`[RULE:HandleHygiene]`).
    - TDD Red 단계 확립:
      - `TestRegression_AUDIT_093_021_SanitizeSMTPHeaderNULByteInjection` 추가하여 NUL 바이트 미제거 시 실패(RED) 확인.
      - `TestRegression_AUDIT_093_021_ProcessBatchLogFailureFailsClosed` 추가하여 `sysmail_log` 실패 시 에러 전파 검증.
      - `TestRegression_AUDIT_093_021_SqlDBClientDoubleCloseSafety` 추가하여 다중 Close 멱등성 검증.
    - TDD Green 단계 적용 후 100% PASS 확인 (`pkg/dbmail` 전체 테스트 통과).
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
  - **1차 패치 내용**: `api.Server.Close()`에서 `s.crons`, `s.outboxDispatcher`, `s.webhooks`, `s.broker`를 정지하도록 수정하고, `cmd/lionsql/server.go` 종료 시퀀스에도 `outboxDispatcher.Stop()`을 추가.
  - **2차 심화 패치 (Re-Patch / TDD Red-Green & `s.Close()` 전수 자원 정리 및 `sync.Once` 멱등성 완비)**:
    - 사각지대 발굴:
      1. `api.Server`가 소유한 `cdcStreamer`(`*cdc.Streamer`) 및 `replication`(`*replication.Manager`)의 `Close()`가 `s.Close()`에서 누락되어, 임베디드 모드나 테스트 시 복제 감시/하트비트 루프 및 CDC 고루틴이 영구 고아(Orphan)로 누수되는 결함 확인 (`[RULE:ZeroOrphan]`).
      2. `api.Server.Close()`에 멱등성 가드가 없어 중복 호출 시 다중 종료 에러/패닉 위험 (`[RULE:DoubleCloseSafety]`).
    - 해결 조치:
      - `Server` 구조체에 `closeOnce sync.Once` 및 `closeErr error`를 도입하여 다중 `Close()` 시 100% 멱등성 보장.
      - `s.Close()` 내부에서 `crons.Stop()`, `outboxDispatcher.Stop()`, `cdcStreamer.Close()`, `replication.Close()`, `webhooks.Close()`, `broker.Close()`, `coordinator.Close()` 전수를 1회 안전 종료하도록 일원화.
      - `cdc.Streamer` 및 `replication.Manager`에 `IsClosed() bool` 조회 메서드를 추가하여 안전한 상태 검증성 확립.
    - TDD Red 단계 확립:
      - `pkg/api/cron_test.go`의 `TestRegression_AUDIT_093_022_APIServerCloseStopsAllBackgroundWorkers`에 `cdcStreamer` 및 `replication.Manager`를 부착하고 `srv.Close()` 실행 시 미정지로 인한 실패(RED) 확인.
    - TDD Green 단계 적용 후 100% PASS 확인 (`pkg/api` 전체 테스트 통과).
  - **판정**: `RESOLVED`
