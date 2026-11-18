# T-SQL 세션 변수 텍스트 치환 전면 폐지 및 VDBE 레지스터 직접 바인딩 아키텍처 계획서 및 최종 구현 명세서 (Living SSOT)
(`notes/plans/REFACTOR_TSQL_SESSION_VARS_VDBE_PLAN.ko.md`)

> **문서 상태**: Completed (구현 및 검증 완료, Living SSOT 동기화)  
> **태스크 ID**: `REFACTOR-TSQL-SESSION-VARS-VDBE-1`  
> **마일스톤**: `v0.94.0`  
> **관련 규칙**: `[RULE:RadicalKISS]`, `[RULE:ModularAbstraction]`, `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`, `[RULE:ZeroOracleTampering]`, `[RULE:PipelineVerification]`

---

## 1. 추진 배경 및 문제 의식 (Problem Statement)

### 1.1 레거시 텍스트 치환 아키텍처의 한계 (`pkg/engine/sqlexec.go:1700-1732`)
과거 LionSQL은 `ClientSession.Vars`에 저장된 T-SQL 세션 변수(`@var`)를 처리할 때, 쿼리가 AST 파서나 바이트코드 컴파일러에 전달되기 전에 **문자열 치환기(`substituteParamOutsideQuotes`)**를 통해 프리프로세싱하고 있었습니다.

```go
// [과거 레거시 코드: pkg/engine/sqlexec.go:1700-1732] - 전면 삭제 완료
// 0.3 Substitute session variables in SQL query if present
rawBeforeVarSub := trimmed
cs.Mu.Lock()
if len(cs.Vars) > 0 {
    for _, k := range keys {
        v := cs.Vars[k]
        ...
        trimmed = substituteParamOutsideQuotes(trimmed, k, varRep)
    }
}
cs.Mu.Unlock()
```

### 1.2 텍스트 치환으로 인해 발생했던 4대 구조적 결함 (완전 박멸)
1. **L-Value(대입문) 문맥 파괴**:
   - 세션에 이미 `@id = 1`이 존재하는 상태에서 `SELECT @id = id FROM accounts WHERE balance = 2000`이 실행되면, 파싱 전에 `SELECT 1 = id FROM ...`로 치환되어 변수 대입문이 컬럼 비교 표현식으로 왜곡되거나 깨짐.
2. **프로시저 반환 코드 및 OUTPUT 매개변수 파괴**:
   - 세션에 `@rc = 0`이 있을 때 `EXEC @rc = sp_proc`를 호출하면 `EXEC 0 = sp_proc`로 치환되어 문법 에러가 발생함.
   - 이를 회피하기 위해 `rawBeforeVarSub`라는 임시 변수를 두어 `execProcedureCallWithRaw`로 전달하는 취약한 땜질 코드가 양산됨.
3. **플랜 캐시(Plan Cache) 무력화 및 CBO 최적화 부하 폭증**:
   - 동일한 쿼리 템플릿이라도 변수 값에 따라 매번 서로 다른 SQL 텍스트로 치환되므로 파라미터화된 쿼리 계획(Parameterized Plan) 캐시 재사용이 불가능해져 CPU가 낭비됨.
4. **타입 정밀도 손실(Type Fidelity Loss) 및 이스케이프 왜곡**:
   - Go 내부 값을 `fmt.Sprintf("%v", val)` 및 따옴표 이스케이프(`''`)로 재직렬화하므로, `DATETIME2` 나노초 정밀도, `VARBINARY` 바이트열, `FLOAT` 부동소수점 오차, 특수 제어 문자 등에서 데이터 손실과 변형이 발생함.

---

## 2. 목표 및 최종 구현 아키텍처 (Living Architecture)

```mermaid
flowchart TD
    A["클라이언트 SQL: SELECT name FROM t WHERE id = @id"] --> B["파서 (Pratt / ANTLR4)"]
    B -->|Context-Aware AST| C["AST: VariableExpr(Name: '@id')"]
    D{"실행 엔진 분기"}
    C --> D
    D -->|Fast-Path VDBE| E["VDBE 바이트코드: OpLoadVar r1, slot_idx"]
    D -->|Volcano CBO| F["FilterIterator: EvalTupleBoolExprWithContext(..., sessionVars)"]
    G["ClientSession.Vars"] -->|Direct Slot Binding| E
    G -->|Direct O(1) Hash Lookup| F
```

1. **텍스트 치환 전면 폐지 (Zero Textual Preprocessing)**:
   - `sqlexec.go`의 `0.3 Substitute session variables` 블록 및 `rawBeforeVarSub` 전면 삭제.
   - `sqlexec_select.go`의 `substitutedFrom` 텍스트 치환 전면 삭제 (`fromClause` 원형 직결).
   - 쿼리 원형 문자열을 그대로 AST 파서와 VDBE 컴파일러로 전달.
