# UPDATE 원본 튜플 스냅샷 기반 평가 및 원자적 컬럼 스왑 보장 계획서 (`UPDATE_SNAPSHOT_COLUMN_SWAP_PLAN.ko.md`)

## 1. 개요
* **태스크 ID**: `FIX-UPDATE-SNAPSHOT-COLUMN-SWAP-1` (결함 56)
* **목표**: `UPDATE` 문의 `SET a = b, b = a` 및 `SET a = a + b, b = a` 등 다중 컬럼 대입 연산 시, 대입문 기술 순서나 중간 계산 결과에 영향받지 않고 갱신 전 원본 행 스냅샷(`origSnapshot := rec.Clone()`)만을 일관되게 참조하여 SQL 표준 및 T-SQL 원자적 다중 컬럼 스왑(Atomic Multi-Column Swap)을 엄격히 보장한다.
* **관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`, `[RULE:PipelineVerification]`, `[RULE:DeterministicTesting]`, `[RULE:ZeroOracleTampering]`

---

## 2. 결함 분석 및 근본 원인
1. **맵 이터레이션 순서 무작위성 (Non-Deterministic Ordering)**:
   - `pkg/engine/sqlexec_dml.go`에서 `setPairs := sqlutil.SplitSetClauses(setClause)` 파싱 결과를 `updatesMap := make(map[string]string)`에 담아 순회(`for colName, exprStr := range updatesMap`)함으로써, Go 런타임의 맵 순회 랜덤화로 인해 동일 컬럼 중복 대입 또는 대소문자 변형 컬럼(`a`, `A`) 간 덮어쓰기 순서가 비결정적이었음.
2. **스냅샷 불변성 보장 (Defensive Snapshot Isolation)**:
   - 각 레코드 갱신 시 `cursor.Record()`로부터 얻은 원본 레코드가 복제되지 않고 표현식 평가기(`evalUpdateExpr`)에 그대로 전달되는 경우, 하위 함수나 UDF 평가에 의해 원본 레코드가 오염될 잠재적 위험이 존재함.
   - 모든 대입 표현식 평가의 우변(RHS)은 반드시 갱신 전 불변 원본 복제본(`origSnapshot := rec.Clone()`)을 참조하여 평가되어야 함.

---

## 3. 구현 세부 계획

### 3.1 DML 엔진 (`pkg/engine/sqlexec_dml.go`) 리팩토링
- `updatesMap` 제거 및 순차 `setPairs []SetPair` 직접 순회:
  ```go
  setPairs := sqlutil.SplitSetClauses(setClause)
  for _, pair := range setPairs {
      if _, _, found := schema.FindColumn(pair.Column); !found {
          if isEphemeral && tx != nil {
              _ = tx.Rollback()
          }
          return nil, fmt.Errorf("column %q does not exist in table %q", pair.Column, tableName)
      }
  }
  ```
- 튜플 순회 시 원본 복제본 스냅샷 기반 평가 강제:
  ```go
  origSnapshot := rec.Clone()
  recUpdates := make(storage.Record, len(setPairs))
  for _, pair := range setPairs {
      colName := pair.Column
      exprStr := pair.Value
      colSchema, _, found := schema.FindColumn(colName)
      targetType := storage.TypeVarChar
      targetCol := colName
      if found {
          targetCol = colSchema.Name
          targetType = colSchema.Type
      }
      val, err := evalUpdateExpr(exprStr, origSnapshot, targetType)
      if err != nil {
          if isEphemeral && tx != nil {
              _ = tx.Rollback()
          }
          return nil, err
      }
      recUpdates.Set(targetCol, val)
  }
  ```

### 3.2 VDBE 가상 머신 및 실행기 확인
- `pkg/proc/runtime/vm_dispatcher.go` (`OpUpdate` 고속 경로 및 일반 경로)
- `pkg/proc/runtime/vm.go` (`execUpdateWhere`)
- `pkg/proc/evaluator.go` (`*UpdateStmt`)
- 모든 경로에서 이미 `oldRow`, `oldRec`, `rowCtx`가 우변 평가의 불변 스냅샷으로 격리되어 있음을 확인하고, 포괄적 회귀 테스트로 상호 검증.

---

## 4. 검증 시나리오 및 테스트 스위트
[`pkg/engine/update_swap_test.go`](pkg/engine/update_swap_test.go):
1. **기본 컬럼 스왑**: `UPDATE swap_tbl SET a = b, b = a WHERE id = 1`
2. **자기 참조 산술 스왑**: `UPDATE swap_tbl SET a = a + b, b = a WHERE id = 1` (a는 30, b는 원본 a인 20)
3. **OUTPUT 절 연계 스왑**: `UPDATE swap_tbl SET a = b, b = a OUTPUT INSERTED.a, INSERTED.b WHERE id = 2`
4. **전체 테이블 다중 행 스왑**: `UPDATE swap_tbl SET a = b, b = a` (WHERE 절 없이 모든 행 동시 스왑)
5. **저장 프로시저 내 스왑**: `CREATE PROCEDURE sp_swap_test ... EXEC sp_swap_test`
6. **기본키 없는 테이블(합성 _rowid) 스왑**: `CREATE TABLE no_pk_swap (a INT, b INT)`
7. **역순 대입 순서 스냅샷 불변성 검증**: `UPDATE swap_tbl SET b = a, a = a + b WHERE id = 1`
8. **OUTPUT 절과 역순 대입 결합 검증**: `UPDATE swap_tbl SET b = a, a = a + b OUTPUT ...`
9. **WiredTiger 스토리지 엔진 스왑 검증**: `update_swap_wt_test.go`
