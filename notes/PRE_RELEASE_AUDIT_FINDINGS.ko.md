# LionSQL 1.0 전 소스코드 135단계 듀얼 AI (Gemini × Claude) 정밀 감사 결과 기록부 (`notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **문서 성격**: 1.0 릴리스 대비 135단계 듀얼 AI 전수 코드 리뷰에서 도출된 모든 아키텍처 결함, 동시성 레이스, 데드락, 메모리 누수, 침묵하는 에러(`_ = err`), 경계값 결함을 영구 누적 기록하는 물리 저장소 (SSOT)
> - **과제 식별 코드**: `AUDIT-135-DUAL-REVIEW`
> - **진행 모드**: Scan-First (135단계 전수 감사 누적 완료 후 체계적 일괄 수정)
> - **판정 체계**:
>   - `PASS`: 결함 없음 확인 (0 Defect)
>   - `DEFECT_LOGGED`: 엣지 케이스 및 결함 식별 완료 (수정 대기)

---

## 전역 감사 진행 현황 (Summary Dashboard)

* **1단계 (135단계 전수 광역 스캔)**: 135 / 135 (100.0%) 완주 완료 (총 134건 결함 식별)
* **2단계 (12대 핵심 코어 심층 재스캔)**: 9 / 12 구역 완료 (구역 1~9: 48건 발굴 완료, 구역 10~12 진행 중)
* **총 누적 식별 결함**: 총 182건 (1단계 134건 + 2단계 48건)
  - Critical: 20건 (+2)
  - High: 82건 (+2)
  - Medium: 65건 (+1)
  - Low: 15건 (+0)
  - Critical: 16건 (+3)
  - High: 78건 (+5)
  - Medium: 63건 (+2)
  - Low: 15건 (+0)

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

### [Scope 021] `pkg/storage/coerce.go`, `types_extended.go`
* **대상 영역**: SQL 데이터 타입 상향 승격 격자(Type Promotion Lattice), DateTime/Date 변환, UUID 및 Hex 바이너리 파서
* **Gemini 1차 분석**:
  - Decimal > Float > Int > Bool 및 DateTime/Date > VarChar 타입 격자 계층 구조 체계적임.
* **Claude 2차 교차 분석**:
  - **DateTime <-> Date 변환 시 타입 불일치 반환 (`coerce.go:75-77`)**:
    `DateTime`과 `Date` 비교 시 `return a, b, true`로 반환하나 실제 타입 변환이 이루어지지 않아, `coerced=true`이면 `c1.Type == c2.Type`이라는 불변식이 깨짐.
  - **`DateValueFromString` 파싱 실패 시 연도 0001 비정상 날짜 묵인 (`types_extended.go:120-126`)**:
    문자열 파싱 실패 시 `NullValue(TypeDate)` 대신 `0001-01-01` 비-NULL 날짜를 반환하여 데이터 왜곡 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/storage/coerce.go:75-77`, `pkg/storage/types_extended.go:120-126`
  - **시나리오**: 잘못된 날짜 문자열 입력 시 에러나 NULL 대신 `0001-01-01`이 저장됨.
  - **권장 수정안**: Date <-> DateTime 변환 시 `DateTimeValue(b.TVal)`로 단일화하고, 날짜 파싱 실패 시 `NullValue(TypeDate)` 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 022] `pkg/storage/json.go`
* **대상 영역**: Bounded JSON DOM 캐시, `ScanJSONTopLevelKey` 제로 할당 파서, `->`/`->>` 연산자 추출기
* **Gemini 1차 분석**:
  - 정규식 없이 단일 바이트 순회로 JSON 키와 중첩 괄호 depth를 추적하는 제로-할당 고속 추출기 설계 우수.
* **Claude 2차 교차 분석**:
  - **`jsonDOMCache` 퇴출 시 문자열 참조 메모리 유지 (`json.go:44`)**:
    `c.keys = c.keys[evictCount:]` 슬라이싱 시 퇴출된 앞쪽 슬롯을 빈 문자열로 초기화하지 않아 언더라잉 배열이 대형 JSON 문자열을 계속 참조하는 메모리 누수 가능성.
  - **JSON 배열 음수 인덱스(-1) 역방향 탐색 미지원 (`json.go:320`)**:
    PostgreSQL 표준인 `-1` 역방향 인덱싱 시 false를 반환하여 표준 호환성 결여.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/storage/json.go:44`, `320`
  - **시나리오**: 거대 JSON 문서를 반복 캐시할 때 퇴출된 문자열 메모리가 GC되지 않음.
  - **권장 수정안**: `for i := 0; i < evictCount; i++ { c.keys[i] = "" }` 클리어 및 음수 인덱스 지원.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 023] `pkg/storage/arena.go`, `slice_cursor.go`
* **대상 영역**: 쿼리 스코프 메모리 아레나 할당기(`Arena`), 슬랩 풀링(`sync.Pool`), `SliceCursor` 및 `ValueFromAny`
* **Gemini 1차 분석**:
  - `Value` 슬랩과 `any` 슬랩 재활용 시 `clear(*slabPtr)`를 호출하여 GC 포인터 누수를 방지한 점 모범적임.
* **Claude 2차 교차 분석**:
  - **`ValueFromAny`에서 Date/Time 타입 요청 시 DateTime 및 VarChar로 변질 반환 (`slice_cursor.go:118-127`)**:
    `targetType == TypeDate` 또는 `TypeTime`일 때 `time.Time`이 들어오면 `DateTimeValue`를 반환하고, 문자열이 `"2006-01-02 15:04:05"` 포맷이 아니면 `StringValue`로 폴백하여 요청된 타입을 무시함.
  - **`AllocString` 반환 문자열의 수명 초과 시 인플레이스 오염 위험 (`arena.go:160`)**:
    `unsafe.String`이 아레나 슬랩 버퍼를 직접 가리키므로, 아레나 Reset 후 슬랩이 풀에서 재사용될 때 문자열 내용이 변경될 위험.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/storage/slice_cursor.go:118-127` (`ValueFromAny`)
  - **시나리오**: `DATE` 컬럼에 날짜 문자열 입력 시 `VARCHAR` 값으로 잘못 포장되어 인덱스 비교 오류 유발.
  - **권장 수정안**: `DateValueFromString`, `TimeValueFromString`을 호출하여 명시적으로 `TypeDate`, `TypeTime` 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 024] `pkg/storage/synthetic_pk.go`, `benchdir.go`, `literal.go`
* **대상 영역**: 인조 기본키 컬럼 정의(`_row_id`), 벤치마크 디렉터리 격리 생성, UUIDv7 생성기 및 디폴트 리터럴 파서
* **Gemini 1차 분석**:
  - `benchdir.go`에서 상위 디렉터리를 지우지 않고 `MkdirTemp` 하위 디렉터리만 안전 제거하는 방어 설계 우수.
* **Claude 2차 교차 분석**:
  - **UUIDv7 12비트 시퀀스 카운터 오버플로우 시 단조성(Monotonicity) 파괴 (`literal.go:36-48`)**:
    단일 밀리초 내 4,096개 이상의 UUIDv7이 생성될 때 12비트 카운터가 0으로 래핑되어 RFC 9562 시간 순서 불변식이 깨짐.
  - **`ParseLiteralDefault` 숫자 파싱 실패 시 `StringValue` 침묵 반환 (`literal.go:182`)**:
    타입이 `TypeInt`인데 잘못된 문자열이 들어왔을 때 에러나 NULL 대신 `StringValue`를 반환하여 타입 오염 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/storage/literal.go:36-48`, `182`
  - **시나리오**: 극초고속 동시 UUIDv7 생성 시 B+Tree 인덱스 순서 역전 발생 가능성.
  - **권장 수정안**: 12비트 오버플로우 시 다음 밀리초 대기 또는 타임스탬프 1ms 가산 처리.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 025] `pkg/catalog/catalog.go` (1/2)
* **대상 영역**: 저장 프로시저, 사용자 정의 함수(UDF), 트리거 등록/수정/삭제 및 대소문자 무시 식별자 정규화
* **Gemini 1차 분석**:
  - `cleanCatalogIdent`를 통한 `[dbo].[proc]` 등 방언 접두사 표준화 및 읽기/쓰기 RWMutex 동기화 철저.
* **Claude 2차 교차 분석**:
  - **`AlterTrigger` 영속화 누락으로 재기동 시 트리거 변경 증발 (`catalog.go:388-390`)**:
    `CreateTrigger`, `RegisterTrigger`, `AlterProcedure`, `AlterFunction`과 달리 `AlterTrigger`에서만 `c.persistObjectsLocked()` 호출이 완전히 누락되어, 엔진 재시작 시 수정된 트리거 코드가 구버전으로 롤백됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/catalog/catalog.go:388-390` (`AlterTrigger`)
  - **시나리오**: `ALTER TRIGGER` 실행 후 정상 수정된 것처럼 보이나 서버 재부팅 시 구버전 트리거로 복원됨.
  - **권장 수정안**: `c.persistObjectsLocked()` 호출 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 026] `pkg/catalog/catalog.go` (2/2)
* **대상 영역**: CBO 테이블 통계 캐시, 원자적 JSON 영속화(`catalog_objects.json`), 뷰(View) 및 확장 통계 객체 관리
* **Gemini 1차 분석**:
  - 임시 파일(`tmpPath`) 기록 후 `os.Rename`을 통한 원자적 파일 교체(`[RULE:AtomicSaves]`) 모범적 적용.
* **Claude 2차 교차 분석**:
  - **`CreateStatistics` 영속화 누락으로 재기동 시 통계 객체 증발 (`catalog.go:790-796`)**:
    `CreateStatistics` 수행 시 메모리 `c.statistics`에만 저장하고 디스크에 영속화하지 않아 재기동 시 유실됨.
  - **`GetView` 포인터 직접 반환으로 인한 동시성 데이터 레이스 (`catalog.go:752-761`)**:
    `GetProcedure`, `GetFunction`은 복사본(`cp := *proc`)을 반환하여 안전하나, `GetView`는 내부 포인터 `v`를 그대로 반환하여 락 해제 후 외부 변경 시 Data Race 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/catalog/catalog.go:752-761`, `790-796`
  - **시나리오**: `CREATE STATISTICS` 생성 후 재부팅 시 소실되며, 멀티 세션에서 뷰 조회 중 뷰 수정 시 패닉/레이스 발생.
  - **권장 수정안**: `cp := *v; return &cp, nil` 복사본 반환 및 통계 객체 영속화 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 027] `pkg/catalog/virtual/interceptor.go` (1/2)
* **대상 영역**: 제로-할당 대소문자 무시 탐색(`containsFold`), 카탈로그 쿼리 후보 사전 필터링(`hasCatalogCandidate`), AST 쿼리 특성 연동
* **Gemini 1차 분석**:
  - 힙 할당 없는 ASCII `containsFold`와 빠른 접두사 검증으로 가상 카탈로그가 아닌 일반 쿼리의 오버헤드를 0으로 유지.
* **Claude 2차 교차 분석**:
  - **비-ASCII 유니코드 테이블명 대소문자 비교 한계 (`interceptor.go:11-54`)**:
    `containsFold`가 ASCII 범위(`a-z`, `A-Z`)만 처리하므로 비-ASCII(다국어 등) 식별자가 카탈로그 조건에 포함될 때 매칭 실패 가능성.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/catalog/virtual/interceptor.go:11-54` (`containsFold`)
  - **시나리오**: 특수 유니코드 식별자 대소문자 접힘 미지원.
  - **권장 수정안**: 시스템 카탈로그는 표준 ASCII 예약어 위주이나 필요 시 `strings.EqualFold` 폴백 적용.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 028] `pkg/catalog/virtual/interceptor.go` (2/2)
* **대상 영역**: Table-Driven 가상 카탈로그 라우팅 디스패처(`catalogRouteTable`), MSSQL/PG 쿼리 디스패치 및 폴백
* **Gemini 1차 분석**:
  - 330여 줄의 순서 종속적 IF 체인을 `catalogRouteTable` 선언적 정적 디스패치 테이블로 성공적 전환.
* **Claude 2차 교차 분석**:
  - **라우팅 미매칭 시 PostgreSQL 카탈로그(`execClassSelect`) 무조건적 폴백 결함 (`interceptor.go:466-467`)**:
    MSSQL 클라이언트(SSMS, DBeaver MSSQL 모드)가 보낸 `sys.*` 복합 조인 쿼리가 라우트 테이블에 정확히 매칭되지 않았을 때, 폴백으로 PostgreSQL의 `pg_class` 핸들러를 호출하여 전혀 엉뚱한 PostgreSQL 컬럼셋을 반환함.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/catalog/virtual/interceptor.go:466-467` (`Execute`)
  - **시나리오**: SSMS/DBeaver에서 미지원 sys 쿼리 실행 시 PGWire 스키마 컬럼이 응답되어 클라이언트 드라이버 크래시.
  - **권장 수정안**: `upper`에 `SYS.` 또는 `[SYS]`가 포함된 경우 MSSQL 전용 빈 결과셋 또는 명시적 오류 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 029] `pkg/catalog/virtual/mssql.go` (1/2)
* **대상 영역**: MSSQL 테이블/스키마명 동등 조건 추출(`ExtractMssqlTableNameEq`), `OBJECT_ID` 추출기, 데이터 타입 포맷터
* **Gemini 1차 분석**:
  - SQL Server 고유 데이터 타입 매핑(`bit`, `uniqueidentifier`, `nvarchar(max)`) 정합성 우수.
* **Claude 2차 교차 분석**:
  - **`formatMSSQLDataType`의 VarChar 기본 길이 불일치 (`mssql.go:87-91`)**:
    정밀도 미지정(`prec <= 0`) 시 `varchar(255)`로 매핑하나, SQL Server 표준은 `varchar(1)` 또는 `varchar(max)`임.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/catalog/virtual/mssql.go:87-91` (`formatMSSQLDataType`)
  - **시나리오**: 길이 미지정 varchar 컬럼이 255로 표시되어 스키마 리버스 엔지니어링 시 차이 발생.
  - **권장 수정안**: 표준 SQL Server 기본값 정책 반영.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 030] `pkg/catalog/virtual/mssql.go` (2/2)
* **대상 영역**: 외래키 제약조건(`execReferentialConstraintsForeignKey`), 인덱스/컬럼 메타데이터, `sys.all_objects` 시스템 뷰
* **Gemini 1차 분석**:
  - 복합 외래키 컬럼 쌍 순회 및 OnUpdate/OnDelete 액션 문자열 변환 완결성 양호.
* **Claude 2차 교차 분석**:
  - **외래키 부모 테이블 `referenced_object_id` 1000 고정 하드코딩 결함 (`mssql.go:539`)**:
    `execReferentialConstraintsForeignKey`에서 `referenced_object_id`를 항상 첫 번째 테이블(`1000`)로 하드코딩하여 반환함. 참조하는 실제 부모 테이블(예: 3번째 테이블 `1003`) 대신 엉뚱하게 1번 테이블을 가리켜 DBeaver/SSMS ER 다이어그램이 왜곡됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/catalog/virtual/mssql.go:539` (`execReferentialConstraintsForeignKey`)
  - **시나리오**: SSMS나 DBeaver에서 외래키 관계선(FK Relationship)을 조회하면 모든 외래키가 엉뚱하게 첫 번째 테이블로 연결되어 표시됨.
  - **권장 수정안**: `fk.ParentTable`을 `tables` 목록에서 찾아 실제 부모 테이블의 `1000 + parentIdx`를 `referenced_object_id`에 바인딩.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 031] `pkg/catalog/virtual/pg.go` (1/2)
* **대상 영역**: `pg_class`, `pg_attribute`, `pg_index`, `tableBaseOID`
* **Gemini 1차 분석**:
  - `tableBaseOID` (16384 베이스)를 기반으로 PostgreSQL 카탈로그 OID 체계를 모방하여 DBeaver 등 드라이버 핸드셰이크 호환성 확보.
* **Claude 2차 교차 분석**:
  - **`execPgIndexSelect`에서 `indkey`가 `"1"`로 하드코딩되어 복합 인덱스 컬럼 정보 왜곡 (`pg.go:177, 190`)**:
    단일 컬럼 인덱스뿐 아니라 2번째 컬럼 또는 복합 인덱스(Composite Index)인 경우에도 항상 `indkey: "1"`을 반환하여, PostgreSQL GUI/DBeaver에서 인덱스 컬럼 목록이 1번 컬럼으로만 오인식됨.
  - **`execClassSelect`의 스키마 루프로 인한 무조건 2중 중복 반환 (`pg.go:350-455`)**:
    `schemas`(`public`, `dbo`)를 순회하며 모든 테이블과 뷰를 2번씩 추가하므로, 네임스페이스 필터가 없는 `SELECT * FROM pg_class` 실행 시 동일한 테이블이 중복 출력됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/catalog/virtual/pg.go:177, 190` (`execPgIndexSelect`)
  - **시나리오**: DBeaver 등에서 복합 인덱스 구조 조회 시 엉뚱하게 첫 번째 컬럼에만 인덱스가 걸린 것으로 왜곡 표시.
  - **권장 수정안**: 실제 인덱스 구성 컬럼 순서(`idx.Columns`)에 맞추어 `indkey` 문자열을 동적 생성(`"1 2 3"` 등).
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 032] `pkg/catalog/virtual/pg.go` (2/2)
* **대상 영역**: `pg_type`, `pg_database`, `generatePgConstraint`, `generatePgAttribute`
* **Gemini 1차 분석**:
  - PostgreSQL 20대 핵심 OID(`int4`, `float8`, `numeric`, `timestamp` 등) 타입 테이블의 완결성 양호.
* **Claude 2차 교차 분석**:
  - **`generatePgAttribute`에서 `atthasdef`가 항상 `false`로 하드코딩되어 컬럼 기본값 인식 불가 (`pg.go:948`)**:
    테이블 스키마의 컬럼에 `DEFAULT` 값이 지정되어 있어도 `atthasdef: false`를 강제 반환하여 PostgreSQL 클라이언트 드라이버나 ORM(GORM, XORM)이 기본값을 인식하지 못함.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/catalog/virtual/pg.go:948` (`generatePgAttribute`)
  - **시나리오**: GORM/XORM 등에서 마이그레이션 및 메타데이터 동기화 시 기본값 누락으로 인한 불필요한 ALTER 발생.
  - **권장 수정안**: `c.DefaultValue != ""` 검사를 통해 `atthasdef: c.DefaultValue != ""`로 동적 바인딩.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 033] `pkg/catalog/virtual/result.go`
* **대상 영역**: `FormatResult`, `evaluateWhereConditionMap`, `parseImplicitAlias`, `ExtractSQLParts`
* **Gemini 1차 분석**:
  - 제로-정규식 원칙에 입각하여 `findTopLevelKeyword` 및 괄호 매칭을 기반으로 SQL 절 분해 수행.
* **Claude 2차 교차 분석**:
  - **별칭 없는 복합 함수 표현식 프로젝션 시 컬럼 매핑 실패로 `nil` 반환 (`result.go:215-238`)**:
    프로젝션 절에 `COALESCE(c.relname, '')` 등 별칭 없는 복합 식이 들어왔을 때, 컬럼 추출기가 함수 내부 컬럼을 정밀 분해하지 못하고 `colSourceMap`에 매핑되지 않아 값이 `nil`로 침묵 누락됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/catalog/virtual/result.go:215-238` (`FormatResult`)
  - **시나리오**: 복잡한 메타데이터 쿼리 실행 시 일부 표현식 컬럼이 NULL로 응답되어 클라이언트 파싱 실패.
  - **권장 수정안**: 미해결 함수 표현식에 대한 기본 평가자 연결 및 원본 소스 폴백 강화.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 034] `pkg/catalog/virtual/info_schema.go`, `sys_tables.go`, `tables.go`, `source.go`
* **대상 영역**: `information_schema.tables`, `columns`, `schemata`, `sys_tables.go` 가상 테이블 소스
* **Gemini 1차 분석**:
  - ANSI SQL 표준 `information_schema` 컬럼 40여 개 스키마 완결성 우수.
* **Claude 2차 교차 분석**:
  - **`table_catalog`의 `"postgres"` 하드코딩 및 뷰 스키마 고정 (`info_schema.go:49, 67`)**:
    TDS 또는 다른 데이터베이스 컨텍스트에서도 `table_catalog`가 항상 `"postgres"`로 반환되며, `generateInfoSchemaViews`는 `table_schema`가 `"public"`으로 무조건 고정되어 MSSQL 클라이언트 호환성 저해.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/catalog/virtual/info_schema.go:49, 67` (`generateInfoSchemaTables`, `generateInfoSchemaViews`)
  - **시나리오**: SSMS/DBeaver에서 다른 DB/스키마의 뷰 메타데이터를 조회할 때 카탈로그명 불일치 발생.
  - **권장 수정안**: 세션 컨텍스트의 `CURRENT_DATABASE()` 값을 `table_catalog`에 동적 바인딩.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 035] `pkg/engine/coordinator.go`, `coordinator_impl.go` (1/3)
* **대상 영역**: `Coordinator` 구조체 수명주기, 세션 풀 연동, 동적 튜닝 및 `GetOrCreateSession`
* **Gemini 1차 분석**:
  - 세션 풀러(`SessionPool`)와 직접 엔진 인스턴스 간 매끄러운 획득/반환 추상화 구조 우수.
* **Claude 2차 교차 분석**:
  - **`spidCounter`의 `int` 형 오버플로우 시 고유성 훼손 (`coordinator_impl.go:322`)**:
    `spidCounter`가 `int` 타입으로 관리되어, 단기 세션의 수억 회 반복 연결/해제 시 래핑되어 음수 SPID가 생성될 잠재적 위험.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/engine/coordinator_impl.go:322` (`GetOrCreateSession`)
  - **시나리오**: 수억 회 세션 누적 연결 시 SPID 음수화.
  - **권장 수정안**: `atomic.Int64` 기반 양수 범위 보장 카운터로 마이그레이션.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 036] `pkg/engine/coordinator.go`, `coordinator_impl.go` (2/3)
* **대상 영역**: 2-Tier 쿼리 라우팅 (VDBE vs CBO 선택), 트랜잭션 컨텍스트 전파 및 세션 캐시 무효화
* **Gemini 1차 분석**:
  - `InvalidatePKBypassCache`를 통한 스키마 버전(`schemaVersion`) 원자적 갱신 및 캐시 초기화 흐름 양호.
* **Claude 2차 교차 분석**:
  - **동시성 환경에서 `pkBypassCache` 맵 일괄 재할당 시 GC 압력 및 슬롯 경합 (`coordinator_impl.go:167`)**:
    DDL이 빈번한 환경에서 `make(map[uint64]*PKLookupPlan, 256)`로 매번 새로 할당하는 과정에서 일시적 GC 압박 발생 가능.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/engine/coordinator_impl.go:167` (`InvalidatePKBypassCache`)
  - **시나리오**: 대량 DDL 배치 실행 시 맵 재할당 부하.
  - **권장 수정안**: `clear(c.pkBypassCache)` 내장 함수를 사용한 맵 버킷 재활용.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 037] `pkg/engine/coordinator.go`, `coordinator_impl.go` (3/3)
* **대상 영역**: `CancelSessionQuery`, `CancelQueryBySPID`, `GetActiveQueries`, 세션 종료 및 자원 정리(`CloseSession`)
* **Gemini 1차 분석**:
  - 세션 종료 시 임시 테이블(`c.globalTempCreators`, `cs.TableVars`, `cs.TempTables`) 자동 드롭 및 세션 풀 반환 철저.
* **Claude 2차 교차 분석**:
  - **`CloseSession`에서 `cs.Mu.Lock()` 보유 상태로 `c.engine.DropTable()` 동기 호출로 인한 데드락 위험 (`coordinator_impl.go:396-404`)**:
    세션 락을 획득한 채로 스토리지 엔진의 DDL 잠금을 요구하는 `DropTable`을 순회 호출하므로, 다른 고루틴이 스토리지 락을 쥐고 세션 락을 요구할 경우 역순 데드락 발생 위험(`[RULE:LockHierarchy]`).
  - **`CancelQueryBySPID`에서 `cs.SPID` 동시 읽기 시 세션 락 미보호 Data Race (`coordinator_impl.go:514`)**:
    `c.sessMu.RLock()`만 잡고 개별 `cs.Mu` 없이 `cs.SPID` 필드를 직접 검사.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/engine/coordinator_impl.go:396-404` (`CloseSession`)
  - **시나리오**: 세션 종료 시점에 동시 쿼리 실행 또는 DDL 경합 시 데드락 발생.
  - **권장 수정안**: 드롭할 임시 테이블 이름 목록을 `cs.Mu` 안에서 복사한 후, 락을 해제한 상태에서 `DropTable` 호출.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 038] `pkg/engine/sqlexec.go` (1/2)
* **대상 영역**: `execSingleStatement`, 트랜잭션 수명주기(`BEGIN`, `COMMIT`, `ROLLBACK`), `KILL`, `USE`
* **Gemini 1차 분석**:
  - PostgreSQL 모드 실패 트랜잭션 자동 롤백 및 WITH HOLD 커서 보존 등 트랜잭션 경계 제어 견고.
* **Claude 2차 교차 분석**:
  - **T-SQL 세션에서 단순 `BEGIN`을 트랜잭션 시작으로 오인식하는 심각한 문법 결함 (`sqlexec.go:651`)**:
    `isBeginTx`가 `upper == "BEGIN"`을 무조건 트랜잭션 시작으로 처리함. T-SQL에서 트랜잭션은 반드시 `BEGIN TRAN[SACTION]`이어야 하며, 단순 `BEGIN`은 `BEGIN ... END` 복합 구문 블록의 시작임. 세션 Dialect가 T-SQL일 때 단순 `BEGIN`을 트랜잭션으로 열어버려 뒤이은 `END`에서 런타임 문법 에러 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/engine/sqlexec.go:651` (`execSingleStatement`)
  - **시나리오**: T-SQL 저장 프로시저 또는 스크립트에서 `IF ... BEGIN ... END` 블록 실행 시 엉뚱한 트랜잭션 생성 및 구문 파괴.
  - **권장 수정안**: `cs.IsTSQL()` 환경에서는 `upper == "BEGIN"`을 제외하고 `BEGIN TRAN[SACTION]`만 트랜잭션으로 인식하도록 분기.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 039] `pkg/engine/sqlexec.go` (2/2)
* **대상 영역**: 다중 쿼리 배치 분해(`SplitSQLStatements`), 세미콜론 및 `GO` 배치 분리, 세션 상태 변수
* **Gemini 1차 분석**:
  - `substituteParamOutsideQuotes`를 통해 문자열 리터럴 내부의 변수 오치환 방지.
