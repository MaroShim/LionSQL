# Architecture Plan: 저장 프로시저 명명된 매개변수(Named Parameter) 바인딩 및 기본값 매핑 결함 수정

> - **작성일**: 2026-11-22
> - **과제 식별자**: `DEFECT-PROC-NAMED-PARAM-BINDING-1`
> - **관련 불변식 및 룰**:
>   - `[RULE:PipelineVerification]`: 빌드 ➔ 테스트(`go test -v -race ./...`) ➔ 커밋 3단계 파이프라인
>   - `[RULE:ZeroOracleTampering]`: 기존 SLT 및 오라클 테스트 보존 불변식
>   - `[RULE:ExplicitError]`: 잘못된 매개변수 명 전달 시 명시적 진단 에러 출력
>   - `[RULE:RadicalKISS]`: 불필요한 레이어 없이 컴파일러-VM 간 간결한 이름 매핑 슬롯 일원화

---

## 1. 개요 및 결함 분석 (Defect Analysis)

### 1.1. 현상 (Symptoms)
저장 프로시저 호출문(`EXEC ProcName @param1 = val1, @param2 = val2`)을 실행할 때, 호출자가 명시한 매개변수 이름(`@param1`, `@param2`)이 파서 단계에서 완전히 버려지고, VDBE 런타임에서 프로시저 정의 시점의 파라미터 선언 순서대로 단순 인덱스(`0, 1, 2...`) 매핑(Positional Binding)되는 결함이 발견되었습니다.

### 1.2. 영향 (Impact)
1. **순서 변경 호출 시 데이터 뒤바뀜**:
   `EXEC sp_test @b = 5, @a = 3;` 호출 시, 프로시저 선언이 `@a, @b` 순서라면 `@a`에 5가 들어가고 `@b`에 3이 들어가 로직이 반대로 연산됨.
2. **기본값(Default)을 가진 앞선 매개변수 건너뛰기 실패**:
   `CREATE PROCEDURE sp_test (@a INT = 1, @b INT = 2)`에 대해 `EXEC sp_test @b = 5;`를 호출하면, `@b`에 5가 들어가야 함에도 불구하고 첫 번째 매개변수인 `@a`에 5가 들어가고 `@b`는 기본값 2로 남아 의도치 않은 연산 오류 발생.
3. **OUTPUT 매개변수 이름 매핑 오류**:
   `EXEC sp_test @out_b = @var OUTPUT`과 같이 순서가 다른 OUTPUT 매개변수 지정 시 호출자의 변수 슬롯에 엉뚱한 출력 값이 기록됨.

### 1.3. 근본 원인 (Root Cause)
1. **`pkg/proc/parser_stmt.go` (`parseExec`)**:
   `arg := p.parseExpression(precLowest)` 수행 후, `bin.Left`에 담긴 매개변수 이름(`@b`)을 확인만 하고 버린 뒤 `arg = bin.Right`만 `args` 슬라이스에 추가함.
2. **`pkg/proc/ast.go` (`ExecStmt`)**:
   `ArgNames []string` 필드가 없어 인자별 매개변수 이름이 AST에 보존되지 않음.
3. **`pkg/proc/bytecode.go` (`CallData`) & `pkg/proc/compiler.go`**:
   `OpCallProc` 바이트코드 명령어의 메타데이터인 `CallData`에 `ArgNames []string`이 누락되어 있음.
4. **`pkg/proc/runtime/vm.go` (`callProc`)**:
   `for i, p := range prog.Params` 루프에서 오직 `argRegs[i]`만을 인덱스로 참조하여 호출 인자를 바인딩함.

---

## 2. 해결 설계 및 아키텍처 (Design Specification)

```
[SQL / EXEC statement]
  EXEC sp_calc @b = 5, @a = 3
        │
        ▼ (1. Parser: pkg/proc/parser_stmt.go)
  ExecStmt {
      ProcName: "sp_calc",
      Args:     [5, 3],
      ArgNames: ["@b", "@a"],
  }
        │
        ▼ (2. Compiler: pkg/proc/compiler.go)
  OpCallProc with CallData {
      Name:     "sp_calc",
      ArgRegs:  [r1, r2],
      ArgNames: ["@b", "@a"],
  }
        │
        ▼ (3. Runtime: pkg/proc/runtime/vm.go - callProc)
  Match target parameter:
    - If ArgNames[k] != "":
        Resolve param by name (case-insensitive, trim '@')
    - Else (positional):
        Resolve param by position k
  Bind into params[param.Name]
  Bind outSlots to correct parameter definition index
```

