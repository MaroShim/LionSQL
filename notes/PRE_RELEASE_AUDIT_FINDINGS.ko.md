# LionSQL 1.0 전 소스코드 100단계 듀얼 AI (Gemini × Claude) 정밀 감사 결과 기록부 (`notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **문서 성격**: 1.0 릴리스 대비 100단계 듀얼 AI 전수 코드 리뷰에서 도출된 모든 아키텍처 결함, 동시성 레이스, 데드락, 메모리 누수, 침묵하는 에러(`_ = err`), 경계값 결함을 영구 누적 기록하는 물리 저장소 (SSOT)
> - **과제 식별 코드**: `AUDIT-100-DUAL-REVIEW`
> - **진행 모드**: Scan-First (100단계 전수 감사 누적 완료 후 체계적 일괄 수정)
> - **판정 체계**:
>   - `PASS`: 결함 없음 확인 (0 Defect)
>   - `DEFECT_LOGGED`: 엣지 케이스 및 결함 식별 완료 (수정 대기)

---

## 전역 감사 진행 현황 (Summary Dashboard)

* **총 스코프**: 100단계
* **감사 완료**: 10 / 100 (10.0%)
* **식별된 결함**: 8건 (Critical: 2, High: 4, Medium: 2, Low: 2)

---

## 스코프별 상세 감사 로그 (Audit Findings Log)

### [Scope 001] `pkg/storage/memengine/memengine.go` (1/2)
* **대상 영역**: OCC 엔진 수명주기, 초기화, 테이블 카탈로그 맵 관리, Close
* **Gemini 1차 분석**:
  - `Close()` 시 락 밖에서 `bgWg.Wait()`를 수행하여 백그라운드 고루틴 종료 대기 중 데드락을 방어한 점은 우수함.
  - `OpenWithOptions` 호출 시 이미 엔진이 가동 중인 상태에서 재호출 가드가 없어 백그라운드 고루틴 및 파일 디스크립터 누수 위험 확인.
* **Claude 2차 교차 분석**:
  - `tx.go:30`에서 트랜잭션 시작 시 `isClosed.Load()`를 선제 체크하므로 `Close()` 도중 트랜잭션 유입 레이스는 안전함 (오탐 배제).
  - 그러나 `memengine.go:357`의 `OpenWithOptions`는 엔진이 이미 열려 있을 때(`!e.closed && e.stopCh != nil`) 재호출에 대한 가드가 완전히 부재함. 이전 `bgSaveLoop`가 고아(Orphan) 고루틴으로 남아 영원히 메모리를 점유할 수 있음 (`[RULE:ZeroOrphan]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/storage/memengine/memengine.go:357` (`OpenWithOptions`)
  - **시나리오**: 실행 중인 엔진에 대해 실수 또는 재시작 로직에서 `Open()` / `OpenWithOptions()`가 재호출될 경우, 이전 `stopCh`를 닫지 않고 새 채널로 덮어써 이전 `bgSaveLoop` 고루틴이 백그라운드에 영구 누수됨.
  - **권장 수정안**:
    ```go
    if !e.closed && e.stopCh != nil {
        return fmt.Errorf("memengine is already open: close existing instance first")
    }
    ```
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 002] `pkg/storage/memengine/memengine.go` (2/2) & `persistence.go` (연계)
* **대상 영역**: 읽기/쓰기 트랜잭션 수명주기, WAL 커밋(`finishWALCommitLocked`), AOF 백그라운드 리라이트, 디스크 스냅샷
* **Gemini 1차 분석**:
  - `finishWALCommitLocked`에서 비동기 AOF rewrite를 트리거할 때 `go func() { e.rewriteAOFLocked() }`를 실행.
  - 그런데 `rewriteAOFLocked` 내부의 `saveDiskLocked`는 `e.diskMu`만 획득하고 `e.mu`를 획득하지 않은 채 `e.tables` 맵을 직접 range 순회함.
* **Claude 2차 교차 분석**:
  - **치명적 Data Race 및 런타임 패닉 식별**: 백그라운드 AOF rewrite 및 RDB 저장 도중 다른 고루틴이 DDL(`CREATE/DROP TABLE`)을 수행하여 `e.mu.Lock()` 상태에서 `e.tables[name] = td`를 기록하면, `concurrent map read and map write` 즉시 패닉 발생.
  - 이미 엔진 내부에 락 프리 스냅샷을 위해 `e.tablesAtomic`(`atomic.Pointer[map[string]*TableData]`)이 완비되어 있음에도, `saveDiskLocked`에서는 이를 활용하지 않고 보호되지 않은 맵을 직접 순회한 명백한 동시성 결함.