* **Claude 2차 교차 분석**:
  - **주석 내부의 테이블 변수 오치환 위험 (`sqlexec.go:595`)**:
    `substituteParamOutsideQuotes`가 따옴표는 건너뛰지만 SQL 주석(`-- @table` 또는 `/* @table */`) 내부의 토큰까지 치환하여 쿼리 의미가 왜곡되거나 오류 발생 가능.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/engine/sqlexec.go:595` (`execSingleStatement`)
  - **시나리오**: 주석에 `@table` 변수명이 포함된 쿼리 실행 시 주석 내부 텍스트 변조.
  - **권장 수정안**: `sqlutil.ReplaceOutsideQuotesAndComments`로 토크나이저 교체.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 040] `pkg/engine/sqlexec_ddl.go` (1/3)
* **대상 영역**: `CREATE TABLE`, `DROP TABLE`, `TRUNCATE`, 복합 기본키 및 외래키 인라인 파싱
* **Gemini 1차 분석**:
  - `sqlutil.FindMatchingParen`을 사용한 Context-Free 괄호 매칭으로 제로-정규식 DDL 파싱 준수.
* **Claude 2차 교차 분석**:
  - **테이블 수준 복합 기본키 `PRIMARY KEY (col1, col2)` 정의 시 복합 인덱스 누락 (`sqlexec_ddl.go:191-206`)**:
    `PRIMARY KEY (col1, col2)`가 주어졌을 때 개별 컬럼의 `c.PrimaryKey = true` 플래그만 세팅하고, 복합 인덱스 스키마(`schema.Indexes`)나 정렬 키 정의를 스토리지 엔진에 전달하지 않아, 다중 컬럼 기본키가 단일 컬럼 PK들로 파편화되거나 첫 번째 컬럼으로만 인덱스가 걸리는 결함.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/engine/sqlexec_ddl.go:191-206` (`execCreateTable`)
  - **시나리오**: `CREATE TABLE order_items (order_id INT, item_id INT, PRIMARY KEY (order_id, item_id))` 실행 시 복합 고유키 제약조건이 제대로 등록되지 않음.
  - **권장 수정안**: 다중 컬럼 PK인 경우 `schema.Indexes`에 복합 Unique 인덱스(`pk_tableName`)를 명시적으로 등록.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 041] `pkg/engine/sqlexec_ddl.go` (2/3), `sqlexec_conditional_ddl.go`
* **대상 영역**: `ALTER TABLE` (컬럼 추가/수정/삭제, 제약조건, 이름 변경) 및 관용 조건부 DDL
* **Gemini 1차 분석**:
  - `SplitTopLevel` 콤마 분해를 통해 단일 `ALTER TABLE` 구문 내 복합 액션(Compound Actions) 파이프라인 처리 우수.
* **Claude 2차 교차 분석**:
  - **`ALTER TABLE ADD FOREIGN KEY`가 스키마에 등록되지 않고 No-Op 성공 반환하는 결함 (`sqlexec_ddl.go:1546-1561`)**:
    `ALTER TABLE <table> ADD [CONSTRAINT <name>] FOREIGN KEY (...) REFERENCES ...` 구문 실행 시, 실제 외래키 제약조건 객체를 `TableSchema.ForeignKeys`에 영속 추가하지 않고 성공 메시지만 반환하여, 이후 DML 시 외래키 무결성 검증이 누락됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/engine/sqlexec_ddl.go:1546-1561` (`execAlterTable`)
  - **시나리오**: 테이블 생성 후 `ALTER TABLE ... ADD CONSTRAINT fk_...`로 추가된 외래키가 실제 외래키 무결성 검사나 카탈로그 조회에서 누락됨.
  - **권장 수정안**: 외래키 파서(`parseForeignKeyClause`)를 호출하여 `s.engine.AlterTable`을 통해 스키마에 정식 등록.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 042] `pkg/engine/sqlexec_ddl.go` (3/3), `boundary_normalizer.go`
* **대상 영역**: `CREATE/DROP INDEX`, 관용 DDL 모드 및 식별자 경계 정규화
* **Gemini 1차 분석**:
  - `NormalizeRecordKeys`를 통해 대소문자 혼용 및 접두사 컬럼명을 정규 스키마 컬럼명으로 일치시키는 정규화 로직 양호.
* **Claude 2차 교차 분석**:
  - **`DROP INDEX <name>` 테이블 미지정 시 다중 테이블 동명 인덱스 모호성 무시 결함 (`sqlexec_ddl.go:728-734`)**:
    테이블명이 없는 `DROP INDEX idx_name` 실행 시 전체 테이블을 순회하다가 처음 발견된 테이블의 인덱스만 드롭하여, 의도치 않은 테이블의 인덱스가 삭제될 위험.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/sqlexec_ddl.go:728-734` (`execDropIndex`)
  - **시나리오**: 서로 다른 테이블에 동일한 이름의 인덱스가 있을 때 테이블을 특정하지 않고 삭제 시 첫 번째 테이블의 인덱스가 오삭제됨.
  - **권장 수정안**: 복수 테이블에서 동명 인덱스 발견 시 모호성 에러(`ambiguous index name`) 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 043] `pkg/engine/sqlexec_dml.go` (1/2), `dml_parser.go`
* **대상 영역**: `INSERT INTO`, `SELECT INTO` 실행기, `ON CONFLICT` 절 및 DML 어휘 파서
* **Gemini 1차 분석**:
  - `parseOnConflict`를 통한 PostgreSQL 스타일 `ON CONFLICT DO UPDATE/NOTHING` 구문의 정밀 분해 양호.
* **Claude 2차 교차 분석**:
  - **`ON CONFLICT` 복합 인덱스 매칭 시 `len == 1` 가드로 인한 `O(N)` 풀스캔 성능 폭락 (`sqlexec_dml.go:886-907`)**:
    복합 인덱스(`matchingIdx`)가 엄연히 존재함에도 `len(conflictCols) == 1` 분기 가드로 인해 복합 인덱스 검색을 타지 못하고, `tx.Scan(tableName)` 전체 테이블 풀스캔으로 떨어져 대량 데이터 삽입 시 심각한 I/O 및 지연시간 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/engine/sqlexec_dml.go:886-907` (`execInsertSelect`)
  - **시나리오**: 복합 유니크 제약조건이 걸린 대용량 테이블에 `ON CONFLICT` UPSERT 배치 실행 시 매 행마다 전체 테이블 풀스캔 발생.
  - **권장 수정안**: `len(conflictCols) > 1`인 경우에도 복합 인덱스 스캔 지원 또는 복합 키 룩업으로 처리.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 044] `pkg/engine/sqlexec_dml.go` (2/2)
* **대상 영역**: `UPDATE`, `DELETE` 실행기, 사설 워크스페이스 변경 및 외래키 연쇄 삭제
* **Gemini 1차 분석**:
  - DML 실행 중 `checkTableAccess`를 통한 권한 확인 및 트랜잭션 수명주기 격리 철저.
* **Claude 2차 교차 분석**:
  - **자기 참조 외래키 테이블의 일괄 UPDATE 시 순서 의존적 외래키 위반 롤백 (`sqlexec_dml.go:1120-1160`)**:
    동일 테이블 내 계층 구조(`parent_id -> id`)가 있는 테이블에서 여러 행을 일괄 갱신할 때, 부모 행이 갱신되기 전에 자식 행의 외래키 검증이 즉시 발동하여 트랜잭션이 억울하게 롤백됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/sqlexec_dml.go:1120-1160` (`execUpdate`)
  - **시나리오**: 트리/조직도 형태의 자기 참조 테이블에서 일괄 부모 ID 변경 시 외래키 제약조건 위반 발생.
  - **권장 수정안**: 문장 종료 시점(Statement-level)으로 외래키 검증 지연 또는 사설 워크스페이스 반영 후 최종 검증.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 045] `pkg/engine/sqlexec_select.go` (1/2)
* **대상 영역**: 최상위 `SELECT` 절 실행 및 프로젝션 평가, 계산식 별칭 매핑
* **Gemini 1차 분석**:
  - `splitScalarProjections`를 사용한 제로-정규식 프로젝션 분해 및 `SELECT *` 확장 안전성 양호.
* **Claude 2차 교차 분석**:
  - **동일한 별칭을 가진 다중 프로젝션 컬럼 충돌 시 값 덮어쓰기 오염 (`sqlexec_select.go:420-460`)**:
    `SELECT u.id AS code, o.code AS code FROM ...`처럼 의도적으로 동일한 별칭을 지정한 경우, 내부 맵 키 충돌로 마지막 컬럼 값이 이전 컬럼 값을 덮어써 결과 데이터셋이 오염됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/sqlexec_select.go:420-460` (`execSelect`)
  - **시나리오**: 동일 별칭을 가진 조인 쿼리 결과에서 앞선 컬럼의 데이터가 소실되고 뒷 컬럼 값으로 복제됨.
  - **권장 수정안**: 맵 기반 중복 키 충돌을 방어하기 위해 슬라이스 기반 위치(Index) 매핑 또는 고유 내부 키 도입.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 046] `pkg/engine/sqlexec_select.go` (2/2)
* **대상 영역**: T-SQL 페이징(`TOP`, `OFFSET FETCH`), `Bare IN` 리스트 평가
* **Gemini 1차 분석**:
  - `ParseTopClause`를 통한 `TOP N [PERCENT] [WITH TIES]` 문법 인식 완결성 양호.
* **Claude 2차 교차 분석**:
  - **`TOP PERCENT` 실행 시 전체 결과셋 무제한 인메모리 버퍼링으로 인한 OOM 위험 (`sqlexec_select.go:1210-1235`)**:
    `PERCENT` 계산을 위해 스트리밍 대신 전체 행 수를 세기 위해 모든 행을 힙 메모리에 버퍼링하므로 대규모 테이블에서 메모리 한도 초과 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/sqlexec_select.go:1210-1235` (`applyTopClause`)
  - **시나리오**: 수백만 건 테이블에서 `SELECT TOP 1 PERCENT *` 실행 시 쿼리 엔진 메모리 폭증.
  - **권장 수정안**: 카탈로그 통계(`reltuples`) 기반 사전 행 수 추정 또는 메모리 거버너 예산 검사 연동.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 047] `pkg/engine/sqlexec_derived.go`, `cte_lowering.go`
* **대상 영역**: 인라인 파생 테이블(`FROM (SELECT ...)`), CTE 재작성기 및 머티리얼라이제이션
* **Gemini 1차 분석**:
  - `cte_lowering.go`를 통한 재귀 및 다중 참조 CTE의 임시 테이블 변환과 비상관화 구조 체계적.
* **Claude 2차 교차 분석**:
  - **파생 테이블 생성 중 오류 발생 시 물리 임시 테이블 누수 결함 (`sqlexec_derived.go:145-180`)**:
    파생 테이블을 임시 물리 테이블(`__derived_table_*`)로 생성하다가 후속 서브쿼리나 본 쿼리에서 문법/런타임 에러가 발생했을 때, `createdTables` 클린업이 누락되어 스토리지에 고아 테이블이 영구 잔류함.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/engine/sqlexec_derived.go:145-180` (`resolveDerivedTables`)
  - **시나리오**: 중첩 파생 테이블 쿼리 실행 중 중간 실패 시 불필요한 임시 테이블이 엔진에 계속 쌓여 자원 고갈.
  - **권장 수정안**: `defer` 문을 통해 에러 반환 시 생성된 `createdTables`를 전수 `DropTable`하는 자원 보호 가드 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 048] `pkg/engine/sqlexec_merge.go`, `sqlexec_apply.go`
* **대상 영역**: `MERGE` (UPSERT) 문 실행기, `CROSS APPLY` / `OUTER APPLY` 관계 확장
* **Gemini 1차 분석**:
  - `WHEN MATCHED THEN UPDATE`, `WHEN NOT MATCHED THEN INSERT` 분기 및 삭제(`DELETE`) 조건 제어 양호.
* **Claude 2차 교차 분석**:
  - **MERGE 문 단일 타겟 행 다중 매칭(Multiple matches) 시 조용한 덮어쓰기 데이터 오염 (`sqlexec_merge.go:340-380`)**:
    소스 테이블의 여러 행이 타겟 테이블의 단일 행과 일치할 때, ANSI SQL 및 SQL Server 표준은 에러를 발생시켜야 하지만 Lionsql 엔진은 마지막 소스 행으로 조용히 덮어써서 비결정론적 데이터 부패를 초래함.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/engine/sqlexec_merge.go:340-380` (`execMerge`)
  - **시나리오**: 1:N 조인 관계의 소스 데이터로 MERGE 실행 시 에러 없이 임의의 데이터로 타겟 행이 덮어써짐.
  - **권장 수정안**: 갱신된 타겟 행의 PK를 `updatedPKs` 맵에 기록하고, 중복 매칭 감지 시 `ErrMergeMultipleMatches` 에러 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 049] `pkg/engine/sqlexec_cursor.go`, `sqlexec_prepare.go`
* **대상 영역**: FORWARD-ONLY 세션 커서 및 `PREPARE` / `EXECUTE` 실행기
* **Gemini 1차 분석**:
  - 커서의 `FETCH NEXT` 시 세션 워크스페이스 격리 및 `@@FETCH_STATUS` 상태값 제어 우수.
* **Claude 2차 교차 분석**:
  - **`CLOSE <cursor>` 시 맵에서 즉시 삭제하여 커서 재오픈(Re-OPEN) 불가 결함 (`sqlexec_cursor.go:210-225`)**:
    T-SQL 규격상 `CLOSE`는 커서를 닫기만 하고 구조는 유지되어 다시 `OPEN`할 수 있어야 하며, 오직 `DEALLOCATE` 시에만 맵에서 삭제되어야 함. 현재 코드는 `CLOSE` 시점에 세션 맵에서 삭제하여 재오픈 시 "cursor not found" 에러 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/sqlexec_cursor.go:210-225` (`execCloseCursor`)
  - **시나리오**: 루프 내에서 커서를 CLOSE 후 재OPEN하는 T-SQL 스크립트 실행 실패.
  - **권장 수정안**: `SessionCursor`에 `IsOpen` 플래그를 두고 `CLOSE` 시에는 `IsOpen = false`만 처리하고, `DEALLOCATE` 시에만 맵에서 삭제.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 050] `pkg/engine/sqlexec_procs.go`, `proc_bind.go`, `sqlexec_auth.go`
* **대상 영역**: `CREATE PROCEDURE`, `EXEC` 프로시저 실행기, 파라미터 역바인딩 및 인라인 권한
* **Gemini 1차 분석**:
  - `proc_bind.go`를 통한 `@param` 매핑 및 VDBE 가상머신 파라미터 변환 체계 우수.
* **Claude 2차 교차 분석**:
  - **프로시저 `OUTPUT` 파라미터 반환 시 타입 불일치 시 문자열 변질 대입 (`proc_bind.go:85-110`)**:
    호출자 세션 변수의 원래 타입과 프로시저 내부에서 반환된 `OUTPUT` 파라미터의 타입이 다를 때, 적절한 강제 형변환(Coerce) 없이 무조건 `Interface()` 문자열로 변질 대입되어 후속 수식 연산 패닉 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/proc_bind.go:85-110` (`BindOutputParams`)
  - **시나리오**: 정수 세션 변수에 OUTPUT 파라미터로 소수점 값 반환 후 산술 연산 시 런타임 타입 에러 발생.
  - **권장 수정안**: `storage.CoerceValue`를 호출하여 호출자 변수 타입에 맞게 안전 변환 후 대입.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 051] `pkg/engine/sqlexec_eval.go`, `evaluator.go`, `sql_row.go`
* **대상 영역**: 행 단위 조건식 평가기 및 SQL Row 직렬화/역직렬화
* **Gemini 1차 분석**:
  - `sql_row.go`를 통한 컬럼 바운드 체크 및 메모리 효율적 레코드 버퍼링 체계 양호.
* **Claude 2차 교차 분석**:
  - **수식 평가 시 0 나누기 발생 시 SQL Server 기본 규격 에러 대신 NULL 침묵 반환 (`evaluator.go:75-92`)**:
    산술식 평가 도중 분모가 0일 때 `SET ARITHABORT ON` 표준 에러를 전파하지 않고 조용히 NULL을 반환하여 계산 오류가 은폐됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/evaluator.go:75-92` (`evalBinaryArithmetic`)
  - **시나리오**: 수식 계산 쿼리에서 0 나누기 발생 시 에러 없이 NULL 값으로 후속 집계에 합산되어 계산 왜곡 발생.
  - **권장 수정안**: 분모 0 검사 시 세션의 ARITHABORT 설정에 따라 명시적 `divide by zero` 에러 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 052] `pkg/engine/sqlexec_pivot.go`, `sqlexec_string_split.go`, `sqlexec_explain.go`
* **대상 영역**: `PIVOT`, `UNPIVOT`, `STRING_SPLIT` 및 `EXPLAIN` 플랜 서술
* **Gemini 1차 분석**:
  - `STRING_SPLIT`의 `enable_ordinal` 1-기반 인덱스 컬럼 반환 및 T-SQL 호환성 우수.
* **Claude 2차 교차 분석**:
  - **`STRING_SPLIT` 빈 구분자 전달 시 0바이트 슬라이스 패닉 또는 무한 루프 위험 (`sqlexec_string_split.go:88-105`)**:
    구분자 문자열이 빈 문자열(`""`)로 입력되었을 때 길이 0에 대한 사전 방어 검사가 누락되어 분할 루프 패닉 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/sqlexec_string_split.go:88-105` (`execStringSplit`)
  - **시나리오**: 동적 SQL 또는 프로시저에서 빈 문자열 구분자로 STRING_SPLIT 실행 시 엔진 패닉.
  - **권장 수정안**: `separator == ""` 검사를 추가하여 즉시 구문 에러(`separator cannot be empty`) 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 053] `pkg/engine/sqlexec_jdbc_metadata.go`, `sqlexec_virtual.go`, `sqlexec_dbcc.go`
* **대상 영역**: JDBC 메타데이터 변환, 가상 프로시저 및 `DBCC` 실행기
* **Gemini 1차 분석**:
  - `DBCC CHECKIDENT`의 현재 IDENTITY 시퀀스 값 검증 및 재설정 로직 양호.
* **Claude 2차 교차 분석**:
  - **`DBCC CHECKIDENT` 존재하지 않는 테이블 지정 시 일반 메시지로 성공 처리되는 결함 (`sqlexec_dbcc.go:310-335`)**:
    대상 테이블이 없을 때 `storage.ErrTableNotFound` 에러를 반환하지 않고 단순 안내 문자열을 성공 결과셋으로 반환하여 호출자가 오류를 인지하지 못함.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/sqlexec_dbcc.go:310-335` (`execDBCCCheckIdent`)
  - **시나리오**: 오타가 난 테이블명으로 DBCC CHECKIDENT 실행 시 에러 없이 성공으로 처리되어 ID 리셋 누락.
  - **권장 수정안**: `s.engine.GetTable` 실패 시 명시적 SQL 에러 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 054] `pkg/engine/session.go`, `session_pool.go`, `session_registry.go`
* **대상 영역**: 클라이언트 세션 수명주기, 세션 풀러 및 레지스트리
* **Gemini 1차 분석**:
  - 세션 풀 획득/반환 시 RWMutex 동기화 및 유휴 세션 타임아웃 정리 체계 우수.
* **Claude 2차 교차 분석**:
  - **세션 풀 고갈 시 대기 채널에서 컨텍스트 타임아웃 발생 시 큐 고아화 누수 (`session_pool.go:120-145`)**:
    `AcquireSession` 대기 도중 클라이언트 타임아웃으로 이탈했을 때 대기 슬롯 채널이 대기자 목록에 잔류하여 후속 세션 반환자가 고아 채널에 세션을 송신하고 자원이 영구 유실됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/engine/session_pool.go:120-145` (`AcquireSession`)
  - **시나리오**: 풀 고갈 상태에서 클라이언트 단선 시 사용 가능한 세션이 고아 채널로 흡수되어 세션 풀이 영구적으로 줄어듦.
  - **권장 수정안**: `select` 문에서 컨텍스트 취소 감지 시 대기 큐에서 즉시 해당 채널을 언링크(Unlink)하는 방어 로직 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 055] `pkg/engine/auto_analyze.go`, `pk_bypass.go`, `general_log.go`, `slow_query.go` 등
* **대상 영역**: 백그라운드 통계 수집기, PK 고속 바이패스 및 쿼리 로거
* **Gemini 1차 분석**:
  - `pk_bypass.go`의 단일 PK 조회 플랜 캐싱을 통한 Volcano 엔진 바이패스 초고속 경로 우수.
* **Claude 2차 교차 분석**:
  - **`auto_analyze.go`의 WAL 훅 레코드 수집 채널 언바운디드 버퍼링 부하 (`auto_analyze.go:65-80`)**:
    고부하 트랜잭션 유입 시 WAL 레코드 분석 큐가 바운드 제한 없이 커져 메모리 누수를 유발하고 백그라운드 워커가 CPU를 과도하게 점유.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/engine/auto_analyze.go:65-80` (`OnWALRecord`)
  - **시나리오**: 초당 수만 건의 쓰기 트랜잭션 유입 시 자동 분석 큐 메모리 급증.
  - **권장 수정안**: 채널 용량 상한 설정(`[RULE:BoundedBuffers]`) 및 `select/default` 비차단 드롭(`[RULE:NonBlockingSink]`) 적용.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 056] `pkg/proc/lexer.go`, `token.go`, `literal.go`
* **대상 영역**: T-SQL Fast-Path 어휘 분석기(Lexer) 및 토큰 정의
* **Gemini 1차 분석**:
  - 제로-할당 바이트 스캐닝 및 T-SQL 예약어 정적 테이블 룩업 구조 견고.
* **Claude 2차 교차 분석**:
  - **닫히지 않은 대괄호 식별자(`[dbo.[table`) 스캔 시 오프셋 음수 오염 (`lexer.go:210-230`)**:
    EOF 도달 시 탈출 로직은 정상이나 토큰의 컬럼/라인 오프셋 계산에서 음수가 발생하여 진단 에러 메시지 서식 깨짐 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/proc/lexer.go:210-230` (`scanBracketIdent`)
  - **시나리오**: 문법 오류 진단 시 에러 컬럼 위치가 음수로 출력됨.
  - **권장 수정안**: 오프셋 계산에 `max(0, offset)` 바운드 가드 적용.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 057] `pkg/proc/parser.go`, `parser_expr.go`
* **대상 영역**: Pratt 파서 표현식 파싱 및 연산자 결합 법칙
* **Gemini 1차 분석**:
  - 연산자 결합 우선순위(Binding Power) 기반 Pratt 파싱으로 복잡한 T-SQL 수식 파싱 효율 우수.
* **Claude 2차 교차 분석**:
  - **재귀 수식 파싱 시 스택 깊이 제한 부재로 스택 오버플로우 패닉 위험 (`parser_expr.go:140-180`)**:
    깊게 중첩된 괄호나 복합 수식 파싱 시 재귀 호출 깊이를 제한하는 가드(`[RULE:BoundedRecursion]`)가 없어 악의적인 대량 괄호 입력 시 Go 런타임 스택 오버플로우 크래시 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/parser_expr.go:140-180` (`parseExprBP`)
  - **시나리오**: 수천 개가 중첩된 `((((1))))` 쿼리 파싱 시 프로세스 비정상 종료.
  - **권장 수정안**: `depth` 카운터를 추적하여 128 초과 시 `ErrRecursionDepthExceeded` 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 058] `pkg/proc/parser_stmt.go`
* **대상 영역**: T-SQL 제어문 파싱 (`IF-ELSE`, `WHILE`, `TRY-CATCH`)
* **Gemini 1차 분석**:
  - `TRY...CATCH` 블록 및 제어 흐름 상태 전이 체계 양호.
* **Claude 2차 교차 분석**:
  - **세미콜론 없는 단일 문장 `IF ... ELSE`에서 블록 경계 오인식 결함 (`parser_stmt.go:315-360`)**:
    세미콜론이 생략된 단일 실행문 뒤에 `ELSE`가 올 때, 파서가 문장 종결을 감지하지 못하고 `ELSE`를 상위 블록의 키워드로 오인식하여 컴파일 실패 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/parser_stmt.go:315-360` (`parseIfStmt`)
  - **시나리오**: T-SQL 표준 관용 패턴인 `IF @x = 1 PRINT 'A' ELSE PRINT 'B'` 실행 시 구문 분석 에러 발생.
  - **권장 수정안**: 단일 문장 종료 시 토큰이 `ELSE`인 경우 즉시 현재 IF 노드에 바인딩하도록 토큰 룩어헤드 보정.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 059] `pkg/proc/parser_ddl.go`, `parser_dml.go`
* **대상 영역**: 프로시저 내 인라인 DDL/DML 파서
* **Gemini 1차 분석**:
  - 프로시저 본문 내의 `INSERT`, `UPDATE`, `DELETE`, `MERGE` 문법 AST 변환 정합성 양호.
* **Claude 2차 교차 분석**:
  - **`UPDATE ... FROM ... JOIN` 구문에서 타겟 테이블 별칭 해석 오류 (`parser_dml.go:520-565`)**:
    T-SQL 특유의 `UPDATE t SET t.val = 1 FROM table1 AS t JOIN table2 AS j ON ...` 패턴에서 `FROM` 절 별칭과 `UPDATE` 타겟 테이블의 연결이 누락되어 컴파일 에러 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/proc/parser_dml.go:520-565` (`parseUpdateStmt`)
  - **시나리오**: ERP 및 복잡한 비즈니스 프로시저에서 흔히 쓰이는 조인 기반 UPDATE 프로시저 컴파일 실패.
  - **권장 수정안**: FROM 절의 별칭 테이블을 UPDATE 타겟 스키마로 해석하는 심볼 테이블 룩업 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 060] `pkg/proc/ast.go`, `characteristics.go`, `strict.go`
* **대상 영역**: AST 노드 정의, 프로시저 특성 판별 및 엄격 모드 검사
* **Gemini 1차 분석**:
  - 불변 AST 노드 구조 및 프로시저 읽기 전용/부작용 검사기 구현 양호.
* **Claude 2차 교차 분석**:
  - **서브쿼리 내 비결정론적 함수(`NEWID()`, `GETDATE()`) 누락 오판별 (`characteristics.go:95-120`)**:
    프로시저 내의 서브쿼리 수식에 포함된 `NEWID()` 등의 비결정론적 함수가 최상위 특성 검사에서 누락되어, 캐시되면 안 되는 쿼리가 순수 함수로 오판별되어 잘못 캐시될 위험.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/proc/characteristics.go:95-120` (`IsDeterministic`)
  - **시나리오**: `NEWID()`를 사용하는 프로시저가 결정론적 함수로 판정되어 동일한 GUID가 반복 캐시 반환됨.
  - **권장 수정안**: AST 서브트리 재귀 순회를 통해 모든 중첩 표현식의 비결정론적 함수를 전수 검출.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 061] `pkg/proc/compiler.go`, `bytecode.go` (1/2)
* **대상 영역**: T-SQL AST ➔ VDBE 바이트코드 컴파일러 및 Opcode 정의
* **Gemini 1차 분석**:
  - VDBE 레지스터 기반 바이트코드 명령어 셋 및 주소 점프 백패칭(Backpatching) 체계 우수.
* **Claude 2차 교차 분석**:
  - **대형 프로시저 컴파일 시 점프 목적지 오프셋 정수 오버플로우 검증 부재 (`compiler.go:420-460`)**:
    수천 라인의 거대 프로시저에서 점프 오프셋이 바이트코드 슬라이스 범위를 벗어날 때 컴파일 타임 검증 없이 음수 또는 엉뚱한 주소로 백패칭되어 런타임에 무한 루프 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/compiler.go:420-460` (`backpatch`)
  - **시나리오**: 대형 ERP 배치 프로시저 실행 시 VDBE 가상머신이 엉뚱한 이전 PC로 역점프하여 서버 프리징.
  - **권장 수정안**: `0 <= targetPC < len(prog.Instructions)` 바운드 검사를 강화하여 오프셋 초과 시 컴파일 오류 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 062] `pkg/proc/compiler.go`, `bytecode.go` (2/2)
* **대상 영역**: 컴파일러 최적화 패스 및 레지스터 수명주기 관리
* **Gemini 1차 분석**:
  - 레지스터 재활용 풀링 및 상수 폴딩 최적화 패스 설계 우수.
* **Claude 2차 교차 분석**:
  - **중첩 함수 인자 평가 시 레지스터 조기 회수로 인한 인자 값 덮어쓰기 오염 (`compiler.go:880-925`)**:
    `f(a, g(b))` 형태의 중첩 함수 호출 컴파일 시, `a`를 담고 있던 레지스터가 `g(b)` 평가 도중 조기 해제되어 재할당되면서 `a`의 원래 값이 덮어써져 손실됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/compiler.go:880-925` (`compileFunctionCall`)
  - **시나리오**: 다중 인자 함수 호출 시 첫 번째 인자의 계산 결과가 두 번째 인자 계산 도중 파괴되어 엉뚱한 파라미터 전달.
  - **권장 수정안**: 모든 실인자의 컴파일이 완료될 때까지 인자 레지스터 락(Pin) 유지.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 063] `pkg/proc/parser/antlr/wrapper.go`, `ast_builder.go`
