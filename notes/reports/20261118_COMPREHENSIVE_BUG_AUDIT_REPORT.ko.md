# LionSQL 엔진 종합 체계적 버그 감사 보고서 (`notes/reports/20261118_COMPREHENSIVE_BUG_AUDIT_REPORT.ko.md`)

> - **작성일자**: 2026-11-18
> - **감사 대상**: LionSQL Database Engine (Volcano / VDBE / WiredTiger / MemEngine)
> - **감사 방법론**: 5-Pattern Static AST/Grep Audit + Dual-Path Differential Verification
> - **조치 계획서**: [`notes/20261118_BUG_REMEDIATION_PLAN.ko.md`](../20261118_BUG_REMEDIATION_PLAN.ko.md)
> - **조치 상태**: 전 16건 수정 완료 및 회귀 테스트 통과 (`COMPLETED`)

---

## 1. 총괄 요약: 5대 취약 패턴 전수 스캔 16개 핵심 결함

| 번호 | 결함 ID | 심각도 | 취약 패턴 분류 | 핵심 결함 내용 및 영향 |
| :--- | :--- | :--- | :--- | :--- |
| **A-1** | `FIX-WT-COMPOSITE-PK-INDEX-SCAN` | **치명적** | [패턴 A] 복합 PK 파싱 | WiredTiger 보조 인덱스 스캔 시 복합 PK 파싱 실패로 조회 0건 증발 (NoREC 불일치) |
| **A-2** | `FIX-WT-FK-CASCADE-COMPOSITE-PK` | **치명적** | [패턴 A] 복합 PK 파싱 | WiredTiger 외래키 ON DELETE CASCADE / SET NULL 복합 PK 자식 삭제 누락 |
| **A-3** | `FIX-CTE-COMPOSITE-PK-OVERWRITE` | **치명적** | [패턴 A] 단일 PK 가정 | Updatable CTE에서 복합 PK 덮어쓰기 및 무PK 테이블 첫 컬럼 기준 대량 오삭제 |
| **A-4** | `FIX-UPDATE-FROM-JOIN-PROPAGATION-1` | **치명적** | [패턴 A] 단일 PK 가정 | UPDATE / DELETE ... FROM JOIN 복합 PK 테이블 무반영 및 거짓 성공 카운트 반환 |
| **A-5** | `FIX-MERGE-COMPOSITE-PK-TARGET` | **높음** | [패턴 A] 단일 PK 가정 | MERGE INTO 구문에서 복합 PK 타깃 테이블 매칭/갱신/삭제 오동작 |
| **A-6** | `FIX-PITR-COMPOSITE-PK-WAL-REPLAY` | **높음** | [패턴 A] 복합 PK 파싱 | PITR(시점 복구) WAL 재생 시 복합 PK 테이블 UPDATE/DELETE 복구 유실 |
| **B-1** | `FIX-STR-COMPARE-CANONICAL-STRIP-1` | **치명적** | [패턴 B] 식별자 함수 오용 | Volcano 범용 비교기(CompareValues)의 대괄호/따옴표 무단 제거 오매칭 (`'[ADMIN]' = 'ADMIN'`) |
| **B-2** | `FIX-HASHJOIN-CANONICAL-KEY-STRIP` | **치명적** | [패턴 B] 식별자 함수 오용 | Hash Join 조인 키 생성기(formatKeyVal)의 대괄호/따옴표 제거로 인한 오조인 (False Positive) |
| **C-1** | `FIX-SP-NULL-EQUAL-3VL-1` | **치명적** | [패턴 C] 3VL 파괴 | 저장 프로시저/절차적 평가기(evaluator.go) 내 NULL = NULL TRUE 오평가 (`IF @a = @b` 역전) |
| **D-1** | `FIX-DIV-BY-ZERO-ERROR-VISIBILITY-1` | **보통** | [패턴 D] 에러 무시 | 일반 나눗셈(/) 연산자의 0 나누기 에러 은폐 및 사일런트 NULL 반환 |
| **D-2** | `FIX-INT64-ARITHMETIC-OVERFLOW-1` | **보통** | [패턴 D] 오버플로 경계 | 64비트 정수(int64) 산술 연산 오버플로 미검출 및 음수 래핑(Wrap-around) |
| **D-3** | `FIX-SUBSTRING-NONPOSITIVE-POS-1` | **보통** | [패턴 D] 함수 시맨틱 오류 | 2인자 SUBSTRING(str, pos)에서 pos <= 0일 때 문자열 우측 절삭 오류 |
| **D-4** | `FIX-DATEDIFF-BOUNDARY-CROSSING-1` | **보통** | [패턴 D] T-SQL 시맨틱 | T-SQL DATEDIFF(week/hour/minute) 경계선 통과 횟수 미인식 오류 |
| **D-5** | `FIX-WINDOW-ARG-INVALID-COLUMN-1` | **보통** | [패턴 D] 시맨틱 검증 누락 | 윈도우 함수 본체 인자(winFn.Arg) 및 ORDER BY 비유효 컬럼/서수 검증 누락 |
| **E-1** | `FIX-SETOP-OPEN-CURSOR-LEAK` | **높음** | [패턴 E] 커서 누수 | SetOpIterator.Open() 우측 자식 Materialization 에러 시 양쪽 커서 누수 (`EBUSY`) |
| **E-2** | `FIX-MERGEJOIN-OPEN-CURSOR-LEAK` | **높음** | [패턴 E] 커서 누수 | MergeJoinIterator.Open() 초기 튜플 Pull 에러 시 양쪽 자식 커서 누수 (`EBUSY`) |

