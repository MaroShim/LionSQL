# LionSQL 1.0 전수 감사 P1 (Should Fix 47선) 난이도별 분류 및 미니배치 안정화 실행 계획 (`notes/plans/P1_AUDIT_STABILIZATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-09
> - **과제 식별 코드**: `AUDIT-PATCH-P1`
> - **참조 원천 SSOT**: [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](../PRE_RELEASE_AUDIT_TRIAGE.ko.md), [`notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md`](../PRE_RELEASE_AUDIT_FINDINGS.ko.md)
> - **적용 원칙**: `[RULE:RadicalKISS]`, `[RULE:PipelineVerification]`, `[RULE:DefensiveIndexing]`, `[RULE:HandleHygiene]`, `[RULE:ExplicitError]`, `[RULE:PlanPersistence]`
> - **목적**: P0 37건 전량 완치 이후, 1.0 안정화 대상인 P1(Should Fix) 47건에 대해 구현 난이도 및 시스템 파급도 기준 **하(Low, 21건) / 중(Medium, 18건) / 상(High, 8건)**으로 정밀 분류하고, 패키지 경계 기반 미니 배치(Mini-Batch) TDD 실행 전략을 수립함.

---

## 1. 난이도별 분류 총괄 매트릭스 (Difficulty Matrix)

| 난이도 | 건수 | 비중 | 기술적 특성 및 리스크 수준 | 실행 전략 |
| :---: | :---: | :---: | :--- | :--- |
| **하 (Low)** | **21건** | 44.7% | 슬라이스/정수 오버플로우 경계 방어(`0 <= idx < len`), `Close()` 누락 보강, 단순 문자열 언이스케이프, 침묵 에러(`return nil`) 제거 (수정 5~20줄, **락 구조 변경 0건, 부작용 제로**) | **패키지별 6개 미니 배치(3~4건씩) TDD 즉각 격파** |
| **중 (Medium)** | **18건** | 38.3% | 세션/커서/임시테이블 수명주기 훅, 카탈로그 디스크 영속화 연동, 소켓 ReadDeadline 부여, 파서 부분 분기 위임 (단일 모듈 내 상태 관리) | **도메인별 묶음 패치 순차 진행** |
| **상 (High)** | **8건** | 17.0% | 스토리지 커밋 역연산(Undo) 롤백, CGo 활성 커서 Ref-Count 드레인, 비동기 AOF Rewrite 델타 버퍼링 등 코어 동시성/락 구조 변경 수반 | **개별 심층 설계 후 선별 적용 또는 v1.1.0 이관 심사** |
| **합계** | **47건** | **100.0%** | **전수 감사 P1 (Should Fix) 전체** | **3단계 파이프라인 (`Build ➔ Test -race ➔ Commit`) 엄수** |

---

## 2. [1단계] 난이도 '하 (Low)' 21건 — 6대 미니 배치(Mini-Batch) 실행 계획

동일하거나 인접한 1~2개 패키지 단위로 3~4건씩 묶어 **원인 격리 100% 보장** 및 **배치별 TDD + `-race` 즉시 검증**을 수행합니다.

### Mini-Batch 1: `pkg/proc/runtime` — VDBE VM 경계 방어 및 재귀 상한 (4건) [완료: `1fcc772`]
1. **`결함 3-2`** (`vm.go:2282, 2318, 2347`, `vm_dispatcher.go:1540`): `callUDF`, `callProc`, `callSpExecuteSql`, `OpCallBuiltin`에서 음수 레지스터 인덱스(`regIdx < 0`) 슬라이스 패닉 방어 (`regIdx >= 0 && int(regIdx) < len(f.regs)`).
2. **`결함 3-3`** (`vm.go:1194`): `fireTriggers` 트리거 재귀 깊이(32) 초과 시 `return nil` 무음 스킵 대신 명시적 에러 반환.
3. **`결함 3-4` / `Scope-067`** (`vm.go:2270`): 스칼라 UDF(`callUDF`)에 `f.callDepth >= 64` 상한 가드 설치 및 `subFrame.callDepth = f.callDepth + 1` 전파.
4. **`결함 3-5`** (`vm_dispatcher.go:1472, 1505, 1515, 1522`): `OpCursorFetch`, `OpCursorClose`, `OpCursorDealloc`, `OpCallBuiltin`의 `f.aux(&inst)` 무보호 타입 단언을 2-value 안전 단언으로 교체.
- **검증 테스트**: `TestP1_Batch1_VMSafetyAndRecursionGuards` (`pkg/proc/runtime/regression_test.go`) — PASS.

