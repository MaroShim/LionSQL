# 2차 인덱스 복원 실패 시 자동 재구축 및 Table Scan 폴백 비활성화 체계 계획서
(`notes/plans/FIX_SECONDARY_INDEX_RECOVERY_FALLBACK_PLAN.ko.md`)

> **문서 상태**: Completed (구현 완료 및 검증 통과)  
> **태스크 ID**: `FIX-SECONDARY-INDEX-RECOVERY-FALLBACK-1`  
> **마일스톤**: `1.0-GA` (Storage Reliability & Data Correctness)  
> **관련 규칙**: `[RULE:ExplicitError]`, `[RULE:SafeDefaults]`, `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`

---

## 1. 추진 배경 및 결함 분석 (Problem Statement)

### 1.1 결함 현상
인메모리 스토리지 엔진(`pkg/storage/memengine/persistence.go`)에서 스냅샷 디스크 바이너리 또는 WAL로부터 2차 인덱스(Secondary Index)를 복원할 때, 손상이나 역직렬화 오류로 인해 `idx.Restore()`가 실패하더라도 에러를 로그만 남기고 삼킨 채 엔진이 계속 기동되었습니다.

### 1.2 발생 위치 및 코드 분석 (`pkg/storage/memengine/persistence.go:474-478`)
```go
// persistence.go:474
for _, idx := range td.Indexes {
    if err := idx.Restore(); err != nil {
        log.Printf("[memengine] CRITICAL: failed to restore secondary index %q on table %q from snapshot: %v", idx.Schema.Name, td.Schema.Name, err)
    }
}
```

### 1.3 위험성 (Silent Wrong Results & Data Loss)
1. **정합성 왜곡 (0건 반환 버그)**:
   - 복원에 실패한 인덱스가 카탈로그 및 테이블 스키마(`td.Schema.Indexes`)에 정상 존재하는 것처럼 남아있음.
   - 쿼리 플래너/옵티마이저는 해당 인덱스를 신뢰하고 `IndexScan`을 선택함.
   - 하지만 내부 스킵리스트(`idx.sl`)는 `nil`이므로 `ScanRange()`가 `nil`(0건)을 반환하여, **테이블에 행이 실제로 존재함에도 검색 결과가 0건으로 나오는 침묵하는 데이터 누락(Silent Wrong Result)** 참사가 발생함.
2. **제약 조건 무력화**:
   - `UNIQUE` 인덱스의 경우 복원되지 않은 상태에서 새로운 레코드가 삽입되면 유니크 중복 검사가 누락되어 테이블 무결성이 붕괴됨.

---

## 2. 해결 목표 (Target Architecture)

1. **테이블 행 기반 1단계 자동 자가 치유 (Auto-Rebuild from Table Rows)**:
   - 2차 인덱스는 1차 테이블 행 데이터로부터 파생된 보조 캐시이므로, 1차 테이블 행 데이터가 온전하다면 `td.forEachRow`를 통해 즉시 인덱스를 자동 재구축함.
2. **명시적 비활성화 격리 (Disabled State Isolation)**:
   - 유니크 제약 충돌 등으로 재구축마저 실패하면, `IndexSchema.Disabled = true`로 마킹하고 `[RULE:ExplicitError]` 원칙에 따라 ERROR 로그와 시스템 뷰(`sys.indexes.is_disabled = 1`)에 명확히 상태를 공시.
3. **순차 테이블 스캔(Sequential Table Scan) 자동 폴백**:
   - 쿼리 플래너(`pkg/planner`), WHERE 바운드 계산기(`bounds.go`), VDBE 런타임(`vm.go`)에서 `Disabled` 인덱스를 탐색 후보에서 완전히 제외하여, **Table Scan으로 안전하게 폴백하여 100% 완전한 쿼리 결과를 반환**.

---

## 3. 세부 아키텍처 및 구현 내용

### 3.1 메타데이터 확장 (`pkg/storage/engine.go`)
- `storage.ErrIndexDisabled`: 비활성화 인덱스 직접 스캔 시 반환할 표준 에러 추가.
- `IndexSchema`:
  ```go
  type IndexSchema struct {
      ...
      Disabled bool `json:"disabled,omitempty" msgpack:"disabled,omitempty"`
  }
  ```
- `TableSchema`: `FindIndexByColumn`, `FindExpressionIndex`, `FindBestCompositeIndex`에서 `idx.Disabled` 인덱스를 스킵하도록 방어 필터 적용. `FindActiveIndex(name)` 헬퍼 추가.

### 3.2 스토리지 복원 및 자가 치유 파이프라인 (`pkg/storage/memengine/`)
- **`TableData.rebuildIndexLocked(idx)`**:
  - 테이블 전체 행을 순회하여 신규 스킵리스트에 인덱스 키와 PK를 다시 적재.
  - 성공 시 `idx.sl`, `idx.Entries` 갱신 및 `idx.Schema.Disabled = false`.
- **`TableData.restoreIndexesLocked()`**:
  - `idx.Restore()` 실패 시 `rebuildIndexLocked(idx)` 호출.
  - 재구축 실패 시 `idx.Schema.Disabled = true` 설정 및 `updateSchemaIndexDisabled(idx.Name, true)`.
- **`tx.IndexScan` / `IndexOnlyScan` / `IndexScanDesc`**:
  - `idxData.Schema.Disabled` 확인 시 즉시 `storage.ErrIndexDisabled` 반환.
- **DML 인덱스 갱신 방어**:
  - `Disabled` 인덱스는 INSERT/UPDATE/DELETE 시 스킵리스트 갱신 및 유니크 제약 검사를 건너뛰어 패닉 방지.

### 3.3 쿼리 옵티마이저 & VDBE 순차 스캔 폴백
- `pkg/planner/planner.go`: `forceIndex` 및 단일 컬럼 인덱스 매칭 시 `Disabled` 인덱스 제외 ➔ Table Scan 선택.
- `pkg/planner/bounds.go`: 인덱스 범위 검색 조건 추출 시 `Disabled` 인덱스 무시.
- `pkg/proc/runtime/vm.go`: T-SQL 바이트코드 VM에서 `Disabled` 인덱스 스킵 ➔ 인라인 테이블 필터 루프로 폴백.
- `pkg/catalog/virtual/mssql_single.go`: `sys.indexes` 뷰에 실제 `is_disabled: idx.Disabled` 컬럼 반영.

---

## 4. 검증 결과 (Verification)

1. **`pkg/storage/memengine/index_recovery_fallback_test.go`**:
   - `TestSecondaryIndex_AutoRebuildOnCorruptedSnapshotEntries`: 스냅샷 인덱스 손상 시 테이블 행 기반 자동 재구축 및 2건 검색 정상 통과.
   - `TestSecondaryIndex_DisableAndFallbackToTableScanOnRebuildFailure`: 재구축 실패 시 `Disabled = true` 격리, `FindIndexByColumn` 스킵, `IndexScan` 에러 반환, Table Scan을 통한 2건 정상 검색 통과.
2. **`pkg/engine/index_fallback_test.go`**:
   - `TestEngine_DisabledIndexFallbackToTableScan`: 실제 SQL 쿼리 레벨에서 `sys.indexes`의 `is_disabled = 1` 확인 및 Table Scan 폴백을 통한 완전한 행 반환 검증.
3. **전체 패키지 회귀 테스트**:
   - `pkg/storage/memengine`, `pkg/planner`, `pkg/proc/runtime`, `pkg/engine` 전체 테스트 100% PASS (0 data races).
   - `./scripts/verify_no_slt_mutation.sh` 오라클 무결성 통과.