---

## 2. [패턴 A] 단일 PK 가정(PrimaryKeyColumn)으로 인한 복합 PK / 무PK 데이터 오염군

### 근본 배경
`storage.TableSchema`에는 이미 복합 기본키를 처리하는 `IsCompositePK()`, `ResolveRecordPK(rec)`, `ResolveRowPK(row)`가 구현되어 있습니다(`\x1f` 구분자 결합).  
그러나 엔진 곳곳에서 단일 컬럼만 반환하는 `schema.PrimaryKeyColumn()`을 호출하여 복합 키의 첫 번째 컬럼만 사용하거나 타입 변환에 실패하는 구조적 결함이 존재했습니다.

---

### A-1. WiredTiger 보조 인덱스 스캔 시 복합 PK 파싱 실패로 조회 0건 증발 (NoREC 불일치)
- **심각도**: 치명적 (인덱스 유무에 따라 SELECT 결과가 증발함)
- **코드 위치**: `pkg/storage/wiredtiger/wt_engine.go:3786-3828` (`wtIndexBTreeCursor`)
```go
func (c *wtIndexBTreeCursor) Row() (storage.Row, error) {
    ...
    pkCol, err := c.tbl.PrimaryKeyColumn()
    pkVal := storage.ParsePKValue(c.currPK, pkCol.Type)
    return c.tx.GetRow(c.tbl.Name, pkVal)
}
```
- **원인 분석**:
  - 복합 PK 테이블(`PRIMARY KEY (k1 INT, k2 INT)`)에 보조 인덱스(`CREATE INDEX`)를 생성하면 인덱스 엔트리의 `c.currPK`에는 복합 문자열 `"1\x1f20"`이 저장됩니다.
  - 그러나 `Row()`, `Record()`, `RawTuple()`에서 `c.tbl.PrimaryKeyColumn()`을 호출해 첫 번째 컬럼 타입(`TypeInt`)을 가져온 뒤 `storage.ParsePKValue("1\x1f20", TypeInt)`를 실행합니다.
  - `strconv.ParseInt("1\x1f20", 10, 64)`가 실패하여 `IntValue(0)`을 반환하고, `c.tx.GetRow`가 키 `0`을 찾다가 `ErrKeyNotFound`로 실패합니다.
- **재현 쿼리 (NoREC 검증)**:
```sql
CREATE TABLE _t_idx_comp (k1 INT, k2 INT, val INT, PRIMARY KEY (k1, k2));
CREATE INDEX idx_comp_val ON _t_idx_comp(val);
INSERT INTO _t_idx_comp VALUES (1, 10, 500), (1, 20, 500);

-- 인덱스 스캔 경유 시 0건 또는 조회 실패 발생 (전수 스캔 시 2건)
SELECT * FROM _t_idx_comp WHERE val = 500;
```
- **수정 방안**:
  `c.tbl.IsCompositePK()`가 true인 경우 `ParsePKValue`를 거치지 않고 `pkVal := storage.StringValue(c.currPK)`를 그대로 사용하도록 수정.

---

