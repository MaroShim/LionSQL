# 저장 프로시저 호출 시 미선언 명명 파라미터(Error 8145) 및 과다 인자(Error 8144) 엄격 검증 계획서
(`notes/plans/FIX_EXEC_UNKNOWN_NAMED_ARG_PLAN.ko.md`)

> **문서 상태**: Completed (구현 완료 및 검증 통과)  
> **태스크 ID**: `FIX-EXEC-UNKNOWN-NAMED-ARG-1`  
> **마일스톤**: `1.0-GA` (T-SQL Proc & TDS Error Parity)  
> **관련 규칙**: `[RULE:ExplicitError]`, `[RULE:SafeDefaults]`, `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`

---

## 1. 추진 배경 및 결함 분석 (Problem Statement)

### 1.1 결함 현상
1. Microsoft SQL Server에서 프로시저 호출 시 프로시저에 선언되지 않은 명명 파라미터(`EXEC sp_test @unknown = 1`)를 전달할 경우 **Error 8145 (`@%s is not a parameter for procedure %s.`)**가 발생해야 합니다. 그러나 기존 구현에서는 미선언 인자를 조용히 무시하거나 기본값으로 처리하여 클라이언트 버그가 은폐되는 문제가 있었습니다.
2. 선언된 파라미터 개수를 초과하여 위치 기반 인자를 전달할 경우 **Error 8144 (`Procedure or function %s has too many arguments specified.`)**가 발생해야 하나, 이를 검증하지 않고 초과 인자를 무시하는 결함이 있었습니다.
3. TDS 와이어 프로토콜 RPC 호출(`RPCRequest`) 시에도 Error 8145 및 8144 토큰(`0xAA ERROR`)과 `DONE_ERROR` 패킷이 정상 전송되어야 클라이언트 드라이버(ADO.NET, mssql-jdbc 등)가 예외를 올바르게 처리할 수 있습니다.

---

## 2. 해결 목표 (Target Architecture)

1. **SQL 텍스트 실행기(`sqlexec_procs.go`)에서의 엄격한 인자 검증**:
   - 명명 인자(`@param = val`) 바인딩 시 프로시저 메타데이터(`procMeta.Params`)에 일치하는 대상이 없으면 Error 8145 반환.
   - 위치 기반 인자(`val1, val2, ...`)가 `len(procMeta.Params)`를 초과하면 Error 8144 반환.
2. **TDS 와이어 프로토콜 VDBE 고속 경로 및 에러 매핑 동기화 (`wire/tds/server.go`)**:
   - TDS 바이너리 파라미터 바인딩 시 `prog.Params`와 비교하여 미선언 명명 파라미터 감지 시 TDS `BuildError(8145, 1, 16, ...)` 및 `BuildDoneProc(DoneFinal|DoneError, 0, 0)` 즉각 응답.
   - `extractTdsErrorInfo`에 8145 및 8144 에러 번호 매핑 등록.

---

## 3. 세부 구현 내용

### 3.1 `pkg/engine/sqlexec_procs.go`
```go
if targetParam == "" {
    return nil, fmt.Errorf("Error 8145, Level 16, State 1, Procedure %s: @%s is not a parameter for procedure %s.", procName, k, procName)
}
...
} else {
    return nil, fmt.Errorf("Error 8144, Level 16, State 2, Procedure %s: Procedure or function %s has too many arguments specified.", procName, procName)
}
```

### 3.2 `pkg/wire/tds/server.go`
- VDBE 프리페어드 프로시저 호출 시 바인딩 파라미터 매칭 검증 및 8145 토큰 전송.
- `extractTdsErrorInfo`에 에러 번호 8145 및 8144 추가.

---

## 4. 검증 결과 (`pkg/engine/proc_bind_test.go`)

- `TestProc_UnknownNamedParam_Error8145`: 미선언 명명 파라미터 전달 시 Error 8145 정상 발생 및 에러 메시지 검증.
- `TestProc_ExcessArguments_Error8144`: 선언된 인자 개수 초과 전달 시 Error 8144 정상 발생 검증.
- `pkg/engine` 및 `pkg/wire/tds` 전체 단위 테스트 100% PASS.