* **합의된 결함 (Defect)**:
  - **심각도**: `High` (동시 DDL 발생 시 런타임 패닉)
  - **위치**: `pkg/storage/memengine/persistence.go:292` (`saveDiskLocked`) 및 `pkg/storage/memengine/memengine.go:195`
  - **시나리오**: 백그라운드 AOF rewrite(또는 주기적 RDB 저장)가 도는 수 밀리초 동안 동시 세션에서 `CREATE TABLE` 또는 `DROP TABLE`을 호출할 경우 Go 런타임이 `fatal error: concurrent map read and map write`를 던지며 프로세스 즉사.
  - **권장 수정안**:
    ```go
    // persistence.go saveDiskLocked
    // e.tables 직접 순회 대신 원자적 스냅샷 맵 tablesAtomic 활용
    tablesPtr := e.tablesAtomic.Load()
    if tablesPtr == nil {
        return nil
    }
    for name, td := range *tablesPtr {
        if storage.IsTemporaryTable(name) {
            continue
        }
        persistTables[name] = td
    }
    ```
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 003] `pkg/storage/memengine/table_data.go` (1/2)
* **대상 영역**: 테이블 샤드 파티션(`numTableShards = 256`), 샤드 엔트리 튜플 레이아웃, `IndexData` 스킵리스트 연동
* **Gemini 1차 분석**:
  - `numTableShards = 256` 기반 FNV-1a 해시 샤딩으로 고동시성 행 락 경합을 극적으로 낮춘 구조 우수.
  - `getRowAndVer`에서 `materialize` 실패 시 주석에는 `Log corruption`이라고 명시되어 있으나 실제 로그 출력 코드가 누락되어 데이터 손상 시 침묵 실패 위험 (`[RULE:ExplicitError]`).
* **Claude 2차 교차 분석**:
  - `putRowAndVer`에서 `td.mode == OCCModeFlat` 인코딩 실패 시 에러가 완전히 무음 삼킴(`err != nil` 로그 없음)되어 원인 모를 Raw 모드 혼재가 발생할 수 있음.
  - `deleteRowAndVer`에서 삭제된 엔트리의 `materialize` 실패 시 `entry.row`를 그대로 반환하는데, Flat 모드에서는 `entry.row.Values`가 비어있어 호출자에게 빈 행(Empty Row)이 반환되어 트리거/CDC에 빈 데이터가 전파될 수 있음.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/storage/memengine/table_data.go:149`, `180`
  - **시나리오**: Flat 모드에서 버퍼 디코딩/인코딩 실패 시 로그 없이 침묵 반환하여 디버깅 추적 불가. 삭제 트리거 실행 시 빈 행 전파 가능성.
  - **권장 수정안**:
    ```go
    if err != nil {
        log.Printf("[memengine] TableData materialize failed for pk %q: %v", pk, err)
        return storage.Row{}, 0, false
    }
    ```
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 004] `pkg/storage/memengine/table_data.go` (2/2)
* **대상 영역**: 스냅샷 생성(`snapshotRowsAndKeys`), 샤드 동기화(`syncShardsToMapsLocked`), 컬럼 동적 추가(`addColumnValues`), 파티션 메타데이터
* **Gemini 1차 분석**:
  - `snapshotRowsAndKeys`의 0~255 정규 락 순서 및 역순 해제는 Deadlock Free 보장으로 모범적임.
  - `syncShardsToMapsLocked`에서 `td.Rows[k] = entry.row`를 수행할 때, Flat 모드(`OCCModeFlat`)에서는 `entry.row.Values`가 비어있어 디스크 스냅샷 저장 시 빈 행이 직렬화되어 데이터 유실 위험.
* **Claude 2차 교차 분석**:
  - **치명적 데이터 증발(Silent Data Loss) 결함 식별**: `addColumnValues`(`ALTER TABLE ADD COLUMN`) 실행 시 `append(entry.row.Values, val)`을 수행하는데, Flat 모드에서는 기존 데이터가 `entry.flatBuf`에 들어있어 `entry.row.Values`가 `nil`임. 따라서 기존 데이터 전체가 날아가고 신규 추가된 컬럼 1개만 남아 **기존 행 데이터가 100% 영구 소실**됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical` (Flat 모드에서 ALTER TABLE ADD COLUMN 시 기존 데이터 전체 영구 증발)
  - **위치**: `pkg/storage/memengine/table_data.go:344` (`addColumnValues`) 및 `277` (`syncShardsToMapsLocked`)
  - **시나리오**: Flat 모드(`OCCModeFlat`) 활성화 환경에서 테이블에 데이터를 채운 뒤 `ALTER TABLE t ADD col INT DEFAULT 0`을 실행하면, 모든 기존 컬럼 값이 유실되고 신규 컬럼 1개만 남는 치명적 데이터 파괴 발생.
  - **권장 수정안**:
    ```go
    // addColumnValues
    for k, entry := range sh.entries {
        r, err := entry.materialize(td)
        if err != nil {
            r = entry.row
        }
        newRow := storage.Row{Values: append(r.Values, val)}
        if td.mode == OCCModeFlat && td.layout != nil {
            newBuf, err := td.layout.EncodeRowToFlat(&td.Schema, newRow)
            if err == nil {
                sh.entries[k] = shardEntry{ver: entry.ver, flatBuf: newBuf}
                continue
            }
        }
        sh.entries[k] = shardEntry{ver: entry.ver, row: newRow}
    }
    ```
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 005] `pkg/storage/memengine/tx.go` (1/3)
* **대상 영역**: 트랜잭션 수명주기, 사설 워크스페이스(Delta buffer), `BeginTx`, `InsertRow` 고속 패스
* **Gemini 1차 분석**:
  - `txPool`의 `sync.Pool` 재활용 시 `inPool` 플래그 및 맵 클리어 처리는 메모리 할당 최적화와 동시성 면에서 안정적임.
  - `getTableLocked`에서 `tablesAtomic`을 사용하여 `engine.mu` 락 경합을 회피한 락 프리 카탈로그 조회 구조 우수.