### A-2. WiredTiger 외래키 ON DELETE CASCADE / SET NULL 복합 PK 자식 삭제 누락
- **심각도**: 치명적 (참조 무결성 파괴 및 고아 레코드 발생)
- **코드 위치**: `pkg/storage/wiredtiger/wt_engine.go:3260, 3294-3297` (`checkReferencingFKsOnDeleteLocked`)
```go
cPkCol, err := cTbl.PrimaryKeyColumn()
...
if rErr == nil && cTbl.RowMatchesFK(row, cr.fk.ChildColumns, parentValues) {
    _, pkIdx, _ := cTbl.FindColumn(cPkCol.Name)
    if pkIdx >= 0 && pkIdx < len(row.Values) {
        matchingPKs = append(matchingPKs, row.Values[pkIdx])
    }
}
```
- **원인 분석**:
  - `memengine`(`pkg/storage/memengine/tx.go:1368`)은 `childSchema.ResolveRowPK(row)`를 사용하여 복합 PK 자식의 키를 정상 추출합니다.
  - 반면 WiredTiger(`wt_engine.go:3294`)는 자식 테이블이 복합 PK(`PRIMARY KEY (order_id, item_id)`)일 때 첫 번째 컬럼(`order_id`) 값만 추출하여 `tx.deleteLocked(cr.childTable, cpk)`에 전달합니다.
  - WiredTiger에 저장된 실제 키는 `"1\x1f10"`이므로 단일 키 `"1"` 삭제가 조용히 무시되어, 부모 행이 삭제된 후에도 자식 행이 그대로 남습니다.
- **수정 방안**:
  `wt_engine.go:3294`에서도 `cPkCol` 대신 `if childPK, ok := cTbl.ResolveRowPK(row); ok`를 사용하여 `matchingPKs`에 추가하도록 수정.

---

### A-3. Updatable CTE에서 복합 PK 덮어쓰기 및 무PK 테이블 첫 컬럼 기준 대량 오삭제
- **심각도**: 치명적 (대량 데이터 오염 및 무관한 행 증발)
- **코드 위치**: `pkg/engine/sqlexec_explain.go:1609-1662` (`execUpdatableCTE`)
```go
pkCol, err := baseSchema.PrimaryKeyColumn()
pkName := ""
if err == nil && pkCol != nil {
    pkName = pkCol.Name
} else if len(baseSchema.Columns) > 0 {
    pkName = baseSchema.Columns[0].Name
}
...
delSQL := fmt.Sprintf("DELETE FROM %s WHERE %s IN (%s)", baseTableName, pkName, inClause)
```
- **원인 분석**:
  1. **복합 PK 테이블**: `baseSchema.IsCompositePK()`일 때도 첫 번째 PK 컬럼(`k1`)만으로 `WHERE k1 IN (...)`을 실행하여, 같은 `k1`을 공유하는 다른 모든 행을 덮어쓰거나 삭제합니다.
  2. **무PK 테이블(또는 PK 미감지 시)**: `baseSchema.Columns[0].Name`(첫 번째 일반 컬럼, 예: `dept_id`)을 키로 삼아 `DELETE FROM emp WHERE dept_id IN (10)`을 실행합니다. 중복 제거를 위해 1건만 지우려다 같은 부서의 모든 직원이 통째로 삭제됩니다.
- **재현 쿼리**:
```sql
CREATE TABLE _t_cte_comp (k1 INT, k2 INT, val INT, PRIMARY KEY (k1, k2));
INSERT INTO _t_cte_comp VALUES (1, 10, 100), (1, 20, 200);
WITH cte AS (SELECT k1, k2, val FROM _t_cte_comp)
UPDATE cte SET val = 999 WHERE k2 = 20;
-- 결과: (1, 10) 행까지 999로 덮어써짐!
```
- **수정 방안**:
  기저 테이블의 모든 PK 컬럼 목록(`PrimaryKeyColumns` 또는 `SyntheticPKColumn`)을 추출하여 복합 조건 `(k1 = v1 AND k2 = v2)` 형태로 타깃 행을 특정하도록 수정.

---

### A-4. UPDATE / DELETE ... FROM JOIN 복합 PK 테이블 무반영 및 거짓 카운트 반환
- **심각도**: 치명적 (Silent Failure 및 거짓 성공 응답)
- **코드 위치**: `pkg/engine/sqlexec_dml.go:1509, 1581` (`execDeleteFromJoin`, `execUpdateFromJoin`)
- **원인 분석**:
  - 드라이빙 SELECT 쿼리가 단일 `pkCol.Name AS __lionsql_pk__`만 프로젝션합니다.
  - 복합 PK 테이블에서는 `tx.Get(targetTable, pkVal)` 및 `tx.Delete(targetTable, pkVal)`이 복합 키(`"1\x1f20"`) 대신 단일 정수 `1`로 호출되어 매칭에 실패합니다.
  - 특히 `DELETE ... FROM JOIN`은 실제로는 0건을 지웠음에도 `DELETE 2`를 반환합니다.