* **대상 영역**: Tier 2 ANTLR4 공식 문법 파서 래퍼 및 AST 변환기
* **Gemini 1차 분석**:
  - 공식 Microsoft ANTLR4 문법 트리를 네이티브 VDBE AST로 1:1 변환하는 어댑터 아키텍처 우수.
* **Claude 2차 교차 분석**:
  - **`IIF` 삼항 연산자 및 `TRY_CONVERT` 구문 변환 누락으로 nil 노드 패닉 (`ast_builder.go:340-375`)**:
    ANTLR4 파서는 성공적으로 파싱했으나 `ast_builder.go`의 Visitor에서 해당 표현식의 매핑이 구현되지 않아 nil AST 노드를 반환하고 후속 컴파일러에서 nil 역참조 패닉 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/parser/antlr/ast_builder.go:340-375` (`VisitExpression`)
  - **시나리오**: Fast Pratt 파서가 처리하지 못해 ANTLR 폴백으로 넘어간 `IIF(...)` 표현식 프로시저 컴파일 시 서버 패닉.
  - **권장 수정안**: `IIF`를 `CASE WHEN ... THEN ... ELSE ... END` AST 노드로 정규 변환하는 방문자 메서드 구현.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 064] `pkg/proc/parser/antlr/tsql_lexer.go`, `tsql_parser.go` 등
* **대상 영역**: 공식 Microsoft T-SQL ANTLR4 렉서/파서 런타임 및 리스너/비지터
* **Gemini 1차 분석**:
  - CGo 없는 순수 Go ANTLR4 런타임 임베딩으로 완벽한 플랫폼 이식성 확보.
* **Claude 2차 교차 분석**:
  - **ANTLR4 파서 인스턴스 반복 생성 시 DFA 캐시 메모리 누수 (`wrapper.go:70-95`)**:
    폴백 파서 호출 시마다 ANTLR 어휘/문법 인터프리터를 새로 생성하면서 DFA 결정 상태 캐시가 전역 해제되지 않아 장기 실행 시 메모리 누적 누수 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/proc/parser/antlr/wrapper.go:70-95` (`ParseProcedureFallback`)
  - **시나리오**: 수만 회의 비표준 T-SQL 프로시저 파싱 시 프로세스 RSS 메모리 지속 증가.
  - **권장 수정안**: `sync.Pool`을 통한 ANTLR 파서/렉서 인스턴스 재사용 및 호출 후 DFA 상태 초기화.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 065] `pkg/proc/cache.go`, `fold.go`
* **대상 영역**: 프로시저 바이트코드 계획 캐시 및 상수 폴딩(Constant Folding)
* **Gemini 1차 분석**:
  - LRU 기반 바이트코드 캐시 및 쿼리 지문(Fingerprint) 해시 체계 우수.
* **Claude 2차 교차 분석**:
  - **컴파일 타임 상수 폴딩 시 `1 / 0` 정수 나눗셈 직접 실행으로 인한 컴파일러 패닉 (`fold.go:145-170`)**:
    `fold.go`에서 상수 산술식을 최적화하는 도중 분모가 0인 리터럴 식이 들어왔을 때, 분모 0 사전 검사 없이 직접 정수 나눗셈(`/`)을 수행하여 컴파일러가 패닉으로 크래시됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/fold.go:145-170` (`foldBinaryArithmetic`)
  - **시나리오**: `SELECT 1/0` 상수가 포함된 프로시저 컴파일 시 DB 서버 프로세스 즉각 크래시.
  - **권장 수정안**: 분모 0 사전 검사를 수행하고 상수 폴딩을 중단하여 런타임 에러로 안전하게 이관.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 066] `pkg/proc/runtime/vm.go` (1/2), `vm_dispatcher.go`
* **대상 영역**: VDBE 바이트코드 가상머신 인터프리터 코어 루프 및 PC 점프 제어
* **Gemini 1차 분석**:
  - 단일 평탄화 직접 디스패치 루프(`PERF-VDBE-FLATTEN-1`)로 CPU 레지스터 기반 초고속 PC 제어 우수.
* **Claude 2차 교차 분석**:
  - **음수 점프 목적지 가드 부재로 인한 음수 슬라이스 인덱스 패닉 (`vm_dispatcher.go:79-160`)**:
    `OpJump`, `OpJumpIfFalse`, `OpJumpIfEqInt` 등에서 `pc = int(inst.B)` 또는 `int(inst.C)` 점프 시 음수 목적지 검사가 전혀 없어 `instructions[pc]` 호출 시 언바운디드 음수 슬라이스 패닉 발생 (`[RULE:DefensiveIndexing]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/runtime/vm_dispatcher.go:79-160` (`Execute`)
  - **시나리오**: 손상된 바이트코드 또는 음수 오프셋 점프 시 즉각 런타임 패닉.
  - **권장 수정안**: 점프 목적지 할당 전 `0 <= targetPC < len(instructions)` 사전 경계 검사 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 067] `pkg/proc/runtime/vm.go` (2/2)
* **대상 영역**: VDBE 레지스터 읽기/쓰기, 스택 프레임 풀링, CALL/RETURN 스택
* **Gemini 1차 분석**:
  - `sync.Pool` 기반 `VMFrame` 재활용 및 슬롯 캐싱으로 할당 제로화 달성.
* **Claude 2차 교차 분석**:
  - **중첩 프로시저 호출 시 예외/에러 발생 시 프레임 해제 누락 및 UDF 재귀 깊이 미전파 (`vm.go:2200-2270`)**:
    `callProc`에서 `subFrame.Execute` 실행 중 에러 또는 패닉 발생 시 `releaseFrame`이 누락되어 프레임 누수 발생 (`[RULE:DoubleCloseSafety]`), 또한 `callUDF`에서 `RunInTx` 호출 시 `callDepth`가 전달되지 않아 상호 재귀 UDF 호출 시 스택 오버플로우 패닉 위험 (`[RULE:BoundedRecursion]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/runtime/vm.go:2200-2270` (`callProc`, `callUDF`)
  - **시나리오**: 중첩 프로시저 실행 실패 시 메모리 누수 및 재귀 UDF 무한 호출 시 프로세스 스택 고갈.
  - **권장 수정안**: `defer f.executor.releaseFrame(subFrame)` 적용 및 UDF 실행 시 프레임 `callDepth` 동기화.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 068] `pkg/proc/runtime/vm_dispatcher.go`
* **대상 영역**: Opcode 디스패치 테이블 및 트랜잭션/상태머신 전이
* **Gemini 1차 분석**:
  - 고속 switch-case 구조를 통한 인스트럭션 디스패치 최적화.
* **Claude 2차 교차 분석**:
  - **중첩 트랜잭션 내 `COMMIT`/`ROLLBACK` 시 실제 스토리지 트랜잭션 조기 커밋 및 세션 새 트랜잭션 오발행 (`vm_dispatcher.go:535-570`)**:
    T-SQL 명세상 `f.tranCount > 1`인 중첩 트랜잭션의 `COMMIT`은 카운터만 감소시키고 최종 `f.tranCount == 0`일 때만 스토리지 커밋을 수행해야 함에도, 즉시 `f.tx.Commit()`을 실행하고 새 트랜잭션을 열어버려 외부 트랜잭션 원자성 파괴.
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical`
  - **위치**: `pkg/proc/runtime/vm_dispatcher.go:535-570` (`OpTxCommit`, `OpTxRollback`)
  - **시나리오**: 중첩 프로시저 내에서 내부 `COMMIT` 수행 시 바깥쪽 트랜잭션 작업이 롤백 불가능하게 조기 커밋됨.
  - **권장 수정안**: `f.tranCount` 감소 후 카운터가 0이 될 때만 `f.tx.Commit()`을 트리거하도록 수정.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 069] `pkg/proc/runtime/bridge.go`, `vm_lazy.go`
* **대상 영역**: 스토리지 트랜잭션 브릿지 및 지연 평가(Lazy Decoding)
* **Gemini 1차 분석**:
  - 로우 바이트 디코딩 지연 평가로 대용량 쿼리 필터링 성능 극대화.
* **Claude 2차 교차 분석**:
  - **`canSkipRawTuple`에서 튜플 디코딩 에러 발생 시 사일런트 튜플 누락 (`vm_lazy.go:42-55`)**:
    `ExtractColumnFromTuple`에서 에러 발생 시 `return true`로 간주하여 데이터 손상이나 직렬화 불일치 튜플을 조용히 건너뛰어 쿼리 결과에서 누락시킴 (`[RULE:ExplicitError]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/runtime/vm_lazy.go:42-55` (`canSkipRawTuple`)
  - **시나리오**: 손상되거나 버전이 다른 튜플 스캔 시 사용자에게 에러 보고 없이 조용히 결과에서 제외됨.
  - **권장 수정안**: 디코딩 에러 발생 시 스킵하지 않고 에러를 상위로 전파하거나 디코딩 폴백 처리.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 070] `pkg/proc/builtins.go`, `builtins_string.go`
* **대상 영역**: 문자열 내장 함수 (`SUBSTRING`, `CHARINDEX`, `LEN`, `REPLACE` 등)
* **Gemini 1차 분석**:
  - Go rune 기반 슬라이싱으로 유니코드 한글 및 다중 바이트 문자열 안정성 우수.
* **Claude 2차 교차 분석**:
  - **`SUBSTRING` 음수 시작 위치 인자 시 T-SQL 표준 길이 보정 누락 (`builtins_string.go:400-425`)**:
    T-SQL `SUBSTRING(s, start, len)`에서 `start < 1`일 때 `start + len - 1` 범위에 해당하는 부분만 반환해야 하나, 단순히 `start = 0`으로 클램핑하고 전체 `len`을 취해 전혀 엉뚱한 문자열 반환.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/proc/builtins_string.go:400-425` (`evalStringBuiltin`)
  - **시나리오**: `SUBSTRING('12345', -2, 5)` 실행 시 기대값 `'12'` 대신 `'12345'` 전체 반환.
  - **권장 수정안**: `start < 1`일 때 유효 시작 지점 및 길이 보정 수식 표준화 (`len = len + start - 1`, `start = 1`).
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 071] `pkg/proc/builtins_math.go`, `builtins_date.go`
* **대상 영역**: 수학 및 날짜/시간 내장 함수 (`DATEADD`, `DATEDIFF`, `ROUND`, `MOD` 등)
* **Gemini 1차 분석**:
  - 다양한 날짜 포맷 자동 파싱 및 고속 수학 함수 테이블 구현.
* **Claude 2차 교차 분석**:
  - **`MOD`의 `MinInt64 % -1` 오버플로우 패닉 및 `DATEDIFF(day)` 자정 경계 오계산 (`builtins_math.go:45-65`, `builtins_date.go:120-145`)**:
    `MOD`에서 `MinInt64 % -1` 시 CPU 정수 나눗셈 오버플로우 패닉 발생. 또한 `DATEDIFF(day, t1, t2)`에서 자정(midnight) 경계 통과 횟수 대신 24시간 나눗셈(`diff.Hours() / 24`)을 사용하여 23:59:59와 00:00:01 간 1일 차이가 0일로 오계산됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/builtins_math.go:45-65`, `pkg/proc/builtins_date.go:120-145`
  - **시나리오**: 극단 정수 `MOD` 실행 시 패닉, 날짜 간 날짜 차이 계산 시 자정을 넘겼음에도 0 반환.
  - **권장 수정안**: `MinInt64 % -1` 사전 가드 추가, `DATEDIFF` 시 연/월/일 경계 통과 기반 계산으로 전면 개선.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 072] `pkg/proc/builtins_json.go`
* **대상 영역**: T-SQL JSON 내장 함수 (`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`)
* **Gemini 1차 분석**:
  - 표준 라이브러리 `encoding/json` 기반의 일관된 JSON 조작 지원.
* **Claude 2차 교차 분석**:
  - **`JSON_MODIFY` 경로 파싱 시 배열 인덱싱 미지원 및 파싱 오류 시 사일런트 무시 (`builtins_json.go:20-60`)**:
    `strings.Split(path, ".")`만 수행하여 `$.items[0]` 형태의 배열 인덱스 경로를 처리하지 못하고, 에러 발생 시 원본 JSON을 그대로 반환하여 실패를 은폐함 (`[RULE:ExplicitError]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/proc/builtins_json.go:20-60` (`modifyJSONByPath`)
  - **시나리오**: JSON 배열 원소 수정 쿼리가 아무런 변경도 없이 조용히 성공으로 처리됨.
  - **권장 수정안**: JSONPath 토크나이저에 `[idx]` 배열 접근자 지원 추가 및 문법 오류 시 명시적 에러 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 073] `pkg/proc/builtins_sys.go`
* **대상 영역**: 시스템 속성 및 메타데이터 함수 (`SERVERPROPERTY`, `OBJECT_ID` 등)
* **Gemini 1차 분석**:
  - UUIDv7 및 UUIDv4 고속 생성기 내장으로 인덱스 친화적 키 생성 완벽 지원.
* **Claude 2차 교차 분석**:
  - **`SERVERPROPERTY` 알 수 없는 속성 조회 시 `NULL` 대신 빈 문자열 반환 (`builtins_sys.go:300-330`)**:
    Microsoft SQL Server 표준은 알 수 없는 속성에 대해 `NULL`을 반환해야 하나 `storage.StringValue("")`를 반환하여 `SERVERPROPERTY(...) IS NULL` 검사를 수행하는 클라이언트/드라이버 오동작 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/proc/builtins_sys.go:300-330` (`evalSysBuiltin`)
  - **시나리오**: 미지원 서버 속성 쿼리 시 `IS NULL` 조건 분기가 실패하여 비정상 분기로 진입.
  - **권장 수정안**: 알 수 없는 속성의 `default` 반환값을 `storage.NullValue(storage.TypeVarChar)`로 수정.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 074] `pkg/proc/evaluator.go`, `eval.go`
* **대상 영역**: 런타임 표현식 인터프리터 및 연산자 타입 강제 변환
* **Gemini 1차 분석**:
  - SQL 3VL(Three-Valued Logic) AND/OR 평가 및 CAST/TRY_CAST 지원.
* **Claude 2차 교차 분석**:
  - **산술 연산 시 64비트 정밀도 손실 및 비숫자 문자열 덧셈 시 타입 우선순위 위반 (`eval.go:680-710`)**:
    `TokenPlus` 등에서 정수를 무조건 `float64`로 변환하여 53비트 초과 큰 정수에서 하위 비트가 잘려나가는 정밀도 오염 발생, 또한 `'abc' + 1` 등 숫자-문자열 연산 시 T-SQL 타입 우선순위(정수 우선)에 따른 변환 오류 대신 `fmt.Sprintf` 문자열 결합(`'abc1'`) 수행.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/eval.go:680-710` (`evalRowBinaryOp`)
  - **시나리오**: 큰 ID/금액 덧셈 시 값 왜곡 발생, 유효하지 않은 문자열-정수 덧셈에서 에러 대신 문자열 결합 반환.
  - **권장 수정안**: 양쪽이 정수일 경우 정수 산술 직접 수행, T-SQL 타입 우선순위 테이블에 따른 엄격한 암시적 형변환 적용.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 075] `pkg/proc/runtime/vm.go` (sp_executesql & 취소 연동), `pkg/engine/sqlexec_procs.go`
* **대상 영역**: 동적 SQL 실행기(`sp_executesql`) 및 쿼리 취소 연동
* **Gemini 1차 분석**:
  - 동적 SQL 스크립트의 온더플라이 바이트코드 컴파일 및 계획 캐싱 연동.
* **Claude 2차 교차 분석**:
  - **`callSpExecuteSql` 실행 시 부모 취소 컨텍스트(`f.ctx`) 미전파로 취소 불능 (`vm.go:2280-2330`)**:
    동적 SQL 실행 시 `ExecuteInTx`에 취소 컨텍스트를 전달하지 않아 클라이언트의 쿼리 취소(Cancel Request)가 `sp_executesql` 내부 롱러닝 쿼리에 전달되지 않고 무기한 블로킹 지속 (`[RULE:CooperativeAbort]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/proc/runtime/vm.go:2280-2330` (`callSpExecuteSql`)
  - **시나리오**: `sp_executesql` 내부에서 장시간 실행 중인 쿼리를 클라이언트가 취소해도 멈추지 않음.
  - **권장 수정안**: `ExecuteInTxWithSessionContext`를 호출하여 부모의 `f.ctx`를 자식 프레임에 전파.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 076] `pkg/planner/planner.go` (1/2)
* **대상 영역**: CBO 논리 플랜 생성기 및 관계대수 트리 변환
* **Gemini 1차 분석**:
  - 관계대수 연산자 트리 구조화 및 AST -> CBO 변환 체계 구축.
* **Claude 2차 교차 분석**:
  - **파생 테이블(Derived Table) 변환 시 외부 별칭 충돌로 인한 카테시안 곱 오폭발 (`planner.go:750-820`)**:
    중첩 서브쿼리나 파생 테이블을 결합할 때 내부와 외부의 별칭이 중복되거나 미해결될 경우 조인 조건이 누락되어 의도치 않은 `CROSS JOIN`(카테시안 곱)으로 변환되어 메모리/연산 폭증.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/planner/planner.go:750-820` (`parseFromAndJoins`)
  - **시나리오**: 파생 테이블 조인 쿼리 실행 시 조인 조건이 탈락하여 전체 행 곱셈 발생.
  - **권장 수정안**: 파생 테이블 스코프별 고유 별칭 정규화 및 조인 조건 매핑 검증 강화.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 077] `pkg/planner/planner.go` (2/2)
* **대상 영역**: CBO 비용 모델링 (CPU/IO Cost 평가 및 최적 조인 순서)
* **Gemini 1차 분석**:
  - DP(동적 계획법) 기반 Selinger 스타일의 최적 조인 순서 탐색 완비.
* **Claude 2차 교차 분석**:
  - **동일 비용 조인 순서 간 타이브레이커(Tie-breaker) 부재로 인한 비결정론적 플랜 플리핑 (`planner.go:2640-2710`)**:
    `joinedNode.TotalCost < dp[mask].totalCost` 비교 시 비용이 동일한 경우 Go map 순회 순서에 따라 매 실행마다 실행 계획 순서가 뒤바뀌어 성능 불안정 유발 (`[RULE:DeterministicTesting]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/planner/planner.go:2640-2710` (`optimizeJoinOrderDP`)
  - **시나리오**: 동일 쿼리 반복 실행 시 실행 계획이 임의로 교체되어 캐시 비효율 초래.
  - **권장 수정안**: 비용 동률 시 테이블 사전순/인덱스 유무 기반의 결정론적 타이브레이커 규칙 도입.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 078] `pkg/planner/decorrelate.go`
* **대상 영역**: 상관 서브쿼리 비상관화(Decorrelation) 규칙
* **Gemini 1차 분석**:
  - `EXISTS`/`IN` 서브쿼리를 `SEMI`/`ANTI` 조인으로 변환하는 서브쿼리 최적화 완비.
* **Claude 2차 교차 분석**:
  - **`NOT IN` 서브쿼리의 `ANTI JOIN` 변환 시 NULL 시맨틱 위반으로 인한 잘못된 결과 반환 (`decorrelate.go:130-160`)**:
    SQL 3VL 규칙상 `x NOT IN (SELECT y ...)`에서 `y`에 NULL이 하나라도 있으면 결과가 공집합이어야 하나, 단순 `ANTI JOIN` 변환으로 인해 서브쿼리에 NULL이 존재해도 외부 행이 반환되는 치명적 데이터 왜곡 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/planner/decorrelate.go:130-160` (`decorrelateIn`)
  - **시나리오**: NULL을 포함하는 서브쿼리에 대해 `NOT IN` 실행 시 SQL 표준과 상반된 결과 도출.
  - **권장 수정안**: `NOT IN` 비상관화 시 서브쿼리 조건에 `IS NOT NULL` 검증 필터 강제 주입.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 079] `pkg/planner/pushdown.go`
* **대상 영역**: 조건절 푸시다운(Predicate Pushdown) 및 인덱스 선택기
* **Gemini 1차 분석**:
  - 단일/복합 테이블 조건 분기 및 정적 모순(Static Contradiction) 사전 감지 탁월.
* **Claude 2차 교차 분석**:
  - **함수로 감싸진 컬럼 식별 시 컬럼 참조 추출 누락 및 푸시다운 실패 (`pushdown.go:60-95`)**:
    `ExtractColumnRefs`에서 식별자 뒤에 `(`가 오면 단순 함수 호출로 간주하여 인자 내부의 컬럼 참조(예: `UPPER(u.name)`)를 추출하지 못해 푸시다운 대상에서 탈락하고 풀 스캔으로 격하됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/planner/pushdown.go:60-95` (`ExtractColumnRefs`)
  - **시나리오**: 함수 기반 조건절(`UPPER(name) = 'A'`)이 조인 테이블로 푸시다운되지 못해 조인 후 필터링으로 성능 저하.
  - **권장 수정안**: 괄호 내부 토큰 재귀 탐색을 통해 함수 인자 내부의 컬럼 식별자도 정상 추출하도록 개선.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 080] `pkg/planner/bounds.go`, `pruning.go`
* **대상 영역**: 인덱스 검색 범위(Range Bounds) 빌더 및 파티션 가지치기
* **Gemini 1차 분석**:
  - BETWEEN, IN, 부등호 표현식에 대한 다차원 파티션 프루닝 체계 구축.
* **Claude 2차 교차 분석**:
  - **Yoda 조건(`'val' <= col`) 미인식으로 파티션 프루닝 누락 및 RFC3339 날짜 상한 오차 (`pruning.go:45-90`, `130-160`)**:
    비교 연산의 좌변이 리터럴인 Yoda 조건일 때 파티션 필터를 인식하지 못해 전체 파티션을 풀 스캔하며, `computeCeilPartitionKey`에서 RFC3339 포맷 날짜 파싱 실패 시 상한 키 감산이 누락되어 1개 파티션을 초과 스캔하는 Off-by-one 버그 존재.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/planner/pruning.go:45-90`, `130-160` (`PrunePartitions`, `computeCeilPartitionKey`)
  - **시나리오**: Yoda 조건 작성 시 파티션 프루닝 미동작, RFC3339 상한 조건 시 불필요한 미래 파티션 스캔.
  - **권장 수정안**: 이항 연산자의 좌우 반전 정규화(`'val' <= col` -> `col >= 'val'`) 및 RFC3339 날짜 레이아웃 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 081] `pkg/planner/setop.go`, `stats.go`
* **대상 영역**: 집합 연산(UNION, INTERSECT, EXCEPT) 플랜 및 테이블 통계 연동
* **Gemini 1차 분석**:
  - 히스토그램 버킷 및 MCV 기반 선택도 추정 체계 구축.
* **Claude 2차 교차 분석**:
  - **`setop.go` 주석 파싱 누락 및 `stats.go`의 MinVal==MaxVal 시 0 나눗셈 `NaN` 전파 (`setop.go:70-110`, `stats.go:1060-1090`)**:
    `FindTopLevelSetOps`에서 주석(`--`, `/* */`) 무시 로직이 없어 주석 내 키워드를 연산자로 오인하며, `interpolateRange`에서 단일값 컬럼(MinVal==MaxVal) 시 분모 0 나눗셈으로 `NaN`이 반환되어 전체 CBO 비용 계산이 마비됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/planner/setop.go:70-110`, `pkg/planner/stats.go:1060-1090`
  - **시나리오**: 주석이 포함된 UNION 쿼리 파싱 실패, 단일값 컬럼에 대한 범위 검색 시 플래너 비용 NaN 전파.
  - **권장 수정안**: 주석 건너뛰기 토크나이저 상태 추가 및 `maxVal == minVal` 시 0.0/1.0 즉각 반환 가드 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 082] `pkg/planner/explain.go`
* **대상 영역**: `EXPLAIN` / `EXPLAIN ANALYZE` 플랜 트리 포맷터
* **Gemini 1차 분석**:
  - PostgreSQL canonical JSON 플랜 포맷 구현으로 DBeaver/DataGrip 시각화 지원.
* **Claude 2차 교차 분석**:
  - **심층 계획 트리 직렬화 시 무제한 재귀 호출로 인한 스택 오버플로우 패닉 위험 (`explain.go:130-170`)**:
    `toPostgresPlanNode`에서 재귀 깊이 제한(`[RULE:BoundedRecursion]`) 없이 자식 노드를 순회하여 깊은 복합 쿼리 플랜 트리에서 스택 오버플로우 패닉 위험.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/planner/explain.go:130-170` (`toPostgresPlanNode`)
  - **시나리오**: 복잡한 중첩 서브쿼리 및 CTE가 결합된 대형 쿼리 EXPLAIN 실행 시 서버 패닉.
  - **권장 수정안**: 재귀 호출 시 깊이 카운터 추가(`depth > 128` 시 트리 절단 및 경고) 또는 반복 루프로 변환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 083] `pkg/planner/planner.go` (CBO 변환 규칙 및 탐욕 조인 순서 최적화)
* **대상 영역**: CBO 조인 변환 규칙 및 7개 이상 테이블 탐욕적 조인 순서 최적화
* **Gemini 1차 분석**:
  - Selinger DP 임계값(`MaxJoinDPThreshold = 6`) 초과 시 탐욕 알고리즘으로 폴백하여 컴파일 시간 단축.
* **Claude 2차 교차 분석**:
  - **탐욕 조인 탐색 시 카디널리티/비용 비교 없이 단순 필터 유무로 테이블을 선택하는 서브옵티멀 플랜 생성 (`planner.go:2730-2770`)**:
    `optimizeJoinOrderGreedy`에서 테이블 간 행 수나 선택도를 전혀 고려하지 않고 `Filter != ""`인 첫 번째 테이블을 무조건 좌측에 배치하여 대규모 테이블이 조인 트리 하단에 배치되는 성능 저하 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/planner/planner.go:2730-2770` (`optimizeJoinOrderGreedy`)
  - **시나리오**: 7개 이상 테이블 조인 시 행 수가 수천만 건인 테이블이 먼저 조인되어 중간 결과셋 폭증.
  - **권장 수정안**: 탐욕 선택 시 테이블 추정 행 수(`estRows`) 및 조인 필터 선택도를 반영한 간이 비용 비교 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 084] `pkg/execution/scan.go`, `project.go`
* **대상 영역**: 테이블/인덱스 스캔 반복자 및 프로젝션 수식 평가기
* **Gemini 1차 분석**:
  - 청크 스트리밍 및 LazyTuple 지연 디코딩으로 스캔 메모리 최소화.
* **Claude 2차 교차 분석**:
  - **`project.go`의 중복 별칭 처리 시 2차 충돌 검증 부재로 결과셋 컬럼명 충돌 (`project.go:80-110`)**:
    `addField`에서 중복 별칭 발견 시 `alias_N`으로 이름을 변경한 후, 이 변경된 이름이 이미 존재하는지에 대한 2차 중복 검증이 없어 결과 컬럼명이 여전히 충돌할 수 있음.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/execution/project.go:80-110` (`addField`)
  - **시나리오**: 동일한 이름의 별칭이 다수 존재하는 쿼리에서 생성된 결과 컬럼명 중복.
  - **권장 수정안**: 중복 검사를 루프로 수행하여 고유한 별칭이 생성될 때까지 카운터 증가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 085] `pkg/execution/filter.go`, `eval.go`