* **Claude 2차 교차 분석**:
  - **`DELETE` 후 재삽입(`INSERT`) 시 OCC 버전 소실 결함**: `InsertRow`의 인덱스 없는 고속 패스(줄 284)에서, 동일 트랜잭션 내에서 이전에 삭제된 행(`tx.isLocallyDeleted == true`)에 대해 다시 `InsertRow`를 호출할 경우, `kind = changeInsert`, `baseVer = 0`으로 덮어씀.
  - 그러나 해당 행은 이미 스토리지에 특정 버전(ver > 0)으로 실재하므로, 커밋 검증 시 이미 존재하는 키로 오인되어 `ErrKeyExists` 거짓 에러를 뿜거나 다른 동시 트랜잭션의 갱신을 맹목적으로 덮어쓰는 팬텀/충돌 결함 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `High` (동일 트랜잭션 내 DELETE ➔ INSERT 재삽입 시 커밋 충돌 또는 팬텀 덮어쓰기)
  - **위치**: `pkg/storage/memengine/tx.go:284` (`InsertRow` Fast-path)
  - **시나리오**: `BEGIN TRAN; DELETE FROM t WHERE id=1; INSERT INTO t VALUES (1, 'new'); COMMIT;` 시, 고속 패스에서 `baseVer`가 0으로 초기화되어 커밋 단계에서 검증 실패 또는 오작동.
  - **권장 수정안**:
    ```go
    if tx.isLocallyDeleted(schema.Name, pkKey) {
        // 기존 행의 원본 baseVer를 유지하고 kind를 changeUpdate로 승격하거나 원래 baseVer 보존
        origVer := tx.getBaseVer(schema.Name, pkKey)
        tx.changes[schema.Name+":"+pkKey] = rowChange{
            kind: changeUpdate,
            baseVer: origVer, ...
        }
    }
    ```
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 006] `pkg/storage/memengine/tx.go` (2/3)
* **대상 영역**: OCC 유효성 검증(`Commit` Step 5), 유니크 인덱스 충돌 감지, 샤드 원자적 커밋 적용(Step 6), WAL 프레임 커밋
* **Gemini 1차 분석**:
  - `sort.Strings(tableNames)` 및 샤드 인덱스(0~255) 오름차순 잠금과 역순 해제(`defer unlockAll`)는 Deadlock Free를 완벽히 보장함.
  - WAL 직렬화를 샤드 락 획득 전에 수행(`Step 3.1 Out-of-Lock WAL Encoding`)하여 락 보유 시간을 최소화한 최적화는 매우 우수함.
* **Claude 2차 교차 분석**:
  - **치명적 원자성 파괴 (Partial Commit / ACID 위반 결함)**: `Step 6`에서 `tx.changes` 루프를 돌며 `sh.entries[ch.key]`에 행을 쓰고 `idxData.Insert`를 호출하는 도중, 만약 후반부 행에서 `EncodeRowToFlat` 에러나 `idxData.Insert` 에러가 발생하여 `return err`가 실행되면, 앞서 이미 메모리 샤드에 쓰여진 행들은 롤백되지 않고 그대로 노출됨.
  - 트랜잭션의 기본 원칙인 All-or-Nothing 원자성이 깨지고 데이터가 반쪽만 반영된 채 커밋 상태로 잔류함 (`[RULE:Invariant 6]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical` (트랜잭션 커밋 중간 에러 시 부분 커밋 잔류로 ACID 원자성 붕괴)
  - **위치**: `pkg/storage/memengine/tx.go:1905`, `1925` (`Commit` Step 6)
  - **시나리오**: 여러 행을 INSERT/UPDATE하는 트랜잭션에서 마지막 행의 Flat 인코딩 실패 또는 보조 인덱스 삽입 에러 발생 시, 이전 행들은 메모리 샤드에 영구 잔류하고 트랜잭션은 에러를 반환하여 상태 불일치 발생.
  - **권장 수정안**:
    - 모든 행의 `EncodeRowToFlat` 및 인덱스 키 유효성 검사를 `Step 5 (검증 단계)`에서 선행 완료(Pre-validate)하여, `Step 6 (적용 단계)`에서는 실패 없이 순수 메모리 대입만 발생하도록 2-Phase 분리.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 007] `pkg/storage/memengine/tx.go` (3/3)