- **수정 방안**:
  `schema.IsCompositePK()`인 경우 모든 PK 컬럼을 드라이빙 쿼리에서 조회한 뒤 `schema.ResolveRecordPK`와 동일하게 `"\x1f"`로 결합한 `StringValue`로 `tx.Get`/`tx.Delete` 호출.

---

### A-5. MERGE INTO 구문에서 복합 PK 타깃 테이블 매칭/갱신/삭제 오동작
- **심각도**: 높음 (복합 PK 테이블 MERGE 실패)
- **코드 위치**: `pkg/engine/sqlexec_merge.go:67, 135, 357, 370, 386, 478, 495`
```go
tRec := targetRows[matchedTargetIdx]
pkVal := tRec[pkCol.Name]
...
tx.Update(parsed.targetTable, pkVal, updatedRec)
```
- **원인 분석**:
  - Fallback 스캔 경로(Path B) 및 `WHEN NOT MATCHED BY SOURCE` 경로에서 타깃 레코드의 키를 `tRec[pkCol.Name]`으로만 추출하여 `tx.Update`와 `tx.Delete`에 전달합니다.
  - 또한 `matchedTargetKeys`와 `newlyInsertedKeys` 맵의 키도 첫 번째 PK 컬럼 값만 사용하여, 복합 PK 테이블에서 `MERGE` 실행 시 타깃 레코드 갱신/삭제가 실패하거나 중복 판정이 깨집니다.
- **수정 방안**:
  `tRec[pkCol.Name]` 대신 `pkVal, _ := targetSchema.ResolveRecordPK(tRec)`을 일관되게 사용.

---

### A-6. PITR(시점 복구) WAL 재생 시 복합 PK 테이블 UPDATE/DELETE 복구 유실
- **심각도**: 높음 (백업/시점 복구 데이터 유실)
- **코드 위치**: `pkg/backup/pitr.go:228-257`
```go
pkCol, err := tblSchema.PrimaryKeyColumn()
...
pkVal := memengine.ParsePKValue(op.Key, pkCol.Type)
tx.Delete(op.Table, pkVal)
```
- **원인 분석**:
  - WAL에 기록된 복합 PK 테이블의 `op.Key`는 `"1\x1f20"` 형태의 문자열입니다.
  - 그러나 PITR 복구 시 `OpUpdate`는 `rec[pkCol.Name]`만 추출하고, `OpDelete`는 `ParsePKValue(op.Key, pkCol.Type)`으로 첫 번째 컬럼 타입(예: `TypeInt`) 파싱을 시도하여 `0`으로 변환됨에 따라 변경분 적용이 실패합니다.
- **수정 방안**:
  `tblSchema.IsCompositePK()`일 때는 `ResolveRecordPK(rec)` 및 `storage.StringValue(op.Key)`를 사용.

---

## 3. [패턴 B] 식별자 정규화 함수(CanonicalIdent)의 데이터 값 오용군

### 근본 배경
`sqlutil.CanonicalIdent(s)`는 SQL 식별자(테이블/컬럼명)의 대괄호(`[...]`), 따옴표(`"..."`, `'...'`), 백틱(`` `...` ``)을 벗겨내는 함수입니다. 이를 실제 데이터 값(String Value) 처리에 사용하여 괄호나 따옴표가 포함된 데이터가 왜곡되었습니다.

---

### B-1. Volcano 범용 비교기(CompareValues)의 대괄호/따옴표 무단 제거 오매칭
- **심각도**: 치명적 (WHERE 필터, 정렬, 집계 비교 왜곡)
- **코드 위치**: `pkg/execution/iterator.go:689-690, 732-733` (`CompareValues`)
```go
if isStrA && isStrB {
    cA := strings.TrimRight(sqlutil.CanonicalIdent(sa), " ")
    cB := strings.TrimRight(sqlutil.CanonicalIdent(sb), " ")
    return strings.Compare(cA, cB)
}
```
- **재현 쿼리**:
```sql
CREATE TABLE _t_bracket (id INT PRIMARY KEY, tag VARCHAR(20));
INSERT INTO _t_bracket VALUES (1, '[ADMIN]'), (2, 'ADMIN');
SELECT * FROM _t_bracket WHERE tag = 'ADMIN';
-- 결과: '[ADMIN]'과 'ADMIN' 2건이 모두 반환됨!
```
- **수정 방안**:
  `sqlutil.CanonicalIdent(sa)` 대신 `strings.ToLower(strings.TrimRight(sa, " "))`를 사용.

