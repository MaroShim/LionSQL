# 프로젝션 스칼라 서브쿼리 내부 에러 삼킴(Silent Drop) 수정 및 유효성 검증 아키텍처 플랜 (`notes/plans/SUBQUERY_PROJ_ERROR_PROPAGATION_PLAN.ko.md`)

## 1. 개요 및 결함 진단

### 현상
- 쿼리: `SELECT (SELECT 1 FROM t_sub1 WHERE no_such_col = t_sub2.val) FROM t_sub2;`
- `no_such_col`은 `t_sub1`과 `t_sub2` 어디에도 존재하지 않는 가짜 컬럼임에도 불구하고, 에러(`column "no_such_col" does not exist`)를 발생시키지 않고 `NULL`을 반환하며 정상 성공 처리되는 결함 발견.

### 왜 지금까지 발견되지 않았는가? (Root Cause of Undetection)
1. **기존 테스트 스위트의 편향**:
   - 기존의 SQLLogicTest 및 Babelfish 회귀 테스트 스위트는 올바른 쿼리(Happy Path)와 문법 결함(Syntax Error) 검증 위주로 구성되어 있었음.
   - "프로젝션 위치의 상관 서브쿼리 내부에 존재하지 않는 가짜 컬럼을 넣었을 때의 부정 검증(Negative Semantic Test)" 케이스가 오라클 테스트셋에 누락되어 있었음.
2. **반환 시그니처와 에러 처리 불일치 (`[RULE:ExplicitError]` 위반)**:
   - `pkg/execution/eval.go`의 `EvalTupleExprWithContext`는 `(any, bool)` 시그니처를 가짐.
   - `default:` 블록(lines 444-450)에서는 비컬럼 표현식 에러 시 `return err, true`로 에러 객체를 명시적으로 전달하도록 되어 있었으나,
   - `case *proc.ScalarSubqueryExpr:` 블록(lines 425-429)에서는 관례적인 Go 불리언 실패 패턴으로 착각하여 `return nil, false`로 반환함.
   - 상위 호출자인 `pkg/execution/project.go`는 `ok == true`일 때 `res.(error)`를 검사하도록 설계되어 있었으나, `ok == false`가 반환되자 해당 서브쿼리를 '일반 컬럼명 식별자'로 오판하고 튜플에서 찾다가 없으니 `NULL`을 채우고 성공 처리함.

---

## 2. 해결 방안

### 2.1 `pkg/execution/eval.go` 에러 전파 수정
- `case *proc.ScalarSubqueryExpr:`에서 `proc.EvalRowExpr`가 에러를 반환했을 때 `return err, true`를 반환하도록 수정:
  ```go
  case *proc.ScalarSubqueryExpr:
      ...
      val, err := proc.EvalRowExpr(e, evalMap, execSubquery)
      if err != nil {
          return err, true
      }
      return val, true
  ```
- 이를 통해 `pkg/execution/project.go:244`의 `if err, isErr := res.(error); isErr` 블록으로 에러가 즉시 전달되어 쿼리 실행이 Fail-Closed로 즉각 중단되고 사용자에게 정확한 오류 메시지가 전달됨.

### 2.2 회귀 테스트 스위트 추가
- `pkg/engine/subquery_err_test.go`:
  - 프로젝션 스칼라 서브쿼리 내부 가짜 컬럼(`no_such_col`) 참조 시 명확한 에러 발생 검증.
  - WHERE 절 서브쿼리 내부 가짜 컬럼 참조 시 에러 검증.
  - 정상적인 상관 서브쿼리는 기존과 100% 동일하게 정상 동작함을 대조 검증.

---

## 3. 검증 계획 (3-Stage Pipeline per [RULE:PipelineVerification])

1. **Stage 1 (Build)**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Stage 2 (Test & Race)**:
   - `go test -v -race ./pkg/execution/... ./pkg/engine/...`
   - SQLLogicTest 코어 스위트 검증 (`./bin/sqllogictest -dir test/slt/core -parallel 16`)
   - 오라클 불변성 검증 (`bash scripts/verify_no_slt_mutation.sh`)
3. **Stage 3 (Commit & Push)**:
   - Conventional Commits, 데코레이션 이모지 배제 per `[RULE:StyleIntegrity]`.
   - sprint DB 완료 처리 및 `TASKS.md` 동기화 per `[RULE:ContinuousBacklog]`.