2. **VDBE VM 레지스터 및 프레임 슬롯 직결 (`pkg/proc/runtime/vm.go`)**:
   - 바이트코드 컴파일러(`compiler.go:1312`): `VariableExpr`를 만났을 때 `OpLoadVar`, `OpStoreVar` 방출 및 슬롯 인덱스 할당.
   - 가상머신 `VMFrame.bindParams`: 2-패스 바인딩 도입 (Bare 키 먼저 바인딩 후, 정식 `@` 프리픽스 키가 최종 덮어써서 맵 순회 순서 비결정성 원천 박멸).
3. **Volcano CBO 엔진 직접 바인딩 (`pkg/execution/`)**:
   - `execution.ExecutorOptions`: `SessionVars map[string]any` 컨텍스트 필드 추가.
   - `execution.eval.go`: `EvalTupleExprWithContext` 및 `EvalTupleBoolExprWithContext` 신설. 행 평가 시 `VariableExpr`를 `sessionVars`에서 $O(1)$로 직접 조회.
   - `FilterIterator` & `ProjectionIterator`: `SetSessionVars` 지원 및 WHERE 절/SELECT 프로젝션 내 변수 평가 직결.
   - `execVolcanoSelect`: `cs.Vars` 스냅샷을 `opts.SessionVars`로 직결 바인딩.
4. **프로시저 호출 일원화 (`pkg/engine/sqlexec_procs.go`)**:
   - `execProcedureCallWithRaw` 우회 코드 폐지 ➔ 단일 `execProcedureCall`로 통합.
   - 프로시저 인자 파싱 시 `valStr`가 `@var`일 때 호출자 세션 변수에서 값 직접 바인딩.
   - 반환 코드(`returnVar`) 및 OUTPUT 바인딩(`outputBindings`) 정규화 저장.

---

## 3. Before & After 구조 비교 매트릭스

| 검증 항목 | Before (텍스트 치환 방식) | After (VDBE/CBO 직접 바인딩 방식) |
| :--- | :--- | :--- |
| **SQL 쿼리 전달** | `substituteParamOutsideQuotes`로 원시 텍스트 변형 | 원형 SQL 그대로 AST 파서 및 VDBE 컴파일러 전달 |
| **L-Value 대입문 (`SELECT @id = col`)** | 이미 존재하는 `@id` 값으로 치환되어 `SELECT 1 = col` 비교문 왜곡 버그 발생 | `VariableExpr` AST 노드로 유지되어 대상 변수에 정확히 값 대입 |
| **프로시저 반환 코드 (`EXEC @rc = proc`)** | `@rc`가 값으로 치환되어 `EXEC 0 = proc` 문법 에러 발생 ➔ `rawBeforeVarSub` 땜질 | 원형 그대로 파싱되어 프로시저 종료 후 `cs.Vars`에 정상 반영 |
| **Volcano CBO WHERE/프로젝션** | 텍스트 치환으로 매번 다른 SQL 생성 ➔ 플랜 캐시 무력화 | `VariableExpr` AST를 `sessionVars`에서 $O(1)$ 직접 바인딩, 플랜 캐시 완벽 보존 |
| **타입 정밀도 보존** | `fmt.Sprintf("%v", val)` 재직렬화로 정밀도 및 바이너리 손실 | Go 네이티브 타입(`int64`, `float64`, `time.Time`, `[]byte`) 원형 보존 |
| **코드베이스 청결성** | `rawBeforeVarSub`, `execProcedureCallWithRaw` 등 취약한 분기 양산 | 단일 `execProcedureCall` 및 표준 VDBE 슬롯 아키텍처로 단순화 (`[RULE:RadicalKISS]`) |

---

## 4. 검증 결과 (Verification Results)

1. **정밀 회귀 테스트 (`pkg/engine/session_var_vdbe_binding_test.go`)**:
   - `TestSessionVarVDBE_NoTextualCorruption`: 100% PASS (0.01s)
     - Volcano WHERE 필터 (`WHERE id = @id`) 정상 평가
     - 관계형 대입문 (`SELECT @id = id FROM accounts WHERE balance = 2000`) 정상 대입
     - 복합 산술 업데이트 (`SET @id = @id + 10`) 정상 반영
     - Volcano 프로젝션 (`SELECT @id AS current_id, name FROM accounts WHERE id = 1`) 정상 바인딩
     - 기 존재하는 `@rc = 0` 상태에서 `EXEC @rc = sp_sample_calc ...` 호출 시 반환 코드 99 및 OUTPUT 파라미터 50 정상 반환
2. **기존 세션 변수 및 프로시저 테스트 스위트**:
   - `TestSessionVar_ArithmeticExpressionEvaluation`: PASS
   - `TestSelectAssign_Tableless`: PASS
   - `TestSelectAssign_NoMixingRule`: PASS
   - `TestSelectAssign_WithFromClause`: PASS
   - `TestSelectAssign_RunningTotal`: PASS
   - `TestSelectAssign_InProcedure`: PASS
3. **동시성 및 레이스 검증**:
   - `go test -race ./pkg/engine ./pkg/execution ./pkg/proc/...`: 100% PASS (Data Races 0건)
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`: 빌드 무결성 확인 완료