* **대상 영역**: 롤백(`Rollback`), 세이브포인트(`Savepoint`, `RollbackTo`, `ReleaseSavepoint`), `txPool` 반환 수명주기
* **Gemini 1차 분석**:
  - `Savepoint` 시점의 사설 워크스페이스 상태를 딥카피(`cloneLocalMap`, `cloneChangesMap`)하여 `RollbackTo` 시 이전 스냅샷으로 정확히 복원하는 로직 우수.
  - `Rollback()`에서 `inPool` 플래그로 이중 반환(Double Put)을 방어함.
* **Claude 2차 교차 분석**:
  - **`Commit()` 성공 시 `txPool` 미반환으로 인한 GC 부하 증가**: `Commit()` 경로에서는 `releaseToPoolLocked()`가 호출되지 않아, 정상 커밋된 트랜잭션 객체는 `defer tx.Rollback()` 패턴을 명시적으로 감싸지 않은 호출자의 경우 풀로 회수되지 않고 GC 가비지로 전락함 (`[RULE:ZeroAllocation]` 효율 저하).
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/storage/memengine/tx.go:2065` (`Commit` 반환부)
  - **시나리오**: `defer tx.Rollback()` 없이 단독 `tx.Commit()`을 수행하는 실행 경로에서 트랜잭션 구조체와 할당된 맵들이 재활용되지 못하고 가비지 컬렉터 부하를 유발함.
  - **권장 수정안**: `Commit()` 성공 완료 직전 `tx.releaseToPoolLocked()` 호출 추가 또는 클린업 일원화.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 008] `pkg/storage/memengine/skiplist.go`
* **대상 영역**: 락 프리 SkipList 인덱스, `Insert`, `Delete`, `Search`, `ScanRange`, 스냅샷 직렬화(`ToEntries`/`FromEntries`)
* **Gemini 1차 분석**:
  - `NewSkipList`에서 `head.forward`를 `maxSkipListLevel = 16`으로 안전하게 할당하고 `sl.mu`로 전역 동기화하여 동시성 안전성을 확보함.
  - `ScanRange` 및 `ScanRangeEntries`에서 `[low, high]` 양방향 폐구간(Inclusive) 정렬 순회 로직 정합성 우수.
* **Claude 2차 교차 분석**:
  - **`Delete` 시 노드 언링크 단계에서 방어적 슬라이스 경계 체크 누락 (`[RULE:DefensiveIndexing]`)**: `Delete` 루프(줄 158)에서 `update[i].forward[i] = curr.forward[i]`를 대입할 때, `curr.forward`의 길이는 노드 생성 시 할당된 `lvl`(`<= maxSkipListLevel`)임. 반면 루프는 `sl.level`까지 순회함.
  - 이론적으로 `i >= len(curr.forward)`인 경우 `curr`가 해당 레벨에 포워드 링크를 갖지 않으므로 `update[i].forward[i] != curr`가 되어 break되어야 하나, 만약 손상된 인덱스 복원이나 동시성 레이스가 개입할 경우 `curr.forward[i]` 접근 시 슬라이스 인덱스 범위 초과 패닉 발생 가능.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/storage/memengine/skiplist.go:160` (`Delete`)
  - **시나리오**: `Delete` 시 `i >= len(curr.forward)` 방어 조건이 없어 비정상적인 레벨 불일치 시 `index out of range` 패닉 가능성.
  - **권장 수정안**:
    ```go
    for i := 0; i < sl.level && i < len(curr.forward); i++ {
        if update[i].forward[i] != curr {
            break
        }
        update[i].forward[i] = curr.forward[i]
    }
    ```
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 009] `pkg/storage/memengine/flat_record.go` (1/2)
* **대상 영역**: Flat Record 바이너리 레이아웃(`FlatRecordLayout`), 고정/가변 슬롯 분할, `EncodeRowToFlat`
* **Gemini 1차 분석**:
  - `NullBitmapLen` 8바이트 정렬 및 비트마스킹을 통한 0-할당 NULL 판별 구조 우수.
  - 가변 길이 컬럼(VARCHAR, JSON, BLOB)의 4바이트 인라인 SSO(Small String Optimization) 최적화 도입.