---

### B-2. Hash Join 조인 키 생성기(formatKeyVal)의 대괄호/따옴표 제거로 인한 오조인
- **심각도**: 치명적 (서로 다른 문자열 키끼리 조인되는 False-Positive Join)
- **코드 위치**: `pkg/execution/join.go:836-862` (`formatKeyVal`)
```go
func formatKeyVal(val any) string {
    switch v := val.(type) {
    case string:
        return sqlutil.CanonicalIdent(v)
    ...
    case storage.Value:
        if v.Type == storage.TypeVarChar {
            return sqlutil.CanonicalIdent(v.SVal)
        }
```
- **원인 분석**:
  - 해시 조인의 해시 테이블 키 생성 시 `sqlutil.CanonicalIdent`를 호출하여 `'[code1]'`, `'"code1"'`, `'code1'`이 모두 동일한 해시 버킷(`"code1"`)으로 들어갑니다.
- **수정 방안**:
  `formatKeyVal`에서 `sqlutil.CanonicalIdent` 호출을 제거하고 `strings.ToLower(strings.TrimRight(v, " "))`로 정규화.

---

## 4. [패턴 C] 스토리지 물리 동등성(Value.Equal) 오용으로 인한 3VL 파괴군

### C-1. 저장 프로시저/절차적 평가기(evaluator.go) 내 NULL = NULL TRUE 오평가
- **심각도**: 치명적 (프로시저 내 IF / WHILE 분기 역전)
- **코드 위치**: `pkg/proc/evaluator.go:399-401`
```go
case proc.TokenEqual:
    return storage.BoolValue(left.Equal(right)), nil
case proc.TokenNotEqual:
    return storage.BoolValue(!left.Equal(right)), nil
```
- **원인 분석**:
  - `storage.Value.Equal`(`pkg/storage/engine.go:1191`)은 내부 자료구조 키 비교를 위해 `if v.IsNull && other.IsNull { return true }`로 정의되어 있습니다.
  - 이를 SQL 절차적 비교 연산자에 그대로 사용하여 `@a = @b` (둘 다 NULL)가 `TRUE`가 되고, `@a <> 10` (`@a`가 NULL)이 `TRUE`가 되어 ANSI 3-Valued Logic이 완전히 깨졌습니다.
- **수정 방안**:
  `evaluator.go`의 비교 연산(`=`, `<>`, `<`, `>`, `<=`, `>=`) 진입부에서 `if left.IsNull || right.IsNull { return storage.NullValue(storage.TypeBool), nil }` 가드 추가.

---

## 5. [패턴 D] 에러 무시(nil 폴백) 및 수식/함수/시맨틱 경계 누락군

### D-1. 일반 나눗셈(/) 연산자의 0 나누기 에러 은폐 및 사일런트 NULL 반환
- **심각도**: 보통 (`[RULE:ExplicitError]` 및 SQL 표준 위반)
- **코드 위치**: `pkg/proc/runtime/vm.go:3471-3479` (`storageDiv`), `pkg/execution/eval.go:560, 585`
- **증상 및 원인**:
  `SELECT 10 % 0`은 `division by zero` 에러를 반환하나, `SELECT 10 / 0`은 분모가 0일 때 에러 대신 `NullValue`를 조용히 반환했습니다.
- **수정 방안**:
  분모가 0일 때 `fmt.Errorf("divide by zero error encountered")`를 일관되게 반환.

---

### D-2. 64비트 정수(int64) 산술 연산 오버플로 미검출 및 음수 래핑(Wrap-around)
- **심각도**: 보통 (수치 연산 정합성 훼손)
- **코드 위치**: `pkg/execution/eval.go:578-583`, `pkg/proc/runtime/vm.go:379, 395, 411`
- **재현 쿼리**:
  - `SELECT 9223372036854775807 * 2;` -> `-2` 반환
  - `SELECT 9223372036854775807 + 1;` -> `-9223372036854775808` 반환