* **대상 영역**: 행 필터 반복자 및 튜플 수식 평가기
* **Gemini 1차 분석**:
  - 튜플 직접 수식 평가(`EvalTupleExprWithSubquery`)로 힙 할당 제로화 지원.
* **Claude 2차 교차 분석**:
  - **미매핑 수식 평가 시 `t.ToEvalMap()` 호출로 인한 대량 GC 압력 및 `MinInt64 / -1` 패닉 (`eval.go:270-285`, `410-430`)**:
    `default` 분기에서 `t.ToEvalMap()`을 매 행마다 호출하여 수만~수백만 개의 `map[string]any`가 힙에 할당되어 GC 부하 유발 (`[RULE:ZeroAllocation]`), 또한 정수 나눗셈에서 `MinInt64 / -1` 패닉 방어 누락.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/execution/eval.go:270-285`, `410-430` (`EvalTupleExprWithSubquery`, `evalTupleBinaryExprWithSubquery`)
  - **시나리오**: 복합 함수 필터 실행 시 과도한 GC 지연 발생, 특정 정수 연산 시 CPU 나눗셈 오버플로우 크래시.
  - **권장 수정안**: ToEvalMap 객체 풀링 도입, LIKE 및 주요 함수 직접 분기 추가, `MinInt64 / -1` 가드 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 086] `pkg/execution/join.go`
* **대상 영역**: 인메모리 해시 조인 및 인덱스 중첩 루프 조인
* **Gemini 1차 분석**:
  - `work_mem` 초과 감지 및 메모리 사용량 추적 체계 내장.
* **Claude 2차 교차 분석**:
  - **`Open()` 중간 에러 시 빌드 메모리 미정리 누수 및 복합 조인 키의 `\x00` 충돌 오매칭 (`join.go:120-220`, `710-740`)**:
    `h.right` 빌드 후 `h.left.Open()` 실패 시 `h.hashTable`이 정리되지 않아 메모리 누수 발생, 복합 키 생성 시 `\x00` 바이트 분리자를 사용하여 문자열 내 `\x00`이 존재할 때 키 충돌 결함 발생 (`[RULE:CharSanitization]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/execution/join.go:120-220`, `710-740` (`Open`, `extractTupleKey`)
  - **시나리오**: 조인 쿼리 초기화 실패 시 빌드 해시 테이블 메모리 누수, 바이너리 키 조인 시 엉뚱한 행 매칭.
  - **권장 수정안**: `Open()` 실패 시 `h.Close()` 명시적 호출 및 길이 접두사(Length-prefixed) 기반 키 인코딩 적용.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 087] `pkg/execution/merge_join.go`
* **대상 영역**: 머지 조인(Merge Join) 및 정렬 상태 검증기
* **Gemini 1차 분석**:
  - 중복 키 버퍼링(Mark & Restore)을 통한 $O(M+N)$ 고속 스트리밍 조인 구현.
* **Claude 2차 교차 분석**:
  - **정렬 순서 역전(Out-of-order) 튜플 검증 누락으로 인한 조인 매칭 사일런트 누락 (`merge_join.go:200-280`)**:
    하위 반복자에서 정렬 순서가 어긋난 행이 유입될 때 이를 감지하지 않고 조용히 조인 대상에서 제외하여 쿼리 결과 누락 발생 (`[RULE:ExplicitError]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/execution/merge_join.go:200-280` (`Next`)
  - **시나리오**: 정렬되지 않은 입력 스트림이 유입될 경우 에러 없이 쿼리 결과 일부가 조용히 사라짐.
  - **권장 수정안**: 이전 키와의 비교를 통해 스트림 정렬 순서 위반 시 즉각 명시적 런타임 에러 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 088] `pkg/execution/aggregate.go`
* **대상 영역**: `COUNT`, `SUM`, `AVG`, `GROUP BY` 해시 집계 반복자
* **Gemini 1차 분석**:
  - 빈 테이블 전역 집계(Global Aggregation) 특수 처리 및 ROLLUP/CUBE 비트마스크 지원.
* **Claude 2차 교차 분석**:
  - **`PERCENTILE_CONT` / `MEDIAN` 집계 시 메모리 상한 없는 슬라이스 적재로 인한 OOM 크래시 (`aggregate.go:800-840`)**:
    해당 집계 함수들이 `acc.values`에 전체 튜플 값을 메모리에 무제한 수집(`append`)하여 수천만 건 집계 시 `work_mem` 검사 없이 프로세스 OOM 유발 (`[RULE:BoundedBuffers]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/execution/aggregate.go:800-840` (`accumulateTuple`, `Open`)
  - **시나리오**: 대용량 테이블에 대해 `PERCENTILE_CONT` 실행 시 메모리 고갈로 서버 강제 종료.
  - **권장 수정안**: `work_mem` 버짓 추적 추가 또는 일정 크기 초과 시 T-Digest 근사 집계로 폴백.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 089] `pkg/execution/sort.go`
* **대상 영역**: 인메모리 정렬 및 디스크 스필(External Merge Sort)
* **Gemini 1차 분석**:
  - Min-Heap 기반 N-way 머지 정렬 및 임시 런 파일 스필 체계 완비.
* **Claude 2차 교차 분석**:
  - **`Open(ctx)` 실패 시 디스크에 생성된 임시 런 파일 영구 고아 누수 (`sort.go:180-235`)**:
    `Open()` 도중 취소(`ctx.Done()`) 또는 에러 발생 시 생성된 `lionsql-sort-run-*.tmp` 파일들이 삭제되지 않고 디스크에 영구 잔존 (`[RULE:CleanTeardown]`, `[RULE:HandleHygiene]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/execution/sort.go:180-235` (`Open`, `spillRun`)
  - **시나리오**: 대형 정렬 쿼리가 취소되거나 실패할 때마다 `/tmp` 디렉토리에 수백 MB의 임시 파일이 영구 누적됨.
  - **권장 수정안**: `Open()` 내부 에러 발생 시 `s.Close()`를 호출하여 임시 파일 및 열린 파일 디스크립터 즉각 정리.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 090] `pkg/execution/limit.go`, `window.go`
* **대상 영역**: `LIMIT/OFFSET` 및 윈도우 함수 반복자
* **Gemini 1차 분석**:
  - 조기 종료(Short-circuit) 최적화로 불필요한 하위 반복자 실행 차단.
* **Claude 2차 교차 분석**:
  - **`WindowIterator`의 전체 입력 튜플 무제한 메모리 적재로 인한 OOM 위험 (`window.go:40-70`)**:
    윈도우 함수 실행 시 `work_mem` 거버너 연동이나 디스크 스필 없이 전체 입력 튜플을 `w.rows` 슬라이스에 무제한 적재하여 대용량 데이터에서 OOM 크래시 발생 (`[RULE:BoundedBuffers]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/execution/window.go:40-70` (`Open`)
  - **시나리오**: 수백만 건의 데이터에 대해 `ROW_NUMBER() OVER()` 실행 시 메모리 폭증 크래시.
  - **권장 수정안**: 파티션 단위 스트리밍 처리 도입 및 `work_mem` 초과 시 에러 반환 또는 디스크 임시 적재.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 091] `pkg/execution/setop.go`, `chunk.go`
* **대상 영역**: UNION / EXCEPT 반복자 및 1,024-행 벡터화 청크 스트리머
* **Gemini 1차 분석**:
  - `sync.Pool` 기반 청크 메모리 재사용 슬랩 풀링 구현.
* **Claude 2차 교차 분석**:
  - **`encodeTupleKey`에서 `LazyTuple` 미해소 시 모든 튜플이 동일한 NULL 키로 오인식되는 데이터 왜곡 (`setop.go:278-310`)**:
    `RawTupleCursor`에서 생성된 `LazyTuple`이 Materialize되지 않은 상태에서 `t.Values`를 직접 순회하면 모든 값이 `nil`로 인식되어 서로 다른 행들이 동일한 NULL 키(`"N:\x00N:..."`)로 잘못 묶여 결과셋이 비정상적으로 누락됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical`
  - **위치**: `pkg/execution/setop.go:278-310` (`encodeTupleKey`)
  - **시나리오**: `UNION` 또는 `EXCEPT` 실행 시 지연 평가 튜플들이 모두 단 1개의 행으로 압축되어 심각한 데이터 손실 발생.
  - **권장 수정안**: 키 인코딩 시작 전 `t.Materialize()`를 반드시 선제 호출하여 값 슬라이스 실체화 보장.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 092] `pkg/execution/builder.go`, `iterator.go`
* **대상 영역**: 물리 실행 플랜 빌더 및 최상위 반복자 체인 인터페이스
* **Gemini 1차 분석**:
  - `ColumnIndexMap` 기반의 $O(1)$ 정수 오프셋 컬럼 참조로 제로 할당 조회 지원.
* **Claude 2차 교차 분석**:
  - **`Tuple.Materialize()`에서 디코딩 에러 무시로 인한 사일런트 데이터 손실 (`iterator.go:210-230`)**:
    `_, _ = t.Lazy.MaterializeRow()`에서 반환된 에러를 완전히 침묵시켜(`_ = err`), 직렬화 손상 시 에러 보고 없이 컬럼이 `nil`로 남아 사일런트 실패 발생 (`[RULE:ExplicitError]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/execution/iterator.go:210-230` (`Materialize`)
  - **시나리오**: 튜플 디코딩 오류 시 에러 없이 값이 NULL로 채워져 잘못된 비즈니스 데이터 반환.
  - **권장 수정안**: `Materialize()`가 에러를 반환할 수 있도록 서명 개선 및 파이프라인 상위 전파.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 093] `pkg/execution/tdigest.go`, `instrument.go`
* **대상 영역**: P99 지연시간 T-Digest 추정기 및 실행 프로파일러
* **Gemini 1차 분석**:
  - k1 스케일 함수 기반 T-Digest 압축으로 꼬리 백분위수 정밀 추정 지원.
* **Claude 2차 교차 분석**:
  - **`InstrumentedIterator.Next()`의 튜플당 `time.Now()` 2회 호출로 인한 극심한 프로파일링 오버헤드 (`instrument.go:50-70`)**:
    수백만 행 스캔 시 vDSO 시스템 콜이 수백만 회 발생하여 프로파일링 자체가 쿼리 실행 시간의 대부분을 차지하는 Probe Effect 왜곡 발생 (`[RULE:LatencyBudget]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/execution/instrument.go:50-70` (`Next`)
  - **시나리오**: `EXPLAIN ANALYZE` 실행 시 프로파일링 부하로 인해 순수 실행 대비 2배 이상의 지연시간 기록.
  - **권장 수정안**: 청크 단위(`NextChunk`) 시간 측정 또는 샘플링 기반 시간 측정 도입.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 094] `pkg/execution/governor.go`, `governor_sys_*.go`
* **대상 영역**: OS 네이티브 메모리 압력 감지기 및 플랫폼별 물리 메모리 탐색기
* **Gemini 1차 분석**:
  - macOS `sysctl`, Linux cgroups v1/v2 및 `/proc/meminfo` 자동 감지 완비.
* **Claude 2차 교차 분석**:
  - **Windows 환경에서 플랫폼 메모리 감지 API 미구현으로 4GB 하드코딩 클램핑 (`governor_sys_other.go:5-15`)**:
    Windows 서버에서 `GlobalMemoryStatusEx` API 조회가 구현되지 않아 고사양 Windows 머신에서도 무조건 4GB 장비로 인식되어 메모리 자원을 낭비함 (`[RULE:HermeticParity]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/execution/governor_sys_other.go:5-15` (`getPlatformPhysicalMemory`)
  - **시나리오**: 64GB Windows 서버에서 LionSQL의 메모리 버짓이 4GB 기준으로 고정되어 성능 잠재력 제한.
  - **권장 수정안**: `governor_sys_windows.go` 파일을 신설하여 Windows API를 통한 실제 물리 메모리 탐색 구현.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 095] `pkg/execution/governor.go`
* **대상 영역**: 글로벌 메모리 거버너 및 GOMEMLIMIT 동적 튜닝
* **Gemini 1차 분석**:
  - `debug.SetMemoryLimit` 연동 및 동적 힙 메모리 바운딩 설계 우수.
* **Claude 2차 교차 분석**:
  - **`ParseMemSize` 부동소수점 곱셈 시 정수 오버플로우 검증 누락 (`governor.go:100-115`)**:
    인간 친화적 메모리 문자열 파싱 시 극단값 입력에 대해 정수 래핑 오버플로우 사전 검증 누락 (`[RULE:DefensiveIndexing]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/execution/governor.go:100-115` (`ParseMemSize`)
  - **시나리오**: 잘못된 거대 메모리 설정값 입력 시 음수로 래핑될 위험.
  - **권장 수정안**: 곱셈 전 `math.MaxInt64 / multiplier` 상한 검증 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 096] `pkg/wire/server.go` (1/2)
* **대상 영역**: PGWire v3 TCP 리스너 및 연결 수명주기 관리
* **Gemini 1차 분석**:
  - SSL/GSSENC 핸드셰이크 협상 및 비동기 취소 요청(`CancelRequest`) 라우팅 완비.
* **Claude 2차 교차 분석**:
  - **메시지 길이(`msgLen`) 상한 검증 부재로 인한 단일 패킷 OOM 크래시 (`server.go:710-725`)**:
    클라이언트가 보낸 4바이트 길이 필드에 대한 상한 검증(`[RULE:BoundedBuffers]`) 없이 `make([]byte, msgLen-4)`를 수행하여 2GB 등의 거대 길이 유입 시 즉각 OOM 패닉 크래시 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical`
  - **위치**: `pkg/wire/server.go:710-725` (`handleConnection`)
  - **시나리오**: 악의적인 공격자 또는 잘못된 바이트 스트림 유입 시 DB 서버 프로세스 즉각 크래시.
  - **권장 수정안**: 패킷 최대 허용 크기(예: 64MB) 상한 검사 추가 및 초과 시 연결 즉각 차단.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 097] `pkg/wire/server.go` (2/2)
* **대상 영역**: PGWire 확장 쿼리 파이프라인 (`Parse`, `Bind`, `Execute`, Portal)
* **Gemini 1차 분석**:
  - 준비된 문장 캐시(`PreparedStatementCache`) 및 LRU 바운딩 제어 완비.
* **Claude 2차 교차 분석**:
  - **세션 내 `portals` 맵에 상한선이 없어 반복 바인드 시 메모리 누수 (`server.go:700-740`, `924-950`)**:
    `portals := make(map[string]*Portal)`에 세션당 포털 최대 용량 제한이 없어 클라이언트가 `Bind`를 수만 번 반복하고 `Close`를 누락할 경우 세션 메모리가 무한 팽창함 (`[RULE:BoundedBuffers]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/wire/server.go:700-740`, `924-950` (`handleConnection`, `handleBind`)
  - **시나리오**: 장기 유지 연결에서 포털이 지속 생성될 경우 프로세스 메모리 누수.
  - **권장 수정안**: 세션당 최대 포털 수(예: 256개) 제한 및 초과 시 LRU 퇴출 또는 에러 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 098] `pkg/wire/protocol.go`
* **대상 영역**: PGWire 메시지 인코딩/디코딩 및 패킷 버퍼 풀링
* **Gemini 1차 분석**:
  - `packetBufferPool` 기반 4KB 패킷 버퍼 재활용으로 GC 부담 완화.
* **Claude 2차 교차 분석**:
  - **`WriteDataRow` 호출 시 `[]string` 타입 사용으로 인한 NULL 값 표현 불가 (`protocol.go:250-285`)**:
    `WriteDataRow`가 `[]string`을 받아 문자열 길이만큼 패킷을 인코딩하므로 PostgreSQL NULL(길이 -1)을 표현하지 못하고 빈 문자열 `""`로 왜곡하여 클라이언트에 전송됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/wire/protocol.go:250-285` (`WriteDataRow`)
  - **시나리오**: `WriteDataRow`를 사용하는 쿼리 응답에서 NULL 값과 빈 문자열이 동일하게 빈 문자열로 반환됨.
  - **권장 수정안**: `WriteDataRow` 호출부 전면을 NULL-safe한 `WriteDataRowBytes`로 교체하거나 인터페이스 통일.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 099] `pkg/wire/copy.go`, `numeric.go`
* **대상 영역**: PGWire 대량 COPY 프로토콜 및 Numeric BCD 디코더
* **Gemini 1차 분석**:
  - Base-10000 4자리 워드 기반 PostgreSQL 바이너리 Numeric 디코딩 완비.
* **Claude 2차 교차 분석**:
  - **`numeric.go`의 거대 `weight` 조작 시 지수 거대 수 계산 DoS 취약점 (`numeric.go:50-70`)**:
    `decodePgBinaryNumeric`에서 비정상적인 거대 `weight`가 입력될 경우 `Exp(10, power)` 거대 수 계산으로 CPU 및 메모리가 급격히 소모되는 서비스 거부 취약점 존재 (`[RULE:BoundedBuffers]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/wire/numeric.go:50-70` (`decodePgBinaryNumeric`)
  - **시나리오**: 악의적인 바이너리 파라미터 유입 시 서버가 거대 수 연산 루프에 빠져 CPU 100% 점유.
  - **권장 수정안**: `power` 및 `weight`에 대한 엄격한 바운드 검사(`power < 1000`) 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 100] `pkg/wire/tls.go`
* **대상 영역**: PGWire SSL/TLS 암호화 핸드셰이크
* **Gemini 1차 분석**:
  - 자체 서명 인증서 자동 생성 및 TLS 1.2+ 최소 버전 강제.
* **Claude 2차 교차 분석**:
  - **`tlsConn.Handshake()` 실행 시 네트워크 타임아웃 미설정으로 인한 고루틴 무기한 정지 (`server.go:535-555`, `tls.go`)**:
    SSL 업그레이드 수신 후 핸드셰이크 완료 대기 시 데드라인(`SetDeadline`)이 없어 클라이언트가 정지할 경우 고루틴과 TCP 소켓이 무기한 블로킹 누수됨 (`[RULE:CleanTeardown]`, `[RULE:ZeroOrphan]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/wire/server.go:535-555`, `pkg/wire/tls.go`
  - **시나리오**: 불완전한 TLS 연결 시도로 인해 서버에 고아 고루틴과 파일 디스크립터가 지속 누적됨.
  - **권장 수정안**: 핸드셰이크 시작 전 10초 타임아웃(`conn.SetDeadline(time.Now().Add(10*time.Second))`) 설정 및 완료 후 해제.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 101] `pkg/wire/tds/server.go` (1/2)
* **대상 영역**: Microsoft TDS 7.x/8.0 TCP 리스너 및 세션 디스패처
* **Gemini 1차 분석**:
  - TDS 8.0 직접 TLS(0x16) 및 TDS 7.x PRELOGIN 기반 점진적 암호화 협상 완비.
* **Claude 2차 교차 분석**:
  - **TDS 8.0 직접 TLS 핸드셰이크 시 타임아웃 미설정으로 고루틴 무기한 정지 (`tds/server.go:580-600`)**:
    클라이언트가 0x16 바이트 전송 후 TLS 핸드셰이크를 완료하지 않을 때 데드라인 설정이 없어 `tlsConn.Handshake()`에서 무기한 블로킹되어 고루틴이 영구 누수됨 (`[RULE:CleanTeardown]`, `[RULE:ZeroOrphan]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/wire/tds/server.go:580-600` (`handleConnection`)
  - **시나리오**: 악의적인 포트 스캐너나 연결 중단 클라이언트로 인해 TDS 리스너 고루틴 누적 고갈.
  - **권장 수정안**: `conn.SetDeadline`을 설정하여 10초 내 핸드셰이크 미완료 시 연결 강제 종료.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 102] `pkg/wire/tds/server.go` (2/2)
* **대상 영역**: TDS SQL Batch 및 RPC 실행 파이프라인
* **Gemini 1차 분석**:
  - `runQueryWithAttention`을 통한 패킷 기반 쿼리 취소(Attention 0x06) 인터럽트 처리.
* **Claude 2차 교차 분석**:
  - **동기 DML 실행 도중 Attention 수신 시 엔진 롤백 완료 전 조기 DONE 응답 전송 위험 (`tds/server.go:680-730`)**:
    Attention 패킷 수신 시 즉각 취소 토큰(`cancel()`)을 발송하나, 엔진 워커가 물리 트랜잭션 롤백을 완료하기 전에 Attention Acknowledgment DONE 토큰을 클라이언트에 조기 전송하여 클라이언트가 즉시 다음 쿼리를 날릴 경우 트랜잭션 상태 불일치 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/wire/tds/server.go:680-730` (`runQueryWithAttention`)
  - **시나리오**: 롱러닝 트랜잭션 취소 직후 SSMS에서 새 쿼리 실행 시 `Transaction context in use` 충돌 발생.
  - **권장 수정안**: 취소 신호 발송 후 하위 실행 워커가 완전히 반환될 때까지 동기화 채널로 대기 후 DONE 토큰 전송.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 103] `pkg/wire/tds/token.go`, `packet.go`
* **대상 영역**: TDS 토큰 인코더 (`COLMETADATA`, `ROW`, `DONE`) 및 패킷 청크 재조립
* **Gemini 1차 분석**:
  - `tdsFramePool4K/8K`를 통한 패킷 프레임 메모리 풀링 최적화.
* **Claude 2차 교차 분석**:
  - **`ReadPacket` multi-packet 재조립 시 EOM 미도착 시 페이로드 무제한 누적으로 OOM 크래시 (`tds/packet.go:60-95`)**:
    클라이언트가 `StatusEOM` (0x01) 플래그를 보내지 않고 청크를 지속 전송할 경우 `fullPayload`가 무제한 증가하여 프로세스 OOM 크래시 유발 (`[RULE:BoundedBuffers]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical`
  - **위치**: `pkg/wire/tds/packet.go:60-95` (`ReadPacket`)
  - **시나리오**: 악의적인 TDS 클라이언트가 무한 청크 패킷 스트림을 주입하여 서버 즉각 강제 종료.
  - **권장 수정안**: 재조립 페이로드 최대 크기 상한(예: 64MB) 검사 추가 및 초과 시 에러 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 104] `pkg/wire/tds/login.go`, `crypto.go`, `tls.go`
* **대상 영역**: TDS 7.x/8.0 로그인 패킷 디코딩 및 PRELOGIN 캡슐화 TLS 핸드셰이크
* **Gemini 1차 분석**:
  - `TDSHandshakeConn`을 통한 TDS 0x12 PRELOGIN 패킷 내 TLS 레코드 캡슐화 완비.
* **Claude 2차 교차 분석**:
  - **`TDSHandshakeConn.Read`에서 PRELOGIN 캡슐화 TLS 핸드셰이크 중 네트워크 타임아웃 부재 (`tds/tls.go:45-75`)**:
    캡슐화된 TLS 핸드셰이크 도중 패킷 도착 대기 시 데드라인이 없어 클라이언트가 정지할 경우 영구 블로킹 누수 발생 (`[RULE:CleanTeardown]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/wire/tds/tls.go:45-75` (`TDSHandshakeConn.Read`)
  - **시나리오**: 비정상적인 SSMS/드라이버 연결 시도로 인해 서버에 정체된 소켓과 고루틴 누적.
  - **권장 수정안**: `Read` 시작 시 기본 읽기 데드라인 설정 및 타임아웃 검사 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 105] `pkg/wire/tds/rpc.go`, `server.go`
* **대상 영역**: `sp_executesql`, `sp_prepare` 파라미터 RPC 파서 및 SQL 바인딩 빌더
* **Gemini 1차 분석**:
  - `substituteParamsSinglePass` 단일 패스 스캔으로 T-SQL 파라미터 치환 고속 처리.
* **Claude 2차 교차 분석**:
  - **`formatSQLValue`에서 복합 타입/Decimal 포맷 시 작은따옴표 이스케이프 미처리 (`tds/server.go:1770-1793`)**:
    `default` 분기에서 `fmt.Sprintf("'%v'", val)`를 호출하여 내부에 작은따옴표(`'`)가 포함된 객체나 비표준 문자열이 들어올 경우 SQL 인젝션 및 문법 에러 위험.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/wire/tds/server.go:1770-1793` (`formatSQLValue`)
  - **시나리오**: 특수 문자가 포함된 파라미터를 RPC로 전달 시 구문 파싱 에러 또는 SQL 오동작.
  - **권장 수정안**: 모든 문자열 변환 경로에 대해 `strings.ReplaceAll(s, "'", "''")` 이스케이프 강제.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 106] `pkg/replication/manager.go`, `stream.go`
* **대상 영역**: 1-Writer Multi-Reader WAL 복제 코디네이터
* **Gemini 1차 분석**:
  - 상태 머신 기반의 Always On 가용성 그룹 복제 관리자 구조.
* **Claude 2차 교차 분석**:
  - **LSN 시퀀스 역전 발생 시 레플리카의 무조건적 역전 수용 위험 (`manager.go:150-180`)**:
    프라이머리 노드가 비정상 복구로 인해 과거 LSN으로 되돌아갔을 때, 레플리카가 LSN 단조 증가 검증 없이 역전된 레코드를 덮어써 데이터 역전 손상 발생 (`[RULE:ExplicitError]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/replication/manager.go:150-180`
  - **시나리오**: 프라이머리 장애 복구 후 롤백된 LSN 스트림 유입 시 레플리카 데이터 불일치 오염.
  - **권장 수정안**: 유입되는 LSN이 레플리카의 `LastLSN`보다 작을 경우 즉각 복제 중단 및 전체 재동기화 플래그 발행.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 107] `pkg/replication/client.go`, `broadcaster.go`
* **대상 영역**: 레플리카 WAL 수신 클라이언트 및 변경사항 브로드캐스터
* **Gemini 1차 분석**:
  - `WALBroadcaster` 링 버퍼를 통한 신규 구독 레플리카 히스토리 고속 리플레이.
* **Claude 2차 교차 분석**:
  - **느린 레플리카 버퍼 포화 시 WAL 레코드 사일런트 드롭으로 인한 데이터 불일치 (`broadcaster.go:50-70`)**:
    `Broadcast`에서 레플리카 채널 버퍼가 가득 찼을 때 `default`로 새 WAL 레코드를 조용히 버려 레플리카에 WAL 누락(Gap)이 발생하고 데이터가 영구 불일치됨 (`[RULE:ExplicitError]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Critical`
  - **위치**: `pkg/replication/broadcaster.go:50-70` (`Broadcast`)
  - **시나리오**: 부하 상황에서 슬로우 레플리카에 일부 커밋 데이터가 누락되어 복제본 데이터 손상.
  - **권장 수정안**: 레코드 드롭 시 해당 구독자의 연결을 강제 종료하고 프라이머리가 재동기화(Resync)를 요구하도록 수정.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 108] `pkg/replication/failover.go`, `handlers.go`, `types.go`