* **Claude 2차 교차 분석**:
  - **치명적 SSO 태그 덮어쓰기 및 데이터 오염 결함 식별**: `EncodeRowToFlat` (줄 163)에서 `len > 4`인 힙 저장 대상에 대해 `slot[0] = 0; slot[1] = 0x00` 태그를 설정한 직후, 바로 다음 줄에서 `binary.LittleEndian.PutUint32(slot[0:4], vOff)`를 실행하여 앞서 설정한 태그 바이트를 덮어씀.
  - 만약 `vOff` 값의 2번째 바이트가 우연히 `0xFF`(예: 오프셋이 65,280 바이트를 넘는 지점)가 되면, 디코더가 이를 인라인 문자열(`slot[1] == 0xFF`)로 오인식하여 가변 힙 대신 슬롯의 가비지 데이터를 문자열로 반환하는 심각한 데이터 왜곡 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High` (가변 오프셋이 커졌을 때 SSO 인라인 태그 오인식으로 인한 데이터 왜곡)
  - **위치**: `pkg/storage/memengine/flat_record.go:163` (`EncodeRowToFlat`)
  - **시나리오**: 한 행에 여러 개의 큰 문자열/JSON이 들어가 `varHeap` 오프셋(`vOff`)의 상위 바이트에 `0xFF`가 포함될 때, 후속 가변 컬럼이 인라인 데이터로 오인식되어 데이터 손상.
  - **권장 수정안**:
    - 태그 영역과 오프셋 슬롯을 명확히 분리 (예: 8바이트 슬롯 중 `slot[7]` 또는 최상위 비트를 인라인 플래그로 전용하고, 하위 31비트를 오프셋으로 사용).
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 010] `pkg/storage/memengine/flat_record.go` (2/2)
* **대상 영역**: Flat Record 디코딩(`DecodeFlatToRow`), NULL 비트맵 역직렬화, 가변 영역 버퍼 절단 및 정수 오버플로우 검증
* **Gemini 1차 분석**:
  - `len(buf) < l.TotalFixedLen` 및 `vOff+vLen > len(varArea)` 경계 검사를 통해 손상된 버퍼 읽기 시 패닉 대신 `ErrFlatRecordTruncated`를 반환하는 방어 로직 모범적임.
  - 시간 타입(DateTime/Date)의 UTC 시간대 고정 복원 정합성 확보.
* **Claude 2차 교차 분석**:
  - **`uint32` 덧셈 오버플로우 방어 취약점 (`[RULE:DefensiveIndexing]`)**: 손상된 버퍼나 악의적 데이터에서 `vOff = 0xFFFFFFFF`, `vLen = 2`인 경우, `uint32` 덧셈(`vOff + vLen`)이 먼저 오버플로우되어 0x00000001로 래핑된 후 `int()`로 캐스팅될 수 있음. 이 경우 `len(varArea)` 검사를 우회하여 슬라이스 패닉 발생 가능.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/storage/memengine/flat_record.go:275`, `292`, `308`, `319` (`DecodeFlatToRow`)
  - **시나리오**: `uint64(vOff) + uint64(vLen) > uint64(len(varArea))` 형태로 64비트 확장을 거치지 않아 정수 래핑 시 버퍼 절단 가드가 무력화될 수 있음.
  - **권장 수정안**:
    ```go
    if uint64(vOff)+uint64(vLen) > uint64(len(varArea)) {
        return storage.Row{}, ErrFlatRecordTruncated
    }
    ```
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 011] `pkg/storage/memengine/wal.go`
* **대상 영역**: WAL 레코드 직렬화, 그룹 커밋(`syncUpTo`), `ReplayWAL`, `Truncate`
* **Gemini 1차 분석**:
  - `syncMu` -> `mu` 계층을 엄격히 준수하여 그룹 커밋 중 디스크 I/O 시 `mu`를 점유하지 않는 논블로킹 설계 우수.
  - 바이너리 MessagePack 길이 접두사 프레이밍(`[4-byte len | payload]`)을 단일 `write(2)` syscall로 처리하여 원자성 확보.
* **Claude 2차 교차 분석**:
  - **일시적 I/O 에러 시 WAL 파일 비가역적 파괴(Truncation) 결함 (`wal.go:343, 377`)**:
    `ReplayWAL` 루프에서 `io.ReadFull(reader, lenBuf[:])` 실행 중 일시적인 디스크 오류(`EIO`)나 권한 문제가 발생하면 `err != io.EOF`임에도 단순 `break` 처리됨. 루프 탈출 후 줄 377에서 `if stat.Size() > validOffset { _ = file.Truncate(validOffset) }`가 무조건 실행되어, 오류 지점 이후의 정상적인 잔여 WAL 레코드 전체를 디스크에서 영구 삭제함.
  - **`Truncate` 실패 시 닫힌 파일 디스크립터 잔류 (`wal.go:270, 275`)**:
    `w.file.Close()` 후 `os.OpenFile`이 실패할 경우 `w.file`이 닫힌 핸들 상태로 유지되어 후속 `Append` 시 예측 불가능한 패닉 발생 가능.
* **합의된 결함 (Defect)**:
  - **심각도**: `High` (일시적 I/O 읽기 오류 시 AOF/WAL 파일 비가역적 절단 데이터 손실)
  - **위치**: `pkg/storage/memengine/wal.go:343, 377` (`ReplayWAL`)
  - **시나리오**: 디스크 배드섹터나 일시적 I/O 지연 발생 시 서버 재기동 시점에 유효한 커밋 로그가 영구 절단 삭제됨.
  - **권장 수정안**:
    `err != nil && err != io.EOF && err != io.ErrUnexpectedEOF`인 경우 즉시 에러를 반환하고, 오직 EOF에 의한 정상 종료나 말단 테일 손상(Torn-write)이 확인된 경우에만 `Truncate`를 수행하도록 격리.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 012] `pkg/storage/memengine/persistence.go`, `ddl.go`
