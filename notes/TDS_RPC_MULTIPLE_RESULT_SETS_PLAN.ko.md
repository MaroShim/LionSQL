# TDS RPC 복수 결과 집합(Multiple Result Sets) 유실 결함 분석 및 해결 계획서
> **문서 식별자**: `notes/TDS_RPC_MULTIPLE_RESULT_SETS_PLAN.ko.md`  
> **결함 식별자**: `DEFECT-TDS-RPC-MULTIPLE-RESULT-SETS-1`  
> **우선순위**: Priority 1 (High - Client Driver Compatibility / Spec Invariant)  
> **관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:PipelineVerification]`, `[RULE:ExplicitError]`, `[RULE:RadicalKISS]`

---

## 1. 결함 개요 (Defect Overview)

Microsoft SQL Server TDS(Tabular Data Stream) 프로토콜 및 클라이언트 생태계(Go `go-mssqldb`, C# ADO.NET / EF Core, Java JDBC, Python `pyodbc`)는 저장 프로시저 호출 시 복수의 `SELECT` 쿼리가 생성하는 결과 집합(Multiple Result Sets)을 순차적으로 탐색할 수 있어야 합니다 (`rows.NextResultSet()`, `SqlDataReader.NextResult()`).

현재 LionSQL 엔진은 원시 SQL 배치(`PacketSQLBatch` `0x01`, 예: `EXEC sp_dual;`)에 대해서는 `executeSQLAndRespond`의 `cur = cur.Next` 루프를 통해 다중 결과 집합을 정상 스트리밍하고 있습니다.

그러나 ADO.NET의 `CommandType.StoredProcedure`나 `go-mssqldb`의 RPC 호출(`PacketRPC` `0x03`, 예: `db.QueryContext(ctx, "sp_name", sql.Named(...))`)을 통해 저장 프로시저 또는 파라미터화된 쿼리가 실행될 경우, VDBE 바이트코드 가속 경로인 `executeVDBEPreparedFastPath`를 거쳐 `server.go`의 `respondExecutionResult`가 응답을 생성합니다.

이때 `respondExecutionResult`는 오직 첫 번째 결과 집합(`res.Columns`, `res.Rows`)만을 와이어에 직렬화하고, `res.ResultSets[1:]`에 저장된 후속 결과 집합들을 완전히 무시한 채 즉시 `WriteReturnStatusDirect` 및 `TokenDoneProc (DoneFinal)` 토큰을 전송하여 스트림을 종결합니다.

결과적으로 클라이언트 드라이버가 후속 결과 집합을 읽기 위해 `rows.NextResultSet()`을 호출하면 즉시 `false`가 반환되어 2번째 이후의 모든 데이터셋이 영구 유실되는 중대한 호환성 결함이 발생합니다.

---

## 2. 프로토콜 토큰 불변성 (MS-TDS RPC Protocol Invariants)

[MS-TDS] 2.2.7 규격에 따르면 RPC 응답 토큰 시퀀스는 다음과 같이 프레이밍되어야 합니다:

1. **결과 집합 1 .. N**:
   - `COLMETADATA (0x81)`: 컬럼 수 및 타입/이름 메타데이터
   - `ROW (0xD1)`: 레코드 페이로드 스트림
   - `DONEINPROC (0xFF)`: 상태 플래그 `DoneMore (0x01) | DoneCount (0x10)`, 명령 코드 `0xC1`, 해당 결과 집합의 행 수
2. **최종 종료 프레이밍**:
   - `RETURNSTATUS (0x79)`: 프로시저 반환 코드 (기본 `0x00000000`)
   - `RETURNVALUE (0xAC)`: `OUTPUT` 파라미터가 있을 경우 순차 출력
   - `DONEPROC (0xFE)`: 최종 완료 플래그 `DoneFinal (0x00)` (트랜잭션 내일 경우 `DoneInXact (0x01)`), 행 수

---

## 3. 근본 원인 분석 (Root Cause Analysis)

`pkg/proc/runtime/vm.go`는 VDBE 실행 중 발생한 다중 `OpRelQuery` 결과 집합을 `res.ResultSets []VMResultSet`에 완벽하게 보존합니다:
- `res.ResultSets[0]`: 1번째 결과 집합 (`Columns`, `Rows`, `Values`)
- `res.ResultSets[1]`: 2번째 결과 집합 (`Columns`, `Rows`, `Values`)

그러나 `pkg/wire/tds/server.go`의 `respondExecutionResult` (1480~1530행):
```go
if len(res.Columns) > 0 {
    // res.Columns 와 res.Rows 만 1회 직렬화
    ...
    WriteDoneDirect(buf, TokenDoneInProc, DoneMore|DoneCount, 0xC1, uint64(len(res.Rows)))
    WriteReturnStatusDirect(buf, returnStatus)
    if extraTokens != nil {
        extraTokens(buf)
    }
    WriteDoneDirect(buf, TokenDoneProc, doneStatus, 0xC1, uint64(len(res.Rows)))
}
```
`res.ResultSets` 슬라이스를 일절 검사하지 않고 단일 결과 집합만 전송한 후 스트림을 종료하고 있었습니다.

---

## 4. 해결 방안 (Implementation Plan)

`pkg/wire/tds/server.go`의 `respondExecutionResult`를 리팩토링합니다:
1. `len(res.ResultSets) > 1`인 경우:
   - `res.ResultSets`를 순회하며 유효한 컬럼이 있는 각 결과 집합에 대해:
     - `s.inferColumnInfos`로 메타데이터 생성
     - `BuildColMetaData` 와이어 패킷 작성
     - `s.streamQueryRows`로 행 데이터 스트리밍
     - `WriteDoneDirect(buf, TokenDoneInProc, DoneMore|DoneCount, 0xC1, uint64(len(rs.Rows)))` 기록
   - 모든 결과 집합 스트리밍 완료 후:
     - `WriteReturnStatusDirect(buf, returnStatus)` 기록
     - `extraTokens(buf)` (OUTPUT 파라미터) 기록
     - `WriteDoneDirect(buf, TokenDoneProc, doneStatus, 0xC1, uint64(len(res.Rows)))` 기록
2. 단일 결과 집합(`len(res.ResultSets) <= 1 && len(res.Columns) > 0`):
   - 기존의 `prog.WireColMeta` 기반 고속 제로 할당 캐시 경로를 유지하여 0.5µs 단일 쿼리 지연시간에 영향이 없도록 보장.
3. 결과 집합이 없는 DML / 프로시저 실행:
   - 기존의 `WriteReturnStatusDirect` + `TokenDoneProc` 경로 유지.

---

## 5. TDD 검증 계획 (Verification)
1. **Red Test**: `test/spec_defects/spec_defect_tds_rpc_multiset_test.go`에서 `sp_dual_rpc_dataset`을 RPC(`sql.Named`)로 호출하여 `rows.NextResultSet()`이 `false`로 실패함을 확인. (확인 완료)
2. **Green Test**: 엔진 수정 후 동일 테스트가 통과하여 1번째, 2번째 결과 집합의 컬럼 및 데이터가 정상 수신됨을 확인.
3. **Regression Tests**:
   - `pkg/wire/tds/...` 전체 테스트 스위트 무결성 검증.
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql` 빌드 무결성 검증.
4. **Changelog & Tasks 동기화**: `CHANGELOG.md` 및 `TASKS.md` 갱신.
