# EXISTS (SELECT ...) 암시적 LIMIT 1 푸시다운 및 1건 조기 탈출 구현 계획서 (`EXISTS_SUBQUERY_LIMIT1_PLAN.ko.md`)

## 1. 개요
* **태스크 ID**: `PERF-EXISTS-SUBQUERY-LIMIT-1` (결함 73)
* **목표**: `EXISTS (SELECT ...)` 및 `NOT EXISTS (SELECT ...)` 구문 평가 시, 하위 쿼리가 수십만~수백만 건의 레코드를 불필요하게 스캔하고 메모리에 버퍼링하는 I/O 낭비를 제거하기 위해, 암시적 `LIMIT 1` 푸시다운 및 단 1건 확인 즉시 조기 탈출(Early Exit)을 적용한다.
* **관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`, `[RULE:PipelineVerification]`, `[RULE:DeterministicTesting]`, `[RULE:ZeroOracleTampering]`

---

## 2. 결함 분석 및 해결 방안
1. **문제점**:
   - `SELECT EXISTS(SELECT * FROM big_table)`
   - `SELECT ... WHERE EXISTS (SELECT * FROM settings WHERE ...)` (비상관 서브쿼리)
   - `IF EXISTS (SELECT * FROM big_table) ...` (조건부 DDL 및 프로시저)
   - `pkg/proc/eval.go`의 `*ExistsExpr` 평가기
   이들 위치에서 `EXISTS` 하위 쿼리를 원본 그대로 실행하여 하위 쿼리의 일치 행 전체를 메모리에 적재한 뒤 `len(rows) > 0` 여부만 검사하고 버림.
2. **해결책**:
   - 하위 쿼리에 `LIMIT` 또는 `TOP` 절이 명시되지 않은 경우, 실행 전 암시적 `LIMIT 1`을 푸시다운하여 스토리지/VM 레이어에서 첫 번째 행을 읽는 즉시 스캔을 중단하고 반환하도록 최적화.
   - `pkg/proc/eval.go`의 `*ExistsExpr`: `e.Query.Limit < 0 && e.Query.LimitExpr == nil`인 경우 복제본에 `clone.Limit = 1` 주입.
   - `pkg/engine/sqlexec_select.go`, `sqlexec_explain.go`, `sqlexec_conditional_ddl.go`: `ensureExistsLimit1(rawSQL string)` 헬퍼를 통해 암시적 `LIMIT 1` 적용.

---

## 3. 검증 시나리오
- 10,000건 이상의 행이 존재하는 대용량 테이블에서:
  1. `SELECT EXISTS (SELECT 1 FROM big_table)`: 정상 true 반환
  2. `SELECT ... WHERE EXISTS (SELECT 1 FROM big_table)`: 정상 필터링
  3. `SELECT ... WHERE NOT EXISTS (SELECT 1 FROM big_table)`: 정상 필터링
  4. `IF EXISTS (SELECT 1 FROM big_table) ...`: 정상 분기 실행
  5. 하위 쿼리에서 정확히 1건만 반환되는지 확인.