* **대상 영역**: AOF 로그 리플레이(`applyWALRecordLocked`), DDL 변경분 반영, 테이블 리네임 및 인덱스 복구
* **Gemini 1차 분석**:
  - DDL 적용 시 임시 테이블 필터링(`storage.IsTemporaryTable`) 철저.
  - 파티션 테이블 키 분기 및 보조 인덱스 동기화 반영 루틴 완결성 양호.
* **Claude 2차 교차 분석**:
  - **`RenameTable`의 AOF 리플레이 누락 및 재기동 시 리네임 무효화 (`persistence.go:92-100`, `ddl.go:665`)**:
    `RenameTable` 실행 시 `WALOp{Type: OpAlterTable, Table: newClean}` 형태로 기록되나 이전 테이블명(`oldClean`)이 보존되지 않고 `op.Column == nil`임. 재기동 시 `applyWALRecordLocked`는 `OpAlterTable`에서 `op.Column != nil`인 경우만 처리하므로, 테이블 리네임이 재기동 시 완전히 무시되어 구 테이블명으로 복원됨.
  - **Flat 모드에서 AOF 리플레이 시 `td.layout` 갱신 누락으로 인한 오프셋 오염 (`persistence.go:92-100`)**:
    `OpAlterTable` 리플레이 시 `td.Schema.Columns`에 컬럼을 추가하고 `td.addColumnValues`를 호출하지만, `td.layout = BuildFlatRecordLayout(&td.Schema)`를 호출하지 않아 고정/가변 오프셋 레이아웃이 갱신되지 않음. 이후 플랫 레코드 디코딩 시 메모리 경계 오염 발생.
  - **`OpCreateIndex` 리플레이 시 Covering 인덱스 `IncludeCols` 누락 (`persistence.go:140`)**:
    `idxData.Insert(keyVal, pk)` 호출 시 `IncludeCols` 추출 로직이 누락되어, AOF 복원 후 커버링 인덱스가 포함 컬럼을 유실함.
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical` (테이블 리네임 AOF 복원 불가 및 Flat 모드 스키마 레이아웃 불일치)
  - **위치**: `pkg/storage/memengine/persistence.go:92-100, 140`, `pkg/storage/memengine/ddl.go:665`
  - **시나리오**: `RENAME TABLE` 수행 후 엔진 재기동 시 테이블명이 복원되지 않으며, `ALTER TABLE` 후 재기동 시 Flat 메모리 레이아웃이 깨져 조회 실패.
  - **권장 수정안**:
    1) `OpRenameTable` 전용 WALOp 신설 또는 `op.Key`에 `oldName` 저장하여 리플레이.
    2) `OpAlterTable` 시 `if td.mode == OCCModeFlat { td.layout = BuildFlatRecordLayout(&td.Schema) }` 반영.
    3) `OpCreateIndex` 시 `extractIncludeValuesFromRow`를 통해 `incVals`도 함께 전달.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 013] `pkg/storage/wiredtiger/wt_engine.go` (1/3)
* **대상 영역**: CGo 바인딩, `WT_CONNECTION` 수명주기, 세션 풀러, 그룹 커밋(`syncUpTo`)
* **Gemini 1차 분석**:
  - `fastCString`을 통한 스택 버퍼 활용으로 CGo 힙 할당 최소화 우수.
  - `Close()` 시 드레인 루프와 `connClosed` 원자 플래그로 use-after-free 방어 구축.
* **Claude 2차 교차 분석**:
  - **`syncUpTo` 동시성 호출 시 `WT_SESSION` 스레드 안전성 위반 및 세션 닫힘 레이스 (`wt_engine.go:259, 2342`)**:
    `tx.Commit()`에서 `eng.syncUpTo(tx.sess.sess, seq)` 호출 시 `eng.syncMu`는 획득하지만 `tx.sess.mu`는 획득하지 않음. 만약 동시 세션에서 `WTSession.Close()`가 호출될 경우 `tx.sess.sess`가 닫히는 도중 `C.go_wt_session_log_flush_sync(sess)`가 실행되어 해제된 C 포인터 역참조(SIGSEGV) 발생 가능.
* **합의된 결함 (Defect)**:
  - **심각도**: `High` (동시 세션 종료 시 WiredTiger log_flush 세션 CGo 크래시)
  - **위치**: `pkg/storage/wiredtiger/wt_engine.go:259, 2342`
  - **시나리오**: 동시 트랜잭션 커밋과 세션 Close가 맞물릴 때 유효하지 않은 WT_SESSION 포인터로 `log_flush` 호출.
  - **권장 수정안**:
    전용 백그라운드 플러시 세션을 `Engine` 수준에서 단일 관리하거나, `syncUpTo` 진입 시 세션 수명주기 락을 명시적으로 보호.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 014] `pkg/storage/wiredtiger/wt_engine.go` (2/3)
* **대상 영역**: WiredTiger 트랜잭션 수명주기(`WTTx`), 행 삽입/수정/삭제, MVCC 충돌 제어(`WT_ROLLBACK`)
* **Gemini 1차 분석**:
  - `WT_ROLLBACK` 에러를 `storage.ErrSerializationFailure` (SQLSTATE 40001)로 정확히 매핑하여 재시도 가능 트랜잭션 규약 준수.
  - 세이브포인트(`Savepoint`, `RollbackTo`, `ReleaseSavepoint`) 역순 롤백 체인 구현 우수.
* **Claude 2차 교차 분석**:
  - **빈 슬라이스 인덱싱 패닉 가능성 (`[RULE:DefensiveIndexing]`, `wt_engine.go:848`)**:
    `AddColumn` 행 재작성 시 `unsafe.Pointer(&u.data[0])`를 호출함. 인코딩된 바이트 길이가 0인 경우 런타임 패닉 발생.
  - **유니크 인덱스 검증 시 O(N^2) 전수 테이블 스캔 (`wt_engine.go:1732-1750`)**:
    `checkUniqueIndexesLocked`에서 각 행의 `Insert`/`Update`마다 `tx.scanLocked(tableName)`을 호출하여 테이블 전체를 전수 스캔함. 대용량 행 입력 시 심각한 락 점유 및 지연 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High` (유니크 인덱스 검증의 O(N^2) 전수 테이블 스캔 및 슬라이스 경계 미확인 접근)
  - **위치**: `pkg/storage/wiredtiger/wt_engine.go:848, 1732`
  - **시나리오**: 유니크 인덱스가 걸린 테이블에 대량 삽입 시 기하급수적 지연 및 0바이트 레코드 시 패닉.
  - **권장 수정안**:
    1) `len(u.data) > 0` 가드 추가 또는 `unsafe.SliceData` 적용.
    2) WiredTiger 인덱스 전용 커서를 사용하거나 최소한 B-Tree 키 조회를 통해 전수 스캔 제거.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 015] `pkg/storage/wiredtiger/wt_engine.go` (3/3)