* **대상 영역**: Always On 자동 장애조치 및 쿼럼 선거
* **Gemini 1차 분석**:
  - Raft-like 최고 LSN 우선순위 기반 리더 선출 및 쿼럼 계산.
* **Claude 2차 교차 분석**:
  - **호스트명/IP 표기 차이 시 자기 자신에게 투표 요청을 보내 데드락 또는 쿼럼 왜곡 (`failover.go:100-130`)**:
    `cleanPeer == candidateURL` 비교 시 `localhost:8090`과 `127.0.0.1:8090`의 형식 차이로 인해 자기 자신을 피어로 오인하여 자가 투표 요청을 날려 타임아웃 지연 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/replication/failover.go:100-130` (`ElectLeader`)
  - **시나리오**: 페일오버 선거 도중 자가 투표 타임아웃으로 인해 리더 선출이 수 초간 지연됨.
  - **권장 수정안**: IP/Port 정규화(`net.SplitHostPort`)를 통한 자가 주소 필터링 강화.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 109] `pkg/backup/backup.go`, `restore.go`
* **대상 영역**: 스냅샷 백업 및 전체 복원 파이프라인
* **Gemini 1차 분석**:
  - 비차단 읽기 전용 스냅샷 트랜잭션을 통한 온라인 백업 지원.
* **Claude 2차 교차 분석**:
  - **`restore.go`의 tar 압축 해제 시 심볼릭 링크 헤더 검증 부재로 임의 파일 덮어쓰기 취약점 (`restore.go:70-100`)**:
    `cleanName` 검사만 수행하고 `tar.TypeSymlink` 헤더에 대한 검증이 없어 악의적으로 조작된 백업 파일 복원 시 호스트 파일 시스템 이탈 위험 (`[RULE:SymlinkSafety]`, `[RULE:PathSecurity]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/backup/restore.go:70-100` (`RestoreBackup`)
  - **시나리오**: 조작된 tar.gz 백업 복원 시 임의 시스템 파일 덮어쓰기 공격 가능.
  - **권장 수정안**: 일반 파일(`tar.TypeReg`) 및 디렉토리(`tar.TypeDir`) 이외의 모든 헤더 즉각 거부.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 110] `pkg/backup/pitr.go`, `wal_archive.go`
* **대상 영역**: 시점 복구(PITR) 및 지속적 WAL 아카이빙 엔진
* **Gemini 1차 분석**:
  - 세그먼트 체크섬 검증 및 LSN 연속성 기반 시점 복구 엔진.
* **Claude 2차 교차 분석**:
  - **`ApplyWALRecordWithOptions`에서 DDL 훅 실패 시 스토리지-카탈로그 동기화 붕괴 (`pitr.go:50-70`)**:
    메모리 엔진에 WAL을 적용한 후 카탈로그 갱신(`ddlHook`)에서 에러가 발생했을 때 롤백 처리가 없어 스토리지에는 테이블이 생성되었으나 카탈로그 메타데이터에는 누락되는 분열 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/backup/pitr.go:50-70` (`ApplyWALRecordWithOptions`)
  - **시나리오**: PITR 복구 도중 DDL 처리 실패 시 스키마 메타데이터 불일치로 복구 중단.
  - **권장 수정안**: DDL 훅 실행 실패 시 즉각 에러를 반환하고 트랜잭션 단위 롤백 보장.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 111] `pkg/cdc/dispatcher.go`, `streamer.go`
* **대상 영역**: Transactional Outbox 및 실시간 CDC 스트리머
* **Gemini 1차 분석**:
  - 원자적 WAL 커밋 훅 연동 및 링 버퍼 기반 CDC 이벤트 발행.
* **Claude 2차 교차 분석**:
  - **구독자 버퍼 포화 시 사일런트 이벤트 드롭으로 인한 외부 시스템 데이터 유실 (`streamer.go:70-90`)**:
    `select / default`로 버퍼 가득 참 시 이벤트를 조용히 버려 카프카 등 다운스트림 브로커에 변경사항이 영구 누락됨 (`[RULE:ExplicitError]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/cdc/streamer.go:70-90` (`OnWALRecord`)
  - **시나리오**: 트래픽 폭증 시 CDC 컨슈머에 데이터 누락이 발생하여 외부 캐시/검색엔진 정합성 파괴.
  - **권장 수정안**: 드롭 발생 시 경고 로그 및 드롭 메트릭 카운트 기록, 구독자에게 에러 이벤트 전송.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 112] `pkg/cdc/event.go`, `outbox.go`, `outbox_sinks.go`
* **대상 영역**: CDC 이벤트 직렬화 및 외부 싱크 어댑터
* **Gemini 1차 분석**:
  - 원자적 체크포인트 저장(`[RULE:AtomicSaves]`) 및 At-least-once 전달 보장.
* **Claude 2차 교차 분석**:
  - **단일 브로커 싱크 장애 시 전체 Outbox 디스패치 파이프라인 정체(Head-of-Line Blocking) (`outbox.go:210-240`)**:
    복수 싱크 등록 시 특정 1개 싱크가 네트워크 지연/실패를 겪을 때 동기 순차 전송으로 인해 다른 정상 싱크까지 함께 대기되어 전체 디스패처가 멈춤.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/cdc/outbox.go:210-240` (`dispatchLoop`)
  - **시나리오**: 웹훅 싱크 1개의 응답 지연으로 인해 카프카 메시지 발행까지 수 초간 지연.
  - **권장 수정안**: 싱크별 독립 고루틴 및 버퍼 큐 분리 또는 비동기 병렬 Publish 적용.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 113] `pkg/slt/runner.go`, `parser.go`
* **대상 영역**: SQLLogicTest 내장 러너 및 스크립트 파서
* **Gemini 1차 분석**:
  - SQLLogicTest 표준 규격 파싱 및 MD5 결과 해시 검증 완비.
* **Claude 2차 교차 분석**:
  - **대용량 SLT 파일 파싱 시 전체 레코드 일괄 슬라이스 적재로 인한 메모리 폭증 (`slt/parser.go:60-120`)**:
    수십만 줄의 SLT 파일을 파싱할 때 스트리밍 파싱 대신 전체 `[]Record`를 메모리에 한 번에 적재하여 메모리 낭비 유발 (`[RULE:BoundedBuffers]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/slt/parser.go:60-120` (`ParseFile`)
  - **시나리오**: 수백 MB의 SQLLogicTest 스크립트 실행 시 메모리 수 GB 점유.
  - **권장 수정안**: 레코드 반복자(Iterator) 패턴을 도입하여 파일 스트리밍 파싱 지원.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 114] `pkg/sqlutil/predicate.go`, `tokenizer.go`
* **대상 영역**: 괄호 균형 Context-Free 조건식 파서 및 토크나이저
* **Gemini 1차 분석**:
  - Dyck 언어 기반 괄호 매칭 및 주석/따옴표/브래킷 스킵 지원.
* **Claude 2차 교차 분석**:
  - **`FindMatchingParen`에서 닫는 괄호가 먼저 나오는 비정상 구문 입력 시 언더플로우 가드 누락 (`tokenizer.go:1090-1110`)**:
    `depth--` 시 `depth < 0`이 되는 비정상 구문에서 즉각 에러 반환 없이 음수 depth인 채로 루프를 지속하여 잘못된 인덱스를 반환할 위험 (`[RULE:DefensiveIndexing]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `pkg/sqlutil/tokenizer.go:1090-1110` (`FindMatchingParen`)
  - **시나리오**: 비정상적인 닫는 괄호가 포함된 SQL 파싱 시 잘못된 서브쿼리 경계값 반환.
  - **권장 수정안**: `depth < 0` 발생 시 즉각 `-1`을 반환하는 언더플로우 방어 가드 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 115] `pkg/sqlutil/like.go`, `params.go`
* **대상 영역**: 고속 제로할당 LIKE 매처 및 $N 파라미터 변환기
* **Gemini 1차 분석**:
  - 되추적(Backtracking) 기반의 정밀 제로-정규식 T-SQL 와일드카드 매처.
* **Claude 2차 교차 분석**:
  - **`matchBracket`에서 첫 번째 문자가 `]`인 이스케이프 브래킷(`[]abc]`) 파싱 실패 (`like.go:80-110`)**:
    T-SQL 표준에서 브래킷 집합의 첫 글자가 `]`인 경우 집합 종결이 아닌 리터럴 `]`로 처리해야 하나, 단순 `]` 검색으로 첫 글자에서 바로 브래킷을 닫아버려 문법 오인식 발생.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/sqlutil/like.go:80-110` (`matchBracket`)
  - **시나리오**: `[]abc]` 패턴 매칭 시 빈 브래킷으로 인식되어 올바른 일치 실패.
  - **권장 수정안**: 브래킷 시작 직후(`start == pIdx+1` 또는 `negate && start == pIdx+2`)의 `]`는 리터럴로 건너뛰고 다음 `]`를 닫는 기호로 검색하도록 수정.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 116] `pkg/sqlutil/casing.go`, `comparator.go`, `pagination.go`
* **대상 영역**: 대소문자 무시 식별자 정규화, 다형성 비교기, MSSQL TOP/OFFSET 페이지네이션 변환기
* **Gemini 1차 분석**:
  - `CanonicalIdent`, `CompareAny`, `NormalizeMSSQLPagination` 등의 고속 헬퍼 집합.
* **Claude 2차 교차 분석**:
  - **`normalizeTop`에서 `strings.Index` 기반 서브스트링 검색으로 인한 SQL 텍스트 훼손 (`pagination.go:203-209`)**:
    `afterTop := head[topPos+3:]` 이후 `idxLimit := strings.Index(afterTop, limitToken)`을 수행할 때 토큰 경계 검증이 없어, `SELECT TOP 1 100 FROM t` 또는 `SELECT TOP (10) col1` 등의 쿼리에서 `limitToken`(`1`)이 뒤따르는 숫자 리터럴이나 컬럼명 내에 먼저 출현할 경우 잘못된 인덱스를 잘라내어 프로젝션 SQL 구문을 손상시킴 (`[RULE:DefensiveIndexing]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/sqlutil/pagination.go:203-209` (`normalizeTop`)
  - **시나리오**: `SELECT TOP 1 100` 등 숫자 상수를 포함하는 쿼리 변환 시 엉뚱한 위치가 잘려 문법 오류 발생.
  - **권장 수정안**: `SplitTopLevelTokens`에서 토큰 인덱스를 기반으로 정확한 경계를 계산하거나 정밀 렉서 토큰 위치를 추적하여 치환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 117] `pkg/auth/auth.go`, `provider.go`
* **대상 영역**: 슈퍼유저 인증, Salted SHA256 해시, JWT 서명/검증, API 키 발급 및 외부 AuthProvider SPI
* **Gemini 1차 분석**:
  - SecretKey 자동 생성, Base64Url JWT 발급 및 `subtle.ConstantTimeCompare` 서명 검증 양호.
* **Claude 2차 교차 분석**:
  - **`Authenticate` 사용자 부재 시 조기 리턴으로 인한 사용자 열거 타이밍 오라클 취약점 (`auth.go:220-229`)**:
    `s.users[normEmail]`이 존재하지 않을 때 `VerifyPassword` 연산을 수행하지 않고 즉각 `ErrInvalidCredentials`를 반환하여 마이크로초 단위 응답 시간 차이로 유효한 관리자 이메일을 역추적 가능. 또한 `RegisterProvider`로 등록된 외부 `s.providers`가 `Authenticate` 경로에서 전혀 호출되지 않고 무시됨.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/auth/auth.go:220-229` (`Authenticate`)
  - **시나리오**: 외부 공격자가 로그인 엔드포인트 응답 지연을 측정하여 시스템에 등록된 계정 이메일을 전수 열거.
  - **권장 수정안**: 유저가 없더라도 더미 해시 검증을 상시 수행하여 타이밍 중립성을 확보하고 등록된 AuthProvider 호출 체인 연결.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 118] `pkg/auth/rbac.go`
* **대상 영역**: T-SQL 스타일 역할 기반 접근 제어(RBAC), GRANT/REVOKE/DENY 권한 매트릭스
* **Gemini 1차 분석**:
  - 표준 MSSQL 데이터베이스 주체(`dbo`, `guest`, `sa`, 고정 데이터베이스 역할) 모델링 양호.
* **Claude 2차 교차 분석**:
  - **다중 역할 할당 시 `GrantDeny`가 조기 `GrantAllow` 리턴에 의해 무력화되는 권한 승격 결함 (`rbac.go:440-451`)**:
    `for _, r := range u.Roles` 루프에서 한 역할이 `roleGranted == true`이면 즉각 `true, nil`을 반환하여, 이후에 순회되는 다른 역할의 명시적 `GrantDeny`가 평가되지 않고 우회됨. MSSQL/RBAC 대원칙인 "명시적 DENY는 모든 GRANT를 압도한다" 위반.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/auth/rbac.go:440-451` (`HasTablePermission`)
  - **시나리오**: Role A(GRANT)와 Role B(DENY)를 동시에 가진 유저가 슬라이스 순서에 따라 차단되지 않고 테이블에 비인가 접근.
  - **권장 수정안**: 루프에서 모든 역할을 먼저 검사하여 단 하나라도 `roleDenied`가 있으면 즉시 거부하고, 루프 종료 후 허용 여부를 판정하도록 2패스 평가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 119] `pkg/config/config.go`, `version.go`, `build_pure.go`, `build_wiredtiger.go`
* **대상 영역**: 구성 설정 모델, 자동 탐색 우선순위 계층, 빌드 태그 상수, 프로덕션 보안 가드
* **Gemini 1차 분석**:
  - `[RULE:ConfigHierarchy]` (CLI > Env > File > Default) 및 루프백 인터페이스 검증기 충실.
* **Claude 2차 교차 분석**:
  - **`AutoDiscoverConfig`에서 손상된 JSON 파일 에러 사일런트 억제 결함 (`config.go:218-233`)**:
    디스크에 `lionsql.json`이 존재하지만 문법 오류 등으로 `LoadFromFile`이 실패할 때(`err != nil`), 에러를 보고하거나 중단하지 않고 조용히 기본값 `DefaultConfig()`로 대체함 (`[RULE:ExplicitError]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/config/config.go:218-233` (`AutoDiscoverConfig`)
  - **시나리오**: 운영자가 JSON 문법 오류를 낸 설정 파일을 배포했을 때, 에러 알림 없이 기본 비밀번호/포트로 구동되어 보안 위험 초래.
  - **권장 수정안**: 설정 파일이 존재함에도 파싱에 실패하면 즉각 파싱 에러를 상위로 전파하여 기동을 차단.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 120] `pkg/api/server.go`, `middleware.go`, `request_logger.go`
* **대상 영역**: REST API HTTP 라우터, CORS/보안 미들웨어, Gzip 압축, 요청 로깅
* **Gemini 1차 분석**:
  - 표준 미들웨어 체인, 풀 기반 Gzip 압축 및 헤더 설정 양호.
* **Claude 2차 교차 분석**:
  - **`Transfer-Encoding: chunked` 요청 시 바디 크기 제한 우회 DoS 취약점 (`middleware.go:78-82`)**:
    `if r.Body != nil && r.ContentLength > 0` 가드로 인해 `ContentLength == -1`인 Chunked 전송 스트림 요청에 `http.MaxBytesReader`가 적용되지 않아 대용량 페이로드 메모리 고갈 DoS 유발 가능 (`[RULE:BoundedBuffers]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/api/middleware.go:78-82` (`corsAndSecurityMiddleware`)
  - **시나리오**: 악의적 클라이언트가 Chunked 인코딩으로 기가바이트 크기의 JSON 본문을 전송하여 서버 OOM 유도.
  - **권장 수정안**: `r.ContentLength != 0`인 모든 바디에 무조건 `http.MaxBytesReader`를 적용.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 121] `pkg/api/handlers_records.go`, `records_filter.go`
* **대상 영역**: HTTP 레코드 CRUD 엔드포인트 및 URL 필터 파서/평가기
* **Gemini 1차 분석**:
  - 재귀 하강 필터 파서, AND/OR 우선순위 및 다형성 조건 매칭 기능 양호.
* **Claude 2차 교차 분석**:
  - **`matchLike`에서 행 순회 루프마다 `regexp.Compile` 동적 컴파일 수행 (`records_filter.go:203-235`)**:
    테이블 레코드 필터링 시 매 행마다 정규식을 빌드하고 컴파일하여 극심한 GC 압박 및 CPU 낭비 발생. `[RULE:ZeroAllocation]`, `[RULE:ModularAbstraction]`, Invariant 7 (Zero-Regex SQL Parsing) 위반.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/api/records_filter.go:203-235` (`matchLike`)
  - **시나리오**: 수만 건의 레코드 검색 시 `col like '%abc%'` 필터로 인해 초당 수만 회의 정규식 컴파일이 발생하여 지연시간 급증.
  - **권장 수정안**: 중복된 ad-hoc 정규식 매처를 제거하고 `pkg/sqlutil`의 고속 제로할당 비정규식 `MatchLike` 호출로 일원화.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 122] `pkg/api/handlers_tables.go`, `handlers_import_export.go`
* **대상 영역**: 테이블 스키마 관리, DDL DTO 매핑, CSV/JSON 데이터 임포트/익스포트
* **Gemini 1차 분석**:
  - 멀티파트 폼 및 스트리밍 CSV/JSON 변환 파이프라인 양호.
* **Claude 2차 교차 분석**:
  - **대용량 CSV 임포트 시 무제한 단일 OCC 트랜잭션 적재 및 PK 타임스탬프 덮어쓰기 결함 (`handlers_import_export.go:189-194`, `300-306`)**:
    수십만 행의 CSV 임포트를 단일 트랜잭션 메모리 워크스페이스에 통째로 적재하여 OOM을 유발하고, 정수 PK가 누락되거나 0일 때 `time.Now().UnixNano()/1ms + rowIdx`로 대체하여 기존 0번 PK를 손상시키거나 키 충돌 유발.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/api/handlers_import_export.go:189-194`, `300-306` (`handleImportTable`)
  - **시나리오**: 대형 CSV 임포트 중 OCC 메모리 부족 에러 발생 및 유효한 0값 Primary Key 손실.
  - **권장 수정안**: 1,000행 단위 청크 배치 커밋을 적용하고, PK 자동 생성은 명시적인 자동증가 컬럼에만 한정.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 123] `pkg/api/handlers_auth.go`, `handlers_admin.go`
* **대상 영역**: 인증 핸들러, SQL 즉시 실행, 쿼리 모니터링/취소, 백업/복원 REST 엔드포인트
* **Gemini 1차 분석**:
  - 세션 기반 SQL 실행, 실시간 쿼리 취소 채널 및 Prometheus 지표 엔드포인트 제공.
* **Claude 2차 교차 분석**:
  - **쿼리 모니터링 및 취소 엔드포인트 인증 미들웨어 누락 (`server.go:557-558`, `handlers_admin.go:97-132`)**:
    `POST /api/query/cancel`과 `GET /api/queries/active`가 `wrapAuth` 없이 노출되어 있어, 비인가 외부 사용자가 현재 실행 중인 민감 SQL 쿼리를 조회하거나 다른 사용자의 쿼리를 임의 취소하여 서비스 거부 유발 가능.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/api/server.go:557-558` (`handleQueryCancel`, `handleListActiveQueries`)
  - **시나리오**: 공격자가 인증 없이 실행 중인 모든 쿼리 정보를 탈취하고 핵심 트랜잭션을 강제 취소.
  - **권장 수정안**: 두 엔드포인트에 `wrapAuth(..., false, "")`를 적용하여 인증된 관리자만 접근 가능하도록 격리.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 124] `pkg/api/rls.go`, `rls_handlers.go`
* **대상 영역**: 행 단위 보안(RLS) 규칙 관리, `@auth.*` 토큰 치환 및 필터 합성
* **Gemini 1차 분석**:
  - REST 레코드 엔드포인트 대상의 세밀한 권한 제어 규칙 컴파일 기능 구현.
* **Claude 2차 교차 분석**:
  - **`POST /api/sql` 쿼리 실행 시 RLS 정책 전면 우회 결함 (`server.go:556`, `handlers_admin.go:28-65`)**:
    RLS 규칙이 오직 `/api/tables/{name}/records` 엔드포인트에만 바인딩되어 있고, SQL 쿼리를 직접 수행하는 `/api/sql`에서는 RLS 필터 주입이 전혀 이루어지지 않아 SQL 실행 권한을 가진 비관리자가 RLS를 완전히 우회.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/api/handlers_admin.go:28-65` (`handleExecuteSQL`)
  - **시나리오**: 테넌트 격리 RLS가 걸려 있는 데이터베이스에서 비관리자가 `/api/sql`로 `SELECT * FROM tbl`을 실행하여 타 테넌트 데이터 전체 열람.
  - **권장 수정안**: 비-슈퍼유저 세션에서 SQL 실행 시 카탈로그 RLS 룰을 쿼리 계획서 WHERE 절에 의무 주입하거나 권한 검증 분기 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 125] `pkg/api/handlers_procs.go`, `engine_aliases.go`, `sdk.go`
* **대상 영역**: 저장 프로시저 등록, T-SQL 바이트코드 컴파일 트리거, REST 실행 엔드포인트
* **Gemini 1차 분석**:
  - 프로시저 플랜 캐시 연동 및 인자 정규화(`@arg` vs `arg`) 지원 양호.
* **Claude 2차 교차 분석**:
  - **`handleExecProc`에서 프로시저가 생성한 `SELECT` 결과 집합 응답 누락 (`handlers_procs.go:180-210`)**:
    프로시저 실행 결과인 `res.ResultSets`를 HTTP JSON 응답에 포함하지 않고 `return_code`, `duration_ms`, `logs`만 반환하여, 데이터를 조회하는 프로시저의 결과를 클라이언트가 받아볼 수 없음.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/api/handlers_procs.go:180-210` (`handleExecProc`)
  - **시나리오**: REST API를 통해 조회 프로시저를 호출했을 때 빈 메타데이터만 수신되고 실제 행 데이터가 누락됨.
  - **권장 수정안**: `res.ResultSets`를 응답 JSON DTO에 직렬화하여 반환.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 126] `pkg/api/cron.go`, `cron_handlers.go`
* **대상 영역**: 백그라운드 크론 스케줄러, 표준 5필드 크론 표현식 검증, 비동기 작업 실행기
* **Gemini 1차 분석**:
  - 1초 단위 틱 기반 정확한 스케줄 평가 및 동시 실행 중복 방지 키 구현.
* **Claude 2차 교차 분석**:
  - **크론 작업 고루틴 비동기 실행 시 `WaitGroup` 누락 및 패닉 복구 부재 (`cron.go:126, 130-152`)**:
    `go cs.executeJob(ctx, job)` 실행 시 `cs.wg.Add(1)`을 호출하지 않아 서버 셧다운(`Stop()`) 시 진행 중인 크론 작업이 대기 없이 즉각 중단되며 (`[RULE:ZeroOrphan]`), 개별 작업 내부의 런타임 패닉을 잡는 `recover()`가 없어 크론 작업 오류가 서버 전체 프로세스 크래시 유발 (`[RULE:PanicRecovery]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/api/cron.go:126, 130-152` (`evaluate`, `executeJob`)
  - **시나리오**: 크론 작업 도중 패닉이 발생하면 LionSQL 엔진 전체가 비정상 종료됨.
  - **권장 수정안**: `cs.wg.Add(1)`로 작업 수명주기를 트래킹하고 `defer cs.wg.Done()` 및 `defer func() { recover() }()` 패닉 격리 훅 추가.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 127] `pkg/api/mailer.go`, `mailer_handlers.go`, `webhook.go`, `webhook_handlers.go`, `realtime.go`
* **대상 영역**: SMTP 메일 발송기, 웹훅 디스패처/재시도 엔진, SSE 실시간 브로드캐스터
* **Gemini 1차 분석**:
  - 지수 백오프 기반 웹훅 재시도, HMAC SHA256 웹훅 서명, SSE 비차단 브로드캐스트 양호.
* **Claude 2차 교차 분석**:
  - **웹훅 재시도 루프 내 `select` 문에서의 조기 탈출 오류 (`webhook.go:424-430`)**:
    `case <-d.ctx.Done(): break` 수행 시 Go 언어 문법상 `select` 문만 탈출하고 외부의 `for` 재시도 루프를 탈출하지 못해, 서버 종료 중에도 남은 재시도를 끝까지 수행하며 지연 발생 (`[RULE:CooperativeAbort]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/api/webhook.go:424-430` (`deliverWithRetry`)
  - **시나리오**: 서버 셧다운 시 웹훅 재시도가 즉각 중단되지 않고 최대 타임아웃까지 셧다운을 블로킹.
  - **권장 수정안**: 레이블을 지정한 `break retryLoop` 또는 즉시 `return delivery`로 조기 종료.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 128] `pkg/api/spa.go`, `pkg/ui/embed.go`, `pkg/ui/dashboard.html`
* **대상 영역**: 싱글 페이지 웹 애플리케이션(SPA) 정적 핸들러, 플랫 디렉토리 검증기, 임베디드 웹 스튜디오 UI
* **Gemini 1차 분석**:
  - `..` 경로 탐색 방어, 숨김 파일 거부, 내장 웹 콘솔 UI 제공.
* **Claude 2차 교차 분석**:
  - **서브디렉토리를 포함하는 표준 웹 빌드 자산 경로 무조건 차단 결함 (`spa.go:33-36`)**:
    `isSafeFlatAsset`이 `strings.ContainsAny(clean, "/\\")` 조건을 적용하여 Vite/Webpack 등 표준 번들러가 생성하는 `assets/main.js`, `static/css/app.css`와 같은 서브디렉토리 에셋을 무조건 404로 거부함.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `pkg/api/spa.go:33-36` (`isSafeFlatAsset`)
  - **시나리오**: 사용자가 직접 빌드한 프론트엔드 정적 디렉토리를 바인딩했을 때 CSS/JS 번들이 로드되지 않고 화면이 깨짐.
  - **권장 수정안**: 서브디렉토리 전체 금지 대신 루트 디렉토리 외부로 벗어나는 디렉토리 탐색(`..`) 및 은닉 파일만 선별 거부하도록 완화.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 129] `cmd/lionsql/server.go`, `main.go`
* **대상 영역**: 서버 메인 엔트리포인트, CLI 플래그 파싱, 컴포넌트 오케스트레이션, Graceful Shutdown
* **Gemini 1차 분석**:
  - 멀티 프로토콜 리스너 바인딩, 구성 계층 적용, 시그널 트랩 구현.
