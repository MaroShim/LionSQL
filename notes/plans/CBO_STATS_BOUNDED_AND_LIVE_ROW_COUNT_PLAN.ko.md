# CBO 통계 자원 제약 및 WiredTiger 실시간 행 카운터 아키텍처 설계서 (`PERF-CBO-STATS-BOUNDED-1`)

> - **태스크 ID**: `PERF-CBO-STATS-BOUNDED-1` (과제 `#24`)
> - **도메인**: Planner / Storage
> - **마일스톤**: 1.0-GA
> - **최초 작성일**: 2026-11-13
> - **최종 갱신일**: 2026-11-14
> - **상태**: 구현 및 검증 완료 (`DONE`)
> - **준수 규칙**: `[RULE:BoundedBuffers]`, `[RULE:ZeroAllocation]`, `[RULE:LockHierarchy]`, `[RULE:PlanPersistence]`

---

## 1. 개요 및 배경

MySQL(InnoDB) 등 전통적인 RDBMS 환경에서는 대량 데이터 적재 직후 `ANALYZE TABLE`이 제때 실행되지 않으면 과거 통계(예: 0건 또는 수건)에 고착되어 플래너가 잘못된 조인 순서(Join Order)나 부적절한 테이블 풀스캔을 선택하는 성능 저하가 빈번합니다.

LionSQL에서는:
1. `memengine`의 경우 `TableRowCount` 인터페이스를 통해 실시간 $O(1)$ 행 수를 즉시 CBO 플래너(`pkg/planner/cost.go`)에 제공하고 있었으나, **`wiredtiger` 엔진은 `TableRowCount(tableName string) (int64, error)`를 구현하지 않아** `ANALYZE` 이전까지 기본 추정치(1,000행)로만 작동하는 한계가 있었습니다.
2. `pkg/engine/sqlexec_explain.go`의 `AnalyzeTable()`이 테이블의 모든 행을 `colVals[colName] = append(colVals[colName], v)` 형태로 메모리에 무제한 적재하고 있어, 수백만~수천만 행 테이블에서 `ANALYZE` 실행 시 급격한 메모리 스파이크(OOM 위험)가 발생할 수 있었습니다 (`[RULE:BoundedBuffers]` 위반 소지).

---

## 2. 아키텍처 요구사항 및 설계안

### 2.1. WiredTiger 실시간 $O(1)$ 행 수 카운터 (`pkg/storage/wiredtiger/wt_engine.go`)

1. **원자적 카운터 풀**:
   - `Engine` 구조체에 `rowCounts map[string]*atomic.Int64`를 유지하고, `catalog.GetTableStats`의 `rowCounter` 인터페이스에 정확히 부합하는 `TableRowCount(tableName string) (int64, error)`를 노출합니다.
2. **트랜잭션 격리 동기화**:
   - 트랜잭션(`WTTx`) 내부에서는 `rowDeltas map[string]int64`로 `Insert`(++), `Delete`(--)를 격리 추적합니다.
   - 트랜잭션이 성공적으로 커밋(`Commit()`)될 때만 엔진 레벨 원자적 카운터(`atomic.Int64.Add(delta)`)에 일괄 반영합니다.
   - 롤백(`Rollback()`) 시에는 `rowDeltas`를 즉시 폐기하여 언커밋된 변경이 외부에 누출되지 않도록 보장합니다.
   - 세이브포인트 롤백(`RollbackToSavepoint()`) 시에는 언두 로그(`wtUndoOp`)를 역순회하며 롤백된 DML에 대해 델타를 자동 상쇄합니다.
3. **수명주기 동기화 및 락 계층 준수 (`[RULE:LockHierarchy]`)**:
   - `TruncateTable`: 원자적 카운터를 즉시 `0`으로 재설정(`Store(0)`).
   - `RenameTable`: 기존 테이블 키의 카운터를 새 테이블 키로 atomic하게 이전.
   - `DropTable`: 카운터 맵에서 안전하게 제거.
   - `Open()` 복구: 엔진 재기동 시 디스크 B-Tree 커서를 빠르게 1회 스캔(`countTableRows`)하여 원자적 카운터를 완벽히 복원.
   - 데드락 방지: `e.mu`를 이미 획득한 상태에서 호출되는 내부 메서드는 재귀 락을 피하기 위해 `getRowCountAtomicLocked`를 호출하도록 엄격히 분리.

---

### 2.2. `AnalyzeTable` 30,000행 Vitter 저수지 샘플링 (`pkg/engine/sqlexec_explain.go`)

PostgreSQL 공식 통계 샘플링 표준(`300 * default_statistics_target = 30,000`행)과 동일한 Bounded 샘플링 상한을 적용합니다:

1. **상한 상수 지정**:
   ```go
   const maxAnalyzeSampleRows int64 = 30000
   ```
2. **스트리밍 전체 스캔과 Bounded 메모리 분리**:
   - 전체 행 수(`totalRows`)와 `nullCounts[colName]`는 테이블 전체를 스트리밍 스캔하며 100% 정확하게 카운트합니다.
   - 히스토그램 및 MCV 산출용 샘플 슬라이스(`colVals[colName]`)는 컬럼별 non-null 행 수가 30,000행 이하일 때는 100% 수집합니다.
   - 컬럼 non-null 행 수가 30,000행을 초과하는 순간부터는 **Vitter's Algorithm R**에 따라 확률적으로 샘플을 교체합니다:
     ```go
     // k = maxAnalyzeSampleRows (30,000), i = nonNull + 1
     j := rand.Int64N(nonNull + 1)
     if j < maxAnalyzeSampleRows {
         colVals[c.Name][j] = v
     }
     ```
   - 복합 인덱스/통계용 `multiSets` 역시 엔트리 수를 `maxAnalyzeSampleRows` 이내로 Bounded 제한합니다.
3. **효과**:
   - 1억 건의 초대형 테이블에 대해 `ANALYZE TABLE`을 실행하더라도, 슬라이스 메모리 사용량이 컬럼당 최대 수 MB 이내로 영구 고정됩니다 (`[RULE:BoundedBuffers]`).

---

## 3. 검증 결과

1. **단위 및 회귀 테스트**:
   - `pkg/storage/wiredtiger/wt_table_row_count_test.go`: `TestWiredTiger_TableRowCount` (커밋, 롤백, 트렁케이트, 미존재 테이블 에러 전수 통과, 0 Data Races)
   - `pkg/engine/analyze_bounded_test.go`: `TestAnalyzeTable_BoundedSampling` (샘플링 및 CBO 통계 생성 정합성 100% 통과)
2. **빌드 무결성**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql` 성공
   - `go test -tags wiredtiger -v -race ./pkg/storage/wiredtiger ./pkg/engine` 100% PASS
