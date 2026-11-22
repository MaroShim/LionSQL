# PGWire 확장 쿼리(Extended Query) 복수 결과 집합 및 프로시저 스키마 유실 결함 분석 및 해결 계획서
> **문서 식별자**: `notes/PGWIRE_EXTENDED_QUERY_MULTIPLE_RESULT_SETS_PLAN.ko.md`  
> **결함 식별자**: `DEFECT-PGWIRE-EXTENDED-QUERY-MULTIPLE-RESULT-SETS-1`  
> **우선순위**: Priority 1 (High - PostgreSQL Wire Protocol Compliance & Client Interoperability)  
> **관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:PipelineVerification]`, `[RULE:ExplicitError]`, `[RULE:RadicalKISS]`

---

## 1. 결함 개요 (Defect Overview)

PostgreSQL Frontend/Backend Protocol v3.0 규약에 따르면, Simple Query(`'Q'`)뿐만 아니라 확장 쿼리 프로토콜(Extended Query Protocol: `Parse ('P')` ➔ `Bind ('B')` ➔ `Describe ('D')` ➔ `Execute ('E')` ➔ `Sync ('S')`)에서도 실행 대상(Portal)이 다중 결과 집합을 반환하는 저장 프로시저(`EXEC sp_name` 또는 `CALL sp_name`)인 경우, 후속 결과 집합들이 정상적으로 반환되어야 합니다.

현재 LionSQL 엔진은 다음 두 가지 치명적 결함으로 인해 PostgreSQL 드라이버(`pgx`, `lib/pq`, C# Npgsql, Java JDBC)에서 프로시저 실행 및 다중 결과 집합 탐색(`rows.NextResultSet()`) 시 장애가 발생합니다:

1. **`Describe ('D')` 시 프로시저 컬럼 메타데이터 누락 및 `NoData ('n')` 오반환**:
   - `writeSchemaDescription` (`pkg/wire/server.go:1685`)은 `prog.ResultColumns()`를 검사하여 `RowDescription ('T')`을 반환합니다.
   - 그러나 `prog.ResultColumns()` (`pkg/proc/bytecode.go:637`)는 `*ResultRowData` 및 `*PointSelectData`만 검사하고, 저장 프로시저 컴파일러가 생성하는 `*RelQueryData`를 완전히 무시하여 `nil`을 반환합니다.
   - 또한 컴파일러(`pkg/proc/compiler.go:1261`)는 프로시저 내 `SELECT` 문을 컴파일할 때 `p.Alias != ""`인 경우만 프로젝션 이름을 수집하여 별칭이 없는 컬럼명이 누락되었습니다.
   - 결과적으로 드라이버는 `NoData`를 수신하여 "컬럼 수가 0개"라고 인식하므로 `rows.Scan()` 시 `expected 0 destination arguments in Scan, not 2` 패닉/에러가 발생합니다.

2. **`handleExecute ('E')` 시 후속 결과 집합(`res.Next`) 전송 누락**:
   - `handleExecute` (`pkg/wire/server.go:1901~1982`)는 `portal.Result`의 첫 번째 결과 집합만 직렬화하고, `res.Next`에 연결된 2번째 이후의 결과 집합들을 순회하지 않고 단일 `CommandComplete ('C')` 전송 후 즉시 종료합니다.
   - PostgreSQL 프로토콜 규격에 따르면 포털 실행이 다중 결과 집합을 생성할 경우, 2번째 이후의 각 결과 집합에 대해 `RowDescription ('T')` ➔ `DataRow ('D')` ➔ `CommandComplete ('C')`가 순차 발행되어야 합니다.
   - 이 누락으로 인해 클라이언트 드라이버가 `rows.NextResultSet()` 호출 시 즉시 `false`를 수신하여 후속 데이터셋이 유실됩니다.

3. **`ResolvePreparedProcedure`의 `sp_` 무차별 배제**:
   - `pkg/engine/proc_bind.go:39`의 `strings.HasPrefix(lowerName, "sp_")` 조건으로 인해 사용자가 정의한 `sp_...` 프로시저가 시스템 프로시저로 오인되어 VDBE 사전 컴파일 경로에서 배제되었습니다.

---

## 2. 해결 방안 (Implementation Plan)

### Step 1: 바이트코드 결과 컬럼 검출 보강 (`pkg/proc/bytecode.go`)
- `ResultColumns()`에서 `aux.(*RelQueryData)`를 검사하여 `relData.Projections`가 존재하면 즉시 반환.
- `pkg/proc/compiler.go:1261`에서 `SELECT` 프로젝션 컴파일 시 별칭이 없더라도 `ColumnRefExpr`의 컬럼명 또는 표현식 문자열을 `projNames`에 보존.

### Step 2: `isBuiltinSystemProcedure` 정확한 화이트리스트 필터링 (`pkg/engine/proc_bind.go`)
- `strings.HasPrefix(lowerName, "sp_")` 단순 접두사 검사를 실제 내장 시스템 프로시저 화이트리스트(`isBuiltinSystemProcedure`)로 교체하여 사용자 정의 `sp_...` 프로시저의 VDBE 사전 컴파일 정상 지원.

### Step 3: `handleExecute` 다중 결과 집합 스트리밍 (`pkg/wire/server.go`)
- `handleExecute`에서 첫 번째 결과 집합 완료(`WriteCommandComplete`) 후 `for cur := res.Next; cur != nil; cur = cur.Next` 루프 추가.
- `len(cur.Columns) > 0`인 경우:
  - `WriteRowDescription(bw, curCols)` 작성
  - `writePgDataRows(bw, cur.Columns, curOIDs, portal.ResultFormats, cur.Rows, cur.Values, false)` 작성
  - `WriteCommandComplete(bw, curTag)` 작성
- 모든 결과 집합 직렬화 후 버퍼 플러시(`bw.Flush()`).

---

## 3. TDD 검증 계획 (Verification)
1. **Red Test**: `test/spec_defects/spec_defect_pgwire_multiset_test.go`에서 `pgx/stdlib` 드라이버로 `sp_pg_multi_dataset` 호출 시 `expected 0 destination arguments` 및 2번째 결과 집합 유실 재현 확인.
2. **Green Test**: 엔진 수정 후 1번째, 2번째 결과 집합이 정상 스캔되고 `rows.NextResultSet()`이 true/false 생명주기를 완벽히 따르는지 확인.
3. **회귀 검증**: `go test -v -race ./pkg/wire/... ./pkg/proc/...` 및 `go build` 무결성 검증.
4. **동기화**: `CHANGELOG.md` 및 `TASKS.md` 갱신.