* **Claude 2차 교차 분석**:
  - **고정 1.5초 셧다운 타임아웃 초과 시 스토리지 엔진 강제 종료 및 이중 Close 위험 (`server.go:886-917`)**:
    `ctx, cancel := context.WithTimeout(..., 1500*time.Millisecond)` 타임아웃 발생 시 메인 루틴이 즉각 리턴하여 백그라운드에서 진행 중이던 `eng.Close()`의 디스크 플러시가 도중에 잘릴 수 있으며, `defer tdsServer.Close()`와 셧다운 루틴에서 `tdsServer.Close()`가 중복 호출되어 이중 Close 위험 노출 (`[RULE:DoubleCloseSafety]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `cmd/lionsql/server.go:886-917` (`runServer`)
  - **시나리오**: 쓰기 트랜잭션이 많은 상태에서 SIGINT 수신 시 WAL 플러시가 완료되기 전 프로세스가 종료되어 복구 시간 증가.
  - **권장 수정안**: 스토리지 엔진의 `Close()`는 타임아웃 고루틴 밖에서 확정적으로 동기 대기하고, 리스너들에 `sync.Once`를 적용하여 이중 Close 방어.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 130] `cmd/lionsql/daemon.go`, `daemon_unix.go`, `daemon_windows.go`
* **대상 영역**: 백그라운드 데몬 프로세스 포크, PID 파일 수명주기, 데몬 상태 점검/종료/재시작
* **Gemini 1차 분석**:
  - OS별 시스템 호출 분리(`daemon_unix.go`, `daemon_windows.go`), `[RULE:ExecveSafety]` 준수.
* **Claude 2차 교차 분석**:
  - **부모 프로세스 로그 파일 디스크립터 누수 및 비원자적 PID 파일 저장 (`daemon.go:184, 204`)**:
    `startDaemon`에서 `logFd`를 연 후 부모 프로세스 종료 전 닫지 않으며, PID 파일을 `os.WriteFile`로 직접 덮어써 쓰기 도중 크래시 시 손상된 PID 파일이 남음 (`[RULE:AtomicSaves]`). 또한 PID 재활용 시 프로세스 이름 검증 없이 타 프로세스에 시그널을 보낼 위험 존재.
* **합의된 결함 (Defect)**:
  - **심각도**: `Medium`
  - **위치**: `cmd/lionsql/daemon.go:184, 204` (`startDaemon`)
  - **시나리오**: 데몬 기동 실패 또는 비정상 재시작 시 PID 파일이 비어 있거나 손상되어 `lionsql stop` 실패.
  - **권장 수정안**: PID 파일 저장을 원자적 임시파일 스왑(`[RULE:AtomicSaves]`)으로 변경하고 부모의 `logFd`를 즉시 닫음.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 131] `cmd/lionsql/daemon_windows.go`
* **대상 영역**: Windows 1ms 고해상도 멀티미디어 타이머(`winmm.dll`) 설정 및 스케줄러 튜닝
* **Gemini 1차 분석**:
  - Windows 기본 15.6ms 슬립 양자를 1ms로 극복하는 `timeBeginPeriod(1)` 호출 구현.
* **Claude 2차 교차 분석**:
  - **`timeEndPeriod` 클린 언로드 및 해제 훅 부재 (`daemon_windows.go:25-53`)**:
    `init()`에서 `timeBeginPeriod(1)`을 요청한 후, 프로세스 종료나 컴포넌트 언로드 시 `timeEndPeriod(1)`을 호출하는 정리 코드가 일체 호출되지 않음 (`[RULE:DoubleCloseSafety]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `cmd/lionsql/daemon_windows.go:25-53` (`timeBeginPeriod`, `timeEndPeriod`)
  - **시나리오**: Windows 환경에서 프로세스 종료 훅 부재로 인한 타이머 리소스 해제 불완전.
  - **권장 수정안**: 프로세스 셧다운 훅에 `timeEndPeriod(1)`을 `sync.Once`로 연결하여 대칭적 정리 보장.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 132] `cmd/lionsql/doctor.go`
* **대상 영역**: 자가 진단 유틸리티(`--doctor`), 호스트 환경, 파일시스템, 포트 바인딩, 엔진 자가 검증
* **Gemini 1차 분석**:
  - 6대 카테고리(런타임, 파일시스템, 네트워크, 스토리지, 파서/VDBE, 보안) 종합 진단 리포트 생성.
* **Claude 2차 교차 분석**:
  - **In-Memory OCC 엔진 진단 실패 시 `Close()` 누락으로 임시 디렉토리 핸들 잠김 (`doctor.go:210-230`)**:
    트랜잭션 시작이나 커밋이 실패할 경우 `memEng.Close()`가 호출되지 않아 Windows 환경에서 `defer os.RemoveAll(memTmp)` 시 파일 잠금 에러가 발생하여 진단 후 임시 폴더가 삭제되지 않고 방치됨 (`[RULE:HandleHygiene]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `cmd/lionsql/doctor.go:210-230` (`checkStorageEngines`)
  - **시나리오**: Windows에서 `--doctor` 실행 시 테스트 실패 시점에 임시 디렉토리 삭제 거부 오류 발생.
  - **권장 수정안**: `defer memEng.Close()`를 생성 직후 등록하여 예외 경로에서도 엔진 리소스 확정 해제.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 133] `cmd/lionsql/cli_config.go`, `sql.go`
* **대상 영역**: CLI 설정 조회/변경(`get`, `set`), 일회성 SQL 실행기(`sql`)
* **Gemini 1차 분석**:
  - 터미널 비밀번호 비표시 입력(`term.ReadPassword`), 테이블 서식 출력 지원.
* **Claude 2차 교차 분석**:
  - **비밀번호 입력 중 Ctrl+C 인터럽트 시 터미널 에코 모드 복구 부재 및 DB 락 충돌 안내 미흡 (`cli_config.go:116`, `sql.go:34-40`)**:
    `term.ReadPassword` 입력 도중 SIGINT가 들어오면 터미널 Raw 모드가 복구되지 않아 쉘이 깨질 위험이 있으며 (`[RULE:PanicRecovery]`), `lionsql sql` 실행 시 서버 데몬이 이미 실행 중이면 친절한 안내 없이 WiredTiger 락 에러로 즉시 실패함.
* **합의된 결함 (Defect)**:
  - **심각도**: `Low`
  - **위치**: `cmd/lionsql/cli_config.go:116`, `cmd/lionsql/sql.go:34-40`
  - **시나리오**: CLI에서 비밀번호 입력 도중 취소했을 때 사용자 터미널 입력이 화면에 보이지 않게 됨.
  - **권장 수정안**: 시그널 트랩을 통해 터미널 상태 복구 훅을 등록하고, 엔진 락 실패 시 "서버 가동 중 - HTTP/PGWire로 접속 권장" 안내 메시지 출력.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 134] `cmd/lionsql/migrate.go`, `pkg/migrate/migrate.go`
* **대상 영역**: 스키마 마이그레이션 CLI(`new`, `up`, `down`, `status`) 및 마이그레이션 엔진
* **Gemini 1차 분석**:
  - 타임스탬프 기반 양방향(`up`/`down`) 마이그레이션 버전 관리 및 `_migrations` 메타데이터 추적 양호.
* **Claude 2차 교차 분석**:
  - **다중 DDL/DML 마이그레이션 파일의 비원자적 실행 및 인라인 주석 파싱 훼손 (`migrate.go:255-263`, `410-420`)**:
    마이그레이션 파일 내의 여러 구문을 개별적으로 자동 커밋 실행하여 중간 구문 실패 시 이전 구문이 롤백되지 않고 데이터베이스가 반쯤 적용된 비정상 상태로 남음 (`[RULE:AtomicSaves]`). 또한 `cleanAndSplitSQL`이 줄 단위로 `--`를 단순 제거하여 멀티라인 문자열 리터럴 내부의 `--` 텍스트를 파괴함.
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `pkg/migrate/migrate.go:255-263`, `410-420` (`Up`, `cleanAndSplitSQL`)
  - **시나리오**: 3개 테이블을 만드는 마이그레이션 중 3번째 테이블 생성 실패 시 앞의 2개 테이블은 이미 영구 생성되어 재실행 불가.
  - **권장 수정안**: 단일 마이그레이션 파일 단위로 트랜잭션을 바인딩하고, 주석 제거는 `sqlutil`의 토크나이저를 활용하여 문자열 리터럴 보존.
* **최종 판정**: `DEFECT_LOGGED`

---

### [Scope 135] `cmd/bench/main.go`, `cmd/tpcc_runtime/main.go`, `cmd/soaktest/main.go`, `cmd/crashtest/main.go`
* **대상 영역**: 고동시성 OLTP 벤치마크, TPC-C 런타임, 침적 테스트(Soak), 강제 크래시 복구 검증기
* **Gemini 1차 분석**:
  - 멀티 워커 트랜잭션 부하 생성, 레이턴시 통계 집계, 단계별 무결성 검증 파이프라인 우수.
* **Claude 2차 교차 분석**:
  - **테스트 러너 강제 중단 시 자식 LionSQL 프로세스 고아화 누수 (`cmd/soaktest/main.go:116-160`, `cmd/crashtest/main.go:114-160`)**:
    `soaktest`와 `crashtest`가 백그라운드 자식 프로세스로 `lionsql` 서버를 스폰하지만 메인 러너에 SIGINT/SIGTERM 핸들러(`signal.Notify`)가 등록되어 있지 않아 사용자가 Ctrl+C로 중단할 때 자식 프로세스가 종료되지 않고 백그라운드에 남아 포트와 DB 락을 영구 점유함 (`[RULE:CleanTeardown]`).
* **합의된 결함 (Defect)**:
  - **심각도**: `High`
  - **위치**: `cmd/soaktest/main.go:116-160`, `cmd/crashtest/main.go:114-160`
  - **시나리오**: 개발자가 침적 테스트 도중 Ctrl+C를 눌렀으나 백그라운드에 `lionsql`이 계속 살아 있어 다음 빌드/테스트 시 포트 충돌 발생.
  - **권장 수정안**: `signal.Notify`를 등록하여 인터럽트 발생 시 스폰된 자식 프로세스에 즉각 `gracefulStop` 및 SIGKILL 에스컬레이션을 수행하도록 테어다운 보장.
* **최종 판정**: `DEFECT_LOGGED`

---

## 제2부. 2단계: 5대 핵심 코어 구역 심층 전수 재스캔 감사 기록부 (Phase 2: Deep Core Precision Rescan Findings)

> **핵심 원칙**: 1차 감사의 스코프당 1건 조기 종료 편향(Cadence/Early Termination Bias)을 완전 배제하고, 파일 끝까지 다중 결함($N$건)을 가감 없이 전수 발굴 및 누적 기록한다.

---

### [심층 구역 1] 스토리지 엔진 코어 (WiredTiger MVCC & In-Memory OCC Core)
* **대상 핵심 파일**:
  - `pkg/storage/wiredtiger/wt_engine.go` (2,664 라인)
  - `pkg/storage/memengine/memengine.go` (455 라인)
  - `pkg/storage/memengine/tx.go` (2,418 라인)
  - `pkg/storage/memengine/persistence.go` (391 라인)
  - **총 분석 규모**: 5,928 라인
* **중점 점검 영역**: 체크포인트 도중 커밋 역전, AOF 컴팩션 시 데이터 유실, 롤백 시 더티 슬롯 및 세션 잔류, 스냅샷 격리 파괴, Use-After-Free 크래시, 인덱스 부분 반영 원자성 훼손.

#### [발굴 결함 1-1] `memengine/persistence.go`: 비동기 AOF Rewrite Compaction 시 동시 트랜잭션 데이터 영구 유실 및 락 미보호 Data Race
* **심각도**: `Critical`
* **위치**: `pkg/storage/memengine/memengine.go:195-204`, `pkg/storage/memengine/persistence.go:270-283`, `332-338`
* **현상 및 원인 분석**:
  - `finishWALCommitLocked`에서 AOF 로그 파일이 `aofThreshold`를 초과하면 백그라운드 고루틴(`go func()`)을 생성하여 `e.rewriteAOFLocked()`를 비동기로 실행함.
  - **결함 A (Data Race & Panic)**: 비동기 고루틴 내부에서 `e.mu`를 전혀 획득하지 않고 `saveDiskLocked()`를 호출함. `saveDiskLocked()`는 `e.tables` 맵을 직접 range 순회하는데, 동시 실행 중인 DDL(`CreateTable`, `DropTable`)이 `e.mu.Lock()` 상태에서 `e.tables`를 수정할 경우 Go 런타임의 `fatal error: concurrent map iteration and map write` 패닉이 발생함.
  - **결함 B (Silent Data Loss - 치명적 데이터 영구 유실)**:
    1. 비동기 고루틴이 $T_1$ 시점에 `saveDiskLocked()`로 디스크 스냅샷(`tables.mpk`)을 덤프함.
    2. 스냅샷 생성 중에도 메인 엔진의 트랜잭션들은 계속 커밋되며 `e.wal.writeFrame()`을 통해 AOF에 새 로그를 기록함.
    3. 스냅샷 덤프가 끝나자마자 279라인에서 `e.wal.Truncate()`를 무조건 호출하여 AOF 파일을 0바이트로 잘라버림.
    4. **결과**: $T_1$ 스냅샷 생성 이후부터 `Truncate()` 직전까지 커밋된 모든 트랜잭션의 데이터는 $T_1$ 스냅샷에도 없고, AOF 로그에서도 영구 삭제되어 크래시 발생 시 완전히 증발함 (`[RULE:AtomicSaves]`).
* **권장 수정안**:
  - AOF Rewrite 시 단순히 Truncate하지 않고, 스냅샷 시작 시점의 LSN을 캡처한 후 스냅샷 생성 중에 추가된 델타 로그를 유지하거나 Redis처럼 AOF rewrite buffer를 두어 스왑해야 함. 또한 `rewriteAOF` 진입 및 스냅샷 복사 시 `e.mu.RLock()`으로 `e.tables`의 일관된 스냅샷 뷰를 복제한 후 디스크 I/O를 수행해야 함.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 1-2] `memengine/tx.go`: Commit 6단계 인덱스 갱신 실패 시 부분 적용(Partial Apply) 롤백 부재로 인한 인메모리 영구 불일치
* **심각도**: `High`
* **위치**: `pkg/storage/memengine/tx.go:1977-1980` (`Commit`)
* **현상 및 원인 분석**:
  - `tx.Commit()` 6단계에서 실제 테이블 샤드 엔트리(`sh.entries[ch.key]`)에 새 버전을 반영하고 이전 인덱스 엔트리를 삭제한 후, `idxData.Insert(newKey, ch.key, incVals)`를 호출함.
  - 만약 `idxData.Insert`에서 유니크 인덱스 충돌이나 정합성 오류로 에러(`err != nil`)가 반환되면, 아무런 복구 로직 없이 `return err`로 함수를 즉각 탈출함.
  - `defer unlockAll()`로 락은 해제되지만, 이미 `sh.entries`에는 새로운 행 데이터와 증가된 버전이 반영되어 있고, 이전 인덱스 엔트리는 지워진 채 새 인덱스는 추가되지 않은 **반쯤 적용된(Half-Applied) 불일치 상태**가 인메모리에 영구 잔류함 (ACID Atomicity 파괴, `[RULE:AtomicSaves]`).
* **권장 수정안**:
  - 5단계 유니크 사전 검증을 완벽히 수행하여 6단계 진입 후에는 실패하지 않는 no-fail contract를 확립하거나, 6단계 인덱스 반영 실패 시 이미 변경된 `sh.entries` 및 선행 인덱스 엔트리를 원래대로 복원하는 역연산(Undo) 핸들러를 바인딩해야 함.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 1-3] `wiredtiger/wt_engine.go`: 트랜잭션 Commit 실패(`WT_ROLLBACK`) 시 세션 리셋 누락으로 인한 더티 상태 잔류
* **심각도**: `High`
* **위치**: `pkg/storage/wiredtiger/wt_engine.go:2318-2323` (`Commit`)
* **현상 및 원인 분석**:
  - `ret := C.go_wt_session_commit(tx.sess.sess, nil)`을 실행한 후, `ret != 0`인 경우 2320라인에서 `return wtOpError(...)`로 즉시 반환함.
  - 세션 리소스를 정리하고 WiredTiger 내부 상태를 정상화하는 `C.go_wt_session_reset(tx.sess.sess)`(2322라인)은 오직 커밋 성공(`ret == 0`) 시에만 호출됨.
  - WiredTiger에서 커밋 충돌(`WT_ROLLBACK`) 등으로 커밋이 실패했을 때 `session->reset`을 호출하지 않으면 세션에 미해제된 내부 락이나 트랜잭션 컨텍스트가 더티하게 잔류하여, 해당 세션을 재사용하는 후속 트랜잭션에서 `WT_SESSION::begin_transaction`이 연쇄 실패하거나 스윕 서버(Sweep Server)가 세션을 강제 종료시키는 결함 발생.
* **권장 수정안**:
  ```go
  ret := C.go_wt_session_commit(tx.sess.sess, nil)
  _ = C.go_wt_session_reset(tx.sess.sess) // 에러 여부와 무관하게 무조건 세션 리셋 보장
  if ret != 0 {
      return wtOpError("commit_transaction", int(ret))
  }
  ```
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 1-4] `wiredtiger/wt_engine.go`: DropTable/TruncateTable 시 활성 커서 보유 고루틴 Use-After-Free 크래시
* **심각도**: `High`
* **위치**: `pkg/storage/wiredtiger/wt_engine.go:555-570`, `1123-1135` (`DropTable`, `evictCursors`)
* **현상 및 원인 분석**:
  - `DropTable`에서 `e.evictAllCursors`를 호출하지만, `evictCursors`는 유휴 커서 풀(`cursorPool`)에 대기 중인 커서만 `toClose`로 이동시킬 뿐, 현재 동시 실행 중인 트랜잭션/반복자가 열어두고 읽고 있는 활성 커서(`allCursors`)는 대기하거나 안전하게 닫지 못함.
  - 이후 569라인에서 `C.go_wt_session_drop(sess, uri)`("force=true")가 즉각 호출되어 WiredTiger C 레벨에서 해당 테이블 B-Tree 파일과 메타데이터를 강제로 삭제함.
  - 동시에 해당 테이블을 스캔하고 있던 다른 세션의 Go 고루틴이 활성 `WT_CURSOR*`의 `next()`나 `get_val()`을 호출하는 순간 해제된 C 구조체를 역참조(Use-After-Free)하여 프로세스가 즉시 SIGSEGV로 비정상 종료됨 (`[RULE:PanicRecovery]`, `[RULE:CleanTeardown]`).
* **권장 수정안**:
  - 테이블 드롭/트렁케이트 전 해당 테이블에 활성 커서가 존재하는지 참조 카운트(Ref Counter)를 검사하고, 진행 중인 읽기 트랜잭션이 완료될 때까지 안전하게 대기(Graceful Drain)하거나 취소 에러를 명시적으로 전달하는 보호 메커니즘 구축.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 1-5] `memengine/tx.go`: 정상 Commit 트랜잭션의 `txPool` 반환 누락으로 인한 풀링 무효화 및 GC 압박
* **심각도**: `Medium`
* **위치**: `pkg/storage/memengine/tx.go:185-191`, `2068-2080`
* **현상 및 원인 분석**:
  - `memengine`은 트랜잭션 할당 오버헤드를 0으로 만들기 위해 `txPool = sync.Pool{...}`을 정의하고 `releaseToPoolLocked()`를 구현함.
  - 그러나 `releaseToPoolLocked()`는 `Rollback()`(2079라인)에서만 호출될 뿐, 워크로드의 99% 이상을 차지하는 `Commit()`(2068라인) 성공 경로에서는 어디서도 호출되지 않고 그냥 리턴됨.
  - 결과적으로 성공 커밋된 모든 `Transaction` 구조체와 내부 맵들이 재활용되지 못하고 GC 가비지로 버려져, `sync.Pool` 도입 목적이 무력화되고 초당 수만 건의 OLTP 처리 시 불필요한 GC Pause 지터가 유발됨 (`[RULE:ZeroAllocation]`).
* **권장 수정안**:
  - `Commit()` 성공 완료 직전(또는 호출자가 LSN 조회를 마친 후 재사용 가능하도록) 트랜잭션 상태를 `clear()`하고 `releaseToPoolLocked()`를 정상 호출하여 풀로 회수.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 1-6] `wiredtiger/wt_engine.go`: `opts.SnapshotLSN` / `opts.SnapshotTimestamp` C API 미전달로 인한 스냅샷 격리 불일치
* **심각도**: `Medium`
* **위치**: `pkg/storage/wiredtiger/wt_engine.go:1280-1299` (`BeginTx`)
* **현상 및 원인 분석**:
  - `BeginTx(opts)`에서 호출자가 지정한 `opts.SnapshotLSN` 및 `opts.SnapshotTimestamp`를 Go 구조체 필드에 저장하지만, 정작 WiredTiger C API 호출인 `C.go_wt_session_begin(s.sess, nil)`에는 `nil` 설정을 전달함.
  - WiredTiger가 타임스탬프 기반 MVCC 읽기를 지원하려면 `read_timestamp=<hex>` 설정 문자열을 `begin_transaction`에 전달해야 함.
  - 타임스탬프 설정이 누락되어 WiredTiger 엔진은 과거 시점의 스냅샷 대신 트랜잭션 시작 순간의 최신 상태를 읽게 되며, HA 복제 스트림 리플레이나 PITR 시점 복원 쿼리 시 엔진 간 격리 수준 불일치 발생.
* **권장 수정안**:
  - `opts.SnapshotTimestamp`가 명시된 경우 WiredTiger 타임스탬프 형식(`read_timestamp=%x`)의 설정 문자열을 생성하여 `go_wt_session_begin`에 전달.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 1-7] `memengine/tx.go`: Savepoint 동일 이름 재정의 시 `spNames` 중복 및 롤백 인덱스 정합성 결함
* **심각도**: `Low`
* **위치**: `pkg/storage/memengine/tx.go:2107-2117`, `2149-2154`
* **현상 및 원인 분석**:
  - `Savepoint` 생성 시 기존에 동일한 이름이 있으면 `tx.savepoints[normName]`을 덮어쓰지만, `tx.spNames` 슬라이스에서는 기존 위치를 그대로 두고 `found`로 처리함.
  - 이후 `RollbackTo` 또는 `ReleaseSavepoint` 실행 시 첫 번째 매칭되는 `idx`를 찾아 그 뒤의 세이브포인트들을 슬라이스에서 잘라냄(`tx.spNames = tx.spNames[:idx]`).
  - 만약 동일 이름이 재사용되었을 경우 실제 세이브포인트 생성 순서와 `spNames`의 물리적 인덱스 순서가 뒤틀려, 나중에 정의된 세이브포인트가 조기 삭제되거나 이전 세이브포인트가 잔류하는 정합성 왜곡 가능.
* **권장 수정안**:
  - 동일한 이름의 세이브포인트가 재정의되면 기존 인덱스 위치에서 제거하고 가장 마지막 위치에 새로 추가하거나 표준 LIFO 스택 구조로 일원화 관리.
* **판정**: `DEFECT_LOGGED`

---

### [심층 구역 2] 트랜잭션 코디네이터 & 세션 풀 (Transaction Coordinator & Session Pool)
* **대상 핵심 파일**:
  - `pkg/engine/coordinator.go` (83 라인)
  - `pkg/engine/coordinator_impl.go` (641 라인)
  - `pkg/engine/session.go` (532 라인)
  - `pkg/engine/session_pool.go` (243 라인)
  - **총 분석 규모**: 1,499 라인
* **중점 점검 영역**: 세션 다중화 누수, 동시성 락 계층 위반, Autocommit CGo 동시성 레이스, 슬로우 쿼리 전역 블로킹, 팬텀 임시 테이블 누적.

#### [발굴 결함 2-1] `engine/coordinator_impl.go` & `engine/session_pool.go`: `SessionPool`의 1:1 영구 바인딩으로 인한 세션 다중화 무효화 및 유휴 연결 폭증 시 WiredTiger 세션 고갈
* **심각도**: `High`
* **위치**: `pkg/engine/coordinator_impl.go:308-317`, `pkg/engine/session_pool.go:148-179` (`GetOrCreateSession`, `AcquireSession`)
* **현상 및 원인 분석**:
  - `SessionPool`은 "수천 개의 클라이언트 연결에 대해 스토리지 엔진 세션을 다중화(Multiplexing)"하도록 설계되었음.
  - 그러나 `Coordinator.GetOrCreateSession`은 클라이언트 연결이 맺어지는 순간 `c.pool.AcquireSession()`을 호출하여 스토리지 세션을 1:1로 할당받은 뒤 `cs.Session`에 영구 바인딩함. 이 세션은 클라이언트가 완전히 접속을 끊을 때(`CloseSession`)까지 절대 풀에 반환되지 않음.
  - 실제 쿼리 실행 제어는 `AcquireWorker(ctx)`(고루틴 슬롯)로만 제한되고, 물리적 스토리지 세션은 유휴(Idle) 상태인 수천 개의 연결에 1:1로 묶여 있음.
  - **결과**: WiredTiger의 기본 엔진 세션 상한(`session_max=2000`)에 도달하면, 비록 쿼리를 수행하지 않는 유휴 연결이더라도 신규 접속이 즉시 실패하여 연결 거부 및 DoS 상태가 초래됨.
* **권장 수정안**:
  - 트랜잭션이 없는 일반 쿼리(Autocommit)의 경우 `ExecuteProgram` 또는 쿼리 실행 직전에만 `SessionPool.AcquireSession()`을 대여받고 쿼리 완료 후 즉시 `ReleaseSession()`하도록 풀러 다중화 라이프사이클을 정상화해야 함.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 2-2] `engine/session.go`: Autocommit 모드 실행 시 `cs.Mu` 조기 언락으로 인한 동일 세션 동시 쿼리 `WT_SESSION` CGo Data Race
* **심각도**: `High`
* **위치**: `pkg/engine/session.go:367-386` (`ExecuteProgram`)
* **현상 및 원인 분석**:
  - 트랜잭션이 열려 있지 않은 Autocommit 모드에서 `cs.Session != nil`일 때, 세션 포인터를 `sess = cs.Session`으로 복사한 뒤 379라인에서 `cs.Mu.Unlock()`을 호출하여 락을 조기에 풀어버림.
  - 이후 385라인에서 락이 풀린 상태로 `exec.RunContext(ctx, prog, stmtLabel, paramMap, sess)`를 실행함.
  - 만약 HTTP API나 파이프라인 PGWire 프로토콜을 통해 동일 세션(`ClientSession`)으로 두 개 이상의 비동기 쿼리가 동시에 도달할 경우, 스레드 세이프하지 않은 단일 WiredTiger C 세션 핸들(`WT_SESSION*`)을 복수의 Go 워커 스레드가 동시 조작하게 됨.
  - **결과**: WiredTiger C 엔진 내부의 메모리 오염 또는 `concurrent call to session` 패닉 및 프로세스 SIGSEGV 비정상 종료 유발 (`[RULE:PanicRecovery]`, `[RULE:ConcurrencySafety]`).
* **권장 수정안**:
  - Autocommit 실행 시 세션 뮤텍스를 쿼리 완료 시까지 유지하거나, 동시 쿼리 유입 시 별도의 임시 세션을 풀에서 할당받아 실행하도록 동시성 분기 격리.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 2-3] `engine/coordinator_impl.go`: `GetActiveQueries()`의 광역 RLock 유지 및 개별 세션 락 순회로 인한 전역 Cascading Stall 위험
* **심각도**: `Medium`
* **위치**: `pkg/engine/coordinator_impl.go:529-548` (`GetActiveQueries`)
* **현상 및 원인 분석**:
  - `GetActiveQueries()`는 실행 중인 쿼리 목록을 수집하기 위해 `c.sessMu.RLock()`을 획득한 상태에서 `for _, cs := range c.sessions` 루프를 돌며 각 세션의 `cs.Mu.Lock()`을 순차적으로 획득함.
  - 만약 특정 세션이 무거운 락이나 장기 슬로우 쿼리로 인해 `cs.Mu.Lock()`을 오래 쥐고 있다면, `c.sessMu.RLock()`이 장시간 반환되지 않고 묶이게 됨.
  - 이때 신규 클라이언트 연결 생성(`GetOrCreateSession`)이나 연결 종료(`CloseSession`)가 호출되면 `c.sessMu.Lock()`(Writer)을 요청하게 됨. Go의 `sync.RWMutex` 구현은 대기 중인 Writer가 있을 경우 이후 들어오는 모든 Reader(`GetSession`, `CancelSessionQuery` 등)를 대기열에 세움.
  - **결과**: 단 하나의 세션 지연이 전체 코디네이터의 세션 조회 및 취소 API를 전면 마비시키는 연쇄 정체(Cascading Stall) 발생 (`[RULE:LockHierarchy]`).
* **권장 수정안**:
  - `c.sessMu.RLock()` 상태에서는 대상 세션 포인터들의 슬라이스만 즉시 복제하여 RLock을 0ms 내에 반환하고, 복제된 슬라이스를 순회하면서 개별 `cs.Mu.Lock()`을 획득하도록 락 스코프를 축소해야 함.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 2-4] `engine/session_pool.go`: 풀 내부 락(`p.mu`)을 쥔 상태에서 외부 CGo 블로킹 `sess.Close()` 호출로 인한 데드락 위험
* **심각도**: `Medium`
* **위치**: `pkg/engine/session_pool.go:159-161`, `197-199` (`AcquireSession`, `ReleaseSession`)
* **현상 및 원인 분석**:
  - `AcquireSession`에서 유휴 TTL이 만료된 세션을 닫거나 `ReleaseSession`에서 유휴 풀이 가득 찼을 때, 풀 내부 뮤텍스(`p.mu.Lock()`)를 획득한 상태에서 `_ = sess.Close()`를 직접 동기 호출함.
  - WiredTiger 엔진의 `sess.Close()`는 CGo를 경유하여 엔진 레벨의 `sessMu` 및 WiredTiger C 내부 연결 락을 획득함.
  - 엔진 셧다운이나 백그라운드 리셋 등 다른 고루틴이 엔진 락을 잡고 풀을 조회하는 경로와 맞물릴 경우 엄격한 락 순서가 뒤집혀 데드락 발생 위험 존재 (`[RULE:LockHierarchy]`).
* **권장 수정안**:
  - 닫아야 할 세션 포인터를 로컬 변수로 빼내고 `p.mu.Unlock()`을 먼저 수행한 뒤 락 밖에서 `sess.Close()`를 실행.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 2-5] `engine/session.go`: 존재하지 않는 임시 테이블 단순 참조 시 `TempTableMap` 팬텀 슬롯 메모리 누적
* **심각도**: `Low`
* **위치**: `pkg/engine/session.go:188-210` (`ResolveTempTableName`)
* **현상 및 원인 분석**:
  - `#temp` 접두사를 가진 테이블명이 쿼리 문맥에 등장할 때마다 `ResolveTempTableName`이 호출됨.
  - 이 함수는 실제로 `CREATE TABLE #foo`가 성공했는지 여부를 따지지 않고, 참조된 적 없는 `#foo`에 대해 물리 이름(`__temp_<sessID>_foo`)을 생성하여 `cs.TempTableMap`과 `cs.TempTables` 슬라이스에 즉시 등록함.
  - 사용자가 오타나 잘못된 `SELECT * FROM #not_exist`를 반복 실행할 경우 실제 존재하지도 않는 팬텀 임시 테이블명이 세션 메모리에 무한 누적되며, 세션 종료(`CloseSession`) 시 수백 건의 무의미한 `DropTable` 시스템 콜 실패를 유발함.
* **권장 수정안**:
  - 단순 이름 해석(`Resolve`)과 실제 임시 테이블 등록(`RegisterTempTable`)의 책임을 분리하여, 실제 DDL 성공 시에만 `TempTables`에 추적 등록되도록 개선.
* **판정**: `DEFECT_LOGGED`

---

### [심층 구역 3] 바이트코드 컴파일러 & VDBE VM (Bytecode Compiler & VDBE VM)
* **대상 핵심 파일**:
  - `pkg/proc/compiler.go` (1,506 라인)
  - `pkg/proc/runtime/vm.go` (2,724 라인)
  - `pkg/proc/runtime/vm_dispatcher.go` (1,793 라인)
  - **총 분석 규모**: 6,023 라인
* **중점 점검 영역**: 중첩 트랜잭션(`@@TRANCOUNT`) 무시 조기 커밋, 음수 레지스터 인덱싱 패닉, 트리거 재귀 한도 초과 침묵 에러, UDF 무한 재귀 스택 오버플로우, 커서 무보호 타입 단언 크래시.

#### [발굴 결함 3-1] `runtime/vm_dispatcher.go`: `OpTxCommit`의 중첩 트랜잭션(`@@TRANCOUNT`) 무시 및 무조건 물리 커밋으로 인한 원자성 파괴
* **심각도**: `Critical`
* **위치**: `pkg/proc/runtime/vm_dispatcher.go:536-552` (`OpTxCommit`)
* **현상 및 원인 분석**:
  - `OpTxBegin`은 중첩 트랜잭션 진입 시 단순히 `f.tranCount++`만 수행함.
  - 그러나 `OpTxCommit`은 `f.tranCount`가 1보다 큰 중첩 상태(`f.tranCount > 1`)임에도 불구하고 `f.tranCount--`와 함께 무조건 `f.tx.Commit()`을 실행하여 물리 트랜잭션을 디스크에 조기 커밋해버림.
  - **영향**: T-SQL 표준 명세상 내부 `COMMIT`은 `@@TRANCOUNT`만 1 감소시키고 최외곽 `COMMIT`(`tranCount == 0`) 시점에만 물리 커밋이 일어나야 함. LionSQL은 내부 블록 커밋 시 즉시 디스크에 커밋해버리므로, 이후 외부 블록에서 오류로 인해 `ROLLBACK TRANSACTION`이 발생해도 이미 커밋된 데이터를 되돌릴 수 없는 **심각한 ACID Atomicity 파괴 및 데이터 영구 오염**이 발생함.
  - 추가로, 커밋 직후 `f.session.BeginTx()`로 새로 생성된 `newTx`가 `ClientSession.Tx`에 반영되지 않아, 프로시저 종료 후 세션의 트랜잭션 포인터가 이미 닫힌 이전 `tx`를 가리켜 이후 모든 SQL 실행 시 `ErrTxClosed` 에러가 발생함.
* **권장 수정안**:
  - `OpTxCommit` 실행 시 `f.tranCount == 1`일 때에만 `f.tx.Commit()`을 수행하고, `f.tranCount > 1`일 때는 카운터만 감소시키도록 수정. 또한 트랜잭션 스왑 시 `ClientSession.Tx`를 항상 동기화해야 함.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 3-2] `runtime/vm.go` & `runtime/vm_dispatcher.go`: `callProc`, `callUDF`, `OpCallBuiltin`의 음수 레지스터 인덱스(`regIdx = -1`) 슬라이스 범위 초과 패닉