* **대상 영역**: `IndexScan`, `IndexScanDesc`, `wtFilteredIndexCursor`, 커서 풀링
* **Gemini 1차 분석**:
  - `wtFilteredIndexCursor`를 통한 복합 인덱스 및 JSON 표현식 인덱스 필터링 유연성 확보.
  - 유휴 커서 풀링(`maxIdleCursorsPerURI = 16`)으로 커서 오픈 오버헤드 완화.
* **Claude 2차 교차 분석**:
  - **무구간 `IndexScan` 시 인덱스 정렬 파괴 결함 (`wt_engine.go:2026-2028`)**:
    `low == nil && high == nil`일 때 `tx.Scan(tableName)`의 결과 커서(`cur`)를 그대로 반환함. 이는 기본키(PK) 순서이지 해당 보조 인덱스의 순서가 아님. 따라서 인덱스 스캔을 요구한 쿼리가 인덱스 정렬 순서가 아닌 물리적 PK 순서로 행을 반환하는 중대한 정렬 불일치 발생 (`IndexScanDesc`에서도 PK 역순으로 잘못 반환).
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical` (보조 인덱스 무구간 스캔 시 인덱스 정렬 순서 파괴 및 PK 순서 오반환)
  - **위치**: `pkg/storage/wiredtiger/wt_engine.go:2026-2028, 2091-2130`
  - **시나리오**: `ORDER BY idx_col`을 인덱스 스캔으로 최적화할 때 전혀 엉뚱한 PK 정렬 결과가 반환됨.
  - **권장 수정안**:
    `low == nil && high == nil`이라도 인덱스 순서 정렬을 유지하도록 인메모리 정렬 커서로 래핑하거나 실제 인덱스 키 순서 순회 보장.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 016] `pkg/storage/wiredtiger/wt_stub.go`
* **대상 영역**: CGo/WiredTiger 비활성화 빌드 시 스텁 구현체
* **Gemini 1차 분석**:
  - `storage.Engine` 인터페이스의 모든 시그니처를 100% 충족하며 순수 Go 빌드(`CGO_ENABLED=0`) 시 컴파일 깨짐 방지.
* **Claude 2차 교차 분석**:
  - 컴파일 및 런타임 에러 전파 명확함.
* **합의된 결함 (Defect)**: 없음 (`CLEAN`)
* **최종 판정**: `PASS`

---
### [Scope 017] `pkg/storage/engine.go`
* **대상 영역**: `storage.Engine`, `Transaction`, `Session`, `Cursor` 핵심 인터페이스 명세 및 `DataType`, `TableSchema`
* **Gemini 1차 분석**:
  - `IsWriteConflict` 함수를 통해 두 스토리지 엔진(OCC 및 WiredTiger)의 동시성 충돌 에러를 일관되게 정규화함.
  - `FindBestCompositeIndex`를 통한 최적 인덱스 프리픽스 매칭 로직 정합성 우수.
* **Claude 2차 교차 분석**:
  - `FastRandN(n)` (줄 60): `UnixNano() % int64(n)` 연산에서 나노초 클록의 음수 래핑 방어(`v < 0 { v = -v }`)는 있으나, `math.MinInt64`의 경우 부호 반전 시 여전히 음수가 될 수 있는 이론적 엣지 케이스 존재.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/storage/engine.go:60-63` (`FastRandN`)
  - **시나리오**: 극단적 나노초 값에서 음수 모듈로 발생 방어 필요.
  - **권장 수정안**: `uint64(time.Now().UnixNano()) % uint64(n)` 형태로 비부호 정수 변환.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 018] `pkg/storage/record.go`, `tuple.go`