### Mini-Batch 2: `pkg/storage` — 바이너리 코덱 64비트 오버플로우 및 SkipList 경계 방어 (3건) [완료]
1. **`Scope-010`** (`pkg/storage/memengine/flat_record.go:61`): `decodeVarSlot`에서 `uint32` 덧셈 래핑(`vOff + vLen` 32비트 오버플로우) 및 32비트 `int` 부호 반전으로 인해 `int(vOff+vLen) > len(varArea)` 길이 검사가 우회되는 취약점 차단 (`uint64(vOff)+uint64(vLen) > uint64(len(varArea))`).
2. **`Scope-018`** (`pkg/storage/tuple.go:367-420, 452-456`): `DecodeTupleValue` 및 `SkipTupleColumn`에서 `binary.Uvarint`가 반환한 거대 `uint64` 길이(`strLen`)를 `int(strLen)`로 캐스팅하여 `offset + n + int(strLen)` 부호 있는 정수 오버플로우(음수 전환)로 슬라이스 패닉이 발생하는 결함 차단.
3. **`Scope-008`** (`pkg/storage/memengine/skiplist.go:163-168`): `SkipList.Delete` 노드 언링크 루프에서 `i < len(curr.forward)` 방어 조건을 추가하여 비정상 레벨 불일치 시 슬라이스 패닉 원천 차단.
- **검증 테스트**: `TestP1_Batch2_TupleUvarintOverflowGuard` (`pkg/storage/tuple_test.go`), `TestP1_Batch2_FlatRecordAndSkipListBounds` (`pkg/storage/memengine/flat_record_test.go`) — PASS.

### Mini-Batch 3: `pkg/storage` — 스토리지 엔진 라이프사이클 및 진단 안전성 (3건) [완료]
1. **`결함 1-3`** (`pkg/storage/wiredtiger/wt_engine.go:2318-2323`): WiredTiger `Commit` 실패(`WT_ROLLBACK` 등) 시에도 `C.go_wt_session_reset(tx.sess.sess)`을 반드시 호출하여 세션 내부 더티 상태 잔류 방지.
2. **`Scope-001`** (`pkg/storage/memengine/memengine.go:357`): `OpenWithOptions` 재호출 시 기존 `stopCh`가 열려 있으면 먼저 닫아 이전 `bgSaveLoop` 고루틴 영구 누수 차단.
3. **`Scope-003`** (`pkg/storage/memengine/table_data.go:149, 180`): Flat 모드 버퍼 인코딩/디코딩 실패 시 무음 무시 대신 명시적 폴백/진단 보장.
- **검증 테스트**: `TestP1_Batch3_ReopenNoGoroutineLeakAndCorruptMaterialize` (`pkg/storage/memengine/flat_record_test.go`), `TestP1_Batch3_SessionResetAfterCommitConflict` (`pkg/storage/wiredtiger/wt_engine_test.go`) — PASS.

### Mini-Batch 4: `pkg/execution` & `pkg/planner` — 실행 이터레이터 정리 및 플래너 경계 방어 (4건) [완료]
1. **`결함 4-3`** (`pkg/execution/join.go`): `HashJoinIterator.Close()`에서 `h.left.Close()` 외에 누락된 `h.right.Close()` 호출을 추가하여 빌드 측 커서/리소스 누수 차단.
2. **`결함 4-4`** (`pkg/execution/sort.go`): `SortIterator.NextChunk`의 Min-Heap 디코딩 중 `io.EOF`가 아닌 디스크 I/O 에러 발생 시 무음 종료 대신 즉시 에러 반환.
3. **`결함 10-5`** (`pkg/execution/window.go:525-528`): 윈도우 프레임 `FrameFollowing` 오프셋 계산 시 `startIdx > total` 슬라이스 상한 클램핑 방어.
4. **`결함 4-5`** (`pkg/planner/planner.go`): Nested Loop Join 계획 수립 시 표현식 인덱스에 대해 `len(innerIndex.Columns) > 0` 확인 후 `Columns[0]` 참조.