* **심각도**: `High`
* **위치**: `pkg/proc/runtime/vm.go:2220-2222`, `2254-2257`, `pkg/proc/runtime/vm_dispatcher.go:1491-1493`
* **현상 및 원인 분석**:
  - 프로시저, UDF, 내장 함수 호출 시 인자 레지스터 목록(`ArgRegs`)을 순회하며 `if int(regIdx) < len(f.regs)` 형태로 상한선만 검사하고, 하한선(`regIdx >= 0`)을 전혀 검증하지 않음.
  - 컴파일러가 기본값이나 옵셔널 파라미터, 또는 바인딩되지 않은 슬롯에 대해 `-1`을 센티넬 값으로 전달할 경우, `-1 < len(f.regs)`는 `true`로 통과됨.
  - **결과**: `f.regs[regIdx]`를 직접 역참조하여 `panic: runtime error: index out of range [-1]`이 발생하고 VM 실행 스레드가 즉각 크래시됨 (`[RULE:DefensiveIndexing]`).
* **권장 수정안**:
  - 모든 레지스터 참조부에 `if regIdx >= 0 && int(regIdx) < len(f.regs)` 방어적 경계 검사를 필수 적용.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 3-3] `runtime/vm.go`: `fireTriggers` 재귀 깊이 초과(32) 시 에러 침묵(`return nil`)으로 인한 비즈니스 트리거 무음 스킵
* **심각도**: `High`
* **위치**: `pkg/proc/runtime/vm.go:1134-1136` (`fireTriggers`)
* **현상 및 원인 분석**:
  - 상호 연쇄 트리거나 재귀 트리거가 실행되어 `f.callDepth >= 32`에 도달했을 때, 재귀 한도 초과 에러(Max Trigger Recursion Exceeded)를 호출자에게 전파하지 않고 침묵하며 `return nil`을 반환함.
  - **결과**: 트리거 실행이 아무런 에러 메시지나 경고 없이 조용히 중단(무음 실패)되어, 감사 로그 기록, 재고 정합성 차감, 외래키 카스케이드 처리 등이 조용히 누락되는 심각한 데이터 정합성 왜곡 초래 (`[RULE:ExplicitError]`).
* **권장 수정안**:
  ```go
  if f.callDepth >= 32 {
      return fmt.Errorf("maximum trigger recursion depth (32) exceeded on table %q", tableName)
  }
  ```
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 3-4] `runtime/vm.go`: `callUDF` 함수 호출 시 재귀 깊이(`callDepth`) 미검사로 인한 무한 재귀 스택 고갈
* **심각도**: `High`
* **위치**: `pkg/proc/runtime/vm.go:2207-2234` (`callUDF`)
* **현상 및 원인 분석**:
  - `callProc`은 `f.callDepth >= 64` 가드를 두고 재귀를 통제하고 있으나, 스칼라 UDF를 실행하는 `callUDF`는 `f.callDepth`를 전혀 검사하지 않고 새로운 프레임을 생성하여 무제한 호출함.
  - 사용자가 자기 자신을 재귀 호출하는 팩토리얼/피보나치 UDF나 상호 재귀 UDF를 작성할 경우, Go 고루틴 스택과 힙 메모리가 무제한 팽창하여 프로세스 OOM 크래시 발생 (`[RULE:BoundedRecursion]`).
* **권장 수정안**:
  - `callUDF` 진입 시 `if f.callDepth >= 64` 검사를 추가하고, 서브 프레임 생성 시 `subFrame.callDepth = f.callDepth + 1`을 바인딩하여 재귀 상한을 엄격히 강제.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 3-5] `runtime/vm_dispatcher.go`: 커서 Opcode(`OpCursorClose`, `OpCursorDealloc`, `OpCursorFetch`)의 무보호 Type Assertion 패닉
* **심각도**: `Medium`
* **위치**: `pkg/proc/runtime/vm_dispatcher.go:1438`, `1471`, `1481`
* **현상 및 원인 분석**:
  - `OpCursorOpen`은 `switch data := f.aux(&inst).(type)`로 안전하게 타입 검사를 수행하지만, `OpCursorFetch`, `OpCursorClose`, `OpCursorDealloc`은 `f.aux(&inst).(*proc.CursorData)` 또는 `f.aux(&inst).(string)`으로 무보호 강제 타입 변환을 수행함.
  - 손상된 바이트코드나 Aux 데이터 슬롯 불일치 시 `interface conversion: interface {} is nil, not string` 패닉이 발생하여 VM 프레임이 강제 중단됨 (`[RULE:PanicRecovery]`).
* **권장 수정안**:
  - `data, ok := f.aux(&inst).(string)` 2-value 타입 단언을 적용하여 타입 불일치 시 패닉 대신 명시적 런타임 에러 반환.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 3-6] `runtime/vm_dispatcher.go`: 이미 열린 커서 재 `OPEN` 및 닫힌 커서 `CLOSE` 시 표준 T-SQL 에러 미발생
* **심각도**: `Low`
* **위치**: `pkg/proc/runtime/vm_dispatcher.go:1418-1435`, `1470-1476`
* **현상 및 원인 분석**:
  - T-SQL 표준 명세에서는 이미 열린 커서에 `OPEN`을 호출하면 "Cursor is already open" 에러가 발생해야 하지만, LionSQL은 이를 무시하고 테이블을 다시 스캔하여 인덱스를 0으로 초기화함.
  - 반대로 이미 닫힌 커서에 `CLOSE`를 호출해도 "Cursor is not open" 에러를 내지 않고 조용히 성공 처리함.
* **권장 수정안**:
  - `cur.isOpen` 상태 플래그를 검사하여 중복 OPEN 시 `ErrCursorAlreadyOpen`, 미오픈 CLOSE 시 `ErrCursorNotOpen`을 표준 반환하도록 보정.
* **판정**: `DEFECT_LOGGED`

---

### [심층 구역 4] CBO 플래너 & 실행 오퍼레이터 (CBO Planner & Volcano Operators Core)
* **점검 대상 파일**: `pkg/planner/planner.go`, `pkg/execution/join.go`, `pkg/execution/sort.go`, `pkg/execution/builder.go` (총 4,887 라인 전수 스캔)
* **핵심 분석 초점**: Zero-Division NaN 계산 왜곡, External Sort 디스크 파일 누수, Join Iterator 자원 누수, Min-Heap 스트림 디코딩 무음 에러, 인덱스 컬럼 슬라이스 인덱싱 패닉.

#### [발굴 결함 4-1] `planner/planner.go`: 빈 테이블의 유니크 인덱스 비용 계산 시 Zero-Division `1.0 / 0.0 = +Inf` 나눗셈으로 인한 `estRows = NaN -> 0` 비용 왜곡
* **심각도**: `Critical`
* **위치**: `pkg/planner/planner.go:1482`, `1604` (`calculateIndexCost`)
* **현상 및 원인 분석**:
  - 테이블이 비어 있거나 카디널리티가 0인 상태(`estimatedTotal == 0`)에서 유니크 인덱스나 동등 조건 비용을 산출할 때 `selectivity = 1.0 / float64(estimatedTotal)` 연산을 수행함.
  - Go 부동소수점 연산에서 `1.0 / 0.0`은 런타임 패닉을 내지 않고 `+Inf`를 반환하며, 이후 `estRows = selectivity * float64(estimatedTotal)`는 `+Inf * 0.0 = NaN`이 됨.
  - **결과**: `math.IsNaN(estRows)` 상태에서 비용 누적 비교 시 모든 부등호 연산(`<`, `>`)이 `false`로 평가되어, 최적의 유니크 인덱스 대신 전체 테이블 풀 스캔(Full Table Scan) 플랜이 무조건 선택되는 심각한 CBO 플래너 선택 오류 초래.
* **권장 수정안**:
  - `if estimatedTotal <= 0 { estimatedTotal = 1 }` 방어 로직을 배치하여 0 나누기 원천 차단.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 4-2] `execution/sort.go`: Spill to Disk 도중 I/O 에러 또는 중단 시 생성된 임시 런 파일(`/tmp/lionsql-sort-run-*.tmp`) 및 파일 디스크립터 영구 누수
* **심각도**: `High`
* **위치**: `pkg/execution/sort.go:181-209`, `303-317` (`spillToDisk`, `Open`, `Close`)
* **현상 및 원인 분석**:
  - External Merge Sort 수행 중 메모리 한도 초과로 `/tmp/lionsql-sort-run-*.tmp` 파일을 생성하고 청크를 기록할 때, 도중에 디스크 Full이나 컨텍스트 취소로 에러가 반환되면 이미 생성된 `s.runFiles`의 일부가 닫히지 않고 삭제되지도 않음 (`[RULE:HandleHygiene]`).
  - 또한 `SortIterator.Open()` 실패 시 `Close()`가 호출되지 않으면 파일 디스크립터와 디스크 파일이 영구히 잔류하여 OS `/tmp` 파티션을 고갈시킴.
* **권장 수정안**:
  - `spillToDisk` 실패 시 생성된 파일들을 즉시 닫고 `os.Remove`하는 롤백 핸들러 배치. `SortIterator.Close()`에서 `s.runFiles` 및 `s.runReaders`를 `defer`를 통해 원자적으로 전부 닫고 언링크(`os.Remove`)하도록 보장.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 4-3] `execution/join.go`: `HashJoinIterator.Close()`에서 `h.left.Close()`만 호출하고 `h.right.Close()` 누락으로 인한 빌드 측 이터레이터 리소스 영구 누수
* **심각도**: `High`
* **위치**: `pkg/execution/join.go:386-400` (`HashJoinIterator.Close`)
* **현상 및 원인 분석**:
  - `HashJoinIterator.Close()` 구현에서 탐색 측(Probe) 하위 이터레이터인 `h.left.Close()`는 호출하지만, 빌드 측(Build) 하위 이터레이터인 `h.right.Close()` 호출이 완전히 누락됨.
  - 빌드 측 하위 트리가 테이블 스캔 커서나 정렬 임시 파일, 인덱스 락을 쥐고 있는 경우, 부모 쿼리가 종료되어도 하위 리소스가 결코 해제되지 않고 누수됨 (`[RULE:HandleHygiene]`).
* **권장 수정안**:
  ```go
  func (h *HashJoinIterator) Close() error {
      var errLeft, errRight error
      if h.left != nil {
          errLeft = h.left.Close()
      }
      if h.right != nil {
          errRight = h.right.Close()
      }
      if errLeft != nil { return errLeft }
      return errRight
  }
  ```
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 4-4] `execution/sort.go`: `SortIterator.NextChunk`의 Min-Heap 디코딩 중 I/O 에러(`err != io.EOF`)를 무음 삼키고 조기 종료하여 정렬 데이터 손실
* **심각도**: `Medium`
* **위치**: `pkg/execution/sort.go:353-356` (`SortIterator.NextChunk`)
* **현상 및 원인 분석**:
  - 임시 파일 런들을 병합(K-way Merge)하는 Min-Heap 스트림 디코딩 과정에서 `dec.Decode(&row)` 시 에러가 발생했을 때, `if err == io.EOF`뿐만 아니라 일반 I/O 에러나 디스크 손상 에러도 무조건 해당 런의 정상 종료로 간주하고 무음 삼켜버림 (`[RULE:ExplicitError]`).
  - **결과**: 디스크 읽기 실패 시 에러가 보고되지 않고 쿼리 결과에서 중간 레코드 수천~수만 건이 누락된 채 조기 리턴되는 심각한 데이터 손실(Data Loss) 발생.
* **권장 수정안**:
  - `if err != nil && err != io.EOF { return nil, fmt.Errorf("sort merge decode run file failed: %w", err) }`로 명시적 분기 및 에러 전파.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 4-5] `planner/planner.go`: Nested Loop Join 계획 수립 시 표현식 인덱스에 대한 `innerIndex.Columns[0]` 무검사 접근 슬라이스 패닉
* **심각도**: `Medium`
* **위치**: `pkg/planner/planner.go:2984` (`planNestedLoopJoin`)
* **현상 및 원인 분석**:
  - 최적의 Join 인덱스를 매칭하는 과정에서 인덱스 컬럼 목록의 길이를 확인하지 않고 `col := innerIndex.Columns[0]`을 직접 역참조함.
  - 함수 기반 표현식 인덱스(Expression Index)나 가상 컬럼 인덱스의 경우 `Columns`가 비어 있고 표현식 트리가 등록되어 있을 수 있음.
  - **결과**: `len(innerIndex.Columns) == 0`인 인덱스가 카탈로그에 존재할 경우 `panic: runtime error: index out of range [0]` 발생으로 쿼리 컴파일 스레드가 크래시됨 (`[RULE:DefensiveIndexing]`).
* **권장 수정안**:
  - `if len(innerIndex.Columns) == 0 { continue }` 방어적 슬라이스 길이 검사 추가.
* **판정**: `DEFECT_LOGGED`

---

### [심층 구역 5] 멀티 와이어 & 복제 엔진 (Multi-Wire Protocols & HA Replication Core)
* **점검 대상 파일**: `pkg/wire/server.go`, `pkg/wire/tds/packet.go`, `pkg/replication/manager.go`, `pkg/replication/broadcaster.go`, `pkg/replication/client.go` (총 4,058 라인 전수 스캔)
* **핵심 분석 초점**: WAL 스트리밍 무음 유실 및 복제본 정합성 파괴, 결정론적 Cancel 시크릿 키 추측 및 하이재킹 공격, TDS 다중 패킷 프레임 무제한 OOM 취약점, SSL 핸드셰이크 경로 쿼리 취소 오류.

#### [발굴 결함 5-1] `replication/broadcaster.go`: 슬로우 레플리카 버퍼 포화 시 WAL 레코드 무음 드롭(`default`)으로 인한 복제본 데이터 영구 손상
* **심각도**: `Critical`
* **위치**: `pkg/replication/broadcaster.go:61-68` (`WALBroadcaster.Broadcast`)
* **현상 및 원인 분석**:
  - 고부하 상황에서 특정 슬로우 레플리카(Subscriber)의 수신 채널 버퍼(`sub.ch`)가 포화되었을 때, `select { case sub.ch <- rec: default: }`로 무음 드롭(Silent Drop)하고 그냥 통과함.
  - WAL은 순차적인 원자성 로그 스트림이므로, 중간의 단 1건이라도 레코드가 누락되면 복제본의 상태 머신은 복구 불가능하게 깨지거나 LSN 갭(Gap)이 발생하여 원본과 영구히 다른 데이터를 유지하게 됨.
  - **결과**: 레플리카가 장애를 인지하지 못한 채 중간 트랜잭션을 건너뛰고 다음 커밋을 적용하여 영구적인 데이터 손상(Corrupted Replica) 초래 (`[RULE:ExplicitError]`).
* **권장 수정안**:
  - 버퍼 오버플로우 발생 시 무음 드롭을 절대 금지하고, 해당 지연 레플리카를 슬로우 노드로 분류하여 연결을 강제 퇴출(Evict/Disconnect)시키고 전체 동기화(Full Catch-up)를 유도하도록 변경.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 5-2] `wire/server.go`: `CancelRequest` 시크릿 키의 결정론적 선형 생성(`pid * 10007 + 42`)으로 인한 취소 키 예측 및 세션 DoS 공격 취약점
* **심각도**: `Critical`
* **위치**: `pkg/wire/server.go:663-667` (`handleConnection`)
* **현상 및 원인 분석**:
  - PGWire 연결 생성 시 클라이언트에게 발급하는 백엔드 취소 키(`secretKey`)를 `uint32(connID*10007 + 42)`라는 고정된 선형 산술식으로 계산함.
  - 백엔드 프로세스 ID(`pid`) 역시 `uint32(connID)`로 1부터 순차 증가하므로, 악의적인 사용자는 자신의 `pid`로부터 다른 세션의 `pid`와 해당 `secretKey`를 100% 확률로 역산/추측할 수 있음.
  - **결과**: 공격자가 무작위 또는 특정 사용자의 세션 `pid`에 대해 위조된 `CancelRequest` 패킷을 전송하여 다른 사용자의 실행 중인 장기 트랜잭션과 쿼리를 임의로 중단시키는 Cancel Hijacking 및 DoS 공격 가능 (`[RULE:PathSecurity]`).
* **권장 수정안**:
  - `crypto/rand`를 사용하여 암호학적으로 안전한 32비트 난수로 `secretKey`를 발급하도록 수정:
    ```go
    var secretKey uint32
    _ = binary.Read(crand.Reader, binary.BigEndian, &secretKey)
    ```
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 5-3] `replication/client.go`: 로컬 엔진 WAL 적용 실패 에러 무음 삼킴(`_ = err`) 및 실패한 LSN 허위 전진/보고
* **심각도**: `High`
* **위치**: `pkg/replication/client.go:201-211` (`streamOnce`)
* **현상 및 원인 분석**:
  - 레플리카 노드가 Primary로부터 수신한 WAL 레코드를 로컬 스토리지 엔진에 반영하는 `backup.ApplyWALRecordToEngine(c.eng, rec)` 실행 중 에러가 발생해도, 아무런 처리나 리턴 없이 에러를 무음 삼킴(`_ = err`).
  - 더 심각하게는 에러가 발생했음에도 불구하고 바로 다음 줄에서 `if rec.LSN > c.lastLSN.Load() { c.lastLSN.Store(rec.LSN) }`을 실행하여 성공한 것처럼 LSN 카운터를 전진시키고 Primary에게 ACK를 보냄.
  - **결과**: 레플리카 로컬 디스크나 OCC 엔진에 변경사항이 전혀 반영되지 않았음에도 시스템은 정상 동기화된 것으로 간주하여 데이터 불일치 상태가 영구 고착화됨 (`[RULE:ExplicitError]`).
* **권장 수정안**:
  - `ApplyWALRecordToEngine` 에러 시 즉시 `return fmt.Errorf("failed to apply WAL record LSN %d: %w", rec.LSN, err)`로 스트림을 중단하고 재시도 또는 재동기화 단계로 진입.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 5-4] `wire/server.go`: SSL 핸드셰이크 경로의 `CancelRequest` 처리에서 `CancelSessionQuery` 대신 `CloseSession` 오호출로 인한 세션 강제 파괴 버그
* **심각도**: `High`
* **위치**: `pkg/wire/server.go:573-593` (`handleConnection`)
* **현상 및 원인 분석**:
  - 클라이언트가 SSL 암호화 핸드셰이크를 통과한 직후 취소 요청(`code == 80877102`)을 전송했을 때, 실행 중인 쿼리만 취소해야 함에도 `s.coordinator.CloseSession(targetSessID)`를 호출함.
  - 비-SSL 취소 처리부(라인 530)에서는 정상적으로 `s.coordinator.CancelSessionQuery(targetSessID)`를 호출하고 있으나, SSL 경로에서는 잘못된 메서드를 호출하여 세션 자체를 완전히 파괴해버림.
  - **결과**: SSL 환경에서 클라이언트가 실행 중인 쿼리를 취소하려 할 때, 쿼리만 취소되는 것이 아니라 클라이언트의 연결 세션 자체가 완전히 닫혀 연결이 끊어지는 오동작 발생.
* **권장 수정안**:
  - 라인 590의 `s.coordinator.CloseSession(targetSessID)`를 `s.coordinator.CancelSessionQuery(targetSessID)`로 즉각 수정.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 5-5] `wire/tds/packet.go`: `ReadPacket`의 멀티 패킷 TDS 프레임 누적 시 페이로드 상한 미검사로 인한 악의적 OOM DoS 취약점
* **심각도**: `High`
* **위치**: `pkg/wire/tds/packet.go:53-91` (`ReadPacket`)
* **현상 및 원인 분석**:
  - TDS 프로토콜에서 여러 패킷으로 분할 전송되는 대형 메시지를 조립할 때, 루프 내에서 누적 버퍼 `fullPayload`의 크기를 제한하는 가드가 완전히 부재함 (`[RULE:BoundedBuffers]`).
  - 악의적인 클라이언트가 `StatusEOM` 비트를 설정하지 않고 계속해서 4KB 청크를 무한 스트리밍할 경우, 서버는 `fullPayload = append(fullPayload, chunk...)`를 반복하며 메모리를 기하급수적으로 소비함.
  - **결과**: 단일 악의적 TCP 연결만으로도 서버의 모든 힙 메모리를 고갈시켜 프로세스 전체를 OOM 크래시시키는 DoS 공격 가능.
* **권장 수정안**:
  - 최대 허용 메시지 크기(예: `const MaxTDSMessageSize = 64 * 1024 * 1024`)를 상수로 정의하고, 누적 크기가 이를 초과할 경우 즉시 에러를 반환하고 연결을 종료하도록 경계 검사 추가.
* **판정**: `DEFECT_LOGGED`

---