* **대상 영역**: 대소문자 무감각 `Record`, 바이너리 튜플 코덱(`EncodeRowToBuffer`, `DecodeTupleToRow`), NULL 비트맵
* **Gemini 1차 분석**:
  - `Record.Get`의 3단계 탐색(Exact -> Prefix stripped -> EqualFold)으로 식별자 정합성 보장.
  - `tupleBufferPool` (`sync.Pool`)을 활용한 0-할당 버퍼 재사용 모범적임.
* **Claude 2차 교차 분석**:
  - **`DecodeTupleValue` 가변 길이 역직렬화 시 정수 오버플로우 가드 취약 (`tuple.go:367, 375, 383, 391`)**:
    `strLen, n := binary.Uvarint(data[offset:])`에서 손상된 패킷으로 인해 `strLen`이 극단적으로 큰 경우, `offset + n + int(strLen)` 덧셈이 정수 오버플로우를 일으켜 `len(data)` 가드를 우회하고 이후 슬라이싱에서 패닉 유발 가능.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/storage/tuple.go:367, 375, 383, 391` (`DecodeTupleValue`)
  - **시나리오**: `uint64(offset) + uint64(n) + strLen > uint64(len(data))` 가드가 아닌 32/64비트 혼합 부호 정수 덧셈으로 인한 오버플로우 취약점.
  - **권장 수정안**: 64비트 비부호 정수 단위로 사전 경계 검증.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 019] `pkg/storage/decimal.go`
* **대상 영역**: 임의 정밀도 고정소수점 `Decimal` 산술 연산(`Add`, `Sub`, `Mul`, `Div`, `Rescale`) 및 JSON 코덱
* **Gemini 1차 분석**:
  - `big.Int` 기반의 완벽한 십진수 산술 구현으로 부동소수점 오차 원천 배제.
  - 사사오입(Half-Up) 반올림 알고리즘의 대칭성 검증 우수.
* **Claude 2차 교차 분석**:
  - **`Int64()` 변환 시 오버플로우 감지 부재 (`decimal.go:359-365`)**:
    `rescaled.value.Int64()`는 `big.Int`가 64비트 범위를 초과할 때 하위 64비트만 잘라내어 반환하므로, 거대 수치 변환 시 무음(Silent) 데이터 왜곡 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/storage/decimal.go:359-365` (`Decimal.Int64`)
  - **시나리오**: `10^20` 이상의 큰 Decimal을 `Int64()`로 변환할 때 에러 없이 음수나 오염된 정수값 반환.
  - **권장 수정안**: `rescaled.value.IsInt64()` 검사 또는 오버플로우 방어 로직 추가.
* **최종 판정**: `DEFECT_LOGGED`

---
### [Scope 020] `pkg/storage/value_codec.go`
* **대상 영역**: MessagePack 콤팩트 2-요소 배열(`[Type, Payload]`) 인코딩/디코딩, 레거시 맵 호환성
* **Gemini 1차 분석**:
  - 레거시 맵 포맷과 초소형 배열 포맷 간의 완벽한 하위 호환성 디코딩 제공.
* **Claude 2차 교차 분석**:
  - **배열 길이 확장 시 스트림 디싱크(Desynchronization) 취약점 (`value_codec.go:95-97`)**:
    `arrLen > 2`인 확장 포맷이 입력될 경우, 요소 0과 1만 읽고 루프를 종료하여 나머지 미소비 바이트가 디코더 스트림에 남아 후속 레코드 디코딩을 연쇄 파괴함.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/storage/value_codec.go:95-97` (`DecodeMsgpack`)
  - **시나리오**: 3개 이상의 요소를 가진 확장 배열 수신 시 스트림 위치 불일치로 후속 역직렬화 실패.
  - **권장 수정안**: `for i := 2; i < arrLen; i++ { _ = dec.Skip() }`를 통해 미사용 후속 슬롯 드레인.
* **최종 판정**: `DEFECT_LOGGED`

---