### 2.1. AST 계층 (`pkg/proc/ast.go`)
- `ExecStmt` 구조체에 `ArgNames []string` 필드 추가.
- `String()` 메서드에서 `ArgNames[i]`가 존재하는 경우 `@name = value` 포맷으로 렌더링하도록 갱신.

### 2.2. 파서 계층 (`pkg/proc/parser_stmt.go`)
- `parseExec()` 함수에서 인자가 `BinaryExpr` (`TokenEq` 또는 `TokenAssign`)인 경우:
  `bin.Left`가 `VariableExpr`이면 그 이름(예: `"@b"`)을 `argNames` 슬라이스에 저장.
  이름이 없는 위치 지정 인자(Positional)인 경우 빈 문자열 `""`을 저장.
- `&ExecStmt{..., ArgNames: argNames}`에 전달.

### 2.3. 바이트코드 및 컴파일러 계층 (`pkg/proc/bytecode.go`, `pkg/proc/compiler.go`)
- `CallData` 구조체에 `ArgNames []string` 필드 추가.
- 컴파일러 `case *ExecStmt:`에서 `CallData{..., ArgNames: s.ArgNames}`로 복사 전달.

### 2.4. VDBE 런타임 계층 (`pkg/proc/runtime/vm.go`)
- `callProc` 함수 시그니처 갱신 (또는 `CallData` 전달 구조 확인):
  `callProc(callData *proc.CallData)` 형태로 정돈하거나 `argNames []string` 인자 전달.
- 프로시저 파라미터 매핑 알고리즘:
  1. `prog.Params`의 이름 인덱스 맵(`paramIndexByName map[string]int`) 구성.
  2. 각 호출 인자 `k (0 ~ len(argRegs)-1)`에 대해:
     - `argNames[k] != ""`인 경우: 이름으로 타겟 파라미터 인덱스 조회. 매칭되는 파라미터가 없으면 명시적 에러(`procedure %s has no parameter named %s`) 반환.
     - `argNames[k] == ""`인 경우: 위치 인덱스 `k`로 타겟 파라미터 매핑.
     - 전달된 값 레지스터(`argRegs[k]`)를 `params[targetParam.Name]`에 대입.
  3. 기본값 처리:
     - `prog.Params` 중 호출 인자에 매핑되지 않은 파라미터는 `subFrame.bindParams`에서 기존 기본값 로직에 따라 자동 처리됨.
  4. OUTPUT 변수 반환 매핑:
     - 인자 위치 `k`의 `outSlots[k]`를 타겟 파라미터의 변수 슬롯에 정확하게 연결하여 역방향 쓰기(Writeback) 수행.

---

## 3. TDD 구현 및 검증 파이프라인 (Execution Steps)

1. **Step 1: TDD Red Test 작성**
   - 파일: `test/spec_defects/spec_defect_proc_named_params_test.go`
   - 케이스 A: 매개변수 순서 역전 호출 (`@b = 5, @a = 3`)
   - 케이스 B: 기본값 건너뛰기 호출 (`@b = 5`)
   - 케이스 C: 명명된 OUTPUT 매개변수 호출 (`@out_val = @result OUTPUT`)
   - 케이스 D: 존재하지 않는 매개변수 명 전달 시 에러 검증
2. **Step 2: 코어 엔진 패치 적용**
   - `pkg/proc/ast.go`
   - `pkg/proc/parser_stmt.go`
   - `pkg/proc/bytecode.go`
   - `pkg/proc/compiler.go`
   - `pkg/proc/runtime/vm.go` 및 `vm_dispatcher.go`
3. **Step 3: Green Test 검증**
   - `go test -v ./test/spec_defects -run TestSpecDefect_ProcNamedParams` 성공 확인.
4. **Step 4: 전체 회귀 및 레이스 디텍터 검증**
   - `go test -v -race ./pkg/proc/...`
   - `go test -v -race ./pkg/engine/...`
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
5. **Step 5: 백로그 및 커밋**
   - `TASKS.md` 등록 및 Conventional Commit 완료.