### Mini-Batch 5: `pkg/engine` & `pkg/proc` — 커서 상태머신, MERGE 이스케이프, 임시테이블 (4건) [완료: `990ff01`]
1. **`결함 8-5`** (`pkg/engine/sqlexec_merge.go:544-553`): `MERGE (VALUES ...)` 소스 파싱 시 작은따옴표 이스케이프(`''` ➔ `'`) 및 큰따옴표(`""` ➔ `"`) 언이스케이프 적용.
2. **`Scope-049`** (`pkg/engine/sqlexec_cursor.go:86-98, 343-511`): `CLOSE <cursor>` 시 커서 정의를 맵에서 삭제하지 않고 `IsOpen = false`로 전환하여 `DEALLOCATE` 전 재오픈(`OPEN`) 지원.
3. **`결함 3-6`** (`pkg/proc/runtime/vm_dispatcher.go:1451-1549`): VM 커서에서 이미 열린 커서 중복 `OPEN` 및 닫힌 커서 `CLOSE` 시 표준 에러 반환.
4. **`결함 2-5`** (`pkg/engine/session.go:231-335`): 존재하지 않는 `#temp` 테이블 단순 조회 시 `TempTableMap`에 팬텀 슬롯이 누적되지 않도록 생성(`RegisterTempTable`)과 조회(`ResolveTempTableName`) 분리.
- **검증 테스트**: `TestP1_Batch5_MergeUnescapeCursorReopenAndTempTableNoPhantom` (`pkg/engine/sqlexec_cursor_test.go`), `TestP1_Batch5_VMCursorDuplicateOpenAndClosedCloseErrors` (`pkg/proc/runtime/cursor_test.go`) — PASS.

### Mini-Batch 6: `cmd/lionsql` — CLI, Doctor 및 Windows 타이머 정리 (3건) [완료]
1. **`Scope-132`** (`cmd/lionsql/doctor.go:188-221`): `--doctor` 스토리지 점검(`probeStorageEngine`) 시 `eng.Open()` 직후 `defer func() { _ = eng.Close() }()`를 등록하여 중간 단계 에러 발생 시에도 임시 디렉터리 삭제 전 엔진 `Close()` 보장.
2. **`Scope-133`** (`cmd/lionsql/cli_config.go:114-135, 211-247`, `cmd/lionsql/sql.go:89-125`): CLI 패스워드 입력(`readPasswordInteractive`) 중 `SIGINT`/`SIGTERM` 수신 시 `term.Restore` 복구 훅(`sync.Once`) 호출 보장 및 `lionsql sql` 실행 시 데몬 가동 중이면 명확한 접속 안내 출력.
3. **`Scope-131`** (`cmd/lionsql/daemon_windows.go:56-68`, `cmd/lionsql/daemon_unix.go:47-58`, `cmd/lionsql/main.go:124`, `cmd/lionsql/server.go:875`): Windows 데몬 및 CLI 종료 시 `cleanupPlatformTimer()`(`sync.Once`)를 통해 `timeEndPeriod(1)` 대칭 호출 보장.
- **검증 테스트**: `TestP1_Batch6_DoctorCloseOnErrorTerminalRestoreAndTimerCleanup` (`cmd/lionsql/doctor_test.go`) — PASS.

---

## 3. [2단계] 난이도 '중 (Medium)' 18건 분류 목록

### 도메인 A: 카탈로그 영속화 및 DDL 무결성 (5건)
1. **뷰(View) 수정 시 디스크 영속화 동기화** (`pkg/catalog/catalog.go:340-355`): `CreateView`/`AlterView`/`DropView` 시 `persistObjectsLocked()` 호출 연동.
2. **`결함 7-3` 복합 식별자 정규화** (`pkg/catalog/catalog.go`): `cleanCatalogIdent`에서 `[dbo].[my_proc]` 대괄호/스키마 분리 정규화.
3. **`결함 8-4` `DROP TABLE` 외래키 참조 검증** (`pkg/engine/sqlexec_ddl.go`): 자식 테이블이 외래키로 참조 중인 부모 테이블 무단 삭제 차단.
4. **`Scope-041` `ALTER TABLE ADD FOREIGN KEY` 스키마 반영** (`pkg/engine/sqlexec_ddl.go:1546`): 파싱된 FK 제약조건을 실제 스키마에 등록.
5. **`Scope-032` `pg_attribute` 기본값 매핑 보강** (`pkg/catalog/virtual/pg.go:948`): ORM 마이그레이션 시 반복 `ALTER` 방지.