- **수정 방안**:
  64비트 정수 덧셈/뺄셈/곱셈 시 오버플로 감지 조건을 추가하여 `arithmetic overflow error` 반환.

---

### D-3. 2인자 SUBSTRING(str, pos)에서 pos <= 0일 때 문자열 우측 절삭 오류
- **심각도**: 보통 (내장 함수 결과 오류)
- **코드 위치**: `pkg/proc/builtins_string.go:431-454`
- **재현 쿼리**:
  `SELECT SUBSTRING('hello', 0), SUBSTRING('hello', -2);` -> `'hell'`, `'he'` 반환 (기대값: `'hello'`)
- **수정 방안**:
  `len(args) == 2`인 경우 끝까지 추출하는 것이므로 `rawStart < 1`일 때 `start = 0`, `end = len(runes)`로 고정.

---

### D-4. T-SQL DATEDIFF(week/hour/minute) 경계선 통과 횟수 미인식 오류
- **심각도**: 보통 (T-SQL 날짜 함수 호환성 결함)
- **코드 위치**: `pkg/proc/builtins_date.go:131-140` (`evalDateDiff`)
- **재현 쿼리**:
  `SELECT DATEDIFF(week, '2023-01-07', '2023-01-08'), DATEDIFF(hour, '2023-01-01 23:59:59', '2023-01-02 00:00:01');`
  -> 둘 다 `0` 반환 (T-SQL 표준 기대값: 둘 다 `1`)
- **수정 방안**:
  단순 `diff.Hours()` 나눗셈 대신 각 datepart 단위(주/시/분/초)로 양쪽 시각을 Truncate한 후 차이 계산.

---

### D-5. 윈도우 함수 본체 인자(winFn.Arg) 및 ORDER BY 비유효 컬럼/서수 검증 누락
- **심각도**: 보통 (시맨틱 컴파일 에러 누락)
- **코드 위치**: `pkg/execution/window.go`, `pkg/planner/planner.go`, `pkg/execution/sort.go`
- **재현 쿼리**:
  1. `SELECT id, AVG(non_existent_col) OVER (PARTITION BY dept_id) FROM t;` -> 에러 없이 NULL 반환
  2. `SELECT id, val FROM t ORDER BY non_existent_col;` -> 에러 없이 무시됨
  3. `SELECT id, val FROM t ORDER BY 0;` 및 `ORDER BY 999;` -> 범위 초과 서수가 에러 없이 무시됨
- **수정 방안**:
  윈도우 함수의 `Arg` 표현식 및 `ORDER BY` 절의 컬럼명/서수 범위(`1 <= pos <= len(projections)`)를 플래너와 실행기 `Open()`에서 검증하여 명시적 에러 반환.

---

## 6. [패턴 E] Volcano 이터레이터 Open() 중도 실패 시 커서 누수 잔존 구간

### E-1. SetOpIterator.Open() 우측 자식 Materialization 에러 시 양쪽 커서 누수
- **심각도**: 높음 (WiredTiger 테이블 핸들 잠금 `EBUSY` 유발)
- **코드 위치**: `pkg/execution/setop.go:146-164` (`SetOpIterator.Open`)
```go
if s.op == SetOpIntersect || s.op == SetOpExcept {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err() // s.left.Close(), s.right.Close() 누락!
        ...
        t, err := s.right.Next(ctx)
        if err != nil {
            return fmt.Errorf("set operator right child materialization error: %w", err) // 누락!
        }
```
- **수정 방안**:
  `SetOpIterator.Open(ctx context.Context) (retErr error)`로 선언하고, 함수 상단에 `defer func() { if retErr != nil { _ = s.left.Close(); _ = s.right.Close() } }()` 추가.

---

### E-2. MergeJoinIterator.Open() 초기 튜플 Pull 에러 시 양쪽 자식 커서 누수
- **심각도**: 높음 (WiredTiger 테이블 핸들 잠금 `EBUSY` 유발)
- **코드 위치**: `pkg/execution/merge_join.go:131-152` (`MergeJoinIterator.Open`)
```go
lTuple, err := m.left.Next(ctx)
if err != nil {
    if err == io.EOF {
        m.leftEOF = true
    } else {
        return err // 이미 열린 m.left, m.right의 Close() 누락!
    }
}
```
- **수정 방안**:
  `MergeJoinIterator.Open(ctx context.Context) (retErr error)`로 선언하고, 에러 반환 시 `m.left`와 `m.right`를 닫는 `defer` 클린업 가드 추가.