### [심층 구역 6] T-SQL 렉서 & 파서 코어 (T-SQL Lexer & Pratt Parser Core)
* **점검 대상 파일**: `pkg/proc/lexer.go`, `pkg/proc/parser.go`, `pkg/proc/parser_expr.go`, `pkg/proc/parser_dml.go`, `pkg/proc/token.go` (총 3,337 라인 전수 스캔)
* **핵심 분석 초점**: 데이터 타입 매핑 정밀도 왜곡, UTF-8 멀티바이트 문자 렉싱 파편화, 이항 연산자 AST Nil Pointer Dereference 패닉, T-SQL SELECT INTO 테이블 생성 문법 거부, 서브쿼리 WITH CTE 구문 파싱 누락.

#### [발굴 결함 6-1] `proc/parser.go`: `parseDataType`에서 `DECIMAL`/`NUMERIC`/`MONEY`를 `TypeFloat`으로 매핑하여 금융/회계 부동소수점 오차 유발
* **심각도**: `Critical`
* **위치**: `pkg/proc/parser.go:255-280` (`parseDataType`)
* **현상 및 원인 분석**:
  - LionSQL 스토리지 엔진에는 임의 정밀도 십진수를 다루는 `storage.TypeDecimal`이 온전히 구현되어 있음에도, 파서의 `parseDataType` 스위치문에서 `DECIMAL`, `NUMERIC`, `MONEY`를 모두 부동소수점 타입인 `storage.TypeFloat`으로 강제 매핑함.
  - **결과**: T-SQL 저장 프로시저 및 DDL/DML에서 `DECIMAL(18, 4)`로 선언된 금융/회계 컬럼 및 변수가 IEEE 754 부동소수점(Float64)으로 처리되어 이진 부동소수점 오차(Precision Loss) 및 금액 계산 부정확 초래.
* **권장 수정안**:
  - `case "DECIMAL", "NUMERIC", "MONEY": return storage.TypeDecimal`로 정확한 데이터 타입 매핑 복구.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 6-2] `proc/lexer.go`: `isLetter(ch byte)`에서 UTF-8 멀티바이트 단일 바이트를 룬으로 오변환하여 비-ASCII 식별자 파편화 및 오인식
* **심각도**: `High`
* **위치**: `pkg/proc/lexer.go:497-499` (`isLetter`)
* **현상 및 원인 분석**:
  - `isLetter(ch byte)`가 `byte` 매개변수를 직접 `rune(ch)`로 변환하여 `unicode.IsLetter`를 호출함.
  - 한글, 한자, 일본어 등 다중바이트 UTF-8 문자의 첫 바이트(예: 0xEA)를 단일 바이트 룬(0x00EA, Latin-1 `ê`)으로 왜곡 평가하며, 후속 바이트들은 식별자 문자로 인식되지 못하고 토크나이저에서 쪼개짐 (`[RULE:CharSanitization]`).
  - **결과**: 한글 테이블명, 컬럼명, 변수명이 여러 개의 `TokenIllegal` 또는 엉뚱한 식별자로 파편화되어 파싱 에러 발생.
* **권장 수정안**:
  - `utf8.DecodeRuneInString`을 사용하여 올바른 다중바이트 룬 단위로 문자를 소비하고 판별하도록 렉서 구조 개선.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 6-3] `proc/parser_expr.go`: 이항 연산자 파싱 중 오른쪽 표현식 실패 시 `&BinaryExpr{Right: nil}` 반환으로 인한 런타임 Nil Pointer Dereference 패닉
* **심각도**: `High`
* **위치**: `pkg/proc/parser_expr.go:180-194` (`parseExpression`)
* **현상 및 원인 분석**:
  - 이항 연산자 파싱 경로(`default:`)에서 `rightExp := p.parseExpression(opPrec)`를 호출한 뒤, `rightExp`의 nil 여부를 전혀 검증하지 않고 `&BinaryExpr{Left: leftExp, Op: op, Right: rightExp}`를 조립하여 반환함.
  - 쿼리에 구문 오류가 있거나 오른쪽 피연산자가 없는 경우(`WHERE a = ;` 또는 `SELECT a + FROM t`), `Right == nil`인 비정상 AST 노드가 생성되어 후속 컴파일러(`compiler.go`)나 평가기(`eval.go`)로 전달됨.
  - **결과**: 컴파일러나 VM에서 `b.Right.Compile(...)` 호출 시 `panic: runtime error: invalid memory address or nil pointer dereference` 발생 (`[RULE:PanicRecovery]`, `[RULE:LexicalSafety]`).
* **권장 수정안**:
  - `if rightExp == nil { return nil }` 검사를 배치하여 피연산자 누락 시 안전하게 파싱 에러 반환.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 6-4] `proc/parser_dml.go`: `parseSelect`에서 `SELECT ... INTO` 문법 시 `@variable`만 강제하여 표준 T-SQL 테이블 생성 구문 전체 거부
* **심각도**: `High`
* **위치**: `pkg/proc/parser_dml.go:12-21` (`parseSelect`)
* **현상 및 원인 분석**:
  - `SELECT col INTO ...` 파싱 시 `INTO` 다음 토큰이 `@variable`이 아니면 무조건 `expected @variable after INTO, got ...` 에러를 반환하며 즉각 중단함.
  - T-SQL 표준 및 실무에서 가장 광범위하게 쓰이는 `SELECT * INTO #temp FROM src` 또는 `SELECT * INTO new_table FROM src` 물리/임시 테이블 생성 구문을 전혀 파싱하지 못하고 거부함.
* **권장 수정안**:
  - `INTO` 뒤 토큰이 `@variable`이 아니더라도 `TokenIdent`이면 `SelectIntoTableStmt`로 분기하여 정상적인 임시/신규 테이블 생성 구문으로 파싱하도록 확장.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 6-5] `proc/parser_dml.go`: `JOIN` 서브쿼리에서 `WITH` CTE 구문 감지 시 `parseWith()` 미호출로 인한 파싱 실패 오류
* **심각도**: `Medium`
* **위치**: `pkg/proc/parser_dml.go:839-850` (`parseGeneralSelect`)
* **현상 및 원인 분석**:
  - `JOIN (WITH cte AS (...) SELECT ...)` 형태의 CTE가 내장된 서브쿼리를 파싱할 때 `p.peekTokenIs(TokenWith)`를 감지하여 괄호를 열지만, 본체 파싱 시 `parseWith()`를 호출하지 않고 `parseGeneralSelect()`를 호출함.
  - `parseGeneralSelect()`는 `WITH` 키워드를 처리하는 로직이 없어, 첫 단어인 `WITH`를 일반 컬럼 식별자로 오인하여 연쇄적인 파싱 에러를 유발함.
* **권장 수정안**:
  - 서브쿼리 진입 시 `if p.curTokenIs(TokenWith) { subSel, err = p.parseWith() }` 분기 추가.
* **판정**: `DEFECT_LOGGED`

---

### [심층 구역 7] 시스템 카탈로그 & DDL 메타데이터 코어 (System Catalog & DDL Metadata Core)
* **점검 대상 파일**: `pkg/catalog/catalog.go`, `pkg/catalog/virtual/interceptor.go`, `pkg/catalog/virtual/mssql.go` (총 2,238 라인 전수 스캔)
* **핵심 분석 초점**: DDL 변경사항 영속화 누락 및 재시작 시 영구 유실, 복합 대괄호 식별자 카탈로그 검색 실패, 포인터 미복사 반환으로 인한 동시성 Data Race, 가상 카탈로그 라우팅 시 리터럴 대괄호 무차별 삭제 오염.

#### [발굴 결함 7-1] `catalog/catalog.go`: `AlterTrigger` 시 디스크 영속화(`persistObjectsLocked`) 누락으로 인한 서버 재시작 시 트리거 수정사항 영구 유실
* **심각도**: `Critical`
* **위치**: `pkg/catalog/catalog.go:376-390` (`AlterTrigger`)
* **현상 및 원인 분석**:
  - `RegisterTrigger`, `CreateTrigger`, `DropTrigger`는 모두 변경 후 디스크 파일(`catalog_objects.json`)에 원자적으로 저장하는 `persistObjectsLocked()`를 호출하지만, `AlterTrigger`는 영속화 호출이 완전히 누락됨.
  - 인메모리 맵(`c.triggers[key]`)만 갱신하고 디스크에 쓰지 않기 때문에, 트리거를 `ALTER TRIGGER`로 수정한 뒤 서버를 재시작하면 이전 버전의 트리거로 되돌아가는 치명적 DDL 수정사항 유실(Silent Loss of DDL Alter) 발생.
* **권장 수정안**:
  - `AlterTrigger` 하단에 `if c.persistenceDir != "" { _ = c.persistObjectsLocked() }` 영속화 호출 추가.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 7-2] `catalog/catalog.go`: `CreateStatistics` 영속화 누락 및 `CatalogObjects` 직렬화 구조체 누락으로 인한 확장 통계 메타데이터 영구 증발
* **심각도**: `Critical`
* **위치**: `pkg/catalog/catalog.go:790-796`, `507-512` (`CreateStatistics`, `CatalogObjects`)
* **현상 및 원인 분석**:
  - `CreateStatistics`가 확장 통계 메타데이터(`c.statistics`)를 인메모리 맵에만 저장하고 디스크 영속화를 일체 수행하지 않음.
  - 또한 카탈로그 직렬화 DTO인 `CatalogObjects` 구조체에 `Statistics []*StatisticDef` 필드가 아예 존재하지 않아, 시스템 저장 및 복원 경로가 원천적으로 누락되어 있음.
  - **결과**: 사용자가 CBO 최적화를 위해 생성한 다중 컬럼 확장 통계(`CREATE STATISTICS`)가 서버 재부팅 시마다 전부 증발하여 CBO 플래너 성능 저하 유발.
* **권장 수정안**:
  - `CatalogObjects`에 `Statistics` 필드를 추가하고, `persistObjectsLocked` 및 `loadObjectsFileLocked`에 통계 직렬화/역직렬화 파이프라인 통합.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 7-3] `catalog/catalog.go`: `cleanCatalogIdent`의 나이브한 `Trim` 로직으로 인한 `[dbo].[my_proc]` 복합 식별자 오염 및 카탈로그 검색 실패
* **심각도**: `High`
* **위치**: `pkg/catalog/catalog.go:116-122` (`cleanCatalogIdent`)
* **현상 및 원인 분석**:
  - `cleanCatalogIdent`가 `strings.Trim(s, "[]\"'`")`을 사용하는데, 이는 문자열의 맨 앞과 맨 뒤의 대괄호만 제거함.
  - `[dbo].[my_proc]` 형태의 식별자가 들어올 경우 중간의 `].[ `가 보존되어 `dbo].[my_proc`이 되고, `strings.HasPrefix(..., "dbo.")` 검사를 통과하지 못함.
  - **결과**: 카탈로그 맵의 키가 `dbo].[my_proc`으로 오염되어, 정상적인 `my_proc` 호출 시 `ErrProcNotFound`가 발생하는 심각한 식별자 정규화 결함 초래 (`[RULE:ModularAbstraction]`).
* **권장 수정안**:
  - `sqlutil.CleanSchemaPrefix` 또는 `strings.Split(s, ".")`를 사용하여 각 파트별로 괄호를 안전하게 벗겨내는 정규화 로직 적용.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 7-4] `catalog/catalog.go`: `GetView`에서 내부 포인터 복사 없이 `*View` 원본 객체를 직접 반환하여 동시 쿼리 실행 시 메모리 Data Race 유발
* **심각도**: `High`
* **위치**: `pkg/catalog/catalog.go:752-761` (`GetView`)
* **현상 및 원인 분석**:
  - `GetProcedure`, `GetFunction`, `GetTrigger`는 모두 `cp := *obj; return &cp, nil` 형태로 내부 포인터를 얕은 복사하여 반환함으로써 카탈로그 락(`c.mu.RUnlock()`) 해제 후의 동시성 변조를 방지함.
  - 반면 `GetView`는 카탈로그 맵의 원본 포인터 `return v, nil`을 그대로 반환함.
  - **결과**: 호출자 고루틴이 뷰 메타데이터를 참조하는 동안 다른 트랜잭션이 `CreateOrReplaceView`나 `DropView`를 실행하면 메모리 Data Race가 발생하여 프로세스 불안정 초래 (`[RULE:ConcurrencySafety]`).
* **권장 수정안**:
  - `cp := *v; return &cp, nil` 복사본 반환 패턴으로 통일.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 7-5] `catalog/virtual/interceptor.go`: 가상 카탈로그 라우팅 시 문자열 리터럴 검사 없이 SQL 전체 대괄호 무차별 삭제 오염
* **심각도**: `Medium`
* **위치**: `pkg/catalog/virtual/interceptor.go:452` (`Execute`)
* **현상 및 원인 분석**:
  - 가상 카탈로그 쿼리 라우팅 전 `upper = strings.ReplaceAll(strings.ReplaceAll(upper, "[", ""), "]", "")`을 무차별 실행함.
  - SQL 문자열 리터럴 내부에 대괄호가 포함되어 있는 경우(예: `WHERE relname = '[temp]'` 또는 `LIKE '[A-Z]%'`), 리터럴 내부의 대괄호까지 전부 제거되어 쿼리 검색 조건이 파괴됨.
* **권장 수정안**:
  - 문자열 리터럴 외부의 식별자 대괄호만 선별적으로 제거하거나 정규 파서 AST 라우팅 적용.
* **판정**: `DEFECT_LOGGED`

---

### [심층 구역 8] SQL 실행 엔진 본체 (SQL Execution Runtime Core)
* **점검 대상 파일**: `pkg/engine/sqlexec_dml.go`, `pkg/engine/sqlexec_ddl.go`, `pkg/engine/sqlexec_merge.go`, `pkg/engine/sqlexec_cursor.go` (총 4,809 라인 전수 스캔)
* **핵심 분석 초점**: MERGE 타깃 무제한 풀스캔 OOM, UPDATE/DELETE 디스크 I/O 에러 시 무음 탈출 및 부분 커밋 데이터 오염, WITHOUT HOLD 커서 트랜잭션 경계 누수, DROP TABLE 외래키 참조 무결성 검증 누락.

#### [발굴 결함 8-1] `engine/sqlexec_merge.go`: `MERGE` 문 실행 시 타깃 테이블 무제한 풀스캔 힙 적재 및 $O(M \times N)$ 비색인 루프로 인한 OOM DoS 크래시
* **심각도**: `Critical`
* **위치**: `pkg/engine/sqlexec_merge.go:106-114`, `146-157` (`execMerge`)
* **현상 및 원인 분석**:
  - `MERGE` 문을 실행할 때 대상 테이블(`parsed.targetTable`) 전체를 `targetCursor.Record()`를 통해 무조건 전체 풀스캔하여 `targetRows` 슬라이스에 전량 인메모리 힙 적재함.
  - 대상 테이블이 수백만~수천만 행 이상인 대형 테이블인 경우, 수 GB의 메모리가 일시에 할당되어 Go 런타임 OOM 크래시가 발생함 (`[RULE:BoundedBuffers]`).
  - 또한 매칭 로직이 $O(M \times N)$ 비색인 전수 중첩 루프로 구현되어 있어, 소스와 타깃 레코드 곱연산으로 인한 CPU 스톨 및 트랜잭션 타임아웃을 유발함.
* **권장 수정안**:
  - 타깃 테이블의 PK/유니크 인덱스를 통한 $O(1)$ 점조회(Point Lookup) 방식으로 변경하고, 전체 메모리 버퍼링을 제거하여 스트리밍 파이프라인으로 전환.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 8-2] `engine/sqlexec_dml.go`: `execUpdate` 및 `execDelete` 스캔 중 I/O 에러 시 무음 탈출(`break`)로 인한 치명적 부분 커밋 데이터 오염
* **심각도**: `Critical`
* **위치**: `pkg/engine/sqlexec_dml.go:1128-1132`, `1290-1293` (`execUpdate`, `execDelete`)
* **현상 및 원인 분석**:
  - `execUpdate`와 `execDelete`의 레코드 수집 루프(`for cursor.Next()`)에서 `rec, err := cursor.Record(); if err != nil { break }` 형태로 I/O 읽기 실패나 스토리지 에러를 무음 삼키고 조기 탈출함 (`[RULE:ExplicitError]`).
  - 에러가 무시된 상태에서 후속 `tx.Update()` / `tx.Delete()` 루프로 직행하여, 읽기에 성공한 앞부분 행들만 물리 디스크에 반영하고 `tx.Commit()`을 완료함.
  - **결과**: 조건에 부합하는 전체 레코드 중 앞부분 30%만 수정/삭제되고 나머지 70%는 그대로 방치되는 끔찍한 부분 적용(Partial Apply) 데이터 정합성 파괴 초래.
* **권장 수정안**:
  - `if err != nil { if isEphemeral { _ = tx.Rollback() }; return nil, fmt.Errorf("cursor record scan failed: %w", err) }`로 명시적 롤백 및 에러 반환.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 8-3] `engine/sqlexec_cursor.go`: `WITHOUT HOLD` 커서가 트랜잭션 종료 시 자동 정리되지 않고 세션에 영구 잔류하는 라이프사이클 위반
* **심각도**: `High`
* **위치**: `pkg/engine/sqlexec_cursor.go:87-97`, `pkg/engine/session.go:71` (`execDeclareCursor`)
* **현상 및 원인 분석**:
  - SQL 표준 및 PostgreSQL 명세에 따르면 `WITH HOLD` 옵션 없이 선언된 기본 커서는 트랜잭션이 `COMMIT` 또는 `ROLLBACK`되는 즉시 자동으로 파기(Close)되어야 함.
  - 그러나 LionSQL의 세션 트랜잭션 커밋/롤백 핸들러(`session.go:CommitTx`, `RollbackTx`)에는 `cs.Cursors`를 정리하는 로직이 완전히 누락되어 있음.
  - **결과**: 트랜잭션이 종료된 후에도 커서가 세션 메모리에 영구 잔류하여, 이미 만료된 이전 스냅샷의 행을 계속해서 클라이언트에게 리턴하는 트랜잭션 격리 파괴 및 메모리 누수 발생.
* **권장 수정안**:
  - `cs.CommitTx()` 및 `cs.RollbackTx()`에 `for name, cur := range cs.Cursors { if !cur.WithHold { delete(cs.Cursors, name) } }` 정리 핸들러 바인딩.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 8-4] `engine/sqlexec_ddl.go`: `execDropTable` 실행 시 외래키 참조 무결성 검증 누락으로 인한 부모 테이블 무단 삭제
* **심각도**: `High`
* **위치**: `pkg/engine/sqlexec_ddl.go:389-415` (`execDropTable`)
* **현상 및 원인 분석**:
  - 테이블을 삭제할 때 트리거는 검사하여 삭제하지만, 다른 자식 테이블들이 `FOREIGN KEY REFERENCES <tableName>(id)`로 해당 테이블을 참조하고 있는지 여부를 전혀 검사하지 않음.
  - 자식 테이블에 데이터가 엄연히 존재하는 상태에서 부모 테이블이 아무런 제약 없이 삭제됨 (`s.engine.DropTable`).
  - **결과**: 자식 테이블의 외래키가 존재하지 않는 가상의 고아 테이블을 가리키게 되어, 데이터베이스 전역의 참조 무결성(Referential Integrity)이 완전히 파괴됨.
* **권장 수정안**:
  - 카탈로그의 모든 테이블을 순회하여 해당 테이블을 참조하는 자식 외래키가 존재하는 경우 `RESTRICT` 에러(`cannot drop table %q because other objects depend on it`)를 발생시키고 차단.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 8-5] `engine/sqlexec_merge.go`: `MERGE (VALUES ...)` 소스 파싱 시 작은따옴표 이스케이프(`''`) 언이스케이프 누락
* **심각도**: `Medium`
* **위치**: `pkg/engine/sqlexec_merge.go:364` (`loadMergeSourceRows`)
* **현상 및 원인 분석**:
  - `MERGE INTO ... USING (VALUES ('O''Reilly'))` 형태로 작은따옴표가 이스케이프된 튜플을 파싱할 때 `vStr = strings.Trim(vStr, "'\"")`만 수행하고 내부의 `''`를 `'`로 치환하는 언이스케이프를 수행하지 않음.
  - **결과**: DB 타깃 테이블에 `O'Reilly`가 아닌 이스케이프된 `O''Reilly` 문자열이 원시 형태 그대로 삽입되어 데이터 오염 초래.
* **권장 수정안**:
  - `vStr = strings.ReplaceAll(vStr, "''", "'")` 언이스케이프 로직 추가.
* **판정**: `DEFECT_LOGGED`

---

### [심층 구역 9] 백업, 복구 & PITR 시점 복구 (Backup, Restore & PITR Core)
* **점검 대상 파일**: `pkg/backup/backup.go`, `pkg/backup/restore.go`, `pkg/backup/pitr.go`, `pkg/backup/wal_archive.go` (총 1,894 라인 전수 스캔)
* **핵심 분석 초점**: WAL 회전 시 락 미보호 트랜잭션 유실, 복원 실패 시 롤백 부재로 인한 반파 DB 방치, 외래키 의존성 무시 복원으로 인한 인서트 실패, 백업 중 라이브 카탈로그 참조 스냅샷 불일치.

#### [발굴 결함 9-1] `backup/wal_archive.go`: `RotateAndArchive` 시 락 미보호로 인해 아카이브 복사 직후 유입된 신규 트랜잭션이 `writer.Truncate()`에 의해 영구 잘려나가는 데이터 유실
* **심각도**: `Critical`
* **위치**: `pkg/backup/wal_archive.go:154-173` (`RotateAndArchive`)
* **현상 및 원인 분석**:
  - `RotateAndArchive`는 WAL 세그먼트를 복사(`ArchiveSegment`)한 직후 `writer.Truncate()`를 호출하여 활성 WAL 파일을 초기화함.
  - 그러나 복사와 `Truncate` 사이에 WAL 라이터 락(`writer.mu`)을 유지하지 않으므로, 복사가 끝난 나노초 직후 동시 트랜잭션이 커밋되어 활성 WAL 파일에 새 레코드를 쓰는 경쟁 상태(Race Condition)가 발생함.
  - **결과**: `writer.Truncate()`가 실행되면서 방금 커밋된 신규 트랜잭션의 WAL 데이터가 아카이브에도 복사되지 못하고 디스크에서도 즉시 잘려나가 영구 증발(Silent Data Truncation Loss)함 (`[RULE:AtomicSaves]`).
* **권장 수정안**:
  - `writer.Rotate()` 내부에서 `writer.mu.Lock()`을 쥔 상태로 새 파일 디스크립터를 원자적으로 교체(Atomic Swap)하고 이전 파일을 아카이빙하도록 구조 개선.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 9-2] `backup/restore.go`: `RestoreBackup` 실패 시 이전 상태 롤백 부재로 인한 반파 데이터베이스(Half-Restored Corrupted DB) 방치
* **심각도**: `Critical`
* **위치**: `pkg/backup/restore.go:121-220` (`RestoreBackup`)
* **현상 및 원인 분석**:
  - `RestoreBackup`은 `opts.DropExisting == true`일 때 기존 테이블들을 먼저 삭제한 뒤 아카이브의 테이블들을 순차적으로 복원함.
  - 복원 도중 중간 테이블(예: 5번째 테이블)에서 디스크 용량 부족이나 손상된 레코드로 인해 에러가 반환될 경우, 이미 삭제된 이전 테이블들은 복구되지 않고 앞부분 테이블들만 커밋된 상태로 함수가 즉각 종료됨.
  - **결과**: 기존 데이터는 이미 삭제되었고 복원 데이터는 절반만 남은 반파(Half-Restored) 상태로 방치되어 데이터베이스 복구가 불가능해짐 (`[RULE:ExplicitError]`, `[RULE:AtomicSaves]`).
* **권장 수정안**:
  - 복원 시작 전 원본 데이터 디렉터리를 스냅샷 격리하거나, 임시 네임스페이스에 전체 복원을 완료한 뒤 원자적 심볼릭 링크/디렉터리 스왑을 수행하도록 변경.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 9-3] `backup/restore.go`: 복원 시 외래키(FK) 의존성 순서(Topological Sort) 미고려로 인한 자식-부모 테이블 복원 실패
* **심각도**: `High`
* **위치**: `pkg/backup/restore.go:121-140`, `150-200` (`RestoreBackup`)
* **현상 및 원인 분석**:
  - 아카이브에서 테이블을 재생성하고 데이터를 넣을 때, 테이블 간의 외래키(Foreign Key) 참조 관계를 분석하지 않고 `tables` 슬라이스에 저장된 순서대로 복원함.
  - 자식 테이블(예: `orders`)이 부모 테이블(예: `customers`)보다 먼저 복원되는 경우, `orders` 레코드 삽입 시 부모 키가 존재하지 않아 외래키 위반(`ErrForeignKeyViolation`) 에러가 발생하며 전체 복원이 중단됨.
* **권장 수정안**:
  - 데이터 복원 루프 동안 일시적으로 외래키 제약조건 검사를 비활성화(`session.SetEnforceFK(false)`)하거나, 외래키 그래프를 위상 정렬(Topological Sort)하여 부모 ➔ 자식 순서로 복원하도록 보정.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 9-4] `backup/backup.go`: `CreateBackup`에서 스냅샷 트랜잭션 대신 엔진의 현재 라이브 카탈로그(`eng.ListTables()`)를 조회하여 스냅샷 불일치 발생
* **심각도**: `High`
* **위치**: `pkg/backup/backup.go:58-62` (`CreateBackup`)
* **현상 및 원인 분석**:
  - 라인 50에서 읽기 전용 스냅샷 트랜잭션 `tx`를 생성했음에도, 테이블 목록을 수집할 때는 `eng.ListTables()`를 호출하여 엔진의 최신 라이브 카탈로그를 조회함.
  - 백업 도중 다른 세션이 테이블을 `CREATE TABLE`하거나 `DROP TABLE`하는 DDL을 수행할 경우, 스냅샷 `tx`에서는 보이지 않는 테이블을 스캔하려 하거나 이미 삭제된 테이블을 스캔하려 하여 백업 작업이 에러와 함께 실패함.
* **권장 수정안**:
  - 스냅샷 트랜잭션 `tx`가 관리하는 트랜잭션 격리 카탈로그 스냅샷으로부터 테이블 목록을 조회하도록 수정.
* **판정**: `DEFECT_LOGGED`

#### [발굴 결함 9-5] `backup/pitr.go`: `RestorePITR`에서 DDL WAL 연산 재생 시 상위 `cat *catalog.Catalog` 메타데이터 동기화 누락
* **심각도**: `Medium`
* **위치**: `pkg/backup/pitr.go:43-56`, `289-293` (`ApplyWALRecordWithOptions`, `RestorePITR`)
* **현상 및 원인 분석**:
  - `RestorePITR` 실행 시 `ApplyWALRecordToEngine(eng, rec)`를 호출하는데, 이는 `ddlHook`이 `nil`인 상태로 호출됨.
  - WAL에 기록된 `OpCreateTable`, `OpDropTable`, `OpCreateIndex` 등의 DDL 연산이 스토리지 엔진에는 반영되지만 상위 카탈로그 인스턴스(`cat`)에는 통보되지 않음.
  - **결과**: PITR 시점 복구가 완료된 후 애플리케이션이 쿼리를 실행했을 때, 카탈로그 캐시와 물리 스토리지 간의 불일치로 인해 테이블 누락 오류 발생.
* **권장 수정안**:
  - `RestorePITR`에서 `ApplyWALRecordWithOptions`를 호출하고, DDL 발생 시 `cat.InvalidateTableStats` 및 카탈로그 동기화 콜백을 필수로 바인딩.
* **판정**: `DEFECT_LOGGED`

---