### 도메인 B: 쿼리 실행기 가드레일 및 문법 위임 (6건)
6. **`결함 14-1` `CROSS/OUTER APPLY` 10만 행 상한 가드레일** (`pkg/engine/sqlexec_apply.go:72-100`).
7. **`결함 4-2` / `Scope-089` External Sort Spill 실패 시 임시 파일 즉시 삭제** (`pkg/execution/sort.go:181-235`).
8. **`결함 8-3` `WITHOUT HOLD` 커서 트랜잭션 종료 시 자동 닫기** (`pkg/engine/sqlexec_cursor.go:87-97`).
9. **`Scope-047` 파생 테이블 구체화 중 에러 시 물리 임시 테이블 롤백 드롭** (`pkg/engine/sqlexec_derived.go:145-180`).
10. **`Scope-038` T-SQL 단순 `BEGIN ... END` 블록의 트랜잭션 오인식 차단** (`pkg/engine/sqlexec.go:651`).
11. **`결함 6-4` & `6-5` Native 파서 `SELECT ... INTO` 및 `JOIN (WITH ...)` 서브쿼리 위임** (`pkg/proc/parser_dml.go`).

### 도메인 C: 네트워크 타임아웃, 복제 에러 전파 및 데몬 (7건)
12. **`결함 13-4` 내장 SPA 웹 서버(`/assets/*.js`) 서브폴더 정적 에셋 허용** (`pkg/api/spa.go:35-37`).
13. **`결함 5-3` 레플리카 WAL 적용 실패 에러 무음 삼킴 제거** (`pkg/replication/client.go`).
14. **`결함 5-4` PGWire SSL 경로 `CancelRequest`의 `CancelSessionQuery` 정상 호출** (`pkg/wire/server.go`).
15. **`Scope-100` / `101` / `104` PGWire & TDS 핸드셰이크 `SetReadDeadline` 부여** (`pkg/wire/tls.go`, `pkg/wire/tds/server.go`, `tls.go`).
16. **`Scope-097` PGWire 장기 연결 Portal 개수 상한 가드** (`pkg/wire/server.go:924`).
17. **`Scope-127` & `129` 웹훅 재시도 컨텍스트 즉시 중단 및 서버 종료 시 WAL `Sync()` 보장** (`pkg/api/webhook.go`, `cmd/lionsql/server.go`).
18. **`Scope-130` & `135` 데몬 PID 원자적 쓰기 및 테스트 하네스 자식 프로세스 정리** (`cmd/lionsql/daemon.go`, `cmd/soaktest/main.go`).

---

## 4. [3단계] 난이도 '상 (High)' 8건 분류 목록 (심층 설계 대상)

1. **`결함 1-1` & `Scope-002`** (`pkg/storage/memengine/persistence.go`): 비동기 AOF Rewrite 중 동시 DDL 패닉 및 델타 트랜잭션 유실 방어.
2. **`결함 1-2` & `Scope-006`** (`pkg/storage/memengine/tx.go`): In-Memory OCC Commit 6단계 도중 에러 시 부분 커밋(Partial Apply) 역연산(Undo) 롤백.
3. **`Scope-005`** (`pkg/storage/memengine/tx.go`): 동일 트랜잭션 내 `DELETE` 직후 동일 PK `INSERT` 재삽입 시 `baseVer` 버전 추적 정합성.
4. **`Scope-012`** (`pkg/storage/memengine/persistence.go`, `ddl.go`): `RENAME TABLE` AOF 리플레이 복원 및 `ALTER TABLE` 후 Flat 레이아웃 동기화.
5. **`결함 1-4` & `Scope-013`** (`pkg/storage/wiredtiger/wt_engine.go`): WiredTiger `DropTable`/`TruncateTable` 및 세션 종료 시 CGo 활성 커서 Use-After-Free 방지.
6. **`Scope-014`** (`pkg/storage/wiredtiger/wt_engine.go`): WiredTiger 복합 유니크 인덱스 검증 시 풀스캔($O(N^2)$) 제거 및 인덱스 커서 탐색 전환.
7. **`결함 9-4` & `9-5`** (`pkg/backup/backup.go`, `pitr.go`): 온라인 백업 스냅샷 카탈로그 일치 및 PITR 복원 시 DDL WAL과 `catalog.Catalog` 동기화.
8. **`Scope-134`** (`pkg/migrate/migrate.go`): 마이그레이션 다중 DDL 실행 중 부분 실패 시 원자적 롤백/복구.
