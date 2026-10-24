# LionSQL 아키텍처 추상화 및 구조적 리팩토링 명세 (REFACTORING.ko.md)

본 문서는 단순 대증적 패치(Whack-a-Mole)를 지양하고, 문제의 근본 원인을 제거하기 위해 추진되는 **아키텍처 추상화 계층 도입(Structural Refactoring & Deletion-First)**의 실행 계획과 검증 현황을 관리하는 전용 대시보드입니다.

---

## 1. 아키텍처 개편 핵심 원칙

1. **Radical KISS, DRY & Deletion-First (골든 룰 #15)**:
   - 파일 곳곳에 복사-붙여넣기된 수동 파싱 루프와 임의의 정규식을 단일 추상화 계층으로 흡수하고, 중복 코드를 과감히 삭제합니다.
2. **Modular Interface Abstraction (골든 룰 #19)**:
   - Lexer, DML Parser, Expression Evaluator를 독립적이고 재사용 가능한 모듈 계약(Contract)으로 분리합니다.
3. **Hermetic Zero-Panic Invariance (골든 룰 #2, #11)**:
   - 제로 나눗셈, 쿼테이션 이스케이프 왜곡, 정규식 매칭 실패를 추상화 계층 수준에서 100% 원천 방어합니다.
4. **3단계 검증 파이프라인 (3-Stage Verification Pipeline)**:
   - 모든 추상화 작업은 반드시 **Build ➔ Race-Free Test (`./...`) ➔ Conventional Commit**을 완수한 후 완료 처리합니다.

---

## 2. 상태 범례

- `[ ]` : 아키텍처 설계 완료 (착수 대기)
- `[~]` : 추상화 계층 구현 및 중복 코드 제거 진행 중
- `[x]` : 3단계 검증 통과 및 커밋 완료

---

## 3. 핵심 3대 구조적 추상화 과제

| ID | 우선순위 | 영역 | 목표 추상화 계층 | 제거 대상 (Deletion-First) | 상태 | 세부 내용 |
| :---: | :---: | :---: | :--- | :--- | :---: | :--- |
| **`ARCH-LEX-1`** | **P1** | Lexical | `pkg/sqlutil/tokenizer.go`<br>• `SplitTopLevelKeyword`<br>• `FindTopLevelArithmeticOp`<br>• `ExtractTopLevelTuples`<br>• `FindTopLevelKeyword`<br>• `SplitTopLevel` | • `sqlexec_eval.go:splitLogicalConditions`<br>• `sqlexec_eval.go:parseValueTuples`<br>• `sqlexec_eval.go:findArithmeticOp`<br>• `sqlexec_select.go:splitScalarProjections`<br>• `sqlexec_select.go:hasFromClause`<br>• `sqlexec_dml.go:findOnConflictIndex` | **`[x]`** | 작은따옴표(`''`), 백슬래시(`\'`), 큰따옴표(`""`), 대괄호(`[...]`), 중첩 괄호(`()`)를 완벽히 지원하는 단일 고속 상태 머신 스캐너 도입 및 300여 라인 수동 파싱 루프 전면 제거 완료 |
| **`ARCH-DML-1`** | **P1** | Parser | `pkg/engine/dml_parser.go`<br>• `DMLStatement`<br>• `ParseDMLCommand` | • `reDmlInsertValues`<br>• `reDmlInsertCheck`<br>• `reDmlInsertSelect`<br>• `reDmlUpsertPrefix`<br>• `reDmlUpdate`<br>• `reDmlUpdateCheck`<br>• `reDmlDelete`<br>• `reDmlDeleteCheck` | **`[x]`** | 8개의 취약한 DML 정규식을 전면 폐기하고, 토큰 기반 경량 1-Pass DML 구문 파서(`ParseDMLCommand`)로 일원화 완료 |
| **`ARCH-EVAL-1`** | **P1** | Evaluator | `pkg/engine/evaluator.go`<br>• `EvalBinaryOp`<br>• `CoerceAndCalculate`<br>• `EvalUpdateExpr`<br>• `ResolveOperand`<br>• `ToNumeric` | • `sqlexec_eval.go:evalUpdateExpr`<br>• `sqlexec_eval.go:toNumeric`<br>• `sqlexec_eval.go:resolveOperand`<br>• 산술 if-switch 분기 80+ 라인<br>• 미사용 `reEvalArithExpr` 정규식 | **`[x]`** | `pkg/storage/coerce.go` (Type Promotion Lattice)와 연동하여 float/int 강제 변환 왜곡 및 정수 0 나누기 패닉을 원천 봉쇄하는 통합 연산 엔진 구축 완료 |
| **`ARCH-LEX-2`** | **P2** | Lexical | `pkg/sqlutil/tokenizer.go`<br>• `FindAllTopLevelKeyword`<br>• `FindTopLevelChar`<br>• `FindMatchingParen` | • `sqlexec_merge.go:findTopLevelKeyword`<br>• `sqlexec_merge.go:findAllTopLevelKeyword`<br>• `sqlexec_string_split.go:findMatchingParen`<br>• `sqlexec_string_split.go:parseStringSplitArgs`<br>• `sqlexec_procs.go:splitExecParams`<br>• `sqlexec_procs.go:splitConcatenationOutsideQuotes`<br>• `sqlexec_procs.go:findTopLevelEquals`<br>• `query_utils.go:SplitDeclarations` | **`[x]`** | MERGE, STRING_SPLIT, PROCS, QUERY_UTILS 내 잔여 수동 분할 루프 및 인용부호 스캐너 200여 라인 전면 삭제 및 `sqlutil.Tokenizer` 표준 API 일원화 완료 |

---

## 4. 실행 로드맵 및 진행 기록

1. **Phase 1: `ARCH-LEX-1` (SQL Scanner 추상화)** ➔ **완료 (`[x]`)**
   - `pkg/sqlutil/tokenizer.go`: 범용 고속 토큰 스캐너 상태 머신 구현 (`SplitTopLevelKeyword`, `FindTopLevelArithmeticOp`, `ExtractTopLevelTuples`, `FindTopLevelKeyword`, `SplitTopLevel`).
   - `sqlexec_eval.go`, `sqlexec_dml.go`, `sqlexec_select.go`, `sqlexec_dbcc.go`, `sqlexec_merge.go` 내 6개소 수동 루프 300여 라인 전면 제거 및 대체.
   - 단위 테스트(`tokenizer_test.go`, `rev_eng_test.go`) 및 100% 레이스 프리 통과 검증 완수.
2. **Phase 2: `ARCH-DML-1` (DML Parser & Dispatcher 추상화)** ➔ **완료 (`[x]`)**
   - **사전 점검 (완료)**: 리팩토링 전 현재 동작을 고정하는 특성화 테스트 `pkg/engine/dml_characterization_test.go` 추가 (INSERT 25건, UPDATE 17건, DELETE 13건). 키워드 생략(`INTO`/`FROM`), 대소문자, 탭/개행, 대괄호/큰따옴표 식별자, 리터럴 내부 키워드/괄호/쉼표, 다중 행 VALUES, `OUTPUT`/`UPSERT`/`ON CONFLICT`/`INSERT...SELECT`(정규식 전용 경로) 및 에러 케이스를 포함합니다. 새 파서는 이 테스트를 그대로 통과해야 합니다.
   - **사전 점검 발견 사항**: `reDmlUpdate`가 `SET` 절을 지연 매칭(`.*?`)하여 문자열 리터럴 내부의 `WHERE`에서 절을 분할하고, 에러 없이 0건 갱신 후 성공을 반환함 (`REV-ENG-006`, `OUTPUT` 경로 한정). 해당 케이스는 `knownBug`로 추적 중이며 `ARCH-DML-1` 완료 시 마커를 제거해야 테스트가 통과합니다.
   - `pkg/engine/dml_parser.go` 신설: 키워드 생략, 공백 변형에 면역인 1-Pass DML 디스패처 구축.
   - 복잡한 10여 개 DML 정규식 전면 삭제.
3. **Phase 3: `ARCH-EVAL-1` (통합 산술 및 연산자 평가 계층)** ➔ **완료 (`[x]`)**
   - `pkg/engine/evaluator.go` 신설: `EvalBinaryOp`, `CoerceAndCalculate`, `ToNumeric`, `ResolveOperand`, `EvalUpdateExpr` 구축.
   - `pkg/storage/coerce.go` Type Promotion Lattice(Decimal > Float > Int) 연동 및 0 나누기 방어 가드 일원화.
   - `sqlexec_eval.go` 내 산재해 있던 중복 연산자 분기 120여 라인 및 미사용 정규식 `reEvalArithExpr` 전면 삭제.
   - 단위 테스트 `evaluator_test.go` 작성 및 100% 검증 통과.
4. **Phase 4: `ARCH-LEX-2` (잔여 파서 `tokenizer.go` 전면 일원화 및 중복 루프 제거)** ➔ **완료 (`[x]`)**
   - `pkg/sqlutil/tokenizer.go`: `FindAllTopLevelKeyword`, `FindTopLevelChar`, `FindMatchingParen` 공통 API 확충 및 단위 테스트 완비.
   - `sqlexec_merge.go`: 자체 구현되었던 `findTopLevelKeyword`, `findAllTopLevelKeyword`, `isWordBoundary` 등 50여 라인 삭제 및 `sqlutil` 대체.
   - `sqlexec_string_split.go`: 40여 라인 수동 파싱 루프 삭제 및 `sqlutil.SplitTopLevel`, `sqlutil.FindMatchingParen` 연동.
   - `sqlexec_procs.go`: `splitExecParams`, `splitConcatenationOutsideQuotes`, `findTopLevelEquals` 등 120여 라인 수동 인용부호 스캐너 삭제 및 `sqlutil` 일원화.
   - `query_utils.go`: `SplitDeclarations` 50여 라인 루프 삭제 및 `sqlutil.SplitTopLevel` 일원화.
   - 총 250여 라인의 취약한 중복 인용부호/괄호 분할 루프를 전면 제거하고 단일 토크나이저 상태머신으로 통일.
